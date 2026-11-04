-- +goose Up
-- A usage binding update notifies the session only when something changed.
--
-- Every agent hook re-upserts its session's binding and the collector keeps
-- refreshing binding state, and almost all of those writes change nothing but
-- updated_at. The unconditional update trigger turned each into an id-only
-- session_updated: the bulk of all CDC traffic, and each one made the renderer
-- re-read the session and refetch its usage.
--
-- Newly ingested usage changes no binding fact either, but it is a change (the
-- usage totals), so TouchUsageBinding now advances usage_revision, and the
-- trigger fires when that or any binding fact changes. Keep the WHEN list in
-- step with the table's columns (TestUsageBindingsCDCTriggerWatchesEveryFact).
DROP TRIGGER IF EXISTS usage_bindings_cdc_update;

ALTER TABLE usage_bindings ADD COLUMN usage_revision INTEGER NOT NULL DEFAULT 0;

-- +goose StatementBegin
CREATE TRIGGER usage_bindings_cdc_update AFTER UPDATE ON usage_bindings
WHEN OLD.usage_revision IS NOT NEW.usage_revision
  OR OLD.session_id IS NOT NEW.session_id
  OR OLD.harness IS NOT NEW.harness
  OR OLD.native_root_id IS NOT NEW.native_root_id
  OR OLD.initial_model_id IS NOT NEW.initial_model_id
  OR OLD.state IS NOT NEW.state
  OR OLD.last_error_code IS NOT NEW.last_error_code
  OR OLD.provider_hint IS NOT NEW.provider_hint
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS usage_bindings_cdc_update;

ALTER TABLE usage_bindings DROP COLUMN usage_revision;

-- +goose StatementBegin
CREATE TRIGGER usage_bindings_cdc_update AFTER UPDATE ON usage_bindings BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES ((SELECT project_id FROM sessions WHERE id = NEW.session_id),
            NEW.session_id, 'session_updated', json_object('id', NEW.session_id), NEW.updated_at);
END;
-- +goose StatementEnd
