CREATE TABLE gateway_ingest_key (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    encrypted_key_value BYTEA NOT NULL,
    key_prefix TEXT NOT NULL,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (btrim(display_name) <> '')
);

CREATE INDEX idx_gateway_ingest_key_workspace_created
    ON gateway_ingest_key(workspace_id, created_at DESC);

CREATE INDEX idx_gateway_ingest_key_workspace_active
    ON gateway_ingest_key(workspace_id, revoked_at)
    WHERE revoked_at IS NULL;
