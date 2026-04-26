package commissioning

// SPAKE2+ implementation for MASH, per DEC-084 (codified in
// docs/testing/behavior/commissioning-pase.md §2). Replaces the pre-DEC-084
// implementation that lacked an MHF, used an unprefixed transcript, missed
// identity-element rejection, and reused a single confirmation MAC key.
//
// Algorithm summary (full normative spec in DEC-084):
//   * Curve P-256, SHA-256, HKDF-SHA256, HMAC-SHA256.
//   * MHF: PBKDF2-HMAC-SHA256, 100k iterations, 80-byte output.
//   * Salt derived: SHA-256("MASH PBKDF2 salt v1" || disc(BE16) || code)[0:16].
//   * w0 = mhf_out[0:40] mod n; w1 = mhf_out[40:80] mod n.
//   * L = w1 * G; verifier = (discriminator, salt, w0, L).
//   * pA = x*G + w0*M; pB = y*G + w0*N (RFC 9383 §4 fixed P-256 M, N).
//   * Z, V, K_shared = SHA-256(TT) where TT is the RFC 9383 §3.3
//     length-prefixed transcript (8-byte LE length + field).
//   * K_main, K_confirm, K_encrypt = HKDF chain with MASH info strings.
//   * KcA||KcB = HKDF(K_confirm, "ConfirmationKeys", 32).
//   * confirmA = HMAC(KcA, pB); confirmB = HMAC(KcB, pA).

import (
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

// SPAKE2+ protocol constants.
const (
	// SharedSecretSize is the size in bytes of K_main (and of every key in the
	// HKDF chain). Exposed for callers that allocate based on the key size.
	SharedSecretSize = 32

	// ConfirmationSize is the size of an HMAC-SHA256 confirmation MAC.
	ConfirmationSize = 32

	// PointSize is the byte length of an SEC1 uncompressed P-256 point
	// (`0x04 || X(32) || Y(32)`).
	PointSize = 65

	// SaltSize is the byte length of the derived PBKDF2 salt (DEC-084 §2.3).
	SaltSize = 16

	// MHFOutputSize is the dkLen of PBKDF2 (DEC-084 §2.3).
	MHFOutputSize = 80

	// MHFIterations is the protocol-constant PBKDF2 iteration count
	// (DEC-084 §2.1).
	MHFIterations = 100_000

	// ScalarSize is the byte length of a 32-byte big-endian P-256 scalar.
	ScalarSize = 32

	// confirmHalfSize is the byte length of one confirmation MAC key
	// (KcA / KcB). RFC 9383 §3.4 splits 32 bytes into two 16-byte halves.
	confirmHalfSize = 16
)

// Protocol-constant identity strings (DEC-084 §2.4).
//
// MASH binds the SPAKE2+ transcript to fixed strings — not session-derived
// nonces and not TLS-derived material. Session entropy comes from the random
// ephemeral scalars x and y instead.
var (
	ControllerIdentity = []byte("mash-controller")
	DeviceIdentity     = []byte("mash-device")
)

// HKDF info strings (DEC-084 §2.4).
var (
	infoKMain    = []byte("MASH PASE Session Key")
	infoKConfirm = []byte("MASH PASE Confirmation")
	infoKEncrypt = []byte("MASH PASE Encryption")
	infoKcAKcB   = []byte("ConfirmationKeys")
)

// saltPrefix is the domain-separation tag for PBKDF2 salt derivation
// (DEC-084 §2.3).
var saltPrefix = []byte("MASH PBKDF2 salt v1")

// SPAKE2+ errors.
var (
	ErrInvalidPublicKey   = errors.New("invalid public key")
	ErrConfirmationFailed = errors.New("confirmation failed")
	ErrInvalidVerifier    = errors.New("invalid verifier")
	ErrInvalidScalar      = errors.New("invalid scalar")
	ErrInvalidPoint       = errors.New("invalid point")
	ErrIdentityElement    = errors.New("identity element rejected")
)

// curve is the P-256 curve.
var curve = elliptic.P256()

// RFC 9383 §4 fixed P-256 SPAKE2+ generator points (DEC-084 §2.2).
var (
	mX = mustHexBigInt("886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f")
	mY = mustHexBigInt("5ff355163e43ce224e0b0e65ff02ac8e5c7be09419c785e0ca547d55a12e2d20")
	nX = mustHexBigInt("d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49")
	nY = mustHexBigInt("07d60aa6bfade45008a636337f5168c64d9bd36034808cd564490b1e656edbe7")
)

// mustHexBigInt parses a hex string to *big.Int or panics. Used only for
// embedded protocol constants.
func mustHexBigInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		panic("invalid hex string: " + s)
	}
	return n
}

// Verifier is the device-side stored material derived from the setup code at
// factory provisioning. Per DEC-084 §2.3 the device persists
// `(discriminator, salt, w0, L)` and discards the setup code.
type Verifier struct {
	// Discriminator is the device's 12-bit discriminator (0–4095). Used to
	// derive the salt; included so a stored verifier carries enough context
	// to be used standalone.
	Discriminator uint16 `cbor:"1,keyasint"`

	// Salt is the 16-byte PBKDF2 salt derived from
	// SHA-256("MASH PBKDF2 salt v1" || disc(BE16) || setup_code_utf8)[0:16].
	Salt []byte `cbor:"2,keyasint"`

	// W0 is the first verification scalar (32-byte big-endian).
	W0 []byte `cbor:"3,keyasint"`

	// L is the verifier point `w1 * G`, SEC1 uncompressed (65 bytes).
	L []byte `cbor:"4,keyasint"`

	// Iterations records the PBKDF2 iteration count used. Persisted for
	// forward compatibility — currently always equals MHFIterations.
	Iterations uint32 `cbor:"5,keyasint"`
}

// GenerateVerifier creates a verifier from a setup code and discriminator.
// Identities are protocol constants (mash-controller / mash-device) per
// DEC-084 §2.4 and need not be passed.
func GenerateVerifier(setupCode SetupCode, discriminator uint16) (*Verifier, error) {
	salt, err := DeriveSalt(discriminator, []byte(setupCode.String()))
	if err != nil {
		return nil, err
	}
	mhfOut, err := MHF([]byte(setupCode.String()), salt, MHFIterations, MHFOutputSize)
	if err != nil {
		return nil, err
	}
	w0Bytes, w1Bytes, err := DeriveW0W1(mhfOut)
	if err != nil {
		return nil, err
	}
	L, err := ComputeL(w1Bytes)
	if err != nil {
		return nil, err
	}
	return &Verifier{
		Discriminator: discriminator,
		Salt:          salt,
		W0:            w0Bytes,
		L:             L,
		Iterations:    MHFIterations,
	}, nil
}

// SPAKE2PlusClient is the controller side of the exchange.
type SPAKE2PlusClient struct {
	setupCode     SetupCode
	discriminator uint16

	// Ephemeral private key.
	x *big.Int

	// Derived from setup code.
	w0 *big.Int
	w1 *big.Int

	// Public values (SEC1 uncompressed).
	pA []byte
	pB []byte

	// Session keys (DEC-084 §2.4).
	kMain    []byte
	kConfirm []byte
	kEncrypt []byte
	kcA      []byte
	kcB      []byte
}

// NewSPAKE2PlusClient creates a client and derives w0, w1 from the setup code
// and discriminator. The ephemeral scalar x is generated with crypto/rand.
func NewSPAKE2PlusClient(setupCode SetupCode, discriminator uint16) (*SPAKE2PlusClient, error) {
	x, err := randomEphemeral()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key: %w", err)
	}
	w0Bytes, w1Bytes, err := deriveW0W1FromCode(setupCode, discriminator)
	if err != nil {
		return nil, err
	}
	w0 := new(big.Int).SetBytes(w0Bytes)
	w1 := new(big.Int).SetBytes(w1Bytes)
	return &SPAKE2PlusClient{
		setupCode:     setupCode,
		discriminator: discriminator,
		x:             x,
		w0:            w0,
		w1:            w1,
	}, nil
}

// PublicValue returns pA = x*G + w0*M (SEC1 uncompressed, 65 bytes).
func (c *SPAKE2PlusClient) PublicValue() []byte {
	if c.pA != nil {
		return c.pA
	}
	xx, xy := curve.ScalarBaseMult(c.x.Bytes())
	w0mx, w0my := curve.ScalarMult(mX, mY, c.w0.Bytes())
	pAx, pAy := curve.Add(xx, xy, w0mx, w0my)
	c.pA = elliptic.Marshal(curve, pAx, pAy)
	return c.pA
}

// ProcessServerValue validates pB and derives the session-key chain.
func (c *SPAKE2PlusClient) ProcessServerValue(pB []byte) error {
	if err := ValidatePoint(pB); err != nil {
		return ErrInvalidPublicKey
	}
	pBx, pBy := elliptic.Unmarshal(curve, pB)
	if pBx == nil {
		return ErrInvalidPublicKey
	}
	c.pB = pB
	_ = c.PublicValue()

	// Y_unmasked = pB - w0*N
	w0nx, w0ny := curve.ScalarMult(nX, nY, c.w0.Bytes())
	w0nyNeg := new(big.Int).Neg(w0ny)
	w0nyNeg.Mod(w0nyNeg, curve.Params().P)
	yx, yy := curve.Add(pBx, pBy, w0nx, w0nyNeg)

	// Z = x * Y_unmasked, V = w1 * Y_unmasked.
	zx, zy := curve.ScalarMult(yx, yy, c.x.Bytes())
	vx, vy := curve.ScalarMult(yx, yy, c.w1.Bytes())

	c.deriveKeys(zx, zy, vx, vy)
	return nil
}

func (c *SPAKE2PlusClient) deriveKeys(zx, zy, vx, vy *big.Int) {
	w0Bytes := scalarToBytes(c.w0)
	TT := composeTranscriptInternal(
		ControllerIdentity, DeviceIdentity,
		mPointBytes(), nPointBytes(),
		c.pA, c.pB,
		marshalAffine(zx, zy), marshalAffine(vx, vy),
		w0Bytes,
	)
	K := sha256Sum(TT)
	c.kMain, c.kConfirm, c.kEncrypt = computeKeyChain(K)
	c.kcA, c.kcB = splitConfirmationKeys(c.kConfirm)
}

// Confirmation returns confirmA = HMAC-SHA256(KcA, pB).
func (c *SPAKE2PlusClient) Confirmation() []byte {
	mac := hmac.New(sha256.New, c.kcA)
	mac.Write(c.pB)
	return mac.Sum(nil)
}

// VerifyServerConfirmation verifies confirmB = HMAC-SHA256(KcB, pA) in
// constant time.
func (c *SPAKE2PlusClient) VerifyServerConfirmation(serverConfirm []byte) error {
	mac := hmac.New(sha256.New, c.kcB)
	mac.Write(c.pA)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(serverConfirm, expected) != 1 {
		return ErrConfirmationFailed
	}
	return nil
}

// SharedSecret returns K_main. The name is preserved for API stability;
// per DEC-084 the value is K_main from the three-key chain (HKDF of K_shared
// with info "MASH PASE Session Key").
func (c *SPAKE2PlusClient) SharedSecret() []byte {
	return c.kMain
}

// EncryptionKey returns K_encrypt (HKDF of K_main with info "MASH PASE
// Encryption"). Available after ProcessServerValue.
func (c *SPAKE2PlusClient) EncryptionKey() []byte {
	return c.kEncrypt
}

// SPAKE2PlusServer is the device side of the exchange.
type SPAKE2PlusServer struct {
	verifier *Verifier

	// Ephemeral private key.
	y *big.Int

	// Public values.
	pA []byte
	pB []byte

	// Derived from verifier.
	w0     *big.Int
	lx, ly *big.Int

	// Session keys.
	kMain    []byte
	kConfirm []byte
	kEncrypt []byte
	kcA      []byte
	kcB      []byte
}

// NewSPAKE2PlusServer creates a server from a stored verifier.
func NewSPAKE2PlusServer(verifier *Verifier) (*SPAKE2PlusServer, error) {
	if verifier == nil {
		return nil, ErrInvalidVerifier
	}
	if len(verifier.W0) != ScalarSize {
		return nil, fmt.Errorf("%w: w0 must be %d bytes, got %d", ErrInvalidVerifier, ScalarSize, len(verifier.W0))
	}
	if err := ValidatePoint(verifier.L); err != nil {
		return nil, fmt.Errorf("%w: invalid L point: %v", ErrInvalidVerifier, err)
	}
	y, err := randomEphemeral()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ephemeral key: %w", err)
	}
	w0 := new(big.Int).SetBytes(verifier.W0)
	lx, ly := elliptic.Unmarshal(curve, verifier.L)
	if lx == nil {
		return nil, fmt.Errorf("%w: cannot unmarshal L", ErrInvalidVerifier)
	}
	return &SPAKE2PlusServer{
		verifier: verifier,
		y:        y,
		w0:       w0,
		lx:       lx,
		ly:       ly,
	}, nil
}

// PublicValue returns pB = y*G + w0*N (SEC1 uncompressed, 65 bytes).
func (s *SPAKE2PlusServer) PublicValue() []byte {
	if s.pB != nil {
		return s.pB
	}
	yx, yy := curve.ScalarBaseMult(s.y.Bytes())
	w0nx, w0ny := curve.ScalarMult(nX, nY, s.w0.Bytes())
	pBx, pBy := curve.Add(yx, yy, w0nx, w0ny)
	s.pB = elliptic.Marshal(curve, pBx, pBy)
	return s.pB
}

// ProcessClientValue validates pA and derives the session-key chain.
func (s *SPAKE2PlusServer) ProcessClientValue(pA []byte) error {
	if err := ValidatePoint(pA); err != nil {
		return ErrInvalidPublicKey
	}
	pAx, pAy := elliptic.Unmarshal(curve, pA)
	if pAx == nil {
		return ErrInvalidPublicKey
	}
	s.pA = pA
	_ = s.PublicValue()

	// X_unmasked = pA - w0*M
	w0mx, w0my := curve.ScalarMult(mX, mY, s.w0.Bytes())
	w0myNeg := new(big.Int).Neg(w0my)
	w0myNeg.Mod(w0myNeg, curve.Params().P)
	xx, xy := curve.Add(pAx, pAy, w0mx, w0myNeg)

	// Z = y * X_unmasked, V = y * L.
	zx, zy := curve.ScalarMult(xx, xy, s.y.Bytes())
	vx, vy := curve.ScalarMult(s.lx, s.ly, s.y.Bytes())

	s.deriveKeys(zx, zy, vx, vy)
	return nil
}

func (s *SPAKE2PlusServer) deriveKeys(zx, zy, vx, vy *big.Int) {
	w0Bytes := scalarToBytes(s.w0)
	TT := composeTranscriptInternal(
		ControllerIdentity, DeviceIdentity,
		mPointBytes(), nPointBytes(),
		s.pA, s.pB,
		marshalAffine(zx, zy), marshalAffine(vx, vy),
		w0Bytes,
	)
	K := sha256Sum(TT)
	s.kMain, s.kConfirm, s.kEncrypt = computeKeyChain(K)
	s.kcA, s.kcB = splitConfirmationKeys(s.kConfirm)
}

// Confirmation returns confirmB = HMAC-SHA256(KcB, pA).
func (s *SPAKE2PlusServer) Confirmation() []byte {
	mac := hmac.New(sha256.New, s.kcB)
	mac.Write(s.pA)
	return mac.Sum(nil)
}

// VerifyClientConfirmation verifies confirmA = HMAC-SHA256(KcA, pB) in
// constant time.
func (s *SPAKE2PlusServer) VerifyClientConfirmation(clientConfirm []byte) error {
	mac := hmac.New(sha256.New, s.kcA)
	mac.Write(s.pB)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(clientConfirm, expected) != 1 {
		return ErrConfirmationFailed
	}
	return nil
}

// SharedSecret returns K_main (see SPAKE2PlusClient.SharedSecret).
func (s *SPAKE2PlusServer) SharedSecret() []byte {
	return s.kMain
}

// EncryptionKey returns K_encrypt.
func (s *SPAKE2PlusServer) EncryptionKey() []byte {
	return s.kEncrypt
}

// =============================================================================
// DEC-084 primitive surface (also exposed for the Phase 7B vector runner).
// =============================================================================

// DeriveSalt computes the per-device PBKDF2 salt:
//
//	salt = SHA-256("MASH PBKDF2 salt v1" || discriminator(BE16) || setup_code)[0:16]
//
// Per DEC-084 §2.3 the discriminator must fit in 12 bits.
func DeriveSalt(discriminator uint16, setupCode []byte) ([]byte, error) {
	if discriminator >= 1<<12 {
		return nil, fmt.Errorf("discriminator %d exceeds 12 bits", discriminator)
	}
	h := sha256.New()
	h.Write(saltPrefix)
	var disc [2]byte
	binary.BigEndian.PutUint16(disc[:], discriminator)
	h.Write(disc[:])
	h.Write(setupCode)
	full := h.Sum(nil)
	return full[:SaltSize], nil
}

// MHF runs PBKDF2-HMAC-SHA256 with the given iteration count and output
// length. DEC-084 §2.3 fixes iterations=100,000 and dkLen=80; this surface is
// parametric only so the conformance vectors can exercise edge-case inputs.
func MHF(setupCode, salt []byte, iterations, dkLen int) ([]byte, error) {
	if iterations <= 0 {
		return nil, fmt.Errorf("iterations must be positive, got %d", iterations)
	}
	if dkLen <= 0 {
		return nil, fmt.Errorf("dkLen must be positive, got %d", dkLen)
	}
	return pbkdf2.Key(setupCode, salt, iterations, dkLen, sha256.New), nil
}

// DeriveW0W1 splits an 80-byte MHF output into two 40-byte halves and reduces
// each modulo n. Returns w0 and w1 as 32-byte big-endian scalars in `[0, n)`.
// Returns an error if either reduces to zero (probability ≈ 2^-256).
func DeriveW0W1(mhfOut []byte) (w0, w1 []byte, err error) {
	if len(mhfOut) != MHFOutputSize {
		return nil, nil, fmt.Errorf("mhf_out must be %d bytes, got %d", MHFOutputSize, len(mhfOut))
	}
	n := curve.Params().N
	w0Int := new(big.Int).SetBytes(mhfOut[:40])
	w1Int := new(big.Int).SetBytes(mhfOut[40:])
	w0Int.Mod(w0Int, n)
	w1Int.Mod(w1Int, n)
	if w0Int.Sign() == 0 || w1Int.Sign() == 0 {
		return nil, nil, fmt.Errorf("%w: w0 or w1 reduced to zero", ErrInvalidScalar)
	}
	return scalarToBytes(w0Int), scalarToBytes(w1Int), nil
}

// ComputeL multiplies w1 by the P-256 base point G. Returns the 65-byte SEC1
// uncompressed encoding of the resulting verifier point.
func ComputeL(w1 []byte) ([]byte, error) {
	if len(w1) != ScalarSize {
		return nil, fmt.Errorf("w1 must be %d bytes, got %d", ScalarSize, len(w1))
	}
	w1Int := new(big.Int).SetBytes(w1)
	if w1Int.Sign() == 0 {
		return nil, fmt.Errorf("%w: w1 is zero", ErrInvalidScalar)
	}
	lx, ly := curve.ScalarBaseMult(w1)
	return elliptic.Marshal(curve, lx, ly), nil
}

// ClientPublic computes pA = x*G + w0*M for the given scalars and returns the
// 65-byte SEC1 uncompressed encoding.
func ClientPublic(w0, x []byte) ([]byte, error) {
	if len(w0) != ScalarSize || len(x) != ScalarSize {
		return nil, fmt.Errorf("w0 and x must be %d bytes each", ScalarSize)
	}
	xx, xy := curve.ScalarBaseMult(x)
	w0mx, w0my := curve.ScalarMult(mX, mY, w0)
	pAx, pAy := curve.Add(xx, xy, w0mx, w0my)
	return elliptic.Marshal(curve, pAx, pAy), nil
}

// ServerPublic computes pB = y*G + w0*N for the given scalars and returns the
// 65-byte SEC1 uncompressed encoding.
func ServerPublic(w0, y []byte) ([]byte, error) {
	if len(w0) != ScalarSize || len(y) != ScalarSize {
		return nil, fmt.Errorf("w0 and y must be %d bytes each", ScalarSize)
	}
	yy0, yy1 := curve.ScalarBaseMult(y)
	w0nx, w0ny := curve.ScalarMult(nX, nY, w0)
	pBx, pBy := curve.Add(yy0, yy1, w0nx, w0ny)
	return elliptic.Marshal(curve, pBx, pBy), nil
}

// ComposeTranscript builds the DEC-084 §2.4 / RFC 9383 §3.3 length-prefixed
// transcript. Each field is preceded by its length as a uint64 little-endian.
// Field byte representations are caller-provided (M, N, pA, pB, Z, V are the
// 65-byte SEC1 encodings; w0 is the 32-byte big-endian scalar).
func ComposeTranscript(controllerIdentity, deviceIdentity, M, N, pA, pB, Z, V, w0 []byte) ([]byte, error) {
	return composeTranscriptInternal(controllerIdentity, deviceIdentity, M, N, pA, pB, Z, V, w0), nil
}

// KShared returns SHA-256(TT).
func KShared(TT []byte) ([]byte, error) {
	return sha256Sum(TT), nil
}

// KeyChain derives K_main, K_confirm, K_encrypt per DEC-084 §2.4.
//
//	K_main    = HKDF(salt="", IKM=K_shared, info="MASH PASE Session Key",  L=32)
//	K_confirm = HKDF(salt="", IKM=K_shared, info="MASH PASE Confirmation", L=32)
//	K_encrypt = HKDF(salt="", IKM=K_main,   info="MASH PASE Encryption",   L=32)
func KeyChain(KSharedBytes []byte) (KMain, KConfirm, KEncrypt []byte, err error) {
	if len(KSharedBytes) != 32 {
		return nil, nil, nil, fmt.Errorf("K_shared must be 32 bytes, got %d", len(KSharedBytes))
	}
	main, confirm, encrypt := computeKeyChain(KSharedBytes)
	return main, confirm, encrypt, nil
}

// ConfirmationKeys derives KcA||KcB = HKDF(K_confirm, "ConfirmationKeys", 32)
// and returns the first 16 bytes as KcA, the next 16 as KcB.
func ConfirmationKeys(KConfirmBytes []byte) (KcA, KcB []byte, err error) {
	if len(KConfirmBytes) != 32 {
		return nil, nil, fmt.Errorf("K_confirm must be 32 bytes, got %d", len(KConfirmBytes))
	}
	a, b := splitConfirmationKeys(KConfirmBytes)
	return a, b, nil
}

// ConfirmMACs computes confirmA = HMAC(KcA, pB), confirmB = HMAC(KcB, pA).
func ConfirmMACs(KcA, KcB, pA, pB []byte) (confirmA, confirmB []byte, err error) {
	if len(KcA) != confirmHalfSize || len(KcB) != confirmHalfSize {
		return nil, nil, fmt.Errorf("KcA, KcB must be %d bytes each", confirmHalfSize)
	}
	macA := hmac.New(sha256.New, KcA)
	macA.Write(pB)
	macB := hmac.New(sha256.New, KcB)
	macB.Write(pA)
	return macA.Sum(nil), macB.Sum(nil), nil
}

// ValidatePoint enforces DEC-084 §2.5 point validation:
//   - Exactly 65 bytes, leading byte 0x04 (SEC1 uncompressed).
//   - On the P-256 curve.
//   - Not the point at infinity (identity element).
//
// Returns nil if the point is valid for SPAKE2+ use.
func ValidatePoint(point []byte) error {
	if len(point) != PointSize || point[0] != 0x04 {
		return fmt.Errorf("%w: not SEC1 uncompressed (need %d bytes, leading 0x04)", ErrInvalidPoint, PointSize)
	}
	x := new(big.Int).SetBytes(point[1:33])
	y := new(big.Int).SetBytes(point[33:65])
	// Identity element check: SEC1 uncompressed encoding of the identity is
	// 0x04 || 32 zero bytes || 32 zero bytes — invalid per DEC-084 §2.5.
	if x.Sign() == 0 && y.Sign() == 0 {
		return fmt.Errorf("%w: point at infinity", ErrIdentityElement)
	}
	if !curve.IsOnCurve(x, y) {
		return fmt.Errorf("%w: not on P-256", ErrInvalidPoint)
	}
	return nil
}

// =============================================================================
// Internal helpers
// =============================================================================

func deriveW0W1FromCode(setupCode SetupCode, discriminator uint16) (w0, w1 []byte, err error) {
	password := []byte(setupCode.String())
	salt, err := DeriveSalt(discriminator, password)
	if err != nil {
		return nil, nil, err
	}
	mhfOut, err := MHF(password, salt, MHFIterations, MHFOutputSize)
	if err != nil {
		return nil, nil, err
	}
	return DeriveW0W1(mhfOut)
}

// composeTranscriptInternal is the unchecked variant used by the handshake
// machinery; the public ComposeTranscript wraps it with an error return for
// API symmetry.
func composeTranscriptInternal(controllerIdentity, deviceIdentity, M, N, pA, pB, Z, V, w0 []byte) []byte {
	fields := [][]byte{controllerIdentity, deviceIdentity, M, N, pA, pB, Z, V, w0}
	total := 0
	for _, f := range fields {
		total += 8 + len(f)
	}
	out := make([]byte, 0, total)
	var lenBuf [8]byte
	for _, f := range fields {
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(f)))
		out = append(out, lenBuf[:]...)
		out = append(out, f...)
	}
	return out
}

func computeKeyChain(KSharedBytes []byte) (kMain, kConfirm, kEncrypt []byte) {
	kMain = hkdfExpand32(KSharedBytes, infoKMain)
	kConfirm = hkdfExpand32(KSharedBytes, infoKConfirm)
	kEncrypt = hkdfExpand32(kMain, infoKEncrypt)
	return kMain, kConfirm, kEncrypt
}

func splitConfirmationKeys(kConfirm []byte) (kcA, kcB []byte) {
	full := hkdfExpand32(kConfirm, infoKcAKcB)
	a := make([]byte, confirmHalfSize)
	b := make([]byte, confirmHalfSize)
	copy(a, full[:confirmHalfSize])
	copy(b, full[confirmHalfSize:])
	return a, b
}

// hkdfExpand32 runs HKDF-SHA256 with empty salt and the given info, returning
// 32 bytes. DEC-084 §2.4 specifies salt="" for every HKDF call in the chain.
func hkdfExpand32(ikm, info []byte) []byte {
	r := hkdf.New(sha256.New, ikm, nil, info)
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		// HKDF over SHA-256 with these sizes never fails; treat as a programmer
		// bug if it does.
		panic(fmt.Sprintf("hkdf expand: %v", err))
	}
	return out
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// scalarToBytes returns the 32-byte big-endian encoding of a P-256 scalar.
// Pads with leading zeros if the scalar is shorter than 32 bytes.
func scalarToBytes(s *big.Int) []byte {
	out := make([]byte, ScalarSize)
	b := s.Bytes()
	copy(out[ScalarSize-len(b):], b)
	return out
}

// marshalAffine returns the SEC1 uncompressed encoding of (x, y).
func marshalAffine(x, y *big.Int) []byte {
	return elliptic.Marshal(curve, x, y)
}

// mPointBytes / nPointBytes return the 65-byte SEC1 encodings of M and N.
func mPointBytes() []byte { return elliptic.Marshal(curve, mX, mY) }
func nPointBytes() []byte { return elliptic.Marshal(curve, nX, nY) }

// randomEphemeral returns a uniform random scalar in [1, n-1].
func randomEphemeral() (*big.Int, error) {
	n := curve.Params().N
	for i := 0; i < 8; i++ {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return nil, err
		}
		if k.Sign() != 0 {
			return k, nil
		}
	}
	return nil, errors.New("failed to generate non-zero ephemeral scalar")
}
