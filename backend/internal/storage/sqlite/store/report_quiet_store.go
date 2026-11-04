package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	gen "tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// FlushQuietReports releases a project's waiting quiet reports when at least
// minCount of them are queued and reports how many it released.
func (s *Store) FlushQuietReports(ctx context.Context, projectID domain.ProjectID, minCount int, at time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.FlushQuietReports(ctx, gen.FlushQuietReportsParams{
		ProjectID: string(projectID), AvailableAt: at, MinCount: int64(minCount),
	})
	return int(n), err
}

// QuietReportState reports whether id is a quiet report and, if so, whether
// its session filed a later report of any state (B403). A daemon-synthesized
// notice (turn_ended, usage_limited) never supersedes a worker-filed report:
// it is filed moments after the worker's own report and carries less (B459).
func (s *Store) QuietReportState(ctx context.Context, id string) (quiet, superseded bool, err error) {
	err = s.readDB.QueryRowContext(ctx, `SELECT
  EXISTS (SELECT 1 FROM report_quiet q WHERE q.report_id = r.id),
  EXISTS (SELECT 1 FROM reports n WHERE n.session_id = r.session_id AND n.id <> r.id
          AND (r.synthesized = 1 OR n.synthesized = 0)
          AND (n.created_at > r.created_at OR (n.created_at = r.created_at AND n.id > r.id)))
FROM reports r WHERE r.id = ?1`, id).Scan(&quiet, &superseded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("quiet report state: %w", err)
	}
	return quiet, superseded, nil
}
