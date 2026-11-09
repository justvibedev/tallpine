//go:build darwin && cgo

package workspacewatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsevents"
)

// FSEvents watches recursively, so it also reports activity inside ignored
// trees that kqueue never registered. An install filling node_modules must
// stay silent, and must not cost one git process per file.
func TestFSEventsIgnoresChurnInIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "tallpine@example.com")
	runGit(t, root, "config", "user.name", "Tallpine Tests")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, "README.md")
	if err := os.WriteFile(tracked, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".gitignore", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	settleFSEventsOrSkip(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	for p := range 20 {
		dir := filepath.Join(root, "node_modules", fmt.Sprintf("pkg-%02d", p), "lib")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := range 10 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.js", f)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case <-changes:
		t.Fatal("writes inside an ignored directory produced a workspace change")
	case <-time.After(500 * time.Millisecond):
	}

	if err := os.WriteFile(tracked, []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForFSEventsChange(t, root, changes)
}

// A change to the rules un-ignores what they no longer match.
func TestFSEventsForgetsIgnoredDirectoriesWhenGitignoreChanges(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := filepath.Join(root, "build")
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}
	settleFSEventsOrSkip(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(build, "out.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
		t.Fatal("write inside the ignored build/ produced a change")
	case <-time.After(500 * time.Millisecond):
	}
	if err := os.WriteFile(ignore, []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForFSEventsChange(t, root, changes)
	drainChanges(changes)
	if err := os.WriteFile(filepath.Join(build, "out.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForFSEventsChange(t, root, changes)
}

// TestFSEventsFilterReportsOnlyRelevantBatches covers the filter with
// synthetic batches, so its rules are proven even when FSEvents itself is
// behind and the stream tests above skip.
func TestFSEventsFilterReportsOnlyRelevantBatches(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	ignore := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignore, []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := &fseventsFilter{root: root, realRoot: realRoot, git: discoverGitWorkspace(ctx, root), ignoredDirs: map[string]struct{}{}}
	event := func(rel string, flags fsevents.EventFlags) []fsevents.Event {
		return []fsevents.Event{{Path: filepath.Join(realRoot, rel), Flags: flags}}
	}
	const created = fsevents.ItemIsFile | fsevents.ItemCreated

	for _, step := range []struct {
		name  string
		batch []fsevents.Event
		want  bool
	}{
		{"the ignored directory itself", event("build", fsevents.ItemIsDir|fsevents.ItemCreated), false},
		{"a file in the ignored directory", event("build/out.txt", created), false},
		{"a metadata-only change to a tracked path", event("README.md", fsevents.ItemIsFile|fsevents.ItemInodeMetaMod), false},
		{"inside .git", event(".git/tallpine-test", created), false},
		{"a tracked path", event("README.md", created), true},
		{"a dropped-events rescan", event("build/out.txt", fsevents.MustScanSubDirs), true},
	} {
		if got := f.relevant(ctx, step.batch); got != step.want {
			t.Errorf("%s: relevant = %v, want %v", step.name, got, step.want)
		}
	}

	if err := os.WriteFile(ignore, []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !f.relevant(ctx, event(".gitignore", fsevents.ItemIsFile|fsevents.ItemModified)) {
		t.Error("a .gitignore change was not relevant")
	}
	if !f.relevant(ctx, event("build/out.txt", created)) {
		t.Error("a file in a directory no longer ignored was not relevant")
	}
}

// fsEventsHealthDeadline is how long the OS may take to deliver a fresh event
// before FSEvents counts as behind.
const fsEventsHealthDeadline = 20 * time.Second

// settleFSEventsOrSkip blocks until FSEvents has delivered everything written
// below root so far. A stream started "since now" can still receive events
// from just before it started (seen as the .git/config write of the test's
// own git init), and events arrive in order, so seeing a sentinel written now
// proves those are past and cannot land inside a "no change" window.
func settleFSEventsOrSkip(t *testing.T, root string) {
	t.Helper()
	if !fseventsDelivers(t, root) {
		t.Skip("FSEvents is behind system-wide (a fresh event did not arrive); cannot test the stream now")
	}
}

// waitForFSEventsChange waits for a change from the stream under test. A
// timeout fails the test only when FSEvents demonstrably still delivers, so a
// stream or filter that drops the change fails while a system-wide backlog
// skips.
func waitForFSEventsChange(t *testing.T, root string, changes <-chan struct{}) {
	t.Helper()
	for range 2 {
		select {
		case _, ok := <-changes:
			if !ok {
				t.Fatal("watch channel closed before change")
			}
			return
		case <-time.After(fsEventsHealthDeadline):
		}
		if !fseventsDelivers(t, root) {
			t.Skip("FSEvents is behind system-wide (a fresh event did not arrive); cannot test the stream now")
		}
	}
	t.Fatal("timed out waiting for workspace change while FSEvents was delivering")
}

// fseventsDelivers reports whether a sentinel written below root reaches a
// fresh FSEvents stream within fsEventsHealthDeadline.
func fseventsDelivers(t *testing.T, root string) bool {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	stream := &fsevents.EventStream{
		Paths:   []string{realRoot},
		Latency: fseventsLatency,
		Flags:   fsevents.FileEvents | fsevents.NoDefer,
		Events:  make(chan []fsevents.Event, 16),
	}
	if err := stream.Start(); err != nil {
		t.Fatalf("start sentinel stream: %v", err)
	}
	defer stopFSEventStream(stream)
	// Inside .git the sentinel is not a workspace change for the stream under test.
	sentinel := filepath.Join(realRoot, ".git", "tallpine-test-sentinel")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(fsEventsHealthDeadline)
	for {
		select {
		case batch := <-stream.Events:
			for _, event := range batch {
				if filepath.Clean(event.Path) == sentinel {
					return true
				}
			}
		case <-deadline:
			return false
		}
	}
}

// A stalled reader blocks the stream's callback, which is what makes this a
// stalled FSEvents client. Keep accepting batches while git is running.
func TestFSEventsConsumerKeepsDrainingWhileFilterIsBusy(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec /bin/sleep 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fseventsFilter{root: root, realRoot: realRoot, git: gitWorkspace{available: true}, ignoredDirs: map[string]struct{}{}}
	events := make(chan []fsevents.Event)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.consume(ctx, events, make(chan struct{}, 1))
	}()
	batch := []fsevents.Event{{Path: filepath.Join(realRoot, "a.txt"), Flags: fsevents.ItemIsFile | fsevents.ItemCreated}}
	for i := range 64 {
		select {
		case events <- batch:
		case <-time.After(1500 * time.Millisecond):
			t.Fatalf("batch %d not accepted while the filter runs git", i)
		}
	}
	cancel()
	<-done
}

func TestCoalesceFSEventsMergesWhileReaderIsBusy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan []fsevents.Event)
	out := coalesceFSEvents(ctx, events)
	for _, path := range []string{"/r/a", "/r/b"} {
		events <- []fsevents.Event{{Path: path}}
	}
	merged := <-out
	if len(merged) != 2 || merged[0].Path != "/r/a" || merged[1].Path != "/r/b" {
		t.Fatalf("merged = %+v", merged)
	}
}

func TestFSEventsBacklogPastCapBecomesRescan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan []fsevents.Event)
	out := coalesceFSEvents(ctx, events)
	events <- make([]fsevents.Event, maxPendingFSEvents+1)
	backlog := <-out
	if len(backlog) != 1 || backlog[0].Flags&fsevents.MustScanSubDirs == 0 {
		t.Fatalf("backlog has %d events, want one rescan", len(backlog))
	}
	f := &fseventsFilter{ignoredDirs: map[string]struct{}{}}
	if !f.relevant(ctx, backlog) {
		t.Fatal("rescan must report a change")
	}
}
