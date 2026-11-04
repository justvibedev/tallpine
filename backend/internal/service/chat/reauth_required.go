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
	account, err := controller.store.ConversationAccount(ctx, controller.conversation.ID)
	return err == nil && account != nil && account.ReauthRequiredAt != nil
}
