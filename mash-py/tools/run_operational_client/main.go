// Command run_operational_client opens an mTLS operational connection to a
// MASH device and exercises the four operational ops (Read / Write / Invoke;
// Subscribe is expected to fail with UNSUPPORTED on a Phase-15 device). It
// is the Phase-15 equivalent of run_commission_client: a focused Go helper
// that drives a single layer of the protocol so the cross-impl test stays
// narrow.
//
// Why a separate helper instead of extending run_commission_client:
// commissioning and operational are two distinct sub-protocols on two
// distinct ports. Combining them would couple the helper to both
// listener lifecycles on the Python side, and a race on the
// commissioning→operational handover would surface as a flaky helper
// rather than a focused Python test failure.
//
// Phase 15 deliberately uses pre-minted certs (passed in as PEM via flags)
// rather than running the commissioning flow to obtain them — commissioning
// interop is already covered by Phase 14b's test. Phase 15 is about the
// operational layer.
//
// Run:
//
//	go run ./run_operational_client \
//	    --target 127.0.0.1:8443 \
//	    --device-id 1A2B3C4D5E6F7A8B \
//	    --controller-cert-pem /tmp/ctrl.crt.pem \
//	    --controller-key-pem /tmp/ctrl.key.pem \
//	    --zone-ca-pem /tmp/zone-ca.pem \
//	    --label-write phase15-test
//
// Output (single-line JSON):
//
//	{
//	  "read_device_info": {"vendorName": "...", "productName": "...", ...},
//	  "write_label": {"echo": "...", "stored": "..."},
//	  "invoke_remove_zone": {"status": 0, "removed": true},
//	  "subscribe_status": 10
//	}
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/mash-protocol/mash-go/pkg/interaction"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// DeviceInfo / Status / Measurement IDs — pinned per
// docs/features/protocol-versions.yaml. We hard-code so the helper has no
// build-time coupling on generated feature constants.
const (
	featureDeviceInfo uint8  = 0x01
	attrVendorName    uint16 = 2
	attrProductName   uint16 = 3
	attrLabel         uint16 = 31

	cmdRemoveZone        uint8 = 0x10
	removeZoneParamZone  uint8 = 1
	removeZoneRespRemove uint8 = 1
)

type output struct {
	ReadDeviceInfo   map[string]any `json:"read_device_info"`
	WriteLabel       writeResult    `json:"write_label"`
	InvokeRemoveZone invokeResult   `json:"invoke_remove_zone"`
	SubscribeStatus  uint8          `json:"subscribe_status"`
}

type writeResult struct {
	Echo   string `json:"echo"`
	Stored string `json:"stored"`
}

type invokeResult struct {
	Status  uint8 `json:"status"`
	Removed bool  `json:"removed"`
}

func main() {
	target := flag.String("target", "", "host:port to dial (required)")
	deviceID := flag.String("device-id", "", "expected device cert CN (server SNI; required)")
	controllerCertPath := flag.String("controller-cert-pem", "", "path to PEM controller op cert (required)")
	controllerKeyPath := flag.String("controller-key-pem", "", "path to PEM controller op key (required)")
	zoneCAPath := flag.String("zone-ca-pem", "", "path to PEM zone CA cert (required)")
	labelWrite := flag.String("label-write", "phase15-helper", "value to write to DeviceInfo.label")
	zoneArg := flag.String("invoke-zone", "phase15-test", "zoneId arg for removeZone invoke")
	timeoutS := flag.Float64("timeout", 30.0, "overall timeout in seconds")
	dialRetryS := flag.Float64("dial-retry", 0.0, "retry connect for up to N seconds (>0 enables retry)")
	flag.Parse()

	for _, p := range []struct {
		name, val string
	}{
		{"--target", *target},
		{"--device-id", *deviceID},
		{"--controller-cert-pem", *controllerCertPath},
		{"--controller-key-pem", *controllerKeyPath},
		{"--zone-ca-pem", *zoneCAPath},
	} {
		if p.val == "" {
			fail(fmt.Sprintf("%s is required", p.name))
		}
	}

	ctlCertPEM := mustRead(*controllerCertPath)
	ctlKeyPEM := mustRead(*controllerKeyPath)
	caPEM := mustRead(*zoneCAPath)

	tlsCfg, err := buildOperationalTLS(ctlCertPEM, ctlKeyPEM, caPEM, *deviceID)
	if err != nil {
		fail(fmt.Sprintf("build TLS config: %v", err))
	}

	overallTO := time.Duration(*timeoutS * float64(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), overallTO)
	defer cancel()

	conn, err := dialWithRetry(ctx, *target, tlsCfg, time.Duration(*dialRetryS*float64(time.Second)))
	if err != nil {
		fail(fmt.Sprintf("dial %s: %v", *target, err))
	}
	defer conn.Close()

	// Verify ALPN/TLS and pull negotiated state for sanity.
	state := conn.ConnectionState()
	if state.NegotiatedProtocol != "mash/1" {
		fail(fmt.Sprintf("unexpected ALPN %q (want mash/1)", state.NegotiatedProtocol))
	}
	if state.Version != tls.VersionTLS13 {
		fail(fmt.Sprintf("unexpected TLS version 0x%04x (want TLS 1.3)", state.Version))
	}

	// Build the interaction client with a transport-level framer + read loop.
	sender := newFramedSender(conn)
	client := interaction.NewClient(sender)
	defer client.Close()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		runReadLoop(conn, client)
	}()

	// 1. Read DeviceInfo (all attributes).
	readResult, err := client.Read(ctx, 0, featureDeviceInfo, nil)
	if err != nil {
		fail(fmt.Sprintf("Read DeviceInfo: %v", err))
	}

	// 2. Write DeviceInfo.label.
	writeResp, err := client.Write(ctx, 0, featureDeviceInfo, map[uint16]any{
		attrLabel: *labelWrite,
	})
	if err != nil {
		fail(fmt.Sprintf("Write label: %v", err))
	}
	storedLabel, _ := writeResp[attrLabel].(string)

	// 3. Invoke removeZone with integer-keyed parameters per §7.1.
	invokeResultRaw, err := client.Invoke(ctx, 0, featureDeviceInfo, cmdRemoveZone, map[uint8]any{
		removeZoneParamZone: *zoneArg,
	})
	var invStatus uint8
	var invRemoved bool
	if err != nil {
		var se *interaction.StatusError
		if errors.As(err, &se) {
			invStatus = uint8(se.Status)
		} else {
			fail(fmt.Sprintf("Invoke removeZone: %v", err))
		}
	} else {
		// Pull "removed" out of the response map. Spec §7.2: integer keys.
		if m, ok := invokeResultRaw.(map[any]any); ok {
			if v, ok := m[uint64(removeZoneRespRemove)]; ok {
				if b, ok := v.(bool); ok {
					invRemoved = b
				}
			}
		} else if m, ok := invokeResultRaw.(map[uint8]any); ok {
			if v, ok := m[removeZoneRespRemove]; ok {
				if b, ok := v.(bool); ok {
					invRemoved = b
				}
			}
		}
	}

	// 4. Subscribe — Phase 15 must report UNSUPPORTED (Phase 16 fixes this).
	var subStatus uint8
	_, _, sErr := client.Subscribe(ctx, 0, featureDeviceInfo, nil)
	if sErr != nil {
		var se *interaction.StatusError
		if errors.As(sErr, &se) {
			subStatus = uint8(se.Status)
		} else {
			fail(fmt.Sprintf("Subscribe: %v", sErr))
		}
	}

	// Stringify the read map so the JSON is human-friendly.
	readPretty := make(map[string]any, len(readResult))
	for k, v := range readResult {
		key := fmt.Sprintf("0x%04x", k)
		switch k {
		case attrVendorName:
			key = "vendorName"
		case attrProductName:
			key = "productName"
		case attrLabel:
			key = "label"
		}
		readPretty[key] = v
	}

	out := output{
		ReadDeviceInfo: readPretty,
		WriteLabel: writeResult{
			Echo:   *labelWrite,
			Stored: storedLabel,
		},
		InvokeRemoveZone: invokeResult{
			Status:  invStatus,
			Removed: invRemoved,
		},
		SubscribeStatus: subStatus,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(fmt.Sprintf("encode output: %v", err))
	}
}

// dialWithRetry dials with optional retry — useful when the Python device's
// operational listener is brought up just before the helper runs and there
// might be a brief moment where the port refuses connections.
func dialWithRetry(ctx context.Context, target string, cfg *tls.Config, retryFor time.Duration) (*tls.Conn, error) {
	if retryFor <= 0 {
		return tls.Dial("tcp", target, cfg)
	}
	deadline := time.Now().Add(retryFor)
	var lastErr error
	for {
		c, err := tls.Dial("tcp", target, cfg)
		if err == nil {
			return c, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("dial retry exhausted (%v): %w", retryFor, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// runReadLoop reads frames off the connection and dispatches Response /
// Notification frames to the interaction.Client. Returns on EOF or error.
func runReadLoop(conn *tls.Conn, client *interaction.Client) {
	fr := transport.NewFrameReader(conn)
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			return
		}
		mt, perr := wire.PeekMessageType(frame)
		if perr != nil {
			continue
		}
		switch mt {
		case wire.MessageTypeResponse:
			resp, derr := wire.DecodeResponse(frame)
			if derr != nil {
				continue
			}
			_ = client.HandleResponse(resp)
		case wire.MessageTypeNotification:
			notif, derr := wire.DecodeNotification(frame)
			if derr != nil {
				continue
			}
			client.HandleNotification(notif)
		default:
			// Control frames are answered at the transport layer in
			// production (see pkg/transport/connection.go); the helper
			// has no keep-alive mode and just ignores them.
		}
	}
}

// framedSender wraps a tls.Conn with the wire framer so it satisfies
// interaction.RequestSender.
type framedSender struct {
	conn *tls.Conn
	fw   *transport.FrameWriter
}

func newFramedSender(conn *tls.Conn) *framedSender {
	return &framedSender{conn: conn, fw: transport.NewFrameWriter(conn)}
}

func (s *framedSender) Send(data []byte) error {
	return s.fw.WriteFrame(data)
}

// buildOperationalTLS assembles the TLS config the helper uses to dial the
// Python device on the operational ALPN. mTLS: server presents its op cert
// (chain validated against the Zone CA) and the helper presents its
// controller op cert (which the Python device validates against the same
// CA via mash_py.transport.tls.make_operational_context).
func buildOperationalTLS(certPEM, keyPEM, caPEM []byte, expectedDeviceID string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("X509KeyPair: %w", err)
	}

	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		return nil, errors.New("zone CA PEM has no PEM block")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse zone CA: %w", err)
	}
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(caCert)

	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		Certificates:       []tls.Certificate{cert},
		RootCAs:            rootCAs,
		NextProtos:         []string{"mash/1"},
		ServerName:         expectedDeviceID,
		InsecureSkipVerify: true, // CN-based identity, not DNS — verified via VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no peer certs presented")
			}
			leaf := cs.PeerCertificates[0]
			// Verify chain against Zone CA (we built rootCAs above).
			opts := x509.VerifyOptions{
				Roots:         rootCAs,
				Intermediates: x509.NewCertPool(),
				CurrentTime:   time.Now(),
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
			}
			for _, intermediate := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(intermediate)
			}
			if _, err := leaf.Verify(opts); err != nil {
				return fmt.Errorf("zone-CA chain validation: %w", err)
			}
			// Identity check: leaf CN MUST equal the expected device-id
			// (commissioning derives it from SHA256(SPKI)[:8] uppercase).
			if leaf.Subject.CommonName != expectedDeviceID {
				return fmt.Errorf("device cert CN %q != expected %q",
					leaf.Subject.CommonName, expectedDeviceID)
			}
			return nil
		},
	}, nil
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		fail(fmt.Sprintf("read %s: %v", path, err))
	}
	return data
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

// hex import kept for consistency with run_commission_client; not currently
// used here but useful when extending the helper to dump raw transcripts.
var _ = hex.EncodeToString

// io import kept for the same reason — read loops are easy to extend with
// io.ReadFull for raw byte dumps.
var _ = io.ReadAll

// net import kept in case future variants need raw-socket setup.
var _ net.Conn
