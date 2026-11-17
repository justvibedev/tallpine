package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestMachineLoadSettingsDefaultAndRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	row, err := s.GetAppSettings(ctx)
	if err != nil || row.MachineMaxReviewers != 3 || row.MachineLoadPercent != 100 {
		t.Fatalf("fresh row = %d/%d, %v; want 3/100", row.MachineMaxReviewers, row.MachineLoadPercent, err)
	}
	if err := s.SetMachineLoad(ctx, 0, 250, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if row, _ := s.GetAppSettings(ctx); row.MachineMaxReviewers != 0 || row.MachineLoadPercent != 250 {
		t.Fatalf("after set = %d/%d", row.MachineMaxReviewers, row.MachineLoadPercent)
	}
	if err := s.SetMachineLoad(ctx, -1, 100, time.Now().UTC()); err == nil {
		t.Fatal("the column accepted a negative cap")
	}
}

// Two passes of one reviewer count once; a finished pass and another reviewer's
// running pass count as they should.
func TestCountRunningReviewersCountsDistinctReviewers(t *testing.T) {
	s, ctx, rec, now := sessionFixture(t)
	for _, id := range []string{"rev-a", "rev-b", "rev-c"} {
		if err := s.UpsertReview(ctx, domain.Review{ID: id, SessionID: rec.ID, ProjectID: rec.ProjectID, Harness: domain.ReviewerHarness("h-" + id), CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	runs := []domain.ReviewRun{
		{ID: "r1", ReviewID: "rev-a", PRURL: "https://example/pr/1", TargetSHA: "s1", Status: domain.ReviewRunRunning},
		{ID: "r2", ReviewID: "rev-a", PRURL: "https://example/pr/2", TargetSHA: "s2", Status: domain.ReviewRunRunning},
		{ID: "r3", ReviewID: "rev-b", PRURL: "https://example/pr/3", TargetSHA: "s3", Status: domain.ReviewRunRunning},
		{ID: "r4", ReviewID: "rev-c", PRURL: "https://example/pr/4", TargetSHA: "s4", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
	}
	for _, run := range runs {
		run.SessionID, run.CreatedAt = rec.ID, now
		run.Harness = domain.ReviewerHarness("h-" + run.ReviewID)
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatalf("insert %s: %v", run.ID, err)
		}
	}
	if n, err := s.CountRunningReviewers(ctx); err != nil || n != 2 {
		t.Fatalf("running reviewers = %d, %v; want 2", n, err)
	}
}
