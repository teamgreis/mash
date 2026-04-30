package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// requireTestZone returns a StatusNotAuthorized error if the caller's zone
// type (from context) is not TEST. TestControl commands must never be accepted
// on production (GRID/LOCAL) zones.
func requireTestZone(ctx context.Context) error {
	zt := zonecontext.CallerZoneTypeFromContext(ctx)
	if zt != cert.ZoneTypeTest {
		return &wire.CommandError{
			Status:  wire.StatusNotAuthorized,
			Message: "TestControl commands require a TEST zone",
		}
	}
	return nil
}

// RegisterTestEventHandler wires the triggerTestEvent command on the given
// TestControl feature to the device service's trigger dispatch. This must be
// called while the *TestControl wrapper is still available (before the inner
// Feature is added to the endpoint) and only when TestMode is enabled.
//
// Per DEC-087, both the TEST-zone gate (delegated to requireTestZone) and the
// enable-key check surface StatusNotAuthorized. Using the same status for
// both authentication-style failures is intentional: distinct codes would
// leak information to a network observer about which check failed.
func (s *DeviceService) RegisterTestEventHandler(tc *features.TestControl) {
	tc.OnTriggerTestEvent(func(ctx context.Context, req features.TriggerTestEventRequest) error {
		if err := requireTestZone(ctx); err != nil {
			return err
		}
		// Validate enable key.
		if req.EnableKey != s.config.TestEnableKey {
			s.debugLog("triggerTestEvent: enable key mismatch")
			return &wire.CommandError{
				Status:  wire.StatusNotAuthorized,
				Message: "triggerTestEvent: enable-key mismatch",
			}
		}

		return s.dispatchTrigger(ctx, req.EventTrigger)
	})
}

// RegisterSetCommissioningWindowDurationHandler wires the setCommissioningWindowDuration
// command on the given TestControl feature. This allows the test harness to dynamically
// set the commissioning window duration on a running device.
func (s *DeviceService) RegisterSetCommissioningWindowDurationHandler(tc *features.TestControl) {
	tc.OnSetCommissioningWindowDuration(func(ctx context.Context, req features.SetCommissioningWindowDurationRequest) error {
		if err := requireTestZone(ctx); err != nil {
			return err
		}
		// Validate enable key (DEC-087: NOT_AUTHORIZED on mismatch).
		if req.EnableKey != s.config.TestEnableKey {
			s.debugLog("setCommissioningWindowDuration: enable key mismatch")
			return &wire.CommandError{
				Status:  wire.StatusNotAuthorized,
				Message: "setCommissioningWindowDuration: enable-key mismatch",
			}
		}

		// Clamp to [3s, 10800s].
		durSec := req.DurationSeconds
		if durSec < 3 {
			durSec = 3
		}
		if durSec > 10800 {
			durSec = 10800
		}

		dur := time.Duration(durSec) * time.Second
		dm := s.DiscoveryManager()
		if dm != nil {
			dm.SetCommissioningWindowDuration(dur)
		}

		s.debugLog("setCommissioningWindowDuration: set",
			"requestedSeconds", req.DurationSeconds,
			"effectiveSeconds", durSec)
		return nil
	})
}

// TestControlCmdGetTestStateParamEnableKey is retained as a back-compat alias
// for the enableKey parameter ID. The canonical declaration lives in
// pkg/features/test_control.go alongside the other manual TestControl
// constants so internal/inspect can reference it without an import cycle.
const TestControlCmdGetTestStateParamEnableKey = features.GetTestStateParamEnableKey

// TestControlCmdGetTestState is the manual diagnostic command ID. Aliased
// from pkg/features for symmetry with the param-name above.
const TestControlCmdGetTestState = features.TestControlCmdGetTestState

// RegisterGetTestStateHandler adds the getTestState command to the TestControl
// feature. This command returns a snapshot of the device's mutable state for
// test-level diagnostics. It allows the test harness to detect state leakage
// between shuffled tests.
func (s *DeviceService) RegisterGetTestStateHandler(tc *features.TestControl) {
	tc.Feature.AddCommand(model.NewCommand(&model.CommandMetadata{
		ID:          TestControlCmdGetTestState,
		Name:        "getTestState",
		Description: "Returns device mutable state snapshot for test diagnostics.",
		Parameters: []model.ParameterMetadata{
			{ID: 1, Name: "enableKey", Type: model.DataTypeString, Required: true},
		},
	}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		if err := requireTestZone(ctx); err != nil {
			return nil, err
		}
		enableKey, _ := params[1].(string)
		if enableKey != s.config.TestEnableKey {
			return nil, &wire.CommandError{
				Status:  wire.StatusNotAuthorized,
				Message: "getTestState: enable-key mismatch",
			}
		}
		// Wrap the diagnostic snapshot under response field id 1 — the
		// snapshot itself is a freeform string-keyed map of internal
		// state and predates the integer-key rule (Issue #11). The
		// harness consumer reads result[1] then walks the snapshot.
		return map[uint8]any{1: s.collectTestState()}, nil
	}))
}

// collectTestState gathers a snapshot of all mutable device state that could
// leak between tests. The keys and types are stable -- the harness diffs
// consecutive snapshots to detect leakage.
func (s *DeviceService) collectTestState() map[string]any {
	s.mu.RLock()
	// Zone info
	zones := make([]map[string]any, 0, len(s.connectedZones))
	for id, z := range s.connectedZones {
		zi := map[string]any{
			"id":        id,
			"type":      int(z.Type),
			"priority":  int(z.Priority),
			"connected": z.Connected,
		}
		if sess, ok := s.zoneSessions[id]; ok {
			zi["has_session"] = true
			zi["subscriptions"] = sess.SubscriptionCount()
		}
		zones = append(zones, zi)
	}

	// Failsafe timers
	failsafeStates := make(map[string]string, len(s.failsafeTimers))
	for id, timer := range s.failsafeTimers {
		failsafeStates[id] = timer.State().String()
	}

	clockOffset := int(s.clockOffset.Seconds())
	autoReentry := s.autoReentryPending
	pairingActive := s.pairingRequestActive
	s.mu.RUnlock()

	snapshot := map[string]any{
		"zone_count":         len(zones),
		"zones":              zones,
		"failsafe_timers":    failsafeStates,
		"clock_offset_s":     clockOffset,
		"commissioning_open": s.commissioningOpen.Load(),
		"auto_reentry":       autoReentry,
		"pairing_active":     pairingActive,
		"active_conns":       int(s.activeConns.Load()),
	}

	if s.connTracker != nil {
		snapshot["conn_tracker_count"] = s.connTracker.Len()
	}
	if s.paseTracker != nil {
		snapshot["pase_failed_attempts"] = s.paseTracker.AttemptCount()
	}

	// Feature attribute snapshot (first endpoint with each feature)
	for _, ep := range s.device.Endpoints() {
		if f, err := ep.GetFeatureByID(uint8(model.FeatureStatus)); err == nil {
			if v, err2 := f.ReadAttribute(features.StatusAttrOperatingState); err2 == nil {
				snapshot["status_operating_state"] = v
			}
			break
		}
	}
	for _, ep := range s.device.Endpoints() {
		if f, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl)); err == nil {
			if v, err2 := f.ReadAttribute(features.EnergyControlAttrControlState); err2 == nil {
				snapshot["ec_control_state"] = v
			}
			if v, err2 := f.ReadAttribute(features.EnergyControlAttrProcessState); err2 == nil {
				snapshot["ec_process_state"] = v
			}
			break
		}
	}
	for _, ep := range s.device.Endpoints() {
		if f, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession)); err == nil {
			if v, err2 := f.ReadAttribute(features.ChargingSessionAttrState); err2 == nil {
				snapshot["charging_state"] = v
			}
			break
		}
	}
	for _, ep := range s.device.Endpoints() {
		if f, err := ep.GetFeatureByID(uint8(model.FeatureMeasurement)); err == nil {
			if v, err2 := f.ReadAttribute(features.MeasurementAttrACActivePower); err2 == nil {
				snapshot["measurement_power"] = v
			}
			break
		}
	}

	return snapshot
}

// dispatchTrigger routes a trigger opcode to the appropriate domain handler.
//
// DEC-087 maps dispatcher failures to wire Status:
//   - unknown domain (this opcode is not in the registry on this device)
//     → StatusUnsupported
//   - known domain, but the named feature is absent on every endpoint
//     → StatusConstraintError (handled in the per-domain helpers)
//   - unknown opcode within a known domain → StatusConstraintError
//     (the device understands the feature but not this specific trigger)
func (s *DeviceService) dispatchTrigger(ctx context.Context, trigger uint64) error {
	domain := features.TriggerDomain(trigger)

	switch domain {
	case uint16(model.FeatureDeviceInfo):
		return s.handleCommissioningTrigger(ctx, trigger)
	case uint16(model.FeatureStatus):
		return s.handleStatusTrigger(ctx, trigger)
	case uint16(model.FeatureMeasurement):
		return s.handleMeasurementTrigger(ctx, trigger)
	case uint16(model.FeatureChargingSession):
		return s.handleChargingSessionTrigger(ctx, trigger)
	case uint16(model.FeatureEnergyControl):
		return s.handleEnergyControlTrigger(ctx, trigger)
	default:
		return &wire.CommandError{
			Status:  wire.StatusUnsupported,
			Message: fmt.Sprintf("unknown trigger domain: 0x%04x", domain),
		}
	}
}

// handleCommissioningTrigger handles DeviceInfo-domain triggers.
func (s *DeviceService) handleCommissioningTrigger(_ context.Context, trigger uint64) error {
	switch trigger {
	case features.TriggerEnterCommissioningMode:
		s.debugLog("trigger: entering commissioning mode")
		return s.EnterCommissioningMode()
	case features.TriggerExitCommissioningMode:
		s.debugLog("trigger: exiting commissioning mode")
		return s.ExitCommissioningMode()
	case features.TriggerFactoryReset:
		s.debugLog("trigger: factory reset (removing all zones)")
		for _, zoneID := range s.ListZoneIDs() {
			_ = s.RemoveZone(zoneID)
		}
		return nil
	case features.TriggerResetTestState:
		s.debugLog("trigger: resetting test state to defaults")
		for _, ep := range s.device.Endpoints() {
			// Reset Status to STANDBY. faultCode/faultMessage are nullable
			// ("no fault" maps to null, not zero) so clear them to nil
			// rather than 0/"".
			if f, err := ep.GetFeatureByID(uint8(model.FeatureStatus)); err == nil {
				_ = f.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateStandby))
				_ = f.SetAttributeInternal(features.StatusAttrFaultCode, nil)
				_ = f.SetAttributeInternal(features.StatusAttrFaultMessage, nil)
			}
			// Reset Measurement to zero.
			if f, err := ep.GetFeatureByID(uint8(model.FeatureMeasurement)); err == nil {
				_ = f.SetAttributeInternal(features.MeasurementAttrACActivePower, int64(0))
			}
			// Reset ChargingSession to NOT_PLUGGED_IN.
			if f, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession)); err == nil {
				_ = f.SetAttributeInternal(features.ChargingSessionAttrState,
					uint8(features.ChargingStateNotPluggedIn))
			}
			// Reset EnergyControl to AUTONOMOUS/NONE.
			if f, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl)); err == nil {
				_ = f.SetAttributeInternal(features.EnergyControlAttrControlState,
					uint8(features.ControlStateAutonomous))
				_ = f.SetAttributeInternal(features.EnergyControlAttrProcessState,
					uint8(features.ProcessStateNone))
				// Restore capability flags overridden by test triggers.
				// Hardcoded to the EVSE/HeatPump factory defaults
				// (both have isPausable=true). If a future device-under-test
				// has different defaults, this needs a per-device hook.
				_ = f.SetAttributeInternal(features.EnergyControlAttrIsPausable, true)
			}
		}
		// Remove leaked/partial non-TEST zones.
		// 1) Zones without an active session (classic failed-PASE leak).
		// 2) Zones marked disconnected, even if a stale session map entry
		//    still exists due close-order races during teardown.
		// TEST zones are preserved: the suite zone is the persistent control
		// channel and may be temporarily disconnected between tests.
		// Collect IDs under RLock, then remove outside the lock because
		// RemoveZone takes s.mu.Lock internally and calls slow operations.
		s.mu.RLock()
		var staleZoneIDs []string
		for id, cz := range s.connectedZones {
			if cz != nil && cz.Type == cert.ZoneTypeTest {
				continue
			}
			_, hasSession := s.zoneSessions[id]
			if !hasSession || (cz != nil && !cz.Connected) {
				staleZoneIDs = append(staleZoneIDs, id)
			}
		}
		s.mu.RUnlock()
		for _, id := range staleZoneIDs {
			s.debugLog("trigger: removing leaked zone", "zoneID", id)
			_ = s.RemoveZone(id)
		}
		// Reset LimitResolver state (clears per-zone limits, timers, callbacks).
		if s.limitResolver != nil {
			s.limitResolver.ResetAll()
		}
		// Reset clock offset.
		s.clockOffset = 0
		// Reset commissioning window: exit clears timers and mDNS, then
		// restore the default duration and re-enter so the ALPN gate
		// (mash-comm/1) stays open for the next test's PASE handshake.
		_ = s.ExitCommissioningMode()
		if s.discoveryManager != nil && s.config.CommissioningWindowDuration > 0 {
			s.discoveryManager.SetCommissioningWindowDuration(s.config.CommissioningWindowDuration)
		}
		_ = s.EnterCommissioningMode()
		// Reset PASE backoff counter so timing-sensitive tests start clean.
		s.ResetPASETracker()
		// Clear connection cooldown timer so cooldown tests are hermetic.
		// Bump the generation to invalidate any stale goroutines from the
		// previous test that hold a defer releaseCommissioningConnection.
		s.connectionMu.Lock()
		s.lastCommissioningAttempt = time.Time{}
		s.commissioningConnActive = false
		s.commissioningGeneration++
		s.connectionMu.Unlock()
		// Reset failsafe timers to prevent active failsafes from prior
		// tests from firing during subsequent tests.
		s.mu.Lock()
		for _, timer := range s.failsafeTimers {
			timer.Reset()
		}
		s.mu.Unlock()
		// Close pre-operational connections tracked by connTracker to
		// prevent stale entries from prior tests from being reaped mid-test.
		if s.connTracker != nil {
			closed := s.connTracker.CloseAll()
			// If connections were closed, give goroutines a brief moment
			// to decrement activeConns via their defers.
			if closed > 0 {
				time.Sleep(100 * time.Millisecond)
			}
		}
		// Reset auto-reentry flag so commissioning re-entry is clean.
		s.autoReentryPending = false
		// Clear inbound subscriptions on all active zone sessions to stop
		// notifications from leaking into the next test.
		s.mu.Lock()
		for _, session := range s.zoneSessions {
			session.ClearSubscriptions()
		}
		s.mu.Unlock()
		// Clear the global subscription manager so no stale subscription
		// entries accumulate across tests (separate from per-session clearing).
		if s.subscriptionManager != nil {
			s.subscriptionManager.ClearAll()
		}
		// Reset pairing request listening: stop first to clear stale state,
		// then restart if configured so the next test can receive pairing
		// requests (e.g., TC-COMM-WINDOW-002).
		_ = s.StopPairingRequestListening()
		if s.config.ListenForPairingRequests && s.ctx != nil {
			_ = s.StartPairingRequestListening(s.ctx)
		}
		return nil
	default:
		// Check for parameterized triggers (base + encoded value).
		if trigger&0xFFFF_FFFF_0000_0000 == features.TriggerAdjustClockBase {
			offsetSeconds := int32(trigger & 0xFFFF_FFFF)
			s.mu.Lock()
			s.clockOffset = time.Duration(offsetSeconds) * time.Second
			s.mu.Unlock()
			s.debugLog("trigger: clock offset adjusted", "offsetSeconds", offsetSeconds)
			return nil
		}
		return &wire.CommandError{
			Status:  wire.StatusConstraintError,
			Message: fmt.Sprintf("unknown commissioning trigger: 0x%016x", trigger),
		}
	}
}

// handleStatusTrigger handles Status-domain triggers.
func (s *DeviceService) handleStatusTrigger(_ context.Context, trigger uint64) error {
	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureStatus))
		if err != nil {
			continue
		}

		switch {
		case trigger == features.TriggerFault:
			s.debugLog("trigger: setting fault state", "endpoint", ep.ID())
			_ = feat.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateFault))
			_ = feat.SetAttributeInternal(features.StatusAttrFaultCode, uint32(1))
			_ = feat.SetAttributeInternal(features.StatusAttrFaultMessage, "Test trigger fault")
			return nil
		case trigger&0xFFFF_FFFF_0000_0000 == features.TriggerFaultWithCodeBase:
			faultCode := uint32(trigger & 0xFFFF_FFFF)
			s.debugLog("trigger: setting fault state with code", "faultCode", faultCode, "endpoint", ep.ID())
			_ = feat.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateFault))
			_ = feat.SetAttributeInternal(features.StatusAttrFaultCode, faultCode)
			_ = feat.SetAttributeInternal(features.StatusAttrFaultMessage, "Test trigger fault")
			return nil
		case trigger == features.TriggerClearFault:
			s.debugLog("trigger: clearing fault", "endpoint", ep.ID())
			_ = feat.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateRunning))
			_ = feat.SetAttributeInternal(features.StatusAttrFaultCode, nil)
			_ = feat.SetAttributeInternal(features.StatusAttrFaultMessage, nil)
			return nil
		case trigger == features.TriggerSetStandby:
			s.debugLog("trigger: setting standby", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateStandby))
		case trigger == features.TriggerSetRunning:
			s.debugLog("trigger: setting running", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.StatusAttrOperatingState, uint8(features.OperatingStateRunning))
		default:
			return &wire.CommandError{
				Status:  wire.StatusConstraintError,
				Message: fmt.Sprintf("unknown status trigger: 0x%016x", trigger),
			}
		}
	}
	return &wire.CommandError{
		Status:  wire.StatusConstraintError,
		Message: "no Status feature found on any endpoint",
	}
}

// handleMeasurementTrigger handles Measurement-domain triggers.
func (s *DeviceService) handleMeasurementTrigger(_ context.Context, trigger uint64) error {
	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureMeasurement))
		if err != nil {
			continue
		}

		switch {
		case trigger == features.TriggerSetPower100:
			s.debugLog("trigger: setting power to 100W", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrACActivePower, int64(100000))
		case trigger == features.TriggerSetPower1000:
			s.debugLog("trigger: setting power to 1kW", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrACActivePower, int64(1000000))
		case trigger == features.TriggerSetPowerZero:
			s.debugLog("trigger: setting power to 0W", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrACActivePower, int64(0))
		case trigger == features.TriggerSetSoC50:
			s.debugLog("trigger: setting SoC to 50%", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrStateOfCharge, uint8(50))
		case trigger == features.TriggerSetSoC100:
			s.debugLog("trigger: setting SoC to 100%", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrStateOfCharge, uint8(100))
		case trigger&0xFFFF_FFFF_0000_0000 == features.TriggerSetPowerCustomBase:
			// Custom power value encoded in lower 32 bits (milliwatts as int32).
			mw := int64(int32(trigger & 0xFFFF_FFFF))
			s.debugLog("trigger: setting power to custom value", "milliwatts", mw, "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.MeasurementAttrACActivePower, mw)
		default:
			return &wire.CommandError{
				Status:  wire.StatusConstraintError,
				Message: fmt.Sprintf("unknown measurement trigger: 0x%016x", trigger),
			}
		}
	}
	return &wire.CommandError{
		Status:  wire.StatusConstraintError,
		Message: "no Measurement feature found on any endpoint",
	}
}

// handleChargingSessionTrigger handles ChargingSession-domain triggers.
func (s *DeviceService) handleChargingSessionTrigger(_ context.Context, trigger uint64) error {
	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession))
		if err != nil {
			continue
		}

		switch trigger {
		case features.TriggerEVPlugIn:
			s.debugLog("trigger: EV plug in", "endpoint", ep.ID())
			_ = feat.SetAttributeInternal(features.ChargingSessionAttrSessionStartTime,
				uint64(time.Now().Unix()))
			return feat.SetAttributeInternal(features.ChargingSessionAttrState,
				uint8(features.ChargingStatePluggedInNoDemand))
		case features.TriggerEVUnplug:
			s.debugLog("trigger: EV unplug", "endpoint", ep.ID())
			_ = feat.SetAttributeInternal(features.ChargingSessionAttrSessionEndTime,
				uint64(time.Now().Unix()))
			return feat.SetAttributeInternal(features.ChargingSessionAttrState,
				uint8(features.ChargingStateNotPluggedIn))
		case features.TriggerEVRequestCharge:
			s.debugLog("trigger: EV request charge", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.ChargingSessionAttrState,
				uint8(features.ChargingStatePluggedInDemand))
		default:
			return &wire.CommandError{
				Status:  wire.StatusConstraintError,
				Message: fmt.Sprintf("unknown charging session trigger: 0x%016x", trigger),
			}
		}
	}
	return &wire.CommandError{
		Status:  wire.StatusConstraintError,
		Message: "no ChargingSession feature found on any endpoint",
	}
}

// handleEnergyControlTrigger handles EnergyControl-domain triggers.
func (s *DeviceService) handleEnergyControlTrigger(_ context.Context, trigger uint64) error {
	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl))
		if err != nil {
			continue
		}

		switch trigger {
		// ControlState triggers.
		case features.TriggerControlStateAutonomous:
			s.debugLog("trigger: setting control state AUTONOMOUS", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrControlState,
				uint8(features.ControlStateAutonomous))
		case features.TriggerControlStateControlled:
			s.debugLog("trigger: setting control state CONTROLLED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrControlState,
				uint8(features.ControlStateControlled))
		case features.TriggerControlStateLimited:
			s.debugLog("trigger: setting control state LIMITED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrControlState,
				uint8(features.ControlStateLimited))
		case features.TriggerControlStateFailsafe:
			s.debugLog("trigger: setting control state FAILSAFE", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrControlState,
				uint8(features.ControlStateFailsafe))
		case features.TriggerControlStateOverride:
			s.debugLog("trigger: setting control state OVERRIDE", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrControlState,
				uint8(features.ControlStateOverride))

		// ProcessState triggers.
		case features.TriggerProcessStateNone:
			s.debugLog("trigger: setting process state NONE", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateNone))
		case features.TriggerProcessStateAvailable:
			s.debugLog("trigger: setting process state AVAILABLE", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateAvailable))
		case features.TriggerProcessStateScheduled:
			s.debugLog("trigger: setting process state SCHEDULED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateScheduled))
		case features.TriggerProcessStateRunning:
			s.debugLog("trigger: setting process state RUNNING", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateRunning))
		case features.TriggerProcessStatePaused:
			s.debugLog("trigger: setting process state PAUSED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStatePaused))
		case features.TriggerProcessStateCompleted:
			s.debugLog("trigger: setting process state COMPLETED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateCompleted))
		case features.TriggerProcessStateAborted:
			s.debugLog("trigger: setting process state ABORTED", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrProcessState,
				uint8(features.ProcessStateAborted))

		// Capability override triggers (test-only). These flip readOnly
		// capability flags so conformance tests can exercise the
		// StatusUnsupported reject path on commands gated by the flag.
		// docs/features/energy-control.md: when isPausable=false, Pause
		// and Resume MUST surface as wire.StatusUnsupported.
		case features.TriggerSetIsPausableFalse:
			s.debugLog("trigger: setting isPausable=false", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrIsPausable, false)
		case features.TriggerSetIsPausableTrue:
			s.debugLog("trigger: setting isPausable=true", "endpoint", ep.ID())
			return feat.SetAttributeInternal(features.EnergyControlAttrIsPausable, true)
		default:
			return &wire.CommandError{
				Status:  wire.StatusConstraintError,
				Message: fmt.Sprintf("unknown energy control trigger: 0x%016x", trigger),
			}
		}
	}
	return &wire.CommandError{
		Status:  wire.StatusConstraintError,
		Message: "no EnergyControl feature found on any endpoint",
	}
}
