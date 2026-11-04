package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

func TestMarkTerminatedDiscardsOnlyThatSessionsTempDir(t *testing.T) {
	data := t.TempDir()
	m, st, _ := newManager(WithDataDir(data))
	st.sessions["mer-1"] = working("mer-1")
	st.sessions["mer-2"] = working("mer-2")
	ended, err := sessionscratch.EnsureTempDir(data, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ended, "leftover.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := sessionscratch.EnsureTempDir(data, "mer-2")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ended); !os.IsNotExist(err) {
		t.Fatalf("ended session's temp dir still in place: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live session's temp dir touched: %v", err)
	}
	trash := filepath.Join(data, sessionscratch.TempRoot, sessionscratch.TrashDir)
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(trash)
		if err == nil && len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discarded temp dir not removed: entries=%v err=%v", entries, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Terminating an already-terminated session is a no-op for scratch.
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
}

func TestReaperObservedDeathDiscardsTheTempDir(t *testing.T) {
	data := t.TempDir()
	m, st, _ := newManager(WithDataDir(data))
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec
	dir, err := sessionscratch.EnsureTempDir(data, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("setup: reaper-observed death did not terminate the session")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("crashed session's temp dir still in place: %v", err)
	}
}
