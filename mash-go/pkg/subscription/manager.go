package subscription

import (
	"sync"
	"time"
)

// Notification represents a subscription notification to send.
type Notification struct {
	// SubscriptionID identifies the subscription.
	SubscriptionID uint32

	// FeatureID identifies the feature.
	FeatureID uint16

	// EndpointID identifies the endpoint.
	EndpointID uint16

	// Attributes maps attribute IDs to their values.
	Attributes map[uint16]any

	// IsPriming indicates this is the initial priming notification.
	IsPriming bool

	// IsHeartbeat indicates this is a heartbeat notification.
	IsHeartbeat bool

	// Timestamp is when the notification was generated.
	Timestamp time.Time
}

// Manager manages subscriptions for a device.
type Manager struct {
	mu sync.RWMutex

	// Configuration
	config Config

	// Active subscriptions by ID
	subscriptions map[uint32]*Subscription

	// Index by (endpointID, featureID) for efficient change dispatch
	featureIndex map[featureKey][]*Subscription

	// Callbacks
	onNotification func(Notification)
}

// featureKey is a composite key for the feature index.
type featureKey struct {
	endpointID uint16
	featureID  uint16
}

// NewManager creates a new subscription manager with default configuration.
func NewManager() *Manager {
	return NewManagerWithConfig(DefaultConfig())
}

// NewManagerWithConfig creates a new subscription manager with custom configuration.
func NewManagerWithConfig(config Config) *Manager {
	if config.MaxSubscriptions <= 0 {
		config.MaxSubscriptions = DefaultMaxSubscriptions
	}
	if config.MaxAttributesPerSub <= 0 {
		config.MaxAttributesPerSub = DefaultMaxAttributesPerSub
	}

	return &Manager{
		config:        config,
		subscriptions: make(map[uint32]*Subscription),
		featureIndex:  make(map[featureKey][]*Subscription),
	}
}

// Subscribe creates a new subscription and returns the subscription ID.
//
// Per DEC-086 the priming report lives ONLY in the Subscribe Response
// payload that the caller builds — this method does NOT fire a separate
// priming Notification through the OnNotification callback. The
// returned subscription is seeded with the priming values so subsequent
// bounce-back checks compare against the values the controller has
// already received in the Subscribe Response.
func (m *Manager) Subscribe(
	endpointID, featureID uint16,
	attributeIDs []uint16,
	minInterval, maxInterval time.Duration,
	currentValues map[uint16]any,
) (uint32, error) {
	// Validate intervals
	if maxInterval == 0 {
		return 0, ErrInvalidInterval
	}
	if minInterval > maxInterval {
		if m.config.AutoCorrectIntervals {
			minInterval, maxInterval = maxInterval, minInterval
		} else {
			return 0, ErrInvalidInterval
		}
	}

	// Validate attribute count
	if len(attributeIDs) > m.config.MaxAttributesPerSub {
		return 0, ErrInvalidAttributeID
	}

	m.mu.Lock()

	// Check subscription limit
	if len(m.subscriptions) >= m.config.MaxSubscriptions {
		m.mu.Unlock()
		return 0, ErrResourceExhausted
	}

	// Create subscription
	id := nextID()
	sub := NewSubscription(id, featureID, endpointID, attributeIDs, minInterval, maxInterval)

	// Filter current values to subscribed attributes and seed
	// last-delivered state so bounce-back comparisons after a Subscribe
	// reference what the controller saw in the Subscribe Response.
	primingValues := filterAttributes(currentValues, attributeIDs)
	sub.SetPrimingValues(primingValues)

	// Store subscription
	m.subscriptions[id] = sub

	// Update feature index
	key := featureKey{endpointID: endpointID, featureID: featureID}
	m.featureIndex[key] = append(m.featureIndex[key], sub)

	m.mu.Unlock()

	return id, nil
}

// Unsubscribe removes a subscription.
func (m *Manager) Unsubscribe(subscriptionID uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscriptions[subscriptionID]
	if !exists {
		return ErrSubscriptionNotFound
	}

	sub.Deactivate()
	delete(m.subscriptions, subscriptionID)

	// Remove from feature index
	key := featureKey{endpointID: sub.EndpointID, featureID: sub.FeatureID}
	subs := m.featureIndex[key]
	for i, s := range subs {
		if s.ID == subscriptionID {
			m.featureIndex[key] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(m.featureIndex[key]) == 0 {
		delete(m.featureIndex, key)
	}

	return nil
}

// NotifyChange records a value change for dispatch to relevant subscriptions.
// Changes are coalesced and notifications sent according to subscription intervals.
func (m *Manager) NotifyChange(endpointID, featureID, attrID uint16, value any) {
	m.mu.RLock()
	key := featureKey{endpointID: endpointID, featureID: featureID}
	subs := m.featureIndex[key]
	m.mu.RUnlock()

	for _, sub := range subs {
		sub.RecordChange(attrID, value)
	}
}

// NotifyChanges records multiple value changes at once.
func (m *Manager) NotifyChanges(endpointID, featureID uint16, changes map[uint16]any) {
	m.mu.RLock()
	key := featureKey{endpointID: endpointID, featureID: featureID}
	subs := m.featureIndex[key]
	m.mu.RUnlock()

	for _, sub := range subs {
		for attrID, value := range changes {
			sub.RecordChange(attrID, value)
		}
	}
}

// ProcessNotifications checks all subscriptions and sends pending notifications.
// This should be called periodically (e.g., every second).
func (m *Manager) ProcessNotifications() {
	m.mu.RLock()
	subs := make([]*Subscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, sub)
	}
	onNotify := m.onNotification
	config := m.config
	m.mu.RUnlock()

	if onNotify == nil {
		return
	}

	for _, sub := range subs {
		// Check for pending changes
		if attrs := sub.GetPendingNotification(config.SuppressBounceBack); attrs != nil {
			onNotify(Notification{
				SubscriptionID: sub.ID,
				FeatureID:      sub.FeatureID,
				EndpointID:     sub.EndpointID,
				Attributes:     attrs,
				Timestamp:      time.Now(),
			})
		}

		// Check for heartbeat. DEC-086: heartbeat MUST carry the full
		// current values of subscribed attributes (interaction-model.md
		// §6.5). The empty-body variant the codebase used to support
		// has been removed.
		if sub.NeedsHeartbeat() {
			sub.mu.RLock()
			attrs := make(map[uint16]any, len(sub.lastValues))
			for k, v := range sub.lastValues {
				attrs[k] = v
			}
			sub.mu.RUnlock()

			sub.RecordHeartbeat()
			onNotify(Notification{
				SubscriptionID: sub.ID,
				FeatureID:      sub.FeatureID,
				EndpointID:     sub.EndpointID,
				Attributes:     attrs,
				IsHeartbeat:    true,
				Timestamp:      time.Now(),
			})
		}
	}
}

// ClearAll removes all subscriptions (e.g., on connection loss).
func (m *Manager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sub := range m.subscriptions {
		sub.Deactivate()
	}
	m.subscriptions = make(map[uint32]*Subscription)
	m.featureIndex = make(map[featureKey][]*Subscription)
}

// Count returns the number of active subscriptions.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subscriptions)
}

// Get returns a subscription by ID.
func (m *Manager) Get(subscriptionID uint32) (*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sub, exists := m.subscriptions[subscriptionID]
	if !exists {
		return nil, ErrSubscriptionNotFound
	}
	return sub, nil
}

// OnNotification sets the callback for notifications.
func (m *Manager) OnNotification(fn func(Notification)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onNotification = fn
}

// filterAttributes returns only the attributes that are subscribed.
// If attributeIDs is empty, all attributes are included.
func filterAttributes(values map[uint16]any, attributeIDs []uint16) map[uint16]any {
	if len(attributeIDs) == 0 {
		// All attributes subscribed
		result := make(map[uint16]any, len(values))
		for k, v := range values {
			result[k] = v
		}
		return result
	}

	// Filter to subscribed attributes
	result := make(map[uint16]any)
	for _, id := range attributeIDs {
		if v, exists := values[id]; exists {
			result[id] = v
		}
	}
	return result
}
