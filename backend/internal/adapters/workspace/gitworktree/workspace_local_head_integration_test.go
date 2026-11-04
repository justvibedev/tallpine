package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestWorkspaceIntegrationLocalHeadFallbackAnnouncedOnSpawn proves the spawn
// path tells the user (via the injected logger) which branch it fell back to.
func TestWorkspaceIntegrationLocalHeadFallbackAnnouncedOnSpawn(t *testing.T) {
	t.Parallel()

	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "headfallback")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "user.email", "tallpine@example.com")
	runGit(t, git, repo, "config", "user.name", "Tallpine Agents")
	runGit(t, git, repo, "commit", "--allow-empty", "-m", "seed")
	var logs bytes.Buffer
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Mirror session_manager/spawn_workspace.go: resolve first, then Create with BaseRef.
	def, err := ws.ResolveDefaultBranch(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("ResolveDefaultBranch: %v", err)
	}
	if _, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "tp/proj-1", BaseRef: def.BaseRef}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(logs.String(), "set-config --default-branch") || !strings.Contains(logs.String(), "main") {
		t.Fatalf("spawn did not announce fallback; logs: %s", logs.String())
	}
}

// TestWorkspaceIntegrationExplicitBaseBranchBeatsLocalHeadFallback proves an
// explicit default branch wins over the no-remote local-HEAD fallback.
func TestWorkspaceIntegrationExplicitBaseBranchBeatsLocalHeadFallback(t *testing.T) {
	t.Parallel()

	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "explicit")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "tallpine@example.com")
	runGit(t, git, repo, "config", "user.name", "Tallpine Agents")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "switch", "-c", "feature/temporary")
	if err := os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	runGit(t, git, repo, "add", "extra.txt")
	runGit(t, git, repo, "commit", "-m", "extra")

	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	created, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "tp/proj-1", BaseBranch: "main"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(created.Path, "extra.txt")); err == nil {
		t.Fatalf("worktree seeded from local HEAD feature/temporary, want explicit main")
	}
}

// TestWorkspaceIntegrationAutoRejectsUnmarkedRemotelessRepo proves automatic
// selection stays unresolved when a remoteless repo has no recorded default and
// HEAD is detached (a checked-out branch is the documented fallback).
func TestWorkspaceIntegrationAutoRejectsUnmarkedRemotelessRepo(t *testing.T) {
	t.Parallel()

	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "unmarked")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "tallpine@example.com")
	runGit(t, git, repo, "config", "user.name", "Tallpine Agents")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "checkout", "--detach") // a branch HEAD would be the fallback default

	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	_, err = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "tp/proj-1"})
	if !errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved) {
		t.Fatalf("Create error = %v, want ErrWorkspaceDefaultBranchUnresolved", err)
	}
	if !strings.Contains(err.Error(), "remote set-head") || !strings.Contains(err.Error(), repo) {
		t.Fatalf("Create error = %v, want repository-specific remote HEAD remediation", err)
	}
	if out, err := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/tp/proj-1").CombinedOutput(); err == nil {
		t.Fatalf("unresolved auto selection leaked a session branch: %s", out)
	}
}
