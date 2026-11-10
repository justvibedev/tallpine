package cli

import (
	"strings"
	"testing"
)

func TestModelCatalogError(t *testing.T) {
	cat := agentModelsResponse{Models: []agentModelInfo{{ID: "claude-sonnet-5-5"}, {ID: "claude-opus-5-5"}}}
	if err := modelCatalogError(cat, "claude-code", "claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	err := modelCatalogError(cat, "claude-code", "sonnet")
	if err == nil || !strings.Contains(err.Error(), "claude-sonnet-5-5, claude-opus-5-5") {
		t.Fatalf("want valid IDs in error, got %v", err)
	}
	// claude-code shape: the daemon sets allowCustom with discovered models.
	cat.AllowCustom = true
	if err := modelCatalogError(cat, "claude-code", "sonnet"); err == nil {
		t.Fatal("alias must be rejected even when allowCustom is set")
	}
	if err := modelCatalogError(cat, "claude-code", "Claude-Opus-5-5"); err != nil {
		t.Fatal(err)
	}
	if err := modelCatalogError(agentModelsResponse{}, "x", "anything"); err != nil {
		t.Fatal(err)
	}
}

func TestSingleReviewerHarness(t *testing.T) {
	if got := singleReviewerHarness([]reviewerConfig{{Harness: "codex"}}); got != "codex" {
		t.Fatal(got)
	}
	if got := singleReviewerHarness([]reviewerConfig{{Harness: "codex"}, {Harness: "claude-code"}}); got != "" {
		t.Fatal(got)
	}
}

func TestModelCatalogErrorAcceptsWellFormedClaudeIDMissingFromList(t *testing.T) {
	cat := agentModelsResponse{AllowCustom: true, Models: []agentModelInfo{{ID: "claude-fable-5-1"}}}
	if err := modelCatalogError(cat, "claude-code", "claude-fable-5-2"); err != nil {
		t.Fatalf("claude-fable-5-2 refused: %v", err)
	}
	err := modelCatalogError(cat, "claude-code", "claude-fabel-5-2")
	if err == nil || !strings.Contains(err.Error(), "claude-fable-5-1") || !strings.Contains(err.Error(), "); a full ID") {
		t.Fatalf("typo error = %v, want valid IDs and the full-ID hint", err)
	}
	if err := modelCatalogError(agentModelsResponse{Models: []agentModelInfo{{ID: "gpt-5"}}}, "codex", "claude-fable-5-2"); err == nil {
		t.Fatal("the well-formed Claude ID rule must not apply to other harnesses")
	}
}

func TestModelCatalogErrorRefusesUnlistedIDOnNonFirstPartyCatalog(t *testing.T) {
	cat := agentModelsResponse{AllowCustom: true, Models: []agentModelInfo{{ID: "anthropic.claude-opus-5-5-v1:0"}}}
	err := modelCatalogError(cat, "claude-code", "claude-opus-5-5")
	if err == nil {
		t.Fatal("Bedrock-style catalog accepted a first-party ID it does not list")
	}
	if strings.Contains(err.Error(), "also accepted") || claudeIDHint("claude-code", cat.Models) != "" {
		t.Fatalf("Bedrock-style catalog got the full-ID hint: %v", err)
	}
}
