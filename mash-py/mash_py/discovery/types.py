"""Constants and result types for MASH mDNS discovery.

Grounded in the spec (canonical), not the Go reference:

- ``docs/discovery.md §1``, ``§2.1`` — three service types, SRV port is authoritative.
- ``docs/discovery.md §2.2`` + DEC-078 — six-key commissionable TXT schema
  (``D``, ``cat``, ``serial``, ``brand``, ``model``, ``DN``); instance name
  ``MASH-NNNN`` (4-digit zero-padded discriminator, per DEC-088).
- ``docs/discovery.md §2.3`` + DEC-088 — operational TXT canonicalization:
  required ``ZI`` (16 hex), ``DI`` (16 hex); optional ``VP``, ``FW``, ``EP``;
  ``FM`` forbidden (DEC-074); instance name ``<zone-id>-<device-id>``.
  Multi-zone devices advertise one instance per (device, zone) pair (DEC-082).
- DEC-082 — two-port deployment: SRV port differs per service; readers MUST
  read the SRV port as ground truth, never assume a default.

Phase 6 of mash-py-tdd-plan.md ships only the browse paths for
``_mash-comm._tcp`` and ``_mash._tcp``. Phase 18 adds the advertiser side
(``mash_py.discovery.advertiser``); the pairing-request service
(``_mashp._udp``) listener is deferred to a later phase.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from typing import Any

# ---------------------------------------------------------------------------
# Service types (DNS-SD service names)
# ---------------------------------------------------------------------------

SERVICE_TYPE_COMMISSIONABLE: str = "_mash-comm._tcp.local."
"""DNS-SD service for devices in commissioning mode (``discovery.md §2.1``).

The trailing dot makes this a fully-qualified mDNS service type, which is the
form ``zeroconf.AsyncServiceBrowser`` and ``AsyncZeroconf.async_register_service``
both expect. The spec uses the bare form ``_mash-comm._tcp.local`` in prose —
both refer to the same RR.
"""

SERVICE_TYPE_OPERATIONAL: str = "_mash._tcp.local."
"""DNS-SD service for commissioned devices (``discovery.md §2.1``)."""

SERVICE_TYPE_PAIRING_REQUEST: str = "_mashp._udp.local."
"""DNS-SD service for controller-to-device pairing requests
(``discovery.md §2.4``). Listener support deferred to a later phase."""

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
# TXT record keys — operational (DEC-088)
# ---------------------------------------------------------------------------

TXT_KEY_ZONE_ID: str = "ZI"
"""Operational TXT — required (DEC-088). Also reused by ``_mashp._udp`` (§2.4)."""

TXT_KEY_DEVICE_ID: str = "DI"
TXT_KEY_VENDOR_PRODUCT: str = "VP"
TXT_KEY_FIRMWARE: str = "FW"
TXT_KEY_ENDPOINTS: str = "EP"

# ---------------------------------------------------------------------------
# TXT record keys — pairing request (discovery.md §2.4)
# ---------------------------------------------------------------------------

TXT_KEY_ZONE_NAME: str = "ZN"

# ---------------------------------------------------------------------------
# Limits (discovery.md §3, DEC-078, DEC-088)
# ---------------------------------------------------------------------------

MAX_DISCRIMINATOR: int = 4095
"""12-bit discriminator upper bound (``discovery.md §3.1``)."""

MAX_INSTANCE_NAME_LEN: int = 63
"""DNS label length limit (RFC 1035)."""

ID_HEX_LEN: int = 16
"""Length of `ZI` and `DI` TXT values (16 hex chars = 64 bits, DEC-088)."""

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
# Result types (browse-side)
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
    """A device discovered via ``_mash._tcp`` (``discovery.md §2.3``, DEC-088).

    Per DEC-088 the canonical instance-name format is ``<zone-id>-<device-id>``
    and ``ZI`` + ``DI`` are both required TXT keys. A device that participates
    in multiple zones advertises one instance per (zone, device) pair.

    Optional TXT fields fall back to empty / zero when absent (``VP``, ``FW``,
    ``EP``). Per DEC-074, ``FM`` is forbidden in operational TXT — decoders
    MUST ignore any legacy ``FM`` key, which is what this parser does.
    """

    instance_name: str
    """mDNS instance name — ``<zone-id>-<device-id>`` per DEC-088."""

    host: str
    port: int
    addresses: tuple[str, ...]

    zone_id: str
    """``ZI`` TXT — required, 16 hex chars (DEC-088)."""

    device_id: str
    """``DI`` TXT — required, 16 hex chars (DEC-088)."""

    vendor_product: str = ""
    """``VP`` TXT — optional, e.g. ``1234:5678``."""

    firmware: str = ""
    """``FW`` TXT — optional firmware version."""

    endpoint_count: int = 0
    """``EP`` TXT — optional endpoint count; 0 means absent."""


# ---------------------------------------------------------------------------
# Advertise-side input types (Phase 18)
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CommissionableAdvertisement:
    """Inputs needed to advertise a ``_mash-comm._tcp`` instance (DEC-078).

    ``host`` is the server name placed in the SRV record (e.g. ``evse-001.local.``);
    if empty, the advertiser will derive it from the local hostname.

    ``port`` is the commissioning listener port — DEC-082 leaves the value
    implementation-defined; callers MUST advertise the actual port their
    commissioning listener bound, not a fixed default.

    ``addresses`` is an optional tuple of IPv4 / IPv6 strings to publish; if
    empty, the advertiser will derive the host's non-loopback addresses.
    """

    discriminator: int
    serial: str
    brand: str
    model: str
    port: int
    categories: tuple[int, ...] = ()
    device_name: str = ""
    host: str = ""
    addresses: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class OperationalAdvertisement:
    """Inputs needed to advertise a single ``_mash._tcp`` instance (DEC-088).

    Both ``zone_id`` and ``device_id`` are required and MUST each be 16 hex
    chars. ``port`` defaults to ``DEFAULT_OPERATIONAL_PORT`` (8443) per DEC-082;
    override only if the operational listener bound a different port.

    Optional TXT fields are emitted only when set; ``FM`` is forbidden by
    DEC-074 and has no field here.
    """

    zone_id: str
    device_id: str
    port: int = DEFAULT_OPERATIONAL_PORT
    vendor_product: str = ""
    firmware: str = ""
    endpoint_count: int = 0
    host: str = ""
    addresses: tuple[str, ...] = ()


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class DiscoveryError(Exception):
    """Base class for discovery errors raised by this package."""


class TXTRecordError(DiscoveryError):
    """A TXT record was missing required keys or contained malformed values."""


class BrowseTimeoutError(DiscoveryError):
    """A ``find_*`` call ran out of time before producing a match."""


class AdvertiseError(DiscoveryError):
    """An advertiser request was malformed (e.g., out-of-range discriminator,
    non-hex zone-id, instance name too long)."""


# ---------------------------------------------------------------------------
# Helpers — instance names (DEC-088)
# ---------------------------------------------------------------------------


def commissionable_instance_name(discriminator: int) -> str:
    """Return the canonical commissionable instance-name (DEC-088 §5).

    Format: ``MASH-NNNN`` where ``NNNN`` is the discriminator zero-padded to
    four decimal digits. Raises :class:`AdvertiseError` if the discriminator
    is out of range.
    """
    if not 0 <= discriminator <= MAX_DISCRIMINATOR:
        raise AdvertiseError(
            f"discriminator {discriminator} out of range 0..{MAX_DISCRIMINATOR}"
        )
    return f"MASH-{discriminator:04d}"


def operational_instance_name(zone_id: str, device_id: str) -> str:
    """Return the canonical operational instance-name ``<zone-id>-<device-id>``
    (DEC-088). Both halves must be 16 hex chars; raises :class:`AdvertiseError`
    otherwise.
    """
    _validate_id(zone_id, label="zone_id")
    _validate_id(device_id, label="device_id")
    name = f"{zone_id}-{device_id}"
    if len(name) > MAX_INSTANCE_NAME_LEN:  # 33 ≤ 63 always; defensive.
        raise AdvertiseError(f"instance name {name!r} exceeds {MAX_INSTANCE_NAME_LEN} chars")
    return name


def _validate_id(value: str, *, label: str) -> None:
    if len(value) != ID_HEX_LEN:
        raise AdvertiseError(
            f"{label} must be {ID_HEX_LEN} hex chars (got {len(value)}: {value!r})"
        )
    try:
        int(value, 16)
    except ValueError as exc:
        raise AdvertiseError(f"{label} must be hex (got {value!r})") from exc


# ---------------------------------------------------------------------------
# Helpers — TXT encoding (DEC-078, DEC-088)
# ---------------------------------------------------------------------------


def build_commissionable_txt(adv: CommissionableAdvertisement) -> dict[str, str]:
    """Return the TXT key/value map for ``_mash-comm._tcp`` per DEC-078.

    Required keys are always emitted; ``DN`` is emitted only when the optional
    ``device_name`` is non-empty. Categories are joined with commas.
    """
    if not 0 <= adv.discriminator <= MAX_DISCRIMINATOR:
        raise AdvertiseError(
            f"discriminator {adv.discriminator} out of range 0..{MAX_DISCRIMINATOR}"
        )
    for label, value in (
        ("serial", adv.serial),
        ("brand", adv.brand),
        ("model", adv.model),
    ):
        if not value:
            raise AdvertiseError(f"commissionable advertisement missing required {label!r}")

    txt: dict[str, str] = {
        TXT_KEY_DISCRIMINATOR: str(adv.discriminator),
        TXT_KEY_CATEGORIES: ",".join(str(c) for c in adv.categories),
        TXT_KEY_SERIAL: adv.serial,
        TXT_KEY_BRAND: adv.brand,
        TXT_KEY_MODEL: adv.model,
    }
    if adv.device_name:
        txt[TXT_KEY_DEVICE_NAME] = adv.device_name
    return txt


def build_operational_txt(adv: OperationalAdvertisement) -> dict[str, str]:
    """Return the TXT key/value map for ``_mash._tcp`` per DEC-088.

    ``ZI`` and ``DI`` are required (validated to 16 hex chars); ``VP``, ``FW``
    are emitted only when non-empty; ``EP`` is emitted only when > 0. ``FM``
    is never emitted (DEC-074).
    """
    _validate_id(adv.zone_id, label="zone_id")
    _validate_id(adv.device_id, label="device_id")
    if adv.endpoint_count < 0 or adv.endpoint_count > 255:
        raise AdvertiseError(
            f"endpoint_count {adv.endpoint_count} out of range 0..255"
        )

    txt: dict[str, str] = {
        TXT_KEY_ZONE_ID: adv.zone_id,
        TXT_KEY_DEVICE_ID: adv.device_id,
    }
    if adv.vendor_product:
        txt[TXT_KEY_VENDOR_PRODUCT] = adv.vendor_product
    if adv.firmware:
        txt[TXT_KEY_FIRMWARE] = adv.firmware
    if adv.endpoint_count > 0:
        txt[TXT_KEY_ENDPOINTS] = str(adv.endpoint_count)
    return txt


# ---------------------------------------------------------------------------
# Helpers — TXT parsing (browse-side; DEC-078, DEC-088)
# ---------------------------------------------------------------------------


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

    Required (DEC-088): ``ZI`` and ``DI``, each 16 hex chars. Optional:
    ``VP``, ``FW``, ``EP``. Per DEC-074 a legacy ``FM`` key, if present, is
    silently ignored.
    """
    norm = _normalise_txt(txt)

    zone_id = norm.get(TXT_KEY_ZONE_ID, "")
    if not zone_id:
        raise TXTRecordError("operational TXT missing required 'ZI' key (DEC-088)")
    if len(zone_id) != ID_HEX_LEN:
        raise TXTRecordError(
            f"operational TXT 'ZI' must be {ID_HEX_LEN} hex chars (got {zone_id!r})"
        )
    try:
        int(zone_id, 16)
    except ValueError as exc:
        raise TXTRecordError(f"operational TXT 'ZI' is not hex: {zone_id!r}") from exc

    device_id = norm.get(TXT_KEY_DEVICE_ID, "")
    if not device_id:
        raise TXTRecordError("operational TXT missing required 'DI' key (DEC-088)")
    if len(device_id) != ID_HEX_LEN:
        raise TXTRecordError(
            f"operational TXT 'DI' must be {ID_HEX_LEN} hex chars (got {device_id!r})"
        )
    try:
        int(device_id, 16)
    except ValueError as exc:
        raise TXTRecordError(f"operational TXT 'DI' is not hex: {device_id!r}") from exc

    endpoint_count = 0
    raw_ep = norm.get(TXT_KEY_ENDPOINTS)
    if raw_ep is not None:
        try:
            endpoint_count = int(raw_ep)
        except ValueError as exc:
            raise TXTRecordError(f"EP TXT value {raw_ep!r} is not an integer") from exc

    return OperationalService(
        instance_name=instance_name,
        host=host,
        port=port,
        addresses=addresses,
        zone_id=zone_id,
        device_id=device_id,
        vendor_product=norm.get(TXT_KEY_VENDOR_PRODUCT, "") or "",
        firmware=norm.get(TXT_KEY_FIRMWARE, "") or "",
        endpoint_count=endpoint_count,
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


# Avoid the dataclass-default ``field`` linter warning when callers omit
# ``categories`` / ``addresses`` for advertisement objects.
_ = field

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
    "BrowseTimeoutError",
    "CommissionableAdvertisement",
    "CommissionableService",
    "DiscoveryError",
    "OperationalAdvertisement",
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
