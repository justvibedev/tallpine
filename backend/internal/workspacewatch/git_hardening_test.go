package workspacewatch

import (
	"context"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the daemon watches an agent's checkout unfenced, so what the agent sets
// in its own config.worktree must not run from the watcher's git.
func agentFsmonitor(t *testing.T) testenv.AgentWorktree {
	t.Helper()
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	return w
}

func TestWatcherListingRunsNoAgentFsmonitor(t *testing.T) {
	w := agentFsmonitor(t)
	w.TryGit(w.Path, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if _, err := listGitFiles(context.Background(), w.Path); err != nil {
		t.Fatal(err)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}

func TestWatcherIgnoreCheckRunsNoAgentFsmonitor(t *testing.T) {
	w := agentFsmonitor(t)
	w.TryGit(w.Path, "check-ignore", "-q", "--", "f")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	gitIgnored(context.Background(), w.Path, filepath.Join(w.Path, "f"))
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}
