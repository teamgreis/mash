// Command browse_mdns scans for a single ``_mash-comm._tcp`` or ``_mash._tcp``
// record using mash-go's discovery package and prints the result as a single
// JSON line on stdout. Used by mash-py's Phase 18 interop test to verify that
// a Python device's mDNS advertiser is discoverable by the Go reference
// browser (DEC-088).
//
// Scope is intentionally narrow: one match, then exit. No commissioning, no
// connection, no continuous browse. The Python test brings up its advertiser,
// invokes this tool with the discriminator (or zone-id) to look for, and
// asserts on the JSON.
//
// Run:
//
//	go run ./tools/browse_mdns -kind commissionable -discriminator 1234 -timeout 5s
//	go run ./tools/browse_mdns -kind operational -zone-id A1B2C3D4E5F6A7B8 -timeout 5s
//
// Output on success (stable JSON, single line, then exit 0):
//
//	{"kind":"commissionable","instance":"MASH-1234","host":"...","port":18443,"addresses":[...],"txt":{"D":"1234","cat":"3","serial":"S","brand":"B","model":"M"}}
//
// On timeout: exits non-zero with a stderr message.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

type result struct {
	Kind      string            `json:"kind"`
	Instance  string            `json:"instance"`
	Host      string            `json:"host"`
	Port      uint16            `json:"port"`
	Addresses []string          `json:"addresses"`
	TXT       map[string]string `json:"txt"`
}

func main() {
	kind := flag.String("kind", "commissionable", "Service to scan: commissionable | operational")
	discriminator := flag.Int("discriminator", -1, "Filter commissionable records by discriminator (0..4095)")
	zoneID := flag.String("zone-id", "", "Filter operational records by ZI (16 hex chars)")
	deviceID := flag.String("device-id", "", "Filter operational records by DI (16 hex chars)")
	timeout := flag.Duration("timeout", 10*time.Second, "Browse timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	browser, err := discovery.NewMDNSBrowser(discovery.BrowserConfig{})
	if err != nil {
		die("new browser: %v", err)
	}

	switch strings.ToLower(*kind) {
	case "commissionable":
		if *discriminator < 0 || *discriminator > discovery.MaxDiscriminator {
			die("commissionable browse requires -discriminator 0..%d", discovery.MaxDiscriminator)
		}
		svc, err := browser.FindByDiscriminator(ctx, uint16(*discriminator))
		if err != nil {
			die("browse: %v", err)
		}
		emitCommissionable(svc)
	case "operational":
		// BrowseOperational filters by ZI when *zoneID != "" but does not
		// filter by DI; the Python interop test may set either, so we loop
		// over the channel and apply the DI filter here.
		ch, err := browser.BrowseOperational(ctx, *zoneID)
		if err != nil {
			die("browse-operational: %v", err)
		}
		for {
			select {
			case svc, ok := <-ch:
				if !ok {
					die("browse-operational: channel closed without match")
				}
				if *deviceID != "" && svc.DeviceID != *deviceID {
					continue
				}
				emitOperational(svc)
				return
			case <-ctx.Done():
				die("browse-operational: timeout (deadline %s)", *timeout)
			}
		}
	default:
		die("unknown -kind %q (want commissionable | operational)", *kind)
	}
}

func emitCommissionable(svc *discovery.CommissionableService) {
	cats := make([]string, len(svc.Categories))
	for i, c := range svc.Categories {
		cats[i] = fmt.Sprintf("%d", uint8(c))
	}
	txt := map[string]string{
		"D":      fmt.Sprintf("%d", svc.Discriminator),
		"cat":    strings.Join(cats, ","),
		"serial": svc.Serial,
		"brand":  svc.Brand,
		"model":  svc.Model,
	}
	if svc.DeviceName != "" {
		txt["DN"] = svc.DeviceName
	}
	emit(result{
		Kind:      "commissionable",
		Instance:  svc.InstanceName,
		Host:      svc.Host,
		Port:      svc.Port,
		Addresses: svc.Addresses,
		TXT:       txt,
	})
}

func emitOperational(svc *discovery.OperationalService) {
	txt := map[string]string{
		"ZI": svc.ZoneID,
		"DI": svc.DeviceID,
	}
	if svc.VendorProduct != "" {
		txt["VP"] = svc.VendorProduct
	}
	if svc.Firmware != "" {
		txt["FW"] = svc.Firmware
	}
	if svc.EndpointCount > 0 {
		txt["EP"] = fmt.Sprintf("%d", svc.EndpointCount)
	}
	emit(result{
		Kind:      "operational",
		Instance:  svc.InstanceName,
		Host:      svc.Host,
		Port:      svc.Port,
		Addresses: svc.Addresses,
		TXT:       txt,
	})
}

func emit(r result) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		die("encode: %v", err)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "browse_mdns: "+format+"\n", args...)
	os.Exit(1)
}
