-- Lane lifecycle: a lane ends when its last live lane orchestrator terminates.
-- Closing or dropping the lane's backlog item does not end it: a running lane
-- orchestrator stays reachable until it stops (see spawnOrchestrator).
--
-- When a lane ends, every session of the lane loses its lane membership
-- (session_lane row deleted, so lane and parent links go), its undelivered
-- reports move to the default lane, and the ended orchestrator is recorded in
-- ended_lane_orchestrators. Its session stays in history as an ordinary ended
-- session, but GET /orchestrators leaves it out: without the marker it would
-- list as a stopped default orchestrator.
--
-- A trigger rather than service code, so every termination path (kill,
-- reconcile, reaper, rollback) ends the lane. The sweep at the end ends the
-- lanes that were already over before this migration (B108).

-- +goose Up
-- +goose StatementBegin
CREATE TABLE ended_lane_orchestrators (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- A clean replacement retires a lane's orchestrator before it spawns the new
-- one; while it holds the lane here, the retirement does not end the lane.
-- Releasing the hold ends the lane if no orchestrator came up (see
-- Store.ReleaseLane).
-- +goose StatementBegin
CREATE TABLE lane_replacements (
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    lane_item_id TEXT NOT NULL,
    PRIMARY KEY (project_id, lane_item_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER session_lane_end_on_terminate
AFTER UPDATE OF is_terminated ON sessions
WHEN NEW.is_terminated AND NOT OLD.is_terminated AND NEW.kind = 'orchestrator'
    AND (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id) <> ''
    -- A clean replacement in progress keeps it open.
    AND NOT EXISTS (
        SELECT 1 FROM lane_replacements r
        WHERE r.project_id = NEW.project_id
          AND r.lane_item_id = (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id))
    -- Another live orchestrator of the same lane keeps it open.
    AND NOT EXISTS (
        SELECT 1 FROM session_lane l JOIN sessions s ON s.id = l.session_id
        WHERE s.project_id = NEW.project_id AND s.kind = 'orchestrator' AND NOT s.is_terminated
          AND l.lane_item_id = (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id))
BEGIN
    INSERT OR IGNORE INTO ended_lane_orchestrators (session_id)
    SELECT l.session_id FROM session_lane l JOIN sessions s ON s.id = l.session_id
    WHERE s.project_id = NEW.project_id AND s.kind = 'orchestrator'
      AND l.lane_item_id = (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id);
    UPDATE reports SET lane_item_id = ''
    WHERE project_id = NEW.project_id AND delivery_state IN ('pending', 'claimed')
      AND lane_item_id = (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id);
    DELETE FROM session_lane
    WHERE session_id IN (SELECT id FROM sessions WHERE project_id = NEW.project_id)
      AND lane_item_id = (SELECT lane_item_id FROM session_lane WHERE session_id = NEW.id);
END;
-- +goose StatementEnd

-- One-time sweep: lanes with no live orchestrator are already over.
-- +goose StatementBegin
CREATE TEMP TABLE ended_lanes AS
SELECT DISTINCT s.project_id, l.lane_item_id
FROM session_lane l JOIN sessions s ON s.id = l.session_id
WHERE l.lane_item_id <> ''
  AND NOT EXISTS (
      SELECT 1 FROM session_lane l2 JOIN sessions s2 ON s2.id = l2.session_id
      WHERE s2.project_id = s.project_id AND l2.lane_item_id = l.lane_item_id
        AND s2.kind = 'orchestrator' AND NOT s2.is_terminated);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT OR IGNORE INTO ended_lane_orchestrators (session_id)
SELECT l.session_id FROM session_lane l JOIN sessions s ON s.id = l.session_id
WHERE s.kind = 'orchestrator'
  AND (s.project_id, l.lane_item_id) IN (SELECT project_id, lane_item_id FROM ended_lanes);
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE reports SET lane_item_id = ''
WHERE delivery_state IN ('pending', 'claimed')
  AND (project_id, lane_item_id) IN (SELECT project_id, lane_item_id FROM ended_lanes);
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM session_lane
WHERE session_id IN (
    SELECT l.session_id FROM session_lane l JOIN sessions s ON s.id = l.session_id
    WHERE (s.project_id, l.lane_item_id) IN (SELECT project_id, lane_item_id FROM ended_lanes));
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE ended_lanes;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_lane_end_on_terminate;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS lane_replacements;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS ended_lane_orchestrators;
-- +goose StatementEnd
