package runner

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
)

// Coordinator manages test lifecycle orchestration: setting up preconditions,
// tearing down tests, and tracking the current precondition level. It
// encapsulates the decision tree that was previously embedded in
// Runner.setupPreconditions, making it independently testable via mocks.
type Coordinator interface {
	// SetupPreconditions inspects tc.Preconditions and ensures the runner
	// is in the correct state (connected, commissioned, etc.) before the
	// test executes.
	SetupPreconditions(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState) error

	// TeardownTest is called after each test completes (pass or fail).
	// It cleans up subscriptions, security pool connections, and stale
	// PASE state so the next test starts clean.
	TeardownTest(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState)

	// CurrentLevel returns the runner's current precondition level based
	// on connection and commissioning state.
	CurrentLevel() int
}

// StateAccessor provides read/write access to commissioning metadata.
// Used by Coordinator.SetupPreconditions and TeardownTest for state queries.
type StateAccessor interface {
	PASEState() *PASEState
	SetPASEState(ps *PASEState)
	DeviceStateModified() bool
	SetDeviceStateModified(modified bool)
	WorkingCrypto() CryptoState
	SetWorkingCrypto(crypto CryptoState)
	ClearWorkingCrypto()
	CommissionZoneType() cert.ZoneType
	SetCommissionZoneType(zt cert.ZoneType)
	DiscoveredDiscriminator() uint16
	LastDeviceConnClose() time.Time
	SetLastDeviceConnClose(t time.Time)
	IsSuiteZoneCommission() bool

	// Per-zone crypto storage
	StoreZoneCrypto(zoneID string)
	LoadZoneCrypto(zoneID string) bool
	HasZoneCrypto(zoneID string) bool
	RemoveZoneCrypto(zoneID string)
	ClearAllCrypto()
}

// LifecycleOps manages connection and commissioning transitions.
// Used by Coordinator.SetupPreconditions and TeardownTest for state transitions.
type LifecycleOps interface {
	EnsureConnected(ctx context.Context, state *engine.ExecutionState) error
	EnsureCommissioned(ctx context.Context, state *engine.ExecutionState) error
	DisconnectConnection()
	EnsureDisconnected()
	ReconnectToZone(state *engine.ExecutionState) error
}

// WireOps sends protocol-level control messages.
// Used by Coordinator.TeardownTest for cleanup.
type WireOps interface {
	SendRemoveZone()
	SendRemoveZoneOnConn(conn *Connection, zoneID string)
	SendTriggerViaZone(ctx context.Context, trigger uint64, state *engine.ExecutionState) error
	SendClearLimitInvoke(ctx context.Context) error
	SendSetLimitInvoke(ctx context.Context) error
}

// DiagnosticsOps provides health inspection and state capture.
// Used by Coordinator for session probing and device state snapshots.
type DiagnosticsOps interface {
	ProbeSessionHealth() error
	RequestDeviceState(ctx context.Context, state *engine.ExecutionState) DeviceStateSnapshot
	DebugSnapshot(label string)
}

// PreconditionHandler dispatches precondition setup logic.
// Used by Coordinator.SetupPreconditions.
type PreconditionHandler interface {
	WaitForCommissioningMode(ctx context.Context, timeout time.Duration) error
	HandlePreconditionCases(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState,
		preconds []loader.Condition, needsMultiZone *bool) error
}

// CommissioningOps defines the operations the coordinator needs from the
// runner to manage test lifecycle. It composes the focused sub-interfaces
// above for backward compatibility. Runner implements all methods.
type CommissioningOps interface {
	StateAccessor
	LifecycleOps
	WireOps
	DiagnosticsOps
	PreconditionHandler
}

// coordinatorImpl is the production implementation of Coordinator.
type coordinatorImpl struct {
	suite  SuiteSession
	pool   ConnPool
	ops    CommissioningOps
	config *Config
	debugf func(string, ...any)
}

// NewCoordinator creates a Coordinator backed by the given components.
func NewCoordinator(suite SuiteSession, pool ConnPool, ops CommissioningOps, config *Config, debugf func(string, ...any)) Coordinator {
	return &coordinatorImpl{
		suite:  suite,
		pool:   pool,
		ops:    ops,
		config: config,
		debugf: debugf,
	}
}

// SetupPreconditions inspects tc.Preconditions and ensures the runner is in
// the right state. When transitioning backwards (e.g., from commissioned to
// commissioning), it disconnects to give the device a clean state.
func (c *coordinatorImpl) SetupPreconditions(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState) error {
	// Populate setup_code so that test steps using ${setup_code} resolve correctly.
	if c.config.SetupCode != "" {
		state.Set(StateSetupCode, c.config.SetupCode)
	}

	// Populate device_discriminator from auto-PICS discovery.
	if c.ops.DiscoveredDiscriminator() > 0 {
		state.Set(StateDeviceDiscriminator, int(c.ops.DiscoveredDiscriminator()))
	}

	// Compute the needed precondition level early.
	needed := preconditionLevelFor(tc.Preconditions)
	current := c.CurrentLevel()

	c.debugf("setupPreconditions %s: current=%d needed=%d", tc.ID, current, needed)
	c.ops.DebugSnapshot("setupPreconditions BEFORE " + tc.ID)

	// Clear stale notification buffer from previous tests.
	c.pool.ClearNotifications()

	// Reset commission zone type to TEST between tests. TestControl commands
	// require a TEST zone, so the default must be TEST (not 0/unspecified).
	c.ops.SetCommissionZoneType(cert.ZoneTypeTest)

	// Always reset device test state between tests.
	if c.config.Target != "" && c.config.EnableKey != "" {
		c.debugf("resetting device test state")
		resetCtx, resetCancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.ops.SendTriggerViaZone(resetCtx, features.TriggerResetTestState, state)
		resetCancel()
		if err != nil {
			c.debugf("reset trigger failed: %v, attempting reconnect+retry", err)
			if c.suite.ZoneID() != "" && c.ops.WorkingCrypto().ZoneCAPool != nil {
				if reconErr := c.ops.ReconnectToZone(state); reconErr != nil {
					c.debugf("reconnect for reset retry failed: %v (continuing)", reconErr)
				} else {
					retryCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
					err = c.ops.SendTriggerViaZone(retryCtx, features.TriggerResetTestState, state)
					retryCancel()
					if err != nil {
						c.debugf("reset trigger retry failed: %v (continuing)", err)
					}
				}
			}
		}
		c.ops.SetDeviceStateModified(false)

		// Normalize leaked non-suite zones before capturing the baseline.
		// Otherwise the next teardown can fail on a "fixed leak" diff
		// (before had leaked zones, after cleanup removed them).
		c.normalizeResidualNonSuiteZones(state)

		// Capture device state snapshot AFTER reset, BEFORE preconditions.
		snapCtx, snapCancel := context.WithTimeout(ctx, 3*time.Second)
		if before := c.ops.RequestDeviceState(snapCtx, state); before != nil {
			state.Custom[engine.StateKeyDeviceStateBefore] = map[string]any(before)
		}
		snapCancel()
	}

	// Clear stale zone CA state for non-commissioned tests.
	needsZoneConns := hasPrecondition(tc.Preconditions, PrecondTwoZonesConnected) ||
		hasPrecondition(tc.Preconditions, PrecondTwoZonesWithLimits) ||
		hasPreconditionInt(tc.Preconditions, PrecondZoneCountAtLeast, 2) ||
		hasPreconditionString(tc.Preconditions, PrecondSecondZoneConnected)
	if needed < precondLevelCommissioned && !needsZoneConns {
		crypto := c.ops.WorkingCrypto()
		if crypto.ZoneCA != nil || crypto.ControllerCert != nil || crypto.ZoneCAPool != nil {
			c.debugf("clearing stale zone CA state (needed=%d < commissioned)", needed)
		}
		c.ops.ClearWorkingCrypto()
	}

	// Connection tier determines session isolation strategy.
	tier := connectionTierFor(tc)
	c.debugf("preconditions: tier=%s needed=%d current=%d for %s", tier, needed, current, tc.ID)
	forceFreshForCommissionStep := needed <= precondLevelConnected && testCaseHasAction(tc, ActionCommission)
	restoreCommissionZoneType := c.ops.CommissionZoneType()
	restoreCommissionZoneTypeSet := false
	defer func() {
		if restoreCommissionZoneTypeSet {
			c.ops.SetCommissionZoneType(restoreCommissionZoneType)
		}
	}()
	if forceFreshForCommissionStep && c.suite.ZoneID() != "" && c.ops.CommissionZoneType() == cert.ZoneTypeTest {
		// Keep suite on TEST; commission-step tests use a disposable non-suite zone.
		c.debugf("commission-step: overriding test session zone type to LOCAL (suite TEST preserved)")
		c.ops.SetCommissionZoneType(cert.ZoneTypeLocal)
		restoreCommissionZoneTypeSet = true
	}
	needsIsolatedConnectSession := testCaseNeedsIsolatedCommissionedSession(tc)
	needsControllerAuthority := testCaseNeedsControllerAuthority(tc)
	needsIsolatedSession := needsIsolatedConnectSession || needsControllerAuthority
	if needed >= precondLevelCommissioned && needsIsolatedSession && c.suite.ZoneID() != "" {
		// Two sibling reasons to force a disposable, non-suite session:
		//   1. Connection-probing tests must not mutate the suite control channel.
		//   2. Per DEC-060, TEST zones (the auto-PICS suite default) are pure
		//      observers and excluded from limit/setpoint resolution. Tests that
		//      exercise SetLimit/SetSetpoint/etc. against the suite session see
		//      `effectiveConsumptionLimit` stay nil and `controlState` stay
		//      AUTONOMOUS. Commission a fresh LOCAL zone for those tests.
		if c.ops.CommissionZoneType() == cert.ZoneTypeTest {
			c.ops.SetCommissionZoneType(cert.ZoneTypeLocal)
			restoreCommissionZoneTypeSet = true
		}
		if m := c.pool.Main(); m != nil && m == c.suite.Conn() {
			detachMainControlChannel(c.pool, state)
		} else {
			c.ops.DisconnectConnection()
		}
		c.ops.SetPASEState(nil)
		c.ops.ClearWorkingCrypto()
	}

	// Session reuse decision: only application-tier tests can reuse.
	canReuseSession := tier == TierApplication &&
		current >= precondLevelCommissioned &&
		needed >= precondLevelCommissioned &&
		!needsIsolatedSession &&
		!needsZoneConns &&
		!hasPrecondition(tc.Preconditions, PrecondDeviceZonesFull) &&
		!hasPrecondition(tc.Preconditions, PrecondDeviceHasGridZone) &&
		!hasPrecondition(tc.Preconditions, PrecondDeviceHasLocalZone) &&
		!hasPrecondition(tc.Preconditions, PrecondSessionPreviouslyConnected)

	// Verify session health before reusing.
	if canReuseSession && c.config.Target != "" {
		if err := c.ops.ProbeSessionHealth(); err != nil {
			c.debugf("session health check failed for %s: %v", tc.ID, err)
			if c.suite.ZoneID() != "" {
				c.debugf("attempting reconnect to suite zone %s", c.suite.ZoneID())
				if reconnErr := c.ops.ReconnectToZone(state); reconnErr != nil {
					c.debugf("reconnect failed: %v, falling back to fresh commission", reconnErr)
					canReuseSession = false
				} else {
					c.debugf("reconnected to suite zone %s", c.suite.ZoneID())
				}
			} else {
				canReuseSession = false
			}
		}
	}

	if !canReuseSession {
		freshTeardownDone := false
		hadActive := c.pool.ZoneCount() > 0
		if hadActive {
			c.debugf("closing %d stale zone connections", c.pool.ZoneCount())
		}
		if (needsFreshCommission(tc.Preconditions) || forceFreshForCommissionStep) && c.suite.ZoneID() != "" {
			// Clean architecture invariant: fresh_commission is test-session
			// isolation only and must never mutate suite control session state.
			c.debugf("fresh_commission: preserving suite zone %s; resetting test session only", c.suite.ZoneID())
			c.pool.CloseZonesExcept(c.suite.ConnKey())
			c.ops.DisconnectConnection()
			c.ops.ClearWorkingCrypto()
			c.ops.SetPASEState(nil)
			freshTeardownDone = true
		} else if (needsFreshCommission(tc.Preconditions) || forceFreshForCommissionStep) && c.suite.ZoneID() == "" {
			// No suite to preserve: full reset is allowed.
			c.pool.CloseAllZones()
			c.ops.EnsureDisconnected()
			freshTeardownDone = true
		} else if c.suite.ZoneID() != "" {
			// Preserve the suite zone's pool entry so that the device keeps
			// it registered (onZoneClose sends RemoveZone which would kill it).
			c.pool.CloseZonesExcept(c.suite.ConnKey())
		} else {
			c.pool.CloseAllZones()
		}
		// DEC-059: backward transition from commissioned.
		if current >= precondLevelCommissioned && needed <= precondLevelCommissioning && !freshTeardownDone {
			if c.suite.ZoneID() != "" {
				c.debugf("backward transition: detaching main (suite zone %s stays alive)", c.suite.ZoneID())
				c.pool.SetMain(&Connection{})
				c.ops.SetPASEState(nil)
			} else {
				// Strict lifecycle: avoid raising a synthetic "no live connection"
				// error when the main socket is already gone. The disconnect path
				// below will reset state deterministically.
				if m := c.pool.Main(); m != nil && m.isConnected() && c.ops.PASEState().Completed() {
					c.debugf("backward transition: sending RemoveZone (current=%d -> needed=%d)", current, needed)
					c.ops.SendRemoveZone()
				} else {
					c.debugf("backward transition: skipping RemoveZone (no live commissioned connection)")
				}
			}
		}

		// Backwards transition: disconnect to give the device a clean state.
		if needed < current && needed <= precondLevelCommissioning && !freshTeardownDone {
			c.debugf("backward transition: disconnecting (current=%d -> needed=%d)", current, needed)
			if c.suite.ZoneID() != "" {
				if sc := c.suite.Conn(); sc != nil && sc.isConnected() {
					c.debugf("backward transition: closing suite zone TCP to free cap slot")
					sc.transitionTo(ConnDisconnected)
				}
				c.pool.SetMain(&Connection{})
				c.ops.SetPASEState(nil)
			} else {
				c.ops.EnsureDisconnected()
			}
			if c.config.Target != "" {
				c.ops.SetLastDeviceConnClose(time.Now())
			}
		}
	} else {
		// Clean up extra zones from previous multi-zone tests.
		if !needsZoneConns {
			for _, id := range c.pool.ZoneKeys() {
				conn := c.pool.Zone(id)
				if conn == c.pool.Main() {
					continue
				}
				c.debugf("reusing session: cleaning up extra zone %s", id)
				if conn != nil && conn.isConnected() && conn.framer != nil {
					zoneID := c.pool.ZoneID(id)
					if zoneID != "" {
						c.ops.SendRemoveZoneOnConn(conn, zoneID)
					}
				}
				if conn != nil {
					conn.transitionTo(ConnDisconnected)
				}
				c.pool.UntrackZone(id)
			}
		}
		c.debugf("reusing session for %s (skipping closeActiveZoneConns)", tc.ID)
	}

	// Store simulation precondition keys in state.
	for _, cond := range tc.Preconditions {
		for key, val := range cond {
			if simulationPreconditionKeys[key] {
				state.Set(key, val)
			}
		}
	}

	// Detect multi-zone setup needs.
	needsMultiZone := false
	for _, cond := range tc.Preconditions {
		if v, ok := cond[PrecondZoneCountAtLeast]; ok {
			if n, ok := v.(int); ok && n >= 2 {
				needsMultiZone = true
			}
		}
		if v, ok := cond[PrecondSecondZoneConnected]; ok {
			if s, ok := v.(string); ok && s != "" {
				needsMultiZone = true
			}
		}
		if v, ok := cond[PrecondTwoZonesWithLimits]; ok {
			if b, ok := v.(bool); ok && b {
				needsMultiZone = true
			}
		}
	}

	// Inject two_zones_connected if needed.
	preconds := tc.Preconditions
	if needsMultiZone && !hasPrecondition(tc.Preconditions, PrecondTwoZonesConnected) {
		preconds = append(preconds, map[string]any{PrecondTwoZonesConnected: true})
	}

	// Save crypto state before precondition handlers may replace it.
	savedCrypto := c.ops.WorkingCrypto()
	savedPASE := c.ops.PASEState()

	// Handle preconditions that require special setup.
	if err := c.ops.HandlePreconditionCases(ctx, tc, state, preconds, &needsMultiZone); err != nil {
		return err
	}

	// Handle needsMultiZone fallthrough.
	if needsMultiZone {
		state.Set(PrecondTwoZonesConnected, true)
		ct := getConnectionTracker(state)
		if len(ct.zoneConnections) < 2 {
			if c.config.Target == "" {
				for _, z := range []string{"GRID", "LOCAL"} {
					if _, exists := ct.zoneConnections[z]; !exists {
						dummyConn := &Connection{state: ConnOperational}
						ct.zoneConnections[z] = dummyConn
						c.pool.TrackZone(z, dummyConn, z)
					}
				}
			}
		}
	}

	// Reset untracked commission sessions. Skip when a suite zone exists --
	// the suite zone is the persistent control channel and must not be destroyed
	// by stale PASE state from a previous test's commission step.
	if needed >= precondLevelCommissioned &&
		c.ops.PASEState().Completed() &&
		c.pool.ZoneCount() == 0 &&
		c.suite.ZoneID() == "" &&
		c.config.Target != "" {
		c.debugf("resetting untracked commission session (no activeZoneConns)")
		c.ops.EnsureDisconnected()
	}

	c.ops.DebugSnapshot("setupPreconditions AFTER " + tc.ID)

	// Level switch: ensure the right connection/commissioning level.
	var setupErr error
	usedSuiteSession := false
	switch needed {
	case precondLevelCommissioned:
		// Determine whether we should use the existing suite zone instead of
		// creating or revalidating a separate test session zone.
		// Use suite when: (1) suite zone exists and (2) zone type is still TEST
		// (not overridden by a precondition like device_has_grid_zone).
		// This keeps current_zone_id aligned with the control channel and avoids
		// stale PASE/session-key drift from previous non-suite commissions.
		suiteCanReconnect := c.suite.ZoneID() != "" &&
			c.ops.CommissionZoneType() == cert.ZoneTypeTest &&
			!needsIsolatedSession &&
			!needsZoneConns

		if suiteCanReconnect {
			suiteZoneID := c.suite.ZoneID()
			// Borrowing the live suite control channel must also restore the
			// suite zone crypto context (zone CA/controller cert pool). Some TLS
			// negative-cert tests construct certs from the active zone CA and will
			// fail with "requires a zone CA" if working crypto stays empty.
			if suiteZoneID != "" {
				if c.ops.LoadZoneCrypto(suiteZoneID) {
					c.debugf("restored suite zone crypto from zone map for %s", tc.ID)
				} else {
					crypto := c.suite.Crypto()
					if crypto.ZoneCA != nil || crypto.ControllerCert != nil || crypto.ZoneCAPool != nil {
						c.ops.SetWorkingCrypto(crypto)
						c.debugf("restored suite zone crypto from suite snapshot for %s", tc.ID)
					}
				}
			}

			// Close stale main if it's not the suite conn and this test does not
			// require additional non-suite zone channels.
			if !needsZoneConns {
				if m := c.pool.Main(); m != nil && m != c.suite.Conn() && m.isConnected() {
					_ = m.Close()
				}
			}

			// Borrow suite connection if it's alive; fall back to reconnect.
			if borrowSuiteControlChannelIfAlive(c.pool, c.suite, state) {
				c.debugf("borrowing suite zone %s for %s", c.suite.ZoneID(), tc.ID)
			} else {
				detachMainControlChannel(c.pool, state)
				c.debugf("reconnecting to suite zone %s for %s (suite conn dead)", c.suite.ZoneID(), tc.ID)
				setupErr = c.ops.ReconnectToZone(state)
				if setupErr != nil {
					c.debugf("reconnect to suite zone FAILED for %s: %v", tc.ID, setupErr)
					return setupErr
				}
			}
			state.Set(KeySessionEstablished, true)
			state.Set(KeyConnectionEstablished, true)
			state.Set(StateCurrentZoneID, c.suite.ZoneID())
			state.Set(StateTestZoneID, c.suite.ZoneID())
			usedSuiteSession = true
		} else {
			c.debugf("ensuring commissioned for %s", tc.ID)
			setupErr = c.ops.EnsureCommissioned(ctx, state)
			if setupErr != nil {
				c.debugf("ensureCommissioned FAILED for %s: %v", tc.ID, setupErr)
				return setupErr
			}
		}
		// Store zone IDs for test interpolation.
		if !usedSuiteSession && !needsZoneConns && c.ops.PASEState().Completed() && c.ops.PASEState().SessionKey() != nil {
			zID := deriveZoneIDFromSecret(c.ops.PASEState().SessionKey())
			var stateKey, zoneLabel string
			switch c.ops.CommissionZoneType() {
			case cert.ZoneTypeGrid:
				stateKey = StateGridZoneID
				zoneLabel = ZoneTypeGrid
			case cert.ZoneTypeLocal, 0:
				stateKey = StateLocalZoneID
				zoneLabel = ZoneTypeLocal
			case cert.ZoneTypeTest:
				stateKey = StateTestZoneID
				zoneLabel = ZoneTypeTest
			}
			if stateKey != "" {
				state.Set(stateKey, zID)
			}
			state.Set(StateCurrentZoneID, zID)
			if zoneLabel != "" {
				zs := getZoneState(state)
				if z, exists := zs.zones[zoneLabel]; exists {
					z.ZoneID = zID
				}
			}
		}
	case precondLevelConnected:
		if current > precondLevelConnected {
			c.debugf("downgrading from commissioned to connected for %s", tc.ID)
			if c.suite.ZoneID() != "" {
				detachMainControlChannel(c.pool, state)
				c.ops.SetPASEState(nil)
			} else {
				c.ops.EnsureDisconnected()
			}
		}
		c.debugf("ensuring connected for %s", tc.ID)
		setupErr = c.ops.EnsureConnected(ctx, state)
		if setupErr != nil {
			return setupErr
		}
	case precondLevelCommissioning:
		c.debugf("ensuring commissioning mode for %s", tc.ID)
		if c.suite.ZoneID() != "" {
			c.debugf("preserving suite zone %s during commissioning setup", c.suite.ZoneID())
			if c.pool.Main() != nil && c.pool.Main().isConnected() {
				detachMainControlChannel(c.pool, state)
			}
			c.ops.SetPASEState(nil)

			needsControlChannel := c.config.Target != "" &&
				c.config.EnableKey != "" &&
				testCaseHasAction(tc, ActionTriggerTestEvent)
			if needsControlChannel {
				if !borrowSuiteControlChannelIfAlive(c.pool, c.suite, state) {
					c.debugf("commissioning setup: suite control channel dead, reconnecting")
					if err := c.ops.ReconnectToZone(state); err != nil {
						return err
					}
				}
				if err := c.ops.ProbeSessionHealth(); err != nil {
					c.debugf("commissioning setup: suite control probe failed: %v, reconnecting", err)
					if err := c.ops.ReconnectToZone(state); err != nil {
						return err
					}
				}
			}
		} else {
			c.ops.EnsureDisconnected()
		}
		// Tests like TC-PAIR-001/002 want level 1 (uncommissioned) AND a
		// closed commissioning window so they can verify pairing-request
		// driven window opening from a clean state. WaitForCommissioningMode
		// would mDNS-cache the device's pre-close advertisement and the
		// later verify_mdns_not_advertising step would race against macOS's
		// mDNSResponder cache. Skip the wait when the test explicitly
		// asked for a closed window.
		windowClosedRequested := hasPrecondition(tc.Preconditions, PrecondCommissioningWindowClosed)
		if c.config.Target != "" && !windowClosedRequested {
			if err := c.ops.WaitForCommissioningMode(ctx, 3*time.Second); err != nil {
				c.debugf("commissioning mode: %v (continuing)", err)
			}
			c.ops.SetLastDeviceConnClose(time.Now())
		}
		// StateCommissioningActive reflects the runner's intent: false when
		// the test explicitly closed the window, true otherwise.
		state.Set(StateCommissioningActive, !windowClosedRequested)
	}

	// Crypto restore: if no fresh PASE occurred and crypto was replaced.
	if c.suite.ZoneID() == "" &&
		c.ops.PASEState() == savedPASE && savedPASE != nil && savedPASE.Completed() &&
		c.ops.WorkingCrypto().ZoneCA != savedCrypto.ZoneCA {
		c.ops.SetWorkingCrypto(savedCrypto)
		c.debugf("restored crypto state after session reuse for %s", tc.ID)
	}

	// F-32: failsafe_duration_short must run BEFORE control_state so that
	// when control_state: FAILSAFE fires, the countdown picks up the
	// short duration (TC-STATE-004 waits 6s for AUTONOMOUS).
	if c.config.Target != "" && c.config.EnableKey != "" && needed >= precondLevelCommissioned {
		if hasPrecondition(tc.Preconditions, PrecondFailsafeDurationShort) {
			trigCtx, trigCancel := context.WithTimeout(ctx, 3*time.Second)
			short := uint32(5) // 5 seconds
			trigger := features.TriggerSetFailsafeDurationBase | uint64(short)
			if err := c.ops.SendTriggerViaZone(trigCtx, trigger, state); err != nil {
				c.debugf("failsafe_duration_short: trigger failed: %v (continuing)", err)
			}
			trigCancel()
		}
	}

	// Send control/process state triggers on real devices.
	if c.config.Target != "" && c.config.EnableKey != "" && needed >= precondLevelCommissioned {
		if csVal := preconditionValue(tc.Preconditions, PrecondControlState); csVal != "" {
			var trigger uint64
			switch csVal {
			case ControlStateControlled:
				trigger = features.TriggerControlStateControlled
			case ControlStateFailsafe:
				trigger = features.TriggerControlStateFailsafe
			case ControlStateAutonomous:
				trigger = features.TriggerControlStateAutonomous
			case ControlStateLimited:
				trigger = features.TriggerControlStateLimited
			case ControlStateOverride:
				trigger = features.TriggerControlStateOverride
			}
			if trigger != 0 {
				trigCtx, trigCancel := context.WithTimeout(ctx, 3*time.Second)
				if err := c.ops.SendTriggerViaZone(trigCtx, trigger, state); err != nil {
					c.debugf("control_state trigger failed: %v (continuing)", err)
				}
				trigCancel()
			}
		}
		if psVal := preconditionValue(tc.Preconditions, PrecondProcessState); psVal != "" {
			var trigger uint64
			switch psVal {
			case ProcessStateRunning:
				trigger = features.TriggerProcessStateRunning
			case ProcessStatePaused:
				trigger = features.TriggerProcessStatePaused
			case ProcessStateAvailable:
				trigger = features.TriggerProcessStateAvailable
			case ProcessStateNone:
				trigger = features.TriggerProcessStateNone
			}
			if trigger != 0 {
				trigCtx, trigCancel := context.WithTimeout(ctx, 3*time.Second)
				if err := c.ops.SendTriggerViaZone(trigCtx, trigger, state); err != nil {
					c.debugf("process_state trigger failed: %v (continuing)", err)
				}
				trigCancel()
			}
		}
	}

	// Clear device-side limits when test requires no_existing_limits.
	if c.config.Target != "" && needed >= precondLevelCommissioned &&
		hasPrecondition(tc.Preconditions, PrecondNoExistingLimits) {
		clearCtx, clearCancel := context.WithTimeout(ctx, 3*time.Second)
		if err := c.ops.SendClearLimitInvoke(clearCtx); err != nil {
			c.debugf("no_existing_limits: ClearLimit failed: %v (continuing)", err)
		}
		clearCancel()
	}

	// Seed a calling-zone consumption limit when test requires limit_is_set
	// or ev_limit_applied (synonyms in the corpus, used by GPL/EVC tests
	// that exercise ClearLimit -> CONTROLLED). Runs after the LOCAL-zone
	// fresh commission triggered by testCaseNeedsControllerAuthority, so
	// pool.Main() is the calling zone whose myConsumptionLimit the test
	// will read.
	if c.config.Target != "" && needed >= precondLevelCommissioned &&
		(hasPrecondition(tc.Preconditions, PrecondLimitIsSet) ||
			hasPrecondition(tc.Preconditions, PrecondEVLimitApplied)) {
		setCtx, setCancel := context.WithTimeout(ctx, 3*time.Second)
		if err := c.ops.SendSetLimitInvoke(setCtx); err != nil {
			c.debugf("limit_is_set/ev_limit_applied: SetLimit failed: %v (continuing)", err)
		}
		setCancel()
	}

	// Flip isPausable=false on the device when test requires
	// device_not_pausable. The trigger sets the readOnly capability flag
	// directly so commands gated on it (Pause/Resume) surface as
	// wire.StatusUnsupported. TriggerResetTestState restores the EVSE/HP
	// default of true between tests.
	if c.config.Target != "" && c.config.EnableKey != "" &&
		needed >= precondLevelCommissioned &&
		hasPrecondition(tc.Preconditions, PrecondDeviceNotPausable) {
		trigCtx, trigCancel := context.WithTimeout(ctx, 3*time.Second)
		if err := c.ops.SendTriggerViaZone(trigCtx, features.TriggerSetIsPausableFalse, state); err != nil {
			c.debugf("device_not_pausable: SetIsPausableFalse trigger failed: %v (continuing)", err)
		}
		trigCancel()
	}

	// Plug in an EV when test requires ev_connected or ev_plugged_in
	// (synonyms in the corpus). TriggerEVPlugIn populates rich state
	// (evStateOfCharge, evBatteryCapacity, evDemandMode, evIdentifications,
	// energy requests, sessionStartTime) so attribute reads under these
	// preconditions return plausible values. TriggerResetTestState reverts
	// to NOT_PLUGGED_IN + null EV state.
	if c.config.Target != "" && c.config.EnableKey != "" &&
		needed >= precondLevelCommissioned &&
		(hasPrecondition(tc.Preconditions, PrecondEVConnected) ||
			hasPrecondition(tc.Preconditions, PrecondEVPluggedIn)) {
		trigCtx, trigCancel := context.WithTimeout(ctx, 3*time.Second)
		if err := c.ops.SendTriggerViaZone(trigCtx, features.TriggerEVPlugIn, state); err != nil {
			c.debugf("ev_connected/plugged_in: TriggerEVPlugIn failed: %v (continuing)", err)
		}
		trigCancel()
	}

	// Post-setup: session_previously_connected.
	if hasPrecondition(tc.Preconditions, PrecondSessionPreviouslyConnected) {
		c.debugf("session_previously_connected: disconnecting but preserving zone state")
		savedCr := c.ops.WorkingCrypto()
		if c.pool.Main() != nil {
			_ = c.pool.Main().Close()
		}
		detachMainControlChannel(c.pool, state)
		c.ops.SetPASEState(nil)
		c.ops.SetWorkingCrypto(savedCr)
	}

	return nil
}

func testCaseHasAction(tc *loader.TestCase, action string) bool {
	if tc == nil || action == "" {
		return false
	}
	for _, step := range tc.Steps {
		if step.Action == action {
			return true
		}
	}
	return false
}

func testCaseNeedsIsolatedCommissionedSession(tc *loader.TestCase) bool {
	if tc == nil {
		return false
	}
	for _, step := range tc.Steps {
		switch step.Action {
		case ActionConnect, ActionConnectAsController, ActionConnectAsZone, ActionConnectWithTiming, ActionConnectOperational, ActionConnectExpectFailure:
			return true
		}
	}
	return false
}

// controllerAuthorityCommands lists EnergyControl invokes that go through
// multi-zone resolution. Per DEC-060, TEST zones are excluded from limit
// and setpoint resolution, so a test that issues these commands against
// the auto-PICS TEST suite session sees no effect on `effective*` values
// and no controlState transition. Such tests need a real LOCAL zone.
//
// ProcessState commands (Pause, Resume, Stop, ScheduleProcess) are not
// listed because they are global, not multi-zone resolved -- a TEST zone
// can drive them just fine.
var controllerAuthorityCommands = map[string]bool{
	"SetLimit":         true,
	"ClearLimit":       true,
	"SetSetpoint":      true,
	"ClearSetpoint":    true,
	"SetCurrentLimits": true,
}

// testCaseNeedsControllerAuthority returns true when the test exercises
// commands or preconditions that require the test session to be a real
// (non-TEST) controller zone. Without this signal the auto-PICS suite zone
// (TEST, observer-only per DEC-060) is reused and the test's SetLimit-style
// commands are stored under TEST and silently dropped during resolution.
func testCaseNeedsControllerAuthority(tc *loader.TestCase) bool {
	if tc == nil {
		return false
	}
	// Explicit zone_type precondition asking for LOCAL or GRID.
	for _, cond := range tc.Preconditions {
		if v, ok := cond[KeyZoneType].(string); ok {
			switch strings.ToUpper(v) {
			case ZoneTypeLocal, ZoneTypeGrid:
				return true
			}
		}
		// Multi-zone preconditions imply the primary session participates in
		// resolution as a real controller; the partner zone is added on top.
		if v, ok := cond[PrecondSecondZoneConnected].(string); ok && v != "" {
			return true
		}
		if b, _ := cond[PrecondTwoZonesConnected].(bool); b {
			return true
		}
		if b, _ := cond[PrecondTwoZonesWithLimits].(bool); b {
			return true
		}
		if n, ok := cond[PrecondZoneCountAtLeast].(int); ok && n >= 2 {
			return true
		}
		if b, _ := cond[PrecondNoOtherZonesConnected].(bool); b {
			return true
		}
		// Seeding a calling-zone consumption limit requires a non-TEST zone:
		// SetLimit on a TEST zone is silently dropped during multi-zone
		// resolution (DEC-060), so the limit_is_set / ev_limit_applied
		// precondition only takes effect when pool.Main() is LOCAL/GRID.
		if b, _ := cond[PrecondLimitIsSet].(bool); b {
			return true
		}
		if b, _ := cond[PrecondEVLimitApplied].(bool); b {
			return true
		}
	}
	// Invokes that go through multi-zone resolution.
	for _, step := range tc.Steps {
		if step.Action != ActionInvoke {
			continue
		}
		cmd, _ := step.Params["command"].(string)
		if controllerAuthorityCommands[cmd] {
			return true
		}
	}
	return false
}

// TeardownTest is called after each test completes (pass or fail).
// It cleans up subscriptions, notifications, stale PASE state, and
// per-test security pool connections.
func (c *coordinatorImpl) TeardownTest(_ context.Context, tc *loader.TestCase, state *engine.ExecutionState) {
	start := time.Now()
	c.debugf("teardown:start %s", tc.ID)
	defer c.debugf("teardown:end %s duration=%s", tc.ID, time.Since(start).Round(time.Millisecond))

	suiteRemovedDuringTest := false
	if sz := c.suite.ZoneID(); sz != "" && wasZoneRemovedInTest(state, sz) {
		suiteRemovedDuringTest = true
		c.debugf("teardown: suite zone %s was removed during test, clearing suite state", sz)
		detachMainControlChannel(c.pool, state)
		c.suite.Clear()
		c.ops.SetPASEState(nil)
		c.ops.ClearWorkingCrypto()
	}

	// Unsubscribe all active subscriptions.
	c.debugf("teardown:phase=unsubscribe")
	c.pool.UnsubscribeAll(c.pool.Main())
	c.pool.ClearNotifications()
	if c.pool.Main() != nil {
		c.pool.Main().pendingNotifications = nil
	}
	for _, key := range c.pool.ZoneKeys() {
		if zc := c.pool.Zone(key); zc != nil {
			zc.pendingNotifications = nil
		}
	}
	// Suite zone conn lives outside the pool -- clear its buffer too.
	if sc := c.suite.Conn(); sc != nil {
		sc.pendingNotifications = nil
	}

	// Fresh-commission tests can remove the old suite and run against a newly
	// commissioned TEST zone. Preserve that live TEST session as the next suite
	// before cleanup disconnects/untracks it.
	// Remove per-test zones created by multi-zone commissioning steps.
	// Keep only the suite zone (if present) so strict cleanup invariants are
	// evaluated against a single authoritative control channel.
	c.debugf("teardown:phase=zone_cleanup")
	suiteZoneID := c.suite.ZoneID()
	suiteKey := ""
	if suiteZoneID != "" {
		suiteKey = c.suite.ConnKey()
	}
	removedZoneIDs := make(map[string]struct{})
	zoneRemovalFallbackConns := make(map[string]*Connection)
	for _, key := range c.pool.ZoneKeys() {
		if suiteKey != "" && key == suiteKey {
			continue
		}
		zc := c.pool.Zone(key)
		zoneID := c.pool.ZoneID(key)
		// If a tracked key aliases the live suite control connection, never use
		// it for per-test RemoveZone cleanup; only untrack the alias.
		if zc != nil && zc == c.suite.Conn() {
			c.pool.UntrackZone(key)
			continue
		}
		// The suite zone can appear under alias keys (for example "step-<id>")
		// while the authoritative suite key is "main-<id>". Never RemoveZone
		// the suite zone during per-test teardown; just untrack alias entries.
		if suiteZoneID != "" && zoneID == suiteZoneID {
			if zc != nil && zc != c.suite.Conn() {
				zc.transitionTo(ConnDisconnected)
			}
			c.pool.UntrackZone(key)
			continue
		}
		if zoneID != "" {
			removedZoneIDs[zoneID] = struct{}{}
			if zc != nil && zc.isConnected() && zc.framer != nil {
				zoneRemovalFallbackConns[zoneID] = zc
			}
		}
		if zc != nil {
			zc.transitionTo(ConnDisconnected)
		}
		c.pool.UntrackZone(key)
	}

	// Clean architecture invariant: RemoveZone is controller authority. Always
	// issue non-suite removals through the suite control channel when available.
	// Skip entirely when nothing needs removing -- reconnecting the suite just
	// to send zero commands is wasted work (and creates spurious failures in
	// tests that mock an "alive but framer-less" suite connection).
	if len(removedZoneIDs) > 0 || len(zoneRemovalFallbackConns) > 0 {
		sc := c.suite.Conn()
		if !suiteRemovedDuringTest && (sc == nil || !sc.isConnected() || sc.framer == nil) && c.suite.ZoneID() != "" {
			if err := c.ops.ReconnectToZone(state); err != nil {
				c.debugf("teardown: zone cleanup suite reconnect failed: %v", err)
			}
			sc = c.suite.Conn()
		}
		if sc != nil && sc.isConnected() && sc.framer != nil {
			for zoneID := range removedZoneIDs {
				c.ops.SendRemoveZoneOnConn(sc, zoneID)
			}
		} else {
			for zoneID, conn := range zoneRemovalFallbackConns {
				c.ops.SendRemoveZoneOnConn(conn, zoneID)
			}
		}
	}

	// Retry non-suite zone removals once based on device-reported zone IDs.
	// This is the authoritative source: tracked pool IDs can go stale when a
	// test step replaces pool.Main() (e.g., transitionToOperational swaps in
	// a fresh operational conn for a newly commissioned LOCAL zone, then the
	// alias filter on the original conn no longer fires for the test's own
	// zone in the primary loop above). Without the sweep, the device retains
	// the LOCAL zone and the next commission gets "zone type already exists"
	// (F-6: TC-E2E-001 cert leak).
	//
	// Previously gated on -strict-lifecycle, but that flag is for fail-on-leak
	// diagnostics; the corrective cleanup itself must run regardless. Skip
	// only when the test itself removed the suite -- there is no authoritative
	// control channel to reconnect to.
	if !suiteRemovedDuringTest && c.config != nil &&
		c.config.Target != "" && c.config.EnableKey != "" {
		sc := c.suite.Conn()
		if (sc == nil || !sc.isConnected() || sc.framer == nil) && c.suite.ZoneID() != "" {
			if err := c.ops.ReconnectToZone(state); err != nil {
				c.debugf("teardown: residual sweep reconnect failed: %v", err)
			}
			sc = c.suite.Conn()
		}
		if sc != nil && sc.isConnected() && sc.framer != nil {
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
			if snap := c.ops.RequestDeviceState(checkCtx, state); snap != nil {
				ids := nonSuiteZoneIDsFromSnapshot(snap, c.suite.ZoneID())
				// Fallback to tracked IDs if snapshot didn't include zone details.
				if len(ids) == 0 && len(removedZoneIDs) > 0 {
					for zoneID := range removedZoneIDs {
						ids = append(ids, zoneID)
					}
				}
				if len(ids) > 0 {
					c.debugf("teardown: residual non-suite zones=%d, retrying remove sweep", len(ids))
					for _, zoneID := range ids {
						c.ops.SendRemoveZoneOnConn(sc, zoneID)
					}
				}
			}
			checkCancel()
		}
	}

	// Close connections with incomplete PASE state.
	c.debugf("teardown:phase=pase_cleanup")
	if c.pool.Main() != nil && c.pool.Main().isConnected() && !c.ops.PASEState().Completed() {
		c.debugf("teardown: closing connection with incomplete PASE state")
		c.pool.Main().transitionTo(ConnDisconnected)
		c.ops.SetPASEState(nil)
	}

	// Clear stale PASE state from failed handshakes.
	if c.ops.PASEState() != nil && !c.ops.PASEState().Completed() {
		c.debugf("teardown: clearing incomplete PASE state")
		c.ops.SetPASEState(nil)
	}

	// Reset hadConnection for the next test.
	if c.pool.Main() != nil {
		c.pool.Main().hadConnection = false
	}

	// Clean up security pool connections.
	c.debugf("teardown:phase=security_pool_cleanup")
	if secState, ok := state.Custom["security"].(*securityState); ok && secState.pool != nil {
		secState.pool.mu.Lock()
		for _, conn := range secState.pool.connections {
			if conn.isConnected() {
				_ = conn.Close()
			}
		}
		secState.pool.connections = nil
		secState.pool.mu.Unlock()
	}

	// Probe suite zone connection health. Tests like TC-FRAME-005 send raw
	// bytes that corrupt the framing layer without transitioning the
	// Connection state to ConnDisconnected. The connection looks alive
	// (isConnected()=true) but is unusable. Detecting this here -- in
	// teardown, which has independent timeouts -- avoids burning the next
	// test's tight timeout on failed reconnect attempts.
	if !suiteRemovedDuringTest && c.config.Target != "" && c.suite.ZoneID() != "" {
		c.debugf("teardown:phase=suite_health_probe")
		// After non-L3 tests, pool.Main() may be dead/empty while
		// suite.Conn() is alive. Restore main to suite conn so
		// ProbeSessionHealth can use it.
		if m := c.pool.Main(); m == nil || !m.isConnected() {
			_ = borrowSuiteControlChannelIfAlive(c.pool, c.suite, state)
		}

		if err := c.ops.ProbeSessionHealth(); err != nil {
			c.debugf("teardown: suite zone health probe failed: %v, reconnecting", err)
			if reconErr := c.ops.ReconnectToZone(state); reconErr != nil {
				c.debugf("teardown: suite zone reconnect failed: %v (continuing)", reconErr)
				// Keep teardown deterministic: no stale main/suite alias after
				// failed reconnect.
				detachMainControlChannel(c.pool, state)
				c.markTeardownError(state, "suite_zone_reconnect", reconErr)
			}
		}
	}

	// Capture device state snapshot after teardown cleanup and verify baseline.
	// This avoids false diffs from transient per-test subscriptions/notifications
	// and gives teardown one chance to repair a broken suite connection first.
	if c.config.Target != "" && c.config.EnableKey != "" {
		c.debugf("teardown:phase=baseline_snapshot")
		afterCtx, afterCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if after := c.ops.RequestDeviceState(afterCtx, state); after != nil {
			state.Custom[engine.StateKeyDeviceStateAfter] = map[string]any(after)

			if beforeRaw, ok := state.Custom[engine.StateKeyDeviceStateBefore]; ok {
				if before, ok := beforeRaw.(map[string]any); ok {
					diffs := filterBaselineDiffs(diffSnapshots(DeviceStateSnapshot(before), DeviceStateSnapshot(after)))
					if len(diffs) > 0 {
						diffMaps := make([]map[string]any, len(diffs))
						for i, d := range diffs {
							diffMaps[i] = map[string]any{
								"key":    d.Key,
								"before": d.Before,
								"after":  d.After,
							}
						}
						state.Custom[engine.StateKeyDeviceStateDiffs] = diffMaps
						c.debugf("teardown: baseline diverged on %d fields, re-resetting", len(diffs))

						// Re-reset and verify baseline is restored, but only when a
						// suite zone control channel exists for trigger delivery.
						canReset := c.suite.ZoneID() != ""
						if !canReset {
							c.debugf("teardown: baseline reset skipped (no suite zone)")
						}
						if canReset {
							resetCtx, resetCancel := context.WithTimeout(context.Background(), 5*time.Second)
							if err := c.ops.SendTriggerViaZone(resetCtx, features.TriggerResetTestState, state); err != nil {
								c.markTeardownError(state, "baseline_reset_retry", err)
							} else {
								verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 3*time.Second)
								if recheck := c.ops.RequestDeviceState(verifyCtx, state); recheck != nil {
									recheckDiffs := filterBaselineDiffs(diffSnapshots(DeviceStateSnapshot(before), recheck))
									if len(recheckDiffs) > 0 {
										c.debugf("teardown: baseline STILL diverged after re-reset (%d fields)", len(recheckDiffs))
										c.markTeardownError(state, "baseline_still_diverged", errors.New("device state baseline still diverged after re-reset"))
									}
								}
								verifyCancel()
							}
							resetCancel()
						}
					}
				}
			}
		}
		afterCancel()
	}
}

func (c *coordinatorImpl) markTeardownError(state *engine.ExecutionState, step string, err error) {
	if err == nil || c.config == nil || !c.config.StrictLifecycle {
		return
	}
	te := &TeardownError{Step: step, Cause: err}
	if existing, ok := state.Custom[engine.StateKeyTeardownError]; ok {
		switch v := existing.(type) {
		case error:
			state.Custom[engine.StateKeyTeardownError] = errors.Join(v, te)
		case string:
			if v == "" {
				state.Custom[engine.StateKeyTeardownError] = te
			} else {
				state.Custom[engine.StateKeyTeardownError] = errors.Join(errors.New(v), te)
			}
		default:
			state.Custom[engine.StateKeyTeardownError] = te
		}
		return
	}
	state.Custom[engine.StateKeyTeardownError] = te
}

func nonSuiteZoneIDsFromSnapshot(snap DeviceStateSnapshot, suiteZoneID string) []string {
	rawZones, ok := snap["zones"]
	if !ok || rawZones == nil {
		return nil
	}
	list, ok := rawZones.([]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if id == "" || id == suiteZoneID {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func (c *coordinatorImpl) normalizeResidualNonSuiteZones(state *engine.ExecutionState) {
	if c.config == nil || !c.config.StrictLifecycle || c.config.Target == "" || c.config.EnableKey == "" {
		return
	}
	suiteZoneID := c.suite.ZoneID()
	if suiteZoneID == "" {
		return
	}

	sc := c.suite.Conn()
	if sc == nil || !sc.isConnected() || sc.framer == nil {
		if err := c.ops.ReconnectToZone(state); err != nil {
			c.debugf("setup: residual zone normalize reconnect failed: %v", err)
			return
		}
		sc = c.suite.Conn()
	}
	if sc == nil || !sc.isConnected() || sc.framer == nil {
		return
	}

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
	snap := c.ops.RequestDeviceState(checkCtx, state)
	checkCancel()
	if snap == nil {
		return
	}

	ids := nonSuiteZoneIDsFromSnapshot(snap, suiteZoneID)
	if len(ids) == 0 {
		return
	}
	c.debugf("setup: removing %d residual non-suite zones before baseline", len(ids))
	for _, zoneID := range ids {
		c.ops.SendRemoveZoneOnConn(sc, zoneID)
	}
}

// CurrentLevel returns the runner's current precondition level based on
// connection and commissioning state.
func (c *coordinatorImpl) CurrentLevel() int {
	if c.ops.PASEState().Completed() {
		return precondLevelCommissioned
	}
	if c.pool.Main() != nil && c.pool.Main().isConnected() {
		return precondLevelConnected
	}
	return precondLevelNone
}
