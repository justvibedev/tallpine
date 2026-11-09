package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the adapter runs unfenced in agent checkouts, so what an agent sets in
// its own config.worktree must not run from there.
func TestRunCommandRunsNoAgentFsmonitor(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	w.TryGit(w.Path, "status", "--porcelain")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if out, err := runCommand(context.Background(), "git", "-C", w.Path, "status", "--porcelain"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}

func TestRunCommandRunsNoAgentHook(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	hooks := filepath.Join(filepath.Dir(w.Path), "hooks")
	if err := os.Mkdir(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(w.Script, filepath.Join(hooks, "post-merge")); err != nil {
		t.Fatal(err)
	}
	w.Git(t, w.Repo, "commit", "-q", "--allow-empty", "-m", "advance")
	w.Git(t, w.Path, "config", "--worktree", "core.hooksPath", hooks)
	before := strings.TrimSpace(w.Git(t, w.Path, "rev-parse", "HEAD"))
	w.TryGit(w.Path, "merge", "--ff-only", "--quiet", "main")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	w.Git(t, w.Path, "reset", "-q", "--hard", before)
	if out, err := runCommand(context.Background(), "git", "-C", w.Path, "merge", "--ff-only", "--quiet", "main"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's hook")
	}
}

// git worktree add runs from the repository, whose hooks the fence protects, so
// the owner's post-checkout hook keeps running for it.
func TestRunCommandKeepsTheOwnersWorktreeHook(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	if err := os.Symlink(w.Script, filepath.Join(w.Repo, ".git", "hooks", "post-checkout")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(w.Path), "added")
	if out, err := runCommand(context.Background(), "git", "-C", w.Repo, "worktree", "add", "-q", path, "-b", "added"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !w.Ran() {
		t.Fatal("the owner's post-checkout hook stopped running for git worktree add")
	}
}

func TestPreserveStagingRunsNoAgentFsmonitor(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	w.TryGit(w.Path, "add", "-A")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	workspace := &Workspace{binary: "git"}
	idx := filepath.Join(t.TempDir(), "index")
	if out, err := workspace.tempIndexGit(context.Background(), idx, addAllTempIndexArgs(w.Path)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}
