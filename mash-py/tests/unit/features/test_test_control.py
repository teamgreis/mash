"""Unit tests for ``mash_py.features.test_control.TestControlFeature``.

What this covers (Phase 17 acceptance — unit layer):

- enable-key match → handler runs, response is ``{1: True}``,
- enable-key mismatch → :class:`Status.NOT_AUTHORIZED` (Spec Issue
  #15.A: matches Go),
- ``triggerTestEvent`` dispatches the registered callback with the
  correct opcode,
- async dispatcher callbacks are awaited,
- TEST-zone gate (Spec Issue #15.B) returns :class:`Status.NOT_AUTHORIZED`
  for non-TEST callers and degrades open without an accessor,
- ``setCommissioningWindowDuration`` clamps to ``[3, 10800]`` and
  delegates to its handler,
- raised :class:`CommandError` from a handler propagates to the wire
  dispatcher.
- ``testEventTriggersEnabled`` reflects ``isEnableKeyValid`` semantics
  (32 hex chars, non-zero).

These tests go through :class:`OperationalServer` so the integer-keyed
parameter wire-shape (``§7.1`` / Issue #11) is verified end-to-end.
"""

from __future__ import annotations

import pytest

from mash_py.features.test_control import (
    DEFAULT_ENABLE_KEY,
    TRIGGER_ADJUST_CLOCK_BASE,
    TRIGGER_ENTER_COMMISSIONING_MODE,
    TRIGGER_FACTORY_RESET,
    TRIGGER_SET_POWER_CUSTOM_BASE,
    TestControlFeature,
    trigger_domain,
)
from mash_py.features.test_control_gen import (
    SET_COMMISSIONING_WINDOW_DURATION_PARAM_DURATION_SECONDS,
    SET_COMMISSIONING_WINDOW_DURATION_PARAM_ENABLE_KEY,
    SET_COMMISSIONING_WINDOW_DURATION_RESP_SUCCESS,
    TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED,
    TEST_CONTROL_CMD_SET_COMMISSIONING_WINDOW_DURATION,
    TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
    TRIGGER_TEST_EVENT_PARAM_ENABLE_KEY,
    TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER,
    TRIGGER_TEST_EVENT_RESP_SUCCESS,
)
from mash_py.interaction.server import CommandError, OperationalServer
from mash_py.model import Device
from mash_py.model.feature_type_gen import FeatureType
from mash_py.wire import (
    ErrorPayload,
    Operation,
    Request,
    Status,
)

TEST_CONTROL_FEATURE_ID = int(FeatureType.TEST_CONTROL)


def _build_device_with_test_control(
    *,
    enable_key: str = DEFAULT_ENABLE_KEY,
    zone_type_accessor=None,
) -> tuple[Device, TestControlFeature]:
    device = Device("test-device-001")
    tc = TestControlFeature(
        enable_key=enable_key, zone_type_accessor=zone_type_accessor
    )
    device.root.add_feature(tc)
    return device, tc


# ---------------------------------------------------------------------------
# Trigger-domain helper
# ---------------------------------------------------------------------------


def test_trigger_domain_extracts_upper_two_bytes() -> None:
    assert trigger_domain(TRIGGER_ENTER_COMMISSIONING_MODE) == 0x0001
    assert trigger_domain(TRIGGER_ADJUST_CLOCK_BASE) == 0x0001
    assert trigger_domain(TRIGGER_SET_POWER_CUSTOM_BASE) == 0x0004
    # Synthetic — make sure we don't sign-extend anything.
    assert trigger_domain(0xABCD_0000_0000_0000) == 0xABCD


# ---------------------------------------------------------------------------
# testEventTriggersEnabled attribute
# ---------------------------------------------------------------------------


def test_test_event_triggers_enabled_reflects_default_key() -> None:
    tc = TestControlFeature()
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is True
    )


def test_test_event_triggers_enabled_false_for_all_zero_key() -> None:
    tc = TestControlFeature(enable_key="0" * 32)
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is False
    )


def test_test_event_triggers_enabled_false_for_short_key() -> None:
    tc = TestControlFeature(enable_key="abcd")
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is False
    )


def test_test_event_triggers_enabled_false_for_non_hex_key() -> None:
    # 32 chars, but not all hex.
    tc = TestControlFeature(enable_key="z" * 32)
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is False
    )


def test_set_enable_key_updates_attribute() -> None:
    tc = TestControlFeature(enable_key="0" * 32)
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is False
    )
    tc.set_enable_key(DEFAULT_ENABLE_KEY)
    assert (
        tc.read_attribute(TEST_CONTROL_ATTR_TEST_EVENT_TRIGGERS_ENABLED) is True
    )


# ---------------------------------------------------------------------------
# triggerTestEvent (cmd 0x01) via OperationalServer
# ---------------------------------------------------------------------------


def _trigger_request(
    *,
    enable_key: str,
    opcode: int,
    message_id: int = 1,
) -> Request:
    return Request(
        message_id=message_id,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=TEST_CONTROL_FEATURE_ID,
        payload={
            1: TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
            2: {
                TRIGGER_TEST_EVENT_PARAM_ENABLE_KEY: enable_key,
                TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER: opcode,
            },
        },
    )


@pytest.mark.asyncio
async def test_trigger_test_event_dispatches_handler_with_opcode() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    seen: list[int] = []
    tc.set_trigger_handler(lambda opcode: seen.append(opcode))

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.SUCCESS
    assert resp.payload == {TRIGGER_TEST_EVENT_RESP_SUCCESS: True}
    assert seen == [TRIGGER_ENTER_COMMISSIONING_MODE]


@pytest.mark.asyncio
async def test_trigger_test_event_async_handler_is_awaited() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    seen: list[int] = []

    async def handler(opcode: int) -> None:
        seen.append(opcode)

    tc.set_trigger_handler(handler)

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_FACTORY_RESET,
        )
    )

    assert resp.status == Status.SUCCESS
    assert seen == [TRIGGER_FACTORY_RESET]


@pytest.mark.asyncio
async def test_trigger_test_event_enable_key_mismatch_is_not_authorized() -> None:
    """Spec Issue #15.A: yaml says ``{success: bool}`` only, but mash-go
    surfaces ``Status.NOT_AUTHORIZED`` here. mash-py matches Go."""
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    resp = await server.handle_request(
        _trigger_request(
            enable_key="ff" * 16,  # wrong key, valid shape
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.NOT_AUTHORIZED
    assert isinstance(resp.payload, ErrorPayload)


@pytest.mark.asyncio
async def test_trigger_test_event_no_handler_registered_returns_unsupported() -> None:
    """Mirrors Go's ``handleTriggerTestEvent`` early-out when the device
    service didn't call ``OnTriggerTestEvent``. Go quietly returns
    ``{1: false}``; mash-py picks ``UNSUPPORTED`` so the failure is
    diagnosable from the wire (a bare ``{success: false}`` looks like a
    successful application-level no-op which is a debugging trap)."""
    device, _tc = _build_device_with_test_control()
    server = OperationalServer(device)
    # No set_trigger_handler call.

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.UNSUPPORTED


@pytest.mark.asyncio
async def test_trigger_test_event_handler_can_raise_command_error() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    def reject(_opcode: int) -> None:
        raise CommandError(
            Status.CONSTRAINT_ERROR, "Status feature not present"
        )

    tc.set_trigger_handler(reject)

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.CONSTRAINT_ERROR
    assert isinstance(resp.payload, ErrorPayload)
    assert "Status feature not present" in resp.payload.message


@pytest.mark.asyncio
async def test_trigger_test_event_missing_enable_key_param_is_invalid_parameter() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    req = Request(
        message_id=1,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=TEST_CONTROL_FEATURE_ID,
        payload={
            1: TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
            2: {
                TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER: TRIGGER_ENTER_COMMISSIONING_MODE,
                # missing enableKey
            },
        },
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_PARAMETER


@pytest.mark.asyncio
async def test_trigger_test_event_non_string_key_is_invalid_parameter() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    req = Request(
        message_id=1,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=TEST_CONTROL_FEATURE_ID,
        payload={
            1: TEST_CONTROL_CMD_TRIGGER_TEST_EVENT,
            2: {
                TRIGGER_TEST_EVENT_PARAM_ENABLE_KEY: 12345,  # int, not str
                TRIGGER_TEST_EVENT_PARAM_EVENT_TRIGGER: TRIGGER_ENTER_COMMISSIONING_MODE,
            },
        },
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_PARAMETER


# ---------------------------------------------------------------------------
# TEST-zone gate (Spec Issue #15.B)
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_zone_gate_rejects_non_test_zone() -> None:
    device, tc = _build_device_with_test_control(
        zone_type_accessor=lambda: "GRID",
    )
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.NOT_AUTHORIZED


@pytest.mark.asyncio
async def test_zone_gate_accepts_test_zone() -> None:
    device, tc = _build_device_with_test_control(
        zone_type_accessor=lambda: "TEST",
    )
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.SUCCESS


@pytest.mark.asyncio
async def test_zone_gate_rejects_unknown_zone() -> None:
    device, tc = _build_device_with_test_control(
        zone_type_accessor=lambda: None,
    )
    server = OperationalServer(device)
    tc.set_trigger_handler(lambda _opcode: None)

    resp = await server.handle_request(
        _trigger_request(
            enable_key=DEFAULT_ENABLE_KEY,
            opcode=TRIGGER_ENTER_COMMISSIONING_MODE,
        )
    )

    assert resp.status == Status.NOT_AUTHORIZED


# ---------------------------------------------------------------------------
# setCommissioningWindowDuration (cmd 0x02)
# ---------------------------------------------------------------------------


def _set_window_request(
    *,
    enable_key: str,
    duration: int,
) -> Request:
    return Request(
        message_id=1,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=TEST_CONTROL_FEATURE_ID,
        payload={
            1: TEST_CONTROL_CMD_SET_COMMISSIONING_WINDOW_DURATION,
            2: {
                SET_COMMISSIONING_WINDOW_DURATION_PARAM_ENABLE_KEY: enable_key,
                SET_COMMISSIONING_WINDOW_DURATION_PARAM_DURATION_SECONDS: duration,
            },
        },
    )


@pytest.mark.asyncio
async def test_set_window_duration_clamps_high() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    received: list[int] = []
    tc.set_duration_handler(lambda dur: received.append(dur))

    resp = await server.handle_request(
        _set_window_request(enable_key=DEFAULT_ENABLE_KEY, duration=99999)
    )

    assert resp.status == Status.SUCCESS
    assert resp.payload == {SET_COMMISSIONING_WINDOW_DURATION_RESP_SUCCESS: True}
    assert received == [10800]


@pytest.mark.asyncio
async def test_set_window_duration_clamps_low() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    received: list[int] = []
    tc.set_duration_handler(lambda dur: received.append(dur))

    resp = await server.handle_request(
        _set_window_request(enable_key=DEFAULT_ENABLE_KEY, duration=0)
    )

    assert resp.status == Status.SUCCESS
    assert received == [3]


@pytest.mark.asyncio
async def test_set_window_duration_passes_in_range() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)

    received: list[int] = []
    tc.set_duration_handler(lambda dur: received.append(dur))

    resp = await server.handle_request(
        _set_window_request(enable_key=DEFAULT_ENABLE_KEY, duration=600)
    )

    assert resp.status == Status.SUCCESS
    assert received == [600]


@pytest.mark.asyncio
async def test_set_window_duration_enable_key_mismatch_is_not_authorized() -> None:
    device, tc = _build_device_with_test_control()
    server = OperationalServer(device)
    tc.set_duration_handler(lambda _dur: None)

    resp = await server.handle_request(
        _set_window_request(enable_key="ff" * 16, duration=600)
    )

    assert resp.status == Status.NOT_AUTHORIZED


@pytest.mark.asyncio
async def test_set_window_duration_no_handler_returns_unsupported() -> None:
    device, _tc = _build_device_with_test_control()
    server = OperationalServer(device)

    resp = await server.handle_request(
        _set_window_request(enable_key=DEFAULT_ENABLE_KEY, duration=600)
    )

    assert resp.status == Status.UNSUPPORTED
