package backlog

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SessionEnded handles a terminated session linked to an item. It reports
// whether the session was a planner. A planner that ended with its plan still
// drafting or writing marks the plan failed and tells the orchestrator once.
func (p *PlanService) SessionEnded(ctx context.Context, sessionID domain.SessionID) bool {
	planner := p.isPlanner(ctx, sessionID)
	if !p.Enabled() {
		return planner
	}
	// An in-place author is an ordinary worker: its end fails a plan it never
	// submitted, but the caller still handles it as a worker (false).
	who := "planner"
	if !planner {
		who = "implementer"
	}
	plan, ok, err := p.store.BacklogPlanByPlanner(ctx, sessionID)
	if !planner {
		if err != nil || !ok || plan.PlannerSessionID != sessionID {
			return false
		}
		p.failEndedAuthor(ctx, plan, sessionID, who)
		return false
	}
	if err != nil || !ok || plan.PlannerSessionID != sessionID {
		return true
	}
	p.failEndedAuthor(ctx, plan, sessionID, who)
	return true
}

// failEndedAuthor marks a plan failed when its author ended while drafting or
// writing, and tells the orchestrator once.
func (p *PlanService) failEndedAuthor(ctx context.Context, plan domain.BacklogPlan, sessionID domain.SessionID, who string) {
	if plan.Status != domain.PlanDrafting && plan.Status != domain.PlanWriting {
		return
	}
	failed, err := p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		if (pl.Status != domain.PlanDrafting && pl.Status != domain.PlanWriting) || pl.PlannerSessionID != sessionID {
			return errNoChange
		}
		pl.Status, pl.Failure, pl.UpdatedAt = domain.PlanFailed, "the "+who+" ended without submitting", p.now().UTC()
		return nil
	})
	if err != nil {
		return
	}
	p.changed(ctx, failed)
	ir := p.itemRef(ctx, failed)
	again := fmt.Sprintf("`tallpine plan start %s`", ir)
	if who == "implementer" {
		again = fmt.Sprintf("`tallpine spawn --backlog %s --plan in-place`", ir) // plan start would switch to a separate planner
	}
	p.notifyOrchestrator(ctx, failed, fmt.Sprintf("The %s for %s ended without submitting a plan. Run %s to plan again.", who, ir, again))
}

// isPlanner reports whether the session is a planner: role planner on its record.
func (p *PlanService) isPlanner(ctx context.Context, id domain.SessionID) bool {
	if p == nil || id == "" {
		return false
	}
	rec, ok, err := p.store.GetSession(ctx, id)
	return err == nil && ok && rec.Role == domain.SessionRolePlanner
}
