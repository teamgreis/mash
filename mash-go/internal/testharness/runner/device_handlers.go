package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// registerDeviceHandlers registers all device state action handlers.
func (r *Runner) registerDeviceHandlers() {
	r.engine.RegisterHandler(ActionDeviceLocalAction, r.handleDeviceLocalAction)
	r.engine.RegisterHandler(ActionDeviceSetValue, r.handleDeviceSetValue)
	r.engine.RegisterHandler(ActionDeviceSetValuesRapid, r.handleDeviceSetValuesRapid)
	r.engine.RegisterHandler(ActionDeviceTrigger, r.handleDeviceTrigger)
	r.engine.RegisterHandler(ActionConfigureDevice, r.handleConfigureDevice)
	r.engine.RegisterHandler(ActionConfigureExposedDevice, r.handleConfigureExposedDevice)
	r.engine.RegisterHandler(ActionUpdateExposedAttribute, r.handleUpdateExposedAttribute)
	r.engine.RegisterHandler(ActionChangeState, r.handleChangeState)
	r.engine.RegisterHandler(ActionSetStateDetail, r.handleSetStateDetail)
	r.engine.RegisterHandler(ActionTriggerFault, r.handleTriggerFault)
	r.engine.RegisterHandler(ActionClearFault, r.handleClearFault)
	r.engine.RegisterHandler(ActionQueryDeviceState, r.handleQueryDeviceState)
	r.engine.RegisterHandler(ActionVerifyDeviceState, r.handleVerifyDeviceState)
	r.engine.RegisterHandler(ActionSetConnected, r.handleSetConnected)
	r.engine.RegisterHandler(ActionSetDisconnected, r.handleSetDisconnected)
	r.engine.RegisterHandler(ActionSetFailsafeLimit, r.handleSetFailsafeLimit)
	r.engine.RegisterHandler(ActionMakeProcessAvailable, r.handleMakeProcessAvailable)
	r.engine.RegisterHandler(ActionStartOperation, r.handleStartOperation)
	r.engine.RegisterHandler(ActionEVConnect, r.handleEVConnect)
	r.engine.RegisterHandler(ActionEVDisconnect, r.handleEVDisconnect)
	r.engine.RegisterHandler(ActionEVRequestsCharge, r.handleEVRequestsCharge)
	r.engine.RegisterHandler(ActionPlugInCable, r.handlePlugInCable)
	r.engine.RegisterHandler(ActionUnplugCable, r.handleUnplugCable)
	r.engine.RegisterHandler(ActionUserOverride, r.handleUserOverride)
	r.engine.RegisterHandler(ActionFactoryReset, r.handleFactoryReset)
	r.engine.RegisterHandler(ActionPowerCycle, r.handlePowerCycle)
	r.engine.RegisterHandler(ActionPowerOnDevice, r.handlePowerOnDevice)
	r.engine.RegisterHandler(ActionReboot, r.handleReboot)
	r.engine.RegisterHandler(ActionRestart, r.handleRestart)
}

// handleDeviceLocalAction dispatches to sub-actions based on sub_action param.
func (r *Runner) handleDeviceLocalAction(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	subAction, _ := params[ParamSubAction].(string)
	if subAction == "" {
		// Try action field.
		subAction, _ = params[KeyAction].(string)
	}

	// Create a new step with the same params for dispatch.
	subStep := &loader.Step{
		Params: params,
	}

	var result map[string]any
	var err error

	switch subAction {
	case "change_state":
		result, err = r.handleChangeState(ctx, subStep, state)
	case "trigger_fault":
		result, err = r.handleTriggerFault(ctx, subStep, state)
	case "clear_fault":
		result, err = r.handleClearFault(ctx, subStep, state)
	case "set_connected":
		result, err = r.handleSetConnected(ctx, subStep, state)
	case "set_disconnected":
		result, err = r.handleSetDisconnected(ctx, subStep, state)
	case "ev_connect":
		result, err = r.handleEVConnect(ctx, subStep, state)
	case "ev_disconnect":
		result, err = r.handleEVDisconnect(ctx, subStep, state)
	case "ev_requests_charge":
		result, err = r.handleEVRequestsCharge(ctx, subStep, state)
	case "plug_in_cable":
		result, err = r.handlePlugInCable(ctx, subStep, state)
	case "unplug_cable":
		result, err = r.handleUnplugCable(ctx, subStep, state)
	case "make_process_available":
		result, err = r.handleMakeProcessAvailable(ctx, subStep, state)
	case "start_operation":
		result, err = r.handleStartOperation(ctx, subStep, state)
	case "set_failsafe_limit":
		result, err = r.handleSetFailsafeLimit(ctx, subStep, state)
	case "user_override":
		result, err = r.handleUserOverride(ctx, subStep, state)
	case "factory_reset":
		result, err = r.handleFactoryReset(ctx, subStep, state)
	case "power_cycle":
		result, err = r.handlePowerCycle(ctx, subStep, state)
	case "reboot":
		result, err = r.handleReboot(ctx, subStep, state)
	case "restart":
		result, err = r.handleRestart(ctx, subStep, state)
	case "set_state_detail":
		result, err = r.handleSetStateDetail(ctx, subStep, state)
	case "configure_device":
		result, err = r.handleConfigureDevice(ctx, subStep, state)
	case "set_value":
		result, err = r.handleDeviceSetValue(ctx, subStep, state)

	// Zone management sub-actions.
	case "add_zone":
		result, err = r.handleAddZone(ctx, subStep, state)
	case "remove_zone":
		result, err = r.handleRemoveZone(ctx, subStep, state)
	case "has_zone":
		result, err = r.handleHasZone(ctx, subStep, state)
	case "list_zones":
		result, err = r.handleListZones(ctx, subStep, state)
	case "zone_count":
		result, err = r.handleZoneCount(ctx, subStep, state)
	case "get_zone":
		result, err = r.handleGetZone(ctx, subStep, state)
	case "highest_priority_zone":
		result, err = r.handleHighestPriorityZone(ctx, subStep, state)
	case "highest_priority_connected_zone":
		result, err = r.handleHighestPriorityConnectedZone(ctx, subStep, state)

	// Network simulation sub-actions.
	case "interface_down":
		result, err = r.handleInterfaceDown(ctx, subStep, state)
	case "interface_up":
		result, err = r.handleInterfaceUp(ctx, subStep, state)
	case "interface_flap":
		result, err = r.handleInterfaceFlap(ctx, subStep, state)
	case "change_address":
		result, err = r.handleChangeAddress(ctx, subStep, state)
	case "adjust_clock":
		result, err = r.handleAdjustClock(ctx, subStep, state)

	// Discovery sub-actions.
	case "get_qr_payload", "get_qr":
		result, err = r.handleGetQRPayload(ctx, subStep, state)
		// Enrich with format validation fields.
		if result != nil {
			payload, _ := result[KeyQRPayload].(string)
			result[KeyQRPresent] = payload != ""
			result[KeyFormatValid] = len(payload) > 0
			if disc, ok := result[KeyDiscriminator].(int); ok {
				result[KeyDiscriminatorLength] = len(fmt.Sprintf("%d", disc))
			}
			if sc, ok := result[KeySetupCode].(string); ok {
				result[KeySetupCodeLength] = len(sc)
			}
		}
	case "check_display":
		// Check if the device has a QR display.
		result, err = r.handleGetQRPayload(ctx, subStep, state)
		if result != nil {
			payload, _ := result[KeyQRPayload].(string)
			result[KeyQRPresent] = payload != ""
			result[KeyFormatValid] = len(payload) > 0
			result[KeyQRDisplayed] = payload != ""
			if disc, ok := result[KeyDiscriminator].(int); ok {
				result[KeyDiscriminatorLength] = len(fmt.Sprintf("%d", disc))
			}
		}
	case "enter_commissioning_mode":
		result, err = r.handleEnterCommissioningMode(ctx, subStep, state)
	default:
		return nil, fmt.Errorf("unknown device_local_action sub_action: %s", subAction)
	}

	// Mark successful dispatches so tests can verify the action was triggered.
	if err == nil && result != nil {
		result[KeyActionTriggered] = true
	}
	return result, err
}

// handleDeviceSetValue sets an attribute value on the device.
func (r *Runner) handleDeviceSetValue(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	// If attribute+feature+endpoint are provided, invoke TriggerTestEvent on the
	// device's TestControl feature to change the value.
	attribute, _ := params[ParamAttribute].(string)
	featureRaw := params[KeyFeature]
	value := params[KeyValue]

	if attribute != "" && featureRaw != nil {
		// Resolve the trigger code for this attribute+value combination.
		trigger, err := r.resolveTriggerForSetValue(featureRaw, attribute, value)
		r.debugf("device_set_value: feature=%v attr=%s value=%v trigger=0x%016x err=%v", featureRaw, attribute, value, trigger, err)
		if err != nil {
			// Fall back to local state only.
			r.debugf("device_set_value: no trigger for %s.%s=%v: %v", featureRaw, attribute, value, err)
			ds.attributes[attribute] = value
			state.Set(attribute, value)
			return map[string]any{KeyValueSet: true, KeyKey: attribute}, nil
		}

		// Invoke TriggerTestEvent on the device.
		result, err := r.invokeTriggerTestEvent(ctx, state, trigger)
		if err != nil {
			return map[string]any{
				KeyValueSet: false,
				KeyError:    err.Error(),
			}, nil
		}
		// Also store locally.
		ds.attributes[attribute] = value
		state.Set(attribute, value)
		return map[string]any{
			KeyValueSet:    true,
			KeyKey:         attribute,
			KeyTriggerSent: true,
			KeyStatus:      result,
		}, nil
	}

	// Legacy: store key/value locally only.
	key, _ := params[KeyKey].(string)
	if key != "" {
		ds.attributes[key] = value
		state.Set(key, value)
	}

	return map[string]any{
		KeyValueSet: true,
		KeyKey:      key,
	}, nil
}

// resolveTriggerForSetValue maps feature+attribute+value to a TriggerTestEvent code.
func (r *Runner) resolveTriggerForSetValue(featureRaw any, attribute string, value any) (uint64, error) {
	featureName := fmt.Sprintf("%v", featureRaw)
	attrLower := strings.ToLower(attribute)
	val := paramInt(map[string]any{"v": value}, "v", 0)

	switch strings.ToLower(featureName) {
	case "measurement":
		switch attrLower {
		case "acactivepower":
			switch val {
			case 0:
				return features.TriggerSetPowerZero, nil
			case 100000:
				return features.TriggerSetPower100, nil
			case 1000000:
				return features.TriggerSetPower1000, nil
			default:
				// Use custom trigger: encode value in lower 32 bits.
				return features.TriggerSetPowerCustomBase | uint64(uint32(val)), nil
			}
		case "stateofcharge":
			switch val {
			case 50:
				return features.TriggerSetSoC50, nil
			case 100:
				return features.TriggerSetSoC100, nil
			}
		}
	case "status":
		switch attrLower {
		case "operatingstate":
			switch val {
			case int(features.OperatingStateStandby):
				return features.TriggerSetStandby, nil
			case int(features.OperatingStateRunning):
				return features.TriggerSetRunning, nil
			case int(features.OperatingStateFault):
				return features.TriggerFault, nil
			}
		}
	}

	return 0, fmt.Errorf("no trigger mapping for %s.%s=%v", featureName, attribute, value)
}

// invokeTriggerTestEvent sends a TriggerTestEvent invoke to the device's TestControl feature.
func (r *Runner) invokeTriggerTestEvent(ctx context.Context, state *engine.ExecutionState, trigger uint64) (string, error) {
	// Route test-control mutations through the authoritative suite control
	// channel. This avoids non-TEST zone invocation (NOT_AUTHORIZED) when
	// Main() points to an operational GRID/LOCAL connection.
	if err := r.sendTriggerViaZone(ctx, trigger, state); err != nil {
		r.debugf("invokeTriggerTestEvent: sendTriggerViaZone error: %v", err)
		return "", err
	}
	return "SUCCESS", nil
}

// handleDeviceSetValuesRapid sets multiple values rapidly (stress test).
func (r *Runner) handleDeviceSetValuesRapid(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	intervalMs := paramInt(params, "interval_ms", 100)
	featureName, _ := params[KeyFeature].(string)

	// Accept "changes" as an array of {attribute, value} maps.
	changes, ok := params[ParamChanges].([]any)
	if !ok {
		return nil, fmt.Errorf("changes parameter must be an array")
	}

	count := 0
	for i, change := range changes {
		cm, ok := change.(map[string]any)
		if !ok {
			continue
		}
		attr, _ := cm[ParamAttribute].(string)
		val := cm[ParamValue]

		trigger, err := r.resolveTriggerForSetValue(featureName, attr, val)
		if err != nil {
			r.debugf("device_set_values_rapid: skip change %d (%s=%v): %v", i, attr, val, err)
			continue
		}

		if _, err := r.invokeTriggerTestEvent(ctx, state, trigger); err != nil {
			r.debugf("device_set_values_rapid: trigger failed for change %d: %v", i, err)
			continue
		}
		count++

		// Delay between changes (except after the last one).
		if i < len(changes)-1 && intervalMs > 0 {
			time.Sleep(time.Duration(intervalMs) * time.Millisecond)
		}
	}

	return map[string]any{
		KeyValuesSet: count,
		KeyRapid:     true,
	}, nil
}

// handleDeviceTrigger triggers a device event.
//
// The corpus uses a small set of named events under
// `params.trigger`. When running against a real device, known events
// are mapped to a TestControl trigger opcode and dispatched via
// SendTriggerViaZone so the device actually transitions state. Unknown
// events fall through to the no-op behavior (record the event name
// and report triggered=true) so corpus entries that haven't been
// wired here don't error out — they just fail at the next assertion
// step the way they would have before.
func (r *Runner) handleDeviceTrigger(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	// The corpus inconsistently uses "trigger" or "event" for the event
	// name. Accept both.
	event, _ := params["trigger"].(string)
	if event == "" {
		event, _ = params[KeyEvent].(string)
	}
	state.Set(StateLastTrigger, event)

	if r.config.Target != "" {
		if opcode, ok := deviceTriggerOpcode(event, params); ok {
			if err := r.sendTriggerViaZone(ctx, opcode, state); err != nil {
				return nil, fmt.Errorf("device_trigger %q: %w", event, err)
			}
		}
	}

	return map[string]any{
		KeyTriggered: true,
		KeyEventType: event,
	}, nil
}

// deviceTriggerOpcode maps a named device_trigger event to a TestControl
// trigger opcode. The bool return indicates whether the event is known;
// callers fall back to the no-op behavior for unknown events.
func deviceTriggerOpcode(event string, params map[string]any) (uint64, bool) {
	switch event {
	case "enter_fault":
		// Parameterized: encode faultCode (default 1) into the opcode so
		// the device populates the attribute with the test-supplied value.
		code := uint32(paramInt(params, "faultCode", 1))
		return features.TriggerFaultWithCodeBase | uint64(code), true
	case "clear_fault":
		return features.TriggerClearFault, true
	}
	return 0, false
}

// handleConfigureDevice configures the device model.
func (r *Runner) handleConfigureDevice(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	ds.configured = true

	// Store configuration params.
	if endpoints, ok := params[ParamEndpoints]; ok {
		ds.attributes[ParamEndpoints] = endpoints
	}
	if features, ok := params[ParamFeatures]; ok {
		ds.attributes[ParamFeatures] = features
	}

	return map[string]any{
		KeyDeviceConfigured:     true,
		KeyConfigurationSuccess: true,
	}, nil
}

// handleConfigureExposedDevice configures an exposed device for controller tests.
func (r *Runner) handleConfigureExposedDevice(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	ds.configured = true
	if attrs, ok := params[ParamAttributes].(map[string]any); ok {
		for k, v := range attrs {
			ds.attributes[k] = v
		}
	}

	return map[string]any{
		KeyExposedDeviceConfigured: true,
	}, nil
}

// handleUpdateExposedAttribute updates an attribute on an exposed device.
func (r *Runner) handleUpdateExposedAttribute(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	key, _ := params[ParamAttribute].(string)
	value := params[KeyValue]

	if key != "" {
		ds.attributes[key] = value
	}

	return map[string]any{
		KeyAttributeUpdated: true,
	}, nil
}

// handleChangeState changes device operating/control/process state.
func (r *Runner) handleChangeState(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	changed := false

	// Accept both "operating_state" and "target_state" as the operating state param.
	targetState, _ := params[ParamOperatingState].(string)
	if targetState == "" {
		targetState, _ = params[ParamTargetState].(string)
	}
	if s := targetState; s != "" {
		ds.operatingState = s
		state.Set(StateOperatingState, s)
		changed = true

		// When running against a real device, send a trigger to actually
		// change the attribute on the device so notifications fire.
		if r.config.Target != "" {
			if trigger, ok := operatingStateTriggers[s]; ok {
				if err := r.sendTriggerViaZone(ctx, trigger, state); err != nil {
					return nil, fmt.Errorf("trigger state change on device: %w", err)
				}
			}
		}
	}
	if s, ok := params[ParamControlState].(string); ok {
		ds.controlState = s
		state.Set(StateControlState, s)
		changed = true
	}
	if s, ok := params[ParamProcessState].(string); ok {
		ds.processState = s
		state.Set(StateProcessState, s)
		changed = true
	}

	return map[string]any{
		KeyStateChanged:     changed,
		StateOperatingState: ds.operatingState,
		StateControlState:   ds.controlState,
		StateProcessState:   ds.processState,
	}, nil
}

// operatingStateTriggers maps operating state names to TestControl trigger opcodes.
var operatingStateTriggers = map[string]uint64{
	"RUNNING": features.TriggerSetRunning,
	"STANDBY": features.TriggerSetStandby,
	"FAULT":   features.TriggerFault,
}

// sendTriggerViaZone sends a triggerTestEvent invoke to the device using the
// main connection or the suite zone connection (the control channel).
func (r *Runner) sendTriggerViaZone(ctx context.Context, trigger uint64, state *engine.ExecutionState) error {
	// Always prefer the suite zone connection (the dedicated control channel).
	// pool.Main() may be a stale connection from a previous test's commission
	// step that the device already closed (DEC-066), causing broken-pipe errors
	// that shadow the working suite zone.
	conn := r.suite.Conn()
	if conn != nil && conn.isConnected() && conn.framer != nil {
		if err := r.sendTriggerOnConn(ctx, trigger, conn); err == nil {
			return nil
		} else if isIOError(err) && r.suite.ZoneID() != "" {
			// Connection can go stale between health checks and trigger readback.
			// Keep the strict suite-only contract by reconnecting suite control
			// channel and retrying once on that same path.
			if recErr := r.reconnectToZone(state); recErr == nil {
				conn = r.suite.Conn()
				if conn != nil && conn.isConnected() && conn.framer != nil {
					return r.sendTriggerOnConn(ctx, trigger, conn)
				}
			}
		} else {
			return err
		}
	}

	// Suite zone TCP can be intentionally closed by the coordinator during
	// backward transitions (commissioned → commissioning) to free a cap
	// slot. The crypto is preserved, so reconnecting is safe and gives
	// commissioning-tier tests (TC-SEC-BACKOFF-*, TC-SEC-CONN-*) a working
	// control channel before their first action runs. Previously gated on
	// StrictLifecycle, but the gate had no value: reconnecting a dead suite
	// is always preferable to hard-failing a trigger when we hold the
	// crypto.
	if r.suite.ZoneID() != "" {
		if err := r.reconnectToZone(state); err == nil {
			conn = r.suite.Conn()
			if conn != nil && conn.isConnected() && conn.framer != nil {
				return r.sendTriggerOnConn(ctx, trigger, conn)
			}
		}
	}

	if r.config != nil && r.config.StrictLifecycle && r.suite.ZoneID() != "" {
		return fmt.Errorf("strict lifecycle: no suite zone connection available for trigger")
	}

	// Fall back to pool.Main() (L3 tests where Main is the operational connection).
	if r.pool.Main() != nil && r.pool.Main().isConnected() && r.pool.Main().framer != nil {
		err := r.sendTriggerOnConn(ctx, trigger, r.pool.Main())
		if err == nil {
			r.connMgr.SetDeviceStateModified(true)
		}
		return err
	}

	return fmt.Errorf("no suite zone connection available for trigger")
}

// sendTriggerOnConn sends a TriggerTestEvent invoke on a specific connection.
func (r *Runner) sendTriggerOnConn(ctx context.Context, trigger uint64, conn *Connection) error {
	enableKey := r.config.EnableKey
	if enableKey == "" {
		return fmt.Errorf("no enable key configured")
	}

	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: 0,
		FeatureID:  uint8(model.FeatureTestControl), //nolint:gosec // constant fits in uint8
		Payload: &wire.InvokePayload{
			CommandID: features.TestControlCmdTriggerTestEvent,
			Parameters: map[uint8]any{
				features.TriggerTestEventParamEnableKey:    enableKey,
				features.TriggerTestEventParamEventTrigger: trigger,
			},
		},
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return fmt.Errorf("encode trigger request: %w", err)
	}

	// Set read deadline so a dead connection doesn't block indefinitely.
	conn.setReadDeadlineFromContext(ctx)
	defer conn.clearReadDeadline()

	if err := conn.framer.WriteFrame(data); err != nil {
		return fmt.Errorf("send trigger request: %w", err)
	}

	// Read frames until we get the invoke response. The zone connection
	// may have active subscriptions, so the device can send notifications
	// (triggered by the state change inside the invoke) before the invoke
	// response arrives on the wire. Any notifications are buffered on
	// the Connection so wait_for_notification_as_zone can retrieve them.
	for range 10 {
		respData, err := conn.framer.ReadFrame()
		if err != nil {
			return fmt.Errorf("read trigger response: %w", err)
		}

		msgType, peekErr := wire.PeekMessageType(respData)
		if peekErr == nil && msgType == wire.MessageTypeNotification {
			conn.pendingNotifications = append(conn.pendingNotifications, respData)
			continue
		}

		resp, err := wire.DecodeResponse(respData)
		if err != nil {
			return fmt.Errorf("decode trigger response: %w", err)
		}

		if !resp.IsSuccess() {
			return fmt.Errorf("trigger failed with status %d", resp.Status)
		}

		r.connMgr.SetDeviceStateModified(true)
		return nil
	}

	return fmt.Errorf("trigger response not received after skipping notifications")
}

// handleSetStateDetail sets stateDetails vendor data.
func (r *Runner) handleSetStateDetail(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	key, _ := params[KeyKey].(string)
	value := params[KeyValue]

	if key != "" {
		ds.stateDetails[key] = value
	}

	return map[string]any{
		KeyDetailSet: true,
	}, nil
}

// handleTriggerFault adds a fault to activeFaults.
func (r *Runner) handleTriggerFault(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	code := uint32(paramInt(params, KeyFaultCode, 0))
	message, _ := params[ParamFaultMessage].(string)

	ds.faults = append(ds.faults, faultEntry{
		Code:    code,
		Message: message,
		Time:    time.Now(),
	})
	ds.operatingState = OperatingStateFault

	state.Set(StateActiveFaultCount, len(ds.faults))

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerFault, state); err != nil {
			return nil, fmt.Errorf("trigger fault on device: %w", err)
		}
	}

	return map[string]any{
		KeyFaultTriggered:     true,
		KeyFaultCode:          code,
		StateActiveFaultCount: len(ds.faults),
	}, nil
}

// handleClearFault removes a fault from activeFaults.
func (r *Runner) handleClearFault(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	code := uint32(paramInt(params, KeyFaultCode, 0))

	found := false
	for i, f := range ds.faults {
		if f.Code == code {
			ds.faults = append(ds.faults[:i], ds.faults[i+1:]...)
			found = true
			break
		}
	}

	if len(ds.faults) == 0 {
		ds.operatingState = OperatingStateStandby
	}

	state.Set(StateActiveFaultCount, len(ds.faults))

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerClearFault, state); err != nil {
			return nil, fmt.Errorf("trigger clear fault on device: %w", err)
		}
	}

	return map[string]any{
		KeyFaultCleared:       found,
		StateActiveFaultCount: len(ds.faults),
	}, nil
}

// handleQueryDeviceState returns the current device state.
func (r *Runner) handleQueryDeviceState(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)

	deviceID, _ := state.Get(StateDeviceID)
	deviceIDStr, _ := deviceID.(string)

	return map[string]any{
		StateOperatingState: ds.operatingState,
		StateControlState:   ds.controlState,
		StateProcessState:   ds.processState,
		KeyActiveFaults:     len(ds.faults),
		KeyEVConnected:      ds.evConnected,
		KeyCablePluggedIn:   ds.cablePluggedIn,
		KeyDeviceID:         deviceIDStr,
		KeyDeviceIDPresent:  deviceIDStr != "",
	}, nil
}

// handleVerifyDeviceState verifies device state matches expected.
func (r *Runner) handleVerifyDeviceState(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	allMatch := true
	if expected, ok := params[ParamOperatingState].(string); ok {
		if ds.operatingState != expected {
			allMatch = false
		}
	}
	if expected, ok := params[ParamControlState].(string); ok {
		if ds.controlState != expected {
			allMatch = false
		}
	}
	if expected, ok := params[ParamProcessState].(string); ok {
		if ds.processState != expected {
			allMatch = false
		}
	}

	// Add device ID information.
	deviceID, _ := state.Get(StateDeviceID)
	deviceIDStr, _ := deviceID.(string)
	hasDeviceID := deviceIDStr != ""

	// Check if device ID matches the cert CN.
	certCN := ""
	if r.connMgr.IssuedDeviceCert() != nil {
		certCN = r.connMgr.IssuedDeviceCert().Subject.CommonName
	}
	idMatchesCN := hasDeviceID && certCN != "" && deviceIDStr == certCN

	return map[string]any{
		KeyStateMatches:          allMatch,
		StateOperatingState:      ds.operatingState,
		StateControlState:        ds.controlState,
		StateProcessState:        ds.processState,
		KeyDeviceHasDeviceID:     hasDeviceID,
		KeyDeviceIDMatchesCertCN: idMatchesCN,
		KeyDeviceID:              deviceIDStr,
	}, nil
}

// handleSetConnected sets the connection state flag.
func (r *Runner) handleSetConnected(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	ds := getDeviceState(state)
	ds.controlState = ControlStateControlled
	state.Set(StateDeviceConnected, true)

	// Also update zone state if zone_id is provided.
	if zoneID, _ := params[KeyZoneID].(string); zoneID != "" {
		zs := getZoneState(state)
		if zone, ok := zs.zones[zoneID]; ok {
			zone.Connected = true
			zone.LastSeen = time.Now()
			zone.LastSeenUpdated = true
		}
	}

	return map[string]any{KeyConnected: true}, nil
}

// handleSetDisconnected sets the disconnection state.
func (r *Runner) handleSetDisconnected(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	ds := getDeviceState(state)
	ds.controlState = ControlStateFailsafe
	state.Set(StateDeviceConnected, false)

	// Also update zone state if zone_id is provided.
	// SetDisconnected does NOT update LastSeen per spec.
	if zoneID, _ := params[KeyZoneID].(string); zoneID != "" {
		zs := getZoneState(state)
		if zone, ok := zs.zones[zoneID]; ok {
			zone.Connected = false
			zone.LastSeenUpdated = false
		}
	}

	return map[string]any{KeyDisconnected: true}, nil
}

// handleSetFailsafeLimit sets the failsafe power limit.
func (r *Runner) handleSetFailsafeLimit(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDeviceState(state)

	limit := paramFloat(params, KeyLimitWatts, 0.0)

	ds.failsafeLimit = &limit
	state.Set(StateFailsafeLimit, limit)

	return map[string]any{
		KeyFailsafeLimitSet: true,
		KeyLimitWatts:       limit,
	}, nil
}

// handleMakeProcessAvailable sets processState to AVAILABLE.
func (r *Runner) handleMakeProcessAvailable(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.processState = ProcessStateAvailable
	state.Set(StateProcessState, ProcessStateAvailable)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerProcessStateAvailable, state); err != nil {
			return nil, fmt.Errorf("trigger make process available on device: %w", err)
		}
	}

	return map[string]any{StateProcessState: ProcessStateAvailable}, nil
}

// handleStartOperation begins process execution.
func (r *Runner) handleStartOperation(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.processState = ProcessStateRunning
	ds.operatingState = OperatingStateRunning
	state.Set(StateProcessState, ProcessStateRunning)
	state.Set(StateOperatingState, OperatingStateRunning)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerSetRunning, state); err != nil {
			return nil, fmt.Errorf("trigger start operation on device: %w", err)
		}
	}

	return map[string]any{
		StateProcessState:   ProcessStateRunning,
		KeyOperationStarted: true,
	}, nil
}

// handleEVConnect simulates EV connection.
func (r *Runner) handleEVConnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.evConnected = true
	ds.cablePluggedIn = true
	state.Set(StateEVConnected, true)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerEVPlugIn, state); err != nil {
			return nil, fmt.Errorf("trigger EV connect on device: %w", err)
		}
	}

	return map[string]any{KeyEVConnected: true}, nil
}

// handleEVDisconnect simulates EV disconnection.
func (r *Runner) handleEVDisconnect(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.evConnected = false
	ds.cablePluggedIn = false
	state.Set(StateEVConnected, false)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerEVUnplug, state); err != nil {
			return nil, fmt.Errorf("trigger EV disconnect on device: %w", err)
		}
	}

	return map[string]any{KeyEVDisconnected: true}, nil
}

// handleEVRequestsCharge simulates EV requesting charge.
func (r *Runner) handleEVRequestsCharge(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.evConnected = true
	state.Set(StateEVChargeRequested, true)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerEVRequestCharge, state); err != nil {
			return nil, fmt.Errorf("trigger EV request charge on device: %w", err)
		}
	}

	return map[string]any{KeyChargeRequested: true}, nil
}

// handlePlugInCable simulates plugging in the cable.
func (r *Runner) handlePlugInCable(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.cablePluggedIn = true
	state.Set(StateCablePluggedIn, true)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerEVPlugIn, state); err != nil {
			return nil, fmt.Errorf("trigger plug in cable on device: %w", err)
		}
	}

	return map[string]any{KeyCablePluggedIn: true}, nil
}

// handleUnplugCable simulates unplugging the cable.
func (r *Runner) handleUnplugCable(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.cablePluggedIn = false
	ds.evConnected = false
	state.Set(StateCablePluggedIn, false)
	state.Set(StateEVConnected, false)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerEVUnplug, state); err != nil {
			return nil, fmt.Errorf("trigger unplug cable on device: %w", err)
		}
	}

	return map[string]any{KeyCableUnplugged: true}, nil
}

// handleUserOverride simulates a user override command.
func (r *Runner) handleUserOverride(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.controlState = ControlStateOverride
	state.Set(StateControlState, ControlStateOverride)

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerControlStateOverride, state); err != nil {
			return nil, fmt.Errorf("trigger user override on device: %w", err)
		}
	}

	return map[string]any{
		KeyOverrideActive: true,
		StateControlState: ControlStateOverride,
	}, nil
}

// handleFactoryReset simulates a factory reset.
func (r *Runner) handleFactoryReset(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	// Reset device state to defaults.
	s := &deviceState{
		operatingState: OperatingStateStandby,
		controlState:   ControlStateAutonomous,
		processState:   ProcessStateNone,
		faults:         make([]faultEntry, 0),
		stateDetails:   make(map[string]any),
		attributes:     make(map[string]any),
	}
	state.Custom["device_state"] = s

	if r.config.Target != "" {
		if err := r.sendTriggerViaZone(ctx, features.TriggerFactoryReset, state); err != nil {
			r.debugf("factory reset trigger skipped (no connection): %v", err)
		}
	}

	// Close and reset connection state -- the device is starting fresh.
	if r.pool.Main() != nil && r.pool.Main().isConnected() {
		_ = r.pool.Main().Close()
	}
	r.connMgr.SetPASEState(nil)

	return map[string]any{
		KeyFactoryReset:     true,
		StateOperatingState: OperatingStateStandby,
		StateControlState:   ControlStateAutonomous,
	}, nil
}

// handlePowerCycle simulates a power cycle.
func (r *Runner) handlePowerCycle(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.operatingState = OperatingStateStandby
	ds.controlState = ControlStateAutonomous
	ds.processState = ProcessStateNone

	// Close connection if any.
	if r.pool.Main() != nil && r.pool.Main().isConnected() {
		_ = r.pool.Main().Close()
	}

	return map[string]any{
		KeyPowerCycled:      true,
		StateOperatingState: OperatingStateStandby,
	}, nil
}

// handlePowerOnDevice simulates powering on a device.
func (r *Runner) handlePowerOnDevice(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDeviceState(state)
	ds.operatingState = OperatingStateStandby

	return map[string]any{
		KeyPoweredOn:        true,
		StateOperatingState: OperatingStateStandby,
	}, nil
}

// handleReboot simulates a device reboot.
func (r *Runner) handleReboot(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	return r.handlePowerCycle(ctx, step, state)
}

// handleRestart simulates a device restart.
func (r *Runner) handleRestart(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	return r.handlePowerCycle(ctx, step, state)
}
