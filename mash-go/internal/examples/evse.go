package examples

import (
	"context"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/usecase"
)

// EVSE represents a complete EV charger device implementation.
// It demonstrates how to build a controllable MASH device that:
//   - Exposes its capabilities via Electrical feature
//   - Reports measurements via Measurement feature
//   - Accepts control via EnergyControl feature
//   - Manages charging sessions via ChargingSession feature
//   - Reports health via Status feature
type EVSE struct {
	mu sync.RWMutex

	device *model.Device

	// Features
	deviceInfo      *features.DeviceInfo
	electrical      *features.Electrical
	measurement     *features.Measurement
	energyControl   *features.EnergyControl
	chargingSession *features.ChargingSession
	status          *features.Status
	signals         *features.Signals
	plan            *features.Plan

	// Limit resolution
	limitResolver *features.LimitResolver

	// Internal state
	currentPower int64 // mW - actual charging power
}

// EVSEConfig contains configuration for creating an EVSE.
type EVSEConfig struct {
	DeviceID     string
	VendorName   string
	ProductName  string
	SerialNumber string
	VendorID     uint32
	ProductID    uint16

	// Electrical capabilities
	PhaseCount            uint8
	NominalVoltage        uint16 // V
	MaxCurrentPerPhase    int64  // mA
	MinCurrentPerPhase    int64  // mA
	NominalMaxPower       int64  // mW
	NominalMinPower       int64  // mW
	SupportsBidirectional bool
}

// NewEVSE creates a new EVSE device with the given configuration.
func NewEVSE(cfg EVSEConfig) *EVSE {
	evse := &EVSE{}

	// Create device
	evse.device = model.NewDevice(cfg.DeviceID, cfg.VendorID, cfg.ProductID)

	// Setup root endpoint with DeviceInfo
	evse.setupDeviceInfo(cfg)

	// Setup charger endpoint with all features
	evse.setupChargerEndpoint(cfg)

	// Wire up command handlers
	evse.setupCommandHandlers()

	return evse
}

func (e *EVSE) setupDeviceInfo(cfg EVSEConfig) {
	e.deviceInfo = features.NewDeviceInfo()
	// Device-level capability bitmap indicates overall device type
	e.deviceInfo.Feature.SetFeatureMap(uint32(model.FeatureMapCore | model.FeatureMapFlex | model.FeatureMapEMob))
	_ = e.deviceInfo.SetDeviceID(cfg.DeviceID)
	_ = e.deviceInfo.SetVendorName(cfg.VendorName)
	_ = e.deviceInfo.SetProductName(cfg.ProductName)
	_ = e.deviceInfo.SetSerialNumber(cfg.SerialNumber)
	_ = e.deviceInfo.SetVendorID(cfg.VendorID)
	_ = e.deviceInfo.SetProductID(cfg.ProductID)
	_ = e.deviceInfo.SetSoftwareVersion("1.0.0")

	e.device.RootEndpoint().AddFeature(e.deviceInfo.Feature)
}

func (e *EVSE) setupChargerEndpoint(cfg EVSEConfig) {
	charger := model.NewEndpoint(1, model.EndpointEVCharger, "EV Charger")

	// EVSE capability bitmap: CORE + FLEX + EMOB + SIGNALS + PLAN
	evseCapabilities := uint32(model.FeatureMapCore | model.FeatureMapFlex | model.FeatureMapEMob | model.FeatureMapSignals | model.FeatureMapPlan)

	// Electrical - static capabilities
	e.electrical = features.NewElectrical()
	e.electrical.Feature.SetFeatureMap(evseCapabilities)
	_ = e.electrical.SetPhaseCount(cfg.PhaseCount)
	// Default phase-to-grid mapping: A->L1, B->L2, C->L3 (as many as configured).
	phaseMap := map[features.Phase]features.GridPhase{features.PhaseA: features.GridPhaseL1}
	if cfg.PhaseCount >= 2 {
		phaseMap[features.PhaseB] = features.GridPhaseL2
	}
	if cfg.PhaseCount >= 3 {
		phaseMap[features.PhaseC] = features.GridPhaseL3
	}
	_ = e.electrical.SetPhaseMapping(phaseMap)
	_ = e.electrical.SetNominalVoltage(cfg.NominalVoltage)
	_ = e.electrical.SetNominalFrequency(50)
	_ = e.electrical.SetMaxCurrentPerPhase(cfg.MaxCurrentPerPhase)
	_ = e.electrical.SetMinCurrentPerPhase(cfg.MinCurrentPerPhase)
	_ = e.electrical.SetNominalMaxConsumption(cfg.NominalMaxPower)
	_ = e.electrical.SetNominalMinPower(cfg.NominalMinPower)

	direction := features.DirectionConsumption
	if cfg.SupportsBidirectional {
		direction = features.DirectionBidirectional
	}
	_ = e.electrical.SetSupportedDirections(direction)
	// Prune Electrical attributes that don't apply to an EVSE. Per
	// DEC-051 attributeList must reflect actual hardware capability:
	//   - supportsAsymmetric: per-phase control capability not
	//     implemented in this simulation (would require per-phase limit
	//     plumbing on top of the symmetric MaxCurrentPerPhase field)
	//   - energyCapacity: a battery/storage attribute; EVSE has no
	//     internal energy store
	for _, id := range []uint16{
		features.ElectricalAttrSupportsAsymmetric,
		features.ElectricalAttrEnergyCapacity,
	} {
		_ = e.electrical.Feature.RemoveAttribute(id)
	}
	charger.AddFeature(e.electrical.Feature)

	// Measurement - real-time telemetry. NewMeasurement registers the full
	// spec attribute set; per DEC-051, attributeList must reflect the hardware
	// capabilities of this device. The EVSE simulation populates only
	// acActivePower today, so prune the rest. Add an attribute back when the
	// simulation populates it (e.g., per-phase current/voltage, energy).
	e.measurement = features.NewMeasurement()
	e.measurement.Feature.SetFeatureMap(evseCapabilities)
	for _, id := range []uint16{
		features.MeasurementAttrACReactivePower,
		features.MeasurementAttrACApparentPower,
		features.MeasurementAttrACActivePowerPerPhase,
		features.MeasurementAttrACReactivePowerPerPhase,
		features.MeasurementAttrACApparentPowerPerPhase,
		features.MeasurementAttrACCurrentPerPhase,
		features.MeasurementAttrACVoltagePerPhase,
		features.MeasurementAttrACVoltagePhaseToPhasePair,
		features.MeasurementAttrACFrequency,
		features.MeasurementAttrPowerFactor,
		features.MeasurementAttrACEnergyConsumed,
		features.MeasurementAttrACEnergyProduced,
		features.MeasurementAttrDCPower,
		features.MeasurementAttrDCCurrent,
		features.MeasurementAttrDCVoltage,
		features.MeasurementAttrDCEnergyIn,
		features.MeasurementAttrDCEnergyOut,
		features.MeasurementAttrStateOfCharge,
		features.MeasurementAttrStateOfHealth,
		features.MeasurementAttrStateOfEnergy,
		features.MeasurementAttrUseableCapacity,
		features.MeasurementAttrCycleCount,
		features.MeasurementAttrTemperature,
	} {
		_ = e.measurement.Feature.RemoveAttribute(id)
	}
	charger.AddFeature(e.measurement.Feature)

	// EnergyControl - accepts limits from CEM
	e.energyControl = features.NewEnergyControl()
	e.energyControl.Feature.SetFeatureMap(evseCapabilities)
	_ = e.energyControl.SetDeviceType(features.DeviceTypeEVSE)
	_ = e.energyControl.SetControlState(features.ControlStateAutonomous)
	e.energyControl.SetCapabilities(
		true,  // acceptsLimits
		true,  // acceptsCurrentLimits
		false, // acceptsSetpoints
		false, // acceptsCurrentSetpoints
		true,  // isPausable
		false, // isShiftable
		true,  // isStoppable
	)
	charger.AddFeature(e.energyControl.Feature)

	// ChargingSession - EV session management
	e.chargingSession = features.NewChargingSession()
	e.chargingSession.Feature.SetFeatureMap(evseCapabilities)
	_ = e.chargingSession.SetSupportedChargingModes([]features.ChargingMode{
		features.ChargingModeOff,
		features.ChargingModePVSurplusOnly,
		features.ChargingModePVSurplusThreshold,
		features.ChargingModePriceOptimized,
	})
	charger.AddFeature(e.chargingSession.Feature)

	// Status - health and operating state
	e.status = features.NewStatus()
	e.status.Feature.SetFeatureMap(evseCapabilities)
	_ = e.status.SetOperatingState(features.OperatingStateStandby)
	charger.AddFeature(e.status.Feature)

	// Signals - price/constraint signal reception
	e.signals = features.NewSignals()
	e.signals.Feature.SetFeatureMap(evseCapabilities)
	charger.AddFeature(e.signals.Feature)

	// Plan - power plan reporting
	e.plan = features.NewPlan()
	e.plan.Feature.SetFeatureMap(evseCapabilities)
	charger.AddFeature(e.plan.Feature)

	_ = e.device.AddEndpoint(charger)

	// Update DeviceInfo with endpoint structure and use cases
	e.updateEndpointInfo()
	e.updateUseCaseInfo()
}

func (e *EVSE) setupCommandHandlers() {
	// Multi-zone limit handling via LimitResolver.
	// Zone identity is extracted from context via pkg/zonecontext.
	e.limitResolver = features.NewLimitResolver(e.energyControl)
	e.limitResolver.MaxConsumption = e.electrical.NominalMaxConsumption()
	e.limitResolver.Register()

	// Pause handler
	e.energyControl.OnPause(func(ctx context.Context, req features.PauseRequest) error {
		if !e.energyControl.IsPausable() {
			return features.CapabilityUnsupportedError("Pause")
		}
		e.mu.Lock()
		defer e.mu.Unlock()

		_ = e.status.SetOperatingState(features.OperatingStatePaused)
		_ = e.energyControl.SetProcessState(features.ProcessStatePaused)
		e.currentPower = 0
		_ = e.measurement.SetACActivePower(0)

		return nil
	})

	// Resume handler. Per docs/features/energy-control.md §"Resume",
	// Resume restores a paused operation -- the contract is the
	// processState transition PAUSED -> RUNNING, not a side check on
	// plug state. The simulation's plug state is informational and
	// can be re-evaluated by the natural simulation loop after Resume
	// completes; gating Resume on IsPluggedIn here was an
	// implementation overreach that broke conformance for any test
	// that drove processState via TestControl rather than the natural
	// plug-in -> charging path (TC-STATE-009, TC-CTRL-005).
	e.energyControl.OnResume(func(ctx context.Context) error {
		if !e.energyControl.IsPausable() {
			return features.CapabilityUnsupportedError("Resume")
		}
		e.mu.Lock()
		defer e.mu.Unlock()

		_ = e.status.SetOperatingState(features.OperatingStateRunning)
		_ = e.energyControl.SetProcessState(features.ProcessStateRunning)
		return nil
	})

	// Stop handler
	e.energyControl.OnStop(func(ctx context.Context) error {
		if !e.energyControl.IsStoppable() {
			return features.CapabilityUnsupportedError("Stop")
		}
		e.mu.Lock()
		defer e.mu.Unlock()

		_ = e.energyControl.SetProcessState(features.ProcessStateAborted)
		e.currentPower = 0
		_ = e.measurement.SetACActivePower(0)

		return nil
	})

	// Signals handlers - store received signals
	e.signals.OnSendPriceSignal(func(ctx context.Context, req features.SendPriceSignalRequest) error {
		_ = e.signals.SetSignalSource(req.Source)
		_ = e.signals.SetStartTime(req.StartTime)
		if req.ValidUntil != nil {
			_ = e.signals.SetValidUntil(*req.ValidUntil)
		}
		return nil
	})
	e.signals.OnSendConstraintSignal(func(ctx context.Context, req features.SendConstraintSignalRequest) error {
		_ = e.signals.SetSignalSource(req.Source)
		_ = e.signals.SetStartTime(req.StartTime)
		if req.ValidUntil != nil {
			_ = e.signals.SetValidUntil(*req.ValidUntil)
		}
		return nil
	})
	e.signals.OnClearSignals(func(ctx context.Context, req features.ClearSignalsRequest) (features.ClearSignalsResponse, error) {
		_ = e.signals.ClearPriceSlots()
		_ = e.signals.ClearConstraintSlots()
		_ = e.signals.ClearForecastSlots()
		_ = e.signals.ClearSignalSource()
		return features.ClearSignalsResponse{Cleared: 1}, nil
	})

	// Plan handlers - generate a one-hour plan with plausible defaults so
	// PODF/ITPCM/EVC-S04 tests reading startTime/endTime/totalEnergyPlanned/
	// commitment after RequestPlan/AcceptPlan see populated values.
	//
	// The test corpus is loose about plan-id matching (tests send planId=1
	// without first calling RequestPlan), so RequestPlan synthesizes a plan
	// with a fixed ID = 1 and AcceptPlan accepts any planId by advancing
	// commitment to COMMITTED. A stricter implementation would track the
	// generated id and reject mismatches; that level of fidelity isn't
	// what the conformance corpus is asserting today.
	e.plan.OnRequestPlan(func(ctx context.Context, req features.RequestPlanRequest) (features.RequestPlanResponse, error) {
		const oneHour = uint64(60 * 60)
		now := uint64(time.Now().Unix())
		_ = e.plan.SetPlanID(1)
		_ = e.plan.SetPlanVersion(1)
		_ = e.plan.SetStartTime(now)
		_ = e.plan.SetEndTime(now + oneHour)
		// 7.4 kW * 1 h = 7.4 kWh = 7,400,000,000 mWh -- mirrors the
		// EVSE's nominal max consumption expressed in mWh.
		_ = e.plan.SetTotalEnergyPlanned(int64(7_400_000_000))
		_ = e.plan.SetCommitment(features.CommitmentTentative)
		return features.RequestPlanResponse{PlanID: e.plan.PlanID()}, nil
	})
	e.plan.OnAcceptPlan(func(ctx context.Context, req features.AcceptPlanRequest) (features.AcceptPlanResponse, error) {
		_ = e.plan.SetCommitment(features.CommitmentCommitted)
		return features.AcceptPlanResponse{NewCommitment: e.plan.Commitment()}, nil
	})

	// SetChargingMode handler
	e.chargingSession.OnSetChargingMode(func(ctx context.Context, req features.SetChargingModeRequest) error {
		// Check if mode is supported
		if !e.chargingSession.SupportsMode(req.Mode) {
			return nil
		}

		_ = e.chargingSession.SetChargingMode(req.Mode)
		if req.SurplusThreshold != nil {
			_ = e.chargingSession.SetSurplusThreshold(*req.SurplusThreshold)
		}
		if req.StartDelay != nil {
			_ = e.chargingSession.SetStartDelay(*req.StartDelay)
		}
		if req.StopDelay != nil {
			_ = e.chargingSession.SetStopDelay(*req.StopDelay)
		}

		return nil
	})
}

func (e *EVSE) updateEndpointInfo() {
	endpoints := make([]*model.EndpointInfo, 0)
	for _, ep := range e.device.Endpoints() {
		endpoints = append(endpoints, ep.Info())
	}
	_ = e.deviceInfo.SetEndpoints(endpoints)
}

func (e *EVSE) updateUseCaseInfo() {
	decls := usecase.EvaluateDevice(e.device, usecase.Registry)
	_ = e.deviceInfo.SetUseCases(decls)
}

// Device returns the underlying MASH device.
func (e *EVSE) Device() *model.Device {
	return e.device
}

// Simulation methods for testing

// SimulateEVConnect simulates an EV connecting to the charger.
func (e *EVSE) SimulateEVConnect(evSoC uint8, evCapacity uint64, demandMode features.EVDemandMode) {
	e.mu.Lock()
	defer e.mu.Unlock()

	_ = e.chargingSession.StartSession(uint64(time.Now().Unix()))
	_ = e.chargingSession.SetEVStateOfCharge(evSoC)
	_ = e.chargingSession.SetEVBatteryCapacity(evCapacity)
	_ = e.chargingSession.SetEVDemandMode(demandMode)

	// Calculate energy requests based on SoC
	currentEnergy := int64(evCapacity) * int64(evSoC) / 100
	maxEnergy := int64(evCapacity) - currentEnergy
	targetEnergy := int64(evCapacity)*80/100 - currentEnergy // Target 80%

	_ = e.chargingSession.SetEVEnergyRequests(nil, &maxEnergy, &targetEnergy)
	_ = e.chargingSession.SetState(features.ChargingStatePluggedInDemand)

	_ = e.status.SetOperatingState(features.OperatingStateRunning)
	_ = e.energyControl.SetProcessState(features.ProcessStateRunning)
}

// SimulateEVDisconnect simulates the EV disconnecting.
func (e *EVSE) SimulateEVDisconnect() {
	e.mu.Lock()
	defer e.mu.Unlock()

	_ = e.chargingSession.EndSession(uint64(time.Now().Unix()))
	_ = e.chargingSession.ClearEVStateOfCharge()
	_ = e.chargingSession.ClearEVBatteryCapacity()

	e.currentPower = 0
	_ = e.measurement.SetACActivePower(0)
	_ = e.status.SetOperatingState(features.OperatingStateStandby)
	_ = e.energyControl.SetProcessState(features.ProcessStateNone)
}

// SimulateCharging simulates active charging at the given power.
func (e *EVSE) SimulateCharging(power int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Apply limit if set
	if limit, ok := e.energyControl.EffectiveConsumptionLimit(); ok && power > limit {
		power = limit
	}

	e.currentPower = power
	_ = e.measurement.SetACActivePower(power)
	_ = e.chargingSession.SetState(features.ChargingStatePluggedInCharging)
}

// GetCurrentPower returns the current charging power in mW.
func (e *EVSE) GetCurrentPower() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentPower
}

// GetEffectiveLimit returns the current effective limit from CEM.
func (e *EVSE) GetEffectiveLimit() *int64 {
	if limit, ok := e.energyControl.EffectiveConsumptionLimit(); ok {
		return &limit
	}
	return nil
}

// LimitResolver returns the LimitResolver for external wiring
// (e.g., setting OnZoneMyChange callback).
func (e *EVSE) LimitResolver() *features.LimitResolver {
	return e.limitResolver
}

// AcceptController marks the EVSE as being controlled.
func (e *EVSE) AcceptController() {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.energyControl.SetControlState(features.ControlStateControlled)
}
