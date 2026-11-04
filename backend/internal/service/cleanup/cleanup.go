// Package cleanup is the daemon's automatic cleanup engine: it removes what
// agent sessions leave behind once the session is over, so the user never has
// to clean up after agents by hand.
//
// Each Rule owns one category (worktrees, artifacts, ...). Every rule follows
// the same safety contract:
//
//   - It only acts on what belongs to a terminated session, re-checked right
//     before each deletion, and never on a live session's files.
//   - It only touches paths and labels Tallpine owns (the data directory, a
//     session's registered worktree), never anything outside them.
//   - On any doubt (a failed probe, an unreadable status) it keeps the item.
//   - A dry run walks the exact same decisions and stops before every
//     mutation, so its report is what a real run would do.
//
// Runs are serialized, paced between items and recorded in a bounded history.
// The scheduler runs a light pass hourly and a full pass daily while the
// policy's Automatic switch is on.
package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

const (
	// DefaultLightEvery paces light passes.
	DefaultLightEvery = time.Hour
	// DefaultFullEvery paces full passes.
	DefaultFullEvery = 24 * time.Hour
	// DefaultStartDelay keeps the first pass away from daemon startup.
	DefaultStartDelay = 10 * time.Minute
	// DefaultPause separates items so cleanup never competes with live work
	// for disk and git for long.
	DefaultPause = 50 * time.Millisecond
	// schedulerTick is how often the scheduler checks whether a pass is due.
	schedulerTick = time.Minute
	// keepRealRuns and keepDryRuns bound the history.
	keepRealRuns int64 = 200
	keepDryRuns  int64 = 20
)

// ErrBusy reports that another run is in progress.
var ErrBusy = errors.New("a cleanup run is already in progress")

// Store is the durable surface the engine needs.
type Store interface {
	GetCleanupPolicy(ctx context.Context) (string, time.Time, error)
	SetCleanupPolicy(ctx context.Context, policy string, now time.Time) error
	InsertCleanupRun(ctx context.Context, run sqlitestore.CleanupRun) (int64, error)
	ListCleanupRuns(ctx context.Context, limit int64) ([]sqlitestore.CleanupRun, error)
	LatestCleanupRun(ctx context.Context, dryRun bool) (sqlitestore.CleanupRun, bool, error)
	TrimCleanupRuns(ctx context.Context, dryRun bool, keep int64) error
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Rule cleans one category.
type Rule interface {
	Category() Category
	// Light reports whether the light pass runs this rule.
	Light() bool
	// Enabled reports whether the policy turns this rule on.
	Enabled(p Policy) bool
	// Run looks at every candidate and records an Item for each through run.
	// It must check run.DryRun before every mutation.
	Run(ctx context.Context, run *Run) error
}

// Run is one rule's view of a run in progress.
type Run struct {
	DryRun bool
	Pass   Pass
	// ConfirmArtifacts is the user's confirmation that the artifacts of
	// sessions past retention (version history, tool directories, and
	// deliverables per the policy's action) may go.
	ConfirmArtifacts bool
	Now              time.Time
	Policy           Policy
	// Sessions is a snapshot taken at the start of the run. Rules use it to
	// find candidates; anything that deletes re-checks the current state.
	Sessions []domain.SessionRecord
	// Only is the request's category filter (empty: every category).
	Only []Category
	// earlier are the categories this run already finished.
	earlier []CategoryReport
	report  *CategoryReport
	pause   time.Duration
	store   Store
}

// Current re-reads one session right before a deletion, so a session
// restored since the snapshot is seen as live. A read error counts as live.
func (r *Run) Current(ctx context.Context, id domain.SessionID) (rec domain.SessionRecord, found, live bool) {
	rec, found, err := r.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, true, true
	}
	return rec, found, found && !rec.IsTerminated
}

// AnyLive re-reads the sessions and reports whether one matching match is
// live. A read error counts as live.
func (r *Run) AnyLive(ctx context.Context, match func(domain.SessionRecord) bool) bool {
	recs, err := r.store.ListAllSessions(ctx)
	if err != nil {
		return true
	}
	for _, rec := range recs {
		if !rec.IsTerminated && match(rec) {
			return true
		}
	}
	return false
}

// Claimed reports whether a category earlier in this run already freed path
// or would free it, so a later rule neither repeats nor double counts it.
func (r *Run) Claimed(path string) bool {
	for _, cat := range r.earlier {
		for _, item := range cat.Items {
			if item.Path != "" && (item.Status == StatusFreed || item.Status == StatusWouldFree) && samePath(item.Path, path) {
				return true
			}
		}
	}
	return false
}

// Add records an item.
func (r *Run) Add(item Item) { r.report.add(item) }

// Pace waits between items. It returns false once ctx is done.
func (r *Run) Pace(ctx context.Context) bool { return sleep(ctx, r.pause) }

// LiveWorkspace reports whether a live session in the snapshot uses path.
func (r *Run) LiveWorkspace(path string) bool {
	for _, rec := range r.Sessions {
		if !rec.IsTerminated && rec.Metadata.WorkspacePath != "" && samePath(rec.Metadata.WorkspacePath, path) {
			return true
		}
	}
	return false
}

// RunRequest asks for one run.
type RunRequest struct {
	Pass             Pass
	DryRun           bool
	Trigger          Trigger
	ConfirmArtifacts bool
	// Only, when set, limits the run to these categories; the others report
	// as skipped.
	Only []Category
	// StorageSafe runs the rules that reclaim disk space (worktrees, session
	// temp, harness caches, the Go build cache) even where the stored policy
	// has them off. The user asked for it by hand on Settings > Storage; every
	// rule keeps its own safety checks and the policy's retention windows.
	StorageSafe bool
}

// Status is the engine's state for Settings and `tallpine cleanup status`.
type Status struct {
	Policy        Policy
	PolicyUpdated time.Time
	// PolicyError is set when the stored policy could not be read or was
	// invalid: Policy is then FailSafePolicy until the user saves a policy.
	PolicyError string
	// FirstPassPending is true until the first scheduled pass has run. That
	// pass is a dry run: it only reports what later passes will remove.
	FirstPassPending bool
	Running          bool
	LastRun          *Report
	LastDryRun       *Report
	LastFullRunAt    time.Time
	NextLightAt      time.Time
	NextFullAt       time.Time
}

// Config customizes the engine. Zero values use the production defaults.
type Config struct {
	LightEvery time.Duration
	FullEvery  time.Duration
	StartDelay time.Duration
	Pause      time.Duration
	Clock      func() time.Time
	Logger     *slog.Logger
}

// Service runs cleanup rules.
type Service struct {
	store Store
	rules []Rule
	cfg   Config
	// managed holds the policy fields the managed file sets and locks (nil: none).
	managed *managedcfg.Locks
	// run serializes runs; a manual run never waits behind a scheduled one.
	run sync.Mutex
	mu  sync.Mutex
	// running mirrors run for Status.
	running   bool
	lastLight time.Time
	lastFull  time.Time
	// scheduleLoaded is set once loadSchedule read the history; scheduled
	// records whether any scheduled pass (dry or real) has run.
	scheduleLoaded bool
	scheduled      bool
}

// New builds a Service over rules, which run in the given order.
func New(store Store, rules []Rule, cfg Config) *Service {
	if cfg.LightEvery <= 0 {
		cfg.LightEvery = DefaultLightEvery
	}
	if cfg.FullEvery <= 0 {
		cfg.FullEvery = DefaultFullEvery
	}
	if cfg.StartDelay < 0 {
		cfg.StartDelay = 0
	} else if cfg.StartDelay == 0 {
		cfg.StartDelay = DefaultStartDelay
	}
	if cfg.Pause < 0 {
		cfg.Pause = 0
	} else if cfg.Pause == 0 {
		cfg.Pause = DefaultPause
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{store: store, rules: rules, cfg: cfg}
}

// Policy reads the current policy. An unreadable row reads as the defaults
// with Automatic off, so a storage failure never deletes anything on its own.
func (s *Service) Policy(ctx context.Context) (Policy, time.Time, error) {
	policy, updated, _, err := s.policy(ctx)
	return policy, updated, err
}

// policy also reports a stored document that could not be decoded, which
// reads as FailSafePolicy.
func (s *Service) policy(ctx context.Context) (Policy, time.Time, error, error) {
	policy, updated, decodeErr, err := s.storedPolicy(ctx)
	return s.applyManaged(policy), updated, decodeErr, err
}

// storedPolicy is the user's policy as saved, without the managed overlay.
func (s *Service) storedPolicy(ctx context.Context) (Policy, time.Time, error, error) {
	doc, updated, err := s.store.GetCleanupPolicy(ctx)
	if err != nil {
		return FailSafePolicy(), time.Time{}, nil, err
	}
	policy, decodeErr := decodePolicy(doc)
	if decodeErr != nil {
		s.cfg.Logger.Warn("cleanup: stored policy is invalid; automatic cleanup is off until it is saved again", "err", decodeErr)
	}
	return policy, updated, decodeErr, nil
}

// SetManaged sets the managed locks; call before the service is shared.
func (s *Service) SetManaged(l *managedcfg.Locks) { s.managed = l }

// managedPrefix is the registry key prefix of the policy's top-level fields.
const managedPrefix = "app.cleanup."

// overlay is the shared managed-config overlay for the policy fields. A locked
// field is decoded over the default policy (the base the managed value was
// validated against) and the merged result must validate.
func (s *Service) overlay() managedcfg.Overlay[Policy] {
	return managedcfg.Overlay[Policy]{
		Locks: s.managed, Prefix: managedPrefix, Base: DefaultPolicy,
		Validate: func(p Policy) error { return p.Validate() },
	}
}

func (s *Service) applyManaged(p Policy) Policy { return s.overlay().Apply(p) }

// keepStoredLocked returns next with each locked field taken from stored.
func (s *Service) keepStoredLocked(stored, next Policy) Policy {
	return s.overlay().Restore(stored, next)
}

// lockedChanges returns the locked policy fields whose value differs between a and b.
func (s *Service) lockedChanges(a, b Policy) []string { return s.overlay().Changed(a, b) }

// UpdatePolicy merges a partial JSON policy over the current one.
func (s *Service) UpdatePolicy(ctx context.Context, patch []byte) (Policy, error) {
	stored, _, _, err := s.storedPolicy(ctx)
	if err != nil {
		return Policy{}, err
	}
	// The patch is checked against what the user sees (stored plus managed) but
	// saved over the stored policy, with locked fields left as the user had
	// them, so a managed value is never persisted as the user's own.
	current := s.applyManaged(stored)
	next, err := ApplyPatch(current, patch)
	if err != nil {
		return Policy{}, err
	}
	if err := s.managed.Refuse(s.lockedChanges(current, next)...); err != nil {
		return Policy{}, err
	}
	toSave, err := ApplyPatch(stored, patch)
	if err != nil {
		return Policy{}, err
	}
	toSave = s.keepStoredLocked(stored, toSave)
	doc, err := json.Marshal(toSave)
	if err != nil {
		return Policy{}, err
	}
	if err := s.store.SetCleanupPolicy(ctx, string(doc), s.cfg.Clock().UTC()); err != nil {
		return Policy{}, err
	}
	return s.applyManaged(toSave), nil
}

// Run performs one run and records it. It returns ErrBusy when another run is
// in progress.
func (s *Service) Run(ctx context.Context, req RunRequest) (Report, error) {
	if req.Pass == "" {
		req.Pass = PassFull
	}
	if !req.Pass.Valid() {
		return Report{}, fmt.Errorf("unknown cleanup pass %q", req.Pass)
	}
	if req.Trigger == "" {
		req.Trigger = TriggerManual
	}
	if !s.run.TryLock() {
		return Report{}, ErrBusy
	}
	defer s.run.Unlock()
	s.setRunning(true)
	defer s.setRunning(false)

	report := Report{StartedAt: s.cfg.Clock().UTC(), Trigger: req.Trigger, Pass: req.Pass, DryRun: req.DryRun, Categories: []CategoryReport{}}
	policy, _, err := s.Policy(ctx)
	if err != nil {
		return Report{}, err
	}
	if req.StorageSafe {
		policy = policy.forStorageCleanup()
	}
	sessions, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("cleanup: list sessions: %w", err)
	}
	for _, rule := range s.rules {
		cat := CategoryReport{Category: rule.Category(), Items: []Item{}}
		switch {
		case len(req.Only) > 0 && !slices.Contains(req.Only, rule.Category()):
			cat.Skipped = "not part of this run"
		case !rule.Enabled(policy):
			cat.Skipped = "turned off"
		case req.Pass == PassLight && !rule.Light():
			cat.Skipped = "runs in the full pass"
		default:
			run := &Run{
				DryRun: req.DryRun, Pass: req.Pass, ConfirmArtifacts: req.ConfirmArtifacts,
				Now: report.StartedAt, Policy: policy, Sessions: sessions, Only: req.Only, earlier: report.Categories,
				report: &cat, pause: s.cfg.Pause, store: s.store,
			}
			if err := rule.Run(ctx, run); err != nil {
				if ctx.Err() != nil {
					report.Error = "cancelled"
				}
				cat.Error = err.Error()
				s.cfg.Logger.Warn("cleanup rule failed", "category", rule.Category(), "err", err)
			}
		}
		cat.finish()
		report.FreedBytes += cat.FreedBytes
		report.Categories = append(report.Categories, cat)
		if ctx.Err() != nil {
			break
		}
	}
	report.FinishedAt = s.cfg.Clock().UTC()
	// Record even a cancelled run: whatever it freed is gone either way.
	recordCtx := context.WithoutCancel(ctx)
	id, err := s.record(recordCtx, report)
	if err != nil {
		s.cfg.Logger.Warn("cleanup: record run failed", "err", err)
	}
	report.ID = id
	if anchorsSchedule(req.DryRun, req.Trigger) {
		s.mu.Lock()
		s.lastLight = report.StartedAt
		if req.Pass == PassFull {
			s.lastFull = report.StartedAt
		}
		s.mu.Unlock()
	}
	if req.Trigger == TriggerSchedule {
		s.mu.Lock()
		s.scheduled = true
		s.mu.Unlock()
	}
	if report.FreedBytes > 0 {
		s.cfg.Logger.Info("cleanup freed space", "pass", req.Pass, "trigger", req.Trigger, "freedBytes", report.FreedBytes)
	}
	return report, nil
}

func (s *Service) record(ctx context.Context, report Report) (int64, error) {
	doc, err := json.Marshal(report) //nolint:gosec // "pass" is the cleanup pass kind (light or full), not a credential.
	if err != nil {
		return 0, err
	}
	id, err := s.store.InsertCleanupRun(ctx, sqlitestore.CleanupRun{
		StartedAt: report.StartedAt, FinishedAt: report.FinishedAt, Trigger: string(report.Trigger),
		Pass: string(report.Pass), DryRun: report.DryRun, FreedBytes: report.FreedBytes,
		Report: string(doc), Error: report.Error,
	})
	if err != nil {
		return 0, err
	}
	keep := keepRealRuns
	if report.DryRun {
		keep = keepDryRuns
	}
	return id, s.store.TrimCleanupRuns(ctx, report.DryRun, keep)
}

func (s *Service) setRunning(v bool) {
	s.mu.Lock()
	s.running = v
	s.mu.Unlock()
}

func decodeRun(row sqlitestore.CleanupRun) (Report, error) {
	var report Report
	if err := json.Unmarshal([]byte(row.Report), &report); err != nil {
		return Report{}, fmt.Errorf("cleanup run %d: %w", row.ID, err)
	}
	report.ID = row.ID
	return report, nil
}

// History lists recent runs, newest first.
func (s *Service) History(ctx context.Context, limit int, includeDryRuns bool) ([]Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	fetch := int64(limit)
	if !includeDryRuns {
		fetch += keepDryRuns
	}
	rows, err := s.store.ListCleanupRuns(ctx, fetch)
	if err != nil {
		return nil, err
	}
	out := make([]Report, 0, limit)
	for _, row := range rows {
		if row.DryRun && !includeDryRuns {
			continue
		}
		report, err := decodeRun(row)
		if err != nil {
			continue
		}
		out = append(out, report)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// Status reports the policy, the latest runs and when the next passes are due.
func (s *Service) Status(ctx context.Context) (Status, error) {
	policy, updated, decodeErr, err := s.policy(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Policy: policy, PolicyUpdated: updated}
	if decodeErr != nil {
		out.PolicyError = decodeErr.Error()
	}
	if row, ok, err := s.store.LatestCleanupRun(ctx, false); err != nil {
		return Status{}, err
	} else if ok {
		if report, err := decodeRun(row); err == nil {
			out.LastRun = &report
		}
	}
	if row, ok, err := s.store.LatestCleanupRun(ctx, true); err != nil {
		return Status{}, err
	} else if ok {
		if report, err := decodeRun(row); err == nil {
			out.LastDryRun = &report
		}
	}
	if err := s.loadSchedule(ctx); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	out.Running = s.running
	out.LastFullRunAt = s.lastFull
	out.FirstPassPending = !s.scheduled
	if policy.Automatic {
		now := s.cfg.Clock().UTC()
		out.NextLightAt = nextAt(s.lastLight, s.cfg.LightEvery, now)
		out.NextFullAt = nextAt(s.lastFull, s.cfg.FullEvery, now)
	}
	s.mu.Unlock()
	return out, nil
}

// nextAt is when a pass last run at last is due again; a pass that never ran,
// or is overdue, is due now.
func nextAt(last time.Time, every time.Duration, now time.Time) time.Time {
	if next := last.Add(every); !last.IsZero() && next.After(now) {
		return next
	}
	return now
}

// anchorsSchedule reports whether a run counts as a pass for the schedule:
// every real run, and scheduled dry runs (the first scheduled pass). A dry run
// the user starts (Scan) must not push the real passes back.
func anchorsSchedule(dryRun bool, trigger Trigger) bool {
	return !dryRun || trigger == TriggerSchedule
}

// loadSchedule seeds the schedule from history once, so a restart does not
// repeat a pass that ran shortly before it, or the first dry-run pass.
func (s *Service) loadSchedule(ctx context.Context) error {
	s.mu.Lock()
	loaded := s.scheduleLoaded
	s.mu.Unlock()
	if loaded {
		return nil
	}
	runs, err := s.store.ListCleanupRuns(ctx, keepRealRuns+keepDryRuns)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range runs {
		trigger := Trigger(run.Trigger)
		if trigger == TriggerSchedule {
			s.scheduled = true
		}
		if !anchorsSchedule(run.DryRun, trigger) {
			continue
		}
		if run.StartedAt.After(s.lastLight) {
			s.lastLight = run.StartedAt
		}
		if Pass(run.Pass) == PassFull && run.StartedAt.After(s.lastFull) {
			s.lastFull = run.StartedAt
		}
	}
	s.scheduleLoaded = true
	return nil
}

// due reports which pass, if any, the schedule calls for now.
func (s *Service) due(now time.Time) (Pass, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastFull) >= s.cfg.FullEvery {
		return PassFull, true
	}
	if now.Sub(s.lastLight) >= s.cfg.LightEvery {
		return PassLight, true
	}
	return "", false
}

// Tick runs a pass if one is due and the policy is automatic. It is the
// scheduler's unit of work, exported for tests.
func (s *Service) Tick(ctx context.Context) {
	policy, _, err := s.Policy(ctx)
	if err != nil || !policy.Automatic {
		return
	}
	if err := s.loadSchedule(ctx); err != nil {
		s.cfg.Logger.Warn("cleanup: read history failed", "err", err)
		return
	}
	pass, ok := s.due(s.cfg.Clock().UTC())
	if !ok {
		return
	}
	// The first scheduled pass after cleanup arrives on an install is a dry
	// run: it reports what later passes will remove, by category, before
	// anything is deleted. Real passes follow on the normal schedule.
	s.mu.Lock()
	first := !s.scheduled
	s.mu.Unlock()
	if first {
		pass = PassFull
	}
	report, err := s.Run(ctx, RunRequest{Pass: pass, Trigger: TriggerSchedule, DryRun: first})
	if err != nil {
		if !errors.Is(err, ErrBusy) && ctx.Err() == nil {
			s.cfg.Logger.Warn("cleanup: scheduled pass failed", "pass", pass, "err", err)
		}
		return
	}
	if first {
		wouldFree, pending := report.Reclaimable()
		s.cfg.Logger.Info("cleanup: first pass was a dry run; nothing was removed",
			"wouldFreeBytes", wouldFree, "awaitingConfirmationBytes", pending)
	}
}

// Start runs the scheduler until ctx ends. The returned channel closes once
// the goroutine has exited, so the caller can wait for it before closing the
// store.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !sleep(ctx, s.cfg.StartDelay) {
			return
		}
		ticker := time.NewTicker(schedulerTick)
		defer ticker.Stop()
		for {
			s.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
