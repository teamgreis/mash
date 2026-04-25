package wire

import (
	"bytes"
	"fmt"
	"io"

	"github.com/fxamacker/cbor/v2"
)

// encMode is the CBOR encoder mode for MASH messages.
// Configured for deterministic encoding with integer keys.
var encMode cbor.EncMode

// decMode is the CBOR decoder mode for MASH messages.
var decMode cbor.DecMode

func init() {
	var err error

	// Configure encoder for deterministic output
	encOpts := cbor.EncOptions{
		Sort:          cbor.SortCanonical, // Deterministic key ordering
		IndefLength:   cbor.IndefLengthForbidden,
		NilContainers: cbor.NilContainerAsNull,
		Time:          cbor.TimeUnix, // Unix timestamps
	}
	encMode, err = encOpts.EncMode()
	if err != nil {
		panic(fmt.Sprintf("failed to create CBOR encoder mode: %v", err))
	}

	// Configure decoder with strict CBOR validation per MASH protocol spec:
	// - Duplicate map keys are rejected (not silently ignored)
	// - NaN and Infinity float values are rejected
	// - Indefinite length encoding is allowed for forward compatibility
	decOpts := cbor.DecOptions{
		DupMapKey:         cbor.DupMapKeyEnforcedAPF, // Reject duplicate keys with error
		IndefLength:       cbor.IndefLengthAllowed,
		ExtraReturnErrors: cbor.ExtraDecErrorNone,
		NaN:               cbor.NaNDecodeForbidden, // Reject NaN float values
		Inf:               cbor.InfDecodeForbidden, // Reject Infinity float values
	}
	decMode, err = decOpts.DecMode()
	if err != nil {
		panic(fmt.Sprintf("failed to create CBOR decoder mode: %v", err))
	}
}

// Marshal encodes a value to CBOR bytes.
func Marshal(v any) ([]byte, error) {
	return encMode.Marshal(v)
}

// Unmarshal decodes CBOR bytes into a value.
func Unmarshal(data []byte, v any) error {
	return decMode.Unmarshal(data, v)
}

// NewEncoder creates a new CBOR encoder that writes to w.
func NewEncoder(w io.Writer) *cbor.Encoder {
	return encMode.NewEncoder(w)
}

// NewDecoder creates a new CBOR decoder that reads from r.
func NewDecoder(r io.Reader) *cbor.Decoder {
	return decMode.NewDecoder(r)
}

// EncodeRequest encodes a request message to CBOR bytes.
func EncodeRequest(req *Request) ([]byte, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	return Marshal(req)
}

// DecodeRequest decodes CBOR bytes into a request message.
func DecodeRequest(data []byte) (*Request, error) {
	var req Request
	if err := Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to decode request: %w", err)
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	return &req, nil
}

// EncodeResponse encodes a response message to CBOR bytes.
func EncodeResponse(resp *Response) ([]byte, error) {
	return Marshal(resp)
}

// DecodeResponse decodes CBOR bytes into a response message.
func DecodeResponse(data []byte) (*Response, error) {
	var resp Response
	if err := Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &resp, nil
}

// EncodeNotification encodes a notification message to CBOR bytes.
// Notifications have messageId=0 which is handled automatically.
func EncodeNotification(notif *Notification) ([]byte, error) {
	// Create the wire format with messageId=0
	wireMsg := struct {
		MessageID      uint32         `cbor:"1,keyasint"`
		SubscriptionID uint32         `cbor:"2,keyasint"`
		EndpointID     uint8          `cbor:"3,keyasint"`
		FeatureID      uint8          `cbor:"4,keyasint"`
		Changes        map[uint16]any `cbor:"5,keyasint"`
	}{
		MessageID:      NotificationMessageID,
		SubscriptionID: notif.SubscriptionID,
		EndpointID:     notif.EndpointID,
		FeatureID:      notif.FeatureID,
		Changes:        notif.Changes,
	}
	return Marshal(wireMsg)
}

// DecodeNotification decodes CBOR bytes into a notification message.
func DecodeNotification(data []byte) (*Notification, error) {
	// First decode to check messageId
	var wireMsg struct {
		MessageID      uint32         `cbor:"1,keyasint"`
		SubscriptionID uint32         `cbor:"2,keyasint"`
		EndpointID     uint8          `cbor:"3,keyasint"`
		FeatureID      uint8          `cbor:"4,keyasint"`
		Changes        map[uint16]any `cbor:"5,keyasint"`
	}
	if err := Unmarshal(data, &wireMsg); err != nil {
		return nil, fmt.Errorf("failed to decode notification: %w", err)
	}
	if wireMsg.MessageID != NotificationMessageID {
		return nil, fmt.Errorf("not a notification message: messageId=%d", wireMsg.MessageID)
	}
	return &Notification{
		SubscriptionID: wireMsg.SubscriptionID,
		EndpointID:     wireMsg.EndpointID,
		FeatureID:      wireMsg.FeatureID,
		Changes:        wireMsg.Changes,
	}, nil
}

// ControlMessageTag is the CBOR tag wrapping every Control message frame.
// See DEC-083: tag 77 (ASCII 'M' for MASH) disambiguates Control from a
// payload-less Response with messageId in {1,2,3}, which would otherwise
// encode to byte-identical CBOR. Receivers MUST treat the absence of this
// tag at the top of a frame as proof the frame is application-layer
// (Request/Response/Notification).
const ControlMessageTag uint64 = 77

// EncodeControlMessage encodes a control message (ping/pong/close) to CBOR bytes.
//
// Per DEC-083, the encoded form is `tag77({1: type, 2: sequence?})`. The
// tag prefix `D8 4D` makes the frame unambiguous at the wire level.
func EncodeControlMessage(msg *ControlMessage) ([]byte, error) {
	return Marshal(cbor.Tag{Number: ControlMessageTag, Content: msg})
}

// DecodeControlMessage decodes CBOR bytes into a control message.
//
// The frame must begin with the DEC-083 tag (`D8 4D`); a tagless map with
// the same shape is rejected so that pre-DEC-083 senders can't accidentally
// be routed through the control path on the receive side.
func DecodeControlMessage(data []byte) (*ControlMessage, error) {
	var raw cbor.RawTag
	if err := Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to decode control message: %w", err)
	}
	if raw.Number != ControlMessageTag {
		return nil, fmt.Errorf("control message: expected tag %d, got %d", ControlMessageTag, raw.Number)
	}
	var msg ControlMessage
	if err := Unmarshal(raw.Content, &msg); err != nil {
		return nil, fmt.Errorf("failed to decode control message body: %w", err)
	}
	return &msg, nil
}

// MessageType represents the type of a decoded message.
type MessageType int

const (
	MessageTypeUnknown MessageType = iota
	MessageTypeRequest
	MessageTypeResponse
	MessageTypeNotification
	MessageTypeControl
)

// PeekMessageType examines CBOR data to determine the message type
// without fully decoding it.
//
// Per DEC-083, every Control message frame begins with CBOR tag 77 (`D8 4D`).
// The tag prefix is the unique CONTROL marker; any frame without it is
// application-layer (Request/Response/Notification) and is classified by
// the messageId-based rules below.
//
// Message type detection logic:
//   - Frame begins with `D8 4D` (tag 77)              → CONTROL
//   - Top-level map key 1 (messageId) == 0            → NOTIFICATION
//   - Top-level map has key 4 (featureId)             → REQUEST
//     (operation in field 2 may be 0; downstream validate() rejects it)
//   - Otherwise                                       → RESPONSE
func PeekMessageType(data []byte) (MessageType, error) {
	// DEC-083: tag-77 prefix is the unambiguous CONTROL signal.
	if len(data) >= 2 && data[0] == 0xD8 && data[1] == byte(ControlMessageTag) {
		return MessageTypeControl, nil
	}

	// Application-layer frames are CBOR maps with integer keys.
	var anyMap map[any]any
	if err := Unmarshal(data, &anyMap); err != nil {
		return MessageTypeUnknown, fmt.Errorf("failed to peek message: %w", err)
	}
	for k := range anyMap {
		switch k.(type) {
		case uint64, int64, int, uint, uint32, int32, uint16, int16, uint8, int8:
			// Valid integer key
		default:
			return MessageTypeUnknown, fmt.Errorf("invalid message: non-integer key %T(%v)", k, k)
		}
	}

	var rawMsg map[uint64]any
	if err := Unmarshal(data, &rawMsg); err != nil {
		return MessageTypeUnknown, fmt.Errorf("failed to peek message: %w", err)
	}

	field1, _ := toUint32(rawMsg[1])
	if field1 == NotificationMessageID {
		return MessageTypeNotification, nil
	}

	// Request frames carry both endpointId (key 3) and featureId (key 4).
	// The presence of key 4 is the cleanest discriminator: Responses never
	// use key 4. Notifications were already handled above.
	if _, hasField4 := rawMsg[4]; hasField4 {
		return MessageTypeRequest, nil
	}

	// No key 4 → Response. Keys 1, 2, and an optional payload at key 3.
	return MessageTypeResponse, nil
}

// toUint32 converts various numeric types to uint32.
func toUint32(v any) (uint32, bool) {
	switch n := v.(type) {
	case uint64:
		return uint32(n), true
	case uint32:
		return n, true
	case int64:
		return uint32(n), true
	case int:
		return uint32(n), true
	default:
		return 0, false
	}
}

// Clone creates a deep copy of the CBOR data by re-encoding.
// Useful for copying messages without shared references.
func Clone[T any](v T) (T, error) {
	var result T
	data, err := Marshal(v)
	if err != nil {
		return result, err
	}
	err = Unmarshal(data, &result)
	return result, err
}

// Equal compares two values by their CBOR encoding.
func Equal(a, b any) bool {
	dataA, errA := Marshal(a)
	dataB, errB := Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(dataA, dataB)
}

// ToInt64 safely converts any numeric type to int64.
// CBOR decoding into `any` may produce different integer types depending on value size,
// so this helper handles all cases. Returns (0, false) if v is not a numeric type.
func ToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int16:
		return int64(n), true
	case int8:
		return int64(n), true
	case uint64:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint8:
		return int64(n), true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	default:
		return 0, false
	}
}

// ToUint32 safely converts any numeric type to uint32.
// CBOR decoding into `any` may produce different integer types depending on value size.
func ToUint32(v any) (uint32, bool) {
	switch n := v.(type) {
	case uint32:
		return n, true
	case uint64:
		return uint32(n), true
	case uint16:
		return uint32(n), true
	case uint8:
		return uint32(n), true
	case int64:
		return uint32(n), true
	case int32:
		return uint32(n), true
	case int16:
		return uint32(n), true
	case int8:
		return uint32(n), true
	case int:
		return uint32(n), true
	default:
		return 0, false
	}
}

// ToUint8Public safely converts any numeric type to uint8.
// This is the public version of toUint8 for use by notification handlers.
func ToUint8Public(v any) (uint8, bool) {
	switch n := v.(type) {
	case uint8:
		return n, true
	case uint64:
		return uint8(n), true
	case uint32:
		return uint8(n), true
	case uint16:
		return uint8(n), true
	case int64:
		return uint8(n), true
	case int32:
		return uint8(n), true
	case int16:
		return uint8(n), true
	case int8:
		return uint8(n), true
	case int:
		return uint8(n), true
	default:
		return 0, false
	}
}

// ToStringMap normalizes a CBOR-decoded map to map[string]any.
// CBOR decodes maps into map[any]any when the target is any.
// This converts both map[any]any and map[string]any to map[string]any.
// Returns nil if the value is not a map type.
func ToStringMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[any]any:
		result := make(map[string]any, len(m))
		for k, val := range m {
			result[fmt.Sprintf("%v", k)] = val
		}
		return result
	default:
		return nil
	}
}
