"""Per-device zone-membership state container (Phase 20).

Mirrors ``mash-go/pkg/zone/manager.go``. The :class:`ZonePool` tracks
which zones the device belongs to, their connection state, and the
last-seen timestamp per zone. It enforces the DEC-043 / DEC-060 zone
limits:

- Max 1 zone per zone type (GRID, LOCAL, TEST)
- Total ceiling: 3 zones (DEC-060: TEST extension to DEC-043)

Type-existence is checked **before** capacity — a same-type duplicate
returns :class:`ZoneTypeExistsError`, not :class:`MaxZonesExceededError`.
This matches mash-go's ordering and keeps the error message specific
when the user is trying to commission a second GRID controller into a
device that already has one.

Threading: this module is sync-safe via a single :class:`asyncio.Lock`
held during state transitions; reads use ``snapshot_*`` helpers that
return immutable copies. Callers running in an async context should
``await`` the methods that lock (``add_zone``, ``remove_zone``,
``set_connected``, ``set_disconnected``, ``update_last_seen``).
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field

from mash_py.commissioning.cert_store import ZoneType
from mash_py.zone.zone import zone_priority


__all__ = [
    "MAX_ZONES",
    "MaxZonesExceededError",
    "Zone",
    "ZoneAddedCallback",
    "ZoneConnectCallback",
    "ZoneDisconnectCallback",
    "ZoneExistsError",
    "ZoneNotConnectedError",
    "ZoneNotFoundError",
    "ZonePool",
    "ZonePoolError",
    "ZoneRemovedCallback",
    "ZoneTypeExistsError",
]


# Per DEC-043 + DEC-060: GRID + LOCAL + TEST = 3 zones maximum.
MAX_ZONES = 3


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class ZonePoolError(Exception):
    """Base class for zone-pool errors. Mirrors the Go package's sentinel
    errors; we use exception subclasses so callers can ``except`` on the
    specific kind without string-matching.
    """


class ZoneNotFoundError(ZonePoolError):
    """Raised when an operation references a zone that isn't in the pool."""


class ZoneExistsError(ZonePoolError):
    """Raised when attempting to add a zone with a duplicate ID."""


class ZoneTypeExistsError(ZonePoolError):
    """Raised when attempting to add a second zone of the same type
    (DEC-043 enforcement). Distinct from :class:`ZoneExistsError` because
    a same-type duplicate has a different fix path (remove the existing
    zone of that type first)."""


class MaxZonesExceededError(ZonePoolError):
    """Raised when adding a zone would push the count above ``MAX_ZONES``."""


class ZoneNotConnectedError(ZonePoolError):
    """Raised when an operation requires the zone's controller to be
    actively connected and it isn't."""


# ---------------------------------------------------------------------------
# Zone
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class Zone:
    """One zone-membership record on a device.

    ``last_seen`` is monotonic-clock seconds, set on every
    ``update_last_seen`` call. Used by the failsafe timer (Phase 20) to
    arm/disarm based on per-zone activity. ``commissioned_at`` is also
    monotonic; if you need wall-clock for telemetry, capture it
    separately.
    """

    id: str
    type: ZoneType
    connected: bool = False
    last_seen: float = 0.0
    commissioned_at: float = field(default_factory=time.monotonic)

    @property
    def priority(self) -> int:
        """Priority of this zone. Lower = higher priority."""
        return zone_priority(self.type)


# ---------------------------------------------------------------------------
# Callback types
# ---------------------------------------------------------------------------


ZoneAddedCallback = Callable[[Zone], Awaitable[None] | None]
ZoneRemovedCallback = Callable[[str], Awaitable[None] | None]
ZoneConnectCallback = Callable[[str], Awaitable[None] | None]
ZoneDisconnectCallback = Callable[[str], Awaitable[None] | None]


# ---------------------------------------------------------------------------
# ZonePool
# ---------------------------------------------------------------------------


class ZonePool:
    """Per-device zone-membership manager.

    Use one instance per :class:`mash_py.service.DeviceService`. Add
    zones as commissioning completes (one per zone joined); remove on
    explicit ``RemoveZone`` command from a controller; flip connected
    state from the operational TLS layer's accept/close handlers.
    """

    def __init__(self) -> None:
        self._zones: dict[str, Zone] = {}
        self._lock = asyncio.Lock()

        # Callbacks fire OUTSIDE the lock to avoid deadlocks if a
        # callback re-enters the pool. The list is rebound on register
        # rather than mutated, so iterating snapshots is safe.
        self._on_added: list[ZoneAddedCallback] = []
        self._on_removed: list[ZoneRemovedCallback] = []
        self._on_connect: list[ZoneConnectCallback] = []
        self._on_disconnect: list[ZoneDisconnectCallback] = []

    # ------------------------------------------------------------------
    # Mutators
    # ------------------------------------------------------------------

    async def add_zone(self, zone_id: str, zone_type: ZoneType) -> Zone:
        """Add a zone membership.

        Raises :class:`ZoneExistsError` if ``zone_id`` is already in the
        pool, :class:`ZoneTypeExistsError` if a different zone of
        ``zone_type`` is already present, or
        :class:`MaxZonesExceededError` if the pool would exceed
        ``MAX_ZONES``. The type check runs before the capacity check so
        the caller gets the most specific error.
        """
        async with self._lock:
            if zone_id in self._zones:
                raise ZoneExistsError(f"zone {zone_id!r} already exists")

            for existing in self._zones.values():
                if existing.type is zone_type:
                    raise ZoneTypeExistsError(
                        f"zone of type {zone_type.name} already exists "
                        f"(existing zone: {existing.id!r})"
                    )

            if len(self._zones) >= MAX_ZONES:
                raise MaxZonesExceededError(
                    f"cannot add zone {zone_id!r}: at capacity ({MAX_ZONES})"
                )

            zone = Zone(id=zone_id, type=zone_type)
            self._zones[zone_id] = zone
            callbacks = list(self._on_added)

        for cb in callbacks:
            res = cb(zone)
            if res is not None:
                await res
        return zone

    async def remove_zone(self, zone_id: str) -> None:
        """Drop a zone membership. Raises :class:`ZoneNotFoundError`."""
        async with self._lock:
            if zone_id not in self._zones:
                raise ZoneNotFoundError(f"zone {zone_id!r} not found")
            del self._zones[zone_id]
            callbacks = list(self._on_removed)

        for cb in callbacks:
            res = cb(zone_id)
            if res is not None:
                await res

    async def set_connected(self, zone_id: str) -> None:
        """Mark ``zone_id`` connected and update ``last_seen``."""
        async with self._lock:
            zone = self._zones.get(zone_id)
            if zone is None:
                raise ZoneNotFoundError(f"zone {zone_id!r} not found")
            zone.connected = True
            zone.last_seen = time.monotonic()
            callbacks = list(self._on_connect)

        for cb in callbacks:
            res = cb(zone_id)
            if res is not None:
                await res

    async def set_disconnected(self, zone_id: str) -> None:
        """Mark ``zone_id`` disconnected. ``last_seen`` is not modified —
        downstream timers (failsafe) need to know how long ago the last
        activity was."""
        async with self._lock:
            zone = self._zones.get(zone_id)
            if zone is None:
                raise ZoneNotFoundError(f"zone {zone_id!r} not found")
            zone.connected = False
            callbacks = list(self._on_disconnect)

        for cb in callbacks:
            res = cb(zone_id)
            if res is not None:
                await res

    async def update_last_seen(self, zone_id: str) -> None:
        """Refresh ``last_seen`` to the current monotonic clock. Called by
        the operational read-loop on every successful frame."""
        async with self._lock:
            zone = self._zones.get(zone_id)
            if zone is None:
                raise ZoneNotFoundError(f"zone {zone_id!r} not found")
            zone.last_seen = time.monotonic()

    # ------------------------------------------------------------------
    # Read accessors (no lock — return snapshots)
    # ------------------------------------------------------------------

    def has_zone(self, zone_id: str) -> bool:
        """True if ``zone_id`` is in the pool."""
        return zone_id in self._zones

    def get_zone(self, zone_id: str) -> Zone:
        """Return the :class:`Zone` for ``zone_id``. Raises
        :class:`ZoneNotFoundError`. The returned dataclass is a *live*
        reference to the pool's internal record — callers should not
        mutate it directly; use the mutator methods instead."""
        zone = self._zones.get(zone_id)
        if zone is None:
            raise ZoneNotFoundError(f"zone {zone_id!r} not found")
        return zone

    def list_zone_ids(self) -> list[str]:
        """All zone IDs in the pool. Order is insertion order (Python
        dicts preserve this)."""
        return list(self._zones.keys())

    def all_zones(self) -> list[Zone]:
        """All :class:`Zone` records. Live references — see
        :meth:`get_zone`."""
        return list(self._zones.values())

    def zone_count(self) -> int:
        """Number of zones currently in the pool."""
        return len(self._zones)

    def connected_zones(self) -> list[str]:
        """IDs of zones whose ``connected`` flag is True."""
        return [zid for zid, z in self._zones.items() if z.connected]

    def highest_priority_zone(self) -> Zone | None:
        """Zone with the lowest priority *number*. ``None`` if pool is
        empty. Connected/disconnected state is ignored — see
        :meth:`highest_priority_connected_zone` for that variant."""
        if not self._zones:
            return None
        return min(self._zones.values(), key=lambda z: z.priority)

    def highest_priority_connected_zone(self) -> Zone | None:
        """Zone with the lowest priority *number* that is currently
        connected. ``None`` if no zones are connected."""
        connected = [z for z in self._zones.values() if z.connected]
        if not connected:
            return None
        return min(connected, key=lambda z: z.priority)

    def can_remove_zone(
        self, requester_type: ZoneType, target_zone_id: str
    ) -> bool:
        """True if a zone of ``requester_type`` may forcibly remove
        ``target_zone_id``. Per ``docs/decision-log.md`` DEC-064 only a
        strictly higher-priority zone can override another's
        membership. Returns False if the target doesn't exist (no zone
        to remove)."""
        target = self._zones.get(target_zone_id)
        if target is None:
            return False
        return zone_priority(requester_type) < target.priority

    # ------------------------------------------------------------------
    # Callbacks
    # ------------------------------------------------------------------

    def on_zone_added(self, fn: ZoneAddedCallback) -> None:
        """Register ``fn`` to fire each time a zone is added.

        Callbacks may be sync or async; if async, the pool ``await``s
        them. They run AFTER the lock is released so they can call back
        into the pool safely.
        """
        self._on_added.append(fn)

    def on_zone_removed(self, fn: ZoneRemovedCallback) -> None:
        """Register ``fn`` to fire each time a zone is removed."""
        self._on_removed.append(fn)

    def on_connect(self, fn: ZoneConnectCallback) -> None:
        """Register ``fn`` to fire each time a zone connects."""
        self._on_connect.append(fn)

    def on_disconnect(self, fn: ZoneDisconnectCallback) -> None:
        """Register ``fn`` to fire each time a zone disconnects."""
        self._on_disconnect.append(fn)
