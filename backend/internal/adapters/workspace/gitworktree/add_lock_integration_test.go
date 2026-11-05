package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// A registration still carrying git's own "initializing" lock (a `git
// worktree add` that died midway) is unlocked and removed the normal,
// non-forced way.
func TestWorkspaceIntegrationDestroyLiftsStaleAddLock(t *testing.T) {
	t.Parallel()

	git, _, repo, _, ws := newOriginWorkspace(t)
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/stale-lock"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", "--reason", gitAddLockReason, info.Path)

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy with a stale add lock: %v", err)
	}
	if _, statErr := os.Stat(info.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("worktree still on disk: %v", statErr)
	}
}

// A lock with any other reason is a deliberate keep and still refuses.
func TestWorkspaceIntegrationDestroyKeepsDeliberateLock(t *testing.T) {
	t.Parallel()

	git, _, repo, _, ws := newOriginWorkspace(t)
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/kept-lock"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", "--reason", "on a usb stick", info.Path)

	if err := ws.Destroy(ctx, info); !errors.Is(err, ports.ErrWorkspaceLocked) {
		t.Fatalf("destroy deliberately locked error = %v, want ports.ErrWorkspaceLocked", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "README.md")); statErr != nil {
		t.Fatalf("locked worktree was not preserved: %v", statErr)
	}
}

func TestUntranslatedGitEnvSwitchesOnlyMessages(t *testing.T) {
	got := untranslatedGitEnv([]string{"PATH=/bin", "LANG=de_DE.UTF-8", "LC_ALL=fr_FR.UTF-8", "LC_CTYPE=de_DE.UTF-8", "LC_MESSAGES=de_DE.UTF-8"})
	want := []string{"PATH=/bin", "LANG=fr_FR.UTF-8", "LC_MESSAGES=C"}
	if len(got) != len(want) {
		t.Fatalf("env = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env = %v, want %v", got, want)
		}
	}
	if !readsGitMessages([]string{"-C", "/repo", "worktree", "remove"}) || readsGitMessages([]string{"-C", "/repo", "commit"}) {
		t.Fatal("readsGitMessages misclassified worktree/commit")
	}
}
