package session

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestListPRSummariesReflectsLatestTallpineReviewVerdictWhenProviderIsSilent(t *testing.T) {
	t.Parallel()
	url := "https://github.com/acme/repo/pull/9"
	t0 := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		provider domain.ReviewDecision
		runs     []domain.CurrentHeadReviewRun
		want     domain.ReviewDecision
	}{
		{"changes requested", domain.ReviewNone, []domain.CurrentHeadReviewRun{{PRURL: url, ID: "a", Verdict: domain.VerdictChangesRequested, Status: domain.ReviewRunComplete, CreatedAt: t0}}, domain.ReviewChangesRequest},
		{"latest approved wins", domain.ReviewNone, []domain.CurrentHeadReviewRun{
			{PRURL: url, ID: "a", Verdict: domain.VerdictChangesRequested, Status: domain.ReviewRunComplete, CreatedAt: t0},
			{PRURL: url, ID: "b", Verdict: domain.VerdictApproved, Status: domain.ReviewRunComplete, CreatedAt: t0.Add(time.Minute)},
		}, domain.ReviewApproved},
		{"running pass has no verdict", domain.ReviewNone, []domain.CurrentHeadReviewRun{{PRURL: url, ID: "a", Status: domain.ReviewRunRunning, CreatedAt: t0}}, domain.ReviewNone},
		{"provider decision wins", domain.ReviewApproved, []domain.CurrentHeadReviewRun{{PRURL: url, ID: "a", Verdict: domain.VerdictChangesRequested, Status: domain.ReviewRunComplete, CreatedAt: t0}}, domain.ReviewApproved},
		{"other PR ignored", domain.ReviewNone, []domain.CurrentHeadReviewRun{{PRURL: "https://github.com/acme/repo/pull/10", ID: "a", Verdict: domain.VerdictChangesRequested, Status: domain.ReviewRunComplete, CreatedAt: t0}}, domain.ReviewNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
			st.reviewRuns["mer-1"] = tc.runs
			stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{URL: url, SessionID: "mer-1", Number: 9, Review: tc.provider}}}
			got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
			if err != nil || len(got) != 1 {
				t.Fatalf("summaries = %+v err=%v", got, err)
			}
			if got[0].Review.Decision != tc.want {
				t.Fatalf("decision = %q, want %q", got[0].Review.Decision, tc.want)
			}
		})
	}
}
