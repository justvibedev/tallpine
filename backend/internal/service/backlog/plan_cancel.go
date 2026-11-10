package backlog

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// Cancel drops the item's plan that waits on the owner (open, or failed) and
// stops its planner. The item stays open, so it can be planned again. An
// approved plan is not cancelled: replan supersedes it.
func (p *PlanService) Cancel(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	// A failed plan can sit behind a newer open one (Plan again keeps it), and
	// would resurface once the open one is superseded: drop every waiting plan.
	var first domain.BacklogPlan
	var cancelled []domain.BacklogPlan
	for range 8 {
		cur, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID)
		if err != nil {
			return domain.BacklogPlan{}, err
		}
		if !ok || (!cur.Status.Open() && cur.Status != domain.PlanFailed) {
			break
		}
		plan, err := p.transition(ctx, cur.ID, domain.PlanSuperseded, func(pl *domain.BacklogPlan) error {
			if pl.Status == domain.PlanApproved { // an approval landed after the check above
				return apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no plan to cancel", item.Ref()), nil)
			}
			noteEnding(pl, planCancelledNote)
			return nil
		})
		if err != nil {
			if first.ID != "" {
				break
			}
			return domain.BacklogPlan{}, err
		}
		cancelled = append(cancelled, cur)
		if first.ID == "" {
			first = plan
		}
	}
	if first.ID == "" {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no plan to cancel", item.Ref()), nil)
	}
	if p.cancelObserver != nil {
		p.cancelObserver(ctx, first)
	}
	// Stop the authors last: the run item is parked by now, so a run pass that
	// the kill sets off cannot restart the plan in always mode.
	for _, cur := range cancelled {
		p.stopAuthor(ctx, cur, item)
	}
	return first, nil
}

// stopAuthor ends the cancelled plan's planner. An in-place author is the
// item's implementer: it is stopped while it has no PR, and told the plan is
// cancelled when it has one (it keeps its work).
func (p *PlanService) stopAuthor(ctx context.Context, plan domain.BacklogPlan, item domain.BacklogItem) {
	if !p.isInPlace(ctx, plan) {
		p.stopPlanner(ctx, plan)
		return
	}
	if plan.PlannerSessionID == "" {
		return
	}
	if p.authorHasPR(ctx, plan) != nil {
		msg := fmt.Sprintf("The plan for %s was cancelled by the owner. Stay read-only and wait; do not submit a plan.", item.Ref())
		if _, err := p.sessions.SendToPlanner(ctx, plan.PlannerSessionID, msg); err != nil {
			p.log.Warn("tell in-place author the plan was cancelled", "plan", plan.ID, "error", err)
		}
		return
	}
	if err := p.sessions.TerminatePlanner(ctx, plan.PlannerSessionID); err != nil {
		p.log.Warn("stop in-place author", "plan", plan.ID, "session", plan.PlannerSessionID, "error", err)
	}
}
