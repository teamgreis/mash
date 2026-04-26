package commissioning_test

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// TestPASERequestEncoding verifies PASE request message encoding/decoding.
func TestPASERequestEncoding(t *testing.T) {
	req := &commissioning.PASERequest{
		MsgType:        commissioning.MsgPASERequest,
		PublicValue:    []byte{0x04, 0x01, 0x02, 0x03}, // Mock public value
		ClientIdentity: []byte("controller-001"),
	}

	// Encode
	data, err := commissioning.EncodePASEMessage(req)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedReq, ok := decoded.(*commissioning.PASERequest)
	if !ok {
		t.Fatalf("Expected *PASERequest, got %T", decoded)
	}

	if decodedReq.MsgType != req.MsgType {
		t.Errorf("MsgType mismatch: expected %d, got %d", req.MsgType, decodedReq.MsgType)
	}
	if !bytes.Equal(decodedReq.PublicValue, req.PublicValue) {
		t.Errorf("PublicValue mismatch")
	}
	if !bytes.Equal(decodedReq.ClientIdentity, req.ClientIdentity) {
		t.Errorf("ClientIdentity mismatch")
	}
}

// TestPASEResponseEncoding verifies PASE response message encoding/decoding.
func TestPASEResponseEncoding(t *testing.T) {
	resp := &commissioning.PASEResponse{
		MsgType:     commissioning.MsgPASEResponse,
		PublicValue: []byte{0x04, 0x05, 0x06, 0x07}, // Mock public value
	}

	data, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedResp, ok := decoded.(*commissioning.PASEResponse)
	if !ok {
		t.Fatalf("Expected *PASEResponse, got %T", decoded)
	}

	if !bytes.Equal(decodedResp.PublicValue, resp.PublicValue) {
		t.Errorf("PublicValue mismatch")
	}
}

// TestPASEConfirmEncoding verifies PASE confirm message encoding/decoding.
func TestPASEConfirmEncoding(t *testing.T) {
	confirm := &commissioning.PASEConfirm{
		MsgType:      commissioning.MsgPASEConfirm,
		Confirmation: make([]byte, 32), // Mock confirmation MAC
	}

	data, err := commissioning.EncodePASEMessage(confirm)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedConfirm, ok := decoded.(*commissioning.PASEConfirm)
	if !ok {
		t.Fatalf("Expected *PASEConfirm, got %T", decoded)
	}

	if !bytes.Equal(decodedConfirm.Confirmation, confirm.Confirmation) {
		t.Errorf("Confirmation mismatch")
	}
}

// TestPASECompleteEncoding verifies PASE complete message encoding/decoding.
func TestPASECompleteEncoding(t *testing.T) {
	complete := &commissioning.PASEComplete{
		MsgType:      commissioning.MsgPASEComplete,
		Confirmation: make([]byte, 32),
		ErrorCode:    commissioning.ErrCodeSuccess,
	}

	data, err := commissioning.EncodePASEMessage(complete)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedComplete, ok := decoded.(*commissioning.PASEComplete)
	if !ok {
		t.Fatalf("Expected *PASEComplete, got %T", decoded)
	}

	if decodedComplete.ErrorCode != complete.ErrorCode {
		t.Errorf("ErrorCode mismatch: expected %d, got %d", complete.ErrorCode, decodedComplete.ErrorCode)
	}
}

// TestPASEErrorEncoding verifies commissioning error message encoding/decoding.
//
// CommissioningError is the spec-defined envelope for cross-cutting
// commissioning rejections (DEC-063 device busy etc.) — see security.md §13.
// In-PASE failures use PASEComplete{ErrCodeAuthFailed} per spec §2.5; this
// round-trip test exercises the busy path because that is the only legitimate
// use of the CommissioningError envelope.
func TestPASEErrorEncoding(t *testing.T) {
	errMsg := &commissioning.CommissioningError{
		MsgType:    commissioning.MsgCommissioningError,
		ErrorCode:  commissioning.ErrCodeBusy,
		Message:    "device busy",
		RetryAfter: 5000,
	}

	data, err := commissioning.EncodePASEMessage(errMsg)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedErr, ok := decoded.(*commissioning.CommissioningError)
	if !ok {
		t.Fatalf("Expected *CommissioningError, got %T", decoded)
	}

	if decodedErr.ErrorCode != errMsg.ErrorCode {
		t.Errorf("ErrorCode mismatch")
	}
	if decodedErr.Message != errMsg.Message {
		t.Errorf("Message mismatch")
	}
	if decodedErr.RetryAfter != errMsg.RetryAfter {
		t.Errorf("RetryAfter mismatch: expected %d, got %d", errMsg.RetryAfter, decodedErr.RetryAfter)
	}
}

// TestDecodeUnknownMessageType verifies error handling for unknown message types.
func TestDecodeUnknownMessageType(t *testing.T) {
	// Create a CBOR message with unknown type
	msg := map[int]interface{}{
		1: 99, // Unknown message type
	}
	data, _ := cbor.Marshal(msg)

	_, err := commissioning.DecodePASEMessage(data)
	if err == nil {
		t.Error("Expected error for unknown message type")
	}
}

// TestCSRRequestEncoding verifies CSR request message encoding/decoding.
func TestCSRRequestEncoding(t *testing.T) {
	req := &commissioning.CSRRequest{
		MsgType: commissioning.MsgCSRRequest,
		Nonce:   make([]byte, 32),
	}

	data, err := commissioning.EncodePASEMessage(req)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedReq, ok := decoded.(*commissioning.CSRRequest)
	if !ok {
		t.Fatalf("Expected *CSRRequest, got %T", decoded)
	}

	if !bytes.Equal(decodedReq.Nonce, req.Nonce) {
		t.Errorf("Nonce mismatch")
	}
}

// TestCSRResponseEncoding verifies CSR response message encoding/decoding.
func TestCSRResponseEncoding(t *testing.T) {
	resp := &commissioning.CSRResponse{
		MsgType:   commissioning.MsgCSRResponse,
		CSR:       []byte("mock-csr-data"),
		ErrorCode: commissioning.ErrCodeSuccess,
	}

	data, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedResp, ok := decoded.(*commissioning.CSRResponse)
	if !ok {
		t.Fatalf("Expected *CSRResponse, got %T", decoded)
	}

	if !bytes.Equal(decodedResp.CSR, resp.CSR) {
		t.Errorf("CSR mismatch")
	}
}

// TestCertInstallEncoding verifies certificate install message encoding/decoding.
func TestCertInstallEncoding(t *testing.T) {
	install := &commissioning.CertInstall{
		MsgType:         commissioning.MsgCertInstall,
		OperationalCert: []byte("mock-op-cert"),
		CACert:          []byte("mock-ca-cert"),
		ZoneType:        2, // LOCAL
		ZonePriority:    1,
	}

	data, err := commissioning.EncodePASEMessage(install)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedInstall, ok := decoded.(*commissioning.CertInstall)
	if !ok {
		t.Fatalf("Expected *CertInstall, got %T", decoded)
	}

	if !bytes.Equal(decodedInstall.OperationalCert, install.OperationalCert) {
		t.Errorf("OperationalCert mismatch")
	}
	if !bytes.Equal(decodedInstall.CACert, install.CACert) {
		t.Errorf("CACert mismatch")
	}
	if decodedInstall.ZoneType != install.ZoneType {
		t.Errorf("ZoneType mismatch")
	}
}

// TestCertInstallResponseEncoding verifies certificate install response encoding/decoding.
func TestCertInstallResponseEncoding(t *testing.T) {
	resp := &commissioning.CertInstallResponse{
		MsgType:   commissioning.MsgCertInstallResponse,
		ErrorCode: commissioning.ErrCodeSuccess,
	}

	data, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	decoded, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	decodedResp, ok := decoded.(*commissioning.CertInstallResponse)
	if !ok {
		t.Fatalf("Expected *CertInstallResponse, got %T", decoded)
	}

	if decodedResp.ErrorCode != resp.ErrorCode {
		t.Errorf("ErrorCode mismatch")
	}
}
