-- name: GetGatewayWorkspaceSettings :one
SELECT * FROM gateway_workspace_settings
WHERE workspace_id = $1;

-- name: UpsertGatewayWorkspaceSettings :one
INSERT INTO gateway_workspace_settings (workspace_id, capture_policy, default_backend_id)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id)
DO UPDATE SET
    capture_policy = EXCLUDED.capture_policy,
    default_backend_id = EXCLUDED.default_backend_id,
    updated_at = now()
RETURNING *;

-- name: CreateGatewayBackend :one
INSERT INTO gateway_backend (
    workspace_id, slug, display_name, backend_type, base_url,
    encrypted_credential, credential_hint, enabled, metadata, created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
RETURNING *;

-- name: ListGatewayBackends :many
SELECT * FROM gateway_backend
WHERE workspace_id = $1
ORDER BY slug;

-- name: ListEnabledGatewayBackends :many
SELECT * FROM gateway_backend
WHERE workspace_id = $1 AND enabled = TRUE
ORDER BY slug;

-- name: GetGatewayBackendByID :one
SELECT * FROM gateway_backend
WHERE workspace_id = $1 AND id = $2;

-- name: GetGatewayBackendBySlug :one
SELECT * FROM gateway_backend
WHERE workspace_id = $1 AND slug = $2;

-- name: UpdateGatewayBackend :one
UPDATE gateway_backend
SET
    display_name = $3,
    backend_type = $4,
    base_url = $5,
    encrypted_credential = $6,
    credential_hint = $7,
    enabled = $8,
    metadata = $9,
    updated_by = $10,
    updated_at = now()
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: SetGatewayBackendEnabled :one
UPDATE gateway_backend
SET enabled = $3, updated_by = $4, updated_at = now()
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: DeleteGatewayBackend :exec
DELETE FROM gateway_backend
WHERE workspace_id = $1 AND id = $2;

-- name: SetGatewayDefaultBackend :one
UPDATE gateway_workspace_settings
SET default_backend_id = $2, updated_at = now()
WHERE workspace_id = $1
RETURNING *;

-- name: CreateGatewayBackendCredential :one
INSERT INTO gateway_backend_credential (
    workspace_id, backend_id, label, encrypted_credential,
    credential_hint, enabled, priority, created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: ListGatewayBackendCredentialsForBackend :many
SELECT * FROM gateway_backend_credential
WHERE workspace_id = $1
  AND backend_id = $2
ORDER BY enabled DESC, priority ASC, created_at ASC;

-- name: ListActiveGatewayBackendCredentialsForBackend :many
SELECT * FROM gateway_backend_credential
WHERE workspace_id = $1
  AND backend_id = $2
  AND enabled = TRUE
ORDER BY priority ASC, created_at ASC;

-- name: GetGatewayBackendCredentialByID :one
SELECT * FROM gateway_backend_credential
WHERE workspace_id = $1
  AND backend_id = $2
  AND id = $3;

-- name: UpdateGatewayBackendCredential :one
UPDATE gateway_backend_credential
SET
    label = $4,
    encrypted_credential = $5,
    credential_hint = $6,
    enabled = $7,
    priority = $8,
    updated_by = $9,
    updated_at = now()
WHERE workspace_id = $1
  AND backend_id = $2
  AND id = $3
RETURNING *;
