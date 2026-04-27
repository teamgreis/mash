"""Unit tests for :class:`mash_py.interaction.InteractionClient`.

These tests exercise the correlation, dispatch, and timeout paths
without touching TLS or asyncio sockets. A :class:`FakeConnection`
duck-types the four methods the client uses (``start``, ``send``,
``close``, plus property ``state``); peer behaviour is simulated by the
test pushing bytes into ``handler.on_message`` and waiting for the
client's outbound bytes via ``await connection.next_sent()``.

Coverage targets:

- Read / Write / Subscribe / Invoke happy-path correlation
- Subscribe priming-report parsing (§6.2)
- Notification routing by subscriptionId (§3.3)
- Unsubscribe local de-registration + wire form (§6.4)
- Per-request timeout (§8.3)
- MessageID allocator: first id is 1 (spec §3.5; mash-go off-by-one)
- MessageID wraparound skipping 0
- on_close: pending futures fail with InteractionClientClosedError
- StatusError with diagnostic message (§8.2)
"""

from __future__ import annotations

import asyncio
from dataclasses import dataclass, field
from typing import Any

import pytest

from mash_py.interaction import (
    DEFAULT_REQUEST_TIMEOUT,
    InteractionClient,
    InteractionClientClosedError,
    InteractionTimeoutError,
    StatusError,
    Subscription,
)
from mash_py.interaction.client import _MAX_MESSAGE_ID
from mash_py.transport.connection import (
    ConnectionClosedError,
    ConnectionState,
)
from mash_py.wire import (
    Notification,
    Operation,
    Response,
    Status,
    decode_request,
    encode_notification,
    encode_response,
)
from mash_py.wire.payloads import ErrorPayload, SubscribeResponsePayload

pytestmark = pytest.mark.asyncio


# ---------------------------------------------------------------------------
# Fake transport
# ---------------------------------------------------------------------------


@dataclass
class FakeConnection:
    """Captures outbound frames in a queue and exposes the handler so tests
    can inject inbound frames via ``handler.on_message`` directly.

    The minimal surface used by InteractionClient:

    - ``start(handler)`` — store handler, transition to CONNECTED.
    - ``send(data)`` — push to outbound queue.
    - ``close()`` — invoke ``handler.on_close(None)`` once, transition
      to DISCONNECTED.

    Synchronous queue ``outbound`` lets tests assert *what* the client
    serialised; ``next_sent()`` is the awaitable convenience for the
    typical "send something, see what came out" flow.
    """

    outbound: asyncio.Queue[bytes] = field(default_factory=asyncio.Queue)
    handler: Any = None
    state: ConnectionState = ConnectionState.DISCONNECTED
    closed: bool = False
    refuse_send: bool = False  # set True to make send() raise ConnectionClosedError

    def start(self, handler: Any) -> None:
        if self.state is not ConnectionState.DISCONNECTED:
            raise RuntimeError(f"start in state {self.state}")
        self.handler = handler
        self.state = ConnectionState.CONNECTED

    async def send(self, data: bytes) -> None:
        if self.refuse_send or self.state is not ConnectionState.CONNECTED:
            raise ConnectionClosedError("fake connection refused send")
        await self.outbound.put(data)

    async def close(self) -> None:
        if self.closed:
            return
        self.closed = True
        prev = self.state
        self.state = ConnectionState.DISCONNECTED
        if prev is ConnectionState.CONNECTED and self.handler is not None:
            await self.handler.on_close(None)

    # --- test helpers ---------------------------------------------------

    async def next_sent(self, timeout: float = 1.0) -> bytes:
        return await asyncio.wait_for(self.outbound.get(), timeout=timeout)

    async def deliver(self, frame: bytes) -> None:
        """Feed a frame into the handler exactly as the read loop would."""
        assert self.handler is not None, "deliver() before start()"
        await self.handler.on_message(frame)


# ---------------------------------------------------------------------------
# Helpers for building peer responses
# ---------------------------------------------------------------------------


def _make_read_response(
    message_id: int,
    attrs: dict[int, Any],
    *,
    status: Status = Status.SUCCESS,
) -> bytes:
    return encode_response(Response(message_id=message_id, status=status, payload=attrs))


def _make_write_response(message_id: int, attrs: dict[int, Any]) -> bytes:
    return encode_response(
        Response(message_id=message_id, status=Status.SUCCESS, payload=attrs)
    )


def _make_subscribe_response(
    message_id: int, sub_id: int, current: dict[int, Any]
) -> bytes:
    return encode_response(
        Response(
            message_id=message_id,
            status=Status.SUCCESS,
            payload=SubscribeResponsePayload(
                subscription_id=sub_id, current_values=current
            ),
        )
    )


def _make_invoke_response(message_id: int, payload: Any) -> bytes:
    return encode_response(
        Response(message_id=message_id, status=Status.SUCCESS, payload=payload)
    )


def _make_error_response(message_id: int, status: Status, message: str = "") -> bytes:
    payload = ErrorPayload(message=message) if message else None
    return encode_response(
        Response(message_id=message_id, status=status, payload=payload)
    )


def _make_notification(sub_id: int, endpoint: int, feature: int, changes: dict[int, Any]) -> bytes:
    return encode_notification(
        Notification(
            subscription_id=sub_id,
            endpoint_id=endpoint,
            feature_id=feature,
            changes=changes,
        )
    )


@pytest.fixture
def conn() -> FakeConnection:
    return FakeConnection()


@pytest.fixture
async def client(conn: FakeConnection):
    c = InteractionClient(conn)
    c.start()
    try:
        yield c
    finally:
        if not c.is_closed:
            await c.close()


# ---------------------------------------------------------------------------
# 1. Pending-call correlation
# ---------------------------------------------------------------------------


async def test_read_correlates_response_to_request(
    conn: FakeConnection, client: InteractionClient
) -> None:
    """The client's first request emits messageId=1 (spec §3.5), and a
    response with that id resolves the pending future."""

    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        assert req.message_id == 1, (
            f"first request should emit messageId=1 per §3.5; got {req.message_id} "
            "(mash-go pre-increments and emits 2)"
        )
        assert req.operation is Operation.READ
        assert req.endpoint_id == 0 and req.feature_id == 0x01
        assert req.payload == [], "empty attribute list = read all (§4.3)"
        await conn.deliver(_make_read_response(req.message_id, {1: "device-id", 2: "ACME"}))

    peer_task = asyncio.create_task(peer())
    attrs = await client.read(0, 0x01)
    await peer_task

    assert attrs == {1: "device-id", 2: "ACME"}


async def test_message_id_allocator_starts_at_one(
    conn: FakeConnection, client: InteractionClient
) -> None:
    """Three sequential requests get ids 1, 2, 3 — not 2, 3, 4 like Go."""
    seen_ids: list[int] = []

    async def peer() -> None:
        for _ in range(3):
            sent = await conn.next_sent()
            req = decode_request(sent)
            seen_ids.append(req.message_id)
            await conn.deliver(_make_read_response(req.message_id, {}))

    peer_task = asyncio.create_task(peer())
    await client.read(0, 0x01)
    await client.read(0, 0x02)
    await client.read(0, 0x03)
    await peer_task

    assert seen_ids == [1, 2, 3]


async def test_message_id_wraps_skipping_zero(conn: FakeConnection) -> None:
    """Wraparound from 2^32-1 must skip 0 (reserved for notifications)."""
    client = InteractionClient(conn)
    client.start()
    # Force counter near the top.
    client._next_msg_id = _MAX_MESSAGE_ID

    async def peer() -> None:
        for _ in range(2):
            sent = await conn.next_sent()
            req = decode_request(sent)
            await conn.deliver(_make_read_response(req.message_id, {}))

    peer_task = asyncio.create_task(peer())
    # Two reads: first should use 0xFFFFFFFF, second should wrap to 1
    # (skipping 0).
    sent1 = asyncio.create_task(client.read(0, 0x01))
    # Yield so the first request lands on the wire and the peer responds.
    await asyncio.sleep(0)
    await sent1
    sent2 = asyncio.create_task(client.read(0, 0x01))
    await sent2
    await peer_task
    assert client._next_msg_id == 2  # wrapped: 1 was used, next is 2


# ---------------------------------------------------------------------------
# 2. Timeout
# ---------------------------------------------------------------------------


async def test_request_times_out_when_no_response(conn: FakeConnection) -> None:
    """No response within the configured timeout raises InteractionTimeoutError."""
    client = InteractionClient(conn, request_timeout=0.05)
    client.start()
    try:
        with pytest.raises(InteractionTimeoutError):
            await client.read(0, 0x01)
        # The pending entry must be cleaned up so a late response doesn't leak.
        assert client._pending == {}
    finally:
        await client.close()


async def test_late_response_is_dropped_quietly(
    conn: FakeConnection, client: InteractionClient, caplog
) -> None:
    """If a response arrives after its caller timed out, drop the frame and
    log a warning — don't raise (would kill the read loop)."""
    # Make a real request, time it out, then deliver the late response.
    client._timeout = 0.05
    with pytest.raises(InteractionTimeoutError):
        await client.read(0, 0x01)
    # Pull the request off the wire so the queue is clean.
    sent = await conn.next_sent()
    req = decode_request(sent)

    import logging as _logging

    with caplog.at_level(_logging.WARNING, logger="mash_py.interaction.client"):
        await conn.deliver(_make_read_response(req.message_id, {1: "late"}))

    assert any(
        "unknown messageId" in rec.message for rec in caplog.records
    ), "late response should log an 'unknown messageId' warning"


# ---------------------------------------------------------------------------
# 3. Status / error response
# ---------------------------------------------------------------------------


async def test_status_error_raised_on_non_success(
    conn: FakeConnection, client: InteractionClient
) -> None:
    """Non-SUCCESS Status surfaces as StatusError carrying the diagnostic."""

    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        await conn.deliver(
            _make_error_response(req.message_id, Status.INVALID_PARAMETER, "bad limit")
        )

    peer_task = asyncio.create_task(peer())
    with pytest.raises(StatusError) as excinfo:
        await client.write(1, 0x05, {21: -100})
    await peer_task

    assert excinfo.value.status is Status.INVALID_PARAMETER
    assert excinfo.value.message == "bad limit"


# ---------------------------------------------------------------------------
# 4. Write
# ---------------------------------------------------------------------------


async def test_write_sends_request_and_returns_resulting_values(
    conn: FakeConnection, client: InteractionClient
) -> None:
    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        assert req.operation is Operation.WRITE
        assert req.endpoint_id == 1 and req.feature_id == 0x05
        assert req.payload == {21: 6_000_000}
        await conn.deliver(_make_write_response(req.message_id, {20: 5_000_000, 21: 6_000_000}))

    peer_task = asyncio.create_task(peer())
    result = await client.write(1, 0x05, {21: 6_000_000})
    await peer_task

    assert result == {20: 5_000_000, 21: 6_000_000}


async def test_write_rejects_empty_attributes(client: InteractionClient) -> None:
    with pytest.raises(ValueError, match="non-empty"):
        await client.write(1, 0x05, {})


# ---------------------------------------------------------------------------
# 5. Subscribe / Notification routing / Unsubscribe
# ---------------------------------------------------------------------------


async def test_subscribe_returns_priming_report_and_routes_notifications(
    conn: FakeConnection, client: InteractionClient
) -> None:
    received: list[Notification] = []

    async def handler(notif: Notification) -> None:
        received.append(notif)

    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        assert req.operation is Operation.SUBSCRIBE
        # SubscribePayload encoded as {1: ids, 2: minMs, 3: maxMs} omitempty
        # — empty ids + zero intervals → all keys omitted.
        assert req.payload == {} or req.payload is None, (
            f"empty Subscribe should omit all keys (omitempty); got {req.payload!r}"
        )
        await conn.deliver(
            _make_subscribe_response(
                req.message_id, sub_id=42, current={1: 100, 2: 200}
            )
        )

    peer_task = asyncio.create_task(peer())
    sub = await client.subscribe(1, 0x04, handler)
    await peer_task

    assert isinstance(sub, Subscription)
    assert sub.subscription_id == 42
    assert sub.initial_values == {1: 100, 2: 200}

    # A notification with subscriptionId=42 should reach the handler.
    await conn.deliver(_make_notification(42, 1, 0x04, {1: 150}))
    await asyncio.sleep(0)  # let the dispatch coroutine run
    assert len(received) == 1
    assert received[0].subscription_id == 42
    assert received[0].changes == {1: 150}


async def test_subscribe_routes_only_to_matching_subscription(
    conn: FakeConnection, client: InteractionClient
) -> None:
    """Two subscriptions, one notification — only the matching handler fires."""
    received_a: list[Notification] = []
    received_b: list[Notification] = []

    async def peer_for_two_subs() -> None:
        for sub_id in (10, 20):
            sent = await conn.next_sent()
            req = decode_request(sent)
            await conn.deliver(_make_subscribe_response(req.message_id, sub_id, {}))

    peer_task = asyncio.create_task(peer_for_two_subs())
    await client.subscribe(1, 0x02, lambda n: received_a.append(n))
    await client.subscribe(2, 0x02, lambda n: received_b.append(n))
    await peer_task

    await conn.deliver(_make_notification(10, 1, 0x02, {24: "RUNNING"}))
    await asyncio.sleep(0)
    assert len(received_a) == 1 and len(received_b) == 0

    await conn.deliver(_make_notification(20, 2, 0x02, {24: "PAUSED"}))
    await asyncio.sleep(0)
    assert len(received_a) == 1 and len(received_b) == 1


async def test_unsubscribe_deregisters_handler_and_sends_correct_wire_form(
    conn: FakeConnection, client: InteractionClient
) -> None:
    received: list[Notification] = []

    async def peer_subscribe() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        await conn.deliver(_make_subscribe_response(req.message_id, 7, {}))

    asyncio.create_task(peer_subscribe())
    sub = await client.subscribe(1, 0x02, lambda n: received.append(n))
    assert 7 in client._subscriptions

    async def peer_unsubscribe() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        # §6.4: Subscribe op, endpoint=0, feature=0, payload {1: subId}.
        assert req.operation is Operation.SUBSCRIBE
        assert req.endpoint_id == 0 and req.feature_id == 0
        assert req.payload == {1: 7}
        await conn.deliver(
            encode_response(
                Response(message_id=req.message_id, status=Status.SUCCESS, payload=None)
            )
        )

    peer_task = asyncio.create_task(peer_unsubscribe())
    await client.unsubscribe(sub)
    await peer_task

    assert 7 not in client._subscriptions

    # Stale notifications after unsubscribe are silently dropped.
    await conn.deliver(_make_notification(7, 1, 0x02, {1: "stale"}))
    await asyncio.sleep(0)
    assert received == []


async def test_subscribe_rejects_reserved_endpoint_zero_feature_zero(
    client: InteractionClient,
) -> None:
    """Endpoint=0, feature=0 is reserved for unsubscribe — Subscribe with
    those addresses is a client-side bug we catch before sending."""
    with pytest.raises(ValueError, match="reserved for unsubscribe"):
        await client.subscribe(0, 0, lambda n: None)


# ---------------------------------------------------------------------------
# 6. Invoke
# ---------------------------------------------------------------------------


async def test_invoke_sends_command_and_returns_response_payload(
    conn: FakeConnection, client: InteractionClient
) -> None:
    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        assert req.operation is Operation.INVOKE
        assert req.endpoint_id == 0 and req.feature_id == 0x01
        # InvokePayload wire form: {1: commandId, 2: parameters} (§7.1)
        assert req.payload == {1: 0x10, 2: {1: "zone-abc"}}
        await conn.deliver(_make_invoke_response(req.message_id, {1: True}))

    peer_task = asyncio.create_task(peer())
    result = await client.invoke(0, 0x01, 0x10, parameters={1: "zone-abc"})
    await peer_task

    assert result == {1: True}


async def test_invoke_with_no_parameters_omits_key_two(
    conn: FakeConnection, client: InteractionClient
) -> None:
    async def peer() -> None:
        sent = await conn.next_sent()
        req = decode_request(sent)
        # InvokePayload.parameters None → {1: commandId} only.
        assert req.payload == {1: 0x10}
        await conn.deliver(_make_invoke_response(req.message_id, None))

    peer_task = asyncio.create_task(peer())
    result = await client.invoke(0, 0x01, 0x10)
    await peer_task
    assert result is None


# ---------------------------------------------------------------------------
# 7. Lifecycle: close + on_close
# ---------------------------------------------------------------------------


async def test_close_fails_pending_futures(conn: FakeConnection) -> None:
    """Calling close() while a request is in flight raises
    InteractionClientClosedError on the pending awaiter."""
    client = InteractionClient(conn, request_timeout=10.0)
    client.start()

    # Start a request but don't deliver a response.
    in_flight = asyncio.create_task(client.read(0, 0x01))
    # Wait for the bytes to appear so we know the future is pending.
    _ = await conn.next_sent()

    await client.close()
    with pytest.raises(InteractionClientClosedError):
        await in_flight


async def test_send_on_closed_connection_raises_client_closed(conn: FakeConnection) -> None:
    """A connection that refuses send() (because it's been torn down) is
    surfaced to the caller as InteractionClientClosedError, NOT as a
    transport-layer error — the interaction layer is the user-facing API."""
    client = InteractionClient(conn)
    client.start()
    conn.refuse_send = True
    try:
        with pytest.raises(InteractionClientClosedError):
            await client.read(0, 0x01)
    finally:
        await client.close()


async def test_on_close_from_peer_fails_pending(conn: FakeConnection) -> None:
    """When the connection's read loop reports on_close (peer hung up),
    pending futures must fail rather than hang forever."""
    client = InteractionClient(conn, request_timeout=10.0)
    client.start()

    in_flight = asyncio.create_task(client.read(0, 0x01))
    _ = await conn.next_sent()

    # Simulate the read loop detecting EOF and calling on_close directly,
    # bypassing close().
    await client.on_close(None)
    with pytest.raises(InteractionClientClosedError):
        await in_flight


# ---------------------------------------------------------------------------
# 8. Sanity on defaults
# ---------------------------------------------------------------------------


async def test_default_request_timeout_matches_spec() -> None:
    """§8.3: Default timeout = 10 seconds."""
    assert DEFAULT_REQUEST_TIMEOUT == 10.0


async def test_invalid_request_timeout_rejected() -> None:
    fake = FakeConnection()
    with pytest.raises(ValueError):
        InteractionClient(fake, request_timeout=0)
    with pytest.raises(ValueError):
        InteractionClient(fake, request_timeout=-1.0)
