CREATE TABLE gateway_backend_credential (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID NOT NULL REFERENCES gateway_backend(id) ON DELETE CASCADE,
    label TEXT NOT NULL DEFAULT '',
    encrypted_credential BYTEA NOT NULL,
    credential_hint TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INT NOT NULL DEFAULT 100,
    last_used_at TIMESTAMPTZ,
    last_error_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    updated_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_backend_credential_active
    ON gateway_backend_credential(workspace_id, backend_id, enabled, priority, created_at);
