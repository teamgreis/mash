package service

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// ControllerRenewalHandler handles certificate renewal on the controller side.
// It orchestrates the renewal protocol: sending requests, signing CSRs,
// and installing new certificates.
type ControllerRenewalHandler struct {
	mu sync.Mutex

	// zoneCA is the Zone CA used to sign device certificates.
	zoneCA *cert.ZoneCA

	// conn is the connection to the device.
	conn Sendable

	// pendingCSR holds the CSR received from the device during renewal.
	pendingCSR []byte

	// pendingDeviceID is the device currently being renewed.
	pendingDeviceID string

	// certSequence is the next certificate sequence number.
	certSequence uint32

	// responseWait is used to wait for async responses.
	responseWait chan any
}

func decodeRenewalOrCommissioningError(data []byte, phase string) (any, error) {
	msg, err := commissioning.DecodeRenewalMessage(data)
	if err == nil {
		return msg, nil
	}

	// Device may send a commissioning error (msgType=255) during the renewal
	// exchange when zone validation rejects the attempt before CSR.
	if genericMsg, genericErr := commissioning.DecodePASEMessage(data); genericErr == nil {
		if cerr, ok := genericMsg.(*commissioning.CommissioningError); ok {
			return nil, fmt.Errorf("%s: commissioning error (%s, code %d)", phase, cerr.Message, cerr.ErrorCode)
		}
	}

	return nil, fmt.Errorf("decode %s: %w", phase, err)
}

// NewControllerRenewalHandler creates a new ControllerRenewalHandler.
func NewControllerRenewalHandler(zoneCA *cert.ZoneCA, conn Sendable) *ControllerRenewalHandler {
	return &ControllerRenewalHandler{
		zoneCA:       zoneCA,
		conn:         conn,
		certSequence: 1, // Start at 1
		responseWait: make(chan any, 1),
	}
}

// InitiateRenewal starts the renewal process by sending a CertRenewalRequest.
// It waits for the CSR response from the device.
func (h *ControllerRenewalHandler) InitiateRenewal(ctx context.Context, deviceID string) error {
	h.mu.Lock()
	h.pendingDeviceID = deviceID
	h.pendingCSR = nil
	h.mu.Unlock()

	// Generate 32-byte nonce
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	// Create and send renewal request
	req := &commissioning.CertRenewalRequest{
		MsgType: commissioning.MsgCertRenewalRequest,
		Nonce:   nonce,
	}

	data, err := cbor.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	if err := h.conn.Send(data); err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	// Wait for CSR response
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-h.responseWait:
		csrResp, ok := resp.(*commissioning.CertRenewalCSR)
		if !ok {
			return fmt.Errorf("unexpected response type: %T", resp)
		}
		h.mu.Lock()
		h.pendingCSR = csrResp.CSR
		h.mu.Unlock()
		return nil
	}
}

// RenewDevice performs the complete renewal flow for a device.
// It sends a renewal request, signs the CSR, and installs the new certificate.
func (h *ControllerRenewalHandler) RenewDevice(ctx context.Context, deviceID string) (*x509.Certificate, error) {
	// Step 1: Initiate renewal (sends request, waits for CSR)
	if err := h.InitiateRenewal(ctx, deviceID); err != nil {
		return nil, fmt.Errorf("initiate renewal: %w", err)
	}

	// Step 2: Sign the CSR
	h.mu.Lock()
	csrDER := h.pendingCSR
	h.mu.Unlock()

	if csrDER == nil {
		return nil, fmt.Errorf("no CSR received")
	}

	newCert, err := cert.SignCSR(h.zoneCA, csrDER)
	if err != nil {
		return nil, fmt.Errorf("sign CSR: %w", err)
	}

	// Step 3: Send the new certificate
	h.mu.Lock()
	h.certSequence++
	seq := h.certSequence
	h.mu.Unlock()

	install := &commissioning.CertRenewalInstall{
		MsgType:  commissioning.MsgCertRenewalInstall,
		NewCert:  newCert.Raw,
		Sequence: seq,
	}

	data, err := cbor.Marshal(install)
	if err != nil {
		return nil, fmt.Errorf("encode install: %w", err)
	}

	if err := h.conn.Send(data); err != nil {
		return nil, fmt.Errorf("send install: %w", err)
	}

	// Step 4: Wait for acknowledgment
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-h.responseWait:
		ack, ok := resp.(*commissioning.CertRenewalAck)
		if !ok {
			return nil, fmt.Errorf("unexpected response type: %T", resp)
		}
		if ack.Status != commissioning.RenewalStatusSuccess {
			return nil, fmt.Errorf("renewal failed with status %d", ack.Status)
		}
		return newCert, nil
	}
}

// HandleResponse processes a response message from the device.
// This should be called when a renewal message is received.
func (h *ControllerRenewalHandler) HandleResponse(msg any) {
	select {
	case h.responseWait <- msg:
	default:
		// Channel full, discard
	}
}

// PendingCSR returns the CSR received from the device, if any.
func (h *ControllerRenewalHandler) PendingCSR() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pendingCSR
}

// CertSequence returns the current certificate sequence number.
func (h *ControllerRenewalHandler) CertSequence() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.certSequence
}
