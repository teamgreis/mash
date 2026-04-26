"""Constants and result types for MASH mDNS discovery.

Grounded in the spec (canonical), not the Go reference:

- ``docs/discovery.md §1, §2.1`` — three service types, SRV port is authoritative.
- ``docs/discovery.md §2.2`` + DEC-078 — six-key commissionable TXT schema
  (``D``, ``cat``, ``serial``, ``brand``, ``model``, ``DN``).
- ``docs/discovery.md §2.3`` — operational TXT schema (``DI``, ``VP``, ``FW``,
  ``EP``, ``FM``). The spec lists ``ZI`` only inside the ``_mashp._udp``
  pairing record (§2.4); for ``_mash._tcp`` the **service instance name** is
  ``<device-id>._mash._tcp.local`` and there is no ``ZI`` TXT field. Multi-zone
  devices advertise one ``_mash._tcp`` instance per (device, zone) tuple — see
  DEC-082 §"Implementation notes" for that derivation.
- DEC-082 — two-port deployment: SRV port differs per service; browser MUST
  read the SRV port as ground truth, never assume a default.

Phase 6 of mash-py-tdd-plan.md ships only ``_mash-comm._tcp`` and
``_mash._tcp`` browse paths. The pairing-request service (``_mashp._udp``) is
deferred to Phase 18 (advertiser side) and Phase 14+ (device-side listener).
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

# ---------------------------------------------------------------------------
# Service types (DNS-SD service names)
# ---------------------------------------------------------------------------

SERVICE_TYPE_COMMISSIONABLE: str = "_mash-comm._tcp.local."
"""DNS-SD service for devices in commissioning mode (``discovery.md §2.1``).

The trailing dot makes this a fully-qualified mDNS service type, which is the
form ``zeroconf.AsyncServiceBrowser`` expects. The spec uses the bare form
``_mash-comm._tcp.local`` in prose — both refer to the same RR.
"""

SERVICE_TYPE_OPERATIONAL: str = "_mash._tcp.local."
"""DNS-SD service for commissioned devices (``discovery.md §2.1``)."""

SERVICE_TYPE_PAIRING_REQUEST: str = "_mashp._udp.local."
"""DNS-SD service for controller-to-device pairing requests
(``discovery.md §2.4``). Browser support deferred to Phase 18."""

MDNS_DOMAIN: str = "local."
"""mDNS link-local domain (RFC 6762)."""

DEFAULT_OPERATIONAL_PORT: int = 8443
"""Default port for ``_mash._tcp`` SRV records (``discovery.md §2.1``).
Commissioning has no canonical default — read SRV per DEC-082."""

# ---------------------------------------------------------------------------
# TXT record keys — commissionable (DEC-078)
# ---------------------------------------------------------------------------

TXT_KEY_DISCRIMINATOR: str = "D"
TXT_KEY_CATEGORIES: str = "cat"
TXT_KEY_SERIAL: str = "serial"
TXT_KEY_BRAND: str = "brand"
TXT_KEY_MODEL: str = "model"
TXT_KEY_DEVICE_NAME: str = "DN"

# ---------------------------------------------------------------------------
# TXT record keys — operational (discovery.md §2.3)
# ---------------------------------------------------------------------------

TXT_KEY_DEVICE_ID: str = "DI"
TXT_KEY_VENDOR_PRODUCT: str = "VP"
TXT_KEY_FIRMWARE: str = "FW"
TXT_KEY_ENDPOINTS: str = "EP"
TXT_KEY_FEATURE_MAP: str = "FM"

# ---------------------------------------------------------------------------
# TXT record keys — pairing request (discovery.md §2.4)
# ---------------------------------------------------------------------------

TXT_KEY_ZONE_ID: str = "ZI"
TXT_KEY_ZONE_NAME: str = "ZN"

# ---------------------------------------------------------------------------
# Limits (discovery.md §3.1, §2.2)
# ---------------------------------------------------------------------------

MAX_DISCRIMINATOR: int = 4095
"""12-bit discriminator upper bound (``discovery.md §3.1``)."""

MAX_INSTANCE_NAME_LEN: int = 63
"""DNS label length limit (RFC 1035)."""

# ---------------------------------------------------------------------------
# Device categories (discovery.md §2.2 — EEBUS SHIP v1.1.0 codes)
# ---------------------------------------------------------------------------

DEVICE_CATEGORY_NAMES: dict[int, str] = {
    1: "GCPH",
    2: "EMS",
    3: "E-MOBILITY",
    4: "HVAC",
    5: "INVERTER",
    6: "APPLIANCE",
    7: "METERING",
}
"""EEBUS SHIP category code → human-readable name (``discovery.md §2.2``).

The spec defines codes 1-7 inclusive. Unknown codes are *not* an error at
discovery time — the spec leaves the value space open for future extensions.
Browser callers receive the raw integer list; rendering is up to them.
"""


# ---------------------------------------------------------------------------
# Result types
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CommissionableService:
    """A device discovered via ``_mash-comm._tcp`` (``discovery.md §2.2``).

    All TXT-derived fields use the spec key names. ``categories`` is the parsed
    comma-separated ``cat`` list; ``device_name`` is empty when the optional
    ``DN`` key is absent.

    ``port`` is the SRV-advertised port and is authoritative per DEC-082 — the
    caller MUST dial this port, not assume any default, because two-port
    deployments advertise different ports for commissioning vs. operational.
    """

    instance_name: str
    """mDNS instance name (e.g. ``MASH-1234``), without the service-type suffix."""

    host: str
    """Host name from the SRV record (e.g. ``evse-001.local.``)."""

    port: int
    """SRV port — authoritative, do not override (DEC-082)."""

    addresses: tuple[str, ...]
    """Resolved IP addresses (IPv4 and/or IPv6)."""

    discriminator: int
    """``D`` TXT — 0..4095."""

    categories: tuple[int, ...]
    """Parsed ``cat`` TXT — EEBUS SHIP category codes."""

    serial: str
    """``serial`` TXT — printed on device label."""

    brand: str
    """``brand`` TXT — vendor / brand name."""

    model: str
    """``model`` TXT — model name."""

    device_name: str = ""
    """Optional ``DN`` TXT — empty when absent."""


@dataclass(frozen=True, slots=True)
class OperationalService:
    """A device discovered via ``_mash._tcp`` (``discovery.md §2.3``).

    The spec defines the service instance name as ``<device-id>._mash._tcp.local``;
    a multi-zone device advertises multiple instances. Optional TXT fields fall
    back to empty / zero when absent (``VP``, ``FW``, ``EP``, ``FM``).
    """

    instance_name: str
    """mDNS instance name (the device-id part of the FQDN)."""

    host: str
    port: int
    addresses: tuple[str, ...]

    device_id: str
    """``DI`` TXT — required (``discovery.md §2.3``)."""

    vendor_product: str = ""
    """``VP`` TXT — optional, e.g. ``1234:5678``."""

    firmware: str = ""
    """``FW`` TXT — optional firmware version."""

    endpoint_count: int = 0
    """``EP`` TXT — optional endpoint count; 0 means absent."""

    feature_map: int = 0
    """``FM`` TXT — optional 32-bit feature map (parsed from hex like ``0x001B``)."""


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class DiscoveryError(Exception):
    """Base class for discovery errors raised by this package."""


class TXTRecordError(DiscoveryError):
    """A TXT record was missing required keys or contained malformed values."""


class BrowseTimeoutError(DiscoveryError):
    """A ``find_*`` call ran out of time before producing a match."""


# Module-level helpers used by both the browser and tests. The intent is that
# the browser does no parsing in-line — it builds a CommissionableService /
# OperationalService through these and surfaces TXTRecordError when fields are
# missing or malformed, so the dispatcher caller has a single place to catch.


def parse_categories(raw: str) -> tuple[int, ...]:
    """Parse the ``cat`` TXT value into a tuple of category codes.

    The spec (``discovery.md §2.2``) defines the value as a comma-separated
    decimal list. Empty entries (e.g. trailing comma) are skipped. A
    non-numeric token raises :class:`TXTRecordError`.
    """
    out: list[int] = []
    for token in raw.split(","):
        token = token.strip()
        if not token:
            continue
        try:
            out.append(int(token))
        except ValueError as exc:
            raise TXTRecordError(
                f"invalid cat TXT entry {token!r} (must be decimal)"
            ) from exc
    return tuple(out)


def parse_feature_map(raw: str) -> int:
    """Parse the operational ``FM`` TXT value (hex like ``0x001B``).

    Plain decimal is also accepted to match the spec's loose phrasing
    ("Feature map (hex)") — implementations may emit either, and a 32-bit
    feature-map fits in either base.
    """
    try:
        if raw.lower().startswith("0x"):
            return int(raw, 16)
        return int(raw, 10)
    except ValueError as exc:
        raise TXTRecordError(f"invalid FM TXT value {raw!r}") from exc


def _decode_txt_value(value: bytes | str | None) -> str | None:
    """Coerce a zeroconf TXT value (``bytes`` or already-decoded ``str``) to text."""
    if value is None:
        return None
    if isinstance(value, bytes):
        return value.decode("utf-8")
    return value


def parse_commissionable_txt(
    instance_name: str,
    host: str,
    port: int,
    addresses: tuple[str, ...],
    txt: Mapping[Any, Any],
) -> CommissionableService:
    """Build a :class:`CommissionableService` from a SRV+TXT record set.

    Required keys (DEC-078): ``D``, ``cat``, ``serial``, ``brand``, ``model``.
    Optional: ``DN``. zeroconf normalises TXT keys to bytes; we accept either
    form for friendliness in unit tests.
    """
    norm = _normalise_txt(txt)

    raw_d = norm.get(TXT_KEY_DISCRIMINATOR)
    if raw_d is None:
        raise TXTRecordError("commissionable TXT missing required 'D' key")
    try:
        discriminator = int(raw_d)
    except ValueError as exc:
        raise TXTRecordError(f"discriminator TXT value {raw_d!r} is not an integer") from exc
    if not 0 <= discriminator <= MAX_DISCRIMINATOR:
        raise TXTRecordError(
            f"discriminator {discriminator} out of range 0..{MAX_DISCRIMINATOR}"
        )

    raw_cat = norm.get(TXT_KEY_CATEGORIES)
    if raw_cat is None:
        raise TXTRecordError("commissionable TXT missing required 'cat' key")
    categories = parse_categories(raw_cat)

    serial = norm.get(TXT_KEY_SERIAL)
    brand = norm.get(TXT_KEY_BRAND)
    model = norm.get(TXT_KEY_MODEL)
    for key, value in (
        (TXT_KEY_SERIAL, serial),
        (TXT_KEY_BRAND, brand),
        (TXT_KEY_MODEL, model),
    ):
        if value is None:
            raise TXTRecordError(f"commissionable TXT missing required {key!r} key")

    return CommissionableService(
        instance_name=instance_name,
        host=host,
        port=port,
        addresses=addresses,
        discriminator=discriminator,
        categories=categories,
        serial=serial or "",
        brand=brand or "",
        model=model or "",
        device_name=norm.get(TXT_KEY_DEVICE_NAME, "") or "",
    )


def parse_operational_txt(
    instance_name: str,
    host: str,
    port: int,
    addresses: tuple[str, ...],
    txt: Mapping[Any, Any],
) -> OperationalService:
    """Build an :class:`OperationalService` from a SRV+TXT record set.

    Required (``discovery.md §2.3``): ``DI``. All others are optional and
    default to empty/zero when absent.
    """
    norm = _normalise_txt(txt)

    device_id = norm.get(TXT_KEY_DEVICE_ID)
    if not device_id:
        raise TXTRecordError("operational TXT missing required 'DI' key")

    endpoint_count = 0
    raw_ep = norm.get(TXT_KEY_ENDPOINTS)
    if raw_ep is not None:
        try:
            endpoint_count = int(raw_ep)
        except ValueError as exc:
            raise TXTRecordError(f"EP TXT value {raw_ep!r} is not an integer") from exc

    feature_map = 0
    raw_fm = norm.get(TXT_KEY_FEATURE_MAP)
    if raw_fm is not None:
        feature_map = parse_feature_map(raw_fm)

    return OperationalService(
        instance_name=instance_name,
        host=host,
        port=port,
        addresses=addresses,
        device_id=device_id,
        vendor_product=norm.get(TXT_KEY_VENDOR_PRODUCT, "") or "",
        firmware=norm.get(TXT_KEY_FIRMWARE, "") or "",
        endpoint_count=endpoint_count,
        feature_map=feature_map,
    )


def _normalise_txt(
    txt: Mapping[Any, Any],
) -> dict[str, str]:
    """zeroconf hands us a ``dict[bytes, bytes | None]``; normalise to ``str``.

    A ``None`` value (TXT key with no ``=``) is dropped — the spec does not
    define any boolean-style TXT keys for MASH, so a missing value is no
    different from a missing key. UTF-8 decoding errors are surfaced as
    :class:`TXTRecordError`.
    """
    out: dict[str, str] = {}
    for raw_key, raw_val in txt.items():
        try:
            key = raw_key.decode("ascii") if isinstance(raw_key, bytes) else raw_key
        except UnicodeDecodeError as exc:
            raise TXTRecordError(f"non-ASCII TXT key {raw_key!r}") from exc
        val = _decode_txt_value(raw_val)
        if val is None:
            continue
        out[key] = val
    return out


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
    "BrowseTimeoutError",
    "CommissionableService",
    "DiscoveryError",
    "OperationalService",
    "TXTRecordError",
    "parse_categories",
    "parse_commissionable_txt",
    "parse_feature_map",
    "parse_operational_txt",
]
