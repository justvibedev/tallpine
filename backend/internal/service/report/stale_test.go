package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type staleStore struct {
	*coordinatorStore
	prs map[domain.SessionID][]domain.PullRequest
}

func (s *staleStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	return s.prs[id], nil
}

func (s *staleStore) DropClaimedReport(_ context.Context, id, token string, at time.Time, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.reports {
		r := &s.reports[i]
		if r.ID == id && r.DeliveryState == domain.ReportClaimed && r.ClaimToken == token {
			r.DeliveryState, r.ClaimToken, r.AcknowledgedAt, r.LastError = domain.ReportAcknowledged, domain.ReportStaleClaimToken, at, reason
			return true, nil
		}
	}
	return false, nil
}

func runStale(t *testing.T, worker domain.SessionRecord, prs []domain.PullRequest, rep domain.ReportRecord) (*staleStore, *coordinatorDelivery) {
	t.Helper()
	now := time.Date(2026, 10, 30, 8, 11, 30, 0, time.UTC)
	base := newCoordinatorStore(now)
	base.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}, worker}
	store := &staleStore{coordinatorStore: base, prs: map[domain.SessionID][]domain.PullRequest{worker.ID: prs}}
	store.add(rep)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, delivery
}

func staleReport(state domain.ReportState, outputs ...domain.ReportOutput) domain.ReportRecord {
	now := time.Date(2026, 10, 30, 8, 11, 20, 0, time.UTC)
	r := reportAt("r1", "w", now, now, "PR #368 approved by review; awaiting merge")
	r.State = state
	r.Outputs = outputs
	return r
}

func TestStaleApprovedThenMergedReportIsDroppedWithReason(t *testing.T) {
	worker := domain.SessionRecord{ID: "w", ProjectID: "p", IsTerminated: true}
	merged := []domain.PullRequest{{URL: "https://x/pull/368", Merged: true}}
	store, delivery := runStale(t, worker, merged, staleReport(domain.ReportNeedsInput))
	if len(delivery.submissions) != 0 {
		t.Fatalf("stale report was delivered: %#v", delivery.submissions)
	}
	got := store.reports[0]
	if got.DeliveryState != domain.ReportAcknowledged || !strings.Contains(got.UndeliveredReason(), "merged or closed") {
		t.Fatalf("row = %#v, want acknowledged with stale reason", got)
	}
}

func TestStaleReportNamingMergedPRIsDroppedEvenForLiveWorker(t *testing.T) {
	worker := domain.SessionRecord{ID: "w", ProjectID: "p"}
	out := domain.ReportOutput{Kind: domain.ReportOutputPRReviewed, Reference: "https://x/pull/368"}
	closedAfter := []domain.PullRequest{{URL: "https://x/pull/368", Closed: true, StateChangedAt: time.Date(2026, 10, 30, 8, 11, 25, 0, time.UTC)}}
	_, delivery := runStale(t, worker, closedAfter, staleReport(domain.ReportNeedsInput, out))
	if len(delivery.submissions) != 0 {
		t.Fatalf("report predating the close was delivered: %#v", delivery.submissions)
	}
	// A live worker's question written after the close ("reopen or abandon?") is kept.
	closedBefore := []domain.PullRequest{{URL: "https://x/pull/368", Closed: true, StateChangedAt: time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)}}
	_, delivery = runStale(t, worker, closedBefore, staleReport(domain.ReportNeedsInput, out))
	if len(delivery.submissions) != 1 {
		t.Fatalf("report written after the close submissions = %d, want 1", len(delivery.submissions))
	}
}

func TestStaleTerminatedWorkersBlockedReportIsStillDelivered(t *testing.T) {
	worker := domain.SessionRecord{ID: "w", ProjectID: "p", IsTerminated: true}
	merged := []domain.PullRequest{{URL: "https://x/pull/368", Merged: true}}
	_, delivery := runStale(t, worker, merged, staleReport(domain.ReportStuck))
	if len(delivery.submissions) != 1 {
		t.Fatalf("blocked report submissions = %d, want 1", len(delivery.submissions))
	}
}

func TestStaleNewHeadReportIsStillDelivered(t *testing.T) {
	worker := domain.SessionRecord{ID: "w", ProjectID: "p", IsTerminated: true}
	open := []domain.PullRequest{{URL: "https://x/pull/368"}}
	_, delivery := runStale(t, worker, open, staleReport(domain.ReportNeedsInput, domain.ReportOutput{Kind: domain.ReportOutputPRCreated, Reference: "https://x/pull/368"}))
	if len(delivery.submissions) != 1 {
		t.Fatalf("new-head report submissions = %d, want 1", len(delivery.submissions))
	}
	// A terminated worker's report that names no PR while one is still open is kept too.
	_, delivery = runStale(t, worker, open, staleReport(domain.ReportNeedsInput))
	if len(delivery.submissions) != 1 {
		t.Fatalf("open-PR report submissions = %d, want 1", len(delivery.submissions))
	}
}

type quietStore struct {
	*staleStore
	quiet      map[string]bool
	superseded map[string]bool
}

func (q *quietStore) QuietReportState(_ context.Context, id string) (bool, bool, error) {
	return q.quiet[id], q.superseded[id], nil
}

func runQuiet(t *testing.T, terminated, quiet, superseded bool, note string) *coordinatorDelivery {
	t.Helper()
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	base := newCoordinatorStore(now)
	base.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}, {ID: "w", ProjectID: "p", IsTerminated: terminated}}
	store := &quietStore{staleStore: &staleStore{coordinatorStore: base}, quiet: map[string]bool{"r1": quiet}, superseded: map[string]bool{"r1": superseded}}
	r := reportAt("r1", "w", now.Add(-10*time.Minute), now, "turn ended")
	if note != "" {
		r.Message, r.Synthesized = note, true
	}
	store.add(r)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	return delivery
}

func TestQuietReportFromTerminatedSenderIsDiscarded(t *testing.T) {
	if n := len(runQuiet(t, true, true, false, "").submissions); n != 0 {
		t.Fatalf("submissions = %d, want 0", n)
	}
}

func TestQuietReportSupersededBySameSessionIsDiscarded(t *testing.T) {
	if n := len(runQuiet(t, false, true, true, "").submissions); n != 0 {
		t.Fatalf("submissions = %d, want 0", n)
	}
}

func TestNonQuietReportIsKeptDespiteTerminationAndNewerReport(t *testing.T) {
	if n := len(runQuiet(t, true, false, true, "").submissions); n != 1 {
		t.Fatalf("submissions = %d, want 1", n)
	}
}

func TestQuietReportFromLiveLatestSenderIsDelivered(t *testing.T) {
	if n := len(runQuiet(t, false, true, false, "").submissions); n != 1 {
		t.Fatalf("submissions = %d, want 1", n)
	}
}

func TestQuietRestartNoticeSurvivesCarrierTerminationAndNewerReport(t *testing.T) {
	if n := len(runQuiet(t, true, true, true, "[auto] restart: app restarted").submissions); n != 1 {
		t.Fatalf("submissions = %d, want 1", n)
	}
}

type listingStore struct {
	*staleStore
	all []domain.ReportRecord
}

func (l *listingStore) ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error) {
	return l.all, nil
}

func headReport(id string, at time.Time, state domain.ReportState, pr string) domain.ReportRecord {
	r := reportAt(id, "w", at, at, "PR ready")
	r.State = state
	r.Outputs = []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: pr}}
	return r
}

func runHeads(t *testing.T, first, second domain.ReportRecord) int {
	t.Helper()
	now := second.CreatedAt.Add(time.Minute)
	base := newCoordinatorStore(now)
	base.sessions = []domain.SessionRecord{{ID: "orch", ProjectID: "p", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat}, {ID: "w", ProjectID: "p"}}
	store := &listingStore{staleStore: &staleStore{coordinatorStore: base}, all: []domain.ReportRecord{first, second}}
	store.add(first)
	delivery := &coordinatorDelivery{}
	c := NewCoordinator(CoordinatorDeps{Store: store, Delivery: delivery, Now: func() time.Time { return now }, NewToken: func() string { return "claim" }})
	if _, err := c.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	return len(delivery.submissions)
}

func TestNeedsInputSupersededByNewerHeadReportIsDropped(t *testing.T) {
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	a := headReport("r1", now, domain.ReportNeedsInput, "https://x/pull/1")
	b := headReport("r2", now.Add(time.Minute), domain.ReportNeedsInput, "https://x/pull/1")
	if n := runHeads(t, a, b); n != 0 {
		t.Fatalf("submissions = %d, want 0", n)
	}
}

func TestNeedsInputForOtherPRIsKept(t *testing.T) {
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	a := headReport("r1", now, domain.ReportNeedsInput, "https://x/pull/1")
	b := headReport("r2", now.Add(time.Minute), domain.ReportNeedsInput, "https://x/pull/2")
	if n := runHeads(t, a, b); n != 1 {
		t.Fatalf("submissions = %d, want 1", n)
	}
}
