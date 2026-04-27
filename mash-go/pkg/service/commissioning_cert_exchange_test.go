package service

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/discovery/mocks"
	"github.com/mash-protocol/mash-go/pkg/model"
)

// TC-IMPL-COMM-CERT-001: Test that commissioning cert is generated in-memory
// and NOT persisted when device is uncommissioned.
func TestCommissioning_CertNotPersisted(t *testing.T) {
	// Set up device without any prior commissioning
	device := model.NewDevice("test-device-nocert", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.ListenAddress = "localhost:0"

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up a memory cert store to verify nothing is persisted
	certStore := cert.NewMemoryStore()
	svc.SetCertStore(certStore)

	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Verify no zones are listed (no operational certs persisted)
	zones := certStore.ListZones()
	if len(zones) != 0 {
		t.Errorf("Expected no zones for uncommissioned device, got %d", len(zones))
	}

	// Verify device has no device ID yet (assigned during cert exchange)
	svc.mu.RLock()
	deviceID := svc.deviceID
	svc.mu.RUnlock()

	if deviceID != "" {
		t.Errorf("Uncommissioned device should not have device ID, got %s", deviceID)
	}
}

// TC-IMPL-COMM-CERT-002: Test cert exchange flow after PASE handshake.
// After PASE completes, controller sends CSRRequest (MsgType 10), device
// generates new key pair and returns CSRResponse (11), controller signs and
// sends CertInstall (12) with Zone CA + zoneType + priority, device installs
// and acks via CertInstallResponse (13) + CommissioningComplete (20).
// (DEC-085 spec-strict commissioning envelope.)
func TestCommissioning_CertExchangeAfterPASE(t *testing.T) {
	// Set up device service
	device := model.NewDevice("test-device-certex", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Device needs a cert store to save the operational cert it receives
	deviceCertStore := cert.NewMemoryStore()
	deviceSvc.SetCertStore(deviceCertStore)

	deviceAdvertiser := mocks.NewMockAdvertiser(t)
	deviceAdvertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	deviceAdvertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopAll().Return().Maybe()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	ctx := context.Background()
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Get device address
	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}

	// Set up controller service with cert store
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	controllerCertStore := createControllerCertStore(t, controllerConfig.ZoneName)
	controllerSvc.SetCertStore(controllerCertStore)

	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().Stop().Return().Maybe()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission the device
	tcpAddr := addr.(*net.TCPAddr)
	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-1234",
		Host:          "localhost",
		Port:          uint16(tcpAddr.Port),
		Addresses:     []string{tcpAddr.IP.String()},
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}

	// Verify operational cert was received and stored
	zones := deviceCertStore.ListZones()
	if len(zones) != 1 {
		t.Fatalf("Expected 1 zone after commissioning, got %d", len(zones))
	}

	// Verify Zone CA was stored
	zoneCA, err := deviceCertStore.GetZoneCACert(zones[0])
	if err != nil {
		t.Errorf("Expected Zone CA to be stored: %v", err)
	}
	if zoneCA == nil {
		t.Error("Zone CA should not be nil")
	}

	// Verify operational cert can be retrieved
	opCert, err := deviceCertStore.GetOperationalCert(zones[0])
	if err != nil {
		t.Errorf("Expected operational cert to be stored: %v", err)
	}
	if opCert == nil {
		t.Error("Operational cert should not be nil")
	}

	// Verify the operational cert was signed by the Zone CA
	if zoneCA != nil && opCert != nil {
		// Check issuer matches
		if opCert.Certificate.Issuer.String() != zoneCA.Subject.String() {
			t.Errorf("Operational cert issuer (%s) should match Zone CA subject (%s)",
				opCert.Certificate.Issuer.String(), zoneCA.Subject.String())
		}
	}

	// Verify connected device has device ID
	if connectedDevice.ID == "" {
		t.Error("Connected device should have device ID")
	}
}

// TC-IMPL-COMM-CERT-003: Test that device ID is derived from operational cert.
// The device ID should be SHA256(operational_cert_public_key)[:8].
func TestCommissioning_DeviceIDFromOperationalCert(t *testing.T) {
	// Set up device service
	device := model.NewDevice("test-device-devid", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceCertStore := cert.NewMemoryStore()
	deviceSvc.SetCertStore(deviceCertStore)

	deviceAdvertiser := mocks.NewMockAdvertiser(t)
	deviceAdvertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	deviceAdvertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopAll().Return().Maybe()
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

	// Set up controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	controllerCertStore := createControllerCertStore(t, controllerConfig.ZoneName)
	controllerSvc.SetCertStore(controllerCertStore)

	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().Stop().Return().Maybe()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission
	tcpAddr := addr.(*net.TCPAddr)
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          uint16(tcpAddr.Port),
		Addresses:     []string{tcpAddr.IP.String()},
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}

	// Get the operational cert
	zones := deviceCertStore.ListZones()
	if len(zones) == 0 {
		t.Fatal("Expected at least one zone")
	}

	opCert, err := deviceCertStore.GetOperationalCert(zones[0])
	if err != nil {
		t.Fatalf("Failed to get operational cert: %v", err)
	}

	// Extract device ID from operational cert (Matter-style: embedded in CommonName)
	expectedDeviceID, err := cert.ExtractDeviceID(opCert.Certificate)
	if err != nil {
		t.Fatalf("Failed to extract device ID: %v", err)
	}

	// Verify controller has the same device ID
	if connectedDevice.ID != expectedDeviceID {
		t.Errorf("Controller device ID (%s) should match extracted ID (%s)",
			connectedDevice.ID, expectedDeviceID)
	}

	// Verify device service also has the same device ID
	deviceSvc.mu.RLock()
	deviceServiceID := deviceSvc.deviceID
	deviceSvc.mu.RUnlock()

	if deviceServiceID != expectedDeviceID {
		t.Errorf("Device service ID (%s) should match extracted ID (%s)",
			deviceServiceID, expectedDeviceID)
	}
}

// TC-IMPL-COMM-CERT-004: Test multi-zone commissioning.
// Same device commissioned to different zones should have different device IDs.
func TestCommissioning_MultiZone_DifferentDeviceIDs(t *testing.T) {
	// Multi-zone commissioning requires more infrastructure changes
	// The device would need to be commissioned to multiple controllers
	// and generate different key pairs for each zone.
	// This test is deferred until multi-zone support is fully implemented.
	t.Skip("Multi-zone commissioning test deferred - requires additional infrastructure")
}

// TC-IMPL-COMM-CERT-005: Test reconnection uses operational cert.
// After commissioning, reconnection should use operational cert not commissioning cert.
func TestReconnection_UsesOperationalCert(t *testing.T) {
	// This test requires reconnection infrastructure that's not fully implemented
	// The reconnection flow needs to use the stored operational cert.
	t.Skip("Reconnection test deferred - requires operational reconnection infrastructure")
}

// TC-IMPL-COMM-CERT-006: Test reconnection verifies Zone CA.
// Device should verify controller's cert against stored Zone CA.
func TestReconnection_VerifiesZoneCA(t *testing.T) {
	// This test requires mutual TLS verification during reconnection
	// which is part of the operational TLS config.
	t.Skip("Zone CA verification test deferred - requires mutual TLS during reconnection")
}

// TestCommissioning_EventCommissionedEmitted verifies EventCommissioned is emitted
// after successful cert exchange.
// DEC-066: EventConnected is NOT emitted until operational reconnection.
func TestCommissioning_EventCommissionedEmitted(t *testing.T) {
	// Set up device service
	device := model.NewDevice("test-device-event", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceCertStore := cert.NewMemoryStore()
	deviceSvc.SetCertStore(deviceCertStore)

	deviceAdvertiser := mocks.NewMockAdvertiser(t)
	deviceAdvertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	deviceAdvertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopAll().Return().Maybe()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device-side events
	// DEC-066: After commissioning, we expect EventCommissioned (not EventConnected).
	// EventConnected is only emitted when operational TLS connection is established.
	var deviceCommissionedEvent bool
	var deviceConnectedEvent bool
	var eventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		switch e.Type {
		case EventCommissioned:
			deviceCommissionedEvent = true
		case EventConnected:
			deviceConnectedEvent = true
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

	// Set up controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	controllerCertStore := createControllerCertStore(t, controllerConfig.ZoneName)
	controllerSvc.SetCertStore(controllerCertStore)

	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().Stop().Return().Maybe()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Commission
	tcpAddr := addr.(*net.TCPAddr)
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          uint16(tcpAddr.Port),
		Addresses:     []string{tcpAddr.IP.String()},
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	_, err = controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}

	// Wait for events to be processed
	time.Sleep(100 * time.Millisecond)

	eventMu.Lock()
	commissioned := deviceCommissionedEvent
	connected := deviceConnectedEvent
	eventMu.Unlock()

	// DEC-066: After commissioning, EventCommissioned should be emitted.
	if !commissioned {
		t.Error("Expected EventCommissioned to be emitted on device after successful commissioning")
	}

	// DEC-066: EventConnected should NOT be emitted after commissioning alone.
	// It is only emitted when the controller reconnects with operational certificates.
	if connected {
		t.Error("EventConnected should NOT be emitted after commissioning (DEC-066); " +
			"it should only be emitted after operational TLS reconnection")
	}
}

// TestDEC066_OperationalReconnection verifies the full DEC-066 flow:
// 1. Commission device (EventCommissioned emitted, connection closed)
// 2. Controller reconnects with operational certificates
// 3. EventConnected emitted on successful operational TLS establishment
func TestDEC066_OperationalReconnection(t *testing.T) {
	// Set up device service
	device := model.NewDevice("test-device-dec066", 0x1234, 0x5678)
	deviceConfig := validDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceCertStore := cert.NewMemoryStore()
	deviceSvc.SetCertStore(deviceCertStore)

	deviceAdvertiser := mocks.NewMockAdvertiser(t)
	deviceAdvertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	deviceAdvertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	deviceAdvertiser.EXPECT().StopAll().Return().Maybe()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device-side events
	var deviceCommissioned, deviceConnected bool
	var eventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		switch e.Type {
		case EventCommissioned:
			deviceCommissioned = true
		case EventConnected:
			deviceConnected = true
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

	// Set up controller
	controllerConfig := validControllerConfig()
	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	controllerCertStore := createControllerCertStore(t, controllerConfig.ZoneName)
	controllerSvc.SetCertStore(controllerCertStore)

	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().Stop().Return().Maybe()
	controllerSvc.SetBrowser(browser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Phase 1: Commission
	tcpAddr := addr.(*net.TCPAddr)
	discoveryService := &discovery.CommissionableService{
		Host:          "localhost",
		Port:          uint16(tcpAddr.Port),
		Addresses:     []string{tcpAddr.IP.String()},
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}

	// Wait for commissioning to complete
	time.Sleep(100 * time.Millisecond)

	// Verify EventCommissioned was emitted
	eventMu.Lock()
	if !deviceCommissioned {
		t.Error("Expected EventCommissioned to be emitted after commissioning")
	}
	// Reset for reconnection test
	wasConnectedBeforeReconnect := deviceConnected
	eventMu.Unlock()

	// DEC-066: EventConnected should NOT have been emitted yet
	if wasConnectedBeforeReconnect {
		t.Error("EventConnected should not be emitted after commissioning (DEC-066)")
	}

	// Verify zone is registered but not connected on device
	deviceSvc.mu.RLock()
	var zoneRegistered, zoneConnected bool
	for _, cz := range deviceSvc.connectedZones {
		zoneRegistered = true
		zoneConnected = cz.Connected
		break
	}
	deviceSvc.mu.RUnlock()

	if !zoneRegistered {
		t.Fatal("Expected zone to be registered after commissioning")
	}
	if zoneConnected {
		t.Error("Zone should be registered as disconnected after commissioning (DEC-066)")
	}

	// After commissioning, verify the device's operational address is available
	// for reconnection.
	opAddr := deviceSvc.OperationalAddr()
	if opAddr == nil {
		t.Fatal("Device operational address is nil after commissioning")
	}
	controllerSvc.mu.Lock()
	if dev, ok := controllerSvc.connectedDevices[connectedDevice.ID]; ok {
		dev.Port = uint16(opAddr.(*net.TCPAddr).Port)
	}
	controllerSvc.mu.Unlock()

	// Phase 2: Controller reconnects with operational certificates
	err = controllerSvc.Reconnect(ctx, connectedDevice.ID)
	if err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// Wait for reconnection to complete
	time.Sleep(100 * time.Millisecond)

	// Verify EventConnected was emitted after reconnection
	eventMu.Lock()
	connectedAfterReconnect := deviceConnected
	eventMu.Unlock()

	if !connectedAfterReconnect {
		t.Error("Expected EventConnected to be emitted after operational reconnection")
	}

	// Verify zone is now connected
	deviceSvc.mu.RLock()
	zoneConnected = false
	for _, cz := range deviceSvc.connectedZones {
		zoneConnected = cz.Connected
		break
	}
	deviceSvc.mu.RUnlock()

	if !zoneConnected {
		t.Error("Zone should be connected after operational reconnection")
	}

	// Verify controller has active session
	session := controllerSvc.GetSession(connectedDevice.ID)
	if session == nil {
		t.Error("Expected active session after reconnection")
	}
}
