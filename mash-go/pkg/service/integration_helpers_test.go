//go:build integration

package service

import (
	"context"
	"sync"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// ============================================================================
// Mock Advertiser for Integration Tests
// ============================================================================

// mockAdvertiser implements discovery.Advertiser for testing.
// It records advertising calls but doesn't actually send mDNS packets.
type mockAdvertiser struct {
	mu sync.Mutex

	// Track what's being advertised
	commissionable   *discovery.CommissionableInfo
	operationalZones map[string]*discovery.OperationalInfo
	commissionerZone *discovery.CommissionerInfo
	pairingRequests  map[uint16]*discovery.PairingRequestInfo
}

func newMockAdvertiser() *mockAdvertiser {
	return &mockAdvertiser{
		operationalZones: make(map[string]*discovery.OperationalInfo),
		pairingRequests:  make(map[uint16]*discovery.PairingRequestInfo),
	}
}

func (m *mockAdvertiser) AdvertiseCommissionable(ctx context.Context, info *discovery.CommissionableInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionable = info
	return nil
}

func (m *mockAdvertiser) StopCommissionable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionable = nil
	return nil
}

func (m *mockAdvertiser) AdvertiseOperational(ctx context.Context, info *discovery.OperationalInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operationalZones[info.ZoneID] = info
	return nil
}

func (m *mockAdvertiser) UpdateOperational(zoneID string, info *discovery.OperationalInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.operationalZones[zoneID]; !exists {
		return discovery.ErrNotFound
	}
	m.operationalZones[zoneID] = info
	return nil
}

func (m *mockAdvertiser) StopOperational(zoneID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.operationalZones, zoneID)
	return nil
}

func (m *mockAdvertiser) AdvertiseCommissioner(ctx context.Context, info *discovery.CommissionerInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionerZone = info
	return nil
}

func (m *mockAdvertiser) UpdateCommissioner(zoneID string, info *discovery.CommissionerInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionerZone = info
	return nil
}

func (m *mockAdvertiser) StopCommissioner(zoneID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionerZone = nil
	return nil
}

func (m *mockAdvertiser) AnnouncePairingRequest(ctx context.Context, info *discovery.PairingRequestInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pairingRequests[info.Discriminator] = info
	return nil
}

func (m *mockAdvertiser) StopPairingRequest(discriminator uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pairingRequests, discriminator)
	return nil
}

func (m *mockAdvertiser) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commissionable = nil
	m.operationalZones = make(map[string]*discovery.OperationalInfo)
	m.commissionerZone = nil
	m.pairingRequests = make(map[uint16]*discovery.PairingRequestInfo)
}

// ============================================================================
// Mock Browser for Integration Tests
// ============================================================================

// mockBrowser implements discovery.Browser for testing.
// It returns preconfigured devices rather than doing actual mDNS browsing.
type mockBrowser struct {
	mu sync.Mutex

	// Preconfigured operational devices that will be returned
	operationalDevices []*discovery.OperationalService
}

func newMockBrowser() *mockBrowser {
	return &mockBrowser{}
}

func (m *mockBrowser) BrowseCommissionable(ctx context.Context) (added, removed <-chan *discovery.CommissionableService, err error) {
	// Return empty channels for commissioning (we don't use this in tests)
	addedCh := make(chan *discovery.CommissionableService)
	removedCh := make(chan *discovery.CommissionableService)
	close(addedCh)
	close(removedCh)
	return addedCh, removedCh, nil
}

func (m *mockBrowser) BrowseOperational(ctx context.Context, zoneID string) (<-chan *discovery.OperationalService, error) {
	ch := make(chan *discovery.OperationalService)

	go func() {
		m.mu.Lock()
		devices := make([]*discovery.OperationalService, len(m.operationalDevices))
		copy(devices, m.operationalDevices)
		m.mu.Unlock()

		for _, svc := range devices {
			if zoneID == "" || svc.ZoneID == zoneID {
				select {
				case ch <- svc:
				case <-ctx.Done():
					close(ch)
					return
				}
			}
		}
		close(ch)
	}()

	return ch, nil
}

func (m *mockBrowser) BrowsePairingRequests(ctx context.Context, callback func(discovery.PairingRequestService)) error {
	// Default implementation: block until cancelled
	<-ctx.Done()
	return nil
}

func (m *mockBrowser) FindByDiscriminator(ctx context.Context, discriminator uint16) (*discovery.CommissionableService, error) {
	// Not used in integration tests - they provide CommissionableService directly
	return nil, discovery.ErrNotFound
}

func (m *mockBrowser) FindAllByDiscriminator(ctx context.Context, discriminator uint16) ([]*discovery.CommissionableService, error) {
	return nil, nil
}

func (m *mockBrowser) Stop() {
	// Nothing to stop in mock
}

// AddOperationalDevice adds a device that will be returned by BrowseOperational.
func (m *mockBrowser) AddOperationalDevice(svc *discovery.OperationalService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operationalDevices = append(m.operationalDevices, svc)
}

// ClearOperationalDevices removes all preconfigured devices.
func (m *mockBrowser) ClearOperationalDevices() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operationalDevices = nil
}
