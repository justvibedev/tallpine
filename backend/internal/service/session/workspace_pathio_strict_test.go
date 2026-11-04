package session

import (
	"os"
	"path/filepath"
	"testing"
)

// The migrated readers keep pathx.StrictlyWithin's meaning: a symlink that
// resolves to the root itself or to a sibling sharing the root's name prefix
// is outside the workspace.
func TestWorkspaceReadersRejectRootAndPrefixSiblingLinks(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "b")
	sibling := filepath.Join(base, "bc")
	for _, dir := range []string{root, sibling} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}

	if _, err := confinedWorkspaceFile(root, "link.txt"); err == nil {
		t.Fatal("confinedWorkspaceFile followed a link into a prefix sibling")
	}
	if _, err := confinedWorkspaceFile(root, "self"); err == nil {
		t.Fatal("confinedWorkspaceFile accepted a link resolving to the root itself")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "link.txt" {
			continue
		}
		if _, ok, err := scratchWorkspaceFileInfo(root, filepath.Join(root, entry.Name()), entry); err != nil || ok {
			t.Fatalf("scratchWorkspaceFileInfo ok=%v err=%v for a link into a prefix sibling", ok, err)
		}
	}
}
