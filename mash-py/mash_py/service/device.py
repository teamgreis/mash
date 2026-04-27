"""Device-side operational service (Phase 15).

Hosts a :class:`mash_py.model.Device` behind the operational ALPN
``mash/1`` listener. Once commissioning has produced a Zone CA + an
operational cert + an operational key (Phase 14b output), the
:class:`DeviceService` listens on the operational port, accepts mTLS
connections from controllers in that zone, and dispatches every
inbound :class:`Request` through an :class:`OperationalServer` against
the underlying device model.

Phase 15 scope (per ``mash-py-tdd-plan.md``):

- One zone per service instance — multi-zone is Phase 19+.
- No commissioning listener — that's Phase 14 / 14b territory and runs
  on a separate ALPN. The device service is purely operational.
- No mDNS advertisement — Phase 18.
- No subscriptions (yet) — Phase 16.

What this gives us today:

1. After commissioning, the test (or a reference device binary) builds
   a :class:`DeviceService` with the persisted ``zone_ca / op_cert /
   op_key`` and the constructed device model.
2. ``await service.start(host, port)`` binds the operational TLS
   listener.
3. A Go controller (or a ``mash_py.interaction.InteractionClient``)
   dials, presents its zone-CA-signed cert, and starts issuing Read /
   Write / Invoke. Every Request flows through
   :meth:`OperationalServer.handle_request`.
4. ``await service.stop()`` tears everything down — closes the listener,
   waits for in-flight connections to terminate, releases sockets.

The operational TLS context built here is the asyncio analogue of
``mash-go`` ``pkg/transport.NewTLSConfigOperational`` — TLS 1.3,
``CERT_REQUIRED`` for mutual auth, ALPN restricted to ``mash/1``,
controller CA pinned to the supplied Zone CA.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Self

from cryptography import x509
from cryptography.hazmat.primitives.asymmetric.ec import EllipticCurvePrivateKey

from mash_py.interaction.server import OperationalServer
from mash_py.model import Device
from mash_py.transport.connection import (
    Connection,
    ConnectionConfig,
    ConnectionHandler,
    ConnectionVerificationError,
)
from mash_py.transport.connection import (
    accept as accept_connection,
)
from mash_py.transport.tls import make_operational_context
from mash_py.wire import (
    DEFAULT_MAX_MESSAGE_SIZE,
    MessageType,
    Request,
    Response,
    decode_request,
    encode_response,
    peek_message_type,
)

_log = logging.getLogger(__name__)


__all__ = [
    "DeviceService",
    "DeviceServiceConfig",
]


ConnectionAcceptCallback = Callable[[Connection], Awaitable[None]]
"""Optional hook invoked once per accepted operational Connection.

Phase 18 will use this to register the connection with the mDNS layer
(so the operational service knows when a controller is online). Phase 16
will use it to wire the per-connection :class:`SubscriptionManager`.
For Phase 15 the default is a no-op.
"""


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------


@dataclass
class DeviceServiceConfig:
    """Tunables for :class:`DeviceService`.

    Defaults follow ``docs/transport.md`` (DEC-069 8 KB max message,
    DEC-082 two-port pattern — except we only run the operational port
    in Phase 15) and the operational-side spec at ``security.md §5``.
    """

    max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE
    """Per DEC-069. Frames larger than this terminate the connection."""

    ssl_handshake_timeout: float = 30.0
    """Per-connection TLS handshake timeout (seconds)."""

    on_connection_accepted: ConnectionAcceptCallback | None = None
    """Optional hook called once per accepted Connection AFTER the TLS
    handshake completes and ALPN is verified. Useful for telemetry /
    Phase 18 hooks."""

    connection_config: ConnectionConfig | None = field(default=None)
    """Optional :class:`ConnectionConfig` overrides for keep-alive /
    close-timeout. Default lets :class:`Connection` pick its own."""


# ---------------------------------------------------------------------------
# DeviceService
# ---------------------------------------------------------------------------


class DeviceService:
    """Operational listener + dispatcher for one commissioned zone.

    Construction takes the artifacts that Phase 14b's
    :class:`mash_py.commissioning.CertExchangeServer` produces — the
    Zone CA cert, the operational cert/key — plus the
    :class:`Device` whose endpoints/features the controller will read
    and write.

    Lifecycle::

        svc = DeviceService(device, zone_ca, op_cert, op_key)
        await svc.start(host="0.0.0.0", port=0)   # 0 → kernel-assigned
        ...                                        # device runs
        await svc.stop()

    The bound port is exposed via :attr:`port` once :meth:`start` returns.
    """

    def __init__(
        self,
        device: Device,
        *,
        zone_ca: x509.Certificate,
        operational_cert: x509.Certificate,
        operational_key: EllipticCurvePrivateKey,
        config: DeviceServiceConfig | None = None,
    ) -> None:
        self._device = device
        self._zone_ca = zone_ca
        self._op_cert = operational_cert
        self._op_key = operational_key
        self._config = config if config is not None else DeviceServiceConfig()
        self._server = OperationalServer(device)

        self._listener: asyncio.Server | None = None
        self._port: int | None = None

        # Track in-flight Connections so stop() can wait for them.
        self._connections: set[Connection] = set()
        # Signalled by stop(); each per-client task watches this so a
        # connection that's still-in-handshake when stop() runs
        # drives its own close instead of wedging the listener's
        # wait_closed() (which in Python 3.12+ waits for every
        # attached accept task).
        self._stop_event = asyncio.Event()

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    @property
    def device(self) -> Device:
        return self._device

    @property
    def operational_server(self) -> OperationalServer:
        return self._server

    @property
    def port(self) -> int:
        if self._port is None:
            raise RuntimeError("DeviceService has not been started")
        return self._port

    @property
    def is_running(self) -> bool:
        return self._listener is not None

    async def start(self, *, host: str = "::", port: int = 0) -> None:
        """Bind the operational listener.

        ``host`` defaults to ``::`` (dual-stack); use ``"127.0.0.1"`` to
        restrict to loopback (interop tests do). ``port=0`` lets the
        OS assign a port — read it back via :attr:`port` after this
        coroutine returns.
        """
        if self._listener is not None:
            raise RuntimeError("DeviceService already running")

        ssl_ctx = make_operational_context(
            zone_ca=self._zone_ca,
            operational_cert=self._op_cert,
            operational_key=self._op_key,
        )

        self._listener = await asyncio.start_server(
            self._handle_client,
            host=host,
            port=port,
            ssl=ssl_ctx,
            ssl_handshake_timeout=self._config.ssl_handshake_timeout,
            start_serving=True,
        )
        self._port = _resolve_port(self._listener)

    async def stop(self) -> None:
        """Stop accepting new connections and close any active ones.

        Idempotent: a second call is a no-op. Each per-connection task
        watches :attr:`_stop_event` and closes its own connection when
        we signal here, which avoids a registration race: a
        ``_handle_client`` task that hasn't yet added its Connection
        to :attr:`_connections` would otherwise be missed by a
        snapshot-iterate-and-close pattern, leaving the listener's
        ``wait_closed`` to deadlock waiting for the still-attached
        accept task.
        """
        listener = self._listener
        if listener is None:
            return

        self._listener = None
        listener.close()

        # Tell every per-client task — including ones still mid-accept
        # — to close their connection. Each task closes its own conn
        # under the same event-loop tick that asyncio.Server tracks.
        self._stop_event.set()

        with contextlib.suppress(Exception):
            await listener.wait_closed()

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(self, *_exc: object) -> None:
        await self.stop()

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _handle_client(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        """Per-connection accept callback.

        After ``asyncio.start_server`` has completed the TLS handshake,
        this method:

        1. Verifies the connection (TLS 1.3, ALPN ``mash/1``).
        2. Wraps it in a :class:`Connection`.
        3. Attaches a :class:`_RequestHandler` and starts the read loop.
        4. Awaits the read loop's exit so the cap counter (Phase 18+
           addition) can release the slot at the right moment.

        Any exception below the verification point is logged and the
        socket torn down — the listener stays up.
        """
        try:
            try:
                conn = await accept_connection(
                    reader, writer, config=self._config.connection_config
                )
            except ConnectionVerificationError as exc:
                _log.warning("rejecting operational connection: %s", exc)
                return  # writer already closed by accept_connection

            self._connections.add(conn)
            try:
                if self._config.on_connection_accepted is not None:
                    with contextlib.suppress(Exception):
                        await self._config.on_connection_accepted(conn)

                handler = _RequestHandler(self._server, conn)
                conn.start(handler)

                # Wait for either the connection to close on its own
                # (peer drove it) OR the service to be stopping. In
                # the latter case we drive the close from here so the
                # listener's wait_closed sees this task complete.
                await self._wait_for_close_or_stop(conn, handler)
            finally:
                self._connections.discard(conn)
        except Exception:
            _log.exception("operational accept loop raised")

    async def _wait_for_close_or_stop(
        self,
        conn: Connection,
        handler: _RequestHandler,
    ) -> None:
        """Block until the connection's read loop exits OR :meth:`stop`
        is called. On stop, drive the connection close locally so the
        per-connection task can complete and the listener's
        ``wait_closed`` can return.
        """
        closed_task = asyncio.create_task(handler.wait_closed())
        stop_task = asyncio.create_task(self._stop_event.wait())
        try:
            done, pending = await asyncio.wait(
                {closed_task, stop_task},
                return_when=asyncio.FIRST_COMPLETED,
            )
            for task in pending:
                task.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await task

            if stop_task in done and closed_task not in done:
                # Stop arrived first. Close the connection — that drives
                # the read loop to exit, which sets handler._closed,
                # which lets us return.
                with contextlib.suppress(Exception):
                    await conn.close()
                with contextlib.suppress(Exception):
                    await handler.wait_closed()
        finally:
            for task in (closed_task, stop_task):
                if not task.done():
                    task.cancel()


# ---------------------------------------------------------------------------
# Per-connection request handler
# ---------------------------------------------------------------------------


class _RequestHandler(ConnectionHandler):
    """Decode + dispatch every Request frame on one Connection.

    Lives for the duration of one operational TLS connection. The read
    loop drives :meth:`on_message`; the dispatcher returns a Response
    that we encode and send back. Notification / Response frames are
    not expected on a *device-side* connection (the device is the
    server, the controller is the client) — we log and drop them.

    Per DEC-071 (write-then-notify ordering) the eventual Phase 16
    integration will need to dispatch any subscriptions touched by a
    Write *before* sending the Write Response. Since Phase 15 has no
    subscriptions, the simple synchronous flow is enough for now.
    """

    def __init__(self, server: OperationalServer, connection: Connection) -> None:
        self._server = server
        self._conn = connection
        self._closed = asyncio.Event()

    async def wait_closed(self) -> None:
        await self._closed.wait()

    async def on_message(self, data: bytes) -> None:
        try:
            msg_type = peek_message_type(data)
        except (ValueError, KeyError) as exc:
            _log.warning(
                "operational: dropping unparseable frame (%d bytes): %r",
                len(data),
                exc,
            )
            return

        if msg_type is not MessageType.REQUEST:
            # Devices are servers; they receive Requests, not Responses or
            # Notifications. A peer sending us one of those is a bug or a
            # Phase 16+ bidirectional extension we don't yet handle.
            _log.debug(
                "operational: ignoring frame of type %s on device-side connection",
                msg_type.name,
            )
            return

        try:
            request: Request = decode_request(data)
        except (ValueError, KeyError) as exc:
            _log.warning("operational: malformed Request: %r", exc)
            return

        response: Response = await self._server.handle_request(request)

        try:
            await self._conn.send(encode_response(response))
        except Exception:
            _log.exception(
                "operational: failed to send Response (msgId=%d, status=%s)",
                response.message_id,
                response.status.name,
            )

    async def on_close(self, exc: BaseException | None) -> None:
        if exc is not None:
            _log.debug("operational connection closed with error: %r", exc)
        self._closed.set()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _resolve_port(server: asyncio.Server) -> int:
    """Pull the actual bound port out of an asyncio.Server.

    Mirrors :func:`mash_py.service.listeners._resolve_port`; we
    duplicate to avoid pulling the listener module's TwoPortListener
    surface into Phase 15.
    """
    sockets = server.sockets
    if not sockets:
        raise RuntimeError("operational listener has no sockets")
    addr = sockets[0].getsockname()
    if isinstance(addr, tuple) and len(addr) >= 2 and isinstance(addr[1], int):
        return addr[1]
    raise RuntimeError(f"operational listener has unexpected sockname: {addr!r}")
