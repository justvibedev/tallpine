package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// EndedLaneOrchestrators returns the orchestrators whose lane has ended (the
// session_lane_end_on_terminate trigger records them). They stay in session
// history but are not listed as orchestrators. Raw SQL: see EndLane.
func (s *Store) EndedLaneOrchestrators(ctx context.Context) (map[domain.SessionID]bool, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT session_id FROM ended_lane_orchestrators`)
	if err != nil {
		return nil, fmt.Errorf("list ended lane orchestrators: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[domain.SessionID]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list ended lane orchestrators: %w", err)
		}
		out[domain.SessionID(id)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list ended lane orchestrators: %w", err)
	}
	return out, nil
}

// HoldLane keeps a lane open while a clean replacement retires its
// orchestrator: the session_lane_end_on_terminate trigger skips held lanes.
func (s *Store) HoldLane(ctx context.Context, project domain.ProjectID, lane string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `INSERT OR IGNORE INTO lane_replacements (project_id, lane_item_id) VALUES (?, ?)`, project, lane); err != nil {
		return fmt.Errorf("hold lane %s: %w", lane, err)
	}
	return nil
}

// ReleaseLane drops a HoldLane hold and, when the lane has no live
// orchestrator (the replacement failed), ends it the way the trigger does.
func (s *Store) ReleaseLane(ctx context.Context, project domain.ProjectID, lane string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("release lane: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM lane_replacements WHERE project_id = ? AND lane_item_id = ?`, project, lane); err != nil {
		return fmt.Errorf("release lane %s: %w", lane, err)
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_lane l JOIN sessions s ON s.id = l.session_id
		WHERE s.project_id = ? AND l.lane_item_id = ? AND s.kind = 'orchestrator' AND NOT s.is_terminated`, project, lane).Scan(&live); err != nil {
		return fmt.Errorf("release lane %s: %w", lane, err)
	}
	if live == 0 {
		const laneSessions = `SELECT l.session_id FROM session_lane l JOIN sessions s ON s.id = l.session_id
			WHERE s.project_id = ? AND l.lane_item_id = ?`
		for _, q := range []string{
			`INSERT OR IGNORE INTO ended_lane_orchestrators (session_id)
				SELECT id FROM sessions WHERE kind = 'orchestrator' AND id IN (` + laneSessions + `)`,
			`UPDATE reports SET lane_item_id = '' WHERE project_id = ? AND lane_item_id = ? AND delivery_state IN ('pending', 'claimed')`,
			`DELETE FROM session_lane WHERE session_id IN (` + laneSessions + `)`,
		} {
			if _, err := tx.ExecContext(ctx, q, project, lane); err != nil {
				return fmt.Errorf("release lane %s: %w", lane, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("release lane: commit: %w", err)
	}
	return nil
}

// ReleaseStaleLaneHolds releases every lane hold, ending held lanes that have
// no live orchestrator. Run at daemon start: no replacement is in flight then,
// so a hold left by a crash mid-replacement would otherwise keep its lane from
// ever ending.
func (s *Store) ReleaseStaleLaneHolds(ctx context.Context) error {
	holds, err := s.listLaneHolds(ctx)
	if err != nil {
		return err
	}
	for _, h := range holds {
		if err := s.ReleaseLane(ctx, h.project, h.lane); err != nil {
			return err
		}
	}
	return nil
}

type laneHold struct {
	project domain.ProjectID
	lane    string
}

func (s *Store) listLaneHolds(ctx context.Context) ([]laneHold, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT project_id, lane_item_id FROM lane_replacements`)
	if err != nil {
		return nil, fmt.Errorf("list lane holds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var holds []laneHold
	for rows.Next() {
		var h laneHold
		if err := rows.Scan(&h.project, &h.lane); err != nil {
			return nil, fmt.Errorf("list lane holds: %w", err)
		}
		holds = append(holds, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list lane holds: %w", err)
	}
	return holds, nil
}
