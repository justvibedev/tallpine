package workspacewatch

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

// maxFingerprintEntries bounds one fingerprint of a root with no git file list.
// Changes past it stay invisible to polling until the event stream catches up.
const maxFingerprintEntries = 200_000

// lagSettings tune the watchdog. FSEvents normally delivers within 100 ms, so
// the threshold only trips when the system-wide pipeline is really behind.
type lagSettings struct {
	tick         time.Duration
	touchEvery   time.Duration // between canaries while the stream keeps up
	threshold    time.Duration // an unanswered canary this old means lag
	pollEvery    time.Duration // between fingerprints while lagging
	retouchAfter time.Duration // replaces a canary the stream may have lost
}

var defaultLagSettings = lagSettings{
	tick:         time.Second,
	touchEvery:   2 * time.Second,
	threshold:    2 * time.Second,
	pollEvery:    2 * time.Second,
	retouchAfter: 30 * time.Second,
}

// lagWatchdog keeps views fresh while FSEvents is behind. FSEvents is one
// system-wide pipeline: under heavy disk activity anywhere on the Mac it can
// deliver more than 100 s late, even on a fresh stream, and a late stream is
// silent rather than failing. The watchdog writes a canary file on the stream
// it guards and times the round trip. While a canary is overdue it polls a
// cheap fingerprint of the root and reports changes itself; polling stops as
// soon as a fresh canary comes back on time, so a healthy stream costs one
// small write every touchEvery and no scans.
type lagWatchdog struct {
	root        string
	settings    lagSettings
	touch       func() (uint64, error)
	fingerprint func(context.Context) (uint64, error)
	notify      func()
	now         func() time.Time

	seen chan canaryReport

	// The fields below belong to run.
	outstanding bool
	seq         uint64
	sentAt      time.Time
	lagging     bool
	lagSince    time.Time
	lastPoll    time.Time
	baseline    uint64
}

// canaryReport is one canary delivery, stamped when the stream delivered it: the
// loop that reads reports also runs the fingerprint, so a report can wait there
// for as long as a scan takes.
type canaryReport struct {
	seq uint64
	at  time.Time
}

func newLagWatchdog(root string, settings lagSettings, touch func() (uint64, error), fingerprint func(context.Context) (uint64, error), notify func()) *lagWatchdog {
	return &lagWatchdog{root: root, settings: settings, touch: touch, fingerprint: fingerprint, notify: notify, now: time.Now, seen: make(chan canaryReport, 8)}
}

// canarySeen reports that the stream delivered canary seq. It never blocks the
// reader; a dropped report is recovered by the next canary.
func (w *lagWatchdog) canarySeen(seq uint64) {
	select {
	case w.seen <- canaryReport{seq: seq, at: w.now()}:
	default:
	}
}

func (w *lagWatchdog) run(ctx context.Context) {
	ticker := time.NewTicker(w.settings.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case report := <-w.seen:
			w.onCanary(report)
		case <-ticker.C:
			w.onTick(ctx)
		}
	}
}

func (w *lagWatchdog) onCanary(report canaryReport) {
	if !w.outstanding || report.seq != w.seq {
		return // a canary already replaced; its timing says nothing
	}
	w.outstanding = false
	if w.lagging && report.at.Sub(w.sentAt) < w.settings.threshold {
		w.lagging = false
		slog.Info("workspace watch: FSEvents caught up, polling stopped", "root", w.root, "lagged_for", w.now().Sub(w.lagSince).Round(time.Second))
	}
}

func (w *lagWatchdog) onTick(ctx context.Context) {
	now := w.now()
	age := now.Sub(w.sentAt)
	if !w.outstanding {
		if age >= w.settings.touchEvery {
			w.sendCanary(now)
		}
	} else if age >= w.settings.retouchAfter {
		w.sendCanary(now)
	} else if !w.lagging && age >= w.settings.threshold {
		w.enterLag(ctx, now)
		return
	}
	if w.lagging && now.Sub(w.lastPoll) >= w.settings.pollEvery {
		w.poll(ctx, now)
	}
}

func (w *lagWatchdog) sendCanary(now time.Time) {
	seq, err := w.touch()
	if err != nil {
		slog.Debug("workspace watch: canary write failed", "root", w.root, "err", err)
		return
	}
	w.seq, w.sentAt, w.outstanding = seq, now, true
}

// enterLag starts polling. A change may already be sitting in the delayed
// stream and no earlier fingerprint exists to reveal it, so the first report is
// unconditional. The baseline is taken before reporting, so a write landing
// between the two shows up at the next poll instead of being absorbed.
func (w *lagWatchdog) enterLag(ctx context.Context, now time.Time) {
	w.lagging, w.lagSince, w.lastPoll = true, now, now
	slog.Warn("workspace watch: FSEvents is behind, polling until it catches up", "root", w.root, "threshold", w.settings.threshold)
	if fingerprint, err := w.fingerprint(ctx); err == nil {
		w.baseline = fingerprint
	}
	w.notify()
}

func (w *lagWatchdog) poll(ctx context.Context, now time.Time) {
	w.lastPoll = now
	fingerprint, err := w.fingerprint(ctx)
	if err != nil || fingerprint == w.baseline {
		return
	}
	w.baseline = fingerprint
	w.notify()
}

// workspaceFingerprint summarises the names, sizes and modification times the
// workspace model depends on. Reading them generates no filesystem events.
func workspaceFingerprint(ctx context.Context, root string, git gitWorkspace) (uint64, error) {
	h := fnv.New64a()
	if git.available {
		files, err := listGitFiles(ctx, root)
		if err != nil {
			return 0, err
		}
		for _, rel := range files {
			hashLstat(h, filepath.Join(root, rel), rel)
		}
		metadata := make([]string, 0, len(git.metadataFiles))
		for name := range git.metadataFiles {
			metadata = append(metadata, name)
		}
		sort.Strings(metadata)
		for _, name := range metadata {
			hashLstat(h, name, name)
		}
		return h.Sum64(), ctx.Err()
	}
	entries := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil // removed mid-walk: the next poll sees the settled tree
			}
			return walkErr
		}
		if path != root && entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		entries++
		if entries > maxFingerprintEntries {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		hashLstat(h, path, rel)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

func hashLstat(h hash.Hash64, path, label string) {
	var stamp [16]byte
	if info, err := os.Lstat(path); err == nil {
		binary.LittleEndian.PutUint64(stamp[:8], uint64(info.Size()))               //nolint:gosec // a hash input
		binary.LittleEndian.PutUint64(stamp[8:], uint64(info.ModTime().UnixNano())) //nolint:gosec // a hash input
	}
	_, _ = h.Write([]byte(label))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(stamp[:])
}

// canaryBaseDir holds canary directories, under the data dir like all other
// app state. A variable so tests can use a scratch directory.
var canaryBaseDir = func() string {
	dataDir, ok := config.DataDirFromEnv()
	if !ok {
		dataDir = config.DefaultDataDir()
	}
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "workspacewatch-canaries")
}

// staleCanaryAge marks a directory left by a killed daemon. A live watcher
// rewrites its canary, and so its directory's mtime, every few seconds.
const staleCanaryAge = 10 * time.Minute

const canaryFilePrefix = "c-"

// fseventsCanary is the probe file behind a lagWatchdog. Only its goroutine
// calls touch; dir is immutable, so owns and parse are safe from the reader.
type fseventsCanary struct {
	dir string
	seq uint64
}

func newFSEventsCanary() (*fseventsCanary, error) {
	base := canaryBaseDir()
	if base == "" {
		return nil, errors.New("no data directory for the FSEvents canary")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	sweepStaleCanaries(base)
	dir, err := os.MkdirTemp(base, "root-")
	if err != nil {
		return nil, err
	}
	// FSEvents reports resolved paths.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &fseventsCanary{dir: realDir}, nil
}

func sweepStaleCanaries(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil || time.Since(info.ModTime()) < staleCanaryAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(base, entry.Name()))
	}
}

// touch replaces the canary with a new name, so a late delivery of an old one
// cannot be mistaken for the current round trip.
func (c *fseventsCanary) touch() (uint64, error) {
	next := c.seq + 1
	if err := os.WriteFile(c.path(next), nil, 0o600); err != nil {
		return 0, fmt.Errorf("write FSEvents canary: %w", err)
	}
	if c.seq != 0 {
		_ = os.Remove(c.path(c.seq))
	}
	c.seq = next
	return next, nil
}

func (c *fseventsCanary) path(seq uint64) string {
	return filepath.Join(c.dir, canaryFilePrefix+strconv.FormatUint(seq, 10))
}

func (c *fseventsCanary) owns(name string) bool {
	return pathx.Within(c.dir, name)
}

// parse returns the sequence number of the canary file at name, if it is one.
func (c *fseventsCanary) parse(name string) (uint64, bool) {
	if filepath.Dir(name) != c.dir {
		return 0, false
	}
	digits, ok := strings.CutPrefix(filepath.Base(name), canaryFilePrefix)
	if !ok {
		return 0, false
	}
	seq, err := strconv.ParseUint(digits, 10, 64)
	return seq, err == nil
}

func (c *fseventsCanary) close() {
	_ = os.RemoveAll(c.dir)
}
