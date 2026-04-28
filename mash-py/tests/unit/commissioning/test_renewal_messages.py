"""Unit tests for ``mash_py.commissioning.renewal_messages``.

Covers:

- Round-trip encode → decode for every renewal MsgType (30/31/32/33).
- Nonce-hash semantics: SHA256 truncation to 16 bytes; constant-time
  validate; rejection of wrong-length / wrong-content hashes.
- Strict-decode behaviour: missing ``nonce_hash`` rejected; wrong byte
  lengths rejected; non-uint8 status rejected; empty CSR/cert rejected.
- Frame-routing helpers ``peek_msg_type`` / ``peek_is_renewal_frame``,
  including the structural disambiguation (no key 4) used to route
  renewal vs data-plane frames on the operational ALPN.

Spec sources: ``docs/security.md §11.3``, ``docs/message-types.md §4``,
``docs/stack-architecture.md §11``.
"""

from __future__ import annotations

import cbor2
import pytest

from mash_py.commissioning.renewal_messages import (
    MSG_CERT_RENEWAL_ACK,
    MSG_CERT_RENEWAL_CSR,
    MSG_CERT_RENEWAL_INSTALL,
    MSG_CERT_RENEWAL_REQUEST,
    NONCE_HASH_SIZE,
    NONCE_SIZE,
    RENEWAL_STATUS_CSR_FAILED,
    RENEWAL_STATUS_INSTALL_FAILED,
    RENEWAL_STATUS_INVALID_CERT,
    RENEWAL_STATUS_INVALID_NONCE,
    RENEWAL_STATUS_SUCCESS,
    CertRenewalAck,
    CertRenewalCSR,
    CertRenewalInstall,
    CertRenewalRequest,
    InvalidRenewalMessageError,
    compute_nonce_hash,
    decode,
    encode,
    peek_is_renewal_frame,
    peek_msg_type,
    renewal_status_string,
    validate_nonce_hash,
)


# ---------------------------------------------------------------------------
# Nonce hash primitives
# ---------------------------------------------------------------------------


def _det_nonce(seed: int = 0) -> bytes:
    return bytes((seed + i) & 0xFF for i in range(NONCE_SIZE))


def test_compute_nonce_hash_truncates_to_16_bytes():
    nonce = _det_nonce()
    h = compute_nonce_hash(nonce)
    assert len(h) == NONCE_HASH_SIZE == 16
    # Spec: SHA256(nonce)[:16]
    import hashlib

    expected = hashlib.sha256(nonce).digest()[:16]
    assert h == expected


def test_compute_nonce_hash_is_deterministic_per_input():
    nonce = _det_nonce()
    assert compute_nonce_hash(nonce) == compute_nonce_hash(nonce)


def test_compute_nonce_hash_changes_with_input():
    a = compute_nonce_hash(_det_nonce(0))
    b = compute_nonce_hash(_det_nonce(1))
    assert a != b


def test_validate_nonce_hash_accepts_correct_hash():
    nonce = _det_nonce()
    assert validate_nonce_hash(nonce, compute_nonce_hash(nonce)) is True


def test_validate_nonce_hash_rejects_wrong_hash():
    nonce = _det_nonce()
    bad = bytearray(compute_nonce_hash(nonce))
    bad[0] ^= 0xFF
    assert validate_nonce_hash(nonce, bytes(bad)) is False


def test_validate_nonce_hash_rejects_wrong_length():
    nonce = _det_nonce()
    h = compute_nonce_hash(nonce)
    assert validate_nonce_hash(nonce, h[:8]) is False
    assert validate_nonce_hash(nonce, h + b"\x00") is False


def test_validate_nonce_hash_rejects_non_bytes():
    nonce = _det_nonce()
    assert validate_nonce_hash(nonce, None) is False  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# Round-trip per MsgType
# ---------------------------------------------------------------------------


def test_request_roundtrip():
    msg = CertRenewalRequest(nonce=_det_nonce())
    blob = encode(msg)

    raw = cbor2.loads(blob)
    assert isinstance(raw, dict)
    assert raw[1] == MSG_CERT_RENEWAL_REQUEST
    assert raw[2] == _det_nonce()
    # Spec: only fields 1+2 on this envelope (no Zone CA).
    assert set(raw.keys()) == {1, 2}

    decoded = decode(blob)
    assert decoded == msg


def test_csr_roundtrip():
    nonce = _det_nonce()
    msg = CertRenewalCSR(
        csr=b"\x30\x82\x01\x00\xDE\xAD\xBE\xEF",
        nonce_hash=compute_nonce_hash(nonce),
    )
    blob = encode(msg)

    raw = cbor2.loads(blob)
    assert raw[1] == MSG_CERT_RENEWAL_CSR
    assert raw[2] == msg.csr
    assert raw[3] == msg.nonce_hash
    assert len(raw[3]) == NONCE_HASH_SIZE

    decoded = decode(blob)
    assert decoded == msg


def test_install_roundtrip():
    msg = CertRenewalInstall(new_cert=b"\x30\x82\x02\x00", sequence=42)
    blob = encode(msg)

    raw = cbor2.loads(blob)
    assert raw[1] == MSG_CERT_RENEWAL_INSTALL
    assert raw[2] == msg.new_cert
    assert raw[3] == 42

    decoded = decode(blob)
    assert decoded == msg


def test_ack_roundtrip():
    msg = CertRenewalAck(status=RENEWAL_STATUS_SUCCESS, active_sequence=42)
    blob = encode(msg)

    raw = cbor2.loads(blob)
    assert raw[1] == MSG_CERT_RENEWAL_ACK
    assert raw[2] == 0
    assert raw[3] == 42

    decoded = decode(blob)
    assert decoded == msg


@pytest.mark.parametrize(
    "code,name",
    [
        (RENEWAL_STATUS_SUCCESS, "success"),
        (RENEWAL_STATUS_CSR_FAILED, "CSR generation failed"),
        (RENEWAL_STATUS_INSTALL_FAILED, "certificate install failed"),
        (RENEWAL_STATUS_INVALID_CERT, "invalid replacement certificate"),
        (RENEWAL_STATUS_INVALID_NONCE, "nonce binding validation failed"),
    ],
)
def test_renewal_status_string_known_codes(code, name):
    assert renewal_status_string(code) == name


def test_renewal_status_string_unknown_code():
    assert "unknown" in renewal_status_string(99)


@pytest.mark.parametrize(
    "code",
    [
        RENEWAL_STATUS_CSR_FAILED,
        RENEWAL_STATUS_INSTALL_FAILED,
        RENEWAL_STATUS_INVALID_CERT,
        RENEWAL_STATUS_INVALID_NONCE,
    ],
)
def test_ack_failure_codes_roundtrip(code):
    msg = CertRenewalAck(status=code, active_sequence=7)
    decoded = decode(encode(msg))
    assert decoded == msg


# ---------------------------------------------------------------------------
# Strict-decode rejections
# ---------------------------------------------------------------------------


def test_request_rejects_short_nonce():
    blob = cbor2.dumps({1: MSG_CERT_RENEWAL_REQUEST, 2: b"\x00" * 16}, canonical=True)
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_request_rejects_long_nonce():
    blob = cbor2.dumps({1: MSG_CERT_RENEWAL_REQUEST, 2: b"\x00" * 64}, canonical=True)
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_csr_rejects_missing_nonce_hash():
    """Spec §11.3: NonceHash is required. Wire shape MUST include field 3."""
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_CSR, 2: b"\x30\x82\x01"}, canonical=True
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_csr_rejects_wrong_length_nonce_hash():
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_CSR, 2: b"\x30\x82", 3: b"\x00" * 8},
        canonical=True,
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_csr_rejects_empty_csr():
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_CSR, 2: b"", 3: b"\x00" * NONCE_HASH_SIZE},
        canonical=True,
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_install_rejects_empty_cert():
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_INSTALL, 2: b"", 3: 1}, canonical=True
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_install_rejects_negative_sequence():
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_INSTALL, 2: b"\x30\x82", 3: -1}, canonical=True
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_ack_rejects_status_out_of_uint8():
    blob = cbor2.dumps(
        {1: MSG_CERT_RENEWAL_ACK, 2: 256, 3: 1}, canonical=True
    )
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_decode_rejects_unknown_msg_type():
    blob = cbor2.dumps({1: 99}, canonical=True)
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_decode_rejects_non_map():
    blob = cbor2.dumps([1, 2, 3], canonical=True)
    with pytest.raises(InvalidRenewalMessageError):
        decode(blob)


def test_encode_rejects_unknown_dataclass():
    class NotARenewalMessage:
        pass

    with pytest.raises(InvalidRenewalMessageError):
        encode(NotARenewalMessage())  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# Frame-routing helpers
# ---------------------------------------------------------------------------


def test_peek_msg_type_returns_int():
    blob = encode(CertRenewalRequest(nonce=_det_nonce()))
    assert peek_msg_type(blob) == MSG_CERT_RENEWAL_REQUEST


def test_peek_msg_type_rejects_non_map():
    blob = cbor2.dumps([1, 2, 3], canonical=True)
    with pytest.raises(InvalidRenewalMessageError):
        peek_msg_type(blob)


def test_peek_is_renewal_frame_accepts_all_renewal_msg_types():
    for mt in (
        MSG_CERT_RENEWAL_REQUEST,
        MSG_CERT_RENEWAL_CSR,
        MSG_CERT_RENEWAL_INSTALL,
        MSG_CERT_RENEWAL_ACK,
    ):
        # Minimal CBOR map with just the msgType — the routing helper does
        # not validate full payload shape (decoder does that).
        blob = cbor2.dumps({1: mt}, canonical=True)
        assert peek_is_renewal_frame(blob) is True


def test_peek_is_renewal_frame_rejects_data_plane_request_with_msg_id_in_renewal_range():
    """Data-plane Request with messageId == 30 + featureId set: must not be misrouted."""
    # data-plane Request shape per docs/interaction-model.md §3:
    # { 1: messageId, 2: operation, 3: endpoint, 4: feature, 5: payload }
    blob = cbor2.dumps(
        {1: 30, 2: 1, 3: 1, 4: 0x05, 5: {}}, canonical=True
    )
    assert peek_is_renewal_frame(blob) is False


def test_peek_is_renewal_frame_rejects_msg_type_outside_30_33():
    blob = cbor2.dumps({1: 29}, canonical=True)
    assert peek_is_renewal_frame(blob) is False
    blob = cbor2.dumps({1: 34}, canonical=True)
    assert peek_is_renewal_frame(blob) is False


def test_peek_is_renewal_frame_rejects_garbage():
    assert peek_is_renewal_frame(b"") is False
    assert peek_is_renewal_frame(b"\xff\xff") is False
    # Non-map CBOR
    assert peek_is_renewal_frame(cbor2.dumps([1, 2], canonical=True)) is False
