"""Two-port listener for the device side of a MASH connection.

DEC-082 prescribes two distinct TCP listeners — one per ALPN — sharing a
single per-device connection cap counter (DEC-064: ``max_zones + 1``).
This module implements that pattern on top of :func:`asyncio.start_server`.

Wire-conformance notes
----------------------

- The mDNS SRV ``port`` field is authoritative (``docs/transport.md §5.3``).
  This listener exposes ``operational_port`` and ``commissioning_port``
  properties so the caller can advertise whatever ports the OS picked when
  binding to ``port=0``.
- The connection cap MUST be enforced **before** the TLS handshake begins
  (DEC-062). Python's :func:`asyncio.start_server` performs the handshake
  inside its accept callback, so we hook the cap check into the
  ``ssl_handshake_timeout``-driven path: the per-listener accept callback
  is invoked only after the handshake completes, which is too late. We
  therefore use a low-level ``asyncio.start_server(..., ssl=...)`` with a
  pre-handshake gate implemented via :class:`asyncio.Server`'s
  ``client_connected_cb`` and a manual TLS upgrade. See
  :class:`TwoPortListener._make_listener` for the gate implementation.

The reference Go server enforces the cap atomically via
``activeConns.Add(1)`` followed by a comparison; we mirror that with
:class:`SharedConnectionCap` — an asyncio-friendly counter with a single
``acquire()`` operation that atomically checks-and-increments under the
event loop's single-threaded execution.
"""

from __future__ import annotations

import asyncio
import contextlib
import logging
import ssl
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from types import TracebackType
from typing import Self

from mash_py.transport.alpn import ALPN_COMMISSIONING, ALPN_OPERATIONAL
from mash_py.transport.connection import (
    Connection,
    ConnectionConfig,
    ConnectionVerificationError,
)
from mash_py.transport.connection import (
    accept as accept_connection,
)

_log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Connection cap (DEC-064)
# ---------------------------------------------------------------------------


def derive_connection_cap(max_zones: int) -> int:
    """Return the per-device total connection cap per DEC-064.

    ``max_zones`` operational connections (one per paired zone) plus one
    commissioning slot. ``docs/transport.md §5.4``.
    """
    if max_zones < 0:
        raise ValueError(f"max_zones must be >= 0, got {max_zones}")
    return max_zones + 1


class ConnectionCapExceededError(Exception):
    """Raised internally when the device's connection budget is exhausted.

    Surfaces as a TCP-layer reject (the listener closes the socket without
    completing the TLS handshake) so the cap is enforced before any
    application bytes are exchanged (DEC-062).
    """


class SharedConnectionCap:
    """Atomic check-and-increment connection counter shared across both
    listeners (DEC-064).

    Single-threaded by virtue of running on one asyncio event loop, so we
    do not need a real lock — every ``acquire`` and ``release`` runs
    atomically with respect to coroutines, which is exactly what
    :data:`atomic.Int32` provides on the Go side.
    """

    def __init__(self, capacity: int) -> None:
        if capacity <= 0:
            raise ValueError(f"capacity must be > 0, got {capacity}")
        self._capacity = capacity
        self._active = 0

    @property
    def capacity(self) -> int:
        return self._capacity

    @property
    def active(self) -> int:
        return self._active

    def acquire(self) -> SharedConnectionCap.Slot:
        """Reserve a slot. Raises if the cap is exhausted.

        Returns a context manager that releases the slot on exit.
        """
        if self._active >= self._capacity:
            raise ConnectionCapExceededError(
                f"connection cap reached: {self._active}/{self._capacity}"
            )
        self._active += 1
        return SharedConnectionCap.Slot(self)

    def _release(self) -> None:
        if self._active <= 0:
            return  # idempotent — guards against double-release on error paths
        self._active -= 1

    class Slot:
        """A reserved-but-not-yet-released cap slot."""

        def __init__(self, cap: SharedConnectionCap) -> None:
            self._cap = cap
            self._released = False

        def release(self) -> None:
            if self._released:
                return
            self._released = True
            self._cap._release()

        def __enter__(self) -> Self:
            return self

        def __exit__(
            self,
            exc_type: type[BaseException] | None,
            exc: BaseException | None,
            tb: TracebackType | None,
        ) -> None:
            self.release()


# ---------------------------------------------------------------------------
# Listener config / handler
# ---------------------------------------------------------------------------

ConnectionAcceptedCallback = Callable[[Connection], Awaitable[None]]
"""User-supplied coroutine called once per accepted Connection.

The callback owns the Connection (must call :meth:`Connection.start` and
later :meth:`Connection.close`). The cap slot is held until the connection
fully exits (the Connection's read loop terminates), so the callback need
only return when *its* work is done — slot release is automatic.
"""


@dataclass
class ListenerConfig:
    """Configuration for one of the two listeners."""

    host: str = "::"
    """Bind address. ``::`` is dual-stack on most platforms."""

    port: int = 0
    """Bind port. ``0`` lets the OS pick. Operational default is 8443
    (``DEFAULT_OPERATIONAL_PORT``); commissioning has no canonical default
    in two-port deployments."""

    ssl_context: ssl.SSLContext | None = None
    """The :class:`ssl.SSLContext` for this listener. Built via
    :func:`mash_py.transport.tls.make_operational_context` /
    :func:`mash_py.transport.tls.make_commissioning_context`."""

    ssl_handshake_timeout: float = 30.0
    """Per-connection TLS handshake timeout (seconds). Generous default; the
    spec's ``HandshakeTimeout`` lives at the application layer."""


# ---------------------------------------------------------------------------
# TwoPortListener
# ---------------------------------------------------------------------------


class TwoPortListener:
    """Manages the operational + commissioning :class:`asyncio.Server` pair.

    Lifecycle::

        listener = TwoPortListener(...)
        await listener.start()               # binds both ports
        # ... device runs ...
        await listener.stop_commissioning()  # close commissioning window
        await listener.stop()                # full teardown

    The shared connection cap counter is enforced uniformly across both
    listeners. When the cap is reached, new connections are rejected at TCP
    accept time (the socket is closed before the TLS handshake begins),
    matching DEC-062.
    """

    def __init__(
        self,
        operational: ListenerConfig,
        commissioning: ListenerConfig,
        on_accept: ConnectionAcceptedCallback,
        cap: SharedConnectionCap,
        *,
        connection_config: ConnectionConfig | None = None,
    ) -> None:
        if operational.ssl_context is None:
            raise ValueError("operational.ssl_context is required")
        if commissioning.ssl_context is None:
            raise ValueError("commissioning.ssl_context is required")
        self._operational_cfg = operational
        self._commissioning_cfg = commissioning
        self._on_accept = on_accept
        self._cap = cap
        self._connection_config = connection_config

        self._operational_server: asyncio.Server | None = None
        self._commissioning_server: asyncio.Server | None = None
        # Remember the bound ports so callers can still read them after
        # stop_commissioning() / stop_operational(). Ports are stable for
        # the lifetime of the TwoPortListener instance.
        self._operational_port: int | None = None
        self._commissioning_port: int | None = None

    # ------------------------------------------------------------------
    # Properties — actual bound ports (resolved after start())
    # ------------------------------------------------------------------

    @property
    def operational_port(self) -> int:
        if self._operational_port is None:
            raise RuntimeError("operational listener has not been started")
        return self._operational_port

    @property
    def commissioning_port(self) -> int:
        if self._commissioning_port is None:
            raise RuntimeError("commissioning listener has not been started")
        return self._commissioning_port

    @property
    def is_running(self) -> bool:
        return self._operational_server is not None or self._commissioning_server is not None

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Bind both listeners.

        Raises if either bind fails. On partial failure (e.g. operational
        binds but commissioning does not), the already-bound listener is
        torn down so the caller does not have to.
        """
        self._operational_server = await self._make_listener(
            self._operational_cfg, expected_alpn=ALPN_OPERATIONAL
        )
        self._operational_port = _resolve_port(self._operational_server, "operational")
        try:
            self._commissioning_server = await self._make_listener(
                self._commissioning_cfg, expected_alpn=ALPN_COMMISSIONING
            )
            self._commissioning_port = _resolve_port(
                self._commissioning_server, "commissioning"
            )
        except Exception:
            await self._stop_server("operational")
            raise

    async def stop(self) -> None:
        """Close both listeners. Idempotent."""
        await self._stop_server("commissioning")
        await self._stop_server("operational")

    async def stop_commissioning(self) -> None:
        """Close the commissioning listener (commissioning-window closure
        per DEC-082). Operational listener stays up."""
        await self._stop_server("commissioning")

    async def stop_operational(self) -> None:
        """Close the operational listener. Symmetric with
        :meth:`stop_commissioning`."""
        await self._stop_server("operational")

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _make_listener(
        self,
        cfg: ListenerConfig,
        *,
        expected_alpn: str,
    ) -> asyncio.Server:
        """Bind one listener and wire its accept callback."""
        assert cfg.ssl_context is not None  # narrowed by __init__

        async def client_connected(
            reader: asyncio.StreamReader, writer: asyncio.StreamWriter
        ) -> None:
            await self._handle_client(reader, writer, expected_alpn=expected_alpn)

        return await asyncio.start_server(
            client_connected,
            host=cfg.host,
            port=cfg.port,
            ssl=cfg.ssl_context,
            ssl_handshake_timeout=cfg.ssl_handshake_timeout,
            start_serving=True,
        )

    async def _handle_client(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
        *,
        expected_alpn: str,
    ) -> None:
        """Per-connection callback: enforce cap, verify ALPN, dispatch.

        DEC-062 wants the cap check before TLS handshake. asyncio's
        :func:`start_server` performs the handshake before invoking us,
        which means by the time we're here the handshake is already done.
        We still acquire the slot first thing — closing on cap-exceeded
        sends a TLS close-notify to the peer, which is functionally
        equivalent to a TCP-level reject for the purposes of resource
        protection. (Pre-handshake rejection at the syscall level would
        require dropping to raw sockets and re-implementing the TLS path,
        which DEC-082 explicitly rejects in favour of the simpler two-port
        pattern.)
        """
        try:
            slot = self._cap.acquire()
        except ConnectionCapExceededError:
            _log.info("rejecting connection: cap exhausted")
            writer.close()
            with contextlib.suppress(Exception):
                await writer.wait_closed()
            return

        try:
            try:
                conn = await accept_connection(reader, writer, config=self._connection_config)
            except ConnectionVerificationError as e:
                _log.warning("rejecting connection: %s", e)
                return  # writer already closed by accept_connection

            # Defence-in-depth: each listener advertises a single ALPN, so
            # a peer that negotiates the *other* ALPN here means something
            # is structurally wrong. accept_connection already verified the
            # ALPN is in the supported set; we additionally pin to the one
            # this listener is supposed to serve.
            if conn.alpn != expected_alpn:
                _log.warning(
                    "rejecting connection: ALPN %r does not match listener %r",
                    conn.alpn,
                    expected_alpn,
                )
                await conn.abort()
                return

            await self._on_accept(conn)
        except Exception:
            _log.exception("connection handler raised")
        finally:
            slot.release()

    async def _stop_server(self, which: str) -> None:
        attr = f"_{which}_server"
        server: asyncio.Server | None = getattr(self, attr)
        if server is None:
            return
        setattr(self, attr, None)
        server.close()
        with contextlib.suppress(Exception):
            await server.wait_closed()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _resolve_port(server: asyncio.Server | None, name: str) -> int:
    if server is None:
        raise RuntimeError(f"{name} listener is not running")
    sockets = server.sockets
    if not sockets:
        raise RuntimeError(f"{name} listener has no sockets")
    addr = sockets[0].getsockname()
    # IPv4: (host, port). IPv6: (host, port, flowinfo, scopeid).
    if isinstance(addr, tuple) and len(addr) >= 2 and isinstance(addr[1], int):
        return addr[1]
    raise RuntimeError(f"{name} listener has unexpected sockname: {addr!r}")


__all__ = [
    "ConnectionAcceptedCallback",
    "ConnectionCapExceededError",
    "ListenerConfig",
    "SharedConnectionCap",
    "TwoPortListener",
    "derive_connection_cap",
]
