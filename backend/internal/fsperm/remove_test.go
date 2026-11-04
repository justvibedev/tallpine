//go:build !windows

package fsperm

import (
	"os"
	"path/filepath"
	"testing"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
}

func TestRemoveAllRepairingReadOnlyTree(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	root := filepath.Join(t.TempDir(), "tree")
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{deep, filepath.Join(root, "a"), root} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveAllRepairing(root); err != nil {
		t.Fatalf("RemoveAllRepairing: %v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("tree still present: %v", err)
	}
}

func TestRemoveAllRepairingMissingPathIsSuccess(t *testing.T) {
	t.Parallel()
	if err := RemoveAllRepairing(filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatalf("missing path: %v", err)
	}
}

// A directory swapped for a symlink to an outside tree must be unlinked, never
// followed: the outside directory keeps its mode and contents.
func TestRemoveAllRepairingNeverFollowsSwappedSymlink(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outside, 0o700) })
	root := filepath.Join(t.TempDir(), "tree")
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(locked, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllRepairing(root); err != nil {
		t.Fatalf("RemoveAllRepairing: %v", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatalf("symlink target removed: %v", err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("symlink target mode = %v, want 0500 (must not be chmodded)", info.Mode().Perm())
	}
}

func TestRepairRemovePermissionsSkipsSymlinkedDirs(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outside, 0o700) })
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if RepairRemovePermissions(root) {
		t.Fatal("repair reported a change for a tree with only a symlink")
	}
	if info, _ := os.Stat(outside); info.Mode().Perm() != 0o500 {
		t.Fatalf("outside mode changed to %v", info.Mode().Perm())
	}
}
