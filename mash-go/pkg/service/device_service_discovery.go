package service

import (
	"context"
	"fmt"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// DiscoveryManager returns the discovery manager (for test control commands).
func (s *DeviceService) DiscoveryManager() *discovery.DiscoveryManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discoveryManager
}

// SetAdvertiser sets the discovery advertiser (for testing/DI).
func (s *DeviceService) SetAdvertiser(advertiser discovery.Advertiser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advertiser = advertiser
	s.discoveryManager = discovery.NewDiscoveryManager(advertiser)
	s.discoveryManager.SetCommissionableInfo(&discovery.CommissionableInfo{
		Discriminator: s.config.Discriminator,
		Categories:    s.config.Categories,
		Serial:        s.config.SerialNumber,
		Brand:         s.config.Brand,
		Model:         s.config.Model,
		DeviceName:    s.config.DeviceName,
		Port:          8443,
	})

	// Set commissioning window duration from config
	if s.config.CommissioningWindowDuration > 0 {
		s.discoveryManager.SetCommissioningWindowDuration(s.config.CommissioningWindowDuration)
	}

	// Register callback for commissioning timeout. Must mirror the
	// callback in Start() -- close the ALPN gate but keep the listener
	// up. See Start() for the rationale.
	s.discoveryManager.OnCommissioningTimeout(func() {
		s.commissioningOpen.Store(false)
		s.emitEvent(Event{
			Type:   EventCommissioningClosed,
			Reason: "timeout",
		})
	})
}

// StartOperationalAdvertising starts mDNS operational advertising for all known zones.
// This should be called after Start() when the device has restored zones from persistence.
// It allows controllers to rediscover the device for reconnection.
func (s *DeviceService) StartOperationalAdvertising() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.discoveryManager == nil {
		s.debugLog("StartOperationalAdvertising: no discovery manager, skipping")
		return nil // No discovery manager, skip
	}

	port := uint16(0)
	if s.listener != nil {
		port = parsePort(s.listener.Addr().String())
	}

	s.debugLog("StartOperationalAdvertising: advertising zones",
		"deviceID", s.deviceID,
		"zoneCount", len(s.connectedZones),
		"port", port)

	for zoneID := range s.connectedZones {
		opInfo := &discovery.OperationalInfo{
			ZoneID:        zoneID,
			DeviceID:      s.deviceID,
			VendorProduct: fmt.Sprintf("%04x:%04x", s.device.VendorID(), s.device.ProductID()),
			EndpointCount: uint8(s.device.EndpointCount()),
			Port:          port,
		}

		s.debugLog("StartOperationalAdvertising: advertising zone",
			"zoneID", zoneID,
			"deviceID", s.deviceID)

		if err := s.discoveryManager.AddZone(s.ctx, opInfo); err != nil {
			s.debugLog("StartOperationalAdvertising: failed to start advertising",
				"zoneID", zoneID, "error", err)
		}
	}

	return nil
}

// SetBrowser sets the discovery browser (for testing/DI).
func (s *DeviceService) SetBrowser(browser discovery.Browser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browser = browser
}

// StartPairingRequestListening starts listening for pairing requests.
// When a pairing request with a matching discriminator is discovered,
// the device will automatically open its commissioning window.
func (s *DeviceService) StartPairingRequestListening(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already listening
	if s.pairingRequestActive {
		return nil
	}

	// Check if at max zones - don't listen if we can't accept more
	if len(s.connectedZones) >= s.config.MaxZones {
		s.debugLog("StartPairingRequestListening: at max zones, not starting")
		return nil
	}

	// Need a browser to listen
	if s.browser == nil {
		s.debugLog("StartPairingRequestListening: no browser available")
		return nil
	}

	// Create cancellable context for browsing
	browseCtx, cancel := context.WithCancel(ctx)
	s.pairingRequestCancel = cancel
	s.pairingRequestActive = true

	// Start browsing in background
	go s.runPairingRequestListener(browseCtx)

	s.debugLog("StartPairingRequestListening: started")
	return nil
}

// StopPairingRequestListening stops listening for pairing requests.
func (s *DeviceService) StopPairingRequestListening() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.pairingRequestActive {
		return nil
	}

	if s.pairingRequestCancel != nil {
		s.pairingRequestCancel()
		s.pairingRequestCancel = nil
	}

	s.pairingRequestActive = false
	s.debugLog("StopPairingRequestListening: stopped")
	return nil
}

// IsPairingRequestListening returns true if the device is actively listening for pairing requests.
func (s *DeviceService) IsPairingRequestListening() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pairingRequestActive
}

// runPairingRequestListener runs the pairing request browser in the background.
func (s *DeviceService) runPairingRequestListener(ctx context.Context) {
	s.mu.RLock()
	browser := s.browser
	discriminator := s.config.Discriminator
	s.mu.RUnlock()

	if browser == nil {
		s.mu.Lock()
		s.pairingRequestActive = false
		s.mu.Unlock()
		return
	}

	// BrowsePairingRequests spawns background goroutines and returns immediately.
	// We block on ctx.Done() so pairingRequestActive stays true until cancelled.
	err := browser.BrowsePairingRequests(ctx, func(svc discovery.PairingRequestService) {
		s.handlePairingRequestDiscovered(svc, discriminator)
	})

	if err != nil && err != context.Canceled {
		s.debugLog("runPairingRequestListener: browse error", "error", err)
	}

	// Wait for cancellation (browse is non-blocking)
	<-ctx.Done()

	// Mark as inactive when browsing stops
	s.mu.Lock()
	s.pairingRequestActive = false
	s.mu.Unlock()
}

// handlePairingRequestDiscovered handles a discovered pairing request.
func (s *DeviceService) handlePairingRequestDiscovered(svc discovery.PairingRequestService, ourDiscriminator uint16) {
	s.debugLog("handlePairingRequestDiscovered: received pairing request",
		"theirDiscriminator", svc.Discriminator,
		"ourDiscriminator", ourDiscriminator,
		"zoneID", svc.ZoneID)

	// Check discriminator match
	if svc.Discriminator != ourDiscriminator {
		s.debugLog("handlePairingRequestDiscovered: discriminator mismatch, ignoring")
		return
	}

	s.mu.RLock()
	// Rate limiting: check if commissioning window is already open
	commissioningOpen := s.discoveryManager != nil && s.discoveryManager.IsCommissioningMode()
	// Check if at max non-TEST zones (TEST zones don't count toward limits)
	atMaxZones := s.nonTestZoneCountLocked() >= s.config.MaxZones
	s.mu.RUnlock()

	if commissioningOpen {
		s.debugLog("handlePairingRequestDiscovered: commissioning window already open, ignoring")
		return
	}

	if atMaxZones {
		s.debugLog("handlePairingRequestDiscovered: at max zones, ignoring")
		return
	}

	// Open commissioning window
	s.debugLog("handlePairingRequestDiscovered: opening commissioning window")
	if err := s.EnterCommissioningMode(); err != nil {
		s.debugLog("handlePairingRequestDiscovered: failed to enter commissioning mode", "error", err)
	}
}

// updatePairingRequestListening updates the listening state based on zone count.
// Called after zone changes to start/stop listening as needed.
func (s *DeviceService) updatePairingRequestListening() {
	if !s.config.ListenForPairingRequests {
		return
	}

	s.mu.RLock()
	zoneCount := s.nonTestZoneCountLocked()
	maxZones := s.config.MaxZones
	active := s.pairingRequestActive
	ctx := s.ctx
	s.mu.RUnlock()

	if zoneCount >= maxZones && active {
		// At max zones - stop listening
		s.debugLog("updatePairingRequestListening: stopping (at max zones)")
		_ = s.StopPairingRequestListening()
	} else if zoneCount < maxZones && !active && ctx != nil {
		// Below max zones and not listening - start
		s.debugLog("updatePairingRequestListening: starting (below max zones)")
		_ = s.StartPairingRequestListening(ctx)
	}
}
