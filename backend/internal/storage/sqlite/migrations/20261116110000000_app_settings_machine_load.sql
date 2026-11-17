-- Machine-wide load gate for native reviewers (B752). machine_max_reviewers is
-- the most reviewers with a running pass on the machine before a new pass is
-- held; machine_load_percent is the one-minute load, as a share of the cores,
-- at which a new pass is held. 0 turns a limit off.
-- +goose Up
ALTER TABLE app_settings ADD COLUMN machine_max_reviewers INTEGER NOT NULL DEFAULT 3
    CHECK (machine_max_reviewers BETWEEN 0 AND 100);
ALTER TABLE app_settings ADD COLUMN machine_load_percent INTEGER NOT NULL DEFAULT 100
    CHECK (machine_load_percent BETWEEN 0 AND 1000);

-- +goose Down
ALTER TABLE app_settings DROP COLUMN machine_load_percent;
ALTER TABLE app_settings DROP COLUMN machine_max_reviewers;
