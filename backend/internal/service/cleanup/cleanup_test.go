package cleanup

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeStore struct {
	mu        sync.Mutex
	policy    string
	policyErr error
	runs      []sqlitestore.CleanupRun
	sessions  []domain.SessionRecord
	trims     int
}

func (f *fakeStore) GetCleanupPolicy(context.Context) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy, time.Time{}, f.policyErr
}

func (f *fakeStore) SetCleanupPolicy(_ context.Context, policy string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policy = policy
	return nil
}

func (f *fakeStore) InsertCleanupRun(_ context.Context, run sqlitestore.CleanupRun) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run.ID = int64(len(f.runs) + 1)
	f.runs = append(f.runs, run)
	return run.ID, nil
}

func (f *fakeStore) ListCleanupRuns(_ context.Context, limit int64) ([]sqlitestore.CleanupRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sqlitestore.CleanupRun
	for i := len(f.runs) - 1; i >= 0 && int64(len(out)) < limit; i-- {
		out = append(out, f.runs[i])
	}
	return out, nil
}

func (f *fakeStore) latest(match func(sqlitestore.CleanupRun) bool) (sqlitestore.CleanupRun, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.runs) - 1; i >= 0; i-- {
		if match(f.runs[i]) {
			return f.runs[i], true
		}
	}
	return sqlitestore.CleanupRun{}, false
}

func (f *fakeStore) LatestCleanupRun(_ context.Context, dryRun bool) (sqlitestore.CleanupRun, bool, error) {
	run, ok := f.latest(func(r sqlitestore.CleanupRun) bool { return r.DryRun == dryRun })
	return run, ok, nil
}

func (f *fakeStore) TrimCleanupRuns(context.Context, bool, int64) error {
	f.mu.Lock()
	f.trims++
	f.mu.Unlock()
	return nil
}

func (f *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SessionRecord(nil), f.sessions...), nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.sessions {
		if rec.ID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakeStore) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

// fakeRule records an item per run and can block until released.
type fakeRule struct {
	cat     Category
	light   bool
	enabled func(Policy) bool
	block   chan struct{}
	started chan struct{}
	calls   int
	mu      sync.Mutex
}

func (r *fakeRule) Category() Category { return r.cat }
func (r *fakeRule) Light() bool        { return r.light }
func (r *fakeRule) Enabled(p Policy) bool {
	if r.enabled == nil {
		return true
	}
	return r.enabled(p)
}

func (r *fakeRule) Run(ctx context.Context, run *Run) error {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.started != nil {
		close(r.started)
	}
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	status := StatusFreed
	if run.DryRun {
		status = StatusWouldFree
	}
	run.Add(Item{What: "thing", Bytes: 10, Status: status})
	return nil
}

func (r *fakeRule) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestService(store *fakeStore, rules ...Rule) (*Service, *clock) {
	clk := &clock{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	return New(store, rules, Config{Pause: -1, StartDelay: -1, Clock: clk.Now}), clk
}

func TestDecodePolicy(t *testing.T) {
	if got := DecodePolicy("{}"); !reflect.DeepEqual(got, DefaultPolicy()) {
		t.Fatalf("empty doc = %+v, want defaults", got)
	}
	got := DecodePolicy(`{"worktrees":{"retentionDays":7}}`)
	if got.Worktrees.RetentionDays != 7 || !got.Worktrees.Enabled || !reflect.DeepEqual(got.Artifacts, DefaultPolicy().Artifacts) {
		t.Fatalf("partial doc = %+v, want only worktree retention changed", got)
	}
	for _, doc := range []string{"not json", `{"artifacts":{"retentionDays":0}}`, `{"artifacts":{"deliverableAction":"shred"}}`, `{"automatic":false,"worktrees":{"retentionDays":-1}}`} {
		got := DecodePolicy(doc)
		if !reflect.DeepEqual(got, FailSafePolicy()) || got.Automatic {
			t.Fatalf("DecodePolicy(%q) = %+v, want the defaults with automatic off", doc, got)
		}
	}
}

func TestStatusSurfacesAnInvalidStoredPolicy(t *testing.T) {
	store := &fakeStore{policy: `{"worktrees":{"retentionDays":-5}}`}
	svc, _ := newTestService(store)
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PolicyError == "" || status.Policy.Automatic {
		t.Fatalf("status = %+v, want the decode error surfaced and automatic off", status)
	}
}

func TestManualScanDoesNotDelayScheduledPasses(t *testing.T) {
	store := &fakeStore{policy: "{}"}
	rule := &fakeRule{cat: CategoryWorktrees, light: true}
	svc, clk := newTestService(store, rule)
	ctx := context.Background()
	svc.Tick(ctx) // first scheduled pass (dry)
	clk.Advance(DefaultLightEvery)
	if _, err := svc.Run(ctx, RunRequest{DryRun: true}); err != nil { // a Scan from Settings
		t.Fatal(err)
	}
	svc.Tick(ctx)
	if last := store.runs[len(store.runs)-1]; last.DryRun || last.Trigger != string(TriggerSchedule) {
		t.Fatalf("last run = %+v, want a real scheduled pass despite the manual scan", last)
	}
}

func TestCorruptPolicyRunsNoScheduledPass(t *testing.T) {
	store := &fakeStore{policy: `{"automatic":false,"worktrees":{"retentionDays":99999}}`}
	rule := &fakeRule{cat: CategoryWorktrees, light: true}
	svc, _ := newTestService(store, rule)
	svc.Tick(context.Background())
	if rule.callCount() != 0 || store.runCount() != 0 {
		t.Fatal("a scheduled pass ran on a corrupt policy")
	}
}

func TestApplyPatch(t *testing.T) {
	base := DefaultPolicy()
	next, err := ApplyPatch(base, []byte(`{"artifacts":{"autoDeliverables":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !next.Artifacts.AutoDeliverables || next.Artifacts.RetentionDays != base.Artifacts.RetentionDays || next.Artifacts.AutoPrune {
		t.Fatalf("patch = %+v, want only autoDeliverables changed", next.Artifacts)
	}
	for _, patch := range []string{``, `{"artifact":{}}`, `{"worktrees":{"retentionDays":-1}}`, `{"artifacts":{"retentionDays":99999}}`, `[]`} {
		if _, err := ApplyPatch(base, []byte(patch)); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("ApplyPatch(%q) err = %v, want ErrInvalidPolicy", patch, err)
		}
	}
}

func TestUnreadablePolicyTurnsAutomaticOff(t *testing.T) {
	store := &fakeStore{policyErr: errors.New("disk")}
	rule := &fakeRule{cat: CategoryWorktrees, light: true}
	svc, _ := newTestService(store, rule)
	svc.Tick(context.Background())
	if rule.callCount() != 0 || store.runCount() != 0 {
		t.Fatal("a scheduled pass ran although the policy could not be read")
	}
}

func TestRunRecordsHistoryAndDryRunsSeparately(t *testing.T) {
	store := &fakeStore{policy: "{}"}
	svc, _ := newTestService(store, &fakeRule{cat: CategoryWorktrees, light: true})
	dry, err := svc.Run(context.Background(), RunRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.FreedBytes != 0 || dry.Categories[0].WouldFreeBytes != 10 || dry.Pass != PassFull || dry.Trigger != TriggerManual {
		t.Fatalf("dry run report = %+v", dry)
	}
	realRun, err := svc.Run(context.Background(), RunRequest{Pass: PassLight})
	if err != nil {
		t.Fatal(err)
	}
	if realRun.FreedBytes != 10 || realRun.ID == 0 {
		t.Fatalf("real run report = %+v", realRun)
	}
	status, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastRun == nil || status.LastRun.ID != realRun.ID || status.LastDryRun == nil || status.LastDryRun.ID != dry.ID {
		t.Fatalf("status = %+v", status)
	}
	history, err := svc.History(context.Background(), 10, false)
	if err != nil || len(history) != 1 || history[0].ID != realRun.ID {
		t.Fatalf("history without dry runs = %+v, %v", history, err)
	}
	if history, _ := svc.History(context.Background(), 10, true); len(history) != 2 {
		t.Fatalf("history with dry runs = %d entries, want 2", len(history))
	}
	if store.trims != 2 {
		t.Fatalf("history trimmed %d times, want after every run", store.trims)
	}
}

func TestRunSkipsDisabledAndFullOnlyRules(t *testing.T) {
	store := &fakeStore{policy: `{"worktrees":{"enabled":false}}`}
	off := &fakeRule{cat: CategoryWorktrees, light: true, enabled: func(p Policy) bool { return p.Worktrees.Enabled }}
	full := &fakeRule{cat: CategoryArtifacts}
	svc, _ := newTestService(store, off, full)
	report, err := svc.Run(context.Background(), RunRequest{Pass: PassLight})
	if err != nil {
		t.Fatal(err)
	}
	if off.callCount() != 0 || full.callCount() != 0 {
		t.Fatalf("calls: disabled=%d full-only=%d, want none", off.callCount(), full.callCount())
	}
	if report.Categories[0].Skipped == "" || report.Categories[1].Skipped == "" {
		t.Fatalf("skips not reported: %+v", report.Categories)
	}
	if _, err := svc.Run(context.Background(), RunRequest{Pass: PassFull}); err != nil {
		t.Fatal(err)
	}
	if full.callCount() != 1 || off.callCount() != 0 {
		t.Fatalf("full pass calls: disabled=%d full-only=%d", off.callCount(), full.callCount())
	}
}

func TestRunRefusesAConcurrentRun(t *testing.T) {
	store := &fakeStore{policy: "{}"}
	rule := &fakeRule{cat: CategoryWorktrees, light: true, block: make(chan struct{}), started: make(chan struct{})}
	svc, _ := newTestService(store, rule)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Run(context.Background(), RunRequest{})
		done <- err
	}()
	<-rule.started
	if _, err := svc.Run(context.Background(), RunRequest{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent run err = %v, want ErrBusy", err)
	}
	if status, _ := svc.Status(context.Background()); !status.Running {
		t.Fatal("status does not report the run in progress")
	}
	close(rule.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTickSchedulesFullThenLightPasses(t *testing.T) {
	store := &fakeStore{policy: "{}"}
	light := &fakeRule{cat: CategoryWorktrees, light: true}
	full := &fakeRule{cat: CategoryArtifacts}
	svc, clk := newTestService(store, light, full)
	ctx := context.Background()

	if status, _ := svc.Status(ctx); !status.FirstPassPending {
		t.Fatal("status does not report the pending first pass")
	}
	svc.Tick(ctx) // nothing ran yet: the full pass is due, as a dry run
	if full.callCount() != 1 || light.callCount() != 1 {
		t.Fatalf("first tick: light=%d full=%d, want a full pass", light.callCount(), full.callCount())
	}
	if store.runs[0].DryRun != true || store.runs[0].Trigger != string(TriggerSchedule) {
		t.Fatalf("first scheduled pass = %+v, want a dry run", store.runs[0])
	}
	if status, _ := svc.Status(ctx); status.FirstPassPending {
		t.Fatal("status still reports the first pass pending after it ran")
	}
	svc.Tick(ctx)
	if store.runCount() != 1 {
		t.Fatal("a pass ran again before it was due")
	}
	clk.Advance(DefaultLightEvery)
	svc.Tick(ctx)
	if store.runs[1].DryRun {
		t.Fatal("the pass after the first one was a dry run too")
	}
	if light.callCount() != 2 || full.callCount() != 1 {
		t.Fatalf("after an hour: light=%d full=%d, want a light pass", light.callCount(), full.callCount())
	}
	clk.Advance(DefaultFullEvery)
	svc.Tick(ctx)
	if full.callCount() != 2 {
		t.Fatalf("after a day: full=%d, want a second full pass", full.callCount())
	}

	// A restarted daemon reads the last runs from history instead of repeating them.
	restarted, _ := newTestService(store, light, full)
	restarted.cfg.Clock = clk.Now
	restarted.Tick(ctx)
	if full.callCount() != 2 || light.callCount() != 3 {
		t.Fatalf("restart repeated a pass: light=%d full=%d", light.callCount(), full.callCount())
	}
}

func TestTickDoesNothingWhenAutomaticIsOff(t *testing.T) {
	store := &fakeStore{policy: `{"automatic":false}`}
	rule := &fakeRule{cat: CategoryWorktrees, light: true}
	svc, _ := newTestService(store, rule)
	svc.Tick(context.Background())
	if rule.callCount() != 0 {
		t.Fatal("a scheduled pass ran with automatic cleanup off")
	}
	if _, err := svc.Run(context.Background(), RunRequest{}); err != nil || rule.callCount() != 1 {
		t.Fatalf("a manual run must still work: calls=%d err=%v", rule.callCount(), err)
	}
}

func TestStorageSafeTurnsOnSpaceRulesAndOnlyLimitsCategories(t *testing.T) {
	store := &fakeStore{policy: `{"worktrees":{"enabled":false}}`}
	wt := &fakeRule{cat: CategoryWorktrees, enabled: func(p Policy) bool { return p.Worktrees.Enabled }}
	branches := &fakeRule{cat: CategoryBranches, enabled: func(p Policy) bool { return p.Branches.Enabled }}
	svc, _ := newTestService(store, wt, branches)
	if _, err := svc.Run(context.Background(), RunRequest{Pass: PassFull, StorageSafe: true, Only: []Category{CategoryWorktrees, CategoryBranches}}); err != nil {
		t.Fatal(err)
	}
	if wt.callCount() != 1 {
		t.Fatalf("worktree rule calls = %d, want 1 (StorageSafe enables it)", wt.callCount())
	}
	if branches.callCount() != 0 {
		t.Fatal("StorageSafe must not turn on branch deletion")
	}
	if _, err := svc.Run(context.Background(), RunRequest{Pass: PassFull, StorageSafe: true, Only: []Category{CategoryGoBuildCache}}); err != nil {
		t.Fatal(err)
	}
	if wt.callCount() != 1 {
		t.Fatal("Only did not skip the worktree rule")
	}
}
