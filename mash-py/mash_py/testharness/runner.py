"""Test harness runner — minimum useful subset of mash-go's runner.

What this module provides:

* :class:`ExecutionState` — per-test mutable state shared across handlers.
* :class:`ActionHandler` protocol — async callable signature.
* :class:`Runner` — owns handler registry + execution state + reporting.
* :class:`RunnerConfig` — CLI-driven configuration.

What it deliberately does NOT provide (out of Phase-21 scope, see
``mash-py-tdd-plan.md`` Phase 21 "minimum useful subset"):

* The Go runner's strict-cleanup invariants, lifecycle controller,
  connection-pool/coordinator pair, suite-zone preservation, and PASE
  attempt-budget bookkeeping. These all serve mass-conformance regression
  needs that Phase 21 acceptance does not require ("at least 5 cases
  pass identically").
* PICS auto-discovery: only static ``-pics`` files are honored.
* Shuffle / parallelism / retry / strict-lifecycle reporting.

The handler signature mirrors Go's ``ActionHandler``: it returns an
``outputs`` dict whose keys are then matched against the step's
``expect`` map. Where a handler needs to surface side-state for later
steps (e.g. the operational cert from ``commission``), it stashes it on
``ExecutionState.custom`` under a documented key.
"""

from __future__ import annotations

import asyncio
import dataclasses
import re
import sys
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, TextIO

from mash_py.testharness.loader import (
    PICSFile,
    Step,
    TestCase,
)
from mash_py.testharness.pics import check_pics_requirements

# ---------------------------------------------------------------------------
# Types
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class RunnerConfig:
    """Runner configuration (mirrors a small subset of ``runner.Config``)."""

    target: str = ""
    """``host:port`` of the device under test (mDNS-resolved if empty)."""

    discriminator: int = 3072
    """Default discriminator for ``commission``/``browse`` actions."""

    setup_code: str = "20202021"
    """Default PASE setup code."""

    setup_code_alt: str = ""
    """Alternative setup code for negative tests (rare)."""

    pics: PICSFile | None = None

    timeout_s: float = 30.0
    """Default per-test timeout if the YAML has no ``timeout``."""

    step_timeout_s: float = 10.0

    verbose: bool = False
    output: TextIO = field(default_factory=lambda: sys.stdout)

    # Cached commissioning artefact directory (used by handlers).
    state: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class ExecutionState:
    """Per-test-case mutable state.

    ``outputs`` is the map of values produced by past steps — both the
    handler's own ``outputs`` dict AND any ``store_result`` flattening.
    Handlers consult ``outputs`` (via :meth:`get`) and add to it (via
    :meth:`set`); the runner adds an entry per step automatically.

    ``custom`` is a free-form bag for cross-handler state — the Phase-21
    handlers stash the live commissioning result there under the key
    ``commissioning_result`` so subsequent steps (``connect_operational``,
    ``read``) can reuse the controller cert + zone CA without
    re-commissioning.
    """

    outputs: dict[str, Any] = field(default_factory=dict)
    custom: dict[str, Any] = field(default_factory=dict)
    config: RunnerConfig | None = None

    def get(self, key: str, default: Any = None) -> Any:
        return self.outputs.get(key, default)

    def set(self, key: str, value: Any) -> None:
        self.outputs[key] = value

    def update(self, mapping: dict[str, Any]) -> None:
        self.outputs.update(mapping)


ActionHandler = Callable[[Step, ExecutionState], Awaitable[dict[str, Any]]]
"""Async handler signature. Receives the step and shared state, returns
``outputs`` dict whose keys are matched against ``step.expect``."""


@dataclass(slots=True)
class ExpectResult:
    key: str
    expected: Any
    actual: Any
    passed: bool
    message: str = ""


@dataclass(slots=True)
class StepResult:
    step: Step
    index: int
    passed: bool = False
    error: str = ""
    duration_s: float = 0.0
    expects: dict[str, ExpectResult] = field(default_factory=dict)
    outputs: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class TestResult:
    __test__ = False

    test: TestCase
    passed: bool = False
    skipped: bool = False
    skip_reason: str = ""
    error: str = ""
    duration_s: float = 0.0
    steps: list[StepResult] = field(default_factory=list)


@dataclass(slots=True)
class SuiteResult:
    name: str = "Test Suite"
    results: list[TestResult] = field(default_factory=list)
    pass_count: int = 0
    fail_count: int = 0
    skip_count: int = 0
    duration_s: float = 0.0


# ---------------------------------------------------------------------------
# Interpolation
# ---------------------------------------------------------------------------

# Matches ``{{ name }}`` with optional whitespace. Spec-vs-Go: identical
# to mash-go's engine.InterpolateParams syntax — the YAML files reference
# templates this way, so we must accept the exact form.
_TEMPLATE = re.compile(r"\{\{\s*([^}\s]+)\s*\}\}")


def interpolate(value: Any, state: ExecutionState) -> Any:
    """Recursively expand ``{{ name }}`` templates against ``state``.

    A whole-string template (``"{{ device_id }}"``) returns the original
    typed value if found; partial templates (``"prefix-{{ x }}"``) coerce
    to strings. Mirrors Go's ``engine.InterpolateParams`` semantics.
    """
    if isinstance(value, str):
        m = _TEMPLATE.fullmatch(value.strip())
        if m:
            name = m.group(1)
            if name in state.outputs:
                return state.outputs[name]
            # Unresolved -> return as-is so the caller sees it verbatim
            # (matches Go's behavior for missing template references).
            return value
        return _TEMPLATE.sub(
            lambda match: str(state.outputs.get(match.group(1), match.group(0))),
            value,
        )
    if isinstance(value, dict):
        return {k: interpolate(v, state) for k, v in value.items()}
    if isinstance(value, list):
        return [interpolate(v, state) for v in value]
    return value


# ---------------------------------------------------------------------------
# Default expectation checker
# ---------------------------------------------------------------------------


def default_checker(key: str, expected: Any, state: ExecutionState) -> ExpectResult:
    """Default ``expect`` evaluator (mirrors ``engine.defaultChecker``).

    Looks up ``key`` in ``state.outputs`` and compares against ``expected``
    via stringified equality (matches Go behavior; avoids YAML/CBOR
    int-vs-float surprises).
    """
    actual = state.outputs.get(key, _MISSING)
    if actual is _MISSING:
        return ExpectResult(
            key=key,
            expected=expected,
            actual=None,
            passed=False,
            message=f"key {key!r} not found in outputs",
        )
    if isinstance(expected, str) and expected == "present":
        return ExpectResult(key, expected, actual, True, f"{key} = {actual!r}")
    passed = str(expected) == str(actual)
    msg = (
        f"{key} = {expected!r}"
        if passed
        else f"expected {expected!r}, got {actual!r}"
    )
    return ExpectResult(key, expected, actual, passed, msg)


_MISSING = object()


def _save_field_checker(
    field_key: str,
) -> Callable[[str, Any, ExecutionState], "ExpectResult"]:
    """Build a ``save_*`` checker that mirrors mash-go's ``checkerSaveField``.

    YAML usage::

        expect:
          save_subscription_id: sub_id    # saves outputs["subscription_id"] -> state["sub_id"]

    The checker reads ``state.outputs[field_key]`` (set by the just-run
    handler) and stores it under the operator-supplied target key so
    later steps can interpolate ``{{ sub_id }}``.
    """

    def _checker(key: str, expected: Any, state: ExecutionState) -> ExpectResult:
        if not isinstance(expected, str):
            return ExpectResult(
                key, expected, None, False,
                f"{key} target must be a string, got {type(expected).__name__}",
            )
        val = state.outputs.get(field_key, _MISSING)
        if val is _MISSING or val is None:
            return ExpectResult(
                key, expected, None, False, f"no {field_key} in step output",
            )
        state.set(expected, val)
        return ExpectResult(key, expected, val, True, f"saved {field_key} as {expected!r}")

    return _checker


def _save_priming_value_checker(
    key: str, expected: Any, state: ExecutionState,
) -> ExpectResult:
    """Save the most recent subscribe priming report under the named state key.

    Mirrors ``CheckerSavePrimingValue`` in
    mash-go/internal/testharness/engine/checkers.go. The priming data is
    stashed in ``state.custom["_priming_data"]`` by ``handle_subscribe``.
    """
    if not isinstance(expected, str):
        return ExpectResult(
            key, expected, None, False,
            f"save_priming_value target must be a string, got {type(expected).__name__}",
        )
    priming = state.custom.get("_priming_data")
    if priming is None:
        return ExpectResult(
            key, expected, None, False, "no priming data available from subscribe",
        )
    state.set(expected, priming)
    return ExpectResult(key, expected, priming, True, f"saved priming data as {expected!r}")


def _priming_value_different_from_checker(
    key: str, expected: Any, state: ExecutionState,
) -> ExpectResult:
    """Assert the current priming differs from a value previously saved.

    YAML::

        expect:
          priming_value_different_from: initial_measurement
    """
    if not isinstance(expected, str):
        return ExpectResult(
            key, expected, None, False,
            f"priming_value_different_from target must be a string, got {type(expected).__name__}",
        )
    current = state.custom.get("_priming_data")
    saved = state.outputs.get(expected, _MISSING)
    if saved is _MISSING:
        return ExpectResult(
            key, expected, current, False,
            f"no saved priming value under {expected!r}",
        )
    if current is None:
        return ExpectResult(
            key, expected, current, False, "no current priming data available",
        )
    different = current != saved
    msg = (
        f"priming differs from {expected!r}"
        if different
        else f"priming unchanged from {expected!r}: {current!r}"
    )
    return ExpectResult(key, expected, current, different, msg)


def _gte_checker(key: str, expected: Any, state: ExecutionState) -> ExpectResult:
    """``actual >= expected`` checker for ``*_min`` style keys.

    Mirrors mash-go's ``checkAAAACountMin`` (``runner/discovery_handlers.go``):
    "min" reads as "must be at least", which is a >= comparison, not
    equality. The default checker would fail on ``aaaa_count_min: 1``
    when the device advertises 18 AAAAs — exactly the situation
    Phase 21 hit during the first interop run.
    """
    actual = state.outputs.get(key, _MISSING)
    if actual is _MISSING:
        return ExpectResult(key, expected, None, False, f"key {key!r} not found")
    try:
        actual_n = float(actual)
        expected_n = float(expected)
    except (TypeError, ValueError):
        return ExpectResult(
            key,
            expected,
            actual,
            False,
            f"non-numeric values: actual={actual!r}, expected={expected!r}",
        )
    passed = actual_n >= expected_n
    msg = (
        f"{key} >= {expected} (got {actual})"
        if passed
        else f"expected {key} >= {expected}, got {actual}"
    )
    return ExpectResult(key, expected, actual, passed, msg)


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------


class Runner:
    """Owns handler registry, runs cases, prints a small text report.

    The runner is single-use per ``RunnerConfig`` — create a fresh
    instance per CLI invocation. It is *not* safe to share across event
    loops because handlers may stash asyncio primitives in
    ``ExecutionState.custom``.
    """

    def __init__(self, config: RunnerConfig | None = None) -> None:
        self.config = config or RunnerConfig()
        self._handlers: dict[str, ActionHandler] = {}
        self._checkers: dict[
            str, Callable[[str, Any, ExecutionState], "ExpectResult"]
        ] = {}
        self._precondition_setup: (
            Callable[[TestCase, ExecutionState], Awaitable[None]] | None
        ) = None
        # Built-in checkers — keys whose semantic differs from default
        # equality. Mirrors mash-go's RegisterEnhancedCheckers subset.
        self.register_checker("aaaa_count_min", _gte_checker)
        self.register_checker("addresses_tried_min", _gte_checker)
        self.register_checker("retries_performed_min", _gte_checker)
        self.register_checker("interface_count_min", _gte_checker)
        self.register_checker("save_subscription_id", _save_field_checker("subscription_id"))
        self.register_checker("save_delay_as", _save_field_checker("connect_duration_ms"))
        self.register_checker("save_priming_value", _save_priming_value_checker)
        self.register_checker("priming_value_different_from", _priming_value_different_from_checker)

    # ------------------------------------------------------------------
    # Registration
    # ------------------------------------------------------------------

    def register(self, action: str, handler: ActionHandler) -> None:
        self._handlers[action] = handler

    def register_checker(
        self,
        key: str,
        checker: Callable[[str, Any, ExecutionState], "ExpectResult"],
    ) -> None:
        """Override the default equality-checker for a specific output key."""
        self._checkers[key] = checker

    def set_precondition_setup(
        self,
        fn: Callable[[TestCase, ExecutionState], Awaitable[None]],
    ) -> None:
        self._precondition_setup = fn

    # ------------------------------------------------------------------
    # Execution
    # ------------------------------------------------------------------

    async def run_case(self, tc: TestCase) -> TestResult:
        result = TestResult(test=tc)
        if tc.skip:
            result.skipped = True
            result.skip_reason = tc.skip_reason or "skipped by test definition"
            return result

        if self.config.pics is not None and not check_pics_requirements(
            self.config.pics, tc.pics_requirements
        ):
            result.skipped = True
            result.skip_reason = "PICS requirements not met"
            return result

        timeout_s = _parse_duration(tc.timeout) or self.config.timeout_s
        state = ExecutionState(config=self.config)

        start = time.monotonic()
        try:
            await asyncio.wait_for(self._run_case_inner(tc, state, result), timeout=timeout_s)
        except asyncio.TimeoutError:
            result.passed = False
            result.error = f"test timed out after {timeout_s:.1f}s"
        finally:
            result.duration_s = time.monotonic() - start

        if not result.steps:
            result.passed = False
            result.error = result.error or "no steps executed"
        else:
            result.passed = (
                not result.error and all(sr.passed for sr in result.steps)
            )

        # Best-effort teardown — handlers register a cleanup callback list.
        await self._run_teardown(state)
        return result

    async def run_suite(self, cases: list[TestCase]) -> SuiteResult:
        suite = SuiteResult()
        start = time.monotonic()
        for tc in cases:
            tr = await self.run_case(tc)
            suite.results.append(tr)
            if tr.skipped:
                suite.skip_count += 1
            elif tr.passed:
                suite.pass_count += 1
            else:
                suite.fail_count += 1
            self._report_test(tr)
        suite.duration_s = time.monotonic() - start
        self._report_suite(suite)
        return suite

    # ------------------------------------------------------------------
    # Internals
    # ------------------------------------------------------------------

    async def _run_case_inner(
        self,
        tc: TestCase,
        state: ExecutionState,
        result: TestResult,
    ) -> None:
        if self._precondition_setup is not None:
            try:
                await self._precondition_setup(tc, state)
            except Exception as exc:  # noqa: BLE001 — surface to result
                result.error = f"precondition failed: {exc}"
                return

        for i, step in enumerate(tc.steps):
            sr = await self._run_step(step, i, state)
            result.steps.append(sr)
            if not sr.passed:
                result.error = sr.error or "step failed"
                return

    async def _run_step(self, step: Step, index: int, state: ExecutionState) -> StepResult:
        sr = StepResult(step=step, index=index)
        handler = self._handlers.get(step.action)
        if handler is None:
            sr.passed = False
            sr.error = f"unknown action: {step.action}"
            return sr

        timeout_s = _parse_duration(step.timeout) or self.config.step_timeout_s
        # Guard against handlers that intentionally sleep longer than
        # the default step budget (mirrors Go's per-step stretch logic).
        wait_s = _wait_duration_from_params(step.params)
        if wait_s and wait_s + 5.0 > timeout_s:
            timeout_s = wait_s + 5.0

        start = time.monotonic()
        try:
            outputs = await asyncio.wait_for(handler(step, state), timeout=timeout_s)
        except asyncio.TimeoutError:
            sr.passed = False
            sr.error = f"step '{step.action}' timed out after {timeout_s:.1f}s"
            sr.duration_s = time.monotonic() - start
            return sr
        except Exception as exc:  # noqa: BLE001
            sr.passed = False
            sr.error = f"{type(exc).__name__}: {exc}"
            sr.duration_s = time.monotonic() - start
            return sr

        if not isinstance(outputs, dict):
            outputs = {}
        sr.outputs = dict(outputs)

        # Merge handler outputs into state for later interpolation.
        for k, v in outputs.items():
            state.set(k, v)

        # store_result: persist the handler's primary value AND flatten
        # all output keys under the given prefix (matches Go's engine).
        if step.store_result:
            primary: Any = outputs
            for primary_key in ("device_id", "zone_id", "value", "result"):
                if primary_key in outputs:
                    primary = outputs[primary_key]
                    break
            state.set(step.store_result, primary)
            for k, v in outputs.items():
                state.set(f"{step.store_result}_{k}", v)

        # Evaluate expectations.
        sr.passed = True
        for key, raw_expected in step.expect.items():
            expected = interpolate(raw_expected, state)
            checker = self._checkers.get(key, default_checker)
            er = checker(key, expected, state)
            sr.expects[key] = er
            if not er.passed:
                sr.passed = False
                sr.error = f"expectation failed: {key} — {er.message}"

        sr.duration_s = time.monotonic() - start
        return sr

    async def _run_teardown(self, state: ExecutionState) -> None:
        teardowns: list[Callable[[], Awaitable[None]]] = state.custom.get(
            "_teardowns", []
        )
        for fn in reversed(teardowns):
            try:
                await fn()
            except Exception:  # noqa: BLE001
                # Teardown is best-effort; never let it break a passing test.
                pass

    # ------------------------------------------------------------------
    # Reporting
    # ------------------------------------------------------------------

    def _report_test(self, result: TestResult) -> None:
        out = self.config.output
        if result.skipped:
            print(f"  SKIP  {result.test.id}  ({result.skip_reason})", file=out)
            return
        status = "PASS" if result.passed else "FAIL"
        print(
            f"  {status}  {result.test.id}  ({result.duration_s * 1000:.0f}ms)",
            file=out,
        )
        if not result.passed:
            if result.error:
                print(f"        error: {result.error}", file=out)
            for sr in result.steps:
                if sr.passed:
                    continue
                print(f"        step[{sr.index}] {sr.step.action}: {sr.error}", file=out)
                for er in sr.expects.values():
                    if not er.passed:
                        print(f"          expect {er.key}: {er.message}", file=out)
        elif self.config.verbose:
            for sr in result.steps:
                print(f"        step[{sr.index}] {sr.step.action}: ok ({sr.duration_s * 1000:.0f}ms)", file=out)

    def _report_suite(self, suite: SuiteResult) -> None:
        out = self.config.output
        print("", file=out)
        print(
            f"Total: {len(suite.results)}  pass={suite.pass_count}  "
            f"fail={suite.fail_count}  skip={suite.skip_count}  "
            f"({suite.duration_s:.1f}s)",
            file=out,
        )


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _parse_duration(s: str) -> float:
    """Best-effort Go-style duration parser ("30s", "500ms", "1m")."""
    if not s:
        return 0.0
    s = s.strip()
    units = (
        ("ms", 1e-3),
        ("us", 1e-6),
        ("µs", 1e-6),
        ("ns", 1e-9),
        ("s", 1.0),
        ("m", 60.0),
        ("h", 3600.0),
    )
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


def _wait_duration_from_params(params: dict[str, Any]) -> float:
    """Stretch step timeout when the params say 'sleep this long'."""
    out = 0.0
    if "duration_seconds" in params:
        try:
            out = max(out, float(params["duration_seconds"]))
        except (TypeError, ValueError):
            pass
    if "duration_ms" in params:
        try:
            out = max(out, float(params["duration_ms"]) / 1000.0)
        except (TypeError, ValueError):
            pass
    if isinstance(params.get("duration"), str):
        out = max(out, _parse_duration(params["duration"]))
    return out


__all__ = [
    "ActionHandler",
    "ExecutionState",
    "ExpectResult",
    "Runner",
    "RunnerConfig",
    "StepResult",
    "SuiteResult",
    "TestResult",
    "default_checker",
    "interpolate",
]
# dataclasses re-export — keeps __all__ tooling-friendly.
_ = dataclasses
