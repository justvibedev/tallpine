package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Kiro's handoff history probe also gates every terminal restore. An id the v3
// store does not hold (v3 still resumes v2-created sessions) must keep
// resuming natively; only a v3 session reserved by session/new with no turn
// may relaunch fresh.
func TestKiroRestoreResumesUnlessV3SessionIsProvablyEmpty(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kiro-cli"), []byte("#!/bin/sh\necho 'kiro-cli 3.0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	workspace := t.TempDir()
	restore := func(t *testing.T, id string) ([]string, RestoreMode) {
		t.Helper()
		argv, _, mode, err := restoreArgv(context.Background(), agentpolicy.Decision{}, kiro.New(),
			"kiro-1", workspace, domain.SessionMetadata{AgentSessionID: id}, "", "", ports.AgentConfig{},
			domain.KindWorker, domain.HarnessKiro, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return argv, mode
	}

	t.Run("id absent from the v3 store resumes", func(t *testing.T) {
		argv, mode := restore(t, "sess_v2-only")
		if mode != RestoreModeNative || !slices.Contains(argv, "--resume-id") {
			t.Fatalf("restore mode %q argv %q, want native --resume-id", mode, argv)
		}
	})

	t.Run("reserved-only v3 session starts fresh", func(t *testing.T) {
		dir := filepath.Join(home, ".kiro", "sessions", "h1", "sess_reserved")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		argv, mode := restore(t, "sess_reserved")
		if mode == RestoreModeNative || slices.Contains(argv, "--resume-id") {
			t.Fatalf("restore mode %q argv %q, want a fresh launch", mode, argv)
		}
	})
}
