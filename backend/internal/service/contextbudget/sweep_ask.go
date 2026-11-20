package contextbudget

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
)

// patchFile is the drafted pointer patch beside evidence.md.
const patchFile = "pointer.patch"

// askInput is the card for a finding: Yes starts a worker, No silences it.
func askInput(f domain.ContextHealthFinding) asksvc.CreateInput {
	text := fmt.Sprintf("The always-loaded instruction files are %s tokens, over the %s budget. Start a worker to trim them?", k(f.MeasuredTokens), k(f.BudgetTokens))
	if f.Kind == domain.ContextMemoryBudget {
		text = fmt.Sprintf("The Claude orchestrator memory index is %s tokens, over the %s budget. Start a worker to archive stale entries?", k(f.MeasuredTokens), k(f.BudgetTokens))
	}
	if f.Kind == domain.ContextRoutingPointer {
		text = fmt.Sprintf("%d doc-pointer gaps this week, with the pointer lines drafted. Start a worker to add them?", f.MeasuredTokens)
	}
	reason, _, _ := strings.Cut(f.Evidence, "\n")
	if utf8.RuneCountInString(reason) > domain.MaxAskReasonChars {
		reason = string([]rune(reason)[:domain.MaxAskReasonChars-1]) + "…"
	}
	return asksvc.CreateInput{
		Text: text, Reason: reason, About: "context-health:" + f.Fingerprint,
		Options: []domain.PlanOption{
			{ID: "yes", Label: "Yes, start a worker"},
			{ID: "no", Label: "No, leave it"},
		},
		Recommended: "yes",
	}
}
