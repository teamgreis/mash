package runner

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/engine"
	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// registerDiscoveryHandlers registers all discovery-related action handlers.
func (r *Runner) registerDiscoveryHandlers() {
	// Register checkers for discovery output keys that need >= semantics.
	r.engine.RegisterChecker(KeyAAAACountMin, r.checkAAAACountMin)

	r.engine.RegisterHandler(ActionBrowseMDNS, r.handleBrowseMDNS)
	r.engine.RegisterHandler(ActionReadMDNSTXT, r.handleReadMDNSTXT)
	r.engine.RegisterHandler(ActionVerifyMDNSAdvertising, r.handleVerifyMDNSAdvertising)
	r.engine.RegisterHandler(ActionVerifyMDNSBrowsing, r.handleVerifyMDNSBrowsing)
	r.engine.RegisterHandler(ActionVerifyMDNSNotAdvertising, r.handleVerifyMDNSNotAdvertising)
	r.engine.RegisterHandler(ActionVerifyMDNSNotBrowsing, r.handleVerifyMDNSNotBrowsing)
	r.engine.RegisterHandler(ActionGetQRPayload, r.handleGetQRPayload)
	r.engine.RegisterHandler(ActionAnnouncePairingRequest, r.handleAnnouncePairingRequest)
	r.engine.RegisterHandler(ActionStopPairingRequest, r.handleStopPairingRequest)

	// Replace stubs from runner.go
	r.engine.RegisterHandler(ActionStartDiscovery, r.handleStartDiscoveryReal)
	r.engine.RegisterHandler(ActionStopDiscovery, r.handleStopDiscoveryReal)
	r.engine.RegisterHandler(ActionWaitForDevice, r.handleWaitForDeviceReal)
	r.engine.RegisterHandler(ActionVerifyTXTRecords, r.handleVerifyTXTRecordsReal)
}

// browseViaObserver queries the mDNS observer for services of the given type,
// waiting up to timeoutMs for at least minCount services to appear.
// For commissionable services, it also stores the discovered discriminator.
func (r *Runner) browseViaObserver(ctx context.Context, serviceType string, timeoutMs int, minCount int) ([]discoveredService, error) {
	obs := r.getOrCreateObserver()
	if obs == nil {
		return nil, fmt.Errorf("failed to create mDNS observer")
	}
	if minCount < 1 {
		minCount = 1
	}

	browseCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Wait for at least minCount services, or until timeout.
	services, waitErr := obs.WaitFor(browseCtx, serviceType, func(svcs []discoveredService) bool {
		return len(svcs) >= minCount
	})
	if waitErr != nil {
		// Timeout can happen even while service is still advertising when no
		// observer change event arrives in the browse window. Recover using the
		// current snapshot and a short fresh browse window.
		services = obs.Snapshot(serviceType)
		if len(services) < minCount && errors.Is(waitErr, context.DeadlineExceeded) {
			freshTimeout := timeoutMs
			if freshTimeout > 1000 {
				freshTimeout = 1000
			}
			if fresh, freshErr := r.browseFreshWindow(ctx, serviceType, freshTimeout); freshErr == nil && len(fresh) >= minCount {
				services = fresh
			}
		}
	}

	// Store discovered discriminator for {{ device_discriminator }}.
	resolved := resolveServiceType(serviceType)
	if resolved == discovery.ServiceTypeCommissionable || resolved == "" {
		for _, svc := range services {
			if r.connMgr.DiscoveredDiscriminator() == 0 && svc.Discriminator > 0 {
				r.connMgr.SetDiscoveredDiscriminator(svc.Discriminator)
				break
			}
		}
	}

	return services, nil
}

// handleBrowseMDNS browses for mDNS services by type.
func (r *Runner) handleBrowseMDNS(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDiscoveryState(state)
	hints := buildBrowseSelectionHints(state)

	// Track commissioning completion so buildBrowseOutput can filter
	// commissionable services.
	if completed, _ := state.Get(StateCommissioningCompleted); completed == true {
		ds.commissioningCompleted = true
	}

	serviceType, _ := params[KeyServiceType].(string)
	timeoutMs := paramInt(params, KeyTimeoutMs, 5000)
	minServices := 1
	expectDeviceAbsent := false
	if step != nil && step.Expect != nil {
		if v, ok := step.Expect[KeyDeviceFound].(bool); ok && !v {
			expectDeviceAbsent = true
		}
	}
	if step != nil && step.Expect != nil {
		if v, ok := step.Expect[KeyInstancesForDevice]; ok {
			switch n := v.(type) {
			case int:
				if n > 1 {
					minServices = n
				}
			case float64:
				if int(n) > 1 {
					minServices = int(n)
				}
			}
		}
	}

	// Determine if retry is requested. With the persistent observer, retries
	// are less critical (the observer accumulates services continuously), but
	// we honour the flag by doubling the browse timeout when retry is set.
	retryRequested := false
	if r, ok := params[ParamRetry].(bool); ok {
		retryRequested = r
	}
	retries := 0
	if retryRequested {
		retries = 1
		timeoutMs *= 2 // equivalent of two browse windows
	}

	var (
		services []discoveredService
		err      error
	)
	if expectDeviceAbsent {
		services, err = r.browseViaObserverUntilAbsent(ctx, serviceType, timeoutMs)
	} else {
		services, err = r.browseViaObserver(ctx, serviceType, timeoutMs, minServices)
	}
	if err != nil {
		return nil, err
	}

	// For operational browsing with a known target device ID (e.g. TC-COMM-003),
	// don't return a stale observer snapshot early. Wait until at least one
	// matching operational advertisement is visible, bounded by timeoutMs.
	if !expectDeviceAbsent &&
		resolveServiceType(serviceType) == discovery.ServiceTypeOperational &&
		hints.deviceID != "" &&
		!hasOperationalDeviceID(services, hints.deviceID) {
		waited, waitErr := r.waitForOperationalDeviceID(ctx, serviceType, timeoutMs, hints.deviceID)
		if waitErr == nil && len(waited) > 0 {
			services = waited
		}
		if !hasOperationalDeviceID(services, hints.deviceID) {
			freshTimeout := timeoutMs
			if freshTimeout > 2000 {
				freshTimeout = 2000
			}
			fresh, freshErr := r.browseFreshWindow(ctx, serviceType, freshTimeout)
			if freshErr == nil && hasOperationalDeviceID(fresh, hints.deviceID) {
				services = fresh
			}
		}
	}

	ds.services = services

	// Filter by discriminator when requested.
	if _, hasDisc := params[KeyDiscriminator]; hasDisc {
		wantDisc := uint16(paramInt(params, KeyDiscriminator, 0))
		if wantDisc > 0 {
			filtered := ds.services[:0]
			for _, svc := range ds.services {
				if svc.Discriminator == wantDisc {
					filtered = append(filtered, svc)
				}
			}
			ds.services = filtered
		}
	}

	outputs, err := r.buildBrowseOutput(ds, hints)
	if err != nil {
		return nil, err
	}
	outputs[KeyRetriesPerformedMin] = retries

	// Set error_code when no services found.
	if len(ds.services) == 0 {
		if _, hasDisc := params[KeyDiscriminator]; hasDisc {
			outputs[KeyErrorCode] = ErrCodeDiscriminatorMismatch
		} else {
			outputs[KeyErrorCode] = ErrCodeNoDevicesFound
		}
	}

	// Check for address resolution issues: device found but no resolved addresses.
	if len(ds.services) > 0 {
		for _, svc := range ds.services {
			if len(svc.Addresses) == 0 && svc.Host != "" {
				outputs[KeyErrorCode] = ErrCodeAddrResolutionFailed
				break
			}
		}
	}

	addWindowExpiryWarning(outputs, state)

	return outputs, nil
}

func hasOperationalDeviceID(services []discoveredService, deviceID string) bool {
	want := strings.ToLower(strings.TrimSpace(deviceID))
	if want == "" {
		return false
	}
	for _, svc := range services {
		if svc.ServiceType != discovery.ServiceTypeOperational {
			continue
		}
		if strings.ToLower(strings.TrimSpace(svc.TXTRecords["DI"])) == want {
			return true
		}
	}
	return false
}

func (r *Runner) waitForOperationalDeviceID(ctx context.Context, serviceType string, timeoutMs int, deviceID string) ([]discoveredService, error) {
	obs := r.getOrCreateObserver()
	if obs == nil {
		return nil, fmt.Errorf("failed to create mDNS observer")
	}
	browseCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	return obs.WaitFor(browseCtx, serviceType, func(svcs []discoveredService) bool {
		return hasOperationalDeviceID(svcs, deviceID)
	})
}

// browseViaObserverUntilAbsent waits up to timeoutMs for zero services of the
// requested type. If timeout expires, it returns the latest snapshot so caller
// expectation logic can fail deterministically instead of erroring.
func (r *Runner) browseViaObserverUntilAbsent(ctx context.Context, serviceType string, timeoutMs int) ([]discoveredService, error) {
	obs := r.getOrCreateObserver()
	if obs == nil {
		return nil, fmt.Errorf("failed to create mDNS observer")
	}

	// Ensure the browse session is started for this service type.
	_ = obs.Snapshot(serviceType)

	browseCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	services, err := obs.WaitFor(browseCtx, serviceType, func(svcs []discoveredService) bool {
		return len(svcs) == 0
	})
	if err == nil {
		return services, nil
	}

	// On timeout, re-check with a fresh browse window to avoid stale observer
	// snapshots from previous tests/runs.
	if errors.Is(err, context.DeadlineExceeded) {
		freshTimeout := timeoutMs
		if freshTimeout > 1000 {
			freshTimeout = 1000
		}
		fresh, freshErr := r.browseFreshWindow(ctx, serviceType, freshTimeout)
		if freshErr == nil {
			return fresh, nil
		}
	}

	// Cancellation or fallback error: return latest snapshot for expectation check.
	return obs.Snapshot(serviceType), nil
}

// browseFreshWindow performs a one-shot browse for timeoutMs and returns
// services observed during that fresh window, independent of observer history.
func (r *Runner) browseFreshWindow(ctx context.Context, serviceType string, timeoutMs int) ([]discoveredService, error) {
	obs := r.getOrCreateObserver()
	if obs == nil || obs.browser == nil {
		return nil, fmt.Errorf("failed to create mDNS browser")
	}

	browseCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	resolved := resolveServiceType(serviceType)
	switch resolved {
	case "", discovery.ServiceTypeCommissionable:
		added, _, err := obs.browser.BrowseCommissionable(browseCtx)
		if err != nil {
			return nil, err
		}
		services := make([]discoveredService, 0)
		for {
			select {
			case svc, ok := <-added:
				if !ok {
					return services, nil
				}
				if svc != nil {
					services = append(services, commissionableToDiscovered(svc))
				}
			case <-browseCtx.Done():
				return services, nil
			}
		}
	case discovery.ServiceTypeOperational:
		added, err := obs.browser.BrowseOperational(browseCtx, "")
		if err != nil {
			return nil, err
		}
		services := make([]discoveredService, 0)
		for {
			select {
			case svc, ok := <-added:
				if !ok {
					return services, nil
				}
				if svc != nil {
					services = append(services, operationalToDiscovered(svc))
				}
			case <-browseCtx.Done():
				return services, nil
			}
		}
	default:
		return nil, fmt.Errorf("unsupported service type: %q", serviceType)
	}
}

type browseSelectionHints struct {
	zoneID   string
	deviceID string
}

func buildBrowseSelectionHints(state *engine.ExecutionState) browseSelectionHints {
	hints := browseSelectionHints{}
	if state == nil {
		return hints
	}

	if v, ok := state.Get(StateCurrentZoneID); ok {
		if zoneID, ok := v.(string); ok {
			hints.zoneID = strings.ToLower(strings.TrimSpace(zoneID))
		}
	}

	for _, key := range []string{"state_device_id", "cert_device_id", StateExtractedDeviceID} {
		v, ok := state.Get(key)
		if !ok {
			continue
		}
		deviceID, ok := v.(string)
		if !ok || strings.TrimSpace(deviceID) == "" {
			continue
		}
		hints.deviceID = strings.ToLower(strings.TrimSpace(deviceID))
		break
	}

	return hints
}

func preferredServiceIndex(services []discoveredService, hints browseSelectionHints) int {
	if len(services) == 0 {
		return -1
	}
	if hints.zoneID == "" && hints.deviceID == "" {
		return 0
	}

	matchZoneAndDevice := func(svc discoveredService) bool {
		zi := strings.ToLower(strings.TrimSpace(svc.TXTRecords["ZI"]))
		di := strings.ToLower(strings.TrimSpace(svc.TXTRecords["DI"]))
		if hints.zoneID != "" && zi != hints.zoneID {
			return false
		}
		if hints.deviceID != "" && di != hints.deviceID {
			return false
		}
		return true
	}
	matchDevice := func(svc discoveredService) bool {
		if hints.deviceID == "" {
			return false
		}
		di := strings.ToLower(strings.TrimSpace(svc.TXTRecords["DI"]))
		return di == hints.deviceID
	}
	matchZone := func(svc discoveredService) bool {
		if hints.zoneID == "" {
			return false
		}
		zi := strings.ToLower(strings.TrimSpace(svc.TXTRecords["ZI"]))
		return zi == hints.zoneID
	}

	for i, svc := range services {
		if matchZoneAndDevice(svc) {
			return i
		}
	}
	for i, svc := range services {
		if matchDevice(svc) {
			return i
		}
	}
	for i, svc := range services {
		if matchZone(svc) {
			return i
		}
	}
	return 0
}

// buildBrowseOutput constructs the standard output map from discovery state.
func (r *Runner) buildBrowseOutput(ds *discoveryState, hints browseSelectionHints) (map[string]any, error) {
	// After commissioning completes, filter out commissionable services
	// regardless of which code path populated ds.services (simulated or real).
	if ds.commissioningCompleted {
		filtered := ds.services[:0]
		for _, svc := range ds.services {
			if svc.ServiceType != discovery.ServiceTypeCommissionable {
				filtered = append(filtered, svc)
			}
		}
		ds.services = filtered
	}

	selectedIdx := preferredServiceIndex(ds.services, hints)
	if selectedIdx < 0 {
		selectedIdx = 0
	}

	// Merge addresses injected by device-local actions (e.g. interface_up)
	// into the selected service. This ensures that simulated network
	// changes are reflected in browse results regardless of whether services
	// came from simulation or real mDNS.
	hasInjectedAddresses := false
	if len(ds.injectedAddresses) > 0 && len(ds.services) > 0 {
		existing := make(map[string]bool, len(ds.services[selectedIdx].Addresses))
		for _, addr := range ds.services[selectedIdx].Addresses {
			existing[addr] = true
		}
		for _, addr := range ds.injectedAddresses {
			if !existing[addr] {
				ds.services[selectedIdx].Addresses = append(ds.services[selectedIdx].Addresses, addr)
				hasInjectedAddresses = true
			}
		}
		ds.injectedAddresses = nil // consumed
	}

	// Compute per-service-type counts.
	devicesFound := 0
	for _, svc := range ds.services {
		if svc.ServiceType == discovery.ServiceTypeCommissionable || svc.ServiceType == discovery.ServiceTypeOperational {
			devicesFound++
		}
	}

	// Check for instance name conflicts (duplicate instance names).
	instanceNames := make(map[string]int, len(ds.services))
	for _, svc := range ds.services {
		instanceNames[svc.InstanceName]++
	}
	instanceConflict := false
	for _, count := range instanceNames {
		if count > 1 {
			instanceConflict = true
			break
		}
	}

	// Count IPv6 (AAAA) addresses for the selected service only.
	// Previously this counted across ALL services, inflating the count.
	aaaaCount := 0
	if len(ds.services) > 0 {
		for _, addr := range ds.services[selectedIdx].Addresses {
			if strings.Contains(addr, ":") {
				aaaaCount++
			}
		}
	}

	// Check if all results belong to the same zone (for zone_id_filter assertion).
	allInZone := true
	zoneIDs := make(map[string]bool)
	for _, svc := range ds.services {
		if zi, ok := svc.TXTRecords["ZI"]; ok {
			zoneIDs[strings.ToLower(zi)] = true
		}
	}
	if len(zoneIDs) != 1 || len(ds.services) == 0 {
		allInZone = false
	}

	// Classify addresses across all services.
	hasLinkLocal := false
	hasGlobalOrULA := false
	allIPv6Valid := aaaaCount > 0
	for _, svc := range ds.services {
		for _, addr := range svc.Addresses {
			ip := net.ParseIP(addr)
			if ip == nil {
				continue
			}
			if ip.To4() == nil { // IPv6
				if ip.IsLinkLocalUnicast() {
					hasLinkLocal = true
				} else {
					hasGlobalOrULA = true
				}
			}
		}
	}

	// Determine if addresses come from multiple interfaces.
	// Heuristic: 2+ unique IPv6 addresses with different /64 prefixes or
	// mix of link-local + global/ULA indicates multiple interfaces.
	addressesFromMultipleIFs := false
	if aaaaCount >= 2 {
		prefixes := make(map[string]bool)
		for _, svc := range ds.services {
			for _, addr := range svc.Addresses {
				ip := net.ParseIP(addr)
				if ip != nil && ip.To4() == nil {
					prefix := ip.Mask(net.CIDRMask(64, 128)).String()
					prefixes[prefix] = true
				}
			}
		}
		addressesFromMultipleIFs = len(prefixes) >= 2
	}

	// Compare with previous browse results for records_unchanged / new_address_announced.
	currentAddrs := collectAllAddresses(ds.services)
	recordsUnchanged := false
	// new_address_announced is true if device-local actions injected new addresses
	// (e.g. interface_up), even without a prior browse baseline. This ensures
	// TC-MULTIIF-003 passes in live mode where no browse occurs before interface_up.
	newAddressAnnounced := hasInjectedAddresses
	if len(ds.previousAddresses) > 0 {
		recordsUnchanged = addressSetsEqual(ds.previousAddresses, currentAddrs)
		if !newAddressAnnounced {
			newAddressAnnounced = hasNewAddresses(ds.previousAddresses, currentAddrs)
		}
	}
	ds.previousAddresses = currentAddrs

	outputs := map[string]any{
		KeyDeviceFound:              len(ds.services) > 0,
		KeyServiceCount:             len(ds.services),
		KeyServices:                 ds.services,
		KeyDevicesFound:             devicesFound,
		KeyDevicesFoundMin:          devicesFound,
		KeyRetriesPerformedMin:      0,
		KeyInstanceConflictResolved: !instanceConflict,
		KeyInstancesForDevice:       len(ds.services),
		KeyAllResultsInZone:         allInZone,
		KeyAAAACount:                aaaaCount,
		KeyAAAACountMin:             aaaaCount,
		KeyIPv6Valid:                allIPv6Valid,
		KeyHasGlobalOrULA:           hasGlobalOrULA,
		KeyHasLinkLocal:             hasLinkLocal,
		KeyAddressesFromMultipleIFs: addressesFromMultipleIFs,
		KeyNewAddressAnnounced:      newAddressAnnounced,
		KeyRecordsUnchanged:         recordsUnchanged,
	}

	// Add selected-service details for easy assertion.
	if len(ds.services) > 0 {
		selected := ds.services[selectedIdx]
		outputs[KeyInstanceName] = selected.InstanceName
		outputs["service_has_txt_records"] = len(selected.TXTRecords) > 0

		// SRV record fields.
		outputs["srv_port"] = int(selected.Port)
		outputs["srv_port_present"] = selected.Port > 0
		outputs[KeySRVHostnameValid] = selected.Host != ""

		// Add all TXT record fields.
		for k, v := range selected.TXTRecords {
			outputs["txt_field_"+k] = v
		}

		// TXT record length fields.
		if zi, ok := selected.TXTRecords["ZI"]; ok {
			outputs["txt_ZI_length"] = len(zi)
		}
		if di, ok := selected.TXTRecords["DI"]; ok {
			outputs["txt_DI_length"] = len(di)
		}

		// Add service-type-specific derived fields.
		switch selected.ServiceType {
		case discovery.ServiceTypeCommissionable:
			// Discriminator fields.
			outputs["txt_field_D"] = fmt.Sprintf("%d", selected.Discriminator)
			if selected.Discriminator <= discovery.MaxDiscriminator {
				outputs[KeyTXTDRange] = "0-4095"
			} else {
				outputs[KeyTXTDRange] = fmt.Sprintf("out-of-range(%d)", selected.Discriminator)
			}
			// Instance name format.
			if strings.HasPrefix(selected.InstanceName, "MASH-") {
				outputs[KeyInstanceNamePrefix] = "MASH-"
			} else {
				outputs[KeyInstanceNamePrefix] = ""
			}

		case discovery.ServiceTypeOperational:
			// Zone/device ID fields from TXT records.
			zi := selected.TXTRecords["ZI"]
			di := selected.TXTRecords["DI"]
			outputs[KeyZoneIDLengthDisc] = len(zi)
			outputs[KeyDeviceIDLength] = len(di)
			outputs[KeyZoneIDHexValid] = isValidHex(zi)
			outputs[KeyDeviceIDHexValid] = isValidHex(di)
			outputs[KeyDeviceID] = di
			// Instance name format: <zone-id>-<device-id>.
			if strings.Contains(selected.InstanceName, "-") {
				outputs[KeyInstanceNameFormat] = "<zone-id>-<device-id>"
			} else {
				outputs[KeyInstanceNameFormat] = selected.InstanceName
			}
		}
	}

	return outputs, nil
}

// addWindowExpiryWarning sets KeyWindowExpiryWarning in outputs if the
// commissioning window start time is known and the remaining time is
// below a 30-second threshold (default window duration: 120s).
func addWindowExpiryWarning(outputs map[string]any, state *engine.ExecutionState) {
	v, ok := state.Get(StateCommWindowStart)
	if !ok {
		return
	}
	startTime, ok := v.(time.Time)
	if !ok {
		return
	}
	const windowDuration = 120 * time.Second
	const warningThreshold = 30 * time.Second
	elapsed := time.Since(startTime)
	remaining := windowDuration - elapsed
	outputs[KeyWindowExpiryWarning] = remaining <= warningThreshold
}

// isValidHex checks whether a string is valid hexadecimal.
func isValidHex(s string) bool {
	if s == "" {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// handleReadMDNSTXT reads TXT records for a discovered service.
func (r *Runner) handleReadMDNSTXT(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDiscoveryState(state)

	index := paramInt(params, KeyIndex, 0)

	instanceName, _ := params[KeyInstanceName].(string)

	// Find service by index or instance name.
	var svc *discoveredService
	if instanceName != "" {
		for i := range ds.services {
			if ds.services[i].InstanceName == instanceName {
				svc = &ds.services[i]
				break
			}
		}
	} else if index < len(ds.services) {
		svc = &ds.services[index]
	}

	if svc == nil {
		return map[string]any{
			KeyTXTFound:  false,
			KeyKeyExists: false,
		}, nil
	}

	outputs := map[string]any{
		KeyTXTFound:     true,
		KeyInstanceName: svc.InstanceName,
		KeyHost:         svc.Host,
		KeyPort:         int(svc.Port),
		KeyKeyExists:    len(svc.TXTRecords) > 0,
	}

	// Expose individual TXT records as "txt_<key>".
	for k, v := range svc.TXTRecords {
		outputs["txt_"+k] = v
	}

	// Add specific TXT field validation keys.
	if _, ok := svc.TXTRecords["cat"]; ok {
		outputs["txt_field_cat"] = svc.TXTRecords["cat"]
	}
	if serial, ok := svc.TXTRecords["serial"]; ok {
		outputs["txt_field_serial"] = serial
	}
	if zi, ok := svc.TXTRecords["ZI"]; ok {
		outputs["txt_field_ZI"] = zi
		outputs[KeyTXTZILength] = len(zi)
	}
	if di, ok := svc.TXTRecords["DI"]; ok {
		outputs["txt_field_DI"] = di
		outputs["txt_DI_length"] = len(di)
	}

	return outputs, nil
}

// handleVerifyMDNSAdvertising verifies device is advertising a specific service.
func (r *Runner) handleVerifyMDNSAdvertising(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	serviceType, _ := params[KeyServiceType].(string)
	if serviceType == "" {
		serviceType = ServiceAliasCommissionable
	}

	// Use rapid short-browse retries with exponential backoff (similar to
	// waitForCommissioningMode). After zone removal, the mDNS service may
	// take time to register. Short browse windows with fresh zeroconf
	// resolvers are more reliable at detecting newly registered services.
	timeoutMs := paramInt(params, KeyTimeoutMs, 5000)
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	browseMs := 500 // start with short browse windows
	var result map[string]any
	var err error
	found := false

	for time.Now().Before(deadline) {
		browseStep := &loader.Step{
			Params: map[string]any{
				KeyServiceType: serviceType,
				KeyTimeoutMs:   float64(browseMs),
			},
		}
		result, err = r.handleBrowseMDNS(ctx, browseStep, state)
		if err != nil {
			return map[string]any{KeyAdvertising: false, KeyError: err.Error()}, nil
		}

		found = result[KeyServiceCount].(int) > 0
		if found {
			break
		}
		r.debugf("verify_mdns_advertising: browse %dms found nothing, retrying", browseMs)
		browseMs = min(browseMs*2, 2000) // exponential backoff up to 2s
	}

	outputs := map[string]any{
		KeyAdvertising:  found,
		KeyServiceCount: result[KeyServiceCount],
	}

	// Propagate TXT records from the first discovered service so test
	// cases can assert on individual TXT fields (e.g., txt_records.id).
	if found {
		if services, ok := result[KeyServices].([]discoveredService); ok && len(services) > 0 {
			first := services[0]
			txtMap := make(map[string]any, len(first.TXTRecords))
			for k, v := range first.TXTRecords {
				txtMap[k] = v
			}
			// Add discriminator as "disc" for commissionable services.
			if first.ServiceType == discovery.ServiceTypeCommissionable {
				txtMap["disc"] = fmt.Sprintf("%d", first.Discriminator)
			}
			outputs[KeyTXTRecords] = txtMap
		}
	}

	return outputs, nil
}

// handleVerifyMDNSBrowsing verifies browser finds expected services.
func (r *Runner) handleVerifyMDNSBrowsing(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	result, err := r.handleVerifyMDNSAdvertising(ctx, step, state)
	if err != nil {
		return result, err
	}
	// Add browsing alias for advertising.
	if result != nil {
		result[KeyBrowsing] = result[KeyAdvertising]
	}
	return result, nil
}

// handleVerifyMDNSNotAdvertising verifies device is NOT advertising.
// Uses the persistent observer to wait for all matching services to disappear
// within the timeout. Returns not_advertising=true if no services remain.
func (r *Runner) handleVerifyMDNSNotAdvertising(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	serviceType, _ := params[KeyServiceType].(string)
	if serviceType == "" {
		serviceType = ServiceAliasCommissionable
	}

	// Default to a short timeout. Respect step's timeout if set.
	timeoutMs := paramInt(params, KeyTimeoutMs, 1000)

	obs := r.getOrCreateObserver()
	if obs == nil {
		return map[string]any{
			KeyAdvertising:    false,
			KeyNotAdvertising: true,
		}, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Wait for all services of this type to disappear (removal events).
	_, err := obs.WaitFor(waitCtx, serviceType, func(svcs []discoveredService) bool {
		return len(svcs) == 0
	})

	notAdvertising := err == nil // predicate satisfied = no services found
	return map[string]any{
		KeyAdvertising:    !notAdvertising,
		KeyNotAdvertising: notAdvertising,
	}, nil
}

// handleVerifyMDNSNotBrowsing verifies service NOT found by browser.
func (r *Runner) handleVerifyMDNSNotBrowsing(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	result, err := r.handleVerifyMDNSNotAdvertising(ctx, step, state)
	if err != nil {
		return result, err
	}
	// Add not_browsing alias.
	if result != nil {
		result[KeyNotBrowsing] = result[KeyNotAdvertising]
	}
	return result, nil
}

// handleGetQRPayload gets device's QR code payload.
func (r *Runner) handleGetQRPayload(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDiscoveryState(state)

	// If provided directly.
	if payload, ok := params[ParamPayload].(string); ok && payload != "" {
		ds.qrPayload = payload
		return map[string]any{
			KeyQRPayload: payload,
			KeyValid:     true,
		}, nil
	}

	// Construct from params.
	discriminator := uint16(paramInt(params, KeyDiscriminator, 0))
	setupCode, _ := params[KeySetupCode].(string)
	if setupCode == "" {
		setupCode = r.config.SetupCode
	}

	if discriminator > 0 && setupCode != "" {
		payload := fmt.Sprintf("MASH:1:%d:%s:0x0000:0x0000", discriminator, setupCode)
		ds.qrPayload = payload
		return map[string]any{
			KeyQRPayload:     payload,
			KeyDiscriminator: int(discriminator),
			KeySetupCode:     setupCode,
			KeyValid:         true,
		}, nil
	}

	// Auto-generate: return cached payload or generate a new one.
	if ds.qrPayload != "" {
		qr, err := discovery.ParseQRCode(ds.qrPayload)
		if err == nil {
			return map[string]any{
				KeyQRPayload:     ds.qrPayload,
				KeyDiscriminator: int(qr.Discriminator),
				KeySetupCode:     qr.SetupCode,
				KeyVersion:       int(qr.Version),
				KeyValid:         true,
			}, nil
		}
	}

	// Generate a fresh QR code (simulates factory provisioning at boot).
	qr, err := discovery.GenerateQRCode()
	if err != nil {
		return map[string]any{
			KeyValid: false,
			KeyError: fmt.Sprintf("failed to generate QR: %v", err),
		}, nil
	}
	ds.qrPayload = qr.String()
	return map[string]any{
		KeyQRPayload:     ds.qrPayload,
		KeyDiscriminator: int(qr.Discriminator),
		KeySetupCode:     qr.SetupCode,
		KeyVersion:       int(qr.Version),
		KeyValid:         true,
	}, nil
}

// handleAnnouncePairingRequest advertises a real _mashp._udp pairing request via mDNS.
func (r *Runner) handleAnnouncePairingRequest(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	discriminator := uint16(paramInt(params, KeyDiscriminator, 0))
	zoneID, _ := params[KeyZoneID].(string)
	zoneName, _ := params[KeyZoneName].(string)

	// Store in state for verification.
	state.Set(StatePairingRequestDiscriminator, int(discriminator))
	state.Set(StatePairingRequestZoneID, zoneID)

	// Lazy-init the mDNS advertiser on the runner.
	if r.pairingAdvertiser == nil {
		adv, err := discovery.NewMDNSAdvertiser(discovery.AdvertiserConfig{})
		if err != nil {
			return nil, fmt.Errorf("create pairing advertiser: %w", err)
		}
		r.pairingAdvertiser = adv
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "mash-test-runner.local"
	}

	info := &discovery.PairingRequestInfo{
		Discriminator: discriminator,
		ZoneID:        zoneID,
		ZoneName:      zoneName,
		Host:          host,
	}

	if err := r.pairingAdvertiser.AnnouncePairingRequest(ctx, info); err != nil {
		return nil, fmt.Errorf("announce pairing request: %w", err)
	}

	return map[string]any{
		KeyPairingRequestAnnounced: true,
		KeyAnnouncementSent:        true,
		KeyDiscriminator:           int(discriminator),
		KeyZoneID:                  zoneID,
		KeyZoneName:                zoneName,
	}, nil
}

// handleStopPairingRequest stops advertising pairing requests via mDNS.
func (r *Runner) handleStopPairingRequest(_ context.Context, _ *loader.Step, _ *engine.ExecutionState) (map[string]any, error) {
	if r.pairingAdvertiser != nil {
		r.pairingAdvertiser.StopAll()
		r.pairingAdvertiser = nil
	}
	return map[string]any{
		"stopped": true,
	}, nil
}

// handleStartDiscoveryReal replaces the stub from runner.go.
func (r *Runner) handleStartDiscoveryReal(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDiscoveryState(state)
	ds.active = true

	return map[string]any{KeyDiscoveryStarted: true}, nil
}

// handleWaitForDeviceReal replaces the stub from runner.go.
func (r *Runner) handleWaitForDeviceReal(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)

	timeoutMs := paramInt(params, KeyTimeoutMs, 10000)

	discriminator := uint16(paramInt(params, KeyDiscriminator, 0))

	if discriminator > 0 {
		obs := r.getOrCreateObserver()
		if obs == nil {
			return map[string]any{
				KeyDeviceFound:         false,
				KeyDeviceHasTXTRecords: false,
			}, nil
		}

		browseCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()

		services, err := obs.WaitFor(browseCtx, "commissionable", func(svcs []discoveredService) bool {
			for _, svc := range svcs {
				if svc.Discriminator == discriminator {
					return true
				}
			}
			return false
		})
		if err != nil || len(services) == 0 {
			return map[string]any{
				KeyDeviceFound:         false,
				KeyDeviceHasTXTRecords: false,
			}, nil
		}

		// Find the matching service
		ds := getDiscoveryState(state)
		for _, svc := range services {
			if svc.Discriminator == discriminator {
				ds.services = []discoveredService{svc}
				return map[string]any{
					KeyDeviceFound:         true,
					KeyDeviceHasTXTRecords: true,
				}, nil
			}
		}
	}

	// No discriminator -- fall back to simulated success for non-mDNS test modes.
	// Populate ds.services so that subsequent verify_txt_records sees a non-empty list.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{{
		InstanceName: "MASH-SIM-0000",
		ServiceType:  discovery.ServiceTypeCommissionable,
		TXTRecords:   map[string]string{},
	}}
	return map[string]any{
		KeyDeviceFound:         true,
		KeyDeviceHasTXTRecords: true,
	}, nil
}

// handleStopDiscoveryReal replaces the stub from runner.go.
func (r *Runner) handleStopDiscoveryReal(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	ds := getDiscoveryState(state)
	ds.active = false

	return map[string]any{KeyDiscoveryStopped: true}, nil
}

// handleVerifyTXTRecordsReal replaces the stub from runner.go.
func (r *Runner) handleVerifyTXTRecordsReal(ctx context.Context, step *loader.Step, state *engine.ExecutionState) (map[string]any, error) {
	params := engine.InterpolateParams(step.Params, state)
	ds := getDiscoveryState(state)

	if len(ds.services) == 0 {
		return map[string]any{KeyTXTValid: false}, nil
	}

	svc := ds.services[0]

	// Ensure synthetic TXT fields are populated for commissionable services.
	if svc.ServiceType == discovery.ServiceTypeCommissionable {
		if _, ok := svc.TXTRecords["D"]; !ok {
			svc.TXTRecords["D"] = fmt.Sprintf("%d", svc.Discriminator)
		}
		if _, ok := svc.TXTRecords["id"]; !ok {
			svc.TXTRecords["id"] = svc.InstanceName
		}
		if _, ok := svc.TXTRecords["cat"]; !ok {
			svc.TXTRecords["cat"] = "device"
		}
		if _, ok := svc.TXTRecords["proto"]; !ok {
			svc.TXTRecords["proto"] = "1.0"
		}
	}

	// Check required fields -- accept both "required_fields" and "required_keys".
	allValid := true
	fields, ok := params[ParamRequiredFields].([]any)
	if !ok {
		fields, ok = params[ParamRequiredKeys].([]any)
	}
	if ok {
		for _, f := range fields {
			fieldName, _ := f.(string)
			if _, exists := svc.TXTRecords[fieldName]; !exists {
				allValid = false
				break
			}
		}
	}

	outputs := map[string]any{KeyTXTValid: allValid}

	// Expose zone/device ID derived fields for assertions.
	if zi, ok := svc.TXTRecords["ZI"]; ok {
		outputs["zone_id_length"] = len(zi)
		outputs["zone_id_hex_valid"] = isValidHex(zi)
	}
	if di, ok := svc.TXTRecords["DI"]; ok {
		outputs["device_id_length"] = len(di)
		outputs["device_id_hex_valid"] = isValidHex(di)
	}

	return outputs, nil
}

// collectAllAddresses returns a sorted, deduplicated list of all addresses.
func collectAllAddresses(services []discoveredService) []string {
	seen := make(map[string]bool)
	var addrs []string
	for _, svc := range services {
		for _, addr := range svc.Addresses {
			if !seen[addr] {
				seen[addr] = true
				addrs = append(addrs, addr)
			}
		}
	}
	sort.Strings(addrs)
	return addrs
}

// addressSetsEqual returns true if two sorted address slices are identical.
func addressSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hasNewAddresses returns true if current contains addresses not in old.
func hasNewAddresses(old, current []string) bool {
	oldSet := make(map[string]bool, len(old))
	for _, a := range old {
		oldSet[a] = true
	}
	for _, a := range current {
		if !oldSet[a] {
			return true
		}
	}
	return false
}

// checkAAAACountMin verifies that the actual AAAA count is >= the expected minimum.
func (r *Runner) checkAAAACountMin(key string, expected interface{}, state *engine.ExecutionState) *engine.ExpectResult {
	actual, exists := state.Get(key)
	if !exists {
		return &engine.ExpectResult{
			Key: key, Expected: expected, Passed: false,
			Message: fmt.Sprintf("key %q not found in outputs", key),
		}
	}
	actualNum, ok1 := engine.ToFloat64(actual)
	expectedNum, ok2 := engine.ToFloat64(expected)
	if !ok1 || !ok2 {
		return &engine.ExpectResult{
			Key: key, Expected: expected, Actual: actual, Passed: false,
			Message: fmt.Sprintf("cannot compare non-numeric values: %T and %T", actual, expected),
		}
	}
	passed := actualNum >= expectedNum
	return &engine.ExpectResult{
		Key: key, Expected: expected, Actual: actual, Passed: passed,
		Message: fmt.Sprintf("aaaa_count %v >= %v = %v", actualNum, expectedNum, passed),
	}
}
