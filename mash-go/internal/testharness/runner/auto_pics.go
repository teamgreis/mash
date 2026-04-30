package runner

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/mash-protocol/mash-go/internal/testharness/loader"
	"github.com/mash-protocol/mash-go/pkg/features"
	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/internal/pics"
	"github.com/mash-protocol/mash-go/pkg/usecase"
	"github.com/mash-protocol/mash-go/pkg/wire"
)

// basePICSFilename is the YAML file containing protocol-level PICS items
// that are true for any conformant device. Located in testdata/pics/ as a
// sibling of the test cases directory.
const basePICSFilename = "protocol-common.yaml"

// capabilityBoolAttrs lists per-feature attribute IDs whose values are
// read and emitted as PICS booleans (rather than the default true-on-
// presence). For these attributes, the PICS item reflects the device's
// actual capability rather than "the device exposes this attribute".
//
// Why: corpus tests like TC-CTRL-003 gate on `MASH.S.CTRL.A0C`
// (acceptsSetpoints presence) but the test author's intent is "this
// device accepts setpoints". An EVSE that has the attribute but its
// value is false should SKIP the test, not run it. CheckPICSRequirements
// already treats `value: false` as failing the requirement, so the only
// missing piece was reading the value. Same shape as the static PICS
// files (e.g. testdata/pics/inverter.yaml) which encode
// `MASH.S.E01.CTRL.A0C: false`.
var capabilityBoolAttrs = map[uint8][]uint16{
	uint8(model.FeatureEnergyControl): {
		features.EnergyControlAttrAcceptsLimits,
		features.EnergyControlAttrAcceptsCurrentLimits,
		features.EnergyControlAttrAcceptsSetpoints,
		features.EnergyControlAttrAcceptsCurrentSetpoints,
		features.EnergyControlAttrIsPausable,
		features.EnergyControlAttrIsShiftable,
		features.EnergyControlAttrIsStoppable,
	},
}

// buildAutoPICS loads the base protocol PICS from YAML, then discovers
// device-specific capabilities over the wire and merges them on top.
// The runner must have an active connection (commissioned) before calling this.
func (r *Runner) buildAutoPICS(ctx context.Context) (*loader.PICSFile, error) {
	if !r.pool.Main().isConnected() {
		return nil, fmt.Errorf("auto-PICS requires an active connection")
	}

	// Load base protocol PICS from YAML (single source of truth for
	// protocol-level items). Located as a sibling of the test cases dir.
	basePath := filepath.Join(filepath.Dir(r.config.TestDir), "pics", basePICSFilename)
	basePICS, err := loader.LoadPICS(basePath)
	if err != nil {
		return nil, fmt.Errorf("auto-PICS: failed to load base PICS from %s: %w", basePath, err)
	}

	// Start with base items; device-specific discoveries override/extend.
	items := basePICS.Items

	// Read DeviceInfo from endpoint 0, feature 0x01.
	deviceAttrs, err := r.readAttributes(ctx, 0, uint8(model.FeatureDeviceInfo))
	if err != nil {
		return nil, fmt.Errorf("auto-PICS: failed to read DeviceInfo: %w", err)
	}

	// Device-level PICS items.
	if v, ok := deviceAttrs[features.DeviceInfoAttrSpecVersion].(string); ok {
		items["MASH.S.VERSION"] = v
	}

	// Device metadata for the PICS file header.
	picsDevice := loader.PICSDevice{}
	if v, ok := deviceAttrs[features.DeviceInfoAttrVendorName].(string); ok {
		picsDevice.Vendor = v
	}
	if v, ok := deviceAttrs[features.DeviceInfoAttrProductName].(string); ok {
		picsDevice.Product = v
	}

	// Parse endpoints and discover features.
	endpoints := parseAutoPICSEndpoints(deviceAttrs[features.DeviceInfoAttrEndpoints])

	for _, ep := range endpoints {
		epCode := fmt.Sprintf("MASH.S.E%02X", ep.id)
		items[epCode] = model.EndpointType(ep.epType).String()

		for _, featID := range ep.features {
			picsCode, ok := pics.FeatureTypeToPICSCode[uint8(featID)]
			if !ok {
				picsCode = fmt.Sprintf("F%02X", featID)
			}
			featKey := fmt.Sprintf("%s.%s", epCode, picsCode)
			items[featKey] = true

			// Also emit endpoint-free feature key (MASH.S.CTRL alongside
			// MASH.S.E01.CTRL) so tests can reference features without
			// knowing which endpoint they're on.
			items[fmt.Sprintf("MASH.S.%s", picsCode)] = true

			// Read global attributes for this feature.
			attrList, cmdList, featMap, gErr := r.readFeatureGlobals(ctx, ep.id, uint8(featID))
			if gErr != nil {
				if r.config.Verbose {
					log.Printf("auto-PICS: warning: failed to read globals for E%02X.%s: %v", ep.id, picsCode, gErr)
				}
				continue
			}

			// Capability boolean attributes need their actual values read
			// so PICS gates correctly skip tests on devices that expose
			// the attribute but answer "false". Pre-fetch all attributes
			// for this feature when it has registered capability bools,
			// otherwise the default true-on-presence emission is fine.
			var featureAttrs map[uint16]any
			if _, hasCapBools := capabilityBoolAttrs[uint8(featID)]; hasCapBools {
				if attrs, rerr := r.readAttributes(ctx, ep.id, uint8(featID)); rerr == nil {
					featureAttrs = attrs
				} else if r.config.Verbose {
					log.Printf("auto-PICS: warning: failed to read attributes for E%02X.%s: %v", ep.id, picsCode, rerr)
				}
			}
			for _, attrID := range attrList {
				picsValue := capabilityPICSValue(uint8(featID), attrID, featureAttrs)
				items[fmt.Sprintf("%s.A%02X", featKey, attrID)] = picsValue
				// Endpoint-free attribute key.
				items[fmt.Sprintf("MASH.S.%s.A%02X", picsCode, attrID)] = picsValue
			}
			for _, cmdID := range cmdList {
				items[fmt.Sprintf("%s.C%02X.Rsp", featKey, cmdID)] = true
				items[fmt.Sprintf("MASH.S.%s.C%02X.Rsp", picsCode, cmdID)] = true
			}
			for bit := 0; bit < 32; bit++ {
				if featMap&(1<<bit) != 0 {
					items[fmt.Sprintf("%s.F%02X", featKey, bit)] = true
					items[fmt.Sprintf("MASH.S.%s.F%02X", picsCode, bit)] = true
				}
			}
		}
	}

	// DEC-043: MaxZones = 2 (1 GRID + 1 LOCAL). TEST zones are an extra
	// observer slot that doesn't count against MaxZones (DEC-060), so the
	// PICS value is always 2 regardless of enable-key / TestControl.
	items["MASH.S.ZONE.MAX"] = 2

	// Parse use case declarations.
	useCases := parseAutoPICSUseCases(deviceAttrs[features.DeviceInfoAttrUseCases])
	for _, uc := range useCases {
		name, ok := usecase.IDToName[usecase.UseCaseID(uc.id)]
		if !ok {
			if r.config.Verbose {
				log.Printf("auto-PICS: unknown use case ID 0x%02X, skipping", uc.id)
			}
			continue
		}
		ucKey := fmt.Sprintf("MASH.S.UC.%s", name)
		items[ucKey] = true

		// Decode scenario bits.
		for bit := 0; bit < 32; bit++ {
			if uc.scenarios&(1<<bit) != 0 {
				items[fmt.Sprintf("%s.S%02d", ucKey, bit)] = true
			}
		}
	}

	pf := &loader.PICSFile{
		Name:   "auto-discovered",
		Device: picsDevice,
		Items:  items,
	}
	return pf, nil
}

// capabilityPICSValue returns the PICS value for an attribute. Most
// attributes get the default true-on-presence; capability booleans
// listed in capabilityBoolAttrs return the device's actual value when
// available, falling back to true if the read failed or the value
// wasn't a bool (defensive default -- an unreadable capability flag
// shouldn't gate the test more strictly than presence alone).
func capabilityPICSValue(featureID uint8, attrID uint16, featureAttrs map[uint16]any) any {
	caps, ok := capabilityBoolAttrs[featureID]
	if !ok {
		return true
	}
	isCap := false
	for _, id := range caps {
		if id == attrID {
			isCap = true
			break
		}
	}
	if !isCap || featureAttrs == nil {
		return true
	}
	if v, ok := featureAttrs[attrID].(bool); ok {
		return v
	}
	return true
}

// readAttributes sends a Read request for all attributes of a feature
// and returns the response payload as a map keyed by attribute ID.
func (r *Runner) readAttributes(ctx context.Context, endpointID, featureID uint8) (map[uint16]any, error) {
	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpRead,
		EndpointID: endpointID,
		FeatureID:  featureID,
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("encode read: %w", err)
	}

	resp, err := r.sendRequest(data, "auto-pics-read", req.MessageID)
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("read failed with status %d", resp.Status)
	}

	return parseReadPayload(resp.Payload), nil
}

// readFeatureGlobals reads the global attributes (attributeList, commandList,
// featureMap) for a specific feature on an endpoint.
func (r *Runner) readFeatureGlobals(ctx context.Context, endpointID, featureID uint8) (attrList []uint16, cmdList []uint8, featMap uint32, err error) {
	req := &wire.Request{
		MessageID:  r.nextMessageID(),
		Operation:  wire.OpRead,
		EndpointID: endpointID,
		FeatureID:  featureID,
		Payload: &wire.ReadPayload{
			AttributeIDs: []uint16{
				model.AttrIDAttributeList,
				model.AttrIDCommandList,
				model.AttrIDFeatureMap,
			},
		},
	}

	data, err := wire.EncodeRequest(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("encode read: %w", err)
	}

	resp, sendErr := r.sendRequest(data, "auto-pics-globals", req.MessageID)
	if sendErr != nil {
		return nil, nil, 0, sendErr
	}
	if !resp.IsSuccess() {
		return nil, nil, 0, fmt.Errorf("read globals failed with status %d", resp.Status)
	}

	attrs := parseReadPayload(resp.Payload)

	// Parse attributeList.
	if v, ok := attrs[model.AttrIDAttributeList].([]any); ok {
		for _, a := range v {
			if id, ok := wire.ToUint32(a); ok {
				attrList = append(attrList, uint16(id))
			}
		}
	}

	// Parse commandList.
	// CBOR encodes []uint8 as a byte string, so handle both []any and []byte.
	switch v := attrs[model.AttrIDCommandList].(type) {
	case []byte:
		cmdList = append(cmdList, v...)
	case []any:
		for _, c := range v {
			if id, ok := wire.ToUint8Public(c); ok {
				cmdList = append(cmdList, id)
			}
		}
	}

	// Parse featureMap.
	if v, ok := wire.ToUint32(attrs[model.AttrIDFeatureMap]); ok {
		featMap = v
	}

	return attrList, cmdList, featMap, nil
}

// parseReadPayload converts the raw CBOR-decoded read response payload into
// a map keyed by uint16 attribute IDs.
func parseReadPayload(raw any) map[uint16]any {
	result := make(map[uint16]any)
	switch m := raw.(type) {
	case map[any]any:
		for k, v := range m {
			if id, ok := wire.ToUint32(k); ok {
				result[uint16(id)] = v
			}
		}
	case map[uint16]any:
		return m
	}
	return result
}

// autoPICSEndpoint holds parsed endpoint data for auto-PICS.
type autoPICSEndpoint struct {
	id       uint8
	epType   uint8
	label    string
	features []uint16
}

// parseAutoPICSEndpoints parses the CBOR-decoded endpoints attribute value.
// This mirrors the parsing in service/capability_read.go but is self-contained
// to avoid import cycles.
func parseAutoPICSEndpoints(raw any) []autoPICSEndpoint {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	var eps []autoPICSEndpoint
	for _, item := range arr {
		m, ok := item.(map[any]any)
		if !ok {
			continue
		}

		var ep autoPICSEndpoint
		if v, ok := m[uint64(1)]; ok {
			if id, ok := wire.ToUint8Public(v); ok {
				ep.id = id
			}
		}
		if v, ok := m[uint64(2)]; ok {
			if t, ok := wire.ToUint8Public(v); ok {
				ep.epType = t
			}
		}
		if v, ok := m[uint64(3)]; ok {
			if s, ok := v.(string); ok {
				ep.label = s
			}
		}
		if v, ok := m[uint64(4)]; ok {
			if feats, ok := v.([]any); ok {
				for _, f := range feats {
					if id, ok := wire.ToUint32(f); ok {
						ep.features = append(ep.features, uint16(id))
					}
				}
			}
		}

		eps = append(eps, ep)
	}

	return eps
}

// autoPICSUseCase holds parsed use case data for auto-PICS.
type autoPICSUseCase struct {
	endpointID uint8
	id         uint16
	major      uint8
	minor      uint8
	scenarios  uint32
}

// parseAutoPICSUseCases parses the CBOR-decoded useCases attribute value.
func parseAutoPICSUseCases(raw any) []autoPICSUseCase {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}

	var ucs []autoPICSUseCase
	for _, item := range arr {
		m, ok := item.(map[any]any)
		if !ok {
			continue
		}

		var uc autoPICSUseCase
		if v, ok := m[uint64(1)]; ok {
			if id, ok := wire.ToUint8Public(v); ok {
				uc.endpointID = id
			}
		}
		if v, ok := m[uint64(2)]; ok {
			if id, ok := wire.ToUint32(v); ok {
				uc.id = uint16(id)
			}
		}
		if v, ok := m[uint64(3)]; ok {
			if maj, ok := wire.ToUint8Public(v); ok {
				uc.major = maj
			}
		}
		if v, ok := m[uint64(4)]; ok {
			if min, ok := wire.ToUint8Public(v); ok {
				uc.minor = min
			}
		}
		if v, ok := m[uint64(5)]; ok {
			if sc, ok := wire.ToUint32(v); ok {
				uc.scenarios = sc
			}
		}

		ucs = append(ucs, uc)
	}

	return ucs
}

// logAutoPICS prints the discovered PICS items in verbose mode.
func logAutoPICS(pf *loader.PICSFile) {
	log.Printf("Auto-PICS: discovered %d items from device", len(pf.Items))
	if pf.Device.Vendor != "" || pf.Device.Product != "" {
		log.Printf("Auto-PICS: device: %s %s", pf.Device.Vendor, pf.Device.Product)
	}

	// Log use cases.
	var ucNames []string
	for key := range pf.Items {
		if strings.HasPrefix(key, "MASH.S.UC.") && strings.Count(key, ".") == 3 {
			ucNames = append(ucNames, strings.TrimPrefix(key, "MASH.S.UC."))
		}
	}
	if len(ucNames) > 0 {
		log.Printf("Auto-PICS: use cases: %s", strings.Join(ucNames, ", "))
	}

	// Log endpoints.
	for key, val := range pf.Items {
		if strings.HasPrefix(key, "MASH.S.E") && strings.Count(key, ".") == 2 {
			log.Printf("Auto-PICS: endpoint %s = %v", key, val)
		}
	}
}
