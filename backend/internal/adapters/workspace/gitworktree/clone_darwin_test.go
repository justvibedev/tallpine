//go:build darwin

package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCloneDirIsCopyOnWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "main", "node_modules")
	dst := filepath.Join(root, "wt", "node_modules")
	writeTree(t, src, map[string]string{"pkg/index.js": "original", "pkg/untouched.js": "same", "empty/.keep": ""})
	if err := os.Chmod(filepath.Join(src, "pkg", "index.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../pkg/index.js", filepath.Join(src, "empty", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cloneDir(src, dst); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) {
			t.Skipf("temp dir is not on a clone-capable APFS volume: %v", err)
		}
		t.Fatalf("cloneDir: %v", err)
	}

	requireFile(t, filepath.Join(dst, "pkg", "index.js"), "original")
	if info, err := os.Stat(filepath.Join(dst, "pkg", "index.js")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, %v; want 0755", info, err)
	}
	if got, err := os.Readlink(filepath.Join(dst, "empty", "link")); err != nil || got != "../pkg/index.js" {
		t.Fatalf("relative symlink = %q, %v", got, err)
	}

	// Writes on either side must not show through to the other.
	if err := os.WriteFile(filepath.Join(dst, "pkg", "index.js"), []byte("changed in clone"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(src, "pkg", "index.js"), "original")
	if err := os.WriteFile(filepath.Join(src, "pkg", "untouched.js"), []byte("changed in source"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(dst, "pkg", "untouched.js"), "same")
	if err := os.Remove(filepath.Join(dst, "empty", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(src, "empty", "link")); err != nil {
		t.Fatalf("removing the clone's link touched the source: %v", err)
	}

	if err := cloneDir(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("clone onto existing destination = %v, want EEXIST", err)
	}
}

func TestSeedEntryClonesWholeDirectoryWithOneCall(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main")
	dst := filepath.Join(root, "wt")
	writeTree(t, src, map[string]string{"node_modules/a/index.js": "a", "node_modules/b/index.js": "b"})
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := spyCloneDir(t, func(src, dst string) error {
		err := cloneDir(src, dst)
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) {
			t.Skipf("temp dir is not on a clone-capable APFS volume: %v", err)
		}
		return err
	})

	if err := seedWorktreeIncludeEntry(context.Background(), src, filepath.Join(src, "node_modules"), filepath.Join(dst, "node_modules"), true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	requireFile(t, filepath.Join(dst, "node_modules", "a", "index.js"), "a")
	requireFile(t, filepath.Join(dst, "node_modules", "b", "index.js"), "b")
	if len(*calls) != 1 || (*calls)[0] != filepath.Join(src, "node_modules") {
		t.Fatalf("clone calls = %v; want exactly one on the node_modules directory", *calls)
	}
}
