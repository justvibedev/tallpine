package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// laneHarness runs the real report service, SQLite store and coordinator: the
// lane of a report is copied from the worker's session_lane row at Create, so a
// fake store would prove nothing about routing.
type laneHarness struct {
	t        *testing.T
	st       *sqlite.Store
	svc      *Service
	delivery *coordinatorDelivery
	coord    *Coordinator
	now      time.Time
}

func newLaneHarness(t *testing.T) *laneHarness {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{ID: "p", Path: "/tmp/p", RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	h := &laneHarness{t: t, st: st, delivery: &coordinatorDelivery{}, now: now}
	h.svc = New(Deps{Store: st})
	h.coord = NewCoordinator(CoordinatorDeps{Store: st, Delivery: h.delivery, Now: func() time.Time { return h.now.Add(time.Minute) }})
	return h
}

func (h *laneHarness) session(kind domain.SessionKind, lane string, terminated bool) domain.SessionID {
	h.t.Helper()
	h.now = h.now.Add(time.Second) // later sessions are newer
	rec, err := h.st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "p", Kind: kind, Harness: domain.HarnessClaudeCode, IsTerminated: terminated, CreatedAt: h.now,
	})
	if err != nil {
		h.t.Fatalf("create %s: %v", kind, err)
	}
	if lane != "" {
		if err := h.st.SetSessionLane(context.Background(), rec.ID, domain.SessionLane{LaneItemID: lane}); err != nil {
			h.t.Fatalf("set lane: %v", err)
		}
	}
	return rec.ID
}

func (h *laneHarness) report(worker domain.SessionID, msg string) {
	h.t.Helper()
	if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: worker, Message: msg, Immediate: true}); err != nil {
		h.t.Fatalf("report %s: %v", msg, err)
	}
}

func (h *laneHarness) run() map[domain.SessionID]string {
	h.t.Helper()
	if _, err := h.coord.RunDue(context.Background()); err != nil {
		h.t.Fatalf("RunDue: %v", err)
	}
	got := map[domain.SessionID]string{}
	for _, s := range h.delivery.submissions {
		got[s.target] += s.message
	}
	return got
}

func TestLaneReportsRouteOnlyToTheirLanesOrchestrator(t *testing.T) {
	h := newLaneHarness(t)
	def := h.session(domain.KindOrchestrator, "", false)
	orchA := h.session(domain.KindOrchestrator, "lane-a", false)
	orchB := h.session(domain.KindOrchestrator, "lane-b", false)
	h.report(h.session(domain.KindWorker, "lane-a", false), "msg-from-a")
	h.report(h.session(domain.KindWorker, "lane-b", false), "msg-from-b")
	h.report(h.session(domain.KindWorker, "", false), "msg-from-unlaned")

	got := h.run()
	for target, want := range map[domain.SessionID]string{orchA: "msg-from-a", orchB: "msg-from-b", def: "msg-from-unlaned"} {
		if !strings.Contains(got[target], want) {
			t.Errorf("orchestrator %s missing %q; got %q", target, want, got[target])
		}
	}
	for target, text := range got {
		for _, other := range []string{"msg-from-a", "msg-from-b", "msg-from-unlaned"} {
			own := map[domain.SessionID]string{orchA: "msg-from-a", orchB: "msg-from-b", def: "msg-from-unlaned"}[target]
			if other != own && strings.Contains(text, other) {
				t.Errorf("orchestrator %s also received %q", target, other)
			}
		}
	}
}

func TestLaneUnlanedWorkGoesToDefaultOrchestratorNotALane(t *testing.T) {
	h := newLaneHarness(t)
	def := h.session(domain.KindOrchestrator, "", false)
	h.session(domain.KindOrchestrator, "lane-a", false) // newest, but laned
	h.report(h.session(domain.KindWorker, "", false), "msg-unlaned")
	got := h.run()
	if !strings.Contains(got[def], "msg-unlaned") || len(h.delivery.submissions) != 1 {
		t.Fatalf("submissions = %#v, want one to the default orchestrator %s", h.delivery.submissions, def)
	}
}

func TestLaneWithNoLiveOrchestratorFallsBackToDefault(t *testing.T) {
	h := newLaneHarness(t)
	def := h.session(domain.KindOrchestrator, "", false)
	h.session(domain.KindOrchestrator, "lane-a", true) // retired
	h.report(h.session(domain.KindWorker, "lane-a", false), "msg-orphaned")
	got := h.run()
	if !strings.Contains(got[def], "msg-orphaned") {
		t.Fatalf("default orchestrator missing the orphaned lane's report: %#v", h.delivery.submissions)
	}
	if _, parked := h.coord.UndeliverableProjects()["p"]; parked {
		t.Fatal("project parked although a default orchestrator is running")
	}
}

// A lane whose orchestrator is gone, in a project with no default orchestrator,
// holds its reports: another lane's orchestrator must not receive them.
func TestLaneWithNoLiveOrchestratorNeverGoesToAnotherLane(t *testing.T) {
	h := newLaneHarness(t)
	h.session(domain.KindOrchestrator, "lane-a", true) // retired
	orchB := h.session(domain.KindOrchestrator, "lane-b", false)
	h.report(h.session(domain.KindWorker, "lane-a", false), "msg-orphaned")
	got := h.run()
	if got[orchB] != "" || len(h.delivery.submissions) != 0 {
		t.Fatalf("lane-a's report reached another lane: %#v", h.delivery.submissions)
	}
}

func TestNoLanesBehavesAsBefore(t *testing.T) {
	h := newLaneHarness(t)
	h.session(domain.KindOrchestrator, "", false)
	newest := h.session(domain.KindOrchestrator, "", false)
	h.report(h.session(domain.KindWorker, "", false), "one")
	h.report(h.session(domain.KindWorker, "", false), "two")
	got := h.run()
	if len(h.delivery.submissions) != 1 || !strings.Contains(got[newest], "one") || !strings.Contains(got[newest], "two") {
		t.Fatalf("submissions = %#v, want one batch with both reports to the newest orchestrator", h.delivery.submissions)
	}
}

// A lane whose batch is deferred (backoff, busy retry, stuck coalescing) must
// not be re-claimed just because another lane is due.
func TestLaneDeferredBatchIsNotClaimedWhenAnotherLaneIsDue(t *testing.T) {
	h := newLaneHarness(t)
	h.session(domain.KindOrchestrator, "", false)
	orchA := h.session(domain.KindOrchestrator, "lane-a", false)
	orchB := h.session(domain.KindOrchestrator, "lane-b", false)
	h.report(h.session(domain.KindWorker, "lane-b", false), "msg-b")
	ctx := context.Background()
	claimed, err := h.st.ClaimPendingReportBatch(ctx, "p", "lane-b", "tok", h.now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim lane-b: %v %v", claimed, err)
	}
	if _, err := h.st.DeferReportBatch(ctx, "p", "tok", h.now.Add(time.Hour), "backoff"); err != nil {
		t.Fatal(err)
	}
	h.report(h.session(domain.KindWorker, "lane-a", false), "msg-a")

	got := h.run()
	if !strings.Contains(got[orchA], "msg-a") {
		t.Fatalf("due lane-a was not delivered: %#v", h.delivery.submissions)
	}
	if got[orchB] != "" {
		t.Fatalf("lane-b's deferred batch was submitted early: %q", got[orchB])
	}
}

// failingDelivery makes every report batch unconfirmed; notices go through.
type failingDelivery struct{ *coordinatorDelivery }

func (d failingDelivery) Submit(ctx context.Context, target domain.SessionID, message, key string) error {
	if strings.HasPrefix(key, "report-notice:") {
		return d.coordinatorDelivery.Submit(ctx, target, message, key)
	}
	return ErrDeliveryUnconfirmed
}

// Unconfirmed batches in two lanes each get one notice, however often they retry.
func TestLaneUnconfirmedBatchesDoNotClobberEachOther(t *testing.T) {
	h := newLaneHarness(t)
	h.session(domain.KindOrchestrator, "lane-a", false)
	h.session(domain.KindOrchestrator, "lane-b", false)
	h.report(h.session(domain.KindWorker, "lane-a", false), "msg-a")
	h.report(h.session(domain.KindWorker, "lane-b", false), "msg-b")
	rec := &coordinatorDelivery{}
	clock := h.now
	h.coord = NewCoordinator(CoordinatorDeps{Store: h.st, Delivery: failingDelivery{rec}, Now: func() time.Time { return clock }})
	for i := 0; i < 5; i++ {
		clock = clock.Add(2 * time.Hour)
		_, _ = h.coord.RunDue(context.Background())
	}
	notices := 0
	for _, s := range rec.submissions {
		if strings.HasPrefix(s.key, "report-notice:") {
			notices++
		}
	}
	if notices != 2 {
		t.Fatalf("notices = %d, want exactly one per lane (2); submissions=%#v", notices, rec.submissions)
	}
}
