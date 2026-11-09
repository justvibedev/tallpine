package localland

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the checkout holding the target branch can be an agent's worktree.
func TestLandGitRunsNoAgentFsmonitor(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	w.TryGit(w.Path, "status", "--porcelain=v1", "--untracked-files=no")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if _, err := git(context.Background(), w.Path, "", "status", "--porcelain=v1", "--untracked-files=no"); err != nil {
		t.Fatal(err)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}
