//go:build darwin && cgo

package workspacewatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsevents"
)

// Tests of the stream assert "no change" windows, which a system-wide backlog
// must not break through an unconditional catch-up report.
func init() { fseventsLagSettings.threshold = time.Hour }

// B596: the production filter on a stream that delivers nothing still reports
// a real write, through the watchdog, within a few seconds.
func TestFSEventsDelayedStreamStillReportsChangeByPolling(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "file.txt")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	git := discoverGitWorkspace(ctx, root)
	canary, err := newFSEventsCanary()
	if err != nil {
		t.Fatal(err)
	}
	defer canary.close()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	f := &fseventsFilter{root: root, realRoot: realRoot, git: git, metadataFiles: map[string]struct{}{}, ignoredDirs: map[string]struct{}{}, canary: canary}
	f.watchdog = newLagWatchdog(root, lagSettings{tick: 10 * time.Millisecond, touchEvery: 10 * time.Millisecond, threshold: 30 * time.Millisecond, pollEvery: 20 * time.Millisecond, retouchAfter: time.Minute},
		canary.touch,
		func(ctx context.Context) (uint64, error) { return workspaceFingerprint(ctx, root, git) },
		func() { notifyChange(changes) })

	delayed := make(chan []fsevents.Event) // never delivers: the stream is behind
	go f.watchdog.run(ctx)
	go f.consume(ctx, delayed, changes)

	waitForChange(t, changes) // the unconditional catch-up
	drainChanges(changes)
	time.Sleep(100 * time.Millisecond) // polls on an unchanged tree
	drainChanges(changes)

	// A new mtime, set explicitly so a coarse clock cannot hide the edit.
	future := time.Now().Add(time.Minute)
	if err := os.WriteFile(file, []byte("two!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes)
}

// The production constructor must actually run the watchdog: with a threshold
// no canary can meet, it reports lag without any file having changed.
func TestWatchRootFSEventsRunsTheLagWatchdog(t *testing.T) {
	previous := fseventsLagSettings
	fseventsLagSettings = lagSettings{tick: 10 * time.Millisecond, touchEvery: 10 * time.Millisecond, threshold: time.Nanosecond, pollEvery: time.Hour, retouchAfter: time.Hour}
	t.Cleanup(func() { fseventsLagSettings = previous })

	root := t.TempDir()
	runGit(t, root, "init")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	waitForChange(t, changes)
}

// The watchdog depends on FSEvents reporting the canary at the exact path
// parse expects, so check that against a real stream.
func TestFSEventsDeliversCanaryAtParsedPath(t *testing.T) {
	canary, err := newFSEventsCanary()
	if err != nil {
		t.Fatal(err)
	}
	defer canary.close()
	stream := &fsevents.EventStream{
		Paths:   []string{canary.dir},
		Latency: fseventsLatency,
		Flags:   fsevents.FileEvents | fsevents.NoDefer,
		Events:  make(chan []fsevents.Event, 16),
	}
	if err := stream.Start(); err != nil {
		t.Fatalf("start stream: %v", err)
	}
	defer stopFSEventStream(stream)
	seq, err := canary.touch()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(fsEventsHealthDeadline)
	for {
		select {
		case batch := <-stream.Events:
			for _, event := range batch {
				if got, ok := canary.parse(filepath.Clean(event.Path)); ok && got == seq {
					return
				}
			}
		case <-deadline:
			t.Skip("FSEvents is behind system-wide; the canary did not arrive")
		}
	}
}
