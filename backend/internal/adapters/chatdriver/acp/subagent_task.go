package acp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// ClaudeSDKMessageMethod is the notification claude-agent-acp sends for each
// raw Claude Agent SDK message a session opted into with
// _meta.claudeCode.emitRawSDKMessages (acp-agent.js shouldEmitRawMessage).
const ClaudeSDKMessageMethod = "_claude/sdkMessage"

// The adapter's async-task lifecycle skips subagent tasks (async-tasks.js
// isSubagentTask), so a background subagent is otherwise invisible: the parent
// prompt is held, or ends, with nothing naming the child that still runs.
// These are the SDK frames that bracket a subagent's life.
const (
	sdkTaskStarted      = "task_started"
	sdkTaskUpdated      = "task_updated"
	sdkTaskNotification = "task_notification"
	sdkLocalAgentTask   = "local_agent"
)

// subagentTask is a subagent the SDK started; announced once it runs in the
// background, the only kind the chat lists.
type subagentTask struct {
	name, model, subagentType string
	announced                 bool
	seq                       uint64 // start order, for eviction
}

// maxTrackedSubagents bounds the ids remembered for a session whose end
// frames were lost. Past it the oldest entry is evicted, never the new one.
const maxTrackedSubagents = 128

type sdkTaskFrame struct {
	SessionID string `json:"sessionId"`
	Message   struct {
		Type           string `json:"type"`
		Subtype        string `json:"subtype"`
		TaskID         string `json:"task_id"`
		ToolUseID      string `json:"tool_use_id"`
		Description    string `json:"description"`
		SubagentType   string `json:"subagent_type"`
		TaskType       string `json:"task_type"`
		IsBackgrounded *bool  `json:"is_backgrounded"`
		Status         string `json:"status"`
		Summary        string `json:"summary"`
		Patch          struct {
			Status         string `json:"status"`
			IsBackgrounded *bool  `json:"is_backgrounded"`
		} `json:"patch"`
	} `json:"message"`
}

// handleSDKMessage turns the SDK task frames of a background subagent into
// async-task events, so the daemon lists the subagent while it runs.
func (c *conversation) handleSDKMessage(params json.RawMessage) {
	if !bytes.Contains(params, []byte(`"task_`)) {
		return
	}
	var frame sdkTaskFrame
	if json.Unmarshal(params, &frame) != nil || frame.Message.Type != "system" {
		return
	}
	m := frame.Message
	id := strings.TrimSpace(m.TaskID)
	if id == "" {
		return
	}
	c.replayMu.Lock()
	replaying := c.replaying
	c.replayMu.Unlock()
	if replaying {
		return
	}
	c.mu.Lock()
	if c.sessionID != "" && frame.SessionID != c.sessionID {
		c.mu.Unlock()
		return
	}
	var event *ports.ChatAsyncTask
	switch m.Subtype {
	case sdkTaskStarted:
		if m.SubagentType == "" && m.TaskType != sdkLocalAgentTask {
			break
		}
		// The Agent tool_call precedes its task in the stream (the tool call is
		// sent when the tool_use streams, the task starts when the tool runs),
		// so its input is known here.
		model, background := c.agentToolInput(m.ToolUseID)
		c.subagentSeq++
		task := &subagentTask{name: strings.TrimSpace(m.Description), model: model, subagentType: m.SubagentType, seq: c.subagentSeq}
		if c.subagents == nil {
			c.subagents = make(map[string]*subagentTask)
		}
		if _, known := c.subagents[id]; !known && len(c.subagents) >= maxTrackedSubagents {
			c.evictSubagent()
		}
		c.subagents[id] = task
		if background || (m.IsBackgrounded != nil && *m.IsBackgrounded) {
			event = c.announceSubagent(id, task)
		}
	case sdkTaskUpdated:
		task := c.subagents[id]
		if task == nil {
			break
		}
		if state := subagentEndState(m.Patch.Status); state != "" {
			event = c.endSubagent(id, task, state, "")
		} else if m.Patch.IsBackgrounded != nil && *m.Patch.IsBackgrounded && !task.announced {
			event = c.announceSubagent(id, task)
		}
	case sdkTaskNotification:
		if task := c.subagents[id]; task != nil {
			// A notification is always the end, whatever status it names.
			event = c.endSubagent(id, task, cmp.Or(subagentEndState(m.Status), ports.ChatAsyncTaskCompleted), m.Summary)
		}
	}
	c.mu.Unlock()
	if event != nil {
		c.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: event})
	}
}

// agentToolInput reads the model and background flag of the Agent tool call
// that spawned a subagent. Callers hold c.mu.
func (c *conversation) agentToolInput(toolUseID string) (model string, background bool) {
	tool := c.tools[toolUseID]
	if tool == nil {
		return "", false
	}
	input, _ := tool.rawInput.(map[string]any)
	model, _ = input["model"].(string)
	background, _ = input["run_in_background"].(bool)
	return strings.TrimSpace(model), background
}

// evictSubagent drops the oldest unlisted subagent (a foreground one whose
// end frame never came), or the oldest of all when every one is listed, so a
// new background subagent is always tracked. Callers hold c.mu.
func (c *conversation) evictSubagent() {
	oldest, oldestListed := "", ""
	for id, task := range c.subagents {
		pick := &oldestListed
		if !task.announced {
			pick = &oldest
		}
		if *pick == "" || task.seq < c.subagents[*pick].seq {
			*pick = id
		}
	}
	delete(c.subagents, cmp.Or(oldest, oldestListed))
}

func (c *conversation) announceSubagent(id string, task *subagentTask) *ports.ChatAsyncTask {
	task.announced = true
	return &ports.ChatAsyncTask{
		ID: id, Name: task.name, TaskType: ports.ChatAsyncTaskSubagent, Description: task.subagentType,
		Model: task.model, State: ports.ChatAsyncTaskRunning,
	}
}

// endSubagent forgets the subagent and reports its end when it was listed.
func (c *conversation) endSubagent(id string, task *subagentTask, state, summary string) *ports.ChatAsyncTask {
	delete(c.subagents, id)
	if !task.announced {
		return nil
	}
	return &ports.ChatAsyncTask{
		ID: id, Name: task.name, TaskType: ports.ChatAsyncTaskSubagent, Model: task.model,
		State: state, Summary: summary,
	}
}

// subagentEndState maps an SDK task status to a terminal async-task state, or
// "" for a status that is not terminal.
func subagentEndState(status string) string {
	switch status {
	case "completed":
		return ports.ChatAsyncTaskCompleted
	case "failed":
		return ports.ChatAsyncTaskFailed
	case "stopped", "killed":
		return ports.ChatAsyncTaskStopped
	}
	return ""
}
