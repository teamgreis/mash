package engine

// Infrastructure keys used internally by the engine.
const (
	InternalStepOutput = "__step_output"
)

// State keys referenced by engine checkers. These correspond to output keys
// set by runner handlers and looked up by engine-level checkers.
const (
	KeyIssuerFingerprint = "issuer_fingerprint"
	KeyFingerprint       = "fingerprint"
)

// Checker registration names -- the string values that appear in YAML test
// files and are used as map keys in Engine.checkers.
const (
	CheckerNameDefault                 = "default"
	CheckerNameValueGreaterThan        = "value_greater_than"
	CheckerNameValueLessThan           = "value_less_than"
	CheckerNameValueInRange            = "value_in_range"
	CheckerNameValueIsNull             = "value_is_null"
	CheckerNameValueIsMap              = "value_is_map"
	CheckerNameContains                = "contains"
	CheckerNameContainsOnly            = "contains_only"
	CheckerNameMapSizeEquals           = "map_size_equals"
	CheckerNameSaveAs                  = "save_as"
	CheckerNameValueEquals             = "value_equals"
	CheckerNameIssuerFingerprintEquals = "issuer_fingerprint_equals"

	// Short-form checkers that operate on the "value" output key.
	CheckerNameValueGT              = "value_gt"
	CheckerNameValueGTE             = "value_gte"
	CheckerNameValueMax             = "value_max"
	CheckerNameValueLTE             = "value_lte"
	CheckerNameValueNot             = "value_not"
	CheckerNameValueNotEqual        = "value_not_equal"
	CheckerNameValueAtLeast         = "value_at_least"
	CheckerNameValueGreaterOrEqual  = "value_greater_or_equal"
	CheckerNameValueDifferentFrom   = "value_different_from"
	CheckerNameValueIn              = "value_in"
	CheckerNameValueNonNegative     = "value_non_negative"
	CheckerNameValueIsArray         = "value_is_array"
	CheckerNameValueIsNotNull       = "value_is_not_null"
	CheckerNameValueIsRecent        = "value_is_recent"
	CheckerNameValueTreatedAsUnknown = "value_treated_as_unknown"
	CheckerNameValueType            = "value_type"
	CheckerNameResponseContains     = "response_contains"
	CheckerNameValueGTESaved        = "value_gte_saved"
	CheckerNameValueMaxRef          = "value_max_ref"
	CheckerNameKeysArePhases          = "keys_are_phases"
	CheckerNameKeysValidPhases        = "keys_valid_phases"
	CheckerNameValuesValidGridPhases  = "values_valid_grid_phases"
	CheckerNameArrayNotEmpty          = "array_not_empty"
	CheckerNameEachItemHas            = "each_item_has"
	CheckerNameSavePrimingValue       = "save_priming_value"
	CheckerNamePrimingValueDiffFrom   = "priming_value_different_from"
	CheckerNameErrorMessageContains   = "error_message_contains"
	CheckerNameNoError                = "no_error"
	CheckerNameLatencyUnder           = "latency_under"
	CheckerNameAverageLatencyUnder    = "average_latency_under"
)
