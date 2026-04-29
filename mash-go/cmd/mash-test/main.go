// Command mash-test is a test runner for MASH protocol conformance testing.
//
// This command runs protocol conformance tests against MASH devices or
// controllers, validating that they correctly implement the specification.
//
// Usage:
//
//	mash-test [flags] [test-pattern]
//
// Flags:
//
//	-target string          Target address (host:port) of device/controller under test
//	-mode string            Test mode: device, controller (default "device")
//	-pics string            Path to PICS file for the target
//	-tests string           Path to test cases directory
//	-filter string          Filter test cases by ID glob pattern (e.g., 'TC-CERT*')
//	-files string           Filter test files by name pattern (e.g., 'protocol-*,connection-*')
//	-tags string            Include only tests with these tags (comma-separated)
//	-exclude-tags string    Exclude tests with these tags (comma-separated)
//	-timeout duration       Test timeout (default 30s)
//	-verbose                Enable verbose output
//	-json                   Output results as JSON
//	-junit                  Output results as JUnit XML
//	-insecure               Skip TLS certificate verification
//	-setup-code string      PASE setup code (8-digit numeric string)
//	-client-identity string Client identity for PASE (default: test-client)
//	-server-identity string Server identity for PASE (default: test-device)
//	-protocol-log string    File path for protocol event logging (CBOR format)
//
// PICS capability filtering is determined automatically:
//   - If -pics is provided, the static PICS file is used.
//   - If -setup-code is provided (and no -pics), PICS is auto-discovered from
//     the device at startup via commissioning.
//   - If neither is provided, all tests run without capability filtering.
//
// Examples:
//
//	# Test a device at localhost:8443
//	mash-test -target localhost:8443 -mode device
//
//	# Test with PASE commissioning (auto-discovers PICS)
//	mash-test -target localhost:8443 -setup-code 20202021
//
//	# Test specific patterns with static PICS file
//	mash-test -target 192.168.1.100:8443 -pics device.pics -tests ./testdata/cases
//
//	# Run specific test pattern with verbose output
//	mash-test -target localhost:8443 -verbose "EnergyControl.*"
//
//	# Run only protocol tests
//	mash-test -target localhost:8443 -files "protocol-*"
//
//	# Exclude slow tests
//	mash-test -target localhost:8443 -exclude-tags slow
//
//	# Run only tests tagged 'connection'
//	mash-test -target localhost:8443 -tags connection
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/runner"
	mashlog "github.com/mash-protocol/mash-go/pkg/log"
)

var (
	target          = flag.String("target", "", "Target address (host:port) of device/controller under test. With -srv-resolve, may be either the commissioning or operational port; SRV records pin the actual ports per ALPN.")
	mode            = flag.String("mode", "device", "Test mode: device, controller")
	pics            = flag.String("pics", "", "Path to PICS file for the target")
	tests           = flag.String("tests", "./testdata/cases", "Path to test cases directory")
	timeout         = flag.Duration("timeout", 30*time.Second, "Test timeout")
	verbose         = flag.Bool("verbose", false, "Enable verbose output")
	jsonOut         = flag.Bool("json", false, "Output results as JSON")
	junitOut        = flag.Bool("junit", false, "Output results as JUnit XML")
	insecure        = flag.Bool("insecure", false, "Skip TLS certificate verification")
	setupCode       = flag.String("setup-code", "", "PASE setup code (8-digit numeric string)")
	protocolLog     = flag.String("protocol-log", "", "File path for protocol event logging (CBOR format)")
	enableKey       = flag.String("enable-key", "00112233445566778899aabbccddeeff", "128-bit hex key for TestControl triggers (32 hex chars)")
	debug           = flag.Bool("debug", false, "Enable debug logging (connection lifecycle, precondition transitions, state snapshots)")
	suiteTimeout    = flag.Duration("suite-timeout", 0, "Suite timeout (0 = auto-calculate from test timeouts)")
	filter          = flag.String("filter", "", "Filter test cases by ID glob pattern (e.g., 'TC-CERT*' or '*ZONE*')")
	files           = flag.String("files", "", "Filter test files by name pattern (e.g., 'protocol-*,connection-*')")
	tags            = flag.String("tags", "", "Include only tests with these tags (comma-separated)")
	excludeTags     = flag.String("exclude-tags", "", "Exclude tests with these tags (comma-separated)")
	shuffle         = flag.Bool("shuffle", false, "Randomize test order within each precondition level")
	shuffleSeed     = flag.Int64("shuffle-seed", 0, "Seed for shuffle randomization (0 = auto-generate)")
	strictLifecycle = flag.Bool("strict-lifecycle", false, "Fail tests when teardown cleanup invariants are violated")
	srvResolve      = flag.Bool("srv-resolve", false, "Resolve TCP port via mDNS SRV records per ALPN before each dial (required for DEC-082 two-port devices; falls back to -target on mDNS failure)")
	srvDiscriminator = flag.Uint("discriminator", 0, "Filter -srv-resolve commissionable browse to this discriminator (per DEC-088 MASH-NNNN). Required on shared LANs where stale records may be visible.")
	spake2plusVecs  = flag.String("spake2plus-vectors", "", "Run SPAKE2+ algorithmic conformance vectors from the given JSON file and exit (Phase 7B). When set, all other flags except -verbose are ignored.")
)

func main() {
	os.Exit(run())
}

func run() int {
	flag.Parse()

	// SPAKE2+ conformance mode short-circuits the network-driven test path:
	// the vectors are pure algorithmic crypto, so a target/PICS/setup-code
	// is irrelevant. See Phase 7B of mash-py-tdd-plan.md.
	if *spake2plusVecs != "" {
		return runSPAKE2PlusConformance(*spake2plusVecs, os.Stdout)
	}

	// Get optional test pattern (from -filter flag or positional argument)
	pattern := *filter
	if pattern == "" && flag.NArg() > 0 {
		pattern = flag.Arg(0)
	}

	// Validate configuration
	if *target == "" {
		fmt.Fprintln(os.Stderr, "Error: target address is required (-target)")
		flag.Usage()
		return 1
	}

	if *mode != "device" && *mode != "controller" {
		fmt.Fprintf(os.Stderr, "Error: mode must be 'device' or 'controller', got '%s'\n", *mode)
		flag.Usage()
		return 1
	}

	// Derive auto-PICS: when setup-code is available but no static PICS
	// file is given, automatically discover capabilities from the device.
	autoPICS := *setupCode != "" && *pics == ""

	if *setupCode == "" && *pics == "" {
		log.Println("Warning: no PICS file or setup code provided; all tests will run without capability filtering")
	}

	// Determine output format
	outputFormat := "text"
	if *jsonOut {
		outputFormat = "json"
	} else if *junitOut {
		outputFormat = "junit"
	}

	// Setup logging for text output
	if outputFormat == "text" {
		log.SetFlags(log.Ltime)
		if *verbose || *debug {
			log.SetFlags(log.Ltime | log.Lmicroseconds)
		}
		printBanner()
		log.Printf("Target: %s", *target)
		log.Printf("Mode: %s", *mode)
		if autoPICS {
			log.Println("PICS: auto-discovery enabled")
		} else if *pics != "" {
			log.Printf("PICS: %s", *pics)
		}
		if pattern != "" {
			log.Printf("Pattern: %s", pattern)
		}
		if *files != "" {
			log.Printf("Files: %s", *files)
		}
		if *tags != "" {
			log.Printf("Tags: %s", *tags)
		}
		if *excludeTags != "" {
			log.Printf("Exclude-Tags: %s", *excludeTags)
		}
		log.Println()
	}

	// Set up protocol logging if requested
	var protocolLogger *mashlog.FileLogger
	if *protocolLog != "" {
		var err error
		protocolLogger, err = mashlog.NewFileLogger(*protocolLog)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to create protocol logger: %v\n", err)
			return 1
		}
		if outputFormat == "text" {
			log.Printf("Protocol logging to: %s", *protocolLog)
		}
	}

	// Create runner configuration
	config := &runner.Config{
		Target:             *target,
		Mode:               *mode,
		PICSFile:           *pics,
		TestDir:            *tests,
		Pattern:            pattern,
		Files:              *files,
		Tags:               *tags,
		ExcludeTags:        *excludeTags,
		Timeout:            *timeout,
		Verbose:            *verbose,
		Output:             os.Stdout,
		OutputFormat:       outputFormat,
		InsecureSkipVerify: *insecure,
		SetupCode:          *setupCode,
		EnableKey:          *enableKey,
		AutoPICS:           autoPICS,
		Debug:              *debug,
		SuiteTimeout:       *suiteTimeout,
		Shuffle:            *shuffle,
		ShuffleSeed:        *shuffleSeed,
		StrictLifecycle:    *strictLifecycle,
		SRVResolve:         *srvResolve,
		SRVDiscriminator:   uint16(*srvDiscriminator),
	}
	// Only set logger when non-nil to avoid typed-nil interface issue.
	if protocolLogger != nil {
		config.ProtocolLogger = protocolLogger
	}

	// Create and run test runner. Using a separate run() function ensures
	// deferred cleanup (Runner.Close, RemoveZone) always executes, even
	// when tests fail. Previously os.Exit(1) in main() skipped defers,
	// leaving stale zones on the device that prevented commissioning mode
	// in the next test run.
	r := runner.New(config)
	defer func() {
		r.Close()
		if protocolLogger != nil {
			protocolLogger.Close()
		}
	}()

	// Suite timeout is managed by the engine's RunSuite (auto-calculated or explicit).
	// The top-level context does not impose a timeout to avoid premature cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := r.Run(ctx)
	if err != nil {
		if outputFormat == "text" {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			// For JSON/JUnit, error is written to stderr
			log.Printf("Error: %v", err)
		}
		return 1
	}

	// Exit with appropriate code
	if result.FailCount > 0 {
		return 1
	}
	return 0
}

func printBanner() {
	fmt.Print(`
 __  __    _    ____  _   _   _____         _
|  \/  |  / \  / ___|| | | | |_   _|__  ___| |_
| |\/| | / _ \ \___ \| |_| |   | |/ _ \/ __| __|
| |  | |/ ___ \ ___) |  _  |   | |  __/\__ \ |_
|_|  |_/_/   \_\____/|_| |_|   |_|\___||___/\__|

Protocol Conformance Test Runner
`)
}
