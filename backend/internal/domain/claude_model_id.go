package domain

import "regexp"

// claudeModelIDPattern matches a first-party Claude API model ID such as
// "claude-fable-5-2", "claude-opus-4-5-20251101" or "claude-opus-5-5[1m]".
var claudeModelIDPattern = regexp.MustCompile(`^claude-(fable|mythos|opus|sonnet|haiku)-\d+(?:-\d{1,2})?(?:-\d{8})?(?:\[[^\]]+\])?$`)

// IsClaudeModelID reports whether id is shaped like a first-party Claude model
// ID. The account's model list can lag a release (and Claude Code's own chat
// picker offers new models before it does), so callers accept a well-formed
// ID the list lacks and leave the exact version to the provider; a malformed
// one is still treated as a typo.
func IsClaudeModelID(id string) bool {
	return claudeModelIDPattern.MatchString(id)
}

// AcceptsUnlistedClaudeModelID reports whether a catalog of listedIDs uses
// first-party IDs, the only route where a well-formed ID the list lacks is
// plausibly just a release the list has not caught up with. Bedrock
// ("anthropic.claude-...") and Vertex ("claude-...@date") catalogs use their
// own formats, which IsClaudeModelID does not match, so for them an unlisted
// ID stays refused.
func AcceptsUnlistedClaudeModelID(id string, listedIDs []string) bool {
	if !IsClaudeModelID(id) {
		return false
	}
	for _, listed := range listedIDs {
		if IsClaudeModelID(listed) {
			return true
		}
	}
	return false
}
