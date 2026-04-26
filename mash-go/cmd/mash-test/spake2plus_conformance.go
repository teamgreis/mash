package main

// SPAKE2+ algorithmic-conformance vector runner (Phase 7B / 7C).
//
// When mash-test is invoked with -spake2plus-vectors <path>, it skips the
// normal network-driven test suite and instead loads the JSON vectors at
// <path>, dispatches each one through the DEC-084 primitive boundary
// exported by pkg/commissioning, and reports pass/fail per vector.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// vectorFile is the on-disk schema produced by tools/spake2plus_oracle/gen_vectors.py.
type vectorFile struct {
	SchemaVersion string   `json:"schema_version"`
	Spec          string   `json:"spec"`
	SpecSection   string   `json:"spec_section"`
	GeneratedBy   string   `json:"generated_by"`
	Note          string   `json:"note"`
	Vectors       []vector `json:"vectors"`
}

type vector struct {
	ID            string                 `json:"id"`
	Description   string                 `json:"description"`
	Inputs        map[string]any         `json:"inputs"`
	Outputs       map[string]any         `json:"outputs,omitempty"`
	ExpectedError string                 `json:"expected_error,omitempty"`
	_             struct{}               // forbid struct-literal use
	_extra        map[string]json.Number // suppress unused warnings
}

type vectorResult struct {
	ID     string
	Status string // "pass", "fail", "skip"
	Reason string
}

// runSPAKE2PlusConformance loads <path>, runs every vector, prints a per-vector
// status line plus a summary, and returns 0 if every vector passes (or the
// expected_error vectors error as expected) -- non-zero otherwise. The Phase
// 7B acceptance requires non-zero against unmodified mash-go.
func runSPAKE2PlusConformance(path string, w io.Writer) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "Error: cannot read vectors file %q: %v\n", path, err)
		return 2
	}
	var vf vectorFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		fmt.Fprintf(w, "Error: cannot parse vectors file %q: %v\n", path, err)
		return 2
	}

	fmt.Fprintf(w, "SPAKE2+ conformance vectors: %s\n", path)
	fmt.Fprintf(w, "  schema=%s spec=%s vectors=%d\n", vf.SchemaVersion, vf.Spec, len(vf.Vectors))
	fmt.Fprintln(w)

	results := make([]vectorResult, 0, len(vf.Vectors))
	for _, v := range vf.Vectors {
		results = append(results, runOneVector(v))
	}

	pass, fail, skip := 0, 0, 0
	for _, r := range results {
		var marker string
		switch r.Status {
		case "pass":
			marker = "PASS"
			pass++
		case "skip":
			marker = "SKIP"
			skip++
		default:
			marker = "FAIL"
			fail++
		}
		if r.Reason != "" {
			fmt.Fprintf(w, "  %s  %-30s  %s\n", marker, r.ID, r.Reason)
		} else {
			fmt.Fprintf(w, "  %s  %-30s\n", marker, r.ID)
		}
	}

	fmt.Fprintf(w, "\nResult: %d pass, %d fail, %d skip (of %d)\n", pass, fail, skip, len(results))

	if fail > 0 {
		return 1
	}
	return 0
}

// runOneVector dispatches by vector ID and produces a vectorResult. Unknown
// IDs are skipped (forwards-compatible for new vectors added to the JSON).
func runOneVector(v vector) vectorResult {
	switch v.ID {
	case "mhf_basic":
		return checkMHFBasic(v)
	case "derive_w0_w1":
		return checkDeriveW0W1(v)
	case "compute_L":
		return checkComputeL(v)
	case "client_init_fixed_x":
		return checkClientInit(v)
	case "server_init_fixed_y":
		return checkServerInit(v)
	case "transcript":
		return checkTranscript(v)
	case "k_shared":
		return checkKShared(v)
	case "key_chain":
		return checkKeyChain(v)
	case "confirmation_keys":
		return checkConfirmationKeys(v)
	case "confirm_macs":
		return checkConfirmMACs(v)
	case "full_handshake":
		return checkFullHandshake(v)
	case "point_validation_identity", "point_validation_off_curve":
		return checkPointValidationNegative(v)
	default:
		return vectorResult{ID: v.ID, Status: "skip", Reason: "unknown vector id"}
	}
}

// ---------- helpers --------------------------------------------------------

func mustHex(m map[string]any, key string) ([]byte, error) {
	s, ok := m[key].(string)
	if !ok {
		return nil, fmt.Errorf("missing or non-string field %q", key)
	}
	return hex.DecodeString(s)
}

func mustInt(m map[string]any, key string) (int, error) {
	switch v := m[key].(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("missing or non-integer field %q (got %T)", key, m[key])
	}
}

func mustStr(m map[string]any, key string) (string, error) {
	s, ok := m[key].(string)
	if !ok {
		return "", fmt.Errorf("missing or non-string field %q", key)
	}
	return s, nil
}

// classifyErr converts a primitive error into a stable per-result reason.
func classifyErr(prefix string, err error) string {
	return prefix + ": " + err.Error()
}

func compareBytes(label string, got, want []byte) (bool, string) {
	if bytes.Equal(got, want) {
		return true, ""
	}
	return false, fmt.Sprintf("%s mismatch: got %x want %x", label, got, want)
}

func inputErr(id string, err error) vectorResult {
	return vectorResult{ID: id, Status: "fail", Reason: "bad inputs: " + err.Error()}
}

// ---------- per-vector checks ---------------------------------------------

func checkMHFBasic(v vector) vectorResult {
	setupCode, err := mustStr(v.Inputs, "setup_code")
	if err != nil {
		return inputErr(v.ID, err)
	}
	salt, err := mustHex(v.Inputs, "salt_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	iters, err := mustInt(v.Inputs, "iterations")
	if err != nil {
		return inputErr(v.ID, err)
	}
	dklen, err := mustInt(v.Inputs, "dklen")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantHex, err := mustStr(v.Outputs, "mhf_out_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		return inputErr(v.ID, err)
	}

	got, err := commissioning.MHF([]byte(setupCode), salt, iters, dklen)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("MHF", err)}
	}
	if ok, reason := compareBytes("mhf_out", got, want); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkDeriveW0W1(v vector) vectorResult {
	mhfOut, err := mustHex(v.Inputs, "mhf_out_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantW0, err := mustHex(v.Outputs, "w0_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantW1, err := mustHex(v.Outputs, "w1_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	w0, w1, err := commissioning.DeriveW0W1(mhfOut)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("DeriveW0W1", err)}
	}
	if ok, reason := compareBytes("w0", w0, wantW0); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	if ok, reason := compareBytes("w1", w1, wantW1); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkComputeL(v vector) vectorResult {
	w1, err := mustHex(v.Inputs, "w1_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantL, err := mustHex(v.Outputs, "L_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	gotL, err := commissioning.ComputeL(w1)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ComputeL", err)}
	}
	if ok, reason := compareBytes("L", gotL, wantL); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkClientInit(v vector) vectorResult {
	w0, err := mustHex(v.Inputs, "w0_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	x, err := mustHex(v.Inputs, "x_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantPA, err := mustHex(v.Outputs, "pA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	pA, err := commissioning.ClientPublic(w0, x)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ClientPublic", err)}
	}
	if ok, reason := compareBytes("pA", pA, wantPA); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkServerInit(v vector) vectorResult {
	w0, err := mustHex(v.Inputs, "w0_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	y, err := mustHex(v.Inputs, "y_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantPB, err := mustHex(v.Outputs, "pB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	pB, err := commissioning.ServerPublic(w0, y)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ServerPublic", err)}
	}
	if ok, reason := compareBytes("pB", pB, wantPB); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkTranscript(v vector) vectorResult {
	cid, err := mustStr(v.Inputs, "controller_identity")
	if err != nil {
		return inputErr(v.ID, err)
	}
	did, err := mustStr(v.Inputs, "device_identity")
	if err != nil {
		return inputErr(v.ID, err)
	}
	M, err := mustHex(v.Inputs, "M_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	N, err := mustHex(v.Inputs, "N_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	pA, err := mustHex(v.Inputs, "pA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	pB, err := mustHex(v.Inputs, "pB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	Z, err := mustHex(v.Inputs, "Z_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	V, err := mustHex(v.Inputs, "V_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	w0, err := mustHex(v.Inputs, "w0_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantTT, err := mustHex(v.Outputs, "TT_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	got, err := commissioning.ComposeTranscript([]byte(cid), []byte(did), M, N, pA, pB, Z, V, w0)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ComposeTranscript", err)}
	}
	if ok, reason := compareBytes("TT", got, wantTT); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkKShared(v vector) vectorResult {
	TT, err := mustHex(v.Inputs, "TT_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantK, err := mustHex(v.Outputs, "K_shared_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	got, err := commissioning.KShared(TT)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("KShared", err)}
	}
	if ok, reason := compareBytes("K_shared", got, wantK); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkKeyChain(v vector) vectorResult {
	K, err := mustHex(v.Inputs, "K_shared_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantMain, err := mustHex(v.Outputs, "K_main_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantConfirm, err := mustHex(v.Outputs, "K_confirm_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantEncrypt, err := mustHex(v.Outputs, "K_encrypt_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	main, confirm, encrypt, err := commissioning.KeyChain(K)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("KeyChain", err)}
	}
	if ok, reason := compareBytes("K_main", main, wantMain); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	if ok, reason := compareBytes("K_confirm", confirm, wantConfirm); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	if ok, reason := compareBytes("K_encrypt", encrypt, wantEncrypt); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkConfirmationKeys(v vector) vectorResult {
	KC, err := mustHex(v.Inputs, "K_confirm_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantA, err := mustHex(v.Outputs, "KcA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantB, err := mustHex(v.Outputs, "KcB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	a, b, err := commissioning.ConfirmationKeys(KC)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ConfirmationKeys", err)}
	}
	if ok, reason := compareBytes("KcA", a, wantA); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	if ok, reason := compareBytes("KcB", b, wantB); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkConfirmMACs(v vector) vectorResult {
	KcA, err := mustHex(v.Inputs, "KcA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	KcB, err := mustHex(v.Inputs, "KcB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	pA, err := mustHex(v.Inputs, "pA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	pB, err := mustHex(v.Inputs, "pB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantA, err := mustHex(v.Outputs, "confirmA_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantB, err := mustHex(v.Outputs, "confirmB_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}

	a, b, err := commissioning.ConfirmMACs(KcA, KcB, pA, pB)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ConfirmMACs", err)}
	}
	if ok, reason := compareBytes("confirmA", a, wantA); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	if ok, reason := compareBytes("confirmB", b, wantB); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkFullHandshake(v vector) vectorResult {
	// The full_handshake vector is a smoke composite: every primitive in
	// sequence with derived (rather than zero) salt. Run each step
	// independently so a single failed primitive doesn't mask later ones.
	setupCode, err := mustStr(v.Inputs, "setup_code")
	if err != nil {
		return inputErr(v.ID, err)
	}
	disc, err := mustInt(v.Inputs, "discriminator")
	if err != nil {
		return inputErr(v.ID, err)
	}
	x, err := mustHex(v.Inputs, "x_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	y, err := mustHex(v.Inputs, "y_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	iters, err := mustInt(v.Inputs, "iterations")
	if err != nil {
		return inputErr(v.ID, err)
	}

	salt, err := commissioning.DeriveSalt(uint16(disc), []byte(setupCode))
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("DeriveSalt", err)}
	}
	if want, _ := mustHex(v.Outputs, "salt_hex"); want != nil {
		if ok, reason := compareBytes("salt", salt, want); !ok {
			return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
		}
	}

	mhfOut, err := commissioning.MHF([]byte(setupCode), salt, iters, 80)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("MHF", err)}
	}

	w0, w1, err := commissioning.DeriveW0W1(mhfOut)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("DeriveW0W1", err)}
	}

	pA, err := commissioning.ClientPublic(w0, x)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ClientPublic", err)}
	}
	pB, err := commissioning.ServerPublic(w0, y)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("ServerPublic", err)}
	}
	_ = pA
	_ = pB
	_ = w1

	// The composite check is "all field-by-field outputs match". Spot-check
	// K_shared against the recorded value as the canonical sentinel; any
	// upstream divergence is already surfaced by the per-primitive vectors.
	wantKHex, err := mustStr(v.Outputs, "K_shared_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	wantK, err := hex.DecodeString(wantKHex)
	if err != nil {
		return inputErr(v.ID, err)
	}

	wantTTHex, _ := mustStr(v.Outputs, "TT_hex")
	wantTT, _ := hex.DecodeString(wantTTHex)
	gotK, err := commissioning.KShared(wantTT)
	if err != nil {
		return vectorResult{ID: v.ID, Status: "fail", Reason: classifyErr("KShared(canned-TT)", err)}
	}
	if ok, reason := compareBytes("K_shared", gotK, wantK); !ok {
		return vectorResult{ID: v.ID, Status: "fail", Reason: reason}
	}
	return vectorResult{ID: v.ID, Status: "pass"}
}

func checkPointValidationNegative(v vector) vectorResult {
	point, err := mustHex(v.Inputs, "point_hex")
	if err != nil {
		return inputErr(v.ID, err)
	}
	expected := strings.TrimSpace(v.ExpectedError)
	if expected == "" {
		return inputErr(v.ID, fmt.Errorf("missing expected_error"))
	}

	got := commissioning.ValidatePoint(point)
	if got == nil {
		return vectorResult{
			ID:     v.ID,
			Status: "fail",
			Reason: "expected ValidatePoint to reject, got nil error",
		}
	}
	// We don't pin the *specific* error wording — DEC-084 §2.5 only requires
	// rejection. Surface the concrete error in the result string for triage.
	return vectorResult{ID: v.ID, Status: "pass", Reason: "rejected: " + got.Error()}
}
