package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ReauthRequired reports whether a live session's provider refused its login
// and the session waits for the user to sign in again. It reads the durable
// conversation account, so it survives a restart; recovery (a completed turn)
// clears it. A session with no running controller reports false.
func (s *Service) ReauthRequired(ctx context.Context, id domain.SessionID) bool {
	controller, err := s.Controller(id)
	if err != nil || controller == nil {
		return false
	}
	return controller.reauthRequired(ctx)
}

// reauthRequired reports a provider login refusal on the durable conversation
// account that no completed turn has cleared yet.
func (c *Controller) reauthRequired(ctx context.Context) bool {
	account, err := c.store.ConversationAccount(ctx, c.conversation.ID)
	return err == nil && account != nil && account.ReauthRequiredAt != nil
}
