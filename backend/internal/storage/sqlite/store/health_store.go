package store

import (
	"context"
	"database/sql"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Read-only facts behind the silent-failure health summary. They run on the
// reader pool and never repair or retire anything, so polling them is safe.

// ListChatSessionsExpectedLive returns Chat sessions that should currently own
// a provider controller: started, not asleep, not terminated, with an
// established provider conversation. Rows younger than settledBefore are still
// starting and are skipped.
func (s *Store) ListChatSessionsExpectedLive(ctx context.Context, settledBefore time.Time) ([]domain.SessionID, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id FROM sessions
WHERE session_mode = 'chat' AND is_terminated = 0 AND is_task_preparation = 0
    AND provision_state = 'ready' AND hibernated_at IS NULL
    AND trim(provider_conversation_id) <> '' AND updated_at <= ?
ORDER BY id`, settledBefore.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SessionID
	for rows.Next() {
		var id domain.SessionID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListStuckReportBatches groups pending reports available before deadline and
// claimed reports claimed before deadline by project.
func (s *Store) ListStuckReportBatches(ctx context.Context, deadline time.Time) ([]domain.StuckReportBatch, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT project_id, delivery_state, available_at, claimed_at FROM reports
WHERE (delivery_state = 'pending' AND available_at <= ?)
   OR (delivery_state = 'claimed' AND claimed_at <= ?)
ORDER BY project_id, created_at, id`, deadline.UTC(), deadline.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.StuckReportBatch
	index := map[domain.ProjectID]int{}
	for rows.Next() {
		var (
			project   domain.ProjectID
			state     string
			available time.Time
			claimed   sql.NullTime
		)
		if err := rows.Scan(&project, &state, &available, &claimed); err != nil {
			return nil, err
		}
		i, ok := index[project]
		if !ok {
			i = len(out)
			index[project] = i
			out = append(out, domain.StuckReportBatch{ProjectID: project})
		}
		since := available
		if state == "claimed" {
			out[i].Claimed++
			since = claimed.Time
		} else {
			out[i].Pending++
		}
		if out[i].Since.IsZero() || since.Before(out[i].Since) {
			out[i].Since = since
		}
	}
	return out, rows.Err()
}

// ListOverdueTurnFallbacks returns pending fallbacks due at or before deadline.
// Unlike ListPendingTurnFallbacks it does not retire fenced intents.
func (s *Store) ListOverdueTurnFallbacks(ctx context.Context, deadline time.Time) ([]domain.OverdueTurnFallback, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT id, session_id, due_at FROM turn_fallbacks
WHERE disposition = 'pending' AND due_at <= ? ORDER BY due_at, id`, deadline.UTC())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.OverdueTurnFallback
	for rows.Next() {
		var f domain.OverdueTurnFallback
		if err := rows.Scan(&f.ID, &f.SessionID, &f.DueAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
