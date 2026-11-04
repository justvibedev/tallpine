-- +goose Up

-- Every sessions UPDATE now advances revision in its own SET list
-- (TestEverySessionUpdateBumpsRevision guards this). The AFTER UPDATE trigger
-- that used to do it re-updated the row it had just written, so each session
-- write, the hottest row in the database, cost two row writes and a trigger
-- dispatch under the single writer.
DROP TRIGGER IF EXISTS sessions_revision_update;

-- +goose Down
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS sessions_revision_update
AFTER UPDATE ON sessions
WHEN NEW.revision = OLD.revision
BEGIN
    UPDATE sessions SET revision = OLD.revision + 1 WHERE id = NEW.id;
END;
-- +goose StatementEnd
