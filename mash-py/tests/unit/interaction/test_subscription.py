"""Unit tests for the Phase 16 subscription stack
(:mod:`mash_py.interaction.subscription` and the Subscribe / Unsubscribe
paths added to :class:`mash_py.interaction.OperationalServer`).

Coverage matches the Phase-16 acceptance gate per
``mash-py-tdd-plan.md §Phase 16``:

- Subscribe → priming-report contents per ``§6.2``.
- Mutate attributes → notification arrives within ``maxInterval``.
- Coalescing: rapid changes within ``minInterval`` collapse to one
  notification (``§6.5``).
- Bounce-back: a value identical to the last delivered value is
  suppressed (Phase 16 decision #3).
- Heartbeat: maxInterval elapses without changes → notification with
  current values (``§6.5``).
- Unsubscribe via the (endpointId=0, featureId=0) sentinel (``§6.4``).
- DEC-071 / §8.4 write-then-notify: the OperationalServer flushes due
  notifications synchronously inside ``_handle_write`` so a Subscribe
  on the same session sees the change before the Write Response is
  built.

These tests don't go through TLS or the wire codec — the operational
server's contract is ``Request → Response`` with a ``SessionContext``
attached, and ``SubscriptionManager`` exposes a sender callback that
we hook to a list. The full path is exercised in
``tests/interop/interaction/test_subscriptions.py`` (Go controller →
Python device).
"""

from __future__ import annotations

import asyncio
import time
from typing import Any

import pytest

from mash_py.features.device_info_gen import (
    DEVICE_INFO_ATTR_LABEL,
    DEVICE_INFO_ATTR_VENDOR_NAME,
    DeviceInfo,
)
from mash_py.features.status_gen import (
    STATUS_ATTR_OPERATING_STATE,
    OperatingState,
    Status as StatusFeature,
)
from mash_py.interaction.server import (
    OperationalServer,
    SessionContext,
)
from mash_py.interaction.subscription import (
    DEFAULT_MAX_INTERVAL_MS,
    DEFAULT_MIN_INTERVAL_MS,
    InvalidInterval,
    NotificationDispatcher,
    ResourceExhausted,
    Subscription,
    SubscriptionManager,
)
from mash_py.model import Device, Endpoint, EndpointType
from mash_py.wire import Operation, Request, Status

FEATURE_DEVICE_INFO = 0x01
FEATURE_STATUS = 0x02
EVSE_ENDPOINT_ID = 1


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


def _build_device() -> Device:
    device = Device("test-device-phase16")
    di = DeviceInfo()
    di.set_vendor_name("MASH-Phase16")
    di.set_product_name("Test EVSE")
    di.set_serial_number("PHASE16-0001")
    device.root.add_feature(di)

    evse = Endpoint(EVSE_ENDPOINT_ID, EndpointType.EV_CHARGER, "Phase 16 EVSE")
    status = StatusFeature()
    status.set_operating_state(OperatingState.STANDBY)
    evse.add_feature(status)
    device.add_endpoint(evse)

    return device


def _build_manager(device: Device) -> SubscriptionManager:
    return SubscriptionManager(
        read_attribute=lambda ep, fid, aid: device.get_endpoint(ep).read_attribute(fid, aid),
        read_all_attributes=lambda ep, fid: (
            device.get_endpoint(ep).get_feature_by_id(fid).read_all_attributes()
        ),
    )


@pytest.fixture
def device() -> Device:
    return _build_device()


@pytest.fixture
def manager(device: Device) -> SubscriptionManager:
    return _build_manager(device)


# ---------------------------------------------------------------------------
# SubscriptionManager — pure logic
# ---------------------------------------------------------------------------


def test_subscribe_returns_priming_with_all_attributes(manager: SubscriptionManager) -> None:
    sub_id, priming = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[],  # all
        min_interval_ms=DEFAULT_MIN_INTERVAL_MS,
        max_interval_ms=DEFAULT_MAX_INTERVAL_MS,
    )
    assert sub_id != 0, "subscriptionId 0 is reserved (Notification messageId sentinel)"
    assert priming.get(DEVICE_INFO_ATTR_VENDOR_NAME) == "MASH-Phase16"


def test_subscribe_with_specific_attribute_ids_filters_priming(
    manager: SubscriptionManager,
) -> None:
    _, priming = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_VENDOR_NAME],
        min_interval_ms=DEFAULT_MIN_INTERVAL_MS,
        max_interval_ms=DEFAULT_MAX_INTERVAL_MS,
    )
    assert list(priming.keys()) == [DEVICE_INFO_ATTR_VENDOR_NAME]


def test_subscribe_rejects_zero_max_interval(manager: SubscriptionManager) -> None:
    """Phase 16 decision #2: explicit maxInterval=0 is rejected."""
    with pytest.raises(InvalidInterval):
        manager.subscribe(
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            attribute_ids=[],
            min_interval_ms=1000,
            max_interval_ms=0,
        )


def test_subscribe_rejects_min_greater_than_max(manager: SubscriptionManager) -> None:
    with pytest.raises(InvalidInterval):
        manager.subscribe(
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            attribute_ids=[],
            min_interval_ms=2000,
            max_interval_ms=1000,
        )


def test_subscribe_caps_session_subscription_count(manager: SubscriptionManager) -> None:
    """Per-session cap surfaces as ResourceExhausted (mapped upstream
    to ``Status.RESOURCE_EXHAUSTED``)."""
    # Lower the cap by patching the module constant at the manager level;
    # easier than minting 51 subscriptions in a unit test.
    from mash_py.interaction import subscription as sub_mod

    original = sub_mod.MAX_SUBSCRIPTIONS_PER_SESSION
    sub_mod.MAX_SUBSCRIPTIONS_PER_SESSION = 2
    try:
        manager.subscribe(0, FEATURE_DEVICE_INFO, [], 1000, 60000)
        manager.subscribe(0, FEATURE_DEVICE_INFO, [], 1000, 60000)
        with pytest.raises(ResourceExhausted):
            manager.subscribe(0, FEATURE_DEVICE_INFO, [], 1000, 60000)
    finally:
        sub_mod.MAX_SUBSCRIPTIONS_PER_SESSION = original


def test_unsubscribe_returns_false_for_unknown_id(manager: SubscriptionManager) -> None:
    assert manager.unsubscribe(99999) is False


def test_record_change_within_min_interval_does_not_flush(
    manager: SubscriptionManager,
) -> None:
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_LABEL],
        min_interval_ms=10_000,  # 10s — guaranteed not to expire in this test
        max_interval_ms=60_000,
    )
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "new")
    due = manager.flush_due()
    assert due == [], (
        "minInterval has not elapsed; the change must remain coalesced "
        "(spec §6.5 minInterval-coalescing rule)"
    )


def test_record_change_after_min_interval_flushes(manager: SubscriptionManager) -> None:
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_LABEL],
        min_interval_ms=10,  # 10ms
        max_interval_ms=60_000,
    )
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "new")
    # Force the clock past minInterval.
    due = manager.flush_due(now=time.monotonic() + 1.0)
    assert len(due) == 1
    assert due[0].subscription_id == sub_id
    assert due[0].changes == {DEVICE_INFO_ATTR_LABEL: "new"}


def test_bounce_back_is_suppressed(manager: SubscriptionManager) -> None:
    """Phase 16 decision #3: writing the priming value back is no-notify."""
    _, priming = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_VENDOR_NAME],
        min_interval_ms=10,
        max_interval_ms=60_000,
    )
    assert priming[DEVICE_INFO_ATTR_VENDOR_NAME] == "MASH-Phase16"
    # Same value → bounce-back, no notification.
    manager.record_change(
        0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_VENDOR_NAME, "MASH-Phase16"
    )
    assert manager.flush_due(now=time.monotonic() + 1.0) == []


def test_coalesces_burst_into_single_delivery(manager: SubscriptionManager) -> None:
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[],  # all
        min_interval_ms=10,
        max_interval_ms=60_000,
    )
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "first")
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "second")
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "third")
    due = manager.flush_due(now=time.monotonic() + 1.0)
    assert len(due) == 1
    assert due[0].changes == {DEVICE_INFO_ATTR_LABEL: "third"}, (
        "Coalescing keeps only the latest value per attribute (§6.5)"
    )


def test_heartbeat_fires_after_max_interval(manager: SubscriptionManager) -> None:
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_VENDOR_NAME],
        min_interval_ms=10,
        max_interval_ms=50,  # 50 ms heartbeat
    )
    # Just after maxInterval, no changes → heartbeat with current values.
    due = manager.flush_due(now=time.monotonic() + 1.0)
    assert len(due) == 1
    assert due[0].subscription_id == sub_id
    assert due[0].changes == {DEVICE_INFO_ATTR_VENDOR_NAME: "MASH-Phase16"}, (
        "Heartbeat must carry current values (§6.5; Phase 16 decision #4)"
    )


def test_pending_changes_take_precedence_over_heartbeat(
    manager: SubscriptionManager,
) -> None:
    """When both a delta AND maxInterval are due, emit only the delta —
    the heartbeat clock is reset by the delivery."""
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[],
        min_interval_ms=10,
        max_interval_ms=50,
    )
    manager.record_change(0, FEATURE_DEVICE_INFO, DEVICE_INFO_ATTR_LABEL, "delta")
    due = manager.flush_due(now=time.monotonic() + 1.0)
    assert len(due) == 1
    # Should be the delta, not a full snapshot.
    assert due[0].changes == {DEVICE_INFO_ATTR_LABEL: "delta"}


# ---------------------------------------------------------------------------
# OperationalServer — Subscribe / Unsubscribe wire-level routing
# ---------------------------------------------------------------------------


@pytest.fixture
def session(device: Device, manager: SubscriptionManager) -> SessionContext:
    """A SessionContext whose dispatcher captures notifications into
    a list rather than encoding to a wire frame. Lets tests assert
    on the dispatch shape without the codec involved."""
    captured: list[tuple[int, int, int, dict[int, Any]]] = []

    async def fake_sender(sub_id: int, ep: int, fid: int, changes: dict[int, Any]) -> None:
        captured.append((sub_id, ep, fid, dict(changes)))

    dispatcher = NotificationDispatcher(manager, fake_sender)
    ctx = SessionContext(subscription_manager=manager, dispatcher=dispatcher)
    # Stash for tests to read.
    ctx.captured = captured  # type: ignore[attr-defined]
    return ctx


@pytest.mark.asyncio
async def test_subscribe_request_returns_priming_response(
    device: Device, session: SessionContext
) -> None:
    server = OperationalServer(device)
    req = Request(
        message_id=1,
        operation=Operation.SUBSCRIBE,
        endpoint_id=EVSE_ENDPOINT_ID,
        feature_id=FEATURE_STATUS,
        payload={1: [], 2: 1000, 3: 60000},
    )
    resp = await server.handle_request(req, session=session)
    assert resp.status == Status.SUCCESS
    assert isinstance(resp.payload, dict)
    sub_id = resp.payload[1]
    assert sub_id != 0
    priming = resp.payload[2]
    assert priming[STATUS_ATTR_OPERATING_STATE] == OperatingState.STANDBY


@pytest.mark.asyncio
async def test_subscribe_default_intervals_when_keys_omitted(
    device: Device, session: SessionContext
) -> None:
    """Phase 16 decisions #1 and #2: omitted keys 2 / 3 default to
    1000 / 60000 ms; explicit ``maxInterval=0`` is rejected."""
    server = OperationalServer(device)

    # Omitted intervals → defaults applied.
    resp = await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={1: []},
        ),
        session=session,
    )
    assert resp.status == Status.SUCCESS

    # Explicit maxInterval=0 → INVALID_PARAMETER.
    resp_bad = await server.handle_request(
        Request(
            message_id=2,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={1: [], 2: 1000, 3: 0},
        ),
        session=session,
    )
    assert resp_bad.status == Status.INVALID_PARAMETER


@pytest.mark.asyncio
async def test_subscribe_invalid_endpoint_status_precedence(
    device: Device, session: SessionContext
) -> None:
    server = OperationalServer(device)
    resp = await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=99,
            feature_id=FEATURE_STATUS,
            payload={1: []},
        ),
        session=session,
    )
    assert resp.status == Status.INVALID_ENDPOINT


@pytest.mark.asyncio
async def test_unsubscribe_via_zero_zero_sentinel(
    device: Device, session: SessionContext
) -> None:
    server = OperationalServer(device)
    sub_resp = await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=EVSE_ENDPOINT_ID,
            feature_id=FEATURE_STATUS,
            payload={1: [], 2: 1000, 3: 60000},
        ),
        session=session,
    )
    sub_id = sub_resp.payload[1]

    unsub_resp = await server.handle_request(
        Request(
            message_id=2,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=0,
            payload={1: sub_id},
        ),
        session=session,
    )
    assert unsub_resp.status == Status.SUCCESS
    assert session.subscription_manager.count() == 0


@pytest.mark.asyncio
async def test_subscribe_without_session_returns_unsupported(
    device: Device,
) -> None:
    """No SessionContext attached (e.g. unit-test callers) → ``UNSUPPORTED``.

    Phase 15 used this to communicate "Subscribe not implemented yet";
    Phase 16 keeps the surface for callers that genuinely don't need
    subscriptions (a Read-only test) — the dispatcher has nowhere to
    keep state without a session."""
    server = OperationalServer(device)
    resp = await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={1: []},
        )
    )
    assert resp.status == Status.UNSUPPORTED


# ---------------------------------------------------------------------------
# DEC-071: write-then-notify ordering
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_write_flushes_due_notifications_before_response(
    device: Device, session: SessionContext
) -> None:
    """Per DEC-071 / transport.md §8.4: when a Write commits, the
    OperationalServer flushes any due notifications synchronously
    BEFORE returning the Write Response.

    We subscribe with a tiny minInterval so the change is immediately
    "due", issue a Write, and verify (a) the captured-notifications
    list grew, and (b) the Write Response carries the new value.
    """
    server = OperationalServer(device)

    sub_resp = await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={1: [DEVICE_INFO_ATTR_LABEL], 2: 1, 3: 60000},
        ),
        session=session,
    )
    assert sub_resp.status == Status.SUCCESS
    sub_id = sub_resp.payload[1]

    # Sleep just past the 1 ms minInterval so the next change is "due".
    await asyncio.sleep(0.005)

    write_resp = await server.handle_request(
        Request(
            message_id=2,
            operation=Operation.WRITE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={DEVICE_INFO_ATTR_LABEL: "after-write"},
        ),
        session=session,
    )
    assert write_resp.status == Status.SUCCESS
    assert write_resp.payload[DEVICE_INFO_ATTR_LABEL] == "after-write"

    captured = session.captured  # type: ignore[attr-defined]
    assert len(captured) == 1, (
        "DEC-071: the notification MUST be delivered before the Write Response"
    )
    sub_id_seen, ep_seen, fid_seen, changes_seen = captured[0]
    assert sub_id_seen == sub_id
    assert ep_seen == 0
    assert fid_seen == FEATURE_DEVICE_INFO
    assert changes_seen == {DEVICE_INFO_ATTR_LABEL: "after-write"}


@pytest.mark.asyncio
async def test_write_notification_buffers_inside_min_interval(
    device: Device, session: SessionContext
) -> None:
    """If a Write happens *within* minInterval, no notification fires
    yet — the change is buffered for the next flush. Per
    transport.md §8.4: failure to deliver MUST NOT block the Write Response.
    """
    server = OperationalServer(device)

    await server.handle_request(
        Request(
            message_id=1,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={1: [DEVICE_INFO_ATTR_LABEL], 2: 60_000, 3: 120_000},
        ),
        session=session,
    )

    write_resp = await server.handle_request(
        Request(
            message_id=2,
            operation=Operation.WRITE,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload={DEVICE_INFO_ATTR_LABEL: "queued"},
        ),
        session=session,
    )
    assert write_resp.status == Status.SUCCESS, (
        "Write Response MUST succeed even though no notification fired (§8.4)"
    )
    assert session.captured == []  # type: ignore[attr-defined]


# ---------------------------------------------------------------------------
# NotificationDispatcher polling task
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_dispatcher_polling_emits_heartbeat(device: Device) -> None:
    """End-to-end: start the polling task with a fast cadence and a
    short maxInterval; assert the sender receives a heartbeat with
    current values."""
    manager = _build_manager(device)

    sent: list[tuple[int, int, int, dict[int, Any]]] = []

    async def fake_sender(sub_id: int, ep: int, fid: int, changes: dict[int, Any]) -> None:
        sent.append((sub_id, ep, fid, dict(changes)))

    dispatcher = NotificationDispatcher(
        manager, fake_sender, processing_interval_ms=10
    )
    sub_id, _ = manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_VENDOR_NAME],
        min_interval_ms=5,
        max_interval_ms=30,
    )
    dispatcher.start()
    try:
        # Give the polling task time to fire at least one heartbeat.
        for _ in range(20):
            if sent:
                break
            await asyncio.sleep(0.05)
    finally:
        await dispatcher.stop()

    assert sent, "polling task did not emit a heartbeat within the budget"
    sent_sub, sent_ep, sent_fid, sent_changes = sent[0]
    assert sent_sub == sub_id
    assert sent_ep == 0
    assert sent_fid == FEATURE_DEVICE_INFO
    assert sent_changes == {DEVICE_INFO_ATTR_VENDOR_NAME: "MASH-Phase16"}


@pytest.mark.asyncio
async def test_dispatcher_swallows_sender_errors(device: Device) -> None:
    """Per §8.4: a delivery failure must not propagate. Stopping a
    dispatcher whose sender raises should not raise."""
    manager = _build_manager(device)

    async def bad_sender(*_args, **_kw) -> None:
        raise RuntimeError("simulated send failure")

    dispatcher = NotificationDispatcher(manager, bad_sender, processing_interval_ms=5)
    manager.subscribe(
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        attribute_ids=[DEVICE_INFO_ATTR_VENDOR_NAME],
        min_interval_ms=1,
        max_interval_ms=10,
    )
    dispatcher.start()
    await asyncio.sleep(0.05)
    await dispatcher.stop()
    # Reaching here without an unhandled exception is the assertion.
