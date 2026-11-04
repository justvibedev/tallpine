package waitescalation

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeStore struct {
	sessions []domain.SessionRecord
	reports  map[domain.SessionID]domain.ReportRecord
}

func (f *fakeStore) ListLiveSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}

func (f *fakeStore) LatestNeedsInputReports(context.Context, []domain.SessionID) (map[domain.SessionID]domain.ReportRecord, error) {
	return f.reports, nil
}

type fakeNotifier struct {
	intents     []ports.NotificationIntent
	resolutions []ports.NotificationResolution
}

func (f *fakeNotifier) Notify(_ context.Context, in ports.NotificationIntent) error {
	f.intents = append(f.intents, in)
	return nil
}

func (f *fakeNotifier) Resolve(_ context.Context, res ports.NotificationResolution) error {
	f.resolutions = append(f.resolutions, res)
	return nil
}

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func fixture(orchestrator bool) (*fakeStore, *fakeNotifier) {
	st := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "w1", ProjectID: "p", Kind: domain.KindWorker, DisplayName: "worker", Activity: domain.Activity{State: domain.ActivityIdle}}},
		reports:  map[domain.SessionID]domain.ReportRecord{"w1": {ID: "rpt_1", SessionID: "w1", State: domain.ReportNeedsInput, Note: "need a call", CreatedAt: t0}},
	}
	if orchestrator {
		st.sessions = append(st.sessions, domain.SessionRecord{ID: "o1", ProjectID: "p", Kind: domain.KindOrchestrator})
	}
	return st, &fakeNotifier{}
}

func reconcileAt(t *testing.T, r *Reconciler) {
	t.Helper()
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileHoldsAnOrchestratorBoundWaitWithoutNotifying(t *testing.T) {
	st, n := fixture(true)
	now := t0.Add(domain.OrchestratorWaitTimeout - time.Second)
	reconcileAt(t, New(st, n, func() time.Time { return now }, nil))
	if len(n.intents) != 0 {
		t.Fatalf("intents = %d, want none inside the timeout", len(n.intents))
	}
}

func TestReconcileEscalatesAnIdleWorkerWithADesktopNotificationAfterTheTimeout(t *testing.T) {
	st, n := fixture(true)
	now := t0.Add(domain.OrchestratorWaitTimeout)
	reconcileAt(t, New(st, n, func() time.Time { return now }, nil))
	if len(n.intents) != 1 {
		t.Fatalf("intents = %d, want one escalation ping", len(n.intents))
	}
	in := n.intents[0]
	if in.Type != domain.NotificationNeedsInput || in.SessionID != "w1" || in.SourceKey != domain.NeedsInputEscalationSourceKeyPrefix+"rpt_1" {
		t.Fatalf("intent = %+v", in)
	}
}

// After a restart a fresh reconciler finds the same due escalation; the shared
// source key is what makes the notification store accept it only once.
func TestReconcileAfterRestartResendsTheSameSourceKeyForADueEscalation(t *testing.T) {
	st, n := fixture(true)
	now := t0.Add(time.Hour)
	reconcileAt(t, New(st, n, func() time.Time { return now }, nil))
	reconcileAt(t, New(st, n, func() time.Time { return now }, nil))
	if len(n.intents) != 2 || n.intents[0].SourceKey != n.intents[1].SourceKey || n.intents[0].SourceKey == "" {
		t.Fatalf("intents = %+v, want the same source key both times", n.intents)
	}
}

func TestReconcileEscalatesAStandaloneWorkerImmediately(t *testing.T) {
	st, n := fixture(false)
	reconcileAt(t, New(st, n, func() time.Time { return t0.Add(time.Second) }, nil))
	if len(n.intents) != 1 {
		t.Fatalf("intents = %d, want an immediate ping without a live orchestrator", len(n.intents))
	}
}

func TestReconcileDoesNotEscalateARequestAnsweredByALaterDone(t *testing.T) {
	st, n := fixture(true)
	st.reports = map[domain.SessionID]domain.ReportRecord{} // the store query drops an answered request
	reconcileAt(t, New(st, n, func() time.Time { return t0.Add(time.Hour) }, nil))
	if len(n.intents) != 0 {
		t.Fatalf("intents = %d, want none once the worker reported done", len(n.intents))
	}
}

func TestReconcileLeavesActivityDrivenWaitsToLifecycle(t *testing.T) {
	st, n := fixture(true)
	st.sessions[0].Activity.State = domain.ActivityWaitingInput
	reconcileAt(t, New(st, n, func() time.Time { return t0.Add(time.Hour) }, nil))
	if len(n.intents) != 0 {
		t.Fatalf("intents = %d, want none: lifecycle already pinged for the agent-level wait", len(n.intents))
	}
}

func TestReconcileResolvesAnEscalationOnceTheOrchestratorActs(t *testing.T) {
	st, n := fixture(true)
	now := t0.Add(time.Hour)
	r := New(st, n, func() time.Time { return now }, nil)
	reconcileAt(t, r)
	st.sessions[0].Metadata.LatestInteractionAt = now.Add(time.Second)
	reconcileAt(t, r)
	if len(n.resolutions) == 0 || n.resolutions[len(n.resolutions)-1].SessionID != "w1" {
		t.Fatalf("resolutions = %+v, want the escalation resolved", n.resolutions)
	}
}

func TestReconcileResolvesAnEscalationWhenTheWorkerIsKilled(t *testing.T) {
	st, n := fixture(true)
	now := t0.Add(time.Hour)
	r := New(st, n, func() time.Time { return now }, nil)
	reconcileAt(t, r)
	st.sessions = st.sessions[1:] // the worker is terminated and leaves the live list
	st.reports = map[domain.SessionID]domain.ReportRecord{}
	reconcileAt(t, r)
	if len(n.resolutions) != 1 || n.resolutions[0].SessionID != "w1" {
		t.Fatalf("resolutions = %+v, want the killed worker's escalation resolved", n.resolutions)
	}
}
