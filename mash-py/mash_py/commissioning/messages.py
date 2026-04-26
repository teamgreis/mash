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


PASEMessage = Union[
    PASERequest,
    PASEResponse,
    PASEConfirm,
    PASEComplete,
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
