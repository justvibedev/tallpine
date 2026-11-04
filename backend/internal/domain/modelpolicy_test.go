package domain

import (
	"strings"
	"testing"
)

func TestResolveModelCodexDefaultIsSol(t *testing.T) {
	got, err := ProjectConfig{}.ResolveModel(HarnessCodex, "")
	if err != nil || got != "gpt-6.1-sol" {
		t.Fatalf("got %q, %v; want gpt-6.1-sol", got, err)
	}
	cfg := ProjectConfig{Models: ModelPolicy{CodexDefault: "gpt-x"}}
	if got, _ := cfg.ResolveModel(HarnessCodex, ""); got != "gpt-x" {
		t.Fatalf("configured default: got %q", got)
	}
	if got, _ := (ProjectConfig{}).ResolveModel(HarnessClaudeCode, ""); got != "" {
		t.Fatalf("non-codex default must stay empty, got %q", got)
	}
}

func TestResolveModelRefusesAstraUnlessAllowlisted(t *testing.T) {
	_, err := ProjectConfig{}.ResolveModel(HarnessCodex, "gpt-6-astra")
	if err == nil || !strings.Contains(err.Error(), "models.allowlist") {
		t.Fatalf("want refusal naming models.allowlist, got %v", err)
	}
	cfg := ProjectConfig{Models: ModelPolicy{Allowlist: []string{"gpt-6-astra"}}}
	if got, err := cfg.ResolveModel(HarnessCodex, "gpt-6-astra"); err != nil || got != "gpt-6-astra" {
		t.Fatalf("allowlisted astra: %q, %v", got, err)
	}
	cfg = ProjectConfig{Models: ModelPolicy{Denylist: []string{"foo"}}}
	if _, err := cfg.ResolveModel(HarnessCodex, "foo"); err == nil {
		t.Fatal("configured denylist entry must be refused")
	}
}

func TestPromptCacheTTLFor(t *testing.T) {
	c := ProjectConfig{}
	if c.PromptCacheTTLFor("reviewer") != "5m" || c.PromptCacheTTLFor("worker") != "" || c.PromptCacheTTLFor("orchestrator") != "" {
		t.Fatal("shipped defaults wrong")
	}
	c.PromptCacheTTL = map[string]string{"worker": "1h", "reviewer": ""}
	if c.PromptCacheTTLFor("worker") != "1h" || c.PromptCacheTTLFor("reviewer") != "" {
		t.Fatal("config override wrong")
	}
}
