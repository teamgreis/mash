package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/interaction"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// ZoneSession manages a device-side session with a connected controller zone.
// It handles incoming requests, routes them to the ProtocolHandler,
// and sends responses back over the connection.
//
// ZoneSession also supports bidirectional communication, allowing the device
// to send requests to the controller (Read/Write/Subscribe/Invoke) and
// receive responses and notifications.
type ZoneSession struct {
	mu sync.RWMutex

	zoneID  string
	conn    Sendable
	handler *dispatch.ProtocolHandler
	closed  bool
	logger  *slog.Logger

	// Notification dispatcher for subscription heartbeats and coalescing
	dispatcher       *dispatch.NotificationDispatcher
	dispatcherConnID uint64

	// Protocol logging (optional)
	protocolLogger log.Logger
	connID         string

	// Capability snapshot tracking (optional)
	snapshotPolicy SnapshotPolicy
	snapshot       *snapshotTracker

	// Bidirectional support: client for sending requests to controller
	client *interaction.Client
	sender *TransportRequestSender

	// Renewal handling
	renewalHandler       *DeviceRenewalHandler
	onCertRenewalSuccess func(zoneID string, handler *DeviceRenewalHandler)
}

// NewZoneSession creates a new zone session.
func NewZoneSession(zoneID string, conn Sendable, device dispatch.DeviceModel) *ZoneSession {
	handler := dispatch.NewProtocolHandler(device)
	handler.SetZoneID(zoneID)

	// Create notification dispatcher for subscription heartbeats and coalescing.
	// Attribute changes are routed here by DeviceService.notifyZoneSessions
	// (via featureChangeSubscriber), which covers both protocol Writes and
	// internal SetAttributeInternal calls (e.g. from TriggerTestEvent).
	dispatcher := dispatch.NewNotificationDispatcher(handler)
	dispatcher.SetProcessingInterval(100 * time.Millisecond)
	connSender := func(data []byte) error { return conn.Send(data) }
	dispatcherConnID := dispatcher.RegisterConnection(connSender)
	dispatcher.Start()

	// Create client for bidirectional communication
	sender := NewTransportRequestSender(conn)
	client := interaction.NewClient(sender)

	return &ZoneSession{
		zoneID:           zoneID,
		conn:             conn,
		handler:          handler,
		dispatcher:       dispatcher,
		dispatcherConnID: dispatcherConnID,
		client:           client,
		sender:           sender,
	}
}

// ZoneID returns the session's zone ID.
func (s *ZoneSession) ZoneID() string {
	return s.zoneID
}

// OnMessage handles an incoming message from the connection.
// This is called by the transport layer when data is received.
func (s *ZoneSession) OnMessage(data []byte) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	renewalHandler := s.renewalHandler
	client := s.client
	logger := s.logger
	protocolLogger := s.protocolLogger
	connID := s.connID
	snapshot := s.snapshot
	s.mu.RUnlock()

	// Check for renewal messages first (MsgType 30-33 at key 1)
	if isRenewalMessage(data) {
		if renewalHandler != nil {
			s.handleRenewalMessage(data, renewalHandler)
		}
		return
	}

	// Determine message type
	msgType, err := wire.PeekMessageType(data)
	if err != nil {
		// Invalid CBOR - send error response (messageID 0 since we couldn't parse it)
		s.sendErrorResponse(0, wire.StatusInvalidParameter, "invalid CBOR: "+err.Error())
		return
	}

	switch msgType {
	case wire.MessageTypeRequest:
		// Incoming request from controller - process through ProtocolHandler
		s.handleRequest(data)
	case wire.MessageTypeResponse:
		// Response to a request we sent - deliver to client
		s.handleResponse(data, client, logger, protocolLogger, connID, snapshot)
	case wire.MessageTypeNotification:
		// Notification from controller (subscription update) - deliver to client
		s.handleNotification(data, client, logger, protocolLogger, connID, snapshot)
	default:
		// Unknown message type - ignore
	}
}

// handleRequest processes an incoming request and sends a response.
func (s *ZoneSession) handleRequest(data []byte) {
	// Decode request
	req, err := wire.DecodeRequest(data)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("handleRequest: decode failed", "zoneID", s.zoneID, "error", err)
		}
		// Send error response with messageID 0 (unknown).
		// Include details from the decode/validation error.
		s.sendErrorResponse(0, wire.StatusInvalidParameter, "Missing required field: "+err.Error())
		return
	}

	if s.logger != nil {
		s.logger.Debug("handleRequest: processing",
			"zoneID", s.zoneID,
			"messageID", req.MessageID,
			"op", req.Operation,
			"endpoint", req.EndpointID,
			"feature", req.FeatureID)
	}

	// Intercept subscribe/unsubscribe: delegate to dispatcher for
	// heartbeat, coalescing, and per-subscription notification delivery.
	var resp *wire.Response
	if req.Operation == wire.OpSubscribe && s.dispatcher != nil {
		if req.FeatureID == 0 {
			resp = s.dispatcher.HandleUnsubscribe(s.dispatcherConnID, req)
		} else {
			resp = s.dispatcher.HandleSubscribe(s.dispatcherConnID, req)
		}
	} else {
		resp = s.handler.HandleRequest(req)
	}

	if s.logger != nil {
		s.logger.Debug("handleRequest: response ready",
			"zoneID", s.zoneID,
			"messageID", resp.MessageID,
			"status", resp.Status)
	}

	// Send response
	respData, err := wire.EncodeResponse(resp)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("handleRequest: encode failed", "zoneID", s.zoneID, "error", err)
		}
		// Can't encode response - send error with simpler payload
		s.sendErrorResponse(req.MessageID, wire.StatusBusy, "failed to encode response")
		return
	}

	if sendErr := s.conn.Send(respData); sendErr != nil {
		if s.logger != nil {
			s.logger.Debug("handleRequest: Send failed", "zoneID", s.zoneID, "error", sendErr)
		}
	} else if s.logger != nil {
		s.logger.Debug("handleRequest: response sent", "zoneID", s.zoneID, "len", len(respData))
	}
}

// sendErrorResponse sends an error response.
func (s *ZoneSession) sendErrorResponse(messageID uint32, status wire.Status, message string) {
	resp := &wire.Response{
		MessageID: messageID,
		Status:    status,
		Payload: &wire.ErrorPayload{
			Message: message,
		},
	}

	if respData, err := wire.EncodeResponse(resp); err == nil {
		s.conn.Send(respData)
	}
}

// handleResponse processes a response to a request we sent to the controller.
func (s *ZoneSession) handleResponse(data []byte, client *interaction.Client, logger *slog.Logger, protocolLogger log.Logger, connID string, snapshot *snapshotTracker) {
	resp, err := wire.DecodeResponse(data)
	if err != nil {
		if logger != nil {
			logger.Debug("handleResponse: failed to decode response",
				"zoneID", s.zoneID,
				"error", err)
		}
		return
	}
	// Log the incoming response
	s.logIncomingResponse(protocolLogger, connID, resp)
	if snapshot != nil {
		snapshot.onMessageLogged()
	}
	client.HandleResponse(resp)
}

// handleNotification processes a notification from the controller (subscription update).
func (s *ZoneSession) handleNotification(data []byte, client *interaction.Client, logger *slog.Logger, protocolLogger log.Logger, connID string, snapshot *snapshotTracker) {
	notif, err := wire.DecodeNotification(data)
	if err != nil {
		if logger != nil {
			logger.Debug("handleNotification: failed to decode notification",
				"zoneID", s.zoneID,
				"error", err)
		}
		return
	}
	// Log the incoming notification
	s.logIncomingNotification(protocolLogger, connID, notif)
	if snapshot != nil {
		snapshot.onMessageLogged()
	}
	if logger != nil {
		logger.Debug("handleNotification: received notification from controller",
			"zoneID", s.zoneID,
			"subscriptionID", notif.SubscriptionID,
			"endpointID", notif.EndpointID,
			"featureID", notif.FeatureID,
			"changesCount", len(notif.Changes))
	}
	client.HandleNotification(notif)
}

// logIncomingResponse logs an incoming response event.
func (s *ZoneSession) logIncomingResponse(logger log.Logger, connectionID string, resp *wire.Response) {
	if logger == nil {
		return
	}

	status := resp.Status

	logger.Log(log.Event{
		Timestamp:    time.Now(),
		ConnectionID: connectionID,
		Direction:    log.DirectionIn,
		Layer:        log.LayerWire,
		Category:     log.CategoryMessage,
		Message: &log.MessageEvent{
			Type:      log.MessageTypeResponse,
			MessageID: resp.MessageID,
			Status:    &status,
			Payload:   resp.Payload,
		},
	})
}

// logIncomingNotification logs an incoming notification event.
func (s *ZoneSession) logIncomingNotification(logger log.Logger, connectionID string, notif *wire.Notification) {
	if logger == nil {
		return
	}

	subscriptionID := notif.SubscriptionID
	endpointID := notif.EndpointID
	featureID := notif.FeatureID

	logger.Log(log.Event{
		Timestamp:    time.Now(),
		ConnectionID: connectionID,
		Direction:    log.DirectionIn,
		Layer:        log.LayerWire,
		Category:     log.CategoryMessage,
		Message: &log.MessageEvent{
			Type:           log.MessageTypeNotification,
			SubscriptionID: &subscriptionID,
			EndpointID:     &endpointID,
			FeatureID:      &featureID,
			Payload:        notif.Changes,
		},
	})
}

// SendNotification sends a subscription notification to the zone.
func (s *ZoneSession) SendNotification(notif *wire.Notification) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		if s.logger != nil {
			s.logger.Debug("SendNotification: session closed",
				"zoneID", s.zoneID,
				"subscriptionID", notif.SubscriptionID)
		}
		return ErrSessionClosed
	}
	logger := s.logger
	s.mu.RUnlock()

	if logger != nil {
		logger.Debug("SendNotification: encoding notification",
			"zoneID", s.zoneID,
			"subscriptionID", notif.SubscriptionID,
			"endpointID", notif.EndpointID,
			"featureID", notif.FeatureID,
			"changesCount", len(notif.Changes))
	}

	data, err := wire.EncodeNotification(notif)
	if err != nil {
		if logger != nil {
			logger.Debug("SendNotification: encode failed",
				"zoneID", s.zoneID,
				"error", err)
		}
		return err
	}

	if logger != nil {
		logger.Debug("SendNotification: sending",
			"zoneID", s.zoneID,
			"dataLen", len(data))
	}

	return s.conn.Send(data)
}

// SetZoneType sets the zone type on the underlying protocol handler.
func (s *ZoneSession) SetZoneType(zt cert.ZoneType) {
	s.handler.SetPeerZoneType(zt)
}

// SetLogger sets the logger for this session.
func (s *ZoneSession) SetLogger(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// SetSnapshotPolicy sets the policy for periodic capability snapshot emission.
// Must be called before SetProtocolLogger to take effect.
func (s *ZoneSession) SetSnapshotPolicy(policy SnapshotPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotPolicy = policy
}

// SetProtocolLogger sets the protocol logger and connection ID.
// Events logged will include the connectionID for correlation.
// This also sets the logger on the embedded ProtocolHandler.
func (s *ZoneSession) SetProtocolLogger(logger log.Logger, connectionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocolLogger = logger
	s.connID = connectionID
	// Also set on the protocol handler for request/response logging
	s.handler.SetLogger(logger, connectionID)

	// Create snapshot tracker and emit initial snapshot
	device := s.handler.Device()
	s.snapshot = newSnapshotTracker(s.snapshotPolicy, device, logger, connectionID, log.RoleDevice)
	s.handler.SetOnMessageLogged(s.snapshot.onMessageLogged)
	s.snapshot.emitInitialSnapshot()
}

// SubscriptionCount returns the number of active subscriptions.
func (s *ZoneSession) SubscriptionCount() int {
	if s.dispatcher != nil {
		return s.dispatcher.SubscriptionCount()
	}
	return s.handler.SubscriptionCount()
}

// ClearSubscriptions removes all inbound subscriptions from this session.
// Unlike Close() which tears down the entire session, this only clears
// subscriptions while keeping the session alive. Used by TriggerResetTestState
// to prevent subscription leakage between tests.
func (s *ZoneSession) ClearSubscriptions() {
	if s.dispatcher != nil {
		s.dispatcher.UnregisterConnection(s.dispatcherConnID)
		// Re-register so the dispatcher is ready for new subscriptions
		connSender := func(data []byte) error { return s.conn.Send(data) }
		s.dispatcherConnID = s.dispatcher.RegisterConnection(connSender)
	} else {
		s.handler.SessionSubscriptions().ClearInbound()
	}
}

// SetOnWrite sets the callback for write operations.
// The callback receives the endpoint ID, feature ID, and written attributes.
// Note: subscription notifications are handled by DeviceService.notifyZoneSessions
// (via featureChangeSubscriber), not by this callback.
func (s *ZoneSession) SetOnWrite(cb dispatch.WriteCallback) {
	s.handler.SetOnWrite(cb)
}

// SetOnInvoke sets the callback for invoke operations.
// The callback receives the endpoint ID, feature ID, command ID, parameters, and result.
func (s *ZoneSession) SetOnInvoke(cb dispatch.InvokeCallback) {
	s.handler.SetOnInvoke(cb)
}

// Close closes the session and cleans up resources.
func (s *ZoneSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true

	// Stop the notification dispatcher
	if s.dispatcher != nil {
		s.dispatcher.Stop()
	}

	// Close the interaction client (cancels pending requests)
	if s.client != nil {
		s.client.Close()
	}

	// Clear all subscriptions for this zone
	s.clearSubscriptions()
}

// clearSubscriptions removes all subscriptions from the handler.
// This is called when the session is closed.
func (s *ZoneSession) clearSubscriptions() {
	if s.dispatcher != nil {
		s.dispatcher.UnregisterConnection(s.dispatcherConnID)
		return
	}
	// Fallback: recreate handler to clear subscriptions
	device := s.handler.Device()
	s.handler = dispatch.NewProtocolHandler(device)
	s.handler.SetZoneID(s.zoneID)
}

// SetRenewalHandler sets the handler for certificate renewal messages.
func (s *ZoneSession) SetRenewalHandler(handler *DeviceRenewalHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewalHandler = handler
}

// handleRenewalMessage processes certificate renewal messages.
func (s *ZoneSession) handleRenewalMessage(data []byte, handler *DeviceRenewalHandler) {
	msg, err := commissioning.DecodeRenewalMessage(data)
	if err != nil {
		return
	}

	var resp any
	var installSuccess bool

	switch m := msg.(type) {
	case *commissioning.CertRenewalRequest:
		resp, err = handler.HandleRenewalRequest(m)
	case *commissioning.CertRenewalInstall:
		resp, err = handler.HandleCertInstall(m)
		// Check if installation was successful
		if ack, ok := resp.(*commissioning.CertRenewalAck); ok && ack.Status == commissioning.RenewalStatusSuccess {
			installSuccess = true
		}
	default:
		// Unknown renewal message type
		return
	}

	if err != nil {
		return
	}

	// Send response
	respData, err := commissioning.EncodeRenewalMessage(resp)
	if err != nil {
		return
	}

	s.conn.Send(respData)

	// Notify callback after successful certificate installation
	if installSuccess {
		s.mu.RLock()
		callback := s.onCertRenewalSuccess
		zoneID := s.zoneID
		s.mu.RUnlock()

		if callback != nil {
			callback(zoneID, handler)
		}
	}
}

// isRenewalMessage checks if data is a renewal message (MsgType 30-33).
func isRenewalMessage(data []byte) bool {
	// Renewal messages use CBOR key 1 as MsgType (30-33) and have 2-3 keys.
	// Regular requests also use key 1 (as messageID) which can collide when
	// the messageID counter reaches 30-33. To distinguish them, verify that
	// key 4 (featureID, present in all requests) is absent -- renewal messages
	// never have key 4.
	var raw map[uint64]any
	if err := wire.Unmarshal(data, &raw); err != nil {
		return false
	}
	// Requests always have key 4 (featureID); renewal messages never do.
	if _, hasKey4 := raw[4]; hasKey4 {
		return false
	}
	// Now safe to try full decode.
	msg, err := commissioning.DecodeRenewalMessage(data)
	if err != nil {
		return false
	}
	msgType := commissioning.RenewalMessageType(msg)
	return msgType >= commissioning.MsgCertRenewalRequest && msgType <= commissioning.MsgCertRenewalAck
}

// InitializeRenewalHandler creates and sets a renewal handler with the given identity.
func (s *ZoneSession) InitializeRenewalHandler(identity *cert.DeviceIdentity) {
	handler := NewDeviceRenewalHandler(identity)
	s.SetRenewalHandler(handler)
}

// RenewalHandler returns the session's renewal handler.
func (s *ZoneSession) RenewalHandler() *DeviceRenewalHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renewalHandler
}

// SetOnCertRenewalSuccess sets a callback to be invoked when certificate renewal succeeds.
// The callback receives the zone ID and the renewal handler (to access the new cert/key).
func (s *ZoneSession) SetOnCertRenewalSuccess(callback func(zoneID string, handler *DeviceRenewalHandler)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCertRenewalSuccess = callback
}

// ============================================================================
// Bidirectional Support: Methods for sending requests to controller
// ============================================================================

// Read reads attributes from a feature on the controller.
// If attrIDs is nil or empty, all attributes are read.
func (s *ZoneSession) Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Read(ctx, endpointID, featureID, attrIDs)
}

// Write writes attributes to a feature on the controller.
func (s *ZoneSession) Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Write(ctx, endpointID, featureID, attrs)
}

// Subscribe subscribes to attribute changes on a controller feature.
// Returns the subscription ID and initial attribute values (priming report).
func (s *ZoneSession) Subscribe(ctx context.Context, endpointID uint8, featureID uint8, opts *interaction.SubscribeOptions) (uint32, map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Subscribe(ctx, endpointID, featureID, opts)
}

// Unsubscribe cancels a subscription to a controller feature.
func (s *ZoneSession) Unsubscribe(ctx context.Context, subscriptionID uint32) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Unsubscribe(ctx, subscriptionID)
}

// Invoke executes a command on a controller feature.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (s *ZoneSession) Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Invoke(ctx, endpointID, featureID, commandID, params)
}

// SetNotificationHandler sets the handler for incoming notifications from the controller.
// This is called when the controller sends subscription updates.
func (s *ZoneSession) SetNotificationHandler(handler func(*wire.Notification)) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	client := s.client
	s.mu.RUnlock()

	client.SetNotificationHandler(handler)
}

// SetTimeout sets the timeout for requests sent to the controller.
func (s *ZoneSession) SetTimeout(timeout time.Duration) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	client := s.client
	s.mu.RUnlock()

	client.SetTimeout(timeout)
}
