package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

const contextHealthCols = `id, project_id, kind, fingerprint, state, evidence, measured_tokens, budget_tokens,
    artifact_dir, ask_id, ask_ref, backlog_item_id, decline_reason, declined_at, declined_tokens, created_at, updated_at`

// UpsertFinding inserts a finding, or rewrites the one with the same project
// and fingerprint (its id and creation time are kept).
func (s *Store) UpsertFinding(ctx context.Context, f domain.ContextHealthFinding) error {
	return s.inTxDB(ctx, "upsert context health finding", func(_ *gen.Queries, tx gen.DBTX) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO context_health_findings (`+contextHealthCols+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, fingerprint) DO UPDATE SET
    kind = excluded.kind, state = excluded.state, evidence = excluded.evidence,
    measured_tokens = excluded.measured_tokens, budget_tokens = excluded.budget_tokens,
    artifact_dir = excluded.artifact_dir, ask_id = excluded.ask_id, ask_ref = excluded.ask_ref,
    backlog_item_id = excluded.backlog_item_id, decline_reason = excluded.decline_reason,
    declined_at = excluded.declined_at, declined_tokens = excluded.declined_tokens,
    updated_at = excluded.updated_at`,
			f.ID, string(f.ProjectID), string(f.Kind), f.Fingerprint, string(f.State), f.Evidence, f.MeasuredTokens, f.BudgetTokens,
			f.ArtifactDir, f.AskID, f.AskRef, f.BacklogItemID, f.DeclineReason, nullTime(f.DeclinedAt), f.DeclinedTokens, f.CreatedAt, f.UpdatedAt)
		return err
	})
}

// SaveFindingTransition rewrites a finding only while it is still in state
// from, so a sweep and an owner's answer cannot both move the same finding. It
// reports whether the row changed.
func (s *Store) SaveFindingTransition(ctx context.Context, f domain.ContextHealthFinding, from domain.ContextHealthState) (bool, error) {
	changed := false
	err := s.inTxDB(ctx, "save context health finding", func(_ *gen.Queries, tx gen.DBTX) error {
		res, err := tx.ExecContext(ctx, `UPDATE context_health_findings SET
    state = ?, evidence = ?, measured_tokens = ?, budget_tokens = ?, artifact_dir = ?, ask_id = ?, ask_ref = ?,
    backlog_item_id = ?, decline_reason = ?, declined_at = ?, declined_tokens = ?, updated_at = ?
WHERE id = ? AND state = ?`,
			string(f.State), f.Evidence, f.MeasuredTokens, f.BudgetTokens, f.ArtifactDir, f.AskID, f.AskRef,
			f.BacklogItemID, f.DeclineReason, nullTime(f.DeclinedAt), f.DeclinedTokens, f.UpdatedAt, f.ID, string(from))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		changed = n > 0
		return err
	})
	return changed, err
}

// ListFindings lists a project's findings, oldest first.
func (s *Store) ListFindings(ctx context.Context, project domain.ProjectID) ([]domain.ContextHealthFinding, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT `+contextHealthCols+` FROM context_health_findings WHERE project_id = ? ORDER BY created_at, id`, string(project))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.ContextHealthFinding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FindingByAsk returns the finding a daemon ask belongs to.
func (s *Store) FindingByAsk(ctx context.Context, askID string) (domain.ContextHealthFinding, bool, error) {
	row := s.readDB.QueryRowContext(ctx, `SELECT `+contextHealthCols+` FROM context_health_findings WHERE ask_id = ?`, askID)
	f, err := scanFinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ContextHealthFinding{}, false, nil
	}
	return f, err == nil, err
}

// GetRun returns when the project's daily check last ran; ok is false when it never did.
func (s *Store) GetRun(ctx context.Context, project domain.ProjectID) (ranAt time.Time, ok bool, err error) {
	err = s.readDB.QueryRowContext(ctx, `SELECT ran_at FROM context_health_runs WHERE project_id = ?`, string(project)).Scan(&ranAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return ranAt, err == nil, err
}

// SaveRun records a finished daily check with its measurement snapshot.
func (s *Store) SaveRun(ctx context.Context, project domain.ProjectID, ranAt time.Time, snapshot string) error {
	return s.inTxDB(ctx, "save context health run", func(_ *gen.Queries, tx gen.DBTX) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO context_health_runs (project_id, ran_at, snapshot) VALUES (?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET ran_at = excluded.ran_at, snapshot = excluded.snapshot`, string(project), ranAt, snapshot)
		return err
	})
}

// FirstTurnInput is the input tokens of the first model call of one session.
type FirstTurnInput struct {
	Harness string
	Kind    string
	Tokens  int64
}

// ListFirstTurnInputs returns the reported input tokens of the first model call
// of the project's newest sessions that started since the given time.
func (s *Store) ListFirstTurnInputs(ctx context.Context, project domain.ProjectID, since time.Time, limit int) ([]FirstTurnInput, error) {
	rows, err := s.readDB.QueryContext(ctx, `SELECT b.harness, s.kind, e.input_tokens
FROM usage_bindings b
JOIN sessions s ON s.id = b.session_id
JOIN model_usage_events e ON e.id = (
    SELECT MIN(e2.id) FROM model_usage_events e2
    JOIN usage_sources u2 ON u2.id = e2.usage_source_id
    WHERE e2.binding_id = b.id AND u2.kind IN ('claude_main', 'codex_rollout') AND e2.input_tokens IS NOT NULL)
WHERE s.project_id = ? AND s.created_at >= ?
ORDER BY b.id DESC LIMIT ?`, string(project), since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FirstTurnInput
	for rows.Next() {
		var in FirstTurnInput
		if err := rows.Scan(&in.Harness, &in.Kind, &in.Tokens); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanFinding(r rowScanner) (domain.ContextHealthFinding, error) {
	var f domain.ContextHealthFinding
	var kind, state, project string
	var declined sql.NullTime
	err := r.Scan(&f.ID, &project, &kind, &f.Fingerprint, &state, &f.Evidence, &f.MeasuredTokens, &f.BudgetTokens,
		&f.ArtifactDir, &f.AskID, &f.AskRef, &f.BacklogItemID, &f.DeclineReason, &declined, &f.DeclinedTokens, &f.CreatedAt, &f.UpdatedAt)
	f.ProjectID, f.Kind, f.State = domain.ProjectID(project), domain.ContextHealthKind(kind), domain.ContextHealthState(state)
	if declined.Valid {
		f.DeclinedAt = declined.Time
	}
	return f, err
}
