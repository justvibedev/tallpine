package orchestratorcompact

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	sessions []domain.SessionRecord
	lanes    map[domain.SessionID]string
	reports  map[domain.SessionID][]domain.ReportRecord
	reviews  map[domain.SessionID][]domain.ReviewRun
}

func (f *fakeStore) ListChatHibernationCandidates(context.Context) ([]domain.SessionID, error) {
	var ids []domain.SessionID
	for _, s := range f.sessions {
		ids = append(ids, s.ID)
	}
	return ids, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			return s, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakeStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}

func (f *fakeStore) ListReportsBySession(_ context.Context, id domain.SessionID) ([]domain.ReportRecord, error) {
	return f.reports[id], nil
}

func (f *fakeStore) ListRunningReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	return f.reviews[id], nil
}

func (f *fakeStore) ListSessionLanes(context.Context, domain.ProjectID) (map[domain.SessionID]string, error) {
	return f.lanes, nil
}

func session(id domain.SessionID, kind domain.SessionKind, quiet time.Duration) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "p", Kind: kind, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-quiet)}}
}

type harness struct {
	store     *fakeStore
	state     ChatState
	compacted []domain.SessionID
	bg        int
	now       time.Time
	// onCompact runs inside the fake Compact, standing in for the real turn.
	onCompact func() error
	logs      *bytes.Buffer
	sweeper   *Sweeper
}

// newHarness is one idle orchestrator above its threshold with one worker
// whose task is done: it qualifies until a test adds something in flight.
func newHarness() *harness {
	h := &harness{
		store: &fakeStore{
			sessions: []domain.SessionRecord{session("orch", domain.KindOrchestrator, 15*time.Minute), session("w1", domain.KindWorker, time.Hour)},
			reports: map[domain.SessionID][]domain.ReportRecord{
				"w1": {{State: domain.ReportCheckpoint, DeliveryState: domain.ReportAcknowledged}, {State: domain.ReportDone, DeliveryState: domain.ReportAcknowledged}},
			},
			reviews: map[domain.SessionID][]domain.ReviewRun{},
		},
		state: ChatState{ContextUsed: 150_000, Threshold: 100_000},
		logs:  &bytes.Buffer{},
		now:   now,
	}
	h.sweeper = New(Deps{
		Store: h.store,
		State: func(context.Context, domain.SessionID) (ChatState, bool) { return h.state, true },
		Compact: func(_ context.Context, id domain.SessionID) error {
			h.compacted = append(h.compacted, id)
			if h.onCompact != nil {
				return h.onCompact()
			}
			return nil
		},
		BackgroundTasks: func(domain.SessionID) int { return h.bg },
		Now:             func() time.Time { return h.now },
		Log:             slog.New(slog.NewTextHandler(h.logs, nil)),
	})
	return h
}

func (h *harness) sweep(t *testing.T) {
	t.Helper()
	if err := h.sweeper.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The compaction is a turn: like the real one, the fake moves the
// orchestrator's activity and leaves a summary that is still above the
// threshold. It must not be compacted again until a real turn runs.
func TestSweepCompactsAnIdleOrchestratorOnce(t *testing.T) {
	h := newHarness()
	h.onCompact = func() error {
		h.store.sessions[0].Activity.LastActivityAt = h.now
		h.state = ChatState{Busy: true, ContextUsed: 150_000, Threshold: 100_000}
		return nil
	}
	h.sweep(t)
	h.state = ChatState{ContextUsed: 120_000, Threshold: 100_000} // turn ended
	for range 3 {
		h.now = h.now.Add(11 * time.Minute)
		h.sweep(t)
	}
	if len(h.compacted) != 1 || h.compacted[0] != "orch" {
		t.Fatalf("compacted = %v, want orch once (the worker is never compacted)", h.compacted)
	}
	// A real turn changes the context and re-arms it.
	h.state.ContextUsed = 140_000
	h.store.sessions[0].Activity.LastActivityAt = h.now
	h.now = h.now.Add(11 * time.Minute)
	h.sweep(t)
	if len(h.compacted) != 2 {
		t.Fatalf("compacted = %v after a real turn, want a second compaction", h.compacted)
	}
}

// A failed attempt is not retried every sweep; a real turn re-arms it.
func TestSweepDoesNotRetryAFailedCompactionEverySweep(t *testing.T) {
	h := newHarness()
	h.onCompact = func() error { return errors.New("provider down") }
	if err := h.sweeper.Sweep(context.Background()); err == nil {
		t.Fatal("the failed compaction was not reported")
	}
	for range 3 {
		h.now = h.now.Add(time.Minute)
		h.sweep(t)
	}
	if len(h.compacted) != 1 {
		t.Fatalf("attempts = %d, want 1 until a real turn", len(h.compacted))
	}
	h.onCompact = nil
	h.state.ContextUsed = 160_000
	h.sweep(t)
	if len(h.compacted) != 2 {
		t.Fatalf("attempts = %d after a real turn, want 2", len(h.compacted))
	}
}

func TestSweepSkipsWhileSomethingIsInFlight(t *testing.T) {
	for name, setup := range map[string]func(*harness){
		"busy":            func(h *harness) { h.state.Busy = true },
		"below threshold": func(h *harness) { h.state.ContextUsed = 50_000 },
		"recent activity": func(h *harness) { h.store.sessions[0].Activity.LastActivityAt = now.Add(-time.Minute) },
		"background task": func(h *harness) { h.bg = 1 },
		"pending report": func(h *harness) {
			h.store.reports["w1"] = append(h.store.reports["w1"], domain.ReportRecord{State: domain.ReportDone, DeliveryState: domain.ReportPending})
		},
		"worker in flight": func(h *harness) {
			h.store.reports["w1"] = append(h.store.reports["w1"], domain.ReportRecord{State: domain.ReportNeedsInput, DeliveryState: domain.ReportAcknowledged})
		},
		"worker never reported": func(h *harness) { h.store.reports["w1"] = nil },
		"review running":        func(h *harness) { h.store.reviews["w1"] = []domain.ReviewRun{{}} },
	} {
		h := newHarness()
		setup(h)
		h.sweep(t)
		if len(h.compacted) != 0 {
			t.Errorf("%s: compacted %v", name, h.compacted)
		}
	}
}

// A worker with an open task that has been quiet past the stall limit no
// longer holds compaction back, and is logged once.
func TestSweepDoesNotWaitForAStalledWorker(t *testing.T) {
	h := newHarness()
	h.store.sessions[1].Activity.LastActivityAt = now.Add(-3 * time.Hour)
	h.store.reports["w1"] = []domain.ReportRecord{{State: domain.ReportCheckpoint, DeliveryState: domain.ReportAcknowledged}}
	h.sweep(t)
	if len(h.compacted) != 1 {
		t.Fatalf("compacted = %v, want the orchestrator despite the stalled worker", h.compacted)
	}
	h.state.ContextUsed = 151_000 // a real turn since the compaction
	h.sweep(t)
	if n := strings.Count(h.logs.String(), "worker with an open task has been quiet"); n != 1 {
		t.Fatalf("stalled worker logged %d times, want once:\n%s", n, h.logs)
	}
}

// A worker routed to another lane's orchestrator does not hold this one back,
// and a terminated worker counts only for its undelivered reports.
func TestSweepCountsOnlyWorkersRoutedToTheOrchestrator(t *testing.T) {
	h := newHarness()
	h.store.sessions = append(h.store.sessions, session("lane-orch", domain.KindOrchestrator, 15*time.Minute), session("w2", domain.KindWorker, time.Minute))
	h.store.lanes = map[domain.SessionID]string{"lane-orch": "B1", "w2": "B1"}
	h.store.reports["w2"] = []domain.ReportRecord{{State: domain.ReportCheckpoint, DeliveryState: domain.ReportPending}}
	if ok, err := h.sweeper.MaybeCompact(context.Background(), "orch"); err != nil || !ok {
		t.Fatalf("default orchestrator held back by another lane's worker: ok=%v err=%v", ok, err)
	}
	if ok, _ := h.sweeper.MaybeCompact(context.Background(), "lane-orch"); ok {
		t.Fatal("lane orchestrator compacted with its own worker in flight")
	}
	h = newHarness()
	h.store.sessions[1].IsTerminated = true
	h.store.reports["w1"] = []domain.ReportRecord{{State: domain.ReportCheckpoint, DeliveryState: domain.ReportAcknowledged}}
	if ok, _ := h.sweeper.MaybeCompact(context.Background(), "orch"); !ok {
		t.Fatal("a terminated worker's open task held compaction back")
	}
}

// A project that chose handoff gets one instead of a compaction, and only
// once everything the compaction gates on is clear.
func TestIdleHandoffReplacesCompaction(t *testing.T) {
	h := newHarness()
	h.state.Handoff = true
	var handed []domain.SessionID
	h.sweeper.deps.Handoff = func(_ context.Context, id domain.SessionID) error {
		handed = append(handed, id)
		return nil
	}
	h.store.reviews["w1"] = []domain.ReviewRun{{}}
	h.sweep(t)
	if len(handed) != 0 || len(h.compacted) != 0 {
		t.Fatalf("handed off %v / compacted %v with a review running", handed, h.compacted)
	}
	h.store.reviews["w1"] = nil
	h.sweep(t)
	if len(handed) != 1 || handed[0] != "orch" || len(h.compacted) != 0 {
		t.Fatalf("handed off %v, compacted %v; want one handoff and no compaction", handed, h.compacted)
	}
}

// Without a wired handoff, a project that asks for one still compacts.
func TestIdleHandoffWithoutDepCompacts(t *testing.T) {
	h := newHarness()
	h.state.Handoff = true
	h.sweep(t)
	if len(h.compacted) != 1 {
		t.Fatalf("compacted = %v, want one", h.compacted)
	}
}

// The before-hibernate hook runs under the hibernate operation, where a
// handoff would deadlock: it must not hand off, and keeps the orchestrator
// awake so the sweep can.
func TestHibernateHookNeverHandsOff(t *testing.T) {
	h := newHarness()
	h.state.Handoff = true
	handed := 0
	h.sweeper.deps.Handoff = func(context.Context, domain.SessionID) error { handed++; return nil }
	keep, err := h.sweeper.MaybeCompact(context.Background(), "orch")
	if err != nil || !keep || handed != 0 || len(h.compacted) != 0 {
		t.Fatalf("hook: keep=%v err=%v handed=%d compacted=%v; want keep awake, no handoff, no compaction", keep, err, handed, h.compacted)
	}
	h.sweep(t)
	if handed != 1 {
		t.Fatalf("sweep handed off %d times, want 1", handed)
	}
}

// A refused handoff falls back to compaction.
func TestIdleHandoffFailureCompacts(t *testing.T) {
	h := newHarness()
	h.state.Handoff = true
	h.sweeper.deps.Handoff = func(context.Context, domain.SessionID) error { return errors.New("no orchestrator agent") }
	h.sweep(t)
	if len(h.compacted) != 1 {
		t.Fatalf("compacted = %v, want a fallback compaction", h.compacted)
	}
}
