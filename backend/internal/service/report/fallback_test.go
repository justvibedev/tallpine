package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type recordingNotifier struct{ got []ports.NotificationIntent }

func (n *recordingNotifier) Notify(_ context.Context, i ports.NotificationIntent) error {
	n.got = append(n.got, i)
	return nil
}

type fallbackHarness struct {
	t        *testing.T
	st       *sqlite.Store
	svc      *Service
	delivery *coordinatorDelivery
	notifier *recordingNotifier
	coord    *Coordinator
	now      time.Time
}

func newFallbackHarness(t *testing.T) *fallbackHarness {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	h := &fallbackHarness{t: t, st: st, delivery: &coordinatorDelivery{}, notifier: &recordingNotifier{}, now: time.Now().UTC().Truncate(time.Second)}
	for _, p := range []string{"p", "q"} {
		if err := st.UpsertProject(context.Background(), domain.ProjectRecord{ID: p, Path: "/tmp/" + p, RegisteredAt: h.now}); err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return h.now }
	h.svc = New(Deps{Store: st, Now: clock})
	h.coord = NewCoordinator(CoordinatorDeps{Store: st, Delivery: h.delivery, Now: clock, Notifier: h.notifier})
	return h
}

func (h *fallbackHarness) session(project domain.ProjectID, kind domain.SessionKind, parent domain.SessionID) domain.SessionID {
	h.t.Helper()
	h.now = h.now.Add(time.Second)
	rec, err := h.st.CreateSession(context.Background(), domain.SessionRecord{ProjectID: project, Kind: kind, Harness: domain.HarnessClaudeCode, CreatedAt: h.now})
	if err != nil {
		h.t.Fatal(err)
	}
	if parent != "" {
		if err := h.st.SetSessionLane(context.Background(), rec.ID, domain.SessionLane{ParentSessionID: parent}); err != nil {
			h.t.Fatal(err)
		}
	}
	return rec.ID
}

func (h *fallbackHarness) report(worker domain.SessionID, note string) {
	h.t.Helper()
	if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: worker, State: domain.ReportNeedsInput, Note: note, Immediate: true}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fallbackHarness) run() {
	h.t.Helper()
	if _, err := h.coord.RunDue(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func TestNoOrchestratorReportGoesToInboxAfterGrace(t *testing.T) {
	h := newFallbackHarness(t)
	w := h.session("p", domain.KindWorker, "")
	h.report(w, "pick a colour")
	h.run()
	if len(h.notifier.got) != 0 || len(h.delivery.submissions) != 0 {
		t.Fatalf("delivered inside the grace period: %+v", h.notifier.got)
	}
	h.now = h.now.Add(NoOrchestratorGrace + time.Second)
	h.coord.Wake()
	h.run()
	if len(h.notifier.got) != 1 || !strings.Contains(h.notifier.got[0].Body, "pick a colour") || !strings.Contains(h.notifier.got[0].Body, "tallpine://sessions/p/"+string(w)) {
		t.Fatalf("inbox notices = %+v", h.notifier.got)
	}
	h.coord.Wake()
	h.run()
	if len(h.notifier.got) != 1 {
		t.Fatalf("notified twice: %d", len(h.notifier.got))
	}
	// Acknowledged, not left claimed: a claimed batch would be released and
	// redelivered by restart recovery.
	recs, err := h.svc.ListProject(context.Background(), "p")
	if err != nil || len(recs) != 1 || recs[0].DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("reports = %+v, err %v", recs, err)
	}
}

func TestLateOrchestratorDoesNotRedeliverInboxReport(t *testing.T) {
	h := newFallbackHarness(t)
	h.report(h.session("p", domain.KindWorker, ""), "stuck")
	h.now = h.now.Add(NoOrchestratorGrace + time.Second)
	h.run()
	h.session("p", domain.KindOrchestrator, "")
	h.coord.Wake()
	h.run()
	if len(h.notifier.got) != 1 || len(h.delivery.submissions) != 0 {
		t.Fatalf("notices=%d submissions=%d", len(h.notifier.got), len(h.delivery.submissions))
	}
}

func TestLateOrchestratorWithinGraceGetsTheReport(t *testing.T) {
	h := newFallbackHarness(t)
	h.report(h.session("p", domain.KindWorker, ""), "stuck")
	h.run()
	orch := h.session("p", domain.KindOrchestrator, "")
	h.coord.Wake()
	h.run()
	if len(h.notifier.got) != 0 || len(h.delivery.submissions) != 1 || h.delivery.submissions[0].target != orch {
		t.Fatalf("notices=%d submissions=%+v", len(h.notifier.got), h.delivery.submissions)
	}
}

func TestOrchestratorPresentIgnoresFallback(t *testing.T) {
	h := newFallbackHarness(t)
	orch := h.session("p", domain.KindOrchestrator, "")
	h.report(h.session("p", domain.KindWorker, ""), "hello")
	h.now = h.now.Add(time.Hour)
	h.run()
	if len(h.notifier.got) != 0 || len(h.delivery.submissions) != 1 || h.delivery.submissions[0].target != orch {
		t.Fatalf("notices=%d submissions=%+v", len(h.notifier.got), h.delivery.submissions)
	}
}

func TestCrossProjectParentOrchestratorGetsReportImmediately(t *testing.T) {
	h := newFallbackHarness(t)
	parent := h.session("q", domain.KindOrchestrator, "")
	h.report(h.session("p", domain.KindWorker, parent), "need a call")
	h.run()
	if len(h.notifier.got) != 0 || len(h.delivery.submissions) != 1 || h.delivery.submissions[0].target != parent {
		t.Fatalf("notices=%d submissions=%+v", len(h.notifier.got), h.delivery.submissions)
	}
}

func TestRouteNotice(t *testing.T) {
	h := newFallbackHarness(t)
	ctx := context.Background()
	w := h.session("p", domain.KindWorker, "")
	if n := h.svc.RouteNotice(ctx, "p", w, ""); !strings.Contains(n, "Inbox") {
		t.Fatalf("notice = %q", n)
	}
	parent := h.session("q", domain.KindOrchestrator, "")
	w2 := h.session("p", domain.KindWorker, parent)
	if n := h.svc.RouteNotice(ctx, "p", w2, ""); !strings.Contains(n, string(parent)) {
		t.Fatalf("notice = %q", n)
	}
	h.session("p", domain.KindOrchestrator, "")
	if n := h.svc.RouteNotice(ctx, "p", w, ""); n != "" {
		t.Fatalf("notice = %q", n)
	}
}

func TestBusyParentOrchestratorDefersWithBusyReason(t *testing.T) {
	h := newFallbackHarness(t)
	parent := h.session("q", domain.KindOrchestrator, "")
	h.report(h.session("p", domain.KindWorker, parent), "need a call")
	h.delivery.submitErr = ErrOrchestratorBusy
	h.run()
	recs, err := h.svc.ListProject(context.Background(), "p")
	if err != nil || len(recs) != 1 {
		t.Fatalf("reports = %+v, err %v", recs, err)
	}
	if r := recs[0]; r.DeliveryState != domain.ReportPending || r.LastError != domain.ReportDeliveryBusyReason || !r.AvailableAt.Equal(h.now.Add(OrchestratorBusyRetry)) {
		t.Fatalf("report = state %s err %q available %v", r.DeliveryState, r.LastError, r.AvailableAt)
	}
}

func TestInboxBodyOmitsSupersededStatuses(t *testing.T) {
	h := newFallbackHarness(t)
	w := h.session("p", domain.KindWorker, "")
	checkpoint := func(note string) {
		if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: w, State: domain.ReportCheckpoint, Note: note, Immediate: true}); err != nil {
			t.Fatal(err)
		}
		h.now = h.now.Add(time.Second)
	}
	checkpoint("old question")
	h.report(w, "blocked on you")
	h.now = h.now.Add(DefaultQuietDelay + NoOrchestratorGrace)
	h.run()
	if len(h.notifier.got) != 1 {
		t.Fatalf("notices = %d", len(h.notifier.got))
	}
	body := h.notifier.got[0].Body
	if !strings.Contains(body, "blocked on you") || strings.Contains(body, "old question") {
		t.Fatalf("body = %q", body)
	}
}
