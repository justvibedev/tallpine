package usage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func writeClaudeTranscript(t testing.TB, root, project, nativeID string) string {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, nativeID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ageClaudeDir moves a directory's mtime out of the racy window, as if it had
// last changed long before any listing.
func ageClaudeDir(t testing.TB, dir string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
}

// countClaudeReads records every directory read made through the index.
func countClaudeReads(index *claudeProjectIndex) map[string]int {
	counts := make(map[string]int)
	inner := index.readNames
	index.readNames = func(dir string) ([]string, error) {
		counts[dir]++
		return inner(dir)
	}
	return counts
}

func TestClaudeProjectIndexRereadsOnlyChangedDirs(t *testing.T) {
	root := t.TempDir()
	first := writeClaudeTranscript(t, root, "p1", "a")
	writeClaudeTranscript(t, root, "p2", "b")
	ageClaudeDir(t, filepath.Dir(first))
	ageClaudeDir(t, filepath.Join(root, "p2"))

	index := newClaudeProjectIndex(root)
	reads := countClaudeReads(index)
	mustNoError(t, index.refresh(context.Background()))
	if len(reads) != 2 {
		t.Fatalf("first refresh reads = %v, want both dirs", reads)
	}
	clear(reads)

	mustNoError(t, index.refresh(context.Background()))
	if len(reads) != 0 {
		t.Fatalf("unchanged refresh reads = %v, want none", reads)
	}
	if got := index.lookup("a.jsonl"); len(got) != 1 || got[0] != first {
		t.Fatalf("lookup from cache = %v, want %s", got, first)
	}

	added := writeClaudeTranscript(t, root, "p1", "c")
	mustNoError(t, index.refresh(context.Background()))
	if len(reads) != 1 || reads[filepath.Dir(added)] != 1 {
		t.Fatalf("refresh after add reads = %v, want only p1", reads)
	}
	if got := index.lookup("c.jsonl"); len(got) != 1 || got[0] != added {
		t.Fatalf("lookup of new transcript = %v, want %s", got, added)
	}
}

// A file created in the same mtime tick as a listing leaves the mtime equal, so
// a recently modified directory has to be listed again.
func TestClaudeProjectIndexRelistsDirInsideRacyWindow(t *testing.T) {
	root := t.TempDir()
	path := writeClaudeTranscript(t, root, "p1", "a")
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	mustNoError(t, err)

	index := newClaudeProjectIndex(root)
	mustNoError(t, index.refresh(context.Background()))

	writeClaudeTranscript(t, root, "p1", "b")
	mustNoError(t, os.Chtimes(dir, info.ModTime(), info.ModTime()))
	mustNoError(t, index.refresh(context.Background()))
	if got := index.lookup("b.jsonl"); len(got) != 1 {
		t.Fatalf("same-tick transcript lookup = %v, want it found", got)
	}
}

func TestClaudeProjectIndexDoesNotCacheFailedRead(t *testing.T) {
	root := t.TempDir()
	path := writeClaudeTranscript(t, root, "p1", "a")
	ageClaudeDir(t, filepath.Dir(path))

	index := newClaudeProjectIndex(root)
	working := index.readNames
	index.readNames = func(string) ([]string, error) { return nil, errors.New("transient") }
	mustNoError(t, index.refresh(context.Background()))
	if got := index.lookup("a.jsonl"); len(got) != 0 {
		t.Fatalf("lookup after failed read = %v, want none", got)
	}

	index.readNames = working
	mustNoError(t, index.refresh(context.Background()))
	if got := index.lookup("a.jsonl"); len(got) != 1 || got[0] != path {
		t.Fatalf("lookup after recovery = %v, want %s", got, path)
	}
}

func TestClaudeProjectIndexFollowsSymlinksAndSkipsFiles(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	writeClaudeTranscript(t, target, "real", "linked")
	if err := os.Symlink(filepath.Join(target, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stray.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	index := newClaudeProjectIndex(root)
	mustNoError(t, index.refresh(context.Background()))
	want := filepath.Join(root, "alias", "linked.jsonl")
	if got := index.lookup("linked.jsonl"); len(got) != 1 || got[0] != want {
		t.Fatalf("symlinked lookup = %v, want %s", got, want)
	}
	if got := index.lookup("stray.jsonl"); len(got) != 0 {
		t.Fatalf("file at root indexed: %v", got)
	}
}

func TestClaudeProjectIndexDropsRemovedDirs(t *testing.T) {
	root := t.TempDir()
	path := writeClaudeTranscript(t, root, "p1", "a")
	index := newClaudeProjectIndex(root)
	mustNoError(t, index.refresh(context.Background()))

	mustNoError(t, os.RemoveAll(filepath.Dir(path)))
	mustNoError(t, index.refresh(context.Background()))
	if got := index.lookup("a.jsonl"); len(got) != 0 {
		t.Fatalf("lookup after removal = %v, want none", got)
	}
}

func TestClaudeProjectIndexStopsOnCanceledContext(t *testing.T) {
	root := t.TempDir()
	writeClaudeTranscript(t, root, "p1", "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newClaudeProjectIndex(root).refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh err = %v, want context.Canceled", err)
	}
}

func TestDiscoverPathPicksNewestAndDropsDeletedTranscripts(t *testing.T) {
	root := t.TempDir()
	older := writeClaudeTranscript(t, root, "p1", "dup")
	newer := writeClaudeTranscript(t, root, "p2", "dup")
	past := time.Now().Add(-time.Hour)
	mustNoError(t, os.Chtimes(older, past, past))

	store := collectorTestStore(t)
	collector := NewCollector(store, SourceRoots{ClaudeProjects: root}, nil)
	got, err := collector.discoverPath(context.Background(), domain.HarnessClaudeCode, "dup")
	mustNoError(t, err)
	if got != newer {
		t.Fatalf("discoverPath = %q, want newest %q", got, newer)
	}

	// The index still lists it; the path must not be returned once gone.
	mustNoError(t, os.Remove(newer))
	got, err = collector.discoverIndexedPath(context.Background(), domain.HarnessClaudeCode, "dup")
	mustNoError(t, err)
	if got != older {
		t.Fatalf("discoverIndexedPath after delete = %q, want %q", got, older)
	}
}

// One discovery pass must read each project directory at most once no matter
// how many bindings it resolves, and an idle pass must read none. Before the
// index, every binding reopened every directory.
func TestReconcileSourcesReadsProjectDirsOncePerPass(t *testing.T) {
	root := t.TempDir()
	store := collectorTestStore(t)
	now := time.Now().UTC()
	const sessions = 3
	for i := range sessions {
		nativeID := fmt.Sprintf("native-%d", i)
		session := collectorTestSession(t, store, domain.HarnessClaudeCode, nativeID, false)
		seedCollectorUsageBinding(t, store, session, nativeID, domain.UsageBindingDiscovering, now, domain.UsageErrorSourceDiscoveryPending)
		writeClaudeTranscript(t, root, fmt.Sprintf("project-%d", i), nativeID)
		ageClaudeDir(t, filepath.Join(root, fmt.Sprintf("project-%d", i)))
	}
	const idleDirs = 5
	for i := range idleDirs {
		writeClaudeTranscript(t, root, fmt.Sprintf("idle-%d", i), fmt.Sprintf("other-%d", i))
		ageClaudeDir(t, filepath.Join(root, fmt.Sprintf("idle-%d", i)))
	}

	collector := NewCollector(store, SourceRoots{ClaudeProjects: root}, nil)
	reads := countClaudeReads(collector.claudeProjects)
	stats := make(map[string]int)
	statDir := collector.claudeProjects.statDir
	collector.claudeProjects.statDir = func(dir string) (os.FileInfo, error) {
		stats[dir]++
		return statDir(dir)
	}

	mustNoError(t, collector.ReconcileSources(context.Background(), 0))
	if len(reads) != sessions+idleDirs {
		t.Fatalf("first pass read %d dirs, want %d: %v", len(reads), sessions+idleDirs, reads)
	}
	for dir, count := range reads {
		if count != 1 {
			t.Fatalf("first pass read %s %d times, want once", dir, count)
		}
	}
	assertOncePerDir := func(pass string) {
		t.Helper()
		if len(stats) != sessions+idleDirs {
			t.Fatalf("%s stat %d dirs, want %d: %v", pass, len(stats), sessions+idleDirs, stats)
		}
		for dir, count := range stats {
			if count != 1 {
				t.Fatalf("%s stat %s %d times, want once", pass, dir, count)
			}
		}
	}
	assertOncePerDir("first pass")
	clear(reads)
	clear(stats)

	mustNoError(t, collector.ReconcileSources(context.Background(), 0))
	if len(reads) != 0 {
		t.Fatalf("idle pass read dirs: %v", reads)
	}
	assertOncePerDir("idle pass")
}

// BenchmarkClaudeDiscoveryPass measures one discovery pass over a tree shaped
// like a long-lived install: many project directories, 64 bindings.
func BenchmarkClaudeDiscoveryPass(b *testing.B) {
	root := b.TempDir()
	const dirs = 500
	for i := range dirs {
		project := fmt.Sprintf("project-%d", i)
		writeClaudeTranscript(b, root, project, fmt.Sprintf("native-%d", i))
		ageClaudeDir(b, filepath.Join(root, project))
	}
	collector := &Collector{claudeProjects: newClaudeProjectIndex(root)}
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if err := collector.claudeProjects.refresh(ctx); err != nil {
			b.Fatal(err)
		}
		for binding := range defaultDiscoveryLimit {
			path, err := collector.discoverIndexedPath(ctx, domain.HarnessClaudeCode, fmt.Sprintf("native-%d", binding))
			if err != nil || path == "" {
				b.Fatalf("binding %d: path=%q err=%v", binding, path, err)
			}
		}
	}
}
