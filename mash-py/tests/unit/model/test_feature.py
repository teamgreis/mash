"""Tests for the :mod:`mash_py.model.feature` runtime base classes.

Phase 13 acceptance: in-memory R/W works, validation enforces declared
constraints, observers fire on changes. Tests pin behavior using the
generated ``Measurement`` (read-only telemetry) and ``EnergyControl``
(read/write controls) features so we exercise both write paths.
"""

from __future__ import annotations

import pytest

from mash_py.features.energy_control_gen import (
    ENERGY_CONTROL_ATTR_OPT_OUT_STATE,
    EnergyControl,
    OptOutState,
)
from mash_py.features.measurement_gen import (
    MEASUREMENT_ATTR_AC_ACTIVE_POWER,
    MEASUREMENT_ATTR_AC_FREQUENCY,
    MEASUREMENT_ATTR_STATE_OF_CHARGE,
    Measurement,
)
from mash_py.model import (
    Access,
    AttributeMetadata,
    AttributeNotFoundError,
    AttributeNotNullableError,
    AttributeNotWritableError,
    AttributeOutOfRangeError,
    AttributeValueTypeError,
    DataType,
    Feature,
    FeatureType,
)

# ---------------------------------------------------------------------------
# Construction and lookup
# ---------------------------------------------------------------------------


def test_generated_feature_instantiates() -> None:
    """Generated features instantiate and expose attribute ids in sorted order."""
    m = Measurement()
    assert m.feature_type == FeatureType.MEASUREMENT
    assert m.revision >= 1
    ids = m.attribute_ids()
    assert ids == sorted(ids)
    assert MEASUREMENT_ATTR_AC_ACTIVE_POWER in ids


def test_unknown_attribute_raises() -> None:
    m = Measurement()
    with pytest.raises(AttributeNotFoundError):
        m.read_attribute(0xDEAD)


# ---------------------------------------------------------------------------
# Access enforcement
# ---------------------------------------------------------------------------


def test_external_write_to_read_only_attribute_raises() -> None:
    """Generated read-only attrs reject external ``write_attribute`` calls."""
    m = Measurement()
    with pytest.raises(AttributeNotWritableError):
        m.write_attribute(MEASUREMENT_ATTR_AC_ACTIVE_POWER, 12345)


def test_internal_setter_bypasses_access_check() -> None:
    """Generated setters route through ``set_attribute_internal`` for read-only attrs."""
    m = Measurement()
    m.set_ac_active_power(7777)
    assert m.ac_active_power() == 7777


def test_set_attribute_internal_directly_works() -> None:
    m = Measurement()
    m.set_attribute_internal(MEASUREMENT_ATTR_AC_ACTIVE_POWER, 42)
    assert m.read_attribute(MEASUREMENT_ATTR_AC_ACTIVE_POWER) == 42


def test_external_write_to_read_write_attribute_succeeds() -> None:
    ec = EnergyControl()
    ec.set_opt_out_state(OptOutState.LOCAL)
    assert ec.opt_out_state() == OptOutState.LOCAL


# ---------------------------------------------------------------------------
# Validation: type, nullable, range
# ---------------------------------------------------------------------------


def test_nullable_attribute_accepts_none() -> None:
    m = Measurement()
    m.set_ac_active_power(1234)
    m.clear_ac_active_power()  # writes None
    assert m.ac_active_power() is None


def test_non_nullable_attribute_rejects_none() -> None:
    f = Feature(FeatureType.MEASUREMENT, 1)
    f.add_attribute(
        AttributeMetadata(
            id=1,
            name="counter",
            type=DataType.UINT32,
            access=Access.READ_WRITE,
            nullable=False,
            default=0,
        )
    )
    with pytest.raises(AttributeNotNullableError):
        f.write_attribute(1, None)


def test_type_check_rejects_string_for_int_attribute() -> None:
    m = Measurement()
    with pytest.raises(AttributeValueTypeError):
        m.set_attribute_internal(MEASUREMENT_ATTR_AC_ACTIVE_POWER, "not-an-int")


def test_type_check_rejects_bool_for_int_attribute() -> None:
    """``bool`` is a subclass of ``int`` in Python — verify we reject it explicitly."""
    m = Measurement()
    with pytest.raises(AttributeValueTypeError):
        m.set_attribute_internal(MEASUREMENT_ATTR_AC_ACTIVE_POWER, True)


def test_range_check_below_minimum() -> None:
    """``acFrequency`` declares min 45000 mHz (DEC-070)."""
    m = Measurement()
    with pytest.raises(AttributeOutOfRangeError):
        m.set_attribute_internal(MEASUREMENT_ATTR_AC_FREQUENCY, 1000)


def test_range_check_above_maximum() -> None:
    """``stateOfCharge`` declares max 100%."""
    m = Measurement()
    with pytest.raises(AttributeOutOfRangeError):
        m.set_attribute_internal(MEASUREMENT_ATTR_STATE_OF_CHARGE, 200)


def test_range_check_at_boundaries_passes() -> None:
    m = Measurement()
    m.set_attribute_internal(MEASUREMENT_ATTR_STATE_OF_CHARGE, 0)
    m.set_attribute_internal(MEASUREMENT_ATTR_STATE_OF_CHARGE, 100)
    assert m.state_of_charge() == 100


# ---------------------------------------------------------------------------
# Observer notifications
# ---------------------------------------------------------------------------


def test_observer_fires_on_external_write() -> None:
    ec = EnergyControl()
    events: list[tuple[FeatureType, int, object]] = []
    ec.subscribe(lambda ft, aid, val: events.append((ft, aid, val)))

    ec.set_opt_out_state(OptOutState.LOCAL)

    assert events == [
        (FeatureType.ENERGY_CONTROL, ENERGY_CONTROL_ATTR_OPT_OUT_STATE, OptOutState.LOCAL)
    ]


def test_observer_fires_on_internal_write() -> None:
    m = Measurement()
    events: list[tuple[FeatureType, int, object]] = []
    m.subscribe(lambda ft, aid, val: events.append((ft, aid, val)))

    m.set_ac_active_power(8888)

    assert events == [(FeatureType.MEASUREMENT, MEASUREMENT_ATTR_AC_ACTIVE_POWER, 8888)]


def test_observer_does_not_fire_for_unchanged_value() -> None:
    """Equality-guarded notification: writing the same value shouldn't re-fire."""
    m = Measurement()
    m.set_ac_active_power(100)

    events: list[tuple[FeatureType, int, object]] = []
    m.subscribe(lambda ft, aid, val: events.append((ft, aid, val)))

    m.set_ac_active_power(100)  # same value
    assert events == []

    m.set_ac_active_power(200)  # actual change
    assert len(events) == 1


def test_observer_unsubscribe_stops_notifications() -> None:
    m = Measurement()
    events: list[int] = []

    def cb(ft: FeatureType, aid: int, val: object) -> None:
        events.append(aid)

    m.subscribe(cb)
    m.set_ac_active_power(10)
    m.unsubscribe(cb)
    m.set_ac_active_power(20)

    assert events == [MEASUREMENT_ATTR_AC_ACTIVE_POWER]


def test_multiple_observers_all_fire() -> None:
    m = Measurement()
    a: list[int] = []
    b: list[int] = []
    m.subscribe(lambda ft, aid, val: a.append(aid))
    m.subscribe(lambda ft, aid, val: b.append(aid))

    m.set_ac_active_power(5)
    assert a == b == [MEASUREMENT_ATTR_AC_ACTIVE_POWER]


# ---------------------------------------------------------------------------
# Dirty tracking
# ---------------------------------------------------------------------------


def test_dirty_attributes_tracks_changes() -> None:
    m = Measurement()
    assert m.dirty_attributes() == {}

    m.set_ac_active_power(1)
    m.set_attribute_internal(MEASUREMENT_ATTR_STATE_OF_CHARGE, 50)

    dirty = m.dirty_attributes()
    assert dirty[MEASUREMENT_ATTR_AC_ACTIVE_POWER] == 1
    assert dirty[MEASUREMENT_ATTR_STATE_OF_CHARGE] == 50

    m.clear_dirty()
    assert m.dirty_attributes() == {}


def test_unchanged_write_does_not_dirty() -> None:
    m = Measurement()
    m.set_ac_active_power(100)
    m.clear_dirty()
    m.set_ac_active_power(100)  # same value
    assert m.dirty_attributes() == {}


# ---------------------------------------------------------------------------
# Read-all and feature map
# ---------------------------------------------------------------------------


def test_read_all_attributes_returns_only_readable() -> None:
    m = Measurement()
    m.set_ac_active_power(123)
    snapshot = m.read_all_attributes()
    # Every Measurement attribute is readOnly → readable.
    assert MEASUREMENT_ATTR_AC_ACTIVE_POWER in snapshot
    assert snapshot[MEASUREMENT_ATTR_AC_ACTIVE_POWER] == 123


def test_set_feature_map_round_trip() -> None:
    m = Measurement()
    m.set_feature_map(0xCAFE)
    assert m.feature_map == 0xCAFE
