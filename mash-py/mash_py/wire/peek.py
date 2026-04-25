"""Classify a raw CBOR message as Request / Response / Notification / Control.

Direct port of mash-go ``PeekMessageType`` (pkg/wire/codec.go:181-272). The
Go classifier is the load-bearing dispatcher on every received frame, so the
algorithm here is intentionally a line-for-line translation rather than a
"cleaner" Pythonic version — matching its exact corner-case behaviour is more
important than any local readability win.

Classification rules, in order:

1. Top-level CBOR object must be a map with integer keys.
2. ``messageId`` (key 1) == 0  → NOTIFICATION.
3. Key 3 holds a CBOR map      → RESPONSE (payload is a map of attributes).
4. Key 1 in {1,2,3} AND no key 3/4 → CONTROL (ping/pong/close).
5. Field 2 > 4                 → RESPONSE (Status values 5-13 aren't valid Operations).
6. Field 2 in {1..4}: REQUEST when key 3/4 present (allowing endpoint=0).
7. Field 2 == 0 with key 3+4   → REQUEST (malformed-but-classifiable; the
   downstream decoder will reject it via ``Request.validate``).
8. Otherwise                   → RESPONSE (default).

KNOWN AMBIGUITY: when a response has ``msgID in {1,2,3}`` AND no payload, its
encoding ``{1: msgID, 2: status}`` is byte-indistinguishable from a control
message ``{1: type, 2: sequence}``, so this classifier returns CONTROL for
that case (matching mash-go). Real dispatchers disambiguate at the session
layer via in-flight messageId tracking; peek is a fast-path hint only. See
mash-py-spec-issues.md Issue #2.
"""

from __future__ import annotations

from typing import Any

from mash_py.wire._strict import strict_validate
from mash_py.wire.operations import NOTIFICATION_MESSAGE_ID, MessageType


def peek_message_type(data: bytes) -> MessageType:
    """Determine the type of a MASH wire message without fully decoding it.

    Raises ``ValueError`` if ``data`` is not valid CBOR or has non-integer
    top-level keys (mirrors mash-go's behaviour at codec.go:194).
    """
    import cbor2

    strict_validate(data)
    raw = cbor2.loads(data)

    if not isinstance(raw, dict):
        raise ValueError("CBOR message must be a map at the top level")

    # Reject string-keyed maps explicitly (mash-go codec.go:189-196).
    for k in raw:
        if not isinstance(k, int) or isinstance(k, bool):
            raise ValueError(f"invalid message: non-integer key {type(k).__name__}({k!r})")

    field1 = _to_uint(raw.get(1))
    field2 = _to_uint(raw.get(2))

    # Notification sentinel
    if field1 == NOTIFICATION_MESSAGE_ID:
        return MessageType.NOTIFICATION

    field3: Any = raw.get(3)

    # If field 3 is a map, it's a Response payload
    if isinstance(field3, dict):
        return MessageType.RESPONSE

    # Control message: field1 in {1..3} AND no key 3/4 present at all.
    # Must be checked BEFORE the field2>4 path because control messages
    # carry the sequence number in field 2 (any uint32). See codec.go:230-238.
    if (
        field1 is not None
        and 1 <= field1 <= 3
        and 3 not in raw
        and 4 not in raw
    ):
        return MessageType.CONTROL

    # Status > 4 (5-13) => Response (codec.go:241-244).
    if field2 is not None and field2 > 4:
        return MessageType.RESPONSE

    # Valid operation (1..4) with endpoint or feature => Request.
    if field2 is not None and 1 <= field2 <= 4:
        endpoint_id = _to_uint(field3)
        feature_id = _to_uint(raw.get(4))
        if (endpoint_id is not None and endpoint_id > 0) or (
            feature_id is not None and feature_id > 0
        ):
            return MessageType.REQUEST
        # endpoint==0 is valid (DEVICE_ROOT) — accept request if key 4 is set
        if 4 in raw:
            return MessageType.REQUEST

    # Field 2 missing or zero with both key 3+4 present:
    # treat as a malformed Request so DecodeRequest.validate rejects it.
    if (field2 is None or field2 == 0) and 3 in raw and 4 in raw:
        return MessageType.REQUEST

    return MessageType.RESPONSE


def _to_uint(v: Any) -> int | None:
    """Coerce a CBOR-decoded value to a non-negative int.

    Returns ``None`` if ``v`` is missing or not an integer. Booleans are
    rejected: in Python ``bool`` is a subtype of ``int`` and would otherwise
    sneak through.
    """
    if v is None:
        return None
    if isinstance(v, bool):
        return None
    if isinstance(v, int):
        return v if v >= 0 else None
    return None
