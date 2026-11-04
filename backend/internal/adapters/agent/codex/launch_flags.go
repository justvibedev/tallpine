package codex

import (
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// appendFastTierFlag starts Codex on its faster service tier. "priority" is the
// tier's id in Codex's own model list and session records.
func appendFastTierFlag(args *[]string, launch domain.AgentLaunchOptions) {
	switch {
	case launch.FastMode:
		*args = append(*args, "-c", "service_tier="+agentruntime.CodexTOMLString("priority"))
	case launch.FastModeOff:
		*args = append(*args, "-c", "service_tier="+agentruntime.CodexTOMLString("default"))
	}
}

// appendAutoCompactFlag sets Codex's model_auto_compact_token_limit (documented
// in its config reference) from the session's compaction choice. Auto, or a
// percent whose model window is unknown, leaves Codex's own default.
func appendAutoCompactFlag(args *[]string, launch domain.AgentLaunchOptions) {
	if tokens := domain.AutoCompactTokenCount(launch.AutoCompactWindow, launch.ModelContextWindow); tokens > 0 {
		*args = append(*args, "-c", "model_auto_compact_token_limit="+strconv.Itoa(tokens))
	}
}

func appendReasoningEffortFlag(args *[]string, effort string) {
	if effort = strings.TrimSpace(effort); effort != "" {
		*args = append(*args, "-c", "model_reasoning_effort="+agentruntime.CodexTOMLString(effort))
	}
}
