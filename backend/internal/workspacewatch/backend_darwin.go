//go:build darwin && cgo

package workspacewatch

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsevents"

	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// fseventsLatency is how long FSEvents coalesces a burst before delivering it.
// NoDefer still delivers the first event of a quiet period at once.
const fseventsLatency = 50 * time.Millisecond

// fseventsContentChange are the item flags that mean content or names
// changed. Owner, xattr and inode-metadata changes alone are skipped: git
// touching files while it builds the diff model would otherwise feed a
// read -> event -> read loop, as kqueue's CHMOD did.
const fseventsContentChange = fsevents.ItemCreated | fsevents.ItemRemoved | fsevents.ItemRenamed | fsevents.ItemModified

// fseventsRescan are stream flags after which individual events may be
// missing; the model must be re-read.
const fseventsRescan = fsevents.MustScanSubDirs | fsevents.KernelDropped | fsevents.UserDropped | fsevents.RootChanged

// maxIgnoreCache bounds the remembered ignored directories of one root.
const maxIgnoreCache = 4096

// maxPendingFSEvents bounds the events held while the filter is busy.
const maxPendingFSEvents = 1 << 16

// watchRootNative watches root with one recursive FSEvents stream. kqueue,
// fsnotify's macOS backend, needs a descriptor for every directory and every
// file in it, thousands per worktree; FSEvents needs none per path.
func watchRootNative(ctx context.Context, root string) (<-chan struct{}, error) {
	changes, err := watchRootFSEvents(ctx, root)
	if err != nil {
		slog.WarnContext(ctx, "workspace watch: FSEvents unavailable, falling back to kqueue", "root", root, "err", err)
		return watchRootFSNotify(ctx, root)
	}
	return changes, nil
}

func watchRootFSEvents(ctx context.Context, root string) (<-chan struct{}, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat workspace path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path is not a directory")
	}
	// FSEvents reports resolved paths (/private/var/... for /var/...).
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	git := discoverGitWorkspace(ctx, root)
	metadataFiles := make(map[string]struct{}, len(git.metadataFiles))
	paths := []string{realRoot}
	watchedDirs := map[string]struct{}{realRoot: {}}
	for name := range git.metadataFiles {
		dir := filepath.Dir(name)
		realDir, dirErr := filepath.EvalSymlinks(dir)
		if dirErr != nil {
			continue
		}
		metadataFiles[filepath.Join(realDir, filepath.Base(name))] = struct{}{}
		if _, seen := watchedDirs[realDir]; seen || pathx.Within(realRoot, realDir) {
			continue
		}
		watchedDirs[realDir] = struct{}{}
		paths = append(paths, realDir)
	}
	canary, canaryErr := newFSEventsCanary()
	if canaryErr != nil {
		// Live updates still work; they just cannot detect a late stream.
		slog.WarnContext(ctx, "workspace watch: no FSEvents lag detection", "root", root, "err", canaryErr)
		canary = nil
	}
	if canary != nil && !pathx.Within(realRoot, canary.dir) {
		paths = append(paths, canary.dir)
	}
	stream := &fsevents.EventStream{
		Paths:   paths,
		Latency: fseventsLatency,
		Flags:   fsevents.FileEvents | fsevents.NoDefer,
		Events:  make(chan []fsevents.Event, 16),
	}
	if err := stream.Start(); err != nil {
		if canary != nil {
			canary.close()
		}
		return nil, fmt.Errorf("start FSEvents stream: %w", err)
	}
	f := &fseventsFilter{root: root, realRoot: realRoot, git: git, metadataFiles: metadataFiles, ignoredDirs: map[string]struct{}{}, canary: canary}
	changes := make(chan struct{}, 1)
	if canary != nil {
		f.watchdog = newLagWatchdog(root, fseventsLagSettings, canary.touch, func(ctx context.Context) (uint64, error) {
			return workspaceFingerprint(ctx, root, git)
		}, func() { notifyChange(changes) })
	}
	go f.run(ctx, stream, changes)
	return changes, nil
}

type fseventsFilter struct {
	root, realRoot string
	git            gitWorkspace
	metadataFiles  map[string]struct{}
	// ignoredDirs remembers git-ignored directories so a burst inside one
	// (an install filling node_modules) costs one ignore check, not one per file.
	ignoredDirs map[string]struct{}
	// canary and watchdog are nil when lag detection could not be set up.
	canary   *fseventsCanary
	watchdog *lagWatchdog
}

// fseventsLagSettings is a variable so a test can shorten the real timings.
var fseventsLagSettings = defaultLagSettings

func notifyChange(changes chan struct{}) {
	select {
	case changes <- struct{}{}:
	default:
	}
}

func (f *fseventsFilter) run(ctx context.Context, stream *fsevents.EventStream, changes chan struct{}) {
	defer close(changes)
	defer stopFSEventStream(stream)
	if f.watchdog != nil {
		// The watchdog sends on changes, so it must be gone before the deferred close.
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.watchdog.run(ctx)
		}()
		defer func() {
			<-done
			f.canary.close()
		}()
	}
	f.consume(ctx, stream.Events, changes)
}

// consume reports relevant batches until ctx ends. The filter can be slow (it
// runs git) on a loaded machine, so it reads coalesced batches: a reader held
// up here would block the stream's callback and stall this FSEvents client.
func (f *fseventsFilter) consume(ctx context.Context, events <-chan []fsevents.Event, changes chan struct{}) {
	batches := coalesceFSEvents(ctx, events)
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-batches:
			if f.relevant(ctx, batch) {
				notifyChange(changes)
			}
			// After the filter on purpose: a filter stuck on git is lag too.
			f.noteCanaries(batch)
		}
	}
}

func (f *fseventsFilter) noteCanaries(batch []fsevents.Event) {
	if f.watchdog == nil {
		return
	}
	for _, event := range batch {
		if seq, ok := f.canary.parse(filepath.Clean(event.Path)); ok {
			f.watchdog.canarySeen(seq)
		}
	}
}

// coalesceFSEvents always accepts from events, merging what arrives while the
// reader of the returned channel is busy.
func coalesceFSEvents(ctx context.Context, events <-chan []fsevents.Event) <-chan []fsevents.Event {
	out := make(chan []fsevents.Event)
	go func() {
		var pending []fsevents.Event
		for {
			var send chan<- []fsevents.Event
			if len(pending) > 0 {
				send = out
			}
			select {
			case <-ctx.Done():
				return
			case batch := <-events:
				pending = appendPendingFSEvents(pending, batch)
			case send <- pending:
				pending = nil
			}
		}
	}()
	return out
}

// appendPendingFSEvents collapses a backlog past maxPendingFSEvents into one
// rescan event, which makes the model re-read and so loses nothing.
func appendPendingFSEvents(pending, batch []fsevents.Event) []fsevents.Event {
	if len(pending)+len(batch) > maxPendingFSEvents {
		return []fsevents.Event{{Flags: fsevents.MustScanSubDirs}}
	}
	return append(pending, batch...)
}

// stopFSEventStream stops stream. Its callback blocks on Events, so they are
// drained until Stop has torn the dispatch queue down.
func stopFSEventStream(stream *fsevents.EventStream) {
	stopped := make(chan struct{})
	go func() {
		for {
			select {
			case <-stream.Events:
			case <-stopped:
				return
			}
		}
	}()
	stream.Stop()
	close(stopped)
}

// relevant reports whether a delivered batch changes the workspace model.
func (f *fseventsFilter) relevant(ctx context.Context, batch []fsevents.Event) bool {
	var candidates []string
	seen := map[string]struct{}{}
	for _, event := range batch {
		if event.Flags&fseventsRescan != 0 {
			return true
		}
		if event.Flags&fseventsContentChange == 0 {
			continue
		}
		name := filepath.Clean(event.Path)
		if f.canary != nil && f.canary.owns(name) {
			continue // lives under the root only if the root contains the data dir
		}
		if _, ok := f.metadataFiles[name]; ok {
			return true
		}
		if !pathx.Within(f.realRoot, name) || name == f.realRoot || hasGitMetadataComponent(f.realRoot, name) {
			continue
		}
		if !f.git.available {
			return true
		}
		if filepath.Base(name) == ".gitignore" {
			// Ignore rules changed: what was ignored may not be any more.
			clear(f.ignoredDirs)
		}
		if f.underIgnoredDir(name) {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, name)
	}
	if len(candidates) == 0 {
		return false
	}
	ignored := f.checkIgnored(ctx, candidates)
	relevant := false
	for _, name := range candidates {
		if _, ok := ignored[name]; !ok {
			relevant = true
			continue
		}
		if info, err := os.Stat(name); err == nil && info.IsDir() && len(f.ignoredDirs) < maxIgnoreCache {
			f.ignoredDirs[name] = struct{}{}
		}
	}
	return relevant
}

func (f *fseventsFilter) underIgnoredDir(name string) bool {
	for dir := filepath.Dir(name); pathx.Within(f.realRoot, dir) && dir != f.realRoot; dir = filepath.Dir(dir) {
		if _, ok := f.ignoredDirs[dir]; ok {
			return true
		}
	}
	return false
}

// checkIgnored asks git once for the whole batch which paths it ignores. On
// error every path counts as not ignored, so a change is never lost.
func (f *fseventsFilter) checkIgnored(ctx context.Context, names []string) map[string]struct{} {
	var stdin bytes.Buffer
	for _, name := range names {
		rel, err := filepath.Rel(f.realRoot, name)
		if err != nil {
			continue
		}
		stdin.WriteString(filepath.ToSlash(rel))
		stdin.WriteByte(0)
	}
	cmd := tallpineprocess.CommandContext(ctx, "git", ports.GitInDir(f.root, "check-ignore", "-z", "--stdin")...)
	cmd.Stdin = &stdin
	out, _ := cmd.Output() // exit 1 means nothing is ignored
	ignored := map[string]struct{}{}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		rel := filepath.FromSlash(strings.TrimSuffix(string(raw), "/"))
		ignored[filepath.Join(f.realRoot, rel)] = struct{}{}
	}
	return ignored
}
