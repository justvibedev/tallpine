package artifactversions

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// setTerminated stores the harness session as terminated (or restored).
func (h *harness) setTerminated(terminated bool) {
	h.t.Helper()
	rec, ok, err := h.store.GetSession(h.ctx, h.sess.ID)
	if err != nil || !ok {
		h.t.Fatalf("read session: %v %t", err, ok)
	}
	rec.IsTerminated = terminated
	if err := h.store.UpdateSession(h.ctx, rec); err != nil {
		h.t.Fatal(err)
	}
}

func TestPruneRegenerableRefusesASessionRestoredSinceTheSnapshot(t *testing.T) {
	h := newHarness(t)
	h.write("report.html", "<h1>v1</h1>")
	h.tick()
	h.tick()
	path := filepath.Join(h.dir, "node_modules", "dep", "index.js")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The cleanup snapshot saw the session terminated; it was restored before
	// the prune ran.
	h.setTerminated(true)
	h.setTerminated(false)

	freed, err := h.cap.PruneRegenerable(h.ctx, h.sess.ID, time.Now().Add(time.Hour))
	if !errors.Is(err, ErrPruneRefused) || freed != 0 {
		t.Fatalf("prune of a live session = %d, %v; want ErrPruneRefused", freed, err)
	}
	for _, kept := range []string{".versions", "node_modules/dep/index.js", "report.html"} {
		if _, err := os.Stat(filepath.Join(h.dir, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("%s removed from a live session: %v", kept, err)
		}
	}
	if len(h.versions("report.html")) == 0 {
		t.Fatal("version rows of a live session were deleted")
	}

	// Terminated again, but more recently than the cutoff: still refused.
	h.setTerminated(true)
	if _, err := h.cap.PruneRegenerable(h.ctx, h.sess.ID, time.Now().Add(-time.Hour)); !errors.Is(err, ErrPruneRefused) {
		t.Fatalf("prune within retention = %v, want ErrPruneRefused", err)
	}
	if _, err := h.cap.PruneRegenerable(h.ctx, "unknown-1", time.Now().Add(time.Hour)); !errors.Is(err, ErrPruneRefused) {
		t.Fatalf("prune of an unknown session = %v, want ErrPruneRefused", err)
	}
}

func TestPruneRegenerableKeepsDeliverablesAndDropsHistoryAndToolDirs(t *testing.T) {
	h := newHarness(t)
	h.write("report.html", "<h1>v1</h1>")
	h.tick()
	h.tick()
	h.write("report.html", "<h1>v2</h1>")
	h.tick()
	h.tick()
	if len(h.versions("report.html")) == 0 || h.blobCount() == 0 {
		t.Fatal("setup: no version history captured")
	}
	for _, rel := range []string{"node_modules/left-pad/index.js", "tool/.venv/bin/python", "a/b/__pycache__/x.pyc", "notes/venv.md"} {
		path := filepath.Join(h.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(h.dir, "node_modules", "linked")); err != nil {
			t.Fatal(err)
		}
	}

	h.setTerminated(true)
	freed, err := h.cap.PruneRegenerable(h.ctx, h.sess.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if freed < 15 {
		t.Fatalf("freed = %d, want at least the three tool files", freed)
	}
	for _, gone := range []string{".versions", "node_modules", "tool/.venv", "a/b/__pycache__"} {
		if _, err := os.Lstat(filepath.Join(h.dir, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Fatalf("%s survived the prune: %v", gone, err)
		}
	}
	for _, kept := range []string{"report.html", "notes/venv.md", "tool", "a/b"} {
		if _, err := os.Stat(filepath.Join(h.dir, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("deliverable %s removed: %v", kept, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(h.dir, "report.html")); string(got) != "<h1>v2</h1>" {
		t.Fatalf("deliverable content changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("symlink target outside the artifact dir removed: %v", err)
	}
	if vs := h.versions("report.html"); len(vs) != 0 {
		t.Fatalf("version rows survived: %+v", vs)
	}
}

func TestPruneRegenerableOnMissingDirIsANoop(t *testing.T) {
	h := newHarness(t)
	h.setTerminated(true)
	if err := os.RemoveAll(h.dir); err != nil {
		t.Fatal(err)
	}
	if freed, err := h.cap.PruneRegenerable(h.ctx, h.sess.ID, time.Now().Add(time.Hour)); err != nil || freed != 0 {
		t.Fatalf("PruneRegenerable on a missing dir = %d, %v", freed, err)
	}
}
