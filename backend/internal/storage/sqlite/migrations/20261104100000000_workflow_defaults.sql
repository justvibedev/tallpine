-- +goose Up
-- App-wide workflow defaults projects inherit when they set none of their own
-- (B525). A JSON object; a missing key reads as the built-in default, so the
-- empty object is today's behavior (auto-review off).
ALTER TABLE app_settings ADD COLUMN workflow_defaults TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(workflow_defaults));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN workflow_defaults;
