package discovery

import (
	"errors"
	"time"
)

// Service type constants for mDNS.
const (
	// ServiceTypeCommissionable is the service type for devices in commissioning mode (DEC-067).
	ServiceTypeCommissionable = "_mash-comm._tcp"

	// ServiceTypeOperational is the service type for commissioned devices.
	ServiceTypeOperational = "_mash._tcp"

	// ServiceTypePairingRequest is the service type for pairing requests.
	// Controllers announce this to signal a specific device to open its commissioning window.
	ServiceTypePairingRequest = "_mashp._udp"

	// Domain is the mDNS domain.
	Domain = "local"

	// DefaultPort is the default MASH port (commissioning and operational share the same port).
	DefaultPort = 8443
)

// TXT record key constants.
const (
	// Commissionable TXT keys
	TXTKeyDiscriminator = "D"      // Discriminator (0-4095)
	TXTKeyCategories    = "cat"    // Device categories (comma-separated)
	TXTKeySerial        = "serial" // Serial number
	TXTKeyBrand         = "brand"  // Vendor/brand name
	TXTKeyModel         = "model"  // Model name
	TXTKeyDeviceName    = "DN"     // Device name (optional, user-configurable)

	// Operational TXT keys
	TXTKeyZoneID     = "ZI" // Zone ID (first 64 bits of SHA-256)
	TXTKeyDeviceID   = "DI" // Device ID (first 64 bits of SHA-256)
	TXTKeyVendorProd = "VP" // Vendor:Product ID (optional)
	TXTKeyFirmware   = "FW" // Firmware version (optional)
	TXTKeyEndpoints  = "EP" // Endpoint count (optional)

	// Pairing request TXT keys (also uses TXTKeyDiscriminator and TXTKeyZoneID)
	TXTKeyZoneName = "ZN" // Zone name (optional, user-friendly)
)

// Timing constants.
const (
	// CommissioningWindowDuration is how long commissioning mode stays open.
	// Default is 15 minutes, aligned with Matter specification 5.4.2.3.1.
	// Pairing requests (_mashp._udp) can re-trigger the window if it expires.
	// See DEC-048 for rationale.
	CommissioningWindowDuration = 15 * time.Minute

	// MinCommissioningWindowDuration is the minimum configurable window duration.
	// Aligned with Matter specification 5.4.2.3.1 (3 minutes minimum).
	MinCommissioningWindowDuration = 3 * time.Minute

	// MaxCommissioningWindowDuration is the maximum configurable window duration.
	// MASH allows up to 3 hours for professional installer scenarios.
	// Matter uses 15 min max, but MASH's pairing request mechanism allows re-triggering.
	// See DEC-048 for rationale.
	MaxCommissioningWindowDuration = 3 * time.Hour

	// BrowseTimeout is the default timeout for mDNS browsing.
	BrowseTimeout = 10 * time.Second

	// MDNSUpdateDelay is the maximum delay for mDNS updates.
	MDNSUpdateDelay = 1 * time.Second

	// PairingRequestTTL is how often controllers should re-announce pairing requests.
	PairingRequestTTL = 2 * time.Minute
)

// Limits.
const (
	// MaxInstanceNameLen is the DNS label limit.
	MaxInstanceNameLen = 63

	// MaxTXTRecordSize is the maximum total TXT record size.
	MaxTXTRecordSize = 400

	// MaxDiscriminator is the maximum discriminator value (12 bits).
	MaxDiscriminator = 4095

	// IDLength is the length of zone ID and device ID (16 hex chars = 64 bits).
	IDLength = 16
)

// QR code constants.
const (
	// QRPrefix is the prefix for MASH QR codes.
	QRPrefix = "MASH:"

	// QRVersion is the current QR code version.
	QRVersion = 1

	// SetupCodeLength is the required length of setup codes.
	SetupCodeLength = 8
)

// Discovery errors.
var (
	ErrInvalidQRCode        = errors.New("invalid QR code format")
	ErrInvalidPrefix        = errors.New("invalid QR code prefix")
	ErrInvalidVersion       = errors.New("invalid protocol version")
	ErrInvalidDiscriminator = errors.New("discriminator out of range")
	ErrInvalidSetupCode     = errors.New("invalid setup code format")
	ErrInvalidFieldCount    = errors.New("invalid field count in QR code")
	ErrInvalidTXTRecord     = errors.New("invalid TXT record format")
	ErrMissingRequired      = errors.New("missing required field")
	ErrInstanceNameTooLong  = errors.New("instance name exceeds 63 characters")
	ErrNotFound             = errors.New("service not found")
	ErrBrowseTimeout        = errors.New("browse timeout")
	ErrAlreadyExists        = errors.New("service already exists")
)

// DeviceCategory represents a device category.
type DeviceCategory uint8

const (
	// CategoryGCPH is Grid Connection Point Hub.
	CategoryGCPH DeviceCategory = 1

	// CategoryEMS is Energy Management System.
	CategoryEMS DeviceCategory = 2

	// CategoryEMobility is E-mobility (EVSE, wallbox).
	CategoryEMobility DeviceCategory = 3

	// CategoryHVAC is HVAC (heat pump, AC).
	CategoryHVAC DeviceCategory = 4

	// CategoryInverter is Inverter (PV, battery, hybrid).
	CategoryInverter DeviceCategory = 5

	// CategoryAppliance is Domestic appliance.
	CategoryAppliance DeviceCategory = 6

	// CategoryMetering is Smart meter, sub-meter.
	CategoryMetering DeviceCategory = 7
)

// String returns the category name.
func (c DeviceCategory) String() string {
	switch c {
	case CategoryGCPH:
		return "GCPH"
	case CategoryEMS:
		return "EMS"
	case CategoryEMobility:
		return "E-MOBILITY"
	case CategoryHVAC:
		return "HVAC"
	case CategoryInverter:
		return "INVERTER"
	case CategoryAppliance:
		return "APPLIANCE"
	case CategoryMetering:
		return "METERING"
	default:
		return "UNKNOWN"
	}
}

// DiscoveryState represents the device's discovery state.
type DiscoveryState uint8

const (
	// StateUnregistered - device is powered off or not advertising.
	StateUnregistered DiscoveryState = iota

	// StateUncommissioned - device has no zones, not in commissioning mode.
	StateUncommissioned

	// StateCommissioningOpen - commissioning window is open (_mash-comm._tcp advertised).
	StateCommissioningOpen

	// StateOperational - device has zones (_mash._tcp advertised per zone).
	StateOperational

	// StateOperationalCommissioning - operational but also in commissioning mode.
	StateOperationalCommissioning
)

// String returns the state name.
func (s DiscoveryState) String() string {
	switch s {
	case StateUnregistered:
		return "UNREGISTERED"
	case StateUncommissioned:
		return "UNCOMMISSIONED"
	case StateCommissioningOpen:
		return "COMMISSIONING_OPEN"
	case StateOperational:
		return "OPERATIONAL"
	case StateOperationalCommissioning:
		return "OPERATIONAL_COMMISSIONING"
	default:
		return "UNKNOWN"
	}
}

// QRCode represents parsed QR code data.
type QRCode struct {
	// Version is the protocol version (1-255).
	Version uint8

	// Discriminator identifies the device (0-4095).
	Discriminator uint16

	// SetupCode is the 8-digit setup code for SPAKE2+.
	// Stored as string to preserve leading zeros.
	SetupCode string
}

// CommissionableService represents a commissionable device found via mDNS.
type CommissionableService struct {
	// InstanceName is the mDNS instance name (e.g., "MASH-1234").
	InstanceName string

	// Host is the hostname (e.g., "evse-001.local").
	Host string

	// Port is the service port.
	Port uint16

	// Addresses contains resolved IP addresses.
	Addresses []string

	// Discriminator is the device discriminator (from TXT "D").
	Discriminator uint16

	// Categories contains device categories (from TXT "cat").
	Categories []DeviceCategory

	// Serial is the serial number (from TXT "serial").
	Serial string

	// Brand is the vendor/brand name (from TXT "brand").
	Brand string

	// Model is the model name (from TXT "model").
	Model string

	// DeviceName is the optional user-configurable name (from TXT "DN").
	DeviceName string
}

// OperationalService represents a commissioned device found via mDNS.
type OperationalService struct {
	// InstanceName is the mDNS instance name (e.g., "A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382").
	InstanceName string

	// Host is the hostname.
	Host string

	// Port is the service port.
	Port uint16

	// Addresses contains resolved IP addresses.
	Addresses []string

	// ZoneID is the zone ID (from TXT "ZI", 16 hex chars).
	ZoneID string

	// DeviceID is the device ID (from TXT "DI", 16 hex chars).
	DeviceID string

	// VendorProduct is optional vendor:product ID (from TXT "VP").
	VendorProduct string

	// Firmware is optional firmware version (from TXT "FW").
	Firmware string

	// EndpointCount is optional endpoint count (from TXT "EP").
	EndpointCount uint8
}

// CommissionableInfo contains information for advertising a commissionable device.
type CommissionableInfo struct {
	// Discriminator identifies this device (0-4095).
	Discriminator uint16

	// Categories lists device categories.
	Categories []DeviceCategory

	// Serial is the device serial number.
	Serial string

	// Brand is the vendor/brand name.
	Brand string

	// Model is the model name.
	Model string

	// DeviceName is an optional user-configurable name.
	DeviceName string

	// Port is the service port.
	Port uint16

	// Host is the hostname to advertise.
	Host string
}

// OperationalInfo contains information for advertising an operational device.
type OperationalInfo struct {
	// ZoneID is the zone ID (16 hex chars from certificate fingerprint).
	ZoneID string

	// DeviceID is the device ID (16 hex chars from certificate fingerprint).
	DeviceID string

	// VendorProduct is optional vendor:product ID.
	VendorProduct string

	// Firmware is optional firmware version.
	Firmware string

	// EndpointCount is optional endpoint count.
	EndpointCount uint8

	// Port is the service port.
	Port uint16

	// Host is the hostname to advertise.
	Host string
}

// PairingRequestInfo contains information for announcing a pairing request.
// Controllers advertise this to signal a specific device to open its commissioning window.
type PairingRequestInfo struct {
	// Discriminator identifies the target device (0-4095).
	Discriminator uint16

	// ZoneID is the requesting zone's ID (16 hex chars).
	ZoneID string

	// ZoneName is an optional user-friendly zone name.
	ZoneName string

	// Host is the hostname to advertise.
	Host string
}

// Validate checks if the PairingRequestInfo is valid.
func (p *PairingRequestInfo) Validate() error {
	if p.Discriminator > MaxDiscriminator {
		return ErrInvalidDiscriminator
	}
	if len(p.ZoneID) != IDLength {
		return ErrMissingRequired
	}
	if !isHexString(p.ZoneID) {
		return ErrInvalidTXTRecord
	}
	if p.Host == "" {
		return ErrMissingRequired
	}
	return nil
}

// PairingRequestService represents a pairing request found via mDNS.
type PairingRequestService struct {
	// InstanceName is the mDNS instance name (e.g., "A1B2C3D4E5F6A7B8-1234").
	InstanceName string

	// Host is the hostname.
	Host string

	// Port is the service port (always 0 for signaling only).
	Port uint16

	// Addresses contains resolved IP addresses.
	Addresses []string

	// Discriminator is the target device discriminator (from TXT "D").
	Discriminator uint16

	// ZoneID is the requesting zone's ID (from TXT "ZI", 16 hex chars).
	ZoneID string

	// ZoneName is the optional user-friendly zone name (from TXT "ZN").
	ZoneName string
}
