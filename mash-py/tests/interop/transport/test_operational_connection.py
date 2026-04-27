"""Phase 10 interop: post-commissioning operational connection + DeviceInfo Read.

The Phase-10 acceptance gate per ``mash-py-tdd-plan.md``:

    Read succeeds; payload contains expected vendor/product/serial fields.

This test stitches Phases 6 (mDNS), 8 (PASE), 9 (cert install), and 10
(operational reconnect) end-to-end against ``mash-go/cmd/mash-device``:

  1. Spawn the Go device in commissioning mode.
  2. Browse ``_mash-comm._tcp`` and run PASE + cert install.
  3. Browse ``_mash._tcp`` for the now-operational record.
  4. Open a fresh TLS connection on ``mash/1`` ALPN with mTLS — the
     controller's leaf cert is signed by the same Zone CA the device
     installed, so the device's ``RequireAndVerifyClientCert`` config
     accepts us.
  5. Send a single ``Request{op=Read, endpoint=0, feature=0x01,
     payload=[]}`` (DeviceInfo, all attributes).
  6. Decode the ``Response`` and assert the well-known string
     attributes carry the values we passed to ``mash-device`` on the
     command line.

The test passes only against a mash-go that has the DEC-085
commissioning envelope (otherwise Phase 9 fails). It is the first test
in the suite that exercises the operational ALPN + mTLS path, so it
also acts as the regression gate for the Zone-CA → controller cert →
mTLS plumbing.
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
from mash_py.commissioning.cert_store import (
    ZoneType,
    generate_controller_cert,
    generate_zone_ca,
)
from mash_py.discovery import Browser, BrowseTimeoutError
from mash_py.service.controller import reconnect_and_read_device_info
from mash_py.transport.connection import _ssl_object  # internal helper
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)

REPO_ROOT = Path(__file__).resolve().parents[4]
MASH_GO_DIR = REPO_ROOT / "mash-go"

pytestmark = pytest.mark.interop


# DeviceInfo attribute IDs (docs/features/device-info/1.0.yaml).
ATTR_DEVICE_ID = 1
ATTR_VENDOR_NAME = 2
ATTR_PRODUCT_NAME = 3
ATTR_SERIAL_NUMBER = 4
ATTR_SOFTWARE_VERSION = 10
ATTR_SPEC_VERSION = 12
ATTR_ENDPOINTS = 20


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _pick_target_host(svc) -> str:
    """Mirror Phase 8/9: prefer 127.0.0.1, then any IPv4, then bare host."""
    if "127.0.0.1" in svc.addresses:
        return "127.0.0.1"
    if svc.addresses:
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        return ipv4 or svc.addresses[0]
    return svc.host.rstrip(".")


@pytest.fixture
def go_device_state_dir():
    d = tempfile.mkdtemp(prefix="mash-py-phase10-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Launch ``go run ./cmd/mash-device`` uncommissioned. Process-group
    SIGTERM tear-down per Phase 9 — ``go run`` does not propagate signals
    to the compiled child on macOS, and an orphaned device keeps
    advertising on mDNS."""
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 2048  # distinct from Phase 8 (4032) and Phase 9 (3072)
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
        "PHASE10-INTEROP-001",
        "-brand",
        "MASH-Phase10",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 10 Interop Device",
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


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
async def test_operational_read_device_info(live_go_device) -> None:
    """End-to-end Phase 10: PASE + cert install + reconnect on mash/1
    + Read DeviceInfo. The Read response MUST contain the vendor /
    product / serial values we passed on the mash-device CLI."""
    proc, discriminator, setup_code, _port = live_go_device

    # ------------------------------------------------------------------
    # 1) Find the commissioning record.
    # ------------------------------------------------------------------
    async with Browser() as browser:
        try:
            comm_svc = await browser.find_commissionable_by_discriminator(
                discriminator, timeout=30.0
            )
        except BrowseTimeoutError:
            stderr = (
                proc.stderr.read(8192).decode("utf-8", errors="replace")
                if proc.stderr
                else "<no stderr>"
            )
            pytest.fail(
                f"Go device did not surface on _mash-comm._tcp within 30s. "
                f"stderr tail:\n{stderr}",
                pytrace=False,
            )

    assert comm_svc.discriminator == discriminator
    target_host = _pick_target_host(comm_svc)

    # ------------------------------------------------------------------
    # 2) Commissioning TLS (mash-comm/1) -> PASE -> cert install.
    # ------------------------------------------------------------------
    comm_ctx = make_commissioning_client_context()
    comm_reader, comm_writer = await asyncio.open_connection(
        host=target_host,
        port=comm_svc.port,
        ssl=comm_ctx,
        server_hostname=target_host,
    )

    zone_ca = generate_zone_ca("mash-py-phase10", ZoneType.LOCAL)
    device_id = None
    try:
        sslobj = _ssl_object(comm_writer)
        assert sslobj is not None
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        assert alpn == "mash-comm/1", f"unexpected commissioning ALPN: {alpn!r}"

        pase = PASEClient(setup_code, discriminator)
        k_main = await asyncio.wait_for(
            pase.handshake(comm_reader, comm_writer), timeout=15.0
        )
        assert len(k_main) == 32  # sanity — the ack is implicit via cert install

        cert_client = CertExchangeClient(zone_ca=zone_ca, zone_priority=1)
        result = await asyncio.wait_for(
            cert_client.run(comm_reader, comm_writer), timeout=30.0
        )
        device_id = result.device_id
    finally:
        comm_writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await comm_writer.wait_closed()

    assert device_id is not None and len(device_id) == 16

    # ------------------------------------------------------------------
    # 3) Browse the operational record. The device closes the
    #    commissioning connection and starts advertising on _mash._tcp
    #    after CommissioningComplete (security.md §4.5). Phase 9 has
    #    already verified the advertisement appears within 30s; we
    #    wait the same budget here.
    # ------------------------------------------------------------------
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
    op_host = _pick_target_host(op_svc)

    # ------------------------------------------------------------------
    # 4) Mint the controller's operational cert from the Zone CA we
    #    just bootstrapped.
    # ------------------------------------------------------------------
    controller_cert = generate_controller_cert(
        zone_ca, controller_id="phase10-controller"
    )

    # ------------------------------------------------------------------
    # 5) Reconnect on mash/1 with mTLS and Read DeviceInfo.
    # ------------------------------------------------------------------
    snapshot = await reconnect_and_read_device_info(
        host=op_host,
        port=op_svc.port,
        controller_cert=controller_cert,
        # SNI hint: mash-go uses ServerName to route to the correct
        # device cert when multiple zones are present (see
        # device_service_lifecycle.go:240). Passing the device-id as
        # SNI mirrors what a real controller would send.
        server_hostname=device_id,
        expected_device_id=device_id,
        timeout=30.0,
    )

    # ------------------------------------------------------------------
    # 6) Verify DeviceInfo carries the values we passed on the CLI.
    # ------------------------------------------------------------------
    attrs = snapshot.attributes
    assert attrs, "DeviceInfo Read returned an empty attribute map"
    # The cert-derived device-id (16-hex-char Matter-style ID per
    # commissioning-pase.md §4.4) is the connection-level identity.
    assert snapshot.device_id == device_id

    # ``DeviceInfo.deviceId`` (attribute 1) is a separate concept from
    # the cert CN per ``docs/features/device-info.md §"Device ID
    # Format"``: it is a human-readable globally unique identifier in
    # ``PEN<number>.<serial>`` form (with an IANA Private Enterprise
    # Number) or ``<vendor>-<product>-<serial>`` fallback. The
    # mash-device CLI uses placeholder Vendor IDs (0x1234), so it
    # composes the fallback form from ``-brand``/``-model``/``-serial``.
    # See mash-py-spec-issues.md Issue #9 for the spec/Go alignment
    # history.
    di_attr = attrs.get(ATTR_DEVICE_ID)
    expected_di = "MASH-Phase10-EVSE-X-PHASE10-INTEROP-001"
    assert di_attr == expected_di, (
        f"DeviceInfo.deviceId ({di_attr!r}) does not match the "
        f"spec-format <vendor>-<product>-<serial> value "
        f"({expected_di!r})"
    )

    # Mandatory string attributes per docs/features/device-info/1.0.yaml.
    assert attrs.get(ATTR_VENDOR_NAME) == "MASH-Phase10"
    assert attrs.get(ATTR_PRODUCT_NAME) == "EVSE-X"
    assert attrs.get(ATTR_SERIAL_NUMBER) == "PHASE10-INTEROP-001"

    # specVersion has a YAML default of "1.0" and is mandatory.
    assert attrs.get(ATTR_SPEC_VERSION) == "1.0"

    # softwareVersion is mandatory; mash-device sets a non-empty string.
    sw = attrs.get(ATTR_SOFTWARE_VERSION)
    assert isinstance(sw, str) and sw, (
        f"DeviceInfo.softwareVersion is missing or empty: {sw!r}"
    )

    # endpoints is mandatory and must include at least the root endpoint
    # (DEVICE_ROOT, ID 0) plus the EVSE endpoint we asked for.
    endpoints = attrs.get(ATTR_ENDPOINTS)
    assert isinstance(endpoints, list) and len(endpoints) >= 2, (
        f"DeviceInfo.endpoints must list >=2 endpoints, got: {endpoints!r}"
    )
