package activitydispatch

import (
	"encoding/json"
)

// claudeSubagentFacts keeps native child identity separate from the resumable
// main session id. A non-nil empty slice proves no children remain; nil means
// Claude could not supply a task-registry snapshot and must not clear children.
func claudeSubagentFacts(event string, payload []byte) (string, *[]string) {
	var p struct {
		AgentID         string          `json:"agent_id"`
		BackgroundTasks json.RawMessage `json:"background_tasks"`
	}
	if json.Unmarshal(normalizeHookPayload(payload), &p) != nil {
		return "", nil
	}
	id := validSubagentID(p.AgentID)
	if (event != "stop" && event != "subagent-stop") || len(p.BackgroundTasks) == 0 || p.BackgroundTasks[0] != '[' {
		return id, nil
	}
	var tasks []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(p.BackgroundTasks, &tasks) != nil || len(tasks) > 128 {
		return id, nil
	}
	running := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task.Type != "subagent" {
			continue
		}
		childID := validSubagentID(task.ID)
		if childID == "" {
			return id, nil
		}
		running = append(running, childID)
	}
	return id, &running
}
