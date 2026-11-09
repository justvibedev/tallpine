package workspacewatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeWatchdog drives a lagWatchdog on a hand-moved clock, without its goroutine.
type fakeWatchdog struct {
	*lagWatchdog
	clock        time.Time
	seq          uint64
	fingerprint  uint64
	fingerprints int
	notifies     int
	scan         func() // runs inside each fingerprint
}

func newFakeWatchdog() *fakeWatchdog {
	f := &fakeWatchdog{clock: time.Unix(1_000, 0)}
	f.lagWatchdog = newLagWatchdog("/root", lagSettings{tick: time.Second, touchEvery: 2 * time.Second, threshold: 2 * time.Second, pollEvery: 2 * time.Second, retouchAfter: 30 * time.Second},
		func() (uint64, error) { f.seq++; return f.seq, nil },
		func(context.Context) (uint64, error) {
			f.fingerprints++
			if f.scan != nil {
				f.scan()
			}
			return f.fingerprint, nil
		},
		func() { f.notifies++ })
	f.now = func() time.Time { return f.clock }
	return f
}

func (f *fakeWatchdog) advance(seconds int) {
	for range seconds {
		f.clock = f.clock.Add(time.Second)
		f.onTick(context.Background())
	}
}

// deliver hands the watchdog a canary the stream delivered at the current time.
func (f *fakeWatchdog) deliver(seq uint64) {
	f.onCanary(canaryReport{seq: seq, at: f.clock})
}

func TestLagWatchdogHealthyStreamNeverScansOrNotifies(t *testing.T) {
	f := newFakeWatchdog()
	for range 60 {
		f.advance(1)
		if f.outstanding {
			f.deliver(f.seq) // delivered within the same tick: a healthy stream
		}
	}
	if f.fingerprints != 0 || f.notifies != 0 {
		t.Fatalf("healthy stream: %d fingerprints, %d notifies; want none", f.fingerprints, f.notifies)
	}
	if f.seq == 0 {
		t.Fatal("no canary was ever written")
	}
}

// B596: a stream delivering nothing must not hold back a change for more than a
// few seconds, and reports without a change must stop.
func TestLagWatchdogPollsAndReportsWhileStreamIsDelayed(t *testing.T) {
	f := newFakeWatchdog()
	f.advance(1) // first canary out, never answered
	f.advance(2)
	if f.notifies != 1 {
		t.Fatalf("after the threshold: %d notifies; want the one unconditional catch-up", f.notifies)
	}

	f.advance(10)
	if f.notifies != 1 {
		t.Fatalf("no change while lagging: %d notifies; want 1", f.notifies)
	}

	f.fingerprint = 42 // a write the stream has not delivered
	f.advance(2)
	if f.notifies != 2 {
		t.Fatalf("changed fingerprint: %d notifies; want 2", f.notifies)
	}
}

func TestLagWatchdogStopsPollingOnceCanaryIsOnTimeAgain(t *testing.T) {
	f := newFakeWatchdog()
	f.advance(3)
	lateSeq := f.seq

	// The old canary finally arrives after 20 s: delivered, but still far behind.
	f.advance(20)
	f.deliver(lateSeq)
	if !f.lagging {
		t.Fatal("a canary delivered late must not end the lag episode")
	}

	f.advance(2) // next canary goes out and comes back immediately
	f.deliver(f.seq)
	if f.lagging {
		t.Fatal("an on-time canary must end the lag episode")
	}
	scans := f.fingerprints
	for range 30 {
		f.advance(1)
		if f.outstanding {
			f.deliver(f.seq)
		}
	}
	if f.fingerprints != scans {
		t.Fatalf("polling continued after recovery: %d more fingerprints", f.fingerprints-scans)
	}
}

// The loop that reads canary reports also runs the scan, so an on-time canary
// can wait behind a scan far longer than the threshold. That wait is not lag.
func TestLagWatchdogEndsLagWhenOnTimeCanaryWaitsBehindSlowScan(t *testing.T) {
	f := newFakeWatchdog()
	f.advance(3)
	f.deliver(f.seq) // the overdue canary lands, leaving none outstanding

	f.lastPoll = f.clock.Add(-time.Hour) // so the tick below sends a canary and scans
	f.scan = func() {
		f.canarySeen(f.seq) // the stream delivers it at once
		f.clock = f.clock.Add(5 * time.Second)
	}
	f.advance(1)
	f.scan = nil
	if !f.lagging {
		t.Fatal("the tick should not have ended the episode by itself")
	}
	f.onCanary(<-f.seen)
	if f.lagging {
		t.Fatal("an on-time canary held up by a slow scan must end the lag episode")
	}
}

func TestLagWatchdogIgnoresCanaryItAlreadyReplaced(t *testing.T) {
	f := newFakeWatchdog()
	f.advance(1)
	first := f.seq
	f.advance(30) // past retouchAfter, so a replacement went out
	if f.seq == first {
		t.Fatal("a lost canary was not replaced")
	}
	f.deliver(first)
	if !f.outstanding || !f.lagging {
		t.Fatal("a stale canary must not count as the outstanding round trip")
	}
}

func TestFingerprintSeesWhatTheModelDependsOn(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored/\n")
	writeFile(t, filepath.Join(root, "tracked.txt"), "one")
	runGit(t, root, "add", "tracked.txt", ".gitignore")
	ctx := context.Background()
	git := discoverGitWorkspace(ctx, root)
	if !git.available {
		t.Fatal("git workspace not discovered")
	}
	fingerprint := func() uint64 {
		t.Helper()
		value, err := workspaceFingerprint(ctx, root, git)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	base := fingerprint()
	if again := fingerprint(); again != base {
		t.Fatal("fingerprint is not stable on an unchanged tree")
	}

	writeFile(t, filepath.Join(root, "ignored", "cache.bin"), "x")
	if got := fingerprint(); got != base {
		t.Fatal("a change in an ignored directory moved the fingerprint")
	}

	writeFile(t, filepath.Join(root, "tracked.txt"), "changed content")
	edited := fingerprint()
	if edited == base {
		t.Fatal("editing a tracked file did not move the fingerprint")
	}

	writeFile(t, filepath.Join(root, "new.txt"), "x")
	created := fingerprint()
	if created == edited {
		t.Fatal("a new untracked file did not move the fingerprint")
	}

	if err := os.Remove(filepath.Join(root, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	if fingerprint() == created {
		t.Fatal("deleting a tracked file did not move the fingerprint")
	}
}

func TestFingerprintWalksAPlainDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.md"), "one")
	ctx := context.Background()
	base, err := workspaceFingerprint(ctx, root, gitWorkspace{})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sub", "b.md"), "two")
	changed, err := workspaceFingerprint(ctx, root, gitWorkspace{})
	if err != nil {
		t.Fatal(err)
	}
	if changed == base {
		t.Fatal("a new file did not move the fingerprint")
	}
}

func TestFSEventsCanaryNamesAndCleanup(t *testing.T) {
	base := t.TempDir()
	restore := canaryBaseDir
	canaryBaseDir = func() string { return base }
	t.Cleanup(func() { canaryBaseDir = restore })

	stale := filepath.Join(base, "root-stale")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	canary, err := newFSEventsCanary()
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Fatal("a canary directory left by a killed daemon was not swept")
	}
	first, err := canary.touch()
	if err != nil {
		t.Fatal(err)
	}
	second, err := canary.touch()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(canary.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("canary directory holds %d files; want only the latest", len(entries))
	}
	if seq, ok := canary.parse(canary.path(second)); !ok || seq != second || first == second {
		t.Fatalf("parse(%q) = %d, %v", canary.path(second), seq, ok)
	}
	if _, ok := canary.parse(filepath.Join(filepath.Dir(canary.dir), "c-1")); ok {
		t.Fatal("parse accepted a file outside the canary directory")
	}
	canary.close()
	if _, statErr := os.Stat(canary.dir); !os.IsNotExist(statErr) {
		t.Fatal("close left the canary directory behind")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
