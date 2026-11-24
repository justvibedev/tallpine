package turnnotify

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ChatQuietDelay is the quiet window of a synthesized chat turn_ended.
const ChatQuietDelay = 2 * time.Minute

// quietPolicy is the one place a synthesized notice picks its quiet queueing,
// shared by the live and durable (fallback row) paths. Only a plain turn end
// or usage limit is an FYI; a chat one gets the short ChatQuietDelay window
// instead of the report service's default (B255, B738). A chat turn_ended
// whose orchestrator is idle and waiting on this worker is not held at all
// (Q23): the hold only spares a busy orchestrator, or one not waiting on the
// worker, a wake per turn.
func (o *Observer) quietPolicy(ctx context.Context, kind string, rec domain.SessionRecord) (quiet bool, quietFor time.Duration) {
	if kind != KindTurnEnded && kind != KindUsageLimited {
		return false, 0
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return true, 0
	}
	if kind == KindTurnEnded && o.OrchestratorAwaits != nil && o.OrchestratorAwaits(ctx, rec) {
		return false, 0
	}
	return true, ChatQuietDelay
}
