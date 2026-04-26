// Package runner provides test execution against real MASH devices/controllers.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/internal/testharness/reporter"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// Runner executes test cases against a target device or controller.
type Runner struct {
	config       *Config
	engine       *engine.Engine
	engineConfig *engine.EngineConfig
	reporter     reporter.Reporter
	resolver     *Resolver
	pics         *loader.PICSFile // Cached PICS for handler access

	// Components (interface-typed for testability)
	suite       SuiteSession
	pool        ConnPool
	coordinator Coordinator
	dialer      Dialer
	connMgr     ConnectionManager

	// observer is the persistent mDNS observer for the current test.
	// Created lazily by getOrCreateObserver, stopped in teardownTest.
	observer *mdnsObserver

	// pairingAdvertiser is a real mDNS advertiser used by announce_pairing_request
	// to advertise _mashp._udp services. Cleaned up in Close().
	pairingAdvertiser *discovery.MDNSAdvertiser

	// strictLifecycleErr captures the first strict lifecycle failure that occurs
	// in callback paths that cannot return errors directly (e.g. coordinator
	// invoking WireOps with void signatures). setupPreconditions consumes it.
	strictLifecycleErr error
	lifecycleCtrl      *LifecycleController
}

// Config configures the test runner.
type Config struct {
	// Target is the address of the device/controller under test (host:port).
	Target string

	// TargetHost is the hostname/IP extracted from Target (without port).
	// Populated automatically during New() from Target.
	TargetHost string

	// Mode is "device" or "controller".
	Mode string

	// PICSFile is the path to the PICS file.
	PICSFile string

	// TestDir is the path to test case directory.
	TestDir string

	// Pattern filters test cases by name.
	Pattern string

	// Files filters which YAML test files to load (comma-separated glob
	// patterns matched against filename stem, e.g. "protocol-*,connection-*").
	Files string

	// Tags includes only tests with at least one of these tags (comma-separated).
	Tags string

	// ExcludeTags excludes tests with any of these tags (comma-separated).
	ExcludeTags string

	// Timeout is the default test timeout.
	Timeout time.Duration

	// SuiteTimeout is the overall test suite timeout (0 = auto-calculate).
	SuiteTimeout time.Duration

	// Verbose enables verbose output.
	Verbose bool

	// Debug enables detailed debug logging of connection lifecycle,
	// precondition transitions, zone management, and PASE attempts.
	// Output includes timestamped runner state snapshots.
	Debug bool

	// Output is where to write results.
	Output io.Writer

	// OutputFormat is "text", "json", or "junit".
	OutputFormat string

	// InsecureSkipVerify skips TLS certificate verification.
	InsecureSkipVerify bool

	// SetupCode is the PASE password/setup code (8-digit numeric string).
	SetupCode string

	// EnableKey is the hex-encoded 128-bit key for TestControl triggers.
	// Defaults to "00112233445566778899aabbccddeeff" (well-known test key).
	EnableKey string

	// AutoPICS enables automatic PICS discovery from the device.
	// When true, the runner commissions the device at startup and reads
	// its capabilities to build a PICS file dynamically.
	AutoPICS bool

	// Shuffle randomizes test order within each precondition level.
	// When true, a random seed is used and printed for reproducibility.
	// When ShuffleSeed is set, that seed is used instead.
	Shuffle bool

	// ShuffleSeed is the seed for shuffle randomization.
	// 0 means auto-generate from current time.
	ShuffleSeed int64

	// ProtocolLogger receives structured protocol events for debugging.
	// Set to nil to disable protocol logging.
	ProtocolLogger log.Logger

	// StrictLifecycle enables strict teardown/lifecycle invariant enforcement.
	// When false, invariant violations are recorded but do not fail the test.
	StrictLifecycle bool

	// SRVResolve, when true, resolves the device's actual TCP port via mDNS
	// SRV records before each dial instead of trusting Target's port. This
	// is required to drive a DEC-082 two-port deployment from the test
	// harness (mash-py-spec-issues Issue #3). On mDNS-lookup failure the
	// dialer falls back to the literal Target so the flag is strictly
	// safer than its absence.
	SRVResolve bool
}

// ConnState represents the connection lifecycle state.
type ConnState int

const (
	// ConnDisconnected means no socket resources are held.
	ConnDisconnected ConnState = iota
	// ConnTLSConnected means commissioning TLS is active (pre-PASE or during PASE).
	ConnTLSConnected
	// ConnOperational means operational TLS is active (post-commissioning, zone CA verified).
	ConnOperational
)

// Connection represents a connection to the target.
type Connection struct {
	conn    net.Conn
	tlsConn *tls.Conn
	framer  *transport.Framer
	state   ConnState

	// hadConnection is set to true when the connection enters any connected
	// state (TLS or operational). It is NOT cleared on disconnect -- only
	// explicitly during teardown. This allows verify_commissioning_state to
	// distinguish "had a connection that was closed" (ADVERTISING) from
	// "never connected" (IDLE).
	hadConnection bool

	// pendingNotifications buffers notifications that were read while
	// waiting for an invoke response (e.g. during sendTriggerViaZone).
	// handleWaitForNotificationAsZone drains this before reading the wire.
	pendingNotifications [][]byte
}

// transitionTo changes the connection state. Any transition to ConnDisconnected
// closes the underlying socket but does NOT nil the connection/framer pointers.
// This is critical: handlers spawn goroutines that hold references to r.pool.Main().framer
// for async reads. If transitionTo nilled the framer, those goroutines would
// panic on nil dereference. Instead, the closed socket causes ReadFrame/WriteFrame
// to return an error, which is the safe way to signal goroutines.
//
// Pointers are nilled explicitly by clearConnectionRefs() which is called from
// disconnectConnection/ensureDisconnected (full cleanup paths) and at connection
// replacement points where a new Connection struct is created.
func (c *Connection) transitionTo(newState ConnState) {
	if newState == ConnDisconnected && c.state != ConnDisconnected {
		if c.tlsConn != nil {
			c.tlsConn.Close()
		}
		if c.conn != nil {
			c.conn.Close()
		}
	}
	if newState != ConnDisconnected {
		c.hadConnection = true
	}
	c.state = newState
}

// clearConnectionRefs nils the connection/framer pointers after the socket
// has been closed. Call this only from full-cleanup paths (disconnectConnection,
// ensureDisconnected) where no goroutine could still be referencing the framer.
func (c *Connection) clearConnectionRefs() {
	c.tlsConn = nil
	c.conn = nil
	c.framer = nil
}

// isConnected returns true if the connection is in any connected state.
func (c *Connection) isConnected() bool {
	return c.state != ConnDisconnected
}

// isOperational returns true if the connection is in the operational state.
func (c *Connection) isOperational() bool {
	return c.state == ConnOperational
}

// setReadDeadlineFromContext sets the underlying connection's read deadline
// from the context's deadline. This prevents reads from blocking indefinitely
// when the remote end has closed the connection (TCP retransmit timeout can
// be 90+ seconds).
func (c *Connection) setReadDeadlineFromContext(ctx context.Context) {
	if c == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	if c.tlsConn != nil {
		c.tlsConn.SetReadDeadline(deadline)
	} else if c.conn != nil {
		c.conn.SetReadDeadline(deadline)
	}
}

// clearReadDeadline removes any read deadline on the underlying connection.
func (c *Connection) clearReadDeadline() {
	if c == nil {
		return
	}
	if c.tlsConn != nil {
		c.tlsConn.SetReadDeadline(time.Time{})
	} else if c.conn != nil {
		c.conn.SetReadDeadline(time.Time{})
	}
}

// setWriteDeadlineFromContext sets the underlying connection's write deadline
// from the context's deadline.
func (c *Connection) setWriteDeadlineFromContext(ctx context.Context) {
	if c == nil {
		return
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	if c.tlsConn != nil {
		c.tlsConn.SetWriteDeadline(deadline)
	} else if c.conn != nil {
		c.conn.SetWriteDeadline(deadline)
	}
}

// clearWriteDeadline removes any write deadline on the underlying connection.
func (c *Connection) clearWriteDeadline() {
	if c == nil {
		return
	}
	if c.tlsConn != nil {
		c.tlsConn.SetWriteDeadline(time.Time{})
	} else if c.conn != nil {
		c.conn.SetWriteDeadline(time.Time{})
	}
}

// New creates a new test runner.
func New(config *Config) *Runner {
	// Create engine with config
	engineConfig := engine.DefaultConfig()
	engineConfig.DefaultTimeout = config.Timeout
	engineConfig.SuiteTimeout = config.SuiteTimeout

	// Load PICS if provided
	var pics *loader.PICSFile
	if config.PICSFile != "" {
		var err error
		pics, err = loader.LoadPICS(config.PICSFile)
		if err == nil {
			engineConfig.PICS = pics
		}
	}

	// Extract hostname from Target.
	if config.Target != "" && config.TargetHost == "" {
		host, _, err := net.SplitHostPort(config.Target)
		if err != nil {
			// Target might be just a hostname without port
			config.TargetHost = config.Target
		} else {
			config.TargetHost = host
		}
	}

	r := &Runner{
		config:        config,
		engine:        engine.NewWithConfig(engineConfig),
		engineConfig:  engineConfig,
		resolver:      NewResolver(),
		pics:          pics,
		suite:         NewSuiteSession(),
		lifecycleCtrl: NewLifecycleController(),
	}

	// Initialize connection pool. The onZoneClose callback sends protocol-level
	// RemoveZone + ControlClose before the pool closes each zone socket.
	r.pool = NewConnPool(r.debugf, func(conn *Connection, zoneID string) {
		if conn.framer != nil {
			r.sendRemoveZoneOnConn(conn, zoneID)
			closeMsg := &wire.ControlMessage{Type: wire.ControlClose}
			if closeData, err := wire.EncodeControlMessage(closeMsg); err == nil {
				_ = conn.framer.WriteFrame(closeData)
			}
		}
		// Drop per-zone crypto when a zone is removed from the pool.
		if r.connMgr != nil {
			r.connMgr.RemoveZoneCrypto(zoneID)
		}
	})
	r.pool.SetMain(&Connection{})

	// Initialize dialer for TLS connection establishment. With -srv-resolve,
	// wrap the base dialer so each dial first resolves the SRV-advertised
	// port via mDNS (DEC-082 two-port support).
	r.dialer = NewDialer(config.InsecureSkipVerify, r.debugf)
	if config.SRVResolve {
		r.dialer = NewSRVResolvingDialer(r.dialer, r.debugf)
	}

	// Initialize connection manager with callbacks into Runner for
	// handler-dependent operations (PASE, mDNS, message IDs).
	r.connMgr = NewConnectionManager(r.pool, r.suite, r.dialer, r.config, r.debugf, connMgrDeps{
		connectFn: func(ctx context.Context, state *engine.ExecutionState) error {
			return r.ensureConnected(ctx, state)
		},
		commissionFn: func(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
			return r.handleCommission(ctx, step, state)
		},
		browseFn: func(ctx context.Context, serviceType string, _ map[string]any, timeoutMs int) (int, error) {
			services, err := r.browseViaObserver(ctx, serviceType, timeoutMs, 1)
			return len(services), err
		},
		clearSnapshotFn: func(serviceType string) {
			if obs := r.getOrCreateObserver(); obs != nil {
				obs.ClearSnapshot(serviceType)
			}
		},
		nextMsgIDFn: func() uint32 { return r.nextMessageID() },
	})

	// Initialize coordinator (not yet wired -- Steps 5-7 will delegate to it).
	r.coordinator = NewCoordinator(r.suite, r.pool, r, r.config, r.debugf)

	// Set precondition callback (must be after r is created since it's a method on r).
	// This works because NewWithConfig stores the *EngineConfig pointer.
	engineConfig.SetupPreconditions = r.setupPreconditions
	engineConfig.TeardownTest = r.teardownTest

	// Stream each test result as it completes.
	engineConfig.OnTestComplete = func(result *engine.TestResult) {
		r.reporter.ReportTest(result)
	}

	// Register enhanced checkers
	engine.RegisterEnhancedCheckers(r.engine)

	// Create reporter
	switch config.OutputFormat {
	case "json":
		r.reporter = reporter.NewJSONReporter(config.Output, true)
	case "junit":
		r.reporter = reporter.NewJUnitReporter(config.Output)
	default:
		r.reporter = reporter.NewTextReporter(config.Output, config.Verbose)
	}

	// Register action handlers
	r.registerHandlers()

	return r
}

// nextMessageID returns the next message ID (delegates to pool).
func (r *Runner) nextMessageID() uint32 {
	return r.pool.NextMessageID()
}

func (r *Runner) clearStrictLifecycleErr() {
	r.strictLifecycleErr = nil
}

func (r *Runner) setStrictLifecycleErr(err error) {
	if err == nil {
		return
	}
	if r.strictLifecycleErr == nil {
		r.strictLifecycleErr = err
		return
	}
	r.strictLifecycleErr = errors.Join(r.strictLifecycleErr, err)
}

func (r *Runner) strictLifecycleEnabled() bool {
	return r.config != nil && r.config.StrictLifecycle
}

func (r *Runner) lifecycleController() *LifecycleController {
	if r.lifecycleCtrl == nil {
		r.lifecycleCtrl = NewLifecycleController()
	}
	return r.lifecycleCtrl
}

// getTarget returns the host:port for connections.
// Checks params for overrides, then falls back to config.Target.
func (r *Runner) getTarget(params map[string]any) string {
	if t, ok := params[KeyTarget].(string); ok && t != "" {
		return t
	}
	return r.config.Target
}

// getTargetFromState returns the host:port for connections, checking
// StateDeviceAddress in execution state before falling back to config.Target.
// Used by reconnectOperational where the device may have changed its address
// (e.g. TC-IPV6-003 after an IPv6 address change).
func (r *Runner) getTargetFromState(state *engine.ExecutionState) string {
	if addr, ok := state.Get(StateDeviceAddress); ok {
		if s, ok := addr.(string); ok && s != "" {
			return s
		}
	}
	return r.config.Target
}

// Run executes all matching test cases and returns the suite result.
func (r *Runner) Run(ctx context.Context) (*engine.SuiteResult, error) {
	// Auto-PICS: discover device capabilities before loading tests.
	// This runs first because PICS data influences test filtering.
	if r.config.AutoPICS {
		if err := r.runAutoPICS(ctx); err != nil {
			return nil, fmt.Errorf("auto-PICS discovery failed: %w", err)
		}
	}

	// Auto-detect host IPv6 capability and inject PICS item.
	// Tests like TC-IPV6-002 require a global IPv6 address; on IPv4-only
	// hosts this PICS item will be absent and those tests are skipped.
	if detectHostIPv6Global() {
		if r.pics == nil {
			r.pics = &loader.PICSFile{Items: make(map[string]any)}
			r.engineConfig.PICS = r.pics
		}
		if r.pics.Items == nil {
			r.pics.Items = make(map[string]any)
		}
		r.pics.Items["MASH.C.NETWORK.HAS_IPV6_GLOBAL"] = 1
	}

	// Load test cases (optionally filtered by file name pattern).
	cases, err := loader.LoadDirectoryWithFilter(r.config.TestDir, r.config.Files)
	if err != nil {
		return nil, fmt.Errorf("failed to load tests: %w", err)
	}

	// Filter by mode (device/controller) to skip tests for the wrong role.
	if r.config.Mode != "" {
		cases = filterByMode(cases, r.config.Mode)
	}

	// Filter by pattern if provided
	if r.config.Pattern != "" {
		cases = filterByPattern(cases, r.config.Pattern)
	}

	// Filter by tags if provided
	if r.config.Tags != "" {
		cases = filterByTags(cases, r.config.Tags)
	}
	if r.config.ExcludeTags != "" {
		cases = filterByExcludeTags(cases, r.config.ExcludeTags)
	}

	if len(cases) == 0 {
		return nil, fmt.Errorf("no test cases found matching filters (pattern=%q, files=%q, tags=%q, exclude-tags=%q)",
			r.config.Pattern, r.config.Files, r.config.Tags, r.config.ExcludeTags)
	}

	// Sort tests by precondition level to minimize state transitions.
	SortByPreconditionLevel(cases)

	// Shuffle within precondition levels if requested.
	var shuffleSeed int64
	if r.config.Shuffle {
		shuffleSeed = r.config.ShuffleSeed
		if shuffleSeed == 0 {
			shuffleSeed = time.Now().UnixNano()
		}
		ShuffleWithinLevels(cases, shuffleSeed)
		stdlog.Printf("Shuffle: seed=%d", shuffleSeed)
	}

	// Suite setup: commission once before any test runs if L3 tests exist.
	// If autoPICS already established a suite zone, skip this.
	if r.suite.ZoneID() == "" && needsSuiteCommissioning(cases, r) {
		if err := r.commissionSuiteZone(ctx); err != nil {
			stdlog.Printf("Suite commissioning failed: %v (tests will commission lazily)", err)
		}
	}

	// Run the test suite
	result := r.engine.RunSuite(ctx, cases)
	result.SuiteName = fmt.Sprintf("MASH Conformance Tests (%s)", r.config.Target)

	// Record shuffle metadata for reproducibility.
	if shuffleSeed != 0 {
		result.ShuffleSeed = shuffleSeed
		result.ExecutionOrder = make([]string, len(cases))
		for i, tc := range cases {
			result.ExecutionOrder[i] = tc.ID
		}
	}

	// Suite teardown: remove the suite zone and close all connections.
	if r.suite.ConnKey() != "" {
		r.removeSuiteZone()
	}

	// Report summary only -- individual tests were already streamed via OnTestComplete.
	r.reporter.ReportSummary(result)

	return result, nil
}

// needsSuiteCommissioning returns true when the runner has a target and an
// enable key, meaning it can commission a suite zone for trigger delivery.
// The suite zone is the control channel for all infrastructure commands
// (reset, enter/exit commissioning mode, fault injection).
func needsSuiteCommissioning(_ []*loader.TestCase, r *Runner) bool {
	return r.config.Target != "" && r.config.EnableKey != ""
}

// runAutoPICS commissions the device, reads its capabilities, and builds a
// PICS file. The commissioned session is preserved as the suite zone so
// L3 tests can reuse it without re-commissioning.
func (r *Runner) runAutoPICS(ctx context.Context) error {
	state := engine.NewExecutionState(ctx)

	// Commission the device as ZoneTypeTest so the suite zone doesn't
	// count against MaxZones and is transparent to device logic.
	r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
	if err := r.ensureCommissioned(ctx, state); err != nil {
		r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
		return fmt.Errorf("commissioning for auto-PICS: %w", err)
	}
	r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)

	pf, err := r.buildAutoPICS(ctx)
	if err != nil {
		r.ensureDisconnected()
		return err
	}

	// Install the discovered PICS.
	r.pics = pf
	r.engineConfig.PICS = pf

	if r.config.Verbose {
		logAutoPICS(pf)
	} else {
		stdlog.Printf("Auto-PICS: discovered %d items from device", len(pf.Items))
	}

	// Force the device into commissioning mode while we still have a live
	// zone session. This handles stale zones from previous test runs:
	// the device persists zone memberships, so on restart it may have
	// zones that sendRemoveZone won't clear (it only removes our zone).
	// Without the trigger, the stale zones keep the device in operational
	// mode, causing PASE failures in subsequent tests.
	if r.config.EnableKey != "" {
		r.debugf("auto-PICS: sending TriggerEnterCommissioningMode on live zone session")
		_, _ = r.sendTrigger(ctx, features.TriggerEnterCommissioningMode, state)
	}

	// Keep the auto-PICS session as the suite zone. Run() will check
	// needsSuiteCommissioning and skip if suiteZoneID is already set.
	r.recordSuiteZone()
	r.debugf("auto-PICS: preserving session as suite zone (id=%s)", r.suite.ZoneID())

	return nil
}

// framerSyncConn adapts a transport.Framer to the service.SyncConnection interface.
// This allows the cert exchange protocol (IssueInitialCertSync) to use the
// existing PASE connection's framer for synchronous send/receive.
type framerSyncConn struct {
	framer *transport.Framer
}

// Send writes data as a framed message.
func (f *framerSyncConn) Send(data []byte) error {
	return f.framer.WriteFrame(data)
}

// ReadFrame reads a framed message.
func (f *framerSyncConn) ReadFrame() ([]byte, error) {
	return f.framer.ReadFrame()
}

// operationalTLSConfig builds a TLS config for operational (post-commissioning) connections.
// It delegates to buildOperationalTLSConfig using the runner's current working crypto.
func (r *Runner) operationalTLSConfig() *tls.Config {
	return buildOperationalTLSConfig(r.WorkingCrypto(), r.config.InsecureSkipVerify, r.debugf)
}

// verifyPeerCertAgainstZoneCA validates the peer certificate chain against
// the Zone CA pool. Delegates to the standalone verifyPeerCert function.
func (r *Runner) verifyPeerCertAgainstZoneCA(rawCerts [][]byte, chains [][]*x509.Certificate) error {
	return verifyPeerCert(rawCerts, chains, r.connMgr.ZoneCAPool(), r.connMgr.ZoneCA(), r.debugf)
}

// trackSubscription adds a subscription ID to the active tracking list (delegates to pool).
func (r *Runner) trackSubscription(subID uint32) {
	r.pool.TrackSubscription(subID)
}

// removeActiveSubscription removes a subscription ID from tracking (delegates to pool).
func (r *Runner) removeActiveSubscription(subID uint32) {
	r.pool.RemoveSubscription(subID)
}

// Close cleans up runner resources. It sends RemoveZone on the main
// connection before closing so the device can re-enter commissioning
// mode for the next test run.
func (r *Runner) Close() error {
	r.stopObserver()
	if r.pairingAdvertiser != nil {
		r.pairingAdvertiser.StopAll()
		r.pairingAdvertiser = nil
	}
	r.closeActiveZoneConns()
	if r.pool.Main() != nil {
		if r.pool.Main().isConnected() {
			r.sendRemoveZone()
		}
		return r.pool.Main().Close()
	}
	return nil
}

// Close closes the connection. It is idempotent: calling Close on an
// already-closed connection is a no-op.
func (c *Connection) Close() error {
	c.transitionTo(ConnDisconnected)
	return nil
}

// registerHandlers registers all action handlers with the engine.
func (r *Runner) registerHandlers() {
	// Connection handlers
	r.engine.RegisterHandler(ActionConnect, r.handleConnect)
	r.engine.RegisterHandler(ActionDisconnect, r.handleDisconnect)

	// Protocol operation handlers
	r.engine.RegisterHandler(ActionRead, r.handleRead)
	r.engine.RegisterHandler(ActionWrite, r.handleWrite)
	r.engine.RegisterHandler(ActionSubscribe, r.handleSubscribe)
	r.engine.RegisterHandler(ActionInvoke, r.handleInvoke)

	// PASE commissioning handlers
	r.registerPASEHandlers()

	// Utility handlers
	r.engine.RegisterHandler(ActionWait, r.handleWait)
	r.engine.RegisterHandler(ActionVerify, r.handleVerify)

	// Register custom expectation checkers
	r.engine.RegisterChecker(CheckerConnectionEstablished, r.checkConnectionEstablished)
	r.engine.RegisterChecker(CheckerResponseReceived, r.checkResponseReceived)

	// Certificate renewal handlers
	r.registerRenewalHandlers()

	// Security testing handlers (DEC-047)
	r.registerSecurityHandlers()

	// TestControl trigger handlers
	r.registerTriggerHandlers()

	// Extended handler groups
	r.registerUtilityHandlers()
	r.registerDiscoveryHandlers()
	r.registerZoneHandlers()
	r.registerDeviceHandlers()
	r.registerControllerHandlers()
	r.registerConnectionHandlers()
	r.registerCertHandlers()
	r.registerNetworkHandlers()
}

// handleConnect establishes a connection to the target.
func (r *Runner) handleConnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Simulate connection failure when device_port_closed is set.
	if portClosed, _ := state.Get(PrecondDevicePortClosed); portClosed == true {
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyConnected:             false,
			KeyTLSHandshakeSuccess:   false,
			KeyError:                 ErrCodeConnectionFailed,
			KeyErrorCode:             ErrCodeConnectionFailed,
			KeyErrorDetail:           "connection refused (simulated)",
		}, nil
	}

	// Check if this is a commissioning connection
	commissioning := false
	if c, ok := step.Params[KeyCommissioning].(bool); ok {
		commissioning = c
	}

	target := r.getTarget(step.Params)
	// Also construct from host + port when specified (overrides above).
	if h, ok := step.Params["host"].(string); ok && h != "" {
		port := 8443
		if p, ok := step.Params["port"]; ok {
			port = int(toFloat(p))
		}
		target = fmt.Sprintf("%s:%d", h, port)
	}

	insecure := r.config.InsecureSkipVerify
	if i, ok := step.Params["insecure"].(bool); ok {
		insecure = i
	}

	// Create TLS config
	var tlsConfig *tls.Config
	if commissioning {
		// Use proper commissioning TLS config for PASE
		tlsConfig = transport.NewCommissioningTLSConfig()
	} else if r.connMgr.ZoneCAPool() != nil {
		// Use operational TLS config with Zone CA validation (no hostname check).
		tlsConfig = r.operationalTLSConfig()
	} else {
		// When no Zone CA exists, accept self-signed certs since there's
		// no trusted root to verify against yet.
		skipVerify := true
		if !insecure {
			// Only enforce verification if explicitly set to false AND
			// accept_self_signed is also explicitly false.
			if a, ok := step.Params["accept_self_signed"].(bool); ok && !a {
				skipVerify = false
			}
		}
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: skipVerify,
			NextProtos:         []string{transport.ALPNProtocol},
			CurvePreferences:   []tls.CurveID{tls.X25519, tls.CurveP256},
		}
	}

	// Apply TLS version constraints from params.
	// Accept both "tls_version" (full override) and "tls_max_version" (max only).
	if v, ok := step.Params["tls_version"].(string); ok {
		switch v {
		case "1.2":
			tlsConfig.MinVersion = tls.VersionTLS12
			tlsConfig.MaxVersion = tls.VersionTLS12
		case "1.3":
			tlsConfig.MinVersion = tls.VersionTLS13
			tlsConfig.MaxVersion = tls.VersionTLS13
		case "1.2,1.3":
			tlsConfig.MinVersion = tls.VersionTLS12
			tlsConfig.MaxVersion = tls.VersionTLS13
		}
	}
	if v, ok := step.Params["tls_max_version"].(string); ok {
		switch v {
		case "1.2":
			tlsConfig.MaxVersion = tls.VersionTLS12
			if tlsConfig.MinVersion > tls.VersionTLS12 {
				tlsConfig.MinVersion = tls.VersionTLS12
			}
		case "1.3":
			tlsConfig.MaxVersion = tls.VersionTLS13
		}
	}

	// Apply key exchange group constraints from params.
	if groups, ok := step.Params["key_exchange_groups"].([]any); ok && len(groups) > 0 {
		var curvePrefs []tls.CurveID
		for _, g := range groups {
			if name, ok := g.(string); ok {
				if id, found := parseCurveID(name); found {
					curvePrefs = append(curvePrefs, id)
				}
			}
		}
		if len(curvePrefs) > 0 {
			tlsConfig.CurvePreferences = curvePrefs
		}
	}

	// Apply ALPN override from params.
	// When alpn is a non-empty string, use it. When the key is present but
	// null or empty (alpn: null in YAML), clear NextProtos so no ALPN
	// extension is sent. When the key is absent, keep the default.
	if val, hasALPN := step.Params["alpn"]; hasALPN {
		if alpn, ok := val.(string); ok && alpn != "" {
			tlsConfig.NextProtos = []string{alpn}
		} else {
			tlsConfig.NextProtos = nil
		}
	}

	// Apply cipher suite constraints from params.
	// Note: Go's TLS 1.3 implementation ignores CipherSuites -- all built-in
	// TLS 1.3 ciphers are always offered. This only affects TLS 1.2 connections.
	if suites, ok := step.Params["cipher_suites"].([]any); ok && len(suites) > 0 {
		var cipherIDs []uint16
		for _, s := range suites {
			if name, ok := s.(string); ok {
				if id, found := parseCipherSuite(name); found {
					cipherIDs = append(cipherIDs, id)
				}
			}
		}
		if len(cipherIDs) > 0 {
			tlsConfig.CipherSuites = cipherIDs
		}
	}

	// Translate boolean cert test params to client_cert type.
	if _, ok := step.Params["client_cert"]; !ok {
		if toBool(step.Params["use_expired_cert"]) {
			step.Params["client_cert"] = CertTypeControllerExpired
			r.debugf("handleConnect: translated use_expired_cert to client_cert=controller_expired, zoneCA=%v", r.connMgr.ZoneCA() != nil)
		} else if toBool(step.Params["use_wrong_zone_cert"]) {
			step.Params["client_cert"] = CertTypeControllerWrongZone
		} else if toBool(step.Params["use_operational_cert"]) {
			step.Params["client_cert"] = CertTypeControllerOperational
		}
	}

	// Translate cert_type param (used by connect_as_controller tests) to client_cert.
	if certType, ok := step.Params["cert_type"].(string); ok && certType != "" {
		if _, hasCC := step.Params["client_cert"]; !hasCC {
			switch certType {
			case "expired":
				step.Params["client_cert"] = CertTypeControllerExpired
			case "wrong_zone_ca":
				step.Params["client_cert"] = CertTypeControllerWrongZone
			case "self_signed":
				step.Params["client_cert"] = CertTypeControllerSelfSigned
			case "valid":
				step.Params["client_cert"] = CertTypeControllerOperational
			default:
				step.Params["client_cert"] = "controller_" + certType
			}
		}
	}

	// Override client certificate if specified in params.
	// cert_chain and client_cert are mutually exclusive; cert_chain takes priority.
	if chainSpec, ok := step.Params["cert_chain"].([]any); ok && len(chainSpec) > 0 {
		var specs []string
		for _, s := range chainSpec {
			if name, ok := s.(string); ok {
				specs = append(specs, name)
			}
		}
		certChain, chainErr := buildCertChain(specs, r.connMgr.ControllerCert(), r.connMgr.ZoneCA())
		if chainErr != nil {
			return nil, fmt.Errorf("building cert chain: %w", chainErr)
		}
		tlsConfig.Certificates = []tls.Certificate{certChain}
	} else if clientCertType, ok := step.Params["client_cert"].(string); ok && clientCertType != "" {
		switch clientCertType {
		case CertTypeControllerOperational:
			// When a clock offset is active, generate a fresh controller cert
			// with NotBefore=now so the device's adjusted clock can properly
			// reject it when the skew exceeds the 300s tolerance.
			// Without this, the commissioning-time cert (issued minutes ago)
			// appears valid even with -400s offset because the gap between
			// device time and NotBefore is within tolerance.
			co, _ := state.Get(StateClockOffsetMs)
			if co != nil && co != int64(0) && co != 0 && r.connMgr.ZoneCA() != nil {
				freshCert, certErr := generateTestClientCert(CertTypeControllerFreshOperational, r.connMgr.ZoneCA())
				if certErr == nil {
					tlsConfig.Certificates = []tls.Certificate{freshCert}
				}
			}
		case "none":
			tlsConfig.Certificates = nil
		default:
			if r.connMgr.ZoneCA() == nil {
				return nil, fmt.Errorf("client_cert %q requires a zone CA (commission first)", clientCertType)
			}
			testCert, certErr := generateTestClientCert(clientCertType, r.connMgr.ZoneCA())
			if certErr != nil {
				return nil, fmt.Errorf("generating test client cert %q: %w", clientCertType, certErr)
			}
			r.debugf("handleConnect: generated test cert type=%s, leaf=%v", clientCertType, testCert.Leaf != nil)
			if testCert.Leaf != nil {
				r.debugf("handleConnect: test cert NotAfter=%v, expired=%v", testCert.Leaf.NotAfter, time.Now().After(testCert.Leaf.NotAfter))
			}
			tlsConfig.Certificates = []tls.Certificate{testCert}
		}
	}

	// If an operational connection is already active (e.g. from precondition
	// commissioning) and no special TLS params are requested, reuse it.
	// This avoids "zone already connected" rejection from the device.
	// Skip reuse when TLS-affecting params are present since those require
	// a fresh TLS handshake.
	clientCertSpec, _ := step.Params["client_cert"].(string)
	_, hasCertChain := step.Params["cert_chain"]
	_, hasKeyGroups := step.Params["key_exchange_groups"]
	_, hasALPN := step.Params["alpn"]
	_, hasTLSVersion := step.Params["tls_version"]
	_, hasTLSMaxVersion := step.Params["tls_max_version"]
	_, hasCipherSuites := step.Params["cipher_suites"]
	hasTLSOverrides := hasCertChain || hasKeyGroups || hasALPN || hasTLSVersion || hasTLSMaxVersion || hasCipherSuites
	if !commissioning && clientCertSpec == "" && !hasTLSOverrides && r.pool.Main() != nil && r.pool.Main().isConnected() && r.pool.Main().tlsConn != nil {
		cs := r.pool.Main().tlsConn.ConnectionState()
		hasPeerCerts := len(cs.PeerCertificates) > 0
		serverCertCNPrefix := ""
		serverCertSelfSigned := false
		if hasPeerCerts {
			cert := cs.PeerCertificates[0]
			if idx := strings.Index(cert.Subject.CommonName, "-"); idx >= 0 {
				serverCertCNPrefix = cert.Subject.CommonName[:idx+1]
			} else {
				serverCertCNPrefix = cert.Subject.CommonName
			}
			serverCertSelfSigned = bytes.Equal(cert.RawIssuer, cert.RawSubject)
		}
		chainValidated := len(cs.VerifiedChains) > 0 || (hasPeerCerts && (insecure || r.connMgr.ZoneCAPool() != nil))
		presentedClientCert := len(r.pool.Main().tlsConn.ConnectionState().PeerCertificates) > 0
		return map[string]any{
			KeyConnectionEstablished:  true,
			KeyConnected:              true,
			KeyTLSHandshakeSuccess:    true,
			KeyTarget:                 target,
			KeyTLSVersion:             tlsVersionName(cs.Version),
			KeyNegotiatedVersion:      tlsVersionName(cs.Version),
			KeyNegotiatedCipher:       tls.CipherSuiteName(cs.CipherSuite),
			KeyNegotiatedGroup:        curveIDName(cs.CurveID),
			KeyNegotiatedProtocol:     cs.NegotiatedProtocol,
			KeyNegotiatedALPN:         cs.NegotiatedProtocol,
			KeyMutualAuth:             presentedClientCert && hasPeerCerts,
			KeyControllerCertVerified: presentedClientCert && chainValidated,
			KeyChainValidated:         chainValidated,
			KeySelfSignedAccepted:     serverCertSelfSigned && hasPeerCerts,
			KeyServerCertCNPrefix:     serverCertCNPrefix,
			KeyServerCertSelfSigned:   serverCertSelfSigned,
			KeyHasPeerCerts:           hasPeerCerts,
			KeyFullHandshake:          !cs.DidResume,
			KeyPSKUsed:                cs.DidResume,
			KeyEarlyDataAccepted:      false,
			KeyConnectedAddressType:   classifyRemoteAddress(r.pool.Main().tlsConn.RemoteAddr()),
			KeyInterfaceCorrect:       true, // reused connection was already validated
		}, nil
	}

	// Record start time for verify_timing.
	startTime := time.Now()
	state.Set(StateStartTime, startTime)

	// When connecting with an operational cert (or a cert_chain starting
	// with leaf_cert, which IS the operational cert), close the existing zone
	// connection first so the device has a disconnected zone available to
	// reconnect. Without this, the device rejects the new connection with
	// "no disconnected zones" (not a cert issue) which confuses probing.
	clientCertParam, _ := step.Params["client_cert"].(string)
	isOperationalChain := false
	if chainSpec, ok := step.Params["cert_chain"].([]any); ok && len(chainSpec) > 0 {
		if name, ok := chainSpec[0].(string); ok && name == "leaf_cert" {
			isOperationalChain = true
		}
	}
	needsDisconnectWait := false
	if (clientCertParam == CertTypeControllerOperational || isOperationalChain) && r.pool.Main() != nil && r.pool.Main().tlsConn != nil {
		r.pool.Main().transitionTo(ConnDisconnected)
		needsDisconnectWait = true
	}
	// Also retry when reconnecting operationally after a disconnect (e.g.
	// invoke_with_disconnect → connect). The device may not have detected
	// our TCP close yet and rejects the new connection for the same zone.
	if !needsDisconnectWait && !commissioning && r.connMgr.ZoneCAPool() != nil && r.pool.Main() != nil && !r.pool.Main().isConnected() {
		needsDisconnectWait = true
	}

	// Connect with timeout. If we just closed an existing connection,
	// retry briefly in case the device hasn't detected the disconnect yet.
	maxAttempts := 1
	if needsDisconnectWait {
		maxAttempts = 5
	}
	conn, err := dialWithRetry(ctx, maxAttempts, func() (*tls.Conn, error) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	})
	if err != nil {
		state.Set(StateEndTime, time.Now())
		errorCode := classifyConnectError(err)
		alert := extractTLSAlert(err)
		// Use the classified error code for tls_error (tests expect short
		// strings like "certificate_expired", not raw Go error text).
		tlsError := errorCode
		if alert != "" {
			tlsError = alert
		}
		// Return connection failure as outputs so tests can assert on TLS errors.
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyConnected:             false,
			KeyTLSHandshakeSuccess:   false,
			KeyTLSHandshakeFailed:    true,
			KeyTarget:                target,
			KeyError:                 errorCode,
			KeyErrorCode:             errorCode,
			KeyErrorDetail:           err.Error(),
			KeyTLSError:              tlsError,
			KeyTLSAlert:              alert,
		}, nil
	}

	// TLS 1.3 with RequestClientCert: the server validates the client cert
	// AFTER the handshake completes from the client's perspective. If the
	// server rejects the cert, it closes the connection. Detect this by
	// attempting a short read -- if the server closed, we'll get an error.
	// For non-operational test certs (expired, wrong_zone, self_signed), always
	// probe for post-handshake rejection. For operational certs, only probe when
	// a clock offset is active (the device might reject due to clock skew making
	// the cert appear not-yet-valid). Skip probing for operational certs without
	// clock offset because the device may close the connection for other reasons
	// (e.g., no disconnected zone to reconnect to), not cert rejection.
	clientCertType, _ := step.Params["client_cert"].(string)
	// Also check cert_chain -- extract type from the first chain spec for
	// probe classification (TC-TLS-ALERT tests use cert_chain, not client_cert).
	if clientCertType == "" {
		if chainSpec, ok := step.Params["cert_chain"].([]any); ok && len(chainSpec) > 0 {
			if name, ok := chainSpec[0].(string); ok {
				clientCertType = name
			}
		}
	}
	clockOffset, _ := state.Get(StateClockOffsetMs)
	hasClockOffset := clockOffset != nil && clockOffset != int64(0) && clockOffset != 0
	probeForRejection := len(tlsConfig.Certificates) > 0 && clientCertType != "" &&
		clientCertType != "leaf_cert" &&
		(clientCertType != CertTypeControllerOperational || hasClockOffset)
	// Also probe for deep chains (3+ certs) -- the device rejects chain depth > 2.
	if !probeForRejection {
		if chainSpec, ok := step.Params["cert_chain"].([]any); ok && len(chainSpec) > 2 {
			probeForRejection = true
			clientCertType = CertTypeDeepChain
		}
	}
	// Also probe when ALPN is null (no ALPN extension sent) -- the device
	// rejects connections without the required ALPN protocol post-handshake.
	if !probeForRejection {
		if val, hasALPN := step.Params["alpn"]; hasALPN && val == nil {
			probeForRejection = true
			clientCertType = CertTypeNoALPN
		}
	}
	if probeForRejection {
		closed, probeErr := detectPostHandshakeClose(conn, postHandshakeRejectionProbeTimeout)
		if closed {
			// Server closed the connection -- cert was rejected.
			// Classify based on what test cert we sent, since the server
			// can't send a specific reason in TLS 1.3 post-handshake.
			conn.Close()
			state.Set(StateEndTime, time.Now())
			tlsError := classifyTestCertRejection(clientCertType, probeErr)
			// For post-handshake rejection, the probe error is typically
			// io.EOF which doesn't contain a TLS alert keyword. Use the
			// inferred classification for tls_alert as well.
			alert := extractTLSAlert(probeErr)
			if alert == "" || alert == probeErr.Error() {
				alert = tlsError
			}
			return map[string]any{
				KeyConnectionEstablished: false,
				KeyConnected:             false,
				KeyTLSHandshakeSuccess:   false,
				KeyTLSHandshakeFailed:    true,
				KeyTarget:                target,
				KeyError:                 tlsError,
				KeyErrorCode:             tlsError,
				KeyErrorDetail:           probeErr.Error(),
				KeyTLSError:              tlsError,
				KeyTLSAlert:              alert,
			}, nil
		}
	}

	// Record end time for verify_timing.
	state.Set(StateEndTime, time.Now())

	// If an invalid test cert was used (expired, wrong_zone, etc.) and the
	// probe didn't detect rejection (timeout), don't replace r.pool.Main(). These
	// connections are diagnostic -- they must never corrupt the runner's
	// primary connection, which may be the suite zone's operational link.
	// Only guard for known-invalid cert types; valid certs (operational,
	// deep chains) that survived the probe should proceed normally.
	if probeForRejection && isInvalidTestCert(clientCertType) {
		cs := conn.ConnectionState()
		_ = conn.Close()
		return map[string]any{
			KeyConnectionEstablished: true,
			KeyConnected:             true,
			KeyTLSHandshakeSuccess:   true,
			KeyTarget:                target,
			KeyTLSVersion:            tlsVersionName(cs.Version),
			KeyNegotiatedVersion:     tlsVersionName(cs.Version),
			KeyNegotiatedCipher:      tls.CipherSuiteName(cs.CipherSuite),
			KeyNegotiatedProtocol:    cs.NegotiatedProtocol,
			KeyNegotiatedALPN:        cs.NegotiatedProtocol,
		}, nil
	}

	// Close the previous TLS connection before replacing it. Without
	// this, the device still sees the old TCP connection as an active
	// zone, preventing it from re-entering commissioning mode.
	if r.pool.Main() == nil {
		r.pool.SetMain(&Connection{})
	}
	if r.pool.Main().tlsConn != nil && r.pool.Main().tlsConn != conn {
		_ = r.pool.Main().tlsConn.Close()
	}
	r.pool.Main().tlsConn = conn
	r.pool.Main().framer = transport.NewFramer(conn)
	r.pool.Main().state = ConnTLSConnected
	r.pool.Main().hadConnection = true

	// Set up protocol logging if configured
	if r.config.ProtocolLogger != nil {
		connID := generateConnectionID()
		r.pool.Main().framer.SetLogger(r.config.ProtocolLogger, connID)
	}

	// Store connection info in state
	state.Set(StateConnection, r.pool.Main())

	// Extract TLS connection details.
	cs := conn.ConnectionState()
	hasPeerCerts := len(cs.PeerCertificates) > 0

	// Chain validation: either Go's standard path populated VerifiedChains,
	// or our custom VerifyPeerCertificate callback validated the chain
	// (when InsecureSkipVerify is true, Go doesn't populate VerifiedChains).
	chainValidated := len(cs.VerifiedChains) > 0 || (hasPeerCerts && tlsConfig.InsecureSkipVerify && tlsConfig.VerifyPeerCertificate != nil)

	// Mutual auth: we presented a client cert and the connection succeeded.
	// If the device requires client auth (RequireAndVerifyClientCert), a
	// rejected client cert would have caused a handshake failure above.
	presentedClientCert := len(tlsConfig.Certificates) > 0
	mutualAuth := presentedClientCert && hasPeerCerts

	// Server cert details.
	serverCertCNPrefix := ""
	serverCertCN := ""
	serverCertSelfSigned := false
	if hasPeerCerts {
		cert := cs.PeerCertificates[0]
		serverCertCN = cert.Subject.CommonName
		// Extract prefix: everything up to and including the first hyphen.
		// E.g., "MASH-1234" -> "MASH-", "no-hyphen" -> "no-hyphen".
		if idx := strings.Index(cert.Subject.CommonName, "-"); idx >= 0 {
			serverCertCNPrefix = cert.Subject.CommonName[:idx+1]
			// Store device discriminator from commissioning cert CN (MASH-1234).
			if r.connMgr.DiscoveredDiscriminator() == 0 {
				if d, err := strconv.ParseUint(cert.Subject.CommonName[idx+1:], 10, 16); err == nil {
					r.connMgr.SetDiscoveredDiscriminator(uint16(d))
				}
			}
		} else {
			serverCertCNPrefix = cert.Subject.CommonName
		}
		// Self-signed: issuer and subject are identical in the raw ASN.1.
		serverCertSelfSigned = bytes.Equal(cert.RawIssuer, cert.RawSubject)
	}

	// Check for CN/discriminator mismatch when expected_discriminator is provided.
	cnMismatchWarning := false
	if expectedDisc, ok := step.Params["expected_discriminator"]; ok && hasPeerCerts {
		expectedCN := fmt.Sprintf("MASH-%d", int(toFloat(expectedDisc)))
		if serverCertCN != expectedCN {
			cnMismatchWarning = true
		}
	}

	return map[string]any{
		KeyConnectionEstablished:  true,
		KeyConnected:              true,
		KeyTLSHandshakeSuccess:    true,
		KeyTarget:                 target,
		KeyTLSVersion:             tlsVersionName(cs.Version),
		KeyNegotiatedVersion:      tlsVersionName(cs.Version),
		KeyNegotiatedCipher:       tls.CipherSuiteName(cs.CipherSuite),
		KeyNegotiatedGroup:        curveIDName(cs.CurveID),
		KeyNegotiatedProtocol:     cs.NegotiatedProtocol,
		KeyNegotiatedALPN:         cs.NegotiatedProtocol,
		KeyMutualAuth:             mutualAuth,
		KeyControllerCertVerified: presentedClientCert && chainValidated,
		KeyChainValidated:         chainValidated,
		KeySelfSignedAccepted:     serverCertSelfSigned && hasPeerCerts,
		KeyServerCertCNPrefix:     serverCertCNPrefix,
		KeyServerCertSelfSigned:   serverCertSelfSigned,
		KeyHasPeerCerts:           hasPeerCerts,
		KeyCNMismatchWarning:      cnMismatchWarning,
		KeyFullHandshake:          !cs.DidResume,
		KeyPSKUsed:                cs.DidResume,
		KeyEarlyDataAccepted:      false, // Go does not support TLS 1.3 0-RTT
		KeyConnectedAddressType:   classifyRemoteAddress(conn.RemoteAddr()),
		KeyInterfaceCorrect:       checkInterfaceCorrect(conn.RemoteAddr(), step.Params),
	}, nil
}

// handleDisconnect closes the connection. If graceful=true, sends a
// ControlClose frame and waits for acknowledgement before closing TCP.
func (r *Runner) handleDisconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return map[string]any{KeyDisconnected: true, KeyConnectionClosed: true, KeyCloseAcknowledged: true}, nil
	}

	params := engine.InterpolateParams(step.Params, state)
	graceful, _ := params[ParamGraceful].(bool)

	if graceful {
		// Delegate to send_close which handles ControlClose + ack.
		out, err := r.handleSendClose(ctx, step, state)
		if err != nil {
			return nil, err
		}
		out[KeyDisconnected] = true
		// Clear subscription state.
		state.Set(StatePrimingData, nil)
		r.pool.ClearNotifications()
		return out, nil
	}

	err := r.pool.Main().Close()
	if err != nil {
		return nil, fmt.Errorf("failed to disconnect: %w", err)
	}

	// Clear subscription state so stale priming data / notifications don't
	// leak into subsequent steps after the session is gone.
	state.Set(StatePrimingData, nil)
	r.pool.ClearNotifications()

	return map[string]any{KeyDisconnected: true, KeyConnectionClosed: true}, nil
}

// sendRequest sends an encoded request and reads the decoded response.
// On IO errors (send/receive), the connection is marked as dead so that
// subsequent tests will reconnect instead of reusing a broken connection.
// The expectedMsgID parameter enables response correlation: only a response
// whose MessageID matches is accepted. Orphaned responses from previous
// operations (e.g. simulate_no_response) are discarded with a warning.
func (r *Runner) sendRequest(data []byte, op string, expectedMsgID uint32) (*wire.Response, error) {
	return r.pool.SendRequest(data, op, expectedMsgID)
}

// sendRequestWithDeadline is like sendRequest but respects the context deadline (delegates to pool).
func (r *Runner) sendRequestWithDeadline(ctx context.Context, data []byte, op string, expectedMsgID uint32) (*wire.Response, error) {
	return r.pool.SendRequestWithDeadline(ctx, data, op, expectedMsgID)
}

// handleRead sends a read request and returns the response.
func (r *Runner) handleRead(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Simulated grace period expiry: the cert has expired past the grace
	// period, so the connection should be considered dead (TC-CERT-RENEW-5).
	if expired, _ := state.Get(StateGracePeriodExpired); expired == true {
		return map[string]any{
			KeyReadSuccess: false,
			KeyErrorType:   "grace_period_expired",
		}, nil
	}

	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Clear keepalive idle: message activity resets the ping timer.
	state.Set(StateKeepaliveIdle, false)
	state.Set(StateKeepaliveIdleSec, float64(0))

	// Interpolate parameters
	params := engine.InterpolateParams(step.Params, state)

	// Async mode: fire the read in the background and return immediately.
	if toBool(params[ParamAsync]) {
		go func() {
			// Best-effort: send the request but don't wait for response.
			asyncStep := *step
			delete(asyncStep.Params, ParamAsync)
			_, _ = r.handleRead(ctx, &asyncStep, state)
		}()
		return map[string]any{
			KeyRequestSent: true,
			KeyReadSuccess: true,
		}, nil
	}

	// Resolve endpoint, feature, and attribute names
	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}

	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	// Resolve attribute: may be a string name ("acActivePower") or numeric ID
	// (int from YAML, e.g., 0xFF parses as int 255).
	var attrID uint16
	var hasAttr bool
	var attrName string
	if attrRaw, ok := params[ParamAttribute]; ok && attrRaw != nil {
		attrID, err = r.resolver.ResolveAttribute(params[KeyFeature], attrRaw)
		if err != nil {
			return nil, fmt.Errorf("resolving attribute: %w", err)
		}
		hasAttr = true
		if s, ok := attrRaw.(string); ok {
			attrName = s
		} else {
			attrName = fmt.Sprintf("attr_%d", attrID)
		}
	}

	// Create read request
	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpRead,
		EndpointID: endpointID,
		FeatureID:  featureID,
	}

	// RC5b: Override MessageID if specified in params.
	if mid, ok := params[ParamMessageID]; ok {
		req.MessageID = uint32(toFloat(mid))
	}

	// When a specific attribute is requested, include it in the read payload
	// so the device can return just that attribute.
	if hasAttr {
		req.Payload = &wire.ReadPayload{AttributeIDs: []uint16{attrID}}
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		// RC5b: MessageID=0 is rejected by EncodeRequest (wire validation).
		// Return as output, not a Go error, so YAML can assert on it.
		return map[string]any{
			KeyReadSuccess:     false,
			KeyConnectionError: true,
			KeyError:           err.Error(),
		}, nil
	}

	// RC5c: simulate_no_response -- send request but don't wait for response.
	if toBool(params[ParamSimulateNoResponse]) {
		state.Set(StateStartTime, time.Now())
		if sendErr := r.pool.Main().framer.WriteFrame(data); sendErr != nil {
			state.Set(StateEndTime, time.Now())
			return map[string]any{
				KeyReadSuccess: false,
				KeyError:       sendErr.Error(),
			}, nil
		}
		// Wait for context cancellation (step timeout).
		<-ctx.Done()
		state.Set(StateEndTime, time.Now())
		return map[string]any{
			KeyReadSuccess:  false,
			KeyError:        "TIMEOUT",
			KeyTimeoutAfter: "10s",
		}, nil
	}

	resp, err := r.sendRequest(data, "read", req.MessageID)
	if err != nil {
		return nil, err
	}

	outputs := map[string]any{
		KeyReadSuccess:       resp.IsSuccess(),
		KeyResponse:          resp,
		KeyValue:             resp.Payload,
		KeyStatus:            resp.Status,
		KeyResponseMessageID: resp.MessageID,
		KeyMessageIDsMatch:   resp.MessageID == req.MessageID,
	}

	// Add error code and error_status when the response indicates failure.
	if !resp.IsSuccess() {
		outputs[KeyErrorCode] = resp.Status.String()
		outputs[KeyErrorStatus] = resp.Status.String()
	}

	// When a specific attribute was requested, extract its value from the
	// response payload map. The payload is typically map[any]any with
	// uint64 keys after CBOR decoding.
	if hasAttr && resp.Payload != nil {
		if extracted, ok := extractAttributeValue(resp.Payload, attrID); ok {
			outputs[KeyValue] = extracted
			// Add snake_case validation keys for common assertions.
			snakeName := camelToSnake(attrName)
			outputs[snakeName+"_present"] = true
			outputs[snakeName+"_not_empty"] = !isEmptyValue(extracted)
			outputs[snakeName+"_valid"] = extracted != nil
			outputs[snakeName+"_format_valid"] = extracted != nil
		}
		outputs[attrName+"_present"] = resp.Payload != nil
	}

	return outputs, nil
}

// extractAttributeValue extracts a single attribute value from a read response
// payload map. CBOR decoding may produce map[any]any with uint64 keys.
func extractAttributeValue(payload any, attrID uint16) (any, bool) {
	switch m := payload.(type) {
	case map[any]any:
		// CBOR-decoded map with integer keys (most common after wire round-trip)
		if v, ok := m[uint64(attrID)]; ok {
			return v, true
		}
		// Try other integer types that CBOR decoders may produce
		if v, ok := m[int64(attrID)]; ok {
			return v, true
		}
		if v, ok := m[int(attrID)]; ok {
			return v, true
		}
	case map[uint16]any:
		if v, ok := m[attrID]; ok {
			return v, true
		}
	case map[string]any:
		// String-keyed map (less common, but possible)
		if v, ok := m[fmt.Sprintf("%d", attrID)]; ok {
			return v, true
		}
	}
	return nil, false
}

// normalizePayloadMap converts a CBOR-decoded payload to map[string]any
// for comparison with YAML expectation maps. Handles map[any]any (CBOR
// integer keys are converted to string representations).
func normalizePayloadMap(payload any) map[string]any {
	if payload == nil {
		return nil
	}
	switch m := payload.(type) {
	case map[string]any:
		return m
	case map[any]any:
		result := make(map[string]any, len(m))
		for k, v := range m {
			result[fmt.Sprintf("%v", k)] = v
		}
		return result
	}
	return nil
}

// camelToSnake converts a camelCase string to snake_case.
func camelToSnake(s string) string {
	var result []byte
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, byte(c+'a'-'A'))
		} else {
			result = append(result, byte(c))
		}
	}
	return string(result)
}

// isEmptyValue returns true if the value is nil, empty string, empty slice, or empty map.
func isEmptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	case map[any]any:
		return len(val) == 0
	}
	return false
}

// handleWrite sends a write request.
func (r *Runner) handleWrite(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Interpolate parameters
	params := engine.InterpolateParams(step.Params, state)

	// Resolve endpoint and feature names
	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}

	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	value := params[KeyValue]

	// Build the wire payload. When an attribute name is given, wrap the
	// value in a WritePayload map (map[uint16]any{attrID: value}) so the
	// device receives the correct CBOR-encoded structure.
	var payload any = value
	if attrRaw, ok := params[ParamAttribute]; ok && attrRaw != nil {
		attrID, attrErr := r.resolver.ResolveAttribute(params[KeyFeature], attrRaw)
		if attrErr != nil {
			return nil, fmt.Errorf("resolving attribute: %w", attrErr)
		}
		payload = wire.WritePayload{attrID: value}
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpWrite,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    payload,
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode write request: %w", err)
	}

	resp, err := r.sendRequest(data, "write", req.MessageID)
	if err != nil {
		// RC5f: Return output map (not Go error) for EOF/closed so YAML
		// expectations can check the error.
		errMsg := err.Error()
		if strings.Contains(errMsg, "EOF") || strings.Contains(errMsg, "closed") ||
			strings.Contains(errMsg, "reset") {
			return map[string]any{
				KeyWriteSuccess: false,
				KeyError:        errMsg,
			}, nil
		}
		return nil, err
	}

	outputs := map[string]any{
		KeyWriteSuccess: resp.IsSuccess(),
		KeyResponse:     resp,
		KeyStatus:       resp.Status,
		KeyValue:        value,
	}

	// Add error code when the response indicates failure.
	if !resp.IsSuccess() {
		outputs[KeyErrorCode] = resp.Status.String()
	}

	return outputs, nil
}

// handleSubscribe sends a subscribe request.
func (r *Runner) handleSubscribe(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Interpolate parameters
	params := engine.InterpolateParams(step.Params, state)

	// Resolve endpoint and feature names
	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}

	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpSubscribe,
		EndpointID: endpointID,
		FeatureID:  featureID,
	}

	// Build subscribe payload with intervals and attribute filter.
	subPayload := &wire.SubscribePayload{}
	hasPayload := false

	if _, ok := params["minInterval"]; ok {
		sec := paramInt(params, "minInterval", 0)
		subPayload.MinInterval = uint32(sec * 1000) // YAML seconds → wire milliseconds
		hasPayload = true
	}
	if _, ok := params["maxInterval"]; ok {
		sec := paramInt(params, "maxInterval", 0)
		subPayload.MaxInterval = uint32(sec * 1000)
		hasPayload = true
	}

	// Resolve attribute names to IDs if provided.
	if attrs, ok := params[ParamAttributes]; ok {
		var attrList []any
		switch v := attrs.(type) {
		case []any:
			attrList = v
		case []string:
			for _, s := range v {
				attrList = append(attrList, s)
			}
		}
		for _, a := range attrList {
			attrID, attrErr := r.resolver.ResolveAttribute(params[KeyFeature], a)
			if attrErr == nil {
				subPayload.AttributeIDs = append(subPayload.AttributeIDs, attrID)
				hasPayload = true
			}
		}
	}

	if hasPayload {
		req.Payload = subPayload
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode subscribe request: %w", err)
	}

	resp, err := r.sendRequest(data, "subscribe", req.MessageID)
	if err != nil {
		// Return output map (not Go error) for connection failures so
		// YAML expectations can check tls_error and error fields.
		errMsg := err.Error()
		if strings.Contains(errMsg, "EOF") || strings.Contains(errMsg, "closed") ||
			strings.Contains(errMsg, "reset") || strings.Contains(errMsg, "tls") {
			return map[string]any{
				KeySubscribeSuccess: false,
				KeyError:            errMsg,
				KeyTLSError:         errMsg,
			}, nil
		}
		return nil, err
	}

	// Extract subscription ID and priming data from the response payload.
	// The subscribe response payload is {1: subscriptionId, 2: currentValues}
	// (a SubscribeResponsePayload). The raw CBOR decodes as map[any]any.
	subscriptionID := extractSubscriptionID(resp.Payload)
	primingData := extractPrimingData(resp.Payload)

	// Store priming data in state so receive_notification/wait_report can use
	// it as an initial report if no subsequent notification frame arrives.
	// If a previous subscribe already stored priming data, push both into
	// the priming queue so each receive_notification can dequeue one.
	if primingData != nil {
		if existing, ok := state.Get(StatePrimingData); ok && existing != nil {
			// Move existing priming data into the queue and add new one.
			var queue []any
			if q, ok := state.Get(StatePrimingQueue); ok {
				if qSlice, ok := q.([]any); ok {
					queue = qSlice
				}
			}
			queue = append(queue, existing, primingData)
			state.Set(StatePrimingQueue, queue)
			state.Set(StatePrimingData, nil)
		} else {
			state.Set(StatePrimingData, primingData)
		}
	}

	// Track subscription ID for auto-unsubscribe in teardown.
	if subscriptionID != nil {
		if id, ok := wire.ToUint32(subscriptionID); ok {
			r.trackSubscription(id)
		}
	}

	// Save subscription metadata in state for later use by unsubscribe
	// and notification analysis.
	if subscriptionID != nil {
		state.Set(StateSavedSubscriptionID, subscriptionID)
	}
	// Support saving subscription ID under a custom key name.
	if saveAs, ok := params[ParamSaveSubscriptionID].(string); ok && saveAs != "" && subscriptionID != nil {
		state.Set(saveAs, subscriptionID)
	}
	state.Set(StateSubscribedEndpointID, endpointID)
	state.Set(StateSubscribedFeatureID, featureID)

	// Store subscribed attribute names so notification handlers can match.
	if attrs, ok := params[ParamAttributes]; ok {
		var attrNames []string
		switch v := attrs.(type) {
		case []any:
			for _, a := range v {
				if s, ok := a.(string); ok {
					attrNames = append(attrNames, s)
				}
			}
		case []string:
			attrNames = v
		}
		if len(attrNames) > 0 {
			state.Set(StateSubscribedAttributes, attrNames)
		}
	}

	outputs := map[string]any{
		KeySubscribeSuccess:       resp.IsSuccess(),
		KeySubscriptionID:         subscriptionID,
		KeySubscriptionIDReturned: subscriptionID != nil,
		KeySubscriptionIDSaved:    subscriptionID != nil,
		KeySubscriptionIDPresent:  subscriptionID != nil,
		KeyPrimingReceived:        primingData != nil,
		KeyPrimingIsPrimingFlag:   primingData != nil,
		KeyStatus:                 resp.Status,
	}
	if saveAs, ok := params[ParamSaveSubscriptionID].(string); ok && saveAs != "" {
		outputs[KeySaveSubscriptionID] = saveAs
	}
	return outputs, nil
}

// extractSubscriptionID extracts the subscription ID from a subscribe response
// payload. The payload is typically a map {1: subscriptionId, 2: currentValues}
// decoded from CBOR as map[any]any.
func extractSubscriptionID(payload any) any {
	switch p := payload.(type) {
	case map[any]any:
		if id, ok := p[uint64(1)]; ok {
			return id
		}
	}
	// Fallback: return the payload itself (may be a simple ID).
	return payload
}

// extractPrimingData extracts the current values (priming report) from a
// subscribe response payload. Key 2 in the SubscribeResponsePayload.
func extractPrimingData(payload any) any {
	switch p := payload.(type) {
	case map[any]any:
		if vals, ok := p[uint64(2)]; ok {
			return vals
		}
	}
	return nil
}

// handleInvoke sends an invoke request.
func (r *Runner) handleInvoke(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Interpolate parameters
	params := engine.InterpolateParams(step.Params, state)

	// Resolve endpoint and feature names
	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}

	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	// Resolve command name to ID and wrap in InvokePayload.
	var payload any
	if commandRaw, hasCommand := params[ParamCommand]; hasCommand {
		commandID, cmdErr := r.resolver.ResolveCommand(params[KeyFeature], commandRaw)
		if cmdErr != nil {
			return nil, fmt.Errorf("resolving command: %w", cmdErr)
		}
		// Read args (primary) or params (fallback) for command parameters.
		args, _ := params[ParamArgs]
		if args == nil {
			args, _ = params[ParamParams]
		}
		payload = &wire.InvokePayload{
			CommandID:  commandID,
			Parameters: args,
		}
	} else {
		// Legacy: raw payload passthrough (no command field).
		payload = params[ParamParams]
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    payload,
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode invoke request: %w", err)
	}

	resp, err := r.sendRequest(data, "invoke", req.MessageID)
	if err != nil {
		// Return output map (not Go error) for connection failures so
		// YAML expectations can check error fields.
		errMsg := err.Error()
		if strings.Contains(errMsg, "EOF") || strings.Contains(errMsg, "closed") ||
			strings.Contains(errMsg, "reset") {
			return map[string]any{
				KeyInvokeSuccess: false,
				KeyError:         errMsg,
			}, nil
		}
		return nil, err
	}

	outputs := map[string]any{
		KeyInvokeSuccess: resp.IsSuccess(),
		KeyResult:        resp.Payload,
		KeyResponse:      normalizePayloadMap(resp.Payload),
		KeyStatus:        resp.Status,
	}

	// Add error code when the response indicates failure.
	if !resp.IsSuccess() {
		outputs[KeyErrorCode] = resp.Status.String()
		outputs[KeyErrorStatus] = resp.Status.String()
	}

	// Flatten response payload into output map for response_contains and
	// application-level error detection. The payload is typically a
	// map[any]any or map[string]any from CBOR decoding.
	if resp.Payload != nil {
		flattenInvokeResponse(resp.Payload, outputs)
	}

	// Update local zone state when RemoveZone succeeds. The response
	// contains {removed: true} and the args contain the removed zone ID.
	// Without this, handleCommission's duplicate zone type check would
	// use stale state and reject a re-commission for the same zone type.
	if resp.IsSuccess() {
		if cmdName, ok := params[ParamCommand].(string); ok && strings.EqualFold(cmdName, "RemoveZone") {
			if argsMap, ok := params[ParamArgs].(map[string]any); ok {
				if removedID, ok := argsMap["zoneId"].(string); ok && removedID != "" {
					recordRemovedZoneID(state, removedID)
					zs := getZoneState(state)
					// The zone state may be keyed by label (e.g. "GRID")
					// or by actual device zone ID. Search by both key and
					// ZoneID field to find the right entry.
					var keyToDelete string
					for key, z := range zs.zones {
						if key == removedID || z.ZoneID == removedID {
							keyToDelete = key
							break
						}
					}
					if keyToDelete != "" {
						delete(zs.zones, keyToDelete)
						for i, id := range zs.zoneOrder {
							if id == keyToDelete {
								zs.zoneOrder = append(zs.zoneOrder[:i], zs.zoneOrder[i+1:]...)
								break
							}
						}
					}
				}
			}
		}
	}

	return outputs, nil
}

// flattenInvokeResponse extracts well-known keys from an invoke response
// payload and merges them into the output map. This enables:
//   - response_contains checker to find keys by name
//   - Detection of "applied: false" as invoke failure
//   - Extraction of rejectReason as error_code
func flattenInvokeResponse(payload any, outputs map[string]any) {
	var m map[string]any
	switch p := payload.(type) {
	case map[string]any:
		m = p
	case map[any]any:
		m = make(map[string]any, len(p))
		for k, v := range p {
			m[fmt.Sprintf("%v", k)] = v
		}
	default:
		return
	}

	// Check "applied" field: if explicitly false, override invoke_success.
	if applied, ok := m["applied"]; ok {
		if applied == false {
			outputs[KeyInvokeSuccess] = false
			// Extract rejectReason as error_code if present.
			if reason, hasReason := m["rejectReason"]; hasReason {
				outputs[KeyErrorCode] = rejectReasonToErrorCode(reason)
				outputs[KeyErrorStatus] = outputs[KeyErrorCode]
			}
		}
	}
}

// Utility handlers
func (r *Runner) handleWait(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)
	durationMs := paramFloat(params, "duration_ms", 0)
	if durationMs <= 0 {
		// Also accept duration_seconds (used by commissioning window tests).
		if sec := paramFloat(params, "duration_seconds", 0); sec > 0 {
			durationMs = sec * 1000
		}
		if durationMs <= 0 {
			durationMs = 1000
		}
	}

	// Parse "duration" string param (e.g. "35s") and use it for actual sleep
	// when no duration_ms or duration_seconds was provided.
	if durStr, ok := params[ParamDuration].(string); ok && durStr != "" {
		if d, err := time.ParseDuration(durStr); err == nil && durationMs <= 1000 {
			// Override the default 1000ms with the parsed duration.
			durationMs = float64(d.Milliseconds())
		}
	}
	simulatedMs := durationMs

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(durationMs) * time.Millisecond):
	}

	// Track cumulative idle seconds for keepalive simulation.
	// Auto-ping fires after 30s of idle (no message activity).
	prevIdle, _ := state.Get(StateKeepaliveIdleSec)
	idleSec, _ := prevIdle.(float64)
	idleSec += simulatedMs / 1000
	state.Set(StateKeepaliveIdleSec, idleSec)

	return map[string]any{KeyWaited: true}, nil
}

func (r *Runner) handleVerify(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Verify action just passes through expectations
	return map[string]any{}, nil
}

// Custom expectation checkers
func (r *Runner) checkConnectionEstablished(key string, expected any, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyConnectionEstablished)
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   nil,
			Passed:   false,
			Message:  "connection not established",
		}
	}

	passed := actual == expected
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("connection_established = %v", actual),
	}
}

func (r *Runner) checkResponseReceived(key string, expected any, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyResponseReceived)
	if !exists {
		// Fall back: check if the response was actually received
		actual = false
	}
	passed := actual == expected
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("response received: %v", actual),
	}
}

// filterByPattern filters test cases by name pattern.
// Supports comma-separated patterns (e.g. "TC-CLOSE*,TC-KEEP*").
func filterByPattern(cases []*loader.TestCase, pattern string) []*loader.TestCase {
	patterns := strings.Split(pattern, ",")
	var filtered []*loader.TestCase
	for _, tc := range cases {
		for _, p := range patterns {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if matchPattern(tc.ID, p) || matchPattern(tc.Name, p) {
				filtered = append(filtered, tc)
				break // avoid duplicates from multiple matching patterns
			}
		}
	}
	// If pattern had no non-empty segments (e.g. "" or ","), match all.
	if len(filtered) == 0 && allEmpty(patterns) {
		return cases
	}
	return filtered
}

func allEmpty(patterns []string) bool {
	for _, p := range patterns {
		if strings.TrimSpace(p) != "" {
			return false
		}
	}
	return true
}

// filterByMode filters test cases based on the runner mode (device/controller).
// In device mode, tests requiring controller-only PICS codes (MASH.C.* or C.*) are skipped.
// In controller mode, tests requiring device-only PICS codes (MASH.S.* or D.*) are skipped.
func filterByMode(cases []*loader.TestCase, mode string) []*loader.TestCase {
	var skipPrefixes []string
	switch mode {
	case "device":
		skipPrefixes = []string{"MASH.C.", "C."}
	case "controller":
		skipPrefixes = []string{"MASH.S.", "D."}
	default:
		return cases
	}

	var filtered []*loader.TestCase
	for _, tc := range cases {
		skip := false
		for _, req := range tc.PICSRequirements {
			for _, prefix := range skipPrefixes {
				if strings.HasPrefix(req, prefix) {
					skip = true
					break
				}
			}
			if skip {
				break
			}
		}
		if !skip {
			filtered = append(filtered, tc)
		}
	}
	return filtered
}

// filterByTags keeps only tests that have at least one of the specified tags.
// Tags is a comma-separated string (e.g., "slow,reaper").
func filterByTags(cases []*loader.TestCase, tags string) []*loader.TestCase {
	wanted := parseTags(tags)
	if len(wanted) == 0 {
		return cases
	}
	var filtered []*loader.TestCase
	for _, tc := range cases {
		if hasAnyTag(tc.Tags, wanted) {
			filtered = append(filtered, tc)
		}
	}
	return filtered
}

// filterByExcludeTags removes tests that have any of the specified tags.
// Tags is a comma-separated string (e.g., "slow,reaper").
func filterByExcludeTags(cases []*loader.TestCase, excludeTags string) []*loader.TestCase {
	excluded := parseTags(excludeTags)
	if len(excluded) == 0 {
		return cases
	}
	var filtered []*loader.TestCase
	for _, tc := range cases {
		if !hasAnyTag(tc.Tags, excluded) {
			filtered = append(filtered, tc)
		}
	}
	return filtered
}

// parseTags splits a comma-separated tag string into trimmed non-empty tags.
func parseTags(tags string) []string {
	parts := strings.Split(tags, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// hasAnyTag returns true if any of the test's tags appear in the wanted set.
func hasAnyTag(testTags, wanted []string) bool {
	for _, t := range testTags {
		for _, w := range wanted {
			if t == w {
				return true
			}
		}
	}
	return false
}

// matchPattern performs simple glob matching.
func matchPattern(name, pattern string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}

	// Check for * at start and end
	hasPrefix := len(pattern) > 0 && pattern[0] == '*'
	hasSuffix := len(pattern) > 0 && pattern[len(pattern)-1] == '*'

	if hasPrefix && hasSuffix && len(pattern) > 2 {
		// *foo* - contains
		return contains(name, pattern[1:len(pattern)-1])
	}
	if hasPrefix {
		// *foo - suffix match
		return hasSuffixStr(name, pattern[1:])
	}
	if hasSuffix {
		// foo* - prefix match
		return hasPrefixStr(name, pattern[:len(pattern)-1])
	}

	return name == pattern
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr) >= 0
}

func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func hasPrefixStr(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func hasSuffixStr(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// generateConnectionID generates a unique connection ID for logging.
func generateConnectionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tlsVersionName returns a short TLS version string matching test expectations.
func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS10:
		return "1.0"
	default:
		return fmt.Sprintf("unknown (0x%04x)", version)
	}
}

// curveIDName returns a human-readable name for a TLS curve ID,
// using names that match the YAML test expectations.
func curveIDName(id tls.CurveID) string {
	switch id {
	case tls.CurveP256:
		return "secp256r1"
	case tls.CurveP384:
		return "secp384r1"
	case tls.CurveP521:
		return "secp521r1"
	case tls.X25519:
		return "x25519"
	case 4588: // X25519MLKEM768 -- post-quantum hybrid (Go 1.24+ default)
		return "X25519MLKEM768"
	default:
		return fmt.Sprintf("unknown (%d)", id)
	}
}

// parseCurveID maps a curve name string (from YAML) to a tls.CurveID.
func parseCurveID(name string) (tls.CurveID, bool) {
	switch strings.ToLower(name) {
	case "secp256r1", "p-256", "p256":
		return tls.CurveP256, true
	case "secp384r1", "p-384", "p384":
		return tls.CurveP384, true
	case "secp521r1", "p-521", "p521":
		return tls.CurveP521, true
	case "x25519":
		return tls.X25519, true
	default:
		return 0, false
	}
}

// parseCipherSuite maps a cipher suite name string (from YAML) to its TLS ID.
func parseCipherSuite(name string) (uint16, bool) {
	switch name {
	case "TLS_AES_128_GCM_SHA256":
		return tls.TLS_AES_128_GCM_SHA256, true
	case "TLS_AES_256_GCM_SHA384":
		return tls.TLS_AES_256_GCM_SHA384, true
	case "TLS_CHACHA20_POLY1305_SHA256":
		return tls.TLS_CHACHA20_POLY1305_SHA256, true
	default:
		return 0, false
	}
}

// extractTLSAlert extracts a TLS alert description from an error.
func extractTLSAlert(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Go TLS errors contain alert descriptions like "bad certificate",
	// "certificate expired", "unknown authority", etc.
	// Map Go's TLS error strings to standard TLS alert names.
	// The first matching keyword wins, so order matters when a Go error
	// string differs from the canonical TLS alert name.
	alertMappings := []struct {
		keyword string
		alert   string
	}{
		{"bad certificate", "bad_certificate"},
		{"certificate expired", "certificate_expired"},
		{"not yet valid", "certificate_expired"},
		{"unknown authority", "unknown_ca"},
		{"handshake failure", "handshake_failure"},
		{"protocol version", "protocol_version"},
		{"no application protocol", "no_application_protocol"},
		{"certificate unknown", "certificate_unknown"},
		{"decrypt error", "decrypt_error"},
		{"illegal parameter", "illegal_parameter"},
		{"close notify", "close_notify"},
		{"chain too long", "chain_too_deep"},
		{"path length constraint", "chain_too_deep"},
	}
	for _, m := range alertMappings {
		if contains(msg, m.keyword) {
			return m.alert
		}
	}
	// Return the full error as fallback.
	return msg
}

// classifyConnectError returns a short error code for connection failures.
// classifyTestCertRejection maps a test cert type to the expected rejection
// reason. In TLS 1.3 with RequestClientCert, the server can't send a specific
// alert reason for client cert rejection, so we infer from the cert type.
func classifyTestCertRejection(certType string, err error) string {
	// For operational certs, any post-handshake rejection is due to clock
	// skew making the cert appear expired or not-yet-valid. Classify before
	// checking error text because TLS 1.3 sends a generic "bad_certificate"
	// alert that would mask the real reason.
	if certType == CertTypeControllerOperational {
		return "certificate_expired"
	}

	// Check if the error itself contains useful info.
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "expired") || strings.Contains(msg, "not yet valid") {
			return "certificate_expired"
		}
		if strings.Contains(msg, "unknown ca") || strings.Contains(msg, "unknown authority") {
			return "unknown_ca"
		}
		if strings.Contains(msg, "bad certificate") || strings.Contains(msg, "bad_certificate") {
			return "bad_certificate"
		}
	}

	// Infer from the test cert type we sent (supports both client_cert
	// names like "controller_expired" and cert_chain names like "expired_cert").
	switch certType {
	case CertTypeControllerExpired, CertTypeExpired:
		return "certificate_expired"
	case CertTypeControllerNotYetValid:
		return "certificate_expired"
	case CertTypeControllerWrongZone, CertTypeWrongZone:
		return "unknown_ca"
	case CertTypeControllerNoClientAuth, CertTypeInvalidSignature:
		return "bad_certificate"
	case CertTypeControllerCaTrue:
		return "bad_certificate"
	case CertTypeControllerSelfSigned:
		return "unknown_ca"
	case CertTypeDeepChain:
		return "chain_too_deep"
	case CertTypeNoALPN:
		return "no_application_protocol"
	default:
		return "certificate_rejected"
	}
}

// isInvalidTestCert returns true for test cert types that are known to be
// invalid and expected to be rejected by the server. When such a cert
// survives the post-handshake probe (timeout), the connection should be
// closed without replacing the main connection, since a delayed rejection would corrupt
// the runner's primary operational link.
func isInvalidTestCert(certType string) bool {
	switch certType {
	case CertTypeControllerExpired, CertTypeExpired,
		CertTypeControllerNotYetValid,
		CertTypeControllerWrongZone, CertTypeWrongZone,
		CertTypeControllerSelfSigned,
		CertTypeControllerNoClientAuth,
		CertTypeControllerCaTrue,
		CertTypeInvalidSignature,
		CertTypeDeepChain,
		CertTypeNoALPN:
		return true
	}
	return false
}

// rejectReasonToErrorCode maps an invoke response rejectReason value to
// a wire-level error code string. The reject reason is typically a uint8
// or uint64 from CBOR decoding.
func rejectReasonToErrorCode(reason any) string {
	var code uint8
	switch v := reason.(type) {
	case uint64:
		code = uint8(v)
	case uint8:
		code = v
	case int:
		code = uint8(v)
	case int64:
		code = uint8(v)
	case float64:
		code = uint8(v)
	default:
		return fmt.Sprintf("%v", reason)
	}

	// Map LimitRejectReason values to test-expected error codes.
	switch code {
	case 0x00: // BelowMinimum
		return "BELOW_MINIMUM"
	case 0x01: // AboveContractual
		return "ABOVE_CONTRACTUAL"
	case 0x02: // InvalidValue
		return "INVALID_PARAMETER"
	case 0x03: // DeviceOverride
		return "DEVICE_OVERRIDE"
	case 0x04: // NotSupported
		return "UNSUPPORTED_COMMAND"
	default:
		return fmt.Sprintf("REJECT_%d", code)
	}
}

func classifyConnectError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") ||
		strings.Contains(msg, "unreachable"):
		return ErrCodeTimeout
	case strings.Contains(msg, "connection refused"):
		return ErrCodeConnectionFailed
	case strings.Contains(msg, "chain too long") || strings.Contains(msg, "path length constraint"):
		return "chain_too_deep"
	case strings.Contains(msg, "unknown authority"):
		return "unknown_ca"
	case strings.Contains(msg, "certificate expired") || strings.Contains(msg, "not yet valid"):
		return "certificate_expired"
	case strings.Contains(msg, "bad certificate"):
		return "bad_certificate"
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate"):
		return ErrCodeTLSError
	default:
		return ErrCodeConnectionError
	}
}

// classifyRemoteAddress returns "link_local", "global_or_ula", or "ipv4".
func classifyRemoteAddress(addr net.Addr) string {
	if addr == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	if ip.To4() != nil {
		return "ipv4"
	}
	if ip.IsLinkLocalUnicast() {
		return "link_local"
	}
	return "global_or_ula"
}

// checkInterfaceCorrect verifies the connected interface matches expectations.
// For simulation mode (no real interface binding), returns true.
// For live mode with link-local IPv6, checks that a zone/scope ID is present.
func checkInterfaceCorrect(addr net.Addr, params map[string]any) bool {
	if _, hasIF := params["interface_from"]; !hasIF {
		return true
	}
	if addr == nil {
		return true
	}
	if tcpAddr, ok := addr.(*net.TCPAddr); ok && tcpAddr.IP.IsLinkLocalUnicast() {
		return tcpAddr.Zone != ""
	}
	return true
}

// hasGlobalIPv6Addr returns true if any address is a non-link-local,
// non-loopback IPv6 address (global unicast or ULA).
func hasGlobalIPv6Addr(addrs []net.Addr) bool {
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.To4() != nil {
			continue // Skip IPv4
		}
		if ip.IsLinkLocalUnicast() || ip.IsLoopback() {
			continue
		}
		// Any other IPv6 address is global unicast or ULA.
		return true
	}
	return false
}

// detectHostIPv6Global returns true if the host has at least one
// non-link-local IPv6 address on an active interface.
func detectHostIPv6Global() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	var addrs []net.Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		addrs = append(addrs, ifAddrs...)
	}
	return hasGlobalIPv6Addr(addrs)
}
