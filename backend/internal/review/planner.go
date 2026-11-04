package review

import (
	"sort"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// StateStatus is the per-PR review planning state.
type StateStatus = domain.PRReviewStatus

// Review states, re-exported from domain.
const (
	ReviewStateNeedsReview      = domain.PRReviewStateNeedsReview
	ReviewStateRunning          = domain.PRReviewStateRunning
	ReviewStateUpToDate         = domain.PRReviewStateUpToDate
	ReviewStateChangesRequested = domain.PRReviewStateChangesRequested
	ReviewStateIneligible       = domain.PRReviewStateIneligible
)

// PRReviewState is one PR-scoped review decision for a worker session.
type PRReviewState = domain.PRReviewState

// Plan computes per-PR review work from the currently observed PRs and existing
// review runs. It is pure so the trigger path and API list path share exactly
// the same eligibility/status rules.
func Plan(prs []domain.PullRequest, runs []domain.ReviewRun) []PRReviewState {
	latest := latestRunsByPRAndSHA(runs)
	reviews := make([]PRReviewState, 0, len(prs))
	for _, pr := range prs {
		review := PRReviewState{
			PRURL:     pr.URL,
			PRNumber:  pr.Number,
			Title:     pr.Title,
			TargetSHA: pr.HeadSHA,
			Status:    ReviewStateNeedsReview,
		}
		review.CurrentHeadRuns = runsForHead(runs, review.PRURL, review.TargetSHA)
		if run, ok := runningRunForOtherSHA(runs, review.PRURL, review.TargetSHA); ok {
			review.OlderHeadRun = &run
		}
		if run, ok := latestCompletedRunForOtherSHA(runs, review.PRURL, review.TargetSHA); ok {
			review.PreviousRun = &run
		}
		if pr.URL == "" || pr.HeadSHA == "" || pr.Merged || pr.Closed {
			review.Status = ReviewStateIneligible
			if run, ok := latest[review.PRURL+"\x00"+review.TargetSHA]; ok {
				review.LatestRun = &run
			}
			reviews = append(reviews, review)
			continue
		}
		if run, ok := latest[review.PRURL+"\x00"+review.TargetSHA]; ok {
			review.LatestRun = &run
			switch {
			case run.Status == domain.ReviewRunRunning:
				review.Status = ReviewStateRunning
			case run.Verdict == domain.VerdictApproved:
				review.Status = ReviewStateUpToDate
			case run.Verdict == domain.VerdictChangesRequested:
				review.Status = ReviewStateChangesRequested
			case run.Status == domain.ReviewRunFailed || run.Status == domain.ReviewRunCancelled:
				review.Status = ReviewStateNeedsReview
			default:
				review.Status = ReviewStateNeedsReview
			}
			// Reviewers can disagree on one head. An approval must not hide
			// another reviewer's standing request for changes.
			if review.Status == ReviewStateUpToDate && headChangesRequested(review.CurrentHeadRuns) {
				review.Status = ReviewStateChangesRequested
			}
		}
		reviews = append(reviews, review)
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if reviews[i].PRNumber != reviews[j].PRNumber {
			return reviews[i].PRNumber < reviews[j].PRNumber
		}
		return reviews[i].PRURL < reviews[j].PRURL
	})
	return reviews
}

// runsForHead returns every run recorded against one PR head, newest first.
func runsForHead(runs []domain.ReviewRun, prURL, targetSHA string) []domain.ReviewRun {
	out := []domain.ReviewRun{}
	if prURL == "" || targetSHA == "" {
		return out
	}
	for _, run := range runs {
		if run.PRURL == prURL && run.TargetSHA == targetSHA {
			out = append(out, run)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// headChangesRequested reports whether any reviewer's latest verdict on the
// head requests changes. A reviewer is a harness and model; a later failed or
// cancelled pass by the same reviewer does not withdraw its posted verdict.
func headChangesRequested(headRuns []domain.ReviewRun) bool {
	seen := make(map[string]bool)
	for _, run := range headRuns { // newest first
		if run.Verdict != domain.VerdictApproved && run.Verdict != domain.VerdictChangesRequested {
			continue
		}
		key := string(run.Harness) + "\x00" + run.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		if run.Verdict == domain.VerdictChangesRequested {
			return true
		}
	}
	return false
}

// runningRunForOtherSHA returns the newest pass still running on a head other
// than targetSHA.
func runningRunForOtherSHA(runs []domain.ReviewRun, prURL, targetSHA string) (domain.ReviewRun, bool) {
	var latest domain.ReviewRun
	found := false
	if prURL == "" || targetSHA == "" {
		return latest, false
	}
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA == "" || run.TargetSHA == targetSHA || run.Status != domain.ReviewRunRunning {
			continue
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) {
			latest, found = run, true
		}
	}
	return latest, found
}

func latestCompletedRunForOtherSHA(runs []domain.ReviewRun, prURL, targetSHA string) (domain.ReviewRun, bool) {
	if prURL == "" || targetSHA == "" {
		return domain.ReviewRun{}, false
	}
	var latest domain.ReviewRun
	found := false
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA == "" || run.TargetSHA == targetSHA {
			continue
		}
		if run.Status != domain.ReviewRunComplete && run.Status != domain.ReviewRunDelivered {
			continue
		}
		if run.Verdict != domain.VerdictApproved && run.Verdict != domain.VerdictChangesRequested {
			continue
		}
		if !found || run.CreatedAt.After(latest.CreatedAt) {
			latest = run
			found = true
		}
	}
	return latest, found
}

// latestRunsByPRAndSHA picks the authoritative run per PR head: the newest
// running pass while any reviewer is still working on that head, otherwise the
// newest pass. Several reviewers may review one head at once, so a reviewer that
// finished first must not hide one that is still running.
func latestRunsByPRAndSHA(runs []domain.ReviewRun) map[string]domain.ReviewRun {
	latest := make(map[string]domain.ReviewRun)
	for _, run := range runs {
		if run.PRURL == "" || run.TargetSHA == "" {
			continue
		}
		key := run.PRURL + "\x00" + run.TargetSHA
		existing, ok := latest[key]
		if !ok {
			latest[key] = run
			continue
		}
		runRunning := run.Status == domain.ReviewRunRunning
		existingRunning := existing.Status == domain.ReviewRunRunning
		if runRunning != existingRunning {
			if runRunning {
				latest[key] = run
			}
			continue
		}
		if run.CreatedAt.After(existing.CreatedAt) {
			latest[key] = run
		}
	}
	return latest
}
