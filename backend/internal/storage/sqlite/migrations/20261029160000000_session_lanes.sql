-- Orchestrator lanes: a lane is a backlog parent item an orchestrator chat is
-- bound to. session_lane records, per session, its lane and the session that
-- spawned it; a session without a row is un-laned. A side table keeps the
-- sessions row shape unchanged. reports.lane_item_id is the lane of the
-- reporting worker at report time ('' = un-laned): it routes the report to
-- that lane's orchestrator.

-- +goose Up
CREATE TABLE session_lane (
    session_id        TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    lane_item_id      TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT NOT NULL DEFAULT ''
);

ALTER TABLE reports ADD COLUMN lane_item_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE reports DROP COLUMN lane_item_id;
DROP TABLE session_lane;
