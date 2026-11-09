package skillbridge

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: skill commits run unfenced in the agent's checkout.
func TestGitOutRunsNoAgentFsmonitor(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	w.TryGit(w.Path, "status", "--porcelain")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if _, err := gitOut(context.Background(), w.Path, nil, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}
