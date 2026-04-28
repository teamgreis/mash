"""Per-zone value tracking + multi-zone resolution (Phase 20).

Mirrors ``mash-go/pkg/zone/zone.go`` (post the negative-value cleanup that
landed alongside this phase). Two operations matter:

- :func:`MultiZoneValue.resolve_limits` — most-restrictive wins. Smallest
  non-negative value across all non-TEST zones. Returns ``None`` if no
  values are set.
- :func:`MultiZoneValue.resolve_setpoints` — highest-priority wins. The
  zone with the lowest priority *number* wins. Same-priority tiebreak
  is "most recently set" per ``docs/testing/behavior/multi-zone-resolution.md
  §3.3``.

Both ignore TEST-zone values entirely (DEC-060: TEST zones are pure
observers and do not influence resolution).

Sign convention: per ``docs/features/energy-control.md`` "Validation
Rules" and ``multi-zone-resolution.md §6.3``, both consumption and
production limits are non-negative. Direction is implicit in the
attribute name — ``myConsumptionLimit`` vs ``myProductionLimit``. Values
are validated >= 0 at the Write boundary; this module does not re-check.

Expiry semantics: ``ZoneValue.duration > 0`` arms an expiry timer; once
elapsed the value is dropped from resolution (treated as if the zone
called ``ClearLimit``). ``duration == 0`` means indefinite.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field

from mash_py.commissioning.cert_store import ZoneType


__all__ = [
    "MultiZoneValue",
    "ZoneValue",
    "zone_priority",
]


# ---------------------------------------------------------------------------
# Priority
# ---------------------------------------------------------------------------


def zone_priority(zone_type: ZoneType) -> int:
    """Numeric priority for ``zone_type``. Lower = higher priority.

    Per ``docs/multi-zone.md §2``: GRID = 1, LOCAL = 2, TEST = 3. The
    ``ZoneType`` IntEnum already encodes those values, so this is the
    identity — but we keep the function to centralise the rule and make
    intent clear at call sites.
    """
    return int(zone_type)


# ---------------------------------------------------------------------------
# ZoneValue
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class ZoneValue:
    """A value set by one zone, with expiry metadata.

    ``set_at`` is monotonic-clock time (seconds, ``time.monotonic()``) so
    expiry comparisons survive wall-clock jumps. ``expires_at`` is set to
    ``set_at + duration`` when ``duration > 0``; otherwise it stays at
    0.0 to mean "indefinite".
    """

    zone_id: str
    zone_type: ZoneType
    value: int
    duration: float  # seconds; 0 == indefinite
    set_at: float = field(default_factory=time.monotonic)
    expires_at: float = 0.0

    def __post_init__(self) -> None:
        if self.duration > 0 and self.expires_at == 0.0:
            self.expires_at = self.set_at + self.duration

    @property
    def priority(self) -> int:
        """Priority of the zone that set this value. Lower = higher priority."""
        return zone_priority(self.zone_type)

    def is_expired(self, *, now: float | None = None) -> bool:
        """True if the value has passed its expiry. Indefinite values
        (``expires_at == 0.0``) never expire.
        """
        if self.expires_at == 0.0:
            return False
        return (now if now is not None else time.monotonic()) >= self.expires_at


# ---------------------------------------------------------------------------
# MultiZoneValue
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class MultiZoneValue:
    """Tracks one attribute's value across all zones; resolves on demand.

    Mirrors ``zone.MultiZoneValue`` in mash-go. Two resolvers:

    - :meth:`resolve_limits` for limits (most-restrictive)
    - :meth:`resolve_setpoints` for setpoints (highest-priority)

    The same instance is reusable for either resolver — it just stores
    one ``ZoneValue`` per zone and the resolver chooses how to combine
    them. In practice the device has one MultiZoneValue per attribute
    that participates in resolution.
    """

    values: dict[str, ZoneValue] = field(default_factory=dict)
    effective_value: int | None = None
    winning_zone_id: str = ""

    def set(
        self,
        zone_id: str,
        zone_type: ZoneType,
        value: int,
        duration: float = 0.0,
    ) -> None:
        """Record ``value`` from ``zone_id``. Replaces any existing value
        for that zone. ``duration > 0`` arms expiry; ``0`` means indefinite.
        """
        self.values[zone_id] = ZoneValue(
            zone_id=zone_id,
            zone_type=zone_type,
            value=value,
            duration=duration,
        )

    def clear(self, zone_id: str) -> None:
        """Drop ``zone_id``'s value. No-op if the zone has no value set."""
        self.values.pop(zone_id, None)

    def get(self, zone_id: str) -> ZoneValue | None:
        """Return ``zone_id``'s ZoneValue, or ``None`` if not set."""
        return self.values.get(zone_id)

    def remove_expired(self, *, now: float | None = None) -> None:
        """Drop all expired values. Called at the start of each resolve."""
        ts = now if now is not None else time.monotonic()
        expired = [zid for zid, v in self.values.items() if v.is_expired(now=ts)]
        for zid in expired:
            del self.values[zid]

    def resolve_limits(self) -> tuple[int | None, str]:
        """Most-restrictive wins: smallest non-negative value across all
        non-TEST zones. TEST zones are excluded per DEC-060 (observer-only).

        Returns ``(effective_value, winning_zone_id)``. ``(None, "")`` if
        no eligible values exist — the caller should treat this as "no
        external limit; use the device's internal capability".

        Per docs/features/energy-control.md "Validation Rules" both
        consumption and production limits are non-negative; direction is
        encoded by the attribute name, not the sign. Negatives are
        rejected at the Write boundary and never reach this resolver.
        """
        self.remove_expired()

        if not self.values:
            self.effective_value = None
            self.winning_zone_id = ""
            return None, ""

        effective: int | None = None
        winning_id = ""
        for zone_id, zv in self.values.items():
            if zv.zone_type is ZoneType.TEST:
                continue
            if effective is None or zv.value < effective:
                effective = zv.value
                winning_id = zone_id

        self.effective_value = effective
        self.winning_zone_id = winning_id
        return effective, winning_id

    def resolve_setpoints(self) -> tuple[int | None, str]:
        """Highest-priority wins: lowest priority *number* across non-TEST
        zones. Same-priority tiebreak: most recently set (highest
        ``set_at``).

        Returns ``(effective_value, winning_zone_id)``.
        """
        self.remove_expired()

        if not self.values:
            self.effective_value = None
            self.winning_zone_id = ""
            return None, ""

        effective: int | None = None
        winning_id = ""
        # Priority 255 sentinel is well above the GRID/LOCAL/TEST values
        # (1/2/3) so any real candidate will replace it.
        winning_priority = 255
        winning_set_at = float("-inf")
        for zone_id, zv in self.values.items():
            if zv.zone_type is ZoneType.TEST:
                continue
            prio = zv.priority
            if prio < winning_priority or (
                prio == winning_priority and zv.set_at > winning_set_at
            ):
                effective = zv.value
                winning_id = zone_id
                winning_priority = prio
                winning_set_at = zv.set_at

        self.effective_value = effective
        self.winning_zone_id = winning_id
        return effective, winning_id

    def effective(self) -> tuple[int | None, str]:
        """Cached last-resolved value + winning zone (no recomputation)."""
        return self.effective_value, self.winning_zone_id
