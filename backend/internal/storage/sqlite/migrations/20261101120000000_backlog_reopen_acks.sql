-- Reopen acknowledgements (B213): the merged PRs an item already had when it was
-- manually reopened. The auto-close rule ignores them, so a reopen wins over the
-- merges that happened before it; a PR merged later still closes the item.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE backlog_reopen_acks (
    item_id TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    pr_url  TEXT NOT NULL,
    PRIMARY KEY (item_id, pr_url)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE backlog_reopen_acks;
-- +goose StatementEnd
