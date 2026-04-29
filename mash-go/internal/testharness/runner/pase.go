package runner

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// PASEState holds the state for PASE commissioning sessions.
type PASEState struct {
	// session is the active PASE client session (if in-progress).
	session *commissioning.PASEClientSession

	// sessionKey is the derived session key after successful handshake.
	sessionKey []byte

	// completed indicates whether commissioning finished successfully.
	completed bool
}

// Completed returns whether the PASE handshake completed successfully.
// Nil-safe: returns false for a nil PASEState.
func (ps *PASEState) Completed() bool {
	if ps == nil {
		return false
	}
	return ps.completed
}

// SessionKey returns the derived session key, or nil if not yet available.
// Nil-safe: returns nil for a nil PASEState.
func (ps *PASEState) SessionKey() []byte {
	if ps == nil {
		return nil
	}
	return ps.sessionKey
}

// swapMainToOperational closes pool.Main()'s existing connection and replaces
// it with a fresh Connection struct wrapping opConn. It returns the new
// Connection (also reachable via r.pool.Main()).
//
// Why a fresh struct, not in-place mutation:
// suite.Conn() may alias r.pool.Main() (see borrowSuiteControlChannelIfAlive
// and reconnectToZone, which both set the pool's main and the suite's conn
// to the same Connection pointer). Mutating the existing pool.Main() struct
// in place would carry the test's new operational TLS conn, framer, and
// state into the suite alias too. The teardown's `zc == c.suite.Conn()`
// alias filter would then misclassify the test's new zone connection as
// the suite zone's, untrack it without sending RemoveZone, and leak a
// LOCAL zone on the device -- breaking the next test's commissioning with
// "zone type already exists, code 10" (F-6 follow-up: TC-TRANS-002 fails
// after TC-TRANS-001 leaves a LOCAL zone behind).
//
// commissioning.go's transitionToOperational already swaps in a fresh
// struct; this helper aligns handleCommission's transition path with that
// invariant.
func (r *Runner) swapMainToOperational(opConn *tls.Conn) *Connection {
	_ = r.pool.Main().Close()
	newConn := &Connection{
		tlsConn:       opConn,
		framer:        transport.NewFramer(opConn),
		state:         ConnOperational,
		hadConnection: true,
	}
	r.pool.SetMain(newConn)
	return newConn
}

// registerPASEHandlers registers all PASE-related action handlers.
func (r *Runner) registerPASEHandlers() {
	// Primary commissioning action (recommended)
	r.engine.RegisterHandler(ActionCommission, r.handleCommission)

	// Legacy granular PASE handlers - these now delegate to commission
	// for backward compatibility with existing test cases
	r.engine.RegisterHandler(ActionPASERequest, r.handlePASERequest)
	r.engine.RegisterHandler(ActionPASEReceiveResponse, r.handlePASEReceiveResponse)
	r.engine.RegisterHandler(ActionPASEConfirm, r.handlePASEConfirm)
	r.engine.RegisterHandler(ActionPASEReceiveVerify, r.handlePASEReceiveVerify)
	r.engine.RegisterHandler(ActionVerifySessionKey, r.handleVerifySessionKey)
}

// handleCommission performs the full PASE commissioning handshake.
// This is the recommended action for test cases - it performs the complete
// SPAKE2+ exchange and returns the session key.
//
// If not already connected, this action will establish a commissioning TLS
// connection first. This ensures the connection and PASE handshake happen
// atomically without any delay between them.
func (r *Runner) handleCommission(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Interpolate parameters so ${setup_code} etc. resolve.
	params := engine.InterpolateParams(step.Params, state)

	// Resolve commission zone type using explicit policy:
	// explicit request wins; otherwise, when suite TEST exists, use LOCAL for
	// test-step commissioning so suite control zone remains isolated.
	requestedZoneType := r.applyCommissionZoneType(params)

	// Check for duplicate zone type before attempting PASE.
	// The device will reject this, but we can return structured output.
	if requestedZoneType != "" {
		zs := getZoneState(state)
		for _, z := range zs.zones {
			if z.ZoneType == requestedZoneType {
				return map[string]any{
					KeySessionEstablished:    false,
					KeyCommissionSuccess:     false,
					KeySuccess:               false,
					KeyErrorCode:             10, // ZONE_TYPE_EXISTS
					KeyError:                 "zone type already exists",
					KeyErrorMessageContains: "zone type already exists",
				}, nil
			}
		}
	}

	// Get setup code from params or config
	setupCode, err := r.getSetupCode(params)
	if err != nil {
		return nil, fmt.Errorf("invalid setup code: %w", err)
	}

	// DEC-068: Reject prohibited low-entropy setup codes before attempting
	// the PASE handshake. SPAKE2+ cannot distinguish "wrong code" from
	// "prohibited code" (both fail at confirmation), so we validate early.
	// Only check the prohibited list -- out-of-range codes (like 0) are
	// legitimate "wrong code" test inputs that should reach PASE.
	if slices.Contains(commissioning.InvalidSetupCodes, setupCode) {
		return map[string]any{
			KeySessionEstablished: false,
			KeyCommissionSuccess:  false,
			KeySuccess:            false,
			KeyError:              "invalid_setup_code",
		}, nil
	}

	// DEC-084 §2.4: SPAKE2+ identity strings are protocol constants. The salt
	// derives from the discriminator instead — captured during mDNS browse and
	// stored on the connection manager.
	discriminator := r.connMgr.DiscoveredDiscriminator()

	// If the connection is operational (either from a completed commission or
	// from suite zone reuse with cleared PASE state), the device won't accept
	// PASE on it -- mash/1 ALPN routes to the operational handler, not the
	// commissioning handler. Disconnect so we create a fresh mash-comm/1
	// commissioning connection below.
	if r.pool.Main().isConnected() && r.pool.Main().state == ConnOperational {
		r.debugf("handleCommission: closing stale operational conn (pase completed, conn live)")
		// Send ControlClose so the device's message loop exits immediately.
		if r.pool.Main().framer != nil {
			closeMsg := &wire.ControlMessage{Type: wire.ControlClose}
			if closeData, encErr := wire.EncodeControlMessage(closeMsg); encErr == nil {
				_ = r.pool.Main().framer.WriteFrame(closeData)
			}
		}
		r.pool.Main().transitionTo(ConnDisconnected)
		// Wait for device to re-enter commissioning mode (mDNS advertisement).
		if err := r.waitForCommissioningMode(ctx, 3*time.Second); err != nil {
			r.debugf("handleCommission: %v (continuing)", err)
		}
	}

	// DEC-066: After a successful commission, the device closes the
	// commissioning socket after cert exchange. If a previous commission step
	// in this test completed, the main conn still references the dead socket at
	// ConnTLSConnected. Clean it up so we dial fresh below.
	if r.pool.Main().state == ConnTLSConnected && r.connMgr.PASEState() != nil && r.connMgr.PASEState().completed {
		r.debugf("handleCommission: cleaning up dead post-commission socket")
		r.pool.Main().transitionTo(ConnDisconnected)
		r.pool.Main().clearConnectionRefs()
		r.connMgr.SetPASEState(nil)
	}

	r.debugf("handleCommission: conn.state=%v tlsConn=%v paseState=%v",
		r.pool.Main().state, r.pool.Main().tlsConn != nil, r.connMgr.PASEState() != nil)

	// Establish commissioning connection if not already connected
	// This ensures connection + PASE happen atomically
	var conn net.Conn
	if r.pool.Main().isConnected() && r.pool.Main().tlsConn != nil {
		r.debugf("handleCommission: reusing existing TLS connection")
		conn = r.pool.Main().tlsConn
	} else {
		// Create new commissioning connection
		target := r.getTarget(params)

		// Multi-step commissioning flows can call commission again immediately
		// after a successful cert exchange. Wait until the device is advertising
		// commissionable mode before dialing mash-comm/1 to avoid transient TLS
		// "internal error" races while the window is reopening.
		if err := r.waitForCommissioningAvailable(ctx, 3*time.Second); err != nil {
			r.debugf("handleCommission: %v (continuing)", err)
		}

		r.debugf("handleCommission: dialing new commissioning connection to %s", target)
		tlsConn, err := r.dialer.DialCommissioning(ctx, target)
		if err != nil {
			r.debugf("handleCommission: dial failed: %v", err)
			return nil, fmt.Errorf("failed to connect for commissioning: %w", err)
		}
		r.debugf("handleCommission: connected to %s", tlsConn.RemoteAddr())

		// Store connection for later use
		r.pool.Main().tlsConn = tlsConn
		r.pool.Main().framer = transport.NewFramer(tlsConn)
		r.pool.Main().state = ConnTLSConnected
		r.pool.Main().hadConnection = true
		conn = tlsConn

		state.Set(StateConnection, r.pool.Main())
		state.Set(KeyConnectionEstablished, true)
	}

	// Create PASE client session
	if err := r.alignLifecycleForCommissioning("main"); err != nil {
		return nil, err
	}

	// Create PASE client session
	session, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		return nil, fmt.Errorf("failed to create PASE session: %w", err)
	}

	// Perform the full SPAKE2+ handshake
	r.debugf("handleCommission: starting PASE handshake")
	sessionKey, err := session.Handshake(ctx, conn)
	if err != nil {
		r.debugf("handleCommission: PASE handshake failed: %v", err)
		// DEC-065: If the device rejected with a cooldown error, wait for it
		// to expire and retry with a fresh connection. This handles tests
		// where a prior step (e.g., pase_attempts) triggered a cooldown.
		if wait := cooldownRemaining(err); wait > 0 {
			r.debugf("handleCommission: cooldown active, waiting %s and retrying", wait.Round(time.Millisecond))
			r.pool.Main().transitionTo(ConnDisconnected)
			if err := contextSleep(ctx, wait); err != nil {
				return nil, fmt.Errorf("cooldown wait cancelled: %w", err)
			}

			// Reconnect and retry PASE.
			target := r.getTarget(params)
			tlsConn, retryErr := r.dialer.DialCommissioning(ctx, target)
			if retryErr != nil {
				return nil, fmt.Errorf("cooldown retry connect failed: %w", retryErr)
			}
			r.pool.Main().tlsConn = tlsConn
			r.pool.Main().framer = transport.NewFramer(tlsConn)
			r.pool.Main().state = ConnTLSConnected
			r.pool.Main().hadConnection = true
			conn = tlsConn

			retrySession, retryErr := commissioning.NewPASEClientSession(setupCode, discriminator)
			if retryErr != nil {
				return nil, fmt.Errorf("cooldown retry PASE session failed: %w", retryErr)
			}
			session = retrySession
			sessionKey, err = session.Handshake(ctx, conn)
		}
	}
	if err != nil {
		// "Commissioning already in progress" retry -- the device has a
		// stale PASE session from a prior test that hasn't expired yet.
		// This is error code 5 without cooldown timing. Wait briefly for
		// the stale session to clear, then retry once.
		if code, hasCode := extractPASEErrorCode(err.Error()); hasCode &&
			code == commissioning.ErrCodeBusy && cooldownRemaining(err) == 0 {
			r.debugf("handleCommission: device busy (stale session), waiting 500ms and retrying")
			r.pool.Main().transitionTo(ConnDisconnected)
			if err := contextSleep(ctx, 500*time.Millisecond); err != nil {
				return nil, fmt.Errorf("stale session wait cancelled: %w", err)
			}

			target := r.getTarget(params)
			tlsConn, retryErr := r.dialer.DialCommissioning(ctx, target)
			if retryErr == nil {
				r.pool.Main().tlsConn = tlsConn
				r.pool.Main().framer = transport.NewFramer(tlsConn)
				r.pool.Main().state = ConnTLSConnected
				r.pool.Main().hadConnection = true
				conn = tlsConn

				retrySession, retryErr := commissioning.NewPASEClientSession(setupCode, discriminator)
				if retryErr == nil {
					session = retrySession
					sessionKey, err = session.Handshake(ctx, conn)
				}
			}
		}
	}
	if err != nil {
		// Store failed state for debugging
		r.connMgr.SetPASEState(&PASEState{
			session:   session,
			completed: false,
		})
		// The handshake writes/reads on the connection. If it fails,
		// the connection is in an unusable state (the device closes
		// it on PASE failure per the MASH spec).
		r.pool.Main().transitionTo(ConnDisconnected)

		// Distinguish PASE protocol errors (device sent an error code)
		// from connection/infrastructure errors (timeout, EOF, etc.).
		// Protocol errors return nil Go error so the engine can check
		// expectations against the structured output. Connection errors
		// return a Go error so ensureCommissioned can retry.
		errStr := err.Error()
		errorName := errStr
		isPASEProtocolError := false
		if code, ok := extractPASEErrorCode(errStr); ok {
			isPASEProtocolError = true
			errorName = paseErrorCodeName(code)
		}

		outputs := map[string]any{
			KeySessionEstablished: false,
			KeyCommissionSuccess:  false,
			KeySuccess:            false,
			KeyError:              errorName,
			PrecondDeviceInZone:   false,
		}

		if isPASEProtocolError {
			// Protocol-level failure: return nil error so engine checks expectations.
			return outputs, nil
		}
		// Infrastructure failure: return Go error for retry logic.
		return outputs, fmt.Errorf("PASE handshake failed: %w", err)
	}

	// Store successful state
	r.connMgr.SetPASEState(&PASEState{
		session:    session,
		sessionKey: sessionKey,
		completed:  true,
	})

	// Perform certificate exchange after PASE (MsgType 30-33).
	// This is required: the device blocks waiting for the cert exchange
	// before it will accept operational messages (Read/Write/Invoke).
	r.debugf("handleCommission: starting cert exchange (framer=%p, conn=%p)", r.pool.Main().framer, r.pool.Main().tlsConn)
	deviceID, certErr := r.performCertExchange(ctx)
	if certErr != nil {
		r.debugf("handleCommission: cert exchange FAILED: %v", certErr)
		r.pool.Main().transitionTo(ConnDisconnected)
		return map[string]any{
			KeySessionEstablished: false,
			KeyCommissionSuccess:  false,
			KeySuccess:            false,
			KeyError:              certErr.Error(),
			PrecondDeviceInZone:   false,
		}, fmt.Errorf("cert exchange failed: %w", certErr)
	}
	r.debugf("handleCommission: cert exchange succeeded, deviceID=%s, zoneCAPool=%p", deviceID, r.connMgr.ZoneCAPool())

	// DEC-066: the device closes the commissioning connection after cert
	// exchange, so we must reconnect operationally for cleanup to work.
	//
	// Two modes:
	// 1. Explicit transition (doTransition=true): replaces the main connection
	//    with an operational connection. Used by test steps that need read/write
	//    after commissioning.
	// 2. Implicit tracking (real device, no explicit transition): creates
	//    a SEPARATE operational connection for zone cleanup, leaving the main
	//    connection unchanged. This avoids the shared-Connection-object bug where
	//    closeActiveZoneConns would corrupt the main connection.
	doTransition, _ := params[ParamTransitionToOperational].(bool)
	_, fromPrecondition := params[ParamFromPrecondition]
	if certErr == nil && (doTransition || (!fromPrecondition && r.config.Target != "")) {
		target := r.getTarget(params)

		// Retry the dial briefly in case the device hasn't registered the
		// zone as awaiting reconnection yet.
		tlsConfig := r.operationalTLSConfig()
		opConn, opErr := dialWithRetry(ctx, 3, func() (*tls.Conn, error) {
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
		})

		if opErr == nil && doTransition {
			// Explicit transition: swap pool.Main() to a fresh operational
			// Connection. swapMainToOperational is load-bearing -- see its
			// docstring for why the fresh struct (vs. in-place mutation)
			// matters for suite-zone alias decoupling (F-6 follow-up).
			newConn := r.swapMainToOperational(opConn)
			state.Set(StateConnection, newConn)
			state.Set(StateOperationalConnEstablished, time.Now())
			if err := r.waitForOperationalReady(2 * time.Second); err != nil {
				r.debugf("handleCommission: %v (continuing)", err)
			}
			if r.connMgr.PASEState() != nil && r.connMgr.PASEState().sessionKey != nil {
				zoneID := deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)
				connKey := "step-" + zoneID
				r.pool.TrackZone(connKey, newConn, zoneID)
				r.debugf("handleCommission: transitioned to operational, zone %s", zoneID)
			}
		} else if opErr == nil {
			// Implicit tracking: create a separate Connection for cleanup.
			// The main connection remains the (dead) commissioning connection so test
			// steps aren't disrupted by an unexpected operational connection.
			if r.connMgr.PASEState() != nil && r.connMgr.PASEState().sessionKey != nil {
				zoneID := deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)
				connKey := "step-" + zoneID
				trackConn := &Connection{
					tlsConn: opConn,
					framer:  transport.NewFramer(opConn),
					state:   ConnOperational,
				}
				r.pool.TrackZone(connKey, trackConn, zoneID)
				r.debugf("handleCommission: tracking connection for zone %s", zoneID)
			} else {
				opConn.Close()
			}
		}
		// If operational reconnect fails, proceed anyway -- the commission
		// itself succeeded and tests can check connection state separately.
	}

	// Clear commissioning state now that commissioning succeeded.
	// The device is no longer in commissioning mode, so mDNS browse
	// should no longer return synthetic commissionable services.
	state.Set(StateCommissioningActive, false)
	state.Set(StateCommissioningCompleted, true)
	secState := getSecurityState(state)
	secState.commissioningActive = false

	// Clear pairing request state now that commissioning succeeded.
	// If this was a deferred commissioning flow (TC-PAIR-004), the
	// discriminator and powered_on flags drove the mDNS simulation;
	// leaving them set would make subsequent browse calls still report
	// the device as advertising.
	state.Set(StatePairingRequestDiscriminator, nil)

	// Also store in execution state for test assertions
	state.Set(KeySessionEstablished, true)
	state.Set(StateSessionKey, sessionKey)
	state.Set(StateSessionKeyLen, len(sessionKey))
	if deviceID != "" {
		state.Set(StateDeviceID, deviceID)
	}

	// Register the commissioned zone in the local zone state so that
	// list_zones, remove_zone, etc. reflect the commissioning result.
	// Use the real derived zone ID when available for consistency with
	// preconditions code and to match what the device stores.
	derivedZoneID := ""
	if r.connMgr.PASEState() != nil && r.connMgr.PASEState().sessionKey != nil {
		derivedZoneID = deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)
		// Set current_zone_id so tests can reference {{ current_zone_id }}.
		state.Set(StateCurrentZoneID, derivedZoneID)
	}
	if zt, ok := params[KeyZoneType].(string); ok && zt != "" {
		zs := getZoneState(state)
		zoneID, _ := params[KeyZoneID].(string)
		if zoneID == "" && derivedZoneID != "" {
			zoneID = derivedZoneID
		}
		if zoneID == "" {
			zoneID = strings.ToUpper(zt) // fallback label
		}
		if _, exists := zs.zones[zoneID]; !exists {
			zone := &zoneInfo{
				ZoneID:    zoneID,
				ZoneType:  strings.ToUpper(zt),
				Priority:  zonePriority[strings.ToUpper(zt)],
				Connected: false,
				Metadata:  make(map[string]any),
			}
			zs.zones[zoneID] = zone
			zs.zoneOrder = append(zs.zoneOrder, zoneID)
		}

		// Update multi-zone state so browse_mdns returns the correct
		// number of operational instances (TC-DSTATE-005, TC-E2E-003).
		if len(zs.zones) >= 2 {
			state.Set(PrecondDeviceInTwoZones, true)
		}

		// Store the zone ID in typed state keys for interpolation
		// (e.g., {{ grid_zone_id }}) -- mirrors preconditions.go logic.
		ztUpper := strings.ToUpper(zt)
		var stateKey string
		switch {
		case ztUpper == ZoneTypeGrid:
			stateKey = StateGridZoneID
		case ztUpper == ZoneTypeLocal:
			stateKey = StateLocalZoneID
		case ztUpper == ZoneTypeTest:
			stateKey = StateTestZoneID
		}
		if stateKey != "" {
			state.Set(stateKey, zoneID)
		}
	}

	outputs := map[string]any{
		KeySessionEstablished: true,
		KeyCommissionSuccess:  true,
		KeySuccess:            true,
		KeyKeyLength:          len(sessionKey),
		KeyKeyNotZero:         !isZeroKey(sessionKey),
		KeyState:              ConnectionStatePASEVerified,
		PrecondDeviceInZone:   true,
	}
	if deviceID != "" {
		outputs[KeyDeviceID] = deviceID
	}
	if derivedZoneID != "" {
		outputs[KeyZoneID] = derivedZoneID
	}

	// When resolving a discriminator collision, a successful PASE handshake
	// confirms the correct device was commissioned (setup code matched).
	if twoDevs, _ := state.Get(PrecondTwoDevicesSameDiscriminator); twoDevs == true {
		outputs[KeyCorrectDeviceCommissioned] = true
	}

	return outputs, nil
}

// performCertExchange executes the post-PASE commissioning cert install
// using the spec-strict envelope (MsgTypes 10/11/12/13/20 per DEC-085).
// It generates a Zone CA, drives PerformCommissioningCertInstall, and stores
// the resulting certs on the runner for later TLS use.
func (r *Runner) performCertExchange(ctx context.Context) (string, error) {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().sessionKey == nil {
		return "", fmt.Errorf("no PASE session key")
	}
	if r.pool.Main() == nil || !r.pool.Main().isConnected() || r.pool.Main().framer == nil {
		return "", fmt.Errorf("no active connection")
	}

	// Derive zone ID from PASE shared secret (same derivation as device).
	zoneID := deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)

	// Use the configured zone type, defaulting to LOCAL.
	zt := r.connMgr.CommissionZoneType()
	if zt == 0 {
		zt = cert.ZoneTypeLocal
	}

	// Generate Zone CA for this zone.
	zoneCA, err := cert.GenerateZoneCA(zoneID, zt)
	if err != nil {
		return "", fmt.Errorf("generate zone CA: %w", err)
	}

	// Create a SyncConnection adapter wrapping the framer.
	syncConn := &framerSyncConn{framer: r.pool.Main().framer}

	// Drive the spec-strict commissioning cert install (DEC-085).
	r.debugf("performCertExchange: issuing cert for zone %s (type=%d)", zoneID, zt)
	deviceCert, err := service.PerformCommissioningCertInstall(ctx, zoneCA, syncConn, zt, 1)
	if err != nil {
		r.debugf("performCertExchange: PerformCommissioningCertInstall failed: %v", err)
		return "", fmt.Errorf("cert exchange: %w", err)
	}

	// Store Zone CA and accumulate the CA pool for operational TLS.
	// We accumulate rather than replace so that multiple commissions
	// (multi-zone) can coexist -- each zone's CA must be trusted.
	r.connMgr.SetZoneCA(zoneCA)
	if r.connMgr.ZoneCAPool() == nil {
		r.connMgr.SetZoneCAPool(x509.NewCertPool())
	}
	r.connMgr.ZoneCAPool().AddCert(zoneCA.Certificate)

	// Generate controller operational cert for mutual TLS.
	controllerID := "test-controller"
	controllerCert, err := cert.GenerateControllerOperationalCert(zoneCA, controllerID)
	if err != nil {
		return "", fmt.Errorf("generate controller cert: %w", err)
	}
	r.connMgr.SetControllerCert(controllerCert)

	// Store the issued device cert for verify_device_cert.
	r.connMgr.SetIssuedDeviceCert(deviceCert)

	// Persist per-zone crypto so it can be restored after a second zone
	// overwrites the working fields (multi-zone scenario).
	r.connMgr.StoreZoneCrypto(zoneID)

	// Extract device ID from the signed certificate.
	deviceID, _ := cert.ExtractDeviceID(deviceCert)

	return deviceID, nil
}

// handlePASERequest handles the legacy pase_request action.
// For backward compatibility, this initiates commissioning and returns
// the expected outputs from the original stub.
func (r *Runner) handlePASERequest(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Get setup code - use "password" param for backward compatibility
	setupCode, err := r.getSetupCodeFromLegacyParams(step.Params)
	if err != nil {
		return nil, fmt.Errorf("invalid setup code: %w", err)
	}

	// DEC-084 §2.4: identities are protocol constants; salt comes from the
	// discriminator captured during mDNS browse.
	discriminator := r.connMgr.DiscoveredDiscriminator()

	// Create session but don't handshake yet - store for subsequent steps
	session, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		return nil, fmt.Errorf("failed to create PASE session: %w", err)
	}

	// Initialize state - we'll perform full handshake on pase_receive_verify
	r.connMgr.SetPASEState(&PASEState{
		session:   session,
		completed: false,
	})

	// Store params for the deferred handshake
	state.Set(StatePasePending, true)

	return map[string]any{
		KeyRequestSent: true,
		KeyPAGenerated: true,
	}, nil
}

// handlePASEReceiveResponse handles the legacy pase_receive_response action.
// Since the handshake is atomic, this just returns the expected outputs.
func (r *Runner) handlePASEReceiveResponse(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().session == nil {
		return nil, fmt.Errorf("no PASE session in progress: call pase_request first")
	}

	return map[string]any{
		KeyResponseReceived: true,
		KeyPBReceived:       true,
	}, nil
}

// handlePASEConfirm handles the legacy pase_confirm action.
// Since the handshake is atomic, this just returns the expected outputs.
func (r *Runner) handlePASEConfirm(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().session == nil {
		return nil, fmt.Errorf("no PASE session in progress")
	}

	return map[string]any{
		KeyConfirmSent: true,
	}, nil
}

// handlePASEReceiveVerify handles the legacy pase_receive_verify action.
// This is where we actually perform the handshake for legacy test cases.
func (r *Runner) handlePASEReceiveVerify(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().session == nil {
		return nil, fmt.Errorf("no PASE session in progress")
	}

	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Perform the actual handshake now
	sessionKey, err := r.connMgr.PASEState().session.Handshake(ctx, r.pool.Main().tlsConn)
	if err != nil {
		r.pool.Main().transitionTo(ConnDisconnected)
		return nil, fmt.Errorf("PASE handshake failed: %w", err)
	}

	// Update state
	r.connMgr.PASEState().sessionKey = sessionKey
	r.connMgr.PASEState().completed = true

	state.Set(KeySessionEstablished, true)
	state.Set(StateSessionKey, sessionKey)

	return map[string]any{
		KeyVerifyReceived:     true,
		KeySessionEstablished: true,
	}, nil
}

// handleVerifySessionKey verifies the derived session key.
func (r *Runner) handleVerifySessionKey(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.connMgr.PASEState() == nil {
		return nil, fmt.Errorf("no PASE session: commissioning not performed")
	}

	if !r.connMgr.PASEState().completed {
		return nil, fmt.Errorf("PASE session not completed")
	}

	key := r.connMgr.PASEState().sessionKey

	return map[string]any{
		KeyKeyLength:  len(key),
		KeyKeyNotZero: !isZeroKey(key),
	}, nil
}

// getSetupCode extracts the setup code from step parameters.
func (r *Runner) getSetupCode(params map[string]any) (commissioning.SetupCode, error) {
	// Try "setup_code" parameter first
	if sc, ok := params[KeySetupCode].(string); ok && sc != "" {
		return commissioning.ParseSetupCode(sc)
	}

	// Try config
	if r.config.SetupCode != "" {
		return commissioning.ParseSetupCode(r.config.SetupCode)
	}

	return 0, fmt.Errorf("no setup code provided: use setup_code parameter or -setup-code flag")
}

// getSetupCodeFromLegacyParams handles the legacy "password" parameter.
func (r *Runner) getSetupCodeFromLegacyParams(params map[string]any) (commissioning.SetupCode, error) {
	// Legacy test cases use "password" parameter
	if pw, ok := params[ParamPassword].(string); ok && pw != "" {
		// If it's an 8-digit numeric string, parse as setup code
		if len(pw) == 8 && isNumeric(pw) {
			return commissioning.ParseSetupCode(pw)
		}
		// For non-numeric passwords (like "test-password" in old tests),
		// generate a deterministic setup code from the password
		return deriveSetupCodeFromPassword(pw), nil
	}

	// Fall back to standard setup code handling
	return r.getSetupCode(params)
}

// isZeroKey checks if a key is all zeros.
func isZeroKey(key []byte) bool {
	for _, b := range key {
		if b != 0 {
			return false
		}
	}
	return true
}

// isNumeric checks if a string contains only digits.
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// deriveSetupCodeFromPassword creates a deterministic setup code from a password string.
// This provides backward compatibility with test cases that use non-numeric passwords.
func deriveSetupCodeFromPassword(password string) commissioning.SetupCode {
	// Simple hash to create an 8-digit code
	var hash uint32
	for _, c := range password {
		hash = hash*31 + uint32(c)
	}
	// Ensure it fits in 8 digits (max 99999999)
	return commissioning.SetupCode(hash % 100000000)
}

// extractPASEErrorCode parses a PASE error code from an error string like
// "PASE failed: error code 2" or "commissioning error code 5: device busy".
func extractPASEErrorCode(errStr string) (uint8, bool) {
	// Match either "error code N" or "(..., code N)" anywhere in the string.
	markers := []string{"error code ", "code "}
	start := -1
	for _, marker := range markers {
		if idx := strings.Index(errStr, marker); idx >= 0 {
			candidate := idx + len(marker)
			if start == -1 || candidate < start {
				start = candidate
			}
		}
	}
	if start < 0 || start >= len(errStr) {
		return 0, false
	}

	numStr := errStr[start:]
	// Extract digits (may be followed by ":" or end of string).
	end := 0
	for end < len(numStr) && numStr[end] >= '0' && numStr[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	var code uint8
	for _, c := range numStr[:end] {
		code = code*10 + uint8(c-'0')
	}
	return code, true
}

// paseErrorCodeName maps a PASE error code to the human-readable name used
// in test expectations.
func paseErrorCodeName(code uint8) string {
	switch code {
	case commissioning.ErrCodeSuccess:
		return PASEErrorSuccess
	case commissioning.ErrCodeAuthFailed: // 1
		return PASEErrorAuthFailed
	case 2: // legacy ErrCodeConfirmFailed — pre-DEC-047 mash-go emitted
		// this on MAC mismatch where spec §2.5 mandates ErrCodeAuthFailed.
		// Kept for backward-compat parsing of older devices' error strings.
		return PASEErrorVerificationFailed
	case commissioning.ErrCodeCSRFailed: // 3
		return PASEErrorCSRFailed
	case commissioning.ErrCodeCertInstallFailed: // 4
		return PASEErrorCertInstallFailed
	case commissioning.ErrCodeBusy: // 5
		return PASEErrorDeviceBusy
	case commissioning.ErrCodeZoneTypeExists: // 10
		return PASEErrorZoneTypeExists
	default:
		return fmt.Sprintf("ERROR_%d", code)
	}
}
