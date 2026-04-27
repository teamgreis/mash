// Package service: controller-side commissioning cert install (DEC-085).
//
// After PASE completes on the commissioning ALPN, the controller issues an
// operational certificate to the device using the **commissioning** envelope
// MsgTypes 10 / 11 / 12 / 13 / 20. This is the spec-strict flow per
// `docs/message-types.md §3` and `docs/testing/behavior/commissioning-pase.md
// §4.3`. It is distinct from in-session renewal (MsgTypes 30–33 on
// operational ALPN `mash/1`), which lives in `controller_renewal.go`.
//
// Pre-DEC-085, mash-go (incorrectly) drove the renewal envelope on the
// commissioning connection — see `mash-py-spec-issues.md` Issue #7.

package service

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"fmt"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// PerformCommissioningCertInstall drives the post-PASE certificate-install
// flow on the controller side using commissioning MsgTypes 10/11/12/13/20.
//
// Wire sequence (sync; no message loop yet):
//
//  1. Controller → Device: CSRRequest{nonce: 32 random bytes}                     (MsgType 10)
//  2. Device     → Controller: CSRResponse{csr, errorCode}                        (MsgType 11)
//  3. Controller → Device: CertInstall{operationalCert, caCert, zoneType, prio}   (MsgType 12)
//  4. Device     → Controller: CertInstallResponse{errorCode}                     (MsgType 13)
//  5. Device     → Controller: CommissioningComplete{}                            (MsgType 20)
//
// On any non-zero error code from the device, the function returns an error
// and does NOT wait for `CommissioningComplete` (the device skips it on the
// failure path per spec). On success the function returns the signed
// operational certificate; the caller is responsible for tearing down the
// commissioning connection and reconnecting on the operational ALPN.
func PerformCommissioningCertInstall(
	ctx context.Context,
	zoneCA *cert.ZoneCA,
	syncConn SyncConnection,
	zoneType cert.ZoneType,
	zonePriority uint8,
) (*x509.Certificate, error) {
	if zoneCA == nil || zoneCA.PrivateKey == nil || zoneCA.Certificate == nil {
		return nil, fmt.Errorf("zone CA is required")
	}

	// Step 1: send CSRRequest with a fresh 32-byte nonce.
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	req := &commissioning.CSRRequest{
		MsgType: commissioning.MsgCSRRequest,
		Nonce:   nonce,
	}
	reqData, err := commissioning.EncodePASEMessage(req)
	if err != nil {
		return nil, fmt.Errorf("encode CSRRequest: %w", err)
	}
	if err := syncConn.Send(reqData); err != nil {
		return nil, fmt.Errorf("send CSRRequest: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Step 2: read CSRResponse.
	csrFrame, err := syncConn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read CSRResponse: %w", err)
	}
	csrMsg, err := decodeCommissioningOrError(csrFrame, "CSRResponse")
	if err != nil {
		return nil, err
	}
	csrResp, ok := csrMsg.(*commissioning.CSRResponse)
	if !ok {
		return nil, fmt.Errorf("expected CSRResponse, got %T", csrMsg)
	}
	if csrResp.ErrorCode != commissioning.ErrCodeSuccess {
		return nil, fmt.Errorf("CSRResponse error: %s (code %d)",
			commissioning.ErrorCodeString(csrResp.ErrorCode), csrResp.ErrorCode)
	}
	if len(csrResp.CSR) == 0 {
		return nil, fmt.Errorf("CSRResponse: empty CSR")
	}

	// Sign the CSR with a controller-assigned device ID derived from the
	// CSR public key (Matter pattern; preserved from the prior implementation).
	deviceID, err := cert.GenerateDeviceID(csrResp.CSR)
	if err != nil {
		return nil, fmt.Errorf("derive device ID from CSR: %w", err)
	}
	newCert, err := cert.SignCSRWithDeviceID(zoneCA, csrResp.CSR, deviceID)
	if err != nil {
		return nil, fmt.Errorf("sign CSR: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Step 3: send CertInstall with operational cert + Zone CA + zone metadata.
	install := &commissioning.CertInstall{
		MsgType:         commissioning.MsgCertInstall,
		OperationalCert: newCert.Raw,
		CACert:          zoneCA.Certificate.Raw,
		ZoneType:        uint8(zoneType),
		ZonePriority:    zonePriority,
	}
	installData, err := commissioning.EncodePASEMessage(install)
	if err != nil {
		return nil, fmt.Errorf("encode CertInstall: %w", err)
	}
	if err := syncConn.Send(installData); err != nil {
		return nil, fmt.Errorf("send CertInstall: %w", err)
	}

	// Step 4: read CertInstallResponse.
	respFrame, err := syncConn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read CertInstallResponse: %w", err)
	}
	respMsg, err := decodeCommissioningOrError(respFrame, "CertInstallResponse")
	if err != nil {
		return nil, err
	}
	resp, ok := respMsg.(*commissioning.CertInstallResponse)
	if !ok {
		return nil, fmt.Errorf("expected CertInstallResponse, got %T", respMsg)
	}
	if resp.ErrorCode != commissioning.ErrCodeSuccess {
		return nil, fmt.Errorf("CertInstall rejected: %s (code %d)",
			commissioning.ErrorCodeString(resp.ErrorCode), resp.ErrorCode)
	}

	// Step 5: read CommissioningComplete.
	completeFrame, err := syncConn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read CommissioningComplete: %w", err)
	}
	completeMsg, err := decodeCommissioningOrError(completeFrame, "CommissioningComplete")
	if err != nil {
		return nil, err
	}
	if _, ok := completeMsg.(*commissioning.CommissioningComplete); !ok {
		return nil, fmt.Errorf("expected CommissioningComplete, got %T", completeMsg)
	}

	return newCert, nil
}

// decodeCommissioningOrError decodes a commissioning-plane CBOR frame and
// surfaces a CommissioningError (MsgType 255) as a typed Go error so
// callers can react with ordinary error handling.
func decodeCommissioningOrError(data []byte, phase string) (any, error) {
	msg, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", phase, err)
	}
	if cerr, ok := msg.(*commissioning.CommissioningError); ok {
		retry := ""
		if cerr.RetryAfter > 0 {
			retry = fmt.Sprintf(" (retry after %d ms)", cerr.RetryAfter)
		}
		return nil, fmt.Errorf("%s: commissioning error: %s (code %d)%s",
			phase, cerr.Message, cerr.ErrorCode, retry)
	}
	return msg, nil
}
