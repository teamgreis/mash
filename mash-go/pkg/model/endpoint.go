package model

import (
	"context"
	"errors"
	"sync"
)

// Endpoint errors.
var (
	ErrFeatureNotFound  = errors.New("feature not found")
	ErrEndpointNotFound = errors.New("endpoint not found")
)

// Endpoint represents a functional unit within a device.
type Endpoint struct {
	mu sync.RWMutex

	// ID is the endpoint identifier (0 is always DEVICE_ROOT).
	id uint8

	// Type is the endpoint type.
	endpointType EndpointType

	// Label is an optional human-readable label.
	label string

	// Features indexed by type.
	features map[FeatureType]*Feature
}

// NewEndpoint creates a new endpoint.
func NewEndpoint(id uint8, endpointType EndpointType, label string) *Endpoint {
	return &Endpoint{
		id:           id,
		endpointType: endpointType,
		label:        label,
		features:     make(map[FeatureType]*Feature),
	}
}

// ID returns the endpoint ID.
func (e *Endpoint) ID() uint8 {
	return e.id
}

// Type returns the endpoint type.
func (e *Endpoint) Type() EndpointType {
	return e.endpointType
}

// Label returns the endpoint label.
func (e *Endpoint) Label() string {
	return e.label
}

// SetLabel sets the endpoint label.
func (e *Endpoint) SetLabel(label string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.label = label
}

// AddFeature adds a feature to the endpoint.
func (e *Endpoint) AddFeature(feature *Feature) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.features[feature.Type()] = feature
}

// GetFeature returns a feature by type.
func (e *Endpoint) GetFeature(featureType FeatureType) (*Feature, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	feature, exists := e.features[featureType]
	if !exists {
		return nil, ErrFeatureNotFound
	}
	return feature, nil
}

// GetFeatureByID returns a feature by its numeric ID.
func (e *Endpoint) GetFeatureByID(featureID uint8) (*Feature, error) {
	return e.GetFeature(FeatureType(featureID))
}

// HasFeature returns true if the endpoint has the given feature.
func (e *Endpoint) HasFeature(featureType FeatureType) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, exists := e.features[featureType]
	return exists
}

// Features returns all features on this endpoint.
func (e *Endpoint) Features() []*Feature {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Feature, 0, len(e.features))
	for _, f := range e.features {
		result = append(result, f)
	}
	return result
}

// FeatureTypes returns the types of all features on this endpoint.
func (e *Endpoint) FeatureTypes() []FeatureType {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]FeatureType, 0, len(e.features))
	for ft := range e.features {
		result = append(result, ft)
	}
	return result
}

// ReadAttribute reads an attribute from a feature.
func (e *Endpoint) ReadAttribute(featureID uint8, attrID uint16) (any, error) {
	feature, err := e.GetFeatureByID(featureID)
	if err != nil {
		return nil, err
	}
	return feature.ReadAttribute(attrID)
}

// WriteAttribute writes an attribute to a feature.
func (e *Endpoint) WriteAttribute(featureID uint8, attrID uint16, value any) error {
	feature, err := e.GetFeatureByID(featureID)
	if err != nil {
		return err
	}
	return feature.WriteAttribute(attrID, value)
}

// InvokeCommand invokes a command on a feature.
//
// params and the response map are integer-keyed per
// docs/interaction-model.md §7.1 / §7.2 (Issue #11 fix).
func (e *Endpoint) InvokeCommand(ctx context.Context, featureID uint8, cmdID uint8, params map[uint8]any) (map[uint8]any, error) {
	feature, err := e.GetFeatureByID(featureID)
	if err != nil {
		return nil, err
	}
	return feature.InvokeCommand(ctx, cmdID, params)
}

// EndpointInfo returns information about this endpoint for discovery.
type EndpointInfo struct {
	ID       uint8        `cbor:"1,keyasint"`
	Type     EndpointType `cbor:"2,keyasint"`
	Label    string       `cbor:"3,keyasint,omitempty"`
	Features []uint16     `cbor:"4,keyasint"` // List of feature type IDs
}

// Info returns endpoint information for discovery.
func (e *Endpoint) Info() *EndpointInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	features := make([]uint16, 0, len(e.features))
	for ft := range e.features {
		features = append(features, uint16(ft))
	}

	return &EndpointInfo{
		ID:       e.id,
		Type:     e.endpointType,
		Label:    e.label,
		Features: features,
	}
}
