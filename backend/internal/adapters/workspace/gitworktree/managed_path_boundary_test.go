package gitworktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestValidateManagedPathBoundary pins the containment rule the pathx caller
// migration must keep: the managed root itself and a prefix sibling are
// outside, a child is inside.
func TestValidateManagedPathBoundary(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "managed")
	sibling := filepath.Join(parent, "managed-sibling")
	child := filepath.Join(root, "proj", "sess")
	for _, d := range []string{root, sibling, child} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": root}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var _ ports.Workspace = ws
	for name, path := range map[string]string{"root itself": ws.managedRoot, "prefix sibling": sibling} {
		if _, err := ws.validateManagedPath(path); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: err = %v, want ErrUnsafePath", name, err)
		}
	}
	if _, err := ws.validateManagedPath(child); err != nil {
		t.Errorf("child rejected: %v", err)
	}
}
