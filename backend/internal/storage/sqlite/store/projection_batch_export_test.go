package store

import (
	"context"
	"database/sql"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// GroupCommitProjectionForTest runs one unit through the projection batcher and
// hands it an opaque handle of the transaction it ran in.
func (s *Store) GroupCommitProjectionForTest(ctx context.Context, run func(ctx context.Context, tx any) (bool, error)) (bool, error) {
	return s.groupCommitProjection(ctx, func(ctx context.Context, tx *sql.Tx, q *gen.Queries) (bool, error) {
		return run(ctx, projectionTxForTest{tx: tx, q: q})
	})
}

// QueuedProjectionsForTest reports how many units wait for the next batch.
func (s *Store) QueuedProjectionsForTest() int {
	s.projections.mu.Lock()
	defer s.projections.mu.Unlock()
	return len(s.projections.queue)
}

// GroupCommitSQLForTest runs one unit that executes statement on the batch's
// transaction under the context the batcher hands the unit.
func (s *Store) GroupCommitSQLForTest(ctx context.Context, started func(), statement string) (bool, error) {
	return s.groupCommitProjection(ctx, func(ctx context.Context, tx *sql.Tx, _ *gen.Queries) (bool, error) {
		started()
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return false, err
		}
		return true, nil
	})
}

// SetProjectionBatchWatchdogForTest shortens the whole-batch watchdog.
func SetProjectionBatchWatchdogForTest(d time.Duration) (restore func()) {
	previous := projectionBatchWatchdog
	projectionBatchWatchdog = d
	return func() { projectionBatchWatchdog = previous }
}

// projectionTxForTest is the opaque transaction handle test units receive. Two
// units share a transaction exactly when their handles are equal.
type projectionTxForTest struct {
	tx *sql.Tx
	q  *gen.Queries
}

// ExecOnProjectionTxForTest executes statement on the unit's transaction.
func (s *Store) ExecOnProjectionTxForTest(ctx context.Context, tx any, statement string) (bool, error) {
	if _, err := tx.(projectionTxForTest).tx.ExecContext(ctx, statement); err != nil {
		return false, err
	}
	return true, nil
}

// KeepLockedForTest runs keepLocked on the writer connection.
func (s *Store) KeepLockedForTest(ctx context.Context, id string, cfg domain.ProjectConfig) (domain.ProjectConfig, error) {
	return s.keepLocked(ctx, s.qw, id, cfg)
}
