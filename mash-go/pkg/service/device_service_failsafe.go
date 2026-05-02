package service

import (
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/duration"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
)

// Failsafe state machine (per docs/testing/behavior/failsafe-timing.md and
// docs/features/energy-control.md):
//
//	connected -> disconnect (no other auth zone)
//	          -> handleFailsafe: ControlState=FAILSAFE,
//	             effective limits := failsafe limits,
//	             start failsafeDuration countdown
//	          -> reconnect (valid TLS)        => exitFailsafe (CONTROLLED)
//	          -> failsafeDuration expires     => AUTONOMOUS, clear effective limits
//
// FAILSAFE is treated as device-wide rather than per-zone-slot: the corpus
// (TC-FAIL-008) requires partial loss to NOT enter FAILSAFE while another
// authoritative zone remains. Spec §5.2 describes per-slot semantics; the
// divergence is intentional in this round and tracked in conformance notes.

// authoritativeConnectedCountLocked counts zones that are connected and not TEST.
// Caller must hold s.mu (read or write).
func (s *DeviceService) authoritativeConnectedCountLocked() int {
	n := 0
	for _, cz := range s.connectedZones {
		if cz == nil || !cz.Connected {
			continue
		}
		if cz.Type == cert.ZoneTypeTest {
			continue
		}
		n++
	}
	return n
}

// applyFailsafeEffects sets ControlState=FAILSAFE and writes the configured
// failsafe limits as the effective limits across every endpoint that
// exposes EnergyControl. Must NOT be called with s.mu held — attribute
// writes fire notifyAttributeChanged → notifyZoneSessions which acquires
// s.mu.RLock().
//
// Returns the configured failsafeDuration (for the countdown) and whether
// any EnergyControl feature was found.
func (s *DeviceService) applyFailsafeEffects() (time.Duration, bool) {
	if s.device == nil {
		return 0, false
	}

	var dur time.Duration
	found := false

	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl))
		if err != nil {
			continue
		}
		found = true

		// Read configured failsafe limits (may be unset/nil).
		var consVal any
		if v, err := feat.ReadAttribute(features.EnergyControlAttrFailsafeConsumptionLimit); err == nil {
			consVal = v
		}
		var prodVal any
		if v, err := feat.ReadAttribute(features.EnergyControlAttrFailsafeProductionLimit); err == nil {
			prodVal = v
		}

		_ = feat.SetAttributeInternal(features.EnergyControlAttrEffectiveConsumptionLimit, consVal)
		_ = feat.SetAttributeInternal(features.EnergyControlAttrEffectiveProductionLimit, prodVal)
		_ = feat.SetAttributeInternal(features.EnergyControlAttrControlState,
			uint8(features.ControlStateFailsafe))

		if dur == 0 {
			if v, err := feat.ReadAttribute(features.EnergyControlAttrFailsafeDuration); err == nil {
				if d, ok := v.(uint32); ok && d > 0 {
					dur = time.Duration(d) * time.Second
				}
			}
		}
	}

	if dur == 0 {
		dur = s.config.FailsafeTimeout
	}
	if dur == 0 {
		dur = failsafe.DefaultDuration
	}
	return dur, found
}

// clearFailsafeEffects drops the failsafe effective limits and forces
// ControlState off FAILSAFE. Caller chooses the next state (CONTROLLED on
// reconnect, AUTONOMOUS on countdown expiry). Must NOT be called with s.mu
// held — see applyFailsafeEffects for the same notification-fanout caveat.
func (s *DeviceService) clearFailsafeEffects(next features.ControlState) {
	if s.device == nil {
		return
	}
	for _, ep := range s.device.Endpoints() {
		feat, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl))
		if err != nil {
			continue
		}
		// Bypass the resolver's FAILSAFE-preserving ratchet by writing the
		// state directly. A subsequent resolveAndApply (via Reapply) may
		// upgrade to LIMITED if commanded limits persisted.
		_ = feat.SetAttributeInternal(features.EnergyControlAttrEffectiveConsumptionLimit, nil)
		_ = feat.SetAttributeInternal(features.EnergyControlAttrEffectiveProductionLimit, nil)
		_ = feat.SetAttributeInternal(features.EnergyControlAttrControlState, uint8(next))
	}
}

// startFailsafeCountdownLocked schedules the FAILSAFE -> AUTONOMOUS
// transition. If a previous countdown is running, it is cancelled first.
// Caller must hold s.mu.
func (s *DeviceService) startFailsafeCountdownLocked(dur time.Duration) {
	if s.failsafeCountdown != nil {
		s.failsafeCountdown.Stop()
	}
	s.failsafeCountdown = time.AfterFunc(dur, s.onFailsafeCountdownExpiry)
	s.failsafeCountdownStartedAt = time.Now()
}

// stopFailsafeCountdownLocked cancels an in-progress countdown if any.
// Caller must hold s.mu.
func (s *DeviceService) stopFailsafeCountdownLocked() {
	if s.failsafeCountdown != nil {
		s.failsafeCountdown.Stop()
		s.failsafeCountdown = nil
	}
	s.failsafeCountdownStartedAt = time.Time{}
}

// onFailsafeCountdownExpiry runs in its own goroutine when the countdown
// reaches zero. The device transitions FAILSAFE -> AUTONOMOUS.
func (s *DeviceService) onFailsafeCountdownExpiry() {
	s.mu.Lock()
	// Guard against late-firing timers: only act if we're still in
	// FAILSAFE. A reconnect or ResetAll may have cleared us already.
	if !s.failsafeActive {
		s.mu.Unlock()
		return
	}
	s.failsafeActive = false
	s.failsafeCountdown = nil
	s.failsafeCountdownStartedAt = time.Time{}
	for _, cz := range s.connectedZones {
		if cz != nil {
			cz.FailsafeActive = false
		}
	}
	s.mu.Unlock()

	// clearFailsafeEffects writes attributes which fan out to subscribers
	// via notifyZoneSessions (which RLocks s.mu) — do it outside the lock.
	s.clearFailsafeEffects(features.ControlStateAutonomous)

	s.emitEvent(Event{Type: EventFailsafeCleared})
}

// handleFailsafe enters FAILSAFE for the device. Called from
// HandleZoneDisconnect (when no other authoritative zone remains) and from
// the simulate_failsafe_timeout test trigger. The zoneID argument names the
// triggering zone for diagnostics; FAILSAFE itself is device-wide.
//
// If FAILSAFE is already active, this is a no-op: a subsequent zone loss
// while still in FAILSAFE must not restart the countdown. (TC-FAIL-005:
// precondition sets short duration, single disconnect triggers FAILSAFE,
// the countdown must run to completion regardless of teardown churn.)
func (s *DeviceService) handleFailsafe(zoneID string) {
	s.mu.Lock()

	if cz, exists := s.connectedZones[zoneID]; exists {
		cz.FailsafeActive = true
	}

	if s.failsafeActive {
		s.mu.Unlock()
		s.emitEvent(Event{
			Type:   EventFailsafeTriggered,
			ZoneID: zoneID,
		})
		return
	}

	zoneIndex, hasIdx := s.zoneIndexMap[zoneID]
	s.mu.Unlock()

	if hasIdx {
		s.durationManager.CancelZoneTimers(zoneIndex)
	}

	// Apply effects (writes attributes -> fires subscriber callbacks that
	// take s.mu.RLock) outside the lock to avoid self-deadlock.
	dur, _ := s.applyFailsafeEffects()

	s.mu.Lock()
	// F-36-C1: test backdoor overrides the attribute-derived duration so
	// conformance tests can exercise the FAILSAFE -> AUTONOMOUS transition
	// without waiting the DEC-070 minimum of 2 hours.
	if s.failsafeDurationOverride > 0 {
		dur = s.failsafeDurationOverride
	}
	s.failsafeActive = true
	s.startFailsafeCountdownLocked(dur)
	s.mu.Unlock()

	s.emitEvent(Event{
		Type:   EventFailsafeTriggered,
		ZoneID: zoneID,
	})
}

// exitFailsafe is called when an authoritative zone reconnects. Cancels the
// countdown, drops failsafe effective limits, and lets the LimitResolver
// re-resolve from per-zone state (which may upgrade to LIMITED).
func (s *DeviceService) exitFailsafe(zoneID string) {
	s.mu.Lock()
	if !s.failsafeActive {
		s.mu.Unlock()
		return
	}
	s.failsafeActive = false
	s.stopFailsafeCountdownLocked()
	for _, cz := range s.connectedZones {
		if cz != nil {
			cz.FailsafeActive = false
		}
	}
	lr := s.limitResolver
	s.mu.Unlock()

	// Both clearFailsafeEffects and the resolver's Reapply path can call
	// back into this service via subscriber callbacks; they must run
	// outside s.mu.
	s.clearFailsafeEffects(features.ControlStateControlled)
	if lr != nil {
		lr.Reapply()
	}

	s.emitEvent(Event{
		Type:   EventFailsafeCleared,
		ZoneID: zoneID,
	})
}

// handleDurationExpiry handles a duration timer expiry.
func (s *DeviceService) handleDurationExpiry(zoneIndex uint8, _ duration.CommandType, _ any) {
	s.mu.RLock()

	// Find zone by index
	var zoneID string
	for id, idx := range s.zoneIndexMap {
		if idx == zoneIndex {
			zoneID = id
			break
		}
	}

	s.mu.RUnlock()

	// Clear the value in the device model
	// This would trigger recalculation of effective values
	// TODO: Implement value clearing based on cmdType

	s.emitEvent(Event{
		Type:   EventValueChanged,
		ZoneID: zoneID,
		Value:  nil, // Cleared
	})
}

// RefreshFailsafe refreshes the failsafe timer for a zone.
func (s *DeviceService) RefreshFailsafe(zoneID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cz, exists := s.connectedZones[zoneID]
	if !exists {
		return ErrDeviceNotFound
	}

	timer, hasTimer := s.failsafeTimers[zoneID]
	if !hasTimer {
		return ErrDeviceNotFound
	}

	if cz.FailsafeActive {
		// Clear failsafe state
		cz.FailsafeActive = false
		timer.Stop() // This will enter grace period

		s.emitEvent(Event{
			Type:   EventFailsafeCleared,
			ZoneID: zoneID,
		})
	}

	// Restart the timer
	timer.Reset()
	timer.Start()

	return nil
}

// SetDurationTimer sets a duration timer for a command.
func (s *DeviceService) SetDurationTimer(zoneID string, cmdType duration.CommandType, dur time.Duration, value any) error {
	s.mu.RLock()
	_, exists := s.connectedZones[zoneID]
	zoneIndex, hasIndex := s.zoneIndexMap[zoneID]
	s.mu.RUnlock()

	if !exists || !hasIndex {
		return ErrDeviceNotFound
	}

	return s.durationManager.SetTimer(zoneIndex, cmdType, dur, value)
}

// CancelDurationTimer cancels a duration timer.
func (s *DeviceService) CancelDurationTimer(zoneID string, cmdType duration.CommandType) error {
	s.mu.RLock()
	zoneIndex, exists := s.zoneIndexMap[zoneID]
	s.mu.RUnlock()

	if !exists {
		return ErrDeviceNotFound
	}

	return s.durationManager.CancelTimer(zoneIndex, cmdType)
}

// SetFailsafeTimer sets a failsafe timer for a zone.
func (s *DeviceService) SetFailsafeTimer(zoneID string, timer *failsafe.Timer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop existing timer if any
	if existing, ok := s.failsafeTimers[zoneID]; ok {
		existing.Reset()
	}

	// Set up callback
	timer.OnFailsafeEnter(func(_ failsafe.Limits) {
		s.handleFailsafe(zoneID)
	})

	s.failsafeTimers[zoneID] = timer
}

// GetFailsafeTimer returns the failsafe timer for a zone (for testing).
func (s *DeviceService) GetFailsafeTimer(zoneID string) *failsafe.Timer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.failsafeTimers[zoneID]
}
