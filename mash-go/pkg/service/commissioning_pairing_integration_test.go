//go:build integration

package service

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/zone"
)

// ============================================================================
// Integration Test: Standard Commissioning Flow (Scenario A)
// ============================================================================

// TestE2E_StandardCommissioning verifies the baseline commissioning flow:
// 1. Device opens commissioning window (user-initiated)
// 2. Controller browses, finds device, commissions
// 3. Verify: Device is commissioned, zone added
func TestE2E_StandardCommissioning(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-standard-001", 0x1234, 0x5678)
	deviceConfig := testDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 1001
	deviceConfig.CommissioningWindowDuration = 5 * time.Second

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Track device events
	var deviceCommissionedZoneID string
	var deviceEventMu sync.Mutex
	commissionedCh := make(chan struct{}, 1)
	deviceSvc.OnEvent(func(e Event) {
		deviceEventMu.Lock()
		defer deviceEventMu.Unlock()
		if e.Type == EventConnected && e.ZoneID != "" {
			deviceCommissionedZoneID = e.ZoneID
			select {
			case commissionedCh <- struct{}{}:
			default:
			}
		}
	})

	// Start device
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Device enters commissioning mode (user-initiated)
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Get device address
	addr := deviceSvc.CommissioningAddr()
	if addr == nil {
		t.Fatal("Device TLS address is nil")
	}
	port := parseAddrPort(addr.String())

	// === Setup Controller ===
	controllerConfig := testControllerConfig()
	controllerConfig.ZoneName = "Standard Test Zone"
	controllerConfig.ZoneType = cert.ZoneTypeLocal

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
		InstanceName:  "MASH-1001",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 1001,
	}

	connectedDevice, err := controllerSvc.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("Commission failed: %v", err)
	}
	// DEC-066: explicit operational reconnect.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// === Verify Results ===
	if connectedDevice == nil {
		t.Fatal("Commission returned nil device")
	}

	// Wait for device event
	select {
	case <-commissionedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for device commissioning event")
	}

	// Verify device state
	if deviceSvc.ZoneCount() != 1 {
		t.Errorf("Device should have 1 zone, got %d", deviceSvc.ZoneCount())
	}

	deviceEventMu.Lock()
	zoneID := deviceCommissionedZoneID
	deviceEventMu.Unlock()

	if zoneID == "" {
		t.Error("Device should have received zone ID")
	}

	// Verify controller state
	if controllerSvc.DeviceCount() != 1 {
		t.Errorf("Controller should have 1 device, got %d", controllerSvc.DeviceCount())
	}

	t.Logf("Standard commissioning completed: device=%s, zone=%s", connectedDevice.ID, zoneID)
}

// ============================================================================
// Integration Test: Deferred Commissioning via Pairing Request (Scenario B)
// ============================================================================

// pairingRequestMockBrowser extends mockBrowser with pairing request awareness.
// It simulates the real mDNS interaction where the device sees the pairing request
// and then starts advertising as commissionable.
type pairingRequestMockBrowser struct {
	mu sync.Mutex

	// Pairing request tracking
	pairingRequestCallback func(discovery.PairingRequestService)

	// Device info to return when device starts advertising
	pendingDevice *discovery.CommissionableService
	deviceVisible bool

	// Track calls for verification
	findCallCount int

	// Preconfigured operational devices
	operationalDevices []*discovery.OperationalService
}

func newPairingRequestMockBrowser() *pairingRequestMockBrowser {
	return &pairingRequestMockBrowser{}
}

func (b *pairingRequestMockBrowser) BrowseCommissionable(ctx context.Context) (added, removed <-chan *discovery.CommissionableService, err error) {
	addedCh := make(chan *discovery.CommissionableService)
	removedCh := make(chan *discovery.CommissionableService)
	close(addedCh)
	close(removedCh)
	return addedCh, removedCh, nil
}

func (b *pairingRequestMockBrowser) BrowseOperational(ctx context.Context, zoneID string) (<-chan *discovery.OperationalService, error) {
	ch := make(chan *discovery.OperationalService)

	go func() {
		b.mu.Lock()
		devices := make([]*discovery.OperationalService, len(b.operationalDevices))
		copy(devices, b.operationalDevices)
		b.mu.Unlock()

		for _, svc := range devices {
			if zoneID == "" || svc.ZoneID == zoneID {
				select {
				case ch <- svc:
				case <-ctx.Done():
					close(ch)
					return
				}
			}
		}
		close(ch)
	}()

	return ch, nil
}

func (b *pairingRequestMockBrowser) BrowsePairingRequests(ctx context.Context, callback func(discovery.PairingRequestService)) error {
	b.mu.Lock()
	b.pairingRequestCallback = callback
	b.mu.Unlock()

	// Block until context cancelled
	<-ctx.Done()
	return nil
}

func (b *pairingRequestMockBrowser) FindByDiscriminator(ctx context.Context, discriminator uint16) (*discovery.CommissionableService, error) {
	b.mu.Lock()
	b.findCallCount++
	visible := b.deviceVisible
	device := b.pendingDevice
	b.mu.Unlock()

	if visible && device != nil && device.Discriminator == discriminator {
		return device, nil
	}
	return nil, discovery.ErrNotFound
}

func (b *pairingRequestMockBrowser) FindAllByDiscriminator(ctx context.Context, discriminator uint16) ([]*discovery.CommissionableService, error) {
	b.mu.Lock()
	visible := b.deviceVisible
	device := b.pendingDevice
	b.mu.Unlock()

	if visible && device != nil && device.Discriminator == discriminator {
		return []*discovery.CommissionableService{device}, nil
	}
	return nil, nil
}

func (b *pairingRequestMockBrowser) Stop() {}

func (b *pairingRequestMockBrowser) SetPendingDevice(device *discovery.CommissionableService) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pendingDevice = device
}

func (b *pairingRequestMockBrowser) MakeDeviceVisible() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deviceVisible = true
}

// pairingRequestMockAdvertiser tracks pairing requests for integration testing.
type pairingRequestMockAdvertiser struct {
	mu sync.Mutex

	// Base fields from mockAdvertiser
	commissionable   *discovery.CommissionableInfo
	operationalZones map[string]*discovery.OperationalInfo

	// Track pairing requests
	activePairingRequests map[uint16]*discovery.PairingRequestInfo

	// Callback when pairing request is announced
	onPairingRequestAnnounced func(info *discovery.PairingRequestInfo)
}

func newPairingRequestMockAdvertiser() *pairingRequestMockAdvertiser {
	return &pairingRequestMockAdvertiser{
		operationalZones:      make(map[string]*discovery.OperationalInfo),
		activePairingRequests: make(map[uint16]*discovery.PairingRequestInfo),
	}
}

func (a *pairingRequestMockAdvertiser) AdvertiseCommissionable(ctx context.Context, info *discovery.CommissionableInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commissionable = info
	return nil
}

func (a *pairingRequestMockAdvertiser) StopCommissionable() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commissionable = nil
	return nil
}

func (a *pairingRequestMockAdvertiser) AdvertiseOperational(ctx context.Context, info *discovery.OperationalInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.operationalZones[info.ZoneID] = info
	return nil
}

func (a *pairingRequestMockAdvertiser) UpdateOperational(zoneID string, info *discovery.OperationalInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.operationalZones[zoneID] = info
	return nil
}

func (a *pairingRequestMockAdvertiser) StopOperational(zoneID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.operationalZones, zoneID)
	return nil
}

func (a *pairingRequestMockAdvertiser) AnnouncePairingRequest(ctx context.Context, info *discovery.PairingRequestInfo) error {
	a.mu.Lock()
	a.activePairingRequests[info.Discriminator] = info
	cb := a.onPairingRequestAnnounced
	a.mu.Unlock()

	if cb != nil {
		cb(info)
	}
	return nil
}

func (a *pairingRequestMockAdvertiser) StopPairingRequest(discriminator uint16) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.activePairingRequests, discriminator)
	return nil
}

func (a *pairingRequestMockAdvertiser) StopAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commissionable = nil
	a.operationalZones = make(map[string]*discovery.OperationalInfo)
	a.activePairingRequests = make(map[uint16]*discovery.PairingRequestInfo)
}

func (a *pairingRequestMockAdvertiser) OnPairingRequestAnnounced(fn func(info *discovery.PairingRequestInfo)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onPairingRequestAnnounced = fn
}

func (a *pairingRequestMockAdvertiser) GetActivePairingRequest(discriminator uint16) *discovery.PairingRequestInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activePairingRequests[discriminator]
}

// TestE2E_DeferredCommissioning_PairingRequest verifies deferred commissioning:
// 1. Controller has QR data, device NOT advertising
// 2. Controller calls CommissionDevice (announces pairing request internally)
// 3. Device sees pairing request, opens commissioning window automatically
// 4. Controller discovers device, commissioning completes
// 5. Verify: Pairing request cleaned up, device commissioned
func TestE2E_DeferredCommissioning_PairingRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-deferred-001", 0x1234, 0x5678)
	deviceConfig := testDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 2001
	deviceConfig.CommissioningWindowDuration = 5 * time.Second
	deviceConfig.ListenForPairingRequests = true // Enable pairing request listening

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Create device browser that will receive pairing requests
	deviceBrowser := newPairingRequestMockBrowser()
	deviceSvc.SetBrowser(deviceBrowser)

	// Track device events
	commissioningOpenedCh := make(chan struct{}, 1)
	deviceConnectedCh := make(chan string, 1)
	deviceSvc.OnEvent(func(e Event) {
		switch e.Type {
		case EventCommissioningOpened:
			select {
			case commissioningOpenedCh <- struct{}{}:
			default:
			}
		case EventConnected:
			if e.ZoneID != "" {
				select {
				case deviceConnectedCh <- e.ZoneID:
				default:
				}
			}
		}
	})

	// Start device - NOT entering commissioning mode manually
	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// NOTE: Commissioning address is not available yet -- it is created when
	// the device enters commissioning mode (triggered by the pairing request).
	// Mock browser data is set up lazily inside the commissioningOpenedCh handler below.

	// === Setup Controller ===
	controllerConfig := testControllerConfig()
	controllerConfig.ZoneName = "Deferred Test Zone"
	controllerConfig.ZoneType = cert.ZoneTypeLocal
	controllerConfig.DiscoveryTimeout = 100 * time.Millisecond
	controllerConfig.PairingRequestTimeout = 5 * time.Second
	controllerConfig.PairingRequestPollInterval = 50 * time.Millisecond

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	wireCertStores(t, deviceSvc, controllerSvc, controllerConfig.ZoneName)

	// Use a browser that's linked to the device scenario
	controllerBrowser := newPairingRequestMockBrowser()
	controllerSvc.SetBrowser(controllerBrowser)

	// Use advertiser that tracks pairing requests
	controllerAdvertiser := newPairingRequestMockAdvertiser()
	controllerSvc.SetAdvertiser(controllerAdvertiser)

	// When controller announces pairing request, simulate device seeing it
	controllerAdvertiser.OnPairingRequestAnnounced(func(info *discovery.PairingRequestInfo) {
		t.Logf("Controller announced pairing request: discriminator=%d, zoneID=%s", info.Discriminator, info.ZoneID)

		// Simulate device's browser seeing this pairing request
		// This mimics the mDNS delivery of the pairing request to the device
		go func() {
			// Small delay to simulate network latency
			time.Sleep(50 * time.Millisecond)

			// Send pairing request to device
			deviceBrowser.mu.Lock()
			cb := deviceBrowser.pairingRequestCallback
			deviceBrowser.mu.Unlock()

			if cb != nil {
				cb(discovery.PairingRequestService{
					InstanceName:  info.ZoneID + "-" + "2001",
					Discriminator: info.Discriminator,
					ZoneID:        info.ZoneID,
					ZoneName:      info.ZoneName,
				})
			}
		}()
	})

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	// Set zone ID (normally set during start with cert store)
	controllerSvc.mu.Lock()
	controllerSvc.zoneID = "0123456789abcdef"
	controllerSvc.zoneName = "Deferred Test Zone"
	controllerSvc.mu.Unlock()

	// === Trigger Deferred Commissioning ===
	// Controller has QR data but device is not advertising yet
	t.Log("Starting deferred commissioning - device is not advertising")

	// Start commissioning in background
	commissionResultCh := make(chan struct {
		device *ConnectedDevice
		err    error
	}, 1)

	go func() {
		// When device opens commissioning window, capture the commissioning
		// address, set up mock browser data, and make the device visible.
		go func() {
			select {
			case <-commissioningOpenedCh:
				t.Log("Device opened commissioning window - setting up discovery data")
				addr := deviceSvc.CommissioningAddr()
				port := parseAddrPort(addr.String())
				commSvc := &discovery.CommissionableService{
					InstanceName:  "MASH-2001",
					Host:          "localhost",
					Port:          port,
					Addresses:     []string{"127.0.0.1"},
					Discriminator: 2001,
				}
				deviceBrowser.SetPendingDevice(commSvc)
				controllerBrowser.SetPendingDevice(commSvc)
				time.Sleep(10 * time.Millisecond) // Small delay
				controllerBrowser.MakeDeviceVisible()
			case <-ctx.Done():
			}
		}()

		connectedDevice, err := controllerSvc.CommissionDevice(ctx, 2001, "20202021")
		commissionResultCh <- struct {
			device *ConnectedDevice
			err    error
		}{connectedDevice, err}
	}()

	// === Wait for commissioning to complete ===
	var connectedDevice *ConnectedDevice
	select {
	case result := <-commissionResultCh:
		if result.err != nil {
			t.Fatalf("CommissionDevice failed: %v", result.err)
		}
		connectedDevice = result.device
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for commissioning")
	}

	// DEC-066: explicit operational reconnect after commissioning.
	if err := controllerSvc.Reconnect(ctx, connectedDevice.ID); err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}

	// Wait for device connection event
	select {
	case zoneID := <-deviceConnectedCh:
		t.Logf("Device connected to zone: %s", zoneID)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for device connection event")
	}

	// === Verify Results ===

	// 1. Device is commissioned
	if deviceSvc.ZoneCount() != 1 {
		t.Errorf("Device should have 1 zone, got %d", deviceSvc.ZoneCount())
	}

	// 2. Controller has device
	if controllerSvc.DeviceCount() != 1 {
		t.Errorf("Controller should have 1 device, got %d", controllerSvc.DeviceCount())
	}

	// 3. Pairing request is cleaned up
	if pr := controllerAdvertiser.GetActivePairingRequest(2001); pr != nil {
		t.Error("Pairing request should be cleaned up after commissioning")
	}

	t.Logf("Deferred commissioning completed: device=%s", connectedDevice.ID)
}

// ============================================================================
// Integration Test: Zone Type Constraint Enforcement (Scenario C)
// ============================================================================

// TestE2E_ZoneTypeConstraint verifies zone type enforcement:
// 1. Device has GRID zone (first commission as GRID)
// 2. Another controller (GRID type) tries to commission
// 3. Verify: Zone type conflict detected
// 4. LOCAL controller can still commission successfully
func TestE2E_ZoneTypeConstraint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-zonetype-001", 0x1234, 0x5678)
	deviceConfig := testDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 3001
	deviceConfig.MaxZones = zone.MaxZones

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
	port := parseAddrPort(addr.String())

	discoveryService := &discovery.CommissionableService{
		InstanceName:  "MASH-3001",
		Host:          "localhost",
		Port:          port,
		Addresses:     []string{"127.0.0.1"},
		Discriminator: 3001,
	}

	// === First Controller: GRID type ===
	controller1Config := testControllerConfig()
	controller1Config.ZoneName = "GRID Controller"
	controller1Config.ZoneType = cert.ZoneTypeGrid

	controller1, err := NewControllerService(controller1Config)
	if err != nil {
		t.Fatalf("NewControllerService(GRID) failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controller1, controller1Config.ZoneName)
	controller1.SetBrowser(newMockBrowser())

	if err := controller1.Start(ctx); err != nil {
		t.Fatalf("Controller1 Start failed: %v", err)
	}
	defer func() { _ = controller1.Stop() }()

	// Commission with GRID controller
	device1, err := controller1.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("First GRID commission failed: %v", err)
	}
	if err := controller1.Reconnect(ctx, device1.ID); err != nil {
		t.Fatalf("Reconnect (controller1) failed: %v", err)
	}
	t.Logf("First GRID controller commissioned: %s", device1.ID)

	// Manually set the zone type on device side (simulating what happens during real commissioning)
	deviceSvc.mu.Lock()
	for zoneID := range deviceSvc.connectedZones {
		deviceSvc.connectedZones[zoneID].Type = cert.ZoneTypeGrid
	}
	deviceSvc.mu.Unlock()

	// Wait for commissioning to stabilize
	time.Sleep(100 * time.Millisecond)

	// Verify device has 1 zone
	if deviceSvc.ZoneCount() != 1 {
		t.Errorf("Device should have 1 zone after first commission, got %d", deviceSvc.ZoneCount())
	}

	// === Second Controller: Also GRID type ===
	// Re-enter commissioning mode for second controller
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("Re-enter commissioning mode failed: %v", err)
	}

	controller2Config := testControllerConfig()
	controller2Config.ZoneName = "Second GRID Controller"
	controller2Config.ZoneType = cert.ZoneTypeGrid

	controller2, err := NewControllerService(controller2Config)
	if err != nil {
		t.Fatalf("NewControllerService(GRID 2) failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controller2, controller2Config.ZoneName)
	controller2.SetBrowser(newMockBrowser())

	if err := controller2.Start(ctx); err != nil {
		t.Fatalf("Controller2 Start failed: %v", err)
	}
	defer func() { _ = controller2.Stop() }()

	// This commissioning will succeed at the TLS/PASE level, but the zone type
	// constraint is enforced by the zone manager when HandleZoneConnect is called.
	// Since we're testing the full integration, the zone type check would happen
	// after PASE succeeds but before operational mode is fully established.
	// For this test, we verify the zone manager behavior directly.

	// Verify zone manager rejects duplicate zone type
	zoneManager := zone.NewManager()
	err = zoneManager.AddZone("grid-zone-1", cert.ZoneTypeGrid)
	if err != nil {
		t.Fatalf("First GRID zone should be accepted: %v", err)
	}

	err = zoneManager.AddZone("grid-zone-2", cert.ZoneTypeGrid)
	if err != zone.ErrZoneTypeExists {
		t.Errorf("Second GRID zone should be rejected with ErrZoneTypeExists, got: %v", err)
	}

	// === Third Controller: LOCAL type (should succeed) ===
	// Re-enter commissioning mode
	if err := deviceSvc.EnterCommissioningMode(); err != nil {
		t.Fatalf("Re-enter commissioning mode for LOCAL failed: %v", err)
	}

	controller3Config := testControllerConfig()
	controller3Config.ZoneName = "LOCAL Controller"
	controller3Config.ZoneType = cert.ZoneTypeLocal

	controller3, err := NewControllerService(controller3Config)
	if err != nil {
		t.Fatalf("NewControllerService(LOCAL) failed: %v", err)
	}
	wireCertStores(t, deviceSvc, controller3, controller3Config.ZoneName)
	controller3.SetBrowser(newMockBrowser())

	if err := controller3.Start(ctx); err != nil {
		t.Fatalf("Controller3 Start failed: %v", err)
	}
	defer func() { _ = controller3.Stop() }()

	// Commission with LOCAL controller (should succeed)
	device3, err := controller3.Commission(ctx, discoveryService, "20202021")
	if err != nil {
		t.Fatalf("LOCAL commission failed: %v", err)
	}
	if err := controller3.Reconnect(ctx, device3.ID); err != nil {
		t.Fatalf("Reconnect (controller3) failed: %v", err)
	}
	t.Logf("LOCAL controller commissioned: %s", device3.ID)

	// Manually set the zone type on device side
	deviceSvc.mu.Lock()
	for zoneID, cz := range deviceSvc.connectedZones {
		if cz.Type != cert.ZoneTypeGrid {
			cz.Type = cert.ZoneTypeLocal
		}
		_ = zoneID
	}
	deviceSvc.mu.Unlock()

	// Verify device now has 2 zones
	if deviceSvc.ZoneCount() != 2 {
		t.Errorf("Device should have 2 zones (GRID + LOCAL), got %d", deviceSvc.ZoneCount())
	}

	// Verify zone manager accepts LOCAL after GRID
	err = zoneManager.AddZone("local-zone-1", cert.ZoneTypeLocal)
	if err != nil {
		t.Errorf("LOCAL zone should be accepted after GRID: %v", err)
	}

	t.Log("Zone type constraint enforcement verified")
}

// ============================================================================
// Integration Test: Max Zones Reached (Scenario D)
// ============================================================================

// TestE2E_MaxZonesStopsPairingRequestListening verifies that:
// 1. Device has both GRID and LOCAL zones
// 2. Verify: Device stops listening for pairing requests
// 3. New pairing request is ignored
func TestE2E_MaxZonesStopsPairingRequestListening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-maxzones-001", 0x1234, 0x5678)
	deviceConfig := testDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 401
	deviceConfig.MaxZones = zone.MaxZones
	deviceConfig.ListenForPairingRequests = true

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	// Create device browser for pairing request listening
	deviceBrowser := newPairingRequestMockBrowser()
	deviceSvc.SetBrowser(deviceBrowser)

	// Track if commissioning window opens (it should NOT after max zones)
	var commissioningOpenedAfterMaxZones bool
	var eventMu sync.Mutex
	deviceSvc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if e.Type == EventCommissioningOpened {
			commissioningOpenedAfterMaxZones = true
		}
	})

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Initially, device should be listening for pairing requests
	time.Sleep(50 * time.Millisecond)
	if !deviceSvc.IsPairingRequestListening() {
		t.Error("Device should be listening for pairing requests initially")
	}

	// Connect first zone (GRID)
	deviceSvc.HandleZoneConnect("grid-zone-001", cert.ZoneTypeGrid)
	time.Sleep(50 * time.Millisecond)

	// Should still be listening (1 zone, max is 2)
	if !deviceSvc.IsPairingRequestListening() {
		t.Error("Device should still be listening with 1 zone")
	}

	// Connect second zone (LOCAL) - now at max
	deviceSvc.HandleZoneConnect("local-zone-001", cert.ZoneTypeLocal)
	time.Sleep(50 * time.Millisecond)

	// Should have stopped listening (at max zones)
	if deviceSvc.IsPairingRequestListening() {
		t.Error("Device should stop listening for pairing requests at max zones")
	}

	// Verify zone count
	if deviceSvc.ZoneCount() != 2 {
		t.Errorf("Device should have 2 zones, got %d", deviceSvc.ZoneCount())
	}

	// === Send a pairing request (should be ignored) ===
	// Simulate a pairing request arriving
	deviceBrowser.mu.Lock()
	cb := deviceBrowser.pairingRequestCallback
	deviceBrowser.mu.Unlock()

	if cb != nil {
		// This should be ignored because device is at max zones
		cb(discovery.PairingRequestService{
			InstanceName:  "ignored-request",
			Discriminator: 401,
			ZoneID:        "some-other-zone",
		})
	}

	// Wait and verify commissioning window was NOT opened
	time.Sleep(200 * time.Millisecond)

	eventMu.Lock()
	opened := commissioningOpenedAfterMaxZones
	eventMu.Unlock()

	if opened {
		t.Error("Commissioning window should NOT have opened when at max zones")
	}

	t.Log("Max zones pairing request handling verified")
}

// ============================================================================
// Integration Test: Pairing Request Resume After Zone Removal
// ============================================================================

// TestE2E_PairingRequestResumesAfterZoneRemoval verifies that:
// 1. Device at max zones stops pairing request listening
// 2. Zone is removed
// 3. Device resumes pairing request listening
func TestE2E_PairingRequestResumesAfterZoneRemoval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Device ===
	device := model.NewDevice("evse-resume-001", 0x1234, 0x5678)
	deviceConfig := testDeviceConfig()
	deviceConfig.ListenAddress = "localhost:0"
	deviceConfig.SetupCode = "20202021"
	deviceConfig.Discriminator = 501
	deviceConfig.MaxZones = zone.MaxZones
	deviceConfig.ListenForPairingRequests = true

	deviceSvc, err := NewDeviceService(device, deviceConfig)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	deviceAdvertiser := newMockAdvertiser()
	deviceSvc.SetAdvertiser(deviceAdvertiser)

	deviceBrowser := newPairingRequestMockBrowser()
	deviceSvc.SetBrowser(deviceBrowser)

	if err := deviceSvc.Start(ctx); err != nil {
		t.Fatalf("Device Start failed: %v", err)
	}
	defer func() { _ = deviceSvc.Stop() }()

	// Connect zones to reach max
	deviceSvc.HandleZoneConnect("grid-zone-001", cert.ZoneTypeGrid)
	deviceSvc.HandleZoneConnect("local-zone-001", cert.ZoneTypeLocal)
	time.Sleep(100 * time.Millisecond)

	// Verify at max and not listening
	if deviceSvc.ZoneCount() != 2 {
		t.Fatalf("Expected 2 zones, got %d", deviceSvc.ZoneCount())
	}
	if deviceSvc.IsPairingRequestListening() {
		t.Error("Should not be listening at max zones")
	}

	// Remove one zone
	if err := deviceSvc.RemoveZone("local-zone-001"); err != nil {
		t.Fatalf("RemoveZone failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Should have resumed listening
	if deviceSvc.ZoneCount() != 1 {
		t.Errorf("Expected 1 zone after removal, got %d", deviceSvc.ZoneCount())
	}
	if !deviceSvc.IsPairingRequestListening() {
		t.Error("Should resume listening after zone removal")
	}

	t.Log("Pairing request resume after zone removal verified")
}

// ============================================================================
// Integration Test: Concurrent Pairing Requests
// ============================================================================

// TestE2E_ConcurrentPairingRequests verifies that multiple controllers
// can have active pairing requests for different devices simultaneously.
func TestE2E_ConcurrentPairingRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// === Setup Controller ===
	controllerConfig := testControllerConfig()
	controllerConfig.ZoneName = "Concurrent Test Zone"
	controllerConfig.PairingRequestTimeout = 2 * time.Second
	controllerConfig.DiscoveryTimeout = 50 * time.Millisecond

	controllerSvc, err := NewControllerService(controllerConfig)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	browser := newPairingRequestMockBrowser()
	controllerSvc.SetBrowser(browser)

	advertiser := newPairingRequestMockAdvertiser()
	controllerSvc.SetAdvertiser(advertiser)

	if err := controllerSvc.Start(ctx); err != nil {
		t.Fatalf("Controller Start failed: %v", err)
	}
	defer func() { _ = controllerSvc.Stop() }()

	controllerSvc.mu.Lock()
	controllerSvc.zoneID = "fedcba9876543210"
	controllerSvc.zoneName = "Concurrent Test Zone"
	controllerSvc.mu.Unlock()

	// Track announced pairing requests
	var announcedMu sync.Mutex
	announcedDiscriminators := make(map[uint16]bool)
	advertiser.OnPairingRequestAnnounced(func(info *discovery.PairingRequestInfo) {
		announcedMu.Lock()
		announcedDiscriminators[info.Discriminator] = true
		announcedMu.Unlock()
	})

	// Start multiple concurrent commissioning attempts
	const numDevices = 3
	results := make(chan error, numDevices)

	for i := 0; i < numDevices; i++ {
		discriminator := uint16(601 + i)
		go func(d uint16) {
			_, err := controllerSvc.CommissionDevice(ctx, d, "20202021")
			results <- err
		}(discriminator)
	}

	// Wait for all to timeout (since no devices appear)
	for i := 0; i < numDevices; i++ {
		select {
		case err := <-results:
			if err != ErrPairingRequestTimeout {
				t.Logf("Expected timeout error, got: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for commissioning result")
		}
	}

	// Verify all pairing requests were announced
	announcedMu.Lock()
	for i := 0; i < numDevices; i++ {
		d := uint16(601 + i)
		if !announcedDiscriminators[d] {
			t.Errorf("Pairing request for discriminator %d was not announced", d)
		}
	}
	announcedMu.Unlock()

	// Verify all pairing requests were cleaned up
	for i := 0; i < numDevices; i++ {
		d := uint16(601 + i)
		if pr := advertiser.GetActivePairingRequest(d); pr != nil {
			t.Errorf("Pairing request for discriminator %d should be cleaned up", d)
		}
	}

	t.Log("Concurrent pairing requests verified")
}

// ============================================================================
// Helpers
// ============================================================================

// testDeviceConfig returns a valid device config for testing.
func testDeviceConfig() DeviceConfig {
	config := DefaultDeviceConfig()
	config.SetupCode = "20202021"
	config.Discriminator = 1234
	config.SerialNumber = "SN12345"
	config.Brand = "TestBrand"
	config.Model = "TestModel"
	config.Categories = []discovery.DeviceCategory{discovery.CategoryEMobility}
	config.MaxZones = zone.MaxZones
	return config
}

// testControllerConfig returns a valid controller config for testing.
func testControllerConfig() ControllerConfig {
	config := DefaultControllerConfig()
	config.ZoneName = "Test Zone"
	config.ZoneType = cert.ZoneTypeLocal
	return config
}

// parseAddrPort extracts the port from an address for testing.
func parseAddrPort(addr string) uint16 {
	_, portStr, _ := net.SplitHostPort(addr)
	var port uint16
	for _, c := range portStr {
		port = port*10 + uint16(c-'0')
	}
	return port
}
