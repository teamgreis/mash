"""Tests for :class:`mash_py.model.Device` — Phase 13 acceptance.

Constructs the canonical EVSE shape from the plan:

    Endpoint 0 (DEVICE_ROOT)  → DeviceInfo
    Endpoint 1 (EV_CHARGER)   → Measurement, EnergyControl, Status

Then exercises in-memory R/W and verifies observers fire.
"""

from __future__ import annotations

import pytest

from mash_py.features.device_info_gen import (
    DEVICE_INFO_ATTR_DEVICE_ID,
    DEVICE_INFO_ATTR_VENDOR_NAME,
    DeviceInfo,
)
from mash_py.features.energy_control_gen import (
    ENERGY_CONTROL_ATTR_OPT_OUT_STATE,
    EnergyControl,
    OptOutState,
)
from mash_py.features.measurement_gen import (
    MEASUREMENT_ATTR_AC_ACTIVE_POWER,
    MEASUREMENT_ATTR_STATE_OF_CHARGE,
    Measurement,
)
from mash_py.features.status_gen import Status
from mash_py.model import (
    Device,
    Endpoint,
    EndpointType,
    FeatureType,
)
from mash_py.model.device import (
    DuplicateEndpointError,
    EndpointNotFoundError,
)

# ---------------------------------------------------------------------------
# Fixtures: build the EVSE example from docs/protocol-overview.md §4
# ---------------------------------------------------------------------------


def _build_evse(device_id: str = "wallbox-pulsarplus-WB2024001234") -> Device:
    """Single-port EVSE: DEVICE_ROOT + DeviceInfo, EV_CHARGER + telemetry/control/status."""
    device = Device(device_id)

    info = DeviceInfo()
    info.set_device_id(device_id)
    info.set_vendor_name("Wallbox")
    info.set_product_name("Pulsar Plus")
    info.set_serial_number("WB-2024-001234")
    info.set_software_version("3.2.1")
    info.set_spec_version("1.0")
    device.root.add_feature(info)

    port = Endpoint(1, EndpointType.EV_CHARGER, label="Port 1")
    port.add_feature(Measurement())
    port.add_feature(EnergyControl())
    port.add_feature(Status())
    device.add_endpoint(port)

    return device


# ---------------------------------------------------------------------------
# Construction
# ---------------------------------------------------------------------------


def test_device_root_endpoint_is_auto_created() -> None:
    """Endpoint 0 (DEVICE_ROOT) is always present per spec §4.4."""
    device = Device("evse-001")
    assert device.has_endpoint(0)
    assert device.root.id == 0
    assert device.root.endpoint_type == EndpointType.DEVICE_ROOT
    assert device.root.features() == []  # caller attaches DeviceInfo


def test_evse_composition() -> None:
    device = _build_evse()
    assert device.endpoint_ids() == [0, 1]
    assert device.endpoint_count() == 2

    root = device.root
    assert root.has_feature(FeatureType.DEVICE_INFO)

    port = device.get_endpoint(1)
    assert port.endpoint_type == EndpointType.EV_CHARGER
    assert port.has_feature(FeatureType.MEASUREMENT)
    assert port.has_feature(FeatureType.ENERGY_CONTROL)
    assert port.has_feature(FeatureType.STATUS)


def test_duplicate_endpoint_id_rejected() -> None:
    device = Device("evse-001")
    device.add_endpoint(Endpoint(1, EndpointType.EV_CHARGER))
    with pytest.raises(DuplicateEndpointError):
        device.add_endpoint(Endpoint(1, EndpointType.BATTERY))


def test_unknown_endpoint_raises() -> None:
    device = Device("evse-001")
    with pytest.raises(EndpointNotFoundError):
        device.get_endpoint(99)


# ---------------------------------------------------------------------------
# In-memory Read / Write (Phase 13 acceptance)
# ---------------------------------------------------------------------------


def test_read_device_info_attributes_via_device() -> None:
    """Read DeviceInfo identity attrs through ``Device.read_attribute(...)``."""
    device = _build_evse()

    assert device.read_attribute(0, FeatureType.DEVICE_INFO, DEVICE_INFO_ATTR_DEVICE_ID) == "wallbox-pulsarplus-WB2024001234"
    assert device.read_attribute(0, FeatureType.DEVICE_INFO, DEVICE_INFO_ATTR_VENDOR_NAME) == "Wallbox"


def test_write_attribute_external_succeeds_for_writable() -> None:
    """``optOutState`` (id 0x05 attr 1) is the read/write opt-out control."""
    device = _build_evse()

    device.write_attribute(
        1, FeatureType.ENERGY_CONTROL, ENERGY_CONTROL_ATTR_OPT_OUT_STATE, OptOutState.GRID
    )

    feat = device.get_feature(1, FeatureType.ENERGY_CONTROL)
    assert isinstance(feat, EnergyControl)
    assert feat.opt_out_state() == OptOutState.GRID


def test_telemetry_publishing_via_internal_setter() -> None:
    """Devices publish read-only telemetry through generated setters
    (which route to ``set_attribute_internal``)."""
    device = _build_evse()
    measurement = device.get_feature(1, FeatureType.MEASUREMENT)
    assert isinstance(measurement, Measurement)

    measurement.set_ac_active_power(11_000)  # 11 W
    measurement.set_state_of_charge(75)

    assert device.read_attribute(1, FeatureType.MEASUREMENT, MEASUREMENT_ATTR_AC_ACTIVE_POWER) == 11_000
    assert device.read_attribute(1, FeatureType.MEASUREMENT, MEASUREMENT_ATTR_STATE_OF_CHARGE) == 75


# ---------------------------------------------------------------------------
# Observers fire across the device
# ---------------------------------------------------------------------------


def test_observer_on_measurement_fires_during_telemetry_publish() -> None:
    device = _build_evse()
    measurement = device.get_feature(1, FeatureType.MEASUREMENT)

    received: list[tuple[FeatureType, int, object]] = []
    measurement.subscribe(lambda ft, aid, val: received.append((ft, aid, val)))

    measurement.set_ac_active_power(2200)

    assert received == [(FeatureType.MEASUREMENT, MEASUREMENT_ATTR_AC_ACTIVE_POWER, 2200)]


def test_observer_on_energy_control_fires_for_external_writes() -> None:
    device = _build_evse()
    ec = device.get_feature(1, FeatureType.ENERGY_CONTROL)

    received: list[OptOutState] = []
    ec.subscribe(lambda ft, aid, val: received.append(val))

    device.write_attribute(
        1, FeatureType.ENERGY_CONTROL, ENERGY_CONTROL_ATTR_OPT_OUT_STATE, OptOutState.GRID
    )
    device.write_attribute(
        1, FeatureType.ENERGY_CONTROL, ENERGY_CONTROL_ATTR_OPT_OUT_STATE, OptOutState.ALL
    )

    assert received == [OptOutState.GRID, OptOutState.ALL]


# ---------------------------------------------------------------------------
# Structural look-ups
# ---------------------------------------------------------------------------


def test_find_endpoints_by_type() -> None:
    device = _build_evse()
    chargers = device.find_endpoints_by_type(EndpointType.EV_CHARGER)
    assert [ep.id for ep in chargers] == [1]

    roots = device.find_endpoints_by_type(EndpointType.DEVICE_ROOT)
    assert [ep.id for ep in roots] == [0]


def test_find_endpoints_with_feature() -> None:
    device = _build_evse()

    eps_with_measurement = device.find_endpoints_with_feature(FeatureType.MEASUREMENT)
    assert [ep.id for ep in eps_with_measurement] == [1]

    eps_with_device_info = device.find_endpoints_with_feature(FeatureType.DEVICE_INFO)
    assert [ep.id for ep in eps_with_device_info] == [0]


def test_endpoints_iteration_order_is_by_id() -> None:
    """Deterministic ordering for downstream serialization."""
    device = Device("multi")
    device.add_endpoint(Endpoint(3, EndpointType.PV_STRING))
    device.add_endpoint(Endpoint(1, EndpointType.INVERTER))
    device.add_endpoint(Endpoint(2, EndpointType.BATTERY))
    assert [ep.id for ep in device.endpoints()] == [0, 1, 2, 3]
