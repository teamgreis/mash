"""MASH multi-zone resolution + zone-pool state (Phase 20).

This package mirrors ``mash-go/pkg/zone``:

- :mod:`mash_py.zone.zone` — :class:`MultiZoneValue` for per-attribute
  multi-zone resolution (most-restrictive limits, highest-priority
  setpoints).
- :mod:`mash_py.zone.pool` — :class:`ZonePool` for tracking zone
  membership + connection state, with DEC-043/-060 enforcement.

See ``docs/multi-zone.md`` for the architectural model and
``docs/testing/behavior/multi-zone-resolution.md`` for the resolution
rules (which the spec patches in this phase reconciled with
``failsafe-timing.md``).
"""

from __future__ import annotations

from mash_py.zone.peer_match import (
    match_zone_by_peer_cert,
    peer_authority_key_id,
    zone_ca_subject_key_id,
)
from mash_py.zone.pool import (
    MAX_ZONES,
    MaxZonesExceededError,
    Zone,
    ZoneAddedCallback,
    ZoneConnectCallback,
    ZoneDisconnectCallback,
    ZoneExistsError,
    ZoneNotConnectedError,
    ZoneNotFoundError,
    ZonePool,
    ZonePoolError,
    ZoneRemovedCallback,
    ZoneTypeExistsError,
)
from mash_py.zone.zone import MultiZoneValue, ZoneValue, zone_priority


__all__ = [
    "MAX_ZONES",
    "MaxZonesExceededError",
    "MultiZoneValue",
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
    "ZoneValue",
    "match_zone_by_peer_cert",
    "peer_authority_key_id",
    "zone_ca_subject_key_id",
    "zone_priority",
]
