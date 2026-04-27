// Command run_commission_client drives a full mash-go commissioning flow
// (PASE handshake + cert install) against a host:port speaking the
// commissioning ALPN. Used by mash-py's Phase 14b interop test to verify that
// a Python `PASEServer` + `CertExchangeServer` pair accepts a real Go
// controller's end-to-end commissioning conversation.
//
// Scope is the *whole* commissioning pipeline minus mDNS discovery (Phase 18):
// TLS dial → PASE → CSRRequest → CSRResponse → CertInstall →
// CertInstallResponse → CommissioningComplete. It does NOT do operational
// reconnect — that's Phase 15's interop scope.
//
// The helper generates a fresh Zone CA per invocation, signs the device's CSR
// against it, and emits the resulting operational cert + Zone CA fingerprints
// as JSON so the Python test can assert byte-for-byte agreement.
//
// Run:
//
//	go run ./tools/run_commission_client \
//	    --target 127.0.0.1:8444 \
//	    --setup-code 20202021 \
//	    --discriminator 3840 \
//	    --zone-name phase14b \
//	    --zone-type 2 \
//	    --zone-priority 1
//
// Output (stable JSON, single line):
//
//	{
//	  "k_main_hex": "...",
//	  "device_id": "...",
//	  "operational_cert_der_hex": "...",
//	  "zone_ca_der_hex": "...",
//	  "zone_type": 2,
//	  "zone_priority": 1
//	}
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

type output struct {
	KMainHex            string `json:"k_main_hex"`
	DeviceID            string `json:"device_id"`
	OperationalCertHex  string `json:"operational_cert_der_hex"`
	ZoneCADERHex        string `json:"zone_ca_der_hex"`
	ZoneType            uint8  `json:"zone_type"`
	ZonePriority        uint8  `json:"zone_priority"`
}

func main() {
	target := flag.String("target", "", "host:port to dial (required)")
	setupCodeStr := flag.String("setup-code", "", "8-digit setup code (required)")
	discriminator := flag.Uint("discriminator", 0, "12-bit discriminator (0..4095)")
	zoneName := flag.String("zone-name", "phase14b-test", "Zone name for the freshly-generated CA")
	zoneType := flag.Uint("zone-type", 2, "Zone type: 1=GRID, 2=LOCAL, 3=TEST")
	zonePriority := flag.Uint("zone-priority", 1, "Zone priority (1..255)")
	timeoutS := flag.Float64("timeout", 60.0, "overall timeout in seconds")
	flag.Parse()

	if *target == "" {
		fail("--target is required")
	}
	if *setupCodeStr == "" {
		fail("--setup-code is required")
	}
	if *discriminator >= 1<<12 {
		fail(fmt.Sprintf("--discriminator %d exceeds 12 bits", *discriminator))
	}
	if *zoneType < 1 || *zoneType > 3 {
		fail(fmt.Sprintf("--zone-type %d not in {1,2,3}", *zoneType))
	}
	if *zonePriority < 1 || *zonePriority > 255 {
		fail(fmt.Sprintf("--zone-priority %d not in [1, 255]", *zonePriority))
	}

	setupCode, err := commissioning.ParseSetupCode(*setupCodeStr)
	if err != nil {
		fail(fmt.Sprintf("invalid setup code: %v", err))
	}

	// Generate a fresh Zone CA for this commissioning attempt. mash-go's
	// `cert.GenerateZoneCA` produces an ECDSA P-256 CA cert + key with the
	// proper extensions for signing operational certs.
	zoneCA, err := cert.GenerateZoneCA(*zoneName, cert.ZoneType(*zoneType))
	if err != nil {
		fail(fmt.Sprintf("generate Zone CA: %v", err))
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), time.Duration(*timeoutS*float64(time.Second)),
	)
	defer cancel()

	// TLS commissioning client config.
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // device cert is self-signed during commissioning
		NextProtos:         []string{"mash-comm/1"},
	}

	dialer := &net.Dialer{Timeout: time.Duration(*timeoutS * float64(time.Second))}
	conn, err := tls.DialWithDialer(dialer, "tcp", *target, tlsConfig)
	if err != nil {
		fail(fmt.Sprintf("tls dial failed: %v", err))
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if state.NegotiatedProtocol != "mash-comm/1" {
		fail(fmt.Sprintf("unexpected negotiated ALPN: %q (want mash-comm/1)",
			state.NegotiatedProtocol))
	}

	// --- PASE ----------------------------------------------------------
	paseSession, err := commissioning.NewPASEClientSession(setupCode, uint16(*discriminator))
	if err != nil {
		fail(fmt.Sprintf("create PASE client session: %v", err))
	}
	kMain, err := paseSession.Handshake(ctx, conn)
	if err != nil {
		fail(fmt.Sprintf("PASE handshake failed: %v", err))
	}

	// --- Cert install --------------------------------------------------
	// Mirror the controller-side cert install flow used by mash-controller
	// (which lives in pkg/cem and isn't exposed as a tidy helper). We
	// drive the four messages manually using the commissioning package's
	// Encode/Decode primitives + a cheap framed-write/read pair.

	// 1. CSRRequest with a fresh 32-byte nonce. Spec §4.3 mandates exactly
	// 32 bytes; mash-go validates this length on the device side.
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		fail(fmt.Sprintf("read random nonce: %v", err))
	}
	csrReq := &commissioning.CSRRequest{
		MsgType: commissioning.MsgCSRRequest,
		Nonce:   nonce,
	}
	if err := writeFramed(conn, csrReq); err != nil {
		fail(fmt.Sprintf("send CSRRequest: %v", err))
	}

	// 2. Read CSRResponse.
	csrRespMsg, err := readFramed(conn)
	if err != nil {
		fail(fmt.Sprintf("read CSRResponse: %v", err))
	}
	csrResp, ok := csrRespMsg.(*commissioning.CSRResponse)
	if !ok {
		fail(fmt.Sprintf("expected CSRResponse, got %T", csrRespMsg))
	}
	if csrResp.ErrorCode != commissioning.ErrCodeSuccess {
		fail(fmt.Sprintf("CSRResponse error_code=%d", csrResp.ErrorCode))
	}

	// 3. Sign the CSR with our fresh Zone CA.
	deviceID, err := cert.GenerateDeviceID(csrResp.CSR)
	if err != nil {
		fail(fmt.Sprintf("derive device ID from CSR: %v", err))
	}
	opCert, err := cert.SignCSRWithDeviceID(zoneCA, csrResp.CSR, deviceID)
	if err != nil {
		fail(fmt.Sprintf("sign CSR: %v", err))
	}
	opCertDER := opCert.Raw

	// 4. CertInstall.
	install := &commissioning.CertInstall{
		MsgType:         commissioning.MsgCertInstall,
		OperationalCert: opCertDER,
		CACert:          zoneCA.Certificate.Raw,
		ZoneType:        uint8(*zoneType),
		ZonePriority:    uint8(*zonePriority),
	}
	if err := writeFramed(conn, install); err != nil {
		fail(fmt.Sprintf("send CertInstall: %v", err))
	}

	// 5. Read CertInstallResponse.
	respMsg, err := readFramed(conn)
	if err != nil {
		fail(fmt.Sprintf("read CertInstallResponse: %v", err))
	}
	resp, ok := respMsg.(*commissioning.CertInstallResponse)
	if !ok {
		fail(fmt.Sprintf("expected CertInstallResponse, got %T", respMsg))
	}
	if resp.ErrorCode != commissioning.ErrCodeSuccess {
		fail(fmt.Sprintf("CertInstallResponse error_code=%d", resp.ErrorCode))
	}

	// 6. Read CommissioningComplete (terminator).
	completeMsg, err := readFramed(conn)
	if err != nil {
		fail(fmt.Sprintf("read CommissioningComplete: %v", err))
	}
	if _, ok := completeMsg.(*commissioning.CommissioningComplete); !ok {
		fail(fmt.Sprintf("expected CommissioningComplete, got %T", completeMsg))
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(output{
		KMainHex:           hex.EncodeToString(kMain),
		DeviceID:           deviceID,
		OperationalCertHex: hex.EncodeToString(opCertDER),
		ZoneCADERHex:       hex.EncodeToString(zoneCA.Certificate.Raw),
		ZoneType:           uint8(*zoneType),
		ZonePriority:       uint8(*zonePriority),
	}); err != nil {
		fail(fmt.Sprintf("encoding output: %v", err))
	}
}

// writeFramed encodes a commissioning message and writes it with the standard
// 4-byte big-endian length prefix used by the wire framer.
func writeFramed(conn net.Conn, msg interface{}) error {
	data, err := commissioning.EncodePASEMessage(msg)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(data)))
	if _, err := conn.Write(hdr); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}

// readFramed reads one length-prefixed commissioning message from conn.
func readFramed(conn net.Conn) (interface{}, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr)
	if n == 0 || n > 65536 {
		return nil, fmt.Errorf("frame length out of range: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return commissioning.DecodePASEMessage(buf)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
