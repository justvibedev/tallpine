package domain

import "strings"

// NewestBranchVerdictRun is the newest pass that recorded a verdict on the
// review key of a session branch (LocalReviewTargetPrefix + branch). It is
// the one rule for "what did review decide about this branch", shared by the
// land gate and the session read model so they cannot disagree. Nil means the
// branch has no verdict.
func NewestBranchVerdictRun(runs []ReviewRun, key string) *ReviewRun {
	var latest *ReviewRun
	for i := range runs {
		r := &runs[i]
		if r.PRURL != key || strings.TrimSpace(string(r.Verdict)) == "" {
			continue
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	return latest
}
