package backlog

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// Plan arms of `tallpine spawn --backlog <ref> --plan <arm>` (B335).
const (
	PlanArmInPlace  = "in-place"
	PlanArmSeparate = "separate"
)

// ValidPlanArm reports whether arm is "", in-place or separate.
func ValidPlanArm(arm string) bool {
	return arm == "" || arm == PlanArmInPlace || arm == PlanArmSeparate
}

// inPlaceBrief is the standing brief of an implementer that plans its own item:
// read-only until a message says the plan is approved. The plan format is the
// planner's (domain.PlanFormatPrompt).
func inPlaceBrief(ref string) string {
	return "## Plan in place\n\n" +
		"This item needs a plan before it is built, and you write it: you are the implementer and the planner. " +
		"Until a message tells you the owner approved the plan, you are READ-ONLY: do not edit files in the repo, commit, push, open a pull request or request a review; use read-only commands only. " +
		"Everything you deliver goes through `tallpine plan` (`tallpine plan questions " + ref + "`, `tallpine plan submit " + ref + "`); a file you write is not delivered. " +
		"After you submit, stop and wait: the owner's answers or the approval arrive as a message. Building starts only when that message says the plan is approved; it carries the decisions and your PR entry.\n\n" +
		domain.PlanFormatPrompt +
		"Submit the plan once, then wait for approval. Your publishing permission above applies after approval, not before."
}

// inPlaceTask is the task text of an in-place implementer started again after
// its session ended: the item, the standing brief and the record so far.
func (p *PlanService) inPlaceTask(ctx context.Context, item domain.BacklogItem, record string) string {
	var b strings.Builder
	b.WriteString(item.TaskContext() + fmt.Sprintf("\n\nPublishing: %s", publishingBrief(p.bl.effectiveScope(ctx, item))) + "\n\n" + inPlaceBrief(item.Ref()))
	if strings.TrimSpace(record) != "" {
		b.WriteString("\n\nThe owner already answered; do not ask again. Questions and answers so far:\n" + record)
	}
	return b.String()
}

// InPlaceEnabled reports whether the project allows plan-in-place.
func (p *PlanService) InPlaceEnabled(ctx context.Context, projectID domain.ProjectID) bool {
	return p != nil && p.enabled && p.inPlace != nil && p.inPlace(ctx, projectID)
}

// ReserveInPlace records a drafting plan for an item whose implementer is about
// to start. The author is attached with AttachAuthor once its session exists.
// An item with an open plan is refused (PLAN_OPEN).
func (p *PlanService) ReserveInPlace(ctx context.Context, item domain.BacklogItem, actor Actor) (domain.BacklogPlan, error) {
	if err := p.guard(); err != nil {
		return domain.BacklogPlan{}, err
	}
	if cur, ok, err := p.store.CurrentBacklogPlanForItem(ctx, item.ID); err != nil {
		return domain.BacklogPlan{}, err
	} else if ok && cur.Status.Open() {
		return domain.BacklogPlan{}, planOpenErr(item.Ref())
	}
	now := p.now().UTC()
	commit, branch := "", ""
	if p.base != nil {
		var err error
		if commit, branch, err = p.base.Base(ctx, item.ProjectID); err != nil {
			p.log.Warn("plan base commit unavailable", "item", item.Ref(), "error", err)
		}
	}
	plan, created, err := p.store.CreateBacklogPlan(ctx, domain.BacklogPlan{
		ID: p.newID(), ProjectID: item.ProjectID, ItemID: item.ID, Status: domain.PlanDrafting,
		BaseCommit: commit, BaseBranch: branch, ItemUpdatedAt: item.UpdatedAt, StartedAt: now, UpdatedAt: now,
		NotifySessionID: notifyTarget(actor),
	})
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	if !created {
		return domain.BacklogPlan{}, planOpenErr(item.Ref())
	}
	p.changed(ctx, plan)
	return plan, nil
}

func planOpenErr(ref string) error {
	return apierr.Conflict("PLAN_OPEN", fmt.Sprintf("%s already has an open plan: answer or approve it, or run `tallpine plan replan %s`", ref, ref), nil)
}

// AttachAuthor records the implementer session as the plan's author.
func (p *PlanService) AttachAuthor(ctx context.Context, planID string, sid domain.SessionID) error {
	_, err := p.store.UpdateBacklogPlan(ctx, planID, func(pl *domain.BacklogPlan) error {
		pl.PlannerSessionID, pl.UpdatedAt = sid, p.now().UTC()
		return nil
	})
	return err
}

// ReleaseInPlace fails a reserved plan whose implementer did not start.
func (p *PlanService) ReleaseInPlace(ctx context.Context, planID, reason string) {
	p.failPlan(ctx, planID, reason)
}

// isInPlace reports whether the plan's author is an implementer: a recorded
// session whose role is not planner. A separate planner and a plan without an
// author are not in place.
func (p *PlanService) isInPlace(ctx context.Context, plan domain.BacklogPlan) bool {
	if plan.PlannerSessionID == "" {
		return false
	}
	rec, ok, err := p.store.GetSession(ctx, plan.PlannerSessionID)
	return err == nil && ok && rec.Role != domain.SessionRolePlanner
}

// authorHasPR refuses an in-place author that already has a pull request: it
// must stay read-only until approval.
func (p *PlanService) authorHasPR(ctx context.Context, plan domain.BacklogPlan) error {
	facts, err := p.bl.store.BacklogFactsFor(ctx, plan.ItemID)
	if err != nil {
		return err
	}
	if sessionHasPR(facts, string(plan.PlannerSessionID), "merged", "open", "draft", "closed") {
		return apierr.Conflict("PLAN_AUTHOR_HAS_PR", "This session has a pull request: it plans read-only until the plan is approved", nil)
	}
	return nil
}

// OpenPlanOf reports whether the session authors a plan that is not approved
// yet (review is refused meanwhile, PLAN_NOT_APPROVED).
func (p *PlanService) OpenPlanOf(ctx context.Context, sid domain.SessionID) (itemRef string, open bool) {
	if p == nil || !p.enabled || sid == "" {
		return "", false
	}
	plan, ok, err := p.store.BacklogPlanByPlanner(ctx, sid)
	if err != nil || !ok || plan.PlannerSessionID != sid || !plan.Status.Open() {
		return "", false
	}
	return p.itemRef(ctx, plan), true
}

// approveInPlace hands the approval to the waiting author: it builds PR 1 (of
// the split, if any) and the orchestrator is told not to spawn it again.
func (p *PlanService) approveInPlace(ctx context.Context, plan domain.BacklogPlan, item domain.BacklogItem, childIDs []string) {
	ref := item.Ref()
	n := len(plan.PRSplit)
	msg := "The owner approved the plan. Build it now; your publishing permission applies.\n\n" + planSpawnContext(plan, ref)
	var rest []string
	buildRef := ref
	switch {
	case len(childIDs) > 0:
		if err := p.bl.LinkSession(ctx, childIDs[0], plan.PlannerSessionID, "plan"); err != nil {
			p.log.Warn("link plan author to child 1", "plan", plan.ID, "error", err)
		}
		buildRef = p.refOf(ctx, childIDs[0])
		for _, id := range childIDs[1:] {
			rest = append(rest, p.refOf(ctx, id))
		}
		msg += fmt.Sprintf("\n\nYou build PR 1 of %d of this plan: %s (backlog item %s). When this PR fully meets that item's done-when, its title must contain `closes %s`; it must never name %s, which closes itself once all its children close.", n, plan.PRSplit[0].Title, buildRef, buildRef, ref)
	case n == 1:
		msg += fmt.Sprintf("\n\nYou build PR 1 of 1 of this plan: %s", plan.PRSplit[0].Title)
	}
	delivered, err := p.sessions.SendToPlanner(ctx, plan.PlannerSessionID, msg)
	if err != nil {
		p.log.Warn("send approval to plan author", "plan", plan.ID, "error", err)
	}
	switch {
	case !delivered && len(rest) > 0:
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is approved and split into %d PRs, but its implementer has ended. Spawn `tallpine spawn --backlog <ref>` for the first now and each next one once the one before it is done (each is blocked by the one before; a blocked spawn is refused): %s.", ref, n, strings.Join(append([]string{buildRef}, rest...), ", ")))
	case !delivered:
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is approved but its implementer has ended. Spawn one with `tallpine spawn --backlog %s`: it receives the decisions and its PR entry.", ref, buildRef))
	case len(rest) > 0:
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is approved and split into %d PRs. Its implementer builds PR 1 in place (%s). Spawn `tallpine spawn --backlog <ref>` for %s one at a time, each once the one before it is done (a blocked spawn is refused).", ref, n, buildRef, strings.Join(rest, ", ")))
	default:
		p.notifyOrchestrator(ctx, plan, fmt.Sprintf("Plan for %s is approved. Its implementer builds in place; do not spawn another.", ref))
	}
}

func (p *PlanService) refOf(ctx context.Context, itemID string) string {
	if it, ok, err := p.bl.store.GetBacklogItem(ctx, itemID); err == nil && ok {
		return it.Ref()
	}
	return itemID
}

// replanInPlace supersedes the plan and asks the same author to plan again; the
// implementer is never killed. An ended author is started again in place.
func (p *PlanService) replanInPlace(ctx context.Context, cur domain.BacklogPlan, item domain.BacklogItem, actor Actor) (domain.BacklogPlan, error) {
	if cur.Status.Open() {
		if _, err := p.transition(ctx, cur.ID, domain.PlanSuperseded, nil); err != nil {
			return domain.BacklogPlan{}, err
		}
	}
	plan, err := p.ReserveInPlace(ctx, item, actor)
	if err != nil {
		return domain.BacklogPlan{}, err
	}
	brief := strings.TrimSpace(p.bl.planChildrenBrief(ctx, item) + "\n\n" + p.priorBrief(ctx, item, cur))
	msg := fmt.Sprintf("The plan is being redone. Stay read-only, plan again and submit with `tallpine plan questions %s` / `tallpine plan submit %s`.", item.Ref(), item.Ref())
	if strings.TrimSpace(brief) != "" {
		msg += "\n\n" + brief
	}
	sid := cur.PlannerSessionID
	delivered := false
	// An author that already opened a PR is read-only-locked out of planning: a
	// fresh implementer plans instead and the old one stays alive, unattached.
	if p.authorHasPR(ctx, cur) == nil {
		var err error
		if delivered, err = p.sessions.SendToPlanner(ctx, sid, msg); err != nil {
			delivered = false
		}
	}
	if !delivered {
		var err error
		if sid, err = p.sessions.SpawnInPlace(ctx, PlannerSpawn{
			ProjectID: item.ProjectID, ItemID: item.ID, ItemRef: item.Ref(), PlanID: plan.ID,
			DisplayName: item.Ref(), LaneItemID: item.ParentID, StarterSessionID: plan.NotifySessionID, Prompt: p.inPlaceTask(ctx, item, "") + "\n\n" + brief,
		}); err != nil {
			p.failPlan(ctx, plan.ID, "implementer session did not start: "+err.Error())
			return domain.BacklogPlan{}, err
		}
		if err := p.bl.LinkSession(ctx, item.ID, sid, "plan"); err != nil {
			p.log.Warn("link plan author to item", "item", item.Ref(), "error", err)
		}
	}
	if err := p.AttachAuthor(ctx, plan.ID, sid); err != nil {
		return domain.BacklogPlan{}, err
	}
	plan.PlannerSessionID = sid
	if !cur.Status.Open() {
		if _, err := p.transition(ctx, cur.ID, domain.PlanSuperseded, nil); err != nil {
			return domain.BacklogPlan{}, err
		}
	}
	return plan, nil
}
