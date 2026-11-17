package codex

import (
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/pkg/agentruntime"
)

// appendLaunchFlags adds the session's launch options: service tier,
// compaction limit and the role's switched-off skills.
func appendLaunchFlags(args *[]string, launch domain.AgentLaunchOptions) {
	appendFastTierFlag(args, launch)
	appendAutoCompactFlag(args, launch)
	appendSkillsConfigFlag(args, launch)
}

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
	if tokens := domain.ResolveAutoCompactWindow(launch.AutoCompactWindow, launch.ModelContextWindow, launch.AutoCompactBaseline); tokens > 0 {
		*args = append(*args, "-c", "model_auto_compact_token_limit="+strconv.Itoa(tokens))
	}
}

// appendSkillsConfigFlag switches off the role's disabled skills. Codex reads
// skills.config entries with a name selector and enabled=false and leaves the
// skill out of its list. Codex 0.169 combines skills.config entries across
// its config layers rather than letting -c replace the array: the account's
// config.toml entries stay in force, and an entry here wins only for the
// skill it names (probed with app-server skills/list, B745 follow-up).
func appendSkillsConfigFlag(args *[]string, launch domain.AgentLaunchOptions) {
	off := launch.SkillLaunch().Off
	if len(off) == 0 {
		return
	}
	entries := make([]string, 0, len(off))
	for _, name := range off {
		entries = append(entries, "{name="+agentruntime.CodexTOMLString(name)+",enabled=false}")
	}
	*args = append(*args, "-c", "skills.config=["+strings.Join(entries, ",")+"]")
}

func appendReasoningEffortFlag(args *[]string, effort string) {
	if effort = strings.TrimSpace(effort); effort != "" {
		*args = append(*args, "-c", "model_reasoning_effort="+agentruntime.CodexTOMLString(effort))
	}
}
