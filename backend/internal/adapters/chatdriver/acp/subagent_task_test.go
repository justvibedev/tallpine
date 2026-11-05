package acp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// sdkFrame is a _claude/sdkMessage notification as claude-agent-acp 0.99.0
// sends it (acp-agent.js extNotification "_claude/sdkMessage"), carrying an SDK
// message shaped as sdk.d.ts SDKTaskStartedMessage / SDKTaskUpdatedMessage /
// SDKTaskNotificationMessage.
func sdkFrame(session, message string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"sessionId":%q,"message":%s}`, session, message))
}

func subagentConversation() *conversation {
	return &conversation{
		sessionID: "s1",
		events:    make(chan ports.ChatEvent, 16),
		log:       slog.New(slog.DiscardHandler),
		tools: map[string]*toolState{
			"toolu_bg": {id: "toolu_bg", rawInput: map[string]any{
				"description": "Fable planner: hub UX plan", "model": "fable", "run_in_background": true,
			}},
			"toolu_fg": {id: "toolu_fg", rawInput: map[string]any{"description": "Explore"}},
		},
	}
}

func notify(t *testing.T, c *conversation, session, message string) {
	t.Helper()
	if !c.handleNotification(ClaudeSDKMessageMethod, sdkFrame(session, message)) {
		t.Fatalf("%s not handled", ClaudeSDKMessageMethod)
	}
}

func asyncTaskEvents(c *conversation) []ports.ChatAsyncTask {
	var out []ports.ChatAsyncTask
	for {
		select {
		case ev := <-c.events:
			if ev.Kind == ports.ChatEventAsyncTask && ev.AsyncTask != nil {
				out = append(out, *ev.AsyncTask)
			}
		default:
			return out
		}
	}
}

// B562: an orchestrator's run_in_background Agent call ran ~5 min with
// nothing in Tallpine naming it. Its SDK task frames now list it while it runs
// (description, model) and end it on the task notification.
func TestBackgroundSubagentFramesBecomeAsyncTasks(t *testing.T) {
	c := subagentConversation()
	notify(t, c, "s1", `{"type":"system","subtype":"task_started","task_id":"acc20e","tool_use_id":"toolu_bg","description":"Fable planner: hub UX plan","subagent_type":"general-purpose","task_type":"local_agent"}`)
	got := asyncTaskEvents(c)
	if len(got) != 1 || got[0].ID != "acc20e" || got[0].TaskType != ports.ChatAsyncTaskSubagent ||
		got[0].Name != "Fable planner: hub UX plan" || got[0].Model != "fable" || got[0].State != ports.ChatAsyncTaskRunning {
		t.Fatalf("started = %+v, want one running subagent task with its description and model", got)
	}
	notify(t, c, "s1", `{"type":"system","subtype":"task_notification","task_id":"acc20e","tool_use_id":"toolu_bg","status":"completed","output_file":"/x","summary":"Agent finished"}`)
	got = asyncTaskEvents(c)
	if len(got) != 1 || got[0].ID != "acc20e" || got[0].State != ports.ChatAsyncTaskCompleted || got[0].Summary != "Agent finished" {
		t.Fatalf("ended = %+v, want the subagent completed", got)
	}
	// The SDK also sends a terminal task_updated; the task is already gone.
	notify(t, c, "s1", `{"type":"system","subtype":"task_updated","task_id":"acc20e","patch":{"status":"completed"}}`)
	if got := asyncTaskEvents(c); len(got) != 0 {
		t.Fatalf("repeated end = %+v, want none", got)
	}
}

func TestForegroundSubagentListedOnlyOnceBackgrounded(t *testing.T) {
	c := subagentConversation()
	notify(t, c, "s1", `{"type":"system","subtype":"task_started","task_id":"fg1","tool_use_id":"toolu_fg","description":"Explore","subagent_type":"Explore","is_backgrounded":false}`)
	if got := asyncTaskEvents(c); len(got) != 0 {
		t.Fatalf("foreground subagent listed: %+v (its tool row already shows it)", got)
	}
	notify(t, c, "s1", `{"type":"system","subtype":"task_updated","task_id":"fg1","patch":{"is_backgrounded":true}}`)
	got := asyncTaskEvents(c)
	if len(got) != 1 || got[0].State != ports.ChatAsyncTaskRunning || got[0].Name != "Explore" {
		t.Fatalf("backgrounded = %+v, want it listed", got)
	}
	notify(t, c, "s1", `{"type":"system","subtype":"task_updated","task_id":"fg1","patch":{"status":"killed"}}`)
	if got := asyncTaskEvents(c); len(got) != 1 || got[0].State != ports.ChatAsyncTaskStopped {
		t.Fatalf("killed = %+v, want stopped", got)
	}
}

func TestSDKTaskFramesIgnoreOtherTasksAndSessions(t *testing.T) {
	c := subagentConversation()
	// A background Bash task already has its own async_task lifecycle.
	notify(t, c, "s1", `{"type":"system","subtype":"task_started","task_id":"b1","description":"npm test","task_type":"local_bash","is_backgrounded":true}`)
	notify(t, c, "s1", `{"type":"system","subtype":"task_notification","task_id":"b1","status":"completed"}`)
	notify(t, c, "other", `{"type":"system","subtype":"task_started","task_id":"x1","tool_use_id":"toolu_bg","description":"d","subagent_type":"general-purpose","is_backgrounded":true}`)
	if got := asyncTaskEvents(c); len(got) != 0 {
		t.Fatalf("events = %+v, want none for a shell task or another session", got)
	}
}

// Review of #1007: foreground subagents whose end frame never arrives must not
// fill the tracked set and hide every later background subagent.
func TestFullSubagentSetStillAnnouncesNewBackgroundSubagent(t *testing.T) {
	c := subagentConversation()
	// A listed subagent outlives the foreground ones: its end must still clear it.
	notify(t, c, "s1", `{"type":"system","subtype":"task_started","task_id":"early","tool_use_id":"toolu_bg","description":"early","subagent_type":"general-purpose"}`)
	asyncTaskEvents(c)
	for i := 0; i < maxTrackedSubagents+5; i++ {
		notify(t, c, "s1", fmt.Sprintf(`{"type":"system","subtype":"task_started","task_id":"fg%d","tool_use_id":"toolu_fg","description":"Explore","subagent_type":"Explore","is_backgrounded":false}`, i))
	}
	notify(t, c, "s1", `{"type":"system","subtype":"task_started","task_id":"late","tool_use_id":"toolu_bg","description":"Fable planner: hub UX plan","subagent_type":"general-purpose"}`)
	got := asyncTaskEvents(c)
	if len(got) != 1 || got[0].ID != "late" || got[0].State != ports.ChatAsyncTaskRunning {
		t.Fatalf("events = %+v, want the late background subagent listed", got)
	}
	if len(c.subagents) > maxTrackedSubagents {
		t.Fatalf("tracked = %d, want at most %d", len(c.subagents), maxTrackedSubagents)
	}
	if _, kept := c.subagents["fg0"]; kept {
		t.Fatal("oldest foreground subagent kept; eviction must drop the oldest unlisted entry")
	}
	for _, id := range []string{"early", "late"} {
		notify(t, c, "s1", fmt.Sprintf(`{"type":"system","subtype":"task_notification","task_id":%q,"status":"completed"}`, id))
		if got := asyncTaskEvents(c); len(got) != 1 || got[0].ID != id || got[0].State != ports.ChatAsyncTaskCompleted {
			t.Fatalf("end of %s = %+v, want it completed", id, got)
		}
	}
}
