package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// standaloneSessionIDPrefix is the id prefix of sessions with no project.
const standaloneSessionIDPrefix = "standalone"

// DeleteTerminatedSession removes a terminated session's row; its own
// conversations, cleanup facts, PR facts and the other per-session rows go
// with it through ON DELETE CASCADE. Callers decide whether the session's
// workspace is released first; this only refuses live sessions and, with
// domain.ErrSessionHistoryShared, shared conversation history, all in one
// transaction so a concurrent restore cannot slip between the checks and the
// delete.
//
// The session's number is recorded in session_num_floors so the next spawn
// never reuses its id (or its branch name). Its usage events cascade away with
// their usage_bindings, so their spend is first folded into
// deleted_session_account_usage, which keeps it in the agent account usage
// view. Its change_log rows are dropped because change_log references
// sessions without ON DELETE CASCADE; the sessions_cdc_delete trigger adds one
// project-level session_updated event in their place.
//
// Returns deleted=false when id does not name a terminated session.
func (s *Store) DeleteTerminatedSession(ctx context.Context, id domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("delete session %s: begin: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	var projectID sql.NullString
	var num int64
	err = tx.QueryRowContext(ctx, `SELECT project_id, num FROM sessions WHERE id = ? AND is_terminated = 1`, id).Scan(&projectID, &num)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete session %s: read: %w", id, err)
	}
	var shared bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM conversation_turns t
    JOIN conversations c ON c.id = t.conversation_id
    WHERE t.handled_by_session_id = ?1
      AND (c.session_id IS NULL OR c.session_id <> ?1)
) OR EXISTS (
    SELECT 1 FROM conversations
    WHERE current_session_id = ?1
      AND (session_id IS NULL OR session_id <> ?1)
)`, id).Scan(&shared); err != nil {
		return false, fmt.Errorf("delete session %s: check shared history: %w", id, err)
	}
	if shared {
		return false, fmt.Errorf("delete session %s: %w", id, domain.ErrSessionHistoryShared)
	}
	prefix := standaloneSessionIDPrefix
	if projectID.Valid && projectID.String != "" {
		prefix = projectID.String
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO session_num_floors (id_prefix, max_num) VALUES (?, ?)
ON CONFLICT (id_prefix) DO UPDATE SET max_num = MAX(max_num, excluded.max_num)`, prefix, num); err != nil {
		return false, fmt.Errorf("delete session %s: record number floor: %w", id, err)
	}
	if err := foldDeletedSessionAccountUsage(ctx, s.qw.WithTx(tx), id); err != nil {
		return false, fmt.Errorf("delete session %s: keep account usage: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_log WHERE session_id = ?`, id); err != nil {
		return false, fmt.Errorf("delete session %s: clear change log: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND is_terminated = 1`, id)
	if err != nil {
		return false, fmt.Errorf("delete session %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("delete session %s: rows affected: %w", id, err)
	} else if n == 0 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("delete session %s: commit: %w", id, err)
	}
	return true, nil
}

// foldDeletedSessionAccountUsage adds a session's usage, attributed to its
// accounts exactly as AgentAccountUsageSince does, to the deleted-session
// totals. It runs inside the delete's transaction, before the session row
// (and with it the usage events and account bindings) cascades away.
func foldDeletedSessionAccountUsage(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	rows, err := q.AgentAccountUsageSince(ctx, gen.AgentAccountUsageSinceParams{
		Since: sql.NullTime{Time: time.Time{}, Valid: true}, SessionID: string(id),
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := q.AddDeletedSessionAccountUsage(ctx, gen.AddDeletedSessionAccountUsageParams(row)); err != nil {
			return err
		}
	}
	return nil
}

// sessionNumFloor is the highest number a deleted session under prefix held.
// Callers hold writeMu.
func (s *Store) sessionNumFloor(ctx context.Context, prefix string) (int64, error) {
	var floor int64
	err := s.writeDB.QueryRowContext(ctx, `SELECT max_num FROM session_num_floors WHERE id_prefix = ?`, prefix).Scan(&floor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return floor, err
}
