DROP INDEX IF EXISTS idx_gateway_backend_credential_headroom;

ALTER TABLE gateway_backend_credential
    DROP COLUMN IF EXISTS rate_limit_reset_at,
    DROP COLUMN IF EXISTS rate_limit_remaining,
    DROP COLUMN IF EXISTS rate_limited_until;
