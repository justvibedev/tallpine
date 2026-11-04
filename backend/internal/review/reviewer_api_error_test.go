package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B455: run 45f2629d died on "API Error: 500" at its first prompt; the idle
// sweep failed it only after the full idle grace as a plain failure.
func TestIdleReviewerOnAPIErrorFailsEarlyAndNeverReviewed(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	apiLine := "API Error: 500 Internal server error. This is a server-side issue, usually temporary"
	for _, tc := range []struct {
		name, screen string
		idleFor      time.Duration
		wantBody     string // empty: the pass keeps running
	}{
		{"api error past the short grace", "Review the requested pull request\n⎿  " + apiLine + "\n> ", reviewerAPIErrorGrace, ReviewerAPIErrorPrefix + apiLine},
		{"api error inside the short grace", "⎿  " + apiLine + "\n> ", reviewerAPIErrorGrace - time.Second, ""},
		{"overloaded", "API Error: 529 Overloaded\n> ", reviewerAPIErrorGrace, ReviewerAPIErrorPrefix + "API Error: 529 Overloaded"},
		// A reviewer quoting the error in a diff, or an old line far above the prompt, is not a failed turn.
		{"quoted in a diff", "+\t\"API Error: 500 Internal server error\"\n> ", reviewerAPIErrorGrace, ""},
		{"old line above the prompt", apiLine + "\n1\n2\n3\n4\n5\n6\n7\n8\n> ", reviewerAPIErrorGrace, ""},
		{"idle without an error keeps the full grace", "all done\n> ", reviewerAPIErrorGrace, ""},
		{"plain idle past the full grace", "all done\n> ", reviewerIdleGrace, reviewerIdleWithoutSubmission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{
				review: &domain.Review{
					ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
					ReviewerActivityState: domain.ActivityIdle, InterfaceMode: domain.ReviewerInterfaceTUI,
					UpdatedAt: now.Add(-tc.idleFor),
				},
				runs: []domain.ReviewRun{{
					ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
					PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
					CreatedAt: now.Add(-tc.idleFor - time.Minute),
				}},
			}
			fake := &fakeLauncher{alive: true}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: fake, screen: tc.screen})
			got, err := eng.List(context.Background(), "mer-1")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got.Runs) != 1 {
				t.Fatalf("runs = %+v", got.Runs)
			}
			run := got.Runs[0]
			if tc.wantBody == "" {
				if run.Status != domain.ReviewRunRunning {
					t.Fatalf("run = %+v, want it still running", run)
				}
				return
			}
			if run.Status != domain.ReviewRunFailed || run.Body != tc.wantBody {
				t.Fatalf("run = %+v, want failed with %q", run, tc.wantBody)
			}
			if wantNever := tc.wantBody != reviewerIdleWithoutSubmission; NeverReviewed(run.Body) != wantNever || !IsUnsubmittedReviewerFailure(run.Body) {
				t.Fatalf("NeverReviewed(%q) = %v, want %v", run.Body, NeverReviewed(run.Body), wantNever)
			}
		})
	}
}
