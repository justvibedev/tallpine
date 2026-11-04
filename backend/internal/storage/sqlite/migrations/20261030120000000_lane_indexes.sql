-- Lane routing reads: the report delivery queries filter on
-- (project_id, delivery_state, lane_item_id, available_at), which
-- idx_reports_project_delivery cannot serve past delivery_state; a partial
-- index keeps it to the pending rows. session_lane had no index on
-- lane_item_id for "sessions of lane X" lookups.

-- +goose Up
CREATE INDEX idx_reports_lane_pending ON reports(project_id, lane_item_id, available_at) WHERE delivery_state = 'pending';
CREATE INDEX idx_session_lane_item ON session_lane(lane_item_id);

-- +goose Down
DROP INDEX idx_session_lane_item;
DROP INDEX idx_reports_lane_pending;
