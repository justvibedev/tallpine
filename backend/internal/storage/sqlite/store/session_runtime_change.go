package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// NotifySessionRuntimeChanged appends an id-only session_updated event for a
// live session whose runtime facts changed outside its row, such as the
// harness background tasks the daemon tracks in memory. No column changes,
// so the row triggers cannot announce it; clients re-read the one session.
// Raw statement, not sqlc: see EndLane.
func (s *Store) NotifySessionRuntimeChanged(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
		SELECT project_id, id, 'session_updated', json_object('id', id), CURRENT_TIMESTAMP
		FROM sessions WHERE id = ? AND is_terminated = 0`, string(id)); err != nil {
		return fmt.Errorf("notify session %s runtime change: %w", id, err)
	}
	return nil
}
