package review

import (
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// applyRiskReviewer swaps in the project's stronger reviewer (agent, model and
// effort) when a PR due for review changes a risky path. An explicit reviewer
// choice for the pass or the session wins, and the normal reviewer runs when the
// stronger agent is not installed or its model is denied by the project's
// policy. configured is the reviewer the project would use without the pick.
func (p *triggerPass) applyRiskReviewer(configured domain.ReviewerHarness) {
	e, ctx := p.e, p.ctx
	// The auto-review coordinator always names the project's own reviewer; that is
	// not a choice made for this pass.
	explicit := p.opts.Harness != "" && (p.source != domain.ReviewTriggerAuto || p.opts.Harness != configured)
	if explicit || p.hasConfigOverride || p.opts.Config.Model != "" ||
		p.worker.ReviewerHarness != "" || p.worker.ReviewerConfig.Model != "" {
		return
	}
	reviews := Plan(p.prs, p.runs)
	pick, ok := e.riskReviewerFor(ctx, p.worker, reviews, p.runs, p.dueForRiskPick)
	if !ok {
		return
	}
	if err := e.checkModelPolicy(ctx, p.worker.ProjectID, pick.config.Model); err != nil {
		slog.Warn("review risk: model denied by project policy; keeping configured reviewer", "session", p.worker.ID, "model", pick.config.Model, "err", err)
		return
	}
	if pick.harness != p.harness {
		if err := e.launcher.Preflight(ctx, p.worker.ID, pick.harness, p.worker.Metadata.WorkspacePath); err != nil {
			slog.Warn("review risk: stronger reviewer agent unavailable; keeping configured reviewer", "session", p.worker.ID, "harness", pick.harness, "err", err)
			return
		}
		// Planning counts one agent's runs, so a pass on another agent reviews
		// only the risky PRs; the rest stay with the normal reviewer.
		var risky []domain.PullRequest
		for _, pr := range p.prs {
			if pick.prURLs[pr.URL] {
				risky = append(risky, pr)
			}
		}
		p.prs = risky
	}
	p.harness, p.config, p.modelReason = pick.harness, pick.config, pick.reason
}

// dropReviewedHeads makes an automatic pass skip PRs whose head already has a
// review from any agent, or is being reviewed right now by an agent other than
// configured, so the normal reviewer does not repeat what the stronger one did
// or is doing.
func (p *triggerPass) dropReviewedHeads(configured domain.ReviewerHarness) {
	if p.source != domain.ReviewTriggerAuto {
		return
	}
	var keep []domain.PullRequest
	reviewed := map[string]bool{}
	for _, state := range Plan(p.prs, p.runs) {
		reviewed[state.PRURL] = state.Status == ReviewStateUpToDate || state.Status == ReviewStateChangesRequested ||
			(state.Status == ReviewStateRunning && state.LatestRun != nil && state.LatestRun.Harness != "" && state.LatestRun.Harness != configured)
	}
	for _, pr := range p.prs {
		if !reviewed[pr.URL] {
			keep = append(keep, pr)
		}
	}
	p.prs = keep
}

// dueForRiskPick reports whether a PR still needs a review from any agent, so a
// head that already has one cannot keep pulling the pass to the stronger agent.
func (p *triggerPass) dueForRiskPick(state PRReviewState) bool {
	switch {
	case state.Status == ReviewStateNeedsReview:
		return true
	case p.opts.Rerun:
		return state.Status != ReviewStateIneligible
	default:
		return p.source != domain.ReviewTriggerAuto && state.Status == ReviewStateChangesRequested
	}
}
