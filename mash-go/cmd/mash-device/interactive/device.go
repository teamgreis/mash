// Package interactive provides the interactive command-line interface
// for the MASH device.
package interactive

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/internal/inspect"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/service"
)

// DeviceType represents supported device types.
type DeviceType string

const (
	DeviceTypeEVSE     DeviceType = "evse"
	DeviceTypeInverter DeviceType = "inverter"
	DeviceTypeBattery  DeviceType = "battery"
	DeviceTypeHeatPump DeviceType = "heatpump"
)

// DeviceConfig provides configuration information to the interactive device.
// This interface allows the interactive layer to access device settings
// without depending on the main package's config structure.
type DeviceConfig interface {
	// DeviceType returns the type of device (evse, inverter, battery).
	DeviceType() DeviceType
}

// Device handles interactive mode for mash-device.
type Device struct {
	svc       *service.DeviceService
	config    DeviceConfig
	inspector *inspect.Inspector
	formatter *inspect.Formatter
	rl        *readline.Instance

	// Simulation control
	simCtx     context.Context
	simCancel  context.CancelFunc
	simRunning bool

	// State tracking for transitions display
	lastControlState features.ControlState
}

// New creates a new interactive device handler.
func New(svc *service.DeviceService, cfg DeviceConfig) (*Device, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "device> ",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create readline: %w", err)
	}

	d := &Device{
		svc:       svc,
		config:    cfg,
		inspector: inspect.NewInspector(svc.Device()),
		formatter: inspect.NewFormatter(),
		rl:        rl,
	}

	// Register event handler for displaying write events
	svc.OnEvent(d.handleEvent)

	return d, nil
}

// Stdout returns a writer that properly coordinates with the readline input.
// Use this for log output to avoid interfering with the command prompt.
func (d *Device) Stdout() io.Writer {
	return d.rl.Stdout()
}

// Stderr returns a writer that properly coordinates with the readline input.
func (d *Device) Stderr() io.Writer {
	return d.rl.Stderr()
}

// Run starts the interactive command loop.
func (d *Device) Run(ctx context.Context, cancel context.CancelFunc) {
	defer d.rl.Close()

	d.printHelp()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := d.rl.Readline()
		if err != nil {
			// EOF or interrupt
			if err == readline.ErrInterrupt {
				continue
			}
			fmt.Fprintln(d.rl.Stdout(), "Exiting...")
			cancel()
			return
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		parts := strings.Fields(input)
		cmd := strings.ToLower(parts[0])
		args := parts[1:]

		switch cmd {
		case "help", "?":
			d.printHelp()

		case "inspect", "i":
			d.cmdInspect(args)

		case "read", "r":
			d.cmdRead(args)

		case "write", "w":
			d.cmdWrite(args)

		case "zones", "z":
			d.cmdZones(args)

		case "cert":
			d.cmdCert(args)

		case "kick":
			d.cmdKick(args)

		case "commission", "comm":
			d.cmdCommission()

		case "start", "sim-start":
			d.cmdStart()

		case "stop", "sim-stop":
			d.cmdStop()

		case "power":
			d.cmdPower(args)

		case "status":
			d.cmdStatus()

		case "override":
			d.cmdOverride(args)

		case "contractual":
			d.cmdContractual(args)

		case "limit-status", "ls":
			d.cmdLimitStatus()

		case "quit", "exit", "q":
			fmt.Fprintln(d.rl.Stdout(), "Exiting...")
			cancel()
			return

		default:
			fmt.Fprintf(d.rl.Stdout(), "Unknown command: %s (type 'help' for commands)\n", cmd)
		}
	}
}

func (d *Device) printHelp() {
	fmt.Fprintln(d.rl.Stdout(),`
MASH Device Commands:
  Inspection:
    inspect [path]     - Inspect device structure (or specific endpoint/feature)
    read <path>        - Read an attribute value
    write <path> <val> - Write an attribute value

  Zone Management:
    zones              - List paired zones
    cert [zone-id]     - Show certificates (or --all for summary)
    kick <zone-id>     - Remove a zone from this device
    commission         - Enter commissioning mode (open for pairing)

  Simulation:
    start              - Start simulation
    stop               - Stop simulation
    power <kw>         - Set power value (kW, positive=consume, negative=produce)
    status             - Show device status

  GPL Simulation:
    override <reason>|clear  - Enter/exit OVERRIDE state
    contractual <kw> [kw]    - Set contractual limits (EMS mode)
    limit-status             - Show current limit state

  General:
    help               - Show this help
    quit               - Exit device

  Path Format:
    endpoint/feature/attribute - e.g., 1/measurement/acActivePower
    Can use IDs or names: 1/2/1 or evCharger/measurement/acActivePower`)
}

// cmdInspect handles the inspect command.
func (d *Device) cmdInspect(args []string) {
	if len(args) == 0 {
		// Show full device tree
		tree := d.inspector.InspectDevice()
		fmt.Fprint(d.rl.Stdout(),d.inspector.FormatDeviceTree(tree, d.formatter))
		return
	}

	// Parse path
	path, err := inspect.ParsePath(args[0])
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Invalid path: %v\n", err)
		return
	}

	if path.IsPartial {
		if path.FeatureID == 0 {
			// Endpoint only
			epInfo, err := d.inspector.InspectEndpoint(path.EndpointID)
			if err != nil {
				fmt.Fprintf(d.rl.Stdout(),"Error: %v\n", err)
				return
			}
			fmt.Fprint(d.rl.Stdout(),d.inspector.FormatEndpoint(epInfo, d.formatter))
		} else {
			// Endpoint and feature
			featInfo, err := d.inspector.InspectFeature(path.EndpointID, path.FeatureID)
			if err != nil {
				fmt.Fprintf(d.rl.Stdout(),"Error: %v\n", err)
				return
			}
			fmt.Fprint(d.rl.Stdout(),d.inspector.FormatFeature(featInfo, d.formatter))
		}
	} else {
		// Full path - show single attribute
		value, meta, err := d.inspector.ReadAttribute(path)
		if err != nil {
			fmt.Fprintf(d.rl.Stdout(),"Error: %v\n", err)
			return
		}
		valueStr := d.formatter.FormatValue(value, meta.Unit)
		fmt.Fprintf(d.rl.Stdout(),"%s = %s\n", meta.Name, valueStr)
	}
}

// cmdRead handles the read command.
func (d *Device) cmdRead(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(d.rl.Stdout(),"Usage: read <path>")
		fmt.Fprintln(d.rl.Stdout(),"  Example: read 1/measurement/acActivePower")
		return
	}

	path, err := inspect.ParsePath(args[0])
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Invalid path: %v\n", err)
		return
	}

	if path.IsPartial {
		// Read all attributes for the feature
		attrs, err := d.inspector.ReadAllAttributes(path.EndpointID, path.FeatureID)
		if err != nil {
			fmt.Fprintf(d.rl.Stdout(),"Error: %v\n", err)
			return
		}
		for attrID, value := range attrs {
			name := inspect.GetAttributeName(path.FeatureID, attrID)
			if name == "" {
				name = fmt.Sprintf("attr_%d", attrID)
			}
			fmt.Fprintf(d.rl.Stdout(),"  %s: %v\n", name, value)
		}
	} else {
		// Read single attribute
		value, meta, err := d.inspector.ReadAttribute(path)
		if err != nil {
			fmt.Fprintf(d.rl.Stdout(),"Error: %v\n", err)
			return
		}
		valueStr := d.formatter.FormatValue(value, meta.Unit)
		fmt.Fprintf(d.rl.Stdout(),"%s = %s\n", meta.Name, valueStr)
	}
}

// cmdWrite handles the write command.
func (d *Device) cmdWrite(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(d.rl.Stdout(),"Usage: write <path> <value>")
		fmt.Fprintln(d.rl.Stdout(),"  Example: write 0/deviceInfo/label \"My EVSE\"")
		return
	}

	path, err := inspect.ParsePath(args[0])
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Invalid path: %v\n", err)
		return
	}

	// Parse the value (try int, then float, then string)
	valueStr := strings.Join(args[1:], " ")
	var value any

	// Try int64 first
	if v, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
		value = v
	} else if v, err := strconv.ParseFloat(valueStr, 64); err == nil {
		value = v
	} else if v, err := strconv.ParseBool(valueStr); err == nil {
		value = v
	} else {
		// Treat as string (strip quotes if present)
		value = strings.Trim(valueStr, "\"'")
	}

	if err := d.inspector.WriteAttribute(path, value); err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Write failed: %v\n", err)
		return
	}

	fmt.Fprintln(d.rl.Stdout(),"OK")
}

// cmdZones handles the zones command.
func (d *Device) cmdZones(_ []string) {
	zones := d.svc.GetAllZones()
	if len(zones) == 0 {
		fmt.Fprintln(d.rl.Stdout(),"No paired zones")
		return
	}

	fmt.Fprintf(d.rl.Stdout(),"\nPaired Zones (%d):\n", len(zones))
	fmt.Fprintln(d.rl.Stdout(),"-------------------------------------------")
	for _, z := range zones {
		status := "connected"
		if !z.Connected {
			status = "disconnected"
		}
		if z.FailsafeActive {
			status = "FAILSAFE"
		}
		fmt.Fprintf(d.rl.Stdout(),"  ID: %s\n", z.ID)
		fmt.Fprintf(d.rl.Stdout(),"      Type: %s (priority %d)\n", z.Type.String(), z.Priority)
		fmt.Fprintf(d.rl.Stdout(),"      Status: %s\n", status)
		fmt.Fprintf(d.rl.Stdout(),"      Last seen: %s\n", z.LastSeen.Format("15:04:05"))
		fmt.Fprintln(d.rl.Stdout(),)
	}
}

// cmdCert handles the cert command.
// Usage:
//   - cert             - Show certificates for all zones
//   - cert <zone-id>   - Show certificate details for a specific zone
//   - cert --all       - Show summary table of all certificates
func (d *Device) cmdCert(args []string) {
	certStore := d.svc.GetCertStore()
	if certStore == nil {
		fmt.Fprintln(d.rl.Stdout(), "No certificate store configured")
		return
	}

	// Check for --all flag
	if len(args) > 0 && args[0] == "--all" {
		d.showAllCerts(certStore)
		return
	}

	// If zone ID provided, show that zone's certs
	if len(args) > 0 {
		d.showZoneCert(certStore, args[0])
		return
	}

	// Default: show all zones' certificates
	d.showAllCerts(certStore)
}

// showAllCerts displays a summary table of all certificates.
func (d *Device) showAllCerts(certStore cert.Store) {
	zones := certStore.ListZones()
	if len(zones) == 0 {
		fmt.Fprintln(d.rl.Stdout(), "No operational certificates (device not commissioned)")
		return
	}

	fmt.Fprintln(d.rl.Stdout(), "\nCertificate Summary:")
	fmt.Fprintln(d.rl.Stdout(), "--------------------------------------------------------------------------------")
	fmt.Fprintf(d.rl.Stdout(), "%-12s %-24s %-20s %-10s %s\n",
		"Type", "Subject", "Issuer", "Expiry", "Status")
	fmt.Fprintln(d.rl.Stdout(), "--------------------------------------------------------------------------------")

	for _, zoneID := range zones {
		// Operational cert
		opCert, err := certStore.GetOperationalCert(zoneID)
		if err == nil && opCert != nil {
			d.printCertRow("Operational", opCert.Certificate)
		}

		// Zone CA
		zoneCACert, err := certStore.GetZoneCACert(zoneID)
		if err == nil && zoneCACert != nil {
			d.printCertRow("Zone CA", zoneCACert)
		}
	}

	fmt.Fprintln(d.rl.Stdout(), "--------------------------------------------------------------------------------")
	fmt.Fprintln(d.rl.Stdout())
}

// showZoneCert displays detailed certificate information for a specific zone.
func (d *Device) showZoneCert(certStore cert.Store, partialZoneID string) {
	// Find matching zone
	var zoneID string
	for _, z := range certStore.ListZones() {
		if z == partialZoneID || strings.Contains(z, partialZoneID) {
			zoneID = z
			break
		}
	}

	if zoneID == "" {
		fmt.Fprintf(d.rl.Stdout(), "Zone not found: %s\n", partialZoneID)
		fmt.Fprintln(d.rl.Stdout(), "Use 'zones' to list zone IDs")
		return
	}

	fmt.Fprintf(d.rl.Stdout(), "\nCertificates for Zone: %s\n", zoneID)

	// Operational cert
	fmt.Fprintln(d.rl.Stdout(), "\nOperational Certificate:")
	fmt.Fprintln(d.rl.Stdout(), "-------------------------------------------")
	opCert, err := certStore.GetOperationalCert(zoneID)
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(), "  Error: %v\n", err)
	} else {
		d.printCertInfo(opCert.Certificate, "  ")
	}

	// Zone CA
	fmt.Fprintln(d.rl.Stdout(), "\nZone CA Certificate:")
	fmt.Fprintln(d.rl.Stdout(), "-------------------------------------------")
	zoneCACert, err := certStore.GetZoneCACert(zoneID)
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(), "  Error: %v\n", err)
	} else {
		d.printCertInfo(zoneCACert, "  ")
	}

	fmt.Fprintln(d.rl.Stdout())
}

// printCertInfo prints detailed certificate information.
func (d *Device) printCertInfo(c *x509.Certificate, prefix string) {
	if c == nil {
		fmt.Fprintf(d.rl.Stdout(), "%sNo certificate\n", prefix)
		return
	}

	fmt.Fprintf(d.rl.Stdout(), "%sSubject:     %s\n", prefix, c.Subject.CommonName)
	if len(c.Subject.Organization) > 0 {
		fmt.Fprintf(d.rl.Stdout(), "%sOrganization: %s\n", prefix, strings.Join(c.Subject.Organization, ", "))
	}
	fmt.Fprintf(d.rl.Stdout(), "%sIssuer:      %s\n", prefix, c.Issuer.CommonName)
	fmt.Fprintf(d.rl.Stdout(), "%sValid From:  %s\n", prefix, c.NotBefore.Format("2006-01-02"))
	fmt.Fprintf(d.rl.Stdout(), "%sValid Until: %s\n", prefix, c.NotAfter.Format("2006-01-02"))

	daysUntil := int(time.Until(c.NotAfter).Hours() / 24)
	status := d.certStatus(daysUntil)
	fmt.Fprintf(d.rl.Stdout(), "%sExpires In:  %d days [%s]\n", prefix, daysUntil, status)

	if c.IsCA {
		fmt.Fprintf(d.rl.Stdout(), "%sIs CA:       true\n", prefix)
	}
}

// printCertRow prints a single row in the certificate summary table.
func (d *Device) printCertRow(certType string, c *x509.Certificate) {
	subject := c.Subject.CommonName
	if len(subject) > 24 {
		subject = subject[:21] + "..."
	}

	issuer := c.Issuer.CommonName
	if len(issuer) > 20 {
		issuer = issuer[:17] + "..."
	}

	daysUntil := int(time.Until(c.NotAfter).Hours() / 24)
	expiry := fmt.Sprintf("%d days", daysUntil)
	status := d.certStatus(daysUntil)

	fmt.Fprintf(d.rl.Stdout(), "%-12s %-24s %-20s %-10s %s\n",
		certType, subject, issuer, expiry, status)
}

// certStatus returns a status string based on days until expiry.
func (d *Device) certStatus(daysUntil int) string {
	if daysUntil <= 0 {
		return "EXPIRED"
	}
	if daysUntil <= 7 {
		return "CRITICAL"
	}
	if daysUntil <= 30 {
		return "RENEW"
	}
	return "OK"
}

// cmdKick handles the kick command (removes a zone).
func (d *Device) cmdKick(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(d.rl.Stdout(),"Usage: kick <zone-id>")
		fmt.Fprintln(d.rl.Stdout(),"  Use 'zones' to list zone IDs")
		return
	}

	zoneID := args[0]

	// Try exact match first
	zone := d.svc.GetZone(zoneID)
	if zone == nil {
		// Try partial match
		for _, z := range d.svc.GetAllZones() {
			if strings.Contains(z.ID, zoneID) {
				zoneID = z.ID
				zone = z
				break
			}
		}
	}

	if zone == nil {
		fmt.Fprintf(d.rl.Stdout(),"Zone not found: %s\n", args[0])
		return
	}

	fmt.Fprintf(d.rl.Stdout(),"Removing zone %s...\n", zoneID)
	if err := d.svc.RemoveZone(zoneID); err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Failed to remove zone: %v\n", err)
		return
	}

	fmt.Fprintln(d.rl.Stdout(),"Zone removed")
}

// cmdCommission enters commissioning mode.
func (d *Device) cmdCommission() {
	if err := d.svc.EnterCommissioningMode(); err != nil {
		fmt.Fprintf(d.rl.Stdout(), "Failed to enter commissioning mode: %v\n", err)
		return
	}
	fmt.Fprintln(d.rl.Stdout(), "Commissioning mode enabled - device is now discoverable")
}

// cmdStart starts the simulation.
func (d *Device) cmdStart() {
	if d.simRunning {
		fmt.Fprintln(d.rl.Stdout(),"Simulation already running")
		return
	}
	d.startSimulation()
	fmt.Fprintln(d.rl.Stdout(),"Simulation started")
}

// cmdStop stops the simulation.
func (d *Device) cmdStop() {
	if !d.simRunning {
		fmt.Fprintln(d.rl.Stdout(),"Simulation not running")
		return
	}
	d.stopSimulation()
	fmt.Fprintln(d.rl.Stdout(),"Simulation stopped")
}

// cmdPower sets the power directly.
func (d *Device) cmdPower(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(d.rl.Stdout(),"Usage: power <kw>")
		fmt.Fprintln(d.rl.Stdout(),"  Positive values = consumption/charging")
		fmt.Fprintln(d.rl.Stdout(),"  Negative values = production/discharging")
		return
	}

	powerKW, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Invalid power value: %v\n", err)
		return
	}

	powerMW := int64(powerKW * 1_000_000)
	d.setPowerDirect(powerMW)
}

// cmdStatus shows the device status.
func (d *Device) cmdStatus() {
	fmt.Fprintln(d.rl.Stdout(),"\nDevice Status")
	fmt.Fprintln(d.rl.Stdout(),"-------------------------------------------")
	fmt.Fprintf(d.rl.Stdout(),"  Device ID:      %s\n", d.svc.Device().DeviceID())
	fmt.Fprintf(d.rl.Stdout(),"  Device Type:    %s\n", d.config.DeviceType())
	fmt.Fprintf(d.rl.Stdout(),"  Service State:  %s\n", d.svc.State())

	// Show paired zones with IDs
	zones := d.svc.GetAllZones()
	pairedIDs := make([]string, 0, len(zones))
	connectedIDs := make([]string, 0, len(zones))
	for _, z := range zones {
		shortID := z.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		pairedIDs = append(pairedIDs, shortID)
		if z.Connected {
			connectedIDs = append(connectedIDs, shortID)
		}
	}

	if len(pairedIDs) > 0 {
		fmt.Fprintf(d.rl.Stdout(),"  Paired Zones:   %d (%s)\n", len(pairedIDs), strings.Join(pairedIDs, ", "))
	} else {
		fmt.Fprintf(d.rl.Stdout(),"  Paired Zones:   0\n")
	}
	if len(connectedIDs) > 0 {
		fmt.Fprintf(d.rl.Stdout(),"  Connected:      %d (%s)\n", len(connectedIDs), strings.Join(connectedIDs, ", "))
	} else {
		fmt.Fprintf(d.rl.Stdout(),"  Connected:      0\n")
	}

	simStatus := "stopped"
	if d.simRunning {
		simStatus = "running"
	}
	fmt.Fprintf(d.rl.Stdout(),"  Simulation:     %s\n", simStatus)

	// Read current power if available
	path := &inspect.Path{
		EndpointID:  1,
		FeatureID:   uint8(model.FeatureMeasurement),
		AttributeID: 1, // acActivePower
	}
	if value, meta, err := d.inspector.ReadAttribute(path); err == nil {
		fmt.Fprintf(d.rl.Stdout(),"  Current Power:  %s\n", d.formatter.FormatValue(value, meta.Unit))
	}

	fmt.Fprintln(d.rl.Stdout(),)
}

// startSimulation starts the background simulation.
func (d *Device) startSimulation() {
	if d.simRunning {
		return
	}
	d.simCtx, d.simCancel = context.WithCancel(context.Background())
	d.simRunning = true
	go d.runSimulation(d.simCtx)
}

// stopSimulation stops the background simulation.
func (d *Device) stopSimulation() {
	if !d.simRunning {
		return
	}
	if d.simCancel != nil {
		d.simCancel()
	}
	d.simRunning = false
}

// setPowerDirect sets the power value directly.
func (d *Device) setPowerDirect(powerMW int64) {
	// Attribute IDs from features package
	const (
		attrACActivePower = uint16(1)  // features.MeasurementAttrACActivePower
		attrDCPower       = uint16(40) // features.MeasurementAttrDCPower
	)

	var attrID uint16
	switch d.config.DeviceType() {
	case DeviceTypeEVSE, DeviceTypeInverter, DeviceTypeHeatPump:
		attrID = attrACActivePower
	case DeviceTypeBattery:
		attrID = attrDCPower
	}

	if err := d.svc.NotifyAttributeChange(1, uint8(model.FeatureMeasurement), attrID, powerMW); err != nil {
		fmt.Fprintf(d.rl.Stdout(),"Failed to set power: %v\n", err)
		return
	}

	fmt.Fprintf(d.rl.Stdout(),"Power set to %.1f kW\n", float64(powerMW)/1_000_000)
}

// runSimulation runs the background simulation loop.
func (d *Device) runSimulation(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var power int64

	// Attribute IDs from features package
	const (
		attrACActivePower = uint16(1)  // features.MeasurementAttrACActivePower
		attrDCPower       = uint16(40) // features.MeasurementAttrDCPower
	)

	deviceType := d.config.DeviceType()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var attrID uint16
			switch deviceType {
			case DeviceTypeEVSE:
				// Simulate varying charging power
				power = (power + 1000000) % 22000000
				if power == 0 {
					power = 1380000
				}
				attrID = attrACActivePower

			case DeviceTypeInverter:
				// Simulate varying PV production based on time
				hour := time.Now().Hour()
				if hour >= 6 && hour <= 20 {
					// Daytime - produce power (negative = production)
					power = -int64((10 - abs(hour-13)) * 1000000)
				} else {
					power = 0
				}
				attrID = attrACActivePower

			case DeviceTypeBattery:
				// Simulate charge/discharge cycles
				power = (power + 500000) % 10000000 - 5000000
				attrID = attrDCPower

			case DeviceTypeHeatPump:
				// Simulate varying heating power
				power = (power + 500000) % 8000000
				if power == 0 {
					power = 1500000
				}
				attrID = attrACActivePower
			}

			// Update the attribute and notify subscribed zones
			if attrID != 0 {
				if err := d.svc.NotifyAttributeChange(1, uint8(model.FeatureMeasurement), attrID, power); err != nil {
					// Silently ignore errors in simulation
					_ = err
				}
			}
		}
	}
}

// IsRunning returns whether simulation is running (for external access).
func (d *Device) IsRunning() bool {
	return d.simRunning
}

// cmdOverride simulates entering/exiting OVERRIDE state.
func (d *Device) cmdOverride(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(d.rl.Stdout(), "Usage: override <reason>|clear")
		fmt.Fprintln(d.rl.Stdout(), "Reasons: self-protection, safety, legal, uncontrolled-load, uncontrolled-producer")
		return
	}

	ec := d.getEnergyControlFeature(1)
	if ec == nil {
		fmt.Fprintln(d.rl.Stdout(), "No EnergyControl feature found")
		return
	}

	if args[0] == "clear" {
		_ = ec.SetControlState(features.ControlStateControlled)
		_ = ec.SetOverrideReasonPtr(nil)
		_ = ec.SetOverrideDirectionPtr(nil)
		fmt.Fprintln(d.rl.Stdout(), "Override cleared, state: CONTROLLED")
		return
	}

	reason := parseOverrideReason(args[0])
	direction := features.DirectionConsumption
	if len(args) >= 2 && args[1] == "production" {
		direction = features.DirectionProduction
	}

	_ = ec.SetControlState(features.ControlStateOverride)
	_ = ec.SetOverrideReasonPtr(&reason)
	_ = ec.SetOverrideDirectionPtr(&direction)

	fmt.Fprintf(d.rl.Stdout(), "Override set: %s (%s)\n", reason, direction)
}

func parseOverrideReason(s string) features.OverrideReason {
	switch strings.ToLower(s) {
	case "self-protection", "protection":
		return features.OverrideReasonSelfProtection
	case "safety":
		return features.OverrideReasonSafety
	case "legal", "legal-requirement":
		return features.OverrideReasonLegalRequirement
	case "uncontrolled-load", "load":
		return features.OverrideReasonUncontrolledLoad
	case "uncontrolled-producer", "producer":
		return features.OverrideReasonUncontrolledProducer
	default:
		return features.OverrideReasonSelfProtection
	}
}

// cmdContractual sets contractual limits (EMS simulation).
func (d *Device) cmdContractual(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(d.rl.Stdout(), "Usage: contractual <consumption-kw> [production-kw]")
		fmt.Fprintln(d.rl.Stdout(), "       contractual clear")
		return
	}

	ec := d.getEnergyControlFeature(1)
	if ec == nil {
		fmt.Fprintln(d.rl.Stdout(), "No EnergyControl feature found")
		return
	}

	if args[0] == "clear" {
		_ = ec.SetContractualConsumptionMaxPtr(nil)
		_ = ec.SetContractualProductionMaxPtr(nil)
		fmt.Fprintln(d.rl.Stdout(), "Contractual limits cleared")
		return
	}

	consumptionKW, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		fmt.Fprintf(d.rl.Stdout(), "Invalid consumption: %v\n", err)
		return
	}
	consumptionMW := int64(consumptionKW * 1000000)
	_ = ec.SetContractualConsumptionMaxPtr(&consumptionMW)

	if len(args) >= 2 {
		productionKW, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			fmt.Fprintf(d.rl.Stdout(), "Invalid production: %v\n", err)
			return
		}
		productionMW := int64(productionKW * 1000000)
		_ = ec.SetContractualProductionMaxPtr(&productionMW)
		fmt.Fprintf(d.rl.Stdout(), "Contractual limits set: consumption=%.1f kW, production=%.1f kW\n",
			consumptionKW, productionKW)
	} else {
		fmt.Fprintf(d.rl.Stdout(), "Contractual consumption limit set: %.1f kW\n", consumptionKW)
	}
}

// cmdLimitStatus shows current limit state.
func (d *Device) cmdLimitStatus() {
	ec := d.getEnergyControlFeature(1)
	if ec == nil {
		fmt.Fprintln(d.rl.Stdout(), "No EnergyControl feature found")
		return
	}

	fmt.Fprintln(d.rl.Stdout(), "Limit Status:")
	fmt.Fprintf(d.rl.Stdout(), "  Control State: %s\n", ec.ControlState())

	// Consumption limit
	if limit, ok := ec.EffectiveConsumptionLimit(); ok {
		fmt.Fprintf(d.rl.Stdout(), "  Effective Consumption Limit: %.1f kW\n", float64(limit)/1000000)
	} else {
		fmt.Fprintln(d.rl.Stdout(), "  Effective Consumption Limit: none")
	}

	// Production limit
	if limit, ok := ec.EffectiveProductionLimit(); ok {
		fmt.Fprintf(d.rl.Stdout(), "  Effective Production Limit:  %.1f kW\n", float64(limit)/1000000)
	} else {
		fmt.Fprintln(d.rl.Stdout(), "  Effective Production Limit:  none")
	}

	// Override info
	if ec.IsOverride() {
		if reason, ok := ec.OverrideReason(); ok {
			fmt.Fprintf(d.rl.Stdout(), "  Override Reason: %s\n", reason)
		}
		if dir, ok := ec.OverrideDirection(); ok {
			fmt.Fprintf(d.rl.Stdout(), "  Override Direction: %s\n", dir)
		}
	}

	// Contractual limits
	if limit, ok := ec.ContractualConsumptionMax(); ok {
		fmt.Fprintf(d.rl.Stdout(), "  Contractual Consumption Max: %.1f kW\n", float64(limit)/1000000)
	}
	if limit, ok := ec.ContractualProductionMax(); ok {
		fmt.Fprintf(d.rl.Stdout(), "  Contractual Production Max:  %.1f kW\n", float64(limit)/1000000)
	}
}

// getEnergyControlFeature returns the EnergyControl feature wrapper for an endpoint.
func (d *Device) getEnergyControlFeature(endpointID uint8) *features.EnergyControl {
	device := d.svc.Device()
	endpoint, err := device.GetEndpoint(endpointID)
	if err != nil || endpoint == nil {
		return nil
	}

	feature, err := endpoint.GetFeature(model.FeatureEnergyControl)
	if err != nil || feature == nil {
		return nil
	}

	// Wrap the model.Feature in features.EnergyControl
	return &features.EnergyControl{Feature: feature}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// handleEvent handles service events and displays relevant ones to the user.
func (d *Device) handleEvent(event service.Event) {
	switch event.Type {
	case service.EventCommandInvoked:
		// Show command invocations on EnergyControl
		if event.FeatureID == uint16(model.FeatureEnergyControl) {
			d.displayCommandInvoked(event)
		}
	case service.EventValueChanged:
		// Only show EnergyControl attribute changes (limits, setpoints, state)
		if event.FeatureID == uint16(model.FeatureEnergyControl) {
			d.displayEnergyControlChange(event)
		}
	}
}

// displayCommandInvoked displays an EnergyControl command invocation with parameters.
func (d *Device) displayCommandInvoked(event service.Event) {
	cmdName := d.getEnergyControlCmdName(event.CommandID)
	if cmdName == "" {
		cmdName = fmt.Sprintf("command_%d", event.CommandID)
	}

	// Format zone ID
	zoneID := event.ZoneID
	if zoneID == "" {
		zoneID = "local"
	} else if len(zoneID) > 8 {
		zoneID = zoneID[:8]
	}

	// Build parameter summary. Parameter IDs are command-specific (per
	// docs/features/energy-control/1.0.yaml) — switch on the command to
	// look up by the right generated constant. Issue #11 fix: command
	// parameters are integer-keyed maps now (`map[uint8]any`).
	var paramParts []string
	if event.CommandParams != nil {
		switch event.CommandID {
		case features.EnergyControlCmdSetLimit:
			if v, ok := event.CommandParams[features.SetLimitParamConsumptionLimit]; ok {
				paramParts = append(paramParts, fmt.Sprintf("consumption=%.1f kW", toKW(v)))
			}
			if v, ok := event.CommandParams[features.SetLimitParamProductionLimit]; ok {
				paramParts = append(paramParts, fmt.Sprintf("production=%.1f kW", toKW(v)))
			}
			if v, ok := event.CommandParams[features.SetLimitParamDuration]; ok {
				paramParts = append(paramParts, fmt.Sprintf("duration=%vs", v))
			}
			if v, ok := event.CommandParams[features.SetLimitParamCause]; ok {
				paramParts = append(paramParts, fmt.Sprintf("cause=%s", formatLimitCause(v)))
			}
		case features.EnergyControlCmdSetSetpoint:
			if v, ok := event.CommandParams[features.SetSetpointParamConsumptionSetpoint]; ok {
				paramParts = append(paramParts, fmt.Sprintf("consumption=%.1f kW", toKW(v)))
			}
			if v, ok := event.CommandParams[features.SetSetpointParamProductionSetpoint]; ok {
				paramParts = append(paramParts, fmt.Sprintf("production=%.1f kW", toKW(v)))
			}
			if v, ok := event.CommandParams[features.SetSetpointParamDuration]; ok {
				paramParts = append(paramParts, fmt.Sprintf("duration=%vs", v))
			}
			if v, ok := event.CommandParams[features.SetSetpointParamCause]; ok {
				paramParts = append(paramParts, fmt.Sprintf("cause=%s", formatLimitCause(v)))
			}
		}
	}

	paramStr := ""
	if len(paramParts) > 0 {
		paramStr = " " + strings.Join(paramParts, ", ")
	}

	fmt.Fprintf(d.rl.Stdout(), "\n[%s] << %s (zone: %s)%s\n",
		time.Now().Format("15:04:05"),
		cmdName,
		zoneID,
		paramStr)
	d.rl.Refresh()
}

// getEnergyControlCmdName returns a human-readable name for an EnergyControl command.
func (d *Device) getEnergyControlCmdName(cmdID uint8) string {
	switch cmdID {
	case features.EnergyControlCmdSetLimit:
		return "SetLimit"
	case features.EnergyControlCmdClearLimit:
		return "ClearLimit"
	case features.EnergyControlCmdSetCurrentLimits:
		return "SetCurrentLimits"
	case features.EnergyControlCmdClearCurrentLimits:
		return "ClearCurrentLimits"
	case features.EnergyControlCmdSetSetpoint:
		return "SetSetpoint"
	case features.EnergyControlCmdClearSetpoint:
		return "ClearSetpoint"
	case features.EnergyControlCmdPause:
		return "Pause"
	case features.EnergyControlCmdResume:
		return "Resume"
	case features.EnergyControlCmdStop:
		return "Stop"
	default:
		return ""
	}
}

// toKW converts a milliwatt value (any integer type) to kilowatts.
func toKW(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n) / 1_000_000
	case uint64:
		return float64(n) / 1_000_000
	case float64:
		return n / 1_000_000
	case int:
		return float64(n) / 1_000_000
	default:
		return 0
	}
}

// formatLimitCause returns a human-readable name for a limit cause value.
func formatLimitCause(v any) string {
	var cause uint8
	switch n := v.(type) {
	case uint8:
		cause = n
	case uint64:
		cause = uint8(n)
	case int64:
		cause = uint8(n)
	default:
		return fmt.Sprintf("%v", v)
	}

	switch features.LimitCause(cause) {
	case features.LimitCauseGridEmergency:
		return "grid-emergency"
	case features.LimitCauseGridOptimization:
		return "grid-optimization"
	case features.LimitCauseLocalProtection:
		return "local-protection"
	case features.LimitCauseLocalOptimization:
		return "local-optimization"
	case features.LimitCauseUserPreference:
		return "user-preference"
	default:
		return fmt.Sprintf("cause(%d)", cause)
	}
}

// displayEnergyControlChange displays an EnergyControl attribute change event.
func (d *Device) displayEnergyControlChange(event service.Event) {
	// Check for control state change first (special handling)
	if event.AttributeID == features.EnergyControlAttrControlState {
		d.displayStateTransition(event)
		return
	}

	// Get attribute name for limit/setpoint changes
	attrName := d.getEnergyControlAttrName(event.AttributeID)
	if attrName == "" {
		return // Not an attribute we care about
	}

	// Format the zone ID (truncate for display)
	zoneID := event.ZoneID
	if zoneID == "" {
		zoneID = "local"
	} else if len(zoneID) > 8 {
		zoneID = zoneID[:8]
	}

	// Format the value
	var valueStr string
	if event.Value == nil {
		valueStr = "cleared"
	} else {
		// Value is in milliwatts, convert to kW
		switch v := event.Value.(type) {
		case int64:
			valueStr = fmt.Sprintf("%.1f kW", float64(v)/1_000_000)
		case float64:
			valueStr = fmt.Sprintf("%.1f kW", v/1_000_000)
		default:
			valueStr = fmt.Sprintf("%v", event.Value)
		}
	}

	// Print the event
	fmt.Fprintf(d.rl.Stdout(), "\n[%s] %s = %s\n",
		time.Now().Format("15:04:05"),
		attrName,
		valueStr)
	d.rl.Refresh()
}

// displayStateTransition displays a control state transition.
func (d *Device) displayStateTransition(event service.Event) {
	var newState features.ControlState
	switch v := event.Value.(type) {
	case uint8:
		newState = features.ControlState(v)
	case int64:
		newState = features.ControlState(v)
	default:
		return
	}

	// Skip if state hasn't actually changed
	if newState == d.lastControlState {
		return
	}

	// Show transition
	fmt.Fprintf(d.rl.Stdout(), "\n[%s] State %s -> %s\n",
		time.Now().Format("15:04:05"),
		d.lastControlState,
		newState)
	d.rl.Refresh()

	// Update tracked state
	d.lastControlState = newState
}

// getEnergyControlAttrName returns a human-readable name for limit/setpoint attributes.
func (d *Device) getEnergyControlAttrName(attrID uint16) string {
	switch attrID {
	// Consumption/production limits
	case features.EnergyControlAttrMyConsumptionLimit:
		return "consumptionLimit"
	case features.EnergyControlAttrMyProductionLimit:
		return "productionLimit"
	// Current limits
	case features.EnergyControlAttrMyCurrentLimitsConsumption:
		return "currentLimitsConsumption"
	case features.EnergyControlAttrMyCurrentLimitsProduction:
		return "currentLimitsProduction"
	// Setpoints
	case features.EnergyControlAttrMyConsumptionSetpoint:
		return "consumptionSetpoint"
	case features.EnergyControlAttrMyProductionSetpoint:
		return "productionSetpoint"
	// Current setpoints
	case features.EnergyControlAttrMyCurrentSetpointsConsumption:
		return "currentSetpointsConsumption"
	case features.EnergyControlAttrMyCurrentSetpointsProduction:
		return "currentSetpointsProduction"
	// Failsafe limits
	case features.EnergyControlAttrFailsafeConsumptionLimit:
		return "failsafeConsumptionLimit"
	case features.EnergyControlAttrFailsafeProductionLimit:
		return "failsafeProductionLimit"
	default:
		return ""
	}
}
