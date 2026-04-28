"""Phase 19 interop: in-session cert renewal against a live ``mash-go`` device.

Phase 19 acceptance per ``mash-py-tdd-plan.md``:

> Renewal completes both directions; the operational connection
> survives (no TLS reconnection per DEC-076).

This test stitches Phase 9 commissioning (DEC-085 envelope) and Phase 10
operational reconnect onto a Phase 19 renewal flow:

  1. Spawn ``mash-go/cmd/mash-device`` uncommissioned.
  2. mash-py controller commissions it (PASE → cert install).
  3. Open the operational connection on ``mash/1`` (mTLS, Zone CA-signed).
  4. Drive renewal end-to-end on the *same* TCP/TLS session:
     ``CertRenewalRequest (30) → CertRenewalCSR (31) →
     CertRenewalInstall (32) → CertRenewalAck (33)``.
  5. Verify ``CertRenewalAck.status == 0`` and that the device echoed
     the controller's sequence in ``active_sequence``.
  6. Confirm the connection is still alive by sending a DeviceInfo Read
     and decoding the response — DEC-076 forbids a TLS reconnection
     across renewal.

The new operational cert produced by renewal does *not* take effect on
the running mTLS session (TLS keys are unchanged); it only binds the
*next* operational handshake. The connection-survival check is therefore
independent of the new cert.

Pre-Phase-19, mash-go's renewal envelope still carried a vestigial
``ZoneCA`` field on ``CertRenewalRequest`` (DEC-085 leftover) and an
optional ``NonceHash`` on ``CertRenewalCSR``. This test passes only
against a mash-go that's been updated to the spec-strict shape — see
``mash-py-tdd-plan.md §Phase 19`` for the cross-repo alignment notes.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import shutil
import signal
import socket
import ssl
import subprocess
import tempfile
from pathlib import Path

import pytest
from cryptography import x509
from cryptography.x509.oid import NameOID

from mash_py.commissioning import PASEClient
from mash_py.commissioning.cert_exchange import CertExchangeClient
from mash_py.commissioning.cert_store import (
    ZoneType,
    generate_controller_cert,
    generate_zone_ca,
)
from mash_py.commissioning.renewal import ControllerRenewalDriver
from mash_py.commissioning.renewal_messages import (
    CertRenewalAck,
    CertRenewalCSR,
    decode as decode_renewal,
    encode as encode_renewal,
)
from mash_py.discovery import Browser, BrowseTimeoutError
from mash_py.service.controller import open_operational_connection
from mash_py.transport.connection import _ssl_object
from mash_py.transport.tls import (
    make_commissioning_client_context,
    verify_alpn,
    verify_tls13,
)
from mash_py.wire import (
    Operation,
    ReadPayload,
    Response,
    Status,
    decode_response,
    encode_request,
)
from mash_py.wire.framing import AsyncFrameReader, AsyncFrameWriter
from mash_py.wire.payloads import Request

REPO_ROOT = Path(__file__).resolve().parents[4]
MASH_GO_DIR = REPO_ROOT / "mash-go"

pytestmark = pytest.mark.interop

FEATURE_DEVICE_INFO = 0x01


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _pick_target_host(svc) -> str:
    if "127.0.0.1" in svc.addresses:
        return "127.0.0.1"
    if svc.addresses:
        ipv4 = next((a for a in svc.addresses if ":" not in a), None)
        return ipv4 or svc.addresses[0]
    return svc.host.rstrip(".")


@pytest.fixture
def go_device_state_dir():
    d = tempfile.mkdtemp(prefix="mash-py-phase19-")
    try:
        yield d
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture
def live_go_device(go_device_state_dir):
    """Launch ``go run ./cmd/mash-device`` uncommissioned.

    Distinct discriminator from Phases 6/8/9/10/11/15/17/18 to avoid mDNS
    cross-test bleed. Process-group SIGTERM teardown matches the existing
    interop fixtures.
    """
    if not _go_available():
        pytest.skip("Go toolchain not available")
    if not MASH_GO_DIR.is_dir():
        pytest.skip(f"mash-go checkout not found at {MASH_GO_DIR}")

    discriminator = 1920
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
        "PHASE19-INTEROP-001",
        "-brand",
        "MASH-Phase19",
        "-model",
        "EVSE-X",
        "-name",
        "Phase 19 Interop Device",
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


async def _commission_and_get_op_target(
    proc, discriminator: int, setup_code: str
):
    """Drive PASE + cert-install, return (op_host, op_port, device_id, zone_ca, controller_cert)."""
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

    target_host = _pick_target_host(comm_svc)
    comm_ctx = make_commissioning_client_context()
    comm_reader, comm_writer = await asyncio.open_connection(
        host=target_host,
        port=comm_svc.port,
        ssl=comm_ctx,
        server_hostname=target_host,
    )

    zone_ca = generate_zone_ca("mash-py-phase19", ZoneType.LOCAL)
    try:
        sslobj = _ssl_object(comm_writer)
        assert sslobj is not None
        verify_tls13(sslobj)
        alpn = verify_alpn(sslobj)
        assert alpn == "mash-comm/1"

        pase = PASEClient(setup_code, discriminator)
        await asyncio.wait_for(
            pase.handshake(comm_reader, comm_writer), timeout=15.0
        )

        cert_client = CertExchangeClient(zone_ca=zone_ca, zone_priority=1)
        result = await asyncio.wait_for(
            cert_client.run(comm_reader, comm_writer), timeout=30.0
        )
        device_id = result.device_id
    finally:
        comm_writer.close()
        with contextlib.suppress(ConnectionError, OSError):
            await comm_writer.wait_closed()

    assert device_id and len(device_id) == 16

    async with Browser() as browser:
        try:
            op_svc = await browser.find_operational_by_device_id(
                device_id, timeout=30.0
            )
        except BrowseTimeoutError:
            pytest.fail(
                f"device did not advertise on _mash._tcp with DI={device_id!r}",
                pytrace=False,
            )

    op_host = _pick_target_host(op_svc)
    controller_cert = generate_controller_cert(
        zone_ca, controller_id="phase19-controller"
    )
    return op_host, op_svc.port, device_id, zone_ca, controller_cert


# ---------------------------------------------------------------------------
# Renewal exchange helpers
# ---------------------------------------------------------------------------


async def _send_renewal(
    fw: AsyncFrameWriter, writer: asyncio.StreamWriter, payload: bytes
) -> None:
    await fw.write_frame(payload)
    await writer.drain()


async def _read_renewal_message(
    fr: AsyncFrameReader, expected_type: type
) -> object:
    """Read one frame, decode as renewal message, assert dataclass type."""
    payload = await fr.read_frame()
    msg = decode_renewal(payload)
    assert isinstance(msg, expected_type), (
        f"expected {expected_type.__name__}, got {type(msg).__name__}: {msg!r}"
    )
    return msg


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(300)
async def test_renewal_against_go_device(live_go_device) -> None:
    """End-to-end Phase 19 acceptance: renewal completes against mash-go,
    and the operational TLS session survives (DEC-076)."""
    proc, discriminator, setup_code, _port = live_go_device

    op_host, op_port, device_id, zone_ca, controller_cert = (
        await _commission_and_get_op_target(proc, discriminator, setup_code)
    )

    reader, writer, _peer_cert = await open_operational_connection(
        op_host,
        op_port,
        controller_cert=controller_cert,
        server_hostname=device_id,
        expected_device_id=device_id,
    )

    fr = AsyncFrameReader(reader)
    fw = AsyncFrameWriter(writer)

    # Sequence 1 was issued during commissioning (via Phase 9 cert install).
    # Renewal MUST emit a strictly higher sequence — start the driver at 1
    # so build_request increments to 2 on first run.
    driver = ControllerRenewalDriver(zone_ca, initial_sequence=1)

    try:
        # Step 1: Controller -> Device  CertRenewalRequest (30)
        request = driver.build_request()
        await _send_renewal(fw, writer, encode_renewal(request))

        # Step 2: Device -> Controller  CertRenewalCSR (31)
        csr_msg = await asyncio.wait_for(
            _read_renewal_message(fr, CertRenewalCSR), timeout=15.0
        )

        # Step 3: Controller -> Device  CertRenewalInstall (32)
        install = driver.handle_csr(csr_msg)
        assert install.sequence == 2
        await _send_renewal(fw, writer, encode_renewal(install))

        # Step 4: Device -> Controller  CertRenewalAck (33)
        ack = await asyncio.wait_for(
            _read_renewal_message(fr, CertRenewalAck), timeout=15.0
        )
        new_cert = driver.handle_ack(ack)
        assert ack.status == 0
        assert ack.active_sequence == install.sequence

        # The device signed off on the new cert. Verify locally that
        # mash-py's controller saw a sane cert: chains to our Zone CA,
        # CN equals the freshly-derived device-id (Matter-style — the
        # CSR public key changed, so the device-id changes too).
        assert isinstance(new_cert, x509.Certificate)
        cn = new_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value
        assert cn == driver.device_id
        # Issuer must be the Zone CA we control.
        assert new_cert.issuer == zone_ca.certificate.subject

        # ------------------------------------------------------------------
        # Connection-survival probe (DEC-076): the same TLS session must
        # still be usable for data-plane traffic. Issue a DeviceInfo Read
        # and decode the response. The peer cert presented at handshake
        # time hasn't changed (renewal does not rotate TLS keys); the new
        # cert only takes effect on the next operational handshake.
        # ------------------------------------------------------------------
        read_req = Request(
            message_id=1,
            operation=Operation.READ,
            endpoint_id=0,
            feature_id=FEATURE_DEVICE_INFO,
            payload=ReadPayload(attribute_ids=[]),
        )
        await fw.write_frame(encode_request(read_req))
        await writer.drain()

        resp_payload = await asyncio.wait_for(fr.read_frame(), timeout=15.0)
        response: Response = decode_response(resp_payload)
        assert response.message_id == read_req.message_id, (
            "DeviceInfo Response messageId did not echo the request — "
            "stream became desynchronised across renewal"
        )
        assert response.status == Status.SUCCESS, (
            f"DeviceInfo Read after renewal returned status="
            f"{int(response.status)} ({response.status.name}); "
            "operational connection did not survive renewal (DEC-076)"
        )
        assert isinstance(response.payload, dict)
        assert response.payload, "DeviceInfo Read returned empty attribute map"
    finally:
        writer.close()
        with contextlib.suppress(ConnectionError, OSError, ssl.SSLError):
            await writer.wait_closed()
