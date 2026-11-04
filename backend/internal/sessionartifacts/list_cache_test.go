package sessionartifacts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// countingCache is a fresh listCache on a controllable clock that counts walks.
func countingCache(t *testing.T) (*listCache, *int, *time.Time) {
	t.Helper()
	c := newListCache()
	walks := 0
	now := time.Now()
	c.now = func() time.Time { return now }
	c.walk = func(ctx context.Context, dir string) ([]domain.SessionArtifactFile, map[string]time.Time, error) {
		walks++
		return walk(ctx, dir)
	}
	return c, &walks, &now
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func listPaths(t *testing.T, c *listCache, dir string) []string {
	t.Helper()
	files, err := c.list(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestListCached_ReusesWalkWhileTreeIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report.md"), "# r")
	writeTestFile(t, filepath.Join(dir, "site", "index.html"), "<p>x</p>")
	c, walks, _ := countingCache(t)

	for range 5 {
		if got := listPaths(t, c, dir); fmt.Sprint(got) != "[report.md site/index.html]" {
			t.Fatalf("paths = %v", got)
		}
	}
	if *walks != 1 {
		t.Fatalf("walks = %d, want 1 for five reads of an unchanged tree", *walks)
	}
}

func TestListCached_SeesEntriesAddedOrRemovedAnywhereInTheTree(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "site", "index.html"), "<p>x</p>")
	c, _, _ := countingCache(t)
	listPaths(t, c, dir)

	// A file added in a nested directory changes only that directory's mtime.
	writeTestFile(t, filepath.Join(dir, "site", "about.html"), "<p>y</p>")
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[site/about.html site/index.html]" {
		t.Fatalf("after nested add, paths = %v", got)
	}
	writeTestFile(t, filepath.Join(dir, "notes.md"), "n")
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[notes.md site/about.html site/index.html]" {
		t.Fatalf("after top-level add, paths = %v", got)
	}
	if err := os.Remove(filepath.Join(dir, "site", "index.html")); err != nil {
		t.Fatal(err)
	}
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[notes.md site/about.html]" {
		t.Fatalf("after nested remove, paths = %v", got)
	}
	if err := os.RemoveAll(filepath.Join(dir, "site")); err != nil {
		t.Fatal(err)
	}
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[notes.md]" {
		t.Fatalf("after directory remove, paths = %v", got)
	}
}

func TestListCached_IgnoresChurnInsideSkippedTrees(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report.md"), "# r")
	writeTestFile(t, filepath.Join(dir, VersionsDir, "blobs", "a"), "v1")
	writeTestFile(t, filepath.Join(dir, "node_modules", "pkg", "index.js"), "x")
	writeTestFile(t, filepath.Join(dir, ".venv", "lib", "site.py"), "x")
	c, walks, _ := countingCache(t)
	listPaths(t, c, dir)

	writeTestFile(t, filepath.Join(dir, VersionsDir, "blobs", "b"), "v2")
	writeTestFile(t, filepath.Join(dir, "node_modules", "pkg", "other.js"), "x")
	writeTestFile(t, filepath.Join(dir, ".venv", "lib", "more.py"), "x")
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[report.md]" {
		t.Fatalf("paths = %v", got)
	}
	if *walks != 1 {
		t.Fatalf("walks = %d, want 1: writes inside skipped trees must not invalidate the listing", *walks)
	}
}

func TestListCached_RewalksAfterTTLForInPlaceRewrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	writeTestFile(t, path, "short")
	c, walks, now := countingCache(t)
	listPaths(t, c, dir)

	// Rewriting an existing file in place leaves every directory mtime alone.
	writeTestFile(t, path, "a much longer body")
	files, err := c.list(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Size != int64(len("short")) || *walks != 1 {
		t.Fatalf("within the TTL: size = %d walks = %d, want the cached listing", files[0].Size, *walks)
	}
	*now = now.Add(listCacheTTL)
	files, err = c.list(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Size != int64(len("a much longer body")) || *walks != 2 {
		t.Fatalf("after the TTL: size = %d walks = %d, want a fresh walk", files[0].Size, *walks)
	}
}

func TestListCached_ReturnsCallerOwnedCopies(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report.md"), "# r")
	c, _, _ := countingCache(t)
	first, err := c.list(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Path = "mutated"
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[report.md]" {
		t.Fatalf("a caller's mutation leaked into the cache: %v", got)
	}
}

func TestListCached_DoesNotCacheAMissingRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifacts", "sess")
	c, _, _ := countingCache(t)
	if got := listPaths(t, c, dir); len(got) != 0 {
		t.Fatalf("missing root listed %v", got)
	}
	writeTestFile(t, filepath.Join(dir, "report.md"), "# r")
	if got := listPaths(t, c, dir); fmt.Sprint(got) != "[report.md]" {
		t.Fatalf("root created after a miss: paths = %v", got)
	}
}

func TestListCached_BoundsEntries(t *testing.T) {
	root := t.TempDir()
	c, _, now := countingCache(t)
	for i := range listCacheMaxEntries + 10 {
		dir := filepath.Join(root, fmt.Sprint(i))
		writeTestFile(t, filepath.Join(dir, "a.md"), "a")
		*now = now.Add(time.Millisecond)
		listPaths(t, c, dir)
	}
	if len(c.entries) != listCacheMaxEntries {
		t.Fatalf("entries = %d, want %d", len(c.entries), listCacheMaxEntries)
	}
	if _, ok := c.entries[filepath.Join(root, "0")]; ok {
		t.Fatal("the oldest listing was kept over newer ones")
	}
}

// benchmarkArtifactTree is a session artifact directory of the shape agents
// produce: a few reports plus a small static site, with version history.
func benchmarkArtifactTree(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	for i := range 40 {
		path := filepath.Join(dir, fmt.Sprintf("report-%02d.md", i))
		if err := os.WriteFile(path, []byte("# report"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for d := range 8 {
		for i := range 20 {
			path := filepath.Join(dir, "site", fmt.Sprintf("section-%d", d), fmt.Sprintf("page-%02d.html", i))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("<p>x</p>"), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	return dir
}

// BenchmarkList is the per-read cost GET /sessions/{id} paid before ListCached.
func BenchmarkList(b *testing.B) {
	dir := benchmarkArtifactTree(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := List(context.Background(), dir); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListCached(b *testing.B) {
	dir := benchmarkArtifactTree(b)
	c := newListCache()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.list(context.Background(), dir); err != nil {
			b.Fatal(err)
		}
	}
}
