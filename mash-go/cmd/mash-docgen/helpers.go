package main

import (
	"fmt"
	"strings"

	"github.com/mash-protocol/mash-go/internal/specparse"
)

// featureSlug converts "DeviceInfo" to "device-info".
func featureSlug(name string) string {
	return specparse.FeatureDirName(name)
}

// isInternalFeature reports whether a feature is reserved for internal or
// test-harness use and should be hidden from user-facing documentation.
// The feature is still emitted in code generation and on the wire; only its
// nav entries and the feature-index page suppress it.
func isInternalFeature(name string) bool {
	switch name {
	case "TestControl":
		return true
	}
	return false
}

// userFacingFeatures returns the subset of all features that should appear in
// the user-facing nav and index pages.
func userFacingFeatures(all []*specparse.RawFeatureDef) []*specparse.RawFeatureDef {
	out := make([]*specparse.RawFeatureDef, 0, len(all))
	for _, f := range all {
		if isInternalFeature(f.Name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// usecaseSlug converts "GPL" to "gpl".
func usecaseSlug(name string) string {
	return strings.ToLower(name)
}

// endpointSlug converts "EV_CHARGER" to "ev-charger".
func endpointSlug(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

// hexByte formats a value as 0x02.
func hexByte(v int) string {
	return fmt.Sprintf("0x%02X", v)
}

// hexWord formats a value as 0x001A.
func hexWord(v int) string {
	return fmt.Sprintf("0x%04X", v)
}

// yesNo returns "Yes" for true, empty string for false.
func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return ""
}

// formatAttrType renders an attribute type string for documentation.
func formatAttrType(attr specparse.RawAttributeDef) string {
	if attr.Enum != "" {
		return fmt.Sprintf("%s (%s)", attr.Type, attr.Enum)
	}
	if attr.Type == "map" {
		return fmt.Sprintf("map[%s]%s", attr.MapKeyType, attr.MapValueType)
	}
	if attr.Type == "array" && attr.Items != nil {
		if attr.Items.Type == "object" {
			return fmt.Sprintf("[]%s", attr.Items.StructName)
		}
		if attr.Items.Enum != "" {
			return fmt.Sprintf("[]%s", attr.Items.Enum)
		}
		return fmt.Sprintf("[]%s", attr.Items.Type)
	}
	return attr.Type
}

// formatParamType renders a parameter type string for documentation.
func formatParamType(p specparse.RawParameterDef) string {
	if p.Enum != "" {
		return fmt.Sprintf("%s (%s)", p.Type, p.Enum)
	}
	return p.Type
}
