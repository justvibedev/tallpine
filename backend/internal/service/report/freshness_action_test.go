package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type actionStore struct {
	*coordinatorStore
	prs  []domain.PullRequest
	runs []domain.ReviewRun
}

func (s *actionStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return s.prs, nil
}

func (s *actionStore) ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	return s.runs, nil
}

func TestReportSupersededWhenOrchestratorActedOnItsPR(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	const pr = "https://github.com/o/r/pull/9"
	later := now.Add(time.Minute)
	cases := []struct {
		name  string
		setup func(*actionStore, *domain.SessionRecord)
		stale bool
	}{
		{"merged", func(s *actionStore, _ *domain.SessionRecord) {
			s.prs = []domain.PullRequest{{URL: pr, Merged: true, StateChangedAt: later}}
		}, true},
		{"review triggered after", func(s *actionStore, _ *domain.SessionRecord) {
			s.runs = []domain.ReviewRun{{PRURL: pr, CreatedAt: later, TriggerSource: domain.ReviewTriggerManual}}
		}, true},
		{"unrelated message to worker", func(_ *actionStore, w *domain.SessionRecord) { w.Metadata.LatestInteractionAt = later }, false},
		{"auto review run", func(s *actionStore, _ *domain.SessionRecord) {
			s.runs = []domain.ReviewRun{{PRURL: pr, CreatedAt: later, TriggerSource: domain.ReviewTriggerAuto}}
		}, false},
		{"nothing happened", func(s *actionStore, _ *domain.SessionRecord) {
			s.runs = []domain.ReviewRun{{PRURL: pr, CreatedAt: now.Add(-time.Hour)}}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := domain.SessionRecord{ID: "worker", ProjectID: "p", Kind: domain.KindWorker}
			st := &actionStore{coordinatorStore: newCoordinatorStore(now)}
			tc.setup(st, &worker)
			st.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}, worker}
			withPR := func(id, note string, state domain.ReportState, kind domain.ReportOutputKind, at time.Duration) domain.ReportRecord {
				r := reportAt(id, "worker", now.Add(at), now, note)
				r.State = state
				r.Outputs = []domain.ReportOutput{{Kind: kind, Reference: pr}}
				return r
			}
			st.add(
				withPR("verdict", "review verdict body", "", domain.ReportOutputPRReviewed, 0),
				withPR("ask", "may I merge?", domain.ReportNeedsInput, domain.ReportOutputPRCreated, time.Second),
				withPR("stale", "approved, waiting for merge", domain.ReportCheckpoint, domain.ReportOutputPRCreated, 3*time.Second),
			)
			delivery := &coordinatorDelivery{}
			c := NewCoordinator(CoordinatorDeps{Store: st, Delivery: delivery, Now: func() time.Time { return now.Add(time.Hour) }})
			if _, err := c.RunDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(delivery.submissions) != 1 {
				t.Fatalf("submissions = %v", delivery.submissions)
			}
			got := delivery.submissions[0].message
			if delivered := strings.Contains(got, "approved, waiting for merge"); delivered == tc.stale {
				t.Errorf("stale report delivered = %v, want %v: %s", delivered, !tc.stale, got)
			}
			if strings.Contains(got, "[outputs]") {
				t.Errorf("an acted-on report still delivered an outputs block: %s", got)
			}
			for _, want := range []string{"review verdict body", "may I merge?"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %s", want, got)
				}
			}
		})
	}
}

func TestStuckReportIsNeverSupersededByAction(t *testing.T) {
	now := time.Now()
	r := reportAt("s", "worker", now, now, "blocked on CI infra")
	r.State = domain.ReportStuck
	r.Outputs = []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: "u"}}
	a := workerActions{prs: []domain.PullRequest{{URL: "u", Merged: true}}}
	if supersededByAction(r, a) {
		t.Fatal("a stuck request was superseded; it is actionable like needs_input")
	}
	r.State = domain.ReportCheckpoint
	if !supersededByAction(r, a) {
		t.Fatal("control: the same report as a checkpoint should be superseded")
	}
}

func TestLoneActedOnReportDeliversNothing(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	const pr = "https://github.com/o/r/pull/9"
	st := &actionStore{coordinatorStore: newCoordinatorStore(now), prs: []domain.PullRequest{{URL: pr, Merged: true, StateChangedAt: now.Add(time.Minute)}}}
	st.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}}
	r := reportAt("stale", "worker", now, now, "approved, waiting for merge")
	r.Outputs = []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: pr}}
	st.add(r)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: st, Delivery: delivery, Now: func() time.Time { return now.Add(time.Hour) }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(delivery.submissions) != 0 || len(delivery.interrupts) != 0 {
		t.Fatalf("a lone stale report woke the orchestrator: %v", delivery.submissions)
	}
}
