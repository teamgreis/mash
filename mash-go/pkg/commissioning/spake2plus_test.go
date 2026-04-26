package commissioning

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

func TestSPAKE2PlusBasicExchange(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, err := GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("GenerateVerifier failed: %v", err)
	}

	client, err := NewSPAKE2PlusClient(setupCode, discriminator)
	if err != nil {
		t.Fatalf("NewSPAKE2PlusClient failed: %v", err)
	}

	server, err := NewSPAKE2PlusServer(verifier)
	if err != nil {
		t.Fatalf("NewSPAKE2PlusServer failed: %v", err)
	}

	pA := client.PublicValue()
	pB := server.PublicValue()

	if err := server.ProcessClientValue(pA); err != nil {
		t.Fatalf("Server.ProcessClientValue failed: %v", err)
	}
	if err := client.ProcessServerValue(pB); err != nil {
		t.Fatalf("Client.ProcessServerValue failed: %v", err)
	}

	if !bytes.Equal(client.SharedSecret(), server.SharedSecret()) {
		t.Errorf("K_main mismatch:\nclient: %x\nserver: %x",
			client.SharedSecret(), server.SharedSecret())
	}
	if !bytes.Equal(client.EncryptionKey(), server.EncryptionKey()) {
		t.Errorf("K_encrypt mismatch")
	}

	if err := server.VerifyClientConfirmation(client.Confirmation()); err != nil {
		t.Errorf("server failed to verify client confirmation: %v", err)
	}
	if err := client.VerifyServerConfirmation(server.Confirmation()); err != nil {
		t.Errorf("client failed to verify server confirmation: %v", err)
	}
}

func TestSPAKE2PlusWrongPassword(t *testing.T) {
	correctCode := SetupCode(20202021)
	wrongCode := SetupCode(87654321)
	const discriminator uint16 = 1234

	verifier, err := GenerateVerifier(correctCode, discriminator)
	if err != nil {
		t.Fatalf("GenerateVerifier failed: %v", err)
	}

	client, err := NewSPAKE2PlusClient(wrongCode, discriminator)
	if err != nil {
		t.Fatalf("NewSPAKE2PlusClient failed: %v", err)
	}

	server, err := NewSPAKE2PlusServer(verifier)
	if err != nil {
		t.Fatalf("NewSPAKE2PlusServer failed: %v", err)
	}

	pA := client.PublicValue()
	pB := server.PublicValue()

	if err := server.ProcessClientValue(pA); err != nil {
		t.Fatalf("Server.ProcessClientValue failed: %v", err)
	}
	if err := client.ProcessServerValue(pB); err != nil {
		t.Fatalf("Client.ProcessServerValue failed: %v", err)
	}

	if bytes.Equal(client.SharedSecret(), server.SharedSecret()) {
		t.Error("K_main should not match with wrong password")
	}

	if err := server.VerifyClientConfirmation(client.Confirmation()); err == nil {
		t.Error("server should reject client confirmation with wrong password")
	}
	if err := client.VerifyServerConfirmation(server.Confirmation()); err == nil {
		t.Error("client should reject server confirmation with wrong password")
	}
}

func TestSPAKE2PlusReplay(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, _ := GenerateVerifier(setupCode, discriminator)

	client1, _ := NewSPAKE2PlusClient(setupCode, discriminator)
	server1, _ := NewSPAKE2PlusServer(verifier)
	pA1 := client1.PublicValue()
	pB1 := server1.PublicValue()
	_ = server1.ProcessClientValue(pA1)
	_ = client1.ProcessServerValue(pB1)

	client2, _ := NewSPAKE2PlusClient(setupCode, discriminator)
	server2, _ := NewSPAKE2PlusServer(verifier)
	pA2 := client2.PublicValue()
	pB2 := server2.PublicValue()
	_ = server2.ProcessClientValue(pA2)
	_ = client2.ProcessServerValue(pB2)

	if bytes.Equal(pA1, pA2) {
		t.Error("client public values should differ between sessions")
	}
	if bytes.Equal(pB1, pB2) {
		t.Error("server public values should differ between sessions")
	}
	if bytes.Equal(client1.SharedSecret(), client2.SharedSecret()) {
		t.Error("K_main should differ between sessions")
	}
}

func TestSPAKE2PlusInvalidPublicKey(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, _ := GenerateVerifier(setupCode, discriminator)
	client, _ := NewSPAKE2PlusClient(setupCode, discriminator)
	server, _ := NewSPAKE2PlusServer(verifier)

	_ = client.PublicValue()
	_ = server.PublicValue()

	// Too short to parse as SEC1 uncompressed.
	err := server.ProcessClientValue([]byte{0x01, 0x02, 0x03})
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Errorf("expected ErrInvalidPublicKey, got %v", err)
	}
	err = client.ProcessServerValue([]byte{0x01, 0x02, 0x03})
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Errorf("expected ErrInvalidPublicKey, got %v", err)
	}
}

func TestSPAKE2PlusNilVerifier(t *testing.T) {
	_, err := NewSPAKE2PlusServer(nil)
	if !errors.Is(err, ErrInvalidVerifier) {
		t.Errorf("expected ErrInvalidVerifier, got %v", err)
	}
}

func TestGenerateVerifier(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, err := GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("GenerateVerifier failed: %v", err)
	}

	if len(verifier.W0) != ScalarSize {
		t.Errorf("W0 should be %d bytes, got %d", ScalarSize, len(verifier.W0))
	}
	if len(verifier.L) != PointSize {
		t.Errorf("L should be %d bytes (SEC1 uncompressed), got %d", PointSize, len(verifier.L))
	}
	if len(verifier.Salt) != SaltSize {
		t.Errorf("Salt should be %d bytes, got %d", SaltSize, len(verifier.Salt))
	}
	if verifier.Discriminator != discriminator {
		t.Errorf("Discriminator = %d, want %d", verifier.Discriminator, discriminator)
	}
	if verifier.Iterations != MHFIterations {
		t.Errorf("Iterations = %d, want %d", verifier.Iterations, MHFIterations)
	}

	// Same inputs should produce same verifier (determinism).
	verifier2, _ := GenerateVerifier(setupCode, discriminator)
	if !bytes.Equal(verifier.W0, verifier2.W0) {
		t.Error("same inputs should produce same W0")
	}
	if !bytes.Equal(verifier.L, verifier2.L) {
		t.Error("same inputs should produce same L")
	}
	if !bytes.Equal(verifier.Salt, verifier2.Salt) {
		t.Error("same inputs should produce same salt")
	}

	// Different setup code → different verifier.
	verifier3, _ := GenerateVerifier(SetupCode(87654321), discriminator)
	if bytes.Equal(verifier.W0, verifier3.W0) {
		t.Error("different setup code should produce different W0")
	}

	// Different discriminator → different salt → different verifier.
	verifier4, _ := GenerateVerifier(setupCode, discriminator+1)
	if bytes.Equal(verifier.Salt, verifier4.Salt) {
		t.Error("different discriminator should produce different salt")
	}
}

func TestSPAKE2PlusPublicValueIdempotent(t *testing.T) {
	setupCode := SetupCode(20202021)
	client, _ := NewSPAKE2PlusClient(setupCode, 1234)
	pA1 := client.PublicValue()
	pA2 := client.PublicValue()
	if !bytes.Equal(pA1, pA2) {
		t.Error("PublicValue should return the same value on multiple calls")
	}
}

func TestSPAKE2PlusSharedSecretSize(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, _ := GenerateVerifier(setupCode, discriminator)
	client, _ := NewSPAKE2PlusClient(setupCode, discriminator)
	server, _ := NewSPAKE2PlusServer(verifier)
	pA := client.PublicValue()
	pB := server.PublicValue()
	_ = server.ProcessClientValue(pA)
	_ = client.ProcessServerValue(pB)

	if len(client.SharedSecret()) != SharedSecretSize {
		t.Errorf("client K_main size = %d, want %d", len(client.SharedSecret()), SharedSecretSize)
	}
	if len(server.SharedSecret()) != SharedSecretSize {
		t.Errorf("server K_main size = %d, want %d", len(server.SharedSecret()), SharedSecretSize)
	}
}

func TestSPAKE2PlusConfirmationSize(t *testing.T) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234

	verifier, _ := GenerateVerifier(setupCode, discriminator)
	client, _ := NewSPAKE2PlusClient(setupCode, discriminator)
	server, _ := NewSPAKE2PlusServer(verifier)
	pA := client.PublicValue()
	pB := server.PublicValue()
	_ = server.ProcessClientValue(pA)
	_ = client.ProcessServerValue(pB)

	if len(client.Confirmation()) != ConfirmationSize {
		t.Errorf("client confirmation size = %d, want %d", len(client.Confirmation()), ConfirmationSize)
	}
	if len(server.Confirmation()) != ConfirmationSize {
		t.Errorf("server confirmation size = %d, want %d", len(server.Confirmation()), ConfirmationSize)
	}
}

func TestSPAKE2PlusPublicValuesAre65Bytes(t *testing.T) {
	// DEC-084 §4.2: pA and pB MUST be exactly 65 bytes (uncompressed). The
	// pre-DEC-084 spec mistyped this as bytes[32].
	client, _ := NewSPAKE2PlusClient(SetupCode(20202021), 1234)
	verifier, _ := GenerateVerifier(SetupCode(20202021), 1234)
	server, _ := NewSPAKE2PlusServer(verifier)

	if got := len(client.PublicValue()); got != PointSize {
		t.Errorf("pA size = %d, want %d", got, PointSize)
	}
	if got := len(server.PublicValue()); got != PointSize {
		t.Errorf("pB size = %d, want %d", got, PointSize)
	}
}

// =============================================================================
// DEC-084 primitive surface tests (Phase 7B vectors run separately via
// cmd/mash-test/spake2plus_conformance.go).
// =============================================================================

func TestDeriveSalt_Deterministic(t *testing.T) {
	salt1, err := DeriveSalt(1234, []byte("20202021"))
	if err != nil {
		t.Fatalf("DeriveSalt: %v", err)
	}
	salt2, err := DeriveSalt(1234, []byte("20202021"))
	if err != nil {
		t.Fatalf("DeriveSalt: %v", err)
	}
	if !bytes.Equal(salt1, salt2) {
		t.Error("DeriveSalt is not deterministic")
	}
	if len(salt1) != SaltSize {
		t.Errorf("salt size = %d, want %d", len(salt1), SaltSize)
	}

	// Different inputs → different salts.
	salt3, _ := DeriveSalt(1235, []byte("20202021"))
	if bytes.Equal(salt1, salt3) {
		t.Error("different discriminator should produce different salt")
	}
	salt4, _ := DeriveSalt(1234, []byte("87654321"))
	if bytes.Equal(salt1, salt4) {
		t.Error("different setup code should produce different salt")
	}
}

func TestDeriveSalt_Rejects13BitDiscriminator(t *testing.T) {
	// DEC-084 §2.3: discriminator is 12 bits.
	_, err := DeriveSalt(1<<12, []byte("20202021"))
	if err == nil {
		t.Error("DeriveSalt should reject a discriminator that doesn't fit in 12 bits")
	}
}

func TestMHF_Roundtrip(t *testing.T) {
	// PBKDF2 100k is the entire point — confirm it actually runs and produces
	// the right length output. This is also a smoke test that golang.org/x/
	// crypto/pbkdf2 is wired up.
	out, err := MHF([]byte("20202021"), bytes.Repeat([]byte{0}, SaltSize), MHFIterations, MHFOutputSize)
	if err != nil {
		t.Fatalf("MHF: %v", err)
	}
	if len(out) != MHFOutputSize {
		t.Errorf("MHF output size = %d, want %d", len(out), MHFOutputSize)
	}
	// Different setup codes → different MHF output.
	out2, _ := MHF([]byte("87654321"), bytes.Repeat([]byte{0}, SaltSize), MHFIterations, MHFOutputSize)
	if bytes.Equal(out, out2) {
		t.Error("different setup code should produce different MHF output")
	}
}

func TestDeriveW0W1_RejectsWrongLength(t *testing.T) {
	_, _, err := DeriveW0W1(make([]byte, 79))
	if err == nil {
		t.Error("DeriveW0W1 should reject input of length 79")
	}
}

func TestComputeL_RejectsZero(t *testing.T) {
	_, err := ComputeL(make([]byte, ScalarSize))
	if !errors.Is(err, ErrInvalidScalar) {
		t.Errorf("ComputeL(zero) should return ErrInvalidScalar, got %v", err)
	}
}

func TestValidatePoint_RejectsIdentity(t *testing.T) {
	// DEC-084 §2.5 mandates rejection of the identity element. Pre-DEC-084
	// mash-go missed this — degenerate-point attack would have made Z and V
	// predictable.
	identity := append([]byte{0x04}, make([]byte, 64)...)
	if err := ValidatePoint(identity); !errors.Is(err, ErrIdentityElement) {
		t.Errorf("ValidatePoint(identity) should return ErrIdentityElement, got %v", err)
	}
}

func TestValidatePoint_RejectsWrongLength(t *testing.T) {
	if err := ValidatePoint(make([]byte, 33)); !errors.Is(err, ErrInvalidPoint) {
		t.Errorf("ValidatePoint(33 bytes) should return ErrInvalidPoint, got %v", err)
	}
}

func TestValidatePoint_RejectsCompressedPrefix(t *testing.T) {
	bad := append([]byte{0x02}, make([]byte, 64)...)
	if err := ValidatePoint(bad); !errors.Is(err, ErrInvalidPoint) {
		t.Errorf("ValidatePoint(0x02 prefix) should return ErrInvalidPoint, got %v", err)
	}
}

func TestValidatePoint_RejectsOffCurve(t *testing.T) {
	// G with the y-coordinate altered by 1 — same x, off-curve.
	offCurve, _ := hex.DecodeString(
		"046b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296" +
			"4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f6")
	if err := ValidatePoint(offCurve); err == nil {
		t.Error("ValidatePoint should reject an off-curve point")
	}
}

func TestComposeTranscript_LengthPrefixCanonicalization(t *testing.T) {
	// DEC-084 §2.4 / RFC 9383 §3.3: each field is preceded by an 8-byte
	// little-endian length. Without that prefix, `("ab", "c")` and
	// `("a", "bc")` collide. Confirm the encoded transcript carries the
	// length prefixes and the boundary moves.
	tt1, err := ComposeTranscript(
		[]byte("ab"), []byte("c"),
		[]byte{}, []byte{}, []byte{}, []byte{}, []byte{}, []byte{}, []byte{},
	)
	if err != nil {
		t.Fatalf("ComposeTranscript: %v", err)
	}
	tt2, err := ComposeTranscript(
		[]byte("a"), []byte("bc"),
		[]byte{}, []byte{}, []byte{}, []byte{}, []byte{}, []byte{}, []byte{},
	)
	if err != nil {
		t.Fatalf("ComposeTranscript: %v", err)
	}
	if bytes.Equal(tt1, tt2) {
		t.Error("(\"ab\", \"c\") and (\"a\", \"bc\") MUST encode differently when length-prefixed")
	}

	// Spot-check the leading 8 bytes encode len=2 little-endian.
	want := make([]byte, 8)
	binary.LittleEndian.PutUint64(want, 2)
	if !bytes.Equal(tt1[:8], want) {
		t.Errorf("leading length prefix = %x, want %x", tt1[:8], want)
	}
}

func TestKeyChain_DistinctOutputs(t *testing.T) {
	// All three keys come from K_shared via different info strings; they MUST
	// be distinct. K_encrypt comes from K_main (not K_shared) per DEC-084.
	K := sha256.Sum256([]byte("test transcript"))
	main, confirm, encrypt, err := KeyChain(K[:])
	if err != nil {
		t.Fatalf("KeyChain: %v", err)
	}
	if bytes.Equal(main, confirm) {
		t.Error("K_main and K_confirm must differ")
	}
	if bytes.Equal(main, encrypt) {
		t.Error("K_main and K_encrypt must differ")
	}
	if bytes.Equal(confirm, encrypt) {
		t.Error("K_confirm and K_encrypt must differ")
	}
}

func TestConfirmationKeys_DistinctHalves(t *testing.T) {
	// RFC 9383 §3.4 / DEC-084: KcA and KcB are 16-byte halves of one HKDF
	// expansion; they must differ to prevent reflection attacks.
	K := sha256.Sum256([]byte("k-confirm fixture"))
	kcA, kcB, err := ConfirmationKeys(K[:])
	if err != nil {
		t.Fatalf("ConfirmationKeys: %v", err)
	}
	if len(kcA) != confirmHalfSize || len(kcB) != confirmHalfSize {
		t.Errorf("KcA, KcB sizes = %d, %d; want both %d", len(kcA), len(kcB), confirmHalfSize)
	}
	if bytes.Equal(kcA, kcB) {
		t.Error("KcA and KcB must differ (reflection-attack prevention)")
	}
}

func BenchmarkSPAKE2PlusExchange(b *testing.B) {
	setupCode := SetupCode(20202021)
	const discriminator uint16 = 1234
	verifier, _ := GenerateVerifier(setupCode, discriminator)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client, _ := NewSPAKE2PlusClient(setupCode, discriminator)
		server, _ := NewSPAKE2PlusServer(verifier)

		pA := client.PublicValue()
		pB := server.PublicValue()

		_ = server.ProcessClientValue(pA)
		_ = client.ProcessServerValue(pB)

		_ = client.Confirmation()
		_ = server.Confirmation()
	}
}
