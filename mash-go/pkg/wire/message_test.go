package wire

import (
	"reflect"
	"testing"
)

// TestExtractReadAttributeIDs_BareArrayWireForm covers the spec §4.1 wire
// shape: request key 5 is a bare CBOR array of attribute IDs. interaction
// Client.Read sends Payload as []uint16, which after CBOR roundtrip is
// []any of uint64. Pre-fix, ExtractReadAttributeIDs returned nil for this
// shape and Reads with specific IDs silently degraded to "read all".
func TestExtractReadAttributeIDs_BareArrayWireForm(t *testing.T) {
	// Roundtrip-equivalent of Payload: []uint16{1, 2, 3}.
	payload := []any{uint64(1), uint64(2), uint64(3)}

	got := ExtractReadAttributeIDs(payload)
	want := []uint16{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare array: got %v, want %v", got, want)
	}
}

// TestExtractReadAttributeIDs_BareArrayEmpty covers the "read all" sentinel.
func TestExtractReadAttributeIDs_BareArrayEmpty(t *testing.T) {
	got := ExtractReadAttributeIDs([]any{})
	if len(got) != 0 {
		t.Errorf("empty array: got %v, want zero-length", got)
	}
}

// TestExtractReadAttributeIDs_TypedSliceShortCircuit covers the in-process
// shape used by interaction.Client.Read before encoding.
func TestExtractReadAttributeIDs_TypedSliceShortCircuit(t *testing.T) {
	got := ExtractReadAttributeIDs([]uint16{4, 5})
	want := []uint16{4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("typed []uint16: got %v, want %v", got, want)
	}
}

// TestExtractReadAttributeIDs_LegacyMapForm guards the back-compat path
// for the *ReadPayload struct's CBOR shape ({1: [...]}).
func TestExtractReadAttributeIDs_LegacyMapForm(t *testing.T) {
	payload := map[any]any{uint64(1): []any{uint64(7), uint64(8)}}
	got := ExtractReadAttributeIDs(payload)
	want := []uint16{7, 8}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("map form: got %v, want %v", got, want)
	}
}

// TestExtractReadAttributeIDs_TypedReadPayload guards the test-only
// short-circuit for *ReadPayload.
func TestExtractReadAttributeIDs_TypedReadPayload(t *testing.T) {
	got := ExtractReadAttributeIDs(&ReadPayload{AttributeIDs: []uint16{9}})
	want := []uint16{9}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("*ReadPayload: got %v, want %v", got, want)
	}
}

// TestExtractReadAttributeIDs_NilAndUnknown ensures non-payload shapes
// degrade to nil cleanly.
func TestExtractReadAttributeIDs_NilAndUnknown(t *testing.T) {
	if got := ExtractReadAttributeIDs(nil); got != nil {
		t.Errorf("nil: got %v, want nil", got)
	}
	if got := ExtractReadAttributeIDs("not a payload"); got != nil {
		t.Errorf("string: got %v, want nil", got)
	}
}
