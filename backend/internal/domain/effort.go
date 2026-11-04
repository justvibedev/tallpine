package domain

import "strings"

// EffortOrder is the one ascending vocabulary of reasoning-effort levels.
// "none" and "off" are the same rank (no reasoning). Background chat, provider
// catalogs, agentConfig.mode validation and the frontend pickers all read their
// ordering from here; frontend/src/shared/effort.ts mirrors it and is held to
// it by TestEffortOrderMatchesFrontend.
var EffortOrder = []string{"none", "off", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

// EffortRank is the position of level in the ascending order, with none and
// off sharing rank 0. ok is false for a level outside the vocabulary
// (including the "default" pseudo-level).
func EffortRank(level string) (rank int, ok bool) {
	level = strings.ToLower(level)
	switch level {
	case "none", "off":
		return 0, true
	}
	for i, known := range EffortOrder {
		if known == level {
			// none and off both occupy rank 0, so later levels shift down one.
			return i - 1, true
		}
	}
	return 0, false
}

// ValidEffort reports whether level is in the effort vocabulary.
func ValidEffort(level string) bool {
	_, ok := EffortRank(level)
	return ok
}

// LowestEffort returns the lowest-ranked known level in values, unchanged, or
// "" when none is known. Ties keep the first.
func LowestEffort(values []string) string {
	best, bestRank := "", len(EffortOrder)
	for _, value := range values {
		if rank, ok := EffortRank(value); ok && rank < bestRank {
			best, bestRank = value, rank
		}
	}
	return best
}

// AgentEfforts is the ascending effort levels an agent CLI accepts, for
// surfaces with no live model catalog (cloud project settings). A live catalog
// (AgentModelInfo.Efforts) always wins. nil means the agent takes no fixed
// list. Mirrored by AGENT_EFFORTS in frontend/src/shared/effort.ts.
var AgentEfforts = map[string][]string{
	"codex":       {"low", "medium", "high", "xhigh", "max"},
	"claude-code": {"low", "medium", "high", "max"},
}
