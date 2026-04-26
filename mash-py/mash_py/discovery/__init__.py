"""MASH mDNS discovery — browser side (Phase 6 of mash-py-tdd-plan.md).

Exports the public surface for browsing ``_mash-comm._tcp`` and ``_mash._tcp``
services. The pairing-request browse path (``_mashp._udp``) and the
advertiser side both ship in later phases (Phase 14+ device-side listener,
Phase 18 advertiser).

Spec sources (canonical):

- ``docs/discovery.md §1`` — service overview.
- ``docs/discovery.md §2.1`` — service types + SRV port authority.
- ``docs/discovery.md §2.2`` + DEC-078 — commissionable TXT schema.
- ``docs/discovery.md §2.3`` — operational TXT schema.
- DEC-082 — two-port deployment; SRV port may differ per service.
"""

from __future__ import annotations

from mash_py.discovery.browser import (
    BrowseEventType,
    Browser,
    CommissionableEvent,
    OperationalEvent,
)
from mash_py.discovery.types import (
    DEFAULT_OPERATIONAL_PORT,
    DEVICE_CATEGORY_NAMES,
    MAX_DISCRIMINATOR,
    MAX_INSTANCE_NAME_LEN,
    MDNS_DOMAIN,
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
    SERVICE_TYPE_PAIRING_REQUEST,
    TXT_KEY_BRAND,
    TXT_KEY_CATEGORIES,
    TXT_KEY_DEVICE_ID,
    TXT_KEY_DEVICE_NAME,
    TXT_KEY_DISCRIMINATOR,
    TXT_KEY_ENDPOINTS,
    TXT_KEY_FEATURE_MAP,
    TXT_KEY_FIRMWARE,
    TXT_KEY_MODEL,
    TXT_KEY_SERIAL,
    TXT_KEY_VENDOR_PRODUCT,
    TXT_KEY_ZONE_ID,
    TXT_KEY_ZONE_NAME,
    BrowseTimeoutError,
    CommissionableService,
    DiscoveryError,
    OperationalService,
    TXTRecordError,
    parse_categories,
    parse_commissionable_txt,
    parse_feature_map,
    parse_operational_txt,
)

__all__ = [
    "DEFAULT_OPERATIONAL_PORT",
    "DEVICE_CATEGORY_NAMES",
    "MAX_DISCRIMINATOR",
    "MAX_INSTANCE_NAME_LEN",
    "MDNS_DOMAIN",
    "SERVICE_TYPE_COMMISSIONABLE",
    "SERVICE_TYPE_OPERATIONAL",
    "SERVICE_TYPE_PAIRING_REQUEST",
    "TXT_KEY_BRAND",
    "TXT_KEY_CATEGORIES",
    "TXT_KEY_DEVICE_ID",
    "TXT_KEY_DEVICE_NAME",
    "TXT_KEY_DISCRIMINATOR",
    "TXT_KEY_ENDPOINTS",
    "TXT_KEY_FEATURE_MAP",
    "TXT_KEY_FIRMWARE",
    "TXT_KEY_MODEL",
    "TXT_KEY_SERIAL",
    "TXT_KEY_VENDOR_PRODUCT",
    "TXT_KEY_ZONE_ID",
    "TXT_KEY_ZONE_NAME",
    "BrowseEventType",
    "BrowseTimeoutError",
    "Browser",
    "CommissionableEvent",
    "CommissionableService",
    "DiscoveryError",
    "OperationalEvent",
    "OperationalService",
    "TXTRecordError",
    "parse_categories",
    "parse_commissionable_txt",
    "parse_feature_map",
    "parse_operational_txt",
]
