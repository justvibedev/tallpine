package review

import "tallpine.eu/tallpine/backend/internal/domain"

// retrySupersededSkipReason explains a retry dropped because the same reviewer
// already has a newer or running pass on the commit.
const retrySupersededSkipReason = "retry superseded by a newer pass on the same commit"

// skipStaleRetry drops an automatic retry of a pass that ended without a
// verdict when, by the time this pass holds the worker lock, the same reviewer
// already has another pass on that commit. The retry decision is made outside
// the lock, so a trigger that ran in between (for example an agent's --rerun)
// would otherwise get a duplicate pass that replaces its reviewer mid-review.
func (p *triggerPass) skipStaleRetry() (TriggerResult, bool, error) {
	retryOf := p.opts.RetryOfRunID
	if retryOf == "" {
		return TriggerResult{}, false, nil
	}
	var failed domain.ReviewRun
	found := false
	for _, run := range p.runs {
		if run.ID == retryOf {
			failed, found = run, true
			break
		}
	}
	if !found {
		return TriggerResult{SkipReason: retrySupersededSkipReason}, true, nil
	}
	if !retryHeadCurrent(p.prs, failed) || retryAlreadyCovered(p.runs, failed) {
		return TriggerResult{SkipReason: retrySupersededSkipReason}, true, nil
	}
	return TriggerResult{}, false, nil
}

// retryHeadCurrent reports whether the failed run's PR still points at the
// commit it was reviewing. A retry never reviews a different commit: a push
// seen while the retry waited on the lock leaves the new head to its own pass.
func retryHeadCurrent(prs []domain.PullRequest, failed domain.ReviewRun) bool {
	for _, pr := range prs {
		if (pr.URL == failed.PRURL || pr.HTMLURL == failed.PRURL) && pr.HeadSHA == failed.TargetSHA {
			return true
		}
	}
	return false
}

// retryAlreadyCovered reports whether the failed run's reviewer has a running
// pass, or a newer one that reached the commit, on the same PR head.
func retryAlreadyCovered(runs []domain.ReviewRun, failed domain.ReviewRun) bool {
	for _, run := range runs {
		if run.ID == failed.ID || run.PRURL != failed.PRURL || run.TargetSHA != failed.TargetSHA || run.Harness != failed.Harness {
			continue
		}
		if run.Status == domain.ReviewRunRunning {
			return true
		}
		if run.CreatedAt.After(failed.CreatedAt) && (run.Status != domain.ReviewRunFailed || !NeverReviewed(run.Body)) && !ReviewerGoneCancel(run) {
			return true
		}
	}
	return false
}
