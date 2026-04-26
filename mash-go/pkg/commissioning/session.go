package commissioning

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/mash-protocol/mash-go/pkg/transport"
)

// PASEClientSession manages the client side of a PASE handshake.
//
// Per DEC-084 §2.4 the SPAKE2+ identity strings are protocol constants
// (b"mash-controller" / b"mash-device") and need not be passed in. The
// discriminator is required because it participates in salt derivation
// (DEC-084 §2.3).
type PASEClientSession struct {
	spake *SPAKE2PlusClient
}

// NewPASEClientSession creates a new PASE client session.
func NewPASEClientSession(setupCode SetupCode, discriminator uint16) (*PASEClientSession, error) {
	spake, err := NewSPAKE2PlusClient(setupCode, discriminator)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPAKE2+ client: %w", err)
	}

	return &PASEClientSession{spake: spake}, nil
}

// ClientIdentity returns the protocol-constant controller identity.
// DEC-084 §2.4 fixes this to b"mash-controller".
func (s *PASEClientSession) ClientIdentity() []byte {
	out := make([]byte, len(ControllerIdentity))
	copy(out, ControllerIdentity)
	return out
}

// Handshake performs the PASE handshake and returns the derived session key.
// The connection should be a TLS connection in commissioning mode.
func (s *PASEClientSession) Handshake(ctx context.Context, conn net.Conn) ([]byte, error) {
	// Step 1: Send PASERequest with our public value. ClientIdentity is the
	// protocol-constant b"mash-controller" per DEC-084 §2.4 / §4.2.
	req := &PASERequest{
		MsgType:        MsgPASERequest,
		PublicValue:    s.spake.PublicValue(),
		ClientIdentity: ControllerIdentity,
	}

	if err := writeMessage(conn, req); err != nil {
		return nil, fmt.Errorf("failed to send PASE request: %w", err)
	}

	// Step 2: Read PASEResponse
	msg, err := readMessageWithContext(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read PASE response: %w", err)
	}

	resp, ok := msg.(*PASEResponse)
	if !ok {
		if errMsg, ok := msg.(*CommissioningError); ok {
			return nil, fmt.Errorf("server error: %s (code %d)", errMsg.Message, errMsg.ErrorCode)
		}
		return nil, fmt.Errorf("unexpected message type: expected PASEResponse, got %T", msg)
	}

	// Step 3: Process server's public value
	if err := s.spake.ProcessServerValue(resp.PublicValue); err != nil {
		return nil, fmt.Errorf("failed to process server value: %w", err)
	}

	// Step 4: Send PASEConfirm with our confirmation
	confirm := &PASEConfirm{
		MsgType:      MsgPASEConfirm,
		Confirmation: s.spake.Confirmation(),
	}

	if err := writeMessage(conn, confirm); err != nil {
		return nil, fmt.Errorf("failed to send PASE confirm: %w", err)
	}

	// Step 5: Read PASEComplete
	msg, err = readMessageWithContext(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read PASE complete: %w", err)
	}

	complete, ok := msg.(*PASEComplete)
	if !ok {
		if errMsg, ok := msg.(*CommissioningError); ok {
			return nil, fmt.Errorf("server error: %s (code %d)", errMsg.Message, errMsg.ErrorCode)
		}
		return nil, fmt.Errorf("unexpected message type: expected PASEComplete, got %T", msg)
	}

	// Check for error
	if complete.ErrorCode != ErrCodeSuccess {
		return nil, fmt.Errorf("PASE failed: error code %d", complete.ErrorCode)
	}

	// Step 6: Verify server's confirmation
	if err := s.spake.VerifyServerConfirmation(complete.Confirmation); err != nil {
		return nil, fmt.Errorf("server confirmation failed: %w", err)
	}

	return s.spake.SharedSecret(), nil
}

// PASEServerSession manages the server side of a PASE handshake.
type PASEServerSession struct {
	verifier *Verifier
	spake    *SPAKE2PlusServer

	// PhaseTimeout, if non-zero, overrides the parent context deadline for
	// individual read phases (e.g. waiting for PASEConfirm after sending
	// PASEResponse). This implements per-phase timeouts as required by the
	// spec (PASE_WAIT_V = 30s). When zero, the parent context governs.
	PhaseTimeout time.Duration
}

// NewPASEServerSession creates a new PASE server session. Identity strings
// are protocol constants per DEC-084 §2.4 and need not be passed.
func NewPASEServerSession(verifier *Verifier) (*PASEServerSession, error) {
	spake, err := NewSPAKE2PlusServer(verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPAKE2+ server: %w", err)
	}

	return &PASEServerSession{
		verifier: verifier,
		spake:    spake,
	}, nil
}

// WaitForPASERequest reads the first message from the connection and validates
// that it is a PASERequest. This method does not acquire any lock and is
// intended to be called before the commissioning lock is held (DEC-061).
// The returned *PASERequest should be passed to CompleteHandshake.
func (s *PASEServerSession) WaitForPASERequest(ctx context.Context, conn net.Conn) (*PASERequest, error) {
	msg, err := readMessageWithContext(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read PASE request: %w", err)
	}

	req, ok := msg.(*PASERequest)
	if !ok {
		return nil, fmt.Errorf("unexpected message type: expected PASERequest, got %T", msg)
	}

	return req, nil
}

// CompleteHandshake processes the PASE exchange after the initial PASERequest
// has been received. It performs steps 2-6 of the PASE protocol and returns
// the derived shared secret. This method should be called with the
// commissioning lock held (DEC-061).
func (s *PASEServerSession) CompleteHandshake(ctx context.Context, conn net.Conn, req *PASERequest) ([]byte, error) {
	// DEC-047: Record start time for constant-time error responses.
	// All error paths pad to a minimum floor to prevent timing oracle attacks
	// that could distinguish invalid_pubkey (early exit) from wrong_password
	// (full handshake).
	const minHandshakeDuration = 200 * time.Millisecond
	handshakeStart := time.Now()

	// DEC-084 §4.2 / §2.5: ClientIdentity in PASERequest MUST equal
	// b"mash-controller" in v1; reserved for future delegated-commissioning
	// use (DEC-019). We treat a wrong identity as ErrCodeAuthFailed and
	// return a generic error (DEC-047) — never leak which check failed.
	//
	// Spec §2.5 mandates PASEComplete (NOT CommissioningError) for every
	// in-PASE failure mode. CommissioningError is reserved for cross-cutting
	// rejections (DEC-063 device-busy, etc.) — see security.md §13. Mixing
	// the two envelopes weakens the structural-oracle-resistance property
	// DEC-047 is supposed to guarantee, since a peer could otherwise
	// distinguish "wrong identity" (CommissioningError) from "wrong setup
	// code" (PASEComplete) by message type alone. Confirmation is
	// zero-padded on the failure path: the SPAKE2 server has not (and on
	// these paths cannot) compute confirmB.
	if !bytes.Equal(req.ClientIdentity, ControllerIdentity) {
		if elapsed := time.Since(handshakeStart); elapsed < minHandshakeDuration {
			time.Sleep(minHandshakeDuration - elapsed)
		}
		writeMessage(conn, &PASEComplete{
			MsgType:      MsgPASEComplete,
			Confirmation: make([]byte, 32),
			ErrorCode:    ErrCodeAuthFailed,
		})
		return nil, fmt.Errorf("client identity rejected")
	}

	// Step 2: Process client's public value
	if err := s.spake.ProcessClientValue(req.PublicValue); err != nil {
		// Pad to minimum duration BEFORE sending the error response.
		// This keeps the client blocked (waiting for a message) during the
		// padding period, which prevents timing oracle attacks and ensures the
		// commissioning lock stays held so the next attempt doesn't overlap.
		if elapsed := time.Since(handshakeStart); elapsed < minHandshakeDuration {
			time.Sleep(minHandshakeDuration - elapsed)
		}
		// Spec §2.5: PASEComplete{ErrCodeAuthFailed} (not CommissioningError)
		// on point-validation / SPAKE2 invariant failure. See the wrong-
		// identity comment above for the envelope-choice rationale.
		writeMessage(conn, &PASEComplete{
			MsgType:      MsgPASEComplete,
			Confirmation: make([]byte, 32),
			ErrorCode:    ErrCodeAuthFailed,
		})
		return nil, fmt.Errorf("failed to process client value: %w", err)
	}

	// Step 3: Send PASEResponse with our public value
	resp := &PASEResponse{
		MsgType:     MsgPASEResponse,
		PublicValue: s.spake.PublicValue(),
	}

	if err := writeMessage(conn, resp); err != nil {
		return nil, fmt.Errorf("failed to send PASE response: %w", err)
	}

	// Step 4: Read PASEConfirm with per-phase timeout if configured.
	readCtx := ctx
	if s.PhaseTimeout > 0 {
		var phaseCancel context.CancelFunc
		readCtx, phaseCancel = context.WithTimeout(ctx, s.PhaseTimeout)
		defer phaseCancel()
	}
	msg, err := readMessageWithContext(readCtx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to read PASE confirm: %w", err)
	}

	confirm, ok := msg.(*PASEConfirm)
	if !ok {
		return nil, fmt.Errorf("unexpected message type: expected PASEConfirm, got %T", msg)
	}

	// Step 5: Verify client's confirmation
	//
	// DEC-047 / spec §2.5: every in-PASE failure mode collapses to a single
	// generic auth-failure code. Pre-DEC-047 mash-go used ErrCodeConfirmFailed
	// here, which leaked "this was a MAC mismatch, not point validation"
	// information to the peer. ErrCodeAuthFailed is correct.
	var errorCode uint8 = ErrCodeSuccess
	if err := s.spake.VerifyClientConfirmation(confirm.Confirmation); err != nil {
		errorCode = ErrCodeAuthFailed
	}

	// Step 6: Send PASEComplete with our confirmation.
	// On failure, pad BEFORE sending so the client stays blocked during the
	// padding period (same rationale as the invalid_pubkey path above).
	if errorCode != ErrCodeSuccess {
		if elapsed := time.Since(handshakeStart); elapsed < minHandshakeDuration {
			time.Sleep(minHandshakeDuration - elapsed)
		}
	}

	complete := &PASEComplete{
		MsgType:      MsgPASEComplete,
		Confirmation: s.spake.Confirmation(),
		ErrorCode:    errorCode,
	}

	if err := writeMessage(conn, complete); err != nil {
		return nil, fmt.Errorf("failed to send PASE complete: %w", err)
	}

	if errorCode != ErrCodeSuccess {
		return nil, fmt.Errorf("client confirmation failed: error code %d", errorCode)
	}

	return s.spake.SharedSecret(), nil
}

// Handshake performs the PASE handshake and returns the derived session key.
// This is a convenience wrapper that calls WaitForPASERequest followed by
// CompleteHandshake. For message-gated locking (DEC-061), use the split
// methods directly.
func (s *PASEServerSession) Handshake(ctx context.Context, conn net.Conn) ([]byte, error) {
	req, err := s.WaitForPASERequest(ctx, conn)
	if err != nil {
		return nil, err
	}
	return s.CompleteHandshake(ctx, conn, req)
}

// MarshalVerifier serializes a verifier to CBOR bytes for storage.
func MarshalVerifier(v *Verifier) ([]byte, error) {
	return cbor.Marshal(v)
}

// UnmarshalVerifier deserializes a verifier from CBOR bytes.
func UnmarshalVerifier(data []byte) (*Verifier, error) {
	var v Verifier
	if err := cbor.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// WriteCommissioningError sends a CommissioningError message over a connection.
// This is used to inform the peer of rejection reasons (e.g., device busy)
// before closing the connection. (DEC-063)
func WriteCommissioningError(conn net.Conn, code uint8, message string, retryAfterMs uint32) error {
	errMsg := &CommissioningError{
		MsgType:    MsgCommissioningError,
		ErrorCode:  code,
		Message:    message,
		RetryAfter: retryAfterMs,
	}
	return writeMessage(conn, errMsg)
}

// Wire protocol helpers

// writeMessage writes a length-prefixed CBOR message to the connection.
func writeMessage(conn net.Conn, msg interface{}) error {
	data, err := cbor.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	// Write length prefix (4 bytes, big-endian)
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))

	if _, err := conn.Write(length); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}

	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	return nil
}

// readMessage reads a length-prefixed CBOR message from the connection.
func readMessage(conn net.Conn) (interface{}, error) {
	// Read length prefix
	length := make([]byte, 4)
	if _, err := io.ReadFull(conn, length); err != nil {
		return nil, fmt.Errorf("failed to read length: %w", err)
	}

	msgLen := binary.BigEndian.Uint32(length)
	if msgLen > transport.DefaultMaxMessageSize { // Sanity check (DEC-069)
		return nil, fmt.Errorf("message too large: %d bytes", msgLen)
	}

	// Read message data
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	return DecodePASEMessage(data)
}

// readMessageWithContext reads a message with context cancellation support.
// It sets a read deadline on the connection to ensure the read goroutine is
// unblocked when the context expires, preventing goroutine leaks on real
// (TCP/TLS) connections. The goroutine + select pattern is retained as a
// fallback for connections where SetReadDeadline is a no-op (e.g. io.Pipe
// in tests).
func readMessageWithContext(ctx context.Context, conn net.Conn) (interface{}, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		defer conn.SetReadDeadline(time.Time{})
	}

	type result struct {
		msg interface{}
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		msg, err := readMessage(conn)
		resultCh <- result{msg, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-resultCh:
		if r.err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return r.msg, r.err
	}
}
