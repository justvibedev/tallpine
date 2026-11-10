package backlog

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// maxPlanContextRunes caps the plan section of a task prompt.
const maxPlanContextRunes = 16000

// planSpawnContext is the plan section of an implementer's task prompt: the
// decisions table and the PR-split entries, in full. The whole plan stays one
// `tallpine plan show` away.
func planSpawnContext(plan domain.BacklogPlan, itemRef string) string {
	var b strings.Builder
	b.WriteString("Approved plan (build from it; report a departure with `tallpine report ... --note \"Deviation: ...\"`):\n")
	writePlanDecisionsAndSplit(&b, plan)
	out := strings.TrimRight(b.String(), "\n")
	if r := []rune(out); len(r) > maxPlanContextRunes {
		out = string(r[:maxPlanContextRunes]) + "\n[truncated]"
	}
	return out + "\n\nFull plan: tallpine plan show " + itemRef
}

// writePlanDecisionsAndSplit renders the decisions table and PR-split entries.
func writePlanDecisionsAndSplit(b *strings.Builder, plan domain.BacklogPlan) {
	if d := strings.TrimSpace(plan.DecisionsMD); d != "" {
		fmt.Fprintf(b, "\nDecisions:\n%s\n", d)
	}
	if len(plan.PRSplit) > 0 {
		b.WriteString("\nPR split:\n")
		for i, pr := range plan.PRSplit {
			fmt.Fprintf(b, "%d. %s\n", i+1, pr.Title)
			if s := strings.TrimSpace(pr.Summary); s != "" {
				fmt.Fprintf(b, "   %s\n", s)
			}
			if len(pr.Files) > 0 {
				fmt.Fprintf(b, "   Files: %s\n", strings.Join(pr.Files, ", "))
			}
			if t := strings.TrimSpace(pr.Tests); t != "" {
				fmt.Fprintf(b, "   Tests: %s\n", t)
			}
			if len(pr.Consumes) > 0 {
				fmt.Fprintf(b, "   Consumes: %s\n", strings.Join(pr.Consumes, "; "))
			}
			if len(pr.Produces) > 0 {
				fmt.Fprintf(b, "   Produces: %s\n", strings.Join(pr.Produces, "; "))
			}
		}
	}
}

// hasPlanBody reports whether a plan holds submitted content (markdown,
// decisions or a PR split), not just questions.
func hasPlanBody(pl domain.BacklogPlan) bool {
	return strings.TrimSpace(pl.PlanMarkdown) != "" || strings.TrimSpace(pl.DecisionsMD) != "" || len(pl.PRSplit) > 0
}

// priorBrief briefs a replanner from cur. When cur has no submitted content (an
// empty draft, a failed replacement, or one that only asked questions) the
// item's newest superseded plan that has, goes first, so a second replan does
// not lose the real plan; cur's own questions and answers still follow.
func (p *PlanService) priorBrief(ctx context.Context, item domain.BacklogItem, cur domain.BacklogPlan) string {
	ref := item.Ref()
	if hasPlanBody(cur) {
		return priorPlanBrief(cur, ref, maxPlanContextRunes)
	}
	if base := p.supersededBrief(ctx, item, cur.ID, maxPlanContextRunes/2); base != "" {
		return strings.TrimSpace(base + "\n\n" + priorPlanBrief(cur, ref, maxPlanContextRunes/2))
	}
	return priorPlanBrief(cur, ref, maxPlanContextRunes)
}

// Failure notes that mark a plan superseded without a replan: the owner
// cancelled it, or its item closed. Such a plan is not replan input.
const (
	planCancelledNote  = "cancelled by the owner"
	planItemClosedNote = "its item was closed"
)

// noteEnding stamps a plan being superseded without a replan, keeping any
// failure reason it already had.
func noteEnding(pl *domain.BacklogPlan, note string) {
	if pl.Failure != "" {
		note += ": " + pl.Failure
	}
	pl.Failure = note
}

// endsReplanLineage reports whether the plan was cancelled or closed with its
// item, so no older plan may brief a later planner.
func endsReplanLineage(pl domain.BacklogPlan) bool {
	return strings.HasPrefix(pl.Failure, planCancelledNote) || strings.HasPrefix(pl.Failure, planItemClosedNote)
}

// supersededBrief is the brief of the item's newest plan that a replan
// superseded (other than skipID) and that has submitted content, else one with
// only questions; never one behind a cancelled or closed plan. Empty if none.
func (p *PlanService) supersededBrief(ctx context.Context, item domain.BacklogItem, skipID string, limit int) string {
	old, err := p.store.SupersededBacklogPlansForItem(ctx, item.ID)
	if err != nil {
		p.log.Warn("list superseded plans", "item", item.Ref(), "error", err)
		return ""
	}
	// Prefer the newest plan with a body; failing that, the newest one that only
	// has questions or assumptions (with any answers), so they are not asked again.
	fallback := ""
	for _, o := range old {
		if o.ID == skipID {
			continue
		}
		if endsReplanLineage(o) {
			break // the owner discarded this line of planning: nothing older is input
		}
		if hasPlanBody(o) {
			return priorPlanBrief(o, item.Ref(), limit)
		}
		if fallback == "" {
			fallback = priorPlanBrief(o, item.Ref(), limit)
		}
	}
	return fallback
}

// priorPlanBrief is the read-only record of a superseded plan for the planner
// of its replacement: the decisions, PR split, questions, answers and
// assumptions, so a small revision need not re-derive everything. Empty when
// the old plan holds nothing. It stays within limit runes (a launch command
// line has a hard length cap on Windows); the header points to the full text.
func priorPlanBrief(old domain.BacklogPlan, itemRef string, limit int) string {
	if !hasPlanBody(old) && len(old.Questions) == 0 && len(old.Assumptions) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Superseded plan %s (read-only input: revise it, do not re-derive it; the full text is `tallpine plan show %s --plan-id %s`):\n", old.ID, itemRef, old.ID)
	if old.Answers != nil && answerRecord(old) != "" {
		fmt.Fprintf(&b, "\nQuestions, answers and assumptions:\n%s", answerRecord(old))
	} else if len(old.Questions)+len(old.Assumptions) > 0 {
		b.WriteString("\nQuestions and assumptions (unanswered):\n")
		for _, q := range old.Questions {
			fmt.Fprintf(&b, "- Q %s: %s\n", q.ID, q.Text)
		}
		for _, a := range old.Assumptions {
			fmt.Fprintf(&b, "- Assumption %s: %s\n", a.ID, a.Text)
		}
	}
	writePlanDecisionsAndSplit(&b, old)
	if m := strings.TrimSpace(old.PlanMarkdown); m != "" {
		fmt.Fprintf(&b, "\n\nPlan:\n%s", m)
	}
	out := b.String()
	if r := []rune(out); len(r) > limit {
		out = string(r[:limit]) + "\n[truncated]"
	}
	return out
}
