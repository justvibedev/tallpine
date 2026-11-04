package session

import (
	"path/filepath"
	"testing"
)

// relativeWithinRoot is strict: the root itself is not "inside" it.
func TestRelativeWithinRootIsStrict(t *testing.T) {
	root := t.TempDir()
	if _, ok := relativeWithinRoot(root, root); ok {
		t.Fatal("root itself must not count as inside")
	}
	if rel, ok := relativeWithinRoot(root, filepath.Join(root, "a", "b")); !ok || rel != "a/b" {
		t.Fatalf("child = %q,%v", rel, ok)
	}
	if _, ok := relativeWithinRoot(root, root+"-sibling"); ok {
		t.Fatal("prefix sibling must not count as inside")
	}
}
