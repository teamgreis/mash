package wire

import (
	"fmt"
)

// CBOR map keys for message encoding.
// All MASH messages use integer keys for efficiency.
const (
	// Common message keys
	KeyMessageID  = 1
	KeyOpOrStatus = 2 // Operation (request) or Status (response)
	KeyEndpointID = 3
	KeyFeatureID  = 4
	KeyPayload    = 5

	// Notification-specific keys (messageId=0 indicates notification)
	KeySubscriptionID = 2 // Replaces operation/status for notifications
)

// MessageID 0 is reserved to indicate a notification message.
const NotificationMessageID uint32 = 0

// Request represents a MASH request message from controller to device.
//
// CBOR encoding:
//
//	{
//	  1: messageId,    // uint32
//	  2: operation,    // uint8: 1=Read, 2=Write, 3=Subscribe, 4=Invoke
//	  3: endpointId,   // uint8
//	  4: featureId,    // uint8
//	  5: payload       // operation-specific data
//	}
type Request struct {
	MessageID  uint32    `cbor:"1,keyasint"`
	Operation  Operation `cbor:"2,keyasint"`
	EndpointID uint8     `cbor:"3,keyasint"`
	FeatureID  uint8     `cbor:"4,keyasint"`
	Payload    any       `cbor:"5,keyasint,omitempty"`
}

// Validate checks if the request is valid.
func (r *Request) Validate() error {
	if r.MessageID == NotificationMessageID {
		return fmt.Errorf("messageId 0 is reserved for notifications")
	}
	if !r.Operation.IsValid() {
		return fmt.Errorf("invalid operation: %d", r.Operation)
	}
	return nil
}

// Response represents a MASH response message from device to controller.
//
// CBOR encoding:
//
//	{
//	  1: messageId,    // uint32: matches request
//	  2: status,       // uint8: 0=success, or error code
//	  3: payload       // operation-specific response data (if success)
//	}
type Response struct {
	MessageID uint32 `cbor:"1,keyasint"`
	Status    Status `cbor:"2,keyasint"`
	Payload   any    `cbor:"3,keyasint,omitempty"`
}

// IsSuccess returns true if the response indicates success.
func (r *Response) IsSuccess() bool {
	return r.Status.IsSuccess()
}

// Notification represents a subscription notification from device to controller.
//
// CBOR encoding:
//
//	{
//	  1: 0,                // messageId 0 = notification
//	  2: subscriptionId,   // uint32
//	  3: endpointId,       // uint8
//	  4: featureId,        // uint8
//	  5: changes           // map of changed attributes
//	}
type Notification struct {
	SubscriptionID uint32         `cbor:"2,keyasint"`
	EndpointID     uint8          `cbor:"3,keyasint"`
	FeatureID      uint8          `cbor:"4,keyasint"`
	Changes        map[uint16]any `cbor:"5,keyasint"`
}

// ReadPayload represents the payload for a Read request.
//
// CBOR encoding: array of attribute IDs to read (empty = all)
type ReadPayload struct {
	AttributeIDs []uint16 `cbor:"1,keyasint,omitempty"`
}

// ExtractReadAttributeIDs extracts attribute IDs from a read request payload.
//
// Per docs/interaction-model.md §4.1 the canonical wire form is a bare CBOR
// array at request key 5 (`5: [1, 2, 3]`, empty = "read all"). interaction
// Client.Read sends `Payload: []uint16{...}`, which after CBOR roundtrip is
// `[]any` of `uint64`. This function also accepts the legacy *ReadPayload
// typed form (`{1: [...]}`) so existing callers and tests keep working.
func ExtractReadAttributeIDs(payload any) []uint16 {
	if payload == nil {
		return nil
	}

	// Typed forms (used pre-encoding by Go callers).
	if rp, ok := payload.(*ReadPayload); ok {
		return rp.AttributeIDs
	}
	if ids, ok := payload.([]uint16); ok {
		return ids
	}

	// Bare CBOR array (spec §4.1) or legacy {1: [...]} map.
	var arr []any
	switch m := payload.(type) {
	case []any:
		arr = m
	case map[any]any:
		if v, ok := m[uint64(1)]; ok {
			arr, _ = v.([]any)
		}
	case map[uint64]any:
		if v, ok := m[uint64(1)]; ok {
			arr, _ = v.([]any)
		}
	default:
		return nil
	}

	if len(arr) == 0 {
		return nil
	}

	ids := make([]uint16, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case uint64:
			ids = append(ids, uint16(v))
		case int64:
			ids = append(ids, uint16(v))
		case float64:
			ids = append(ids, uint16(v))
		}
	}
	return ids
}

// ReadResponsePayload represents the payload for a Read response.
// Keys are attribute IDs, values are attribute values.
type ReadResponsePayload map[uint16]any

// WritePayload represents the payload for a Write request.
// Keys are attribute IDs, values are values to write.
type WritePayload map[uint16]any

// ExtractWritePayload extracts a write payload from a raw CBOR-decoded value.
// After CBOR round-trip the payload is map[any]any with uint64 keys.
func ExtractWritePayload(payload any) WritePayload {
	if payload == nil {
		return nil
	}
	if wp, ok := payload.(WritePayload); ok {
		return wp
	}
	if wp, ok := payload.(map[uint16]any); ok {
		return WritePayload(wp)
	}

	// Raw CBOR map with uint64 keys
	result := make(WritePayload)
	switch m := payload.(type) {
	case map[any]any:
		for k, v := range m {
			switch key := k.(type) {
			case uint64:
				result[uint16(key)] = v
			case int64:
				result[uint16(key)] = v
			}
		}
	case map[uint64]any:
		for k, v := range m {
			result[uint16(k)] = v
		}
	default:
		return nil
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// WriteResponsePayload represents the payload for a Write response.
// Contains resulting values (may differ from requested due to constraints).
type WriteResponsePayload map[uint16]any

// SubscribePayload represents the payload for a Subscribe request.
//
// CBOR encoding:
//
//	{
//	  1: attributeIds,  // array (empty = all)
//	  2: minInterval,   // uint32: minimum ms between notifications
//	  3: maxInterval    // uint32: maximum ms without notification (heartbeat)
//	}
type SubscribePayload struct {
	AttributeIDs []uint16 `cbor:"1,keyasint,omitempty"`
	MinInterval  uint32   `cbor:"2,keyasint,omitempty"`
	MaxInterval  uint32   `cbor:"3,keyasint,omitempty"`
}

// ExtractSubscribePayload extracts a subscribe payload from a raw
// CBOR-decoded value. Returns nil if there is no payload.
func ExtractSubscribePayload(payload any) *SubscribePayload {
	if payload == nil {
		return nil
	}
	if sp, ok := payload.(*SubscribePayload); ok {
		return sp
	}

	sp := &SubscribePayload{}
	var m map[uint64]any

	switch raw := payload.(type) {
	case map[any]any:
		m = make(map[uint64]any, len(raw))
		for k, v := range raw {
			if key, ok := k.(uint64); ok {
				m[key] = v
			}
		}
	case map[uint64]any:
		m = raw
	default:
		return nil
	}

	// key 1: attributeIDs
	if arr, ok := m[1].([]any); ok {
		for _, item := range arr {
			switch v := item.(type) {
			case uint64:
				sp.AttributeIDs = append(sp.AttributeIDs, uint16(v))
			case int64:
				sp.AttributeIDs = append(sp.AttributeIDs, uint16(v))
			}
		}
	}
	// key 2: minInterval
	if v, ok := m[2].(uint64); ok {
		sp.MinInterval = uint32(v)
	}
	// key 3: maxInterval
	if v, ok := m[3].(uint64); ok {
		sp.MaxInterval = uint32(v)
	}

	return sp
}

// SubscribeResponsePayload represents the payload for a Subscribe response.
//
// CBOR encoding:
//
//	{
//	  1: subscriptionId,  // uint32
//	  2: currentValues    // map of current attribute values (priming report)
//	}
type SubscribeResponsePayload struct {
	SubscriptionID uint32         `cbor:"1,keyasint"`
	CurrentValues  map[uint16]any `cbor:"2,keyasint,omitempty"`
}

// UnsubscribePayload represents the payload for an Unsubscribe request.
// Sent as Subscribe operation with endpointId=0, featureId=0.
//
// CBOR encoding:
//
//	{
//	  1: subscriptionId  // uint32: subscription to cancel
//	}
type UnsubscribePayload struct {
	SubscriptionID uint32 `cbor:"1,keyasint"`
}

// InvokePayload represents the payload for an Invoke request.
//
// CBOR encoding:
//
//	{
//	  1: commandId,   // uint8
//	  2: parameters   // command-specific parameters
//	}
type InvokePayload struct {
	CommandID  uint8 `cbor:"1,keyasint"`
	Parameters any   `cbor:"2,keyasint,omitempty"`
}

// InvokeResponsePayload represents the payload for an Invoke response.
// Structure is command-specific.
type InvokeResponsePayload any

// ErrorPayload represents additional error information in a response.
//
// CBOR encoding:
//
//	{
//	  1: message  // string: human-readable error message
//	}
type ErrorPayload struct {
	Message string `cbor:"1,keyasint,omitempty"`
}

// ControlMessage represents a transport-level control message.
// These are separate from the request/response/notification model.
type ControlMessage struct {
	Type     ControlMessageType `cbor:"1,keyasint"`
	Sequence uint32             `cbor:"2,keyasint,omitempty"`
}

// ControlMessageType represents the type of control message.
type ControlMessageType uint8

const (
	// ControlPing is sent to check connection liveness.
	ControlPing ControlMessageType = 1

	// ControlPong is the response to a ping.
	ControlPong ControlMessageType = 2

	// ControlClose initiates graceful connection close.
	ControlClose ControlMessageType = 3
)

// String returns the control message type name.
func (t ControlMessageType) String() string {
	switch t {
	case ControlPing:
		return "ping"
	case ControlPong:
		return "pong"
	case ControlClose:
		return "close"
	default:
		return "unknown"
	}
}
