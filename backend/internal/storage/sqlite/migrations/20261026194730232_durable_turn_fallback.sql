-- +goose Up
CREATE TABLE turn_fallbacks (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('chat', 'tui')),
    generation TEXT NOT NULL,
    native_id TEXT NOT NULL DEFAULT '',
    turn_id TEXT NOT NULL,
    -- Completion identity above is immutable across an interface handoff.
    carry_transition_id TEXT NOT NULL DEFAULT '',
    owner_mode TEXT NOT NULL DEFAULT '' CHECK (owner_mode IN ('', 'chat', 'tui')),
    owner_generation TEXT NOT NULL DEFAULT '',
    owner_native_id TEXT NOT NULL DEFAULT '',
    owner_claimed INTEGER NOT NULL DEFAULT 0 CHECK (owner_claimed IN (0, 1)),
    disposition TEXT NOT NULL DEFAULT 'active' CHECK (disposition IN ('active', 'pending', 'suppressed', 'superseded', 'emitted')),
    completed_at DATETIME,
    due_at DATETIME,
    excerpt TEXT NOT NULL DEFAULT '' CHECK (length(excerpt) <= 500)
);
CREATE INDEX turn_fallbacks_pending ON turn_fallbacks(due_at) WHERE disposition = 'pending';
CREATE INDEX turn_fallbacks_session ON turn_fallbacks(session_id);

-- +goose Down
DROP TABLE turn_fallbacks;
