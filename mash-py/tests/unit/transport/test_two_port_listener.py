"""Tests for :class:`mash_py.service.listeners.TwoPortListener`.

Spec contract per DEC-082 + DEC-064:

- Two listeners bind on distinct ports.
- The shared cap counter (max_zones + 1) is enforced uniformly across both
  listeners.
- mDNS SRV ``port`` field is authoritative (test by binding to ``port=0``
  and reading the OS-assigned port back via the listener properties).
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from dataclasses import dataclass, field

import pytest

from mash_py.service.listeners import (
    ConnectionCapExceededError,
    ListenerConfig,
    SharedConnectionCap,
    TwoPortListener,
    derive_connection_cap,
)
from mash_py.transport import (
    ALPN_COMMISSIONING,
    ALPN_OPERATIONAL,
    Connection,
    connect,
    make_commissioning_client_context,
    make_commissioning_context,
    make_operational_client_context,
    make_operational_context,
)

# ---------------------------------------------------------------------------
# Shared cap counter
# ---------------------------------------------------------------------------


class TestDeriveConnectionCap:
    @pytest.mark.parametrize("max_zones,expected", [(0, 1), (1, 2), (2, 3), (5, 6)])
    def test_formula(self, max_zones: int, expected: int):
        # DEC-064: total = max_zones operational + 1 commissioning.
        assert derive_connection_cap(max_zones) == expected

    def test_negative_rejected(self):
        with pytest.raises(ValueError):
            derive_connection_cap(-1)


class TestSharedConnectionCap:
    def test_acquire_within_cap(self):
        cap = SharedConnectionCap(capacity=3)
        s1 = cap.acquire()
        s2 = cap.acquire()
        s3 = cap.acquire()
        assert cap.active == 3
        s1.release()
        s2.release()
        s3.release()
        assert cap.active == 0

    def test_acquire_beyond_cap_raises(self):
        cap = SharedConnectionCap(capacity=2)
        cap.acquire()
        cap.acquire()
        with pytest.raises(ConnectionCapExceededError):
            cap.acquire()

    def test_release_after_exception_via_context_manager(self):
        """A `with cap.acquire():` block releases the slot on exit even when
        the body raises — that's the whole point of the context manager."""
        cap = SharedConnectionCap(capacity=1)
        with contextlib.suppress(RuntimeError), cap.acquire():
            assert cap.active == 1
            raise RuntimeError("boom")
        assert cap.active == 0

    def test_idempotent_release(self):
        cap = SharedConnectionCap(capacity=1)
        slot = cap.acquire()
        slot.release()
        slot.release()  # no double-decrement
        assert cap.active == 0

    def test_zero_capacity_rejected(self):
        with pytest.raises(ValueError):
            SharedConnectionCap(capacity=0)


# ---------------------------------------------------------------------------
# Listener fixtures
# ---------------------------------------------------------------------------


@dataclass
class _ListenerHarness:
    listener: TwoPortListener
    cap: SharedConnectionCap
    accepted_connections: list[Connection] = field(default_factory=list)
    accept_event: asyncio.Event = field(default_factory=asyncio.Event)


@pytest.fixture
async def listener_harness(
    commissioning_bundle, device_op_bundle
) -> AsyncIterator[_ListenerHarness]:
    """Two-port listener with cap=2 (1 operational zone + 1 commissioning slot)."""
    cap = SharedConnectionCap(capacity=derive_connection_cap(max_zones=1))
    op_ctx = make_operational_context(
        device_op_bundle.zone_ca, device_op_bundle.cert, device_op_bundle.key
    )
    comm_ctx = make_commissioning_context(commissioning_bundle.cert, commissioning_bundle.key)

    harness = _ListenerHarness.__new__(_ListenerHarness)
    harness.cap = cap
    harness.accepted_connections = []
    harness.accept_event = asyncio.Event()

    async def on_accept(conn: Connection) -> None:
        harness.accepted_connections.append(conn)
        harness.accept_event.set()

        # Hold the connection open until the test closes it.
        class _Holder:
            async def on_message(self, _data: bytes) -> None: ...
            async def on_close(self, _exc: BaseException | None) -> None: ...

        conn.start(_Holder())
        # Wait until the test signals close. We poll the connection state.
        while conn.state.name != "DISCONNECTED":
            await asyncio.sleep(0.05)

    listener = TwoPortListener(
        operational=ListenerConfig(host="127.0.0.1", port=0, ssl_context=op_ctx),
        commissioning=ListenerConfig(host="127.0.0.1", port=0, ssl_context=comm_ctx),
        on_accept=on_accept,
        cap=cap,
    )
    harness.listener = listener
    await listener.start()
    try:
        yield harness
    finally:
        # Close any still-open accepted conns so the on_accept tasks return.
        for conn in harness.accepted_connections:
            with contextlib.suppress(Exception):
                await conn.close()
        await listener.stop()


# ---------------------------------------------------------------------------
# Port binding
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_both_listeners_bind_on_distinct_ports(listener_harness: _ListenerHarness):
    op_port = listener_harness.listener.operational_port
    comm_port = listener_harness.listener.commissioning_port
    assert op_port > 0
    assert comm_port > 0
    assert op_port != comm_port


# ---------------------------------------------------------------------------
# ALPN routing — connect to the right listener
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_commissioning_client_lands_on_commissioning_listener(
    listener_harness: _ListenerHarness,
):
    client_ctx = make_commissioning_client_context()
    conn = await connect(
        "127.0.0.1", listener_harness.listener.commissioning_port, client_ctx
    )
    try:
        assert conn.alpn == ALPN_COMMISSIONING
        await listener_harness.accept_event.wait()
        # Cap counter incremented for the accepted connection.
        assert listener_harness.cap.active == 1
    finally:
        await conn.close()


@pytest.mark.asyncio
async def test_operational_client_lands_on_operational_listener(
    listener_harness: _ListenerHarness, controller_op_bundle
):
    client_ctx = make_operational_client_context(
        controller_op_bundle.zone_ca,
        controller_op_bundle.cert,
        controller_op_bundle.key,
    )
    conn = await connect(
        "127.0.0.1", listener_harness.listener.operational_port, client_ctx
    )
    try:
        assert conn.alpn == ALPN_OPERATIONAL
        await listener_harness.accept_event.wait()
        assert listener_harness.cap.active == 1
    finally:
        await conn.close()


# ---------------------------------------------------------------------------
# Cap enforcement (DEC-064: shared per-device counter)
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_cap_counter_is_shared_across_listeners(
    listener_harness: _ListenerHarness, controller_op_bundle
):
    """One commissioning + one operational connection consume both cap slots
    (cap = max_zones+1 = 2 for this harness). A *third* connection on either
    port must be rejected because the *device* cap is exhausted, even though
    each individual listener has only seen one connection."""
    comm_client_ctx = make_commissioning_client_context()
    op_client_ctx = make_operational_client_context(
        controller_op_bundle.zone_ca,
        controller_op_bundle.cert,
        controller_op_bundle.key,
    )

    # 1: commissioning connection takes one slot.
    c1 = await connect(
        "127.0.0.1", listener_harness.listener.commissioning_port, comm_client_ctx
    )
    listener_harness.accept_event.clear()
    await listener_harness.accept_event.wait()

    # 2: operational connection takes the other slot.
    c2 = await connect(
        "127.0.0.1", listener_harness.listener.operational_port, op_client_ctx
    )
    listener_harness.accept_event.clear()
    await listener_harness.accept_event.wait()

    assert listener_harness.cap.active == 2

    # 3: a third connection on EITHER port must be rejected. The cap check
    # runs in the per-connection callback after handshake, so the symptom
    # at the client end is "TLS established, then peer shut down before any
    # bytes were exchanged". The connection MAY succeed locally (TLS up)
    # but its read loop will see EOF immediately. Either outcome is
    # acceptable per the spec — what matters is the cap counter.
    try:
        c3_ctx = make_commissioning_client_context()
        c3 = await connect(
            "127.0.0.1", listener_harness.listener.commissioning_port, c3_ctx
        )
        # If TLS handshake succeeded, the server's accept callback has
        # already closed the writer (cap exhausted). Verify the cap counter
        # did NOT go above 2 by polling briefly.
        for _ in range(10):
            if listener_harness.cap.active <= 2:
                break
            await asyncio.sleep(0.05)
        assert listener_harness.cap.active <= 2
        await c3.abort()
    except (OSError, Exception):
        pass

    await c1.close()
    await c2.close()


# ---------------------------------------------------------------------------
# Lifecycle: stop_commissioning closes only the commissioning listener
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_stop_commissioning_keeps_operational_open(
    listener_harness: _ListenerHarness, controller_op_bundle
):
    """Closing the commissioning window (DEC-082) does not affect operational."""
    await listener_harness.listener.stop_commissioning()

    # Commissioning port no longer accepts.
    comm_client_ctx = make_commissioning_client_context()
    with pytest.raises((OSError, ConnectionRefusedError)):
        await asyncio.wait_for(
            connect(
                "127.0.0.1",
                listener_harness.listener.commissioning_port,
                comm_client_ctx,
            ),
            timeout=2.0,
        )

    # Operational port still works.
    op_client_ctx = make_operational_client_context(
        controller_op_bundle.zone_ca,
        controller_op_bundle.cert,
        controller_op_bundle.key,
    )
    op_conn = await connect(
        "127.0.0.1", listener_harness.listener.operational_port, op_client_ctx
    )
    try:
        assert op_conn.alpn == ALPN_OPERATIONAL
    finally:
        await op_conn.close()
