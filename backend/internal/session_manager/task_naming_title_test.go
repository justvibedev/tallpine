package sessionmanager

import (
	"context"
	"errors"
	"testing"
)

// B730: an explicit --name names the branch, not the prompt body, which may
// mention other tasks or versions.
func TestTaskNamingFallbackUsesExplicitTitle(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.chat = &taskNamingLauncher{err: errors.New("offline")}
	cfg := asyncChatSpawnConfig("Previous release was 0.13.15; bump the version")
	cfg.NameFromPrompt, cfg.Async, cfg.DisplayName, cfg.KeepDisplayName = true, false, "Release 0.13.17", true
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Metadata.Branch; got != "tp/release-0-13-17/root" {
		t.Fatalf("branch = %q", got)
	}
}
