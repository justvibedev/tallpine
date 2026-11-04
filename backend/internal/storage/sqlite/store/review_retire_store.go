package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ListSessionsWithIdleTerminalReviewers returns workers whose terminal reviewer
// pane has no running pass and no activity since updatedBefore.
func (s *Store) ListSessionsWithIdleTerminalReviewers(ctx context.Context, updatedBefore time.Time) ([]domain.SessionID, error) {
	ids, err := s.qr.ListSessionsWithIdleTerminalReviewers(ctx, updatedBefore)
	if err != nil {
		return nil, fmt.Errorf("list sessions with idle terminal reviewers: %w", err)
	}
	return ids, nil
}
