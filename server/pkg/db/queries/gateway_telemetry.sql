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

-- name: GetGatewayOverviewSummary :one
WITH filtered_sessions AS (
    SELECT s.*
    FROM gateway_session s
    WHERE s.workspace_id = $1
      AND s.started_at >= @since::timestamptz
      AND (@status::text = '' OR s.status = @status::text)
),
filtered_requests AS (
    SELECT r.*
    FROM gateway_request r
    JOIN filtered_sessions s ON s.id = r.session_id
    WHERE @backend::text = '' OR r.provider_slug = @backend::text
),
filtered_model_calls AS (
    SELECT m.*
    FROM gateway_model_call m
    JOIN filtered_requests r ON r.id = m.request_id
    WHERE @model::text = ''
       OR m.request_model = @model::text
       OR m.response_model = @model::text
),
active_sessions AS (
    SELECT s.id
    FROM filtered_sessions s
    WHERE (@backend::text = '' AND @model::text = '')
       OR (@model::text = '' AND EXISTS (
            SELECT 1 FROM filtered_requests r WHERE r.session_id = s.id
       ))
       OR EXISTS (
            SELECT 1 FROM filtered_model_calls m WHERE m.session_id = s.id
       )
)
SELECT
    (SELECT count(*) FROM active_sessions)::bigint AS session_count,
    CASE
        WHEN @model::text = '' THEN (SELECT count(*) FROM filtered_requests)::bigint
        ELSE (SELECT count(DISTINCT request_id) FROM filtered_model_calls)::bigint
    END AS request_count,
    (SELECT count(*) FROM filtered_model_calls)::bigint AS llm_call_count,
    COALESCE((SELECT sum(prompt_tokens) FROM filtered_model_calls), 0)::bigint AS prompt_tokens,
    COALESCE((SELECT sum(completion_tokens) FROM filtered_model_calls), 0)::bigint AS completion_tokens,
    COALESCE((SELECT sum(total_tokens) FROM filtered_model_calls), 0)::bigint AS total_tokens,
    COALESCE((SELECT sum(total_cost) FROM filtered_model_calls), 0)::numeric AS total_cost,
    COALESCE((SELECT sum(CASE WHEN status IN ('upstream_error', 'gateway_error', 'policy_blocked', 'client_cancelled') THEN 1 ELSE 0 END) FROM filtered_requests), 0)::bigint AS error_count,
    COALESCE((SELECT sum(CASE WHEN streaming THEN 1 ELSE 0 END) FROM filtered_requests), 0)::bigint AS streaming_request_count,
    COALESCE((SELECT avg(latency_ms) FILTER (WHERE latency_ms IS NOT NULL) FROM filtered_requests), 0)::bigint AS avg_latency_ms;

-- name: ListGatewayOverviewBuckets :many
SELECT
    date_trunc(@bucket_width::text, r.created_at)::timestamptz AS bucket_start,
    count(DISTINCT r.id)::bigint AS request_count,
    COALESCE(sum(CASE WHEN r.status IN ('upstream_error', 'gateway_error', 'policy_blocked', 'client_cancelled') THEN 1 ELSE 0 END), 0)::bigint AS error_count,
    COALESCE(sum(m.total_tokens), 0)::bigint AS total_tokens,
    COALESCE(sum(m.total_cost), 0)::numeric AS total_cost
FROM gateway_request r
JOIN gateway_session s ON s.id = r.session_id
LEFT JOIN gateway_model_call m ON m.request_id = r.id
WHERE r.workspace_id = $1
  AND r.created_at >= @since::timestamptz
  AND (@status::text = '' OR r.status = @status::text OR s.status = @status::text)
  AND (@backend::text = '' OR r.provider_slug = @backend::text)
  AND (@model::text = '' OR m.request_model = @model::text OR m.response_model = @model::text)
GROUP BY bucket_start
ORDER BY bucket_start ASC;

-- name: ListGatewayTopModels :many
SELECT
    COALESCE(NULLIF(m.response_model, ''), NULLIF(m.request_model, ''), '(unknown)')::text AS model,
    count(*)::bigint AS call_count,
    COALESCE(sum(m.total_tokens), 0)::bigint AS total_tokens,
    COALESCE(sum(m.total_cost), 0)::numeric AS total_cost
FROM gateway_model_call m
JOIN gateway_request r ON r.id = m.request_id
JOIN gateway_session s ON s.id = m.session_id
WHERE m.workspace_id = $1
  AND m.created_at >= @since::timestamptz
  AND (@status::text = '' OR r.status = @status::text OR s.status = @status::text)
  AND (@backend::text = '' OR m.provider_slug = @backend::text)
  AND (@model::text = '' OR m.request_model = @model::text OR m.response_model = @model::text)
GROUP BY model
ORDER BY call_count DESC, total_tokens DESC, model ASC
LIMIT $2;

-- name: ListGatewayTopBackends :many
SELECT
    COALESCE(NULLIF(r.provider_slug, ''), '(unknown)')::text AS backend,
    count(*)::bigint AS call_count,
    COALESCE(sum(CASE WHEN r.status IN ('upstream_error', 'gateway_error', 'policy_blocked', 'client_cancelled') THEN 1 ELSE 0 END), 0)::bigint AS error_count,
    COALESCE(avg(r.latency_ms) FILTER (WHERE r.latency_ms IS NOT NULL), 0)::bigint AS avg_latency_ms,
    COALESCE(sum(m.total_tokens), 0)::bigint AS total_tokens,
    COALESCE(sum(m.total_cost), 0)::numeric AS total_cost
FROM gateway_request r
JOIN gateway_session s ON s.id = r.session_id
LEFT JOIN gateway_model_call m ON m.request_id = r.id
WHERE r.workspace_id = $1
  AND r.created_at >= @since::timestamptz
  AND (@status::text = '' OR r.status = @status::text OR s.status = @status::text)
  AND (@backend::text = '' OR r.provider_slug = @backend::text)
  AND (@model::text = '' OR m.request_model = @model::text OR m.response_model = @model::text)
GROUP BY backend
ORDER BY call_count DESC, error_count DESC, backend ASC
LIMIT $2;

-- name: ListGatewaySessionsDashboard :many
WITH request_stats AS (
    SELECT
        session_id,
        count(*)::bigint AS request_count,
        COALESCE(sum(CASE WHEN status IN ('upstream_error', 'gateway_error', 'policy_blocked', 'client_cancelled') THEN 1 ELSE 0 END), 0)::bigint AS request_error_count,
        COALESCE(sum(CASE WHEN streaming THEN 1 ELSE 0 END), 0)::bigint AS streaming_request_count,
        COALESCE(avg(latency_ms) FILTER (WHERE latency_ms IS NOT NULL), 0)::bigint AS avg_latency_ms,
        array_remove(array_agg(DISTINCT NULLIF(provider_slug, '')), NULL)::text[] AS backends
    FROM gateway_request
    WHERE workspace_id = $1
    GROUP BY session_id
),
model_stats AS (
    SELECT
        session_id,
        count(*)::bigint AS llm_call_count,
        COALESCE(sum(prompt_tokens), 0)::bigint AS prompt_tokens,
        COALESCE(sum(completion_tokens), 0)::bigint AS completion_tokens,
        COALESCE(sum(total_tokens), 0)::bigint AS total_tokens,
        COALESCE(sum(total_cost), 0)::numeric AS total_cost,
        array_remove(array_agg(DISTINCT COALESCE(NULLIF(response_model, ''), NULLIF(request_model, ''))), NULL)::text[] AS models
    FROM gateway_model_call
    WHERE workspace_id = $1
    GROUP BY session_id
)
SELECT
    s.id, s.workspace_id, s.user_id, s.agent_id, s.task_id, s.trace_id, s.root_span_id,
    s.name, s.client_protocol, s.client_tool_hint, s.service_name, s.tags, s.status,
    s.started_at, s.ended_at, s.duration_ms, s.span_count, s.error_count, s.total_cost,
    s.resource_attributes,
    COALESCE(rs.request_count, 0)::bigint AS request_count,
    COALESCE(ms.llm_call_count, 0)::bigint AS llm_call_count,
    COALESCE(ms.prompt_tokens, 0)::bigint AS prompt_tokens,
    COALESCE(ms.completion_tokens, 0)::bigint AS completion_tokens,
    COALESCE(ms.total_tokens, 0)::bigint AS total_tokens,
    COALESCE(ms.total_cost, 0)::numeric AS usage_cost,
    COALESCE(rs.request_error_count, 0)::bigint AS request_error_count,
    COALESCE(rs.streaming_request_count, 0)::bigint AS streaming_request_count,
    COALESCE(rs.avg_latency_ms, 0)::bigint AS avg_latency_ms,
    COALESCE(ms.models, ARRAY[]::text[]) AS models,
    COALESCE(rs.backends, ARRAY[]::text[]) AS backends,
    count(*) OVER()::bigint AS total_count
FROM gateway_session s
LEFT JOIN request_stats rs ON rs.session_id = s.id
LEFT JOIN model_stats ms ON ms.session_id = s.id
WHERE s.workspace_id = $1
  AND s.started_at >= @since::timestamptz
  AND (@status::text = '' OR s.status = @status::text)
  AND (@backend::text = '' OR EXISTS (
      SELECT 1 FROM gateway_request r
      WHERE r.session_id = s.id AND r.provider_slug = @backend::text
  ))
ORDER BY s.started_at DESC
LIMIT $2;

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

-- name: ListGatewayRequestsForSession :many
SELECT * FROM gateway_request
WHERE workspace_id = $1 AND session_id = $2
ORDER BY created_at;

-- name: ListGatewayModelCallsForSession :many
SELECT * FROM gateway_model_call
WHERE workspace_id = $1 AND session_id = $2
ORDER BY created_at;

-- name: ListGatewayEventsForSession :many
SELECT * FROM gateway_event
WHERE workspace_id = $1 AND session_id = $2
ORDER BY occurred_at;

-- name: ListGatewayLogsForSession :many
SELECT * FROM gateway_log
WHERE workspace_id = $1 AND session_id = $2
ORDER BY occurred_at;

-- name: ListGatewayAgentObservationsForSession :many
SELECT * FROM gateway_agent_observation
WHERE workspace_id = $1 AND session_id = $2
ORDER BY created_at;

-- name: ListGatewayToolObservationsForSession :many
SELECT * FROM gateway_tool_observation
WHERE workspace_id = $1 AND session_id = $2
ORDER BY created_at;

-- name: ListGatewayLLMCalls :many
SELECT
    m.id, m.request_id, m.session_id, m.workspace_id, m.backend_id, m.provider_slug,
    m.request_model, m.response_model, m.request_type, m.streaming,
    m.prompt_messages, m.completion_messages, m.completion_chunks,
    m.prompt_tokens, m.completion_tokens, m.total_tokens,
    m.cache_creation_input_tokens, m.cache_read_input_tokens, m.reasoning_tokens, m.streaming_tokens,
    m.usage_source, m.prompt_cost, m.completion_cost, m.total_cost,
    m.response_id, m.finish_reason, m.stop_reason,
    m.time_to_first_token_ms, m.time_to_generate_ms, m.streaming_duration_ms, m.streaming_chunk_count,
    m.created_at,
    s.trace_id, s.name AS session_name,
    r.route, r.status AS request_status, r.http_status, r.latency_ms, r.capture_policy,
    count(*) OVER()::bigint AS total_count
FROM gateway_model_call m
JOIN gateway_session s ON s.id = m.session_id
JOIN gateway_request r ON r.id = m.request_id
WHERE m.workspace_id = $1
  AND m.created_at >= @since::timestamptz
  AND (@status::text = '' OR r.status = @status::text OR s.status = @status::text)
  AND (@backend::text = '' OR m.provider_slug = @backend::text)
  AND (@model::text = '' OR m.request_model = @model::text OR m.response_model = @model::text)
ORDER BY m.created_at DESC
LIMIT $2;

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

-- name: UpsertGatewayMetricRollup :one
INSERT INTO gateway_metric_rollup (
    workspace_id, bucket_start, bucket_width, user_id, backend_id, model, agent_id,
    prompt_tokens, completion_tokens, cache_tokens, reasoning_tokens, total_cost,
    request_count, error_count, latency_p50_ms, latency_p95_ms, latency_p99_ms
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (workspace_id, bucket_start, bucket_width, user_id, backend_id, model, agent_id)
DO UPDATE SET
    prompt_tokens = EXCLUDED.prompt_tokens,
    completion_tokens = EXCLUDED.completion_tokens,
    cache_tokens = EXCLUDED.cache_tokens,
    reasoning_tokens = EXCLUDED.reasoning_tokens,
    total_cost = EXCLUDED.total_cost,
    request_count = EXCLUDED.request_count,
    error_count = EXCLUDED.error_count,
    latency_p50_ms = EXCLUDED.latency_p50_ms,
    latency_p95_ms = EXCLUDED.latency_p95_ms,
    latency_p99_ms = EXCLUDED.latency_p99_ms
RETURNING *;

-- name: ListGatewayMetricRollups :many
SELECT * FROM gateway_metric_rollup
WHERE workspace_id = $1
  AND bucket_start >= @since::timestamptz
  AND bucket_width = $3
ORDER BY bucket_start DESC
LIMIT $2;
