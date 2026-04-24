package discovery

import (
	"testing"
)

// QR Code Tests

func TestParseQRCodeValid(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantVersion   uint8
		wantDiscrim   uint16
		wantSetupCode string
	}{
		{"Basic", "MASH:1:1234:20202021", 1, 1234, "20202021"},
		{"LeadingZerosSetup", "MASH:1:0:00001234", 1, 0, "00001234"},
		{"MaxDiscriminator", "MASH:1:4095:99999999", 1, 4095, "99999999"},
		{"Version255", "MASH:255:100:00000000", 255, 100, "00000000"},
		{"ZeroDiscriminator", "MASH:1:0:20202021", 1, 0, "20202021"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qr, err := ParseQRCode(tt.input)
			if err != nil {
				t.Fatalf("ParseQRCode(%q) error = %v", tt.input, err)
			}
			if qr.Version != tt.wantVersion {
				t.Errorf("Version = %d, want %d", qr.Version, tt.wantVersion)
			}
			if qr.Discriminator != tt.wantDiscrim {
				t.Errorf("Discriminator = %d, want %d", qr.Discriminator, tt.wantDiscrim)
			}
			if qr.SetupCode != tt.wantSetupCode {
				t.Errorf("SetupCode = %q, want %q", qr.SetupCode, tt.wantSetupCode)
			}
		})
	}
}

func TestParseQRCodeInvalid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"InvalidPrefix", "EEBUS:1:1234:20202021", ErrInvalidPrefix},
		{"WrongFieldCount3", "MASH:1:1234", ErrInvalidFieldCount},
		{"WrongFieldCount5", "MASH:1:1234:20202021:extra", ErrInvalidFieldCount},
		{"VersionZero", "MASH:0:1234:20202021", ErrInvalidVersion},
		{"VersionTooHigh", "MASH:256:1234:20202021", ErrInvalidVersion},
		{"VersionNonNumeric", "MASH:a:1234:20202021", ErrInvalidVersion},
		{"DiscriminatorTooHigh", "MASH:1:9999:20202021", ErrInvalidDiscriminator},
		{"DiscriminatorNonNumeric", "MASH:1:abc:20202021", ErrInvalidDiscriminator},
		{"SetupCodeTooShort", "MASH:1:1234:1234", ErrInvalidSetupCode},
		{"SetupCodeTooLong", "MASH:1:1234:123456789", ErrInvalidSetupCode},
		{"SetupCodeNonNumeric", "MASH:1:1234:1234abcd", ErrInvalidSetupCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseQRCode(tt.input)
			if err != tt.wantErr {
				t.Errorf("ParseQRCode(%q) error = %v, want %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestQRCodeString(t *testing.T) {
	qr := &QRCode{
		Version:       1,
		Discriminator: 1234,
		SetupCode:     "00001234",
	}

	want := "MASH:1:1234:00001234"
	got := qr.String()
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestNewQRCode(t *testing.T) {
	qr, err := NewQRCode(1234, "20202021")
	if err != nil {
		t.Fatalf("NewQRCode() error = %v", err)
	}

	if qr.Version != QRVersion {
		t.Errorf("Version = %d, want %d", qr.Version, QRVersion)
	}
	if qr.Discriminator != 1234 {
		t.Errorf("Discriminator = %d, want 1234", qr.Discriminator)
	}
	if qr.SetupCode != "20202021" {
		t.Errorf("SetupCode = %q, want \"20202021\"", qr.SetupCode)
	}
}

func TestNewQRCodeInvalid(t *testing.T) {
	// Discriminator too high
	_, err := NewQRCode(5000, "20202021")
	if err != ErrInvalidDiscriminator {
		t.Errorf("NewQRCode with discriminator 5000 error = %v, want ErrInvalidDiscriminator", err)
	}

	// Setup code too short
	_, err = NewQRCode(1234, "1234")
	if err != ErrInvalidSetupCode {
		t.Errorf("NewQRCode with short setup code error = %v, want ErrInvalidSetupCode", err)
	}
}

func TestGenerateQRCode(t *testing.T) {
	qr, err := GenerateQRCode()
	if err != nil {
		t.Fatalf("GenerateQRCode() error = %v", err)
	}
	if qr.Version != QRVersion {
		t.Errorf("Version = %d, want %d", qr.Version, QRVersion)
	}
	if qr.Discriminator > MaxDiscriminator {
		t.Errorf("Discriminator = %d, exceeds max %d", qr.Discriminator, MaxDiscriminator)
	}
	if len(qr.SetupCode) != SetupCodeLength {
		t.Errorf("SetupCode length = %d, want %d", len(qr.SetupCode), SetupCodeLength)
	}

	// Verify the output is parseable.
	parsed, err := ParseQRCode(qr.String())
	if err != nil {
		t.Fatalf("ParseQRCode(GenerateQRCode().String()) error = %v", err)
	}
	if parsed.Discriminator != qr.Discriminator {
		t.Errorf("round-trip discriminator mismatch: %d vs %d", parsed.Discriminator, qr.Discriminator)
	}
}

func TestFormatSetupCode(t *testing.T) {
	tests := []struct {
		code uint32
		want string
	}{
		{1234, "00001234"},
		{0, "00000000"},
		{20202021, "20202021"},
		{99999999, "99999999"},
	}

	for _, tt := range tests {
		got := FormatSetupCode(tt.code)
		if got != tt.want {
			t.Errorf("FormatSetupCode(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestInstanceNameFromDiscriminator(t *testing.T) {
	tests := []struct {
		discriminator uint16
		want          string
	}{
		{1234, "MASH-1234"},
		{0, "MASH-0"},
		{4095, "MASH-4095"},
	}

	for _, tt := range tests {
		got := InstanceNameFromDiscriminator(tt.discriminator)
		if got != tt.want {
			t.Errorf("InstanceNameFromDiscriminator(%d) = %q, want %q", tt.discriminator, got, tt.want)
		}
	}
}

func TestDiscriminatorFromInstanceName(t *testing.T) {
	tests := []struct {
		name    string
		want    uint16
		wantErr bool
	}{
		{"MASH-1234", 1234, false},
		{"MASH-0", 0, false},
		{"MASH-4095", 4095, false},
		{"MASH-5000", 0, true},       // Out of range
		{"INVALID-1234", 0, true},    // Wrong prefix
		{"MASH-", 0, true},           // Missing discriminator
		{"MASH-abc", 0, true},        // Non-numeric
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DiscriminatorFromInstanceName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("DiscriminatorFromInstanceName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("DiscriminatorFromInstanceName(%q) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestOperationalInstanceName(t *testing.T) {
	got := OperationalInstanceName("A1B2C3D4E5F6A7B8", "F9E8D7C6B5A49382")
	want := "A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382"
	if got != want {
		t.Errorf("OperationalInstanceName() = %q, want %q", got, want)
	}
}

func TestParseOperationalInstanceName(t *testing.T) {
	tests := []struct {
		name       string
		wantZone   string
		wantDevice string
		wantErr    bool
	}{
		{"A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382", "A1B2C3D4E5F6A7B8", "F9E8D7C6B5A49382", false},
		{"1234567890ABCDEF-FEDCBA0987654321", "1234567890ABCDEF", "FEDCBA0987654321", false},
		{"invalid", "", "", true},
		{"short-12345", "", "", true},                      // IDs too short
		{"A1B2C3D4E5F6A7B8-GHIJ", "", "", true},             // Invalid hex
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			zoneID, deviceID, err := ParseOperationalInstanceName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseOperationalInstanceName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if zoneID != tt.wantZone {
					t.Errorf("zoneID = %q, want %q", zoneID, tt.wantZone)
				}
				if deviceID != tt.wantDevice {
					t.Errorf("deviceID = %q, want %q", deviceID, tt.wantDevice)
				}
			}
		})
	}
}

// TXT Record Tests

func TestCommissionableTXTRoundtrip(t *testing.T) {
	info := &CommissionableInfo{
		Discriminator: 1234,
		Categories:    []DeviceCategory{CategoryEMobility},
		Serial:        "WB-2024-001234",
		Brand:         "ChargePoint",
		Model:         "Home Flex",
		DeviceName:    "Garage Charger",
	}

	txt := EncodeCommissionableTXT(info)

	// Verify TXT records
	if txt[TXTKeyDiscriminator] != "1234" {
		t.Errorf("D = %q, want \"1234\"", txt[TXTKeyDiscriminator])
	}
	if txt[TXTKeyCategories] != "3" {
		t.Errorf("cat = %q, want \"3\"", txt[TXTKeyCategories])
	}

	// Decode and verify
	decoded, err := DecodeCommissionableTXT(txt)
	if err != nil {
		t.Fatalf("DecodeCommissionableTXT() error = %v", err)
	}

	if decoded.Discriminator != info.Discriminator {
		t.Errorf("Discriminator = %d, want %d", decoded.Discriminator, info.Discriminator)
	}
	if len(decoded.Categories) != 1 || decoded.Categories[0] != CategoryEMobility {
		t.Errorf("Categories = %v, want [%d]", decoded.Categories, CategoryEMobility)
	}
	if decoded.Serial != info.Serial {
		t.Errorf("Serial = %q, want %q", decoded.Serial, info.Serial)
	}
	if decoded.Brand != info.Brand {
		t.Errorf("Brand = %q, want %q", decoded.Brand, info.Brand)
	}
	if decoded.Model != info.Model {
		t.Errorf("Model = %q, want %q", decoded.Model, info.Model)
	}
	if decoded.DeviceName != info.DeviceName {
		t.Errorf("DeviceName = %q, want %q", decoded.DeviceName, info.DeviceName)
	}
}

func TestCommissionableTXTMultipleCategories(t *testing.T) {
	info := &CommissionableInfo{
		Discriminator: 2048,
		Categories:    []DeviceCategory{CategoryEMS, CategoryInverter},
		Serial:        "INV-2024-567890",
		Brand:         "SolarEdge",
		Model:         "Home Hub",
	}

	txt := EncodeCommissionableTXT(info)

	if txt[TXTKeyCategories] != "2,5" {
		t.Errorf("cat = %q, want \"2,5\"", txt[TXTKeyCategories])
	}

	decoded, err := DecodeCommissionableTXT(txt)
	if err != nil {
		t.Fatalf("DecodeCommissionableTXT() error = %v", err)
	}

	if len(decoded.Categories) != 2 {
		t.Errorf("Categories len = %d, want 2", len(decoded.Categories))
	}
}

func TestDecodeCommissionableTXTMissingRequired(t *testing.T) {
	tests := []struct {
		name   string
		txt    TXTRecordMap
	}{
		{"MissingD", TXTRecordMap{"cat": "3", "serial": "X", "brand": "Y", "model": "Z"}},
		{"MissingCat", TXTRecordMap{"D": "1234", "serial": "X", "brand": "Y", "model": "Z"}},
		{"MissingSerial", TXTRecordMap{"D": "1234", "cat": "3", "brand": "Y", "model": "Z"}},
		{"MissingBrand", TXTRecordMap{"D": "1234", "cat": "3", "serial": "X", "model": "Z"}},
		{"MissingModel", TXTRecordMap{"D": "1234", "cat": "3", "serial": "X", "brand": "Y"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeCommissionableTXT(tt.txt)
			if err == nil {
				t.Error("DecodeCommissionableTXT() should fail with missing required field")
			}
		})
	}
}

func TestOperationalTXTRoundtrip(t *testing.T) {
	info := &OperationalInfo{
		ZoneID:        "A1B2C3D4E5F6A7B8",
		DeviceID:      "F9E8D7C6B5A49382",
		VendorProduct: "1234:5678",
		Firmware:      "1.2.3",
		EndpointCount: 2,
	}

	txt := EncodeOperationalTXT(info)

	decoded, err := DecodeOperationalTXT(txt)
	if err != nil {
		t.Fatalf("DecodeOperationalTXT() error = %v", err)
	}

	if decoded.ZoneID != info.ZoneID {
		t.Errorf("ZoneID = %q, want %q", decoded.ZoneID, info.ZoneID)
	}
	if decoded.DeviceID != info.DeviceID {
		t.Errorf("DeviceID = %q, want %q", decoded.DeviceID, info.DeviceID)
	}
	if decoded.VendorProduct != info.VendorProduct {
		t.Errorf("VendorProduct = %q, want %q", decoded.VendorProduct, info.VendorProduct)
	}
	if decoded.Firmware != info.Firmware {
		t.Errorf("Firmware = %q, want %q", decoded.Firmware, info.Firmware)
	}
	if decoded.EndpointCount != info.EndpointCount {
		t.Errorf("EndpointCount = %d, want %d", decoded.EndpointCount, info.EndpointCount)
	}
}

// TestOperationalTXT_OmitsFeatureMap enforces DEC-074: operational mDNS TXT
// MUST NOT carry featureMap bits. Capability discovery is performed through
// the wire-level featureMap attribute (0xFFFC) per feature, not via mDNS
// broadcast — where the encoding is ambiguous (raw bits don't map 1:1 to
// feature presence) and wastes constrained announce-packet budget.
func TestOperationalTXT_OmitsFeatureMap(t *testing.T) {
	info := &OperationalInfo{
		ZoneID:        "A1B2C3D4E5F6A7B8",
		DeviceID:      "F9E8D7C6B5A49382",
		VendorProduct: "1234:5678",
		Firmware:      "1.2.3",
		EndpointCount: 2,
	}
	txt := EncodeOperationalTXT(info)
	if v, ok := txt["FM"]; ok {
		t.Errorf("Operational TXT must not include FM key (DEC-074); got FM=%q", v)
	}
}

func TestDecodeOperationalTXTMissingRequired(t *testing.T) {
	tests := []struct {
		name string
		txt  TXTRecordMap
	}{
		{"MissingZI", TXTRecordMap{"DI": "F9E8D7C6B5A49382"}},
		{"MissingDI", TXTRecordMap{"ZI": "A1B2C3D4E5F6A7B8"}},
		{"ShortZI", TXTRecordMap{"ZI": "A1B2", "DI": "F9E8D7C6B5A49382"}},
		{"ShortDI", TXTRecordMap{"ZI": "A1B2C3D4E5F6A7B8", "DI": "F9E8"}},
		{"InvalidHexZI", TXTRecordMap{"ZI": "GHIJKLMNOPQRSTUV", "DI": "F9E8D7C6B5A49382"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeOperationalTXT(tt.txt)
			if err == nil {
				t.Error("DecodeOperationalTXT() should fail with missing/invalid required field")
			}
		})
	}
}

func TestTXTRecordsToStrings(t *testing.T) {
	txt := TXTRecordMap{
		"D":      "1234",
		"cat":    "3",
		"serial": "WB-001",
	}

	strs := TXTRecordsToStrings(txt)

	if len(strs) != 3 {
		t.Errorf("len(strs) = %d, want 3", len(strs))
	}

	// Convert back
	parsed := StringsToTXTRecords(strs)
	if parsed["D"] != "1234" {
		t.Errorf("D = %q, want \"1234\"", parsed["D"])
	}
}

func TestStringsToTXTRecords(t *testing.T) {
	strs := []string{
		"D=1234",
		"cat=3",
		"serial=WB-001",
		"flag",          // Key without value
		"empty=",        // Key with empty value
	}

	txt := StringsToTXTRecords(strs)

	if txt["D"] != "1234" {
		t.Errorf("D = %q, want \"1234\"", txt["D"])
	}
	if txt["cat"] != "3" {
		t.Errorf("cat = %q, want \"3\"", txt["cat"])
	}
	if txt["flag"] != "" {
		t.Errorf("flag = %q, want \"\"", txt["flag"])
	}
	if txt["empty"] != "" {
		t.Errorf("empty = %q, want \"\"", txt["empty"])
	}
}

func TestValidateInstanceName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"MASH-1234", false},
		{"Home Energy", false},
		{"A1B2C3D4E5F6A7B8-F9E8D7C6B5A49382", false},
		{"", true},
		{string(make([]byte, 64)), true}, // 64 chars, too long
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstanceName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateInstanceName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

// DeviceCategory Tests

func TestDeviceCategoryString(t *testing.T) {
	tests := []struct {
		cat  DeviceCategory
		want string
	}{
		{CategoryGCPH, "GCPH"},
		{CategoryEMS, "EMS"},
		{CategoryEMobility, "E-MOBILITY"},
		{CategoryHVAC, "HVAC"},
		{CategoryInverter, "INVERTER"},
		{CategoryAppliance, "APPLIANCE"},
		{CategoryMetering, "METERING"},
		{DeviceCategory(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.cat.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// DiscoveryState Tests

func TestDiscoveryStateString(t *testing.T) {
	tests := []struct {
		state DiscoveryState
		want  string
	}{
		{StateUnregistered, "UNREGISTERED"},
		{StateUncommissioned, "UNCOMMISSIONED"},
		{StateCommissioningOpen, "COMMISSIONING_OPEN"},
		{StateOperational, "OPERATIONAL"},
		{StateOperationalCommissioning, "OPERATIONAL_COMMISSIONING"},
		{DiscoveryState(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
