package domain

import "testing"

func TestReviewRunNotPostedToPR(t *testing.T) {
	const pr = "https://github.com/o/r/pull/1"
	for name, tc := range map[string]struct {
		run  ReviewRun
		want bool
	}{
		"verdict with no review id":  {ReviewRun{PRURL: pr, Status: ReviewRunComplete, Verdict: VerdictChangesRequested}, true},
		"delivered with no id":       {ReviewRun{PRURL: pr, Status: ReviewRunDelivered, Verdict: VerdictApproved}, true},
		"posted":                     {ReviewRun{PRURL: pr, Status: ReviewRunComplete, Verdict: VerdictChangesRequested, GithubReviewID: "9"}, false},
		"still running":              {ReviewRun{PRURL: pr, Status: ReviewRunRunning}, false},
		"failed pass has no verdict": {ReviewRun{PRURL: pr, Status: ReviewRunFailed}, false},
		"local branch has no PR":     {ReviewRun{PRURL: LocalReviewTargetPrefix + "b", Status: ReviewRunComplete, Verdict: VerdictChangesRequested}, false},
	} {
		if got := tc.run.NotPostedToPR(); got != tc.want {
			t.Errorf("%s: NotPostedToPR() = %v, want %v", name, got, tc.want)
		}
	}
}
