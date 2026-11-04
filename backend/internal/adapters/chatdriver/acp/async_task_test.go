package acp

import (
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Frames shaped as claude-agent-acp 0.99.0 async-tasks.js:571 (spawned) and :662 (state_update).
func TestParseAsyncTaskFrames(t *testing.T) {
	spawned := `{"sessionId":"s1","update":{"sessionUpdate":"async_task_spawned","asyncTaskId":"b7x","name":"Monitor","taskType":"monitor","description":"watch ci","showInTranscript":false,"canStop":false}}`
	task, sid, ok := parseAsyncTask(json.RawMessage(spawned))
	if !ok || sid != "s1" || task.ID != "b7x" || task.Name != "Monitor" || task.State != ports.ChatAsyncTaskRunning || task.Done() {
		t.Fatalf("spawned = %+v %q %v", task, sid, ok)
	}
	ended := `{"sessionId":"s1","update":{"sessionUpdate":"async_task_state_update","asyncTaskId":"b7x","state":"completed","summary":"done"}}`
	task, _, ok = parseAsyncTask(json.RawMessage(ended))
	if !ok || !task.Done() || task.Summary != "done" {
		t.Fatalf("ended = %+v %v", task, ok)
	}
	other := `{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk"}}`
	if _, _, ok := parseAsyncTask(json.RawMessage(other)); ok {
		t.Fatal("agent_message_chunk parsed as async task")
	}
}
