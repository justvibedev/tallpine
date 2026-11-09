package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// B629: the carry check diffs an agent's checkout unfenced, so a diff program
// the agent names in its own config.worktree must not run.
func TestPatchIDRunsNoAgentDiffProgram(t *testing.T) {
	for name, setup := range map[string][]string{
		"diff.external": {"diff.external"},
		"textconv":      {"diff.x.textconv"},
	} {
		t.Run(name, func(t *testing.T) {
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
			sha := strings.TrimSpace(w.Git(t, w.Path, "rev-parse", "HEAD"))
			w.Git(t, w.Path, "config", "--worktree", setup[0], w.Script)
			w.TryGit(w.Path, "diff", base+"..."+sha)
			if !w.Ran() {
				t.Fatal("payload never ran: this test no longer exercises the attack")
			}
			if _, err := gitPatchID(context.Background(), w.Path, base, sha); err != nil {
				t.Fatal(err)
			}
			if w.Ran() {
				t.Fatal("the daemon's git ran the agent's diff program")
			}
		})
	}
}
