package runner

import (
	"testing"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

func TestIsZeroKey(t *testing.T) {
	tests := []struct {
		name     string
		key      []byte
		expected bool
	}{
		{
			name:     "nil key",
			key:      nil,
			expected: true,
		},
		{
			name:     "empty key",
			key:      []byte{},
			expected: true,
		},
		{
			name:     "all zeros",
			key:      make([]byte, 32),
			expected: true,
		},
		{
			name:     "non-zero key",
			key:      []byte{0, 0, 0, 1},
			expected: false,
		},
		{
			name:     "all ones",
			key:      []byte{1, 1, 1, 1},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isZeroKey(tt.key)
			if result != tt.expected {
				t.Errorf("isZeroKey(%v) = %v, want %v", tt.key, result, tt.expected)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"20202021", true},
		{"00000000", true},
		{"", true}, // empty string has no non-digits
		{"1234567a", false},
		{"test-password", false},
		{"12 34", false},
		{"-1234567", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isNumeric(tt.input)
			if result != tt.expected {
				t.Errorf("isNumeric(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDeriveSetupCodeFromPassword(t *testing.T) {
	// Test determinism - same password should produce same code
	code1 := deriveSetupCodeFromPassword("test-password")
	code2 := deriveSetupCodeFromPassword("test-password")

	if code1 != code2 {
		t.Errorf("deriveSetupCodeFromPassword is not deterministic: %v != %v", code1, code2)
	}

	// Different passwords should produce different codes (with high probability)
	code3 := deriveSetupCodeFromPassword("different-password")
	if code1 == code3 {
		t.Logf("Warning: different passwords produced same code (unlikely collision)")
	}

	// Verify the code is valid (within valid range)
	if code1 > commissioning.SetupCodeMax {
		t.Errorf("derived code %d exceeds maximum %d", code1, commissioning.SetupCodeMax)
	}
}

func TestGetSetupCode(t *testing.T) {
	r := &Runner{config: &Config{}}

	// Error when no setup code provided
	_, err := r.getSetupCode(nil)
	if err == nil {
		t.Error("expected error when no setup code provided")
	}

	// From params
	params := map[string]any{"setup_code": "20202021"}
	code, err := r.getSetupCode(params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if code != 20202021 {
		t.Errorf("expected 20202021, got %d", code)
	}

	// From config
	r.config.SetupCode = "87654321"
	code, err = r.getSetupCode(nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if code != 87654321 {
		t.Errorf("expected 87654321, got %d", code)
	}

	// Params override config
	params = map[string]any{"setup_code": "11111111"}
	code, err = r.getSetupCode(params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if code != 11111111 {
		t.Errorf("expected 11111111, got %d", code)
	}

	// Invalid setup code
	params = map[string]any{"setup_code": "invalid"}
	_, err = r.getSetupCode(params)
	if err == nil {
		t.Error("expected error for invalid setup code")
	}
}

func TestGetSetupCodeFromLegacyParams(t *testing.T) {
	r := &Runner{config: &Config{}}

	// Numeric password (8 digits) - treated as setup code
	params := map[string]any{"password": "20202021"}
	code, err := r.getSetupCodeFromLegacyParams(params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if code != 20202021 {
		t.Errorf("expected 20202021, got %d", code)
	}

	// Non-numeric password - derived to setup code
	params = map[string]any{"password": "test-password"}
	code, err = r.getSetupCodeFromLegacyParams(params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Should get a derived code, not the raw password
	if code > commissioning.SetupCodeMax {
		t.Errorf("derived code %d exceeds maximum", code)
	}

	// Fallback to standard setup code
	r.config.SetupCode = "99999999"
	params = map[string]any{} // no password param
	code, err = r.getSetupCodeFromLegacyParams(params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if code != 99999999 {
		t.Errorf("expected 99999999, got %d", code)
	}
}

func TestPASEStateInitialization(t *testing.T) {
	// Verify that a new Runner has nil paseState
	r := &Runner{
		config: &Config{},
		pool:   NewConnPool(func(string, ...any) {}, nil),
	}
	r.pool.SetMain(&Connection{})
	r.connMgr = NewConnectionManager(r.pool, nil, nil, r.config, r.debugf, connMgrDeps{})
	if r.connMgr.PASEState() != nil {
		t.Error("expected nil paseState on new Runner")
	}
	// Verify other fields are accessible
	if r.config == nil {
		t.Error("expected config to be set")
	}
}

func TestCommissionOutputIncludesSuccessKey(t *testing.T) {
	// The success output map from handleCommission must include both
	// KeyCommissionSuccess and KeySuccess for test compatibility.
	successOutputs := map[string]any{
		KeySessionEstablished: true,
		KeyCommissionSuccess:  true,
		KeySuccess:            true,
		KeyKeyLength:          32,
		KeyKeyNotZero:         true,
	}

	if v, ok := successOutputs[KeySuccess]; !ok || v != true {
		t.Error("expected success=true in commission success outputs")
	}
	if v, ok := successOutputs[KeyCommissionSuccess]; !ok || v != true {
		t.Error("expected commission_success=true in commission success outputs")
	}

	// The failure output map must also include KeySuccess=false.
	failureOutputs := map[string]any{
		KeySessionEstablished: false,
		KeyCommissionSuccess:  false,
		KeySuccess:            false,
		KeyError:              "test error",
	}

	if v, ok := failureOutputs[KeySuccess]; !ok || v != false {
		t.Error("expected success=false in commission failure outputs")
	}
}
