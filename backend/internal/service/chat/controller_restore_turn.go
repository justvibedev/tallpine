package chat

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// restoreLiveTurnOwnership rebuilds the volatile busy gate from durable facts
// before a replacement daemon publishes a reconnected controller. The provider
// kept running while Tallpine was detached, so forgetting this turn would let a new
// Send start a second root turn on the same native conversation.
func (c *Controller) restoreLiveTurnOwnership(turns []domain.ConversationTurn) string {
	latest := liveTurnToRestore(turns)
	if latest == nil {
		return ""
	}
	c.pendingTurnID = latest.ProviderTurnID
	c.ackedTurnID = latest.ProviderTurnID
	c.state = ports.ChatControllerBusy
	return latest.ProviderTurnID
}

// Share the exact selection with the durable generation claim so report
// suppression belongs to the turn this controller actually reclaims.
func liveTurnToRestore(turns []domain.ConversationTurn) *domain.ConversationTurn {
	var latest *domain.ConversationTurn
	for i := range turns {
		turn := &turns[i]
		if turn.State != domain.TurnStateRunning || turn.ProviderTurnID == "" || turn.RolledBackAt != nil {
			continue
		}
		if latest == nil || turn.RequestedAt.After(latest.RequestedAt) {
			latest = turn
		}
	}
	return latest
}
