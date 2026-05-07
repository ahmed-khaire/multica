CREATE TABLE ai_evidence_export (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    actor_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    export_type TEXT NOT NULL
        CHECK (export_type IN ('evidence_bundle')),
    subject_type TEXT NOT NULL
        CHECK (subject_type IN ('session', 'incident', 'policy_decision')),
    subject_id TEXT NOT NULL,
    digest_sha256 TEXT NOT NULL,
    sections JSONB NOT NULL DEFAULT '[]'::jsonb,
    bundle_snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_evidence_export_workspace_created
    ON ai_evidence_export(workspace_id, created_at DESC);

CREATE INDEX idx_ai_evidence_export_workspace_subject
    ON ai_evidence_export(workspace_id, subject_type, subject_id, created_at DESC);
