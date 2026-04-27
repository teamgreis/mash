// Command print_cert_install_vectors emits JSON-encoded mash-go cert
// install messages (DEC-085) for cross-implementation byte-level
// validation. mash-py's vector tests invoke this via `go run` and:
//
//  1. assert mash-py's decode(cbor_hex) yields the same fields
//  2. assert mash-py's encode(fields) reproduces the same cbor_hex
//
// Together those two assertions form a tight cross-impl byte pin: the
// commissioning cert install envelope (MsgTypes 10/11/12/13/20) is
// byte-identical between mash-go and mash-py for any logically-equal
// message. Encoder drift on either side breaks the test.
//
// The cert/CSR/nonce blobs in the fixtures are deterministic opaque
// bytes — the wire codec doesn't care whether they're real DER, only
// that the CBOR shape is preserved. Cert chain validity is exercised
// by the live interop test, not here.
//
// Run:
//
//	go run ./tools/print_cert_install_vectors
//
// Output: a single JSON object with one entry per MsgType, fields
// match what mash-py's test loader expects.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// Vector is one cert-install message in (fields, encoded bytes) form.
// Field shapes mirror mash-py's CSRRequest / CSRResponse / CertInstall /
// CertInstallResponse / CommissioningComplete dataclasses, so the
// Python test loader can validate decoded fields directly.
type Vector struct {
	Name        string         `json:"name"`
	MsgType     uint8          `json:"msg_type"`
	GoStruct    string         `json:"go_struct"`
	Fields      map[string]any `json:"fields"`
	CBORHex     string         `json:"cbor_hex"`
	Description string         `json:"description"`
}

type output struct {
	SpecRefs []string `json:"spec_refs"`
	Vectors  []Vector `json:"vectors"`
}

func main() {
	// Deterministic opaque blobs. 32 bytes for nonce per spec; arbitrary
	// fixed bytes for CSR/cert DER (the wire codec treats them as
	// byte strings).
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	csrDER := []byte("PKCS10-DER-fixture-bytes")
	opCertDER := []byte("OPERATIONAL-CERT-DER-fixture")
	caCertDER := []byte("ZONE-CA-DER-fixture")

	vectors := []Vector{
		{
			Name:     "csr_request_basic",
			MsgType:  commissioning.MsgCSRRequest,
			GoStruct: "commissioning.CSRRequest",
			Fields: map[string]any{
				"nonce_hex": hex.EncodeToString(nonce),
			},
			Description: "CSRRequest with the spec-required 32-byte nonce.",
			CBORHex: encodeOrFatal(&commissioning.CSRRequest{
				MsgType: commissioning.MsgCSRRequest,
				Nonce:   nonce,
			}),
		},
		{
			Name:     "csr_response_success",
			MsgType:  commissioning.MsgCSRResponse,
			GoStruct: "commissioning.CSRResponse",
			Fields: map[string]any{
				"csr_hex":    hex.EncodeToString(csrDER),
				"error_code": int(commissioning.ErrCodeSuccess),
			},
			Description: "CSRResponse on the success path: csr present, errorCode=0.",
			CBORHex: encodeOrFatal(&commissioning.CSRResponse{
				MsgType:   commissioning.MsgCSRResponse,
				CSR:       csrDER,
				ErrorCode: commissioning.ErrCodeSuccess,
			}),
		},
		{
			Name:     "csr_response_failure",
			MsgType:  commissioning.MsgCSRResponse,
			GoStruct: "commissioning.CSRResponse",
			Fields: map[string]any{
				// Per spec §4.3, csr MAY be empty on failure paths.
				"csr_hex":    "",
				"error_code": int(commissioning.ErrCodeCSRFailed),
			},
			Description: "CSRResponse on the failure path: csr empty, errorCode=ErrCodeCSRFailed.",
			CBORHex: encodeOrFatal(&commissioning.CSRResponse{
				MsgType:   commissioning.MsgCSRResponse,
				CSR:       []byte{},
				ErrorCode: commissioning.ErrCodeCSRFailed,
			}),
		},
		{
			Name:     "cert_install_local_zone",
			MsgType:  commissioning.MsgCertInstall,
			GoStruct: "commissioning.CertInstall",
			Fields: map[string]any{
				"operational_cert_hex": hex.EncodeToString(opCertDER),
				"ca_cert_hex":          hex.EncodeToString(caCertDER),
				"zone_type":            2, // LOCAL per DEC-060
				"zone_priority":        1,
			},
			Description: "CertInstall delivering operational cert + Zone CA for a LOCAL zone.",
			CBORHex: encodeOrFatal(&commissioning.CertInstall{
				MsgType:         commissioning.MsgCertInstall,
				OperationalCert: opCertDER,
				CACert:          caCertDER,
				ZoneType:        2,
				ZonePriority:    1,
			}),
		},
		{
			Name:     "cert_install_grid_zone",
			MsgType:  commissioning.MsgCertInstall,
			GoStruct: "commissioning.CertInstall",
			Fields: map[string]any{
				"operational_cert_hex": hex.EncodeToString(opCertDER),
				"ca_cert_hex":          hex.EncodeToString(caCertDER),
				"zone_type":            1, // GRID per DEC-060
				"zone_priority":        2,
			},
			Description: "CertInstall for a GRID zone with priority 2.",
			CBORHex: encodeOrFatal(&commissioning.CertInstall{
				MsgType:         commissioning.MsgCertInstall,
				OperationalCert: opCertDER,
				CACert:          caCertDER,
				ZoneType:        1,
				ZonePriority:    2,
			}),
		},
		{
			Name:     "cert_install_response_success",
			MsgType:  commissioning.MsgCertInstallResponse,
			GoStruct: "commissioning.CertInstallResponse",
			Fields: map[string]any{
				"error_code": int(commissioning.ErrCodeSuccess),
			},
			Description: "CertInstallResponse acking success (errorCode=0).",
			CBORHex: encodeOrFatal(&commissioning.CertInstallResponse{
				MsgType:   commissioning.MsgCertInstallResponse,
				ErrorCode: commissioning.ErrCodeSuccess,
			}),
		},
		{
			Name:     "cert_install_response_zone_type_exists",
			MsgType:  commissioning.MsgCertInstallResponse,
			GoStruct: "commissioning.CertInstallResponse",
			Fields: map[string]any{
				"error_code": int(commissioning.ErrCodeZoneTypeExists),
			},
			Description: "CertInstallResponse rejecting a duplicate zone type per DEC-043.",
			CBORHex: encodeOrFatal(&commissioning.CertInstallResponse{
				MsgType:   commissioning.MsgCertInstallResponse,
				ErrorCode: commissioning.ErrCodeZoneTypeExists,
			}),
		},
		{
			Name:        "commissioning_complete",
			MsgType:     commissioning.MsgCommissioningComplete,
			GoStruct:    "commissioning.CommissioningComplete",
			Fields:      map[string]any{},
			Description: "CommissioningComplete terminator — empty payload.",
			CBORHex: encodeOrFatal(&commissioning.CommissioningComplete{
				MsgType: commissioning.MsgCommissioningComplete,
			}),
		},
	}

	out := output{
		SpecRefs: []string{
			"docs/decision-log.md DEC-085",
			"docs/message-types.md §3",
			"docs/testing/behavior/commissioning-pase.md §4.3",
		},
		Vectors: vectors,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: encoding output: %v\n", err)
		os.Exit(1)
	}
}

func encodeOrFatal(msg any) string {
	data, err := commissioning.EncodePASEMessage(msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: EncodePASEMessage(%T): %v\n", msg, err)
		os.Exit(1)
	}
	return hex.EncodeToString(data)
}
