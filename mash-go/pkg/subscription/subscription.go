package subscription

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Subscription errors.
var (
	ErrInvalidInterval     = errors.New("invalid subscription interval")
	ErrResourceExhausted   = errors.New("maximum subscriptions reached")
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrInvalidAttributeID  = errors.New("invalid attribute ID")
)

// Default subscription limits.
const (
	DefaultMinInterval = 1 * time.Second
	DefaultMaxInterval = 60 * time.Second
	DefaultMaxSubscriptions = 50
	DefaultMaxAttributesPerSub = 100
)

// Config holds subscription manager configuration.
//
// Per DEC-086, heartbeat content is fixed: every heartbeat carries the
// full current values of subscribed attributes (the spec rules out an
// empty-body heartbeat — interaction-model.md §6.5). The earlier
// HeartbeatMode toggle was removed when the spec was tightened.
type Config struct {
	// MaxSubscriptions is the maximum number of subscriptions allowed.
	MaxSubscriptions int

	// MaxAttributesPerSub is the maximum attributes per subscription.
	MaxAttributesPerSub int

	// SuppressBounceBack enables bounce-back suppression. DEC-086
	// makes this MUST behavior; the field is kept for test injection
	// of the off-state but production callers should leave it true.
	SuppressBounceBack bool

	// AutoCorrectIntervals swaps min/max if min > max.
	AutoCorrectIntervals bool
}

// DefaultConfig returns the default subscription configuration.
func DefaultConfig() Config {
	return Config{
		MaxSubscriptions:     DefaultMaxSubscriptions,
		MaxAttributesPerSub:  DefaultMaxAttributesPerSub,
		SuppressBounceBack:   true,
		AutoCorrectIntervals: false,
	}
}

// Subscription represents an active subscription.
type Subscription struct {
	mu sync.RWMutex

	// ID is the unique subscription identifier.
	ID uint32

	// FeatureID identifies the subscribed feature.
	FeatureID uint16

	// EndpointID identifies the endpoint.
	EndpointID uint16

	// AttributeIDs lists subscribed attributes (empty = all).
	AttributeIDs []uint16

	// MinInterval is the minimum time between notifications.
	MinInterval time.Duration

	// MaxInterval is the maximum time without notification (heartbeat).
	MaxInterval time.Duration

	// lastNotified is when the last notification was sent.
	lastNotified time.Time

	// lastValues holds the last notified values for bounce-back detection.
	lastValues map[uint16]any

	// pendingChanges accumulates changes during coalescing window.
	pendingChanges map[uint16]any

	// changeWindowStart is when the first change occurred in current window.
	changeWindowStart time.Time

	// hasChanges indicates pending changes exist.
	hasChanges bool

	// active indicates if subscription is active.
	active bool
}

// NewSubscription creates a new subscription.
func NewSubscription(id uint32, featureID, endpointID uint16, attributeIDs []uint16, minInterval, maxInterval time.Duration) *Subscription {
	return &Subscription{
		ID:             id,
		FeatureID:      featureID,
		EndpointID:     endpointID,
		AttributeIDs:   attributeIDs,
		MinInterval:    minInterval,
		MaxInterval:    maxInterval,
		lastNotified:   time.Now(),
		lastValues:     make(map[uint16]any),
		pendingChanges: make(map[uint16]any),
		active:         true,
	}
}

// IsActive returns whether the subscription is active.
func (s *Subscription) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Deactivate marks the subscription as inactive.
func (s *Subscription) Deactivate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

// RecordChange records a value change for an attribute.
// Returns true if this is a new change that starts the coalescing window.
func (s *Subscription) RecordChange(attrID uint16, value any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active {
		return false
	}

	// Check if attribute is subscribed (empty means all)
	if len(s.AttributeIDs) > 0 {
		found := false
		for _, id := range s.AttributeIDs {
			if id == attrID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Start coalescing window if this is first change
	isNewWindow := !s.hasChanges
	if isNewWindow {
		s.changeWindowStart = time.Now()
	}

	s.pendingChanges[attrID] = value
	s.hasChanges = true

	return isNewWindow
}

// GetPendingNotification returns attributes that should be notified.
// It implements bounce-back suppression and clears pending changes.
// Returns nil if no notification is needed.
func (s *Subscription) GetPendingNotification(suppressBounceBack bool) map[uint16]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active || !s.hasChanges {
		return nil
	}

	// Check if coalescing window has elapsed
	if time.Since(s.changeWindowStart) < s.MinInterval {
		return nil
	}

	// Build notification with changed values
	notification := make(map[uint16]any)

	for attrID, newValue := range s.pendingChanges {
		// Bounce-back suppression: skip if value same as last notified
		if suppressBounceBack {
			if lastVal, exists := s.lastValues[attrID]; exists {
				if valuesEqual(lastVal, newValue) {
					continue
				}
			}
		}
		notification[attrID] = newValue
		s.lastValues[attrID] = newValue
	}

	// Clear pending changes
	s.pendingChanges = make(map[uint16]any)
	s.hasChanges = false
	s.lastNotified = time.Now()

	if len(notification) == 0 {
		return nil // All changes were bounce-backs
	}

	return notification
}

// NeedsHeartbeat returns true if maxInterval has elapsed since last notification.
func (s *Subscription) NeedsHeartbeat() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.active {
		return false
	}

	return time.Since(s.lastNotified) >= s.MaxInterval
}

// RecordHeartbeat records that a heartbeat was sent.
func (s *Subscription) RecordHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastNotified = time.Now()
}

// SetPrimingValues sets the initial values from priming notification.
func (s *Subscription) SetPrimingValues(values map[uint16]any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for attrID, value := range values {
		s.lastValues[attrID] = value
	}
	s.lastNotified = time.Now()
}

// TimeSinceLastNotification returns time since the last notification.
func (s *Subscription) TimeSinceLastNotification() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Since(s.lastNotified)
}

// TimeUntilCoalesceExpiry returns time until coalescing window expires.
// Returns 0 if no changes are pending.
func (s *Subscription) TimeUntilCoalesceExpiry() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.hasChanges {
		return 0
	}

	elapsed := time.Since(s.changeWindowStart)
	if elapsed >= s.MinInterval {
		return 0
	}
	return s.MinInterval - elapsed
}

// valuesEqual compares two values for equality.
func valuesEqual(a, b any) bool {
	// Handle nil cases
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Direct comparison for comparable types
	switch av := a.(type) {
	case int64:
		if bv, ok := b.(int64); ok {
			return av == bv
		}
	case int32:
		if bv, ok := b.(int32); ok {
			return av == bv
		}
	case int:
		if bv, ok := b.(int); ok {
			return av == bv
		}
	case uint64:
		if bv, ok := b.(uint64); ok {
			return av == bv
		}
	case uint32:
		if bv, ok := b.(uint32); ok {
			return av == bv
		}
	case uint:
		if bv, ok := b.(uint); ok {
			return av == bv
		}
	case float64:
		if bv, ok := b.(float64); ok {
			return av == bv
		}
	case float32:
		if bv, ok := b.(float32); ok {
			return av == bv
		}
	case string:
		if bv, ok := b.(string); ok {
			return av == bv
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv
		}
	}

	// Fallback to reflect-based comparison could go here
	// For now, assume not equal if types don't match
	return false
}

// idGenerator generates unique subscription IDs.
var idGenerator atomic.Uint32

// nextID returns the next unique subscription ID.
func nextID() uint32 {
	return idGenerator.Add(1)
}
