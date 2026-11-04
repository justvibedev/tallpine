package session

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink swapped in after the path check must not be followed out of the
// workspace.
func TestOpenConfinedWorkspaceFile_SymlinkSwappedAfterCheck(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterWorkspaceFileCheck = func() {
		_ = os.Remove(target)
		if err := os.Symlink(outside, target); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	t.Cleanup(func() { afterWorkspaceFileCheck = nil })

	handle, _, err := openConfinedWorkspaceFile(root, "a.txt", os.O_RDWR)
	if err == nil {
		_ = handle.Close()
		t.Fatal("opened a file that escapes the workspace after the check")
	}
}

func TestOpenConfinedWorkspaceFile_OpensRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle, info, err := openConfinedWorkspaceFile(root, "a.txt", os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if info.Size() != 5 {
		t.Fatalf("size = %d", info.Size())
	}
}
