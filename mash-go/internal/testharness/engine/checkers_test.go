package engine

import (
	"context"
	"testing"
)

// TestChecker_ValueGreaterThan tests the value_greater_than checker.
func TestChecker_ValueGreaterThan(t *testing.T) {
	state := NewExecutionState(context.Background())
	state.Set("value", float64(10))

	tests := []struct {
		expected interface{}
		passed   bool
	}{
		{float64(5), true},   // 10 > 5
		{float64(10), false}, // 10 > 10 (not strictly greater)
		{float64(15), false}, // 10 > 15
	}

	for _, tt := range tests {
		result := CheckerValueGreaterThan("value_greater_than", tt.expected, state)
		if result.Passed != tt.passed {
			t.Errorf("ValueGreaterThan(10, %v) = %v, want %v", tt.expected, result.Passed, tt.passed)
		}
	}
}

// TestChecker_ValueLessThan tests the value_less_than checker.
func TestChecker_ValueLessThan(t *testing.T) {
	state := NewExecutionState(context.Background())
	state.Set("value", float64(10))

	tests := []struct {
		expected interface{}
		passed   bool
	}{
		{float64(15), true},  // 10 < 15
		{float64(10), false}, // 10 < 10 (not strictly less)
		{float64(5), false},  // 10 < 5
	}

	for _, tt := range tests {
		result := CheckerValueLessThan("value_less_than", tt.expected, state)
		if result.Passed != tt.passed {
			t.Errorf("ValueLessThan(10, %v) = %v, want %v", tt.expected, result.Passed, tt.passed)
		}
	}
}

// TestChecker_ValueInRange tests the value_in_range checker.
func TestChecker_ValueInRange(t *testing.T) {
	state := NewExecutionState(context.Background())
	state.Set("value", float64(50))

	tests := []struct {
		expected interface{}
		passed   bool
	}{
		{map[string]interface{}{"min": float64(0), "max": float64(100)}, true},   // 50 in [0, 100]
		{map[string]interface{}{"min": float64(50), "max": float64(100)}, true},  // 50 in [50, 100] (inclusive)
		{map[string]interface{}{"min": float64(0), "max": float64(50)}, true},    // 50 in [0, 50] (inclusive)
		{map[string]interface{}{"min": float64(60), "max": float64(100)}, false}, // 50 not in [60, 100]
		{map[string]interface{}{"min": float64(0), "max": float64(40)}, false},   // 50 not in [0, 40]
	}

	for _, tt := range tests {
		result := CheckerValueInRange("value", tt.expected, state)
		if result.Passed != tt.passed {
			t.Errorf("ValueInRange(50, %v) = %v, want %v", tt.expected, result.Passed, tt.passed)
		}
	}
}

// TestChecker_ValueIsNull tests the value_is_null checker.
func TestChecker_ValueIsNull(t *testing.T) {
	state := NewExecutionState(context.Background())

	// Test nil value
	state.Set("value", nil)
	result := CheckerValueIsNull("value_is_null", true, state)
	if !result.Passed {
		t.Error("ValueIsNull(nil) should pass")
	}

	// Test non-nil value expecting null
	state.Set("value", "something")
	result = CheckerValueIsNull("value_is_null", true, state)
	if result.Passed {
		t.Error("ValueIsNull(something) expecting true should fail")
	}

	// Test non-nil value expecting not null
	state.Set("value", "something")
	result = CheckerValueIsNull("value_is_null", false, state)
	if !result.Passed {
		t.Error("ValueIsNull(something) expecting false should pass")
	}
}

// TestChecker_ValueIsMap tests the value_is_map checker.
func TestChecker_ValueIsMap(t *testing.T) {
	state := NewExecutionState(context.Background())

	// Test map[string]any value
	state.Set("value", map[string]interface{}{"key": "value"})
	result := CheckerValueIsMap("value_is_map", true, state)
	if !result.Passed {
		t.Error("ValueIsMap(map[string]any) should pass")
	}

	// Test map[any]any value (CBOR round-trip)
	state.Set("value", map[any]any{uint64(0): uint64(1)})
	result = CheckerValueIsMap("value_is_map", true, state)
	if !result.Passed {
		t.Error("ValueIsMap(map[any]any) should pass")
	}

	// Test non-map value
	state.Set("value", "not a map")
	result = CheckerValueIsMap("value_is_map", true, state)
	if result.Passed {
		t.Error("ValueIsMap(string) should fail")
	}

	// Test array value (not a map)
	state.Set("value", []interface{}{1, 2, 3})
	result = CheckerValueIsMap("value_is_map", true, state)
	if result.Passed {
		t.Error("ValueIsMap(array) should fail")
	}

	// Test expecting not a map
	state.Set("value", "not a map")
	result = CheckerValueIsMap("value_is_map", false, state)
	if !result.Passed {
		t.Error("ValueIsMap(string) expecting false should pass")
	}
}

// TestChecker_Contains tests the contains checker (for arrays/maps).
func TestChecker_Contains(t *testing.T) {
	state := NewExecutionState(context.Background())
	state.Set("array", []interface{}{"apple", "banana", "cherry"})
	state.Set("map", map[string]interface{}{"name": "test", "count": 42})

	// Test array contains
	result := CheckerContains("array", "banana", state)
	if !result.Passed {
		t.Error("Contains(array, banana) should pass")
	}

	result = CheckerContains("array", "grape", state)
	if result.Passed {
		t.Error("Contains(array, grape) should fail")
	}

	// Test map contains key
	result = CheckerContains("map", "name", state)
	if !result.Passed {
		t.Error("Contains(map, name) should pass")
	}

	result = CheckerContains("map", "missing", state)
	if result.Passed {
		t.Error("Contains(map, missing) should fail")
	}
}

// TestChecker_MapSizeEquals tests the map_size_equals checker.
func TestChecker_MapSizeEquals(t *testing.T) {
	state := NewExecutionState(context.Background())

	// Test map size equals
	state.Set("value", map[string]interface{}{"a": 1, "b": 2})
	result := CheckerMapSizeEquals("map_size_equals", float64(2), state)
	if !result.Passed {
		t.Errorf("MapSizeEquals(map2, 2) = %v, want true. Message: %s", result.Passed, result.Message)
	}

	result = CheckerMapSizeEquals("map_size_equals", float64(3), state)
	if result.Passed {
		t.Error("MapSizeEquals(map2, 3) should fail")
	}

	// Test empty map
	state.Set("value", map[string]interface{}{})
	result = CheckerMapSizeEquals("map_size_equals", float64(0), state)
	if !result.Passed {
		t.Error("MapSizeEquals(map0, 0) should pass")
	}

	// Test array size
	state.Set("value", []interface{}{1, 2, 3})
	result = CheckerMapSizeEquals("map_size_equals", float64(3), state)
	if !result.Passed {
		t.Error("MapSizeEquals(array3, 3) should pass (works for arrays too)")
	}
}

// TestChecker_SaveAs tests the save_as checker (stores step output for later use).
func TestChecker_SaveAs(t *testing.T) {
	state := NewExecutionState(context.Background())

	// Simulate step output (engine stores __step_output before running checkers).
	stepOutput := map[string]interface{}{
		"fingerprint": "abc123",
	}
	state.Set(InternalStepOutput, stepOutput)

	// SaveAs should save the step output under the target key.
	result := CheckerSaveAs(CheckerNameSaveAs, "saved_output", state)
	if !result.Passed {
		t.Errorf("SaveAs failed: %s", result.Message)
	}

	// Check that the step output was saved.
	saved, exists := state.Get("saved_output")
	if !exists {
		t.Error("SaveAs did not create 'saved_output' key")
	}
	savedMap, ok := saved.(map[string]interface{})
	if !ok {
		t.Fatalf("saved value is %T, want map[string]interface{}", saved)
	}
	if savedMap["fingerprint"] != "abc123" {
		t.Errorf("saved fingerprint = %v, want abc123", savedMap["fingerprint"])
	}
}

// TestChecker_NotFound tests checker behavior when key is not found.
func TestChecker_NotFound(t *testing.T) {
	state := NewExecutionState(context.Background())

	// All checkers should fail gracefully when key not found
	checkers := []struct {
		name   string
		fn     ExpectChecker
		expect interface{}
	}{
		{CheckerNameValueGreaterThan, CheckerValueGreaterThan, float64(5)},
		{CheckerNameValueLessThan, CheckerValueLessThan, float64(5)},
		{CheckerNameValueInRange, CheckerValueInRange, map[string]interface{}{"min": 0, "max": 10}},
		{CheckerNameValueIsMap, CheckerValueIsMap, true},
		{CheckerNameContains, CheckerContains, "value"},
		{CheckerNameMapSizeEquals, CheckerMapSizeEquals, float64(1)},
	}

	for _, tc := range checkers {
		result := tc.fn("nonexistent", tc.expect, state)
		if result.Passed {
			t.Errorf("%s should fail for nonexistent key", tc.name)
		}
	}
}

// TestChecker_TypeConversion tests numeric type handling.
func TestChecker_TypeConversion(t *testing.T) {
	state := NewExecutionState(context.Background())

	// Should work with int value
	state.Set("value", int(10))
	result := CheckerValueGreaterThan("value_greater_than", float64(5), state)
	if !result.Passed {
		t.Error("Should handle int value with float64 comparison")
	}

	// Should work with float64 value
	state.Set("value", 10.5)
	result = CheckerValueGreaterThan("value_greater_than", float64(5), state)
	if !result.Passed {
		t.Error("Should handle float64 value")
	}
}

// TestChecker_ValueType_LogicalMatch tests that value_type accepts any
// compatible Go numeric type as long as the value fits in the declared
// spec type's range. CBOR canonically decodes uint values as uint64, so a
// strict Go-typename comparison would reject every uint8/uint16/uint32
// attribute; the checker uses logical-type matching instead.
func TestChecker_ValueType_LogicalMatch(t *testing.T) {
	state := NewExecutionState(context.Background())

	tests := []struct {
		name     string
		value    interface{}
		expected string
		passed   bool
	}{
		{"uint64 in uint32 range matches uint32", uint64(7200), "uint32", true},
		{"uint64 max matches uint64", uint64(1 << 63), "uint64", true},
		{"uint64 overflows uint32", uint64(1 << 33), "uint32", false},
		{"int64 negative matches int32", int64(-100), "int32", true},
		{"int64 overflows int8", int64(200), "int8", false},
		{"uint8 value matches uint32", uint8(42), "uint32", true},
		{"int matches int64 alias", int(-5), "int64", true},
		{"float with no fractional part matches uint32", float64(7200), "uint32", true},
		{"float with fractional part rejects uint32", float64(7200.5), "uint32", false},
		{"string matches string strictly", "hello", "string", true},
		{"string vs uint32 fails", "hello", "uint32", false},
		{"bool matches bool strictly", true, "bool", true},
		{"bool vs string fails", true, "string", false},
		{"float64 matches float64", float64(3.14), "float64", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state.Set("value", tt.value)
			result := CheckerValueType("value_type", tt.expected, state)
			if result.Passed != tt.passed {
				t.Errorf("CheckerValueType(%T(%v), %q) passed=%v, want %v (%s)",
					tt.value, tt.value, tt.expected, result.Passed, tt.passed, result.Message)
			}
		})
	}
}
