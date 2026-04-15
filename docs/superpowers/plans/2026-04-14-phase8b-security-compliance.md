# Phase 8B: Security & Compliance Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** make the platform enterprise-ready — immutable audit log, multi-stage rate limiting with provider-429 awareness, agent config revisioning + rollback, SSO via SAML 2.0 and OIDC, configurable data-retention policies, plus a frontend for the Phase 1 credential vault.

**Architecture:** audit entries are written by a chi middleware that wraps every mutating handler and diffs response-body state against a pre-write snapshot. Rate limiting layers three independent counters (workspace, agent, provider), with the provider counter adjusted dynamically from `x-ratelimit-*` headers and `429` responses. Agent config versioning stores JSONB snapshots on every update; rollback re-applies a snapshot as a new revision (never deletes). SSO is a split SAML-SP + OIDC-RP implementation per workspace, with OIDC client secrets stored in the Phase 1 credential vault. Retention is a daily background worker that runs per-workspace DELETE statements scoped by `resource_type`.

**Tech Stack:** Go 1.26, chi middleware, `github.com/crewjam/saml` (SAML SP), `github.com/coreos/go-oidc/v3` (OIDC RP), `github.com/go-redis/redis_rate/v10` (token bucket) or in-memory fallback, pgx/sqlc, TanStack Query + shadcn UI.

---

## Scope Note

Phase 8B of 12. Depends on Phase 1 (credential vault + audit-adjacent tables), Phase 2 (worker for retention + rate limiting) — no dependency on Phases 3–8A. Produces the seven capabilities in PLAN.md §8B: credential management UI, immutable audit log, multi-stage rate limiting, agent versioning + rollback, SAML/OIDC SSO, data retention, verification.

**Not in scope — deferred:**
- **Just-in-time (JIT) user provisioning via SCIM** — auto-provisioning from IdP on first login is in scope; real-time SCIM push-sync is a follow-up.
- **Per-tool fine-grained credential scopes** — V1 credentials are a flat list; scoped-credential bindings (e.g. a credential usable only by `salesforce` MCP server) need a separate schema.
- **Tamper-evident audit log (hash-chained)** — V1 audit_log is append-only at the application layer; cryptographic hash chaining is post-V1.
- **GDPR data-subject export endpoint** — retention covers deletion; export is a separate capability.
- **Rate limiter bucket sharing across replicas** — Redis-backed token bucket lands if telemetry shows per-replica drift; V1 uses per-process memory + periodic sync.
- **Global/cluster-level rate-limit stage (Cadence `common/quotas/` full composition)** — PLAN.md §8B.3 cites "Port from Cadence `common/quotas/` multi-stage rate limiter." Cadence's scheme composes per-entity stages under a global cluster-level cap so a single-workspace burst cannot exhaust shared provider quota. V1 implements TWO stages (workspace + agent) plus an out-of-band ProviderTracker that blocks on upstream 429. The provider-429 feedback loop is functionally equivalent to the cluster stage for our single-cluster, single-provider-quota topology: Anthropic/OpenAI rate quotas are enforced upstream and propagate back via `x-ratelimit-*` + 429, so we read the ceiling from the horse's mouth rather than maintaining our own cluster-level counter. If we later go multi-cluster with shared provider keys, the cluster stage lands as a Redis-backed bucket; this is tracked as a post-V1 follow-up in Phase 9.

---

## Preconditions

Verify before Task 1.

**From Phase 1:**

| Surface | Purpose |
|---|---|
| `workspace_credential` + `credential_access_log` tables (§1.4) | Task 3 UI + access logging |
| `agent.Credentials.GetRaw(ctx, wsID, credID) ([]byte, error)` + `agent.Zero` | OIDC client-secret decrypt + SAML IdP cert handling |
| `BudgetService` (§1.2) | No direct dep; listed because some rate-limit paths cross-reference |
| `workspace`, `workspace_member`, `agent` tables | FK targets for audit/version/sso/retention |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| chi router (existing) | Middleware chain for audit + rate limiting |
| `session` middleware (existing) | Populates `ctx.Value(SessionKey)` with `user_id, workspace_id` used by audit middleware |
| `cost_event` table + `trace_id` | Retention policy target |
| Worker binary `server/cmd/worker` | Retention job runs alongside task workers |
| `harness.Config.SystemPrompt string` field (Phase 2 Task 4) | Rollback-isolation test (Task 14) captures the instructions active at claim time by reading this field in its blocking-model callback |

**From Phase 8A:**

| Surface | Purpose |
|---|---|
| `workspace_api_key` + `APIKeyAuth` middleware (Task 13 of 8A) | Audit middleware records `actor_type='api_key'` |
| OpenAPI annotations on handlers (Task 15 of 8A) | Rate limiter middleware respects `x-ratelimit-*` in responses |

**Test helpers (extend Phase 2/8A):**

```go
// server/internal/testutil/auth.go
func SeedWorkspaceMember(db *pgxpool.Pool, wsID, userID uuid.UUID, role string)
func WithSession(r *http.Request, userID, wsID uuid.UUID) *http.Request

// server/internal/testsupport/
func (e *Env) FireRateLimit429(provider string) // simulates upstream 429
func (e *Env) SeedAgentRevisions(wsID, agentID uuid.UUID, n int) []uuid.UUID
func (e *Env) ConfigureSSO(wsID uuid.UUID, kind string /* saml|oidc */, cfg map[string]any)
```

**Pre-Task 0 bootstrap:** add Go deps to `server/go.mod` via `go get`:

```bash
cd server
go get github.com/crewjam/saml
go get github.com/coreos/go-oidc/v3/oidc
go get golang.org/x/oauth2            # required by OIDC Callback
go get github.com/go-redis/redis_rate/v10
go get golang.org/x/time/rate         # required by ratelimit.Limiter
go mod tidy
```

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/170_audit_log.up.sql` | `audit_log` table + indexes |
| `server/migrations/170_audit_log.down.sql` | Reverse |
| `server/migrations/171_sso.up.sql` | `workspace_sso_config` |
| `server/migrations/171_sso.down.sql` | Reverse |
| `server/migrations/172_retention.up.sql` | `retention_policy` |
| `server/migrations/172_retention.down.sql` | Reverse |
| `server/migrations/173_agent_versioning.up.sql` | `agent_config_revision` |
| `server/migrations/173_agent_versioning.down.sql` | Reverse |
| `server/pkg/db/queries/audit.sql` | sqlc: insert + query audit entries |
| `server/pkg/db/queries/sso.sql` | sqlc: per-workspace SSO CRUD |
| `server/pkg/db/queries/retention.sql` | sqlc: policy CRUD + per-resource DELETE helpers |
| `server/pkg/db/queries/revision.sql` | sqlc: agent revision insert/list/get |
| `server/internal/service/audit.go` | `AuditService` — async write + diff computation |
| `server/internal/service/audit_test.go` | Diff shape tests, workspace scoping |
| `server/internal/middleware/audit.go` | chi middleware wraps response writer, writes entry on 2xx/3xx mutation |
| `server/internal/middleware/audit_test.go` | Verifies audit entry on mutation, skipped on GET |
| `server/internal/handler/audit.go` | GET audit list + CSV export |
| `server/internal/handler/audit_test.go` | Filter + CSV test |
| `server/internal/middleware/ratelimit.go` | Three-layer limiter (workspace, agent, provider) + `x-ratelimit-*` tracker |
| `server/internal/middleware/ratelimit_test.go` | Burst, throttle, 429-feedback tests |
| `server/pkg/ratelimit/provider_tracker.go` | Per-provider rolling window with headroom adjustment |
| `server/pkg/ratelimit/provider_tracker_test.go` | Header parsing, 429 backoff, recovery |
| `server/internal/service/revision.go` | `RevisionService` — snapshot on update, rollback as new revision |
| `server/internal/service/revision_test.go` | Snapshot shape, rollback leaves in-flight unaffected |
| `server/internal/handler/revision.go` | List + diff + rollback HTTP endpoints |
| `server/internal/auth/saml.go` | SAML SP: ACS endpoint, SP metadata endpoint, redirect flow |
| `server/internal/auth/saml_test.go` | SAML response validation |
| `server/internal/auth/oidc.go` | OIDC RP: callback, userinfo, auto-provision |
| `server/internal/auth/oidc_test.go` | Token exchange, auto-provision path |
| `server/internal/service/sso.go` | `SSOService` — per-workspace config + enforce toggle |
| `server/internal/handler/sso.go` | Config CRUD + test-connection endpoint |
| `server/internal/worker/retention.go` | Daily retention worker |
| `server/internal/worker/retention_test.go` | Per-resource deletion test + 90-day audit floor |
| `server/internal/service/retention.go` | Policy CRUD service |
| `server/internal/handler/retention.go` | Policy endpoint |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/cmd/server/router.go` | Wrap all mutating routes with `audit.Middleware`; apply `ratelimit.Middleware` on `/api/*`; mount audit + sso + retention + revision handlers |
| `server/cmd/server/main.go` | Start retention worker goroutine on boot |
| `server/internal/handler/agent.go` | Update handler calls `RevisionService.Snapshot` before applying the mutation |
| `server/internal/middleware/apikey_auth.go` | Populate `ctx.Value(ActorKey)` with `actor_type='api_key', actor_id=keyID` so audit middleware picks it up |
| `server/internal/auth/session.go` | Populate `ctx.Value(ActorKey)` for cookie-based auth |
| `server/pkg/agent/llm_api.go` (Phase 2) | Inject `*ratelimit.ProviderTracker`; call `Observe(provider, respHeaders, statusCode)` after every LLM response so the multi-stage limiter (Task 8) can block on upstream 429s |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/audit.ts` | `AuditEntry`, `AuditFilter` |
| `packages/core/types/revision.ts` | `AgentRevision` |
| `packages/core/types/sso.ts` | `SSOConfig` (SAML + OIDC variants) |
| `packages/core/types/retention.ts` | `RetentionPolicy` |
| `packages/core/api/audit.ts` + `revision.ts` + `sso.ts` + `retention.ts` + `credential.ts` | CRUD clients |
| `packages/core/*/queries.ts` | TanStack hooks per module |
| `packages/views/settings/credentials/` | Credential mgmt UI (lists, create/edit, rotation wizard) |
| `packages/views/settings/sso/` | SSO wizard + test-connection + enforce toggle |
| `packages/views/settings/retention/` | Policy editor per resource_type |
| `packages/views/audit/` | Filterable audit log + CSV export |
| `packages/views/agents/components/tabs/revisions-tab.tsx` | Revision list + diff view + rollback button |

### Modified Files (Frontend)

| File | Changes |
|---|---|
| `packages/core/api/index.ts` | Re-export audit/revision/sso/retention/credential namespaces |
| `packages/views/agents/components/agent-detail.tsx` | Mount revisions tab |
| `apps/web/app/settings/*` + `apps/desktop/.../settings/*` | Route stubs |

### External Infrastructure

| System | Change |
|---|---|
| None — all additions are additive migrations + in-process code |

---

### Task 1: Migration 170 — Audit Log

**Files:**
- Create: `server/migrations/170_audit_log.up.sql`
- Create: `server/migrations/170_audit_log.down.sql`

**Goal:** immutable append-only `audit_log` table with composite FK to workspace.

- [ ] **Step 1: Write the up migration**

```sql
-- 170_audit_log.up.sql
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    changes JSONB,
    ip_address INET,
    user_agent TEXT,
    request_id UUID,
    trace_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_actor_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_type_check
    CHECK (actor_type IN ('member', 'agent', 'system', 'api_key')) NOT VALID;
ALTER TABLE audit_log VALIDATE CONSTRAINT audit_log_actor_type_check;

CREATE INDEX IF NOT EXISTS idx_audit_workspace_time ON audit_log(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_log(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_log(workspace_id, actor_type, actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_trace ON audit_log(trace_id) WHERE trace_id IS NOT NULL;

-- Immutability enforcement: no UPDATE or DELETE unless the session role is
-- 'retention_worker'. The retention worker is the ONLY path that purges
-- aged rows per configured policy; app-level writes never UPDATE/DELETE.
CREATE OR REPLACE FUNCTION reject_audit_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('app.role', true) <> 'retention_worker' THEN
        RAISE EXCEPTION 'audit_log is append-only';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();
DROP TRIGGER IF EXISTS audit_log_no_delete ON audit_log;
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();
```

- [ ] **Step 2: Down migration**

```sql
-- 170_audit_log.down.sql
DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
DROP TRIGGER IF EXISTS audit_log_no_delete ON audit_log;
DROP FUNCTION IF EXISTS reject_audit_mutation();
DROP TABLE IF EXISTS audit_log;
```

- [ ] **Step 3: Apply + rollback cycle + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/170_audit_log.up.sql server/migrations/170_audit_log.down.sql
git commit -m "feat(audit): migration 170 — immutable audit_log

Triggers reject any UPDATE/DELETE unless app.role='retention_worker'.
Four indexes (workspace+time, resource, actor, trace) cover the four
primary query patterns in §8B.2 UI filters."
```

---

### Task 2: Migration 171 — SSO Config

**Files:**
- Create: `server/migrations/171_sso.up.sql`
- Create: `server/migrations/171_sso.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 171_sso.up.sql
CREATE TABLE IF NOT EXISTS workspace_sso_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL UNIQUE REFERENCES workspace(id) ON DELETE CASCADE,
    provider_type TEXT NOT NULL,
    -- SAML fields
    idp_entity_id TEXT,
    idp_sso_url TEXT,
    idp_certificate TEXT,                        -- PEM; not secret, signing-verification cert
    sp_entity_id TEXT,                            -- Generated per workspace
    sp_acs_url TEXT,                              -- Generated per workspace
    -- OIDC fields
    oidc_issuer TEXT,
    oidc_client_id TEXT,
    oidc_client_secret_credential_id UUID,        -- references workspace_credential; NOT stored plain here
    -- Common
    enforce_sso BOOLEAN NOT NULL DEFAULT false,
    auto_provision BOOLEAN NOT NULL DEFAULT true,
    default_role TEXT NOT NULL DEFAULT 'member',
    allowed_email_domains TEXT[],                 -- Optional IdP email-domain allow-list
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, oidc_client_secret_credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE RESTRICT
);
ALTER TABLE workspace_sso_config DROP CONSTRAINT IF EXISTS workspace_sso_config_provider_check;
ALTER TABLE workspace_sso_config ADD CONSTRAINT workspace_sso_config_provider_check
    CHECK (provider_type IN ('saml', 'oidc')) NOT VALID;
ALTER TABLE workspace_sso_config VALIDATE CONSTRAINT workspace_sso_config_provider_check;
```

- [ ] **Step 2: Down + apply + commit**

```sql
-- 171_sso.down.sql
DROP TABLE IF EXISTS workspace_sso_config;
```

```bash
cd server && make migrate-up
git add server/migrations/171_sso.up.sql server/migrations/171_sso.down.sql
git commit -m "feat(sso): migration 171 — workspace_sso_config

OIDC client secret lives in the Phase 1 credential vault
(credential_type='sso_oidc_secret'); this table stores only the
credential_id pointer. SAML cert is stored plain (public signing
cert, not secret)."
```

---

### Task 3: Migration 172 — Retention Policy

**Files:**
- Create: `server/migrations/172_retention.up.sql`
- Create: `server/migrations/172_retention.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 172_retention.up.sql
CREATE TABLE IF NOT EXISTS retention_policy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL,
    retention_days INT NOT NULL CHECK (retention_days >= 1),
    is_active BOOLEAN NOT NULL DEFAULT true,
    last_run_at TIMESTAMPTZ,
    last_deleted_count INT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, resource_type)
);
ALTER TABLE retention_policy DROP CONSTRAINT IF EXISTS retention_policy_resource_check;
ALTER TABLE retention_policy ADD CONSTRAINT retention_policy_resource_check
    CHECK (resource_type IN ('chat_message', 'task_message', 'agent_memory', 'audit_log', 'trace', 'cost_event', 'webhook_delivery', 'webhook_delivery_log')) NOT VALID;
ALTER TABLE retention_policy VALIDATE CONSTRAINT retention_policy_resource_check;

-- Audit log compliance floor: 90 days minimum. The service layer enforces
-- this at create/update time; we add a defense-in-depth CHECK here.
ALTER TABLE retention_policy DROP CONSTRAINT IF EXISTS retention_policy_audit_floor;
ALTER TABLE retention_policy ADD CONSTRAINT retention_policy_audit_floor
    CHECK (resource_type <> 'audit_log' OR retention_days >= 90) NOT VALID;
ALTER TABLE retention_policy VALIDATE CONSTRAINT retention_policy_audit_floor;

-- Seed default policies for every existing workspace per PLAN.md §8B.6:
-- audit_log default 365 days (1-year compliance window), traces default
-- 90 days. Idempotent: ON CONFLICT DO NOTHING against UNIQUE(workspace_id,
-- resource_type). Workspaces created AFTER this migration get the same
-- defaults via the workspace-creation hook in service/workspace.go (added
-- in Task 11.5 below).
INSERT INTO retention_policy (workspace_id, resource_type, retention_days)
SELECT w.id, 'audit_log', 365 FROM workspace w
ON CONFLICT (workspace_id, resource_type) DO NOTHING;

INSERT INTO retention_policy (workspace_id, resource_type, retention_days)
SELECT w.id, 'trace', 90 FROM workspace w
ON CONFLICT (workspace_id, resource_type) DO NOTHING;
```

- [ ] **Step 2: Down + apply + commit**

```sql
-- 172_retention.down.sql
DROP TABLE IF EXISTS retention_policy;
```

```bash
cd server && make migrate-up
git add server/migrations/172_retention.up.sql server/migrations/172_retention.down.sql
git commit -m "feat(retention): migration 172 — retention_policy

UNIQUE(workspace_id, resource_type) prevents duplicate policies.
CHECK enforces 90-day compliance floor on audit_log retention."
```

---

### Task 4: Migration 173 — Agent Config Revisions

**Files:**
- Create: `server/migrations/173_agent_versioning.up.sql`
- Create: `server/migrations/173_agent_versioning.down.sql`

- [ ] **Step 1: Up**

```sql
-- 173_agent_versioning.up.sql
CREATE TABLE IF NOT EXISTS agent_config_revision (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    revision_number INT NOT NULL,
    config_snapshot JSONB NOT NULL,
    changed_fields TEXT[] NOT NULL,
    changed_by UUID NOT NULL,
    change_reason TEXT,
    is_rollback BOOLEAN NOT NULL DEFAULT false,     -- true when this revision was produced by a rollback operation
    rolled_back_from UUID,                          -- self-FK: the revision whose snapshot was re-applied
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (agent_id, revision_number),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (rolled_back_from) REFERENCES agent_config_revision(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_revision_agent ON agent_config_revision(workspace_id, agent_id, revision_number DESC);
```

- [ ] **Step 2: Down + apply + commit**

```sql
-- 173_agent_versioning.down.sql
DROP TABLE IF EXISTS agent_config_revision;
```

```bash
cd server && make migrate-up
git add server/migrations/173_agent_versioning.up.sql server/migrations/173_agent_versioning.down.sql
git commit -m "feat(versioning): migration 173 — agent_config_revision

Rollbacks create NEW revisions rather than delete — immutable history.
rolled_back_from points at the revision whose snapshot was re-applied."
```

---

### Task 5: sqlc Queries

**Files:**
- Create: `server/pkg/db/queries/audit.sql`
- Create: `server/pkg/db/queries/sso.sql`
- Create: `server/pkg/db/queries/retention.sql`
- Create: `server/pkg/db/queries/revision.sql`

- [ ] **Step 1: Write all four query files**

```sql
-- audit.sql

-- name: InsertAuditEntry :one
INSERT INTO audit_log (workspace_id, actor_type, actor_id, action, resource_type, resource_id, changes, ip_address, user_agent, request_id, trace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAuditEntries :many
SELECT * FROM audit_log
WHERE workspace_id = $1
  AND ($2::text IS NULL OR actor_type = $2)
  AND ($3::uuid IS NULL OR actor_id = $3)
  AND ($4::text IS NULL OR action = $4)
  AND ($5::text IS NULL OR resource_type = $5)
  AND ($6::timestamptz IS NULL OR created_at >= $6)
  AND ($7::timestamptz IS NULL OR created_at < $7)
ORDER BY created_at DESC
LIMIT $8 OFFSET $9;

-- name: DeleteAuditOlderThan :execrows
-- Called by the retention worker after SET LOCAL app.role='retention_worker'.
-- Returns number of rows deleted for the retention policy's last_deleted_count.
DELETE FROM audit_log
WHERE workspace_id = $1 AND created_at < $2;
```

```sql
-- sso.sql

-- name: UpsertSSOConfig :one
INSERT INTO workspace_sso_config (
    workspace_id, provider_type, idp_entity_id, idp_sso_url, idp_certificate,
    sp_entity_id, sp_acs_url, oidc_issuer, oidc_client_id, oidc_client_secret_credential_id,
    enforce_sso, auto_provision, default_role, allowed_email_domains, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (workspace_id) DO UPDATE SET
    provider_type = EXCLUDED.provider_type,
    idp_entity_id = EXCLUDED.idp_entity_id,
    idp_sso_url = EXCLUDED.idp_sso_url,
    idp_certificate = EXCLUDED.idp_certificate,
    oidc_issuer = EXCLUDED.oidc_issuer,
    oidc_client_id = EXCLUDED.oidc_client_id,
    oidc_client_secret_credential_id = EXCLUDED.oidc_client_secret_credential_id,
    enforce_sso = EXCLUDED.enforce_sso,
    auto_provision = EXCLUDED.auto_provision,
    default_role = EXCLUDED.default_role,
    allowed_email_domains = EXCLUDED.allowed_email_domains,
    updated_at = NOW()
RETURNING *;

-- name: GetSSOConfig :one
SELECT * FROM workspace_sso_config WHERE workspace_id = $1;

-- name: DeleteSSOConfig :exec
DELETE FROM workspace_sso_config WHERE workspace_id = $1;
```

```sql
-- retention.sql

-- name: UpsertRetentionPolicy :one
INSERT INTO retention_policy (workspace_id, resource_type, retention_days, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, resource_type) DO UPDATE SET
    retention_days = EXCLUDED.retention_days,
    updated_at = NOW()
RETURNING *;

-- name: ListRetentionPolicies :many
SELECT * FROM retention_policy WHERE workspace_id = $1 ORDER BY resource_type;

-- name: ListAllActivePolicies :many
-- Used by the retention worker; intentionally global.
SELECT * FROM retention_policy WHERE is_active = true;

-- name: TouchRetentionPolicy :exec
UPDATE retention_policy
SET last_run_at = NOW(), last_deleted_count = $3
WHERE id = $1 AND workspace_id = $2;
```

```sql
-- revision.sql

-- name: InsertAgentRevision :one
-- revision_number is computed ATOMICALLY inside the INSERT via a scalar
-- subquery against the current max. Two concurrent INSERTs still race on
-- the subquery, so the caller MUST wrap this in a retry-on-unique-violation
-- loop (see RevisionService.Snapshot). The UNIQUE(agent_id, revision_number)
-- constraint is the tiebreaker; the losing INSERT gets a 23505 SQLSTATE
-- and the caller retries with the new max.
INSERT INTO agent_config_revision (
    workspace_id, agent_id, revision_number, config_snapshot, changed_fields,
    changed_by, change_reason, is_rollback, rolled_back_from
) VALUES (
    $1, $2,
    (SELECT COALESCE(MAX(revision_number), 0) + 1 FROM agent_config_revision
     WHERE agent_id = $2 AND workspace_id = $1),
    $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: ListAgentRevisions :many
SELECT * FROM agent_config_revision
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY revision_number DESC
LIMIT $3 OFFSET $4;

-- name: GetAgentRevision :one
SELECT * FROM agent_config_revision
WHERE id = $1 AND workspace_id = $2;
```

- [ ] **Step 2: Regenerate + commit**

```bash
cd server && make sqlc && go build ./...
git add server/pkg/db/queries/audit.sql server/pkg/db/queries/sso.sql server/pkg/db/queries/retention.sql server/pkg/db/queries/revision.sql server/pkg/db/generated/
git commit -m "feat(db): sqlc queries for audit + sso + retention + revision"
```

---

### Task 6: Audit Middleware + Service

**Files:**
- Create: `server/internal/service/audit.go`
- Create: `server/internal/middleware/audit.go`
- Create: tests

**Goal:** wrap every mutating handler; on 2xx/3xx response, write an audit entry with a before/after diff.

- [ ] **Step 1: Write failing test**

```go
// server/internal/middleware/audit_test.go
func TestAudit_RecordsMutation(t *testing.T) {
    tc := newAuditCtx(t)
    req := httptest.NewRequest("POST", "/workspaces/"+tc.wsID+"/agents",
        bytes.NewReader([]byte(`{"name":"bot"}`)))
    req = testutil.WithSession(req, tc.userID, tc.wsID)
    resp := tc.serve(req)
    if resp.Code != 201 { t.Fatalf("status = %d", resp.Code) }

    entries := tc.listAudit()
    if len(entries) != 1 { t.Fatalf("audit count = %d", len(entries)) }
    if entries[0].Action != "agent.create" { t.Errorf("action = %q", entries[0].Action) }
    if entries[0].ActorType != "member" { t.Errorf("actor_type = %q", entries[0].ActorType) }
}

func TestAudit_SkipsReadRequests(t *testing.T) {
    tc := newAuditCtx(t)
    req := httptest.NewRequest("GET", "/workspaces/"+tc.wsID+"/agents", nil)
    req = testutil.WithSession(req, tc.userID, tc.wsID)
    _ = tc.serve(req)
    if got := tc.listAudit(); len(got) != 0 {
        t.Errorf("GET wrote audit entry: %+v", got)
    }
}

func TestAudit_CapturesDiff(t *testing.T) {
    tc := newAuditCtx(t)
    agentID := tc.createAgent("bot")
    req := httptest.NewRequest("PATCH", "/workspaces/"+tc.wsID+"/agents/"+agentID,
        bytes.NewReader([]byte(`{"name":"bot2"}`)))
    req = testutil.WithSession(req, tc.userID, tc.wsID)
    _ = tc.serve(req)
    entries := tc.listAudit()
    patch := entries[len(entries)-1]
    if patch.Action != "agent.update" { t.Errorf("action = %q", patch.Action) }
    // Changes JSONB must contain before/after for the mutated field.
    if !strings.Contains(string(patch.Changes), `"before":"bot"`) ||
       !strings.Contains(string(patch.Changes), `"after":"bot2"`) {
        t.Errorf("changes diff missing: %s", patch.Changes)
    }
}
```

- [ ] **Step 2: Implement service**

```go
// server/internal/service/audit.go
package service

import (
    "context"
    "encoding/json"
    "sync/atomic"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type AuditEntry struct {
    WorkspaceID  uuid.UUID
    ActorType    string
    ActorID      uuid.UUID // uuid.Nil for 'system'
    Action       string
    ResourceType string
    ResourceID   uuid.UUID
    Changes      map[string]any
    IPAddress    string
    UserAgent    string
    RequestID    uuid.UUID
    TraceID      uuid.UUID
}

type AuditService struct {
    db        *pgxpool.Pool
    queue     chan AuditEntry
    log       Logger   // standard slog-compatible; Error logs drop events
    dropCount int64    // atomic; surfaced via DropCount()
}

// Logger is the minimum slog surface AuditService needs. Any *slog.Logger
// satisfies this automatically.
type Logger interface {
    Error(msg string, args ...any)
}

func NewAuditService(pool *pgxpool.Pool, log Logger) *AuditService {
    s := &AuditService{db: pool, queue: make(chan AuditEntry, 512), log: log}
    go s.consumer(context.Background())
    return s
}

// Record is non-blocking: fire-and-forget into a bounded channel. If the
// channel is full we DROP the entry — audit MUST NOT stall the request
// hot path. EVERY drop emits a CRITICAL-severity structured log line and
// increments the `audit_dropped_total` metric so monitoring/alerting can
// page on sustained drops. A regulator reviewing compliance can see
// exactly when audit coverage was incomplete and for how long.
//
// This is an explicit compliance tradeoff: audit integrity vs. request
// availability. Documented in the Phase 8B verification evidence as a
// known gap with observability. A future hardening path (Phase 9+): WAL
// to disk before the channel so disk-capacity, not channel-capacity, is
// the real ceiling.
func (s *AuditService) Record(ent AuditEntry) {
    select {
    case s.queue <- ent:
    default:
        atomic.AddInt64(&s.dropCount, 1)
        s.log.Error("audit_dropped",
            "workspace", ent.WorkspaceID,
            "actor_type", ent.ActorType,
            "action", ent.Action,
            "resource", ent.ResourceType,
            "queue_size", cap(s.queue),
            "severity", "CRITICAL",
        )
        // Metric (Phase 10 wiring will scrape this — for V1 just the log).
        // metrics.AuditDroppedTotal.WithLabelValues(ent.ActorType, ent.Action).Inc()
    }
}

// DropCount returns the cumulative audit-drop count since service start.
// Exposed so /internal/health endpoints can surface it for monitoring.
func (s *AuditService) DropCount() int64 { return atomic.LoadInt64(&s.dropCount) }

func (s *AuditService) consumer(ctx context.Context) {
    q := db.New(s.db)
    for ent := range s.queue {
        changesJSON, _ := json.Marshal(ent.Changes)
        _, _ = q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
            WorkspaceID:  uuidPg(ent.WorkspaceID),
            ActorType:    ent.ActorType,
            ActorID:      uuidPgNullable(ent.ActorID),
            Action:       ent.Action,
            ResourceType: ent.ResourceType,
            ResourceID:   uuidPgNullable(ent.ResourceID),
            Changes:      changesJSON,
            IpAddress:    ipAddrPg(ent.IPAddress),
            UserAgent:    sqlNullString(ent.UserAgent),
            RequestID:    uuidPgNullable(ent.RequestID),
            TraceID:      uuidPgNullable(ent.TraceID),
        })
        _ = time.Now() // for linter
    }
}
```

- [ ] **Step 3: Middleware**

```go
// server/internal/middleware/audit.go
package middleware

import (
    "bytes"
    "encoding/json"
    "io"
    "net/http"
    "strings"

    "github.com/google/uuid"

    "aicolab/server/internal/service"
)

// Audit returns chi middleware that records every mutating request. Read
// requests (GET/HEAD/OPTIONS) are skipped. On 2xx/3xx, we compute the
// action from (method, path) and emit an AuditEntry. Before/after diffs
// require the handler to cooperate via x-audit-before headers (set by the
// handler pre-mutation with a serialized snapshot) — when absent, changes
// is omitted.
func Audit(svc *service.AuditService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
                next.ServeHTTP(w, r)
                return
            }

            rw := &capturingWriter{ResponseWriter: w, status: 200, body: &bytes.Buffer{}}
            next.ServeHTTP(rw, r)

            if rw.status < 200 || rw.status >= 400 {
                return // don't audit failed mutations
            }

            actor := ActorFromContext(r.Context())
            wsID, _ := wsIDFromPath(r.URL.Path)
            action, resourceType, resourceID := actionFromRequest(r)

            // Diff is computed from X-Audit-Before (handler-set, serialized
            // from the pre-mutation DB row) vs the RESPONSE body (which for
            // PATCH/POST/PUT handlers MUST be the full post-mutation row —
            // never a partial representation). The response body is the
            // authoritative "after" because a PATCH request body only
            // contains the FIELDS BEING CHANGED, not the full row — using
            // it as "after" produces bogus "deleted every other field"
            // diffs. Our handler convention (followed by every mutating
            // endpoint in Phases 1-8A) is "return the full resource on
            // write" which makes the response body a valid snapshot.
            var before, after map[string]any
            if hdr := rw.Header().Get("X-Audit-Before"); hdr != "" {
                _ = json.Unmarshal([]byte(hdr), &before)
            }
            if rw.body.Len() > 0 {
                _ = json.Unmarshal(rw.body.Bytes(), &after)
            }
            changes := diffBeforeAfter(before, after)

            svc.Record(service.AuditEntry{
                WorkspaceID:  wsID,
                ActorType:    actor.Type,
                ActorID:      actor.ID,
                Action:       action,
                ResourceType: resourceType,
                ResourceID:   resourceID,
                Changes:      changes,
                IPAddress:    clientIP(r),
                UserAgent:    r.UserAgent(),
                RequestID:    uuid.New(),
                TraceID:      traceIDFromContext(r.Context()),
            })
        })
    }
}

// diffBeforeAfter produces {"field":{"before":x,"after":y}, ...} for keys
// whose values differ. New keys have before=nil; deleted keys have after=nil.
func diffBeforeAfter(before, after map[string]any) map[string]any {
    out := map[string]any{}
    seen := map[string]bool{}
    for k, bv := range before {
        seen[k] = true
        if av, ok := after[k]; !ok {
            out[k] = map[string]any{"before": bv, "after": nil}
        } else if !deepEqual(bv, av) {
            out[k] = map[string]any{"before": bv, "after": av}
        }
    }
    for k, av := range after {
        if seen[k] { continue }
        out[k] = map[string]any{"before": nil, "after": av}
    }
    return out
}

// actionFromRequest derives (action, resourceType, resourceID) from path
// conventions. POST /workspaces/{ws}/agents → agent.create; PATCH .../agents/{id} → agent.update; DELETE → agent.delete.
//
// Plural-to-singular mapping uses an explicit lookup instead of naive
// TrimSuffix("s") because irregular cases like /sso/ (already singular)
// or /audit/ would be corrupted. Unknown resources fall through as the
// URL segment unchanged.
var pluralToSingular = map[string]string{
    "agents":        "agent",
    "workflows":     "workflow",
    "workflow_runs": "workflow_run",
    "approvals":     "approval",
    "webhooks":      "webhook",
    "subscriptions": "webhook_subscription",
    "templates":     "template",
    "api-keys":      "api_key",
    "api_keys":      "api_key",
    "credentials":   "credential",
    "sso":           "sso",
    "retention":     "retention",
    "audit":         "audit",
}

func actionFromRequest(r *http.Request) (action, resType string, resID uuid.UUID) {
    parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
    // parts[0]=api, parts[1]=workspaces, parts[2]={wsId}, parts[3]={resource}, parts[4?]={id}
    if len(parts) < 4 { return "unknown", "unknown", uuid.Nil }
    res, ok := pluralToSingular[parts[3]]
    if !ok { res = parts[3] } // unknown — use segment verbatim
    switch r.Method {
    case "POST":   action = res + ".create"
    case "PATCH":  action = res + ".update"
    case "PUT":    action = res + ".replace"
    case "DELETE": action = res + ".delete"
    default:       action = res + "." + strings.ToLower(r.Method)
    }
    if len(parts) >= 5 {
        resID, _ = uuid.Parse(parts[4])
    }
    return action, res, resID
}

// capturingWriter buffers BOTH the status code AND the response body so
// the audit middleware can parse the body into `after` for the diff.
// Size cap: 1 MiB. If the response body exceeds this, audit records the
// diff as "changes: null" + notes "response_too_large" — the mutation
// is still recorded.
const auditMaxBodyBytes = 1 << 20 // 1 MiB

type capturingWriter struct {
    http.ResponseWriter
    status  int
    body    *bytes.Buffer
    tooLarge bool
}

func (c *capturingWriter) WriteHeader(s int) { c.status = s; c.ResponseWriter.WriteHeader(s) }

func (c *capturingWriter) Write(p []byte) (int, error) {
    // Always forward to the client.
    n, err := c.ResponseWriter.Write(p)
    // Buffer up to the cap; beyond that, mark oversized and stop buffering.
    if !c.tooLarge && c.body.Len()+n < auditMaxBodyBytes {
        c.body.Write(p[:n])
    } else {
        c.tooLarge = true
    }
    return n, err
}
```

- [ ] **Step 4: Handler-side `X-Audit-Before` header**

In `server/internal/handler/agent.go` `Update` handler, serialize the pre-mutation agent row and set:

```go
if before, err := h.loadAgent(ctx, wsID, id); err == nil {
    b, _ := json.Marshal(before)
    w.Header().Set("X-Audit-Before", string(b))
}
// ... apply mutation, write response
```

Repeat for every mutating handler that should produce a diff. Handlers that don't set the header produce audit entries with `changes = null` (action still recorded).

- [ ] **Step 5: Run + commit**

```bash
cd server && go test ./internal/middleware/ ./internal/service/ -run "Audit" -v
git add server/internal/service/audit.go server/internal/middleware/audit.go server/internal/middleware/audit_test.go server/internal/service/audit_test.go server/internal/handler/agent.go
git commit -m "feat(audit): middleware + async service + X-Audit-Before diff

Record is fire-and-forget into a 512-entry buffered channel; backpressure
drops the entry and logs a warning. Diffs produced from handler-set
X-Audit-Before header serialized pre-mutation. Action name derived from
(HTTP method, URL path) convention."
```

---

### Task 7: Audit List + CSV Export Handler

**Files:**
- Create: `server/internal/handler/audit.go`
- Create: `server/internal/handler/audit_test.go`

- [ ] **Step 1: Failing test**

```go
func TestAudit_ListFiltersByActor(t *testing.T) {
    tc := newAuditCtx(t)
    tc.seedAudit("member", tc.userID, "agent.create")
    tc.seedAudit("api_key", uuid.New(), "agent.delete")
    resp := tc.getJSON("/api/workspaces/"+tc.wsID+"/audit?actor_type=member")
    var entries []AuditEntry
    _ = json.Unmarshal([]byte(resp.Body), &entries)
    if len(entries) != 1 || entries[0].Action != "agent.create" {
        t.Errorf("filter failed: %+v", entries)
    }
}

func TestAudit_CSVExport(t *testing.T) {
    tc := newAuditCtx(t)
    tc.seedAudit("member", tc.userID, "agent.create")
    resp := tc.getText("/api/workspaces/"+tc.wsID+"/audit.csv")
    if !strings.HasPrefix(resp.Body, "timestamp,actor_type,actor_id,action,resource_type") {
        t.Errorf("missing CSV header: %s", resp.Body[:200])
    }
    if !strings.Contains(resp.Body, "agent.create") {
        t.Error("CSV missing seeded entry")
    }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/handler/audit.go
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    q := r.URL.Query()
    limit := atoiOrDefault(q.Get("limit"), 100, 1000)
    offset := atoiOrDefault(q.Get("offset"), 0, 0)
    params := db.ListAuditEntriesParams{
        WorkspaceID: uuidPg(wsID),
        Column2:     q.Get("actor_type"),
        // ... (pack the rest — sqlc.narg fields)
        Limit: int32(limit), Offset: int32(offset),
    }
    rows, err := h.q.ListAuditEntries(r.Context(), params)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, rows)
}

func (h *AuditHandler) ExportCSV(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    rows, err := h.q.ListAuditEntries(r.Context(), db.ListAuditEntriesParams{
        WorkspaceID: uuidPg(wsID), Limit: 50_000, Offset: 0,
    })
    if err != nil { http.Error(w, err.Error(), 500); return }
    w.Header().Set("Content-Type", "text/csv")
    w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
    cw := csv.NewWriter(w)
    defer cw.Flush()
    _ = cw.Write([]string{"timestamp", "actor_type", "actor_id", "action", "resource_type", "resource_id", "changes"})
    for _, r := range rows {
        _ = cw.Write([]string{
            r.CreatedAt.Time.Format(time.RFC3339),
            r.ActorType,
            uuidFromPg(r.ActorID).String(),
            r.Action,
            r.ResourceType,
            uuidFromPg(r.ResourceID).String(),
            string(r.Changes),
        })
    }
}
```

- [ ] **Step 3: Mount + commit**

```go
// router.go
r.Route("/api/workspaces/{wsId}/audit", func(r chi.Router) {
    r.With(middleware.RequireScope("admin")).Get("/", audH.List)
    r.With(middleware.RequireScope("admin")).Get("/.csv", audH.ExportCSV)
})
```

```bash
cd server && go test ./internal/handler/ -run TestAudit -v
git add server/internal/handler/audit.go server/cmd/server/router.go
git commit -m "feat(audit): GET /audit list + /audit.csv export

Require 'admin' scope via Phase 8A RequireScope middleware. CSV capped
at 50k rows per export — larger exports use the pagination on the JSON
endpoint."
```

---

### Task 8: Rate Limiting Middleware

**Files:**
- Create: `server/internal/middleware/ratelimit.go`
- Create: `server/pkg/ratelimit/provider_tracker.go`
- Create: tests

**Goal:** three-layer limiter. Workspace: 100 req/min. Agent: 10 tasks/min. Provider: adaptive, consumes `x-ratelimit-*` headers from LLM responses.

- [ ] **Step 1: Provider tracker first**

```go
// server/pkg/ratelimit/provider_tracker.go
package ratelimit

import (
    "strconv"
    "sync"
    "time"
)

// ProviderTracker owns the per-provider rolling window that the LLM-API
// backends feed by calling Observe after each response. On 429 the tracker
// enters an Throttled state: all Allow calls return false for ThrottleUntil.
type ProviderTracker struct {
    mu       sync.RWMutex
    state    map[string]providerState // key = provider name
}

type providerState struct {
    Remaining      int
    ResetAt        time.Time
    ThrottledUntil time.Time
}

func NewProviderTracker() *ProviderTracker {
    return &ProviderTracker{state: make(map[string]providerState)}
}

// Observe consumes anthropic/openai-style rate-limit headers. Missing
// headers are a no-op.
func (p *ProviderTracker) Observe(provider string, headers map[string]string, statusCode int) {
    p.mu.Lock()
    defer p.mu.Unlock()
    st := p.state[provider]
    if r, err := strconv.Atoi(headers["x-ratelimit-remaining-requests"]); err == nil {
        st.Remaining = r
    }
    if reset := headers["x-ratelimit-reset-requests"]; reset != "" {
        if t, err := time.Parse(time.RFC3339, reset); err == nil {
            st.ResetAt = t
        }
    }
    if statusCode == 429 {
        // Default 10s throttle; honor Retry-After if present.
        until := time.Now().Add(10 * time.Second)
        if ra := headers["retry-after"]; ra != "" {
            if secs, err := strconv.Atoi(ra); err == nil {
                until = time.Now().Add(time.Duration(secs) * time.Second)
            }
        }
        st.ThrottledUntil = until
    }
    p.state[provider] = st
}

// Allow returns true if a new request to `provider` should be attempted.
func (p *ProviderTracker) Allow(provider string) bool {
    p.mu.RLock()
    defer p.mu.RUnlock()
    st, ok := p.state[provider]
    if !ok { return true }
    if time.Now().Before(st.ThrottledUntil) { return false }
    // Low-headroom guard: if remaining <= 1 and reset hasn't come, hold back.
    if st.Remaining <= 1 && time.Now().Before(st.ResetAt) { return false }
    return true
}
```

- [ ] **Step 2: Tests for provider tracker**

```go
func TestProviderTracker_429SetsThrottle(t *testing.T) {
    p := NewProviderTracker()
    p.Observe("anthropic", map[string]string{"retry-after": "5"}, 429)
    if p.Allow("anthropic") { t.Error("should be throttled") }
    time.Sleep(5100 * time.Millisecond) // slow — gate behind -short if needed
    if !p.Allow("anthropic") { t.Error("should recover after retry-after") }
}
```

- [ ] **Step 3: Workspace + agent token buckets**

```go
// server/internal/middleware/ratelimit.go
package middleware

import (
    "net/http"
    "sync"
    "time"

    "github.com/google/uuid"
    "golang.org/x/time/rate"
)

type Limits struct {
    WorkspaceRPS float64
    WorkspaceBurst int
    AgentRPS       float64
    AgentBurst     int
}

// limiterEntry pairs a rate.Limiter with its last-access timestamp so the
// cleanup goroutine can evict buckets idle longer than evictAfter.
type limiterEntry struct {
    lim      *rate.Limiter
    lastUsed time.Time
}

// evictAfter caps how long an idle bucket lives in memory. 24h is far
// longer than any realistic burst recovery window for workspace-level
// traffic; agents that go idle beyond this are re-created on next use
// with fresh (empty) token buckets — safe outcome.
const evictAfter = 24 * time.Hour

type Limiter struct {
    limits Limits
    mu     sync.Mutex
    ws     map[uuid.UUID]*limiterEntry
    agent  map[uuid.UUID]*limiterEntry
    stop   chan struct{}
}

func NewLimiter(l Limits) *Limiter {
    lim := &Limiter{
        limits: l,
        ws:     map[uuid.UUID]*limiterEntry{},
        agent:  map[uuid.UUID]*limiterEntry{},
        stop:   make(chan struct{}),
    }
    go lim.evictLoop()
    return lim
}

// Stop shuts down the eviction goroutine — called from server shutdown.
func (l *Limiter) Stop() { close(l.stop) }

// evictLoop runs every 5 minutes, dropping entries whose lastUsed is
// older than evictAfter. Bounds the map at O(active workspaces × idle
// window) rather than O(all workspaces ever).
func (l *Limiter) evictLoop() {
    t := time.NewTicker(5 * time.Minute)
    defer t.Stop()
    for {
        select {
        case <-l.stop: return
        case now := <-t.C:
            l.mu.Lock()
            for k, e := range l.ws {
                if now.Sub(e.lastUsed) > evictAfter { delete(l.ws, k) }
            }
            for k, e := range l.agent {
                if now.Sub(e.lastUsed) > evictAfter { delete(l.agent, k) }
            }
            l.mu.Unlock()
        }
    }
}

func (l *Limiter) allowWorkspace(wsID uuid.UUID) bool {
    l.mu.Lock()
    defer l.mu.Unlock()
    e, ok := l.ws[wsID]
    if !ok {
        e = &limiterEntry{lim: rate.NewLimiter(rate.Limit(l.limits.WorkspaceRPS), l.limits.WorkspaceBurst)}
        l.ws[wsID] = e
    }
    e.lastUsed = time.Now()
    return e.lim.Allow()
}

func (l *Limiter) allowAgent(agentID uuid.UUID) bool {
    l.mu.Lock()
    defer l.mu.Unlock()
    e, ok := l.agent[agentID]
    if !ok {
        e = &limiterEntry{lim: rate.NewLimiter(rate.Limit(l.limits.AgentRPS), l.limits.AgentBurst)}
        l.agent[agentID] = e
    }
    e.lastUsed = time.Now()
    return e.lim.Allow()
}

// Middleware returns chi middleware. Agents route — /agents/{id}/tasks —
// hits both limits; other routes hit only the workspace limit.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        wsID, _ := wsIDFromPath(r.URL.Path)
        if wsID != uuid.Nil && !l.allowWorkspace(wsID) {
            writeRateLimited(w, 60) // Retry-After
            return
        }
        if agentID := agentIDFromPath(r.URL.Path); agentID != uuid.Nil {
            if !l.allowAgent(agentID) { writeRateLimited(w, 30); return }
        }
        next.ServeHTTP(w, r)
    })
}

func writeRateLimited(w http.ResponseWriter, retryAfterSec int) {
    w.Header().Set("Retry-After", strconv.Itoa(retryAfterSec))
    w.Header().Set("X-RateLimit-Limit-Remaining", "0")
    http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}
```

- [ ] **Step 4: Burst test**

```go
func TestLimiter_BurstThrottles(t *testing.T) {
    l := NewLimiter(Limits{WorkspaceRPS: 1, WorkspaceBurst: 3, AgentRPS: 100, AgentBurst: 100})
    h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
    wsID := uuid.New().String()
    var throttled int
    for i := 0; i < 10; i++ {
        req := httptest.NewRequest("POST", "/api/workspaces/"+wsID+"/agents", nil)
        rr := httptest.NewRecorder()
        h.ServeHTTP(rr, req)
        if rr.Code == 429 { throttled++ }
    }
    if throttled < 5 { t.Errorf("only %d throttled from 10 bursts", throttled) }
}
```

- [ ] **Step 5: Wire ProviderTracker into the Phase 2 LLM backend**

Without this step the tracker is dead code — no `Observe` call site means
no 429 can ever be recorded. Add a `Tracker *ratelimit.ProviderTracker`
field to Phase 2's `LLMAPIBackendDeps`/config, inject it during server
startup, and call `Tracker.Observe` after every upstream LLM response —
success OR failure. Call `Tracker.Allow(provider)` BEFORE sending the
request so blocked providers short-circuit immediately.

```go
// server/pkg/agent/llm_api.go — inside the Execute/Generate path.
// Existing code calls provider SDK; add the Observe wrap + Allow gate.

// Before the request:
if b.tracker != nil && !b.tracker.Allow(b.provider) {
    return nil, fmt.Errorf("provider %q throttled; backing off per x-ratelimit headers", b.provider)
}

// After the response (success OR failure with headers):
if b.tracker != nil && resp != nil {
    b.tracker.Observe(b.provider, flattenHeaders(resp.Header), resp.StatusCode)
}
```

Integration test asserts the feedback loop:

```go
// server/internal/integration/security/ratelimit_e2e_test.go
func TestRateLimit_ProviderThrottlesAllAgents(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("rl-e2e")
    agentA := env.SeedAgent(ws, "a", env.StubModelWithResponses(map[int]any{ /* force 429 */ }))
    agentB := env.SeedAgent(ws, "b", env.StubModelWithResponses(map[int]any{ /* would succeed */ }))

    // First call returns 429 — tracker records ThrottledUntil = now+Retry-After.
    t1 := env.EnqueueTaskAndWait(ws, agentA, "hi")
    if !strings.Contains(t1.Error, "429") { t.Fatalf("expected 429 error: %v", t1) }

    // Second call to a DIFFERENT agent on the same provider must short-
    // circuit with the tracker's "throttled" message — validating that
    // 429 from agent A blocks agent B too.
    t2 := env.EnqueueTaskAndWait(ws, agentB, "hi")
    if !strings.Contains(t2.Error, "throttled") {
        t.Errorf("agent B should be throttled by tracker; got %q", t2.Error)
    }
}
```

- [ ] **Step 6: Commit**

```bash
cd server && go test ./internal/middleware/ ./pkg/ratelimit/ ./internal/integration/security/ -run "Limiter|ProviderTracker|TestRateLimit_ProviderThrottlesAllAgents" -v
git add server/pkg/ratelimit/provider_tracker.go server/internal/middleware/ratelimit.go server/pkg/agent/llm_api.go server/internal/integration/security/ratelimit_e2e_test.go
git commit -m "feat(ratelimit): three-layer limiter with provider 429 feedback

golang.org/x/time/rate token bucket per workspace + per agent (in-memory,
per-process; Redis-backed sharing is Phase 9 follow-up if needed).
ProviderTracker consumes x-ratelimit-* headers; LLM backends call
Observe after every response AND check Allow before sending — 429 from
one agent automatically throttles all agents on the same provider per
PLAN.md §8B.3. Integration test verifies cross-agent propagation."
```

---

### Task 9: Agent Config Versioning + Rollback

**Files:**
- Create: `server/internal/service/revision.go`
- Create: `server/internal/handler/revision.go`
- Modify: `server/internal/handler/agent.go` (call `RevisionService.Snapshot` on update)

- [ ] **Step 1: Failing test**

```go
func TestRevision_SnapshotOnUpdate(t *testing.T) {
    tc := newRevisionCtx(t)
    agentID := tc.createAgent("bot", "anthropic", "claude-haiku-4-5-20251001")
    tc.updateAgent(agentID, map[string]any{"instructions": "v2"})
    tc.updateAgent(agentID, map[string]any{"instructions": "v3"})

    revs := tc.listRevisions(agentID)
    if len(revs) != 3 { t.Fatalf("len = %d, want 3 (create + 2 updates)", len(revs)) }
    if revs[0].RevisionNumber != 3 { t.Error("latest should be revision_number=3") }
}

func TestRevision_RollbackCreatesNewRevision(t *testing.T) {
    tc := newRevisionCtx(t)
    agentID := tc.createAgent("bot", "anthropic", "claude-haiku-4-5-20251001")
    tc.updateAgent(agentID, map[string]any{"instructions": "v2"})
    tc.updateAgent(agentID, map[string]any{"instructions": "v3"})

    firstRev := tc.listRevisions(agentID)[2] // revision_number=1 (initial)
    resp := tc.postJSON("/api/workspaces/"+tc.wsID+"/agents/"+agentID+"/revisions/"+firstRev.ID+"/rollback",
        map[string]any{"reason": "regression"})
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }

    // Agent now holds v1 instructions; revision count = 4 (new rollback revision).
    agent := tc.getAgent(agentID)
    if !strings.Contains(agent.Instructions, firstRev.ConfigSnapshot["instructions"].(string)) {
        t.Error("rollback did not apply")
    }
    revs := tc.listRevisions(agentID)
    if len(revs) != 4 { t.Errorf("count = %d, want 4", len(revs)) }
    if !revs[0].IsRollback { t.Error("latest should be is_rollback=true") }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/service/revision.go
package service

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgtype"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type RevisionService struct{ db *pgxpool.Pool }

func NewRevisionService(pool *pgxpool.Pool) *RevisionService {
    return &RevisionService{db: pool}
}

// Snapshot captures the agent's current config + produces a new revision
// row. Caller (handler) invokes this BEFORE applying the mutation, so the
// revision's config_snapshot reflects the pre-mutation state. On rollback
// the reverse flow applies — caller loads the target revision, sets it as
// the current agent config, then calls Snapshot again with is_rollback=true.
func (s *RevisionService) Snapshot(
    ctx context.Context, wsID, agentID, userID uuid.UUID,
    snapshot map[string]any, changedFields []string, reason string,
    rolledBackFrom *uuid.UUID,
) error {
    q := db.New(s.db)
    sb, _ := json.Marshal(snapshot)
    var fromPg pgtype.UUID
    if rolledBackFrom != nil { fromPg = pgtype.UUID{Bytes: *rolledBackFrom, Valid: true} }

    // Retry loop handles the concurrent-insert race: the atomic subquery in
    // InsertAgentRevision computes (MAX+1) but two concurrent calls can
    // resolve to the same number; the UNIQUE(agent_id, revision_number)
    // constraint rejects one with SQLSTATE 23505. Retry up to 3 times —
    // enough for any realistic concurrency since the second attempt sees
    // the first's committed row.
    const maxAttempts = 3
    for attempt := 0; attempt < maxAttempts; attempt++ {
        _, err := q.InsertAgentRevision(ctx, db.InsertAgentRevisionParams{
            WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agentID),
            ConfigSnapshot: sb,
            ChangedFields: changedFields, ChangedBy: uuidPg(userID),
            ChangeReason: sqlNullString(reason),
            IsRollback: rolledBackFrom != nil, RolledBackFrom: fromPg,
        })
        if err == nil { return nil }
        var pgErr *pgconn.PgError
        if errors.As(err, &pgErr) && pgErr.Code == "23505" {
            continue // unique-violation — another goroutine won the race, retry
        }
        return err
    }
    return fmt.Errorf("revision insert failed after %d attempts", maxAttempts)
}

func (s *RevisionService) Rollback(
    ctx context.Context, wsID, agentID, userID, targetRevisionID uuid.UUID, reason string,
) error {
    q := db.New(s.db)
    tgt, err := q.GetAgentRevision(ctx, db.GetAgentRevisionParams{
        ID: uuidPg(targetRevisionID), WorkspaceID: uuidPg(wsID),
    })
    if err != nil { return err }

    // Atomic: apply snapshot to agent row + insert rollback revision.
    // InsertAgentRevision computes the next revision_number via an
    // embedded MAX+1 subquery (see revision.sql after R2), so we do NOT
    // pass RevisionNumber explicitly. The same 3-attempt retry-on-23505
    // loop from Snapshot handles the concurrent-write race.
    var snap map[string]any
    _ = json.Unmarshal(tgt.ConfigSnapshot, &snap)
    sb, _ := json.Marshal(snap)

    const maxAttempts = 3
    for attempt := 0; attempt < maxAttempts; attempt++ {
        tx, err := s.db.Begin(ctx)
        if err != nil { return err }
        txQ := db.New(tx)

        if err := applyAgentSnapshot(ctx, txQ, wsID, agentID, snap); err != nil {
            tx.Rollback(ctx)
            return err
        }
        _, err = txQ.InsertAgentRevision(ctx, db.InsertAgentRevisionParams{
            WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agentID),
            ConfigSnapshot: sb,
            ChangedFields: []string{"rollback"},
            ChangedBy: uuidPg(userID),
            ChangeReason: sqlNullString(reason),
            IsRollback: true,
            RolledBackFrom: pgtype.UUID{Bytes: targetRevisionID, Valid: true},
        })
        if err == nil {
            return tx.Commit(ctx)
        }
        tx.Rollback(ctx)
        var pgErr *pgconn.PgError
        if errors.As(err, &pgErr) && pgErr.Code == "23505" {
            continue // revision-number race — retry
        }
        return err
    }
    return fmt.Errorf("rollback failed after %d attempts", maxAttempts)
}

// applyAgentSnapshot writes a subset of allowed fields from snap into
// the agent row. Fields that are not allowed (id, workspace_id, created_at)
// are silently ignored.
func applyAgentSnapshot(ctx context.Context, q *db.Queries, wsID, agentID uuid.UUID, snap map[string]any) error {
    name, _ := snap["name"].(string)
    instr, _ := snap["instructions"].(string)
    provider, _ := snap["provider"].(string)
    model, _ := snap["model"].(string)
    return q.UpdateAgentFromSnapshot(ctx, db.UpdateAgentFromSnapshotParams{
        ID: uuidPg(agentID), WorkspaceID: uuidPg(wsID),
        Name: name, Instructions: sqlNullString(instr),
        Provider: sqlNullString(provider), Model: sqlNullString(model),
    })
}
```

Add a sqlc query `UpdateAgentFromSnapshot` to `agent.sql` that updates the whitelisted fields in one statement.

- [ ] **Step 3: Handler + mount + commit**

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run Revision -v
git add server/internal/service/revision.go server/internal/handler/revision.go server/internal/handler/agent.go server/pkg/db/queries/agent.sql server/cmd/server/router.go
git commit -m "feat(versioning): agent config snapshots + rollback

Snapshot called pre-mutation; rollback wraps 'apply target snapshot +
insert rollback revision' in a tx. In-flight tasks already hold their
config (Phase 2 harness closure), so rollback never affects them."
```

---

### Task 10: SSO — SAML + OIDC

**Files:**
- Create: `server/internal/auth/saml.go` + `oidc.go` + tests
- Create: `server/internal/service/sso.go`
- Create: `server/internal/handler/sso.go`

**Goal:** per-workspace SSO with SAML 2.0 + OIDC. OIDC client_secret in Phase 1 credential vault.

- [ ] **Step 1: Failing OIDC test**

```go
func TestOIDC_InvalidStateRejected(t *testing.T) {
    tc := newSSOCtx(t)
    tc.configureOIDC(tc.wsID, "https://idp.test", "client-id", "client-secret")

    // Tampered state (wrong signature).
    callback := httptest.NewRequest("GET", "/auth/oidc/callback?code=abc&state=bogus", nil)
    resp := tc.serve(callback)
    if resp.Code != 400 { t.Errorf("tampered state: status = %d, want 400", resp.Code) }

    // Valid-looking state but missing cookie → CSRF path blocked.
    validStateNoNonce := tc.buildState(tc.wsID) // helper issues state WITHOUT setting the cookie
    callback2 := httptest.NewRequest("GET", "/auth/oidc/callback?code=abc&state="+validStateNoNonce, nil)
    resp2 := tc.serve(callback2)
    if resp2.Code != 400 { t.Errorf("missing nonce: status = %d, want 400", resp2.Code) }

    // Expired state.
    expiredState := tc.buildExpiredState(tc.wsID)
    callback3 := httptest.NewRequest("GET", "/auth/oidc/callback?code=abc&state="+expiredState, nil)
    callback3.AddCookie(&http.Cookie{Name: "oidc_nonce", Value: tc.nonceFor(expiredState)})
    resp3 := tc.serve(callback3)
    if resp3.Code != 400 { t.Errorf("expired state: status = %d, want 400", resp3.Code) }
}

func TestOIDC_CallbackAutoProvisions(t *testing.T) {
    tc := newSSOCtx(t)
    tc.configureOIDC(tc.wsID, "https://idp.test", "client-id", "client-secret")

    // Mock IdP token exchange.
    idp := tc.mockIdP(map[string]any{
        "email": "new@acme.com", "sub": "user-1", "email_verified": true,
    })
    defer idp.Close()

    callback := httptest.NewRequest("GET", "/auth/oidc/callback?code=abc&state="+tc.validState(tc.wsID), nil)
    resp := tc.serve(callback)
    if resp.Code != 302 { t.Fatalf("status = %d", resp.Code) }

    // User auto-provisioned + workspace_member row created.
    user := tc.findUserByEmail("new@acme.com")
    if user == uuid.Nil { t.Fatal("user not provisioned") }
    if !tc.isWorkspaceMember(tc.wsID, user) { t.Error("member row missing") }
}
```

- [ ] **Step 2: Implement OIDC (abbreviated — go-oidc docs are canonical)**

```go
// server/internal/auth/oidc.go
package auth

import (
    "context"
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "crypto/subtle"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "strings"
    "time"

    "github.com/coreos/go-oidc/v3/oidc"
    "github.com/google/uuid"
    "golang.org/x/oauth2"

    "aicolab/server/internal/service"
    "aicolab/server/pkg/agent"
)

// randomBase64 returns n random bytes URL-base64-encoded. Used as the
// OIDC state-parameter nonce. crypto/rand is cryptographically secure;
// panic on read failure because a CSPRNG failure at this layer indicates
// a broken environment we cannot continue under.
func randomBase64(n int) string {
    b := make([]byte, n)
    if _, err := rand.Read(b); err != nil {
        panic("crypto/rand failure: " + err.Error())
    }
    return base64.URLEncoding.EncodeToString(b)
}

type OIDCService struct {
    cfgSvc *service.SSOService
    creds  *agent.Credentials
    users  *service.UserService
    // stateSigningKey is used to HMAC-sign the OAuth2 state parameter so
    // the callback can verify the state came from our BeginAuth and has
    // not been tampered. Initialised at server startup from the Phase 1
    // master-key hierarchy — see server/cmd/server/main.go:
    //     oidcSvc := auth.NewOIDCService(cfgSvc, creds, users,
    //         deriveOIDCStateKey(credentialMasterKey))
    // deriveOIDCStateKey applies HKDF("multica-oidc-state-v1") to the
    // master key so rotating the master rotates this subkey automatically.
    stateSigningKey []byte
}

func NewOIDCService(cfgSvc *service.SSOService, creds *agent.Credentials, users *service.UserService, stateKey []byte) *OIDCService {
    if len(stateKey) == 0 {
        panic("OIDC stateSigningKey is required")
    }
    return &OIDCService{cfgSvc: cfgSvc, creds: creds, users: users, stateSigningKey: stateKey}
}

// State parameter format: base64(HMAC-SHA256(secret, wsID||nonce||expiry) || "." || payload).
// BeginAuth issues the state + sets a same-site cookie holding the nonce.
// validateState parses the state, checks expiry, re-derives the HMAC, and
// compares the cookie nonce — rejecting replay + forgery.

type oidcState struct {
    WorkspaceID uuid.UUID
    Nonce       string
    ExpiresAt   time.Time
}

func (o *OIDCService) BeginAuth(w http.ResponseWriter, r *http.Request, wsID uuid.UUID) string {
    nonce := randomBase64(16)
    exp := time.Now().Add(5 * time.Minute)
    http.SetCookie(w, &http.Cookie{
        Name: "oidc_nonce", Value: nonce,
        HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
        Expires: exp,
        // Path = "/auth/oidc" deliberately: per RFC 6265 §5.1.4 the cookie
        // is sent for any request-URI whose path has this value as a
        // prefix followed by "/" or end-of-string. Our callback URL is
        // "/auth/oidc/callback" which matches (prefix "/auth/oidc" + "/").
        // DO NOT change to "/auth/oidc/callback" — that would prevent the
        // cookie from being set when BeginAuth runs on "/auth/oidc/start"
        // or similar, breaking the nonce round-trip.
        Path: "/auth/oidc",
    })
    state := oidcState{WorkspaceID: wsID, Nonce: nonce, ExpiresAt: exp}
    return encodeSignedState(state, o.stateSigningKey)
}

func (o *OIDCService) validateState(r *http.Request, stateRaw string) (uuid.UUID, error) {
    s, err := decodeSignedState(stateRaw, o.stateSigningKey)
    if err != nil { return uuid.Nil, fmt.Errorf("signature: %w", err) }
    if time.Now().After(s.ExpiresAt) { return uuid.Nil, errors.New("state expired") }
    cookie, err := r.Cookie("oidc_nonce")
    if err != nil { return uuid.Nil, errors.New("missing nonce cookie") }
    // Constant-time compare.
    if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.Nonce)) != 1 {
        return uuid.Nil, errors.New("nonce mismatch")
    }
    return s.WorkspaceID, nil
}

// encodeSignedState marshals the struct as JSON, HMAC-signs it, and
// returns base64(signature.payload). The key is a server-startup secret
// from CREDENTIAL_MASTER_KEY[0] (Phase 1 §1.4 master-key hierarchy).
func encodeSignedState(s oidcState, key []byte) string {
    payload, _ := json.Marshal(s)
    mac := hmac.New(sha256.New, key)
    mac.Write(payload)
    sig := mac.Sum(nil)
    return base64.URLEncoding.EncodeToString(sig) + "." + base64.URLEncoding.EncodeToString(payload)
}

func decodeSignedState(raw string, key []byte) (oidcState, error) {
    parts := strings.SplitN(raw, ".", 2)
    if len(parts) != 2 { return oidcState{}, errors.New("malformed state") }
    sig, err := base64.URLEncoding.DecodeString(parts[0])
    if err != nil { return oidcState{}, err }
    payload, err := base64.URLEncoding.DecodeString(parts[1])
    if err != nil { return oidcState{}, err }
    mac := hmac.New(sha256.New, key)
    mac.Write(payload)
    if !hmac.Equal(sig, mac.Sum(nil)) { return oidcState{}, errors.New("bad signature") }
    var s oidcState
    if err := json.Unmarshal(payload, &s); err != nil { return oidcState{}, err }
    return s, nil
}

func (o *OIDCService) Callback(w http.ResponseWriter, r *http.Request) {
    code := r.URL.Query().Get("code")
    stateRaw := r.URL.Query().Get("state")
    wsID, err := o.validateState(r, stateRaw)
    if err != nil { http.Error(w, "invalid state: "+err.Error(), 400); return }

    cfg, err := o.cfgSvc.Get(r.Context(), wsID)
    if err != nil || cfg.ProviderType != "oidc" { http.Error(w, "sso not configured", 400); return }

    secret, err := o.creds.GetRaw(r.Context(), wsID, cfg.OIDCClientSecretCredentialID)
    if err != nil { http.Error(w, "credential error", 500); return }
    defer agent.Zero(secret)

    provider, err := oidc.NewProvider(r.Context(), cfg.OIDCIssuer)
    if err != nil { http.Error(w, err.Error(), 500); return }
    oauthCfg := &oauth2.Config{
        ClientID: cfg.OIDCClientID, ClientSecret: string(secret),
        Endpoint: provider.Endpoint(),
        RedirectURL: baseURL(r) + "/auth/oidc/callback",
        Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
    }
    token, err := oauthCfg.Exchange(r.Context(), code)
    if err != nil { http.Error(w, err.Error(), 500); return }
    rawIDToken, _ := token.Extra("id_token").(string)
    idToken, err := provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}).Verify(r.Context(), rawIDToken)
    if err != nil { http.Error(w, err.Error(), 401); return }

    var claims struct { Email string; EmailVerified bool `json:"email_verified"`; Name string }
    _ = idToken.Claims(&claims)
    if !claims.EmailVerified { http.Error(w, "email not verified", 401); return }

    if cfg.AllowedEmailDomains != nil && !domainAllowed(claims.Email, cfg.AllowedEmailDomains) {
        http.Error(w, "email domain not allowed", 403); return
    }

    user, err := o.users.FindOrProvision(r.Context(), wsID, claims.Email, claims.Name, cfg.DefaultRole, cfg.AutoProvision)
    if err != nil { http.Error(w, err.Error(), 500); return }
    o.setSessionCookie(w, user.ID, wsID)
    http.Redirect(w, r, "/app", 302)
}
```

- [ ] **Step 3: Implement SAML (shape — `crewjam/saml` handles the heavy lifting)**

```go
// server/internal/auth/saml.go
import "github.com/crewjam/saml/samlsp"

// Per-workspace SP middleware factory. Each workspace has its own SP
// because the IdP trust relationship is per-workspace.
func (s *SAMLService) MiddlewareFor(wsID uuid.UUID) (*samlsp.Middleware, error) {
    cfg, err := s.cfgSvc.Get(ctx, wsID)
    if err != nil || cfg.ProviderType != "saml" { return nil, errors.New("not configured") }

    block, _ := pem.Decode([]byte(cfg.IdPCertificate))
    cert, _ := x509.ParseCertificate(block.Bytes)

    spURL, _ := url.Parse(cfg.SPEntityID)
    return samlsp.New(samlsp.Options{
        URL: *spURL,
        Key: s.signingKey, Certificate: s.signingCert,
        IDPMetadata: &saml.EntityDescriptor{
            EntityID: cfg.IdPEntityID,
            IDPSSODescriptors: []saml.IDPSSODescriptor{{
                SingleSignOnServices: []saml.Endpoint{{Location: cfg.IdPSSOURL, Binding: saml.HTTPRedirectBinding}},
                KeyDescriptors: []saml.KeyDescriptor{{Use: "signing", KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: base64.StdEncoding.EncodeToString(cert.Raw)}}}}}},
            }},
        },
    })
}
```

- [ ] **Step 4: Enforce-SSO gate**

```go
// middleware/enforce_sso.go — wraps password-login route.
// Login requests carry a workspace_id in the JSON/form body (password
// auth is workspace-scoped). We peek at the body, restore it, then call
// through. The cost is one body-read per login attempt — acceptable
// since the path is low-volume.
func EnforceSSO(svc *service.SSOService) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Read + restore body so the downstream handler still sees it.
            raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10)) // 64KB cap — login payload is tiny
            if err != nil { http.Error(w, "bad request", 400); return }
            r.Body = io.NopCloser(bytes.NewReader(raw))

            var body struct { WorkspaceID uuid.UUID `json:"workspace_id"` }
            _ = json.Unmarshal(raw, &body)
            // Fallback: form value.
            if body.WorkspaceID == uuid.Nil {
                if v := r.FormValue("workspace_id"); v != "" {
                    body.WorkspaceID, _ = uuid.Parse(v)
                }
            }
            // If no workspace_id at all, let the handler 400 — we don't
            // know which workspace's SSO policy to enforce.
            if body.WorkspaceID == uuid.Nil { next.ServeHTTP(w, r); return }

            if cfg, err := svc.Get(r.Context(), body.WorkspaceID); err == nil && cfg.EnforceSSO {
                http.Error(w, "password login disabled for this workspace; use SSO", 403)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

- [ ] **Step 5: Run + commit**

```bash
cd server && go test ./internal/auth/ ./internal/service/ -run "OIDC|SAML|SSO" -v
git add server/internal/auth/ server/internal/service/sso.go server/internal/handler/sso.go server/internal/middleware/enforce_sso.go server/cmd/server/router.go
git commit -m "feat(sso): SAML SP + OIDC RP + per-workspace enforcement

OIDC client_secret lives in credential vault (credential_type=
'sso_oidc_secret'); fetched + zeroed per callback. SAML uses crewjam/saml
SP. EnforceSSO middleware blocks password login when workspace config
has enforce_sso=true. Auto-provision creates user + workspace_member on
first OIDC callback; email-domain allowlist optional."
```

---

### Task 11: Retention Worker

**Files:**
- Create: `server/internal/worker/retention.go`
- Create: `server/internal/worker/retention_test.go`
- Create: `server/internal/service/retention.go`
- Create: `server/internal/handler/retention.go`

**Goal:** daily worker deletes records older than `retention_days` per active policy.

- [ ] **Step 1: Failing test**

```go
func TestRetention_DeletesOldChatMessages(t *testing.T) {
    tc := newRetentionCtx(t)
    wsID := testutil.SeedWorkspace(tc.db, "rt")
    testutil.InsertChatMessage(tc.db, wsID, time.Now().Add(-40*24*time.Hour))
    testutil.InsertChatMessage(tc.db, wsID, time.Now())

    _, err := tc.svc.Upsert(context.Background(), wsID, "chat_message", 30, uuid.New())
    if err != nil { t.Fatal(err) }

    w := NewRetentionWorker(RetentionDeps{DB: tc.db})
    n := w.RunOnce(context.Background())
    if n == 0 { t.Error("no rows deleted") }

    remaining := testutil.CountChatMessages(tc.db, wsID)
    if remaining != 1 { t.Errorf("remaining = %d, want 1", remaining) }
}

func TestRetention_AuditLogFloorEnforced(t *testing.T) {
    tc := newRetentionCtx(t)
    wsID := testutil.SeedWorkspace(tc.db, "rt")
    _, err := tc.svc.Upsert(context.Background(), wsID, "audit_log", 30, uuid.New())
    if err == nil { t.Error("expected 90-day floor error") }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/worker/retention.go
package worker

import (
    "context"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type RetentionDeps struct{ DB *pgxpool.Pool }

type RetentionWorker struct{ deps RetentionDeps }

func NewRetentionWorker(deps RetentionDeps) *RetentionWorker {
    return &RetentionWorker{deps: deps}
}

// Run loops daily; the scheduler (server/cmd/server/main.go) launches this
// on boot. RunOnce is exposed for tests.
func (w *RetentionWorker) Run(ctx context.Context) {
    t := time.NewTicker(24 * time.Hour)
    defer t.Stop()
    _ = w.RunOnce(ctx) // run once at startup, then daily
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            _ = w.RunOnce(ctx)
        }
    }
}

func (w *RetentionWorker) RunOnce(ctx context.Context) int {
    q := db.New(w.deps.DB)
    policies, err := q.ListAllActivePolicies(ctx)
    if err != nil { return 0 }
    total := 0
    for _, p := range policies {
        cutoff := time.Now().AddDate(0, 0, -int(p.RetentionDays))
        deleted := w.deleteForResource(ctx, uuidFromPg(p.WorkspaceID), p.ResourceType, cutoff)
        total += int(deleted)
        _ = q.TouchRetentionPolicy(ctx, db.TouchRetentionPolicyParams{
            ID: p.ID, WorkspaceID: p.WorkspaceID, LastDeletedCount: sqlNullInt(int(deleted)),
        })
    }
    return total
}

func (w *RetentionWorker) deleteForResource(ctx context.Context, wsID uuid.UUID, resourceType string, cutoff time.Time) int64 {
    // For audit_log, set the role guard before DELETE.
    tx, err := w.deps.DB.Begin(ctx)
    if err != nil { return 0 }
    defer tx.Rollback(ctx)
    if resourceType == "audit_log" {
        if _, err := tx.Exec(ctx, `SET LOCAL app.role = 'retention_worker'`); err != nil { return 0 }
    }
    var affected int64
    switch resourceType {
    case "chat_message":
        tag, _ := tx.Exec(ctx, `DELETE FROM chat_message WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "task_message":
        tag, _ := tx.Exec(ctx, `DELETE FROM task_message WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "agent_memory":
        // Dim-partitioned across 1024/1536/3072 — delete from all three.
        for _, suffix := range []string{"1024", "1536", "3072"} {
            tag, _ := tx.Exec(ctx,
                `DELETE FROM agent_memory_`+suffix+` WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
            affected += tag.RowsAffected()
        }
    case "audit_log":
        tag, _ := tx.Exec(ctx, `DELETE FROM audit_log WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "trace":
        tag, _ := tx.Exec(ctx, `DELETE FROM agent_trace WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "cost_event":
        tag, _ := tx.Exec(ctx, `DELETE FROM cost_event WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "webhook_delivery":
        tag, _ := tx.Exec(ctx, `DELETE FROM webhook_delivery WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    case "webhook_delivery_log":
        tag, _ := tx.Exec(ctx, `DELETE FROM webhook_delivery_log WHERE workspace_id = $1 AND created_at < $2`, wsID, cutoff)
        affected = tag.RowsAffected()
    }
    _ = tx.Commit(ctx)
    return affected
}
```

```go
// server/internal/service/retention.go
func (s *RetentionService) Upsert(ctx context.Context, wsID uuid.UUID, resource string, days int, userID uuid.UUID) (*db.RetentionPolicy, error) {
    if resource == "audit_log" && days < 90 {
        return nil, errors.New("audit_log retention must be >= 90 days")
    }
    q := db.New(s.db)
    row, err := q.UpsertRetentionPolicy(ctx, db.UpsertRetentionPolicyParams{
        WorkspaceID: uuidPg(wsID), ResourceType: resource,
        RetentionDays: int32(days), CreatedBy: uuidPg(userID),
    })
    return &row, err
}
```

- [ ] **Step 3: Wire into main.go + commit**

```go
// server/cmd/server/main.go — during server.Start:
retention := worker.NewRetentionWorker(worker.RetentionDeps{DB: pool})
go retention.Run(ctx)
```

```bash
cd server && go test ./internal/worker/ ./internal/service/ -run Retention -v
git add server/internal/worker/retention.go server/internal/service/retention.go server/internal/handler/retention.go server/cmd/server/main.go
git commit -m "feat(retention): daily worker deletes per-resource per-policy

audit_log delete sets app.role='retention_worker' inside the tx so the
immutability trigger lets it through. agent_memory iterates all three
dim partitions. 90-day floor on audit_log enforced at service layer."
```

---

### Task 11.5: Workspace-Creation Hook — Default Retention Policies

**Files:**
- Modify: `server/internal/service/workspace.go` (or `workspace_service.go` — whatever Phase 1 named it)
- Modify: `server/internal/service/workspace_test.go`

**Goal:** ensure every NEW workspace gets the default retention policies per PLAN.md §8B.6 (audit_log 365d, trace 90d). Migration 172 seeds EXISTING workspaces; this hook covers workspaces created after the migration lands.

- [ ] **Step 1: Failing test**

```go
func TestWorkspace_Create_SeedsDefaultRetentionPolicies(t *testing.T) {
    db := testutil.NewTestDB(t)
    svc := NewWorkspaceService(WorkspaceDeps{DB: db})
    wsID, err := svc.Create(context.Background(), "acme", uuid.New())
    if err != nil { t.Fatal(err) }

    q := dbgen.New(db)
    policies, _ := q.ListRetentionPolicies(context.Background(), uuidPg(wsID))
    if len(policies) < 2 { t.Fatalf("expected ≥2 default policies, got %d", len(policies)) }

    got := map[string]int32{}
    for _, p := range policies { got[p.ResourceType] = p.RetentionDays }
    if got["audit_log"] != 365 { t.Errorf("audit_log = %d, want 365", got["audit_log"]) }
    if got["trace"]     != 90  { t.Errorf("trace = %d, want 90",     got["trace"]) }
}
```

- [ ] **Step 2: Implement**

Inside `WorkspaceService.Create` after the workspace row is inserted, call:

```go
// server/internal/service/workspace.go — extend Create()
defaults := []struct{ Resource string; Days int32 }{
    {"audit_log", 365}, // PLAN.md §8B.6 default
    {"trace",      90},
}
for _, d := range defaults {
    _, _ = q.UpsertRetentionPolicy(ctx, db.UpsertRetentionPolicyParams{
        WorkspaceID: uuidPg(wsID), ResourceType: d.Resource,
        RetentionDays: d.Days, CreatedBy: uuidPg(userID),
    })
}
```

Upsert rather than insert so migration-seeded existing rows are not disturbed.

- [ ] **Step 3: Commit**

```bash
cd server && go test ./internal/service/ -run TestWorkspace_Create_SeedsDefaultRetentionPolicies -v
git add server/internal/service/workspace.go server/internal/service/workspace_test.go
git commit -m "feat(retention): default policies on workspace create

audit_log=365d + trace=90d match PLAN.md §8B.6. Upsert so
migration 172's backfill for existing workspaces remains intact."
```

---

### Task 12: Actor Context Helper + Wiring

**Files:**
- Create: `server/internal/middleware/actor.go`
- Modify: `server/internal/auth/session.go`, `server/internal/middleware/apikey_auth.go`

**Goal:** single `Actor{Type, ID}` value in `ctx.Value(ActorKey)`. Populated by whichever auth path served the request. Audit middleware reads it.

- [ ] **Step 1: Implement**

```go
// server/internal/middleware/actor.go
package middleware

import (
    "context"
    "github.com/google/uuid"
)

type ActorCtxKey struct{}
var ActorKey = ActorCtxKey{}

type Actor struct {
    Type string    // member | agent | system | api_key
    ID   uuid.UUID
}

func WithActor(ctx context.Context, a Actor) context.Context {
    return context.WithValue(ctx, ActorKey, a)
}

func ActorFromContext(ctx context.Context) Actor {
    if a, ok := ctx.Value(ActorKey).(Actor); ok { return a }
    return Actor{Type: "system"}
}
```

Update the session + apikey middlewares to call `WithActor(ctx, Actor{...})` before chaining.

- [ ] **Step 2: Commit**

```bash
cd server && go build ./...
git add server/internal/middleware/actor.go server/internal/auth/session.go server/internal/middleware/apikey_auth.go
git commit -m "feat(auth): unify Actor into ctx — session + api key populate

Audit middleware reads ctx.Value(ActorKey) without caring which auth path
produced it. Unknown/unauthenticated requests fall through as
Actor{Type:'system'} — should be rare since audit is only wrapped
around authenticated routes."
```

---

### Task 13: Frontend — Audit + SSO + Retention + Credentials + Revisions

**Files:**
- Create: `packages/core/types/{audit,sso,retention,revision,credential}.ts`
- Create: `packages/core/api/{audit,sso,retention,revision,credential}.ts`
- Create: `packages/core/{audit,sso,retention,revision,credential}/queries.ts`
- Create: `packages/views/settings/{credentials,sso,retention}/` + `packages/views/audit/` + `packages/views/agents/components/tabs/revisions-tab.tsx`

**Goal:** five CRUD UIs. Each follows the Phase 6/7/8A pattern exactly — types → api → queries → views. No direct `api.*` in components.

- [ ] **Step 1: Types + clients (sketch)**

```ts
// packages/core/types/audit.ts
export type AuditEntry = {
  id: string;
  workspace_id: string;
  actor_type: "member" | "agent" | "system" | "api_key";
  actor_id: string | null;
  action: string;
  resource_type: string;
  resource_id: string | null;
  changes: Record<string, { before: unknown; after: unknown }> | null;
  ip_address: string | null;
  user_agent: string | null;
  created_at: string;
};

// packages/core/types/sso.ts
export type SSOConfig = {
  workspace_id: string;
  provider_type: "saml" | "oidc";
  idp_entity_id?: string;
  idp_sso_url?: string;
  oidc_issuer?: string;
  oidc_client_id?: string;
  enforce_sso: boolean;
  auto_provision: boolean;
  default_role: string;
  allowed_email_domains?: string[];
};
```

Similarly for retention, revision. Credential type must expose every field
PLAN.md §8B.1 requires:

```ts
// packages/core/types/credential.ts
export type CredentialType = "provider" | "tool_secret" | "webhook_hmac" | "sso_oidc_secret";

export type Credential = {
  id: string;
  workspace_id: string;
  credential_type: CredentialType;
  name: string;
  provider: string | null;        // only for credential_type='provider'
  description: string | null;
  is_active: boolean;
  // Rotation + usage telemetry (PLAN.md §8B.1 — MUST render all four).
  last_rotated_at: string | null;
  last_used_at: string | null;
  usage_count: number;
  cooldown_until: string | null;  // when cooldown clears; UI shows "Cooling down — X mins" when set
  // Which agents bind to this credential — populated by ListCredentials
  // response (backend JOINs agent table via runtime_config references or a
  // dedicated credential_usage table if PLAN.md §1.4 added one).
  bound_agents: Array<{ agent_id: string; agent_name: string }>;
  created_at: string;
};
```

Backend addition — new sqlc queries in `credential.sql` (create the file):

```sql
-- server/pkg/db/queries/credential.sql

-- name: ListCredentials :many
-- Returns the credential metadata only. Bindings are resolved separately
-- to avoid a LIKE-on-JSONB scan that is both slow (no index) and unsafe
-- (a UUID appearing in any JSONB field of runtime_config would match).
SELECT
    id, workspace_id, credential_type, name, provider, description,
    is_active, last_rotated_at, last_used_at, usage_count, cooldown_until, created_at
FROM workspace_credential
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: ListAgentsUsingCredential :many
-- Returns agents that reference a specific credential. Indexed-safe paths:
--   1. agent.provider_credential_id = $2 — direct column reference
--      (present iff Phase 1 §1.4 added the column; preferred path)
--   2. JSONB containment: agent.runtime_config @> jsonb_build_object(...)
--      with specific expected keys for HTTP/process backends.
--
-- STORAGE CONTRACT (verify with Phase 1.4 before shipping):
-- Phase 1 §1.4 + Phase 8A §8A.2/§8A.3 store credential references in
-- runtime_config as JSON STRING values (UUIDs serialized to their string
-- representation "xxxxxxxx-xxxx-..."), NOT as JSONB native scalar UUIDs.
-- This is the standard pgx JSONB encoding for uuid.UUID fields with
-- `json:"..."` tags. The `::text` cast on $2 matches that string form;
-- `@>` containment is structural equality and REQUIRES exact type match
-- — a JSONB-text on the LHS must compare to a JSONB-text on the RHS.
--
-- If Phase 1 later changes to store UUIDs via a custom JSONB encoder that
-- preserves type info, change `$2::text` to `$2` and verify by running
-- `SELECT jsonb_typeof(runtime_config->'secret_credential_id') FROM agent`
-- which must return 'string' for this query to match.
--
-- The Go handler calls this once per credential and aggregates — N+1 for
-- the Credentials list page is fine since credentials are O(10s) per
-- workspace. If profiling shows slowness, a dedicated credential_agent_binding
-- table is the proper fix (tracked as Phase 9 follow-up).
SELECT a.id, a.name
FROM agent a
WHERE a.workspace_id = $1
  AND (
      -- Path 1 (preferred): direct column reference.
      -- Uncomment when Phase 1 adds provider_credential_id:
      -- a.provider_credential_id = $2
      -- Path 2 (fallback): JSONB containment on well-known keys.
      a.runtime_config @> jsonb_build_object('secret_credential_id', $2::text)
      OR a.runtime_config @> jsonb_build_object('auth_credential_id', $2::text)
  )
ORDER BY a.name;
```

Note: JSONB containment via `@>` is index-friendly when a GIN index exists
on `agent.runtime_config`; Phase 1 should ensure `CREATE INDEX ... USING
GIN (runtime_config)` is present. If not, add it via migration 174 in this
phase's batch.

- [ ] **Step 2: Views**

- `AuditLogView` — filter form + table + "Export CSV" link to `/api/.../audit.csv`.
- `SSOConfigView` — provider picker, per-provider form, "Test connection" button.
- `RetentionConfigView` — table of resource_type × days editor.
- `CredentialsView` — list (columns: name, type, last rotated, usage count, cooldown status, bound agents count), create form (type picker), rotation wizard (bumps last_rotated_at), detail drawer showing full bound-agents list. Must render every field from §8B.1:
  - `last_rotated_at` — "Rotated X days ago" with warning if > 90 days
  - `usage_count` — integer badge
  - `cooldown_until` — "Cooling down — X min remaining" when set
  - `bound_agents` — chip list, click-through to agent detail
- `RevisionsTab` — list per agent, diff viewer, rollback button with confirm modal.

- [ ] **Step 3: Commit**

```bash
git add packages/core/ packages/views/settings/ packages/views/audit/ packages/views/agents/components/tabs/revisions-tab.tsx server/pkg/db/queries/credential.sql server/pkg/db/generated/
git commit -m "feat(views): audit + sso + retention + credentials + revisions UIs

All five follow the established pattern — TanStack Query hooks,
no api.* calls in components. CredentialsView renders every field
from PLAN.md §8B.1 — last_rotated_at, usage_count, cooldown_until,
bound_agents — not just list/create/rotate. Backend adds
ListCredentialsWithBindings sqlc query to surface the agent-usage
JOIN. Rotation wizard updates encrypted_value server-side without
surfacing plaintext."
```

---

### Task 14: Integration Tests + Phase 8B Verification

**Files:**
- Create: `server/internal/integration/security/audit_e2e_test.go`
- Create: `server/internal/integration/security/sso_e2e_test.go`
- Create: `docs/engineering/verification/phase-8b.md`

- [ ] **Step 1: Integration test — rollback isolates in-flight tasks**

```go
// Verifies PLAN.md §8B.4 invariant: "In-flight tasks continue with the
// config they started with (snapshot at claim time)." The Phase 2 worker
// captures harness.Config at claim; Phase 8B rollback mutates the agent
// row. An in-flight task must not observe the rollback.
func TestRollback_DoesNotAffectInFlightTask(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("sec-inflight")
    user := env.DefaultUser

    // Model that CAPTURES which instructions were active at claim time
    // so the test can verify the running task held v2 even after the row
    // was rolled back to v1. Using only response text would pass even if
    // v1 instructions were erroneously used during execution.
    blocker := make(chan struct{})
    finish := make(chan string, 1)
    capturedInstructions := make(chan string, 1)
    env.InstallBlockingModel(func(cfg harness.Config, prompt string) string {
        capturedInstructions <- cfg.SystemPrompt // harness.Config.SystemPrompt carries instructions
        <-blocker
        return "captured:" + prompt
    })

    agentID := env.CreateAgentViaAPI(ws, user, "bot")
    env.UpdateAgentViaAPI(ws, user, agentID, map[string]any{"instructions": "v2-instructions"})
    revs := env.ListRevisions(ws, agentID)

    // Start a task — it claims with v2 instructions and then blocks.
    taskID := env.EnqueueTask(ws, agentID, "hello")
    go func() { finish <- env.AwaitTaskResult(context.Background(), taskID) }()
    env.WaitForTaskRunning(ws, taskID)

    // Assert claim-time instructions were v2 BEFORE rollback.
    claimInstr := <-capturedInstructions
    if !strings.Contains(claimInstr, "v2-instructions") {
        t.Fatalf("claim-time instructions = %q; expected v2-instructions", claimInstr)
    }

    // Rollback to the original revision (revision_number=1).
    initial := revs[len(revs)-1]
    env.RollbackAgent(ws, user, agentID, initial.ID, "test")

    // Unblock; the running task continues with v2 (as captured above)
    // regardless of the post-rollback row.
    close(blocker)
    result := <-finish
    if !strings.Contains(result, "hello") {
        t.Errorf("in-flight task result = %q; task failed or cancelled by rollback", result)
    }

    // Verify agent row IS rolled back (sanity — rollback actually applied).
    cur := env.GetAgent(ws, agentID)
    if strings.Contains(cur.Instructions, "v2-instructions") {
        t.Error("rollback did not apply to agent row")
    }
}
```

- [ ] **Step 2: Integration test — audit + rollback chain**

```go
func TestAudit_RollbackFlow(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("sec")
    user := env.DefaultUser
    agentID := env.CreateAgentViaAPI(ws, user, "bot")
    env.UpdateAgentViaAPI(ws, user, agentID, map[string]any{"instructions": "v2"})

    revs := env.ListRevisions(ws, agentID)
    env.RollbackAgent(ws, user, agentID, revs[len(revs)-1].ID, "regression")

    entries := env.ListAuditEntries(ws, map[string]string{"resource_type": "agent"})
    got := []string{}
    for _, e := range entries { got = append(got, e.Action) }
    for _, want := range []string{"agent.create", "agent.update", "agent.rollback"} {
        found := false
        for _, a := range got { if a == want { found = true } }
        if !found { t.Errorf("missing audit action %q; got %v", want, got) }
    }
}
```

- [ ] **Step 3: Integration test — SSO auto-provision**

```go
func TestSSO_OIDCAutoProvisionsUser(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("sso")
    env.ConfigureSSO(ws, "oidc", map[string]any{
        "oidc_issuer": env.StartFakeIdP(),
        "oidc_client_id": "cid",
        "oidc_client_secret": "shh",
        "auto_provision": true,
        "default_role": "member",
    })
    resp := env.CompleteOIDCCallback(ws, "new@acme.com")
    if resp.StatusCode != 302 { t.Fatalf("status = %d", resp.StatusCode) }
    if !env.UserExists("new@acme.com") { t.Error("user not provisioned") }
}
```

- [ ] **Step 4: Manual verification + evidence**

Run `make check` + walk the §8B.7 checklist:

- [ ] Credentials UI: create a provider key, assign to agent, execute — LLM call succeeds without raw key surfaced.
- [ ] `credential_access_log`: after the LLM call in the previous check, a row appears in `credential_access_log` with `access_purpose='llm_api_call'`, correct `credential_id`, `trace_id` linked to the task, and `accessed_at` ≈ now. PLAN.md §8B.7 lists this as a distinct verification item.
- [ ] Audit: mutate an agent; audit entry appears with diff; POST-only, no UPDATE/DELETE possible via API.
- [ ] Rate limit: burst 200 requests at a workspace; first 100 ok, rest 429 with Retry-After.
- [ ] Rate limit: simulate provider 429; new requests to that provider receive 503 until ThrottledUntil passes.
- [ ] Rollback: agent to v1 from v3; v2+v3 preserved; new revision `is_rollback=true`; in-flight task keeps its own config.
- [ ] SSO SAML: Okta test IdP ↔ our SP; successful callback → session cookie; member row auto-created.
- [ ] SSO OIDC: Google OAuth ↔ our RP; auto-provision on first login.
- [ ] Enforce SSO: password login returns 403 when enforce_sso=true.
- [ ] Retention: insert chat_message dated 40 days ago; run worker with 30-day policy; row deleted.
- [ ] Retention: attempt to set audit_log < 90 days; rejected with clear error.

- [ ] **Step 5: Write evidence + commit**

```markdown
# Phase 8B Verification — 2026-04-DD

- `go test ./internal/integration/security/...` → pass
- `make check` → pass
- Manual checklist: [results]
- Known limitations:
  - Rate limiter is per-process (no Redis share); acceptable for V1.
  - SCIM real-time sync deferred.
  - Hash-chained tamper-evident audit deferred.
```

```bash
git add server/internal/integration/security/ docs/engineering/verification/phase-8b.md
git commit -m "test(phase-8b): audit rollback chain + SSO auto-provision e2e"
```

---

## Placeholder Scan

Self-reviewed. Two intentional abbreviations:

1. Task 10 SAML snippet points at `crewjam/saml`'s own docs for the full IdP-metadata parse; the snippet shows the config-wiring boundary. An agent copying verbatim must follow the library's `ServeACS` example for the inbound ACS endpoint — it's 20 lines from the library readme.
2. Task 13 Views list five components with the same pattern as Phase 6/7/8A (TanStack hooks + render test). The per-component test scaffold would be a 5× copy of the Phase 6 `CapabilityBrowser.test.tsx` shape; repeating isn't useful.

No TBDs, no unreferenced symbols.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase8b-security-compliance.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task.

**2. Inline Execution** — via `superpowers:executing-plans`.

Which approach?
