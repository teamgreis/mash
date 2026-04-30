package features

import "github.com/mash-protocol/mash-go/pkg/wire"

// CapabilityUnsupportedError returns the spec-defined error for a command
// that is gated by a capability flag the device does not advertise.
//
// Per docs/features/energy-control.md: "A device that only has a notion
// of being limited ... simply leaves isPausable = false, isShiftable =
// false, isStoppable = false ... The process lifecycle commands are
// ignored by such devices; the controller-facing response surfaces as
// wire.StatusUnsupported."
//
// Device implementations are responsible for checking the relevant
// capability flag in their command callbacks (OnPause, OnResume, OnStop,
// etc.) and returning this error when the flag is false.
func CapabilityUnsupportedError(commandName string) error {
	return &wire.CommandError{
		Status:  wire.StatusUnsupported,
		Message: "EnergyControl: " + commandName + " not supported by this device",
	}
}

// SetCapabilities sets the control capabilities.
func (e *EnergyControl) SetCapabilities(limits, currentLimits, setpoints, currentSetpoints, pausable, shiftable, stoppable bool) {
	_ = e.SetAcceptsLimits(limits)
	_ = e.SetAcceptsCurrentLimits(currentLimits)
	_ = e.SetAcceptsSetpoints(setpoints)
	_ = e.SetAcceptsCurrentSetpoints(currentSetpoints)
	_ = e.SetIsPausable(pausable)
	_ = e.SetIsShiftable(shiftable)
	_ = e.SetIsStoppable(stoppable)
}

// IsLimited returns true if currently in LIMITED state.
func (e *EnergyControl) IsLimited() bool {
	return e.ControlState() == ControlStateLimited
}

// IsFailsafe returns true if in FAILSAFE state.
func (e *EnergyControl) IsFailsafe() bool {
	return e.ControlState() == ControlStateFailsafe
}

// IsOverride returns true if in OVERRIDE state.
func (e *EnergyControl) IsOverride() bool {
	return e.ControlState() == ControlStateOverride
}
