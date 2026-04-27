package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
	"github.com/mash-protocol/mash-go/pkg/zonecontext"
)

// NotificationSender is a function that sends a notification to the remote peer.
type NotificationSender func(notification *wire.Notification) error

// WriteCallback is called after a successful write operation.
// It receives the endpoint ID, feature ID, and the map of attribute IDs to values that were written.
type WriteCallback func(endpointID uint8, featureID uint8, attrs map[uint16]any)

// InvokeCallback is called after a successful invoke operation.
// It receives the endpoint ID, feature ID, command ID, parameters, and result.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
type InvokeCallback func(endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any, result any)

// ProtocolHandler handles MASH protocol messages for a device or controller.
// It routes Read/Write/Subscribe/Invoke requests to the appropriate
// features and generates responses. The handler is bidirectional and can be
// used on both device and controller sides.
type ProtocolHandler struct {
	mu sync.RWMutex

	device       DeviceModel
	peerID       string        // The remote peer's identifier (generic, works for both device and controller)
	peerZoneType cert.ZoneType // The remote peer's zone type (GRID, LOCAL, etc.)

	// Subscription management
	subscriptions *SessionSubscriptionTracker

	// Send function for notifications
	sendNotification NotificationSender

	// Write callback (optional) - called after successful writes
	onWrite WriteCallback

	// Invoke callback (optional) - called after successful invokes
	onInvoke InvokeCallback

	// Protocol logging (optional)
	logger          log.Logger
	connID          string
	onMessageLogged func()
}

// NewProtocolHandler creates a new protocol handler for a device.
func NewProtocolHandler(device DeviceModel) *ProtocolHandler {
	return &ProtocolHandler{
		device:        device,
		subscriptions: NewSessionSubscriptionTracker(),
	}
}

// NewProtocolHandlerWithSend creates a new protocol handler with a custom notification sender.
func NewProtocolHandlerWithSend(device DeviceModel, send NotificationSender) *ProtocolHandler {
	return &ProtocolHandler{
		device:           device,
		subscriptions:    NewSessionSubscriptionTracker(),
		sendNotification: send,
	}
}

// Device returns the underlying device model.
func (h *ProtocolHandler) Device() DeviceModel {
	return h.device
}

// SessionSubscriptions returns the session subscription tracker for this handler.
func (h *ProtocolHandler) SessionSubscriptions() *SessionSubscriptionTracker {
	return h.subscriptions
}

// SetSendNotification sets the notification sender function.
func (h *ProtocolHandler) SetSendNotification(send NotificationSender) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sendNotification = send
}

// SetOnWrite sets the callback for write operations.
func (h *ProtocolHandler) SetOnWrite(cb WriteCallback) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onWrite = cb
}

// SetOnInvoke sets the callback for invoke operations.
func (h *ProtocolHandler) SetOnInvoke(cb InvokeCallback) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onInvoke = cb
}

// PeerID returns the remote peer's identifier.
func (h *ProtocolHandler) PeerID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peerID
}

// SetPeerID sets the remote peer's identifier.
func (h *ProtocolHandler) SetPeerID(peerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.peerID = peerID
}

// ZoneID returns the current zone context.
// Deprecated: Use PeerID instead for generic peer identification.
func (h *ProtocolHandler) ZoneID() string {
	return h.PeerID()
}

// SetZoneID sets the zone context for authorization.
// Deprecated: Use SetPeerID instead for generic peer identification.
func (h *ProtocolHandler) SetZoneID(zoneID string) {
	h.SetPeerID(zoneID)
}

// PeerZoneType returns the remote peer's zone type.
func (h *ProtocolHandler) PeerZoneType() cert.ZoneType {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peerZoneType
}

// SetPeerZoneType sets the remote peer's zone type.
func (h *ProtocolHandler) SetPeerZoneType(zt cert.ZoneType) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.peerZoneType = zt
}

// SetLogger sets the protocol logger and connection ID.
// Events logged will include the connectionID for correlation.
func (h *ProtocolHandler) SetLogger(logger log.Logger, connectionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logger = logger
	h.connID = connectionID
}

// SetOnMessageLogged sets the callback invoked after each protocol message is logged.
func (h *ProtocolHandler) SetOnMessageLogged(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onMessageLogged = fn
}

// HandleRequest processes a protocol request and returns a response.
func (h *ProtocolHandler) HandleRequest(req *wire.Request) *wire.Response {
	startTime := time.Now()

	// Log the incoming request
	h.logRequest(req)

	var resp *wire.Response

	// Validate the operation
	if !req.Operation.IsValid() {
		resp = &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusUnsupported,
			Payload: &wire.ErrorPayload{
				Message: "unsupported operation",
			},
		}
	} else {
		// Route to appropriate handler
		switch req.Operation {
		case wire.OpRead:
			resp = h.handleRead(req)
		case wire.OpWrite:
			resp = h.handleWrite(req)
		case wire.OpSubscribe:
			resp = h.handleSubscribe(req)
		case wire.OpInvoke:
			resp = h.handleInvoke(req)
		default:
			resp = &wire.Response{
				MessageID: req.MessageID,
				Status:    wire.StatusUnsupported,
				Payload: &wire.ErrorPayload{
					Message: "unsupported operation",
				},
			}
		}
	}

	// Log the outgoing response with processing time
	processingTime := time.Since(startTime)
	h.logResponse(resp, processingTime)

	return resp
}

// logRequest logs an incoming request event.
func (h *ProtocolHandler) logRequest(req *wire.Request) {
	h.mu.RLock()
	logger := h.logger
	connID := h.connID
	onLogged := h.onMessageLogged
	h.mu.RUnlock()

	if logger == nil {
		return
	}

	op := req.Operation
	endpointID := req.EndpointID
	featureID := req.FeatureID

	logger.Log(log.Event{
		Timestamp:    time.Now(),
		ConnectionID: connID,
		Direction:    log.DirectionIn,
		Layer:        log.LayerWire,
		Category:     log.CategoryMessage,
		Message: &log.MessageEvent{
			Type:       log.MessageTypeRequest,
			MessageID:  req.MessageID,
			Operation:  &op,
			EndpointID: &endpointID,
			FeatureID:  &featureID,
			Payload:    req.Payload,
		},
	})

	if onLogged != nil {
		onLogged()
	}
}

// logResponse logs an outgoing response event.
func (h *ProtocolHandler) logResponse(resp *wire.Response, processingTime time.Duration) {
	h.mu.RLock()
	logger := h.logger
	connID := h.connID
	onLogged := h.onMessageLogged
	h.mu.RUnlock()

	if logger == nil {
		return
	}

	status := resp.Status

	logger.Log(log.Event{
		Timestamp:    time.Now(),
		ConnectionID: connID,
		Direction:    log.DirectionOut,
		Layer:        log.LayerWire,
		Category:     log.CategoryMessage,
		Message: &log.MessageEvent{
			Type:           log.MessageTypeResponse,
			MessageID:      resp.MessageID,
			Status:         &status,
			Payload:        resp.Payload,
			ProcessingTime: &processingTime,
		},
	})

	if onLogged != nil {
		onLogged()
	}
}

// handleRead processes a Read request.
func (h *ProtocolHandler) handleRead(req *wire.Request) *wire.Response {
	// Get the endpoint
	endpoint, err := h.device.GetEndpoint(req.EndpointID)
	if err != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidEndpoint,
			Payload: &wire.ErrorPayload{
				Message: "endpoint not found",
			},
		}
	}

	// Get the feature
	feature, ferr := endpoint.GetFeatureByID(req.FeatureID)
	if ferr != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidFeature,
			Payload: &wire.ErrorPayload{
				Message: "feature not found",
			},
		}
	}

	// Determine which attributes to read.
	// The Payload field is decoded as `any` by CBOR, so after a round-trip
	// it's a raw map (map[any]any), not *wire.ReadPayload.
	attributeIDs := wire.ExtractReadAttributeIDs(req.Payload)

	// Build context with caller zone identity
	ctx := context.Background()
	if h.peerID != "" {
		ctx = zonecontext.ContextWithCallerZoneID(ctx, h.peerID)
	}
	if h.peerZoneType != 0 {
		ctx = zonecontext.ContextWithCallerZoneType(ctx, h.peerZoneType)
	}

	// Read attributes using context-aware methods
	var result map[uint16]any
	if len(attributeIDs) == 0 {
		// Read all attributes
		result = feature.ReadAllAttributesWithContext(ctx)
	} else {
		// Read specific attributes
		result = make(map[uint16]any)
		for _, attrID := range attributeIDs {
			value, err := feature.ReadAttributeWithContext(ctx, attrID)
			if err != nil {
				return &wire.Response{
					MessageID: req.MessageID,
					Status:    wire.StatusInvalidAttribute,
					Payload: &wire.ErrorPayload{
						Message: fmt.Sprintf("attribute 0x%04x not found", attrID),
					},
				}
			}
			result[attrID] = value
		}
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   result,
	}
}

// handleWrite processes a Write request.
func (h *ProtocolHandler) handleWrite(req *wire.Request) *wire.Response {
	// Get the endpoint
	endpoint, err := h.device.GetEndpoint(req.EndpointID)
	if err != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidEndpoint,
			Payload: &wire.ErrorPayload{
				Message: "endpoint not found",
			},
		}
	}

	// Get the feature
	feature, ferr := endpoint.GetFeatureByID(req.FeatureID)
	if ferr != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidFeature,
			Payload: &wire.ErrorPayload{
				Message: "feature not found",
			},
		}
	}

	// Parse write payload. After CBOR round-trip, the payload is a raw map
	// with uint64 keys, not wire.WritePayload.
	writePayload := wire.ExtractWritePayload(req.Payload)
	if writePayload == nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidParameter,
			Payload: &wire.ErrorPayload{
				Message: "invalid write payload",
			},
		}
	}

	// Write attributes
	result := make(map[uint16]any)
	var firstError error
	var firstErrorStatus wire.Status

	for attrID, value := range writePayload {
		err := feature.WriteAttribute(attrID, value)
		if err != nil {
			if firstError == nil {
				firstError = err
				// Determine error type
				switch err {
				case model.ErrAttributeNotFound:
					firstErrorStatus = wire.StatusInvalidAttribute
				case model.ErrFeatureReadOnly, model.ErrAttributeNotWritable:
					firstErrorStatus = wire.StatusReadOnly
				default:
					firstErrorStatus = wire.StatusConstraintError
				}
			}
		} else {
			// Read back the actual value (may have been constrained)
			if actual, err := feature.ReadAttribute(attrID); err == nil {
				result[attrID] = actual
			}
		}
	}

	if firstError != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    firstErrorStatus,
			Payload: &wire.ErrorPayload{
				Message: firstError.Error(),
			},
		}
	}

	// Call write callback if set and we have successful writes
	if len(result) > 0 {
		h.mu.RLock()
		onWrite := h.onWrite
		h.mu.RUnlock()
		if onWrite != nil {
			onWrite(req.EndpointID, req.FeatureID, result)
		}
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   result,
	}
}

// handleSubscribe processes a Subscribe or Unsubscribe request.
func (h *ProtocolHandler) handleSubscribe(req *wire.Request) *wire.Response {
	// Check for unsubscribe (featureID = 0)
	if req.FeatureID == 0 {
		return h.handleUnsubscribe(req)
	}

	// Get the endpoint
	endpoint, err := h.device.GetEndpoint(req.EndpointID)
	if err != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidEndpoint,
			Payload: &wire.ErrorPayload{
				Message: "endpoint not found",
			},
		}
	}

	// Get the feature
	feature, ferr := endpoint.GetFeatureByID(req.FeatureID)
	if ferr != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidFeature,
			Payload: &wire.ErrorPayload{
				Message: "feature not found",
			},
		}
	}

	// Parse subscribe payload. After CBOR round-trip the payload is a raw
	// map, not *wire.SubscribePayload.
	subPayload := wire.ExtractSubscribePayload(req.Payload)

	// Extract attribute IDs from payload
	var attributeIDs []uint16
	if subPayload != nil {
		attributeIDs = subPayload.AttributeIDs
	}

	// Add subscription (inbound = from remote peer to our features)
	subID := h.subscriptions.AddInbound(req.EndpointID, req.FeatureID, attributeIDs)

	// Build context with caller zone identity for priming report
	ctx := context.Background()
	if h.peerID != "" {
		ctx = zonecontext.ContextWithCallerZoneID(ctx, h.peerID)
	}
	if h.peerZoneType != 0 {
		ctx = zonecontext.ContextWithCallerZoneType(ctx, h.peerZoneType)
	}

	// Read current values for priming report using context-aware methods
	var currentValues map[uint16]any
	if len(attributeIDs) == 0 {
		currentValues = feature.ReadAllAttributesWithContext(ctx)
	} else {
		currentValues = make(map[uint16]any)
		for _, attrID := range attributeIDs {
			if value, err := feature.ReadAttributeWithContext(ctx, attrID); err == nil {
				currentValues[attrID] = value
			}
		}
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload: &wire.SubscribeResponsePayload{
			SubscriptionID: subID,
			CurrentValues:  currentValues,
		},
	}
}

// handleUnsubscribe processes an unsubscribe request.
func (h *ProtocolHandler) handleUnsubscribe(req *wire.Request) *wire.Response {
	// Parse unsubscribe payload. After CBOR roundtrip, the payload may be
	// a typed *UnsubscribePayload (from Go tests) or a map[any]any (from wire).
	var subID uint32
	if req.Payload != nil {
		switch p := req.Payload.(type) {
		case *wire.UnsubscribePayload:
			subID = p.SubscriptionID
		case map[any]any:
			// CBOR key 1 = subscriptionId
			if id, ok := wire.ToUint32(p[uint64(1)]); ok {
				subID = id
			}
		}
	}

	if subID == 0 {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidParameter,
			Payload: &wire.ErrorPayload{
				Message: "subscription ID required",
			},
		}
	}

	// Remove subscription
	exists := h.subscriptions.RemoveInbound(subID)

	if !exists {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidParameter,
			Payload: &wire.ErrorPayload{
				Message: "subscription not found",
			},
		}
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
	}
}

// handleInvoke processes an Invoke request.
func (h *ProtocolHandler) handleInvoke(req *wire.Request) *wire.Response {
	// Get the endpoint
	endpoint, err := h.device.GetEndpoint(req.EndpointID)
	if err != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidEndpoint,
			Payload: &wire.ErrorPayload{
				Message: "endpoint not found",
			},
		}
	}

	// Get the feature
	feature, ferr := endpoint.GetFeatureByID(req.FeatureID)
	if ferr != nil {
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidFeature,
			Payload: &wire.ErrorPayload{
				Message: "feature not found",
			},
		}
	}

	// Parse invoke payload — CBOR may decode the parameters map either
	// as ``map[any]any`` (generic decode path) or as a typed
	// ``map[uint8]any`` (when the CBOR library knows the schema).
	// Per docs/interaction-model.md §7.1 the parameters map is
	// integer-keyed; we coerce both decode shapes into ``map[uint8]any``.
	// (Pre-Issue-#11 mash-go silently dropped integer keys via a
	// string-only converter; that violated the spec.)
	var commandID uint8
	var params map[uint8]any

	switch p := req.Payload.(type) {
	case *wire.InvokePayload:
		commandID = p.CommandID
		if p.Parameters != nil {
			params = wire.ToUint8Map(p.Parameters)
		}
	case wire.InvokePayload:
		commandID = p.CommandID
		if p.Parameters != nil {
			params = wire.ToUint8Map(p.Parameters)
		}
	case map[any]any:
		// CBOR decoded payload as generic map with integer keys.
		// InvokePayload: {1: commandId, 2: parameters}
		if v, ok := p[uint64(1)].(uint64); ok {
			commandID = uint8(v)
		}
		if raw, ok := p[uint64(2)]; ok {
			params = wire.ToUint8Map(raw)
		}
	default:
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusInvalidParameter,
			Payload: &wire.ErrorPayload{
				Message: "command payload required",
			},
		}
	}

	// Invoke the command with caller zone ID and type in context
	ctx := context.Background()
	if h.peerID != "" {
		ctx = zonecontext.ContextWithCallerZoneID(ctx, h.peerID)
	}
	if h.peerZoneType != 0 {
		ctx = zonecontext.ContextWithCallerZoneType(ctx, h.peerZoneType)
	}
	result, err := feature.InvokeCommand(ctx, commandID, params)
	if err != nil {
		status := wire.StatusInvalidCommand
		var cmdErr *wire.CommandError
		if errors.As(err, &cmdErr) {
			status = cmdErr.Status
		} else if errors.Is(err, model.ErrCommandNotFound) {
			status = wire.StatusInvalidCommand
		} else if errors.Is(err, model.ErrInvalidParameters) {
			status = wire.StatusInvalidParameter
		} else if errors.Is(err, model.ErrCommandNotAllowed) {
			status = wire.StatusConstraintError
		}
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    status,
			Payload: &wire.ErrorPayload{
				Message: err.Error(),
			},
		}
	}

	// Call invoke callback if set
	h.mu.RLock()
	onInvoke := h.onInvoke
	h.mu.RUnlock()
	if onInvoke != nil {
		onInvoke(req.EndpointID, req.FeatureID, commandID, params, result)
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   result,
	}
}

// GetSubscription returns an inbound subscription by ID.
func (h *ProtocolHandler) GetSubscription(id uint32) (*Subscription, bool) {
	sub := h.subscriptions.GetInbound(id)
	return sub, sub != nil
}

// SubscriptionCount returns the number of active inbound subscriptions.
func (h *ProtocolHandler) SubscriptionCount() int {
	return h.subscriptions.InboundCount()
}

// GetMatchingSubscriptions returns subscription IDs that match the given endpoint, feature, and attribute.
// If attrID is 0, it matches subscriptions to any attribute of the feature.
func (h *ProtocolHandler) GetMatchingSubscriptions(endpointID uint8, featureID uint8, attrID uint16) []uint32 {
	matches := h.subscriptions.GetMatchingInbound(endpointID, featureID, attrID)
	result := make([]uint32, len(matches))
	for i, sub := range matches {
		result[i] = sub.ID
	}
	return result
}

// NotifyAttributeChange sends notifications to all inbound subscriptions that
// match the given endpoint, feature, and attribute.
func (h *ProtocolHandler) NotifyAttributeChange(endpointID, featureID uint8, attributeID uint16, value any) error {
	h.mu.RLock()
	sendFunc := h.sendNotification
	h.mu.RUnlock()

	// If no send function is configured, silently succeed
	if sendFunc == nil {
		return nil
	}

	// Find matching subscriptions
	matches := h.subscriptions.GetMatchingInbound(endpointID, featureID, attributeID)

	// Send notification to each matching subscription
	for _, sub := range matches {
		notification := &wire.Notification{
			SubscriptionID: sub.ID,
			EndpointID:     endpointID,
			FeatureID:      featureID,
			Changes:        map[uint16]any{attributeID: value},
		}
		if err := sendFunc(notification); err != nil {
			return err
		}
	}

	return nil
}

