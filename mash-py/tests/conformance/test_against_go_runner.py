"""Phase 22 conformance gate.

> "Go's ``mash-test --target mdns:MASH-1234 --srv-resolve`` passes the target
>  subset against a Python device deployed in two-port mode."
> — ``mash-py-tdd-plan.md`` §Phase 22

This module spawns ``mash-py-device`` (the new Phase 22 CLI), waits for
its commissioning mDNS record, then drives ``mash-go/cmd/mash-test`` against
it with ``--srv-resolve`` enabled (DEC-082). The harness validates that
the Phase 22 target subset of the Go conformance corpus passes against
a Python device.

Subset selection (``--files`` glob, see Phase 22 of the TDD plan):
    commissioning-*, protocol-*, connection-*, read-*, subscription-*,
    bidirectional-read, bidirectional-subscribe, feature-interaction-*,
    message-*

This is the closest-existing-files match to the plan's "commissioning-*,
base-protocol-*, interaction-*" wording (the latter two prefixes do not
exist in the corpus; the plan author probably meant tag-driven filtering
but the chosen files cover the same ground without depending on tagging
that has drifted).

Excluded tags: ``env:multi-device`` (multi-device orchestration; Phase 22
explicitly excludes), ``slow``.

Override the subset by passing ``--phase22-files=<glob>`` /
``--phase22-exclude-tags=<csv>`` via pytest -- handy when bisecting
which case in the corpus is failing without re-editing this file.
"""

from __future__ import annotations

import contextlib
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]
MASH_PY_ROOT = REPO_ROOT / "mash-py"
MASH_GO_DIR = REPO_ROOT / "mash-go"

DEFAULT_FILES = (
    "commissioning-*,protocol-*,connection-*,read-*,subscription-*,"
    "bidirectional-read,bidirectional-subscribe,feature-interaction-*,"
    "message-*"
)
DEFAULT_EXCLUDE_TAGS = "env:multi-device,slow"

pytestmark = [pytest.mark.conformance, pytest.mark.slow]


# ---------------------------------------------------------------------------
# pytest plumbing
# ---------------------------------------------------------------------------


def pytest_addoption(parser: pytest.Parser) -> None:  # pragma: no cover
    """Hook for callers (Makefiles, dev shells) to override the subset.

    Defined here -- not in ``conftest.py`` -- because this is the only
    file that uses it. Pytest collects ``pytest_addoption`` from any
    test module it loads.
    """
    group = parser.getgroup("phase22")
    group.addoption(
        "--phase22-files",
        action="store",
        default=None,
        help="Override the file glob for the conformance subset.",
    )
    group.addoption(
        "--phase22-exclude-tags",
        action="store",
        default=None,
        help="Override the excluded tags (comma-separated).",
    )
    group.addoption(
        "--phase22-filter",
        action="store",
        default=None,
        help="Pass-through to mash-test --filter (test-id glob).",
    )


# ---------------------------------------------------------------------------
# Device fixture
# ---------------------------------------------------------------------------


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _pick_discriminator() -> int:
    # 0..4095 inclusive; offset by PID so concurrent dev runs don't clash.
    return (os.getpid() * 7919) % 4096


@pytest.fixture
def python_device(tmp_path: Path):
    """Spawn ``mash-py-device`` in two-port mode and yield discriminator+setup.

    The device is started as a separate process (not in-thread asyncio)
    because mash-go's mash-test fork-execs ``go run`` for the runner,
    and asyncio in the test process competing with subprocess streams
    has bitten earlier interop tests. ``start_new_session=True`` keeps
    SIGTERM clean across the fork.
    """
    if not _go_available():
        pytest.skip("Go toolchain unavailable; cannot drive mash-test.")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = _pick_discriminator()
    setup_code = "20202021"
    op_port = _free_port()
    comm_port = _free_port()
    state_dir = tmp_path / "state"

    cmd = [
        sys.executable,
        "-m",
        "mash_py.cli.device",
        "--discriminator",
        str(discriminator),
        "--setup-code",
        setup_code,
        "--port",
        str(op_port),
        "--commissioning-port",
        str(comm_port),
        "--state-dir",
        str(state_dir),
        "--reset",
        "--log-level",
        "info",
        "--zone-name",
        "phase22-test",
    ]
    proc = subprocess.Popen(
        cmd,
        cwd=str(MASH_PY_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        start_new_session=True,
    )
    try:
        # Wait for the "advertised _mash-comm._tcp" log line so callers
        # don't race the listener bind. Bound the wait so a misbehaving
        # device doesn't hang the suite.
        deadline = time.time() + 30.0
        ready = False
        log_buffer: list[str] = []
        assert proc.stdout is not None
        while time.time() < deadline:
            line = proc.stdout.readline()
            if not line:
                if proc.poll() is not None:
                    break
                time.sleep(0.05)
                continue
            log_buffer.append(line)
            if "advertised _mash-comm._tcp" in line:
                ready = True
                break
        if not ready:
            log_text = "".join(log_buffer)
            proc.terminate()
            with contextlib.suppress(subprocess.TimeoutExpired):
                proc.wait(timeout=5)
            pytest.fail(
                f"mash-py-device failed to advertise commissioning "
                f"within 30s. Log:\n{log_text}",
                pytrace=False,
            )

        yield {
            "discriminator": discriminator,
            "setup_code": setup_code,
            "op_port": op_port,
            "comm_port": comm_port,
            "state_dir": state_dir,
            "log_buffer": log_buffer,
            "proc": proc,
        }
    finally:
        pgid = None
        with contextlib.suppress(ProcessLookupError, OSError):
            pgid = os.getpgid(proc.pid)
        if pgid is not None:
            with contextlib.suppress(ProcessLookupError, PermissionError):
                os.killpg(pgid, signal.SIGTERM)
        try:
            # Generous: graceful teardown sends mDNS goodbye for every
            # advertised record (see Advertiser.close). Cutting this short
            # leaves stale records on the LAN that other test runs see for
            # up to TTL seconds. Normal shutdown completes well under 5 s.
            proc.wait(timeout=30.0)
        except subprocess.TimeoutExpired:
            if pgid is not None:
                with contextlib.suppress(ProcessLookupError, PermissionError):
                    os.killpg(pgid, signal.SIGKILL)
            with contextlib.suppress(subprocess.TimeoutExpired):
                proc.wait(timeout=5.0)


# ---------------------------------------------------------------------------
# Conformance gate
# ---------------------------------------------------------------------------


_PASS_RE = re.compile(r"PASS\s*[:|]\s*(\d+)")
_FAIL_RE = re.compile(r"FAIL\s*[:|]\s*(\d+)")
_SKIP_RE = re.compile(r"SKIP\s*[:|]\s*(\d+)")


def _parse_summary(stdout: str) -> dict[str, int]:
    """Best-effort parse of mash-test's textual summary.

    mash-test currently emits a final line like ``PASS:N FAIL:M SKIP:K``
    in text mode. We don't depend on JSON so the harness keeps working
    if the format gets a cosmetic change. If parsing fails, the test
    falls back to checking the process exit code -- mash-test exits 0
    iff every test passes.
    """
    out = {"pass": 0, "fail": 0, "skip": 0}
    for key, regex in (("pass", _PASS_RE), ("fail", _FAIL_RE), ("skip", _SKIP_RE)):
        m = regex.search(stdout)
        if m:
            out[key] = int(m.group(1))
    return out


@pytest.mark.timeout(900)
def test_phase22_conformance_subset_passes(python_device, request):
    """Drive ``mash-test --srv-resolve`` against ``mash-py-device``.

    Acceptance: zero failed tests in the configured subset. Skips and
    PICS-driven not-applicables are fine; the gate is failures.
    """
    discriminator = python_device["discriminator"]
    setup_code = python_device["setup_code"]

    files = request.config.getoption("--phase22-files") or DEFAULT_FILES
    exclude_tags = (
        request.config.getoption("--phase22-exclude-tags") or DEFAULT_EXCLUDE_TAGS
    )
    case_filter = request.config.getoption("--phase22-filter") or ""

    target = f"127.0.0.1:{python_device['op_port']}"
    cmd = [
        "go",
        "run",
        "./cmd/mash-test",
        "-target",
        target,
        "-srv-resolve",
        "-discriminator",
        str(discriminator),
        "-mode",
        "device",
        "-setup-code",
        setup_code,
        "-files",
        files,
        "-exclude-tags",
        exclude_tags,
        "-timeout",
        "30s",
    ]
    if case_filter:
        cmd += ["-filter", case_filter]

    env = os.environ.copy()
    env["MASH_PHASE22_DISCRIMINATOR"] = str(discriminator)

    result = subprocess.run(
        cmd,
        cwd=str(MASH_GO_DIR),
        capture_output=True,
        text=True,
        env=env,
        timeout=900,
    )
    summary = _parse_summary(result.stdout)

    if result.returncode != 0:
        pytest.fail(
            "mash-test reported failures against mash-py-device.\n"
            f"  exit: {result.returncode}\n"
            f"  pass={summary['pass']} fail={summary['fail']} skip={summary['skip']}\n"
            f"  files: {files}\n"
            f"  exclude-tags: {exclude_tags}\n"
            f"  filter: {case_filter or '<none>'}\n"
            f"---- stdout ----\n{result.stdout}\n"
            f"---- stderr ----\n{result.stderr}\n",
            pytrace=False,
        )
    # Spec-strict assertion in case mash-test ever returns 0 on no-tests-run.
    assert summary["pass"] >= 1, (
        "mash-test exited 0 but reported zero passing tests; "
        f"summary={summary}, files={files}"
    )
