"""Tests for :class:`mash_py.model.Endpoint`."""

from __future__ import annotations

import pytest

from mash_py.features.energy_control_gen import EnergyControl
from mash_py.features.measurement_gen import (
    MEASUREMENT_ATTR_STATE_OF_CHARGE,
    Measurement,
)
from mash_py.features.status_gen import Status
from mash_py.model import (
    Endpoint,
    EndpointType,
    FeatureType,
)
from mash_py.model.endpoint import (
    DuplicateFeatureError,
    FeatureNotFoundError,
)


def test_endpoint_construction() -> None:
    ep = Endpoint(1, EndpointType.EV_CHARGER, label="Port 1")
    assert ep.id == 1
    assert ep.endpoint_type == EndpointType.EV_CHARGER
    assert ep.label == "Port 1"
    assert ep.features() == []


def test_endpoint_id_must_be_uint8() -> None:
    with pytest.raises(ValueError):
        Endpoint(-1, EndpointType.EV_CHARGER)
    with pytest.raises(ValueError):
        Endpoint(256, EndpointType.EV_CHARGER)


def test_add_and_get_feature() -> None:
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    m = Measurement()
    ep.add_feature(m)
    assert ep.has_feature(FeatureType.MEASUREMENT)
    assert ep.get_feature(FeatureType.MEASUREMENT) is m
    assert ep.get_feature_by_id(int(FeatureType.MEASUREMENT)) is m


def test_duplicate_feature_rejected() -> None:
    """Endpoint refuses to overwrite a feature of the same type — surfaces accidental double-bind."""
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    ep.add_feature(Measurement())
    with pytest.raises(DuplicateFeatureError):
        ep.add_feature(Measurement())


def test_missing_feature_raises() -> None:
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    with pytest.raises(FeatureNotFoundError):
        ep.get_feature(FeatureType.MEASUREMENT)


def test_feature_types_sorted_by_id() -> None:
    """Sorted ordering matches the wire emission order for ``EndpointInfo.features``."""
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    ep.add_feature(EnergyControl())  # id 0x05
    ep.add_feature(Measurement())  # id 0x04
    ep.add_feature(Status())  # id 0x02

    types = ep.feature_types()
    assert types == sorted(types, key=int)
    assert types == [
        FeatureType.STATUS,
        FeatureType.MEASUREMENT,
        FeatureType.ENERGY_CONTROL,
    ]


def test_attribute_pass_through_via_endpoint() -> None:
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    m = Measurement()
    ep.add_feature(m)

    m.set_attribute_internal(MEASUREMENT_ATTR_STATE_OF_CHARGE, 60)
    assert ep.read_attribute(int(FeatureType.MEASUREMENT), MEASUREMENT_ATTR_STATE_OF_CHARGE) == 60


def test_has_attribute_pass_through() -> None:
    ep = Endpoint(1, EndpointType.EV_CHARGER)
    ep.add_feature(Measurement())
    assert ep.has_attribute(int(FeatureType.MEASUREMENT), MEASUREMENT_ATTR_STATE_OF_CHARGE)
    assert not ep.has_attribute(int(FeatureType.MEASUREMENT), 0xDEAD)
    # Unknown feature on this endpoint
    assert not ep.has_attribute(int(FeatureType.PLAN), 1)
