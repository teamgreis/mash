package service

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/log"
)

// Service errors.
var (
	ErrNotStarted             = errors.New("service not started")
	ErrAlreadyStarted         = errors.New("service already started")
	ErrCommissionFailed       = errors.New("commissioning failed")
	ErrPASEFailed             = fmt.Errorf("%w: PASE authentication failed", ErrCommissionFailed)
	ErrNotConnected           = errors.New("not connected")
	ErrDeviceNotFound         = errors.New("device not found")
	ErrZoneFull               = errors.New("maximum zones reached")
	ErrUnauthorized           = errors.New("unauthorized")
	ErrInvalidConfig          = errors.New("invalid configuration")
	ErrSessionClosed          = errors.New("session closed")
	ErrPairingRequestTimeout  = errors.New("pairing request timeout")
	ErrCommissioningCancelled = errors.New("commissioning cancelled")
	ErrNoPairingRequestActive = errors.New("no pairing request active for discriminator")
	ErrZoneIDRequired         = errors.New("zone ID required for pairing request")
)

// Pairing request timing constants.
const (
	// DefaultPairingRequestTimeout is the default timeout for pairing requests.
	// This is suitable for interactive commissioning scenarios.
	DefaultPairingRequestTimeout = 1 * time.Hour

	// SMGWPairingRequestTimeout is a longer timeout for SMGW/backend scenarios
	// where a device may be provisioned days before installation.
	SMGWPairingRequestTimeout = 7 * 24 * time.Hour

	// PairingRequestPollInterval is how often to poll for the device during pairing request.
	PairingRequestPollInterval = 5 * time.Second
)

// ServiceState represents the service state.
type ServiceState uint8

const (
	// StateIdle - service created but not started.
	StateIdle ServiceState = iota

	// StateStarting - service is starting up.
	StateStarting

	// StateRunning - service is running normally.
	StateRunning

	// StateStopping - service is shutting down.
	StateStopping

	// StateStopped - service has stopped.
	StateStopped
)

// String returns the state name.
func (s ServiceState) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateStarting:
		return "STARTING"
	case StateRunning:
		return "RUNNING"
	case StateStopping:
		return "STOPPING"
	case StateStopped:
		return "STOPPED"
	default:
		return "UNKNOWN"
	}
}

// DeviceConfig configures a DeviceService.
type DeviceConfig struct {
	// OperationalListenAddress is the address for the unified TLS listener.
	// Default: ":8443". A single listener handles both commissioning (mash-comm/1 ALPN)
	// and operational (mash/1 ALPN) connections via GetConfigForClient routing.
	OperationalListenAddress string

	// ListenAddress is deprecated. If set and OperationalListenAddress is empty,
	// it is used as OperationalListenAddress for backward compatibility.
	ListenAddress string

	// TLSConfig provides TLS configuration for the server.
	// If nil, the service will generate a self-signed certificate.
	TLSConfig *tls.Config

	// Discriminator identifies this device for commissioning (0-4095).
	Discriminator uint16

	// SetupCode is the 8-digit setup code for SPAKE2+.
	SetupCode string

	// Categories lists device categories for mDNS discovery.
	Categories []discovery.DeviceCategory

	// SerialNumber is the device serial number.
	SerialNumber string

	// Brand is the vendor/brand name.
	Brand string

	// Model is the model name.
	Model string

	// DeviceName is an optional user-configurable name.
	DeviceName string

	// MaxZones is the maximum number of zones a device can belong to.
	// Default is 2: one GRID zone (SMGW) and one LOCAL zone (EMS/home app).
	MaxZones int

	// FailsafeTimeout is the default failsafe duration.
	FailsafeTimeout time.Duration

	// ReconnectBackoff configures reconnection timing.
	ReconnectBackoff BackoffConfig

	// HeartbeatInterval is the keep-alive interval.
	HeartbeatInterval time.Duration

	// CommissioningWindowDuration is how long commissioning mode stays open.
	CommissioningWindowDuration time.Duration

	// ListenForPairingRequests enables automatic response to pairing requests.
	// When true, the device will browse for _mashp._udp and open commissioning
	// window when a matching request is found.
	ListenForPairingRequests bool

	// EnableAutoReconnect enables automatic reconnection to zones.
	EnableAutoReconnect bool

	// Security Hardening (DEC-047)

	// TLSHandshakeTimeout is the maximum time allowed for the TLS handshake to
	// complete after a TCP connection is accepted. Connections that do not send
	// a TLS ClientHello within this time are closed, freeing the connection cap
	// slot. Default: 10s. Set to 0 to disable (not recommended).
	TLSHandshakeTimeout time.Duration

	// ConnectionCooldown is the minimum time between commissioning connection attempts.
	// Default: 500ms. Set to 0 to disable cooldown.
	ConnectionCooldown time.Duration

	// PASEFirstMessageTimeout is the timeout for receiving the first PASE message
	// after TLS handshake completes. No commissioning lock is held during this wait.
	// Default: 5s. (DEC-061)
	PASEFirstMessageTimeout time.Duration

	// HandshakeTimeout is the overall timeout for commissioning handshake.
	// Default: 85s. This is an absolute limit across all phases (starting from PASERequest).
	HandshakeTimeout time.Duration

	// PASEPhaseTimeout is the per-phase timeout for individual PASE read operations.
	// For example, after sending PASEResponse, the device waits at most this long
	// for PASEConfirm before closing the connection. Default: 30s (PASE_WAIT_V).
	PASEPhaseTimeout time.Duration

	// PASEBackoffEnabled enables exponential backoff on failed PASE attempts.
	// Default: true.
	PASEBackoffEnabled bool

	// PASEBackoffTiers configures the backoff delays for failed attempts.
	// Index 0 = delay for attempts 1-3, Index 1 = 4-6, Index 2 = 7-10, Index 3 = 11+.
	// Default: [0ms, 1000ms, 3000ms, 10000ms].
	PASEBackoffTiers [4]time.Duration

	// GenericErrors enables generic error responses to prevent information leakage.
	// Default: true. When enabled, all authentication failures return AUTH_FAILED.
	GenericErrors bool

	// ErrorDelayMin is the minimum random delay added to error responses (milliseconds).
	// Default: 100ms.
	ErrorDelayMin time.Duration

	// ErrorDelayMax is the maximum random delay added to error responses (milliseconds).
	// Default: 500ms.
	ErrorDelayMax time.Duration

	// Stale Connection Reaper (DEC-064)

	// StaleConnectionTimeout is the maximum age for pre-operational connections.
	// Connections that have not completed commissioning within this time are
	// force-closed by the reaper. Default: 90s. Set to 0 to disable.
	StaleConnectionTimeout time.Duration

	// ReaperInterval is how often the stale connection reaper runs.
	// Default: 10s.
	ReaperInterval time.Duration

	// TestMode disables security hardening for test harness usage.
	// When enabled: PASE backoff tracking is skipped, the commissioning window
	// is extended to 24h (if at the default 15m), connection cooldown is skipped,
	// and the commissioning window auto-reopens after each successful commission.
	TestMode bool

	// TestEnableKey is the hex-encoded 128-bit key (32 hex chars) required by
	// the triggerTestEvent command. Only used when TestMode is true.
	TestEnableKey string

	// SnapshotPolicy controls when capability snapshots are emitted to the protocol log.
	SnapshotPolicy SnapshotPolicy

	// Logger is the optional logger for debug output.
	// If nil, logging is disabled.
	Logger *slog.Logger

	// ProtocolLogger receives structured protocol events for debugging.
	// Set to nil to disable protocol logging.
	ProtocolLogger log.Logger
}

// ControllerConfig configures a ControllerService.
type ControllerConfig struct {
	// TLSConfig provides TLS configuration for the client.
	TLSConfig *tls.Config

	// ZoneType is the type of zone this controller creates.
	ZoneType cert.ZoneType

	// ZoneName is the user-friendly zone name.
	ZoneName string

	// VendorProduct is optional vendor:product ID.
	VendorProduct string

	// ControllerName is optional controller name.
	ControllerName string

	// DiscoveryTimeout is the timeout for mDNS discovery.
	DiscoveryTimeout time.Duration

	// ConnectionTimeout is the timeout for connecting to devices.
	ConnectionTimeout time.Duration

	// ReconnectBackoff configures reconnection timing.
	ReconnectBackoff BackoffConfig

	// HeartbeatInterval is the keep-alive interval.
	HeartbeatInterval time.Duration

	// SubscriptionMinInterval is the minimum subscription notification interval.
	SubscriptionMinInterval time.Duration

	// SubscriptionMaxInterval is the maximum subscription notification interval.
	SubscriptionMaxInterval time.Duration

	// EnableAutoReconnect enables automatic reconnection to devices.
	EnableAutoReconnect bool

	// EnableBounceBackSuppression enables subscription bounce-back suppression.
	EnableBounceBackSuppression bool

	// PairingRequestTimeout is how long to wait for a device to respond to a pairing request.
	// Default: 1 hour. Set longer (e.g., 7 days) for SMGW scenarios.
	PairingRequestTimeout time.Duration

	// PairingRequestPollInterval is how often to poll for the device during pairing request.
	// Default: 5 seconds.
	PairingRequestPollInterval time.Duration

	// SnapshotPolicy controls when capability snapshots are emitted to the protocol log.
	SnapshotPolicy SnapshotPolicy

	// Logger is the optional logger for debug output.
	// If nil, logging is disabled.
	Logger *slog.Logger

	// ProtocolLogger receives structured protocol events for debugging.
	// Set to nil to disable protocol logging.
	ProtocolLogger log.Logger
}

// SnapshotPolicy controls when capability snapshots are emitted to the protocol log.
type SnapshotPolicy struct {
	// MaxInterval is the maximum time between snapshots. The time trigger
	// only fires if at least MinMessages have been logged since the last
	// snapshot. Zero disables time-based triggering.
	MaxInterval time.Duration

	// MaxMessages is the maximum number of protocol messages logged for a
	// connection before a snapshot is emitted. Zero disables count-based triggering.
	MaxMessages int

	// MinMessages is the minimum number of messages that must have been
	// logged since the last snapshot for the time-based trigger to fire.
	// This prevents snapshots from dominating the log on near-idle connections.
	// The message-count trigger (MaxMessages) is not affected by this floor.
	// Zero means the time trigger fires unconditionally.
	MinMessages int
}

// DefaultSnapshotPolicy returns a SnapshotPolicy with sensible defaults.
func DefaultSnapshotPolicy() SnapshotPolicy {
	return SnapshotPolicy{
		MaxInterval: 30 * time.Minute,
		MaxMessages: 1000,
		MinMessages: 50,
	}
}

// BackoffConfig configures exponential backoff for reconnection.
type BackoffConfig struct {
	// InitialInterval is the first retry delay.
	InitialInterval time.Duration

	// MaxInterval is the maximum retry delay.
	MaxInterval time.Duration

	// Multiplier is the backoff multiplier (e.g., 2.0 for doubling).
	Multiplier float64

	// MaxRetries is the maximum number of retries (0 = unlimited).
	MaxRetries int
}

// DefaultDeviceConfig returns a DeviceConfig with sensible defaults.
func DefaultDeviceConfig() DeviceConfig {
	return DeviceConfig{
		OperationalListenAddress:    ":8443",
		MaxZones:                    2, // 1 GRID + 1 LOCAL
		FailsafeTimeout:             2 * time.Hour,
		HeartbeatInterval:           30 * time.Second,
		CommissioningWindowDuration: 15 * time.Minute, // DEC-048: Aligned with Matter
		EnableAutoReconnect:         true,
		ReconnectBackoff: BackoffConfig{
			InitialInterval: 1 * time.Second,
			MaxInterval:     5 * time.Minute,
			Multiplier:      2.0,
			MaxRetries:      0, // Unlimited
		},
		// Security Hardening (DEC-047, DEC-061)
		TLSHandshakeTimeout:    10 * time.Second,
		ConnectionCooldown:      500 * time.Millisecond,
		PASEFirstMessageTimeout: 5 * time.Second,
		HandshakeTimeout:        85 * time.Second,
		PASEPhaseTimeout:        30 * time.Second,
		PASEBackoffEnabled: true,
		PASEBackoffTiers: [4]time.Duration{
			0,                        // Tier 1: attempts 1-3, no delay
			1000 * time.Millisecond,  // Tier 2: attempts 4-6, 1s delay
			3000 * time.Millisecond,  // Tier 3: attempts 7-10, 3s delay
			10000 * time.Millisecond, // Tier 4: attempts 11+, 10s delay
		},
		// Stale Connection Reaper (DEC-064)
		StaleConnectionTimeout: 90 * time.Second,
		ReaperInterval:         10 * time.Second,
		GenericErrors:          true,
		ErrorDelayMin:  100 * time.Millisecond,
		ErrorDelayMax:  500 * time.Millisecond,
		SnapshotPolicy: DefaultSnapshotPolicy(),
	}
}

// DefaultControllerConfig returns a ControllerConfig with sensible defaults.
func DefaultControllerConfig() ControllerConfig {
	return ControllerConfig{
		ZoneType:                    cert.ZoneTypeLocal,
		DiscoveryTimeout:            10 * time.Second,
		ConnectionTimeout:           30 * time.Second,
		HeartbeatInterval:           30 * time.Second,
		SubscriptionMinInterval:     1 * time.Second,
		SubscriptionMaxInterval:     60 * time.Second,
		EnableAutoReconnect:         true,
		EnableBounceBackSuppression: true,
		ReconnectBackoff: BackoffConfig{
			InitialInterval: 1 * time.Second,
			MaxInterval:     5 * time.Minute,
			Multiplier:      2.0,
			MaxRetries:      0,
		},
		SnapshotPolicy: DefaultSnapshotPolicy(),
	}
}

// Validate checks if the device config is valid.
func (c *DeviceConfig) Validate() error {
	if c.Discriminator > discovery.MaxDiscriminator {
		return ErrInvalidConfig
	}
	if len(c.SetupCode) != discovery.SetupCodeLength {
		return ErrInvalidConfig
	}
	sc, err := commissioning.ParseSetupCode(c.SetupCode)
	if err != nil {
		return ErrInvalidConfig
	}
	if err := sc.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if c.SerialNumber == "" || c.Brand == "" || c.Model == "" {
		return ErrInvalidConfig
	}
	if len(c.Categories) == 0 {
		return ErrInvalidConfig
	}
	return nil
}

// Validate checks if the controller config is valid.
func (c *ControllerConfig) Validate() error {
	if c.ZoneName == "" {
		return ErrInvalidConfig
	}
	if c.ZoneType < cert.ZoneTypeGrid || c.ZoneType > cert.ZoneTypeTest {
		return ErrInvalidConfig
	}
	return nil
}

// ConnectedDevice represents a device connected to a controller.
type ConnectedDevice struct {
	// ID is the device ID (fingerprint-derived).
	ID string

	// ZoneID is the zone ID this device belongs to.
	ZoneID string

	// Host is the device hostname.
	Host string

	// Port is the device port.
	Port uint16

	// Addresses contains resolved IP addresses.
	Addresses []string

	// DeviceType is the device type string (e.g., "EVSE", "INVERTER").
	DeviceType string

	// VendorProduct is the vendor:product ID if available.
	VendorProduct string

	// Firmware is the firmware version if available.
	Firmware string

	// FeatureMap is the device's feature map.
	FeatureMap uint32

	// EndpointCount is the number of endpoints.
	EndpointCount uint8

	// Connected indicates if currently connected.
	Connected bool

	// LastSeen is when the device was last seen.
	LastSeen time.Time

	// OperationalCert is the device's operational certificate received during commissioning.
	// This is the certificate signed by the Zone CA, with device ID in CommonName.
	OperationalCert *x509.Certificate
}

// ConnectedZone represents a zone connected to a device.
type ConnectedZone struct {
	// ID is the zone ID (fingerprint-derived).
	ID string

	// Type is the zone type.
	Type cert.ZoneType

	// Priority is the zone's priority (derived from type).
	Priority uint8

	// Connected indicates if currently connected.
	Connected bool

	// LastSeen is when the zone was last seen.
	LastSeen time.Time

	// FailsafeActive indicates if failsafe is active for this zone.
	FailsafeActive bool
}

// Event types for service callbacks.
type EventType uint8

const (
	// EventConnected - connection established.
	EventConnected EventType = iota

	// EventDisconnected - connection lost.
	EventDisconnected

	// EventCommissioned - zone added.
	EventCommissioned

	// EventDecommissioned - zone removed.
	EventDecommissioned

	// EventValueChanged - attribute value changed.
	EventValueChanged

	// EventFailsafeStarted - failsafe timer started.
	EventFailsafeStarted

	// EventFailsafeTriggered - failsafe triggered (connection timeout).
	EventFailsafeTriggered

	// EventFailsafeCleared - failsafe cleared.
	EventFailsafeCleared

	// EventCommissioningOpened - commissioning window opened.
	EventCommissioningOpened

	// EventCommissioningClosed - commissioning window closed.
	EventCommissioningClosed

	// EventDeviceDiscovered - new device discovered via mDNS.
	EventDeviceDiscovered

	// EventDeviceGone - device disappeared from mDNS commissioning.
	EventDeviceGone

	// EventZoneRemoved - zone was removed from device.
	EventZoneRemoved

	// EventZoneRestored - zone was restored from persistence (awaiting reconnection).
	EventZoneRestored

	// EventDeviceRediscovered - known device found via operational mDNS.
	EventDeviceRediscovered

	// EventDeviceReconnected - reconnected to a known device.
	EventDeviceReconnected

	// EventReconnectionFailed - reconnection attempt failed.
	EventReconnectionFailed

	// EventDeviceRemoved - device was removed from the zone (controller initiated).
	EventDeviceRemoved

	// EventCertificateRenewed - device certificate was renewed.
	EventCertificateRenewed

	// EventControllerCertRenewed - controller's own certificate was renewed.
	EventControllerCertRenewed

	// EventCommandInvoked - a command was invoked on the device.
	EventCommandInvoked

	// EventError - an error occurred during background operations.
	EventError
)

// String returns the event type name.
func (e EventType) String() string {
	switch e {
	case EventConnected:
		return "CONNECTED"
	case EventDisconnected:
		return "DISCONNECTED"
	case EventCommissioned:
		return "COMMISSIONED"
	case EventDecommissioned:
		return "DECOMMISSIONED"
	case EventValueChanged:
		return "VALUE_CHANGED"
	case EventFailsafeStarted:
		return "FAILSAFE_STARTED"
	case EventFailsafeTriggered:
		return "FAILSAFE_TRIGGERED"
	case EventFailsafeCleared:
		return "FAILSAFE_CLEARED"
	case EventCommissioningOpened:
		return "COMMISSIONING_OPENED"
	case EventCommissioningClosed:
		return "COMMISSIONING_CLOSED"
	case EventDeviceDiscovered:
		return "DEVICE_DISCOVERED"
	case EventDeviceGone:
		return "DEVICE_GONE"
	case EventZoneRemoved:
		return "ZONE_REMOVED"
	case EventZoneRestored:
		return "ZONE_RESTORED"
	case EventDeviceRediscovered:
		return "DEVICE_REDISCOVERED"
	case EventDeviceReconnected:
		return "DEVICE_RECONNECTED"
	case EventReconnectionFailed:
		return "RECONNECTION_FAILED"
	case EventDeviceRemoved:
		return "DEVICE_REMOVED"
	case EventCertificateRenewed:
		return "CERTIFICATE_RENEWED"
	case EventControllerCertRenewed:
		return "CONTROLLER_CERT_RENEWED"
	case EventCommandInvoked:
		return "COMMAND_INVOKED"
	case EventError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Event represents a service event.
type Event struct {
	// Type is the event type.
	Type EventType

	// ZoneID is the zone ID (for zone-related events).
	ZoneID string

	// DeviceID is the device ID (for device-related events).
	DeviceID string

	// EndpointID is the endpoint ID (for value change events).
	EndpointID uint8

	// FeatureID is the feature ID (for value change events).
	FeatureID uint16

	// AttributeID is the attribute ID (for value change events).
	AttributeID uint16

	// Value is the new value (for value change events).
	Value any

	// CommandID is the command that was invoked (for command events).
	CommandID uint8

	// CommandParams are the parameters passed to the command (for command events).
	// Integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
	CommandParams map[uint8]any

	// DiscoveredService contains the discovered service info (for discovery events).
	DiscoveredService any

	// Reason provides additional context for certain events (e.g., why commissioning closed).
	Reason string

	// Error is set if the event is an error.
	Error error
}

// EventHandler handles service events.
type EventHandler func(Event)
