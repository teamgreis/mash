package model

import (
	"context"
	"testing"
)

func TestAttributeBasics(t *testing.T) {
	meta := &AttributeMetadata{
		ID:       AttrIDPrimary,
		Name:     "testAttr",
		Type:     DataTypeInt32,
		Access:   AccessReadWrite,
		Default:  int32(42),
		MinValue: int32(0),
		MaxValue: int32(100),
	}

	attr := NewAttribute(meta)

	t.Run("ID", func(t *testing.T) {
		if attr.ID() != AttrIDPrimary {
			t.Errorf("expected ID %d, got %d", AttrIDPrimary, attr.ID())
		}
	})

	t.Run("Metadata", func(t *testing.T) {
		if attr.Metadata().Name != "testAttr" {
			t.Errorf("expected name testAttr, got %s", attr.Metadata().Name)
		}
	})

	t.Run("DefaultValue", func(t *testing.T) {
		if attr.Value() != int32(42) {
			t.Errorf("expected default value 42, got %v", attr.Value())
		}
	})

	t.Run("SetValue", func(t *testing.T) {
		err := attr.SetValue(int32(50))
		if err != nil {
			t.Fatalf("SetValue failed: %v", err)
		}
		if attr.Value() != int32(50) {
			t.Errorf("expected value 50, got %v", attr.Value())
		}
	})

	t.Run("DirtyFlag", func(t *testing.T) {
		attr.ClearDirty()
		if attr.IsDirty() {
			t.Error("expected dirty=false after ClearDirty")
		}

		_ = attr.SetValue(int32(60))
		if !attr.IsDirty() {
			t.Error("expected dirty=true after SetValue")
		}
	})
}

func TestAttributeReadOnly(t *testing.T) {
	meta := &AttributeMetadata{
		ID:     1,
		Name:   "readOnly",
		Type:   DataTypeString,
		Access: AccessReadOnly,
	}

	attr := NewAttribute(meta)

	err := attr.SetValue("test")
	if err != ErrAttributeNotWritable {
		t.Errorf("expected ErrAttributeNotWritable, got %v", err)
	}

	// SetValueInternal should work for read-only
	err = attr.SetValueInternal("internal")
	if err != nil {
		t.Fatalf("SetValueInternal failed: %v", err)
	}
	if attr.Value() != "internal" {
		t.Errorf("expected value 'internal', got %v", attr.Value())
	}
}

func TestAttributeNullable(t *testing.T) {
	notNullable := NewAttribute(&AttributeMetadata{
		ID:       1,
		Name:     "notNullable",
		Type:     DataTypeInt32,
		Access:   AccessReadWrite,
		Nullable: false,
	})

	nullable := NewAttribute(&AttributeMetadata{
		ID:       2,
		Name:     "nullable",
		Type:     DataTypeInt32,
		Access:   AccessReadWrite,
		Nullable: true,
	})

	t.Run("NotNullable", func(t *testing.T) {
		err := notNullable.SetValue(nil)
		if err != ErrAttributeNotNullable {
			t.Errorf("expected ErrAttributeNotNullable, got %v", err)
		}
	})

	t.Run("Nullable", func(t *testing.T) {
		err := nullable.SetValue(nil)
		if err != nil {
			t.Errorf("expected no error for nullable, got %v", err)
		}
	})
}

func TestAttributeRangeValidation(t *testing.T) {
	attr := NewAttribute(&AttributeMetadata{
		ID:       1,
		Name:     "ranged",
		Type:     DataTypeInt32,
		Access:   AccessReadWrite,
		MinValue: int32(10),
		MaxValue: int32(100),
	})

	tests := []struct {
		name    string
		value   int32
		wantErr bool
	}{
		{"in range", 50, false},
		{"at min", 10, false},
		{"at max", 100, false},
		{"below min", 5, true},
		{"above max", 150, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := attr.SetValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetValue(%d) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestAttributeTypeValidation(t *testing.T) {
	boolAttr := NewAttribute(&AttributeMetadata{
		ID:     1,
		Name:   "boolAttr",
		Type:   DataTypeBool,
		Access: AccessReadWrite,
	})

	stringAttr := NewAttribute(&AttributeMetadata{
		ID:     2,
		Name:   "stringAttr",
		Type:   DataTypeString,
		Access: AccessReadWrite,
	})

	t.Run("BoolAcceptsBool", func(t *testing.T) {
		err := boolAttr.SetValue(true)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("BoolRejectsString", func(t *testing.T) {
		err := boolAttr.SetValue("true")
		if err == nil {
			t.Error("expected error for wrong type")
		}
	})

	t.Run("StringAcceptsString", func(t *testing.T) {
		err := stringAttr.SetValue("hello")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("StringRejectsInt", func(t *testing.T) {
		err := stringAttr.SetValue(123)
		if err == nil {
			t.Error("expected error for wrong type")
		}
	})
}

func TestAccessFlags(t *testing.T) {
	tests := []struct {
		access       Access
		canRead      bool
		canWrite     bool
		canSubscribe bool
	}{
		{AccessRead, true, false, false},
		{AccessWrite, false, true, false},
		{AccessSubscribe, false, false, true},
		{AccessReadOnly, true, false, true},
		{AccessReadWrite, true, true, true},
	}

	for _, tt := range tests {
		if tt.access.CanRead() != tt.canRead {
			t.Errorf("Access(%d).CanRead() = %v, want %v", tt.access, tt.access.CanRead(), tt.canRead)
		}
		if tt.access.CanWrite() != tt.canWrite {
			t.Errorf("Access(%d).CanWrite() = %v, want %v", tt.access, tt.access.CanWrite(), tt.canWrite)
		}
		if tt.access.CanSubscribe() != tt.canSubscribe {
			t.Errorf("Access(%d).CanSubscribe() = %v, want %v", tt.access, tt.access.CanSubscribe(), tt.canSubscribe)
		}
	}
}

func TestCommand(t *testing.T) {
	handlerCalled := false
	var receivedParams map[uint8]any

	cmd := NewCommand(&CommandMetadata{
		ID:          CmdIDPrimary,
		Name:        "testCmd",
		Description: "A test command",
		Parameters: []ParameterMetadata{
			{ID: 1, Name: "value", Type: DataTypeInt32, Required: true},
		},
	}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		handlerCalled = true
		receivedParams = params
		return map[uint8]any{1: "ok"}, nil
	})

	t.Run("ID", func(t *testing.T) {
		if cmd.ID() != CmdIDPrimary {
			t.Errorf("expected ID %d, got %d", CmdIDPrimary, cmd.ID())
		}
	})

	t.Run("Metadata", func(t *testing.T) {
		if cmd.Metadata().Name != "testCmd" {
			t.Errorf("expected name testCmd, got %s", cmd.Metadata().Name)
		}
	})

	t.Run("InvokeSuccess", func(t *testing.T) {
		result, err := cmd.Invoke(context.Background(), map[uint8]any{1: 42})
		if err != nil {
			t.Fatalf("Invoke failed: %v", err)
		}
		if !handlerCalled {
			t.Error("handler was not called")
		}
		if receivedParams[1] != 42 {
			t.Errorf("expected value 42 at id 1, got %v", receivedParams[1])
		}
		if result[1] != "ok" {
			t.Errorf("expected result ok at id 1, got %v", result[1])
		}
	})

	t.Run("InvokeMissingRequired", func(t *testing.T) {
		_, err := cmd.Invoke(context.Background(), map[uint8]any{})
		if err != ErrInvalidParameters {
			t.Errorf("expected ErrInvalidParameters, got %v", err)
		}
	})
}

func TestFeature(t *testing.T) {
	feature := NewFeature(FeatureMeasurement, 1)

	t.Run("Type", func(t *testing.T) {
		if feature.Type() != FeatureMeasurement {
			t.Errorf("expected type Measurement, got %v", feature.Type())
		}
	})

	t.Run("Revision", func(t *testing.T) {
		if feature.Revision() != 1 {
			t.Errorf("expected revision 1, got %d", feature.Revision())
		}
	})

	t.Run("GlobalAttributes", func(t *testing.T) {
		// clusterRevision (0xFFFD) is no longer a wire attribute (DEC-050)
		_, err := feature.ReadAttribute(0xFFFD)
		if err != ErrAttributeNotFound {
			t.Errorf("expected ErrAttributeNotFound for 0xFFFD, got %v", err)
		}

		// Revision() internal getter should still work
		if feature.Revision() != 1 {
			t.Errorf("expected Revision() = 1, got %d", feature.Revision())
		}

		// AttributeList should be present
		attrList, err := feature.ReadAttribute(AttrIDAttributeList)
		if err != nil {
			t.Fatalf("failed to read attributeList: %v", err)
		}
		if attrList == nil {
			t.Error("attributeList should not be nil")
		}
	})

	t.Run("AddAttribute", func(t *testing.T) {
		attr := NewAttribute(&AttributeMetadata{
			ID:     AttrIDPrimary,
			Name:   "power",
			Type:   DataTypeInt32,
			Access: AccessReadOnly,
		})
		feature.AddAttribute(attr)

		readAttr, err := feature.GetAttribute(AttrIDPrimary)
		if err != nil {
			t.Fatalf("GetAttribute failed: %v", err)
		}
		if readAttr.Metadata().Name != "power" {
			t.Errorf("expected name power, got %s", readAttr.Metadata().Name)
		}
	})

	t.Run("RemoveAttribute", func(t *testing.T) {
		const id uint16 = 0x0099
		attr := NewAttribute(&AttributeMetadata{
			ID:     id,
			Name:   "scratch",
			Type:   DataTypeInt32,
			Access: AccessReadOnly,
		})
		feature.AddAttribute(attr)

		listed := false
		for _, listID := range feature.AttributeList() {
			if listID == id {
				listed = true
				break
			}
		}
		if !listed {
			t.Fatalf("expected attribute 0x%04x in attributeList before removal", id)
		}

		if err := feature.RemoveAttribute(id); err != nil {
			t.Fatalf("RemoveAttribute returned %v, want nil", err)
		}
		if _, err := feature.GetAttribute(id); err != ErrAttributeNotFound {
			t.Errorf("after RemoveAttribute, GetAttribute returned %v, want ErrAttributeNotFound", err)
		}
		for _, listID := range feature.AttributeList() {
			if listID == id {
				t.Errorf("attribute 0x%04x still present in attributeList after removal", id)
			}
		}

		if err := feature.RemoveAttribute(id); err != ErrAttributeNotFound {
			t.Errorf("second RemoveAttribute returned %v, want ErrAttributeNotFound", err)
		}
		if err := feature.RemoveAttribute(AttrIDFeatureMap); err != ErrFeatureReadOnly {
			t.Errorf("RemoveAttribute on global ID returned %v, want ErrFeatureReadOnly", err)
		}
	})

	t.Run("AddCommand", func(t *testing.T) {
		cmd := NewCommand(&CommandMetadata{
			ID:   CmdIDPrimary,
			Name: "reset",
		}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
			return nil, nil
		})
		feature.AddCommand(cmd)

		readCmd, err := feature.GetCommand(CmdIDPrimary)
		if err != nil {
			t.Fatalf("GetCommand failed: %v", err)
		}
		if readCmd.Metadata().Name != "reset" {
			t.Errorf("expected name reset, got %s", readCmd.Metadata().Name)
		}
	})

	t.Run("FeatureMap", func(t *testing.T) {
		feature.SetFeatureMap(uint32(FeatureMapCore | FeatureMapFlex))

		if feature.FeatureMap() != 0x0003 {
			t.Errorf("expected featureMap 0x0003, got 0x%04x", feature.FeatureMap())
		}

		// Should also update the featureMap attribute
		fmAttr, err := feature.ReadAttribute(AttrIDFeatureMap)
		if err != nil {
			t.Fatalf("failed to read featureMap attribute: %v", err)
		}
		if fmAttr != uint32(0x0003) {
			t.Errorf("expected featureMap attribute 0x0003, got %v", fmAttr)
		}
	})

	t.Run("WriteGlobalAttributeFails", func(t *testing.T) {
		err := feature.WriteAttribute(AttrIDFeatureMap, uint32(99))
		if err != ErrFeatureReadOnly {
			t.Errorf("expected ErrFeatureReadOnly, got %v", err)
		}
	})
}

func TestFeatureSubscriber(t *testing.T) {
	feature := NewFeature(FeatureStatus, 1)

	attr := NewAttribute(&AttributeMetadata{
		ID:     AttrIDPrimary,
		Name:   "state",
		Type:   DataTypeUint8,
		Access: AccessReadWrite,
	})
	feature.AddAttribute(attr)

	var notifiedFeature FeatureType
	var notifiedAttrID uint16
	var notifiedValue any

	sub := &testSubscriber{
		onChanged: func(ft FeatureType, attrID uint16, value any) {
			notifiedFeature = ft
			notifiedAttrID = attrID
			notifiedValue = value
		},
	}

	feature.Subscribe(sub)

	err := feature.WriteAttribute(AttrIDPrimary, uint8(5))
	if err != nil {
		t.Fatalf("WriteAttribute failed: %v", err)
	}

	if notifiedFeature != FeatureStatus {
		t.Errorf("expected feature Status, got %v", notifiedFeature)
	}
	if notifiedAttrID != AttrIDPrimary {
		t.Errorf("expected attrID %d, got %d", AttrIDPrimary, notifiedAttrID)
	}
	if notifiedValue != uint8(5) {
		t.Errorf("expected value 5, got %v", notifiedValue)
	}

	// Test unsubscribe
	feature.Unsubscribe(sub)
	notifiedAttrID = 0

	_ = feature.WriteAttribute(AttrIDPrimary, uint8(10))
	if notifiedAttrID != 0 {
		t.Error("subscriber should not be notified after unsubscribe")
	}
}

type testSubscriber struct {
	onChanged func(ft FeatureType, attrID uint16, value any)
}

func (s *testSubscriber) OnAttributeChanged(ft FeatureType, attrID uint16, value any) {
	if s.onChanged != nil {
		s.onChanged(ft, attrID, value)
	}
}

func TestEndpoint(t *testing.T) {
	endpoint := NewEndpoint(1, EndpointEVCharger, "Main Charger")

	t.Run("Properties", func(t *testing.T) {
		if endpoint.ID() != 1 {
			t.Errorf("expected ID 1, got %d", endpoint.ID())
		}
		if endpoint.Type() != EndpointEVCharger {
			t.Errorf("expected type EV_CHARGER, got %v", endpoint.Type())
		}
		if endpoint.Label() != "Main Charger" {
			t.Errorf("expected label 'Main Charger', got %s", endpoint.Label())
		}
	})

	t.Run("SetLabel", func(t *testing.T) {
		endpoint.SetLabel("Updated Label")
		if endpoint.Label() != "Updated Label" {
			t.Errorf("expected label 'Updated Label', got %s", endpoint.Label())
		}
	})

	t.Run("AddFeature", func(t *testing.T) {
		feature := NewFeature(FeatureChargingSession, 1)
		endpoint.AddFeature(feature)

		if !endpoint.HasFeature(FeatureChargingSession) {
			t.Error("expected HasFeature to return true")
		}

		readFeature, err := endpoint.GetFeature(FeatureChargingSession)
		if err != nil {
			t.Fatalf("GetFeature failed: %v", err)
		}
		if readFeature.Type() != FeatureChargingSession {
			t.Errorf("expected feature type ChargingSession, got %v", readFeature.Type())
		}
	})

	t.Run("GetFeatureNotFound", func(t *testing.T) {
		_, err := endpoint.GetFeature(FeatureTariff)
		if err != ErrFeatureNotFound {
			t.Errorf("expected ErrFeatureNotFound, got %v", err)
		}
	})

	t.Run("Features", func(t *testing.T) {
		features := endpoint.Features()
		if len(features) != 1 {
			t.Errorf("expected 1 feature, got %d", len(features))
		}
	})

	t.Run("FeatureTypes", func(t *testing.T) {
		types := endpoint.FeatureTypes()
		if len(types) != 1 {
			t.Errorf("expected 1 feature type, got %d", len(types))
		}
		if types[0] != FeatureChargingSession {
			t.Errorf("expected ChargingSession, got %v", types[0])
		}
	})

	t.Run("Info", func(t *testing.T) {
		info := endpoint.Info()
		if info.ID != 1 {
			t.Errorf("expected info ID 1, got %d", info.ID)
		}
		if info.Type != EndpointEVCharger {
			t.Errorf("expected info type EV_CHARGER, got %v", info.Type)
		}
	})
}

func TestDevice(t *testing.T) {
	device := NewDevice("test-device-123", 0x1234, 0x5678)

	t.Run("Properties", func(t *testing.T) {
		if device.DeviceID() != "test-device-123" {
			t.Errorf("expected deviceID test-device-123, got %s", device.DeviceID())
		}
		if device.VendorID() != 0x1234 {
			t.Errorf("expected vendorID 0x1234, got 0x%04x", device.VendorID())
		}
		if device.ProductID() != 0x5678 {
			t.Errorf("expected productID 0x5678, got 0x%04x", device.ProductID())
		}
	})

	t.Run("SerialNumber", func(t *testing.T) {
		device.SetSerialNumber("SN12345")
		if device.SerialNumber() != "SN12345" {
			t.Errorf("expected serial SN12345, got %s", device.SerialNumber())
		}
	})

	t.Run("FirmwareVersion", func(t *testing.T) {
		device.SetFirmwareVersion("1.2.3")
		if device.FirmwareVersion() != "1.2.3" {
			t.Errorf("expected firmware 1.2.3, got %s", device.FirmwareVersion())
		}
	})

	t.Run("RootEndpoint", func(t *testing.T) {
		root := device.RootEndpoint()
		if root == nil {
			t.Fatal("root endpoint should not be nil")
		}
		if root.ID() != 0 {
			t.Errorf("expected root endpoint ID 0, got %d", root.ID())
		}
		if root.Type() != EndpointDeviceRoot {
			t.Errorf("expected root endpoint type DEVICE_ROOT, got %v", root.Type())
		}
	})

	t.Run("AddEndpoint", func(t *testing.T) {
		charger := NewEndpoint(1, EndpointEVCharger, "Charger 1")
		err := device.AddEndpoint(charger)
		if err != nil {
			t.Fatalf("AddEndpoint failed: %v", err)
		}

		if device.EndpointCount() != 2 {
			t.Errorf("expected 2 endpoints, got %d", device.EndpointCount())
		}

		readEndpoint, err := device.GetEndpoint(1)
		if err != nil {
			t.Fatalf("GetEndpoint failed: %v", err)
		}
		if readEndpoint.Type() != EndpointEVCharger {
			t.Errorf("expected type EV_CHARGER, got %v", readEndpoint.Type())
		}
	})

	t.Run("DuplicateEndpoint", func(t *testing.T) {
		duplicate := NewEndpoint(1, EndpointInverter, "Duplicate")
		err := device.AddEndpoint(duplicate)
		if err != ErrDuplicateEndpoint {
			t.Errorf("expected ErrDuplicateEndpoint, got %v", err)
		}
	})

	t.Run("GetEndpointNotFound", func(t *testing.T) {
		_, err := device.GetEndpoint(99)
		if err != ErrEndpointNotFound {
			t.Errorf("expected ErrEndpointNotFound, got %v", err)
		}
	})

	t.Run("Endpoints", func(t *testing.T) {
		endpoints := device.Endpoints()
		if len(endpoints) != 2 {
			t.Errorf("expected 2 endpoints, got %d", len(endpoints))
		}
	})

	t.Run("FindEndpointsByType", func(t *testing.T) {
		chargers := device.FindEndpointsByType(EndpointEVCharger)
		if len(chargers) != 1 {
			t.Errorf("expected 1 EV_CHARGER, got %d", len(chargers))
		}
	})
}

func TestDeviceFeatureAccess(t *testing.T) {
	device := NewDevice("test", 1, 1)

	// Add an endpoint with a feature
	endpoint := NewEndpoint(1, EndpointInverter, "Inverter")
	feature := NewFeature(FeatureMeasurement, 1)

	// Add a writable attribute
	attr := NewAttribute(&AttributeMetadata{
		ID:     AttrIDPrimary,
		Name:   "power",
		Type:   DataTypeInt32,
		Access: AccessReadWrite,
	})
	feature.AddAttribute(attr)

	// Add a command
	cmd := NewCommand(&CommandMetadata{
		ID:   CmdIDPrimary,
		Name: "reset",
	}, func(ctx context.Context, params map[uint8]any) (map[uint8]any, error) {
		return map[uint8]any{1: "done"}, nil
	})
	feature.AddCommand(cmd)

	endpoint.AddFeature(feature)
	_ = device.AddEndpoint(endpoint)

	t.Run("GetFeature", func(t *testing.T) {
		f, err := device.GetFeature(1, FeatureMeasurement)
		if err != nil {
			t.Fatalf("GetFeature failed: %v", err)
		}
		if f.Type() != FeatureMeasurement {
			t.Errorf("expected Measurement, got %v", f.Type())
		}
	})

	t.Run("ReadAttribute", func(t *testing.T) {
		// Set a value first
		_ = feature.WriteAttribute(AttrIDPrimary, int32(1000))

		val, err := device.ReadAttribute(1, FeatureMeasurement, AttrIDPrimary)
		if err != nil {
			t.Fatalf("ReadAttribute failed: %v", err)
		}
		if val != int32(1000) {
			t.Errorf("expected 1000, got %v", val)
		}
	})

	t.Run("WriteAttribute", func(t *testing.T) {
		err := device.WriteAttribute(1, FeatureMeasurement, AttrIDPrimary, int32(2000))
		if err != nil {
			t.Fatalf("WriteAttribute failed: %v", err)
		}

		val, _ := device.ReadAttribute(1, FeatureMeasurement, AttrIDPrimary)
		if val != int32(2000) {
			t.Errorf("expected 2000, got %v", val)
		}
	})

	t.Run("InvokeCommand", func(t *testing.T) {
		result, err := device.InvokeCommand(context.Background(), 1, FeatureMeasurement, CmdIDPrimary, nil)
		if err != nil {
			t.Fatalf("InvokeCommand failed: %v", err)
		}
		if result[1] != "done" {
			t.Errorf("expected status done at id 1, got %v", result[1])
		}
	})

	t.Run("InvalidEndpoint", func(t *testing.T) {
		_, err := device.ReadAttribute(99, FeatureMeasurement, AttrIDPrimary)
		if err != ErrEndpointNotFound {
			t.Errorf("expected ErrEndpointNotFound, got %v", err)
		}
	})

	t.Run("InvalidFeature", func(t *testing.T) {
		_, err := device.ReadAttribute(1, FeatureTariff, AttrIDPrimary)
		if err != ErrFeatureNotFound {
			t.Errorf("expected ErrFeatureNotFound, got %v", err)
		}
	})
}

func TestDeviceInfo(t *testing.T) {
	device := NewDevice("device-123", 0x1234, 0x5678)
	device.SetSerialNumber("SN123")
	device.SetFirmwareVersion("2.0.0")

	// Add an endpoint with a feature
	endpoint := NewEndpoint(1, EndpointBattery, "Battery")
	feature := NewFeature(FeatureElectrical, 1)
	endpoint.AddFeature(feature)
	_ = device.AddEndpoint(endpoint)

	info := device.Info()

	if info.DeviceID != "device-123" {
		t.Errorf("expected deviceID device-123, got %s", info.DeviceID)
	}
	if info.VendorID != 0x1234 {
		t.Errorf("expected vendorID 0x1234, got 0x%04x", info.VendorID)
	}
	if info.ProductID != 0x5678 {
		t.Errorf("expected productID 0x5678, got 0x%04x", info.ProductID)
	}
	if info.SerialNumber != "SN123" {
		t.Errorf("expected serial SN123, got %s", info.SerialNumber)
	}
	if info.FirmwareVersion != "2.0.0" {
		t.Errorf("expected firmware 2.0.0, got %s", info.FirmwareVersion)
	}
	if len(info.Endpoints) != 2 {
		t.Errorf("expected 2 endpoints in info, got %d", len(info.Endpoints))
	}
}

func TestEndpointTypeString(t *testing.T) {
	tests := []struct {
		et   EndpointType
		want string
	}{
		{EndpointDeviceRoot, "DEVICE_ROOT"},
		{EndpointEVCharger, "EV_CHARGER"},
		{EndpointInverter, "INVERTER"},
		{EndpointBattery, "BATTERY"},
		{EndpointType(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.et.String(); got != tt.want {
			t.Errorf("EndpointType(%d).String() = %s, want %s", tt.et, got, tt.want)
		}
	}
}

func TestFeatureTypeString(t *testing.T) {
	tests := []struct {
		ft   FeatureType
		want string
	}{
		{FeatureElectrical, "Electrical"},
		{FeatureMeasurement, "Measurement"},
		{FeatureEnergyControl, "EnergyControl"},
		{FeatureStatus, "Status"},
		{FeatureDeviceInfo, "DeviceInfo"},
		{FeatureVendorBase, "Vendor"},
		{FeatureType(0xFF), "Vendor"},  // 0xFF is in vendor range (0x80-0xFF)
		{FeatureType(0x10), "Unknown"}, // 0x10 is undefined standard feature
	}

	for _, tt := range tests {
		if got := tt.ft.String(); got != tt.want {
			t.Errorf("FeatureType(%d).String() = %s, want %s", tt.ft, got, tt.want)
		}
	}
}

func TestDataTypeString(t *testing.T) {
	tests := []struct {
		dt   DataType
		want string
	}{
		{DataTypeBool, "bool"},
		{DataTypeInt32, "int32"},
		{DataTypeString, "string"},
		{DataType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.dt.String(); got != tt.want {
			t.Errorf("DataType(%d).String() = %s, want %s", tt.dt, got, tt.want)
		}
	}
}

func TestFeatureMapBitString(t *testing.T) {
	tests := []struct {
		b    FeatureMapBit
		want string
	}{
		{FeatureMapCore, "CORE"},
		{FeatureMapFlex, "FLEX"},
		{FeatureMapBattery, "BATTERY"},
		{FeatureMapEMob, "EMOB"},
		{FeatureMapBit(0x8000), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.b.String(); got != tt.want {
			t.Errorf("FeatureMapBit(%d).String() = %s, want %s", tt.b, got, tt.want)
		}
	}
}

func TestAttributeListSorted(t *testing.T) {
	feature := NewFeature(FeatureMeasurement, 1)

	// Add attributes in non-sequential order to ensure sorting is tested
	// The order of addition: 50, 10, 30, 1, 20, 40
	// Expected sorted order: 1, 10, 20, 30, 40, 50, plus global attrs at end
	for _, id := range []uint16{50, 10, 30, 1, 20, 40} {
		attr := NewAttribute(&AttributeMetadata{
			ID:     id,
			Name:   "attr",
			Type:   DataTypeInt32,
			Access: AccessReadOnly,
		})
		feature.AddAttribute(attr)
	}

	// Call AttributeList multiple times to detect non-deterministic ordering
	for i := 0; i < 10; i++ {
		ids := feature.AttributeList()

		// Verify the list is sorted
		for j := 1; j < len(ids); j++ {
			if ids[j-1] >= ids[j] {
				t.Errorf("iteration %d: AttributeList not sorted at index %d: %d >= %d",
					i, j, ids[j-1], ids[j])
				t.Logf("full list: %v", ids)
				break
			}
		}
	}
}

func TestCommandListSorted(t *testing.T) {
	feature := NewFeature(FeatureMeasurement, 1)

	// Add commands in non-sequential order
	// The order of addition: 5, 1, 3, 2, 4
	// Expected sorted order: 1, 2, 3, 4, 5
	for _, id := range []uint8{5, 1, 3, 2, 4} {
		cmd := NewCommand(&CommandMetadata{
			ID:   id,
			Name: "cmd",
		}, func(_ context.Context, _ map[uint8]any) (map[uint8]any, error) {
			return nil, nil
		})
		feature.AddCommand(cmd)
	}

	// Call CommandList multiple times to detect non-deterministic ordering
	for i := 0; i < 10; i++ {
		ids := feature.CommandList()

		// Verify the list is sorted
		for j := 1; j < len(ids); j++ {
			if ids[j-1] >= ids[j] {
				t.Errorf("iteration %d: CommandList not sorted at index %d: %d >= %d",
					i, j, ids[j-1], ids[j])
				t.Logf("full list: %v", ids)
				break
			}
		}
	}
}

func TestFindEndpointsWithFeature(t *testing.T) {
	device := NewDevice("test", 1, 1)

	// Add two endpoints with Measurement feature
	ep1 := NewEndpoint(1, EndpointInverter, "Inverter")
	ep1.AddFeature(NewFeature(FeatureMeasurement, 1))
	_ = device.AddEndpoint(ep1)

	ep2 := NewEndpoint(2, EndpointBattery, "Battery")
	ep2.AddFeature(NewFeature(FeatureMeasurement, 1))
	ep2.AddFeature(NewFeature(FeatureElectrical, 1))
	_ = device.AddEndpoint(ep2)

	ep3 := NewEndpoint(3, EndpointPVString, "PV")
	ep3.AddFeature(NewFeature(FeatureElectrical, 1))
	_ = device.AddEndpoint(ep3)

	t.Run("FindMeasurement", func(t *testing.T) {
		found := device.FindEndpointsWithFeature(FeatureMeasurement)
		if len(found) != 2 {
			t.Errorf("expected 2 endpoints with Measurement, got %d", len(found))
		}
	})

	t.Run("FindElectrical", func(t *testing.T) {
		found := device.FindEndpointsWithFeature(FeatureElectrical)
		if len(found) != 2 {
			t.Errorf("expected 2 endpoints with Electrical, got %d", len(found))
		}
	})

	t.Run("FindNone", func(t *testing.T) {
		found := device.FindEndpointsWithFeature(FeatureTariff)
		if len(found) != 0 {
			t.Errorf("expected 0 endpoints with Tariff, got %d", len(found))
		}
	})
}
