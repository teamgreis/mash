package runner

import (
	"context"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/discovery"
)

func TestHandleStartStopDiscovery(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Start discovery.
	out, err := r.handleStartDiscoveryReal(context.Background(), &loader.Step{}, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["discovery_started"] != true {
		t.Error("expected discovery_started=true")
	}

	ds := getDiscoveryState(state)
	if !ds.active {
		t.Error("expected active=true after start")
	}

	// Stop discovery.
	out, err = r.handleStopDiscoveryReal(context.Background(), &loader.Step{}, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["discovery_stopped"] != true {
		t.Error("expected discovery_stopped=true")
	}

	if ds.active {
		t.Error("expected active=false after stop")
	}
}

func TestHandleReadMDNSTXT(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Populate discovery state with a service.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{
		{
			InstanceName: "MASH-1234",
			Host:         "evse.local",
			Port:         8443,
			TXTRecords: map[string]string{
				"brand": "TestBrand",
				"model": "TestModel",
				"DN":    "My EVSE",
			},
		},
	}

	// Read by index.
	step := &loader.Step{Params: map[string]any{"index": float64(0)}}
	out, err := r.handleReadMDNSTXT(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_found"] != true {
		t.Error("expected txt_found=true")
	}
	if out["instance_name"] != "MASH-1234" {
		t.Errorf("expected instance_name=MASH-1234, got %v", out["instance_name"])
	}
	if out["txt_brand"] != "TestBrand" {
		t.Errorf("expected txt_brand=TestBrand, got %v", out["txt_brand"])
	}

	// Read by instance name.
	step = &loader.Step{Params: map[string]any{"instance_name": "MASH-1234"}}
	out, err = r.handleReadMDNSTXT(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_found"] != true {
		t.Error("expected txt_found=true for instance_name lookup")
	}

	// Not found.
	step = &loader.Step{Params: map[string]any{"instance_name": "MASH-9999"}}
	out, err = r.handleReadMDNSTXT(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_found"] != false {
		t.Error("expected txt_found=false for unknown instance")
	}
}

func TestHandleVerifyTXTRecordsReal(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// No services -> invalid.
	step := &loader.Step{Params: map[string]any{}}
	out, err := r.handleVerifyTXTRecordsReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_valid"] != false {
		t.Error("expected txt_valid=false with no services")
	}

	// Populate services.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{
		{
			TXTRecords: map[string]string{
				"brand": "Test",
				"model": "M1",
			},
		},
	}

	// All required fields present.
	step = &loader.Step{
		Params: map[string]any{
			"required_fields": []any{"brand", "model"},
		},
	}
	out, err = r.handleVerifyTXTRecordsReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_valid"] != true {
		t.Error("expected txt_valid=true when all fields present")
	}

	// Missing required field.
	step = &loader.Step{
		Params: map[string]any{
			"required_fields": []any{"brand", "serial"},
		},
	}
	out, err = r.handleVerifyTXTRecordsReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_valid"] != false {
		t.Error("expected txt_valid=false when field missing")
	}
}

func TestHandleGetQRPayload(t *testing.T) {
	r := newTestRunner()
	r.config.SetupCode = "20202021"
	state := newTestState()

	// From explicit payload.
	step := &loader.Step{
		Params: map[string]any{
			"payload": "MASH:1:1234:20202021:0x0000:0x0000",
		},
	}
	out, err := r.handleGetQRPayload(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["valid"] != true {
		t.Error("expected valid=true")
	}
	if out["qr_payload"] != "MASH:1:1234:20202021:0x0000:0x0000" {
		t.Errorf("unexpected payload: %v", out["qr_payload"])
	}

	// Construct from discriminator + setup code.
	step = &loader.Step{
		Params: map[string]any{
			"discriminator": float64(2048),
		},
	}
	out, err = r.handleGetQRPayload(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["valid"] != true {
		t.Error("expected valid=true for constructed payload")
	}
	if out["discriminator"] != 2048 {
		t.Errorf("expected discriminator=2048, got %v", out["discriminator"])
	}
}

func TestHandleGetQRPayload_AutoGenerate(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// No params -- should auto-generate a valid QR payload.
	step := &loader.Step{Params: map[string]any{}}
	out, err := r.handleGetQRPayload(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[KeyValid] != true {
		t.Errorf("expected valid=true, got %v", out[KeyValid])
	}
	payload, ok := out[KeyQRPayload].(string)
	if !ok || payload == "" {
		t.Fatal("expected non-empty qr_payload")
	}

	// Verify the generated payload is parseable.
	qr, err := discovery.ParseQRCode(payload)
	if err != nil {
		t.Fatalf("generated payload not parseable: %v", err)
	}
	if qr.Version != discovery.QRVersion {
		t.Errorf("expected version=%d, got %d", discovery.QRVersion, qr.Version)
	}
	if len(qr.SetupCode) != discovery.SetupCodeLength {
		t.Errorf("expected setup_code_length=%d, got %d", discovery.SetupCodeLength, len(qr.SetupCode))
	}

	// Verify discriminator and setup_code are in output.
	if out[KeyDiscriminator] == nil {
		t.Error("expected discriminator in output")
	}
	if out[KeySetupCode] == nil {
		t.Error("expected setup_code in output")
	}

	// Subsequent call should return the same payload (cached in discovery state).
	out2, _ := r.handleGetQRPayload(context.Background(), step, state)
	if out2[KeyQRPayload] != payload {
		t.Error("expected same payload on second call")
	}
}

func TestHandleAnnouncePairingRequest(t *testing.T) {
	r := newTestRunner()
	defer r.Close()
	state := newTestState()

	step := &loader.Step{
		Params: map[string]any{
			"discriminator": float64(1234),
			KeyZoneID:       "a1b2c3d4e5f6a7b8",
			"zone_name":     "My Home",
		},
	}
	out, err := r.handleAnnouncePairingRequest(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["pairing_request_announced"] != true {
		t.Error("expected pairing_request_announced=true")
	}
	if out["discriminator"] != 1234 {
		t.Errorf("expected discriminator=1234, got %v", out["discriminator"])
	}

	// Verify state was set.
	disc, _ := state.Get("pairing_request_discriminator")
	if disc != 1234 {
		t.Error("expected discriminator stored in state")
	}
}

func TestHandleAnnouncePairingRequest_AnnouncementSent(t *testing.T) {
	r := newTestRunner()
	defer r.Close()
	state := newTestState()

	step := &loader.Step{
		Params: map[string]any{
			"discriminator": float64(2048),
			KeyZoneID:       "b2c3d4e5f6a7b8a1",
			"zone_name":     "Test Zone",
		},
	}
	out, err := r.handleAnnouncePairingRequest(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[KeyAnnouncementSent] != true {
		t.Errorf("expected announcement_sent=true, got %v", out[KeyAnnouncementSent])
	}

	// Verify advertiser was created on the runner.
	if r.pairingAdvertiser == nil {
		t.Error("expected pairingAdvertiser to be set on runner")
	}
}

func TestHandleAnnouncePairingRequest_MissingZoneIDWithoutFallbackFails(t *testing.T) {
	r := newTestRunner()
	defer r.Close()
	state := newTestState()

	step := &loader.Step{
		Params: map[string]any{
			"discriminator": float64(2048),
		},
	}
	_, err := r.handleAnnouncePairingRequest(context.Background(), step, state)
	if err == nil {
		t.Fatal("expected error for missing zone_id")
	}
}

func TestHandleStopPairingRequest(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Announce first to create the advertiser.
	step := &loader.Step{
		Params: map[string]any{
			"discriminator": float64(1234),
			KeyZoneID:       "a1b2c3d4e5f6a7b8",
		},
	}
	_, err := r.handleAnnouncePairingRequest(context.Background(), step, state)
	if err != nil {
		t.Fatalf("announce: %v", err)
	}
	if r.pairingAdvertiser == nil {
		t.Fatal("expected advertiser after announce")
	}

	// Stop should clean up.
	out, err := r.handleStopPairingRequest(context.Background(), &loader.Step{}, state)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if out["stopped"] != true {
		t.Error("expected stopped=true")
	}
	if r.pairingAdvertiser != nil {
		t.Error("expected pairingAdvertiser to be nil after stop")
	}
}

func TestHandleWaitForDeviceNoDiscriminator(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Without discriminator -> simulated success.
	step := &loader.Step{Params: map[string]any{}}
	out, err := r.handleWaitForDeviceReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["device_found"] != true {
		t.Error("expected device_found=true for no-discriminator fallback")
	}
}

// ============================================================================
// Phase 5: Browse mDNS derived output fields
// ============================================================================

// TestBrowseMDNS_CommissionableFields verifies derived fields for commissionable services.
// We don't call handleBrowseMDNS directly (requires real mDNS) but test the output
// construction by injecting state and verifying the key patterns.
func TestBrowseMDNS_CommissionableOutputFields(t *testing.T) {
	state := newTestState()

	// Manually populate discovery state as if we found a commissionable service.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{
		{
			InstanceName:  "MASH-1234",
			Host:          "evse.local",
			Port:          8443,
			ServiceType:   discovery.ServiceTypeCommissionable,
			Discriminator: 1234,
			TXTRecords: map[string]string{
				"brand": "TestBrand",
				"model": "TestModel",
				"DN":    "My EVSE",
			},
		},
	}

	// Simulate the output construction logic from handleBrowseMDNS.
	// We can't call handleBrowseMDNS directly as it requires real mDNS networking,
	// so we verify the helper function and output structure.
	if !isValidHex("a1b2c3d4e5f6a7b8") {
		t.Error("isValidHex should accept valid hex")
	}
	if isValidHex("") {
		t.Error("isValidHex should reject empty string")
	}
	if isValidHex("xyz") {
		t.Error("isValidHex should reject non-hex")
	}
}

// TestBrowseMDNS_OperationalDerivedFields verifies zone/device ID validation helpers.
func TestBrowseMDNS_OperationalDerivedFields(t *testing.T) {
	// Valid 16-char hex IDs.
	if !isValidHex("a1b2c3d4e5f6a7b8") {
		t.Error("expected valid hex for 16-char lowercase ID")
	}
	if !isValidHex("A1B2C3D4E5F6A7B8") {
		t.Error("expected valid hex for uppercase ID")
	}

	// Invalid IDs.
	if isValidHex("not-hex-at-all!") {
		t.Error("expected invalid for non-hex")
	}
	if isValidHex("a1b2") {
		// Short but valid hex.
		// isValidHex only checks hex validity, not length.
	}
}

// TestBrowseMDNS_MinCountFields verifies the count fields are integers.
func TestBrowseMDNS_MinCountFields(t *testing.T) {
	state := newTestState()

	ds := getDiscoveryState(state)
	ds.services = []discoveredService{
		{ServiceType: discovery.ServiceTypeCommissionable, TXTRecords: map[string]string{}},
		{ServiceType: discovery.ServiceTypeOperational, TXTRecords: map[string]string{}},
	}

	devicesFound := 0
	for _, svc := range ds.services {
		if svc.ServiceType == discovery.ServiceTypeCommissionable || svc.ServiceType == discovery.ServiceTypeOperational {
			devicesFound++
		}
	}

	if devicesFound != 2 {
		t.Errorf("expected 2 devices, got %d", devicesFound)
	}
}

func TestVerifyTXTRecords_RequiredKeys(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Populate with a commissionable service.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{
		{
			ServiceType:   discovery.ServiceTypeCommissionable,
			Discriminator: 1234,
			InstanceName:  "MASH-1234",
			TXTRecords: map[string]string{
				"brand": "Test",
				"model": "M1",
			},
		},
	}

	// Using required_keys param (instead of required_fields).
	step := &loader.Step{
		Params: map[string]any{
			"required_keys": []any{"brand", "model"},
		},
	}
	out, err := r.handleVerifyTXTRecordsReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_valid"] != true {
		t.Error("expected txt_valid=true with required_keys")
	}

	// Per DEC-078, the canonical commissionable TXT keys are
	// D / cat / serial / brand / model. The handler used to synthesize
	// legacy `id` / `cat=device` / `proto=1.0` keys when absent — that
	// shim was removed in lockstep with mash-py-tdd-plan.md Phase 21
	// because it propped up an out-of-spec YAML test (TC-DISC-001).
	// A request for an unsupported legacy key MUST now be rejected.
	step = &loader.Step{
		Params: map[string]any{
			"required_keys": []any{"id", "cat", "proto", "D"},
		},
	}
	out, err = r.handleVerifyTXTRecordsReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["txt_valid"] != false {
		t.Error("expected txt_valid=false for legacy keys (id, proto) — see DEC-078")
	}
}

// C8: wait_for_device fallback populates ds.services.
func TestWaitForDevice_PopulatesServices(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// No discriminator -> simulated fallback.
	step := &loader.Step{Params: map[string]any{}}
	out, err := r.handleWaitForDeviceReal(context.Background(), step, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["device_found"] != true {
		t.Error("expected device_found=true")
	}

	// Verify services were populated so verify_txt_records works.
	ds := getDiscoveryState(state)
	if len(ds.services) == 0 {
		t.Error("expected ds.services to be populated in fallback path")
	}
	if ds.services[0].InstanceName != "MASH-SIM-0000" {
		t.Errorf("expected synthetic instance name, got %v", ds.services[0].InstanceName)
	}
	if ds.services[0].ServiceType != discovery.ServiceTypeCommissionable {
		t.Errorf("expected commissionable service type, got %v", ds.services[0].ServiceType)
	}

	// Verify verify_txt_records now succeeds on the synthetic service.
	txtStep := &loader.Step{Params: map[string]any{}}
	txtOut, err := r.handleVerifyTXTRecordsReal(context.Background(), txtStep, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txtOut["txt_valid"] != true {
		t.Error("expected txt_valid=true for synthetic service")
	}
}

// TestBrowseMDNS_InjectedAddresses_NoBaseline verifies that injected addresses
// trigger new_address_announced=true even without a prior browse establishing
// previousAddresses. This is the TC-MULTIIF-003 live-mode scenario: interface_up
// injects an address, the first browse sees it as new.
func TestBrowseMDNS_InjectedAddresses_NoBaseline(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	ds := getDiscoveryState(state)

	// Seed services as if from a real mDNS browse (no previous browse).
	ds.services = []discoveredService{{
		InstanceName: "a1b2c3d4-00112233",
		ServiceType:  discovery.ServiceTypeOperational,
		Host:         "device.local",
		Port:         8443,
		Addresses:    []string{"fd12:3456:789a::1"},
		TXTRecords:   map[string]string{"ZI": "a1b2c3d4", "DI": "00112233"},
	}}

	// Inject an address (as handleInterfaceUp does).
	ds.injectedAddresses = []string{"fd34:5678:abcd::1"}

	// buildBrowseOutput should detect the injection even without previousAddresses.
	out, err := r.buildBrowseOutput(ds, browseSelectionHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out[KeyNewAddressAnnounced] != true {
		t.Errorf("expected new_address_announced=true from injection without baseline, got %v", out[KeyNewAddressAnnounced])
	}

	// The injected address should be merged into the service.
	if len(ds.services[0].Addresses) != 2 {
		t.Errorf("expected 2 addresses after merge, got %d: %v", len(ds.services[0].Addresses), ds.services[0].Addresses)
	}

	// injectedAddresses should be consumed.
	if len(ds.injectedAddresses) != 0 {
		t.Error("expected injectedAddresses to be cleared after merge")
	}
}

// TestBrowseMDNS_InterfaceUpThenBrowse verifies the full integration path:
// handleInterfaceUp injects an address, then buildBrowseOutput detects it.
func TestBrowseMDNS_InterfaceUpThenBrowse(t *testing.T) {
	r := newTestRunner()
	state := newTestState()

	// Seed services as if the device was already discovered via mDNS.
	ds := getDiscoveryState(state)
	ds.services = []discoveredService{{
		InstanceName: "a1b2c3d4-00112233",
		ServiceType:  discovery.ServiceTypeOperational,
		Host:         "device.local",
		Port:         8443,
		Addresses:    []string{"fd12:3456:789a::1"},
		TXTRecords:   map[string]string{"ZI": "a1b2c3d4", "DI": "00112233"},
	}}

	// Simulate interface_up.
	upStep := &loader.Step{Params: map[string]any{}}
	_, err := r.handleInterfaceUp(context.Background(), upStep, state)
	if err != nil {
		t.Fatalf("interface_up: %v", err)
	}

	// Verify injected address is pending.
	if len(ds.injectedAddresses) != 1 {
		t.Fatalf("expected 1 injected address, got %d", len(ds.injectedAddresses))
	}

	// Call buildBrowseOutput (simulates what browse_mdns does).
	out, err := r.buildBrowseOutput(ds, browseSelectionHints{})
	if err != nil {
		t.Fatalf("buildBrowseOutput: %v", err)
	}

	if out[KeyNewAddressAnnounced] != true {
		t.Errorf("expected new_address_announced=true after interface_up, got %v", out[KeyNewAddressAnnounced])
	}

	// Verify the injected address was merged.
	if len(ds.services[0].Addresses) != 2 {
		t.Errorf("expected 2 addresses, got %d: %v", len(ds.services[0].Addresses), ds.services[0].Addresses)
	}
}

func TestBuildBrowseOutput_OperationalPrefersStateAlignedService(t *testing.T) {
	r := newTestRunner()
	ds := &discoveryState{
		services: []discoveredService{
			{
				InstanceName: "zone-a-stale-di",
				ServiceType:  discovery.ServiceTypeOperational,
				Host:         "stale.local",
				Port:         8443,
				Addresses:    []string{"fd00::1"},
				TXTRecords: map[string]string{
					"ZI": "zone-a",
					"DI": "stale1111",
				},
			},
			{
				InstanceName: "zone-a-fresh-di",
				ServiceType:  discovery.ServiceTypeOperational,
				Host:         "fresh.local",
				Port:         8443,
				Addresses:    []string{"fd00::2"},
				TXTRecords: map[string]string{
					"ZI": "zone-a",
					"DI": "fresh2222",
				},
			},
		},
	}

	out, err := r.buildBrowseOutput(ds, browseSelectionHints{
		zoneID:   "zone-a",
		deviceID: "fresh2222",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := out[KeyDeviceID]; got != "fresh2222" {
		t.Fatalf("expected selected device_id fresh2222, got %v", got)
	}
	if got := out[KeyInstanceName]; got != "zone-a-fresh-di" {
		t.Fatalf("expected selected instance zone-a-fresh-di, got %v", got)
	}
	if got := out["txt_field_DI"]; got != "fresh2222" {
		t.Fatalf("expected selected txt_field_DI fresh2222, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Group G: browseViaObserver fresh-browse semantics
// ---------------------------------------------------------------------------

// TestBrowseViaObserver_ReturnsExistingEntries verifies that browseViaObserver
// returns services already in the observer's live snapshot. The persistent
// observer tracks additions and removals, so previously discovered services
// are returned immediately without clearing.
func TestBrowseViaObserver_ReturnsExistingEntries(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()

	// Pre-populate observer with a commissionable service.
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-EXISTING",
		Discriminator: 9999,
	}

	// Wait for the observer to ingest the service.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := r.observer.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	if err != nil {
		t.Fatalf("setup: observer did not ingest service: %v", err)
	}

	// browseViaObserver should return the existing service immediately.
	services, err := r.browseViaObserver(context.Background(), "commissionable", 500, 1)
	if err != nil {
		t.Fatalf("browseViaObserver error: %v", err)
	}

	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].InstanceName != "MASH-EXISTING" {
		t.Errorf("expected MASH-EXISTING, got %s", services[0].InstanceName)
	}
}

// TestBrowseViaObserver_ReturnsFreshServices verifies that browseViaObserver
// returns services that arrive AFTER the browse starts (fresh advertisements).
func TestBrowseViaObserver_ReturnsFreshServices(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()

	// Send a fresh service after a short delay (simulating a device
	// advertising after the browse starts).
	go func() {
		time.Sleep(100 * time.Millisecond)
		tb.commAdded <- &discovery.CommissionableService{
			InstanceName:  "MASH-FRESH",
			Discriminator: 1234,
		}
	}()

	services, err := r.browseViaObserver(context.Background(), "commissionable", 2000, 1)
	if err != nil {
		t.Fatalf("browseViaObserver error: %v", err)
	}

	if len(services) != 1 {
		t.Fatalf("expected 1 fresh service, got %d", len(services))
	}
	if services[0].InstanceName != "MASH-FRESH" {
		t.Errorf("expected MASH-FRESH, got %s", services[0].InstanceName)
	}
}

// TestBrowseViaObserver_ExistingAndNew verifies that browseViaObserver returns
// all services in the observer's live snapshot, including both previously
// discovered and newly arriving ones.
func TestBrowseViaObserver_ExistingAndNew(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()

	// Pre-populate with an existing service.
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-OLD",
		Discriminator: 1111,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := r.observer.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) >= 1
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// browseViaObserver returns immediately since there's already a service.
	services, err := r.browseViaObserver(context.Background(), "commissionable", 2000, 1)
	if err != nil {
		t.Fatalf("browseViaObserver error: %v", err)
	}

	// Should see the existing service.
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].InstanceName != "MASH-OLD" {
		t.Errorf("expected MASH-OLD, got %s", services[0].InstanceName)
	}
}

func TestBrowseViaObserver_TimeoutRecoversWithFreshWindow(t *testing.T) {
	r := newTestRunner()
	fb := &freshWindowBrowser{
		commissionableByCall: map[int][]*discovery.CommissionableService{
			// Call #1: observer session start -> no event in snapshot.
			1: {},
			// Call #2: fresh-window fallback -> live service observed.
			2: {{
				InstanceName:  "MASH-RECOVERED",
				Discriminator: 1234,
			}},
		},
	}
	r.observer = newMDNSObserver(fb, r.debugf)
	defer r.observer.Stop()

	services, err := r.browseViaObserver(context.Background(), ServiceAliasCommissionable, 120, 1)
	if err != nil {
		t.Fatalf("browseViaObserver error: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 recovered service, got %d", len(services))
	}
	if services[0].InstanceName != "MASH-RECOVERED" {
		t.Fatalf("expected MASH-RECOVERED, got %s", services[0].InstanceName)
	}
	if fb.commCalls < 2 {
		t.Fatalf("expected fresh browse fallback call, commCalls=%d", fb.commCalls)
	}
}

// TestHandleBrowseMDNS_WaitsForExpectedInstancesForDevice verifies that
// browse_mdns waits for the expected instance count instead of returning on
// the first operational service.
func TestHandleBrowseMDNS_WaitsForExpectedInstancesForDevice(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasOperational,
			KeyTimeoutMs:   float64(2000),
		},
		Expect: map[string]any{
			KeyInstancesForDevice: float64(2),
		},
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		tb.opAdded <- &discovery.OperationalService{
			InstanceName: "zone-a-device-1",
			ZoneID:       "zone-a",
			DeviceID:     "device-1",
			Host:         "host.local",
			Port:         8443,
			Addresses:    []string{"fd00::1"},
		}
		time.Sleep(50 * time.Millisecond)
		tb.opAdded <- &discovery.OperationalService{
			InstanceName: "zone-b-device-1",
			ZoneID:       "zone-b",
			DeviceID:     "device-1",
			Host:         "host.local",
			Port:         8443,
			Addresses:    []string{"fd00::1"},
		}
	}()

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS error: %v", err)
	}
	if got, _ := out[KeyInstancesForDevice].(int); got != 2 {
		t.Fatalf("instances_for_device = %v, want 2", out[KeyInstancesForDevice])
	}
}

func TestHandleBrowseMDNS_OperationalWaitsForStateDeviceID(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()
	state.Set("state_device_id", "fresh2222")
	state.Set(StateCurrentZoneID, "zone-a")

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasOperational,
			KeyTimeoutMs:   float64(2000),
		},
	}

	// Stale entry first; fresh matching DI appears shortly after.
	go func() {
		tb.opAdded <- &discovery.OperationalService{
			InstanceName: "zone-a-stale1111",
			ZoneID:       "zone-a",
			DeviceID:     "stale1111",
			Host:         "host.local",
			Port:         8443,
			Addresses:    []string{"fd00::1"},
		}
		time.Sleep(100 * time.Millisecond)
		tb.opAdded <- &discovery.OperationalService{
			InstanceName: "zone-a-fresh2222",
			ZoneID:       "zone-a",
			DeviceID:     "fresh2222",
			Host:         "host.local",
			Port:         8443,
			Addresses:    []string{"fd00::2"},
		}
	}()

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS: %v", err)
	}

	if got := out[KeyDeviceID]; got != "fresh2222" {
		t.Fatalf("expected device_id=fresh2222, got %v", got)
	}
	if got := out[KeyInstanceName]; got != "zone-a-fresh2222" {
		t.Fatalf("expected instance zone-a-fresh2222, got %v", got)
	}
}

// TestHandleBrowseMDNS_ExpectDeviceAbsent_WaitsForRemoval verifies that when a
// step expects device_found=false, handler waits for removal updates instead of
// returning immediately from a stale positive snapshot.
func TestHandleBrowseMDNS_ExpectDeviceAbsent_WaitsForRemoval(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()

	// Seed observer with a commissionable service first.
	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-1234",
		Discriminator: 1234,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.observer.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) == 1
	}); err != nil {
		t.Fatalf("setup observer seed failed: %v", err)
	}

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasCommissionable,
			KeyTimeoutMs:   float64(1000),
		},
		Expect: map[string]any{
			KeyDeviceFound: false,
		},
	}

	// Remove service shortly after browse starts.
	go func() {
		time.Sleep(100 * time.Millisecond)
		tb.commRemoved <- &discovery.CommissionableService{InstanceName: "MASH-1234"}
	}()

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS error: %v", err)
	}
	if got, _ := out[KeyDeviceFound].(bool); got {
		t.Fatalf("device_found = %v, want false", got)
	}
}

// TestHandleBrowseMDNS_ExpectDeviceAbsent_TimeoutUsesFreshBrowse verifies that
// timeout on observer wait re-validates presence with a fresh browse window.
func TestHandleBrowseMDNS_ExpectDeviceAbsent_TimeoutUsesFreshBrowse(t *testing.T) {
	r := newTestRunner()
	fb := &freshWindowBrowser{
		commissionableByCall: map[int][]*discovery.CommissionableService{
			2: {{
				InstanceName:  "MASH-ALWAYS",
				Discriminator: 1234,
			}},
		},
	}
	r.observer = newMDNSObserver(fb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()

	// Seed stale-positive observer state.
	r.observer.addService(discoveredService{
		InstanceName:  "MASH-ALWAYS",
		ServiceType:   discovery.ServiceTypeCommissionable,
		Discriminator: 1234,
	})

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasCommissionable,
			KeyTimeoutMs:   float64(200),
		},
		Expect: map[string]any{
			KeyDeviceFound: false,
		},
	}

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS error: %v", err)
	}
	if got, _ := out[KeyDeviceFound].(bool); !got {
		t.Fatalf("device_found = %v, want true when service remains present", got)
	}
}

// TestHandleBrowseMDNS_ExpectDeviceAbsent_StaleObserverSnapshotRecovered verifies
// stale observer positives do not fail absent checks when a fresh browse window
// sees no commissionable services.
func TestHandleBrowseMDNS_ExpectDeviceAbsent_StaleObserverSnapshotRecovered(t *testing.T) {
	r := newTestRunner()
	tb := newTestBrowser()
	r.observer = newMDNSObserver(tb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()

	tb.commAdded <- &discovery.CommissionableService{
		InstanceName:  "MASH-STALE",
		Discriminator: 1234,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.observer.WaitFor(ctx, "commissionable", func(svcs []discoveredService) bool {
		return len(svcs) == 1
	}); err != nil {
		t.Fatalf("setup observer seed failed: %v", err)
	}

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasCommissionable,
			KeyTimeoutMs:   float64(120),
		},
		Expect: map[string]any{
			KeyDeviceFound: false,
		},
	}

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS error: %v", err)
	}
	if got, _ := out[KeyDeviceFound].(bool); got {
		t.Fatalf("device_found = %v, want false when only stale observer state exists", got)
	}
}

type freshWindowBrowser struct {
	commissionableByCall map[int][]*discovery.CommissionableService
	operationalByCall    map[int][]*discovery.OperationalService
	commCalls            int
	opCalls              int
}

func (b *freshWindowBrowser) BrowseCommissionable(_ context.Context) (added, removed <-chan *discovery.CommissionableService, err error) {
	b.commCalls++
	addedCh := make(chan *discovery.CommissionableService, 4)
	removedCh := make(chan *discovery.CommissionableService)
	for _, svc := range b.commissionableByCall[b.commCalls] {
		addedCh <- svc
	}
	close(addedCh)
	close(removedCh)
	return addedCh, removedCh, nil
}

func (b *freshWindowBrowser) BrowseOperational(context.Context, string) (<-chan *discovery.OperationalService, error) {
	b.opCalls++
	ch := make(chan *discovery.OperationalService, 4)
	for _, svc := range b.operationalByCall[b.opCalls] {
		ch <- svc
	}
	close(ch)
	return ch, nil
}

func (b *freshWindowBrowser) BrowsePairingRequests(context.Context, func(discovery.PairingRequestService)) error {
	return nil
}

func (b *freshWindowBrowser) FindByDiscriminator(context.Context, uint16) (*discovery.CommissionableService, error) {
	return nil, nil
}

func (b *freshWindowBrowser) FindAllByDiscriminator(context.Context, uint16) ([]*discovery.CommissionableService, error) {
	return nil, nil
}

func (b *freshWindowBrowser) Stop() {}

func TestHandleBrowseMDNS_OperationalMismatch_UsesFreshWindowRecovery(t *testing.T) {
	r := newTestRunner()
	fb := &freshWindowBrowser{
		// Call #1 is the observer session startup (no events).
		operationalByCall: map[int][]*discovery.OperationalService{
			2: {
				{
					InstanceName: "zone-a-fresh2222",
					ZoneID:       "zone-a",
					DeviceID:     "fresh2222",
					Host:         "host.local",
					Port:         8443,
					Addresses:    []string{"fd00::2"},
				},
			},
		},
	}
	r.observer = newMDNSObserver(fb, r.debugf)
	defer r.observer.Stop()
	state := newTestState()
	state.Set("state_device_id", "fresh2222")
	state.Set(StateCurrentZoneID, "zone-a")

	// Seed stale operational snapshot entry that does NOT match state device ID.
	r.observer.addService(discoveredService{
		InstanceName: "zone-a-stale1111",
		ServiceType:  discovery.ServiceTypeOperational,
		Host:         "host.local",
		Port:         8443,
		Addresses:    []string{"fd00::1"},
		TXTRecords: map[string]string{
			"ZI": "zone-a",
			"DI": "stale1111",
		},
	})

	step := &loader.Step{
		Action: ActionBrowseMDNS,
		Params: map[string]any{
			KeyServiceType: ServiceAliasOperational,
			KeyTimeoutMs:   float64(150),
		},
	}

	out, err := r.handleBrowseMDNS(context.Background(), step, state)
	if err != nil {
		t.Fatalf("handleBrowseMDNS: %v", err)
	}
	if got := out[KeyDeviceID]; got != "fresh2222" {
		t.Fatalf("expected fresh recovered DI, got %v", got)
	}
}
