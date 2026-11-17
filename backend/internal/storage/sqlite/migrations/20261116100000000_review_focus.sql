-- focus and focus_author keep a pass's one-off review focus (`tallpine review
-- trigger --focus`) and who gave it, on the run and on a parked trigger, so a
-- retry or a queued rerun reviews with the same focus. Empty means none.

-- +goose Up
ALTER TABLE review_run ADD COLUMN focus TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN focus_author TEXT NOT NULL DEFAULT '';
ALTER TABLE review_trigger_queue ADD COLUMN focus TEXT NOT NULL DEFAULT '';
ALTER TABLE review_trigger_queue ADD COLUMN focus_author TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE review_trigger_queue DROP COLUMN focus_author;
ALTER TABLE review_trigger_queue DROP COLUMN focus;
ALTER TABLE review_run DROP COLUMN focus_author;
ALTER TABLE review_run DROP COLUMN focus;
