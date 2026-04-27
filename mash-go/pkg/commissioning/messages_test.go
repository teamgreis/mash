package commissioning_test

import (
	"testing"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// TestErrorCodeZoneTypeExists_Value verifies the error code constant is 10.
func TestErrorCodeZoneTypeExists_Value(t *testing.T) {
	if commissioning.ErrCodeZoneTypeExists != 10 {
		t.Errorf("ErrCodeZoneTypeExists: expected 10, got %d", commissioning.ErrCodeZoneTypeExists)
	}
}

// TestErrorCodeString_ZoneTypeExists verifies the string representation of ZONE_TYPE_EXISTS.
func TestErrorCodeString_ZoneTypeExists(t *testing.T) {
	str := commissioning.ErrorCodeString(commissioning.ErrCodeZoneTypeExists)
	if str != "zone type already exists" {
		t.Errorf("ErrorCodeString(ErrCodeZoneTypeExists): expected 'zone type already exists', got '%s'", str)
	}
}

// TestErrorCodeString_AllCodes verifies string representation for all error codes.
func TestErrorCodeString_AllCodes(t *testing.T) {
	tests := []struct {
		code     uint8
		expected string
	}{
		{commissioning.ErrCodeSuccess, "success"},
		// DEC-047: All SPAKE2+ failures collapse to a single generic
		// "authentication failed" code (ErrCodeAuthFailed = 1). The
		// pre-DEC-047 ErrCodeInvalidPublicKey / ErrCodeConfirmFailed
		// constants were removed in the Issue #6 fix; the legacy
		// numeric value 2 is still recognised on the wire by the
		// runner classifier for backward-compat with older devices.
		{commissioning.ErrCodeAuthFailed, "authentication failed"},
		{commissioning.ErrCodeCSRFailed, "CSR generation failed"},
		{commissioning.ErrCodeCertInstallFailed, "certificate installation failed"},
		{commissioning.ErrCodeBusy, "device busy"},
		{commissioning.ErrCodeZoneTypeExists, "zone type already exists"},
		{commissioning.ErrCodeInternalError, "internal error"},
		{99, "unknown error (99)"}, // Unknown code
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := commissioning.ErrorCodeString(tt.code)
			if got != tt.expected {
				t.Errorf("ErrorCodeString(%d): expected '%s', got '%s'", tt.code, tt.expected, got)
			}
		})
	}
}

// TestCertInstallResponse_ZoneTypeExistsError verifies CertInstallResponse can encode the ZONE_TYPE_EXISTS error.
func TestCertInstallResponse_ZoneTypeExistsError(t *testing.T) {
	resp := &commissioning.CertInstallResponse{
		MsgType:   commissioning.MsgCertInstallResponse,
		ErrorCode: commissioning.ErrCodeZoneTypeExists,
	}

	// Encode
	data, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedResp, ok := decoded.(*commissioning.CertInstallResponse)
	if !ok {
		t.Fatalf("Expected *CertInstallResponse, got %T", decoded)
	}

	if decodedResp.ErrorCode != commissioning.ErrCodeZoneTypeExists {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeZoneTypeExists, decodedResp.ErrorCode)
	}
}

// =============================================================================
// ErrCodeBusy + RetryAfter tests (DEC-063)
// =============================================================================

// TestErrCodeBusy_Value verifies the error code constant is 5.
func TestErrCodeBusy_Value(t *testing.T) {
	if commissioning.ErrCodeBusy != 5 {
		t.Errorf("ErrCodeBusy: expected 5, got %d", commissioning.ErrCodeBusy)
	}
}

// TestErrCodeBusy_String verifies the string representation of DEVICE_BUSY.
func TestErrCodeBusy_String(t *testing.T) {
	str := commissioning.ErrorCodeString(commissioning.ErrCodeBusy)
	if str != "device busy" {
		t.Errorf("ErrorCodeString(ErrCodeBusy): expected 'device busy', got '%s'", str)
	}
}

// TestCommissioningError_BusyWithRetryAfter_RoundTrip verifies CBOR round-trip
// for a busy error with RetryAfter set.
func TestCommissioningError_BusyWithRetryAfter_RoundTrip(t *testing.T) {
	errMsg := &commissioning.CommissioningError{
		MsgType:    commissioning.MsgCommissioningError,
		ErrorCode:  commissioning.ErrCodeBusy,
		Message:    "commissioning already in progress",
		RetryAfter: 5000,
	}

	// Encode
	data, err := commissioning.EncodePASEMessage(errMsg)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedErr, ok := decoded.(*commissioning.CommissioningError)
	if !ok {
		t.Fatalf("Expected *CommissioningError, got %T", decoded)
	}

	if decodedErr.ErrorCode != commissioning.ErrCodeBusy {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeBusy, decodedErr.ErrorCode)
	}
	if decodedErr.Message != errMsg.Message {
		t.Errorf("Message: expected '%s', got '%s'", errMsg.Message, decodedErr.Message)
	}
	if decodedErr.RetryAfter != 5000 {
		t.Errorf("RetryAfter: expected 5000, got %d", decodedErr.RetryAfter)
	}
}

// TestCommissioningError_RetryAfterZero_Omitted verifies that RetryAfter=0
// is not serialized (omitempty), keeping backward compatibility.
func TestCommissioningError_RetryAfterZero_EncodesSmaller(t *testing.T) {
	errMsg := &commissioning.CommissioningError{
		MsgType:    commissioning.MsgCommissioningError,
		ErrorCode:  commissioning.ErrCodeCSRFailed,
		Message:    "CSR failed",
		RetryAfter: 0,
	}

	data, err := commissioning.EncodePASEMessage(errMsg)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode and verify RetryAfter is 0 (default)
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedErr := decoded.(*commissioning.CommissioningError)
	if decodedErr.RetryAfter != 0 {
		t.Errorf("RetryAfter: expected 0, got %d", decodedErr.RetryAfter)
	}

	// Verify key 4 is not in the CBOR data by checking encoding size.
	// A message without RetryAfter should be smaller than one with it.
	withRetry := &commissioning.CommissioningError{
		MsgType:    commissioning.MsgCommissioningError,
		ErrorCode:  commissioning.ErrCodeCSRFailed,
		Message:    "CSR failed",
		RetryAfter: 1000,
	}
	dataWithRetry, _ := commissioning.EncodePASEMessage(withRetry)
	if len(data) >= len(dataWithRetry) {
		t.Errorf("Expected RetryAfter=0 to produce smaller encoding than RetryAfter=1000: zero=%d, thousand=%d", len(data), len(dataWithRetry))
	}
}

// TestCommissioningError_BackwardCompat_NoRetryAfter verifies that CBOR data
// encoded without key 4 (RetryAfter) decodes cleanly with RetryAfter=0.
func TestCommissioningError_BackwardCompat_NoRetryAfter(t *testing.T) {
	// Encode a message without RetryAfter (simulating old format)
	oldMsg := &commissioning.CommissioningError{
		MsgType:   commissioning.MsgCommissioningError,
		ErrorCode: commissioning.ErrCodeAuthFailed,
		Message:   "auth failed",
	}

	data, err := commissioning.EncodePASEMessage(oldMsg)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode -- RetryAfter should be 0 (zero value)
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode old-format message: %v", err)
	}

	decodedErr := decoded.(*commissioning.CommissioningError)
	if decodedErr.RetryAfter != 0 {
		t.Errorf("RetryAfter from old format: expected 0, got %d", decodedErr.RetryAfter)
	}
	if decodedErr.ErrorCode != commissioning.ErrCodeAuthFailed {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeAuthFailed, decodedErr.ErrorCode)
	}
}

// =============================================================================
// DEC-085 namespace separation: commissioning decoder rejects renewal MsgTypes
// and vice versa. These tests pin the invariant that `mash-comm/1` carries
// only MsgTypes 1-4, 10-13, 20, 255 and `mash/1` renewal carries only 30-33.
// =============================================================================

// TestDecodePASEMessage_RejectsRenewalMsgTypes verifies that the commissioning
// decoder treats MsgTypes in the renewal range (30-33) as unknown. Prevents
// an attacker (or a buggy controller) from emitting a renewal-shaped frame
// on the commissioning ALPN to exploit a dual-purpose envelope.
func TestDecodePASEMessage_RejectsRenewalMsgTypes(t *testing.T) {
	for _, msgType := range []uint8{30, 31, 32, 33} {
		t.Run("MsgType_"+commissioning.ErrorCodeString(msgType), func(t *testing.T) {
			// Hand-build a flat CBOR map with key 1 = msgType. A trivial
			// payload (CSR-shaped, with bytes at key 2) that would decode
			// fine as CertRenewalCSR via DecodeRenewalMessage.
			data, err := commissioning.EncodePASEMessage(map[int]any{
				1: msgType,
				2: []byte{0xde, 0xad},
			})
			if err != nil {
				t.Fatalf("encode helper map: %v", err)
			}
			if _, err := commissioning.DecodePASEMessage(data); err == nil {
				t.Fatalf("DecodePASEMessage accepted renewal MsgType %d on commissioning plane", msgType)
			}
		})
	}
}

// TestDecodeRenewalMessage_RejectsCommissioningMsgTypes verifies the symmetric
// invariant: a commissioning MsgType (1-4, 10-13, 20, 255) on the renewal
// decoder is unknown. Prevents an out-of-channel commissioning frame from
// being processed by the operational dispatcher's renewal sub-handler.
func TestDecodeRenewalMessage_RejectsCommissioningMsgTypes(t *testing.T) {
	for _, msgType := range []uint8{1, 2, 3, 4, 10, 11, 12, 13, 20, 255} {
		t.Run("MsgType_"+commissioning.ErrorCodeString(msgType), func(t *testing.T) {
			data, err := commissioning.EncodePASEMessage(map[int]any{
				1: msgType,
				2: []byte{0xde, 0xad},
			})
			if err != nil {
				t.Fatalf("encode helper map: %v", err)
			}
			if _, err := commissioning.DecodeRenewalMessage(data); err == nil {
				t.Fatalf("DecodeRenewalMessage accepted commissioning MsgType %d on renewal plane", msgType)
			}
		})
	}
}

// TestCommissioningError_ZoneTypeExists verifies CommissioningError can use ZONE_TYPE_EXISTS.
func TestCommissioningError_ZoneTypeExists(t *testing.T) {
	errMsg := &commissioning.CommissioningError{
		MsgType:   commissioning.MsgCommissioningError,
		ErrorCode: commissioning.ErrCodeZoneTypeExists,
		Message:   "zone of this type already exists",
	}

	// Encode
	data, err := commissioning.EncodePASEMessage(errMsg)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedErr, ok := decoded.(*commissioning.CommissioningError)
	if !ok {
		t.Fatalf("Expected *CommissioningError, got %T", decoded)
	}

	if decodedErr.ErrorCode != commissioning.ErrCodeZoneTypeExists {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeZoneTypeExists, decodedErr.ErrorCode)
	}
	if decodedErr.Message != errMsg.Message {
		t.Errorf("Message: expected '%s', got '%s'", errMsg.Message, decodedErr.Message)
	}
}
