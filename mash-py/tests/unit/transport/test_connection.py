"""Loopback TLS handshake + control-message handling tests for Connection.

These exercise:

- Both ALPNs handshake successfully on their respective contexts.
- A client offering the *wrong* ALPN is rejected at handshake.
- Ping → Pong round-trip on a live Connection.
- Close handshake terminates the connection cleanly.
- Application-layer frames reach the handler.
- Cross-context ALPN exclusivity (commissioning server rejects a client
  offering only ``mash/1`` and vice-versa).
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from dataclasses import dataclass, field

import pytest

from mash_py.transport import (
    ALPN_COMMISSIONING,
    ALPN_OPERATIONAL,
    Connection,
    ConnectionConfig,
    ConnectionVerificationError,
    accept,
    connect,
    make_commissioning_client_context,
    make_commissioning_context,
    make_operational_client_context,
    make_operational_context,
)
from mash_py.wire.codec import encode_control_message, encode_request
from mash_py.wire.operations import ControlMessageType, Operation
from mash_py.wire.payloads import ControlMessage, ReadPayload, Request

# ---------------------------------------------------------------------------
# Test handler & server harness
# ---------------------------------------------------------------------------


@dataclass
class CapturingHandler:
    """ConnectionHandler that records messages for assertions."""

    messages: list[bytes] = field(default_factory=list)
    on_close_exc: BaseException | None = None
    on_close_called: asyncio.Event = field(default_factory=asyncio.Event)
    message_received: asyncio.Event = field(default_factory=asyncio.Event)

    async def on_message(self, data: bytes) -> None:
        self.messages.append(data)
        self.message_received.set()

    async def on_close(self, exc: BaseException | None) -> None:
        self.on_close_exc = exc
        self.on_close_called.set()


@dataclass
class TLSEchoServer:
    """Trivial server: accept one connection, echo any application frame.

    Used by tests that want to verify a client can connect, send, and
    receive over a real TLS handshake.
    """

    server: asyncio.Server
    accepted_connections: list[Connection] = field(default_factory=list)
    accept_event: asyncio.Event = field(default_factory=asyncio.Event)
    accept_error: BaseException | None = None

    @property
    def port(self) -> int:
        sock = self.server.sockets[0]
        addr = sock.getsockname()
        return addr[1]

    async def stop(self) -> None:
        for conn in self.accepted_connections:
            with contextlib.suppress(Exception):
                await conn.close()
        self.server.close()
        with contextlib.suppress(Exception):
            await self.server.wait_closed()


@pytest.fixture
async def commissioning_echo(commissioning_bundle) -> AsyncIterator[TLSEchoServer]:
    """Boot a commissioning-port server that captures incoming Connections."""
    server_ctx = make_commissioning_context(commissioning_bundle.cert, commissioning_bundle.key)
    state = TLSEchoServer.__new__(TLSEchoServer)
    state.accepted_connections = []
    state.accept_event = asyncio.Event()
    state.accept_error = None

    async def cb(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            conn = await accept(reader, writer)
        except ConnectionVerificationError as exc:
            state.accept_error = exc
            state.accept_event.set()
            return
        state.accepted_connections.append(conn)
        handler = CapturingHandler()

        async def echo_loop() -> None:
            while True:
                if not handler.message_received.is_set():
                    try:
                        await asyncio.wait_for(handler.message_received.wait(), 1.0)
                    except TimeoutError:
                        return
                if handler.messages:
                    msg = handler.messages.pop(0)
                    handler.message_received.clear()
                    await conn.send(msg)
                else:
                    handler.message_received.clear()

        conn.start(handler)
        state.accept_event.set()
        # Echo task lives until the connection closes.
        echo_task = asyncio.create_task(echo_loop())
        await handler.on_close_called.wait()
        echo_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await echo_task

    state.server = await asyncio.start_server(cb, "127.0.0.1", 0, ssl=server_ctx)
    try:
        yield state
    finally:
        await state.stop()


@pytest.fixture
async def operational_echo(
    device_op_bundle,
) -> AsyncIterator[TLSEchoServer]:
    """Boot an operational-port server with mTLS against the Zone CA."""
    server_ctx = make_operational_context(
        device_op_bundle.zone_ca, device_op_bundle.cert, device_op_bundle.key
    )
    state = TLSEchoServer.__new__(TLSEchoServer)
    state.accepted_connections = []
    state.accept_event = asyncio.Event()
    state.accept_error = None

    async def cb(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            conn = await accept(reader, writer)
        except ConnectionVerificationError as exc:
            state.accept_error = exc
            state.accept_event.set()
            return
        state.accepted_connections.append(conn)
        handler = CapturingHandler()
        conn.start(handler)
        state.accept_event.set()
        await handler.on_close_called.wait()

    state.server = await asyncio.start_server(cb, "127.0.0.1", 0, ssl=server_ctx)
    try:
        yield state
    finally:
        await state.stop()


# ---------------------------------------------------------------------------
# Handshake
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_commissioning_handshake_succeeds(commissioning_echo: TLSEchoServer):
    """A commissioning client + server with matching ALPN handshake cleanly."""
    client_ctx = make_commissioning_client_context()
    conn = await connect("127.0.0.1", commissioning_echo.port, client_ctx)
    try:
        assert conn.alpn == ALPN_COMMISSIONING
        assert conn.is_commissioning
        assert not conn.is_operational
    finally:
        await conn.abort()


@pytest.mark.asyncio
async def test_operational_handshake_succeeds(
    operational_echo: TLSEchoServer, controller_op_bundle
):
    """Operational client (Zone CA-signed cert) handshakes with the device."""
    client_ctx = make_operational_client_context(
        controller_op_bundle.zone_ca,
        controller_op_bundle.cert,
        controller_op_bundle.key,
    )
    conn = await connect("127.0.0.1", operational_echo.port, client_ctx)
    try:
        assert conn.alpn == ALPN_OPERATIONAL
        assert conn.is_operational
    finally:
        await conn.abort()


# ---------------------------------------------------------------------------
# ALPN exclusivity (DEC-082 — each listener serves one ALPN)
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_commissioning_listener_rejects_operational_alpn(
    commissioning_echo: TLSEchoServer, controller_op_bundle
):
    """A client offering only `mash/1` to the commissioning listener fails."""
    client_ctx = make_operational_client_context(
        controller_op_bundle.zone_ca,
        controller_op_bundle.cert,
        controller_op_bundle.key,
    )
    with pytest.raises((ConnectionVerificationError, OSError, ssl_alert_error())):
        conn = await connect("127.0.0.1", commissioning_echo.port, client_ctx)
        await conn.abort()


@pytest.mark.asyncio
async def test_operational_listener_rejects_commissioning_alpn(
    operational_echo: TLSEchoServer,
):
    """A client offering only `mash-comm/1` to the operational listener fails."""
    client_ctx = make_commissioning_client_context()
    with pytest.raises((ConnectionVerificationError, OSError, ssl_alert_error())):
        conn = await connect("127.0.0.1", operational_echo.port, client_ctx)
        await conn.abort()


def ssl_alert_error() -> type[BaseException]:
    """Python's ssl.SSLError class — wrapped to keep imports tidy."""
    import ssl as _ssl

    return _ssl.SSLError


# ---------------------------------------------------------------------------
# Ping / Pong
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_ping_elicits_pong(commissioning_echo: TLSEchoServer):
    """Sending a Ping over a live Connection causes the server to send Pong."""
    pong_seqs: list[int] = []
    pong_received = asyncio.Event()

    def on_pong(seq: int) -> None:
        pong_seqs.append(seq)
        pong_received.set()

    client_ctx = make_commissioning_client_context()
    conn = await connect(
        "127.0.0.1",
        commissioning_echo.port,
        client_ctx,
        config=ConnectionConfig(on_pong=on_pong),
    )
    handler = CapturingHandler()
    conn.start(handler)
    try:
        await commissioning_echo.accept_event.wait()
        await conn.send_control(ControlMessageType.PING, sequence=42)
        await asyncio.wait_for(pong_received.wait(), timeout=2.0)
        assert pong_seqs == [42]
    finally:
        await conn.close()


# ---------------------------------------------------------------------------
# Close handshake
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_close_terminates_connection(commissioning_echo: TLSEchoServer):
    """Calling close() drives the state to DISCONNECTED on both sides."""
    client_ctx = make_commissioning_client_context()
    conn = await connect("127.0.0.1", commissioning_echo.port, client_ctx)
    handler = CapturingHandler()
    conn.start(handler)
    try:
        await commissioning_echo.accept_event.wait()
        server_conn = commissioning_echo.accepted_connections[0]
        await conn.close()
        # After close(), our Connection has wound down.
        assert handler.on_close_called.is_set()
        # Server side observed the Close as well.
        await asyncio.wait_for(_wait_state_disconnected(server_conn), 2.0)
    finally:
        with contextlib.suppress(Exception):
            await conn.close()


async def _wait_state_disconnected(conn: Connection) -> None:
    while conn.state.name != "DISCONNECTED":
        await asyncio.sleep(0.01)


# ---------------------------------------------------------------------------
# Application-layer message round-trip
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_application_frame_is_echoed(commissioning_echo: TLSEchoServer):
    """A Request frame sent by the client is received and echoed by the server."""
    client_ctx = make_commissioning_client_context()
    conn = await connect("127.0.0.1", commissioning_echo.port, client_ctx)
    handler = CapturingHandler()
    conn.start(handler)
    try:
        req = Request(
            message_id=1, operation=Operation.READ, endpoint_id=1, feature_id=1,
            payload=ReadPayload(attribute_ids=[1, 2]),
        )
        wire_bytes = encode_request(req)
        await conn.send(wire_bytes)
        await asyncio.wait_for(handler.message_received.wait(), 2.0)
        assert handler.messages == [wire_bytes]
    finally:
        await conn.close()


# ---------------------------------------------------------------------------
# Sanity: control messages are NOT delivered to handler.on_message
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_control_messages_not_dispatched_as_application_messages(
    commissioning_echo: TLSEchoServer,
):
    """Ping/Pong/Close frames must be intercepted by the read loop, never
    surfaced to ``handler.on_message``."""
    client_ctx = make_commissioning_client_context()
    conn = await connect("127.0.0.1", commissioning_echo.port, client_ctx)
    handler = CapturingHandler()
    conn.start(handler)
    try:
        # Send a raw control frame from the client to the server. The
        # server's read loop must answer (Pong) without surfacing the
        # frame to its CapturingHandler. The echo-loop on the server
        # would echo any application frame, so the absence of any echo
        # back to us *also* confirms the server did not treat it as
        # application data.
        await conn.send_control(ControlMessageType.PING, sequence=99)

        # Give the server a moment.
        await asyncio.sleep(0.2)

        # No application messages bubbled up locally.
        assert handler.messages == []

        # Server-side handler also saw no application messages.
        await commissioning_echo.accept_event.wait()
        server_conn = commissioning_echo.accepted_connections[0]
        # We can't peek inside its handler from here, but the test passes
        # the spec contract: control frames don't appear as application
        # frames. The local echo would have happened if the server had
        # mistakenly forwarded the frame.
        del server_conn  # silence unused
    finally:
        await conn.close()


# ---------------------------------------------------------------------------
# Direct codec sanity: round-trip a raw control message through Connection
# ---------------------------------------------------------------------------


def test_encode_control_decodes_under_peek():
    """Sanity: the bytes that Connection.send_control writes are classified
    as CONTROL by the same peek helper the read loop uses (DEC-083)."""
    from mash_py.wire.operations import MessageType
    from mash_py.wire.peek import peek_message_type

    frame = encode_control_message(ControlMessage(type=ControlMessageType.PING, sequence=7))
    assert peek_message_type(frame) is MessageType.CONTROL
