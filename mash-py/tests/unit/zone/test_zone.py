"""Unit tests for ``mash_py.zone.zone``.

Covers MultiZoneValue behaviour against the spec:

- Most-restrictive limit resolution across multiple zones.
- Highest-priority setpoint resolution; same-priority tiebreak.
- TEST-zone exclusion (DEC-060 observer-only).
- Expiry of timed values (treated as ClearLimit per
  ``docs/testing/behavior/multi-zone-resolution.md §2.3``).

Spec sources:
- ``docs/multi-zone.md §5`` — limit/setpoint resolution overview.
- ``docs/testing/behavior/multi-zone-resolution.md §2-3`` — exact rules.
- ``docs/features/energy-control.md`` "Validation Rules" — limits >= 0.
"""

from __future__ import annotations

import time

import pytest

from mash_py.commissioning.cert_store import ZoneType
from mash_py.zone.zone import MultiZoneValue, ZoneValue, zone_priority


# ---------------------------------------------------------------------------
# Priority helper
# ---------------------------------------------------------------------------


def test_zone_priority_matches_dec_060() -> None:
    """GRID = 1 (highest), LOCAL = 2, TEST = 3 (lowest)."""
    assert zone_priority(ZoneType.GRID) == 1
    assert zone_priority(ZoneType.LOCAL) == 2
    assert zone_priority(ZoneType.TEST) == 3


# ---------------------------------------------------------------------------
# Limit resolution
# ---------------------------------------------------------------------------


class TestResolveLimits:
    def test_empty_returns_none(self) -> None:
        mzv = MultiZoneValue()
        val, zid = mzv.resolve_limits()
        assert val is None
        assert zid == ""

    def test_single_value(self) -> None:
        mzv = MultiZoneValue()
        mzv.set("z1", ZoneType.LOCAL, 5000)
        val, zid = mzv.resolve_limits()
        assert val == 5000
        assert zid == "z1"

    def test_most_restrictive_wins(self) -> None:
        """min(GRID=6000, LOCAL=5000) = 5000 — LOCAL is the more
        restrictive contributor even though it has lower priority."""
        mzv = MultiZoneValue()
        mzv.set("grid", ZoneType.GRID, 6_000_000)
        mzv.set("local", ZoneType.LOCAL, 5_000_000)
        val, zid = mzv.resolve_limits()
        assert val == 5_000_000
        assert zid == "local"

    def test_production_limits_use_positive_values(self) -> None:
        """Per energy-control.md, production limits are positive too —
        direction is implicit in the attribute name. Most-restrictive =
        smallest value, same as consumption."""
        mzv = MultiZoneValue()
        mzv.set("grid", ZoneType.GRID, 3000)
        mzv.set("home", ZoneType.LOCAL, 2000)
        val, zid = mzv.resolve_limits()
        assert val == 2000
        assert zid == "home"

    def test_zero_limit_is_valid(self) -> None:
        """consumptionLimit=0 means 'do not consume' — valid per
        multi-zone-resolution.md §6.4."""
        mzv = MultiZoneValue()
        mzv.set("grid", ZoneType.GRID, 0)
        mzv.set("local", ZoneType.LOCAL, 5000)
        val, zid = mzv.resolve_limits()
        assert val == 0
        assert zid == "grid"

    def test_test_zone_excluded(self) -> None:
        """TEST zones are observer-only (DEC-060) — even a numerically
        more restrictive value from a TEST zone must be ignored."""
        mzv = MultiZoneValue()
        mzv.set("grid", ZoneType.GRID, 3000)
        mzv.set("test", ZoneType.TEST, 1000)
        val, zid = mzv.resolve_limits()
        assert val == 3000
        assert zid == "grid"

    def test_only_test_zone_returns_none(self) -> None:
        """If every value is from a TEST zone, the resolver behaves as
        if no limits are set — caller should fall back to internal
        capability."""
        mzv = MultiZoneValue()
        mzv.set("test", ZoneType.TEST, 5000)
        val, zid = mzv.resolve_limits()
        assert val is None
        assert zid == ""

    def test_clear_drops_value(self) -> None:
        mzv = MultiZoneValue()
        mzv.set("grid", ZoneType.GRID, 3000)
        mzv.set("local", ZoneType.LOCAL, 5000)
        mzv.clear("grid")
        val, zid = mzv.resolve_limits()
        assert val == 5000
        assert zid == "local"

    def test_expired_values_dropped(self) -> None:
        """A value with a duration that has elapsed must be ignored —
        treated as ClearLimit per multi-zone-resolution.md §2.3.4."""
        mzv = MultiZoneValue()
        # Set with a tiny duration that will be expired by the time we
        # resolve; resolution doesn't sleep — we just feed an explicit
        # zv with expires_at in the past.
        zv = ZoneValue(
            zone_id="grid",
            zone_type=ZoneType.GRID,
            value=1000,
            duration=0.001,
            set_at=time.monotonic() - 1.0,
            expires_at=time.monotonic() - 0.5,
        )
        mzv.values["grid"] = zv
        mzv.set("local", ZoneType.LOCAL, 5000)

        val, zid = mzv.resolve_limits()
        assert val == 5000
        assert zid == "local"
        # Expired entry must have been pruned.
        assert "grid" not in mzv.values

    def test_indefinite_value_never_expires(self) -> None:
        """duration == 0 means indefinite — even an old value is still
        resolvable."""
        mzv = MultiZoneValue()
        mzv.values["grid"] = ZoneValue(
            zone_id="grid",
            zone_type=ZoneType.GRID,
            value=3000,
            duration=0.0,
            set_at=time.monotonic() - 100.0,
            expires_at=0.0,
        )
        val, zid = mzv.resolve_limits()
        assert val == 3000
        assert zid == "grid"


# ---------------------------------------------------------------------------
# Setpoint resolution
# ---------------------------------------------------------------------------


class TestResolveSetpoints:
    def test_empty_returns_none(self) -> None:
        mzv = MultiZoneValue()
        val, zid = mzv.resolve_setpoints()
        assert val is None
        assert zid == ""

    def test_highest_priority_wins(self) -> None:
        """GRID (priority 1) beats LOCAL (priority 2) regardless of
        which has the smaller value — setpoints are NOT
        most-restrictive."""
        mzv = MultiZoneValue()
        mzv.set("local", ZoneType.LOCAL, 5_000_000)  # higher numeric value
        mzv.set("grid", ZoneType.GRID, 3_000_000)
        val, zid = mzv.resolve_setpoints()
        assert val == 3_000_000
        assert zid == "grid"

    def test_local_wins_when_no_grid(self) -> None:
        mzv = MultiZoneValue()
        mzv.set("local", ZoneType.LOCAL, 5_000_000)
        val, zid = mzv.resolve_setpoints()
        assert val == 5_000_000
        assert zid == "local"

    def test_same_priority_most_recent_wins(self) -> None:
        """Per multi-zone-resolution.md §3.3: if priorities tie, the
        most recently set value wins. We use ZoneValue ``set_at`` to
        order them deterministically."""
        mzv = MultiZoneValue()
        # Two LOCAL zones is impossible per DEC-043 in production, but
        # the resolver must still be deterministic if it ever happens.
        now = time.monotonic()
        mzv.values["local-1"] = ZoneValue(
            zone_id="local-1",
            zone_type=ZoneType.LOCAL,
            value=5000,
            duration=0.0,
            set_at=now,
        )
        mzv.values["local-2"] = ZoneValue(
            zone_id="local-2",
            zone_type=ZoneType.LOCAL,
            value=3000,
            duration=0.0,
            set_at=now + 0.001,  # slightly later → wins the tiebreak
        )
        val, zid = mzv.resolve_setpoints()
        assert val == 3000
        assert zid == "local-2"

    def test_test_zone_excluded(self) -> None:
        """TEST has the highest *numeric* priority value (3) so it
        loses to GRID (1) and LOCAL (2) anyway — but it must also be
        excluded outright so a single-TEST-zone-only resolution returns
        None."""
        mzv = MultiZoneValue()
        mzv.set("test", ZoneType.TEST, 3000)
        val, zid = mzv.resolve_setpoints()
        assert val is None
        assert zid == ""

    def test_test_zone_does_not_displace_local(self) -> None:
        mzv = MultiZoneValue()
        mzv.set("local", ZoneType.LOCAL, 8000)
        mzv.set("test", ZoneType.TEST, 2000)
        val, zid = mzv.resolve_setpoints()
        assert val == 8000
        assert zid == "local"


# ---------------------------------------------------------------------------
# ZoneValue
# ---------------------------------------------------------------------------


class TestZoneValueExpiry:
    def test_indefinite_value_does_not_expire(self) -> None:
        zv = ZoneValue(
            zone_id="z1",
            zone_type=ZoneType.LOCAL,
            value=5000,
            duration=0.0,
        )
        assert zv.is_expired() is False

    def test_timed_value_not_yet_expired(self) -> None:
        zv = ZoneValue(
            zone_id="z1",
            zone_type=ZoneType.LOCAL,
            value=5000,
            duration=3600.0,
        )
        assert zv.is_expired() is False

    def test_timed_value_expired(self) -> None:
        now = time.monotonic()
        zv = ZoneValue(
            zone_id="z1",
            zone_type=ZoneType.LOCAL,
            value=5000,
            duration=0.001,
            set_at=now - 1.0,
            expires_at=now - 0.5,
        )
        assert zv.is_expired() is True

    def test_priority_matches_zone_type(self) -> None:
        zv = ZoneValue(
            zone_id="z1",
            zone_type=ZoneType.GRID,
            value=5000,
            duration=0.0,
        )
        assert zv.priority == 1


@pytest.mark.parametrize(
    ("first_type", "first_val", "second_type", "second_val", "expected_val", "expected_zone"),
    [
        # GRID more restrictive than LOCAL
        (ZoneType.GRID, 3000, ZoneType.LOCAL, 5000, 3000, "z1"),
        # LOCAL more restrictive than GRID
        (ZoneType.GRID, 5000, ZoneType.LOCAL, 3000, 3000, "z2"),
        # Equal values - first encountered wins (insertion order)
        (ZoneType.GRID, 3000, ZoneType.LOCAL, 3000, 3000, "z1"),
    ],
)
def test_resolve_limits_parametric(
    first_type: ZoneType,
    first_val: int,
    second_type: ZoneType,
    second_val: int,
    expected_val: int,
    expected_zone: str,
) -> None:
    """Small parametric matrix to reinforce that resolution is purely
    value-driven — it does not depend on zone type for limits."""
    mzv = MultiZoneValue()
    mzv.set("z1", first_type, first_val)
    mzv.set("z2", second_type, second_val)
    val, zid = mzv.resolve_limits()
    assert val == expected_val
    assert zid == expected_zone
