"""Unit tests for mash_py.wire.peek.peek_message_type.

Mirrors mash-go's PeekMessageType behaviour — same inputs must classify the
same way. These tests construct messages via the encoder so the inputs stay
in lockstep with the codec.
"""

from __future__ import annotations

import cbor2
import pytest

from mash_py.wire import (
    ControlMessage,
    ControlMessageType,
    MessageType,
    Notification,
    Operation,
    ReadPayload,
    Request,
    Response,
    Status,
    encode_control_message,
    encode_notification,
    encode_request,
    encode_response,
    peek_message_type,
)


class TestPeekClassification:
    def test_request_read(self) -> None:
        req = Request(
            message_id=1,
            operation=Operation.READ,
            endpoint_id=1,
            feature_id=2,
            payload=ReadPayload(attribute_ids=[1]),
        )
        assert peek_message_type(encode_request(req)) == MessageType.REQUEST

    def test_request_write_to_device_root(self) -> None:
        # endpointId=0 (DEVICE_ROOT) is valid; peek must not misclassify it.
        req = Request(
            message_id=2,
            operation=Operation.WRITE,
            endpoint_id=0,
            feature_id=1,
            payload={1: 100},
        )
        assert peek_message_type(encode_request(req)) == MessageType.REQUEST

    def test_response_success(self) -> None:
        resp = Response(message_id=1, status=Status.SUCCESS, payload={1: 100})
        assert peek_message_type(encode_response(resp)) == MessageType.RESPONSE

    def test_response_error_status_5_with_large_msg_id(self) -> None:
        # An error response with a payload-less body and msgID outside [1..3]
        # peeks as RESPONSE (the control-overlap doesn't trigger).
        resp = Response(message_id=42, status=Status.INVALID_PARAMETER)  # status=5
        assert peek_message_type(encode_response(resp)) == MessageType.RESPONSE

    def test_response_error_with_no_payload_msg_id_in_control_range(self) -> None:
        """KNOWN AMBIGUITY (mash-py-spec-issues.md Issue #2).

        A response of the form ``{1: msgID, 2: status}`` with no payload and
        msgID in {1,2,3} is byte-indistinguishable from a control message and
        peek will return CONTROL. Real receivers must disambiguate via session
        state (in-flight messageId tracking). This test pins mash-py to the
        same behavior as mash-go (verified by running pkg/wire.PeekMessageType
        against the same bytes).
        """
        resp = Response(message_id=1, status=Status.BUSY)  # status 9
        assert peek_message_type(encode_response(resp)) == MessageType.CONTROL

    def test_notification(self) -> None:
        n = Notification(
            subscription_id=1, endpoint_id=1, feature_id=1, changes={1: 1}
        )
        assert peek_message_type(encode_notification(n)) == MessageType.NOTIFICATION

    @pytest.mark.parametrize(
        "ctrl_type",
        [ControlMessageType.PING, ControlMessageType.PONG, ControlMessageType.CLOSE],
    )
    def test_control(self, ctrl_type: ControlMessageType) -> None:
        msg = ControlMessage(type=ctrl_type, sequence=42 if ctrl_type != ControlMessageType.CLOSE else 0)
        assert peek_message_type(encode_control_message(msg)) == MessageType.CONTROL


class TestPeekRejection:
    def test_reject_string_keyed_map(self) -> None:
        with pytest.raises(ValueError, match="non-integer"):
            peek_message_type(bytes.fromhex("A1616101"))  # {"a": 1}

    def test_reject_non_map_top_level(self) -> None:
        # Top-level CBOR integer is not a map.
        with pytest.raises(ValueError, match="map"):
            peek_message_type(bytes.fromhex("01"))


class TestPeekDoesNotMisclassifyControl:
    """Regression guard for the most subtle case noted in the plan
    (mash-py-tdd-plan.md §7.1, "see Go classification at codec.go:208"):
    a control message has field 1 in {1..3} which overlaps Status values, so
    the classifier must check key 3/4 absence to disambiguate."""

    def test_control_with_seq_in_field_2_not_misread_as_response(self) -> None:
        # Ping with sequence=99 — field 2 is 99 (>4), naive logic might
        # classify as Response. The control-first check must win.
        ctrl = ControlMessage(type=ControlMessageType.PING, sequence=99)
        assert peek_message_type(encode_control_message(ctrl)) == MessageType.CONTROL

    def test_response_with_status_3_not_misread_as_control(self) -> None:
        # Response with status=3 (INVALID_ATTRIBUTE) shares field-1 range 1..3
        # with control types, but a Response carries messageId in field 1
        # and the status in field 2. With non-zero messageId AND no key 3/4,
        # the control classifier would WRONGLY trigger if we ignored field 2.
        # Construct such a response and verify classification.
        resp = Response(message_id=42, status=Status.INVALID_ATTRIBUTE)  # status=3
        # Encoded = {1: 42, 2: 3} — field1=42 NOT in [1..3], so control branch
        # is skipped, and field2=3 is in [1..4] (treated as operation), but
        # there's no endpoint/feature, so it falls through to default:Response.
        assert peek_message_type(encode_response(resp)) == MessageType.RESPONSE

    def test_response_with_message_id_in_control_range(self) -> None:
        # If messageId happens to be 1, 2, or 3, key 1 alone matches the
        # control-type range. Disambiguator is "no key 3/4 present".
        resp = Response(message_id=2, status=Status.SUCCESS, payload={1: 100})
        # Has key 3 (payload map), so NOT control.
        assert peek_message_type(encode_response(resp)) == MessageType.RESPONSE


class TestPeekHandlesMalformedRequest:
    def test_zero_operation_with_endpoint_and_feature_classifies_as_request(self) -> None:
        """Mirror Go codec.go:262-268: field2==0 + key3 + key4 → Request so
        the downstream decoder can reject it via Request.validate."""
        # Build {1: 5, 2: 0, 3: 1, 4: 2} — operation=0 is invalid but the
        # message looks like a request structurally.
        bad = cbor2.dumps({1: 5, 2: 0, 3: 1, 4: 2}, canonical=True)
        from mash_py.wire import MessageType

        assert peek_message_type(bad) == MessageType.REQUEST
