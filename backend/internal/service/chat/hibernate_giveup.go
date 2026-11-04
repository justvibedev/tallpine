package chat

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// giveUpWake fails the queued messages once the retries are spent. The sleep
// marker kept for the retries is cleared first, so the session shows as
// stopped with its Resume action instead of hiding the failure.
func (s *Service) giveUpWake(ctx context.Context, id domain.SessionID, message string) {
	if !s.HasLiveChatController(id) {
		if err := s.clearHibernation(ctx, id); err != nil {
			s.log.Warn("could not clear the sleep marker of a chat session that would not wake", "session", id, "err", err)
		}
	}
	s.failQueuedTurns(ctx, id, message)
}
