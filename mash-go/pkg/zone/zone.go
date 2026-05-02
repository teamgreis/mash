package zone

import (
	"errors"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
)

// Zone errors.
var (
	ErrZoneNotFound     = errors.New("zone not found")
	ErrZoneExists       = errors.New("zone already exists")
	ErrZoneTypeExists   = errors.New("zone of this type already exists")
	ErrMaxZonesExceeded = errors.New("maximum zones exceeded")
	ErrZoneNotConnected = errors.New("zone not connected")
)

// MaxZones is the maximum number of zones a device can belong to.
// Per DEC-043/DEC-060: GRID + LOCAL + TEST = 3 zones maximum.
// TEST zones are only accepted when DeviceConfig.TestMode is true.
const MaxZones = 3

// Zone represents a zone membership for a device.
type Zone struct {
	// ID is the unique zone identifier.
	ID string

	// Type determines the zone's priority.
	Type cert.ZoneType

	// Connected indicates if there's an active connection to this zone's controller.
	Connected bool

	// LastSeen is when the controller was last active.
	LastSeen time.Time

	// CommissionedAt is when the device joined this zone.
	CommissionedAt time.Time
}

// Priority returns the zone's numeric priority (1 = highest).
func (z *Zone) Priority() uint8 {
	return z.Type.Priority()
}

// ZoneValue represents a value set by a zone with its metadata.
type ZoneValue struct {
	// ZoneID identifies which zone set this value.
	ZoneID string

	// ZoneType is the type of zone that set this value.
	ZoneType cert.ZoneType

	// Value is the actual value.
	Value int64

	// Duration is how long the value is valid (0 = indefinite).
	Duration time.Duration

	// SetAt is when the value was set.
	SetAt time.Time

	// ExpiresAt is when the value expires (zero if indefinite).
	ExpiresAt time.Time

	// Dormant indicates the originating zone is currently disconnected.
	// The commanded value is preserved (per multi-zone-resolution.md §6.2)
	// but does NOT contribute to ResolveLimits/ResolveSetpoints aggregation
	// while the slot is dormant. Cleared when the zone reconnects.
	Dormant bool
}

// IsExpired returns true if the value has expired.
func (v *ZoneValue) IsExpired() bool {
	if v.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(v.ExpiresAt)
}

// Priority returns the zone's priority for resolution.
func (v *ZoneValue) Priority() uint8 {
	return v.ZoneType.Priority()
}

// MultiZoneValue tracks values from multiple zones for a single attribute.
type MultiZoneValue struct {
	// Values holds the value from each zone, keyed by zone ID.
	Values map[string]*ZoneValue

	// EffectiveValue is the resolved value after priority/limit resolution.
	EffectiveValue *int64

	// WinningZoneID is the zone whose value is currently effective.
	WinningZoneID string
}

// NewMultiZoneValue creates a new multi-zone value tracker.
func NewMultiZoneValue() *MultiZoneValue {
	return &MultiZoneValue{
		Values: make(map[string]*ZoneValue),
	}
}

// Set sets a value for a specific zone.
func (m *MultiZoneValue) Set(zoneID string, zoneType cert.ZoneType, value int64, duration time.Duration) {
	now := time.Now()
	zv := &ZoneValue{
		ZoneID:   zoneID,
		ZoneType: zoneType,
		Value:    value,
		Duration: duration,
		SetAt:    now,
	}
	if duration > 0 {
		zv.ExpiresAt = now.Add(duration)
	}
	m.Values[zoneID] = zv
}

// Clear clears the value for a specific zone.
func (m *MultiZoneValue) Clear(zoneID string) {
	delete(m.Values, zoneID)
}

// SetDormant marks an existing entry as dormant (or active).
// No-op if the zone has no entry. The entry itself is preserved so that
// the commanded value survives a disconnect and contributes again on
// reconnect (per multi-zone-resolution.md §6.2).
func (m *MultiZoneValue) SetDormant(zoneID string, dormant bool) {
	if v, ok := m.Values[zoneID]; ok {
		v.Dormant = dormant
	}
}

// Get returns the value for a specific zone, or nil if not set.
func (m *MultiZoneValue) Get(zoneID string) *ZoneValue {
	return m.Values[zoneID]
}

// RemoveExpired removes all expired values.
func (m *MultiZoneValue) RemoveExpired() {
	for zoneID, v := range m.Values {
		if v.IsExpired() {
			delete(m.Values, zoneID)
		}
	}
}

// ResolveLimits resolves multiple limit values using "most restrictive wins":
// the smallest value across all non-TEST zones. Returns nil if no values are
// set.
//
// Per docs/features/energy-control.md "Validation Rules" and
// docs/testing/behavior/multi-zone-resolution.md §6.3, both consumption and
// production limits are non-negative; direction is implicit in the attribute
// name (myConsumptionLimit vs myProductionLimit). Negative values are
// rejected at the Write boundary (StatusInvalidValue) and never reach this
// resolver.
func (m *MultiZoneValue) ResolveLimits() (*int64, string) {
	m.RemoveExpired()

	if len(m.Values) == 0 {
		m.EffectiveValue = nil
		m.WinningZoneID = ""
		return nil, ""
	}

	var effectiveValue *int64
	var winningZoneID string

	for zoneID, v := range m.Values {
		// TEST zones are pure observers -- exclude from limit resolution (DEC-060).
		if v.ZoneType == cert.ZoneTypeTest {
			continue
		}
		// Disconnected zones' commanded limits are preserved but do not
		// contribute to aggregation (multi-zone-resolution.md §6.2).
		if v.Dormant {
			continue
		}
		if effectiveValue == nil || v.Value < *effectiveValue {
			val := v.Value
			effectiveValue = &val
			winningZoneID = zoneID
		}
	}

	m.EffectiveValue = effectiveValue
	m.WinningZoneID = winningZoneID
	return effectiveValue, winningZoneID
}

// ResolveSetpoints resolves multiple setpoint values using "highest priority wins".
// The zone with the lowest priority number wins.
// Returns nil if no values are set.
func (m *MultiZoneValue) ResolveSetpoints() (*int64, string) {
	m.RemoveExpired()

	if len(m.Values) == 0 {
		m.EffectiveValue = nil
		m.WinningZoneID = ""
		return nil, ""
	}

	var effectiveValue *int64
	var winningZoneID string
	var winningPriority uint8 = 255

	for zoneID, v := range m.Values {
		// TEST zones are pure observers -- exclude from setpoint resolution (DEC-060).
		if v.ZoneType == cert.ZoneTypeTest {
			continue
		}
		// Disconnected zones' commanded setpoints are preserved but do not
		// contribute to aggregation (multi-zone-resolution.md §6.2).
		if v.Dormant {
			continue
		}
		if v.Priority() < winningPriority {
			val := v.Value
			effectiveValue = &val
			winningZoneID = zoneID
			winningPriority = v.Priority()
		}
	}

	m.EffectiveValue = effectiveValue
	m.WinningZoneID = winningZoneID
	return effectiveValue, winningZoneID
}

// Effective returns the current effective value and winning zone.
func (m *MultiZoneValue) Effective() (*int64, string) {
	return m.EffectiveValue, m.WinningZoneID
}
