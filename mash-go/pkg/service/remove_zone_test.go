package service

import (
	"context"
	"testing"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
)

func TestRemoveZoneHandler_SelfRemoval(t *testing.T) {
	// Setup: Create a device service with one connected zone
	device := model.NewDevice("test-device", 1234, 5678)
	device.AddEndpoint(&model.Endpoint{})

	svc := &DeviceService{
		deviceID:       "test-device",
		device:         device,
		connectedZones: make(map[string]*ConnectedZone),
		zoneSessions:   make(map[string]*ZoneSession),
		zoneIndexMap:   make(map[string]uint8),
		failsafeTimers: make(map[string]*failsafe.Timer),
	}

	// Simulate a connected zone
	zoneID := "zone-abc123"
	svc.connectedZones[zoneID] = &ConnectedZone{
		ID: zoneID,
	}

	// Create handler
	handler := svc.makeRemoveZoneHandler()

	// Create context with caller zone ID
	ctx := ContextWithCallerZoneID(context.Background(), zoneID)

	// Invoke: self-removal (zone removes itself)
	params := map[uint8]any{
		features.RemoveZoneParamZoneID: zoneID,
	}
	result, err := handler(ctx, params)

	// Assert: success
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if removed, ok := result[features.RemoveZoneRespRemoved].(bool); !ok || !removed {
		t.Errorf("expected removed=true, got %v", result[features.RemoveZoneRespRemoved])
	}

	// Verify zone was removed
	if len(svc.connectedZones) != 0 {
		t.Errorf("expected 0 zones, got %d", len(svc.connectedZones))
	}
}

func TestRemoveZoneHandler_RejectsOtherZone(t *testing.T) {
	// Setup: Create a device service with two connected zones (no enable-key)
	device := model.NewDevice("test-device", 1234, 5678)
	device.AddEndpoint(&model.Endpoint{})

	svc := &DeviceService{
		deviceID:       "test-device",
		device:         device,
		config:         DeviceConfig{}, // No enable-key
		connectedZones: make(map[string]*ConnectedZone),
		zoneSessions:   make(map[string]*ZoneSession),
		zoneIndexMap:   make(map[string]uint8),
		failsafeTimers: make(map[string]*failsafe.Timer),
	}

	// Simulate two connected zones
	zoneA := "zone-aaaaaa"
	zoneB := "zone-bbbbbb"
	svc.connectedZones[zoneA] = &ConnectedZone{ID: zoneA}
	svc.connectedZones[zoneB] = &ConnectedZone{ID: zoneB}

	// Create handler
	handler := svc.makeRemoveZoneHandler()

	// Create context with caller zone A
	ctx := ContextWithCallerZoneID(context.Background(), zoneA)

	// Invoke: zone A tries to remove zone B (not allowed without enable-key)
	params := map[uint8]any{
		features.RemoveZoneParamZoneID: zoneB,
	}
	_, err := handler(ctx, params)

	// Assert: error - permission denied
	if err == nil {
		t.Fatal("expected error for cross-zone removal without enable-key")
	}
	if err != model.ErrCommandNotAllowed {
		t.Errorf("expected ErrCommandNotAllowed, got: %v", err)
	}

	// Verify both zones still exist
	if len(svc.connectedZones) != 2 {
		t.Errorf("expected 2 zones, got %d", len(svc.connectedZones))
	}
}

func TestRemoveZoneHandler_TestZoneAllowsCrossZone(t *testing.T) {
	// Setup: Create a device service with two connected zones where the
	// caller is a TEST zone (which is allowed to remove any zone).
	device := model.NewDevice("test-device", 1234, 5678)
	device.AddEndpoint(&model.Endpoint{})

	svc := &DeviceService{
		deviceID:       "test-device",
		device:         device,
		connectedZones: make(map[string]*ConnectedZone),
		zoneSessions:   make(map[string]*ZoneSession),
		zoneIndexMap:   make(map[string]uint8),
		failsafeTimers: make(map[string]*failsafe.Timer),
	}

	zoneA := "zone-aaaaaa"
	zoneB := "zone-bbbbbb"
	svc.connectedZones[zoneA] = &ConnectedZone{ID: zoneA}
	svc.connectedZones[zoneB] = &ConnectedZone{ID: zoneB}

	handler := svc.makeRemoveZoneHandler()

	// Zone A (TEST type) removes zone B (allowed for TEST zones)
	ctx := ContextWithCallerZoneID(context.Background(), zoneA)
	ctx = ContextWithCallerZoneType(ctx, cert.ZoneTypeTest)
	params := map[uint8]any{
		features.RemoveZoneParamZoneID: zoneB,
	}
	result, err := handler(ctx, params)
	if err != nil {
		t.Fatalf("expected no error for TEST zone cross-removal, got: %v", err)
	}
	if removed, ok := result[features.RemoveZoneRespRemoved].(bool); !ok || !removed {
		t.Errorf("expected removed=true, got %v", result[features.RemoveZoneRespRemoved])
	}
	if len(svc.connectedZones) != 1 {
		t.Errorf("expected 1 zone after removal, got %d", len(svc.connectedZones))
	}
}

func TestRemoveZoneHandler_InvalidParameters(t *testing.T) {
	device := model.NewDevice("test-device", 1234, 5678)
	device.AddEndpoint(&model.Endpoint{})

	svc := &DeviceService{
		deviceID:       "test-device",
		device:         device,
		connectedZones: make(map[string]*ConnectedZone),
		zoneSessions:   make(map[string]*ZoneSession),
		zoneIndexMap:   make(map[string]uint8),
		failsafeTimers: make(map[string]*failsafe.Timer),
	}

	handler := svc.makeRemoveZoneHandler()
	ctx := ContextWithCallerZoneID(context.Background(), "zone-abc")

	t.Run("MissingZoneID", func(t *testing.T) {
		params := map[uint8]any{}
		_, err := handler(ctx, params)
		if err != model.ErrInvalidParameters {
			t.Errorf("expected ErrInvalidParameters, got: %v", err)
		}
	})

	t.Run("WrongTypeZoneID", func(t *testing.T) {
		params := map[uint8]any{
			features.RemoveZoneParamZoneID: 12345, // wrong type
		}
		_, err := handler(ctx, params)
		if err != model.ErrInvalidParameters {
			t.Errorf("expected ErrInvalidParameters, got: %v", err)
		}
	})
}

func TestRemoveZoneHandler_NoCallerContext(t *testing.T) {
	device := model.NewDevice("test-device", 1234, 5678)
	device.AddEndpoint(&model.Endpoint{})

	svc := &DeviceService{
		deviceID:       "test-device",
		device:         device,
		connectedZones: make(map[string]*ConnectedZone),
		zoneSessions:   make(map[string]*ZoneSession),
		zoneIndexMap:   make(map[string]uint8),
		failsafeTimers: make(map[string]*failsafe.Timer),
	}

	handler := svc.makeRemoveZoneHandler()

	// No caller zone ID in context
	ctx := context.Background()
	params := map[uint8]any{
		features.RemoveZoneParamZoneID: "zone-abc",
	}

	_, err := handler(ctx, params)
	if err != model.ErrCommandNotAllowed {
		t.Errorf("expected ErrCommandNotAllowed when no caller context, got: %v", err)
	}
}

func TestCallerZoneIDContext(t *testing.T) {
	t.Run("SetAndGet", func(t *testing.T) {
		ctx := ContextWithCallerZoneID(context.Background(), "zone-123")
		zoneID := CallerZoneIDFromContext(ctx)
		if zoneID != "zone-123" {
			t.Errorf("expected zone-123, got %s", zoneID)
		}
	})

	t.Run("EmptyIfNotSet", func(t *testing.T) {
		ctx := context.Background()
		zoneID := CallerZoneIDFromContext(ctx)
		if zoneID != "" {
			t.Errorf("expected empty string, got %s", zoneID)
		}
	})
}

func TestDeviceInfo_HasRemoveZoneCommand(t *testing.T) {
	// Create a device with DeviceInfo feature
	device := model.NewDevice("test-device", 1234, 5678)
	deviceInfo := features.NewDeviceInfo()
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Create device service (this should register the RemoveZone command)
	config := DeviceConfig{
		SetupCode:     "20202021",
		Discriminator: 100,
		ListenAddress: "127.0.0.1:0",
		SerialNumber:  "TEST-001",
		Brand:         "TestBrand",
		Model:         "TestModel",
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility}, // At least one category required
	}
	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("failed to create device service: %v", err)
	}
	_ = svc

	// Verify the command was registered
	cmdList := deviceInfo.CommandList()
	found := false
	for _, cmdID := range cmdList {
		if cmdID == features.DeviceInfoCmdRemoveZone {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected RemoveZone command (0x%02x) in command list, got: %v", features.DeviceInfoCmdRemoveZone, cmdList)
	}
}
