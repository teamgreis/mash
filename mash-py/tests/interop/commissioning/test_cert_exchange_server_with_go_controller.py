"""Phase 14b interop: a Python device-side commissioning stack
(``PASEServer`` + ``CertExchangeServer``) accepts a live Go
controller's full commissioning conversation.

This is the milestone bidirectional acceptance: with Phase 14 + Phase 14b
shipped, mash-py is the first non-Go MASH implementation that can be
commissioned end-to-end by a Go controller. After this, the Python
device persists an operational cert + key signed by the Go-supplied
Zone CA — exactly the artifacts Phase 15 (operational server) needs.

Why a focused helper instead of `mash-controller -auto-commission`:
mash-controller couples PASE+cert-install to mDNS discovery (Phase 18)
and to its own cert-store layer. The ``run_commission_client`` helper
exercises only the wire flow — TLS dial, PASE, cert install — without
the discovery / persistence baggage.

Acceptance:

- Server's ``PASEServer.shared_secret`` matches helper's K_main hex.
- Server's ``CertExchangeServerResult.operational_cert`` DER matches
  the helper's signed-cert DER (byte-equal).
- Server's ``operational_key`` produces a public key matching the
  cert's ``SubjectPublicKeyInfo``.
- Server's ``zone_ca_cert`` DER matches the helper's CA DER.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import shutil
import socket
from pathlib import Path

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec

from mash_py.commissioning import (
    CertExchangeServer,
    PASEServer,
    generate_verifier,
)
from mash_py.crypto.certs import generate_commissioning_cert
from mash_py.transport.tls import make_commissioning_context

REPO_ROOT = Path(__file__).resolve().parents[4]
HELPER_DIR = REPO_ROOT / "mash-py" / "tools" / "run_commission_client"


pytestmark = pytest.mark.interop


def _go_available() -> bool:
    return shutil.which("go") is not None


def _free_port() -> int:
    """Bind a kernel-assigned port and immediately release it."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


async def _run_go_helper(
    *,
    target: str,
    setup_code: str,
    discriminator: int,
    zone_name: str = "phase14b-test",
    zone_type: int = 2,
    zone_priority: int = 1,
    timeout_s: float = 60.0,
) -> tuple[int, bytes, bytes]:
    """Spawn ``go run ./run_commission_client`` and capture
    ``(returncode, stdout, stderr)``.

    Uses ``create_subprocess_exec`` (no shell — argv-style invocation)
    so flag values pass through to the helper without parsing by any
    intermediate shell. Cwd is the helper's tools/ root so its
    ``go.mod`` ``replace`` directive picks up the local mash-go
    checkout (same pattern as ``test_pase_server_with_go_controller.py``).
    """
    proc = await asyncio.create_subprocess_exec(
        "go",
        "run",
        "./run_commission_client",
        "--target",
        target,
        "--setup-code",
        setup_code,
        "--discriminator",
        str(discriminator),
        "--zone-name",
        zone_name,
        "--zone-type",
        str(zone_type),
        "--zone-priority",
        str(zone_priority),
        "--timeout",
        str(timeout_s),
        cwd=str(HELPER_DIR.parent),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    try:
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(), timeout=timeout_s + 30.0
        )
    except TimeoutError:
        proc.kill()
        await proc.wait()
        raise
    return proc.returncode or 0, stdout, stderr


@pytest.mark.skipif(not _go_available(), reason="go toolchain not available")
@pytest.mark.slow
@pytest.mark.timeout(180)
async def test_python_device_accepts_full_go_commissioning() -> None:
    """Acceptance gate for Phase 14b.

    Sequence:
      1. Generate a self-signed commissioning cert + matching SSLContext.
      2. Stand up an asyncio TLS listener; the accept handler runs
         ``PASEServer`` then ``CertExchangeServer`` on the same connection.
      3. Spawn the Go ``run_commission_client`` helper against the listener.
      4. Assert: helper exits 0, server returns a populated
         ``CertExchangeServerResult``, op-cert DER and Zone CA DER
         match what the helper signed and shipped.
    """
    setup_code = "20202021"
    discriminator = 3840

    device_key = ec.generate_private_key(ec.SECP256R1())
    device_cert = generate_commissioning_cert(device_key, str(discriminator))
    ssl_ctx = make_commissioning_context(device_cert, device_key)

    verifier = generate_verifier(setup_code, discriminator)

    pase_secret: bytes | None = None
    cert_result_holder: list = []
    handshake_error: BaseException | None = None
    handshake_done = asyncio.Event()

    async def handle_client(
        reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        nonlocal pase_secret, handshake_error
        try:
            pase_server = PASEServer(verifier)
            pase_secret = await pase_server.handshake(reader, writer)

            cert_server = CertExchangeServer()
            cert_result_holder.append(await cert_server.run(reader, writer))
        except BaseException as exc:
            handshake_error = exc
        finally:
            handshake_done.set()
            writer.close()
            with contextlib.suppress(ConnectionError, OSError):
                await writer.wait_closed()

    port = _free_port()
    listener = await asyncio.start_server(
        handle_client, host="127.0.0.1", port=port, ssl=ssl_ctx
    )

    try:
        rc, stdout, stderr = await _run_go_helper(
            target=f"127.0.0.1:{port}",
            setup_code=setup_code,
            discriminator=discriminator,
            zone_name="phase14b-acceptance",
            zone_type=2,
            zone_priority=1,
        )
        if rc != 0:
            pytest.fail(
                f"helper exit {rc}\n"
                f"stdout: {stdout.decode('utf-8', errors='replace')}\n"
                f"stderr: {stderr.decode('utf-8', errors='replace')}",
                pytrace=False,
            )

        await asyncio.wait_for(handshake_done.wait(), timeout=10.0)

        if handshake_error is not None:
            raise AssertionError(
                f"server-side commissioning failed: {handshake_error!r}"
            )
        assert pase_secret is not None
        assert len(pase_secret) == 32
        assert cert_result_holder, "server did not produce a cert-exchange result"

        cert_result = cert_result_holder[0]
        helper_out = json.loads(stdout.decode("utf-8"))

        assert helper_out["k_main_hex"] == pase_secret.hex(), (
            "PASE K_main mismatch — SPAKE2+ transcript divergence"
        )

        srv_op_cert_der = cert_result.operational_cert.public_bytes(
            serialization.Encoding.DER
        )
        assert helper_out["operational_cert_der_hex"] == srv_op_cert_der.hex(), (
            "operational cert DER mismatch — Python server installed "
            "different bytes than the Go controller signed"
        )

        srv_ca_der = cert_result.zone_ca_cert.public_bytes(
            serialization.Encoding.DER
        )
        assert helper_out["zone_ca_der_hex"] == srv_ca_der.hex()

        op_pub_der = cert_result.operational_cert.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        key_pub_der = cert_result.operational_key.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        assert op_pub_der == key_pub_der

        assert cert_result.zone_type == 2
        assert cert_result.zone_priority == 1

        # Sanity: the Go-signed cert chains to the Go-supplied CA on
        # the Python side too. If this raises, something deeper than
        # this test usually surfaces is wrong.
        ca = cert_result.zone_ca_cert
        op = cert_result.operational_cert
        ca_pub = ca.public_key()
        ca_pub.verify(
            op.signature,
            op.tbs_certificate_bytes,
            ec.ECDSA(op.signature_hash_algorithm),
        )

        cn = op.subject.get_attributes_for_oid(x509.NameOID.COMMON_NAME)
        assert cn, "operational cert is missing a CommonName"
        assert cn[0].value == helper_out["device_id"], (
            f"device ID mismatch: cert CN={cn[0].value!r} vs helper "
            f"derived={helper_out['device_id']!r}"
        )
    finally:
        listener.close()
        await listener.wait_closed()
