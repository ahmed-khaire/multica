CREATE TABLE gateway_workspace_settings (
    workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    capture_policy TEXT NOT NULL DEFAULT 'redacted_content'
        CHECK (capture_policy IN ('metadata_only', 'redacted_content', 'full_content')),
    default_backend_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE gateway_backend (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    display_name TEXT NOT NULL,
    backend_type TEXT NOT NULL
        CHECK (backend_type IN ('openai_compatible', 'anthropic', 'claude_oauth')),
    base_url TEXT NOT NULL,
    encrypted_credential BYTEA NOT NULL,
    credential_hint TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

ALTER TABLE gateway_workspace_settings
    ADD CONSTRAINT gateway_workspace_settings_default_backend_fk
    FOREIGN KEY (default_backend_id) REFERENCES gateway_backend(id) ON DELETE SET NULL;

CREATE INDEX idx_gateway_backend_workspace_enabled
    ON gateway_backend(workspace_id, enabled);

CREATE TABLE gateway_user_key (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    encrypted_key_value BYTEA NOT NULL,
    key_prefix TEXT NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_gateway_user_key_one_active
    ON gateway_user_key(workspace_id, user_id)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_gateway_user_key_workspace_user
    ON gateway_user_key(workspace_id, user_id);

CREATE TABLE gateway_policy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    policy_type TEXT NOT NULL
        CHECK (policy_type IN ('provider', 'model', 'tool', 'data', 'budget', 'approval', 'routing', 'capture')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    version INT NOT NULL DEFAULT 1,
    rule_definition JSONB NOT NULL DEFAULT '{}',
    enforcement_mode TEXT NOT NULL DEFAULT 'enforce'
        CHECK (enforcement_mode IN ('monitor', 'enforce')),
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE INDEX idx_gateway_policy_workspace_enabled
    ON gateway_policy(workspace_id, enabled);

CREATE TABLE gateway_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    trace_id TEXT NOT NULL,
    root_span_id TEXT,
    name TEXT NOT NULL DEFAULT 'Gateway session',
    client_protocol TEXT NOT NULL DEFAULT 'unknown'
        CHECK (client_protocol IN ('openai', 'anthropic', 'multica_trace', 'unknown')),
    client_tool_hint TEXT NOT NULL DEFAULT '',
    service_name TEXT NOT NULL DEFAULT 'multica-gateway',
    tags JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'success', 'error', 'cancelled', 'unknown')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,
    duration_ms BIGINT,
    span_count INT NOT NULL DEFAULT 0,
    error_count INT NOT NULL DEFAULT 0,
    total_cost NUMERIC(18, 8),
    resource_attributes JSONB NOT NULL DEFAULT '{}',
    UNIQUE (workspace_id, trace_id)
);

CREATE INDEX idx_gateway_session_workspace_started
    ON gateway_session(workspace_id, started_at DESC);
CREATE INDEX idx_gateway_session_workspace_user
    ON gateway_session(workspace_id, user_id, started_at DESC);
CREATE INDEX idx_gateway_session_workspace_agent
    ON gateway_session(workspace_id, agent_id, started_at DESC);

CREATE TABLE gateway_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES gateway_session(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    backend_id UUID REFERENCES gateway_backend(id) ON DELETE SET NULL,
    route TEXT NOT NULL,
    method TEXT NOT NULL,
    model_requested TEXT NOT NULL DEFAULT '',
    model_forwarded TEXT NOT NULL DEFAULT '',
    provider_slug TEXT NOT NULL DEFAULT '',
    streaming BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'success', 'upstream_error', 'gateway_error', 'policy_blocked', 'client_cancelled')),
    http_status INT,
    latency_ms BIGINT,
    error_type TEXT,
    error_message TEXT,
    capture_policy TEXT NOT NULL
        CHECK (capture_policy IN ('metadata_only', 'redacted_content', 'full_content')),
    request_metadata JSONB NOT NULL DEFAULT '{}',
    response_metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_gateway_request_workspace_created
    ON gateway_request(workspace_id, created_at DESC);
CREATE INDEX idx_gateway_request_session
    ON gateway_request(session_id, created_at DESC);
CREATE INDEX idx_gateway_request_backend_model
    ON gateway_request(workspace_id, backend_id, model_forwarded, created_at DESC);

CREATE TABLE gateway_model_call (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES gateway_request(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES gateway_session(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID REFERENCES gateway_backend(id) ON DELETE SET NULL,
    provider_slug TEXT NOT NULL DEFAULT '',
    request_model TEXT NOT NULL DEFAULT '',
    response_model TEXT NOT NULL DEFAULT '',
    request_type TEXT NOT NULL DEFAULT 'chat',
    streaming BOOLEAN NOT NULL DEFAULT FALSE,
    prompt_messages JSONB,
    completion_messages JSONB,
    completion_chunks JSONB,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    total_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_input_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_input_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    streaming_tokens BIGINT NOT NULL DEFAULT 0,
    usage_source TEXT NOT NULL DEFAULT 'unknown'
        CHECK (usage_source IN ('upstream', 'estimated', 'unknown')),
    prompt_cost NUMERIC(18, 8),
    completion_cost NUMERIC(18, 8),
    total_cost NUMERIC(18, 8),
    response_id TEXT,
    finish_reason TEXT,
    stop_reason TEXT,
    time_to_first_token_ms BIGINT,
    time_to_generate_ms BIGINT,
    streaming_duration_ms BIGINT,
    streaming_chunk_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_model_call_workspace_created
    ON gateway_model_call(workspace_id, created_at DESC);
CREATE INDEX idx_gateway_model_call_backend_model
    ON gateway_model_call(workspace_id, backend_id, request_model, created_at DESC);
CREATE INDEX idx_gateway_model_call_session
    ON gateway_model_call(session_id, created_at DESC);

CREATE TABLE gateway_span (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES gateway_session(id) ON DELETE CASCADE,
    request_id UUID REFERENCES gateway_request(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    parent_span_id TEXT,
    span_kind TEXT NOT NULL DEFAULT 'unknown'
        CHECK (span_kind IN ('workflow', 'session', 'task', 'operation', 'agent', 'tool', 'llm', 'chain', 'text', 'guardrail', 'http', 'unknown')),
    name TEXT NOT NULL,
    service_name TEXT NOT NULL DEFAULT 'multica-gateway',
    status_code TEXT NOT NULL DEFAULT 'unset'
        CHECK (status_code IN ('unset', 'ok', 'error')),
    status_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_ms BIGINT,
    attributes JSONB NOT NULL DEFAULT '{}',
    resource_attributes JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, trace_id, span_id)
);

CREATE INDEX idx_gateway_span_session_started
    ON gateway_span(session_id, started_at);
CREATE INDEX idx_gateway_span_workspace_kind
    ON gateway_span(workspace_id, span_kind, started_at DESC);

CREATE TABLE gateway_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id UUID REFERENCES gateway_session(id) ON DELETE CASCADE,
    request_id UUID REFERENCES gateway_request(id) ON DELETE CASCADE,
    span_id UUID REFERENCES gateway_span(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_event_session
    ON gateway_event(session_id, occurred_at);

CREATE TABLE gateway_span_link (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    linked_trace_id TEXT NOT NULL,
    linked_span_id TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_span_link_trace
    ON gateway_span_link(workspace_id, trace_id, span_id);

CREATE TABLE gateway_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id UUID REFERENCES gateway_session(id) ON DELETE CASCADE,
    request_id UUID REFERENCES gateway_request(id) ON DELETE CASCADE,
    span_id UUID REFERENCES gateway_span(id) ON DELETE CASCADE,
    severity TEXT NOT NULL DEFAULT 'info'
        CHECK (severity IN ('trace', 'debug', 'info', 'warn', 'error', 'fatal')),
    body TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_log_session
    ON gateway_log(session_id, occurred_at);

CREATE TABLE gateway_agent_observation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES gateway_session(id) ON DELETE CASCADE,
    span_row_id UUID REFERENCES gateway_span(id) ON DELETE SET NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL DEFAULT '',
    models JSONB NOT NULL DEFAULT '[]',
    tools JSONB NOT NULL DEFAULT '[]',
    handoff_source TEXT NOT NULL DEFAULT '',
    handoff_destination TEXT NOT NULL DEFAULT '',
    reasoning_summary TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_agent_observation_session
    ON gateway_agent_observation(session_id, created_at);

CREATE TABLE gateway_tool_observation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES gateway_session(id) ON DELETE CASCADE,
    span_row_id UUID REFERENCES gateway_span(id) ON DELETE SET NULL,
    tool_id TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    parameters JSONB,
    result JSONB,
    status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (status IN ('success', 'error', 'blocked', 'unknown')),
    duration_ms BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_tool_observation_session
    ON gateway_tool_observation(session_id, created_at);

CREATE TABLE gateway_metric_rollup (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_width TEXT NOT NULL DEFAULT 'hour'
        CHECK (bucket_width IN ('hour', 'day')),
    user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    backend_id UUID REFERENCES gateway_backend(id) ON DELETE SET NULL,
    model TEXT NOT NULL DEFAULT '',
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    prompt_tokens BIGINT NOT NULL DEFAULT 0,
    completion_tokens BIGINT NOT NULL DEFAULT 0,
    cache_tokens BIGINT NOT NULL DEFAULT 0,
    reasoning_tokens BIGINT NOT NULL DEFAULT 0,
    total_cost NUMERIC(18, 8),
    request_count BIGINT NOT NULL DEFAULT 0,
    error_count BIGINT NOT NULL DEFAULT 0,
    latency_p50_ms BIGINT,
    latency_p95_ms BIGINT,
    latency_p99_ms BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, bucket_start, bucket_width, user_id, backend_id, model, agent_id)
);

CREATE INDEX idx_gateway_metric_rollup_workspace_bucket
    ON gateway_metric_rollup(workspace_id, bucket_start DESC);

CREATE TABLE gateway_model_pricing (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_slug TEXT NOT NULL,
    backend_type TEXT NOT NULL
        CHECK (backend_type IN ('openai_compatible', 'anthropic', 'claude_oauth')),
    model_pattern TEXT NOT NULL,
    input_token_price_per_million NUMERIC(18, 8),
    output_token_price_per_million NUMERIC(18, 8),
    cache_read_token_price_per_million NUMERIC(18, 8),
    cache_write_token_price_per_million NUMERIC(18, 8),
    currency TEXT NOT NULL DEFAULT 'USD',
    effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_model_pricing_lookup
    ON gateway_model_pricing(provider_slug, backend_type, model_pattern, effective_at DESC);

CREATE TABLE gateway_policy_decision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES gateway_policy(id) ON DELETE SET NULL,
    policy_version INT,
    subject_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    subject_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL DEFAULT '',
    resource_label TEXT NOT NULL DEFAULT '',
    decision TEXT NOT NULL
        CHECK (decision IN ('allow', 'warn', 'require_approval', 'redact', 'route_to_backend', 'block')),
    reason_code TEXT NOT NULL,
    matched_rules JSONB NOT NULL DEFAULT '[]',
    request_id UUID REFERENCES gateway_request(id) ON DELETE SET NULL,
    session_id UUID REFERENCES gateway_session(id) ON DELETE SET NULL,
    span_row_id UUID REFERENCES gateway_span(id) ON DELETE SET NULL,
    approval_status TEXT
        CHECK (approval_status IN ('requested', 'approved', 'denied', 'expired')),
    evidence_references JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_policy_decision_workspace_created
    ON gateway_policy_decision(workspace_id, created_at DESC);
CREATE INDEX idx_gateway_policy_decision_resource
    ON gateway_policy_decision(workspace_id, resource_type, resource_label, created_at DESC);

CREATE TABLE ai_system_inventory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    owner_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    intended_purpose TEXT NOT NULL DEFAULT '',
    business_process TEXT NOT NULL DEFAULT '',
    autonomy_level TEXT NOT NULL DEFAULT 'assistive'
        CHECK (autonomy_level IN ('assistive', 'semi_autonomous', 'high_autonomy')),
    external_impact_level TEXT NOT NULL DEFAULT 'internal'
        CHECK (external_impact_level IN ('internal', 'customer_facing', 'regulated', 'critical')),
    data_domains JSONB NOT NULL DEFAULT '[]',
    risk_classification TEXT NOT NULL DEFAULT 'unclassified'
        CHECK (risk_classification IN ('unclassified', 'low', 'medium', 'high', 'prohibited')),
    approval_state TEXT NOT NULL DEFAULT 'draft'
        CHECK (approval_state IN ('draft', 'approved', 'restricted', 'retired')),
    linked_agent_ids JSONB NOT NULL DEFAULT '[]',
    linked_backend_ids JSONB NOT NULL DEFAULT '[]',
    linked_tool_refs JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE INDEX idx_ai_system_inventory_workspace
    ON ai_system_inventory(workspace_id, updated_at DESC);

CREATE TABLE ai_third_party_risk (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID REFERENCES gateway_backend(id) ON DELETE SET NULL,
    provider_name TEXT NOT NULL,
    owner_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    approved_use_cases JSONB NOT NULL DEFAULT '[]',
    data_categories JSONB NOT NULL DEFAULT '[]',
    regions JSONB NOT NULL DEFAULT '[]',
    hosting_notes TEXT NOT NULL DEFAULT '',
    contract_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (contract_status IN ('unknown', 'not_started', 'in_review', 'approved', 'rejected', 'expired')),
    security_review_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (security_review_status IN ('unknown', 'not_started', 'in_review', 'approved', 'rejected', 'expired')),
    evidence_links JSONB NOT NULL DEFAULT '[]',
    limitations TEXT NOT NULL DEFAULT '',
    prohibited_uses TEXT NOT NULL DEFAULT '',
    model_list JSONB NOT NULL DEFAULT '[]',
    capability_class TEXT NOT NULL DEFAULT '',
    risk_score INT NOT NULL DEFAULT 0 CHECK (risk_score >= 0 AND risk_score <= 100),
    review_cadence_days INT NOT NULL DEFAULT 365,
    last_assessment_at TIMESTAMPTZ,
    next_review_at TIMESTAMPTZ,
    active_exception_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, provider_name)
);

CREATE INDEX idx_ai_third_party_risk_workspace_review
    ON ai_third_party_risk(workspace_id, next_review_at NULLS FIRST);

CREATE TABLE ai_control_mapping (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    framework TEXT NOT NULL,
    control_id TEXT NOT NULL,
    control_title TEXT NOT NULL,
    mapped_policy_ids JSONB NOT NULL DEFAULT '[]',
    mapped_evidence_queries JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'not_started'
        CHECK (status IN ('not_started', 'in_progress', 'covered', 'gap', 'accepted_risk')),
    owner_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, framework, control_id)
);

CREATE TABLE ai_evidence (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    evidence_type TEXT NOT NULL,
    framework_refs JSONB NOT NULL DEFAULT '[]',
    linked_request_id UUID REFERENCES gateway_request(id) ON DELETE SET NULL,
    linked_session_id UUID REFERENCES gateway_session(id) ON DELETE SET NULL,
    linked_span_row_id UUID REFERENCES gateway_span(id) ON DELETE SET NULL,
    linked_policy_id UUID REFERENCES gateway_policy(id) ON DELETE SET NULL,
    linked_backend_id UUID REFERENCES gateway_backend(id) ON DELETE SET NULL,
    linked_provider_risk_id UUID REFERENCES ai_third_party_risk(id) ON DELETE SET NULL,
    summary TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    attachment_ref TEXT NOT NULL DEFAULT '',
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retain_until TIMESTAMPTZ
);

CREATE INDEX idx_ai_evidence_workspace_generated
    ON ai_evidence(workspace_id, generated_at DESC);

CREATE TABLE ai_policy_exception (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES gateway_policy(id) ON DELETE SET NULL,
    requester_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    approver_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    reason TEXT NOT NULL,
    scope JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested', 'approved', 'denied', 'expired', 'revoked')),
    expires_at TIMESTAMPTZ,
    evidence_references JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_policy_exception_workspace_status
    ON ai_policy_exception(workspace_id, status, expires_at);

CREATE TABLE ai_incident (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    severity TEXT NOT NULL
        CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    category TEXT NOT NULL,
    linked_request_id UUID REFERENCES gateway_request(id) ON DELETE SET NULL,
    linked_session_id UUID REFERENCES gateway_session(id) ON DELETE SET NULL,
    linked_span_row_id UUID REFERENCES gateway_span(id) ON DELETE SET NULL,
    linked_policy_id UUID REFERENCES gateway_policy(id) ON DELETE SET NULL,
    linked_provider_risk_id UUID REFERENCES ai_third_party_risk(id) ON DELETE SET NULL,
    summary TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'investigating', 'remediated', 'closed')),
    remediation_notes TEXT NOT NULL DEFAULT '',
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ
);

CREATE INDEX idx_ai_incident_workspace_status
    ON ai_incident(workspace_id, status, opened_at DESC);

CREATE TABLE ai_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    actor_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL DEFAULT '',
    before_state JSONB,
    after_state JSONB,
    request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_audit_log_workspace_created
    ON ai_audit_log(workspace_id, created_at DESC);
