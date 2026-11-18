package turnnotify

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatQuietDelay is the quiet window of a synthesized chat turn_ended.
const ChatQuietDelay = 2 * time.Minute

// quietPolicy is the one place a synthesized notice picks its quiet queueing,
// shared by the live and durable (fallback row) paths. Only a plain turn end
// or usage limit is an FYI; a chat one gets the short ChatQuietDelay window
// instead of the report service's default (B255, B738).
func quietPolicy(kind string, rec domain.SessionRecord) (quiet bool, quietFor time.Duration) {
	if kind != KindTurnEnded && kind != KindUsageLimited {
		return false, 0
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return true, ChatQuietDelay
	}
	return true, 0
}
