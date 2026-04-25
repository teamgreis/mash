"""CBOR codec for MASH wire messages.

Ports mash-go/pkg/wire/codec.go.

Encoding uses cbor2's canonical mode, which produces RFC 7049 §3.9 / RFC 8949
§4.2.1 deterministic output: shortest integer form, length-first key sort.
This matches fxamacker's ``SortCanonical`` mode used by mash-go.

Decoding adds a strict validation pass (mash_py.wire._strict) that rejects
duplicate map keys and NaN/Infinity floats — cbor2 silently accepts both,
which would diverge from mash-go's ``DupMapKeyEnforcedAPF`` /
``NaNDecodeForbidden`` / ``InfDecodeForbidden`` decoder options.

Wire forms by message type:

- Request:      ``{1: messageId, 2: operation, 3: endpointId, 4: featureId, 5: payload?}``
- Response:     ``{1: messageId, 2: status, 3: payload?}``
- Notification: ``{1: 0, 2: subscriptionId, 3: endpointId, 4: featureId, 5: changes}``
- Control:      ``{1: type, 2: sequence?}``  — sequence omitted when zero

Payloads embedded inside Requests/Responses are encoded operation-by-operation.
See mash_py.wire.payloads for dataclass definitions and the encode helpers
below for the per-operation rules.
"""

from __future__ import annotations

from typing import Any

import cbor2

from mash_py.wire._strict import strict_validate
from mash_py.wire.operations import (
    NOTIFICATION_MESSAGE_ID,
    ControlMessageType,
    Operation,
    Status,
)
from mash_py.wire.payloads import (
    ControlMessage,
    ErrorPayload,
    InvokePayload,
    Notification,
    ReadPayload,
    Request,
    Response,
    SubscribePayload,
    SubscribeResponsePayload,
    UnsubscribePayload,
)

# ---------------------------------------------------------------------------
# Marshal helpers
# ---------------------------------------------------------------------------


def marshal(value: Any) -> bytes:
    """Encode ``value`` to canonical CBOR bytes.

    All MASH wire encoding goes through this helper to guarantee canonical
    ordering, just as mash-go funnels everything through ``encMode.Marshal``.
    """
    return cbor2.dumps(value, canonical=True)


def unmarshal(data: bytes) -> Any:
    """Decode ``data`` after running the strict validation pass."""
    strict_validate(data)
    return cbor2.loads(data)


# ---------------------------------------------------------------------------
# Payload serialisation — per-operation wire form
# ---------------------------------------------------------------------------


def _encode_payload(payload: Any) -> Any:
    """Convert a typed payload (dataclass / list / dict) into the CBOR-shaped
    object that goes into key 5 of a Request / key 3 of a Response.

    The decision tree mirrors how mash-go fills ``Request.Payload``:

    - Read       -> bare list[int]            (docs/interaction-model.md §4.1)
    - Write      -> bare dict[int, Any]       (§5.1)
    - Subscribe  -> {1: ids, 2: minMs, 3: maxMs}, omitempty per field (§6.1)
    - Unsubscribe-> {1: subId}                 (§6.x — payload of Subscribe op
      with endpoint/feature both 0)
    - Invoke     -> {1: commandId, 2: params}  (§7.1)
    - Error      -> {1: message}, omitempty    (mash-go message.go:310)
    - SubscribeResponse -> {1: subId, 2: currentValues}, omitempty
    """
    if payload is None:
        return None

    if isinstance(payload, ReadPayload):
        # Bare CBOR array; empty means "all attributes" (docs §4.3).
        return list(payload.attribute_ids)

    if isinstance(payload, SubscribePayload):
        out: dict[int, Any] = {}
        if payload.attribute_ids:
            out[1] = list(payload.attribute_ids)
        if payload.min_interval:
            out[2] = payload.min_interval
        if payload.max_interval:
            out[3] = payload.max_interval
        return out

    if isinstance(payload, UnsubscribePayload):
        return {1: payload.subscription_id}

    if isinstance(payload, InvokePayload):
        out2: dict[int, Any] = {1: payload.command_id}
        if payload.parameters is not None:
            out2[2] = payload.parameters
        return out2

    if isinstance(payload, ErrorPayload):
        out3: dict[int, Any] = {}
        if payload.message:
            out3[1] = payload.message
        return out3

    if isinstance(payload, SubscribeResponsePayload):
        out4: dict[int, Any] = {1: payload.subscription_id}
        if payload.current_values:
            out4[2] = payload.current_values
        return out4

    # Fall-through: caller passed a raw dict / list / scalar. Trust it.
    return payload


# ---------------------------------------------------------------------------
# Request
# ---------------------------------------------------------------------------


def encode_request(req: Request) -> bytes:
    """Serialise a Request to canonical CBOR bytes.

    Mirrors mash-go ``EncodeRequest`` (codec.go:71-76): validate then marshal.
    """
    req.validate()
    msg: dict[int, Any] = {
        1: req.message_id,
        2: int(req.operation),
        3: req.endpoint_id,
        4: req.feature_id,
    }
    encoded_payload = _encode_payload(req.payload)
    if encoded_payload is not None:
        msg[5] = encoded_payload
    return marshal(msg)


def decode_request(data: bytes) -> Request:
    """Decode a Request from CBOR bytes.

    Validates the message after decoding (mash-go codec.go:79-88). The payload
    is left as a raw CBOR-decoded value (list / dict / scalar / None); higher
    layers convert it to typed dataclasses based on the operation.
    """
    raw = unmarshal(data)
    if not isinstance(raw, dict):
        raise ValueError(f"request must be a CBOR map, got {type(raw).__name__}")
    _require_int_keys(raw)

    # Pull operation as a raw int. Don't cast to Operation IntEnum here —
    # invalid uint values (e.g. 99 from a forged frame) would raise Python's
    # "X is not a valid Operation" and obscure the MASH-level "invalid
    # operation" error from Request.validate().
    op_raw = _get_uint(raw, 2, "operation")
    req = Request(
        message_id=_get_uint(raw, 1, "messageId"),
        operation=op_raw,  # type: ignore[arg-type]  # promoted after validate()
        endpoint_id=_get_uint(raw, 3, "endpointId"),
        feature_id=_get_uint(raw, 4, "featureId"),
        payload=raw.get(5),
    )
    req.validate()
    req.operation = Operation(op_raw)
    return req


# ---------------------------------------------------------------------------
# Response
# ---------------------------------------------------------------------------


def encode_response(resp: Response) -> bytes:
    msg: dict[int, Any] = {
        1: resp.message_id,
        2: int(resp.status),
    }
    encoded_payload = _encode_payload(resp.payload)
    if encoded_payload is not None:
        msg[3] = encoded_payload
    return marshal(msg)


def decode_response(data: bytes) -> Response:
    raw = unmarshal(data)
    if not isinstance(raw, dict):
        raise ValueError(f"response must be a CBOR map, got {type(raw).__name__}")
    _require_int_keys(raw)
    return Response(
        message_id=_get_uint(raw, 1, "messageId"),
        status=Status(_get_uint(raw, 2, "status")),
        payload=raw.get(3),
    )


# ---------------------------------------------------------------------------
# Notification
# ---------------------------------------------------------------------------


def encode_notification(notif: Notification) -> bytes:
    """Serialise a Notification.

    The wire form embeds the sentinel ``messageId=0`` (NOTIFICATION_MESSAGE_ID).
    Mirrors mash-go ``EncodeNotification`` (codec.go:106-122) which builds the
    wire struct inline rather than reusing ``Notification`` directly.
    """
    msg: dict[int, Any] = {
        1: NOTIFICATION_MESSAGE_ID,
        2: notif.subscription_id,
        3: notif.endpoint_id,
        4: notif.feature_id,
        5: notif.changes,
    }
    return marshal(msg)


def decode_notification(data: bytes) -> Notification:
    raw = unmarshal(data)
    if not isinstance(raw, dict):
        raise ValueError(f"notification must be a CBOR map, got {type(raw).__name__}")
    _require_int_keys(raw)
    msg_id = _get_uint(raw, 1, "messageId")
    if msg_id != NOTIFICATION_MESSAGE_ID:
        raise ValueError(f"not a notification message: messageId={msg_id}")
    changes_raw = raw.get(5, {})
    if changes_raw is None:
        changes: dict[int, Any] = {}
    elif isinstance(changes_raw, dict):
        changes = {int(k): v for k, v in changes_raw.items()}
    else:
        raise ValueError(f"notification changes must be a map, got {type(changes_raw).__name__}")
    return Notification(
        subscription_id=_get_uint(raw, 2, "subscriptionId"),
        endpoint_id=_get_uint(raw, 3, "endpointId"),
        feature_id=_get_uint(raw, 4, "featureId"),
        changes=changes,
    )


# ---------------------------------------------------------------------------
# Control message
# ---------------------------------------------------------------------------


def encode_control_message(msg: ControlMessage) -> bytes:
    out: dict[int, Any] = {1: int(msg.type)}
    if msg.sequence:
        out[2] = msg.sequence
    return marshal(out)


def decode_control_message(data: bytes) -> ControlMessage:
    raw = unmarshal(data)
    if not isinstance(raw, dict):
        raise ValueError(f"control message must be a CBOR map, got {type(raw).__name__}")
    _require_int_keys(raw)
    return ControlMessage(
        type=ControlMessageType(_get_uint(raw, 1, "type")),
        sequence=_get_uint_optional(raw, 2, default=0),
    )


# ---------------------------------------------------------------------------
# Internal map helpers
# ---------------------------------------------------------------------------


def _require_int_keys(m: dict[Any, Any]) -> None:
    """MASH messages use integer keys at the top level (and structurally,
    everywhere). Mirrors mash-go ``PeekMessageType`` codec.go:189-196 and the
    test in TestCBORValidationStringKeys.
    """
    for k in m:
        if not isinstance(k, int) or isinstance(k, bool):
            raise ValueError(f"non-integer top-level key in MASH message: {k!r}")


def _get_uint(m: dict[Any, Any], key: int, name: str) -> int:
    if key not in m:
        raise ValueError(f"missing required field {name} (key {key})")
    v = m[key]
    if not isinstance(v, int) or isinstance(v, bool):
        raise ValueError(f"field {name} must be an integer, got {type(v).__name__}")
    if v < 0:
        raise ValueError(f"field {name} must be non-negative, got {v}")
    return v


def _get_uint_optional(m: dict[Any, Any], key: int, default: int = 0) -> int:
    if key not in m:
        return default
    v = m[key]
    if not isinstance(v, int) or isinstance(v, bool):
        raise ValueError(f"field {key} must be an integer if present, got {type(v).__name__}")
    return v
