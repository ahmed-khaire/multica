-- name: CreateAISystemInventory :one
INSERT INTO ai_system_inventory (
    workspace_id, name, owner_user_id, intended_purpose, business_process,
    autonomy_level, external_impact_level, data_domains, risk_classification,
    approval_state, linked_agent_ids, linked_backend_ids, linked_tool_refs
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListAISystemInventory :many
SELECT * FROM ai_system_inventory
WHERE workspace_id = $1
ORDER BY updated_at DESC;

-- name: UpsertAIThirdPartyRisk :one
INSERT INTO ai_third_party_risk (
    workspace_id, backend_id, provider_name, owner_user_id,
    approved_use_cases, data_categories, regions, hosting_notes,
    contract_status, security_review_status, evidence_links,
    limitations, prohibited_uses, model_list, capability_class,
    risk_score, review_cadence_days, last_assessment_at, next_review_at,
    active_exception_count
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
ON CONFLICT (workspace_id, provider_name)
DO UPDATE SET
    backend_id = EXCLUDED.backend_id,
    owner_user_id = EXCLUDED.owner_user_id,
    approved_use_cases = EXCLUDED.approved_use_cases,
    data_categories = EXCLUDED.data_categories,
    regions = EXCLUDED.regions,
    hosting_notes = EXCLUDED.hosting_notes,
    contract_status = EXCLUDED.contract_status,
    security_review_status = EXCLUDED.security_review_status,
    evidence_links = EXCLUDED.evidence_links,
    limitations = EXCLUDED.limitations,
    prohibited_uses = EXCLUDED.prohibited_uses,
    model_list = EXCLUDED.model_list,
    capability_class = EXCLUDED.capability_class,
    risk_score = EXCLUDED.risk_score,
    review_cadence_days = EXCLUDED.review_cadence_days,
    last_assessment_at = EXCLUDED.last_assessment_at,
    next_review_at = EXCLUDED.next_review_at,
    active_exception_count = EXCLUDED.active_exception_count,
    updated_at = now()
RETURNING *;

-- name: ListAIThirdPartyRisk :many
SELECT * FROM ai_third_party_risk
WHERE workspace_id = $1
ORDER BY risk_score DESC, next_review_at NULLS FIRST, provider_name;

-- name: UpsertAIControlMapping :one
INSERT INTO ai_control_mapping (
    workspace_id, framework, control_id, control_title,
    mapped_policy_ids, mapped_evidence_queries, status, owner_user_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (workspace_id, framework, control_id)
DO UPDATE SET
    control_title = EXCLUDED.control_title,
    mapped_policy_ids = EXCLUDED.mapped_policy_ids,
    mapped_evidence_queries = EXCLUDED.mapped_evidence_queries,
    status = EXCLUDED.status,
    owner_user_id = EXCLUDED.owner_user_id,
    updated_at = now()
RETURNING *;

-- name: ListAIControlMappingsWithEvidence :many
SELECT
    m.*,
    COALESCE(evidence.evidence_count, 0)::bigint AS evidence_count,
    evidence.last_evidence_generated_at
FROM ai_control_mapping m
LEFT JOIN LATERAL (
    SELECT
        count(*)::bigint AS evidence_count,
        max(e.generated_at) AS last_evidence_generated_at
    FROM ai_evidence e
    WHERE e.workspace_id = m.workspace_id
      AND (
        jsonb_exists(e.framework_refs, m.framework)
        OR jsonb_exists(e.framework_refs, m.framework || ':' || m.control_id)
      )
) evidence ON true
WHERE m.workspace_id = $1
ORDER BY m.framework, m.control_id;

-- name: CreateAIEvidence :one
INSERT INTO ai_evidence (
    workspace_id, evidence_type, framework_refs,
    linked_request_id, linked_session_id, linked_span_row_id,
    linked_policy_id, linked_backend_id, linked_provider_risk_id,
    summary, payload, attachment_ref, retain_until
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListAIEvidence :many
SELECT * FROM ai_evidence
WHERE workspace_id = $1
ORDER BY generated_at DESC
LIMIT $2;

-- name: CreateAIPolicyException :one
INSERT INTO ai_policy_exception (
    workspace_id, policy_id, requester_user_id, approver_user_id,
    reason, scope, status, expires_at, evidence_references
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAIPolicyExceptions :many
SELECT * FROM ai_policy_exception
WHERE workspace_id = $1
ORDER BY updated_at DESC
LIMIT $2;

-- name: UpdateAIPolicyException :one
UPDATE ai_policy_exception
SET
    status = $3,
    approver_user_id = CASE
        WHEN $3 IN ('approved', 'denied', 'revoked') THEN $2
        ELSE approver_user_id
    END,
    expires_at = $4,
    updated_at = now()
WHERE workspace_id = $1
  AND id = $5
RETURNING *;

-- name: GetActiveAIPolicyExceptionForProvider :one
SELECT * FROM ai_policy_exception
WHERE workspace_id = $1
  AND status = 'approved'
  AND (expires_at IS NULL OR expires_at > now())
  AND (scope @> $2 OR scope @> $3)
ORDER BY updated_at DESC
LIMIT 1;

-- name: CreateAIIncident :one
INSERT INTO ai_incident (
    workspace_id, severity, category, linked_request_id, linked_session_id,
    linked_span_row_id, linked_policy_id, linked_provider_risk_id,
    summary, status, remediation_notes
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAIIncidents :many
SELECT * FROM ai_incident
WHERE workspace_id = $1
ORDER BY opened_at DESC
LIMIT $2;

-- name: UpdateAIIncident :one
UPDATE ai_incident
SET
    status = $3,
    remediation_notes = $4,
    closed_at = CASE WHEN $3 = 'closed' THEN now() ELSE closed_at END
WHERE workspace_id = $1
  AND id = $2
RETURNING *;

-- name: CreateAIAuditLog :one
INSERT INTO ai_audit_log (
    workspace_id, actor_user_id, action, target_type, target_id,
    before_state, after_state, request_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListGatewayAuditLog :many
SELECT
    l.id,
    l.workspace_id,
    l.actor_user_id,
    COALESCE(u.name, '')::text AS actor_name,
    COALESCE(u.email, '')::text AS actor_email,
    l.action,
    l.target_type,
    l.target_id,
    l.before_state,
    l.after_state,
    l.request_id,
    l.created_at
FROM ai_audit_log l
LEFT JOIN "user" u ON u.id = l.actor_user_id
WHERE l.workspace_id = $1
  AND l.action LIKE 'gateway.%'
ORDER BY l.created_at DESC
LIMIT $2;

-- name: CreateAIEvidenceExport :one
INSERT INTO ai_evidence_export (
    id, workspace_id, actor_user_id, export_type, subject_type, subject_id,
    digest_sha256, sections, bundle_snapshot
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListAIEvidenceExports :many
SELECT
    e.id,
    e.workspace_id,
    e.actor_user_id,
    COALESCE(u.name, '')::text AS actor_name,
    COALESCE(u.email, '')::text AS actor_email,
    e.export_type,
    e.subject_type,
    e.subject_id,
    e.digest_sha256,
    e.sections,
    e.created_at
FROM ai_evidence_export e
LEFT JOIN "user" u ON u.id = e.actor_user_id
WHERE e.workspace_id = $1
ORDER BY e.created_at DESC
LIMIT $2;

-- name: GetAIEvidenceExport :one
SELECT
    e.id,
    e.workspace_id,
    e.actor_user_id,
    COALESCE(u.name, '')::text AS actor_name,
    COALESCE(u.email, '')::text AS actor_email,
    e.export_type,
    e.subject_type,
    e.subject_id,
    e.digest_sha256,
    e.sections,
    e.bundle_snapshot,
    e.created_at
FROM ai_evidence_export e
LEFT JOIN "user" u ON u.id = e.actor_user_id
WHERE e.workspace_id = $1
  AND e.id = $2;
