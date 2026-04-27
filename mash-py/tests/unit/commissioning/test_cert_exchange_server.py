"""Unit tests for ``mash_py.commissioning.cert_exchange_server`` (Phase 14b).

Round-trip: a Python :class:`CertExchangeServer` accepts a cert install
driven by the existing :class:`CertExchangeClient` (Phase 9) and both
sides hold matching operational-cert public keys. Negative cases
exercise the spec-strict failure paths the eventual interop test against
the Go controller helper will rely on.

Spec references woven through the tests:

- §2.5 — point validation; not directly tested here (PASE path).
- §3.1 — state-machine transitions PASE_VERIFIED → CSR_GENERATE →
  CERT_WAIT → CERT_INSTALL → COMPLETE.
- §4.3 — message wire formats; in particular ``CSRRequest.nonce`` MUST
  be 32 bytes.
- §6.5 / DEC-047 — failure-envelope rule
  (``CertInstallResponse{error_code != 0}``, never
  ``CommissioningError`` from inside cert exchange).
"""

from __future__ import annotations

import asyncio

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec

from mash_py.commissioning import (
    CSR_NONCE_SIZE,
    ERR_CODE_BUSY,
    ERR_CODE_CERT_INSTALL_FAILED,
    ERR_CODE_ZONE_TYPE_EXISTS,
    CertExchangeServer,
    CertExchangeServerError,
    CertExchangeServerInstallError,
    CertExchangeServerProtocolError,
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CSRRequest,
    CSRResponse,
    ZoneRejectedError,
    decode_message,
    encode_message,
)
from mash_py.commissioning.cert_exchange import CertExchangeClient
from mash_py.commissioning.cert_exchange_server import CertExchangePhaseTimeoutError
from mash_py.commissioning.cert_store import ZoneCA, ZoneType, generate_zone_ca
from mash_py.crypto.certs import sign_csr
from mash_py.wire.framing import AsyncFrameReader, AsyncFrameWriter

# ---------------------------------------------------------------------------
# Harness helpers
# ---------------------------------------------------------------------------


def _make_pipe_pair() -> tuple[
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
]:
    reader_a = asyncio.StreamReader()
    proto_a = asyncio.StreamReaderProtocol(reader_a)
    reader_b = asyncio.StreamReader()
    proto_b = asyncio.StreamReaderProtocol(reader_b)

    transport_a_to_b = _MemoryTransport(reader_b)
    transport_b_to_a = _MemoryTransport(reader_a)

    writer_a = asyncio.StreamWriter(
        transport_a_to_b, proto_a, reader_a, asyncio.get_event_loop()
    )
    writer_b = asyncio.StreamWriter(
        transport_b_to_a, proto_b, reader_b, asyncio.get_event_loop()
    )
    return (reader_a, writer_a), (reader_b, writer_b)


class _MemoryTransport(asyncio.WriteTransport):
    def __init__(self, peer_reader: asyncio.StreamReader) -> None:
        super().__init__()
        self._peer_reader = peer_reader
        self._closed = False

    def write(self, data: bytes) -> None:
        if self._closed:
            raise ConnectionResetError("transport closed")
        self._peer_reader.feed_data(data)

    def close(self) -> None:
        if not self._closed:
            self._closed = True
            self._peer_reader.feed_eof()

    def is_closing(self) -> bool:
        return self._closed

    def get_extra_info(self, name: str, default=None):  # type: ignore[no-untyped-def]
        return default


@pytest.fixture
def zone_ca() -> ZoneCA:
    """Fresh Zone CA per test (deterministic but non-overlapping)."""
    return generate_zone_ca("phase14b-test-zone", zone_type=ZoneType.LOCAL)


# ---------------------------------------------------------------------------
# Round-trip — CertExchangeClient ↔ CertExchangeServer (load-bearing happy path)
# ---------------------------------------------------------------------------


async def test_cert_install_round_trip_returns_matching_op_cert(
    zone_ca: ZoneCA,
) -> None:
    """Client signs the CSR; server installs the resulting op cert.
    Server-side public key MUST match the public key the client signed."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    client = CertExchangeClient(zone_ca, zone_priority=1)
    server = CertExchangeServer()

    cli_task = asyncio.create_task(client.run(cli_r, cli_w))
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))
    cli_result, srv_result = await asyncio.gather(cli_task, srv_task)

    # Both sides hold the same operational cert (byte-equal DER).
    cli_cert_der = cli_result.operational_cert.public_bytes(
        serialization.Encoding.DER
    )
    srv_cert_der = srv_result.operational_cert.public_bytes(
        serialization.Encoding.DER
    )
    assert cli_cert_der == srv_cert_der

    # Server's persisted private key matches the cert's public key
    # (the public-key binding check passed).
    op_pub_der = srv_result.operational_cert.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    key_pub_der = srv_result.operational_key.public_key().public_bytes(
        encoding=serialization.Encoding.DER,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    assert op_pub_der == key_pub_der

    # Zone CA round-trips byte-equal.
    srv_ca_der = srv_result.zone_ca_cert.public_bytes(
        serialization.Encoding.DER
    )
    assert srv_ca_der == zone_ca.cert_der

    assert srv_result.zone_type == int(zone_ca.zone_type)
    assert srv_result.zone_priority == 1


async def test_result_unavailable_until_completion() -> None:
    """``result`` raises before ``run`` succeeds — defensive guard."""
    server = CertExchangeServer()
    with pytest.raises(CertExchangeServerError):
        _ = server.result


# ---------------------------------------------------------------------------
# Failure path: chain mismatch (controller signs with the wrong CA)
# ---------------------------------------------------------------------------


async def test_chain_mismatch_emits_install_failed(zone_ca: ZoneCA) -> None:
    """A controller installs an op cert NOT signed by the CA it claims.
    Server emits ``CertInstallResponse{ErrCodeCertInstallFailed}`` and
    raises :class:`CertExchangeServerInstallError`."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    # Pre-build a "fake" op-cert signed by a DIFFERENT Zone CA.
    rogue_ca = generate_zone_ca("rogue-ca", zone_type=ZoneType.LOCAL)

    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    # Drive the wire by hand from the client side.
    fr = AsyncFrameReader(cli_r)
    fw = AsyncFrameWriter(cli_w)

    # 1) Send CSRRequest.
    nonce = b"\x00" * CSR_NONCE_SIZE
    await fw.write_frame(encode_message(CSRRequest(nonce=nonce)))

    # 2) Receive CSRResponse, parse the device's CSR.
    msg = decode_message(await fr.read_frame())
    assert isinstance(msg, CSRResponse)
    csr = x509.load_der_x509_csr(msg.csr)

    # 3) Sign the CSR with the ROGUE CA, then send CertInstall claiming
    # the LEGITIMATE zone_ca as the trust anchor. This is the attack
    # we're testing: chain doesn't validate.
    rogue_op_cert = sign_csr(
        rogue_ca.certificate,
        rogue_ca.private_key,
        csr,
        zone_name=rogue_ca.zone_name,
        device_id="ROGUE001",
    )
    rogue_op_der = rogue_op_cert.public_bytes(serialization.Encoding.DER)
    await fw.write_frame(
        encode_message(
            CertInstall(
                operational_cert=rogue_op_der,
                ca_cert=zone_ca.cert_der,  # legit CA — chain check fails
                zone_type=int(zone_ca.zone_type),
                zone_priority=1,
            )
        )
    )

    # 4) Server should respond with CertInstallResponse{ErrCodeCertInstallFailed}
    # and raise CertExchangeServerInstallError.
    reply = decode_message(await fr.read_frame())
    assert isinstance(reply, CertInstallResponse)
    assert reply.error_code == ERR_CODE_CERT_INSTALL_FAILED

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    assert excinfo.value.error_code == ERR_CODE_CERT_INSTALL_FAILED
    assert "chain validation" in excinfo.value.reason


# ---------------------------------------------------------------------------
# Failure path: public-key binding mismatch (cert for someone else's key)
# ---------------------------------------------------------------------------


async def test_public_key_mismatch_emits_install_failed(
    zone_ca: ZoneCA,
) -> None:
    """A controller installs a cert whose public key does NOT match the
    device's CSR. Server detects via the public-key binding check and
    raises ``CertInstallFailed``. Without this check, a malicious
    controller could install a cert for a key the device doesn't hold,
    leaving the cert unusable and the device unable to operate."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    fr = AsyncFrameReader(cli_r)
    fw = AsyncFrameWriter(cli_w)

    # 1) CSRRequest.
    await fw.write_frame(encode_message(CSRRequest(nonce=b"\x00" * CSR_NONCE_SIZE)))

    # 2) Read device's CSR, then **discard it** — we'll sign a cert for
    # a different (attacker-controlled) key pair instead of for the
    # CSR's pubkey.
    msg = decode_message(await fr.read_frame())
    assert isinstance(msg, CSRResponse)

    attacker_key = ec.generate_private_key(ec.SECP256R1())
    # Build a fresh CSR for the attacker key, then sign that.
    from mash_py.crypto.certs import create_csr

    attacker_csr = create_csr(attacker_key)
    attacker_cert = sign_csr(
        zone_ca.certificate,
        zone_ca.private_key,
        attacker_csr,
        zone_name=zone_ca.zone_name,
        device_id="EVIL0001",
    )
    attacker_der = attacker_cert.public_bytes(serialization.Encoding.DER)

    # 3) Send CertInstall with the attacker's cert — chain validates
    # (signed by legit CA) but pubkey doesn't match the device's CSR.
    await fw.write_frame(
        encode_message(
            CertInstall(
                operational_cert=attacker_der,
                ca_cert=zone_ca.cert_der,
                zone_type=int(zone_ca.zone_type),
                zone_priority=1,
            )
        )
    )

    reply = decode_message(await fr.read_frame())
    assert isinstance(reply, CertInstallResponse)
    assert reply.error_code == ERR_CODE_CERT_INSTALL_FAILED

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    assert excinfo.value.error_code == ERR_CODE_CERT_INSTALL_FAILED
    assert "public key" in excinfo.value.reason


# ---------------------------------------------------------------------------
# Failure path: malformed cert DER
# ---------------------------------------------------------------------------


async def test_malformed_cert_emits_install_failed(zone_ca: ZoneCA) -> None:
    """``CertInstall.operational_cert`` is non-DER garbage. Server emits
    ``CertInstallResponse{ErrCodeCertInstallFailed}``."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    fr = AsyncFrameReader(cli_r)
    fw = AsyncFrameWriter(cli_w)

    await fw.write_frame(encode_message(CSRRequest(nonce=b"\x00" * CSR_NONCE_SIZE)))
    msg = decode_message(await fr.read_frame())
    assert isinstance(msg, CSRResponse)

    # operational_cert is non-DER but len > 0 (passes the message-level
    # decoder's "non-empty" check); falls into the parse path.
    await fw.write_frame(
        encode_message(
            CertInstall(
                operational_cert=b"\xff\xfe\xfd not a cert",
                ca_cert=zone_ca.cert_der,
                zone_type=int(zone_ca.zone_type),
                zone_priority=1,
            )
        )
    )

    reply = decode_message(await fr.read_frame())
    assert isinstance(reply, CertInstallResponse)
    assert reply.error_code == ERR_CODE_CERT_INSTALL_FAILED

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    assert "parse" in excinfo.value.reason


# ---------------------------------------------------------------------------
# Failure path: validate_zone callback rejects
# ---------------------------------------------------------------------------


async def test_validate_zone_rejects_zone_type_exists(
    zone_ca: ZoneCA,
) -> None:
    """Caller's ``validate_zone`` raises ``ZoneRejectedError`` with
    ``ERR_CODE_ZONE_TYPE_EXISTS`` (DEC-043: only one zone per type).
    Server emits the matching wire code."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    def reject_local_zone(zone_type: int, ca: x509.Certificate) -> None:
        if zone_type == int(ZoneType.LOCAL):
            raise ZoneRejectedError(
                "LOCAL zone already exists", ERR_CODE_ZONE_TYPE_EXISTS
            )

    client = CertExchangeClient(zone_ca, zone_priority=1)
    server = CertExchangeServer(validate_zone=reject_local_zone)

    cli_task = asyncio.create_task(client.run(cli_r, cli_w))
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    assert excinfo.value.error_code == ERR_CODE_ZONE_TYPE_EXISTS

    # Client surfaces a CertInstallFailedError carrying the same code
    # (Phase 9's existing contract).
    with pytest.raises(Exception) as cli_exc:
        await cli_task
    # Match Phase 9's typed surface either via attribute or message.
    assert "10" in str(cli_exc.value) or "ZoneTypeExists" in str(cli_exc.value)


async def test_validate_zone_async_callback_supported(
    zone_ca: ZoneCA,
) -> None:
    """``validate_zone`` may be an async callable. The state machine
    awaits it; rejection bubbles the same way as a sync callback."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    async def async_reject(zone_type: int, ca: x509.Certificate) -> None:
        await asyncio.sleep(0)  # actually yield to the loop
        raise ZoneRejectedError("slots full", ERR_CODE_BUSY)

    client = CertExchangeClient(zone_ca, zone_priority=1)
    server = CertExchangeServer(validate_zone=async_reject)

    cli_task = asyncio.create_task(client.run(cli_r, cli_w))
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    assert excinfo.value.error_code == ERR_CODE_BUSY

    # Drain client side cleanly.
    import contextlib

    with contextlib.suppress(Exception):
        await cli_task


async def test_validate_zone_unknown_code_falls_back_to_install_failed(
    zone_ca: ZoneCA,
) -> None:
    """``ZoneRejectedError`` with an out-of-set error code is mapped to
    ``ERR_CODE_CERT_INSTALL_FAILED`` so the wire never carries an
    undefined code (defensive — keeps the failure surface in §6.5's
    closed set)."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()

    def reject_with_garbage(zone_type: int, ca: x509.Certificate) -> None:
        raise ZoneRejectedError("garbage code", 200)  # not in spec set

    client = CertExchangeClient(zone_ca, zone_priority=1)
    server = CertExchangeServer(validate_zone=reject_with_garbage)

    cli_task = asyncio.create_task(client.run(cli_r, cli_w))
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    with pytest.raises(CertExchangeServerInstallError) as excinfo:
        await srv_task
    # Server normalised to a spec-defined code.
    assert excinfo.value.error_code == ERR_CODE_CERT_INSTALL_FAILED

    import contextlib

    with contextlib.suppress(Exception):
        await cli_task


# ---------------------------------------------------------------------------
# Failure path: protocol-level errors
# ---------------------------------------------------------------------------


async def test_first_frame_must_be_csr_request(zone_ca: ZoneCA) -> None:
    """A non-``CSRRequest`` first frame is a protocol error — no
    envelope is emitted (we never started cert exchange in the
    spec-defined sense)."""
    (_cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    fw = AsyncFrameWriter(cli_w)
    # Send a CertInstall where a CSRRequest is expected.
    await fw.write_frame(
        encode_message(
            CertInstall(
                operational_cert=b"\x00\x01",
                ca_cert=zone_ca.cert_der,
                zone_type=int(zone_ca.zone_type),
                zone_priority=1,
            )
        )
    )

    with pytest.raises(CertExchangeServerProtocolError, match="expected CSRRequest"):
        await srv_task


async def test_second_frame_must_be_cert_install(zone_ca: ZoneCA) -> None:
    """Sending a ``CommissioningComplete`` where a ``CertInstall`` is
    expected is a protocol error."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))

    fr = AsyncFrameReader(cli_r)
    fw = AsyncFrameWriter(cli_w)

    await fw.write_frame(encode_message(CSRRequest(nonce=b"\x00" * CSR_NONCE_SIZE)))
    # Drain CSRResponse so the server is now waiting on CertInstall.
    msg = decode_message(await fr.read_frame())
    assert isinstance(msg, CSRResponse)

    # Send a wrong-slot frame.
    await fw.write_frame(encode_message(CommissioningComplete()))

    with pytest.raises(CertExchangeServerProtocolError, match="expected CertInstall"):
        await srv_task


async def test_phase_timeout_raises_without_envelope(zone_ca: ZoneCA) -> None:
    """If CERT_WAIT (the wait for CertInstall) elapses, the server
    raises :class:`CertExchangePhaseTimeoutError` without emitting any
    envelope (§3.1 transitions to ADVERTISING with no error message)."""
    (cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = CertExchangeServer(phase_timeout_s=0.1)

    fr = AsyncFrameReader(cli_r)
    fw = AsyncFrameWriter(cli_w)

    srv_task = asyncio.create_task(server.run(srv_r, srv_w))
    await fw.write_frame(encode_message(CSRRequest(nonce=b"\x00" * CSR_NONCE_SIZE)))
    # Drain CSRResponse, then never send CertInstall.
    msg = decode_message(await fr.read_frame())
    assert isinstance(msg, CSRResponse)

    with pytest.raises(CertExchangePhaseTimeoutError):
        await srv_task


async def test_truncated_frame_is_protocol_error() -> None:
    """An EOF mid-handshake surfaces as :class:`ServerProtocolError`."""
    (_cli_r, cli_w), (srv_r, srv_w) = _make_pipe_pair()
    server = CertExchangeServer()
    srv_task = asyncio.create_task(server.run(srv_r, srv_w))
    cli_w.close()

    with pytest.raises(CertExchangeServerProtocolError):
        await srv_task


# ---------------------------------------------------------------------------
# Constructor validation
# ---------------------------------------------------------------------------


def test_constructor_rejects_non_positive_phase_timeout() -> None:
    with pytest.raises(ValueError, match="phase_timeout_s"):
        CertExchangeServer(phase_timeout_s=0)
    with pytest.raises(ValueError, match="phase_timeout_s"):
        CertExchangeServer(phase_timeout_s=-1)
