package lifecycle

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// dropToolFlight forgets a session's tool and permission correlation.
func (m *Manager) dropToolFlight(id domain.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.flights, id)
}

// toolFlight tracks one session's in-flight tool executions and the pending
// permission dialog's identity, so a sticky `blocked` is cleared by the post
// of the exact approved tool — and by nothing else tool-shaped. Answering a
// permission dialog fires no hook of its own, so the approved tool's
// post-tool-use is the earliest observable "the decision was resolved"
// signal; but tool hooks also fire for parallel subagents on the same
// session, whose traffic must never clear a dialog that is still on screen.
// In-memory only: a daemon restart loses it and the session degrades to
// clearing at the next turn boundary — fail-safe staleness, never a spurious
// clear.
type toolFlight struct {
	// inflight maps toolUseID -> toolName for pre-tool-use signals whose post
	// has not arrived yet.
	inflight map[string]string
	// blockedCandidate is the tool-use id of the UNIQUE in-flight tool bearing
	// the dialog's tool_name when it appeared — the tool whose post proves the
	// dialog was answered. Empty when no in-flight tool matched, or when the
	// match was ambiguous (two same-name tools in flight: the permission
	// payload carries no tool_use_id to disambiguate, so a sibling's post must
	// NOT be mistaken for the approval). Either way, empty means nothing
	// tool-shaped may clear the block and it lifts only at a turn boundary.
	blockedCandidate string
	// cursorPending counts native Cursor permission dialogs by execution family
	// and bounded tool name. Cursor does not provide tool-use ids, so a matching
	// after-execution event can clear only its own key, and the session remains
	// blocked until every observed dialog has completed.
	cursorPending map[string]int
}

func cloneToolFlight(flight *toolFlight) *toolFlight {
	if flight == nil {
		return nil
	}
	clone := &toolFlight{
		inflight:         make(map[string]string, len(flight.inflight)),
		blockedCandidate: flight.blockedCandidate,
		cursorPending:    make(map[string]int, len(flight.cursorPending)),
	}
	for id, name := range flight.inflight {
		clone.inflight[id] = name
	}
	for key, count := range flight.cursorPending {
		clone.cursorPending[key] = count
	}
	return clone
}

// restoreToolFlightLocked rolls back the in-memory half of a reducer attempt
// whose durable session projection lost an optimistic revision race. The same
// signal will immediately be reduced again against the winning session row, so
// its tool correlation must also start from the same pre-attempt facts.
func (m *Manager) restoreToolFlightLocked(id domain.SessionID, snapshot *toolFlight) {
	if snapshot == nil {
		delete(m.flights, id)
		return
	}
	m.flights[id] = cloneToolFlight(snapshot)
}

// maxInflightTools caps a session's in-flight map so lost posts cannot grow
// it without bound; hitting the cap resets the map, degrading that session to
// turn-boundary clearing (fail-safe).
const maxInflightTools = 128

// isToolUseEvent reports whether the Tallpine hook event is one of the tool-use
// trio whose signals must not demote a sticky state on their own.
func isToolUseEvent(event string) bool {
	return event == "pre-tool-use" || isPostToolUseEvent(event)
}

func isPostToolUseEvent(event string) bool {
	// post-tool-use-fail is retained for Kimchi hook files installed before the
	// adapter switched to Tallpine's canonical failure event name.
	return event == "post-tool-use" || event == "post-tool-use-failure" || event == "post-tool-use-fail"
}

func cursorBeforeExecutionKey(s ports.ActivitySignal) (string, bool) {
	if s.ToolName == "" {
		return "", false
	}
	switch s.Event {
	case "before-shell-execution":
		return "shell\x00" + s.ToolName, true
	case "before-mcp-execution":
		return "mcp\x00" + s.ToolName, true
	default:
		return "", false
	}
}

func cursorResolvedExecutionKey(s ports.ActivitySignal) (string, bool) {
	if s.ToolName == "" {
		return "", false
	}
	switch s.Event {
	case "after-shell-execution", "cursor-shell-terminal-failure":
		return "shell\x00" + s.ToolName, true
	case "after-mcp-execution", "cursor-mcp-terminal-failure":
		return "mcp\x00" + s.ToolName, true
	default:
		return "", false
	}
}

// isTurnBoundaryEvent reports events that reliably mean the pending dialog is
// gone: a definitive permission reply, a new prompt, or the end of the turn or
// session. Generic active/tool events remain insufficient to clear blocked.
func isTurnBoundaryEvent(event string) bool {
	return event == "user-prompt-submit" || event == "stop" || event == "stop-failure" || event == "interrupt" || event == "session-end" ||
		event == "process-exited" || event == "chat.controller.stopped" || event == "permission-resolved" ||
		event == "liveness-probe"
}

// applyToolPrecedenceLocked folds an event-tagged activity signal through the
// session's tool-flight state and decides whether its state write may
// proceed. Returned signal with Valid=false means "suppressed": the tracking
// side effects have landed but the state must not change. Signals without an
// Event pass through untouched — the compatibility contract for old CLIs and
// for adapters that don't tag their signals (their last-writer-wins semantics
// are pinned by tests). Caller must hold m.mu.
func (m *Manager) applyToolPrecedenceLocked(id domain.SessionID, cur domain.ActivityState, s ports.ActivitySignal) ports.ActivitySignal {
	if s.Event == "" {
		return s
	}
	suppressed := s
	suppressed.Valid = false

	fl := m.flights[id]
	ensure := func() *toolFlight {
		if fl == nil {
			fl = &toolFlight{inflight: map[string]string{}}
			m.flights[id] = fl
		}
		return fl
	}

	// Tracking side effects happen regardless of what the state decision is.
	switch s.Event {
	case "pre-tool-use":
		if s.ToolUseID != "" {
			f := ensure()
			if len(f.inflight) >= maxInflightTools {
				f.inflight = map[string]string{}
			}
			f.inflight[s.ToolUseID] = s.ToolName
		}
	case "post-tool-use", "post-tool-use-failure", "post-tool-use-fail":
		if fl != nil {
			delete(fl.inflight, s.ToolUseID)
		}
	}

	switch {
	case s.State == domain.ActivityBlocked:
		if key, ok := cursorBeforeExecutionKey(s); ok {
			f := ensure()
			f.blockedCandidate = ""
			if f.cursorPending == nil {
				f.cursorPending = map[string]int{}
			}
			f.cursorPending[key]++
			return s
		}
		// Entering (or re-asserting) blocked: snapshot the dialog's identity.
		// permission-request carries the blocking tool_name; the Notification
		// duplicate does not and must not wipe an existing snapshot.
		//
		// The permission hook payload does not carry the blocking tool's
		// tool_use_id (only its name), so we can only identify the blocking
		// tool unambiguously when EXACTLY ONE in-flight tool bears that name.
		// With two same-name tools in flight (a batch of Bash calls, one of
		// them the one at the dialog), a sibling's post could otherwise clear
		// a still-open dialog — the exact permission-bypass this change exists
		// to prevent. So we correlate only in the unique case; otherwise no
		// candidate is recorded and the block clears only at a turn boundary
		// (fail-closed).
		f := ensure()
		f.cursorPending = nil
		// Recompute only when this signal identifies a dialog. Claude can emit an
		// identity-less Notification duplicate after permission-request; that
		// duplicate must not erase the candidate captured by the first signal.
		if s.ToolUseID != "" || s.ToolName != "" {
			f.blockedCandidate = ""
		}
		if s.ToolUseID != "" {
			// If the blocking signal carries a tool_use_id that is in the
			// inflight map, use it directly — this is more precise than a
			// name match and handles adapters whose notification payloads
			// use a different tool_name casing than their PreToolUse/PostToolUse
			// payloads (e.g. Kimchi: "bash" in notification vs "Bash" in hooks).
			if _, ok := f.inflight[s.ToolUseID]; ok {
				f.blockedCandidate = s.ToolUseID
			}
		}
		if f.blockedCandidate == "" && s.ToolName != "" {
			for useID, name := range f.inflight {
				if name != s.ToolName {
					continue
				}
				if f.blockedCandidate != "" {
					// A second same-name tool: ambiguous, fail closed by
					// leaving no candidate (only a turn boundary clears).
					f.blockedCandidate = ""
					break
				}
				f.blockedCandidate = useID
			}
		}
		return s

	case cur == domain.ActivityBlocked:
		// Paused on a decision: only a turn boundary or the correlated post
		// may change the state.
		switch {
		case isTurnBoundaryEvent(s.Event):
			delete(m.flights, id)
			return s
		default:
			if fl != nil {
				if key, ok := cursorResolvedExecutionKey(s); ok && fl.cursorPending[key] > 0 {
					if fl.cursorPending[key] == 1 {
						delete(fl.cursorPending, key)
					} else {
						fl.cursorPending[key]--
					}
					if len(fl.cursorPending) == 0 {
						delete(m.flights, id)
						return s
					}
					return suppressed
				}
				if isPostToolUseEvent(s.Event) && fl.blockedCandidate != "" && s.ToolUseID == fl.blockedCandidate {
					// The single unambiguous blocking tool finished: the dialog was
					// answered. Clear the candidate so a later dialog in the same turn
					// starts from a clean slate.
					fl.blockedCandidate = ""
					return s
				}
			}
			// Subagent/sibling tool traffic (including a same-name sibling when
			// the block was ambiguous), notification sub-types (idle_prompt,
			// agent_completed), and anything else that is not proof the dialog
			// closed.
			return suppressed
		}

	case cur.IsSticky() && isToolUseEvent(s.Event):
		// waiting_input: background tool traffic must not clear the "waiting
		// on the user" marker; only an explicit user/turn signal does.
		return suppressed

	default:
		if isTurnBoundaryEvent(s.Event) {
			delete(m.flights, id)
		}
		return s
	}
}
