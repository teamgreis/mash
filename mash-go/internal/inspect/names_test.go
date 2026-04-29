package inspect_test

import (
	"testing"

	"github.com/mash-protocol/mash-go/internal/inspect"
	"github.com/mash-protocol/mash-go/pkg/model"
)

func TestResolveCommandName(t *testing.T) {
	tests := []struct {
		name      string
		feature   uint8
		command   string
		wantID    uint8
		wantFound bool
	}{
		{"tariff SetTariff", uint8(model.FeatureTariff), "settariff", 1, true},
		{"signals SendPriceSignal", uint8(model.FeatureSignals), "sendpricesignal", 1, true},
		{"signals ClearSignals", uint8(model.FeatureSignals), "clearsignals", 4, true},
		{"energycontrol SetLimit", uint8(model.FeatureEnergyControl), "setlimit", 1, true},
		{"energycontrol Stop", uint8(model.FeatureEnergyControl), "stop", 11, true},
		{"plan RequestPlan", uint8(model.FeaturePlan), "requestplan", 1, true},
		{"chargingsession SetChargingMode", uint8(model.FeatureChargingSession), "setchargingmode", 1, true},
		{"deviceinfo RemoveZone", uint8(model.FeatureDeviceInfo), "removezone", 16, true},
		{"testcontrol TriggerTestEvent", uint8(model.FeatureTestControl), "triggertestevent", 1, true},
		{"nonexistent command", uint8(model.FeatureTariff), "nonexistent", 0, false},
		{"wrong feature for command", uint8(model.FeatureMeasurement), "settariff", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, found := inspect.ResolveCommandName(tt.feature, tt.command)
			if found != tt.wantFound {
				t.Errorf("ResolveCommandName(0x%02x, %q) found = %v, want %v", tt.feature, tt.command, found, tt.wantFound)
			}
			if id != tt.wantID {
				t.Errorf("ResolveCommandName(0x%02x, %q) = %d, want %d", tt.feature, tt.command, id, tt.wantID)
			}
		})
	}
}

func TestResolveAttributeName(t *testing.T) {
	tests := []struct {
		name      string
		feature   uint8
		attr      string
		wantID    uint16
		wantFound bool
	}{
		// ChargingSession attributes
		{"chargingsession state", uint8(model.FeatureChargingSession), "state", 1, true},
		{"chargingsession evdemandmode", uint8(model.FeatureChargingSession), "evdemandmode", 40, true},
		{"chargingsession startdelay", uint8(model.FeatureChargingSession), "startdelay", 80, true},
		{"chargingsession evstateofcharge", uint8(model.FeatureChargingSession), "evstateofcharge", 30, true},
		{"chargingsession sessionenergycharged", uint8(model.FeatureChargingSession), "sessionenergycharged", 10, true},
		{"chargingsession evbatterycapacity", uint8(model.FeatureChargingSession), "evbatterycapacity", 31, true},
		{"chargingsession evtargetstateofcharge", uint8(model.FeatureChargingSession), "evtargetstateofcharge", 33, true},
		{"chargingsession chargingmode", uint8(model.FeatureChargingSession), "chargingmode", 70, true},
		{"chargingsession evminenergyrequest", uint8(model.FeatureChargingSession), "evminenergyrequest", 41, true},
		{"chargingsession global featuremap", uint8(model.FeatureChargingSession), "featuremap", 0xFFFC, true},

		// Signals attributes
		{"signals signalsource", uint8(model.FeatureSignals), "signalsource", 1, true},
		{"signals priceslots", uint8(model.FeatureSignals), "priceslots", 10, true},
		{"signals constraintslots", uint8(model.FeatureSignals), "constraintslots", 20, true},
		{"signals forecastslots", uint8(model.FeatureSignals), "forecastslots", 30, true},
		{"signals global featuremap", uint8(model.FeatureSignals), "featuremap", 0xFFFC, true},

		// Plan attributes
		{"plan planid", uint8(model.FeaturePlan), "planid", 1, true},
		{"plan commitment", uint8(model.FeaturePlan), "commitment", 3, true},
		{"plan starttime", uint8(model.FeaturePlan), "starttime", 10, true},
		{"plan totalenergyplanned", uint8(model.FeaturePlan), "totalenergyplanned", 20, true},
		{"plan slots", uint8(model.FeaturePlan), "slots", 40, true},
		{"plan global featuremap", uint8(model.FeaturePlan), "featuremap", 0xFFFC, true},

		// Tariff attributes
		{"tariff tariffid", uint8(model.FeatureTariff), "tariffid", 1, true},
		{"tariff currency", uint8(model.FeatureTariff), "currency", 2, true},
		{"tariff priceunit", uint8(model.FeatureTariff), "priceunit", 3, true},
		{"tariff tariffdescription", uint8(model.FeatureTariff), "tariffdescription", 4, true},
		{"tariff global featuremap", uint8(model.FeatureTariff), "featuremap", 0xFFFC, true},

		// DeviceInfo - verify usecases was added
		{"deviceinfo usecases", uint8(model.FeatureDeviceInfo), "usecases", 21, true},
		{"deviceinfo endpoints", uint8(model.FeatureDeviceInfo), "endpoints", 20, true},

		// Existing features still work
		{"measurement acactivepower", uint8(model.FeatureMeasurement), "acactivepower", 1, true},
		{"energycontrol controlstate", uint8(model.FeatureEnergyControl), "controlstate", 2, true},
		{"electrical phasecount", uint8(model.FeatureElectrical), "phasecount", 1, true},

		// Negative cases
		{"nonexistent attr", uint8(model.FeatureChargingSession), "nonexistent", 0, false},
		{"unknown feature", 0xFF, "state", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, found := inspect.ResolveAttributeName(tt.feature, tt.attr)
			if found != tt.wantFound {
				t.Errorf("ResolveAttributeName(0x%02x, %q) found = %v, want %v", tt.feature, tt.attr, found, tt.wantFound)
			}
			if id != tt.wantID {
				t.Errorf("ResolveAttributeName(0x%02x, %q) = %d, want %d", tt.feature, tt.attr, id, tt.wantID)
			}
		})
	}
}

func TestGetCommandName(t *testing.T) {
	tests := []struct {
		name     string
		feature  uint8
		cmdID    uint8
		wantName string
	}{
		{"tariff cmd 1", uint8(model.FeatureTariff), 1, "setTariff"},
		{"signals cmd 4", uint8(model.FeatureSignals), 4, "clearSignals"},
		{"unknown cmd", uint8(model.FeatureTariff), 99, ""},
		{"unknown feature", 0xFF, 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := inspect.GetCommandName(tt.feature, tt.cmdID)
			if name != tt.wantName {
				t.Errorf("GetCommandName(0x%02x, %d) = %q, want %q", tt.feature, tt.cmdID, name, tt.wantName)
			}
		})
	}
}

func TestCommandParamID_SetLimit(t *testing.T) {
	feature := uint8(model.FeatureEnergyControl)
	cmd := uint8(1) // setLimit

	tests := []struct {
		name      string
		paramName string
		wantID    uint8
		wantFound bool
	}{
		{"consumptionLimit", "consumptionLimit", 1, true},
		{"productionLimit", "productionLimit", 2, true},
		{"duration", "duration", 3, true},
		{"cause", "cause", 4, true},
		{"unknown", "totallyMadeUp", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, found := inspect.CommandParamID(feature, cmd, tt.paramName)
			if found != tt.wantFound || id != tt.wantID {
				t.Errorf("CommandParamID(%q) = (%d,%v), want (%d,%v)",
					tt.paramName, id, found, tt.wantID, tt.wantFound)
			}
		})
	}
}

func TestConvertCommandParams_NamedToInt(t *testing.T) {
	feature := uint8(model.FeatureEnergyControl)
	cmd := uint8(1) // setLimit

	args := map[string]any{
		"consumptionLimit": int64(7000000),
		"cause":            uint8(3),
	}
	out := inspect.ConvertCommandParams(feature, cmd, args)
	got, ok := out.(map[uint8]any)
	if !ok {
		t.Fatalf("ConvertCommandParams returned %T, want map[uint8]any", out)
	}
	if got[1] != int64(7000000) {
		t.Errorf("got[1]=%v, want 7000000", got[1])
	}
	if got[4] != uint8(3) {
		t.Errorf("got[4]=%v, want 3", got[4])
	}
}

func TestConvertCommandParams_AlreadyIntKeyed(t *testing.T) {
	args := map[uint8]any{1: int64(5000000), 4: uint8(2)}
	out := inspect.ConvertCommandParams(uint8(model.FeatureEnergyControl), 1, args)
	got, ok := out.(map[uint8]any)
	if !ok || got[1] != int64(5000000) || got[4] != uint8(2) {
		t.Errorf("pass-through failed: %v %v", out, ok)
	}
}

func TestConvertCommandParams_UnknownCommand(t *testing.T) {
	// Unknown feature/command pair must pass args through verbatim so
	// legacy/test-only invokes are not silently dropped.
	args := map[string]any{"foo": "bar"}
	out := inspect.ConvertCommandParams(0xFE, 0xFE, args)
	got, ok := out.(map[string]any)
	if !ok || got["foo"] != "bar" {
		t.Errorf("expected unmodified passthrough, got %v ok=%v", out, ok)
	}
}

func TestRemapResponsePayload_SetLimitNamed(t *testing.T) {
	// Wire response from device: integer-keyed map[any]any post-CBOR.
	payload := map[any]any{
		uint64(1): true,
		uint64(2): uint64(0),
		uint64(3): int64(7000000),
	}
	out := inspect.RemapResponsePayload(uint8(model.FeatureEnergyControl), 1, payload)
	if out["applied"] != true {
		t.Errorf("applied=%v, want true", out["applied"])
	}
	if out["controlState"] != uint64(0) {
		t.Errorf("controlState=%v, want 0", out["controlState"])
	}
	if out["effectiveConsumptionLimit"] != int64(7000000) {
		t.Errorf("effectiveConsumptionLimit=%v, want 7000000", out["effectiveConsumptionLimit"])
	}
}

func TestRemapResponsePayload_UnknownKeyFallsThrough(t *testing.T) {
	payload := map[any]any{
		uint64(1):   true,
		uint64(99): "extra",
	}
	out := inspect.RemapResponsePayload(uint8(model.FeatureEnergyControl), 1, payload)
	if out["applied"] != true {
		t.Errorf("applied=%v, want true", out["applied"])
	}
	if out["99"] != "extra" {
		t.Errorf("expected unknown key 99 to fall through as %q, got %v", "99", out["99"])
	}
}

func TestRemapResponsePayload_NonMapPayload(t *testing.T) {
	if got := inspect.RemapResponsePayload(uint8(model.FeatureEnergyControl), 1, "not-a-map"); got != nil {
		t.Errorf("expected nil for non-map payload, got %v", got)
	}
}
