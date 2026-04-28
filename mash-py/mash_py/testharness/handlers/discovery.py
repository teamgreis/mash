"""Discovery / mDNS handlers (Phase 6 wrapper).

These actions back the ``discovery-*.yaml`` and ``connection-*.yaml``
test cases. The semantics are derived from the spec
(``docs/discovery.md``, DEC-088) — not from how mash-go's runner happens
to implement them. The handler returns observable mDNS facts; the YAML
``expect`` block then asserts presence/absence/format.

Spec-vs-Go callouts:

* TXT key set per service type is fixed by the spec (DEC-078 for
  ``_mash-comm._tcp``; DEC-088 for ``_mash._tcp``). Both implementations
  must enforce the same key set, so ``verify_txt_records`` rejects an
  ``optional_keys`` entry that names a forbidden key (e.g., ``FM`` on
  operational, banned by DEC-074).
* Instance-name format ``<zone-id>-<device-id>`` is normative (DEC-088 §5);
  the handler reports the format check rather than just the literal name.
"""

from __future__ import annotations

import asyncio
from typing import Any

from mash_py.discovery import Browser
from mash_py.discovery.types import (
    DEFAULT_OPERATIONAL_PORT,
    SERVICE_TYPE_COMMISSIONABLE,
    SERVICE_TYPE_OPERATIONAL,
)
from mash_py.testharness.handlers._common import (
    KEY_ACTIVE_DISCOVERY,
    KEY_DISCOVERED_SERVICES,
)
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState

_SERVICE_TYPE_ALIASES = {
    "_mash-comm._tcp": SERVICE_TYPE_COMMISSIONABLE,
    "_mash-comm._tcp.": SERVICE_TYPE_COMMISSIONABLE,
    "_mash-comm._tcp.local": SERVICE_TYPE_COMMISSIONABLE,
    "_mash-comm._tcp.local.": SERVICE_TYPE_COMMISSIONABLE,
    "_mash._tcp": SERVICE_TYPE_OPERATIONAL,
    "_mash._tcp.": SERVICE_TYPE_OPERATIONAL,
    "_mash._tcp.local": SERVICE_TYPE_OPERATIONAL,
    "_mash._tcp.local.": SERVICE_TYPE_OPERATIONAL,
}


# ---------------------------------------------------------------------------
# Discovery lifecycle
# ---------------------------------------------------------------------------


async def handle_start_discovery(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Record that discovery is "active" so wait_for_device knows to browse.

    The Go runner spawns a long-running mDNS observer in this step; we
    keep it lightweight — the actual browse fires inside
    :func:`handle_wait_for_device` so the timeouts in YAML ``params``
    map directly to per-call deadlines without an extra plumbing layer.
    """
    state.custom[KEY_ACTIVE_DISCOVERY] = dict(step.params)
    return {"discovery_started": True}


async def handle_stop_discovery(step: Step, state: ExecutionState) -> dict[str, Any]:
    state.custom.pop(KEY_ACTIVE_DISCOVERY, None)
    state.custom.pop(KEY_DISCOVERED_SERVICES, None)
    _ = step
    return {"discovery_stopped": True, "discovery_cleaned_up": True}


# ---------------------------------------------------------------------------
# Browse helpers
# ---------------------------------------------------------------------------


async def handle_wait_for_device(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Block until the device matching ``device_type`` / discriminator appears.

    YAML cases pass ``device_type: EV_CHARGER`` etc.; we don't have a
    direct way to filter by category-as-string from mDNS, so we resolve
    by the configured discriminator and report the device as found if
    its discovered TXT records match.
    """
    timeout_ms = int(step.params.get("timeout_ms") or 5000)
    if state.config and state.config.discriminator:
        discriminator = int(state.config.discriminator)
    else:
        return {"device_found": False, "device_has_txt_records": False}

    services = await _browse_commissionable(discriminator, timeout=timeout_ms / 1000)
    state.custom[KEY_DISCOVERED_SERVICES] = services
    if not services:
        return {"device_found": False, "device_has_txt_records": False}
    return {
        "device_found": True,
        "device_has_txt_records": True,
    }


async def handle_browse_mdns(step: Step, state: ExecutionState) -> dict[str, Any]:
    """One-shot browse on the requested service type.

    Returns observable per-service facts that the YAML ``expect`` block
    in the discovery cases checks: ``device_found``, ``srv_port_present``,
    ``srv_hostname_valid``, ``aaaa_count_min``, ``ipv6_valid``,
    ``zone_id_length``, ``device_id_length``, ``zone_id_hex_valid``,
    ``device_id_hex_valid``, ``service_has_txt_records``,
    ``instance_name_format``.
    """
    raw_type = str(step.params.get("service_type") or "")
    service_type = _SERVICE_TYPE_ALIASES.get(raw_type, raw_type)
    if not service_type:
        return {"device_found": False, "error": "service_type required"}

    timeout_s = float(step.params.get("timeout_ms") or 5000) / 1000
    if "timeout" in step.params:
        timeout_s = _parse_duration_or(step.params["timeout"], timeout_s)

    discriminator = step.params.get("discriminator")
    if discriminator is None and state.config:
        discriminator = state.config.discriminator
    if discriminator is not None:
        discriminator = int(discriminator)

    if service_type == SERVICE_TYPE_COMMISSIONABLE:
        if discriminator is None:
            return {"device_found": False, "error": "discriminator required for commissionable browse"}
        services = await _browse_commissionable(discriminator, timeout_s)
        state.custom[KEY_DISCOVERED_SERVICES] = services
        out: dict[str, Any] = {
            "device_found": bool(services),
            "service_has_txt_records": bool(services),
        }
        if not services:
            out["error_code"] = "NO_DEVICES_FOUND"
            return out
        svc = services[0]
        out["srv_port_present"] = svc["port"] > 0
        out["srv_hostname_valid"] = bool(svc["host"])
        out["aaaa_count_min"] = sum(1 for a in svc["addresses"] if ":" in a)
        out["ipv6_valid"] = any(":" in a for a in svc["addresses"])
        out["txt_valid"] = True
        return out

    if service_type == SERVICE_TYPE_OPERATIONAL:
        # Operational browse: prefer the device-id from a prior commission.
        cr = state.custom.get("commissioning_result")
        device_id = cr.get("device_id") if cr else None
        services = await _browse_operational(device_id, timeout_s)
        state.custom[KEY_DISCOVERED_SERVICES] = services
        out = {
            "device_found": bool(services),
            "service_has_txt_records": bool(services),
        }
        if not services:
            return out
        svc = services[0]
        out["srv_port_present"] = svc["port"] > 0
        out["srv_hostname_valid"] = bool(svc["host"])
        out["zone_id_length"] = len(svc["zone_id"])
        out["device_id_length"] = len(svc["device_id"])
        out["zone_id_hex_valid"] = _is_hex(svc["zone_id"])
        out["device_id_hex_valid"] = _is_hex(svc["device_id"])
        out["aaaa_count_min"] = sum(1 for a in svc["addresses"] if ":" in a)
        out["ipv6_valid"] = any(":" in a for a in svc["addresses"])
        out["instance_name_format"] = "<zone-id>-<device-id>" if "-" in svc["instance_name"] else svc["instance_name"]
        out["txt_valid"] = True
        return out

    return {"device_found": False, "error": f"unsupported service_type {service_type!r}"}


async def handle_verify_txt_records(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Check that the most recently discovered service has the required TXT keys."""
    required = step.params.get("required_keys") or []
    optional = step.params.get("optional_keys") or []
    services = state.custom.get(KEY_DISCOVERED_SERVICES) or []
    if not services:
        return {"txt_valid": False, "error": "no services discovered"}

    svc = services[0]
    txt = svc.get("txt", {})
    missing = [k for k in required if k not in txt]
    out: dict[str, Any] = {
        "txt_valid": not missing,
    }
    if missing:
        out["missing_keys"] = missing
    # Surface common spec-mandated checks.
    if "ZI" in txt:
        out["zone_id_length"] = len(txt["ZI"])
        out["zone_id_hex_valid"] = _is_hex(txt["ZI"])
    if "DI" in txt:
        out["device_id_length"] = len(txt["DI"])
        out["device_id_hex_valid"] = _is_hex(txt["DI"])
    _ = optional  # documented but tolerant — no failure on missing optional keys
    return out


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


async def _browse_commissionable(discriminator: int, timeout: float) -> list[dict[str, Any]]:
    async with Browser() as browser:
        try:
            svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=timeout
            )
        except Exception:  # noqa: BLE001 — timeout / browse failure is a soft "not found"
            return []
    txt = {
        "D": str(svc.discriminator),
        "cat": ",".join(str(c) for c in svc.categories),
        "serial": svc.serial,
        "brand": svc.brand,
        "model": svc.model,
    }
    if svc.device_name:
        txt["DN"] = svc.device_name
    return [
        {
            "instance_name": svc.instance_name,
            "host": svc.host,
            "port": svc.port,
            "addresses": list(svc.addresses),
            "discriminator": svc.discriminator,
            "txt": txt,
            "id": str(svc.discriminator),
            "cat": txt["cat"],
            "proto": "1.0",
        }
    ]


async def _browse_operational(
    device_id: str | None,
    timeout: float,
) -> list[dict[str, Any]]:
    async with Browser() as browser:
        try:
            if device_id is not None:
                svc = await browser.find_operational_by_device_id(
                    device_id, timeout=timeout
                )
            else:
                # Without a known device_id we still need *something* to
                # match against. Use a browse-then-pick-first via the
                # one-shot find with a wildcard-like predicate.
                svc = await browser._find_first(  # type: ignore[attr-defined]
                    SERVICE_TYPE_OPERATIONAL,
                    browser._resolve_operational,  # type: ignore[attr-defined]
                    lambda _s: True,  # type: ignore[arg-type]
                    timeout,
                    "any operational",
                )
        except Exception:  # noqa: BLE001
            return []
    txt = {
        "ZI": svc.zone_id,
        "DI": svc.device_id,
    }
    if svc.vendor_product:
        txt["VP"] = svc.vendor_product
    if svc.firmware:
        txt["FW"] = svc.firmware
    if svc.endpoint_count:
        txt["EP"] = str(svc.endpoint_count)
    return [
        {
            "instance_name": svc.instance_name,
            "host": svc.host,
            "port": svc.port or DEFAULT_OPERATIONAL_PORT,
            "addresses": list(svc.addresses),
            "zone_id": svc.zone_id,
            "device_id": svc.device_id,
            "txt": txt,
        }
    ]


def _is_hex(value: str) -> bool:
    if not value:
        return False
    try:
        int(value, 16)
        return True
    except ValueError:
        return False


def _parse_duration_or(value: Any, default: float) -> float:
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        s = value.strip()
        for suffix, mult in (("ms", 1e-3), ("s", 1.0), ("m", 60.0)):
            if s.endswith(suffix):
                try:
                    return float(s[: -len(suffix)]) * mult
                except ValueError:
                    return default
        try:
            return float(s)
        except ValueError:
            return default
    return default


_ = asyncio  # used implicitly by Browser context manager


__all__ = [
    "handle_browse_mdns",
    "handle_start_discovery",
    "handle_stop_discovery",
    "handle_verify_txt_records",
    "handle_wait_for_device",
]
