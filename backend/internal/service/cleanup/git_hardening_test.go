package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the cleanup pass inspects terminated sessions' worktrees unfenced, with
// no owner action, so what the agent set in its config.worktree must not run.
func TestGitInspectRunsNoAgentFsmonitor(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	w.Git(t, w.Path, "config", "--worktree", "core.fsmonitor", w.Script)
	w.TryGit(w.Path, "status", "--porcelain")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if _, err := GitInspect(context.Background(), w.Path, false); err != nil {
		t.Fatal(err)
	}
	if w.Ran() {
		t.Fatal("the daemon's git ran the agent's fsmonitor")
	}
}

func TestSalvageDiffRunsNoAgentDiffProgram(t *testing.T) {
	for _, key := range []string{"diff.external", "diff.x.textconv"} {
		t.Run(key, func(t *testing.T) {
			w := testenv.NewAgentWorktree(t)
			file := filepath.Join(w.Path, "f")
			if err := os.WriteFile(file, []byte("a\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.Path, ".gitattributes"), []byte("f diff=x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w.Git(t, w.Path, "add", "f", ".gitattributes")
			w.Git(t, w.Path, "commit", "-q", "-m", "c")
			if err := os.WriteFile(file, []byte("b\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			w.Git(t, w.Path, "config", "--worktree", key, w.Script)
			w.TryGit(w.Path, "diff", "HEAD")
			if !w.Ran() {
				t.Fatal("payload never ran: this test no longer exercises the attack")
			}
			if _, err := gitOutput(context.Background(), w.Path, "diff", "--binary", "HEAD"); err != nil {
				t.Fatal(err)
			}
			if w.Ran() {
				t.Fatal("the daemon's git ran the agent's diff program")
			}
		})
	}
}
