# Gateway Governance Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the shared PostgreSQL schema, query layer, encryption helpers, gateway key primitives, and deterministic policy evaluator that the Multica Gateway, Observer telemetry, SDK/OTLP enterprise observability layer, and AI governance platform will use.

**Architecture:** This is the first plan in the Gateway plan set. It creates the durable model and small domain packages under `server/internal/gateway`, without adding provider-compatible routing, CLI commands, dashboard UI, SDK packages, OTLP ingest, or Claude OAuth sidecar code. Subsequent plans can build streaming gateway routes, management APIs, SDK/OTLP telemetry ingestion, application inventory resolution, dashboard views, governance APIs, and CLI commands on top of these stable primitives.

**Tech Stack:** Go 1.26, PostgreSQL, sqlc, pgx/v5, AES-256-GCM, `crypto/rand`, existing `server/internal/auth.HashToken`, existing `server/pkg/redact`.

---

## Plan Set

The approved spec covers multiple independent product surfaces. Keep the work split so each slice can be built and reviewed independently:

1. `2026-05-03-gateway-governance-foundation.md`: schema, sqlc queries, secrets, gateway key primitives, deterministic policy evaluator.
2. Gateway management API and `multica gateway` CLI commands.
3. OpenAI-compatible and Anthropic-compatible hosted gateway routing with streaming.
4. Gateway telemetry recorder, explicit trace ingestion API, OTLP-compatible ingest, application/environment/deployment/artifact extensions, app inventory resolver, and lightweight TypeScript/Python SDKs for context, headers, spans, logs, artifacts, and pre-action policy evaluation.
5. Gateway dashboard APIs and shared frontend views for Overview, Applications, Sessions, Drilldown, LLM Calls, Agents, Workflows/Tools, Logs/Artifacts, and visualizations.
6. Governance APIs and shared frontend views for application and AI-system inventory, policy decisions, third-party risk, exceptions, incidents, evidence, app/agent risk scoring, alerts, recommendations, and insights.
7. `claude-oauth` adapter boundary with sidecar-backed first implementation.

This plan produces working, testable backend foundation code. It intentionally stops before HTTP routing and UI wiring so those pieces can depend on generated database types and domain packages that already compile.

Milestone 1 now treats the SDK-based enterprise app and agent observability layer as part of the first release control plane. The existing foundation migration covers the base gateway/session/span/log/agent/tool/governance shape. If the implementation branch already contains migration `036_gateway_foundation`, the Phase 4 telemetry-ingest plan should add application, environment, deployment, artifact, guardrail, and ingest-key tables in a new migration rather than rewriting the applied foundation migration.

## File Structure

- Create `server/migrations/036_gateway_foundation.up.sql`: Gateway configuration, telemetry, governance, evidence, and audit tables.
- Create `server/migrations/036_gateway_foundation.down.sql`: reverse migration in dependency order.
- Create `server/pkg/db/queries/gateway_backend.sql`: sqlc queries for workspace settings, backend CRUD, and default backend selection.
- Create `server/pkg/db/queries/gateway_key.sql`: sqlc queries for active gateway key retrieval, creation, revocation, and last-used updates.
- Create `server/pkg/db/queries/gateway_policy.sql`: sqlc queries for policy CRUD and policy decision writes.
- Create `server/pkg/db/queries/gateway_telemetry.sql`: sqlc write/read queries for sessions, requests, model calls, spans, logs, events, observations, and rollups.
- Create `server/pkg/db/queries/governance.sql`: sqlc queries for inventory, third-party risk, control mapping, evidence, exceptions, incidents, and audit log records.
- Regenerate `server/pkg/db/generated/*.go` with `make sqlc`.
- Create `server/internal/gateway/secrets/secrets.go`: AES-GCM encryption/decryption using `MULTICA_GATEWAY_SECRET_KEY`.
- Create `server/internal/gateway/secrets/secrets_test.go`: encryption round trip and invalid configuration tests.
- Create `server/internal/gateway/keyring/keyring.go`: gateway key generation, hashing, prefix extraction, and encrypted storage helpers.
- Create `server/internal/gateway/keyring/keyring_test.go`: key format, hash, encryption round trip, and key uniqueness tests.
- Create `server/internal/gateway/policy/policy.go`: deterministic rule evaluator and decision types.
- Create `server/internal/gateway/policy/policy_test.go`: allow, warn, approval, redact, route, and block decision tests.

## Task 1: Add Gateway And Governance Foundation Migration

**Files:**
- Create: `server/migrations/036_gateway_foundation.up.sql`
- Create: `server/migrations/036_gateway_foundation.down.sql`

- [ ] **Step 1: Create the up migration**

Write `server/migrations/036_gateway_foundation.up.sql`:

```sql
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
```

- [ ] **Step 2: Create the down migration**

Write `server/migrations/036_gateway_foundation.down.sql`:

```sql
DROP TABLE IF EXISTS ai_audit_log;
DROP TABLE IF EXISTS ai_incident;
DROP TABLE IF EXISTS ai_policy_exception;
DROP TABLE IF EXISTS ai_evidence;
DROP TABLE IF EXISTS ai_control_mapping;
DROP TABLE IF EXISTS ai_third_party_risk;
DROP TABLE IF EXISTS ai_system_inventory;
DROP TABLE IF EXISTS gateway_policy_decision;
DROP TABLE IF EXISTS gateway_model_pricing;
DROP TABLE IF EXISTS gateway_metric_rollup;
DROP TABLE IF EXISTS gateway_tool_observation;
DROP TABLE IF EXISTS gateway_agent_observation;
DROP TABLE IF EXISTS gateway_log;
DROP TABLE IF EXISTS gateway_span_link;
DROP TABLE IF EXISTS gateway_event;
DROP TABLE IF EXISTS gateway_span;
DROP TABLE IF EXISTS gateway_model_call;
DROP TABLE IF EXISTS gateway_request;
DROP TABLE IF EXISTS gateway_session;
DROP TABLE IF EXISTS gateway_policy;
DROP TABLE IF EXISTS gateway_user_key;
ALTER TABLE IF EXISTS gateway_workspace_settings
    DROP CONSTRAINT IF EXISTS gateway_workspace_settings_default_backend_fk;
DROP TABLE IF EXISTS gateway_backend;
DROP TABLE IF EXISTS gateway_workspace_settings;
```

- [ ] **Step 3: Verify migration syntax with sqlc**

Run:

```bash
make sqlc
```

Expected:

```text
cd server && sqlc generate
```

The command exits with status 0.

- [ ] **Step 4: Commit the migration**

Run:

```bash
git add server/migrations/036_gateway_foundation.up.sql server/migrations/036_gateway_foundation.down.sql server/pkg/db/generated
git commit -m "feat: add gateway governance foundation schema"
```

Expected: commit succeeds and `git status --short` is empty.

## Task 2: Add sqlc Query Files

**Files:**
- Create: `server/pkg/db/queries/gateway_backend.sql`
- Create: `server/pkg/db/queries/gateway_key.sql`
- Create: `server/pkg/db/queries/gateway_policy.sql`
- Create: `server/pkg/db/queries/gateway_telemetry.sql`
- Create: `server/pkg/db/queries/governance.sql`
- Modify: `server/pkg/db/generated/*.go`

- [ ] **Step 1: Add backend and workspace settings queries**

Write `server/pkg/db/queries/gateway_backend.sql`:

```sql
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
```

- [ ] **Step 2: Add gateway key queries**

Write `server/pkg/db/queries/gateway_key.sql`:

```sql
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
```

- [ ] **Step 3: Add policy and decision queries**

Write `server/pkg/db/queries/gateway_policy.sql`:

```sql
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
ORDER BY enabled DESC, name;

-- name: ListEnabledGatewayPolicies :many
SELECT * FROM gateway_policy
WHERE workspace_id = $1 AND enabled = TRUE
ORDER BY policy_type, name;

-- name: GetGatewayPolicy :one
SELECT * FROM gateway_policy
WHERE workspace_id = $1 AND id = $2;

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
```

- [ ] **Step 4: Add telemetry queries**

Write `server/pkg/db/queries/gateway_telemetry.sql`:

```sql
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
```

- [ ] **Step 5: Add governance queries**

Write `server/pkg/db/queries/governance.sql`:

```sql
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

-- name: CreateAIIncident :one
INSERT INTO ai_incident (
    workspace_id, severity, category, linked_request_id, linked_session_id,
    linked_span_row_id, linked_policy_id, linked_provider_risk_id,
    summary, status, remediation_notes
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: CreateAIAuditLog :one
INSERT INTO ai_audit_log (
    workspace_id, actor_user_id, action, target_type, target_id,
    before_state, after_state, request_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
```

- [ ] **Step 6: Regenerate database code**

Run:

```bash
make sqlc
```

Expected:

```text
cd server && sqlc generate
```

The command exits with status 0 and generated Go files under `server/pkg/db/generated` include gateway and governance query methods.

- [ ] **Step 7: Commit sqlc queries**

Run:

```bash
git add server/pkg/db/queries/gateway_backend.sql server/pkg/db/queries/gateway_key.sql server/pkg/db/queries/gateway_policy.sql server/pkg/db/queries/gateway_telemetry.sql server/pkg/db/queries/governance.sql server/pkg/db/generated
git commit -m "feat: add gateway governance database queries"
```

Expected: commit succeeds and `git status --short` is empty.

## Task 3: Add Gateway Secrets Package

**Files:**
- Create: `server/internal/gateway/secrets/secrets_test.go`
- Create: `server/internal/gateway/secrets/secrets.go`

- [ ] **Step 1: Write failing tests**

Write `server/internal/gateway/secrets/secrets_test.go`:

```go
package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	box, err := NewBox(testKey(t))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}

	ciphertext, err := box.EncryptString("sk-proj-secret")
	if err != nil {
		t.Fatalf("EncryptString returned error: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("sk-proj-secret")) {
		t.Fatalf("ciphertext contains plaintext: %q", ciphertext)
	}

	plaintext, err := box.DecryptString(ciphertext)
	if err != nil {
		t.Fatalf("DecryptString returned error: %v", err)
	}
	if plaintext != "sk-proj-secret" {
		t.Fatalf("plaintext mismatch: got %q", plaintext)
	}
}

func TestNewBoxRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	_, err := NewBox("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestNewBoxRejectsWrongKeyLength(t *testing.T) {
	t.Parallel()

	_, err := NewBox(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	box, err := NewBox(testKey(t))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}

	ciphertext, err := box.EncryptString("secret")
	if err != nil {
		t.Fatalf("EncryptString returned error: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0x01

	_, err = box.DecryptString(ciphertext)
	if err == nil {
		t.Fatal("expected tampered ciphertext to fail")
	}
}
```

- [ ] **Step 2: Run the tests and verify failure**

Run:

```bash
cd server && go test ./internal/gateway/secrets
```

Expected:

```text
FAIL    github.com/multica-ai/multica/server/internal/gateway/secrets
```

The failure includes undefined symbols such as `NewBox`.

- [ ] **Step 3: Implement the secrets package**

Write `server/internal/gateway/secrets/secrets.go`:

```go
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	EnvKeyName = "MULTICA_GATEWAY_SECRET_KEY"
	keyBytes   = 32
	nonceBytes = 12
)

type Box struct {
	aead cipher.AEAD
}

func FromEnv() (*Box, error) {
	return NewBox(os.Getenv(EnvKeyName))
}

func NewBox(encodedKey string) (*Box, error) {
	if encodedKey == "" {
		return nil, fmt.Errorf("%s is required", EnvKeyName)
	}

	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", EnvKeyName, err)
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("%s must decode to %d bytes", EnvKeyName, keyBytes)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	return &Box{aead: aead}, nil
}

func (b *Box) EncryptString(plaintext string) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secrets box is not initialized")
	}

	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	sealed := b.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

func (b *Box) DecryptString(ciphertext []byte) (string, error) {
	if b == nil || b.aead == nil {
		return "", errors.New("secrets box is not initialized")
	}
	if len(ciphertext) <= nonceBytes {
		return "", errors.New("ciphertext is too short")
	}

	nonce := ciphertext[:nonceBytes]
	sealed := ciphertext[nonceBytes:]
	plaintext, err := b.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}

	return string(plaintext), nil
}
```

- [ ] **Step 4: Run the package tests**

Run:

```bash
cd server && go test ./internal/gateway/secrets
```

Expected:

```text
ok      github.com/multica-ai/multica/server/internal/gateway/secrets
```

- [ ] **Step 5: Commit secrets package**

Run:

```bash
git add server/internal/gateway/secrets
git commit -m "feat: add gateway secrets encryption"
```

Expected: commit succeeds and `git status --short` is empty.

## Task 4: Add Gateway Keyring Package

**Files:**
- Create: `server/internal/gateway/keyring/keyring_test.go`
- Create: `server/internal/gateway/keyring/keyring.go`

- [ ] **Step 1: Write failing tests**

Write `server/internal/gateway/keyring/keyring_test.go`:

```go
package keyring

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	key := bytes.Repeat([]byte{3}, 32)
	box, err := secrets.NewBox(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewBox returned error: %v", err)
	}
	return box
}

func TestGenerateGatewayKeyFormat(t *testing.T) {
	t.Parallel()

	key, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey returned error: %v", err)
	}
	if !strings.HasPrefix(key, Prefix) {
		t.Fatalf("expected prefix %q, got %q", Prefix, key)
	}
	if len(key) != len(Prefix)+40 {
		t.Fatalf("unexpected key length: %d", len(key))
	}
}

func TestPrepareGatewayKeyStoresHashAndEncryptedValue(t *testing.T) {
	t.Parallel()

	prepared, err := PrepareNewGatewayKey(testBox(t))
	if err != nil {
		t.Fatalf("PrepareNewGatewayKey returned error: %v", err)
	}
	if prepared.Raw == "" {
		t.Fatal("expected raw key for one-time CLI output")
	}
	if prepared.Hash == "" || prepared.Hash == prepared.Raw {
		t.Fatalf("invalid hash: %q", prepared.Hash)
	}
	if prepared.DisplayPrefix != prepared.Raw[:12] {
		t.Fatalf("display prefix mismatch: got %q want %q", prepared.DisplayPrefix, prepared.Raw[:12])
	}
	if bytes.Contains(prepared.Encrypted, []byte(prepared.Raw)) {
		t.Fatalf("encrypted value contains raw key: %q", prepared.Encrypted)
	}
}

func TestDecryptStoredGatewayKey(t *testing.T) {
	t.Parallel()

	box := testBox(t)
	prepared, err := PrepareNewGatewayKey(box)
	if err != nil {
		t.Fatalf("PrepareNewGatewayKey returned error: %v", err)
	}

	got, err := DecryptStoredGatewayKey(box, prepared.Encrypted)
	if err != nil {
		t.Fatalf("DecryptStoredGatewayKey returned error: %v", err)
	}
	if got != prepared.Raw {
		t.Fatalf("raw key mismatch: got %q want %q", got, prepared.Raw)
	}
}

func TestGenerateGatewayKeyProducesUniqueValues(t *testing.T) {
	t.Parallel()

	a, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey a returned error: %v", err)
	}
	b, err := GenerateGatewayKey()
	if err != nil {
		t.Fatalf("GenerateGatewayKey b returned error: %v", err)
	}
	if a == b {
		t.Fatal("expected distinct gateway keys")
	}
}
```

- [ ] **Step 2: Run the tests and verify failure**

Run:

```bash
cd server && go test ./internal/gateway/keyring
```

Expected:

```text
FAIL    github.com/multica-ai/multica/server/internal/gateway/keyring
```

The failure includes undefined symbols such as `GenerateGatewayKey`.

- [ ] **Step 3: Implement the keyring package**

Write `server/internal/gateway/keyring/keyring.go`:

```go
package keyring

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

const (
	Prefix       = "mgw_"
	randomBytes  = 20
	prefixLength = 12
)

type PreparedKey struct {
	Raw           string
	Hash          string
	Encrypted     []byte
	DisplayPrefix string
}

func GenerateGatewayKey() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate gateway key: %w", err)
	}
	return Prefix + hex.EncodeToString(b), nil
}

func PrepareNewGatewayKey(box *secrets.Box) (PreparedKey, error) {
	raw, err := GenerateGatewayKey()
	if err != nil {
		return PreparedKey{}, err
	}

	encrypted, err := box.EncryptString(raw)
	if err != nil {
		return PreparedKey{}, fmt.Errorf("encrypt gateway key: %w", err)
	}

	return PreparedKey{
		Raw:           raw,
		Hash:          HashGatewayKey(raw),
		Encrypted:     encrypted,
		DisplayPrefix: raw[:prefixLength],
	}, nil
}

func HashGatewayKey(raw string) string {
	return auth.HashToken(raw)
}

func DecryptStoredGatewayKey(box *secrets.Box, encrypted []byte) (string, error) {
	raw, err := box.DecryptString(encrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt gateway key: %w", err)
	}
	return raw, nil
}
```

- [ ] **Step 4: Run keyring tests**

Run:

```bash
cd server && go test ./internal/gateway/keyring
```

Expected:

```text
ok      github.com/multica-ai/multica/server/internal/gateway/keyring
```

- [ ] **Step 5: Commit keyring package**

Run:

```bash
git add server/internal/gateway/keyring
git commit -m "feat: add gateway keyring primitives"
```

Expected: commit succeeds and `git status --short` is empty.

## Task 5: Add Deterministic Policy Evaluator

**Files:**
- Create: `server/internal/gateway/policy/policy_test.go`
- Create: `server/internal/gateway/policy/policy.go`

- [ ] **Step 1: Write failing policy tests**

Write `server/internal/gateway/policy/policy_test.go`:

```go
package policy

import "testing"

func TestEvaluateAllowsWhenNoRulesMatch(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "blocked-provider",
		Action:     ActionBlock,
		ReasonCode: "provider_blocked",
		Match: Match{Providers: []string{"shadow"}},
	}}, Request{Provider: "openai", Model: "gpt-4.1"})

	if got.Action != ActionAllow {
		t.Fatalf("expected allow, got %s", got.Action)
	}
	if len(got.MatchedRules) != 0 {
		t.Fatalf("expected no matches, got %#v", got.MatchedRules)
	}
}

func TestEvaluateBlocksMatchingProvider(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "block-openrouter",
		Action:     ActionBlock,
		ReasonCode: "provider_not_approved",
		Match: Match{Providers: []string{"openrouter"}},
	}}, Request{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"})

	if got.Action != ActionBlock {
		t.Fatalf("expected block, got %s", got.Action)
	}
	if got.ReasonCode != "provider_not_approved" {
		t.Fatalf("reason mismatch: %q", got.ReasonCode)
	}
}

func TestEvaluateRequiresApprovalForSensitiveDataClass(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "approval-sensitive",
		Action:     ActionRequireApproval,
		ReasonCode: "sensitive_data_requires_approval",
		Match: Match{DataClasses: []string{"customer_pii"}},
	}}, Request{DataClasses: []string{"customer_pii", "source_code"}})

	if got.Action != ActionRequireApproval {
		t.Fatalf("expected require approval, got %s", got.Action)
	}
}

func TestEvaluateChoosesHighestSeverity(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{
		{
			ID:         "warn-expensive",
			Action:     ActionWarn,
			ReasonCode: "high_cost",
			Match:      Match{Models: []string{"gpt-4.1"}},
		},
		{
			ID:         "block-tool",
			Action:     ActionBlock,
			ReasonCode: "tool_blocked",
			Match:      Match{Tools: []string{"prod-deploy"}},
		},
	}, Request{Model: "gpt-4.1", Tools: []string{"prod-deploy"}})

	if got.Action != ActionBlock {
		t.Fatalf("expected block to win, got %s", got.Action)
	}
	if len(got.MatchedRules) != 2 {
		t.Fatalf("expected two matched rules, got %d", len(got.MatchedRules))
	}
}

func TestEvaluateRoutesWhenRouteRuleMatches(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:               "route-local",
		Action:           ActionRouteToBackend,
		ReasonCode:       "source_code_routes_local",
		RouteBackendSlug: "local",
		Match:            Match{DataClasses: []string{"source_code"}},
	}}, Request{Provider: "openai", Model: "gpt-4.1", DataClasses: []string{"source_code"}})

	if got.Action != ActionRouteToBackend {
		t.Fatalf("expected route, got %s", got.Action)
	}
	if got.RouteBackendSlug != "local" {
		t.Fatalf("route mismatch: %q", got.RouteBackendSlug)
	}
}

func TestEvaluateRedactsMatchingTool(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "redact-shell",
		Action:     ActionRedact,
		ReasonCode: "tool_payload_redacted",
		Match:      Match{Tools: []string{"shell"}},
	}}, Request{Tools: []string{"shell"}})

	if got.Action != ActionRedact {
		t.Fatalf("expected redact, got %s", got.Action)
	}
}
```

- [ ] **Step 2: Run policy tests and verify failure**

Run:

```bash
cd server && go test ./internal/gateway/policy
```

Expected:

```text
FAIL    github.com/multica-ai/multica/server/internal/gateway/policy
```

The failure includes undefined symbols such as `Evaluate`.

- [ ] **Step 3: Implement policy evaluator**

Write `server/internal/gateway/policy/policy.go`:

```go
package policy

type Action string

const (
	ActionAllow           Action = "allow"
	ActionWarn            Action = "warn"
	ActionRequireApproval Action = "require_approval"
	ActionRedact          Action = "redact"
	ActionRouteToBackend  Action = "route_to_backend"
	ActionBlock           Action = "block"
)

type Request struct {
	Provider    string
	Model       string
	Tools       []string
	DataClasses []string
	UserID      string
	AgentID     string
}

type Match struct {
	Providers   []string `json:"providers"`
	Models      []string `json:"models"`
	Tools       []string `json:"tools"`
	DataClasses []string `json:"data_classes"`
	Users       []string `json:"users"`
	Agents      []string `json:"agents"`
}

type Rule struct {
	ID               string `json:"id"`
	Action           Action `json:"action"`
	ReasonCode       string `json:"reason_code"`
	Message          string `json:"message"`
	RouteBackendSlug string `json:"route_backend_slug"`
	Match            Match  `json:"match"`
}

type Decision struct {
	Action           Action
	ReasonCode       string
	Message          string
	RouteBackendSlug string
	MatchedRules     []Rule
}

func Evaluate(rules []Rule, req Request) Decision {
	decision := Decision{Action: ActionAllow}

	for _, rule := range rules {
		if !matches(rule.Match, req) {
			continue
		}

		decision.MatchedRules = append(decision.MatchedRules, rule)
		if severity(rule.Action) >= severity(decision.Action) {
			decision.Action = rule.Action
			decision.ReasonCode = rule.ReasonCode
			decision.Message = rule.Message
			decision.RouteBackendSlug = rule.RouteBackendSlug
		}
	}

	return decision
}

func matches(m Match, req Request) bool {
	return matchScalar(m.Providers, req.Provider) &&
		matchScalar(m.Models, req.Model) &&
		matchAny(m.Tools, req.Tools) &&
		matchAny(m.DataClasses, req.DataClasses) &&
		matchScalar(m.Users, req.UserID) &&
		matchScalar(m.Agents, req.AgentID)
}

func matchScalar(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == value {
			return true
		}
	}
	return false
}

func matchAny(allowed []string, values []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedValue := range allowed {
		for _, value := range values {
			if allowedValue == value {
				return true
			}
		}
	}
	return false
}

func severity(action Action) int {
	switch action {
	case ActionAllow:
		return 0
	case ActionWarn:
		return 1
	case ActionRedact:
		return 2
	case ActionRouteToBackend:
		return 3
	case ActionRequireApproval:
		return 4
	case ActionBlock:
		return 5
	default:
		return 0
	}
}
```

- [ ] **Step 4: Run policy tests**

Run:

```bash
cd server && go test ./internal/gateway/policy
```

Expected:

```text
ok      github.com/multica-ai/multica/server/internal/gateway/policy
```

- [ ] **Step 5: Commit policy evaluator**

Run:

```bash
git add server/internal/gateway/policy
git commit -m "feat: add gateway policy evaluator"
```

Expected: commit succeeds and `git status --short` is empty.

## Task 6: Run Foundation Verification

**Files:**
- Verify: `server/migrations/036_gateway_foundation.up.sql`
- Verify: `server/pkg/db/queries/*.sql`
- Verify: `server/internal/gateway/secrets`
- Verify: `server/internal/gateway/keyring`
- Verify: `server/internal/gateway/policy`

- [ ] **Step 1: Run sqlc generation**

Run:

```bash
make sqlc
```

Expected:

```text
cd server && sqlc generate
```

The command exits with status 0.

- [ ] **Step 2: Run focused Go tests**

Run:

```bash
cd server && go test ./internal/gateway/...
```

Expected:

```text
ok      github.com/multica-ai/multica/server/internal/gateway/keyring
ok      github.com/multica-ai/multica/server/internal/gateway/policy
ok      github.com/multica-ai/multica/server/internal/gateway/secrets
```

- [ ] **Step 3: Run generated database package compile test**

Run:

```bash
cd server && go test ./pkg/db/generated
```

Expected:

```text
?       github.com/multica-ai/multica/server/pkg/db/generated    [no test files]
```

- [ ] **Step 4: Run a broader server compile check**

Run:

```bash
cd server && go test ./internal/... ./pkg/...
```

Expected: all packages compile and tests pass.

- [ ] **Step 5: Commit verification fixes if needed**

If any verification step required code changes, run:

```bash
git add server/migrations server/pkg/db/queries server/pkg/db/generated server/internal/gateway
git commit -m "fix: stabilize gateway foundation"
```

Expected: commit succeeds. If no files changed, this command is skipped and `git status --short` is empty.

## Success Criteria

- `server/migrations/036_gateway_foundation.up.sql` defines the Gateway, telemetry, policy, governance, evidence, and audit tables required by the approved spec foundation.
- `server/migrations/036_gateway_foundation.down.sql` drops the same tables in dependency order.
- `make sqlc` succeeds and generated query methods exist for backend configuration, keys, policy decisions, telemetry records, and governance records.
- `server/internal/gateway/secrets` encrypts and decrypts retrievable credentials using `MULTICA_GATEWAY_SECRET_KEY`-compatible base64 AES-256-GCM keys.
- `server/internal/gateway/keyring` generates `mgw_` keys, hashes them with the existing Multica token hashing helper, and encrypts retrievable key values.
- `server/internal/gateway/policy` produces deterministic explainable decisions for `allow`, `warn`, `require_approval`, `redact`, `route_to_backend`, and `block`.
- `cd server && go test ./internal/gateway/...` passes.
- `cd server && go test ./pkg/db/generated` passes.

## Self-Review Notes

- Spec coverage: this plan covers the database model, PostgreSQL-only telemetry foundation, capture-policy column, encrypted Gateway credentials, retrievable gateway key storage, deterministic policy decisions, governance inventory, third-party risk, evidence, exceptions, incidents, and audit logs. Gateway HTTP routing, streaming, CLI, dashboard views, trace ingestion handlers, and Claude OAuth sidecar are separate plan slices listed in the Plan Set.
- Placeholder scan: the plan contains concrete paths, SQL, Go tests, Go implementation code, commands, and expected outputs for every task.
- Type consistency: policy action values match the approved spec and the database `CHECK` constraints; capture policy values match the spec and database constraints; backend type values match the spec and database constraints.
