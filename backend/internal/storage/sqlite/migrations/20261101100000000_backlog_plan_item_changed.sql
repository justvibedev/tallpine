-- Plan item-changed flag (B205 PR 4): set when the item's title, note or
-- done-when is edited while its plan is open or approved. Link, hold and state
-- writes also bump the item's updated_at, so that column cannot carry the flag.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE backlog_plans ADD COLUMN item_changed_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE backlog_plans DROP COLUMN item_changed_at;
-- +goose StatementEnd
