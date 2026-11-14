package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ListBranchReviewVerdictsForSessions returns, per requested session, its
// local-branch review passes that recorded a verdict. It reads only the
// columns a verdict needs (no review text), in one query, for session-list
// reads. Sessions without such passes are absent from the map.
func (s *Store) ListBranchReviewVerdictsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.ReviewRun, error) {
	out := map[domain.SessionID][]domain.ReviewRun{}
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT session_id, pr_url, target_sha, verdict, created_at
FROM review_run
WHERE session_id IN (SELECT CAST(value AS TEXT) FROM json_each(?))
  AND pr_url LIKE 'local:%' AND verdict != ''`, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list branch review verdicts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			run     domain.ReviewRun
			created time.Time
		)
		if err := rows.Scan(&run.SessionID, &run.PRURL, &run.TargetSHA, &run.Verdict, &created); err != nil {
			return nil, fmt.Errorf("scan branch review verdict: %w", err)
		}
		run.CreatedAt = created
		out[run.SessionID] = append(out[run.SessionID], run)
	}
	return out, rows.Err()
}
