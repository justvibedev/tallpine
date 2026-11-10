package turnnotify

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (h *harness) terminal(event string, state domain.ActivityState, tool, subagent string) {
	_ = h.o.ApplyActivitySignal(context.Background(), "w1", ports.ActivitySignal{Valid: true, State: state, Event: event, ToolName: tool, SubagentID: subagent})
}

// promptCount counts the plain terminal-prompt messages. They are never
// needs-input reports, so no open wait outlives the prompt (a false human
// wait or a late stale Inbox item).
func promptCount(h *harness) (n int) {
	for _, r := range h.reports.got {
		if strings.HasPrefix(r.Message, "[auto] terminal_") && r.State == "" && r.Synthesized {
			n++
		}
	}
	return n
}

// A terminal agent blocked on a permission dialog ends no turn, so the
// orchestrator must hear of it from the wait itself, once per episode.
func TestTerminalPermissionPromptMessagesOrchestratorOnce(t *testing.T) {
	for _, tc := range []struct {
		name, event, sub string
		state            domain.ActivityState
	}{
		{"claude hook", "permission-request", "", domain.ActivityBlocked},
		{"claude subagent", "permission-request", "sub-1", domain.ActivityBlocked},
		{"codex hook", "permission-request", "", domain.ActivityWaitingInput},
		{"kiro screen", "terminal-waiting-input", "", domain.ActivityWaitingInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(domain.KindWorker)
			h.terminal("user-prompt-submit", domain.ActivityActive, "", "")
			h.terminal(tc.event, tc.state, "Bash", tc.sub)
			h.terminal(tc.event, tc.state, "Bash", tc.sub)
			if got := promptCount(h); got != 1 || len(h.reports.got) != 1 {
				t.Fatalf("prompt messages = %d of %d, want 1", got, len(h.reports.got))
			}
			if m := h.reports.got[0].Message; !strings.Contains(m, "Bash") {
				t.Fatalf("message = %q", m)
			}
			h.terminal("post-tool-use", domain.ActivityActive, "Bash", tc.sub)
			h.terminal(tc.event, tc.state, "Bash", tc.sub)
			if got := promptCount(h); got != 2 {
				t.Fatalf("a new prompt after work resumed must report again, got %d", got)
			}
		})
	}
}

// Claude fires Notification(permission_prompt) for the dialog the
// permission-request hook already reported; sibling tool traffic that
// lifecycle suppressed (state stays blocked) must not reopen the episode.
func TestSiblingToolTrafficWhileBlockedDoesNotReportTheSameDialogTwice(t *testing.T) {
	h := newHarness(domain.KindWorker)
	h.terminal("user-prompt-submit", domain.ActivityActive, "", "")
	h.terminal("permission-request", domain.ActivityBlocked, "Bash", "")
	rec := h.sessions["w1"]
	rec.Activity.State = domain.ActivityBlocked
	h.sessions["w1"] = rec
	h.terminal("post-tool-use", domain.ActivityActive, "Read", "")
	h.terminal("notification", domain.ActivityBlocked, "", "")
	if got := promptCount(h); got != 1 {
		t.Fatalf("prompt messages = %d, want 1", got)
	}
}

// Without an orchestrator nothing can take the message; the owner already has
// the immediate Inbox ping, so none is filed.
func TestTerminalPromptWithoutOrchestratorSendsNothing(t *testing.T) {
	h := newHarness(domain.KindWorker)
	h.o.OrchestratorLive = func(context.Context, domain.ProjectID, domain.SessionID) bool { return false }
	h.terminal("permission-request", domain.ActivityBlocked, "Bash", "")
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d, want 0", len(h.reports.got))
	}
}

// Only a permission dialog is called one: a Claude question, an expired login
// (both reach the observer as waiting_input) and Codex's other waits are not.
func TestTerminalWaitWordingFollowsWhatTheSignalIs(t *testing.T) {
	for _, tc := range []struct {
		name, event, harness, want string
		state                      domain.ActivityState
	}{
		{"claude question", "notification", "claude-code", "terminal_wait", domain.ActivityWaitingInput},
		{"claude login expired", "terminal-waiting-input", "claude-code", "terminal_wait", domain.ActivityWaitingInput},
		{"kiro approval screen", "terminal-waiting-input", "kiro", "terminal_prompt", domain.ActivityWaitingInput},
		{"claude dialog", "notification", "claude-code", "terminal_prompt", domain.ActivityBlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(domain.KindWorker)
			rec := h.sessions["w1"]
			rec.Harness = domain.AgentHarness(tc.harness)
			h.sessions["w1"] = rec
			h.terminal(tc.event, tc.state, "", "")
			if len(h.reports.got) != 1 || !strings.HasPrefix(h.reports.got[0].Message, "[auto] "+tc.want+":") {
				t.Fatalf("reports = %+v, want one %s", h.reports.got, tc.want)
			}
			if strings.Contains(h.reports.got[0].Message, "refused") {
				t.Fatalf("message claims a refusal: %q", h.reports.got[0].Message)
			}
		})
	}
}

// A wait nobody could receive is not latched: an orchestrator that starts
// during it is still told.
func TestTerminalPromptWithoutOrchestratorIsNotLatched(t *testing.T) {
	h := newHarness(domain.KindWorker)
	live := false
	h.o.OrchestratorLive = func(context.Context, domain.ProjectID, domain.SessionID) bool { return live }
	h.terminal("permission-request", domain.ActivityBlocked, "Bash", "")
	live = true
	h.terminal("notification", domain.ActivityBlocked, "", "")
	if got := promptCount(h); got != 1 {
		t.Fatalf("prompt messages = %d, want 1", got)
	}
}
