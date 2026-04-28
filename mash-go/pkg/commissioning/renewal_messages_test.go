package commissioning_test

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// TestCertRenewalRequest_Encode verifies CertRenewalRequest encoding/decoding.
func TestCertRenewalRequest_Encode(t *testing.T) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	req := &commissioning.CertRenewalRequest{
		MsgType: commissioning.MsgCertRenewalRequest,
		Nonce:   nonce,
	}

	// Encode
	data, err := cbor.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Verify raw CBOR structure has correct keys
	var decoded map[int]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode as map: %v", err)
	}

	if decoded[1] != uint64(commissioning.MsgCertRenewalRequest) {
		t.Errorf("Expected msgType %d, got %v", commissioning.MsgCertRenewalRequest, decoded[1])
	}

	nonceBytes, ok := decoded[2].([]byte)
	if !ok {
		t.Fatalf("Expected nonce as bytes, got %T", decoded[2])
	}
	if len(nonceBytes) != 32 {
		t.Errorf("Expected 32-byte nonce, got %d bytes", len(nonceBytes))
	}

	// Verify round-trip via typed decode
	var roundTrip commissioning.CertRenewalRequest
	if err := cbor.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Failed to decode to struct: %v", err)
	}

	if roundTrip.MsgType != req.MsgType {
		t.Errorf("MsgType mismatch: expected %d, got %d", req.MsgType, roundTrip.MsgType)
	}
	if !bytes.Equal(roundTrip.Nonce, req.Nonce) {
		t.Error("Nonce mismatch")
	}
}

// TestCertRenewalCSR_Encode verifies CertRenewalCSR encoding/decoding.
func TestCertRenewalCSR_Encode(t *testing.T) {
	// Mock DER-encoded CSR (real CSRs start with 0x30 0x82 for SEQUENCE)
	mockCSR := []byte{0x30, 0x82, 0x01, 0x00, 0xDE, 0xAD, 0xBE, 0xEF}

	// docs/security.md §11.3: NonceHash is required and exactly 16 bytes.
	mockNonceHash := make([]byte, commissioning.NonceHashLen)
	for i := range mockNonceHash {
		mockNonceHash[i] = byte(i)
	}

	csr := &commissioning.CertRenewalCSR{
		MsgType:   commissioning.MsgCertRenewalCSR,
		CSR:       mockCSR,
		NonceHash: mockNonceHash,
	}

	// Encode
	data, err := cbor.Marshal(csr)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Verify raw CBOR structure
	var decoded map[int]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode as map: %v", err)
	}

	if decoded[1] != uint64(commissioning.MsgCertRenewalCSR) {
		t.Errorf("Expected msgType %d, got %v", commissioning.MsgCertRenewalCSR, decoded[1])
	}

	csrBytes, ok := decoded[2].([]byte)
	if !ok {
		t.Fatalf("Expected CSR as bytes, got %T", decoded[2])
	}
	if !bytes.Equal(csrBytes, mockCSR) {
		t.Error("CSR content mismatch")
	}

	// Verify round-trip
	var roundTrip commissioning.CertRenewalCSR
	if err := cbor.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Failed to decode to struct: %v", err)
	}

	if roundTrip.MsgType != csr.MsgType {
		t.Errorf("MsgType mismatch: expected %d, got %d", csr.MsgType, roundTrip.MsgType)
	}
	if !bytes.Equal(roundTrip.CSR, csr.CSR) {
		t.Error("CSR mismatch")
	}
}

// TestCertRenewalInstall_Encode verifies CertRenewalInstall encoding/decoding.
func TestCertRenewalInstall_Encode(t *testing.T) {
	// Mock DER-encoded certificate
	mockCert := []byte{0x30, 0x82, 0x02, 0x00, 0xCA, 0xFE, 0xBA, 0xBE}

	install := &commissioning.CertRenewalInstall{
		MsgType:  commissioning.MsgCertRenewalInstall,
		NewCert:  mockCert,
		Sequence: 2,
	}

	// Encode
	data, err := cbor.Marshal(install)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Verify raw CBOR structure
	var decoded map[int]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode as map: %v", err)
	}

	if decoded[1] != uint64(commissioning.MsgCertRenewalInstall) {
		t.Errorf("Expected msgType %d, got %v", commissioning.MsgCertRenewalInstall, decoded[1])
	}

	certBytes, ok := decoded[2].([]byte)
	if !ok {
		t.Fatalf("Expected cert as bytes, got %T", decoded[2])
	}
	if !bytes.Equal(certBytes, mockCert) {
		t.Error("Cert content mismatch")
	}

	if decoded[3] != uint64(2) {
		t.Errorf("Expected sequence 2, got %v", decoded[3])
	}

	// Verify round-trip
	var roundTrip commissioning.CertRenewalInstall
	if err := cbor.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Failed to decode to struct: %v", err)
	}

	if roundTrip.MsgType != install.MsgType {
		t.Errorf("MsgType mismatch: expected %d, got %d", install.MsgType, roundTrip.MsgType)
	}
	if !bytes.Equal(roundTrip.NewCert, install.NewCert) {
		t.Error("NewCert mismatch")
	}
	if roundTrip.Sequence != install.Sequence {
		t.Errorf("Sequence mismatch: expected %d, got %d", install.Sequence, roundTrip.Sequence)
	}
}

// TestCertRenewalAck_Encode verifies CertRenewalAck encoding/decoding.
func TestCertRenewalAck_Encode(t *testing.T) {
	ack := &commissioning.CertRenewalAck{
		MsgType:        commissioning.MsgCertRenewalAck,
		Status:         commissioning.RenewalStatusSuccess,
		ActiveSequence: 2,
	}

	// Encode
	data, err := cbor.Marshal(ack)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Verify raw CBOR structure
	var decoded map[int]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode as map: %v", err)
	}

	if decoded[1] != uint64(commissioning.MsgCertRenewalAck) {
		t.Errorf("Expected msgType %d, got %v", commissioning.MsgCertRenewalAck, decoded[1])
	}

	if decoded[2] != uint64(commissioning.RenewalStatusSuccess) {
		t.Errorf("Expected status %d, got %v", commissioning.RenewalStatusSuccess, decoded[2])
	}

	if decoded[3] != uint64(2) {
		t.Errorf("Expected activeSequence 2, got %v", decoded[3])
	}

	// Verify round-trip
	var roundTrip commissioning.CertRenewalAck
	if err := cbor.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Failed to decode to struct: %v", err)
	}

	if roundTrip.MsgType != ack.MsgType {
		t.Errorf("MsgType mismatch: expected %d, got %d", ack.MsgType, roundTrip.MsgType)
	}
	if roundTrip.Status != ack.Status {
		t.Errorf("Status mismatch: expected %d, got %d", ack.Status, roundTrip.Status)
	}
	if roundTrip.ActiveSequence != ack.ActiveSequence {
		t.Errorf("ActiveSequence mismatch: expected %d, got %d", ack.ActiveSequence, roundTrip.ActiveSequence)
	}
}

// TestCertRenewalAck_ErrorStatus tests error status codes.
func TestCertRenewalAck_ErrorStatus(t *testing.T) {
	testCases := []struct {
		name   string
		status uint8
	}{
		{"Success", commissioning.RenewalStatusSuccess},
		{"CSRFailed", commissioning.RenewalStatusCSRFailed},
		{"InstallFailed", commissioning.RenewalStatusInstallFailed},
		{"InvalidCert", commissioning.RenewalStatusInvalidCert},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ack := &commissioning.CertRenewalAck{
				MsgType:        commissioning.MsgCertRenewalAck,
				Status:         tc.status,
				ActiveSequence: 1,
			}

			data, err := cbor.Marshal(ack)
			if err != nil {
				t.Fatalf("Failed to encode: %v", err)
			}

			var roundTrip commissioning.CertRenewalAck
			if err := cbor.Unmarshal(data, &roundTrip); err != nil {
				t.Fatalf("Failed to decode: %v", err)
			}

			if roundTrip.Status != tc.status {
				t.Errorf("Expected status %d, got %d", tc.status, roundTrip.Status)
			}
		})
	}
}

// TestDecodeRenewalMessage verifies the decode function dispatches correctly.
func TestDecodeRenewalMessage(t *testing.T) {
	t.Run("CertRenewalRequest", func(t *testing.T) {
		req := &commissioning.CertRenewalRequest{
			MsgType: commissioning.MsgCertRenewalRequest,
			Nonce:   make([]byte, 32),
		}
		data, _ := cbor.Marshal(req)

		decoded, err := commissioning.DecodeRenewalMessage(data)
		if err != nil {
			t.Fatalf("Failed to decode: %v", err)
		}

		if _, ok := decoded.(*commissioning.CertRenewalRequest); !ok {
			t.Errorf("Expected *CertRenewalRequest, got %T", decoded)
		}
	})

	t.Run("CertRenewalCSR", func(t *testing.T) {
		nonce := make([]byte, 32)
		csr := &commissioning.CertRenewalCSR{
			MsgType:   commissioning.MsgCertRenewalCSR,
			CSR:       []byte{0x30, 0x82},
			NonceHash: commissioning.ComputeNonceHash(nonce),
		}
		data, _ := cbor.Marshal(csr)

		decoded, err := commissioning.DecodeRenewalMessage(data)
		if err != nil {
			t.Fatalf("Failed to decode: %v", err)
		}

		if _, ok := decoded.(*commissioning.CertRenewalCSR); !ok {
			t.Errorf("Expected *CertRenewalCSR, got %T", decoded)
		}
	})

	t.Run("CertRenewalCSR_RejectsMissingNonceHash", func(t *testing.T) {
		// Spec: docs/security.md §11.3 — NonceHash is required.
		raw := map[int]any{
			1: commissioning.MsgCertRenewalCSR,
			2: []byte{0x30, 0x82},
		}
		data, _ := cbor.Marshal(raw)

		if _, err := commissioning.DecodeRenewalMessage(data); err == nil {
			t.Error("Expected error decoding CertRenewalCSR without NonceHash")
		}
	})

	t.Run("CertRenewalCSR_RejectsWrongLengthNonceHash", func(t *testing.T) {
		raw := map[int]any{
			1: commissioning.MsgCertRenewalCSR,
			2: []byte{0x30, 0x82},
			3: make([]byte, 8), // wrong length, must be 16
		}
		data, _ := cbor.Marshal(raw)

		if _, err := commissioning.DecodeRenewalMessage(data); err == nil {
			t.Error("Expected error decoding CertRenewalCSR with 8-byte NonceHash")
		}
	})

	t.Run("CertRenewalInstall", func(t *testing.T) {
		install := &commissioning.CertRenewalInstall{
			MsgType:  commissioning.MsgCertRenewalInstall,
			NewCert:  []byte{0x30, 0x82},
			Sequence: 1,
		}
		data, _ := cbor.Marshal(install)

		decoded, err := commissioning.DecodeRenewalMessage(data)
		if err != nil {
			t.Fatalf("Failed to decode: %v", err)
		}

		if _, ok := decoded.(*commissioning.CertRenewalInstall); !ok {
			t.Errorf("Expected *CertRenewalInstall, got %T", decoded)
		}
	})

	t.Run("CertRenewalAck", func(t *testing.T) {
		ack := &commissioning.CertRenewalAck{
			MsgType:        commissioning.MsgCertRenewalAck,
			Status:         0,
			ActiveSequence: 1,
		}
		data, _ := cbor.Marshal(ack)

		decoded, err := commissioning.DecodeRenewalMessage(data)
		if err != nil {
			t.Fatalf("Failed to decode: %v", err)
		}

		if _, ok := decoded.(*commissioning.CertRenewalAck); !ok {
			t.Errorf("Expected *CertRenewalAck, got %T", decoded)
		}
	})

	t.Run("UnknownType", func(t *testing.T) {
		// Create message with unknown type
		data, _ := cbor.Marshal(map[int]any{1: 99})

		_, err := commissioning.DecodeRenewalMessage(data)
		if err == nil {
			t.Error("Expected error for unknown message type")
		}
	})
}

// TestComputeNonceHash verifies nonce hash computation (DEC-047).
func TestComputeNonceHash(t *testing.T) {
	// Test with a known nonce
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	hash := commissioning.ComputeNonceHash(nonce)

	// Verify hash length
	if len(hash) != commissioning.NonceHashLen {
		t.Errorf("Expected hash length %d, got %d", commissioning.NonceHashLen, len(hash))
	}

	// Verify consistency
	hash2 := commissioning.ComputeNonceHash(nonce)
	if !bytes.Equal(hash, hash2) {
		t.Error("Same nonce should produce same hash")
	}

	// Verify different nonces produce different hashes
	differentNonce := make([]byte, 32)
	copy(differentNonce, nonce)
	differentNonce[0] = 255

	differentHash := commissioning.ComputeNonceHash(differentNonce)
	if bytes.Equal(hash, differentHash) {
		t.Error("Different nonces should produce different hashes")
	}
}

// TestValidateNonceHash verifies nonce hash validation (DEC-047).
func TestValidateNonceHash(t *testing.T) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	hash := commissioning.ComputeNonceHash(nonce)

	t.Run("ValidHash", func(t *testing.T) {
		if !commissioning.ValidateNonceHash(nonce, hash) {
			t.Error("Valid hash should pass validation")
		}
	})

	t.Run("WrongNonce", func(t *testing.T) {
		differentNonce := make([]byte, 32)
		copy(differentNonce, nonce)
		differentNonce[0] = 255

		if commissioning.ValidateNonceHash(differentNonce, hash) {
			t.Error("Wrong nonce should fail validation")
		}
	})

	t.Run("WrongHash", func(t *testing.T) {
		tamperedHash := make([]byte, len(hash))
		copy(tamperedHash, hash)
		tamperedHash[0] ^= 0xFF

		if commissioning.ValidateNonceHash(nonce, tamperedHash) {
			t.Error("Tampered hash should fail validation")
		}
	})

	t.Run("WrongHashLength", func(t *testing.T) {
		shortHash := hash[:8]
		if commissioning.ValidateNonceHash(nonce, shortHash) {
			t.Error("Wrong hash length should fail validation")
		}
	})

	t.Run("EmptyHash", func(t *testing.T) {
		if commissioning.ValidateNonceHash(nonce, nil) {
			t.Error("Empty hash should fail validation")
		}
	})
}

// TestCertRenewalCSR_WithNonceHash verifies CSR encoding with nonce hash (DEC-047).
func TestCertRenewalCSR_WithNonceHash(t *testing.T) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	nonceHash := commissioning.ComputeNonceHash(nonce)

	csr := &commissioning.CertRenewalCSR{
		MsgType:   commissioning.MsgCertRenewalCSR,
		CSR:       []byte{0x30, 0x82, 0x01, 0x22},
		NonceHash: nonceHash,
	}

	// Encode
	data, err := cbor.Marshal(csr)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Verify raw CBOR has nonce hash field
	var decoded map[int]any
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode as map: %v", err)
	}

	hashBytes, ok := decoded[3].([]byte)
	if !ok {
		t.Fatalf("Expected nonce hash as bytes, got %T", decoded[3])
	}
	if len(hashBytes) != commissioning.NonceHashLen {
		t.Errorf("Expected %d-byte nonce hash, got %d bytes", commissioning.NonceHashLen, len(hashBytes))
	}

	// Verify round-trip
	var roundTrip commissioning.CertRenewalCSR
	if err := cbor.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Failed to decode to struct: %v", err)
	}

	if !bytes.Equal(roundTrip.NonceHash, nonceHash) {
		t.Error("Nonce hash mismatch after round-trip")
	}
}
