package report

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// actedOnMarker tags a Superseded entry whose report is dropped whole,
// outputs included: the orchestrator already acted on that PR, so even an
// outputs-only line would be a stale wakeup (B362).
const actedOnMarker = "acted-on"

func (b PreparedBatch) actedOn(id string) bool {
	v, ok := b.Superseded[id]
	return ok && v.Note == actedOnMarker
}

// workerActions is what the orchestrator side did about a worker's PRs: the
// PRs as tracked now and the review passes triggered. A store without a capability contributes nothing.
type workerActions struct {
	prs  []domain.PullRequest
	runs []domain.ReviewRun
}

func (c *Coordinator) workerActions(ctx context.Context, worker domain.SessionID) workerActions {
	var a workerActions
	if lister, ok := c.store.(prLister); ok {
		if prs, err := lister.ListPRsBySession(ctx, worker); err == nil {
			a.prs = prs
		}
	}
	if lister, ok := c.store.(interface {
		ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error)
	}); ok {
		if runs, err := lister.ListReviewRunsBySession(ctx, worker); err == nil {
			a.runs = runs
		}
	}
	return a
}

// supersededByAction reports whether a queued status or FYI about a PR is
// stale because the orchestrator side has since acted on it (B362): the PR
// merged or closed, or a review pass was triggered by a person or agent
// (not the daemon's auto-review) after the report was written ("approved, waiting for merge"
// arriving after the merge). needs_input and stuck requests, done results and review
// verdicts (pr_reviewed outputs) are never replaced. Such a report is dropped
// whole, outputs included (see actedOn).
func supersededByAction(r domain.ReportRecord, a workerActions) bool {
	if r.State == domain.ReportNeedsInput || r.State == domain.ReportDone || r.State == domain.ReportStuck {
		return false
	}
	var refs []string
	for _, o := range r.Outputs {
		switch o.Kind {
		case domain.ReportOutputPRReviewed:
			return false
		case domain.ReportOutputPRCreated:
			refs = append(refs, o.Reference)
		}
	}
	if len(refs) == 0 {
		return false
	}
	for _, ref := range refs {
		for _, pr := range a.prs {
			if (pr.URL == ref || pr.HTMLURL == ref) && (pr.Merged || pr.Closed) &&
				(pr.StateChangedAt.IsZero() || r.CreatedAt.Before(pr.StateChangedAt)) {
				return true
			}
		}
		for _, run := range a.runs {
			if run.PRURL == ref && run.TriggerSource != domain.ReviewTriggerAuto && run.CreatedAt.After(r.CreatedAt) {
				return true
			}
		}
	}
	return false
}
