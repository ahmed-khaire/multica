DROP INDEX IF EXISTS idx_ai_incident_workspace_active;
DROP INDEX IF EXISTS idx_ai_control_mapping_workspace_active;
DROP INDEX IF EXISTS idx_ai_third_party_risk_workspace_active;
DROP INDEX IF EXISTS idx_gateway_policy_workspace_active;

ALTER TABLE ai_incident
    DROP COLUMN archived_at;

ALTER TABLE ai_control_mapping
    DROP COLUMN archived_at;

ALTER TABLE ai_third_party_risk
    DROP COLUMN archived_at;

ALTER TABLE gateway_policy
    DROP COLUMN archived_at;
