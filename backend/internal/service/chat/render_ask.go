package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// RecordAsk shows a tracked owner ask inline on the turn the session is
// running, as a system activity with the "ask" discriminator (the strip's jump
// link scrolls to it). It returns the provider turn and the activity id, and
// ErrNoActiveTurn when no turn is in flight: the ask then has no anchor.
func (s *Service) RecordAsk(ctx context.Context, id domain.SessionID, ask domain.OwnerAsk) (string, string, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return "", "", err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return "", "", err
	}
	return controller.recordAsk(ctx, ask)
}

func (c *Controller) recordAsk(ctx context.Context, ask domain.OwnerAsk) (string, string, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	providerTurnID, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return "", "", ErrNoActiveTurn
	}
	detail, err := json.Marshal(map[string]any{"event": "ask", "ask": map[string]any{"id": ask.ID, "ref": ask.Ref}})
	if err != nil {
		return "", "", fmt.Errorf("encode ask detail: %w", err)
	}
	activityID := c.newID()
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID, domain.ConversationActivity{
		ID:             activityID,
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        ask.Ref + ": " + ask.Text,
		Detail:         detail,
		ProviderItemID: "ask:" + ask.ID,
	}, c.now()); err != nil {
		return "", "", fmt.Errorf("record ask on turn %s: %w", providerTurnID, err)
	}
	return providerTurnID, activityID, nil
}
