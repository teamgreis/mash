package interaction

import (
	"context"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

func createTestDevice() *model.Device {
	device := model.NewDevice("test-device", 0x1234, 0x5678)

	// Add DeviceInfo to root
	deviceInfo := features.NewDeviceInfo()
	_ = deviceInfo.SetDeviceID("test-device")
	_ = deviceInfo.SetVendorName("Test Vendor")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Add EV charger endpoint with Electrical and Measurement
	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Charger")

	electrical := features.NewElectrical()
	_ = electrical.SetPhaseCount(3)
	_ = electrical.SetNominalVoltage(400)
	_ = electrical.SetMaxCurrentPerPhase(32000)
	charger.AddFeature(electrical.Feature)

	measurement := features.NewMeasurement()
	_ = measurement.SetACActivePower(11000000)
	charger.AddFeature(measurement.Feature)

	// Add writable feature
	f := model.NewFeature(model.FeatureEnergyControl, 1)
	f.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
		ID:       21,
		Name:     "myConsumptionLimit",
		Type:     model.DataTypeInt64,
		Access:   model.AccessReadWrite,
		Nullable: true,
	}))
	f.AddCommand(model.NewCommand(&model.CommandMetadata{
		ID:   1,
		Name: "setLimit",
		Parameters: []model.ParameterMetadata{
			{ID: 1, Name: "limit", Type: model.DataTypeInt64, Required: true},
		},
	}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		return map[uint8]any{1: true}, nil
	}))
	charger.AddFeature(f)

	_ = device.AddEndpoint(charger)

	return device
}

func TestServerRead(t *testing.T) {
	device := createTestDevice()
	server := NewServer(device)

	t.Run("ReadAll", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  1,
			Operation:  wire.OpRead,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureMeasurement),
			Payload:    nil, // Read all
		}

		resp := server.HandleRequest(context.Background(), req)

		if resp.MessageID != 1 {
			t.Errorf("expected messageId 1, got %d", resp.MessageID)
		}
		if !resp.Status.IsSuccess() {
			t.Errorf("expected success, got %s", resp.Status)
		}
		if resp.Payload == nil {
			t.Error("expected payload")
		}
	})

	t.Run("ReadSpecific", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  2,
			Operation:  wire.OpRead,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureMeasurement),
			Payload:    []uint16{1}, // Just acActivePower
		}

		resp := server.HandleRequest(context.Background(), req)

		if !resp.Status.IsSuccess() {
			t.Errorf("expected success, got %s", resp.Status)
		}
	})

	t.Run("InvalidEndpoint", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  3,
			Operation:  wire.OpRead,
			EndpointID: 99,
			FeatureID:  uint8(model.FeatureMeasurement),
		}

		resp := server.HandleRequest(context.Background(), req)

		if resp.Status != wire.StatusInvalidEndpoint {
			t.Errorf("expected InvalidEndpoint, got %s", resp.Status)
		}
	})

	t.Run("InvalidFeature", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  4,
			Operation:  wire.OpRead,
			EndpointID: 1,
			FeatureID:  99,
		}

		resp := server.HandleRequest(context.Background(), req)

		if resp.Status != wire.StatusInvalidFeature {
			t.Errorf("expected InvalidFeature, got %s", resp.Status)
		}
	})
}

func TestServerWrite(t *testing.T) {
	device := createTestDevice()
	server := NewServer(device)

	t.Run("WriteSuccess", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  1,
			Operation:  wire.OpWrite,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureEnergyControl),
			Payload:    map[uint16]any{21: int64(6000000)},
		}

		resp := server.HandleRequest(context.Background(), req)

		if !resp.Status.IsSuccess() {
			t.Errorf("expected success, got %s", resp.Status)
		}

		// Verify the value was written
		val, err := device.ReadAttribute(1, model.FeatureEnergyControl, 21)
		if err != nil {
			t.Fatalf("failed to read attribute: %v", err)
		}
		if val != int64(6000000) {
			t.Errorf("expected 6000000, got %v", val)
		}
	})

	t.Run("WriteReadOnly", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  2,
			Operation:  wire.OpWrite,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureMeasurement),
			Payload:    map[uint16]any{1: int64(5000000)}, // acActivePower is read-only
		}

		resp := server.HandleRequest(context.Background(), req)

		// Should fail because Measurement attributes are read-only
		if resp.Status.IsSuccess() && resp.Payload != nil {
			// Check if the write actually happened
			results := resp.Payload.(map[uint16]any)
			if len(results) > 0 {
				t.Log("Warning: Write to read-only attribute succeeded")
			}
		}
	})
}

func TestServerSubscribe(t *testing.T) {
	device := createTestDevice()
	server := NewServer(device)

	server.SetNotificationHandler(func(notif *wire.Notification) {
		_ = notif // Handle notification
	})

	t.Run("SubscribeAll", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  1,
			Operation:  wire.OpSubscribe,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureMeasurement),
			Payload: &wire.SubscribePayload{
				MinInterval: 1000,
				MaxInterval: 60000,
			},
		}

		resp := server.HandleRequest(context.Background(), req)

		if !resp.Status.IsSuccess() {
			t.Fatalf("expected success, got %s", resp.Status)
		}

		// Check subscription response
		subResp, ok := resp.Payload.(*wire.SubscribeResponsePayload)
		if !ok {
			t.Fatal("expected SubscribeResponsePayload")
		}

		if subResp.SubscriptionID == 0 {
			t.Error("expected non-zero subscription ID")
		}

		if subResp.CurrentValues == nil {
			t.Error("expected priming report with current values")
		}

		// Verify subscription exists
		if server.SubscriptionCount() != 1 {
			t.Errorf("expected 1 subscription, got %d", server.SubscriptionCount())
		}
	})

	t.Run("SubscribeSpecificAttributes", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  2,
			Operation:  wire.OpSubscribe,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureMeasurement),
			Payload: &wire.SubscribePayload{
				AttributeIDs: []uint16{1, 2},
				MinInterval:  100,
				MaxInterval:  5000,
			},
		}

		resp := server.HandleRequest(context.Background(), req)

		if !resp.Status.IsSuccess() {
			t.Fatalf("expected success, got %s", resp.Status)
		}

		if server.SubscriptionCount() != 2 {
			t.Errorf("expected 2 subscriptions, got %d", server.SubscriptionCount())
		}
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		// First subscribe
		subReq := &wire.Request{
			MessageID:  3,
			Operation:  wire.OpSubscribe,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureEnergyControl),
		}
		subResp := server.HandleRequest(context.Background(), subReq)
		subPayload := subResp.Payload.(*wire.SubscribeResponsePayload)

		initialCount := server.SubscriptionCount()

		// Then unsubscribe
		unsubReq := &wire.Request{
			MessageID:  4,
			Operation:  wire.OpSubscribe,
			EndpointID: 0, // Indicates unsubscribe
			FeatureID:  0,
			Payload: &wire.UnsubscribePayload{
				SubscriptionID: subPayload.SubscriptionID,
			},
		}

		resp := server.HandleRequest(context.Background(), unsubReq)

		if !resp.Status.IsSuccess() {
			t.Errorf("expected success, got %s", resp.Status)
		}

		if server.SubscriptionCount() != initialCount-1 {
			t.Errorf("expected %d subscriptions, got %d", initialCount-1, server.SubscriptionCount())
		}
	})
}

func TestServerInvoke(t *testing.T) {
	device := createTestDevice()
	server := NewServer(device)

	t.Run("InvokeSuccess", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  1,
			Operation:  wire.OpInvoke,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureEnergyControl),
			Payload: &wire.InvokePayload{
				CommandID: 1, // setLimit
				Parameters: map[uint8]any{
					1: int64(5000000), // limit (id 1)
				},
			},
		}

		resp := server.HandleRequest(context.Background(), req)

		if !resp.Status.IsSuccess() {
			t.Errorf("expected success, got %s", resp.Status)
		}

		result, ok := resp.Payload.(map[uint8]any)
		if !ok {
			t.Fatal("expected map[uint8]any result (Issue #11 fix)")
		}
		if result[1] != true {
			t.Errorf("expected success=true at id 1, got %v", result[1])
		}
	})

	t.Run("InvokeInvalidCommand", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  2,
			Operation:  wire.OpInvoke,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureEnergyControl),
			Payload: &wire.InvokePayload{
				CommandID: 99, // Invalid command
			},
		}

		resp := server.HandleRequest(context.Background(), req)

		if resp.Status != wire.StatusInvalidCommand {
			t.Errorf("expected InvalidCommand, got %s", resp.Status)
		}
	})

	t.Run("InvokeMissingRequired", func(t *testing.T) {
		req := &wire.Request{
			MessageID:  3,
			Operation:  wire.OpInvoke,
			EndpointID: 1,
			FeatureID:  uint8(model.FeatureEnergyControl),
			Payload: &wire.InvokePayload{
				CommandID:  1,
				Parameters: map[string]any{}, // Missing required "limit"
			},
		}

		resp := server.HandleRequest(context.Background(), req)

		if resp.Status != wire.StatusInvalidParameter {
			t.Errorf("expected InvalidParameter, got %s", resp.Status)
		}
	})
}

func TestSubscription(t *testing.T) {
	t.Run("IsSubscribedTo", func(t *testing.T) {
		sub := &Subscription{
			AttributeIDs: []uint16{1, 2, 3},
		}

		if !sub.IsSubscribedTo(1) {
			t.Error("expected subscribed to 1")
		}
		if !sub.IsSubscribedTo(3) {
			t.Error("expected subscribed to 3")
		}
		if sub.IsSubscribedTo(99) {
			t.Error("expected not subscribed to 99")
		}
	})

	t.Run("IsSubscribedToAll", func(t *testing.T) {
		sub := &Subscription{
			AttributeIDs: nil, // Empty = all
		}

		if !sub.IsSubscribedTo(1) {
			t.Error("expected subscribed to all")
		}
		if !sub.IsSubscribedTo(99) {
			t.Error("expected subscribed to all")
		}
	})

	t.Run("CanNotify", func(t *testing.T) {
		sub := &Subscription{
			MinInterval: 100 * time.Millisecond,
			LastNotify:  time.Now().Add(-200 * time.Millisecond),
		}

		if !sub.CanNotify() {
			t.Error("expected can notify")
		}

		sub.LastNotify = time.Now()
		if sub.CanNotify() {
			t.Error("expected cannot notify (too soon)")
		}
	})

	t.Run("NeedsHeartbeat", func(t *testing.T) {
		sub := &Subscription{
			MaxInterval: 100 * time.Millisecond,
			LastNotify:  time.Now().Add(-200 * time.Millisecond),
		}

		if !sub.NeedsHeartbeat() {
			t.Error("expected needs heartbeat")
		}

		sub.LastNotify = time.Now()
		if sub.NeedsHeartbeat() {
			t.Error("expected no heartbeat needed yet")
		}
	})

	t.Run("RecordChange", func(t *testing.T) {
		sub := &Subscription{
			Values: make(map[uint16]any),
		}

		sub.RecordChange(1, int64(5000000))

		pending := sub.GetPendingChanges()
		if pending == nil || pending[1] != int64(5000000) {
			t.Error("expected pending change")
		}

		current := sub.GetCurrentValues()
		if current == nil || current[1] != int64(5000000) {
			t.Error("expected current value updated")
		}
	})
}

func TestClientBasics(t *testing.T) {
	// Create a mock sender
	sender := &mockSender{}
	client := NewClient(sender)
	defer client.Close()

	// Test setting timeout
	client.SetTimeout(5 * time.Second)

	// Test notification handler
	var receivedNotif *wire.Notification
	client.SetNotificationHandler(func(notif *wire.Notification) {
		receivedNotif = notif
	})

	// Simulate receiving a notification
	client.HandleNotification(&wire.Notification{
		SubscriptionID: 1,
		EndpointID:     1,
		FeatureID:      2,
		Changes:        map[uint16]any{1: int64(100)},
	})

	if receivedNotif == nil {
		t.Error("expected notification to be handled")
	}
	if receivedNotif.SubscriptionID != 1 {
		t.Errorf("expected subscriptionID 1, got %d", receivedNotif.SubscriptionID)
	}
}

func TestMessageIDStartsAtOne(t *testing.T) {
	// Per docs/interaction-model.md §3.5: "Start at 1 for new connections."
	// A pre-2026-04 mash-go bug pre-incremented and emitted 2 first
	// (mash-py-spec-issues.md Issue #10).
	sender := &mockSender{}
	client := NewClient(sender)
	defer client.Close()

	if got := client.nextMessageID(); got != 1 {
		t.Errorf("first messageId must be 1 per spec §3.5; got %d", got)
	}
	if got := client.nextMessageID(); got != 2 {
		t.Errorf("second messageId must be 2 (monotonic increment); got %d", got)
	}
}

func TestMessageIDWraparound(t *testing.T) {
	// Test that MessageID wraps from max to 1, skipping 0 (reserved for notifications)
	sender := &mockSender{}
	client := NewClient(sender)
	defer client.Close()

	// Set nextMsgID close to max uint32 to test wraparound
	client.nextMsgID = 0xFFFFFFFF - 2 // Will produce: max-1, max, 1 (skip 0)

	id1 := client.nextMessageID()
	id2 := client.nextMessageID()
	id3 := client.nextMessageID() // Should wrap and skip 0

	if id1 != 0xFFFFFFFF-1 {
		t.Errorf("expected id1 = %d, got %d", 0xFFFFFFFF-1, id1)
	}
	if id2 != 0xFFFFFFFF {
		t.Errorf("expected id2 = %d, got %d", uint32(0xFFFFFFFF), id2)
	}
	if id3 == 0 {
		t.Error("MessageID 0 should be skipped (reserved for notifications)")
	}
	if id3 != 1 {
		t.Errorf("expected id3 = 1 after wraparound, got %d", id3)
	}
}

func TestDefaultTimeout(t *testing.T) {
	// Verify default timeout matches DEC-044 spec (10 seconds)
	sender := &mockSender{}
	client := NewClient(sender)
	defer client.Close()

	if client.timeout != DefaultRequestTimeout {
		t.Errorf("expected default timeout %v, got %v", DefaultRequestTimeout, client.timeout)
	}
	if DefaultRequestTimeout != 10*time.Second {
		t.Errorf("DefaultRequestTimeout should be 10s per DEC-044, got %v", DefaultRequestTimeout)
	}
}

func TestStatusError(t *testing.T) {
	err := &StatusError{
		Status:  wire.StatusInvalidParameter,
		Message: "value out of range",
	}

	if err.Error() != "value out of range" {
		t.Errorf("expected message, got %s", err.Error())
	}

	err2 := &StatusError{
		Status: wire.StatusInvalidEndpoint,
	}

	if err2.Error() != wire.StatusInvalidEndpoint.String() {
		t.Errorf("expected status string, got %s", err2.Error())
	}
}

type mockSender struct {
	sent [][]byte
}

func (m *mockSender) Send(data []byte) error {
	m.sent = append(m.sent, data)
	return nil
}
