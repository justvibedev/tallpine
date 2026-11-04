package cleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeRetentionStore struct {
	mu          sync.Mutex
	events      map[domain.SessionID]int64
	transcripts map[domain.SessionID]int64
	marked      map[domain.SessionID][2]bool
	vacuums     int
	// failAfter makes the transcript purge delete this many rows, then fail.
	failAfter int64
	// onFootprint runs when a session is measured, before any purge.
	onFootprint func(domain.SessionID)
}

func newFakeRetentionStore(ids ...domain.SessionID) *fakeRetentionStore {
	f := &fakeRetentionStore{events: map[domain.SessionID]int64{}, transcripts: map[domain.SessionID]int64{}, marked: map[domain.SessionID][2]bool{}}
	for _, id := range ids {
		f.events[id], f.transcripts[id] = 10, 5
	}
	return f
}

func (f *fakeRetentionStore) SessionRetentionFootprint(_ context.Context, id domain.SessionID) (sqlitestore.RetentionFootprint, error) {
	if f.onFootprint != nil {
		f.onFootprint(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return sqlitestore.RetentionFootprint{
		ProviderEvents: f.events[id], ProviderEventBytes: f.events[id] * 100,
		TranscriptRows: f.transcripts[id], TranscriptRowsBytes: f.transcripts[id] * 100,
	}, nil
}

func (f *fakeRetentionStore) PurgeSessionTranscript(_ context.Context, id domain.SessionID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAfter > 0 {
		f.transcripts[id] -= f.failAfter
		return f.failAfter, errors.New("database is locked")
	}
	n := f.transcripts[id]
	f.transcripts[id] = 0
	return n, nil
}

func (f *fakeRetentionStore) MarkSessionRetentionPurge(_ context.Context, id domain.SessionID, events, transcript bool, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev := f.marked[id]
	f.marked[id] = [2]bool{prev[0] || events, prev[1] || transcript}
	return nil
}

func (f *fakeRetentionStore) OptimizeStorage(context.Context, int64) (sqlitestore.StorageMaintenance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vacuums++
	if f.vacuums > 2 {
		return sqlitestore.StorageMaintenance{IncrementalVacuum: true}, nil
	}
	return sqlitestore.StorageMaintenance{IncrementalVacuum: true, FreedPages: 100}, nil
}

var retentionNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func retentionSession(id, project string, terminated bool, endedAgo time.Duration) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), ProjectID: domain.ProjectID(project), IsTerminated: terminated, UpdatedAt: retentionNow.Add(-endedAgo)}
}

func runRetention(t *testing.T, sessions []domain.SessionRecord, data *fakeRetentionStore, policy RetentionPolicy, dryRun bool) CategoryReport {
	t.Helper()
	store := &fakeStore{policy: "{}", sessions: sessions}
	svc, _ := newTestService(store, NewRetentionRule(data, policy, "", nil, nil, nil))
	report, err := svc.Run(context.Background(), RunRequest{DryRun: dryRun, Only: []Category{CategorySessionRetention}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Categories {
		if c.Category == CategorySessionRetention {
			return c
		}
	}
	t.Fatal("no session_retention category in the report")
	return CategoryReport{}
}

func fixedPolicy(s RetentionSettings) RetentionPolicy {
	return func(context.Context, domain.ProjectID) RetentionSettings { return s }
}

var onAll = RetentionSettings{Enabled: true, Days: 7, DropWorktrees: true, DropTranscripts: true}

const day = 24 * time.Hour

// A dry run reports what a run would drop and the space it frees, even while
// the policy is off, and deletes nothing.
func TestRetentionDryRunDeletesNothing(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		data := newFakeRetentionStore("old")
		settings := onAll
		settings.Enabled = enabled
		cat := runRetention(t, []domain.SessionRecord{retentionSession("old", "p", true, 40*day)}, data, fixedPolicy(settings), true)
		if cat.WouldFreeBytes != 500 || cat.WouldFree != 1 || cat.FreedBytes != 0 {
			t.Fatalf("enabled=%v: dry run report = %+v", enabled, cat)
		}
		if data.events["old"] != 10 || data.transcripts["old"] != 5 || len(data.marked) != 0 || data.vacuums != 0 {
			t.Fatalf("enabled=%v: dry run changed data: events=%d transcripts=%d marked=%v vacuums=%d",
				enabled, data.events["old"], data.transcripts["old"], data.marked, data.vacuums)
		}
	}
}

// A real run purges only expired terminated sessions of projects where the
// policy is on, records the purge and vacuums.
func TestRetentionPurgesExpiredSessionsOfEnabledProjects(t *testing.T) {
	data := newFakeRetentionStore("old", "recent", "live", "off")
	policy := func(_ context.Context, project domain.ProjectID) RetentionSettings {
		if project == "quiet" {
			return RetentionSettings{Enabled: false, Days: 1}
		}
		return onAll
	}
	cat := runRetention(t, []domain.SessionRecord{
		retentionSession("old", "p", true, 40*day),
		retentionSession("recent", "p", true, 5*day),
		retentionSession("live", "p", false, 400*day),
		retentionSession("off", "quiet", true, 400*day),
	}, data, policy, false)
	if cat.FreedBytes != 500 || cat.Freed != 1 {
		t.Fatalf("report = %+v", cat)
	}
	if data.events["old"] != 10 || data.transcripts["old"] != 0 {
		t.Fatalf("old: events=%d transcripts=%d, want transcript purged and raw events left to the storage retention", data.events["old"], data.transcripts["old"])
	}
	for _, id := range []domain.SessionID{"recent", "live", "off"} {
		if data.events[id] != 10 || data.transcripts[id] != 5 {
			t.Fatalf("%s was touched", id)
		}
	}
	if got := data.marked["old"]; got != [2]bool{false, true} || len(data.marked) != 1 {
		t.Fatalf("marked = %v", data.marked)
	}
	if data.vacuums != 3 {
		t.Fatalf("vacuum steps = %d, want 3 (until no pages are freed)", data.vacuums)
	}
}

// A session restored between the snapshot and the purge is live and keeps
// everything.
func TestRetentionNeverTouchesASessionThatIsLiveAgain(t *testing.T) {
	sessions := []domain.SessionRecord{retentionSession("back", "p", true, 40*day)}
	store := &fakeStore{policy: "{}", sessions: sessions}
	data := newFakeRetentionStore("back")
	data.onFootprint = func(id domain.SessionID) {
		store.mu.Lock()
		defer store.mu.Unlock()
		for i := range store.sessions {
			if store.sessions[i].ID == id {
				store.sessions[i].IsTerminated = false
			}
		}
	}
	svc, _ := newTestService(store, NewRetentionRule(data, fixedPolicy(onAll), "", nil, nil, nil))
	report, err := svc.Run(context.Background(), RunRequest{Only: []Category{CategorySessionRetention}})
	if err != nil {
		t.Fatal(err)
	}
	if data.events["back"] != 10 || data.transcripts["back"] != 5 || len(data.marked) != 0 || data.vacuums != 0 {
		t.Fatalf("a live session was purged: events=%d transcripts=%d marked=%v", data.events["back"], data.transcripts["back"], data.marked)
	}
	idx := slices.IndexFunc(report.Categories, func(c CategoryReport) bool { return c.Category == CategorySessionRetention })
	if idx < 0 || report.Categories[idx].Kept != 1 {
		t.Fatalf("report = %+v, want both items kept", report.Categories)
	}
}

// orchestratorFixture is a live orchestrator and two long-ended ones sharing
// worktrees/<project>/orchestrator, plus an ended worker with its own
// worktree as the control that worktree removal works at all.
func orchestratorFixture(t *testing.T) (root string, sessions []domain.SessionRecord, remover *fakeRemover) {
	t.Helper()
	root = t.TempDir()
	shared := filepath.Join(root, "proj", "orchestrator")
	worker := filepath.Join(root, "proj", "worker-1")
	for _, dir := range []string{shared, worker} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orch := func(id string, terminated bool) domain.SessionRecord {
		rec := retentionSession(id, "proj", terminated, 400*day)
		rec.Kind, rec.Metadata.WorkspacePath = domain.KindOrchestrator, shared
		return rec
	}
	w := retentionSession("worker-1", "proj", true, 400*day)
	w.Metadata.WorkspacePath = worker
	sessions = []domain.SessionRecord{orch("orch-live", false), orch("orch-1", true), orch("orch-18", true), w}
	byID := map[domain.SessionID]domain.SessionRecord{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	remover = &fakeRemover{
		paths:   func(id domain.SessionID) []string { return []string{byID[id].Metadata.WorkspacePath} },
		current: func(id domain.SessionID) domain.SessionRecord { return byID[id] },
	}
	return root, sessions, remover
}

func runWithRemover(t *testing.T, root string, sessions []domain.SessionRecord, data *fakeRetentionStore, remover *fakeRemover, s RetentionSettings) {
	t.Helper()
	store := &fakeStore{policy: "{}", sessions: sessions}
	ok := func(context.Context, string, bool) (string, error) { return "", nil }
	svc, _ := newTestService(store, NewRetentionRule(data, fixedPolicy(s), root, remover, nil, ok))
	if _, err := svc.Run(context.Background(), RunRequest{Only: []Category{CategorySessionRetention}}); err != nil {
		t.Fatal(err)
	}
}

// By default orchestrators keep everything after they end: a fresh
// orchestrator chat reads its predecessors' history.
func TestRetentionExcludesOrchestratorsByDefault(t *testing.T) {
	root, sessions, remover := orchestratorFixture(t)
	data := newFakeRetentionStore("orch-live", "orch-1", "orch-18", "worker-1")
	runWithRemover(t, root, sessions, data, remover, onAll)
	for _, id := range []domain.SessionID{"orch-live", "orch-1", "orch-18"} {
		if data.events[id] != 10 || data.transcripts[id] != 5 || data.marked[id] != ([2]bool{}) {
			t.Fatalf("%s conversation data was purged", id)
		}
	}
	if got := remover.called(); !slices.Equal(got, []domain.SessionID{"worker-1"}) {
		t.Fatalf("worktree removals = %v, want only the worker's", got)
	}
	if data.transcripts["worker-1"] != 0 {
		t.Fatal("control: the ended worker was not purged")
	}
}

// With the opt-in, ended orchestrators lose their purgeable data, but the live
// one is untouched and the shared orchestrator worktree is never removed.
func TestRetentionIncludeOrchestratorsNeverTouchesTheSharedWorktree(t *testing.T) {
	root, sessions, remover := orchestratorFixture(t)
	data := newFakeRetentionStore("orch-live", "orch-1", "orch-18")
	s := onAll
	s.IncludeOrchestrators = true
	runWithRemover(t, root, sessions, data, remover, s)
	if data.transcripts["orch-1"] != 0 || data.transcripts["orch-18"] != 0 {
		t.Fatal("opted-in ended orchestrators were not purged")
	}
	if data.events["orch-live"] != 10 || data.transcripts["orch-live"] != 5 {
		t.Fatal("the live orchestrator was purged")
	}
	if got := remover.called(); slices.ContainsFunc(got, func(id domain.SessionID) bool { return id != "worker-1" }) {
		t.Fatalf("worktree removals = %v, the orchestrator worktree must never be removed", got)
	}
}

// The orchestrator kind alone protects its worktree, even when no other
// session shares it any more.
func TestRetentionNeverRemovesALoneOrchestratorWorktree(t *testing.T) {
	root, sessions, remover := orchestratorFixture(t)
	lone := sessions[1:2]
	s := onAll
	s.IncludeOrchestrators = true
	runWithRemover(t, root, lone, newFakeRetentionStore("orch-1"), remover, s)
	if got := remover.called(); len(got) != 0 {
		t.Fatalf("worktree removals = %v, want none", got)
	}
}

// A worktree another session still points at is kept, whatever its kind.
func TestRetentionKeepsAWorktreeSharedBetweenSessions(t *testing.T) {
	root, sessions, remover := orchestratorFixture(t)
	a, b := sessions[3], sessions[3]
	b.ID = "worker-2"
	runWithRemover(t, root, []domain.SessionRecord{a, b}, newFakeRetentionStore("worker-1", "worker-2"), remover, onAll)
	if got := remover.called(); len(got) != 0 {
		t.Fatalf("worktree removals = %v, a shared worktree must be kept", got)
	}
}

// A purge that fails after deleting some rows still leaves the session
// marked, so restore refuses its half-deleted history.
func TestRetentionMarksBeforeAPurgeThatFailsPartWay(t *testing.T) {
	data := newFakeRetentionStore("old")
	data.failAfter = 3
	cat := runRetention(t, []domain.SessionRecord{retentionSession("old", "p", true, 40*day)}, data, fixedPolicy(onAll), false)
	if data.transcripts["old"] != 2 || cat.Failed != 1 {
		t.Fatalf("fixture: transcripts=%d report=%+v, want a partial failed purge", data.transcripts["old"], cat)
	}
	if got := data.marked["old"]; !got[1] {
		t.Fatalf("marked = %v, want the session guarded despite the failure", data.marked)
	}
}

// A general dry run (tallpine cleanup --dry-run) with the policy off reports
// nothing for retention; only the retention-only preview shows it.
func TestRetentionGeneralDryRunIgnoresProjectsWithThePolicyOff(t *testing.T) {
	data := newFakeRetentionStore("old")
	off := onAll
	off.Enabled = false
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{retentionSession("old", "p", true, 40*day)}}
	svc, _ := newTestService(store, NewRetentionRule(data, fixedPolicy(off), "", nil, nil, nil))
	report, err := svc.Run(context.Background(), RunRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if wouldFree, _ := report.Reclaimable(); wouldFree != 0 {
		t.Fatalf("general dry run with the policy off would free %d bytes, want 0", wouldFree)
	}
}

// claimRule stands in for the worktree rule: it reports a worktree as freed.
type claimRule struct{ path string }

func (claimRule) Category() Category  { return CategoryWorktrees }
func (claimRule) Light() bool         { return true }
func (claimRule) Enabled(Policy) bool { return true }
func (c claimRule) Run(_ context.Context, run *Run) error {
	run.Add(Item{What: "worktree", Path: c.path, Bytes: 10, Status: StatusWouldFree})
	return nil
}

// A worktree the worktree rule already reported is not listed again.
func TestRetentionSkipsAWorktreeTheWorktreeRuleClaimed(t *testing.T) {
	root, sessions, remover := orchestratorFixture(t)
	worker := sessions[3]
	store := &fakeStore{policy: "{}", sessions: []domain.SessionRecord{worker}}
	ok := func(context.Context, string, bool) (string, error) { return "", nil }
	svc, _ := newTestService(store, claimRule{path: worker.Metadata.WorkspacePath},
		NewRetentionRule(newFakeRetentionStore(), fixedPolicy(onAll), root, remover, nil, ok))
	report, err := svc.Run(context.Background(), RunRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Categories {
		if c.Category == CategorySessionRetention && len(c.Items) != 0 {
			t.Fatalf("retention listed %v again", c.Items)
		}
	}
}

// Raw provider events follow app.providerEventRetentionDays only: a retention
// sweep with every purge on never touches them, even for an expired session.
func TestRetentionNeverPurgesProviderEvents(t *testing.T) {
	data := newFakeRetentionStore("old")
	runRetention(t, []domain.SessionRecord{retentionSession("old", "p", true, 400*day)}, data, fixedPolicy(onAll), false)
	if data.events["old"] != 10 {
		t.Fatalf("provider events = %d, want 10: the sessionRetention sweep must not drop them", data.events["old"])
	}
}
