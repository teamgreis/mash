"""Tests for the YAML loaders in ``codegen.specparse``.

These tests exercise the loaders against the real spec inputs in
``docs/`` because the spec is the authoritative source — fabricated
fixtures would risk drift the moment a real schema field is added.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from codegen import specparse

# ---------------------------------------------------------------------------
# Feature loading
# ---------------------------------------------------------------------------


def test_load_measurement_feature(features_dir: Path) -> None:
    feat = specparse.load_feature(features_dir / "measurement" / "1.0.yaml")
    assert feat.name == "Measurement"
    assert feat.id == 0x04
    assert feat.revision == 1

    # Spot-check well-known attribute IDs from the YAML.
    by_id = {a.id: a for a in feat.attributes}
    assert by_id[1].name == "acActivePower"
    assert by_id[1].type == "int64"
    assert by_id[1].nullable is True
    assert by_id[50].name == "stateOfCharge"
    assert by_id[50].min == 0
    assert by_id[50].max == 100


def test_load_charging_session_keeps_off_as_string(features_dir: Path) -> None:
    """``ChargingMode.OFF`` would be coerced to ``False`` under YAML 1.1.

    The custom :class:`_Yaml12SafeLoader` strips that resolver; we keep a
    regression test in case the resolver gets reset or pyyaml's table
    layout changes.
    """
    feat = specparse.load_feature(features_dir / "charging-session" / "1.0.yaml")
    charging_mode = next(e for e in feat.enums if e.name == "ChargingMode")
    member_names = [v.name for v in charging_mode.values]
    assert "OFF" in member_names, member_names
    assert False not in member_names


def test_load_test_control_command_with_response(features_dir: Path) -> None:
    feat = specparse.load_feature(features_dir / "test-control" / "1.0.yaml")
    cmd = next(c for c in feat.commands if c.name == "triggerTestEvent")
    assert cmd.id == 1
    param_ids = {p.id: p.name for p in cmd.parameters}
    assert param_ids == {1: "enableKey", 2: "eventTrigger"}
    resp_ids = {r.id: r.name for r in cmd.response}
    assert resp_ids == {1: "success"}


def test_load_signals_array_of_objects(features_dir: Path) -> None:
    """Array-of-object attributes carry ``items.type=object`` + ``items.fields``."""
    feat = specparse.load_feature(features_dir / "signals" / "1.0.yaml")
    # The send* commands declare slot arrays as their parameters.
    cmd = next(c for c in feat.commands if c.name == "sendPriceSignal")
    slots_param = next(p for p in cmd.parameters if p.name == "slots")
    assert slots_param.items is not None
    assert slots_param.items.type == "object"
    assert slots_param.items.structName == "PriceSlot"


# ---------------------------------------------------------------------------
# Protocol / shared / use case loading
# ---------------------------------------------------------------------------


def test_load_protocol_version_canonical_order(features_dir: Path) -> None:
    pv = specparse.load_protocol_version(features_dir / "protocol-versions.yaml", "1.0")
    # feature_types is a slice in YAML — order is the spec author's
    # declaration order. The generator depends on this for deterministic
    # output, so the loader must preserve it.
    names = [t.name for t in pv.feature_types]
    assert names == [
        "DeviceInfo",
        "Status",
        "Electrical",
        "Measurement",
        "EnergyControl",
        "ChargingSession",
        "Tariff",
        "Signals",
        "Plan",
        "TestControl",
    ]
    # Endpoint registry is also a slice; spot-check.
    ep_names = [t.name for t in pv.endpoint_types]
    assert ep_names[0] == "DEVICE_ROOT"
    assert "EV_CHARGER" in ep_names


def test_load_protocol_version_unknown_version_raises(features_dir: Path) -> None:
    with pytest.raises(KeyError):
        specparse.load_protocol_version(features_dir / "protocol-versions.yaml", "0.0")


def test_load_shared_enums(shared_path: Path) -> None:
    shared = specparse.load_shared(shared_path)
    enum_names = [e.name for e in shared.enums]
    assert {"Phase", "GridPhase", "PhasePair", "Direction", "AsymmetricSupport"}.issubset(
        enum_names
    )


def test_load_use_case_evc(usecases_dir: Path) -> None:
    uc = specparse.load_use_case(usecases_dir / "evc.yaml")
    assert uc.name == "EVC"
    assert uc.id == 0x03
    base = next(s for s in uc.scenarios if s.bit == 0)
    assert base.name == "BASE"
    feat_names = [f.feature for f in base.features]
    assert "EnergyControl" in feat_names
    assert "Measurement" in feat_names


# ---------------------------------------------------------------------------
# load_all_features + validation
# ---------------------------------------------------------------------------


def test_load_all_features_iterates_in_registry_order(features_dir: Path) -> None:
    pv = specparse.load_protocol_version(features_dir / "protocol-versions.yaml", "1.0")
    feats = specparse.load_all_features(features_dir, pv)
    assert [f.name for f in feats] == [t.name for t in pv.feature_types]


def test_validate_feature_ids_passes_on_real_spec(features_dir: Path) -> None:
    pv = specparse.load_protocol_version(features_dir / "protocol-versions.yaml", "1.0")
    feats = specparse.load_all_features(features_dir, pv)
    specparse.validate_feature_ids(pv.feature_types, feats)  # raises if drifted


def test_validate_feature_ids_raises_on_id_mismatch() -> None:
    types = [specparse.RawModelType(name="Measurement", id=0x04)]
    feats = [specparse.RawFeature(name="Measurement", id=0x99, revision=1)]
    with pytest.raises(ValueError, match="YAML id=0x99"):
        specparse.validate_feature_ids(types, feats)


def test_validate_feature_ids_raises_when_missing_from_registry() -> None:
    types: list[specparse.RawModelType] = []
    feats = [specparse.RawFeature(name="Measurement", id=0x04, revision=1)]
    with pytest.raises(ValueError, match="missing from feature_types registry"):
        specparse.validate_feature_ids(types, feats)


def test_feature_dir_name_matches_disk_layout(features_dir: Path) -> None:
    """``DeviceInfo`` -> ``device-info`` should locate a real directory."""
    for feature in ("DeviceInfo", "EnergyControl", "ChargingSession", "TestControl"):
        path = features_dir / specparse.feature_dir_name(feature)
        assert path.is_dir(), f"{feature} -> {path} should exist"
