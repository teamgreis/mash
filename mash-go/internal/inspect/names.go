package inspect

import (
	"fmt"
	"strings"

	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
)

// Name tables for resolving human-readable names to IDs.
// Tables are populated by generated code in names_gen.go.
var (
	endpointNames  map[string]uint8
	featureNames   map[string]uint8
	attributeNames = map[uint8]map[string]uint16{}
	commandNames   = map[uint8]map[string]uint8{}

	// commandResponseFieldNames[featureID][cmdID][fieldID] = spec field name.
	// Populated by initGeneratedNameTables() so the test harness can rewrite
	// CBOR-decoded invoke responses (which use uint8 wire IDs per
	// docs/interaction-model.md §7.2) into name-keyed maps that YAML
	// conformance assertions can target by spec field name.
	commandResponseFieldNames = map[uint8]map[uint8]map[uint8]string{}

	// commandParamNames[featureID][cmdID][paramName] = paramID.
	// Inverse of *ParamNameToID maps in pkg/features/*_gen.go, collected
	// here so the test harness can translate YAML-supplied named args
	// (`consumptionLimit: 7000000`) into the integer-keyed CBOR map the
	// wire requires (`{1: 7000000}`) before encoding the InvokePayload.
	commandParamNames = map[uint8]map[uint8]map[string]uint8{}
)

func init() {
	endpointNames = generatedEndpointNames()
	featureNames = generatedFeatureNames()
	initGeneratedNameTables()

	// Aliases not present in YAML -- used by test specs and legacy references.
	attributeNames[uint8(model.FeatureDeviceInfo)]["endpointList"] = features.DeviceInfoAttrEndpoints
	attributeNames[uint8(model.FeatureSignals)]["schedule"] = features.SignalsAttrPriceSlots
	// getTestState is added manually in DeviceService (not generated).
	commandNames[uint8(model.FeatureTestControl)]["getTestState"] = features.TestControlCmdGetTestState

	// getTestState's enableKey parameter mapping. Without this, the runner's
	// invoke_as_zone -> ConvertCommandParams sees no schema for cmd=3 and
	// passes the YAML's string-keyed args through unchanged; the device's
	// validateParameters then rejects with InvalidParameter before the
	// requireTestZone check can return NotAuthorized. Bug surfaced by
	// TC-TESTCTRL-ZONE-001 (expected NOT_AUTHORIZED, got INVALID_PARAMETER).
	tcGetTestState, ok := commandParamNames[uint8(model.FeatureTestControl)]
	if !ok {
		tcGetTestState = map[uint8]map[string]uint8{}
		commandParamNames[uint8(model.FeatureTestControl)] = tcGetTestState
	}
	tcGetTestState[features.TestControlCmdGetTestState] = map[string]uint8{
		"enableKey": features.GetTestStateParamEnableKey,
	}
}

// ResolveEndpointName resolves an endpoint name to its ID (case-insensitive).
func ResolveEndpointName(name string) (uint8, bool) {
	lname := strings.ToLower(name)
	for k, v := range endpointNames {
		if strings.ToLower(k) == lname {
			return v, true
		}
	}
	return 0, false
}

// ResolveFeatureName resolves a feature name to its ID (case-insensitive).
func ResolveFeatureName(name string) (uint8, bool) {
	lname := strings.ToLower(name)
	for k, v := range featureNames {
		if strings.ToLower(k) == lname {
			return v, true
		}
	}
	return 0, false
}

// ResolveAttributeName resolves an attribute name to its ID for a given feature (case-insensitive).
func ResolveAttributeName(featureID uint8, name string) (uint16, bool) {
	if attrNames, ok := attributeNames[featureID]; ok {
		lname := strings.ToLower(name)
		for k, v := range attrNames {
			if strings.ToLower(k) == lname {
				return v, true
			}
		}
	}
	return 0, false
}

// GetEndpointName returns the name for an endpoint type.
func GetEndpointName(id uint8) string {
	epType := model.EndpointType(id)
	return epType.String()
}

// GetFeatureName returns the name for a feature type.
func GetFeatureName(id uint8) string {
	featType := model.FeatureType(id)
	return featType.String()
}

// GetAttributeName returns the name for an attribute ID within a feature.
func GetAttributeName(featureID uint8, attrID uint16) string {
	if attrNames, ok := attributeNames[featureID]; ok {
		for name, id := range attrNames {
			if id == attrID {
				return name
			}
		}
	}
	// Return numeric if not found
	return ""
}

// ResolveCommandName resolves a command name to its ID for a given feature (case-insensitive).
func ResolveCommandName(featureID uint8, name string) (uint8, bool) {
	if cmdNames, ok := commandNames[featureID]; ok {
		lname := strings.ToLower(name)
		for k, v := range cmdNames {
			if strings.ToLower(k) == lname {
				return v, true
			}
		}
	}
	return 0, false
}

// GetCommandName returns the name for a command ID within a feature.
func GetCommandName(featureID uint8, cmdID uint8) string {
	if cmdNames, ok := commandNames[featureID]; ok {
		for name, id := range cmdNames {
			if id == cmdID {
				return name
			}
		}
	}
	return ""
}

// ResponseFieldName returns the spec-defined name for a command response
// field, or "" if the field is unknown.
func ResponseFieldName(featureID, cmdID, fieldID uint8) string {
	if cmds, ok := commandResponseFieldNames[featureID]; ok {
		if fields, ok := cmds[cmdID]; ok {
			return fields[fieldID]
		}
	}
	return ""
}

// RemapResponsePayload converts a CBOR-decoded invoke response payload
// (typically map[any]any keyed by uint8 wire IDs per
// docs/interaction-model.md §7.2) into a map[string]any keyed by the
// spec field names. Unknown fields fall back to a stringified key so no
// information is lost. Returns nil for non-map payloads.
func RemapResponsePayload(featureID, cmdID uint8, payload any) map[string]any {
	if payload == nil {
		return nil
	}
	switch m := payload.(type) {
	case map[string]any:
		// Already string-keyed; return a shallow copy so callers may modify.
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			id, ok := toResponseFieldID(k)
			var name string
			if ok {
				name = ResponseFieldName(featureID, cmdID, id)
			}
			if name != "" {
				out[name] = v
			} else {
				out[fmt.Sprintf("%v", k)] = v
			}
		}
		return out
	}
	return nil
}

// CommandParamID returns the wire ID for a command parameter name, or
// 0/false if not registered.
func CommandParamID(featureID, cmdID uint8, paramName string) (uint8, bool) {
	if cmds, ok := commandParamNames[featureID]; ok {
		if params, ok := cmds[cmdID]; ok {
			id, found := params[paramName]
			return id, found
		}
	}
	return 0, false
}

// ConvertCommandParams translates a name-keyed argument map into the
// integer-keyed map[uint8]any that the wire spec requires. Unknown
// names are dropped (and logged via the optional logger callback) so
// that callers cannot accidentally smuggle string keys onto the wire.
//
// Returns the original value unchanged when:
//   - args is nil
//   - args is already a map[uint8]any (caller did the conversion already)
//   - args is not a map (e.g. an array or scalar passthrough)
//
// Unknown command schemas (no entry in the registry) cause args to be
// returned unchanged so legacy/unregistered commands still work.
func ConvertCommandParams(featureID, cmdID uint8, args any) any {
	if args == nil {
		return nil
	}
	stringArgs, ok := args.(map[string]any)
	if !ok {
		return args
	}
	cmds, hasFeature := commandParamNames[featureID]
	if !hasFeature {
		return args
	}
	params, hasCmd := cmds[cmdID]
	if !hasCmd {
		return args
	}
	out := make(map[uint8]any, len(stringArgs))
	for name, v := range stringArgs {
		if id, ok := params[name]; ok {
			out[id] = v
		}
	}
	return out
}

func toResponseFieldID(k any) (uint8, bool) {
	switch v := k.(type) {
	case uint8:
		return v, true
	case int:
		if v >= 0 && v <= 255 {
			return uint8(v), true
		}
	case int64:
		if v >= 0 && v <= 255 {
			return uint8(v), true
		}
	case uint64:
		if v <= 255 {
			return uint8(v), true
		}
	case uint:
		if v <= 255 {
			return uint8(v), true
		}
	}
	return 0, false
}
