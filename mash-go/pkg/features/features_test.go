package features

import (
	"context"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/mash-protocol/mash-go/pkg/model"
)

func TestDeviceInfo(t *testing.T) {
	di := NewDeviceInfo()

	t.Run("Type", func(t *testing.T) {
		if di.Type() != model.FeatureDeviceInfo {
			t.Errorf("expected type DeviceInfo, got %v", di.Type())
		}
	})

	t.Run("Revision", func(t *testing.T) {
		if di.Revision() != DeviceInfoFeatureRevision {
			t.Errorf("expected revision %d, got %d", DeviceInfoFeatureRevision, di.Revision())
		}
	})

	t.Run("SetDeviceID", func(t *testing.T) {
		err := di.SetDeviceID("PEN12345.EVSE-001")
		if err != nil {
			t.Fatalf("SetDeviceID failed: %v", err)
		}
		if di.DeviceID() != "PEN12345.EVSE-001" {
			t.Errorf("expected deviceId PEN12345.EVSE-001, got %s", di.DeviceID())
		}
	})

	t.Run("SetVendorInfo", func(t *testing.T) {
		_ = di.SetVendorName("Wallbox")
		_ = di.SetProductName("Pulsar Plus")
		_ = di.SetSerialNumber("WB-2024-001234")
		_ = di.SetVendorID(12345)
		_ = di.SetProductID(100)

		if di.VendorName() != "Wallbox" {
			t.Errorf("expected vendorName Wallbox, got %s", di.VendorName())
		}
		if di.ProductName() != "Pulsar Plus" {
			t.Errorf("expected productName Pulsar Plus, got %s", di.ProductName())
		}
		if di.SerialNumber() != "WB-2024-001234" {
			t.Errorf("expected serialNumber WB-2024-001234, got %s", di.SerialNumber())
		}
		if v, ok := di.VendorID(); !ok || v != 12345 {
			t.Errorf("expected vendorId 12345, got %d (ok=%v)", v, ok)
		}
		if v, ok := di.ProductID(); !ok || v != 100 {
			t.Errorf("expected productId 100, got %d (ok=%v)", v, ok)
		}
	})

	t.Run("SetSoftwareVersion", func(t *testing.T) {
		_ = di.SetSoftwareVersion("3.2.1")
		if di.SoftwareVersion() != "3.2.1" {
			t.Errorf("expected version 3.2.1, got %s", di.SoftwareVersion())
		}
	})

	t.Run("DefaultSpecVersion", func(t *testing.T) {
		if di.SpecVersion() != "1.0" {
			t.Errorf("expected specVersion 1.0, got %s", di.SpecVersion())
		}
	})

	t.Run("WritableAttributes", func(t *testing.T) {
		// Location and label should be writable by users
		err := di.SetLocation("Garage")
		if err != nil {
			t.Errorf("SetLocation failed: %v", err)
		}
		if loc, ok := di.Location(); !ok || loc != "Garage" {
			t.Errorf("expected location Garage, got %s (ok=%v)", loc, ok)
		}

		err = di.SetLabel("Main Charger")
		if err != nil {
			t.Errorf("SetLabel failed: %v", err)
		}
		if lbl, ok := di.Label(); !ok || lbl != "Main Charger" {
			t.Errorf("expected label Main Charger, got %s (ok=%v)", lbl, ok)
		}
	})

	t.Run("SetEndpoints", func(t *testing.T) {
		endpoints := []*model.EndpointInfo{
			{ID: 0, Type: model.EndpointDeviceRoot, Features: []uint16{0x0006}},
			{ID: 1, Type: model.EndpointEVCharger, Label: "Port 1", Features: []uint16{0x0001, 0x0002}},
		}
		err := di.SetEndpoints(endpoints)
		if err != nil {
			t.Fatalf("SetEndpoints failed: %v", err)
		}
	})

	t.Run("UseCasesAttribute", func(t *testing.T) {
		if DeviceInfoAttrUseCases != 21 {
			t.Errorf("expected useCases attribute ID 21, got %d", DeviceInfoAttrUseCases)
		}
	})

	t.Run("SetUseCases", func(t *testing.T) {
		ucs := []*model.UseCaseDecl{
			{EndpointID: 1, ID: 0x01, Major: 1, Minor: 0, Scenarios: 0x01},
			{EndpointID: 1, ID: 0x03, Major: 1, Minor: 0, Scenarios: 0x01},
		}
		err := di.SetUseCases(ucs)
		if err != nil {
			t.Fatalf("SetUseCases failed: %v", err)
		}

		got := di.UseCases()
		if len(got) != 2 {
			t.Fatalf("expected 2 use cases, got %d", len(got))
		}
		if got[0].ID != 0x01 {
			t.Errorf("expected first use case ID 0x01, got 0x%02X", got[0].ID)
		}
		if got[0].EndpointID != 1 {
			t.Errorf("expected endpointId 1, got %d", got[0].EndpointID)
		}
		if got[1].ID != 0x03 {
			t.Errorf("expected second use case ID 0x03, got 0x%02X", got[1].ID)
		}
	})

	t.Run("RemoveZoneCommandConstants", func(t *testing.T) {
		// Verify command ID is as specified in the protocol
		if DeviceInfoCmdRemoveZone != 0x10 {
			t.Errorf("expected RemoveZone command ID 0x10, got 0x%02x", DeviceInfoCmdRemoveZone)
		}
		// Verify parameter and response IDs (Issue #11 fix: integer keys
		// per docs/interaction-model.md §7.1).
		if RemoveZoneParamZoneID != 1 {
			t.Errorf("expected parameter ID 1, got %d", RemoveZoneParamZoneID)
		}
		if RemoveZoneRespRemoved != 1 {
			t.Errorf("expected response field ID 1, got %d", RemoveZoneRespRemoved)
		}
		// The codegen also emits a name-to-id map for harness use.
		if RemoveZoneParamNameToID["zoneId"] != RemoveZoneParamZoneID {
			t.Errorf("expected zoneId in RemoveZoneParamNameToID, got %v", RemoveZoneParamNameToID)
		}
	})
}

func TestElectrical(t *testing.T) {
	elec := NewElectrical()

	t.Run("Type", func(t *testing.T) {
		if elec.Type() != model.FeatureElectrical {
			t.Errorf("expected type Electrical, got %v", elec.Type())
		}
	})

	t.Run("DefaultValues", func(t *testing.T) {
		if elec.PhaseCount() != 1 {
			t.Errorf("expected default phaseCount 1, got %d", elec.PhaseCount())
		}
		if elec.NominalVoltage() != 230 {
			t.Errorf("expected default voltage 230, got %d", elec.NominalVoltage())
		}
		if elec.NominalFrequency() != 50 {
			t.Errorf("expected default frequency 50, got %d", elec.NominalFrequency())
		}
		if elec.SupportedDirections() != DirectionConsumption {
			t.Errorf("expected default direction CONSUMPTION, got %v", elec.SupportedDirections())
		}
	})

	t.Run("SetPhaseConfig", func(t *testing.T) {
		_ = elec.SetPhaseCount(3)
		_ = elec.SetPhaseMapping(map[Phase]GridPhase{
			PhaseA: GridPhaseL1,
			PhaseB: GridPhaseL2,
			PhaseC: GridPhaseL3,
		})

		if elec.PhaseCount() != 3 {
			t.Errorf("expected phaseCount 3, got %d", elec.PhaseCount())
		}

		mapping, mapOk := elec.PhaseMapping()
		if !mapOk || mapping == nil || len(mapping) != 3 {
			t.Error("expected 3-phase mapping")
		}
	})

	t.Run("SetPowerRatings", func(t *testing.T) {
		_ = elec.SetNominalMaxConsumption(22_000_000) // 22 kW
		_ = elec.SetNominalMaxProduction(0)
		_ = elec.SetNominalMinPower(1_400_000) // 1.4 kW

		if elec.NominalMaxConsumption() != 22_000_000 {
			t.Errorf("expected maxConsumption 22000000, got %d", elec.NominalMaxConsumption())
		}
		if elec.NominalMinPower() != 1_400_000 {
			t.Errorf("expected minPower 1400000, got %d", elec.NominalMinPower())
		}
	})

	t.Run("SetCurrentRatings", func(t *testing.T) {
		_ = elec.SetMaxCurrentPerPhase(32_000) // 32A
		_ = elec.SetMinCurrentPerPhase(6_000)  // 6A

		if elec.MaxCurrentPerPhase() != 32_000 {
			t.Errorf("expected maxCurrent 32000, got %d", elec.MaxCurrentPerPhase())
		}
		if elec.MinCurrentPerPhase() != 6_000 {
			t.Errorf("expected minCurrent 6000, got %d", elec.MinCurrentPerPhase())
		}
	})

	t.Run("BidirectionalSupport", func(t *testing.T) {
		_ = elec.SetSupportedDirections(DirectionBidirectional)
		_ = elec.SetSupportsAsymmetric(AsymmetricSupportConsumption)

		if !elec.IsBidirectional() {
			t.Error("expected bidirectional support")
		}
		if !elec.CanConsume() {
			t.Error("expected consumption capability")
		}
		if !elec.CanProduce() {
			t.Error("expected production capability")
		}
		if elec.SupportsAsymmetric() != AsymmetricSupportConsumption {
			t.Errorf("expected asymmetric CONSUMPTION, got %v", elec.SupportsAsymmetric())
		}
	})

	t.Run("SetEnergyCapacity", func(t *testing.T) {
		_ = elec.SetEnergyCapacity(10_000_000) // 10 kWh

		if elec.EnergyCapacity() != 10_000_000 {
			t.Errorf("expected capacity 10000000, got %d", elec.EnergyCapacity())
		}
	})
}

func TestMeasurement(t *testing.T) {
	meas := NewMeasurement()

	t.Run("Type", func(t *testing.T) {
		if meas.Type() != model.FeatureMeasurement {
			t.Errorf("expected type Measurement, got %v", meas.Type())
		}
	})

	t.Run("ACPower", func(t *testing.T) {
		_ = meas.SetACActivePower(11_040_000) // 11.04 kW consuming
		_ = meas.SetACReactivePower(200_000)
		_ = meas.SetACApparentPower(11_050_000)

		power, ok := meas.ACActivePower()
		if !ok || power != 11_040_000 {
			t.Errorf("expected activePower 11040000, got %d (ok=%v)", power, ok)
		}

		reactive, ok := meas.ACReactivePower()
		if !ok || reactive != 200_000 {
			t.Errorf("expected reactivePower 200000, got %d (ok=%v)", reactive, ok)
		}

		apparent, ok := meas.ACApparentPower()
		if !ok || apparent != 11_050_000 {
			t.Errorf("expected apparentPower 11050000, got %d (ok=%v)", apparent, ok)
		}
	})

	t.Run("ACPerPhase", func(t *testing.T) {
		currents := map[Phase]int64{
			PhaseA: 16_000,
			PhaseB: 16_000,
			PhaseC: 16_000,
		}
		_ = meas.SetACCurrentPerPhase(currents)

		voltages := map[Phase]uint32{
			PhaseA: 230_000,
			PhaseB: 231_000,
			PhaseC: 229_000,
		}
		_ = meas.SetACVoltagePerPhase(voltages)

		readCurrents, ok := meas.ACCurrentPerPhase()
		if !ok || len(readCurrents) != 3 {
			t.Errorf("expected 3 phase currents, got %d", len(readCurrents))
		}

		readVoltages, ok := meas.ACVoltagePerPhase()
		if !ok || len(readVoltages) != 3 {
			t.Errorf("expected 3 phase voltages, got %d", len(readVoltages))
		}
	})

	t.Run("ACFrequencyAndPowerFactor", func(t *testing.T) {
		_ = meas.SetACFrequency(50_020) // 50.02 Hz
		_ = meas.SetPowerFactor(998)    // 0.998

		freq, ok := meas.ACFrequency()
		if !ok || freq != 50_020 {
			t.Errorf("expected frequency 50020, got %d", freq)
		}

		pf, ok := meas.PowerFactor()
		if !ok || pf != 998 {
			t.Errorf("expected powerFactor 998, got %d", pf)
		}
	})

	t.Run("ACEnergy", func(t *testing.T) {
		_ = meas.SetACEnergyConsumed(2_500_000_000) // 2500 kWh
		_ = meas.SetACEnergyProduced(0)

		consumed, ok := meas.ACEnergyConsumed()
		if !ok || consumed != 2_500_000_000 {
			t.Errorf("expected consumed 2500000000, got %d", consumed)
		}
	})

	t.Run("DCMeasurements", func(t *testing.T) {
		_ = meas.SetDCPower(-3_200_000) // -3.2 kW producing
		_ = meas.SetDCCurrent(-8_000)   // -8A out
		_ = meas.SetDCVoltage(400_000)  // 400V

		power, ok := meas.DCPower()
		if !ok || power != -3_200_000 {
			t.Errorf("expected dcPower -3200000, got %d", power)
		}

		current, ok := meas.DCCurrent()
		if !ok || current != -8_000 {
			t.Errorf("expected dcCurrent -8000, got %d", current)
		}

		voltage, ok := meas.DCVoltage()
		if !ok || voltage != 400_000 {
			t.Errorf("expected dcVoltage 400000, got %d", voltage)
		}
	})

	t.Run("BatteryState", func(t *testing.T) {
		_ = meas.SetStateOfCharge(65)
		_ = meas.SetStateOfHealth(97)
		_ = meas.SetStateOfEnergy(6_500_000) // 6.5 kWh
		_ = meas.SetCycleCount(342)
		_ = meas.SetTemperature(2850) // 28.5C

		soc, ok := meas.StateOfCharge()
		if !ok || soc != 65 {
			t.Errorf("expected soc 65, got %d", soc)
		}

		soh, ok := meas.StateOfHealth()
		if !ok || soh != 97 {
			t.Errorf("expected soh 97, got %d", soh)
		}

		soe, ok := meas.StateOfEnergy()
		if !ok || soe != 6_500_000 {
			t.Errorf("expected soe 6500000, got %d", soe)
		}

		temp, ok := meas.Temperature()
		if !ok || temp != 2850 {
			t.Errorf("expected temp 2850, got %d", temp)
		}
	})

	t.Run("IsConsumingProducing", func(t *testing.T) {
		_ = meas.SetACActivePower(5_000_000) // 5kW consuming
		if !meas.IsConsuming() {
			t.Error("expected IsConsuming to be true")
		}
		if meas.IsProducing() {
			t.Error("expected IsProducing to be false")
		}

		_ = meas.SetACActivePower(-5_000_000) // -5kW producing
		if meas.IsConsuming() {
			t.Error("expected IsConsuming to be false")
		}
		if !meas.IsProducing() {
			t.Error("expected IsProducing to be true")
		}
	})

	t.Run("ActivePowerKW", func(t *testing.T) {
		_ = meas.SetACActivePower(11_040_000) // 11.04 kW
		kw, ok := meas.ActivePowerKW()
		if !ok {
			t.Error("expected ActivePowerKW to return ok")
		}
		if kw < 11.039 || kw > 11.041 {
			t.Errorf("expected ~11.04 kW, got %f", kw)
		}
	})
}

func TestEnums(t *testing.T) {
	t.Run("Direction", func(t *testing.T) {
		tests := []struct {
			d    Direction
			want string
		}{
			{DirectionConsumption, "CONSUMPTION"},
			{DirectionProduction, "PRODUCTION"},
			{DirectionBidirectional, "BIDIRECTIONAL"},
			{Direction(99), "UNKNOWN"},
		}
		for _, tt := range tests {
			if got := tt.d.String(); got != tt.want {
				t.Errorf("Direction(%d).String() = %s, want %s", tt.d, got, tt.want)
			}
		}
	})

	t.Run("AsymmetricSupport", func(t *testing.T) {
		tests := []struct {
			a    AsymmetricSupport
			want string
		}{
			{AsymmetricSupportNone, "NONE"},
			{AsymmetricSupportConsumption, "CONSUMPTION"},
			{AsymmetricSupportProduction, "PRODUCTION"},
			{AsymmetricSupportBidirectional, "BIDIRECTIONAL"},
		}
		for _, tt := range tests {
			if got := tt.a.String(); got != tt.want {
				t.Errorf("AsymmetricSupport(%d).String() = %s, want %s", tt.a, got, tt.want)
			}
		}
	})

	t.Run("Phase", func(t *testing.T) {
		tests := []struct {
			p    Phase
			want string
		}{
			{PhaseA, "A"},
			{PhaseB, "B"},
			{PhaseC, "C"},
		}
		for _, tt := range tests {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("Phase(%d).String() = %s, want %s", tt.p, got, tt.want)
			}
		}
	})

	t.Run("GridPhase", func(t *testing.T) {
		tests := []struct {
			g    GridPhase
			want string
		}{
			{GridPhaseL1, "L1"},
			{GridPhaseL2, "L2"},
			{GridPhaseL3, "L3"},
		}
		for _, tt := range tests {
			if got := tt.g.String(); got != tt.want {
				t.Errorf("GridPhase(%d).String() = %s, want %s", tt.g, got, tt.want)
			}
		}
	})

	t.Run("PhasePair", func(t *testing.T) {
		tests := []struct {
			p    PhasePair
			want string
		}{
			{PhasePairAB, "AB"},
			{PhasePairBC, "BC"},
			{PhasePairCA, "CA"},
		}
		for _, tt := range tests {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("PhasePair(%d).String() = %s, want %s", tt.p, got, tt.want)
			}
		}
	})
}

func TestFeatureIntegration(t *testing.T) {
	// Test using features with the device model
	device := model.NewDevice("test-device", 0x1234, 0x5678)

	// Add DeviceInfo to root endpoint
	deviceInfo := NewDeviceInfo()
	_ = deviceInfo.SetDeviceID("test-device")
	_ = deviceInfo.SetVendorName("Test Vendor")
	_ = deviceInfo.SetProductName("Test Product")
	_ = deviceInfo.SetSerialNumber("SN12345")
	_ = deviceInfo.SetSoftwareVersion("1.0.0")
	device.RootEndpoint().AddFeature(deviceInfo.Feature)

	// Create an EV charger endpoint with Electrical and Measurement
	charger := model.NewEndpoint(1, model.EndpointEVCharger, "Main Charger")

	electrical := NewElectrical()
	_ = electrical.SetPhaseCount(3)
	_ = electrical.SetNominalVoltage(400)
	_ = electrical.SetMaxCurrentPerPhase(32_000)
	_ = electrical.SetNominalMaxConsumption(22_000_000)
	_ = electrical.SetSupportedDirections(DirectionConsumption)
	charger.AddFeature(electrical.Feature)

	measurement := NewMeasurement()
	_ = measurement.SetACActivePower(11_000_000)
	_ = measurement.SetACCurrentPerPhase(map[Phase]int64{
		PhaseA: 16_000,
		PhaseB: 16_000,
		PhaseC: 16_000,
	})
	charger.AddFeature(measurement.Feature)

	_ = device.AddEndpoint(charger)

	// Verify device structure
	if device.EndpointCount() != 2 {
		t.Errorf("expected 2 endpoints, got %d", device.EndpointCount())
	}

	// Read attributes through device
	power, err := device.ReadAttribute(1, model.FeatureMeasurement, MeasurementAttrACActivePower)
	if err != nil {
		t.Fatalf("ReadAttribute failed: %v", err)
	}
	if power != int64(11_000_000) {
		t.Errorf("expected power 11000000, got %v", power)
	}

	phases, err := device.ReadAttribute(1, model.FeatureElectrical, ElectricalAttrPhaseCount)
	if err != nil {
		t.Fatalf("ReadAttribute failed: %v", err)
	}
	if phases != uint8(3) {
		t.Errorf("expected 3 phases, got %v", phases)
	}

	// Update endpoint info in DeviceInfo
	endpoints := make([]*model.EndpointInfo, 0)
	for _, ep := range device.Endpoints() {
		endpoints = append(endpoints, ep.Info())
	}
	_ = deviceInfo.SetEndpoints(endpoints)
}

func TestNullableAttributes(t *testing.T) {
	meas := NewMeasurement()

	// Without setting a value, nullable attributes should return not-ok
	_, ok := meas.ACActivePower()
	if ok {
		t.Error("expected ACActivePower to return not-ok when not set")
	}

	_, ok = meas.StateOfCharge()
	if ok {
		t.Error("expected StateOfCharge to return not-ok when not set")
	}

	// After setting, should return ok
	_ = meas.SetACActivePower(1000)
	_, ok = meas.ACActivePower()
	if !ok {
		t.Error("expected ACActivePower to return ok after setting")
	}
}

func TestStatus(t *testing.T) {
	status := NewStatus()

	t.Run("Type", func(t *testing.T) {
		if status.Type() != model.FeatureStatus {
			t.Errorf("expected type Status, got %v", status.Type())
		}
	})

	t.Run("DefaultState", func(t *testing.T) {
		if status.OperatingState() != OperatingStateUnknown {
			t.Errorf("expected default state UNKNOWN, got %v", status.OperatingState())
		}
	})

	t.Run("SetOperatingState", func(t *testing.T) {
		states := []struct {
			state OperatingState
			name  string
		}{
			{OperatingStateOffline, "OFFLINE"},
			{OperatingStateStandby, "STANDBY"},
			{OperatingStateStarting, "STARTING"},
			{OperatingStateRunning, "RUNNING"},
			{OperatingStatePaused, "PAUSED"},
			{OperatingStateShuttingDown, "SHUTTING_DOWN"},
			{OperatingStateFault, "FAULT"},
			{OperatingStateMaintenance, "MAINTENANCE"},
		}

		for _, tc := range states {
			err := status.SetOperatingState(tc.state)
			if err != nil {
				t.Fatalf("SetOperatingState(%s) failed: %v", tc.name, err)
			}
			if status.OperatingState() != tc.state {
				t.Errorf("expected state %s, got %v", tc.name, status.OperatingState())
			}
			if tc.state.String() != tc.name {
				t.Errorf("expected String() %s, got %s", tc.name, tc.state.String())
			}
		}
	})

	t.Run("SetFault", func(t *testing.T) {
		err := status.SetFault(1001, "Overcurrent detected")
		if err != nil {
			t.Fatalf("SetFault failed: %v", err)
		}

		if status.OperatingState() != OperatingStateFault {
			t.Error("expected FAULT state after SetFault")
		}

		code, ok := status.FaultCode()
		if !ok || code != 1001 {
			t.Errorf("expected fault code 1001, got %d (ok=%v)", code, ok)
		}

		msg, msgOk := status.FaultMessage()
		if !msgOk || msg != "Overcurrent detected" {
			t.Errorf("expected fault message 'Overcurrent detected', got '%s' (ok=%v)", msg, msgOk)
		}
	})

	t.Run("ClearFault", func(t *testing.T) {
		_ = status.SetFault(1001, "Test fault")
		err := status.ClearFault()
		if err != nil {
			t.Fatalf("ClearFault failed: %v", err)
		}

		// Fault attributes should be cleared
		_, ok := status.FaultCode()
		if ok {
			t.Error("expected fault code to be cleared")
		}

		_, msgOk := status.FaultMessage()
		if msgOk {
			t.Error("expected fault message to be cleared")
		}
	})

	t.Run("HelperMethods", func(t *testing.T) {
		_ = status.SetOperatingState(OperatingStateFault)
		if !status.IsFaulted() {
			t.Error("expected IsFaulted to be true")
		}

		_ = status.SetOperatingState(OperatingStateRunning)
		if !status.IsRunning() {
			t.Error("expected IsRunning to be true")
		}
		if !status.IsReady() {
			t.Error("expected IsReady to be true for RUNNING")
		}

		_ = status.SetOperatingState(OperatingStateStandby)
		if !status.IsReady() {
			t.Error("expected IsReady to be true for STANDBY")
		}

		_ = status.SetOperatingState(OperatingStateOffline)
		if !status.IsOffline() {
			t.Error("expected IsOffline to be true")
		}
	})

	t.Run("SetStateDetail", func(t *testing.T) {
		err := status.SetStateDetail(42)
		if err != nil {
			t.Fatalf("SetStateDetail failed: %v", err)
		}

		detail, ok := status.StateDetail()
		if !ok || detail != 42 {
			t.Errorf("expected state detail 42, got %d (ok=%v)", detail, ok)
		}

		err = status.ClearStateDetail()
		if err != nil {
			t.Fatalf("ClearStateDetail failed: %v", err)
		}

		_, ok = status.StateDetail()
		if ok {
			t.Error("expected state detail to be cleared")
		}
	})
}

func TestEnergyControl(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("Type", func(t *testing.T) {
		if ec.Type() != model.FeatureEnergyControl {
			t.Errorf("expected type EnergyControl, got %v", ec.Type())
		}
	})

	t.Run("DefaultValues", func(t *testing.T) {
		if ec.DeviceType() != DeviceTypeOther {
			t.Errorf("expected default device type OTHER, got %v", ec.DeviceType())
		}
		if ec.ControlState() != ControlStateAutonomous {
			t.Errorf("expected default control state AUTONOMOUS, got %v", ec.ControlState())
		}
		if ec.ProcessState() != ProcessStateNone {
			t.Errorf("expected default process state NONE, got %v", ec.ProcessState())
		}
	})

	t.Run("SetDeviceType", func(t *testing.T) {
		types := []struct {
			dt   DeviceType
			name string
		}{
			{DeviceTypeEVSE, "EVSE"},
			{DeviceTypeHeatPump, "HEAT_PUMP"},
			{DeviceTypeWaterHeater, "WATER_HEATER"},
			{DeviceTypeBattery, "BATTERY"},
			{DeviceTypeInverter, "INVERTER"},
			{DeviceTypeFlexibleLoad, "FLEXIBLE_LOAD"},
		}

		for _, tc := range types {
			err := ec.SetDeviceType(tc.dt)
			if err != nil {
				t.Fatalf("SetDeviceType(%s) failed: %v", tc.name, err)
			}
			if ec.DeviceType() != tc.dt {
				t.Errorf("expected type %s, got %v", tc.name, ec.DeviceType())
			}
			if tc.dt.String() != tc.name {
				t.Errorf("expected String() %s, got %s", tc.name, tc.dt.String())
			}
		}
	})

	t.Run("SetControlState", func(t *testing.T) {
		states := []struct {
			cs   ControlState
			name string
		}{
			{ControlStateAutonomous, "AUTONOMOUS"},
			{ControlStateControlled, "CONTROLLED"},
			{ControlStateLimited, "LIMITED"},
			{ControlStateFailsafe, "FAILSAFE"},
			{ControlStateOverride, "OVERRIDE"},
		}

		for _, tc := range states {
			err := ec.SetControlState(tc.cs)
			if err != nil {
				t.Fatalf("SetControlState(%s) failed: %v", tc.name, err)
			}
			if ec.ControlState() != tc.cs {
				t.Errorf("expected state %s, got %v", tc.name, ec.ControlState())
			}
		}
	})

	t.Run("SetProcessState", func(t *testing.T) {
		states := []struct {
			ps   ProcessState
			name string
		}{
			{ProcessStateNone, "NONE"},
			{ProcessStateAvailable, "AVAILABLE"},
			{ProcessStateScheduled, "SCHEDULED"},
			{ProcessStateRunning, "RUNNING"},
			{ProcessStatePaused, "PAUSED"},
			{ProcessStateCompleted, "COMPLETED"},
			{ProcessStateAborted, "ABORTED"},
		}

		for _, tc := range states {
			err := ec.SetProcessState(tc.ps)
			if err != nil {
				t.Fatalf("SetProcessState(%s) failed: %v", tc.name, err)
			}
			if ec.ProcessState() != tc.ps {
				t.Errorf("expected state %s, got %v", tc.name, ec.ProcessState())
			}
		}
	})

	t.Run("SetCapabilities", func(t *testing.T) {
		ec.SetCapabilities(true, true, false, false, true, false, true)

		if !ec.AcceptsLimits() {
			t.Error("expected acceptsLimits to be true")
		}
		if !ec.IsPausable() {
			t.Error("expected isPausable to be true")
		}
	})

	t.Run("SetEffectiveLimits", func(t *testing.T) {
		limit := int64(11_000_000) // 11 kW
		err := ec.SetEffectiveConsumptionLimitPtr(&limit)
		if err != nil {
			t.Fatalf("SetEffectiveConsumptionLimitPtr failed: %v", err)
		}

		readLimit, ok := ec.EffectiveConsumptionLimit()
		if !ok || readLimit != limit {
			t.Errorf("expected limit %d, got %d (ok=%v)", limit, readLimit, ok)
		}

		// Clear limit
		err = ec.SetEffectiveConsumptionLimitPtr(nil)
		if err != nil {
			t.Fatalf("SetEffectiveConsumptionLimit(nil) failed: %v", err)
		}

		_, ok = ec.EffectiveConsumptionLimit()
		if ok {
			t.Error("expected limit to be cleared")
		}
	})

	t.Run("HelperMethods", func(t *testing.T) {
		_ = ec.SetControlState(ControlStateLimited)
		if !ec.IsLimited() {
			t.Error("expected IsLimited to be true")
		}

		_ = ec.SetControlState(ControlStateFailsafe)
		if !ec.IsFailsafe() {
			t.Error("expected IsFailsafe to be true")
		}
	})
}

// TestEnergyControl_ProcessLifecycleOptIn locks in DEC-075: process lifecycle
// support (pause/resume/stop/adjustStartTime) is opt-in. A fresh EnergyControl
// exposes isPausable/isShiftable/isStoppable = false and processState = NONE.
// Pause/Resume/Stop invoked against a device that hasn't registered a handler
// are gracefully rejected (success=false) rather than panicking — leaving
// room for the dispatcher to surface wire.StatusUnsupported to the caller.
// TestAttributeRangeValidation enforces DEC-070: int/uint attributes declaring
// `min:` / `max:` in YAML MUST reject out-of-range writes at runtime. This
// locks in the three seed attributes annotated as part of §L#1:
//   - Electrical.nominalVoltage (85..600 V)
//   - Electrical.nominalFrequency (45..65 Hz)
//   - EnergyControl.failsafeDuration (7200..86400 s)
//
// The range-checking plumbing already existed in pkg/model/attribute.go
// (`checkRange`); DEC-070 is primarily a YAML-annotation convention so that
// the plumbing is actually populated per attribute.
func TestAttributeRangeValidation(t *testing.T) {
	t.Run("Electrical/nominalVoltage", func(t *testing.T) {
		el := NewElectrical()
		if err := el.SetNominalVoltage(230); err != nil {
			t.Fatalf("SetNominalVoltage(230) unexpected error: %v", err)
		}
		if err := el.SetNominalVoltage(50); err == nil {
			t.Error("SetNominalVoltage(50) must fail (below min=85, DEC-070)")
		}
		if err := el.SetNominalVoltage(800); err == nil {
			t.Error("SetNominalVoltage(800) must fail (above max=600, DEC-070)")
		}
	})

	t.Run("Electrical/nominalFrequency", func(t *testing.T) {
		el := NewElectrical()
		if err := el.SetNominalFrequency(50); err != nil {
			t.Fatalf("SetNominalFrequency(50) unexpected error: %v", err)
		}
		if err := el.SetNominalFrequency(40); err == nil {
			t.Error("SetNominalFrequency(40) must fail (below min=45, DEC-070)")
		}
		if err := el.SetNominalFrequency(70); err == nil {
			t.Error("SetNominalFrequency(70) must fail (above max=65, DEC-070)")
		}
	})

	t.Run("EnergyControl/failsafeDuration", func(t *testing.T) {
		ec := NewEnergyControl()
		if err := ec.SetFailsafeDuration(7200); err != nil {
			t.Fatalf("SetFailsafeDuration(7200) unexpected error: %v", err)
		}
		if err := ec.SetFailsafeDuration(3600); err == nil {
			t.Error("SetFailsafeDuration(3600) must fail (below min=7200, DEC-070)")
		}
		if err := ec.SetFailsafeDuration(100000); err == nil {
			t.Error("SetFailsafeDuration(100000) must fail (above max=86400, DEC-070)")
		}
	})

	t.Run("ChargingSession/evStateOfCharge", func(t *testing.T) {
		cs := NewChargingSession()
		if err := cs.SetEVStateOfCharge(50); err != nil {
			t.Fatalf("SetEVStateOfCharge(50) unexpected error: %v", err)
		}
		if err := cs.SetEVStateOfCharge(0); err != nil {
			t.Errorf("SetEVStateOfCharge(0) must succeed (boundary); got %v", err)
		}
		if err := cs.SetEVStateOfCharge(100); err != nil {
			t.Errorf("SetEVStateOfCharge(100) must succeed (boundary); got %v", err)
		}
		if err := cs.SetEVStateOfCharge(101); err == nil {
			t.Error("SetEVStateOfCharge(101) must fail (above max=100, DEC-070)")
		}
	})

	t.Run("Measurement/acFrequency", func(t *testing.T) {
		m := NewMeasurement()
		if err := m.SetACFrequency(50000); err != nil {
			t.Fatalf("SetACFrequency(50000) unexpected error: %v", err)
		}
		if err := m.SetACFrequency(30000); err == nil {
			t.Error("SetACFrequency(30000) must fail (below min=45000, DEC-070)")
		}
		if err := m.SetACFrequency(70000); err == nil {
			t.Error("SetACFrequency(70000) must fail (above max=65000, DEC-070)")
		}
	})
}

// TestAttributeList_FitsInSingleMessage enforces DEC-072: a feature's
// attributeList response MUST fit in a single transport frame (8 KB per
// DEC-069). The protocol deliberately has no chunking mechanism. This test
// encodes the attributeList of every built-in feature as CBOR and asserts
// the encoded size is well below the 8192-byte cap, leaving headroom for
// Response envelope fields (messageId, status, path). Features that grow
// past ~6 KB attributeList MUST either split across endpoints or drop
// attributes — they cannot be advertised whole.
func TestAttributeList_FitsInSingleMessage(t *testing.T) {
	const budgetBytes = 6144 // ~6 KB leaves 2 KB for Response envelope overhead

	type feat interface {
		AttributeList() []uint16
	}

	cases := []struct {
		name string
		f    feat
	}{
		{"DeviceInfo", NewDeviceInfo()},
		{"Status", NewStatus()},
		{"Electrical", NewElectrical()},
		{"Measurement", NewMeasurement()},
		{"EnergyControl", NewEnergyControl()},
		{"ChargingSession", NewChargingSession()},
		{"Signals", NewSignals()},
		{"Tariff", NewTariff()},
		{"Plan", NewPlan()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := tc.f.AttributeList()
			encoded, err := cbor.Marshal(ids)
			if err != nil {
				t.Fatalf("cbor.Marshal(%s attributeList): %v", tc.name, err)
			}
			if len(encoded) > budgetBytes {
				t.Errorf("%s attributeList encoded to %d bytes, exceeds %d-byte budget (DEC-072)",
					tc.name, len(encoded), budgetBytes)
			}
		})
	}
}

func TestEnergyControl_ProcessLifecycleOptIn(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("DefaultsSignalOptOut", func(t *testing.T) {
		if ec.IsPausable() {
			t.Error("isPausable default must be false (opt-in; DEC-075)")
		}
		if ec.IsShiftable() {
			t.Error("isShiftable default must be false (opt-in; DEC-075)")
		}
		if ec.IsStoppable() {
			t.Error("isStoppable default must be false (opt-in; DEC-075)")
		}
		if ec.ProcessState() != ProcessStateNone {
			t.Errorf("processState default must be NONE (opt-in; DEC-075), got %v", ec.ProcessState())
		}
	})

	t.Run("PauseWithoutHandlerIsNonPanicking", func(t *testing.T) {
		out, err := ec.handlePause(context.Background(), map[uint8]any{})
		if err != nil {
			t.Fatalf("handlePause on opt-out device must not error; got %v", err)
		}
		// Codegen returns {1: false} for nil-handler simple-response commands
		// (id 1 = success/applied by spec convention; see Issue #11).
		if ok, _ := out[1].(bool); ok {
			t.Errorf("handlePause on opt-out device must report success=false; got out=%v", out)
		}
	})

	t.Run("ResumeWithoutHandlerIsNonPanicking", func(t *testing.T) {
		out, err := ec.handleResume(context.Background(), map[uint8]any{})
		if err != nil {
			t.Fatalf("handleResume on opt-out device must not error; got %v", err)
		}
		if ok, _ := out[1].(bool); ok {
			t.Errorf("handleResume on opt-out device must report success=false; got out=%v", out)
		}
	})

	t.Run("StopWithoutHandlerIsNonPanicking", func(t *testing.T) {
		out, err := ec.handleStop(context.Background(), map[uint8]any{})
		if err != nil {
			t.Fatalf("handleStop on opt-out device must not error; got %v", err)
		}
		if ok, _ := out[1].(bool); ok {
			t.Errorf("handleStop on opt-out device must report success=false; got out=%v", out)
		}
	})
}

func TestChargingSession(t *testing.T) {
	cs := NewChargingSession()

	t.Run("Type", func(t *testing.T) {
		if cs.Type() != model.FeatureChargingSession {
			t.Errorf("expected type ChargingSession, got %v", cs.Type())
		}
	})

	t.Run("DefaultState", func(t *testing.T) {
		if cs.State() != ChargingStateNotPluggedIn {
			t.Errorf("expected default state NOT_PLUGGED_IN, got %v", cs.State())
		}
		if cs.EVDemandMode() != EVDemandModeNone {
			t.Errorf("expected default demand mode NONE, got %v", cs.EVDemandMode())
		}
		if cs.ChargingMode() != ChargingModeOff {
			t.Errorf("expected default charging mode OFF, got %v", cs.ChargingMode())
		}
	})

	t.Run("ChargingStateEnum", func(t *testing.T) {
		states := []struct {
			state ChargingState
			name  string
		}{
			{ChargingStateNotPluggedIn, "NOT_PLUGGED_IN"},
			{ChargingStatePluggedInNoDemand, "PLUGGED_IN_NO_DEMAND"},
			{ChargingStatePluggedInDemand, "PLUGGED_IN_DEMAND"},
			{ChargingStatePluggedInCharging, "PLUGGED_IN_CHARGING"},
			{ChargingStatePluggedInDischarging, "PLUGGED_IN_DISCHARGING"},
			{ChargingStateSessionComplete, "SESSION_COMPLETE"},
			{ChargingStateFault, "FAULT"},
		}

		for _, tc := range states {
			err := cs.SetState(tc.state)
			if err != nil {
				t.Fatalf("SetState(%s) failed: %v", tc.name, err)
			}
			if cs.State() != tc.state {
				t.Errorf("expected state %s, got %v", tc.name, cs.State())
			}
			if tc.state.String() != tc.name {
				t.Errorf("expected String() %s, got %s", tc.name, tc.state.String())
			}
		}
	})

	t.Run("EVDemandModeEnum", func(t *testing.T) {
		modes := []struct {
			mode EVDemandMode
			name string
		}{
			{EVDemandModeNone, "NONE"},
			{EVDemandModeSingleDemand, "SINGLE_DEMAND"},
			{EVDemandModeScheduled, "SCHEDULED"},
			{EVDemandModeDynamic, "DYNAMIC"},
			{EVDemandModeDynamicBidirectional, "DYNAMIC_BIDIRECTIONAL"},
		}

		for _, tc := range modes {
			if tc.mode.String() != tc.name {
				t.Errorf("expected EVDemandMode String() %s, got %s", tc.name, tc.mode.String())
			}
		}
	})

	t.Run("EVIDTypeEnum", func(t *testing.T) {
		types := []struct {
			idType EVIDType
			name   string
		}{
			{EVIDTypePCID, "PCID"},
			{EVIDTypeMACEUI48, "MAC_EUI48"},
			{EVIDTypeMACEUI64, "MAC_EUI64"},
			{EVIDTypeRFID, "RFID"},
			{EVIDTypeVIN, "VIN"},
			{EVIDTypeContractID, "CONTRACT_ID"},
			{EVIDTypeEVCCID, "EVCC_ID"},
			{EVIDTypeOther, "OTHER"},
		}

		for _, tc := range types {
			if tc.idType.String() != tc.name {
				t.Errorf("expected EVIDType String() %s, got %s", tc.name, tc.idType.String())
			}
		}
	})

	t.Run("ChargingModeEnum", func(t *testing.T) {
		modes := []struct {
			mode ChargingMode
			name string
		}{
			{ChargingModeOff, "OFF"},
			{ChargingModePVSurplusOnly, "PV_SURPLUS_ONLY"},
			{ChargingModePVSurplusThreshold, "PV_SURPLUS_THRESHOLD"},
			{ChargingModePriceOptimized, "PRICE_OPTIMIZED"},
			{ChargingModeScheduled, "SCHEDULED"},
		}

		for _, tc := range modes {
			if tc.mode.String() != tc.name {
				t.Errorf("expected ChargingMode String() %s, got %s", tc.name, tc.mode.String())
			}
		}
	})

	t.Run("StartEndSession", func(t *testing.T) {
		err := cs.StartSession(1706180400)
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		if cs.State() != ChargingStatePluggedInNoDemand {
			t.Errorf("expected state PLUGGED_IN_NO_DEMAND, got %v", cs.State())
		}
		if cs.SessionEnergyCharged() != 0 {
			t.Errorf("expected initial energy 0, got %d", cs.SessionEnergyCharged())
		}
		if !cs.IsPluggedIn() {
			t.Error("expected IsPluggedIn to be true")
		}

		err = cs.EndSession(1706190000)
		if err != nil {
			t.Fatalf("EndSession failed: %v", err)
		}

		if cs.State() != ChargingStateNotPluggedIn {
			t.Errorf("expected state NOT_PLUGGED_IN, got %v", cs.State())
		}
		if cs.IsPluggedIn() {
			t.Error("expected IsPluggedIn to be false after EndSession")
		}
	})

	t.Run("SessionEnergy", func(t *testing.T) {
		err := cs.SetSessionEnergyCharged(5_500_000) // 5.5 kWh
		if err != nil {
			t.Fatalf("SetSessionEnergyCharged failed: %v", err)
		}

		if cs.SessionEnergyCharged() != 5_500_000 {
			t.Errorf("expected energy 5500000, got %d", cs.SessionEnergyCharged())
		}

		err = cs.SetSessionEnergyDischarged(1_000_000) // 1 kWh V2G
		if err != nil {
			t.Fatalf("SetSessionEnergyDischarged failed: %v", err)
		}

		if cs.SessionEnergyDischarged() != 1_000_000 {
			t.Errorf("expected discharged 1000000, got %d", cs.SessionEnergyDischarged())
		}
	})

	t.Run("EVBatteryState", func(t *testing.T) {
		err := cs.SetEVStateOfCharge(72)
		if err != nil {
			t.Fatalf("SetEVStateOfCharge failed: %v", err)
		}

		soc, ok := cs.EVStateOfCharge()
		if !ok || soc != 72 {
			t.Errorf("expected SoC 72, got %d (ok=%v)", soc, ok)
		}

		err = cs.SetEVBatteryCapacity(82_000_000) // 82 kWh
		if err != nil {
			t.Fatalf("SetEVBatteryCapacity failed: %v", err)
		}

		cap, ok := cs.EVBatteryCapacity()
		if !ok || cap != 82_000_000 {
			t.Errorf("expected capacity 82000000, got %d (ok=%v)", cap, ok)
		}
	})

	t.Run("EVDemandMode", func(t *testing.T) {
		err := cs.SetEVDemandMode(EVDemandModeDynamicBidirectional)
		if err != nil {
			t.Fatalf("SetEVDemandMode failed: %v", err)
		}

		if cs.EVDemandMode() != EVDemandModeDynamicBidirectional {
			t.Errorf("expected DYNAMIC_BIDIRECTIONAL, got %v", cs.EVDemandMode())
		}
	})

	t.Run("EVEnergyRequests", func(t *testing.T) {
		min := int64(-26_240_000)   // Can discharge to 40%
		max := int64(22_960_000)    // To 100%
		target := int64(16_000_000) // To 80%

		err := cs.SetEVEnergyRequests(&min, &max, &target)
		if err != nil {
			t.Fatalf("SetEVEnergyRequests failed: %v", err)
		}

		readTarget, ok := cs.EVTargetEnergyRequest()
		if !ok || readTarget != target {
			t.Errorf("expected target %d, got %d (ok=%v)", target, readTarget, ok)
		}
	})

	t.Run("EVIdentifications", func(t *testing.T) {
		ids := []EVIdentification{
			{Type: EVIDTypePCID, Value: "PCID-VW-2024-ABC123"},
			{Type: EVIDTypeVIN, Value: "WVWZZZ3CZWE123456"},
		}

		err := cs.SetEVIdentifications(ids)
		if err != nil {
			t.Fatalf("SetEVIdentifications failed: %v", err)
		}
	})

	t.Run("ChargingMode", func(t *testing.T) {
		err := cs.SetSupportedChargingModes([]ChargingMode{
			ChargingModeOff,
			ChargingModePVSurplusOnly,
			ChargingModePVSurplusThreshold,
			ChargingModePriceOptimized,
		})
		if err != nil {
			t.Fatalf("SetSupportedChargingModes failed: %v", err)
		}

		if !cs.SupportsMode(ChargingModePVSurplusOnly) {
			t.Error("expected PV_SURPLUS_ONLY to be supported")
		}
		if cs.SupportsMode(ChargingModeScheduled) {
			t.Error("expected SCHEDULED to not be supported")
		}

		err = cs.SetChargingMode(ChargingModePVSurplusThreshold)
		if err != nil {
			t.Fatalf("SetChargingMode failed: %v", err)
		}

		if cs.ChargingMode() != ChargingModePVSurplusThreshold {
			t.Errorf("expected PV_SURPLUS_THRESHOLD, got %v", cs.ChargingMode())
		}

		err = cs.SetSurplusThreshold(1_400_000) // 1.4 kW
		if err != nil {
			t.Fatalf("SetSurplusThreshold failed: %v", err)
		}
	})

	t.Run("StartStopDelays", func(t *testing.T) {
		err := cs.SetStartDelay(60)
		if err != nil {
			t.Fatalf("SetStartDelay failed: %v", err)
		}

		err = cs.SetStopDelay(120)
		if err != nil {
			t.Fatalf("SetStopDelay failed: %v", err)
		}
	})

	t.Run("IsChargingDischarging", func(t *testing.T) {
		_ = cs.SetState(ChargingStatePluggedInCharging)
		if !cs.IsCharging() {
			t.Error("expected IsCharging to be true")
		}
		if cs.IsDischarging() {
			t.Error("expected IsDischarging to be false")
		}

		_ = cs.SetState(ChargingStatePluggedInDischarging)
		if cs.IsCharging() {
			t.Error("expected IsCharging to be false")
		}
		if !cs.IsDischarging() {
			t.Error("expected IsDischarging to be true")
		}
	})

	t.Run("CanDischarge", func(t *testing.T) {
		// Setup for V2G: minDischarge < 0, maxDischarge >= 0, target <= 0
		minDischarge := int64(-16_400_000)
		maxDischarge := int64(8_200_000)
		target := int64(-8_200_000) // Already above target
		_ = cs.SetEVEnergyRequests(&minDischarge, nil, &target)
		_ = cs.SetEVDischargeConstraints(&minDischarge, &maxDischarge, nil)

		if !cs.CanDischarge() {
			t.Error("expected CanDischarge to be true when target <= 0")
		}
	})
}

func TestEnergyControlEnums(t *testing.T) {
	t.Run("LimitCause", func(t *testing.T) {
		// Test that enum values are defined correctly
		if LimitCauseGridEmergency != 0 {
			t.Error("LimitCauseGridEmergency should be 0")
		}
		if LimitCauseLocalProtection != 2 {
			t.Error("LimitCauseLocalProtection should be 2")
		}
	})

	t.Run("SetpointCause", func(t *testing.T) {
		if SetpointCauseGridRequest != 0 {
			t.Error("SetpointCauseGridRequest should be 0")
		}
		if SetpointCauseSelfConsumption != 1 {
			t.Error("SetpointCauseSelfConsumption should be 1")
		}
	})

	t.Run("OptOutState", func(t *testing.T) {
		if OptOutStateNone != 0 {
			t.Error("OptOutStateNone should be 0")
		}
		if OptOutStateAll != 3 {
			t.Error("OptOutStateAll should be 3")
		}
	})
}

func TestOverrideReason(t *testing.T) {
	tests := []struct {
		reason OverrideReason
		value  uint8
		name   string
	}{
		{OverrideReasonSelfProtection, 0x00, "SELF_PROTECTION"},
		{OverrideReasonSafety, 0x01, "SAFETY"},
		{OverrideReasonLegalRequirement, 0x02, "LEGAL_REQUIREMENT"},
		{OverrideReasonUncontrolledLoad, 0x03, "UNCONTROLLED_LOAD"},
		{OverrideReasonUncontrolledProducer, 0x04, "UNCONTROLLED_PRODUCER"},
	}
	for _, tc := range tests {
		if uint8(tc.reason) != tc.value {
			t.Errorf("OverrideReason %s: expected value 0x%02x, got 0x%02x", tc.name, tc.value, uint8(tc.reason))
		}
		if tc.reason.String() != tc.name {
			t.Errorf("OverrideReason(%d).String() = %s, want %s", tc.reason, tc.reason.String(), tc.name)
		}
	}

	// Test unknown value
	unknown := OverrideReason(99)
	if unknown.String() != "UNKNOWN" {
		t.Errorf("OverrideReason(99).String() = %s, want UNKNOWN", unknown.String())
	}
}

func TestLimitRejectReason(t *testing.T) {
	tests := []struct {
		reason LimitRejectReason
		value  uint8
		name   string
	}{
		{LimitRejectReasonBelowMinimum, 0x00, "BELOW_MINIMUM"},
		{LimitRejectReasonAboveContractual, 0x01, "ABOVE_CONTRACTUAL"},
		{LimitRejectReasonInvalidValue, 0x02, "INVALID_VALUE"},
		{LimitRejectReasonDeviceOverride, 0x03, "DEVICE_OVERRIDE"},
		{LimitRejectReasonNotSupported, 0x04, "NOT_SUPPORTED"},
	}
	for _, tc := range tests {
		if uint8(tc.reason) != tc.value {
			t.Errorf("LimitRejectReason %s: expected value 0x%02x, got 0x%02x", tc.name, tc.value, uint8(tc.reason))
		}
		if tc.reason.String() != tc.name {
			t.Errorf("LimitRejectReason(%d).String() = %s, want %s", tc.reason, tc.reason.String(), tc.name)
		}
	}

	// Test unknown value
	unknown := LimitRejectReason(99)
	if unknown.String() != "UNKNOWN" {
		t.Errorf("LimitRejectReason(99).String() = %s, want UNKNOWN", unknown.String())
	}
}

func TestEnergyControlGPLConstants(t *testing.T) {
	// Test that new attribute IDs are in the expected range (73-76)
	t.Run("ContractualAttributes", func(t *testing.T) {
		if EnergyControlAttrContractualConsumptionMax != 73 {
			t.Errorf("EnergyControlAttrContractualConsumptionMax = %d, want 73", EnergyControlAttrContractualConsumptionMax)
		}
		if EnergyControlAttrContractualProductionMax != 74 {
			t.Errorf("EnergyControlAttrContractualProductionMax = %d, want 74", EnergyControlAttrContractualProductionMax)
		}
	})

	t.Run("OverrideAttributes", func(t *testing.T) {
		if EnergyControlAttrOverrideReason != 75 {
			t.Errorf("EnergyControlAttrOverrideReason = %d, want 75", EnergyControlAttrOverrideReason)
		}
		if EnergyControlAttrOverrideDirection != 76 {
			t.Errorf("EnergyControlAttrOverrideDirection = %d, want 76", EnergyControlAttrOverrideDirection)
		}
	})
}

func TestEnergyControlContractualAttributes(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("ContractualConsumptionMax", func(t *testing.T) {
		attr, err := ec.GetAttribute(EnergyControlAttrContractualConsumptionMax)
		if err != nil {
			t.Fatalf("contractualConsumptionMax attribute missing: %v", err)
		}
		if !attr.Metadata().Nullable {
			t.Error("contractualConsumptionMax should be nullable")
		}
		if attr.Metadata().Name != "contractualConsumptionMax" {
			t.Errorf("expected name 'contractualConsumptionMax', got '%s'", attr.Metadata().Name)
		}
	})

	t.Run("ContractualProductionMax", func(t *testing.T) {
		attr, err := ec.GetAttribute(EnergyControlAttrContractualProductionMax)
		if err != nil {
			t.Fatalf("contractualProductionMax attribute missing: %v", err)
		}
		if !attr.Metadata().Nullable {
			t.Error("contractualProductionMax should be nullable")
		}
	})

	t.Run("OverrideReason", func(t *testing.T) {
		attr, err := ec.GetAttribute(EnergyControlAttrOverrideReason)
		if err != nil {
			t.Fatalf("overrideReason attribute missing: %v", err)
		}
		if !attr.Metadata().Nullable {
			t.Error("overrideReason should be nullable")
		}
	})

	t.Run("OverrideDirection", func(t *testing.T) {
		attr, err := ec.GetAttribute(EnergyControlAttrOverrideDirection)
		if err != nil {
			t.Fatalf("overrideDirection attribute missing: %v", err)
		}
		if !attr.Metadata().Nullable {
			t.Error("overrideDirection should be nullable")
		}
	})
}

func TestEnergyControlSetContractualLimits(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("SetAndGetContractualConsumptionMax", func(t *testing.T) {
		limit := int64(43_000_000) // 43 kW
		err := ec.SetContractualConsumptionMaxPtr(&limit)
		if err != nil {
			t.Fatalf("SetContractualConsumptionMax failed: %v", err)
		}

		val, ok := ec.ContractualConsumptionMax()
		if !ok || val != limit {
			t.Errorf("expected %d, got %d (ok=%v)", limit, val, ok)
		}
	})

	t.Run("ClearContractualConsumptionMax", func(t *testing.T) {
		limit := int64(43_000_000)
		_ = ec.SetContractualConsumptionMaxPtr(&limit)

		err := ec.SetContractualConsumptionMaxPtr(nil)
		if err != nil {
			t.Fatalf("SetContractualConsumptionMax(nil) failed: %v", err)
		}

		_, ok := ec.ContractualConsumptionMax()
		if ok {
			t.Error("expected nil after clear")
		}
	})

	t.Run("SetAndGetContractualProductionMax", func(t *testing.T) {
		limit := int64(30_000_000) // 30 kW
		err := ec.SetContractualProductionMaxPtr(&limit)
		if err != nil {
			t.Fatalf("SetContractualProductionMax failed: %v", err)
		}

		val, ok := ec.ContractualProductionMax()
		if !ok || val != limit {
			t.Errorf("expected %d, got %d (ok=%v)", limit, val, ok)
		}
	})

	t.Run("ClearContractualProductionMax", func(t *testing.T) {
		limit := int64(30_000_000)
		_ = ec.SetContractualProductionMaxPtr(&limit)

		err := ec.SetContractualProductionMaxPtr(nil)
		if err != nil {
			t.Fatalf("SetContractualProductionMax(nil) failed: %v", err)
		}

		_, ok := ec.ContractualProductionMax()
		if ok {
			t.Error("expected nil after clear")
		}
	})
}

func TestEnergyControlSetOverrideReason(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("SetAndGetOverrideReason", func(t *testing.T) {
		reason := OverrideReasonSelfProtection
		err := ec.SetOverrideReasonPtr(&reason)
		if err != nil {
			t.Fatalf("SetOverrideReason failed: %v", err)
		}

		val, ok := ec.OverrideReason()
		if !ok || val != reason {
			t.Errorf("expected %v, got %v (ok=%v)", reason, val, ok)
		}
	})

	t.Run("ClearOverrideReason", func(t *testing.T) {
		reason := OverrideReasonSafety
		_ = ec.SetOverrideReasonPtr(&reason)

		err := ec.SetOverrideReasonPtr(nil)
		if err != nil {
			t.Fatalf("SetOverrideReason(nil) failed: %v", err)
		}

		_, ok := ec.OverrideReason()
		if ok {
			t.Error("expected nil after clear")
		}
	})

	t.Run("AllOverrideReasons", func(t *testing.T) {
		reasons := []OverrideReason{
			OverrideReasonSelfProtection,
			OverrideReasonSafety,
			OverrideReasonLegalRequirement,
			OverrideReasonUncontrolledLoad,
			OverrideReasonUncontrolledProducer,
		}
		for _, reason := range reasons {
			r := reason
			err := ec.SetOverrideReasonPtr(&r)
			if err != nil {
				t.Fatalf("SetOverrideReason(%v) failed: %v", reason, err)
			}
			val, ok := ec.OverrideReason()
			if !ok || val != reason {
				t.Errorf("expected %v, got %v", reason, val)
			}
		}
	})
}

func TestEnergyControlSetOverrideDirection(t *testing.T) {
	ec := NewEnergyControl()

	t.Run("SetAndGetOverrideDirection", func(t *testing.T) {
		dir := DirectionConsumption
		err := ec.SetOverrideDirectionPtr(&dir)
		if err != nil {
			t.Fatalf("SetOverrideDirection failed: %v", err)
		}

		val, ok := ec.OverrideDirection()
		if !ok || val != dir {
			t.Errorf("expected %v, got %v (ok=%v)", dir, val, ok)
		}
	})

	t.Run("ClearOverrideDirection", func(t *testing.T) {
		dir := DirectionProduction
		_ = ec.SetOverrideDirectionPtr(&dir)

		err := ec.SetOverrideDirectionPtr(nil)
		if err != nil {
			t.Fatalf("SetOverrideDirection(nil) failed: %v", err)
		}

		_, ok := ec.OverrideDirection()
		if ok {
			t.Error("expected nil after clear")
		}
	})
}

func TestSetLimitEnhancedResponse(t *testing.T) {
	ec := NewEnergyControl()
	ec.SetCapabilities(true, false, false, false, false, false, false)

	t.Run("EnhancedHandlerApplied", func(t *testing.T) {
		// Handler that returns enhanced response
		ec.OnSetLimit(func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error) {
			return SetLimitResponse{
				Applied:                   true,
				EffectiveConsumptionLimit: req.ConsumptionLimit,
				EffectiveProductionLimit:  nil,
				RejectReason:              nil,
				ControlState:              ControlStateLimited,
			}, nil
		})

		// Invoke via command — integer-keyed parameters per Issue #11.
		result, err := ec.InvokeCommand(context.Background(), EnergyControlCmdSetLimit, map[uint8]any{
			SetLimitParamConsumptionLimit: int64(7_000_000),
			SetLimitParamCause:            uint8(LimitCauseGridEmergency),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}

		// Check enhanced response fields — integer-keyed per Issue #11.
		if applied, ok := result[SetLimitRespApplied].(bool); !ok || !applied {
			t.Errorf("expected applied=true, got %v", result[SetLimitRespApplied])
		}
		if cs, ok := result[SetLimitRespControlState].(uint8); !ok || ControlState(cs) != ControlStateLimited {
			t.Errorf("expected controlState=LIMITED, got %v", result[SetLimitRespControlState])
		}
		if effLimit, ok := result[SetLimitRespEffectiveConsumptionLimit].(int64); !ok || effLimit != 7_000_000 {
			t.Errorf("expected effectiveConsumptionLimit=7000000, got %v", result[SetLimitRespEffectiveConsumptionLimit])
		}
	})

	t.Run("EnhancedHandlerWithProductionLimit", func(t *testing.T) {
		prodLimit := int64(5_000_000)
		ec.OnSetLimit(func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error) {
			return SetLimitResponse{
				Applied:                  true,
				EffectiveProductionLimit: &prodLimit,
				ControlState:             ControlStateLimited,
			}, nil
		})

		result, err := ec.InvokeCommand(context.Background(), EnergyControlCmdSetLimit, map[uint8]any{
			SetLimitParamProductionLimit: int64(5_000_000),
			SetLimitParamCause:           uint8(LimitCauseGridEmergency),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}

		if effLimit, ok := result[SetLimitRespEffectiveProductionLimit].(int64); !ok || effLimit != 5_000_000 {
			t.Errorf("expected effectiveProductionLimit=5000000, got %v", result[SetLimitRespEffectiveProductionLimit])
		}
	})
}

func TestSetLimitRejectsNegativeValue(t *testing.T) {
	ec := NewEnergyControl()
	ec.SetCapabilities(true, false, false, false, false, false, false)

	ec.OnSetLimit(func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error) {
		// Validate negative values
		if req.ConsumptionLimit != nil && *req.ConsumptionLimit < 0 {
			reason := LimitRejectReasonInvalidValue
			return SetLimitResponse{
				Applied:      false,
				RejectReason: &reason,
				ControlState: ControlStateControlled,
			}, nil
		}
		return SetLimitResponse{Applied: true, ControlState: ControlStateLimited}, nil
	})

	result, err := ec.InvokeCommand(context.Background(), EnergyControlCmdSetLimit, map[uint8]any{
		SetLimitParamConsumptionLimit: int64(-1000),
		SetLimitParamCause:            uint8(LimitCauseGridEmergency),
	})
	if err != nil {
		t.Fatalf("InvokeCommand failed: %v", err)
	}

	if applied, ok := result[SetLimitRespApplied].(bool); ok && applied {
		t.Error("expected applied=false for negative value")
	}
	if reason, ok := result[SetLimitRespRejectReason].(uint8); !ok || LimitRejectReason(reason) != LimitRejectReasonInvalidValue {
		t.Errorf("expected rejectReason=INVALID_VALUE, got %v", result[SetLimitRespRejectReason])
	}
	if cs, ok := result[SetLimitRespControlState].(uint8); !ok || ControlState(cs) != ControlStateControlled {
		t.Errorf("expected controlState=CONTROLLED, got %v", result[SetLimitRespControlState])
	}
}

func TestTariff(t *testing.T) {
	tariff := NewTariff()

	t.Run("Type", func(t *testing.T) {
		if tariff.Type() != model.FeatureTariff {
			t.Errorf("expected type Tariff, got %v", tariff.Type())
		}
	})

	t.Run("SetAndGet", func(t *testing.T) {
		_ = tariff.SetTariffID(42)
		_ = tariff.SetCurrency("EUR")
		_ = tariff.SetPriceUnit(PriceUnitPerKwh)
		_ = tariff.SetTariffDescription("Day/Night Tariff")

		if tariff.TariffID() != 42 {
			t.Errorf("expected tariffId 42, got %d", tariff.TariffID())
		}
		if tariff.Currency() != "EUR" {
			t.Errorf("expected currency EUR, got %s", tariff.Currency())
		}
		if tariff.PriceUnit() != PriceUnitPerKwh {
			t.Errorf("expected priceUnit PER_KWH, got %v", tariff.PriceUnit())
		}
		desc, ok := tariff.TariffDescription()
		if !ok || desc != "Day/Night Tariff" {
			t.Errorf("expected description 'Day/Night Tariff', got %q (ok=%v)", desc, ok)
		}
	})

	t.Run("OnSetTariff", func(t *testing.T) {
		var called bool
		tariff.OnSetTariff(func(ctx context.Context, req SetTariffRequest) error {
			called = true
			if req.TariffID != 100 {
				t.Errorf("expected tariffId 100, got %d", req.TariffID)
			}
			if req.Currency != "USD" {
				t.Errorf("expected currency USD, got %s", req.Currency)
			}
			return nil
		})

		_, err := tariff.InvokeCommand(context.Background(), TariffCmdSetTariff, map[uint8]any{
			SetTariffParamTariffID: uint32(100),
			SetTariffParamCurrency: "USD",
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if !called {
			t.Error("expected OnSetTariff handler to be called")
		}
	})
}

func TestSignals(t *testing.T) {
	sig := NewSignals()

	t.Run("Type", func(t *testing.T) {
		if sig.Type() != model.FeatureSignals {
			t.Errorf("expected type Signals, got %v", sig.Type())
		}
	})

	t.Run("SetAndGetSignalSource", func(t *testing.T) {
		_ = sig.SetSignalSource(SignalSourceEnergySupplier)
		src, ok := sig.SignalSource()
		if !ok || src != SignalSourceEnergySupplier {
			t.Errorf("expected ENERGY_SUPPLIER, got %v (ok=%v)", src, ok)
		}
	})

	t.Run("SetAndGetPriceSlots", func(t *testing.T) {
		slots := []PriceSlot{
			{Duration: 3600, Price: 2500, PriceLevel: 2, RenewablePercent: 45},
			{Duration: 3600, Price: 1800, PriceLevel: 1, RenewablePercent: 70},
		}
		_ = sig.SetPriceSlots(slots)
		read, ok := sig.PriceSlots()
		if !ok || len(read) != 2 {
			t.Fatalf("expected 2 price slots, got %d (ok=%v)", len(read), ok)
		}
		if read[0].Price != 2500 {
			t.Errorf("expected price 2500, got %d", read[0].Price)
		}
	})

	t.Run("SetAndGetConstraintSlots", func(t *testing.T) {
		slots := []ConstraintSlot{
			{Duration: 3600, ConsumptionMax: 11_000_000, ConsumptionMin: 0},
		}
		_ = sig.SetConstraintSlots(slots)
		read, ok := sig.ConstraintSlots()
		if !ok || len(read) != 1 {
			t.Fatalf("expected 1 constraint slot, got %d (ok=%v)", len(read), ok)
		}
		if read[0].ConsumptionMax != 11_000_000 {
			t.Errorf("expected consumptionMax 11000000, got %d", read[0].ConsumptionMax)
		}
	})

	t.Run("OnSendPriceSignal", func(t *testing.T) {
		var called bool
		sig.OnSendPriceSignal(func(ctx context.Context, req SendPriceSignalRequest) error {
			called = true
			if req.Source != SignalSourceGrid {
				t.Errorf("expected source GRID, got %v", req.Source)
			}
			return nil
		})

		_, err := sig.InvokeCommand(context.Background(), SignalsCmdSendPriceSignal, map[uint8]any{
			SendPriceSignalParamSource:    uint8(SignalSourceGrid),
			SendPriceSignalParamStartTime: uint64(1700000000),
			SendPriceSignalParamSlots:     []any{},
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if !called {
			t.Error("expected OnSendPriceSignal handler to be called")
		}
	})

	t.Run("OnClearSignals", func(t *testing.T) {
		var called bool
		sig.OnClearSignals(func(ctx context.Context, req ClearSignalsRequest) (ClearSignalsResponse, error) {
			called = true
			return ClearSignalsResponse{Cleared: 3}, nil
		})

		result, err := sig.InvokeCommand(context.Background(), SignalsCmdClearSignals, map[uint8]any{})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if !called {
			t.Error("expected OnClearSignals handler to be called")
		}
		if cleared, ok := result[ClearSignalsRespCleared].(uint8); !ok || cleared != 3 {
			t.Errorf("expected cleared=3, got %v", result[ClearSignalsRespCleared])
		}
	})

	t.Run("HasActivePriceSignal", func(t *testing.T) {
		sig2 := NewSignals()
		if sig2.HasActivePriceSignal() {
			t.Error("expected no active price signal initially")
		}

		_ = sig2.SetPriceSlots([]PriceSlot{{Duration: 3600, Price: 2500}})
		if !sig2.HasActivePriceSignal() {
			t.Error("expected active price signal after setting slots")
		}

		_ = sig2.ClearPriceSlots()
		if sig2.HasActivePriceSignal() {
			t.Error("expected no active price signal after clearing")
		}
	})

	t.Run("HasActiveConstraints", func(t *testing.T) {
		sig2 := NewSignals()
		if sig2.HasActiveConstraints() {
			t.Error("expected no active constraints initially")
		}

		_ = sig2.SetConstraintSlots([]ConstraintSlot{{Duration: 3600, ConsumptionMax: 11_000_000}})
		if !sig2.HasActiveConstraints() {
			t.Error("expected active constraints after setting slots")
		}

		_ = sig2.ClearConstraintSlots()
		if sig2.HasActiveConstraints() {
			t.Error("expected no active constraints after clearing")
		}
	})
}

func TestPlan(t *testing.T) {
	plan := NewPlan()

	t.Run("Type", func(t *testing.T) {
		if plan.Type() != model.FeaturePlan {
			t.Errorf("expected type Plan, got %v", plan.Type())
		}
	})

	t.Run("DefaultValues", func(t *testing.T) {
		if plan.PlanID() != 0 {
			t.Errorf("expected default planId 0, got %d", plan.PlanID())
		}
		if plan.Commitment() != CommitmentPreliminary {
			t.Errorf("expected default commitment PRELIMINARY, got %v", plan.Commitment())
		}
	})

	t.Run("SetAndGet", func(t *testing.T) {
		_ = plan.SetPlanID(42)
		_ = plan.SetPlanVersion(3)
		_ = plan.SetCommitment(CommitmentTentative)

		if plan.PlanID() != 42 {
			t.Errorf("expected planId 42, got %d", plan.PlanID())
		}
		if plan.PlanVersion() != 3 {
			t.Errorf("expected planVersion 3, got %d", plan.PlanVersion())
		}
		if plan.Commitment() != CommitmentTentative {
			t.Errorf("expected commitment TENTATIVE, got %v", plan.Commitment())
		}
	})

	t.Run("OnRequestPlan", func(t *testing.T) {
		var called bool
		plan.OnRequestPlan(func(ctx context.Context, req RequestPlanRequest) (RequestPlanResponse, error) {
			called = true
			return RequestPlanResponse{PlanID: 99}, nil
		})

		result, err := plan.InvokeCommand(context.Background(), PlanCmdRequestPlan, map[uint8]any{})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if !called {
			t.Error("expected OnRequestPlan handler to be called")
		}
		if planID, ok := result[RequestPlanRespPlanID].(uint32); !ok || planID != 99 {
			t.Errorf("expected planId=99, got %v", result[RequestPlanRespPlanID])
		}
	})

	t.Run("OnAcceptPlan", func(t *testing.T) {
		var called bool
		plan.OnAcceptPlan(func(ctx context.Context, req AcceptPlanRequest) (AcceptPlanResponse, error) {
			called = true
			if req.PlanID != 99 {
				t.Errorf("expected planId 99, got %d", req.PlanID)
			}
			return AcceptPlanResponse{NewCommitment: CommitmentCommitted}, nil
		})

		result, err := plan.InvokeCommand(context.Background(), PlanCmdAcceptPlan, map[uint8]any{
			AcceptPlanParamPlanID:      uint32(99),
			AcceptPlanParamPlanVersion: uint32(1),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if !called {
			t.Error("expected OnAcceptPlan handler to be called")
		}
		if nc, ok := result[AcceptPlanRespNewCommitment].(uint8); !ok || Commitment(nc) != CommitmentCommitted {
			t.Errorf("expected newCommitment=COMMITTED, got %v", result[AcceptPlanRespNewCommitment])
		}
	})

	t.Run("IsCommitted", func(t *testing.T) {
		p := NewPlan()
		if p.IsCommitted() {
			t.Error("expected not committed initially")
		}
		_ = p.SetCommitment(CommitmentCommitted)
		if !p.IsCommitted() {
			t.Error("expected IsCommitted after setting COMMITTED")
		}
		_ = p.SetCommitment(CommitmentExecuting)
		if !p.IsCommitted() {
			t.Error("expected IsCommitted for EXECUTING (committed or above)")
		}
	})

	t.Run("IsExecuting", func(t *testing.T) {
		p := NewPlan()
		if p.IsExecuting() {
			t.Error("expected not executing initially")
		}
		_ = p.SetCommitment(CommitmentExecuting)
		if !p.IsExecuting() {
			t.Error("expected IsExecuting after setting EXECUTING")
		}
		_ = p.SetCommitment(CommitmentCommitted)
		if p.IsExecuting() {
			t.Error("expected not executing for COMMITTED")
		}
	})

	t.Run("CommitmentEnum", func(t *testing.T) {
		tests := []struct {
			c    Commitment
			name string
		}{
			{CommitmentPreliminary, "PRELIMINARY"},
			{CommitmentTentative, "TENTATIVE"},
			{CommitmentCommitted, "COMMITTED"},
			{CommitmentExecuting, "EXECUTING"},
		}
		for _, tc := range tests {
			if tc.c.String() != tc.name {
				t.Errorf("Commitment(%d).String() = %s, want %s", tc.c, tc.c.String(), tc.name)
			}
		}
	})
}

func TestSetLimitRequestParsing(t *testing.T) {
	ec := NewEnergyControl()
	ec.SetCapabilities(true, false, false, false, false, false, false)

	var capturedReq SetLimitRequest
	ec.OnSetLimit(func(ctx context.Context, req SetLimitRequest) (SetLimitResponse, error) {
		capturedReq = req
		return SetLimitResponse{Applied: true, ControlState: ControlStateLimited}, nil
	})

	t.Run("AllFieldsParsed", func(t *testing.T) {
		_, err := ec.InvokeCommand(context.Background(), EnergyControlCmdSetLimit, map[uint8]any{
			SetLimitParamConsumptionLimit: int64(10_000_000),
			SetLimitParamProductionLimit:  int64(5_000_000),
			SetLimitParamDuration:         uint32(3600),
			SetLimitParamCause:            uint8(LimitCauseLocalProtection),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}

		if capturedReq.ConsumptionLimit == nil || *capturedReq.ConsumptionLimit != 10_000_000 {
			t.Errorf("expected consumptionLimit=10000000, got %v", capturedReq.ConsumptionLimit)
		}
		if capturedReq.ProductionLimit == nil || *capturedReq.ProductionLimit != 5_000_000 {
			t.Errorf("expected productionLimit=5000000, got %v", capturedReq.ProductionLimit)
		}
		if capturedReq.Duration == nil || *capturedReq.Duration != 3600 {
			t.Errorf("expected duration=3600, got %v", capturedReq.Duration)
		}
		if capturedReq.Cause != LimitCauseLocalProtection {
			t.Errorf("expected cause=LOCAL_PROTECTION, got %v", capturedReq.Cause)
		}
	})

	t.Run("OptionalFieldsNil", func(t *testing.T) {
		_, err := ec.InvokeCommand(context.Background(), EnergyControlCmdSetLimit, map[uint8]any{
			SetLimitParamCause: uint8(LimitCauseGridEmergency),
		})
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}

		if capturedReq.ConsumptionLimit != nil {
			t.Error("expected consumptionLimit to be nil")
		}
		if capturedReq.ProductionLimit != nil {
			t.Error("expected productionLimit to be nil")
		}
		if capturedReq.Duration != nil {
			t.Error("expected duration to be nil")
		}
	})
}

