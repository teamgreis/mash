package dispatch_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/wire"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// capturingLogger records log events for testing.
type capturingLogger struct {
	mu     sync.Mutex
	events []log.Event
}

func newCapturingLogger() *capturingLogger {
	return &capturingLogger{}
}

func (l *capturingLogger) Log(event log.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *capturingLogger) Events() []log.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]log.Event, len(l.events))
	copy(result, l.events)
	return result
}

func (l *capturingLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = nil
}

// Feature IDs match model.FeatureType values
const (
	featureIDElectrical = uint8(model.FeatureElectrical) // 0x0001 = 1
	featureIDDeviceInfo = uint8(model.FeatureDeviceInfo) // 0x0006 = 6
)

// createTestDevice creates a device with DeviceInfo for testing.
func createTestDevice() *model.Device {
	device := model.NewDevice("test-device-001", 0x1234, 0x0001)

	// Add DeviceInfo to root endpoint
	deviceInfo := features.NewDeviceInfo()
	_ = deviceInfo.SetDeviceID("test-device-001")
	_ = deviceInfo.SetVendorName("Test Vendor")
	_ = deviceInfo.SetProductName("Test Product")
	_ = deviceInfo.SetSerialNumber("SN-12345")
	_ = deviceInfo.SetSoftwareVersion("1.0.0")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Add an EVSE endpoint with Electrical and Measurement features
	evse := model.NewEndpoint(1, model.EndpointEVCharger, "Test EVSE")

	electrical := features.NewElectrical()
	_ = electrical.SetPhaseCount(3)
	_ = electrical.SetNominalVoltage(230)
	_ = electrical.SetNominalFrequency(50)
	_ = electrical.SetNominalMaxConsumption(22000000) // 22 kW
	evse.AddFeature(electrical.Feature)

	measurement := features.NewMeasurement()
	evse.AddFeature(measurement.Feature)

	_ = device.AddEndpoint(evse)

	return device
}

func TestProtocolHandler_HandleRead_DeviceInfo(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Create read request for DeviceInfo (endpoint 0, feature 1)
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload:    nil,
	}

	resp := handler.HandleRequest(req)

	// Verify response
	if resp.MessageID != req.MessageID {
		t.Errorf("MessageID mismatch: expected %d, got %d", req.MessageID, resp.MessageID)
	}

	if !resp.IsSuccess() {
		t.Errorf("Expected success response, got status %d", resp.Status)
	}

	// Verify payload contains device info attributes
	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("Expected map[uint16]any payload, got %T", resp.Payload)
	}

	// Check for expected attributes (DeviceID, VendorName, etc.)
	if _, exists := payload[1]; !exists { // DeviceID attribute
		t.Error("Expected DeviceID (attribute 1) in response")
	}
	if _, exists := payload[2]; !exists { // VendorName attribute
		t.Error("Expected VendorName (attribute 2) in response")
	}
}

func TestProtocolHandler_HandleRead_SpecificAttributes(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Request only specific attributes
	req := &wire.Request{
		MessageID:  2,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.ReadPayload{
			AttributeIDs: []uint16{1, 2}, // Only DeviceID and VendorName
		},
	}

	resp := handler.HandleRequest(req)

	if !resp.IsSuccess() {
		t.Errorf("Expected success response, got status %d", resp.Status)
	}

	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("Expected map[uint16]any payload, got %T", resp.Payload)
	}

	// Should have only the requested attributes
	if len(payload) > 2 {
		t.Errorf("Expected at most 2 attributes, got %d", len(payload))
	}
}

func TestProtocolHandler_HandleRead_InvalidEndpoint(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	req := &wire.Request{
		MessageID:  3,
		Operation:  wire.OpRead,
		EndpointID: 99, // Non-existent endpoint
		FeatureID:  featureIDDeviceInfo,
	}

	resp := handler.HandleRequest(req)

	if resp.IsSuccess() {
		t.Error("Expected error response for invalid endpoint")
	}

	if resp.Status != wire.StatusInvalidEndpoint {
		t.Errorf("Expected StatusInvalidEndpoint (%d), got %d", wire.StatusInvalidEndpoint, resp.Status)
	}
}

func TestProtocolHandler_HandleRead_InvalidFeature(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	req := &wire.Request{
		MessageID:  4,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  99, // Non-existent feature
	}

	resp := handler.HandleRequest(req)

	if resp.IsSuccess() {
		t.Error("Expected error response for invalid feature")
	}

	if resp.Status != wire.StatusInvalidFeature {
		t.Errorf("Expected StatusInvalidFeature (%d), got %d", wire.StatusInvalidFeature, resp.Status)
	}
}

func TestProtocolHandler_HandleWrite(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Write request - try to write to a writable attribute
	// DeviceInfo attributes are generally read-only, so this should fail
	req := &wire.Request{
		MessageID:  5,
		Operation:  wire.OpWrite,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: wire.WritePayload{
			1: "new-device-id", // Try to write DeviceID
		},
	}

	resp := handler.HandleRequest(req)

	// DeviceInfo attributes are read-only, so write should fail
	if resp.IsSuccess() {
		t.Error("Expected write to read-only attribute to fail")
	}
}

func TestProtocolHandler_HandleSubscribe(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	req := &wire.Request{
		MessageID:  6,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.SubscribePayload{
			MinInterval: 1000,  // 1 second
			MaxInterval: 60000, // 60 seconds
		},
	}

	resp := handler.HandleRequest(req)

	if !resp.IsSuccess() {
		t.Errorf("Expected success response, got status %d", resp.Status)
	}

	// Verify subscription ID is returned
	payload, ok := resp.Payload.(*wire.SubscribeResponsePayload)
	if !ok {
		t.Fatalf("Expected *wire.SubscribeResponsePayload, got %T", resp.Payload)
	}

	if payload.SubscriptionID == 0 {
		t.Error("Expected non-zero subscription ID")
	}

	// Priming report should contain current values
	if payload.CurrentValues == nil {
		t.Error("Expected current values in priming report")
	}
}

func TestProtocolHandler_HandleUnsubscribe(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// First create a subscription
	subReq := &wire.Request{
		MessageID:  7,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.SubscribePayload{
			MinInterval: 1000,
			MaxInterval: 60000,
		},
	}
	subResp := handler.HandleRequest(subReq)

	if !subResp.IsSuccess() {
		t.Fatalf("Failed to create subscription: status %d", subResp.Status)
	}

	subPayload := subResp.Payload.(*wire.SubscribeResponsePayload)

	// Now unsubscribe (endpoint 0, feature 0 indicates unsubscribe)
	unsubReq := &wire.Request{
		MessageID:  8,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  0, // 0 indicates unsubscribe
		Payload: &wire.UnsubscribePayload{
			SubscriptionID: subPayload.SubscriptionID,
		},
	}

	unsubResp := handler.HandleRequest(unsubReq)

	if !unsubResp.IsSuccess() {
		t.Errorf("Expected success response for unsubscribe, got status %d", unsubResp.Status)
	}
}

func TestProtocolHandler_HandleInvoke(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Try to invoke a command on DeviceInfo
	// DeviceInfo typically doesn't have commands, so this should fail
	req := &wire.Request{
		MessageID:  9,
		Operation:  wire.OpInvoke,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.InvokePayload{
			CommandID: 1, // Non-existent command
		},
	}

	resp := handler.HandleRequest(req)

	// Should fail as DeviceInfo has no commands
	if resp.IsSuccess() {
		t.Error("Expected invoke on feature without commands to fail")
	}
}

func TestProtocolHandler_InvalidOperation(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	req := &wire.Request{
		MessageID:  10,
		Operation:  wire.Operation(99), // Invalid operation
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
	}

	resp := handler.HandleRequest(req)

	if resp.IsSuccess() {
		t.Error("Expected error response for invalid operation")
	}

	if resp.Status != wire.StatusUnsupported {
		t.Errorf("Expected StatusUnsupported (%d), got %d", wire.StatusUnsupported, resp.Status)
	}
}

func TestProtocolHandler_ReadElectrical(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Read Electrical feature on endpoint 1
	req := &wire.Request{
		MessageID:  11,
		Operation:  wire.OpRead,
		EndpointID: 1, // EVSE endpoint
		FeatureID:  featureIDElectrical,
	}

	resp := handler.HandleRequest(req)

	if !resp.IsSuccess() {
		t.Errorf("Expected success response, got status %d", resp.Status)
	}

	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("Expected map[uint16]any payload, got %T", resp.Payload)
	}

	// Check for phaseCount attribute
	if phaseCount, exists := payload[1]; exists {
		if phaseCount != uint8(3) {
			t.Errorf("Expected phaseCount 3, got %v", phaseCount)
		}
	} else {
		t.Error("Expected phaseCount attribute in response")
	}
}

func TestProtocolHandler_ZoneID(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Set zone context
	handler.SetZoneID("zone-123")

	if handler.ZoneID() != "zone-123" {
		t.Errorf("Expected zone ID 'zone-123', got '%s'", handler.ZoneID())
	}
}

func TestProtocolHandler_SetPeerID(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Set peer ID
	handler.SetPeerID("peer-456")

	if handler.PeerID() != "peer-456" {
		t.Errorf("Expected peer ID 'peer-456', got '%s'", handler.PeerID())
	}
}

func TestProtocolHandler_SubscriptionManagerIntegration(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Create a subscription
	req := &wire.Request{
		MessageID:  100,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.SubscribePayload{
			MinInterval: 1000,
			MaxInterval: 60000,
		},
	}

	resp := handler.HandleRequest(req)

	if !resp.IsSuccess() {
		t.Fatalf("Expected success response, got status %d", resp.Status)
	}

	payload := resp.Payload.(*wire.SubscribeResponsePayload)

	// Verify the subscription is tracked in the SubscriptionManager
	subMgr := handler.SessionSubscriptions()
	sub := subMgr.GetInbound(payload.SubscriptionID)
	if sub == nil {
		t.Error("Expected subscription to be tracked in SubscriptionManager")
	}
	if sub != nil {
		if sub.EndpointID != 0 {
			t.Errorf("Expected EndpointID 0, got %d", sub.EndpointID)
		}
		if sub.FeatureID != featureIDDeviceInfo {
			t.Errorf("Expected FeatureID %d, got %d", featureIDDeviceInfo, sub.FeatureID)
		}
	}
}

func TestProtocolHandler_NotifyAttributeChange(t *testing.T) {
	device := createTestDevice()

	// Track sent notifications
	var sentNotifications []*wire.Notification
	sendFunc := func(n *wire.Notification) error {
		sentNotifications = append(sentNotifications, n)
		return nil
	}

	handler := dispatch.NewProtocolHandlerWithSend(device, sendFunc)

	// Create a subscription to all attributes
	req := &wire.Request{
		MessageID:  101,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload:    &wire.SubscribePayload{},
	}

	resp := handler.HandleRequest(req)
	if !resp.IsSuccess() {
		t.Fatalf("Failed to create subscription: status %d", resp.Status)
	}

	subPayload := resp.Payload.(*wire.SubscribeResponsePayload)

	// Notify an attribute change
	err := handler.NotifyAttributeChange(0, featureIDDeviceInfo, 1, "new-value")
	if err != nil {
		t.Fatalf("NotifyAttributeChange failed: %v", err)
	}

	// Verify notification was sent
	if len(sentNotifications) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(sentNotifications))
	}

	n := sentNotifications[0]
	if n.SubscriptionID != subPayload.SubscriptionID {
		t.Errorf("Expected subscription ID %d, got %d", subPayload.SubscriptionID, n.SubscriptionID)
	}
	if n.EndpointID != 0 {
		t.Errorf("Expected EndpointID 0, got %d", n.EndpointID)
	}
	if n.FeatureID != featureIDDeviceInfo {
		t.Errorf("Expected FeatureID %d, got %d", featureIDDeviceInfo, n.FeatureID)
	}
	if n.Changes[1] != "new-value" {
		t.Errorf("Expected change value 'new-value', got %v", n.Changes[1])
	}
}

func TestProtocolHandler_NotifyAttributeChange_WithAttributeFilter(t *testing.T) {
	device := createTestDevice()

	var sentNotifications []*wire.Notification
	sendFunc := func(n *wire.Notification) error {
		sentNotifications = append(sentNotifications, n)
		return nil
	}

	handler := dispatch.NewProtocolHandlerWithSend(device, sendFunc)

	// Create a subscription to specific attributes only (attribute 1 and 2)
	req := &wire.Request{
		MessageID:  102,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload: &wire.SubscribePayload{
			AttributeIDs: []uint16{1, 2},
		},
	}

	resp := handler.HandleRequest(req)
	if !resp.IsSuccess() {
		t.Fatalf("Failed to create subscription: status %d", resp.Status)
	}

	// Notify an attribute that IS subscribed (attribute 1)
	err := handler.NotifyAttributeChange(0, featureIDDeviceInfo, 1, "value1")
	if err != nil {
		t.Fatalf("NotifyAttributeChange failed: %v", err)
	}

	if len(sentNotifications) != 1 {
		t.Errorf("Expected 1 notification for subscribed attribute, got %d", len(sentNotifications))
	}

	// Notify an attribute that is NOT subscribed (attribute 99)
	sentNotifications = nil
	err = handler.NotifyAttributeChange(0, featureIDDeviceInfo, 99, "value99")
	if err != nil {
		t.Fatalf("NotifyAttributeChange failed: %v", err)
	}

	if len(sentNotifications) != 0 {
		t.Errorf("Expected 0 notifications for unsubscribed attribute, got %d", len(sentNotifications))
	}
}

func TestProtocolHandler_NotifyAttributeChange_MultipleSubscriptions(t *testing.T) {
	device := createTestDevice()

	var sentNotifications []*wire.Notification
	sendFunc := func(n *wire.Notification) error {
		sentNotifications = append(sentNotifications, n)
		return nil
	}

	handler := dispatch.NewProtocolHandlerWithSend(device, sendFunc)

	// Create two subscriptions to the same feature
	for i := 0; i < 2; i++ {
		req := &wire.Request{
			MessageID:  uint32(103 + i),
			Operation:  wire.OpSubscribe,
			EndpointID: 0,
			FeatureID:  featureIDDeviceInfo,
			Payload:    &wire.SubscribePayload{},
		}
		resp := handler.HandleRequest(req)
		if !resp.IsSuccess() {
			t.Fatalf("Failed to create subscription %d: status %d", i, resp.Status)
		}
	}

	// Notify an attribute change
	err := handler.NotifyAttributeChange(0, featureIDDeviceInfo, 1, "multi-value")
	if err != nil {
		t.Fatalf("NotifyAttributeChange failed: %v", err)
	}

	// Both subscriptions should receive notifications
	if len(sentNotifications) != 2 {
		t.Errorf("Expected 2 notifications, got %d", len(sentNotifications))
	}
}

func TestProtocolHandler_CustomSendFunction(t *testing.T) {
	device := createTestDevice()

	callCount := 0
	sendFunc := func(n *wire.Notification) error {
		callCount++
		return nil
	}

	handler := dispatch.NewProtocolHandlerWithSend(device, sendFunc)

	// Verify handler was created with send function
	if handler == nil {
		t.Fatal("Expected handler to be created")
	}

	// Create subscription and trigger notification
	req := &wire.Request{
		MessageID:  105,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload:    &wire.SubscribePayload{},
	}
	handler.HandleRequest(req)

	handler.NotifyAttributeChange(0, featureIDDeviceInfo, 1, "test")

	if callCount != 1 {
		t.Errorf("Expected send function to be called 1 time, got %d", callCount)
	}
}

func TestProtocolHandler_UnsubscribeRemovesFromManager(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Create a subscription
	subReq := &wire.Request{
		MessageID:  106,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload:    &wire.SubscribePayload{},
	}
	subResp := handler.HandleRequest(subReq)

	if !subResp.IsSuccess() {
		t.Fatalf("Failed to create subscription: status %d", subResp.Status)
	}

	subPayload := subResp.Payload.(*wire.SubscribeResponsePayload)
	subID := subPayload.SubscriptionID

	// Verify subscription exists in manager
	subMgr := handler.SessionSubscriptions()
	if subMgr.GetInbound(subID) == nil {
		t.Error("Expected subscription in manager before unsubscribe")
	}

	// Unsubscribe
	unsubReq := &wire.Request{
		MessageID:  107,
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  0,
		Payload: &wire.UnsubscribePayload{
			SubscriptionID: subID,
		},
	}
	unsubResp := handler.HandleRequest(unsubReq)

	if !unsubResp.IsSuccess() {
		t.Fatalf("Failed to unsubscribe: status %d", unsubResp.Status)
	}

	// Verify subscription is removed from manager
	if subMgr.GetInbound(subID) != nil {
		t.Error("Expected subscription to be removed from manager after unsubscribe")
	}
}

// =============================================================================
// Protocol Logging Tests
// =============================================================================

func TestProtocolHandler_LogsRequest(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	logger := newCapturingLogger()
	handler.SetLogger(logger, "conn-123")

	req := &wire.Request{
		MessageID:  42,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
	}

	_ = handler.HandleRequest(req)

	events := logger.Events()
	if len(events) < 2 {
		t.Fatalf("Expected at least 2 log events (request + response), got %d", len(events))
	}

	// First event should be the request
	reqEvent := events[0]
	if reqEvent.Direction != log.DirectionIn {
		t.Errorf("Request should have Direction=In, got %s", reqEvent.Direction)
	}
	if reqEvent.Layer != log.LayerWire {
		t.Errorf("Request should have Layer=Wire, got %s", reqEvent.Layer)
	}
	if reqEvent.Category != log.CategoryMessage {
		t.Errorf("Request should have Category=Message, got %s", reqEvent.Category)
	}
	if reqEvent.ConnectionID != "conn-123" {
		t.Errorf("Request should have ConnectionID='conn-123', got '%s'", reqEvent.ConnectionID)
	}

	// Check message event details
	if reqEvent.Message == nil {
		t.Fatal("Request event should have Message payload")
	}
	if reqEvent.Message.Type != log.MessageTypeRequest {
		t.Errorf("Request should have Type=Request, got %s", reqEvent.Message.Type)
	}
	if reqEvent.Message.MessageID != 42 {
		t.Errorf("Request should have MessageID=42, got %d", reqEvent.Message.MessageID)
	}
	if reqEvent.Message.Operation == nil || *reqEvent.Message.Operation != wire.OpRead {
		t.Errorf("Request should have Operation=Read")
	}
}

func TestProtocolHandler_LogsResponse(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	logger := newCapturingLogger()
	handler.SetLogger(logger, "conn-456")

	req := &wire.Request{
		MessageID:  99,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
	}

	_ = handler.HandleRequest(req)

	events := logger.Events()
	if len(events) < 2 {
		t.Fatalf("Expected at least 2 log events, got %d", len(events))
	}

	// Second event should be the response
	respEvent := events[1]
	if respEvent.Direction != log.DirectionOut {
		t.Errorf("Response should have Direction=Out, got %s", respEvent.Direction)
	}
	if respEvent.Layer != log.LayerWire {
		t.Errorf("Response should have Layer=Wire, got %s", respEvent.Layer)
	}
	if respEvent.Message == nil {
		t.Fatal("Response event should have Message payload")
	}
	if respEvent.Message.Type != log.MessageTypeResponse {
		t.Errorf("Response should have Type=Response, got %s", respEvent.Message.Type)
	}
	if respEvent.Message.MessageID != 99 {
		t.Errorf("Response should have MessageID=99, got %d", respEvent.Message.MessageID)
	}
	if respEvent.Message.Status == nil || *respEvent.Message.Status != wire.StatusSuccess {
		t.Errorf("Response should have Status=Success")
	}
}

func TestProtocolHandler_LogsProcessingTime(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	logger := newCapturingLogger()
	handler.SetLogger(logger, "conn-789")

	req := &wire.Request{
		MessageID:  55,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
	}

	// Add a small delay to ensure measurable processing time
	time.Sleep(1 * time.Millisecond)
	_ = handler.HandleRequest(req)

	events := logger.Events()
	if len(events) < 2 {
		t.Fatalf("Expected at least 2 log events, got %d", len(events))
	}

	// Response should have processing time
	respEvent := events[1]
	if respEvent.Message == nil {
		t.Fatal("Response event should have Message payload")
	}
	if respEvent.Message.ProcessingTime == nil {
		t.Error("Response should have ProcessingTime set")
	} else if *respEvent.Message.ProcessingTime < 0 {
		t.Error("ProcessingTime should be non-negative")
	}
}

func TestProtocolHandler_NoLoggerNoPanic(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// No logger set - should not panic
	req := &wire.Request{
		MessageID:  1,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
	}

	// Should not panic
	resp := handler.HandleRequest(req)

	if !resp.IsSuccess() {
		t.Errorf("Expected success response even without logger, got status %d", resp.Status)
	}
}

func TestProtocolHandler_LogsErrorResponse(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	logger := newCapturingLogger()
	handler.SetLogger(logger, "conn-err")

	// Request for non-existent endpoint
	req := &wire.Request{
		MessageID:  77,
		Operation:  wire.OpRead,
		EndpointID: 99, // Non-existent
		FeatureID:  featureIDDeviceInfo,
	}

	_ = handler.HandleRequest(req)

	events := logger.Events()
	if len(events) < 2 {
		t.Fatalf("Expected at least 2 log events, got %d", len(events))
	}

	// Response should have error status
	respEvent := events[1]
	if respEvent.Message == nil {
		t.Fatal("Response event should have Message payload")
	}
	if respEvent.Message.Status == nil {
		t.Fatal("Response should have Status")
	}
	if *respEvent.Message.Status == wire.StatusSuccess {
		t.Error("Response should have error status for invalid endpoint")
	}
}

// =============================================================================
// Context Threading Tests
// =============================================================================

// featureTypeForHookTest is a feature type used in context threading tests.
// Using EnergyControl (0x05) since per-zone reads are most relevant there.
const featureTypeForHookTest = model.FeatureEnergyControl

// testAttrID is the attribute ID used in hook tests.
const testAttrID uint16 = 20

// createDeviceWithReadHook creates a device with endpoint 1 containing a feature
// that has a ReadHook installed. The hook captures the context it receives via
// the provided pointer. The feature has a single readable attribute (testAttrID)
// with a stored value of "stored-value".
func createDeviceWithReadHook(hookCalled *bool, capturedZoneID *string) *model.Device {
	device := model.NewDevice("test-hook-device", 0x1234, 0x0001)

	// Add DeviceInfo to root endpoint (required by convention)
	deviceInfo := features.NewDeviceInfo()
	_ = deviceInfo.SetDeviceID("test-hook-device")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Create endpoint 1 with a feature that has a ReadHook
	ep := model.NewEndpoint(1, model.EndpointEVCharger, "Test EP")

	feature := model.NewFeature(featureTypeForHookTest, 1)
	feature.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:      testAttrID,
		Name:    "testAttr",
		Type:    model.DataTypeString,
		Access:  model.AccessReadOnly,
		Default: "stored-value",
	}))

	feature.SetReadHook(func(ctx context.Context, attrID uint16) (any, bool) {
		*hookCalled = true
		*capturedZoneID = zonecontext.CallerZoneIDFromContext(ctx)
		if attrID == testAttrID {
			return "hook-value-for-" + *capturedZoneID, true
		}
		return nil, false
	})

	ep.AddFeature(feature)
	_ = device.AddEndpoint(ep)

	return device
}

func TestProtocolHandler_HandleRead_PassesZoneContext(t *testing.T) {
	var hookCalled bool
	var capturedZoneID string
	device := createDeviceWithReadHook(&hookCalled, &capturedZoneID)

	handler := dispatch.NewProtocolHandler(device)
	handler.SetPeerID("zone-alpha")

	// Read all attributes
	resp := handler.HandleRequest(&wire.Request{
		MessageID:  200,
		Operation:  wire.OpRead,
		EndpointID: 1,
		FeatureID:  uint8(featureTypeForHookTest),
	})

	if !resp.IsSuccess() {
		t.Fatalf("expected success, got status %d", resp.Status)
	}

	if !hookCalled {
		t.Fatal("expected ReadHook to be called during handleRead")
	}

	if capturedZoneID != "zone-alpha" {
		t.Errorf("expected zone ID 'zone-alpha' in context, got %q", capturedZoneID)
	}

	// Verify the hook's return value is used
	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("expected map[uint16]any payload, got %T", resp.Payload)
	}
	if v, exists := payload[testAttrID]; !exists {
		t.Error("expected testAttrID in response")
	} else if v != "hook-value-for-zone-alpha" {
		t.Errorf("expected hook value 'hook-value-for-zone-alpha', got %v", v)
	}

	// Also test specific attribute read path
	hookCalled = false
	capturedZoneID = ""

	resp = handler.HandleRequest(&wire.Request{
		MessageID:  201,
		Operation:  wire.OpRead,
		EndpointID: 1,
		FeatureID:  uint8(featureTypeForHookTest),
		Payload: &wire.ReadPayload{
			AttributeIDs: []uint16{testAttrID},
		},
	})

	if !resp.IsSuccess() {
		t.Fatalf("expected success for specific attribute read, got status %d", resp.Status)
	}

	if !hookCalled {
		t.Fatal("expected ReadHook to be called for specific attribute read")
	}

	if capturedZoneID != "zone-alpha" {
		t.Errorf("expected zone ID 'zone-alpha' for specific read, got %q", capturedZoneID)
	}
}

func TestProtocolHandler_HandleRead_NoPeerID(t *testing.T) {
	var hookCalled bool
	var capturedZoneID string
	device := createDeviceWithReadHook(&hookCalled, &capturedZoneID)

	handler := dispatch.NewProtocolHandler(device)
	// Do NOT set peerID

	resp := handler.HandleRequest(&wire.Request{
		MessageID:  210,
		Operation:  wire.OpRead,
		EndpointID: 1,
		FeatureID:  uint8(featureTypeForHookTest),
	})

	if !resp.IsSuccess() {
		t.Fatalf("expected success, got status %d", resp.Status)
	}

	if !hookCalled {
		t.Fatal("expected ReadHook to be called even without peerID")
	}

	// Zone ID should be empty string (from background context without value)
	if capturedZoneID != "" {
		t.Errorf("expected empty zone ID when no peerID set, got %q", capturedZoneID)
	}

	// The hook still returns a value (with empty zone ID)
	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("expected map[uint16]any payload, got %T", resp.Payload)
	}
	if v, exists := payload[testAttrID]; !exists {
		t.Error("expected testAttrID in response")
	} else if v != "hook-value-for-" {
		t.Errorf("expected hook value 'hook-value-for-', got %v", v)
	}
}

func TestProtocolHandler_HandleSubscribe_PrimingUsesContext(t *testing.T) {
	var hookCalled bool
	var capturedZoneID string
	device := createDeviceWithReadHook(&hookCalled, &capturedZoneID)

	handler := dispatch.NewProtocolHandler(device)
	handler.SetPeerID("zone-beta")

	// Subscribe to all attributes - the priming report should use context-aware reads
	resp := handler.HandleRequest(&wire.Request{
		MessageID:  220,
		Operation:  wire.OpSubscribe,
		EndpointID: 1,
		FeatureID:  uint8(featureTypeForHookTest),
		Payload:    &wire.SubscribePayload{},
	})

	if !resp.IsSuccess() {
		t.Fatalf("expected success, got status %d", resp.Status)
	}

	if !hookCalled {
		t.Fatal("expected ReadHook to be called during subscribe priming")
	}

	if capturedZoneID != "zone-beta" {
		t.Errorf("expected zone ID 'zone-beta' in context, got %q", capturedZoneID)
	}

	// Verify priming report uses hook values
	subResp, ok := resp.Payload.(*wire.SubscribeResponsePayload)
	if !ok {
		t.Fatalf("expected *wire.SubscribeResponsePayload, got %T", resp.Payload)
	}

	if v, exists := subResp.CurrentValues[testAttrID]; !exists {
		t.Error("expected testAttrID in priming report")
	} else if v != "hook-value-for-zone-beta" {
		t.Errorf("expected hook value 'hook-value-for-zone-beta' in priming, got %v", v)
	}

	// Also test subscribe with specific attribute IDs
	hookCalled = false
	capturedZoneID = ""

	resp = handler.HandleRequest(&wire.Request{
		MessageID:  221,
		Operation:  wire.OpSubscribe,
		EndpointID: 1,
		FeatureID:  uint8(featureTypeForHookTest),
		Payload: &wire.SubscribePayload{
			AttributeIDs: []uint16{testAttrID},
		},
	})

	if !resp.IsSuccess() {
		t.Fatalf("expected success for specific subscribe, got status %d", resp.Status)
	}

	if !hookCalled {
		t.Fatal("expected ReadHook to be called for specific attribute subscribe priming")
	}

	if capturedZoneID != "zone-beta" {
		t.Errorf("expected zone ID 'zone-beta' for specific subscribe, got %q", capturedZoneID)
	}
}


// TestProtocolHandler_UnsubscribeRequiresBothEndpointAndFeatureZero
// guards the spec §6.4 invariant: only requests with BOTH endpointID == 0
// AND featureID == 0 enter the unsubscribe path. A pre-fix request with
// endpoint != 0 / feature == 0 was misrouted as unsubscribe.
func TestProtocolHandler_UnsubscribeRequiresBothEndpointAndFeatureZero(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Subscribe with featureID=0 on a real endpoint must NOT be treated as
	// unsubscribe. The dispatcher should fall through to the normal subscribe
	// path and reject feature 0 as INVALID_FEATURE.
	req := &wire.Request{
		MessageID:  101,
		Operation:  wire.OpSubscribe,
		EndpointID: 1, // non-zero — not the unsubscribe sentinel
		FeatureID:  0, // would have triggered the bug pre-fix
		Payload:    &wire.SubscribePayload{},
	}

	resp := handler.HandleRequest(req)

	if resp.IsSuccess() {
		t.Errorf("expected non-success for endpoint=1 feature=0; got status %d (was misrouted as unsubscribe?)", resp.Status)
	}
	if resp.Status != wire.StatusInvalidFeature {
		t.Errorf("expected StatusInvalidFeature for feature=0 subscribe; got %d", resp.Status)
	}
}


// TestProtocolHandler_HandleRead_BareArrayWireForm verifies the spec §4.1
// wire form: request key 5 is a bare CBOR array (the shape mash-go\x27s own
// interaction.Client.Read sends and the shape mash-py controllers send).
// Pre-fix, ExtractReadAttributeIDs returned nil for this shape and the
// dispatcher silently degraded a Read-with-IDs into a Read-all.
func TestProtocolHandler_HandleRead_BareArrayWireForm(t *testing.T) {
	device := createTestDevice()
	handler := dispatch.NewProtocolHandler(device)

	// Encode + decode a Request that carries Payload: []uint16{1, 2}
	// (the shape interaction.Client.Read emits) so the test exercises the
	// real wire-decoded payload, not the typed-form short-circuit.
	original := &wire.Request{
		MessageID:  201,
		Operation:  wire.OpRead,
		EndpointID: 0,
		FeatureID:  featureIDDeviceInfo,
		Payload:    []uint16{1, 2}, // DeviceID + VendorName
	}
	data, err := wire.EncodeRequest(original)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	decoded, err := wire.DecodeRequest(data)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	resp := handler.HandleRequest(decoded)

	if !resp.IsSuccess() {
		t.Fatalf("expected success; got status %d", resp.Status)
	}
	payload, ok := resp.Payload.(map[uint16]any)
	if !ok {
		t.Fatalf("expected map[uint16]any payload, got %T", resp.Payload)
	}
	// Pre-fix, the dispatcher would have returned ALL DeviceInfo attributes
	// because ExtractReadAttributeIDs silently saw zero IDs.
	if len(payload) != 2 {
		t.Errorf("expected exactly 2 requested attributes; got %d (keys %v)", len(payload), payload)
	}
	if _, ok := payload[1]; !ok {
		t.Errorf("expected DeviceID (attr 1) in response; got %v", payload)
	}
	if _, ok := payload[2]; !ok {
		t.Errorf("expected VendorName (attr 2) in response; got %v", payload)
	}
}
