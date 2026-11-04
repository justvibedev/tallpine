package review

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChangesRequestedRounds counts the reviews on the PR that asked for changes
// since its latest approval; an approval ends the loop and resets the count.
// The auto-review coordinator and the engine's automatic planning both use it,
// so they agree on which PRs have used up their rounds.
func ChangesRequestedRounds(runs []domain.ReviewRun, prURL string) int {
	var approvedAt time.Time
	for _, r := range runs {
		if r.PRURL == prURL && r.Verdict == domain.VerdictApproved && r.CreatedAt.After(approvedAt) {
			approvedAt = r.CreatedAt
		}
	}
	n := 0
	for _, r := range runs {
		if r.PRURL == prURL && r.Verdict == domain.VerdictChangesRequested && r.CreatedAt.After(approvedAt) {
			n++
		}
	}
	return n
}

// excludeExhaustedPRs makes an automatic pass skip a PR that used up the
// project's AutoReviewMaxRounds, so it is not reviewed alongside another PR
// that is due. A lookup failure keeps the PR in the plan.
func (p *triggerPass) excludeExhaustedPRs(runs []domain.ReviewRun) {
	if p.e.projects == nil {
		return
	}
	proj, ok, err := p.e.projects.GetProject(p.ctx, string(p.worker.ProjectID))
	limit := proj.Config.AutoReviewMaxRounds
	if err != nil || !ok || limit <= 0 {
		return
	}
	for i := range p.reviews {
		if p.reviews[i].Status == ReviewStateNeedsReview && ChangesRequestedRounds(runs, p.reviews[i].PRURL) >= limit {
			p.reviews[i].Status = ReviewStateIneligible
		}
	}
}
