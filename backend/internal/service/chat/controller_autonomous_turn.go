package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// autonomousTurnTaskType is the async task Claude Code's bridge publishes for a
// model turn the harness starts with no prompt open, for example to answer a
// finished background command (see bgtasks).
const autonomousTurnTaskType = "turn"

// noteAutonomousTurn mirrors a harness-started turn into the activity pipeline.
// Such a turn has no provider turn start or completion event (ACP v1 has no
// session state), so the session stayed idle while it worked and, when it
// ended, no turn_ended report was ever filed: a worker woken by its background
// command answered into its own chat and the orchestrator never heard (B440).
// The task's spawn and end frames stand in for the lifecycle signals a
// prompted turn sends. Runs after projection, on the projection goroutine.
func (c *Controller) noteAutonomousTurn(ctx context.Context, task *ports.ChatAsyncTask, now time.Time) {
	if task == nil || task.ID == "" || c.reviewID != "" {
		return
	}
	c.mu.Lock()
	known := c.autonomousTurns[task.ID]
	switch {
	case !known && task.TaskType == autonomousTurnTaskType && !task.Done():
		if c.autonomousTurns == nil {
			c.autonomousTurns = map[string]bool{}
		}
		c.autonomousTurns[task.ID] = true
	case known && task.Done():
		delete(c.autonomousTurns, task.ID)
	default:
		c.mu.Unlock()
		return
	}
	// A prompted turn owns the activity state and its own lifecycle signals.
	promptedTurn := c.pendingTurnID != ""
	othersOpen := len(c.autonomousTurns) > 0
	c.mu.Unlock()
	if promptedTurn {
		return
	}
	if !known {
		// The previous prompted turn's completed row would otherwise still own
		// the durable notification and swallow this turn's end (turnnotify).
		if err := c.store.SupersedeCompletedTurnFallbacks(ctx, c.sessionID); err != nil {
			c.log.Warn("autonomous turn: supersede completed fallbacks failed", "session", c.sessionID, "error", err)
		}
		c.reportActivity(ctx, domain.ActivityActive, "chat.turn.started", now)
		return
	}
	if othersOpen {
		return
	}
	c.reportActivitySignal(ctx, ports.ActivitySignal{
		State: domain.ActivityIdle, Event: "chat.turn.completed", Timestamp: now,
		FinalAssistantMessage: c.finalAssistant.take(""),
	})
}
