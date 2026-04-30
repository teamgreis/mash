package examples

import (
	"context"
	"sync"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/usecase"
)

// HeatPump represents a heat pump device implementation.
// It demonstrates how to build a controllable MASH device that:
//   - Exposes its capabilities via Electrical feature
//   - Reports measurements via Measurement feature
//   - Accepts control via EnergyControl feature (limits, pause/resume)
//   - Reports health via Status feature
type HeatPump struct {
	mu sync.RWMutex

	device *model.Device

	// Features
	deviceInfo    *features.DeviceInfo
	electrical    *features.Electrical
	measurement   *features.Measurement
	energyControl *features.EnergyControl
	status        *features.Status

	// Limit resolution
	limitResolver *features.LimitResolver

	// Internal state
	currentPower int64 // mW - actual heating power
}

// HeatPumpConfig contains configuration for creating a heat pump.
type HeatPumpConfig struct {
	DeviceID     string
	VendorName   string
	ProductName  string
	SerialNumber string
	VendorID     uint32
	ProductID    uint16

	// Electrical capabilities
	PhaseCount         uint8
	NominalVoltage     uint16 // V
	NominalMaxPower    int64  // mW
	NominalMinPower    int64  // mW
	MaxCurrentPerPhase int64  // mA
}

// NewHeatPump creates a new heat pump device with the given configuration.
func NewHeatPump(cfg HeatPumpConfig) *HeatPump {
	hp := &HeatPump{}

	hp.device = model.NewDevice(cfg.DeviceID, cfg.VendorID, cfg.ProductID)

	hp.setupDeviceInfo(cfg)
	hp.setupHeatPumpEndpoint(cfg)
	hp.setupCommandHandlers()

	return hp
}

func (h *HeatPump) setupDeviceInfo(cfg HeatPumpConfig) {
	h.deviceInfo = features.NewDeviceInfo()
	h.deviceInfo.Feature.SetFeatureMap(uint32(model.FeatureMapCore | model.FeatureMapFlex))
	_ = h.deviceInfo.SetDeviceID(cfg.DeviceID)
	_ = h.deviceInfo.SetVendorName(cfg.VendorName)
	_ = h.deviceInfo.SetProductName(cfg.ProductName)
	_ = h.deviceInfo.SetSerialNumber(cfg.SerialNumber)
	_ = h.deviceInfo.SetVendorID(cfg.VendorID)
	_ = h.deviceInfo.SetProductID(cfg.ProductID)
	_ = h.deviceInfo.SetSoftwareVersion("1.0.0")

	h.device.RootEndpoint().AddFeature(h.deviceInfo.Feature)
}

func (h *HeatPump) setupHeatPumpEndpoint(cfg HeatPumpConfig) {
	hpEndpoint := model.NewEndpoint(1, model.EndpointHeatPump, "Heat Pump")

	hpCapabilities := uint32(model.FeatureMapCore | model.FeatureMapFlex)

	// Electrical - static capabilities (consumption only)
	h.electrical = features.NewElectrical()
	h.electrical.Feature.SetFeatureMap(hpCapabilities)
	_ = h.electrical.SetPhaseCount(cfg.PhaseCount)
	_ = h.electrical.SetNominalVoltage(cfg.NominalVoltage)
	_ = h.electrical.SetNominalFrequency(50)
	_ = h.electrical.SetNominalMaxConsumption(cfg.NominalMaxPower)
	_ = h.electrical.SetNominalMinPower(cfg.NominalMinPower)
	if cfg.MaxCurrentPerPhase > 0 {
		_ = h.electrical.SetMaxCurrentPerPhase(cfg.MaxCurrentPerPhase)
	}
	_ = h.electrical.SetSupportedDirections(features.DirectionConsumption)
	hpEndpoint.AddFeature(h.electrical.Feature)

	// Measurement - real-time telemetry
	h.measurement = features.NewMeasurement()
	h.measurement.Feature.SetFeatureMap(hpCapabilities)
	hpEndpoint.AddFeature(h.measurement.Feature)

	// EnergyControl - accepts limits from CEM
	h.energyControl = features.NewEnergyControl()
	h.energyControl.Feature.SetFeatureMap(hpCapabilities)
	_ = h.energyControl.SetDeviceType(features.DeviceTypeHeatPump)
	_ = h.energyControl.SetControlState(features.ControlStateAutonomous)
	h.energyControl.SetCapabilities(
		true,  // acceptsLimits
		false, // acceptsCurrentLimits
		false, // acceptsSetpoints
		false, // acceptsCurrentSetpoints
		true,  // isPausable
		false, // isShiftable
		false, // isStoppable
	)
	hpEndpoint.AddFeature(h.energyControl.Feature)

	// Status - health and operating state
	h.status = features.NewStatus()
	h.status.Feature.SetFeatureMap(hpCapabilities)
	_ = h.status.SetOperatingState(features.OperatingStateStandby)
	hpEndpoint.AddFeature(h.status.Feature)

	_ = h.device.AddEndpoint(hpEndpoint)

	// Update DeviceInfo with endpoint structure and use cases
	h.updateEndpointInfo()
	h.updateUseCaseInfo()
}

func (h *HeatPump) setupCommandHandlers() {
	h.limitResolver = features.NewLimitResolver(h.energyControl)
	h.limitResolver.MaxConsumption = h.electrical.NominalMaxConsumption()
	h.limitResolver.Register()

	// Pause handler
	h.energyControl.OnPause(func(ctx context.Context, req features.PauseRequest) error {
		if !h.energyControl.IsPausable() {
			return features.CapabilityUnsupportedError("Pause")
		}
		h.mu.Lock()
		defer h.mu.Unlock()

		_ = h.status.SetOperatingState(features.OperatingStatePaused)
		_ = h.energyControl.SetProcessState(features.ProcessStatePaused)
		h.currentPower = 0
		_ = h.measurement.SetACActivePower(0)

		return nil
	})

	// Resume handler
	h.energyControl.OnResume(func(ctx context.Context) error {
		if !h.energyControl.IsPausable() {
			return features.CapabilityUnsupportedError("Resume")
		}
		h.mu.Lock()
		defer h.mu.Unlock()

		_ = h.status.SetOperatingState(features.OperatingStateRunning)
		_ = h.energyControl.SetProcessState(features.ProcessStateRunning)

		return nil
	})
}

func (h *HeatPump) updateEndpointInfo() {
	endpoints := make([]*model.EndpointInfo, 0)
	for _, ep := range h.device.Endpoints() {
		endpoints = append(endpoints, ep.Info())
	}
	_ = h.deviceInfo.SetEndpoints(endpoints)
}

func (h *HeatPump) updateUseCaseInfo() {
	decls := usecase.EvaluateDevice(h.device, usecase.Registry)
	_ = h.deviceInfo.SetUseCases(decls)
}

// Device returns the underlying MASH device.
func (h *HeatPump) Device() *model.Device {
	return h.device
}

// LimitResolver returns the LimitResolver for external wiring.
func (h *HeatPump) LimitResolver() *features.LimitResolver {
	return h.limitResolver
}

// SimulateHeating simulates the heat pump running at the given power.
func (h *HeatPump) SimulateHeating(power int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Apply limit if set
	if limit, ok := h.energyControl.EffectiveConsumptionLimit(); ok && power > limit {
		power = limit
	}

	h.currentPower = power
	_ = h.measurement.SetACActivePower(power)
	_ = h.status.SetOperatingState(features.OperatingStateRunning)
	_ = h.energyControl.SetProcessState(features.ProcessStateRunning)
}

// GetCurrentPower returns the current heating power in mW.
func (h *HeatPump) GetCurrentPower() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.currentPower
}

// AcceptController marks the heat pump as being controlled.
func (h *HeatPump) AcceptController() {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.energyControl.SetControlState(features.ControlStateControlled)
}
