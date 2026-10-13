"""Cert-renewal messages — Phase 19.

CBOR-encoded messages exchanged on the operational ALPN ``mash/1`` to renew
a device's operational certificate in-session. Wire formats are normative
in ``docs/security.md §11.3`` and ``docs/message-types.md §4``.

Spec layout (each message is a CBOR map with integer keys, framed per
``transport.md §4`` — 4-byte big-endian length prefix + canonical CBOR)::

    CertRenewalRequest (MsgType 30, C → D): {1: 30, 2: nonce[32]}
    CertRenewalCSR     (MsgType 31, D → C): {1: 31, 2: csr,    3: nonceHash[16]}
    CertRenewalInstall (MsgType 32, C → D): {1: 32, 2: cert,   3: sequence}
    CertRenewalAck     (MsgType 33, D → C): {1: 33, 2: status, 3: activeSequence}

mash-py is spec-strict. Three drift points were found vs mash-go and
resolved in lockstep with the spec entry into Phase 19 (see
``mash-py-tdd-plan.md §Phase 19``):

- ``CertRenewalRequest`` carries only ``nonce`` — no ZoneCA field. The Go
  ``omitempty`` ZoneCA was a pre-DEC-085 leftover (the renewal envelope
  used to be reused for commissioning cert install).
- ``CertRenewalCSR.nonce_hash`` is REQUIRED on the wire and exactly
  16 bytes (``SHA256(nonce)[0:16]``). Go's ``omitempty`` was a wire-shape
  bug (the device-side handler always populated it, but the field was
  declared optional). Both impls now reject the field's absence.
- ``CertRenewalAck.status`` codes 0–4 are spec-defined: ``Success (0)``,
  ``CSRFailed (1)``, ``InstallFailed (2)``, ``InvalidCert (3)``,
  ``InvalidNonce (4)``. Codes 1–3 were Go-only until the spec patch
  landed alongside Phase 19.

Renewal frames share the operational TCP connection with data-plane
traffic (DEC-076: no TLS reconnection on rotation). Disambiguation is
structural — renewal maps carry no ``featureID`` (key 4) — see
:func:`peek_is_renewal_frame` and ``mash-go/pkg/service/zone_session.go::isRenewalMessage``.
"""

from __future__ import annotations

import hashlib
import hmac
from dataclasses import dataclass
from typing import Final, Union

import cbor2

from mash_py.wire._mutable import to_mutable
from mash_py.wire._strict import strict_validate


# ---------------------------------------------------------------------------
# MsgType constants (docs/message-types.md §4)
# ---------------------------------------------------------------------------

MSG_CERT_RENEWAL_REQUEST: Final[int] = 30
MSG_CERT_RENEWAL_CSR: Final[int] = 31
MSG_CERT_RENEWAL_INSTALL: Final[int] = 32
MSG_CERT_RENEWAL_ACK: Final[int] = 33

NONCE_SIZE: Final[int] = 32
"""Spec: ``CertRenewalRequest.nonce`` MUST be exactly 32 bytes
(``security.md §11.3``)."""

NONCE_HASH_SIZE: Final[int] = 16
"""Spec: ``CertRenewalCSR.nonce_hash`` is ``SHA256(nonce)[0:16]`` —
truncated to 128 bits."""


# ---------------------------------------------------------------------------
# Status codes (docs/security.md §11.3)
# ---------------------------------------------------------------------------

RENEWAL_STATUS_SUCCESS: Final[int] = 0
RENEWAL_STATUS_CSR_FAILED: Final[int] = 1
RENEWAL_STATUS_INSTALL_FAILED: Final[int] = 2
RENEWAL_STATUS_INVALID_CERT: Final[int] = 3
RENEWAL_STATUS_INVALID_NONCE: Final[int] = 4


def renewal_status_string(code: int) -> str:
    """Human-readable label for a renewal status code (``security.md §11.3``)."""
    match code:
        case 0:
            return "success"
        case 1:
            return "CSR generation failed"
        case 2:
            return "certificate install failed"
        case 3:
            return "invalid replacement certificate"
        case 4:
            return "nonce binding validation failed"
        case _:
            return f"unknown renewal status ({code})"


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class RenewalMessageError(ValueError):
    """Base class for renewal message encode/decode errors."""


class InvalidRenewalMessageError(RenewalMessageError):
    """CBOR was parseable but doesn't match the spec'd shape."""


# ---------------------------------------------------------------------------
# Nonce binding
# ---------------------------------------------------------------------------


def compute_nonce_hash(nonce: bytes) -> bytes:
    """Return ``SHA256(nonce)[:16]`` per ``security.md §11.3``.

    The 16-byte truncation matches mash-go's
    :go:func:`commissioning.ComputeNonceHash`. The full 32-byte digest is
    NOT what goes on the wire; truncation is mandatory.
    """
    if not isinstance(nonce, (bytes, bytearray)):
        raise TypeError("nonce must be bytes")
    return hashlib.sha256(bytes(nonce)).digest()[:NONCE_HASH_SIZE]


def validate_nonce_hash(nonce: bytes, hash_value: bytes) -> bool:
    """Constant-time comparison of ``hash_value`` against ``compute_nonce_hash(nonce)``.

    Returns ``False`` (not an exception) on length mismatch so callers can
    map a single boolean onto a wire error code without exception
    handling. Constant-time per ``stack-architecture.md §11.2``.
    """
    if not isinstance(hash_value, (bytes, bytearray)):
        return False
    if len(hash_value) != NONCE_HASH_SIZE:
        return False
    return hmac.compare_digest(compute_nonce_hash(nonce), bytes(hash_value))


# ---------------------------------------------------------------------------
# Message dataclasses
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CertRenewalRequest:
    """Controller → Device. Initiates renewal.

    Wire form: ``{1: 30, 2: nonce}``. Nonce is exactly 32 bytes.

    No Zone CA field — DEC-085 separated commissioning cert install
    (MsgTypes 10–13/20 on ``mash-comm/1``) from in-session renewal
    (MsgTypes 30–33 on ``mash/1``). The renewal envelope therefore carries
    only the anti-replay nonce.
    """

    nonce: bytes


@dataclass(frozen=True, slots=True)
class CertRenewalCSR:
    """Device → Controller. Returns a fresh CSR with nonce binding.

    Wire form: ``{1: 31, 2: csr, 3: nonce_hash}``.

    ``csr`` is a DER-encoded PKCS#10 CSR for a freshly-generated P-256
    key pair (NOT the active operational key per DEC-047 / key freshness).
    ``nonce_hash`` is required and exactly ``NONCE_HASH_SIZE`` bytes —
    spec patch landed alongside Phase 19.
    """

    csr: bytes
    nonce_hash: bytes


@dataclass(frozen=True, slots=True)
class CertRenewalInstall:
    """Controller → Device. Delivers the Zone CA-signed replacement cert.

    Wire form: ``{1: 32, 2: new_cert, 3: sequence}``.

    ``sequence`` is a monotonic per-zone counter the controller assigns
    to each issued cert. The device echoes it back as ``active_sequence``
    in the ack so the controller can confirm which cert is now in use.
    """

    new_cert: bytes
    sequence: int


@dataclass(frozen=True, slots=True)
class CertRenewalAck:
    """Device → Controller. Confirms install or reports failure.

    Wire form: ``{1: 33, 2: status, 3: active_sequence}``.

    On ``status == RENEWAL_STATUS_SUCCESS`` the new cert is bound to
    the ``active_sequence`` value (which equals the controller's
    ``CertRenewalInstall.sequence``). On any other status the device
    has rolled back and ``active_sequence`` reflects the cert still in
    use.
    """

    status: int
    active_sequence: int


RenewalMessage = Union[
    CertRenewalRequest,
    CertRenewalCSR,
    CertRenewalInstall,
    CertRenewalAck,
]


# ---------------------------------------------------------------------------
# Encoding
# ---------------------------------------------------------------------------


def encode(msg: RenewalMessage) -> bytes:
    """CBOR-encode a renewal message in canonical form (RFC 8949 §4.2.1)."""
    return cbor2.dumps(_to_cbor(msg), canonical=True)


def _to_cbor(msg: RenewalMessage) -> dict[int, object]:
    if isinstance(msg, CertRenewalRequest):
        _check_size("nonce", msg.nonce, NONCE_SIZE)
        return {1: MSG_CERT_RENEWAL_REQUEST, 2: bytes(msg.nonce)}
    if isinstance(msg, CertRenewalCSR):
        if not isinstance(msg.csr, (bytes, bytearray)) or len(msg.csr) == 0:
            raise InvalidRenewalMessageError("CertRenewalCSR.csr must be non-empty bytes")
        _check_size("nonce_hash", msg.nonce_hash, NONCE_HASH_SIZE)
        return {
            1: MSG_CERT_RENEWAL_CSR,
            2: bytes(msg.csr),
            3: bytes(msg.nonce_hash),
        }
    if isinstance(msg, CertRenewalInstall):
        if not isinstance(msg.new_cert, (bytes, bytearray)) or len(msg.new_cert) == 0:
            raise InvalidRenewalMessageError(
                "CertRenewalInstall.new_cert must be non-empty bytes"
            )
        if not isinstance(msg.sequence, int) or msg.sequence < 0 or msg.sequence > 0xFFFFFFFF:
            raise InvalidRenewalMessageError(
                f"CertRenewalInstall.sequence out of uint32 range: {msg.sequence}"
            )
        return {
            1: MSG_CERT_RENEWAL_INSTALL,
            2: bytes(msg.new_cert),
            3: msg.sequence,
        }
    if isinstance(msg, CertRenewalAck):
        if not isinstance(msg.status, int) or msg.status < 0 or msg.status > 255:
            raise InvalidRenewalMessageError(
                f"CertRenewalAck.status out of uint8 range: {msg.status}"
            )
        if (
            not isinstance(msg.active_sequence, int)
            or msg.active_sequence < 0
            or msg.active_sequence > 0xFFFFFFFF
        ):
            raise InvalidRenewalMessageError(
                f"CertRenewalAck.active_sequence out of uint32 range: {msg.active_sequence}"
            )
        return {
            1: MSG_CERT_RENEWAL_ACK,
            2: msg.status,
            3: msg.active_sequence,
        }
    raise InvalidRenewalMessageError(
        f"unknown renewal message type: {type(msg).__name__}"
    )


def _check_size(field_name: str, value: bytes, expected: int) -> None:
    if not isinstance(value, (bytes, bytearray)):
        raise InvalidRenewalMessageError(f"{field_name} must be bytes")
    if len(value) != expected:
        raise InvalidRenewalMessageError(
            f"{field_name} must be {expected} bytes, got {len(value)}"
        )


# ---------------------------------------------------------------------------
# Decoding
# ---------------------------------------------------------------------------


def decode(data: bytes) -> RenewalMessage:
    """Decode a single CBOR-encoded renewal message.

    Runs ``strict_validate`` first (rejects duplicate keys, NaN, Inf —
    matches mash-go's strict-decode options) and then dispatches on the
    integer ``msgType``.
    """
    strict_validate(data)
    try:
        obj = to_mutable(cbor2.loads(data))
    except cbor2.CBORDecodeError as exc:
        raise InvalidRenewalMessageError(f"CBOR decode failed: {exc}") from exc

    if not isinstance(obj, dict):
        raise InvalidRenewalMessageError(
            f"renewal message must be a CBOR map, got {type(obj).__name__}"
        )

    msg_type = obj.get(1)
    if not isinstance(msg_type, int):
        raise InvalidRenewalMessageError("missing or non-integer msgType (key 1)")

    match msg_type:
        case 30:
            return _decode_request(obj)
        case 31:
            return _decode_csr(obj)
        case 32:
            return _decode_install(obj)
        case 33:
            return _decode_ack(obj)
        case _:
            raise InvalidRenewalMessageError(
                f"unsupported renewal msgType: {msg_type}"
            )


def _decode_request(obj: dict[int, object]) -> CertRenewalRequest:
    nonce = _require_bytes(obj, 2, "CertRenewalRequest.nonce", expected=NONCE_SIZE)
    return CertRenewalRequest(nonce=nonce)


def _decode_csr(obj: dict[int, object]) -> CertRenewalCSR:
    csr = _require_bytes(obj, 2, "CertRenewalCSR.csr", expected=None)
    if len(csr) == 0:
        raise InvalidRenewalMessageError("CertRenewalCSR.csr must be non-empty")
    nonce_hash = _require_bytes(
        obj, 3, "CertRenewalCSR.nonce_hash", expected=NONCE_HASH_SIZE
    )
    return CertRenewalCSR(csr=csr, nonce_hash=nonce_hash)


def _decode_install(obj: dict[int, object]) -> CertRenewalInstall:
    new_cert = _require_bytes(obj, 2, "CertRenewalInstall.new_cert", expected=None)
    if len(new_cert) == 0:
        raise InvalidRenewalMessageError("CertRenewalInstall.new_cert must be non-empty")
    sequence = obj.get(3)
    if not isinstance(sequence, int) or sequence < 0 or sequence > 0xFFFFFFFF:
        raise InvalidRenewalMessageError(
            "CertRenewalInstall.sequence must be a uint32"
        )
    return CertRenewalInstall(new_cert=new_cert, sequence=sequence)


def _decode_ack(obj: dict[int, object]) -> CertRenewalAck:
    status = obj.get(2)
    if not isinstance(status, int) or status < 0 or status > 255:
        raise InvalidRenewalMessageError("CertRenewalAck.status must be a uint8")
    active_sequence = obj.get(3)
    if (
        not isinstance(active_sequence, int)
        or active_sequence < 0
        or active_sequence > 0xFFFFFFFF
    ):
        raise InvalidRenewalMessageError(
            "CertRenewalAck.active_sequence must be a uint32"
        )
    return CertRenewalAck(status=status, active_sequence=active_sequence)


def _require_bytes(
    obj: dict[int, object], key: int, where: str, expected: int | None
) -> bytes:
    val = obj.get(key)
    if not isinstance(val, (bytes, bytearray)):
        raise InvalidRenewalMessageError(f"{where} (key {key}) must be a byte string")
    out = bytes(val)
    if expected is not None and len(out) != expected:
        raise InvalidRenewalMessageError(
            f"{where} must be {expected} bytes, got {len(out)}"
        )
    return out


# ---------------------------------------------------------------------------
# Frame routing helpers
# ---------------------------------------------------------------------------


def peek_msg_type(data: bytes) -> int:
    """Return the ``msgType`` field of a CBOR-encoded renewal message.

    Raises :class:`InvalidRenewalMessageError` if ``data`` is not a parseable
    CBOR map with an integer key 1.
    """
    try:
        obj = to_mutable(cbor2.loads(data))
    except cbor2.CBORDecodeError as exc:
        raise InvalidRenewalMessageError(f"CBOR decode failed: {exc}") from exc
    if not isinstance(obj, dict):
        raise InvalidRenewalMessageError("not a CBOR map")
    val = obj.get(1)
    if not isinstance(val, int):
        raise InvalidRenewalMessageError("missing or non-integer msgType (key 1)")
    return val


def peek_is_renewal_frame(data: bytes) -> bool:
    """Return True if ``data`` looks like a renewal frame on ``mash/1``.

    Renewal frames are flat CBOR maps with key 1 = ``uint8`` ∈ {30..33} and
    no key 4. Data-plane Requests always carry key 4 (``featureID``); Responses
    and Notifications use key 1 as a uint32 messageId, which won't fall in
    {30..33} except by collision when the controller's counter wraps. Even
    in that collision case the data-plane request has key 4 set, so the
    "no key 4" check is the disambiguating signal mash-go uses
    (``pkg/service/zone_session.go::isRenewalMessage``).

    Returns False on any decode failure rather than raising — callers use
    this as a routing predicate, not a validator.
    """
    try:
        obj = to_mutable(cbor2.loads(data))
    except (cbor2.CBORDecodeError, EOFError, ValueError):
        return False
    if not isinstance(obj, dict):
        return False
    msg_type = obj.get(1)
    if not isinstance(msg_type, int):
        return False
    if msg_type < MSG_CERT_RENEWAL_REQUEST or msg_type > MSG_CERT_RENEWAL_ACK:
        return False
    if 4 in obj:
        return False
    return True


__all__ = [
    "MSG_CERT_RENEWAL_ACK",
    "MSG_CERT_RENEWAL_CSR",
    "MSG_CERT_RENEWAL_INSTALL",
    "MSG_CERT_RENEWAL_REQUEST",
    "NONCE_HASH_SIZE",
    "NONCE_SIZE",
    "RENEWAL_STATUS_CSR_FAILED",
    "RENEWAL_STATUS_INSTALL_FAILED",
    "RENEWAL_STATUS_INVALID_CERT",
    "RENEWAL_STATUS_INVALID_NONCE",
    "RENEWAL_STATUS_SUCCESS",
    "CertRenewalAck",
    "CertRenewalCSR",
    "CertRenewalInstall",
    "CertRenewalRequest",
    "InvalidRenewalMessageError",
    "RenewalMessage",
    "RenewalMessageError",
    "compute_nonce_hash",
    "decode",
    "encode",
    "peek_is_renewal_frame",
    "peek_msg_type",
    "renewal_status_string",
    "validate_nonce_hash",
]
