package service

import (
	"context"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// mockDeviceSessionForRemove is a minimal mock for testing RemoveDevice.
type mockDeviceSessionForRemove struct {
	deviceID     string
	invokeCalled bool
	invokeResult any
	invokeErr    error
	closed       bool
}

func (m *mockDeviceSessionForRemove) DeviceID() string {
	return m.deviceID
}

func (m *mockDeviceSessionForRemove) Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error) {
	return nil, nil
}

func (m *mockDeviceSessionForRemove) Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error) {
	return nil, nil
}

func (m *mockDeviceSessionForRemove) Subscribe(ctx context.Context, endpointID uint8, featureID uint8, opts any) (uint32, map[uint16]any, error) {
	return 0, nil, nil
}

func (m *mockDeviceSessionForRemove) Unsubscribe(ctx context.Context, subscriptionID uint32) error {
	return nil
}

func (m *mockDeviceSessionForRemove) Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error) {
	m.invokeCalled = true

	// Verify correct target
	if endpointID != 0 {
		return nil, model.ErrCommandFailed
	}
	if featureID != uint8(model.FeatureDeviceInfo) {
		return nil, model.ErrCommandFailed
	}
	if commandID != features.DeviceInfoCmdRemoveZone {
		return nil, model.ErrCommandFailed
	}

	if m.invokeErr != nil {
		return nil, m.invokeErr
	}
	return m.invokeResult, nil
}

func (m *mockDeviceSessionForRemove) SetNotificationHandler(handler func(*wire.Notification)) {}

func (m *mockDeviceSessionForRemove) Close() error {
	m.closed = true
	return nil
}

func TestControllerService_RemoveDevice_Success(t *testing.T) {
	// Create controller service
	config := ControllerConfig{
		ZoneName: "Test Zone",
		ZoneType: cert.ZoneTypeLocal,
	}
	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("failed to create controller service: %v", err)
	}

	// Manually set zone ID (normally done during Start)
	svc.zoneID = "zone-test-123"

	// Create mock session with success response
	mockSession := &mockDeviceSessionForRemove{
		deviceID: "device-abc",
		invokeResult: map[uint8]any{
			features.RemoveZoneRespRemoved: true,
		},
	}

	// Add device to controller's maps
	svc.connectedDevices["device-abc"] = &ConnectedDevice{
		ID:        "device-abc",
		Connected: true,
	}
	svc.deviceSessions["device-abc"] = &DeviceSession{
		deviceID: "device-abc",
	}

	// Track events using a channel to handle async event handlers
	eventCh := make(chan Event, 1)
	svc.OnEvent(func(e Event) {
		if e.Type == EventDeviceRemoved {
			eventCh <- e
		}
	})

	// Call RemoveDevice with mock session injected
	ctx := context.Background()
	err = svc.removeDeviceWithSession(ctx, "device-abc", mockSession)
	if err != nil {
		t.Fatalf("RemoveDevice failed: %v", err)
	}

	// Verify invoke was called
	if !mockSession.invokeCalled {
		t.Error("expected Invoke to be called")
	}

	// Verify device was removed from local state
	if _, exists := svc.connectedDevices["device-abc"]; exists {
		t.Error("device should be removed from connectedDevices")
	}
	if _, exists := svc.deviceSessions["device-abc"]; exists {
		t.Error("device should be removed from deviceSessions")
	}

	// Verify event was emitted (wait for async handler)
	select {
	case e := <-eventCh:
		if e.DeviceID != "device-abc" {
			t.Errorf("expected deviceID device-abc, got %s", e.DeviceID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected EventDeviceRemoved to be emitted")
	}
}

func TestControllerService_RemoveDevice_NotFound(t *testing.T) {
	config := ControllerConfig{
		ZoneName: "Test Zone",
		ZoneType: cert.ZoneTypeLocal,
	}
	svc, err := NewControllerService(config)
	if err != nil {
		t.Fatalf("failed to create controller service: %v", err)
	}
	svc.zoneID = "zone-test-123"

	ctx := context.Background()
	err = svc.RemoveDevice(ctx, "nonexistent-device")
	if err != ErrDeviceNotFound {
		t.Errorf("expected ErrDeviceNotFound, got: %v", err)
	}
}
