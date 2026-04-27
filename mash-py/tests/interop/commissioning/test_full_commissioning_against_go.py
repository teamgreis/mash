"""Phase 9 interop: full commissioning against a live mash-go device (DEC-085).

The Phase 9 acceptance gate per ``mash-py-tdd-plan.md``:

> Go device transitions to operational state and starts advertising
> ``_mash._tcp`` with the new ZoneID.

This test stitches PASE (Phase 8) + cert install (Phase 9) end-to-end
against ``mash-go/cmd/mash-device``:

  1. Spawn the Go device in commissioning mode (uncommissioned state dir).
  2. mDNS-browse for ``_mash-comm._tcp`` and connect on ``mash-comm/1`` ALPN.
  3. Run :class:`PASEClient` to derive ``K_main``.
  4. Run :class:`CertExchangeClient` to drive CSRRequest/CSRResponse/
     CertInstall/CertInstallResponse/CommissioningComplete (MsgTypes
     10/11/12/13/20).
  5. Verify the returned operational cert chains to the controller's
     Zone CA and that the device ID derives correctly from the CSR
     public key.
  6. Verify the device closes the commissioning connection and starts
     advertising on ``_mash._tcp`` with the new device ID.

Pre-DEC-085, mash-go drove this step with the renewal envelope (MsgTypes
30–33). This test passes only against a mash-go that's been updated to
the spec-strict envelope, so it's also the regression gate that pins
both implementations to the same wire shape going forward.
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

from mash_py.commissioning import PASEClient
from mash_py.commissioning.cert_exchange import CertExchangeClient
from mash_py.commissioning.cert_store import ZoneType, generate_zone_ca
from mash_py.crypto.certs import derive_device_id, verify_cert_chain
from mash_py.discovery import Browser, BrowseTimeoutError
from mash_py.transport.connection import _ssl_object  # internal helper
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)

REPO_ROOT = Path(__file__).resolve().parents[4]
MASH_GO_DIR = REPO_ROOT / "mash-go"

pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def go_device_state_dir():
    d = tempfile.mkdtemp(prefix="mash-py-phase9-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Launch ``go run ./cmd/mash-device`` uncommissioned. Same fixture
    shape as Phase 8's ``live_go_device`` — process-group SIGTERM tear-down
    is required because ``go run`` does not propagate signals to the
    compiled child on macOS, and orphaned devices keep advertising on
    mDNS and pollute subsequent test runs."""
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    # Discriminator is 12-bit (0-4095). Phase 8 uses 4032; pick a distinct
    # in-range value to avoid mDNS collisions if both interop fixtures
    # ever run concurrently.
    discriminator = 3072
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
        "PHASE9-INTEROP-001",
        "-brand",
        "MASH-Phase9",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 9 Interop Device",
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
            print(
                f"\n--- mash-device stderr (exit {proc.returncode}) ---\n{err}"
            )


def _pick_target_host(svc) -> str:
    """Some hosts (CI boxes) lack an inline ``.local`` resolver, so feeding
    ``getaddrinfo`` an mDNS hostname raises ``gaierror``. Plain IP
    literals always resolve. Mirror Phase 8's resolution order: prefer
    127.0.0.1, fall back to the first IPv4 address, then anything else."""
    if "127.0.0.1" in svc.addresses:
        return "127.0.0.1"
    if svc.addresses:
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        return ipv4 or svc.addresses[0]
    return svc.host.rstrip(".")


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
async def test_full_commissioning_against_go_device(live_go_device) -> None:
    """End-to-end Phase 9 acceptance: PASE + cert install completes against
    the Go device, the device transitions to operational, and the new
    operational mDNS record (DI=device_id) becomes visible."""
    proc, discriminator, setup_code, _port = live_go_device

    # 1) mDNS browse the commissionable record.
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

    # 2) TLS connect on commissioning ALPN.
    target_host = _pick_target_host(svc)
    ssl_ctx = make_commissioning_client_context()
    reader, writer = await asyncio.open_connection(
        host=target_host,
        port=svc.port,
        ssl=ssl_ctx,
        server_hostname=target_host,
    )

    operational_cert = None
    device_id = None
    zone_ca = generate_zone_ca("mash-py-phase9", ZoneType.LOCAL)

    try:
        sslobj = _ssl_object(writer)
        assert sslobj is not None
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        assert alpn == "mash-comm/1", f"unexpected ALPN: {alpn!r}"

        # 3) PASE.
        pase = PASEClient(setup_code, discriminator)
        k_main = await asyncio.wait_for(
            pase.handshake(reader, writer), timeout=15.0
        )
        assert len(k_main) == 32

        # 4) Cert install (DEC-085 envelope).
        cert_client = CertExchangeClient(zone_ca=zone_ca, zone_priority=1)
        result = await asyncio.wait_for(
            cert_client.run(reader, writer), timeout=30.0
        )
        operational_cert = result.operational_cert
        device_id = result.device_id
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await writer.wait_closed()

    # 5) Cert chain + device ID assertions.
    assert operational_cert is not None
    assert device_id is not None
    assert verify_cert_chain(operational_cert, zone_ca.certificate)
    # The device ID is the controller-derived 16-hex-char identifier; the
    # CN of the operational cert MUST match it (Matter pattern,
    # commissioning-pase.md §4.4).
    from cryptography.x509.oid import NameOID

    cn = operational_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value
    assert cn == device_id, (
        f"operational cert CN ({cn!r}) does not match derived device ID ({device_id!r})"
    )
    # Re-derive the device ID from the cert public key and confirm it
    # matches what CertExchangeClient returned. If these diverge, the
    # device used a different public key than the one in the CSR — i.e.
    # the cert install was processed against a stale keypair.
    pub = operational_cert.public_key()
    assert derive_device_id(pub) == device_id

    # 6) Verify the device transitioned to operational + advertises on _mash._tcp.
    #    The device closes the commissioning connection after
    #    CommissioningComplete (security.md §4.5), so this browse runs on
    #    a fresh Browser context. The device may take a moment to swap
    #    advertisements; a 30s budget mirrors Phase 6.
    async with Browser() as browser:
        try:
            op_svc = await browser.find_operational_by_device_id(
                device_id, timeout=30.0
            )
        except BrowseTimeoutError:
            pytest.fail(
                f"device did not advertise on _mash._tcp with DI={device_id!r} "
                f"within 30s after commissioning",
                pytrace=False,
            )

    assert op_svc.device_id == device_id
    assert op_svc.port > 0
