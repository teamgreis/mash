"""Wire message dataclasses.

Ports of the structs in mash-go/pkg/wire/message.go. Each dataclass mirrors
the Go struct fields and uses snake_case names. CBOR (en|de)coding lives in
mash_py.wire.codec — these classes are pure data containers.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from mash_py.wire.operations import (
    ControlMessageType,
    Operation,
    Status,
)


@dataclass
class Request:
    """MASH request from controller to device (or vice-versa post-pairing).

    Wire form (mash-go/pkg/wire/message.go:25-41):
        {1: messageId, 2: operation, 3: endpointId, 4: featureId, 5: payload}

    `payload` is operation-dependent and may be ``None``. Encoder omits key 5
    when payload is None (Go's ``omitempty``).
    """

    message_id: int
    operation: Operation
    endpoint_id: int
    feature_id: int
    payload: Any = None

    def validate(self) -> None:
        """Reject the same things mash-go's Request.Validate rejects.

        Mirrors mash-go/pkg/wire/message.go:44-52. Validates the raw integer
        rather than reconstructing the IntEnum so that decoded-from-wire
        invalid operations (e.g. 99) raise a *MASH* "invalid operation" error
        and not Python's generic "X is not a valid Operation".
        """
        from mash_py.wire.operations import NOTIFICATION_MESSAGE_ID

        if self.message_id == NOTIFICATION_MESSAGE_ID:
            raise ValueError("messageId 0 is reserved for notifications")
        op = int(self.operation)
        if op < int(Operation.READ) or op > int(Operation.INVOKE):
            raise ValueError(f"invalid operation: {op}")


@dataclass
class Response:
    """MASH response message.

    Wire form (mash-go/pkg/wire/message.go:54-67):
        {1: messageId, 2: status, 3: payload}
    """

    message_id: int
    status: Status
    payload: Any = None

    def is_success(self) -> bool:
        return Status(self.status).is_success()


@dataclass
class Notification:
    """Subscription notification (messageId=0 sentinel).

    Wire form (mash-go/pkg/wire/message.go:74-90):
        {1: 0, 2: subscriptionId, 3: endpointId, 4: featureId, 5: changes}
    """

    subscription_id: int
    endpoint_id: int
    feature_id: int
    changes: dict[int, Any] = field(default_factory=dict)


@dataclass
class ControlMessage:
    """Transport-level control (ping/pong/close).

    Wire form (mash-go/pkg/wire/message.go:314-319):
        {1: type, 2: sequence}  # sequence omitted when zero
    """

    type: ControlMessageType
    sequence: int = 0


# ---------------------------------------------------------------------------
# Operation-specific payload structs
# ---------------------------------------------------------------------------


@dataclass
class ReadPayload:
    """Read request payload. Wire form is a bare CBOR array of attribute IDs.

    Per docs/interaction-model.md §4.1: ``5: [1, 2, 3]`` (NOT ``{1: [1,2,3]}``).
    Empty array means "read all attributes".

    The Go code ALSO accepts ``{1: [...]}`` defensively
    (mash-go/pkg/wire/message.go:99-143), but the canonical wire form is the
    bare array, and that is what mash-py emits.
    """

    attribute_ids: list[int] = field(default_factory=list)


@dataclass
class SubscribePayload:
    """Subscribe request payload.

    Wire form (mash-go/pkg/wire/message.go:196-209):
        {1: [attributeIds], 2: minIntervalMs, 3: maxIntervalMs}

    Each field has ``omitempty`` semantics: empty list / zero interval is
    omitted from the encoding.
    """

    attribute_ids: list[int] = field(default_factory=list)
    min_interval: int = 0  # ms — minimum interval between notifications
    max_interval: int = 0  # ms — maximum heartbeat interval


@dataclass
class SubscribeResponsePayload:
    """Subscribe response payload.

    Wire form (mash-go/pkg/wire/message.go:269-272):
        {1: subscriptionId, 2: currentValues}
    """

    subscription_id: int
    current_values: dict[int, Any] = field(default_factory=dict)


@dataclass
class UnsubscribePayload:
    """Unsubscribe payload (sent as Subscribe op with endpointId=featureId=0).

    Wire form (mash-go/pkg/wire/message.go:282-284):
        {1: subscriptionId}
    """

    subscription_id: int


@dataclass
class InvokePayload:
    """Invoke request payload.

    Wire form (mash-go/pkg/wire/message.go:294-297):
        {1: commandId, 2: parameters}

    ``parameters`` is command-specific and encoded as-is. Map-keyed parameter
    sets typically use uint8 keys (matching feature command-parameter IDs).
    """

    command_id: int
    parameters: Any = None


@dataclass
class ErrorPayload:
    """Error detail attached to a non-success Response.

    Wire form (mash-go/pkg/wire/message.go:310-312):
        {1: message}  # message omitted if empty
    """

    message: str = ""
