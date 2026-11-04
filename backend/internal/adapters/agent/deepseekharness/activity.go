package deepseekharness

import "tallpine.eu/tallpine/backend/internal/domain"

// DeriveActivityState maps a `tallpine hooks deepseek-harness <event>` token onto
// a Tallpine activity state. Events are the generated hooks.json tokens (see
// hookEvents). Subagent events carry no activity of their own: the CLI reports
// them as subagent facts so a parent turn is not ended by a child's stop.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	// session-start carries no activity: it only reports the native session id,
	// and an idle there would read as a finished turn before the task starts.
	case "user-prompt-submit", "pre-tool-use", "post-tool-use":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
