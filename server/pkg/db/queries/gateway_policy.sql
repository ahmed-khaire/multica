-- name: CreateGatewayPolicy :one
INSERT INTO gateway_policy (
    workspace_id, name, description, policy_type, enabled,
    version, rule_definition, enforcement_mode, created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
RETURNING *;

-- name: ListGatewayPolicies :many
SELECT * FROM gateway_policy
WHERE workspace_id = $1
  AND archived_at IS NULL
ORDER BY enabled DESC, name;

-- name: ListEnabledGatewayPolicies :many
SELECT * FROM gateway_policy
WHERE workspace_id = $1
  AND enabled = TRUE
  AND archived_at IS NULL
ORDER BY policy_type, name;

-- name: GetGatewayPolicy :one
SELECT * FROM gateway_policy
WHERE workspace_id = $1 AND id = $2 AND archived_at IS NULL;

-- name: UpdateGatewayPolicy :one
UPDATE gateway_policy
SET
    name = $3,
    description = $4,
    policy_type = $5,
    enabled = $6,
    version = version + 1,
    rule_definition = $7,
    enforcement_mode = $8,
    updated_by = $9,
    updated_at = now()
WHERE workspace_id = $1 AND id = $2
  AND archived_at IS NULL
RETURNING *;

-- name: ArchiveGatewayPolicy :one
UPDATE gateway_policy
SET
    enabled = FALSE,
    archived_at = now(),
    updated_by = $3,
    updated_at = now()
WHERE workspace_id = $1
  AND id = $2
  AND archived_at IS NULL
RETURNING *;

-- name: RecordGatewayPolicyDecision :one
INSERT INTO gateway_policy_decision (
    workspace_id, policy_id, policy_version, subject_user_id, subject_agent_id,
    resource_type, resource_id, resource_label, decision, reason_code,
    matched_rules, request_id, session_id, span_row_id, approval_status, evidence_references
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: ListGatewayPolicyDecisions :many
SELECT * FROM gateway_policy_decision
WHERE workspace_id = $1
  AND created_at >= @since::timestamptz
ORDER BY created_at DESC
LIMIT $2;

-- name: GetGatewayPolicyDecision :one
SELECT * FROM gateway_policy_decision
WHERE workspace_id = $1 AND id = $2;

-- name: UpdateGatewayPolicyDecisionApprovalStatus :one
UPDATE gateway_policy_decision
SET approval_status = $3
WHERE workspace_id = $1 AND id = $2
RETURNING *;
