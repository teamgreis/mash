package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/pkg/discovery/mocks"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// testZoneCtx returns a context with TEST zone type set, required for
// TestControl command authorization.
func testZoneCtx() context.Context {
	ctx := zonecontext.ContextWithCallerZoneID(context.Background(), "test-zone")
	return zonecontext.ContextWithCallerZoneType(ctx, cert.ZoneTypeTest)
}

// =============================================================================
// Commissioning Window Timing Constants (DEC-048)
// =============================================================================
// These tests verify the service-level defaults align with DEC-048.

// TestServiceDefaultCommissioningWindow verifies that DefaultDeviceConfig uses
// the correct commissioning window duration per DEC-048.
func TestServiceDefaultCommissioningWindow(t *testing.T) {
	config := DefaultDeviceConfig()

	// DEC-048: Default config should use 15-minute window
	// (aligned with discovery.CommissioningWindowDuration)
	want := 15 * time.Minute
	if config.CommissioningWindowDuration != want {
		t.Errorf("DefaultDeviceConfig().CommissioningWindowDuration = %v, want %v",
			config.CommissioningWindowDuration, want)
	}
}

// TestCommissioningWindowDurationAlignedWithDiscovery verifies that the service
// default matches the discovery package constant.
func TestCommissioningWindowDurationAlignedWithDiscovery(t *testing.T) {
	config := DefaultDeviceConfig()

	// Service default should match discovery constant
	if config.CommissioningWindowDuration != discovery.CommissioningWindowDuration {
		t.Errorf("Service default (%v) does not match discovery constant (%v)",
			config.CommissioningWindowDuration, discovery.CommissioningWindowDuration)
	}
}

// TestCommissioningWindowTimeoutEmitsEvent verifies that when the commissioning
// window expires, an EventCommissioningClosed event is emitted with Reason "timeout".
func TestCommissioningWindowTimeoutEmitsEvent(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.CommissioningWindowDuration = 50 * time.Millisecond // Short duration for testing

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Once()
	advertiser.EXPECT().StopCommissionable().Return(nil).Once()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Track events
	var receivedEvent *Event
	var eventMu sync.Mutex
	eventCh := make(chan struct{}, 1)
	svc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if e.Type == EventCommissioningClosed {
			eventCopy := e
			receivedEvent = &eventCopy
			select {
			case eventCh <- struct{}{}:
			default:
			}
		}
	})

	// Start service
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Wait for the commissioning window to expire
	select {
	case <-eventCh:
		// Event received
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for EventCommissioningClosed")
	}

	eventMu.Lock()
	defer eventMu.Unlock()

	if receivedEvent == nil {
		t.Fatal("expected to receive EventCommissioningClosed")
	}

	if receivedEvent.Type != EventCommissioningClosed {
		t.Errorf("expected EventCommissioningClosed, got %v", receivedEvent.Type)
	}

	if receivedEvent.Reason != "timeout" {
		t.Errorf("expected Reason='timeout', got %q", receivedEvent.Reason)
	}
}

// TestCommissioningWindowSuccessfulCommissionEmitsEvent verifies that when
// commissioning succeeds (ExitCommissioningMode is called), an EventCommissioningClosed
// event is emitted with Reason "commissioned".
func TestCommissioningWindowSuccessfulCommissionEmitsEvent(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Once()
	advertiser.EXPECT().StopCommissionable().Return(nil).Once()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Track events
	var receivedEvent *Event
	var eventMu sync.Mutex
	svc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if e.Type == EventCommissioningClosed {
			eventCopy := e
			receivedEvent = &eventCopy
		}
	})

	// Start service
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Exit commissioning mode (simulating successful commission)
	if err := svc.ExitCommissioningMode(); err != nil {
		t.Fatalf("ExitCommissioningMode failed: %v", err)
	}

	// Wait for event to be processed
	time.Sleep(50 * time.Millisecond)

	eventMu.Lock()
	defer eventMu.Unlock()

	if receivedEvent == nil {
		t.Fatal("expected to receive EventCommissioningClosed")
	}

	if receivedEvent.Type != EventCommissioningClosed {
		t.Errorf("expected EventCommissioningClosed, got %v", receivedEvent.Type)
	}

	if receivedEvent.Reason != "commissioned" {
		t.Errorf("expected Reason='commissioned', got %q", receivedEvent.Reason)
	}
}

// TestCommissioningWindowTimeoutStopsMDNSAdvertisement verifies that when the
// commissioning window expires, the mDNS commissionable advertisement is stopped.
func TestCommissioningWindowTimeoutStopsMDNSAdvertisement(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.CommissioningWindowDuration = 50 * time.Millisecond // Short duration for testing

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser - verify StopCommissionable is called
	stopCalled := make(chan struct{}, 1)
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Once()
	advertiser.EXPECT().StopCommissionable().Run(func() {
		select {
		case stopCalled <- struct{}{}:
		default:
		}
	}).Return(nil).Once()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Start service
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Wait for StopCommissionable to be called
	select {
	case <-stopCalled:
		// StopCommissionable was called as expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for StopCommissionable to be called")
	}
}

// TestCommissioningWindowStateTransitions verifies the correct state transitions
// when the commissioning window expires.
func TestCommissioningWindowStateTransitions(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.CommissioningWindowDuration = 50 * time.Millisecond

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Once()
	advertiser.EXPECT().StopCommissionable().Return(nil).Once()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Track discovery state changes
	var stateChanges []discovery.DiscoveryState
	var stateMu sync.Mutex
	svc.mu.Lock()
	dm := svc.discoveryManager
	svc.mu.Unlock()
	dm.OnStateChange(func(_, new discovery.DiscoveryState) {
		stateMu.Lock()
		stateChanges = append(stateChanges, new)
		stateMu.Unlock()
	})

	// Start service
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Wait for timeout and state transition
	time.Sleep(200 * time.Millisecond)

	stateMu.Lock()
	defer stateMu.Unlock()

	// Should have transitioned to CommissioningOpen then to Uncommissioned
	// (since no zones are connected)
	if len(stateChanges) < 2 {
		t.Fatalf("expected at least 2 state changes, got %d: %v", len(stateChanges), stateChanges)
	}

	// First state change should be to CommissioningOpen
	if stateChanges[0] != discovery.StateCommissioningOpen {
		t.Errorf("expected first state change to StateCommissioningOpen, got %v", stateChanges[0])
	}

	// Second state change should be to Uncommissioned (no zones connected)
	if stateChanges[1] != discovery.StateUncommissioned {
		t.Errorf("expected second state change to StateUncommissioned, got %v", stateChanges[1])
	}
}

// TestCommissioningWindowDoesNotFireAfterSuccessfulCommission verifies that
// the timeout callback does not fire if commissioning succeeds before the window expires.
func TestCommissioningWindowDoesNotFireAfterSuccessfulCommission(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.CommissioningWindowDuration = 100 * time.Millisecond

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Once()
	advertiser.EXPECT().StopCommissionable().Return(nil).Once() // Only once for ExitCommissioningMode
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Track events - count how many times we get "timeout"
	var timeoutCount int
	var eventMu sync.Mutex
	svc.OnEvent(func(e Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if e.Type == EventCommissioningClosed && e.Reason == "timeout" {
			timeoutCount++
		}
	})

	// Start service
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// Exit commissioning mode before timeout (simulating successful commission)
	if err := svc.ExitCommissioningMode(); err != nil {
		t.Fatalf("ExitCommissioningMode failed: %v", err)
	}

	// Wait longer than the commissioning window
	time.Sleep(200 * time.Millisecond)

	eventMu.Lock()
	defer eventMu.Unlock()

	if timeoutCount > 0 {
		t.Errorf("expected no timeout events after successful commission, got %d", timeoutCount)
	}
}

// TestTestModeDoesNotExtendCommissioningWindow verifies that TestMode no longer
// overrides the commissioning window duration to 24h. The window should remain
// at the configured default (15 minutes) regardless of test mode.
func TestTestModeDoesNotExtendCommissioningWindow(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.TestMode = true

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	want := 15 * time.Minute
	if svc.config.CommissioningWindowDuration != want {
		t.Errorf("TestMode CommissioningWindowDuration = %v, want %v (should NOT be 24h)",
			svc.config.CommissioningWindowDuration, want)
	}
}

// TestSetCommissioningWindowDurationHandler verifies the TestControl command
// for setting the commissioning window duration.
func TestSetCommissioningWindowDurationHandler(t *testing.T) {
	device := model.NewDevice("test-device", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.TestMode = true
	config.TestEnableKey = "00112233445566778899aabbccddeeff"

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Set up mock advertiser so SetAdvertiser creates a DiscoveryManager.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	dm := svc.DiscoveryManager()
	if dm == nil {
		t.Fatal("DiscoveryManager is nil after SetAdvertiser")
	}

	t.Run("ValidDuration", func(t *testing.T) {
		dm.SetCommissioningWindowDuration(15 * time.Minute) // reset

		tc := newTestControlFeature(t)
		svc.RegisterSetCommissioningWindowDurationHandler(tc)

		_, err := tc.InvokeCommand(testZoneCtx(), 2, map[uint8]any{
			features.SetCommissioningWindowDurationParamEnableKey:       "00112233445566778899aabbccddeeff",
			features.SetCommissioningWindowDurationParamDurationSeconds: uint32(20),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}

		got := dm.CommissioningWindowDuration()
		if got != 20*time.Second {
			t.Errorf("CommissioningWindowDuration = %v, want 20s", got)
		}
	})

	t.Run("WrongEnableKey", func(t *testing.T) {
		tc := newTestControlFeature(t)
		svc.RegisterSetCommissioningWindowDurationHandler(tc)

		_, err := tc.InvokeCommand(testZoneCtx(), 2, map[uint8]any{
			features.SetCommissioningWindowDurationParamEnableKey:       "wrong-key",
			features.SetCommissioningWindowDurationParamDurationSeconds: uint32(20),
		})
		if err == nil {
			t.Error("expected error for wrong enableKey, got nil")
		}
	})

	t.Run("ClampBelowMinimum", func(t *testing.T) {
		dm.SetCommissioningWindowDuration(15 * time.Minute) // reset

		tc := newTestControlFeature(t)
		svc.RegisterSetCommissioningWindowDurationHandler(tc)

		_, _ = tc.InvokeCommand(testZoneCtx(), 2, map[uint8]any{
			features.SetCommissioningWindowDurationParamEnableKey:       "00112233445566778899aabbccddeeff",
			features.SetCommissioningWindowDurationParamDurationSeconds: uint32(1),
		})

		got := dm.CommissioningWindowDuration()
		if got != 3*time.Second {
			t.Errorf("CommissioningWindowDuration = %v, want 3s (clamped)", got)
		}
	})

	t.Run("ClampAboveMaximum", func(t *testing.T) {
		dm.SetCommissioningWindowDuration(15 * time.Minute) // reset

		tc := newTestControlFeature(t)
		svc.RegisterSetCommissioningWindowDurationHandler(tc)

		_, _ = tc.InvokeCommand(testZoneCtx(), 2, map[uint8]any{
			features.SetCommissioningWindowDurationParamEnableKey:       "00112233445566778899aabbccddeeff",
			features.SetCommissioningWindowDurationParamDurationSeconds: uint32(20000),
		})

		got := dm.CommissioningWindowDuration()
		if got != 10800*time.Second {
			t.Errorf("CommissioningWindowDuration = %v, want 10800s (clamped)", got)
		}
	})
}

// TestSetAdvertiser_TimeoutClosesCommissioningOpen verifies that the
// OnCommissioningTimeout callback registered by SetAdvertiser() properly
// sets commissioningOpen to false when the window expires. This mirrors
// the full callback from Start(). Without the fix, commissioningOpen
// stays stale at true after timeout.
func TestSetAdvertiser_TimeoutClosesCommissioningOpen(t *testing.T) {
	device := model.NewDevice("test-device-timeout", 0x1234, 0x5678)
	config := validDeviceConfig()
	config.CommissioningWindowDuration = 100 * time.Millisecond // Short window

	svc, err := NewDeviceService(device, config)
	if err != nil {
		t.Fatalf("NewDeviceService failed: %v", err)
	}

	// Call SetAdvertiser BEFORE Start -- this is the code path under test.
	advertiser := mocks.NewMockAdvertiser(t)
	advertiser.EXPECT().AdvertiseCommissionable(mock.Anything, mock.Anything).Return(nil).Maybe()
	advertiser.EXPECT().StopCommissionable().Return(nil).Maybe()
	advertiser.EXPECT().StopAll().Return().Maybe()
	svc.SetAdvertiser(advertiser)

	// Track timeout event
	eventCh := make(chan struct{}, 1)
	svc.OnEvent(func(e Event) {
		if e.Type == EventCommissioningClosed && e.Reason == "timeout" {
			select {
			case eventCh <- struct{}{}:
			default:
			}
		}
	})

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	// Enter commissioning mode
	if err := svc.EnterCommissioningMode(); err != nil {
		t.Fatalf("EnterCommissioningMode failed: %v", err)
	}

	// commissioningOpen should be true now
	if !svc.commissioningOpen.Load() {
		t.Fatal("commissioningOpen should be true after EnterCommissioningMode")
	}

	// Wait for the window to expire
	select {
	case <-eventCh:
		// Timeout event received
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for EventCommissioningClosed")
	}

	// Key assertion: commissioningOpen must be false after timeout.
	// Without the fix, SetAdvertiser's minimal callback doesn't reset this.
	if svc.commissioningOpen.Load() {
		t.Error("commissioningOpen should be false after commissioning window timeout (SetAdvertiser callback incomplete)")
	}
}

// newTestControlFeature creates a TestControl feature for testing.
func newTestControlFeature(t *testing.T) *features.TestControl {
	t.Helper()
	return features.NewTestControl()
}
