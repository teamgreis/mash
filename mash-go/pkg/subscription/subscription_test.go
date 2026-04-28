package subscription

import (
	"sync"
	"testing"
	"time"
)

func TestSubscriptionBasic(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	if sub.ID != 1 {
		t.Errorf("ID = %d, want 1", sub.ID)
	}
	if sub.FeatureID != 0x0003 {
		t.Errorf("FeatureID = %d, want 3", sub.FeatureID)
	}
	if !sub.IsActive() {
		t.Error("IsActive() = false, want true")
	}
}

func TestSubscriptionDeactivate(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, time.Second, 60*time.Second)

	sub.Deactivate()

	if sub.IsActive() {
		t.Error("IsActive() = true after deactivate, want false")
	}
}

func TestSubscriptionRecordChange(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	// First change starts coalescing window
	isNew := sub.RecordChange(20, int64(5000000))
	if !isNew {
		t.Error("First RecordChange should return true (new window)")
	}

	// Second change in same window
	isNew = sub.RecordChange(20, int64(3000000))
	if isNew {
		t.Error("Second RecordChange should return false (same window)")
	}
}

func TestSubscriptionFilteredAttributes(t *testing.T) {
	// Subscribe to specific attributes
	sub := NewSubscription(1, 0x0003, 1, []uint16{20, 21}, 100*time.Millisecond, time.Second)

	// Change to subscribed attribute
	if !sub.RecordChange(20, int64(5000000)) {
		t.Error("RecordChange for subscribed attr should start window")
	}

	// Change to non-subscribed attribute - should be ignored
	sub.RecordChange(99, int64(1234))

	// Wait for coalescing
	time.Sleep(150 * time.Millisecond)

	notification := sub.GetPendingNotification(false)
	if notification == nil {
		t.Fatal("GetPendingNotification returned nil")
	}

	// Should only have subscribed attribute
	if _, exists := notification[20]; !exists {
		t.Error("Notification should contain attr 20")
	}
	if _, exists := notification[99]; exists {
		t.Error("Notification should NOT contain attr 99 (not subscribed)")
	}
}

func TestSubscriptionCoalescing(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	// Record multiple changes rapidly (within minInterval)
	sub.RecordChange(20, int64(1000000))
	sub.RecordChange(20, int64(2000000))
	sub.RecordChange(20, int64(3000000)) // Last value wins

	// Before minInterval - should return nil
	if notification := sub.GetPendingNotification(false); notification != nil {
		t.Error("GetPendingNotification before minInterval should return nil")
	}

	// Wait for coalescing window
	time.Sleep(150 * time.Millisecond)

	notification := sub.GetPendingNotification(false)
	if notification == nil {
		t.Fatal("GetPendingNotification after minInterval should return notification")
	}

	// Should have last value (3000000)
	if v, ok := notification[20].(int64); !ok || v != 3000000 {
		t.Errorf("Notification value = %v, want 3000000", notification[20])
	}
}

func TestSubscriptionBounceBackSuppression(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	// Set initial priming value
	sub.SetPrimingValues(map[uint16]any{20: int64(5000000)})

	// Record bounce-back: X → Y → X
	sub.RecordChange(20, int64(3000000)) // Change
	sub.RecordChange(20, int64(5000000)) // Back to original

	time.Sleep(150 * time.Millisecond)

	// With bounce-back suppression enabled
	notification := sub.GetPendingNotification(true)
	if notification != nil {
		t.Error("Bounce-back should be suppressed - notification should be nil")
	}
}

func TestSubscriptionBounceBackDisabled(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	// Set initial priming value
	sub.SetPrimingValues(map[uint16]any{20: int64(5000000)})

	// Record bounce-back: X → Y → X
	sub.RecordChange(20, int64(3000000))
	sub.RecordChange(20, int64(5000000)) // Back to original

	time.Sleep(150 * time.Millisecond)

	// With bounce-back suppression disabled
	notification := sub.GetPendingNotification(false)
	if notification == nil {
		t.Fatal("Without bounce-back suppression, notification should be sent")
	}
}

func TestSubscriptionHeartbeat(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, time.Second, 100*time.Millisecond)

	// Initially no heartbeat needed
	if sub.NeedsHeartbeat() {
		t.Error("NeedsHeartbeat() = true immediately after creation")
	}

	// Wait for maxInterval
	time.Sleep(150 * time.Millisecond)

	if !sub.NeedsHeartbeat() {
		t.Error("NeedsHeartbeat() = false after maxInterval")
	}

	// Record heartbeat
	sub.RecordHeartbeat()

	if sub.NeedsHeartbeat() {
		t.Error("NeedsHeartbeat() = true after RecordHeartbeat")
	}
}

func TestSubscriptionMultipleAttributes(t *testing.T) {
	sub := NewSubscription(1, 0x0003, 1, nil, 100*time.Millisecond, time.Second)

	// Set priming values
	sub.SetPrimingValues(map[uint16]any{
		20: int64(5000000),
		21: int64(5000000),
		2:  uint8(1),
	})

	// Change multiple attributes (one bounces back, others don't)
	sub.RecordChange(20, int64(3000000))       // Changed
	sub.RecordChange(21, int64(5000000))       // Bounce-back (same as priming)
	sub.RecordChange(2, uint8(2))              // Changed

	time.Sleep(150 * time.Millisecond)

	notification := sub.GetPendingNotification(true)
	if notification == nil {
		t.Fatal("Expected notification with some changed values")
	}

	// Should contain changed attributes
	if _, exists := notification[20]; !exists {
		t.Error("Notification should contain attr 20 (changed)")
	}
	if _, exists := notification[2]; !exists {
		t.Error("Notification should contain attr 2 (changed)")
	}

	// Should NOT contain bounce-back
	if _, exists := notification[21]; exists {
		t.Error("Notification should NOT contain attr 21 (bounce-back)")
	}
}

func TestManagerSubscribe(t *testing.T) {
	// DEC-086: Subscribe no longer fires a priming notification
	// through the OnNotification callback. The priming values live
	// in the Subscribe Response that the *caller* (the protocol
	// handler / dispatcher) builds. The manager just stores them as
	// the bounce-back baseline.
	m := NewManager()

	currentValues := map[uint16]any{
		20: int64(5000000),
		21: int64(5000000),
	}

	var spuriousNotifications []Notification
	m.OnNotification(func(n Notification) {
		spuriousNotifications = append(spuriousNotifications, n)
	})

	id, err := m.Subscribe(1, 0x0003, nil, time.Second, 60*time.Second, currentValues)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	if id == 0 {
		t.Error("Subscribe() returned ID = 0 (reserved per §3.3 / DEC-086)")
	}

	// DEC-086 forbids the spurious priming wire emission.
	if len(spuriousNotifications) != 0 {
		t.Errorf("Subscribe must NOT fire OnNotification (DEC-086 priming uniqueness); "+
			"got %d notifications", len(spuriousNotifications))
	}

	// The subscription itself MUST have remembered the priming values
	// so subsequent bounce-back checks compare against them.
	sub, getErr := m.Get(id)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	sub.mu.RLock()
	gotLast := len(sub.lastValues)
	sub.mu.RUnlock()
	if gotLast != 2 {
		t.Errorf("Subscription should hold 2 priming values for bounce-back, got %d", gotLast)
	}

	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1", m.Count())
	}
}

func TestManagerUnsubscribe(t *testing.T) {
	m := NewManager()

	id, _ := m.Subscribe(1, 0x0003, nil, time.Second, 60*time.Second, nil)

	err := m.Unsubscribe(id)
	if err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}

	if m.Count() != 0 {
		t.Errorf("Count() = %d after unsubscribe, want 0", m.Count())
	}

	// Unsubscribe again should fail
	err = m.Unsubscribe(id)
	if err != ErrSubscriptionNotFound {
		t.Errorf("Second Unsubscribe() error = %v, want ErrSubscriptionNotFound", err)
	}
}

func TestManagerInvalidInterval(t *testing.T) {
	m := NewManager()

	// maxInterval = 0 should fail
	_, err := m.Subscribe(1, 0x0003, nil, time.Second, 0, nil)
	if err != ErrInvalidInterval {
		t.Errorf("Subscribe with maxInterval=0 error = %v, want ErrInvalidInterval", err)
	}

	// minInterval > maxInterval should fail
	_, err = m.Subscribe(1, 0x0003, nil, 60*time.Second, 10*time.Second, nil)
	if err != ErrInvalidInterval {
		t.Errorf("Subscribe with min>max error = %v, want ErrInvalidInterval", err)
	}
}

func TestManagerAutoCorrectIntervals(t *testing.T) {
	config := DefaultConfig()
	config.AutoCorrectIntervals = true
	m := NewManagerWithConfig(config)

	// minInterval > maxInterval should be auto-corrected
	id, err := m.Subscribe(1, 0x0003, nil, 60*time.Second, 10*time.Second, nil)
	if err != nil {
		t.Fatalf("Subscribe with auto-correct error = %v", err)
	}

	sub, _ := m.Get(id)
	if sub.MinInterval != 10*time.Second {
		t.Errorf("MinInterval = %v, want 10s (auto-corrected)", sub.MinInterval)
	}
	if sub.MaxInterval != 60*time.Second {
		t.Errorf("MaxInterval = %v, want 60s (auto-corrected)", sub.MaxInterval)
	}
}

func TestManagerResourceExhausted(t *testing.T) {
	config := DefaultConfig()
	config.MaxSubscriptions = 2
	m := NewManagerWithConfig(config)

	m.Subscribe(1, 0x0001, nil, time.Second, 60*time.Second, nil)
	m.Subscribe(1, 0x0002, nil, time.Second, 60*time.Second, nil)

	_, err := m.Subscribe(1, 0x0003, nil, time.Second, 60*time.Second, nil)
	if err != ErrResourceExhausted {
		t.Errorf("Third Subscribe error = %v, want ErrResourceExhausted", err)
	}
}

func TestManagerNotifyChange(t *testing.T) {
	m := NewManager()

	currentValues := map[uint16]any{20: int64(5000000)}
	m.Subscribe(1, 0x0003, nil, 100*time.Millisecond, time.Second, currentValues)

	var notifications []Notification
	var mu sync.Mutex
	m.OnNotification(func(n Notification) {
		mu.Lock()
		notifications = append(notifications, n)
		mu.Unlock()
	})

	// Clear priming notification
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	notifications = notifications[:0]
	mu.Unlock()

	// Record change
	m.NotifyChange(1, 0x0003, 20, int64(3000000))

	// Wait for coalescing
	time.Sleep(150 * time.Millisecond)
	m.ProcessNotifications()

	mu.Lock()
	defer mu.Unlock()

	if len(notifications) == 0 {
		t.Fatal("No notification received after change")
	}

	if notifications[0].Attributes[20] != int64(3000000) {
		t.Errorf("Notification value = %v, want 3000000", notifications[0].Attributes[20])
	}
}

func TestManagerHeartbeat(t *testing.T) {
	// DEC-086: heartbeats always carry full current values; the
	// HeartbeatMode toggle was removed. The manager also no longer
	// fires a wire-callback for priming (priming lives in the
	// Subscribe Response only), so this test asserts on the heartbeat
	// notification alone.
	m := NewManager()

	var notifications []Notification
	var mu sync.Mutex
	m.OnNotification(func(n Notification) {
		mu.Lock()
		notifications = append(notifications, n)
		mu.Unlock()
	})

	currentValues := map[uint16]any{20: int64(5000000)}
	// Note: minInterval (50ms) must be <= maxInterval (100ms)
	_, err := m.Subscribe(1, 0x0003, nil, 50*time.Millisecond, 100*time.Millisecond, currentValues)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// Wait for heartbeat
	time.Sleep(150 * time.Millisecond)
	m.ProcessNotifications()

	mu.Lock()
	defer mu.Unlock()

	if len(notifications) == 0 {
		t.Fatal("Expected at least one heartbeat notification, got none")
	}
	last := notifications[len(notifications)-1]
	if !last.IsHeartbeat {
		t.Errorf("Last notification should be heartbeat, got %+v", last)
	}
	// DEC-086: heartbeat MUST carry full current values.
	if len(last.Attributes) == 0 {
		t.Error("Heartbeat must carry current values per DEC-086 / §6.5")
	}
}

func TestManagerClearAll(t *testing.T) {
	m := NewManager()

	m.Subscribe(1, 0x0001, nil, time.Second, 60*time.Second, nil)
	m.Subscribe(1, 0x0002, nil, time.Second, 60*time.Second, nil)
	m.Subscribe(1, 0x0003, nil, time.Second, 60*time.Second, nil)

	if m.Count() != 3 {
		t.Fatalf("Count() = %d before ClearAll, want 3", m.Count())
	}

	m.ClearAll()

	if m.Count() != 0 {
		t.Errorf("Count() = %d after ClearAll, want 0", m.Count())
	}
}

// TestHeartbeatModeString was removed — DEC-086 collapses heartbeat
// to a single (always-full) mode and the HeartbeatMode type itself
// was deleted from subscription.go. See pkg/subscription/manager.go's
// ProcessNotifications heartbeat branch for the current behaviour.

func TestValuesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b any
		want bool
	}{
		{"nil-nil", nil, nil, true},
		{"nil-value", nil, int64(5), false},
		{"int64-same", int64(5), int64(5), true},
		{"int64-diff", int64(5), int64(10), false},
		{"int32-same", int32(5), int32(5), true},
		{"string-same", "hello", "hello", true},
		{"string-diff", "hello", "world", false},
		{"bool-same", true, true, true},
		{"bool-diff", true, false, false},
		{"float64-same", float64(3.14), float64(3.14), true},
		{"type-mismatch", int64(5), int32(5), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := valuesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("valuesEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestPartialAttributeSubscription(t *testing.T) {
	// DEC-086: priming lives in the Subscribe Response, not in an
	// OnNotification callback. Assert the *subscription* has the
	// expected (filtered) priming baseline rather than peeking via a
	// callback that no longer fires.
	m := NewManager()

	// Subscribe to only one attribute
	currentValues := map[uint16]any{
		20: int64(5000000),
		21: int64(3000000),
		2:  uint8(1),
	}

	id, err := m.Subscribe(1, 0x0003, []uint16{20}, 100*time.Millisecond, time.Second, currentValues)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	sub, _ := m.Get(id)
	sub.mu.RLock()
	last := make(map[uint16]any, len(sub.lastValues))
	for k, v := range sub.lastValues {
		last[k] = v
	}
	sub.mu.RUnlock()

	if len(last) != 1 {
		t.Errorf("Priming should have 1 attribute (filtered), got %d", len(last))
	}
	if _, exists := last[20]; !exists {
		t.Error("Priming should contain attr 20")
	}
}
