-- contextBudgetCheck (B822) becomes contextHealth (B825): an install that turned
-- the check on keeps it on. The old key is removed; a rerun changes nothing.

-- +goose Up
-- +goose StatementBegin
UPDATE projects
SET config = json_set(json_remove(config, '$.contextBudgetCheck'), '$.contextHealth', json('true'))
WHERE config IS NOT NULL AND json_valid(config) AND json_type(config) = 'object'
  AND json_extract(config, '$.contextBudgetCheck') = 1;

UPDATE projects
SET config = json_remove(config, '$.contextBudgetCheck')
WHERE config IS NOT NULL AND json_valid(config) AND json_type(config) = 'object'
  AND json_type(config, '$.contextBudgetCheck') IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
