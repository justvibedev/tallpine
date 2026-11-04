-- synthesized marks daemon-authored reports (turn_ended, review notices,
-- restart wakes). The renderer must not frame them as worker text, so the flag
-- has to survive the round trip through the store. Reports filed before this
-- migration stay unmarked (framed as worker text, the safe default).

-- +goose Up
ALTER TABLE reports ADD COLUMN synthesized INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE reports DROP COLUMN synthesized;
