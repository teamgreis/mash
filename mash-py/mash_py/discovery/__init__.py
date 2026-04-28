"""MASH mDNS discovery — browser + advertiser.

Phase 6 ships ``Browser`` (browse paths for ``_mash-comm._tcp`` and
``_mash._tcp``); Phase 18 adds ``Advertiser`` (publish paths for the same two
service types). The pairing-request listener (``_mashp._udp``) ships in a
later phase.

Spec sources (canonical):

- ``docs/discovery.md §1`` — service overview.
- ``docs/discovery.md §2.1`` — service types + SRV port authority.
- ``docs/discovery.md §2.2`` + DEC-078 — commissionable TXT schema.
- ``docs/discovery.md §2.3`` + DEC-088 — operational TXT canonicalization.
- DEC-074 — ``FM`` (feature map) is forbidden in operational TXT.
- DEC-082 — two-port deployment; SRV port may differ per service.
- DEC-088 — operational instance-name format ``<zone-id>-<device-id>``;
  commissionable instance-name format ``MASH-NNNN``.
"""

from __future__ import annotations

from mash_py.discovery.advertiser import Advertiser
from mash_py.discovery.browser import (
    BrowseEventType,
    Browser,
    CommissionableEvent,
    OperationalEvent,
)
from mash_py.discovery.types import (
    DEFAULT_OPERATIONAL_PORT,
    DEVICE_CATEGORY_NAMES,
    ID_HEX_LEN,
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
    TXT_KEY_FIRMWARE,
    TXT_KEY_MODEL,
    TXT_KEY_SERIAL,
    TXT_KEY_VENDOR_PRODUCT,
    TXT_KEY_ZONE_ID,
    TXT_KEY_ZONE_NAME,
    AdvertiseError,
    BrowseTimeoutError,
    CommissionableAdvertisement,
    CommissionableService,
    DiscoveryError,
    OperationalAdvertisement,
    OperationalService,
    TXTRecordError,
    build_commissionable_txt,
    build_operational_txt,
    commissionable_instance_name,
    operational_instance_name,
    parse_categories,
    parse_commissionable_txt,
    parse_operational_txt,
)

__all__ = [
    "DEFAULT_OPERATIONAL_PORT",
    "DEVICE_CATEGORY_NAMES",
    "ID_HEX_LEN",
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
    "TXT_KEY_FIRMWARE",
    "TXT_KEY_MODEL",
    "TXT_KEY_SERIAL",
    "TXT_KEY_VENDOR_PRODUCT",
    "TXT_KEY_ZONE_ID",
    "TXT_KEY_ZONE_NAME",
    "AdvertiseError",
    "Advertiser",
    "BrowseEventType",
    "BrowseTimeoutError",
    "Browser",
    "CommissionableAdvertisement",
    "CommissionableEvent",
    "CommissionableService",
    "DiscoveryError",
    "OperationalAdvertisement",
    "OperationalEvent",
    "OperationalService",
    "TXTRecordError",
    "build_commissionable_txt",
    "build_operational_txt",
    "commissionable_instance_name",
    "operational_instance_name",
    "parse_categories",
    "parse_commissionable_txt",
    "parse_operational_txt",
]
