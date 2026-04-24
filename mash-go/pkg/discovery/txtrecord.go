package discovery

import (
	"fmt"
	"strconv"
	"strings"
)

// TXTRecordMap is a map of TXT record key-value pairs.
type TXTRecordMap map[string]string

// EncodeCommissionableTXT creates TXT records for commissionable discovery.
func EncodeCommissionableTXT(info *CommissionableInfo) TXTRecordMap {
	txt := make(TXTRecordMap)

	// Required fields
	txt[TXTKeyDiscriminator] = strconv.FormatUint(uint64(info.Discriminator), 10)
	txt[TXTKeyCategories] = encodeCategories(info.Categories)
	txt[TXTKeySerial] = info.Serial
	txt[TXTKeyBrand] = info.Brand
	txt[TXTKeyModel] = info.Model

	// Optional fields
	if info.DeviceName != "" {
		txt[TXTKeyDeviceName] = info.DeviceName
	}

	return txt
}

// DecodeCommissionableTXT parses TXT records from commissionable discovery.
func DecodeCommissionableTXT(txt TXTRecordMap) (*CommissionableInfo, error) {
	info := &CommissionableInfo{}

	// Parse discriminator (required)
	dStr, ok := txt[TXTKeyDiscriminator]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyDiscriminator)
	}
	d, err := strconv.ParseUint(dStr, 10, 16)
	if err != nil || d > MaxDiscriminator {
		return nil, ErrInvalidDiscriminator
	}
	info.Discriminator = uint16(d)

	// Parse categories (required)
	catStr, ok := txt[TXTKeyCategories]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyCategories)
	}
	info.Categories, err = parseCategories(catStr)
	if err != nil {
		return nil, err
	}

	// Parse serial (required)
	info.Serial, ok = txt[TXTKeySerial]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeySerial)
	}

	// Parse brand (required)
	info.Brand, ok = txt[TXTKeyBrand]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyBrand)
	}

	// Parse model (required)
	info.Model, ok = txt[TXTKeyModel]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyModel)
	}

	// Optional fields
	info.DeviceName = txt[TXTKeyDeviceName]

	return info, nil
}

// EncodeOperationalTXT creates TXT records for operational discovery.
func EncodeOperationalTXT(info *OperationalInfo) TXTRecordMap {
	txt := make(TXTRecordMap)

	// Required fields
	txt[TXTKeyZoneID] = info.ZoneID
	txt[TXTKeyDeviceID] = info.DeviceID

	// Optional fields
	if info.VendorProduct != "" {
		txt[TXTKeyVendorProd] = info.VendorProduct
	}
	if info.Firmware != "" {
		txt[TXTKeyFirmware] = info.Firmware
	}
	if info.EndpointCount > 0 {
		txt[TXTKeyEndpoints] = strconv.FormatUint(uint64(info.EndpointCount), 10)
	}

	return txt
}

// DecodeOperationalTXT parses TXT records from operational discovery.
func DecodeOperationalTXT(txt TXTRecordMap) (*OperationalInfo, error) {
	info := &OperationalInfo{}

	// Parse zone ID (required)
	var ok bool
	info.ZoneID, ok = txt[TXTKeyZoneID]
	if !ok || len(info.ZoneID) != IDLength {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyZoneID)
	}
	if !isHexString(info.ZoneID) {
		return nil, fmt.Errorf("%w: invalid zone ID format", ErrInvalidTXTRecord)
	}

	// Parse device ID (required)
	info.DeviceID, ok = txt[TXTKeyDeviceID]
	if !ok || len(info.DeviceID) != IDLength {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyDeviceID)
	}
	if !isHexString(info.DeviceID) {
		return nil, fmt.Errorf("%w: invalid device ID format", ErrInvalidTXTRecord)
	}

	// Optional fields
	info.VendorProduct = txt[TXTKeyVendorProd]
	info.Firmware = txt[TXTKeyFirmware]

	if epStr, ok := txt[TXTKeyEndpoints]; ok {
		ep, err := strconv.ParseUint(epStr, 10, 8)
		if err == nil {
			info.EndpointCount = uint8(ep)
		}
	}

	return info, nil
}

// encodeCategories converts categories to comma-separated string.
func encodeCategories(cats []DeviceCategory) string {
	if len(cats) == 0 {
		return ""
	}

	strs := make([]string, len(cats))
	for i, c := range cats {
		strs[i] = strconv.FormatUint(uint64(c), 10)
	}
	return strings.Join(strs, ",")
}

// parseCategories parses comma-separated category string.
func parseCategories(s string) ([]DeviceCategory, error) {
	if s == "" {
		return nil, nil
	}

	parts := strings.Split(s, ",")
	cats := make([]DeviceCategory, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid category %q", ErrInvalidTXTRecord, p)
		}
		cats = append(cats, DeviceCategory(n))
	}

	return cats, nil
}

// TXTRecordsToStrings converts a TXTRecordMap to a slice of "key=value" strings.
// This format is commonly used by mDNS libraries.
func TXTRecordsToStrings(txt TXTRecordMap) []string {
	result := make([]string, 0, len(txt))
	for k, v := range txt {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// StringsToTXTRecords parses a slice of "key=value" strings into a TXTRecordMap.
func StringsToTXTRecords(strs []string) TXTRecordMap {
	txt := make(TXTRecordMap)
	for _, s := range strs {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			txt[parts[0]] = parts[1]
		} else if len(parts) == 1 && parts[0] != "" {
			// Key without value (boolean flag)
			txt[parts[0]] = ""
		}
	}
	return txt
}

// ValidateInstanceName checks if an instance name is valid for mDNS.
func ValidateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInstanceNameTooLong)
	}
	if len(name) > MaxInstanceNameLen {
		return ErrInstanceNameTooLong
	}
	return nil
}

// EncodePairingRequestTXT creates TXT records for pairing request discovery.
func EncodePairingRequestTXT(info *PairingRequestInfo) TXTRecordMap {
	txt := make(TXTRecordMap)

	// Required fields
	txt[TXTKeyDiscriminator] = strconv.FormatUint(uint64(info.Discriminator), 10)
	txt[TXTKeyZoneID] = info.ZoneID

	// Optional fields
	if info.ZoneName != "" {
		txt[TXTKeyZoneName] = info.ZoneName
	}

	return txt
}

// DecodePairingRequestTXT parses TXT records from pairing request discovery.
func DecodePairingRequestTXT(txt TXTRecordMap) (*PairingRequestInfo, error) {
	info := &PairingRequestInfo{}

	// Parse discriminator (required)
	dStr, ok := txt[TXTKeyDiscriminator]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyDiscriminator)
	}
	d, err := strconv.ParseUint(dStr, 10, 16)
	if err != nil || d > MaxDiscriminator {
		return nil, ErrInvalidDiscriminator
	}
	info.Discriminator = uint16(d)

	// Parse zone ID (required)
	info.ZoneID, ok = txt[TXTKeyZoneID]
	if !ok || len(info.ZoneID) != IDLength {
		return nil, fmt.Errorf("%w: %s", ErrMissingRequired, TXTKeyZoneID)
	}
	if !isHexString(info.ZoneID) {
		return nil, fmt.Errorf("%w: invalid zone ID format", ErrInvalidTXTRecord)
	}

	// Optional fields
	info.ZoneName = txt[TXTKeyZoneName]

	return info, nil
}

// PairingRequestInstanceName creates an mDNS instance name for a pairing request.
// Format: "{ZoneID}-{Discriminator}" (e.g., "A1B2C3D4E5F6A7B8-1234").
func PairingRequestInstanceName(zoneID string, discriminator uint16) string {
	return fmt.Sprintf("%s-%d", zoneID, discriminator)
}

// ParsePairingRequestInstanceName parses a pairing request instance name.
// Returns the zone ID and discriminator.
func ParsePairingRequestInstanceName(name string) (zoneID string, discriminator uint16, err error) {
	// Find the last hyphen (discriminator comes after it)
	lastHyphen := strings.LastIndex(name, "-")
	if lastHyphen == -1 || lastHyphen == 0 || lastHyphen == len(name)-1 {
		return "", 0, fmt.Errorf("%w: invalid pairing request instance name format", ErrInvalidTXTRecord)
	}

	zoneID = name[:lastHyphen]
	discrimStr := name[lastHyphen+1:]

	// Validate zone ID
	if len(zoneID) != IDLength {
		return "", 0, fmt.Errorf("%w: invalid zone ID length", ErrInvalidTXTRecord)
	}
	if !isHexString(zoneID) {
		return "", 0, fmt.Errorf("%w: invalid zone ID format", ErrInvalidTXTRecord)
	}

	// Parse discriminator
	d, err := strconv.ParseUint(discrimStr, 10, 16)
	if err != nil || d > MaxDiscriminator {
		return "", 0, ErrInvalidDiscriminator
	}

	return zoneID, uint16(d), nil
}
