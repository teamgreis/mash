"""Operation, Status, MessageType, and ControlMessageType enums.

Ports:
- mash-go/pkg/wire/operation.go (Operation)
- mash-go/pkg/wire/status.go (Status)
- mash-go/pkg/wire/codec.go:163-171 (MessageType)
- mash-go/pkg/wire/message.go:316-347 (ControlMessageType)

All numeric values match the Go reference exactly — they appear on the wire,
so divergence would break interop.
"""

from __future__ import annotations

from enum import IntEnum

# MessageID 0 is reserved to indicate a notification message.
# See mash-go/pkg/wire/message.go:22.
NOTIFICATION_MESSAGE_ID: int = 0


class Operation(IntEnum):
    """MASH protocol operation. Matter-style 4-operation model.

    See docs/interaction-model.md §2.
    """

    READ = 1
    WRITE = 2
    SUBSCRIBE = 3
    INVOKE = 4

    def is_valid(self) -> bool:
        return Operation.READ <= self <= Operation.INVOKE

    def __str__(self) -> str:
        return self.name.capitalize()


class Status(IntEnum):
    """Response status code. 0 = success, 1-13 = error codes.

    See mash-go/pkg/wire/status.go.
    """

    SUCCESS = 0
    INVALID_ENDPOINT = 1
    INVALID_FEATURE = 2
    INVALID_ATTRIBUTE = 3
    INVALID_COMMAND = 4
    INVALID_PARAMETER = 5
    READ_ONLY = 6
    WRITE_ONLY = 7
    NOT_AUTHORIZED = 8
    BUSY = 9
    UNSUPPORTED = 10
    CONSTRAINT_ERROR = 11
    TIMEOUT = 12
    RESOURCE_EXHAUSTED = 13

    def is_success(self) -> bool:
        return self == Status.SUCCESS

    def is_error(self) -> bool:
        return self != Status.SUCCESS


class ControlMessageType(IntEnum):
    """Transport-level control message type.

    See mash-go/pkg/wire/message.go:322-333.
    """

    PING = 1
    PONG = 2
    CLOSE = 3


class MessageType(IntEnum):
    """Classification result from peek_message_type.

    See mash-go/pkg/wire/codec.go:163-171.
    """

    UNKNOWN = 0
    REQUEST = 1
    RESPONSE = 2
    NOTIFICATION = 3
    CONTROL = 4
