package features

// Trigger opcode domain constants.
//
// The upper 2 bytes encode the feature domain (matching feature IDs from
// protocol-versions.yaml), the lower 2 bytes encode the specific trigger.
// Middle 4 bytes are reserved (zero).

// Commissioning triggers (domain 0x0001 = DeviceInfo).
const (
	TriggerEnterCommissioningMode uint64 = 0x0001_0000_0000_0001
	TriggerExitCommissioningMode  uint64 = 0x0001_0000_0000_0002
	TriggerFactoryReset           uint64 = 0x0001_0000_0000_0003
	TriggerResetTestState         uint64 = 0x0001_0000_0000_0004

	// TriggerAdjustClockBase encodes a clock offset in the lower 32 bits.
	// The value is in seconds (signed, stored as uint32 via two's complement).
	// Usage: TriggerAdjustClockBase | uint64(uint32(int32(offsetSeconds)))
	TriggerAdjustClockBase uint64 = 0x0001_0001_0000_0000
)

// Status triggers (domain 0x0002 = Status).
const (
	TriggerFault      uint64 = 0x0002_0000_0000_0001
	TriggerClearFault uint64 = 0x0002_0000_0000_0002
	TriggerSetStandby uint64 = 0x0002_0000_0000_0003
	TriggerSetRunning uint64 = 0x0002_0000_0000_0004

	// TriggerFaultWithCodeBase encodes a vendor-supplied faultCode in the
	// lower 32 bits, distinct from the bare TriggerFault which always
	// uses faultCode=1.
	// Usage: TriggerFaultWithCodeBase | uint64(faultCode)
	TriggerFaultWithCodeBase uint64 = 0x0002_0001_0000_0000

	// TriggerSetStateDetailBase encodes a vendor-specific stateDetail value
	// in the lower 32 bits. The harness uses this so corpus tests can
	// populate the nullable Status.stateDetail attribute on the device.
	// Usage: TriggerSetStateDetailBase | uint64(stateDetail)
	TriggerSetStateDetailBase uint64 = 0x0002_0002_0000_0000
)

// Measurement triggers (domain 0x0004 = Measurement).
const (
	TriggerSetPower100  uint64 = 0x0004_0000_0000_0001 // 100W
	TriggerSetPower1000 uint64 = 0x0004_0000_0000_0002 // 1kW
	TriggerSetPowerZero uint64 = 0x0004_0000_0000_0003
	TriggerSetSoC50     uint64 = 0x0004_0000_0000_0010
	TriggerSetSoC100    uint64 = 0x0004_0000_0000_0011

	// TriggerSetPowerCustomBase encodes an arbitrary power value in the lower 32 bits.
	// The value is in milliwatts (signed, stored as uint32).
	// Usage: TriggerSetPowerCustomBase | uint64(uint32(milliwatts))
	TriggerSetPowerCustomBase uint64 = 0x0004_0001_0000_0000
)

// ChargingSession triggers (domain 0x0006 = ChargingSession).
const (
	TriggerEVPlugIn        uint64 = 0x0006_0000_0000_0001
	TriggerEVUnplug        uint64 = 0x0006_0000_0000_0002
	TriggerEVRequestCharge uint64 = 0x0006_0000_0000_0003
)

// EnergyControl triggers (domain 0x0005 = EnergyControl).
const (
	TriggerControlStateAutonomous uint64 = 0x0005_0000_0000_0001
	TriggerControlStateControlled uint64 = 0x0005_0000_0000_0002
	TriggerControlStateLimited    uint64 = 0x0005_0000_0000_0003
	TriggerControlStateFailsafe   uint64 = 0x0005_0000_0000_0004
	TriggerControlStateOverride   uint64 = 0x0005_0000_0000_0005

	TriggerProcessStateNone      uint64 = 0x0005_0000_0000_0010
	TriggerProcessStateAvailable uint64 = 0x0005_0000_0000_0011
	TriggerProcessStateScheduled uint64 = 0x0005_0000_0000_0012
	TriggerProcessStateRunning   uint64 = 0x0005_0000_0000_0013
	TriggerProcessStatePaused    uint64 = 0x0005_0000_0000_0014
	TriggerProcessStateCompleted uint64 = 0x0005_0000_0000_0015
	TriggerProcessStateAborted   uint64 = 0x0005_0000_0000_0016

	// Capability override triggers (test-only). The capability flags
	// (isPausable, isShiftable, isStoppable) are readOnly attributes
	// representing static device capabilities; these triggers let the
	// conformance harness force a specific capability posture so tests
	// like TC-CTRL-010 can exercise the StatusUnsupported reject path
	// without spawning a separately configured device. TriggerResetTestState
	// restores the device's startup default.
	TriggerSetIsPausableFalse uint64 = 0x0005_0000_0000_0020
	TriggerSetIsPausableTrue  uint64 = 0x0005_0000_0000_0021
)

// TriggerDomain extracts the feature domain from a trigger opcode.
// The domain is encoded in the upper 2 bytes.
func TriggerDomain(trigger uint64) uint16 {
	return uint16(trigger >> 48)
}

// Manual TestControl command IDs and parameter IDs that aren't generated
// from the YAML spec (diagnostic/test-only commands). These live alongside
// the trigger constants so both pkg/service (handler registration) and
// internal/inspect (name<->ID translation) can reference them without
// import cycles.
const (
	// TestControlCmdGetTestState is the diagnostic command that returns a
	// device state snapshot for cross-test leak detection.
	TestControlCmdGetTestState uint8 = 3

	// GetTestStateParamEnableKey is the only parameter on getTestState
	// (CBOR integer key per docs/interaction-model.md §7.1).
	GetTestStateParamEnableKey uint8 = 1
)
