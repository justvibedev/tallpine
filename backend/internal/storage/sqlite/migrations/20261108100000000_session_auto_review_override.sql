-- +goose Up
-- A deliberate per-session Auto review choice (SetAutoReview). A session with no
-- row follows the project/app setting, so toggling that covers live workers.
CREATE TABLE session_auto_review_override (
    session_id TEXT    NOT NULL PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL
);

-- +goose Down
DROP TABLE session_auto_review_override;
