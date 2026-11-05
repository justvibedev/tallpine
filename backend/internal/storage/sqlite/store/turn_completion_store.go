package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// LastCompletedTurnAt is when the session's controller last ran a turn to a
// normal end (state completed). Activity timestamps move on any signal; this
// moves only when a turn finishes. ok is false when the session has none.
func (s *Store) LastCompletedTurnAt(ctx context.Context, id domain.SessionID) (time.Time, bool, error) {
	row, err := s.qr.SelectLatestCompletedTurnBySession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("select last completed turn for %s: %w", id, err)
	}
	turn := turnToDomain(row)
	if turn.CompletedAt == nil {
		return time.Time{}, false, nil
	}
	return turn.CompletedAt.UTC(), true, nil
}
