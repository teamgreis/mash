"""Async :class:`Connection` for MASH transport.

A Connection wraps a TLS-secured :class:`asyncio.StreamReader` /
:class:`asyncio.StreamWriter` pair plus the length-prefixed framer from
:mod:`mash_py.wire.framing`. It runs a read loop that:

1. Reads one frame per iteration.
2. Classifies it via :func:`mash_py.wire.peek.peek_message_type`. The
   classifier looks for the DEC-083 CBOR tag-77 wrapper to distinguish
   control frames; everything else is application-layer.
3. For control frames: handles Ping (send Pong), Pong (notify
   keep-alive hook), Close (send Close ack and shut down).
4. For application frames: forwards bytes to ``handler.on_message``.

The connection lifecycle (DISCONNECTED → CONNECTING → CONNECTED → CLOSING →
DISCONNECTED) mirrors mash-go ``pkg/transport/connection.go`` but is
intentionally simpler — Python's asyncio gives us a single event loop to
serialise everything, so we don't need atomics or fine-grained mutexes.

This module is the application-layer endpoint. The TLS handshake and ALPN
verification happen in :func:`accept` (server) or :func:`connect` (client),
both of which return a fully-set-up Connection ready to ``start()``.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import ssl
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from enum import Enum
from typing import Protocol

from mash_py.transport.alpn import ALPN_COMMISSIONING, ALPN_OPERATIONAL
from mash_py.transport.tls import TLSConfigError, verify_alpn, verify_tls13
from mash_py.wire.codec import (
    decode_control_message,
    encode_control_message,
)
from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    FramingError,
)
from mash_py.wire.operations import ControlMessageType, MessageType
from mash_py.wire.payloads import ControlMessage
from mash_py.wire.peek import peek_message_type

_log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Public state / errors
# ---------------------------------------------------------------------------


class ConnectionState(Enum):
    """Lifecycle state of a Connection.

    Mirrors ``ConnectionState`` in mash-go ``pkg/transport/connection.go:17-31``,
    minus the ``CONNECTING`` intermediate (asyncio gives us atomic transitions
    via ``await`` rather than a CAS-style state machine).
    """

    DISCONNECTED = "DISCONNECTED"
    CONNECTED = "CONNECTED"
    CLOSING = "CLOSING"


class ConnectionClosedError(Exception):
    """Raised when an operation is attempted on a closed connection."""


class ConnectionVerificationError(Exception):
    """Raised when TLS / ALPN post-handshake verification fails."""


# ---------------------------------------------------------------------------
# Handler protocol
# ---------------------------------------------------------------------------


class ConnectionHandler(Protocol):
    """Callbacks the Connection invokes from its read loop.

    All methods are async so handlers may freely await — sending replies,
    journaling to disk, etc. The Connection serialises calls (one at a time)
    so handlers do not need their own concurrency control.
    """

    async def on_message(self, data: bytes) -> None:
        """Handle one application-layer frame (Request / Response /
        Notification CBOR bytes — handler decodes per its needs)."""

    async def on_close(self, exc: BaseException | None) -> None:
        """Notification that the read loop has exited.

        ``exc`` is ``None`` for a clean close (peer or local Close
        control message), or a framing / OS exception for an abnormal
        close. Called exactly once per Connection.
        """


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------


@dataclass
class ConnectionConfig:
    """Tunables for a Connection.

    Defaults track ``docs/transport.md §6`` (keep-alive) and DEC-069 (8 KB
    max message size).
    """

    max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE
    """Per DEC-069 (8 KB). Frames larger than this on read trigger
    :class:`mash_py.wire.framing.MessageTooLargeError`, which terminates the
    read loop."""

    close_timeout: float = 5.0
    """Maximum seconds to wait for a Close acknowledgment from the peer
    after sending one (``docs/transport.md §10.2``)."""

    on_pong: Callable[[int], None] | Callable[[int], Awaitable[None]] | None = field(
        default=None
    )
    """Optional callback for Pong frames; receives the echoed sequence
    number. Phase-X keep-alive driver registers here. May be sync or
    async."""


# ---------------------------------------------------------------------------
# Connection
# ---------------------------------------------------------------------------


class Connection:
    """A live MASH connection: TLS, framing, control-message handling.

    The constructor takes already-handshaked StreamReader/StreamWriter
    objects plus the negotiated ALPN protocol. Use :func:`connect` /
    :func:`accept` factory functions to perform the TLS handshake and
    return a Connection.

    Lifecycle::

        conn = await connect(...)        # state == DISCONNECTED
        conn.start(handler)              # state -> CONNECTED, read loop running
        await conn.send(frame)           # arbitrary application sends
        await conn.close()               # state -> CLOSING -> DISCONNECTED
    """

    def __init__(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
        alpn: str,
        *,
        config: ConnectionConfig | None = None,
        peer_addr: tuple[str, int] | None = None,
    ) -> None:
        self._reader = reader
        self._writer = writer
        self._alpn = alpn
        self._config = config if config is not None else ConnectionConfig()
        self._peer_addr = peer_addr

        self._frame_reader = AsyncFrameReader(reader, max_message_size=self._config.max_message_size)
        self._frame_writer = AsyncFrameWriter(writer, max_message_size=self._config.max_message_size)

        self._state = ConnectionState.DISCONNECTED
        self._read_task: asyncio.Task[None] | None = None
        self._handler: ConnectionHandler | None = None
        self._closed = False

        # Signalled by the read loop when it observes a Close from the peer.
        # close() awaits this (with timeout) after sending its own Close.
        self._peer_close_event = asyncio.Event()

    # ------------------------------------------------------------------
    # Properties
    # ------------------------------------------------------------------

    @property
    def state(self) -> ConnectionState:
        return self._state

    @property
    def alpn(self) -> str:
        """ALPN protocol negotiated for this connection."""
        return self._alpn

    @property
    def is_commissioning(self) -> bool:
        return self._alpn == ALPN_COMMISSIONING

    @property
    def is_operational(self) -> bool:
        return self._alpn == ALPN_OPERATIONAL

    @property
    def peer_addr(self) -> tuple[str, int] | None:
        return self._peer_addr

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    def start(self, handler: ConnectionHandler) -> None:
        """Attach a handler and start the read loop.

        Raises :class:`ConnectionClosedError` if the connection has already
        been closed. May only be called once per Connection.
        """
        if self._state is not ConnectionState.DISCONNECTED:
            raise ConnectionClosedError(f"cannot start in state {self._state}")
        self._handler = handler
        self._state = ConnectionState.CONNECTED
        self._read_task = asyncio.create_task(
            self._read_loop(), name=f"mash-conn-read-{id(self):x}"
        )

    async def send(self, data: bytes) -> None:
        """Write one application-layer frame.

        Use :meth:`send_control` for Ping/Pong/Close. Concurrent sends are
        serialised by :class:`AsyncFrameWriter`'s lock so the prefix and
        payload of one frame are never interleaved with another.
        """
        if self._state is not ConnectionState.CONNECTED:
            raise ConnectionClosedError(f"cannot send in state {self._state}")
        await self._frame_writer.write_frame(data)
        # drain() respects the asyncio buffer high-water mark so we don't
        # let a slow peer balloon memory. mash-go's framer writes
        # synchronously through the TLS layer; this is the asyncio analogue.
        await self._writer.drain()

    async def send_control(self, msg_type: ControlMessageType, sequence: int = 0) -> None:
        """Send a ping/pong/close control message.

        See ``docs/transport.md §6`` (DEC-083 CBOR tag-77 wrapper).
        """
        if self._state is ConnectionState.DISCONNECTED:
            raise ConnectionClosedError("connection is disconnected")
        frame = encode_control_message(ControlMessage(type=msg_type, sequence=sequence))
        await self._frame_writer.write_frame(frame)
        await self._writer.drain()

    async def close(self) -> None:
        """Graceful close per ``docs/transport.md §10.2``.

        - If currently CONNECTED, sends a Close control message and waits
          up to :attr:`ConnectionConfig.close_timeout` for the peer to echo
          Close back (or for the read loop to terminate).
        - In any state, closes the underlying transport.

        Idempotent: a second call returns immediately.
        """
        if self._closed:
            return

        if self._state is ConnectionState.CONNECTED:
            self._state = ConnectionState.CLOSING
            with contextlib.suppress(Exception):
                await self.send_control(ControlMessageType.CLOSE)
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(
                    self._peer_close_event.wait(), timeout=self._config.close_timeout
                )

        await self._force_close()

    async def abort(self) -> None:
        """Tear down the transport without the graceful Close handshake.

        For unstarted connections (e.g. a listener rejecting on ALPN
        mismatch before :meth:`start` is called), this is the only valid
        teardown path because :meth:`close` requires CONNECTED state to
        send the Close control message.
        """
        if self._closed:
            return
        await self._force_close()

    async def _force_close(self) -> None:
        """Tear down the transport and read task without waiting for peer."""
        if self._closed:
            return
        self._closed = True
        self._state = ConnectionState.CLOSING

        # Closing the writer will EOF the reader; the read loop sees that
        # and exits. We then await the task to surface any handler errors.
        try:
            self._writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await self._writer.wait_closed()
        except Exception:
            _log.debug("error closing transport writer", exc_info=True)

        if self._read_task is not None:
            with contextlib.suppress(asyncio.CancelledError):
                await self._read_task

        self._state = ConnectionState.DISCONNECTED

    # ------------------------------------------------------------------
    # Read loop
    # ------------------------------------------------------------------

    async def _read_loop(self) -> None:
        """Read frames until EOF, peer Close, or unrecoverable error.

        Application frames are dispatched to ``handler.on_message``;
        control frames are answered inline (Ping->Pong, Close->Close-ack +
        shutdown). The handler's :meth:`ConnectionHandler.on_close` is
        invoked exactly once when the loop exits.
        """
        assert self._handler is not None
        exc: BaseException | None = None
        try:
            while True:
                try:
                    frame = await self._frame_reader.read_frame()
                except (asyncio.IncompleteReadError, EOFError):
                    # Clean EOF between frames — peer hung up without a
                    # Close message. Treat as expected end-of-stream.
                    break
                except (FramingError, ssl.SSLError, OSError) as e:
                    exc = e
                    _log.debug("read loop terminating: %r", e)
                    break

                msg_type = peek_message_type(frame)
                if msg_type is MessageType.CONTROL:
                    if await self._handle_control_frame(frame):
                        # Peer closed; stop reading.
                        break
                    continue

                # Application-layer frame.
                try:
                    await self._handler.on_message(frame)
                except Exception as e:
                    exc = e
                    _log.exception("handler.on_message raised; closing connection")
                    break
        finally:
            # Make sure the writer side is torn down even if we got here
            # via an exception path that did not call _force_close.
            self._peer_close_event.set()
            try:
                await self._handler.on_close(exc)
            except Exception:
                _log.exception("handler.on_close raised; ignoring")
            self._state = ConnectionState.DISCONNECTED

    async def _handle_control_frame(self, frame: bytes) -> bool:
        """Process a control frame. Returns True if the peer asked to close."""
        try:
            ctrl = decode_control_message(frame)
        except (ValueError, KeyError) as e:
            _log.warning("malformed control frame: %r", e)
            return False

        match ctrl.type:
            case ControlMessageType.PING:
                with contextlib.suppress(Exception):
                    await self.send_control(ControlMessageType.PONG, ctrl.sequence)
                return False

            case ControlMessageType.PONG:
                cb = self._config.on_pong
                if cb is None:
                    return False
                try:
                    result = cb(ctrl.sequence)
                    if asyncio.iscoroutine(result):
                        await result
                except Exception:
                    _log.exception("on_pong callback raised; ignoring")
                return False

            case ControlMessageType.CLOSE:
                # Acknowledge with our own Close, then signal we should exit.
                # Suppress because the peer may have already shut their side.
                with contextlib.suppress(Exception):
                    await self.send_control(ControlMessageType.CLOSE)
                self._peer_close_event.set()
                return True

        return False


# ---------------------------------------------------------------------------
# Handshake helpers
# ---------------------------------------------------------------------------


async def connect(
    host: str,
    port: int,
    ssl_context: ssl.SSLContext,
    *,
    config: ConnectionConfig | None = None,
    server_hostname: str | None = None,
) -> Connection:
    """Open a TLS connection and return a started-but-not-yet-running Connection.

    Caller still needs to call :meth:`Connection.start` with a handler. This
    split lets the caller wire the handler before the read loop begins, so
    no message can arrive before ``on_message`` is reachable.

    ``server_hostname`` is forwarded to :func:`asyncio.open_connection`. For
    MASH it is informational (we disable hostname verification — see
    ``mash_py.transport.tls`` docstring) but TLS 1.3 still requires SNI for
    interop with strict servers. Defaults to ``host``.
    """
    sni = server_hostname if server_hostname is not None else host
    reader, writer = await asyncio.open_connection(
        host=host,
        port=port,
        ssl=ssl_context,
        server_hostname=sni,
    )
    sslobj = _ssl_object(writer)
    if sslobj is None:
        writer.close()
        with contextlib.suppress(Exception):
            await writer.wait_closed()
        raise ConnectionVerificationError("transport is not TLS")

    try:
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
    except TLSConfigError as e:
        writer.close()
        with contextlib.suppress(Exception):
            await writer.wait_closed()
        raise ConnectionVerificationError(str(e)) from e

    peer = writer.get_extra_info("peername")
    return Connection(reader, writer, alpn, config=config, peer_addr=peer)


async def accept(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
    *,
    config: ConnectionConfig | None = None,
) -> Connection:
    """Wrap an already-accepted TLS stream into a Connection.

    For server-side use after :func:`asyncio.start_server` invokes the
    per-connection callback. The TLS handshake has already completed by
    the time the callback fires, so this is just verification + ctor.
    """
    sslobj = _ssl_object(writer)
    if sslobj is None:
        writer.close()
        with contextlib.suppress(Exception):
            await writer.wait_closed()
        raise ConnectionVerificationError("accepted transport is not TLS")

    try:
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
    except TLSConfigError as e:
        writer.close()
        with contextlib.suppress(Exception):
            await writer.wait_closed()
        raise ConnectionVerificationError(str(e)) from e

    peer = writer.get_extra_info("peername")
    return Connection(reader, writer, alpn, config=config, peer_addr=peer)


def _ssl_object(writer: asyncio.StreamWriter) -> ssl.SSLObject | None:
    """Pull the negotiated SSLObject out of an asyncio writer.

    asyncio surfaces the SSL state on the *transport* via
    ``get_extra_info('ssl_object')``. We use that for both
    :func:`verify_tls13` and :func:`verify_alpn` because :class:`SSLObject`
    is the closest analogue to mash-go's ``tls.ConnectionState``.
    """
    obj = writer.get_extra_info("ssl_object")
    if isinstance(obj, ssl.SSLObject):
        return obj
    return None


__all__ = [
    "Connection",
    "ConnectionClosedError",
    "ConnectionConfig",
    "ConnectionHandler",
    "ConnectionState",
    "ConnectionVerificationError",
    "accept",
    "connect",
]
