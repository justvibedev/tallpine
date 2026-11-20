package backlog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// PlanCommentInput is an owner comment on a whole submitted plan (B831).
type PlanCommentInput struct {
	Text string
	// Approve holds an approval on the comment: it lands once the planner
	// accepts the comment, never before.
	Approve bool
	// OwnerMessage backs an orchestrator's held approval, as for Approve.
	OwnerMessage string
}

// Comment records an owner comment on the submitted plan and sends it to the
// planner, which accepts it, revises the plan, or asks follow-up questions.
func (p *PlanService) Comment(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in PlanCommentInput) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if !decider(actor) {
		return domain.BacklogPlan{}, notDecider()
	}
	if err := domain.ValidatePlanComment(in.Text); err != nil {
		return domain.BacklogPlan{}, invalidPlan(err)
	}
	item, err := p.bl.resolve(ctx, projectID, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	open, ok, err := p.store.OpenBacklogPlanForItem(ctx, item.ID)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !ok {
		return domain.BacklogPlan{}, apierr.Conflict("PLAN_NOT_OPEN", fmt.Sprintf("%s has no open plan", item.Ref()), nil)
	}
	c := domain.PlanComment{ID: p.newID(), Text: strings.TrimSpace(in.Text), By: "user", Approve: in.Approve, At: p.now().UTC()}
	if actor.Kind == domain.BacklogOriginOrchestrator {
		c.By = "orchestrator:" + actor.SessionID
		if in.Approve {
			// The held approval is the owner's: check it now, as Approve would.
			if c.OwnerMessageID, err = p.verifyOwnerMessage(ctx, open, in.OwnerMessage); err != nil {
				return domain.BacklogPlan{}, err
			}
		}
	}
	inPlace := p.isInPlace(ctx, open)
	plan, err := p.store.UpdateBacklogPlan(ctx, open.ID, func(pl *domain.BacklogPlan) error {
		if pl.Status != domain.PlanAwaitingApproval {
			return apierr.Conflict("PLAN_WRONG_STATE", fmt.Sprintf("the plan is %s: only a submitted plan takes comments", pl.Status), nil)
		}
		pl.Comments = append(pl.Comments, c)
		pl.UpdatedAt = c.At
		return nil
	})
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	p.changed(ctx, plan)
	msg := commentMessage(item.Ref(), plan.PendingComments(), in.Approve)
	if _, err := p.deliverToAuthor(ctx, plan, item, inPlace, msg, commentRecord(plan), msg); err != nil {
		// No planner will answer: the comment must not hold the approval forever.
		p.resolveComments(ctx, plan.ID, domain.PlanCommentUnanswered)
		return domain.BacklogPlan{}, err
	}
	what := "commented on"
	if in.Approve {
		what = "approved, on the condition of a comment,"
	}
	p.notifyOrchestrator(ctx, plan, fmt.Sprintf("The owner %s the plan for %s. Its planner decides: accept the comment, revise the plan, or ask follow-up questions. Do not spawn the implementer until the plan is approved.", what, item.Ref()))
	return plan, nil
}

// commentRecord is the answers so far, for a planner started to answer a comment.
func commentRecord(plan domain.BacklogPlan) string {
	if plan.Answers == nil {
		return ""
	}
	return answerRecord(plan)
}

// commentMessage is what the planner reads: the pending comments and its three ways out.
func commentMessage(ref string, pending []domain.PlanComment, approve bool) string {
	var b strings.Builder
	b.WriteString("The owner commented on your submitted plan (read it with `tallpine plan show " + ref + "`). The comment applies to the whole plan:\n")
	for _, c := range pending {
		fmt.Fprintf(&b, "\n> %s\n", strings.ReplaceAll(c.Text, "\n", "\n> "))
	}
	b.WriteString("\nDecide, then do exactly one of:\n")
	fmt.Fprintf(&b, "- Accept it as is (the plan stands): `tallpine plan accept-comment %s`.\n", ref)
	fmt.Fprintf(&b, "- Revise the plan now: `tallpine plan submit %s` with the revised plan; the owner approves it again.\n", ref)
	fmt.Fprintf(&b, "- Ask follow-up questions on the card: `tallpine plan questions %s`; you write the plan again after the answers.\n", ref)
	if approve {
		b.WriteString("\nThe owner approved on the condition of this comment: accepting it approves the plan and lets the implementer start, so accept only when the plan already satisfies it.")
	}
	return b.String()
}

// AcceptComments is the planner taking the owner's pending comments as they
// are. A held approval lands now; else the plan waits for the owner again.
func (p *PlanService) AcceptComments(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (domain.BacklogPlan, error) {
	open, err := p.plannerPlan(ctx, projectID, actor, ref)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	held, isHeld := open.HeldApproval()
	plan, err := p.store.UpdateBacklogPlan(ctx, open.ID, func(pl *domain.BacklogPlan) error {
		if pl.Status != domain.PlanAwaitingApproval || len(pl.PendingComments()) == 0 {
			return apierr.Conflict("PLAN_NO_COMMENT", "the plan has no owner comment waiting for an answer", nil)
		}
		pl.ResolveComments(domain.PlanCommentAccepted, p.now().UTC())
		pl.UpdatedAt = p.now().UTC()
		return nil
	})
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	p.changed(ctx, plan)
	ir := p.itemRef(ctx, plan)
	if isHeld {
		approved, aerr := p.approve(ctx, projectID, heldActor(held), ref, PlanApproveInput{held: &held}, held.By)
		if aerr == nil {
			return approved, nil
		}
		// The comment stays accepted, but the owner's approval did not land: it
		// must not vanish silently. The plan waits for the owner, who approves again.
		p.log.Warn("held plan approval failed after the comment was accepted", "plan", plan.ID, "err", aerr)
		p.alertOwner(ctx, plan)
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("The planner for %s accepted the owner's comment, but the approval held on it could not land (%v). The plan waits for the owner to approve again; do not approve without the owner's word.", ir, aerr))
		return plan, nil
	}
	p.alertOwner(ctx, plan)
	p.notifyOrchestrator(ctx, plan, fmt.Sprintf("The planner for %s accepted the owner's comment; the plan stands and waits for the owner to approve. Do not approve without the owner's word.", ir))
	return plan, nil
}

// heldActor is who approved with a held comment, for the children it makes.
func heldActor(c domain.PlanComment) Actor {
	if sid, ok := strings.CutPrefix(c.By, "orchestrator:"); ok {
		return Actor{Kind: domain.BacklogOriginOrchestrator, SessionID: sid}
	}
	return Actor{Kind: domain.BacklogOriginHuman}
}

// resolveComments stamps the plan's pending comments with outcome.
func (p *PlanService) resolveComments(ctx context.Context, planID string, outcome domain.PlanCommentOutcome) {
	plan, err := p.store.UpdateBacklogPlan(ctx, planID, func(pl *domain.BacklogPlan) error {
		if pl.ResolveComments(outcome, p.now().UTC()) == 0 {
			return errNoChange
		}
		pl.UpdatedAt = p.now().UTC()
		return nil
	})
	if err == nil {
		p.changed(ctx, plan)
	}
}

// replanInstructions is the brief line for a replan's comment: the new run's instructions.
func replanInstructions(comment string) string {
	if strings.TrimSpace(comment) == "" {
		return ""
	}
	return "The owner asked for this replan with these instructions; follow them, they apply to the whole plan:\n> " + strings.ReplaceAll(strings.TrimSpace(comment), "\n", "\n> ")
}

// recordReplanComment stores a replan's comment on the new plan.
func (p *PlanService) recordReplanComment(ctx context.Context, plan domain.BacklogPlan, actor Actor, comment string) domain.BacklogPlan {
	if strings.TrimSpace(comment) == "" {
		return plan
	}
	c := domain.PlanComment{ID: p.newID(), Text: strings.TrimSpace(comment), By: "user", Replan: true, Outcome: domain.PlanCommentInstructions, At: p.now().UTC()}
	if actor.Kind == domain.BacklogOriginOrchestrator {
		c.By = "orchestrator:" + actor.SessionID
	}
	c.ResolvedAt = c.At
	updated, err := p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		pl.Comments = append(pl.Comments, c)
		return nil
	})
	if err != nil {
		p.log.Warn("store replan comment", "plan", plan.ID, "error", err)
		return plan
	}
	p.changed(ctx, updated)
	return updated
}

// supersedeComments marks a superseded plan's pending comments unanswered.
func supersedeComments(at time.Time) func(*domain.BacklogPlan) error {
	return func(pl *domain.BacklogPlan) error {
		pl.ResolveComments(domain.PlanCommentUnanswered, at)
		return nil
	}
}

// commentOutcomeLabel is how the implementer reads what became of a comment.
func commentOutcomeLabel(c domain.PlanComment) string {
	switch c.Outcome {
	case domain.PlanCommentAccepted:
		return "accepted by the planner; the plan stands with it"
	case domain.PlanCommentRevised:
		return "the plan was revised for it"
	case domain.PlanCommentQuestions:
		return "answered through follow-up questions"
	case domain.PlanCommentInstructions:
		return "the owner's instructions for this plan"
	case domain.PlanCommentUnanswered:
		return "not answered by the planner"
	}
	return "waiting for the planner"
}

// writePlanComments renders the owner's comments for a brief.
func writePlanComments(b *strings.Builder, plan domain.BacklogPlan) {
	if len(plan.Comments) == 0 {
		return
	}
	b.WriteString("\nOwner comments on the plan (they apply to the whole plan):\n")
	for _, c := range plan.Comments {
		fmt.Fprintf(b, "- %s (%s)\n", strings.ReplaceAll(c.Text, "\n", " "), commentOutcomeLabel(c))
	}
}

// approvalBlocked refuses an approval while an owner comment waits for the
// planner: the planner must answer it first (B831). A held approval landing
// after its comment was accepted is not blocked.
func approvalBlocked(pl domain.BacklogPlan, in PlanApproveInput) error {
	if in.held == nil && len(pl.PendingComments()) > 0 {
		return apierr.Conflict("PLAN_COMMENT_PENDING", "the planner is still answering the owner's comment: approve once it has accepted the comment or revised the plan", nil)
	}
	return nil
}
