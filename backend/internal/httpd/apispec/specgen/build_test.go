package specgen_test

import (
	"bytes"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec/specgen"
)

type openAPISchemaNode struct {
	Ref        string                       `yaml:"$ref"`
	Type       any                          `yaml:"type"`
	Format     string                       `yaml:"format"`
	Enum       []string                     `yaml:"enum"`
	Required   []string                     `yaml:"required"`
	Properties map[string]openAPISchemaNode `yaml:"properties"`
	AnyOf      []openAPISchemaNode          `yaml:"anyOf"`
	OneOf      []openAPISchemaNode          `yaml:"oneOf"`
}

func TestBuild_CodexSwitchContractIsRedactedAndOnlyMountedRoutesAreDocumented(t *testing.T) {
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc struct {
		Paths      map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}

	phase := doc.Components.Schemas["CodexAccountSwitchResponse"].Properties["phase"]
	want := []string{
		"requested", "checkpointing_source", "activating_target",
		"recovery_required", "completed", "failed",
	}
	if !slices.Equal(phase.Enum, want) {
		t.Fatalf("CodexAccountSwitchResponse.phase enum = %v, want %v", phase.Enum, want)
	}
	for _, obsolete := range []string{"sessions"} {
		if _, ok := doc.Components.Schemas["CodexAccountSwitchResponse"].Properties[obsolete]; ok {
			t.Fatalf("obsolete %q remains in CodexAccountSwitchResponse", obsolete)
		}
		if _, ok := doc.Components.Schemas["StartCodexAccountSwitchRequest"].Properties[obsolete]; ok {
			t.Fatalf("obsolete %q remains in StartCodexAccountSwitchRequest", obsolete)
		}
	}
	if _, ok := doc.Components.Schemas["CodexAccountSwitchSessionResponse"]; ok {
		t.Fatal("obsolete CodexAccountSwitchSessionResponse schema remains")
	}
	if _, ok := doc.Paths["/api/v1/agents/codex/account-switches/{switchId}"]; !ok {
		t.Fatal("durable switch GET path is missing from generated contract")
	}
	if _, ok := doc.Paths["/api/v1/agents/codex/account-switches/{switchId}/cancel"]; ok {
		t.Fatal("stale switch cancel path remains in generated contract")
	}
}

// TestBuild_MatchesEmbedded is the drift guard: the committed (embedded)
// openapi.yaml must equal fresh Build() output. If this fails, run
// `go generate ./...` and commit the result.
func TestBuild_MatchesEmbedded(t *testing.T) {
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	embedded := apispec.Default().YAML()
	if !bytes.Equal(normalizeYAML(got), normalizeYAML(embedded)) {
		t.Fatalf("embedded openapi.yaml is stale — run `go generate ./...` and commit.\n"+
			"len(fresh)=%d len(embedded)=%d", len(got), len(embedded))
	}
}

func TestBuild_InstallJobTargetRemainsAnEnum(t *testing.T) {
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	targets := doc.Components.Schemas["InstallJob"].Properties["target"].Enum
	for _, target := range []string{"tmux", "cloudflared", "kiro"} {
		if !slices.Contains(targets, target) {
			t.Fatalf("InstallJob.target enum = %v, missing %q", targets, target)
		}
	}
}

func TestBuild_InstallContracts(t *testing.T) {
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc struct {
		Paths map[string]struct {
			Post struct {
				Parameters []struct {
					Name   string            `yaml:"name"`
					Schema openAPISchemaNode `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"post"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	var targets []string
	for _, parameter := range doc.Paths["/api/v1/system/install/{target}"].Post.Parameters {
		if parameter.Name == "target" {
			targets = parameter.Schema.Enum
		}
	}
	if want := []string{"tmux", "gh", "claude", "codex", "kiro", "cloudflared"}; !slices.Equal(targets, want) {
		t.Fatalf("InstallTargetParam target enum = %v, want %v", targets, want)
	}
	for _, removed := range []string{"opencode", "opencode-v2", "copilot"} {
		if slices.Contains(doc.Components.Schemas["InstallJob"].Properties["target"].Enum, removed) {
			t.Fatalf("InstallJob.target enum still contains removed target %q", removed)
		}
	}
	for schema := range map[string]bool{
		"AgentInstallPlan":   true,
		"AgentInstallMethod": true,
		"InstallJob":         true,
	} {
		if _, ok := doc.Components.Schemas[schema].Properties["notice"]; !ok {
			t.Errorf("%s is missing the install replacement notice", schema)
		}
	}
}

func TestBuild_UsageEstimatedCostIsNamedReusableAndNullable(t *testing.T) {
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}

	cost, ok := doc.Components.Schemas["EstimatedCostResponse"]
	if !ok {
		t.Fatal("EstimatedCostResponse is not a named schema")
	}
	for _, field := range []string{"totalNanos", "inputNanos", "cachedInputNanos", "outputNanos", "coverage", "providerAttribution"} {
		if !slices.Contains(cost.Required, field) {
			t.Fatalf("EstimatedCostResponse required = %v, missing %s", cost.Required, field)
		}
	}
	for _, field := range []string{"totalNanos", "inputNanos", "cachedInputNanos", "outputNanos"} {
		if cost.Properties[field].Format != "int64" {
			t.Fatalf("EstimatedCostResponse.%s format = %q, want int64", field, cost.Properties[field].Format)
		}
	}
	if want := []string{"complete", "partial"}; !slices.Equal(cost.Properties["coverage"].Enum, want) {
		t.Fatalf("coverage enum = %v, want %v", cost.Properties["coverage"].Enum, want)
	}
	if want := []string{"observed", "inferred", "mixed"}; !slices.Equal(cost.Properties["providerAttribution"].Enum, want) {
		t.Fatalf("provider attribution enum = %v, want %v", cost.Properties["providerAttribution"].Enum, want)
	}

	const costRef = "#/components/schemas/EstimatedCostResponse"
	for schemaName, propertyName := range map[string]string{
		"CompactSessionUsageResponse": "estimatedCost",
		"UsageTotalsResponse":         "estimatedCost",
	} {
		property := doc.Components.Schemas[schemaName].Properties[propertyName]
		if property.Type != nil {
			t.Fatalf("%s.%s has contradictory outer type %v", schemaName, propertyName, property.Type)
		}
		if !schemaContainsRef(property, costRef) || !schemaAllowsNull(property) {
			t.Fatalf("%s.%s = %+v, want nullable %s", schemaName, propertyName, property, costRef)
		}
	}
	// One model is one row. The billing provider is a pricing input the
	// aggregate has already applied, so it must not reappear here and split a
	// model apart by Tallpine's own attribution state.
	model := doc.Components.Schemas["UsageModelResponse"]
	if slices.Contains(model.Required, "providerId") {
		t.Fatalf("UsageModelResponse still exposes providerId: %v", model.Required)
	}
}

func schemaContainsRef(node openAPISchemaNode, want string) bool {
	if node.Ref == want {
		return true
	}
	for _, child := range append(node.AnyOf, node.OneOf...) {
		if child.Ref == want {
			return true
		}
	}
	return false
}

func schemaAllowsNull(node openAPISchemaNode) bool {
	containsNull := func(value any) bool {
		switch typed := value.(type) {
		case string:
			return typed == "null"
		case []any:
			return slices.Contains(typed, any("null"))
		default:
			return false
		}
	}
	if node.Type != nil && !containsNull(node.Type) {
		return false
	}
	if len(node.AnyOf) > 0 {
		for _, child := range node.AnyOf {
			if schemaAllowsNull(child) {
				return true
			}
		}
		return false
	}
	if len(node.OneOf) > 0 {
		matches := 0
		for _, child := range node.OneOf {
			if schemaAllowsNull(child) {
				matches++
			}
		}
		return matches == 1
	}
	if node.Ref != "" {
		return false
	}
	return containsNull(node.Type)
}

type schemaDocument struct {
	Components struct {
		Schemas map[string]struct {
			Properties map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func buildSchemas(t *testing.T) schemaDocument {
	t.Helper()
	got, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var doc schemaDocument
	if err := yaml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	return doc
}

// TestBuild_Deterministic guards against nondeterministic output (which would
// make the drift check flaky in CI).
func TestBuild_Deterministic(t *testing.T) {
	a, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build #1: %v", err)
	}
	b, err := specgen.Build()
	if err != nil {
		t.Fatalf("Build #2: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("Build() is not deterministic across calls")
	}
}

func normalizeYAML(in []byte) []byte {
	return bytes.ReplaceAll(in, []byte("\r\n"), []byte("\n"))
}

// Only claude-code, codex and kiro ship. The public contracts must not offer a
// removed harness as a spawn, delegation or reviewer choice.
func TestBuild_HarnessEnumsListOnlySupportedHarnesses(t *testing.T) {
	doc := buildSchemas(t)
	for schema, field := range map[string]string{
		"SpawnSessionRequest":       "harness",
		"DelegateTaskRequest":       "agent",
		"SessionView":               "reviewerHarness",
		"SetSessionReviewerRequest": "harness",
		"TriggerReviewRequest":      "harness",
	} {
		values := doc.Components.Schemas[schema].Properties[field].Enum
		for _, want := range []string{"claude-code", "codex", "kiro"} {
			if !slices.Contains(values, want) {
				t.Errorf("%s.%s enum = %v, missing %s", schema, field, values, want)
			}
		}
		for _, removed := range []string{"opencode", "opencode-v2", "omp", "fx", "codewhale", "prime-agent", "aider"} {
			if slices.Contains(values, removed) {
				t.Errorf("%s.%s enum = %v, still lists removed %s", schema, field, values, removed)
			}
		}
	}
}
