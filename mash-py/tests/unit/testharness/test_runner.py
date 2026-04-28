"""Runner-engine unit tests using in-process mock handlers.

These tests cover the engine's contract:

* expectations are evaluated after a step's outputs are merged into state
* ``store_result`` flattens output keys under the named prefix
* ``{{ name }}`` interpolation resolves template references in params
* unknown actions surface as a clear error rather than crashing
* PICS requirements that aren't met cause the case to be skipped
* timeouts at the test and step level both surface as failures
"""

from __future__ import annotations

import asyncio
from typing import Any

import pytest

from mash_py.testharness.loader import Step, TestCase
from mash_py.testharness.pics import parse_pics
from mash_py.testharness.runner import (
    ExecutionState,
    Runner,
    RunnerConfig,
    interpolate,
)


pytestmark = pytest.mark.asyncio


# ---------------------------------------------------------------------------
# Engine contract
# ---------------------------------------------------------------------------


async def test_run_case_passes_when_all_expects_met() -> None:
    runner = Runner()

    async def echo(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {"answer": 42}

    runner.register("echo", echo)
    tc = TestCase(
        id="TC-OK",
        steps=[Step(action="echo", expect={"answer": 42})],
    )
    result = await runner.run_case(tc)
    assert result.passed
    assert result.steps[0].expects["answer"].passed


async def test_run_case_fails_on_unmet_expect() -> None:
    runner = Runner()

    async def echo(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {"answer": 41}

    runner.register("echo", echo)
    tc = TestCase(
        id="TC-FAIL",
        steps=[Step(action="echo", expect={"answer": 42})],
    )
    result = await runner.run_case(tc)
    assert not result.passed
    assert "expectation failed" in result.error


async def test_unknown_action_surfaces_clear_error() -> None:
    runner = Runner()
    tc = TestCase(id="TC-X", steps=[Step(action="missing")])
    result = await runner.run_case(tc)
    assert not result.passed
    assert "unknown action" in (result.steps[0].error or result.error)


async def test_store_result_flattens_outputs() -> None:
    runner = Runner()
    seen: dict[str, Any] = {}

    async def producer(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {"device_id": "ABCD1234", "extra": 7}

    async def consumer(step: Step, state: ExecutionState) -> dict[str, Any]:
        seen.update(state.outputs)
        return {}

    runner.register("producer", producer)
    runner.register("consumer", consumer)
    tc = TestCase(
        id="TC-STORE",
        steps=[
            Step(action="producer", store_result="zone1"),
            Step(action="consumer"),
        ],
    )
    result = await runner.run_case(tc)
    assert result.passed
    # The primary value (device_id) was stashed under 'zone1'.
    assert seen["zone1"] == "ABCD1234"
    # Each output key was also flattened with the store_result prefix.
    assert seen["zone1_device_id"] == "ABCD1234"
    assert seen["zone1_extra"] == 7


async def test_expect_interpolation_resolves_against_prior_outputs() -> None:
    """``expect:`` values may reference state via ``{{ name }}``.

    Mirrors mash-go: ``params`` are passed to the handler verbatim
    (handlers interpolate when they need to), but ``expect`` values are
    interpolated by the engine before comparison.
    """
    runner = Runner()

    async def producer(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {"device_id": "DEVICE-X"}

    async def echo(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {"value": "DEVICE-X"}

    runner.register("producer", producer)
    runner.register("echo", echo)
    tc = TestCase(
        id="TC-INT",
        steps=[
            Step(action="producer"),
            Step(action="echo", expect={"value": "{{ device_id }}"}),
        ],
    )
    result = await runner.run_case(tc)
    assert result.passed, result.error


async def test_template_interpolation_in_outputs_lookup() -> None:
    """Interpolation resolves ``{{ key }}`` to typed values from state.outputs."""
    state = ExecutionState()
    state.set("device_id", "ABCD1234")
    state.set("count", 7)
    assert interpolate("{{ device_id }}", state) == "ABCD1234"
    assert interpolate("{{ count }}", state) == 7
    assert interpolate("prefix-{{ device_id }}-suffix", state) == "prefix-ABCD1234-suffix"
    # Missing template stays verbatim.
    assert interpolate("{{ unknown }}", state) == "{{ unknown }}"


async def test_pics_skip_when_requirements_unmet() -> None:
    pics = parse_pics(b"items:\n  MASH.S.OTHER: true\n")
    runner = Runner(RunnerConfig(pics=pics))

    async def x(step: Step, state: ExecutionState) -> dict[str, Any]:
        return {}

    runner.register("x", x)
    tc = TestCase(
        id="TC-PICS",
        pics_requirements=["MASH.S.MISSING"],
        steps=[Step(action="x")],
    )
    result = await runner.run_case(tc)
    assert result.skipped
    assert "PICS" in result.skip_reason


async def test_step_timeout_marks_failure() -> None:
    runner = Runner()

    async def sleepy(step: Step, state: ExecutionState) -> dict[str, Any]:
        await asyncio.sleep(5)
        return {}

    runner.register("sleepy", sleepy)
    tc = TestCase(
        id="TC-TIMEOUT",
        steps=[Step(action="sleepy", timeout="500ms")],
    )
    result = await runner.run_case(tc)
    assert not result.passed
    assert "timed out" in (result.steps[0].error or result.error)


async def test_explicit_skip_flag() -> None:
    runner = Runner()
    tc = TestCase(
        id="TC-SKIP",
        skip=True,
        skip_reason="not relevant",
        steps=[Step(action="any")],
    )
    result = await runner.run_case(tc)
    assert result.skipped
    assert result.skip_reason == "not relevant"
