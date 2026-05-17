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
    encrypted_credential, credential_hint, enabled, metadata,
    transport, subscription_provider, dispatch_scope, validation_status,
    created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
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
    transport = $10,
    subscription_provider = $11,
    dispatch_scope = $12,
    validation_status = $13,
    updated_by = $14,
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
    credential_hint, enabled, priority,
    credential_type, subscription_provider, encrypted_payload, payload_format,
    dispatch_scope, validation_status, created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
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
  AND (rate_limited_until IS NULL OR rate_limited_until <= now())
ORDER BY
  CASE WHEN rate_limit_remaining IS NULL THEN 1 ELSE 0 END,
  rate_limit_remaining DESC NULLS LAST,
  priority ASC,
  created_at ASC;

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
    credential_type = $9,
    subscription_provider = $10,
    encrypted_payload = $11,
    payload_format = $12,
    dispatch_scope = $13,
    validation_status = $14,
    updated_by = $15,
    updated_at = now()
WHERE workspace_id = $1
  AND backend_id = $2
  AND id = $3
RETURNING *;

-- name: RecordGatewayBackendCredentialResult :one
UPDATE gateway_backend_credential
SET
    last_used_at = CASE WHEN sqlc.arg(success)::boolean THEN now() ELSE last_used_at END,
    last_error_at = CASE WHEN sqlc.arg(success)::boolean THEN NULL ELSE now() END,
    last_error = CASE WHEN sqlc.arg(success)::boolean THEN '' ELSE sqlc.arg(last_error) END,
    rate_limited_until = sqlc.arg(rate_limited_until),
    rate_limit_remaining = sqlc.arg(rate_limit_remaining),
    rate_limit_reset_at = sqlc.arg(rate_limit_reset_at),
    updated_at = now()
WHERE workspace_id = sqlc.arg(workspace_id)
  AND backend_id = sqlc.arg(backend_id)
  AND id = sqlc.arg(id)
RETURNING *;

-- name: UpdateGatewayBackendValidationStatus :one
UPDATE gateway_backend
SET
    validation_status = $3,
    validated_runtime_id = $4,
    last_validation_at = now(),
    last_validation_error = $5,
    updated_at = now()
FROM agent_runtime ar
WHERE gateway_backend.workspace_id = $1
  AND gateway_backend.id = $2
  AND ar.id = $4
  AND ar.workspace_id = gateway_backend.workspace_id
  AND ar.provider = CASE WHEN gateway_backend.subscription_provider = 'claude_code' THEN 'claude' ELSE gateway_backend.subscription_provider END
RETURNING gateway_backend.*;

-- name: UpdateGatewayBackendCredentialValidationStatus :one
UPDATE gateway_backend_credential
SET
    validation_status = $4,
    validated_runtime_id = $5,
    account_hint = $6,
    account_fingerprint = $7,
    last_validation_at = now(),
    last_validation_error = $8,
    updated_at = now()
FROM gateway_backend b, agent_runtime ar
WHERE gateway_backend_credential.workspace_id = $1
  AND gateway_backend_credential.backend_id = $2
  AND gateway_backend_credential.id = $3
  AND b.workspace_id = gateway_backend_credential.workspace_id
  AND b.id = gateway_backend_credential.backend_id
  AND ar.id = $5
  AND ar.workspace_id = gateway_backend_credential.workspace_id
  AND ar.provider = CASE WHEN gateway_backend_credential.subscription_provider = 'claude_code' THEN 'claude' ELSE gateway_backend_credential.subscription_provider END
RETURNING gateway_backend_credential.*;
