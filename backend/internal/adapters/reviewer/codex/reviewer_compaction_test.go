package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The compaction window the review engine puts in a reviewer's config reaches
// Codex's model_auto_compact_token_limit flag (B734).
func TestReviewCommandCarriesTheCompactionWindow(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	got, err := New().ReviewCommand(context.Background(), ports.ReviewInvocation{
		Config: domain.AgentConfig{Launch: domain.AgentLaunchOptions{AutoCompactWindow: "20%", ModelContextWindow: 272_000}},
		Prompt: "review it",
	})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Contains(got.Argv, "model_auto_compact_token_limit=200000") {
		t.Fatalf("review command %#v missing the compaction limit", got.Argv)
	}
}
