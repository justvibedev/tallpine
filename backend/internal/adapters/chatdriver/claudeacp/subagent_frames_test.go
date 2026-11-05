package claudeacp

import (
	"reflect"
	"testing"

	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
)

// B562: a background subagent is visible only through the SDK task frames, so
// every session, even one with no prompt or options, asks for exactly those.
func TestClaudeSessionMetaRequestsSubagentTaskFrames(t *testing.T) {
	t.Setenv("CLAUDE_CODE_ENABLE_TODO_TOOLS", "0")
	for name, cfg := range map[string]acpdriver.LaunchConfig{
		"empty": {}, "tools off": {DisableTools: true, SystemPrompt: "x"},
	} {
		code := claudeNestedMap(claudeSessionMeta(cfg), "claudeCode")
		want := []map[string]string{
			{"type": "system", "subtype": "task_started"},
			{"type": "system", "subtype": "task_updated"},
			{"type": "system", "subtype": "task_notification"},
		}
		if !reflect.DeepEqual(code["emitRawSDKMessages"], want) {
			t.Fatalf("%s: emitRawSDKMessages = %#v, want the task frames only", name, code["emitRawSDKMessages"])
		}
	}
}
