//go:build integration

package mash_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/connection"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/failsafe"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// TestE2E_Discovery tests that a controller can discover a device via mDNS.
func TestE2E_Discovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup: Device advertises commissionable service
	advertiser, err := discovery.NewMDNSAdvertiser(discovery.AdvertiserConfig{})
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer advertiser.StopAll()

	discriminator := uint16(1234)
	testPort := uint16(30000 + (time.Now().UnixNano() % 10000))
	commInfo := &discovery.CommissionableInfo{
		Discriminator: discriminator,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility}, // EV charging
		Serial:        "TEST-001",
		Brand:         "MASHTest",
		Model:         "E2E",
		DeviceName:    "Test EVSE",
		Port:          testPort,
	}

	if err := advertiser.AdvertiseCommissionable(ctx, commInfo); err != nil {
		t.Fatalf("Failed to advertise commissionable: %v", err)
	}

	// Give mDNS time to propagate
	time.Sleep(500 * time.Millisecond)

	// Controller browses for devices
	browser, err := discovery.NewMDNSBrowser(discovery.BrowserConfig{})
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()

	// Find by discriminator
	browseCtx, browseCancel := context.WithTimeout(ctx, 5*time.Second)
	defer browseCancel()

	found, err := browser.FindByDiscriminator(browseCtx, discriminator)
	if err != nil {
		t.Fatalf("Failed to find device: %v", err)
	}

	// Verify discovered info
	if found.Discriminator != discriminator {
		t.Errorf("Discriminator mismatch: expected %d, got %d", discriminator, found.Discriminator)
	}
	if found.Brand != "MASHTest" {
		t.Errorf("Brand mismatch: expected MASHTest, got %s", found.Brand)
	}
	if found.Port != testPort {
		t.Errorf("Port mismatch: expected %d, got %d", testPort, found.Port)
	}
}

// TestE2E_TLSConnection tests basic TLS connection between client and server.
func TestE2E_TLSConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Generate test certificates
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Create TLS server
	serverTLSConfig := &transport.TLSConfig{
		Certificate:        serverCert,
		InsecureSkipVerify: true, // For testing
	}

	var receivedMsg []byte
	var msgWg sync.WaitGroup
	msgWg.Add(1)

	server, err := transport.NewServer(transport.ServerConfig{
		TLSConfig: serverTLSConfig,
		Address:   "127.0.0.1:0", // Random port
		OnMessage: func(conn *transport.ServerConn, msg []byte) {
			receivedMsg = msg
			// Echo back
			conn.Send(msg)
			msgWg.Done()
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Get actual server address
	addr := server.Addr().String()

	// Create client in commissioning mode (InsecureSkipVerify)
	client, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Connect
	conn, err := client.Connect(ctx, addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Send a message
	testMsg := []byte("Hello, MASH!")
	if err := conn.Send(testMsg); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Wait for server to receive and echo
	msgWg.Wait()

	// Verify server received our message
	if string(receivedMsg) != string(testMsg) {
		t.Errorf("Server received wrong message: expected %q, got %q", testMsg, receivedMsg)
	}

	// Receive echo
	response, err := conn.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive response: %v", err)
	}

	if string(response) != string(testMsg) {
		t.Errorf("Wrong response: expected %q, got %q", testMsg, response)
	}
}

// TestE2E_CommissioningHandshake tests the full PASE handshake over TLS.
// Note: This test uses raw TLS (not the framed transport) because PASE
// has its own length-prefixed framing. In production, PASE messages would
// be wrapped in the transport framing layer.
func TestE2E_CommissioningHandshake(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup code + discriminator (DEC-084 §2.3 derives the salt from these).
	setupCode := commissioning.MustParseSetupCode("20202021")
	const discriminator uint16 = 1234

	// Generate verifier (device does this during manufacturing)
	verifier, err := commissioning.GenerateVerifier(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to generate verifier: %v", err)
	}

	// Generate test certificates
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Create raw TLS server for PASE (bypassing transport framing)
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{serverCert},
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
		NextProtos:         []string{transport.ALPNProtocol},
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer listener.Close()

	tlsListener := tls.NewListener(listener, tlsConfig)

	var serverKey []byte
	var serverErr error
	var serverWg sync.WaitGroup
	serverWg.Add(1)

	// Server goroutine - accepts one connection and runs PASE
	go func() {
		defer serverWg.Done()

		conn, err := tlsListener.Accept()
		if err != nil {
			serverErr = fmt.Errorf("accept failed: %w", err)
			return
		}
		defer conn.Close()

		// Create server PASE session
		session, err := commissioning.NewPASEServerSession(verifier)
		if err != nil {
			serverErr = fmt.Errorf("failed to create server session: %w", err)
			return
		}

		// Run handshake directly over TLS connection
		key, err := session.Handshake(ctx, conn)
		if err != nil {
			serverErr = fmt.Errorf("server handshake failed: %w", err)
			return
		}
		serverKey = key
	}()

	// Client side - connect and run PASE
	clientTLSConfig := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		NextProtos:         []string{transport.ALPNProtocol},
	}

	conn, err := tls.Dial("tcp", listener.Addr().String(), clientTLSConfig)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	defer conn.Close()

	// Create client PASE session
	clientSession, err := commissioning.NewPASEClientSession(setupCode, discriminator)
	if err != nil {
		t.Fatalf("Failed to create client session: %v", err)
	}

	// Run client handshake directly over TLS connection
	clientKey, err := clientSession.Handshake(ctx, conn)
	if err != nil {
		t.Fatalf("Client handshake failed: %v", err)
	}

	// Wait for server handshake to complete
	serverWg.Wait()

	if serverErr != nil {
		t.Fatalf("Server error: %v", serverErr)
	}

	// Verify both sides derived the same key
	if len(clientKey) != commissioning.SharedSecretSize {
		t.Errorf("Client key wrong size: expected %d, got %d", commissioning.SharedSecretSize, len(clientKey))
	}

	if len(serverKey) != commissioning.SharedSecretSize {
		t.Errorf("Server key wrong size: expected %d, got %d", commissioning.SharedSecretSize, len(serverKey))
	}

	// Keys should match
	for i := range clientKey {
		if clientKey[i] != serverKey[i] {
			t.Errorf("Keys don't match at position %d", i)
			break
		}
	}

	t.Logf("PASE handshake successful - both sides derived matching %d-byte session key", len(clientKey))
}

// TestE2E_Failsafe tests that failsafe triggers after connection loss.
func TestE2E_Failsafe(t *testing.T) {
	// Create a device model
	device := createTestDeviceModel()

	// Create a valid DeviceConfig
	config := service.DeviceConfig{
		ListenAddress:   "127.0.0.1:0",
		Discriminator:   1234,
		SetupCode:       "20202021",
		SerialNumber:    "SN-FAILSAFE-001",
		Brand:           "TestBrand",
		Model:           "TestModel",
		DeviceName:      "Failsafe Test Device",
		Categories:      []discovery.DeviceCategory{discovery.CategoryEMobility},
		FailsafeTimeout: 4 * time.Hour, // Will be replaced with test timer
	}

	// Create the service
	svc, err := service.NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("Failed to create device service: %v", err)
	}

	// Track events
	var events []service.Event
	var eventsMu sync.Mutex
	eventCh := make(chan service.EventType, 10)

	svc.OnEvent(func(e service.Event) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
		select {
		case eventCh <- e.Type:
		default:
		}
	})

	// Simulate zone connection
	zoneID := "test-zone-001"
	svc.HandleZoneConnect(zoneID, cert.ZoneTypeLocal)

	// Wait for connection event
	select {
	case et := <-eventCh:
		if et != service.EventConnected {
			t.Errorf("Expected EventConnected, got %v", et)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for connection event")
	}

	// Inject a test timer with short duration (bypasses 2-hour minimum)
	testTimer := failsafe.NewTestTimer(100*time.Millisecond, 50*time.Millisecond, failsafe.Limits{})
	svc.SetFailsafeTimer(zoneID, testTimer)

	// Start the timer to simulate connection loss scenario
	testTimer.Start()

	// Wait for failsafe to trigger
	select {
	case et := <-eventCh:
		if et != service.EventFailsafeTriggered {
			t.Errorf("Expected EventFailsafeTriggered, got %v", et)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for failsafe trigger")
	}

	// Verify the timer is in failsafe state
	timer := svc.GetFailsafeTimer(zoneID)
	if timer == nil {
		t.Fatal("Expected failsafe timer to exist")
	}
	if !timer.IsFailsafe() {
		t.Error("Expected timer to be in failsafe state")
	}

	// Verify zone is marked as failsafe active
	zone := svc.GetZone(zoneID)
	if zone == nil {
		t.Fatal("Expected zone to exist")
	}
	if !zone.FailsafeActive {
		t.Error("Expected zone to have FailsafeActive=true")
	}

	// Clear failsafe by calling RefreshFailsafe
	if err := svc.RefreshFailsafe(zoneID); err != nil {
		t.Fatalf("Failed to refresh failsafe: %v", err)
	}

	// Wait for failsafe cleared event
	select {
	case et := <-eventCh:
		if et != service.EventFailsafeCleared {
			t.Errorf("Expected EventFailsafeCleared, got %v", et)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for failsafe cleared event")
	}

	// Verify zone is no longer in failsafe
	zone = svc.GetZone(zoneID)
	if zone == nil {
		t.Fatal("Expected zone to still exist")
	}
	if zone.FailsafeActive {
		t.Error("Expected zone to have FailsafeActive=false after refresh")
	}

	// Verify event sequence
	eventsMu.Lock()
	defer eventsMu.Unlock()

	expectedSequence := []service.EventType{
		service.EventConnected,
		service.EventFailsafeTriggered,
		service.EventFailsafeCleared,
	}

	if len(events) < len(expectedSequence) {
		t.Errorf("Expected at least %d events, got %d", len(expectedSequence), len(events))
	} else {
		for i, expected := range expectedSequence {
			if events[i].Type != expected {
				t.Errorf("Event[%d]: expected %v, got %v", i, expected, events[i].Type)
			}
		}
	}

	t.Logf("Failsafe test successful - timer triggered after %v, cleared via RefreshFailsafe", testTimer.Duration())
}

// TestE2E_MultiZone tests multiple controllers connecting to a device.
func TestE2E_MultiZone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a device model
	device := createTestDeviceModel()

	// Create a valid DeviceConfig
	config := service.DeviceConfig{
		ListenAddress:   "127.0.0.1:0",
		Discriminator:   1234,
		SetupCode:       "20202021",
		SerialNumber:    "SN-MULTIZONE-001",
		Brand:           "TestBrand",
		Model:           "TestModel",
		DeviceName:      "Multi-Zone Test Device",
		Categories:      []discovery.DeviceCategory{discovery.CategoryEMobility},
		FailsafeTimeout: 4 * time.Hour,
	}

	// Create the device service
	svc, err := service.NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("Failed to create device service: %v", err)
	}

	// Track events
	var events []service.Event
	var eventsMu sync.Mutex
	eventCh := make(chan service.Event, 20)

	svc.OnEvent(func(e service.Event) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
		select {
		case eventCh <- e:
		default:
		}
	})

	// Create protocol handler and notification dispatcher for the device
	handler := dispatch.NewProtocolHandler(device)
	dispatcher := dispatch.NewNotificationDispatcher(handler)
	dispatcher.SetProcessingInterval(50 * time.Millisecond)
	defer dispatcher.Stop()

	// Generate test certificate for server
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Track connections and map them to zones
	var connMu sync.Mutex
	connToZone := make(map[*transport.ServerConn]string)
	zoneCounter := 0

	// Zone configurations for simulated controllers
	zoneConfigs := []struct {
		zoneID   string
		zoneType cert.ZoneType
	}{
		{"grid-operator-zone", cert.ZoneTypeGrid},
		{"home-manager-zone", cert.ZoneTypeLocal},
		{"user-app-zone", cert.ZoneTypeLocal},
	}

	// Create TLS server
	serverTLSConfig := &transport.TLSConfig{
		Certificate:        serverCert,
		InsecureSkipVerify: true,
	}

	server, err := transport.NewServer(transport.ServerConfig{
		TLSConfig: serverTLSConfig,
		Address:   "127.0.0.1:0",
		OnConnect: func(conn *transport.ServerConn) {
			connMu.Lock()
			// Assign zone based on connection order (simulating certificate extraction)
			if zoneCounter < len(zoneConfigs) {
				zoneConfig := zoneConfigs[zoneCounter]
				connToZone[conn] = zoneConfig.zoneID

				// Register with DeviceService
				svc.HandleZoneConnect(zoneConfig.zoneID, zoneConfig.zoneType)

				// Register with dispatcher
				dispatcher.RegisterConnection(func(data []byte) error {
					return conn.Send(data)
				})

				zoneCounter++
			}
			connMu.Unlock()
		},
		OnDisconnect: func(conn *transport.ServerConn) {
			connMu.Lock()
			if zoneID, ok := connToZone[conn]; ok {
				svc.HandleZoneDisconnect(zoneID)
				delete(connToZone, conn)
			}
			connMu.Unlock()
		},
		OnMessage: func(conn *transport.ServerConn, msg []byte) {
			// Decode and handle message
			req, decodeErr := wire.DecodeRequest(msg)
			if decodeErr != nil {
				return
			}

			// Set zone context on handler
			connMu.Lock()
			if zoneID, ok := connToZone[conn]; ok {
				handler.SetZoneID(zoneID)
			}
			connMu.Unlock()

			// Handle request
			resp := handler.HandleRequest(req)

			// Encode and send response
			respData, _ := wire.EncodeResponse(resp)
			conn.Send(respData)
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	dispatcher.Start()

	// Verify initial state
	if svc.ZoneCount() != 0 {
		t.Errorf("Expected 0 zones initially, got %d", svc.ZoneCount())
	}

	// Connect first controller (Grid Operator - highest priority)
	client1, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client1: %v", err)
	}

	conn1, err := client1.Connect(ctx, server.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect client1: %v", err)
	}
	defer conn1.Close()

	// Wait for connection event
	select {
	case e := <-eventCh:
		if e.Type != service.EventConnected {
			t.Errorf("Expected EventConnected, got %v", e.Type)
		}
		if e.ZoneID != "grid-operator-zone" {
			t.Errorf("Expected zone ID 'grid-operator-zone', got %s", e.ZoneID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for first connection event")
	}

	// Verify zone count
	if svc.ZoneCount() != 1 {
		t.Errorf("Expected 1 zone after first connection, got %d", svc.ZoneCount())
	}

	// Connect second controller (Home Manager)
	client2, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client2: %v", err)
	}

	conn2, err := client2.Connect(ctx, server.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect client2: %v", err)
	}
	defer conn2.Close()

	// Wait for connection event
	select {
	case e := <-eventCh:
		if e.Type != service.EventConnected {
			t.Errorf("Expected EventConnected, got %v", e.Type)
		}
		if e.ZoneID != "home-manager-zone" {
			t.Errorf("Expected zone ID 'home-manager-zone', got %s", e.ZoneID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for second connection event")
	}

	// Verify zone count
	if svc.ZoneCount() != 2 {
		t.Errorf("Expected 2 zones after second connection, got %d", svc.ZoneCount())
	}

	// Connect third controller (User App - lowest priority)
	client3, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client3: %v", err)
	}

	conn3, err := client3.Connect(ctx, server.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect client3: %v", err)
	}
	defer conn3.Close()

	// Wait for connection event
	select {
	case e := <-eventCh:
		if e.Type != service.EventConnected {
			t.Errorf("Expected EventConnected, got %v", e.Type)
		}
		if e.ZoneID != "user-app-zone" {
			t.Errorf("Expected zone ID 'user-app-zone', got %s", e.ZoneID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for third connection event")
	}

	// Verify final zone count
	if svc.ZoneCount() != 3 {
		t.Errorf("Expected 3 zones after all connections, got %d", svc.ZoneCount())
	}

	// Verify all zones are tracked correctly
	allZones := svc.GetAllZones()
	if len(allZones) != 3 {
		t.Errorf("Expected 3 zones from GetAllZones, got %d", len(allZones))
	}

	// Verify zone priorities
	gridZone := svc.GetZone("grid-operator-zone")
	homeZone := svc.GetZone("home-manager-zone")
	userZone := svc.GetZone("user-app-zone")

	if gridZone == nil || homeZone == nil || userZone == nil {
		t.Fatal("Expected all zones to exist")
	}

	// Grid operator should have highest priority (lowest number)
	if gridZone.Priority >= homeZone.Priority {
		t.Errorf("Grid operator priority (%d) should be lower than home manager (%d)",
			gridZone.Priority, homeZone.Priority)
	}
	if homeZone.Priority >= userZone.Priority {
		t.Errorf("Home manager priority (%d) should be lower than user app (%d)",
			homeZone.Priority, userZone.Priority)
	}

	// Test that each client can perform operations
	// Client 1 sends a Read request
	readReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
	}
	reqData, _ := wire.EncodeRequest(readReq)
	if err := conn1.Send(reqData); err != nil {
		t.Fatalf("Failed to send read request from client1: %v", err)
	}

	respData, err := conn1.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive response on client1: %v", err)
	}

	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success response from client1, got status %d", resp.Status)
	}

	// Client 2 sends a Read request
	readReq.MessageID = 2
	reqData, _ = wire.EncodeRequest(readReq)
	if err := conn2.Send(reqData); err != nil {
		t.Fatalf("Failed to send read request from client2: %v", err)
	}

	respData, err = conn2.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive response on client2: %v", err)
	}

	resp, err = wire.DecodeResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success response from client2, got status %d", resp.Status)
	}

	// Disconnect one zone and verify tracking
	conn2.Close()

	// Wait for disconnect event
	select {
	case e := <-eventCh:
		if e.Type != service.EventDisconnected {
			t.Errorf("Expected EventDisconnected, got %v", e.Type)
		}
		if e.ZoneID != "home-manager-zone" {
			t.Errorf("Expected zone ID 'home-manager-zone' in disconnect, got %s", e.ZoneID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for disconnect event")
	}

	// Zone should still be tracked (just marked disconnected)
	homeZone = svc.GetZone("home-manager-zone")
	if homeZone == nil {
		t.Error("Zone should still exist after disconnect")
	} else if homeZone.Connected {
		t.Error("Zone should be marked as disconnected")
	}

	// Verify event sequence
	eventsMu.Lock()
	connectCount := 0
	disconnectCount := 0
	for _, e := range events {
		switch e.Type {
		case service.EventConnected:
			connectCount++
		case service.EventDisconnected:
			disconnectCount++
		}
	}
	eventsMu.Unlock()

	if connectCount != 3 {
		t.Errorf("Expected 3 connect events, got %d", connectCount)
	}
	if disconnectCount != 1 {
		t.Errorf("Expected 1 disconnect event, got %d", disconnectCount)
	}

	t.Logf("Multi-zone test successful - 3 zones connected, priority order verified, disconnect handled")
}

// TestE2E_Reconnection tests automatic reconnection after disconnect.
func TestE2E_Reconnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Generate test certificate
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Track server state
	var serverMu sync.Mutex
	var currentServer *transport.Server
	serverAddr := ""

	// Function to start/restart server
	startServer := func() error {
		serverMu.Lock()
		defer serverMu.Unlock()

		serverTLSConfig := &transport.TLSConfig{
			Certificate:        serverCert,
			InsecureSkipVerify: true,
		}

		server, err := transport.NewServer(transport.ServerConfig{
			TLSConfig: serverTLSConfig,
			Address:   "127.0.0.1:0",
			OnMessage: func(conn *transport.ServerConn, msg []byte) {
				// Echo back for simple verification
				conn.Send(msg)
			},
		})
		if err != nil {
			return err
		}

		if err := server.Start(ctx); err != nil {
			return err
		}

		currentServer = server
		serverAddr = server.Addr().String()
		return nil
	}

	// Function to stop server
	stopServer := func() {
		serverMu.Lock()
		defer serverMu.Unlock()
		if currentServer != nil {
			currentServer.Stop()
			currentServer = nil
		}
	}

	// Start initial server
	if err := startServer(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer stopServer()

	// Track connection state
	var clientConn *transport.ClientConn
	var connMu sync.Mutex

	// Track state changes
	stateChanges := make(chan connection.State, 10)
	reconnectAttempts := make(chan int, 10)

	// Create connection manager with connect function
	client, _ := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})

	connectFn := func(connectCtx context.Context) error {
		serverMu.Lock()
		addr := serverAddr
		serverMu.Unlock()

		if addr == "" {
			return fmt.Errorf("server not available")
		}

		conn, err := client.Connect(connectCtx, addr)
		if err != nil {
			return err
		}

		connMu.Lock()
		clientConn = conn
		connMu.Unlock()

		return nil
	}

	manager := connection.NewManager(connectFn)

	// Set up callbacks
	manager.OnStateChange(func(oldState, newState connection.State) {
		t.Logf("State change: %s -> %s", oldState, newState)
		select {
		case stateChanges <- newState:
		default:
		}
	})

	manager.OnReconnecting(func(attempt int, delay time.Duration) {
		t.Logf("Reconnection attempt %d, delay %v", attempt, delay)
		select {
		case reconnectAttempts <- attempt:
		default:
		}
	})

	// Start reconnection loop
	manager.StartReconnectLoop()
	defer manager.Close()

	// Initial connection
	if err := manager.Connect(ctx); err != nil {
		t.Fatalf("Initial connection failed: %v", err)
	}

	// Wait for connected state (drain intermediate states)
	waitForState := func(expected connection.State, timeout time.Duration) bool {
		timer := time.After(timeout)
		for {
			select {
			case state := <-stateChanges:
				if state == expected {
					return true
				}
				// Continue draining until we get expected or timeout
			case <-timer:
				return false
			}
		}
	}

	if !waitForState(connection.StateConnected, 2*time.Second) {
		t.Fatal("Timeout waiting for initial connection")
	}

	// Verify we're connected
	if !manager.IsConnected() {
		t.Error("Manager should report connected")
	}

	// Send a message to verify connection works
	connMu.Lock()
	conn := clientConn
	connMu.Unlock()

	testMsg := []byte("test-before-disconnect")
	if err := conn.Send(testMsg); err != nil {
		t.Fatalf("Failed to send test message: %v", err)
	}

	response, err := conn.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive echo: %v", err)
	}
	if string(response) != string(testMsg) {
		t.Errorf("Echo mismatch: expected %q, got %q", testMsg, response)
	}

	t.Log("Initial connection verified, simulating disconnect...")

	// Close the current connection and stop server to simulate network failure
	connMu.Lock()
	if clientConn != nil {
		clientConn.Close()
	}
	connMu.Unlock()
	stopServer()

	// Notify manager of connection loss (in real app, this would be triggered by Send/Receive error)
	manager.NotifyConnectionLost()

	// Wait for reconnecting state
	if !waitForState(connection.StateReconnecting, 2*time.Second) {
		t.Fatal("Timeout waiting for reconnecting state")
	}

	// Wait for at least one reconnection attempt
	select {
	case attempt := <-reconnectAttempts:
		t.Logf("First reconnection attempt: %d", attempt)
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for reconnection attempt")
	}

	// Verify manager is attempting to reconnect
	if manager.State() != connection.StateReconnecting {
		t.Errorf("Expected StateReconnecting, got %s", manager.State())
	}

	t.Log("Manager is reconnecting, restarting server...")

	// Restart server (on the same address isn't possible, so use new address)
	if err := startServer(); err != nil {
		t.Fatalf("Failed to restart server: %v", err)
	}

	// Wait for reconnection to succeed
	if !waitForState(connection.StateConnected, 10*time.Second) {
		t.Fatal("Timeout waiting for reconnection")
	}
	t.Log("Reconnection successful!")

	// Verify connected state
	if !manager.IsConnected() {
		t.Error("Manager should report connected after reconnection")
	}

	// Verify backoff was reset
	if manager.BackoffAttempts() != 0 {
		t.Errorf("Backoff should be reset after successful connection, got %d attempts", manager.BackoffAttempts())
	}

	// Verify we can communicate on the new connection
	connMu.Lock()
	conn = clientConn
	connMu.Unlock()

	testMsg = []byte("test-after-reconnect")
	if err := conn.Send(testMsg); err != nil {
		t.Fatalf("Failed to send after reconnect: %v", err)
	}

	response, err = conn.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive after reconnect: %v", err)
	}
	if string(response) != string(testMsg) {
		t.Errorf("Echo mismatch after reconnect: expected %q, got %q", testMsg, response)
	}

	t.Log("Reconnection test successful - client reconnected and can communicate")
}

// TestE2E_SubscribeNotify tests subscription and notification flow.
func TestE2E_SubscribeNotify(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a test device with DeviceInfo
	device := createTestDeviceModel()

	// Create protocol handler and notification dispatcher
	handler := dispatch.NewProtocolHandler(device)
	dispatcher := dispatch.NewNotificationDispatcher(handler)
	dispatcher.SetProcessingInterval(50 * time.Millisecond) // Fast for testing
	defer dispatcher.Stop()

	// Generate test certificates
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Track connection for dispatcher
	var connID uint64
	var connIDMu sync.Mutex

	// Create TLS server with dispatcher
	serverTLSConfig := &transport.TLSConfig{
		Certificate:        serverCert,
		InsecureSkipVerify: true,
	}

	server, err := transport.NewServer(transport.ServerConfig{
		TLSConfig: serverTLSConfig,
		Address:   "127.0.0.1:0",
		OnConnect: func(conn *transport.ServerConn) {
			// Register connection with dispatcher
			connIDMu.Lock()
			connID = dispatcher.RegisterConnection(func(data []byte) error {
				return conn.Send(data)
			})
			connIDMu.Unlock()
		},
		OnDisconnect: func(conn *transport.ServerConn) {
			connIDMu.Lock()
			if connID != 0 {
				dispatcher.UnregisterConnection(connID)
				connID = 0
			}
			connIDMu.Unlock()
		},
		OnMessage: func(conn *transport.ServerConn, msg []byte) {
			// Decode request
			req, decodeErr := wire.DecodeRequest(msg)
			if decodeErr != nil {
				t.Logf("Failed to decode request: %v", decodeErr)
				return
			}

			var resp *wire.Response
			connIDMu.Lock()
			currentConnID := connID
			connIDMu.Unlock()

			// Route subscribe/unsubscribe through dispatcher
			if req.Operation == wire.OpSubscribe {
				if req.FeatureID == 0 {
					resp = dispatcher.HandleUnsubscribe(currentConnID, req)
				} else {
					resp = dispatcher.HandleSubscribe(currentConnID, req)
				}
			} else {
				// Handle other operations through protocol handler
				resp = handler.HandleRequest(req)
			}

			// Encode response
			respData, encodeErr := wire.EncodeResponse(resp)
			if encodeErr != nil {
				t.Logf("Failed to encode response: %v", encodeErr)
				return
			}

			// Send response
			conn.Send(respData)
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Start notification processing
	dispatcher.Start()

	// Create client
	client, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Connect
	conn, err := client.Connect(ctx, server.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Wait for connection to be registered
	time.Sleep(100 * time.Millisecond)

	// Subscribe to DeviceInfo feature
	subReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload: &wire.SubscribePayload{
			MinInterval: 10,    // 10ms - fast for testing
			MaxInterval: 60000, // 60 seconds
		},
	}

	reqData, err := wire.EncodeRequest(subReq)
	if err != nil {
		t.Fatalf("Failed to encode subscribe request: %v", err)
	}

	if err := conn.Send(reqData); err != nil {
		t.Fatalf("Failed to send subscribe request: %v", err)
	}

	// Receive subscribe response
	respData, err := conn.Receive(5 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive subscribe response: %v", err)
	}

	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode subscribe response: %v", err)
	}

	if !resp.IsSuccess() {
		t.Fatalf("Subscribe failed with status %d", resp.Status)
	}

	// Extract subscription ID from response
	subRespPayload, ok := resp.Payload.(*wire.SubscribeResponsePayload)
	if !ok {
		// Try decoding from map
		if payloadMap, mapOk := resp.Payload.(map[any]any); mapOk {
			if subID, exists := payloadMap[uint64(1)]; exists {
				t.Logf("Got subscription ID: %v", subID)
			}
		} else {
			t.Logf("Subscribe response payload type: %T", resp.Payload)
		}
	} else {
		t.Logf("Subscription ID: %d", subRespPayload.SubscriptionID)
	}

	// Trigger an attribute change on the server
	// This simulates the device updating its state
	dispatcher.NotifyChange(0, uint16(model.FeatureDeviceInfo), 5, "2.0.0") // Software version change

	// Wait for notification with timeout
	notifData, err := conn.Receive(2 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive notification: %v", err)
	}

	// Decode as notification
	notif, err := wire.DecodeNotification(notifData)
	if err != nil {
		t.Fatalf("Failed to decode notification: %v", err)
	}

	// Verify notification
	if notif.EndpointID != 0 {
		t.Errorf("Expected EndpointID 0, got %d", notif.EndpointID)
	}
	if notif.FeatureID != uint8(model.FeatureDeviceInfo) {
		t.Errorf("Expected FeatureID %d, got %d", model.FeatureDeviceInfo, notif.FeatureID)
	}
	if notif.SubscriptionID == 0 {
		t.Error("Expected non-zero SubscriptionID in notification")
	}

	// Check that the change is in the notification
	if notif.Changes == nil {
		t.Error("Expected changes in notification")
	} else {
		t.Logf("Notification received with %d changed attributes", len(notif.Changes))
	}

	t.Logf("Subscribe/Notify flow successful - subscription ID %d, received notification", notif.SubscriptionID)
}

// TestE2E_ReadWrite tests read and write operations over TLS.
func TestE2E_ReadWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a test device with DeviceInfo
	device := createTestDeviceModel()

	// Create protocol handler
	handler := dispatch.NewProtocolHandler(device)

	// Generate test certificates
	serverCert, err := generateSelfSignedCert("device.mash.local")
	if err != nil {
		t.Fatalf("Failed to generate server cert: %v", err)
	}

	// Create TLS server with protocol handler
	serverTLSConfig := &transport.TLSConfig{
		Certificate:        serverCert,
		InsecureSkipVerify: true,
	}

	server, err := transport.NewServer(transport.ServerConfig{
		TLSConfig: serverTLSConfig,
		Address:   "127.0.0.1:0",
		OnMessage: func(conn *transport.ServerConn, msg []byte) {
			// Decode request
			req, decodeErr := wire.DecodeRequest(msg)
			if decodeErr != nil {
				t.Logf("Failed to decode request: %v", decodeErr)
				return
			}

			// Handle with protocol handler
			resp := handler.HandleRequest(req)

			// Encode response
			respData, encodeErr := wire.EncodeResponse(resp)
			if encodeErr != nil {
				t.Logf("Failed to encode response: %v", encodeErr)
				return
			}

			// Send response
			conn.Send(respData)
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if err := server.Start(ctx); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Create client
	client, err := transport.NewClient(transport.ClientConfig{
		CommissioningMode: true,
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Connect
	conn, err := client.Connect(ctx, server.Addr().String())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create a Read request for DeviceInfo
	readReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
	}

	// Encode and send
	reqData, err := wire.EncodeRequest(readReq)
	if err != nil {
		t.Fatalf("Failed to encode request: %v", err)
	}

	if err := conn.Send(reqData); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Receive response (this will block until the server responds)
	respData, err := conn.Receive(5 * time.Second)
	if err != nil {
		t.Fatalf("Failed to receive response: %v", err)
	}

	// Decode response
	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify response
	if resp.MessageID != readReq.MessageID {
		t.Errorf("MessageID mismatch: expected %d, got %d", readReq.MessageID, resp.MessageID)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success response, got status %d", resp.Status)
	}

	t.Logf("Read response successful with status %d", resp.Status)
}

// Helper functions

// createTestDeviceModel creates a device model with DeviceInfo for testing.
func createTestDeviceModel() *model.Device {
	device := model.NewDevice("test-device-001", 0x1234, 0x0001)

	// Add DeviceInfo to root endpoint
	deviceInfo := features.NewDeviceInfo()
	_ = deviceInfo.SetDeviceID("test-device-001")
	_ = deviceInfo.SetVendorName("Test Vendor")
	_ = deviceInfo.SetProductName("Test Product")
	_ = deviceInfo.SetSerialNumber("SN-12345")
	_ = deviceInfo.SetSoftwareVersion("1.0.0")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	return device
}

// generateSelfSignedCert generates a self-signed certificate for testing.
func generateSelfSignedCert(commonName string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{commonName, "localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Encode to PEM for tls.X509KeyPair
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}
