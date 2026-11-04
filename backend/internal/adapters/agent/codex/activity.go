package codex

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// DeriveActivityState maps a Codex hook event onto a Tallpine activity state. The
// bool is false when the event carries no activity signal.
//
// event is the Tallpine hook sub-command name installed in codexManagedHooks
// ("user-prompt-submit", "permission-request", "stop", ...), not the native
// Codex event name. Codex has no Notification or StopFailure hook (harness limit);
// SessionEnd is not guaranteed on a crash, so the process supervisor stays the
// exit backstop.
func DeriveActivityState(event string, payload []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit":
		return domain.ActivityActive, true
	case "permission-request":
		// waiting_input, not blocked: Codex's only PostToolUse hook tracks
		// successful subagent spawns, not approval resolution. A blocked
		// state could therefore remain stuck until the turn ends.
		return domain.ActivityWaitingInput, true
	case "pre-tool-use":
		// Tool traffic keeps the agent active. It never clears a sticky
		// waiting_input (lifecycle suppresses it); only a prompt or turn
		// boundary does.
		return domain.ActivityActive, true
	case "pre-compact", "post-compact":
		// Subscribed but carry no state: a manual /compact fires no Stop, so
		// mapping either to active could leave an idle session stuck active.
		return "", false
	case "session-end":
		// clear/resume keep the same Tallpine session alive.
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(payload, &p)
		if p.Reason == "clear" || p.Reason == "resume" {
			return "", false
		}
		return domain.ActivityExited, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
