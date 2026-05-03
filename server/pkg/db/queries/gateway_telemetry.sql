-- name: CreateGatewaySession :one
INSERT INTO gateway_session (
    workspace_id, user_id, agent_id, task_id, trace_id, root_span_id,
    name, client_protocol, client_tool_hint, service_name, tags,
    status, resource_attributes
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: CompleteGatewaySession :one
UPDATE gateway_session
SET
    status = $3,
    ended_at = $4,
    duration_ms = $5,
    span_count = $6,
    error_count = $7,
    total_cost = $8
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: ListGatewaySessions :many
SELECT * FROM gateway_session
WHERE workspace_id = $1
  AND started_at >= @since::timestamptz
ORDER BY started_at DESC
LIMIT $2;

-- name: GetGatewaySession :one
SELECT * FROM gateway_session
WHERE workspace_id = $1 AND id = $2;

-- name: CreateGatewayRequest :one
INSERT INTO gateway_request (
    session_id, workspace_id, user_id, backend_id, route, method,
    model_requested, model_forwarded, provider_slug, streaming,
    status, http_status, capture_policy, request_metadata
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: CompleteGatewayRequest :one
UPDATE gateway_request
SET
    status = $3,
    http_status = $4,
    latency_ms = $5,
    error_type = $6,
    error_message = $7,
    response_metadata = $8,
    completed_at = now()
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: CreateGatewayModelCall :one
INSERT INTO gateway_model_call (
    request_id, session_id, workspace_id, backend_id, provider_slug,
    request_model, response_model, request_type, streaming,
    prompt_messages, completion_messages, completion_chunks,
    prompt_tokens, completion_tokens, total_tokens,
    cache_creation_input_tokens, cache_read_input_tokens, reasoning_tokens, streaming_tokens,
    usage_source, prompt_cost, completion_cost, total_cost,
    response_id, finish_reason, stop_reason,
    time_to_first_token_ms, time_to_generate_ms, streaming_duration_ms, streaming_chunk_count
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18, $19,
    $20, $21, $22, $23,
    $24, $25, $26,
    $27, $28, $29, $30
)
RETURNING *;

-- name: CreateGatewaySpan :one
INSERT INTO gateway_span (
    session_id, request_id, workspace_id, trace_id, span_id, parent_span_id,
    span_kind, name, service_name, status_code, status_message,
    started_at, ended_at, duration_ms, attributes, resource_attributes
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: ListGatewaySpansForSession :many
SELECT * FROM gateway_span
WHERE workspace_id = $1 AND session_id = $2
ORDER BY started_at;

-- name: CreateGatewayEvent :one
INSERT INTO gateway_event (workspace_id, session_id, request_id, span_id, event_type, payload, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CreateGatewayLog :one
INSERT INTO gateway_log (workspace_id, session_id, request_id, span_id, severity, body, attributes, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateGatewayAgentObservation :one
INSERT INTO gateway_agent_observation (
    workspace_id, session_id, span_row_id, agent_id, agent_name, role,
    models, tools, handoff_source, handoff_destination, reasoning_summary
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: CreateGatewayToolObservation :one
INSERT INTO gateway_tool_observation (
    workspace_id, session_id, span_row_id, tool_id, tool_name,
    description, parameters, result, status, duration_ms
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;
