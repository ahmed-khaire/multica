DROP INDEX IF EXISTS idx_gateway_runtime_request_wait;
DROP INDEX IF EXISTS idx_gateway_runtime_request_claim;
DROP TABLE IF EXISTS gateway_runtime_request;

DROP INDEX IF EXISTS idx_gateway_subscription_validation_runtime;
DROP INDEX IF EXISTS idx_gateway_subscription_validation_claim;
DROP TABLE IF EXISTS gateway_subscription_runtime_validation;

DROP INDEX IF EXISTS idx_agent_runtime_workspace_id;
DROP INDEX IF EXISTS idx_gateway_backend_credential_workspace_backend_id;

ALTER TABLE gateway_backend_credential
    DROP COLUMN IF EXISTS last_validation_error,
    DROP COLUMN IF EXISTS last_validation_at,
    DROP COLUMN IF EXISTS refreshable,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS account_fingerprint,
    DROP COLUMN IF EXISTS account_hint,
    DROP COLUMN IF EXISTS validated_runtime_id,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS dispatch_scope,
    DROP COLUMN IF EXISTS payload_format,
    DROP COLUMN IF EXISTS encrypted_payload,
    DROP COLUMN IF EXISTS subscription_provider,
    DROP COLUMN IF EXISTS credential_type;

ALTER TABLE gateway_backend
    DROP COLUMN IF EXISTS last_validation_error,
    DROP COLUMN IF EXISTS last_validation_at,
    DROP COLUMN IF EXISTS validated_runtime_id,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS dispatch_scope,
    DROP COLUMN IF EXISTS subscription_provider,
    DROP COLUMN IF EXISTS transport;

ALTER TABLE gateway_backend
    DROP CONSTRAINT IF EXISTS gateway_backend_backend_type_check,
    ADD CONSTRAINT gateway_backend_backend_type_check
        CHECK (backend_type IN ('openai_compatible', 'anthropic', 'claude_oauth'));
