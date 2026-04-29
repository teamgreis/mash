package runner

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

func TestDiffSnapshots_NoDiffs(t *testing.T) {
	before := DeviceStateSnapshot{
		"zone_count":     0,
		"clock_offset_s": 0,
		"active_conns":   1,
	}
	after := DeviceStateSnapshot{
		"zone_count":     0,
		"clock_offset_s": 0,
		"active_conns":   1,
	}
	diffs := diffSnapshots(before, after)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %v", len(diffs), diffs)
	}
}

func TestDiffSnapshots_DetectsChanges(t *testing.T) {
	before := DeviceStateSnapshot{
		"zone_count":     1,
		"clock_offset_s": 0,
		"active_conns":   1,
	}
	after := DeviceStateSnapshot{
		"zone_count":     2,
		"clock_offset_s": 0,
		"active_conns":   3,
	}
	diffs := diffSnapshots(before, after)
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %v", len(diffs), diffs)
	}
	// Sorted by key: active_conns, zone_count
	if diffs[0].Key != "active_conns" {
		t.Errorf("diffs[0].Key = %q, want active_conns", diffs[0].Key)
	}
	if diffs[1].Key != "zone_count" {
		t.Errorf("diffs[1].Key = %q, want zone_count", diffs[1].Key)
	}
}

func TestDiffSnapshots_MissingKeys(t *testing.T) {
	before := DeviceStateSnapshot{
		"zone_count": 1,
		"old_field":  "gone",
	}
	after := DeviceStateSnapshot{
		"zone_count": 1,
		"new_field":  "appeared",
	}
	diffs := diffSnapshots(before, after)
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %v", len(diffs), diffs)
	}
	// new_field (after-only), old_field (before-only)
	if diffs[0].Key != "new_field" || diffs[0].Before != nil {
		t.Errorf("diffs[0] = %+v, want new_field with before=nil", diffs[0])
	}
	if diffs[1].Key != "old_field" || diffs[1].After != nil {
		t.Errorf("diffs[1] = %+v, want old_field with after=nil", diffs[1])
	}
}

func TestDiffSnapshots_NilInputs(t *testing.T) {
	if diffs := diffSnapshots(nil, DeviceStateSnapshot{"a": 1}); diffs != nil {
		t.Errorf("expected nil for nil before, got %v", diffs)
	}
	if diffs := diffSnapshots(DeviceStateSnapshot{"a": 1}, nil); diffs != nil {
		t.Errorf("expected nil for nil after, got %v", diffs)
	}
}

func TestFilterBaselineDiffs_RemovesVolatileKeys(t *testing.T) {
	diffs := []StateDiff{
		{Key: "zones", Before: 0, After: 1},
		{Key: "failsafe_timers", Before: "a", After: "b"},
		{Key: "pairing_active", Before: false, After: true},
		{Key: "active_conns", Before: 0, After: 1},
		{Key: "conn_tracker_count", Before: 0, After: 1},
		{Key: "status_operating_state", Before: 2, After: 4},
	}
	got := filterBaselineDiffs(diffs)
	if len(got) != 1 {
		t.Fatalf("expected 1 stable diff, got %d: %+v", len(got), got)
	}
	if got[0].Key != "status_operating_state" {
		t.Fatalf("expected status_operating_state to remain, got %q", got[0].Key)
	}
}

func TestRequestDeviceState_IgnoresOrphanedResponses(t *testing.T) {
	r := newTestRunner()
	r.config.EnableKey = "deadbeefdeadbeefdeadbeefdeadbeef"

	client, server := net.Pipe()
	r.pool.SetMain(&Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		srvFramer := transport.NewFramer(server)
		reqData, err := srvFramer.ReadFrame()
		if err != nil {
			return
		}
		req, err := wire.DecodeRequest(reqData)
		if err != nil {
			return
		}

		// Orphaned response from a different in-flight request.
		orphan := &wire.Response{
			MessageID: req.MessageID + 99,
			Status:    wire.StatusSuccess,
			Payload: map[string]any{
				"stale": true,
			},
		}
		orphanData, _ := wire.EncodeResponse(orphan)
		_ = srvFramer.WriteFrame(orphanData)

		// Matching response for requestDeviceState.
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload: map[string]any{
				"zone_count": 1,
			},
		}
		respData, _ := wire.EncodeResponse(resp)
		_ = srvFramer.WriteFrame(respData)
	}()

	snap := r.requestDeviceState(context.Background(), engine.NewExecutionState(context.Background()))
	<-done

	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if _, ok := snap["stale"]; ok {
		t.Fatal("orphaned response payload must be ignored")
	}
	if got, ok := snap["zone_count"]; !ok || got != uint64(1) {
		t.Fatalf("expected zone_count=1 from matching response, got %v", snap["zone_count"])
	}
}

func TestRequestDeviceState_DoesNotBlockOnWriteToUnresponsivePeer(t *testing.T) {
	r := newTestRunner()
	r.config.EnableKey = "deadbeefdeadbeefdeadbeefdeadbeef"

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	r.pool.SetMain(&Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = r.requestDeviceState(ctx, engine.NewExecutionState(context.Background()))
		close(done)
	}()

	select {
	case <-done:
		// Expected: write should respect context deadline.
	case <-time.After(2 * time.Second):
		_ = server.Close() // Unblock writer for cleanup.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("requestDeviceState blocked on write to unresponsive peer")
	}
}

// TestRequestDeviceState_SendsIntegerKeyedParameters guards against a
// regression of Issue #11 / Phase 11: getTestState's enableKey parameter
// must travel as an integer-keyed CBOR map (key = 1) so the device's
// command machinery can match it against the parameter metadata. A
// string-keyed map encodes correctly on the wire but the device's
// validateParameters (pkg/model/command.go) rejects the request with
// StatusInvalidParameter (status 5) before the enable-key check ever
// runs. That regression silently disabled the residual zone sweep in
// teardown and caused F-6 (TC-E2E-001 cert leak).
func TestRequestDeviceState_SendsIntegerKeyedParameters(t *testing.T) {
	r := newTestRunner()
	r.config.EnableKey = "deadbeefdeadbeefdeadbeefdeadbeef"

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	r.pool.SetMain(&Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	})

	type capture struct {
		params any
	}
	captured := make(chan capture, 1)
	go func() {
		srvFramer := transport.NewFramer(server)
		reqData, err := srvFramer.ReadFrame()
		if err != nil {
			return
		}
		req, err := wire.DecodeRequest(reqData)
		if err != nil {
			return
		}
		// Pull the parameters off the encoded payload so we can assert
		// on their keying. wire.DecodeRequest leaves Payload as the raw
		// CBOR map for unknown command types, so we decode it back.
		if invoke, ok := req.Payload.(*wire.InvokePayload); ok {
			captured <- capture{params: invoke.Parameters}
		} else {
			captured <- capture{params: req.Payload}
		}
		// Reply success with empty snapshot so the caller exits cleanly.
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload:   map[string]any{"zone_count": 0},
		}
		respData, _ := wire.EncodeResponse(resp)
		_ = srvFramer.WriteFrame(respData)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = r.requestDeviceState(ctx, engine.NewExecutionState(context.Background()))

	got := <-captured
	if got.params == nil {
		t.Fatal("expected parameters map, got nil")
	}
	// CBOR decoded into map[any]any with uint64 keys — that's what the device
	// machinery walks via integer parameter IDs. A string-keyed payload would
	// surface as map[any]any with string keys here.
	m, ok := got.params.(map[any]any)
	if !ok {
		t.Fatalf("expected parameters to decode as map[any]any, got %T", got.params)
	}
	if _, hasIntKey := m[uint64(1)]; !hasIntKey {
		t.Errorf("expected integer key 1 (enableKey) in parameters, got keys: %v", mapKeys(m))
	}
	for k := range m {
		if _, isString := k.(string); isString {
			t.Errorf("parameters must be integer-keyed, found string key: %v", k)
		}
	}
}

func mapKeys(m map[any]any) []any {
	keys := make([]any, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestRequestDeviceState_PrefersSuiteConnectionOverMain(t *testing.T) {
	r := newTestRunner()
	r.config.EnableKey = "deadbeefdeadbeefdeadbeefdeadbeef"

	mainClient, mainServer := net.Pipe()
	defer mainClient.Close()
	defer mainServer.Close()
	r.pool.SetMain(&Connection{
		conn:   mainClient,
		framer: transport.NewFramer(mainClient),
		state:  ConnOperational,
	})

	suiteClient, suiteServer := net.Pipe()
	defer suiteClient.Close()
	defer suiteServer.Close()
	r.suite.SetConn(&Connection{
		conn:   suiteClient,
		framer: transport.NewFramer(suiteClient),
		state:  ConnOperational,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		srvFramer := transport.NewFramer(suiteServer)
		reqData, err := srvFramer.ReadFrame()
		if err != nil {
			return
		}
		req, err := wire.DecodeRequest(reqData)
		if err != nil {
			return
		}
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
			Payload: map[string]any{
				"zone_count": 1,
			},
		}
		respData, _ := wire.EncodeResponse(resp)
		_ = srvFramer.WriteFrame(respData)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snap := r.requestDeviceState(ctx, engine.NewExecutionState(context.Background()))
	<-done

	if snap == nil {
		t.Fatal("expected non-nil snapshot from suite connection")
	}
	if got, ok := snap["zone_count"]; !ok || got != uint64(1) {
		t.Fatalf("expected zone_count=1 from suite response, got %v", snap["zone_count"])
	}
}
