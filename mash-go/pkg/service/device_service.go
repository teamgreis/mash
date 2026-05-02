package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/duration"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/persistence"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/subscription"
)

var (
	errCommissionTestZoneDisabled = errors.New("commissioning: test zone rejected (no valid enable-key)")
	errCommissionZoneTypeExists   = errors.New("commissioning: zone type already exists")
	errCommissionZoneSlotsFull    = errors.New("commissioning: zone slots full")
)

const defaultDisconnectReentryHoldoff = 3 * time.Second

// DeviceService orchestrates a MASH device.
type DeviceService struct {
	mu sync.RWMutex

	config DeviceConfig
	device *model.Device
	state  ServiceState

	// Device identity (derived from certificate fingerprint)
	deviceID string

	// Discovery management
	discoveryManager *discovery.DiscoveryManager
	advertiser       discovery.Advertiser
	browser          discovery.Browser

	// Pairing request browsing
	pairingRequestActive bool
	pairingRequestCancel context.CancelFunc

	// Single unified listener with ALPN-based routing (DEC-067).
	// Commissioning (mash-comm/1) and operational (mash/1) connections share
	// one port. GetConfigForClient routes based on the client's ALPN.
	listener             net.Listener
	operationalTLSConfig *tls.Config
	commissioningOpen    atomic.Bool     // true when commissioning window is open
	commissioningEpoch   atomic.Uint64   // incremented on each EnterCommissioningMode
	commissioningCert    tls.Certificate // Stable, generated once at startup
	tlsCert              tls.Certificate // Operational cert (from zone CA)

	// PASE commissioning
	verifier *commissioning.Verifier
	serverID []byte

	// Timer management - one failsafe timer per zone (legacy API; new
	// failsafe state machine uses failsafeCountdown below).
	failsafeTimers  map[string]*failsafe.Timer
	durationManager *duration.Manager

	// Device-wide failsafe state (F-32). FAILSAFE is entered when the last
	// authoritative zone disconnects and the countdown runs from FAILSAFE to
	// AUTONOMOUS per docs/testing/behavior/failsafe-timing.md §2.1.
	failsafeActive            bool
	failsafeCountdown         *time.Timer
	failsafeCountdownStartedAt time.Time

	// failsafeDurationOverride is set by the TriggerSetFailsafeDuration
	// test backdoor (F-36-C1). The countdown picks this up in preference
	// to the failsafeDuration attribute, which is range-validated to the
	// DEC-070 floor of 7200s and therefore cannot be set to test-friendly
	// short values via the normal attribute path. Zero = no override.
	failsafeDurationOverride time.Duration

	// Subscription management
	subscriptionManager dispatch.SubscriptionTracker

	// Connected zones
	connectedZones map[string]*ConnectedZone

	// Zone sessions for operational messaging
	zoneSessions map[string]*ZoneSession

	// Zone ID to index mapping (for duration timers which use uint8)
	zoneIndexMap  map[string]uint8
	nextZoneIndex uint8

	// Event handlers
	eventHandlers []EventHandler

	// autoReentryPending is set after commissioning completes in test mode.
	// The next handleOperationalConnection consumes it to re-enter
	// commissioning mode without a sleep-based delay.
	autoReentryPending bool

	// disconnectReentryHoldoff suppresses auto-reentry in HandleZoneDisconnect
	// for a short period after explicit commissioning exit.
	disconnectReentryHoldoff      time.Duration
	disconnectReentryBlockedUntil time.Time
	disconnectReentryTimer        *time.Timer

	// Logger for debug output (optional)
	logger *slog.Logger

	// Protocol logger for structured event capture (optional)
	protocolLogger log.Logger

	// LimitResolver (optional, set by CLI via SetLimitResolver)
	limitResolver *features.LimitResolver

	// Persistence (optional, set by CLI)
	certStore  cert.Store
	stateStore *persistence.DeviceStateStore

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Test clock offset for certificate validation (set via TriggerAdjustClockBase)
	clockOffset time.Duration

	// Security Hardening (DEC-047)
	// Connection tracking
	commissioningConnActive  bool       // Only one commissioning connection allowed
	commissioningGeneration  uint64     // Monotonic counter to prevent stale release
	lastCommissioningAttempt time.Time  // For connection cooldown
	connectionMu             sync.Mutex // Protects connection tracking fields

	// PASE attempt tracking
	paseTracker *PASEAttemptTracker

	// Transport-level connection cap (DEC-062)
	activeConns atomic.Int32

	// Stale connection reaper (DEC-064)
	connTracker *connTracker
}

// generateConnectionID generates a random connection ID for logging.
func generateConnectionID() string {
	b := make([]byte, 8) // 8 bytes = 16 hex chars
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewDeviceService creates a new device service.
func NewDeviceService(device *model.Device, config DeviceConfig) (*DeviceService, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	svc := &DeviceService{
		config:                   config,
		device:                   device,
		state:                    StateIdle,
		connectedZones:           make(map[string]*ConnectedZone),
		zoneSessions:             make(map[string]*ZoneSession),
		failsafeTimers:           make(map[string]*failsafe.Timer),
		zoneIndexMap:             make(map[string]uint8),
		connTracker:              newConnTracker(),
		certStore:                cert.NewMemoryStore(),
		logger:                   config.Logger,
		protocolLogger:           config.ProtocolLogger,
		disconnectReentryHoldoff: defaultDisconnectReentryHoldoff,
	}

	// Initialize duration manager with expiry callback
	svc.durationManager = duration.NewManager()
	svc.durationManager.OnExpiry(func(zoneIndex uint8, cmdType duration.CommandType, value any) {
		svc.handleDurationExpiry(zoneIndex, cmdType, value)
	})

	// Initialize subscription manager
	subConfig := subscription.DefaultConfig()
	svc.subscriptionManager = subscription.NewManagerWithConfig(subConfig)

	// Initialize PASE attempt tracker (DEC-047)
	// Backoff only triggers on failed attempts (wrong setup code), so it
	// does not slow down normal commissioning cycles that succeed immediately.
	if svc.config.PASEBackoffEnabled {
		svc.paseTracker = NewPASEAttemptTracker(svc.config.PASEBackoffTiers)
	}

	// Register service-level commands on DeviceInfo feature
	svc.registerDeviceCommands()

	// Subscribe to attribute changes on all features so that command handlers
	// (e.g., SetLimit) that modify attributes internally trigger EventValueChanged
	// events for the interactive display and other event listeners.
	svc.subscribeToFeatureChanges()

	return svc, nil
}

// Device returns the underlying device model.
func (s *DeviceService) Device() *model.Device {
	return s.device
}

// State returns the current service state.
func (s *DeviceService) State() ServiceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// isEnableKeyValid returns true if a valid enable-key is configured.
// A valid key is non-empty and not all zeros.
func (s *DeviceService) isEnableKeyValid() bool {
	key := s.config.TestEnableKey
	return key != "" && key != "00000000000000000000000000000000"
}

// OnEvent registers an event handler.
func (s *DeviceService) OnEvent(handler EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventHandlers = append(s.eventHandlers, handler)
}

// Addr returns the unified listener's address.
func (s *DeviceService) Addr() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// TLSAddr returns the listener's address.
func (s *DeviceService) TLSAddr() net.Addr {
	return s.Addr()
}

// OperationalAddr returns the listener's address (same port as commissioning).
func (s *DeviceService) OperationalAddr() net.Addr {
	return s.Addr()
}

// CommissioningAddr returns the listener's address (same port as operational).
func (s *DeviceService) CommissioningAddr() net.Addr {
	return s.Addr()
}

// ActiveConns returns the current number of active connections (DEC-062).
func (s *DeviceService) ActiveConns() int32 {
	return s.activeConns.Load()
}

// ServerIdentity returns the server identity used for PASE.
func (s *DeviceService) ServerIdentity() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverID
}

// emitEvent sends an event to all registered handlers.
func (s *DeviceService) emitEvent(event Event) {
	for _, handler := range s.eventHandlers {
		go handler(event)
	}
}

// debugLog logs a debug message if logging is enabled.
func (s *DeviceService) debugLog(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Debug(msg, args...)
	}
}

// SetLimitResolver sets the LimitResolver so that TriggerResetTestState and
// RemoveZone can clear resolver state (limits, timers) alongside attribute state.
func (s *DeviceService) SetLimitResolver(lr *features.LimitResolver) {
	s.limitResolver = lr
}

// parsePort extracts the port from a listen address (e.g., ":8443" -> 8443).
func parsePort(addr string) uint16 {
	// Handle formats: ":8443", "0.0.0.0:8443", "localhost:8443"
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			var port uint16
			for j := i + 1; j < len(addr); j++ {
				port = port*10 + uint16(addr[j]-'0')
			}
			return port
		}
	}
	return 8443 // Default port
}

// buildDeviceIdentity creates a DeviceIdentity from the device's information.
func (s *DeviceService) buildDeviceIdentity() *cert.DeviceIdentity {
	return &cert.DeviceIdentity{
		DeviceID:  s.deviceID,
		VendorID:  s.device.VendorID(),
		ProductID: s.device.ProductID(),
	}
}
