-- +goose Up
-- A worker's last message was cut at 500 characters in the turn_ended notice;
-- the excerpt now keeps up to 800. SQLite cannot alter a CHECK, so the table is rebuilt.
CREATE TABLE turn_fallbacks_new (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('chat', 'tui')),
    generation TEXT NOT NULL,
    native_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL,
    carry_transition_id TEXT NOT NULL DEFAULT '',
    owner_mode TEXT NOT NULL DEFAULT '' CHECK (owner_mode IN ('', 'chat', 'tui')),
    owner_generation TEXT NOT NULL DEFAULT '',
    owner_native_id TEXT NOT NULL DEFAULT '',
    owner_claimed INTEGER NOT NULL DEFAULT 0 CHECK (owner_claimed IN (0, 1)),
    disposition TEXT NOT NULL DEFAULT 'active' CHECK (disposition IN ('active', 'pending', 'suppressed', 'superseded', 'emitted')),
    completed_at DATETIME,
    due_at DATETIME,
    excerpt TEXT NOT NULL DEFAULT '' CHECK (length(excerpt) <= 800)
);
INSERT INTO turn_fallbacks_new SELECT id, session_id, mode, generation, native_id, turn_id, carry_transition_id, owner_mode, owner_generation, owner_native_id, owner_claimed, disposition, completed_at, due_at, excerpt FROM turn_fallbacks;
DROP TABLE turn_fallbacks;
ALTER TABLE turn_fallbacks_new RENAME TO turn_fallbacks;
CREATE INDEX turn_fallbacks_pending ON turn_fallbacks(due_at) WHERE disposition = 'pending';
CREATE INDEX turn_fallbacks_session ON turn_fallbacks(session_id);

-- +goose Down
SELECT 1;
