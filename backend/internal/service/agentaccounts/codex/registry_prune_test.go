package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPruneStaleRowsRemovesOnlyManagedRowsWithoutFolder(t *testing.T) {
	root := t.TempDir()
	kept, stale := "b60a377d-da68-4a61-86f2-f31f04c571f2", "1c5de3ab-82d0-4a68-a06b-8495cdeab909"
	if err := os.MkdirAll(filepath.Join(root, kept), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := string(domain.CodexAccountSourceManaged)
	rows := &registryRows{accounts: []domain.AgentAccount{
		{ID: kept, Provider: domain.AgentAccountProviderCodex, Source: managed},
		{ID: stale, Provider: domain.AgentAccountProviderCodex, Source: managed},
		{ID: "9b2f6c1e-3f7a-4c52-9a1d-0e8f5b7c1a23", Provider: domain.AgentAccountProviderCodex},
		{ID: "claude-1", Provider: domain.AgentAccountProviderClaude, Source: managed},
	}}
	n, err := PruneStaleRows(context.Background(), root, rows, nil)
	if err != nil || n != 1 || len(rows.accounts) != 3 {
		t.Fatalf("pruned %d, %v, rows=%+v", n, err, rows.accounts)
	}
	for _, a := range rows.accounts {
		if a.ID == stale {
			t.Fatalf("stale row survived: %+v", rows.accounts)
		}
	}
}
