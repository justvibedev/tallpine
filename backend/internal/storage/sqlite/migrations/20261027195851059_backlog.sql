-- Summary: per-project backlog of deferred work (items, typed links, ref counters).
-- +goose Up
-- +goose StatementBegin
CREATE TABLE backlog_items (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    seq               INTEGER NOT NULL,
    title             TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    note              TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 20000),
    done_when         TEXT NOT NULL DEFAULT '' CHECK (length(done_when) <= 2000),
    state             TEXT NOT NULL CHECK (state IN ('inbox', 'open', 'done', 'dropped')),
    hold              TEXT NOT NULL DEFAULT '' CHECK (length(hold) <= 200),
    scope             TEXT CHECK (scope IS NULL OR scope IN ('local', 'pr', 'pr-merge')),
    parent_id         TEXT REFERENCES backlog_items(id) ON DELETE CASCADE,
    position          INTEGER NOT NULL,
    origin_kind       TEXT NOT NULL CHECK (origin_kind IN ('human', 'orchestrator', 'worker', 'reviewer')),
    origin_session_id TEXT NOT NULL DEFAULT '',
    origin_excerpt    TEXT NOT NULL DEFAULT '' CHECK (length(origin_excerpt) <= 1000),
    agent_notes       TEXT NOT NULL DEFAULT '' CHECK (length(agent_notes) <= 20000),
    fields            TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(fields) AND json_type(fields) = 'object'),
    closed_reason     TEXT CHECK (closed_reason IS NULL OR length(closed_reason) <= 200),
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL,
    closed_at         TIMESTAMP,
    UNIQUE (project_id, seq),
    CHECK ((state IN ('done', 'dropped')) = (closed_at IS NOT NULL)),
    CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX idx_backlog_items_project_state ON backlog_items (project_id, state, position);
CREATE INDEX idx_backlog_items_parent ON backlog_items (parent_id);

CREATE TABLE backlog_links (
    item_id    TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('session', 'pr', 'issue', 'artifact', 'memory', 'item', 'blocked_by')),
    ref        TEXT NOT NULL CHECK (length(trim(ref)) > 0),
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (item_id, kind, ref)
);
CREATE INDEX idx_backlog_links_reverse ON backlog_links (kind, ref);

CREATE TABLE backlog_counters (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    next_seq   INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS backlog_counters;
DROP TABLE IF EXISTS backlog_links;
DROP TABLE IF EXISTS backlog_items;
-- +goose StatementEnd
