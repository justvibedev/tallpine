package activitydispatch

import (
	"encoding/json"
)

func codexSubagentID(event string, payload []byte) string {
	if event != "subagent-start" && event != "subagent-stop" && event != "user-prompt-submit" && event != "pre-tool-use" {
		return ""
	}
	var p struct {
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil {
		return ""
	}
	return validSubagentID(p.AgentID)
}

// Codex emits PostToolUse for spawn_agent before the new child's
// SubagentStart hook. The successful tool response carries a task path but
// not the child's native agent_id, so its tool_use_id is a provisional key.
func codexSpawnToolUseID(payload []byte) string {
	var p struct {
		ToolName     string `json:"tool_name"`
		ToolUseID    string `json:"tool_use_id"`
		AgentID      string `json:"agent_id"`
		ToolResponse string `json:"tool_response"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil ||
		!isCodexSpawnToolName(p.ToolName) || p.AgentID != "" {
		return ""
	}
	id := validSubagentID(p.ToolUseID)
	if id == "" {
		return ""
	}
	var response struct {
		TaskName string `json:"task_name"`
	}
	if json.Unmarshal([]byte(p.ToolResponse), &response) != nil || response.TaskName == "" {
		return ""
	}
	return id
}

func isCodexSpawnToolName(name string) bool {
	return name == "spawn_agent" || name == "collaborationspawn_agent"
}
