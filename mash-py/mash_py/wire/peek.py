"""Classify a raw CBOR message as Request / Response / Notification / Control.

Port of mash-go ``PeekMessageType`` (pkg/wire/codec.go:181-272).

Per DEC-083, every Control message frame begins with CBOR tag 77
(``D8 4D``). The tag prefix is the unique CONTROL marker; any frame without
it is application-layer (Request / Response / Notification) and is
classified by the messageId-based rules below.

Classification rules, in order:

1. Frame begins with ``D8 4D`` (tag 77)              → CONTROL
2. Top-level CBOR object must be a map with integer keys.
3. ``messageId`` (key 1) == 0                        → NOTIFICATION
4. Top-level map has key 4 (featureId)               → REQUEST
   (operation in field 2 may be 0; downstream ``Request.validate``
   rejects invalid operations explicitly.)
5. Otherwise                                         → RESPONSE.

The pre-DEC-083 heuristic that tried to disambiguate Control from
payload-less Response by inspecting field 1 / field 2 is gone — it was
needed only because the wire format permitted byte-identical encodings
for ``ControlMessage{Type:t, Sequence:s}`` and ``Response{MessageID:t, Status:s}``
when t ∈ {1,2,3} and there was no payload. The tag wrapper eliminates
that ambiguity at the wire level.
"""

from __future__ import annotations

from typing import Any

import cbor2

from mash_py.wire._mutable import to_mutable
from mash_py.wire._strict import strict_validate
from mash_py.wire.codec import CONTROL_MESSAGE_TAG
from mash_py.wire.operations import NOTIFICATION_MESSAGE_ID, MessageType


def peek_message_type(data: bytes) -> MessageType:
    """Determine the type of a MASH wire message without fully decoding it.

    Raises ``ValueError`` if ``data`` is not valid CBOR or has non-integer
    top-level keys.
    """
    # DEC-083: a frame beginning with CBOR tag 77 (`D8 4D`) is unambiguously
    # a control message. Check this prefix before any decode work.
    if (
        len(data) >= 2
        and data[0] == 0xD8
        and data[1] == CONTROL_MESSAGE_TAG
    ):
        return MessageType.CONTROL

    strict_validate(data)
    raw = to_mutable(cbor2.loads(data))

    if not isinstance(raw, dict):
        raise ValueError("CBOR message must be a map at the top level")

    # Reject string-keyed maps explicitly (mash-go codec.go:189-196).
    for k in raw:
        if not isinstance(k, int) or isinstance(k, bool):
            raise ValueError(f"invalid message: non-integer key {type(k).__name__}({k!r})")

    field1 = _to_uint(raw.get(1))

    # Notification sentinel
    if field1 == NOTIFICATION_MESSAGE_ID:
        return MessageType.NOTIFICATION

    # Request frames carry both endpointId (key 3) and featureId (key 4).
    # The presence of key 4 is the cleanest discriminator — Responses never
    # use key 4, and Notifications were already handled above.
    if 4 in raw:
        return MessageType.REQUEST

    # No key 4 → Response. Keys 1, 2, and an optional payload at key 3.
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
