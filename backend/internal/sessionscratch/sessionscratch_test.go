package sessionscratch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPathsRejectUnsafeNames(t *testing.T) {
	data := t.TempDir()
	for _, id := range []domain.SessionID{"", ".", "..", ".trash", "a/b", `a\b`, "a\x00b"} {
		if got := TempDir(data, id); got != "" {
			t.Errorf("TempDir(%q) = %q, want empty", id, got)
		}
	}
	for _, project := range []domain.ProjectID{".", "..", "a/b", ".hidden"} {
		if got := GradleHome(data, project); got != "" {
			t.Errorf("GradleHome(%q) = %q, want empty", project, got)
		}
	}
	if got := TempDir("", "s-1"); got != "" {
		t.Errorf("TempDir without data dir = %q", got)
	}
	if got, want := TempDir(data, "s-1"), filepath.Join(data, TempRoot, "s-1"); got != want {
		t.Errorf("TempDir = %q, want %q", got, want)
	}
	if got, want := GradleHome(data, ""), filepath.Join(data, CacheRoot, GradleDir, StandaloneGradleHome); got != want {
		t.Errorf("standalone GradleHome = %q, want %q", got, want)
	}
}

func TestEnsureTempDirCreatesPrivateDir(t *testing.T) {
	data := t.TempDir()
	dir, err := EnsureTempDir(data, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("temp dir not created: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("temp dir mode = %v, want 0700", info.Mode().Perm())
	}
	if _, err := EnsureTempDir(data, ".."); err == nil {
		t.Fatal("EnsureTempDir accepted an unsafe id")
	}
}

func TestEnsureGradleHomeLinksUserConfigurationOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	data := t.TempDir()
	user := t.TempDir()
	if err := os.WriteFile(filepath.Join(user, "gradle.properties"), []byte("signing=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(user, "init.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(user, "caches"), 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := EnsureGradleHome(data, "mer", user)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(home, "gradle.properties")); err != nil || target != filepath.Join(user, "gradle.properties") {
		t.Fatalf("gradle.properties link = %q, %v", target, err)
	}
	if _, err := os.Readlink(filepath.Join(home, "init.d")); err != nil {
		t.Fatalf("init.d not linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "caches")); !os.IsNotExist(err) {
		t.Fatalf("caches must not be shared with the user's gradle home: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, "init.gradle")); !os.IsNotExist(err) {
		t.Fatalf("absent entry linked: %v", err)
	}

	// A real file the project home already has is left alone.
	own := filepath.Join(GradleHome(data, "other"), "gradle.properties")
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureGradleHome(data, "other", user); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(own); string(got) != "mine\n" {
		t.Fatalf("existing gradle.properties replaced: %q", got)
	}
}

func TestDiscardMovesOnlyTheSessionsDir(t *testing.T) {
	data := t.TempDir()
	mine, _ := EnsureTempDir(data, "s-1")
	other, _ := EnsureTempDir(data, "s-2")
	if err := os.WriteFile(filepath.Join(mine, "big.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := Discard(data, "s-1", time.Unix(1, 0))
	if err != nil || moved == "" {
		t.Fatalf("Discard = %q, %v", moved, err)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatalf("session temp dir still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moved, "big.bin")); err != nil {
		t.Fatalf("discarded content not in trash: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another session's temp dir was touched: %v", err)
	}
	if moved, err := Discard(data, "s-1", time.Unix(2, 0)); err != nil || moved != "" {
		t.Fatalf("second Discard = %q, %v; want no-op", moved, err)
	}
	for _, id := range []domain.SessionID{"", "..", "."} {
		if moved, err := Discard(data, id, time.Unix(3, 0)); err != nil || moved != "" {
			t.Fatalf("Discard(%q) = %q, %v; want no-op", id, moved, err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, TempRoot)); err != nil {
		t.Fatalf("temp root removed by an unsafe id: %v", err)
	}
	if err := EmptyTrash(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatalf("trash entry survived EmptyTrash: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("EmptyTrash touched a live session's temp dir: %v", err)
	}
}

func TestDiscardGradleHomeMovesOnlyThatProjectsHome(t *testing.T) {
	data := t.TempDir()
	mine, _ := EnsureGradleHome(data, "mer", "")
	other, _ := EnsureGradleHome(data, "app", "")
	if err := os.WriteFile(filepath.Join(mine, "dep.jar"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := DiscardGradleHome(data, "mer", time.Unix(1, 0))
	if err != nil || moved == "" {
		t.Fatalf("DiscardGradleHome = %q, %v", moved, err)
	}
	if filepath.Dir(moved) != filepath.Join(data, CacheRoot, CacheTrashDir) {
		t.Fatalf("moved to %q, want the cache trash", moved)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatalf("project gradle home still present: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another project's gradle home was touched: %v", err)
	}
	if moved, err := DiscardGradleHome(data, "mer", time.Unix(2, 0)); err != nil || moved != "" {
		t.Fatalf("second DiscardGradleHome = %q, %v; want no-op", moved, err)
	}
	if _, err := DiscardGradleHome(data, "..", time.Unix(3, 0)); err == nil {
		t.Fatal("DiscardGradleHome accepted an unsafe project id")
	}
	if err := EmptyCacheTrash(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatalf("trash entry survived EmptyCacheTrash: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("EmptyCacheTrash touched a project's gradle home: %v", err)
	}
}

func TestRemoveTreeRepairsReadOnlyDirsAndKeepsLinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	root := filepath.Join(t.TempDir(), "tree")
	locked := filepath.Join(root, "pkg", "mod")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f.go"), []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("tree survived: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("symlink target outside the tree was removed: %v", err)
	}
	if err := RemoveTree(root); err != nil {
		t.Fatalf("removing a missing tree: %v", err)
	}
}
