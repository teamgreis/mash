package main

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/mash-protocol/mash-go/internal/specparse"
)

// funcMap provides helper functions available to all templates.
var funcMap = template.FuncMap{
	"concat":             func(a, b string) string { return a + b },
	"firstLower":         firstLower,
	"enumValueSuffix":    enumValueSuffix,
	"goTitleCase":        goTitleCase,
	"goTypeName":         goTypeName,
	"modelDataType":      modelDataType,
	"accessConst":        accessConst,
	"commandFieldType":   commandFieldType,
	"commandHandlerType": commandHandlerType,
	"hasParameters":      hasParameters,
	"isSimpleResponse":   isSimpleResponse,
	"toEndpointGoName":   toEndpointGoName,
	"hexByte":            func(v int) string { return fmt.Sprintf("0x%02X", v) },
	"quote":              func(s string) string { return fmt.Sprintf("%q", s) },
	"recv":               func(name string) string { return strings.ToLower(name[:1]) },
}

// templates holds all parsed code generation templates.
var templates = template.Must(template.New("").Funcs(funcMap).Parse(
	enumsTmpl +
		constantsTmpl +
		featureStructTmpl +
		arrayStructsTmpl +
		commandConstantsTmpl +
		commandStructsTmpl +
		callbackSettersTmpl +
		constructorTmpl +
		addCommandsTmpl +
		featureTypesTmpl +
		endpointTypesTmpl,
))

// renderTemplate executes a named template into the builder.
func renderTemplate(b *strings.Builder, name string, data any) {
	if err := templates.ExecuteTemplate(b, name, data); err != nil {
		panic(fmt.Sprintf("template %s: %v", name, err))
	}
}

// --- Template data types ---

// constructorData holds pre-computed data for the constructor template.
type constructorData struct {
	Name             string
	FeatureTypeConst string
	Attributes       []constructorAttrData
	HasCommands      bool
}

type constructorAttrData struct {
	ConstName   string
	Name        string
	DataType    string
	Access      string
	Nullable    bool
	DefaultExpr string
	MinExpr     string
	MaxExpr     string
	Unit        string
	Description string
}

// enumsData holds data for the enums template.
type enumsData struct {
	Prefix string
	Enums  []specparse.RawEnumDef
}

// --- Template definitions ---

const enumsTmpl = `{{define "enums"}}
{{- range .Enums -}}
{{- $typeName := concat $.Prefix .Name}}
// {{$typeName}} represents {{firstLower .Description}}.
type {{$typeName}} {{.Type}}

const (
{{- range .Values}}
{{- $constName := concat $typeName (enumValueSuffix .Name)}}
{{- if .Description}}
// {{$constName}} {{firstLower .Description}}.
{{- end}}
{{$constName}} {{$typeName}} = {{hexByte .Value}}
{{- end}}
)

// String returns the {{firstLower $typeName}} name.
func (v {{$typeName}}) String() string {
switch v {
{{- range .Values}}
case {{concat $typeName (enumValueSuffix .Name)}}:
return {{quote .Name}}
{{- end}}
default:
return "UNKNOWN"
}
}

{{end}}
{{- end}}`

const constantsTmpl = `{{define "constants"}}
{{- if .Attributes}}
// {{.Name}} attribute IDs.
const (
{{- range .Attributes}}
{{concat $.Name (concat "Attr" (goTitleCase .Name))}} uint16 = {{.ID}}
{{- end}}
)
{{end}}

// {{.Name}}FeatureRevision is the current revision of the {{.Name}} feature.
const {{.Name}}FeatureRevision uint16 = {{.Revision}}

{{end}}`

const featureStructTmpl = `{{define "featureStruct"}}
{{- $recv := recv .Name}}
// {{.Name}} wraps a Feature with {{.Name}}-specific functionality.
{{- if .Description}}
// {{.Description}}
{{- end}}
type {{.Name}} struct {
*model.Feature
{{- range .Commands}}
{{concat "on" (goTitleCase .Name)}} {{commandHandlerType .}}
{{- end}}
}

{{end}}`

const arrayStructsTmpl = `{{define "arrayStructs"}}
{{- range .Attributes}}
{{- if and (eq .Type "array") .Items}}
{{- if eq .Items.Type "object"}}
// {{.Items.StructName}} represents an item in the {{.Name}} array.
type {{.Items.StructName}} struct {
{{- range .Items.Fields}}
{{- if .Enum}}
{{goTitleCase .Name}} {{.Enum}}
{{- else}}
{{goTitleCase .Name}} {{goTypeName .Type}}
{{- end}}
{{- end}}
}

{{end}}
{{- end}}
{{- end}}
{{- end}}`

const commandConstantsTmpl = `{{define "commandConstants"}}
// {{.Name}} command IDs.
const (
{{- range .Commands}}
{{concat $.Name (concat "Cmd" (goTitleCase .Name))}} uint8 = {{.ID}}
{{- end}}
)

{{range .Commands}}
{{- $cmd := goTitleCase .Name}}
{{- if .Parameters}}
// {{$cmd}} command parameter IDs (CBOR integer keys per
// docs/interaction-model.md §7.1).
const (
{{- range .Parameters}}
{{$cmd}}Param{{goTitleCase .Name}} uint8 = {{.ID}}
{{- end}}
)

// {{$cmd}}ParamNameToID maps human-readable parameter names (used in
// YAML test fixtures) to their wire IDs. Generated for the test harness;
// production code should reference the per-parameter constants directly.
var {{$cmd}}ParamNameToID = map[string]uint8{
{{- range .Parameters}}
{{quote .Name}}: {{$cmd}}Param{{goTitleCase .Name}},
{{- end}}
}

{{end}}
{{- if .Response}}
// {{$cmd}} command response field IDs (CBOR integer keys per
// docs/interaction-model.md §7.2).
const (
{{- range .Response}}
{{$cmd}}Resp{{goTitleCase .Name}} uint8 = {{.ID}}
{{- end}}
)

// {{$cmd}}RespNameToID maps response field names to wire IDs.
var {{$cmd}}RespNameToID = map[string]uint8{
{{- range .Response}}
{{quote .Name}}: {{$cmd}}Resp{{goTitleCase .Name}},
{{- end}}
}

{{end}}
{{- end}}
{{end}}`

const commandStructsTmpl = `{{define "commandStructs"}}
{{- range .Commands}}
{{- if hasParameters .}}
// {{goTitleCase .Name}}Request represents the {{.Name}} command parameters.
type {{goTitleCase .Name}}Request struct {
{{- range .Parameters}}
{{goTitleCase .Name}} {{commandFieldType .}}
{{- end}}
}

{{end}}
{{- if not (isSimpleResponse .)}}
// {{goTitleCase .Name}}Response represents the {{.Name}} command response.
type {{goTitleCase .Name}}Response struct {
{{- range .Response}}
{{goTitleCase .Name}} {{commandFieldType .}}
{{- end}}
}

{{end}}
{{- end}}
{{- end}}`

const callbackSettersTmpl = `{{define "callbackSetters"}}
{{- $name := .Name}}
{{- $recv := recv .Name}}
{{- range .Commands}}
// On{{goTitleCase .Name}} sets the handler for {{.Name}} command.
func ({{$recv}} *{{$name}}) On{{goTitleCase .Name}}(handler {{commandHandlerType .}}) {
{{$recv}}.on{{goTitleCase .Name}} = handler
}

{{end}}
{{- end}}`

const constructorTmpl = `{{define "constructor"}}
{{- $name := .Name}}
{{- $recv := recv .Name}}
// New{{$name}} creates a new {{$name}} feature.
func New{{$name}}() *{{$name}} {
f := model.NewFeature({{.FeatureTypeConst}}, {{$name}}FeatureRevision)

{{range .Attributes -}}
f.AddAttribute(model.NewAttribute(&model.AttributeMetadata{
ID: {{.ConstName}},
Name: {{quote .Name}},
Type: {{.DataType}},
Access: {{.Access}},
{{- if .Nullable}}
Nullable: true,
{{- end}}
{{- if .DefaultExpr}}
Default: {{.DefaultExpr}},
{{- end}}
{{- if .MinExpr}}
MinValue: {{.MinExpr}},
{{- end}}
{{- if .MaxExpr}}
MaxValue: {{.MaxExpr}},
{{- end}}
{{- if .Unit}}
Unit: {{quote .Unit}},
{{- end}}
{{- if .Description}}
Description: {{quote .Description}},
{{- end}}
}))

{{end}}
{{- if .HasCommands}}
{{$recv}} := &{{$name}}{Feature: f}
{{$recv}}.addCommands()

return {{$recv}}
{{- else}}
return &{{$name}}{Feature: f}
{{- end}}
}

{{end}}`

const addCommandsTmpl = `{{define "addCommands"}}
{{- $name := .Name}}
{{- $recv := recv .Name}}
// addCommands adds the {{$name}} commands.
func ({{$recv}} *{{$name}}) addCommands() {
{{range .Commands -}}
{{$recv}}.AddCommand(model.NewCommand(&model.CommandMetadata{
ID: {{concat $name (concat "Cmd" (goTitleCase .Name))}},
Name: {{quote .Name}},
Description: {{quote .Description}},
{{- if .Parameters}}
Parameters: []model.ParameterMetadata{
{{- range .Parameters}}
{ID: {{.ID}}, Name: {{quote .Name}}, Type: {{modelDataType .Type}}, Required: {{.Required}}},
{{- end}}
},
{{- end}}
}, {{$recv}}.handle{{goTitleCase .Name}}))

{{end -}}
}

{{end}}`

// --- Model type templates ---

// modelTypeData holds data for model type generation templates.
type modelTypeData struct {
	Types []specparse.RawModelTypeDef
}

const featureTypesTmpl = `{{define "featureTypes"}}
// Code generated by mash-featgen. DO NOT EDIT.

package model

// FeatureType identifies the type of a feature.
type FeatureType uint8

{{- if .Types}}

const (
{{- range .Types}}
// Feature{{.Name}}: {{firstLower .Description}}.
Feature{{.Name}} FeatureType = {{hexByte .ID}}
{{- end}}
)

// String returns the feature type name.
func (f FeatureType) String() string {
switch f {
{{- range .Types}}
case Feature{{.Name}}:
return {{quote .Name}}
{{- end}}
default:
if f >= FeatureVendorBase {
return "Vendor"
}
return "Unknown"
}
}
{{- else}}
// String returns the feature type name.
func (f FeatureType) String() string {
return "Unknown"
}
{{- end}}

{{end}}`

const endpointTypesTmpl = `{{define "endpointTypes"}}
// Code generated by mash-featgen. DO NOT EDIT.

package model

// EndpointType identifies the type/purpose of an endpoint.
type EndpointType uint8

{{- if .Types}}

const (
{{- range .Types}}
// {{toEndpointGoName .Name}}: {{firstLower .Description}}.
{{toEndpointGoName .Name}} EndpointType = {{hexByte .ID}}
{{- end}}
)

// String returns the endpoint type name.
func (e EndpointType) String() string {
switch e {
{{- range .Types}}
case {{toEndpointGoName .Name}}:
return {{quote .Name}}
{{- end}}
default:
return "UNKNOWN"
}
}
{{- else}}
// String returns the endpoint type name.
func (e EndpointType) String() string {
return "UNKNOWN"
}
{{- end}}

{{end}}`
