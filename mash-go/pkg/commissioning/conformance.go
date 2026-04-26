package commissioning

// DEC-084 primitive boundary. This file declares the algorithmic surface that
// the Phase 7B conformance vectors exercise. Every function below corresponds
// directly to one row of the Phase 7B vector table in mash-py-tdd-plan.md and
// to one numbered step in DEC-084 / commissioning-pase.md §2.
//
// In Phase 7B these are stubs returning ErrPrimitiveNotImplemented. The
// vector runner picks that up and reports the vector as FAIL with a stable
// reason -- the expected "initial failure against unmodified mash-go" per
// the Phase 7B acceptance text.
//
// In Phase 7C the stubs are replaced with real implementations; the same
// runner then passes every vector. The primitive signatures below are the
// stable interface 7C must honour. Changing them after 7C ships requires
// regenerating the JSON vectors from the oracle.
//
// All byte slices use spec-canonical encodings:
//   * Scalars (w0, w1, x, y) are 32-byte big-endian integers in [0, n).
//   * Curve points are SEC1 uncompressed, 65 bytes (0x04 || X(32) || Y(32)).
//   * MHF output is 80 bytes (DEC-084 §2.3 dkLen).
//   * Salt is 16 bytes (DEC-084 §2.3).
//   * Identity strings are passed verbatim (UTF-8, no length prefix).

import "errors"

// ErrPrimitiveNotImplemented marks DEC-084 primitives that have not yet been
// implemented. Phase 7C replaces every stub below with a real implementation.
var ErrPrimitiveNotImplemented = errors.New("DEC-084 primitive not implemented (Phase 7C pending)")

// DeriveSalt computes the per-device PBKDF2 salt from the QR-code contents:
//   salt = SHA-256("MASH PBKDF2 salt v1" || discriminator(BE16) || setup_code_utf8)[0:16].
func DeriveSalt(discriminator uint16, setupCode []byte) ([]byte, error) {
	_ = discriminator
	_ = setupCode
	return nil, ErrPrimitiveNotImplemented
}

// MHF runs PBKDF2-HMAC-SHA256 with the DEC-084 protocol-constant iteration
// count and produces dkLen bytes of output.
func MHF(setupCode, salt []byte, iterations, dkLen int) ([]byte, error) {
	_ = setupCode
	_ = salt
	_ = iterations
	_ = dkLen
	return nil, ErrPrimitiveNotImplemented
}

// DeriveW0W1 splits an 80-byte MHF output into two 40-byte halves and reduces
// each modulo the P-256 curve order n. Returns (w0, w1) as 32-byte big-endian
// scalars.
func DeriveW0W1(mhfOut []byte) (w0, w1 []byte, err error) {
	_ = mhfOut
	return nil, nil, ErrPrimitiveNotImplemented
}

// ComputeL multiplies w1 by the P-256 base point G and returns the resulting
// verifier point in SEC1 uncompressed encoding (65 bytes).
func ComputeL(w1 []byte) ([]byte, error) {
	_ = w1
	return nil, ErrPrimitiveNotImplemented
}

// ClientPublic computes pA = x*G + w0*M and returns the SEC1 uncompressed
// encoding (65 bytes).
func ClientPublic(w0, x []byte) ([]byte, error) {
	_ = w0
	_ = x
	return nil, ErrPrimitiveNotImplemented
}

// ServerPublic computes pB = y*G + w0*N and returns the SEC1 uncompressed
// encoding (65 bytes).
func ServerPublic(w0, y []byte) ([]byte, error) {
	_ = w0
	_ = y
	return nil, ErrPrimitiveNotImplemented
}

// ComposeTranscript builds the RFC 9383 §3.3 length-prefixed transcript TT
// from the nine input fields. Lengths are 8-byte little-endian unsigned
// integers; the M/N point encodings, pA/pB, Z, V are SEC1 uncompressed
// 65-byte; w0 is 32-byte big-endian.
func ComposeTranscript(controllerIdentity, deviceIdentity, M, N, pA, pB, Z, V, w0 []byte) ([]byte, error) {
	_ = controllerIdentity
	_ = deviceIdentity
	_ = M
	_ = N
	_ = pA
	_ = pB
	_ = Z
	_ = V
	_ = w0
	return nil, ErrPrimitiveNotImplemented
}

// KShared returns SHA-256(TT).
func KShared(TT []byte) ([]byte, error) {
	_ = TT
	return nil, ErrPrimitiveNotImplemented
}

// KeyChain derives K_main, K_confirm, K_encrypt from K_shared with the
// DEC-084 §2.4 HKDF info strings. K_encrypt is derived from K_main, not from
// K_shared, per the spec.
func KeyChain(KShared []byte) (KMain, KConfirm, KEncrypt []byte, err error) {
	_ = KShared
	return nil, nil, nil, ErrPrimitiveNotImplemented
}

// ConfirmationKeys derives KcA||KcB from K_confirm via
// HKDF(K_confirm, "ConfirmationKeys", 32). Returns the first 16 bytes as KcA
// and the next 16 as KcB.
func ConfirmationKeys(KConfirm []byte) (KcA, KcB []byte, err error) {
	_ = KConfirm
	return nil, nil, ErrPrimitiveNotImplemented
}

// ConfirmMACs computes confirmA = HMAC-SHA256(KcA, pB) and
// confirmB = HMAC-SHA256(KcB, pA).
func ConfirmMACs(KcA, KcB, pA, pB []byte) (confirmA, confirmB []byte, err error) {
	_ = KcA
	_ = KcB
	_ = pA
	_ = pB
	return nil, nil, ErrPrimitiveNotImplemented
}

// ValidatePoint enforces DEC-084 §2.5 point validation: 65-byte SEC1
// uncompressed, on-curve, and not the identity element.
func ValidatePoint(point []byte) error {
	_ = point
	return ErrPrimitiveNotImplemented
}
