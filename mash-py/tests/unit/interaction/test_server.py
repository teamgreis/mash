"""Unit tests for the device-side request dispatcher
(:class:`mash_py.interaction.OperationalServer`).

Coverage matches the Phase-15 acceptance gate:

- Read / Write / Invoke routing against a real :class:`Device`
- All Status codes the dispatcher emits per ``§8.1``
- Spec-strict bare-array Read payload (``§4.1``)
- Spec-strict Write payload validation (type, range, read-only)
- Spec-strict Invoke parameter handling (integer keys, ``§7.1``)
- Subscribe → ``UNSUPPORTED`` until Phase 16

The tests don't go through the wire codec — the server's contract is
``Request → Response``. The codec is exercised separately in
``tests/unit/wire/`` and end-to-end in the Phase-15 interop test.
"""

from __future__ import annotations

import pytest

from mash_py.features.device_info_gen import (
    DEVICE_INFO_ATTR_LABEL,
    DEVICE_INFO_ATTR_LOCATION,
    DEVICE_INFO_ATTR_VENDOR_NAME,
    DEVICE_INFO_CMD_REMOVE_ZONE,
    REMOVE_ZONE_PARAM_ZONE_ID,
    REMOVE_ZONE_RESP_REMOVED,
    DeviceInfo,
)
from mash_py.features.measurement_gen import (
    MEASUREMENT_ATTR_AC_ACTIVE_POWER,
    Measurement,
)
from mash_py.interaction.server import CommandError, OperationalServer
from mash_py.model import (
    Device,
    Endpoint,
    EndpointType,
)
from mash_py.wire import (
    ErrorPayload,
    Operation,
    Request,
    Status,
)

# The protocol-versions YAML pins these (and the codegen reuses them);
# referencing them as locals keeps the test independent of the gen-file
# import surface for readability.
FEATURE_DEVICE_INFO = 0x01
FEATURE_MEASUREMENT = 0x04


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


def _build_device() -> Device:
    """Compose a small device:

    - endpoint 0 (DEVICE_ROOT) carries DeviceInfo
    - endpoint 1 (EV_CHARGER) carries Measurement
    """
    device = Device("test-device-001")

    di = DeviceInfo()
    di.set_vendor_name("MASH-Phase15")
    di.set_product_name("Test EVSE")
    di.set_serial_number("TEST-0001")
    device.root.add_feature(di)

    evse = Endpoint(1, EndpointType.EV_CHARGER, "Phase 15 EVSE")
    measurement = Measurement()
    evse.add_feature(measurement)
    device.add_endpoint(evse)

    return device


@pytest.fixture
def device() -> Device:
    return _build_device()


@pytest.fixture
def server(device: Device) -> OperationalServer:
    return OperationalServer(device)


# ---------------------------------------------------------------------------
# Read
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_read_all_attributes_on_root_device_info(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=1,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload=[],  # spec §4.3 empty = read all
    )
    resp = await server.handle_request(req)

    assert resp.status == Status.SUCCESS
    assert resp.message_id == 1
    assert isinstance(resp.payload, dict)
    # Vendor name we set in the fixture should be present.
    assert resp.payload[DEVICE_INFO_ATTR_VENDOR_NAME] == "MASH-Phase15"


@pytest.mark.asyncio
async def test_read_specific_attributes_bare_array_form(
    server: OperationalServer,
) -> None:
    """Spec §4.1 wire form — payload is a bare list of attribute IDs."""
    req = Request(
        message_id=2,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload=[DEVICE_INFO_ATTR_VENDOR_NAME, DEVICE_INFO_ATTR_LABEL],
    )
    resp = await server.handle_request(req)

    assert resp.status == Status.SUCCESS
    assert isinstance(resp.payload, dict)
    assert set(resp.payload.keys()) == {
        DEVICE_INFO_ATTR_VENDOR_NAME,
        DEVICE_INFO_ATTR_LABEL,
    }


@pytest.mark.asyncio
async def test_read_specific_attributes_legacy_map_form(
    server: OperationalServer,
) -> None:
    """Defensive: accept Go's legacy ``{1: [...]}`` shape too.

    A Go controller using ``*wire.ReadPayload{...}`` instead of a bare
    slice would land here. Keeping mash-py tolerant means a future
    Go-version diff doesn't break interop on the device side.
    """
    req = Request(
        message_id=3,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={1: [DEVICE_INFO_ATTR_VENDOR_NAME]},
    )
    resp = await server.handle_request(req)

    assert resp.status == Status.SUCCESS
    assert isinstance(resp.payload, dict)
    assert DEVICE_INFO_ATTR_VENDOR_NAME in resp.payload


@pytest.mark.asyncio
async def test_read_unknown_endpoint(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=4,
        operation=Operation.READ,
        endpoint_id=99,
        feature_id=FEATURE_DEVICE_INFO,
        payload=[],
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_ENDPOINT
    assert isinstance(resp.payload, ErrorPayload)


@pytest.mark.asyncio
async def test_read_unknown_feature_on_known_endpoint(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=5,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=0xFE,  # unassigned
        payload=[],
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_FEATURE


@pytest.mark.asyncio
async def test_read_unknown_attribute_returns_invalid_attribute(
    server: OperationalServer,
) -> None:
    """Strict-per-ID first-failure-wins, matching mash-go (decision C)."""
    req = Request(
        message_id=6,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload=[DEVICE_INFO_ATTR_VENDOR_NAME, 9999],
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_ATTRIBUTE


@pytest.mark.asyncio
async def test_read_malformed_payload_is_invalid_parameter(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=7,
        operation=Operation.READ,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload="not a list",
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_PARAMETER


# ---------------------------------------------------------------------------
# Write
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_write_writable_attribute_echoes_stored_value(
    server: OperationalServer, device: Device,
) -> None:
    label = "phase15-write-test"
    req = Request(
        message_id=10,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={DEVICE_INFO_ATTR_LABEL: label},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.SUCCESS
    assert isinstance(resp.payload, dict)
    assert resp.payload[DEVICE_INFO_ATTR_LABEL] == label
    # Side effect: device model actually has the new label.
    di = device.get_feature(0, DeviceInfo().feature_type)
    assert di.read_attribute(DEVICE_INFO_ATTR_LABEL) == label


@pytest.mark.asyncio
async def test_write_clearing_nullable_attribute(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=11,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={DEVICE_INFO_ATTR_LOCATION: None},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.SUCCESS


@pytest.mark.asyncio
async def test_write_readonly_attribute_returns_read_only_status(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=12,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={DEVICE_INFO_ATTR_VENDOR_NAME: "tampered"},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.READ_ONLY


@pytest.mark.asyncio
async def test_write_unknown_attribute_returns_invalid_attribute(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=13,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={9999: "anything"},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_ATTRIBUTE


@pytest.mark.asyncio
async def test_write_wrong_type_returns_constraint_error(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=14,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        # label is STRING; supplying an int violates type validation.
        payload={DEVICE_INFO_ATTR_LABEL: 42},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.CONSTRAINT_ERROR


@pytest.mark.asyncio
async def test_write_empty_payload_is_invalid_parameter(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=15,
        operation=Operation.WRITE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_PARAMETER


# ---------------------------------------------------------------------------
# Invoke
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_invoke_runs_registered_handler_with_integer_keyed_params(
    server: OperationalServer, device: Device,
) -> None:
    """Spec §7.1: parameters use integer keys (Issue #11)."""
    seen: dict[str, object] = {}

    def remove_zone_handler(params: dict[int, object]) -> dict[int, object]:
        seen["params"] = params
        return {REMOVE_ZONE_RESP_REMOVED: True}

    di = device.get_feature(0, DeviceInfo().feature_type)
    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, remove_zone_handler)

    req = Request(
        message_id=20,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={
            1: DEVICE_INFO_CMD_REMOVE_ZONE,
            2: {REMOVE_ZONE_PARAM_ZONE_ID: "zone-xyz"},
        },
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.SUCCESS
    assert resp.payload == {REMOVE_ZONE_RESP_REMOVED: True}
    assert seen["params"] == {REMOVE_ZONE_PARAM_ZONE_ID: "zone-xyz"}


@pytest.mark.asyncio
async def test_invoke_unregistered_command_returns_unsupported(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=21,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={1: DEVICE_INFO_CMD_REMOVE_ZONE},
    )
    resp = await server.handle_request(req)
    # No handler installed on this fresh DeviceInfo → UNSUPPORTED, not
    # INVALID_COMMAND. INVALID_COMMAND is reserved for "this command is
    # not in the YAML at all".
    assert resp.status == Status.UNSUPPORTED


@pytest.mark.asyncio
async def test_invoke_unknown_command_returns_invalid_command(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=22,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={1: 0xEE},  # not in DeviceInfo YAML
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_COMMAND


@pytest.mark.asyncio
async def test_invoke_handler_can_raise_command_error_to_pick_status(
    server: OperationalServer, device: Device,
) -> None:
    def reject(_params: dict[int, object]) -> dict[int, object]:
        raise CommandError(Status.CONSTRAINT_ERROR, "zoneId mismatch")

    di = device.get_feature(0, DeviceInfo().feature_type)
    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, reject)

    req = Request(
        message_id=23,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={1: DEVICE_INFO_CMD_REMOVE_ZONE, 2: {1: "wrong"}},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.CONSTRAINT_ERROR
    assert isinstance(resp.payload, ErrorPayload)
    assert "zoneId mismatch" in resp.payload.message


@pytest.mark.asyncio
async def test_invoke_async_handler_is_awaited(
    server: OperationalServer, device: Device,
) -> None:
    async def async_handler(_params: dict[int, object]) -> dict[int, object]:
        return {REMOVE_ZONE_RESP_REMOVED: True}

    di = device.get_feature(0, DeviceInfo().feature_type)
    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, async_handler)

    req = Request(
        message_id=24,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload={1: DEVICE_INFO_CMD_REMOVE_ZONE, 2: {1: "z"}},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.SUCCESS
    assert resp.payload == {REMOVE_ZONE_RESP_REMOVED: True}


@pytest.mark.asyncio
async def test_invoke_malformed_payload_is_invalid_parameter(
    server: OperationalServer,
) -> None:
    req = Request(
        message_id=25,
        operation=Operation.INVOKE,
        endpoint_id=0,
        feature_id=FEATURE_DEVICE_INFO,
        payload="not a map",
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.INVALID_PARAMETER


# ---------------------------------------------------------------------------
# Subscribe (Phase 16 placeholder)
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_subscribe_returns_unsupported(server: OperationalServer) -> None:
    req = Request(
        message_id=30,
        operation=Operation.SUBSCRIBE,
        endpoint_id=1,
        feature_id=FEATURE_MEASUREMENT,
        payload={},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.UNSUPPORTED


@pytest.mark.asyncio
async def test_unsubscribe_returns_unsupported(server: OperationalServer) -> None:
    """The §6.4 unsubscribe sentinel still hits the Phase-16-pending path."""
    req = Request(
        message_id=31,
        operation=Operation.SUBSCRIBE,
        endpoint_id=0,
        feature_id=0,
        payload={1: 5001},
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.UNSUPPORTED


# ---------------------------------------------------------------------------
# Cross-feature routing — make sure Measurement on endpoint 1 is reachable
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_read_measurement_after_telemetry_published(
    server: OperationalServer, device: Device,
) -> None:
    """Telemetry published via the internal setter should be readable."""
    measurement = device.get_feature(1, Measurement().feature_type)
    measurement.set_attribute_internal(MEASUREMENT_ATTR_AC_ACTIVE_POWER, 5_000_000)

    req = Request(
        message_id=40,
        operation=Operation.READ,
        endpoint_id=1,
        feature_id=FEATURE_MEASUREMENT,
        payload=[MEASUREMENT_ATTR_AC_ACTIVE_POWER],
    )
    resp = await server.handle_request(req)
    assert resp.status == Status.SUCCESS
    assert isinstance(resp.payload, dict)
    assert resp.payload[MEASUREMENT_ATTR_AC_ACTIVE_POWER] == 5_000_000
