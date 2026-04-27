package interaction

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mash-protocol/mash-go/pkg/wire"
)

// Client errors.
var (
	ErrRequestTimeout  = errors.New("request timed out")
	ErrClientClosed    = errors.New("client is closed")
	ErrUnexpectedReply = errors.New("unexpected reply")
)

// RequestSender is the interface for sending requests over a connection.
type RequestSender interface {
	// SendRequest sends a request and returns the raw response bytes.
	Send(data []byte) error
}

// Client provides a high-level API for making MASH requests.
type Client struct {
	mu sync.RWMutex

	sender  RequestSender
	timeout time.Duration

	// Message ID generator
	nextMsgID uint32

	// Pending requests awaiting responses
	pending   map[uint32]chan *wire.Response
	pendingMu sync.RWMutex

	// Notification handler
	notifyHandler func(*wire.Notification)

	closed bool
}

// DefaultRequestTimeout is the default timeout for requests (per DEC-044).
const DefaultRequestTimeout = 10 * time.Second

// NewClient creates a new interaction client.
func NewClient(sender RequestSender) *Client {
	return &Client{
		sender:  sender,
		timeout: DefaultRequestTimeout,
		// Start at 0 so the first atomic.AddUint32 returns 1 per
		// docs/interaction-model.md §3.5 ("Start at 1 for new connections").
		nextMsgID: 0,
		pending:   make(map[uint32]chan *wire.Response),
	}
}

// SetTimeout sets the request timeout.
func (c *Client) SetTimeout(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = timeout
}

// SetNotificationHandler sets the handler for incoming notifications.
func (c *Client) SetNotificationHandler(handler func(*wire.Notification)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyHandler = handler
}

// Close closes the client.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true

	// Cancel all pending requests
	c.pendingMu.Lock()
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = make(map[uint32]chan *wire.Response)
	c.pendingMu.Unlock()

	return nil
}

// nextMessageID generates the next unique message ID.
// MessageID 0 is reserved for notifications, so we skip it on wraparound (per DEC-044).
func (c *Client) nextMessageID() uint32 {
	for {
		id := atomic.AddUint32(&c.nextMsgID, 1)
		if id != wire.NotificationMessageID {
			return id
		}
		// Wrapped to 0, try again to get 1
	}
}

// sendRequest sends a request and waits for the response.
func (c *Client) sendRequest(ctx context.Context, req *wire.Request) (*wire.Response, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, ErrClientClosed
	}
	timeout := c.timeout
	c.mu.RUnlock()

	// Create response channel
	respCh := make(chan *wire.Response, 1)

	c.pendingMu.Lock()
	c.pending[req.MessageID] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, req.MessageID)
		c.pendingMu.Unlock()
	}()

	// Encode and send request
	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, err
	}

	if err := c.sender.Send(data); err != nil {
		return nil, err
	}

	// Wait for response with timeout
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(timeout):
		return nil, ErrRequestTimeout
	case resp, ok := <-respCh:
		if !ok {
			return nil, ErrClientClosed
		}
		return resp, nil
	}
}

// HandleResponse should be called when a response is received.
func (c *Client) HandleResponse(resp *wire.Response) error {
	c.pendingMu.RLock()
	ch, exists := c.pending[resp.MessageID]
	c.pendingMu.RUnlock()

	if !exists {
		return ErrUnexpectedReply
	}

	select {
	case ch <- resp:
	default:
		// Channel full or closed
	}
	return nil
}

// HandleNotification should be called when a notification is received.
func (c *Client) HandleNotification(notif *wire.Notification) {
	c.mu.RLock()
	handler := c.notifyHandler
	c.mu.RUnlock()

	if handler != nil {
		handler(notif)
	}
}

// Read reads attributes from a feature.
// If attrIDs is nil or empty, all attributes are read.
func (c *Client) Read(ctx context.Context, endpointID uint8, featureID uint8, attrIDs []uint16) (map[uint16]any, error) {
	req := &wire.Request{
		MessageID:  c.nextMessageID(),
		Operation:  wire.OpRead,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    attrIDs,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	if !resp.Status.IsSuccess() {
		return nil, statusError(resp.Status, resp.Payload)
	}

	// Parse response payload
	switch p := resp.Payload.(type) {
	case map[uint16]any:
		return p, nil
	case map[any]any:
		result := make(map[uint16]any, len(p))
		for k, v := range p {
			switch key := k.(type) {
			case uint16:
				result[key] = v
			case uint64:
				result[uint16(key)] = v
			case int64:
				result[uint16(key)] = v
			}
		}
		return result, nil
	default:
		return nil, ErrUnexpectedReply
	}
}

// Write writes attributes to a feature.
func (c *Client) Write(ctx context.Context, endpointID uint8, featureID uint8, attrs map[uint16]any) (map[uint16]any, error) {
	req := &wire.Request{
		MessageID:  c.nextMessageID(),
		Operation:  wire.OpWrite,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    attrs,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	if !resp.Status.IsSuccess() {
		return nil, statusError(resp.Status, resp.Payload)
	}

	// Parse response payload
	switch p := resp.Payload.(type) {
	case map[uint16]any:
		return p, nil
	case map[any]any:
		result := make(map[uint16]any, len(p))
		for k, v := range p {
			switch key := k.(type) {
			case uint16:
				result[key] = v
			case uint64:
				result[uint16(key)] = v
			case int64:
				result[uint16(key)] = v
			}
		}
		return result, nil
	default:
		return nil, nil // Success with no payload
	}
}

// Subscribe subscribes to attribute changes on a feature.
// Returns the subscription ID and the initial attribute values (priming report).
func (c *Client) Subscribe(ctx context.Context, endpointID uint8, featureID uint8, opts *SubscribeOptions) (uint32, map[uint16]any, error) {
	payload := &wire.SubscribePayload{}
	if opts != nil {
		payload.AttributeIDs = opts.AttributeIDs
		payload.MinInterval = uint32(opts.MinInterval.Milliseconds())
		payload.MaxInterval = uint32(opts.MaxInterval.Milliseconds())
	}

	req := &wire.Request{
		MessageID:  c.nextMessageID(),
		Operation:  wire.OpSubscribe,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload:    payload,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return 0, nil, err
	}

	if !resp.Status.IsSuccess() {
		return 0, nil, statusError(resp.Status, resp.Payload)
	}

	// Parse response payload
	switch p := resp.Payload.(type) {
	case *wire.SubscribeResponsePayload:
		return p.SubscriptionID, p.CurrentValues, nil
	case wire.SubscribeResponsePayload:
		return p.SubscriptionID, p.CurrentValues, nil
	case map[any]any:
		var subID uint32
		var values map[uint16]any
		if v, ok := p[uint64(1)].(uint64); ok {
			subID = uint32(v)
		}
		if v, ok := p[uint64(2)].(map[any]any); ok {
			values = make(map[uint16]any, len(v))
			for k, val := range v {
				switch key := k.(type) {
				case uint64:
					values[uint16(key)] = val
				}
			}
		}
		return subID, values, nil
	default:
		return 0, nil, ErrUnexpectedReply
	}
}

// Unsubscribe cancels a subscription.
func (c *Client) Unsubscribe(ctx context.Context, subscriptionID uint32) error {
	req := &wire.Request{
		MessageID:  c.nextMessageID(),
		Operation:  wire.OpSubscribe,
		EndpointID: 0, // Indicates unsubscribe
		FeatureID:  0,
		Payload:    &wire.UnsubscribePayload{SubscriptionID: subscriptionID},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return err
	}

	if !resp.Status.IsSuccess() {
		return statusError(resp.Status, resp.Payload)
	}

	return nil
}

// Invoke executes a command on a feature.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (c *Client) Invoke(ctx context.Context, endpointID uint8, featureID uint8, commandID uint8, params map[uint8]any) (any, error) {
	req := &wire.Request{
		MessageID:  c.nextMessageID(),
		Operation:  wire.OpInvoke,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload: &wire.InvokePayload{
			CommandID:  commandID,
			Parameters: params,
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	if !resp.Status.IsSuccess() {
		return nil, statusError(resp.Status, resp.Payload)
	}

	return resp.Payload, nil
}

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// AttributeIDs limits the subscription to specific attributes.
	// Empty means subscribe to all attributes.
	AttributeIDs []uint16

	// MinInterval is the minimum time between notifications.
	MinInterval time.Duration

	// MaxInterval is the maximum time without a notification (heartbeat).
	MaxInterval time.Duration
}

// StatusError represents an error response from the server.
type StatusError struct {
	Status  wire.Status
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Status.String()
}

// statusError creates an error from a response status.
func statusError(status wire.Status, payload any) error {
	msg := ""
	if ep, ok := payload.(*wire.ErrorPayload); ok {
		msg = ep.Message
	}
	return &StatusError{Status: status, Message: msg}
}
