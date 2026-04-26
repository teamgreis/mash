//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// ============================================================================
// Phase 5: Bidirectional Communication Integration Tests
// ============================================================================
//
// These tests verify that bidirectional communication works end-to-end:
// - Controller can expose features that devices can read
// - Both sides can subscribe to each other
// - The dual-service pattern (EMS) works correctly

// TestBidirectional_ControllerExposesFeatures tests Scenario 1:
// Controller exposes read-only features that device can query.
func TestBidirectional_ControllerExposesFeatures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-bidir-001", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 1111

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// === Setup Controller with Exposed Features ===
	// Create a device model that the controller will expose (e.g., meter data)
	controllerExposedDevice := model.NewDevice("smgw-meter", 0x2000, 0x0001)

	// Add a Measurement endpoint with meter data
	meterEndpoint := model.NewEndpoint(1, model.EndpointGridConnection, "Grid Meter")
	measurementFeature := model.NewFeature(model.FeatureMeasurement, 1)
	measurementFeature.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      1, // ACActivePower
		Name:    "ACActivePower",
		Type:    model.DataTypeInt64,
		Access:  model.AccessRead,
		Default: int64(5000000), // 5 kW
	}))
	measurementFeature.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      2, // ACVoltage
		Name:    "ACVoltage",
		Type:    model.DataTypeInt32,
		Access:  model.AccessRead,
		Default: int32(230000), // 230V in millivolts
	}))
	meterEndpoint.AddFeature(measurementFeature)
	_ = controllerExposedDevice.AddEndpoint(meterEndpoint)

	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Smart Meter Gateway"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// === Commission Device ===
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-1111",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1111,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// === Configure Controller Session to Expose Features ===
	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found for commissioned device")
	}

	// Set exposed device on the session
	session.SetExposedDevice(controllerExposedDevice)

	// === Device Reads from Controller ===
	// Get the ZoneSession on the device side
	zoneID := controllerSvc.ZoneID()
	deviceSvc.mu.RLock()
	zoneSession := deviceSvc.zoneSessions[zoneID]
	deviceSvc.mu.RUnlock()

	if zoneSession == nil {
		t.Fatal("Device should have a zone session")
	}

	// Device reads Measurement feature from controller's meter endpoint
	attrs, err := zoneSession.Read(ctx, 1, uint8(model.FeatureMeasurement), nil)
	if err != nil {
		t.Fatalf("Device Read from controller failed: %v", err)
	}

	if attrs == nil {
		t.Fatal("Expected attributes from controller")
	}

	t.Logf("Device read %d attributes from controller's meter", len(attrs))

	// Verify specific attributes
	// Note: CBOR may return values with different key types
	found := false
	for k, v := range attrs {
		t.Logf("  Attr %v = %v", k, v)
		if k == 1 { // ACActivePower
			found = true
		}
	}

	if !found {
		t.Error("Expected to find ACActivePower attribute")
	}
}

// TestBidirectional_ControllerWithoutExposedFeatures verifies that
// when controller doesn't expose features, device gets StatusUnsupported.
func TestBidirectional_ControllerWithoutExposedFeatures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-bidir-002", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 2222

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// === Setup Controller WITHOUT Exposed Features ===
	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Simple Controller"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission device (no SetExposedDevice called)
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-2222",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 2222,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify session exists but has no exposed device
	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found")
	}

	if session.Handler() != nil {
		t.Error("Session should NOT have a handler when no exposed device configured")
	}

	// === Device Tries to Read from Controller ===
	zoneID := controllerSvc.ZoneID()
	deviceSvc.mu.RLock()
	zoneSession := deviceSvc.zoneSessions[zoneID]
	deviceSvc.mu.RUnlock()

	if zoneSession == nil {
		t.Fatal("Device should have a zone session")
	}

	// This should fail with StatusUnsupported
	_, err = zoneSession.Read(ctx, 0, 1, nil)
	if err == nil {
		t.Fatal("Expected error when controller doesn't expose features")
	}

	t.Logf("Got expected error: %v", err)
}

// TestBidirectional_Subscriptions tests Scenario 2:
// Both sides can subscribe to each other's features.
func TestBidirectional_Subscriptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device with Measurement Feature ===
	device := model.NewDevice("evse-bidir-003", 0x1234, 0x5678)

	// Add Measurement feature that controller will subscribe to
	ep1 := model.NewEndpoint(1, model.EndpointEVCharger, "EV Charger")
	deviceMeasurement := model.NewFeature(model.FeatureMeasurement, 1)
	deviceMeasurement.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      1, // ChargingPower
		Name:    "ChargingPower",
		Type:    model.DataTypeInt64,
		Access:  model.AccessReadWrite,
		Default: int64(0),
	}))
	ep1.AddFeature(deviceMeasurement)
	_ = device.AddEndpoint(ep1)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 3333

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// === Setup Controller with Exposed Meter Feature ===
	controllerExposedDevice := model.NewDevice("smgw-003", 0x2000, 0x0001)
	meterEndpoint := model.NewEndpoint(1, model.EndpointGridConnection, "Grid Meter")
	controllerMeasurement := model.NewFeature(model.FeatureMeasurement, 1)
	controllerMeasurement.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      1, // GridPower
		Name:    "GridPower",
		Type:    model.DataTypeInt64,
		Access:  model.AccessReadWrite,
		Default: int64(0),
	}))
	meterEndpoint.AddFeature(controllerMeasurement)
	_ = controllerExposedDevice.AddEndpoint(meterEndpoint)

	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Bidirectional Controller"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// === Commission ===
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-3333",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 3333,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found")
	}

	session.SetExposedDevice(controllerExposedDevice)

	// Get device's zone session
	zoneID := controllerSvc.ZoneID()
	deviceSvc.mu.RLock()
	zoneSession := deviceSvc.zoneSessions[zoneID]
	deviceSvc.mu.RUnlock()

	// === Controller subscribes to device's Measurement ===
	var controllerReceivedNotif *wire.Notification
	var controllerNotifMu sync.Mutex
	controllerNotifCh := make(chan struct{}, 1)

	session.SetNotificationHandler(func(notif *wire.Notification) {
		controllerNotifMu.Lock()
		controllerReceivedNotif = notif
		controllerNotifMu.Unlock()
		select {
		case controllerNotifCh <- struct{}{}:
		default:
		}
	})

	controllerSubID, _, err := session.Subscribe(ctx, 1, uint8(model.FeatureMeasurement), nil)
	if err != nil {
		t.Fatalf("Controller subscribe failed: %v", err)
	}
	t.Logf("Controller subscribed with ID %d", controllerSubID)

	// === Device subscribes to controller's Measurement ===
	var deviceReceivedNotif *wire.Notification
	var deviceNotifMu sync.Mutex
	deviceNotifCh := make(chan struct{}, 1)

	zoneSession.SetNotificationHandler(func(notif *wire.Notification) {
		deviceNotifMu.Lock()
		deviceReceivedNotif = notif
		deviceNotifMu.Unlock()
		select {
		case deviceNotifCh <- struct{}{}:
		default:
		}
	})

	deviceSubID, _, err := zoneSession.Subscribe(ctx, 1, uint8(model.FeatureMeasurement), nil)
	if err != nil {
		t.Fatalf("Device subscribe to controller failed: %v", err)
	}
	t.Logf("Device subscribed with ID %d", deviceSubID)

	// === Trigger notification from device to controller ===
	if err := deviceSvc.NotifyAttributeChange(1, uint8(model.FeatureMeasurement), 1, int64(7000000)); err != nil {
		t.Fatalf("Device NotifyAttributeChange failed: %v", err)
	}

	// Wait for controller to receive notification
	select {
	case <-controllerNotifCh:
		controllerNotifMu.Lock()
		if controllerReceivedNotif == nil {
			t.Error("Controller notification is nil")
		} else {
			t.Logf("Controller received notification: sub=%d, changes=%v",
				controllerReceivedNotif.SubscriptionID, controllerReceivedNotif.Changes)
		}
		controllerNotifMu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("Controller timed out waiting for notification from device")
	}

	// === Trigger notification from controller to device ===
	// Use the session's handler to send notification
	handler := session.Handler()
	if handler != nil {
		handler.NotifyAttributeChange(1, uint8(model.FeatureMeasurement), 1, int64(10000000))
	}

	// Wait for device to receive notification
	select {
	case <-deviceNotifCh:
		deviceNotifMu.Lock()
		if deviceReceivedNotif == nil {
			t.Error("Device notification is nil")
		} else {
			t.Logf("Device received notification: sub=%d, changes=%v",
				deviceReceivedNotif.SubscriptionID, deviceReceivedNotif.Changes)
		}
		deviceNotifMu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("Device timed out waiting for notification from controller")
	}

	t.Log("Bidirectional subscription test passed")
}

// TestBidirectional_NormalOperationsStillWork verifies that adding
// bidirectional support doesn't break normal controller->device operations.
func TestBidirectional_NormalOperationsStillWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup standard device with DeviceInfo
	device := model.NewDevice("evse-bidir-004", 0x1234, 0x5678)
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 4040 // Max is 4095

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// Setup controller with exposed features (to enable bidirectional)
	controllerExposedDevice := model.NewDevice("controller-exposed", 0x2000, 0x0001)
	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Normal Ops Controller"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-4040",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 4040,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found")
	}

	// Enable bidirectional by setting exposed device
	session.SetExposedDevice(controllerExposedDevice)

	// === Normal operations should still work ===

	// Read from device
	attrs, err := session.Read(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Normal Read failed: %v", err)
	}
	if attrs == nil {
		t.Error("Expected attributes from normal Read")
	}
	t.Logf("Normal Read returned %d attributes", len(attrs))

	// Subscribe to device
	subID, priming, err := session.Subscribe(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Normal Subscribe failed: %v", err)
	}
	if subID == 0 {
		t.Error("Expected non-zero subscription ID")
	}
	t.Logf("Normal Subscribe returned subID=%d, priming=%d attrs", subID, len(priming))

	t.Log("Normal operations still work with bidirectional enabled")
}
