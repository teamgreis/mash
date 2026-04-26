//go:build integration

package service

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// parseTestPort extracts the port number from an address string.
// The format is "IP:PORT" or "[::]:PORT".
func parseTestPort(addr string) uint16 {
	var port uint16
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			for j := i + 1; j < len(addr); j++ {
				port = port*10 + uint16(addr[j]-'0')
			}
			return port
		}
	}
	return 0
}

// TestE2E_CommissioningFlow tests the full commissioning flow:
// 1. Device starts and enters commissioning mode
// 2. Controller discovers and commissions with correct setup code
// 3. Both sides emit appropriate events
func TestE2E_CommissioningFlow(t *testing.T) {
	// === Setup Device ===
	device := model.NewDevice("evse-001", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 1234

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device events
	var deviceConnectedZoneID string
	var deviceEventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		deviceEventMu.Lock()
		defer deviceEventMu.Unlock()
		if e.Type == EventConnected && e.ZoneID != "" {
			deviceConnectedZoneID = e.ZoneID
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start device
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Enter commissioning mode
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Get device address
	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil - TLS server not started")
	}
	t.Logf("Device listening on %s", addr.String())

	// === Setup Controller ===
	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Home Energy Manager"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	// Track controller events
	var controllerDeviceID string
	var controllerEventMu sync.Mutex
	controllerSvc.OnEvent(func(e Event) {
		controllerEventMu.Lock()
		defer controllerEventMu.Unlock()
		if e.Type == EventCommissioned && e.DeviceID != "" {
			controllerDeviceID = e.DeviceID
		}
	})

	// Start controller
	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// === Commission Device ===
	port := parseTestPort(addr.String())

	// Simulate discovery result (in real scenario, mDNS would provide this)
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-1234",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	t.Logf("Commissioning device at %s:%d", discoveryService.Host, discoveryService.Port)

	// Commission with correct setup code
	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// === Verify Results ===
	if connectedDevice == nil {
		t.Fatal("Commission returned nil device")
	}

	if connectedDevice.ID == "" {
		t.Error("Device ID should not be empty after commissioning")
	}
	t.Logf("Commissioned device ID: %s", connectedDevice.ID)

	// Wait for events to propagate
	time.Sleep(200 * time.Millisecond)

	// Check device side
	deviceEventMu.Lock()
	deviceZoneID := deviceConnectedZoneID
	deviceEventMu.Unlock()

	if deviceZoneID == "" {
		t.Error("Device should have received EventConnected with zone ID")
	} else {
		t.Logf("Device connected zone ID: %s", deviceZoneID)
	}

	// Check controller side
	controllerEventMu.Lock()
	ctrlDeviceID := controllerDeviceID
	controllerEventMu.Unlock()

	if ctrlDeviceID == "" {
		t.Error("Controller should have received EventCommissioned with device ID")
	} else {
		t.Logf("Controller commissioned device ID: %s", ctrlDeviceID)
	}

	// Verify device state
	if deviceSvc.ZoneCount() != 1 {
		t.Errorf("Device should have 1 connected zone, got %d", deviceSvc.ZoneCount())
	}

	zone := deviceSvc.GetZone(deviceZoneID)
	if zone == nil {
		t.Error("Device should have zone record")
	} else if !zone.Connected {
		t.Error("Zone should be marked as connected")
	}

	// Verify controller state
	if controllerSvc.DeviceCount() != 1 {
		t.Errorf("Controller should have 1 device, got %d", controllerSvc.DeviceCount())
	}

	storedDevice := controllerSvc.GetDevice(ctrlDeviceID)
	if storedDevice == nil {
		t.Error("Controller should have device record")
	} else if !storedDevice.Connected {
		t.Error("Device should be marked as connected")
	}
}

// TestE2E_CommissioningFlowWrongCode verifies that commissioning fails with wrong code
// and leaves both sides in a clean state.
func TestE2E_CommissioningFlowWrongCode(t *testing.T) {
	// Setup device
	device := model.NewDevice("evse-002", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track if device received any connected events (shouldn't)
	var deviceGotConnected bool
	var deviceEventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		deviceEventMu.Lock()
		defer deviceEventMu.Unlock()
		if e.Type == EventConnected {
			deviceGotConnected = true
		}
	})

	ctx := context.Background()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}

	// Setup controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Create discovery service
	port := parseTestPort(addr.String())

	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
	}

	// Attempt commissioning with WRONG code
	_, err = controllerSvc.Commission(ctx, discoveryService, "87654321")
	if err == nil {
		t.Fatal("Expected commissioning to fail with wrong setup code")
	}

	t.Logf("Commission correctly failed: %v", err)

	// Wait for any async events
	time.Sleep(100 * time.Millisecond)

	// Verify device state - should have no zones
	if deviceSvc.ZoneCount() != 0 {
		t.Errorf("Device should have 0 zones after failed commission, got %d", deviceSvc.ZoneCount())
	}

	deviceEventMu.Lock()
	gotConnected := deviceGotConnected
	deviceEventMu.Unlock()

	if gotConnected {
		t.Error("Device should NOT have received EventConnected with wrong setup code")
	}

	// Verify controller state - should have no devices
	if controllerSvc.DeviceCount() != 0 {
		t.Errorf("Controller should have 0 devices after failed commission, got %d", controllerSvc.DeviceCount())
	}
}

// TestE2E_MultipleCommissioning verifies that a device can be commissioned by multiple zones.
func TestE2E_MultipleCommissioning(t *testing.T) {
	// Setup device
	device := model.NewDevice("evse-003", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	// Per DEC-043: MaxZones is 2 (one GRID + one LOCAL)

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx := context.Background()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}

	port := parseTestPort(addr.String())

	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
	}

	// Commission from first controller
	controller1Config := validControllerConfig()
	controller1Config.ZoneName = "Home Manager 1"
	controller1, _ := NewControllerService(controller1Config)
	wireCertStores(t, deviceSvc, controller1, controller1Config.ZoneName)
	controller1.SetBrowser(newMockBrowser())
	_ = controller1.Start(ctx)
	defer func() { _ = controller1.Stop() }()

	device1, err := controller1.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("First commission failed: %v", err)
	}
	if err := controller1.Reconnect(ctx, device1.ID); err != nil {
		t.Fatalf("Reconnect (controller1) failed: %v", err)
	}
	t.Logf("First controller commissioned device: %s", device1.ID)

	// Device exits commissioning mode after first commission.
	// For additional zones, device must re-enter commissioning mode.
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("Re-enter commissioning mode failed: %v", err)
	}

	// Commission from second controller
	controller2Config := validControllerConfig()
	controller2Config.ZoneName = "Home Manager 2"
	controller2, _ := NewControllerService(controller2Config)
	wireCertStores(t, deviceSvc, controller2, controller2Config.ZoneName)
	controller2.SetBrowser(newMockBrowser())
	_ = controller2.Start(ctx)
	defer func() { _ = controller2.Stop() }()

	device2, err := controller2.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Second commission failed: %v", err)
	}
	if err := controller2.Reconnect(ctx, device2.ID); err != nil {
		t.Fatalf("Reconnect (controller2) failed: %v", err)
	}
	t.Logf("Second controller commissioned device: %s", device2.ID)

	// Wait for events
	time.Sleep(200 * time.Millisecond)

	// Device should have 2 zones
	if deviceSvc.ZoneCount() != 2 {
		t.Errorf("Device should have 2 zones, got %d", deviceSvc.ZoneCount())
	}

	// Each controller should have 1 device
	if controller1.DeviceCount() != 1 || controller2.DeviceCount() != 1 {
		t.Errorf("Each controller should have 1 device, got %d and %d",
			controller1.DeviceCount(), controller2.DeviceCount())
	}
}

// TestE2E_OperationalRead tests reading attributes after commissioning.
func TestE2E_OperationalRead(t *testing.T) {
	// Setup device with DeviceInfo feature
	device := model.NewDevice("evse-read-001", 0x1234, 0x5678)

	// Add DeviceInfo feature to endpoint 0
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// Setup and start controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)
	controllerSvc.SetBrowser(newMockBrowser())

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission device. Discriminator must match the device's
	// validDeviceConfig() value (1234) — DEC-084 §2.3 makes it part of the
	// PBKDF2 salt, so a mismatch fails PASE before the controller can read.
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// Wait for connection to be fully established
	time.Sleep(100 * time.Millisecond)

	// Get session for operational messaging
	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found for commissioned device")
	}

	// Read DeviceInfo attributes
	attrs, err := session.Read(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if attrs == nil {
		t.Fatal("Expected attributes, got nil")
	}

	t.Logf("Read %d attributes from DeviceInfo", len(attrs))

	// Should have at least global attributes (featureMap, attributeList, etc.)
	if len(attrs) == 0 {
		t.Error("Expected at least some attributes")
	}
}

// TestE2E_OperationalSubscribe tests subscribing and receiving priming report.
func TestE2E_OperationalSubscribe(t *testing.T) {
	// Setup device with DeviceInfo feature
	device := model.NewDevice("evse-sub-001", 0x1234, 0x5678)

	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// Setup and start controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)
	controllerSvc.SetBrowser(newMockBrowser())

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission device. DEC-084: discriminator participates in salt
	// derivation; must match validDeviceConfig().
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found for commissioned device")
	}

	// Subscribe to DeviceInfo
	subID, priming, err := session.Subscribe(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if subID == 0 {
		t.Error("Expected non-zero subscription ID")
	}

	t.Logf("Subscription ID: %d", subID)

	if priming == nil {
		t.Error("Expected priming report")
	} else {
		t.Logf("Priming report has %d attributes", len(priming))
	}
}

// TestE2E_SubscriptionNotification tests receiving notifications on attribute changes.
func TestE2E_SubscriptionNotification(t *testing.T) {
	// Setup device with a feature that can have its attributes changed
	device := model.NewDevice("evse-notif-001", 0x1234, 0x5678)

	// Add DeviceInfo feature
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())

	// Setup and start controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)
	controllerSvc.SetBrowser(newMockBrowser())

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission device. DEC-084: discriminator participates in salt
	// derivation; must match validDeviceConfig().
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1234,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found for commissioned device")
	}

	// Set up notification handler
	var receivedNotif *wire.Notification
	var notifMu sync.Mutex
	notifReceived := make(chan struct{}, 1)

	session.SetNotificationHandler(func(notif *wire.Notification) {
		notifMu.Lock()
		receivedNotif = notif
		notifMu.Unlock()
		select {
		case notifReceived <- struct{}{}:
		default:
		}
	})

	// Subscribe to DeviceInfo
	subID, _, err := session.Subscribe(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	t.Logf("Subscribed with ID %d", subID)

	// NOTE: For a full notification test, we would need to:
	// 1. Change an attribute on the device side
	// 2. Have the device send a notification
	// 3. Verify the controller receives it
	//
	// Since DeviceInfo attributes are mostly read-only and we don't have
	// a notification dispatch mechanism wired up yet, this test verifies
	// that subscription works and handlers can be set up.

	// Verify no spurious notifications were received (none should be sent)
	notifMu.Lock()
	if receivedNotif != nil {
		t.Errorf("Unexpected notification received: %+v", receivedNotif)
	}
	notifMu.Unlock()

	t.Log("Subscription and notification handler setup successful")
}

// TestE2E_NotificationDelivery tests that attribute changes are delivered as notifications.
func TestE2E_NotificationDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create device with Measurement feature
	device := createTestDevice()

	// Add a Measurement feature on endpoint 1 for testing notifications
	ep1 := model.NewEndpoint(1, model.EndpointEVCharger, "EV Charger")
	measurementFeature := model.NewFeature(model.FeatureMeasurement, 1)
	// Add ACActivePower attribute (ID 1)
	measurementFeature.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      1, // MeasurementAttrACActivePower
		Name:    "ACActivePower",
		Type:    model.DataTypeInt64,
		Access:  model.AccessReadWrite, // Need write access for device-side updates
		Default: int64(0),
	}))
	ep1.AddFeature(measurementFeature)
	_ = device.AddEndpoint(ep1)

	// Create device service
	deviceConfig := validDeviceConfig()
	deviceConfig.Discriminator = 2345 // Max is 4095
	deviceConfig.SetupCode = "20202021"
	deviceConfig.ListenAddress = "127.0.0.1:0"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Enter commissioning mode
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := addr.(*net.TCPAddr).Port
	t.Logf("Device listening on port %d", port)

	// Create controller
	controllerConfig := DefaultControllerConfig()
	controllerConfig.ZoneName = "Test Controller"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)
	controllerSvc.SetBrowser(newMockBrowser())

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission device. Discriminator must match the device's
	// (deviceConfig.Discriminator = 2345 above) — DEC-084 §2.3.
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          uint16(port),
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 2345,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Fatal("No session found for commissioned device")
	}

	// Set up notification handler
	var receivedNotif *wire.Notification
	var notifMu sync.Mutex
	notifReceived := make(chan struct{}, 1)

	session.SetNotificationHandler(func(notif *wire.Notification) {
		notifMu.Lock()
		receivedNotif = notif
		notifMu.Unlock()
		select {
		case notifReceived <- struct{}{}:
		default:
		}
	})

	// Subscribe to Measurement feature on endpoint 1
	subID, priming, err := session.Subscribe(ctx, 1, uint8(model.FeatureMeasurement), nil)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	t.Logf("Subscribed with ID %d, priming: %v", subID, priming)

	// Trigger an attribute change on the device side
	const attrACActivePower = uint16(1)
	newPower := int64(5000000) // 5 kW

	if err := deviceSvc.NotifyAttributeChange(1, uint8(model.FeatureMeasurement), attrACActivePower, newPower); err != nil {
		t.Fatalf("NotifyAttributeChange failed: %v", err)
	}

	// Wait for notification to arrive
	select {
	case <-notifReceived:
		notifMu.Lock()
		if receivedNotif == nil {
			t.Fatal("Notification received signal but notif is nil")
		}
		if receivedNotif.SubscriptionID != subID {
			t.Errorf("Expected subscription ID %d, got %d", subID, receivedNotif.SubscriptionID)
		}
		if receivedNotif.EndpointID != 1 {
			t.Errorf("Expected endpoint ID 1, got %d", receivedNotif.EndpointID)
		}
		if receivedNotif.FeatureID != uint8(model.FeatureMeasurement) {
			t.Errorf("Expected feature ID %d, got %d", model.FeatureMeasurement, receivedNotif.FeatureID)
		}
		if val, ok := receivedNotif.Changes[attrACActivePower]; !ok {
			t.Error("Notification missing ACActivePower attribute")
		} else {
			t.Logf("Received notification: power = %v", val)
		}
		notifMu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for notification")
	}

	t.Log("Notification delivery test passed")
}

// TestE2E_DeviceIDConsistencyForReconnection verifies that the device ID stored by the
// controller after commissioning matches the device ID advertised by the device.
// This is critical for reconnection - if these don't match, operational discovery
// won't recognize the device.
func TestE2E_DeviceIDConsistencyForReconnection(t *testing.T) {
	// === Setup Device ===
	device := model.NewDevice("evse-reconnect", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 3333 // Must be <= 4095

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device's zone ID
	var deviceZoneID string
	var deviceEventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		deviceEventMu.Lock()
		defer deviceEventMu.Unlock()
		if e.Type == EventConnected && e.ZoneID != "" {
			deviceZoneID = e.ZoneID
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}

	// === Setup Controller ===
	controllerConfig := validControllerConfig()
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
	port := parseTestPort(addr.String())
	commissionSvc := &discovery.CommissionableService{
		InstanceName:  "MASH-3333",
		Host:          "localhost",
		Port:          port,
		Discriminator: 3333,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, commissionSvc, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// Wait for device to process connection
	time.Sleep(100 * time.Millisecond)

	// === Get the device ID stored by controller ===
	controllerStoredDeviceID := connectedDevice.ID
	t.Logf("Controller stored device ID: %s", controllerStoredDeviceID)

	// === Get the device ID from device's operational advertising ===
	deviceEventMu.Lock()
	zoneID := deviceZoneID
	deviceEventMu.Unlock()

	if zoneID == "" {
		t.Fatal("Device did not receive zone connection event")
	}

	// Check the mock advertiser for the operational info
	deviceAdvertiser.mu.Lock()
	opInfo, exists := deviceAdvertiser.operationalZones[zoneID]
	deviceAdvertiser.mu.Unlock()

	if !exists {
		t.Fatal("Device did not start operational advertising for the zone")
	}

	advertisedDeviceID := opInfo.DeviceID
	t.Logf("Device advertised device ID: %s", advertisedDeviceID)

	// === THE CRITICAL CHECK ===
	// This is exactly what the bug was about - these didn't match before the fix
	if controllerStoredDeviceID != advertisedDeviceID {
		t.Errorf("DEVICE ID MISMATCH - reconnection would fail!\n"+
			"  Controller stored: %s\n"+
			"  Device advertises: %s\n"+
			"  These must be identical for operational discovery to work.",
			controllerStoredDeviceID, advertisedDeviceID)
	} else {
		t.Log("Device IDs match - reconnection will work correctly")
	}

	// Also verify the zone IDs match
	controllerZoneID := controllerSvc.ZoneID()
	if controllerZoneID != zoneID {
		t.Errorf("Zone ID mismatch:\n  Controller: %s\n  Device: %s",
			controllerZoneID, zoneID)
	}

	if opInfo.ZoneID != zoneID {
		t.Errorf("Advertised zone ID mismatch:\n  Expected: %s\n  Advertised: %s",
			zoneID, opInfo.ZoneID)
	}
}

// TestE2E_ReconnectionAfterDisconnect tests the full reconnection flow:
// 1. Commission a device
// 2. Disconnect
// 3. Verify operational discovery can find and reconnect to the device
func TestE2E_ReconnectionAfterDisconnect(t *testing.T) {
	// === Setup Device ===
	device := model.NewDevice("evse-reconnect-full", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 2222 // Must be <= 4095

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()

	// === Setup Controller ===
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	browser := newMockBrowser()
	controllerSvc.SetBrowser(browser)

	// Track controller events
	reconnectedCh := make(chan string, 1)
	controllerSvc.OnEvent(func(e Event) {
		if e.Type == EventDeviceReconnected {
			select {
			case reconnectedCh <- e.DeviceID:
			default:
			}
		}
	})

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// === Commission ===
	port := parseTestPort(addr.String())
	commissionSvc := &discovery.CommissionableService{
		InstanceName:  "MASH-2222",
		Host:          "localhost",
		Port:          port,
		Discriminator: 2222,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, commissionSvc, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	deviceID := connectedDevice.ID
	zoneID := controllerSvc.ZoneID()
	t.Logf("Commissioned device: %s (zone: %s)", deviceID, zoneID)

	// Wait for stable connection
	time.Sleep(100 * time.Millisecond)

	// === Simulate disconnect ===
	// Directly mark device as disconnected (simulates network failure)
	// In real scenario, this happens when the message loop detects connection closure
	controllerSvc.HandleDeviceDisconnect(deviceID)

	// Verify device is marked disconnected
	storedDevice := controllerSvc.GetDevice(deviceID)
	if storedDevice == nil {
		t.Fatal("Device record missing after disconnect")
	}
	if storedDevice.Connected {
		t.Fatal("Device should be marked as disconnected")
	}
	t.Log("Device marked as disconnected")

	// === Setup mock browser with operational service ===
	// This simulates what the device advertises via mDNS
	browser.AddOperationalDevice(&discovery.OperationalService{
		InstanceName: zoneID + "-" + deviceID,
		Host:         "localhost",
		Port:         port,
		ZoneID:       zoneID,
		DeviceID:     deviceID, // Must match what controller stored
	})

	// === Start operational discovery ===
	if err := controllerSvc.StartOperationalDiscovery(ctx); err != nil {
		t.Fatalf("StartOperationalDiscovery failed: %v", err)
	}
	defer controllerSvc.StopOperationalDiscovery()

	// === Wait for reconnection ===
	select {
	case reconnectedID := <-reconnectedCh:
		if reconnectedID != deviceID {
			t.Errorf("Reconnected device ID mismatch: expected %s, got %s", deviceID, reconnectedID)
		}
		t.Logf("Device reconnected successfully: %s", reconnectedID)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for device reconnection")
	}

	// Verify device is marked connected again
	storedDevice = controllerSvc.GetDevice(deviceID)
	if storedDevice == nil {
		t.Fatal("Device record missing after reconnection")
	}
	if !storedDevice.Connected {
		t.Error("Device should be marked as connected after reconnection")
	}
}

// TestE2E_RenewalOverTLS tests certificate renewal over an established TLS connection.
// This is TC-E2E-RENEW-1: Full renewal over TLS test.
func TestE2E_RenewalOverTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test device with DeviceInfo feature
	device := model.NewDevice("evse-renewal", 0x1234, 0x5678)

	// Add DeviceInfo feature to endpoint 0
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	// Create device config with a setup code
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 3456 // Max is 4095

	// Create device service
	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("Failed to create device service: %v", err)
	}

	// Setup mock advertiser for device discovery
	mockAdv := newMockAdvertiser()
	deviceSvc.SetAdvertiser(mockAdv)

	// Create controller config
	controllerConfig := validControllerConfig()

	// Create controller service
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("Failed to create controller service: %v", err)
	}

	// Wire device + controller cert stores (controller store needs a Zone CA
	// to issue operational certs; the bare NewMemoryControllerStore() above
	// would fail at SignCSR).
	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	// Setup mock browser for controller discovery
	mockBrowser := newMockBrowser()
	controllerSvc.SetBrowser(mockBrowser)

	// Start both services
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Failed to start device service: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Enter commissioning mode on device
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller service: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Get device address for commissioning
	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}
	port := parseTestPort(addr.String())

	// Create commissionable service info
	commService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: deviceConfig.Discriminator,
	}

	// Commission the device
	connectedDevice, err := controllerSvc.Commission(ctx, commService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	deviceID := connectedDevice.ID
	t.Logf("Commissioned device: %s", deviceID)

	// Wait for session to be established
	time.Sleep(100 * time.Millisecond)

	// Verify we can communicate before renewal
	session := controllerSvc.GetSession(deviceID)
	if session == nil {
		t.Fatal("No session found for device")
	}

	// Read an attribute to verify the connection works
	attrs, err := session.Read(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Pre-renewal read failed: %v", err)
	}
	if len(attrs) == 0 {
		t.Error("Expected to read some attributes")
	}
	t.Logf("Pre-renewal: Read %d attributes", len(attrs))

	// Perform certificate renewal
	t.Log("Starting certificate renewal...")
	if err := controllerSvc.RenewDevice(ctx, deviceID); err != nil {
		t.Fatalf("RenewDevice failed: %v", err)
	}
	t.Log("Certificate renewal completed successfully")

	// Verify the session still works after renewal
	attrs, err = session.Read(ctx, 0, uint8(model.FeatureDeviceInfo), nil)
	if err != nil {
		t.Fatalf("Post-renewal read failed: %v", err)
	}
	if len(attrs) == 0 {
		t.Error("Expected to read some attributes after renewal")
	}
	t.Logf("Post-renewal: Read %d attributes", len(attrs))

	// Verify device's renewal handler was invoked
	s := deviceSvc
	s.mu.RLock()
	zoneSession := s.zoneSessions[controllerSvc.ZoneID()]
	s.mu.RUnlock()

	if zoneSession == nil {
		t.Fatal("No zone session found on device")
	}

	renewalHandler := zoneSession.RenewalHandler()
	if renewalHandler == nil {
		t.Fatal("Device should have a renewal handler")
	}

	activeCert := renewalHandler.ActiveCert()
	if activeCert == nil {
		t.Error("Device should have an active certificate after renewal")
	} else {
		t.Logf("Device has active cert, expires: %v", activeCert.NotAfter)
	}

	// Verify the connection was preserved (same session, not reconnected)
	newSession := controllerSvc.GetSession(deviceID)
	if newSession != session {
		t.Error("Session should be preserved during renewal, not replaced")
	}
}

// TestE2E_RemoveDevice tests the full device removal flow:
// 1. Device is commissioned to controller
// 2. Controller sends RemoveDevice
// 3. Device receives RemoveZone command and removes zone
// 4. Both sides clean up properly
func TestE2E_RemoveDevice(t *testing.T) {
	// === Setup Device ===
	device := model.NewDevice("evse-remove-001", 0x1234, 0x5678)

	// Add DeviceInfo feature - required for RemoveZone command
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 3210 // Max is 4095

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device zone removal event
	deviceZoneRemovedCh := make(chan string, 1)
	deviceSvc.OnEvent(func(e Event) {
		if e.Type == EventZoneRemoved {
			deviceZoneRemovedCh <- e.ZoneID
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start device
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	addr := deviceSvc.CommissioningAddr()
	port := parseTestPort(addr.String())
	t.Logf("Device listening on port %d", port)

	// === Setup Controller ===
	controllerConfig := validControllerConfig()
	controllerConfig.ZoneName = "Home Energy Manager"

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)
	controllerSvc.SetBrowser(newMockBrowser())

	// Track controller device removal event
	controllerDeviceRemovedCh := make(chan string, 1)
	controllerSvc.OnEvent(func(e Event) {
		if e.Type == EventDeviceRemoved {
			controllerDeviceRemovedCh <- e.DeviceID
		}
	})

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// === Commission Device ===
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-3210",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 3210,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: operational reconnect is now an explicit step after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	deviceID := connectedDevice.ID
	t.Logf("Commissioned device: %s", deviceID)

	// Wait for commissioning to stabilize
	time.Sleep(200 * time.Millisecond)

	// Verify both sides show device connected
	if deviceSvc.ZoneCount() != 1 {
		t.Fatalf("Device should have 1 zone after commissioning, got %d", deviceSvc.ZoneCount())
	}
	if controllerSvc.DeviceCount() != 1 {
		t.Fatalf("Controller should have 1 device after commissioning, got %d", controllerSvc.DeviceCount())
	}

	// === Remove Device ===
	t.Log("Removing device from zone...")

	err = controllerSvc.RemoveDevice(ctx, deviceID)
	if err != nil {
		t.Fatalf("RemoveDevice failed: %v", err)
	}

	// === Verify Results ===
	// Wait for controller event
	select {
	case removedDeviceID := <-controllerDeviceRemovedCh:
		if removedDeviceID != deviceID {
			t.Errorf("Expected device ID %s, got %s", deviceID, removedDeviceID)
		}
		t.Log("Controller received EventDeviceRemoved")
	case <-time.After(2 * time.Second):
		t.Error("Controller should have received EventDeviceRemoved")
	}

	// Wait for device zone removal
	select {
	case <-deviceZoneRemovedCh:
		t.Log("Device received EventZoneRemoved")
	case <-time.After(2 * time.Second):
		t.Error("Device should have received EventZoneRemoved")
	}

	// Verify device state - zone should be removed
	time.Sleep(100 * time.Millisecond) // Allow async cleanup
	if deviceSvc.ZoneCount() != 0 {
		t.Errorf("Device should have 0 zones after removal, got %d", deviceSvc.ZoneCount())
	}

	// Verify controller state - device should be removed
	if controllerSvc.DeviceCount() != 0 {
		t.Errorf("Controller should have 0 devices after removal, got %d", controllerSvc.DeviceCount())
	}

	storedDevice := controllerSvc.GetDevice(deviceID)
	if storedDevice != nil {
		t.Error("Device should not exist in controller after removal")
	}

	t.Log("Device successfully removed from zone")
}
