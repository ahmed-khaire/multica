-- name: GetActiveGatewayUserKey :one
SELECT * FROM gateway_user_key
WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: GetGatewayUserKeyByHash :one
SELECT * FROM gateway_user_key
WHERE key_hash = $1 AND revoked_at IS NULL;

-- name: CreateGatewayUserKey :one
INSERT INTO gateway_user_key (
    workspace_id, user_id, key_hash, encrypted_key_value, key_prefix
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListGatewayUserKeys :many
SELECT * FROM gateway_user_key
WHERE workspace_id = $1 AND user_id = $2
ORDER BY created_at DESC;

-- name: RevokeGatewayUserKey :one
UPDATE gateway_user_key
SET revoked_at = now()
WHERE workspace_id = $1 AND user_id = $2 AND id = $3 AND revoked_at IS NULL
RETURNING *;

-- name: TouchGatewayUserKeyLastUsed :exec
UPDATE gateway_user_key
SET last_used_at = now()
WHERE id = $1 AND revoked_at IS NULL;
