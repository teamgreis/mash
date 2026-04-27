package service

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery/mocks"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
)

// newDeviceServiceWithChargingSession creates a DeviceService whose device has
// an EV_CHARGER endpoint (ID 1) with a ChargingSession feature. This is the
// minimal setup needed to exercise handleChargingSessionTrigger.
func newDeviceServiceWithChargingSession(t *testing.T) *DeviceService {
	t.Helper()

	device := model.NewDevice("test-device", 0x1234, 0x5678)

	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Test Charger")
	cs := features.NewChargingSession()
	charger.AddFeature(cs.Feature)
	if err := device.AddEndpoint(charger); err != nil {
		t.Fatalf("AddEndpoint failed: %v", err)
	}

	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}
	return svc
}

// readChargingState reads the ChargingSession state attribute from endpoint 1.
func readChargingState(t *testing.T, svc *DeviceService) features.ChargingState {
	t.Helper()

	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}
	feat, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession))
	if err != nil {
		t.Fatalf("GetFeatureByID(ChargingSession) failed: %v", err)
	}
	val, err := feat.ReadAttribute(features.ChargingSessionAttrState)
	if err != nil {
		t.Fatalf("ReadAttribute(state) failed: %v", err)
	}
	return features.ChargingState(val.(uint8))
}

func TestHandleChargingSessionTrigger_EVPlugIn(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	// Default state should be NOT_PLUGGED_IN.
	if got := readChargingState(t, svc); got != features.ChargingStateNotPluggedIn {
		t.Fatalf("expected initial state NOT_PLUGGED_IN, got %v", got)
	}

	if err := svc.dispatchTrigger(ctx, features.TriggerEVPlugIn); err != nil {
		t.Fatalf("dispatchTrigger(EVPlugIn) error: %v", err)
	}

	if got := readChargingState(t, svc); got != features.ChargingStatePluggedInNoDemand {
		t.Errorf("expected state PLUGGED_IN_NO_DEMAND after plug in, got %v", got)
	}
}

func TestHandleChargingSessionTrigger_EVUnplug(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	// First plug in so we start from a non-default state.
	if err := svc.dispatchTrigger(ctx, features.TriggerEVPlugIn); err != nil {
		t.Fatalf("dispatchTrigger(EVPlugIn) error: %v", err)
	}

	if err := svc.dispatchTrigger(ctx, features.TriggerEVUnplug); err != nil {
		t.Fatalf("dispatchTrigger(EVUnplug) error: %v", err)
	}

	if got := readChargingState(t, svc); got != features.ChargingStateNotPluggedIn {
		t.Errorf("expected state NOT_PLUGGED_IN after unplug, got %v", got)
	}
}

func TestHandleChargingSessionTrigger_EVRequestCharge(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	if err := svc.dispatchTrigger(ctx, features.TriggerEVRequestCharge); err != nil {
		t.Fatalf("dispatchTrigger(EVRequestCharge) error: %v", err)
	}

	if got := readChargingState(t, svc); got != features.ChargingStatePluggedInDemand {
		t.Errorf("expected state PLUGGED_IN_DEMAND after request charge, got %v", got)
	}
}

func TestHandleChargingSessionTrigger_UnknownTrigger(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	// Use a trigger with the ChargingSession domain but an unknown sub-opcode.
	unknownTrigger := uint64(0x0006_0000_0000_FFFF)
	err := svc.dispatchTrigger(ctx, unknownTrigger)
	if err == nil {
		t.Fatal("expected error for unknown charging session trigger")
	}
}

func TestHandleChargingSessionTrigger_NoFeature(t *testing.T) {
	// Create a device without any ChargingSession feature.
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	ctx := context.Background()
	err = svc.dispatchTrigger(ctx, features.TriggerEVPlugIn)
	if err == nil {
		t.Fatal("expected error when no ChargingSession feature exists")
	}
}

// --- ChargingSession timestamp tests ---

// readSessionStartTime reads sessionStartTime from endpoint 1's ChargingSession feature.
func readSessionStartTime(t *testing.T, svc *DeviceService) (uint64, bool) {
	t.Helper()
	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}
	feat, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession))
	if err != nil {
		t.Fatalf("GetFeatureByID(ChargingSession) failed: %v", err)
	}
	val, err := feat.ReadAttribute(features.ChargingSessionAttrSessionStartTime)
	if err != nil {
		t.Fatalf("ReadAttribute(sessionStartTime) failed: %v", err)
	}
	if val == nil {
		return 0, false
	}
	return val.(uint64), true
}

// readSessionEndTime reads sessionEndTime from endpoint 1's ChargingSession feature.
func readSessionEndTime(t *testing.T, svc *DeviceService) (uint64, bool) {
	t.Helper()
	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}
	feat, err := ep.GetFeatureByID(uint8(model.FeatureChargingSession))
	if err != nil {
		t.Fatalf("GetFeatureByID(ChargingSession) failed: %v", err)
	}
	val, err := feat.ReadAttribute(features.ChargingSessionAttrSessionEndTime)
	if err != nil {
		t.Fatalf("ReadAttribute(sessionEndTime) failed: %v", err)
	}
	if val == nil {
		return 0, false
	}
	return val.(uint64), true
}

func TestHandleChargingSessionTrigger_EVPlugIn_SetsStartTime(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	// Before plug-in, sessionStartTime should be nil (nullable attribute).
	if _, ok := readSessionStartTime(t, svc); ok {
		t.Fatal("expected sessionStartTime to be nil before plug in")
	}

	before := uint64(time.Now().Unix())
	if err := svc.dispatchTrigger(ctx, features.TriggerEVPlugIn); err != nil {
		t.Fatalf("dispatchTrigger(EVPlugIn) error: %v", err)
	}
	after := uint64(time.Now().Unix())

	ts, ok := readSessionStartTime(t, svc)
	if !ok {
		t.Fatal("expected sessionStartTime to be set after plug in")
	}
	if ts < before || ts > after {
		t.Errorf("sessionStartTime %d not in expected range [%d, %d]", ts, before, after)
	}
}

func TestHandleChargingSessionTrigger_EVUnplug_SetsEndTime(t *testing.T) {
	svc := newDeviceServiceWithChargingSession(t)
	ctx := context.Background()

	// Plug in first so unplug is meaningful.
	if err := svc.dispatchTrigger(ctx, features.TriggerEVPlugIn); err != nil {
		t.Fatalf("dispatchTrigger(EVPlugIn) error: %v", err)
	}

	before := uint64(time.Now().Unix())
	if err := svc.dispatchTrigger(ctx, features.TriggerEVUnplug); err != nil {
		t.Fatalf("dispatchTrigger(EVUnplug) error: %v", err)
	}
	after := uint64(time.Now().Unix())

	ts, ok := readSessionEndTime(t, svc)
	if !ok {
		t.Fatal("expected sessionEndTime to be set after unplug")
	}
	if ts < before || ts > after {
		t.Errorf("sessionEndTime %d not in expected range [%d, %d]", ts, before, after)
	}
}

// --- EnergyControl trigger tests ---

// newDeviceServiceWithEnergyControl creates a DeviceService whose device has
// an EV_CHARGER endpoint (ID 1) with an EnergyControl feature.
func newDeviceServiceWithEnergyControl(t *testing.T) *DeviceService {
	t.Helper()

	device := model.NewDevice("test-device", 0x1234, 0x5678)

	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Test Charger")
	ec := features.NewEnergyControl()
	charger.AddFeature(ec.Feature)
	if err := device.AddEndpoint(charger); err != nil {
		t.Fatalf("AddEndpoint failed: %v", err)
	}

	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}
	return svc
}

// readControlState reads the EnergyControl ControlState attribute from endpoint 1.
func readControlState(t *testing.T, svc *DeviceService) features.ControlState {
	t.Helper()

	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}
	feat, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl))
	if err != nil {
		t.Fatalf("GetFeatureByID(EnergyControl) failed: %v", err)
	}
	val, err := feat.ReadAttribute(features.EnergyControlAttrControlState)
	if err != nil {
		t.Fatalf("ReadAttribute(controlState) failed: %v", err)
	}
	return features.ControlState(val.(uint8))
}

// readProcessState reads the EnergyControl ProcessState attribute from endpoint 1.
func readProcessState(t *testing.T, svc *DeviceService) features.ProcessState {
	t.Helper()

	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}
	feat, err := ep.GetFeatureByID(uint8(model.FeatureEnergyControl))
	if err != nil {
		t.Fatalf("GetFeatureByID(EnergyControl) failed: %v", err)
	}
	val, err := feat.ReadAttribute(features.EnergyControlAttrProcessState)
	if err != nil {
		t.Fatalf("ReadAttribute(processState) failed: %v", err)
	}
	return features.ProcessState(val.(uint8))
}

func TestHandleEnergyControlTrigger_ControlStateLimited(t *testing.T) {
	svc := newDeviceServiceWithEnergyControl(t)
	ctx := context.Background()

	// Default state should be AUTONOMOUS.
	if got := readControlState(t, svc); got != features.ControlStateAutonomous {
		t.Fatalf("expected initial control state AUTONOMOUS, got %v", got)
	}

	if err := svc.dispatchTrigger(ctx, features.TriggerControlStateLimited); err != nil {
		t.Fatalf("dispatchTrigger(ControlStateLimited) error: %v", err)
	}

	if got := readControlState(t, svc); got != features.ControlStateLimited {
		t.Errorf("expected control state LIMITED, got %v", got)
	}
}

func TestHandleEnergyControlTrigger_ProcessStateRunning(t *testing.T) {
	svc := newDeviceServiceWithEnergyControl(t)
	ctx := context.Background()

	// Default state should be NONE.
	if got := readProcessState(t, svc); got != features.ProcessStateNone {
		t.Fatalf("expected initial process state NONE, got %v", got)
	}

	if err := svc.dispatchTrigger(ctx, features.TriggerProcessStateRunning); err != nil {
		t.Fatalf("dispatchTrigger(ProcessStateRunning) error: %v", err)
	}

	if got := readProcessState(t, svc); got != features.ProcessStateRunning {
		t.Errorf("expected process state RUNNING, got %v", got)
	}
}

func TestHandleEnergyControlTrigger_UnknownTrigger(t *testing.T) {
	svc := newDeviceServiceWithEnergyControl(t)
	ctx := context.Background()

	unknownTrigger := uint64(0x0005_0000_0000_FFFF)
	err := svc.dispatchTrigger(ctx, unknownTrigger)
	if err == nil {
		t.Fatal("expected error for unknown energy control trigger")
	}
}

func TestHandleEnergyControlTrigger_NoFeature(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	ctx := context.Background()
	err = svc.dispatchTrigger(ctx, features.TriggerControlStateControlled)
	if err == nil {
		t.Fatal("expected error when no EnergyControl feature exists")
	}
}

// --- Reset trigger tests ---

// newDeviceServiceWithAllFeatures creates a DeviceService whose device has an
// EV_CHARGER endpoint (ID 1) with Status, Measurement, ChargingSession, and
// EnergyControl features. This is the setup needed to exercise TriggerResetTestState.
func newDeviceServiceWithAllFeatures(t *testing.T) *DeviceService {
	t.Helper()

	device := model.NewDevice("test-device", 0x1234, 0x5678)

	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Test Charger")
	charger.AddFeature(features.NewStatus().Feature)
	charger.AddFeature(features.NewMeasurement().Feature)
	charger.AddFeature(features.NewChargingSession().Feature)
	charger.AddFeature(features.NewEnergyControl().Feature)
	if err := device.AddEndpoint(charger); err != nil {
		t.Fatalf("AddEndpoint failed: %v", err)
	}

	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}
	return svc
}

func TestTriggerResetTestState(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Move all features away from their default states.
	if err := svc.dispatchTrigger(ctx, features.TriggerFault); err != nil {
		t.Fatalf("dispatchTrigger(Fault) error: %v", err)
	}
	if err := svc.dispatchTrigger(ctx, features.TriggerSetPower1000); err != nil {
		t.Fatalf("dispatchTrigger(SetPower1000) error: %v", err)
	}
	if err := svc.dispatchTrigger(ctx, features.TriggerEVPlugIn); err != nil {
		t.Fatalf("dispatchTrigger(EVPlugIn) error: %v", err)
	}
	if err := svc.dispatchTrigger(ctx, features.TriggerControlStateLimited); err != nil {
		t.Fatalf("dispatchTrigger(ControlStateLimited) error: %v", err)
	}
	if err := svc.dispatchTrigger(ctx, features.TriggerProcessStateRunning); err != nil {
		t.Fatalf("dispatchTrigger(ProcessStateRunning) error: %v", err)
	}

	// Reset all state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState) error: %v", err)
	}

	// Verify Status reset to STANDBY.
	ep, err := svc.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1) failed: %v", err)
	}

	statusFeat, err := ep.GetFeatureByID(uint8(model.FeatureStatus))
	if err != nil {
		t.Fatalf("GetFeatureByID(Status) failed: %v", err)
	}
	osVal, err := statusFeat.ReadAttribute(features.StatusAttrOperatingState)
	if err != nil {
		t.Fatalf("ReadAttribute(operatingState) failed: %v", err)
	}
	if features.OperatingState(osVal.(uint8)) != features.OperatingStateStandby {
		t.Errorf("expected operating state STANDBY after reset, got %v", osVal)
	}

	// Verify Measurement reset to zero.
	measFeat, err := ep.GetFeatureByID(uint8(model.FeatureMeasurement))
	if err != nil {
		t.Fatalf("GetFeatureByID(Measurement) failed: %v", err)
	}
	powerVal, err := measFeat.ReadAttribute(features.MeasurementAttrACActivePower)
	if err != nil {
		t.Fatalf("ReadAttribute(acActivePower) failed: %v", err)
	}
	if powerVal.(int64) != 0 {
		t.Errorf("expected power 0 after reset, got %v", powerVal)
	}

	// Verify ChargingSession reset to NOT_PLUGGED_IN.
	if got := readChargingState(t, svc); got != features.ChargingStateNotPluggedIn {
		t.Errorf("expected charging state NOT_PLUGGED_IN after reset, got %v", got)
	}

	// Verify EnergyControl reset to AUTONOMOUS/NONE.
	if got := readControlState(t, svc); got != features.ControlStateAutonomous {
		t.Errorf("expected control state AUTONOMOUS after reset, got %v", got)
	}
	if got := readProcessState(t, svc); got != features.ProcessStateNone {
		t.Errorf("expected process state NONE after reset, got %v", got)
	}
}

func TestTriggerFactoryReset_ClearsAllZones(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.TestEnableKey = "test-enable-key"

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	ctx := context.Background()

	// Simulate two connected zones by directly adding to connectedZones.
	svc.mu.Lock()
	svc.connectedZones["zone-1"] = &ConnectedZone{ID: "zone-1", Connected: true}
	svc.connectedZones["zone-2"] = &ConnectedZone{ID: "zone-2", Connected: true}
	svc.mu.Unlock()

	// Verify zones exist.
	if len(svc.ListZoneIDs()) != 2 {
		t.Fatalf("expected 2 zones before reset, got %d", len(svc.ListZoneIDs()))
	}

	// Send factory reset trigger.
	if err := svc.dispatchTrigger(ctx, features.TriggerFactoryReset); err != nil {
		t.Fatalf("dispatchTrigger(FactoryReset) error: %v", err)
	}

	// Verify all zones removed.
	if len(svc.ListZoneIDs()) != 0 {
		t.Errorf("expected 0 zones after factory reset, got %d", len(svc.ListZoneIDs()))
	}
}

func TestTriggerAdjustClock_SetsOffset(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService: %v", err)
	}

	// Encode +400 seconds in trigger
	trigger := features.TriggerAdjustClockBase | uint64(uint32(int32(400)))
	if err := svc.dispatchTrigger(context.Background(), trigger); err != nil {
		t.Fatalf("dispatchTrigger: %v", err)
	}

	expected := 400 * time.Second
	if svc.clockOffset != expected {
		t.Errorf("clockOffset = %v, want %v", svc.clockOffset, expected)
	}
}

func TestTriggerAdjustClock_NegativeOffset(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService: %v", err)
	}

	// Encode -200 seconds via two's complement
	offsetSec := int32(-200)
	trigger := features.TriggerAdjustClockBase | uint64(uint32(offsetSec))
	if err := svc.dispatchTrigger(context.Background(), trigger); err != nil {
		t.Fatalf("dispatchTrigger: %v", err)
	}

	expected := -200 * time.Second
	if svc.clockOffset != expected {
		t.Errorf("clockOffset = %v, want %v", svc.clockOffset, expected)
	}
}

func TestTriggerResetTestState_ClearsClockOffset(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Set a clock offset first
	trigger := features.TriggerAdjustClockBase | uint64(uint32(int32(400)))
	if err := svc.dispatchTrigger(ctx, trigger); err != nil {
		t.Fatalf("dispatchTrigger(AdjustClock): %v", err)
	}
	if svc.clockOffset == 0 {
		t.Fatal("clockOffset should be non-zero after adjust")
	}

	// Reset should clear it
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}
	if svc.clockOffset != 0 {
		t.Errorf("clockOffset = %v after reset, want 0", svc.clockOffset)
	}
}

func TestTriggerResetTestState_ResetsCommissioningWindowDuration(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Set up a mock advertiser so SetAdvertiser creates a DiscoveryManager.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().StopAll().Return().Maybe()
	advertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	svc.SetAdvertiser(advertiser)

	dm := svc.DiscoveryManager()
	if dm == nil {
		t.Fatal("DiscoveryManager is nil after SetAdvertiser")
	}

	// Verify the config default was applied.
	configDefault := svc.config.CommissioningWindowDuration
	if configDefault == 0 {
		t.Fatal("config.CommissioningWindowDuration should be non-zero")
	}

	// Set a non-default commissioning window duration.
	dm.SetCommissioningWindowDuration(20 * time.Second)
	if dm.CommissioningWindowDuration() != 20*time.Second {
		t.Fatal("CommissioningWindowDuration not set to 20s")
	}

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// Verify commissioning window duration was restored to the config default.
	got := dm.CommissioningWindowDuration()
	if got != configDefault {
		t.Errorf("CommissioningWindowDuration = %v after reset, want %v", got, configDefault)
	}
}

func TestTriggerResetTestState_ReopensCommissioningALPNGate(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Use an ephemeral port so ensureListenerStarted can bind.
	svc.config.OperationalListenAddress = "localhost:0"

	// Mock advertiser: expect the exit+enter cycle during reset.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().StopAll().Return().Maybe()
	advertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	advertiser.EXPECT().AdvertiseCommissionable(
		mock.Anything, mock.Anything,
	).Return(nil).Maybe()
	svc.SetAdvertiser(advertiser)

	// Service must be running for EnterCommissioningMode to succeed.
	svc.state = StateRunning
	svc.ctx, svc.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { svc.cancel() })

	// Open the commissioning window.
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode: %v", err)
	}
	t.Cleanup(func() { svc.stopListener() })

	if !svc.commissioningOpen.Load() {
		t.Fatal("commissioningOpen should be true after EnterCommissioningMode")
	}

	// Reset test state — this must close and re-open the ALPN gate so
	// subsequent commissioning connections (mash-comm/1) still succeed.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// The ALPN gate must be open after reset.
	if !svc.commissioningOpen.Load() {
		t.Error("commissioningOpen should be true after reset (ALPN gate must stay open for next test)")
	}
}

func TestVerifyClientCert_UsesClockOffset(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService: %v", err)
	}

	// Generate a zone CA and store it
	zoneCA, err := cert.GenerateZoneCA("test-zone", cert.ZoneTypeLocal)
	if err != nil {
		t.Fatalf("GenerateZoneCA: %v", err)
	}
	if err := svc.certStore.SetZoneCACert("test-zone", zoneCA.Certificate); err != nil {
		t.Fatalf("SetZoneCACert: %v", err)
	}

	// Generate a valid controller cert (valid for OperationalCertValidity = 365 days)
	controllerCert, err := cert.GenerateControllerOperationalCert(zoneCA, "test-ctrl")
	if err != nil {
		t.Fatalf("GenerateControllerOperationalCert: %v", err)
	}

	// Without offset, cert should be valid
	if err := svc.verifyClientCert(controllerCert.Certificate); err != nil {
		t.Fatalf("cert should be valid without offset: %v", err)
	}

	// With +366d offset, cert should be expired (NotAfter is +365d from cert creation)
	svc.clockOffset = 366 * 24 * time.Hour
	if err := svc.verifyClientCert(controllerCert.Certificate); err == nil {
		t.Error("cert should be rejected with +366d clock offset")
	}

	// Reset offset, should be valid again
	svc.clockOffset = 0
	if err := svc.verifyClientCert(controllerCert.Certificate); err != nil {
		t.Errorf("cert should be valid after clearing offset: %v", err)
	}
}

func TestTriggerResetTestState_ResetsFailsafeTimers(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Create a failsafe timer and start it (simulates a prior test
	// that set a failsafe but didn't clean up).
	timer := failsafe.NewTimer()
	timer.Start()
	if timer.State() != failsafe.StateTimerRunning {
		t.Fatal("failsafe timer should be TimerRunning after Start()")
	}

	svc.mu.Lock()
	svc.failsafeTimers["test-zone"] = timer
	svc.mu.Unlock()

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// Failsafe timer should be reset to Normal.
	if timer.State() != failsafe.StateNormal {
		t.Errorf("failsafe timer state = %v after reset, want Normal", timer.State())
	}
}

func TestTriggerResetTestState_ClearsConnTracker(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Create a pipe to simulate a pre-operational connection tracked
	// by connTracker (e.g., from a failed commissioning attempt).
	server, client := net.Pipe()
	t.Cleanup(func() {
		server.Close()
		client.Close()
	})

	svc.connTracker.Add(server)
	if svc.connTracker.Len() != 1 {
		t.Fatal("expected 1 tracked connection before reset")
	}

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// ConnTracker should be empty.
	if svc.connTracker.Len() != 0 {
		t.Errorf("connTracker.Len() = %d after reset, want 0", svc.connTracker.Len())
	}
}

func TestTriggerResetTestState_DrainsActiveConns(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Simulate 2 pre-operational connections that hold activeConns slots
	// (as happens when connTracker.CloseAll runs but goroutine defers
	// haven't decremented activeConns yet).
	server1, client1 := net.Pipe()
	server2, client2 := net.Pipe()
	t.Cleanup(func() {
		server1.Close()
		client1.Close()
		server2.Close()
		client2.Close()
	})

	svc.connTracker.Add(server1)
	svc.connTracker.Add(server2)
	svc.activeConns.Store(2)

	// Simulate goroutines that decrement activeConns when their conn is closed.
	for _, conn := range []net.Conn{server1, server2} {
		go func(c net.Conn) {
			buf := make([]byte, 1)
			c.Read(buf) // blocks until conn is closed
			svc.activeConns.Add(-1)
		}(conn)
	}

	// Reset test state -- should close connections and wait for drain.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// After reset, activeConns should be 0.
	if got := svc.activeConns.Load(); got != 0 {
		t.Errorf("activeConns = %d after reset, want 0", got)
	}
}

func TestTriggerResetTestState_ResetsAutoReentryPending(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Simulate a prior test that set autoReentryPending.
	svc.autoReentryPending = true

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	if svc.autoReentryPending {
		t.Error("autoReentryPending should be false after reset")
	}
}

func TestTriggerResetTestState_ClearsGlobalSubscriptionManager(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Add a subscription to the global subscription manager to simulate
	// a subscription that accumulated from a prior test.
	_, err := svc.subscriptionManager.Subscribe(
		1, 2, // endpointID=1, featureID=2
		[]uint16{1}, // attributeID=1
		1*time.Second, 10*time.Second,
		map[uint16]any{1: uint8(0)},
	)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if svc.subscriptionManager.Count() != 1 {
		t.Fatal("expected 1 subscription before reset")
	}

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// Global subscription manager should be empty.
	if svc.subscriptionManager.Count() != 0 {
		t.Errorf("subscriptionManager.Count() = %d after reset, want 0",
			svc.subscriptionManager.Count())
	}
}

func TestTriggerResetTestState_RestartsPairingRequestListening(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.ListenForPairingRequests = true

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock browser so StartPairingRequestListening succeeds.
	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().BrowsePairingRequests(mock.Anything, mock.Anything).
		Return(nil).Maybe()
	svc.SetBrowser(browser)

	// Start the service so ctx is set.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// After Start, listening should be active.
	if !svc.IsPairingRequestListening() {
		t.Fatal("expected listening active after Start")
	}

	// Reset test state -- should stop then restart listening.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	if !svc.IsPairingRequestListening() {
		t.Error("pairing request listening should be restarted after reset when ListenForPairingRequests=true")
	}
}

func TestTriggerResetTestState_NoRestartWhenListeningDisabled(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// ListenForPairingRequests is false by default in validDeviceConfig.
	// Simulate a prior test leaving pairing request active.
	svc.mu.Lock()
	svc.pairingRequestActive = true
	svc.mu.Unlock()

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	if svc.IsPairingRequestListening() {
		t.Error("pairingRequestActive should be false after reset when ListenForPairingRequests=false")
	}
}

// ---------------------------------------------------------------------------
// Zone type enforcement: TestControl commands must only be accepted on TEST zones
// ---------------------------------------------------------------------------

// newDeviceServiceWithTestControl creates a DeviceService with a registered
// TestControl feature on endpoint 0. Returns the service and the feature so
// callers can invoke commands directly via feature.InvokeCommand.
func newDeviceServiceWithTestControl(t *testing.T) (*DeviceService, *model.Feature) {
	t.Helper()

	device := model.NewDevice("test-device", 0x1234, 0x5678)

	// Add a functional endpoint so feature resets have something to operate on.
	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Test Charger")
	charger.AddFeature(features.NewStatus().Feature)
	if err := device.AddEndpoint(charger); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}

	config := validDeviceConfig()
	config.TestEnableKey = "test-key-abc"
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService: %v", err)
	}

	tc := features.NewTestControl()
	svc.RegisterTestEventHandler(tc)
	svc.RegisterSetCommissioningWindowDurationHandler(tc)
	svc.RegisterGetTestStateHandler(tc)

	// Get endpoint 0 and add the feature.
	ep0, err := device.GetEndpoint(0)
	if err != nil {
		t.Fatalf("GetEndpoint(0): %v", err)
	}
	ep0.AddFeature(tc.Feature)

	return svc, tc.Feature
}

func TestTestControl_TriggerTestEvent_RejectedOnGridZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "grid-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeGrid)

	params := map[uint8]any{
		features.TriggerTestEventParamEnableKey:    "test-key-abc",
		features.TriggerTestEventParamEventTrigger: features.TriggerResetTestState,
	}
	_, err := feat.InvokeCommand(ctx, features.TestControlCmdTriggerTestEvent, params)
	if err == nil {
		t.Fatal("expected error: TriggerTestEvent should be rejected on GRID zone")
	}
}

func TestTestControl_TriggerTestEvent_RejectedOnLocalZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "local-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeLocal)

	params := map[uint8]any{
		features.TriggerTestEventParamEnableKey:    "test-key-abc",
		features.TriggerTestEventParamEventTrigger: features.TriggerResetTestState,
	}
	_, err := feat.InvokeCommand(ctx, features.TestControlCmdTriggerTestEvent, params)
	if err == nil {
		t.Fatal("expected error: TriggerTestEvent should be rejected on LOCAL zone")
	}
}

func TestTestControl_TriggerTestEvent_AcceptedOnTestZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "test-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeTest)

	params := map[uint8]any{
		features.TriggerTestEventParamEnableKey:    "test-key-abc",
		features.TriggerTestEventParamEventTrigger: features.TriggerResetTestState,
	}
	_, err := feat.InvokeCommand(ctx, features.TestControlCmdTriggerTestEvent, params)
	if err != nil {
		t.Fatalf("TriggerTestEvent should succeed on TEST zone: %v", err)
	}
}

func TestTestControl_SetCommWindowDuration_RejectedOnGridZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "grid-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeGrid)

	params := map[uint8]any{
		features.SetCommissioningWindowDurationParamEnableKey:       "test-key-abc",
		features.SetCommissioningWindowDurationParamDurationSeconds: uint32(60),
	}
	_, err := feat.InvokeCommand(ctx, features.TestControlCmdSetCommissioningWindowDuration, params)
	if err == nil {
		t.Fatal("expected error: SetCommissioningWindowDuration should be rejected on GRID zone")
	}
}

func TestTestControl_SetCommWindowDuration_AcceptedOnTestZone(t *testing.T) {
	svc, feat := newDeviceServiceWithTestControl(t)

	// SetCommissioningWindowDuration needs a DiscoveryManager.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	ctx := ContextWithCallerZoneID(context.Background(), "test-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeTest)

	params := map[uint8]any{
		features.SetCommissioningWindowDurationParamEnableKey:       "test-key-abc",
		features.SetCommissioningWindowDurationParamDurationSeconds: uint32(60),
	}
	_, err := feat.InvokeCommand(ctx, features.TestControlCmdSetCommissioningWindowDuration, params)
	if err != nil {
		t.Fatalf("SetCommissioningWindowDuration should succeed on TEST zone: %v", err)
	}
}

func TestTestControl_GetTestState_RejectedOnLocalZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "local-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeLocal)

	params := map[uint8]any{
		1: "test-key-abc", // GetTestState param 1 = enableKey
	}
	_, err := feat.InvokeCommand(ctx, TestControlCmdGetTestState, params)
	if err == nil {
		t.Fatal("expected error: GetTestState should be rejected on LOCAL zone")
	}
}

func TestTestControl_GetTestState_AcceptedOnTestZone(t *testing.T) {
	_, feat := newDeviceServiceWithTestControl(t)

	ctx := ContextWithCallerZoneID(context.Background(), "test-zone-1")
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeTest)

	params := map[uint8]any{
		1: "test-key-abc", // GetTestState param 1 = enableKey
	}
	result, err := feat.InvokeCommand(ctx, TestControlCmdGetTestState, params)
	if err != nil {
		t.Fatalf("GetTestState should succeed on TEST zone: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result from GetTestState")
	}
}

func TestTriggerResetTestState_RemovesLeakedZones(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Simulate a leaked zone WITHOUT a session (from a failed PASE handshake).
	// This is the exact scenario: PASE creates a zone record in connectedZones,
	// but the connection dies (EOF) before operational TLS establishes a session.
	leakedZoneID := "leaked-zone-xyz"
	svc.mu.Lock()
	svc.connectedZones[leakedZoneID] = &ConnectedZone{
		ID:        leakedZoneID,
		Type:      cert.ZoneTypeLocal,
		Priority:  2,
		Connected: false,
	}
	if svc.failsafeTimers == nil {
		svc.failsafeTimers = make(map[string]*failsafe.Timer)
	}
	svc.failsafeTimers[leakedZoneID] = failsafe.NewTimer()
	svc.mu.Unlock()

	// Verify zone exists before reset.
	if len(svc.ListZoneIDs()) != 1 {
		t.Fatalf("expected 1 zone before reset, got %d", len(svc.ListZoneIDs()))
	}

	// Reset test state.
	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	// Leaked zone (no session) should be removed.
	zoneIDs := svc.ListZoneIDs()
	if len(zoneIDs) != 0 {
		t.Fatalf("expected 0 zones after reset, got %d: %v", len(zoneIDs), zoneIDs)
	}
}

func TestTriggerResetTestState_RemovesDisconnectedZoneWithStaleSessionEntry(t *testing.T) {
	svc := newDeviceServiceWithAllFeatures(t)
	ctx := context.Background()

	// Reproduces teardown race: zone is already marked disconnected, but
	// zoneSessions still carries an entry until async close completes.
	zoneID := "disconnected-stale-session-zone"
	svc.mu.Lock()
	svc.connectedZones[zoneID] = &ConnectedZone{
		ID:        zoneID,
		Type:      cert.ZoneTypeLocal,
		Priority:  2,
		Connected: false,
	}
	svc.zoneSessions[zoneID] = NewZoneSession(zoneID, newMockSendableConnection(), svc.device)
	svc.mu.Unlock()

	if len(svc.ListZoneIDs()) != 1 {
		t.Fatalf("expected 1 zone before reset, got %d", len(svc.ListZoneIDs()))
	}

	if err := svc.dispatchTrigger(ctx, features.TriggerResetTestState); err != nil {
		t.Fatalf("dispatchTrigger(ResetTestState): %v", err)
	}

	zoneIDs := svc.ListZoneIDs()
	if len(zoneIDs) != 0 {
		t.Fatalf("expected 0 zones after reset, got %d: %v", len(zoneIDs), zoneIDs)
	}
}
