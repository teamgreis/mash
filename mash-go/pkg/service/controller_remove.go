package service

import (
	"context"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
)

// DeviceSessionInvoker is the interface needed to invoke commands on a device.
// This allows mocking in tests.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
type DeviceSessionInvoker interface {
	Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error)
	Close() error
}

// RemoveDevice removes a device from this controller's zone.
// It sends the RemoveZone command to the device, which will:
// - Clean up the zone on the device side
// - Close the connection
// The controller then cleans up its local state and emits EventDeviceRemoved.
func (s *ControllerService) RemoveDevice(ctx context.Context, deviceID string) error {
	s.mu.RLock()
	session, exists := s.deviceSessions[deviceID]
	s.mu.RUnlock()

	if !exists {
		return ErrDeviceNotFound
	}

	return s.removeDeviceWithSession(ctx, deviceID, session)
}

// removeDeviceWithSession performs the removal using the provided session.
// This allows injection of mock sessions for testing.
func (s *ControllerService) removeDeviceWithSession(ctx context.Context, deviceID string, session DeviceSessionInvoker) error {
	s.mu.RLock()
	zoneID := s.zoneID
	s.mu.RUnlock()

	// Send RemoveZone command to device — integer-keyed parameters per
	// docs/interaction-model.md §7.1 (Issue #11 fix).
	params := map[uint8]any{
		features.RemoveZoneParamZoneID: zoneID,
	}

	_, err := session.Invoke(ctx, 0, uint8(model.FeatureDeviceInfo), features.DeviceInfoCmdRemoveZone, params)
	if err != nil {
		return err
	}

	// Clean up local state
	s.mu.Lock()
	delete(s.connectedDevices, deviceID)
	delete(s.deviceSessions, deviceID)
	s.mu.Unlock()

	// Emit event
	s.emitEvent(Event{
		Type:     EventDeviceRemoved,
		DeviceID: deviceID,
	})

	return nil
}
