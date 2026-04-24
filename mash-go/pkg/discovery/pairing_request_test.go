package discovery_test

import (
	"context"
	"testing"

	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnnouncePairingRequest_TXTRecords verifies correct TXT record format for pairing requests.
func TestAnnouncePairingRequest_TXTRecords(t *testing.T) {
	// Create a test advertiser that captures the TXT records
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)

	info := &discovery.PairingRequestInfo{
		Discriminator: 1234,
		ZoneID:        "A1B2C3D4E5F6A7B8",
		ZoneName:      "Home Energy",
		Host:          "controller.local",
	}

	ctx := context.Background()
	err := dm.AnnouncePairingRequest(ctx, info)
	require.NoError(t, err)

	// Verify the captured TXT records
	require.NotNil(t, advertiser.lastPairingRequest)
	assert.Equal(t, info.Discriminator, advertiser.lastPairingRequest.Discriminator)
	assert.Equal(t, info.ZoneID, advertiser.lastPairingRequest.ZoneID)
	assert.Equal(t, info.ZoneName, advertiser.lastPairingRequest.ZoneName)

	// Verify TXT encoding produces correct format
	txt := discovery.EncodePairingRequestTXT(info)
	assert.Equal(t, "1234", txt["D"])
	assert.Equal(t, "A1B2C3D4E5F6A7B8", txt["ZI"])
	assert.Equal(t, "Home Energy", txt["ZN"])
}

// TestAnnouncePairingRequest_InstanceName verifies the instance name format.
func TestAnnouncePairingRequest_InstanceName(t *testing.T) {
	tests := []struct {
		name          string
		zoneID        string
		discriminator uint16
		wantInstance  string
	}{
		{
			name:          "standard case",
			zoneID:        "A1B2C3D4E5F6A7B8",
			discriminator: 1234,
			wantInstance:  "A1B2C3D4E5F6A7B8-1234",
		},
		{
			name:          "zero discriminator",
			zoneID:        "1234567890ABCDEF",
			discriminator: 0,
			wantInstance:  "1234567890ABCDEF-0",
		},
		{
			name:          "max discriminator",
			zoneID:        "FEDCBA9876543210",
			discriminator: 4095,
			wantInstance:  "FEDCBA9876543210-4095",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discovery.PairingRequestInstanceName(tt.zoneID, tt.discriminator)
			assert.Equal(t, tt.wantInstance, got)
		})
	}
}

// TestStopPairingRequest_Removes verifies that announcements are properly removed.
func TestStopPairingRequest_Removes(t *testing.T) {
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)

	info := &discovery.PairingRequestInfo{
		Discriminator: 1234,
		ZoneID:        "A1B2C3D4E5F6A7B8",
		ZoneName:      "Home Energy",
		Host:          "controller.local",
	}

	ctx := context.Background()
	err := dm.AnnouncePairingRequest(ctx, info)
	require.NoError(t, err)

	// Verify it's tracked
	assert.Equal(t, 1, dm.PairingRequestCount())

	// Stop the announcement
	err = dm.StopPairingRequest(1234)
	require.NoError(t, err)

	// Verify it was stopped
	assert.Equal(t, uint16(1234), advertiser.stoppedPairingRequest)
	assert.Equal(t, 0, dm.PairingRequestCount())
}

// TestStopPairingRequest_NotFound verifies error when stopping non-existent request.
func TestStopPairingRequest_NotFound(t *testing.T) {
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)

	err := dm.StopPairingRequest(9999)
	assert.ErrorIs(t, err, discovery.ErrNotFound)
}

// TestMultiplePairingRequests verifies that multiple concurrent requests work.
func TestMultiplePairingRequests(t *testing.T) {
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)

	// Create multiple pairing requests
	infos := []*discovery.PairingRequestInfo{
		{
			Discriminator: 1000,
			ZoneID:        "A1B2C3D4E5F6A7B8",
			ZoneName:      "Zone 1",
			Host:          "controller.local",
		},
		{
			Discriminator: 2000,
			ZoneID:        "A1B2C3D4E5F6A7B8",
			ZoneName:      "Zone 1",
			Host:          "controller.local",
		},
		{
			Discriminator: 3000,
			ZoneID:        "A1B2C3D4E5F6A7B8",
			ZoneName:      "Zone 1",
			Host:          "controller.local",
		},
	}

	ctx := context.Background()

	// Announce all requests
	for _, info := range infos {
		err := dm.AnnouncePairingRequest(ctx, info)
		require.NoError(t, err)
	}

	// Verify all are tracked
	assert.Equal(t, 3, dm.PairingRequestCount())

	// Stop the middle one
	err := dm.StopPairingRequest(2000)
	require.NoError(t, err)

	// Verify count
	assert.Equal(t, 2, dm.PairingRequestCount())

	// Stop the rest
	err = dm.StopPairingRequest(1000)
	require.NoError(t, err)
	err = dm.StopPairingRequest(3000)
	require.NoError(t, err)

	assert.Equal(t, 0, dm.PairingRequestCount())
}

// TestAnnouncePairingRequest_ValidationError tests validation of invalid input.
func TestAnnouncePairingRequest_ValidationError(t *testing.T) {
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)
	ctx := context.Background()

	tests := []struct {
		name    string
		info    *discovery.PairingRequestInfo
		wantErr error
	}{
		{
			name: "discriminator too high",
			info: &discovery.PairingRequestInfo{
				Discriminator: 5000, // Max is 4095
				ZoneID:        "A1B2C3D4E5F6A7B8",
				Host:          "controller.local",
			},
			wantErr: discovery.ErrInvalidDiscriminator,
		},
		{
			name: "zone ID too short",
			info: &discovery.PairingRequestInfo{
				Discriminator: 1234,
				ZoneID:        "A1B2", // Should be 16 hex chars
				Host:          "controller.local",
			},
			wantErr: discovery.ErrMissingRequired,
		},
		{
			name: "zone ID wrong length",
			info: &discovery.PairingRequestInfo{
				Discriminator: 1234,
				ZoneID:        "A1B2C3D4E5F6A7B", // 15 chars, should be 16
				Host:          "controller.local",
			},
			wantErr: discovery.ErrMissingRequired,
		},
		{
			name: "zone ID invalid hex",
			info: &discovery.PairingRequestInfo{
				Discriminator: 1234,
				ZoneID:        "GHIJKLMNOPQRSTUV", // Not valid hex
				Host:          "controller.local",
			},
			wantErr: discovery.ErrInvalidTXTRecord,
		},
		{
			name: "missing host",
			info: &discovery.PairingRequestInfo{
				Discriminator: 1234,
				ZoneID:        "A1B2C3D4E5F6A7B8",
				Host:          "",
			},
			wantErr: discovery.ErrMissingRequired,
		},
		{
			name: "nil info",
			info: nil,
			wantErr: discovery.ErrMissingRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dm.AnnouncePairingRequest(ctx, tt.info)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// TestAnnouncePairingRequest_Duplicate verifies error when announcing same discriminator twice.
func TestAnnouncePairingRequest_Duplicate(t *testing.T) {
	advertiser := &capturingAdvertiser{}
	dm := discovery.NewDiscoveryManager(advertiser)

	info := &discovery.PairingRequestInfo{
		Discriminator: 1234,
		ZoneID:        "A1B2C3D4E5F6A7B8",
		ZoneName:      "Home Energy",
		Host:          "controller.local",
	}

	ctx := context.Background()

	// First announcement should succeed
	err := dm.AnnouncePairingRequest(ctx, info)
	require.NoError(t, err)

	// Second announcement with same discriminator should fail
	err = dm.AnnouncePairingRequest(ctx, info)
	assert.ErrorIs(t, err, discovery.ErrAlreadyExists)
}

// TestPairingRequestInfo_Validate tests the validation method directly.
func TestPairingRequestInfo_Validate(t *testing.T) {
	tests := []struct {
		name    string
		info    *discovery.PairingRequestInfo
		wantErr error
	}{
		{
			name: "valid info",
			info: &discovery.PairingRequestInfo{
				Discriminator: 1234,
				ZoneID:        "A1B2C3D4E5F6A7B8",
				Host:          "controller.local",
			},
			wantErr: nil,
		},
		{
			name: "valid with zone name",
			info: &discovery.PairingRequestInfo{
				Discriminator: 0,
				ZoneID:        "1234567890ABCDEF",
				ZoneName:      "My Zone",
				Host:          "my-controller.local",
			},
			wantErr: nil,
		},
		{
			name: "max discriminator",
			info: &discovery.PairingRequestInfo{
				Discriminator: 4095,
				ZoneID:        "FEDCBA9876543210",
				Host:          "controller.local",
			},
			wantErr: nil,
		},
		{
			name: "discriminator too high",
			info: &discovery.PairingRequestInfo{
				Discriminator: 4096,
				ZoneID:        "A1B2C3D4E5F6A7B8",
				Host:          "controller.local",
			},
			wantErr: discovery.ErrInvalidDiscriminator,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.info.Validate()
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// capturingAdvertiser is a test advertiser that captures calls for verification.
type capturingAdvertiser struct {
	lastPairingRequest    *discovery.PairingRequestInfo
	stoppedPairingRequest uint16
}

func (a *capturingAdvertiser) AdvertiseCommissionable(_ context.Context, _ *discovery.CommissionableInfo) error {
	return nil
}

func (a *capturingAdvertiser) StopCommissionable() error {
	return nil
}

func (a *capturingAdvertiser) AdvertiseOperational(_ context.Context, _ *discovery.OperationalInfo) error {
	return nil
}

func (a *capturingAdvertiser) UpdateOperational(_ string, _ *discovery.OperationalInfo) error {
	return nil
}

func (a *capturingAdvertiser) StopOperational(_ string) error {
	return nil
}

func (a *capturingAdvertiser) StopAll() {
}

func (a *capturingAdvertiser) AnnouncePairingRequest(_ context.Context, info *discovery.PairingRequestInfo) error {
	a.lastPairingRequest = info
	return nil
}

func (a *capturingAdvertiser) StopPairingRequest(discriminator uint16) error {
	a.stoppedPairingRequest = discriminator
	return nil
}
