"""Shared helpers for the Phase-21 test-harness handlers.

Concentrating these in one place keeps the per-action handler files small
and makes the spec-vs-Go choices explicit (each helper documents which
spec section grounds it).
"""

from __future__ import annotations

from typing import Any

from mash_py.testharness.runner import ExecutionState, interpolate

# State keys (cross-handler). Handlers stash side-state under these
# documented names instead of inventing private keys per call site.
KEY_COMMISSIONING_RESULT = "commissioning_result"
"""Handler stash: the live ``commissioning_result`` dict carrying the
operational cert, controller cert, zone CA, and device id from the most
recent ``commission`` action. ``connect_operational`` / ``read`` consult it."""

KEY_DISCOVERED_SERVICES = "discovered_services"
"""Handler stash: the most recent mDNS browse result (list of dicts).
``verify_txt_records`` reads this; ``stop_discovery`` clears it."""

KEY_ACTIVE_DISCOVERY = "active_discovery"
"""Handler stash: the most recent ``start_discovery`` parameters."""

KEY_OPERATIONAL_CONN = "operational_connection"
"""Handler stash: the live operational connection dict (``reader``,
``writer``, ``client``) so subsequent ``read``/``invoke`` reuse it."""


def pick_target_host(addresses: list[str] | tuple[str, ...], host: str = "") -> str:
    """Return the best dial target from a set of mDNS-resolved addresses.

    Preference order (matches Phase 9's interop fixture):

    1. ``127.0.0.1`` if present (loopback always resolves)
    2. First IPv4 address (avoids ``%scope`` hassles with link-local v6)
    3. First address regardless of family
    4. Fall back to the SRV ``host`` minus the trailing dot

    Spec-vs-Go: spec says SRV port + AAAA records; nothing about
    address-selection priority. mash-go's controller does the same v4-first
    preference (`mash-go/internal/testharness/runner/dialer.go`); we mirror
    it because the spec is silent and identical behavior keeps interop
    diff-able with mash-test.
    """
    addresses = tuple(addresses or ())
    if "127.0.0.1" in addresses:
        return "127.0.0.1"
    if addresses:
        ipv4 = next((a for a in addresses if ":" not in a), None)
        return ipv4 or addresses[0]
    if host:
        return host.rstrip(".")
    return ""


def get_setup_code(state: ExecutionState, params: dict[str, Any]) -> str:
    """Resolve the PASE setup code: step params override CLI default.

    YAML cases pass ``"{{ setup_code }}"`` so the value is recorded once
    at runner-config time and reused per-step. We interpolate against
    state in case the user has stashed a different value mid-suite.
    """
    explicit = interpolate(params.get("setup_code"), state)
    if isinstance(explicit, str) and explicit and "{{" not in explicit:
        return explicit
    if state.config and state.config.setup_code:
        return state.config.setup_code
    raise ValueError("no setup_code available (pass --setup-code or step params.setup_code)")


def get_discriminator(state: ExecutionState, params: dict[str, Any]) -> int:
    """Resolve the device discriminator: step params override CLI default."""
    raw = interpolate(
        params.get("device_discriminator") or params.get("discriminator"),
        state,
    )
    if raw is not None and not (isinstance(raw, str) and "{{" in raw):
        try:
            return int(raw)
        except (TypeError, ValueError):
            pass
    if state.config:
        return int(state.config.discriminator)
    raise ValueError("no discriminator available")


def add_teardown(state: ExecutionState, callback: Any) -> None:
    """Register a teardown callback to run after the current test case."""
    teardowns = state.custom.setdefault("_teardowns", [])
    teardowns.append(callback)


def truthy(params: dict[str, Any], key: str, default: bool = False) -> bool:
    val = params.get(key)
    if val is None:
        return default
    if isinstance(val, bool):
        return val
    if isinstance(val, (int, float)):
        return val != 0
    if isinstance(val, str):
        return val.lower() in ("true", "yes", "1")
    return bool(val)
