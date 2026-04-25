package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mash-protocol/mash-go/internal/specparse"
	"github.com/mash-protocol/mash-go/pkg/usecase"
)

// GenerateFeaturePage produces the Markdown content for a feature reference page.
func GenerateFeaturePage(def *specparse.RawFeatureDef, m *DocModel) string {
	var b strings.Builder

	writeFeatureHeader(&b, def, m)
	writeFeatureAttributes(&b, def)
	writeFeatureEnums(&b, def)

	// Embed state machine diagrams for EnergyControl
	if def.Name == "EnergyControl" {
		b.WriteString("## State Machines\n\n")
		b.WriteString("### ControlStateEnum\n\n")
		b.WriteString(ControlStateDiagram())
		b.WriteString("\n### ProcessStateEnum\n\n")
		b.WriteString(ProcessStateDiagram())
		b.WriteString("\n")
	}

	writeFeatureCommands(&b, def)
	writeFeatureBacklinks(&b, def, m)

	return b.String()
}

func writeFeatureHeader(b *strings.Builder, def *specparse.RawFeatureDef, m *DocModel) {
	fmt.Fprintf(b, "# %s\n\n", def.Name)

	if def.Description != "" {
		fmt.Fprintf(b, "> %s\n\n", strings.TrimSpace(def.Description))
	}

	fmt.Fprintf(b, "| | |\n|---|---|\n")
	fmt.Fprintf(b, "| **ID** | %s |\n", hexByte(int(def.ID)))
	fmt.Fprintf(b, "| **Revision** | %d |\n", def.Revision)
	fmt.Fprintf(b, "| **Mandatory** | %s |\n", yesNoFull(def.Mandatory))
	b.WriteString("\n")

	// Link to hand-written design doc
	slug := featureSlug(def.Name)
	fmt.Fprintf(b, "> See also: [%s Design](../../features/%s.md)\n\n", def.Name, slug)
}

func writeFeatureAttributes(b *strings.Builder, def *specparse.RawFeatureDef) {
	if len(def.Attributes) == 0 {
		return
	}

	b.WriteString("## Attributes\n\n")
	b.WriteString("| ID | Name | Type | Access | Mandatory | Nullable | Unit | Description |\n")
	b.WriteString("|---:|------|------|--------|:---------:|:--------:|------|-------------|\n")

	for _, attr := range def.Attributes {
		fmt.Fprintf(b, "| %d | `%s` | `%s` | %s | %s | %s | %s | %s |\n",
			attr.ID,
			attr.Name,
			formatAttrType(attr),
			attr.Access,
			yesNo(attr.Mandatory),
			yesNo(attr.Nullable),
			attr.Unit,
			attr.Description,
		)
	}
	b.WriteString("\n")
}

func writeFeatureEnums(b *strings.Builder, def *specparse.RawFeatureDef) {
	if len(def.Enums) == 0 {
		return
	}

	b.WriteString("## Enums\n\n")

	for _, enum := range def.Enums {
		fmt.Fprintf(b, "### %s\n\n", enum.Name)
		if enum.Description != "" {
			fmt.Fprintf(b, "%s\n\n", enum.Description)
		}
		fmt.Fprintf(b, "Type: `%s`\n\n", enum.Type)

		b.WriteString("| Value | Name | Description |\n")
		b.WriteString("|------:|------|-------------|\n")
		for _, val := range enum.Values {
			fmt.Fprintf(b, "| %s | `%s` | %s |\n",
				hexByte(val.Value),
				val.Name,
				val.Description,
			)
		}
		b.WriteString("\n")
	}
}

func writeFeatureCommands(b *strings.Builder, def *specparse.RawFeatureDef) {
	if len(def.Commands) == 0 {
		return
	}

	b.WriteString("## Commands\n\n")

	for _, cmd := range def.Commands {
		fmt.Fprintf(b, "### `%s`\n\n", cmd.Name)
		if cmd.Description != "" {
			fmt.Fprintf(b, "%s\n\n", cmd.Description)
		}
		fmt.Fprintf(b, "- **ID**: %d\n", cmd.ID)
		fmt.Fprintf(b, "- **Mandatory**: %s\n\n", yesNoFull(cmd.Mandatory))

		if len(cmd.Parameters) > 0 {
			b.WriteString("**Parameters:**\n\n")
			b.WriteString("| Name | Type | Required | Description |\n")
			b.WriteString("|------|------|:--------:|-------------|\n")
			for _, p := range cmd.Parameters {
				fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n",
					p.Name,
					formatParamType(p),
					yesNo(p.Required),
					p.Description,
				)
			}
			b.WriteString("\n")
		}

		if len(cmd.Response) > 0 {
			b.WriteString("**Response:**\n\n")
			b.WriteString("| Name | Type | Required | Description |\n")
			b.WriteString("|------|------|:--------:|-------------|\n")
			for _, r := range cmd.Response {
				fmt.Fprintf(b, "| `%s` | `%s` | %s | %s |\n",
					r.Name,
					formatParamType(r),
					yesNo(r.Required),
					r.Description,
				)
			}
			b.WriteString("\n")
		}
	}
}

func writeFeatureBacklinks(b *strings.Builder, def *specparse.RawFeatureDef, m *DocModel) {
	refs, ok := m.FeatureUseCaseRefs[def.Name]
	if !ok || len(refs) == 0 {
		return
	}

	b.WriteString("---\n\n")
	b.WriteString("**Referenced by:** ")
	links := make([]string, len(refs))
	for i, ucName := range refs {
		links[i] = fmt.Sprintf("[%s](../usecases/%s.md)", ucName, usecaseSlug(ucName))
	}
	b.WriteString(strings.Join(links, ", "))
	b.WriteString("\n")
}

// generateAll writes all generated Markdown pages to outputDir.
func generateAll(m *DocModel, outputDir string) error {
	if err := generateAllFeaturePages(m, outputDir); err != nil {
		return err
	}
	if err := generateAllUseCasePages(m, outputDir); err != nil {
		return err
	}
	if err := generateAllEndpointPages(m, outputDir); err != nil {
		return err
	}
	if err := generateAllIndexPages(m, outputDir); err != nil {
		return err
	}
	return nil
}

// generateAllFeaturePages writes all feature Markdown pages to outputDir/features/.
func generateAllFeaturePages(m *DocModel, outputDir string) error {
	featDir := filepath.Join(outputDir, "features")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		return fmt.Errorf("creating features dir: %w", err)
	}

	for _, def := range userFacingFeatures(m.Features) {
		content := GenerateFeaturePage(def, m)
		slug := featureSlug(def.Name)
		path := filepath.Join(featDir, slug+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", slug, err)
		}
	}
	return nil
}

// generateAllUseCasePages writes all use case Markdown pages to outputDir/usecases/.
func generateAllUseCasePages(m *DocModel, outputDir string) error {
	ucDir := filepath.Join(outputDir, "usecases")
	if err := os.MkdirAll(ucDir, 0o755); err != nil {
		return fmt.Errorf("creating usecases dir: %w", err)
	}

	for _, uc := range m.UseCases {
		content := GenerateUseCasePage(uc, m)
		slug := usecaseSlug(uc.Name)
		path := filepath.Join(ucDir, slug+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", slug, err)
		}
	}
	return nil
}

// GenerateUseCasePage produces the Markdown content for a use case reference page.
func GenerateUseCasePage(uc *usecase.RawUseCaseDef, m *DocModel) string {
	var b strings.Builder

	writeUseCaseHeader(&b, uc)
	writeUseCaseEndpointTypes(&b, uc)

	// Scenario map diagram (before detailed breakdown)
	if len(uc.Scenarios) > 0 {
		b.WriteString("## Scenario Map\n\n")
		b.WriteString(ScenarioMapDiagram(uc))
	}

	writeUseCaseScenarioMatrix(&b, uc)
	writeUseCaseScenarios(&b, uc, m)

	return b.String()
}

func writeUseCaseHeader(b *strings.Builder, uc *usecase.RawUseCaseDef) {
	fmt.Fprintf(b, "# %s -- %s\n\n", uc.Name, uc.FullName)

	if uc.Description != "" {
		fmt.Fprintf(b, "> %s\n\n", strings.TrimSpace(uc.Description))
	}

	fmt.Fprintf(b, "| | |\n|---|---|\n")
	fmt.Fprintf(b, "| **ID** | %s |\n", hexByte(int(uc.ID)))
	fmt.Fprintf(b, "| **Version** | %d.%d |\n", uc.Major, uc.Minor)
	b.WriteString("\n")
}

func writeUseCaseEndpointTypes(b *strings.Builder, uc *usecase.RawUseCaseDef) {
	if len(uc.EndpointTypes) == 0 {
		return
	}

	b.WriteString("## Endpoint Types\n\n")
	for _, et := range uc.EndpointTypes {
		fmt.Fprintf(b, "- [%s](../endpoints/%s.md)\n", et, endpointSlug(et))
	}
	b.WriteString("\n")
}

func writeUseCaseScenarioMatrix(b *strings.Builder, uc *usecase.RawUseCaseDef) {
	if len(uc.Scenarios) == 0 || len(uc.EndpointTypes) == 0 {
		return
	}

	b.WriteString("## Scenario / Endpoint Type Matrix\n\n")

	// Header row
	b.WriteString("| Endpoint Type |")
	for _, sc := range uc.Scenarios {
		fmt.Fprintf(b, " %s |", sc.Name)
	}
	b.WriteString("\n")

	// Separator
	b.WriteString("|---|")
	for range uc.Scenarios {
		b.WriteString(":---:|")
	}
	b.WriteString("\n")

	// One row per endpoint type
	for _, et := range uc.EndpointTypes {
		fmt.Fprintf(b, "| %s |", et)
		for _, sc := range uc.Scenarios {
			if scenarioAppliesToEndpoint(sc, et) {
				b.WriteString(" x |")
			} else {
				b.WriteString(" - |")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// scenarioAppliesToEndpoint reports whether a scenario applies to the given
// endpoint type. If the scenario defines its own EndpointTypes list, only those
// are applicable; otherwise it inherits all top-level endpoint types.
func scenarioAppliesToEndpoint(sc usecase.RawScenarioDef, et string) bool {
	if len(sc.EndpointTypes) == 0 {
		return true
	}
	for _, scET := range sc.EndpointTypes {
		if scET == et {
			return true
		}
	}
	return false
}

func writeUseCaseScenarios(b *strings.Builder, uc *usecase.RawUseCaseDef, m *DocModel) {
	if len(uc.Scenarios) == 0 {
		return
	}

	b.WriteString("## Scenarios\n\n")

	for _, sc := range uc.Scenarios {
		fmt.Fprintf(b, "### Bit %d: %s\n\n", sc.Bit, sc.Name)
		if sc.Description != "" {
			fmt.Fprintf(b, "%s\n\n", sc.Description)
		}

		// Scenario dependencies
		if len(sc.Requires) > 0 {
			fmt.Fprintf(b, "**Requires:** %s\n\n", strings.Join(sc.Requires, ", "))
		}
		if len(sc.RequiresAny) > 0 {
			fmt.Fprintf(b, "**Requires any of:** %s\n\n", strings.Join(sc.RequiresAny, ", "))
		}

		// Per-scenario endpoint type restrictions
		if len(sc.EndpointTypes) > 0 {
			fmt.Fprintf(b, "**Endpoint types:** %s\n\n", strings.Join(sc.EndpointTypes, ", "))
		}

		for _, fr := range sc.Features {
			reqLabel := effectiveFeatureLabel(fr, sc, uc.Scenarios)
			fmt.Fprintf(b, "#### %s (%s)\n\n", fr.Feature, reqLabel)

			writeScenarioFeatureAttributes(b, fr, m)

			if len(fr.Commands) > 0 {
				b.WriteString("**Commands:** ")
				cmds := make([]string, len(fr.Commands))
				for i, c := range fr.Commands {
					cmds[i] = fmt.Sprintf("`%s`", c)
				}
				b.WriteString(strings.Join(cmds, ", "))
				b.WriteString("\n\n")
			}

			if fr.Subscribe != "" {
				fmt.Fprintf(b, "**Subscribe:** %s\n\n", fr.Subscribe)
			}
		}
	}
}

// --- Index pages ---

// effectiveFeatureLabel returns the requirement label for a feature within a
// scenario by resolving the requires dependency chain. If the feature is not
// directly required but a scenario in the requires list marks it as required,
// the label reflects the transitive dependency (e.g. "required via BASE").
func effectiveFeatureLabel(fr usecase.RawFeatureReq, sc usecase.RawScenarioDef, scenarios []usecase.RawScenarioDef) string {
	if fr.Required {
		return "required"
	}

	// Build scenario lookup for dependency resolution.
	byName := make(map[string]*usecase.RawScenarioDef, len(scenarios))
	for i := range scenarios {
		byName[scenarios[i].Name] = &scenarios[i]
	}

	// Walk the requires chain (breadth-first) looking for a scenario
	// that marks this feature as required.
	visited := make(map[string]bool)
	queue := append([]string{}, sc.Requires...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true

		dep, ok := byName[name]
		if !ok {
			continue
		}
		for _, depFr := range dep.Features {
			if depFr.Feature == fr.Feature && depFr.Required {
				return fmt.Sprintf("required via %s", name)
			}
		}
		queue = append(queue, dep.Requires...)
	}

	return "optional"
}

// writeScenarioFeatureAttributes writes a merged attribute table for a feature
// within a scenario. It combines the feature's mandatory attributes (from the
// feature YAML) with the scenario-specific attribute constraints (from the use
// case YAML), so the reader sees the complete picture without cross-referencing.
func writeScenarioFeatureAttributes(b *strings.Builder, fr usecase.RawFeatureReq, m *DocModel) {
	// Build lookup of scenario-specific attributes.
	scenarioAttrs := make(map[string]*usecase.RawAttrReq, len(fr.Attributes))
	for i := range fr.Attributes {
		scenarioAttrs[fr.Attributes[i].Name] = &fr.Attributes[i]
	}

	type attrRow struct {
		name       string
		constraint string
	}
	var rows []attrRow
	shown := make(map[string]bool)

	// Feature-mandatory attributes first (in feature-defined order).
	if featureDef, ok := m.FeatureByName[fr.Feature]; ok {
		for _, attr := range featureDef.Attributes {
			if !attr.Mandatory {
				continue
			}
			if sc, ok := scenarioAttrs[attr.Name]; ok {
				// Present in both: use the scenario constraint.
				constraint := ""
				if sc.RequiredValue != nil {
					constraint = fmt.Sprintf("must be `%v`", *sc.RequiredValue)
				}
				rows = append(rows, attrRow{attr.Name, constraint})
			} else {
				// Feature-mandatory only: mark so the reader knows it exists.
				rows = append(rows, attrRow{attr.Name, "*(always present)*"})
			}
			shown[attr.Name] = true
		}
	}

	// Scenario-specific attributes that are not feature-mandatory.
	for _, sa := range fr.Attributes {
		if shown[sa.Name] {
			continue
		}
		constraint := ""
		if sa.RequiredValue != nil {
			constraint = fmt.Sprintf("must be `%v`", *sa.RequiredValue)
		}
		rows = append(rows, attrRow{sa.Name, constraint})
	}

	if len(rows) == 0 {
		return
	}

	b.WriteString("| Attribute | Constraint |\n")
	b.WriteString("|-----------|------------|\n")
	for _, r := range rows {
		fmt.Fprintf(b, "| `%s` | %s |\n", r.name, r.constraint)
	}
	b.WriteString("\n")
}

// GenerateFeatureIndexPage produces the feature registry index page.
func GenerateFeatureIndexPage(m *DocModel) string {
	var b strings.Builder

	b.WriteString("# Feature Reference\n\n")
	b.WriteString("| ID | Name | Description |\n")
	b.WriteString("|---:|------|-------------|\n")

	for _, def := range userFacingFeatures(m.Features) {
		slug := featureSlug(def.Name)
		fmt.Fprintf(&b, "| %s | [%s](%s.md) | %s |\n",
			hexByte(int(def.ID)), def.Name, slug, strings.TrimSpace(def.Description))
	}
	b.WriteString("\n")

	return b.String()
}

// GenerateUseCaseIndexPage produces the use case registry index page.
func GenerateUseCaseIndexPage(m *DocModel) string {
	var b strings.Builder

	b.WriteString("# Use Case Reference\n\n")
	b.WriteString("| ID | Name | Full Name | Endpoint Types |\n")
	b.WriteString("|---:|------|-----------|----------------|\n")

	for _, uc := range m.UseCases {
		slug := usecaseSlug(uc.Name)
		endpoints := strings.Join(uc.EndpointTypes, ", ")
		fmt.Fprintf(&b, "| %s | [%s](%s.md) | %s | %s |\n",
			hexByte(int(uc.ID)), uc.Name, slug, uc.FullName, endpoints)
	}
	b.WriteString("\n")

	return b.String()
}

// GenerateEndpointIndexPage produces the endpoint type registry index page.
func GenerateEndpointIndexPage(m *DocModel) string {
	var b strings.Builder

	b.WriteString("# Endpoint Types\n\n")
	b.WriteString("| ID | Name | Description |\n")
	b.WriteString("|---:|------|-------------|\n")

	for _, et := range m.EndpointTypes {
		slug := endpointSlug(et.Name)
		fmt.Fprintf(&b, "| %s | [%s](%s.md) | %s |\n",
			hexByte(et.ID), et.Name, slug, et.Description)
	}
	b.WriteString("\n")

	return b.String()
}

// GenerateReferenceIndexPage produces the top-level reference index page.
func GenerateReferenceIndexPage(m *DocModel) string {
	var b strings.Builder

	b.WriteString("# MASH Protocol Reference\n\n")
	fmt.Fprintf(&b, "Protocol version: %s\n\n", m.Version)

	b.WriteString("## Sections\n\n")
	b.WriteString("- [Feature Reference](features/index.md)\n")
	b.WriteString("- [Use Case Reference](usecases/index.md)\n")
	b.WriteString("- [Endpoint Types](endpoints/index.md)\n")
	b.WriteString("\n")

	b.WriteString("## Feature / Use Case Cross-Reference\n\n")
	b.WriteString(FeatureCrossRefDiagram(m))
	b.WriteString("\n")

	return b.String()
}

// --- Endpoint pages ---

// GenerateEndpointPage produces the Markdown content for an endpoint type page.
func GenerateEndpointPage(epName string, m *DocModel) string {
	var b strings.Builder

	// Find description from EndpointTypes
	var description string
	for _, et := range m.EndpointTypes {
		if et.Name == epName {
			description = et.Description
			break
		}
	}

	fmt.Fprintf(&b, "# %s\n\n", epName)
	if description != "" {
		fmt.Fprintf(&b, "> %s\n\n", description)
	}

	// Applicable use cases
	writeEndpointUseCases(&b, epName, m)

	// Conformance
	writeEndpointConformance(&b, epName, m)

	// Device composition diagram
	b.WriteString(DeviceCompositionDiagram(epName, m))

	return b.String()
}

func writeEndpointUseCases(b *strings.Builder, epName string, m *DocModel) {
	var applicable []string
	for _, uc := range m.UseCases {
		for _, et := range uc.EndpointTypes {
			if et == epName {
				applicable = append(applicable, uc.Name)
				break
			}
		}
	}

	if len(applicable) == 0 {
		return
	}

	b.WriteString("## Applicable Use Cases\n\n")
	for _, ucName := range applicable {
		fmt.Fprintf(b, "- [%s](../usecases/%s.md)\n", ucName, usecaseSlug(ucName))
	}
	b.WriteString("\n")
}

func writeEndpointConformance(b *strings.Builder, epName string, m *DocModel) {
	if m.Conformance == nil {
		return
	}

	features, ok := m.Conformance.EndpointTypes[epName]
	if !ok || len(features) == 0 {
		return
	}

	b.WriteString("## Feature Conformance\n\n")

	for featureName, conf := range features {
		fmt.Fprintf(b, "### %s\n\n", featureName)

		if len(conf.Mandatory) > 0 {
			b.WriteString("**Mandatory attributes:**\n\n")
			for _, attr := range conf.Mandatory {
				fmt.Fprintf(b, "- `%s`\n", attr)
			}
			b.WriteString("\n")
		}

		if len(conf.Recommended) > 0 {
			b.WriteString("**Recommended attributes:**\n\n")
			for _, attr := range conf.Recommended {
				fmt.Fprintf(b, "- `%s`\n", attr)
			}
			b.WriteString("\n")
		}
	}
}

// --- Page generation helpers ---

func generateAllEndpointPages(m *DocModel, outputDir string) error {
	epDir := filepath.Join(outputDir, "endpoints")
	if err := os.MkdirAll(epDir, 0o755); err != nil {
		return fmt.Errorf("creating endpoints dir: %w", err)
	}

	for _, et := range m.EndpointTypes {
		content := GenerateEndpointPage(et.Name, m)
		slug := endpointSlug(et.Name)
		path := filepath.Join(epDir, slug+".md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", slug, err)
		}
	}
	return nil
}

func generateAllIndexPages(m *DocModel, outputDir string) error {
	// Feature index
	featIndexPath := filepath.Join(outputDir, "features", "index.md")
	if err := os.WriteFile(featIndexPath, []byte(GenerateFeatureIndexPage(m)), 0o644); err != nil {
		return fmt.Errorf("writing feature index: %w", err)
	}

	// Use case index
	ucIndexPath := filepath.Join(outputDir, "usecases", "index.md")
	if err := os.WriteFile(ucIndexPath, []byte(GenerateUseCaseIndexPage(m)), 0o644); err != nil {
		return fmt.Errorf("writing use case index: %w", err)
	}

	// Endpoint index
	epIndexPath := filepath.Join(outputDir, "endpoints", "index.md")
	if err := os.WriteFile(epIndexPath, []byte(GenerateEndpointIndexPage(m)), 0o644); err != nil {
		return fmt.Errorf("writing endpoint index: %w", err)
	}

	// Reference index
	refIndexPath := filepath.Join(outputDir, "index.md")
	if err := os.WriteFile(refIndexPath, []byte(GenerateReferenceIndexPage(m)), 0o644); err != nil {
		return fmt.Errorf("writing reference index: %w", err)
	}

	return nil
}

// yesNoFull returns "Yes" or "No".
func yesNoFull(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}
