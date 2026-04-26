package service

// Commissioning flow: enter/exit window, PASE handshake, cert exchange.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// acceptCommissioningConnection checks if a new commissioning connection should be accepted.
// Returns true if the connection can proceed, false if it should be rejected.
// The rejection reason is returned as a string for diagnostic logging.
//
// Connection protection rules:
// 1. Only one commissioning connection at a time
// 2. Connection cooldown (500ms default) between completed attempts
// 3. All zone slots must not be full (commissioning would fail anyway)
//
// The cooldown timer starts when a connection is released (success or failure),
// not when it is accepted. This prevents legitimate follow-up attempts from
// being blocked by the cooldown of a still-in-progress connection.
// commissioningRejectReason classifies why acceptCommissioningConnection rejected.
type commissioningRejectReason int

const (
	rejectAlreadyInProgress commissioningRejectReason = iota + 1
	rejectCooldown
	rejectZonesFull
)

func (s *DeviceService) acceptCommissioningConnection() (bool, commissioningRejectReason, string, uint64) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()

	// Check 1: Is commissioning already in progress?
	if s.commissioningConnActive {
		return false, rejectAlreadyInProgress, "commissioning already in progress", 0
	}

	// Check 2: Connection cooldown (starts at release, not acceptance).
	// When PASE backoff is enabled, cooldown rejections are still counted as
	// failed attempts (see RecordFailure in the rejection path), so the
	// backoff tracker escalates through tiers even if cooldown blocks the
	// attempt from reaching the PASE handler.
	if s.config.ConnectionCooldown > 0 {
		elapsed := time.Since(s.lastCommissioningAttempt)
		if elapsed < s.config.ConnectionCooldown {
			return false, rejectCooldown, fmt.Sprintf("cooldown active (%s remaining)", s.config.ConnectionCooldown-elapsed), 0
		}
	}

	// Check 3: Is there a zone slot available?
	// TEST zones don't count against MaxZones (they're an extra observer slot).
	// If enable-key is valid, we might be getting a TEST zone, so accept even if full.
	enableKeyValid := s.isEnableKeyValid()

	// When enable-key is valid, evict disconnected zones to free slots.
	// This recovers from orphaned zones left by dead test connections.
	if enableKeyValid {
		if evicted := s.evictDisconnectedZone(); evicted != "" {
			s.debugLog("acceptCommissioningConnection: evicted stale zone(s)", "firstZoneID", evicted)
		}
	}

	s.mu.RLock()
	nonTestCount := s.nonTestZoneCountLocked()
	totalZoneCount := len(s.connectedZones)
	maxZones := s.config.MaxZones
	// Log zone state for debugging
	if nonTestCount >= maxZones {
		zoneStates := make([]string, 0, len(s.connectedZones))
		for zid, cz := range s.connectedZones {
			zoneStates = append(zoneStates, fmt.Sprintf("%s(type=%s,connected=%v)", zid, cz.Type, cz.Connected))
		}
		s.debugLog("acceptCommissioningConnection: zone slots full", "zones", zoneStates, "enableKeyValid", enableKeyValid)
	}
	s.mu.RUnlock()

	if nonTestCount >= maxZones && !enableKeyValid {
		return false, rejectZonesFull, fmt.Sprintf("zone slots full (%d/%d)", nonTestCount, maxZones), 0
	}

	// Even with enable-key, reject when all slots (including the TEST slot)
	// are occupied. The enable-key bypass allows a TEST zone, but if one
	// already exists alongside full non-test slots, there's truly no room.
	if totalZoneCount > maxZones {
		return false, rejectZonesFull, fmt.Sprintf("all zone slots full (%d non-test + %d test)", nonTestCount, totalZoneCount-nonTestCount), 0
	}

	// Accept the connection (cooldown timestamp is set on release, not here).
	// Bump the generation counter so stale goroutines from previous tests
	// cannot release this lock (they hold the old generation).
	s.commissioningConnActive = true
	s.commissioningGeneration++
	return true, 0, "", s.commissioningGeneration
}

// releaseCommissioningConnection marks the commissioning connection as complete
// and starts the cooldown timer. The generation parameter must match the value
// returned by acceptCommissioningConnection; stale goroutines from previous
// tests that hold an old generation are silently ignored, preventing them from
// releasing a lock they no longer own.
func (s *DeviceService) releaseCommissioningConnection(gen uint64) {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if s.commissioningGeneration != gen {
		// Stale release from a previous test's goroutine -- ignore.
		return
	}
	s.commissioningConnActive = false
	s.lastCommissioningAttempt = time.Now()
}

// ResetPASETracker resets the PASE attempt tracker.
// Called when commissioning window closes or commissioning succeeds.
func (s *DeviceService) ResetPASETracker() {
	if s.paseTracker != nil {
		s.paseTracker.Reset()
	}
}

// computeBusyRetryAfter returns the RetryAfter hint (in ms) for a busy rejection.
// The value depends on the rejection reason:
//   - Commissioning in progress: HandshakeTimeout ms (wait for current handshake to finish)
//   - Cooldown active: remaining cooldown ms
//   - Zones full: 0 (no point retrying until a zone is decommissioned)
func (s *DeviceService) computeBusyRetryAfter() uint32 {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()

	// Check if cooldown is active
	if s.config.ConnectionCooldown > 0 {
		elapsed := time.Since(s.lastCommissioningAttempt)
		remaining := s.config.ConnectionCooldown - elapsed
		if remaining > 0 {
			return uint32(remaining.Milliseconds())
		}
	}

	// Check if commissioning is in progress
	if s.commissioningConnActive {
		return uint32(s.config.HandshakeTimeout.Milliseconds())
	}

	// Zones full or unknown reason. Per DEC-063, RetryAfter is 0 for
	// zones-full because there is no predictable retry window -- the
	// controller should not retry until a zone is explicitly decommissioned.
	return 0
}

// randomErrorDelay returns a random duration between ErrorDelayMin and ErrorDelayMax.
// This is used to add jitter to error responses to prevent timing attacks (DEC-047).
func (s *DeviceService) randomErrorDelay() time.Duration {
	if !s.config.GenericErrors {
		return 0
	}
	if s.config.ErrorDelayMin >= s.config.ErrorDelayMax {
		return s.config.ErrorDelayMin
	}

	// Generate random delay in the range [min, max]
	delayRange := s.config.ErrorDelayMax - s.config.ErrorDelayMin
	randomBytes := make([]byte, 8)
	rand.Read(randomBytes)
	// Convert to uint64 and take modulo to get random offset
	randomOffset := time.Duration(0)
	for _, b := range randomBytes {
		randomOffset = (randomOffset << 8) | time.Duration(b)
	}
	randomOffset = randomOffset % (delayRange + 1)

	return s.config.ErrorDelayMin + randomOffset
}

// EnterCommissioningMode opens the commissioning window.
func (s *DeviceService) EnterCommissioningMode() error {
	s.mu.Lock()

	var discoveryState string
	if s.discoveryManager != nil {
		discoveryState = s.discoveryManager.State().String()
	}
	zoneCount := len(s.connectedZones)

	s.debugLog("EnterCommissioningMode: called",
		"serviceState", s.state.String(),
		"discoveryState", discoveryState,
		"zoneCount", zoneCount)

	if s.state != StateRunning {
		s.debugLog("EnterCommissioningMode: rejected - service not running", "state", s.state.String())
		s.mu.Unlock()
		return ErrNotStarted
	}

	// DEC-047: Reset PASE backoff when a new commissioning window opens
	// so controllers get a fresh start without accumulated delays.
	s.ResetPASETracker()

	// Capture context before releasing lock.
	ctx := s.ctx
	dm := s.discoveryManager

	// Open the commissioning ALPN gate and bump the epoch under the lock.
	// The epoch lets exitCommissioningForEpoch detect that a new window
	// opened between decision time and execution time, preventing a stale
	// exit from overriding a newer enter.
	if s.disconnectReentryTimer != nil {
		s.disconnectReentryTimer.Stop()
		s.disconnectReentryTimer = nil
	}
	s.disconnectReentryBlockedUntil = time.Time{}
	s.commissioningOpen.Store(true)
	s.commissioningEpoch.Add(1)
	s.mu.Unlock()
	if err := s.ensureListenerStarted(); err != nil {
		s.commissioningOpen.Store(false)
		return fmt.Errorf("start listener: %w", err)
	}

	// Start mDNS commissioning advertising outside the lock because mDNS
	// operations can take >1s on macOS and would block new connections
	// from acquiring even a read lock on s.mu.
	if dm != nil {
		if err := dm.EnterCommissioningMode(ctx); err != nil {
			s.debugLog("EnterCommissioningMode: failed", "error", err)
			s.commissioningOpen.Store(false)
			return err
		}
	}

	s.debugLog("EnterCommissioningMode: success")
	s.emitEvent(Event{Type: EventCommissioningOpened})
	return nil
}

// ExitCommissioningMode closes the commissioning window.
func (s *DeviceService) ExitCommissioningMode() error {
	s.mu.Lock()

	var discoveryState string
	if s.discoveryManager != nil {
		discoveryState = s.discoveryManager.State().String()
	}
	s.debugLog("ExitCommissioningMode: called", "discoveryState", discoveryState)

	// DEC-047: Reset PASE tracker when commissioning window closes
	s.ResetPASETracker()

	zoneCount := len(s.connectedZones)
	dm := s.discoveryManager
	s.disconnectReentryBlockedUntil = time.Now().Add(s.disconnectReentryHoldoff)
	s.mu.Unlock()

	// Close the commissioning ALPN gate.
	s.commissioningOpen.Store(false)

	// Stop listener if no zones exist (nothing needs the port).
	if zoneCount == 0 {
		s.stopListener()
	}

	// Stop mDNS commissioning advertising outside the lock because mDNS
	// operations can take >1s on macOS and would block new connections.
	if dm != nil {
		if err := dm.ExitCommissioningMode(); err != nil {
			s.debugLog("ExitCommissioningMode: failed", "error", err)
			return err
		}
	}

	s.debugLog("ExitCommissioningMode: success")
	s.emitEvent(Event{Type: EventCommissioningClosed, Reason: "commissioned"})
	return nil
}

// exitCommissioningForEpoch exits commissioning only if the commissioning
// epoch hasn't changed since the caller captured it. This prevents a stale
// exit from overriding a concurrent EnterCommissioningMode (e.g., triggered
// by HandleZoneDisconnect auto-reentry while ExitCommissioningMode was
// still running slow mDNS operations).
func (s *DeviceService) exitCommissioningForEpoch(epoch uint64) error {
	s.mu.Lock()
	if s.commissioningEpoch.Load() != epoch {
		s.debugLog("exitCommissioningForEpoch: skipped (commissioning re-entered)",
			"capturedEpoch", epoch, "currentEpoch", s.commissioningEpoch.Load())
		s.mu.Unlock()
		return nil
	}

	var discoveryState string
	if s.discoveryManager != nil {
		discoveryState = s.discoveryManager.State().String()
	}
	s.debugLog("ExitCommissioningMode: called", "discoveryState", discoveryState)

	s.ResetPASETracker()

	// Close the gate under the same lock that verified the epoch,
	// so no concurrent EnterCommissioningMode can slip in between.
	s.commissioningOpen.Store(false)
	s.disconnectReentryBlockedUntil = time.Now().Add(s.disconnectReentryHoldoff)

	zoneCount := len(s.connectedZones)
	dm := s.discoveryManager
	s.mu.Unlock()

	if zoneCount == 0 {
		s.stopListener()
	}

	if dm != nil {
		if err := dm.ExitCommissioningMode(); err != nil {
			s.debugLog("ExitCommissioningMode: failed", "error", err)
			return err
		}
	}

	s.debugLog("ExitCommissioningMode: success")
	s.emitEvent(Event{Type: EventCommissioningClosed, Reason: "commissioned"})
	return nil
}

// handleCommissioningConnection handles PASE commissioning over TLS.
// After PASE succeeds, it performs the certificate exchange to receive an
// operational certificate from the controller's Zone CA.
//
// DEC-061: The commissioning lock is NOT acquired until the first PASERequest
// message arrives. This prevents idle TLS connections from blocking commissioning.
// Flow: Create PASE -> WaitForPASERequest (no lock) -> Acquire lock -> CompleteHandshake -> Cert exchange -> Release lock.
func (s *DeviceService) handleCommissioningConnection(rawConn net.Conn, conn *tls.Conn, releaseActiveConn func()) {
	s.debugLog("handleCommissioningConnection: entered", "remoteAddr", conn.RemoteAddr().String())

	// Phase 1: Create PASE session and wait for first message (no lock held).
	// DEC-084 §2.4: identity strings are protocol constants, no longer params.
	paseSession, err := commissioning.NewPASEServerSession(s.verifier)
	if err != nil {
		s.debugLog("handleCommissioningConnection: NewPASEServerSession failed", "error", err)
		conn.Close()
		return
	}

	// DEC-061: Wait for PASERequest with short timeout (no lock held)
	firstMsgCtx := s.ctx
	if s.config.PASEFirstMessageTimeout > 0 {
		var cancel context.CancelFunc
		firstMsgCtx, cancel = context.WithTimeout(s.ctx, s.config.PASEFirstMessageTimeout)
		defer cancel()
	}

	req, err := paseSession.WaitForPASERequest(firstMsgCtx, conn)
	if err != nil {
		// Timeout or connection closed before PASERequest -- no lock was held
		s.debugLog("handleCommissioningConnection: WaitForPASERequest failed (no lock held)", "error", err)
		conn.Close()
		return
	}

	// DEC-047: Apply PASE backoff delay before acquiring the commissioning lock.
	// This must happen before acceptCommissioningConnection so that connections
	// rejected by cooldown still experience the backoff delay.
	if s.paseTracker != nil {
		delay := s.paseTracker.GetDelay()
		s.debugLog("handleCommissioningConnection: backoff check",
			"delay", delay,
			"failedAttempts", s.paseTracker.AttemptCount())
		if delay > 0 {
			s.debugLog("handleCommissioningConnection: applying backoff delay", "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
				s.debugLog("handleCommissioningConnection: backoff delay completed")
			case <-s.ctx.Done():
				timer.Stop()
				conn.Close()
				return
			}
		}
	}

	// Phase 2: PASERequest received -- now acquire the commissioning lock
	ok, rejectReason, reason, commGen := s.acceptCommissioningConnection()
	if !ok {
		// DEC-047: Count cooldown rejections as failed attempts so the
		// backoff tracker escalates. Skip "already in progress" since
		// the concurrent PASE will be counted when it completes -- counting
		// both would double-penalize and mis-align backoff tiers.
		if s.paseTracker != nil && rejectReason != rejectAlreadyInProgress {
			s.paseTracker.RecordFailure()
		}
		s.debugLog("handleCommissioningConnection: rejected after PASERequest",
			"reason", reason,
			"failedAttempts", func() int {
				if s.paseTracker != nil {
					return s.paseTracker.AttemptCount()
				}
				return -1
			}())
		retryAfterMs := s.computeBusyRetryAfter()
		_ = commissioning.WriteCommissioningError(conn, commissioning.ErrCodeBusy, reason, retryAfterMs)
		conn.Close()
		return
	}
	commReleased := false
	defer func() {
		if !commReleased {
			s.releaseCommissioningConnection(commGen)
		}
	}()

	// DEC-047: Overall handshake timeout (starts at PASERequest, not TLS accept)
	handshakeCtx := s.ctx
	if s.config.HandshakeTimeout > 0 {
		var cancel context.CancelFunc
		handshakeCtx, cancel = context.WithTimeout(s.ctx, s.config.HandshakeTimeout)
		defer cancel()
	}

	// Phase 3: Complete PASE handshake (lock held)
	paseSession.PhaseTimeout = s.config.PASEPhaseTimeout
	sharedSecret, err := paseSession.CompleteHandshake(handshakeCtx, conn, req)
	if err != nil {
		// PASE failed - wrong setup code or protocol error
		// DEC-047: Record failure for backoff
		if s.paseTracker != nil {
			s.paseTracker.RecordFailure()
			s.debugLog("handleCommissioningConnection: PASE failed, recorded failure",
				"error", err,
				"failedAttempts", s.paseTracker.AttemptCount())
		} else {
			s.debugLog("handleCommissioningConnection: PASE failed (no tracker)",
				"error", err)
		}
		// Release lock before delay so immediate follow-up attempts are not
		// rejected as "commissioning already in progress" after the failure
		// has already been decided.
		s.releaseCommissioningConnection(commGen)
		commReleased = true
		// Brief delay before closing so the error message bytes are fully
		// delivered to the peer. Without this, the test harness may see an
		// EOF before reading the CommissioningError frame (TC-ZONE-ADD-003).
		time.Sleep(100 * time.Millisecond)
		conn.Close()
		return
	}

	// DEC-047: Reset PASE tracker on successful authentication
	s.ResetPASETracker()

	// Reset test-mode clock offset on new PASE so operational
	// connections during this commission use real time for cert
	// validation. Without this, a stale offset from a previous
	// test would cause verifyClientCert to reject the fresh
	// operational cert issued in the current cert exchange.
	s.mu.Lock()
	s.clockOffset = 0
	s.mu.Unlock()

	// Derive zone ID from shared secret
	zoneID := deriveZoneID(sharedSecret)

	s.debugLog("handleCommissioningConnection: PASE succeeded, starting cert exchange", "zoneID", zoneID)

	// Create framed connection FIRST - needed for cert exchange messages
	framedConn := newFramedConnection(conn)

	// Perform certificate exchange with controller
	// This is the critical step that gives us an operational cert from the Zone CA
	zoneType := cert.ZoneTypeLocal
	operationalCert, zoneCA, err := s.performCertExchange(framedConn, zoneID, func(zoneCA *x509.Certificate) error {
		// Extract zone type from the Zone CA certificate's OU field.
		// Falls back to LOCAL if extraction fails (backward compatibility).
		if zt, parseErr := cert.ExtractZoneTypeFromCert(zoneCA); parseErr == nil {
			zoneType = zt
		}

		// Reject TEST zones unless a valid enable-key is configured (DEC-060).
		// TEST zones are an extra "observer" slot that doesn't count against MaxZones.
		if zoneType == cert.ZoneTypeTest && !s.isEnableKeyValid() {
			return errCommissionTestZoneDisabled
		}

		// TEST zones are infrastructure-only observer channels. If a previous
		// TEST controller disconnected without RemoveZone, recycle that stale
		// disconnected TEST zone before duplicate-type validation so a new test
		// harness session can bootstrap cleanly.
		if zoneType == cert.ZoneTypeTest && s.isEnableKeyValid() {
			s.evictDisconnectedZonesOfType(cert.ZoneTypeTest)
		}

		// Reject zones when a zone of the same type already exists (DEC-043).
		// Each device supports max 1 zone per type.
		if s.hasZoneOfType(zoneType) {
			return errCommissionZoneTypeExists
		}

		// Reject GRID/LOCAL zones when slots are full.
		// We accepted the connection earlier because enable-key was valid (potential TEST zone),
		// but now we know it's not a TEST zone, so check slots again.
		if zoneType != cert.ZoneTypeTest && s.isZonesFull() {
			return errCommissionZoneSlotsFull
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, errCommissionTestZoneDisabled) {
			s.debugLog("handleCommissioningConnection: TEST zone rejected (no valid enable-key)", "zoneID", zoneID)
			conn.Close()
			return
		}
		if errors.Is(err, errCommissionZoneTypeExists) {
			s.debugLog("handleCommissioningConnection: zone type already exists", "zoneID", zoneID, "zoneType", zoneType)
			_ = commissioning.WriteCommissioningError(conn, commissioning.ErrCodeZoneTypeExists, "zone type already exists", 0)
			conn.Close()
			return
		}
		if errors.Is(err, errCommissionZoneSlotsFull) {
			s.debugLog("handleCommissioningConnection: GRID/LOCAL zone rejected (slots full)", "zoneID", zoneID, "zoneType", zoneType)
			retryAfterMs := s.computeBusyRetryAfter()
			_ = commissioning.WriteCommissioningError(conn, commissioning.ErrCodeBusy, "zone slots full", retryAfterMs)
			conn.Close()
			return
		}
		s.debugLog("handleCommissioningConnection: cert exchange failed", "error", err)
		conn.Close()
		return
	}
	commissionAccepted := false
	defer func() {
		if !commissionAccepted {
			s.rollbackCommissioningCertArtifacts(zoneID)
		}
	}()

	// Extract device ID from operational certificate (Matter-style: embedded in CommonName)
	deviceID, err := cert.ExtractDeviceID(operationalCert.Certificate)
	if err != nil {
		s.debugLog("handleCommissioningConnection: failed to extract device ID", "error", err)
		conn.Close()
		return
	}

	s.debugLog("handleCommissioningConnection: cert exchange complete",
		"deviceID", deviceID,
		"zoneID", zoneID,
		"certExpires", operationalCert.Certificate.NotAfter)

	// Update service device ID (use first zone's ID as primary)
	s.mu.Lock()
	if s.deviceID == "" {
		s.deviceID = deviceID
	}
	s.mu.Unlock()

	// Ensure listener is running for the controller's operational reconnection.
	if err := s.ensureListenerStarted(); err != nil {
		s.debugLog("handleCommissioningConnection: failed to ensure listener", "error", err)
		conn.Close()
		return
	}

	// Store Zone CA for future verification of controller connections
	_ = zoneCA // Zone CA already stored in performCertExchange

	// All validation passed -- now update TLS cert to use the latest operational cert.
	// This is intentionally placed AFTER zone validation so that rejected zones
	// (e.g., duplicate zone type) do not pollute the device's TLS configuration.
	s.mu.Lock()
	s.tlsCert = operationalCert.TLSCertificate()
	s.buildOperationalTLSConfig()
	s.mu.Unlock()

	// Capture the commissioning epoch BEFORE registering the zone.
	// RegisterZoneAwaitingConnection enables the concurrent auto-reentry
	// path (HandleZoneDisconnect → EnterCommissioningMode), which would
	// increment the epoch. The epoch guard ensures the exit below is
	// skipped if a newer commissioning window opened in the meantime.
	exitEpoch := s.commissioningEpoch.Load()

	// DEC-066: Register the zone as awaiting operational connection.
	// The zone is marked as disconnected; the controller will reconnect
	// with operational certificates via handleOperationalConnection.
	s.RegisterZoneAwaitingConnection(zoneID, zoneType)
	commissionAccepted = true

	// Persist state immediately after commissioning
	_ = s.SaveState()

	// Signal handleOperationalConnection to re-enter commissioning mode
	// after the operational TLS reconnect succeeds. Set BEFORE the exit
	// so the operational handler can consume it even if the exit runs first.
	if s.isEnableKeyValid() {
		s.mu.Lock()
		s.autoReentryPending = true
		s.mu.Unlock()
	}

	// Exit commissioning mode. The epoch guard prevents this from
	// overriding a concurrent EnterCommissioningMode that may have
	// been triggered by HandleZoneDisconnect auto-reentry.
	if err := s.exitCommissioningForEpoch(exitEpoch); err != nil {
		s.debugLog("handleCommissioningConnection: failed to exit commissioning mode", "error", err)
	}

	// DEC-066: Close the commissioning connection. The controller must
	// reconnect using operational TLS with the newly issued certificate.
	// This provides a clean trust boundary between PASE and operational sessions.
	s.debugLog("handleCommissioningConnection: closing commissioning connection (DEC-066)", "zoneID", zoneID)

	// Release resources before closing
	s.releaseCommissioningConnection(commGen)
	s.connTracker.Remove(rawConn)
	releaseActiveConn()

	// Close the commissioning connection
	conn.Close()

	// Emit commissioned event (zone added, but not yet connected via operational TLS)
	s.emitEvent(Event{
		Type:   EventCommissioned,
		ZoneID: zoneID,
	})
}

func (s *DeviceService) rollbackCommissioningCertArtifacts(zoneID string) {
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()
	if certStore == nil {
		return
	}

	if err := certStore.RemoveOperationalCert(zoneID); err != nil && err != cert.ErrCertNotFound {
		s.debugLog("rollbackCommissioningCertArtifacts: remove operational cert failed", "zoneID", zoneID, "error", err)
		return
	}
	if err := certStore.Save(); err != nil {
		s.debugLog("rollbackCommissioningCertArtifacts: save failed", "zoneID", zoneID, "error", err)
	}
}

// performCertExchange handles the certificate exchange protocol with the controller.
// It receives the Zone CA, generates a new key pair, sends a CSR, and installs
// the signed operational certificate.
//
// Protocol flow:
// 1. Receive CertRenewalRequest with ZoneCA from controller
// 2. Generate NEW key pair (not reusing commissioning key)
// 3. Send CertRenewalCSR with device's CSR
// 4. Receive CertRenewalInstall with signed operational cert
// 5. Verify and store operational cert + Zone CA
// 6. Send CertRenewalAck
func (s *DeviceService) performCertExchange(
	conn *framedConnection,
	zoneID string,
	validateZone func(*x509.Certificate) error,
) (*cert.OperationalCert, *x509.Certificate, error) {
	// Step 1: Wait for CertRenewalRequest from controller
	data, err := conn.ReadFrame()
	if err != nil {
		return nil, nil, fmt.Errorf("read cert renewal request: %w", err)
	}

	msg, err := commissioning.DecodeRenewalMessage(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode cert renewal request: %w", err)
	}

	certReq, ok := msg.(*commissioning.CertRenewalRequest)
	if !ok {
		return nil, nil, fmt.Errorf("expected CertRenewalRequest, got %T", msg)
	}

	// Verify we received the Zone CA
	if len(certReq.ZoneCA) == 0 {
		return nil, nil, fmt.Errorf("CertRenewalRequest missing Zone CA")
	}

	// Parse the Zone CA certificate
	zoneCA, err := x509.ParseCertificate(certReq.ZoneCA)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Zone CA: %w", err)
	}

	s.debugLog("performCertExchange: received Zone CA",
		"issuer", zoneCA.Issuer.String(),
		"notAfter", zoneCA.NotAfter)
	if validateZone != nil {
		if err := validateZone(zoneCA); err != nil {
			return nil, nil, err
		}
	}

	// Step 2: Generate NEW key pair for this zone
	// Important: We generate a fresh key pair, NOT reusing the commissioning key
	keyPair, err := cert.GenerateKeyPair()
	if err != nil {
		return nil, nil, fmt.Errorf("generate key pair: %w", err)
	}

	// Step 3: Create and send CSR
	csrInfo := &cert.CSRInfo{
		Identity: cert.DeviceIdentity{
			DeviceID:  "", // Will be derived from cert
			VendorID:  s.device.VendorID(),
			ProductID: s.device.ProductID(),
		},
	}

	csrDER, err := cert.CreateCSR(keyPair, csrInfo)
	if err != nil {
		return nil, nil, fmt.Errorf("create CSR: %w", err)
	}

	csrResp := &commissioning.CertRenewalCSR{
		MsgType: commissioning.MsgCertRenewalCSR,
		CSR:     csrDER,
	}

	csrData, err := cbor.Marshal(csrResp)
	if err != nil {
		return nil, nil, fmt.Errorf("encode CSR: %w", err)
	}

	if err := conn.Send(csrData); err != nil {
		return nil, nil, fmt.Errorf("send CSR: %w", err)
	}

	// Step 4: Wait for signed certificate
	data, err = conn.ReadFrame()
	if err != nil {
		return nil, nil, fmt.Errorf("read cert install: %w", err)
	}

	msg, err = commissioning.DecodeRenewalMessage(data)
	if err != nil {
		return nil, nil, fmt.Errorf("decode cert install: %w", err)
	}

	certInstall, ok := msg.(*commissioning.CertRenewalInstall)
	if !ok {
		return nil, nil, fmt.Errorf("expected CertRenewalInstall, got %T", msg)
	}

	// Parse the new operational certificate
	newCert, err := x509.ParseCertificate(certInstall.NewCert)
	if err != nil {
		return nil, nil, fmt.Errorf("parse operational cert: %w", err)
	}

	// Verify the certificate is signed by the Zone CA
	roots := x509.NewCertPool()
	roots.AddCert(zoneCA)
	if _, err := newCert.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		return nil, nil, fmt.Errorf("verify operational cert: %w", err)
	}

	// Step 5: Store operational cert and Zone CA
	operationalCert := &cert.OperationalCert{
		Certificate: newCert,
		PrivateKey:  keyPair.PrivateKey,
		ZoneID:      zoneID,
	}

	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore != nil {
		// Store operational cert for this zone
		if err := certStore.SetOperationalCert(operationalCert); err != nil {
			return nil, nil, fmt.Errorf("store operational cert: %w", err)
		}

		// Store Zone CA for this zone
		if err := certStore.SetZoneCACert(zoneID, zoneCA); err != nil {
			return nil, nil, fmt.Errorf("store Zone CA: %w", err)
		}

		// Persist to disk
		if err := certStore.Save(); err != nil {
			return nil, nil, fmt.Errorf("save cert store: %w", err)
		}
	}

	// Step 6: Send acknowledgment
	ack := &commissioning.CertRenewalAck{
		MsgType:        commissioning.MsgCertRenewalAck,
		Status:         commissioning.RenewalStatusSuccess,
		ActiveSequence: certInstall.Sequence,
	}

	ackData, err := cbor.Marshal(ack)
	if err != nil {
		return nil, nil, fmt.Errorf("encode ack: %w", err)
	}

	if err := conn.Send(ackData); err != nil {
		return nil, nil, fmt.Errorf("send ack: %w", err)
	}

	s.debugLog("performCertExchange: complete",
		"zoneID", zoneID,
		"certExpires", newCert.NotAfter)

	return operationalCert, zoneCA, nil
}
