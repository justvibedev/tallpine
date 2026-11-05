package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ---- sessions ----

// CreateSession assigns a per-project identity or a global standalone identity
// and inserts the record. The next-num read and insert share writeMu, so two
// concurrent creates cannot collide.
func (s *Store) CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	created, _, err := s.createSessionLocked(ctx, rec)
	return created, err
}

// CreateClientRequestSession atomically decides which session owns a request
// key. The caller launches only when fresh is true.
func (s *Store) CreateClientRequestSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	if rec.ClientRequestID == "" || rec.ClientRequestHash == "" {
		return domain.SessionRecord{}, false, fmt.Errorf("client request id and hash are required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.createSessionLocked(ctx, rec)
}

// GetSessionByClientRequestID finds the session created for a retryable request.
func (s *Store) GetSessionByClientRequestID(ctx context.Context, key string) (domain.SessionRecord, bool, error) {
	return s.getSessionByClientRequestID(ctx, s.qr, key)
}

func (s *Store) getSessionByClientRequestID(ctx context.Context, q *gen.Queries, key string) (domain.SessionRecord, bool, error) {
	row, err := q.GetClientRequestSession(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("find client request %s: %w", key, err)
	}
	session, err := q.GetSession(ctx, row.ID)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("load client request session %s: %w", row.ID, err)
	}
	rec := rowToRecord(session)
	rec.ClientRequestID = key
	rec.ClientRequestHash = row.ClientRequestHash
	rec.ClientRequestCommitted = row.ClientRequestCommitted
	return rec, true, nil
}

// CommitClientRequestSession marks a successfully launched session replayable.
func (s *Store) CommitClientRequestSession(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.CommitClientRequestSession(ctx, id)
	if err != nil {
		return fmt.Errorf("commit client request session %s: %w", id, err)
	}
	if rows != 1 {
		return fmt.Errorf("client request session %s is missing", id)
	}
	return nil
}

// CreateAutomationSession reports whether it inserted the seed. Callers must
// not continue launching when fresh=false unless the returned row carries the
// durable launch-complete marker.
func (s *Store) CreateAutomationSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	if rec.AutomationRunID == nil {
		return domain.SessionRecord{}, false, fmt.Errorf("automation run id is required")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.createSessionLocked(ctx, rec)
}

func (s *Store) createSessionLocked(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	if rec.ClientRequestID != "" {
		existing, found, err := s.getSessionByClientRequestID(ctx, s.qw, rec.ClientRequestID)
		if err != nil || found {
			return existing, false, err
		}
	}
	if rec.AutomationRunID != nil {
		existing, err := s.qw.GetSessionByAutomationRunID(ctx, rec.AutomationRunID)
		if err == nil {
			return rowToRecord(gen.GetSessionRow(existing)), false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.SessionRecord{}, false, fmt.Errorf("find session for automation run %s: %w", *rec.AutomationRunID, err)
		}
	}

	var num int64
	var err error
	prefix := string(rec.ProjectID)
	if rec.ProjectID == "" {
		num, err = s.qw.NextStandaloneSessionNum(ctx)
		prefix = standaloneSessionIDPrefix
	} else {
		num, err = s.qw.NextSessionNum(ctx, optionalProjectID(rec.ProjectID))
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("next session num for %s: %w", rec.ProjectID, err)
	}
	// A deleted session's number (and so its id and branch) is never reused.
	floor, err := s.sessionNumFloor(ctx, prefix)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("session num floor for %s: %w", rec.ProjectID, err)
	}
	if num <= floor {
		num = floor + 1
	}
	for {
		rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", prefix, num))
		exists, err := s.qw.SessionIDExists(ctx, rec.ID)
		if err != nil {
			return domain.SessionRecord{}, false, fmt.Errorf("check session id %s: %w", rec.ID, err)
		}
		if !exists {
			break
		}
		num++
	}
	if err := s.insertSessionWithLane(ctx, rec, num); err != nil {
		if rec.ClientRequestID != "" {
			existing, found, reloadErr := s.getSessionByClientRequestID(ctx, s.qw, rec.ClientRequestID)
			if reloadErr == nil && found {
				return existing, false, nil
			}
		}
		if rec.AutomationRunID != nil {
			existing, reloadErr := s.qw.GetSessionByAutomationRunID(ctx, rec.AutomationRunID)
			if reloadErr == nil {
				return rowToRecord(gen.GetSessionRow(existing)), false, nil
			}
		}
		return domain.SessionRecord{}, false, fmt.Errorf("insert session %s: %w", rec.ID, err)
	}
	return rec, true, nil
}

// insertSessionWithLane inserts the session row and, when rec.Lane carries a
// binding, its session_lane row in one transaction, so no reader sees a laned
// session without its lane.
func (s *Store) insertSessionWithLane(ctx context.Context, rec domain.SessionRecord, num int64) error {
	if rec.Lane == (domain.SessionLane{}) {
		return s.qw.InsertSession(ctx, recordToInsert(rec, num))
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	if err := q.InsertSession(ctx, recordToInsert(rec, num)); err != nil {
		return err
	}
	if err := q.SetSessionLane(ctx, gen.SetSessionLaneParams{
		SessionID: string(rec.ID), LaneItemID: rec.Lane.LaneItemID, ParentSessionID: string(rec.Lane.ParentSessionID),
	}); err != nil {
		return fmt.Errorf("set lane for %s: %w", rec.ID, err)
	}
	return tx.Commit()
}

// DeleteSession removes a session row, but only if it is still in seed state
// (no workspace, no runtime handle, no agent session id, no prompt, no handoff
// metadata, and not already terminated). Rows that have observable spawn output are immutable
// to preserve the no-resurrection guarantee — for those, callers fall back to
// MarkTerminated (lifecycle.Manager) instead.
//
// The deletion runs in a transaction. It first probes seed state with
// SessionIsSeed; only if that returns true does it clear the session's
// change_log rows (required because change_log FKs sessions(id) without
// ON DELETE CASCADE) and then delete the session row. For live or absent
// sessions the transaction commits with no rows touched — critically, the
// session_created / session_updated CDC events for live sessions are NOT
// destroyed when callers (e.g. RollbackSpawn's delete-then-kill fallback)
// invoke DeleteSession on a fully-spawned row.
//
// Returns deleted=true when a seed row was removed; deleted=false when the
// session id did not match a seed row (either it never existed, or it had
// already progressed past seed state). The latter case is benign — the caller
// should fall back to MarkTerminated.
func (s *Store) DeleteSession(ctx context.Context, id domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete seed session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)

	isSeed, err := q.SessionIsSeed(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete seed session: probe seed state for %s: %w", id, err)
	}
	if !isSeed {
		// Commit the empty tx so we don't leak a transaction. Critically, do
		// NOT touch change_log here — for a live session that contains real
		// session_created / session_updated CDC events.
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("delete seed session: commit no-op: %w", err)
		}
		return false, nil
	}

	// Drop change_log rows for this session id first so the FK doesn't reject
	// the session DELETE. We do not touch project-level events (session_id IS
	// NULL) — those belong to the project, not this session. Both this DELETE
	// and the session DELETE below run via raw ExecContext to sidestep sqlc
	// 1.31's SQLite-parser bug, which strips trailing `?` placeholders and
	// string literals from DELETE statements (see queries/changelog.sql and
	// queries/sessions.sql for the documented workaround context).
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_log WHERE session_id = ?`, id); err != nil {
		return false, fmt.Errorf("delete seed session: clear change log for %s: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `
DELETE FROM sessions
WHERE id = ?
  AND is_terminated = 0
  AND workspace_path = ''
  AND runtime_handle_id = ''
  AND agent_session_id = ''
  AND prompt = ''
  AND latest_user_prompt = ''
  AND latest_user_prompt_at IS NULL
  AND latest_assistant_update = ''
  AND native_transcript_path = ''`, id)
	if err != nil {
		return false, fmt.Errorf("delete seed session %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete seed session %s: rows affected: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("delete seed session: commit: %w", err)
	}
	return n > 0, nil
}

// GetSession returns the full record for a session, or ok=false if absent.
func (s *Store) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	row, err := s.qr.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session %s: %w", id, err)
	}
	return getSessionRowToRecord(row), true, nil
}

// GetSessionByAutomationRunID returns the unique session spawned for a durable
// automation occurrence, if one exists.
func (s *Store) GetSessionByAutomationRunID(ctx context.Context, id domain.AutomationRunID) (domain.SessionRecord, bool, error) {
	row, err := s.qr.GetSessionByAutomationRunID(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get automation session %s: %w", id, err)
	}
	return rowToRecord(gen.GetSessionRow(row)), true, nil
}

// ListSessions returns every session in a project, ordered by num.
func (s *Store) ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListSessionsByProject(ctx, optionalProjectID(project))
	if err != nil {
		return nil, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	return mapListSessionsByProjectRows(rows), nil
}

// ListAllSessions returns every session across all projects.
func (s *Store) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	return mapListAllSessionsRows(rows), nil
}

// CountArchivedSessions counts terminated sessions per project and kind,
// excluding hidden task preparations, without decoding any session row.
func (s *Store) CountArchivedSessions(ctx context.Context) ([]domain.ArchivedSessionCount, error) {
	rows, err := s.qr.CountArchivedSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("count archived sessions: %w", err)
	}
	out := make([]domain.ArchivedSessionCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.ArchivedSessionCount{ProjectID: projectIDValue(r.ProjectID), Kind: r.Kind, Count: int(r.Sessions)})
	}
	return out, nil
}

// ListLivePreviewSessions returns the non-terminated worker sessions that have
// a preview URL selected: the only sessions the preview poller can act on.
func (s *Store) ListLivePreviewSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListLivePreviewSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live preview sessions: %w", err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToRecord(gen.GetSessionRow(r)))
	}
	return out, nil
}

// ListLiveSessions returns every non-terminated session across all projects.
// Periodic observers use it so a tick costs O(live sessions), not O(history).
func (s *Store) ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListLiveSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live sessions: %w", err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToRecord(gen.GetSessionRow(r)))
	}
	return out, nil
}

// ListChatHibernationCandidates avoids decoding inactive sessions and activity JSON.
func (s *Store) ListChatHibernationCandidates(ctx context.Context) ([]domain.SessionID, error) {
	ids, err := s.qr.ListChatHibernationCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list chat hibernation candidates: %w", err)
	}
	return ids, nil
}

// ListHibernatedChatsWithQueuedTurns lists sleeping Chat sessions that hold
// queued messages, so a wake interrupted by a daemon stop is retried.
func (s *Store) ListHibernatedChatsWithQueuedTurns(ctx context.Context) ([]domain.SessionID, error) {
	ids, err := s.qr.ListHibernatedChatsWithQueuedTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list hibernated chats with queued turns: %w", err)
	}
	return ids, nil
}

// ListTurnFailureCandidates returns the harness's live terminal sessions that
// are in a turn and have a native transcript path, reading only the columns
// the failed-turn observer needs.
func (s *Store) ListTurnFailureCandidates(ctx context.Context, harness domain.AgentHarness) ([]domain.TurnFailureCandidate, error) {
	rows, err := s.qr.ListTurnFailureCandidates(ctx, harness)
	if err != nil {
		return nil, fmt.Errorf("list turn failure candidates: %w", err)
	}
	out := make([]domain.TurnFailureCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.TurnFailureCandidate{
			ID:                   r.ID,
			Harness:              r.Harness,
			Activity:             domain.Activity{State: r.ActivityState, LastActivityAt: r.ActivityLastAt},
			LatestUserPromptAt:   nullTimeToTime(r.LatestUserPromptAt),
			Revision:             r.Revision,
			RuntimeLaunchID:      r.RuntimeLaunchID,
			NativeTranscriptPath: r.NativeTranscriptPath,
		})
	}
	return out, nil
}

// SetSessionAgentExitReason records why the session's agent exited; an empty
// reason clears it.
func (s *Store) SetSessionAgentExitReason(ctx context.Context, id domain.SessionID, reason domain.AgentExitReason) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var err error
	if reason == domain.AgentExitUnknown {
		err = s.qw.ClearSessionAgentExitReason(ctx, string(id))
	} else {
		err = s.qw.SetSessionAgentExitReason(ctx, gen.SetSessionAgentExitReasonParams{SessionID: string(id), Reason: string(reason)})
	}
	if err != nil {
		return fmt.Errorf("set agent exit reason for %s: %w", id, err)
	}
	return nil
}

// SessionAgentExitReason returns the recorded exit reason, or AgentExitUnknown.
func (s *Store) SessionAgentExitReason(ctx context.Context, id domain.SessionID) (domain.AgentExitReason, error) {
	reason, err := s.qr.GetSessionAgentExitReason(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AgentExitUnknown, nil
	}
	if err != nil {
		return domain.AgentExitUnknown, fmt.Errorf("get agent exit reason for %s: %w", id, err)
	}
	return domain.AgentExitReason(reason), nil
}
