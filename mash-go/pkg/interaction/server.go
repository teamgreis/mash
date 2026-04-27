package interaction

import (
	"context"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// Server handles incoming MASH requests and manages subscriptions.
type Server struct {
	mu sync.RWMutex

	device *model.Device

	// Subscription management
	subscriptions   map[uint32]*Subscription
	nextSubID       uint32
	notifyHandler   NotificationHandler
	subscriptionsMu sync.RWMutex
}

// NotificationHandler is called when a notification needs to be sent.
type NotificationHandler func(notif *wire.Notification)

// NewServer creates a new interaction server for the given device.
func NewServer(device *model.Device) *Server {
	return &Server{
		device:        device,
		subscriptions: make(map[uint32]*Subscription),
		nextSubID:     1,
	}
}

// SetNotificationHandler sets the handler for outgoing notifications.
func (s *Server) SetNotificationHandler(handler NotificationHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyHandler = handler
}

// HandleRequest processes an incoming request and returns a response.
func (s *Server) HandleRequest(ctx context.Context, req *wire.Request) *wire.Response {
	switch req.Operation {
	case wire.OpRead:
		return s.handleRead(ctx, req)
	case wire.OpWrite:
		return s.handleWrite(ctx, req)
	case wire.OpSubscribe:
		return s.handleSubscribe(ctx, req)
	case wire.OpInvoke:
		return s.handleInvoke(ctx, req)
	default:
		return &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusUnsupported,
			Payload:   &wire.ErrorPayload{Message: "unknown operation"},
		}
	}
}

// handleRead processes a Read request.
func (s *Server) handleRead(ctx context.Context, req *wire.Request) *wire.Response {
	// Get the feature
	feature, err := s.device.GetFeature(req.EndpointID, model.FeatureType(req.FeatureID))
	if err != nil {
		if err == model.ErrEndpointNotFound {
			return errorResponse(req.MessageID, wire.StatusInvalidEndpoint, "endpoint not found")
		}
		return errorResponse(req.MessageID, wire.StatusInvalidFeature, "feature not found")
	}

	// Parse payload to get requested attribute IDs
	var attrIDs []uint16
	if req.Payload != nil {
		// Handle different payload types
		switch p := req.Payload.(type) {
		case *wire.ReadPayload:
			attrIDs = p.AttributeIDs
		case wire.ReadPayload:
			attrIDs = p.AttributeIDs
		case []uint16:
			attrIDs = p
		case []any:
			// CBOR may decode as []any
			attrIDs = make([]uint16, len(p))
			for i, v := range p {
				switch n := v.(type) {
				case uint16:
					attrIDs[i] = n
				case uint64:
					attrIDs[i] = uint16(n)
				case int64:
					attrIDs[i] = uint16(n)
				}
			}
		case map[string]any:
			// CBOR may decode struct as map
			if ids, ok := p["1"].([]any); ok {
				attrIDs = make([]uint16, len(ids))
				for i, v := range ids {
					if n, ok := v.(uint64); ok {
						attrIDs[i] = uint16(n)
					}
				}
			}
		}
	}

	// Read attributes
	values := make(map[uint16]any)
	if len(attrIDs) == 0 {
		// Read all attributes
		values = feature.ReadAllAttributes()
	} else {
		// Read specific attributes
		for _, id := range attrIDs {
			val, err := feature.ReadAttribute(id)
			if err != nil {
				continue // Skip unreadable attributes
			}
			values[id] = val
		}
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   values,
	}
}

// handleWrite processes a Write request.
func (s *Server) handleWrite(ctx context.Context, req *wire.Request) *wire.Response {
	// Get the feature
	feature, err := s.device.GetFeature(req.EndpointID, model.FeatureType(req.FeatureID))
	if err != nil {
		if err == model.ErrEndpointNotFound {
			return errorResponse(req.MessageID, wire.StatusInvalidEndpoint, "endpoint not found")
		}
		return errorResponse(req.MessageID, wire.StatusInvalidFeature, "feature not found")
	}

	// Parse payload to get attributes to write
	var attrs map[uint16]any
	switch p := req.Payload.(type) {
	case map[uint16]any:
		attrs = p
	case map[any]any:
		attrs = make(map[uint16]any)
		for k, v := range p {
			switch key := k.(type) {
			case uint16:
				attrs[key] = v
			case uint64:
				attrs[uint16(key)] = v
			case int64:
				attrs[uint16(key)] = v
			}
		}
	default:
		return errorResponse(req.MessageID, wire.StatusInvalidParameter, "invalid payload format")
	}

	if attrs == nil || len(attrs) == 0 {
		return errorResponse(req.MessageID, wire.StatusInvalidParameter, "no attributes to write")
	}

	// Write attributes
	results := make(map[uint16]any)
	var writeErr error
	for id, val := range attrs {
		if err := feature.WriteAttribute(id, val); err != nil {
			writeErr = err
			continue
		}
		// Return the actual value after write (may differ due to constraints)
		if readVal, err := feature.ReadAttribute(id); err == nil {
			results[id] = readVal
		} else {
			results[id] = val
		}
	}

	if writeErr != nil && len(results) == 0 {
		return errorResponse(req.MessageID, wire.StatusConstraintError, writeErr.Error())
	}

	// Notify subscribers of changes
	s.notifyChanges(req.EndpointID, req.FeatureID, results)

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   results,
	}
}

// handleSubscribe processes a Subscribe request.
func (s *Server) handleSubscribe(ctx context.Context, req *wire.Request) *wire.Response {
	// Check for unsubscribe (endpointId=0, featureId=0)
	if req.EndpointID == 0 && req.FeatureID == 0 {
		return s.handleUnsubscribe(ctx, req)
	}

	// Get the feature
	feature, err := s.device.GetFeature(req.EndpointID, model.FeatureType(req.FeatureID))
	if err != nil {
		if err == model.ErrEndpointNotFound {
			return errorResponse(req.MessageID, wire.StatusInvalidEndpoint, "endpoint not found")
		}
		return errorResponse(req.MessageID, wire.StatusInvalidFeature, "feature not found")
	}

	// Parse subscription parameters
	var attrIDs []uint16
	var minInterval, maxInterval uint32 = 1000, 60000 // Defaults

	if req.Payload != nil {
		switch p := req.Payload.(type) {
		case *wire.SubscribePayload:
			attrIDs = p.AttributeIDs
			if p.MinInterval > 0 {
				minInterval = p.MinInterval
			}
			if p.MaxInterval > 0 {
				maxInterval = p.MaxInterval
			}
		case wire.SubscribePayload:
			attrIDs = p.AttributeIDs
			if p.MinInterval > 0 {
				minInterval = p.MinInterval
			}
			if p.MaxInterval > 0 {
				maxInterval = p.MaxInterval
			}
		case map[any]any:
			if v, ok := p[uint64(1)].([]any); ok {
				attrIDs = make([]uint16, len(v))
				for i, id := range v {
					if n, ok := id.(uint64); ok {
						attrIDs[i] = uint16(n)
					}
				}
			}
			if v, ok := p[uint64(2)].(uint64); ok {
				minInterval = uint32(v)
			}
			if v, ok := p[uint64(3)].(uint64); ok {
				maxInterval = uint32(v)
			}
		}
	}

	// Create subscription
	s.subscriptionsMu.Lock()
	subID := s.nextSubID
	s.nextSubID++

	sub := &Subscription{
		ID:          subID,
		EndpointID:  req.EndpointID,
		FeatureID:   req.FeatureID,
		AttributeIDs: attrIDs,
		MinInterval: time.Duration(minInterval) * time.Millisecond,
		MaxInterval: time.Duration(maxInterval) * time.Millisecond,
		LastNotify:  time.Now(),
		Values:      make(map[uint16]any),
	}
	s.subscriptions[subID] = sub
	s.subscriptionsMu.Unlock()

	// Get priming report (current values)
	currentValues := make(map[uint16]any)
	if len(attrIDs) == 0 {
		// Subscribe to all
		currentValues = feature.ReadAllAttributes()
	} else {
		for _, id := range attrIDs {
			if val, err := feature.ReadAttribute(id); err == nil {
				currentValues[id] = val
			}
		}
	}

	// Store current values for delta tracking
	sub.mu.Lock()
	for k, v := range currentValues {
		sub.Values[k] = v
	}
	sub.mu.Unlock()

	// Subscribe to feature for notifications
	feature.Subscribe(sub)

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload: &wire.SubscribeResponsePayload{
			SubscriptionID: subID,
			CurrentValues:  currentValues,
		},
	}
}

// handleUnsubscribe cancels a subscription.
func (s *Server) handleUnsubscribe(ctx context.Context, req *wire.Request) *wire.Response {
	var subID uint32
	switch p := req.Payload.(type) {
	case *wire.UnsubscribePayload:
		subID = p.SubscriptionID
	case wire.UnsubscribePayload:
		subID = p.SubscriptionID
	case map[any]any:
		if v, ok := p[uint64(1)].(uint64); ok {
			subID = uint32(v)
		}
	default:
		return errorResponse(req.MessageID, wire.StatusInvalidParameter, "invalid unsubscribe payload")
	}

	s.subscriptionsMu.Lock()
	sub, exists := s.subscriptions[subID]
	if exists {
		delete(s.subscriptions, subID)
	}
	s.subscriptionsMu.Unlock()

	if !exists {
		return errorResponse(req.MessageID, wire.StatusInvalidParameter, "subscription not found")
	}

	// Unsubscribe from feature
	if feature, err := s.device.GetFeature(sub.EndpointID, model.FeatureType(sub.FeatureID)); err == nil {
		feature.Unsubscribe(sub)
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
	}
}

// handleInvoke processes an Invoke request.
func (s *Server) handleInvoke(ctx context.Context, req *wire.Request) *wire.Response {
	// Get the feature
	feature, err := s.device.GetFeature(req.EndpointID, model.FeatureType(req.FeatureID))
	if err != nil {
		if err == model.ErrEndpointNotFound {
			return errorResponse(req.MessageID, wire.StatusInvalidEndpoint, "endpoint not found")
		}
		return errorResponse(req.MessageID, wire.StatusInvalidFeature, "feature not found")
	}

	// Parse invoke payload — integer-keyed parameters per
	// docs/interaction-model.md §7.1 (Issue #11 fix).
	var cmdID uint8
	var params map[uint8]any

	switch p := req.Payload.(type) {
	case *wire.InvokePayload:
		cmdID = p.CommandID
		params = wire.ToUint8Map(p.Parameters)
	case wire.InvokePayload:
		cmdID = p.CommandID
		params = wire.ToUint8Map(p.Parameters)
	case map[any]any:
		if v, ok := p[uint64(1)].(uint64); ok {
			cmdID = uint8(v)
		}
		if raw, ok := p[uint64(2)]; ok {
			params = wire.ToUint8Map(raw)
		}
	default:
		return errorResponse(req.MessageID, wire.StatusInvalidParameter, "invalid invoke payload")
	}

	// Invoke the command
	result, err := feature.InvokeCommand(ctx, cmdID, params)
	if err != nil {
		if err == model.ErrCommandNotFound {
			return errorResponse(req.MessageID, wire.StatusInvalidCommand, "command not found")
		}
		if err == model.ErrInvalidParameters {
			return errorResponse(req.MessageID, wire.StatusInvalidParameter, err.Error())
		}
		return errorResponse(req.MessageID, wire.StatusConstraintError, err.Error())
	}

	return &wire.Response{
		MessageID: req.MessageID,
		Status:    wire.StatusSuccess,
		Payload:   result,
	}
}

// notifyChanges sends notifications to relevant subscriptions.
func (s *Server) notifyChanges(endpointID uint8, featureID uint8, changes map[uint16]any) {
	s.mu.RLock()
	handler := s.notifyHandler
	s.mu.RUnlock()

	if handler == nil {
		return
	}

	s.subscriptionsMu.RLock()
	defer s.subscriptionsMu.RUnlock()

	for _, sub := range s.subscriptions {
		if sub.EndpointID != endpointID || sub.FeatureID != featureID {
			continue
		}

		// Filter to subscribed attributes
		subChanges := make(map[uint16]any)
		for id, val := range changes {
			if sub.IsSubscribedTo(id) {
				subChanges[id] = val
			}
		}

		if len(subChanges) == 0 {
			continue
		}

		// Check min interval
		if !sub.CanNotify() {
			continue
		}

		handler(&wire.Notification{
			SubscriptionID: sub.ID,
			EndpointID:     endpointID,
			FeatureID:      featureID,
			Changes:        subChanges,
		})
		sub.MarkNotified()
	}
}

// CancelAllSubscriptions cancels all subscriptions (e.g., on disconnect).
func (s *Server) CancelAllSubscriptions() {
	s.subscriptionsMu.Lock()
	defer s.subscriptionsMu.Unlock()

	for subID, sub := range s.subscriptions {
		if feature, err := s.device.GetFeature(sub.EndpointID, model.FeatureType(sub.FeatureID)); err == nil {
			feature.Unsubscribe(sub)
		}
		delete(s.subscriptions, subID)
	}
}

// GetSubscription returns a subscription by ID.
func (s *Server) GetSubscription(id uint32) (*Subscription, bool) {
	s.subscriptionsMu.RLock()
	defer s.subscriptionsMu.RUnlock()
	sub, ok := s.subscriptions[id]
	return sub, ok
}

// SubscriptionCount returns the number of active subscriptions.
func (s *Server) SubscriptionCount() int {
	s.subscriptionsMu.RLock()
	defer s.subscriptionsMu.RUnlock()
	return len(s.subscriptions)
}

// errorResponse creates an error response.
func errorResponse(msgID uint32, status wire.Status, message string) *wire.Response {
	return &wire.Response{
		MessageID: msgID,
		Status:    status,
		Payload:   &wire.ErrorPayload{Message: message},
	}
}

