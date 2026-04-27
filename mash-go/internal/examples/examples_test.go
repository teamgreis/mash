package examples

import (
	"context"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/interaction"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/usecase"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

func TestEVSEUseCases(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	ucs := evse.deviceInfo.UseCases()
	if len(ucs) == 0 {
		t.Fatal("expected EVSE to declare use cases")
	}

	// Build lookup by ID
	ucMap := make(map[uint16]*model.UseCaseDecl)
	for _, uc := range ucs {
		ucMap[uc.ID] = uc
	}

	// EVSE should match GPL, MPD, EVC at minimum
	for _, id := range []usecase.UseCaseID{usecase.GPLID, usecase.MPDID, usecase.EVCID} {
		uc, ok := ucMap[uint16(id)]
		if !ok {
			t.Errorf("expected use case 0x%02X to be declared", id)
			continue
		}
		if uc.EndpointID != 1 {
			t.Errorf("0x%02X: EndpointID = %d, want 1", id, uc.EndpointID)
		}
		if uc.Major != 1 {
			t.Errorf("0x%02X: Major = %d, want 1", id, uc.Major)
		}
	}
}

func TestEVSECreation(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000, // 32A
		MinCurrentPerPhase: 6000,  // 6A
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	if evse.Device() == nil {
		t.Fatal("expected device to be created")
	}

	// Check device has correct structure
	if evse.Device().EndpointCount() != 2 {
		t.Errorf("expected 2 endpoints (root + charger), got %d", evse.Device().EndpointCount())
	}

	// Endpoint 1 should have Signals and Plan features
	ep1, err := evse.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1): %v", err)
	}
	if _, err := ep1.GetFeature(model.FeatureSignals); err != nil {
		t.Error("charger endpoint missing Signals feature")
	}
	if _, err := ep1.GetFeature(model.FeaturePlan); err != nil {
		t.Error("charger endpoint missing Plan feature")
	}

	// Feature map should include Signals and Plan bits
	sigFeature, sigErr := ep1.GetFeature(model.FeatureSignals)
	if sigErr != nil {
		t.Fatal("cannot check featureMap: Signals feature missing")
	}
	fmap := sigFeature.FeatureMap()
	if fmap&uint32(model.FeatureMapSignals) == 0 {
		t.Error("featureMap missing Signals bit")
	}
	if fmap&uint32(model.FeatureMapPlan) == 0 {
		t.Error("featureMap missing Plan bit")
	}
}

func TestCEMUseCases(t *testing.T) {
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	ucs := cem.deviceInfo.UseCases()
	if len(ucs) == 0 {
		t.Fatal("expected CEM to declare use cases")
	}

	// Build lookup by ID
	ucMap := make(map[uint16]*model.UseCaseDecl)
	for _, uc := range ucs {
		ucMap[uc.ID] = uc
	}

	// CEM should declare at least GPL, MPD, EVC, COB
	for _, id := range []usecase.UseCaseID{usecase.GPLID, usecase.MPDID, usecase.EVCID, usecase.COBID} {
		uc, ok := ucMap[uint16(id)]
		if !ok {
			t.Errorf("expected use case 0x%02X to be declared", id)
			continue
		}
		if uc.EndpointID != 0 {
			t.Errorf("0x%02X: EndpointID = %d, want 0", id, uc.EndpointID)
		}
		if uc.Major != 1 {
			t.Errorf("0x%02X: Major = %d, want 1", id, uc.Major)
		}
	}

	// All declarations should have EndpointID 0
	for _, uc := range ucs {
		if uc.EndpointID != 0 {
			t.Errorf("0x%02X: EndpointID = %d, want 0 (controller-side)", uc.ID, uc.EndpointID)
		}
	}
}

func TestCEMCreation(t *testing.T) {
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	if cem.Device() == nil {
		t.Fatal("expected device to be created")
	}

	// CEM only has root endpoint
	if cem.Device().EndpointCount() != 1 {
		t.Errorf("expected 1 endpoint (root), got %d", cem.Device().EndpointCount())
	}
}

func TestEVSECharging(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	// Simulate EV connecting
	evse.SimulateEVConnect(40, 80000000, features.EVDemandModeDynamic) // 40% SoC, 80 kWh

	// Simulate charging at 11 kW
	evse.SimulateCharging(11000000)

	if evse.GetCurrentPower() != 11000000 {
		t.Errorf("expected power 11000000, got %d", evse.GetCurrentPower())
	}

	// Disconnect
	evse.SimulateEVDisconnect()

	if evse.GetCurrentPower() != 0 {
		t.Errorf("expected power 0 after disconnect, got %d", evse.GetCurrentPower())
	}
}

// mockRequestSender captures requests for testing (one-way, discards responses)
type mockRequestSender struct {
	server *interaction.Server
}

func (m *mockRequestSender) Send(data []byte) error {
	// Decode the request
	req, err := wire.DecodeRequest(data)
	if err != nil {
		return err
	}

	// Process through server
	resp := m.server.HandleRequest(context.Background(), req)

	// The response would normally go back through the wire
	// For testing, we just verify it worked
	_ = resp
	return nil
}

// roundTripSender routes responses back to the client, enabling Subscribe/Read via interaction.Client.
type roundTripSender struct {
	server *interaction.Server
	client *interaction.Client
}

func (m *roundTripSender) Send(data []byte) error {
	req, err := wire.DecodeRequest(data)
	if err != nil {
		return err
	}

	resp := m.server.HandleRequest(context.Background(), req)

	// Route response back to client asynchronously (like a real transport would)
	go m.client.HandleResponse(resp)
	return nil
}

func TestZoneInteraction(t *testing.T) {
	// Create EVSE and its interaction server
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	// Mark EVSE as accepting control
	evse.AcceptController()

	// Create CEM
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	// Simulate connection by creating a client connected to the EVSE server
	// In a real implementation, this would go through SHIP transport
	mockSender := &mockRequestSender{server: evseServer}
	client := interaction.NewClient(mockSender)
	client.SetTimeout(5 * time.Second)

	// CEM connects to EVSE
	_, err := cem.ConnectDevice("PEN12345.EVSE-001", client)
	if err != nil {
		t.Fatalf("ConnectDevice failed: %v", err)
	}

	// Verify connection
	ids := cem.ConnectedDeviceIDs()
	if len(ids) != 1 || ids[0] != "PEN12345.EVSE-001" {
		t.Errorf("expected connected device PEN12345.EVSE-001, got %v", ids)
	}

	// Simulate EV connecting
	evse.SimulateEVConnect(40, 80000000, features.EVDemandModeDynamic)
	evse.SimulateCharging(22000000) // Full 22 kW

	if evse.GetCurrentPower() != 22000000 {
		t.Errorf("expected power 22000000, got %d", evse.GetCurrentPower())
	}

	// CEM sets a limit via the interaction model.
	// LimitResolver requires zone identity on the context.
	ctx := context.Background()
	ctx = service.ContextWithCallerZoneID(ctx, "zone-cem-001")
	ctx = service.ContextWithCallerZoneType(ctx, cert.ZoneTypeLocal)

	// Direct command invocation since we're simulating the wire protocol
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID: features.EnergyControlCmdSetLimit,
			Parameters: map[uint8]any{
				features.SetLimitParamConsumptionLimit: int64(11000000), // 11 kW limit
				features.SetLimitParamCause:            uint8(features.LimitCauseLocalOptimization),
			},
		},
	}

	resp := evseServer.HandleRequest(ctx, req)
	if !resp.Status.IsSuccess() {
		t.Fatalf("SetLimit failed: %v", resp.Status)
	}

	// Verify the limit was applied
	effectiveLimit := evse.GetEffectiveLimit()
	if effectiveLimit == nil {
		t.Fatal("expected effective limit to be set")
	}
	if *effectiveLimit != 11000000 {
		t.Errorf("expected effective limit 11000000, got %d", *effectiveLimit)
	}

	// EVSE should now respect the limit
	evse.SimulateCharging(22000000) // Try to charge at 22 kW
	if evse.GetCurrentPower() != 11000000 {
		t.Errorf("expected power limited to 11000000, got %d", evse.GetCurrentPower())
	}

	// Clear limit
	clearReq := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID:  features.EnergyControlCmdClearLimit,
			Parameters: map[uint8]any{},
		},
	}

	resp = evseServer.HandleRequest(ctx, clearReq)
	if !resp.Status.IsSuccess() {
		t.Fatalf("ClearLimit failed: %v", resp.Status)
	}

	if evse.GetEffectiveLimit() != nil {
		t.Error("expected effective limit to be cleared")
	}

	// Disconnect
	err = cem.DisconnectDevice("PEN12345.EVSE-001")
	if err != nil {
		t.Fatalf("DisconnectDevice failed: %v", err)
	}

	if len(cem.ConnectedDeviceIDs()) != 0 {
		t.Error("expected no connected devices after disconnect")
	}
}

func TestReadMeasurements(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	// Set some measurement values
	evse.SimulateEVConnect(60, 80000000, features.EVDemandModeDynamic)
	evse.SimulateCharging(11000000)

	// Read measurements through interaction model
	ctx := context.Background()
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureMeasurement),
		Payload:    []uint16{1}, // ACActivePower
	}

	resp := evseServer.HandleRequest(ctx, req)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Read failed: %v", resp.Status)
	}

	attrs, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatal("expected map payload")
	}

	power, ok := attrs[1].(int64)
	if !ok || power != 11000000 {
		t.Errorf("expected power 11000000, got %v", attrs[1])
	}
}

func TestSubscribeToMeasurements(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	// Track notifications
	var notifications []*wire.Notification
	evseServer.SetNotificationHandler(func(notif *wire.Notification) {
		notifications = append(notifications, notif)
	})

	// Set initial measurement
	evse.SimulateEVConnect(60, 80000000, features.EVDemandModeDynamic)
	evse.SimulateCharging(11000000)

	// Subscribe to measurements
	ctx := context.Background()
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpSubscribe,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureMeasurement),
		Payload: &wire.SubscribePayload{
			AttributeIDs: []uint16{1}, // ACActivePower
			MinInterval:  0,           // No delay for testing
			MaxInterval:  60000,
		},
	}

	resp := evseServer.HandleRequest(ctx, req)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Subscribe failed: %v", resp.Status)
	}

	subResp, ok := resp.Payload.(*wire.SubscribeResponsePayload)
	if !ok {
		t.Fatal("expected SubscribeResponsePayload")
	}

	if subResp.SubscriptionID == 0 {
		t.Error("expected non-zero subscription ID")
	}

	// Priming report should contain current value
	if subResp.CurrentValues == nil {
		t.Fatal("expected priming report")
	}

	power, ok := subResp.CurrentValues[1].(int64)
	if !ok || power != 11000000 {
		t.Errorf("expected priming report with power 11000000, got %v", subResp.CurrentValues[1])
	}

	// Verify subscription count
	if evseServer.SubscriptionCount() != 1 {
		t.Errorf("expected 1 subscription, got %d", evseServer.SubscriptionCount())
	}
}

func TestSubscribeToEnergyControl(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	// Track notifications
	var notifications []*wire.Notification
	evseServer.SetNotificationHandler(func(notif *wire.Notification) {
		notifications = append(notifications, notif)
	})

	// Create CEM and connect
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	sender := &roundTripSender{server: evseServer}
	client := interaction.NewClient(sender)
	sender.client = client
	client.SetTimeout(5 * time.Second)

	_, err := cem.ConnectDevice("PEN12345.EVSE-001", client)
	if err != nil {
		t.Fatalf("ConnectDevice failed: %v", err)
	}

	// Mark EVSE as accepting control
	evse.AcceptController()

	// Subscribe to EnergyControl on endpoint 1
	ctx := context.Background()
	if err := cem.SubscribeToEnergyControl(ctx, "PEN12345.EVSE-001", 1); err != nil {
		t.Fatalf("SubscribeToEnergyControl failed: %v", err)
	}

	// Verify priming report populated control state
	// AcceptController() sets state to CONTROLLED, so that's what we expect
	device := cem.GetDevice("PEN12345.EVSE-001")
	if device == nil {
		t.Fatal("expected device to exist")
	}
	if device.ControlState != features.ControlStateControlled {
		t.Errorf("expected CONTROLLED control state from priming report, got %s", device.ControlState)
	}

	// Verify subscription ID is tracked
	if len(device.SubscriptionIDs) != 1 {
		t.Errorf("expected 1 subscription ID, got %d", len(device.SubscriptionIDs))
	}

	// Verify server has the subscription
	if evseServer.SubscriptionCount() != 1 {
		t.Errorf("expected 1 subscription on server, got %d", evseServer.SubscriptionCount())
	}
}

func TestHandleNotification_Signals(t *testing.T) {
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	_, _ = cem.ConnectDevice("dev-1", nil)

	// Simulate Signals notification with signalSource
	cem.HandleNotification("dev-1", 1, uint8(model.FeatureSignals), map[uint16]any{
		features.SignalsAttrSignalSource: uint8(features.SignalSourceLocalEms),
	})

	device := cem.GetDevice("dev-1")
	if device.SignalSource == nil {
		t.Fatal("expected SignalSource to be set")
	}
	if *device.SignalSource != features.SignalSourceLocalEms {
		t.Errorf("SignalSource = %v, want LOCAL_EMS", *device.SignalSource)
	}

	// Simulate priceSlots arriving (non-nil indicates active signal)
	cem.HandleNotification("dev-1", 1, uint8(model.FeatureSignals), map[uint16]any{
		features.SignalsAttrPriceSlots: []any{map[string]any{"duration": uint32(3600)}},
	})
	if !device.HasPriceSignal {
		t.Error("expected HasPriceSignal to be true after priceSlots notification")
	}

	// Simulate constraintSlots arriving
	cem.HandleNotification("dev-1", 1, uint8(model.FeatureSignals), map[uint16]any{
		features.SignalsAttrConstraintSlots: []any{map[string]any{"duration": uint32(1800)}},
	})
	if !device.HasConstraintSignal {
		t.Error("expected HasConstraintSignal to be true after constraintSlots notification")
	}

	// Clear signalSource (nil value)
	cem.HandleNotification("dev-1", 1, uint8(model.FeatureSignals), map[uint16]any{
		features.SignalsAttrSignalSource: nil,
	})
	if device.SignalSource != nil {
		t.Error("expected SignalSource to be nil after clear")
	}
}

func TestHandleNotification_Plan(t *testing.T) {
	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	_, _ = cem.ConnectDevice("dev-1", nil)

	// Simulate Plan notification with planId and commitment
	cem.HandleNotification("dev-1", 1, uint8(model.FeaturePlan), map[uint16]any{
		features.PlanAttrPlanID:     uint32(42),
		features.PlanAttrCommitment: uint8(features.CommitmentCommitted),
	})

	device := cem.GetDevice("dev-1")
	if device.PlanID == nil || *device.PlanID != 42 {
		t.Errorf("PlanID = %v, want 42", device.PlanID)
	}
	if device.PlanCommitment == nil || *device.PlanCommitment != features.CommitmentCommitted {
		t.Errorf("PlanCommitment = %v, want COMMITTED", device.PlanCommitment)
	}
}

func TestCEM_SendPriceSignal(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	sender := &roundTripSender{server: evseServer}
	client := interaction.NewClient(sender)
	sender.client = client
	client.SetTimeout(5 * time.Second)

	_, err := cem.ConnectDevice("PEN12345.EVSE-001", client)
	if err != nil {
		t.Fatalf("ConnectDevice failed: %v", err)
	}

	ctx := context.Background()
	slots := []features.PriceSlot{
		{Duration: 3600, Price: 250, PriceLevel: 2},
		{Duration: 3600, Price: 180, PriceLevel: 1},
	}

	err = cem.SendPriceSignal(ctx, "PEN12345.EVSE-001", 1, features.SignalSourceLocalEms, 1700000000, nil, slots)
	if err != nil {
		t.Fatalf("SendPriceSignal failed: %v", err)
	}
}

func TestCEM_RequestAndAcceptPlan(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	sender := &roundTripSender{server: evseServer}
	client := interaction.NewClient(sender)
	sender.client = client
	client.SetTimeout(5 * time.Second)

	_, err := cem.ConnectDevice("PEN12345.EVSE-001", client)
	if err != nil {
		t.Fatalf("ConnectDevice failed: %v", err)
	}

	ctx := context.Background()

	// Request plan
	planID, err := cem.RequestPlan(ctx, "PEN12345.EVSE-001", 1)
	if err != nil {
		t.Fatalf("RequestPlan failed: %v", err)
	}
	// The EVSE handler returns current planID (defaults to 0)
	if planID != 0 {
		t.Errorf("expected planID 0, got %d", planID)
	}

	// Accept plan
	commitment, err := cem.AcceptPlan(ctx, "PEN12345.EVSE-001", 1, planID)
	if err != nil {
		t.Fatalf("AcceptPlan failed: %v", err)
	}
	// The EVSE handler commits the plan when IDs match
	if commitment != features.CommitmentCommitted {
		t.Errorf("expected COMMITTED, got %s", commitment)
	}
}

func TestCEM_ClearSignals(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	cem := NewCEM(CEMConfig{
		DeviceID:     "PEN67890.CEM-001",
		VendorName:   "Test Vendor",
		ProductName:  "Test CEM",
		SerialNumber: "SN-CEM-001",
		VendorID:     67890,
		ProductID:    200,
	})

	sender := &roundTripSender{server: evseServer}
	client := interaction.NewClient(sender)
	sender.client = client
	client.SetTimeout(5 * time.Second)

	_, err := cem.ConnectDevice("PEN12345.EVSE-001", client)
	if err != nil {
		t.Fatalf("ConnectDevice failed: %v", err)
	}

	ctx := context.Background()
	err = cem.ClearSignals(ctx, "PEN12345.EVSE-001", 1)
	if err != nil {
		t.Fatalf("ClearSignals failed: %v", err)
	}
}

func TestPauseResume(t *testing.T) {
	evse := NewEVSE(EVSEConfig{
		DeviceID:           "PEN12345.EVSE-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Charger",
		SerialNumber:       "SN-001",
		VendorID:           12345,
		ProductID:          100,
		PhaseCount:         3,
		NominalVoltage:     400,
		MaxCurrentPerPhase: 32000,
		MinCurrentPerPhase: 6000,
		NominalMaxPower:    22000000,
		NominalMinPower:    1400000,
	})

	evseServer := interaction.NewServer(evse.Device())

	// Start charging
	evse.SimulateEVConnect(40, 80000000, features.EVDemandModeDynamic)
	evse.SimulateCharging(11000000)

	ctx := context.Background()

	// Pause
	pauseReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID:  features.EnergyControlCmdPause,
			Parameters: map[uint8]any{},
		},
	}

	resp := evseServer.HandleRequest(ctx, pauseReq)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Pause failed: %v", resp.Status)
	}

	// Power should be 0 after pause
	if evse.GetCurrentPower() != 0 {
		t.Errorf("expected power 0 after pause, got %d", evse.GetCurrentPower())
	}

	// Resume
	resumeReq := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID:  features.EnergyControlCmdResume,
			Parameters: map[uint8]any{},
		},
	}

	resp = evseServer.HandleRequest(ctx, resumeReq)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Resume failed: %v", resp.Status)
	}

	// Simulate charging resuming
	evse.SimulateCharging(11000000)

	if evse.GetCurrentPower() != 11000000 {
		t.Errorf("expected power 11000000 after resume, got %d", evse.GetCurrentPower())
	}
}

func TestHeatPumpCreation(t *testing.T) {
	hp := NewHeatPump(HeatPumpConfig{
		DeviceID:           "PEN12345.HP-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Heat Pump",
		SerialNumber:       "SN-HP-001",
		VendorID:           12345,
		ProductID:          300,
		PhaseCount:         3,
		NominalVoltage:     230,
		NominalMaxPower:    8000000,  // 8 kW
		NominalMinPower:    1500000,  // 1.5 kW
		MaxCurrentPerPhase: 12000,    // 12A
	})

	if hp.Device() == nil {
		t.Fatal("expected device to be created")
	}

	// Check structure: root + heat pump endpoint
	if hp.Device().EndpointCount() != 2 {
		t.Errorf("expected 2 endpoints, got %d", hp.Device().EndpointCount())
	}

	// Endpoint 1 should be HEAT_PUMP type
	ep1, err := hp.Device().GetEndpoint(1)
	if err != nil {
		t.Fatalf("GetEndpoint(1): %v", err)
	}
	if ep1.Info().Type != model.EndpointHeatPump {
		t.Errorf("endpoint type = %v, want EndpointHeatPump", ep1.Info().Type)
	}

	// Check features
	if _, err := ep1.GetFeature(model.FeatureElectrical); err != nil {
		t.Error("missing Electrical feature")
	}
	if _, err := ep1.GetFeature(model.FeatureMeasurement); err != nil {
		t.Error("missing Measurement feature")
	}
	if _, err := ep1.GetFeature(model.FeatureEnergyControl); err != nil {
		t.Error("missing EnergyControl feature")
	}
	if _, err := ep1.GetFeature(model.FeatureStatus); err != nil {
		t.Error("missing Status feature")
	}
}

func TestHeatPumpPauseResume(t *testing.T) {
	hp := NewHeatPump(HeatPumpConfig{
		DeviceID:           "PEN12345.HP-001",
		VendorName:         "Test Vendor",
		ProductName:        "Test Heat Pump",
		SerialNumber:       "SN-HP-001",
		VendorID:           12345,
		ProductID:          300,
		PhaseCount:         3,
		NominalVoltage:     230,
		NominalMaxPower:    8000000,
		NominalMinPower:    1500000,
		MaxCurrentPerPhase: 12000,
	})

	hpServer := interaction.NewServer(hp.Device())

	// Simulate heat pump running
	hp.SimulateHeating(5000000) // 5 kW

	if hp.GetCurrentPower() != 5000000 {
		t.Errorf("expected power 5000000, got %d", hp.GetCurrentPower())
	}

	ctx := context.Background()

	// Pause
	pauseReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID:  features.EnergyControlCmdPause,
			Parameters: map[uint8]any{},
		},
	}

	resp := hpServer.HandleRequest(ctx, pauseReq)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Pause failed: %v", resp.Status)
	}

	if hp.GetCurrentPower() != 0 {
		t.Errorf("expected power 0 after pause, got %d", hp.GetCurrentPower())
	}

	// Resume
	resumeReq := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpInvoke,
		EndpointID: 1,
		FeatureID:  uint8(model.FeatureEnergyControl),
		Payload: &wire.InvokePayload{
			CommandID:  features.EnergyControlCmdResume,
			Parameters: map[uint8]any{},
		},
	}

	resp = hpServer.HandleRequest(ctx, resumeReq)
	if !resp.Status.IsSuccess() {
		t.Fatalf("Resume failed: %v", resp.Status)
	}
}
