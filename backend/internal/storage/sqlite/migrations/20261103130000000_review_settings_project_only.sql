-- +goose Up
-- Review settings become project-only (B411). Copy the install-wide values in
-- app_settings.review_defaults into every project that does not set its own, so
-- no install behaves differently afterwards. The column stays so a downgrade
-- still reads it. Every step only fills what the project leaves unset, so a
-- rerun changes nothing.

-- Auto review and end-on-merge: a project that never set the flag takes the
-- install-wide value when that was on (off is already the default).
UPDATE projects
SET config = json_set(COALESCE(config, '{}'), '$.autoReview', json('true'))
WHERE (config IS NULL OR (json_valid(config) AND json_type(config) = 'object'))
  AND json_type(COALESCE(config, '{}'), '$.autoReview') IS NULL
  AND (SELECT json_extract(review_defaults, '$.autoReview') FROM app_settings WHERE id = 1) = 1;

UPDATE projects
SET config = json_set(COALESCE(config, '{}'), '$.terminateOnPrMerge', json('true'))
WHERE (config IS NULL OR (json_valid(config) AND json_type(config) = 'object'))
  AND json_type(COALESCE(config, '{}'), '$.terminateOnPrMerge') IS NULL
  AND (SELECT json_extract(review_defaults, '$.terminateOnPrMerge') FROM app_settings WHERE id = 1) = 1;

-- First review only: the project's flag OR the install-wide one.
UPDATE projects
SET config = json_set(COALESCE(config, '{}'), '$.reviewRisk.firstReviewOnly', json('true'))
WHERE (config IS NULL OR (json_valid(config) AND json_type(config) = 'object'))
  AND COALESCE(json_extract(config, '$.reviewRisk.firstReviewOnly'), 0) != 1
  AND (SELECT json_extract(review_defaults, '$.reviewRisk.firstReviewOnly') FROM app_settings WHERE id = 1) = 1;

-- Reviewer model: a project without one takes the install-wide model.
UPDATE projects
SET config = json_set(COALESCE(config, '{}'), '$.reviewRisk.model',
    (SELECT json_extract(review_defaults, '$.reviewRisk.model') FROM app_settings WHERE id = 1))
WHERE (config IS NULL OR (json_valid(config) AND json_type(config) = 'object'))
  AND COALESCE(json_extract(config, '$.reviewRisk.model'), '') = ''
  AND COALESCE((SELECT json_extract(review_defaults, '$.reviewRisk.model') FROM app_settings WHERE id = 1), '') != '';

-- An explicit risky model only ever applied to Claude Code and keeps the
-- agent's default effort.
UPDATE projects
SET config = json_set(config, '$.reviewRisk.harness', 'claude-code')
WHERE json_valid(config) AND json_type(config) = 'object'
  AND COALESCE(json_extract(config, '$.reviewRisk.model'), '') != ''
  AND COALESCE(json_extract(config, '$.reviewRisk.harness'), '') = '';

-- Path globs: a project without its own takes the install-wide list, else the
-- list the old built-in default gave it.
UPDATE projects
SET config = json_set(COALESCE(config, '{}'), '$.reviewRisk.paths', json(
    CASE
        WHEN COALESCE(json_array_length((SELECT json_extract(review_defaults, '$.reviewRisk.paths') FROM app_settings WHERE id = 1)), 0) > 0
        THEN (SELECT json_extract(review_defaults, '$.reviewRisk.paths') FROM app_settings WHERE id = 1)
        ELSE '["backend/internal/service/{chat,report,turnnotify,review}/**","backend/internal/storage/**","backend/internal/session_manager/**","backend/internal/httpd/**","**/migrations/**"]'
    END))
WHERE (config IS NULL OR (json_valid(config) AND json_type(config) = 'object'))
  AND COALESCE(json_array_length(json_extract(COALESCE(config, '{}'), '$.reviewRisk.paths')), 0) = 0;

-- +goose Down
SELECT 1;
