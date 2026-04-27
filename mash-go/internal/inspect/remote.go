package inspect

import (
	"context"
	"errors"
)

// SessionReader defines the interface for reading/writing to a remote device.
// This is implemented by service.DeviceSession.
//
// Invoke params and response are integer-keyed per
// docs/interaction-model.md §7.1 / §7.2 (Issue #11 fix).
type SessionReader interface {
	DeviceID() string
	Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error)
	Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error)
	Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error)
}

// RemoteInspector provides inspection and mutation capabilities for remote devices
// via a DeviceSession.
type RemoteInspector struct {
	session SessionReader
}

// NewRemoteInspector creates a new remote inspector for the given session.
func NewRemoteInspector(session SessionReader) *RemoteInspector {
	return &RemoteInspector{
		session: session,
	}
}

// DeviceID returns the remote device's ID.
func (r *RemoteInspector) DeviceID() string {
	return r.session.DeviceID()
}

// ReadAttribute reads a single attribute from the remote device.
func (r *RemoteInspector) ReadAttribute(ctx context.Context, path *Path) (any, error) {
	if path == nil {
		return nil, errors.New("path is nil")
	}
	if path.IsPartial {
		return nil, errors.New("path is partial, use ReadAllAttributes for feature-level reads")
	}

	attrs, err := r.session.Read(ctx, path.EndpointID, path.FeatureID, []uint16{path.AttributeID})
	if err != nil {
		return nil, err
	}

	value, ok := attrs[path.AttributeID]
	if !ok {
		return nil, errors.New("attribute not found in response")
	}

	return value, nil
}

// ReadAllAttributes reads all attributes from a feature on the remote device.
func (r *RemoteInspector) ReadAllAttributes(ctx context.Context, endpointID uint8, featureID uint8) (map[uint16]any, error) {
	// Pass nil to read all attributes
	return r.session.Read(ctx, endpointID, featureID, nil)
}

// WriteAttribute writes a single attribute to the remote device.
func (r *RemoteInspector) WriteAttribute(ctx context.Context, path *Path, value any) error {
	if path == nil {
		return errors.New("path is nil")
	}
	if path.IsPartial {
		return errors.New("path is partial, cannot write to partial path")
	}

	attrs := map[uint16]any{
		path.AttributeID: value,
	}

	_, err := r.session.Write(ctx, path.EndpointID, path.FeatureID, attrs)
	return err
}

// InvokeCommand invokes a command on the remote device.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (r *RemoteInspector) InvokeCommand(ctx context.Context, path *Path, params map[uint8]any) (any, error) {
	if path == nil {
		return nil, errors.New("path is nil")
	}
	if !path.IsCommand {
		return nil, errors.New("path is not a command path")
	}

	return r.session.Invoke(ctx, path.EndpointID, path.FeatureID, path.CommandID, params)
}
