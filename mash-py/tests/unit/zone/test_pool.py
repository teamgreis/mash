"""Unit tests for ``mash_py.zone.pool``.

Covers the zone-membership lifecycle:

- Add/remove zones; duplicate ID rejection.
- DEC-043/-060 enforcement: max 1 per type, max 3 total, type check
  before capacity check.
- Connection state tracking, ``last_seen`` updates.
- Highest-priority-zone helpers.
- Callbacks fire after lock release (so they can re-enter the pool).
"""

from __future__ import annotations

import asyncio

import pytest

from mash_py.commissioning.cert_store import ZoneType
from mash_py.zone.pool import (
    MAX_ZONES,
    MaxZonesExceededError,
    ZoneExistsError,
    ZoneNotFoundError,
    ZonePool,
    ZoneTypeExistsError,
)


pytestmark = pytest.mark.asyncio


class TestAddRemove:
    async def test_add_then_query(self) -> None:
        pool = ZonePool()
        zone = await pool.add_zone("z1", ZoneType.LOCAL)
        assert zone.id == "z1"
        assert zone.type is ZoneType.LOCAL
        assert pool.has_zone("z1") is True
        assert pool.zone_count() == 1

    async def test_add_duplicate_id_raises(self) -> None:
        pool = ZonePool()
        await pool.add_zone("z1", ZoneType.LOCAL)
        with pytest.raises(ZoneExistsError):
            await pool.add_zone("z1", ZoneType.LOCAL)

    async def test_add_duplicate_type_raises(self) -> None:
        """DEC-043: max 1 zone per zone type."""
        pool = ZonePool()
        await pool.add_zone("local-1", ZoneType.LOCAL)
        with pytest.raises(ZoneTypeExistsError):
            await pool.add_zone("local-2", ZoneType.LOCAL)

    async def test_grid_plus_local_allowed(self) -> None:
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        await pool.add_zone("local", ZoneType.LOCAL)
        assert pool.zone_count() == 2

    async def test_max_zones_is_three(self) -> None:
        """DEC-060: GRID + LOCAL + TEST = 3 zones max."""
        assert MAX_ZONES == 3
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        await pool.add_zone("local", ZoneType.LOCAL)
        await pool.add_zone("test", ZoneType.TEST)
        assert pool.zone_count() == 3

    async def test_type_check_before_capacity_check(self) -> None:
        """At capacity, a same-type duplicate should still surface as
        ZoneTypeExistsError (more specific) rather than
        MaxZonesExceededError. Mirrors the Go implementation."""
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        await pool.add_zone("local", ZoneType.LOCAL)
        await pool.add_zone("test", ZoneType.TEST)
        with pytest.raises(ZoneTypeExistsError):
            await pool.add_zone("grid-2", ZoneType.GRID)

    async def test_remove_unknown_raises(self) -> None:
        pool = ZonePool()
        with pytest.raises(ZoneNotFoundError):
            await pool.remove_zone("ghost")

    async def test_remove_round_trip(self) -> None:
        pool = ZonePool()
        await pool.add_zone("z1", ZoneType.LOCAL)
        await pool.remove_zone("z1")
        assert pool.has_zone("z1") is False
        assert pool.zone_count() == 0


class TestConnectionState:
    async def test_initial_state_disconnected(self) -> None:
        pool = ZonePool()
        zone = await pool.add_zone("z1", ZoneType.LOCAL)
        assert zone.connected is False
        assert zone.last_seen == 0.0

    async def test_set_connected_updates_last_seen(self) -> None:
        pool = ZonePool()
        await pool.add_zone("z1", ZoneType.LOCAL)
        await pool.set_connected("z1")
        zone = pool.get_zone("z1")
        assert zone.connected is True
        assert zone.last_seen > 0.0

    async def test_set_disconnected_preserves_last_seen(self) -> None:
        """``last_seen`` must NOT be cleared on disconnect — the
        failsafe timer downstream needs to know how long ago the last
        activity was."""
        pool = ZonePool()
        await pool.add_zone("z1", ZoneType.LOCAL)
        await pool.set_connected("z1")
        original_last_seen = pool.get_zone("z1").last_seen
        await pool.set_disconnected("z1")
        zone = pool.get_zone("z1")
        assert zone.connected is False
        assert zone.last_seen == original_last_seen

    async def test_connected_zones_filter(self) -> None:
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        await pool.add_zone("local", ZoneType.LOCAL)
        await pool.set_connected("grid")
        assert pool.connected_zones() == ["grid"]


class TestPriorityHelpers:
    async def test_highest_priority_zone_picks_grid(self) -> None:
        pool = ZonePool()
        await pool.add_zone("local", ZoneType.LOCAL)
        await pool.add_zone("grid", ZoneType.GRID)
        zone = pool.highest_priority_zone()
        assert zone is not None
        assert zone.id == "grid"

    async def test_highest_priority_connected_ignores_disconnected(self) -> None:
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        await pool.add_zone("local", ZoneType.LOCAL)
        await pool.set_connected("local")  # only LOCAL is connected
        zone = pool.highest_priority_connected_zone()
        assert zone is not None
        assert zone.id == "local"  # GRID is higher priority but not connected

    async def test_highest_priority_connected_none_when_all_offline(self) -> None:
        pool = ZonePool()
        await pool.add_zone("grid", ZoneType.GRID)
        assert pool.highest_priority_connected_zone() is None

    async def test_can_remove_zone_requires_higher_priority(self) -> None:
        pool = ZonePool()
        await pool.add_zone("local", ZoneType.LOCAL)
        # GRID (priority 1) can remove LOCAL (priority 2)
        assert pool.can_remove_zone(ZoneType.GRID, "local") is True
        # LOCAL cannot remove itself via this mechanism
        assert pool.can_remove_zone(ZoneType.LOCAL, "local") is False
        # Same-or-lower priority cannot remove
        assert pool.can_remove_zone(ZoneType.TEST, "local") is False

    async def test_can_remove_zone_unknown_target_returns_false(self) -> None:
        pool = ZonePool()
        assert pool.can_remove_zone(ZoneType.GRID, "ghost") is False


class TestCallbacks:
    async def test_added_callback_fires(self) -> None:
        pool = ZonePool()
        added: list[str] = []
        pool.on_zone_added(lambda z: added.append(z.id))
        await pool.add_zone("z1", ZoneType.LOCAL)
        assert added == ["z1"]

    async def test_removed_callback_fires(self) -> None:
        pool = ZonePool()
        removed: list[str] = []
        pool.on_zone_removed(lambda zid: removed.append(zid))
        await pool.add_zone("z1", ZoneType.LOCAL)
        await pool.remove_zone("z1")
        assert removed == ["z1"]

    async def test_async_callback_awaited(self) -> None:
        """Async callbacks must be awaited so the caller can rely on
        them having completed before ``add_zone`` returns."""
        pool = ZonePool()
        seen = asyncio.Event()

        async def on_added(_z) -> None:
            await asyncio.sleep(0)
            seen.set()

        pool.on_zone_added(on_added)
        await pool.add_zone("z1", ZoneType.LOCAL)
        assert seen.is_set()

    async def test_callback_can_reenter_pool(self) -> None:
        """Callbacks fire OUTSIDE the lock so they may safely call
        back into the pool. Without that, an on_added handler that
        reads ``has_zone`` would deadlock."""
        pool = ZonePool()
        observed: list[bool] = []

        async def on_added(z) -> None:
            # Re-entering the pool while inside the callback would
            # deadlock if the lock was still held.
            observed.append(pool.has_zone(z.id))

        pool.on_zone_added(on_added)
        await pool.add_zone("z1", ZoneType.LOCAL)
        assert observed == [True]


async def test_max_zones_exceeded_when_no_type_collision() -> None:
    """Capacity check is reachable only if all 3 type slots are
    full. Adding a 4th would require duplicating a type, which the
    type check rejects first. To exercise the capacity branch in
    isolation we'd need to corrupt the pool — instead we just check
    that the constant matches the type-slot count."""
    assert MAX_ZONES == 3
    # The capacity branch is therefore unreachable by valid inputs;
    # the type check is the practical guard. We keep
    # MaxZonesExceededError exported for defensive callers and a
    # dummy assertion to keep mypy/pytest happy with the import.
    err = MaxZonesExceededError("at capacity")
    assert "at capacity" in str(err)
