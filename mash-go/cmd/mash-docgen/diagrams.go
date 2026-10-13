package main

import (
	"fmt"
	"strings"

	"github.com/mash-protocol/mash-go/pkg/usecase"
)

const mermaidFence = "```"

// ControlStateDiagram returns a fenced Mermaid stateDiagram-v2 for ControlStateEnum.
func ControlStateDiagram() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%smermaid\n", mermaidFence)
	b.WriteString("stateDiagram-v2\n")
	b.WriteString("    [*] --> AUTONOMOUS\n")
	b.WriteString("    AUTONOMOUS --> CONTROLLED : controller connects\n")
	b.WriteString("    CONTROLLED --> LIMITED : SetLimit()\n")
	b.WriteString("    LIMITED --> CONTROLLED : ClearLimit() / expires\n")
	b.WriteString("    CONTROLLED --> FAILSAFE : connection lost\n")
	b.WriteString("    LIMITED --> FAILSAFE : connection lost\n")
	b.WriteString("    FAILSAFE --> AUTONOMOUS : failsafeDuration expired\n")
	b.WriteString("    LIMITED --> OVERRIDE : self-protection\n")
	b.WriteString("    OVERRIDE --> LIMITED : condition cleared\n")
	fmt.Fprintf(&b, "%s\n", mermaidFence)
	return b.String()
}

// ProcessStateDiagram returns a fenced Mermaid stateDiagram-v2 for ProcessStateEnum.
func ProcessStateDiagram() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%smermaid\n", mermaidFence)
	b.WriteString("stateDiagram-v2\n")
	b.WriteString("    [*] --> NONE\n")
	b.WriteString("    NONE --> AVAILABLE : task announced\n")
	b.WriteString("    AVAILABLE --> SCHEDULED : setSetpoint(cause=SCHEDULED)\n")
	b.WriteString("    SCHEDULED --> RUNNING : scheduled time reached\n")
	b.WriteString("    RUNNING --> PAUSED : Pause()\n")
	b.WriteString("    PAUSED --> RUNNING : Resume()\n")
	b.WriteString("    RUNNING --> COMPLETED : task finishes\n")
	b.WriteString("    RUNNING --> ABORTED : Stop()\n")
	b.WriteString("    PAUSED --> ABORTED : Stop()\n")
	b.WriteString("    SCHEDULED --> ABORTED : Stop()\n")
	b.WriteString("    COMPLETED --> NONE : reset\n")
	b.WriteString("    ABORTED --> NONE : reset\n")
	fmt.Fprintf(&b, "%s\n", mermaidFence)
	return b.String()
}

// DeviceCompositionDiagram returns a fenced Mermaid graph for an endpoint type
// showing its features and mandatory attributes (derived from conformance YAML).
func DeviceCompositionDiagram(epName string, m *DocModel) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%smermaid\ngraph TD\n", mermaidFence)
	epID := sanitizeMermaidID(epName)
	fmt.Fprintf(&b, "    %s[\"%s\"]\n", epID, epName)

	if m.Conformance != nil {
		if features, ok := m.Conformance.EndpointTypes[epName]; ok {
			// Sorted for deterministic output — Go map iteration order is
			// random, and this string lands verbatim in a generated page.
			for _, featureName := range sortedKeys(features) {
				conf := features[featureName]
				fID := sanitizeMermaidID(featureName)
				fmt.Fprintf(&b, "    %s[\"%s\"]\n", fID, featureName)
				fmt.Fprintf(&b, "    %s --> %s\n", epID, fID)

				for _, attr := range conf.Mandatory {
					aID := sanitizeMermaidID(featureName + "_" + attr)
					fmt.Fprintf(&b, "    %s[\"%s\"]\n", aID, attr)
					fmt.Fprintf(&b, "    %s --> %s\n", fID, aID)
				}
			}
		}
	}

	fmt.Fprintf(&b, "%s\n", mermaidFence)
	return b.String()
}

// FeatureCrossRefDiagram returns a fenced Mermaid graph showing
// which use cases reference which features.
func FeatureCrossRefDiagram(m *DocModel) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%smermaid\ngraph LR\n", mermaidFence)

	// Collect all referenced features and their use cases. Sorted for
	// deterministic output — Go map iteration order is random, and this
	// string lands verbatim in a generated page.
	for _, featureName := range sortedKeys(m.FeatureUseCaseRefs) {
		ucNames := m.FeatureUseCaseRefs[featureName]
		fID := sanitizeMermaidID("f_" + featureName)
		fmt.Fprintf(&b, "    %s[\"%s\"]\n", fID, featureName)

		for _, ucName := range ucNames {
			ucID := sanitizeMermaidID("uc_" + ucName)
			fmt.Fprintf(&b, "    %s[\"%s\"]\n", ucID, ucName)
			fmt.Fprintf(&b, "    %s --> %s\n", ucID, fID)
		}
	}

	fmt.Fprintf(&b, "%s\n", mermaidFence)
	return b.String()
}

// ScenarioMapDiagram returns a fenced Mermaid graph showing
// scenarios and their required features for a use case.
// Each scenario gets its own feature nodes so the hierarchy reads
// left-to-right: UseCase -> Scenario -> Features.
// Scenarios with restricted endpoint types are annotated.
func ScenarioMapDiagram(uc *usecase.RawUseCaseDef) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%smermaid\ngraph LR\n", mermaidFence)

	ucID := sanitizeMermaidID("uc_" + uc.Name)
	fmt.Fprintf(&b, "    %s[\"%s\"]\n", ucID, uc.Name)

	for _, sc := range uc.Scenarios {
		scID := sanitizeMermaidID("sc_" + uc.Name + "_" + sc.Name)

		// Annotate scenario node with endpoint type restriction if present.
		if len(sc.EndpointTypes) > 0 {
			etList := strings.Join(sc.EndpointTypes, ", ")
			fmt.Fprintf(&b, "    %s[\"%s<br/><small>%s</small>\"]\n", scID, sc.Name, etList)
		} else {
			fmt.Fprintf(&b, "    %s[\"%s\"]\n", scID, sc.Name)
		}
		fmt.Fprintf(&b, "    %s --> %s\n", ucID, scID)

		for _, fr := range sc.Features {
			// Unique ID per scenario so each scenario shows its own feature nodes.
			fID := sanitizeMermaidID("f_" + sc.Name + "_" + fr.Feature)
			fmt.Fprintf(&b, "    %s[\"%s\"]\n", fID, fr.Feature)
			fmt.Fprintf(&b, "    %s --> %s\n", scID, fID)
		}
	}

	fmt.Fprintf(&b, "%s\n", mermaidFence)
	return b.String()
}

// sanitizeMermaidID replaces characters that are invalid in Mermaid IDs.
func sanitizeMermaidID(s string) string {
	r := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
	)
	return r.Replace(s)
}
