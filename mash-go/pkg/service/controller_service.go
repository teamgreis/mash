package service

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/persistence"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/subscription"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// ControllerService orchestrates a MASH controller (EMS).
type ControllerService struct {
	mu sync.RWMutex

	config ControllerConfig
	state  ServiceState

	// Zone identity
	zoneID   string
	zoneName string

	// Discovery
	browser          discovery.Browser
	discoveryManager *discovery.DiscoveryManager
	advertiser       discovery.Advertiser

	// Background discovery state (commissionable)
	discoveryActive bool
	discoveryCancel context.CancelFunc

	// Operational discovery state (for reconnection)
	operationalDiscoveryActive bool
	operationalDiscoveryCancel context.CancelFunc

	// Connected devices
	connectedDevices map[string]*ConnectedDevice

	// Device sessions for operational messaging
	deviceSessions map[string]*DeviceSession

	// Subscription management
	subscriptionManager dispatch.SubscriptionTracker

	// Event handlers
	eventHandlers []EventHandler

	// Persistence (optional, set by CLI)
	certStore  cert.ControllerStore
	stateStore *persistence.ControllerStateStore

	// Protocol logger for structured event capture (optional)
	protocolLogger log.Logger

	// Active pairing requests (keyed by discriminator)
	activePairingRequests map[uint16]context.CancelFunc

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// generateControllerConnID generates a random connection ID for logging.
func (s *ControllerService) generateControllerConnID() string {
	b := make([]byte, 8) // 8 bytes = 16 hex chars
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewControllerService creates a new controller service.
func NewControllerService(config ControllerConfig) (*ControllerService, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	svc := &ControllerService{
		config:                config,
		state:                 StateIdle,
		zoneName:              config.ZoneName,
		connectedDevices:      make(map[string]*ConnectedDevice),
		deviceSessions:        make(map[string]*DeviceSession),
		activePairingRequests: make(map[uint16]context.CancelFunc),
		protocolLogger:        config.ProtocolLogger,
	}

	// Initialize subscription manager
	subConfig := subscription.DefaultConfig()
	subConfig.SuppressBounceBack = config.EnableBounceBackSuppression
	svc.subscriptionManager = subscription.NewManagerWithConfig(subConfig)

	return svc, nil
}

// State returns the current service state.
func (s *ControllerService) State() ServiceState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// ZoneID returns the controller's zone ID.
func (s *ControllerService) ZoneID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.zoneID
}

// ZoneName returns the controller's zone name.
func (s *ControllerService) ZoneName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.zoneName
}

// OnEvent registers an event handler.
func (s *ControllerService) OnEvent(handler EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventHandlers = append(s.eventHandlers, handler)
}

// Start starts the controller service.
func (s *ControllerService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state != StateIdle && s.state != StateStopped {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	s.state = StateStarting
	certStore := s.certStore
	config := s.config
	existingZoneID := s.zoneID
	s.mu.Unlock()

	// Create cancellable context
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Generate or load Zone CA if certStore is configured
	if certStore != nil {
		zoneCA, err := certStore.GetZoneCA()
		if err != nil && err != cert.ErrCertNotFound {
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return fmt.Errorf("failed to load zone CA: %w", err)
		}

		if zoneCA == nil {
			// Generate new Zone CA
			// Use zone name as the zone ID for generation
			zoneID := config.ZoneName
			if existingZoneID != "" {
				zoneID = existingZoneID // Prefer existing zone ID from state
			}

			zoneCA, err = cert.GenerateZoneCA(zoneID, config.ZoneType)
			if err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to generate zone CA: %w", err)
			}

			// Save the Zone CA
			if err := certStore.SetZoneCA(zoneCA); err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to save zone CA: %w", err)
			}
			if err := certStore.Save(); err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to persist zone CA: %w", err)
			}
		}

		// Use the Zone CA's zone ID if we don't have one from state
		if existingZoneID == "" {
			s.mu.Lock()
			s.zoneID = zoneCA.ZoneID
			s.mu.Unlock()
		}

		// Generate or load Controller Operational Certificate
		controllerCert, err := certStore.GetControllerCert()
		if err != nil && err != cert.ErrCertNotFound {
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return fmt.Errorf("failed to load controller cert: %w", err)
		}

		if controllerCert == nil {
			// Generate new controller operational certificate
			controllerID := fmt.Sprintf("controller-%s", zoneCA.ZoneID)
			controllerCert, err = cert.GenerateControllerOperationalCert(zoneCA, controllerID)
			if err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to generate controller cert: %w", err)
			}

			// Save the controller cert
			if err := certStore.SetControllerCert(controllerCert); err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to save controller cert: %w", err)
			}
			if err := certStore.Save(); err != nil {
				s.mu.Lock()
				s.state = StateIdle
				s.mu.Unlock()
				return fmt.Errorf("failed to persist controller cert: %w", err)
			}
		}
	}

	// Initialize mDNS browser if not already set (e.g., by tests)
	if s.browser == nil {
		browser, err := discovery.NewMDNSBrowser(discovery.DefaultBrowserConfig())
		if err != nil {
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return err
		}
		s.browser = browser
	}

	s.mu.Lock()
	s.state = StateRunning
	s.mu.Unlock()

	return nil
}

// Stop stops the controller service.
func (s *ControllerService) Stop() error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return ErrNotStarted
	}
	s.state = StateStopping
	s.mu.Unlock()

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Stop browser
	if s.browser != nil {
		s.browser.Stop()
	}

	// Stop discovery advertising
	if s.discoveryManager != nil {
		s.discoveryManager.Stop()
	}

	// Clear subscriptions
	s.subscriptionManager.ClearAll()

	s.mu.Lock()
	s.state = StateStopped
	s.mu.Unlock()

	return nil
}

// Discover discovers commissionable devices.
func (s *ControllerService) Discover(ctx context.Context, filter discovery.FilterFunc) ([]*discovery.CommissionableService, error) {
	s.mu.RLock()
	if s.state != StateRunning {
		s.mu.RUnlock()
		return nil, ErrNotStarted
	}
	browser := s.browser
	s.mu.RUnlock()

	if browser == nil {
		return nil, ErrNotStarted
	}

	// Create timeout context
	timeout := s.config.DiscoveryTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	browseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Start browsing
	// Note: Browser handles aggregation/deduplication by instance name
	// For one-shot discovery, we only care about added devices (ignore removed channel)
	added, _, err := browser.BrowseCommissionable(browseCtx)
	if err != nil {
		return nil, err
	}

	// Collect results
	var services []*discovery.CommissionableService
	for svc := range added {
		if filter == nil || filter(svc) {
			services = append(services, svc)
		}
	}

	return services, nil
}

// DiscoverByDiscriminator finds a specific device by discriminator.
func (s *ControllerService) DiscoverByDiscriminator(ctx context.Context, discriminator uint16) (*discovery.CommissionableService, error) {
	s.mu.RLock()
	if s.state != StateRunning {
		s.mu.RUnlock()
		return nil, ErrNotStarted
	}
	browser := s.browser
	s.mu.RUnlock()

	if browser == nil {
		return nil, ErrNotStarted
	}

	return browser.FindByDiscriminator(ctx, discriminator)
}

// Commission commissions a device using a setup code.
func (s *ControllerService) Commission(ctx context.Context, service *discovery.CommissionableService, setupCode string) (*ConnectedDevice, error) {
	s.mu.RLock()
	if s.state != StateRunning {
		s.mu.RUnlock()
		return nil, ErrNotStarted
	}
	s.mu.RUnlock()

	// Parse setup code
	code, err := strconv.ParseUint(setupCode, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid setup code", ErrCommissionFailed)
	}

	// Build address to connect to
	addr := fmt.Sprintf("%s:%d", service.Host, service.Port)
	if len(service.Addresses) > 0 {
		// Prefer IP address over hostname
		addr = fmt.Sprintf("%s:%d", service.Addresses[0], service.Port)
	}

	// Connect via TLS with InsecureSkipVerify (security from PASE)
	tlsConfig := transport.NewCommissioningTLSConfig()
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("%w: connection failed: %v", ErrCommissionFailed, err)
	}

	// Create PASE client session. DEC-084 §2.3 requires the discriminator for
	// salt derivation; identities are protocol constants per §2.4.
	session, err := commissioning.NewPASEClientSession(
		commissioning.SetupCode(code),
		service.Discriminator,
	)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: failed to create PASE session: %v", ErrCommissionFailed, err)
	}

	// Perform PASE handshake
	sharedSecret, err := session.Handshake(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, ErrPASEFailed
	}

	// Zone ID is derived from shared secret (both sides compute the same value)
	zoneID := deriveZoneID(sharedSecret)

	// Create framed connection FIRST - needed for cert exchange messages
	framedConn := newFramedConnection(conn)

	// Perform certificate exchange with device
	// This issues an operational certificate signed by our Zone CA
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore == nil {
		conn.Close()
		return nil, fmt.Errorf("%w: no certificate store configured", ErrCommissionFailed)
	}

	zoneCA, err := certStore.GetZoneCA()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: failed to get Zone CA: %v", ErrCommissionFailed, err)
	}

	// Perform commissioning cert install on the commissioning ALPN.
	// Per DEC-085, this uses MsgTypes 10/11/12/13/20 — distinct from in-session
	// renewal (30–33 on operational ALPN), which is in controller_renewal.go.
	operationalCert, err := PerformCommissioningCertInstall(
		ctx,
		zoneCA,
		framedConn,
		s.config.ZoneType,
		1, // zonePriority — controller uses priority 1 for the first zone in this controller's set
	)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: certificate exchange failed: %v", ErrCommissionFailed, err)
	}

	// Extract device ID from the operational certificate (Matter-style: embedded in CommonName)
	// The device ID was assigned by the controller during certificate signing
	deviceID, err := cert.ExtractDeviceID(operationalCert)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: failed to extract device ID: %v", ErrCommissionFailed, err)
	}

	// framedConn was already created above for cert exchange
	// Now use it for operational messaging

	// Create device session for operational messaging
	deviceSession := NewDeviceSession(deviceID, framedConn)

	// Set snapshot policy and protocol logger if configured
	deviceSession.SetSnapshotPolicy(s.config.SnapshotPolicy)
	if s.protocolLogger != nil {
		connID := s.generateControllerConnID()
		deviceSession.SetProtocolLogger(s.protocolLogger, connID)
	}

	// Create device record
	device := &ConnectedDevice{
		ID:              deviceID,
		ZoneID:          zoneID,
		Host:            service.Host,
		Port:            service.Port,
		Addresses:       service.Addresses,
		Connected:       true,
		LastSeen:        time.Now(),
		OperationalCert: operationalCert, // Store the operational cert for display
	}

	// Store device and session
	s.mu.Lock()
	s.connectedDevices[device.ID] = device
	s.deviceSessions[device.ID] = deviceSession
	s.zoneID = zoneID // Store our zone ID
	s.mu.Unlock()

	// Start message loop in background to receive responses/notifications
	go s.runDeviceMessageLoop(deviceID, framedConn, deviceSession)

	// Check protocol version compatibility (DEC-050)
	if err := s.checkDeviceVersion(ctx, deviceSession); err != nil {
		// Incompatible version -- tear down
		s.mu.Lock()
		delete(s.connectedDevices, device.ID)
		delete(s.deviceSessions, device.ID)
		s.mu.Unlock()
		deviceSession.Close()
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrCommissionFailed, err)
	}

	// Read remote device capabilities for snapshot tracking
	if snap := readRemoteCapabilities(ctx, deviceSession); snap != nil {
		deviceSession.SetRemoteSnapshotCache(snap)
	}

	// Persist state immediately after commissioning
	_ = s.SaveState()

	// Emit event
	s.emitEvent(Event{
		Type:     EventCommissioned,
		DeviceID: device.ID,
	})

	return device, nil
}

// CommissionDevice discovers and commissions a device by discriminator.
// If the device is not immediately available, it announces a pairing request
// and waits for the device to appear (up to PairingRequestTimeout).
//
// This method supports deferred commissioning scenarios like:
// - SMGW pre-provisioning devices before installation
// - Backend systems that provision devices ahead of time
func (s *ControllerService) CommissionDevice(ctx context.Context, discriminator uint16, setupCode string) (*ConnectedDevice, error) {
	s.mu.RLock()
	if s.state != StateRunning {
		s.mu.RUnlock()
		return nil, ErrNotStarted
	}
	browser := s.browser
	zoneID := s.zoneID
	zoneName := s.zoneName
	advertiser := s.advertiser
	timeout := s.config.PairingRequestTimeout
	s.mu.RUnlock()

	if browser == nil {
		return nil, ErrNotStarted
	}

	// Apply default timeout if not configured
	if timeout == 0 {
		timeout = DefaultPairingRequestTimeout
	}

	// First, try to find devices directly (may find multiple with same discriminator)
	discoverCtx, discoverCancel := context.WithTimeout(ctx, s.config.DiscoveryTimeout)
	candidates, err := browser.FindAllByDiscriminator(discoverCtx, discriminator)
	discoverCancel()

	if len(candidates) > 0 {
		result, err := commissionCandidates(ctx, candidates, setupCode, s.Commission)
		if err == nil {
			return result, nil
		}
		if !isPASEFailure(err) {
			return nil, err
		}
		// All candidates failed PASE -- fall through to pairing request
		// (the correct device may not be advertising yet)
	}
	_ = err // Browse errors are non-fatal here; we fall through to pairing request

	// No candidates found or all failed PASE - need to announce pairing request
	// First check that we have a zone ID (required for pairing request)
	if zoneID == "" {
		return nil, ErrZoneIDRequired
	}

	// Check if there's already an active pairing request for this discriminator
	s.mu.Lock()
	if _, exists := s.activePairingRequests[discriminator]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: discriminator %d", discovery.ErrAlreadyExists, discriminator)
	}

	// Create cancellable context for the pairing request
	pairingCtx, pairingCancel := context.WithCancel(ctx)
	s.activePairingRequests[discriminator] = pairingCancel
	s.mu.Unlock()

	// Ensure cleanup on exit
	defer func() {
		pairingCancel()
		s.mu.Lock()
		delete(s.activePairingRequests, discriminator)
		s.mu.Unlock()
	}()

	// Create DiscoveryManager if needed for pairing request
	if s.discoveryManager == nil && advertiser != nil {
		s.mu.Lock()
		if s.discoveryManager == nil {
			s.discoveryManager = discovery.NewDiscoveryManager(advertiser)
		}
		s.mu.Unlock()
	}

	// Get the discovery manager
	s.mu.RLock()
	dm := s.discoveryManager
	s.mu.RUnlock()

	if dm == nil {
		return nil, fmt.Errorf("no advertiser configured for pairing requests")
	}

	// Announce pairing request
	pairingInfo := &discovery.PairingRequestInfo{
		Discriminator: discriminator,
		ZoneID:        zoneID,
		ZoneName:      zoneName,
		Host:          "controller.local", // TODO: Get actual hostname
	}

	if err := dm.AnnouncePairingRequest(pairingCtx, pairingInfo); err != nil {
		return nil, fmt.Errorf("failed to announce pairing request: %w", err)
	}

	// Ensure pairing request is stopped on exit
	defer func() {
		_ = dm.StopPairingRequest(discriminator)
	}()

	// Poll for the device to appear
	pollInterval := s.config.PairingRequestPollInterval
	if pollInterval == 0 {
		pollInterval = PairingRequestPollInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	// Track candidates that already failed PASE (deterministic -- wrong device
	// will never succeed with the same setup code). Keyed by "host:port".
	tried := make(map[string]struct{})

	for {
		select {
		case <-pairingCtx.Done():
			// Check if this was our cancellation or parent context
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrCommissioningCancelled

		case <-timeoutTimer.C:
			return nil, ErrPairingRequestTimeout

		case <-ticker.C:
			// Discover all matching candidates
			discoverCtx, discoverCancel := context.WithTimeout(pairingCtx, s.config.DiscoveryTimeout)
			found, _ := browser.FindAllByDiscriminator(discoverCtx, discriminator)
			discoverCancel()

			if len(found) == 0 {
				continue
			}

			// Filter out already-tried candidates
			var untried []*discovery.CommissionableService
			for _, c := range found {
				key := fmt.Sprintf("%s:%d", c.Host, c.Port)
				if _, ok := tried[key]; !ok {
					untried = append(untried, c)
				}
			}
			if len(untried) == 0 {
				continue
			}

			result, err := commissionCandidates(ctx, untried, setupCode, s.Commission)
			if err == nil {
				return result, nil
			}

			// Mark failed PASE candidates as tried
			for _, c := range untried {
				key := fmt.Sprintf("%s:%d", c.Host, c.Port)
				tried[key] = struct{}{}
			}

			if !isPASEFailure(err) {
				return nil, err
			}
			// All untried candidates failed PASE -- continue polling for new ones
		}
	}
}

// CancelCommissioning cancels an active commissioning operation for a discriminator.
// This stops the pairing request and causes CommissionDevice to return with ErrCommissioningCancelled.
func (s *ControllerService) CancelCommissioning(discriminator uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cancel, exists := s.activePairingRequests[discriminator]
	if !exists {
		return ErrNoPairingRequestActive
	}

	// Cancel the pairing request context
	cancel()

	return nil
}

// runDeviceMessageLoop reads messages from the device and dispatches to the session.
func (s *ControllerService) runDeviceMessageLoop(deviceID string, conn *framedConnection, session *DeviceSession) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		data, err := conn.ReadFrame()
		if err != nil {
			// Connection closed or error
			s.handleDeviceSessionClose(deviceID)
			return
		}

		// Dispatch to session
		session.OnMessage(data)
	}
}

// handleDeviceSessionClose cleans up when a device session closes.
func (s *ControllerService) handleDeviceSessionClose(deviceID string) {
	s.mu.Lock()
	if session, exists := s.deviceSessions[deviceID]; exists {
		session.Close()
		delete(s.deviceSessions, deviceID)
	}
	s.mu.Unlock()

	// Notify disconnect
	s.HandleDeviceDisconnect(deviceID)
}

// GetSession returns the session for a connected device.
// Returns nil if the device is not connected or has no session.
func (s *ControllerService) GetSession(deviceID string) *DeviceSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deviceSessions[deviceID]
}

// Decommission removes a device from the zone.
func (s *ControllerService) Decommission(deviceID string) error {
	s.mu.Lock()

	device, exists := s.connectedDevices[deviceID]
	if !exists {
		s.mu.Unlock()
		return ErrDeviceNotFound
	}

	// Close and remove the session if it exists
	if session, hasSession := s.deviceSessions[deviceID]; hasSession {
		session.Close()
		delete(s.deviceSessions, deviceID)
	}

	// TODO: Remove subscriptions for this device
	// TODO: Send decommission command to device
	// TODO: Revoke device certificate

	delete(s.connectedDevices, deviceID)
	s.mu.Unlock()

	// Save state to persist the removal
	_ = s.SaveState() // Ignore error - device is already removed from memory

	s.emitEvent(Event{
		Type:     EventDecommissioned,
		DeviceID: device.ID,
	})

	return nil
}

// DeviceCount returns the number of paired (commissioned) devices.
// Note: This includes both online and offline devices.
func (s *ControllerService) DeviceCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.connectedDevices)
}

// ConnectedCount returns the number of currently connected devices.
func (s *ControllerService) ConnectedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, d := range s.connectedDevices {
		if d.Connected {
			count++
		}
	}
	return count
}

// GetDevice returns information about a connected device.
func (s *ControllerService) GetDevice(deviceID string) *ConnectedDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if d, exists := s.connectedDevices[deviceID]; exists {
		// Return a copy
		copy := *d
		return &copy
	}
	return nil
}

// GetAllDevices returns all connected devices.
func (s *ControllerService) GetAllDevices() []*ConnectedDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*ConnectedDevice, 0, len(s.connectedDevices))
	for _, d := range s.connectedDevices {
		copy := *d
		result = append(result, &copy)
	}
	return result
}

// HandleDeviceConnect handles a device connection.
func (s *ControllerService) HandleDeviceConnect(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if device, exists := s.connectedDevices[deviceID]; exists {
		device.Connected = true
		device.LastSeen = time.Now()
	}

	s.emitEvent(Event{
		Type:     EventConnected,
		DeviceID: deviceID,
	})
}

// HandleDeviceDisconnect handles a device disconnection.
func (s *ControllerService) HandleDeviceDisconnect(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if device, exists := s.connectedDevices[deviceID]; exists {
		device.Connected = false
		device.LastSeen = time.Now()
	}

	s.emitEvent(Event{
		Type:     EventDisconnected,
		DeviceID: deviceID,
	})
}

// emitEvent sends an event to all registered handlers.
func (s *ControllerService) emitEvent(event Event) {
	for _, handler := range s.eventHandlers {
		go handler(event)
	}
}

// SetBrowser sets the discovery browser (for testing/DI).
func (s *ControllerService) SetBrowser(browser discovery.Browser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browser = browser
}

// SetAdvertiser sets the discovery advertiser (for testing/DI).
func (s *ControllerService) SetAdvertiser(advertiser discovery.Advertiser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advertiser = advertiser
	s.discoveryManager = discovery.NewDiscoveryManager(advertiser)
}

// StartDiscovery starts background mDNS discovery.
// Discovered devices are emitted as EventDeviceDiscovered events.
// Use StopDiscovery to stop the background discovery.
func (s *ControllerService) StartDiscovery(ctx context.Context, filter discovery.FilterFunc) error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return ErrNotStarted
	}
	if s.discoveryActive {
		s.mu.Unlock()
		return nil // Already discovering
	}

	browser := s.browser
	if browser == nil {
		s.mu.Unlock()
		return ErrNotStarted
	}

	// Create cancellable context for this discovery session
	discoveryCtx, cancel := context.WithCancel(ctx)
	s.discoveryActive = true
	s.discoveryCancel = cancel
	s.mu.Unlock()

	// Start browsing in background
	go s.runDiscoveryLoop(discoveryCtx, filter)

	return nil
}

// runDiscoveryLoop runs the background discovery and emits events.
func (s *ControllerService) runDiscoveryLoop(ctx context.Context, filter discovery.FilterFunc) {
	added, removed, err := s.browser.BrowseCommissionable(ctx)
	if err != nil {
		s.mu.Lock()
		s.discoveryActive = false
		s.discoveryCancel = nil
		s.mu.Unlock()
		return
	}

	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.discoveryActive = false
			s.discoveryCancel = nil
			s.mu.Unlock()
			return

		case svc, ok := <-added:
			if !ok {
				s.mu.Lock()
				s.discoveryActive = false
				s.discoveryCancel = nil
				s.mu.Unlock()
				return
			}

			// Apply filter if provided
			if filter != nil && !filter(svc) {
				continue
			}

			// Emit discovery event
			s.emitEvent(Event{
				Type:              EventDeviceDiscovered,
				DiscoveredService: svc,
			})

		case svc, ok := <-removed:
			if !ok {
				// Removed channel closed, but added might still be open
				continue
			}

			// Apply filter if provided
			if filter != nil && !filter(svc) {
				continue
			}

			// Emit removal event
			s.emitEvent(Event{
				Type:              EventDeviceGone,
				DiscoveredService: svc,
			})
		}
	}
}

// StopDiscovery stops background mDNS discovery.
func (s *ControllerService) StopDiscovery() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	s.discoveryActive = false
}

// IsDiscovering returns true if background discovery is active.
func (s *ControllerService) IsDiscovering() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discoveryActive
}

// StartOperationalDiscovery starts background mDNS discovery for known devices.
// This browses for devices advertising on _mash._tcp with this controller's zone ID.
// Known devices that are found will trigger reconnection attempts.
func (s *ControllerService) StartOperationalDiscovery(ctx context.Context) error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return ErrNotStarted
	}
	if s.operationalDiscoveryActive {
		s.mu.Unlock()
		return nil // Already discovering
	}

	browser := s.browser
	zoneID := s.zoneID
	if browser == nil || zoneID == "" {
		s.mu.Unlock()
		return ErrNotStarted
	}

	// Create cancellable context for this discovery session
	discoveryCtx, cancel := context.WithCancel(ctx)
	s.operationalDiscoveryActive = true
	s.operationalDiscoveryCancel = cancel
	s.mu.Unlock()

	// Start browsing in background
	go s.runOperationalDiscoveryLoop(discoveryCtx, zoneID)

	return nil
}

// runOperationalDiscoveryLoop runs operational discovery and handles reconnection.
func (s *ControllerService) runOperationalDiscoveryLoop(ctx context.Context, zoneID string) {
	if s.config.Logger != nil {
		s.config.Logger.Debug("runOperationalDiscoveryLoop: starting", "zoneID", zoneID)
	}

	results, err := s.browser.BrowseOperational(ctx, zoneID)
	if err != nil {
		if s.config.Logger != nil {
			s.config.Logger.Debug("runOperationalDiscoveryLoop: BrowseOperational failed", "error", err)
		}
		s.mu.Lock()
		s.operationalDiscoveryActive = false
		s.operationalDiscoveryCancel = nil
		s.mu.Unlock()
		return
	}

	for {
		select {
		case <-ctx.Done():
			if s.config.Logger != nil {
				s.config.Logger.Debug("runOperationalDiscoveryLoop: context cancelled")
			}
			s.mu.Lock()
			s.operationalDiscoveryActive = false
			s.operationalDiscoveryCancel = nil
			s.mu.Unlock()
			return

		case svc, ok := <-results:
			if !ok {
				if s.config.Logger != nil {
					s.config.Logger.Debug("runOperationalDiscoveryLoop: results channel closed")
				}
				s.mu.Lock()
				s.operationalDiscoveryActive = false
				s.operationalDiscoveryCancel = nil
				s.mu.Unlock()
				return
			}

			// Check if this is a known device
			s.mu.RLock()
			device, isKnown := s.connectedDevices[svc.DeviceID]
			alreadyConnected := isKnown && device.Connected
			s.mu.RUnlock()

			if !isKnown {
				continue // Not our device
			}

			if alreadyConnected {
				continue // Already connected
			}

			// Emit rediscovery event
			s.emitEvent(Event{
				Type:              EventDeviceRediscovered,
				DeviceID:          svc.DeviceID,
				DiscoveredService: svc,
			})

			// Attempt reconnection in background
			go s.attemptReconnection(ctx, svc)
		}
	}
}

// attemptReconnection tries to reconnect to a known device.
func (s *ControllerService) attemptReconnection(ctx context.Context, svc *discovery.OperationalService) {
	// Build the connection address
	addr := fmt.Sprintf("%s:%d", svc.Host, svc.Port)

	// Create TLS config for operational connection
	tlsConfig, err := s.buildOperationalTLSConfig(svc.DeviceID)
	if err != nil {
		// Fall back to insecure mode if certificates not available
		tlsConfig = transport.NewCommissioningTLSConfig()
	}

	// Attempt connection
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		// Connection failed - device might not be ready yet
		s.emitEvent(Event{
			Type:     EventReconnectionFailed,
			DeviceID: svc.DeviceID,
			Error:    err,
		})
		return
	}

	// Update device state
	s.mu.Lock()
	device, exists := s.connectedDevices[svc.DeviceID]
	if exists {
		device.Connected = true
		device.Host = svc.Host
		device.Port = svc.Port
		device.LastSeen = time.Now()
	}
	s.mu.Unlock()

	if !exists {
		conn.Close()
		return
	}

	// Create framed connection wrapper for operational messaging
	framedConn := newFramedConnection(conn)

	// Create device session
	session := NewDeviceSession(svc.DeviceID, framedConn)

	// Set snapshot policy and protocol logger if configured
	session.SetSnapshotPolicy(s.config.SnapshotPolicy)
	if s.protocolLogger != nil {
		connID := s.generateControllerConnID()
		session.SetProtocolLogger(s.protocolLogger, connID)
	}

	s.mu.Lock()
	s.deviceSessions[svc.DeviceID] = session
	s.mu.Unlock()

	// Check protocol version compatibility (DEC-050)
	if err := s.checkDeviceVersion(ctx, session); err != nil {
		s.mu.Lock()
		delete(s.deviceSessions, svc.DeviceID)
		if device, ok := s.connectedDevices[svc.DeviceID]; ok {
			device.Connected = false
		}
		s.mu.Unlock()
		session.Close()
		conn.Close()
		s.emitEvent(Event{
			Type:     EventReconnectionFailed,
			DeviceID: svc.DeviceID,
			Error:    err,
		})
		return
	}

	// Read remote device capabilities for snapshot tracking
	if snap := readRemoteCapabilities(ctx, session); snap != nil {
		session.SetRemoteSnapshotCache(snap)
	}

	// Emit reconnected event
	s.emitEvent(Event{
		Type:     EventDeviceReconnected,
		DeviceID: svc.DeviceID,
	})

	// Start message loop - blocks until connection closes
	// Note: runDeviceMessageLoop calls handleDeviceSessionClose on exit
	s.runDeviceMessageLoop(svc.DeviceID, framedConn, session)
}

// Reconnect establishes an operational TLS connection to a previously commissioned device.
// This is used after DEC-066 commissioning flow where the commissioning connection is closed
// and the controller must reconnect using operational certificates.
//
// The deviceID must correspond to a device that was previously commissioned and is stored
// in the connectedDevices map with valid Host and Port information.
func (s *ControllerService) Reconnect(ctx context.Context, deviceID string) error {
	// Look up device information
	s.mu.RLock()
	device, exists := s.connectedDevices[deviceID]
	if !exists {
		s.mu.RUnlock()
		return fmt.Errorf("device %s not found", deviceID)
	}
	host := device.Host
	port := device.Port
	s.mu.RUnlock()

	if host == "" || port == 0 {
		return fmt.Errorf("device %s has no address information", deviceID)
	}

	// Build the connection address
	addr := fmt.Sprintf("%s:%d", host, port)

	// Create TLS config for operational connection
	tlsConfig, err := s.buildOperationalTLSConfig(deviceID)
	if err != nil {
		// Fall back to insecure mode if certificates not available
		tlsConfig = transport.NewCommissioningTLSConfig()
	}

	// Attempt connection with context timeout
	dialer := &tls.Dialer{Config: tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		s.emitEvent(Event{
			Type:     EventReconnectionFailed,
			DeviceID: deviceID,
			Error:    err,
		})
		return fmt.Errorf("failed to connect to device %s at %s: %w", deviceID, addr, err)
	}

	tlsConn := conn.(*tls.Conn)

	// Update device state
	s.mu.Lock()
	if dev, ok := s.connectedDevices[deviceID]; ok {
		dev.Connected = true
		dev.LastSeen = time.Now()
	}
	s.mu.Unlock()

	// Create framed connection wrapper for operational messaging
	framedConn := newFramedConnection(tlsConn)

	// Create device session
	session := NewDeviceSession(deviceID, framedConn)

	// Set snapshot policy and protocol logger if configured
	session.SetSnapshotPolicy(s.config.SnapshotPolicy)
	if s.protocolLogger != nil {
		connID := s.generateControllerConnID()
		session.SetProtocolLogger(s.protocolLogger, connID)
	}

	s.mu.Lock()
	s.deviceSessions[deviceID] = session
	s.mu.Unlock()

	// Check protocol version compatibility (DEC-050)
	if err := s.checkDeviceVersion(ctx, session); err != nil {
		s.mu.Lock()
		delete(s.deviceSessions, deviceID)
		if dev, ok := s.connectedDevices[deviceID]; ok {
			dev.Connected = false
		}
		s.mu.Unlock()
		session.Close()
		tlsConn.Close()
		s.emitEvent(Event{
			Type:     EventReconnectionFailed,
			DeviceID: deviceID,
			Error:    err,
		})
		return fmt.Errorf("version check failed for device %s: %w", deviceID, err)
	}

	// Read remote device capabilities for snapshot tracking
	if snap := readRemoteCapabilities(ctx, session); snap != nil {
		session.SetRemoteSnapshotCache(snap)
	}

	// Emit reconnected event
	s.emitEvent(Event{
		Type:     EventDeviceReconnected,
		DeviceID: deviceID,
	})

	// Start message loop in goroutine (non-blocking)
	// Note: runDeviceMessageLoop calls handleDeviceSessionClose on exit
	go s.runDeviceMessageLoop(deviceID, framedConn, session)

	return nil
}

// isPASEFailure reports whether the error is a PASE authentication failure,
// which is retryable during discriminator collision handling.
func isPASEFailure(err error) bool {
	return errors.Is(err, ErrPASEFailed)
}

// commissionFunc is the signature for a function that commissions a single device.
type commissionFunc func(context.Context, *discovery.CommissionableService, string) (*ConnectedDevice, error)

// commissionCandidates tries to commission each candidate in order.
// Returns on first success or first non-PASE error.
// Returns the last error if all candidates fail with PASE errors.
func commissionCandidates(
	ctx context.Context,
	candidates []*discovery.CommissionableService,
	setupCode string,
	commission commissionFunc,
) (*ConnectedDevice, error) {
	var lastErr error
	for _, candidate := range candidates {
		result, err := commission(ctx, candidate, setupCode)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isPASEFailure(err) {
			return nil, err // Non-retryable
		}
		// PASE failed -- try next candidate
	}
	return nil, lastErr
}

// buildOperationalTLSConfig creates a TLS config for operational (post-commissioning)
// connections to devices. Uses the controller's operational certificate for mutual TLS.
func (s *ControllerService) buildOperationalTLSConfig(deviceID string) (*tls.Config, error) {
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore == nil {
		return nil, fmt.Errorf("no certificate store configured")
	}

	// Get controller's operational certificate
	controllerCert, err := certStore.GetControllerCert()
	if err != nil {
		return nil, fmt.Errorf("failed to get controller certificate: %w", err)
	}

	// Get Zone CA for verifying device certificates
	zoneCA, err := certStore.GetZoneCA()
	if err != nil {
		return nil, fmt.Errorf("failed to get Zone CA: %w", err)
	}

	// Build operational TLS config with mutual authentication
	return transport.NewOperationalClientTLSConfig(&transport.OperationalTLSConfig{
		ControllerCert: controllerCert.TLSCertificate(),
		ZoneCAs:        zoneCA.TLSClientCAs(),
		ServerName:     deviceID,
	})
}

// StopOperationalDiscovery stops background operational discovery.
func (s *ControllerService) StopOperationalDiscovery() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.operationalDiscoveryCancel != nil {
		s.operationalDiscoveryCancel()
		s.operationalDiscoveryCancel = nil
	}
	s.operationalDiscoveryActive = false
}

// IsOperationalDiscovering returns true if operational discovery is active.
func (s *ControllerService) IsOperationalDiscovering() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.operationalDiscoveryActive
}

// SetCertStore sets the certificate store for persistence.
func (s *ControllerService) SetCertStore(store cert.ControllerStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.certStore = store
}

// GetCertStore returns the certificate store.
func (s *ControllerService) GetCertStore() cert.ControllerStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.certStore
}

// SetStateStore sets the state store for persistence.
func (s *ControllerService) SetStateStore(store *persistence.ControllerStateStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateStore = store
}

// SaveState persists the current controller state.
// This should be called on graceful shutdown and after commissioning changes.
func (s *ControllerService) SaveState() error {
	s.mu.RLock()
	store := s.stateStore
	if store == nil {
		s.mu.RUnlock()
		return nil // No store configured, no-op
	}

	state := &persistence.ControllerState{
		SavedAt: time.Now(),
		ZoneID:  s.zoneID,
	}

	// Save device memberships
	for _, device := range s.connectedDevices {
		dm := persistence.DeviceMembership{
			DeviceID:   device.ID,
			DeviceType: device.DeviceType,
			JoinedAt:   device.LastSeen, // Use LastSeen as proxy for JoinedAt
			LastSeenAt: device.LastSeen,
		}
		state.Devices = append(state.Devices, dm)
	}

	s.mu.RUnlock()

	return store.Save(state)
}

// LoadState restores the controller state from persistence.
// This should be called during Start() if a state store is configured.
func (s *ControllerService) LoadState() error {
	s.mu.Lock()
	store := s.stateStore
	s.mu.Unlock()

	if store == nil {
		return nil // No store configured, no-op
	}

	state, err := store.Load()
	if err != nil {
		return err
	}
	if state == nil {
		return nil // No saved state
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Restore zone ID
	s.zoneID = state.ZoneID

	// Restore device list (as disconnected - they'll reconnect)
	for _, dm := range state.Devices {
		s.connectedDevices[dm.DeviceID] = &ConnectedDevice{
			ID:         dm.DeviceID,
			DeviceType: dm.DeviceType,
			Connected:  false, // Will be updated on reconnect
			LastSeen:   dm.LastSeenAt,
		}
	}

	return nil
}

// RenewDevice initiates and completes certificate renewal for a connected device.
// The renewal happens over the existing TLS connection without disconnection.
func (s *ControllerService) RenewDevice(ctx context.Context, deviceID string) error {
	s.mu.RLock()
	session := s.deviceSessions[deviceID]
	certStore := s.certStore
	s.mu.RUnlock()

	if session == nil {
		return fmt.Errorf("no session for device %s", deviceID)
	}

	if certStore == nil {
		return fmt.Errorf("no certificate store configured")
	}

	// Get Zone CA for signing
	zoneCA, err := certStore.GetZoneCA()
	if err != nil {
		return fmt.Errorf("get Zone CA: %w", err)
	}
	if zoneCA == nil {
		return fmt.Errorf("Zone CA not available")
	}

	// Create renewal handler using the session's connection
	renewalHandler := NewControllerRenewalHandler(zoneCA, session.Conn())

	// Set the handler on the session so responses get routed correctly
	session.SetRenewalHandler(renewalHandler)
	defer session.SetRenewalHandler(nil) // Clean up after renewal

	// Perform the renewal
	newCert, err := renewalHandler.RenewDevice(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("renewal failed: %w", err)
	}

	// Emit success event
	s.emitEvent(Event{
		Type:     EventCertificateRenewed,
		DeviceID: deviceID,
	})

	// Log the new certificate expiry
	_ = newCert // Could update tracking here

	return nil
}

// RenewalCheckInterval is how often to check for certificate renewal needs.
const RenewalCheckInterval = 24 * time.Hour

// RenewControllerCert renews the controller's operational certificate.
// This generates a new certificate signed by the Zone CA and saves it.
// Existing TLS sessions are not affected (they use session keys).
func (s *ControllerService) RenewControllerCert() error {
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore == nil {
		return fmt.Errorf("no certificate store configured")
	}

	// Get Zone CA for signing
	zoneCA, err := certStore.GetZoneCA()
	if err != nil {
		return fmt.Errorf("get Zone CA: %w", err)
	}

	// Generate new controller operational certificate
	controllerID := fmt.Sprintf("controller-%s", zoneCA.ZoneID)
	newCert, err := cert.GenerateControllerOperationalCert(zoneCA, controllerID)
	if err != nil {
		return fmt.Errorf("generate controller cert: %w", err)
	}

	// Save the new certificate
	if err := certStore.SetControllerCert(newCert); err != nil {
		return fmt.Errorf("set controller cert: %w", err)
	}
	if err := certStore.Save(); err != nil {
		return fmt.Errorf("save controller cert: %w", err)
	}

	// Emit event for renewal
	s.emitEvent(Event{
		Type: EventControllerCertRenewed,
	})

	return nil
}

// ControllerCertNeedsRenewal checks if the controller's certificate needs renewal.
func (s *ControllerService) ControllerCertNeedsRenewal() bool {
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore == nil {
		return false
	}

	controllerCert, err := certStore.GetControllerCert()
	if err != nil {
		return true // No cert means we need one
	}

	return controllerCert.NeedsRenewal()
}

// GetControllerCert returns the controller's operational certificate.
func (s *ControllerService) GetControllerCert() (*cert.OperationalCert, error) {
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	if certStore == nil {
		return nil, fmt.Errorf("no certificate store configured")
	}

	return certStore.GetControllerCert()
}

// StartRenewalChecking starts the background renewal checking goroutine.
// It checks daily if the controller certificate needs renewal.
func (s *ControllerService) StartRenewalChecking(ctx context.Context) {
	go s.runRenewalCheckLoop(ctx)
}

// runRenewalCheckLoop periodically checks if the controller cert needs renewal.
func (s *ControllerService) runRenewalCheckLoop(ctx context.Context) {
	// Check immediately on start
	s.checkAndRenewControllerCert()

	ticker := time.NewTicker(RenewalCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAndRenewControllerCert()
		}
	}
}

// checkAndRenewControllerCert checks if renewal is needed and performs it.
func (s *ControllerService) checkAndRenewControllerCert() {
	if s.ControllerCertNeedsRenewal() {
		if err := s.RenewControllerCert(); err != nil {
			// Log error but don't crash - will retry on next check
			s.emitEvent(Event{
				Type:  EventError,
				Error: fmt.Errorf("controller cert renewal failed: %w", err),
			})
		}
	}
}
