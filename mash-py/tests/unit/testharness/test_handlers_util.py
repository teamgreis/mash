"""Unit tests for the pure-state handlers (wait/compare/evaluate/verify).

These handlers don't talk to a device; they shape the
:class:`~mash_py.testharness.runner.ExecutionState`. Wire-talking
handlers (commission, connect, browse_mdns, read/write/...) are covered
by the interop test suite, which runs them against a live Go device
(``tests/interop/testharness/``).
"""

from __future__ import annotations

import time

import pytest

from mash_py.testharness.handlers._common import KEY_COMMISSIONING_RESULT
from mash_py.testharness.handlers.util import (
    handle_compare,
    handle_compare_values,
    handle_evaluate,
    handle_verify_commissioning_state,
    handle_wait,
)
from mash_py.testharness.loader import Step
from mash_py.testharness.runner import ExecutionState

pytestmark = pytest.mark.asyncio


async def test_wait_sleeps_for_duration() -> None:
    state = ExecutionState()
    start = time.monotonic()
    out = await handle_wait(Step(action="wait", params={"duration_ms": 50}), state)
    elapsed = time.monotonic() - start
    assert out["waited_seconds"] == pytest.approx(0.05)
    assert elapsed >= 0.04  # allow some scheduler slack


async def test_wait_no_duration_is_noop() -> None:
    state = ExecutionState()
    out = await handle_wait(Step(action="wait"), state)
    assert out["waited_seconds"] == 0.0


async def test_compare_interpolates_both_sides() -> None:
    state = ExecutionState()
    state.set("device_id", "ABCD")
    out = await handle_compare(
        Step(
            action="compare",
            params={"expected": "{{ device_id }}", "actual": "ABCD"},
        ),
        state,
    )
    assert out["matches"] is True
    assert out["all_equal"] is True


async def test_compare_values_all_equal_and_all_different() -> None:
    state = ExecutionState()
    state.set("a", "X")
    state.set("b", "X")
    state.set("c", "X")
    out = await handle_compare_values(
        Step(
            action="compare_values",
            params={"values": ["{{ a }}", "{{ b }}", "{{ c }}"]},
        ),
        state,
    )
    assert out["all_equal"] is True
    assert out["all_different"] is False

    state.set("c", "Y")
    out = await handle_compare_values(
        Step(action="compare_values", params={"values": ["{{ a }}", "{{ c }}"]}),
        state,
    )
    assert out["all_equal"] is False
    assert out["all_different"] is True


async def test_evaluate_basic_equality() -> None:
    state = ExecutionState()
    state.set("x", "42")
    out = await handle_evaluate(
        Step(action="evaluate", params={"expression": "{{ x }}==42"}),
        state,
    )
    assert out["result"] is True

    out = await handle_evaluate(
        Step(action="evaluate", params={"expression": "{{ x }}!=43"}),
        state,
    )
    assert out["result"] is True


async def test_verify_commissioning_state_no_result() -> None:
    state = ExecutionState()
    out = await handle_verify_commissioning_state(
        Step(action="verify_commissioning_state"), state
    )
    assert out["commissioning_complete"] is False
    assert "no commissioning_result" in out["error"]


async def test_verify_commissioning_state_with_result() -> None:
    state = ExecutionState()
    state.custom[KEY_COMMISSIONING_RESULT] = {
        "device_id": "ABCD",
        "zone_ca": object(),
        "operational_cert": object(),
    }
    out = await handle_verify_commissioning_state(
        Step(action="verify_commissioning_state"), state
    )
    assert out["commissioning_complete"] is True
    assert out["device_id_assigned"] is True
    assert out["zone_ca_stored"] is True
    assert out["operational_cert_active"] is True
