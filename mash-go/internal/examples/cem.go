package examples

import (
	"context"
	"errors"
	"sync"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/interaction"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/usecase"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// DeviceClient is an interface for interacting with a remote device.
// Both interaction.Client and service.DeviceSession implement this interface.
type DeviceClient interface {
	Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error)
	Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error)
	Subscribe(ctx context.Context, endpointID uint8, featureID uint8, opts *interaction.SubscribeOptions) (uint32, map[uint16]any, error)
	Unsubscribe(ctx context.Context, subscriptionID uint32) error
	Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error)
}

// CEM represents a Central Energy Manager that controls other devices.
// It demonstrates how to build a MASH controller that:
//   - Discovers and connects to controllable devices
//   - Establishes zone relationships
//   - Sets limits and setpoints on devices
//   - Monitors device state via subscriptions
type CEM struct {
	mu sync.RWMutex

	device *model.Device

	// Features
	deviceInfo *features.DeviceInfo

	// Connected devices (zone members)
	connectedDevices map[string]*ConnectedDevice

	// Zone configuration
	zoneType features.LimitCause
}

// ConnectedDevice represents a device the CEM is controlling.
type ConnectedDevice struct {
	DeviceID string
	Client   DeviceClient

	// Cached device info
	VendorName  string
	ProductName string
	DeviceType  features.DeviceType

	// Current state from subscriptions
	CurrentPower          int64
	EffectiveLimit        *int64
	ControlState          features.ControlState
	ChargingState         features.ChargingState
	EVStateOfCharge       *uint8
	EVTargetEnergyRequest *int64

	EffectiveProductionLimit *int64                // From EnergyControl subscription
	ProcessState             features.ProcessState // From EnergyControl subscription

	// Signals state (from Signals subscription)
	SignalSource        *features.SignalSource // Who sent the active signal
	HasPriceSignal      bool                  // Non-nil priceSlots present
	HasConstraintSignal bool                  // Non-nil constraintSlots present

	// Plan state (from Plan subscription)
	PlanID         *uint32             // Current plan ID
	PlanCommitment *features.Commitment // How firm the current plan is

	// GPL state
	LimitApplied      bool                        // Was last SetLimit applied?
	RejectReason      *features.LimitRejectReason // Why limit was rejected
	OverrideReason    *features.OverrideReason    // If in OVERRIDE state
	OverrideDirection *features.Direction         // Which direction triggered override

	// Capacity info (read from device)
	NominalMaxConsumption     *int64 // From Electrical
	NominalMaxProduction      *int64 // From Electrical
	ContractualConsumptionMax *int64 // From EnergyControl (EMS only)
	ContractualProductionMax  *int64 // From EnergyControl (EMS only)

	// Use case discovery results
	UseCases *usecase.DeviceUseCases

	// Active subscriptions
	SubscriptionIDs []uint32
}

// CEMConfig contains configuration for creating a CEM.
type CEMConfig struct {
	DeviceID     string
	VendorName   string
	ProductName  string
	SerialNumber string
	VendorID     uint32
	ProductID    uint16
}

// SetLimitResult contains the enhanced SetLimit response.
type SetLimitResult struct {
	Applied                   bool
	EffectiveConsumptionLimit *int64
	EffectiveProductionLimit  *int64
	RejectReason              *features.LimitRejectReason
	ControlState              features.ControlState
}

// NewCEM creates a new CEM device with the given configuration.
func NewCEM(cfg CEMConfig) *CEM {
	cem := &CEM{
		connectedDevices: make(map[string]*ConnectedDevice),
		zoneType:         features.LimitCauseLocalOptimization,
	}

	// Create device
	cem.device = model.NewDevice(cfg.DeviceID, cfg.VendorID, cfg.ProductID)

	// Setup root endpoint with DeviceInfo
	cem.setupDeviceInfo(cfg)

	return cem
}

func (c *CEM) setupDeviceInfo(cfg CEMConfig) {
	c.deviceInfo = features.NewDeviceInfo()
	c.deviceInfo.Feature.SetFeatureMap(uint32(model.FeatureMapCore)) // CEM is a MASH controller
	_ = c.deviceInfo.SetDeviceID(cfg.DeviceID)
	_ = c.deviceInfo.SetVendorName(cfg.VendorName)
	_ = c.deviceInfo.SetProductName(cfg.ProductName)
	_ = c.deviceInfo.SetSerialNumber(cfg.SerialNumber)
	_ = c.deviceInfo.SetVendorID(cfg.VendorID)
	_ = c.deviceInfo.SetProductID(cfg.ProductID)
	_ = c.deviceInfo.SetSoftwareVersion("1.0.0")

	c.device.RootEndpoint().AddFeature(c.deviceInfo.Feature)

	// Update endpoint info
	endpoints := make([]*model.EndpointInfo, 0)
	for _, ep := range c.device.Endpoints() {
		endpoints = append(endpoints, ep.Info())
	}
	_ = c.deviceInfo.SetEndpoints(endpoints)

	// Declare controller-side use case support
	c.updateUseCaseInfo()
}

func (c *CEM) updateUseCaseInfo() {
	decls := usecase.EvaluateController(usecase.Registry)
	_ = c.deviceInfo.SetUseCases(decls)
}

// Device returns the underlying MASH device.
func (c *CEM) Device() *model.Device {
	return c.device
}

// SetZoneType sets the cause/priority for limits set by this CEM.
func (c *CEM) SetZoneType(zoneType features.LimitCause) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.zoneType = zoneType
}

// ConnectDevice establishes a zone relationship with a device.
// In a real implementation, this would happen after SHIP connection and pairing.
func (c *CEM) ConnectDevice(deviceID string, client DeviceClient) (*ConnectedDevice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.connectedDevices[deviceID]; exists {
		return nil, errors.New("device already connected")
	}

	device := &ConnectedDevice{
		DeviceID:     deviceID,
		Client:       client,
		ControlState: features.ControlStateAutonomous,
	}

	c.connectedDevices[deviceID] = device
	return device, nil
}

// DisconnectDevice removes a device from the zone.
func (c *CEM) DisconnectDevice(deviceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	device, exists := c.connectedDevices[deviceID]
	if !exists {
		return errors.New("device not connected")
	}

	// Unsubscribe from all active subscriptions
	ctx := context.Background()
	for _, subID := range device.SubscriptionIDs {
		_ = device.Client.Unsubscribe(ctx, subID)
	}

	delete(c.connectedDevices, deviceID)
	return nil
}

// GetConnectedDevice returns a connected device by ID.
func (c *CEM) GetConnectedDevice(deviceID string) (*ConnectedDevice, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	device, exists := c.connectedDevices[deviceID]
	return device, exists
}

// ConnectedDeviceIDs returns the IDs of all connected devices.
func (c *CEM) ConnectedDeviceIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.connectedDevices))
	for id := range c.connectedDevices {
		ids = append(ids, id)
	}
	return ids
}

// SetDeviceUseCases stores use case discovery results for a connected device.
func (c *CEM) SetDeviceUseCases(deviceID string, useCases *usecase.DeviceUseCases) {
	c.mu.Lock()
	defer c.mu.Unlock()

	device, exists := c.connectedDevices[deviceID]
	if !exists {
		return
	}
	device.UseCases = useCases
}

// ReadDeviceInfo reads and caches device information from a connected device.
func (c *CEM) ReadDeviceInfo(ctx context.Context, deviceID string) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	// Read DeviceInfo from endpoint 0
	attrs, err := device.Client.Read(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if v, ok := attrs[features.DeviceInfoAttrVendorName].(string); ok {
		device.VendorName = v
	}
	if v, ok := attrs[features.DeviceInfoAttrProductName].(string); ok {
		device.ProductName = v
	}

	return nil
}

// ReadEnergyControlState reads the current EnergyControl state from a device.
func (c *CEM) ReadEnergyControlState(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	attrs, err := device.Client.Read(ctx, endpointID, uint8(model.FeatureEnergyControl), nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if rawVal, exists := attrs[features.EnergyControlAttrDeviceType]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.DeviceType = features.DeviceType(v)
		}
	}
	if rawVal, exists := attrs[features.EnergyControlAttrControlState]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.ControlState = features.ControlState(v)
		}
	}
	if rawVal, exists := attrs[features.EnergyControlAttrEffectiveConsumptionLimit]; exists {
		if v, ok := wire.ToInt64(rawVal); ok {
			device.EffectiveLimit = &v
		}
	}

	return nil
}

// SubscribeToMeasurement subscribes to power measurements from a device.
func (c *CEM) SubscribeToMeasurement(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	subID, values, err := device.Client.Subscribe(ctx, endpointID, uint8(model.FeatureMeasurement), &interaction.SubscribeOptions{
		AttributeIDs: []uint16{features.MeasurementAttrACActivePower},
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	device.SubscriptionIDs = append(device.SubscriptionIDs, subID)

	// Process priming report using type-safe coercion
	if rawVal, exists := values[features.MeasurementAttrACActivePower]; exists {
		if v, ok := wire.ToInt64(rawVal); ok {
			device.CurrentPower = v
		}
	}
	c.mu.Unlock()

	return nil
}

// SubscribeToChargingSession subscribes to charging session updates.
func (c *CEM) SubscribeToChargingSession(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	subID, values, err := device.Client.Subscribe(ctx, endpointID, uint8(model.FeatureChargingSession), &interaction.SubscribeOptions{
		AttributeIDs: []uint16{
			features.ChargingSessionAttrState,
			features.ChargingSessionAttrEVStateOfCharge,
			features.ChargingSessionAttrEVTargetEnergyRequest,
		},
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	device.SubscriptionIDs = append(device.SubscriptionIDs, subID)

	// Process priming report using type-safe coercion
	if rawVal, exists := values[features.ChargingSessionAttrState]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.ChargingState = features.ChargingState(v)
		}
	}
	if rawVal, exists := values[features.ChargingSessionAttrEVStateOfCharge]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.EVStateOfCharge = &v
		}
	}
	if rawVal, exists := values[features.ChargingSessionAttrEVTargetEnergyRequest]; exists {
		if v, ok := wire.ToInt64(rawVal); ok {
			device.EVTargetEnergyRequest = &v
		}
	}
	c.mu.Unlock()

	return nil
}

// SubscribeToEnergyControl subscribes to energy control state changes from a device.
// This includes control state transitions, effective limits, override events, and process state.
func (c *CEM) SubscribeToEnergyControl(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	subID, values, err := device.Client.Subscribe(ctx, endpointID, uint8(model.FeatureEnergyControl), &interaction.SubscribeOptions{
		AttributeIDs: []uint16{
			features.EnergyControlAttrControlState,
			features.EnergyControlAttrEffectiveConsumptionLimit,
			features.EnergyControlAttrEffectiveProductionLimit,
			features.EnergyControlAttrOverrideReason,
			features.EnergyControlAttrOverrideDirection,
			features.EnergyControlAttrProcessState,
		},
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	device.SubscriptionIDs = append(device.SubscriptionIDs, subID)

	// Process priming report using type-safe coercion
	if rawVal, exists := values[features.EnergyControlAttrControlState]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.ControlState = features.ControlState(v)
		}
	}
	if rawVal, exists := values[features.EnergyControlAttrEffectiveConsumptionLimit]; exists {
		if v, ok := wire.ToInt64(rawVal); ok {
			device.EffectiveLimit = &v
		}
	}
	if rawVal, exists := values[features.EnergyControlAttrEffectiveProductionLimit]; exists {
		if v, ok := wire.ToInt64(rawVal); ok {
			device.EffectiveProductionLimit = &v
		}
	}
	if rawVal, exists := values[features.EnergyControlAttrOverrideReason]; exists && rawVal != nil {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			reason := features.OverrideReason(v)
			device.OverrideReason = &reason
		}
	}
	if rawVal, exists := values[features.EnergyControlAttrOverrideDirection]; exists && rawVal != nil {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			dir := features.Direction(v)
			device.OverrideDirection = &dir
		}
	}
	if rawVal, exists := values[features.EnergyControlAttrProcessState]; exists {
		if v, ok := wire.ToUint8Public(rawVal); ok {
			device.ProcessState = features.ProcessState(v)
		}
	}
	c.mu.Unlock()

	return nil
}

// SetPowerLimit sets a consumption limit on a device.
// Returns an enhanced result with applied status and effective limits.
func (c *CEM) SetPowerLimit(ctx context.Context, deviceID string, endpointID uint8, limitMW int64) (*SetLimitResult, error) {
	return c.SetPowerLimitFull(ctx, deviceID, endpointID, limitMW, features.LimitCause(0), nil)
}

// SetPowerLimitWithCause sets a consumption limit on a device with a specified cause.
// Returns an enhanced result with applied status and effective limits.
func (c *CEM) SetPowerLimitWithCause(ctx context.Context, deviceID string, endpointID uint8, limitMW int64, cause features.LimitCause) (*SetLimitResult, error) {
	return c.SetPowerLimitFull(ctx, deviceID, endpointID, limitMW, cause, nil)
}

// SetPowerLimitFull sets a consumption limit on a device with cause and optional duration.
// Duration is in seconds; pass nil for indefinite limit.
// Returns an enhanced result with applied status and effective limits.
func (c *CEM) SetPowerLimitFull(ctx context.Context, deviceID string, endpointID uint8, limitMW int64, cause features.LimitCause, durationSec *uint32) (*SetLimitResult, error) {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	zoneType := c.zoneType
	c.mu.RUnlock()

	if !exists {
		return nil, errors.New("device not connected")
	}

	// Use provided cause, or fall back to zone type
	effectiveCause := cause
	if cause == 0 {
		effectiveCause = zoneType
	}

	params := map[uint8]any{
		features.SetLimitParamConsumptionLimit: limitMW,
		features.SetLimitParamCause:            uint8(effectiveCause),
	}
	if durationSec != nil {
		params[features.SetLimitParamDuration] = *durationSec
	}

	rawResult, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureEnergyControl),
		features.EnergyControlCmdSetLimit, params)
	if err != nil {
		return nil, err
	}

	result := &SetLimitResult{}

	// Parse enhanced response using type-safe coercion. The response
	// payload is integer-keyed per docs/interaction-model.md §7.2 (Issue
	// #11 fix); CBOR widens uint8 to uint64 during generic decode, so we
	// narrow back via wire.ToUint8Map.
	if resultMap := wire.ToUint8Map(rawResult); resultMap != nil {
		if rawVal, exists := resultMap[features.SetLimitRespApplied]; exists {
			if v, ok := rawVal.(bool); ok {
				result.Applied = v
			}
		}

		if rawVal, exists := resultMap[features.SetLimitRespControlState]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				result.ControlState = features.ControlState(v)
			}
		}

		if rawVal, exists := resultMap[features.SetLimitRespRejectReason]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				reason := features.LimitRejectReason(v)
				result.RejectReason = &reason
			}
		}

		if rawVal, exists := resultMap[features.SetLimitRespEffectiveConsumptionLimit]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				result.EffectiveConsumptionLimit = &v
			}
		}
		if rawVal, exists := resultMap[features.SetLimitRespEffectiveProductionLimit]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				result.EffectiveProductionLimit = &v
			}
		}
	}

	// Update cached device state
	c.mu.Lock()
	device.LimitApplied = result.Applied
	device.RejectReason = result.RejectReason
	device.ControlState = result.ControlState
	if result.EffectiveConsumptionLimit != nil {
		device.EffectiveLimit = result.EffectiveConsumptionLimit
	}
	c.mu.Unlock()

	return result, nil
}

// ClearPowerLimit removes the consumption limit from a device.
func (c *CEM) ClearPowerLimit(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureEnergyControl),
		features.EnergyControlCmdClearLimit, nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	device.EffectiveLimit = nil
	device.ControlState = features.ControlStateControlled
	c.mu.Unlock()

	return nil
}

// PauseDevice pauses a device's operation.
func (c *CEM) PauseDevice(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureEnergyControl),
		features.EnergyControlCmdPause, nil)
	return err
}

// ResumeDevice resumes a paused device.
func (c *CEM) ResumeDevice(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureEnergyControl),
		features.EnergyControlCmdResume, nil)
	return err
}

// SetChargingMode sets the charging optimization mode on an EVSE.
func (c *CEM) SetChargingMode(ctx context.Context, deviceID string, endpointID uint8, mode features.ChargingMode, surplusThreshold *int64) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	params := map[uint8]any{
		features.SetChargingModeParamMode: uint8(mode),
	}
	if surplusThreshold != nil {
		params[features.SetChargingModeParamSurplusThreshold] = *surplusThreshold
	}

	result, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureChargingSession),
		features.ChargingSessionCmdSetChargingMode, params)
	if err != nil {
		return err
	}

	// Check if successful — simple-response form: codegen returns
	// {1: success} where id 1 is the conventional success/applied field
	// (Issue #11 fix; YAML setChargingMode declares no response block,
	// so id 1 is synthesized).
	if resultMap := wire.ToUint8Map(result); resultMap != nil {
		if success, ok := resultMap[1].(bool); ok && !success {
			return errors.New("charging mode not accepted")
		}
	}

	return nil
}

// SendPriceSignal sends a price signal to a device's Signals feature.
func (c *CEM) SendPriceSignal(ctx context.Context, deviceID string, endpointID uint8, source features.SignalSource, startTime uint64, validUntil *uint64, slots []features.PriceSlot) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	slotsData := make([]any, len(slots))
	for i, s := range slots {
		slotsData[i] = map[string]any{
			"duration":         s.Duration,
			"price":            s.Price,
			"priceLevel":       s.PriceLevel,
			"renewablePercent": s.RenewablePercent,
			"co2Intensity":     s.Co2Intensity,
		}
	}

	params := map[uint8]any{
		features.SendPriceSignalParamSource:    uint8(source),
		features.SendPriceSignalParamStartTime: startTime,
		features.SendPriceSignalParamSlots:     slotsData,
	}
	if validUntil != nil {
		params[features.SendPriceSignalParamValidUntil] = *validUntil
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureSignals),
		features.SignalsCmdSendPriceSignal, params)
	return err
}

// SendConstraintSignal sends a constraint signal to a device's Signals feature.
func (c *CEM) SendConstraintSignal(ctx context.Context, deviceID string, endpointID uint8, source features.SignalSource, startTime uint64, validUntil *uint64, slots []features.ConstraintSlot) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	slotsData := make([]any, len(slots))
	for i, s := range slots {
		slotsData[i] = map[string]any{
			"duration":       s.Duration,
			"consumptionMax": s.ConsumptionMax,
			"consumptionMin": s.ConsumptionMin,
			"productionMax":  s.ProductionMax,
			"productionMin":  s.ProductionMin,
		}
	}

	params := map[uint8]any{
		features.SendConstraintSignalParamSource:    uint8(source),
		features.SendConstraintSignalParamStartTime: startTime,
		features.SendConstraintSignalParamSlots:     slotsData,
	}
	if validUntil != nil {
		params[features.SendConstraintSignalParamValidUntil] = *validUntil
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureSignals),
		features.SignalsCmdSendConstraintSignal, params)
	return err
}

// ClearSignals clears all active signals on a device.
func (c *CEM) ClearSignals(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	_, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeatureSignals),
		features.SignalsCmdClearSignals, nil)
	return err
}

// RequestPlan asks a device to generate or update its power plan.
// Returns the plan ID from the device's response.
func (c *CEM) RequestPlan(ctx context.Context, deviceID string, endpointID uint8) (uint32, error) {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return 0, errors.New("device not connected")
	}

	rawResult, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeaturePlan),
		features.PlanCmdRequestPlan, nil)
	if err != nil {
		return 0, err
	}

	if resultMap := wire.ToUint8Map(rawResult); resultMap != nil {
		if rawVal, exists := resultMap[features.RequestPlanRespPlanID]; exists {
			if v, ok := wire.ToUint32(rawVal); ok {
				return v, nil
			}
		}
	}

	return 0, nil
}

// AcceptPlan accepts a device's plan, advancing its commitment level.
// Returns the new commitment level.
func (c *CEM) AcceptPlan(ctx context.Context, deviceID string, endpointID uint8, planID uint32) (features.Commitment, error) {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return 0, errors.New("device not connected")
	}

	params := map[uint8]any{
		features.AcceptPlanParamPlanID:      planID,
		features.AcceptPlanParamPlanVersion: uint32(0),
	}

	rawResult, err := device.Client.Invoke(ctx, endpointID, uint8(model.FeaturePlan),
		features.PlanCmdAcceptPlan, params)
	if err != nil {
		return 0, err
	}

	if resultMap := wire.ToUint8Map(rawResult); resultMap != nil {
		if rawVal, exists := resultMap[features.AcceptPlanRespNewCommitment]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				return features.Commitment(v), nil
			}
		}
	}

	return features.CommitmentPreliminary, nil
}

// HandleNotification processes incoming notifications from subscribed devices.
// Call this when a notification is received from a connected device.
// Uses wire.ToInt64/ToUint8Public for type-safe CBOR value extraction.
func (c *CEM) HandleNotification(deviceID string, endpointID uint8, featureID uint8, changes map[uint16]any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	device, exists := c.connectedDevices[deviceID]
	if !exists {
		return
	}

	switch model.FeatureType(featureID) {
	case model.FeatureMeasurement:
		if rawVal, exists := changes[features.MeasurementAttrACActivePower]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				device.CurrentPower = v
			}
		}

	case model.FeatureEnergyControl:
		if rawVal, exists := changes[features.EnergyControlAttrControlState]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				device.ControlState = features.ControlState(v)
			}
		}
		if rawVal, exists := changes[features.EnergyControlAttrEffectiveConsumptionLimit]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				device.EffectiveLimit = &v
			}
		}
		if rawVal, exists := changes[features.EnergyControlAttrEffectiveProductionLimit]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				device.EffectiveProductionLimit = &v
			}
		}
		if rawVal, ok := changes[features.EnergyControlAttrOverrideReason]; ok {
			if rawVal != nil {
				if v, ok := wire.ToUint8Public(rawVal); ok {
					reason := features.OverrideReason(v)
					device.OverrideReason = &reason
				}
			} else {
				device.OverrideReason = nil
			}
		}
		if rawVal, ok := changes[features.EnergyControlAttrOverrideDirection]; ok {
			if rawVal != nil {
				if v, ok := wire.ToUint8Public(rawVal); ok {
					dir := features.Direction(v)
					device.OverrideDirection = &dir
				}
			} else {
				device.OverrideDirection = nil
			}
		}
		if rawVal, exists := changes[features.EnergyControlAttrProcessState]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				device.ProcessState = features.ProcessState(v)
			}
		}

	case model.FeatureChargingSession:
		if rawVal, exists := changes[features.ChargingSessionAttrState]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				device.ChargingState = features.ChargingState(v)
			}
		}
		if rawVal, exists := changes[features.ChargingSessionAttrEVStateOfCharge]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				device.EVStateOfCharge = &v
			}
		}
		if rawVal, exists := changes[features.ChargingSessionAttrEVTargetEnergyRequest]; exists {
			if v, ok := wire.ToInt64(rawVal); ok {
				device.EVTargetEnergyRequest = &v
			}
		}

	case model.FeatureSignals:
		if rawVal, ok := changes[features.SignalsAttrSignalSource]; ok {
			if rawVal != nil {
				if v, ok := wire.ToUint8Public(rawVal); ok {
					src := features.SignalSource(v)
					device.SignalSource = &src
				}
			} else {
				device.SignalSource = nil
			}
		}
		if rawVal, ok := changes[features.SignalsAttrPriceSlots]; ok {
			device.HasPriceSignal = rawVal != nil
		}
		if rawVal, ok := changes[features.SignalsAttrConstraintSlots]; ok {
			device.HasConstraintSignal = rawVal != nil
		}

	case model.FeaturePlan:
		if rawVal, exists := changes[features.PlanAttrPlanID]; exists {
			if v, ok := wire.ToUint32(rawVal); ok {
				device.PlanID = &v
			}
		}
		if rawVal, exists := changes[features.PlanAttrCommitment]; exists {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				c := features.Commitment(v)
				device.PlanCommitment = &c
			}
		}
	}
}

// GetTotalPower returns the sum of power consumption across all connected devices.
func (c *CEM) GetTotalPower() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var total int64
	for _, device := range c.connectedDevices {
		total += device.CurrentPower
	}
	return total
}

// GetDevice returns a connected device by ID (or nil if not found).
func (c *CEM) GetDevice(deviceID string) *ConnectedDevice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connectedDevices[deviceID]
}

// ReadDeviceCapacity reads capacity information from a device.
func (c *CEM) ReadDeviceCapacity(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	// Read Electrical.nominalMaxConsumption
	val, err := device.Client.Read(ctx, endpointID, uint8(model.FeatureElectrical),
		[]uint16{features.ElectricalAttrNominalMaxConsumption})
	if err == nil && val != nil {
		if rawVal, ok := val[features.ElectricalAttrNominalMaxConsumption]; ok {
			if v, ok := wire.ToInt64(rawVal); ok {
				c.mu.Lock()
				device.NominalMaxConsumption = &v
				c.mu.Unlock()
			}
		}
	}

	// Read Electrical.nominalMaxProduction
	val, err = device.Client.Read(ctx, endpointID, uint8(model.FeatureElectrical),
		[]uint16{features.ElectricalAttrNominalMaxProduction})
	if err == nil && val != nil {
		if rawVal, ok := val[features.ElectricalAttrNominalMaxProduction]; ok {
			if v, ok := wire.ToInt64(rawVal); ok {
				c.mu.Lock()
				device.NominalMaxProduction = &v
				c.mu.Unlock()
			}
		}
	}

	// Read EnergyControl.contractualConsumptionMax (may not exist)
	val, err = device.Client.Read(ctx, endpointID, uint8(model.FeatureEnergyControl),
		[]uint16{features.EnergyControlAttrContractualConsumptionMax})
	if err == nil && val != nil {
		if rawVal, ok := val[features.EnergyControlAttrContractualConsumptionMax]; ok {
			if v, ok := wire.ToInt64(rawVal); ok {
				c.mu.Lock()
				device.ContractualConsumptionMax = &v
				c.mu.Unlock()
			}
		}
	}

	// Read EnergyControl.contractualProductionMax (may not exist)
	val, err = device.Client.Read(ctx, endpointID, uint8(model.FeatureEnergyControl),
		[]uint16{features.EnergyControlAttrContractualProductionMax})
	if err == nil && val != nil {
		if rawVal, ok := val[features.EnergyControlAttrContractualProductionMax]; ok {
			if v, ok := wire.ToInt64(rawVal); ok {
				c.mu.Lock()
				device.ContractualProductionMax = &v
				c.mu.Unlock()
			}
		}
	}

	return nil
}

// ReadOverrideState reads override state from a device.
func (c *CEM) ReadOverrideState(ctx context.Context, deviceID string, endpointID uint8) error {
	c.mu.RLock()
	device, exists := c.connectedDevices[deviceID]
	c.mu.RUnlock()

	if !exists {
		return errors.New("device not connected")
	}

	// Read overrideReason
	val, err := device.Client.Read(ctx, endpointID, uint8(model.FeatureEnergyControl),
		[]uint16{features.EnergyControlAttrOverrideReason})
	if err == nil && val != nil {
		if rawVal, ok := val[features.EnergyControlAttrOverrideReason]; ok && rawVal != nil {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				c.mu.Lock()
				reason := features.OverrideReason(v)
				device.OverrideReason = &reason
				c.mu.Unlock()
			}
		} else {
			c.mu.Lock()
			device.OverrideReason = nil
			c.mu.Unlock()
		}
	}

	// Read overrideDirection
	val, err = device.Client.Read(ctx, endpointID, uint8(model.FeatureEnergyControl),
		[]uint16{features.EnergyControlAttrOverrideDirection})
	if err == nil && val != nil {
		if rawVal, ok := val[features.EnergyControlAttrOverrideDirection]; ok && rawVal != nil {
			if v, ok := wire.ToUint8Public(rawVal); ok {
				c.mu.Lock()
				dir := features.Direction(v)
				device.OverrideDirection = &dir
				c.mu.Unlock()
			}
		} else {
			c.mu.Lock()
			device.OverrideDirection = nil
			c.mu.Unlock()
		}
	}

	return nil
}
