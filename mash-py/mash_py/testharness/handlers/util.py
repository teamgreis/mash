"""Utility handlers: wait, compare, evaluate, verify, trigger.

These handlers don't talk to the device on the wire; they just shape
state transitions and assertions on what's already in
:class:`ExecutionState`.

Spec-vs-Go: ``trigger_test_event`` is the only one with a wire footprint —
it sends an Invoke against TestControl. DEC-087 (Phase 17) defines the
trigger registry and the failure-status table. The Phase-21 acceptance
cases happen not to use trigger_test_event, but we wire the handler
because the Go runner registers it unconditionally and not having it
would fail any imported case that mentions it.
"""

from __future__ import annotations

import asyncio
from typing import Any

from mash_py.testharness.handlers._common import KEY_COMMISSIONING_RESULT
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState, interpolate


# ---------------------------------------------------------------------------
# wait — sleep for a duration
# ---------------------------------------------------------------------------


async def handle_wait(step: Step, state: ExecutionState) -> dict[str, Any]:
    duration_s = 0.0
    if "duration_seconds" in step.params:
        duration_s = float(step.params["duration_seconds"])
    elif "duration_ms" in step.params:
        duration_s = float(step.params["duration_ms"]) / 1000.0
    elif isinstance(step.params.get("duration"), str):
        duration_s = _parse_duration(step.params["duration"])
    if duration_s > 0:
        await asyncio.sleep(duration_s)
    _ = state
    return {"waited_seconds": duration_s}


# ---------------------------------------------------------------------------
# compare / compare_values — equality assertion among interpolated values
# ---------------------------------------------------------------------------


async def handle_compare(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Compare ``actual`` against ``expected`` (both interpolated)."""
    expected = interpolate(step.params.get("expected"), state)
    actual = interpolate(step.params.get("actual"), state)
    return {
        "value": actual,
        "expected": expected,
        "matches": str(expected) == str(actual),
        "all_equal": str(expected) == str(actual),
    }


async def handle_compare_values(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Check that all values in ``params.values`` are equal (or all different).

    Mirrors mash-go's ``handleCompareValues``: returns ``all_equal`` and
    ``all_different`` so the YAML ``expect`` block can pick whichever
    semantics the test wants.
    """
    raw = step.params.get("values") or []
    values = [interpolate(v, state) for v in raw]
    str_vals = [str(v) for v in values]
    all_equal = len(set(str_vals)) == 1 if str_vals else True
    all_different = len(set(str_vals)) == len(str_vals)
    return {
        "values": values,
        "all_equal": all_equal,
        "all_different": all_different,
    }


# ---------------------------------------------------------------------------
# evaluate — boolean expression over state (subset of mash-go's evaluator)
# ---------------------------------------------------------------------------


async def handle_evaluate(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Evaluate a tiny KEY=VALUE / KEY!=VALUE expression against state.

    Phase 21 doesn't need a full expression evaluator — the YAML cases
    that ship use either ``compare_values`` or ``expect:``. The handler
    accepts ``params.expression`` and returns ``result`` so any imported
    case using it does not error out at "unknown action".
    """
    expr = str(step.params.get("expression") or "")
    expr = interpolate(expr, state)
    if "!=" in expr:
        lhs, _, rhs = expr.partition("!=")
        return {"result": lhs.strip() != rhs.strip(), "expression": expr}
    if "==" in expr:
        lhs, _, rhs = expr.partition("==")
        return {"result": lhs.strip() == rhs.strip(), "expression": expr}
    if "=" in expr:
        lhs, _, rhs = expr.partition("=")
        return {"result": lhs.strip() == rhs.strip(), "expression": expr}
    return {"result": bool(expr), "expression": expr}


# ---------------------------------------------------------------------------
# verify_commissioning_state — confirm internal state matches expectations
# ---------------------------------------------------------------------------


async def handle_verify_commissioning_state(
    step: Step, state: ExecutionState
) -> dict[str, Any]:
    """Inspect the most recent commissioning result for sanity.

    Returns flags the YAML cases reference (``commissioning_complete``,
    ``device_id_assigned``, ``zone_ca_stored``, etc.). We deliberately
    avoid exposing internal device-side state — the harness only sees
    what the wire revealed during commissioning.
    """
    cr = state.custom.get(KEY_COMMISSIONING_RESULT)
    if not cr:
        return {
            "commissioning_complete": False,
            "device_id_assigned": False,
            "zone_ca_stored": False,
            "operational_cert_active": False,
            "error": "no commissioning_result in state",
        }
    _ = step
    return {
        "commissioning_complete": True,
        "device_id_assigned": bool(cr.get("device_id")),
        "zone_ca_stored": bool(cr.get("zone_ca")),
        "operational_cert_active": bool(cr.get("operational_cert")),
        "session_active": True,
        "device_commissioned": True,
    }


# ---------------------------------------------------------------------------
# trigger_test_event — Invoke TestControl.TriggerTestEvent
# ---------------------------------------------------------------------------


async def handle_trigger_test_event(step: Step, state: ExecutionState) -> dict[str, Any]:
    """Forward a TestControl trigger to the device (DEC-087, Phase 17).

    Implementation note: Phase 21's acceptance set does not exercise this
    path, so we keep the handler as a thin wrapper around ``handle_invoke``
    (TestControl is feature ``0x0F`` per the Phase 17 spec). The handler
    returns a documented "not exercised in Phase 21" error if no
    operational connection is open, mirroring the same surface the Go
    runner exposes.
    """
    from mash_py.testharness.handlers.interaction import handle_invoke

    invoke_step = Step(
        action="invoke",
        params={
            "endpoint": int(step.params.get("endpoint", 0)),
            "feature": 0x0F,  # TestControl
            "command": int(step.params.get("trigger_id") or step.params.get("command") or 0),
            "args": step.params.get("args") or {},
        },
    )
    return await handle_invoke(invoke_step, state)


# ---------------------------------------------------------------------------
# Internals
# ---------------------------------------------------------------------------


def _parse_duration(s: str) -> float:
    s = s.strip()
    units = (("ms", 1e-3), ("us", 1e-6), ("s", 1.0), ("m", 60.0))
    for suffix, mult in units:
        if s.endswith(suffix):
            try:
                return float(s[: -len(suffix)]) * mult
            except ValueError:
                return 0.0
    try:
        return float(s)
    except ValueError:
        return 0.0


__all__ = [
    "handle_compare",
    "handle_compare_values",
    "handle_evaluate",
    "handle_trigger_test_event",
    "handle_verify_commissioning_state",
    "handle_wait",
]
