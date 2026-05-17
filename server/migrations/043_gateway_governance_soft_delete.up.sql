ALTER TABLE gateway_policy
    ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE ai_third_party_risk
    ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE ai_control_mapping
    ADD COLUMN archived_at TIMESTAMPTZ;

ALTER TABLE ai_incident
    ADD COLUMN archived_at TIMESTAMPTZ;

CREATE INDEX idx_gateway_policy_workspace_active
    ON gateway_policy(workspace_id, updated_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX idx_ai_third_party_risk_workspace_active
    ON ai_third_party_risk(workspace_id, updated_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX idx_ai_control_mapping_workspace_active
    ON ai_control_mapping(workspace_id, updated_at DESC)
    WHERE archived_at IS NULL;

CREATE INDEX idx_ai_incident_workspace_active
    ON ai_incident(workspace_id, opened_at DESC)
    WHERE archived_at IS NULL;
