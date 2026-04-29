package service

// Service lifecycle: Start/Stop, TLS cert rotation, ALPN-routed listener, accept loop, client cert vetting, stale connection reaper.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/transport"
)

// Start starts the device service.
func (s *DeviceService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state != StateIdle && s.state != StateStopped {
		s.mu.Unlock()
		return ErrAlreadyStarted
	}
	s.state = StateStarting
	s.mu.Unlock()

	// Resolve listen addresses for backward compatibility.
	s.resolveListenAddresses()

	// Create cancellable context
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Generate server identity for PASE
	// Use a fixed identity for commissioning that both sides agree on
	s.serverID = []byte("mash-device")

	// Generate verifier from setup code + discriminator (DEC-084 §2.3 derives
	// the PBKDF2 salt from those two inputs; identity strings are protocol
	// constants, not parameters).
	setupCode, err := strconv.ParseUint(s.config.SetupCode, 10, 32)
	if err != nil {
		s.mu.Lock()
		s.state = StateIdle
		s.mu.Unlock()
		return err
	}

	s.verifier, err = commissioning.GenerateVerifier(
		commissioning.SetupCode(setupCode),
		s.config.Discriminator,
	)
	if err != nil {
		s.mu.Lock()
		s.state = StateIdle
		s.mu.Unlock()
		return err
	}

	// DEC-067: Generate stable commissioning certificate once at startup.
	// This cert is reused across all commissioning windows.
	s.commissioningCert, err = generateSelfSignedCert(s.config.Discriminator)
	if err != nil {
		s.mu.Lock()
		s.state = StateIdle
		s.mu.Unlock()
		return err
	}

	// Get cert store for loading zone memberships
	s.mu.RLock()
	certStore := s.certStore
	s.mu.RUnlock()

	// Check if we have any zone memberships (i.e., we're commissioned)
	var zones []string
	if certStore != nil {
		zones = certStore.ListZones()
	}

	if len(zones) > 0 {
		// COMMISSIONED: Load operational certs from zones
		firstZone := zones[0]
		opCert, err := certStore.GetOperationalCert(firstZone)
		if err != nil {
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return fmt.Errorf("failed to load operational certificate for zone %s: %w", firstZone, err)
		}

		// Use operational cert for TLS
		s.tlsCert = opCert.TLSCertificate()
		s.deviceID, _ = cert.ExtractDeviceID(opCert.Certificate)

		// Build operational TLS config and start unified listener.
		s.buildOperationalTLSConfig()
		if err := s.ensureListenerStarted(); err != nil {
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return err
		}
	} else {
		// UNCOMMISSIONED: No operational cert yet. The operational listener
		// will be started after the first zone is commissioned.
		s.deviceID = ""
	}

	// Start stale connection reaper if enabled (DEC-064)
	if s.config.StaleConnectionTimeout > 0 {
		go s.runStaleConnectionReaper()
	}

	// Initialize discovery advertiser if not already set (e.g., by tests)
	if s.advertiser == nil {
		advConfig := discovery.DefaultAdvertiserConfig()
		advertiser, err := discovery.NewMDNSAdvertiser(advConfig)
		if err != nil {
			s.stopListener()
			s.mu.Lock()
			s.state = StateIdle
			s.mu.Unlock()
			return err
		}
		s.advertiser = advertiser
		s.discoveryManager = discovery.NewDiscoveryManager(advertiser)

		// Use operational port for commissionable info (same port, ALPN routing).
		commPort := parsePort(s.config.OperationalListenAddress)
		s.discoveryManager.SetCommissionableInfo(&discovery.CommissionableInfo{
			Discriminator: s.config.Discriminator,
			Categories:    s.config.Categories,
			Serial:        s.config.SerialNumber,
			Brand:         s.config.Brand,
			Model:         s.config.Model,
			DeviceName:    s.config.DeviceName,
			Port:          commPort,
		})

		// Set commissioning window duration from config
		if s.config.CommissioningWindowDuration > 0 {
			s.discoveryManager.SetCommissioningWindowDuration(s.config.CommissioningWindowDuration)
		}

		// Register callback for commissioning timeout. The listener
		// stays up for the service lifetime; the ALPN gate is the only
		// state we flip here. Tearing down the kernel listener on
		// timeout used to cause back-to-back conformance tests to see
		// ECONNREFUSED whenever the previous test left the device with
		// zoneCount == 0 -- harmless in production (idle device) but
		// fatal under the suite-driven test cadence.
		s.discoveryManager.OnCommissioningTimeout(func() {
			s.commissioningOpen.Store(false)
			s.emitEvent(Event{
				Type:   EventCommissioningClosed,
				Reason: "timeout",
			})
		})
	}

	s.mu.Lock()
	s.state = StateRunning
	s.mu.Unlock()

	// Start pairing request listening if configured
	if s.config.ListenForPairingRequests {
		_ = s.StartPairingRequestListening(s.ctx)
	}

	return nil
}

// resolveListenAddresses applies backward-compat mapping for ListenAddress.
func (s *DeviceService) resolveListenAddresses() {
	if s.config.ListenAddress != "" {
		if s.config.OperationalListenAddress == "" || s.config.OperationalListenAddress == ":8443" {
			s.config.OperationalListenAddress = s.config.ListenAddress
		}
	}
}

// buildOperationalTLSConfig creates the operational TLS config with RequireAndVerifyClientCert.
// The config includes operational certificates from ALL zones so the device
// can present the correct cert during TLS handshake regardless of which zone
// the controller is reconnecting to.
func (s *DeviceService) buildOperationalTLSConfig() {
	// Build CA pool from known zone CAs for client cert verification.
	caPool := x509.NewCertPool()
	if s.certStore != nil {
		for _, ca := range s.certStore.GetAllZoneCAs() {
			caPool.AddCert(ca)
		}
	}

	// Put the most recently set cert (s.tlsCert) first, then add
	// remaining zone certs. In TLS 1.3 the server cert is sent before
	// the client cert, so Go picks the first cert matching the negotiated
	// signature algorithm. Putting the newest cert first ensures fresh
	// commissions present the correct cert to the reconnecting controller.
	var certs []tls.Certificate
	deviceCertByID := make(map[string]*tls.Certificate)
	addCert := func(tc tls.Certificate) {
		certs = append(certs, tc)
		if deviceID, ok := tlsCertDeviceID(tc); ok {
			certCopy := tc
			deviceCertByID[deviceID] = &certCopy
			deviceCertByID[strings.ToLower(deviceID)] = &certCopy
		}
	}

	hasTLSCert := len(s.tlsCert.Certificate) > 0
	if hasTLSCert {
		addCert(s.tlsCert) // newest cert first
	}
	if s.certStore != nil {
		for _, zoneID := range s.certStore.ListZones() {
			if opCert, err := s.certStore.GetOperationalCert(zoneID); err == nil {
				tc := opCert.TLSCertificate()
				if !hasTLSCert || !sameTLSCert(tc, s.tlsCert) {
					addCert(tc)
				}
			}
		}
	}

	s.operationalTLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: certs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		NextProtos:   []string{transport.ALPNProtocol},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello != nil && hello.ServerName != "" {
				if certByID, ok := deviceCertByID[hello.ServerName]; ok {
					return certByID, nil
				}
				if certByID, ok := deviceCertByID[strings.ToLower(hello.ServerName)]; ok {
					return certByID, nil
				}
			}
			if len(certs) == 0 {
				return nil, fmt.Errorf("no operational certificate configured")
			}
			return &certs[0], nil
		},
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no peer certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("parse peer certificate: %w", err)
			}
			if cert.IsCA {
				return fmt.Errorf("controller certificate has CA:TRUE (must be end-entity)")
			}
			// Check certificate time validity using the device's clock offset
			// (set via TriggerAdjustClockBase). The 300s tolerance matches the
			// spec's clock-skew allowance.
			s.mu.RLock()
			offset := s.clockOffset
			s.mu.RUnlock()
			now := time.Now().Add(offset)
			const clockSkewTolerance = 300 * time.Second
			if now.Before(cert.NotBefore) && cert.NotBefore.Sub(now) > clockSkewTolerance {
				return fmt.Errorf("certificate not yet valid (notBefore=%s, now=%s)", cert.NotBefore.UTC(), now.UTC())
			}
			if now.After(cert.NotAfter) && now.Sub(cert.NotAfter) > clockSkewTolerance {
				return fmt.Errorf("certificate has expired (notAfter=%s, now=%s)", cert.NotAfter.UTC(), now.UTC())
			}
			return nil
		},
	}
}

// sameTLSCert returns true if two tls.Certificate values wrap the same leaf.
func sameTLSCert(a, b tls.Certificate) bool {
	if len(a.Certificate) == 0 || len(b.Certificate) == 0 {
		return false
	}
	return bytes.Equal(a.Certificate[0], b.Certificate[0])
}

func tlsCertDeviceID(c tls.Certificate) (string, bool) {
	if len(c.Certificate) == 0 {
		return "", false
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil || leaf.Subject.CommonName == "" {
		return "", false
	}
	return leaf.Subject.CommonName, true
}

// refreshTLSCert updates s.tlsCert to a remaining zone's operational cert.
// Called after RemoveZone to avoid presenting a stale cert from a removed zone.
// Caller must hold s.mu.Lock.
func (s *DeviceService) refreshTLSCert() {
	if s.certStore == nil {
		return
	}
	for _, zoneID := range s.certStore.ListZones() {
		if opCert, err := s.certStore.GetOperationalCert(zoneID); err == nil {
			s.tlsCert = opCert.TLSCertificate()
			s.buildOperationalTLSConfig()
			return
		}
	}
	// No zones left -- config will be rebuilt on next commissioning.
}

// getConfigForClient is the TLS callback that routes connections based on ALPN.
// Commissioning connections (mash-comm/1) get a NoClientCert config with the
// self-signed commissioning cert. Operational connections (mash/1) get a
// RequireAndVerifyClientCert config with the zone CA-signed cert.
func (s *DeviceService) getConfigForClient(hello *tls.ClientHelloInfo) (*tls.Config, error) {
	wantsComm := false
	for _, proto := range hello.SupportedProtos {
		if proto == transport.ALPNCommissioningProtocol {
			wantsComm = true
			break
		}
	}

	if wantsComm {
		if !s.commissioningOpen.Load() {
			return nil, fmt.Errorf("commissioning not active")
		}
		return &tls.Config{
			MinVersion:   tls.VersionTLS13,
			MaxVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{s.commissioningCert},
			ClientAuth:   tls.NoClientCert,
			NextProtos:   []string{transport.ALPNCommissioningProtocol},
		}, nil
	}

	// Operational path
	if s.operationalTLSConfig == nil {
		return nil, fmt.Errorf("device not commissioned")
	}
	return s.operationalTLSConfig, nil
}

// ensureListenerStarted starts the unified TCP listener if not already running.
func (s *DeviceService) ensureListenerStarted() error {
	if s.listener != nil {
		return nil // Already running
	}
	listener, err := net.Listen("tcp", s.config.OperationalListenAddress)
	if err != nil {
		return fmt.Errorf("listener: %w", err)
	}
	s.listener = listener

	// Base TLS config with GetConfigForClient for ALPN routing.
	baseTLS := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		GetConfigForClient: s.getConfigForClient,
	}

	go s.acceptLoop(listener, baseTLS)
	s.debugLog("ensureListenerStarted: listening", "addr", listener.Addr().String())
	return nil
}

// stopListener closes the unified listener.
func (s *DeviceService) stopListener() {
	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
}

// acceptLoop accepts connections on the unified listener and dispatches
// them through TLS handshake with ALPN-based routing.
func (s *DeviceService) acceptLoop(listener net.Listener, baseTLS *tls.Config) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			// Listener closed or context cancelled.
			return
		}

		// Transport-level connection cap (DEC-062): reject at TCP level before TLS.
		cap := int32(s.config.MaxZones + 1)
		current := s.activeConns.Load()
		if current >= cap {
			s.debugLog("acceptLoop: connection rejected (cap)",
				"activeConns", current, "cap", cap,
				"remoteAddr", conn.RemoteAddr().String())
			_ = conn.Close()
			continue
		}
		s.activeConns.Add(1)
		s.connTracker.Add(conn)

		go s.handleIncomingConnection(conn, baseTLS)
	}
}

// verifyClientCert validates a client certificate against known Zone CAs.
// Returns nil if the certificate is valid, or an error describing why it was rejected.
func (s *DeviceService) verifyClientCert(peerCert *x509.Certificate) error {
	// Build a CA pool from all known Zone CAs.
	caPool := x509.NewCertPool()
	foundCA := false
	if s.certStore != nil {
		for _, ca := range s.certStore.GetAllZoneCAs() {
			caPool.AddCert(ca)
			foundCA = true
		}
	}

	if !foundCA {
		// No Zone CAs known yet -- allow connection (first commissioning
		// may present a self-signed cert before cert exchange completes).
		return nil
	}

	// Read clock offset under lock (may be set by test triggers).
	s.mu.RLock()
	offset := s.clockOffset
	s.mu.RUnlock()

	// Verify the client certificate against known Zone CAs.
	// Apply clock offset (from test triggers) to simulate clock skew.
	now := time.Now().Add(offset)
	opts := x509.VerifyOptions{
		Roots:       caPool,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		CurrentTime: now,
	}
	_, err := peerCert.Verify(opts)
	if err == nil {
		return nil
	}

	// Clock skew tolerance: accept certs whose NotBefore is up to 300s in
	// the future (device clock behind controller), or whose NotAfter is up
	// to 300s in the past (device clock ahead of controller).
	const clockSkewTolerance = 300 * time.Second
	if now.Before(peerCert.NotBefore) && peerCert.NotBefore.Sub(now) <= clockSkewTolerance {
		opts.CurrentTime = peerCert.NotBefore
		if _, err2 := peerCert.Verify(opts); err2 == nil {
			return nil
		}
	}
	if now.After(peerCert.NotAfter) && now.Sub(peerCert.NotAfter) <= clockSkewTolerance {
		opts.CurrentTime = peerCert.NotAfter
		if _, err2 := peerCert.Verify(opts); err2 == nil {
			return nil
		}
	}
	return err
}

// handleIncomingConnection handles TLS handshake for any incoming connection.
// ALPN-based routing (via GetConfigForClient) determines whether this is a
// commissioning or operational connection after the TLS handshake completes.
func (s *DeviceService) handleIncomingConnection(conn net.Conn, baseTLS *tls.Config) {
	activeConnReleased := false
	remoteAddr := conn.RemoteAddr().String()
	defer func() {
		if !activeConnReleased {
			newVal := s.activeConns.Add(-1)
			s.debugLog("handleIncomingConnection: defer released activeConn",
				"activeConns", newVal, "remoteAddr", remoteAddr)
		}
	}()
	releaseActiveConn := func() {
		if !activeConnReleased {
			s.activeConns.Add(-1)
			activeConnReleased = true
		}
	}
	defer s.connTracker.Remove(conn)

	// TLS handshake with per-connection timeout to prevent slowloris-style
	// attacks from holding cap slots indefinitely.
	handshakeCtx := s.ctx
	if s.config.TLSHandshakeTimeout > 0 {
		var cancel context.CancelFunc
		handshakeCtx, cancel = context.WithTimeout(s.ctx, s.config.TLSHandshakeTimeout)
		defer cancel()
	}
	s.debugLog("handleIncomingConnection: starting TLS handshake",
		"timeout", s.config.TLSHandshakeTimeout,
		"remoteAddr", remoteAddr)
	tlsConn := tls.Server(conn, baseTLS)
	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		conn.Close()
		return
	}

	state := tlsConn.ConnectionState()
	if err := transport.VerifyALPN(state); err != nil {
		tlsConn.Close()
		return
	}

	// Route based on negotiated ALPN protocol.
	if state.NegotiatedProtocol == transport.ALPNCommissioningProtocol {
		s.debugLog("handleIncomingConnection: -> commissioning handler", "remoteAddr", remoteAddr)
		s.handleCommissioningConnection(conn, tlsConn, releaseActiveConn)
		return
	}

	// Operational path: verify client certificate.
	if len(state.PeerCertificates) == 0 {
		s.debugLog("handleIncomingConnection: no client certificate", "remoteAddr", remoteAddr)
		tlsConn.Close()
		return
	}
	if len(state.PeerCertificates) > 2 {
		s.debugLog("handleIncomingConnection: certificate chain too long",
			"depth", len(state.PeerCertificates))
		tlsConn.Close()
		return
	}
	if err := s.verifyClientCert(state.PeerCertificates[0]); err != nil {
		s.debugLog("handleIncomingConnection: client cert rejected",
			"cn", state.PeerCertificates[0].Subject.CommonName,
			"err", err)
		tlsConn.Close()
		return
	}

	s.debugLog("handleIncomingConnection: -> operational handler", "remoteAddr", remoteAddr)
	s.handleOperationalConnection(conn, tlsConn, releaseActiveConn)
}

// Stop stops the device service.
func (s *DeviceService) Stop() error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return ErrNotStarted
	}
	s.state = StateStopping
	s.mu.Unlock()

	// Stop pairing request listening
	_ = s.StopPairingRequestListening()

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Close the unified listener.
	s.stopListener()

	// Stop all failsafe timers
	s.mu.Lock()
	if s.disconnectReentryTimer != nil {
		s.disconnectReentryTimer.Stop()
		s.disconnectReentryTimer = nil
	}
	for _, timer := range s.failsafeTimers {
		timer.Reset()
	}
	s.mu.Unlock()

	// Clear subscriptions
	s.subscriptionManager.ClearAll()

	// Stop discovery advertising
	if s.discoveryManager != nil {
		s.discoveryManager.Stop()
	}

	s.mu.Lock()
	s.state = StateStopped
	s.mu.Unlock()

	return nil
}

// runStaleConnectionReaper periodically closes pre-operational connections that
// have exceeded the StaleConnectionTimeout. This is a safety net for connections
// that never complete commissioning (DEC-064).
func (s *DeviceService) runStaleConnectionReaper() {
	ticker := time.NewTicker(s.config.ReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if closed := s.connTracker.CloseStale(s.config.StaleConnectionTimeout); closed > 0 {
				s.debugLog("staleConnectionReaper: closed connections", "count", closed)
			}
		}
	}
}
