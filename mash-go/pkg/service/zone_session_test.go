package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// createTestDevice creates a device with DeviceInfo feature for testing.
func createTestDevice() *model.Device {
	device := model.NewDevice("test-device", 0x1234, 0x5678)

	// Get endpoint 0 (DEVICE_ROOT) and add DeviceInfo feature
	endpoint, _ := device.GetEndpoint(0)
	deviceInfo := model.NewFeature(model.FeatureDeviceInfo, 1)
	endpoint.AddFeature(deviceInfo)

	return device
}

// mockSendableConnection implements Sendable and tracks sent messages.
type mockSendableConnection struct {
	mu      sync.Mutex
	sent    [][]byte
	sendErr error
	closed  bool
}

func newMockSendableConnection() *mockSendableConnection {
	return &mockSendableConnection{}
}

func (m *mockSendableConnection) Send(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, data)
	return nil
}

func (m *mockSendableConnection) SentMessages() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]byte, len(m.sent))
	copy(result, m.sent)
	return result
}

func (m *mockSendableConnection) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErr = err
}

func TestZoneSession_HandleReadRequest(t *testing.T) {
	// Create a device with a feature
	device := createTestDevice()

	// Create mock connection
	conn := newMockSendableConnection()

	// Create zone session
	session := NewZoneSession("zone-1", conn, device)

	// Create a read request for DeviceInfo (endpoint 0, feature 6)
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo), // DeviceInfo feature = 6
		Payload:    nil,                            // Read all attributes
	}

	// Encode the request
	reqData, err := wire.EncodeRequest(req)
	if err != nil {
		t.Fatalf("Failed to encode request: %v", err)
	}

	// Handle the message
	session.OnMessage(reqData)

	// Wait for response to be sent
	time.Sleep(50 * time.Millisecond)

	// Check that a response was sent
	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(sent))
	}

	// Decode the response
	resp, err := wire.DecodeResponse(sent[0])
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.MessageID != req.MessageID {
		t.Errorf("Response MessageID mismatch: got %d, want %d", resp.MessageID, req.MessageID)
	}

	if resp.Status != wire.StatusSuccess {
		t.Errorf("Expected success status, got %v", resp.Status)
	}

	// Payload should contain device attributes
	if resp.Payload == nil {
		t.Error("Expected payload with attributes")
	}
}

func TestZoneSession_HandleReadRequest_InvalidEndpoint(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Request for non-existent endpoint
	req := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpRead,
		EndpointID: 99, // Non-existent
		FeatureID:  uint8(model.FeatureDeviceInfo),
	}

	reqData, _ := wire.EncodeRequest(req)
	session.OnMessage(reqData)

	time.Sleep(50 * time.Millisecond)

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(sent))
	}

	resp, _ := wire.DecodeResponse(sent[0])
	if resp.Status != wire.StatusInvalidEndpoint {
		t.Errorf("Expected InvalidEndpoint status, got %v", resp.Status)
	}
}

func TestZoneSession_HandleWriteRequest(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Write request - note: most DeviceInfo attributes are read-only
	// This test verifies the flow even if the write is rejected
	req := &wire.Request{
		MessageID:  3,
		Operation:  wire.OpWrite,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload:    wire.WritePayload{1: "new-value"},
	}

	reqData, _ := wire.EncodeRequest(req)
	session.OnMessage(reqData)

	time.Sleep(50 * time.Millisecond)

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(sent))
	}

	resp, _ := wire.DecodeResponse(sent[0])
	// DeviceInfo attributes are read-only, so we expect an error
	if resp.Status == wire.StatusSuccess {
		t.Log("Write unexpectedly succeeded")
	}
	// The important thing is we got a valid response
	if resp.MessageID != req.MessageID {
		t.Errorf("Response MessageID mismatch: got %d, want %d", resp.MessageID, req.MessageID)
	}
}

func TestZoneSession_InvokeResponseWaitsForCommandCompletion(t *testing.T) {
	device := createTestDevice()
	endpoint, _ := device.GetEndpoint(0)
	feature, err := endpoint.GetFeatureByID(uint8(model.FeatureDeviceInfo))
	if err != nil {
		t.Fatalf("GetFeatureByID failed: %v", err)
	}

	const slowCmdID uint8 = 99
	feature.AddCommand(model.NewCommand(&model.CommandMetadata{
		ID:   slowCmdID,
		Name: "SlowCommand",
	}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		time.Sleep(700 * time.Millisecond)
		return map[uint8]any{1: true}, nil
	}))

	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	req := &wire.Request{
		MessageID:  99,
		Operation:  wire.OpInvoke,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload: &wire.InvokePayload{
			CommandID:  slowCmdID,
			Parameters: map[uint8]any{},
		},
	}
	reqData, err := wire.EncodeRequest(req)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		session.OnMessage(reqData)
		close(done)
	}()

	time.Sleep(550 * time.Millisecond)
	if got := len(conn.SentMessages()); got != 0 {
		t.Fatalf("expected no response before command completion, got %d", got)
	}

	<-done
	if elapsed := time.Since(start); elapsed < 650*time.Millisecond {
		t.Fatalf("expected invoke handling to block on command, elapsed=%v", elapsed)
	}

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected exactly one response after completion, got %d", len(sent))
	}
	resp, err := wire.DecodeResponse(sent[0])
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}
	if resp.Status != wire.StatusSuccess {
		t.Fatalf("expected success status, got %v", resp.Status)
	}
}

func TestZoneSession_HandleSubscribeRequest(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Subscribe request
	req := &wire.Request{
		MessageID:  4,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload:    &wire.SubscribePayload{},
	}

	reqData, _ := wire.EncodeRequest(req)
	session.OnMessage(reqData)

	time.Sleep(50 * time.Millisecond)

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(sent))
	}

	resp, _ := wire.DecodeResponse(sent[0])
	if resp.Status != wire.StatusSuccess {
		t.Errorf("Expected success status, got %v", resp.Status)
	}

	// Check subscription count
	if session.SubscriptionCount() != 1 {
		t.Errorf("Expected 1 subscription, got %d", session.SubscriptionCount())
	}
}

func TestZoneSession_Close(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Create a subscription first
	req := &wire.Request{
		MessageID:  5,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
		Payload:    &wire.SubscribePayload{},
	}

	reqData, _ := wire.EncodeRequest(req)
	session.OnMessage(reqData)
	time.Sleep(50 * time.Millisecond)

	if session.SubscriptionCount() != 1 {
		t.Errorf("Expected 1 subscription before close, got %d", session.SubscriptionCount())
	}

	// Close the session
	session.Close()

	// Subscriptions should be cleared
	if session.SubscriptionCount() != 0 {
		t.Errorf("Expected 0 subscriptions after close, got %d", session.SubscriptionCount())
	}
}

func TestZoneSession_ZoneID(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("my-zone-id", conn, device)

	if session.ZoneID() != "my-zone-id" {
		t.Errorf("Expected zone ID 'my-zone-id', got %q", session.ZoneID())
	}
}

func TestZoneSession_HandleMultipleRequests(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Send multiple read requests
	for i := uint32(1); i <= 3; i++ {
		req := &wire.Request{
			MessageID:  i,
			Operation:  wire.OpRead,
			EndpointID: 0,
			FeatureID:  uint8(model.FeatureDeviceInfo),
		}
		reqData, _ := wire.EncodeRequest(req)
		session.OnMessage(reqData)
	}

	time.Sleep(100 * time.Millisecond)

	// Should have 3 responses
	sent := conn.SentMessages()
	if len(sent) != 3 {
		t.Fatalf("Expected 3 responses, got %d", len(sent))
	}

	// Verify each response
	for i, data := range sent {
		resp, err := wire.DecodeResponse(data)
		if err != nil {
			t.Errorf("Failed to decode response %d: %v", i, err)
			continue
		}
		if resp.Status != wire.StatusSuccess {
			t.Errorf("Response %d: expected success, got %v", i, resp.Status)
		}
	}
}

// ============================================================================
// Bidirectional Support Tests
// ============================================================================

// mockBidirectionalConnection extends mockSendableConnection with the ability
// to inject responses when requests are sent.
type mockBidirectionalConnection struct {
	mu      sync.Mutex
	sent    [][]byte
	sendErr error
	onSend  func(data []byte) // Callback when data is sent
	session *ZoneSession      // Reference to inject responses
}

func newMockBidirectionalConnection() *mockBidirectionalConnection {
	return &mockBidirectionalConnection{}
}

func (m *mockBidirectionalConnection) Send(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, data)

	if m.onSend != nil {
		m.onSend(data)
	}
	return nil
}

func (m *mockBidirectionalConnection) SentMessages() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([][]byte, len(m.sent))
	copy(result, m.sent)
	return result
}

func (m *mockBidirectionalConnection) SetSendError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendErr = err
}

func (m *mockBidirectionalConnection) SetSession(session *ZoneSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = session
}

func (m *mockBidirectionalConnection) SetOnSend(fn func(data []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSend = fn
}

func TestZoneSession_OnMessage_HandlesResponse(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Simulate that we sent a request with MessageID 42 and are awaiting response
	// First, we need to trigger the client to wait for a response
	// We'll use a goroutine to send a request and verify it receives the response

	// Set up connection to auto-respond to requests
	conn.SetOnSend(func(data []byte) {
		msgType, err := wire.PeekMessageType(data)
		if err != nil || msgType != wire.MessageTypeRequest {
			return
		}
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		// Send back a success response
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[uint16]any{1: "test-value"},
		}
		respData, _ := wire.EncodeResponse(resp)
		// Simulate async response
		go func() {
			time.Sleep(10 * time.Millisecond)
			session.OnMessage(respData)
		}()
	})
	conn.SetSession(session)

	// Now call Read and verify it gets the response
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Read(ctx, 0, 1, nil)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
	if result[1] != "test-value" {
		t.Errorf("Expected test-value, got %v", result[1])
	}
}

func TestZoneSession_OnMessage_HandlesNotification(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Set up notification handler
	var receivedNotif *wire.Notification
	var receivedMu sync.Mutex
	session.SetNotificationHandler(func(notif *wire.Notification) {
		receivedMu.Lock()
		defer receivedMu.Unlock()
		receivedNotif = notif
	})

	// Create a notification
	notif := &wire.Notification{
		SubscriptionID: 42,
		EndpointID:     1,
		FeatureID:      2,
		Changes:        map[uint16]any{1: "changed-value"},
	}
	notifData, err := wire.EncodeNotification(notif)
	if err != nil {
		t.Fatalf("Failed to encode notification: %v", err)
	}

	// Send notification to session
	session.OnMessage(notifData)

	// Give handler time to process
	time.Sleep(50 * time.Millisecond)

	// Verify notification was received
	receivedMu.Lock()
	defer receivedMu.Unlock()
	if receivedNotif == nil {
		t.Fatal("Expected notification handler to be called")
	}
	if receivedNotif.SubscriptionID != 42 {
		t.Errorf("Expected subscription ID 42, got %d", receivedNotif.SubscriptionID)
	}
	if receivedNotif.Changes[1] != "changed-value" {
		t.Errorf("Expected changed-value, got %v", receivedNotif.Changes[1])
	}
}

func TestZoneSession_Read_SendsRequestAndReceivesResponse(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Auto-respond to read requests
	conn.SetOnSend(func(data []byte) {
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		if req.Operation != wire.OpRead {
			return
		}
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[uint16]any{1: "device-id", 2: "vendor-name"},
		}
		respData, _ := wire.EncodeResponse(resp)
		go func() {
			time.Sleep(5 * time.Millisecond)
			session.OnMessage(respData)
		}()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Read(ctx, 1, 6, []uint16{1, 2})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 attributes, got %d", len(result))
	}
}

func TestZoneSession_Write_SendsRequestAndReceivesResponse(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	var capturedReq *wire.Request
	conn.SetOnSend(func(data []byte) {
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		if req.Operation != wire.OpWrite {
			return
		}
		capturedReq = req
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[uint16]any{1: "written-value"},
		}
		respData, _ := wire.EncodeResponse(resp)
		go func() {
			time.Sleep(5 * time.Millisecond)
			session.OnMessage(respData)
		}()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Write(ctx, 1, 6, map[uint16]any{1: "new-value"})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify request was sent
	if capturedReq == nil {
		t.Fatal("Expected request to be sent")
	}
	if capturedReq.Operation != wire.OpWrite {
		t.Errorf("Expected Write operation, got %v", capturedReq.Operation)
	}

	// Verify response was received
	if result[1] != "written-value" {
		t.Errorf("Expected written-value, got %v", result[1])
	}
}

func TestZoneSession_ClearSubscriptions(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Create an inbound subscription via handleRequest (subscribe request).
	subscribeReq := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  0x01, // DeviceInfo
	}
	data, err := wire.EncodeRequest(subscribeReq)
	if err != nil {
		t.Fatalf("encode subscribe: %v", err)
	}
	session.OnMessage(data)

	if session.SubscriptionCount() == 0 {
		t.Fatal("expected at least 1 subscription after subscribe request")
	}

	// ClearSubscriptions should remove inbound subscriptions without closing the session.
	session.ClearSubscriptions()

	if session.SubscriptionCount() != 0 {
		t.Errorf("expected 0 subscriptions after ClearSubscriptions, got %d", session.SubscriptionCount())
	}

	// Session should still be usable (not closed).
	readReq := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  0x01,
	}
	readData, _ := wire.EncodeRequest(readReq)
	session.OnMessage(readData)
	// If session was closed, OnMessage would silently return and no response sent.
	if len(conn.SentMessages()) < 2 {
		t.Error("expected session to still process requests after ClearSubscriptions")
	}
}

func TestZoneSession_Subscribe_TracksInSubscriptionManager(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	conn.SetOnSend(func(data []byte) {
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		if req.Operation != wire.OpSubscribe {
			return
		}
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload: &wire.SubscribeResponsePayload{
				SubscriptionID: 123,
				CurrentValues:  map[uint16]any{1: "initial"},
			},
		}
		respData, _ := wire.EncodeResponse(resp)
		go func() {
			time.Sleep(5 * time.Millisecond)
			session.OnMessage(respData)
		}()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	subID, values, err := session.Subscribe(ctx, 1, 6, nil)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	if subID != 123 {
		t.Errorf("Expected subscription ID 123, got %d", subID)
	}
	if values[1] != "initial" {
		t.Errorf("Expected initial value, got %v", values[1])
	}
}

func TestZoneSession_Invoke_SendsRequestAndReceivesResponse(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	var capturedReq *wire.Request
	conn.SetOnSend(func(data []byte) {
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		if req.Operation != wire.OpInvoke {
			return
		}
		capturedReq = req
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[string]any{"result": "success"},
		}
		respData, _ := wire.EncodeResponse(resp)
		go func() {
			time.Sleep(5 * time.Millisecond)
			session.OnMessage(respData)
		}()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := session.Invoke(ctx, 1, 6, 1, map[uint8]any{1: "value"})
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	// Verify request was sent
	if capturedReq == nil {
		t.Fatal("Expected request to be sent")
	}
	if capturedReq.Operation != wire.OpInvoke {
		t.Errorf("Expected Invoke operation, got %v", capturedReq.Operation)
	}

	// Verify response
	if result == nil {
		t.Fatal("Expected result, got nil")
	}
}

func TestZoneSession_ConcurrentRequests(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Track message IDs to ensure each request gets its own response
	var messageIDsMu sync.Mutex
	messageIDs := make(map[uint32]bool)

	conn.SetOnSend(func(data []byte) {
		req, err := wire.DecodeRequest(data)
		if err != nil {
			return
		}
		messageIDsMu.Lock()
		messageIDs[req.MessageID] = true
		messageIDsMu.Unlock()

		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[uint16]any{1: req.MessageID}, // Echo message ID in payload
		}
		respData, _ := wire.EncodeResponse(resp)
		go func() {
			time.Sleep(time.Duration(req.MessageID%10) * time.Millisecond) // Vary response time
			session.OnMessage(respData)
		}()
	})

	// Launch concurrent requests
	const numRequests = 10
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)
	results := make(chan map[uint16]any, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			result, err := session.Read(ctx, 0, 1, nil)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}

	wg.Wait()
	close(errors)
	close(results)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent request failed: %v", err)
	}

	// Verify all requests succeeded
	resultCount := 0
	for range results {
		resultCount++
	}
	if resultCount != numRequests {
		t.Errorf("Expected %d results, got %d", numRequests, resultCount)
	}

	// Verify unique message IDs were used
	messageIDsMu.Lock()
	if len(messageIDs) != numRequests {
		t.Errorf("Expected %d unique message IDs, got %d", numRequests, len(messageIDs))
	}
	messageIDsMu.Unlock()
}

func TestZoneSession_RequestTimeout(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)
	session.SetTimeout(50 * time.Millisecond) // Short timeout for test

	// Don't send any response - request should timeout
	conn.SetOnSend(func(data []byte) {
		// Intentionally do nothing - simulate no response
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := session.Read(ctx, 0, 1, nil)
	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
}

func TestZoneSession_NotificationHandlerCalled(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	var callCount int32
	session.SetNotificationHandler(func(notif *wire.Notification) {
		atomic.AddInt32(&callCount, 1)
	})

	// Send multiple notifications
	for i := 0; i < 5; i++ {
		notif := &wire.Notification{
			SubscriptionID: uint32(i),
			EndpointID:     1,
			FeatureID:      2,
			Changes:        map[uint16]any{1: i},
		}
		notifData, _ := wire.EncodeNotification(notif)
		session.OnMessage(notifData)
	}

	time.Sleep(100 * time.Millisecond)

	count := atomic.LoadInt32(&callCount)
	if count != 5 {
		t.Errorf("Expected notification handler called 5 times, got %d", count)
	}
}

func TestZoneSession_CloseStopsClient(t *testing.T) {
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Start a request in background
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := session.Read(ctx, 0, 1, nil)
		errCh <- err
	}()

	// Give the request time to start
	time.Sleep(50 * time.Millisecond)

	// Close the session
	session.Close()

	// Request should fail with client closed error
	select {
	case err := <-errCh:
		if err == nil {
			t.Error("Expected error after close, got nil")
		}
	case <-time.After(time.Second):
		t.Error("Request did not complete after session close")
	}
}

// ============================================================================
// Snapshot Integration Tests
// ============================================================================

func TestZoneSession_EmitsInitialSnapshot(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	logger := &snapshotCapturingLogger{}
	session.SetSnapshotPolicy(SnapshotPolicy{MaxMessages: 1000})
	session.SetProtocolLogger(logger, "conn-snap-1")

	if logger.snapshotCount() != 1 {
		t.Fatalf("expected 1 initial snapshot, got %d", logger.snapshotCount())
	}

	event := logger.lastEvent()
	if event.Category != log.CategorySnapshot {
		t.Errorf("Category: got %v, want %v", event.Category, log.CategorySnapshot)
	}
	if event.Snapshot == nil {
		t.Fatal("Snapshot is nil")
	}
	if event.Snapshot.Local == nil {
		t.Fatal("Snapshot.Local is nil")
	}
	if event.Snapshot.Local.DeviceID != "test-device" {
		t.Errorf("Local.DeviceID: got %q, want %q", event.Snapshot.Local.DeviceID, "test-device")
	}
	if event.LocalRole != log.RoleDevice {
		t.Errorf("LocalRole: got %v, want %v", event.LocalRole, log.RoleDevice)
	}
	if event.ConnectionID != "conn-snap-1" {
		t.Errorf("ConnectionID: got %q, want %q", event.ConnectionID, "conn-snap-1")
	}
}

func TestZoneSession_SnapshotOnMessageCount(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	logger := &snapshotCapturingLogger{}
	// Each request generates 2 handler log events (logRequest + logResponse).
	// With MaxMessages=4, after 2 requests (=4 events) we trigger a snapshot.
	session.SetSnapshotPolicy(SnapshotPolicy{MaxMessages: 4})
	session.SetProtocolLogger(logger, "conn-snap-2")

	// Initial snapshot
	if logger.snapshotCount() != 1 {
		t.Fatalf("expected 1 initial snapshot, got %d", logger.snapshotCount())
	}

	// Send 2 read requests (4 handler log events -> triggers snapshot)
	for i := uint32(1); i <= 2; i++ {
		req := &wire.Request{
			MessageID:  i,
			Operation:  wire.OpRead,
			EndpointID: 0,
			FeatureID:  uint8(model.FeatureDeviceInfo),
		}
		reqData, _ := wire.EncodeRequest(req)
		session.OnMessage(reqData)
	}

	time.Sleep(50 * time.Millisecond)

	if logger.snapshotCount() != 2 {
		t.Fatalf("expected 2 snapshots (initial + triggered), got %d", logger.snapshotCount())
	}
}

func TestZoneSession_NoSnapshotWithoutLogger(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// No SetProtocolLogger call - process messages normally, should not panic.
	for i := uint32(1); i <= 5; i++ {
		req := &wire.Request{
			MessageID:  i,
			Operation:  wire.OpRead,
			EndpointID: 0,
			FeatureID:  uint8(model.FeatureDeviceInfo),
		}
		reqData, _ := wire.EncodeRequest(req)
		session.OnMessage(reqData)
	}
}

func TestZoneSession_IncomingRequestStillWorks(t *testing.T) {
	// Verify that adding bidirectional support doesn't break existing request handling
	device := createTestDevice()
	conn := newMockBidirectionalConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Send an incoming read request (from controller)
	req := &wire.Request{
		MessageID:  100,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureDeviceInfo),
	}
	reqData, _ := wire.EncodeRequest(req)
	session.OnMessage(reqData)

	time.Sleep(50 * time.Millisecond)

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 response, got %d", len(sent))
	}

	resp, err := wire.DecodeResponse(sent[0])
	if err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.MessageID != 100 {
		t.Errorf("Expected MessageID 100, got %d", resp.MessageID)
	}
	if resp.Status != wire.StatusSuccess {
		t.Errorf("Expected success, got %v", resp.Status)
	}
}

// TestZoneSession_InvalidCBORReturnsError verifies that invalid CBOR (e.g. duplicate keys)
// results in an INVALID_PARAMETER error response, not silent ignoring.
func TestZoneSession_InvalidCBORReturnsError(t *testing.T) {
	device := createTestDevice()
	conn := newMockSendableConnection()
	session := NewZoneSession("zone-1", conn, device)

	// Send invalid CBOR with duplicate keys:
	// A3 01 01 01 02 02 03 = map(3) with key 1 appearing twice
	invalidCBOR := []byte{0xA3, 0x01, 0x01, 0x01, 0x02, 0x02, 0x03}
	session.OnMessage(invalidCBOR)

	time.Sleep(50 * time.Millisecond)

	sent := conn.SentMessages()
	if len(sent) != 1 {
		t.Fatalf("Expected 1 error response for invalid CBOR, got %d", len(sent))
	}

	resp, err := wire.DecodeResponse(sent[0])
	if err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	if resp.Status != wire.StatusInvalidParameter {
		t.Errorf("Expected INVALID_PARAMETER status, got %v", resp.Status)
	}
}
