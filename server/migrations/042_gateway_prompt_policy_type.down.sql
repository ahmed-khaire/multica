ALTER TABLE gateway_policy
    DROP CONSTRAINT gateway_policy_policy_type_check;

ALTER TABLE gateway_policy
    ADD CONSTRAINT gateway_policy_policy_type_check
    CHECK (policy_type IN ('provider', 'model', 'tool', 'data', 'budget', 'approval', 'routing', 'capture'));
