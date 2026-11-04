package domain

import "tallpine.eu/tallpine/backend/pkg/contract"

// PRReviewStatus is the per-PR review planning state.
type PRReviewStatus = contract.TallpineReviewState

const (
	// PRReviewStateNeedsReview means an eligible PR has no current Tallpine approval or running pass.
	PRReviewStateNeedsReview = contract.TallpineReviewNeedsReview
	// PRReviewStateRunning means a review run is already active for the PR's current head.
	PRReviewStateRunning = contract.TallpineReviewRunning
	// PRReviewStateUpToDate means Tallpine approved the PR's current head.
	PRReviewStateUpToDate = contract.TallpineReviewUpToDate
	// PRReviewStateChangesRequested means Tallpine requested changes on the PR's current head.
	PRReviewStateChangesRequested = contract.TallpineReviewChangesRequested
	// PRReviewStateIneligible means the PR is closed, merged, or missing required facts.
	PRReviewStateIneligible = contract.TallpineReviewIneligible
)

// PRReviewState is one PR-scoped review decision for a worker session.
type PRReviewState struct {
	PRURL       string         `json:"prUrl"`
	PRNumber    int            `json:"prNumber"`
	Title       string         `json:"title"`
	TargetSHA   string         `json:"targetSha"`
	Status      PRReviewStatus `json:"status" enum:"needs_review,running,up_to_date,changes_requested,ineligible"`
	LatestRun   *ReviewRun     `json:"latestRun,omitempty"`
	PreviousRun *ReviewRun     `json:"previousRun,omitempty"`
	// CurrentHeadRuns is every pass recorded against TargetSHA, newest first.
	// Several reviewers can review one head; LatestRun shows only one of them.
	CurrentHeadRuns []ReviewRun `json:"currentHeadRuns,omitempty"`
	// OlderHeadRun is a pass still running on an earlier head than TargetSHA:
	// the worker pushed while it ran, so Status alone would hide it.
	OlderHeadRun *ReviewRun `json:"olderHeadRun,omitempty"`
}
