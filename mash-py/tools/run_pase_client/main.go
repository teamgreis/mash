// Command run_pase_client drives a mash-go PASE client handshake against a
// host:port speaking the commissioning ALPN. Used by mash-py's Phase 14 interop
// test to verify that a Python `PASEServer` accepts a real Go controller's
// handshake.
//
// Scope is intentionally narrow: TLS dial + PASE only. No mDNS discovery, no
// cert exchange, no operational reconnect — those layers come in later phases.
// The helper exits 0 on a successful PASE and prints the derived K_main as
// JSON; non-zero exit + stderr message on any failure path.
//
// Run:
//
//	go run ./tools/run_pase_client \
//	    --target 127.0.0.1:8444 \
//	    --setup-code 20202021 \
//	    --discriminator 3840
//
// Output (stable JSON, single line):
//
//	{"k_main_hex":"...","setup_code":"20202021","discriminator":3840}
package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

type output struct {
	KMainHex      string `json:"k_main_hex"`
	SetupCode     string `json:"setup_code"`
	Discriminator uint16 `json:"discriminator"`
}

func main() {
	target := flag.String("target", "", "host:port to dial (required)")
	setupCodeStr := flag.String("setup-code", "", "8-digit setup code (required)")
	discriminator := flag.Uint("discriminator", 0, "12-bit discriminator (0..4095)")
	timeoutS := flag.Float64("timeout", 30.0, "overall timeout in seconds")
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

	setupCode, err := commissioning.ParseSetupCode(*setupCodeStr)
	if err != nil {
		fail(fmt.Sprintf("invalid setup code: %v", err))
	}

	ctx, cancel := context.WithTimeout(
		context.Background(), time.Duration(*timeoutS*float64(time.Second)),
	)
	defer cancel()

	// TLS commissioning client: TLS 1.3, ALPN "mash-comm/1", no peer cert
	// verification (PASE/SPAKE2+ authenticates at the application layer).
	// Mirrors mash-go's mash-controller commissioning dial path.
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

	// Verify ALPN landed on commissioning before driving PASE — a
	// misconfigured server would happily complete TLS without negotiating
	// the right ALPN, and we'd want to fail loudly here rather than blame
	// the PASE state machine.
	state := conn.ConnectionState()
	if state.NegotiatedProtocol != "mash-comm/1" {
		fail(fmt.Sprintf(
			"unexpected negotiated ALPN: %q (want mash-comm/1)",
			state.NegotiatedProtocol,
		))
	}

	session, err := commissioning.NewPASEClientSession(setupCode, uint16(*discriminator))
	if err != nil {
		fail(fmt.Sprintf("create PASE client session: %v", err))
	}

	kMain, err := session.Handshake(ctx, conn)
	if err != nil {
		fail(fmt.Sprintf("PASE handshake failed: %v", err))
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(output{
		KMainHex:      hex.EncodeToString(kMain),
		SetupCode:     *setupCodeStr,
		Discriminator: uint16(*discriminator),
	}); err != nil {
		fail(fmt.Sprintf("encoding output: %v", err))
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
