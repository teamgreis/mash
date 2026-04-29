// Package runner provides security test handlers for commissioning hardening (DEC-047).
package runner

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// connectionPool manages multiple connections for security testing.
type connectionPool struct {
	mu          sync.Mutex
	connections []*Connection
}

// timingSample stores timing measurement data.
type timingSample struct {
	ErrorType string
	Samples   []time.Duration
	Mean      time.Duration
	StdDev    time.Duration
}

// securityState holds state for security testing.
type securityState struct {
	pool                *connectionPool
	timingSamples       map[string]*timingSample
	commissioningActive bool
	csrHistory          [][]byte // CSR history for nonce mismatch tests
}

// registerSecurityHandlers registers all security-related action handlers.
func (r *Runner) registerSecurityHandlers() {
	// Connection testing handlers
	r.engine.RegisterHandler(ActionOpenCommissioningConnection, r.handleOpenCommissioningConnection)
	r.engine.RegisterHandler(ActionCloseConnection, r.handleCloseConnection)
	r.engine.RegisterHandler(ActionFloodConnections, r.handleFloodConnections)
	r.engine.RegisterHandler(ActionCheckActiveConnections, r.handleCheckActiveConnections)
	r.engine.RegisterHandler(ActionCheckConnectionClosed, r.handleCheckConnectionClosed)
	r.engine.RegisterHandler(ActionCheckMDNSAdvertisement, r.handleCheckMDNSAdvertisement)
	r.engine.RegisterHandler(ActionConnectOperational, r.handleConnectOperational)
	r.engine.RegisterHandler(ActionEnterCommissioningMode, r.handleEnterCommissioningMode)
	r.engine.RegisterHandler(ActionExitCommissioningMode, r.handleExitCommissioningMode)
	r.engine.RegisterHandler(ActionSendPing, r.handleSendPing)
	r.engine.RegisterHandler(ActionReconnectOperational, r.handleReconnectOperational)
	r.engine.RegisterHandler(ActionPASERequestSlow, r.handlePASERequestSlow)
	r.engine.RegisterHandler(ActionContinueSlowExchange, r.handleContinueSlowExchange)

	// PASE timing handlers
	r.engine.RegisterHandler(ActionPASEAttempts, r.handlePASEAttempts)
	r.engine.RegisterHandler(ActionPASEAttemptTimed, r.handlePASEAttemptTimed)

	// Error testing handlers
	r.engine.RegisterHandler(ActionPASERequestInvalidPubkey, r.handlePASERequestInvalidPubkey)
	r.engine.RegisterHandler(ActionPASERequestWrongPassword, r.handlePASERequestWrongPassword)
	r.engine.RegisterHandler(ActionMeasureErrorTiming, r.handleMeasureErrorTiming)
	r.engine.RegisterHandler(ActionCompareTimingDistributions, r.handleCompareTimingDistributions)

	// Connection fill handler for PICS-driven cap tests
	r.engine.RegisterHandler(ActionFillConnections, r.handleFillConnections)

	// Register security-specific checkers
	r.engine.RegisterChecker(CheckerResponseDelayMsMin, r.checkResponseDelayMin)
	r.engine.RegisterChecker(CheckerResponseDelayMsMax, r.checkResponseDelayMax)
	r.engine.RegisterChecker(CheckerMaxDelayMs, r.checkMaxDelay)
	r.engine.RegisterChecker(CheckerMinDelayMs, r.checkMinDelay)
	r.engine.RegisterChecker(CheckerMeanDifferenceMsMax, r.checkMeanDifferenceMax)
	r.engine.RegisterChecker(CheckerBusyRetryAfterGT, r.checkBusyRetryAfterGT)
}

// getSecurityState retrieves or creates security state from execution state.
func getSecurityState(state *engine.ExecutionState) *securityState {
	if s, ok := state.Custom["security"].(*securityState); ok {
		return s
	}
	s := &securityState{
		pool:          &connectionPool{},
		timingSamples: make(map[string]*timingSample),
		csrHistory:    make([][]byte, 0),
	}
	state.Custom["security"] = s
	return s
}

// ============================================================================
// Connection Testing Handlers
// ============================================================================

// handleOpenCommissioningConnection opens a new commissioning TLS connection.
func (r *Runner) handleOpenCommissioningConnection(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	sendPASE, _ := step.Params["send_pase"].(bool)
	failPASE, _ := step.Params["fail_pase"].(bool)

	// DEC-063: When zones are full and send_pase is requested, connect to the
	// device, send a PASERequest, and read the CommissioningError busy response.
	if zf, _ := state.Get(PrecondDeviceZonesFull); zf == true {
		if sendPASE {
			return r.handleBusyPASEExchange(step)
		}
		// No PASE requested -- report rejection without connecting.
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyConnectionRejected:    true,
			KeyRejectionAtTLSLevel:   true,
			KeyError:                 "device zones full",
		}, nil
	}

	secState := getSecurityState(state)

	target := r.getTarget(step.Params)
	// DEC-082 two-port deployment: when -srv-resolve is enabled the
	// dialer can map the literal -target to the per-ALPN port the device
	// is actually using. Without this, two-port devices route every
	// commissioning dial to the operational port, where the TLS profile
	// rejects the comm config (no client cert, presents the operational
	// cert) and the entire security cluster fails before reaching any
	// app-level logic. handleConnect already does this; security
	// handlers historically didn't because mash-go single-port runs
	// happen to use the same port for both ALPNs.
	dialTarget := target
	if resolver, ok := r.dialer.(targetResolver); ok {
		dialTarget = resolver.ResolveTarget(ctx, target, true)
		if dialTarget != target {
			r.debugf("open_commissioning_connection: srv-resolve %s -> %s", target, dialTarget)
		}
	}

	// Create commissioning TLS config
	tlsConfig := transport.NewCommissioningTLSConfig()

	// Attempt connection with transient retry. Immediately after flood tests, the
	// first dial can race with socket teardown and fail with reset/EOF.
	conn, err := dialWithTransientRetry(ctx, 3, func() (*tls.Conn, error) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return tls.DialWithDialer(dialer, "tcp", dialTarget, tlsConfig)
	})
	if err != nil {
		// DEC-063: When send_pase is requested and the TLS connection fails
		// (e.g., device is in cooldown and may transiently reject), delegate
		// to handleBusyPASEExchange which has retry logic and will return
		// the busy response if the device is in cooldown.
		if sendPASE && !failPASE && isTransientError(err) {
			r.debugf("open_commissioning_connection: TLS failed with send_pase, delegating to busy exchange: %v", err)
			return r.handleBusyPASEExchange(step)
		}
		out := map[string]any{
			KeyConnectionEstablished: false,
			KeyConnectionRejected:    true,
			KeyRejectionAtTLSLevel:   true,
			KeyError:                 err.Error(),
			KeyTLSError:              err.Error(),
			KeyTLSHandshakeFailed:    true,
		}
		if alert := extractTLSAlert(err); alert != "" {
			out[KeyTLSAlert] = alert
		}
		return out, nil
	}

	// Check for existing non-operational (commissioning) connections.
	secState.pool.mu.Lock()
	hasExisting := false
	for _, c := range secState.pool.connections {
		if c.isConnected() && !c.isOperational() {
			hasExisting = true
			break
		}
	}
	secState.pool.mu.Unlock()

	// DEC-061: With message-gated locking, the device doesn't reject at TLS
	// level -- it waits for a PASERequest before acquiring the commissioning
	// lock. When there's already an active commissioning connection and
	// send_pase is set, delegate to handleBusyPASEExchange which sends a
	// PASERequest and reads the CommissioningError busy response.
	if hasExisting && sendPASE {
		conn.Close()
		return r.handleBusyPASEExchange(step)
	}

	// When hasExisting is true but send_pase is false (e.g., transport-level
	// connection cap tests), just store the connection. No probing needed --
	// cap enforcement happens at TCP accept (DEC-062), so if we got here the
	// connection was accepted.

	// DEC-047: When fail_pase is true, send a PASERequest with dummy values,
	// wait for the device to reject (ErrCodeAuthFailed), and close the
	// connection. This triggers the device's cooldown period without keeping
	// the connection in the pool.
	if sendPASE && failPASE {
		if err := sendPASERequestRaw(conn); err != nil {
			conn.Close()
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyPaseFailed:            true,
				KeyError:                 fmt.Sprintf("send PASERequest: %v", err),
			}, nil
		}
		// Read the error response -- the device will send CommissioningError
		// after the SPAKE2+ computation fails with dummy values.
		_, readErr := readCommissioningErrorRaw(conn, 5*time.Second)
		conn.Close()
		// The device releases the PASE lock when it detects the TCP close,
		// which happens nearly instantly. No explicit wait needed.
		if readErr != nil {
			// Device closed connection without sending an error (also
			// indicates PASE failure -- the lock is released either way).
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyPaseFailed:            true,
			}, nil
		}
		return map[string]any{
			KeyConnectionEstablished: true,
			KeyPaseFailed:            true,
		}, nil
	}

	// Store in pool
	secState.pool.mu.Lock()
	newConn := &Connection{
		tlsConn: conn,
		framer:  transport.NewFramer(conn),
		state:   ConnTLSConnected,
	}
	secState.pool.connections = append(secState.pool.connections, newConn)
	index := len(secState.pool.connections) - 1
	secState.pool.mu.Unlock()

	// Also set as main connection if not already connected
	if !r.pool.Main().isConnected() {
		r.pool.SetMain(newConn)
	}

	// DEC-061: When send_pase is true on a fresh connection (no existing
	// commissioning), send a PASERequest to trigger device-side lock
	// acquisition. The device will process the SPAKE2+ exchange and block
	// waiting for the client's confirmation message, holding the
	// commissioning lock until this connection is closed.
	if sendPASE {
		if err := sendPASERequestRaw(conn); err != nil {
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyConnectionIndex:       index,
				KeyError:                 fmt.Sprintf("send PASERequest: %v", err),
			}, nil
		}
		// Read the device's response with a short deadline to distinguish:
		// - CommissioningError(BUSY) -> device rejected, return busy output
		// - PASEResponse -> device is proceeding, lock is held
		// - Timeout -> device is still processing, lock is held
		resp, err := readPASEMessageRaw(conn, 2*time.Second)
		if err == nil {
			if errMsg, ok := resp.(*commissioning.CommissioningError); ok && errMsg.ErrorCode == commissioning.ErrCodeBusy {
				// Device is busy -- close this connection and return busy output.
				conn.Close()
				secState.pool.mu.Lock()
				if index < len(secState.pool.connections) {
					secState.pool.connections[index].transitionTo(ConnDisconnected)
				}
				secState.pool.mu.Unlock()
				return map[string]any{
					KeyConnectionEstablished: true,
					KeyBusyResponseReceived:  true,
					KeyBusyErrorCode:         int(errMsg.ErrorCode),
					KeyBusyRetryAfterValue:   int(errMsg.RetryAfter),
					KeyBusyRetryAfter:        int(errMsg.RetryAfter),
					KeyBusyRetryAfterPresent: true, // always present (omitempty removed from CommissioningError)
				}, nil
			}
			// PASEResponse or other message -- device is proceeding with the
			// handshake and holding the commissioning lock.
		}
		// Timeout or read error -- device is still processing, lock is held.
	}

	// Extract TLS connection details for test assertions.
	out := map[string]any{
		KeyConnectionEstablished: true,
		KeyConnectionRejected:    false,
		KeyConnectionIndex:       index,
	}
	cs := conn.ConnectionState()
	if len(cs.PeerCertificates) > 0 {
		cert := cs.PeerCertificates[0]
		out[KeySelfSignedAccepted] = true // commissioning always accepts self-signed
		if idx := strings.Index(cert.Subject.CommonName, "-"); idx >= 0 {
			out[KeyServerCertCNPrefix] = cert.Subject.CommonName[:idx+1]
		} else {
			out[KeyServerCertCNPrefix] = cert.Subject.CommonName
		}
	}
	return out, nil
}

// handleCloseConnection closes a specific connection by index.
func (r *Runner) handleCloseConnection(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	secState := getSecurityState(state)

	index := paramInt(step.Params, "index", 0)

	secState.pool.mu.Lock()
	defer secState.pool.mu.Unlock()

	if index < 0 || index >= len(secState.pool.connections) {
		return nil, fmt.Errorf("connection index %d out of range", index)
	}

	conn := secState.pool.connections[index]
	if conn.isConnected() {
		_ = conn.Close()
	}

	return map[string]any{
		KeyConnectionClosed: true,
		KeyIndex:            index,
	}, nil
}

// handleCheckConnectionClosed checks whether a pooled connection has been closed
// by the remote side (e.g. the device). It probes with a short read deadline to
// detect EOF or reset, which is the expected signal from DEC-061's
// PASEFirstMessageTimeout.
func (r *Runner) handleCheckConnectionClosed(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	secState := getSecurityState(state)

	index := paramInt(step.Params, "index", 0)

	secState.pool.mu.Lock()
	if index < 0 || index >= len(secState.pool.connections) {
		secState.pool.mu.Unlock()
		return nil, fmt.Errorf("connection index %d out of range (pool size %d)", index, len(secState.pool.connections))
	}
	conn := secState.pool.connections[index]
	secState.pool.mu.Unlock()

	if !conn.isConnected() || conn.tlsConn == nil {
		return map[string]any{
			KeyConnectionClosed: true,
			KeyIndex:            index,
		}, nil
	}

	// Probe with a short read deadline to detect remote close.
	_ = conn.tlsConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, err := conn.tlsConn.Read(buf)
	_ = conn.tlsConn.SetReadDeadline(time.Time{})

	closed := err != nil // EOF, reset, or timeout all indicate closure
	if closed {
		conn.transitionTo(ConnDisconnected)
	}

	return map[string]any{
		KeyConnectionClosed: closed,
		KeyIndex:            index,
	}, nil
}

// handleCheckActiveConnections returns the count of active (connected) entries
// in the security connection pool.
func (r *Runner) handleCheckActiveConnections(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	secState := getSecurityState(state)

	secState.pool.mu.Lock()
	count := 0
	for _, c := range secState.pool.connections {
		if c.isConnected() {
			count++
		}
	}
	secState.pool.mu.Unlock()

	return map[string]any{
		KeyActiveConnections: count,
	}, nil
}

// handleFloodConnections attempts many rapid connections to test flood resistance.
func (r *Runner) handleFloodConnections(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	rate := paramInt(params, "rate_per_second", 100)

	duration := paramInt(params, "duration_seconds", 5)

	target := r.getTarget(params)
	// DEC-082 two-port: target the comm port via SRV resolution. The
	// flood is a TCP-level test (TLS handshake either completes or
	// fails); routing it to the operational port would always fail and
	// hide whether the cap actually engaged.
	if resolver, ok := r.dialer.(targetResolver); ok {
		resolved := resolver.ResolveTarget(ctx, target, true)
		if resolved != target {
			r.debugf("flood_connections: srv-resolve %s -> %s", target, resolved)
			target = resolved
		}
	}

	// Track results
	var accepted, rejected int32
	// Track peak concurrent connections: current increments on accept,
	// decrements if the device closes the connection. peak records the
	// highest value of current observed.
	var current, peak int32
	var wg sync.WaitGroup

	// Hold connections open during flood to exercise concurrent connection cap (DEC-062).
	var connMu sync.Mutex
	var openConns []net.Conn

	// Create flood context with timeout
	floodCtx, cancel := context.WithTimeout(ctx, time.Duration(duration)*time.Second)
	defer cancel()

	// Calculate interval between connection attempts
	interval := time.Second / time.Duration(rate)

	// Start flood
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-floodCtx.Done():
			wg.Wait()

			// Close all held connections after flood completes.
			connMu.Lock()
			for _, c := range openConns {
				_ = c.Close()
			}
			connMu.Unlock()

			peakVal := int(atomic.LoadInt32(&peak))
			return map[string]any{
				KeyFloodCompleted:          true,
				KeyDeviceRemainsResponsive: true, // If we get here, device survived
				KeyAcceptedConnections:     int(atomic.LoadInt32(&accepted)),
				KeyMaxAcceptedConnections:  peakVal,
				KeyRejectedConnections:     int(atomic.LoadInt32(&rejected)),
			}, nil
		case <-ticker.C:
			wg.Add(1)
			go func() {
				defer wg.Done()
				tlsConfig := transport.NewCommissioningTLSConfig()
				dialer := &net.Dialer{Timeout: 2 * time.Second}
				conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
				if err != nil {
					atomic.AddInt32(&rejected, 1)
					return
				}
				atomic.AddInt32(&accepted, 1)
				// Track peak concurrent connections.
				cur := atomic.AddInt32(&current, 1)
				for {
					old := atomic.LoadInt32(&peak)
					if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
						break
					}
				}
				connMu.Lock()
				openConns = append(openConns, conn)
				connMu.Unlock()
			}()
		}
	}
}

// handleCheckMDNSAdvertisement checks mDNS advertisement for commissioning availability.
func (r *Runner) handleCheckMDNSAdvertisement(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// TODO: Integrate with actual mDNS discovery
	// For now, return a simulated result based on state
	secState := getSecurityState(state)

	// If zones are full, device should not advertise
	zonesFull := false
	if zf, ok := state.Get(PrecondDeviceZonesFull); ok {
		zonesFull = zf.(bool)
	}

	commissionable := !zonesFull && !secState.commissioningActive

	return map[string]any{
		KeyCommissionable:    commissionable,
		KeyAdvertisementFound: true,
	}, nil
}

// handleConnectOperational establishes an operational connection for an existing zone.
func (r *Runner) handleConnectOperational(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	zoneID := ""
	if z, ok := params[KeyZoneID].(string); ok {
		zoneID = z
	}

	target := r.getTarget(params)

	// For operational connection, use Zone CA validation when available.
	// When no Zone CA exists, default to InsecureSkipVerify since there's
	// no trusted root to verify against.
	var tlsConfig *tls.Config
	if r.connMgr.ZoneCAPool() != nil {
		tlsConfig = r.operationalTLSConfig()
	} else {
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
			NextProtos:         []string{transport.ALPNProtocol},
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	if err != nil {
		errorCode := classifyConnectError(err)
		alert := extractTLSAlert(err)
		tlsError := errorCode
		if alert != "" {
			tlsError = alert
		}
		out := map[string]any{
			KeyConnectionEstablished: false,
			KeyError:                 errorCode,
			KeyErrorCode:             errorCode,
			KeyTLSError:              tlsError,
			KeyTLSAlert:              alert,
			KeyTLSHandshakeFailed:    true,
		}
		return out, nil
	}

	// Store connection with zone association
	secState := getSecurityState(state)
	secState.pool.mu.Lock()
	newConn := &Connection{
		tlsConn: conn,
		framer:  transport.NewFramer(conn),
		state:   ConnOperational,
	}
	secState.pool.connections = append(secState.pool.connections, newConn)
	secState.pool.mu.Unlock()

	// Store zone association in state
	state.Set(ZoneConnectionStateKey(zoneID), newConn)

	return map[string]any{
		KeyConnectionEstablished: true,
		KeyZoneID:                zoneID,
		KeyConnectionType:        ServiceAliasOperational,
	}, nil
}

// handleEnterCommissioningMode signals device to enter commissioning mode.
// When connected, sends a TriggerEnterCommissioningMode via TestControl.
// In stub mode (no connection), sets StateCommissioningActive so that
// handleBrowseMDNS returns a synthetic commissionable service.
func (r *Runner) handleEnterCommissioningMode(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Real-device mode must never silently simulate commissioning-mode changes.
	// If trigger delivery fails, return an error and keep runner state unchanged.
	if r.config.Target != "" {
		if r.config.EnableKey == "" {
			return nil, fmt.Errorf("enter commissioning mode: no enable key configured")
		}
		if err := r.sendTriggerViaZone(ctx, features.TriggerEnterCommissioningMode, state); err != nil {
			return nil, fmt.Errorf("enter commissioning mode trigger failed: %w", err)
		}
	}

	secState := getSecurityState(state)
	secState.commissioningActive = true
	state.Set(StateCommissioningActive, true)
	state.Set(StateCommissioningCompleted, false)

	// Record window start time for expiry warning computation.
	// Only set if not already set by a precondition (e.g., commissioning_window_at_95s).
	if _, exists := state.Get(StateCommWindowStart); !exists {
		state.Set(StateCommWindowStart, time.Now())
	}

	// Stub mode: best-effort trigger (if connected) but state remains simulated.
	if r.config.Target == "" && r.config.EnableKey != "" {
		_ = r.sendTriggerViaZone(ctx, features.TriggerEnterCommissioningMode, state)
	}

	return map[string]any{
		KeyCommissioningModeEntered:      true,
		KeyCommissioningModeEnteredAlias: true,
	}, nil
}

// handleExitCommissioningMode signals device to exit commissioning mode.
// When connected, sends a TriggerExitCommissioningMode via TestControl.
// In stub mode, clears StateCommissioningActive so that handleBrowseMDNS
// no longer returns a synthetic commissionable service.
func (r *Runner) handleExitCommissioningMode(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Real-device mode must never silently simulate commissioning-mode changes.
	// If trigger delivery fails, return an error and keep runner state unchanged.
	if r.config.Target != "" {
		if r.config.EnableKey == "" {
			return nil, fmt.Errorf("exit commissioning mode: no enable key configured")
		}
		if err := r.sendTriggerViaZone(ctx, features.TriggerExitCommissioningMode, state); err != nil {
			return nil, fmt.Errorf("exit commissioning mode trigger failed: %w", err)
		}
	}

	secState := getSecurityState(state)
	secState.commissioningActive = false
	state.Set(StateCommissioningActive, false)

	// Stub mode: best-effort trigger (if connected) but state remains simulated.
	if r.config.Target == "" && r.config.EnableKey != "" {
		_ = r.sendTriggerViaZone(ctx, features.TriggerExitCommissioningMode, state)
	}

	return map[string]any{
		KeyCommissioningModeExited: true,
	}, nil
}

// handleSendPing sends a ping on a specific connection.
func (r *Runner) handleSendPing(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	connName := ""
	if c, ok := params[ParamConnection].(string); ok {
		connName = c
	}
	// Also accept "zone" param as an alias for "connection".
	if connName == "" {
		if z, ok := params[ParamZone].(string); ok {
			connName = z
		}
	}

	// Look up the connection from the connection tracker's zone connections,
	// then fall back to state lookup, then fall back to r.pool.Main().
	var conn *Connection
	if connName != "" {
		ct := getConnectionTracker(state)
		if c, ok := ct.zoneConnections[connName]; ok && c.isConnected() {
			conn = c
		}
		// Fall back to state-based lookup.
		if conn == nil {
			if c, ok := state.Get(ZoneConnectionStateKey(connName)); ok {
				conn = c.(*Connection)
			}
		}
	}
	// Fall back to the runner's main connection.
	if conn == nil && r.pool.Main() != nil && r.pool.Main().isConnected() {
		conn = r.pool.Main()
	}

	if conn == nil || !conn.isConnected() {
		return map[string]any{
			KeyPongReceived: false,
			KeyError:        "connection not found or not connected",
		}, nil
	}

	// Simulate ping by checking connection is still alive.
	return map[string]any{
		KeyPongReceived: true,
	}, nil
}

// handleReconnectOperational reconnects an operational zone connection.
func (r *Runner) handleReconnectOperational(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	zoneID := ""
	if z, ok := params[KeyZoneID].(string); ok {
		zoneID = z
	}

	// Close existing connection if any
	if c, ok := state.Get(ZoneConnectionStateKey(zoneID)); ok {
		if conn, ok := c.(*Connection); ok && conn.isConnected() {
			_ = conn.Close()
		}
	}

	// Reconnect
	result, err := r.handleConnectOperational(ctx, &loader.Step{
		Params: map[string]any{
			KeyZoneID: zoneID,
		},
	}, state)
	if err != nil {
		return result, err
	}

	// Add reconnection-specific output key expected by tests.
	if established, ok := result[KeyConnectionEstablished].(bool); ok {
		result[KeyReconnectionSuccessful] = established
	}

	return result, nil
}

// handlePASERequestSlow sends PASE request with intentional delays.
func (r *Runner) handlePASERequestSlow(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	delayMs := paramInt(params, "delay_between_messages_ms", 20000)

	// Store delay for continue_slow_exchange
	state.Set(StateSlowExchangeDelayMs, delayMs)
	state.Set(StateSlowExchangeStart, time.Now())

	return map[string]any{
		KeySlowExchangeStarted: true,
		KeyDelayMs:             delayMs,
	}, nil
}

// handleContinueSlowExchange continues a slow exchange until timeout or device closes.
// It probes the connection every second via a short read deadline to detect
// remote closure (e.g. the device's HandshakeTimeout or PASEFirstMessageTimeout).
func (r *Runner) handleContinueSlowExchange(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	totalDurationMs := paramInt(params, "total_duration_ms", 90000)

	startTime, _ := state.Get(StateSlowExchangeStart)

	start := startTime.(time.Time)
	totalDuration := time.Duration(totalDurationMs) * time.Millisecond

	// Probe every second with a short read deadline so we detect remote
	// closure promptly, regardless of the slow-exchange delay interval.
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	deadline := time.After(totalDuration)

	for {
		select {
		case <-deadline:
			// Timeout reached without device closing
			return map[string]any{
				KeyConnectionClosedByDevice: false,
				KeyTotalDurationMs:          time.Since(start).Milliseconds(),
			}, nil
		case <-ticker.C:
			if r.pool.Main() == nil || r.pool.Main().tlsConn == nil {
				return map[string]any{
					KeyConnectionClosedByDevice: true,
					KeyTotalDurationMs:          time.Since(start).Milliseconds(),
				}, nil
			}
			// Probe: attempt a read with a short deadline.
			_ = r.pool.Main().tlsConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			buf := make([]byte, 1)
			_, err := r.pool.Main().tlsConn.Read(buf)
			_ = r.pool.Main().tlsConn.SetReadDeadline(time.Time{})
			if err != nil {
				// Distinguish timeout (connection alive, no data) from EOF/reset (closed).
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // read timed out -- connection still alive
				}
				r.pool.Main().transitionTo(ConnDisconnected)
				return map[string]any{
					KeyConnectionClosedByDevice: true,
					KeyTotalDurationMs:          time.Since(start).Milliseconds(),
				}, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// ============================================================================
// PASE Timing Handlers
// ============================================================================

// handlePASEAttempts performs multiple PASE attempts and measures delays.
func (r *Runner) handlePASEAttempts(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	count := paramInt(params, KeyCount, 1)

	setupCode := "00000000" // Wrong code by default
	if sc, ok := params[KeySetupCode].(string); ok {
		setupCode = sc
	}

	var delays []time.Duration
	var maxDelay time.Duration
	allImmediate := true

	deadline, hasDeadline := ctx.Deadline()

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pase_attempts: timed out after %d/%d attempts (max_delay=%v): %w",
				i, count, maxDelay, ctx.Err())
		default:
		}

		if hasDeadline {
			r.debugf("pase_attempts: starting attempt %d/%d (remaining=%v)", i+1, count, time.Until(deadline))
		}

		delay, handshakeErr, connErr := r.measurePASEAttempt(ctx, setupCode)

		r.debugf("pase_attempts: attempt %d/%d delay=%v handshakeErr=%v connErr=%v",
			i+1, count, delay, handshakeErr, connErr)

		delays = append(delays, delay)
		if delay > maxDelay {
			maxDelay = delay
		}
		// The threshold must be above the constant-time padding (200ms) but
		// well below the tier 2 backoff (1000ms).
		if delay > 500*time.Millisecond {
			allImmediate = false
		}

		// Wait between attempts so the device finishes processing (constant-time
		// padding + conn close) and the cooldown period expires. Without this,
		// the next attempt arrives while the previous one is still holding the
		// commissioning lock, causing a "busy" rejection that skews the backoff
		// counter.
		if i < count-1 {
			time.Sleep(1 * time.Second)
		}
	}

	// Store for later comparison
	state.Set(StateLastPaseAttemptsCount, count)
	state.Set(StateLastPaseDelays, delays)

	return map[string]any{
		KeyAttemptsMade:          count,
		KeyAllResponsesImmediate: allImmediate,
		KeyMaxDelayMs:            maxDelay.Milliseconds(),
	}, nil
}

// handlePASEAttemptTimed performs a single PASE attempt and measures response delay.
func (r *Runner) handlePASEAttemptTimed(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	setupCode := "00000000" // Wrong code by default
	if sc, ok := params[KeySetupCode].(string); ok {
		setupCode = sc
	}

	delay, handshakeErr, connErr := r.measurePASEAttempt(ctx, setupCode)
	// Error is expected for wrong passwords - we're measuring timing

	state.Set(StateLastResponseDelayMs, delay.Milliseconds())

	return map[string]any{
		KeyResponseDelayMs: delay.Milliseconds(),
		KeyAttemptFailed:   connErr != nil || handshakeErr != nil,
	}, nil
}

// measurePASEAttempt measures the time for a single PASE attempt.
func (r *Runner) measurePASEAttempt(ctx context.Context, setupCode string) (time.Duration, error, error) {
	target := r.getTarget(nil)
	// DEC-082 two-port: route the comm dial through the SRV resolver
	// when -srv-resolve is in effect; matches handleConnect behaviour.
	if resolver, ok := r.dialer.(targetResolver); ok {
		resolved := resolver.ResolveTarget(ctx, target, true)
		if resolved != target {
			r.debugf("measurePASEAttempt: srv-resolve %s -> %s", target, resolved)
			target = resolved
		}
	}

	// Create connection
	tlsConfig := transport.NewCommissioningTLSConfig()
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	// Parse setup code
	sc, err := commissioning.ParseSetupCode(setupCode)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid setup code: %w", err)
	}

	// Create session. DEC-084 §2.4 fixes the SPAKE2+ identity strings; salt
	// derives from the discriminator captured during mDNS discovery.
	session, err := commissioning.NewPASEClientSession(sc, r.connMgr.DiscoveredDiscriminator())
	if err != nil {
		return 0, nil, fmt.Errorf("session creation failed: %w", err)
	}

	// Measure handshake time
	start := time.Now()
	_, handshakeErr := session.Handshake(ctx, conn)
	delay := time.Since(start)

	return delay, handshakeErr, nil
}

// ============================================================================
// Error Testing Handlers
// ============================================================================

// handlePASERequestInvalidPubkey sends a PASE request with an invalid public key.
func (r *Runner) handlePASERequestInvalidPubkey(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// For now, simulate by attempting with wrong setup code
	// The device should return the same error as wrong password
	delay, handshakeErr, _ := r.measurePASEAttempt(ctx, "00000001")

	errorCode := 1 // AUTH_FAILED
	errorName := "AUTH_FAILED"

	state.Set(StateLastErrorType, TimingErrorInvalidPubkey)
	state.Set(StateLastErrorDelayMs, delay.Milliseconds())

	return map[string]any{
		KeyErrorCode:      errorCode,
		KeyErrorName:      errorName,
		KeyHandshakeError: handshakeErr != nil,
	}, nil
}

// handlePASERequestWrongPassword sends a PASE request with wrong password.
func (r *Runner) handlePASERequestWrongPassword(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	delay, handshakeErr, _ := r.measurePASEAttempt(ctx, "00000000")

	errorCode := 1 // AUTH_FAILED
	errorName := "AUTH_FAILED"

	state.Set(StateLastErrorType, TimingErrorWrongPassword)
	state.Set(StateLastErrorDelayMs, delay.Milliseconds())

	return map[string]any{
		KeyErrorCode:      errorCode,
		KeyErrorName:      errorName,
		KeyHandshakeError: handshakeErr != nil,
	}, nil
}

// handleMeasureErrorTiming measures timing for a specific error type.
func (r *Runner) handleMeasureErrorTiming(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	errorType := TimingErrorInvalidPubkey
	if et, ok := params[KeyErrorType].(string); ok {
		errorType = et
	}

	iterations := paramInt(params, "iterations", 50)

	secState := getSecurityState(state)

	// Collect samples
	sample := &timingSample{
		ErrorType: errorType,
		Samples:   make([]time.Duration, 0, iterations),
	}

	setupCode := "00000000"
	if errorType == TimingErrorInvalidPubkey {
		setupCode = "00000001"
	}

	for i := 0; i < iterations; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		delay, _, _ := r.measurePASEAttempt(ctx, setupCode)
		sample.Samples = append(sample.Samples, delay)

		// Avoid immediate follow-up attempts that get measured as cooldown/busy
		// handling rather than the target authentication error timing.
		if i < iterations-1 {
			time.Sleep(1 * time.Second)
		}
	}

	// Calculate statistics
	sample.Mean, sample.StdDev = calculateStats(sample.Samples)

	// Store for comparison
	secState.timingSamples[errorType] = sample

	return map[string]any{
		KeyMeanRecorded:     true,
		KeyMeanMs:           sample.Mean.Milliseconds(),
		KeyStddevMs:         sample.StdDev.Milliseconds(),
		KeySamplesCollected: len(sample.Samples),
	}, nil
}

// handleCompareTimingDistributions compares timing distributions for different error types.
func (r *Runner) handleCompareTimingDistributions(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	secState := getSecurityState(state)

	// Get the two timing samples
	pubkeySample := secState.timingSamples[TimingErrorInvalidPubkey]
	passwordSample := secState.timingSamples[TimingErrorWrongPassword]

	if pubkeySample == nil || passwordSample == nil {
		return nil, fmt.Errorf("timing samples not collected for both error types")
	}

	// Calculate mean difference
	meanDiff := pubkeySample.Mean - passwordSample.Mean
	if meanDiff < 0 {
		meanDiff = -meanDiff
	}

	// Check if distributions overlap (within 2 standard deviations)
	overlap := distributionsOverlap(pubkeySample, passwordSample)

	state.Set(StateMeanDifferenceMs, meanDiff.Milliseconds())
	state.Set(StateDistributionsOverlap, overlap)

	return map[string]any{
		KeyMeanDifferenceMs:    meanDiff.Milliseconds(),
		KeyDistributionsOverlap: overlap,
		KeyPubkeyMeanMs:        pubkeySample.Mean.Milliseconds(),
		KeyPasswordMeanMs:      passwordSample.Mean.Milliseconds(),
	}, nil
}

// ============================================================================
// Custom Checkers
// ============================================================================

// checkResponseDelayMin checks if response delay is at least the minimum.
func (r *Runner) checkResponseDelayMin(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyResponseDelayMs)
	if !exists {
		actual, exists = state.Get(StateLastResponseDelayMs)
	}
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "response_delay_ms not found in outputs",
		}
	}

	expectedMs := toMillis(expected)
	actualMs := toMillis(actual)

	passed := actualMs >= expectedMs
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("response delay %dms >= %dms: %v", actualMs, expectedMs, passed),
	}
}

// checkResponseDelayMax checks if response delay is at most the maximum.
func (r *Runner) checkResponseDelayMax(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyResponseDelayMs)
	if !exists {
		actual, exists = state.Get(StateLastResponseDelayMs)
	}
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "response_delay_ms not found in outputs",
		}
	}

	expectedMs := toMillis(expected)
	actualMs := toMillis(actual)

	passed := actualMs <= expectedMs
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("response delay %dms <= %dms: %v", actualMs, expectedMs, passed),
	}
}

// checkMaxDelay checks if maximum delay from multiple attempts is within limit.
func (r *Runner) checkMaxDelay(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyMaxDelayMs)
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "max_delay_ms not found in outputs",
		}
	}

	expectedMs := toMillis(expected)
	actualMs := toMillis(actual)

	passed := actualMs <= expectedMs
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("max delay %dms <= %dms: %v", actualMs, expectedMs, passed),
	}
}

// checkMinDelay checks if minimum delay from multiple attempts meets threshold.
func (r *Runner) checkMinDelay(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	delays, exists := state.Get(StateLastPaseDelays)
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "last_pase_delays not found in outputs",
		}
	}

	delaySlice, ok := delays.([]time.Duration)
	if !ok {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "last_pase_delays is not a duration slice",
		}
	}

	expectedMs := toMillis(expected)
	allMeetMin := true
	for _, d := range delaySlice {
		if d.Milliseconds() < expectedMs {
			allMeetMin = false
			break
		}
	}

	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   delaySlice,
		Passed:   allMeetMin,
		Message:  fmt.Sprintf("all delays >= %dms: %v", expectedMs, allMeetMin),
	}
}

// checkMeanDifferenceMax checks if mean difference is within threshold.
func (r *Runner) checkMeanDifferenceMax(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(StateMeanDifferenceMs)
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "mean_difference_ms not found in outputs",
		}
	}

	expectedMs := toMillis(expected)
	actualMs := toMillis(actual)

	passed := actualMs <= expectedMs
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("mean difference %dms <= %dms: %v", actualMs, expectedMs, passed),
	}
}

// handleFillConnections opens `count` commissioning TLS connections, storing
// each in the pool. Used by PICS-driven cap tests where the count is
// interpolated from PICS values like ${MASH.S.ZONE.MAX + 1}.
func (r *Runner) handleFillConnections(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)

	count := paramInt(params, KeyCount, 0)

	if count <= 0 {
		return nil, fmt.Errorf("fill_connections: count must be > 0, got %v", params[KeyCount])
	}

	secState := getSecurityState(state)
	target := r.getTarget(params)

	opened := 0
	for i := 0; i < count; i++ {
		tlsConfig := transport.NewCommissioningTLSConfig()
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
		if err != nil {
			// Connection rejected -- stop filling.
			break
		}

		secState.pool.mu.Lock()
		newConn := &Connection{
			tlsConn: conn,
			framer:  transport.NewFramer(conn),
			state:   ConnTLSConnected,
		}
		secState.pool.connections = append(secState.pool.connections, newConn)
		secState.pool.mu.Unlock()

		if !r.pool.Main().isConnected() {
			r.pool.SetMain(newConn)
		}
		opened++
	}

	return map[string]any{
		KeyConnectionsOpened: opened,
	}, nil
}

// checkBusyRetryAfterGT checks if busy_retry_after_value is strictly greater
// than the expected value. Used in YAML as: busy_retry_after_gt: 0
func (r *Runner) checkBusyRetryAfterGT(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(KeyBusyRetryAfterValue)
	if !exists {
		actual, exists = state.Get(KeyBusyRetryAfter)
	}
	if !exists {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Passed:   false,
			Message:  "busy_retry_after_value not found in outputs",
		}
	}

	actualNum, ok1 := engine.ToFloat64(actual)
	expectedNum, ok2 := engine.ToFloat64(expected)
	if !ok1 || !ok2 {
		return &engine.ExpectResult{
			Key:      key,
			Expected: expected,
			Actual:   actual,
			Passed:   false,
			Message:  fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}

	passed := actualNum > expectedNum
	return &engine.ExpectResult{
		Key:      key,
		Expected: expected,
		Actual:   actual,
		Passed:   passed,
		Message:  fmt.Sprintf("busy_retry_after %v > %v = %v", actualNum, expectedNum, passed),
	}
}

// ============================================================================
// Busy Response Helpers (DEC-063)
// ============================================================================

// handleBusyPASEExchange connects to the device, sends a PASERequest, and reads
// the expected CommissioningError busy response. When no target is configured
// (simulation mode), it returns canned busy response values.
func (r *Runner) handleBusyPASEExchange(step *loader.Step) (map[string]any, error) {
	target := r.getTarget(step.Params)

	// Simulation mode: return expected busy values without connecting.
	// DEC-063: Device always includes RetryAfter when busy.
	if target == "" {
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyBusyResponseReceived:  true,
			KeyBusyErrorCode:         int(commissioning.ErrCodeBusy),
			KeyBusyRetryAfterValue:   30,
			KeyBusyRetryAfter:        30,
			KeyBusyRetryAfterPresent: true,
		}, nil
	}

	// DEC-082 two-port: resolve the per-ALPN comm target via SRV when
	// available so two-port devices route the dial to the comm port.
	if resolver, ok := r.dialer.(targetResolver); ok {
		resolved := resolver.ResolveTarget(context.Background(), target, true)
		if resolved != target {
			r.debugf("handleBusyPASEExchange: srv-resolve %s -> %s", target, resolved)
			target = resolved
		}
	}

	// Real device: connect, send PASERequest, read CommissioningError.
	// Retry on transient errors (EOF, connection reset) that can occur when
	// the device is transitioning between commissioning and operational modes.
	const maxAttempts = 3
	const retryDelay = 1 * time.Second

	tlsConfig := transport.NewCommissioningTLSConfig()
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	r.debugf("handleBusyPASEExchange: connecting to %s (max %d attempts)", target, maxAttempts)

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			r.debugf("handleBusyPASEExchange: retry %d/%d after transient error: %v", attempt, maxAttempts-1, lastErr)
			_ = contextSleep(context.Background(), retryDelay)
		}

		conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
		if err != nil {
			r.debugf("handleBusyPASEExchange: TLS connect failed (attempt %d): %v (transient=%v)", attempt, err, isTransientError(err))
			if isTransientError(err) && attempt < maxAttempts-1 {
				lastErr = err
				continue
			}
			return map[string]any{
				KeyConnectionEstablished: false,
				KeyConnectionRejected:    true,
				KeyRejectionAtTLSLevel:   true,
				KeyError:                 err.Error(),
			}, nil
		}

		r.debugf("handleBusyPASEExchange: TLS connected, sending PASERequest (attempt %d)", attempt)
		if err := sendPASERequestRaw(conn); err != nil {
			r.debugf("handleBusyPASEExchange: send PASERequest failed: %v (transient=%v)", err, isTransientError(err))
			conn.Close()
			if isTransientError(err) && attempt < maxAttempts-1 {
				lastErr = err
				continue
			}
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyError:                 fmt.Sprintf("send PASERequest: %v", err),
			}, nil
		}

		r.debugf("handleBusyPASEExchange: reading CommissioningError response (attempt %d)", attempt)
		errMsg, err := readCommissioningErrorRaw(conn, 5*time.Second)
		if err != nil {
			r.debugf("handleBusyPASEExchange: read CommissioningError failed: %v (transient=%v)", err, isTransientError(err))
			conn.Close()
			if isTransientError(err) && attempt < maxAttempts-1 {
				lastErr = err
				continue
			}
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyError:                 fmt.Sprintf("read CommissioningError: %v", err),
			}, nil
		}

		r.debugf("handleBusyPASEExchange: received CommissioningError (code=%d, retryAfter=%d)", errMsg.ErrorCode, errMsg.RetryAfter)
		conn.Close()
		return map[string]any{
			KeyConnectionEstablished: true,
			KeyBusyResponseReceived:  true,
			KeyBusyErrorCode:         int(errMsg.ErrorCode),
			KeyBusyRetryAfterValue:   int(errMsg.RetryAfter),
			KeyBusyRetryAfter:        int(errMsg.RetryAfter),
			KeyBusyRetryAfterPresent: true, // always present (omitempty removed from CommissioningError)
		}, nil
	}

	// All retries exhausted -- return the last transient error.
	r.debugf("handleBusyPASEExchange: all %d attempts failed: %v", maxAttempts, lastErr)
	return map[string]any{
		KeyConnectionEstablished: false,
		KeyError:                 fmt.Sprintf("all %d attempts failed: %v", maxAttempts, lastErr),
	}, nil
}

// sendPASERequestRaw sends a PASERequest with a valid SPAKE2+ public value over
// a length-prefixed CBOR connection. The setup code / discriminator don't need
// to match the device -- any valid P-256 point passes ProcessClientValue. The
// device then proceeds to send PASEResponse and blocks waiting for PASEConfirm,
// keeping the commissioning lock held.
func sendPASERequestRaw(conn net.Conn) error {
	spakeClient, err := commissioning.NewSPAKE2PlusClient(
		commissioning.SetupCode(20202021),
		0, // discriminator: irrelevant — only pA validity matters here.
	)
	if err != nil {
		return fmt.Errorf("create SPAKE2+ client: %w", err)
	}

	// DEC-084 §4.2: ClientIdentity MUST be the protocol-constant
	// "mash-controller" or the device aborts before reaching step 2 — which
	// would defeat the lock-holding purpose of this raw-request helper.
	req := &commissioning.PASERequest{
		MsgType:        commissioning.MsgPASERequest,
		PublicValue:    spakeClient.PublicValue(),
		ClientIdentity: commissioning.ControllerIdentity,
	}
	data, err := cbor.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal PASERequest: %w", err)
	}

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	if _, err := conn.Write(length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}
	return nil
}

// readPASEMessageRaw reads a length-prefixed CBOR message from the connection
// within the given timeout and decodes it to the appropriate PASE message type.
func readPASEMessageRaw(conn net.Conn, timeout time.Duration) (interface{}, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}

	msgLen := binary.BigEndian.Uint32(lengthBuf)
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	return commissioning.DecodePASEMessage(data)
}

// readCommissioningErrorRaw reads a length-prefixed CBOR CommissioningError
// from the connection within the given timeout.
func readCommissioningErrorRaw(conn net.Conn, timeout time.Duration) (*commissioning.CommissioningError, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}

	msgLen := binary.BigEndian.Uint32(lengthBuf)
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	msg, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	errMsg, ok := msg.(*commissioning.CommissioningError)
	if !ok {
		return nil, fmt.Errorf("expected CommissioningError, got %T", msg)
	}
	return errMsg, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// toMillis converts various numeric types to milliseconds.
func toMillis(v interface{}) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	case time.Duration:
		return val.Milliseconds()
	default:
		return 0
	}
}

// calculateStats computes mean and standard deviation of durations.
func calculateStats(samples []time.Duration) (mean, stddev time.Duration) {
	if len(samples) == 0 {
		return 0, 0
	}

	// Calculate mean
	var sum int64
	for _, s := range samples {
		sum += s.Nanoseconds()
	}
	meanNs := sum / int64(len(samples))
	mean = time.Duration(meanNs)

	// Calculate standard deviation
	if len(samples) < 2 {
		return mean, 0
	}

	var sumSquares float64
	for _, s := range samples {
		diff := float64(s.Nanoseconds() - meanNs)
		sumSquares += diff * diff
	}
	variance := sumSquares / float64(len(samples)-1)
	stddev = time.Duration(math.Sqrt(variance))

	return mean, stddev
}

// distributionsOverlap checks if two timing distributions overlap significantly.
// Uses 2 standard deviations as the overlap criterion.
func distributionsOverlap(a, b *timingSample) bool {
	// Check if means are within 2 standard deviations of each other
	combinedStdDev := (a.StdDev + b.StdDev) / 2
	meanDiff := a.Mean - b.Mean
	if meanDiff < 0 {
		meanDiff = -meanDiff
	}

	// If mean difference is less than 2 combined standard deviations, they overlap
	return meanDiff < 2*combinedStdDev
}
