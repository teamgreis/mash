package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/enbility/zeroconf/v3/api"
)

// Advertiser provides mDNS service advertising capabilities.
type Advertiser interface {
	// AdvertiseCommissionable starts advertising a commissionable service.
	// The service will be advertised until StopCommissionable is called or
	// the commissioning window expires.
	AdvertiseCommissionable(ctx context.Context, info *CommissionableInfo) error

	// StopCommissionable stops advertising the commissionable service.
	StopCommissionable() error

	// AdvertiseOperational starts advertising an operational service for a zone.
	// Multiple zones can be advertised simultaneously.
	AdvertiseOperational(ctx context.Context, info *OperationalInfo) error

	// UpdateOperational updates TXT records for an operational service.
	UpdateOperational(zoneID string, info *OperationalInfo) error

	// StopOperational stops advertising operational service for a specific zone.
	StopOperational(zoneID string) error

	// AnnouncePairingRequest starts advertising a pairing request.
	// Controllers use this to signal devices that they want to commission them.
	// The discriminator identifies the target device.
	// Port is always 0 for pairing requests (signaling only, no actual connection).
	AnnouncePairingRequest(ctx context.Context, info *PairingRequestInfo) error

	// StopPairingRequest stops advertising a pairing request for a discriminator.
	StopPairingRequest(discriminator uint16) error

	// StopAll stops all advertisements.
	StopAll()
}

// AdvertiserConfig configures advertiser behavior.
type AdvertiserConfig struct {
	// Interface specifies which network interface to use.
	// Empty string means all interfaces.
	Interface string

	// TTL is the DNS record TTL.
	// Default: 120 seconds.
	TTL time.Duration

	// Quiet suppresses all mDNS network operations. When true, the
	// advertiser methods return nil without sending any multicast
	// traffic. The DiscoveryManager still tracks state correctly
	// (commissioning mode, zones), so IsCommissioningMode() and other
	// state queries work as expected. Use this in test mode where the
	// test harness connects directly by address.
	Quiet bool

	// ConnectionFactory creates multicast connections.
	// If nil, uses the default zeroconf connection factory.
	// Set this in tests to inject mock connections.
	ConnectionFactory api.ConnectionFactory

	// InterfaceProvider lists network interfaces.
	// If nil, uses the default zeroconf interface provider.
	// Set this in tests to inject mock interface lists.
	InterfaceProvider api.InterfaceProvider
}

// DefaultAdvertiserConfig returns the default advertiser configuration.
func DefaultAdvertiserConfig() AdvertiserConfig {
	return AdvertiserConfig{
		Interface: "",
		TTL:       120 * time.Second,
	}
}

// DiscoveryManager manages the device's discovery state machine.
type DiscoveryManager struct {
	mu sync.RWMutex

	state      DiscoveryState
	advertiser Advertiser

	// Commissionable info (used when in commissioning mode)
	commissionableInfo *CommissionableInfo

	// Operational zones
	operationalZones map[string]*OperationalInfo

	// Pairing requests (for zone controllers)
	// Keyed by discriminator to support multiple concurrent requests
	pairingRequests map[uint16]*PairingRequestInfo

	// Commissioning window timer
	commissioningTimer *time.Timer

	// Commissioning window duration (defaults to CommissioningWindowDuration constant)
	commissioningWindowDuration time.Duration

	// Callback for state changes
	onStateChange func(old, new DiscoveryState)

	// Callback for commissioning timeout (called when window expires)
	onCommissioningTimeout func()
}

// NewDiscoveryManager creates a new discovery manager.
func NewDiscoveryManager(advertiser Advertiser) *DiscoveryManager {
	return &DiscoveryManager{
		state:            StateUnregistered,
		advertiser:       advertiser,
		operationalZones: make(map[string]*OperationalInfo),
		pairingRequests:  make(map[uint16]*PairingRequestInfo),
	}
}

// State returns the current discovery state.
func (m *DiscoveryManager) State() DiscoveryState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// IsCommissioningMode returns true if the device is in commissioning mode.
func (m *DiscoveryManager) IsCommissioningMode() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateCommissioningOpen || m.state == StateOperationalCommissioning
}

// OnStateChange sets a callback for state changes.
func (m *DiscoveryManager) OnStateChange(fn func(old, new DiscoveryState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// OnCommissioningTimeout sets a callback that fires when the commissioning window expires.
// This is useful for notifying users that commissioning mode has ended due to timeout.
func (m *DiscoveryManager) OnCommissioningTimeout(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCommissioningTimeout = fn
}

// SetCommissioningWindowDuration sets the duration of the commissioning window.
// If not set or set to 0, defaults to CommissioningWindowDuration constant (3 hours).
// This must be called before EnterCommissioningMode for the setting to take effect.
func (m *DiscoveryManager) SetCommissioningWindowDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissioningWindowDuration = d
	// Reset active timer with new duration so tests that change the window
	// duration while commissioning is active see the updated timeout.
	if (m.state == StateCommissioningOpen || m.state == StateOperationalCommissioning) &&
		m.commissioningTimer != nil {
		m.commissioningTimer.Stop()
		m.commissioningTimer = time.AfterFunc(d, func() {
			m.exitCommissioningModeInternal(false)
		})
	}
}

// CommissioningWindowDuration returns the current commissioning window duration.
func (m *DiscoveryManager) CommissioningWindowDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.commissioningWindowDuration
}

// SetCommissionableInfo sets the device's commissionable information.
// This should be called before entering commissioning mode.
func (m *DiscoveryManager) SetCommissionableInfo(info *CommissionableInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionableInfo = info
}

// EnterCommissioningMode starts advertising the commissionable service.
// The service will be automatically stopped after the commissioning window expires.
// This method is idempotent: if already in commissioning mode, it resets the
// commissioning window timer without re-registering the mDNS service.
func (m *DiscoveryManager) EnterCommissioningMode(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.commissionableInfo == nil {
		return ErrMissingRequired
	}

	// Set up commissioning window timer
	// Use configured duration or default to CommissioningWindowDuration
	windowDuration := m.commissioningWindowDuration
	if windowDuration <= 0 {
		windowDuration = CommissioningWindowDuration
	}

	// If already advertising commissionable, just reset the timer.
	// This avoids redundant mDNS Shutdown+Register cycles that produce
	// network traffic spikes during rapid test-mode zone transitions.
	if m.state == StateCommissioningOpen || m.state == StateOperationalCommissioning {
		if m.commissioningTimer != nil {
			m.commissioningTimer.Stop()
		}
		m.commissioningTimer = time.AfterFunc(windowDuration, func() {
			m.exitCommissioningModeInternal(false)
		})
		return nil
	}

	// Start advertising
	if err := m.advertiser.AdvertiseCommissionable(ctx, m.commissionableInfo); err != nil {
		return err
	}

	// Stop any existing timer to prevent a leaked goroutine from firing
	// exitCommissioningModeInternal at the wrong time (e.g., during
	// test-mode auto-reentry).
	if m.commissioningTimer != nil {
		m.commissioningTimer.Stop()
	}

	m.commissioningTimer = time.AfterFunc(windowDuration, func() {
		m.exitCommissioningModeInternal(false)
	})

	// Update state
	oldState := m.state
	if len(m.operationalZones) > 0 {
		m.state = StateOperationalCommissioning
	} else {
		m.state = StateCommissioningOpen
	}

	if m.onStateChange != nil && oldState != m.state {
		m.onStateChange(oldState, m.state)
	}

	return nil
}

// ExitCommissioningMode stops advertising the commissionable service.
// Call this on successful commissioning or user cancellation.
func (m *DiscoveryManager) ExitCommissioningMode() error {
	return m.exitCommissioningModeInternal(true)
}

// exitCommissioningModeInternal stops commissioning mode.
// userInitiated indicates whether this was triggered by user action (true) or
// by the commissioning window timeout (false). This can be used for logging
// or to notify callbacks with context about why commissioning ended.
func (m *DiscoveryManager) exitCommissioningModeInternal(userInitiated bool) error {
	m.mu.Lock()

	// Cancel timer if still running
	if m.commissioningTimer != nil {
		m.commissioningTimer.Stop()
		m.commissioningTimer = nil
	}

	// Stop advertising
	if err := m.advertiser.StopCommissionable(); err != nil {
		m.mu.Unlock()
		return err
	}

	// Update state
	oldState := m.state
	if len(m.operationalZones) > 0 {
		m.state = StateOperational
	} else {
		m.state = StateUncommissioned
	}

	// Capture callbacks before releasing lock
	stateChangeCb := m.onStateChange
	timeoutCb := m.onCommissioningTimeout
	newState := m.state

	m.mu.Unlock()

	// Call callbacks outside lock to avoid deadlocks
	if stateChangeCb != nil && oldState != newState {
		stateChangeCb(oldState, newState)
	}

	// Notify timeout callback if this was an automatic timeout (not user-initiated)
	if !userInitiated && timeoutCb != nil {
		timeoutCb()
	}

	return nil
}

// AddZone adds an operational zone and starts advertising it.
func (m *DiscoveryManager) AddZone(ctx context.Context, info *OperationalInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate
	if info.ZoneID == "" || info.DeviceID == "" {
		return ErrMissingRequired
	}

	// Start advertising
	if err := m.advertiser.AdvertiseOperational(ctx, info); err != nil {
		return err
	}

	m.operationalZones[info.ZoneID] = info

	// Update state if this is first zone
	if len(m.operationalZones) == 1 && m.state == StateUncommissioned {
		oldState := m.state
		m.state = StateOperational
		if m.onStateChange != nil {
			m.onStateChange(oldState, m.state)
		}
	}

	return nil
}

// UpdateZone updates an operational zone's TXT records.
func (m *DiscoveryManager) UpdateZone(info *OperationalInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.operationalZones[info.ZoneID]; !exists {
		return ErrNotFound
	}

	if err := m.advertiser.UpdateOperational(info.ZoneID, info); err != nil {
		return err
	}

	m.operationalZones[info.ZoneID] = info
	return nil
}

// RemoveZone stops advertising an operational zone.
func (m *DiscoveryManager) RemoveZone(zoneID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.operationalZones[zoneID]; !exists {
		return ErrNotFound
	}

	if err := m.advertiser.StopOperational(zoneID); err != nil {
		return err
	}

	delete(m.operationalZones, zoneID)

	// Update state if all zones removed
	if len(m.operationalZones) == 0 {
		oldState := m.state
		switch m.state {
		case StateOperational:
			m.state = StateUncommissioned
		case StateOperationalCommissioning:
			m.state = StateCommissioningOpen
		}
		if m.onStateChange != nil && oldState != m.state {
			m.onStateChange(oldState, m.state)
		}
	}

	return nil
}

// ZoneCount returns the number of operational zones.
func (m *DiscoveryManager) ZoneCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.operationalZones)
}

// Stop stops all advertising.
func (m *DiscoveryManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Cancel commissioning timer
	if m.commissioningTimer != nil {
		m.commissioningTimer.Stop()
		m.commissioningTimer = nil
	}

	// Stop all advertising
	m.advertiser.StopAll()

	// Clear state
	m.operationalZones = make(map[string]*OperationalInfo)
	m.pairingRequests = make(map[uint16]*PairingRequestInfo)

	oldState := m.state
	m.state = StateUnregistered

	if m.onStateChange != nil && oldState != m.state {
		m.onStateChange(oldState, m.state)
	}
}

// AnnouncePairingRequest starts advertising a pairing request for a specific device.
// Controllers use this to signal devices that they want to commission them.
// Multiple concurrent pairing requests are supported (keyed by discriminator).
func (m *DiscoveryManager) AnnouncePairingRequest(ctx context.Context, info *PairingRequestInfo) error {
	if info == nil {
		return ErrMissingRequired
	}

	// Validate the info
	if err := info.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for duplicate
	if _, exists := m.pairingRequests[info.Discriminator]; exists {
		return ErrAlreadyExists
	}

	// Start advertising
	if err := m.advertiser.AnnouncePairingRequest(ctx, info); err != nil {
		return err
	}

	m.pairingRequests[info.Discriminator] = info
	return nil
}

// StopPairingRequest stops advertising a pairing request for a specific discriminator.
func (m *DiscoveryManager) StopPairingRequest(discriminator uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pairingRequests[discriminator]; !exists {
		return ErrNotFound
	}

	if err := m.advertiser.StopPairingRequest(discriminator); err != nil {
		return err
	}

	delete(m.pairingRequests, discriminator)
	return nil
}

// PairingRequestCount returns the number of active pairing requests.
func (m *DiscoveryManager) PairingRequestCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pairingRequests)
}
