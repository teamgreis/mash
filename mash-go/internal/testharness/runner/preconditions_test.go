package runner

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

func TestPreconditionLevel(t *testing.T) {
	r := newTestRunner()

	tests := []struct {
		name       string
		conditions []loader.Condition
		wantLevel  int
	}{
		{
			name:       "empty conditions",
			conditions: nil,
			wantLevel:  0,
		},
		{
			name: "session_established true",
			conditions: []loader.Condition{
				{PrecondSessionEstablished: true},
			},
			wantLevel: 3,
		},
		{
			name: "device_commissioned true",
			conditions: []loader.Condition{
				{PrecondDeviceCommissioned: true},
			},
			wantLevel: 3,
		},
		{
			name: "tls_connection_established true",
			conditions: []loader.Condition{
				{PrecondTLSConnectionEstablished: true},
			},
			wantLevel: 2,
		},
		{
			name: "connection_established true",
			conditions: []loader.Condition{
				{PrecondConnectionEstablished: true},
			},
			wantLevel: 2,
		},
		{
			name: "device_in_commissioning_mode true",
			conditions: []loader.Condition{
				{"device_in_commissioning_mode": true},
			},
			wantLevel: 1,
		},
		{
			name: "unknown condition",
			conditions: []loader.Condition{
				{"some_other_condition": true},
			},
			wantLevel: 0,
		},
		{
			name: "multiple conditions highest wins",
			conditions: []loader.Condition{
				{PrecondConnectionEstablished: true},
				{PrecondSessionEstablished: true},
			},
			wantLevel: 3,
		},
		{
			name: "false value ignored",
			conditions: []loader.Condition{
				{PrecondSessionEstablished: false},
			},
			wantLevel: 0,
		},
		{
			name: "mixed true and false",
			conditions: []loader.Condition{
				{PrecondSessionEstablished: false},
				{PrecondConnectionEstablished: true},
			},
			wantLevel: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.preconditionLevel(tt.conditions)
			if got != tt.wantLevel {
				t.Errorf("preconditionLevel() = %d, want %d", got, tt.wantLevel)
			}
		})
	}
}

func TestSetupPreconditions_Level0(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID:            "TC-TEST-001",
		Preconditions: nil,
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error for level 0: %v", err)
	}
}

func TestSetupPreconditions_Level1(t *testing.T) {
	r := newTestRunner()
	// Simulate an existing connection
	r.pool.Main().state = ConnOperational

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-TEST-002",
		Preconditions: []loader.Condition{
			{"device_in_commissioning_mode": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error for level 1: %v", err)
	}

	// Should have disconnected
	if r.pool.Main().isConnected() {
		t.Error("expected connection to be closed for commissioning mode")
	}
}

func TestEnsureDisconnected(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational

	r.ensureDisconnected()

	if r.pool.Main().isConnected() {
		t.Error("expected connected to be false after ensureDisconnected")
	}
}

func TestEnsureDisconnected_AlreadyDisconnected(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnDisconnected

	// Should not panic or error
	r.ensureDisconnected()

	if r.pool.Main().isConnected() {
		t.Error("expected connected to remain false")
	}
}

func TestEnsureConnected_AlreadyOnCommissioningALPN(t *testing.T) {
	// `ensureConnected` is the level-2 (`tls_connection_established`)
	// path: it should only short-circuit when pool.Main is *already* a
	// commissioning TLS conn. The level-3 reuse case is
	// `ensureCommissioned`'s job (see counterpart test below).
	r := newTestRunner()
	r.pool.Main().state = ConnTLSConnected
	state := engine.NewExecutionState(context.Background())

	err := r.ensureConnected(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error when already on commissioning TLS: %v", err)
	}
	if !r.pool.Main().hadConnection {
		t.Error("expected hadConnection=true after early-return")
	}
}

// TestEnsureConnected_OperationalPoolMainTriggersReconnect locks in the
// TC-PASE-003 fix: a stale operational pool.Main (e.g. the suite control
// channel left attached after a level-3 → level-2 downgrade) must NOT be
// reused for level-2 preconditions. ensureConnected detaches it and
// dials a fresh commissioning TLS conn — sending PASE /
// CommissioningError envelopes over the operational `mash/1` ALPN is a
// category mismatch.
//
// We use the simulated-port-closed state to short-circuit the actual
// dial (CI-stable: no real network) and assert the error path proves
// the reconnect was attempted, not that the operational conn was reused
// as a no-op.
func TestEnsureConnected_OperationalPoolMainTriggersReconnect(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	state := engine.NewExecutionState(context.Background())
	state.Set(PrecondDevicePortClosed, true)

	err := r.ensureConnected(context.Background(), state)
	if err == nil {
		t.Fatal("expected reconnect attempt to error against simulated closed port")
	}
	if r.pool.Main() != nil && r.pool.Main().state == ConnOperational {
		t.Error("expected operational pool.Main to be detached before reconnect")
	}
}

func TestEnsureConnected_IncludesErrorDetailFromConnectFailure(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())
	state.Set(PrecondDevicePortClosed, true)

	err := r.ensureConnected(context.Background(), state)
	if err == nil {
		t.Fatal("expected ensureConnected error when simulated port is closed")
	}
	if !strings.Contains(err.Error(), ErrCodeConnectionFailed) {
		t.Fatalf("expected error code in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused (simulated)") {
		t.Fatalf("expected error detail in message, got: %v", err)
	}
}

func TestShouldRetryConnectFailure(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		detail string
		want   bool
	}{
		{name: "retry connection error", code: ErrCodeConnectionError, detail: "dial tcp timeout", want: true},
		{name: "retry connection failed", code: ErrCodeConnectionFailed, detail: "connection refused", want: true},
		{name: "retry timeout", code: ErrCodeTimeout, detail: "i/o timeout", want: true},
		{name: "no retry simulated", code: ErrCodeConnectionFailed, detail: "connection refused (simulated)", want: false},
		{name: "no retry non-connect", code: ErrCodeTLSError, detail: "bad certificate", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetryConnectFailure(tt.code, tt.detail)
			if got != tt.want {
				t.Fatalf("shouldRetryConnectFailure(%q, %q) = %v, want %v", tt.code, tt.detail, got, tt.want)
			}
		})
	}
}

func TestSortByPreconditionLevel(t *testing.T) {
	cases := []*loader.TestCase{
		{ID: "TC-COMM-001", Preconditions: []loader.Condition{{PrecondDeviceCommissioned: true}}},      // level 3
		{ID: "TC-DISC-001", Preconditions: nil},                                                        // level 0
		{ID: "TC-CONN-001", Preconditions: []loader.Condition{{PrecondConnectionEstablished: true}}},   // level 2
		{ID: "TC-PASE-001", Preconditions: []loader.Condition{{"device_in_commissioning_mode": true}}}, // level 1
		{ID: "TC-COMM-002", Preconditions: []loader.Condition{{PrecondSessionEstablished: true}}},      // level 3
		{ID: "TC-DISC-002", Preconditions: nil},                                                        // level 0
		{ID: "TC-PASE-002", Preconditions: []loader.Condition{{"device_uncommissioned": true}}},        // level 1
	}

	SortByPreconditionLevel(cases)

	// Verify ordering: level 0, level 0, level 1, level 1, level 2, level 3, level 3
	wantOrder := []string{
		"TC-DISC-001", "TC-DISC-002", // level 0 -- stable order preserved
		"TC-PASE-001", "TC-PASE-002", // level 1 -- stable order preserved
		"TC-CONN-001",                // level 2
		"TC-COMM-001", "TC-COMM-002", // level 3 -- stable order preserved
	}

	if len(cases) != len(wantOrder) {
		t.Fatalf("got %d cases, want %d", len(cases), len(wantOrder))
	}

	for i, tc := range cases {
		if tc.ID != wantOrder[i] {
			t.Errorf("position %d: got %s, want %s", i, tc.ID, wantOrder[i])
		}
	}
}

func TestShuffleWithinLevels(t *testing.T) {
	// Create sorted test cases (must be pre-sorted by level).
	makeCases := func() []*loader.TestCase {
		return []*loader.TestCase{
			{ID: "L0-A", Preconditions: nil},
			{ID: "L0-B", Preconditions: nil},
			{ID: "L0-C", Preconditions: nil},
			{ID: "L0-D", Preconditions: nil},
			{ID: "L3-A", Preconditions: []loader.Condition{{PrecondDeviceCommissioned: true}}},
			{ID: "L3-B", Preconditions: []loader.Condition{{PrecondDeviceCommissioned: true}}},
			{ID: "L3-C", Preconditions: []loader.Condition{{PrecondDeviceCommissioned: true}}},
			{ID: "L3-D", Preconditions: []loader.Condition{{PrecondDeviceCommissioned: true}}},
		}
	}

	// Same seed produces same order.
	cases1 := makeCases()
	cases2 := makeCases()
	ShuffleWithinLevels(cases1, 42)
	ShuffleWithinLevels(cases2, 42)
	for i := range cases1 {
		if cases1[i].ID != cases2[i].ID {
			t.Errorf("determinism: position %d: %s != %s", i, cases1[i].ID, cases2[i].ID)
		}
	}

	// Level boundaries are preserved: L0 cases still come first.
	for i := 0; i < 4; i++ {
		if cases1[i].ID[:2] != "L0" {
			t.Errorf("position %d: expected L0 case, got %s", i, cases1[i].ID)
		}
	}
	for i := 4; i < 8; i++ {
		if cases1[i].ID[:2] != "L3" {
			t.Errorf("position %d: expected L3 case, got %s", i, cases1[i].ID)
		}
	}

	// Different seed produces different order (with very high probability for 4 items).
	cases3 := makeCases()
	ShuffleWithinLevels(cases3, 99)
	sameCount := 0
	for i := range cases1 {
		if cases1[i].ID == cases3[i].ID {
			sameCount++
		}
	}
	if sameCount == len(cases1) {
		t.Error("different seeds produced identical order (extremely unlikely)")
	}
}

func TestPreconditionLevel_NewKeys(t *testing.T) {
	r := newTestRunner()

	tests := []struct {
		name      string
		key       string
		wantLevel int
	}{
		{"device_uncommissioned", "device_uncommissioned", 1},
		{PrecondCommissioningWindowOpen, PrecondCommissioningWindowOpen, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := []loader.Condition{{tt.key: true}}
			got := r.preconditionLevel(conditions)
			if got != tt.wantLevel {
				t.Errorf("preconditionLevel(%s) = %d, want %d", tt.key, got, tt.wantLevel)
			}
		})
	}
}

func TestCurrentLevel(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(r *Runner)
		wantLevel int
	}{
		{
			name:      "no connection",
			setup:     func(r *Runner) {},
			wantLevel: precondLevelNone,
		},
		{
			name: "connected only",
			setup: func(r *Runner) {
				r.pool.Main().state = ConnTLSConnected
			},
			wantLevel: precondLevelConnected,
		},
		{
			name: "commissioned",
			setup: func(r *Runner) {
				r.pool.Main().state = ConnOperational
				r.connMgr.SetPASEState(&PASEState{completed: true})
			},
			wantLevel: precondLevelCommissioned,
		},
		{
			name: "pase incomplete treated as connected",
			setup: func(r *Runner) {
				r.pool.Main().state = ConnTLSConnected
				r.connMgr.SetPASEState(&PASEState{completed: false})
			},
			wantLevel: precondLevelConnected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTestRunner()
			tt.setup(r)
			got := r.currentLevel()
			if got != tt.wantLevel {
				t.Errorf("currentLevel() = %d, want %d", got, tt.wantLevel)
			}
		})
	}
}

func TestSetupPreconditions_BackwardsTransition(t *testing.T) {
	// Commissioned runner + commissioning-level test -> should disconnect.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-BACK-001",
		Preconditions: []loader.Condition{
			{"device_in_commissioning_mode": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.pool.Main().isConnected() {
		t.Error("expected connection to be closed for backwards transition")
	}
	if r.connMgr.PASEState() != nil {
		t.Error("expected paseState to be nil after backwards transition")
	}
}

// C9: D2D precondition keys are stored in state.
func TestSetupPreconditions_D2DStateKeys(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	tc := &loader.TestCase{
		ID: "TC-D2D-001",
		Preconditions: []loader.Condition{
			{"two_devices_same_zone": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, ok := state.Get("two_devices_same_zone")
	if !ok {
		t.Error("expected two_devices_same_zone to be stored in state")
	}
	if v != true {
		t.Errorf("expected true, got %v", v)
	}
}

func TestSetupPreconditions_D2DExpiredKey(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	tc := &loader.TestCase{
		ID: "TC-D2D-003",
		Preconditions: []loader.Condition{
			{"device_b_cert_expired": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	v, ok := state.Get("device_b_cert_expired")
	if !ok {
		t.Error("expected device_b_cert_expired to be stored in state")
	}
	if v != true {
		t.Errorf("expected true, got %v", v)
	}
}

// D2D precondition keys should be at level 0 (no setup needed).
func TestPreconditionLevel_D2DKeys(t *testing.T) {
	r := newTestRunner()

	for _, key := range []string{"two_devices_same_zone", "two_devices_different_zones", "device_b_cert_expired"} {
		conditions := []loader.Condition{{key: true}}
		got := r.preconditionLevel(conditions)
		if got != precondLevelNone {
			t.Errorf("preconditionLevel(%s) = %d, want %d (level 0)", key, got, precondLevelNone)
		}
	}
}

func TestPreconditionLevel_TwoZonesConnected(t *testing.T) {
	r := newTestRunner()
	conditions := []loader.Condition{
		{PrecondTwoZonesConnected: true},
	}
	got := r.preconditionLevel(conditions)
	if got != precondLevelNone {
		t.Errorf("preconditionLevel(two_zones_connected) = %d, want %d (level 0)", got, precondLevelNone)
	}
}

func TestSetupPreconditions_TwoZonesConnected(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	tc := &loader.TestCase{
		ID: "TC-MULTI-003",
		Preconditions: []loader.Condition{
			{PrecondTwoZonesConnected: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State key should be set.
	v, ok := state.Get(PrecondTwoZonesConnected)
	if !ok {
		t.Error("expected two_zones_connected to be stored in state")
	}
	if v != true {
		t.Errorf("expected true, got %v", v)
	}

	// Connection tracker should have exactly 2 zone connections.
	ct := getConnectionTracker(state)
	if len(ct.zoneConnections) != 2 {
		t.Errorf("expected 2 zone connections, got %d", len(ct.zoneConnections))
	}
	for _, name := range []string{"GRID", "LOCAL"} {
		conn, exists := ct.zoneConnections[name]
		if !exists {
			t.Errorf("expected zone connection %q to exist", name)
			continue
		}
		if !conn.isConnected() {
			t.Errorf("expected zone %q to be connected", name)
		}
	}
}

func TestSetupPreconditions_TwoZonesConnected_HandlersWork(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	tc := &loader.TestCase{
		ID: "TC-MULTI-003",
		Preconditions: []loader.Condition{
			{PrecondTwoZonesConnected: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Dummy connections created by precondition should support simulated I/O.
	step := &loader.Step{Params: map[string]any{
		KeyZoneID:   "GRID",
		KeyEndpoint: float64(1),
		KeyFeature:  "measurement",
	}}
	out, err := r.handleSubscribeAsZone(context.Background(), step, state)
	if err != nil {
		t.Fatalf("subscribe_as_zone on dummy GRID connection: %v", err)
	}
	if out[KeySubscribeSuccess] != true {
		t.Error("expected subscribe_success=true on dummy GRID connection")
	}

	step = &loader.Step{Params: map[string]any{
		KeyZoneID:   "LOCAL",
		KeyEndpoint: float64(1),
		KeyFeature:  "measurement",
	}}
	out, err = r.handleReadAsZone(context.Background(), step, state)
	if err != nil {
		t.Fatalf("read_as_zone on dummy LOCAL connection: %v", err)
	}
	if out[KeyReadSuccess] != true {
		t.Error("expected read_success=true on dummy LOCAL connection")
	}
}

func TestPreconditionLevel_TwoDevicesSameDiscriminator(t *testing.T) {
	r := newTestRunner()
	conditions := []loader.Condition{
		{PrecondTwoDevicesSameDiscriminator: true},
	}
	got := r.preconditionLevel(conditions)
	if got != precondLevelNone {
		t.Errorf("preconditionLevel(two_devices_same_discriminator) = %d, want %d (level 0)", got, precondLevelNone)
	}
}

func TestSendRemoveZone_NilPaseState(t *testing.T) {
	// sendRemoveZone should be a no-op when not commissioned.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(nil)

	// Should not panic.
	r.sendRemoveZone()
}

func TestSendRemoveZone_NoSessionKey(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: nil})

	// Should not panic.
	r.sendRemoveZone()
}

func TestSendRemoveZoneStrict_NoLiveConn(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnDisconnected

	err := r.sendRemoveZoneStrict()
	if !errors.Is(err, errRemoveZoneNoConnection) {
		t.Fatalf("expected no-connection error, got: %v", err)
	}
}

func TestSendRemoveZoneStrict_NoPASE(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	r.pool.Main().conn = client
	r.pool.Main().framer = transport.NewFramer(client)
	r.connMgr.SetPASEState(nil)

	err := r.sendRemoveZoneStrict()
	if !errors.Is(err, errRemoveZoneNoPASE) {
		t.Fatalf("expected no-PASE error, got: %v", err)
	}
}

func TestSendRemoveZoneStrict_WriteError(t *testing.T) {
	r := newTestRunner()
	client, server := net.Pipe()
	r.pool.Main().state = ConnOperational
	r.pool.Main().conn = client
	r.pool.Main().framer = transport.NewFramer(client)
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0xAA, 0xBB, 0xCC, 0xDD}})

	_ = server.Close()
	defer client.Close()

	err := r.sendRemoveZoneStrict()
	if err == nil {
		t.Fatal("expected write/read error")
	}
	if !strings.Contains(err.Error(), "remove zone") {
		t.Fatalf("expected remove zone error context, got: %v", err)
	}
}

func TestSendRemoveZoneStrict_SucceedsOnAck(t *testing.T) {
	r := newTestRunner()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	r.pool.Main().state = ConnOperational
	r.pool.Main().conn = client
	r.pool.Main().framer = transport.NewFramer(client)
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0x11, 0x22, 0x33, 0x44}})

	done := make(chan error, 1)
	go func() {
		fr := transport.NewFramer(server)
		reqData, err := fr.ReadFrame()
		if err != nil {
			done <- err
			return
		}
		req, err := wire.DecodeRequest(reqData)
		if err != nil {
			done <- err
			return
		}
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusSuccess,
		}
		respData, err := wire.EncodeResponse(resp)
		if err != nil {
			done <- err
			return
		}
		done <- fr.WriteFrame(respData)
	}()

	if err := r.sendRemoveZoneStrict(); err != nil {
		t.Fatalf("expected strict remove zone success, got: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server goroutine failed: %v", err)
	}
}

func TestSendRemoveZoneOnConnStrict_AckTimeoutWhenResponseIsLate(t *testing.T) {
	r := newTestRunner()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	go func() {
		fr := transport.NewFramer(server)
		_, _ = fr.ReadFrame()
		time.Sleep(strictRemoveZoneAckTimeout + 150*time.Millisecond)
		_ = server.Close()
	}()

	err := r.sendRemoveZoneOnConnStrict(conn, "zone-1")
	if err == nil {
		t.Fatal("expected timeout waiting for remove-zone ack")
	}
	if !errors.Is(err, errRemoveZoneAckRead) {
		t.Fatalf("expected remove-zone ack read sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout in error, got: %v", err)
	}
}

func TestSendRemoveZoneOnConn_DoesNotBlockOnWriteToUnresponsivePeer(t *testing.T) {
	r := newTestRunner()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	done := make(chan struct{})
	go func() {
		r.sendRemoveZoneOnConn(conn, "zone-1")
		close(done)
	}()

	select {
	case <-done:
		// Expected: write deadline should bound this path.
	case <-time.After(removeZoneWriteTimeout + 200*time.Millisecond):
		t.Fatal("sendRemoveZoneOnConn blocked on write to unresponsive peer")
	}
}

func TestSetupPreconditions_StrictMode_BubblesRemoveZoneFailure(t *testing.T) {
	r := newTestRunner()
	r.config.StrictLifecycle = true
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{
		completed:  true,
		sessionKey: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-BACK-REMOVE-STRICT-001",
		Preconditions: []loader.Condition{
			{"device_in_commissioning_mode": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err == nil {
		t.Fatal("expected strict setup to fail on RemoveZone cleanup failure")
	}
	if !errors.Is(err, errRemoveZoneNoConnection) {
		t.Fatalf("expected no-connection strict error, got: %v", err)
	}
}

func TestSendRemoveZoneOnConn_StrictMode_AckReadFailureIsBestEffort(t *testing.T) {
	r := newTestRunner()
	r.config.StrictLifecycle = true
	r.clearStrictLifecycleErr()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Read the request frame and then close before sending an ack frame.
		_, _ = transport.NewFramer(server).ReadFrame()
		_ = server.Close()
	}()

	r.SendRemoveZoneOnConn(conn, "zone-1")
	<-done

	if r.strictLifecycleErr != nil {
		t.Fatalf("expected no strict lifecycle error for best-effort ack read failure, got: %v", r.strictLifecycleErr)
	}
}

func TestSendRemoveZoneOnConn_StrictMode_RecordsHardFailure(t *testing.T) {
	r := newTestRunner()
	r.config.StrictLifecycle = true
	r.clearStrictLifecycleErr()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	// Empty zone ID is a hard failure (not best-effort).
	r.SendRemoveZoneOnConn(conn, "")
	if !errors.Is(r.strictLifecycleErr, errRemoveZoneNoZoneID) {
		t.Fatalf("expected strict lifecycle error %v, got: %v", errRemoveZoneNoZoneID, r.strictLifecycleErr)
	}
}

func TestSendRemoveZoneOnConn_StrictMode_DeviceNotFoundIsBestEffort(t *testing.T) {
	r := newTestRunner()
	r.config.StrictLifecycle = true
	r.clearStrictLifecycleErr()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		fr := transport.NewFramer(server)
		reqData, err := fr.ReadFrame()
		if err != nil {
			return
		}
		req, err := wire.DecodeRequest(reqData)
		if err != nil {
			return
		}
		resp := &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidCommand,
			Payload: &wire.ErrorPayload{
				Message: "device not found",
			},
		}
		respData, err := wire.EncodeResponse(resp)
		if err != nil {
			return
		}
		_ = fr.WriteFrame(respData)
	}()

	r.SendRemoveZoneOnConn(conn, "zone-1")
	<-done

	if r.strictLifecycleErr != nil {
		t.Fatalf("expected no strict lifecycle error for best-effort 'device not found', got: %v", r.strictLifecycleErr)
	}
}

func TestSendRemoveZoneOnConn_StrictMode_SendFailureIsBestEffort(t *testing.T) {
	r := newTestRunner()
	r.config.StrictLifecycle = true
	r.clearStrictLifecycleErr()

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}

	// Close peer before the write so WriteFrame fails with broken pipe/reset.
	_ = server.Close()

	r.SendRemoveZoneOnConn(conn, "zone-1")

	if r.strictLifecycleErr != nil {
		t.Fatalf("expected no strict lifecycle error for best-effort send failure, got: %v", r.strictLifecycleErr)
	}
}

func TestSetupPreconditions_BackwardsFromCommissioned_SendsRemoveZone(t *testing.T) {
	// When transitioning from commissioned (level 3) to commissioning (level 1),
	// sendRemoveZone is called before disconnect. Since there's no real server,
	// the send will silently fail but the transition should still complete.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{
		completed:  true,
		sessionKey: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-BACK-REMOVE-001",
		Preconditions: []loader.Condition{
			{"device_in_commissioning_mode": true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connection should be closed after backward transition
	if r.pool.Main().isConnected() {
		t.Error("expected connection to be closed")
	}
	// PASE state should be cleared
	if r.connMgr.PASEState() != nil {
		t.Error("expected paseState to be nil")
	}
}

func TestPreconditionLevel_DeviceInZoneKeys(t *testing.T) {
	r := newTestRunner()

	// device_in_zone requires commissioned level (the device must be in a zone).
	{
		conditions := []loader.Condition{{PrecondDeviceInZone: true}}
		got := r.preconditionLevel(conditions)
		if got != precondLevelCommissioned {
			t.Errorf("preconditionLevel(%s) = %d, want %d (commissioned)", PrecondDeviceInZone, got, precondLevelCommissioned)
		}
	}

	// device_in_two_zones is simulation-only (level 0).
	{
		conditions := []loader.Condition{{PrecondDeviceInTwoZones: true}}
		got := r.preconditionLevel(conditions)
		if got != precondLevelNone {
			t.Errorf("preconditionLevel(%s) = %d, want %d (level 0)", PrecondDeviceInTwoZones, got, precondLevelNone)
		}
	}

	// Verify they're registered as simulation keys.
	if !simulationPreconditionKeys[PrecondDeviceInZone] {
		t.Error("expected device_in_zone to be a simulation precondition key")
	}
	if !simulationPreconditionKeys[PrecondDeviceInTwoZones] {
		t.Error("expected device_in_two_zones to be a simulation precondition key")
	}
}

func TestZoneCreatedIsSimulationPreconditionKey(t *testing.T) {
	if !simulationPreconditionKeys[PrecondZoneCreated] {
		t.Error("expected zone_created to be a simulation precondition key")
	}
}

func TestSetupPreconditions_ZoneCreated_SetsStateFlag(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	tc := &loader.TestCase{
		ID: "TC-MASHD-002",
		Preconditions: []loader.Condition{
			{PrecondZoneCreated: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The zone_created flag must be stored in state so handlers can check it.
	v, ok := state.Get(PrecondZoneCreated)
	if !ok {
		t.Fatal("expected zone_created to be set in state")
	}
	if v != true {
		t.Errorf("expected zone_created=true, got %v", v)
	}
}

func TestSetupPreconditions_ClearsZoneCAForNonCommissionedTests(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	// Simulate stale zone CA from a previous commissioned test.
	r.connMgr.SetZoneCAPool(x509.NewCertPool())

	// A level-2 test (connection_established) should clear the stale pool.
	tc := &loader.TestCase{
		ID: "TC-CONN-001",
		Preconditions: []loader.Condition{
			{PrecondConnectionEstablished: true},
		},
	}

	// Will fail to connect (no target), but that's OK -- we're checking
	// that zoneCAPool was cleared before the connection attempt.
	_ = r.setupPreconditions(context.Background(), tc, state)

	if r.connMgr.ZoneCAPool() != nil {
		t.Error("expected zoneCAPool to be nil for non-commissioned (level 2) test")
	}
}

func TestSetupPreconditions_TwoZonesConnected_PreservesZoneCA(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	// Simulate zone CA from a previous commissioned test.
	r.connMgr.SetZoneCAPool(x509.NewCertPool())

	tc := &loader.TestCase{
		ID: "TC-MULTI-003",
		Preconditions: []loader.Condition{
			{PrecondTwoZonesConnected: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// two_zones_connected needs the zone CA for operational TLS, so it
	// should NOT be cleared even though the precondition level is 0.
	if r.connMgr.ZoneCAPool() == nil {
		t.Error("expected zoneCAPool to be preserved for two_zones_connected")
	}
}

func TestSetupPreconditions_ClearsZoneCA_ForNonZoneTests(t *testing.T) {
	r := newTestRunner()
	state := engine.NewExecutionState(context.Background())

	// Simulate stale zone CA from a previous commissioned test.
	r.connMgr.SetZoneCAPool(x509.NewCertPool())

	tc := &loader.TestCase{
		ID: "TC-SIMPLE-001",
		Preconditions: []loader.Condition{
			{PrecondDeviceBooted: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Level 0 test without two_zones_connected should clear stale zone CA.
	if r.connMgr.ZoneCAPool() != nil {
		t.Error("expected zoneCAPool to be nil for simple level-0 test")
	}
}

func TestSetupPreconditions_ClosesStaleZoneConnections(t *testing.T) {
	r := newTestRunner()

	// Simulate zone connections left over from a previous test.
	conn1 := &Connection{state: ConnOperational}
	conn2 := &Connection{state: ConnOperational}
	r.pool.TrackZone("GRID", conn1, "GRID")
	r.pool.TrackZone("LOCAL", conn2, "LOCAL")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-MULTI-003",
		Preconditions: []loader.Condition{
			{PrecondTwoZonesConnected: true},
		},
	}

	_ = r.setupPreconditions(context.Background(), tc, state)

	// Stale zone connections should have been closed.
	if conn1.isConnected() {
		t.Error("expected stale GRID connection to be closed")
	}
	if conn2.isConnected() {
		t.Error("expected stale LOCAL connection to be closed")
	}
	if r.pool.ZoneCount() != 2 {
		// New connections (dummy since no target) should have been created.
		t.Errorf("expected 2 active zone conns, got %d", r.pool.ZoneCount())
	}
}

func TestHandleConnectAsZone_TracksInRunner(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Without a target, handleConnectAsZone won't establish a real connection.
	// Directly verify the runner-level tracking by simulating what the
	// precondition does: dummy connections should be tracked.
	ct := getConnectionTracker(state)
	conn := &Connection{state: ConnOperational}
	ct.zoneConnections["GRID"] = conn
	r.pool.TrackZone("GRID", conn, "GRID")

	if r.pool.Zone("GRID") == nil {
		t.Error("expected GRID to be tracked in pool zones")
	}
}

func TestEnsureDisconnected_ClearsZoneCA(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational

	// Simulate state left over from a previous commissioned test.
	r.connMgr.SetZoneCA(&cert.ZoneCA{})
	r.connMgr.SetControllerCert(&cert.OperationalCert{})
	r.connMgr.SetZoneCAPool(x509.NewCertPool())

	r.ensureDisconnected()

	if r.connMgr.ZoneCA() != nil {
		t.Error("expected zoneCA to be nil after ensureDisconnected")
	}
	if r.connMgr.ControllerCert() != nil {
		t.Error("expected controllerCert to be nil after ensureDisconnected")
	}
	if r.connMgr.ZoneCAPool() != nil {
		t.Error("expected zoneCAPool to be nil after ensureDisconnected")
	}
}

func TestPreconditionLevel_DeviceStateKeys(t *testing.T) {
	r := newTestRunner()

	tests := []struct {
		name      string
		key       string
		wantLevel int
	}{
		{"device_reset", PrecondDeviceReset, precondLevelCommissioned},
		{"device_has_grid_zone", PrecondDeviceHasGridZone, precondLevelCommissioned},
		{"device_has_local_zone", PrecondDeviceHasLocalZone, precondLevelCommissioned},
		{"device_has_one_zone", PrecondDeviceHasOneZone, precondLevelCommissioned},
		{"device_has_available_zone_slot", PrecondDeviceHasAvailableZoneSlot, precondLevelCommissioned},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := []loader.Condition{{tt.key: true}}
			got := r.preconditionLevel(conditions)
			if got != tt.wantLevel {
				t.Errorf("preconditionLevel(%s) = %d, want %d", tt.key, got, tt.wantLevel)
			}
		})
	}
}

func TestSetupPreconditions_DeviceHasGridZone(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZONE-010",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasGridZone: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify a GRID zone was created in zone state.
	zs := getZoneState(state)
	found := false
	for _, z := range zs.zones {
		if z.ZoneType == ZoneTypeGrid {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a GRID zone to be created")
	}

	// Simulation key should be in state.
	v, ok := state.Get(PrecondDeviceHasGridZone)
	if !ok || v != true {
		t.Error("expected device_has_grid_zone=true in state")
	}
}

func TestSetupPreconditions_DeviceHasBothZones(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZTYPE-004",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasGridZone: true},
			{PrecondDeviceHasLocalZone: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	zs := getZoneState(state)
	if len(zs.zones) != 2 {
		t.Fatalf("expected 2 zones, got %d", len(zs.zones))
	}

	hasGrid, hasLocal := false, false
	for _, z := range zs.zones {
		switch z.ZoneType {
		case ZoneTypeGrid:
			hasGrid = true
		case ZoneTypeLocal:
			hasLocal = true
		}
	}
	if !hasGrid {
		t.Error("expected GRID zone")
	}
	if !hasLocal {
		t.Error("expected LOCAL zone")
	}
}

func TestPreconditionLevel_SessionPreviouslyConnected(t *testing.T) {
	r := newTestRunner()
	conditions := []loader.Condition{
		{PrecondSessionPreviouslyConnected: true},
	}
	got := r.preconditionLevel(conditions)
	if got != precondLevelCommissioned {
		t.Errorf("preconditionLevel(session_previously_connected) = %d, want %d", got, precondLevelCommissioned)
	}
}

func TestSetupPreconditions_SessionPreviouslyConnected(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3, 4}})

	// Set up zone crypto state (simulates what PASE would produce).
	r.connMgr.SetZoneCA(&cert.ZoneCA{})
	r.connMgr.SetControllerCert(&cert.OperationalCert{})
	r.connMgr.SetZoneCAPool(x509.NewCertPool())

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-SUB-010",
		Preconditions: []loader.Condition{
			{PrecondSessionPreviouslyConnected: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Connection should be closed (simulating disconnect).
	if r.pool.Main().isConnected() {
		t.Error("expected connection to be closed after session_previously_connected setup")
	}

	// Zone crypto state should be preserved for reconnection.
	if r.connMgr.ZoneCAPool() == nil {
		t.Error("expected zoneCAPool to be preserved")
	}
	if r.connMgr.ZoneCA() == nil {
		t.Error("expected zoneCA to be preserved")
	}
	if r.connMgr.ControllerCert() == nil {
		t.Error("expected controllerCert to be preserved")
	}

	// PASE state should be cleared (session is over).
	if r.connMgr.PASEState() != nil {
		t.Error("expected paseState to be nil")
	}
}

func TestPreconditionLevel_FreshCommission(t *testing.T) {
	conditions := []loader.Condition{{PrecondFreshCommission: true}}
	got := preconditionLevelFor(conditions)
	if got != precondLevelCommissioned {
		t.Errorf("preconditionLevel(fresh_commission) = %d, want %d", got, precondLevelCommissioned)
	}
}

func TestNeedsFreshCommission(t *testing.T) {
	tests := []struct {
		name       string
		conditions []loader.Condition
		want       bool
	}{
		{"nil conditions", nil, false},
		{"empty conditions", []loader.Condition{}, false},
		{"session_established only", []loader.Condition{{PrecondSessionEstablished: true}}, false},
		{"fresh_commission true", []loader.Condition{{PrecondFreshCommission: true}}, true},
		{"fresh_commission false", []loader.Condition{{PrecondFreshCommission: false}}, false},
		{"mixed with fresh_commission", []loader.Condition{
			{PrecondSessionEstablished: true},
			{PrecondFreshCommission: true},
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsFreshCommission(tt.conditions)
			if got != tt.want {
				t.Errorf("needsFreshCommission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetupPreconditions_SessionReuse_SkipsCloseZoneConns(t *testing.T) {
	// Commissioned runner + session_established test -> session preserved.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID:            "TC-READ-002",
		Preconditions: []loader.Condition{{PrecondSessionEstablished: true}},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !r.pool.Main().isConnected() {
		t.Error("expected connection to remain connected")
	}
	ps := r.connMgr.PASEState()
	if ps == nil || !ps.completed {
		t.Error("expected PASE state to be preserved")
	}
	if r.pool.ZoneCount() != 1 {
		t.Errorf("expected 1 active zone conn, got %d", r.pool.ZoneCount())
	}
}

func TestSetupPreconditions_FreshCommission_ClosesZoneConns(t *testing.T) {
	// fresh_commission=true -> session torn down even at level 3 -> level 3.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-COMM-001",
		Preconditions: []loader.Condition{
			{PrecondSessionEstablished: true},
			{PrecondFreshCommission: true},
		},
	}

	_ = r.setupPreconditions(context.Background(), tc, state)

	// closeActiveZoneConns should have been called -- verify the map is empty.
	if r.pool.ZoneCount() != 0 {
		t.Errorf("expected 0 active zone conns, got %d", r.pool.ZoneCount())
	}
	// Note: paseState is cleared only when real sockets (tlsConn/conn) are
	// present, which stub connections lack. The important assertion is that
	// closeActiveZoneConns was called (activeZoneConns emptied).
}

func TestSetupPreconditions_SessionReuse_TwoZonesConnectedForcesClose(t *testing.T) {
	// two_zones_connected forces teardown even at level 3 -> level 3.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-MULTI-003",
		Preconditions: []loader.Condition{
			{PrecondTwoZonesConnected: true},
		},
	}

	_ = r.setupPreconditions(context.Background(), tc, state)

	// The old main connection should have been closed by closeActiveZoneConns.
	if r.connMgr.PASEState() != nil {
		t.Error("expected paseState cleared after two_zones_connected teardown")
	}
}

func TestSetupPreconditions_SessionReuse_DeviceHasGridZoneForcesClose(t *testing.T) {
	// device_has_grid_zone forces teardown even at level 3 -> level 3.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZONE-010",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasGridZone: true},
		},
	}

	_ = r.setupPreconditions(context.Background(), tc, state)

	// closeActiveZoneConns should have been called, clearing paseState.
	if r.pool.ZoneCount() != 0 {
		t.Errorf("expected 0 active zone conns after device_has_grid_zone, got %d", r.pool.ZoneCount())
	}
}

func TestSetupPreconditions_SessionReuse_BackwardsTransitionUnaffected(t *testing.T) {
	// Level 3 -> level 1 still tears down (canReuseSession=false because needed < commissioned).
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-BACK-002",
		Preconditions: []loader.Condition{
			{PrecondDeviceInCommissioningMode: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.pool.Main().isConnected() {
		t.Error("expected connection to be closed for backwards transition")
	}
	if r.connMgr.PASEState() != nil {
		t.Error("expected paseState to be nil after backwards transition")
	}
}

// Phase 1: Subscription tracking tests.

func TestTrackSubscription(t *testing.T) {
	r := newTestRunner()
	r.trackSubscription(1)
	r.trackSubscription(2)

	subs := r.pool.Subscriptions()
	if len(subs) != 2 {
		t.Fatalf("expected 2 tracked subs, got %d", len(subs))
	}
	if subs[0] != 1 || subs[1] != 2 {
		t.Errorf("unexpected IDs: %v", subs)
	}
}

func TestRemoveActiveSubscription(t *testing.T) {
	r := newTestRunner()
	r.trackSubscription(10)
	r.trackSubscription(20)
	r.trackSubscription(30)

	r.removeActiveSubscription(20)

	subs := r.pool.Subscriptions()
	if len(subs) != 2 {
		t.Fatalf("expected 2 tracked subs after remove, got %d", len(subs))
	}
	// Remaining should be 10 and 30.
	for _, id := range subs {
		if id == 20 {
			t.Error("expected subscription 20 to be removed")
		}
	}
}

func TestRemoveActiveSubscription_NotFound(t *testing.T) {
	r := newTestRunner()
	r.trackSubscription(10)

	// Removing a non-existent ID should be a no-op.
	r.removeActiveSubscription(99)

	subs := r.pool.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("expected 1 tracked sub, got %d", len(subs))
	}
}

func TestTeardownTest_ClearsTrackingList(t *testing.T) {
	r := newTestRunner()
	r.trackSubscription(1)
	r.trackSubscription(2)

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{ID: "TC-TEST-001"}
	r.teardownTest(context.Background(), tc, state)

	subs := r.pool.Subscriptions()
	if len(subs) != 0 {
		t.Errorf("expected empty tracking list after teardown, got %d", len(subs))
	}
}

func TestTeardownTest_NoUnsubscribeWhenDisconnected(t *testing.T) {
	r := newTestRunner()
	r.trackSubscription(1)
	// conn is not connected (default stub)

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{ID: "TC-TEST-001"}

	// Should not panic or attempt to send on a nil/disconnected conn.
	r.teardownTest(context.Background(), tc, state)

	subs := r.pool.Subscriptions()
	if len(subs) != 0 {
		t.Errorf("expected empty tracking list after teardown, got %d", len(subs))
	}
}

func TestTeardownTest_ClearsPendingNotifications(t *testing.T) {
	r := newTestRunner()
	r.pool.AppendNotification([]byte{1, 2, 3})

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{ID: "TC-TEST-001"}
	r.teardownTest(context.Background(), tc, state)

	pending := r.pool.PendingNotifications()
	if len(pending) != 0 {
		t.Errorf("expected empty pending notifications after teardown, got %d", len(pending))
	}
}

// Phase 2: Session health check tests.

func TestProbeSessionHealth_NilConnection(t *testing.T) {
	r := newTestRunner()
	r.pool.SetMain(nil)

	err := r.probeSessionHealth()
	if err == nil {
		t.Fatal("expected error for nil connection")
	}
	if err.Error() != "no active connection" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProbeSessionHealth_DisconnectedConnection(t *testing.T) {
	r := newTestRunner()
	// Default stub has connected=false

	err := r.probeSessionHealth()
	if err == nil {
		t.Fatal("expected error for disconnected connection")
	}
	if err.Error() != "no active connection" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProbeSessionHealth_NilFramer(t *testing.T) {
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.pool.Main().framer = nil

	err := r.probeSessionHealth()
	if err == nil {
		t.Fatal("expected error for nil framer")
	}
	if err.Error() != "no active connection" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestProbeSessionHealth_DoesNotBlockOnWriteToUnresponsivePeer(t *testing.T) {
	r := newTestRunner()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	r.pool.Main().state = ConnOperational
	r.pool.Main().conn = client
	r.pool.Main().framer = transport.NewFramer(client)

	done := make(chan error, 1)
	go func() {
		done <- r.probeSessionHealth()
	}()

	select {
	case <-done:
		// Expected: write deadline should bound this path.
	case <-time.After(healthProbeWriteTimeout + 300*time.Millisecond):
		_ = server.Close() // Unblock writer for cleanup.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("probeSessionHealth blocked on write to unresponsive peer")
	}
}

func TestSetupPreconditions_SessionReuse_FallsBackOnHealthCheckFailure(t *testing.T) {
	// Commissioned runner with connected=true but no real framer.
	// probeSessionHealth should fail (nil framer) and canReuseSession
	// should fall back to closeActiveZoneConns.
	// Set a target so the health check is actually executed (it's
	// skipped for stub connections without a target).
	r := newTestRunner()
	r.config.Target = "127.0.0.1:9999"
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID:            "TC-READ-HEALTH-001",
		Preconditions: []loader.Condition{{PrecondSessionEstablished: true}},
	}

	// setupPreconditions will try to reuse the session but the health
	// check fails (stub connection has no framer), so it falls back to
	// closeActiveZoneConns.
	_ = r.setupPreconditions(context.Background(), tc, state)

	// activeZoneConns should be emptied because closeActiveZoneConns was called.
	if r.pool.ZoneCount() != 0 {
		t.Errorf("expected 0 active zone conns after health check failure, got %d", r.pool.ZoneCount())
	}
}

// Phase 3: Broadened reset condition tests.

func TestSetupPreconditions_SessionReuseNoResetWhenUnmodified(t *testing.T) {
	// Commissioned runner with deviceStateModified=false.
	// The reset trigger should NOT fire when device state was not modified.
	// Verify that session reuse works correctly in this case.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})
	r.connMgr.SetDeviceStateModified(false)
	r.pool.TrackZone("main-abc123", r.pool.Main(), "abc123")

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID:            "TC-READ-003",
		Preconditions: []loader.Condition{{PrecondSessionEstablished: true}},
	}

	// Should succeed without error (no target means reset is skipped).
	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Session should still be reusable (no target means health check skipped too).
	if !r.pool.Main().isConnected() {
		t.Error("expected connection to remain connected")
	}
}

func TestDeviceHasGridZone_PreservesCryptoOnSessionReuse(t *testing.T) {
	// When a completed PASE session exists (suite zone), device_has_grid_zone
	// calls handleCreateZone which generates NEW crypto (GRID Zone CA + cert).
	// But ensureCommissioned reuses the session (no fresh PASE), so the device
	// never learns about the GRID crypto. The runner must restore the original
	// crypto that matches the actual connection.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})

	// Simulate existing suite zone crypto (what the device actually knows).
	origZoneCA := &cert.ZoneCA{}
	origControllerCert := &cert.OperationalCert{}
	origZoneCAPool := x509.NewCertPool()
	origIssuedDeviceCert := &x509.Certificate{}
	r.connMgr.SetZoneCA(origZoneCA)
	r.connMgr.SetControllerCert(origControllerCert)
	r.connMgr.SetZoneCAPool(origZoneCAPool)
	r.connMgr.SetIssuedDeviceCert(origIssuedDeviceCert)

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZTYPE-002",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasGridZone: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The precondition handler should still have set commissionZoneType to GRID.
	if r.connMgr.CommissionZoneType() != cert.ZoneTypeGrid {
		t.Errorf("expected commissionZoneType=GRID, got %v", r.connMgr.CommissionZoneType())
	}

	// But the crypto state must match the original (suite zone) crypto,
	// because the session was reused and the device doesn't know about
	// the GRID crypto.
	if r.connMgr.ZoneCA() != origZoneCA {
		t.Error("expected zoneCA to be restored to original after session reuse")
	}
	if r.connMgr.ControllerCert() != origControllerCert {
		t.Error("expected controllerCert to be restored to original after session reuse")
	}
	if r.connMgr.ZoneCAPool() != origZoneCAPool {
		t.Error("expected zoneCAPool to be restored to original after session reuse")
	}
	if r.connMgr.IssuedDeviceCert() != origIssuedDeviceCert {
		t.Error("expected issuedDeviceCert to be restored to original after session reuse")
	}
}

func TestDeviceHasLocalZone_PreservesCryptoOnSessionReuse(t *testing.T) {
	// Same as grid zone test but for device_has_local_zone.
	r := newTestRunner()
	r.pool.Main().state = ConnOperational
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{1, 2, 3}})

	origZoneCA := &cert.ZoneCA{}
	origControllerCert := &cert.OperationalCert{}
	origZoneCAPool := x509.NewCertPool()
	r.connMgr.SetZoneCA(origZoneCA)
	r.connMgr.SetControllerCert(origControllerCert)
	r.connMgr.SetZoneCAPool(origZoneCAPool)

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZTYPE-003",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasLocalZone: true},
		},
	}

	err := r.setupPreconditions(context.Background(), tc, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if r.connMgr.CommissionZoneType() != cert.ZoneTypeLocal {
		t.Errorf("expected commissionZoneType=LOCAL, got %v", r.connMgr.CommissionZoneType())
	}

	if r.connMgr.ZoneCA() != origZoneCA {
		t.Error("expected zoneCA to be restored to original after session reuse")
	}
	if r.connMgr.ControllerCert() != origControllerCert {
		t.Error("expected controllerCert to be restored to original after session reuse")
	}
	if r.connMgr.ZoneCAPool() != origZoneCAPool {
		t.Error("expected zoneCAPool to be restored to original after session reuse")
	}
}

// TestIPv6PreconditionKeysHaveLevels verifies that the 3 IPv6/multi-interface
// keys are registered in preconditionKeyLevels at level 0 (no connection needed).
func TestIPv6PreconditionKeysHaveLevels(t *testing.T) {
	r := newTestRunner()
	for _, key := range []string{
		PrecondDeviceHasGlobalAndLinkLocal,
		PrecondDeviceHasLinkLocal,
		PrecondDeviceHasMultipleInterfaces,
	} {
		conditions := []loader.Condition{{key: true}}
		got := r.preconditionLevel(conditions)
		if got != precondLevelNone {
			t.Errorf("preconditionLevel(%s) = %d, want %d (level 0)", key, got, precondLevelNone)
		}
	}
}

// TestRemoveSuiteZone_ReconnectsWhenMainDead verifies that removeSuiteZone
// attempts to reconnect the suite zone connection before sending RemoveZone
// when the main pool connection is dead. Without this, the device is left
// with stale zones because sendRemoveZone silently returns when there is
// no live connection.
func TestRemoveSuiteZone_ReconnectsWhenMainDead(t *testing.T) {
	r := newTestRunner()

	// Set up suite zone with zone ID but NO live connection.
	r.suite.Record("suite-zone-id", makeCryptoState())
	// Main connection is dead (default stub has ConnDisconnected).

	// removeSuiteZone should still complete without panic.
	// It will attempt reconnect (which fails since no target), then
	// proceed to cleanup.
	r.removeSuiteZone()

	// After removeSuiteZone, suite should be cleared.
	if r.suite.ZoneID() != "" {
		t.Error("expected suite zone ID to be cleared after removeSuiteZone")
	}
	if r.suite.IsCommissioned() {
		t.Error("expected suite to not be commissioned after removeSuiteZone")
	}
}

// TestRemoveSuiteZone_PromotesSuiteConnToMain verifies that removeSuiteZone
// promotes the suite connection to main when the main pool connection is dead,
// allowing sendRemoveZone to use it for sending the RemoveZone message.
func TestRemoveSuiteZone_PromotesSuiteConnToMain(t *testing.T) {
	r := newTestRunner()

	// Create a suite zone with a "live" connection (ConnOperational).
	suiteConn := &Connection{state: ConnOperational}
	r.suite.Record("suite-zone-id", makeCryptoState())
	r.suite.SetConn(suiteConn)

	// Set PASE state so sendRemoveZone has a session key for zone ID derivation.
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0xDE, 0xAD}})

	// Main connection is dead (default stub has ConnDisconnected).
	if r.pool.Main().isConnected() {
		t.Fatal("precondition: main should be disconnected")
	}

	// removeSuiteZone should promote suite conn to main so RemoveZone can be sent.
	r.removeSuiteZone()

	// After removeSuiteZone, suite should be cleared.
	if r.suite.ZoneID() != "" {
		t.Error("expected suite zone ID to be cleared after removeSuiteZone")
	}
}

// TestRemoveSuiteZone_CleansUpEvenWhenReconnectFails verifies that
// removeSuiteZone still cleans up all state even when reconnection fails.
func TestRemoveSuiteZone_CleansUpEvenWhenReconnectFails(t *testing.T) {
	r := newTestRunner()

	// Set up suite zone with no connection and no target (reconnect will fail).
	r.suite.Record("suite-zone-id", makeCryptoState())
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0xAB}})

	r.removeSuiteZone()

	// All state should be cleared regardless.
	if r.suite.ZoneID() != "" {
		t.Error("expected suite zone cleared")
	}
	if r.connMgr.PASEState() != nil {
		t.Error("expected PASE state cleared")
	}
	if r.connMgr.ZoneCA() != nil {
		t.Error("expected zone CA cleared")
	}
}

func TestRemoveSuiteZone_UsesLiveMainWhenSuiteConnDead(t *testing.T) {
	r := newTestRunner()

	// Suite identity exists, but suite connection is dead.
	r.suite.Record("suite-zone-id", makeCryptoState())
	r.suite.SetConn(&Connection{state: ConnDisconnected})

	// Main connection is live and must be used for teardown RemoveZone.
	client, server := net.Pipe()
	mainConn := &Connection{
		conn:   client,
		framer: transport.NewFramer(client),
		state:  ConnOperational,
	}
	r.pool.SetMain(mainConn)

	gotFrame := make(chan error, 1)
	go func() {
		fr := transport.NewFramer(server)
		for i := 0; i < 3; i++ {
			frame, err := fr.ReadFrame()
			if err != nil {
				gotFrame <- err
				return
			}
			req, err := wire.DecodeRequest(frame)
			if err != nil {
				continue
			}
			if req.Operation == wire.OpInvoke {
				gotFrame <- nil
				_ = server.Close()
				return
			}
		}
		gotFrame <- errors.New("did not observe invoke request")
		_ = server.Close()
	}()

	r.removeSuiteZone()

	select {
	case err := <-gotFrame:
		if err != nil {
			t.Fatalf("expected RemoveZone request frame, got read/decode error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected removeSuiteZone to send RemoveZone on live main connection")
	}
	if r.suite.ZoneID() != "" {
		t.Fatal("expected suite zone to be cleared after teardown")
	}
}

func TestAdoptMainAsSuiteIfPossible_SucceedsForCommissionedTestZone(t *testing.T) {
	r := newTestRunner()

	main := &Connection{state: ConnOperational}
	r.pool.SetMain(main)
	r.connMgr.SetCommissionZoneType(cert.ZoneTypeTest)
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0xAA, 0xBB, 0xCC}})
	r.connMgr.SetWorkingCrypto(makeCryptoState())

	ok := r.adoptMainAsSuiteIfPossible()
	if !ok {
		t.Fatal("expected adoption to succeed")
	}
	if r.suite.ZoneID() == "" {
		t.Fatal("expected suite zone id to be populated")
	}
	if r.suite.Conn() != main {
		t.Fatal("expected main connection to become suite connection")
	}
	if r.pool.Main() == main {
		t.Fatal("expected main to be detached after suite adoption")
	}
}

func TestAdoptMainAsSuiteIfPossible_RejectsNonTestCommissionType(t *testing.T) {
	r := newTestRunner()

	main := &Connection{state: ConnOperational}
	r.pool.SetMain(main)
	r.connMgr.SetCommissionZoneType(cert.ZoneTypeGrid)
	r.connMgr.SetPASEState(&PASEState{completed: true, sessionKey: []byte{0xAA}})
	r.connMgr.SetWorkingCrypto(makeCryptoState())

	ok := r.adoptMainAsSuiteIfPossible()
	if ok {
		t.Fatal("expected adoption to be rejected for non-TEST commission type")
	}
	if r.suite.ZoneID() != "" {
		t.Fatal("expected suite to remain empty")
	}
}

func TestDeviceHasGridZone_KeepsNewCryptoOnFreshCommission(t *testing.T) {
	// When there's NO existing PASE session, ensureCommissioned does a fresh
	// commission. In this case the new GRID crypto IS correct (it matches
	// the fresh PASE) and should NOT be restored.
	r := newTestRunner()
	// No PASE session -- paseState is nil.
	r.pool.Main().state = ConnDisconnected

	state := engine.NewExecutionState(context.Background())
	tc := &loader.TestCase{
		ID: "TC-ZTYPE-FRESH",
		Preconditions: []loader.Condition{
			{PrecondDeviceHasGridZone: true},
		},
	}

	// Will fail at ensureConnected (no target), but precondition handlers
	// already ran. Check that zoneCA was set by handleCreateZone and NOT
	// restored (since there was no prior session to restore).
	_ = r.setupPreconditions(context.Background(), tc, state)

	// handleCreateZone should have set new GRID crypto.
	if r.connMgr.ZoneCA() == nil {
		t.Error("expected zoneCA to be set by handleCreateZone (not restored to nil)")
	}
	if r.connMgr.ControllerCert() == nil {
		t.Error("expected controllerCert to be set by handleCreateZone (not restored to nil)")
	}
}

func TestRecoverDeadSuiteControlChannel_ClearsSuiteOnReconnectFailure(t *testing.T) {
	r := newTestRunner()
	r.config.Target = "localhost:8443"
	r.config.EnableKey = "deadbeef"
	r.suite.Record("suite-zone-1", makeCryptoState())
	// Dead suite connection.
	r.suite.SetConn(&Connection{state: ConnDisconnected})

	// No reconnect target/crypto should make reconnectToZone fail.
	r.recoverDeadSuiteControlChannel(engine.NewExecutionState(context.Background()))

	if r.suite.ZoneID() != "" {
		t.Fatal("expected suite zone to be cleared when reconnect is unrecoverable")
	}
	if r.connMgr.PASEState() != nil {
		t.Fatal("expected PASE state cleared when suite is cleared")
	}
}

func TestRecoverDeadSuiteControlChannel_NoopWhenSuiteAlive(t *testing.T) {
	r := newTestRunner()
	r.config.Target = "localhost:8443"
	r.config.EnableKey = "deadbeef"
	liveConn := &Connection{state: ConnOperational, framer: &transport.Framer{}}
	r.suite.Record("suite-zone-1", makeCryptoState())
	r.suite.SetConn(liveConn)

	r.recoverDeadSuiteControlChannel(engine.NewExecutionState(context.Background()))

	if r.suite.ZoneID() == "" {
		t.Fatal("expected suite zone to be preserved when suite conn is alive")
	}
	if r.suite.Conn() != liveConn {
		t.Fatal("expected suite connection to be unchanged when alive")
	}
}
