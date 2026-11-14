package codexappserver

import (
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// codexModelNamePatterns find the rejected model's name in Codex's wordings:
// "The model `gpt-x` does not exist ...", "The 'gpt-x' model is not supported ...",
// or an unquoted "model: gpt-x". The unquoted form needs a digit or hyphen,
// which every model ID has, so English words ("model is not ...") never match.
var codexModelNamePatterns = []*regexp.Regexp{
	regexp.MustCompile("(?i)model[ :=]+[`'\"]([A-Za-z0-9][A-Za-z0-9._:/-]*)[`'\"]"),
	regexp.MustCompile("(?i)[`'\"]([A-Za-z0-9][A-Za-z0-9._:/-]*)[`'\"] model\\b"),
	regexp.MustCompile("(?i)model[ :=]+([A-Za-z0-9._:/]*[0-9-][A-Za-z0-9._:/-]*)"),
}

// isCodexModelUnavailable reports whether a provider failure is the account
// rejecting the requested model (HTTP 400 model_not_found and its wordings).
func isCodexModelUnavailable(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "model_not_found") ||
		(strings.Contains(lower, "model") && strings.Contains(lower, "does not exist or you do not have access"))
}

// codexModelUnavailableFailure rewrites the raw 400 into a message that says
// what happened and where the model may have come from: with no explicit model,
// Codex uses the one pinned in ~/.codex/config.toml. The wording stays
// conditional because the driver cannot tell an explicit choice from that default.
func codexModelUnavailableFailure(message, detail string, cause error) error {
	name := codexRejectedModelName(message, detail)
	subject := "The selected model"
	if name != "" {
		subject = "Model " + name
	}
	summary := subject + " is not available for this account. If you did not pick this model yourself, Codex took it from the `model` setting in ~/.codex/config.toml: change or remove that line. Otherwise choose another model for the session."
	return ports.NewChatProviderFailure(summary, strings.TrimSpace(message+"\n"+detail), cause)
}

// codexRejectedModelName returns the model name the error texts mention, or ""
// when none of them names one.
func codexRejectedModelName(texts ...string) string {
	for _, pattern := range codexModelNamePatterns {
		for _, text := range texts {
			if m := pattern.FindStringSubmatch(text); len(m) > 1 && !strings.EqualFold(m[1], "not_found") {
				return m[1]
			}
		}
	}
	return ""
}
