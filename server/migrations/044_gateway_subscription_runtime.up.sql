ALTER TABLE gateway_backend
    ADD COLUMN transport TEXT NOT NULL DEFAULT 'direct_http'
        CHECK (transport IN ('direct_http', 'daemon_dispatch')),
    ADD COLUMN subscription_provider TEXT NOT NULL DEFAULT ''
        CHECK (subscription_provider IN ('', 'claude_code', 'codex')),
    ADD COLUMN dispatch_scope TEXT NOT NULL DEFAULT 'workspace_authenticated_daemons'
        CHECK (dispatch_scope IN ('workspace_authenticated_daemons', 'owner_daemons_only', 'selected_daemons')),
    ADD COLUMN validation_status TEXT NOT NULL DEFAULT ''
        CHECK (validation_status IN ('', 'pending_runtime_validation', 'validating_on_runtime', 'active', 'degraded_no_runtime', 'invalid_credentials', 'disabled')),
    ADD COLUMN validated_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    ADD COLUMN last_validation_at TIMESTAMPTZ,
    ADD COLUMN last_validation_error TEXT NOT NULL DEFAULT '';

ALTER TABLE gateway_backend_credential
    ADD COLUMN credential_type TEXT NOT NULL DEFAULT 'api_key'
        CHECK (credential_type IN ('api_key', 'subscription_bundle')),
    ADD COLUMN subscription_provider TEXT NOT NULL DEFAULT ''
        CHECK (subscription_provider IN ('', 'claude_code', 'codex')),
    ADD COLUMN encrypted_payload BYTEA,
    ADD COLUMN payload_format TEXT NOT NULL DEFAULT '',
    ADD COLUMN dispatch_scope TEXT NOT NULL DEFAULT 'workspace_authenticated_daemons'
        CHECK (dispatch_scope IN ('workspace_authenticated_daemons', 'owner_daemons_only', 'selected_daemons')),
    ADD COLUMN validation_status TEXT NOT NULL DEFAULT ''
        CHECK (validation_status IN ('', 'pending_runtime_validation', 'validating_on_runtime', 'active', 'degraded_no_runtime', 'invalid_credentials', 'disabled')),
    ADD COLUMN validated_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    ADD COLUMN account_hint TEXT NOT NULL DEFAULT '',
    ADD COLUMN account_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN refreshable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN last_validation_at TIMESTAMPTZ,
    ADD COLUMN last_validation_error TEXT NOT NULL DEFAULT '';

CREATE TABLE gateway_subscription_runtime_validation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID NOT NULL REFERENCES gateway_backend(id) ON DELETE CASCADE,
    credential_id UUID NOT NULL REFERENCES gateway_backend_credential(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    provider TEXT NOT NULL CHECK (provider IN ('claude_code', 'codex')),
    account_hint TEXT NOT NULL DEFAULT '',
    account_fingerprint TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_subscription_validation_claim
    ON gateway_subscription_runtime_validation(workspace_id, provider, status, created_at);

CREATE UNIQUE INDEX idx_gateway_subscription_validation_runtime
    ON gateway_subscription_runtime_validation(credential_id, runtime_id);

CREATE TABLE gateway_runtime_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID NOT NULL REFERENCES gateway_backend(id) ON DELETE CASCADE,
    credential_id UUID REFERENCES gateway_backend_credential(id) ON DELETE SET NULL,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    provider TEXT NOT NULL CHECK (provider IN ('claude_code', 'codex')),
    surface TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'dispatched', 'running', 'completed', 'failed', 'timeout')),
    request_body JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_body JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_type TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    stream BOOLEAN NOT NULL DEFAULT false,
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_runtime_request_claim
    ON gateway_runtime_request(workspace_id, provider, status, created_at);

CREATE INDEX idx_gateway_runtime_request_wait
    ON gateway_runtime_request(workspace_id, id, status);
