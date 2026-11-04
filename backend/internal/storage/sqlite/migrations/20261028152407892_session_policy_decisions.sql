-- Summary: the secret-free record of the agent policy decision each session launched under.
-- +goose Up
-- One row per session, replaced at every spawn, relaunch and restore. The record
-- names rules, settings, the layer that set each and what a seam changed
-- (requested -> effective mode, dropped variable names): never a secret value.
-- Absent for sessions that launched before the policy existed.
CREATE TABLE session_policy_decisions (
    session_id TEXT PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    record     TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE session_policy_decisions;
