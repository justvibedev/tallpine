package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B405: a held quiet report in a lane with no orchestrator of its own resolves
// to the default orchestrator, so it must ride the due immediate report of the
// un-laned lane instead of arriving after its hold, out of order.
func TestLaneHeldQuietReportRidesImmediateReportOfSharedTarget(t *testing.T) {
	h := newLaneHarness(t)
	orch := h.session(domain.KindOrchestrator, "", false)
	quietWorker := h.session(domain.KindWorker, "lane-x", false)
	if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: quietWorker, Message: "quiet-turn-ended", Quiet: true}); err != nil {
		t.Fatal(err)
	}
	h.report(h.session(domain.KindWorker, "", false), "immediate-done")

	got := h.run()
	if len(h.delivery.submissions) != 1 {
		t.Fatalf("want one submission, got %#v", h.delivery.submissions)
	}
	if m := got[orch]; !strings.Contains(m, "immediate-done") || !strings.Contains(m, "quiet-turn-ended") {
		t.Fatalf("batch missing a report: %q", m)
	}
}

// A lane with its own live orchestrator is never swept into another target.
func TestLaneHeldQuietReportOfOwnOrchestratorLaneIsNotSwept(t *testing.T) {
	h := newLaneHarness(t)
	orch := h.session(domain.KindOrchestrator, "", false)
	h.session(domain.KindOrchestrator, "lane-x", false)
	if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: h.session(domain.KindWorker, "lane-x", false), Message: "quiet-x", Quiet: true}); err != nil {
		t.Fatal(err)
	}
	h.report(h.session(domain.KindWorker, "", false), "immediate")
	if got := h.run(); strings.Contains(got[orch], "quiet-x") {
		t.Fatalf("swept across distinct targets: %q", got[orch])
	}
}

// A merged batch must stay whole across lanes on retry: after a busy deferral
// the next submission carries both reports (one idempotency key, one report set).
func TestLaneSweptBatchStaysWholeOnRetry(t *testing.T) {
	h := newLaneHarness(t)
	orch := h.session(domain.KindOrchestrator, "", false)
	if _, err := h.svc.Create(context.Background(), CreateInput{SessionID: h.session(domain.KindWorker, "lane-x", false), Message: "quiet-turn-ended", Quiet: true}); err != nil {
		t.Fatal(err)
	}
	h.report(h.session(domain.KindWorker, "", false), "immediate-done")
	busy := &busyOnceDelivery{coordinatorDelivery: h.delivery}
	h.coord = NewCoordinator(CoordinatorDeps{Store: h.st, Delivery: busy, Now: func() time.Time { return h.now.Add(time.Minute) }})
	_, _ = h.coord.RunDue(context.Background())
	h.coord = NewCoordinator(CoordinatorDeps{Store: h.st, Delivery: busy, Now: func() time.Time { return h.now.Add(time.Hour) }})
	_, _ = h.coord.RunDue(context.Background())
	if len(h.delivery.submissions) != 1 {
		t.Fatalf("want exactly one accepted submission, got %#v", h.delivery.submissions)
	}
	if m := h.delivery.submissions[0].message; h.delivery.submissions[0].target != orch || !strings.Contains(m, "immediate-done") || !strings.Contains(m, "quiet-turn-ended") {
		t.Fatalf("retry split the batch: %q", m)
	}
}

type busyOnceDelivery struct {
	*coordinatorDelivery
	done bool
}

func (d *busyOnceDelivery) Submit(ctx context.Context, target domain.SessionID, message, key string) error {
	if !d.done {
		d.done = true
		return ErrOrchestratorBusy
	}
	return d.coordinatorDelivery.Submit(ctx, target, message, key)
}
