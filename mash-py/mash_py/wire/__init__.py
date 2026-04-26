"""MASH wire codec.

Ports of mash-go/pkg/wire — CBOR message encoding for Request, Response,
Notification, and Control messages, plus the message-type peek classifier.

The Go implementation is canonical; this package must produce byte-identical
output and accept the same inputs as fxamacker/cbor with the SortCanonical
encoder mode and DupMapKey/NaN/Inf-forbidden decoder mode used in
pkg/wire/codec.go:18-48.
"""

from __future__ import annotations

from mash_py.wire.codec import (
    CONTROL_MESSAGE_TAG,
    decode_control_message,
    decode_notification,
    decode_request,
    decode_response,
    encode_control_message,
    encode_notification,
    encode_request,
    encode_response,
)
from mash_py.wire.framing import (
    DEFAULT_MAX_MESSAGE_SIZE,
    LENGTH_PREFIX_SIZE,
    MIN_MESSAGE_SIZE,
    AsyncFrameReader,
    AsyncFrameWriter,
    Framer,
    FrameReader,
    FrameTruncatedError,
    FrameWriter,
    FramingError,
    MessageEmptyError,
    MessageTooLargeError,
    frame_size,
)
from mash_py.wire.operations import (
    NOTIFICATION_MESSAGE_ID,
    ControlMessageType,
    MessageType,
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
from mash_py.wire.peek import peek_message_type

__all__ = [
    "CONTROL_MESSAGE_TAG",
    "DEFAULT_MAX_MESSAGE_SIZE",
    "LENGTH_PREFIX_SIZE",
    "MIN_MESSAGE_SIZE",
    "NOTIFICATION_MESSAGE_ID",
    "AsyncFrameReader",
    "AsyncFrameWriter",
    "ControlMessage",
    "ControlMessageType",
    "ErrorPayload",
    "FrameReader",
    "FrameTruncatedError",
    "FrameWriter",
    "Framer",
    "FramingError",
    "InvokePayload",
    "MessageEmptyError",
    "MessageTooLargeError",
    "MessageType",
    "Notification",
    "Operation",
    "ReadPayload",
    "Request",
    "Response",
    "Status",
    "SubscribePayload",
    "SubscribeResponsePayload",
    "UnsubscribePayload",
    "decode_control_message",
    "decode_notification",
    "decode_request",
    "decode_response",
    "encode_control_message",
    "encode_notification",
    "encode_request",
    "encode_response",
    "frame_size",
    "peek_message_type",
]
