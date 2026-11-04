-- Summary: batches that switch every live session of a provider to one agent account, with a per-session outcome.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE account_adoptions (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL,
    provider    TEXT NOT NULL CHECK (provider IN ('claude', 'codex')),
    policy      TEXT NOT NULL CHECK (policy IN ('wait', 'interrupt')),
    request_key TEXT NOT NULL CHECK (length(trim(request_key)) > 0),
    state       TEXT NOT NULL CHECK (state IN ('running', 'completed')),
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL,
    UNIQUE (account_id, request_key)
);
CREATE INDEX idx_account_adoptions_account ON account_adoptions (account_id, created_at);
CREATE INDEX idx_account_adoptions_state ON account_adoptions (provider, state);

CREATE TABLE account_adoption_items (
    adoption_id     TEXT NOT NULL REFERENCES account_adoptions(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    position        INTEGER NOT NULL,
    is_orchestrator INTEGER NOT NULL DEFAULT 0 CHECK (is_orchestrator IN (0, 1)),
    state           TEXT NOT NULL CHECK (state IN ('queued', 'moving', 'waiting', 'moved', 'rebound', 'failed', 'skipped')),
    transition_id   TEXT NOT NULL DEFAULT '',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMP NOT NULL,
    PRIMARY KEY (adoption_id, session_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_adoption_items;
DROP TABLE IF EXISTS account_adoptions;
-- +goose StatementEnd
