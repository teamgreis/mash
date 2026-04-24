package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateMkDocsYAML produces the mkdocs.yml content for the project.
func GenerateMkDocsYAML(m *DocModel) string {
	var b strings.Builder

	// Site metadata
	b.WriteString("site_name: MASH Protocol\n")
	b.WriteString("site_description: Minimal Application-layer Smart Home Protocol\n\n")

	// Theme
	b.WriteString("theme:\n")
	b.WriteString("  name: material\n")
	b.WriteString("  palette:\n")
	b.WriteString("    - media: \"(prefers-color-scheme: light)\"\n")
	b.WriteString("      scheme: default\n")
	b.WriteString("      primary: white\n")
	b.WriteString("      accent: blue grey\n")
	b.WriteString("      toggle:\n")
	b.WriteString("        icon: material/brightness-7\n")
	b.WriteString("        name: Switch to dark mode\n")
	b.WriteString("    - media: \"(prefers-color-scheme: dark)\"\n")
	b.WriteString("      scheme: slate\n")
	b.WriteString("      primary: black\n")
	b.WriteString("      accent: blue grey\n")
	b.WriteString("      toggle:\n")
	b.WriteString("        icon: material/brightness-4\n")
	b.WriteString("        name: Switch to light mode\n")
	b.WriteString("  font:\n")
	b.WriteString("    text: Inter\n")
	b.WriteString("    code: JetBrains Mono\n")
	b.WriteString("  features:\n")
	b.WriteString("    - navigation.sections\n")
	b.WriteString("    - navigation.expand\n")
	b.WriteString("    - navigation.indexes\n")
	b.WriteString("    - navigation.top\n")
	b.WriteString("    - search.highlight\n")
	b.WriteString("    - search.suggest\n")
	b.WriteString("    - content.code.copy\n\n")

	// Docs directory
	b.WriteString("docs_dir: docs\n\n")

	// Extra CSS
	b.WriteString("extra_css:\n")
	b.WriteString("  - generated/stylesheets/extra.css\n\n")

	// Markdown extensions
	b.WriteString("markdown_extensions:\n")
	b.WriteString("  - pymdownx.superfences:\n")
	b.WriteString("      custom_fences:\n")
	b.WriteString("        - name: mermaid\n")
	b.WriteString("          class: mermaid\n")
	b.WriteString("          format: !!python/name:pymdownx.superfences.fence_code_format\n")
	b.WriteString("  - tables\n")
	b.WriteString("  - admonition\n")
	b.WriteString("  - pymdownx.details\n")
	b.WriteString("  - attr_list\n")
	b.WriteString("  - def_list\n")
	b.WriteString("  - toc:\n")
	b.WriteString("      permalink: true\n\n")

	// Navigation
	b.WriteString("nav:\n")
	b.WriteString("  - Home: index.md\n")
	writeNavProtocol(&b)
	writeNavGeneratedFeatures(&b, m)
	writeNavFeatureDesign(&b, m)
	writeNavGeneratedUseCases(&b, m)
	writeNavGeneratedEndpoints(&b, m)
	writeNavTesting(&b)
	writeNavReference(&b)

	return b.String()
}

func writeNavProtocol(b *strings.Builder) {
	b.WriteString("  - Protocol:\n")
	b.WriteString("      - Overview: protocol-overview.md\n")
	b.WriteString("      - Transport: transport.md\n")
	b.WriteString("      - Security: security.md\n")
	b.WriteString("      - Discovery: discovery.md\n")
	b.WriteString("      - Interaction Model: interaction-model.md\n")
	b.WriteString("      - Multi-Zone: multi-zone.md\n")
	b.WriteString("      - Stack Architecture: stack-architecture.md\n")
}

func writeNavGeneratedFeatures(b *strings.Builder, m *DocModel) {
	b.WriteString("  - Features:\n")
	b.WriteString("      - generated/features/index.md\n")
	for _, def := range m.Features {
		slug := featureSlug(def.Name)
		fmt.Fprintf(b, "      - %s: generated/features/%s.md\n", def.Name, slug)
	}
}

func writeNavFeatureDesign(b *strings.Builder, m *DocModel) {
	b.WriteString("  - Feature Design:\n")
	b.WriteString("      - Overview: features/README.md\n")
	for _, def := range m.Features {
		slug := featureSlug(def.Name)
		fmt.Fprintf(b, "      - %s: features/%s.md\n", def.Name, slug)
	}
}

func writeNavGeneratedUseCases(b *strings.Builder, m *DocModel) {
	b.WriteString("  - Use Cases:\n")
	b.WriteString("      - generated/usecases/index.md\n")
	for _, uc := range m.UseCases {
		slug := usecaseSlug(uc.Name)
		fmt.Fprintf(b, "      - %s: generated/usecases/%s.md\n", uc.Name, slug)
	}
}

func writeNavGeneratedEndpoints(b *strings.Builder, m *DocModel) {
	b.WriteString("  - Endpoint Types:\n")
	b.WriteString("      - generated/endpoints/index.md\n")
	for _, et := range m.EndpointTypes {
		slug := endpointSlug(et.Name)
		fmt.Fprintf(b, "      - %s: generated/endpoints/%s.md\n", et.Name, slug)
	}
}

func writeNavTesting(b *strings.Builder) {
	b.WriteString("  - Testing:\n")
	b.WriteString("      - Overview: testing/README.md\n")
	b.WriteString("      - Test Matrix: testing/test-matrix.md\n")
	b.WriteString("      - PICS Format: testing/pics-format.md\n")
	b.WriteString("      - Behavior Specs:\n")
	b.WriteString("          - Protocol Behaviors: testing/behavior/protocol-behaviors.md\n")
	b.WriteString("          - State Machines: testing/behavior/state-machines.md\n")
	b.WriteString("          - Connection: testing/behavior/connection-state-machine.md\n")
	b.WriteString("          - Commissioning: testing/behavior/commissioning-pase.md\n")
	b.WriteString("          - Discovery: testing/behavior/discovery.md\n")
	b.WriteString("          - Subscriptions: testing/behavior/subscription-semantics.md\n")
	b.WriteString("          - Multi-Zone Resolution: testing/behavior/multi-zone-resolution.md\n")
	b.WriteString("          - Failsafe Timing: testing/behavior/failsafe-timing.md\n")
	b.WriteString("          - Feature Interactions: testing/behavior/feature-interactions.md\n")
}

func writeNavReference(b *strings.Builder) {
	b.WriteString("  - Reference:\n")
	b.WriteString("      - Spec Conventions: spec-conventions.md\n")
	b.WriteString("      - Glossary: glossary.md\n")
	b.WriteString("      - Message Types: message-types.md\n")
	b.WriteString("      - Error Codes: error-codes.md\n")
	b.WriteString("      - Protocol Comparison: protocol-comparison.md\n")
	b.WriteString("      - Matter Comparison: matter-comparison.md\n")
	b.WriteString("      - Design Decisions: decision-log.md\n")
}

// GenerateExtraCSS produces the custom CSS for MkDocs Material.
func GenerateExtraCSS() string {
	return `/* Typography: tighter, more professional */
.md-typeset { font-size: 0.82rem; line-height: 1.6; }

/* Headers: quiet weight, no color, clear hierarchy */
.md-typeset h1 { font-weight: 600; letter-spacing: -0.02em; }
.md-typeset h2 {
  font-weight: 600;
  border-bottom: 1px solid var(--md-default-fg-color--lightest);
  padding-bottom: 0.3em;
  margin-top: 2em;
}
.md-typeset h3 { font-weight: 500; color: var(--md-default-fg-color--light); }

/* Tables: clean, scannable, no heavy borders */
.md-typeset table:not([class]) { font-size: 0.8rem; }
.md-typeset table:not([class]) th {
  background: var(--md-default-fg-color--lightest);
  font-weight: 600;
  text-transform: uppercase;
  font-size: 0.7rem;
  letter-spacing: 0.05em;
}
.md-typeset table:not([class]) td { vertical-align: top; }

/* Inline code / badges */
.md-typeset code { font-size: 0.78rem; }

/* Mermaid diagrams: constrained width, centered */
.mermaid { max-width: 720px; margin: 1.5em auto; }

/* Blockquotes for metadata */
.md-typeset blockquote {
  border-left-color: var(--md-accent-fg-color);
  padding: 0.4em 1em;
  font-size: 0.82rem;
}
.md-typeset blockquote p { margin: 0; }
`
}

// GenerateHomePage produces the docs/index.md homepage content from the home.md.tmpl template.
func GenerateHomePage(m *DocModel) string {
	out, err := renderTemplate("home.md.tmpl", m)
	if err != nil {
		// Template is embedded and tested; panic indicates a build-time bug.
		panic(err)
	}
	return out
}

// writeMkDocsConfig writes mkdocs.yml to projectRoot and extra.css to generatedDir.
func writeMkDocsConfig(m *DocModel, projectRoot, generatedDir string) error {
	// Write mkdocs.yml
	mkdocsPath := filepath.Join(projectRoot, "mkdocs.yml")
	if err := os.WriteFile(mkdocsPath, []byte(GenerateMkDocsYAML(m)), 0o644); err != nil {
		return fmt.Errorf("writing mkdocs.yml: %w", err)
	}

	// Write homepage (docs/index.md)
	docsDir := filepath.Join(projectRoot, "docs")
	homePath := filepath.Join(docsDir, "index.md")
	if err := os.WriteFile(homePath, []byte(GenerateHomePage(m)), 0o644); err != nil {
		return fmt.Errorf("writing homepage: %w", err)
	}

	// Write extra CSS
	cssDir := filepath.Join(generatedDir, "stylesheets")
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		return fmt.Errorf("creating stylesheets dir: %w", err)
	}
	cssPath := filepath.Join(cssDir, "extra.css")
	if err := os.WriteFile(cssPath, []byte(GenerateExtraCSS()), 0o644); err != nil {
		return fmt.Errorf("writing extra.css: %w", err)
	}

	return nil
}
