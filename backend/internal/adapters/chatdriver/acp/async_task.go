package acp

import (
	"bytes"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Updates claude-agent-acp sends to a client that advertises the AIR
// "asyncTasks" capability (async-tasks.js: async_task_spawned,
// async_task_progress, async_task_state_update). The SDK's SessionUpdate union
// has no variant for them, so they are read from the raw frame before the
// union decode would reject them.
const (
	asyncTaskSpawned  = "async_task_spawned"
	asyncTaskProgress = "async_task_progress"
	asyncTaskState    = "async_task_state_update"
)

type asyncTaskFrame struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		Kind        string `json:"sessionUpdate"`
		ID          string `json:"asyncTaskId"`
		Name        string `json:"name"`
		TaskType    string `json:"taskType"`
		Description string `json:"description"`
		State       string `json:"state"`
		Summary     string `json:"summary"`
	} `json:"update"`
}

// parseAsyncTask returns the task an async-task session/update frame carries,
// and false for any other frame.
func parseAsyncTask(params json.RawMessage) (ports.ChatAsyncTask, string, bool) {
	// Streaming chunks dominate this path; skip their second decode.
	if !bytes.Contains(params, []byte("async_task_")) {
		return ports.ChatAsyncTask{}, "", false
	}
	var frame asyncTaskFrame
	if json.Unmarshal(params, &frame) != nil {
		return ports.ChatAsyncTask{}, "", false
	}
	u := frame.Update
	switch u.Kind {
	case asyncTaskSpawned, asyncTaskProgress, asyncTaskState:
	default:
		return ports.ChatAsyncTask{}, "", false
	}
	id := strings.TrimSpace(u.ID)
	if id == "" {
		return ports.ChatAsyncTask{}, "", false
	}
	task := ports.ChatAsyncTask{
		ID: id, Name: u.Name, TaskType: u.TaskType, Description: u.Description,
		State: u.State, Summary: u.Summary,
	}
	if u.Kind == asyncTaskSpawned {
		task.State = ports.ChatAsyncTaskRunning
	}
	return task, frame.SessionID, true
}

// handleAsyncTask emits the event for an async-task frame and reports whether
// the frame was one.
func (c *conversation) handleAsyncTask(params json.RawMessage) bool {
	task, sessionID, ok := parseAsyncTask(params)
	if !ok {
		return false
	}
	// A loaded session replays its tasks as history; a task from before the load
	// is not one the agent waits on now.
	c.replayMu.Lock()
	replaying := c.replaying
	c.replayMu.Unlock()
	if replaying {
		return true
	}
	c.mu.Lock()
	own := c.sessionID
	c.mu.Unlock()
	if own != "" && sessionID != own {
		return true
	}
	c.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &task})
	return true
}
