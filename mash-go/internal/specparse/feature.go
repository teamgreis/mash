// Package specparse provides shared YAML parsing types and functions for
// MASH protocol specification files. Both mash-featgen and mash-docgen
// import this package.
package specparse

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RawFeatureDef represents a feature definition loaded from YAML.
type RawFeatureDef struct {
	Name        string            `yaml:"name"`
	ID          uint8             `yaml:"id"`
	Revision    uint16            `yaml:"revision"`
	Mandatory   bool              `yaml:"mandatory"`
	Description string            `yaml:"description"`
	Enums       []RawEnumDef      `yaml:"enums"`
	Attributes  []RawAttributeDef `yaml:"attributes"`
	Commands    []RawCommandDef   `yaml:"commands"`
}

// RawEnumDef represents an enum type definition.
type RawEnumDef struct {
	Name        string         `yaml:"name"`
	Type        string         `yaml:"type"` // "uint8"
	Description string         `yaml:"description"`
	Values      []RawEnumValue `yaml:"values"`
}

// RawEnumValue represents a single enum value.
type RawEnumValue struct {
	Name        string `yaml:"name"`
	Value       int    `yaml:"value"`
	Description string `yaml:"description"`
}

// RawAttributeDef represents an attribute definition.
type RawAttributeDef struct {
	ID           uint16           `yaml:"id"`
	Name         string           `yaml:"name"`
	Type         string           `yaml:"type"`         // "uint8", "int64", "map", "string", "bool", "array", etc.
	Enum         string           `yaml:"enum"`         // Optional: references enum name
	MapKeyType   string           `yaml:"mapKeyType"`   // For map types: "Phase", "PhasePair"
	MapValueType string           `yaml:"mapValueType"` // For map types: "int64", "uint32"
	Items        *RawArrayItemDef `yaml:"items"`        // For typed array attributes
	Access       string           `yaml:"access"`       // "readOnly", "readWrite"
	Mandatory    bool             `yaml:"mandatory"`
	Nullable     bool             `yaml:"nullable"`
	Default      any              `yaml:"default"`
	Min          any              `yaml:"min"`
	Max          any              `yaml:"max"`
	Unit         string           `yaml:"unit"`
	Description  string           `yaml:"description"`
}

// RawArrayItemDef describes the items of a typed array attribute.
type RawArrayItemDef struct {
	Type       string             `yaml:"type"`       // "uint8", "string", "object"
	Enum       string             `yaml:"enum"`       // for enum arrays
	StructName string             `yaml:"structName"` // for object arrays
	Fields     []RawArrayFieldDef `yaml:"fields"`     // for object arrays
}

// RawArrayFieldDef describes a field within an object array item.
type RawArrayFieldDef struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	Enum string `yaml:"enum"`
}

// RawCommandDef represents a command definition.
type RawCommandDef struct {
	ID          uint8             `yaml:"id"`
	Name        string            `yaml:"name"`
	Mandatory   bool              `yaml:"mandatory"`
	Description string            `yaml:"description"`
	Parameters  []RawParameterDef `yaml:"parameters"`
	Response    []RawParameterDef `yaml:"response"`
}

// RawParameterDef represents a command parameter or response field.
//
// ID is the integer key used in the on-the-wire CBOR map per
// docs/interaction-model.md §7.1 / §7.2 (and the protocol-wide §2.2
// integer-key design). YAML must declare ID explicitly per parameter
// and per response field; codegen relies on it for the CBOR (en|de)code
// path. Name is kept for human-readable test fixtures and
// documentation; it is NOT a wire-level identifier.
type RawParameterDef struct {
	ID          uint8  `yaml:"id"`
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Enum        string `yaml:"enum"`
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
}

// ParseFeatureDef parses a feature definition from YAML bytes.
func ParseFeatureDef(data []byte) (*RawFeatureDef, error) {
	var def RawFeatureDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parsing feature def: %w", err)
	}
	if def.Name == "" {
		return nil, fmt.Errorf("feature definition missing name")
	}
	return &def, nil
}

// LoadFeatureDef loads and parses a feature definition from a file.
func LoadFeatureDef(path string) (*RawFeatureDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseFeatureDef(data)
}
