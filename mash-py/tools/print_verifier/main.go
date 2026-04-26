// Command print_verifier emits a JSON-encoded mash-go SPAKE2+ verifier
// for the given setup code + discriminator. mash-py's interop tests use
// this as a live oracle: they invoke `go run` and assert that mash-py's
// own generate_verifier produces the same (salt, w0, L) bytes.
//
// This is the cheapest possible cross-implementation check — no TLS, no
// mDNS, no PASE messages. The full live PASE handshake lands in Phase 8.
//
// Run:
//
//	go run ./tools/print_verifier --setup-code 20202021 --discriminator 3840
//
// Output (stable JSON):
//
//	{
//	  "discriminator": 3840,
//	  "salt_hex": "...",
//	  "w0_hex":   "...",
//	  "l_hex":    "...",
//	  "iterations": 100000
//	}
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

type output struct {
	Discriminator uint16 `json:"discriminator"`
	SaltHex       string `json:"salt_hex"`
	W0Hex         string `json:"w0_hex"`
	LHex          string `json:"l_hex"`
	Iterations    uint32 `json:"iterations"`
}

func main() {
	setupCodeStr := flag.String("setup-code", "", "8-digit setup code (required)")
	discriminator := flag.Uint("discriminator", 0, "12-bit discriminator (0..4095)")
	flag.Parse()

	if *setupCodeStr == "" {
		fmt.Fprintln(os.Stderr, "error: --setup-code is required")
		os.Exit(2)
	}
	if *discriminator >= 1<<12 {
		fmt.Fprintf(os.Stderr, "error: --discriminator %d exceeds 12 bits\n", *discriminator)
		os.Exit(2)
	}

	setupCode, err := commissioning.ParseSetupCode(*setupCodeStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid setup code: %v\n", err)
		os.Exit(2)
	}

	verifier, err := commissioning.GenerateVerifier(setupCode, uint16(*discriminator))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: GenerateVerifier failed: %v\n", err)
		os.Exit(1)
	}

	out := output{
		Discriminator: verifier.Discriminator,
		SaltHex:       hex.EncodeToString(verifier.Salt),
		W0Hex:         hex.EncodeToString(verifier.W0),
		LHex:          hex.EncodeToString(verifier.L),
		Iterations:    verifier.Iterations,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: encoding output: %v\n", err)
		os.Exit(1)
	}
}
