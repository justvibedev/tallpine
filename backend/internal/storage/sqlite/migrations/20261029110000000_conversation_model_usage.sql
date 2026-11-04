-- +goose Up
-- Cumulative token spend per conversation and model (B84): the sum of the
-- per-turn usage the provider reports, so a compaction A/B test can be read
-- from the app instead of transcripts.
CREATE TABLE conversation_model_usage (
    conversation_id       TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    model                 TEXT NOT NULL,
    input_tokens          INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens         INTEGER NOT NULL DEFAULT 0,
    turns                 INTEGER NOT NULL DEFAULT 0,
    updated_at            DATETIME NOT NULL,
    PRIMARY KEY (conversation_id, model)
);

-- +goose Down
DROP TABLE conversation_model_usage;
