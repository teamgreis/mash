"""Phase 7D + Phase 8 interop: SPAKE2+ with a live mash-go device.

Two test surfaces in this file:

1. **Phase 7D — algorithmic equivalence.** Python's
   :func:`generate_verifier` and mash-go's ``commissioning.GenerateVerifier``
   produce byte-identical ``(salt, w0, L)`` for the same QR-code inputs.
   Plus a frozen vector cross-check against the third-party SPAKE2+
   oracle. These run without needing the Go device to be live; only
   the algorithm surface is exercised.

2. **Phase 8 — live PASE handshake.** Spin up ``mash-go/cmd/mash-device``
   in commissioning mode, discover it via mDNS, dial the
   ``_mash-comm._tcp`` ALPN, drive ``PASEClient.handshake`` to
   completion, assert the derived ``K_main`` is 32 bytes. This is the
   load-bearing Phase 8 acceptance gate.

   Spec reference: ``docs/testing/behavior/commissioning-pase.md §4.1``
   (full sequence) and ``§4.2`` (wire format).

**Failure-path coverage.** mash-go was fixed in the same workstream that
filed Issue #6 — `pkg/commissioning/session.go` now sends
``PASEComplete{ErrCodeAuthFailed}`` on every in-PASE failure mode (wrong
identity, invalid pA, MAC mismatch), as the spec mandates. The
``test_live_pase_wrong_setup_code_raises_auth_failed`` test below
exercises the spec-conformant failure path against a live mash-go
device to keep the contract enforced from both sides.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import shutil
import signal
import socket
import subprocess
import tempfile
from pathlib import Path
from typing import Any

import pytest

from mash_py.commissioning import (
    PASEAuthFailedError,
    PASEClient,
    generate_verifier,
)
from mash_py.discovery import Browser, BrowseTimeoutError
from mash_py.transport.connection import _ssl_object  # internal helper, used for verify
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)

REPO_ROOT = Path(__file__).resolve().parents[4]
GO_HELPER_DIR = REPO_ROOT / "mash-py" / "tools" / "print_verifier"
MASH_GO_DIR = REPO_ROOT / "mash-go"


pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


def _run_go_helper(setup_code: str, discriminator: int) -> dict[str, Any]:
    """Invoke the Go helper and return its parsed JSON output.

    Uses ``go run`` against ``mash-py/tools/print_verifier`` so the
    helper picks up the local mash-go via the ``replace`` directive in
    ``mash-py/tools/go.mod``. PBKDF2 100k takes ~1-3 seconds — keep
    the timeout generous to avoid CI flakes on slower runners.
    """
    cmd = [
        "go",
        "run",
        "./print_verifier",
        "--setup-code",
        setup_code,
        "--discriminator",
        str(discriminator),
    ]
    result = subprocess.run(
        cmd,
        cwd=str(GO_HELPER_DIR.parent),
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )
    if result.returncode != 0:
        pytest.fail(
            "go helper failed:\n"
            f"  stdout: {result.stdout}\n"
            f"  stderr: {result.stderr}",
            pytrace=False,
        )
    parsed: dict[str, Any] = json.loads(result.stdout)
    return parsed


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.parametrize(
    ("setup_code", "discriminator"),
    [
        ("20202021", 3840),
        ("12345678", 1024),
    ],
)
def test_python_verifier_matches_go(setup_code: str, discriminator: int) -> None:
    """Cross-check: Python and Go produce byte-identical ``(salt, w0, L)``.

    The 100k PBKDF2 iterations make this run ~3-5 seconds; marked
    ``slow`` so a normal ``pytest`` invocation excludes it unless
    explicitly requested.
    """
    py_v = generate_verifier(setup_code, discriminator)
    go_v = _run_go_helper(setup_code, discriminator)

    assert int(go_v["discriminator"]) == discriminator
    assert go_v["salt_hex"] == py_v.salt.hex()
    assert go_v["w0_hex"] == py_v.w0.hex()
    assert go_v["l_hex"] == py_v.L.hex()
    assert int(go_v["iterations"]) == py_v.iterations


def test_static_verifier_matches_recorded_vector() -> None:
    """Frozen-ground-truth: ``full_handshake`` vector outputs (recorded
    from the third-party oracle, also matched by mash-go) MUST match
    Python's ``generate_verifier`` for the same inputs.

    This runs without the Go toolchain — useful for CI environments
    that ship Python only.
    """
    vector_path = REPO_ROOT / "mash-go" / "testdata" / "spake2plus_vectors.json"
    if not vector_path.exists():
        pytest.skip(f"vectors not present at {vector_path}")
    with vector_path.open(encoding="utf-8") as fh:
        doc = json.load(fh)
    full = next(v for v in doc["vectors"] if v["id"] == "full_handshake")
    inp = full["inputs"]
    out = full["outputs"]

    v = generate_verifier(inp["setup_code"], inp["discriminator"], iterations=inp["iterations"])
    assert v.salt.hex() == out["salt_hex"]
    assert v.w0.hex() == out["w0_hex"]
    assert v.L.hex() == out["L_hex"]


# ---------------------------------------------------------------------------
# Phase 8 — live PASE handshake against ``mash-go/cmd/mash-device``
# ---------------------------------------------------------------------------


def _free_port() -> int:
    """Bind a kernel-assigned port and immediately release it.

    Same pattern as ``tests/interop/discovery/test_browse_go.py`` —
    avoids collisions when multiple interop tests run on the same host.
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def go_device_state_dir():
    """Fresh state directory so ``mash-device`` boots uncommissioned."""
    d = tempfile.mkdtemp(prefix="mash-py-phase8-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Launch ``go run ./cmd/mash-device`` in commissioning mode.

    Yields ``(proc, discriminator, setup_code, port)``. The fixture tears
    the process down on exit even on assertion failure.

    Process-group SIGTERM is required because ``go run`` on macOS does
    not propagate signals to the compiled child binary; orphaned
    devices keep advertising on mDNS and leak into subsequent test runs.
    See the discovery interop fixture for the full rationale.
    """
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 4032  # arbitrary; not the binary's default of 1234
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
        "PHASE8-INTEROP-001",
        "-brand",
        "MASH-Phase8",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 8 Interop Device",
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
        yield proc, discriminator, setup_code, port
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
            err = (
                proc.stderr.read().decode("utf-8", errors="replace")
                if proc.stderr
                else ""
            )
            print(f"\n--- mash-device stderr (exit {proc.returncode}) ---\n{err}")


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(180)
async def test_live_pase_handshake_against_go_device(live_go_device) -> None:
    """End-to-end Phase 8 acceptance: PASE handshake completes with the
    Go device.

    Sequence:
      1. mDNS-browse for the device's commissionable record.
      2. Open TLS over the SRV-advertised commissioning port (DEC-082).
      3. Run :meth:`PASEClient.handshake` to derive ``K_main``.
      4. Assert the secret is 32 bytes and the encryption key is derived.

    This is the load-bearing test for Phase 8: success here means a
    Python controller can authenticate against a real Go device. Failure
    paths land in a follow-up once mash-go ships the Issue #6 fix.
    """
    proc, discriminator, setup_code, _port = live_go_device

    # 1) mDNS browse — wait up to 30s for ``go run`` cold compile + mDNS
    #    announce. The 30s budget mirrors the Phase 6 discovery test.
    async with Browser() as browser:
        try:
            svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=30.0
            )
        except BrowseTimeoutError:
            stderr = (
                proc.stderr.read(8192).decode("utf-8", errors="replace")
                if proc.stderr
                else "<no stderr>"
            )
            pytest.fail(
                f"Go device did not surface on mDNS within 30s. "
                f"stderr tail:\n{stderr}",
                pytrace=False,
            )

    assert svc.discriminator == discriminator
    assert svc.port > 0

    # 2) TLS connect on the SRV-advertised host:port using the
    #    commissioning ALPN (``mash-comm/1``). The browser surfaced both
    #    the .local hostname and a list of IP addresses. Prefer
    #    127.0.0.1 when the device is local; otherwise dial the first
    #    advertised IP — some hosts (notably this CI box) lack an inline
    #    .local resolver, so feeding ``getaddrinfo`` an mDNS hostname
    #    raises ``gaierror``. Plain IP literals always resolve.
    if "127.0.0.1" in svc.addresses:
        target_host = "127.0.0.1"
    elif svc.addresses:
        # Skip IPv6 addresses (which contain ":") to avoid IPv6 sockets
        # and dual-stack quirks; pick the first IPv4 if available.
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        target_host = ipv4 or svc.addresses[0]
    else:
        target_host = svc.host.rstrip(".")
    ssl_ctx = make_commissioning_client_context()

    reader, writer = await asyncio.open_connection(
        host=target_host,
        port=svc.port,
        ssl=ssl_ctx,
        server_hostname=target_host,
    )
    try:
        # Defensive: confirm the TLS layer landed on TLS 1.3 + commissioning ALPN
        # before we feed bytes to the PASE state machine.
        sslobj = _ssl_object(writer)
        assert sslobj is not None
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        assert alpn == "mash-comm/1", f"unexpected ALPN: {alpn!r}"

        # 3) Drive PASE.
        client = PASEClient(setup_code, discriminator)
        k_main = await asyncio.wait_for(client.handshake(reader, writer), timeout=15.0)
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await writer.wait_closed()

    # 4) Acceptance assertions.
    assert isinstance(k_main, bytes)
    assert len(k_main) == 32
    assert client.shared_secret == k_main
    assert len(client.encryption_key) == 32
    # K_main and K_encrypt are derived through different HKDF info
    # strings; if they collide that's a chain-derivation bug.
    assert client.shared_secret != client.encryption_key


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(180)
async def test_live_pase_wrong_setup_code_raises_auth_failed(live_go_device) -> None:
    """Failure-path acceptance: a wrong setup code triggers a
    spec-conformant PASEComplete{ErrCodeAuthFailed} from the device,
    which mash-py surfaces as :class:`PASEAuthFailedError`.

    Pre-Issue-#6 mash-go would have sent ``PASEComplete{ErrCodeConfirmFailed
    = 2}`` on MAC mismatch, which mash-py's :class:`PASEAuthFailedError`
    *also* covers (any non-zero code raises that class), but the test
    here additionally pins the error_code value to 1 — the DEC-047
    consolidated value — to lock in the fix. If mash-go regresses to
    sending code 2 again, this assertion catches it.
    """
    proc, discriminator, _setup_code, _port = live_go_device

    async with Browser() as browser:
        try:
            svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=30.0
            )
        except BrowseTimeoutError:
            pytest.fail("Go device did not surface on mDNS within 30s", pytrace=False)

    if "127.0.0.1" in svc.addresses:
        target_host = "127.0.0.1"
    elif svc.addresses:
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        target_host = ipv4 or svc.addresses[0]
    else:
        target_host = svc.host.rstrip(".")
    ssl_ctx = make_commissioning_client_context()

    reader, writer = await asyncio.open_connection(
        host=target_host,
        port=svc.port,
        ssl=ssl_ctx,
        server_hostname=target_host,
    )
    try:
        # Use a deliberately wrong setup code. The device computed its
        # verifier from "20202021"; we drive PASE with "87654321" so
        # the SPAKE2+ MAC mismatch fires.
        client = PASEClient("87654321", discriminator)
        with pytest.raises(PASEAuthFailedError) as excinfo:
            await asyncio.wait_for(client.handshake(reader, writer), timeout=15.0)
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await writer.wait_closed()

    # DEC-047 / Issue #6: every in-PASE failure unifies to ErrCodeAuthFailed = 1.
    # If this assertion regresses to 2 (ErrCodeConfirmFailed), mash-go has
    # backslid on the §2.5 unification.
    from mash_py.commissioning import ERR_CODE_AUTH_FAILED

    assert excinfo.value.error_code == ERR_CODE_AUTH_FAILED, (
        f"expected error_code={ERR_CODE_AUTH_FAILED} (ErrCodeAuthFailed) "
        f"per spec §2.5, got {excinfo.value.error_code}"
    )
