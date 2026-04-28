package runner

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// serviceKey uniquely identifies an mDNS service for deduplication.
type serviceKey struct {
	instanceName string
	serviceType  string
}

// mdnsObserver maintains a live snapshot of mDNS services by running
// persistent browse sessions via the discovery.Browser interface.
// Browse sessions start lazily on first query for a given service type.
type mdnsObserver struct {
	browser discovery.Browser
	debugf  func(string, ...any)

	mu       sync.Mutex
	services map[serviceKey]discoveredService
	notify   chan struct{}                 // closed+replaced on every change to wake WaitFor callers
	sessions map[string]context.CancelFunc // serviceType -> cancel func
	stopped  bool
}

// newMDNSObserver creates an observer backed by the given browser.
// The browser's browse methods will be called lazily when Snapshot or WaitFor
// is first invoked for a particular service type.
func newMDNSObserver(browser discovery.Browser, debugf func(string, ...any)) *mdnsObserver {
	return &mdnsObserver{
		browser:  browser,
		debugf:   debugf,
		services: make(map[serviceKey]discoveredService),
		notify:   make(chan struct{}),
		sessions: make(map[string]context.CancelFunc),
	}
}

// Snapshot returns current services matching the service type.
// Pass "" to get all services. Starts a browse session lazily if needed.
// Returns a deep copy so callers can safely mutate the result.
func (o *mdnsObserver) Snapshot(serviceType string) []discoveredService {
	o.ensureSession(serviceType)

	o.mu.Lock()
	defer o.mu.Unlock()

	return o.snapshotLocked(serviceType)
}

// WaitFor blocks until predicate returns true on the current snapshot for the
// given service type, or until ctx expires. The predicate is re-evaluated on
// every mDNS change event.
func (o *mdnsObserver) WaitFor(ctx context.Context, serviceType string, pred func([]discoveredService) bool) ([]discoveredService, error) {
	o.ensureSession(serviceType)

	for {
		o.mu.Lock()
		snap := o.snapshotLocked(serviceType)
		ch := o.notify
		o.mu.Unlock()

		if pred(snap) {
			return snap, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
			// snapshot changed, re-evaluate
		}
	}
}

// ClearSnapshot removes all services matching the given service type from the
// snapshot and wakes any WaitFor callers. Pass "" to clear all types.
// Browse sessions continue running, so new events will repopulate the snapshot.
// This is used before waiting for fresh advertisements (e.g., after zone removal
// the observer may have stale commissionable entries that need to be forgotten
// before waiting for the device to re-advertise).
func (o *mdnsObserver) ClearSnapshot(serviceType string) {
	resolved := resolveServiceType(serviceType)

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stopped {
		return
	}

	if resolved == "" {
		o.services = make(map[serviceKey]discoveredService)
	} else {
		for key := range o.services {
			if key.serviceType == resolved {
				delete(o.services, key)
			}
		}
	}
	o.broadcast()
}

// Stop tears down all browse sessions and the underlying browser.
// After Stop, Snapshot returns empty slices and WaitFor returns immediately.
func (o *mdnsObserver) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stopped {
		return
	}
	o.stopped = true

	for svcType, cancel := range o.sessions {
		cancel()
		delete(o.sessions, svcType)
	}

	o.browser.Stop()
	o.services = make(map[serviceKey]discoveredService)
	o.broadcast()
}

// snapshotLocked returns a filtered copy of services. Caller must hold o.mu.
func (o *mdnsObserver) snapshotLocked(serviceType string) []discoveredService {
	resolved := resolveServiceType(serviceType)
	var result []discoveredService
	for _, svc := range o.services {
		if resolved == "" || svc.ServiceType == resolved {
			// Deep copy TXTRecords
			txt := make(map[string]string, len(svc.TXTRecords))
			for k, v := range svc.TXTRecords {
				txt[k] = v
			}
			// Deep copy Addresses
			addrs := make([]string, len(svc.Addresses))
			copy(addrs, svc.Addresses)
			cp := svc
			cp.TXTRecords = txt
			cp.Addresses = addrs
			result = append(result, cp)
		}
	}
	return result
}

// broadcast closes the current notify channel and creates a new one,
// waking all WaitFor callers. Caller must hold o.mu.
func (o *mdnsObserver) broadcast() {
	close(o.notify)
	o.notify = make(chan struct{})
}

// ensureSession starts a browse session for the given service type if one
// isn't already running. Pass "" to start commissionable browsing.
func (o *mdnsObserver) ensureSession(serviceType string) {
	resolved := resolveServiceType(serviceType)
	if resolved == "" {
		// "" means all -- start all known session types
		o.ensureSession(discovery.ServiceTypeCommissionable)
		o.ensureSession(discovery.ServiceTypeOperational)
		o.ensureSession(discovery.ServiceTypePairingRequest)
		return
	}

	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return
	}
	if _, exists := o.sessions[resolved]; exists {
		o.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	o.sessions[resolved] = cancel
	o.mu.Unlock()

	switch resolved {
	case discovery.ServiceTypeCommissionable:
		o.startCommissionableBrowse(ctx)
	case discovery.ServiceTypeOperational:
		o.startOperationalBrowse(ctx)
	case discovery.ServiceTypePairingRequest:
		o.startPairingRequestBrowse(ctx)
	}
}

// startCommissionableBrowse starts a persistent commissionable browse session.
func (o *mdnsObserver) startCommissionableBrowse(ctx context.Context) {
	added, removed, err := o.browser.BrowseCommissionable(ctx)
	if err != nil {
		o.debugf("mdnsObserver: commissionable browse error: %v", err)
		return
	}

	go func() {
		for svc := range added {
			o.addService(commissionableToDiscovered(svc))
		}
	}()

	go func() {
		for svc := range removed {
			o.removeService(serviceKey{
				instanceName: svc.InstanceName,
				serviceType:  discovery.ServiceTypeCommissionable,
			})
		}
	}()
}

// startOperationalBrowse starts a persistent operational browse session.
func (o *mdnsObserver) startOperationalBrowse(ctx context.Context) {
	ch, err := o.browser.BrowseOperational(ctx, "")
	if err != nil {
		o.debugf("mdnsObserver: operational browse error: %v", err)
		return
	}

	go func() {
		for svc := range ch {
			o.addService(operationalToDiscovered(svc))
		}
	}()
}

// startPairingRequestBrowse starts a persistent pairing request browse session.
func (o *mdnsObserver) startPairingRequestBrowse(ctx context.Context) {
	err := o.browser.BrowsePairingRequests(ctx, func(svc discovery.PairingRequestService) {
		o.addService(pairingRequestToDiscovered(&svc))
	})
	if err != nil {
		o.debugf("mdnsObserver: pairing request browse error: %v", err)
	}
}

// addService adds or updates a service in the snapshot and notifies waiters.
func (o *mdnsObserver) addService(svc discoveredService) {
	key := serviceKey{instanceName: svc.InstanceName, serviceType: svc.ServiceType}
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stopped {
		return
	}
	o.services[key] = svc
	o.broadcast()
}

// removeService removes a service from the snapshot and notifies waiters.
func (o *mdnsObserver) removeService(key serviceKey) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.stopped {
		return
	}
	if _, exists := o.services[key]; exists {
		delete(o.services, key)
		o.broadcast()
	}
}

// ---------------------------------------------------------------------------
// Service type conversion helpers
// ---------------------------------------------------------------------------

func commissionableToDiscovered(svc *discovery.CommissionableService) discoveredService {
	catParts := make([]string, len(svc.Categories))
	for i, c := range svc.Categories {
		catParts[i] = strconv.FormatUint(uint64(c), 10)
	}
	return discoveredService{
		InstanceName:  svc.InstanceName,
		Host:          svc.Host,
		Port:          svc.Port,
		Addresses:     svc.Addresses,
		ServiceType:   discovery.ServiceTypeCommissionable,
		Discriminator: svc.Discriminator,
		// DEC-078 mandates D / cat / serial / brand / model on every
		// commissionable record; mirror that here so verify_txt_records
		// sees the same key set the device actually advertises. The old
		// runner-side synthesizer in handleVerifyTXTRecordsReal was
		// removed in lockstep with mash-py-tdd-plan.md Phase 21.
		TXTRecords: map[string]string{
			"D":      strconv.FormatUint(uint64(svc.Discriminator), 10),
			"brand":  svc.Brand,
			"model":  svc.Model,
			"DN":     svc.DeviceName,
			"serial": svc.Serial,
			"cat":    strings.Join(catParts, ","),
		},
	}
}

func operationalToDiscovered(svc *discovery.OperationalService) discoveredService {
	return discoveredService{
		InstanceName: svc.InstanceName,
		Host:         svc.Host,
		Port:         svc.Port,
		Addresses:    svc.Addresses,
		ServiceType:  discovery.ServiceTypeOperational,
		TXTRecords: map[string]string{
			"ZI": svc.ZoneID,
			"DI": svc.DeviceID,
		},
	}
}

func pairingRequestToDiscovered(svc *discovery.PairingRequestService) discoveredService {
	return discoveredService{
		InstanceName:  svc.InstanceName,
		Host:          svc.Host,
		Port:          svc.Port,
		Addresses:     svc.Addresses,
		ServiceType:   discovery.ServiceTypePairingRequest,
		Discriminator: svc.Discriminator,
		TXTRecords: map[string]string{
			"ZI": svc.ZoneID,
			"ZN": svc.ZoneName,
		},
	}
}

// ---------------------------------------------------------------------------
// Runner integration
// ---------------------------------------------------------------------------

// getOrCreateObserver returns the Runner's current observer, creating one
// lazily if needed. Uses a real MDNSBrowser in production.
func (r *Runner) getOrCreateObserver() *mdnsObserver {
	if r.observer != nil {
		return r.observer
	}
	browser, err := discovery.NewMDNSBrowser(discovery.DefaultBrowserConfig())
	if err != nil {
		r.debugf("getOrCreateObserver: failed to create browser: %v", err)
		return nil
	}
	r.observer = newMDNSObserver(browser, r.debugf)
	return r.observer
}

// stopObserver stops and clears the current mDNS observer.
func (r *Runner) stopObserver() {
	if r.observer != nil {
		r.observer.Stop()
		r.observer = nil
	}
}

// resolveServiceType maps aliases to canonical service type strings.
// Returns "" if the input is "" (meaning all types).
func resolveServiceType(serviceType string) string {
	switch serviceType {
	case discovery.ServiceTypeCommissionable, ServiceAliasCommissionable:
		return discovery.ServiceTypeCommissionable
	case discovery.ServiceTypeOperational, ServiceAliasOperational:
		return discovery.ServiceTypeOperational
	case discovery.ServiceTypePairingRequest, ServiceAliasPairingRequest:
		return discovery.ServiceTypePairingRequest
	case "":
		return ""
	default:
		return fmt.Sprintf("unknown(%s)", serviceType)
	}
}
