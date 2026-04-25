package wire

import (
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "read request",
			req: Request{
				MessageID:  1,
				Operation:  OpRead,
				EndpointID: 1,
				FeatureID:  2,
				Payload:    []uint16{1, 2, 3},
			},
		},
		{
			name: "write request",
			req: Request{
				MessageID:  2,
				Operation:  OpWrite,
				EndpointID: 1,
				FeatureID:  3,
				Payload: map[uint16]any{
					21: int64(6000000),
				},
			},
		},
		{
			name: "subscribe request",
			req: Request{
				MessageID:  3,
				Operation:  OpSubscribe,
				EndpointID: 1,
				FeatureID:  2,
				Payload: SubscribePayload{
					AttributeIDs: []uint16{1, 2, 3},
					MinInterval:  1000,
					MaxInterval:  60000,
				},
			},
		},
		{
			name: "invoke request",
			req: Request{
				MessageID:  4,
				Operation:  OpInvoke,
				EndpointID: 1,
				FeatureID:  3,
				Payload: InvokePayload{
					CommandID: 1,
					Parameters: map[uint8]any{
						1: int64(6000000),
						4: uint8(2),
					},
				},
			},
		},
		{
			name: "read all attributes",
			req: Request{
				MessageID:  5,
				Operation:  OpRead,
				EndpointID: 1,
				FeatureID:  2,
				Payload:    []uint16{}, // empty = read all
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			data, err := EncodeRequest(&tt.req)
			if err != nil {
				t.Fatalf("EncodeRequest failed: %v", err)
			}

			// Decode
			decoded, err := DecodeRequest(data)
			if err != nil {
				t.Fatalf("DecodeRequest failed: %v", err)
			}

			// Verify basic fields
			if decoded.MessageID != tt.req.MessageID {
				t.Errorf("MessageID mismatch: got %d, want %d", decoded.MessageID, tt.req.MessageID)
			}
			if decoded.Operation != tt.req.Operation {
				t.Errorf("Operation mismatch: got %v, want %v", decoded.Operation, tt.req.Operation)
			}
			if decoded.EndpointID != tt.req.EndpointID {
				t.Errorf("EndpointID mismatch: got %d, want %d", decoded.EndpointID, tt.req.EndpointID)
			}
			if decoded.FeatureID != tt.req.FeatureID {
				t.Errorf("FeatureID mismatch: got %d, want %d", decoded.FeatureID, tt.req.FeatureID)
			}
		})
	}
}

func TestResponseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		resp Response
	}{
		{
			name: "success response",
			resp: Response{
				MessageID: 1,
				Status:    StatusSuccess,
				Payload: map[uint16]any{
					1: int64(5000000),
					2: int64(200000),
				},
			},
		},
		{
			name: "error response",
			resp: Response{
				MessageID: 2,
				Status:    StatusInvalidParameter,
				Payload: ErrorPayload{
					Message: "consumptionLimit must be >= 0",
				},
			},
		},
		{
			name: "subscribe response",
			resp: Response{
				MessageID: 3,
				Status:    StatusSuccess,
				Payload: SubscribeResponsePayload{
					SubscriptionID: 5001,
					CurrentValues: map[uint16]any{
						1: int64(5000000),
						2: int64(200000),
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := EncodeResponse(&tt.resp)
			if err != nil {
				t.Fatalf("EncodeResponse failed: %v", err)
			}

			decoded, err := DecodeResponse(data)
			if err != nil {
				t.Fatalf("DecodeResponse failed: %v", err)
			}

			if decoded.MessageID != tt.resp.MessageID {
				t.Errorf("MessageID mismatch: got %d, want %d", decoded.MessageID, tt.resp.MessageID)
			}
			if decoded.Status != tt.resp.Status {
				t.Errorf("Status mismatch: got %v, want %v", decoded.Status, tt.resp.Status)
			}
		})
	}
}

func TestNotificationRoundTrip(t *testing.T) {
	notif := Notification{
		SubscriptionID: 5001,
		EndpointID:     1,
		FeatureID:      2,
		Changes: map[uint16]any{
			1: int64(5500000),
		},
	}

	data, err := EncodeNotification(&notif)
	if err != nil {
		t.Fatalf("EncodeNotification failed: %v", err)
	}

	decoded, err := DecodeNotification(data)
	if err != nil {
		t.Fatalf("DecodeNotification failed: %v", err)
	}

	if decoded.SubscriptionID != notif.SubscriptionID {
		t.Errorf("SubscriptionID mismatch: got %d, want %d", decoded.SubscriptionID, notif.SubscriptionID)
	}
	if decoded.EndpointID != notif.EndpointID {
		t.Errorf("EndpointID mismatch: got %d, want %d", decoded.EndpointID, notif.EndpointID)
	}
	if decoded.FeatureID != notif.FeatureID {
		t.Errorf("FeatureID mismatch: got %d, want %d", decoded.FeatureID, notif.FeatureID)
	}
}

func TestControlMessageRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  ControlMessage
	}{
		{
			name: "ping",
			msg:  ControlMessage{Type: ControlPing, Sequence: 1},
		},
		{
			name: "pong",
			msg:  ControlMessage{Type: ControlPong, Sequence: 1},
		},
		{
			name: "close",
			msg:  ControlMessage{Type: ControlClose},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := EncodeControlMessage(&tt.msg)
			if err != nil {
				t.Fatalf("EncodeControlMessage failed: %v", err)
			}

			decoded, err := DecodeControlMessage(data)
			if err != nil {
				t.Fatalf("DecodeControlMessage failed: %v", err)
			}

			if decoded.Type != tt.msg.Type {
				t.Errorf("Type mismatch: got %v, want %v", decoded.Type, tt.msg.Type)
			}
			if decoded.Sequence != tt.msg.Sequence {
				t.Errorf("Sequence mismatch: got %d, want %d", decoded.Sequence, tt.msg.Sequence)
			}
		})
	}
}

// TestControlMessageHasTagPrefix asserts that every encoded control message
// begins with the DEC-083 tag prefix `D8 4D`. If a future refactor of
// EncodeControlMessage drops the tag (e.g., reverting to a bare CBOR map),
// this test fails before any cross-language interop test can.
func TestControlMessageHasTagPrefix(t *testing.T) {
	cases := []ControlMessage{
		{Type: ControlPing, Sequence: 1},
		{Type: ControlPong, Sequence: 1},
		{Type: ControlClose},
		{Type: ControlPing, Sequence: 4_294_967_295},
	}
	for _, msg := range cases {
		data, err := EncodeControlMessage(&msg)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if len(data) < 2 || data[0] != 0xD8 || data[1] != 0x4D {
			t.Errorf("missing tag-77 prefix on %v: got %x", msg, data)
		}
	}
}

// TestDecodeControlMessageRejectsTaglessMap is the load-bearing regression
// guard for DEC-083: a CBOR map that *looks* like a control message
// (`{1: type, 2: sequence}`) without the tag wrapper MUST be rejected.
// Pre-DEC-083 this byte form was the canonical control encoding; accepting
// it now would re-open the ambiguity with payload-less Responses.
func TestDecodeControlMessageRejectsTaglessMap(t *testing.T) {
	// Hand-craft `{1:1, 2:1}` — pre-DEC-083 ping(seq=1).
	taglessPing := []byte{0xA2, 0x01, 0x01, 0x02, 0x01}
	if _, err := DecodeControlMessage(taglessPing); err == nil {
		t.Errorf("DecodeControlMessage accepted a tagless map; DEC-083 requires tag 77")
	}
}

// TestDecodeControlMessageRejectsWrongTag pins the exact tag number to 77.
// A future change that switched to a different tag (e.g., for IANA-assigned
// reasons) should be a deliberate, code-visible diff.
func TestDecodeControlMessageRejectsWrongTag(t *testing.T) {
	// Tag 78 wrapping a valid control map: D8 4E A2 01 01 02 01
	wrongTag := []byte{0xD8, 0x4E, 0xA2, 0x01, 0x01, 0x02, 0x01}
	if _, err := DecodeControlMessage(wrongTag); err == nil {
		t.Errorf("DecodeControlMessage accepted tag 78; only tag 77 is valid")
	}
}

// TestPeekClassifiesPreviouslyAmbiguousResponseAsResponse is the explicit
// regression for the bug DEC-083 fixes. Pre-DEC-083, this exact byte
// sequence (a payload-less Response with msgID in {1,2,3}) was misclassified
// by PeekMessageType as CONTROL — causing the transport handler to treat it
// as a Close and tear down the connection. The tagless wire form is now
// unambiguously RESPONSE.
//
// If this test fails after a future peek refactor, do NOT relax the
// assertion. The catastrophic case (msgID=3 + Status=Success → connection
// close) was the proximate cause of DEC-083; preserving its non-control
// classification is the contract.
func TestPeekClassifiesPreviouslyAmbiguousResponseAsResponse(t *testing.T) {
	cases := []struct {
		name string
		resp Response
	}{
		{"msgID=1 success", Response{MessageID: 1, Status: StatusSuccess}},
		{"msgID=2 busy", Response{MessageID: 2, Status: StatusBusy}},
		// The catastrophic case — msgID=3 + payload-less success.
		// Pre-DEC-083 this peeked as CONTROL Close and the entire
		// connection was torn down on receipt.
		{"msgID=3 success (catastrophic case)", Response{MessageID: 3, Status: StatusSuccess}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodeResponse(&tc.resp)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			mt, err := PeekMessageType(data)
			if err != nil {
				t.Fatalf("peek: %v", err)
			}
			if mt != MessageTypeResponse {
				t.Errorf("got %v, want MessageTypeResponse (DEC-083 regression)", mt)
			}
		})
	}
}

// TestPeekClassifiesTaggedFrameAsControl complements the test above:
// a tag-77-wrapped frame must classify as CONTROL even when its contents
// would otherwise look like a Response (we hand-craft the worst case).
func TestPeekClassifiesTaggedFrameAsControl(t *testing.T) {
	// D8 4D A2 01 03 02 00 — tag77({1:3, 2:0})
	// Inner map happens to match Response{MsgID:3, Status:Success} byte-shape,
	// but the tag prefix forces the CONTROL classification.
	tagged := []byte{0xD8, 0x4D, 0xA2, 0x01, 0x03, 0x02, 0x00}
	mt, err := PeekMessageType(tagged)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if mt != MessageTypeControl {
		t.Errorf("got %v, want MessageTypeControl", mt)
	}
}

func TestRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{
			name: "valid request",
			req: Request{
				MessageID:  1,
				Operation:  OpRead,
				EndpointID: 1,
				FeatureID:  2,
			},
			wantErr: false,
		},
		{
			name: "messageId 0 reserved",
			req: Request{
				MessageID:  0,
				Operation:  OpRead,
				EndpointID: 1,
				FeatureID:  2,
			},
			wantErr: true,
		},
		{
			name: "invalid operation",
			req: Request{
				MessageID:  1,
				Operation:  Operation(99),
				EndpointID: 1,
				FeatureID:  2,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNullableVsAbsent(t *testing.T) {
	// Test that null values are preserved (not treated as absent)
	payload := map[uint16]any{
		1: uint64(5000000), // Has value
		2: nil,             // Explicitly null
		// Key 3 is absent
	}

	data, err := Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded map[uint16]any
	if err := Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Check value present (CBOR decodes positive integers as uint64)
	if v, ok := decoded[1]; !ok {
		t.Errorf("Key 1 should be present")
	} else if v != uint64(5000000) {
		t.Errorf("Key 1: got %v (%T), want 5000000", v, v)
	}

	// Check null preserved
	if v, ok := decoded[2]; !ok {
		t.Errorf("Key 2 should be present (with null value)")
	} else if v != nil {
		t.Errorf("Key 2: got %v, want nil", v)
	}

	// Check absent key
	if _, ok := decoded[3]; ok {
		t.Errorf("Key 3 should be absent")
	}
}

func TestCBORCompactness(t *testing.T) {
	// Verify that CBOR with integer keys is reasonably compact
	req := Request{
		MessageID:  12345,
		Operation:  OpRead,
		EndpointID: 1,
		FeatureID:  2,
		Payload:    []uint16{1, 2, 3},
	}

	data, err := EncodeRequest(&req)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	// Should be much smaller than JSON equivalent
	// JSON: {"1":12345,"2":1,"3":1,"4":2,"5":[1,2,3]} = ~40 bytes
	// CBOR with integer keys should be ~15-20 bytes
	if len(data) > 30 {
		t.Errorf("CBOR encoding too large: %d bytes (expected < 30)", len(data))
	}

	t.Logf("CBOR size: %d bytes", len(data))
}

func TestUnknownFieldsIgnored(t *testing.T) {
	// Test forward compatibility: unknown fields should be ignored
	// This simulates receiving a message from a newer protocol version

	// Create a map with extra fields
	msg := map[int]any{
		1:  uint32(1),         // messageId
		2:  uint8(1),          // operation
		3:  uint8(1),          // endpointId
		4:  uint8(2),          // featureId
		5:  []uint16{1, 2, 3}, // payload
		99: "future field",    // unknown field from future version
	}

	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Should decode without error, ignoring unknown field
	decoded, err := DecodeRequest(data)
	if err != nil {
		t.Fatalf("DecodeRequest should succeed with unknown fields: %v", err)
	}

	if decoded.MessageID != 1 {
		t.Errorf("MessageID mismatch: got %d, want 1", decoded.MessageID)
	}
}

func TestClone(t *testing.T) {
	original := Request{
		MessageID:  1,
		Operation:  OpRead,
		EndpointID: 1,
		FeatureID:  2,
		Payload:    []uint16{1, 2, 3},
	}

	cloned, err := Clone(original)
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if cloned.MessageID != original.MessageID {
		t.Errorf("MessageID mismatch")
	}
	if cloned.Operation != original.Operation {
		t.Errorf("Operation mismatch")
	}
}

func TestEqual(t *testing.T) {
	a := Request{
		MessageID:  1,
		Operation:  OpRead,
		EndpointID: 1,
		FeatureID:  2,
	}
	b := Request{
		MessageID:  1,
		Operation:  OpRead,
		EndpointID: 1,
		FeatureID:  2,
	}
	c := Request{
		MessageID:  2, // different
		Operation:  OpRead,
		EndpointID: 1,
		FeatureID:  2,
	}

	if !Equal(a, b) {
		t.Errorf("Equal(a, b) should be true")
	}
	if Equal(a, c) {
		t.Errorf("Equal(a, c) should be false")
	}
}

// TestToInt64 tests the type coercion helper for all supported numeric types.
func TestToInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		want     int64
		wantOK   bool
	}{
		{"int64", int64(12345), 12345, true},
		{"int", int(12345), 12345, true},
		{"int32", int32(12345), 12345, true},
		{"int16", int16(12345), 12345, true},
		{"int8", int8(123), 123, true},
		{"uint64", uint64(12345), 12345, true},
		{"uint32", uint32(12345), 12345, true},
		{"uint16", uint16(12345), 12345, true},
		{"uint8", uint8(123), 123, true},
		{"float64", float64(12345.0), 12345, true},
		{"float32", float32(12345.0), 12345, true},
		{"negative int64", int64(-5000000), -5000000, true},
		{"negative int", int(-5000000), -5000000, true},
		{"large int64", int64(5000000000), 5000000000, true},
		{"string", "12345", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
		{"slice", []int{1, 2, 3}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToInt64(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ToInt64(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("ToInt64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestToUint8Public tests the type coercion helper for uint8 conversion.
func TestToUint8Public(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   uint8
		wantOK bool
	}{
		{"uint8", uint8(123), 123, true},
		{"uint64", uint64(123), 123, true},
		{"uint32", uint32(123), 123, true},
		{"uint16", uint16(123), 123, true},
		{"int64", int64(123), 123, true},
		{"int32", int32(123), 123, true},
		{"int16", int16(123), 123, true},
		{"int8", int8(123), 123, true},
		{"int", int(123), 123, true},
		{"string", "123", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ToUint8Public(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ToUint8Public(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("ToUint8Public(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestCBORIntegerTypeRoundtrip verifies that int64 values survive CBOR encode/decode
// and can be correctly extracted using ToInt64 regardless of the decoded type.
func TestCBORIntegerTypeRoundtrip(t *testing.T) {
	tests := []struct {
		name  string
		value int64
	}{
		{"small positive", 5},
		{"medium positive", 12345},
		{"large positive", 5000000000},
		{"small negative", -5},
		{"medium negative", -12345},
		{"large negative", -5000000000},
		{"power milliwatts", 7200000}, // 7.2 kW
		{"zero", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode a map with the int64 value (simulating notification changes)
			original := map[uint16]any{
				1: tt.value,
			}

			data, err := Marshal(original)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			var decoded map[uint16]any
			if err := Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			// Extract using ToInt64 - this should work regardless of decoded type
			rawVal := decoded[1]
			got, ok := ToInt64(rawVal)
			if !ok {
				t.Errorf("ToInt64 failed for decoded value %v (type %T)", rawVal, rawVal)
			}
			if got != tt.value {
				t.Errorf("Value mismatch: got %d, want %d (decoded type: %T)", got, tt.value, rawVal)
			}
		})
	}
}

// TestCBORValidationStringKeys verifies that string keys in messages are rejected.
func TestCBORValidationStringKeys(t *testing.T) {
	// CBOR map with string keys - should be rejected by PeekMessageType
	// A2 68 6D657373616765 01 68 6F7065726174696F6E 01
	// = {"message": 1, "operation": 1}
	// Simpler: A1 61 61 01 = {"a": 1}
	stringKeyCBOR := []byte{0xA1, 0x61, 0x61, 0x01} // {"a": 1}

	_, err := PeekMessageType(stringKeyCBOR)
	if err == nil {
		t.Errorf("Expected error for string keys in CBOR message, got nil")
	}
}

// TestCBORValidationDuplicateKeys verifies that duplicate map keys are rejected.
func TestCBORValidationDuplicateKeys(t *testing.T) {
	// Hand-crafted CBOR with duplicate key 1:
	// A3       # map(3)
	//    01    # unsigned(1) - key 1
	//    19 3039 # unsigned(12345) - value
	//    01    # unsigned(1) - duplicate key 1
	//    02    # unsigned(2) - value
	//    02    # unsigned(2) - key 2
	//    03    # unsigned(3) - value
	duplicateKeyCBOR := []byte{0xA3, 0x01, 0x19, 0x30, 0x39, 0x01, 0x02, 0x02, 0x03}

	var result map[int]int
	err := Unmarshal(duplicateKeyCBOR, &result)
	if err == nil {
		t.Errorf("Expected error for duplicate CBOR keys, got nil (result: %v)", result)
	}
}

// TestCBORValidationNaN verifies that NaN float values are rejected.
func TestCBORValidationNaN(t *testing.T) {
	// CBOR NaN: F9 7E 00 (half-precision quiet NaN)
	nanCBOR := []byte{0xF9, 0x7E, 0x00}

	var result float64
	err := Unmarshal(nanCBOR, &result)
	if err == nil {
		t.Errorf("Expected error for NaN value, got nil (result: %v)", result)
	}
}

// TestCBORValidationInfinity verifies that Infinity float values are rejected.
func TestCBORValidationInfinity(t *testing.T) {
	// CBOR +Infinity: F9 7C 00 (half-precision positive infinity)
	infCBOR := []byte{0xF9, 0x7C, 0x00}

	var result float64
	err := Unmarshal(infCBOR, &result)
	if err == nil {
		t.Errorf("Expected error for Infinity value, got nil (result: %v)", result)
	}

	// CBOR -Infinity: F9 FC 00 (half-precision negative infinity)
	negInfCBOR := []byte{0xF9, 0xFC, 0x00}

	err = Unmarshal(negInfCBOR, &result)
	if err == nil {
		t.Errorf("Expected error for -Infinity value, got nil (result: %v)", result)
	}
}

// TestNotificationChangesTypeCoercion tests the full notification path type handling.
func TestNotificationChangesTypeCoercion(t *testing.T) {
	// Create a notification with int64 power value
	notif := Notification{
		SubscriptionID: 5001,
		EndpointID:     1,
		FeatureID:      2,
		Changes: map[uint16]any{
			1: int64(7200000), // ACActivePower: 7.2 kW in mW
		},
	}

	// Encode and decode
	data, err := EncodeNotification(&notif)
	if err != nil {
		t.Fatalf("EncodeNotification failed: %v", err)
	}

	decoded, err := DecodeNotification(data)
	if err != nil {
		t.Fatalf("DecodeNotification failed: %v", err)
	}

	// The raw value may be decoded as a different integer type
	rawVal := decoded.Changes[1]
	t.Logf("Decoded type: %T, value: %v", rawVal, rawVal)

	// Using ToInt64 should always work
	power, ok := ToInt64(rawVal)
	if !ok {
		t.Fatalf("ToInt64 failed for %v (type %T)", rawVal, rawVal)
	}
	if power != 7200000 {
		t.Errorf("Power mismatch: got %d, want 7200000", power)
	}

	// Direct type assertion may fail depending on CBOR decoder behavior
	// This demonstrates why ToInt64 is needed
	if _, directOK := rawVal.(int64); !directOK {
		t.Logf("Note: Direct int64 assertion failed (type is %T), ToInt64 correctly handles this", rawVal)
	}
}
