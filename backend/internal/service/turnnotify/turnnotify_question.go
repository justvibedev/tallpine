package turnnotify

import (
	"context"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// questionNotice is the needs-input text for an in-chat question. It carries
// the request id and the commands that list and answer it, so the orchestrator
// can act without opening the session.
func questionNotice(s ports.ActivitySignal, text string) string {
	out := "question: " + cmpOr(truncate(text, maxReasonCharacters), "no text given")
	rid := strings.TrimSpace(s.ApprovalRequestID)
	if rid == "" || utf8.RuneCountInString(rid) > maxRequestIDCharacters {
		return out
	}
	return out + "\nRequest id: " + rid + "\nList with `" + brand.CLIName + " session approvals <session>`, answer with `" +
		brand.CLIName + " session answer <session> <requestId> --action accept|decline|cancel`."
}

// terminalInputWait reports whether a signal from a terminal (TUI) session
// says its agent stopped on a prompt: a permission dialog, a hook
// notification or the observer's own reading of the screen. Chat sessions
// report through their chat events instead. A terminal agent that is paused
// on a dialog ends no turn, so without this the orchestrator never hears of it.
func terminalInputWait(s ports.ActivitySignal) bool {
	return s.Valid && s.ControllerGeneration == "" && s.State.NeedsInput() && !strings.HasPrefix(s.Event, "chat.")
}

// terminalInputResolved reports whether a terminal signal shows the agent
// working again, which ends the waiting episode so a later prompt reports anew.
func terminalInputResolved(s ports.ActivitySignal) bool {
	return s.Valid && s.ControllerGeneration == "" && s.State == domain.ActivityActive && !strings.HasPrefix(s.Event, "chat.")
}

// terminalPrompt tells the project's orchestrator, once per waiting episode,
// that a terminal worker stopped on a prompt. Without a live orchestrator it
// sends nothing: the owner already has the immediate Inbox ping, and a report
// no one can take would only resurface later as a stale item.
func (o *Observer) terminalPrompt(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok || (o.OrchestratorLive != nil && !o.OrchestratorLive(ctx, rec.ProjectID, id)) {
		return // not latched: an orchestrator that starts mid-wait still hears of it
	}
	o.mu.Lock()
	t := o.turnForGenerationLocked(id, generation)
	if t.inputReported {
		o.mu.Unlock()
		return
	}
	t.inputReported = true
	o.mu.Unlock()
	kind := KindTerminalWait
	if isPermissionPrompt(s, rec.Harness) {
		kind = KindTerminalPrompt
	}
	what := ""
	if tool := truncate(strings.TrimSpace(s.ToolName), maxToolNameCharacters); tool != "" {
		what = "tool: " + tool
	}
	o.send(id, kind, what)
}

// terminalPromptResolved ends the episode when a terminal signal shows the
// agent working again. A stored blocked state means lifecycle suppressed the
// signal as sibling traffic, so the dialog is still open and stays reported.
func (o *Observer) terminalPromptResolved(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) {
	generation := o.workerGeneration(ctx, id, s)
	if generation == "" {
		return
	}
	if rec, ok, err := o.Sessions.GetSession(ctx, id); err != nil || !ok || rec.Activity.State == domain.ActivityBlocked {
		return
	}
	o.mu.Lock()
	o.turnForGenerationLocked(id, generation).inputReported = false
	o.mu.Unlock()
}

// approvalNotice is the text of an approval's needs-input report. The request id
// is never cut (a cut id matches nothing): it gets its full length first, then
// the reason takes whatever the report limit leaves after the id, the command
// and a reserve for the prefix. An id over maxRequestIDCharacters is not
// printed; the orchestrator is pointed at `session approvals` instead. The id
// comes first and the command last.
func approvalNotice(s ports.ActivitySignal, reason string) string {
	var lines []string
	idLen := 0
	if id := strings.TrimSpace(s.ApprovalRequestID); id != "" {
		if utf8.RuneCountInString(id) <= maxRequestIDCharacters {
			lines = append(lines, "Request id: "+id)
			idLen = utf8.RuneCountInString(id) + len("Request id: \n")
		} else {
			lines = append(lines, "Request id too long to print: list it with `"+brand.CLIName+" session approvals <session>`")
			idLen = 100
		}
	}
	tool := cmpOr(truncate(strings.TrimSpace(s.ToolName), maxToolNameCharacters), "a tool call")
	commandLine := "(no command: open the session to see what it would change)"
	if s.ApprovalCommand != "" {
		commandLine = "Command: " + s.ApprovalCommand
	}
	head := "approval for " + tool + ":"
	room := domain.MaxReportTextCharacters - prefixReserve - idLen - utf8.RuneCountInString(commandLine) - utf8.RuneCountInString(head) - 2
	room = min(room, maxReasonCharacters)
	if room >= minReasonCharacters {
		lines = append(lines, head+" "+cmpOr(truncate(reason, room), "no reason given"))
	} else {
		lines = append(lines, head)
	}
	return strings.Join(append(lines, commandLine), "\n")
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func terminalKind(kind string) bool { return kind == KindTerminalPrompt || kind == KindTerminalWait }

// isPermissionPrompt says whether a terminal wait signal is a permission
// dialog: the permission-request hook, a blocked state, or Kiro's approval
// screen. A waiting_input from anything else may be a question or an expired
// login (Claude's terminal-waiting-input reads both).
func isPermissionPrompt(s ports.ActivitySignal, harness domain.AgentHarness) bool {
	return s.Event == "permission-request" || s.State == domain.ActivityBlocked ||
		(s.Event == "terminal-waiting-input" && harness == domain.HarnessKiro)
}
