package features

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/duration"
	"github.com/mash-protocol/mash-go/pkg/wire"
	"github.com/mash-protocol/mash-go/pkg/zone"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// LimitResolver tracks per-zone limits and resolves the effective limit
// using "most restrictive wins" semantics. It manages duration timers
// per zone and updates the EnergyControl feature attributes when limits change.
//
// Zone identity is extracted from context via pkg/zonecontext.
type LimitResolver struct {
	mu sync.Mutex

	ec *EnergyControl

	consumptionLimits *zone.MultiZoneValue
	productionLimits  *zone.MultiZoneValue

	timers       *duration.Manager
	zoneIndexMap map[string]uint8
	indexZoneMap map[uint8]string
	nextIndex    uint8

	// MaxConsumption is the device's nominal maximum consumption power (mW).
	// If > 0, SetLimit rejects consumptionLimit values above this threshold
	// with StatusConstraintError (following Matter's fail-fast pattern).
	MaxConsumption int64
	// MaxProduction is the device's nominal maximum production power (mW).
	// If > 0, SetLimit rejects productionLimit values above this threshold.
	MaxProduction int64

	// OnZoneMyChange is called when a zone's "my" attribute values change.
	// The callback receives the zone ID and a map of changed attribute IDs to values.
	// Injected by the service layer to avoid import cycles.
	OnZoneMyChange func(zoneID string, changes map[uint16]any)
}

// NewLimitResolver creates a new LimitResolver for the given EnergyControl feature.
func NewLimitResolver(ec *EnergyControl) *LimitResolver {
	lr := &LimitResolver{
		ec:                ec,
		consumptionLimits: zone.NewMultiZoneValue(),
		productionLimits:  zone.NewMultiZoneValue(),
		timers:            duration.NewManager(),
		zoneIndexMap:      make(map[string]uint8),
		indexZoneMap:      make(map[uint8]string),
	}

	lr.timers.OnExpiry(lr.handleTimerExpiry)

	return lr
}

// Register wires the resolver's handlers into the EnergyControl feature.
func (lr *LimitResolver) Register() {
	lr.ec.OnSetLimit(lr.HandleSetLimit)
	lr.ec.OnClearLimit(lr.HandleClearLimit)

	lr.ec.SetReadHook(func(ctx context.Context, attrID uint16) (any, bool) {
		switch attrID {
		case EnergyControlAttrMyConsumptionLimit, EnergyControlAttrMyProductionLimit:
			// Intercept per-zone "my" attributes
		default:
			return nil, false
		}

		zoneID := zonecontext.CallerZoneIDFromContext(ctx)
		if zoneID == "" {
			return nil, true
		}

		lr.mu.Lock()
		defer lr.mu.Unlock()

		var mzv *zone.MultiZoneValue
		if attrID == EnergyControlAttrMyConsumptionLimit {
			mzv = lr.consumptionLimits
		} else {
			mzv = lr.productionLimits
		}

		zv := mzv.Get(zoneID)
		if zv == nil {
			return nil, true
		}
		return zv.Value, true
	})
}

// HandleSetLimit handles a SetLimit command from a zone.
func (lr *LimitResolver) HandleSetLimit(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	// Extract zone identity from context
	zoneID := zonecontext.CallerZoneIDFromContext(ctx)
	if zoneID == "" {
		reason := LimitRejectReasonInvalidValue
		return SetLimitResponse{
			Applied:      false,
			RejectReason: &reason,
			ControlState: lr.ec.ControlState(),
		}, nil
	}

	zoneType := zonecontext.CallerZoneTypeFromContext(ctx)

	// Validate negative values
	if req.ConsumptionLimit != nil && *req.ConsumptionLimit < 0 {
		reason := LimitRejectReasonInvalidValue
		return SetLimitResponse{
			Applied:      false,
			RejectReason: &reason,
			ControlState: lr.ec.ControlState(),
		}, nil
	}
	if req.ProductionLimit != nil && *req.ProductionLimit < 0 {
		reason := LimitRejectReasonInvalidValue
		return SetLimitResponse{
			Applied:      false,
			RejectReason: &reason,
			ControlState: lr.ec.ControlState(),
		}, nil
	}

	// Validate against device capacity constraints (fail-fast, before state changes).
	if req.ConsumptionLimit != nil && lr.MaxConsumption > 0 && *req.ConsumptionLimit > lr.MaxConsumption {
		return SetLimitResponse{}, &wire.CommandError{
			Status:  wire.StatusConstraintError,
			Message: fmt.Sprintf("consumptionLimit %d mW exceeds device maximum %d mW", *req.ConsumptionLimit, lr.MaxConsumption),
		}
	}
	if req.ProductionLimit != nil && lr.MaxProduction > 0 && *req.ProductionLimit > lr.MaxProduction {
		return SetLimitResponse{}, &wire.CommandError{
			Status:  wire.StatusConstraintError,
			Message: fmt.Sprintf("productionLimit %d mW exceeds device maximum %d mW", *req.ProductionLimit, lr.MaxProduction),
		}
	}

	// Check override state
	if lr.ec.IsOverride() {
		reason := LimitRejectReasonDeviceOverride
		return SetLimitResponse{
			Applied:      false,
			RejectReason: &reason,
			ControlState: ControlStateOverride,
		}, nil
	}

	// Both nil = deactivate this zone's limits
	if req.ConsumptionLimit == nil && req.ProductionLimit == nil {
		lr.clearZoneLocked(zoneID)
		lr.resolveAndApply()
		if lr.OnZoneMyChange != nil {
			lr.OnZoneMyChange(zoneID, map[uint16]any{
				EnergyControlAttrMyConsumptionLimit: nil,
				EnergyControlAttrMyProductionLimit:  nil,
			})
		}
		return SetLimitResponse{
			Applied:      true,
			ControlState: lr.ec.ControlState(),
		}, nil
	}

	// Ensure zone has an index for duration timers
	zoneIdx := lr.ensureZoneIndex(zoneID)

	// Compute duration
	var dur time.Duration
	if req.Duration != nil && *req.Duration > 0 {
		dur = time.Duration(*req.Duration) * time.Second
	}

	// Store per-zone values
	if req.ConsumptionLimit != nil {
		lr.consumptionLimits.Set(zoneID, zoneType, *req.ConsumptionLimit, dur)
		if dur > 0 {
			_ = lr.timers.SetTimer(zoneIdx, duration.CmdLimitConsumption, dur, *req.ConsumptionLimit)
		} else {
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitConsumption)
		}
	}
	if req.ProductionLimit != nil {
		lr.productionLimits.Set(zoneID, zoneType, *req.ProductionLimit, dur)
		if dur > 0 {
			_ = lr.timers.SetTimer(zoneIdx, duration.CmdLimitProduction, dur, *req.ProductionLimit)
		} else {
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitProduction)
		}
	}

	lr.resolveAndApply()

	if lr.OnZoneMyChange != nil {
		changes := make(map[uint16]any)
		if req.ConsumptionLimit != nil {
			changes[EnergyControlAttrMyConsumptionLimit] = *req.ConsumptionLimit
		}
		if req.ProductionLimit != nil {
			changes[EnergyControlAttrMyProductionLimit] = *req.ProductionLimit
		}
		lr.OnZoneMyChange(zoneID, changes)
	}

	// Build response with effective values
	resp := SetLimitResponse{
		Applied:      true,
		ControlState: lr.ec.ControlState(),
	}
	if effC, ok := lr.ec.EffectiveConsumptionLimit(); ok {
		resp.EffectiveConsumptionLimit = &effC
	}
	if effP, ok := lr.ec.EffectiveProductionLimit(); ok {
		resp.EffectiveProductionLimit = &effP
	}

	if req.Duration != nil && *req.Duration > 0 {
		log.Printf("[LIMIT] Zone %s set limit with duration: %ds", zoneID, *req.Duration)
	}

	return resp, nil
}

// HandleClearLimit handles a ClearLimit command from a zone.
func (lr *LimitResolver) HandleClearLimit(ctx context.Context, req ClearLimitRequest) error {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	zoneID := zonecontext.CallerZoneIDFromContext(ctx)
	if zoneID == "" {
		return nil
	}

	zoneIdx, hasIdx := lr.zoneIndexMap[zoneID]

	if req.Direction == nil {
		// Clear both directions
		lr.consumptionLimits.Clear(zoneID)
		lr.productionLimits.Clear(zoneID)
		if hasIdx {
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitConsumption)
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitProduction)
		}
	} else if *req.Direction == DirectionConsumption {
		lr.consumptionLimits.Clear(zoneID)
		if hasIdx {
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitConsumption)
		}
	} else if *req.Direction == DirectionProduction {
		lr.productionLimits.Clear(zoneID)
		if hasIdx {
			_ = lr.timers.CancelTimer(zoneIdx, duration.CmdLimitProduction)
		}
	}

	lr.resolveAndApply()

	if lr.OnZoneMyChange != nil {
		changes := make(map[uint16]any)
		if req.Direction == nil || *req.Direction == DirectionConsumption {
			changes[EnergyControlAttrMyConsumptionLimit] = nil
		}
		if req.Direction == nil || *req.Direction == DirectionProduction {
			changes[EnergyControlAttrMyProductionLimit] = nil
		}
		lr.OnZoneMyChange(zoneID, changes)
	}

	return nil
}

// ResetAll clears all zone limits, cancels all timers, and resets the
// EnergyControl feature to AUTONOMOUS. This is used by TriggerResetTestState
// to prevent stale timers from prior tests from firing during subsequent tests.
func (lr *LimitResolver) ResetAll() {
	lr.mu.Lock()

	// Cancel all timers for all known zones.
	for _, zoneIdx := range lr.zoneIndexMap {
		lr.timers.CancelZoneTimers(zoneIdx)
	}

	// Collect zone IDs that had limits so we can fire callbacks.
	var zonesWithLimits []string
	for zoneID := range lr.zoneIndexMap {
		zonesWithLimits = append(zonesWithLimits, zoneID)
	}

	// Replace limit maps with fresh instances (clear all zone values).
	lr.consumptionLimits = zone.NewMultiZoneValue()
	lr.productionLimits = zone.NewMultiZoneValue()

	// Force back to AUTONOMOUS before re-resolving. The default
	// resolveAndApply rule preserves CONTROLLED across a transient
	// ClearAll, but ResetAll's contract is "fresh device" -- callers
	// (TriggerResetTestState, factory reset) want AUTONOMOUS.
	_ = lr.ec.SetControlState(ControlStateAutonomous)

	// Re-resolve: sets effective limits to nil, leaves state AUTONOMOUS.
	lr.resolveAndApply()

	lr.mu.Unlock()

	// Fire callbacks outside the lock to avoid deadlocks.
	if lr.OnZoneMyChange != nil {
		for _, zoneID := range zonesWithLimits {
			lr.OnZoneMyChange(zoneID, map[uint16]any{
				EnergyControlAttrMyConsumptionLimit: nil,
				EnergyControlAttrMyProductionLimit:  nil,
			})
		}
	}
}

// ClearZone removes all limits for a zone (e.g., on disconnect/failsafe).
func (lr *LimitResolver) ClearZone(zoneID string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	lr.clearZoneLocked(zoneID)
	lr.resolveAndApply()

	if lr.OnZoneMyChange != nil {
		lr.OnZoneMyChange(zoneID, map[uint16]any{
			EnergyControlAttrMyConsumptionLimit: nil,
			EnergyControlAttrMyProductionLimit:  nil,
		})
	}
}

// clearZoneLocked clears a zone's limits and timers. Must be called with mu held.
func (lr *LimitResolver) clearZoneLocked(zoneID string) {
	lr.consumptionLimits.Clear(zoneID)
	lr.productionLimits.Clear(zoneID)

	if zoneIdx, ok := lr.zoneIndexMap[zoneID]; ok {
		lr.timers.CancelZoneTimers(zoneIdx)
	}
}

// handleTimerExpiry is called by the duration manager when a timer expires.
func (lr *LimitResolver) handleTimerExpiry(zoneIdx uint8, cmdType duration.CommandType, _ any) {
	lr.mu.Lock()
	defer lr.mu.Unlock()

	zoneID, ok := lr.indexZoneMap[zoneIdx]
	if !ok {
		return
	}

	switch cmdType {
	case duration.CmdLimitConsumption:
		lr.consumptionLimits.Clear(zoneID)
		log.Printf("[LIMIT] Zone %s consumption limit expired", zoneID)
	case duration.CmdLimitProduction:
		lr.productionLimits.Clear(zoneID)
		log.Printf("[LIMIT] Zone %s production limit expired", zoneID)
	}

	lr.resolveAndApply()

	if lr.OnZoneMyChange != nil {
		changes := make(map[uint16]any)
		switch cmdType {
		case duration.CmdLimitConsumption:
			changes[EnergyControlAttrMyConsumptionLimit] = nil
		case duration.CmdLimitProduction:
			changes[EnergyControlAttrMyProductionLimit] = nil
		}
		lr.OnZoneMyChange(zoneID, changes)
	}
}

// resolveAndApply computes effective limits and updates the EnergyControl feature.
// Must be called with mu held.
//
// State transitions follow the ControlStateEnum diagram in
// docs/features/energy-control.md:
//
//	AUTONOMOUS --controller connects--> CONTROLLED --SetLimit()--> LIMITED
//	                                       ^---ClearLimit/expires----┘
//
//   - LIMITED: an effective limit is currently being applied.
//   - CONTROLLED: controller authority is established (the resolver was
//     just in LIMITED) but no active limit remains.
//   - AUTONOMOUS: no controller authority has been exercised yet.
//
// FAILSAFE and OVERRIDE are entered/exited by paths outside the limit
// resolver (failsafe timer, device-side safety override) and are not
// modified here.
func (lr *LimitResolver) resolveAndApply() {
	effConsumption, _ := lr.consumptionLimits.ResolveLimits()
	effProduction, _ := lr.productionLimits.ResolveLimits()

	_ = lr.ec.SetEffectiveConsumptionLimitPtr(effConsumption)
	_ = lr.ec.SetEffectiveProductionLimitPtr(effProduction)

	prev := lr.ec.ControlState()
	if prev == ControlStateFailsafe || prev == ControlStateOverride {
		return
	}

	hasLimits := effConsumption != nil || effProduction != nil
	switch {
	case hasLimits:
		_ = lr.ec.SetControlState(ControlStateLimited)
	case prev == ControlStateLimited:
		_ = lr.ec.SetControlState(ControlStateControlled)
	case prev == ControlStateControlled:
		// stay CONTROLLED -- controller still attached, no active limit.
	default:
		_ = lr.ec.SetControlState(ControlStateAutonomous)
	}
}

// ensureZoneIndex returns a uint8 index for the zone, creating one if needed.
// Must be called with mu held.
func (lr *LimitResolver) ensureZoneIndex(zoneID string) uint8 {
	if idx, ok := lr.zoneIndexMap[zoneID]; ok {
		return idx
	}
	idx := lr.nextIndex
	lr.zoneIndexMap[zoneID] = idx
	lr.indexZoneMap[idx] = zoneID
	lr.nextIndex++
	return idx
}
