-- Batches that move every live session of a provider to one agent account.

-- name: CreateAccountAdoption :exec
INSERT INTO account_adoptions (id, account_id, provider, policy, request_key, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'running', ?, ?);

-- name: CreateAccountAdoptionItem :exec
INSERT INTO account_adoption_items (adoption_id, session_id, position, is_orchestrator, state, updated_at)
VALUES (?, ?, ?, ?, 'queued', ?);

-- name: GetAccountAdoption :one
SELECT * FROM account_adoptions WHERE id = ?;

-- name: GetAccountAdoptionByKey :one
SELECT * FROM account_adoptions WHERE account_id = ? AND request_key = ?;

-- name: GetLatestAccountAdoption :one
SELECT * FROM account_adoptions WHERE account_id = ? ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: GetRunningAccountAdoption :one
SELECT * FROM account_adoptions WHERE provider = ? AND state = 'running' ORDER BY created_at LIMIT 1;

-- name: ListRunningAccountAdoptions :many
SELECT * FROM account_adoptions WHERE state = 'running' ORDER BY created_at, id;

-- name: ListAccountAdoptionItems :many
SELECT * FROM account_adoption_items WHERE adoption_id = ? ORDER BY position, session_id;

-- name: UpdateAccountAdoptionItem :exec
UPDATE account_adoption_items
SET state = ?, transition_id = ?, error_code = ?, error_message = ?, updated_at = ?
WHERE adoption_id = ? AND session_id = ?;

-- name: CompleteAccountAdoption :exec
UPDATE account_adoptions SET state = 'completed', updated_at = ? WHERE id = ? AND state = 'running';
