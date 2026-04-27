package runner

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// pipeReadWriter combines a PipeReader and PipeWriter into an io.ReadWriter.
type pipeReadWriter struct {
	io.Reader
	io.Writer
}

// TestFramerSyncConn_Send verifies the adapter delegates to framer.WriteFrame.
func TestFramerSyncConn_Send(t *testing.T) {
	// Create a bidirectional pipe pair.
	aR, aW := io.Pipe()
	bR, bW := io.Pipe()
	defer aR.Close()
	defer aW.Close()
	defer bR.Close()
	defer bW.Close()

	// Writer side: reads from bR (unused), writes to aW.
	writerFramer := transport.NewFramer(&pipeReadWriter{Reader: bR, Writer: aW})
	sc := &framerSyncConn{framer: writerFramer}

	// Reader side: reads from aR, writes to bW (unused).
	readerFramer := transport.NewFramer(&pipeReadWriter{Reader: aR, Writer: bW})

	payload := []byte("hello")

	go func() {
		if err := sc.Send(payload); err != nil {
			t.Errorf("Send failed: %v", err)
		}
	}()

	got, err := readerFramer.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %v, want %v", got, payload)
	}
}

// TestFramerSyncConn_ReadFrame verifies the adapter delegates to framer.ReadFrame.
func TestFramerSyncConn_ReadFrame(t *testing.T) {
	aR, aW := io.Pipe()
	bR, bW := io.Pipe()
	defer aR.Close()
	defer aW.Close()
	defer bR.Close()
	defer bW.Close()

	writeFramer := transport.NewFramer(&pipeReadWriter{Reader: bR, Writer: aW})
	readFramer := transport.NewFramer(&pipeReadWriter{Reader: aR, Writer: bW})
	sc := &framerSyncConn{framer: readFramer}

	payload := []byte("world")

	go func() {
		if err := writeFramer.WriteFrame(payload); err != nil {
			t.Errorf("WriteFrame failed: %v", err)
		}
	}()

	got, err := sc.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %v, want %v", got, payload)
	}
}

// TestFramerSyncConn_ImplementsSyncConnection verifies the adapter satisfies the interface.
func TestFramerSyncConn_ImplementsSyncConnection(t *testing.T) {
	var _ service.SyncConnection = (*framerSyncConn)(nil)
}

// TestCertExchange_FullFlow simulates the 5-message cert exchange after PASE
// using the spec-strict commissioning envelope (DEC-085): CSRRequest (10) →
// CSRResponse (11) → CertInstall (12) → CertInstallResponse (13) →
// CommissioningComplete (20). It creates a mock device on the other end of a
// pipe that drives the device-side half of that flow.
func TestCertExchange_FullFlow(t *testing.T) {
	// Create two pipes: one for each direction.
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	defer clientR.Close()
	defer serverW.Close()
	defer serverR.Close()
	defer clientW.Close()

	clientFramer := transport.NewFramer(&pipeReadWriter{Reader: clientR, Writer: clientW})
	serverFramer := transport.NewFramer(&pipeReadWriter{Reader: serverR, Writer: serverW})

	// Generate a device key pair for the mock device.
	deviceKeyPair, err := cert.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	// Run mock device in background.
	deviceErr := make(chan error, 1)
	go func() {
		deviceErr <- mockDeviceCertExchange(serverFramer, deviceKeyPair)
	}()

	// Set up runner with a fake PASE session key.
	r := &Runner{
		config: &Config{},
		pool:   NewConnPool(func(string, ...any) {}, nil),
		suite:  NewSuiteSession(),
	}
	r.dialer = NewDialer(false, r.debugf)
	r.connMgr = NewConnectionManager(r.pool, r.suite, r.dialer, r.config, r.debugf, connMgrDeps{})
	r.connMgr.SetPASEState(&PASEState{
		sessionKey: []byte("test-session-key-for-zone-deriv"),
		completed:  true,
	})
	r.pool.SetMain(&Connection{
		framer: clientFramer,
		state:  ConnTLSConnected,
	})

	// Perform cert exchange.
	ctx := context.Background()
	deviceID, err := r.performCertExchange(ctx)
	if err != nil {
		t.Fatalf("performCertExchange: %v", err)
	}

	// Wait for mock device to finish.
	if devErr := <-deviceErr; devErr != nil {
		t.Fatalf("mock device error: %v", devErr)
	}

	// Verify: zoneCA is set.
	if r.connMgr.ZoneCA() == nil {
		t.Error("expected zoneCA to be set")
	}

	// Verify: zoneCAPool is set.
	if r.connMgr.ZoneCAPool() == nil {
		t.Error("expected zoneCAPool to be set")
	}

	// Verify: controllerCert is set and signed by Zone CA.
	if r.connMgr.ControllerCert() == nil {
		t.Fatal("expected controllerCert to be set")
	}
	if r.connMgr.ControllerCert().Certificate == nil {
		t.Fatal("expected controllerCert.Certificate to be set")
	}
	// The controller cert's issuer should match the zone CA's subject.
	if r.connMgr.ControllerCert().Certificate.Issuer.CommonName != r.connMgr.ZoneCA().Certificate.Subject.CommonName {
		t.Errorf("controller cert issuer %q != zone CA subject %q",
			r.connMgr.ControllerCert().Certificate.Issuer.CommonName,
			r.connMgr.ZoneCA().Certificate.Subject.CommonName)
	}

	// Verify: device ID was extracted.
	if deviceID == "" {
		t.Error("expected non-empty device ID")
	}

	// Verify: zone ID derivation.
	expectedZoneID := deriveZoneIDFromSecret([]byte("test-session-key-for-zone-deriv"))
	if r.connMgr.ZoneCA().ZoneID != expectedZoneID {
		t.Errorf("zone ID %q != expected %q", r.connMgr.ZoneCA().ZoneID, expectedZoneID)
	}
}

// TestCertExchange_FailsWhenCommissioningCompleteMissing verifies the
// controller errors when the device acks CertInstall (13) but skips the
// terminator CommissioningComplete (20). Per DEC-085 the controller MUST
// read both before considering the device commissioned.
func TestCertExchange_FailsWhenCommissioningCompleteMissing(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	defer clientR.Close()
	defer serverW.Close()
	defer serverR.Close()
	defer clientW.Close()

	clientFramer := transport.NewFramer(&pipeReadWriter{Reader: clientR, Writer: clientW})
	serverFramer := transport.NewFramer(&pipeReadWriter{Reader: serverR, Writer: serverW})

	deviceKeyPair, err := cert.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	deviceErr := make(chan error, 1)
	go func() {
		deviceErr <- mockDeviceCertExchangeNoComplete(serverFramer, deviceKeyPair)
		_ = serverW.Close()
	}()

	r := &Runner{
		config: &Config{},
		pool:   NewConnPool(func(string, ...any) {}, nil),
		suite:  NewSuiteSession(),
	}
	r.dialer = NewDialer(false, r.debugf)
	r.connMgr = NewConnectionManager(r.pool, r.suite, r.dialer, r.config, r.debugf, connMgrDeps{})
	r.connMgr.SetPASEState(&PASEState{
		sessionKey: []byte("test-session-key-for-zone-deriv"),
		completed:  true,
	})
	r.pool.SetMain(&Connection{
		framer: clientFramer,
		state:  ConnTLSConnected,
	})

	_, err = r.performCertExchange(context.Background())
	if err == nil {
		t.Fatal("expected performCertExchange to fail when CommissioningComplete is missing")
	}
	if !strings.Contains(err.Error(), "CommissioningComplete") {
		t.Fatalf("expected CommissioningComplete error, got %v", err)
	}

	if devErr := <-deviceErr; devErr != nil {
		t.Fatalf("mock device error: %v", devErr)
	}
}

// mockDeviceCertExchange simulates the device side of the spec-strict
// commissioning cert install (DEC-085). It receives CSRRequest (10),
// responds with CSRResponse (11), receives CertInstall (12), and emits
// CertInstallResponse (13) + CommissioningComplete (20).
func mockDeviceCertExchange(framer *transport.Framer, keyPair *cert.KeyPair) error {
	// Step 1: Read CSRRequest (MsgType 10).
	reqData, err := framer.ReadFrame()
	if err != nil {
		return err
	}
	msg, err := commissioning.DecodePASEMessage(reqData)
	if err != nil {
		return err
	}
	req, ok := msg.(*commissioning.CSRRequest)
	if !ok {
		return io.ErrUnexpectedEOF
	}
	if len(req.Nonce) != 32 {
		return io.ErrShortBuffer
	}

	// Step 2: Build PKCS#10 CSR using the device's key pair, send CSRResponse.
	csrDER, err := cert.CreateCSR(keyPair, &cert.CSRInfo{
		Identity: cert.DeviceIdentity{DeviceID: "mock-device"},
	})
	if err != nil {
		return err
	}
	csrResp := &commissioning.CSRResponse{
		MsgType:   commissioning.MsgCSRResponse,
		CSR:       csrDER,
		ErrorCode: commissioning.ErrCodeSuccess,
	}
	csrData, err := commissioning.EncodePASEMessage(csrResp)
	if err != nil {
		return err
	}
	if err := framer.WriteFrame(csrData); err != nil {
		return err
	}

	// Step 3: Read CertInstall (MsgType 12).
	installData, err := framer.ReadFrame()
	if err != nil {
		return err
	}
	msg, err = commissioning.DecodePASEMessage(installData)
	if err != nil {
		return err
	}
	if _, ok := msg.(*commissioning.CertInstall); !ok {
		return io.ErrUnexpectedEOF
	}

	// Step 4: Send CertInstallResponse{Status: 0} (MsgType 13).
	resp := &commissioning.CertInstallResponse{
		MsgType:   commissioning.MsgCertInstallResponse,
		ErrorCode: commissioning.ErrCodeSuccess,
	}
	respData, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		return err
	}
	if err := framer.WriteFrame(respData); err != nil {
		return err
	}

	// Step 5: Send CommissioningComplete (MsgType 20).
	complete := &commissioning.CommissioningComplete{
		MsgType: commissioning.MsgCommissioningComplete,
	}
	completeData, err := commissioning.EncodePASEMessage(complete)
	if err != nil {
		return err
	}
	return framer.WriteFrame(completeData)
}

// mockDeviceCertExchangeNoComplete acks CertInstall (13) but skips the
// CommissioningComplete (20) terminator. Used to verify the controller
// errors out when the spec's required terminator is missing.
func mockDeviceCertExchangeNoComplete(framer *transport.Framer, keyPair *cert.KeyPair) error {
	reqData, err := framer.ReadFrame()
	if err != nil {
		return err
	}
	msg, err := commissioning.DecodePASEMessage(reqData)
	if err != nil {
		return err
	}
	if _, ok := msg.(*commissioning.CSRRequest); !ok {
		return io.ErrUnexpectedEOF
	}

	csrDER, err := cert.CreateCSR(keyPair, &cert.CSRInfo{
		Identity: cert.DeviceIdentity{DeviceID: "mock-device"},
	})
	if err != nil {
		return err
	}
	csrResp := &commissioning.CSRResponse{
		MsgType:   commissioning.MsgCSRResponse,
		CSR:       csrDER,
		ErrorCode: commissioning.ErrCodeSuccess,
	}
	csrData, err := commissioning.EncodePASEMessage(csrResp)
	if err != nil {
		return err
	}
	if err := framer.WriteFrame(csrData); err != nil {
		return err
	}

	installData, err := framer.ReadFrame()
	if err != nil {
		return err
	}
	msg, err = commissioning.DecodePASEMessage(installData)
	if err != nil {
		return err
	}
	if _, ok := msg.(*commissioning.CertInstall); !ok {
		return io.ErrUnexpectedEOF
	}

	// Send CertInstallResponse{Status: 0} but deliberately omit
	// CommissioningComplete to exercise the controller's error path.
	resp := &commissioning.CertInstallResponse{
		MsgType:   commissioning.MsgCertInstallResponse,
		ErrorCode: commissioning.ErrCodeSuccess,
	}
	respData, err := commissioning.EncodePASEMessage(resp)
	if err != nil {
		return err
	}
	return framer.WriteFrame(respData)
}
