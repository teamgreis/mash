"""PASE commissioning messages — Phase 8.

CBOR-encoded messages exchanged during the SPAKE2+ handshake between a
controller and a device. Wire formats are normative in
``docs/testing/behavior/commissioning-pase.md §4.2`` (codified by DEC-084);
this module is the Python port.

Spec layout (each message is a CBOR map with integer keys, framed per
``transport.md §4`` — 4-byte big-endian length prefix + canonical CBOR)::

    PASERequest  (MsgType 1, C → D): {1: 1, 2: pA[65], 3: ClientIdentity}
    PASEResponse (MsgType 2, D → C): {1: 2, 2: pB[65]}
    PASEConfirm  (MsgType 3, C → D): {1: 3, 2: confirmA[32]}
    PASEComplete (MsgType 4, D → C): {1: 4, 2: confirmB[32], 3: errorCode}

Plus ``CommissioningError`` (MsgType 255), which has a single legitimate
use per ``docs/security.md §13`` and DEC-063: the device's *early
rejection* of a commissioning attempt before / instead of producing
``PASEResponse`` — e.g. busy with another session. It is **not** a
PASE-failure envelope.

**Spec vs Go failure-envelope divergence (Issue #6).** mash-go currently
mis-uses ``CommissioningError`` for failures *inside* the PASE handshake
(invalid pA, wrong client identity) where the spec mandates
``PASEComplete{ErrorCode: ErrCodeAuthFailed}``. mash-py is spec-strict:
``CommissioningError`` is decodable as a discrete message but is
**never** treated as an in-PASE failure. The PASE client state machine
accepts ``CommissioningError`` only in the slot where ``PASEResponse``
is expected, and only when its ``error_code == ERR_CODE_BUSY`` (DEC-063);
any other envelope mismatch is a protocol error. mash-go is being fixed
in a separate workstream — see ``mash-py-spec-issues.md`` Issue #6.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Final, Union

import cbor2

from mash_py.commissioning.spake2plus import (
    CONFIRM_MAC_SIZE,
    CONTROLLER_IDENTITY,
    POINT_SIZE,
)
from mash_py.wire._strict import strict_validate

# ---------------------------------------------------------------------------
# Message-type constants (DEC-084 §4.2 / mash-go pkg/commissioning/messages.go)
# ---------------------------------------------------------------------------

MSG_PASE_REQUEST: Final[int] = 1
MSG_PASE_RESPONSE: Final[int] = 2
MSG_PASE_CONFIRM: Final[int] = 3
MSG_PASE_COMPLETE: Final[int] = 4
MSG_CSR_REQUEST: Final[int] = 10
MSG_CSR_RESPONSE: Final[int] = 11
MSG_CERT_INSTALL: Final[int] = 12
MSG_CERT_INSTALL_RESPONSE: Final[int] = 13
MSG_COMMISSIONING_COMPLETE: Final[int] = 20
MSG_COMMISSIONING_ERROR: Final[int] = 255


# ---------------------------------------------------------------------------
# Error-code constants (DEC-047 / commissioning-pase.md §6.5)
# ---------------------------------------------------------------------------

ERR_CODE_SUCCESS: Final[int] = 0
ERR_CODE_AUTH_FAILED: Final[int] = 1
"""DEC-047: any SPAKE2+ verification failure (includes pre-DEC-047
``InvalidPublicKey`` and ``ConfirmFailed``)."""

ERR_CODE_TIMEOUT: Final[int] = 2
ERR_CODE_CSR_FAILED: Final[int] = 3
ERR_CODE_CERT_INSTALL_FAILED: Final[int] = 4
ERR_CODE_BUSY: Final[int] = 5
"""Device busy, retry later (DEC-063)."""

ERR_CODE_ZONE_TYPE_EXISTS: Final[int] = 10
ERR_CODE_INTERNAL_ERROR: Final[int] = 255


def error_code_string(code: int) -> str:
    """Human-readable label for a commissioning error code.

    Mirrors mash-go's :go:func:`commissioning.ErrorCodeString`.
    Intentionally vague for auth failures per DEC-047 (no information
    leakage about which check failed).
    """
    match code:
        case 0:
            return "success"
        case 1:
            return "authentication failed"
        case 2:
            return "timeout"
        case 3:
            return "CSR generation failed"
        case 4:
            return "certificate installation failed"
        case 5:
            return "device busy"
        case 10:
            return "zone type already exists"
        case 255:
            return "internal error"
        case _:
            return f"unknown error ({code})"


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class MessageError(ValueError):
    """Base class for PASE message encode/decode errors."""


class InvalidMessageError(MessageError):
    """CBOR was parseable but doesn't match the spec'd shape (wrong type,
    missing field, wrong byte length, etc.)."""


# ---------------------------------------------------------------------------
# Message dataclasses (DEC-084 §4.2)
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class PASERequest:
    """Controller → Device. Initiates the SPAKE2+ handshake.

    Wire form: ``{1: 1, 2: pA, 3: ClientIdentity}``.

    ``client_identity`` defaults to ``b"mash-controller"`` per DEC-084 §4.2;
    devices MUST reject any other value with ``ERR_CODE_AUTH_FAILED``.
    Reserved for future delegated-commissioning use (DEC-019).
    """

    public_value: bytes
    """``pA = x*G + w0*M`` in SEC1 uncompressed encoding (65 bytes)."""

    client_identity: bytes = CONTROLLER_IDENTITY


@dataclass(frozen=True, slots=True)
class PASEResponse:
    """Device → Controller. Carries the device's public value.

    Wire form: ``{1: 2, 2: pB}``.
    """

    public_value: bytes
    """``pB = y*G + w0*N`` in SEC1 uncompressed encoding (65 bytes)."""


@dataclass(frozen=True, slots=True)
class PASEConfirm:
    """Controller → Device. Carries the controller's confirmation MAC.

    Wire form: ``{1: 3, 2: confirmA}``.
    """

    confirmation: bytes
    """``confirmA = HMAC-SHA256(KcA, pB)`` (32 bytes)."""


@dataclass(frozen=True, slots=True)
class PASEComplete:
    """Device → Controller. Final PASE message: device confirmation +
    success/failure status.

    Wire form: ``{1: 4, 2: confirmB, 3: errorCode}``.

    A non-zero ``error_code`` indicates SPAKE2+ failure on the device
    side. The spec (§2.5 / DEC-047) mandates ``ERR_CODE_AUTH_FAILED`` for
    every in-PASE failure mode. Any other non-zero value is non-conformant
    on the wire (mash-go currently sends ``ErrCodeConfirmFailed = 2`` on
    MAC mismatch — Issue #6, fix pending); the controller surfaces it as
    a generic auth failure either way, but does so via a single typed
    error (``PASEAuthFailedError``) rather than blurring the envelope
    distinction.
    """

    confirmation: bytes
    """``confirmB = HMAC-SHA256(KcB, pA)`` (32 bytes). Meaningful only when
    ``error_code == ERR_CODE_SUCCESS``; some implementations send all-zero
    bytes on the failure path."""

    error_code: int = ERR_CODE_SUCCESS


@dataclass(frozen=True, slots=True)
class CommissioningError:
    """Cross-cutting commissioning rejection (MsgType 255).

    Wire form: ``{1: 255, 2: errorCode, 3: message?, 4: retryAfterMs}``.

    Spec-defined use is DEC-063 / ``security.md §13``: device rejects a
    commissioning attempt early (e.g. busy with another session) before
    it would otherwise emit ``PASEResponse``. ``error_code`` is one of
    the cross-cutting codes (``ERR_CODE_BUSY``, ``ERR_CODE_INTERNAL_ERROR``);
    ``retry_after_ms`` is a controller hint for back-off.

    NOT a valid envelope for failures *inside* the PASE handshake — those
    use ``PASEComplete{error_code != 0}`` per spec §2.5. See Issue #6 for
    the mash-go bug that mis-uses this envelope; mash-py's PASE client
    rejects such messages as protocol errors.
    """

    error_code: int
    message: str = ""
    retry_after_ms: int = 0


# ---------------------------------------------------------------------------
# Cert install dataclasses (DEC-085 — commissioning-pase.md §4.3)
#
# Post-PASE certificate-install messages on the commissioning ALPN. These
# carry their own MsgType range (10–13, 20) to keep the namespace disjoint
# from in-session renewal (30–33 on the operational ALPN). See
# ``mash-py-spec-issues.md`` Issue #7 and DEC-085 for the rationale.
# ---------------------------------------------------------------------------


CSR_NONCE_SIZE: Final[int] = 32
"""Spec: ``CSRRequest.nonce`` MUST be exactly 32 bytes
(``commissioning-pase.md §4.3``)."""


@dataclass(frozen=True, slots=True)
class CSRRequest:
    """Controller → Device. Asks the device to generate an operational key
    pair and return a Certificate Signing Request.

    Wire form: ``{1: 10, 2: nonce}``.

    The 32-byte ``nonce`` is fresh per commissioning attempt and exists for
    defense-in-depth against replay. The PASE-derived shared secret is the
    primary anti-replay guarantee on this channel; the nonce is reserved
    for future delegated-commissioning use (DEC-019) where a third party
    relays the CSR.
    """

    nonce: bytes
    """Random 32 bytes from a CSPRNG."""


@dataclass(frozen=True, slots=True)
class CSRResponse:
    """Device → Controller. Returns a PKCS#10 CSR signed by a fresh P-256
    key pair the device has just generated for this zone.

    Wire form: ``{1: 11, 2: csr, 3: errorCode}``.

    On non-zero ``error_code`` the device rejects the CSR step; ``csr`` may
    be empty in that case. Otherwise ``csr`` is DER-encoded PKCS#10.
    """

    csr: bytes
    error_code: int = ERR_CODE_SUCCESS


@dataclass(frozen=True, slots=True)
class CertInstall:
    """Controller → Device. Delivers the Zone CA-signed operational
    certificate plus the trust anchor (Zone CA cert) and zone metadata.

    Wire form: ``{1: 12, 2: operationalCert, 3: caCert, 4: zoneType, 5: zonePriority}``.

    ``zone_type`` is one of the values in DEC-060 (1=GRID, 2=LOCAL, 3=TEST).
    ``zone_priority`` is 1 or 2 (controller-assigned within this zone type).
    The device MUST verify the chain ``operational_cert ← ca_cert`` and
    that ``operational_cert.SubjectPublicKeyInfo`` matches the public key
    in the CSR it just sent. See ``commissioning-pase.md §4.3``.
    """

    operational_cert: bytes
    ca_cert: bytes
    zone_type: int
    zone_priority: int


@dataclass(frozen=True, slots=True)
class CertInstallResponse:
    """Device → Controller. Acks (or rejects) cert installation.

    Wire form: ``{1: 13, 2: errorCode}``.

    ``error_code`` is 0 on success; on failure it is one of:
    ``ERR_CODE_CERT_INSTALL_FAILED`` (4), ``ERR_CODE_BUSY`` (5),
    ``ERR_CODE_ZONE_TYPE_EXISTS`` (10). On a non-zero code the device does
    NOT send ``CommissioningComplete``; the controller MUST treat the
    commissioning attempt as failed and close the connection.
    """

    error_code: int = ERR_CODE_SUCCESS


@dataclass(frozen=True, slots=True)
class CommissioningComplete:
    """Device → Controller. Terminator: signals the device has durably
    installed the operational cert and is ready to accept an operational
    TLS reconnection on ``mash/1``.

    Wire form: ``{1: 20}`` — empty payload.

    Sent only after ``CertInstallResponse{error_code = 0}``. The device
    MUST close the commissioning connection immediately after sending this
    frame (``security.md §4.5``).
    """


PASEMessage = Union[
    PASERequest,
    PASEResponse,
    PASEConfirm,
    PASEComplete,
    CSRRequest,
    CSRResponse,
    CertInstall,
    CertInstallResponse,
    CommissioningComplete,
    CommissioningError,
]


# ---------------------------------------------------------------------------
# Encoding
# ---------------------------------------------------------------------------


def encode(msg: PASEMessage) -> bytes:
    """CBOR-encode a PASE message in canonical form.

    Canonical encoding (RFC 8949 §4.2.1) keeps mash-py byte-for-byte
    compatible with mash-go's ``cbor.SortCanonical`` mode — particularly
    useful for vector-based interop tests where we record bytes from one
    runtime and decode them in another.
    """
    obj = _to_cbor(msg)
    return cbor2.dumps(obj, canonical=True)


def _to_cbor(msg: PASEMessage) -> dict[int, object]:
    """Convert a typed message to its CBOR map representation."""
    if isinstance(msg, PASERequest):
        _check_size("public_value", msg.public_value, POINT_SIZE)
        return {1: MSG_PASE_REQUEST, 2: msg.public_value, 3: msg.client_identity}
    if isinstance(msg, PASEResponse):
        _check_size("public_value", msg.public_value, POINT_SIZE)
        return {1: MSG_PASE_RESPONSE, 2: msg.public_value}
    if isinstance(msg, PASEConfirm):
        _check_size("confirmation", msg.confirmation, CONFIRM_MAC_SIZE)
        return {1: MSG_PASE_CONFIRM, 2: msg.confirmation}
    if isinstance(msg, PASEComplete):
        # Confirmation length is not strictly enforced on encode for the
        # failure path: some implementations zero-fill, others omit. We
        # require the field to be ``bytes`` and enforce the 32-byte size
        # only on the success path; that matches mash-go's emit behavior
        # at session.go:249-253.
        if not isinstance(msg.confirmation, (bytes, bytearray)):
            raise InvalidMessageError("PASEComplete.confirmation must be bytes")
        if msg.error_code == ERR_CODE_SUCCESS:
            _check_size("confirmation", msg.confirmation, CONFIRM_MAC_SIZE)
        return {
            1: MSG_PASE_COMPLETE,
            2: bytes(msg.confirmation),
            3: msg.error_code,
        }
    if isinstance(msg, CSRRequest):
        _check_size("nonce", msg.nonce, CSR_NONCE_SIZE)
        return {1: MSG_CSR_REQUEST, 2: msg.nonce}
    if isinstance(msg, CSRResponse):
        if not isinstance(msg.csr, (bytes, bytearray)):
            raise InvalidMessageError("CSRResponse.csr must be bytes")
        return {
            1: MSG_CSR_RESPONSE,
            2: bytes(msg.csr),
            3: msg.error_code,
        }
    if isinstance(msg, CertInstall):
        if not isinstance(msg.operational_cert, (bytes, bytearray)):
            raise InvalidMessageError("CertInstall.operational_cert must be bytes")
        if not isinstance(msg.ca_cert, (bytes, bytearray)):
            raise InvalidMessageError("CertInstall.ca_cert must be bytes")
        if msg.zone_type < 0 or msg.zone_type > 255:
            raise InvalidMessageError(
                f"CertInstall.zone_type out of range: {msg.zone_type}"
            )
        if msg.zone_priority < 0 or msg.zone_priority > 255:
            raise InvalidMessageError(
                f"CertInstall.zone_priority out of range: {msg.zone_priority}"
            )
        return {
            1: MSG_CERT_INSTALL,
            2: bytes(msg.operational_cert),
            3: bytes(msg.ca_cert),
            4: msg.zone_type,
            5: msg.zone_priority,
        }
    if isinstance(msg, CertInstallResponse):
        if msg.error_code < 0 or msg.error_code > 255:
            raise InvalidMessageError(
                f"CertInstallResponse.error_code out of range: {msg.error_code}"
            )
        return {1: MSG_CERT_INSTALL_RESPONSE, 2: msg.error_code}
    if isinstance(msg, CommissioningComplete):
        return {1: MSG_COMMISSIONING_COMPLETE}
    if isinstance(msg, CommissioningError):
        out: dict[int, object] = {1: MSG_COMMISSIONING_ERROR, 2: msg.error_code}
        # Field 3 is ``omitempty`` in mash-go (messages.go:177). Match
        # that on the encode side so canonical-bytes round-trip cleanly.
        if msg.message:
            out[3] = msg.message
        out[4] = msg.retry_after_ms
        return out
    raise InvalidMessageError(f"unknown message type: {type(msg).__name__}")


def _check_size(field_name: str, value: bytes, expected: int) -> None:
    if not isinstance(value, (bytes, bytearray)):
        raise InvalidMessageError(f"{field_name} must be bytes")
    if len(value) != expected:
        raise InvalidMessageError(
            f"{field_name} must be {expected} bytes, got {len(value)}"
        )


# ---------------------------------------------------------------------------
# Decoding
# ---------------------------------------------------------------------------


def decode(data: bytes) -> PASEMessage:
    """Decode a single CBOR-encoded PASE message.

    Runs ``strict_validate`` first to reject duplicate map keys / NaN /
    Infinity (matches mash-go's strict decoder options); then dispatches
    on the integer ``msgType`` field. Unknown / malformed inputs raise
    :class:`InvalidMessageError`.
    """
    strict_validate(data)
    try:
        obj = cbor2.loads(data)
    except cbor2.CBORDecodeError as exc:
        raise InvalidMessageError(f"CBOR decode failed: {exc}") from exc

    if not isinstance(obj, dict):
        raise InvalidMessageError(f"PASE message must be a CBOR map, got {type(obj).__name__}")

    msg_type = obj.get(1)
    if not isinstance(msg_type, int):
        raise InvalidMessageError("missing or non-integer msgType (key 1)")

    match msg_type:
        case 1:  # MSG_PASE_REQUEST
            return _decode_pase_request(obj)
        case 2:  # MSG_PASE_RESPONSE
            return _decode_pase_response(obj)
        case 3:  # MSG_PASE_CONFIRM
            return _decode_pase_confirm(obj)
        case 4:  # MSG_PASE_COMPLETE
            return _decode_pase_complete(obj)
        case 10:  # MSG_CSR_REQUEST
            return _decode_csr_request(obj)
        case 11:  # MSG_CSR_RESPONSE
            return _decode_csr_response(obj)
        case 12:  # MSG_CERT_INSTALL
            return _decode_cert_install(obj)
        case 13:  # MSG_CERT_INSTALL_RESPONSE
            return _decode_cert_install_response(obj)
        case 20:  # MSG_COMMISSIONING_COMPLETE
            return CommissioningComplete()
        case 255:  # MSG_COMMISSIONING_ERROR
            return _decode_commissioning_error(obj)
        case _:
            raise InvalidMessageError(f"unsupported PASE message type: {msg_type}")


def _decode_pase_request(obj: dict[int, object]) -> PASERequest:
    pA = _require_bytes(obj, 2, "PASERequest.publicValue", POINT_SIZE)
    identity = _require_bytes(obj, 3, "PASERequest.clientIdentity", expected=None)
    return PASERequest(public_value=pA, client_identity=identity)


def _decode_pase_response(obj: dict[int, object]) -> PASEResponse:
    pB = _require_bytes(obj, 2, "PASEResponse.publicValue", POINT_SIZE)
    return PASEResponse(public_value=pB)


def _decode_pase_confirm(obj: dict[int, object]) -> PASEConfirm:
    confirm = _require_bytes(obj, 2, "PASEConfirm.confirmation", CONFIRM_MAC_SIZE)
    return PASEConfirm(confirmation=confirm)


def _decode_pase_complete(obj: dict[int, object]) -> PASEComplete:
    # Confirmation size is enforced only on the success path; some
    # implementations zero-fill or shorten on failure (see encode docs).
    confirm = _require_bytes(obj, 2, "PASEComplete.confirmation", expected=None)
    error_code = obj.get(3, ERR_CODE_SUCCESS)
    if not isinstance(error_code, int):
        raise InvalidMessageError("PASEComplete.errorCode must be a non-negative integer")
    if error_code < 0 or error_code > 255:
        raise InvalidMessageError(f"PASEComplete.errorCode out of range: {error_code}")
    if error_code == ERR_CODE_SUCCESS and len(confirm) != CONFIRM_MAC_SIZE:
        raise InvalidMessageError(
            f"PASEComplete.confirmation must be {CONFIRM_MAC_SIZE} bytes on success, "
            f"got {len(confirm)}"
        )
    return PASEComplete(confirmation=confirm, error_code=error_code)


def _decode_csr_request(obj: dict[int, object]) -> CSRRequest:
    nonce = _require_bytes(obj, 2, "CSRRequest.nonce", expected=CSR_NONCE_SIZE)
    return CSRRequest(nonce=nonce)


def _decode_csr_response(obj: dict[int, object]) -> CSRResponse:
    csr = _require_bytes(obj, 2, "CSRResponse.csr", expected=None)
    error_code = obj.get(3, ERR_CODE_SUCCESS)
    if not isinstance(error_code, int) or error_code < 0 or error_code > 255:
        raise InvalidMessageError("CSRResponse.errorCode must be a uint8")
    if error_code == ERR_CODE_SUCCESS and len(csr) == 0:
        raise InvalidMessageError("CSRResponse.csr must be non-empty on success")
    return CSRResponse(csr=csr, error_code=error_code)


def _decode_cert_install(obj: dict[int, object]) -> CertInstall:
    operational_cert = _require_bytes(
        obj, 2, "CertInstall.operationalCert", expected=None
    )
    if len(operational_cert) == 0:
        raise InvalidMessageError("CertInstall.operationalCert must be non-empty")
    ca_cert = _require_bytes(obj, 3, "CertInstall.caCert", expected=None)
    if len(ca_cert) == 0:
        raise InvalidMessageError("CertInstall.caCert must be non-empty")
    zone_type = obj.get(4)
    if not isinstance(zone_type, int) or zone_type < 0 or zone_type > 255:
        raise InvalidMessageError("CertInstall.zoneType must be a uint8")
    zone_priority = obj.get(5)
    if not isinstance(zone_priority, int) or zone_priority < 0 or zone_priority > 255:
        raise InvalidMessageError("CertInstall.zonePriority must be a uint8")
    return CertInstall(
        operational_cert=operational_cert,
        ca_cert=ca_cert,
        zone_type=zone_type,
        zone_priority=zone_priority,
    )


def _decode_cert_install_response(obj: dict[int, object]) -> CertInstallResponse:
    error_code = obj.get(2)
    if not isinstance(error_code, int) or error_code < 0 or error_code > 255:
        raise InvalidMessageError("CertInstallResponse.errorCode must be a uint8")
    return CertInstallResponse(error_code=error_code)


def _decode_commissioning_error(obj: dict[int, object]) -> CommissioningError:
    error_code = obj.get(2)
    if not isinstance(error_code, int) or error_code < 0 or error_code > 255:
        raise InvalidMessageError("CommissioningError.errorCode must be a uint8")
    message = obj.get(3, "")
    if not isinstance(message, str):
        raise InvalidMessageError("CommissioningError.message must be a string")
    retry_after_ms = obj.get(4, 0)
    if not isinstance(retry_after_ms, int) or retry_after_ms < 0:
        raise InvalidMessageError(
            "CommissioningError.retryAfter must be a non-negative integer"
        )
    return CommissioningError(
        error_code=error_code, message=message, retry_after_ms=retry_after_ms
    )


def _require_bytes(
    obj: dict[int, object], key: int, where: str, expected: int | None
) -> bytes:
    val = obj.get(key)
    if not isinstance(val, (bytes, bytearray)):
        raise InvalidMessageError(f"{where} (key {key}) must be a byte string")
    out = bytes(val)
    if expected is not None and len(out) != expected:
        raise InvalidMessageError(
            f"{where} must be {expected} bytes, got {len(out)}"
        )
    return out


# ---------------------------------------------------------------------------
# Convenience: peek the message type without full decoding
# ---------------------------------------------------------------------------


def peek_msg_type(data: bytes) -> int:
    """Return the ``msgType`` field of a CBOR-encoded PASE message.

    Useful for the controller dispatcher to route an inbound frame to the
    expected message handler before paying for full validation. Raises
    :class:`InvalidMessageError` if the bytes are not a parseable map
    with an integer key 1.
    """
    try:
        obj = cbor2.loads(data)
    except cbor2.CBORDecodeError as exc:
        raise InvalidMessageError(f"CBOR decode failed: {exc}") from exc
    if not isinstance(obj, dict):
        raise InvalidMessageError("not a CBOR map")
    val = obj.get(1)
    if not isinstance(val, int):
        raise InvalidMessageError("missing or non-integer msgType (key 1)")
    return val


__all__ = [
    "CSR_NONCE_SIZE",
    "ERR_CODE_AUTH_FAILED",
    "ERR_CODE_BUSY",
    "ERR_CODE_CERT_INSTALL_FAILED",
    "ERR_CODE_CSR_FAILED",
    "ERR_CODE_INTERNAL_ERROR",
    "ERR_CODE_SUCCESS",
    "ERR_CODE_TIMEOUT",
    "ERR_CODE_ZONE_TYPE_EXISTS",
    "MSG_CERT_INSTALL",
    "MSG_CERT_INSTALL_RESPONSE",
    "MSG_COMMISSIONING_COMPLETE",
    "MSG_COMMISSIONING_ERROR",
    "MSG_CSR_REQUEST",
    "MSG_CSR_RESPONSE",
    "MSG_PASE_COMPLETE",
    "MSG_PASE_CONFIRM",
    "MSG_PASE_REQUEST",
    "MSG_PASE_RESPONSE",
    "CSRRequest",
    "CSRResponse",
    "CertInstall",
    "CertInstallResponse",
    "CommissioningComplete",
    "CommissioningError",
    "InvalidMessageError",
    "MessageError",
    "PASEComplete",
    "PASEConfirm",
    "PASEMessage",
    "PASERequest",
    "PASEResponse",
    "decode",
    "encode",
    "error_code_string",
    "peek_msg_type",
]
