package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// scratchGrace keeps a leftover that may belong to a session being started or
// restored right now: a temp dir with no session row yet, or one whose session
// ended moments ago.
const scratchGrace = time.Hour

// SessionTempRule removes session temp directories (<data dir>/tmp/<id>) that
// outlived their session, and empties the trash of discarded ones. A live
// session's directory is never touched, and the session is re-read right
// before each removal.
type SessionTempRule struct{ dataDir string }

// NewSessionTempRule builds the rule.
func NewSessionTempRule(dataDir string) *SessionTempRule { return &SessionTempRule{dataDir: dataDir} }

// Category implements Rule.
func (*SessionTempRule) Category() Category { return CategorySessionTemp }

// Light implements Rule.
func (*SessionTempRule) Light() bool { return true }

// Enabled implements Rule.
func (*SessionTempRule) Enabled(p Policy) bool { return p.SessionTemp.Enabled }

// Run implements Rule.
func (r *SessionTempRule) Run(ctx context.Context, run *Run) error {
	if r.dataDir == "" {
		return errors.New("session temp cleanup is not configured")
	}
	root := filepath.Join(r.dataDir, sessionscratch.TempRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	byID := make(map[domain.SessionID]domain.SessionRecord, len(run.Sessions))
	for _, rec := range run.Sessions {
		byID[rec.ID] = rec
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		path := filepath.Join(root, name)
		if name == sessionscratch.TrashDir {
			emptyTrash(ctx, run, "discarded session temp directories", path, func(ctx context.Context) error {
				return sessionscratch.EmptyTrash(ctx, r.dataDir)
			})
			continue
		}
		// Only real directories named like a session; anything else is not ours
		// to judge.
		if strings.HasPrefix(name, ".") || !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		id := domain.SessionID(name)
		rec, known := byID[id]
		if known && !rec.IsTerminated {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		item := Item{SessionID: id, ProjectID: rec.ProjectID, What: "session temp directory", Path: path}
		if (known && run.Now.Sub(rec.UpdatedAt) < scratchGrace) || (!known && run.Now.Sub(info.ModTime()) < scratchGrace) {
			continue
		}
		if item.Bytes, err = dirSize(ctx, path); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if run.DryRun {
			item.Status = StatusWouldFree
			run.Add(item)
			continue
		}
		if _, _, live := run.Current(ctx, id); live {
			continue
		}
		moved, err := sessionscratch.Discard(r.dataDir, id, run.Now)
		if err == nil && moved != "" {
			err = sessionscratch.RemoveTree(moved)
		}
		if err != nil {
			item.Status, item.Reason = StatusFailed, err.Error()
		} else {
			item.Status = StatusFreed
		}
		run.Add(item)
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}

// emptyTrash reports the trash directory's size and, in a real run, empties
// it.
func emptyTrash(ctx context.Context, run *Run, what, trash string, empty func(context.Context) error) {
	size, err := dirSize(ctx, trash)
	if err != nil || size == 0 {
		return
	}
	item := Item{What: what, Path: trash, Bytes: size}
	if run.DryRun {
		item.Status = StatusWouldFree
		run.Add(item)
		return
	}
	if err := empty(ctx); err != nil {
		left, _ := dirSize(ctx, trash)
		item.Bytes = max(size-left, 0)
		item.Status, item.Reason = StatusFailed, err.Error()
		if item.Bytes > 0 {
			item.Status, item.Reason = StatusFreed, "partly removed: "+err.Error()
		}
		run.Add(item)
		return
	}
	item.Status = StatusFreed
	run.Add(item)
}

// HarnessCacheRule removes a project's harness caches (the per-project Gradle
// home under <data dir>/caches) once the project has had no live session for
// the retention window. A project with a live session keeps its cache; the
// sessions are re-read right before each removal. A cache is first renamed
// into the cache trash, so a session that starts while it is being removed
// gets a fresh home; what a removal could not finish is emptied on the next
// pass and at daemon start.
type HarnessCacheRule struct {
	dataDir string
	// afterDiscard, when set, runs between the rename and the removal (tests).
	afterDiscard func()
}

// NewHarnessCacheRule builds the rule.
func NewHarnessCacheRule(dataDir string) *HarnessCacheRule {
	return &HarnessCacheRule{dataDir: dataDir}
}

// Category implements Rule.
func (*HarnessCacheRule) Category() Category { return CategoryHarnessCaches }

// Light implements Rule: caches are measured and removed in the full pass.
func (*HarnessCacheRule) Light() bool { return false }

// Enabled implements Rule.
func (*HarnessCacheRule) Enabled(p Policy) bool { return p.HarnessCaches.Enabled }

// Run implements Rule.
func (r *HarnessCacheRule) Run(ctx context.Context, run *Run) error {
	if r.dataDir == "" {
		return errors.New("harness cache cleanup is not configured")
	}
	emptyTrash(ctx, run, "discarded gradle caches", filepath.Join(r.dataDir, sessionscratch.CacheRoot, sessionscratch.CacheTrashDir), func(ctx context.Context) error {
		return sessionscratch.EmptyCacheTrash(ctx, r.dataDir)
	})
	root := filepath.Join(r.dataDir, sessionscratch.CacheRoot, sessionscratch.GradleDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	retention := time.Duration(run.Policy.HarnessCaches.RetentionDays) * 24 * time.Hour
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		project := domain.ProjectID(name)
		if name == sessionscratch.StandaloneGradleHome {
			project = ""
		}
		inProject := func(rec domain.SessionRecord) bool { return rec.ProjectID == project }
		path := filepath.Join(root, name)
		item := Item{ProjectID: project, What: "gradle cache", Path: path, Status: StatusKept}
		var lastUsed time.Time
		live := false
		for _, rec := range run.Sessions {
			if !inProject(rec) {
				continue
			}
			live = live || !rec.IsTerminated
			if rec.UpdatedAt.After(lastUsed) {
				lastUsed = rec.UpdatedAt
			}
		}
		if info, err := entry.Info(); err == nil && lastUsed.IsZero() {
			lastUsed = info.ModTime()
		}
		switch {
		case live:
			item.Reason = "in use by a live session"
			run.Add(item)
			continue
		case run.Now.Sub(lastUsed) < retention:
			item.Reason = fmt.Sprintf("used within the last %d day(s)", run.Policy.HarnessCaches.RetentionDays)
			run.Add(item)
			continue
		}
		if item.Bytes, err = dirSize(ctx, path); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if run.DryRun {
			item.Status = StatusWouldFree
			run.Add(item)
			continue
		}
		if run.AnyLive(ctx, inProject) {
			item.Bytes, item.Reason = 0, "in use by a live session"
			run.Add(item)
			continue
		}
		// Renamed away right after the re-check: a session spawned from here
		// on creates a fresh home instead of building against this one.
		moved, err := sessionscratch.DiscardGradleHome(r.dataDir, project, run.Now)
		if err == nil && moved != "" {
			if r.afterDiscard != nil {
				r.afterDiscard()
			}
			err = sessionscratch.RemoveTree(moved)
		}
		if err != nil {
			item.Status, item.Reason = StatusFailed, err.Error()
		} else {
			item.Status = StatusFreed
		}
		run.Add(item)
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}

// SystemTempRule removes stale leftovers in the system temp directory whose
// names match the policy's patterns. Tallpine cannot attribute these to a session
// (sessions now get their own temp dir, but tools that ignore TMPDIR and runs
// from before still leave things here), so it is off by default and, while
// off, only reports what it would remove. It only looks at top-level entries
// owned by the daemon's user, readable and owned by it throughout, not
// modified anywhere inside for MinAgeDays, and that no process has open or as
// its working directory. It never follows symlinks and never touches the data
// directory.
type SystemTempRule struct {
	dir     string
	dataDir string
	inUse   func(ctx context.Context, path string) (bool, error)
}

// NewSystemTempRule builds the rule over dir, the daemon's own temp directory
// (os.TempDir at startup).
func NewSystemTempRule(dir, dataDir string) *SystemTempRule {
	return &SystemTempRule{dir: dir, dataDir: dataDir, inUse: pathInUse}
}

// Category implements Rule.
func (*SystemTempRule) Category() Category { return CategorySystemTemp }

// Light implements Rule.
func (*SystemTempRule) Light() bool { return false }

// Enabled implements Rule: the rule always runs, and only removes anything
// once the policy turns it on.
func (*SystemTempRule) Enabled(Policy) bool { return true }

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, err := filepath.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

// newestAndSize walks path and reports the newest modification time and the
// total size of its regular files. Anything it cannot read, and anything owned
// by another user, is an error: the age and the removal would otherwise be
// judged on part of the tree.
func newestAndSize(ctx context.Context, path string) (time.Time, int64, error) {
	var newest time.Time
	var size int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !ownedByCurrentUser(info) {
			return fmt.Errorf("%s is owned by another user", p)
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if d.Type().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return newest, size, err
}

// Run implements Rule.
func (r *SystemTempRule) Run(ctx context.Context, run *Run) error {
	policy := run.Policy.SystemTemp
	dir := filepath.Clean(r.dir)
	if r.dir == "" || !filepath.IsAbs(dir) || dir == filepath.Dir(dir) || len(policy.Patterns) == 0 {
		return nil
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(home, dir) {
		return errors.New("refusing to treat the home directory as the system temp directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	minAge := time.Duration(policy.MinAgeDays) * 24 * time.Hour
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if !matchesAny(policy.Patterns, name) || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, name)
		if r.dataDir != "" && (pathx.Within(path, r.dataDir) || pathx.Within(r.dataDir, path)) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !ownedByCurrentUser(info) {
			continue
		}
		newest, size, err := newestAndSize(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			run.Add(Item{What: "system temp leftover", Path: path, Status: StatusKept, Reason: "could not be read completely: " + err.Error()})
			continue
		}
		if run.Now.Sub(newest) < minAge {
			continue
		}
		item := Item{What: "system temp leftover", Path: path, Bytes: size}
		if policy.Enabled {
			if busy, err := r.inUse(ctx, path); err != nil || busy {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				item.Bytes, item.Status, item.Reason = 0, StatusKept, "in use by a process"
				if err != nil {
					item.Reason = "could not tell whether a process uses it: " + err.Error()
				}
				run.Add(item)
				continue
			}
		}
		switch {
		case !policy.Enabled:
			item.Status = StatusNeedsConfirmation
			item.Reason = "system temp cleanup is off; turn on systemTemp to remove these"
		case run.DryRun:
			item.Status = StatusWouldFree
		default:
			if err := sessionscratch.RemoveTree(path); err != nil {
				item.Status, item.Reason = StatusFailed, err.Error()
			} else {
				item.Status = StatusFreed
			}
		}
		run.Add(item)
		if !run.DryRun && policy.Enabled && !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}
