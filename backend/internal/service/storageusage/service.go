package storageusage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// Budgets bound the cost of a measurement.
const (
	// EntryBudget is the longest one location is walked.
	EntryBudget = 20 * time.Second
	// RefreshBudget is the longest a whole measurement takes.
	RefreshBudget = 2 * time.Minute
	// StaleAfter is how old a snapshot gets before a read starts a refresh.
	StaleAfter = 2 * time.Minute
	workers    = 3
)

// GoCacheDefaultCapGB is scripts/go's default cap on the shared Go build cache.
const GoCacheDefaultCapGB = 30

// ErrNotFound reports an entry id the disk no longer has.
var ErrNotFound = errors.New("storage entry not found")

// ErrProtected reports an entry that may not be removed; the message says why.
type ErrProtected struct{ Reason string }

func (e ErrProtected) Error() string { return "cannot remove: " + e.Reason }

// Sessions lists the sessions the daemon knows.
type Sessions interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Cleaner is the cleanup engine.
type Cleaner interface {
	Run(ctx context.Context, req cleanup.RunRequest) (cleanup.Report, error)
}

// WorktreeRemover removes one ended session's worktrees the way
// `tallpine session cleanup` does: under the session guard, salvaging dirty
// work first. kept is non-empty when the worktree was left in place.
type WorktreeRemover func(ctx context.Context, id domain.SessionID) (kept string, err error)

// Config wires the service.
type Config struct {
	DataDir string
	// StateRoot is the directory holding running.json, releases/, exports/.
	StateRoot string
	// GoCacheDir is the shared Go build cache ("" = the default location).
	GoCacheDir string
	Sessions   Sessions
	Cleaner    Cleaner
	Worktrees  WorktreeRemover
	// Processes lists the process table; nil uses sessionreap.Snapshot.
	Processes func() ([]sessionreap.Proc, error)
	Now       func() time.Time
}

// Service measures and cleans.
type Service struct {
	cfg Config

	mu         sync.Mutex
	snap       Snapshot
	have       bool
	refreshing bool
	done       chan struct{}
}

// New builds the service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Processes == nil {
		cfg.Processes = sessionreap.Snapshot
	}
	if cfg.GoCacheDir == "" {
		cfg.GoCacheDir = defaultGoCacheDir()
	}
	return &Service{cfg: cfg}
}

func defaultGoCacheDir() string {
	if v := os.Getenv("GOCACHE"); v != "" && v != "off" {
		return v
	}
	if c, err := os.UserCacheDir(); err == nil {
		return filepath.Join(c, "go-build")
	}
	return ""
}

func (s *Service) roots() roots {
	return roots{data: s.cfg.DataDir, state: s.cfg.StateRoot, goDir: s.cfg.GoCacheDir, now: s.cfg.Now}
}

// Get returns the cached snapshot at once and starts a background refresh
// when it is missing, stale, or force is set. The first call has no entries
// yet (Refreshing is true); the page polls.
func (s *Service) Get(force bool) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if force || !s.have || s.cfg.Now().Sub(s.snap.ComputedAt) > StaleAfter {
		s.startRefreshLocked()
	}
	out := s.snap
	out.Refreshing = s.refreshing
	if out.Entries == nil {
		out.Entries = []Entry{}
	}
	if out.Processes == nil {
		out.Processes = []Process{}
	}
	return out
}

func (s *Service) startRefreshLocked() {
	if s.refreshing {
		return
	}
	s.refreshing = true
	s.done = make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), RefreshBudget)
		defer cancel()
		snap := s.measure(ctx)
		s.mu.Lock()
		s.snap, s.have, s.refreshing = snap, true, false
		close(s.done)
		s.mu.Unlock()
	}()
}

// Refresh measures now and waits for the result (tests, and after an action).
func (s *Service) Refresh(ctx context.Context) Snapshot {
	s.mu.Lock()
	s.startRefreshLocked()
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return s.Get(false)
}

func (s *Service) index(ctx context.Context) (sessionIndex, error) {
	recs, err := s.cfg.Sessions.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	ix := make(sessionIndex, len(recs))
	for _, r := range recs {
		ix[r.ID] = r
	}
	return ix, nil
}

func (s *Service) measure(ctx context.Context) Snapshot {
	now := s.cfg.Now().UTC()
	snap := Snapshot{ComputedAt: now}
	ix, err := s.index(ctx)
	if err != nil {
		return snap
	}
	r := s.roots()
	entries := scan(r, ix)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s.size(ctx, &entries[i])
			}
		}()
	}
	for i := range entries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Category != entries[j].Category {
			return categoryRank(entries[i].Category) < categoryRank(entries[j].Category)
		}
		return entries[i].Bytes > entries[j].Bytes
	})
	for _, e := range entries {
		snap.TotalBytes += e.Bytes
		if e.Category == CategoryGoBuildCache {
			snap.GoCache = &GoCache{Path: e.Path, Bytes: e.Bytes, Partial: e.Partial, CapBytes: goCacheCap()}
		}
	}
	snap.Entries = entries
	snap.Processes = s.processes(ix)
	return snap
}

func categoryRank(c Category) int {
	for i, v := range []Category{
		CategoryWorktrees, CategorySessionTemp, CategoryArtifacts, CategoryAttachments, CategorySalvage,
		CategoryCaches, CategoryGoBuildCache, CategoryReleases, CategoryReleaseBuild, CategoryDatabase,
		CategoryLogs, CategoryExports, CategoryBackups, CategoryBuildCache, CategoryLocalCI,
	} {
		if v == c {
			return i
		}
	}
	return 99
}

func goCacheCap() int64 {
	gb := int64(GoCacheDefaultCapGB)
	if v := os.Getenv("TALLPINE_GOCACHE_MAX_GB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			gb = n
		}
	}
	return gb << 30
}

func (s *Service) size(ctx context.Context, e *Entry) {
	ectx, cancel := context.WithTimeout(ctx, EntryBudget)
	defer cancel()
	paths := []string{e.Path}
	if e.Category == CategoryDatabase {
		paths = append(paths, e.Path+"-wal", e.Path+"-shm")
	}
	for _, p := range paths {
		m := measureTree(ectx, p)
		e.Bytes += m.Bytes
		e.Files += m.Files
		e.Partial = e.Partial || m.Partial
		if m.Newest.After(e.Newest) {
			e.Newest = m.Newest
		}
	}
}

// processes lists what the reaper would stop: processes of sessions this store
// knows as terminated, selected with the reaper's own rules. A session id the
// store does not know (another install's live session, say) is never listed:
// the reaper leaves those alone.
func (s *Service) processes(ix sessionIndex) []Process {
	procs, err := s.cfg.Processes()
	if err != nil {
		return nil
	}
	byPID := make(map[int]sessionreap.Proc, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var out []Process
	for sid, rec := range ix {
		if !rec.IsTerminated {
			continue
		}
		id := string(sid)
		dirs := []string{sessionscratch.TempDir(s.cfg.DataDir, sid)}
		for _, pid := range sessionreap.Select(procs, sessionreap.Match{SessionID: id, Dirs: dirs}, os.Getpid()) {
			p := byPID[pid]
			out = append(out, Process{PID: pid, Command: p.Exe, Session: sid, Project: ix[sid].ProjectID, State: ix.state(sid), Started: p.Start})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// find re-scans (cheap: no sizes) and returns the entry with id, so a client
// can only ever name something a scan produced.
func (s *Service) find(ctx context.Context, id string) (Entry, sessionIndex, error) {
	ix, err := s.index(ctx)
	if err != nil {
		return Entry{}, nil, err
	}
	for _, e := range scan(s.roots(), ix) {
		if e.ID == id {
			return e, ix, nil
		}
	}
	return Entry{}, nil, ErrNotFound
}

// Delete removes one entry.
func (s *Service) Delete(ctx context.Context, id string) error {
	e, ix, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	if e.Protected != "" && e.Category != CategoryGoBuildCache {
		return ErrProtected{Reason: e.Protected}
	}
	switch e.Category {
	case CategoryWorktrees:
		if e.State == StateOrphaned {
			return ErrProtected{Reason: "no session record; inspect it before removing"}
		}
		kept, err := s.cfg.Worktrees(ctx, e.Session)
		if err != nil {
			return err
		}
		if kept != "" {
			return ErrProtected{Reason: kept}
		}
	case CategorySessionTemp:
		err = s.removeScratch(ctx, e)
	case CategoryCaches:
		err = s.removeCache(ix, e)
	case CategoryGoBuildCache:
		_, err = s.cfg.Cleaner.Run(ctx, cleanup.RunRequest{Pass: cleanup.PassFull, Only: []cleanup.Category{cleanup.CategoryGoBuildCache}, StorageSafe: true})
	default:
		err = s.removeTree(e)
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.snap.ComputedAt = time.Time{} // the next read refreshes
	s.mu.Unlock()
	return nil
}

// removeCache discards a Gradle home by one rename into the cache trash, so a
// session starting in the project meanwhile gets a fresh home rather than one
// being removed underneath it, as the cleanup rule does. Other caches are
// plain directories.
func (s *Service) removeCache(ix sessionIndex, e Entry) error {
	if ix.liveInProject(e.Project) {
		return ErrProtected{Reason: "a session in this project is live"}
	}
	if filepath.Dir(e.Path) != filepath.Join(s.cfg.DataDir, sessionscratch.CacheRoot, sessionscratch.GradleDir) {
		return s.removeTree(e)
	}
	moved, err := sessionscratch.DiscardGradleHome(s.cfg.DataDir, e.Project, s.cfg.Now())
	if err == nil && moved != "" {
		err = sessionscratch.RemoveTree(moved)
	}
	return err
}

func (s *Service) removeScratch(ctx context.Context, e Entry) error {
	if e.Session == "" { // the trash
		return sessionscratch.EmptyTrash(ctx, s.cfg.DataDir)
	}
	// Re-read right before removing: a session restored meanwhile is live.
	if rec, ok, err := s.cfg.Sessions.GetSession(ctx, e.Session); err != nil || (ok && (!rec.IsTerminated || rec.Kind == domain.KindOrchestrator)) {
		return ErrProtected{Reason: "session is live"}
	}
	moved, err := sessionscratch.Discard(s.cfg.DataDir, e.Session, s.cfg.Now())
	if err == nil && moved != "" {
		err = sessionscratch.RemoveTree(moved)
	}
	return err
}

// removeTree removes a plain directory or file the scan listed, after
// re-checking it is still a real (non-symlink) entry below a known root.
func (s *Service) removeTree(e Entry) error {
	info, err := os.Lstat(e.Path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", e.Path)
	}
	r := s.roots()
	if !pathx.StrictlyWithin(r.state, e.Path) && !pathx.StrictlyWithin(r.data, e.Path) {
		return fmt.Errorf("%s is outside Tallpine's storage", e.Path)
	}
	return sessionscratch.RemoveTree(e.Path)
}

// CleanSafe runs the cleanup engine over everything it may remove (ended
// sessions' worktrees, scratch and caches, the Go build cache trimmed to what
// is unused), then drops rollback points and release builds beyond the newest
// one. dryRun reports without removing.
func (s *Service) CleanSafe(ctx context.Context, dryRun bool) (cleanup.Report, error) {
	rep, err := s.cfg.Cleaner.Run(ctx, cleanup.RunRequest{
		Pass: cleanup.PassFull, DryRun: dryRun, Trigger: cleanup.TriggerManual, StorageSafe: true,
		Only: []cleanup.Category{cleanup.CategoryWorktrees, cleanup.CategorySessionTemp, cleanup.CategoryHarnessCaches, cleanup.CategoryGoBuildCache},
	})
	if err != nil {
		return rep, err
	}
	cat := cleanup.CategoryReport{Category: "releases", Items: []cleanup.Item{}}
	ix, err := s.index(ctx)
	if err != nil {
		return rep, err
	}
	for _, e := range scan(s.roots(), ix) {
		if (e.Category != CategoryReleases && e.Category != CategoryReleaseBuild) || e.Protected != "" {
			continue
		}
		item := cleanup.Item{What: e.Label, Path: e.Path, Bytes: measureTree(ctx, e.Path).Bytes, Status: cleanup.StatusWouldFree}
		if !dryRun {
			if err := s.removeTree(e); err != nil {
				item.Status, item.Reason = cleanup.StatusFailed, err.Error()
			} else {
				item.Status = cleanup.StatusFreed
			}
		}
		cat.Items = append(cat.Items, item)
		switch item.Status {
		case cleanup.StatusFreed:
			cat.Freed++
			cat.FreedBytes += item.Bytes
			rep.FreedBytes += item.Bytes
		case cleanup.StatusWouldFree:
			cat.WouldFree++
			cat.WouldFreeBytes += item.Bytes
		default:
			cat.Failed++
		}
	}
	rep.Categories = append(rep.Categories, cat)
	s.mu.Lock()
	s.snap.ComputedAt = time.Time{}
	s.mu.Unlock()
	return rep, nil
}
