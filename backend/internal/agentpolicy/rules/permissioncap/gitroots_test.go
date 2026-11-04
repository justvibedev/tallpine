package permissioncap

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGitWritableRootsForLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "main", ".git")
	gitdir := filepath.Join(common, "worktrees", "w")
	ws := filepath.Join(root, "ws")
	for _, d := range []string{gitdir, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644)
	got := GitWritableRoots(ws)
	want := []string{gitdir, filepath.Join(common, "objects"), filepath.Join(common, "refs"), filepath.Join(common, "logs")}
	if !slices.Equal(got, want) {
		t.Fatalf("GitWritableRoots = %v, want %v", got, want)
	}
	for _, g := range got {
		if strings.HasSuffix(g, "hooks") || g == common {
			t.Errorf("hooks/config must stay read-only: %s", g)
		}
	}
	if GitWritableRoots(t.TempDir()) != nil {
		t.Error("no .git file must yield no roots")
	}
}
