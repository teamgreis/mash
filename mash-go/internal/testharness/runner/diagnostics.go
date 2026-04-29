package runner

import (
	"context"
	"fmt"
	"sort"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// DeviceStateSnapshot is a map of key-value pairs representing device mutable
// state at a point in time. The keys match those returned by the device's
// getTestState command.
type DeviceStateSnapshot map[string]any

// StateDiff describes a single field that changed between two snapshots.
type StateDiff struct {
	Key    string `json:"key"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

var volatileBaselineKeys = map[string]struct{}{
	// Zone identity and per-zone timer internals are expected to change when
	// suite zones are re-created across fresh_commission transitions.
	"zones":           {},
	"failsafe_timers": {},
	// Pairing state can toggle around commissioning transitions and is not
	// a stable per-test baseline invariant.
	"pairing_active": {},
	// Connection accounting can vary around fresh_commission reconnect windows.
	"active_conns":       {},
	"conn_tracker_count": {},
}

// diffSnapshots compares two device state snapshots and returns fields that
// differ. Keys present in only one snapshot are included (with nil for the
// missing side). This is the core leak-detection mechanism: if post-test
// state differs from pre-test state on fields that should have been reset,
// a leak occurred.
func diffSnapshots(before, after DeviceStateSnapshot) []StateDiff {
	if before == nil || after == nil {
		return nil
	}

	seen := make(map[string]bool, len(before)+len(after))
	var diffs []StateDiff

	for k, bv := range before {
		seen[k] = true
		av, ok := after[k]
		if !ok {
			diffs = append(diffs, StateDiff{Key: k, Before: bv, After: nil})
		} else if fmt.Sprintf("%v", bv) != fmt.Sprintf("%v", av) {
			diffs = append(diffs, StateDiff{Key: k, Before: bv, After: av})
		}
	}
	for k, av := range after {
		if !seen[k] {
			diffs = append(diffs, StateDiff{Key: k, Before: nil, After: av})
		}
	}

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Key < diffs[j].Key })
	return diffs
}

func filterBaselineDiffs(diffs []StateDiff) []StateDiff {
	if len(diffs) == 0 {
		return nil
	}
	out := make([]StateDiff, 0, len(diffs))
	for _, d := range diffs {
		if _, volatile := volatileBaselineKeys[d.Key]; volatile {
			continue
		}
		out = append(out, d)
	}
	return out
}

// requestDeviceState sends a getTestState invoke to the device and returns
// the response payload as a DeviceStateSnapshot. It uses the same connection
// search chain as sendTriggerViaZone: main conn -> per-test zone conns ->
// runner-level activeZoneConns (suite zone).
//
// Returns nil (no error) if no connection is available -- this is a
// best-effort diagnostic, not a test failure.
func (r *Runner) requestDeviceState(ctx context.Context, state *engine.ExecutionState) DeviceStateSnapshot {
	if r.config.EnableKey == "" {
		return nil
	}

	conn := r.findZoneConnection(state)
	if conn == nil {
		return nil
	}

	// Parameters use integer keys per docs/interaction-model.md §7.1 (Issue #11).
	// String-keyed params decode into a CBOR map the device's command
	// machinery cannot match against required parameter IDs, so the device
	// returns StatusInvalidParameter (status 5) before reaching the
	// enable-key check -- which silently disabled the residual zone sweep
	// (F-6: TC-E2E-001 cert leak).
	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureTestControl), //nolint:gosec // constant fits in uint8
		Payload: &wire.InvokePayload{
			CommandID: service.TestControlCmdGetTestState,
			Parameters: map[uint8]any{
				service.TestControlCmdGetTestStateParamEnableKey: r.config.EnableKey,
			},
		},
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		r.debugf("requestDeviceState: encode error: %v", err)
		return nil
	}

	conn.setReadDeadlineFromContext(ctx)
	defer conn.clearReadDeadline()
	conn.setWriteDeadlineFromContext(ctx)
	defer conn.clearWriteDeadline()

	if err := conn.framer.WriteFrame(data); err != nil {
		r.debugf("requestDeviceState: write error: %v", err)
		return nil
	}

	// Read frames, skipping notifications, until we get the invoke response.
	discarded := 0
	for range 10 {
		respData, err := conn.framer.ReadFrame()
		if err != nil {
			r.debugf("requestDeviceState: read error: %v", err)
			return nil
		}

		msgType, peekErr := wire.PeekMessageType(respData)
		if peekErr == nil && msgType == wire.MessageTypeNotification {
			conn.pendingNotifications = append(conn.pendingNotifications, respData)
			continue
		}

		resp, err := wire.DecodeResponse(respData)
		if err != nil {
			r.debugf("requestDeviceState: decode error: %v", err)
			return nil
		}
		if resp.MessageID == 0 {
			conn.pendingNotifications = append(conn.pendingNotifications, respData)
			discarded++
			continue
		}
		if resp.MessageID != req.MessageID {
			r.debugf("requestDeviceState: discarding orphaned response (got msgID=%d, want %d)", resp.MessageID, req.MessageID)
			discarded++
			continue
		}

		if !resp.IsSuccess() {
			r.debugf("requestDeviceState: status %d", resp.Status)
			return nil
		}
		if discarded > 0 {
			r.debugf("requestDeviceState: discarded %d stale frames", discarded)
		}

		return payloadToSnapshot(resp.Payload)
	}

	r.debugf("requestDeviceState: no matching response after 10 frames (discarded=%d)", discarded)
	return nil
}

// findZoneConnection returns the first usable operational connection,
// searching: main conn -> per-test zone conns -> activeZoneConns (suite zone).
func (r *Runner) findZoneConnection(state *engine.ExecutionState) *Connection {
	if sc := r.suite.Conn(); sc != nil && sc.isConnected() && sc.framer != nil {
		return sc
	}
	if r.pool.Main() != nil && r.pool.Main().isConnected() && r.pool.Main().framer != nil {
		return r.pool.Main()
	}
	if state != nil {
		ct := getConnectionTracker(state)
		for _, c := range ct.zoneConnections {
			if c.isConnected() && c.framer != nil {
				return c
			}
		}
	}
	for _, key := range r.pool.ZoneKeys() {
		if c := r.pool.Zone(key); c != nil && c.isConnected() && c.framer != nil {
			return c
		}
	}
	return nil
}

// payloadToSnapshot converts a CBOR response payload to a DeviceStateSnapshot.
// CBOR decoding produces map[any]any which JSON can't marshal, so we
// recursively normalize all maps to map[string]any.
func payloadToSnapshot(payload any) DeviceStateSnapshot {
	normalized := normalizeCBOR(payload)
	if m, ok := normalized.(map[string]any); ok {
		return DeviceStateSnapshot(m)
	}
	return nil
}

// normalizeCBOR recursively converts map[any]any (from CBOR) to map[string]any
// so the result is JSON-serializable.
func normalizeCBOR(v any) any {
	switch val := v.(type) {
	case map[any]any:
		result := make(map[string]any, len(val))
		for k, v2 := range val {
			result[fmt.Sprintf("%v", k)] = normalizeCBOR(v2)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v2 := range val {
			result[k] = normalizeCBOR(v2)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v2 := range val {
			result[i] = normalizeCBOR(v2)
		}
		return result
	default:
		return v
	}
}
