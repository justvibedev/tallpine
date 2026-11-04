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
