package store

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// EndLane removes a lane orchestrator's session row and moves everything else
// in the lane to the project's default (un-laned) orchestrator, in one
// transaction: the lane's workers lose their lane binding and the lane's
// pending reports are re-filed under the default lane. Rows that cascade from
// the session (its lane binding, conversation, transitions) go with it. The
// change_log rows naming the session are dropped first because that reference
// does not cascade. Raw statements, not sqlc: see DeleteSession.
func (s *Store) EndLane(ctx context.Context, project domain.ProjectID, lane string, orchestrator domain.SessionID) error {
	if lane == "" {
		return fmt.Errorf("end lane: lane is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("end lane: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmts := []struct {
		query string
		args  []any
	}{
		{`UPDATE session_lane SET lane_item_id = '' WHERE lane_item_id = ? AND session_id <> ?
			AND session_id IN (SELECT id FROM sessions WHERE project_id = ?)`, []any{lane, orchestrator, project}},
		{`UPDATE reports SET lane_item_id = '' WHERE project_id = ? AND lane_item_id = ? AND delivery_state IN ('pending', 'claimed')`, []any{project, lane}},
		{`DELETE FROM change_log WHERE session_id = ?`, []any{orchestrator}},
		// Its change_log rows go with it, so clients get a project-level event
		// (no session id: they refetch the whole list) that the session is gone.
		{`INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
			VALUES (?, NULL, 'session_updated', json_object('removed', ?, 'reason', 'lane_ended'), CURRENT_TIMESTAMP)`, []any{project, orchestrator}},
		{`DELETE FROM sessions WHERE id = ? AND project_id = ? AND is_terminated = 1`, []any{orchestrator, project}},
	}
	for _, st := range stmts {
		res, err := tx.ExecContext(ctx, st.query, st.args...)
		if err != nil {
			return fmt.Errorf("end lane %s: %w", lane, err)
		}
		if n, _ := res.RowsAffected(); n == 0 && strings.HasPrefix(st.query, "DELETE FROM sessions") {
			return fmt.Errorf("end lane %s: orchestrator %s is not a terminated session of project %s", lane, orchestrator, project)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("end lane: commit: %w", err)
	}
	return nil
}
