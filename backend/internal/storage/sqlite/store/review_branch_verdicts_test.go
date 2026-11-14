package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The branch-verdict batch returns only verdict-bearing local-branch passes, with
// the columns the land gate's rule reads, so the session list never loads review text.
func TestListBranchReviewVerdictsForSessions(t *testing.T) {
	s, ctx, rec, now := sessionFixture(t)
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerClaudeCode, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	insert := func(id, url, sha string, verdict domain.ReviewVerdict, at time.Time) {
		t.Helper()
		if err := s.InsertReviewRun(ctx, domain.ReviewRun{
			ID: id, ReviewID: "rev-1", SessionID: rec.ID, Harness: domain.ReviewerClaudeCode,
			PRURL: url, TargetSHA: sha, Status: domain.ReviewRunComplete, Verdict: verdict, Body: "long review text", CreatedAt: at,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("approved", "local:tp/s1", "sha1", domain.VerdictApproved, now)
	insert("pending", "local:tp/s1", "sha2", domain.VerdictNone, now.Add(time.Second))
	insert("pr", "https://example/pr/1", "sha3", domain.VerdictApproved, now.Add(2*time.Second))

	got, err := s.ListBranchReviewVerdictsForSessions(context.Background(), []domain.SessionID{rec.ID, "unknown"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	runs := got[rec.ID]
	if len(runs) != 1 || len(got) != 1 {
		t.Fatalf("got %+v, want one local verdict run for the session only", got)
	}
	r := runs[0]
	if r.PRURL != "local:tp/s1" || r.TargetSHA != "sha1" || r.Verdict != domain.VerdictApproved || !r.CreatedAt.Equal(now) || r.Body != "" {
		t.Fatalf("run = %+v", r)
	}
	if empty, err := s.ListBranchReviewVerdictsForSessions(context.Background(), nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty ids = %v, %v", empty, err)
	}
}
