package sessionartifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestDir_JoinsDataDirArtifactsAndID(t *testing.T) {
	got := Dir("/data", domain.SessionID("mer-1"))
	want := filepath.Join("/data", "artifacts", "mer", "mer-1")
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestDir_EmptyDataDirReturnsEmpty(t *testing.T) {
	if got := Dir("", domain.SessionID("mer-1")); got != "" {
		t.Fatalf("Dir with empty dataDir = %q, want empty", got)
	}
	if got := Dir("   ", domain.SessionID("mer-1")); got != "" {
		t.Fatalf("Dir with whitespace-only dataDir = %q, want empty", got)
	}
}

func TestList_ClassifiesByExtensionAndSortsByPath(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"b.html": "<p>x</p>", "a.md": "# x", "c.bin": "x", "noext": "<!doctype html><html></html>"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]domain.SessionArtifactKind{}
	var paths []string
	for _, f := range files {
		got[f.Path] = f.Kind
		paths = append(paths, f.Path)
	}
	if !sort.StringsAreSorted(paths) || len(paths) != 4 {
		t.Fatalf("paths = %v, want 4 sorted", paths)
	}
	if got["b.html"] != domain.SessionArtifactHTML || got["a.md"] != domain.SessionArtifactMarkdown || got["c.bin"] != domain.SessionArtifactGeneric || got["noext"] != domain.SessionArtifactGeneric {
		t.Fatalf("kinds = %v", got)
	}
}

func TestList_SkipsOSMetadataAndToolFiles(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"report.md",
		"site/index.html",
		".DS_Store",
		"site/.DS_Store",
		"._report.md",
		"Thumbs.db",
		"site/desktop.ini",
		".git/HEAD",
		".versions/blobs/x",
		"node_modules/pkg/index.js",
		"site/__pycache__/gen.pyc",
		"venv/lib/python3/site.py",
		".venv/bin/python",
		"analysis/.venv/lib/x.py",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if fmt.Sprint(paths) != "[report.md site/index.html]" {
		t.Fatalf("paths = %v, want [report.md site/index.html]", paths)
	}
}

func TestList_BoundsFileCount(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxFiles+25; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != MaxFiles {
		t.Fatalf("len = %d, want %d", len(files), MaxFiles)
	}
}

func TestList_SkipsUnreadableSubdirInsteadOfFailing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not enforced on Windows")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List returned error for unreadable subdir: %v", err)
	}
	if len(files) != 1 || files[0].Path != "ok.md" {
		t.Fatalf("files = %+v, want only ok.md", files)
	}
}

func TestList_BoundsTraversalOfEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	const maxVisited = 20
	for i := 0; i < maxVisited+50; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("d%05d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "zzz.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, _, err := walkBounded(context.Background(), dir, maxVisited)
	if err != nil {
		t.Fatalf("walkBounded: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %+v, want the traversal cut off before reaching zzz.txt", files)
	}
}

func TestList_SkipsTopLevelVersionsDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"page.html",
		filepath.Join(VersionsDir, "blobs", "ab", "abcd"),
		filepath.Join("nested", VersionsDir, "kept.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := List(context.Background(), dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{"nested/" + VersionsDir + "/kept.md", "page.html"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestList_StopsOnCancelledContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("List with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestIgnoredPath(t *testing.T) {
	for rel, want := range map[string]bool{
		"report.html":                 false,
		"venv/lib/site.py":            true,
		"tool/.venv/bin/python":       true,
		"web/node_modules/a/index.js": true,
		"src/__pycache__/x.pyc":       true,
		".git/HEAD":                   true,
		"notes/.DS_Store":             true,
		"docs/venv-guide.md":          false,
	} {
		if got := IgnoredPath(rel); got != want {
			t.Errorf("IgnoredPath(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestRemoveIfEmpty(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(filepath.Join(empty, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := RemoveIfEmpty(empty); err != nil || !ok {
		t.Fatalf("empty tree: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty tree still exists: %v", err)
	}
	full := filepath.Join(root, "full")
	if err := os.MkdirAll(filepath.Join(full, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "a", "f.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := RemoveIfEmpty(full); err != nil || ok {
		t.Fatalf("non-empty tree: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(full, "a", "f.md")); err != nil {
		t.Fatalf("file removed: %v", err)
	}
	if ok, err := RemoveIfEmpty(filepath.Join(root, "missing")); err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
}
