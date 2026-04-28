"""Phase 15 interop: a Go controller exercises Read / Write / Invoke /
Subscribe against a Python :class:`mash_py.service.DeviceService`.

What this test proves
---------------------

The Phase-15 acceptance gate per ``mash-py-tdd-plan.md``: a Go controller
performs the four operational ops and gets the expected results from a
Python device. Concretely:

- **Read DeviceInfo** — controller reads, gets the values the Python
  device set (vendorName / productName / serialNumber).
- **Write DeviceInfo.label** — controller writes a string, response
  echoes the stored value, the Python model reflects the change.
- **Invoke removeZone** — controller invokes with integer-keyed
  parameters per ``§7.1`` (Issue #11), helper unpacks the integer-keyed
  response per ``§7.2``.
- **Subscribe** — controller's Subscribe MUST surface ``UNSUPPORTED``
  (Phase 16 wires up real subscriptions).

Scope and what's deliberately *not* tested here
-----------------------------------------------

Commissioning (PASE + cert install) is exercised end-to-end in the
Phase 14b test (``test_cert_exchange_server_with_go_controller.py``).
Reproducing it here would re-cover known-working ground and force a
two-stage helper. Instead the Python test mints synthetic certs (Zone
CA, device op cert, controller op cert) and stands up the
:class:`DeviceService` directly. The combined commissioning +
operational lifecycle would be a Phase 22 conformance scenario.
"""

from __future__ import annotations

import asyncio
import json
import shutil
from pathlib import Path

import pytest
from cryptography.hazmat.primitives import serialization

from mash_py.commissioning.cert_store import (
    ZoneCA,
    ZoneType,
    sign_device_csr,
)
from mash_py.crypto.certs import (
    CSRInfo,
    create_csr,
    generate_ecdsa_p256_keypair,
    generate_zone_ca,
    sign_csr,
)
from mash_py.features.device_info_gen import (
    DEVICE_INFO_ATTR_LABEL,
    DEVICE_INFO_CMD_REMOVE_ZONE,
    REMOVE_ZONE_PARAM_ZONE_ID,
    REMOVE_ZONE_RESP_REMOVED,
    DeviceInfo,
)
from mash_py.model import Device, Endpoint, EndpointType
from mash_py.service import DeviceService

MASH_PY_ROOT = Path(__file__).resolve().parents[2]  # .../mash-py/
HELPER_DIR = MASH_PY_ROOT / "tools" / "run_operational_client"


pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


@pytest.fixture
def cert_files(tmp_path: Path):
    """Mint Zone CA + device op cert + controller op cert; write the
    files the Go helper needs.

    Returns a dict with the paths and the in-memory certs/keys (the
    Python test side spins up :class:`DeviceService` directly with the
    in-memory artifacts).
    """
    zone_ca_key = generate_ecdsa_p256_keypair()
    zone_ca_cert = generate_zone_ca(zone_ca_key, zone_name="phase15-interop")
    zone_ca = ZoneCA(
        zone_name="phase15-interop",
        zone_type=ZoneType.LOCAL,
        certificate=zone_ca_cert,
        private_key=zone_ca_key,
    )

    device_key = generate_ecdsa_p256_keypair()
    device_csr = create_csr(device_key, info=CSRInfo(common_name="device-csr"))
    device_cert, device_id = sign_device_csr(zone_ca, device_csr)

    controller_key = generate_ecdsa_p256_keypair()
    controller_csr = create_csr(
        controller_key, info=CSRInfo(common_name="controller-csr")
    )
    controller_cert = sign_csr(
        zone_ca_cert,
        zone_ca_key,
        controller_csr,
        zone_name="phase15-interop",
    )

    ca_pem = zone_ca_cert.public_bytes(serialization.Encoding.PEM)
    ctl_cert_pem = controller_cert.public_bytes(serialization.Encoding.PEM)
    ctl_key_pem = controller_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    )

    ca_path = tmp_path / "zone-ca.pem"
    ctl_cert_path = tmp_path / "ctl.crt.pem"
    ctl_key_path = tmp_path / "ctl.key.pem"
    ca_path.write_bytes(ca_pem)
    ctl_cert_path.write_bytes(ctl_cert_pem)
    ctl_key_path.write_bytes(ctl_key_pem)

    return {
        "zone_ca_cert": zone_ca_cert,
        "device_cert": device_cert,
        "device_key": device_key,
        "device_id": device_id,
        "ca_path": ca_path,
        "ctl_cert_path": ctl_cert_path,
        "ctl_key_path": ctl_key_path,
    }


def _build_device_for_interop(device_id: str):
    """Build the Phase-15 interop fixture device.

    Returns ``(device, captured_invocations)``. The handler appends each
    received parameter map to ``captured_invocations`` so the test can
    assert the integer-keyed wire shape arrived intact.
    """
    captured: list[dict[int, object]] = []

    device = Device(device_id)
    di = DeviceInfo()
    di.set_device_id(device_id)
    di.set_vendor_name("MASH-Phase15")
    di.set_product_name("Phase15 EVSE")
    di.set_serial_number("PHASE15-INTEROP-001")
    device.root.add_feature(di)
    device.add_endpoint(Endpoint(1, EndpointType.EV_CHARGER, "Phase15 EVSE"))

    def remove_zone(params: dict[int, object]) -> dict[int, object]:
        captured.append(dict(params))
        if params.get(REMOVE_ZONE_PARAM_ZONE_ID) == "phase15-interop":
            return {REMOVE_ZONE_RESP_REMOVED: True}
        return {REMOVE_ZONE_RESP_REMOVED: False}

    di.set_command_handler(DEVICE_INFO_CMD_REMOVE_ZONE, remove_zone)

    return device, captured


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(180)
async def test_full_device_lifecycle_against_go_controller(cert_files) -> None:
    """End-to-end: Go controller drives Read / Write / Invoke / Subscribe
    against a Python :class:`DeviceService`."""
    device, captured = _build_device_for_interop(cert_files["device_id"])

    service = DeviceService(
        device,
        zone_ca=cert_files["zone_ca_cert"],
        operational_cert=cert_files["device_cert"],
        operational_key=cert_files["device_key"],
    )
    await service.start(host="127.0.0.1", port=0)

    try:
        target = f"127.0.0.1:{service.port}"
        proc = await asyncio.create_subprocess_exec(
            "go",
            "run",
            "./run_operational_client",
            "--target",
            target,
            "--device-id",
            cert_files["device_id"],
            "--controller-cert-pem",
            str(cert_files["ctl_cert_path"]),
            "--controller-key-pem",
            str(cert_files["ctl_key_path"]),
            "--zone-ca-pem",
            str(cert_files["ca_path"]),
            "--label-write",
            "phase15-helper-write",
            "--invoke-zone",
            "phase15-interop",
            "--timeout",
            "30",
            "--dial-retry",
            "5",
            cwd=str(HELPER_DIR.parent),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(
                proc.communicate(), timeout=120.0
            )
        except TimeoutError:
            proc.kill()
            await proc.wait()
            raise

        if proc.returncode != 0:
            pytest.fail(
                f"helper exit {proc.returncode}\n"
                f"stdout: {stdout.decode('utf-8', errors='replace')}\n"
                f"stderr: {stderr.decode('utf-8', errors='replace')}",
                pytrace=False,
            )

        result = json.loads(stdout.decode("utf-8"))

        read_dict = result["read_device_info"]
        assert read_dict["vendorName"] == "MASH-Phase15"
        assert read_dict["productName"] == "Phase15 EVSE"

        assert result["write_label"]["echo"] == "phase15-helper-write"
        assert result["write_label"]["stored"] == "phase15-helper-write"
        di = device.get_feature(0, DeviceInfo().feature_type)
        assert di.read_attribute(DEVICE_INFO_ATTR_LABEL) == "phase15-helper-write"

        assert result["invoke_remove_zone"]["status"] == 0  # SUCCESS
        assert result["invoke_remove_zone"]["removed"] is True
        assert len(captured) == 1
        assert captured[0] == {REMOVE_ZONE_PARAM_ZONE_ID: "phase15-interop"}

        # Phase 16: Subscribe is now SUCCESS (status 0). The Phase-15 era
        # value (10 = UNSUPPORTED) was the expected outcome only while the
        # operational server stubbed Subscribe out — see git history of
        # mash_py/interaction/server.py. Helper without --observe-subscribe
        # only confirms the priming round-trip; the rich notification
        # acceptance is in test_phase16_subscriptions_against_go_controller.
        assert result["subscribe_status"] == 0  # Status.SUCCESS
    finally:
        await service.stop()


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(180)
async def test_phase16_subscriptions_against_go_controller(cert_files) -> None:
    """Phase 16 acceptance: a Go controller subscribes to DeviceInfo on
    a Python :class:`DeviceService`, then writes the label, and observes
    the resulting Notification frame.

    What this proves
    ----------------
    - Subscribe wire path (`§6.1` request shape, `§6.2` priming response).
    - Write-then-notify ordering (`transport.md §8.4` / DEC-071):
      the Notification arrives on the same TLS connection, after the
      helper's Write has been acknowledged.
    - Wire-encoded Notification frame (`§3.3`, `§6.3`).
    - Unsubscribe via the (endpoint=0, feature=0) sentinel (`§6.4`).

    Same-connection observation
    ---------------------------
    The Go helper subscribes and writes on the same TLS connection.
    DEC-071 specifies cross-connection ordering, but same-connection
    delivery is the looser test that still proves notifications fan
    out end-to-end. The cross-connection ordering test belongs in
    Phase 21 (multi-zone test harness).
    """
    device, _ = _build_device_for_interop(cert_files["device_id"])

    service = DeviceService(
        device,
        zone_ca=cert_files["zone_ca_cert"],
        operational_cert=cert_files["device_cert"],
        operational_key=cert_files["device_key"],
    )
    await service.start(host="127.0.0.1", port=0)

    try:
        target = f"127.0.0.1:{service.port}"
        proc = await asyncio.create_subprocess_exec(
            "go",
            "run",
            "./run_operational_client",
            "--target",
            target,
            "--device-id",
            cert_files["device_id"],
            "--controller-cert-pem",
            str(cert_files["ctl_cert_path"]),
            "--controller-key-pem",
            str(cert_files["ctl_key_path"]),
            "--zone-ca-pem",
            str(cert_files["ca_path"]),
            "--label-write",
            "phase16-helper",
            "--observe-subscribe",
            "--subscribe-min-interval-ms",
            "50",
            "--subscribe-max-interval-ms",
            "60000",
            "--subscribe-wait",
            "5",
            "--timeout",
            "60",
            "--dial-retry",
            "5",
            cwd=str(HELPER_DIR.parent),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(
                proc.communicate(), timeout=120.0
            )
        except TimeoutError:
            proc.kill()
            await proc.wait()
            raise

        if proc.returncode != 0:
            pytest.fail(
                f"helper exit {proc.returncode}\n"
                f"stdout: {stdout.decode('utf-8', errors='replace')}\n"
                f"stderr: {stderr.decode('utf-8', errors='replace')}",
                pytrace=False,
            )

        result = json.loads(stdout.decode("utf-8"))
        sub = result.get("subscription")
        assert sub is not None, (
            f"helper did not produce a subscription observation: {result!r}"
        )

        # Priming evidence: priming carried the existing values.
        assert sub["priming_vendor"] == "MASH-Phase15", (
            f"priming vendorName mismatch: {sub!r}"
        )

        # Notification evidence: a Notification arrived after the Write,
        # carrying the new label as a delta.
        assert sub["notif_received"], (
            "Phase 16: helper did not receive a Notification within "
            "subscribe-wait. DEC-071 requires the Python device to fan "
            "out notifications synchronously on Write commit. "
            f"Helper output: {sub!r}"
        )
        assert sub["notif_label"] == "phase16-helper-after-sub", (
            f"Notification did not carry the new label: {sub!r}"
        )
        # Bounce-back suppression should keep vendorName out of the delta.
        assert "notif_keys" in sub
        assert sub["notif_keys"], "delta keys list is empty"

        # Unsubscribe round-trips (§6.4 sentinel).
        assert sub["unsubscribe_ok"], (
            "Unsubscribe (endpoint=0, feature=0 sentinel) did not succeed"
        )
    finally:
        await service.stop()
