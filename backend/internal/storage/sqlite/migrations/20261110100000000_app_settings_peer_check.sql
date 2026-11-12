-- The owner's off switch for the daemon peer check (B631). 'default' follows
-- the build: enforcing on a signed macOS build. 'off' stops refusing callers
-- Tallpine did not start. Only the desktop app writes it through the API;
-- editing it here while the daemon is stopped is the documented last resort.
-- +goose Up
ALTER TABLE app_settings ADD COLUMN peer_check TEXT NOT NULL DEFAULT 'default'
    CHECK (peer_check IN ('default', 'off'));

-- +goose Down
ALTER TABLE app_settings DROP COLUMN peer_check;
