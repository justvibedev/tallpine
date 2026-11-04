package scratch

import (
	"path/filepath"
	"testing"
)

// validateManagedPath keeps its strict meaning: children pass, the managed
// root itself and a prefix sibling are refused.
func TestValidateManagedPathStrict(t *testing.T) {
	root := t.TempDir()
	w, err := New(Options{ManagedRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.validateManagedPath(filepath.Join(w.managedRoot, "s1")); err != nil {
		t.Fatalf("child refused: %v", err)
	}
	if _, err := w.validateManagedPath(w.managedRoot); err == nil {
		t.Fatal("managed root itself must be refused")
	}
	if _, err := w.validateManagedPath(w.managedRoot + "-sibling"); err == nil {
		t.Fatal("prefix sibling must be refused")
	}
}
