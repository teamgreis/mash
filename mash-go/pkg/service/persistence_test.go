package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery/mocks"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/persistence"
)

func TestDeviceServiceSetStateStore(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	dir := t.TempDir()
	store := persistence.NewDeviceStateStore(filepath.Join(dir, "state.json"))

	svc.SetStateStore(store)

	// Verify it's set
	if svc.stateStore != store {
		t.Error("state store not set correctly")
	}
}

func TestDeviceServiceSaveLoadState(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser and start
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Connect a zone
	zoneID := "zone-persist-001"
	svc.HandleZoneConnect(zoneID, cert.ZoneTypeLocal)

	// Set up persistence
	dir := t.TempDir()
	store := persistence.NewDeviceStateStore(filepath.Join(dir, "state.json"))
	svc.SetStateStore(store)

	// Save state
	if err := svc.SaveState(); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Create new service and load state
	svc2, err := NewDeviceService(model.NewDevice("test-device", 0x1234, 0x5678), config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}
	svc2.SetStateStore(store)

	if err := svc2.LoadState(); err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}

	// Verify zone index was restored
	svc2.mu.RLock()
	idx, exists := svc2.zoneIndexMap[zoneID]
	svc2.mu.RUnlock()

	if !exists {
		t.Error("zone index map not restored")
	}
	if idx != 0 {
		t.Errorf("zone index = %d, want 0", idx)
	}

	// Verify connected zones were restored (with Connected: false)
	zones := svc2.GetAllZones()
	if len(zones) != 1 {
		t.Fatalf("expected 1 zone, got %d", len(zones))
	}
	if zones[0].ID != zoneID {
		t.Errorf("zone ID = %q, want %q", zones[0].ID, zoneID)
	}
	if zones[0].Type != cert.ZoneTypeLocal {
		t.Errorf("zone type = %v, want HomeManager", zones[0].Type)
	}
	if zones[0].Connected {
		t.Error("restored zone should have Connected = false")
	}
}

func TestDeviceServiceSaveStateWithFailsafeTimer(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser and start
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().AdvertiseOperational(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Connect a zone with a failsafe timer
	zoneID := "zone-failsafe-001"
	svc.HandleZoneConnect(zoneID, cert.ZoneTypeLocal)

	// F-32: timers are no longer auto-created on connect (the failsafe
	// state machine fires from disconnect, not as a watchdog). Install one
	// explicitly through the legacy API so the persistence path has
	// something to snapshot.
	t0 := failsafe.NewTimer()
	svc.SetFailsafeTimer(zoneID, t0)
	t0.Start()
	timer := svc.GetFailsafeTimer(zoneID)
	if timer == nil {
		t.Fatal("failsafe timer not created")
	}

	// Verify timer is running
	if timer.State() != failsafe.StateTimerRunning {
		t.Errorf("timer state = %v, want TIMER_RUNNING", timer.State())
	}

	// Set up persistence
	dir := t.TempDir()
	store := persistence.NewDeviceStateStore(filepath.Join(dir, "state.json"))
	svc.SetStateStore(store)

	// Save state
	if err := svc.SaveState(); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Load and verify failsafe state was persisted
	state, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load() error = %v", err)
	}

	if len(state.FailsafeState) != 1 {
		t.Errorf("expected 1 failsafe state, got %d", len(state.FailsafeState))
	}

	snap, exists := state.FailsafeState[zoneID]
	if !exists {
		t.Fatal("failsafe state for zone not found")
	}

	if snap.State != uint8(failsafe.StateTimerRunning) {
		t.Errorf("failsafe state = %d, want %d (TIMER_RUNNING)", snap.State, failsafe.StateTimerRunning)
	}
}

func TestDeviceServiceLoadStateBackwardCompatibility(t *testing.T) {
	// Test that zones are derived from zone_index_map when state.Zones is empty
	// (backward compatibility with old state files)
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	dir := t.TempDir()
	store := persistence.NewDeviceStateStore(filepath.Join(dir, "state.json"))
	svc.SetStateStore(store)

	// Manually create a state file without the Zones field (old format)
	oldState := &persistence.DeviceState{
		SavedAt: time.Now(),
		ZoneIndexMap: map[string]uint8{
			"zone-old-001": 0,
			"zone-old-002": 1,
		},
		// Note: Zones field is intentionally not set (simulating old format)
	}
	if err := store.Save(oldState); err != nil {
		t.Fatalf("store.Save() error = %v", err)
	}

	// Load state
	if err := svc.LoadState(); err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}

	// Verify zones were derived from zone_index_map
	zones := svc.GetAllZones()
	if len(zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(zones))
	}

	// Check that zones were created with default type (HomeManager)
	for _, z := range zones {
		if z.Type != cert.ZoneTypeLocal {
			t.Errorf("zone %s: type = %v, want HomeManager", z.ID, z.Type)
		}
		if z.Connected {
			t.Errorf("zone %s: should have Connected = false", z.ID)
		}
	}
}

func TestDeviceServiceLoadStateNoStore(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// LoadState without store should be no-op (not error)
	if err := svc.LoadState(); err != nil {
		t.Errorf("LoadState() without store should not error, got %v", err)
	}
}

func TestDeviceServiceSaveStateNoStore(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// SaveState without store should be no-op (not error)
	if err := svc.SaveState(); err != nil {
		t.Errorf("SaveState() without store should not error, got %v", err)
	}
}

func TestControllerServiceSetStateStore(t *testing.T) {
	config := validControllerConfig()

	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	dir := t.TempDir()
	store := persistence.NewControllerStateStore(filepath.Join(dir, "state.json"))

	svc.SetStateStore(store)

	if svc.stateStore != store {
		t.Error("state store not set correctly")
	}
}

func TestControllerServiceSaveLoadState(t *testing.T) {
	config := validControllerConfig()

	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	// Set up mock browser and start
	browser := mocks.NewMockBrowser(t)
	browser.EXPECT().Stop().Return().Maybe()
	svc.SetBrowser(browser)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Set zone ID and add a device
	svc.mu.Lock()
	svc.zoneID = "my-zone-123"
	deviceID := "device-001"
	svc.connectedDevices[deviceID] = &ConnectedDevice{
		ID:        deviceID,
		Host:      "evse-001.local",
		Port:      8443,
		Connected: true,
		LastSeen:  time.Now(),
	}
	svc.mu.Unlock()

	// Set up persistence
	dir := t.TempDir()
	store := persistence.NewControllerStateStore(filepath.Join(dir, "state.json"))
	svc.SetStateStore(store)

	// Save state
	if err := svc.SaveState(); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Create new service and load state
	svc2, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}
	svc2.SetStateStore(store)

	if err := svc2.LoadState(); err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}

	// Verify zone ID was restored
	if svc2.ZoneID() != "my-zone-123" {
		t.Errorf("zone ID = %q, want %q", svc2.ZoneID(), "my-zone-123")
	}

	// Verify device list was restored
	if svc2.DeviceCount() != 1 {
		t.Errorf("device count = %d, want 1", svc2.DeviceCount())
	}
}

func TestControllerServiceLoadStateNoStore(t *testing.T) {
	config := validControllerConfig()

	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	// LoadState without store should be no-op
	if err := svc.LoadState(); err != nil {
		t.Errorf("LoadState() without store should not error, got %v", err)
	}
}

func TestControllerServiceSaveStateNoStore(t *testing.T) {
	config := validControllerConfig()

	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("NewControllerService failed: %v", err)
	}

	// SaveState without store should be no-op
	if err := svc.SaveState(); err != nil {
		t.Errorf("SaveState() without store should not error, got %v", err)
	}
}
