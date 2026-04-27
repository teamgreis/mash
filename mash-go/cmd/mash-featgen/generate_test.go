package main

import (
	"strings"
	"testing"

	"github.com/mash-protocol/mash-go/internal/specparse"
)

func statusDef() *specparse.RawFeatureDef {
	return &specparse.RawFeatureDef{
		Name:     "Status",
		ID:       0x02,
		Revision: 1,
		Enums: []specparse.RawEnumDef{
			{
				Name: "OperatingState",
				Type: "uint8",
				Values: []specparse.RawEnumValue{
					{Name: "UNKNOWN", Value: 0x00, Description: "State not known"},
					{Name: "OFFLINE", Value: 0x01},
					{Name: "STANDBY", Value: 0x02},
					{Name: "STARTING", Value: 0x03},
					{Name: "RUNNING", Value: 0x04},
					{Name: "PAUSED", Value: 0x05},
					{Name: "SHUTTING_DOWN", Value: 0x06},
					{Name: "FAULT", Value: 0x07},
					{Name: "MAINTENANCE", Value: 0x08},
				},
			},
		},
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "operatingState", Type: "uint8", Enum: "OperatingState", Access: "readOnly", Mandatory: true, Default: "UNKNOWN", Description: "Current operating state"},
			{ID: 2, Name: "stateDetail", Type: "uint32", Access: "readOnly", Nullable: true, Description: "Vendor-specific state detail code"},
			{ID: 3, Name: "faultCode", Type: "uint32", Access: "readOnly", Nullable: true, Description: "Fault/error code when state=FAULT"},
			{ID: 4, Name: "faultMessage", Type: "string", Access: "readOnly", Nullable: true, Description: "Human-readable fault description"},
		},
	}
}

func TestGenerateAttributeConstants(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "StatusAttrOperatingState uint16 = 1")
	mustContain(t, output, "StatusAttrStateDetail uint16 = 2")
	mustContain(t, output, "StatusAttrFaultCode uint16 = 3")
	mustContain(t, output, "StatusAttrFaultMessage uint16 = 4")
}

func TestGenerateRevisionConstant(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "const StatusFeatureRevision uint16 = 1")
}

func TestGenerateEnumType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "type OperatingState uint8")
	mustContain(t, output, "OperatingStateUnknown OperatingState = 0x00")
	mustContain(t, output, "OperatingStateMaintenance OperatingState = 0x08")
	mustContain(t, output, "OperatingStateShuttingDown OperatingState = 0x06")

	// String method
	mustContain(t, output, "func (v OperatingState) String() string")
	mustContain(t, output, `return "UNKNOWN"`)
	mustContain(t, output, `return "MAINTENANCE"`)
	mustContain(t, output, `return "SHUTTING_DOWN"`)
}

func TestGenerateSharedEnumType(t *testing.T) {
	shared := &specparse.RawSharedTypes{
		Version: "1.0",
		Enums: []specparse.RawEnumDef{
			{
				Name: "Phase",
				Type: "uint8",
				Values: []specparse.RawEnumValue{
					{Name: "A", Value: 0x00},
					{Name: "B", Value: 0x01},
					{Name: "C", Value: 0x02},
				},
			},
		},
	}

	output, err := GenerateSharedEnums(shared)
	if err != nil {
		t.Fatalf("GenerateSharedEnums failed: %v", err)
	}

	mustContain(t, output, "type Phase uint8")
	mustContain(t, output, "PhaseA Phase = 0x00")
	mustContain(t, output, "PhaseB Phase = 0x01")
	mustContain(t, output, "PhaseC Phase = 0x02")

	// String method
	mustContain(t, output, "func (v Phase) String() string")
	mustContain(t, output, `return "A"`)
}

func TestGenerateConstructor(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "type Status struct {")
	mustContain(t, output, "*model.Feature")
	mustContain(t, output, "func NewStatus() *Status {")
	mustContain(t, output, "model.NewFeature(model.FeatureStatus, StatusFeatureRevision)")

	// Attribute additions
	mustContain(t, output, "ID: StatusAttrOperatingState,")
	mustContain(t, output, `Name: "operatingState",`)
	mustContain(t, output, "Type: model.DataTypeUint8,")
	mustContain(t, output, "Access: model.AccessReadOnly,")

	// Default using enum constant
	mustContain(t, output, "Default: uint8(OperatingStateUnknown),")

	// Nullable attribute
	mustContain(t, output, "ID: StatusAttrStateDetail,")
	mustContain(t, output, "Nullable: true,")
}

func TestGenerateConstructor_EnumDefault(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "Default: uint8(OperatingStateUnknown),")
}

func TestGenerateGetter_EnumType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Non-nullable enum getter
	mustContain(t, output, "func (s *Status) OperatingState() OperatingState {")
	mustContain(t, output, "s.ReadAttribute(StatusAttrOperatingState)")
	mustContain(t, output, "return OperatingState(v)")
	mustContain(t, output, "return OperatingStateUnknown")
}

func TestGenerateGetter_NullableType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// stateDetail: (uint32, bool) getter
	mustContain(t, output, "func (s *Status) StateDetail() (uint32, bool) {")
	mustContain(t, output, "s.ReadAttribute(StatusAttrStateDetail)")
	mustContain(t, output, "return 0, false")
}

func TestGenerateGetter_StringType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// faultMessage: nullable string returns string
	mustContain(t, output, "func (s *Status) FaultMessage() (string, bool) {")
}

func TestGenerateSetter_EnumType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (s *Status) SetOperatingState(operatingState OperatingState) error {")
	mustContain(t, output, "return attr.SetValueInternal(uint8(operatingState))")
}

func TestGenerateSetter_NullableType(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// SetStateDetail + ClearStateDetail
	mustContain(t, output, "func (s *Status) SetStateDetail(stateDetail uint32) error {")
	mustContain(t, output, "func (s *Status) ClearStateDetail() error {")
	mustContain(t, output, "return attr.SetValueInternal(nil)")
}

func TestGenerateGetter_SimpleNumericType(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "Electrical",
		ID:       0x03,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "phaseCount", Type: "uint8", Access: "readOnly", Default: 1, Description: "Number of phases"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (e *Electrical) PhaseCount() uint8 {")
	mustContain(t, output, "return uint8(1)")
}

func TestGenerateGetter_MapType(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "Measurement",
		ID:       0x04,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 10, Name: "acActivePowerPerPhase", Type: "map", MapKeyType: "Phase", MapValueType: "int64", Access: "readOnly", Nullable: true, Description: "Active power per phase"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (m *Measurement) ACActivePowerPerPhase() (map[Phase]int64, bool) {")
	mustContain(t, output, "return nil, false")
}

func TestGenerateSetter_MapType(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "Electrical",
		ID:       0x03,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 2, Name: "phaseMapping", Type: "map", MapKeyType: "Phase", MapValueType: "GridPhase", Access: "readOnly", Description: "Device phase to grid phase mapping"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (e *Electrical) SetPhaseMapping(phaseMapping map[Phase]GridPhase) error {")
}

func TestGenerateCommandConstants(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 1, Name: "setLimit", Mandatory: true, Description: "Set power limits"},
			{ID: 2, Name: "clearLimit", Mandatory: true, Description: "Remove limits"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "EnergyControlCmdSetLimit uint8 = 1")
	mustContain(t, output, "EnergyControlCmdClearLimit uint8 = 2")
}

func TestGenerateCommandStructs(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{
				ID:   1,
				Name: "setLimit",
				Parameters: []specparse.RawParameterDef{
					{Name: "consumptionLimit", Type: "int64", Required: false},
					{Name: "cause", Type: "uint8", Enum: "LimitCause", Required: true},
				},
				Response: []specparse.RawParameterDef{
					{Name: "applied", Type: "bool", Required: true},
					{Name: "rejectReason", Type: "uint8", Enum: "LimitRejectReason", Required: false},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Request struct
	mustContain(t, output, "type SetLimitRequest struct {")
	mustContain(t, output, "ConsumptionLimit *int64")
	mustContain(t, output, "Cause LimitCause")

	// Response struct
	mustContain(t, output, "type SetLimitResponse struct {")
	mustContain(t, output, "Applied bool")
	mustContain(t, output, "RejectReason *LimitRejectReason")
}

func TestGenerateCallbackSetter(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 1, Name: "setLimit", Description: "Set power limits",
				Response: []specparse.RawParameterDef{
					{Name: "applied", Type: "bool", Required: true},
					{Name: "controlState", Type: "uint8", Enum: "ControlState", Required: true},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Handler field in struct uses typed signature (no params, complex response)
	mustContain(t, output, "onSetLimit func(ctx context.Context) (SetLimitResponse, error)")

	// OnSetLimit setter
	mustContain(t, output, "func (e *EnergyControl) OnSetLimit(handler func(ctx context.Context) (SetLimitResponse, error)) {")
	mustContain(t, output, "e.onSetLimit = handler")
}

func TestGenerateBoolGetter(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 10, Name: "acceptsLimits", Type: "bool", Access: "readOnly", Default: false, Description: "Accepts SetLimit command"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (e *EnergyControl) AcceptsLimits() bool {")
	mustContain(t, output, "return false")
}

func TestGenerateHeader(t *testing.T) {
	def := statusDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "// Code generated by mash-featgen. DO NOT EDIT.")
	mustContain(t, output, "package features")
}

// --- Phase A: Go initialisms tests ---

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"acActivePower", []string{"ac", "Active", "Power"}},
		{"dcPower", []string{"dc", "Power"}},
		{"evStateOfCharge", []string{"ev", "State", "Of", "Charge"}},
		{"sessionId", []string{"session", "Id"}},
		{"deviceId", []string{"device", "Id"}},
		{"operatingState", []string{"operating", "State"}},
		{"isPausable", []string{"is", "Pausable"}},
		{"phaseMapping", []string{"phase", "Mapping"}},
		{"evIdentifications", []string{"ev", "Identifications"}},
		{"setLimit", []string{"set", "Limit"}},
		{"controlState", []string{"control", "State"}},
		{"zoneId", []string{"zone", "Id"}},
		{"evDemandMode", []string{"ev", "Demand", "Mode"}},
		{"", []string{}},
		{"ABC", []string{"A", "B", "C"}},
		{"ABCDef", []string{"A", "B", "C", "Def"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCamelCase(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, g, tt.want[i])
				}
			}
		})
	}
}

func TestGoTitleCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"acActivePower", "ACActivePower"},
		{"dcPower", "DCPower"},
		{"evStateOfCharge", "EVStateOfCharge"},
		{"sessionId", "SessionID"},
		{"deviceId", "DeviceID"},
		{"vendorId", "VendorID"},
		{"operatingState", "OperatingState"},
		{"isPausable", "IsPausable"},
		{"phaseMapping", "PhaseMapping"},
		{"evIdentifications", "EVIdentifications"},
		{"setLimit", "SetLimit"},
		{"controlState", "ControlState"},
		{"zoneId", "ZoneID"},
		{"productId", "ProductID"},
		{"evDemandMode", "EVDemandMode"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := goTitleCase(tt.input)
			if got != tt.want {
				t.Errorf("goTitleCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnumValueSuffix_Initialisms(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"UNKNOWN", "Unknown"},
		{"SHUTTING_DOWN", "ShuttingDown"},
		{"PV_SURPLUS_ONLY", "PVSurplusOnly"},
		{"PCID", "PCID"},
		{"MAC_EUI48", "MACEUI48"},
		{"MAC_EUI64", "MACEUI64"},
		{"RFID", "RFID"},
		{"VIN", "VIN"},
		{"CONTRACT_ID", "ContractID"},
		{"EVCC_ID", "EVCCID"},
		{"EVSE", "EVSE"},
		{"AB", "AB"},
		{"BC", "BC"},
		{"CA", "CA"},
		{"L1", "L1"},
		{"NOT_PLUGGED_IN", "NotPluggedIn"},
		{"CONSUMPTION", "Consumption"},
		{"BIDIRECTIONAL", "Bidirectional"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := enumValueSuffix(tt.input)
			if got != tt.want {
				t.Errorf("enumValueSuffix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateConstants_Initialisms(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "Measurement",
		ID:       0x04,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "acActivePower", Type: "int64", Access: "readOnly", Nullable: true, Description: "Active power"},
			{ID: 40, Name: "dcPower", Type: "int64", Access: "readOnly", Nullable: true, Description: "DC power"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "MeasurementAttrACActivePower uint16 = 1")
	mustContain(t, output, "MeasurementAttrDCPower uint16 = 40")
}

func TestGenerateGetters_Initialisms(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "DeviceInfo",
		ID:       0x06,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "deviceId", Type: "string", Access: "readOnly", Description: "Device identifier"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (d *DeviceInfo) DeviceID() string")
}

func TestGenerateSetters_Initialisms(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "DeviceInfo",
		ID:       0x06,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "deviceId", Type: "string", Access: "readOnly", Description: "Device identifier"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (d *DeviceInfo) SetDeviceID(")
}

func TestGenerateEnums_Initialisms(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "ChargingSession",
		ID:       0x07,
		Revision: 1,
		Enums: []specparse.RawEnumDef{
			{
				Name: "EVIDType",
				Type: "uint8",
				Values: []specparse.RawEnumValue{
					{Name: "PCID", Value: 0x00},
					{Name: "MAC_EUI48", Value: 0x01},
					{Name: "RFID", Value: 0x03},
					{Name: "VIN", Value: 0x04},
					{Name: "CONTRACT_ID", Value: 0x05},
					{Name: "EVCC_ID", Value: 0x06},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "EVIDTypePCID EVIDType = 0x00")
	mustContain(t, output, "EVIDTypeMACEUI48 EVIDType = 0x01")
	mustContain(t, output, "EVIDTypeRFID EVIDType = 0x03")
	mustContain(t, output, "EVIDTypeVIN EVIDType = 0x04")
	mustContain(t, output, "EVIDTypeContractID EVIDType = 0x05")
	mustContain(t, output, "EVIDTypeEVCCID EVIDType = 0x06")
}

func TestGenerateEnums_SharedInitialisms(t *testing.T) {
	shared := &specparse.RawSharedTypes{
		Version: "1.0",
		Enums: []specparse.RawEnumDef{
			{
				Name: "PhasePair",
				Type: "uint8",
				Values: []specparse.RawEnumValue{
					{Name: "AB", Value: 0x00},
					{Name: "BC", Value: 0x01},
					{Name: "CA", Value: 0x02},
				},
			},
		},
	}
	output, err := GenerateSharedEnums(shared)
	if err != nil {
		t.Fatalf("GenerateSharedEnums failed: %v", err)
	}

	mustContain(t, output, "PhasePairAB PhasePair = 0x00")
	mustContain(t, output, "PhasePairBC PhasePair = 0x01")
	mustContain(t, output, "PhasePairCA PhasePair = 0x02")
}

// --- Phase B: Typed command handler tests ---

func TestIsSimpleResponse(t *testing.T) {
	tests := []struct {
		name string
		cmd  specparse.RawCommandDef
		want bool
	}{
		{"no response", specparse.RawCommandDef{Name: "resume"}, true},
		{"success only", specparse.RawCommandDef{Name: "pause", Response: []specparse.RawParameterDef{
			{Name: "success", Type: "bool", Required: true},
		}}, true},
		{"complex response", specparse.RawCommandDef{Name: "setLimit", Response: []specparse.RawParameterDef{
			{Name: "applied", Type: "bool", Required: true},
			{Name: "controlState", Type: "uint8", Enum: "ControlState", Required: true},
		}}, false},
		{"non-success bool", specparse.RawCommandDef{Name: "removeZone", Response: []specparse.RawParameterDef{
			{Name: "removed", Type: "bool", Required: true},
		}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSimpleResponse(tt.cmd)
			if got != tt.want {
				t.Errorf("isSimpleResponse(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHasParameters(t *testing.T) {
	tests := []struct {
		name string
		cmd  specparse.RawCommandDef
		want bool
	}{
		{"with params", specparse.RawCommandDef{Name: "pause", Parameters: []specparse.RawParameterDef{
			{Name: "duration", Type: "uint32"},
		}}, true},
		{"no params", specparse.RawCommandDef{Name: "resume"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasParameters(tt.cmd)
			if got != tt.want {
				t.Errorf("hasParameters(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestTypedHandlerType(t *testing.T) {
	tests := []struct {
		name string
		cmd  specparse.RawCommandDef
		want string
	}{
		{
			"no params, no response",
			specparse.RawCommandDef{Name: "resume"},
			"func(ctx context.Context) error",
		},
		{
			"params, simple response",
			specparse.RawCommandDef{Name: "pause", Parameters: []specparse.RawParameterDef{
				{Name: "duration", Type: "uint32", Required: false},
			}},
			"func(ctx context.Context, req PauseRequest) error",
		},
		{
			"params, complex response",
			specparse.RawCommandDef{Name: "setLimit", Parameters: []specparse.RawParameterDef{
				{Name: "consumptionLimit", Type: "int64", Required: false},
			}, Response: []specparse.RawParameterDef{
				{Name: "applied", Type: "bool", Required: true},
				{Name: "controlState", Type: "uint8", Enum: "ControlState", Required: true},
			}},
			"func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error)",
		},
		{
			"params, complex response (removeZone)",
			specparse.RawCommandDef{Name: "removeZone", Parameters: []specparse.RawParameterDef{
				{Name: "zoneId", Type: "string", Required: true},
			}, Response: []specparse.RawParameterDef{
				{Name: "removed", Type: "bool", Required: true},
			}},
			"func(ctx context.Context, req RemoveZoneRequest) (RemoveZoneResponse, error)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := typedHandlerType(tt.cmd)
			if got != tt.want {
				t.Errorf("typedHandlerType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestGenerateHandler_SimpleNoParams(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 8, Name: "resume", Description: "Resume operation"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Nil check — Issue #11 fix: integer-keyed return shape, id 1 = success.
	mustContain(t, output, `if e.onResume == nil {`)
	mustContain(t, output, `return map[uint8]any{1: false}, nil`)
	// Call handler
	mustContain(t, output, `err := e.onResume(ctx)`)
	// Return — no YAML response declared, so id 1 is synthesised by convention.
	mustContain(t, output, `return map[uint8]any{1: err == nil}, err`)
}

func TestGenerateHandler_SimpleWithParams(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 7, Name: "pause", Description: "Pause operation",
				Parameters: []specparse.RawParameterDef{
					{Name: "duration", Type: "uint32", Required: false},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Nil check
	mustContain(t, output, `if e.onPause == nil {`)
	// Request parsing
	mustContain(t, output, `req := PauseRequest{}`)
	// Call handler
	mustContain(t, output, `err := e.onPause(ctx, req)`)
	// Return — Issue #11 fix: integer-keyed; no YAML response so id 1 by convention.
	mustContain(t, output, `return map[uint8]any{1: err == nil}, err`)
}

func TestGenerateHandler_ComplexResponse(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 1, Name: "setLimit", Description: "Set power limits",
				Parameters: []specparse.RawParameterDef{
					{ID: 1, Name: "consumptionLimit", Type: "int64", Required: false},
					{ID: 2, Name: "productionLimit", Type: "int64", Required: false},
					{ID: 3, Name: "duration", Type: "uint32", Required: false},
					{ID: 4, Name: "cause", Type: "uint8", Enum: "LimitCause", Required: true},
				},
				Response: []specparse.RawParameterDef{
					{ID: 1, Name: "applied", Type: "bool", Required: true},
					{ID: 2, Name: "controlState", Type: "uint8", Enum: "ControlState", Required: true},
					{ID: 3, Name: "effectiveConsumptionLimit", Type: "int64", Required: false},
					{ID: 4, Name: "effectiveProductionLimit", Type: "int64", Required: false},
					{ID: 5, Name: "rejectReason", Type: "uint8", Enum: "LimitRejectReason", Required: false},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Call handler returns (Response, error)
	mustContain(t, output, `resp, err := e.onSetLimit(ctx, req)`)
	mustContain(t, output, `if err != nil {`)
	// Response serialization — Issue #11 fix: integer-keyed via generated constants.
	mustContain(t, output, `result[SetLimitRespApplied] = resp.Applied`)
	mustContain(t, output, `result[SetLimitRespControlState] = uint8(resp.ControlState)`)
	// Optional fields
	mustContain(t, output, `if resp.EffectiveConsumptionLimit != nil {`)
	mustContain(t, output, `result[SetLimitRespEffectiveConsumptionLimit] = *resp.EffectiveConsumptionLimit`)
	// Optional enum
	mustContain(t, output, `if resp.RejectReason != nil {`)
	mustContain(t, output, `result[SetLimitRespRejectReason] = uint8(*resp.RejectReason)`)
}

func TestGenerateCallbackSetter_Typed(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 7, Name: "pause", Description: "Pause operation",
				Parameters: []specparse.RawParameterDef{
					{Name: "duration", Type: "uint32", Required: false},
				},
			},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// OnPause setter accepts typed handler
	mustContain(t, output, "func (e *EnergyControl) OnPause(handler func(ctx context.Context, req PauseRequest) error)")
}

func TestGenerateStruct_TypedFields(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 1, Name: "setLimit", Description: "Set power limits",
				Parameters: []specparse.RawParameterDef{
					{Name: "consumptionLimit", Type: "int64", Required: false},
				},
				Response: []specparse.RawParameterDef{
					{Name: "applied", Type: "bool", Required: true},
					{Name: "controlState", Type: "uint8", Enum: "ControlState", Required: true},
				},
			},
			{ID: 7, Name: "pause", Description: "Pause operation",
				Parameters: []specparse.RawParameterDef{
					{Name: "duration", Type: "uint32", Required: false},
				},
			},
			{ID: 8, Name: "resume", Description: "Resume operation"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Struct fields use typed signatures
	mustContain(t, output, "onSetLimit func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error)")
	mustContain(t, output, "onPause func(ctx context.Context, req PauseRequest) error")
	mustContain(t, output, "onResume func(ctx context.Context) error")
}

func TestGenerateNoResponseStruct_SimpleCommand(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Commands: []specparse.RawCommandDef{
			{ID: 8, Name: "resume", Description: "Resume operation"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// No Request or Response struct for parameterless simple commands
	mustNotContain(t, output, "type ResumeRequest struct")
	mustNotContain(t, output, "type ResumeResponse struct")
}

// --- Phase C: Ptr setter tests ---

func TestGenerateSetters_NullablePtr(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 20, Name: "effectiveConsumptionLimit", Type: "int64", Access: "readOnly", Nullable: true, Description: "Effective consumption limit"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// Direct setter
	mustContain(t, output, "func (e *EnergyControl) SetEffectiveConsumptionLimit(")
	// Clear
	mustContain(t, output, "func (e *EnergyControl) ClearEffectiveConsumptionLimit() error {")
	// Ptr convenience
	mustContain(t, output, "func (e *EnergyControl) SetEffectiveConsumptionLimitPtr(v *int64) error {")
	mustContain(t, output, "return e.ClearEffectiveConsumptionLimit()")
	mustContain(t, output, "return e.SetEffectiveConsumptionLimit(*v)")
}

func TestGenerateSetters_NonNullableNoPtr(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "Electrical",
		ID:       0x03,
		Revision: 1,
		Attributes: []specparse.RawAttributeDef{
			{ID: 1, Name: "phaseCount", Type: "uint8", Access: "readOnly", Default: 1, Description: "Number of phases"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// SetPhaseCount exists
	mustContain(t, output, "func (e *Electrical) SetPhaseCount(")
	// No Ptr method for non-nullable
	mustNotContain(t, output, "SetPhaseCountPtr")
}

func TestGenerateSetters_NullableEnumPtr(t *testing.T) {
	def := &specparse.RawFeatureDef{
		Name:     "EnergyControl",
		ID:       0x05,
		Revision: 1,
		Enums: []specparse.RawEnumDef{
			{Name: "OverrideReason", Type: "uint8", Values: []specparse.RawEnumValue{
				{Name: "SELF_PROTECTION", Value: 0x00},
			}},
		},
		Attributes: []specparse.RawAttributeDef{
			{ID: 75, Name: "overrideReason", Type: "uint8", Enum: "OverrideReason", Access: "readOnly", Nullable: true, Description: "Override reason"},
		},
	}
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (e *EnergyControl) SetOverrideReasonPtr(v *OverrideReason) error {")
}

// Helper

func mustContain(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("output does not contain %q\nOutput (first 3000 chars):\n%s", substr, truncate(output, 3000))
	}
}

func mustNotContain(t *testing.T, output, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("output should not contain %q", substr)
	}
}

// --- Phase D: Typed array support tests ---

func chargingSessionDef() *specparse.RawFeatureDef {
	return &specparse.RawFeatureDef{
		Name:     "ChargingSession",
		ID:       0x06,
		Revision: 1,
		Enums: []specparse.RawEnumDef{
			{Name: "EVIDType", Type: "uint8", Description: "Type of EV identification", Values: []specparse.RawEnumValue{
				{Name: "PCID", Value: 0x00},
				{Name: "VIN", Value: 0x04},
			}},
			{Name: "ChargingMode", Type: "uint8", Description: "Charging optimization strategy", Values: []specparse.RawEnumValue{
				{Name: "OFF", Value: 0x00},
				{Name: "PV_SURPLUS_ONLY", Value: 0x01},
			}},
		},
		Attributes: []specparse.RawAttributeDef{
			{
				ID: 20, Name: "evIdentifications", Type: "array", Access: "readOnly", Nullable: true,
				Description: "List of EV identifiers",
				Items: &specparse.RawArrayItemDef{
					Type:       "object",
					StructName: "EVIdentification",
					Fields: []specparse.RawArrayFieldDef{
						{Name: "type", Type: "uint8", Enum: "EVIDType"},
						{Name: "value", Type: "string"},
					},
				},
			},
			{
				ID: 71, Name: "supportedChargingModes", Type: "array", Access: "readOnly",
				Description: "Optimization modes EVSE supports",
				Items: &specparse.RawArrayItemDef{
					Type: "uint8",
					Enum: "ChargingMode",
				},
			},
			{
				ID: 99, Name: "endpoints", Type: "array", Access: "readOnly",
				Description: "Endpoint structure",
				// No Items field -- untyped array, should be skipped
			},
		},
	}
}

func TestGenerateArrayStruct_Object(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "type EVIdentification struct {")
	mustContain(t, output, "Type EVIDType")
	mustContain(t, output, "Value string")
}

func TestGenerateGetter_EnumArray(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (c *ChargingSession) SupportedChargingModes() ([]ChargingMode, bool) {")
	mustContain(t, output, "ChargingMode(")
}

func TestGenerateGetter_ObjectArray(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (c *ChargingSession) EVIdentifications() ([]EVIdentification, bool) {")
	mustContain(t, output, "map[string]any")
}

func TestGenerateSetter_EnumArray(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (c *ChargingSession) SetSupportedChargingModes(supportedChargingModes []ChargingMode) error {")
	mustContain(t, output, "uint8(")
}

func TestGenerateSetter_ObjectArray(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	mustContain(t, output, "func (c *ChargingSession) SetEVIdentifications(evIdentifications []EVIdentification) error {")
	mustContain(t, output, `"type": uint8(item.Type)`)
	mustContain(t, output, `"value": item.Value`)
}

func TestGenerateSetter_NullableArrayClear(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// evIdentifications is nullable array -- should generate Clear method
	mustContain(t, output, "func (c *ChargingSession) ClearEVIdentifications() error {")

	// supportedChargingModes is NOT nullable -- should NOT generate Clear
	mustNotContain(t, output, "ClearSupportedChargingModes")
}

func TestGenerateGetter_UntypedArraySkipped(t *testing.T) {
	def := chargingSessionDef()
	output, err := GenerateFeature(def, nil)
	if err != nil {
		t.Fatalf("GenerateFeature failed: %v", err)
	}

	// endpoints has no Items, so getter/setter should NOT be generated
	mustNotContain(t, output, "func (c *ChargingSession) Endpoints()")
	mustNotContain(t, output, "func (c *ChargingSession) SetEndpoints(")
}

// --- Phase E: Model type generation tests ---

func TestToEndpointGoName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"DEVICE_ROOT", "EndpointDeviceRoot"},
		{"GRID_CONNECTION", "EndpointGridConnection"},
		{"PV_STRING", "EndpointPVString"},
		{"EV_CHARGER", "EndpointEVCharger"},
		{"HVAC", "EndpointHVAC"},
		{"SUB_METER", "EndpointSubMeter"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toEndpointGoName(tt.input)
			if got != tt.want {
				t.Errorf("toEndpointGoName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateFeatureTypes(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "DeviceInfo", ID: 0x01, Description: "Device identity and structure"},
		{Name: "Status", ID: 0x02, Description: "Operating state and fault information"},
		{Name: "EnergyControl", ID: 0x05, Description: "Limits, setpoints, and control commands"},
	}

	output, err := GenerateFeatureTypes(types)
	if err != nil {
		t.Fatalf("GenerateFeatureTypes failed: %v", err)
	}

	// Header
	mustContain(t, output, "// Code generated by mash-featgen. DO NOT EDIT.")
	mustContain(t, output, "package model")

	// Type declaration
	mustContain(t, output, "type FeatureType uint8")

	// Constants with doc comments and hex values
	mustContain(t, output, "FeatureDeviceInfo FeatureType = 0x01")
	mustContain(t, output, "FeatureStatus FeatureType = 0x02")
	mustContain(t, output, "FeatureEnergyControl FeatureType = 0x05")

	// Doc comments from descriptions
	mustContain(t, output, "// FeatureDeviceInfo: device identity and structure.")

	// String() method
	mustContain(t, output, "func (f FeatureType) String() string")
	mustContain(t, output, `return "DeviceInfo"`)
	mustContain(t, output, `return "Status"`)
	mustContain(t, output, `return "EnergyControl"`)
}

func TestGenerateFeatureTypes_EmptyInput(t *testing.T) {
	output, err := GenerateFeatureTypes(nil)
	if err != nil {
		t.Fatalf("GenerateFeatureTypes failed: %v", err)
	}

	// Should still have header and type but no const block
	mustContain(t, output, "package model")
	mustContain(t, output, "type FeatureType uint8")
	mustNotContain(t, output, "const (")
}

func TestGenerateEndpointTypes(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "DEVICE_ROOT", ID: 0x00, Description: "Device-level metadata (always endpoint 0)"},
		{Name: "EV_CHARGER", ID: 0x05, Description: "EVSE / wallbox"},
		{Name: "HVAC", ID: 0x08, Description: "HVAC system"},
	}

	output, err := GenerateEndpointTypes(types)
	if err != nil {
		t.Fatalf("GenerateEndpointTypes failed: %v", err)
	}

	// Header
	mustContain(t, output, "// Code generated by mash-featgen. DO NOT EDIT.")
	mustContain(t, output, "package model")

	// Type declaration
	mustContain(t, output, "type EndpointType uint8")

	// Constants
	mustContain(t, output, "EndpointDeviceRoot EndpointType = 0x00")
	mustContain(t, output, "EndpointEVCharger EndpointType = 0x05")

	// String() returns SCREAMING_SNAKE
	mustContain(t, output, "func (e EndpointType) String() string")
	mustContain(t, output, `return "DEVICE_ROOT"`)
	mustContain(t, output, `return "EV_CHARGER"`)
}

func TestGenerateEndpointTypes_HVAC(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "HVAC", ID: 0x08, Description: "HVAC system"},
	}

	output, err := GenerateEndpointTypes(types)
	if err != nil {
		t.Fatalf("GenerateEndpointTypes failed: %v", err)
	}

	// Must be EndpointHVAC, not EndpointHvac
	mustContain(t, output, "EndpointHVAC EndpointType = 0x08")
	mustNotContain(t, output, "EndpointHvac")
	mustContain(t, output, `return "HVAC"`)
}

func TestValidateFeatureIDs_Match(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "DeviceInfo", ID: 0x01},
		{Name: "Status", ID: 0x02},
	}
	defs := []*specparse.RawFeatureDef{
		{Name: "DeviceInfo", ID: 0x01},
		{Name: "Status", ID: 0x02},
	}

	err := ValidateFeatureIDs(types, defs)
	if err != nil {
		t.Errorf("ValidateFeatureIDs returned unexpected error: %v", err)
	}
}

func TestValidateFeatureIDs_IDMismatch(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "DeviceInfo", ID: 0x01},
		{Name: "Status", ID: 0x05},
	}
	defs := []*specparse.RawFeatureDef{
		{Name: "DeviceInfo", ID: 0x01},
		{Name: "Status", ID: 0x02},
	}

	err := ValidateFeatureIDs(types, defs)
	if err == nil {
		t.Fatal("expected error for ID mismatch")
	}
	if !strings.Contains(err.Error(), "Status") {
		t.Errorf("error should mention Status: %v", err)
	}
}

func TestValidateFeatureIDs_MissingEntry(t *testing.T) {
	types := []specparse.RawModelTypeDef{
		{Name: "DeviceInfo", ID: 0x01},
	}
	defs := []*specparse.RawFeatureDef{
		{Name: "DeviceInfo", ID: 0x01},
		{Name: "Status", ID: 0x02},
	}

	err := ValidateFeatureIDs(types, defs)
	if err == nil {
		t.Fatal("expected error for missing entry")
	}
	if !strings.Contains(err.Error(), "Status") {
		t.Errorf("error should mention Status: %v", err)
	}
}

func TestEnumValueSuffix_HVAC(t *testing.T) {
	got := enumValueSuffix("HVAC")
	if got != "HVAC" {
		t.Errorf("enumValueSuffix(%q) = %q, want %q", "HVAC", got, "HVAC")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
