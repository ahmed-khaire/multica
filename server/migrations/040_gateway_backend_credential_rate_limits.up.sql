ALTER TABLE gateway_backend_credential
    ADD COLUMN rate_limited_until TIMESTAMPTZ,
    ADD COLUMN rate_limit_remaining INT,
    ADD COLUMN rate_limit_reset_at TIMESTAMPTZ;

CREATE INDEX idx_gateway_backend_credential_headroom
    ON gateway_backend_credential(workspace_id, backend_id, enabled, rate_limited_until, priority, created_at);
