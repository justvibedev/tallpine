package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B831: owner comments on a plan survive a write and a read, with their outcome.
func TestBacklogPlanCommentsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	item := addBacklog(t, s, "p1", "planned", domain.BacklogOpen, "")
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	if _, _, err := s.CreateBacklogPlan(ctx, domain.BacklogPlan{
		ID: "plan-1", ProjectID: "p1", ItemID: item.ID, Status: domain.PlanAwaitingApproval, StartedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	want := []domain.PlanComment{
		{ID: "c1", Text: "use the shared parser", By: "user", Approve: true, At: at},
		{ID: "c2", Text: "drop PR 3", By: "orchestrator:o1", Outcome: domain.PlanCommentAccepted, At: at, ResolvedAt: at.Add(time.Minute)},
	}
	if _, err := s.UpdateBacklogPlan(ctx, "plan-1", func(pl *domain.BacklogPlan) error {
		pl.Comments = want
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.OpenBacklogPlanForItem(ctx, item.ID)
	if err != nil || !ok {
		t.Fatalf("read plan: ok %v err %v", ok, err)
	}
	if len(got.Comments) != 2 || got.Comments[0] != want[0] || !got.Comments[1].ResolvedAt.Equal(want[1].ResolvedAt) || got.Comments[1].Outcome != domain.PlanCommentAccepted {
		t.Fatalf("comments = %+v, want %+v", got.Comments, want)
	}
	if held, ok := got.HeldApproval(); !ok || held.ID != "c1" {
		t.Fatalf("held approval = %+v %v, want c1", held, ok)
	}
}
