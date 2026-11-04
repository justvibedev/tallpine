-- +goose Up
-- Install-wide review defaults (app.reviewDefaults). An empty object keeps
-- every default off, so existing installs behave as before.
ALTER TABLE app_settings ADD COLUMN review_defaults TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(review_defaults) AND json_type(review_defaults) = 'object');

-- +goose Down
ALTER TABLE app_settings DROP COLUMN review_defaults;
