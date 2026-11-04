package chat

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// UsageLimitPausedUntil reports when a live session whose provider quota is
// exhausted may start turns again. It reads the durable turn, so it survives a
// restart; a session with no running controller reports no pause.
func (s *Service) UsageLimitPausedUntil(ctx context.Context, id domain.SessionID) (time.Time, bool) {
	controller, err := s.Controller(id)
	if err != nil || controller == nil {
		return time.Time{}, false
	}
	return controller.usageLimitPausedUntil(ctx)
}
