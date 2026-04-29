package runner

import (
	"context"
	"errors"
	"fmt"
	mathrand "math/rand"
	"sort"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

var (
	errRemoveZoneNoConnection = errors.New("remove zone: no live connection")
	errRemoveZoneNoPASE       = errors.New("remove zone: no completed PASE session")
	errRemoveZoneNoZoneID     = errors.New("remove zone: empty zone id")
	errRemoveZoneSend         = errors.New("remove zone: send failure")
	errRemoveZoneAckRead      = errors.New("remove zone: ack read failure")
	errRemoveZoneAlreadyGone  = errors.New("remove zone: target already absent")
)

const strictRemoveZoneAckTimeout = 500 * time.Millisecond
const removeZoneWriteTimeout = 200 * time.Millisecond

// Precondition levels form a hierarchy:
//
//	Level 0: No relevant preconditions     -> no-op
//	Level 1: device_in_commissioning_mode  -> ensure disconnected (clean state)
//	Level 2: tls/connection_established    -> connect
//	Level 3: session/device_commissioned   -> connect + commission (PASE)
const (
	precondLevelNone          = 0
	precondLevelCommissioning = 1
	precondLevelConnected     = 2
	precondLevelCommissioned  = 3
)

// preconditionKeyLevels maps known precondition keys to their required level.
// simulationPreconditionKeys are precondition keys that should be stored in
// execution state so that handlers can adapt their behavior based on the
// simulated scenario (D2D, environment, capacity, etc.).
var simulationPreconditionKeys = map[string]bool{
	// D2D simulation.
	PrecondTwoDevicesSameZone:          true,
	PrecondTwoDevicesDifferentZones:    true,
	PrecondDeviceBCertExpired:          true,
	PrecondTwoDevicesSameDiscriminator: true,
	// Commissioning window simulation.
	PrecondCommissioningWindowAt95s: true,
	// Environment / capacity simulation.
	PrecondDeviceZonesFull:              true,
	PrecondNoDevicesAdvertising:         true,
	PrecondDeviceSRVPresent:             true,
	PrecondDeviceAAAAMissing:            true,
	PrecondDeviceAddressValid:           true,
	PrecondDevicePortClosed:             true,
	PrecondDeviceWillAppearAfterDelay:   true,
	PrecondFiveZonesConnected:           true,
	PrecondTwoZonesConnected:            true,
	PrecondDeviceListening:              true,
	PrecondDeviceInZone:                 true,
	PrecondDeviceInTwoZones:             true,
	PrecondZoneCreated:                  true,
	PrecondMultipleDevicesCommissioning: true,
	PrecondMultipleDevicesCommissioned:  true,
	PrecondMultipleControllersRunning:   true,
	// Device state simulation.
	PrecondDeviceReset:                true,
	PrecondDeviceHasGridZone:          true,
	PrecondDeviceHasLocalZone:         true,
	PrecondDeviceInLocalZone:          true,
	PrecondSessionPreviouslyConnected: true,
	PrecondDeviceHasOneZone:           true,
	PrecondDeviceHasAvailableZoneSlot: true,
	// State-machine simulation.
	PrecondControlState:          true,
	PrecondInitialControlState:   true,
	PrecondProcessState:          true,
	PrecondProcessCapable:        true,
	PrecondDeviceIsPausable:      true,
	PrecondDeviceIsStoppable:     true,
	PrecondFailsafeDurationShort: true,
	// Zone limit/removal simulation.
	PrecondZoneCount:                true,
	PrecondZoneCountAtLeast:         true,
	PrecondNoOtherZonesConnected:    true,
	PrecondAcceptsSetpoints:         true,
	PrecondTwoZonesWithLimits:       true,
	PrecondSecondZoneConnected:      true,
	PrecondNoExistingLimits:         true,
	PrecondZoneHasSetValues:         true,
	PrecondDeviceSupportsProduction: true,
	PrecondDeviceIsBidirectional:    true,
	PrecondDeviceSupportsAsymmetric: true,
	// IPv6 / multi-interface simulation.
	PrecondDeviceHasGlobalAndLinkLocal: true,
	PrecondDeviceHasLinkLocal:          true,
	PrecondDeviceHasMultipleInterfaces: true,
}

var preconditionKeyLevels = map[string]int{
	// Level 0: Always-true environment preconditions (no setup needed).
	PrecondDeviceBooted:      precondLevelNone,
	PrecondControllerRunning: precondLevelNone,
	PrecondDeviceInNetwork:   precondLevelNone,
	PrecondDeviceListening:   precondLevelNone,

	// D2D simulation preconditions (no actual connection needed).
	PrecondTwoDevicesSameZone:          precondLevelNone,
	PrecondTwoDevicesDifferentZones:    precondLevelNone,
	PrecondDeviceBCertExpired:          precondLevelNone,
	PrecondTwoDevicesSameDiscriminator: precondLevelNone,

	// Controller preconditions (zone/cert state, no connection needed).
	PrecondZoneCreated:              precondLevelNone,
	PrecondControllerHasCert:        precondLevelNone,
	PrecondControllerCertNearExpiry: precondLevelNone,

	// Environment/negative-test preconditions (simulation, no connection needed).
	PrecondDeviceZonesFull:              precondLevelNone,
	PrecondNoDevicesAdvertising:         precondLevelNone,
	PrecondDeviceSRVPresent:             precondLevelNone,
	PrecondDeviceAAAAMissing:            precondLevelNone,
	PrecondDeviceAddressValid:           precondLevelNone,
	PrecondDevicePortClosed:             precondLevelNone,
	PrecondDeviceWillAppearAfterDelay:   precondLevelNone,
	PrecondFiveZonesConnected:           precondLevelNone,
	PrecondTwoZonesConnected:            precondLevelNone,
	PrecondDeviceInZone:                 precondLevelCommissioned,
	PrecondDeviceInTwoZones:             precondLevelNone,
	PrecondMultipleDevicesCommissioning: precondLevelNone,
	PrecondMultipleDevicesCommissioned:  precondLevelNone,
	PrecondMultipleControllersRunning:   precondLevelNone,
	// IPv6 / multi-interface simulation (no real connection needed).
	PrecondDeviceHasGlobalAndLinkLocal: precondLevelNone,
	PrecondDeviceHasLinkLocal:          precondLevelNone,
	PrecondDeviceHasMultipleInterfaces: precondLevelNone,

	// Zone management test preconditions (runner-side state, no connection needed).
	PrecondNoZonesConfigured:   precondLevelNone,
	PrecondLocalZoneConfigured: precondLevelNone,
	PrecondTwoZonesConfigured:  precondLevelNone,
	PrecondSubscriptionActive:  precondLevelNone,

	PrecondDeviceInCommissioningMode: precondLevelCommissioning,
	PrecondDeviceUncommissioned:      precondLevelCommissioning,
	PrecondCommissioningWindowOpen:   precondLevelCommissioning,
	PrecondCommissioningWindowClosed: precondLevelCommissioning,
	PrecondCommissioningWindowAt95s:  precondLevelCommissioning,
	PrecondDeviceConnected:           precondLevelConnected,
	PrecondTLSConnectionEstablished:  precondLevelConnected,
	PrecondConnectionEstablished:     precondLevelConnected,
	PrecondDeviceCommissioned:        precondLevelCommissioned,
	PrecondSessionEstablished:        precondLevelCommissioned,

	// Device state preconditions (require commissioned session for read/write).
	PrecondDeviceReset:                precondLevelCommissioned,
	PrecondDeviceHasGridZone:          precondLevelCommissioned,
	PrecondDeviceHasLocalZone:         precondLevelCommissioned,
	PrecondDeviceInLocalZone:          precondLevelCommissioned,
	PrecondSessionPreviouslyConnected: precondLevelCommissioned,
	PrecondFreshCommission:            precondLevelCommissioned,
	PrecondDeviceHasOneZone:           precondLevelCommissioned,
	PrecondDeviceHasAvailableZoneSlot: precondLevelCommissioned,

	// State-machine preconditions (require commissioned session).
	PrecondControlState:          precondLevelCommissioned,
	PrecondInitialControlState:   precondLevelCommissioned,
	PrecondProcessState:          precondLevelCommissioned,
	PrecondProcessCapable:        precondLevelCommissioned,
	PrecondDeviceIsPausable:      precondLevelCommissioned,
	PrecondDeviceIsStoppable:     precondLevelCommissioned,
	PrecondFailsafeDurationShort: precondLevelCommissioned,

	// Zone limit/removal test preconditions.
	PrecondZoneCount:                precondLevelCommissioned,
	PrecondZoneCountAtLeast:         precondLevelCommissioned,
	PrecondNoOtherZonesConnected:    precondLevelCommissioned,
	PrecondAcceptsSetpoints:         precondLevelCommissioned,
	PrecondTwoZonesWithLimits:       precondLevelCommissioned,
	PrecondSecondZoneConnected:      precondLevelCommissioned,
	PrecondNoExistingLimits:         precondLevelCommissioned,
	PrecondZoneHasSetValues:         precondLevelCommissioned,
	PrecondDeviceSupportsProduction: precondLevelCommissioned,
	PrecondDeviceIsBidirectional:    precondLevelCommissioned,
	PrecondDeviceSupportsAsymmetric: precondLevelCommissioned,
}

// preconditionLevelFor determines the highest setup level needed for the given conditions.
func preconditionLevelFor(conditions []loader.Condition) int {
	level := precondLevelNone
	for _, cond := range conditions {
		for key, val := range cond {
			// Skip conditions explicitly set to false.
			if b, ok := val.(bool); ok && !b {
				continue
			}
			// Accept boolean true and non-boolean values (e.g., string
			// enum values like control_state: FAILSAFE).
			if l, ok := preconditionKeyLevels[key]; ok && l > level {
				level = l
			}
		}
	}
	return level
}

// preconditionLevel determines the highest setup level needed for the given conditions.
func (r *Runner) preconditionLevel(conditions []loader.Condition) int {
	return preconditionLevelFor(conditions)
}

// hasPrecondition checks if any condition in the list has the given key set to true.
func hasPrecondition(conditions []loader.Condition, key string) bool {
	for _, cond := range conditions {
		if b, ok := cond[key].(bool); ok && b {
			return true
		}
	}
	return false
}

// hasPreconditionInt checks if a precondition has the given key with an int value >= minVal.
func hasPreconditionInt(conditions []loader.Condition, key string, minVal int) bool {
	for _, cond := range conditions {
		if v, ok := cond[key].(int); ok && v >= minVal {
			return true
		}
	}
	return false
}

// hasPreconditionString checks if a precondition has the given key with a non-empty string value.
func hasPreconditionString(conditions []loader.Condition, key string) bool {
	for _, cond := range conditions {
		if s, ok := cond[key].(string); ok && s != "" {
			return true
		}
	}
	return false
}

// preconditionValue returns the string value of a precondition key, or "" if not found.
func preconditionValue(conditions []loader.Condition, key string) string {
	for _, cond := range conditions {
		if s, ok := cond[key].(string); ok {
			return s
		}
	}
	return ""
}

// needsFreshCommission checks if any condition explicitly requests a fresh commissioning cycle.
func needsFreshCommission(conditions []loader.Condition) bool {
	return hasPrecondition(conditions, PrecondFreshCommission)
}

// SortByPreconditionLevel sorts test cases by their required precondition level
// (lowest first). The sort is stable, preserving file order within the same level.
func SortByPreconditionLevel(cases []*loader.TestCase) {
	sort.SliceStable(cases, func(i, j int) bool {
		return preconditionLevelFor(cases[i].Preconditions) <
			preconditionLevelFor(cases[j].Preconditions)
	})
}

// ShuffleWithinLevels randomizes the order of test cases within each
// precondition level group. Cases must already be sorted by level.
// The seed is used for reproducibility.
func ShuffleWithinLevels(cases []*loader.TestCase, seed int64) {
	rng := mathrand.New(mathrand.NewSource(seed))
	i := 0
	for i < len(cases) {
		level := preconditionLevelFor(cases[i].Preconditions)
		j := i + 1
		for j < len(cases) && preconditionLevelFor(cases[j].Preconditions) == level {
			j++
		}
		// Shuffle cases[i:j]
		group := cases[i:j]
		rng.Shuffle(len(group), func(a, b int) {
			group[a], group[b] = group[b], group[a]
		})
		i = j
	}
}

// currentLevel returns the runner's current precondition level based on connection
// and commissioning state.
func (r *Runner) currentLevel() int {
	return r.coordinator.CurrentLevel()
}

// teardownTest is the callback registered with the engine.
// It delegates to the coordinator for test cleanup and stops the mDNS observer
// so the next test starts with a fresh observer.
func (r *Runner) teardownTest(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState) {
	r.clearStrictLifecycleErr()
	r.stopObserver()
	if r.pairingAdvertiser != nil {
		r.pairingAdvertiser.StopAll()
		r.pairingAdvertiser = nil
	}
	r.coordinator.TeardownTest(ctx, tc, state)
	r.recoverDeadSuiteControlChannel(state)

	// Re-commission the suite zone if it still doesn't exist (e.g. remove_device
	// with zone=all truly removed all zones). Without this, all subsequent tests
	// lose the control channel for reset triggers, causing cascading failures.
	if r.config.Target != "" && r.config.EnableKey != "" && r.suite.ZoneID() == "" {
		r.debugf("teardown: suite zone destroyed, re-commissioning")
		// Wait for the device to enter commissioning mode after zone removal.
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := r.waitForCommissioningMode(waitCtx, 3*time.Second); err != nil {
			r.debugf("teardown: wait for commissioning mode: %v (continuing)", err)
		}
		waitCancel()

		reCtx, reCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := r.commissionSuiteZone(reCtx); err != nil {
			r.debugf("teardown: suite zone re-commission failed: %v", err)
		}
		reCancel()
	}

	report := r.BuildCleanupReport()
	state.Custom[engine.StateKeyCleanupReport] = report.ToMap()
	if r.config != nil && r.config.StrictLifecycle && r.strictLifecycleErr != nil {
		appendTeardownError(state, &TeardownError{
			Step:  "strict_lifecycle",
			Cause: r.strictLifecycleErr,
		})
	}
	if !report.IsClean() {
		r.debugf("teardown: cleanup invariants failed: %s", report.Summary())
		if r.config != nil && r.config.StrictLifecycle {
			appendTeardownError(state, &TeardownError{
				Step:  "cleanup_invariants",
				Cause: errors.New(report.Summary()),
			})
		}
	}
	if r.config != nil && r.config.StrictLifecycle {
		contract := r.runStrictCleanupContract(ctx, state)
		state.Custom[stateKeyStrictCleanupContract] = contract.ToMap()
		if !contract.IsClean() {
			r.debugf("teardown: strict cleanup contract failed: %s", contract.Summary())
			appendTeardownError(state, &TeardownError{
				Step:  "strict_cleanup_contract",
				Cause: errors.New(contract.Summary()),
			})
		}
	}
}

// recoverDeadSuiteControlChannel ensures that teardown does not carry a
// poisoned suite state into the next test. If a suite zone exists but its
// control connection is dead and reconnect fails, clear suite state so the
// normal re-commission path can rebuild a fresh control channel.
func (r *Runner) recoverDeadSuiteControlChannel(state *engine.ExecutionState) {
	if r.config == nil || r.config.Target == "" || r.config.EnableKey == "" {
		return
	}
	if r.suite.ZoneID() == "" {
		return
	}
	if sc := r.suite.Conn(); sc != nil && sc.isConnected() && sc.framer != nil {
		return
	}
	if err := r.reconnectToZone(state); err != nil {
		r.debugf("teardown: suite control channel unrecoverable (%v), clearing suite state", err)
		r.ensureDisconnected()
	}
}

// adoptMainAsSuiteIfPossible promotes an existing commissioned TEST session on
// pool.Main() into suite session state. Returns true when adoption succeeds.
func (r *Runner) adoptMainAsSuiteIfPossible() bool {
	if r.suite.ZoneID() != "" {
		return false
	}
	if r.connMgr.CommissionZoneType() != cert.ZoneTypeTest {
		return false
	}
	ps := r.connMgr.PASEState()
	if ps == nil || !ps.completed || len(ps.sessionKey) == 0 {
		return false
	}
	main := r.pool.Main()
	if main == nil || !main.isConnected() {
		return false
	}
	r.recordSuiteZone()
	return r.suite.ZoneID() != ""
}

func appendTeardownError(state *engine.ExecutionState, err error) {
	if state == nil || err == nil {
		return
	}
	if existing, ok := state.Custom[engine.StateKeyTeardownError]; ok {
		switch v := existing.(type) {
		case error:
			state.Custom[engine.StateKeyTeardownError] = errors.Join(v, err)
		case string:
			if v == "" {
				state.Custom[engine.StateKeyTeardownError] = err
			} else {
				state.Custom[engine.StateKeyTeardownError] = errors.Join(errors.New(v), err)
			}
		default:
			state.Custom[engine.StateKeyTeardownError] = err
		}
		return
	}
	state.Custom[engine.StateKeyTeardownError] = err
}

// setupPreconditions is the callback registered with the engine.
// It delegates to the coordinator for all lifecycle orchestration.
func (r *Runner) setupPreconditions(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState) error {
	r.clearStrictLifecycleErr()
	if err := r.coordinator.SetupPreconditions(ctx, tc, state); err != nil {
		return err
	}
	if r.config != nil && r.config.StrictLifecycle && r.strictLifecycleErr != nil {
		return r.strictLifecycleErr
	}
	return nil
}

// requireCommissioningAvailable enforces that the device is currently
// advertising a commissionable service before starting a commissioning step.
// This avoids racing TLS connects against transient mode transitions.
func (r *Runner) requireCommissioningAvailable(ctx context.Context, phase string) error {
	if err := r.waitForCommissioningAvailable(ctx, 3*time.Second); err != nil {
		return fmt.Errorf("%s: %w", phase, err)
	}
	return nil
}

// sendClearLimitInvoke sends a ClearLimit invoke (direction=nil, i.e. clear both)
// to endpoint 1 / EnergyControl on the device. Used by the no_existing_limits
// precondition to ensure the device has no stale limits from prior tests.
func (r *Runner) sendClearLimitInvoke(_ context.Context) error {
	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return fmt.Errorf("no connection for ClearLimit")
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID: features.EnergyControlCmdClearLimit,
		},
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return fmt.Errorf("encode ClearLimit: %w", err)
	}

	_, err = r.sendRequest(data, "clearLimit", req.MessageID)
	return err
}

// ensureConnected checks if already on a commissioning TLS connection; if
// not, establishes one.
//
// This is the level-2 (`tls_connection_established`) precondition path. The
// "right" connection is specifically a commissioning TLS conn —
// `ConnTLSConnected`. An *operational* pool.Main (e.g. the suite-zone
// control channel left attached after a level-3 → level-2 downgrade, or
// after a teardown that borrowed it for triggers) IS `isConnected()` but
// is the wrong shape: tests that follow this precondition expect to drive
// PASE / `CommissioningError` envelopes over the `mash-comm/1` ALPN.
// Sending those over `mash/1` is a category mismatch — the device's
// operational read loop rejects the bytes as malformed CBOR and stays in
// the operational session, so subsequent `verify_commissioning_state`
// checks see `ConnOperational` and report COMMISSIONED instead of the
// ADVERTISING the failure path implies. (Surfaced live by TC-PASE-003
// when it runs after TC-PASE-002.)
//
// Callers that legitimately want to reuse an operational pool.Main (the
// already-commissioned short-circuit in `ensureCommissioned`) must
// handle that case BEFORE delegating here — this function will detach
// it.
func (r *Runner) ensureConnected(ctx context.Context, state *engine.ExecutionState) error {
	if r.pool.Main() != nil && r.pool.Main().state == ConnTLSConnected {
		r.pool.Main().hadConnection = true
		return nil
	}
	if r.pool.Main() != nil && r.pool.Main().state == ConnOperational {
		r.debugf("ensureConnected: pool.Main is operational; detaching for fresh commissioning conn")
		detachMainControlChannel(r.pool, state)
	}

	maxAttempts := 1
	if r.config != nil && r.config.Target != "" {
		maxAttempts = 3
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Create a synthetic step to drive handleConnect.
		step := &loader.Step{
			Action: "connect",
			Params: map[string]any{
				KeyCommissioning: true,
			},
		}

		outputs, err := r.handleConnect(ctx, step, state)
		if err != nil {
			if attempt < maxAttempts && isTransientError(err) {
				r.debugf("ensureConnected: transient connect error on attempt %d/%d: %v", attempt, maxAttempts, err)
				_ = contextSleep(ctx, time.Duration(attempt)*200*time.Millisecond)
				continue
			}
			return fmt.Errorf("precondition connect failed: %w", err)
		}

		// handleConnect returns connection_established in outputs even on TLS failure.
		if established, ok := outputs[KeyConnectionEstablished].(bool); ok && !established {
			errCode, _ := outputs[KeyError].(string)
			errDetail, _ := outputs[KeyErrorDetail].(string)
			if attempt < maxAttempts && shouldRetryConnectFailure(errCode, errDetail) {
				r.debugf("ensureConnected: retryable connect failure on attempt %d/%d: code=%s detail=%s", attempt, maxAttempts, errCode, errDetail)
				_ = contextSleep(ctx, time.Duration(attempt)*200*time.Millisecond)
				continue
			}
			return formatPreconditionConnectFailure(errCode, errDetail)
		}
		if r.strictLifecycleEnabled() {
			// setupPreconditions may detach pool.Main while the lifecycle
			// controller still tracks an operational authority. Align before
			// entering a fresh commissioning connection.
			if r.lifecycleController().State() != LifecycleDisconnected {
				r.lifecycleController().ToDisconnected()
			}
			if err := r.lifecycleController().ToCommissioning("main"); err != nil {
				return err
			}
		}
		return nil
	}

	return fmt.Errorf("precondition connect failed: %s", ErrCodeConnectionError)
}

func shouldRetryConnectFailure(errCode, errDetail string) bool {
	if strings.Contains(strings.ToLower(errDetail), "simulated") {
		return false
	}
	switch errCode {
	case ErrCodeConnectionError, ErrCodeConnectionFailed, ErrCodeTimeout:
		return true
	default:
		return false
	}
}

func formatPreconditionConnectFailure(errCode, errDetail string) error {
	if errCode == "" {
		errCode = ErrCodeConnectionError
	}
	if errDetail == "" || errDetail == errCode {
		return fmt.Errorf("precondition connect failed: %s", errCode)
	}
	return fmt.Errorf("precondition connect failed: %s (%s)", errCode, errDetail)
}

// closeActiveZoneConns closes runner-tracked zone connections from previous
// tests. The suite zone lives on suite.Conn() (outside the pool), so it is
// not affected. Use closeAllZoneConns for full cleanup (suite teardown).
func (r *Runner) closeActiveZoneConns() {
	r.closeActiveZoneConnsExcept("")
}

// closeAllZoneConns closes ALL zone connections including the suite zone.
// Used during suite teardown and fresh_commission precondition.
func (r *Runner) closeAllZoneConns() {
	r.closeActiveZoneConnsExcept("")
}

// closeActiveZoneConnsExcept closes all runner-tracked zone connections
// except the one matching exceptKey.
func (r *Runner) closeActiveZoneConnsExcept(exceptKey string) {
	closedAny := false
	for _, key := range r.pool.ZoneKeys() {
		if exceptKey != "" && key == exceptKey {
			r.debugf("closeActiveZoneConns: keeping suite zone %s", key)
			continue
		}
		conn := r.pool.Zone(key)
		if conn == nil {
			r.pool.UntrackZone(key)
			continue
		}
		r.debugf("closeActiveZoneConns: zone %s (state=%v tls=%v raw=%v)",
			key, conn.state, conn.tlsConn != nil, conn.conn != nil)
		// Send explicit RemoveZone before closing so the device can
		// synchronously process the zone removal and re-enter commissioning
		// mode quickly.
		if conn.framer != nil {
			zoneID := r.pool.ZoneID(key)
			if zoneID != "" {
				r.debugf("closeActiveZoneConns: sending RemoveZone for zone %s (zoneID=%s, state=%v)", key, zoneID, conn.state)
				r.sendRemoveZoneOnConn(conn, zoneID)
			}
		}
		if conn.tlsConn != nil || conn.conn != nil {
			closedAny = true
		}
		// Send ControlClose before TCP close.
		if conn.framer != nil {
			closeMsg := &wire.ControlMessage{Type: wire.ControlClose}
			if closeData, err := wire.EncodeControlMessage(closeMsg); err == nil {
				_ = conn.framer.WriteFrame(closeData)
			}
		}
		_ = conn.Close()
		conn.clearConnectionRefs()
		r.pool.UntrackZone(key)
	}
	if closedAny {
		r.connMgr.SetPASEState(nil)
		r.connMgr.SetLastDeviceConnClose(time.Now())
	}
}

// disconnectConnection closes the TCP connection but preserves crypto material
// (zoneCA, controllerCert, zoneCAPool) and suite zone identity. Used for L3->L1
// transitions where the connection will be re-established later.
func (r *Runner) disconnectConnection() {
	if r.pool.Main() != nil {
		if r.pool.Main().isConnected() || r.pool.Main().tlsConn != nil || r.pool.Main().conn != nil {
			r.debugf("disconnectConnection: closing (state=%v tls=%v raw=%v)",
				r.pool.Main().state, r.pool.Main().tlsConn != nil, r.pool.Main().conn != nil)
			// Send ControlClose before TCP close.
			if r.pool.Main().framer != nil {
				closeMsg := &wire.ControlMessage{Type: wire.ControlClose}
				if closeData, err := wire.EncodeControlMessage(closeMsg); err == nil {
					_ = r.pool.Main().framer.WriteFrame(closeData)
				}
			}
		}
		_ = r.pool.Main().Close()
		// Nil pointers after close. This is the full-cleanup path --
		// no goroutines should be referencing this connection's framer
		// because disconnectConnection is called between tests, not
		// mid-handler.
		r.pool.Main().clearConnectionRefs()
	}
	r.connMgr.SetPASEState(nil)
	if r.strictLifecycleEnabled() {
		r.lifecycleController().ToDisconnected()
	}
}

// ensureDisconnected closes the connection AND clears all crypto material.
// Used when abandoning the suite zone entirely (suite teardown, fresh_commission).
// Clears both current AND suite crypto to prevent stale suite crypto from being
// restored by ensureCommissioned's session-reuse path. Without clearing suite
// crypto, a subsequent fresh commission creates new crypto but suiteZoneCAPool
// still points to the old CA, causing "unknown_ca" TLS failures when the
// session-reuse path restores the stale suite crypto.
func (r *Runner) ensureDisconnected() {
	r.disconnectConnection()
	r.connMgr.ClearAllCrypto()
	// suite.Clear() closes the suite zone connection and nils all suite state.
	r.suite.Clear()
}

// sendRemoveZone sends a RemoveZone invoke to the device so it re-enters
// commissioning mode (DEC-059). Errors are ignored because the device may
// have already closed the connection.
func (r *Runner) sendRemoveZone() {
	main := r.pool.Main()
	if main == nil || !main.isConnected() || main.framer == nil {
		return
	}
	ps := r.connMgr.PASEState()
	if ps == nil || !ps.completed || ps.sessionKey == nil {
		return
	}

	// Derive zone ID from shared secret (same derivation as device).
	zoneID := deriveZoneIDFromSecret(ps.sessionKey)

	// Build RemoveZone invoke: endpoint 0, DeviceInfo feature (1), command 0x10.
	_, data, err := r.buildRemoveZoneRequest(zoneID)
	if err != nil {
		return
	}

	// Best-effort: send and read response with a short deadline to avoid
	// blocking forever when the device closes the connection (e.g., last
	// zone removed triggers commissioning mode).
	if main.tlsConn != nil {
		_ = main.tlsConn.SetWriteDeadline(time.Now().Add(removeZoneWriteTimeout))
		defer main.tlsConn.SetWriteDeadline(time.Time{})
	} else if main.conn != nil {
		_ = main.conn.SetWriteDeadline(time.Now().Add(removeZoneWriteTimeout))
		defer main.conn.SetWriteDeadline(time.Time{})
	}
	if err := main.framer.WriteFrame(data); err != nil {
		return
	}
	if main.tlsConn != nil {
		main.tlsConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	}
	_, _ = main.framer.ReadFrame()
	// Reset deadline for subsequent operations.
	if main.tlsConn != nil {
		main.tlsConn.SetReadDeadline(time.Time{})
	}
}

// sendRemoveZoneOnConn sends a RemoveZone invoke on a specific zone connection.
// Used by closeActiveZoneConns to explicitly remove zones before closing TCP
// connections, giving the device a synchronous signal instead of relying on
// async disconnect detection. A short read deadline prevents blocking when
// the device enters commissioning mode before responding (e.g., last zone).
func (r *Runner) sendRemoveZoneOnConn(conn *Connection, zoneID string) {
	if conn == nil || !conn.isConnected() || conn.framer == nil {
		return
	}
	_, data, err := r.buildRemoveZoneRequest(zoneID)
	if err != nil {
		return
	}

	if conn.tlsConn != nil {
		_ = conn.tlsConn.SetWriteDeadline(time.Now().Add(removeZoneWriteTimeout))
		defer conn.tlsConn.SetWriteDeadline(time.Time{})
	} else if conn.conn != nil {
		_ = conn.conn.SetWriteDeadline(time.Now().Add(removeZoneWriteTimeout))
		defer conn.conn.SetWriteDeadline(time.Time{})
	}
	if err := conn.framer.WriteFrame(data); err != nil {
		return
	}

	// Read response with short deadline. The response arrives in <2ms when
	// it's going to arrive at all. When the last zone is removed, the device
	// enters commissioning mode and may close the connection before
	// responding, so this deadline avoids a long unnecessary wait.
	if conn.tlsConn != nil {
		conn.tlsConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	}
	_, _ = conn.framer.ReadFrame()
}

func (r *Runner) buildRemoveZoneRequest(zoneID string) (*wire.Request, []byte, error) {
	// Wire spec uses integer-keyed parameters (docs/interaction-model.md
	// §7.1); a string key here is silently rejected by the device with
	// StatusInvalidParameter (the reason TC-ZONE-REMOVE-001 / TC-ZTYPE-005
	// were failing pre-fix).
	req := &wire.Request{
		MessageID:  r.pool.NextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload: &wire.InvokePayload{
			CommandID: features.DeviceInfoCmdRemoveZone,
			Parameters: map[uint8]any{
				features.RemoveZoneParamZoneID: zoneID,
			},
		},
	}
	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, nil, err
	}
	return req, data, nil
}

// sendRemoveZoneStrict sends RemoveZone and returns errors instead of silently
// continuing. Used in strict lifecycle mode where cleanup must be deterministic.
func (r *Runner) sendRemoveZoneStrict() error {
	main := r.pool.Main()
	if main == nil || !main.isConnected() || main.framer == nil {
		return errRemoveZoneNoConnection
	}
	ps := r.connMgr.PASEState()
	if ps == nil || !ps.completed || ps.sessionKey == nil {
		return errRemoveZoneNoPASE
	}
	zoneID := deriveZoneIDFromSecret(ps.sessionKey)
	if zoneID == "" {
		return errRemoveZoneNoZoneID
	}
	return r.sendRemoveZoneOnConnStrict(main, zoneID)
}

// sendRemoveZoneOnConnStrict is the strict variant of sendRemoveZoneOnConn.
func (r *Runner) sendRemoveZoneOnConnStrict(conn *Connection, zoneID string) error {
	if conn == nil || !conn.isConnected() || conn.framer == nil {
		return errRemoveZoneNoConnection
	}
	if zoneID == "" {
		return errRemoveZoneNoZoneID
	}
	req, data, err := r.buildRemoveZoneRequest(zoneID)
	if err != nil {
		return fmt.Errorf("remove zone encode: %w", err)
	}

	if conn.tlsConn != nil {
		_ = conn.tlsConn.SetWriteDeadline(time.Now().Add(strictRemoveZoneAckTimeout))
		defer conn.tlsConn.SetWriteDeadline(time.Time{})
	} else if conn.conn != nil {
		_ = conn.conn.SetWriteDeadline(time.Now().Add(strictRemoveZoneAckTimeout))
		defer conn.conn.SetWriteDeadline(time.Time{})
	}
	if err := conn.framer.WriteFrame(data); err != nil {
		return fmt.Errorf("%w: %v", errRemoveZoneSend, err)
	}

	if conn.tlsConn != nil {
		_ = conn.tlsConn.SetReadDeadline(time.Now().Add(strictRemoveZoneAckTimeout))
		defer conn.tlsConn.SetReadDeadline(time.Time{})
	} else if conn.conn != nil {
		_ = conn.conn.SetReadDeadline(time.Now().Add(strictRemoveZoneAckTimeout))
		defer conn.conn.SetReadDeadline(time.Time{})
	}

	respData, err := conn.framer.ReadFrame()
	if err != nil {
		return fmt.Errorf("%w: %v", errRemoveZoneAckRead, err)
	}
	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		return fmt.Errorf("remove zone ack decode: %w", err)
	}
	if resp.MessageID != req.MessageID {
		return fmt.Errorf("remove zone ack message mismatch: got %d want %d", resp.MessageID, req.MessageID)
	}
	if resp.Status != wire.StatusSuccess {
		if resp.Status == wire.StatusInvalidCommand {
			if msg := removeZoneErrorMessage(resp.Payload); strings.Contains(strings.ToLower(msg), "device not found") {
				return fmt.Errorf("%w: %s", errRemoveZoneAlreadyGone, msg)
			}
		}
		return fmt.Errorf("remove zone ack status: %d", resp.Status)
	}

	return nil
}

func removeZoneErrorMessage(payload any) string {
	switch p := payload.(type) {
	case *wire.ErrorPayload:
		return p.Message
	case wire.ErrorPayload:
		return p.Message
	case map[string]any:
		if msg, ok := p["message"].(string); ok {
			return msg
		}
		if msg, ok := p["1"].(string); ok {
			return msg
		}
	case map[any]any:
		if msg, ok := p[uint64(1)].(string); ok {
			return msg
		}
		if msg, ok := p[1].(string); ok {
			return msg
		}
	}
	return ""
}

// hasZoneOfType checks if a zone of the given type exists in zone state.
func hasZoneOfType(zs *zoneState, zoneType string) bool {
	for _, z := range zs.zones {
		if z.ZoneType == zoneType {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// CommissioningOps interface implementation
// ---------------------------------------------------------------------------
// These exported wrappers allow the Coordinator to call back into Runner
// without knowing about Runner's internals. Each method delegates to the
// corresponding private method or field.

// EnsureConnected wraps ensureConnected.
func (r *Runner) EnsureConnected(ctx context.Context, state *engine.ExecutionState) error {
	return r.ensureConnected(ctx, state)
}

// EnsureCommissioned wraps ensureCommissioned.
func (r *Runner) EnsureCommissioned(ctx context.Context, state *engine.ExecutionState) error {
	return r.ensureCommissioned(ctx, state)
}

// DisconnectConnection wraps disconnectConnection.
func (r *Runner) DisconnectConnection() {
	r.disconnectConnection()
}

// EnsureDisconnected wraps ensureDisconnected.
func (r *Runner) EnsureDisconnected() {
	r.ensureDisconnected()
}

// AdoptMainAsSuiteIfPossible wraps adoptMainAsSuiteIfPossible.
func (r *Runner) AdoptMainAsSuiteIfPossible() bool {
	return r.adoptMainAsSuiteIfPossible()
}

// ReconnectToZone wraps reconnectToZone.
func (r *Runner) ReconnectToZone(state *engine.ExecutionState) error {
	return r.reconnectToZone(state)
}

// ProbeSessionHealth wraps probeSessionHealth.
func (r *Runner) ProbeSessionHealth() error {
	return r.probeSessionHealth()
}

// WaitForCommissioningMode wraps waitForCommissioningMode.
func (r *Runner) WaitForCommissioningMode(ctx context.Context, timeout time.Duration) error {
	return r.waitForCommissioningMode(ctx, timeout)
}

// SendRemoveZone wraps sendRemoveZone.
func (r *Runner) SendRemoveZone() {
	if r.config != nil && r.config.StrictLifecycle {
		if err := r.sendRemoveZoneStrict(); err != nil {
			r.setStrictLifecycleErr(err)
		}
		return
	}
	r.sendRemoveZone()
}

// SendRemoveZoneOnConn wraps sendRemoveZoneOnConn.
func (r *Runner) SendRemoveZoneOnConn(conn *Connection, zoneID string) {
	if r.config != nil && r.config.StrictLifecycle {
		if err := r.sendRemoveZoneOnConnStrict(conn, zoneID); err != nil {
			if isBestEffortRemoveZoneOnConnError(err) {
				r.debugf("remove zone on conn best-effort cleanup failed: %v (continuing)", err)
				return
			}
			r.setStrictLifecycleErr(err)
		}
		return
	}
	r.sendRemoveZoneOnConn(conn, zoneID)
}

func isBestEffortRemoveZoneOnConnError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errRemoveZoneNoConnection) ||
		errors.Is(err, errRemoveZoneSend) ||
		errors.Is(err, errRemoveZoneAckRead) ||
		errors.Is(err, errRemoveZoneAlreadyGone)
}

// SendTriggerViaZone wraps sendTriggerViaZone.
func (r *Runner) SendTriggerViaZone(ctx context.Context, trigger uint64, state *engine.ExecutionState) error {
	return r.sendTriggerViaZone(ctx, trigger, state)
}

// SendClearLimitInvoke wraps sendClearLimitInvoke.
func (r *Runner) SendClearLimitInvoke(ctx context.Context) error {
	return r.sendClearLimitInvoke(ctx)
}

// PASEState returns the current PASE state (delegates to connMgr).
func (r *Runner) PASEState() *PASEState {
	return r.connMgr.PASEState()
}

// SetPASEState sets the current PASE state (delegates to connMgr).
func (r *Runner) SetPASEState(ps *PASEState) {
	r.connMgr.SetPASEState(ps)
}

// DeviceStateModified returns whether device state has been modified (delegates to connMgr).
func (r *Runner) DeviceStateModified() bool {
	return r.connMgr.DeviceStateModified()
}

// SetDeviceStateModified sets the device state modified flag (delegates to connMgr).
func (r *Runner) SetDeviceStateModified(modified bool) {
	r.connMgr.SetDeviceStateModified(modified)
}

// WorkingCrypto returns the current working crypto material (delegates to connMgr).
func (r *Runner) WorkingCrypto() CryptoState {
	return r.connMgr.WorkingCrypto()
}

// SetWorkingCrypto replaces the working crypto material (delegates to connMgr).
func (r *Runner) SetWorkingCrypto(crypto CryptoState) {
	r.connMgr.SetWorkingCrypto(crypto)
}

// ClearWorkingCrypto nils all working crypto fields (delegates to connMgr).
func (r *Runner) ClearWorkingCrypto() {
	r.connMgr.ClearWorkingCrypto()
}

// CommissionZoneType returns the current commission zone type (delegates to connMgr).
func (r *Runner) CommissionZoneType() cert.ZoneType {
	return r.connMgr.CommissionZoneType()
}

// SetCommissionZoneType sets the commission zone type (delegates to connMgr).
func (r *Runner) SetCommissionZoneType(zt cert.ZoneType) {
	r.connMgr.SetCommissionZoneType(zt)
}

// DiscoveredDiscriminator returns the mDNS-discovered discriminator (delegates to connMgr).
func (r *Runner) DiscoveredDiscriminator() uint16 {
	return r.connMgr.DiscoveredDiscriminator()
}

// LastDeviceConnClose returns when zone connections were last closed (delegates to connMgr).
func (r *Runner) LastDeviceConnClose() time.Time {
	return r.connMgr.LastDeviceConnClose()
}

// SetLastDeviceConnClose sets the last device connection close time (delegates to connMgr).
func (r *Runner) SetLastDeviceConnClose(t time.Time) {
	r.connMgr.SetLastDeviceConnClose(t)
}

// IsSuiteZoneCommission wraps isSuiteZoneCommission.
func (r *Runner) IsSuiteZoneCommission() bool {
	return r.isSuiteZoneCommission()
}

// StoreZoneCrypto delegates to connMgr.
func (r *Runner) StoreZoneCrypto(zoneID string) { r.connMgr.StoreZoneCrypto(zoneID) }

// LoadZoneCrypto delegates to connMgr.
func (r *Runner) LoadZoneCrypto(zoneID string) bool { return r.connMgr.LoadZoneCrypto(zoneID) }

// HasZoneCrypto delegates to connMgr.
func (r *Runner) HasZoneCrypto(zoneID string) bool { return r.connMgr.HasZoneCrypto(zoneID) }

// RemoveZoneCrypto delegates to connMgr.
func (r *Runner) RemoveZoneCrypto(zoneID string) { r.connMgr.RemoveZoneCrypto(zoneID) }

// ClearAllCrypto delegates to connMgr.
func (r *Runner) ClearAllCrypto() { r.connMgr.ClearAllCrypto() }

// RequestDeviceState wraps requestDeviceState.
func (r *Runner) RequestDeviceState(ctx context.Context, state *engine.ExecutionState) DeviceStateSnapshot {
	return r.requestDeviceState(ctx, state)
}

// DebugSnapshot wraps debugSnapshot.
func (r *Runner) DebugSnapshot(label string) {
	r.debugSnapshot(label)
}

// HandlePreconditionCases processes the boolean precondition switch that sets
// up zone state, crypto, and multi-zone commissioning. Extracted from
// setupPreconditions so the coordinator can delegate it back to Runner.
func (r *Runner) HandlePreconditionCases(ctx context.Context, tc *loader.TestCase, state *engine.ExecutionState,
	preconds []loader.Condition, needsMultiZone *bool) error {
	for _, cond := range preconds {
		for key, val := range cond {
			b, ok := val.(bool)
			if !ok || !b {
				continue
			}
			switch key {
			case PrecondZoneCreated, PrecondControllerHasCert:
				// Create a default zone (generates Zone CA + controller cert).
				if r.connMgr.ZoneCA() == nil {
					step := &loader.Step{Params: map[string]any{KeyZoneType: "LOCAL"}}
					_, _ = r.handleCreateZone(ctx, step, state)
				}
			case PrecondDeviceHasGridZone:
				zs := getZoneState(state)
				if !hasZoneOfType(zs, ZoneTypeGrid) {
					step := &loader.Step{Params: map[string]any{KeyZoneType: ZoneTypeGrid, KeyZoneID: "GRID"}}
					_, _ = r.handleCreateZone(ctx, step, state)
				}
				r.connMgr.SetCommissionZoneType(cert.ZoneTypeGrid)

				// For real devices: commission a real GRID zone alongside the suite zone.
				// Detach pool.Main from suite and clear session+working crypto so the
				// upcoming ensureCommissioned call performs a fresh GRID commission.
				if r.config.Target != "" {
					r.debugf("device_has_grid_zone: commissioning real GRID zone on device")
					r.pool.SetMain(&Connection{})
					r.connMgr.SetPASEState(nil)
					r.connMgr.SetZoneCA(nil)
					r.connMgr.SetControllerCert(nil)
					r.connMgr.SetIssuedDeviceCert(nil)
					if err := r.waitForCommissioningMode(ctx, 3*time.Second); err != nil {
						r.debugf("device_has_grid_zone: %v (continuing)", err)
					}
				}
			case PrecondDeviceHasLocalZone:
				zs := getZoneState(state)
				if !hasZoneOfType(zs, ZoneTypeLocal) {
					step := &loader.Step{Params: map[string]any{KeyZoneType: ZoneTypeLocal, KeyZoneID: "LOCAL"}}
					_, _ = r.handleCreateZone(ctx, step, state)
				}
				r.connMgr.SetCommissionZoneType(cert.ZoneTypeLocal)

				// For real devices: commission a real LOCAL zone alongside the suite zone.
				// Detach pool.Main from suite, clear PASE + crypto so ensureCommissioned
				// creates a fresh LOCAL zone tracked in the pool.
				// StoreZoneCrypto (called in performCertExchange) automatically adds
				// the new zone's CA to the accumulated pool, so no savedPool needed.
				if r.config.Target != "" {
					r.debugf("device_has_local_zone: commissioning real LOCAL zone on device")
					r.pool.SetMain(&Connection{})
					r.connMgr.SetPASEState(nil)
					r.connMgr.SetZoneCA(nil)
					r.connMgr.SetControllerCert(nil)
					r.connMgr.SetIssuedDeviceCert(nil)
					if err := r.waitForCommissioningMode(ctx, 3*time.Second); err != nil {
						r.debugf("device_has_local_zone: %v (continuing)", err)
					}
				}
			case PrecondDeviceInLocalZone:
				zs := getZoneState(state)
				if !hasZoneOfType(zs, ZoneTypeLocal) {
					step := &loader.Step{Params: map[string]any{KeyZoneType: ZoneTypeLocal, KeyZoneID: "LOCAL"}}
					_, _ = r.handleCreateZone(ctx, step, state)
				}
				if len(zs.zones) == 0 {
					zoneID := "sim-local-zone"
					zs.zones[zoneID] = &zoneInfo{
						ZoneID:         zoneID,
						ZoneType:       ZoneTypeLocal,
						Priority:       zonePriority[ZoneTypeLocal],
						Connected:      true,
						Metadata:       make(map[string]any),
						CommissionedAt: time.Now(),
					}
					zs.zoneOrder = append(zs.zoneOrder, zoneID)
					state.Set(StateLocalZoneID, zoneID)
				}
				r.connMgr.SetCommissionZoneType(cert.ZoneTypeLocal)
			case PrecondNoZonesConfigured:
				zs := getZoneState(state)
				zs.zones = make(map[string]*zoneInfo)
				zs.zoneOrder = nil
			case PrecondLocalZoneConfigured:
				zs := getZoneState(state)
				if _, exists := zs.zones["zone-local-001"]; !exists {
					zs.zones["zone-local-001"] = &zoneInfo{
						ZoneID:         "zone-local-001",
						ZoneType:       ZoneTypeLocal,
						Priority:       zonePriority[ZoneTypeLocal],
						Connected:      false,
						Metadata:       make(map[string]any),
						CommissionedAt: time.Now(),
					}
					zs.zoneOrder = append(zs.zoneOrder, "zone-local-001")
				}
			case PrecondTwoZonesConfigured:
				zs := getZoneState(state)
				if _, exists := zs.zones["zone-grid-001"]; !exists {
					zs.zones["zone-grid-001"] = &zoneInfo{
						ZoneID:         "zone-grid-001",
						ZoneType:       ZoneTypeGrid,
						Priority:       zonePriority[ZoneTypeGrid],
						Connected:      false,
						Metadata:       make(map[string]any),
						CommissionedAt: time.Now(),
					}
					zs.zoneOrder = append(zs.zoneOrder, "zone-grid-001")
				}
				if _, exists := zs.zones["zone-local-001"]; !exists {
					zs.zones["zone-local-001"] = &zoneInfo{
						ZoneID:         "zone-local-001",
						ZoneType:       ZoneTypeLocal,
						Priority:       zonePriority[ZoneTypeLocal],
						Connected:      false,
						Metadata:       make(map[string]any),
						CommissionedAt: time.Now(),
					}
					zs.zoneOrder = append(zs.zoneOrder, "zone-local-001")
				}
			case PrecondCommissioningWindowClosed:
				state.Set(StateCommissioningActive, false)
				// For real devices, the runner's auto-PICS path explicitly
				// re-opens the commissioning window after suite-zone setup
				// (runner.go: TriggerEnterCommissioningMode) so subsequent
				// tests can commission new zones without re-triggering.
				// Tests like TC-PAIR-002 need the OPPOSITE -- the device must
				// be uncommissioned-AND-window-closed at step 1 for the
				// "verify_mdns_not_advertising" check. Without the trigger
				// here the auto-PICS posture leaks into the test setup.
				//
				// We also stop the runner's mDNS observer so that step 1's
				// verify_mdns_not_advertising starts a fresh browse rather
				// than seeing observer-cached service entries from before
				// the device's goodbye announcement (commissionable mDNS
				// goodbye is asynchronous; the observer may not have
				// processed it within the step's default 1s window).
				if r.config.Target != "" && r.config.EnableKey != "" {
					r.debugf("commissioning_window_closed: sending TriggerExitCommissioningMode")
					if err := r.sendTriggerViaZone(ctx, features.TriggerExitCommissioningMode, state); err != nil {
						r.debugf("commissioning_window_closed: trigger failed: %v (continuing)", err)
					}
					r.stopObserver()
				}
			case PrecondDeviceInZone:
				zs := getZoneState(state)
				if len(zs.zones) == 0 {
					zoneID := "sim-local-zone"
					zs.zones[zoneID] = &zoneInfo{
						ZoneID:         zoneID,
						ZoneType:       ZoneTypeLocal,
						Priority:       zonePriority[ZoneTypeLocal],
						Connected:      true,
						Metadata:       make(map[string]any),
						CommissionedAt: time.Now(),
					}
					zs.zoneOrder = append(zs.zoneOrder, zoneID)
					state.Set(StateLocalZoneID, zoneID)
				}
			case PrecondCommissioningWindowAt95s:
				state.Set(StateCommWindowStart, time.Now().Add(-95*time.Second))
			case PrecondControllerCertNearExpiry:
				state.Set(StateCertDaysUntilExpiry, 29)
			case PrecondFiveZonesConnected:
				ct := getConnectionTracker(state)
				for _, name := range []string{"GRID", "BUILDING", "HOME", "USER1", "USER2"} {
					if _, exists := ct.zoneConnections[name]; !exists {
						ct.zoneConnections[name] = &Connection{state: ConnOperational}
					}
				}
			case PrecondTwoZonesConnected:
				*needsMultiZone = false // Already handled inline.
				ct := getConnectionTracker(state)
				zones := []struct {
					name string
					zt   cert.ZoneType
				}{
					{"GRID", cert.ZoneTypeGrid},
					{"LOCAL", cert.ZoneTypeLocal},
				}
				if r.config.Target != "" {
					r.debugf("two_zones_connected: commissioning against real device")
					if err := r.requireCommissioningAvailable(ctx, "two_zones_connected: commissioning unavailable before zone setup"); err != nil {
						return err
					}
					for i, z := range zones {
						if _, exists := ct.zoneConnections[z.name]; exists {
							r.debugf("two_zones_connected: zone %s already exists, skipping", z.name)
							continue
						}
						if err := r.requireCommissioningAvailable(ctx, fmt.Sprintf("two_zones_connected: commissioning unavailable before zone %s", z.name)); err != nil {
							return err
						}
						r.debugf("two_zones_connected: commissioning zone %s (type=%d)", z.name, z.zt)
						r.debugSnapshot("two_zones_connected BEFORE commission " + z.name)

						ps := r.connMgr.PASEState()
						if r.pool.Main() != nil && r.pool.Main().isConnected() && ps != nil && ps.completed && r.suite.ZoneID() == "" {
							r.debugf("two_zones_connected: sending RemoveZone before disconnect (zone %d)", i)
							if r.config != nil && r.config.StrictLifecycle {
								if err := r.sendRemoveZoneStrict(); err != nil {
									return fmt.Errorf("two_zones_connected: strict remove zone: %w", err)
								}
							} else {
								r.sendRemoveZone()
							}
						}

						r.disconnectConnection()
						r.connMgr.SetZoneCA(nil)
						r.connMgr.SetControllerCert(nil)
						r.connMgr.SetIssuedDeviceCert(nil)
						// zoneCAPool is rebuilt by StoreZoneCrypto after each cert exchange,
						// so no manual savedPool save/restore needed.

						r.connMgr.SetCommissionZoneType(z.zt)

						if err := r.ensureCommissioned(ctx, state); err != nil {
							r.debugf("two_zones_connected: PASE FAILED for zone %s: %v", z.name, err)
							r.debugSnapshot("two_zones_connected AFTER PASE FAIL " + z.name)
							return fmt.Errorf("precondition two_zones_connected commission zone %s: %w", z.name, err)
						}

						r.debugf("two_zones_connected: zone %s commissioned successfully", z.name)

						zoneConn := r.pool.Main()
						ct.zoneConnections[z.name] = zoneConn
						state.Set(ZoneConnectionStateKey(z.name), zoneConn)

						ps = r.connMgr.PASEState()
						if ps != nil && ps.sessionKey != nil {
							zID := deriveZoneIDFromSecret(ps.sessionKey)
							r.pool.TrackZone(z.name, zoneConn, zID)

							var stateKey string
							switch z.zt {
							case cert.ZoneTypeGrid:
								stateKey = StateGridZoneID
							case cert.ZoneTypeLocal:
								stateKey = StateLocalZoneID
							case cert.ZoneTypeTest:
								stateKey = StateTestZoneID
							}
							if stateKey != "" {
								state.Set(stateKey, zID)
							}
						}

						if i == len(zones)-1 {
							if err := r.waitForOperationalReady(2 * time.Second); err != nil {
								r.debugf("two_zones_connected: %v (continuing)", err)
							}
						}

						r.pool.SetMain(&Connection{})
					}
					firstZone := zones[0]
					if zc, ok := ct.zoneConnections[firstZone.name]; ok && zc.isConnected() {
						r.pool.SetMain(zc)
						r.debugf("two_zones_connected: restored main conn from zone %s", firstZone.name)
					}
					if gridID, ok := state.Get(StateGridZoneID); ok {
						state.Set(StateCurrentZoneID, gridID)
					}
					if localID, ok := state.Get(StateLocalZoneID); ok {
						state.Set(StateOtherZoneID, localID)
					}
					r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
				} else {
					for _, z := range zones {
						if _, exists := ct.zoneConnections[z.name]; exists {
							continue
						}
						dummyConn := &Connection{state: ConnOperational}
						ct.zoneConnections[z.name] = dummyConn
						r.pool.TrackZone(z.name, dummyConn, z.name)
					}
				}
			case PrecondDeviceZonesFull:
				if r.config.Target != "" {
					r.debugf("device_zones_full: commissioning zones to fill device slots")
					ct := getConnectionTracker(state)

					if !r.connMgr.LastDeviceConnClose().IsZero() {
						if err := r.waitForCommissioningMode(ctx, 3*time.Second); err != nil {
							r.debugf("device_zones_full: %v (continuing)", err)
						}
					}

					zones := []struct {
						name string
						zt   cert.ZoneType
					}{
						{"GRID", cert.ZoneTypeGrid},
						{"LOCAL", cert.ZoneTypeLocal},
					}

					for i, z := range zones {
						if _, exists := ct.zoneConnections[z.name]; exists {
							r.debugf("device_zones_full: zone %s already exists, skipping", z.name)
							continue
						}
						r.debugf("device_zones_full: commissioning zone %s (type=%d)", z.name, z.zt)

						ps := r.connMgr.PASEState()
						if r.pool.Main() != nil && r.pool.Main().isConnected() && ps != nil && ps.completed {
							r.debugf("device_zones_full: sending RemoveZone before disconnect (zone %d)", i)
							if r.config != nil && r.config.StrictLifecycle {
								if err := r.sendRemoveZoneStrict(); err != nil {
									return fmt.Errorf("device_zones_full: strict remove zone: %w", err)
								}
							} else {
								r.sendRemoveZone()
							}
						}

						r.disconnectConnection()
						r.connMgr.SetZoneCA(nil)
						r.connMgr.SetControllerCert(nil)
						r.connMgr.SetIssuedDeviceCert(nil)
						// zoneCAPool is rebuilt by StoreZoneCrypto after each cert exchange.

						r.connMgr.SetCommissionZoneType(z.zt)
						if err := r.ensureCommissioned(ctx, state); err != nil {
							r.debugf("device_zones_full: commission zone %s FAILED: %v", z.name, err)
							return fmt.Errorf("precondition device_zones_full commission zone %s: %w", z.name, err)
						}

						r.debugf("device_zones_full: zone %s commissioned successfully", z.name)

						zoneConn := r.pool.Main()
						ct.zoneConnections[z.name] = zoneConn
						state.Set(ZoneConnectionStateKey(z.name), zoneConn)

						ps = r.connMgr.PASEState()
						if ps != nil && ps.sessionKey != nil {
							zID := deriveZoneIDFromSecret(ps.sessionKey)
							r.pool.TrackZone(z.name, zoneConn, zID)

							var stateKey string
							switch z.zt {
							case cert.ZoneTypeGrid:
								stateKey = StateGridZoneID
							case cert.ZoneTypeLocal:
								stateKey = StateLocalZoneID
							}
							if stateKey != "" {
								state.Set(stateKey, zID)
							}
						}

						r.pool.SetMain(&Connection{})

						if i < len(zones)-1 {
							for _, tracked := range ct.zoneConnections {
								if tracked != nil && tracked.isConnected() {
									savedConn := r.pool.Main()
									r.pool.SetMain(tracked)
									r.debugf("device_zones_full: triggering enter commissioning mode on tracked zone connection")
									_, _ = r.sendTrigger(ctx, features.TriggerEnterCommissioningMode, state)
									r.pool.SetMain(savedConn)
									break
								}
							}
							if err := r.waitForCommissioningMode(ctx, 5*time.Second); err != nil {
								r.debugf("device_zones_full: %v (continuing)", err)
							}
						}
					}
					if err := r.waitForCommissioningMode(ctx, 3*time.Second); err != nil {
						r.debugf("device_zones_full: post-fill %v (continuing)", err)
					}

					r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
					r.connMgr.SetPASEState(nil)
				}
			}
		}
	}
	return nil
}
