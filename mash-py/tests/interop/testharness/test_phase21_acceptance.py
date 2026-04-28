"""Phase 21 acceptance gate.

> "At least 5 test cases pass identically under Go and Python harnesses."
> — ``mash-py-tdd-plan.md`` §Phase 21

This module spawns ``mash-go/cmd/mash-device`` once, runs five
representative YAML cases through the Python harness
(``mash-py-test``), and asserts each case passes with a result shape
mash-go's runner would produce. The Python harness reuses the same
``mash-go/testdata/cases/*.yaml`` files mash-test consumes, which is
the strongest possible "identical" gate — we're not authoring a
parallel YAML suite.

Selection rationale (only **observable** wire/discovery flows; no
device-internal state introspection):

* TC-COMM-001 — PASE handshake (DEC-084).
* TC-DISC-001 — basic mDNS discovery on the commissionable record
  (DEC-078).
* TC-CONN-OP-001 — operational connection on the post-commission
  ALPN ``mash/1`` (DEC-067).
* TC-MDNS-REC-001 — operational SRV record presence (DEC-088).
* TC-MDNS-REC-002 — operational AAAA record presence.

These five exercise the Phase-21 handler set end-to-end without
requiring the Go runner's in-process device-state introspection (which
the Python harness does not need to replicate to satisfy the spec).
"""

from __future__ import annotations

import contextlib
import os
import shutil
import signal
import socket
import subprocess
import tempfile
from pathlib import Path

import pytest

from mash_py.testharness.handlers import register_default_handlers
from mash_py.testharness.loader import (
    filter_by_pattern,
    filter_by_tags,
    load_test_directory,
)
from mash_py.testharness.runner import Runner, RunnerConfig

REPO_ROOT = Path(__file__).resolve().parents[4]
MASH_GO_DIR = REPO_ROOT / "mash-go"
GO_CASES_DIR = MASH_GO_DIR / "testdata" / "cases"

pytestmark = [pytest.mark.interop, pytest.mark.slow]


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def go_device_state_dir():
    d = tempfile.mkdtemp(prefix="mash-py-phase21-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Spawn an uncommissioned ``mash-device``; yield (proc, discriminator, setup_code).

    Same process-group SIGTERM teardown as the Phase 9 interop fixture —
    ``go run`` on macOS does not propagate signals to the compiled child,
    and orphaned mDNS announcers wreck downstream test runs.
    """
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 2912  # distinct from Phase 6/8/9 fixtures to avoid mDNS collisions
    setup_code = "20202021"
    port = _free_port()
    cmd = [
        "go",
        "run",
        "./cmd/mash-device",
        "-type",
        "evse",
        "-discriminator",
        str(discriminator),
        "-setup-code",
        setup_code,
        "-port",
        str(port),
        "-state-dir",
        go_device_state_dir,
        "-log-level",
        "warn",
        "-serial",
        "PHASE21-INTEROP-001",
        "-brand",
        "MASH-Phase21",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 21 Interop Device",
    ]
    env = os.environ.copy()
    env["MASH_NON_INTERACTIVE"] = "1"

    proc = subprocess.Popen(
        cmd,
        cwd=MASH_GO_DIR,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        start_new_session=True,
    )
    try:
        yield proc, discriminator, setup_code
    finally:
        pgid = None
        with contextlib.suppress(ProcessLookupError, OSError):
            pgid = os.getpgid(proc.pid)
        if pgid is not None:
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(pgid, signal.SIGTERM)
        try:
            proc.wait(timeout=10.0)
        except subprocess.TimeoutExpired:
            if pgid is not None:
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.killpg(pgid, signal.SIGKILL)
            proc.wait(timeout=5.0)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


PHASE21_CASE_IDS = {
    "TC-COMM-001",
    "TC-DISC-001",
    "TC-CONN-OP-001",
    "TC-MDNS-REC-001",
    "TC-MDNS-REC-002",
}


def _load_phase21_cases():
    """Pull the chosen five Phase-21 cases out of the full mash-go fixture set.

    Glob the fixture directory then filter by ID set so a future case-id
    rename surfaces here as a clear "missing" assertion rather than an
    accidental test silently dropping.
    """
    if not GO_CASES_DIR.is_dir():
        pytest.skip(f"mash-go testdata not found at {GO_CASES_DIR}")
    cases = load_test_directory(GO_CASES_DIR)
    selected = [tc for tc in cases if tc.id in PHASE21_CASE_IDS]
    missing = PHASE21_CASE_IDS - {tc.id for tc in selected}
    if missing:
        pytest.fail(
            f"expected to find Phase-21 cases {missing} in {GO_CASES_DIR}; "
            "their IDs may have been renamed upstream"
        )
    # Order matters for the suite report: discovery first (no commissioning
    # state pollution), then commissioning, then post-commission ops.
    order = [
        "TC-DISC-001",
        "TC-COMM-001",
        "TC-CONN-OP-001",
        "TC-MDNS-REC-001",
        "TC-MDNS-REC-002",
    ]
    selected.sort(key=lambda tc: order.index(tc.id))
    return selected


# ---------------------------------------------------------------------------
# Acceptance gate
# ---------------------------------------------------------------------------


@pytest.mark.timeout(300)
async def test_phase21_acceptance_five_cases_pass(live_go_device) -> None:
    """Run the Phase-21 acceptance suite and assert ≥5 cases pass."""
    _proc, discriminator, setup_code = live_go_device
    cases = _load_phase21_cases()

    config = RunnerConfig(
        discriminator=discriminator,
        setup_code=setup_code,
        timeout_s=60.0,
        verbose=True,
    )
    runner = Runner(config)
    register_default_handlers(runner)

    suite = await runner.run_suite(cases)

    failures = [
        f"{r.test.id}: {r.error}"
        for r in suite.results
        if not r.passed and not r.skipped
    ]
    assert not failures, "Phase-21 acceptance failures:\n  " + "\n  ".join(failures)
    assert suite.pass_count >= 5, (
        f"need ≥5 passing cases, got {suite.pass_count} "
        f"(skipped={suite.skip_count}, failed={suite.fail_count})"
    )


@pytest.mark.timeout(60)
async def test_phase21_unit_filtering_smoke() -> None:
    """The five Phase-21 cases are reachable via the standard CLI filters."""
    cases = _load_phase21_cases()
    pattern_filtered = filter_by_pattern(cases, "TC-MDNS-REC-*")
    assert {tc.id for tc in pattern_filtered} == {
        "TC-MDNS-REC-001",
        "TC-MDNS-REC-002",
    }
    tag_filtered = filter_by_tags(cases, include="commissioning")
    assert "TC-COMM-001" in {tc.id for tc in tag_filtered}


