package engine

import (
	"fmt"
	"strings"
	"time"
)

// ToFloat64 converts various numeric types to float64 for comparison.
func ToFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	default:
		return 0, false
	}
}

// CheckerValueGreaterThan checks if the "value" output is greater than expected.
func CheckerValueGreaterThan(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("output key %q not found", "value"),
		}
	}

	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}

	passed := actualNum > expectedNum
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("%v > %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerValueLessThan checks if the "value" output is less than expected.
func CheckerValueLessThan(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("output key %q not found", "value"),
		}
	}

	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}

	passed := actualNum < expectedNum
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("%v < %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerValueInRange checks if the actual value is within a range [min, max].
// Expected should be a map with "min" and "max" keys.
func CheckerValueInRange(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("output key %q not found", "value"),
		}
	}

	// Expected can be a map with min/max keys or a [min, max] array.
	var minVal, maxVal interface{}
	switch e := expected.(type) {
	case map[string]interface{}:
		var hasMin, hasMax bool
		minVal, hasMin = e["min"]
		maxVal, hasMax = e["max"]
		if !hasMin || !hasMax {
			return &ExpectResult{
				Key: key, Expected: expected, Actual: actual,
				Passed: false, Message: "expected must have both 'min' and 'max' keys",
			}
		}
	case []interface{}:
		if len(e) != 2 {
			return &ExpectResult{
				Key: key, Expected: expected, Actual: actual,
				Passed: false, Message: "expected array must have exactly 2 elements [min, max]",
			}
		}
		minVal, maxVal = e[0], e[1]
	default:
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual,
			Passed: false, Message: "expected must be a map with 'min'/'max' or a [min, max] array",
		}
	}

	actualNum, ok1 := ToFloat64(actual)
	minNum, ok2 := ToFloat64(minVal)
	maxNum, ok3 := ToFloat64(maxVal)
	if !ok1 || !ok2 || !ok3 {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("cannot compare non-numeric values: actual=%T(%v) min=%T(%v) max=%T(%v)", actual, actual, minVal, minVal, maxVal, maxVal),
		}
	}

	passed := actualNum >= minNum && actualNum <= maxNum
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("%v in [%v, %v] = %v", actualNum, minNum, maxNum, passed),
	}
}

// CheckerValueIsNull checks if the "value" output is nil/null.
func CheckerValueIsNull(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")

	expectNull, _ := expected.(bool)
	if !expectNull {
		// Expecting not null
		isNull := !exists || actual == nil
		passed := !isNull
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   passed,
			Message:  fmt.Sprintf("value is null = %v (expected not null)", isNull),
		}
	}

	// Expecting null
	isNull := !exists || actual == nil
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   isNull,
		Message:  fmt.Sprintf("value is null = %v", isNull),
	}
}

// CheckerValueIsMap checks if the "value" output is a map.
func CheckerValueIsMap(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("output key %q not found", "value"),
		}
	}

	isMap := false
	switch actual.(type) {
	case map[string]any, map[any]any:
		isMap = true
	}
	expectMap, _ := expected.(bool)

	passed := isMap == expectMap
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("value is map = %v (expected %v)", isMap, expectMap),
	}
}

// CheckerContains checks if an array/map contains a value or set of values.
// When expected is a list, checks that ALL items in expected are present in actual.
// Supports actual types: []interface{}, []string, map[string]interface{}, map[string]any.
func CheckerContains(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get(key)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("key %q not found", key),
		}
	}

	// Normalize actual to a set of string keys for uniform checking.
	actualSet := make(map[string]bool)
	switch a := actual.(type) {
	case []interface{}:
		for _, item := range a {
			actualSet[fmt.Sprintf("%v", item)] = true
		}
	case []string:
		for _, item := range a {
			actualSet[item] = true
		}
	case map[string]interface{}:
		for k := range a {
			actualSet[k] = true
		}
	default:
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("value is neither array nor map: %T", actual),
		}
	}

	// If expected is a list, check ALL items are present.
	if expList, ok := expected.([]interface{}); ok {
		var missing []string
		for _, item := range expList {
			s := fmt.Sprintf("%v", item)
			if !actualSet[s] {
				missing = append(missing, s)
			}
		}
		passed := len(missing) == 0
		msg := fmt.Sprintf("contains all %d expected items", len(expList))
		if !passed {
			msg = fmt.Sprintf("missing: %v", missing)
		}
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: passed,
			Message: msg,
		}
	}

	// Single value check.
	s := fmt.Sprintf("%v", expected)
	passed := actualSet[s]
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("contains %q = %v", s, passed),
	}
}

// CheckerContainsOnly checks if an array/map contains EXACTLY the expected items
// (no more, no fewer). Used to verify delta notifications contain only changed attributes.
func CheckerContainsOnly(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get(key)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("key %q not found", key),
		}
	}

	// Normalize actual to a set of string keys.
	actualSet := make(map[string]bool)
	switch a := actual.(type) {
	case []interface{}:
		for _, item := range a {
			actualSet[fmt.Sprintf("%v", item)] = true
		}
	case []string:
		for _, item := range a {
			actualSet[item] = true
		}
	case map[string]interface{}:
		for k := range a {
			actualSet[k] = true
		}
	default:
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("value is neither array nor map: %T", actual),
		}
	}

	// Normalize expected to a set of string keys.
	expectedSet := make(map[string]bool)
	switch e := expected.(type) {
	case []interface{}:
		for _, item := range e {
			expectedSet[fmt.Sprintf("%v", item)] = true
		}
	case []string:
		for _, item := range e {
			expectedSet[item] = true
		}
	case string:
		expectedSet[e] = true
	default:
		expectedSet[fmt.Sprintf("%v", expected)] = true
	}

	// Check both directions: all expected present AND no extras.
	var missing, extra []string
	for k := range expectedSet {
		if !actualSet[k] {
			missing = append(missing, k)
		}
	}
	for k := range actualSet {
		if !expectedSet[k] {
			extra = append(extra, k)
		}
	}

	passed := len(missing) == 0 && len(extra) == 0
	msg := fmt.Sprintf("contains exactly %d expected items", len(expectedSet))
	if !passed {
		parts := []string{}
		if len(missing) > 0 {
			parts = append(parts, fmt.Sprintf("missing: %v", missing))
		}
		if len(extra) > 0 {
			parts = append(parts, fmt.Sprintf("extra: %v", extra))
		}
		msg = strings.Join(parts, "; ")
	}
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: msg,
	}
}

// CheckerMapSizeEquals checks if the size of the "value" map or array equals expected.
// The expected value can be a number or a string referencing a saved state key.
func CheckerMapSizeEquals(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("output key %q not found", "value"),
		}
	}

	// Expected can be a saved state reference (string like "phase_count").
	resolvedExpected := expected
	if ref, ok := expected.(string); ok {
		if saved, savedOK := state.Get(ref); savedOK {
			resolvedExpected = saved
		}
	}

	expectedSize, ok := ToFloat64(resolvedExpected)
	if !ok {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("expected size must be numeric, got %T (%v)", resolvedExpected, resolvedExpected),
		}
	}

	var actualSize int

	// Check if it's a map (handle both string-keyed and any-keyed from CBOR)
	switch m := actual.(type) {
	case map[string]any:
		actualSize = len(m)
	case map[any]any:
		actualSize = len(m)
	case []any:
		actualSize = len(m)
	default:
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("value is neither map nor array: %T", actual),
		}
	}

	passed := float64(actualSize) == expectedSize
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("size = %d (expected %v)", actualSize, expectedSize),
	}
}

// CheckerSaveAs saves the current step's complete output under the given name.
// This allows later steps to compare their output against the saved snapshot
// using the value_equals checker.
func CheckerSaveAs(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	targetKey, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("save_as target must be a string, got %T", expected),
		}
	}

	output, exists := state.Get(InternalStepOutput)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  "no step output to save",
		}
	}

	state.Set(targetKey, output)
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   output,
		Passed:   true,
		Message:  fmt.Sprintf("saved step output as %q", targetKey),
	}
}

// CheckerSaveField saves a specific field from the step output under the given
// key name. The field to extract is derived from the checker name (e.g.,
// "save_delay_as" extracts "connect_duration_ms", "save_subscription_id"
// extracts "subscription_id").
func checkerSaveField(fieldKey string) func(string, interface{}, *ExecutionState) *ExpectResult {
	return func(key string, expected interface{}, state *ExecutionState) *ExpectResult {
		targetKey, ok := expected.(string)
		if !ok {
			return &ExpectResult{Key: key, Expected: expected, Passed: false,
				Message: fmt.Sprintf("%s target must be a string, got %T", key, expected)}
		}
		output, exists := state.Get(InternalStepOutput)
		if !exists {
			return &ExpectResult{Key: key, Expected: expected, Passed: false, Message: "no step output"}
		}
		var val interface{}
		if m, ok := output.(map[string]any); ok {
			val = m[fieldKey]
		}
		if val == nil {
			return &ExpectResult{Key: key, Expected: expected, Passed: false,
				Message: fmt.Sprintf("no %s in output", fieldKey)}
		}
		state.Set(targetKey, val)
		return &ExpectResult{Key: key, Expected: expected, Actual: val, Passed: true,
			Message: fmt.Sprintf("saved %s as %q", fieldKey, targetKey)}
	}
}

// CheckerValueEquals compares the current step's output with a previously saved
// output (stored via save_as). All keys present in the saved map must match.
func CheckerValueEquals(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	savedName, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  fmt.Sprintf("value_equals target must be a string, got %T", expected),
		}
	}

	savedVal, exists := state.Get(savedName)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: savedName,
			Passed:   false,
			Message:  fmt.Sprintf("saved value %q not found", savedName),
		}
	}

	currentOutput, _ := state.Get(InternalStepOutput)

	savedMap, savedIsMap := savedVal.(map[string]interface{})
	currentMap, currentIsMap := currentOutput.(map[string]interface{})

	if savedIsMap && currentIsMap {
		var mismatches []string
		for k, sv := range savedMap {
			cv, has := currentMap[k]
			if !has || fmt.Sprintf("%v", sv) != fmt.Sprintf("%v", cv) {
				mismatches = append(mismatches, fmt.Sprintf("%s: saved=%v current=%v", k, sv, cv))
			}
		}
		passed := len(mismatches) == 0
		msg := "values match"
		if !passed {
			msg = fmt.Sprintf("mismatches: %v", mismatches)
		}
		return &ExpectResult{
			Key:      key,
			Expected: savedVal,
			Actual:   currentOutput,
			Passed:   passed,
			Message:  msg,
		}
	}

	passed := fmt.Sprintf("%v", savedVal) == fmt.Sprintf("%v", currentOutput)
	return &ExpectResult{
		Key:      key,
		Expected: savedVal,
		Actual:   currentOutput,
		Passed:   passed,
		Message:  fmt.Sprintf("saved=%v current=%v", savedVal, currentOutput),
	}
}

// CheckerIssuerFingerprintEquals compares the issuer_fingerprint output of the
// current step against a fingerprint stored in a previously saved output map.
func CheckerIssuerFingerprintEquals(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	savedName, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  fmt.Sprintf("issuer_fingerprint_equals target must be a string, got %T", expected),
		}
	}

	savedVal, exists := state.Get(savedName)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: savedName,
			Passed:   false,
			Message:  fmt.Sprintf("saved value %q not found", savedName),
		}
	}

	// Extract fingerprint from saved value (may be a map or plain string).
	expectedFP := ""
	if savedMap, ok := savedVal.(map[string]interface{}); ok {
		expectedFP, _ = savedMap[KeyFingerprint].(string)
	} else if s, ok := savedVal.(string); ok {
		expectedFP = s
	}

	actualFP, _ := state.Get(KeyIssuerFingerprint)
	actualFPStr := fmt.Sprintf("%v", actualFP)

	passed := actualFPStr == expectedFP && expectedFP != ""
	return &ExpectResult{
		Key:      key,
		Expected: expectedFP,
		Actual:   actualFP,
		Passed:   passed,
		Message:  fmt.Sprintf("issuer_fingerprint=%v expected=%v", actualFP, expectedFP),
	}
}

// CheckerValueGT checks if the "value" output is strictly greater than expected.
// Used in YAML as: value_gt: 0
func CheckerValueGT(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}
	passed := actualNum > expectedNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v > %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerValueGTE checks if the "value" output is >= expected.
// Used in YAML as: value_gte: 1
func CheckerValueGTE(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}
	passed := actualNum >= expectedNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v >= %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerValueMax checks if the "value" output is <= expected (upper bound).
// Used in YAML as: value_max: 5000000
func CheckerValueMax(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}
	passed := actualNum <= expectedNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v <= %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerResponseContains checks if the "result" output map contains the
// expected key(s). Expected can be a string or a list of strings.
// Used in YAML as: response_contains: ["applied"] or response_contains: applied
func CheckerResponseContains(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	result, exists := state.Get("result")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "result"),
		}
	}

	// Collect expected keys from string or list.
	var expectedKeys []string
	switch v := expected.(type) {
	case string:
		expectedKeys = []string{v}
	case []interface{}:
		for _, item := range v {
			expectedKeys = append(expectedKeys, fmt.Sprintf("%v", item))
		}
	default:
		expectedKeys = []string{fmt.Sprintf("%v", expected)}
	}

	// Check against the result map (may have string or any keys).
	var missing []string
	for _, ek := range expectedKeys {
		found := false
		switch m := result.(type) {
		case map[string]any:
			_, found = m[ek]
		case map[any]any:
			for mk := range m {
				if fmt.Sprintf("%v", mk) == ek {
					found = true
					break
				}
			}
		}
		if !found {
			missing = append(missing, ek)
		}
	}

	passed := len(missing) == 0
	msg := fmt.Sprintf("result contains %v", expectedKeys)
	if !passed {
		msg = fmt.Sprintf("result missing keys: %v", missing)
	}
	return &ExpectResult{
		Key: key, Expected: expected, Actual: result, Passed: passed,
		Message: msg,
	}
}

// CheckerValueGTESaved checks if the "value" output is >= a previously saved value.
// Expected is the name under which the reference value was saved.
// Used in YAML as: value_gte_saved: energy_first
func CheckerValueGTESaved(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	savedName, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("value_gte_saved target must be a string, got %T", expected),
		}
	}
	savedVal, exists := state.Get(savedName)
	if !exists {
		return &ExpectResult{
			Key: key, Expected: savedName, Passed: false,
			Message: fmt.Sprintf("saved value %q not found", savedName),
		}
	}
	actual, actualExists := state.Get("value")
	if !actualExists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	// The saved value may be a full step output map; extract "value" if so.
	ref := savedVal
	if savedMap, isMap := savedVal.(map[string]interface{}); isMap {
		if v, has := savedMap["value"]; has {
			ref = v
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	refNum, ok2 := ToFloat64(ref)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, ref),
		}
	}
	passed := actualNum >= refNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v >= saved(%s) %v = %v", actualNum, savedName, refNum, passed),
	}
}

// CheckerValueMaxRef checks if the "value" output is <= a previously saved value.
// Expected is the name under which the reference value was saved.
// Used in YAML as: value_max_ref: ev_max_discharge
func CheckerValueMaxRef(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	savedName, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("value_max_ref target must be a string, got %T", expected),
		}
	}
	savedVal, exists := state.Get(savedName)
	if !exists {
		return &ExpectResult{
			Key: key, Expected: savedName, Passed: false,
			Message: fmt.Sprintf("saved value %q not found", savedName),
		}
	}
	actual, actualExists := state.Get("value")
	if !actualExists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	ref := savedVal
	if savedMap, isMap := savedVal.(map[string]interface{}); isMap {
		if v, has := savedMap["value"]; has {
			ref = v
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	refNum, ok2 := ToFloat64(ref)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, ref),
		}
	}
	passed := actualNum <= refNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v <= saved(%s) %v = %v", actualNum, savedName, refNum, passed),
	}
}

// CheckerValueLTE checks if the "value" output is <= expected.
// Used in YAML as: value_lte: 100
func CheckerValueLTE(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualNum, ok1 := ToFloat64(actual)
	expectedNum, ok2 := ToFloat64(expected)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}
	passed := actualNum <= expectedNum
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v <= %v = %v", actualNum, expectedNum, passed),
	}
}

// CheckerValueNot checks if the "value" output is not equal to expected.
// Comparison uses fmt.Sprintf for type-agnostic equality.
// Used in YAML as: value_not: CHARGING
func CheckerValueNot(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	passed := fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", expected)
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v != %v = %v", actual, expected, passed),
	}
}

// CheckerValueIn checks if the "value" output is one of the expected values.
// Expected should be an array of valid values.
// Used in YAML as: value_in: [1, 2, 3]
func CheckerValueIn(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualStr := fmt.Sprintf("%v", actual)
	switch vals := expected.(type) {
	case []interface{}:
		for _, v := range vals {
			if fmt.Sprintf("%v", v) == actualStr {
				return &ExpectResult{
					Key: key, Expected: expected, Actual: actual, Passed: true,
					Message: fmt.Sprintf("value %v in %v", actual, expected),
				}
			}
		}
	}
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: false,
		Message: fmt.Sprintf("value %v not in %v", actual, expected),
	}
}

// CheckerValueNonNegative checks if the "value" output is >= 0.
// Used in YAML as: value_non_negative: true
func CheckerValueNonNegative(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualNum, ok := ToFloat64(actual)
	if !ok {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot convert to numeric: %T", actual),
		}
	}
	passed := actualNum >= 0
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %v >= 0 = %v", actualNum, passed),
	}
}

// CheckerValueIsArray checks if the "value" output is an array/slice.
// Used in YAML as: value_is_array: true
func CheckerValueIsArray(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	_, isArray := actual.([]interface{})
	expectArray, _ := expected.(bool)
	passed := isArray == expectArray
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value is array = %v (expected %v)", isArray, expectArray),
	}
}

// CheckerValueIsNotNull checks if the "value" output is not nil.
// Used in YAML as: value_is_not_null: true
func CheckerValueIsNotNull(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	isNull := !exists || actual == nil
	passed := !isNull
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value is not null = %v", passed),
	}
}

// CheckerValueIsRecent checks if the "value" output is a recent timestamp
// (within a configurable window, default 30 seconds).
// Used in YAML as: value_is_recent: true
func CheckerValueIsRecent(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	// Accept numeric (unix seconds or millis) timestamps.
	if num, ok := ToFloat64(actual); ok {
		now := float64(time.Now().Unix())
		// If the value looks like milliseconds (> year 2000 in seconds), convert.
		if num > 1e12 {
			num = num / 1000
		}
		diff := now - num
		if diff < 0 {
			diff = -diff
		}
		passed := diff < 30
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: passed,
			Message: fmt.Sprintf("timestamp age %.0fs (threshold 30s)", diff),
		}
	}
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: false,
		Message: fmt.Sprintf("value is not a numeric timestamp: %T", actual),
	}
}

// CheckerValueTreatedAsUnknown checks that the "value" output is treated as
// unknown by the device (nil, 0, or "unknown"). Domain-specific check.
// Used in YAML as: value_treated_as_unknown: true
func CheckerValueTreatedAsUnknown(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists || actual == nil {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: true,
			Message: "value is nil (treated as unknown)",
		}
	}
	actualStr := fmt.Sprintf("%v", actual)
	passed := actualStr == "0" || actualStr == "" || actualStr == "unknown" || actualStr == "<nil>"
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("value %q treated as unknown = %v", actualStr, passed),
	}
}

// CheckerValueType checks the Go type name of the "value" output.
// Used in YAML as: value_type: string
func CheckerValueType(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}
	actualType := fmt.Sprintf("%T", actual)
	expectedStr := fmt.Sprintf("%v", expected)
	passed := actualType == expectedStr
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("type = %s (expected %s)", actualType, expectedStr),
	}
}

// isValidPhaseKey checks if a key represents a valid AC phase identifier.
// Accepts string names (A, B, C) and numeric IDs (0=A, 1=B, 2=C) as they
// appear after CBOR round-trip.
func isValidPhaseKey(k any) bool {
	switch v := k.(type) {
	case string:
		return v == "A" || v == "B" || v == "C" || v == "a" || v == "b" || v == "c"
	case uint64:
		return v <= 2
	case int64:
		return v >= 0 && v <= 2
	case int:
		return v >= 0 && v <= 2
	case uint8:
		return v <= 2
	default:
		return false
	}
}

// isValidGridPhaseValue checks if a value represents a valid grid phase.
// Accepts string names (L1, L2, L3) and numeric IDs (0=L1, 1=L2, 2=L3).
func isValidGridPhaseValue(v any) bool {
	switch val := v.(type) {
	case string:
		return val == "L1" || val == "L2" || val == "L3" || val == "l1" || val == "l2" || val == "l3"
	case uint64:
		return val <= 2
	case int64:
		return val >= 0 && val <= 2
	case int:
		return val >= 0 && val <= 2
	case uint8:
		return val <= 2
	default:
		return false
	}
}

// CheckerKeysArePhases checks if a map's keys are valid AC phase identifiers.
// Accepts string keys (A, B, C) or numeric IDs (0, 1, 2) from CBOR.
// Used in YAML as: keys_are_phases: true
func CheckerKeysArePhases(key string, expected any, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}

	checkKeys := func(keys []any) *ExpectResult {
		if len(keys) == 0 {
			return &ExpectResult{Key: key, Expected: expected, Actual: actual, Passed: false, Message: "map is empty"}
		}
		for _, k := range keys {
			if !isValidPhaseKey(k) {
				return &ExpectResult{
					Key: key, Expected: expected, Actual: actual, Passed: false,
					Message: fmt.Sprintf("key %v is not a valid phase identifier", k),
				}
			}
		}
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: true,
			Message: fmt.Sprintf("all %d keys are valid phase identifiers", len(keys)),
		}
	}

	switch m := actual.(type) {
	case map[string]any:
		keys := make([]any, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return checkKeys(keys)
	case map[any]any:
		keys := make([]any, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return checkKeys(keys)
	default:
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("value is not a map: %T", actual),
		}
	}
}

// CheckerValuesValidGridPhases checks if a map's values are valid grid phase identifiers.
// Accepts string values (L1, L2, L3) or numeric IDs (0, 1, 2) from CBOR.
// Used in YAML as: values_valid_grid_phases: true
func CheckerValuesValidGridPhases(key string, expected any, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}

	checkValues := func(values []any) *ExpectResult {
		if len(values) == 0 {
			return &ExpectResult{Key: key, Expected: expected, Actual: actual, Passed: false, Message: "map is empty"}
		}
		for _, v := range values {
			if !isValidGridPhaseValue(v) {
				return &ExpectResult{
					Key: key, Expected: expected, Actual: actual, Passed: false,
					Message: fmt.Sprintf("value %v is not a valid grid phase identifier", v),
				}
			}
		}
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: true,
			Message: fmt.Sprintf("all %d values are valid grid phase identifiers", len(values)),
		}
	}

	switch m := actual.(type) {
	case map[string]any:
		vals := make([]any, 0, len(m))
		for _, v := range m {
			vals = append(vals, v)
		}
		return checkValues(vals)
	case map[any]any:
		vals := make([]any, 0, len(m))
		for _, v := range m {
			vals = append(vals, v)
		}
		return checkValues(vals)
	default:
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("value is not a map: %T", actual),
		}
	}
}

// CheckerArrayNotEmpty checks if the "value" output is a non-empty array.
// Used in YAML as: array_not_empty: true
func CheckerArrayNotEmpty(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("value")
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("output key %q not found", "value"),
		}
	}

	if arr, ok := actual.([]interface{}); ok {
		passed := len(arr) > 0
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: passed,
			Message: fmt.Sprintf("array length = %d", len(arr)),
		}
	}

	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: false,
		Message: fmt.Sprintf("value is not an array: %T", actual),
	}
}

// CheckerSavePrimingValue saves the priming data from a subscribe response
// under a given name so later steps can compare against it.
// Used in YAML as: save_priming_value: initial_limit
func CheckerSavePrimingValue(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	targetKey, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("save_priming_value target must be a string, got %T", expected),
		}
	}

	primingData, exists := state.Get("_priming_data")
	if !exists || primingData == nil {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: "no priming data available from subscribe",
		}
	}

	state.Set(targetKey, primingData)
	return &ExpectResult{
		Key: key, Expected: expected, Actual: primingData, Passed: true,
		Message: fmt.Sprintf("saved priming data as %q", targetKey),
	}
}

// CheckerPrimingValueDifferentFrom checks that the current priming data differs
// from a previously saved value. Used in YAML as: priming_value_different_from: initial_limit
func CheckerPrimingValueDifferentFrom(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	savedKey, ok := expected.(string)
	if !ok {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("priming_value_different_from target must be a string, got %T", expected),
		}
	}

	savedValue, exists := state.Get(savedKey)
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("no saved value under key %q", savedKey),
		}
	}

	currentPriming, exists := state.Get("_priming_data")
	if !exists || currentPriming == nil {
		return &ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: "no current priming data available",
		}
	}

	// Compare: different means the test passes.
	different := fmt.Sprintf("%v", currentPriming) != fmt.Sprintf("%v", savedValue)
	return &ExpectResult{
		Key: key, Expected: expected, Actual: currentPriming, Passed: different,
		Message: fmt.Sprintf("priming value different from saved %q: %v", savedKey, different),
	}
}

// CheckerErrorMessageContains checks if the error_message_contains output contains the expected substring.
// This is used for validating error messages from protocol responses.
func CheckerErrorMessageContains(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get(key)
	if !exists {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  fmt.Sprintf("key %q not found in outputs", key),
		}
	}

	actualStr, ok1 := actual.(string)
	expectedStr, ok2 := expected.(string)
	if !ok1 || !ok2 {
		return &ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("expected string types for contains check, got %T and %T", actual, expected),
		}
	}

	passed := strings.Contains(actualStr, expectedStr)
	return &ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("error message contains %q: %v", expectedStr, passed),
	}
}

// CheckerNoError verifies the "error" output field is absent, nil, or empty.
// Used in YAML as: no_error: true
func CheckerNoError(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get("error")
	if !exists || actual == nil || actual == "" {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: true,
			Message: "no error present",
		}
	}
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: false,
		Message: fmt.Sprintf("error present: %v", actual),
	}
}

// CheckerDurationUnder checks that actual duration is under the expected threshold.
// The expected value is a duration string (e.g. "1000ms"). The actual value from
// the handler is a time.Duration (nanoseconds as int64) or a boolean true
// (meaning latency ~0 in simulated mode).
func CheckerDurationUnder(key string, expected interface{}, state *ExecutionState) *ExpectResult {
	actual, exists := state.Get(key)
	if !exists {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: nil, Passed: false,
			Message: fmt.Sprintf("key %q not found in outputs", key),
		}
	}

	// Parse expected threshold as a duration.
	threshold, err := parseDuration(expected)
	if err != nil {
		// Fall back to string comparison if threshold is not a duration.
		passed := fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: passed,
			Message: fmt.Sprintf("%s: expected %v, got %v", key, expected, actual),
		}
	}

	// Parse actual value as a duration.
	actualDur, err := parseDuration(actual)
	if err != nil {
		return &ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot parse actual value %v as duration", actual),
		}
	}

	passed := actualDur < threshold
	return &ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("%s: %v < %v = %v", key, actualDur, threshold, passed),
	}
}

// parseDuration parses a value as time.Duration. Supports:
// - time.Duration (returned as-is)
// - bool true (treated as 0, i.e. instant/simulated)
// - string ("1000ms", "5s", etc.)
// - numeric int/float (treated as milliseconds)
func parseDuration(v interface{}) (time.Duration, error) {
	switch val := v.(type) {
	case time.Duration:
		return val, nil
	case bool:
		if val {
			return 0, nil // true = instant/simulated
		}
		return 0, fmt.Errorf("false is not a valid duration")
	case string:
		return time.ParseDuration(val)
	case int:
		return time.Duration(val) * time.Millisecond, nil
	case int64:
		return time.Duration(val) * time.Millisecond, nil
	case float64:
		return time.Duration(val * float64(time.Millisecond)), nil
	default:
		return 0, fmt.Errorf("unsupported duration type %T", v)
	}
}

// RegisterEnhancedCheckers registers all enhanced checkers with the engine.
func RegisterEnhancedCheckers(e *Engine) {
	e.RegisterChecker(CheckerNameValueGreaterThan, CheckerValueGreaterThan)
	e.RegisterChecker(CheckerNameValueLessThan, CheckerValueLessThan)
	e.RegisterChecker(CheckerNameValueInRange, CheckerValueInRange)
	e.RegisterChecker(CheckerNameValueIsNull, CheckerValueIsNull)
	e.RegisterChecker(CheckerNameValueIsMap, CheckerValueIsMap)
	e.RegisterChecker(CheckerNameContains, CheckerContains)
	e.RegisterChecker(CheckerNameContainsOnly, CheckerContainsOnly)
	e.RegisterChecker(CheckerNameMapSizeEquals, CheckerMapSizeEquals)
	e.RegisterChecker(CheckerNameSaveAs, CheckerSaveAs)
	e.RegisterChecker("save_subscription_id", checkerSaveField("subscription_id"))
	e.RegisterChecker("save_delay_as", checkerSaveField("connect_duration_ms"))
	e.RegisterChecker(CheckerNameValueEquals, CheckerValueEquals)
	e.RegisterChecker(CheckerNameIssuerFingerprintEquals, CheckerIssuerFingerprintEquals)

	// Short-form checkers that operate on the "value" output key.
	e.RegisterChecker(CheckerNameValueGT, CheckerValueGT)
	e.RegisterChecker(CheckerNameValueGTE, CheckerValueGTE)
	e.RegisterChecker(CheckerNameValueMax, CheckerValueMax)
	e.RegisterChecker(CheckerNameValueLTE, CheckerValueLTE)
	e.RegisterChecker(CheckerNameValueNot, CheckerValueNot)
	e.RegisterChecker(CheckerNameValueNotEqual, CheckerValueNot)       // alias
	e.RegisterChecker(CheckerNameValueDifferentFrom, CheckerValueNot)  // alias
	e.RegisterChecker(CheckerNameValueAtLeast, CheckerValueGTE)        // alias
	e.RegisterChecker(CheckerNameValueGreaterOrEqual, CheckerValueGTE) // alias
	e.RegisterChecker(CheckerNameValueIn, CheckerValueIn)
	e.RegisterChecker(CheckerNameValueNonNegative, CheckerValueNonNegative)
	e.RegisterChecker(CheckerNameValueIsArray, CheckerValueIsArray)
	e.RegisterChecker(CheckerNameValueIsNotNull, CheckerValueIsNotNull)
	e.RegisterChecker(CheckerNameValueIsRecent, CheckerValueIsRecent)
	e.RegisterChecker(CheckerNameValueTreatedAsUnknown, CheckerValueTreatedAsUnknown)
	e.RegisterChecker(CheckerNameValueType, CheckerValueType)
	e.RegisterChecker(CheckerNameResponseContains, CheckerResponseContains)
	e.RegisterChecker(CheckerNameValueGTESaved, CheckerValueGTESaved)
	e.RegisterChecker(CheckerNameValueMaxRef, CheckerValueMaxRef)
	e.RegisterChecker(CheckerNameKeysArePhases, CheckerKeysArePhases)
	e.RegisterChecker(CheckerNameKeysValidPhases, CheckerKeysArePhases) // alias
	e.RegisterChecker(CheckerNameValuesValidGridPhases, CheckerValuesValidGridPhases)
	e.RegisterChecker(CheckerNameArrayNotEmpty, CheckerArrayNotEmpty)
	e.RegisterChecker(CheckerNameSavePrimingValue, CheckerSavePrimingValue)
	e.RegisterChecker(CheckerNamePrimingValueDiffFrom, CheckerPrimingValueDifferentFrom)
	e.RegisterChecker(CheckerNameErrorMessageContains, CheckerErrorMessageContains)
	e.RegisterChecker(CheckerNameNoError, CheckerNoError)
	e.RegisterChecker(CheckerNameLatencyUnder, CheckerDurationUnder)
	e.RegisterChecker(CheckerNameAverageLatencyUnder, CheckerDurationUnder) // same logic
}
