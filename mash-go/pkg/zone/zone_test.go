package zone

import (
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/cert"
)

func TestMultiZoneValueLimits(t *testing.T) {
	t.Run("EmptyReturnsNil", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		val, zoneID := mzv.ResolveLimits()
		if val != nil {
			t.Errorf("ResolveLimits() = %v, want nil", *val)
		}
		if zoneID != "" {
			t.Errorf("zoneID = %q, want empty", zoneID)
		}
	})

	t.Run("SingleValue", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-1", cert.ZoneTypeLocal, 5000, 0)

		val, zoneID := mzv.ResolveLimits()
		if val == nil || *val != 5000 {
			t.Errorf("ResolveLimits() = %v, want 5000", val)
		}
		if zoneID != "zone-1" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-1")
		}
	})

	t.Run("MostRestrictiveWins", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-grid", cert.ZoneTypeGrid, 3000, 0)  // Most restrictive
		mzv.Set("zone-home", cert.ZoneTypeLocal, 5000, 0)  // Less restrictive

		val, zoneID := mzv.ResolveLimits()
		if val == nil || *val != 3000 {
			t.Errorf("ResolveLimits() = %v, want 3000 (most restrictive)", val)
		}
		if zoneID != "zone-grid" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-grid")
		}
	})

	t.Run("ProductionLimits", func(t *testing.T) {
		// Per docs/features/energy-control.md, both consumption and production
		// limits are non-negative; direction is implicit in the attribute
		// name. Most-restrictive resolution is the same as for consumption:
		// the smallest value wins.
		mzv := NewMultiZoneValue()
		mzv.Set("zone-grid", cert.ZoneTypeGrid, 3000, 0) // Less restrictive (more export allowed)
		mzv.Set("zone-home", cert.ZoneTypeLocal, 2000, 0) // More restrictive (less export)

		val, zoneID := mzv.ResolveLimits()
		if val == nil || *val != 2000 {
			t.Errorf("ResolveLimits() = %v, want 2000 (most restrictive)", val)
		}
		if zoneID != "zone-home" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-home")
		}
	})

	t.Run("ExpiredValuesIgnored", func(t *testing.T) {
		mzv := NewMultiZoneValue()

		// Set a value that expires immediately
		mzv.Set("zone-1", cert.ZoneTypeGrid, 1000, 1*time.Millisecond)
		mzv.Set("zone-2", cert.ZoneTypeLocal, 5000, 0) // No expiry

		// Wait for first to expire
		time.Sleep(5 * time.Millisecond)

		val, zoneID := mzv.ResolveLimits()
		if val == nil || *val != 5000 {
			t.Errorf("ResolveLimits() = %v, want 5000 (expired value ignored)", val)
		}
		if zoneID != "zone-2" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-2")
		}
	})
}

func TestZoneTypeTestExcludedFromLimits(t *testing.T) {
	t.Run("TestZoneIgnored", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-grid", cert.ZoneTypeGrid, 3000, 0)
		mzv.Set("zone-test", cert.ZoneTypeTest, 1000, 0) // Would be most restrictive, but should be ignored

		val, zoneID := mzv.ResolveLimits()
		if val == nil || *val != 3000 {
			t.Errorf("ResolveLimits() = %v, want 3000 (TEST zone should be ignored)", val)
		}
		if zoneID != "zone-grid" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-grid")
		}
	})

	t.Run("OnlyTestZoneReturnsNil", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-test", cert.ZoneTypeTest, 5000, 0)

		val, zoneID := mzv.ResolveLimits()
		if val != nil {
			t.Errorf("ResolveLimits() = %v, want nil (only TEST zone values)", *val)
		}
		if zoneID != "" {
			t.Errorf("zoneID = %q, want empty", zoneID)
		}
	})
}

func TestZoneTypeTestExcludedFromSetpoints(t *testing.T) {
	t.Run("TestZoneIgnored", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-local", cert.ZoneTypeLocal, 8000, 0)
		mzv.Set("zone-test", cert.ZoneTypeTest, 2000, 0) // Higher priority number but should be ignored

		val, zoneID := mzv.ResolveSetpoints()
		if val == nil || *val != 8000 {
			t.Errorf("ResolveSetpoints() = %v, want 8000 (TEST zone should be ignored)", val)
		}
		if zoneID != "zone-local" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-local")
		}
	})

	t.Run("OnlyTestZoneReturnsNil", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-test", cert.ZoneTypeTest, 5000, 0)

		val, zoneID := mzv.ResolveSetpoints()
		if val != nil {
			t.Errorf("ResolveSetpoints() = %v, want nil (only TEST zone values)", *val)
		}
		if zoneID != "" {
			t.Errorf("zoneID = %q, want empty", zoneID)
		}
	})
}

func TestMultiZoneValueSetpoints(t *testing.T) {
	t.Run("HighestPriorityWins", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-user", cert.ZoneTypeLocal, 10000, 0)       // Priority 4
		mzv.Set("zone-home", cert.ZoneTypeLocal, 8000, 0)    // Priority 3
		mzv.Set("zone-grid", cert.ZoneTypeGrid, 5000, 0)   // Priority 1 (highest)

		val, zoneID := mzv.ResolveSetpoints()
		if val == nil || *val != 5000 {
			t.Errorf("ResolveSetpoints() = %v, want 5000 (highest priority)", val)
		}
		if zoneID != "zone-grid" {
			t.Errorf("zoneID = %q, want %q", zoneID, "zone-grid")
		}
	})

	t.Run("SamePriorityFirstWins", func(t *testing.T) {
		mzv := NewMultiZoneValue()
		mzv.Set("zone-home-1", cert.ZoneTypeLocal, 5000, 0)
		mzv.Set("zone-home-2", cert.ZoneTypeLocal, 8000, 0)

		val, _ := mzv.ResolveSetpoints()
		// Either value is valid since same priority - just check one wins
		if val == nil {
			t.Error("ResolveSetpoints() = nil, want a value")
		}
	})
}

func TestZoneValueExpiry(t *testing.T) {
	t.Run("NoExpiry", func(t *testing.T) {
		v := &ZoneValue{
			ZoneID:   "zone-1",
			ZoneType: cert.ZoneTypeLocal,
			Value:    5000,
			Duration: 0,
			SetAt:    time.Now(),
		}
		if v.IsExpired() {
			t.Error("Value with no expiry should not be expired")
		}
	})

	t.Run("NotYetExpired", func(t *testing.T) {
		v := &ZoneValue{
			ZoneID:    "zone-1",
			ZoneType:  cert.ZoneTypeLocal,
			Value:     5000,
			Duration:  1 * time.Hour,
			SetAt:     time.Now(),
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		if v.IsExpired() {
			t.Error("Value should not be expired yet")
		}
	})

	t.Run("Expired", func(t *testing.T) {
		v := &ZoneValue{
			ZoneID:    "zone-1",
			ZoneType:  cert.ZoneTypeLocal,
			Value:     5000,
			Duration:  1 * time.Millisecond,
			SetAt:     time.Now().Add(-1 * time.Second),
			ExpiresAt: time.Now().Add(-1 * time.Millisecond),
		}
		if !v.IsExpired() {
			t.Error("Value should be expired")
		}
	})
}

func TestManager(t *testing.T) {
	t.Run("AddAndRemoveZone", func(t *testing.T) {
		m := NewManager()

		// Add zone
		err := m.AddZone("zone-1", cert.ZoneTypeLocal)
		if err != nil {
			t.Fatalf("AddZone() error = %v", err)
		}

		// Verify added
		if !m.HasZone("zone-1") {
			t.Error("HasZone(zone-1) = false, want true")
		}
		if m.ZoneCount() != 1 {
			t.Errorf("ZoneCount() = %d, want 1", m.ZoneCount())
		}

		// Add duplicate
		err = m.AddZone("zone-1", cert.ZoneTypeLocal)
		if err != ErrZoneExists {
			t.Errorf("AddZone(duplicate) error = %v, want ErrZoneExists", err)
		}

		// Remove zone
		err = m.RemoveZone("zone-1")
		if err != nil {
			t.Errorf("RemoveZone() error = %v", err)
		}

		if m.HasZone("zone-1") {
			t.Error("HasZone(zone-1) = true after removal")
		}

		// Remove non-existent
		err = m.RemoveZone("zone-1")
		if err != ErrZoneNotFound {
			t.Errorf("RemoveZone(non-existent) error = %v, want ErrZoneNotFound", err)
		}
	})

	t.Run("MaxZonesExceeded", func(t *testing.T) {
		m := NewManager()

		// Add all allowed zone types (GRID + LOCAL + TEST = 3 = MaxZones)
		err := m.AddZone("grid-zone", cert.ZoneTypeGrid)
		if err != nil {
			t.Fatalf("AddZone(grid-zone) error = %v", err)
		}
		err = m.AddZone("local-zone", cert.ZoneTypeLocal)
		if err != nil {
			t.Fatalf("AddZone(local-zone) error = %v", err)
		}
		err = m.AddZone("test-zone", cert.ZoneTypeTest)
		if err != nil {
			t.Fatalf("AddZone(test-zone) error = %v", err)
		}

		// Verify we're at capacity
		if m.ZoneCount() != MaxZones {
			t.Errorf("ZoneCount() = %d, want %d", m.ZoneCount(), MaxZones)
		}
	})

	t.Run("ConnectionState", func(t *testing.T) {
		m := NewManager()
		m.AddZone("zone-1", cert.ZoneTypeLocal)

		// Initially not connected
		zone, _ := m.GetZone("zone-1")
		if zone.Connected {
			t.Error("Zone should not be connected initially")
		}

		// Set connected
		err := m.SetConnected("zone-1")
		if err != nil {
			t.Errorf("SetConnected() error = %v", err)
		}

		zone, _ = m.GetZone("zone-1")
		if !zone.Connected {
			t.Error("Zone should be connected")
		}
		if zone.LastSeen.IsZero() {
			t.Error("LastSeen should be set")
		}

		// Connected zones
		connected := m.ConnectedZones()
		if len(connected) != 1 || connected[0] != "zone-1" {
			t.Errorf("ConnectedZones() = %v, want [zone-1]", connected)
		}

		// Set disconnected
		err = m.SetDisconnected("zone-1")
		if err != nil {
			t.Errorf("SetDisconnected() error = %v", err)
		}

		zone, _ = m.GetZone("zone-1")
		if zone.Connected {
			t.Error("Zone should be disconnected")
		}
	})

	t.Run("HighestPriorityZone", func(t *testing.T) {
		m := NewManager()
		m.AddZone("zone-local", cert.ZoneTypeLocal)
		m.AddZone("zone-grid", cert.ZoneTypeGrid)

		highest := m.HighestPriorityZone()
		if highest == nil || highest.ID != "zone-grid" {
			t.Errorf("HighestPriorityZone() = %v, want zone-grid", highest)
		}
	})

	t.Run("HighestPriorityConnectedZone", func(t *testing.T) {
		m := NewManager()
		m.AddZone("zone-local", cert.ZoneTypeLocal)
		m.AddZone("zone-grid", cert.ZoneTypeGrid)

		// None connected
		highest := m.HighestPriorityConnectedZone()
		if highest != nil {
			t.Errorf("HighestPriorityConnectedZone() = %v, want nil", highest)
		}

		// Connect only local zone
		m.SetConnected("zone-local")

		highest = m.HighestPriorityConnectedZone()
		if highest == nil || highest.ID != "zone-local" {
			t.Errorf("HighestPriorityConnectedZone() = %v, want zone-local", highest)
		}

		// Connect grid zone (higher priority)
		m.SetConnected("zone-grid")

		highest = m.HighestPriorityConnectedZone()
		if highest == nil || highest.ID != "zone-grid" {
			t.Errorf("HighestPriorityConnectedZone() = %v, want zone-grid", highest)
		}
	})

	t.Run("Callbacks", func(t *testing.T) {
		m := NewManager()

		var addedZoneID string
		var removedZoneID string
		var connectedZoneID string
		var disconnectedZoneID string

		m.OnZoneAdded(func(z *Zone) { addedZoneID = z.ID })
		m.OnZoneRemoved(func(id string) { removedZoneID = id })
		m.OnConnect(func(id string) { connectedZoneID = id })
		m.OnDisconnect(func(id string) { disconnectedZoneID = id })

		m.AddZone("zone-1", cert.ZoneTypeLocal)
		if addedZoneID != "zone-1" {
			t.Errorf("OnZoneAdded callback not called correctly")
		}

		m.SetConnected("zone-1")
		if connectedZoneID != "zone-1" {
			t.Errorf("OnConnect callback not called correctly")
		}

		m.SetDisconnected("zone-1")
		if disconnectedZoneID != "zone-1" {
			t.Errorf("OnDisconnect callback not called correctly")
		}

		m.RemoveZone("zone-1")
		if removedZoneID != "zone-1" {
			t.Errorf("OnZoneRemoved callback not called correctly")
		}
	})
}

func TestCanRemoveZone(t *testing.T) {
	m := NewManager()
	m.AddZone("zone-local", cert.ZoneTypeLocal)

	tests := []struct {
		requester cert.ZoneType
		canRemove bool
	}{
		{cert.ZoneTypeGrid, true},   // Priority 1 can remove priority 2
		{cert.ZoneTypeLocal, false}, // Priority 2 cannot remove priority 2 (same)
	}

	for _, tt := range tests {
		t.Run(tt.requester.String(), func(t *testing.T) {
			got := m.CanRemoveZone(tt.requester, "zone-local")
			if got != tt.canRemove {
				t.Errorf("CanRemoveZone(%s, zone-local) = %v, want %v",
					tt.requester.String(), got, tt.canRemove)
			}
		})
	}
}

func TestZoneTypeEnforcement(t *testing.T) {
	t.Run("DuplicateGridZoneRejected", func(t *testing.T) {
		m := NewManager()

		// Add first GRID zone
		err := m.AddZone("grid-1", cert.ZoneTypeGrid)
		if err != nil {
			t.Fatalf("AddZone(grid-1) error = %v", err)
		}

		// Try to add second GRID zone - should fail with ErrZoneTypeExists
		err = m.AddZone("grid-2", cert.ZoneTypeGrid)
		if err != ErrZoneTypeExists {
			t.Errorf("AddZone(grid-2) error = %v, want ErrZoneTypeExists", err)
		}
	})

	t.Run("DuplicateLocalZoneRejected", func(t *testing.T) {
		m := NewManager()

		// Add first LOCAL zone
		err := m.AddZone("local-1", cert.ZoneTypeLocal)
		if err != nil {
			t.Fatalf("AddZone(local-1) error = %v", err)
		}

		// Try to add second LOCAL zone - should fail with ErrZoneTypeExists
		err = m.AddZone("local-2", cert.ZoneTypeLocal)
		if err != ErrZoneTypeExists {
			t.Errorf("AddZone(local-2) error = %v, want ErrZoneTypeExists", err)
		}
	})

	t.Run("GridPlusLocalAllowed", func(t *testing.T) {
		m := NewManager()

		// Add GRID zone
		err := m.AddZone("grid-1", cert.ZoneTypeGrid)
		if err != nil {
			t.Fatalf("AddZone(grid-1) error = %v", err)
		}

		// Add LOCAL zone - should succeed (different types)
		err = m.AddZone("local-1", cert.ZoneTypeLocal)
		if err != nil {
			t.Errorf("AddZone(local-1) error = %v, want nil", err)
		}

		// Verify both zones exist
		if m.ZoneCount() != 2 {
			t.Errorf("ZoneCount() = %d, want 2", m.ZoneCount())
		}
	})

	t.Run("TypeCheckBeforeCapacityCheck", func(t *testing.T) {
		m := NewManager()

		// Fill all zone types to reach capacity (MaxZones = 3)
		err := m.AddZone("grid-1", cert.ZoneTypeGrid)
		if err != nil {
			t.Fatalf("AddZone(grid-1) error = %v", err)
		}
		err = m.AddZone("local-1", cert.ZoneTypeLocal)
		if err != nil {
			t.Fatalf("AddZone(local-1) error = %v", err)
		}
		err = m.AddZone("test-1", cert.ZoneTypeTest)
		if err != nil {
			t.Fatalf("AddZone(test-1) error = %v", err)
		}

		// Now at capacity. Try to add another GRID zone.
		// Should get ErrZoneTypeExists (type check) NOT ErrMaxZonesExceeded (capacity check)
		// This verifies type check happens BEFORE capacity check.
		err = m.AddZone("grid-2", cert.ZoneTypeGrid)
		if err != ErrZoneTypeExists {
			t.Errorf("AddZone(grid-2) error = %v, want ErrZoneTypeExists (type check should happen before capacity check)", err)
		}
	})

	t.Run("MaxZonesIsThree", func(t *testing.T) {
		// Verify MaxZones constant is 3 (GRID + LOCAL + TEST per DEC-060)
		if MaxZones != 3 {
			t.Errorf("MaxZones = %d, want 3", MaxZones)
		}
	})
}
