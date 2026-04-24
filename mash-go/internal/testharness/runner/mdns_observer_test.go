package runner

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// testBrowser: a minimal Browser implementation for observer tests.
// Tests control what the observer sees by sending into the exposed channels.
// ---------------------------------------------------------------------------

type testBrowser struct {
	mu      sync.Mutex
	stopped bool

	// Commissionable browse channels
	commAdded   chan *discovery.CommissionableService
	commRemoved chan *discovery.CommissionableService

	// Operational browse channel
	opAdded chan *discovery.OperationalService

	// Pairing request callback (set by BrowsePairingRequests)
	pairingCallback func(discovery.PairingRequestService)

	// Tracking which sessions were started
	commStarted atomic.Bool
	opStarted   atomic.Bool
	stopCalled  atomic.Bool
}

func newTestBrowser() *testBrowser {
	return &testBrowser{
		commAdded:   make(chan *discovery.CommissionableService, 100),
		commRemoved: make(chan *discovery.CommissionableService, 100),
		opAdded:     make(chan *discovery.OperationalService, 100),
	}
}

func (b *testBrowser) BrowseCommissionable(ctx context.Context) (added, removed <-chan *discovery.CommissionableService, err error) {
	b.commStarted.Store(true)
	// Wrap channels so they close when ctx is cancelled
	addedOut := make(chan *discovery.CommissionableService)
	removedOut := make(chan *discovery.CommissionableService)
	go func() {
		defer close(addedOut)
		for {
			select {
			case <-ctx.Done():
				return
			case svc, ok := <-b.commAdded:
				if !ok {
					return
				}
				select {
				case addedOut <- svc:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	go func() {
		defer close(removedOut)
		for {
			select {
			case <-ctx.Done():
				return
			case svc, ok := <-b.commRemoved:
				if !ok {
					return
				}
				select {
				case removedOut <- svc:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return addedOut, removedOut, nil
}

func (b *testBrowser) BrowseOperational(ctx context.Context, _ string) (<-chan *discovery.OperationalService, error) {
	b.opStarted.Store(true)
	out := make(chan *discovery.OperationalService)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case svc, ok := <-b.opAdded:
				if !ok {
					return
				}
				select {
				case out <- svc:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (b *testBrowser) BrowsePairingRequests(ctx context.Context, callback func(discovery.PairingRequestService)) error {
	b.mu.Lock()
	b.pairingCallback = callback
	b.mu.Unlock()
	return nil
}

func (b *testBrowser) FindByDiscriminator(_ context.Context, _ uint16) (*discovery.CommissionableService, error) {
	return nil, nil
}

func (b *testBrowser) FindAllByDiscriminator(_ context.Context, _ uint16) ([]*discovery.CommissionableService, error) {
	return nil, nil
}

func (b *testBrowser) Stop() {
	b.stopCalled.Store(true)
}

// sendPairingRequest invokes the stored callback if set.
func (b *testBrowser) sendPairingRequest(svc discovery.PairingRequestService) {
	b.mu.Lock()
	cb := b.pairingCallback
	b.mu.Unlock()
	if cb != nil {
		cb(svc)
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func noopDebugf(string, ...any) {}

// waitForCondition polls until f returns true, failing after timeout.
func waitForCondition(t *testing.T, timeout time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// ---------------------------------------------------------------------------
// Group A: Observer Core
// ---------------------------------------------------------------------------

func TestObserver_Snapshot_EmptyBeforeAnyBrowse(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Snapshot with a specific type should lazily start a session,
	// but return empty since no services have arrived yet.
	snap := obs.Snapshot("commissionable")
	assert.Empty(t, snap)
	assert.True(t, tb.commStarted.Load(), "commissionable session should have started lazily")
}

func TestObserver_Snapshot_AccumulatesCommissionable(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Send 2 commissionable services
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-001",
		Host:          "device1.local",
		Port:          8443,
		Discriminator: 1234,
		Brand:         "TestBrand",
	}
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-002",
		Host:          "device2.local",
		Port:          8443,
		Discriminator: 5678,
	}

	// Wait for both to appear
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 2
	})
	require.NoError(t, err)
	assert.Len(t, snap, 2)

	// Verify service details
	names := map[string]bool{}
	for _, svc := range snap {
		names[svc.InstanceName] = true
		assert.Equal(t, discovery.ServiceTypeCommissionable, svc.ServiceType)
	}
	assert.True(t, names["MASH-001"])
	assert.True(t, names["MASH-002"])
}

func TestObserver_Snapshot_AccumulatesOperational(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zone1-dev1",
		Host:         "device1.local",
		Port:         8443,
		ZoneID:       "AABB",
		DeviceID:     "1122",
	}
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zone1-dev2",
		Host:         "device2.local",
		Port:         8443,
		ZoneID:       "AABB",
		DeviceID:     "3344",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "operational", func(svcs []discoveredService) bool {
		return len(svcs) >= 2
	})
	require.NoError(t, err)
	assert.Len(t, snap, 2)
	for _, svc := range snap {
		assert.Equal(t, discovery.ServiceTypeOperational, svc.ServiceType)
		assert.Equal(t, "AABB", svc.TXTRecords["ZI"])
	}
}

func TestObserver_Snapshot_FiltersByServiceType(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Send one commissionable and one operational
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-COMM", Discriminator: 1000,
	}

	// Wait for commissionable to arrive first
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Now send operational
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zone-op", ZoneID: "AA", DeviceID: "BB",
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	_, err = obs.WaitFor(ctx2, "operational", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Filter by commissionable
	commSnap := obs.Snapshot("commissionable")
	assert.Len(t, commSnap, 1)
	assert.Equal(t, "MASH-COMM", commSnap[0].InstanceName)

	// Filter by operational
	opSnap := obs.Snapshot("operational")
	assert.Len(t, opSnap, 1)
	assert.Equal(t, "zone-op", opSnap[0].InstanceName)

	// All
	allSnap := obs.Snapshot("")
	assert.Len(t, allSnap, 2)
}

func TestObserver_Removal_UpdatesSnapshot(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Add a service
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-DEL", Discriminator: 100,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Remove it
	tb.commRemoved <- &discovery.CommissionableService{InstanceName: "MASH-DEL"}

	// Wait for removal
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	snap, err := obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) == 0
	})
	require.NoError(t, err)
	assert.Empty(t, snap)
}

func TestObserver_Stop_ClosesAllSessions(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)

	// Trigger sessions for commissionable and operational
	_ = obs.Snapshot("commissionable")
	_ = obs.Snapshot("operational")

	waitForCondition(t, time.Second, func() bool {
		return tb.commStarted.Load() && tb.opStarted.Load()
	})

	obs.Stop()
	assert.True(t, tb.stopCalled.Load())

	// After stop, snapshot returns empty
	snap := obs.Snapshot("commissionable")
	assert.Empty(t, snap)
}

func TestObserver_LazySessionStart(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// No sessions started yet
	assert.False(t, tb.commStarted.Load())
	assert.False(t, tb.opStarted.Load())

	// Query commissionable -- only that session should start
	_ = obs.Snapshot("commissionable")
	waitForCondition(t, time.Second, func() bool { return tb.commStarted.Load() })
	assert.False(t, tb.opStarted.Load(), "operational session should NOT have started")

	// Query operational -- now it starts
	_ = obs.Snapshot("operational")
	waitForCondition(t, time.Second, func() bool { return tb.opStarted.Load() })
}

func TestObserver_SnapshotIsDeepCopy(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-DEEP", Discriminator: 42, Brand: "Original",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap1, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Mutate the returned snapshot
	snap1[0].TXTRecords["brand"] = "MUTATED"
	snap1[0].Addresses = append(snap1[0].Addresses, "1.2.3.4")

	// Get a fresh snapshot
	snap2 := obs.Snapshot("commissionable")
	require.Len(t, snap2, 1)
	assert.Equal(t, "Original", snap2[0].TXTRecords["brand"], "mutation should not affect internal state")
	assert.Empty(t, snap2[0].Addresses, "address mutation should not affect internal state")
}

// ---------------------------------------------------------------------------
// Group B: WaitFor Semantics
// ---------------------------------------------------------------------------

func TestObserver_WaitFor_ImmediatelySatisfied(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Pre-populate
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-PRE", Discriminator: 10,
	}

	// First call triggers session and should pick up the buffered service
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) > 0
	})
	require.NoError(t, err)
	assert.Len(t, snap, 1)
}

func TestObserver_WaitFor_BlocksUntilSatisfied(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Trigger session start
	_ = obs.Snapshot("commissionable")

	done := make(chan []discoveredService, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
			return len(svcs) >= 2
		})
		if err == nil {
			done <- snap
		}
	}()

	// Send first service after 50ms
	time.Sleep(50 * time.Millisecond)
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-W1", Discriminator: 1,
	}

	// Should still be waiting
	select {
	case <-done:
		t.Fatal("should not have returned with only 1 service")
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	// Send second service
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-W2", Discriminator: 2,
	}

	select {
	case snap := <-done:
		assert.Len(t, snap, 2)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitFor should have returned after 2nd service")
	}
}

func TestObserver_WaitFor_ContextTimeout(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) > 0
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestObserver_WaitFor_ContextCancelled(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) > 0
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestObserver_WaitFor_ReEvaluatesOnRemoval(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Add 2 services
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-R1", Discriminator: 1}
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-R2", Discriminator: 2}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 2
	})
	require.NoError(t, err)

	// Now wait for len == 1
	done := make(chan []discoveredService, 1)
	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		snap, err := obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
			return len(svcs) == 1
		})
		if err == nil {
			done <- snap
		}
	}()

	// Remove one service
	time.Sleep(50 * time.Millisecond)
	tb.commRemoved <- &discovery.CommissionableService{InstanceName: "MASH-R1"}

	select {
	case snap := <-done:
		assert.Len(t, snap, 1)
		assert.Equal(t, "MASH-R2", snap[0].InstanceName)
	case <-time.After(2 * time.Second):
		t.Fatal("WaitFor should have returned after removal")
	}
}

func TestObserver_WaitFor_ConcurrentCallers(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Trigger the session
	_ = obs.Snapshot("commissionable")

	var wg sync.WaitGroup
	results := make([]int, 3)

	// 3 goroutines waiting for different counts
	for i, threshold := range []int{1, 2, 3} {
		wg.Add(1)
		go func(idx, thr int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
				return len(svcs) >= thr
			})
			if err == nil {
				results[idx] = len(snap)
			}
		}(i, threshold)
	}

	// Send services one at a time
	time.Sleep(20 * time.Millisecond)
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "C1", Discriminator: 1}
	time.Sleep(20 * time.Millisecond)
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "C2", Discriminator: 2}
	time.Sleep(20 * time.Millisecond)
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "C3", Discriminator: 3}

	wg.Wait()
	assert.GreaterOrEqual(t, results[0], 1)
	assert.GreaterOrEqual(t, results[1], 2)
	assert.GreaterOrEqual(t, results[2], 3)
}

// ---------------------------------------------------------------------------
// Group D: Edge Cases
// ---------------------------------------------------------------------------

func TestObserver_DuplicateService_Deduplicated(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Same InstanceName sent twice
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-DUP", Discriminator: 100, Brand: "First",
	}
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName: "MASH-DUP", Discriminator: 100, Brand: "Second",
	}

	// Wait until the second update has been processed
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		if len(svcs) != 1 {
			return false
		}
		return svcs[0].TXTRecords["brand"] == "Second"
	})
	require.NoError(t, err)
	assert.Len(t, snap, 1, "duplicate should be deduplicated")
}

func TestObserver_ServiceUpdate_OverwritesOld(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zone-dev", ZoneID: "AAAA", DeviceID: "aaa",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "operational", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Update with new DeviceID
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zone-dev", ZoneID: "AAAA", DeviceID: "bbb",
	}

	// Wait for the update
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	snap, err := obs.WaitFor(ctx2, "operational", func(svcs []discoveredService) bool {
		return len(svcs) == 1 && svcs[0].TXTRecords["DI"] == "bbb"
	})
	require.NoError(t, err)
	assert.Len(t, snap, 1)
	assert.Equal(t, "bbb", snap[0].TXTRecords["DI"])
}

func TestObserver_StopThenSnapshot_ReturnsEmpty(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)

	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-X", Discriminator: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	obs.Stop()
	snap := obs.Snapshot("commissionable")
	assert.Empty(t, snap)
}

func TestObserver_PairingRequestServices(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Start pairing request session by querying it
	_ = obs.Snapshot(discovery.ServiceTypePairingRequest)

	// Give time for BrowsePairingRequests to register the callback
	waitForCondition(t, time.Second, func() bool {
		tb.mu.Lock()
		defer tb.mu.Unlock()
		return tb.pairingCallback != nil
	})

	// Send a pairing request via callback
	tb.sendPairingRequest(discovery.PairingRequestService{
		InstanceName:  "pair-1",
		Discriminator: 4567,
		ZoneID:        "CCDD",
		ZoneName:      "Office",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, discovery.ServiceTypePairingRequest, func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)
	require.Len(t, snap, 1)
	assert.Equal(t, "pair-1", snap[0].InstanceName)
	assert.Equal(t, uint16(4567), snap[0].Discriminator)
	assert.Equal(t, "CCDD", snap[0].TXTRecords["ZI"])
}

func TestObserver_HighFrequencyUpdates(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Start session
	_ = obs.Snapshot("commissionable")

	// Rapidly send 100 services
	const count = 100
	for i := range count {
		tb.commAdded <- &discovery.CommissionableService{
			InstanceName:  fmt.Sprintf("MASH-%04d", i),
			Discriminator: uint16(i),
		}
	}

	// Wait for all to appear
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= count
	})
	require.NoError(t, err)
	assert.Len(t, snap, count)
}

// ---------------------------------------------------------------------------
// Group E: ClearSnapshot
// ---------------------------------------------------------------------------

func TestObserver_ClearSnapshot_RemovesMatchingServices(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Add commissionable and operational services
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-CLR", Discriminator: 1}
	tb.opAdded <- &discovery.OperationalService{InstanceName: "zone-clr", ZoneID: "AA", DeviceID: "BB"}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "", func(svcs []discoveredService) bool {
		return len(svcs) >= 2
	})
	require.NoError(t, err)

	// Clear only commissionable
	obs.ClearSnapshot("commissionable")

	commSnap := obs.Snapshot("commissionable")
	opSnap := obs.Snapshot("operational")
	assert.Empty(t, commSnap, "commissionable should be cleared")
	assert.Len(t, opSnap, 1, "operational should be untouched")
}

func TestObserver_ClearSnapshot_AllTypes(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-A", Discriminator: 1}
	tb.opAdded <- &discovery.OperationalService{InstanceName: "zone-A", ZoneID: "AA", DeviceID: "BB"}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "", func(svcs []discoveredService) bool {
		return len(svcs) >= 2
	})
	require.NoError(t, err)

	// Clear all
	obs.ClearSnapshot("")

	assert.Empty(t, obs.Snapshot("commissionable"))
	assert.Empty(t, obs.Snapshot("operational"))
}

func TestObserver_ClearSnapshot_WakesWaitForCallers(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Add a service
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-WAKE", Discriminator: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Start WaitFor that expects absence
	done := make(chan struct{})
	go func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_, err := obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
			return len(svcs) == 0
		})
		if err == nil {
			close(done)
		}
	}()

	// ClearSnapshot should wake the WaitFor caller and satisfy len==0
	time.Sleep(50 * time.Millisecond)
	obs.ClearSnapshot("commissionable")

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("WaitFor(len==0) should have been satisfied by ClearSnapshot")
	}
}

func TestObserver_ClearSnapshot_ThenFreshServiceAppears(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Add and verify
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-OLD", Discriminator: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Clear, then send a fresh service
	obs.ClearSnapshot("commissionable")
	assert.Empty(t, obs.Snapshot("commissionable"))

	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-NEW", Discriminator: 2}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	snap, err := obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)
	require.Len(t, snap, 1)
	assert.Equal(t, "MASH-NEW", snap[0].InstanceName, "should see fresh service, not old")
}

// TestObserver_ClearSnapshot_TimesOutWhenNoNewEvent demonstrates the problem
// with ClearSnapshot in waitForCommissioningMode: if the device is already
// advertising (service was already discovered by the persistent browse session)
// but no NEW mDNS event arrives after clearing, WaitFor times out.
//
// In real mDNS, zeroconf's persistent browse session fires events only for
// changes (new/removed services). If the device hasn't changed its advertisement,
// no new event arrives and ClearSnapshot causes a false timeout.
func TestObserver_ClearSnapshot_TimesOutWhenNoNewEvent(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Device is already advertising -- observer discovers it
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-STEADY", Discriminator: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)
	require.Len(t, snap, 1)

	// Simulate what waitForCommissioningMode does: clear + wait.
	// No new mDNS event is sent (device hasn't changed its advertisement).
	obs.ClearSnapshot("commissionable")

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer waitCancel()
	_, err = obs.WaitFor(waitCtx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) > 0
	})

	// BUG: This times out because ClearSnapshot removed the service from the
	// observer's internal map, and no new event arrives to repopulate it.
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"ClearSnapshot + no new event should cause WaitFor to time out (demonstrating the bug)")
}

// TestObserver_WaitForPresence_WithoutClear shows that without ClearSnapshot,
// WaitFor returns immediately when the service is already in the snapshot.
// This is the desired behavior for waitForCommissioningMode when the device
// is continuously advertising.
func TestObserver_WaitForPresence_WithoutClear(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Device is already advertising
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-STEADY", Discriminator: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)
	require.Len(t, snap, 1)

	// Without ClearSnapshot, WaitFor returns immediately from existing snapshot
	start := time.Now()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer waitCancel()
	snap, err = obs.WaitFor(waitCtx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) > 0
	})

	assert.NoError(t, err, "WaitFor should succeed immediately from existing snapshot")
	assert.Len(t, snap, 1)
	assert.Less(t, time.Since(start), 50*time.Millisecond,
		"should return near-instantly from cached snapshot")
}

// ---------------------------------------------------------------------------
// Group F: WaitFor absence patterns
// ---------------------------------------------------------------------------

func TestObserver_WaitFor_AbsenceAfterClearAndRemoval(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Populate observer
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-ABS", Discriminator: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// Simulate device stopping: removal event arrives after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		tb.commRemoved <- &discovery.CommissionableService{InstanceName: "MASH-ABS"}
	}()

	// WaitFor absence -- should succeed within timeout
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	snap, err := obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) == 0
	})
	require.NoError(t, err)
	assert.Empty(t, snap)
}

// ---------------------------------------------------------------------------
// TC-COMM-003 root cause verification: stale operational entries
// ---------------------------------------------------------------------------

// TestObserver_TwoOperationalEntries_BothInSnapshot demonstrates that when a
// device is commissioned twice (creating two different zones), the observer
// holds BOTH operational entries in its snapshot. This is the root cause of
// TC-COMM-003's intermittent "all_equal = false" failure: the browse handler
// picks services[0], which could be the stale zone's entry with an old device ID.
func TestObserver_TwoOperationalEntries_BothInSnapshot(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Zone A commissioned first -- device ID "aaaa".
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zoneA-aaaa",
		ZoneID:       "zoneA",
		DeviceID:     "aaaa",
		Port:         8443,
	}
	// Zone B commissioned after fallback -- device ID "bbbb".
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "zoneB-bbbb",
		ZoneID:       "zoneB",
		DeviceID:     "bbbb",
		Port:         8443,
	}

	// Wait for both to appear.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svcs, err := obs.WaitFor(ctx, "operational", func(s []discoveredService) bool {
		return len(s) >= 2
	})
	require.NoError(t, err)
	require.Len(t, svcs, 2, "observer must hold both operational entries")

	// Both device IDs are present.
	ids := map[string]bool{}
	for _, svc := range svcs {
		ids[svc.TXTRecords["DI"]] = true
	}
	assert.True(t, ids["aaaa"], "zone A device ID should be in snapshot")
	assert.True(t, ids["bbbb"], "zone B device ID should be in snapshot")

	// The critical issue: services[0] could be either entry.
	// Whatever device_id services[0] has, if it's the stale zone, comparing
	// it against cert_device_id and state_device_id (both from zone B)
	// produces all_equal = false.
	firstDI := svcs[0].TXTRecords["DI"]
	t.Logf("services[0] has DI=%q (non-deterministic)", firstDI)
}

// TestObserver_StaleOperationalEntry_WrongDeviceID demonstrates the exact
// TC-COMM-003 failure: three device IDs from different sources don't match
// because the mDNS browse picks a stale operational entry.
func TestObserver_StaleOperationalEntry_WrongDeviceID(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	const (
		staleDeviceID = "aaaa1111"
		freshDeviceID = "bbbb2222"
	)

	// Stale zone from auto-PICS commissioning.
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "staleZone-" + staleDeviceID,
		ZoneID:       "staleZone",
		DeviceID:     staleDeviceID,
		Port:         8443,
	}
	// Fresh zone from fallback re-commissioning.
	tb.opAdded <- &discovery.OperationalService{
		InstanceName: "freshZone-" + freshDeviceID,
		ZoneID:       "freshZone",
		DeviceID:     freshDeviceID,
		Port:         8443,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svcs, err := obs.WaitFor(ctx, "operational", func(s []discoveredService) bool {
		return len(s) >= 2
	})
	require.NoError(t, err)

	// Simulate what TC-COMM-003 does: pick services[0] device_id as mdns_device_id.
	mdnsDeviceID := svcs[0].TXTRecords["DI"]

	// The cert and state always have the fresh device ID (from the latest commission).
	certDeviceID := freshDeviceID
	stateDeviceID := freshDeviceID

	// The all_equal check.
	allEqual := certDeviceID == stateDeviceID && stateDeviceID == mdnsDeviceID

	if mdnsDeviceID == staleDeviceID {
		// Observer returned the stale entry first -- this is the TC-COMM-003 failure.
		assert.False(t, allEqual, "stale DI must cause all_equal=false")
		t.Logf("REPRODUCED: services[0] has stale DI=%q, cert/state have %q", mdnsDeviceID, freshDeviceID)
	} else {
		// Observer returned the fresh entry first -- test would pass.
		assert.True(t, allEqual, "fresh DI should make all_equal=true")
		t.Logf("NOT REPRODUCED this run: services[0] has fresh DI=%q (map iteration order)", mdnsDeviceID)
	}
}

func TestObserver_WaitFor_AbsenceTimesOutIfStillAdvertising(t *testing.T) {
	tb := newTestBrowser()
	obs := newMDNSObserver(tb, noopDebugf)
	defer obs.Stop()

	// Service present and never removed
	tb.commAdded <- &discovery.CommissionableService{InstanceName: "MASH-STAY", Discriminator: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := obs.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	require.NoError(t, err)

	// WaitFor absence with short timeout -- should fail
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	_, err = obs.WaitFor(ctx2, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) == 0
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
