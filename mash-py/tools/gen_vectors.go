// Command gen_vectors emits JSON test vectors for the mash-py wire codec.
//
// It imports github.com/mash-protocol/mash-go/pkg/wire and encodes a curated
// set of canonical Request/Response/Notification/Control messages plus a
// handful of negative cases (string keys, duplicate keys, NaN/Infinity).
// Each vector is written as JSON to mash-py/tests/vectors/wire/ so the
// Python test suite can:
//
//  1. encode the same logical message and assert byte-identical CBOR output
//  2. decode the bytes and assert the expected fields round-trip
//  3. classify the bytes via peek_message_type and assert the expected type
//
// Run:
//
//	go run ./tools/gen_vectors.go -out ../tests/vectors/wire
//
// The plan (mash-py-tdd-plan.md §6) targets ~30 vectors for Phase 1; this
// program currently produces 32, comfortably above the acceptance gate.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mash-protocol/mash-go/pkg/wire"
)

// Vector is the on-disk format. Field names match what the Python tests
// expect — keep them in sync with tests/vectors/wire/*.py loaders.
type Vector struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	GoStructRepr  string `json:"go_struct_repr"`
	MessageType   string `json:"message_type"` // "request"|"response"|"notification"|"control"|"invalid"
	CBORHex       string `json:"cbor_hex"`
	ExpectedFields any   `json:"expected,omitempty"` // structural decode expectations
	ExpectError   string `json:"expect_error,omitempty"` // for negative vectors
}

func main() {
	out := flag.String("out", "../tests/vectors/wire", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail("mkdir: %v", err)
	}

	writeFile(filepath.Join(*out, "request_vectors.json"), buildRequests())
	writeFile(filepath.Join(*out, "response_vectors.json"), buildResponses())
	writeFile(filepath.Join(*out, "notification_vectors.json"), buildNotifications())
	writeFile(filepath.Join(*out, "control_vectors.json"), buildControls())
	writeFile(filepath.Join(*out, "peek_vectors.json"), buildPeekVectors())
	writeFile(filepath.Join(*out, "strict_vectors.json"), buildStrictVectors())

	fmt.Println("vectors written to", *out)
}

// ---------- Request vectors ----------

func buildRequests() []Vector {
	out := []Vector{}

	// 1. Read with explicit attribute IDs (canonical wire form: bare CBOR array).
	read := wire.Request{
		MessageID: 1, Operation: wire.OpRead, EndpointID: 1, FeatureID: 2,
		Payload: []uint16{1, 2, 3},
	}
	out = append(out, mkRequest("read_request_explicit_ids", "Read with explicit attribute IDs",
		"Request{MessageID:1, Operation:Read, EndpointID:1, FeatureID:2, Payload:[]uint16{1,2,3}}",
		read,
		map[string]any{
			"message_id": 1, "operation": 1, "endpoint_id": 1, "feature_id": 2,
			"payload": []int{1, 2, 3},
		},
	))

	// 2. Read all attributes (empty array)
	readAll := wire.Request{
		MessageID: 5, Operation: wire.OpRead, EndpointID: 1, FeatureID: 2,
		Payload: []uint16{},
	}
	out = append(out, mkRequest("read_request_all_attrs", "Read all attributes (empty array payload)",
		"Request{MessageID:5, Operation:Read, EndpointID:1, FeatureID:2, Payload:[]uint16{}}",
		readAll,
		map[string]any{
			"message_id": 5, "operation": 1, "endpoint_id": 1, "feature_id": 2,
			"payload": []int{},
		},
	))

	// 3. Read on DEVICE_ROOT (endpoint 0)
	readRoot := wire.Request{
		MessageID: 7, Operation: wire.OpRead, EndpointID: 0, FeatureID: 1,
		Payload: []uint16{1},
	}
	out = append(out, mkRequest("read_request_device_root", "Read against DEVICE_ROOT (endpoint=0)",
		"Request{MessageID:7, Operation:Read, EndpointID:0, FeatureID:1, Payload:[]uint16{1}}",
		readRoot,
		map[string]any{
			"message_id": 7, "operation": 1, "endpoint_id": 0, "feature_id": 1,
			"payload": []int{1},
		},
	))

	// 4. Write with int64 attribute value
	write := wire.Request{
		MessageID: 2, Operation: wire.OpWrite, EndpointID: 1, FeatureID: 3,
		Payload: map[uint16]any{21: int64(6000000)},
	}
	out = append(out, mkRequest("write_request", "Write a single attribute",
		"Request{Op:Write, Payload:map[uint16]any{21:int64(6000000)}}",
		write,
		map[string]any{
			"message_id": 2, "operation": 2, "endpoint_id": 1, "feature_id": 3,
			"payload": map[string]any{"21": 6000000},
		},
	))

	// 5. Write multiple attributes (canonical key order)
	writeMulti := wire.Request{
		MessageID: 11, Operation: wire.OpWrite, EndpointID: 1, FeatureID: 3,
		Payload: map[uint16]any{20: int64(5000000), 21: int64(6000000)},
	}
	out = append(out, mkRequest("write_request_multi_attr", "Write multiple attributes — verifies key sort",
		"Request{Op:Write, Payload:map[uint16]any{20:5000000, 21:6000000}}",
		writeMulti,
		map[string]any{
			"message_id": 11, "operation": 2, "endpoint_id": 1, "feature_id": 3,
			"payload": map[string]any{"20": 5000000, "21": 6000000},
		},
	))

	// 6. Subscribe with intervals
	sub := wire.Request{
		MessageID: 3, Operation: wire.OpSubscribe, EndpointID: 1, FeatureID: 2,
		Payload: &wire.SubscribePayload{
			AttributeIDs: []uint16{1, 2, 3},
			MinInterval:  1000,
			MaxInterval:  60000,
		},
	}
	out = append(out, mkRequest("subscribe_request", "Subscribe with attribute IDs and intervals",
		"Request{Op:Subscribe, Payload:SubscribePayload{IDs:[1,2,3], Min:1000, Max:60000}}",
		sub,
		map[string]any{
			"message_id": 3, "operation": 3, "endpoint_id": 1, "feature_id": 2,
			"payload": map[string]any{
				"1": []int{1, 2, 3},
				"2": 1000,
				"3": 60000,
			},
		},
	))

	// 7. Subscribe to all attributes (no payload fields)
	subAll := wire.Request{
		MessageID: 13, Operation: wire.OpSubscribe, EndpointID: 1, FeatureID: 2,
		Payload: &wire.SubscribePayload{},
	}
	out = append(out, mkRequest("subscribe_request_all", "Subscribe with empty payload (all attrs, no intervals)",
		"Request{Op:Subscribe, Payload:SubscribePayload{}}",
		subAll,
		map[string]any{
			"message_id": 13, "operation": 3, "endpoint_id": 1, "feature_id": 2,
			"payload": map[string]any{}, // omitempty leaves an empty CBOR map
		},
	))

	// 8. Unsubscribe (Subscribe op with endpoint=0, feature=0)
	unsub := wire.Request{
		MessageID: 4, Operation: wire.OpSubscribe, EndpointID: 0, FeatureID: 0,
		Payload: &wire.UnsubscribePayload{SubscriptionID: 5001},
	}
	out = append(out, mkRequest("unsubscribe_request", "Unsubscribe (Subscribe op + endpoint=feature=0)",
		"Request{Op:Subscribe, EP:0, Feat:0, Payload:UnsubscribePayload{ID:5001}}",
		unsub,
		map[string]any{
			"message_id": 4, "operation": 3, "endpoint_id": 0, "feature_id": 0,
			"payload": map[string]any{"1": 5001},
		},
	))

	// 9. Invoke with parameters
	inv := wire.Request{
		MessageID: 6, Operation: wire.OpInvoke, EndpointID: 1, FeatureID: 3,
		Payload: &wire.InvokePayload{
			CommandID: 1,
			Parameters: map[uint8]any{
				1: int64(6000000),
				4: uint8(2),
			},
		},
	}
	out = append(out, mkRequest("invoke_request", "Invoke with command parameters",
		"Request{Op:Invoke, Payload:InvokePayload{CmdID:1, Params:map[uint8]any{...}}}",
		inv,
		map[string]any{
			"message_id": 6, "operation": 4, "endpoint_id": 1, "feature_id": 3,
			"payload": map[string]any{
				"1": 1,
				"2": map[string]any{"1": 6000000, "4": 2},
			},
		},
	))

	// 10. Invoke with no parameters
	invNoParams := wire.Request{
		MessageID: 8, Operation: wire.OpInvoke, EndpointID: 1, FeatureID: 3,
		Payload: &wire.InvokePayload{CommandID: 2},
	}
	out = append(out, mkRequest("invoke_request_no_params", "Invoke command with no parameters",
		"Request{Op:Invoke, Payload:InvokePayload{CmdID:2}}",
		invNoParams,
		map[string]any{
			"message_id": 8, "operation": 4, "endpoint_id": 1, "feature_id": 3,
			"payload": map[string]any{"1": 2},
		},
	))

	// 11. Read with large messageId (boundary uint32)
	readBigID := wire.Request{
		MessageID: 4_294_967_295, Operation: wire.OpRead, EndpointID: 1, FeatureID: 1,
		Payload: []uint16{1},
	}
	out = append(out, mkRequest("read_request_max_message_id", "Read with messageId at uint32 max",
		"Request{MessageID:max-uint32, ...}",
		readBigID,
		map[string]any{
			"message_id": 4294967295, "operation": 1, "endpoint_id": 1, "feature_id": 1,
			"payload": []int{1},
		},
	))

	// 12. Write with negative int64 (production: discharge power)
	writeNeg := wire.Request{
		MessageID: 9, Operation: wire.OpWrite, EndpointID: 1, FeatureID: 3,
		Payload: map[uint16]any{1: int64(-5000000)},
	}
	out = append(out, mkRequest("write_request_negative_int", "Write negative int (discharge convention)",
		"Request{Op:Write, Payload:map[uint16]any{1:int64(-5000000)}}",
		writeNeg,
		map[string]any{
			"message_id": 9, "operation": 2, "endpoint_id": 1, "feature_id": 3,
			"payload": map[string]any{"1": -5000000},
		},
	))

	// 13. Subscribe with only minInterval set (boundary: omitempty edges)
	subMinOnly := wire.Request{
		MessageID: 15, Operation: wire.OpSubscribe, EndpointID: 1, FeatureID: 2,
		Payload: &wire.SubscribePayload{MinInterval: 500},
	}
	out = append(out, mkRequest("subscribe_request_min_only", "Subscribe with only minInterval set (other fields omitempty)",
		"Request{Op:Subscribe, Payload:SubscribePayload{Min:500}}",
		subMinOnly,
		map[string]any{
			"message_id": 15, "operation": 3, "endpoint_id": 1, "feature_id": 2,
			"payload": map[string]any{"2": 500},
		},
	))

	// 14. Read with attribute id at uint16 max boundary
	readMaxAttr := wire.Request{
		MessageID: 16, Operation: wire.OpRead, EndpointID: 1, FeatureID: 1,
		Payload: []uint16{65535},
	}
	out = append(out, mkRequest("read_request_max_attr_id", "Read with attribute ID at uint16 max",
		"Request{Op:Read, Payload:[]uint16{65535}}",
		readMaxAttr,
		map[string]any{
			"message_id": 16, "operation": 1, "endpoint_id": 1, "feature_id": 1,
			"payload": []int{65535},
		},
	))

	// 15. Write with mixed value types (int + bool + string)
	writeMixed := wire.Request{
		MessageID: 17, Operation: wire.OpWrite, EndpointID: 2, FeatureID: 4,
		Payload: map[uint16]any{
			1: int64(100),
			2: true,
			3: "hello",
		},
	}
	out = append(out, mkRequest("write_request_mixed_types", "Write with mixed value types per attribute",
		"Request{Op:Write, Payload:map[uint16]any{int+bool+string}}",
		writeMixed,
		map[string]any{
			"message_id": 17, "operation": 2, "endpoint_id": 2, "feature_id": 4,
			"payload": map[string]any{"1": 100, "2": true, "3": "hello"},
		},
	))

	return out
}

// ---------- Response vectors ----------

func buildResponses() []Vector {
	out := []Vector{}

	// 1. Read success with attribute values
	r1 := wire.Response{
		MessageID: 1, Status: wire.StatusSuccess,
		Payload: map[uint16]any{
			1: int64(5000000),
			2: int64(200000),
		},
	}
	out = append(out, mkResponse("read_response_success", "Read success with two attributes",
		"Response{Status:Success, Payload:map[uint16]any{1:5000000, 2:200000}}",
		r1,
		map[string]any{
			"message_id": 1, "status": 0,
			"payload": map[string]any{"1": 5000000, "2": 200000},
		},
	))

	// 2. Error response with ErrorPayload
	r2 := wire.Response{
		MessageID: 2, Status: wire.StatusInvalidParameter,
		Payload: &wire.ErrorPayload{Message: "consumptionLimit must be >= 0"},
	}
	out = append(out, mkResponse("error_response_with_message", "Error response carrying a message",
		"Response{Status:InvalidParameter, Payload:ErrorPayload{...}}",
		r2,
		map[string]any{
			"message_id": 2, "status": 5,
			"payload": map[string]any{"1": "consumptionLimit must be >= 0"},
		},
	))

	// 3. Error response with no payload (omitempty)
	r3 := wire.Response{
		MessageID: 14, Status: wire.StatusInvalidEndpoint,
	}
	out = append(out, mkResponse("error_response_no_payload", "Error response with no payload",
		"Response{Status:InvalidEndpoint}",
		r3,
		map[string]any{
			"message_id": 14, "status": 1,
			"payload": nil,
		},
	))

	// 4. Subscribe response with priming values
	r4 := wire.Response{
		MessageID: 3, Status: wire.StatusSuccess,
		Payload: &wire.SubscribeResponsePayload{
			SubscriptionID: 5001,
			CurrentValues: map[uint16]any{
				1: int64(5000000),
				2: int64(200000),
			},
		},
	}
	out = append(out, mkResponse("subscribe_response", "Subscribe success with priming values",
		"Response{Status:Success, Payload:SubscribeResponsePayload{...}}",
		r4,
		map[string]any{
			"message_id": 3, "status": 0,
			"payload": map[string]any{
				"1": 5001,
				"2": map[string]any{"1": 5000000, "2": 200000},
			},
		},
	))

	// 5. Subscribe response with no priming values
	r5 := wire.Response{
		MessageID: 13, Status: wire.StatusSuccess,
		Payload: &wire.SubscribeResponsePayload{SubscriptionID: 5002},
	}
	out = append(out, mkResponse("subscribe_response_no_priming", "Subscribe success with no priming values",
		"Response{Status:Success, Payload:SubscribeResponsePayload{ID:5002}}",
		r5,
		map[string]any{
			"message_id": 13, "status": 0,
			"payload": map[string]any{"1": 5002},
		},
	))

	// 6. Status timeout (boundary value 12)
	r6 := wire.Response{
		MessageID: 99, Status: wire.StatusTimeout,
	}
	out = append(out, mkResponse("error_response_timeout", "Timeout error (status 12)",
		"Response{Status:Timeout}",
		r6,
		map[string]any{"message_id": 99, "status": 12, "payload": nil},
	))

	// 7. Resource exhausted (boundary value 13)
	r7 := wire.Response{
		MessageID: 100, Status: wire.StatusResourceExhausted,
	}
	out = append(out, mkResponse("error_response_resource_exhausted", "Resource exhausted (status 13)",
		"Response{Status:ResourceExhausted}",
		r7,
		map[string]any{"message_id": 100, "status": 13, "payload": nil},
	))

	// 8. Write response with effective values
	r8 := wire.Response{
		MessageID: 11, Status: wire.StatusSuccess,
		Payload: map[uint16]any{
			20: int64(5000000),
			21: int64(6000000),
		},
	}
	out = append(out, mkResponse("write_response_effective_values", "Write response carrying effective values",
		"Response{Status:Success, Payload:map[uint16]any{20:5M, 21:6M}}",
		r8,
		map[string]any{
			"message_id": 11, "status": 0,
			"payload": map[string]any{"20": 5000000, "21": 6000000},
		},
	))

	// 9. Empty success response (Invoke with no return)
	r9 := wire.Response{MessageID: 12, Status: wire.StatusSuccess}
	out = append(out, mkResponse("invoke_response_no_return", "Invoke success with no return value",
		"Response{Status:Success}",
		r9,
		map[string]any{"message_id": 12, "status": 0, "payload": nil},
	))

	return out
}

// ---------- Notification vectors ----------

func buildNotifications() []Vector {
	out := []Vector{}

	n1 := wire.Notification{
		SubscriptionID: 5001, EndpointID: 1, FeatureID: 2,
		Changes: map[uint16]any{1: int64(5500000)},
	}
	out = append(out, mkNotification("notification_single_change", "Notification with one changed attribute",
		"Notification{SubID:5001, EP:1, Feat:2, Changes:{1:5500000}}",
		n1,
		map[string]any{
			"subscription_id": 5001, "endpoint_id": 1, "feature_id": 2,
			"changes": map[string]any{"1": 5500000},
		},
	))

	n2 := wire.Notification{
		SubscriptionID: 5001, EndpointID: 1, FeatureID: 2,
		Changes: map[uint16]any{
			1: int64(7200000),
			2: int64(150000),
			3: int64(7203000),
		},
	}
	out = append(out, mkNotification("notification_multiple_changes", "Notification with multiple changed attributes",
		"Notification{Changes:{1:7200000, 2:150000, 3:7203000}}",
		n2,
		map[string]any{
			"subscription_id": 5001, "endpoint_id": 1, "feature_id": 2,
			"changes": map[string]any{"1": 7200000, "2": 150000, "3": 7203000},
		},
	))

	n3 := wire.Notification{
		SubscriptionID: 6000, EndpointID: 0, FeatureID: 1,
		Changes: map[uint16]any{},
	}
	out = append(out, mkNotification("notification_empty_changes", "Notification with empty changes map (heartbeat)",
		"Notification{Changes:{}}",
		n3,
		map[string]any{
			"subscription_id": 6000, "endpoint_id": 0, "feature_id": 1,
			"changes": map[string]any{},
		},
	))

	return out
}

// ---------- Control vectors ----------

func buildControls() []Vector {
	out := []Vector{}

	pingMsg := wire.ControlMessage{Type: wire.ControlPing, Sequence: 1}
	out = append(out, mkControl("control_ping_seq_1", "Ping with sequence number",
		"ControlMessage{Type:Ping, Sequence:1}", pingMsg,
		map[string]any{"type": 1, "sequence": 1},
	))

	pongMsg := wire.ControlMessage{Type: wire.ControlPong, Sequence: 1}
	out = append(out, mkControl("control_pong_seq_1", "Pong with sequence number",
		"ControlMessage{Type:Pong, Sequence:1}", pongMsg,
		map[string]any{"type": 2, "sequence": 1},
	))

	closeMsg := wire.ControlMessage{Type: wire.ControlClose}
	out = append(out, mkControl("control_close", "Close (no sequence)",
		"ControlMessage{Type:Close}", closeMsg,
		map[string]any{"type": 3, "sequence": 0},
	))

	// Boundary: ping with large sequence number
	pingBig := wire.ControlMessage{Type: wire.ControlPing, Sequence: 4_294_967_295}
	out = append(out, mkControl("control_ping_max_seq", "Ping with max-uint32 sequence",
		"ControlMessage{Type:Ping, Sequence:max-uint32}", pingBig,
		map[string]any{"type": 1, "sequence": 4294967295},
	))

	return out
}

// ---------- Peek classification vectors ----------

// Peek vectors reuse encodings from above and assert the classification result.
// The Python test iterates and checks peek_message_type(bytes) returns the
// expected message_type field.
func buildPeekVectors() []Vector {
	all := []Vector{}

	// Use one of each kind for peek tests
	r := wire.Request{MessageID: 1, Operation: wire.OpRead, EndpointID: 1, FeatureID: 2, Payload: []uint16{1}}
	all = append(all, mkRequest("peek_request_read", "peek classifies a read request as REQUEST",
		"Request{Op:Read}", r, nil))

	resp := wire.Response{MessageID: 1, Status: wire.StatusSuccess, Payload: map[uint16]any{1: int64(100)}}
	all = append(all, mkResponse("peek_response_success", "peek classifies a success response as RESPONSE",
		"Response{Status:Success}", resp, nil))

	// NB: msgID intentionally outside [1..3] to avoid the ambiguity in
	// mash-py-spec-issues.md Issue #2 — peek would otherwise misclassify
	// a payload-less {1: msgID, 2: status} as CONTROL.
	respErr := wire.Response{MessageID: 42, Status: wire.StatusBusy}
	all = append(all, mkResponse("peek_response_error_no_payload", "peek classifies a status>4 error response as RESPONSE",
		"Response{MsgID:42, Status:Busy}", respErr, nil))

	notif := wire.Notification{SubscriptionID: 1, EndpointID: 1, FeatureID: 1, Changes: map[uint16]any{1: int64(1)}}
	all = append(all, mkNotification("peek_notification", "peek classifies a notification by messageId=0",
		"Notification{...}", notif, nil))

	pingMsg := wire.ControlMessage{Type: wire.ControlPing, Sequence: 1}
	all = append(all, mkControl("peek_control_ping", "peek classifies a ping as CONTROL",
		"ControlMessage{Type:Ping, Seq:1}", pingMsg, nil))

	closeMsg := wire.ControlMessage{Type: wire.ControlClose}
	all = append(all, mkControl("peek_control_close", "peek classifies a close (no seq) as CONTROL",
		"ControlMessage{Type:Close}", closeMsg, nil))

	return all
}

// ---------- Strict-decode negative vectors ----------

func buildStrictVectors() []Vector {
	out := []Vector{}

	// Duplicate map key — handcrafted: A3 01 19 30 39 01 02 02 03
	out = append(out, Vector{
		Name:        "strict_duplicate_top_level_key",
		Description: "Duplicate key 1 at top of map should be rejected by strict decoder",
		MessageType: "invalid",
		CBORHex:     "A30119303901020203",
		ExpectError: "duplicate map key",
	})

	// NaN half-float
	out = append(out, Vector{
		Name:        "strict_nan_half_float",
		Description: "Half-precision NaN should be rejected",
		MessageType: "invalid",
		CBORHex:     "F97E00",
		ExpectError: "NaN",
	})

	// +Infinity half-float
	out = append(out, Vector{
		Name:        "strict_positive_infinity",
		Description: "Half-precision +Infinity should be rejected",
		MessageType: "invalid",
		CBORHex:     "F97C00",
		ExpectError: "Infinity",
	})

	// -Infinity half-float
	out = append(out, Vector{
		Name:        "strict_negative_infinity",
		Description: "Half-precision -Infinity should be rejected",
		MessageType: "invalid",
		CBORHex:     "F9FC00",
		ExpectError: "Infinity",
	})

	// String key in top-level map ({"a": 1})
	out = append(out, Vector{
		Name:        "strict_string_keyed_map",
		Description: "Top-level CBOR map with string keys must be rejected",
		MessageType: "invalid",
		CBORHex:     "A1616101",
		ExpectError: "non-integer",
	})

	return out
}

// ---------- Construction helpers ----------

func mkRequest(name, desc, repr string, req wire.Request, expected any) Vector {
	data, err := wire.EncodeRequest(&req)
	if err != nil {
		fail("encode %s: %v", name, err)
	}
	return Vector{
		Name: name, Description: desc, GoStructRepr: repr,
		MessageType: "request", CBORHex: hex.EncodeToString(data),
		ExpectedFields: expected,
	}
}

func mkResponse(name, desc, repr string, resp wire.Response, expected any) Vector {
	data, err := wire.EncodeResponse(&resp)
	if err != nil {
		fail("encode %s: %v", name, err)
	}
	return Vector{
		Name: name, Description: desc, GoStructRepr: repr,
		MessageType: "response", CBORHex: hex.EncodeToString(data),
		ExpectedFields: expected,
	}
}

func mkNotification(name, desc, repr string, n wire.Notification, expected any) Vector {
	data, err := wire.EncodeNotification(&n)
	if err != nil {
		fail("encode %s: %v", name, err)
	}
	return Vector{
		Name: name, Description: desc, GoStructRepr: repr,
		MessageType: "notification", CBORHex: hex.EncodeToString(data),
		ExpectedFields: expected,
	}
}

func mkControl(name, desc, repr string, m wire.ControlMessage, expected any) Vector {
	data, err := wire.EncodeControlMessage(&m)
	if err != nil {
		fail("encode %s: %v", name, err)
	}
	return Vector{
		Name: name, Description: desc, GoStructRepr: repr,
		MessageType: "control", CBORHex: hex.EncodeToString(data),
		ExpectedFields: expected,
	}
}

func writeFile(path string, vectors []Vector) {
	data, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		fail("marshal %s: %v", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
	fmt.Printf("  wrote %d vectors to %s\n", len(vectors), filepath.Base(path))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen_vectors: "+format+"\n", args...)
	os.Exit(1)
}
