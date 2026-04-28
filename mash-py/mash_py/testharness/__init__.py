"""Phase 21: Python test harness — minimum useful subset.

Implements the YAML test-case schema and a runner subset compatible with
``mash-go/internal/testharness/`` so Python can drive the same conformance
cases against a live Go device. See ``mash-py-tdd-plan.md`` Phase 21.

Spec-vs-Go note: the YAML *schema* is shared mechanics (no spec coverage),
so we copy it from the Go loader byte-for-byte. Handler *behavior*
implements the spec — never the Go runner's quirks. Where a divergence
matters it is flagged inline and surfaced in the Phase 21 findings memory.
"""

from mash_py.testharness.loader import (
    Condition,
    LoadError,
    PICSFile,
    Step,
    TestCase,
    load_test_cases,
    load_test_directory,
    parse_test_cases,
)
from mash_py.testharness.pics import (
    check_pics_requirements,
    load_pics,
    parse_pics,
)
from mash_py.testharness.runner import (
    ExecutionState,
    Runner,
    RunnerConfig,
    StepResult,
    SuiteResult,
    TestResult,
)

__all__ = [
    "Condition",
    "ExecutionState",
    "LoadError",
    "PICSFile",
    "Runner",
    "RunnerConfig",
    "Step",
    "StepResult",
    "SuiteResult",
    "TestCase",
    "TestResult",
    "check_pics_requirements",
    "load_pics",
    "load_test_cases",
    "load_test_directory",
    "parse_pics",
    "parse_test_cases",
]
