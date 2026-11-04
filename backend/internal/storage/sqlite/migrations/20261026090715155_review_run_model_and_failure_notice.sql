-- model records the reviewer model a pass ran under when AO resolved one
-- (empty means the harness default), so `tallpine review ls` can tell two passes of
-- the same harness apart.
--
-- failure_notified_at marks a failed or cancelled pass whose outcome AO has
-- already handled for notification: told the worker and the orchestrator, or
-- decided it needs no notice (a user cancellation). It makes that notice
-- once-per-run across list calls, sweeps and daemon restarts. Passes that
-- ended before this migration are stamped now so upgrading does not replay
-- every historical failure.

-- +goose Up
ALTER TABLE review_run ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN failure_notified_at TIMESTAMP;
UPDATE review_run SET failure_notified_at = created_at WHERE status IN ('failed', 'cancelled');

-- Both sweeps read a handful of rows: passes still running, and ended passes
-- not yet handled. Partial indexes keep those reads off the full history.
CREATE INDEX idx_review_run_running_session
    ON review_run(session_id)
    WHERE status = 'running';
CREATE INDEX idx_review_run_failure_unnotified
    ON review_run(created_at)
    WHERE failure_notified_at IS NULL AND status IN ('failed', 'cancelled');

-- +goose Down
DROP INDEX IF EXISTS idx_review_run_failure_unnotified;
DROP INDEX IF EXISTS idx_review_run_running_session;
ALTER TABLE review_run DROP COLUMN failure_notified_at;
ALTER TABLE review_run DROP COLUMN model;
