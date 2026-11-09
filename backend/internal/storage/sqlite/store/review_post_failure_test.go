package store_test

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B590: a pass whose review never reached the PR reads back as a posting failure.
func TestReviewRunReadsBackPostFailure(t *testing.T) {
	s, ctx, rec, now := sessionFixture(t)
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerClaudeCode, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	for _, tc := range []struct {
		id, sha, githubReviewID string
		want                    bool
	}{{"run-1", "sha1", "", true}, {"run-2", "sha2", "12345", false}} {
		run := domain.ReviewRun{
			ID: tc.id, ReviewID: "rev-1", SessionID: rec.ID, Harness: domain.ReviewerClaudeCode,
			PRURL: "https://example/pr/1", TargetSHA: tc.sha, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone, CreatedAt: now,
		}
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatalf("insert run: %v", err)
		}
		if _, err := s.UpdateReviewRunResult(ctx, tc.id, domain.ReviewRunComplete, domain.VerdictChangesRequested, "findings", tc.githubReviewID, true); err != nil {
			t.Fatalf("update run: %v", err)
		}
		got, ok, err := s.GetReviewRun(ctx, tc.id)
		if err != nil || !ok {
			t.Fatalf("get run: ok=%v err=%v", ok, err)
		}
		if got.PostFailed != tc.want {
			t.Fatalf("%s: PostFailed = %v, want %v", tc.id, got.PostFailed, tc.want)
		}
	}
}
