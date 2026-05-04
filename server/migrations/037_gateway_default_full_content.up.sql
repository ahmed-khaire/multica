ALTER TABLE gateway_workspace_settings
    ALTER COLUMN capture_policy SET DEFAULT 'full_content';

ALTER TABLE gateway_request
    ALTER COLUMN capture_policy SET DEFAULT 'full_content';
