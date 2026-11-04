package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ApproveWaiting approves the project's plans already awaiting approval when
// its planAutoApprove setting is on (same eligibility as Submit: the setting and
// any managed lock). It runs after a project config write, so turning the
// setting on clears the queue (any later write retries plans still waiting). A plan that fails to approve stays waiting.
func (p *PlanService) ApproveWaiting(ctx context.Context, projectID domain.ProjectID) {
	if p.guard() != nil || !p.autoApproveOn(ctx, projectID) {
		return
	}
	plans, err := p.store.OpenBacklogPlansForProject(ctx, projectID)
	if err != nil {
		p.log.Warn("list plans for auto-approve", "project", projectID, "err", err)
		return
	}
	for _, plan := range plans {
		if plan.Status != domain.PlanAwaitingApproval {
			continue
		}
		ref := p.refOf(ctx, plan.ItemID)
		if _, err := p.approve(ctx, projectID, Actor{Kind: domain.BacklogOriginHuman}, ref, PlanApproveInput{}, AutoApprovedBy); err != nil {
			p.log.Warn("plan auto-approve failed; waiting for the owner", "plan", plan.ID, "err", err)
		}
	}
}
