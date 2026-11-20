-- comments_json keeps the owner's comments on a whole plan (B831): sent alone,
-- with Approve (held until the planner accepts) or with Replan, each with what
-- the planner made of it. Empty means none.

-- +goose Up
ALTER TABLE backlog_plans ADD COLUMN comments_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE backlog_plans DROP COLUMN comments_json;
