package service

import (
	"context"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// registerDeviceCommands registers service-level commands on the device's features.
// This must be called after the device model is set but before the service starts.
func (s *DeviceService) registerDeviceCommands() {
	// Get DeviceInfo feature from root endpoint (endpoint 0)
	rootEndpoint := s.device.RootEndpoint()
	if rootEndpoint == nil {
		return
	}

	deviceInfo, err := rootEndpoint.GetFeatureByID(uint8(model.FeatureDeviceInfo))
	if err != nil {
		return
	}

	// Register a read hook for dynamic attributes (e.g., zoneCount).
	deviceInfo.SetReadHook(func(ctx context.Context, attrID uint16) (any, bool) {
		if attrID == features.DeviceInfoAttrZoneCount {
			return uint8(s.ZoneCount()), true
		}
		return nil, false
	})

	// Register RemoveZone command
	removeZoneCmd := model.NewCommand(
		&model.CommandMetadata{
			ID:          features.DeviceInfoCmdRemoveZone,
			Name:        "RemoveZone",
			Description: "Remove a zone from this device (self-removal only)",
			Parameters: []model.ParameterMetadata{
				{ID: features.RemoveZoneParamZoneID, Name: "zoneId", Type: model.DataTypeString, Required: true, Description: "Zone ID to remove"},
			},
			Response: []model.ParameterMetadata{
				{ID: features.RemoveZoneRespRemoved, Name: "removed", Type: model.DataTypeBool, Description: "Whether the zone was removed"},
			},
		},
		s.makeRemoveZoneHandler(),
	)
	deviceInfo.AddCommand(removeZoneCmd)
}

// ContextWithCallerZoneID returns a new context with the caller's zone ID.
// Delegates to pkg/zonecontext.
func ContextWithCallerZoneID(ctx context.Context, zoneID string) context.Context {
	return zonecontext.ContextWithCallerZoneID(ctx, zoneID)
}

// CallerZoneIDFromContext extracts the caller's zone ID from the context.
// Returns empty string if not set. Delegates to pkg/zonecontext.
func CallerZoneIDFromContext(ctx context.Context) string {
	return zonecontext.CallerZoneIDFromContext(ctx)
}

// ContextWithCallerZoneType returns a new context with the caller's zone type.
// Delegates to pkg/zonecontext.
func ContextWithCallerZoneType(ctx context.Context, zoneType cert.ZoneType) context.Context {
	return zonecontext.ContextWithCallerZoneType(ctx, zoneType)
}

// CallerZoneTypeFromContext extracts the caller's zone type from the context.
// Returns 0 (invalid) if not set. Delegates to pkg/zonecontext.
func CallerZoneTypeFromContext(ctx context.Context) cert.ZoneType {
	return zonecontext.CallerZoneTypeFromContext(ctx)
}

// makeRemoveZoneHandler creates a command handler for the RemoveZone command.
// This handler validates that only the zone itself can request removal (self-removal).
//
// Parameters are integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (s *DeviceService) makeRemoveZoneHandler() model.CommandHandler {
	return func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		// Extract the zone ID parameter
		zoneIDParam, ok := params[features.RemoveZoneParamZoneID]
		if !ok {
			return nil, model.ErrInvalidParameters
		}
		zoneID, ok := zoneIDParam.(string)
		if !ok {
			return nil, model.ErrInvalidParameters
		}

		// Get the caller's zone ID from context
		callerZoneID := CallerZoneIDFromContext(ctx)
		if callerZoneID == "" {
			// No caller context - reject for safety
			return nil, model.ErrCommandNotAllowed
		}

		// Validate self-removal only: the caller must be the zone being removed.
		// Exception: TEST zones can remove any zone (needed for test
		// orchestration, e.g. TC-ZTYPE-005/007).
		callerZoneType := CallerZoneTypeFromContext(ctx)
		if zoneID != callerZoneID && callerZoneType != cert.ZoneTypeTest {
			return nil, model.ErrCommandNotAllowed
		}

		// Perform the removal
		if err := s.RemoveZone(zoneID); err != nil {
			return nil, err
		}

		return map[uint8]any{
			features.RemoveZoneRespRemoved: true,
		}, nil
	}
}
