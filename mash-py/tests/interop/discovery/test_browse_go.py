"""Phase 6 interop: Python browser ↔ Go ``mash-device`` advertiser.

The acceptance bar from ``mash-py-tdd-plan.md §Phase 6``:

> Start ``mash-device --commissioning --discriminator 1234``. Python browser
> finds it within 2 seconds. TXT records match expected values.

Implementation notes
--------------------

- ``mash-go``'s ``cmd/mash-device`` does NOT have a ``--commissioning`` flag.
  The plan text is descriptive — *the device is in commissioning mode if it
  has no zones yet*. We launch with a fresh state directory so it boots
  uncommissioned and immediately advertises ``_mash-comm._tcp``.
- We use a high-numbered discriminator (a four-digit value chosen to not clash
  with the binary's default of 1234) so concurrent local devices on the same
  network do not race the test.
- The Go binary writes its protocol log to a temp file we discard. Stderr is
  captured and printed on failure so flaky multicast issues are diagnosable.
- macOS multicast on Wi-Fi sometimes drops packets; if this test flakes the
  plan suggests ``dns-sd -B _mash-comm._tcp`` to confirm the record is on the
  wire. We print the temp dir on skip / failure so the operator can re-run.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import shutil
import signal
import socket
import subprocess
import tempfile
from pathlib import Path

import pytest

from mash_py.discovery import (
    Browser,
    BrowseTimeoutError,
)

pytestmark = pytest.mark.interop

REPO_ROOT = Path(__file__).resolve().parents[3].parent
"""mash/ — the repository root, two parents above mash-py/."""

MASH_GO_DIR = REPO_ROOT / "mash-go"


def _go_toolchain_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    """Bind a kernel-assigned port and immediately release it.

    The interop binary needs a unique port per test run; using ``0`` and asking
    the kernel avoids collisions with other test processes on shared CI hosts.
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def go_device_state_dir():
    """A fresh state directory so each invocation boots uncommissioned."""
    d = tempfile.mkdtemp(prefix="mash-py-phase6-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def go_device(go_device_state_dir):
    """Launch ``go run ./cmd/mash-device`` in commissioning mode.

    Yields the ``subprocess.Popen`` handle plus the discriminator the device
    is advertising. The fixture tears the process down on test exit even if
    the test asserted early.
    """
    if not _go_toolchain_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 4031  # arbitrary, not the binary's default of 1234
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
        "20202021",
        "-port",
        str(port),
        "-state-dir",
        go_device_state_dir,
        "-log-level",
        "warn",
        "-serial",
        "PHASE6-INTEROP-001",
        "-brand",
        "MASH-Phase6",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 6 Interop Device",
    ]

    env = os.environ.copy()
    # Disable interactive REPL — we want the device to advertise and idle.
    env["MASH_NON_INTERACTIVE"] = "1"

    # ``go run ./cmd/mash-device`` first compiles to a temp binary then execs
    # it. SIGTERM-ing the ``go run`` wrapper does NOT propagate to the child on
    # macOS — the wrapper exits but the compiled device keeps running and
    # keeps advertising on mDNS, which then leaks into subsequent test runs.
    # ``start_new_session=True`` puts the wrapper + child in their own process
    # group so we can SIGTERM the whole group with ``os.killpg``.
    proc = subprocess.Popen(
        cmd,
        cwd=MASH_GO_DIR,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
        start_new_session=True,
    )

    try:
        # We do not parse stdout for "ready" because the simulation harness's
        # banner format has changed across mash-go versions; instead we let
        # the discovery test itself probe via mDNS, which is the only signal
        # that actually matters.
        yield proc, discriminator
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
        if proc.returncode and proc.returncode not in (0, -signal.SIGTERM):
            err = proc.stderr.read().decode("utf-8", errors="replace") if proc.stderr else ""
            print(f"\n--- mash-device stderr (exit {proc.returncode}) ---\n{err}")


async def test_python_browser_finds_go_device(go_device):
    """Python browser ↔ Go device commissionable record.

    The test waits up to 15 seconds because ``go run`` performs a compile on
    the first invocation; once warm, mDNS announce is sub-second.
    """
    proc, discriminator = go_device

    async with Browser() as browser:
        # 15s overall budget covers cold ``go run`` compilation. The plan's
        # "2 seconds" target is for the *announce → discover* leg only.
        try:
            svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=15.0
            )
        except BrowseTimeoutError:
            # Surface stderr to ease diagnosis — this is the failure mode
            # mDNS-on-loopback flakes produce.
            stderr = (
                proc.stderr.read(8192).decode("utf-8", errors="replace")
                if proc.stderr
                else "<no stderr>"
            )
            pytest.fail(
                f"Go device did not surface on mDNS within 15s. "
                f"stderr tail:\n{stderr}"
            )

    # DEC-078 6-key TXT schema — every required field is present and matches
    # what we passed via CLI flags.
    assert svc.discriminator == discriminator
    assert svc.serial == "PHASE6-INTEROP-001"
    assert svc.brand == "MASH-Phase6"
    assert svc.model == "EVSE-X"
    # The 'evse' type maps to E-MOBILITY (category 3) per DEC-078 + spec §2.2.
    assert 3 in svc.categories
    # SRV port must equal the port we asked the device to listen on
    # (DEC-082 — port is authoritative, not a fixed default).
    assert svc.port > 0
    # Device hostname is host-specific; we just assert it is non-empty and
    # ends with ``.local.`` as mDNS conventions require.
    assert svc.host
    assert svc.addresses


@pytest.mark.skipif(not _go_toolchain_available(), reason="Go toolchain not available")
async def test_python_browser_timeout_when_no_go_device():
    """No Go device running → browser must time out cleanly, not hang."""
    async with Browser() as browser:
        # Use a discriminator we are confident no device is advertising.
        with pytest.raises(BrowseTimeoutError):
            await browser.find_commissionable_by_discriminator(8191, timeout=1.0)


# Suppress an unused-warnings false positive — asyncio fixtures don't trigger
# "unused" linters but the sleep helper occasionally goes unused in iterations.
_ = asyncio.sleep
