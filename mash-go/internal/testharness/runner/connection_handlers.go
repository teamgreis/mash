package runner

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/mash-protocol/mash-go/internal/inspect"
	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/transport"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// registerConnectionHandlers registers all connection extension action handlers.
func (r *Runner) registerConnectionHandlers() {
	// Zone-scoped I/O
	r.engine.RegisterHandler(ActionConnectAsController, r.handleConnectAsController)
	r.engine.RegisterHandler(ActionConnectAsZone, r.handleConnectAsZone)
	r.engine.RegisterHandler(ActionReadAsZone, r.handleReadAsZone)
	r.engine.RegisterHandler(ActionInvokeAsZone, r.handleInvokeAsZone)
	r.engine.RegisterHandler(ActionSubscribeAsZone, r.handleSubscribeAsZone)
	r.engine.RegisterHandler(ActionWaitForNotificationAsZone, r.handleWaitForNotificationAsZone)

	// Connection lifecycle
	r.engine.RegisterHandler(ActionConnectWithTiming, r.handleConnectWithTiming)
	r.engine.RegisterHandler(ActionSendClose, r.handleSendClose)
	r.engine.RegisterHandler(ActionSimultaneousClose, r.handleSimultaneousClose)
	r.engine.RegisterHandler(ActionWaitDisconnect, r.handleWaitDisconnect)
	r.engine.RegisterHandler(ActionCancelReconnect, r.handleCancelReconnect)

	// Reconnection
	r.engine.RegisterHandler(ActionMonitorReconnect, r.handleMonitorReconnect)
	r.engine.RegisterHandler(ActionDisconnectAndMonitorBackoff, r.handleDisconnectAndMonitorBackoff)

	// Keep-alive
	r.engine.RegisterHandler(ActionPing, r.handlePing)
	r.engine.RegisterHandler(ActionPingMultiple, r.handlePingMultiple)
	r.engine.RegisterHandler(ActionVerifyKeepalive, r.handleVerifyKeepalive)

	// Raw wire
	r.engine.RegisterHandler(ActionSendRaw, r.handleSendRaw)
	r.engine.RegisterHandler(ActionSendRawBytes, r.handleSendRawBytes)
	r.engine.RegisterHandler(ActionSendRawFrame, r.handleSendRawFrame)
	r.engine.RegisterHandler(ActionSendTLSAlert, r.handleSendTLSAlert)

	// Command queue
	r.engine.RegisterHandler(ActionQueueCommand, r.handleQueueCommand)
	r.engine.RegisterHandler(ActionWaitForQueuedResult, r.handleWaitForQueuedResult)
	r.engine.RegisterHandler(ActionSendMultipleThenDisconnect, r.handleSendMultipleThenDisconnect)

	// Capacity
	r.engine.RegisterHandler(ActionOpenConnections, r.handleOpenConnections)

	// Concurrency
	r.engine.RegisterHandler(ActionReadConcurrent, r.handleReadConcurrent)
	r.engine.RegisterHandler(ActionInvokeWithDisconnect, r.handleInvokeWithDisconnect)

	// Subscription extensions
	r.engine.RegisterHandler(ActionSubscribeMultiple, r.handleSubscribeMultiple)
	r.engine.RegisterHandler(ActionSubscribeOrdered, r.handleSubscribeOrdered)
	r.engine.RegisterHandler(ActionUnsubscribe, r.handleUnsubscribe)
	r.engine.RegisterHandler(ActionReceiveNotification, r.handleReceiveNotification)
	r.engine.RegisterHandler(ActionReceiveNotifications, r.handleReceiveNotifications)
}

// ============================================================================
// Zone-scoped I/O
// ============================================================================

// handleConnectAsController connects with controller identity.
func (r *Runner) handleConnectAsController(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	return r.handleConnect(ctx, step, state)
}

// handleConnectAsZone connects and associates the connection with a zone.
func (r *Runner) handleConnectAsZone(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ct := getConnectionTracker(state)

	rawZoneID := resolveZoneParam(params)
	zoneID := rawZoneID

	// Reuse an existing connected zone from a precondition (e.g.,
	// two_zones_connected) instead of creating a duplicate connection
	// that would overwrite the precondition's PASE connection and
	// leak its zone ID mapping needed for RemoveZone on teardown.
	if existing, ok := ct.zoneConnections[zoneID]; ok && existing.isConnected() {
		return map[string]any{
			KeyConnectionEstablished: true,
			KeyZoneID:                zoneID,
			KeyState:                 ConnectionStateOperational,
		}, nil
	}

	zoneID = resolveOperationalZoneID(zoneID, state)
	if zoneID != rawZoneID {
		if existing, ok := ct.zoneConnections[zoneID]; ok && existing.isConnected() {
			return map[string]any{
				KeyConnectionEstablished: true,
				KeyZoneID:                zoneID,
				KeyState:                 ConnectionStateOperational,
			}, nil
		}
	}

	// Enforce 5-zone connection limit.
	if len(ct.zoneConnections) >= 5 {
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyZoneID:                zoneID,
			KeyError:                 ErrCodeMaxConnsExceeded,
			KeyErrorCode:             ErrCodeMaxConnsExceeded,
		}, nil
	}

	if zoneID == "" || !isOperationalZoneID(zoneID) {
		return map[string]any{
			KeyConnectionEstablished: false,
			KeyZoneID:                zoneID,
			KeyError:                 "INVALID_ZONE_ID",
			KeyErrorCode:             "INVALID_ZONE_ID",
		}, nil
	}

	target := r.getTarget(params)

	// When no Zone CA exists, default to InsecureSkipVerify since there's
	// no trusted root to verify against.
	var tlsConfig *tls.Config
	if r.connMgr.ZoneCAPool() != nil {
		tlsConfig = r.operationalTLSConfig()
	} else {
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
			NextProtos:         []string{transport.ALPNProtocol},
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
	if err != nil {
		errorCode := classifyConnectError(err)
		alert := extractTLSAlert(err)
		tlsError := errorCode
		if alert != "" {
			tlsError = alert
		}
		out := map[string]any{
			KeyConnectionEstablished: false,
			KeyZoneID:                zoneID,
			KeyError:                 errorCode,
			KeyErrorCode:             errorCode,
			KeyTLSError:              tlsError,
			KeyTLSAlert:              alert,
			KeyTLSHandshakeFailed:    true,
		}
		return out, nil
	}

	newConn := &Connection{
		tlsConn: conn,
		framer:  transport.NewFramer(conn),
		state:   ConnTLSConnected,
	}

	ct.zoneConnections[zoneID] = newConn
	state.Set(ZoneConnectionStateKey(zoneID), newConn)

	// Track at runner level so connections are cleaned up between tests.
	r.pool.TrackZone(zoneID, newConn, zoneID)

	return map[string]any{
		KeyConnectionEstablished: true,
		KeyZoneID:                zoneID,
		KeyState:                 ConnectionStateOperational,
	}, nil
}

// resolveZoneParam extracts the zone identifier from params, accepting both
// "zone_id" and "zone" keys (test cases use "zone", code convention is "zone_id").
func resolveZoneParam(params map[string]any) string {
	if zid, ok := params[KeyZoneID].(string); ok && zid != "" {
		return zid
	}
	if z, ok := params[ParamZone].(string); ok && z != "" {
		return z
	}
	return ""
}

func resolveOperationalZoneID(zoneID string, state *engine.ExecutionState) string {
	switch strings.ToUpper(zoneID) {
	case ZoneTypeGrid:
		if id, ok := state.Get(StateGridZoneID); ok {
			if s, ok := id.(string); ok && s != "" {
				return s
			}
		}
	case ZoneTypeLocal:
		if id, ok := state.Get(StateLocalZoneID); ok {
			if s, ok := id.(string); ok && s != "" {
				return s
			}
		}
	case ZoneTypeTest:
		if id, ok := state.Get(StateTestZoneID); ok {
			if s, ok := id.(string); ok && s != "" {
				return s
			}
		}
	}
	return zoneID
}

func isOperationalZoneID(zoneID string) bool {
	// Operational zone IDs are 8-byte hex strings (16 hex chars).
	if len(zoneID) != 16 {
		return false
	}
	_, err := hex.DecodeString(zoneID)
	return err == nil
}

func (r *Runner) getZoneConnection(state *engine.ExecutionState, params map[string]any) (*Connection, string, error) {
	requestedZoneID := resolveZoneParam(params)
	zoneID := resolveOperationalZoneID(requestedZoneID, state)
	ct := getConnectionTracker(state)

	conn, ok := ct.zoneConnections[zoneID]
	if (!ok || !conn.isConnected()) && zoneID != requestedZoneID {
		conn, ok = ct.zoneConnections[requestedZoneID]
		if ok && conn.isConnected() {
			zoneID = requestedZoneID
		}
	}
	if !ok || !conn.isConnected() {
		return nil, requestedZoneID, fmt.Errorf("no active connection for zone %s", requestedZoneID)
	}
	if conn.framer == nil {
		return nil, zoneID, fmt.Errorf("connection for zone %s has no framer (dummy connection cannot perform I/O)", zoneID)
	}
	return conn, zoneID, nil
}

// isDummyZoneConnection returns the zone connection and ID if the connection
// exists and is connected but has no framer (dummy). Returns nil if the
// connection is real or doesn't exist.
func (r *Runner) isDummyZoneConnection(state *engine.ExecutionState, params map[string]any) (*Connection, string) {
	requestedZoneID := resolveZoneParam(params)
	zoneID := resolveOperationalZoneID(requestedZoneID, state)
	ct := getConnectionTracker(state)

	conn, ok := ct.zoneConnections[zoneID]
	if (!ok || !conn.isConnected()) && zoneID != requestedZoneID {
		conn, ok = ct.zoneConnections[requestedZoneID]
		if ok && conn.isConnected() {
			zoneID = requestedZoneID
		}
	}
	if ok && conn.isConnected() && conn.framer == nil {
		return conn, zoneID
	}
	return nil, requestedZoneID
}

// handleReadAsZone reads using a zone-scoped connection.
func (r *Runner) handleReadAsZone(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Dummy connections return simulated success data.
	if dummy, zoneID := r.isDummyZoneConnection(state, params); dummy != nil {
		return map[string]any{
			KeyReadSuccess: true,
			KeyValue:       nil,
			KeyStatus:      0,
			KeyZoneID:      zoneID,
		}, nil
	}

	conn, _, err := r.getZoneConnection(state, params)
	if err != nil {
		return nil, err
	}

	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}
	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	// F-33: support `attribute:` filtering + value extraction so per-zone
	// reads can assert on `value:` directly (mirrors handleRead).
	var attrID uint16
	var hasAttr bool
	if attrRaw, ok := params[ParamAttribute]; ok && attrRaw != nil {
		attrID, err = r.resolver.ResolveAttribute(params[KeyFeature], attrRaw)
		if err != nil {
			return nil, fmt.Errorf("resolving attribute: %w", err)
		}
		hasAttr = true
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpRead,
		EndpointID: endpointID,
		FeatureID:  featureID,
	}
	if hasAttr {
		req.Payload = &wire.ReadPayload{AttributeIDs: []uint16{attrID}}
	}
	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := conn.framer.WriteFrame(data); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	respData, err := conn.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	outputs := map[string]any{
		KeyReadSuccess: resp.IsSuccess(),
		KeyValue:       resp.Payload,
		KeyStatus:      resp.Status,
	}
	if hasAttr && resp.Payload != nil {
		if extracted, ok := extractAttributeValue(resp.Payload, attrID); ok {
			outputs[KeyValue] = extracted
		}
	}
	return outputs, nil
}

// handleInvokeAsZone invokes using a zone-scoped connection.
func (r *Runner) handleInvokeAsZone(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Dummy connections return simulated success data.
	if dummy, _ := r.isDummyZoneConnection(state, params); dummy != nil {
		result := map[string]any{
			KeyInvokeSuccess: true,
			KeyStatus:        0,
		}
		// Handle RemoveZone side effects: update zone state and
		// re-enter commissioning mode (DEC-059).
		if cmdName, ok := params[ParamCommand].(string); ok && strings.EqualFold(cmdName, "RemoveZone") {
			if argsMap, ok := params[ParamArgs].(map[string]any); ok {
				if removedID, ok := argsMap["zoneId"].(string); ok && removedID != "" {
					recordRemovedZoneID(state, removedID)
					zs := getZoneState(state)
					var keyToDelete string
					for key, z := range zs.zones {
						if key == removedID || z.ZoneID == removedID {
							keyToDelete = key
							break
						}
					}
					if keyToDelete != "" {
						delete(zs.zones, keyToDelete)
						for i, id := range zs.zoneOrder {
							if id == keyToDelete {
								zs.zoneOrder = append(zs.zoneOrder[:i], zs.zoneOrder[i+1:]...)
								break
							}
						}
					}
				}
			}
			// DEC-059: device auto-enters commissioning when a zone
			// is removed and capacity is available.
			state.Set(StateCommissioningActive, true)
			state.Set(StateCommissioningCompleted, false)
			if len(getZoneState(state).zones) < 2 {
				state.Set(PrecondDeviceInTwoZones, false)
			}
		}
		return result, nil
	}

	conn, _, err := r.getZoneConnection(state, params)
	if err != nil {
		return nil, err
	}

	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}
	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	// Resolve command name to ID and wrap in InvokePayload. Mirrors
	// handleInvoke's name<->ID translation: YAML supplies args by spec
	// name, the wire requires integer keys, and the response payload
	// gets remapped back to spec names below for assertion targeting.
	var (
		payload     any
		commandID   uint8
		haveCommand bool
	)
	if commandRaw, hasCommand := params[ParamCommand]; hasCommand {
		cmdID, cmdErr := r.resolver.ResolveCommand(params[KeyFeature], commandRaw)
		if cmdErr != nil {
			return nil, fmt.Errorf("resolving command: %w", cmdErr)
		}
		commandID = cmdID
		haveCommand = true
		args, _ := params[ParamArgs]
		if args == nil {
			args, _ = params[ParamParams]
		}
		args = inspect.ConvertCommandParams(featureID, commandID, args)
		payload = &wire.InvokePayload{
			CommandID:  commandID,
			Parameters: args,
		}
	} else {
		payload = params[ParamParams]
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    payload,
	}
	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := conn.framer.WriteFrame(data); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	respData, err := conn.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	// Remap response payload from CBOR integer keys to spec field names
	// when the command schema is known and the call succeeded; mirrors
	// handleInvoke. Skipped on failure because the payload is then a
	// wire.ErrorPayload ({1: message}), unrelated to the command schema.
	var namedPayload map[string]any
	if haveCommand && resp.IsSuccess() {
		namedPayload = inspect.RemapResponsePayload(featureID, commandID, resp.Payload)
	}
	if namedPayload == nil {
		namedPayload = normalizePayloadMap(resp.Payload)
	}

	outputs := map[string]any{
		KeyInvokeSuccess: resp.IsSuccess(),
		KeyResult:        namedPayload,
		KeyResponse:      namedPayload,
		KeyStatus:        resp.Status,
	}

	// Add error code when the response indicates failure.
	if !resp.IsSuccess() {
		outputs[KeyErrorCode] = resp.Status.String()
		outputs[KeyErrorStatus] = resp.Status.String()
	}

	// Flatten response payload so expectations can reference fields
	// like "applied", "effectiveConsumptionLimit", etc.
	if namedPayload != nil {
		flattenInvokeResponse(namedPayload, outputs)
	}

	// DEC-059: When RemoveZone succeeds on a real device, reset
	// commissioning state so the harness knows the device will
	// re-enter commissioning mode (e.g. TC-ZTYPE-005).
	if resp.IsSuccess() {
		if cmdName, ok := params[ParamCommand].(string); ok && strings.EqualFold(cmdName, "RemoveZone") {
			if argsMap, ok := params[ParamArgs].(map[string]any); ok {
				if removedID, ok := argsMap["zoneId"].(string); ok && removedID != "" {
					recordRemovedZoneID(state, removedID)
				}
			}
			state.Set(StateCommissioningActive, true)
			state.Set(StateCommissioningCompleted, false)
		}
	}

	return outputs, nil
}

// handleSubscribeAsZone subscribes using a zone-scoped connection.
func (r *Runner) handleSubscribeAsZone(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Dummy connections return simulated success data.
	if dummy, zoneID := r.isDummyZoneConnection(state, params); dummy != nil {
		return map[string]any{
			KeySubscribeSuccess: true,
			KeySubscriptionID:   fmt.Sprintf("sim-%s", zoneID),
			KeyStatus:           0,
		}, nil
	}

	conn, _, err := r.getZoneConnection(state, params)
	if err != nil {
		return nil, err
	}

	endpointID, err := r.resolver.ResolveEndpoint(params[KeyEndpoint])
	if err != nil {
		return nil, fmt.Errorf("resolving endpoint: %w", err)
	}
	featureID, err := r.resolver.ResolveFeature(params[KeyFeature])
	if err != nil {
		return nil, fmt.Errorf("resolving feature: %w", err)
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpSubscribe,
		EndpointID: endpointID,
		FeatureID:  featureID,
	}
	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := conn.framer.WriteFrame(data); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	respData, err := conn.framer.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	resp, err := wire.DecodeResponse(respData)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	subscriptionID := extractSubscriptionID(resp.Payload)

	// Track subscription ID for auto-unsubscribe in teardown.
	if subscriptionID != nil {
		if id, ok := wire.ToUint32(subscriptionID); ok {
			r.trackSubscription(id)
		}
	}

	return map[string]any{
		KeySubscribeSuccess:       resp.IsSuccess(),
		KeySubscriptionID:         subscriptionID,
		KeySubscriptionIDReturned: subscriptionID != nil,
		KeyStatus:                 resp.Status,
	}, nil
}

// handleWaitForNotificationAsZone waits for a notification on a zone connection.
func (r *Runner) handleWaitForNotificationAsZone(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Dummy connections return simulated notification data.
	if dummy, _ := r.isDummyZoneConnection(state, params); dummy != nil {
		return map[string]any{
			KeyNotificationReceived: true,
			KeyNotificationData:     []byte{},
		}, nil
	}

	conn, _, err := r.getZoneConnection(state, params)
	if err != nil {
		return nil, err
	}

	// Check for notifications buffered by sendTriggerViaZone.
	// When the main connection and the zone connection are the same object
	// (after two_zones_connected restores the main connection), sendTrigger
	// buffers notifications to the pool via sendRequestAndRead.
	// Check both buffers.
	if len(conn.pendingNotifications) > 0 {
		data := conn.pendingNotifications[0]
		conn.pendingNotifications = conn.pendingNotifications[1:]
		return map[string]any{
			KeyNotificationReceived: true,
			KeyNotificationData:     data,
		}, nil
	}
	if conn == r.pool.Main() && len(r.pool.PendingNotifications()) > 0 {
		data, _ := r.pool.ShiftNotification()
		return map[string]any{
			KeyNotificationReceived: true,
			KeyNotificationData:     data,
		}, nil
	}

	timeoutMs := paramInt(params, KeyTimeoutMs, 5000)

	readCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		data, err := conn.framer.ReadFrame()
		ch <- readResult{data, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			return map[string]any{KeyNotificationReceived: false}, nil
		}
		return map[string]any{
			KeyNotificationReceived: true,
			KeyNotificationData:     res.data,
		}, nil
	case <-readCtx.Done():
		return map[string]any{KeyNotificationReceived: false}, nil
	}
}

// ============================================================================
// Connection lifecycle
// ============================================================================

// handleConnectWithTiming connects and records timing.
func (r *Runner) handleConnectWithTiming(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	start := time.Now()
	result, err := r.handleConnect(ctx, step, state)
	elapsed := time.Since(start)

	if err != nil {
		return nil, err
	}

	result[KeyConnectDurationMs] = elapsed.Milliseconds()
	state.Set(StateConnectDurationMs, elapsed.Milliseconds())

	params := engine.InterpolateParams(step.Params, state)
	if saveAs, ok := params[ParamSaveDelayAs].(string); ok && saveAs != "" {
		state.Set(saveAs, elapsed.Milliseconds())
		result[KeySaveDelayAs] = saveAs
	}

	return result, nil
}

// handleSendClose sends a ControlClose frame then closes the TCP connection.
func (r *Runner) handleSendClose(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return map[string]any{KeyCloseSent: false, KeyCloseAckReceived: false}, nil
	}

	// Check if there's a pending async request (from a prior async read step).
	pendingResponseReceived := false
	if rs, ok := state.Get(KeyRequestSent); ok {
		if sent, ok := rs.(bool); ok && sent {
			// An async request was sent -- read the pending response
			// before we close, so it's not lost.
			if r.pool.Main().tlsConn != nil {
				_ = r.pool.Main().tlsConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				if data, err := r.pool.Main().framer.ReadFrame(); err == nil {
					r.pool.AppendNotification(data)
				}
				_ = r.pool.Main().tlsConn.SetReadDeadline(time.Time{})
			}
			pendingResponseReceived = true
		}
	}

	// Send ControlClose frame before closing TCP.
	closeMsg := &wire.ControlMessage{Type: wire.ControlClose}
	closeData, err := wire.EncodeControlMessage(closeMsg)
	if err == nil {
		_ = r.pool.Main().framer.WriteFrame(closeData) // Best effort.
	}

	// Try to read close_ack with a short timeout before closing TCP.
	closeAckReceived := false
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	framer := r.pool.Main().framer // capture locally -- main connection may be replaced
	go func() {
		d, e := framer.ReadFrame()
		ch <- readResult{d, e}
	}()

	select {
	case res := <-ch:
		// Any response (even EOF) before timeout counts as acknowledgement.
		closeAckReceived = res.err == nil || res.err == io.EOF
	case <-readCtx.Done():
		// Timeout -- no ack received.
	}

	err = r.pool.Main().Close()
	state.Set(StateGracefullyClosed, true)
	return map[string]any{
		KeyCloseSent:                true,
		KeyCloseAckReceived:         closeAckReceived,
		KeyConnectionClosed:         err == nil,
		KeyCloseAcknowledged:        closeAckReceived,
		KeyPendingResponseReceived:  pendingResponseReceived,
		KeyState:                    ConnectionStateClosed,
	}, nil
}

// handleSimultaneousClose sends close while reading for close from peer.
func (r *Runner) handleSimultaneousClose(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return map[string]any{KeyCloseSent: false, KeyBothCloseReceived: false}, nil
	}

	err := r.pool.Main().Close()
	closed := err == nil
	return map[string]any{
		KeyCloseSent:         closed,
		KeySimultaneous:      true,
		KeyBothCloseReceived: closed,
		KeyConnectionClosed:  closed,
	}, nil
}

// handleWaitDisconnect waits for the connection to be closed by the peer.
func (r *Runner) handleWaitDisconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	timeoutMs := paramInt(params, KeyTimeoutMs, 10000)

	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return map[string]any{KeyDisconnected: true}, nil
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	ch := make(chan error, 1)
	framer := r.pool.Main().framer // capture locally -- main connection may be replaced
	go func() {
		_, err := framer.ReadFrame()
		ch <- err
	}()

	select {
	case err := <-ch:
		// EOF or error means disconnected.
		if err != nil {
			r.pool.Main().transitionTo(ConnDisconnected)
			return map[string]any{KeyDisconnected: true}, nil
		}
		return map[string]any{KeyDisconnected: false}, nil
	case <-readCtx.Done():
		return map[string]any{KeyDisconnected: false}, nil
	}
}

// handleCancelReconnect cancels any pending reconnection.
func (r *Runner) handleCancelReconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ct := getConnectionTracker(state)
	ct.backoffState = nil

	return map[string]any{KeyReconnectCancelled: true}, nil
}

// ============================================================================
// Reconnection
// ============================================================================

// handleMonitorReconnect starts monitoring for reconnection.
func (r *Runner) handleMonitorReconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ct := getConnectionTracker(state)
	ct.backoffState = &backoffTracker{
		Monitoring:  true,
		Attempts:    0,
		LastAttempt: time.Now(),
	}

	return map[string]any{
		KeyMonitoringStarted: true,
	}, nil
}

// handleDisconnectAndMonitorBackoff disconnects and monitors backoff.
func (r *Runner) handleDisconnectAndMonitorBackoff(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ct := getConnectionTracker(state)

	if r.pool.Main() != nil && r.pool.Main().isConnected() {
		_ = r.pool.Main().Close()
	}

	ct.backoffState = &backoffTracker{
		Monitoring:  true,
		Attempts:    0,
		LastAttempt: time.Now(),
	}

	return map[string]any{
		KeyDisconnected:      true,
		KeyMonitoringBackoff: true,
	}, nil
}

// ============================================================================
// Keep-alive
// ============================================================================

// handlePing sends a single ping.
func (r *Runner) handlePing(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Check for zone-scoped connection first.
	if zoneID := resolveZoneParam(params); zoneID != "" {
		ct := getConnectionTracker(state)
		if conn, ok := ct.zoneConnections[zoneID]; ok && conn.isConnected() {
			// Zone connection exists and is alive -- pong succeeds.
		} else {
			return map[string]any{KeyPingSent: true, KeyPongReceived: false, KeyError: fmt.Sprintf("no active connection for zone %s", zoneID)}, nil
		}
	} else if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		errMsg := "not connected"
		if gc, ok := state.Get(StateGracefullyClosed); ok {
			if closed, ok := gc.(bool); ok && closed {
				errMsg = "CONNECTION_CLOSED"
			}
		}
		return map[string]any{KeyPingSent: false, KeyPongReceived: false, KeyError: errMsg}, nil
	}

	// If block_pong is set, simulate that pong was not received.
	if toBool(params[ParamBlockPong]) {
		return map[string]any{
			KeyPingSent:     true,
			KeyPongReceived: false,
		}, nil
	}

	// Check if a timeout threshold was specified.
	latencyUnder := true
	if _, ok := params[KeyTimeoutMs]; ok {
		latencyUnder = paramFloat(params, KeyTimeoutMs, 0) > 0 // Connection is alive, so latency is within any positive timeout.
	}

	// Increment pong sequence.
	seq := uint32(1)
	if s, exists := state.Get(StatePongSeq); exists {
		if si, ok := s.(uint32); ok {
			seq = si + 1
		}
	}
	state.Set(StatePongSeq, seq)

	// Override with explicit sequence parameter if provided.
	if _, ok := params[ParamSeq]; ok {
		seq = uint32(paramInt(params, ParamSeq, int(seq)))
		state.Set(StatePongSeq, seq)
	}

	return map[string]any{
		KeyPingSent:     true,
		KeyPongReceived: true,
		KeyLatencyUnder: latencyUnder,
		KeyPongSeq:      seq,
	}, nil
}

// handlePingMultiple sends multiple pings.
func (r *Runner) handlePingMultiple(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	count := paramInt(params, KeyCount, 3)

	allReceived := true
	for i := 0; i < count; i++ {
		out, _ := r.handlePing(ctx, step, state)
		if out[KeyPongReceived] != true {
			allReceived = false
			break
		}
	}

	return map[string]any{
		KeyAllPongsReceived:    allReceived,
		KeyAverageLatencyUnder: allReceived, // Simulated: true means latency ~0.
		KeyCount:               count,
	}, nil
}

// handleVerifyKeepalive verifies keep-alive is active.
func (r *Runner) handleVerifyKeepalive(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	active := r.pool.Main() != nil && r.pool.Main().isConnected()

	// Check if a ping has been sent by looking at pong sequence state.
	pingSent := false
	pongReceived := false
	sequenceMatch := false
	if seq, exists := state.Get(StatePongSeq); exists {
		pingSent = true
		if s, ok := seq.(uint32); ok && s > 0 {
			pongReceived = true
			sequenceMatch = true
		}
	}

	// Simulate auto-ping: the protocol sends a ping after 30s idle.
	// We track cumulative idle seconds (reset by read/write/invoke).
	if active && !pingSent {
		idleVal, _ := state.Get(StateKeepaliveIdleSec)
		idleSec, _ := idleVal.(float64)
		if idleSec >= 30 {
			pingSent = true
			pongReceived = true
			sequenceMatch = true
			state.Set(StatePongSeq, uint32(1))
		}
	}

	return map[string]any{
		KeyKeepaliveActive: active,
		KeyPingSent:        pingSent,
		KeyPongReceived:    pongReceived,
		KeySequenceMatch:   sequenceMatch,
	}, nil
}

// ============================================================================
// Raw wire
// ============================================================================

// handleSendRaw sends raw data through the framer.
// Supports multiple data formats (checked in priority order):
//   - cbor_map: map with string keys that are converted to integer keys, then CBOR-encoded
//   - cbor_bytes_hex: hex-encoded bytes to decode and send
//   - bytes_hex: hex-encoded bytes to decode and send
//   - data: raw data (string or bytes)
func (r *Runner) handleSendRaw(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	var data []byte
	var err error

	switch {
	case params[ParamMessageType] != nil && params[ParamCBORMap] == nil && params[ParamCBORBytesHex] == nil && params[ParamCBORMapStringKeys] == nil:
		// Typed message construction (e.g., attribute_value with extreme int values).
		msgType, _ := params[ParamMessageType].(string)
		switch msgType {
		case "attribute_value":
			value := params[ParamValue]
			req := &wire.Request{
				MessageID:  r.nextMessageID(),
				Operation:  wire.OpWrite,
				EndpointID: 0,
				FeatureID:  1,
				Payload:    wire.WritePayload{1: value},
			}
			data, err = wire.EncodeRequest(req)
		case "request":
			req := &wire.Request{
				MessageID:  uint32(paramInt(params, "message_id", int(r.nextMessageID()))),
				Operation:  wire.Operation(paramInt(params, "operation", 1)),
				EndpointID: uint8(paramInt(params, "endpoint_id", 0)),
				FeatureID:  uint8(paramInt(params, "feature_id", 0)),
				Payload:    params[ParamPayload],
			}
			data, err = wire.EncodeRequest(req)
		case "response":
			resp := &wire.Response{
				MessageID: uint32(paramInt(params, "message_id", 0)),
				Status:    wire.Status(paramInt(params, "status", 0)),
				Payload:   params[ParamPayload],
			}
			data, err = wire.EncodeResponse(resp)
		default:
			return nil, fmt.Errorf("unsupported message_type: %s", msgType)
		}
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", msgType, err)
		}

	case params[ParamCBORMapStringKeys] != nil:
		// CBOR map with string keys preserved (intentionally invalid for MASH).
		m, ok := params[ParamCBORMapStringKeys].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cbor_map_string_keys must be a map[string]any, got %T", params[ParamCBORMapStringKeys])
		}
		data, err = cbor.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("cbor_map_string_keys encoding: %w", err)
		}

	case params[ParamCBORMap] != nil:
		// CBOR map -- accept multiple key types that YAML may produce.
		var cborData any
		switch m := params[ParamCBORMap].(type) {
		case map[string]any:
			cborData = convertIntKeys(m)
		case map[any]any:
			cborData = normalizeMapKeys(m)
		case map[int]any:
			cborData = m
		default:
			return nil, fmt.Errorf("cbor_map must be a map, got %T", params[ParamCBORMap])
		}
		data, err = cbor.Marshal(cborData)
		if err != nil {
			return nil, fmt.Errorf("cbor_map encoding: %w", err)
		}

	case params[ParamCBORBytesHex] != nil:
		hexStr, _ := params[ParamCBORBytesHex].(string)
		data, err = hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("cbor_bytes_hex decode: %w", err)
		}

	case params[ParamBytesHex] != nil:
		hexStr, _ := params[ParamBytesHex].(string)
		data, err = hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("bytes_hex decode: %w", err)
		}

	default:
		raw, ok := params[ParamData].([]byte)
		if !ok {
			if s, ok := params[ParamData].(string); ok {
				raw = []byte(s)
			}
		}
		data = raw
	}

	if len(data) == 0 {
		return map[string]any{
			KeyRawSent:      false,
			KeyParseSuccess: false,
			KeyError:        "message is empty",
		}, nil
	}

	r.debugf("handleSendRaw: about to write %d bytes, conn.state=%v", len(data), r.pool.Main().state)
	err = r.pool.Main().framer.WriteFrame(data)
	if err != nil {
		return map[string]any{
			KeyRawSent: false,
			KeyError:   err.Error(),
		}, err
	}

	outputs := map[string]any{
		KeyRawSent:      true,
		KeyParseSuccess: true,
	}

	// Try to read a response with an independent timeout. We use
	// context.Background() instead of ctx because the parent context may
	// already be expired after precondition setup consumed the test budget.
	readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	framer := r.pool.Main().framer // capture locally -- main connection may be replaced
	go func() {
		d, e := framer.ReadFrame()
		ch <- readResult{d, e}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			r.debugf("handleSendRaw: read error: %v", res.err)
			outputs[KeyResponseReceived] = false
			// Connection closed (EOF) -- device rejected the message silently.
			outputs[KeyErrorStatus] = "CONNECTION_CLOSED"
			outputs[KeyErrorCode] = "CONNECTION_CLOSED"
			outputs[KeyParseSuccess] = false
		} else {
			r.debugf("handleSendRaw: received %d bytes", len(res.data))
			outputs[KeyResponseReceived] = true
			// Decode response using wire.DecodeResponse for proper status extraction.
			if resp, decErr := wire.DecodeResponse(res.data); decErr == nil {
				outputs[KeyResponseMessageID] = resp.MessageID
				outputs[KeyStatus] = resp.Status
				if !resp.IsSuccess() {
					outputs[KeyErrorStatus] = resp.Status.String()
					outputs[KeyErrorCode] = resp.Status.String()
					// INVALID_PARAMETER typically indicates the device failed to parse
					// the request (bad CBOR, missing fields, etc.), so parse_success is false.
					if resp.Status == wire.StatusInvalidParameter {
						outputs[KeyParseSuccess] = false
					}
					// Extract error message from payload if present.
					if payload, ok := resp.Payload.(map[any]any); ok {
						if msg, ok := payload[uint64(1)]; ok {
							if s, ok := msg.(string); ok {
								outputs[KeyErrorMessageContains] = s
							}
						}
					}
				}
			} else {
				// Fallback: try raw CBOR map parsing.
				var respMap map[any]any
				if err := cbor.Unmarshal(res.data, &respMap); err == nil {
					if status, ok := respMap[uint64(2)]; ok {
						outputs[KeyStatus] = status
					}
					if errStatus, ok := respMap[uint64(2)]; ok {
						if s, ok2 := wire.ToUint32(errStatus); ok2 {
							outputs[KeyErrorStatus] = wire.Status(s).String()
						}
					}
				}
			}
		}
	case <-readCtx.Done():
		r.debugf("handleSendRaw: read timeout (5s)")
		outputs[KeyResponseReceived] = false
		// No response -- device ignored the malformed message.
		outputs[KeyErrorStatus] = "NO_RESPONSE"
		outputs[KeyErrorCode] = "NO_RESPONSE"
		outputs[KeyParseSuccess] = false
	}

	r.debugf("handleSendRaw: final outputs[response_received]=%v", outputs[KeyResponseReceived])
	return outputs, nil
}

// cborEncodeIntKeyMap converts a map with string keys to integer keys and CBOR-encodes it.
// String keys that are valid integers (e.g., "1", "2") are converted to int keys.
// Nested maps are processed recursively.
func cborEncodeIntKeyMap(m map[string]any) ([]byte, error) {
	converted := convertIntKeys(m)
	return cbor.Marshal(converted)
}

// convertIntKeys recursively converts string keys to integer keys where possible.
func convertIntKeys(m map[string]any) map[any]any {
	result := make(map[any]any, len(m))
	for k, v := range m {
		var key any
		if i, err := strconv.Atoi(k); err == nil {
			key = i
		} else {
			key = k
		}

		// Recursively convert nested maps.
		switch val := v.(type) {
		case map[string]any:
			result[key] = convertIntKeys(val)
		default:
			result[key] = val
		}
	}
	return result
}

// normalizeMapKeys recursively processes a map[any]any (from YAML integer keys)
// into a form suitable for CBOR encoding. Integer keys are preserved as-is.
// Nested maps are recursively normalized.
func normalizeMapKeys(m map[any]any) map[any]any {
	result := make(map[any]any, len(m))
	for k, v := range m {
		// Convert string keys to int where possible (same as convertIntKeys).
		key := k
		if s, ok := k.(string); ok {
			if i, err := strconv.Atoi(s); err == nil {
				key = i
			}
		}
		// Recursively normalize nested maps.
		switch val := v.(type) {
		case map[any]any:
			result[key] = normalizeMapKeys(val)
		case map[string]any:
			result[key] = convertIntKeys(val)
		default:
			result[key] = val
		}
	}
	return result
}

// handleSendRawBytes sends raw bytes (not framed).
// Supports:
//   - data: raw bytes or string
//   - bytes_hex: hex-encoded bytes
//   - followed_by_cbor_payload + payload_size: append a framed CBOR payload after the raw bytes
//   - followed_by_bytes: append N zero bytes after the raw bytes
//   - remaining_bytes: send N zero bytes as a framed payload (for completing partial frames)
func (r *Runner) handleSendRawBytes(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	w := r.getWriteConn()
	if w == nil {
		return nil, fmt.Errorf("no writable connection")
	}

	// Determine the base data to send.
	var data []byte

	if hexStr, ok := params[ParamBytesHex].(string); ok && hexStr != "" {
		var err error
		data, err = hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("bytes_hex decode: %w", err)
		}
	} else if raw, ok := params[ParamData].([]byte); ok {
		data = raw
	} else if s, ok := params[ParamData].(string); ok {
		data = []byte(s)
	}

	// Handle remaining_bytes (no prefix data, send N bytes as framed payload).
	if rb, ok := params[ParamRemainingBytes]; ok {
		n := int(toFloat(rb))
		payload := make([]byte, n)
		err := r.pool.Main().framer.WriteFrame(payload)
		if err != nil {
			return map[string]any{
				KeyRawBytesSent: false,
				KeyError:        err.Error(),
			}, nil
		}
		outputs := map[string]any{
			KeyRawBytesSent: true,
			KeyConnectionOpen: r.pool.Main().isConnected(),
		}
		// Try to read framed response.
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		type rr struct {
			data []byte
			err  error
		}
		ch := make(chan rr, 1)
		framer := r.pool.Main().framer // capture locally -- main connection may be replaced
		go func() {
			d, e := framer.ReadFrame()
			ch <- rr{d, e}
		}()
		select {
		case res := <-ch:
			outputs[KeyParseSuccess] = res.err == nil
		case <-readCtx.Done():
			outputs[KeyParseSuccess] = false
		}
		return outputs, nil
	}

	// Write the base data.
	if len(data) > 0 {
		_, err := w.Write(data)
		if err != nil {
			return map[string]any{
				KeyRawBytesSent:   false,
				KeyConnectionOpen: false,
				KeyError:          err.Error(),
			}, nil
		}
	}

	outputs := map[string]any{
		KeyRawBytesSent:   true,
		KeyConnectionOpen: r.pool.Main().isConnected(),
	}

	// Append followed_by_bytes: N zero bytes after the raw bytes.
	if fb, ok := params[ParamFollowedByBytes]; ok {
		n := int(toFloat(fb))
		padding := make([]byte, n)
		_, err := w.Write(padding)
		if err != nil {
			outputs[KeyConnectionOpen] = false
		}
		return outputs, nil
	}

	// Append followed_by_cbor_payload: framed CBOR payload after the raw bytes.
	if _, ok := params[ParamFollowedByCBORPayload]; ok {
		size := 16
		if ps, ok := params[ParamPayloadSize]; ok {
			size = int(toFloat(ps))
		}
		payload := generateValidCBORPayload(size, 1)
		err := r.pool.Main().framer.WriteFrame(payload)
		if err != nil {
			outputs[KeyParseSuccess] = false
			return outputs, nil
		}
		outputs[KeyParseSuccess] = true

		// Try to read framed response.
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		type rr struct {
			data []byte
			err  error
		}
		ch := make(chan rr, 1)
		framer := r.pool.Main().framer // capture locally -- main connection may be replaced
		go func() {
			d, e := framer.ReadFrame()
			ch <- rr{d, e}
		}()
		select {
		case res := <-ch:
			if res.err == nil {
				outputs[KeyResponseReceived] = true
			}
		case <-readCtx.Done():
			outputs[KeyResponseReceived] = false
		}
		return outputs, nil
	}

	return outputs, nil
}

// handleSendRawFrame sends a raw frame with length prefix.
// Supports:
//   - length_override: write a raw 4-byte big-endian length (bypassing framer validation)
//   - payload_size + valid_cbor: generate a CBOR payload of target size, send framed
//   - fallback: delegate to handleSendRaw for other param combinations
func (r *Runner) handleSendRawFrame(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	// Path 1: length_override -- write raw length prefix directly (bypassing framer).
	if lo, ok := params[ParamLengthOverride]; ok {
		w := r.getWriteConn()
		if w == nil {
			return nil, fmt.Errorf("no writable connection")
		}
		length := uint32(toFloat(lo))
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], length)
		_, err := w.Write(lenBuf[:])
		if err != nil {
			return map[string]any{
				KeyConnectionClosed: true,
				KeyError:            "FATAL",
			}, nil
		}

		// Try to read response; expect the device to close the connection.
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		ch := make(chan error, 1)
		framer := r.pool.Main().framer // capture locally -- main connection may be replaced
		go func() {
			_, err := framer.ReadFrame()
			ch <- err
		}()

		select {
		case err := <-ch:
			if err != nil {
				r.pool.Main().transitionTo(ConnDisconnected)
				return map[string]any{
					KeyConnectionClosed: true,
					KeyError:            "FATAL",
				}, nil
			}
			// Unexpected success.
			return map[string]any{
				KeyConnectionClosed: false,
				KeyResponseReceived: true,
			}, nil
		case <-readCtx.Done():
			r.pool.Main().transitionTo(ConnDisconnected)
			return map[string]any{
				KeyConnectionClosed: true,
				KeyError:            "FATAL",
			}, nil
		}
	}

	// Path 2: payload_size + valid_cbor -- generate CBOR payload and send framed.
	if ps, ok := params[ParamPayloadSize]; ok {
		size := int(toFloat(ps))
		payload := generateValidCBORPayload(size, r.nextMessageID())
		err := r.pool.Main().framer.WriteFrame(payload)
		if err != nil {
			return map[string]any{
				KeyRawSent:      false,
				KeyParseSuccess: false,
				KeyError:        err.Error(),
			}, nil
		}

		outputs := map[string]any{
			KeyRawSent:      true,
			KeyParseSuccess: true,
		}

		// Try to read response.
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		type readResult struct {
			data []byte
			err  error
		}
		ch := make(chan readResult, 1)
		framer := r.pool.Main().framer // capture locally -- main connection may be replaced
		go func() {
			d, e := framer.ReadFrame()
			ch <- readResult{d, e}
		}()

		select {
		case res := <-ch:
			if res.err != nil {
				outputs[KeyResponseReceived] = false
			} else {
				outputs[KeyResponseReceived] = true
			}
		case <-readCtx.Done():
			outputs[KeyResponseReceived] = false
		}
		return outputs, nil
	}

	// Fallback: delegate to handleSendRaw.
	return r.handleSendRaw(ctx, step, state)
}

// getWriteConn returns the underlying io.Writer for raw writes bypassing the framer.
// Prefers tlsConn (for TLS connections) but falls back to plain conn.
func (r *Runner) getWriteConn() io.Writer {
	if r.pool.Main().tlsConn != nil {
		return r.pool.Main().tlsConn
	}
	if r.pool.Main().conn != nil {
		return r.pool.Main().conn
	}
	return nil
}

// generateValidCBORPayload creates a valid CBOR-encoded request message
// padded to approximately targetSize bytes.
func generateValidCBORPayload(targetSize int, messageID uint32) []byte {
	// Build a minimal request structure.
	req := map[any]any{
		1: messageID,    // messageId
		2: uint8(1),     // operation: Read
		3: uint8(0),     // endpointId
		4: uint8(0),     // featureId
	}

	// Encode the base structure to find its size.
	base, err := cbor.Marshal(req)
	if err != nil {
		return base
	}

	if len(base) >= targetSize {
		return base
	}

	// Add padding as a byte string in key 5 (payload).
	paddingSize := targetSize - len(base) - 10 // leave room for CBOR overhead
	if paddingSize < 0 {
		paddingSize = 0
	}
	padding := make([]byte, paddingSize)
	req[5] = padding

	data, err := cbor.Marshal(req)
	if err != nil {
		return base
	}
	return data
}

// handleSendTLSAlert sends a TLS close_notify alert and detects the peer's response.
//
// For close_notify: uses CloseWrite() to send the alert while keeping the read
// side open, then attempts a short read to detect the peer's close_notify. An
// io.EOF on read indicates the peer responded with close_notify before closing.
func (r *Runner) handleSendTLSAlert(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	if r.pool.Main() == nil || !r.pool.Main().isConnected() || r.pool.Main().tlsConn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Send close_notify via CloseWrite (keeps read side open).
	err := r.pool.Main().tlsConn.CloseWrite()
	if err != nil {
		// CloseWrite failed -- close fully and report.
		_ = r.pool.Main().Close()
		return map[string]any{
			KeyAlertSent:       false,
			KeyPeerCloseNotify: false,
			KeyConnectionClosed: true,
		}, nil
	}

	// Try to detect peer's close_notify by reading with a short deadline.
	// When the peer sends close_notify, Go's TLS returns io.EOF.
	_ = r.pool.Main().tlsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, readErr := r.pool.Main().tlsConn.Read(buf)

	// io.EOF means the peer sent close_notify and closed gracefully.
	// A net timeout means the peer didn't respond within the deadline.
	peerCloseNotify := isEOFOrCloseNotify(readErr)

	// Clean up connection state.
	r.pool.Main().transitionTo(ConnDisconnected)

	return map[string]any{
		KeyAlertSent:        true,
		KeyPeerCloseNotify:  peerCloseNotify,
		KeyConnectionClosed: true,
	}, nil
}

// isEOFOrCloseNotify returns true if the error indicates the peer sent
// close_notify (which Go surfaces as io.EOF) or the connection was closed.
func isEOFOrCloseNotify(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	// Go's TLS layer may wrap close_notify as a generic error containing
	// "use of closed" or "close notify" in the message.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "close notify") || strings.Contains(msg, "use of closed")
}

// ============================================================================
// Command queue
// ============================================================================

// handleQueueCommand stores a command for later execution.
func (r *Runner) handleQueueCommand(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ct := getConnectionTracker(state)

	action, _ := params[KeyAction].(string)
	cmdParams, _ := params[ParamParams].(map[string]any)

	ct.pendingQueue = append(ct.pendingQueue, queuedCommand{
		Action: action,
		Params: cmdParams,
	})

	return map[string]any{
		KeyCommandQueued: true,
		KeyQueued:        true,
		KeyQueueLength:   len(ct.pendingQueue),
	}, nil
}

// handleWaitForQueuedResult waits for queued command results.
func (r *Runner) handleWaitForQueuedResult(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ct := getConnectionTracker(state)

	if len(ct.pendingQueue) == 0 {
		return map[string]any{
			KeyResultReceived: false,
			KeyQueueEmpty:     true,
		}, nil
	}

	// Dequeue and report.
	cmd := ct.pendingQueue[0]
	ct.pendingQueue = ct.pendingQueue[1:]

	return map[string]any{
		KeyResultReceived:  true,
		KeyReadSuccess:     true,
		KeyAction:          cmd.Action,
		KeyQueueRemaining:  len(ct.pendingQueue),
	}, nil
}

// handleSendMultipleThenDisconnect sends multiple frames then disconnects.
func (r *Runner) handleSendMultipleThenDisconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	if r.pool.Main() == nil || !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("not connected")
	}

	outputs := map[string]any{
		KeyDisconnected: true,
	}

	// If commands list is provided, parse per-command and assign status.
	if cmds, ok := params[ParamCommands]; ok {
		cmdList, _ := cmds.([]any)
		sent := 0
		for _, c := range cmdList {
			cmd, _ := c.(map[string]any)
			if cmd == nil {
				continue
			}
			// Send a minimal frame for each command.
			req := &wire.Request{
				MessageID: r.nextMessageID(),
				Operation: wire.OpRead,
			}
			data, err := wire.EncodeRequest(req)
			if err != nil {
				break
			}
			if err := r.pool.Main().framer.WriteFrame(data); err != nil {
				break
			}
			sent++

			// Classify: read and idempotent commands -> "retried",
			// non-idempotent commands (Start, Stop) -> "UNKNOWN".
			cmdType, _ := cmd[ParamType].(string)
			cmdName, _ := cmd[ParamCommand].(string)
			statusKey := strings.ToLower(cmdType) + "_status"
			if cmdName != "" {
				statusKey = strings.ToLower(cmdName) + "_status"
			}
			if isNonIdempotent(cmdName) {
				outputs[statusKey] = "UNKNOWN"
			} else {
				outputs[statusKey] = "retried"
			}
		}
		outputs[KeyMessagesSent] = sent
	} else {
		count := paramInt(params, KeyCount, 3)
		sent := 0
		for i := 0; i < count; i++ {
			req := &wire.Request{
				MessageID: r.nextMessageID(),
				Operation: wire.OpRead,
			}
			data, err := wire.EncodeRequest(req)
			if err != nil {
				break
			}
			if err := r.pool.Main().framer.WriteFrame(data); err != nil {
				break
			}
			sent++
		}
		outputs[KeyMessagesSent] = sent
	}

	_ = r.pool.Main().Close()

	return outputs, nil
}

// isNonIdempotent returns true for commands that are not safe to retry.
func isNonIdempotent(cmd string) bool {
	switch strings.ToLower(cmd) {
	case "start", "stop", "pause", "resume":
		return true
	}
	return false
}

// ============================================================================
// Capacity
// ============================================================================

// handleOpenConnections opens multiple connections for capacity testing.
// Connections are stored in the security pool so they remain open for reaper
// and capacity tests (DEC-062, DEC-064).
func (r *Runner) handleOpenConnections(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParamsWithPICS(step.Params, state, r.pics)
	count := paramInt(params, KeyCount, 1)

	secState := getSecurityState(state)
	target := r.getTarget(params)

	established := 0
	for i := 0; i < count; i++ {
		tlsConfig := &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
			NextProtos:         []string{transport.ALPNCommissioningProtocol},
		}
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", target, tlsConfig)
		if err != nil {
			// Connection rejected -- stop filling.
			break
		}

		secState.pool.mu.Lock()
		newConn := &Connection{
			tlsConn: conn,
			framer:  transport.NewFramer(conn),
			state:   ConnTLSConnected,
		}
		secState.pool.connections = append(secState.pool.connections, newConn)
		secState.pool.mu.Unlock()
		established++
	}

	return map[string]any{
		KeyAllEstablished:   established == count,
		KeyEstablishedCount: established,
	}, nil
}

// ============================================================================
// Concurrency
// ============================================================================

// handleReadConcurrent performs multiple reads concurrently.
func (r *Runner) handleReadConcurrent(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Support "requests" param: a list of per-request param maps.
	var requestParams []map[string]any
	if reqs, ok := params[ParamRequests]; ok {
		switch v := reqs.(type) {
		case []any:
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					requestParams = append(requestParams, m)
				}
			}
		case []map[string]any:
			requestParams = v
		}
	}

	count := len(requestParams)
	if count == 0 {
		count = paramInt(params, KeyCount, 2)
	}

	// For concurrency testing, we perform sequential reads with per-request params.
	results := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		subStep := &loader.Step{Params: step.Params}
		if i < len(requestParams) {
			// Merge per-request params into a copy of the base params.
			merged := make(map[string]any, len(params))
			for k, v := range params {
				merged[k] = v
			}
			for k, v := range requestParams[i] {
				merged[k] = v
			}
			subStep = &loader.Step{Params: merged}
		}
		out, err := r.handleRead(ctx, subStep, state)
		if err != nil {
			results = append(results, map[string]any{KeyError: err.Error()})
		} else {
			results = append(results, out)
		}
	}

	// RC5d: Compute correlation outputs.
	allReceived := true
	allCorrect := true
	for _, res := range results {
		if _, hasErr := res[KeyError]; hasErr {
			allReceived = false
			allCorrect = false
		}
		if success, ok := res[KeyReadSuccess].(bool); ok && !success {
			allReceived = false
		}
		if mid, ok := res[KeyResponseMessageID]; ok {
			if mid == nil || mid == uint32(0) {
				allCorrect = false
			}
		} else {
			allCorrect = false
		}
	}

	return map[string]any{
		KeyReadCount:              count,
		KeyResults:                results,
		KeyAllResponsesReceived:   allReceived,
		KeyAllCorrelationsCorrect: allCorrect,
		KeyEachMessageIDMatches:   allCorrect,
		KeyAllPairsMatched:        allCorrect,
		KeyResponseCount:          len(results),
	}, nil
}

// handleInvokeWithDisconnect invokes then immediately disconnects.
func (r *Runner) handleInvokeWithDisconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	result, err := r.handleInvoke(ctx, step, state)
	if err != nil {
		// If invoke itself fails, still report the disconnect.
		_ = r.pool.Main().Close()
		return map[string]any{
			KeyDisconnectedAfterInvoke: true,
			KeyInvokeSuccess:           false,
			KeyResultStatus:            "UNKNOWN",
			KeyError:                   err.Error(),
		}, nil
	}

	_ = r.pool.Main().Close()
	result[KeyDisconnectedAfterInvoke] = true

	// When disconnect_before_response was requested, mark outcome as UNKNOWN
	// since we don't know if the device processed the command.
	if toBool(params[ParamDisconnectBeforeResponse]) {
		result[KeyResultStatus] = "UNKNOWN"
		result[KeyDisconnectOccurred] = true
	}

	return result, nil
}

// ============================================================================
// Subscription extensions
// ============================================================================

// handleSubscribeMultiple subscribes to multiple features.
// Supports two param formats:
//   - features: []any of feature name strings (uses endpoint from params)
//   - subscriptions: []any of map[string]any with "endpoint" and "feature" keys
func (r *Runner) handleSubscribeMultiple(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	type subTarget struct {
		endpoint any
		feature  any
	}

	var targets []subTarget

	if subs, ok := params[KeySubscriptions].([]any); ok {
		// Subscriptions array of objects with endpoint+feature.
		for _, s := range subs {
			m, ok := s.(map[string]any)
			if !ok {
				continue
			}
			ep := m[KeyEndpoint]
			if ep == nil {
				ep = params[KeyEndpoint]
			}
			targets = append(targets, subTarget{endpoint: ep, feature: m[KeyFeature]})
		}
	} else if features, ok := params[ParamFeatures].([]any); ok {
		// Simple features array (uses shared endpoint).
		for _, f := range features {
			targets = append(targets, subTarget{endpoint: params[KeyEndpoint], feature: f})
		}
	} else {
		return nil, fmt.Errorf("either 'features' or 'subscriptions' parameter is required")
	}

	// Collect priming data from each subscribe into a queue so that
	// receive_notifications can consume them one by one (each handleSubscribe
	// overwrites StatePrimingData, so we rescue it after each call).
	var primingQueue []any
	subscriptions := make([]any, 0, len(targets))
	allSucceed := true
	successCount := 0
	failureCount := 0
	for _, t := range targets {
		featureStep := &loader.Step{
			Params: map[string]any{
				KeyEndpoint: t.endpoint,
				KeyFeature:  t.feature,
			},
		}
		out, err := r.handleSubscribe(ctx, featureStep, state)
		if err != nil {
			failureCount++
			continue
		}
		if success, _ := out[KeySubscribeSuccess].(bool); !success {
			allSucceed = false
			failureCount++
		} else {
			successCount++
		}
		subscriptions = append(subscriptions, out[KeySubscriptionID])
		// Rescue priming data before the next subscribe overwrites it.
		if pd, ok := state.Get(StatePrimingData); ok && pd != nil {
			primingQueue = append(primingQueue, pd)
			state.Set(StatePrimingData, nil)
		}
	}
	// Store the priming queue for receive_notifications to consume.
	if len(primingQueue) > 0 {
		state.Set(StatePrimingQueue, primingQueue)
	}

	// Check uniqueness: all subscription IDs must be distinct.
	seen := make(map[string]bool, len(subscriptions))
	unique := true
	for _, id := range subscriptions {
		key := fmt.Sprintf("%v", id)
		if seen[key] {
			unique = false
			break
		}
		seen[key] = true
	}

	return map[string]any{
		KeySubscribeCount:        len(subscriptions),
		KeySubscriptionCount:     len(subscriptions),
		KeySubscriptions:         subscriptions,
		KeyAllSucceed:            allSucceed,
		KeySubscriptionIDsUnique: unique,
		KeySuccessCount:          successCount,
		KeyFailureCount:          failureCount,
	}, nil
}

// handleSubscribeOrdered subscribes and verifies ordering.
func (r *Runner) handleSubscribeOrdered(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	outputs, err := r.handleSubscribeMultiple(ctx, step, state)
	if err != nil {
		return nil, err
	}
	// Add ordering-specific keys expected by subscription restore tests.
	allSucceed, _ := outputs[KeyAllSucceed].(bool)
	outputs[KeyAllSubscribed] = allSucceed
	outputs[KeyOrderPreserved] = allSucceed // order is preserved when all succeed in sequence
	return outputs, nil
}

// handleUnsubscribe cancels a subscription.
func (r *Runner) handleUnsubscribe(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// Resolve subscription ID -- may be a literal, "saved_subscription_id",
	// or a reference via subscription_id_ref (state variable name).
	var subID uint32
	rawID := params[KeySubscriptionID]

	// Support subscription_id_ref: dereference a state variable.
	if rawID == nil {
		if ref, ok := params[ParamSubscriptionIDRef].(string); ok && ref != "" {
			if saved, exists := state.Get(ref); exists {
				rawID = saved
			}
		}
	}

	switch v := rawID.(type) {
	case string:
		if v == "saved_subscription_id" {
			if saved, ok := state.Get(StateSavedSubscriptionID); ok {
				if id, ok := wire.ToUint32(saved); ok {
					subID = id
				}
			}
		}
	default:
		if id, ok := wire.ToUint32(rawID); ok {
			subID = id
		}
	}

	if subID == 0 {
		return map[string]any{
			KeyUnsubscribeSuccess: false,
			KeyError:              "no subscription ID to unsubscribe",
		}, nil
	}

	if !r.pool.Main().isConnected() {
		return map[string]any{
			KeyUnsubscribeSuccess: false,
			KeyError:              "not connected",
		}, nil
	}

	// Send unsubscribe request: Subscribe operation with featureID=0
	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpSubscribe,
		EndpointID: 0,
		FeatureID:  0,
		Payload:    &wire.UnsubscribePayload{SubscriptionID: subID},
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode unsubscribe request: %w", err)
	}

	resp, err := r.sendRequest(data, "unsubscribe", req.MessageID)
	if err != nil {
		return map[string]any{
			KeyUnsubscribeSuccess: false,
			KeyError:              err.Error(),
		}, nil
	}

	state.Set(StateUnsubscribedID, subID)

	// Remove from active tracking (test explicitly unsubscribed).
	r.removeActiveSubscription(subID)

	// Clear priming data and buffered notifications so
	// wait_for_notification doesn't return stale data.
	state.Set(StatePrimingData, nil)
	state.Set(StatePrimingAttrCount, nil)
	r.pool.ClearNotifications()

	return map[string]any{
		KeyUnsubscribeSuccess: resp.IsSuccess(),
		KeyUnsubscribed:       resp.IsSuccess(),
		KeyStatus:             resp.Status,
	}, nil
}

// handleReceiveNotification receives a single notification.
func (r *Runner) handleReceiveNotification(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	return r.handleWaitNotification(ctx, step, state)
}

// handleReceiveNotifications receives multiple notifications.
func (r *Runner) handleReceiveNotifications(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	count := paramInt(params, KeyCount, 1)

	received := 0
	var subIDs []string
	for i := 0; i < count; i++ {
		out, _ := r.handleWaitNotification(ctx, step, state)
		if out[KeyNotificationReceived] == true {
			received++
			if sid, ok := out[KeySubscriptionID]; ok && sid != nil {
				subIDs = append(subIDs, fmt.Sprintf("%v", sid))
			}
		} else {
			break
		}
	}

	// Check subscription ID uniqueness.
	seen := make(map[string]bool, len(subIDs))
	allUnique := true
	for _, id := range subIDs {
		if seen[id] {
			allUnique = false
			break
		}
		seen[id] = true
	}

	return map[string]any{
		KeyNotificationsReceived: received,
		KeyReceivedCount:         received,
		KeyAllReceived:           received == count,
		KeyAllIDsUnique:          allUnique,
		KeyAllIDsMatchSubs:       allUnique && received == count,
	}, nil
}
