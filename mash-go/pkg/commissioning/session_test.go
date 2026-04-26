package commissioning_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// mockConn implements a simple in-memory connection for testing.
type mockConn struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func newMockConnPair() (*mockConn, *mockConn) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	return &mockConn{reader: r1, writer: w2},
		&mockConn{reader: r2, writer: w1}
}

func (c *mockConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *mockConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *mockConn) Close() error {
	c.reader.Close()
	c.writer.Close()
	return nil
}
func (c *mockConn) LocalAddr() net.Addr                { return nil }
func (c *mockConn) RemoteAddr() net.Addr               { return nil }
func (c *mockConn) SetDeadline(t time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// TestPASESessionSuccess verifies successful PASE handshake between client and server.
func TestPASESessionSuccess(t *testing.T) {
	// Setup. DEC-084 §2.4 fixes the SPAKE2+ identity strings; the salt is
	// derived from the discriminator + setup code instead.
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	// Device generates verifier from setup code (done during manufacturing)
	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	// Create mock connections
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	// Create sessions
	clientSession, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to create client session: %v", err)
	}

	serverSession, err := commissioning.NewPASEServerSession(verifier)
	if err != nil {
		t.Fatalf("Failed to create server session: %v", err)
	}

	// Run handshake concurrently
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var clientErr, serverErr error
	var clientKey, serverKey []byte

	wg.Add(2)

	// Client side
	go func() {
		defer wg.Done()
		clientKey, clientErr = clientSession.Handshake(ctx, clientConn)
	}()

	// Server side
	go func() {
		defer wg.Done()
		serverKey, serverErr = serverSession.Handshake(ctx, serverConn)
	}()

	wg.Wait()

	// Check results
	if clientErr != nil {
		t.Fatalf("Client handshake failed: %v", clientErr)
	}
	if serverErr != nil {
		t.Fatalf("Server handshake failed: %v", serverErr)
	}

	// Verify both sides derived the same key
	if !bytes.Equal(clientKey, serverKey) {
		t.Error("Client and server derived different session keys")
	}

	if len(clientKey) != commissioning.SharedSecretSize {
		t.Errorf("Session key wrong size: expected %d, got %d", commissioning.SharedSecretSize, len(clientKey))
	}
}

// TestPASESessionWrongPassword verifies PASE fails with wrong setup code.
func TestPASESessionWrongPassword(t *testing.T) {
	// Setup with correct code
	correctCode := commissioning.MustParseSetupCode("20202021")
	wrongCode := commissioning.MustParseSetupCode("87654321")
	const discriminator uint16 = 1234

	// Device has verifier for correct code
	verifier, err := commissioning.GenerateVerifier(correctCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	// Create mock connections
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	// Client uses wrong code
	clientSession, _ := commissioning.NewPASEClientSession(wrongCode, discriminator)
	serverSession, _ := commissioning.NewPASEServerSession(verifier)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var clientErr, serverErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		_, clientErr = clientSession.Handshake(ctx, clientConn)
	}()

	go func() {
		defer wg.Done()
		_, serverErr = serverSession.Handshake(ctx, serverConn)
	}()

	wg.Wait()

	// At least one side should fail
	if clientErr == nil && serverErr == nil {
		t.Error("Expected handshake to fail with wrong password")
	}
}

// TestPASESessionTimeout verifies PASE times out properly.
func TestPASESessionTimeout(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	// Create connection pair but only use client side (server won't respond)
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	clientSession, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to create client session: %v", err)
	}

	// Start server session but don't run handshake - just read and discard
	go func() {
		buf := make([]byte, 1024)
		for {
			_, err := serverConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = clientSession.Handshake(ctx, clientConn)
	if err == nil {
		t.Error("Expected timeout error")
	}
}

// TestPASEClientIdentity verifies the protocol-constant client identity is
// returned. Per DEC-084 §2.4 v1 always uses b"mash-controller".
func TestPASEClientIdentity(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	clientSession, err := commissioning.NewPASEClientSession(setupCode, 1234)
	if err != nil {
		t.Fatalf("Failed to create client session: %v", err)
	}

	if !bytes.Equal(clientSession.ClientIdentity(), commissioning.ControllerIdentity) {
		t.Errorf("ClientIdentity = %q, want %q",
			clientSession.ClientIdentity(), commissioning.ControllerIdentity)
	}
}

// TestPASEServerSplitHandshake verifies the split handshake API:
// server uses WaitForPASERequest + CompleteHandshake, client uses Handshake.
// Both sides should derive the same shared secret.
func TestPASEServerSplitHandshake(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	clientSession, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to create client session: %v", err)
	}

	serverSession, err := commissioning.NewPASEServerSession(verifier)
	if err != nil {
		t.Fatalf("Failed to create server session: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var clientErr, serverErr error
	var clientKey, serverKey []byte

	wg.Add(2)

	// Client side uses normal Handshake
	go func() {
		defer wg.Done()
		clientKey, clientErr = clientSession.Handshake(ctx, clientConn)
	}()

	// Server side uses split API
	go func() {
		defer wg.Done()
		req, err := serverSession.WaitForPASERequest(ctx, serverConn)
		if err != nil {
			serverErr = err
			return
		}
		serverKey, serverErr = serverSession.CompleteHandshake(ctx, serverConn, req)
	}()

	wg.Wait()

	if clientErr != nil {
		t.Fatalf("Client handshake failed: %v", clientErr)
	}
	if serverErr != nil {
		t.Fatalf("Server split handshake failed: %v", serverErr)
	}

	if !bytes.Equal(clientKey, serverKey) {
		t.Error("Client and server derived different session keys")
	}

	if len(clientKey) != commissioning.SharedSecretSize {
		t.Errorf("Session key wrong size: expected %d, got %d", commissioning.SharedSecretSize, len(clientKey))
	}
}

// TestWaitForPASERequest_Timeout verifies WaitForPASERequest returns
// context.DeadlineExceeded when no message arrives within the timeout.
func TestWaitForPASERequest_Timeout(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	_, serverConn := newMockConnPair()
	defer serverConn.Close()

	serverSession, err := commissioning.NewPASEServerSession(verifier)
	if err != nil {
		t.Fatalf("Failed to create server session: %v", err)
	}

	// Very short timeout -- no client will send anything
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = serverSession.WaitForPASERequest(ctx, serverConn)
	if err == nil {
		t.Fatal("Expected error from WaitForPASERequest with no client message")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
	}
}

// TestWaitForPASERequest_ConnectionClosed verifies WaitForPASERequest returns
// an error when the client closes the connection immediately.
func TestWaitForPASERequest_ConnectionClosed(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	clientConn, serverConn := newMockConnPair()
	defer serverConn.Close()

	serverSession, err := commissioning.NewPASEServerSession(verifier)
	if err != nil {
		t.Fatalf("Failed to create server session: %v", err)
	}

	// Close client side immediately
	clientConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = serverSession.WaitForPASERequest(ctx, serverConn)
	if err == nil {
		t.Fatal("Expected error when client closes connection")
	}
}

// =============================================================================
// WriteCommissioningError tests (DEC-063)
// =============================================================================

// TestWriteCommissioningError_SendsValidCBOR verifies WriteCommissioningError
// sends a length-prefixed CBOR message that decodes to a CommissioningError.
func TestWriteCommissioningError_SendsValidCBOR(t *testing.T) {
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	// Write a busy error with RetryAfter
	done := make(chan error, 1)
	go func() {
		done <- commissioning.WriteCommissioningError(serverConn, commissioning.ErrCodeBusy, "device busy", 5000)
	}()

	// Read it back on the client side using the standard readMessage path
	// (length-prefixed CBOR)
	msg, err := readLengthPrefixedMessage(clientConn)
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	// Wait for write to complete
	if writeErr := <-done; writeErr != nil {
		t.Fatalf("WriteCommissioningError failed: %v", writeErr)
	}

	errMsg, ok := msg.(*commissioning.CommissioningError)
	if !ok {
		t.Fatalf("Expected *CommissioningError, got %T", msg)
	}

	if errMsg.MsgType != commissioning.MsgCommissioningError {
		t.Errorf("MsgType: expected %d, got %d", commissioning.MsgCommissioningError, errMsg.MsgType)
	}
	if errMsg.ErrorCode != commissioning.ErrCodeBusy {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeBusy, errMsg.ErrorCode)
	}
	if errMsg.Message != "device busy" {
		t.Errorf("Message: expected 'device busy', got '%s'", errMsg.Message)
	}
	if errMsg.RetryAfter != 5000 {
		t.Errorf("RetryAfter: expected 5000, got %d", errMsg.RetryAfter)
	}
}

// TestWriteCommissioningError_ZeroRetryAfter verifies that RetryAfter=0
// round-trips correctly.
func TestWriteCommissioningError_ZeroRetryAfter(t *testing.T) {
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- commissioning.WriteCommissioningError(serverConn, commissioning.ErrCodeBusy, "zones full", 0)
	}()

	msg, err := readLengthPrefixedMessage(clientConn)
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	if writeErr := <-done; writeErr != nil {
		t.Fatalf("WriteCommissioningError failed: %v", writeErr)
	}

	errMsg := msg.(*commissioning.CommissioningError)
	if errMsg.RetryAfter != 0 {
		t.Errorf("RetryAfter: expected 0, got %d", errMsg.RetryAfter)
	}
}

// TestWriteCommissioningError_NonBusyCode verifies WriteCommissioningError
// works with any error code, not just ErrCodeBusy.
func TestWriteCommissioningError_NonBusyCode(t *testing.T) {
	clientConn, serverConn := newMockConnPair()
	defer clientConn.Close()
	defer serverConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- commissioning.WriteCommissioningError(serverConn, commissioning.ErrCodeAuthFailed, "auth failed", 0)
	}()

	msg, err := readLengthPrefixedMessage(clientConn)
	if err != nil {
		t.Fatalf("Failed to read message: %v", err)
	}

	if writeErr := <-done; writeErr != nil {
		t.Fatalf("WriteCommissioningError failed: %v", writeErr)
	}

	errMsg := msg.(*commissioning.CommissioningError)
	if errMsg.ErrorCode != commissioning.ErrCodeAuthFailed {
		t.Errorf("ErrorCode: expected %d, got %d", commissioning.ErrCodeAuthFailed, errMsg.ErrorCode)
	}
	if errMsg.Message != "auth failed" {
		t.Errorf("Message: expected 'auth failed', got '%s'", errMsg.Message)
	}
}

// readLengthPrefixedMessage reads a length-prefixed CBOR message from a conn.
// This mirrors the internal readMessage() wire format.
func readLengthPrefixedMessage(conn net.Conn) (interface{}, error) {
	// Read 4-byte length prefix
	length := make([]byte, 4)
	if _, err := io.ReadFull(conn, length); err != nil {
		return nil, err
	}

	msgLen := uint32(length[0])<<24 | uint32(length[1])<<16 | uint32(length[2])<<8 | uint32(length[3])
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	return commissioning.DecodePASEMessage(data)
}

// TestVerifierPersistence verifies verifier can be serialized and restored.
// DEC-084 §2.3 stores (discriminator, salt, w0, L); the post-DEC-084 Verifier
// struct now persists those plus iterations for forward compatibility.
func TestVerifierPersistence(t *testing.T) {
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	// Generate verifier
	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	// Serialize
	data, err := commissioning.MarshalVerifier(verifier)
	if err != nil {
		t.Fatalf("Failed to marshal verifier: %v", err)
	}

	// Restore
	restored, err := commissioning.UnmarshalVerifier(data)
	if err != nil {
		t.Fatalf("Failed to unmarshal verifier: %v", err)
	}

	// Verify equality across every persisted field.
	if restored.Discriminator != verifier.Discriminator {
		t.Errorf("Discriminator mismatch: got %d, want %d", restored.Discriminator, verifier.Discriminator)
	}
	if !bytes.Equal(verifier.Salt, restored.Salt) {
		t.Error("Salt mismatch after restore")
	}
	if !bytes.Equal(verifier.W0, restored.W0) {
		t.Error("W0 mismatch after restore")
	}
	if !bytes.Equal(verifier.L, restored.L) {
		t.Error("L mismatch after restore")
	}
	if restored.Iterations != verifier.Iterations {
		t.Errorf("Iterations mismatch: got %d, want %d", restored.Iterations, verifier.Iterations)
	}
}
