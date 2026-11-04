package review

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestPlanStatuses(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name string
		pr   domain.PullRequest
		runs []domain.ReviewRun
		want StateStatus
	}{
		{name: "open needs review", pr: planPR("pr1", 1, "sha1"), want: ReviewStateNeedsReview},
		{name: "draft needs review", pr: withDraft(planPR("pr1", 1, "sha1")), want: ReviewStateNeedsReview},
		{name: "merged ineligible", pr: withMerged(planPR("pr1", 1, "sha1")), want: ReviewStateIneligible},
		{name: "closed ineligible", pr: withClosed(planPR("pr1", 1, "sha1")), want: ReviewStateIneligible},
		{name: "approved current sha up to date", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		}, want: ReviewStateUpToDate},
		{name: "changes requested current sha", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: now},
		}, want: ReviewStateChangesRequested},
		{name: "running current sha", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, CreatedAt: now},
		}, want: ReviewStateRunning},
		{name: "different sha needs review", pr: planPR("pr1", 1, "sha2"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		}, want: ReviewStateNeedsReview},
		{name: "failed current sha retryable", pr: planPR("pr1", 1, "sha1"), runs: []domain.ReviewRun{
			{ID: "run-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunFailed, CreatedAt: now},
		}, want: ReviewStateNeedsReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan([]domain.PullRequest{tt.pr}, tt.runs)
			if len(got) != 1 {
				t.Fatalf("review states = %d, want 1", len(got))
			}
			if got[0].Status != tt.want {
				t.Fatalf("status = %s, want %s; item=%+v", got[0].Status, tt.want, got[0])
			}
		})
	}
}

func TestPlanPreservesPreviousVerdictWithoutChangingCurrentHeadState(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name       string
		currentRun *domain.ReviewRun
		wantStatus StateStatus
	}{
		{name: "new head needs review", wantStatus: ReviewStateNeedsReview},
		{
			name: "new head running",
			currentRun: &domain.ReviewRun{
				ID: "run-current", PRURL: "pr1", TargetSHA: "sha2", Status: domain.ReviewRunRunning, CreatedAt: now.Add(time.Second),
			},
			wantStatus: ReviewStateRunning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := []domain.ReviewRun{{
				ID: "run-previous", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunDelivered,
				Verdict: domain.VerdictChangesRequested, CreatedAt: now,
			}}
			if tt.currentRun != nil {
				runs = append(runs, *tt.currentRun)
			}

			got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, runs)
			if got[0].Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", got[0].Status, tt.wantStatus)
			}
			if got[0].PreviousRun == nil || got[0].PreviousRun.ID != "run-previous" {
				t.Fatalf("previous run = %+v, want run-previous", got[0].PreviousRun)
			}
			if tt.currentRun == nil {
				if got[0].LatestRun != nil {
					t.Fatalf("latest run = %+v, want nil for unreviewed current head", got[0].LatestRun)
				}
			} else if got[0].LatestRun == nil || got[0].LatestRun.ID != tt.currentRun.ID {
				t.Fatalf("latest run = %+v, want current-head run %q", got[0].LatestRun, tt.currentRun.ID)
			}
		})
	}
}

func TestPlanPreviousRunChoosesNewestQualifyingDifferentHead(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha-current")}, []domain.ReviewRun{
		{ID: "older", PRURL: "pr1", TargetSHA: "sha-old", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now},
		{ID: "newest-other", PRURL: "pr1", TargetSHA: "sha-newer", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictChangesRequested, CreatedAt: now.Add(time.Second)},
		{ID: "failed-other", PRURL: "pr1", TargetSHA: "sha-failed", Status: domain.ReviewRunFailed, Verdict: domain.VerdictApproved, CreatedAt: now.Add(2 * time.Second)},
		{ID: "current", PRURL: "pr1", TargetSHA: "sha-current", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: now.Add(3 * time.Second)},
	})

	if got[0].PreviousRun == nil || got[0].PreviousRun.ID != "newest-other" {
		t.Fatalf("previous run = %+v, want newest qualifying different-head run", got[0].PreviousRun)
	}
	if got[0].LatestRun == nil || got[0].LatestRun.ID != "current" || got[0].Status != ReviewStateUpToDate {
		t.Fatalf("current-head state = %+v, want current approved run", got[0])
	}
}

func planPR(url string, n int, sha string) domain.PullRequest {
	return domain.PullRequest{URL: url, Number: n, HeadSHA: sha}
}

func withDraft(pr domain.PullRequest) domain.PullRequest {
	pr.Draft = true
	return pr
}

func withMerged(pr domain.PullRequest) domain.PullRequest {
	pr.Merged = true
	return pr
}

func withClosed(pr domain.PullRequest) domain.PullRequest {
	pr.Closed = true
	return pr
}

func TestPlanShowsEveryCurrentHeadRunAndLetsChangesRequestedWin(t *testing.T) {
	t0 := time.Unix(100, 0).UTC()
	codexCR := domain.ReviewRun{ID: "codex", PRURL: "pr1", TargetSHA: "sha2", Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: t0}
	opusOK := domain.ReviewRun{ID: "opus", PRURL: "pr1", TargetSHA: "sha2", Harness: domain.ReviewerClaudeCode, Model: "opus", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: t0.Add(time.Minute)}
	oldHead := domain.ReviewRun{ID: "old", PRURL: "pr1", TargetSHA: "sha1", Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: t0.Add(-time.Hour)}

	got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, []domain.ReviewRun{codexCR, oldHead, opusOK})[0]
	if got.Status != ReviewStateChangesRequested {
		t.Fatalf("status = %s, want changes_requested: an approval must not hide another reviewer's request", got.Status)
	}
	if got.LatestRun == nil || got.LatestRun.ID != "opus" {
		t.Fatalf("latestRun = %+v, want the newest run kept for compatibility", got.LatestRun)
	}
	if len(got.CurrentHeadRuns) != 2 || got.CurrentHeadRuns[0].ID != "opus" || got.CurrentHeadRuns[1].ID != "codex" {
		t.Fatalf("currentHeadRuns = %+v, want both current-head runs newest first", got.CurrentHeadRuns)
	}

	// The same reviewer approving on a later pass withdraws its request.
	codexOK := domain.ReviewRun{ID: "codex-2", PRURL: "pr1", TargetSHA: "sha2", Harness: domain.ReviewerCodex, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, CreatedAt: t0.Add(2 * time.Minute)}
	if got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, []domain.ReviewRun{codexCR, opusOK, codexOK})[0]; got.Status != ReviewStateUpToDate {
		t.Fatalf("status = %s, want up_to_date once codex approved", got.Status)
	}
	// A newest pass that failed still reads needs_review, as before.
	codexFailed := domain.ReviewRun{ID: "codex-3", PRURL: "pr1", TargetSHA: "sha2", Harness: domain.ReviewerCodex, Status: domain.ReviewRunFailed, CreatedAt: t0.Add(3 * time.Minute)}
	if got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, []domain.ReviewRun{codexCR, opusOK, codexFailed})[0]; got.Status != ReviewStateNeedsReview {
		t.Fatalf("status = %s, want needs_review (newest pass failed)", got.Status)
	}
	// A reviewer still running is shown over every finished verdict.
	running := domain.ReviewRun{ID: "kiro", PRURL: "pr1", TargetSHA: "sha2", Harness: domain.ReviewerKiro, Status: domain.ReviewRunRunning, CreatedAt: t0.Add(-time.Minute)}
	got = Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, []domain.ReviewRun{codexCR, opusOK, running})[0]
	if got.Status != ReviewStateRunning || len(got.CurrentHeadRuns) != 3 {
		t.Fatalf("status = %s runs = %d, want running with all three runs", got.Status, len(got.CurrentHeadRuns))
	}
}

func TestPlanShowsRunningPassOnOlderHead(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	runs := []domain.ReviewRun{
		{ID: "old", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, CreatedAt: now},
		{ID: "done", PRURL: "pr1", TargetSHA: "sha0", Status: domain.ReviewRunComplete, CreatedAt: now},
	}
	got := Plan([]domain.PullRequest{planPR("pr1", 1, "sha2")}, runs)[0]
	if got.Status != ReviewStateNeedsReview {
		t.Fatalf("status = %s", got.Status)
	}
	if got.OlderHeadRun == nil || got.OlderHeadRun.ID != "old" {
		t.Fatalf("OlderHeadRun = %+v, want run old", got.OlderHeadRun)
	}
	if cur := Plan([]domain.PullRequest{planPR("pr1", 1, "sha1")}, runs)[0]; cur.OlderHeadRun != nil {
		t.Fatalf("running pass on the current head must not be reported as older: %+v", cur.OlderHeadRun)
	}
}
