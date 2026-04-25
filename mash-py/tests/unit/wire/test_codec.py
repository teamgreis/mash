"""Unit tests for mash_py.wire.codec.

Mirror the Go codec tests in mash-go/pkg/wire/codec_test.go. Covers:
- round-trip Request/Response/Notification/ControlMessage
- per-operation payload wire forms (Read array, Write map, Subscribe struct,
  Unsubscribe struct, Invoke struct)
- canonical key ordering in encoded output
- strict-decode rejection: NaN, Infinity, duplicate keys, string keys
- Request validation: messageId 0 reserved, operation must be 1..4
"""

from __future__ import annotations

import cbor2
import pytest
from hypothesis import given
from hypothesis import strategies as st

from mash_py.wire import (
    ControlMessage,
    ControlMessageType,
    ErrorPayload,
    InvokePayload,
    Notification,
    Operation,
    ReadPayload,
    Request,
    Response,
    Status,
    SubscribePayload,
    SubscribeResponsePayload,
    UnsubscribePayload,
    decode_control_message,
    decode_notification,
    decode_request,
    decode_response,
    encode_control_message,
    encode_notification,
    encode_request,
    encode_response,
)
from mash_py.wire._strict import StrictDecodeError

# ---------------------------------------------------------------------------
# Round-trip tests — one per operation variant
# ---------------------------------------------------------------------------


class TestRequestRoundTrip:
    def test_read_with_explicit_ids(self) -> None:
        req = Request(
            message_id=1,
            operation=Operation.READ,
            endpoint_id=1,
            feature_id=2,
            payload=ReadPayload(attribute_ids=[1, 2, 3]),
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.message_id == 1
        assert decoded.operation == Operation.READ
        assert decoded.endpoint_id == 1
        assert decoded.feature_id == 2
        # Read payload survives as a bare list (not wrapped in {1: [...]}).
        assert decoded.payload == [1, 2, 3]

    def test_read_all_attributes_empty_payload(self) -> None:
        req = Request(
            message_id=5,
            operation=Operation.READ,
            endpoint_id=1,
            feature_id=2,
            payload=ReadPayload(),  # empty list = read all
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.payload == []

    def test_write(self) -> None:
        req = Request(
            message_id=2,
            operation=Operation.WRITE,
            endpoint_id=1,
            feature_id=3,
            payload={21: 6_000_000},  # raw map[uint16]any
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.operation == Operation.WRITE
        assert decoded.payload == {21: 6_000_000}

    def test_subscribe_with_intervals(self) -> None:
        req = Request(
            message_id=3,
            operation=Operation.SUBSCRIBE,
            endpoint_id=1,
            feature_id=2,
            payload=SubscribePayload(
                attribute_ids=[1, 2, 3], min_interval=1000, max_interval=60000
            ),
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.payload == {1: [1, 2, 3], 2: 1000, 3: 60000}

    def test_subscribe_omitempty_drops_zero_intervals(self) -> None:
        """Per Go's omitempty: zero intervals must not appear on the wire."""
        req = Request(
            message_id=13,
            operation=Operation.SUBSCRIBE,
            endpoint_id=1,
            feature_id=2,
            payload=SubscribePayload(),  # all zero
        )
        data = encode_request(req)
        decoded = decode_request(data)
        # Encoded payload is an empty CBOR map ({}), not absent
        assert decoded.payload == {}

    def test_unsubscribe(self) -> None:
        req = Request(
            message_id=4,
            operation=Operation.SUBSCRIBE,
            endpoint_id=0,
            feature_id=0,
            payload=UnsubscribePayload(subscription_id=5001),
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.endpoint_id == 0
        assert decoded.feature_id == 0
        assert decoded.payload == {1: 5001}

    def test_invoke_with_parameters(self) -> None:
        req = Request(
            message_id=6,
            operation=Operation.INVOKE,
            endpoint_id=1,
            feature_id=3,
            payload=InvokePayload(command_id=1, parameters={1: 6_000_000, 4: 2}),
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.payload == {1: 1, 2: {1: 6_000_000, 4: 2}}

    def test_invoke_no_parameters(self) -> None:
        req = Request(
            message_id=8,
            operation=Operation.INVOKE,
            endpoint_id=1,
            feature_id=3,
            payload=InvokePayload(command_id=2),
        )
        data = encode_request(req)
        decoded = decode_request(data)
        assert decoded.payload == {1: 2}


class TestResponseRoundTrip:
    def test_success_with_attributes(self) -> None:
        resp = Response(
            message_id=1,
            status=Status.SUCCESS,
            payload={1: 5_000_000, 2: 200_000},
        )
        data = encode_response(resp)
        decoded = decode_response(data)
        assert decoded.status == Status.SUCCESS
        assert decoded.payload == {1: 5_000_000, 2: 200_000}

    def test_error_with_message(self) -> None:
        resp = Response(
            message_id=2,
            status=Status.INVALID_PARAMETER,
            payload=ErrorPayload(message="consumptionLimit must be >= 0"),
        )
        data = encode_response(resp)
        decoded = decode_response(data)
        assert decoded.status == Status.INVALID_PARAMETER
        assert decoded.payload == {1: "consumptionLimit must be >= 0"}

    def test_error_without_payload(self) -> None:
        resp = Response(message_id=14, status=Status.INVALID_ENDPOINT)
        data = encode_response(resp)
        decoded = decode_response(data)
        assert decoded.payload is None

    def test_subscribe_response(self) -> None:
        resp = Response(
            message_id=3,
            status=Status.SUCCESS,
            payload=SubscribeResponsePayload(
                subscription_id=5001,
                current_values={1: 5_000_000, 2: 200_000},
            ),
        )
        data = encode_response(resp)
        decoded = decode_response(data)
        assert decoded.payload == {1: 5001, 2: {1: 5_000_000, 2: 200_000}}

    def test_subscribe_response_omits_empty_priming(self) -> None:
        resp = Response(
            message_id=13,
            status=Status.SUCCESS,
            payload=SubscribeResponsePayload(subscription_id=5002),
        )
        data = encode_response(resp)
        decoded = decode_response(data)
        # current_values omitted when empty
        assert decoded.payload == {1: 5002}


class TestNotificationRoundTrip:
    def test_basic(self) -> None:
        n = Notification(
            subscription_id=5001,
            endpoint_id=1,
            feature_id=2,
            changes={1: 5_500_000},
        )
        data = encode_notification(n)
        decoded = decode_notification(data)
        assert decoded.subscription_id == 5001
        assert decoded.endpoint_id == 1
        assert decoded.feature_id == 2
        assert decoded.changes == {1: 5_500_000}

    def test_empty_changes_heartbeat(self) -> None:
        n = Notification(subscription_id=6000, endpoint_id=0, feature_id=1, changes={})
        data = encode_notification(n)
        decoded = decode_notification(data)
        assert decoded.changes == {}

    def test_decode_rejects_non_zero_message_id(self) -> None:
        """A CBOR-shaped notification message with messageId != 0 must be
        rejected — the sentinel is what makes it a notification.

        This guards the same check in mash-go codec.go:137.
        """
        # Build a forgery: Notification shape but messageId=99
        bad = cbor2.dumps({1: 99, 2: 5001, 3: 1, 4: 2, 5: {}}, canonical=True)
        with pytest.raises(ValueError, match="messageId="):
            decode_notification(bad)


class TestControlMessageRoundTrip:
    @pytest.mark.parametrize(
        "msg",
        [
            ControlMessage(type=ControlMessageType.PING, sequence=1),
            ControlMessage(type=ControlMessageType.PONG, sequence=1),
            ControlMessage(type=ControlMessageType.CLOSE),
            ControlMessage(type=ControlMessageType.PING, sequence=4_294_967_295),
        ],
    )
    def test_round_trip(self, msg: ControlMessage) -> None:
        data = encode_control_message(msg)
        decoded = decode_control_message(data)
        assert decoded.type == msg.type
        assert decoded.sequence == msg.sequence


class TestControlMessageDEC083:
    """Regression tests for the DEC-083 wire-format change (CBOR tag 77).

    The bug DEC-083 fixes: a payload-less Response with messageId in {1,2,3}
    used to encode byte-identically to a ControlMessage, so peek_message_type
    misclassified it as CONTROL — and the transport handler treated msgID=3
    as a Close, tearing down the connection. These tests pin the new
    invariants so a future refactor can't silently re-open the ambiguity.
    """

    def test_encoded_control_has_tag_prefix(self) -> None:
        """Every encoded control message must begin with `D8 4D` (tag 77)."""
        for msg in [
            ControlMessage(type=ControlMessageType.PING, sequence=1),
            ControlMessage(type=ControlMessageType.PONG, sequence=42),
            ControlMessage(type=ControlMessageType.CLOSE),
        ]:
            data = encode_control_message(msg)
            assert data[:2] == b"\xd8\x4d", f"missing tag prefix on {msg}: {data.hex()}"

    def test_decode_rejects_tagless_map(self) -> None:
        """A pre-DEC-083 ping (`{1:1, 2:1}` with no tag) must be rejected."""
        tagless_ping = bytes.fromhex("a201010201")
        with pytest.raises(ValueError, match="tag"):
            decode_control_message(tagless_ping)

    def test_decode_rejects_wrong_tag(self) -> None:
        """Only tag 77 is valid. Tag 78 wrapping the same map must error."""
        wrong_tag = bytes.fromhex("d84ea201010201")  # tag 78
        with pytest.raises(ValueError, match="tag 77"):
            decode_control_message(wrong_tag)

    def test_decode_rejects_tag_with_non_map_body(self) -> None:
        """Tag 77 wrapping something other than a CBOR map must error."""
        # D8 4D 0A — tag 77 wrapping integer 10
        with pytest.raises(ValueError, match="map"):
            decode_control_message(bytes.fromhex("d84d0a"))


# ---------------------------------------------------------------------------
# Validation tests
# ---------------------------------------------------------------------------


class TestRequestValidation:
    def test_message_id_zero_reserved(self) -> None:
        req = Request(message_id=0, operation=Operation.READ, endpoint_id=1, feature_id=2)
        with pytest.raises(ValueError, match="reserved"):
            encode_request(req)

    def test_invalid_operation(self) -> None:
        # Pass a raw int that is not in the Operation IntEnum range.
        # Bypass IntEnum's strict instantiation by going through the dataclass.
        req = Request(message_id=1, operation=Operation.READ, endpoint_id=1, feature_id=2)
        # mutate the raw int after construction so we can simulate a forged
        # invalid operation arriving via the wire.
        object.__setattr__(req, "operation", 99)
        with pytest.raises(ValueError, match="invalid operation"):
            encode_request(req)


# ---------------------------------------------------------------------------
# Strict-decode rejection — mirrors Go TestCBORValidation* tests
# ---------------------------------------------------------------------------


class TestStrictDecode:
    def test_reject_nan(self) -> None:
        # CBOR half-precision NaN: F9 7E 00
        with pytest.raises(StrictDecodeError, match="NaN"):
            decode_request(bytes.fromhex("F97E00"))

    def test_reject_positive_infinity(self) -> None:
        with pytest.raises(StrictDecodeError, match="Infinity"):
            decode_request(bytes.fromhex("F97C00"))

    def test_reject_negative_infinity(self) -> None:
        with pytest.raises(StrictDecodeError, match="Infinity"):
            decode_request(bytes.fromhex("F9FC00"))

    def test_reject_single_precision_nan(self) -> None:
        # CBOR single-precision NaN: FA 7FC00000
        with pytest.raises(StrictDecodeError, match="NaN"):
            decode_request(bytes.fromhex("FA7FC00000"))

    def test_reject_double_precision_inf(self) -> None:
        # CBOR double-precision +Inf: FB 7FF0000000000000
        with pytest.raises(StrictDecodeError, match="Infinity"):
            decode_request(bytes.fromhex("FB7FF0000000000000"))

    def test_reject_duplicate_top_level_key(self) -> None:
        # Hand-crafted: A3 01 19 30 39 01 02 02 03  =>  {1:12345, 1:2, 2:3}
        with pytest.raises(StrictDecodeError, match="duplicate"):
            decode_request(bytes.fromhex("A30119303901020203"))

    def test_reject_duplicate_nested_key(self) -> None:
        # {1: {1: 1, 1: 2}}  -- duplicate inside payload
        # A1 01 A2 01 01 01 02
        nested_dup = bytes.fromhex("A101A2010101 02".replace(" ", ""))
        with pytest.raises(StrictDecodeError, match="duplicate"):
            decode_request(nested_dup)

    def test_reject_string_keys(self) -> None:
        # {"a": 1}  =>  A1 61 61 01
        with pytest.raises(ValueError, match="non-integer"):
            decode_request(bytes.fromhex("A1616101"))

    def test_reject_trailing_bytes(self) -> None:
        # A valid integer (0x01) followed by a stray byte (0x02)
        with pytest.raises(StrictDecodeError, match="trailing"):
            decode_request(bytes.fromhex("0102"))


# ---------------------------------------------------------------------------
# Canonical encoding
# ---------------------------------------------------------------------------


class TestCanonicalOrdering:
    def test_keys_sorted_in_output(self) -> None:
        """cbor2's canonical mode sorts keys length-first then bytewise.

        For all-small (< 24) integer keys this collapses to numeric order,
        matching fxamacker's SortCanonical (mash-go/pkg/wire/codec.go:23).
        """
        # Build a Notification that internally constructs the dict in
        # numerical order; assert the byte stream has key 1 before key 2 etc.
        n = Notification(
            subscription_id=5001,
            endpoint_id=1,
            feature_id=2,
            changes={5: 50, 1: 10, 3: 30, 2: 20, 4: 40},
        )
        data = encode_notification(n)
        # Decode back and inspect the changes map order — cbor2 returns dict
        # which is insertion-ordered in CPython 3.7+, so this checks the wire
        # ordering directly.
        decoded = decode_notification(data)
        assert list(decoded.changes.keys()) == [1, 2, 3, 4, 5]

    def test_request_message_compactness(self) -> None:
        """Mirror Go's TestCBORCompactness — encoded Request should be < 30
        bytes for typical case (mash-go/pkg/wire/codec_test.go:329-352)."""
        req = Request(
            message_id=12345,
            operation=Operation.READ,
            endpoint_id=1,
            feature_id=2,
            payload=ReadPayload(attribute_ids=[1, 2, 3]),
        )
        data = encode_request(req)
        assert len(data) < 30, f"encoded length {len(data)} bytes exceeds budget"


# ---------------------------------------------------------------------------
# Property test: random integer-keyed maps round-trip
# ---------------------------------------------------------------------------


@given(
    changes=st.dictionaries(
        keys=st.integers(min_value=0, max_value=65535),
        values=st.integers(min_value=-(2**63), max_value=(2**63) - 1),
        max_size=20,
    ),
)
def test_notification_random_changes_round_trip(changes: dict[int, int]) -> None:
    """Random integer-keyed change maps must encode and decode losslessly.

    This guards against integer-encoding edge cases (small/short/long forms
    in CBOR) that could diverge from Go's canonical encoding.
    """
    n = Notification(
        subscription_id=42, endpoint_id=1, feature_id=1, changes=dict(changes)
    )
    decoded = decode_notification(encode_notification(n))
    assert decoded.changes == changes
