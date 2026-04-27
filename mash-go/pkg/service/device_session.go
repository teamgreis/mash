package service

import (
	"context"
	"crypto/tls"
	"log/slog"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
	"github.com/mash-protocol/mash-go/pkg/interaction"
	"github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/service/dispatch"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// DeviceSession manages a controller-side session with a connected device.
// It wraps an interaction.Client to provide Read/Write/Subscribe/Invoke
// operations to applications.
//
// DeviceSession also supports bidirectional communication, allowing devices
// to send requests to the controller (e.g., to read meter data exposed by
// the controller). This is enabled by calling SetExposedDevice with a device
// model that the controller wants to expose.
type DeviceSession struct {
	mu sync.RWMutex

	deviceID string
	conn     Sendable
	client   *interaction.Client
	sender   *TransportRequestSender
	closed   bool
	logger   *slog.Logger

	// Protocol logging (optional)
	protocolLogger log.Logger
	connID         string

	// Capability snapshot tracking (optional)
	snapshotPolicy SnapshotPolicy
	snapshot       *snapshotTracker

	// Bidirectional support: handler for incoming requests from the device.
	// Optional - only set if controller exposes features to devices.
	handler *dispatch.ProtocolHandler

	// Renewal handling
	renewalHandler *ControllerRenewalHandler
}

// NewDeviceSession creates a new device session.
func NewDeviceSession(deviceID string, conn Sendable) *DeviceSession {
	sender := NewTransportRequestSender(conn)
	client := interaction.NewClient(sender)

	return &DeviceSession{
		deviceID: deviceID,
		conn:     conn,
		client:   client,
		sender:   sender,
	}
}

// DeviceID returns the session's device ID.
func (s *DeviceSession) DeviceID() string {
	return s.deviceID
}

// OnMessage handles an incoming message from the device.
// This is called by the transport layer when data is received.
func (s *DeviceSession) OnMessage(data []byte) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	client := s.client
	logger := s.logger
	protocolLogger := s.protocolLogger
	connID := s.connID
	snapshot := s.snapshot
	renewalHandler := s.renewalHandler
	s.mu.RUnlock()

	// Check for renewal response messages first (MsgType 31 or 33)
	if isRenewalMessage(data) {
		if renewalHandler != nil {
			s.handleRenewalResponse(data, renewalHandler)
		}
		return
	}

	// Determine message type
	msgType, err := wire.PeekMessageType(data)
	if err != nil {
		if logger != nil {
			logger.Debug("OnMessage: failed to peek message type",
				"deviceID", s.deviceID,
				"error", err)
		}
		// Invalid CBOR - send error response
		s.sendErrorResponse(0, wire.StatusInvalidParameter, "invalid CBOR: "+err.Error())
		return
	}

	switch msgType {
	case wire.MessageTypeRequest:
		// Incoming request from device - process through ProtocolHandler
		s.handleRequest(data)

	case wire.MessageTypeResponse:
		// Decode and deliver to client
		resp, err := wire.DecodeResponse(data)
		if err != nil {
			if logger != nil {
				logger.Debug("OnMessage: failed to decode response",
					"deviceID", s.deviceID,
					"error", err)
			}
			return
		}
		// Log the incoming response
		s.logResponse(protocolLogger, connID, resp)
		if snapshot != nil {
			snapshot.onMessageLogged()
		}
		client.HandleResponse(resp)

	case wire.MessageTypeNotification:
		// Decode and deliver to client
		notif, err := wire.DecodeNotification(data)
		if err != nil {
			if logger != nil {
				logger.Debug("OnMessage: failed to decode notification",
					"deviceID", s.deviceID,
					"error", err)
			}
			return
		}
		// Log the incoming notification
		s.logNotification(protocolLogger, connID, notif)
		if snapshot != nil {
			snapshot.onMessageLogged()
		}
		if logger != nil {
			logger.Debug("OnMessage: received notification",
				"deviceID", s.deviceID,
				"subscriptionID", notif.SubscriptionID,
				"endpointID", notif.EndpointID,
				"featureID", notif.FeatureID,
				"changesCount", len(notif.Changes))
			for attrID, val := range notif.Changes {
				logger.Debug("OnMessage: notification change",
					"attrID", attrID,
					"valueType", slog.AnyValue(val).Kind().String(),
					"value", val)
			}
		}
		client.HandleNotification(notif)
	}
}

// logResponse logs an incoming response event.
func (s *DeviceSession) logResponse(logger log.Logger, connectionID string, resp *wire.Response) {
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

// logNotification logs an incoming notification event.
func (s *DeviceSession) logNotification(logger log.Logger, connectionID string, notif *wire.Notification) {
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

// SetLogger sets the logger for this session.
func (s *DeviceSession) SetLogger(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// SetSnapshotPolicy sets the policy for periodic capability snapshot emission.
// Must be called before SetProtocolLogger to take effect.
func (s *DeviceSession) SetSnapshotPolicy(policy SnapshotPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotPolicy = policy
}

// SetProtocolLogger sets the protocol logger and connection ID.
// Events logged will include the connectionID for correlation.
func (s *DeviceSession) SetProtocolLogger(logger log.Logger, connectionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protocolLogger = logger
	s.connID = connectionID

	// Create snapshot tracker and emit initial snapshot.
	// For controller-side sessions, localDevice is nil unless SetExposedDevice is called.
	s.snapshot = newSnapshotTracker(s.snapshotPolicy, nil, logger, connectionID, log.RoleController)
	if s.handler != nil {
		s.handler.SetOnMessageLogged(s.snapshot.onMessageLogged)
	}
	s.snapshot.emitInitialSnapshot()
}

// Read reads attributes from a feature on the device.
// If attrIDs is nil, all attributes are read.
func (s *DeviceSession) Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Read(ctx, endpointID, featureID, attrIDs)
}

// Write writes attributes to a feature on the device.
func (s *DeviceSession) Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Write(ctx, endpointID, featureID, attrs)
}

// Subscribe subscribes to attribute changes on a feature.
// Returns the subscription ID and initial attribute values (priming report).
func (s *DeviceSession) Subscribe(ctx context.Context, endpointID uint8, featureID uint8, opts *interaction.SubscribeOptions) (uint32, map[uint16]any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return 0, nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Subscribe(ctx, endpointID, featureID, opts)
}

// Unsubscribe cancels a subscription.
func (s *DeviceSession) Unsubscribe(ctx context.Context, subscriptionID uint32) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Unsubscribe(ctx, subscriptionID)
}

// Invoke executes a command on a feature.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (s *DeviceSession) Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrSessionClosed
	}
	client := s.client
	s.mu.RUnlock()

	return client.Invoke(ctx, endpointID, featureID, commandID, params)
}

// SetNotificationHandler sets the handler for incoming notifications.
func (s *DeviceSession) SetNotificationHandler(handler func(*wire.Notification)) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	client := s.client
	s.mu.RUnlock()

	client.SetNotificationHandler(handler)
}

// Close closes the session and cleans up resources.
func (s *DeviceSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	return s.client.Close()
}

// SetRenewalHandler sets the handler for certificate renewal.
func (s *DeviceSession) SetRenewalHandler(handler *ControllerRenewalHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewalHandler = handler
}

// RenewalHandler returns the session's renewal handler.
func (s *DeviceSession) RenewalHandler() *ControllerRenewalHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renewalHandler
}

// handleRenewalResponse processes a renewal response from the device.
func (s *DeviceSession) handleRenewalResponse(data []byte, handler *ControllerRenewalHandler) {
	msg, err := commissioning.DecodeRenewalMessage(data)
	if err != nil {
		return
	}

	// Route to the handler's response channel
	handler.HandleResponse(msg)
}

// Conn returns the underlying connection (for renewal handler initialization).
func (s *DeviceSession) Conn() Sendable {
	return s.conn
}

// ============================================================================
// Bidirectional Support: Methods for handling incoming requests from device
// ============================================================================

// SetExposedDevice configures this session to expose a device model to the
// connected device. This enables bidirectional communication where the device
// can query the controller's features (e.g., read meter data from an SMGW).
//
// If not called, incoming requests from the device will receive StatusUnsupported.
func (s *DeviceSession) SetExposedDevice(device dispatch.DeviceModel) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.handler = dispatch.NewProtocolHandler(device)
	s.handler.SetPeerID(s.deviceID)

	// Wire up notification sender so NotifyAttributeChange can send to device
	s.handler.SetSendNotification(s.SendNotification)

	// Propagate logger and snapshot tracker to the new handler
	if s.protocolLogger != nil {
		s.handler.SetLogger(s.protocolLogger, s.connID)
	}
	if s.snapshot != nil {
		s.handler.SetOnMessageLogged(s.snapshot.onMessageLogged)
	}
}

// SetRemoteSnapshotCache updates the cached remote device snapshot on the
// snapshot tracker. Subsequent snapshot emissions will include this data.
func (s *DeviceSession) SetRemoteSnapshotCache(snap *log.DeviceSnapshot) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot != nil {
		s.snapshot.setRemoteCache(snap)
	}
}

// handleRequest processes an incoming request from the device and sends a response.
func (s *DeviceSession) handleRequest(data []byte) {
	s.mu.RLock()
	handler := s.handler
	logger := s.logger
	s.mu.RUnlock()

	// Decode request
	req, err := wire.DecodeRequest(data)
	if err != nil {
		// Send error response with messageID 0 (unknown)
		s.sendErrorResponse(0, wire.StatusInvalidParameter, "failed to decode request")
		return
	}

	var resp *wire.Response
	if handler == nil {
		// No handler configured - controller doesn't expose features
		resp = &wire.Response{
			MessageID: req.MessageID,
			Status:    wire.StatusUnsupported,
			Payload: &wire.ErrorPayload{
				Message: "controller does not expose features",
			},
		}
	} else {
		// Process request through handler
		resp = handler.HandleRequest(req)
	}

	if logger != nil {
		logger.Debug("handleRequest: processed request",
			"deviceID", s.deviceID,
			"messageID", req.MessageID,
			"operation", req.Operation,
			"status", resp.Status)
	}

	// Send response
	respData, err := wire.EncodeResponse(resp)
	if err != nil {
		// Can't encode response - send error with simpler payload
		s.sendErrorResponse(req.MessageID, wire.StatusBusy, "failed to encode response")
		return
	}

	s.conn.Send(respData)
}

// sendErrorResponse sends an error response.
func (s *DeviceSession) sendErrorResponse(messageID uint32, status wire.Status, message string) {
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

// SendNotification sends a subscription notification to the device.
// This is used when the controller has features that the device has subscribed to.
func (s *DeviceSession) SendNotification(notif *wire.Notification) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSessionClosed
	}
	logger := s.logger
	s.mu.RUnlock()

	if logger != nil {
		logger.Debug("SendNotification: encoding notification",
			"deviceID", s.deviceID,
			"subscriptionID", notif.SubscriptionID,
			"endpointID", notif.EndpointID,
			"featureID", notif.FeatureID,
			"changesCount", len(notif.Changes))
	}

	data, err := wire.EncodeNotification(notif)
	if err != nil {
		if logger != nil {
			logger.Debug("SendNotification: encode failed",
				"deviceID", s.deviceID,
				"error", err)
		}
		return err
	}

	return s.conn.Send(data)
}

// Handler returns the session's ProtocolHandler (for testing and diagnostics).
// Returns nil if no exposed device is configured.
func (s *DeviceSession) Handler() *dispatch.ProtocolHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler
}

// TLSConnectionState returns the TLS connection state for this session.
// Returns nil if the session is closed or connection is not TLS.
func (s *DeviceSession) TLSConnectionState() *tls.ConnectionState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil
	}

	// Try to get TLS state from framed connection
	if fc, ok := s.conn.(*framedConnection); ok {
		return fc.TLSConnectionState()
	}
	return nil
}
