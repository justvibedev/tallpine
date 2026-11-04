package gitworktree

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// advanceOriginMain pushes a new commit to origin/main from a separate clone,
// leaving repo's view of origin stale until it fetches.
func advanceOriginMain(t *testing.T, git, tmp, repo string) string {
	t.Helper()
	updater := filepath.Join(tmp, "updater")
	run(t, git, "clone", gitOutput(t, git, repo, "remote", "get-url", "origin"), updater)
	runGit(t, git, updater, "config", "user.email", "tallpine@example.com")
	runGit(t, git, updater, "config", "user.name", "Tallpine Agents")
	if err := os.WriteFile(filepath.Join(updater, "fresh.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	runGit(t, git, updater, "add", "fresh.txt")
	runGit(t, git, updater, "commit", "-m", "fresh")
	runGit(t, git, updater, "push", "origin", "HEAD:main")
	return gitOutput(t, git, updater, "rev-parse", "HEAD")
}

type refreshFixture struct {
	git, tmp, repo string
	ws             *Workspace
	cfg            ports.WorkspaceConfig
	path, oldHead  string
}

func newRefreshFixture(t *testing.T, kind domain.SessionKind) refreshFixture {
	t.Helper()
	git, tmp, repo, _, ws := newOriginWorkspace(t)
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-orchestrator", Kind: kind, Branch: "tp/proj-orchestrator", RefreshOrchestrator: true}
	info, err := ws.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return refreshFixture{git: git, tmp: tmp, repo: repo, ws: ws, cfg: cfg, path: info.Path, oldHead: gitOutput(t, git, info.Path, "rev-parse", "HEAD")}
}

func (f refreshFixture) head(t *testing.T) string {
	t.Helper()
	return gitOutput(t, f.git, f.path, "rev-parse", "HEAD")
}

func TestOrchestratorWorktreeFastForwardsWhenCleanOnCreateReuse(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	fresh := advanceOriginMain(t, f.git, f.tmp, f.repo)
	if _, err := f.ws.Create(context.Background(), f.cfg); err != nil {
		t.Fatalf("create reuse: %v", err)
	}
	if got := f.head(t); got != fresh {
		t.Fatalf("HEAD = %s, want fast-forwarded to %s", got, fresh)
	}
}

func TestOrchestratorWorktreeFastForwardsWhenCleanOnRestore(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	fresh := advanceOriginMain(t, f.git, f.tmp, f.repo)
	cfg := f.cfg
	cfg.Path = f.path
	if _, err := f.ws.Restore(context.Background(), cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := f.head(t); got != fresh {
		t.Fatalf("HEAD = %s, want fast-forwarded to %s", got, fresh)
	}
}

func TestOrchestratorWorktreeRefreshSkipsDirtyWorktree(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	if err := os.WriteFile(filepath.Join(f.path, "README.md"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatalf("dirty: %v", err)
	}
	advanceOriginMain(t, f.git, f.tmp, f.repo)
	if _, err := f.ws.Restore(context.Background(), f.cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := f.head(t); got != f.oldHead {
		t.Fatalf("dirty worktree moved: HEAD = %s, want %s", got, f.oldHead)
	}
	if b, _ := os.ReadFile(filepath.Join(f.path, "README.md")); string(b) != "local edit\n" {
		t.Fatalf("local edit lost: %q", b)
	}
}

func TestOrchestratorWorktreeRefreshSkipsAheadWorktree(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	if err := os.WriteFile(filepath.Join(f.path, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, f.git, f.path, "add", "mine.txt")
	runGit(t, f.git, f.path, "commit", "-m", "local work")
	local := f.head(t)
	advanceOriginMain(t, f.git, f.tmp, f.repo)
	var logs bytes.Buffer
	f.ws.logger = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := f.ws.Create(context.Background(), f.cfg); err != nil {
		t.Fatalf("create reuse: %v", err)
	}
	if got := f.head(t); got != local {
		t.Fatalf("ahead worktree moved: HEAD = %s, want %s", got, local)
	}
	if !bytes.Contains(logs.Bytes(), []byte("not on refs/remotes/origin/main")) {
		t.Fatalf("ahead skip not logged; logs:\n%s", logs.String())
	}
}

func TestWorkerWorktreeIsNeverRefreshed(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindWorker)
	advanceOriginMain(t, f.git, f.tmp, f.repo)
	if _, err := f.ws.Restore(context.Background(), f.cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := f.head(t); got != f.oldHead {
		t.Fatalf("worker worktree moved: HEAD = %s, want %s", got, f.oldHead)
	}
}

func TestOrchestratorWorktreeRefreshFetchFailureDoesNotBlock(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	runGit(t, f.git, f.repo, "remote", "set-url", "origin", filepath.Join(f.tmp, "missing.git"))
	if _, err := f.ws.Restore(context.Background(), f.cfg); err != nil {
		t.Fatalf("restore with unreachable remote: %v", err)
	}
	if _, err := f.ws.Create(context.Background(), f.cfg); err != nil {
		t.Fatalf("create reuse with unreachable remote: %v", err)
	}
}

// The reconcile path for a Chat session that may still be live does not opt in;
// its orchestrator worktree must stay where it is.
func TestOrchestratorWorktreeNotRefreshedWithoutOptIn(t *testing.T) {
	t.Parallel()
	f := newRefreshFixture(t, domain.KindOrchestrator)
	advanceOriginMain(t, f.git, f.tmp, f.repo)
	cfg := f.cfg
	cfg.RefreshOrchestrator = false
	if _, err := f.ws.Restore(context.Background(), cfg); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := f.ws.Create(context.Background(), cfg); err != nil {
		t.Fatalf("create reuse: %v", err)
	}
	if got := f.head(t); got != f.oldHead {
		t.Fatalf("worktree moved without opt-in: HEAD = %s, want %s", got, f.oldHead)
	}
}
