// Command mash-device is a reference MASH device implementation.
//
// This command demonstrates a complete MASH-compliant device with:
//   - CLI argument parsing
//   - Configuration file support
//   - Multiple simulated device types (EVSE, inverter, battery)
//   - mDNS discovery advertising
//   - Commissioning support
//   - Comprehensive logging
//
// Usage:
//
//	mash-device [flags]
//
// Flags:
//
//	-type string        Device type: evse, inverter, battery (default "evse")
//	-config string      Configuration file path
//	-discriminator int  Discriminator for commissioning (0-4095)
//	-setup-code string  8-digit setup code for commissioning
//	-port int           Listen port (default 8443)
//	-log-level string   Log level: debug, info, warn, error (default "info")
//	-simulate           Enable simulation mode with synthetic data
//	-interactive        Enable interactive command mode
//	-state-dir string   Directory for persistent state
//	-reset              Clear all persisted state before starting
//	-protocol-log string File path for protocol event logging (CBOR format)
//
// Examples:
//
//	# Start EVSE device with default settings
//	mash-device -type evse -discriminator 1234 -setup-code 20202021
//
//	# Start inverter with config file
//	mash-device -type inverter -config /etc/mash/inverter.yaml
//
//	# Start battery in simulation mode
//	mash-device -type battery -simulate -log-level debug
//
//	# Start with persistence (remembers zones across restarts)
//	mash-device -type evse -state-dir /var/lib/mash-device
//
//	# Reset persistent state
//	mash-device -type evse -state-dir /var/lib/mash-device -reset
//
//	# Start in interactive mode (simulation controlled manually)
//	mash-device -type evse -interactive
//
// Interactive Commands:
//
//	start       - Start simulation
//	stop        - Stop simulation
//	power <kw>  - Set power value directly (in kW)
//	status      - Show device status
//	help        - Show available commands
//	quit        - Exit the device
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mash-protocol/mash-go/cmd/mash-device/interactive"
	"github.com/mash-protocol/mash-go/pkg/cert"
	"github.com/mash-protocol/mash-go/pkg/discovery"
	"github.com/mash-protocol/mash-go/internal/examples"
	"github.com/mash-protocol/mash-go/pkg/features"
	mashlog "github.com/mash-protocol/mash-go/pkg/log"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/persistence"
	"github.com/mash-protocol/mash-go/pkg/service"
	"github.com/mash-protocol/mash-go/pkg/usecase"
)

// DeviceType represents supported device types.
type DeviceType string

const (
	DeviceTypeEVSE     DeviceType = "evse"
	DeviceTypeInverter DeviceType = "inverter"
	DeviceTypeBattery  DeviceType = "battery"
	DeviceTypeHeatPump DeviceType = "heatpump"
)

// Config holds the device configuration.
// It implements interactive.DeviceConfig.
type Config struct {
	Type              DeviceType
	ConfigFile        string
	Discriminator     uint16
	SetupCode         string
	Port              int
	LogLevel          string
	Simulate          bool
	Interactive       bool

	// Device-specific settings
	SerialNumber string
	Brand        string
	Model        string
	DeviceName   string

	// Persistence settings
	StateDir string
	Reset    bool

	// Protocol logging
	ProtocolLogFile string

	// Test harness support
	EnableKey string // Hex-encoded 128-bit key for TestControl triggers
}

// DeviceType implements interactive.DeviceConfig.
func (c *Config) DeviceType() interactive.DeviceType {
	return interactive.DeviceType(c.Type)
}

var (
	config        Config
	discriminator uint // Temp var for flag parsing

	// Device service (used by simulation and event handling)
	deviceSvc *service.DeviceService
)

func init() {
	flag.StringVar((*string)(&config.Type), "type", "evse", "Device type: evse, inverter, battery, heatpump")
	flag.StringVar(&config.ConfigFile, "config", "", "Configuration file path")
	flag.UintVar(&discriminator, "discriminator", 1234, "Discriminator for commissioning (0-4095)")
	flag.StringVar(&config.SetupCode, "setup-code", "20202021", "8-digit setup code for commissioning")
	flag.IntVar(&config.Port, "port", 8443, "Listen port")
	flag.StringVar(&config.LogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	flag.BoolVar(&config.Simulate, "simulate", false, "Enable simulation mode with synthetic data")
	flag.BoolVar(&config.Interactive, "interactive", false, "Enable interactive command mode")

	flag.StringVar(&config.SerialNumber, "serial", "", "Device serial number (auto-generated if empty)")
	flag.StringVar(&config.Brand, "brand", "MASH Reference", "Device brand/vendor name")
	flag.StringVar(&config.Model, "model", "", "Device model name (auto-generated if empty)")
	flag.StringVar(&config.DeviceName, "name", "", "User-friendly device name")

	flag.StringVar(&config.StateDir, "state-dir", "", "Directory for persistent state")
	flag.BoolVar(&config.Reset, "reset", false, "Clear all persisted state before starting")

	flag.StringVar(&config.ProtocolLogFile, "protocol-log", "", "File path for protocol event logging (CBOR format)")

	flag.StringVar(&config.EnableKey, "enable-key", "00112233445566778899aabbccddeeff", "128-bit hex key for TestControl triggers (32 hex chars)")
}

func main() {
	flag.Parse()
	config.Discriminator = uint16(discriminator)

	// Setup logging
	setupLogging(config.LogLevel)

	log.Println("MASH Reference Device")
	log.Println("=====================")
	log.Printf("Device type: %s", config.Type)
	log.Printf("Discriminator: %d", config.Discriminator)
	log.Printf("Port: %d", config.Port)

	// Validate configuration
	if err := validateConfig(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Apply defaults
	applyDefaults()

	// Create device based on type
	device, limitResolver, deviceCategory := createDevice()
	if device == nil {
		log.Fatalf("Failed to create device of type: %s", config.Type)
	}

	// Create structured logger based on --log-level flag.
	// This enables all debugLog() calls in device_service.go.
	var slogLevel slog.Level
	switch config.LogLevel {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slogLevel}))

	// Create device service
	svcConfig := service.DefaultDeviceConfig()
	svcConfig.Discriminator = config.Discriminator
	svcConfig.SetupCode = config.SetupCode
	svcConfig.SerialNumber = config.SerialNumber
	svcConfig.Brand = config.Brand
	svcConfig.Model = config.Model
	svcConfig.DeviceName = config.DeviceName
	svcConfig.Categories = []discovery.DeviceCategory{deviceCategory}
	svcConfig.OperationalListenAddress = fmt.Sprintf(":%d", config.Port)
	svcConfig.TestEnableKey = config.EnableKey
	svcConfig.ListenForPairingRequests = true
	svcConfig.Logger = logger

	// Add TestControl feature to root endpoint.
	// TestControl is always compiled in (like Matter's TestEventTrigger), but
	// testEventTriggersEnabled is only true when a valid enable-key is provided.
	// This follows Matter's pattern: same device code, just enable via --enable-key.
	testControl := features.NewTestControl()
	enableKeyValid := config.EnableKey != "" && config.EnableKey != "00000000000000000000000000000000"
	_ = testControl.SetTestEventTriggersEnabled(enableKeyValid)
	device.RootEndpoint().AddFeature(testControl.Feature)

	// Refresh DeviceInfo endpoints attribute so auto-PICS discovery
	// sees TestControl (used to derive MASH.S.ZONE.MAX).
	if feat, err := device.RootEndpoint().GetFeature(model.FeatureDeviceInfo); err == nil {
		if attr, err := feat.GetAttribute(features.DeviceInfoAttrEndpoints); err == nil {
			var epInfos []*model.EndpointInfo
			for _, ep := range device.Endpoints() {
				epInfos = append(epInfos, ep.Info())
			}
			_ = attr.SetValueInternal(epInfos)
		}
	}

	// Set up protocol logging if requested
	var protocolLogger *mashlog.FileLogger
	if config.ProtocolLogFile != "" {
		var err error
		protocolLogger, err = mashlog.NewFileLogger(config.ProtocolLogFile)
		if err != nil {
			log.Fatalf("Failed to create protocol logger: %v", err)
		}
		svcConfig.ProtocolLogger = protocolLogger
		log.Printf("Protocol logging to: %s", config.ProtocolLogFile)
	}

	svc, err := service.NewDeviceService(device, svcConfig)
	if err != nil {
		log.Fatalf("Failed to create device service: %v", err)
	}

	// Set up mDNS browser for pairing request listening.
	browser, err := discovery.NewMDNSBrowser(discovery.DefaultBrowserConfig())
	if err != nil {
		log.Printf("Warning: Failed to create mDNS browser: %v", err)
	} else {
		svc.SetBrowser(browser)
	}

	// Register test event trigger handler when enable-key is valid.
	// This follows Matter's pattern: TestEventTrigger always compiled in,
	// but only functional when enable-key is provided.
	if enableKeyValid {
		svc.RegisterTestEventHandler(testControl)
		svc.RegisterSetCommissioningWindowDurationHandler(testControl)
		svc.RegisterGetTestStateHandler(testControl)
		log.Printf("TestControl triggers enabled (enable-key: %s...)", config.EnableKey[:8])
	}

	// Wire per-zone "my" attribute notifications.
	// When a zone's limit changes, the resolver notifies that specific zone's
	// subscriptions so it sees its own myConsumptionLimit/myProductionLimit values.
	if limitResolver != nil {
		const endpointID uint8 = 1
		featureID := uint8(model.FeatureEnergyControl)
		limitResolver.OnZoneMyChange = func(zoneID string, changes map[uint16]any) {
			svc.NotifyZoneAttributeChange(zoneID, endpointID, featureID, changes)
		}
		svc.SetLimitResolver(limitResolver)
	}

	// Store for simulation
	deviceSvc = svc

	// Register event handler early so we can see events during state loading
	svc.OnEvent(handleEvent)

	// Set up persistence if state-dir is provided
	if config.StateDir != "" {
		log.Printf("Using state directory: %s", config.StateDir)

		// Create certificate store
		certStore := cert.NewFileStore(config.StateDir)

		// Create state store
		stateStore := persistence.NewDeviceStateStore(filepath.Join(config.StateDir, "state.json"))

		// Handle --reset flag
		if config.Reset {
			log.Println("Resetting persisted state...")
			if err := stateStore.Clear(); err != nil {
				log.Printf("Warning: Failed to clear state: %v", err)
			}
			// Note: certStore.Clear() would need to be implemented if we want to clear certs too
		}

		// Load existing certs (may fail if first run)
		if err := certStore.Load(); err != nil {
			log.Printf("No existing certificates found (first run or reset): %v", err)
		}

		// Set stores on the service
		svc.SetCertStore(certStore)
		svc.SetStateStore(stateStore)

		// Load state (will restore zone memberships, failsafe timers, etc.)
		if err := svc.LoadState(); err != nil {
			log.Printf("Warning: Failed to load state: %v", err)
		}
	}

	// Start service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := svc.Start(ctx); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}
	log.Printf("Service started (state: %s)", svc.State())

	// Check if we have existing zones from persistence
	knownZones := svc.ZoneCount()
	if knownZones > 0 {
		// Device is already commissioned - start operational advertising
		log.Printf("Device has %d known zone(s), starting operational advertising", knownZones)
		if err := svc.StartOperationalAdvertising(); err != nil {
			log.Printf("Warning: Failed to start operational advertising: %v", err)
		}
	} else {
		// No zones - enter commissioning mode
		if err := svc.EnterCommissioningMode(); err != nil {
			log.Printf("Warning: Failed to enter commissioning mode: %v", err)
		} else {
			log.Println("Commissioning mode active")
			printCommissioningInfo()
		}
	}

	// Set up simulation behavior
	if config.Interactive {
		log.Println("Interactive mode enabled - use 'start' to begin simulation")
		id, err := interactive.New(svc, &config)
		if err != nil {
			log.Fatalf("Failed to create interactive device: %v", err)
		}
		// Redirect log output through readline to avoid interfering with input
		log.SetOutput(id.Stdout())
		go id.Run(ctx, cancel)
	} else if config.Simulate {
		// Note: Simulation starts automatically when a zone connects (see handleEvent)
		log.Println("Simulation mode enabled (will start when controller connects)")
	}

	// Wait for shutdown signal or context cancellation
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal: %v", sig)
	case <-ctx.Done():
		// Context was cancelled (e.g., by interactive quit command)
	}

	log.Println("Shutting down...")

	// Save state before stopping
	if config.StateDir != "" {
		log.Println("Saving state...")
		if err := svc.SaveState(); err != nil {
			log.Printf("Warning: Failed to save state: %v", err)
		}
	}

	if err := svc.Stop(); err != nil {
		log.Printf("Error stopping service: %v", err)
	}

	// Close protocol logger
	if protocolLogger != nil {
		if err := protocolLogger.Close(); err != nil {
			log.Printf("Warning: Failed to close protocol logger: %v", err)
		}
	}

	log.Println("Goodbye!")
}

func setupLogging(level string) {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	switch level {
	case "debug":
		log.SetFlags(log.Ltime | log.Lmicroseconds | log.Lshortfile)
	case "warn", "error":
		log.SetFlags(log.Ltime)
	}
}

func validateConfig() error {
	if config.Discriminator > 4095 {
		return fmt.Errorf("discriminator must be 0-4095, got %d", config.Discriminator)
	}
	if len(config.SetupCode) != 8 {
		return fmt.Errorf("setup code must be 8 digits, got %d", len(config.SetupCode))
	}
	switch config.Type {
	case DeviceTypeEVSE, DeviceTypeInverter, DeviceTypeBattery, DeviceTypeHeatPump:
		// Valid
	default:
		return fmt.Errorf("unknown device type: %s", config.Type)
	}

	// Validate enable key format when provided.
	// Enable-key activates TestControl triggers (like Matter's TestEventTrigger).
	if config.EnableKey != "" {
		if len(config.EnableKey) != 32 {
			return fmt.Errorf("enable-key must be 32 hex characters (128-bit), got %d", len(config.EnableKey))
		}
		for _, c := range config.EnableKey {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("enable-key contains non-hex character: %c", c)
			}
		}
	}

	return nil
}

func applyDefaults() {
	if config.SerialNumber == "" {
		config.SerialNumber = fmt.Sprintf("%s-%d", config.Type, time.Now().Unix()%10000)
	}
	if config.Model == "" {
		switch config.Type {
		case DeviceTypeEVSE:
			config.Model = "Reference EVSE 22kW"
		case DeviceTypeInverter:
			config.Model = "Reference Inverter 10kW"
		case DeviceTypeBattery:
			config.Model = "Reference Battery 10kWh"
		case DeviceTypeHeatPump:
			config.Model = "Reference Heat Pump 8kW"
		}
	}
	if config.DeviceName == "" {
		config.DeviceName = fmt.Sprintf("MASH %s", config.Type)
	}
}

// specDeviceID composes a spec-compliant DeviceInfo.deviceId per
// docs/features/device-info.md §"Device ID Format". This reference CLI
// uses placeholder Vendor IDs (0x1234) rather than real IANA Private
// Enterprise Numbers, so we use the "without PEN" fallback form
// <vendor>-<product>-<serial>. Production devices with an assigned PEN
// should use PEN<number>.<serial> instead.
func specDeviceID(brand, model, serial string) string {
	return fmt.Sprintf("%s-%s-%s", brand, model, serial)
}

func createDevice() (*model.Device, *features.LimitResolver, discovery.DeviceCategory) {
	switch config.Type {
	case DeviceTypeEVSE:
		evse := examples.NewEVSE(examples.EVSEConfig{
			DeviceID:              specDeviceID(config.Brand, config.Model, config.SerialNumber),
			VendorName:            config.Brand,
			ProductName:           config.Model,
			SerialNumber:          config.SerialNumber,
			VendorID:              0x1234,
			ProductID:             0x0001,
			PhaseCount:            3,
			NominalVoltage:        230,
			MaxCurrentPerPhase:    32000,
			MinCurrentPerPhase:    6000,
			NominalMaxPower:       22000000,
			NominalMinPower:       1380000,
			SupportsBidirectional: false,
		})
		return evse.Device(), evse.LimitResolver(), discovery.CategoryEMobility

	case DeviceTypeInverter:
		device, resolver := createInverterDevice()
		return device, resolver, discovery.CategoryInverter

	case DeviceTypeBattery:
		device, resolver := createBatteryDevice()
		return device, resolver, discovery.CategoryInverter

	case DeviceTypeHeatPump:
		device, resolver := createHeatPumpDevice()
		return device, resolver, discovery.CategoryHVAC

	default:
		return nil, nil, 0
	}
}

func createInverterDevice() (*model.Device, *features.LimitResolver) {
	deviceID := specDeviceID(config.Brand, config.Model, config.SerialNumber)
	device := model.NewDevice(deviceID, 0x1234, 0x0002)

	// Root endpoint with DeviceInfo
	deviceInfo := features.NewDeviceInfo()
	deviceInfo.Feature.SetFeatureMap(uint32(model.FeatureMapCore | model.FeatureMapFlex))
	_ = deviceInfo.SetDeviceID(deviceID)
	_ = deviceInfo.SetVendorName(config.Brand)
	_ = deviceInfo.SetProductName(config.Model)
	_ = deviceInfo.SetSerialNumber(config.SerialNumber)
	_ = deviceInfo.SetSoftwareVersion("1.0.0")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Inverter endpoint
	inverter := model.NewEndpoint(1, model.EndpointInverter, "PV Inverter")

	// PV inverter capability bitmap: CORE + FLEX
	pvCapabilities := uint32(model.FeatureMapCore | model.FeatureMapFlex)

	// Electrical capabilities
	electrical := features.NewElectrical()
	electrical.Feature.SetFeatureMap(pvCapabilities)
	_ = electrical.SetPhaseCount(3)
	_ = electrical.SetNominalVoltage(230)
	_ = electrical.SetNominalFrequency(50)
	_ = electrical.SetNominalMaxProduction(10000000) // 10 kW
	_ = electrical.SetSupportedDirections(features.DirectionProduction)
	inverter.AddFeature(electrical.Feature)

	// Measurement
	measurement := features.NewMeasurement()
	measurement.Feature.SetFeatureMap(pvCapabilities)
	inverter.AddFeature(measurement.Feature)

	// EnergyControl - accepts production limits (curtailment)
	energyControl := features.NewEnergyControl()
	energyControl.Feature.SetFeatureMap(pvCapabilities)
	_ = energyControl.SetDeviceType(features.DeviceTypeInverter)
	_ = energyControl.SetControlState(features.ControlStateAutonomous)
	energyControl.SetCapabilities(true, false, false, false, false, false, false)
	resolver := setupEnergyControlHandler(energyControl)
	inverter.AddFeature(energyControl.Feature)

	// Status
	status := features.NewStatus()
	status.Feature.SetFeatureMap(pvCapabilities)
	_ = status.SetOperatingState(features.OperatingStateRunning)
	inverter.AddFeature(status.Feature)

	_ = device.AddEndpoint(inverter)

	// Populate use cases on DeviceInfo
	decls := usecase.EvaluateDevice(device, usecase.Registry)
	_ = deviceInfo.SetUseCases(decls)

	return device, resolver
}

func createBatteryDevice() (*model.Device, *features.LimitResolver) {
	deviceID := specDeviceID(config.Brand, config.Model, config.SerialNumber)
	device := model.NewDevice(deviceID, 0x1234, 0x0003)

	// Root endpoint with DeviceInfo
	deviceInfo := features.NewDeviceInfo()
	deviceInfo.Feature.SetFeatureMap(uint32(model.FeatureMapCore | model.FeatureMapFlex | model.FeatureMapBattery))
	_ = deviceInfo.SetDeviceID(deviceID)
	_ = deviceInfo.SetVendorName(config.Brand)
	_ = deviceInfo.SetProductName(config.Model)
	_ = deviceInfo.SetSerialNumber(config.SerialNumber)
	_ = deviceInfo.SetSoftwareVersion("1.0.0")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Battery endpoint
	battery := model.NewEndpoint(1, model.EndpointBattery, "Battery Storage")

	// Battery capability bitmap: CORE + FLEX + BATTERY
	batteryCapabilities := uint32(model.FeatureMapCore | model.FeatureMapFlex | model.FeatureMapBattery)

	// Electrical capabilities
	electrical := features.NewElectrical()
	electrical.Feature.SetFeatureMap(batteryCapabilities)
	_ = electrical.SetPhaseCount(3)
	_ = electrical.SetNominalVoltage(230)
	_ = electrical.SetNominalFrequency(50)
	_ = electrical.SetNominalMaxConsumption(5000000)  // 5 kW charge
	_ = electrical.SetNominalMaxProduction(5000000)   // 5 kW discharge
	_ = electrical.SetSupportedDirections(features.DirectionBidirectional)
	battery.AddFeature(electrical.Feature)

	// Measurement
	measurement := features.NewMeasurement()
	measurement.Feature.SetFeatureMap(batteryCapabilities)
	battery.AddFeature(measurement.Feature)

	// EnergyControl
	energyControl := features.NewEnergyControl()
	energyControl.Feature.SetFeatureMap(batteryCapabilities)
	_ = energyControl.SetDeviceType(features.DeviceTypeBattery)
	_ = energyControl.SetControlState(features.ControlStateAutonomous)
	energyControl.SetCapabilities(true, false, true, false, true, true, true)
	resolver := setupEnergyControlHandler(energyControl)
	battery.AddFeature(energyControl.Feature)

	// Status
	status := features.NewStatus()
	status.Feature.SetFeatureMap(batteryCapabilities)
	_ = status.SetOperatingState(features.OperatingStateStandby)
	battery.AddFeature(status.Feature)

	_ = device.AddEndpoint(battery)

	// Populate use cases on DeviceInfo
	decls := usecase.EvaluateDevice(device, usecase.Registry)
	_ = deviceInfo.SetUseCases(decls)

	return device, resolver
}

func createHeatPumpDevice() (*model.Device, *features.LimitResolver) {
	hp := examples.NewHeatPump(examples.HeatPumpConfig{
		DeviceID:           specDeviceID(config.Brand, config.Model, config.SerialNumber),
		VendorName:         config.Brand,
		ProductName:        config.Model,
		SerialNumber:       config.SerialNumber,
		VendorID:           0x1234,
		ProductID:          0x0004,
		PhaseCount:         3,
		NominalVoltage:     230,
		NominalMaxPower:    8000000, // 8 kW
		NominalMinPower:    1500000, // 1.5 kW
		MaxCurrentPerPhase: 12000,   // 12A
	})
	return hp.Device(), hp.LimitResolver()
}

// setupEnergyControlHandler configures the SetLimit/ClearLimit handlers
// using LimitResolver for per-zone tracking with "most restrictive wins" resolution.
// Returns the resolver so the caller can wire the OnZoneMyChange callback.
func setupEnergyControlHandler(energyControl *features.EnergyControl) *features.LimitResolver {
	resolver := features.NewLimitResolver(energyControl)
	resolver.Register()
	return resolver
}

func handleEvent(event service.Event) {
	switch event.Type {
	case service.EventConnected:
		log.Printf("[EVENT] Zone connected: %s", event.ZoneID)
		connectedCnt++

		// Check if this is a TEST zone -- disable simulation for deterministic testing.
		// Unlike --test-mode (which enables TestControl triggers), this specifically
		// targets simulation behavior: TEST zones need deterministic responses.
		if zone := deviceSvc.GetZone(event.ZoneID); zone != nil && zone.Type == cert.ZoneTypeTest {
			log.Println("[EVENT] TEST zone connected - disabling simulation for deterministic testing")
			config.Simulate = false
			stopSimulation()
		}

		// Start simulation on first connection (unless in interactive mode or simulation disabled)
		if config.Simulate && !config.Interactive && !simRunning && connectedCnt == 1 {
			startSimulation()
		}

	case service.EventDisconnected:
		log.Printf("[EVENT] Zone disconnected: %s", event.ZoneID)
		connectedCnt--
		// Stop simulation when all zones disconnect
		if connectedCnt <= 0 {
			connectedCnt = 0
			stopSimulation()
		}

	case service.EventCommissioningOpened:
		log.Println("[EVENT] Commissioning window opened")

	case service.EventCommissioningClosed:
		if event.Reason == "timeout" {
			log.Println("[EVENT] Commissioning window EXPIRED (15m default timeout)")
		} else if event.Reason == "commissioned" {
			log.Println("[EVENT] Commissioning window closed (device commissioned)")
		} else {
			log.Println("[EVENT] Commissioning window closed")
		}

	case service.EventFailsafeTriggered:
		log.Printf("[EVENT] FAILSAFE triggered for zone %s!", event.ZoneID)
		// Stop simulation on failsafe - device should go to safe state
		stopSimulation()

	case service.EventFailsafeCleared:
		log.Printf("[EVENT] Failsafe cleared for zone %s", event.ZoneID)
		// Resume simulation if still connected
		if config.Simulate && !simRunning && connectedCnt > 0 {
			startSimulation()
		}

	case service.EventValueChanged:
		// In interactive mode, the interactive handler displays these with better formatting.
		// Only log in non-interactive mode.
		if !config.Interactive {
			log.Printf("[EVENT] Value changed (zone: %s)", event.ZoneID)
		}

	case service.EventZoneRestored:
		log.Printf("[EVENT] Zone restored from persistence: %s (awaiting reconnection)", event.ZoneID)

	case service.EventZoneRemoved:
		log.Printf("[EVENT] Zone removed: %s", event.ZoneID)
	}
}


func printCommissioningInfo() {
	qrString := fmt.Sprintf("MASH:1:%d:%s", config.Discriminator, config.SetupCode)

	log.Println("")
	log.Println("============================================")
	log.Println("         COMMISSIONING INFORMATION          ")
	log.Println("============================================")
	log.Printf("QR Code String: %s", qrString)
	log.Println("")
	log.Printf("  Discriminator: %d", config.Discriminator)
	log.Printf("  Setup Code:    %s", config.SetupCode)
	log.Printf("  Port:          %d", config.Port)
	log.Println("============================================")
	log.Println("")
}


