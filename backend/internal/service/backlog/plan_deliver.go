package backlog

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// answerRecord renders the questions and the answers as the text a planner reads.
func answerRecord(pl domain.BacklogPlan) string {
	var b strings.Builder
	for _, q := range pl.Questions {
		a := pl.Answers.Questions[q.ID]
		answer := a.Other
		if answer == "" {
			for _, o := range q.Options {
				if o.ID == a.Option {
					answer = o.Label
				}
			}
		}
		fmt.Fprintf(&b, "- Q %s: %s\n  Answer: %s\n", q.ID, q.Text, answer)
	}
	for _, a := range pl.Assumptions {
		if c, ok := pl.Answers.Corrections[a.ID]; ok {
			fmt.Fprintf(&b, "- Assumption %s: %s\n  Correction: %s\n", a.ID, a.Text, c)
		} else {
			fmt.Fprintf(&b, "- Assumption %s: %s\n  Accepted.\n", a.ID, a.Text)
		}
	}
	return b.String()
}

// deliverAnswers sends the answers to the plan's planner. When that session
// has ended, a new planner is started with the record in its brief. It reports
// whether it started a new one.
func (p *PlanService) deliverAnswers(ctx context.Context, plan domain.BacklogPlan, item domain.BacklogItem, inPlace bool) (bool, error) {
	record := answerRecord(plan)
	msg := fmt.Sprintf("The owner answered. Write and submit the plan now with `tallpine plan submit %s`.\n\n%s", item.Ref(), record)
	return p.deliverToAuthor(ctx, plan, item, inPlace, msg, record, p.supersededBrief(ctx, item, plan.ID, maxPlanContextRunes))
}

// deliverToAuthor sends msg to the plan's live planner, or starts a new one
// (an implementer in place for an in-place plan) whose brief carries record and
// extra (a caller whose message must reach a new session passes it in extra).
// It reports whether it started a new session.
func (p *PlanService) deliverToAuthor(ctx context.Context, plan domain.BacklogPlan, item domain.BacklogItem, inPlace bool, msg, record string, extra ...string) (bool, error) {
	if plan.PlannerSessionID != "" {
		delivered, err := p.sessions.SendToPlanner(ctx, plan.PlannerSessionID, msg)
		if err == nil && delivered {
			return false, nil
		}
		if err != nil {
			p.log.Warn("send answers to planner", "plan", plan.ID, "session", plan.PlannerSessionID, "error", err)
		}
	}
	spawn := PlannerSpawn{
		ProjectID: plan.ProjectID, ItemID: item.ID, ItemRef: item.Ref(), PlanID: plan.ID,
		DisplayName: "Plan " + item.Ref(), StarterSessionID: plan.NotifySessionID, Prompt: plannerTask(item, record, extra...),
	}
	spawnFn := p.sessions.SpawnPlanner
	if inPlace {
		// The author ended: start the implementer again in place, never a planner.
		spawn.DisplayName, spawn.LaneItemID, spawn.Prompt = item.Ref(), item.ParentID, strings.TrimSpace(p.inPlaceTask(ctx, item, record)+"\n\n"+strings.Join(extra, "\n\n"))
		spawnFn = p.sessions.SpawnInPlace
	}
	sid, err := spawnFn(ctx, spawn)
	if err != nil {
		return false, err
	}
	if _, err := p.store.UpdateBacklogPlan(ctx, plan.ID, func(pl *domain.BacklogPlan) error {
		pl.PlannerSessionID, pl.UpdatedAt = sid, p.now().UTC()
		return nil
	}); err != nil {
		return true, err
	}
	if err := p.bl.LinkSession(ctx, item.ID, sid, "planner"); err != nil {
		p.log.Warn("link planner session to item", "item", item.Ref(), "session", sid, "error", err)
	}
	return true, nil
}
