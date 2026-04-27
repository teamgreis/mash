"""Unit tests for Phase 9 cert install (DEC-085).

Three surfaces:

1. **Encode/decode round-trip per MsgType.** Each new dataclass
   (``CSRRequest``, ``CSRResponse``, ``CertInstall``,
   ``CertInstallResponse``, ``CommissioningComplete``) is driven through
   ``encode → decode`` and the resulting CBOR map is checked for the
   spec's integer keys + field values. This is the byte-level contract
   that has to match mash-go's emit on the wire.

2. **Cert-store sanity.** Zone CA generation produces a CA cert whose
   subject becomes the issuer of every signed operational cert; signed
   chains validate via :func:`verify_cert_chain`; the device ID embedded
   in the operational cert's CN matches :func:`derive_device_id` of the
   CSR public key.

3. **CertExchangeClient state machine.** End-to-end runs of the
   five-message exchange against a stub device on the other end of an
   ``asyncio`` pipe. Covers: success path (cert + device ID returned),
   ``CSRResponse{error_code != 0}`` (CSR generation failure surfaces as
   :class:`CertInstallFailedError`), ``CertInstallResponse{error_code !=
   0}`` (install failure with no CommissioningComplete), and
   ``CommissioningError`` (DEC-063 cross-cutting rejection).
"""

from __future__ import annotations

import asyncio

import cbor2
import pytest
from cryptography import x509
from cryptography.hazmat.primitives import serialization

from mash_py.commissioning import (
    CSR_NONCE_SIZE,
    ERR_CODE_BUSY,
    ERR_CODE_CERT_INSTALL_FAILED,
    ERR_CODE_CSR_FAILED,
    ERR_CODE_SUCCESS,
    ERR_CODE_ZONE_TYPE_EXISTS,
    MSG_CERT_INSTALL,
    MSG_CERT_INSTALL_RESPONSE,
    MSG_COMMISSIONING_COMPLETE,
    MSG_CSR_REQUEST,
    MSG_CSR_RESPONSE,
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CommissioningError,
    CSRRequest,
    CSRResponse,
    InvalidMessageError,
    decode_message,
    encode_message,
    peek_msg_type,
)
from mash_py.commissioning.cert_exchange import (
    CertExchangeClient,
    CertExchangeProtocolError,
    CertInstallFailedError,
    CommissioningRejectedError,
)
from mash_py.commissioning.cert_store import (
    ZoneCA,
    ZoneType,
    generate_zone_ca,
    sign_device_csr,
)
from mash_py.crypto.certs import (
    create_csr,
    derive_device_id,
    generate_ecdsa_p256_keypair,
    verify_cert_chain,
)
from mash_py.wire.framing import AsyncFrameReader, AsyncFrameWriter


# ---------------------------------------------------------------------------
# Section 1: encode/decode round-trip
# ---------------------------------------------------------------------------


class TestCSRRequestRoundTrip:
    def test_round_trip_preserves_nonce(self) -> None:
        nonce = bytes(range(32))
        msg = CSRRequest(nonce=nonce)
        data = encode_message(msg)

        # Wire shape: {1: 10, 2: <bytes[32]>}
        decoded_map = cbor2.loads(data)
        assert decoded_map == {1: MSG_CSR_REQUEST, 2: nonce}

        decoded = decode_message(data)
        assert decoded == CSRRequest(nonce=nonce)

    def test_nonce_must_be_32_bytes(self) -> None:
        with pytest.raises(InvalidMessageError):
            encode_message(CSRRequest(nonce=b"\x00" * 31))

    def test_nonce_size_is_32(self) -> None:
        # The constant is part of the spec — pin it so an accidental
        # change doesn't silently regress the wire format.
        assert CSR_NONCE_SIZE == 32

    def test_decode_rejects_short_nonce(self) -> None:
        bad = cbor2.dumps({1: MSG_CSR_REQUEST, 2: b"\x00" * 16})
        with pytest.raises(InvalidMessageError):
            decode_message(bad)


class TestCSRResponseRoundTrip:
    def test_success_round_trip(self) -> None:
        csr_bytes = b"PKCS10-DER-here"
        msg = CSRResponse(csr=csr_bytes, error_code=ERR_CODE_SUCCESS)
        data = encode_message(msg)

        decoded_map = cbor2.loads(data)
        assert decoded_map == {1: MSG_CSR_RESPONSE, 2: csr_bytes, 3: 0}

        assert decode_message(data) == msg

    def test_failure_with_empty_csr_decodes(self) -> None:
        # On the failure path, csr MAY be empty — verify the dataclass
        # accepts it but the decoder still requires the field.
        msg = CSRResponse(csr=b"", error_code=ERR_CODE_CSR_FAILED)
        data = encode_message(msg)
        with pytest.raises(InvalidMessageError):
            # Decoder rejects empty csr only on success path; ensure that
            # gating works correctly. For non-zero error_code, empty is OK.
            # (Build a frame with success + empty to confirm rejection.)
            decode_message(
                cbor2.dumps({1: MSG_CSR_RESPONSE, 2: b"", 3: ERR_CODE_SUCCESS})
            )
        # The genuine failure case round-trips cleanly:
        assert decode_message(data) == msg


class TestCertInstallRoundTrip:
    def test_round_trip_preserves_all_fields(self) -> None:
        msg = CertInstall(
            operational_cert=b"OP-CERT-DER",
            ca_cert=b"CA-CERT-DER",
            zone_type=int(ZoneType.LOCAL),
            zone_priority=2,
        )
        data = encode_message(msg)
        decoded_map = cbor2.loads(data)
        assert decoded_map == {
            1: MSG_CERT_INSTALL,
            2: b"OP-CERT-DER",
            3: b"CA-CERT-DER",
            4: 2,  # ZoneType.LOCAL
            5: 2,
        }
        assert decode_message(data) == msg

    def test_decode_rejects_missing_zone_type(self) -> None:
        bad = cbor2.dumps(
            {
                1: MSG_CERT_INSTALL,
                2: b"OP",
                3: b"CA",
                # 4 missing
                5: 1,
            }
        )
        with pytest.raises(InvalidMessageError):
            decode_message(bad)

    def test_decode_rejects_empty_operational_cert(self) -> None:
        bad = cbor2.dumps(
            {1: MSG_CERT_INSTALL, 2: b"", 3: b"CA", 4: 2, 5: 1}
        )
        with pytest.raises(InvalidMessageError):
            decode_message(bad)


class TestCertInstallResponseRoundTrip:
    @pytest.mark.parametrize(
        "code",
        [
            ERR_CODE_SUCCESS,
            ERR_CODE_CERT_INSTALL_FAILED,
            ERR_CODE_BUSY,
            ERR_CODE_ZONE_TYPE_EXISTS,
        ],
    )
    def test_round_trip_per_error_code(self, code: int) -> None:
        msg = CertInstallResponse(error_code=code)
        data = encode_message(msg)
        assert cbor2.loads(data) == {1: MSG_CERT_INSTALL_RESPONSE, 2: code}
        assert decode_message(data) == msg


class TestCommissioningCompleteRoundTrip:
    def test_empty_payload_round_trip(self) -> None:
        msg = CommissioningComplete()
        data = encode_message(msg)
        # Wire shape is {1: 20} with no other keys.
        assert cbor2.loads(data) == {1: MSG_COMMISSIONING_COMPLETE}
        assert decode_message(data) == msg

    def test_peek_msg_type(self) -> None:
        data = encode_message(CommissioningComplete())
        assert peek_msg_type(data) == MSG_COMMISSIONING_COMPLETE


# ---------------------------------------------------------------------------
# Section 2: cert store / signing
# ---------------------------------------------------------------------------


class TestZoneCAAndSigning:
    def test_generate_zone_ca_defaults_to_local(self) -> None:
        ca = generate_zone_ca("z1")
        assert ca.zone_type == ZoneType.LOCAL
        assert ca.zone_name == "z1"
        # Self-signed: subject == issuer.
        assert ca.certificate.subject == ca.certificate.issuer

    def test_zone_ca_cert_der_is_self_describing(self) -> None:
        ca = generate_zone_ca("z2")
        # The DER bytes round-trip back to the same cert object.
        roundtripped = x509.load_der_x509_certificate(ca.cert_der)
        assert roundtripped.subject == ca.certificate.subject

    def test_sign_device_csr_produces_valid_chain(self) -> None:
        ca = generate_zone_ca("z3", ZoneType.GRID)
        device_key = generate_ecdsa_p256_keypair()
        csr = create_csr(device_key)

        op_cert, device_id = sign_device_csr(ca, csr)

        # Chain validates.
        assert verify_cert_chain(op_cert, ca.certificate)
        # Device ID is the spec-derived 16-hex-char identifier.
        assert len(device_id) == 16
        assert device_id == derive_device_id(device_key.public_key())
        # The cert's CN is the device ID (Matter pattern).
        cn = op_cert.subject.get_attributes_for_oid(
            x509.NameOID.COMMON_NAME
        )[0].value
        assert cn == device_id

    def test_sign_device_csr_rejects_non_p256(self) -> None:
        from cryptography.hazmat.primitives.asymmetric import ec

        ca = generate_zone_ca("z4")
        wrong_key = ec.generate_private_key(ec.SECP384R1())
        csr_builder = x509.CertificateSigningRequestBuilder().subject_name(
            x509.Name([x509.NameAttribute(x509.NameOID.COMMON_NAME, "x")])
        )
        # cryptography's CSR builder doesn't enforce the curve; we sign
        # with SECP384R1 and expect sign_device_csr to reject.
        from cryptography.hazmat.primitives import hashes

        bad_csr = csr_builder.sign(wrong_key, hashes.SHA256())
        with pytest.raises(ValueError, match="P-256"):
            sign_device_csr(ca, bad_csr)


# ---------------------------------------------------------------------------
# Section 3: CertExchangeClient state machine
# ---------------------------------------------------------------------------


def _make_pipe_pair() -> tuple[
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
    tuple[asyncio.StreamReader, asyncio.StreamWriter],
]:
    """Build two `(reader, writer)` halves of an in-memory full-duplex
    pipe. Each side reads what the other writes.
    """
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
    """Tiny in-memory transport that funnels writes into a peer's
    StreamReader. Just enough for AsyncFrameWriter to drain frames in
    these tests; not a full asyncio transport."""

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


async def _stub_device_success(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
    device_key,
) -> tuple[bytes, bytes]:
    """Run the device side of a happy-path cert exchange. Returns
    ``(received_nonce, received_cert_install_op_cert_der)`` so tests can
    assert on what the controller sent.
    """
    fr = AsyncFrameReader(reader)
    fw = AsyncFrameWriter(writer)

    # Read CSRRequest, capture nonce.
    csr_req = decode_message(await fr.read_frame())
    assert isinstance(csr_req, CSRRequest)
    nonce = csr_req.nonce

    # Build CSR using the supplied device key + send CSRResponse.
    csr_obj = create_csr(device_key)
    csr_der = csr_obj.public_bytes(serialization.Encoding.DER)
    await fw.write_frame(
        encode_message(CSRResponse(csr=csr_der, error_code=ERR_CODE_SUCCESS))
    )

    # Read CertInstall, capture op cert DER.
    install = decode_message(await fr.read_frame())
    assert isinstance(install, CertInstall)
    op_cert_der = install.operational_cert

    # Reply success + Complete.
    await fw.write_frame(
        encode_message(CertInstallResponse(error_code=ERR_CODE_SUCCESS))
    )
    await fw.write_frame(encode_message(CommissioningComplete()))

    return nonce, op_cert_der


async def _stub_device_csr_failure(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
) -> None:
    fr = AsyncFrameReader(reader)
    fw = AsyncFrameWriter(writer)
    decoded = decode_message(await fr.read_frame())
    assert isinstance(decoded, CSRRequest)
    await fw.write_frame(
        encode_message(CSRResponse(csr=b"", error_code=ERR_CODE_CSR_FAILED))
    )


async def _stub_device_install_failure(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
    device_key,
) -> None:
    fr = AsyncFrameReader(reader)
    fw = AsyncFrameWriter(writer)
    csr_req = decode_message(await fr.read_frame())
    assert isinstance(csr_req, CSRRequest)
    csr_obj = create_csr(device_key)
    await fw.write_frame(
        encode_message(
            CSRResponse(
                csr=csr_obj.public_bytes(serialization.Encoding.DER),
                error_code=ERR_CODE_SUCCESS,
            )
        )
    )
    install = decode_message(await fr.read_frame())
    assert isinstance(install, CertInstall)
    # Reject with ZoneTypeExists; do NOT send CommissioningComplete.
    await fw.write_frame(
        encode_message(
            CertInstallResponse(error_code=ERR_CODE_ZONE_TYPE_EXISTS)
        )
    )


async def _stub_device_busy(
    reader: asyncio.StreamReader,
    writer: asyncio.StreamWriter,
) -> None:
    fr = AsyncFrameReader(reader)
    fw = AsyncFrameWriter(writer)
    decoded = decode_message(await fr.read_frame())
    assert isinstance(decoded, CSRRequest)
    # DEC-063 cross-cutting rejection in the CSRResponse slot.
    await fw.write_frame(
        encode_message(
            CommissioningError(
                error_code=ERR_CODE_BUSY,
                message="zone slots full",
                retry_after_ms=5000,
            )
        )
    )


@pytest.mark.asyncio
async def test_cert_exchange_success_path() -> None:
    (reader_a, writer_a), (reader_b, writer_b) = _make_pipe_pair()
    device_key = generate_ecdsa_p256_keypair()

    ca = generate_zone_ca("z-success", ZoneType.LOCAL)

    device_task = asyncio.create_task(
        _stub_device_success(reader_b, writer_b, device_key)
    )
    client = CertExchangeClient(zone_ca=ca, zone_priority=1)
    result = await client.run(reader_a, writer_a)

    nonce, op_cert_der = await device_task
    assert len(nonce) == 32
    # The device received the same op cert the controller signed.
    assert (
        result.operational_cert.public_bytes(serialization.Encoding.DER)
        == op_cert_der
    )
    # The chain validates and the device ID matches the one we'd derive
    # from the device's CSR pubkey.
    assert verify_cert_chain(result.operational_cert, ca.certificate)
    assert result.device_id == derive_device_id(device_key.public_key())
    assert result.zone_id == "z-success"


@pytest.mark.asyncio
async def test_cert_exchange_csr_failure_aborts_before_install() -> None:
    (reader_a, writer_a), (reader_b, writer_b) = _make_pipe_pair()
    ca = generate_zone_ca("z-csr-fail")

    device_task = asyncio.create_task(_stub_device_csr_failure(reader_b, writer_b))
    client = CertExchangeClient(zone_ca=ca)

    with pytest.raises(CertInstallFailedError) as exc_info:
        await client.run(reader_a, writer_a)
    assert exc_info.value.step == "CSR generation"
    assert exc_info.value.error_code == ERR_CODE_CSR_FAILED

    await device_task


@pytest.mark.asyncio
async def test_cert_exchange_install_failure_does_not_wait_for_complete() -> None:
    (reader_a, writer_a), (reader_b, writer_b) = _make_pipe_pair()
    device_key = generate_ecdsa_p256_keypair()
    ca = generate_zone_ca("z-install-fail")

    device_task = asyncio.create_task(
        _stub_device_install_failure(reader_b, writer_b, device_key)
    )
    client = CertExchangeClient(zone_ca=ca)

    with pytest.raises(CertInstallFailedError) as exc_info:
        await client.run(reader_a, writer_a)
    assert exc_info.value.step == "cert install"
    assert exc_info.value.error_code == ERR_CODE_ZONE_TYPE_EXISTS

    await device_task


@pytest.mark.asyncio
async def test_cert_exchange_commissioning_error_surfaces_as_rejected() -> None:
    (reader_a, writer_a), (reader_b, writer_b) = _make_pipe_pair()
    ca = generate_zone_ca("z-busy")

    device_task = asyncio.create_task(_stub_device_busy(reader_b, writer_b))
    client = CertExchangeClient(zone_ca=ca)

    with pytest.raises(CommissioningRejectedError) as exc_info:
        await client.run(reader_a, writer_a)
    assert exc_info.value.error_code == ERR_CODE_BUSY
    assert exc_info.value.retry_after_ms == 5000

    await device_task


@pytest.mark.asyncio
async def test_cert_exchange_protocol_error_on_unexpected_message() -> None:
    (reader_a, writer_a), (reader_b, writer_b) = _make_pipe_pair()
    ca = generate_zone_ca("z-proto")

    async def evil_device() -> None:
        fr = AsyncFrameReader(reader_b)
        fw = AsyncFrameWriter(writer_b)
        # Read CSRRequest, then send a CertInstallResponse instead of CSRResponse.
        decoded = decode_message(await fr.read_frame())
        assert isinstance(decoded, CSRRequest)
        await fw.write_frame(
            encode_message(CertInstallResponse(error_code=ERR_CODE_SUCCESS))
        )

    device_task = asyncio.create_task(evil_device())
    client = CertExchangeClient(zone_ca=ca)
    with pytest.raises(CertExchangeProtocolError):
        await client.run(reader_a, writer_a)
    await device_task
