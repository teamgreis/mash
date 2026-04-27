package model

import (
	"context"
	"errors"
	"sync"
)

// Device errors.
var (
	ErrDeviceNotConfigured = errors.New("device not configured")
	ErrDuplicateEndpoint   = errors.New("duplicate endpoint ID")
)

// Device represents a MASH device with its endpoint hierarchy.
// It is the top-level container in the Device > Endpoint > Feature model.
type Device struct {
	mu sync.RWMutex

	// DeviceID is the unique device identifier (from certificate SKI).
	deviceID string

	// VendorID identifies the device manufacturer (IANA PEN or registry-assigned).
	vendorID uint32

	// ProductID identifies the device product within the vendor.
	productID uint16

	// SerialNumber is the device serial number.
	serialNumber string

	// FirmwareVersion is the current firmware version.
	firmwareVersion string

	// Endpoints indexed by ID.
	endpoints map[uint8]*Endpoint
}

// NewDevice creates a new device with the given identity.
func NewDevice(deviceID string, vendorID uint32, productID uint16) *Device {
	d := &Device{
		deviceID:  deviceID,
		vendorID:  vendorID,
		productID: productID,
		endpoints: make(map[uint8]*Endpoint),
	}

	// Always create endpoint 0 (DEVICE_ROOT) with DeviceInfo
	root := NewEndpoint(0, EndpointDeviceRoot, "")
	d.endpoints[0] = root

	return d
}

// DeviceID returns the unique device identifier.
func (d *Device) DeviceID() string {
	return d.deviceID
}

// VendorID returns the vendor identifier.
func (d *Device) VendorID() uint32 {
	return d.vendorID
}

// ProductID returns the product identifier.
func (d *Device) ProductID() uint16 {
	return d.productID
}

// SerialNumber returns the device serial number.
func (d *Device) SerialNumber() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.serialNumber
}

// SetSerialNumber sets the device serial number.
func (d *Device) SetSerialNumber(sn string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serialNumber = sn
}

// FirmwareVersion returns the firmware version.
func (d *Device) FirmwareVersion() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.firmwareVersion
}

// SetFirmwareVersion sets the firmware version.
func (d *Device) SetFirmwareVersion(version string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.firmwareVersion = version
}

// RootEndpoint returns the device root endpoint (endpoint 0).
func (d *Device) RootEndpoint() *Endpoint {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.endpoints[0]
}

// AddEndpoint adds an endpoint to the device.
// Returns an error if an endpoint with the same ID already exists.
func (d *Device) AddEndpoint(endpoint *Endpoint) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.endpoints[endpoint.ID()]; exists {
		return ErrDuplicateEndpoint
	}

	d.endpoints[endpoint.ID()] = endpoint
	return nil
}

// GetEndpoint returns an endpoint by ID.
func (d *Device) GetEndpoint(id uint8) (*Endpoint, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	endpoint, exists := d.endpoints[id]
	if !exists {
		return nil, ErrEndpointNotFound
	}
	return endpoint, nil
}

// Endpoints returns all endpoints on this device.
func (d *Device) Endpoints() []*Endpoint {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*Endpoint, 0, len(d.endpoints))
	for _, ep := range d.endpoints {
		result = append(result, ep)
	}
	return result
}

// EndpointCount returns the number of endpoints.
func (d *Device) EndpointCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.endpoints)
}

// GetFeature returns a feature from a specific endpoint.
func (d *Device) GetFeature(endpointID uint8, featureType FeatureType) (*Feature, error) {
	endpoint, err := d.GetEndpoint(endpointID)
	if err != nil {
		return nil, err
	}
	return endpoint.GetFeature(featureType)
}

// ReadAttribute reads an attribute from a specific endpoint and feature.
func (d *Device) ReadAttribute(endpointID uint8, featureType FeatureType, attrID uint16) (any, error) {
	feature, err := d.GetFeature(endpointID, featureType)
	if err != nil {
		return nil, err
	}
	return feature.ReadAttribute(attrID)
}

// WriteAttribute writes an attribute to a specific endpoint and feature.
func (d *Device) WriteAttribute(endpointID uint8, featureType FeatureType, attrID uint16, value any) error {
	feature, err := d.GetFeature(endpointID, featureType)
	if err != nil {
		return err
	}
	return feature.WriteAttribute(attrID, value)
}

// InvokeCommand invokes a command on a specific endpoint and feature.
//
// params and the response map are integer-keyed per
// docs/interaction-model.md §7.1 / §7.2 (Issue #11 fix).
func (d *Device) InvokeCommand(ctx context.Context, endpointID uint8, featureType FeatureType, cmdID uint8, params map[uint8]any) (map[uint8]any, error) {
	feature, err := d.GetFeature(endpointID, featureType)
	if err != nil {
		return nil, err
	}
	return feature.InvokeCommand(ctx, cmdID, params)
}

// DeviceInfo returns a summary of the device for discovery.
type DeviceInfo struct {
	DeviceID        string          `cbor:"1,keyasint"`
	VendorID        uint32          `cbor:"2,keyasint"`
	ProductID       uint16          `cbor:"3,keyasint"`
	SerialNumber    string          `cbor:"4,keyasint,omitempty"`
	FirmwareVersion string          `cbor:"5,keyasint,omitempty"`
	Endpoints       []*EndpointInfo `cbor:"6,keyasint"`
}

// Info returns device information for discovery.
func (d *Device) Info() *DeviceInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	endpoints := make([]*EndpointInfo, 0, len(d.endpoints))
	for _, ep := range d.endpoints {
		endpoints = append(endpoints, ep.Info())
	}

	return &DeviceInfo{
		DeviceID:        d.deviceID,
		VendorID:        d.vendorID,
		ProductID:       d.productID,
		SerialNumber:    d.serialNumber,
		FirmwareVersion: d.firmwareVersion,
		Endpoints:       endpoints,
	}
}

// FindEndpointsByType returns all endpoints of a given type.
func (d *Device) FindEndpointsByType(endpointType EndpointType) []*Endpoint {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*Endpoint
	for _, ep := range d.endpoints {
		if ep.Type() == endpointType {
			result = append(result, ep)
		}
	}
	return result
}

// FindEndpointsWithFeature returns all endpoints that have a given feature.
func (d *Device) FindEndpointsWithFeature(featureType FeatureType) []*Endpoint {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*Endpoint
	for _, ep := range d.endpoints {
		if ep.HasFeature(featureType) {
			result = append(result, ep)
		}
	}
	return result
}
