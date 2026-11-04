package review

import (
	stdctx "context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// PlanSource returns the approved plan's decisions table and review focus for
// the backlog item a worker session builds (or that item's parent).
type PlanSource interface {
	ApprovedPlanForWorker(ctx stdctx.Context, id domain.SessionID) (decisions string, focus []string, ok bool, err error)
}

// planFor is best effort: a lookup failure means the reviewer gets no plan
// block, never a failed review.
func (e *Engine) planFor(ctx stdctx.Context, id domain.SessionID) (string, []string) {
	if e.plans == nil {
		return "", nil
	}
	decisions, focus, ok, err := e.plans.ApprovedPlanForWorker(ctx, id)
	if err != nil || !ok {
		return "", nil
	}
	return decisions, focus
}
