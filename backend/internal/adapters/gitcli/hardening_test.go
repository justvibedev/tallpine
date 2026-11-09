package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: a textconv program named in an agent's config.worktree must not run from
// the daemon's git diff.
func TestDiffRunsNoAgentTextconv(t *testing.T) {
	w := testenv.NewAgentWorktree(t)
	base := strings.TrimSpace(w.Git(t, w.Path, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(w.Path, "f"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, ".gitattributes"), []byte("f diff=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.Git(t, w.Path, "add", "f", ".gitattributes")
	w.Git(t, w.Path, "commit", "-q", "-m", "c")
	w.Git(t, w.Path, "config", "--worktree", "diff.x.textconv", w.Script)
	w.TryGit(w.Path, "diff", base, "HEAD")
	if !w.Ran() {
		t.Fatal("payload never ran: this test no longer exercises the attack")
	}
	if _, err := New().Output(context.Background(), w.Path, "diff", base, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if w.Ran() {
		t.Fatal("the daemon's git diff ran the agent's textconv")
	}
}
