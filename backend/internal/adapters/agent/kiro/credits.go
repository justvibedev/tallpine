package kiro

// PromptTurnSummary is one entry of Kiro's promptTurnSummaries, which both the
// terminal transcript (usage_summary records) and the ACP prompt response
// (_meta.kiro) report.
type PromptTurnSummary struct {
	Unit  string  `json:"unit"`
	Usage float64 `json:"usage"`
}

// CreditsFromSummaries sums the credits one turn cost: entries whose unit is
// "credit" and whose usage is positive. ok is false when the turn cost nothing,
// so callers skip turns with no credit row. This is the single place that knows
// Kiro's credit schema.
func CreditsFromSummaries(summaries []PromptTurnSummary) (credits float64, ok bool) {
	for _, s := range summaries {
		if s.Unit == "credit" && s.Usage > 0 {
			credits += s.Usage
		}
	}
	return credits, credits > 0
}
