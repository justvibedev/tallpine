package store_test

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TestReviewFocusRoundTrips guards B743: a pass's one-off focus and its author
// survive the review_run and review_trigger_queue rows, so a retry or a queued
// rerun reads back the focus it was requested with.
func TestReviewFocusRoundTrips(t *testing.T) {
	s, ctx, rec, now := sessionFixture(t)
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerClaudeCode, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	run := domain.ReviewRun{
		ID: "run-1", ReviewID: "rev-1", SessionID: rec.ID, Harness: domain.ReviewerClaudeCode,
		PRURL: "https://example/pr/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, CreatedAt: now,
		Focus: "check the migration", FocusAuthor: domain.ReviewFocusOrchestrator,
	}
	if err := s.InsertReviewRun(ctx, run); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	got, ok, err := s.GetReviewRun(ctx, "run-1")
	if err != nil || !ok {
		t.Fatalf("get run: ok=%v err=%v", ok, err)
	}
	if got.Focus != run.Focus || got.FocusAuthor != run.FocusAuthor {
		t.Fatalf("run focus = %q/%q, want %q/%q", got.Focus, got.FocusAuthor, run.Focus, run.FocusAuthor)
	}

	q := domain.QueuedReviewTrigger{
		SessionID: rec.ID, PRURL: run.PRURL, Harness: domain.ReviewerClaudeCode, CreatedAt: now,
		TriggerSource: domain.ReviewTriggerAgent, Focus: "look at the queue", FocusAuthor: domain.ReviewFocusWorker,
	}
	if err := s.EnqueueReviewTrigger(ctx, q); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	queued, err := s.ListQueuedReviewTriggers(ctx)
	if err != nil || len(queued) != 1 {
		t.Fatalf("list queued = %v, %v; want one", queued, err)
	}
	if queued[0].Focus != q.Focus || queued[0].FocusAuthor != q.FocusAuthor {
		t.Fatalf("queued focus = %q/%q, want %q/%q", queued[0].Focus, queued[0].FocusAuthor, q.Focus, q.FocusAuthor)
	}
}
