package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// TestMDNSAdvertiserCreate verifies the advertiser can be created with mock config.
func TestMDNSAdvertiserCreate(t *testing.T) {
	config := testAdvertiserConfig(t)
	adv, err := discovery.NewMDNSAdvertiser(config)
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()
}

// TestMDNSAdvertiserCommissionable verifies advertising a commissionable device.
func TestMDNSAdvertiserCommissionable(t *testing.T) {
	config := testAdvertiserConfig(t)
	adv, err := discovery.NewMDNSAdvertiser(config)
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	ctx := context.Background()
	info := &discovery.CommissionableInfo{
		Discriminator: 1234,
		Categories:    []discovery.DeviceCategory{discovery.CategoryEMobility},
		Serial:        "ABC123",
		Brand:         "TestCo",
		Model:         "Model1",
		Port:          8443,
	}

	err = adv.AdvertiseCommissionable(ctx, info)
	if err != nil {
		t.Fatalf("Failed to advertise commissionable: %v", err)
	}

	// Stop should work without error
	err = adv.StopCommissionable()
	if err != nil {
		t.Errorf("Failed to stop commissionable: %v", err)
	}
}

// TestMDNSAdvertiserOperational verifies advertising an operational device.
func TestMDNSAdvertiserOperational(t *testing.T) {
	config := testAdvertiserConfig(t)
	adv, err := discovery.NewMDNSAdvertiser(config)
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	ctx := context.Background()
	info := &discovery.OperationalInfo{
		ZoneID:        "A1B2C3D4E5F6A7B8",
		DeviceID:      "F9E8D7C6B5A49382",
		VendorProduct: "TestCo:Model1",
		Port:          8443,
	}

	err = adv.AdvertiseOperational(ctx, info)
	if err != nil {
		t.Fatalf("Failed to advertise operational: %v", err)
	}

	// Stop should work without error
	err = adv.StopOperational(info.ZoneID)
	if err != nil {
		t.Errorf("Failed to stop operational: %v", err)
	}
}

// TestMDNSAdvertiserMultipleZones verifies advertising multiple operational zones.
func TestMDNSAdvertiserMultipleZones(t *testing.T) {
	config := testAdvertiserConfig(t)
	adv, err := discovery.NewMDNSAdvertiser(config)
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	ctx := context.Background()

	// Zone 1
	info1 := &discovery.OperationalInfo{
		ZoneID:   "A1B2C3D4E5F6A7B8",
		DeviceID: "F9E8D7C6B5A49382",
		Port:     8443,
	}

	// Zone 2
	info2 := &discovery.OperationalInfo{
		ZoneID:   "1234567890ABCDEF",
		DeviceID: "F9E8D7C6B5A49382",
		Port:     8443,
	}

	err = adv.AdvertiseOperational(ctx, info1)
	if err != nil {
		t.Fatalf("Failed to advertise zone 1: %v", err)
	}

	err = adv.AdvertiseOperational(ctx, info2)
	if err != nil {
		t.Fatalf("Failed to advertise zone 2: %v", err)
	}

	// Stop zone 1
	err = adv.StopOperational(info1.ZoneID)
	if err != nil {
		t.Errorf("Failed to stop zone 1: %v", err)
	}

	// Zone 2 should still be active, stopping it should work
	err = adv.StopOperational(info2.ZoneID)
	if err != nil {
		t.Errorf("Failed to stop zone 2: %v", err)
	}
}

// TestMDNSAdvertiserUpdateOperational verifies updating TXT records.
func TestMDNSAdvertiserUpdateOperational(t *testing.T) {
	adv, err := discovery.NewMDNSAdvertiser(testAdvertiserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	ctx := context.Background()
	info := &discovery.OperationalInfo{
		ZoneID:        "A1B2C3D4E5F6A7B8",
		DeviceID:      "F9E8D7C6B5A49382",
		EndpointCount: 2,
		Port:          8443,
	}

	err = adv.AdvertiseOperational(ctx, info)
	if err != nil {
		t.Fatalf("Failed to advertise operational: %v", err)
	}

	// Update with new endpoint count
	updatedInfo := &discovery.OperationalInfo{
		ZoneID:        "A1B2C3D4E5F6A7B8",
		DeviceID:      "F9E8D7C6B5A49382",
		EndpointCount: 3,
		Port:          8443,
	}

	err = adv.UpdateOperational(info.ZoneID, updatedInfo)
	if err != nil {
		t.Errorf("Failed to update operational: %v", err)
	}
}

// TestMDNSBrowserCreate verifies the browser can be created with default config.
func TestMDNSBrowserCreate(t *testing.T) {
	browser, err := discovery.NewMDNSBrowser(testBrowserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()
}

// TestMDNSBrowserCommissionable verifies browsing starts without error.
// Note: With mocked connections, no devices will be found - this is expected.
// Full integration tests would require real mDNS.
func TestMDNSBrowserCommissionable(t *testing.T) {
	browser, err := discovery.NewMDNSBrowser(testBrowserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	added, removed, err := browser.BrowseCommissionable(ctx)
	if err != nil {
		t.Fatalf("Failed to browse: %v", err)
	}

	// With mocks, we won't find any devices - just verify the channels work
	for range added {
		// Drain channel
	}
	for range removed {
		// Drain channel
	}
}

// TestMDNSBrowserOperational verifies browsing for operational devices starts without error.
func TestMDNSBrowserOperational(t *testing.T) {
	browser, err := discovery.NewMDNSBrowser(testBrowserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results, err := browser.BrowseOperational(ctx, "")
	if err != nil {
		t.Fatalf("Failed to browse: %v", err)
	}

	// With mocks, we won't find any devices - just verify the channel works
	for range results {
		// Drain channel
	}
}

// TestMDNSBrowserFindByDiscriminator verifies FindByDiscriminator times out when no device found.
// Note: With mocked connections, no devices will be found.
func TestMDNSBrowserFindByDiscriminator(t *testing.T) {
	browser, err := discovery.NewMDNSBrowser(testBrowserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// With mocks, device won't be found - expect context deadline exceeded
	_, err = browser.FindByDiscriminator(ctx, 1234)
	if err == nil {
		t.Error("Expected error when device not found")
	}
}

// TestMDNSBrowserFindByDiscriminatorTimeout verifies timeout when device not found.
func TestMDNSBrowserFindByDiscriminatorTimeout(t *testing.T) {
	browser, err := discovery.NewMDNSBrowser(testBrowserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create browser: %v", err)
	}
	defer browser.Stop()

	// Use short timeout since we expect it to fail
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err = browser.FindByDiscriminator(ctx, 9999) // Non-existent discriminator
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// TestMDNSAdvertiserStopNonexistent verifies stopping a non-existent service returns error.
func TestMDNSAdvertiserStopNonexistent(t *testing.T) {
	adv, err := discovery.NewMDNSAdvertiser(testAdvertiserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	// Try to stop a service that was never started
	err = adv.StopOperational("nonexistent-zone")
	if err == nil {
		t.Error("Expected error when stopping non-existent service")
	}
}

// TestMDNSAdvertiserUpdateNonexistent verifies updating a non-existent service returns error.
func TestMDNSAdvertiserUpdateNonexistent(t *testing.T) {
	adv, err := discovery.NewMDNSAdvertiser(testAdvertiserConfig(t))
	if err != nil {
		t.Fatalf("Failed to create advertiser: %v", err)
	}
	defer adv.StopAll()

	info := &discovery.OperationalInfo{
		ZoneID:   "nonexistent",
		DeviceID: "device",
		Port:     8443,
	}

	err = adv.UpdateOperational("nonexistent", info)
	if err == nil {
		t.Error("Expected error when updating non-existent service")
	}
}
