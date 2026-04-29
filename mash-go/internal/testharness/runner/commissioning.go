package runner

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// ensureCommissioned checks if already commissioned; if not, connects and performs PASE.
func (r *Runner) ensureCommissioned(ctx context.Context, state *engine.ExecutionState) error {
	// Track whether we already had a live connection before ensureConnected.
	wasConnected := r.pool.Main() != nil && r.pool.Main().isConnected()

	r.debugf("ensureCommissioned: wasConnected=%v paseState=%v paseCompleted=%v",
		wasConnected,
		r.connMgr.PASEState() != nil,
		r.connMgr.PASEState() != nil && r.connMgr.PASEState().completed)

	// If main is dead but we have a commissioned suite zone, recover main
	// operationally from suite crypto context before any fresh commissioning.
	if !wasConnected && r.connMgr.PASEState() != nil && r.connMgr.PASEState().completed && r.suite.ZoneID() != "" {
		r.debugf("ensureCommissioned: recovering dead main from suite zone before ensureConnected")
		if err := r.reconnectMainFromSuite(state); err == nil {
			state.Set(KeySessionEstablished, true)
			state.Set(KeyConnectionEstablished, true)
			if r.connMgr.PASEState().sessionKey != nil {
				state.Set(StateSessionKey, r.connMgr.PASEState().sessionKey)
				state.Set(StateSessionKeyLen, len(r.connMgr.PASEState().sessionKey))
			}
			return nil
		}
	}

	// Already-commissioned short-circuit. If pool.Main is *operational* and
	// PASE has completed, the test is asking for "commissioned" — that's
	// already true. Returning early here also lets ensureConnected (which
	// runs below for any other shape) reject operational pool.Main without
	// regressing the level-3 path. Without this short-circuit, ensureConnected's
	// "operational pool.Main → reconnect on commissioning ALPN" rule (added
	// for TC-PASE-003) would tear down the live commissioned session every
	// time a level-3 precondition runs.
	if wasConnected &&
		r.pool.Main().state == ConnOperational &&
		r.connMgr.PASEState() != nil && r.connMgr.PASEState().completed {
		r.debugf("ensureCommissioned: pool.Main already operational + PASE complete; reusing session")
		state.Set(KeySessionEstablished, true)
		state.Set(KeyConnectionEstablished, true)
		if r.connMgr.PASEState().sessionKey != nil {
			state.Set(StateSessionKey, r.connMgr.PASEState().sessionKey)
			state.Set(StateSessionKeyLen, len(r.connMgr.PASEState().sessionKey))
		}
		// Restore suite zone crypto so subsequent steps see the right pool.
		suiteZoneID := r.suite.ZoneID()
		if suiteZoneID != "" && r.connMgr.LoadZoneCrypto(suiteZoneID) {
			r.debugf("ensureCommissioned: restored suite zone crypto from zone map")
		} else {
			crypto := r.suite.Crypto()
			if crypto.ZoneCAPool != nil {
				r.connMgr.SetWorkingCrypto(crypto)
				r.debugf("ensureCommissioned: restored suite zone crypto from suite snapshot")
			}
		}
		return nil
	}

	// First ensure we're connected.
	if err := r.ensureConnected(ctx, state); err != nil {
		r.debugf("ensureCommissioned: ensureConnected failed: %v", err)
		return err
	}

	// If already commissioned AND the connection was already live (not
	// freshly established), we can reuse the existing session. If the
	// connection was dead and ensureConnected created a new one, the
	// old PASE session is invalid -- the device expects PASE on the
	// new connection, so we must redo commissioning.
	if r.connMgr.PASEState() != nil && r.connMgr.PASEState().completed {
		if wasConnected {
			r.debugf("ensureCommissioned: reusing existing PASE session")
			// Restore suite zone crypto from per-zone map (preferred) or
			// suite snapshot (fallback). The per-zone map automatically
			// rebuilds an accumulated pool containing ALL zone CAs.
			suiteZoneID := r.suite.ZoneID()
			if suiteZoneID != "" && r.connMgr.LoadZoneCrypto(suiteZoneID) {
				r.debugf("ensureCommissioned: restored suite zone crypto from zone map")
			} else {
				crypto := r.suite.Crypto()
				if crypto.ZoneCAPool != nil {
					r.connMgr.SetWorkingCrypto(crypto)
					r.debugf("ensureCommissioned: restored suite zone crypto from suite snapshot")
				}
			}
			state.Set(KeySessionEstablished, true)
			state.Set(KeyConnectionEstablished, true)
			if r.connMgr.PASEState().sessionKey != nil {
				state.Set(StateSessionKey, r.connMgr.PASEState().sessionKey)
				state.Set(StateSessionKeyLen, len(r.connMgr.PASEState().sessionKey))
			}
			return nil
		}
		if r.suite.ZoneID() != "" {
			r.debugf("ensureCommissioned: main connection was re-established, recovering main from suite zone")
			if err := r.reconnectMainFromSuite(state); err == nil {
				state.Set(KeySessionEstablished, true)
				state.Set(KeyConnectionEstablished, true)
				if r.connMgr.PASEState() != nil && r.connMgr.PASEState().sessionKey != nil {
					state.Set(StateSessionKey, r.connMgr.PASEState().sessionKey)
					state.Set(StateSessionKeyLen, len(r.connMgr.PASEState().sessionKey))
				}
				return nil
			} else {
				r.debugf("ensureCommissioned: recover main from suite failed: %v", err)
			}
		}
		// Connection was re-established -- old PASE session is stale.
		r.debugf("ensureCommissioned: connection was re-established, clearing stale PASE state")
		r.connMgr.SetPASEState(nil)
	}

	// Create a synthetic step to drive handleCommission.
	// _from_precondition tells handleCommission to skip creating a tracking
	// connection -- ensureCommissioned calls transitionToOperational after,
	// which handles the operational connection and zone registration.
	step := &loader.Step{
		Action: "commission",
		Params: map[string]any{
			ParamFromPrecondition: true,
		},
	}

	// Pass setup_code from config if available.
	if r.config.SetupCode != "" {
		step.Params["setup_code"] = r.config.SetupCode
	}

	_, err := r.handleCommission(ctx, step, state)
	if err != nil {
		classified := classifyPASEError(err)

		// DEC-065: If the device rejects with a cooldown error, wait for the
		// remaining cooldown and retry once. This handles transitions between
		// auto-PICS discovery and test execution where the cooldown from the
		// previous commissioning is still active.
		if wait := cooldownRemaining(err); wait > 0 {
			r.debugf("ensureCommissioned: cooldown active, waiting %s", wait.Round(time.Millisecond))
			if err := contextSleep(ctx, wait); err != nil {
				return fmt.Errorf("cooldown wait cancelled: %w", err)
			}
			// Clear connection but preserve suite zone state so multi-zone
			// precondition loops don't lose the suite zone on retry.
			r.disconnectConnection()
			r.connMgr.ClearWorkingCrypto()
			if connErr := r.ensureConnected(ctx, state); connErr != nil {
				return fmt.Errorf("precondition commission cooldown retry connect failed: %w", connErr)
			}
			_, err = r.handleCommission(ctx, step, state)
			if err != nil {
				return fmt.Errorf("precondition commission failed after cooldown wait: %w", err)
			}
			if r.connMgr.PASEState() == nil || !r.connMgr.PASEState().completed {
				return fmt.Errorf("precondition commission: PASE did not complete after cooldown retry")
			}
			if err := r.transitionToOperational(state); err != nil {
				return err
			}
			if r.isSuiteZoneCommission() {
				r.suite.Record(r.suite.ZoneID(), r.connMgr.WorkingCrypto())
				r.debugf("ensureCommissioned: updated suite zone crypto after cooldown retry commission")
			}
			return nil
		}

		// Device errors (wrong setup code, zone type exists) are not retryable.
		if Category(classified) == ErrCatDevice {
			return fmt.Errorf("precondition commission rejected by device: %w", err)
		}

		// Infrastructure errors (network, zone slots full via auto-evict): retry
		// after disconnect + delay. The device may still be cycling its
		// commissioning window in test mode, especially after zone removals.
		const maxRetries = 2
		if Category(classified) == ErrCatInfrastructure {
			for retry := 1; retry <= maxRetries; retry++ {
				// Clear connection but preserve suite zone state.
				r.disconnectConnection()
				r.connMgr.ClearWorkingCrypto()
				if err := contextSleep(ctx, 1*time.Second); err != nil {
					return fmt.Errorf("infrastructure retry wait cancelled: %w", err)
				}
				if connErr := r.ensureConnected(ctx, state); connErr != nil {
					return fmt.Errorf("precondition commission retry %d connect failed: %w", retry, connErr)
				}
				_, err = r.handleCommission(ctx, step, state)
				if err == nil {
					if r.connMgr.PASEState() == nil || !r.connMgr.PASEState().completed {
						return fmt.Errorf("precondition commission: PASE did not complete on retry %d", retry)
					}
					if trErr := r.transitionToOperational(state); trErr != nil {
						return trErr
					}
					if r.isSuiteZoneCommission() {
						r.suite.Record(r.suite.ZoneID(), r.connMgr.WorkingCrypto())
						r.debugf("ensureCommissioned: updated suite zone crypto after infra retry commission")
					}
					return nil
				}
				retryClassified := classifyPASEError(err)
				if Category(retryClassified) != ErrCatInfrastructure {
					break
				}
			}
			return fmt.Errorf("precondition commission failed after %d retries: %w", maxRetries, err)
		}

		// Protocol errors: fail immediately.
		return fmt.Errorf("precondition commission failed: %w", err)
	}

	// handleCommission may return nil error for PASE protocol failures
	// (device-sent error codes). Check paseState to detect these.
	if r.connMgr.PASEState() == nil || !r.connMgr.PASEState().completed {
		return fmt.Errorf("precondition commission: PASE handshake did not complete")
	}

	if err := r.transitionToOperational(state); err != nil {
		return err
	}

	// After a fresh commission that re-creates the suite zone, update the
	// saved suite crypto so subsequent session-reuse restores use the correct
	// (current) crypto. Only update when this commission IS the suite zone;
	// secondary zones (GRID/LOCAL from two_zones_connected) must not
	// overwrite suite crypto.
	if r.isSuiteZoneCommission() {
		r.suite.Record(r.suite.ZoneID(), r.connMgr.WorkingCrypto())
		r.debugf("ensureCommissioned: updated suite zone crypto after fresh commission")
	}

	return nil
}

func (r *Runner) alignLifecycleForCommissioning(authority string) error {
	if !r.strictLifecycleEnabled() {
		return nil
	}
	if r.lifecycleController().State() != LifecycleDisconnected {
		r.lifecycleController().ToDisconnected()
	}
	return r.lifecycleController().ToCommissioning(authority)
}

// isSuiteZoneCommission returns true when the most recent commission created
// (or re-created) the suite zone. Secondary zones such as GRID/LOCAL from
// two_zones_connected must not overwrite the suite zone's saved crypto.
//
// Heuristic: if the suite zone connection is still alive, this commission
// created a secondary zone alongside it. If the suite zone connection is
// dead or gone, this commission replaces it.
func (r *Runner) isSuiteZoneCommission() bool {
	if r.suite.ZoneID() == "" {
		return false
	}
	ps := r.connMgr.PASEState()
	if ps == nil || len(ps.sessionKey) == 0 {
		return false
	}
	return deriveZoneIDFromSecret(ps.sessionKey) == r.suite.ZoneID()
}

// reconnectMainFromSuite rebuilds pool.Main() as an operational connection
// using the suite zone's crypto context, without mutating suite.Conn().
func (r *Runner) reconnectMainFromSuite(state *engine.ExecutionState) error {
	if r.suite.ZoneID() == "" {
		return fmt.Errorf("no suite zone to recover main from")
	}

	suiteZoneID := r.suite.ZoneID()
	if !r.connMgr.LoadZoneCrypto(suiteZoneID) {
		crypto := r.suite.Crypto()
		if crypto.ZoneCAPool == nil || crypto.ControllerCert == nil {
			return fmt.Errorf("no suite crypto material for main recovery")
		}
		r.connMgr.SetWorkingCrypto(crypto)
		r.debugf("reconnectMainFromSuite: restored suite crypto from snapshot")
	} else {
		r.debugf("reconnectMainFromSuite: restored suite crypto from zone map")
	}

	target := r.getTarget(nil)
	ctx := context.Background()
	wc := r.WorkingCrypto()
	tlsConn, dialErr := dialWithRetry(ctx, 3, func() (*tls.Conn, error) {
		return r.dialer.DialOperational(ctx, target, canonicalZoneIDFromCrypto(wc), wc)
	})
	if dialErr != nil {
		return fmt.Errorf("reconnect main from suite failed: %w", dialErr)
	}

	newConn := &Connection{
		tlsConn: tlsConn,
		framer:  transport.NewFramer(tlsConn),
		state:   ConnOperational,
	}
	if err := waitForOperationalReadyOnConn(
		newConn,
		2*time.Second,
		r.nextMessageID,
		r.pool.AppendNotification,
		r.debugf,
	); err != nil {
		newConn.transitionTo(ConnDisconnected)
		return fmt.Errorf("reconnect main from suite readiness failed: %w", err)
	}

	r.pool.SetMain(newConn)
	state.Set(StateConnection, newConn)
	state.Set(StateOperationalConnEstablished, time.Now())
	return nil
}

// transitionToOperational closes the commissioning connection and establishes
// a new operational TLS connection using the controller certificate received
// during cert exchange. This implements DEC-066: the device closes the
// commissioning connection after sending CertInstallAck, and the controller
// must reconnect with operational certificates.
//
// The new connection is registered in activeZoneConns so that
// closeActiveZoneConns can send RemoveZone and close it between tests.
// Without this registration, the zone leaks on the device and subsequent
// tests fail with "zone slots full".
func (r *Runner) transitionToOperational(state *engine.ExecutionState) error {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().sessionKey == nil {
		return fmt.Errorf("no PASE session to transition")
	}

	zoneID := deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)

	// DEC-066: Close the commissioning connection.
	// The device has already closed its end after sending CertInstallAck.
	if r.pool.Main() != nil {
		r.debugf("transitionToOperational: closing commissioning connection")
		_ = r.pool.Main().Close()
	}
	r.pool.SetMain(&Connection{})

	// DEC-066: Establish new operational TLS connection.
	// Retry the dial briefly in case the device hasn't finished registering
	// the zone as awaiting reconnection.
	r.debugf("transitionToOperational: reconnecting with operational TLS")

	target := r.getTarget(nil)
	ctx := context.Background()
	crypto := r.WorkingCrypto()
	tlsConn, dialErr := dialWithRetry(ctx, 3, func() (*tls.Conn, error) {
		return r.dialer.DialOperational(ctx, target, canonicalZoneIDFromCrypto(crypto), crypto)
	})
	if dialErr != nil {
		r.invalidateCommissioningState(state)
		return fmt.Errorf("operational reconnection failed: %w", dialErr)
	}

	// Create new connection wrapper
	newConn := &Connection{
		tlsConn: tlsConn,
		framer:  transport.NewFramer(tlsConn),
		state:   ConnOperational,
	}
	r.pool.SetMain(newConn)
	state.Set(StateConnection, newConn)
	// Record timestamp for verify_timing (TC-TRANS-004).
	state.Set(StateOperationalConnEstablished, time.Now())

	// Verify the device is processing protocol messages on this connection.
	if err := r.waitForOperationalReady(2 * time.Second); err != nil {
		newConn.transitionTo(ConnDisconnected)
		r.invalidateCommissioningState(state)
		return fmt.Errorf("operational readiness failed: %w", err)
	}

	// Register the commissioned zone so closeActiveZoneConns can clean it up.
	connKey := "main-" + zoneID
	r.pool.TrackZone(connKey, newConn, zoneID)
	r.debugf("transitionToOperational: reconnected and registered zone %s in activeZoneConns", zoneID)
	if r.strictLifecycleEnabled() {
		if err := r.lifecycleController().ToOperational(connKey); err != nil {
			return err
		}
	}

	return nil
}

// reconnectToZone re-establishes an operational TLS connection to an existing
// suite zone using stored crypto material. No PASE/cert exchange needed.
// Returns an error if the reconnection fails.
func (r *Runner) reconnectToZone(state *engine.ExecutionState) error {
	if r.suite.ZoneID() == "" {
		return fmt.Errorf("no suite zone to reconnect to")
	}
	// Restore suite zone crypto from the per-zone map first (preferred --
	// contains the accumulated pool). Fall back to suite snapshot for
	// backward compatibility.
	suiteZoneID := r.suite.ZoneID()
	if !r.connMgr.LoadZoneCrypto(suiteZoneID) {
		crypto := r.suite.Crypto()
		if crypto.ZoneCAPool == nil || crypto.ControllerCert == nil {
			return fmt.Errorf("no crypto material for reconnection")
		}
		r.connMgr.SetWorkingCrypto(crypto)
		r.debugf("reconnectToZone: restored suite zone crypto from suite snapshot (fallback)")
	} else {
		r.debugf("reconnectToZone: restored suite zone crypto from zone map")
	}

	r.debugf("reconnectToZone: reconnecting to zone %s", r.suite.ZoneID())

	target := r.getTarget(nil)
	ctx := context.Background()
	wc := r.WorkingCrypto()
	const readinessRetries = 2
	var lastErr error

	canonicalZID := canonicalZoneIDFromCrypto(wc)
	prevMain := r.pool.Main()
	for attempt := 1; attempt <= readinessRetries; attempt++ {
		tlsConn, dialErr := dialWithRetry(ctx, 3, func() (*tls.Conn, error) {
			return r.dialer.DialOperational(ctx, target, canonicalZID, wc)
		})
		if dialErr != nil {
			lastErr = fmt.Errorf("reconnectToZone failed: %w", dialErr)
			break
		}

		newConn := &Connection{
			tlsConn: tlsConn,
			framer:  transport.NewFramer(tlsConn),
			state:   ConnOperational,
		}

		// Verify the device accepts us on this connection.
		if err := waitForOperationalReadyOnConn(
			newConn,
			2*time.Second,
			r.nextMessageID,
			r.pool.AppendNotification,
			r.debugf,
		); err != nil {
			lastErr = fmt.Errorf("reconnectToZone readiness failed: %w", err)
			r.debugf("reconnectToZone: readiness check failed (attempt %d/%d): %v", attempt, readinessRetries, err)
			newConn.transitionTo(ConnDisconnected)
			restoreMainControlChannel(r.pool, prevMain, state)
			if attempt < readinessRetries && isIOError(err) {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return lastErr
		}
		setMainControlChannel(r.pool, newConn, state)

		// Store on suite session (not in pool -- suite zone lives outside pool).
		r.suite.SetConn(newConn)
		r.debugf("reconnectToZone: reconnected to zone %s", r.suite.ZoneID())
		if r.strictLifecycleEnabled() {
			if err := r.lifecycleController().ReconnectOperational(r.suite.ConnKey()); err != nil {
				return err
			}
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("reconnectToZone failed")
}

// cooldownRemaining extracts the remaining cooldown duration from a PASE
// handshake error. Returns 0 if the error is not a cooldown rejection.
// Error format: "...cooldown active (123.456ms remaining)..."
func cooldownRemaining(err error) time.Duration {
	if err == nil {
		return 0
	}
	msg := err.Error()
	const marker = "cooldown active ("
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return 0
	}
	rest := msg[idx+len(marker):]
	endIdx := strings.Index(rest, " remaining)")
	if endIdx < 0 {
		return 0
	}
	d, parseErr := time.ParseDuration(rest[:endIdx])
	if parseErr != nil {
		return 0
	}
	// Add a buffer that covers TLS reconnection overhead (~35ms) plus margin.
	return d + 200*time.Millisecond
}

// isTransientError returns true for errors that may resolve on retry.
// Checks for classified infrastructure errors first, then falls back to
// IO-level pattern matching for unclassified errors.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Classified errors: only infrastructure is retryable.
	var ce *ClassifiedError
	if errors.As(err, &ce) {
		return ce.Category == ErrCatInfrastructure
	}
	// Unclassified: fall back to IO pattern matching.
	return isIOError(err)
}

// deriveZoneIDFromSecret derives the runner's internal zone-tracking key
// from a PASE shared secret. This is NOT the spec zone-id (DEC-088 mandates
// SHA-256(Zone-CA-cert-DER)[:8] -- see canonicalZoneIDFromCrypto). It is a
// stable per-PASE-session string the harness uses as a map key for its own
// zone bookkeeping (pool.TrackZone, activeZoneConns, suite zone, ...).
//
// Mixing the two breaks SRV-resolve under DEC-082 two-port deployments:
// mDNS instances are keyed on the canonical zone-id, so any browse filter
// must use that. Use this function only for runner-internal tracking; pass
// canonicalZoneIDFromCrypto(crypto) to the dialer.
func deriveZoneIDFromSecret(secret []byte) string {
	hash := sha256.Sum256(secret)
	return hex.EncodeToString(hash[:8])
}

// canonicalZoneIDFromCrypto returns the DEC-088 zone-id of the zone whose
// CA cert is held in crypto. Returns "" when no Zone CA is loaded yet
// (pre-commissioning, or after ClearWorkingCrypto). Pass this to the dialer
// for SRV-resolve filtering -- the device advertises operational mDNS
// records with this id in the ZI TXT field.
func canonicalZoneIDFromCrypto(crypto CryptoState) string {
	if crypto.ZoneCA == nil || crypto.ZoneCA.Certificate == nil {
		return ""
	}
	return discovery.ZoneIDFromCertificate(crypto.ZoneCA.Certificate)
}

// commissionSuiteZone performs one-time suite-level commissioning.
// The suite zone is commissioned as ZoneTypeTest so it doesn't count
// against the device's MaxZones and is transparent to device logic.
func (r *Runner) commissionSuiteZone(ctx context.Context) error {
	r.debugf("commissionSuiteZone: commissioning device for suite")
	state := engine.NewExecutionState(ctx)

	// Always establish a fresh suite control channel. Reusing stale PASE from
	// prior test-local commissioning can leave suite attached to a dead socket.
	r.disconnectConnection()
	r.connMgr.SetPASEState(nil)
	r.connMgr.ClearWorkingCrypto()

	r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
	if err := r.ensureCommissioned(ctx, state); err != nil {
		r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
		return fmt.Errorf("suite commissioning: %w", err)
	}
	r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)

	r.recordSuiteZone()
	r.debugf("commissionSuiteZone: suite zone established (id=%s key=%s)", r.suite.ZoneID(), r.suite.ConnKey())
	return nil
}

// recordSuiteZone captures the current commissioned zone as the suite zone.
// It also saves the crypto state so it can be restored after lower-level
// tests clear the working crypto (non-commissioned tests nil out zoneCA
// and zoneCAPool to avoid stale TLS configs).
//
// The suite zone connection is moved out of the ConnPool so that pool-level
// operations (close, scan, cleanup) never touch it.
func (r *Runner) recordSuiteZone() {
	if r.connMgr.PASEState() == nil || r.connMgr.PASEState().sessionKey == nil {
		return
	}
	zoneID := deriveZoneIDFromSecret(r.connMgr.PASEState().sessionKey)
	r.suite.Record(zoneID, r.connMgr.WorkingCrypto())

	// Move the suite zone connection from pool to suite session.
	connKey := r.suite.ConnKey()
	if conn := r.pool.Zone(connKey); conn != nil {
		r.suite.SetConn(conn)
		r.pool.UntrackZone(connKey)
	} else if r.pool.Main() != nil && r.pool.Main().isConnected() {
		r.suite.SetConn(r.pool.Main())
	}

	// Detach main so the suite zone connection lives independently.
	// Tier transitions only affect pool.Main(), never suite.Conn().
	r.pool.SetMain(&Connection{})
}

// removeSuiteZone sends RemoveZone for the suite zone and clears all state.
// pool.Main() is typically empty (detached) after recordSuiteZone, so this
// promotes suite.Conn() to main for sending RemoveZone. If the suite
// connection is dead, we attempt reconnection. Cleanup always proceeds.
func (r *Runner) removeSuiteZone() {
	zoneID := r.suite.ZoneID()
	r.debugf("removeSuiteZone: tearing down suite zone %s", zoneID)

	conn := r.resolveSuiteTeardownConn()
	if zoneID != "" {
		if conn != nil {
			if r.config != nil && r.config.StrictLifecycle {
				if err := r.sendRemoveZoneOnConnStrict(conn, zoneID); err != nil {
					r.setStrictLifecycleErr(err)
				}
			} else {
				r.sendRemoveZoneOnConn(conn, zoneID)
			}
		} else if r.config != nil && r.config.StrictLifecycle {
			r.setStrictLifecycleErr(errRemoveZoneNoConnection)
		}
	}
	r.closeAllZoneConns()
	r.ensureDisconnected()
}

func (r *Runner) resolveSuiteTeardownConn() *Connection {
	isLive := func(c *Connection) bool {
		return c != nil && c.isConnected() && c.framer != nil
	}

	if sc := r.suite.Conn(); isLive(sc) {
		return sc
	}
	if mc := r.pool.Main(); isLive(mc) {
		r.suite.SetConn(mc)
		return mc
	}
	if r.config != nil && r.config.Target != "" && r.suite.ZoneID() != "" {
		state := engine.NewExecutionState(context.Background())
		if err := r.reconnectToZone(state); err != nil {
			r.debugf("removeSuiteZone: reconnect failed: %v", err)
			return nil
		}
		if sc := r.suite.Conn(); isLive(sc) {
			return sc
		}
		if mc := r.pool.Main(); isLive(mc) {
			r.suite.SetConn(mc)
			return mc
		}
	}
	return nil
}
