package backlog

import (
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
	if d := strings.TrimSpace(plan.DecisionsMD); d != "" {
		fmt.Fprintf(&b, "\nDecisions:\n%s\n", d)
	}
	if len(plan.PRSplit) > 0 {
		b.WriteString("\nPR split:\n")
		for i, pr := range plan.PRSplit {
			fmt.Fprintf(&b, "%d. %s\n", i+1, pr.Title)
			if s := strings.TrimSpace(pr.Summary); s != "" {
				fmt.Fprintf(&b, "   %s\n", s)
			}
			if len(pr.Files) > 0 {
				fmt.Fprintf(&b, "   Files: %s\n", strings.Join(pr.Files, ", "))
			}
			if t := strings.TrimSpace(pr.Tests); t != "" {
				fmt.Fprintf(&b, "   Tests: %s\n", t)
			}
			if len(pr.Consumes) > 0 {
				fmt.Fprintf(&b, "   Consumes: %s\n", strings.Join(pr.Consumes, "; "))
			}
			if len(pr.Produces) > 0 {
				fmt.Fprintf(&b, "   Produces: %s\n", strings.Join(pr.Produces, "; "))
			}
		}
	}
	out := strings.TrimRight(b.String(), "\n")
	if r := []rune(out); len(r) > maxPlanContextRunes {
		out = string(r[:maxPlanContextRunes]) + "\n[truncated]"
	}
	return out + "\n\nFull plan: tallpine plan show " + itemRef
}
