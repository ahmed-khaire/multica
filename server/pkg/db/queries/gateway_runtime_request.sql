-- name: CreateGatewaySubscriptionValidation :one
INSERT INTO gateway_subscription_runtime_validation (
    workspace_id, backend_id, credential_id, runtime_id, status, provider
)
SELECT sqlc.arg(workspace_id), b.id, c.id, ar.id, 'pending', sqlc.arg(provider)
FROM gateway_backend b
JOIN gateway_backend_credential c
  ON c.workspace_id = b.workspace_id AND c.backend_id = b.id
JOIN agent_runtime ar
  ON ar.workspace_id = b.workspace_id
JOIN member m
  ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
WHERE b.workspace_id = sqlc.arg(workspace_id)
  AND b.id = sqlc.arg(backend_id)
  AND c.id = sqlc.arg(credential_id)
  AND ar.id = sqlc.arg(runtime_id)
  AND b.transport = 'daemon_dispatch'
  AND b.subscription_provider = sqlc.arg(provider)
  AND c.credential_type = 'subscription_bundle'
  AND c.subscription_provider = sqlc.arg(provider)
  AND ar.status = 'online'
  AND ar.provider = CASE WHEN sqlc.arg(provider) = 'claude_code' THEN 'claude' ELSE sqlc.arg(provider) END
ON CONFLICT (credential_id, runtime_id)
DO UPDATE SET
    backend_id = EXCLUDED.backend_id,
    provider = EXCLUDED.provider,
    status = 'pending',
    account_hint = '',
    account_fingerprint = '',
    error_code = '',
    error_message = '',
    started_at = NULL,
    completed_at = NULL,
    updated_at = now()
RETURNING *;

-- name: CreatePendingGatewaySubscriptionValidationsForRuntime :many
INSERT INTO gateway_subscription_runtime_validation (
    workspace_id, backend_id, credential_id, runtime_id, status, provider
)
SELECT b.workspace_id, b.id, c.id, ar.id, 'pending', b.subscription_provider
FROM agent_runtime ar
JOIN gateway_backend b
  ON b.workspace_id = ar.workspace_id
JOIN gateway_backend_credential c
  ON c.workspace_id = b.workspace_id AND c.backend_id = b.id
JOIN member m
  ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
WHERE ar.workspace_id = sqlc.arg(workspace_id)
  AND ar.id = sqlc.arg(runtime_id)
  AND ar.status = 'online'
  AND b.enabled = TRUE
  AND b.transport = 'daemon_dispatch'
  AND b.subscription_provider != ''
  AND b.validation_status IN ('pending_runtime_validation', 'degraded_no_runtime')
  AND c.enabled = TRUE
  AND c.credential_type = 'subscription_bundle'
  AND c.subscription_provider = b.subscription_provider
  AND c.validation_status IN ('pending_runtime_validation', 'degraded_no_runtime')
  AND ar.provider = CASE WHEN b.subscription_provider = 'claude_code' THEN 'claude' ELSE b.subscription_provider END
ON CONFLICT (credential_id, runtime_id)
DO UPDATE SET
    backend_id = EXCLUDED.backend_id,
    provider = EXCLUDED.provider,
    status = 'pending',
    account_hint = '',
    account_fingerprint = '',
    error_code = '',
    error_message = '',
    started_at = NULL,
    completed_at = NULL,
    updated_at = now()
RETURNING *;

-- name: ClaimGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation v
SET status = 'running', started_at = now(), updated_at = now()
WHERE v.id = (
    SELECT v2.id
    FROM gateway_subscription_runtime_validation v2
    JOIN agent_runtime ar ON ar.id = v2.runtime_id
    JOIN member m ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
    WHERE v2.workspace_id = $1
      AND v2.runtime_id = $2
      AND v2.provider = $3
      AND v2.status = 'pending'
      AND ar.workspace_id = v2.workspace_id
      AND ar.provider = CASE WHEN v2.provider = 'claude_code' THEN 'claude' ELSE v2.provider END
      AND ar.status = 'online'
    ORDER BY v2.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation
SET
    status = 'succeeded',
    account_hint = $4,
    account_fingerprint = $5,
    error_code = '',
    error_message = '',
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1
  AND id = $2
  AND runtime_id = $3
  AND status = 'running'
RETURNING *;

-- name: FailGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation
SET
    status = 'failed',
    error_code = $4,
    error_message = $5,
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1
  AND id = $2
  AND runtime_id = $3
  AND status = 'running'
RETURNING *;

-- name: ListValidatedSubscriptionRuntimes :many
SELECT ar.*
FROM gateway_subscription_runtime_validation v
JOIN agent_runtime ar ON ar.id = v.runtime_id
JOIN member m ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
WHERE v.workspace_id = $1
  AND v.backend_id = $2
  AND v.credential_id = $3
  AND v.provider = $4
  AND v.status = 'succeeded'
  AND ar.workspace_id = v.workspace_id
  AND ar.provider = CASE WHEN v.provider = 'claude_code' THEN 'claude' ELSE v.provider END
  AND ar.status = 'online'
ORDER BY ar.last_seen_at DESC;

-- name: CreateGatewayRuntimeRequest :one
INSERT INTO gateway_runtime_request (
    workspace_id, backend_id, credential_id, runtime_id, provider, surface, status,
    request_body, stream
)
SELECT
  sqlc.arg(workspace_id), b.id, c.id, ar.id, sqlc.arg(provider),
  sqlc.arg(surface), 'queued', sqlc.arg(request_body), sqlc.arg(stream)
FROM gateway_backend b
JOIN gateway_backend_credential c
  ON c.workspace_id = b.workspace_id AND c.backend_id = b.id
JOIN agent_runtime ar
  ON ar.workspace_id = b.workspace_id
JOIN member m
  ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
WHERE b.workspace_id = sqlc.arg(workspace_id)
  AND b.id = sqlc.arg(backend_id)
  AND c.id = sqlc.arg(credential_id)
  AND ar.id = sqlc.arg(runtime_id)
  AND b.enabled = TRUE
  AND c.enabled = TRUE
  AND b.transport = 'daemon_dispatch'
  AND b.subscription_provider = sqlc.arg(provider)
  AND c.credential_type = 'subscription_bundle'
  AND c.subscription_provider = sqlc.arg(provider)
  AND c.validation_status = 'active'
  AND ar.status = 'online'
  AND ar.provider = CASE WHEN sqlc.arg(provider) = 'claude_code' THEN 'claude' ELSE sqlc.arg(provider) END
RETURNING *;

-- name: ClaimGatewayRuntimeRequest :one
UPDATE gateway_runtime_request r
SET status = 'running', claimed_at = now(), updated_at = now()
WHERE r.id = (
    SELECT r2.id
    FROM gateway_runtime_request r2
    JOIN agent_runtime ar ON ar.id = r2.runtime_id
    JOIN member m ON m.workspace_id = ar.workspace_id AND m.user_id = ar.owner_id
    WHERE r2.workspace_id = $1
      AND r2.runtime_id = $2
      AND r2.provider = $3
      AND r2.status = 'queued'
      AND ar.workspace_id = r2.workspace_id
      AND ar.provider = CASE WHEN r2.provider = 'claude_code' THEN 'claude' ELSE r2.provider END
      AND ar.status = 'online'
    ORDER BY r2.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteGatewayRuntimeRequest :one
UPDATE gateway_runtime_request
SET
    status = 'completed',
    response_body = $4,
    error_type = '',
    error_message = '',
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1
  AND id = $2
  AND runtime_id = $3
  AND status = 'running'
RETURNING *;

-- name: FailGatewayRuntimeRequest :one
UPDATE gateway_runtime_request
SET
    status = 'failed',
    error_type = $4,
    error_message = $5,
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1
  AND id = $2
  AND runtime_id = $3
  AND status = 'running'
RETURNING *;

-- name: GetGatewayRuntimeRequest :one
SELECT * FROM gateway_runtime_request
WHERE workspace_id = $1 AND id = $2;
