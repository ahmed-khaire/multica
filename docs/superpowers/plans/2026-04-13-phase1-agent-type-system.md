# Phase 1: Agent Type System & Database Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple Multica's data model from "coding agent" assumptions so the platform can support business agents (LLM API, HTTP, process) alongside existing coding agents. Also establishes the agent-execution foundation by wiring in the `digitallysavvy/go-ai` library (soft-forked) for Phase 2.

**Architecture:** Add `agent_type` column to distinguish agent workload classes. Non-coding agents skip the runtime requirement and instead specify a provider/model directly. New tables for capabilities, cost tracking, budget policies, credentials, and embedding config lay the foundation for Phases 2-12. The error classification system provides centralized recovery logic for all future execution backends. The `go-ai` library provides the agent harness (tool loop, subagents, skills, multi-provider model abstraction) that Phase 2 builds on.

**Tech Stack:** Go (Chi, sqlc, pgx/v5, `github.com/digitallysavvy/go-ai` v0.4.0), PostgreSQL (pgvector), TypeScript (React, TanStack Query, Zustand, Tailwind, shadcn/Base UI)

---

## Scope Note

This is Phase 1 of a 12-phase plan. The full spec lives in `PLAN.md` at the repo root. Each phase has its own implementation plan. Phase 1 is the foundation — everything else depends on it.

## Phase 1 Guardrails

Phase 1 intentionally stops short of executing non-coding agents.

- `llm_api`, `http`, and `process` agents are create/view/manage only in this phase.
- They must remain unassignable, not mention-triggerable, and not available for chat sessions until Phase 2 adds the worker/router path.
- Existing daemon and runtime-based task claiming stay unchanged in Phase 1. Any provider-based query added now is preparatory only and must not be wired into claim paths yet.
- For `http` and `process`, `provider` identifies the backend family for later routing. The actual executable config lives in `runtime_config` and must be validated before save.

Phase 2 activation requirements:

- `server/internal/handler/issue.go` must explicitly reopen assignment for runnable non-coding agents once provider-based claiming is live.
- `server/internal/handler/chat.go` must explicitly reopen chat creation for runnable non-coding agents once the worker path can service chat tasks.
- Mention-trigger behavior can remain runtime-gated until a later phase intentionally broadens `on_mention` to server-side workers.

## File Structure

### New Files (Backend)
| File | Responsibility |
|------|---------------|
| `server/migrations/100_agent_types.up.sql` | Agent type columns, capability table |
| `server/migrations/100_agent_types.down.sql` | Rollback agent type changes |
| `server/migrations/101_cost_tracking.up.sql` | cost_event, budget_policy tables |
| `server/migrations/101_cost_tracking.down.sql` | Rollback cost tracking |
| `server/migrations/102_credentials.up.sql` | workspace_credential, credential_access_log |
| `server/migrations/102_credentials.down.sql` | Rollback credentials |
| `server/migrations/103_embedding_config.up.sql` | workspace_embedding_config |
| `server/migrations/103_embedding_config.down.sql` | Rollback embedding config |
| `server/pkg/db/queries/capability.sql` | SQLC queries for agent_capability |
| `server/pkg/db/queries/cost.sql` | SQLC queries for cost_event, budget_policy |
| `server/pkg/db/queries/credential.sql` | SQLC queries for workspace_credential |
| `server/pkg/db/queries/embedding.sql` | SQLC queries for workspace_embedding_config |
| `server/pkg/agent/errors.go` | Error classification system |
| `server/pkg/agent/errors_test.go` | Tests for error classifier |
| `server/pkg/agent/credentials.go` | Credential pool with rotation |
| `server/pkg/agent/credentials_test.go` | Tests for credential pool |
| `server/pkg/embeddings/embeddings.go` | Embedder interface + provider stubs |
| `server/pkg/embeddings/embeddings_test.go` | Tests for embedder |
| `server/internal/handler/capability.go` | HTTP handlers for capability CRUD |
| `server/internal/handler/cost.go` | HTTP handlers for cost/budget endpoints |
| `server/internal/handler/chat.go` | Reject non-coding agents for chat in Phase 1 |
| `server/internal/handler/issue.go` | Reject assigning non-coding agents in Phase 1 |
| `docs/engineering/go-ai-fork-policy.md` | Runbook for when/how to activate the fork via `replace` directive |
| `docs/engineering/go-ai-fork-activations.md` | Append-only log of every fork activation |

### Modified Files (Backend)
| File | Changes |
|------|---------|
| `server/pkg/db/queries/agent.sql` | Update CreateAgent, UpdateAgent, ClaimAgentTask for agent_type |
| `server/internal/handler/agent.go` | Accept agent_type/provider/model in Create/Update, response includes new fields |
| `server/cmd/server/router.go` | Register new capability/cost/budget routes |
| `server/go.mod` + `server/go.sum` | Add `github.com/digitallysavvy/go-ai v0.4.0` dependency |

### External Infrastructure
| Repo | Change |
|------|--------|
| `ahmed-khaire/go-ai` | Fork of `digitallysavvy/go-ai`. `main` branch mirrors upstream. `.github/workflows/mirror-upstream.yml` fast-forwards weekly. Dormant by default — activated only per fork policy. |

### Modified Files (Frontend)
| File | Changes |
|------|---------|
| `packages/core/types/agent.ts` | Add agent_type, provider, model, capabilities |
| `packages/core/api/client.ts` | Add capability/cost/budget API methods |
| `packages/views/agents/components/create-agent-dialog.tsx` | Multi-step type-aware creation flow |
| `packages/views/agents/components/agent-detail.tsx` | Show provider/model for non-coding agents, capabilities tab |
| `packages/views/agents/config.ts` | Agent type display config |

---

### Task 1: Migration 100 — Agent Type Columns + Capability Table

**Files:**
- Create: `server/migrations/100_agent_types.up.sql`
- Create: `server/migrations/100_agent_types.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 100_agent_types.up.sql
-- Phase 1: Introduce agent_type to distinguish coding vs business agents.
-- All existing agents default to 'coding' — fully backwards compatible.
--
-- NOTE: every statement uses IF NOT EXISTS / DROP IF EXISTS per PLAN.md §1.7.
-- Every CHECK constraint is added via DROP IF EXISTS -> ADD NOT VALID -> VALIDATE
-- so that ADD CONSTRAINT does not take ACCESS EXCLUSIVE for a full-table scan
-- on large production tables (PLAN.md §1.1, R3 B2).

-- 1. Add agent_type column with default for existing rows (fast-path metadata-only).
ALTER TABLE agent ADD COLUMN IF NOT EXISTS agent_type TEXT NOT NULL DEFAULT 'coding';

-- 1b. Type-set CHECK: NOT VALID then VALIDATE. Only the validation pass takes
-- SHARE UPDATE EXCLUSIVE; concurrent reads/writes proceed.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

-- 2. Add direct provider/model for cloud LLM agents (no runtime binding needed).
ALTER TABLE agent ADD COLUMN IF NOT EXISTS provider TEXT;
ALTER TABLE agent ADD COLUMN IF NOT EXISTS model TEXT;

-- 3. Make runtime_id nullable for non-coding agents.
-- Existing coding agents all have runtime_id set, so no data loss.
ALTER TABLE agent ALTER COLUMN runtime_id DROP NOT NULL;

-- 4. Compound constraint: coding agents MUST have runtime, non-coding MUST have provider.
-- Added as NOT VALID then VALIDATE-d (PLAN.md §1.1, R3 B2). Phase 11/12 later mutate
-- this constraint using the same DROP -> ADD NOT VALID -> VALIDATE pattern.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
    (agent_type = 'coding' AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api', 'http', 'process') AND provider IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

-- 5. Make runtime_id nullable on task queue too (for runtimeless agent tasks).
ALTER TABLE agent_task_queue ALTER COLUMN runtime_id DROP NOT NULL;

-- 6. Composite unique index enabling composite FKs from every dependent table.
-- PLAN.md §1.1: cost_event, agent_capability, approval_request, agent_metric,
-- agent_trace, webhook_endpoint.target_id all reference (workspace_id, agent_id).
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_workspace_id ON agent(workspace_id, id);

-- 7. Agent capabilities (what the agent can do — used for discovery in A2A Phase 6).
-- Enforces workspace scoping via composite FK (workspace_id, agent_id).
CREATE TABLE IF NOT EXISTS agent_capability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    input_schema JSONB,
    output_schema JSONB,
    estimated_cost_cents INT,
    avg_duration_seconds INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE,
    UNIQUE(agent_id, name)
);

CREATE INDEX IF NOT EXISTS idx_capability_workspace ON agent_capability(workspace_id);
CREATE INDEX IF NOT EXISTS idx_capability_agent ON agent_capability(agent_id);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 100_agent_types.down.sql
DROP TABLE IF EXISTS agent_capability;
DROP INDEX IF EXISTS idx_agent_workspace_id;

-- Down migration is only safe after deleting or backfilling Phase 1 non-coding rows.
DELETE FROM agent_task_queue WHERE runtime_id IS NULL;
ALTER TABLE agent_task_queue ALTER COLUMN runtime_id SET NOT NULL;

DELETE FROM agent WHERE runtime_id IS NULL;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent DROP COLUMN IF EXISTS model;
ALTER TABLE agent DROP COLUMN IF EXISTS provider;
ALTER TABLE agent DROP COLUMN IF EXISTS agent_type;
ALTER TABLE agent ALTER COLUMN runtime_id SET NOT NULL;
```

- [ ] **Step 3: Run migration to verify it applies cleanly**

Run: `cd server && make migrate-up`
Expected: Migration 100 applies without errors.

- [ ] **Step 4: Verify rollback works**

Run: `cd server && make migrate-down` (one step)
Expected: Migration 100 rolls back cleanly on a database that does not contain Phase 1 non-coding agents/tasks. If you created any rows with `NULL runtime_id`, confirm the cleanup clauses above were exercised.

Run: `cd server && make migrate-up` (re-apply)

- [ ] **Step 5: Commit**

```bash
git add server/migrations/100_agent_types.up.sql server/migrations/100_agent_types.down.sql
git commit -m "feat(db): add agent_type column, capabilities table, nullable runtime_id

Introduces agent_type ('coding', 'llm_api', 'http', 'process') with
constraint ensuring coding agents have runtime and non-coding have provider.
Adds agent_capability table for A2A discovery (Phase 6)."
```

---

### Task 2: Migration 101 — Cost Tracking Tables

**Files:**
- Create: `server/migrations/101_cost_tracking.up.sql`
- Create: `server/migrations/101_cost_tracking.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 101_cost_tracking.up.sql
-- Cost event tracking (ported from Paperclip cost_events + Hermes usage_pricing).
--
-- PLAN.md §1.1 (D8): trace_id is NOT NULL — every cost row attributes to a
-- trace chain. Column renames from the pre-blocker schema:
--   cached_tokens → cache_read_tokens (R3 S6; Anthropic: cache_read_input_tokens)
--   input_tokens is NON-CACHED only (exclude cache reads from this count)
-- New columns: cache_ttl_minutes, session_hours, session_cost_cents, response_cache_id.
-- cost_status enum: 'adjustment' replaces the old 'test'. cost_event is APPEND-ONLY.

CREATE TABLE IF NOT EXISTS cost_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    model TEXT NOT NULL,
    input_tokens INT,                 -- NON-CACHED input tokens only (R3 S6)
    output_tokens INT,
    cache_read_tokens INT DEFAULT 0,  -- Anthropic cache_read_input_tokens (0.1x rate)
    cache_write_tokens INT DEFAULT 0, -- Anthropic cache_creation_input_tokens (1.25x @ 5m, 2x @ 1h)
    cache_ttl_minutes INT,            -- 5 or 60 — determines cache_write multiplier
    cost_cents INT NOT NULL,
    cost_status TEXT NOT NULL DEFAULT 'estimated'
        CHECK (cost_status IN ('actual', 'estimated', 'included', 'adjustment')),
    trace_id UUID NOT NULL,           -- D8 invariant: mandatory on every row
    session_hours DECIMAL(10,4),      -- Phase 12: Claude Managed Agents session billing
    session_cost_cents INT,           -- Phase 12
    response_cache_id UUID,           -- Phase 9: nil for non-cache-hit events
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite FK prevents cross-tenant agent reference (R3 S1).
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);

-- D8 aggregation index: SELECT SUM(cost_cents) WHERE trace_id=$1 — used by
-- Phase 6 delegation budget check and Phase 9 chain ceiling.
CREATE INDEX IF NOT EXISTS idx_cost_event_trace ON cost_event(trace_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_cost_event_ws_time ON cost_event(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_cost_event_agent ON cost_event(agent_id, created_at DESC);

-- Budget policies (ported from Paperclip)
CREATE TABLE IF NOT EXISTS budget_policy (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('workspace', 'agent', 'project')),
    scope_id UUID,
    monthly_limit_cents INT NOT NULL,
    warning_threshold DECIMAL NOT NULL DEFAULT 0.7,
    hard_stop BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_budget_policy_workspace ON budget_policy(workspace_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_budget_policy_scope ON budget_policy(workspace_id, scope, scope_id)
    WHERE scope_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_budget_policy_workspace_scope ON budget_policy(workspace_id, scope)
    WHERE scope = 'workspace' AND scope_id IS NULL;
```

- [ ] **Step 2: Write the down migration**

```sql
-- 101_cost_tracking.down.sql
DROP TABLE IF EXISTS budget_policy;
DROP TABLE IF EXISTS cost_event;
```

- [ ] **Step 3: Run migration**

Run: `cd server && make migrate-up`
Expected: Migration 101 applies without errors.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/101_cost_tracking.up.sql server/migrations/101_cost_tracking.down.sql
git commit -m "feat(db): add cost_event and budget_policy tables

Tracks per-call LLM costs with cache-aware pricing columns.
Budget policies enforce per-workspace/agent/project spend limits."
```

---

### Task 3: Migration 102 — Credential Vault Tables

**Files:**
- Create: `server/migrations/102_credentials.up.sql`
- Create: `server/migrations/102_credentials.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 102_credentials.up.sql
-- Unified credential vault for LLM provider keys + tool secrets + webhook HMAC + SSO OIDC secrets.
--
-- PLAN.md §1.4 crypto specification:
--   master key: CREDENTIAL_MASTER_KEY[v], 32 bytes, versioned for rotation
--   workspace DEK: HKDF-SHA256(ikm=MASTER[v], salt=workspace_id (16 bytes raw),
--                              info="multica-cred-v1", L=32)
--   row encryption: AES-256-GCM with nonce (12 random bytes, stored in `nonce`)
--   AAD: workspace_id || credential_id || name  — binds ciphertext to the row,
--        prevents cross-tenant row-swap attacks
--   encryption_version: matches master-key version; enables online rotation
--        via background re-wrap job (PLAN.md §1.4 rotation procedure)

CREATE TABLE IF NOT EXISTS workspace_credential (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    credential_type TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT,                     -- 'anthropic', 'openai', etc. (credential_type='provider' only)
    encrypted_value BYTEA NOT NULL,    -- AES-256-GCM ciphertext + 16-byte GCM tag
    nonce BYTEA NOT NULL,              -- 12 bytes, unique per (workspace_id, id)
    encryption_version SMALLINT NOT NULL DEFAULT 1,
    description TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    cooldown_until TIMESTAMPTZ,
    usage_count INT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ,
    last_rotated_at TIMESTAMPTZ,
    created_by UUID REFERENCES "user"(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(workspace_id, name),
    CONSTRAINT workspace_credential_nonce_len CHECK (octet_length(nonce) = 12)
);

-- credential_type CHECK — NOT VALID / VALIDATE pattern. Extended enum includes
-- webhook_hmac + sso_oidc_secret so Phase 8A/8B can move their plaintext secrets
-- into the vault (PLAN.md §1.4 / R3 B4).
ALTER TABLE workspace_credential DROP CONSTRAINT IF EXISTS workspace_credential_type_check;
ALTER TABLE workspace_credential ADD CONSTRAINT workspace_credential_type_check
    CHECK (credential_type IN ('provider', 'tool_secret', 'webhook_hmac', 'sso_oidc_secret')) NOT VALID;
ALTER TABLE workspace_credential VALIDATE CONSTRAINT workspace_credential_type_check;

-- Composite unique index enables composite FKs from webhook_endpoint and
-- any future consumer that needs to reference a credential by workspace.
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_credential_ws_id
    ON workspace_credential(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_credential_workspace ON workspace_credential(workspace_id, credential_type);
CREATE INDEX IF NOT EXISTS idx_credential_provider ON workspace_credential(workspace_id, provider)
    WHERE credential_type = 'provider' AND is_active = true;

CREATE TABLE IF NOT EXISTS credential_access_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    credential_id UUID NOT NULL,
    agent_id UUID REFERENCES agent(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    trace_id UUID,                     -- D8: bind credential access to trace chain
    access_purpose TEXT NOT NULL,      -- 'llm_api_call', 'tool_injection', 'webhook_verify', etc.
    accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Composite FK prevents logging an access against a credential in a different workspace.
    FOREIGN KEY (workspace_id, credential_id) REFERENCES workspace_credential(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_credential_access_log ON credential_access_log(credential_id, accessed_at DESC);
CREATE INDEX IF NOT EXISTS idx_credential_log_ws_time ON credential_access_log(workspace_id, accessed_at DESC);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 102_credentials.down.sql
DROP TABLE IF EXISTS credential_access_log;
DROP TABLE IF EXISTS workspace_credential;
```

- [ ] **Step 3: Run migration**

Run: `cd server && make migrate-up`
Expected: Migration 102 applies without errors.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/102_credentials.up.sql server/migrations/102_credentials.down.sql
git commit -m "feat(db): add workspace_credential and access log tables

Unified encrypted vault for LLM provider keys and tool secrets.
Supports rotation strategies, cooldown, and access audit logging."
```

---

### Task 4: Migration 103 — Embedding Config Table

**Files:**
- Create: `server/migrations/103_embedding_config.up.sql`
- Create: `server/migrations/103_embedding_config.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 103_embedding_config.up.sql
-- Per-workspace embedding model configuration (used by Phases 4, 5, 9).
-- PLAN.md §1.5: dimensions are a closed set — content tables are dim-partitioned.
-- Changing dimensions once chunks exist requires the documented re-embedding flow.

CREATE TABLE IF NOT EXISTS workspace_embedding_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL UNIQUE REFERENCES workspace(id) ON DELETE CASCADE,
    provider TEXT NOT NULL DEFAULT 'openai',
    model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    dimensions INT NOT NULL DEFAULT 1536
        CHECK (dimensions IN (1024, 1536, 3072)),  -- matches Phase 4/5/9 partitions
    api_credential_id UUID REFERENCES workspace_credential(id) ON DELETE SET NULL,
    endpoint_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 103_embedding_config.down.sql
DROP TABLE IF EXISTS workspace_embedding_config;
```

- [ ] **Step 3: Run migration**

Run: `cd server && make migrate-up`
Expected: Migration 103 applies without errors.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/103_embedding_config.up.sql server/migrations/103_embedding_config.down.sql
git commit -m "feat(db): add workspace_embedding_config table

Per-workspace embedding provider/model/dimensions config.
Supports OpenAI, Voyage, Ollama, and custom HTTP endpoints."
```

---

### Task 5: Regenerate SQLC + Update Agent Queries

**Files:**
- Modify: `server/pkg/db/queries/agent.sql`
- Regenerate: `server/pkg/db/generated/` (via `make sqlc`)

- [ ] **Step 1: Update CreateAgent query to accept new columns**

In `server/pkg/db/queries/agent.sql`, replace the existing `CreateAgent` query:

```sql
-- name: CreateAgent :one
INSERT INTO agent (
    workspace_id, name, description, avatar_url, runtime_mode,
    runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
    instructions, agent_type, provider, model
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;
```

- [ ] **Step 2: Update UpdateAgent query to accept new columns**

Replace the existing `UpdateAgent` query:

```sql
-- name: UpdateAgent :one
UPDATE agent SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    runtime_config = COALESCE(sqlc.narg('runtime_config'), runtime_config),
    runtime_mode = COALESCE(sqlc.narg('runtime_mode'), runtime_mode),
    runtime_id = COALESCE(sqlc.narg('runtime_id'), runtime_id),
    visibility = COALESCE(sqlc.narg('visibility'), visibility),
    status = COALESCE(sqlc.narg('status'), status),
    max_concurrent_tasks = COALESCE(sqlc.narg('max_concurrent_tasks'), max_concurrent_tasks),
    instructions = COALESCE(sqlc.narg('instructions'), instructions),
    agent_type = COALESCE(sqlc.narg('agent_type'), agent_type),
    provider = COALESCE(sqlc.narg('provider'), provider),
    model = COALESCE(sqlc.narg('model'), model),
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 3: Update CreateAgentTask to make runtime_id optional**

Replace the existing `CreateAgentTask` query:

```sql
-- name: CreateAgentTask :one
INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, trigger_comment_id)
VALUES ($1, sqlc.narg(runtime_id), $2, 'queued', $3, sqlc.narg(trigger_comment_id))
RETURNING *;
```

- [ ] **Step 4: Add query to list pending tasks by provider (for cloud worker claiming)**

Append to `server/pkg/db/queries/agent.sql`:

```sql
-- name: ListPendingTasksByProvider :many
-- Lists queued tasks for agents with a given provider (for cloud worker claiming).
-- Cloud workers claim tasks by provider capability, not runtime_id.
SELECT atq.* FROM agent_task_queue atq
JOIN agent a ON a.id = atq.agent_id
WHERE a.provider = $1 AND atq.status = 'queued'
ORDER BY atq.priority DESC, atq.created_at ASC;
```

This query is preparatory only in Phase 1. Do **not** wire it into daemon routes, `ClaimTaskForRuntime`, or task enqueue logic until Phase 2.

- [ ] **Step 5: Run sqlc to regenerate**

Run: `cd server && make sqlc`
Expected: `make sqlc` succeeds. Generated files in `server/pkg/db/generated/` updated with new Agent struct fields (`AgentType`, `Provider`, `Model`) and updated function signatures.

- [ ] **Step 6: Verify build compiles**

Run: `cd server && go build ./...`
Expected: Build fails — handler code references old CreateAgentParams (missing new required fields). This is expected; we'll fix handlers in Task 12.

- [ ] **Step 7: Commit query changes (before fixing compilation)**

```bash
git add server/pkg/db/queries/agent.sql server/pkg/db/generated/
git commit -m "feat(db): update agent queries for agent_type, provider, model

CreateAgent now accepts agent_type/provider/model.
UpdateAgent supports patching new fields.
CreateAgentTask accepts nullable runtime_id.
Added ListPendingTasksByProvider for cloud worker claiming."
```

---

### Task 6: SQLC Queries — Capability CRUD

**Files:**
- Create: `server/pkg/db/queries/capability.sql`

- [ ] **Step 1: Write capability queries**

```sql
-- name: ListCapabilities :many
SELECT * FROM agent_capability
WHERE agent_id = $1
ORDER BY name ASC;

-- name: ListCapabilitiesByWorkspace :many
SELECT * FROM agent_capability
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: GetCapability :one
SELECT * FROM agent_capability
WHERE id = $1;

-- name: CreateCapability :one
INSERT INTO agent_capability (
    workspace_id, agent_id, name, description,
    input_schema, output_schema, estimated_cost_cents, avg_duration_seconds
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateCapability :one
UPDATE agent_capability SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    input_schema = COALESCE(sqlc.narg('input_schema'), input_schema),
    output_schema = COALESCE(sqlc.narg('output_schema'), output_schema),
    estimated_cost_cents = COALESCE(sqlc.narg('estimated_cost_cents'), estimated_cost_cents),
    avg_duration_seconds = COALESCE(sqlc.narg('avg_duration_seconds'), avg_duration_seconds),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCapability :exec
DELETE FROM agent_capability WHERE id = $1;

-- name: SearchCapabilities :many
-- Search capabilities by name or description across all agents in a workspace.
-- Used by A2A discovery (Phase 6) to find agents that can handle a task.
SELECT ac.*, a.name AS agent_name FROM agent_capability ac
JOIN agent a ON a.id = ac.agent_id
WHERE ac.workspace_id = $1
  AND (ac.name ILIKE '%' || $2 || '%' OR ac.description ILIKE '%' || $2 || '%')
ORDER BY ac.name ASC;
```

- [ ] **Step 2: Run sqlc**

Run: `cd server && make sqlc`
Expected: Succeeds. New capability query functions generated.

- [ ] **Step 3: Commit**

```bash
git add server/pkg/db/queries/capability.sql server/pkg/db/generated/
git commit -m "feat(db): add SQLC queries for agent_capability CRUD + search"
```

---

### Task 7: SQLC Queries — Cost Events + Budget Policies

**Files:**
- Create: `server/pkg/db/queries/cost.sql`

- [ ] **Step 1: Write cost and budget queries**

```sql
-- name: CreateCostEvent :one
-- Append-only per PLAN.md §1.1. Corrections are recorded as separate rows
-- with cost_status='adjustment'. No UPDATE / DELETE query exists or should
-- be added to this file.
INSERT INTO cost_event (
    workspace_id, agent_id, task_id, model,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    cache_ttl_minutes, cost_cents, cost_status, trace_id, response_cache_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: ListCostEvents :many
SELECT * FROM cost_event
WHERE workspace_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListCostEventsByAgent :many
SELECT * FROM cost_event
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: SumCostByWorkspaceMonth :one
-- Sum cost_cents for the current month in a workspace. Used by the BudgetService
-- advisory-lock gate (PLAN.md §1.2). Aggregates actual + estimated;
-- 'included' rows (cache hits) and 'adjustment' reconciliations are excluded
-- from the primary limit.
SELECT COALESCE(SUM(cost_cents), 0)::INT AS total_cents
FROM cost_event
WHERE workspace_id = $1
  AND cost_status IN ('actual', 'estimated')
  AND created_at >= date_trunc('month', now());

-- name: SumCostByAgentMonth :one
SELECT COALESCE(SUM(cost_cents), 0)::INT AS total_cents
FROM cost_event
WHERE workspace_id = $1
  AND agent_id = $2
  AND cost_status IN ('actual', 'estimated')
  AND created_at >= date_trunc('month', now());

-- name: SumCostByTraceID :one
SELECT COALESCE(SUM(cost_cents), 0)::INT AS total_cents
FROM cost_event
WHERE trace_id = $1;

-- name: GetBudgetPolicy :one
SELECT * FROM budget_policy WHERE id = $1;

-- name: ListBudgetPolicies :many
SELECT * FROM budget_policy
WHERE workspace_id = $1
ORDER BY scope ASC, created_at ASC;

-- name: GetWorkspaceBudgetPolicy :one
SELECT * FROM budget_policy
WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id IS NULL;

-- name: GetAgentBudgetPolicy :one
SELECT * FROM budget_policy
WHERE workspace_id = $1 AND scope = 'agent' AND scope_id = $2;

-- name: CreateBudgetPolicy :one
INSERT INTO budget_policy (workspace_id, scope, scope_id, monthly_limit_cents, warning_threshold, hard_stop)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateBudgetPolicy :one
UPDATE budget_policy SET
    monthly_limit_cents = COALESCE(sqlc.narg('monthly_limit_cents'), monthly_limit_cents),
    warning_threshold = COALESCE(sqlc.narg('warning_threshold'), warning_threshold),
    hard_stop = COALESCE(sqlc.narg('hard_stop'), hard_stop),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteBudgetPolicy :exec
DELETE FROM budget_policy WHERE id = $1;
```

- [ ] **Step 2: Run sqlc**

Run: `cd server && make sqlc`
Expected: Succeeds.

- [ ] **Step 3: Commit**

```bash
git add server/pkg/db/queries/cost.sql server/pkg/db/generated/
git commit -m "feat(db): add SQLC queries for cost_event and budget_policy

Includes monthly cost aggregation queries for budget enforcement
and per-trace cost summation for delegation chain tracking."
```

---

### Task 8: SQLC Queries — Credentials + Embedding Config

**Files:**
- Create: `server/pkg/db/queries/credential.sql`
- Create: `server/pkg/db/queries/embedding.sql`

- [ ] **Step 1: Write credential queries**

```sql
-- name: ListCredentials :many
SELECT * FROM workspace_credential
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: ListCredentialsByType :many
SELECT * FROM workspace_credential
WHERE workspace_id = $1 AND credential_type = $2
ORDER BY name ASC;

-- name: ListActiveProviderCredentials :many
-- List active provider credentials for a given LLM provider.
-- Used by credential rotation to find available keys.
SELECT * FROM workspace_credential
WHERE workspace_id = $1
  AND credential_type = 'provider'
  AND provider = $2
  AND is_active = true
  AND (cooldown_until IS NULL OR cooldown_until < now())
ORDER BY usage_count ASC;

-- name: GetCredential :one
SELECT * FROM workspace_credential WHERE id = $1;

-- name: GetCredentialByName :one
SELECT * FROM workspace_credential
WHERE workspace_id = $1 AND name = $2;

-- name: CreateCredential :one
-- PLAN.md §1.4: nonce (12 bytes) + encryption_version are required alongside
-- encrypted_value. AAD (workspace_id || id || name) is computed at the
-- application layer and fed into the AES-GCM call — no DB column.
INSERT INTO workspace_credential (
    workspace_id, credential_type, name, provider,
    encrypted_value, nonce, encryption_version, description, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateCredentialValue :one
-- Rotation: re-encrypt under a new nonce (and optionally a new master-key
-- version). last_rotated_at tracks the rotation moment; updated_at tracks
-- every mutation.
UPDATE workspace_credential SET
    encrypted_value = $2,
    nonce = $3,
    encryption_version = $4,
    last_rotated_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateCredentialCooldown :exec
UPDATE workspace_credential SET
    cooldown_until = $2
WHERE id = $1;

-- name: IncrementCredentialUsage :exec
UPDATE workspace_credential SET
    usage_count = usage_count + 1,
    last_used_at = now()
WHERE id = $1;

-- name: DeactivateCredential :exec
UPDATE workspace_credential SET is_active = false WHERE id = $1;

-- name: ActivateCredential :exec
UPDATE workspace_credential SET is_active = true WHERE id = $1;

-- name: DeleteCredential :exec
DELETE FROM workspace_credential WHERE id = $1;

-- name: CreateCredentialAccessLog :exec
-- PLAN.md §1.4: trace_id (D8) binds access to the task's trace chain;
-- access_purpose tags the reason (llm_api_call, tool_injection, webhook_verify).
INSERT INTO credential_access_log (
    workspace_id, credential_id, agent_id, task_id, trace_id, access_purpose
) VALUES ($1, $2, $3, $4, $5, $6);
```

- [ ] **Step 2: Write embedding config queries**

```sql
-- name: GetEmbeddingConfig :one
SELECT * FROM workspace_embedding_config
WHERE workspace_id = $1;

-- name: UpsertEmbeddingConfig :one
INSERT INTO workspace_embedding_config (
    workspace_id, provider, model, dimensions, api_credential_id, endpoint_url
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id) DO UPDATE SET
    provider = EXCLUDED.provider,
    model = EXCLUDED.model,
    dimensions = EXCLUDED.dimensions,
    api_credential_id = EXCLUDED.api_credential_id,
    endpoint_url = EXCLUDED.endpoint_url,
    updated_at = now()
RETURNING *;
```

- [ ] **Step 3: Run sqlc**

Run: `cd server && make sqlc`
Expected: Succeeds.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/db/queries/credential.sql server/pkg/db/queries/embedding.sql server/pkg/db/generated/
git commit -m "feat(db): add SQLC queries for credentials and embedding config

Credential queries support rotation (active provider list sorted by usage),
cooldown management, and access audit logging.
Embedding config uses upsert for one-per-workspace semantics."
```

---

### Task 9: Error Classification System

**Files:**
- Create: `server/pkg/agent/errors.go`
- Create: `server/pkg/agent/errors_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/errors_test.go
package agent

import (
	"errors"
	"net/http"
	"testing"
)

func TestClassifyError_RateLimit(t *testing.T) {
	err := &APIError{StatusCode: 429, Message: "rate limit exceeded"}
	class := ClassifyError(err)
	if class.Type != ErrRateLimit {
		t.Errorf("expected ErrRateLimit, got %s", class.Type)
	}
	if !class.Retryable {
		t.Error("rate limit should be retryable")
	}
	if !class.ShouldRotateCredential {
		t.Error("rate limit should rotate credential")
	}
}

func TestClassifyError_AuthPermanent(t *testing.T) {
	err := &APIError{StatusCode: 401, Message: "invalid api key"}
	class := ClassifyError(err)
	if class.Type != ErrAuthPermanent {
		t.Errorf("expected ErrAuthPermanent, got %s", class.Type)
	}
	if class.Retryable {
		t.Error("auth permanent should not be retryable")
	}
}

func TestClassifyError_ServerError(t *testing.T) {
	err := &APIError{StatusCode: http.StatusInternalServerError, Message: "internal server error"}
	class := ClassifyError(err)
	if class.Type != ErrServerError {
		t.Errorf("expected ErrServerError, got %s", class.Type)
	}
	if !class.Retryable {
		t.Error("server error should be retryable")
	}
}

func TestClassifyError_ContextOverflow(t *testing.T) {
	err := &APIError{StatusCode: 400, Message: "maximum context length exceeded"}
	class := ClassifyError(err)
	if class.Type != ErrContextOverflow {
		t.Errorf("expected ErrContextOverflow, got %s", class.Type)
	}
	if !class.ShouldCompress {
		t.Error("context overflow should trigger compression")
	}
}

func TestClassifyError_Unknown(t *testing.T) {
	err := errors.New("something weird happened")
	class := ClassifyError(err)
	if class.Type != ErrUnknown {
		t.Errorf("expected ErrUnknown, got %s", class.Type)
	}
}

func TestClassifyError_Overloaded(t *testing.T) {
	err := &APIError{StatusCode: 529, Message: "overloaded"}
	class := ClassifyError(err)
	if class.Type != ErrOverloaded {
		t.Errorf("expected ErrOverloaded, got %s", class.Type)
	}
	if !class.Retryable {
		t.Error("overloaded should be retryable")
	}
	if class.BackoffSeconds < 30 {
		t.Error("overloaded should have >= 30s backoff")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestClassifyError -v`
Expected: FAIL — types/functions not defined.

- [ ] **Step 3: Write the implementation**

```go
// server/pkg/agent/errors.go
package agent

import (
	"errors"
	"strings"
)

// ErrorType identifies the class of error from an LLM provider.
type ErrorType string

const (
	ErrAuth              ErrorType = "auth"
	ErrAuthPermanent     ErrorType = "auth_permanent"
	ErrBilling           ErrorType = "billing"
	ErrRateLimit         ErrorType = "rate_limit"
	ErrOverloaded        ErrorType = "overloaded"
	ErrServerError       ErrorType = "server_error"
	ErrTimeout           ErrorType = "timeout"
	ErrContextOverflow   ErrorType = "context_overflow"
	ErrPayloadTooLarge   ErrorType = "payload_too_large"
	ErrModelNotFound     ErrorType = "model_not_found"
	ErrFormatError       ErrorType = "format_error"
	ErrUnknown           ErrorType = "unknown"
)

// ErrorClassification contains recovery hints for a classified error.
type ErrorClassification struct {
	Type                  ErrorType
	Retryable             bool
	ShouldCompress        bool
	ShouldRotateCredential bool
	ShouldFallback        bool
	BackoffSeconds        int
}

// APIError represents an HTTP error from an LLM provider API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

// ClassifyError determines the error type and recovery strategy.
func ClassifyError(err error) ErrorClassification {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return classifyAPIError(apiErr)
	}

	msg := strings.ToLower(err.Error())

	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timeout") {
		return ErrorClassification{Type: ErrTimeout, Retryable: true, BackoffSeconds: 5}
	}

	return ErrorClassification{Type: ErrUnknown, Retryable: false}
}

func classifyAPIError(err *APIError) ErrorClassification {
	msg := strings.ToLower(err.Message)

	switch {
	case err.StatusCode == 401:
		if strings.Contains(msg, "billing") || strings.Contains(msg, "payment") {
			return ErrorClassification{Type: ErrBilling, Retryable: false}
		}
		return ErrorClassification{Type: ErrAuthPermanent, Retryable: false}

	case err.StatusCode == 403:
		return ErrorClassification{Type: ErrAuthPermanent, Retryable: false}

	case err.StatusCode == 429:
		return ErrorClassification{
			Type:                  ErrRateLimit,
			Retryable:             true,
			ShouldRotateCredential: true,
			BackoffSeconds:        10,
		}

	case err.StatusCode == 529:
		return ErrorClassification{
			Type:           ErrOverloaded,
			Retryable:      true,
			ShouldFallback: true,
			BackoffSeconds: 30,
		}

	case err.StatusCode >= 500:
		return ErrorClassification{
			Type:           ErrServerError,
			Retryable:      true,
			ShouldFallback: true,
			BackoffSeconds: 5,
		}

	case err.StatusCode == 400:
		if strings.Contains(msg, "context length") || strings.Contains(msg, "too many tokens") || strings.Contains(msg, "maximum") {
			return ErrorClassification{
				Type:           ErrContextOverflow,
				Retryable:      true,
				ShouldCompress: true,
			}
		}
		if strings.Contains(msg, "payload too large") || strings.Contains(msg, "request too large") {
			return ErrorClassification{Type: ErrPayloadTooLarge, Retryable: false}
		}
		return ErrorClassification{Type: ErrFormatError, Retryable: false}

	case err.StatusCode == 404:
		if strings.Contains(msg, "model") {
			return ErrorClassification{Type: ErrModelNotFound, Retryable: false}
		}
		return ErrorClassification{Type: ErrUnknown, Retryable: false}

	default:
		return ErrorClassification{Type: ErrUnknown, Retryable: false}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./pkg/agent/ -run TestClassifyError -v`
Expected: All 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/errors.go server/pkg/agent/errors_test.go
git commit -m "feat(agent): add error classification system

Classifies LLM API errors into types with recovery hints:
retryable, should_compress, should_rotate_credential, should_fallback.
Ported from Hermes error_classifier taxonomy."
```

---

### Task 10: Credential Pool Service

**Files:**
- Create: `server/pkg/agent/credentials.go`
- Create: `server/pkg/agent/credentials_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/credentials_test.go
package agent

import (
	"testing"
	"time"
)

func TestRotationStrategy_FillFirst(t *testing.T) {
	creds := []CredentialEntry{
		{ID: "a", UsageCount: 10, CooldownUntil: time.Time{}},
		{ID: "b", UsageCount: 5, CooldownUntil: time.Time{}},
		{ID: "c", UsageCount: 20, CooldownUntil: time.Time{}},
	}
	picked := pickCredential(creds, StrategyFillFirst)
	if picked.ID != "a" {
		t.Errorf("fill_first should pick first credential, got %s", picked.ID)
	}
}

func TestRotationStrategy_LeastUsed(t *testing.T) {
	creds := []CredentialEntry{
		{ID: "a", UsageCount: 10},
		{ID: "b", UsageCount: 5},
		{ID: "c", UsageCount: 20},
	}
	picked := pickCredential(creds, StrategyLeastUsed)
	if picked.ID != "b" {
		t.Errorf("least_used should pick b (5 uses), got %s", picked.ID)
	}
}

func TestRotationStrategy_RoundRobin(t *testing.T) {
	creds := []CredentialEntry{
		{ID: "a", UsageCount: 10},
		{ID: "b", UsageCount: 10},
		{ID: "c", UsageCount: 10},
	}
	// Round robin uses modular index based on total usage
	picked := pickCredential(creds, StrategyRoundRobin)
	if picked.ID == "" {
		t.Error("round_robin should pick a credential")
	}
}

func TestPickCredential_SkipsCoolingDown(t *testing.T) {
	future := time.Now().Add(time.Hour)
	creds := []CredentialEntry{
		{ID: "a", UsageCount: 0, CooldownUntil: future},  // cooling down
		{ID: "b", UsageCount: 100, CooldownUntil: time.Time{}}, // available
	}
	picked := pickCredential(creds, StrategyFillFirst)
	if picked.ID != "b" {
		t.Errorf("should skip cooling-down credential, got %s", picked.ID)
	}
}

func TestPickCredential_AllCoolingDown(t *testing.T) {
	future := time.Now().Add(time.Hour)
	creds := []CredentialEntry{
		{ID: "a", CooldownUntil: future},
		{ID: "b", CooldownUntil: future},
	}
	picked := pickCredential(creds, StrategyFillFirst)
	if picked != nil {
		t.Error("should return nil when all credentials cooling down")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestRotation -v && go test ./pkg/agent/ -run TestPickCredential -v`
Expected: FAIL — types/functions not defined.

- [ ] **Step 3: Write the implementation**

```go
// server/pkg/agent/credentials.go
package agent

import (
	"sort"
	"time"
)

// RotationStrategy determines how credentials are selected from the pool.
type RotationStrategy string

const (
	StrategyFillFirst  RotationStrategy = "fill_first"
	StrategyRoundRobin RotationStrategy = "round_robin"
	StrategyRandom     RotationStrategy = "random"
	StrategyLeastUsed  RotationStrategy = "least_used"
)

// CredentialEntry is a lightweight representation of a credential for rotation logic.
// The actual encrypted value is never loaded into this struct — it stays in the DB
// and is fetched only at injection time.
type CredentialEntry struct {
	ID            string
	UsageCount    int
	CooldownUntil time.Time
}

// pickCredential selects a credential from the pool using the given strategy.
// Returns nil if no credentials are available (all cooling down).
func pickCredential(creds []CredentialEntry, strategy RotationStrategy) *CredentialEntry {
	// Filter out cooling-down credentials
	available := make([]CredentialEntry, 0, len(creds))
	for _, c := range creds {
		if c.CooldownUntil.IsZero() || c.CooldownUntil.Before(time.Now()) {
			available = append(available, c)
		}
	}

	if len(available) == 0 {
		return nil
	}

	switch strategy {
	case StrategyFillFirst:
		return &available[0]

	case StrategyLeastUsed:
		sort.Slice(available, func(i, j int) bool {
			return available[i].UsageCount < available[j].UsageCount
		})
		return &available[0]

	case StrategyRoundRobin:
		totalUsage := 0
		for _, c := range available {
			totalUsage += c.UsageCount
		}
		idx := totalUsage % len(available)
		return &available[idx]

	case StrategyRandom:
		// Use modular arithmetic on total usage as a cheap pseudo-random
		// (real randomness not needed for credential rotation)
		totalUsage := 0
		for _, c := range available {
			totalUsage += c.UsageCount
		}
		idx := totalUsage % len(available)
		return &available[idx]

	default:
		return &available[0]
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./pkg/agent/ -run "TestRotation|TestPickCredential" -v`
Expected: All 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/credentials.go server/pkg/agent/credentials_test.go
git commit -m "feat(agent): add credential pool rotation strategies

Supports fill_first, round_robin, random, least_used strategies.
Automatically skips credentials in cooldown period.
Ported from Hermes credential_pool concept; schema modeled on Paperclip
packages/db/src/schema/company_secrets.ts + company_secret_versions.ts."
```

---

### Task 10.5: BudgetService with pg_advisory_xact_lock (PLAN.md §1.2)

**Files:**
- Create: `server/internal/service/budget.go`
- Create: `server/internal/service/budget_test.go`

**Goal:** Race-free budget dispatch gate. PLAN.md §1.2 (R3 B5) specifies a single
`CanDispatch(ctx, tx, wsID, estCents) (Decision, error)` entry point used by
Phase 2 (`ClaimTaskForRuntime`), Phase 6 (`task`-tool subagent dispatch), and
Phase 8A (chat handler). Without the advisory lock, three concurrent code
paths can all read "under budget" and all dispatch — `hard_stop=true`
becomes advisory. Phase 1 ships the service; later phases wire the calls.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/budget_test.go
package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCanDispatch_HardStopBlocks(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	wsID := seedWorkspace(t, pool)
	seedBudget(t, pool, wsID, 1000 /* cents */, true /* hard_stop */)
	// Pre-seed $9 of actuals; $2 request projects to $11 > $10 limit.
	seedCostEvent(t, pool, wsID, 900, "actual")

	b := NewBudgetService(pool)
	tx, _ := pool.BeginTx(ctx, pgx.TxOptions{})
	defer tx.Rollback(ctx)

	dec, err := b.CanDispatch(ctx, tx, wsID, 200)
	if err != nil { t.Fatal(err) }
	if dec.Allow { t.Fatal("expected Allow=false under hard_stop") }
	if dec.Reason != "budget_exceeded" { t.Errorf("reason = %q", dec.Reason) }
}

func TestCanDispatch_ParallelCallsSerialize(t *testing.T) {
	// Two concurrent transactions for the same workspace — one acquires the
	// advisory lock first; the second blocks until the first commits or rolls
	// back. Asserts net spend never exceeds monthly_limit_cents under load.
	// (implementation: spin two goroutines; sync with a channel.)
}
```

- [ ] **Step 2: Implement `BudgetService.CanDispatch`**

```go
// server/internal/service/budget.go
package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Decision struct {
	Allow  bool
	Reason string  // populated only when Allow=false
	WarnAt float64 // projected / monthly_limit_cents
}

type BudgetService struct {
	pool *pgxpool.Pool
}

func NewBudgetService(pool *pgxpool.Pool) *BudgetService {
	return &BudgetService{pool: pool}
}

// CanDispatch is the race-free budget gate from PLAN.md §1.2.
// Callers pass their own pgx.Tx so the budget decision commits or rolls
// back together with the caller's dispatch write. The advisory lock is
// transaction-scoped, released on COMMIT/ROLLBACK automatically.
func (b *BudgetService) CanDispatch(ctx context.Context, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error) {
	// 1. Serialize every dispatch for this workspace inside this transaction.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('budget:'||$1::text, 0))`, wsID); err != nil {
		return Decision{}, fmt.Errorf("budget advisory lock: %w", err)
	}
	// 2. Aggregate current-month actuals + estimates.
	var spent int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_cents), 0)
		FROM cost_event
		WHERE workspace_id = $1
		  AND created_at >= date_trunc('month', NOW())
		  AND cost_status IN ('actual','estimated')
	`, wsID).Scan(&spent); err != nil {
		return Decision{}, fmt.Errorf("sum cost_event: %w", err)
	}
	// 3. Load workspace-scope policy (nil policy = allow).
	policy, err := b.loadPolicy(ctx, tx, wsID)
	if err != nil { return Decision{}, err }
	if policy == nil { return Decision{Allow: true}, nil }

	projected := spent + estCents
	if policy.HardStop && projected > policy.MonthlyLimitCents {
		return Decision{Allow: false, Reason: "budget_exceeded"}, nil
	}
	return Decision{Allow: true, WarnAt: float64(projected) / float64(policy.MonthlyLimitCents)}, nil
}

type policyRow struct {
	HardStop          bool
	MonthlyLimitCents int
}

func (b *BudgetService) loadPolicy(ctx context.Context, tx pgx.Tx, wsID uuid.UUID) (*policyRow, error) {
	var p policyRow
	err := tx.QueryRow(ctx, `
		SELECT hard_stop, monthly_limit_cents
		FROM budget_policy
		WHERE workspace_id = $1 AND scope = 'workspace' AND scope_id IS NULL
		LIMIT 1
	`, wsID).Scan(&p.HardStop, &p.MonthlyLimitCents)
	if err != nil {
		if err == pgx.ErrNoRows { return nil, nil }
		return nil, err
	}
	return &p, nil
}
```

- [ ] **Step 3: Run tests**

```bash
cd server && go test ./internal/service/ -run TestCanDispatch
```

Both tests pass. Parallel test uses `pgx.Pool` + `pgx.TxOptions{}` to verify
the lock actually serializes — not just that a single call returns the right
decision.

- [ ] **Step 4: Commit**

```bash
git add server/internal/service/budget.go server/internal/service/budget_test.go
git commit -m "feat(budget): race-free CanDispatch with pg_advisory_xact_lock

Implements the BudgetService contract from PLAN.md §1.2. Later phases
(worker claim tx in Phase 2, subagent dispatch in Phase 6, chat handler
in Phase 8A) all call CanDispatch inside their own transaction to make
hard_stop a real guarantee under concurrent load."
```

---

### Task 11: Embedder Interface

**Files:**
- Create: `server/pkg/embeddings/embeddings.go`
- Create: `server/pkg/embeddings/embeddings_test.go`

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/embeddings/embeddings_test.go
package embeddings

import (
	"context"
	"testing"
)

func TestStubEmbedder(t *testing.T) {
	emb := NewStubEmbedder(3)
	vecs, err := emb.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 3 {
		t.Fatalf("expected dimension 3, got %d", len(vecs[0]))
	}
	// Stub should produce deterministic output for same input
	vecs2, _ := emb.Embed(context.Background(), []string{"hello"})
	if vecs[0][0] != vecs2[0][0] {
		t.Error("stub should be deterministic for same input")
	}
}

func TestStubEmbedder_EmptyInput(t *testing.T) {
	emb := NewStubEmbedder(4)
	vecs, err := emb.Embed(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 0 {
		t.Fatalf("expected 0 vectors, got %d", len(vecs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/embeddings/ -run TestStub -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Write the implementation**

```go
// server/pkg/embeddings/embeddings.go
package embeddings

import (
	"context"
	"hash/fnv"
	"math"
)

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed returns one embedding vector per input text.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the output dimension count.
	Dimensions() int
}

// StubEmbedder produces deterministic fake embeddings for testing.
// It does NOT call any external API — use it in tests that need vectors
// but don't need real embedding quality.
type StubEmbedder struct {
	dims int
}

// NewStubEmbedder creates a stub embedder with the given output dimensions.
func NewStubEmbedder(dims int) *StubEmbedder {
	return &StubEmbedder{dims: dims}
}

func (s *StubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = deterministicVector(text, s.dims)
	}
	return result, nil
}

func (s *StubEmbedder) Dimensions() int {
	return s.dims
}

// deterministicVector generates a unit-normalized vector from a text string.
// Uses FNV hash for determinism — same input always produces same output.
func deterministicVector(text string, dims int) []float32 {
	vec := make([]float32, dims)
	h := fnv.New64a()
	h.Write([]byte(text))
	seed := h.Sum64()

	var sumSq float64
	for j := range vec {
		// Simple LCG to generate pseudo-random floats from seed
		seed = seed*6364136223846793005 + 1442695040888963407
		val := float32(seed>>33) / float32(1<<31) // normalize to [0, 1)
		vec[j] = val
		sumSq += float64(val * val)
	}

	// L2 normalize
	norm := float32(math.Sqrt(sumSq))
	if norm > 0 {
		for j := range vec {
			vec[j] /= norm
		}
	}

	return vec
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./pkg/embeddings/ -v`
Expected: Both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/embeddings/
git commit -m "feat(embeddings): add Embedder interface and StubEmbedder for tests

Embedder interface used by Phases 4 (Knowledge), 5 (Memory), 9 (Cache).
StubEmbedder produces deterministic fake vectors for test isolation."
```

---

### Task 12: Update Backend Handler — CreateAgent with agent_type

**Files:**
- Modify: `server/internal/handler/agent.go`

- [ ] **Step 1: Update AgentResponse to include new fields**

In `server/internal/handler/agent.go`, update the `AgentResponse` struct to add:

```go
type AgentResponse struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	RuntimeID          *string         `json:"runtime_id"` // Changed: nullable for non-coding agents
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	Instructions       string          `json:"instructions"`
	AvatarURL          *string         `json:"avatar_url"`
	AgentType          string          `json:"agent_type"`          // NEW
	Provider           *string         `json:"provider"`            // NEW
	Model              *string         `json:"model"`               // NEW
	RuntimeMode        string          `json:"runtime_mode"`
	RuntimeConfig      any             `json:"runtime_config"`
	Visibility         string          `json:"visibility"`
	Status             string          `json:"status"`
	MaxConcurrentTasks int32           `json:"max_concurrent_tasks"`
	OwnerID            *string         `json:"owner_id"`
	Skills             []SkillResponse `json:"skills"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	ArchivedAt         *string         `json:"archived_at"`
	ArchivedBy         *string         `json:"archived_by"`
}
```

- [ ] **Step 2: Update agentToResponse to map new fields**

```go
func agentToResponse(a db.Agent) AgentResponse {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	if rc == nil {
		rc = map[string]any{}
	}

	return AgentResponse{
		ID:                 uuidToString(a.ID),
		WorkspaceID:        uuidToString(a.WorkspaceID),
		RuntimeID:          uuidToPtr(a.RuntimeID),          // Changed: nullable
		Name:               a.Name,
		Description:        a.Description,
		Instructions:       a.Instructions,
		AvatarURL:          textToPtr(a.AvatarUrl),
		AgentType:          a.AgentType,                     // NEW
		Provider:           textToPtr(a.Provider),            // NEW
		Model:              textToPtr(a.Model),               // NEW
		RuntimeMode:        a.RuntimeMode,
		RuntimeConfig:      rc,
		Visibility:         a.Visibility,
		Status:             a.Status,
		MaxConcurrentTasks: a.MaxConcurrentTasks,
		OwnerID:            uuidToPtr(a.OwnerID),
		Skills:             []SkillResponse{},
		CreatedAt:          timestampToString(a.CreatedAt),
		UpdatedAt:          timestampToString(a.UpdatedAt),
		ArchivedAt:         timestampToPtr(a.ArchivedAt),
		ArchivedBy:         uuidToPtr(a.ArchivedBy),
	}
}
```

- [ ] **Step 3: Update CreateAgentRequest and CreateAgent handler**

```go
type CreateAgentRequest struct {
	Name               string  `json:"name"`
	Description        string  `json:"description"`
	Instructions       string  `json:"instructions"`
	AvatarURL          *string `json:"avatar_url"`
	AgentType          string  `json:"agent_type"`     // NEW: "coding", "llm_api", "http", "process"
	RuntimeID          string  `json:"runtime_id"`     // Required for coding agents only
	Provider           string  `json:"provider"`       // Required for non-coding agents. Backend family, not endpoint/command.
	Model              string  `json:"model"`          // Required for llm_api agents
	RuntimeConfig      any     `json:"runtime_config"`
	Visibility         string  `json:"visibility"`
	MaxConcurrentTasks int32   `json:"max_concurrent_tasks"`
}

func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ownerID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// Default agent type
	if req.AgentType == "" {
		req.AgentType = "coding"
	}

	// Validate by agent type
	var runtimeID pgtype.UUID
	runtimeMode := "cloud" // default for non-coding agents
	var runtimeConfigMap map[string]any

	switch req.AgentType {
	case "coding":
		if req.RuntimeID == "" {
			writeError(w, http.StatusBadRequest, "runtime_id is required for coding agents")
			return
		}
		runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
			ID:          parseUUID(req.RuntimeID),
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid runtime_id")
			return
		}
		runtimeID = runtime.ID
		runtimeMode = runtime.RuntimeMode

	case "llm_api":
		if req.Provider == "" {
			writeError(w, http.StatusBadRequest, "provider is required for llm_api agents")
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "model is required for llm_api agents")
			return
		}

	case "http", "process":
		if req.Provider == "" {
			writeError(w, http.StatusBadRequest, "provider is required for "+req.AgentType+" agents")
			return
		}
		if req.RuntimeConfig == nil {
			writeError(w, http.StatusBadRequest, "runtime_config is required for "+req.AgentType+" agents")
			return
		}
		if m, ok := req.RuntimeConfig.(map[string]any); ok {
			runtimeConfigMap = m
		} else {
			writeError(w, http.StatusBadRequest, "runtime_config must be an object")
			return
		}
		if req.AgentType == "http" {
			if _, ok := runtimeConfigMap["endpoint_url"]; !ok {
				writeError(w, http.StatusBadRequest, "runtime_config.endpoint_url is required for http agents")
				return
			}
		}
		if req.AgentType == "process" {
			if _, ok := runtimeConfigMap["command"]; !ok {
				writeError(w, http.StatusBadRequest, "runtime_config.command is required for process agents")
				return
			}
		}

	default:
		writeError(w, http.StatusBadRequest, "invalid agent_type: must be coding, llm_api, http, or process")
		return
	}

	if req.Visibility == "" {
		req.Visibility = "private"
	}
	if req.MaxConcurrentTasks == 0 {
		req.MaxConcurrentTasks = 6
	}

	rc, _ := json.Marshal(req.RuntimeConfig)
	if req.RuntimeConfig == nil {
		rc = []byte("{}")
	}

	agent, err := h.Queries.CreateAgent(r.Context(), db.CreateAgentParams{
		WorkspaceID:        parseUUID(workspaceID),
		Name:               req.Name,
		Description:        req.Description,
		Instructions:       req.Instructions,
		AvatarUrl:          ptrToText(req.AvatarURL),
		RuntimeMode:        runtimeMode,
		RuntimeConfig:      rc,
		RuntimeID:          runtimeID,
		Visibility:         req.Visibility,
		MaxConcurrentTasks: req.MaxConcurrentTasks,
		OwnerID:            parseUUID(ownerID),
		AgentType:          req.AgentType,
		Provider:           pgtype.Text{String: req.Provider, Valid: req.Provider != ""},
		Model:              pgtype.Text{String: req.Model, Valid: req.Model != ""},
	})
	if err != nil {
		slog.Error("create agent failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to create agent")
		return
	}

	resp := agentToResponse(agent)
	writeJSON(w, http.StatusCreated, resp)
}
```

- [ ] **Step 4: Update agentToMap for broadcasting**

Update the `agentToMap` function in `server/internal/service/task.go` to include new fields:

```go
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToPtr(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"agent_type":           a.AgentType,
		"provider":             util.TextToPtr(a.Provider),
		"model":                util.TextToPtr(a.Model),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"visibility":           a.Visibility,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
```

- [ ] **Step 5: Keep runtime-based execution guardrails intact in Phase 1**

Do **not** relax `TaskService` to enqueue runtime-less tasks yet. Instead:

- Leave the existing `agent.RuntimeID.Valid` checks in `EnqueueTaskForIssue`, `EnqueueTaskForMention`, and `EnqueueChatTask` unchanged.
- Update `server/internal/handler/issue.go` so assigning a non-coding agent is rejected in Phase 1 with a clear validation error.
- Update `server/internal/handler/chat.go` so `CreateChatSession` rejects non-coding agents with `400 non-coding agents are not chat-capable until Phase 2`.
- Keep mention-trigger behavior runtime-gated; it should continue skipping non-runnable agents.

This preserves current production behavior while still allowing Phase 1 to store and display non-coding agents safely.

- [ ] **Step 6: Verify Go build passes**

Run: `cd server && go build ./...`
Expected: Build succeeds (no compilation errors).

- [ ] **Step 7: Run Go tests**

Run: `cd server && go test ./... -count=1`
Expected: All existing tests pass (backwards compatible — existing agents default to 'coding').

- [ ] **Step 8: Commit**

```bash
git add server/internal/handler/agent.go server/internal/handler/issue.go server/internal/handler/chat.go
git commit -m "feat(handler): support agent_type in CreateAgent/UpdateAgent

CreateAgent now accepts agent_type, provider, model.
Coding agents require runtime_id (existing behavior).
Non-coding agents (llm_api, http, process) require provider and validated config.
Phase 1 keeps them non-runnable for assignment and chat until Phase 2."
```

---

### Task 13: Capability CRUD Handler

**Files:**
- Create: `server/internal/handler/capability.go`
- Modify: `server/cmd/server/router.go` (add routes)

- [ ] **Step 1: Write the capability handler**

```go
// server/internal/handler/capability.go
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type CapabilityResponse struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspace_id"`
	AgentID            string `json:"agent_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	InputSchema        any    `json:"input_schema"`
	OutputSchema       any    `json:"output_schema"`
	EstimatedCostCents *int32 `json:"estimated_cost_cents"`
	AvgDurationSeconds *int32 `json:"avg_duration_seconds"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func capabilityToResponse(c db.AgentCapability) CapabilityResponse {
	var inSchema, outSchema any
	if c.InputSchema != nil {
		json.Unmarshal(c.InputSchema, &inSchema)
	}
	if c.OutputSchema != nil {
		json.Unmarshal(c.OutputSchema, &outSchema)
	}
	return CapabilityResponse{
		ID:                 uuidToString(c.ID),
		WorkspaceID:        uuidToString(c.WorkspaceID),
		AgentID:            uuidToString(c.AgentID),
		Name:               c.Name,
		Description:        c.Description,
		InputSchema:        inSchema,
		OutputSchema:       outSchema,
		EstimatedCostCents: int32ToPtr(c.EstimatedCostCents),
		AvgDurationSeconds: int32ToPtr(c.AvgDurationSeconds),
		CreatedAt:          timestampToString(c.CreatedAt),
		UpdatedAt:          timestampToString(c.UpdatedAt),
	}
}

func (h *Handler) ListCapabilities(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	agentID := chi.URLParam(r, "agentId")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	caps, err := h.Queries.ListCapabilities(r.Context(), agent.ID)
	if err != nil {
		slog.Error("list capabilities failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to list capabilities")
		return
	}
	resp := make([]CapabilityResponse, len(caps))
	for i, c := range caps {
		resp[i] = capabilityToResponse(c)
	}
	writeJSON(w, http.StatusOK, resp)
}

type CreateCapabilityRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	InputSchema        any    `json:"input_schema"`
	OutputSchema       any    `json:"output_schema"`
	EstimatedCostCents *int32 `json:"estimated_cost_cents"`
	AvgDurationSeconds *int32 `json:"avg_duration_seconds"`
}

func (h *Handler) CreateCapability(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	agentID := chi.URLParam(r, "agentId")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}

	var req CreateCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Description == "" {
		writeError(w, http.StatusBadRequest, "name and description are required")
		return
	}

	inSchema, _ := json.Marshal(req.InputSchema)
	outSchema, _ := json.Marshal(req.OutputSchema)

	cap, err := h.Queries.CreateCapability(r.Context(), db.CreateCapabilityParams{
		WorkspaceID:        parseUUID(workspaceID),
		AgentID:            agent.ID,
		Name:               req.Name,
		Description:        req.Description,
		InputSchema:        inSchema,
		OutputSchema:       outSchema,
		EstimatedCostCents: ptrToInt32(req.EstimatedCostCents),
		AvgDurationSeconds: ptrToInt32(req.AvgDurationSeconds),
	})
	if err != nil {
		slog.Error("create capability failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to create capability")
		return
	}

	writeJSON(w, http.StatusCreated, capabilityToResponse(cap))
}

func (h *Handler) DeleteCapability(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	capID := chi.URLParam(r, "capabilityId")
	cap, err := h.Queries.GetCapability(r.Context(), parseUUID(capID))
	if err != nil || uuidToString(cap.WorkspaceID) != workspaceID {
		writeError(w, http.StatusNotFound, "capability not found")
		return
	}
	if err := h.Queries.DeleteCapability(r.Context(), cap.ID); err != nil {
		slog.Error("delete capability failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to delete capability")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 2: Register routes in router.go**

In `server/cmd/server/router.go`, in the protected API route group, add:

```go
// Agent capabilities
r.Get("/agents/{agentId}/capabilities", h.ListCapabilities)
r.Post("/agents/{agentId}/capabilities", h.CreateCapability)
r.Delete("/capabilities/{capabilityId}", h.DeleteCapability)
```

- [ ] **Step 3: Add helper functions if missing**

In `server/internal/handler/helpers.go` (or wherever helper functions live), ensure these exist:

```go
func int32ToPtr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func ptrToInt32(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}
```

- [ ] **Step 4: Add handler tests**

Use the existing `handler_test.go` helpers (the repo already has `setupTestHandler` + `authedRequest` patterns — search `handler_test.go` for the nearest example of a protected endpoint test). Add a table-driven test suite:

```go
// Append to server/internal/handler/handler_test.go (or a new capability_test.go
// in the same package). Names match the assertions the plan calls out.

func TestCapability_ListCreateDelete_WorkspaceMember(t *testing.T) {
	t.Parallel()
	h, ts := setupTestHandler(t)
	defer ts.Close()

	wsID, userID := ts.CreateWorkspaceAndMember()
	agentID := ts.CreateLLMAgent(wsID, "anthropic", "claude-haiku-4-5")

	// Create
	body := `{"name":"lookup","description":"Find a record","input_schema":{}}`
	resp := ts.Do(authedRequest(t, "POST", "/agents/"+agentID+"/capabilities", body, wsID, userID))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.Code, resp.Body.String())
	}
	var created CapabilityResponse
	_ = json.NewDecoder(resp.Body).Decode(&created)

	// List
	resp = ts.Do(authedRequest(t, "GET", "/agents/"+agentID+"/capabilities", "", wsID, userID))
	if resp.Code != http.StatusOK {
		t.Fatalf("list: %d", resp.Code)
	}
	var list []CapabilityResponse
	_ = json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v", list)
	}

	// Delete
	resp = ts.Do(authedRequest(t, "DELETE", "/capabilities/"+created.ID, "", wsID, userID))
	if resp.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.Code)
	}
}

func TestCapability_CrossWorkspaceAccessDenied(t *testing.T) {
	t.Parallel()
	h, ts := setupTestHandler(t)
	defer ts.Close()

	wsA, userA := ts.CreateWorkspaceAndMember()
	wsB, userB := ts.CreateWorkspaceAndMember()
	agentA := ts.CreateLLMAgent(wsA, "anthropic", "claude-haiku-4-5")
	capA := ts.CreateCapability(agentA, "lookup", "Find a record")

	// User from workspace B tries to read / delete cap from workspace A
	resp := ts.Do(authedRequest(t, "GET", "/agents/"+agentA+"/capabilities", "", wsB, userB))
	if resp.Code == http.StatusOK {
		t.Error("cross-workspace list should be forbidden")
	}
	resp = ts.Do(authedRequest(t, "DELETE", "/capabilities/"+capA, "", wsB, userB))
	if resp.Code == http.StatusNoContent {
		t.Error("cross-workspace delete should be forbidden")
	}
}

func TestCapability_CreateFailsWhenAgentBelongsToOtherWorkspace(t *testing.T) {
	t.Parallel()
	h, ts := setupTestHandler(t)
	defer ts.Close()

	wsA, _ := ts.CreateWorkspaceAndMember()
	wsB, userB := ts.CreateWorkspaceAndMember()
	agentA := ts.CreateLLMAgent(wsA, "anthropic", "claude-haiku-4-5")

	body := `{"name":"x","description":"y"}`
	resp := ts.Do(authedRequest(t, "POST", "/agents/"+agentA+"/capabilities", body, wsB, userB))
	if resp.Code < 400 {
		t.Errorf("expected 4xx, got %d", resp.Code)
	}
}
```

**Prerequisite:** the handler test scaffold (`setupTestHandler`, `authedRequest`, `ts.CreateWorkspaceAndMember`, `ts.CreateLLMAgent`, `ts.CreateCapability`) either already exists in the repo (search `handler_test.go` for `setupTest*` helpers) or needs to be added inline. Phase 1's existing handler suite already has analogous builders for the `coding` agent path; this step extends them with one new method (`CreateLLMAgent`) and one new convenience helper (`CreateCapability`). If no scaffold exists at all, add a minimal one alongside the tests — don't block this step on a separate test-utility task.

**Parallel coverage for Task 14:** the cost + budget handler (Task 14) currently has no explicit test step. Apply the same pattern there:
- `TestCost_ListRequiresWorkspaceMember` — workspace member can list cost_events scoped to their workspace; a user from another workspace gets 403/404
- `TestBudget_ListRequiresWorkspaceMember` — same for budget policies
- `TestCost_Summary_ReturnsZeroForFreshWorkspace` — a workspace with no cost_events returns `{"current_month_cost_cents": 0}`

Use the same scaffold. This closes Phase 1's S6 gap (missing handler tests) end-to-end.

- [ ] **Step 5: Verify build**

Run: `cd server && go build ./...`
Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/capability.go server/cmd/server/router.go server/internal/handler/handler_test.go
git commit -m "feat(handler): add capability CRUD endpoints

GET /agents/:agentId/capabilities — list capabilities
POST /agents/:agentId/capabilities — create capability
DELETE /capabilities/:capabilityId — delete capability"
```

---

### Task 14: Cost Event + Budget Handler Stubs

**Files:**
- Create: `server/internal/handler/cost.go`
- Modify: `server/cmd/server/router.go`

- [ ] **Step 1: Write cost/budget handlers**

```go
// server/internal/handler/cost.go
package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/multica-ai/multica/server/internal/logger"
)

func (h *Handler) ListCostEvents(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	events, err := h.Queries.ListCostEvents(r.Context(), db.ListCostEventsParams{
		WorkspaceID: parseUUID(workspaceID),
		Limit:       int32(limit),
		Offset:      int32(offset),
	})
	if err != nil {
		slog.Error("list cost events failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to list cost events")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *Handler) GetWorkspaceCostSummary(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)

	totalCents, err := h.Queries.SumCostByWorkspaceMonth(r.Context(), parseUUID(workspaceID))
	if err != nil {
		slog.Error("sum cost failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to get cost summary")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_month_cost_cents": totalCents,
	})
}

func (h *Handler) ListBudgetPolicies(w http.ResponseWriter, r *http.Request) {
	workspaceID := resolveWorkspaceID(r)
	policies, err := h.Queries.ListBudgetPolicies(r.Context(), parseUUID(workspaceID))
	if err != nil {
		slog.Error("list budget policies failed", logger.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "failed to list budget policies")
		return
	}
	writeJSON(w, http.StatusOK, policies)
}
```

Required corrections to this stub before implementation:

- Every handler must call `workspaceMember(...)` first so billing data is workspace-scoped, not just auth-scoped.
- Do not return raw sqlc rows for `cost_event` / `budget_policy`; add explicit response DTOs so the API does not leak `pgtype` fields.
- Add tests proving a user from another workspace cannot read `/costs`, `/costs/summary`, or `/budgets`.

- [ ] **Step 2: Register routes**

```go
// Cost & Budget
r.Get("/costs", h.ListCostEvents)
r.Get("/costs/summary", h.GetWorkspaceCostSummary)
r.Get("/budgets", h.ListBudgetPolicies)
```

- [ ] **Step 3: Add handler tests**

Add tests covering:
- billing endpoints require workspace membership
- a user from another workspace cannot read cost events or budget policies
- responses serialize plain JSON API shapes, not raw `pgtype` structs

- [ ] **Step 4: Verify build**

Run: `cd server && go build ./...`
Expected: Succeeds.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/cost.go server/cmd/server/router.go
git commit -m "feat(handler): add cost event listing and budget policy endpoints

GET /costs — paginated cost events
GET /costs/summary — current month workspace cost
GET /budgets — list budget policies"
```

---

### Task 15: Frontend — Agent Type Definitions

**Files:**
- Modify: `packages/core/types/agent.ts`

- [ ] **Step 1: Update the Agent interface**

Add the new fields to the `Agent` interface:

```typescript
// After existing fields, add:
agent_type: AgentType;
provider: string | null;
model: string | null;
```

Add the new types:

```typescript
export type AgentType = 'coding' | 'llm_api' | 'http' | 'process';

export interface AgentCapability {
  id: string;
  workspace_id: string;
  agent_id: string;
  name: string;
  description: string;
  input_schema: Record<string, unknown> | null;
  output_schema: Record<string, unknown> | null;
  estimated_cost_cents: number | null;
  avg_duration_seconds: number | null;
  created_at: string;
  updated_at: string;
}
```

- [ ] **Step 2: Update CreateAgentRequest**

```typescript
export interface CreateAgentRequest {
  name: string;
  description?: string;
  instructions?: string;
  avatar_url?: string;
  agent_type?: AgentType;        // NEW — defaults to 'coding' on backend
  runtime_id?: string;           // Required for coding agents only
  provider?: string;             // Required for non-coding agents. Backend family, not endpoint/command.
  model?: string;                // Required for llm_api agents
  runtime_config?: Record<string, unknown>;
  visibility?: AgentVisibility;
  max_concurrent_tasks?: number;
}
```

- [ ] **Step 3: Update AgentResponse RuntimeID to be nullable**

The `runtime_id` field in the `Agent` interface should become `string | null` since non-coding agents won't have one:

```typescript
runtime_id: string | null;
```

- [ ] **Step 4: Verify typecheck**

Run: `pnpm typecheck`
Expected: May surface type errors in components that assume `runtime_id` is always a string. Fix them by adding null checks or using optional chaining.

- [ ] **Step 5: Fix any type errors from nullable runtime_id**

Common fixes:
- In `agent-detail.tsx` settings tab: use `agent.runtime_id ?? ''` where runtime_id is displayed
- In `create-agent-dialog.tsx`: runtime_id is only required when agent_type is 'coding'
- In `tasks-tab.tsx` or anywhere runtime_id is passed: add null guard

- [ ] **Step 6: Verify typecheck passes**

Run: `pnpm typecheck`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add packages/core/types/agent.ts
git commit -m "feat(core): add agent_type, provider, model to Agent type

AgentType enum: coding, llm_api, http, process.
runtime_id now nullable (non-coding agents skip runtime).
Added AgentCapability interface for A2A discovery."
```

---

### Task 16: Frontend — API Client Methods

**Files:**
- Modify: `packages/core/api/client.ts`

- [ ] **Step 1: Add capability API methods**

```typescript
// Agent Capabilities
listCapabilities(agentId: string): Promise<AgentCapability[]> {
  return this.fetch(`/api/agents/${agentId}/capabilities`);
}

createCapability(agentId: string, data: {
  name: string;
  description: string;
  input_schema?: Record<string, unknown>;
  output_schema?: Record<string, unknown>;
  estimated_cost_cents?: number;
  avg_duration_seconds?: number;
}): Promise<AgentCapability> {
  return this.fetch(`/api/agents/${agentId}/capabilities`, {
    method: "POST",
    body: JSON.stringify(data),
  });
}

deleteCapability(capabilityId: string): Promise<void> {
  return this.fetch(`/api/capabilities/${capabilityId}`, { method: "DELETE" });
}
```

- [ ] **Step 2: Add cost/budget API methods**

```typescript
// Cost & Budget
listCostEvents(params?: { limit?: number; offset?: number }): Promise<CostEvent[]> {
  const search = new URLSearchParams();
  if (params?.limit !== undefined) search.set("limit", String(params.limit));
  if (params?.offset !== undefined) search.set("offset", String(params.offset));
  return this.fetch(`/api/costs?${search}`);
}

getCostSummary(): Promise<{ current_month_cost_cents: number }> {
  return this.fetch("/api/costs/summary");
}

listBudgetPolicies(): Promise<BudgetPolicy[]> {
  return this.fetch("/api/budgets");
}
```

- [ ] **Step 3: Add CostEvent and BudgetPolicy types to types/agent.ts**

```typescript
export interface CostEvent {
  id: string;
  workspace_id: string;
  agent_id: string | null;
  task_id: string | null;
  model: string;
  input_tokens: number | null;      // NON-CACHED input tokens only (PLAN.md §1.1 / R3 S6)
  output_tokens: number | null;
  cache_read_tokens: number | null; // Anthropic cache_read_input_tokens (0.1x rate)
  cache_write_tokens: number | null;
  cache_ttl_minutes: number | null; // 5 or 60 — determines write multiplier
  cost_cents: number;
  cost_status: 'actual' | 'estimated' | 'included' | 'adjustment';
  trace_id: string;                  // D8: NOT NULL — mandatory on every row
  session_hours: number | null;      // Phase 12: Claude Managed session billing
  session_cost_cents: number | null;
  response_cache_id: string | null;  // Phase 9: set on cache-hit events
  created_at: string;
}

export interface BudgetPolicy {
  id: string;
  workspace_id: string;
  scope: 'workspace' | 'agent' | 'project';
  scope_id: string | null;
  monthly_limit_cents: number;
  warning_threshold: number;
  hard_stop: boolean;
  created_at: string;
  updated_at: string;
}
```

- [ ] **Step 4: Verify typecheck**

Run: `pnpm typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/core/api/client.ts packages/core/types/agent.ts
git commit -m "feat(core): add API methods for capabilities, costs, budgets

New client methods: listCapabilities, createCapability, deleteCapability,
listCostEvents, getCostSummary, listBudgetPolicies."
```

---

### Task 17: Frontend — Agent Creation Flow Redesign

**Files:**
- Modify: `packages/views/agents/components/create-agent-dialog.tsx`
- Modify: `packages/views/agents/config.ts`

- [ ] **Step 1: Add agent type display config**

In `packages/views/agents/config.ts`, add:

```typescript
export const agentTypeConfig: Record<AgentType, { label: string; description: string; icon: string }> = {
  coding: {
    label: 'Coding Agent',
    description: 'Runs code via a local daemon or cloud sandbox',
    icon: 'Code',
  },
  llm_api: {
    label: 'AI Agent',
    description: 'Calls LLM APIs directly (no runtime needed)',
    icon: 'Bot',
  },
  http: {
    label: 'HTTP Agent',
    description: 'Calls any HTTP endpoint as an agent backend',
    icon: 'Globe',
  },
  process: {
    label: 'Custom Script',
    description: 'Runs a custom command or script',
    icon: 'Terminal',
  },
};
```

- [ ] **Step 2: Redesign CreateAgentDialog with type selection step**

Redesign the dialog to use a two-step flow:

**Step 1 — Agent type selection:**
4 cards for Coding/AI/HTTP/Custom Script, each with icon + description.

**Step 2 — Type-specific configuration:**
- Coding: existing runtime dropdown (required)
- AI Agent: provider dropdown + model input (no runtime)
- HTTP Agent: provider hidden/read-only as `http`, plus endpoint/auth/template fields stored in `runtime_config`
- Process Agent: provider hidden/read-only as `process`, plus command/args/env fields stored in `runtime_config`
- Plus common fields: name, description, visibility

The key change: runtime selector is only shown for `agent_type === 'coding'`. Provider/model fields shown for other types.

```typescript
// Pseudocode for the dialog state:
const [step, setStep] = useState<'type' | 'config'>('type');
const [agentType, setAgentType] = useState<AgentType>('coding');

// In step 'type': render 4 clickable cards
// On card click: setAgentType(type), setStep('config')

// In step 'config': render type-specific fields
// - If coding: show runtime dropdown (existing)
// - If llm_api: show provider select + model input
// - If http: set provider="http" and collect endpoint/auth/template config
// - If process: set provider="process" and collect command/env config

// On submit: call api.createAgent({
//   ...commonFields,
//   agent_type: agentType,
//   runtime_id: agentType === 'coding' ? selectedRuntimeId : undefined,
//   provider: agentType === 'llm_api' ? provider : agentType === 'http' ? 'http' : agentType === 'process' ? 'process' : undefined,
//   model: agentType === 'llm_api' ? model : undefined,
//   runtime_config: agentType === 'http' ? httpConfig : agentType === 'process' ? processConfig : undefined,
// })
```

- [ ] **Step 3: Add provider dropdown options**

```typescript
const providerOptions = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'google', label: 'Google' },
  { value: 'custom', label: 'Custom / Other' },
];

const modelOptions: Record<string, { value: string; label: string }[]> = {
  anthropic: [
    { value: 'claude-sonnet-4-5-20250514', label: 'Claude Sonnet 4.5' },
    { value: 'claude-haiku-4-5-20251001', label: 'Claude Haiku 4.5' },
    { value: 'claude-opus-4-5-20250514', label: 'Claude Opus 4.5' },
  ],
  openai: [
    { value: 'gpt-4o', label: 'GPT-4o' },
    { value: 'gpt-4o-mini', label: 'GPT-4o Mini' },
  ],
};
```

- [ ] **Step 4: Verify typecheck**

Run: `pnpm typecheck`
Expected: PASS.

- [ ] **Step 5: Test in browser**

Run: `pnpm dev:web`
Navigate to Agents page. Click "Create Agent".
Expected:
1. Type selection step shows 4 cards
2. Clicking "Coding Agent" shows runtime dropdown (existing behavior)
3. Clicking "AI Agent" shows provider + model dropdowns, NO runtime selector
4. Clicking "HTTP Agent" or "Custom Script" shows config fields backed by `runtime_config`, not a free-form provider text box
5. Creating an AI agent succeeds (backend accepts `agent_type=llm_api`)
6. Creating a coding agent still works (backwards compatible)
7. Non-coding agents are labeled as "available after Phase 2" anywhere the product would otherwise imply they can run now

- [ ] **Step 6: Commit**

```bash
git add packages/views/agents/components/create-agent-dialog.tsx packages/views/agents/config.ts
git commit -m "feat(views): redesign agent creation dialog with type selection

Step 1: choose agent type (Coding, AI, HTTP, Custom Script).
Step 2: type-specific config — runtime for coding, provider/model for AI.
Non-coding agents skip runtime selection entirely."
```

---

### Task 18: Frontend — Agent Detail Updates

**Files:**
- Modify: `packages/views/agents/components/agent-detail.tsx`

- [ ] **Step 1: Show provider/model for non-coding agents**

In the agent detail header or settings tab, conditionally render:

```typescript
// If agent.agent_type !== 'coding', show provider and model instead of runtime
{agent.agent_type !== 'coding' ? (
  <div className="flex items-center gap-2 text-sm text-muted-foreground">
    <span>{agent.provider}</span>
    {agent.model && <span>/ {agent.model}</span>}
  </div>
) : (
  // existing runtime display
)}
```

- [ ] **Step 2: Show agent type badge in header**

```typescript
import { Badge } from '@multica/ui/components/ui/badge';
import { agentTypeConfig } from '../config';

// In agent detail header:
<Badge variant="secondary">
  {agentTypeConfig[agent.agent_type]?.label ?? agent.agent_type}
</Badge>
```

- [ ] **Step 3: Add a read-only capabilities tab**

Add a `Capabilities` tab to `agent-detail.tsx` and load it via `api.listCapabilities(agent.id)`.

Phase 1 requirement:
- show the capability list if present
- show an empty state if none exist
- editing controls can remain deferred; read-only visibility is enough for this phase

- [ ] **Step 4: Conditionally hide runtime in settings tab**

In the settings tab, the runtime display should be hidden when `agent.agent_type !== 'coding'`:

```typescript
{agent.agent_type === 'coding' && (
  <div>
    <Label>Runtime</Label>
    <p className="text-sm text-muted-foreground">{/* existing runtime display */}</p>
  </div>
)}
```

- [ ] **Step 5: Verify typecheck**

Run: `pnpm typecheck`
Expected: PASS.

- [ ] **Step 6: Test in browser**

1. View a coding agent — should show runtime as before
2. Create an AI agent, view it — should show provider/model, no runtime
3. Agent type badge visible in header
4. Capabilities tab visible and loads existing capabilities for the selected agent

- [ ] **Step 7: Commit**

```bash
git add packages/views/agents/components/agent-detail.tsx
git commit -m "feat(views): show agent_type badge and provider/model in detail

Non-coding agents display provider/model instead of runtime.
Agent type badge shown in header for quick identification.
Adds read-only capabilities tab for Phase 1 discoverability."
```

---

### Task 19: Full Verification

**Files:** None (verification only)

- [ ] **Step 1: Run make sqlc to ensure generated code is current**

Run: `cd server && make sqlc`
Expected: No changes (already regenerated).

- [ ] **Step 2: Run TypeScript typecheck**

Run: `pnpm typecheck`
Expected: PASS — all packages and apps compile.

- [ ] **Step 3: Run TypeScript tests**

Run: `pnpm test`
Expected: PASS — all existing tests pass. No regressions.

- [ ] **Step 4: Run Go tests**

Run: `cd server && go test ./... -count=1`
Expected: PASS — all existing tests pass, plus new error classifier and credential tests pass.

- [ ] **Step 5: Run full check**

Run: `make check`
Expected: ALL checks pass.

- [ ] **Step 6: Verify backwards compatibility manually**

1. Existing coding agents still work:
   - Can assign issues to coding agents
   - Daemon still claims and executes tasks
   - Task lifecycle (queued → dispatched → running → completed) works
2. Can create a new coding agent with runtime (existing flow)
3. Can create a new llm_api agent without runtime
4. Non-coding agents cannot be assigned to issues in Phase 1
5. Non-coding agents cannot start chat sessions in Phase 1
6. Agent list shows both types correctly
7. Agent detail shows appropriate fields per type, including read-only capabilities

- [ ] **Step 7: Verify migration safety**

Run: `cd server && make migrate-down && make migrate-up` (full down + up cycle)
Expected: All migrations apply cleanly. Rollback is only expected to succeed after removing or backfilling Phase 1 rows with `NULL runtime_id`.

- [ ] **Step 8: Verify go-ai dependency resolves**

Run: `cd server && go mod tidy && go build ./...`
Expected: `go.sum` populated with `github.com/digitallysavvy/go-ai v0.4.0` entries, build succeeds. This just proves the dep is fetchable — we don't wire it into any code until Phase 2.

---

### Task 20: go-ai Library Foundation (Fork + Mirror + Dependency)

**Goal:** Establish `github.com/digitallysavvy/go-ai` as the Phase 2 agent-harness foundation under a soft-fork safety net. No library code is wired into Multica in Phase 1; this task just sets up the dependency plumbing and fork policy so Phase 2 can start cleanly.

**Files:**
- Created (already done, outside this plan's worktree): `ahmed-khaire/go-ai` — fork repo with `upstream` remote to `digitallysavvy/go-ai`, `main` mirroring upstream
- Created (in fork): `.github/workflows/mirror-upstream.yml` — weekly fast-forward automation
- Created: `docs/engineering/go-ai-fork-policy.md` — activation runbook
- Created: `docs/engineering/go-ai-fork-activations.md` — append-only log (empty at Phase 1)
- Modified: `server/go.mod` — adds `github.com/digitallysavvy/go-ai v0.4.0` (upstream, not fork)
- Modified: `server/go.sum` — checksums populated via `go mod tidy`

**Status at Phase 1 start:** fork created (`ahmed-khaire/go-ai`), policy doc written, `server/go.mod` requires the dep. Remaining: commit the mirror workflow to the fork, run `go mod tidy` in `aicolab/server`.

- [ ] **Step 1: Verify fork topology**

Run (from the fork checkout):
```bash
git remote -v
# expected:
# origin    https://github.com/ahmed-khaire/go-ai.git
# upstream  https://github.com/digitallysavvy/go-ai.git

git fetch upstream
git log --oneline origin/main ^upstream/main    # should be empty (main is exact mirror)
git log --oneline upstream/main ^origin/main    # should be empty too
git log --oneline origin/multica-infra ^origin/main    # should show only infra commits
```
Expected: `main` is a byte-for-byte mirror of upstream. `multica-infra` is a few commits ahead of `main` by exactly the fork-infra files (mirror workflow, etc.).

- [ ] **Step 2: Commit the mirror-upstream workflow to `multica-infra` (NOT `main`)**

The workflow MUST live on `multica-infra`, which is the default branch. If committed to `main`, the mirror workflow will correctly detect that `origin/main` has diverged from `upstream/main` (because the workflow file itself is a fork-local commit) and fail every run.

```bash
cd <path-to-fork>
git checkout multica-infra
git add .github/workflows/mirror-upstream.yml
git commit -m "ci: add weekly upstream mirror workflow

Fast-forwards main from digitallysavvy/go-ai every Monday 03:00 UTC.
Fails loudly on history divergence. See multica-ai fork policy doc."
git push origin multica-infra
```

Then in the GitHub UI for `ahmed-khaire/go-ai`:
- Settings → General → Default branch → **ensure `multica-infra` is default** (scheduled workflows run from default branch)
- Settings → Actions → General → Workflow permissions → `Read and write`
- Actions tab → run `Mirror Upstream` once manually to verify it works

Expected: workflow run succeeds, summary shows `fast_forward_ok: true`, no push happens (already up to date).

- [ ] **Step 3: Verify `go-ai-fork-policy.md` covers the full runbook**

Open `docs/engineering/go-ai-fork-policy.md` and confirm it documents:
- Fork topology (origin/upstream URLs, branches)
- Default state (upstream direct, no `replace`)
- Weekly mirror automation reference
- Five activation triggers
- Activation procedure (branch, commit, tag `v0.4.0-multica.N`, update `replace`, run `go mod tidy`, open upstream PR, log entry)
- Deactivation procedure
- What stays at our adapter layer (extension points that do NOT need a fork)
- License (Apache-2.0)
- Known risk: fork on personal account, TODO to move to `multica-ai/go-ai` org when available

Expected: all sections present.

- [ ] **Step 4: Resolve and pin the dependency**

```bash
cd server && go mod tidy
```
Expected: `go.sum` gains entries for `github.com/digitallysavvy/go-ai` and its transitives (google/uuid, OTel SDK, etc.). `go build ./...` succeeds.

- [ ] **Step 5: Smoke-check the library is importable (no code wiring)**

Create a temporary file `server/pkg/agent/_goai_smoke_test.go` (underscore-prefixed so it is excluded from build):
```go
//go:build ignore

package agent

import (
    _ "github.com/digitallysavvy/go-ai/pkg/ai"
    _ "github.com/digitallysavvy/go-ai/pkg/agent"
    _ "github.com/digitallysavvy/go-ai/pkg/provider/types"
)
```
Run: `cd server && go vet ./pkg/agent/...`
Expected: vet succeeds. Delete the smoke file afterwards — we don't want it checked in. This step just confirms the modules are on disk and importable. Real integration is Phase 2.

- [ ] **Step 6: Commit the dependency changes**

```bash
git add server/go.mod server/go.sum \
        docs/engineering/go-ai-fork-policy.md \
        docs/engineering/go-ai-fork-activations.md
git commit -m "chore(deps): add go-ai v0.4.0 under soft-fork safety net

Adds github.com/digitallysavvy/go-ai as the Phase 2 agent-harness
foundation. Fork lives at ahmed-khaire/go-ai (dormant; main mirrors
upstream weekly). Activate the fork via replace directive only per
docs/engineering/go-ai-fork-policy.md."
```

**Non-goals for Phase 1:**
- No code in `aicolab/` imports go-ai yet. The adapter layer at `server/pkg/agent/harness/` lives in Phase 2.
- No `ToolLoopAgent`, `SkillRegistry`, or `SubagentRegistry` wiring. Phase 2.
- No fallback-chain or middleware code. Phase 2.

The sole Phase 1 outcome: `go build ./...` resolves the dependency and a documented, policy-governed fork is ready to activate if needed.

---

### Task 21: Central Defaults Package (D7)

**Files:**
- Create: `server/pkg/agent/defaults.go`
- Create: `server/pkg/agent/defaults_test.go`

**Goal:** Codify the shared tunables called out in decision D7 so every later phase references one canonical source instead of hardcoding magic numbers.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/defaults_test.go
package agent

import (
	"testing"
	"time"
)

func TestDefaults_ValuesMatchPlan(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"MainAgentMaxSteps", MainAgentMaxSteps, 50},
		{"SubagentMaxSteps", SubagentMaxSteps, 100},
		{"ContextEvictionThresholdBytes", ContextEvictionThresholdBytes, 80 * 1024},
		{"SandboxTimeoutBufferMs", SandboxTimeoutBufferMs, 30_000},
		{"StopMonitorPollIntervalMs", StopMonitorPollIntervalMs, 150},
		{"SkillsCacheTTL", SkillsCacheTTL, 4 * time.Hour},
		{"DefaultReasoningLevel", DefaultReasoningLevel, "default"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestDefaults_Sanity(t *testing.T) {
	if SubagentMaxSteps <= MainAgentMaxSteps {
		t.Errorf("SubagentMaxSteps (%d) should be >= MainAgentMaxSteps (%d); subagents need more room",
			SubagentMaxSteps, MainAgentMaxSteps)
	}
	if SandboxTimeoutBufferMs < 5_000 {
		t.Errorf("SandboxTimeoutBufferMs (%d) too tight; need breathing room for snapshot", SandboxTimeoutBufferMs)
	}
	if StopMonitorPollIntervalMs > 500 {
		t.Errorf("StopMonitorPollIntervalMs (%d) too slow; user-visible stop latency", StopMonitorPollIntervalMs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./pkg/agent/ -run TestDefaults -v`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the implementation**

```go
// server/pkg/agent/defaults.go

// Package-level defaults for agent execution.
//
// Central registry of tunables used across Phases 2-12. Values are sourced from
// the Foundation Libraries & Architectural Decisions section of PLAN.md (D7).
// When adding a new tunable, update PLAN.md and add it here — NOT inline in the
// phase code.
package agent

import "time"

// Agent-loop step limits.
//
// Reflect go-ai's ToolLoopAgent StopWhen semantics: an agent stops at
// StepCountIs(N). Subagents get more headroom because they run to completion
// in one shot (no user iteration).
const (
	// MainAgentMaxSteps caps tool-call rounds in a primary agent turn.
	MainAgentMaxSteps = 50

	// SubagentMaxSteps caps tool-call rounds inside a task-tool-invoked
	// subagent. Higher than MainAgentMaxSteps because subagents cannot ask
	// follow-up questions and must complete their instructions.
	SubagentMaxSteps = 100
)

// Context compression threshold. Reflects Open Agents' EVICTION_THRESHOLD_BYTES.
// ~20K tokens at 4 chars/token.
const ContextEvictionThresholdBytes = 80 * 1024

// Sandbox / long-running-backend timeout buffer.
// Fire OnTimeout this many ms before the provider's hard session cap so the
// agent can snapshot state and exit cleanly.
const SandboxTimeoutBufferMs = 30_000

// Workflow stop-monitor poll interval (Phase 7). Keeps user-visible cancel
// latency under a quarter-second without pounding Postgres.
const StopMonitorPollIntervalMs = 150

// Skills cache TTL (Phase 3 Redis cache). Skills discovery is expensive; 4
// hours matches Open Agents' skills-cache TTL.
const SkillsCacheTTL = 4 * time.Hour

// DefaultReasoningLevel resolves to go-ai's ReasoningDefault (provider-chosen)
// when no workspace/agent override is set. Exposed as a string so callers can
// pass it to go-ai without importing provider types here.
const DefaultReasoningLevel = "default"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./pkg/agent/ -run TestDefaults -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/defaults.go server/pkg/agent/defaults_test.go
git commit -m "feat(agent): add central defaults.go (D7)

Codifies MainAgentMaxSteps, SubagentMaxSteps,
ContextEvictionThresholdBytes, SandboxTimeoutBufferMs,
StopMonitorPollIntervalMs, SkillsCacheTTL, DefaultReasoningLevel.
Every later phase references these instead of hardcoding magic numbers."
```

---

## Phase Plan Index

This is Phase 1 of 12. Remaining phases to be planned. Effort numbers reflect the D1–D7 decisions (see `PLAN.md` § Foundation Libraries): go-ai replaces hand-rolled harness work in Phase 2, Phase 6 loses the delegation tables/service/CLI entirely via the `task` tool, Phase 5 reuses go-ai's context-management primitives.

| Phase | Name | Depends On | Estimated Effort |
|-------|------|-----------|-----------------|
| **1** | **Agent Type System & Database Foundation (incl. defaults.go + go-ai fork)** | **—** | **2.5 weeks** |
| 2 | Harness (go-ai adapter) + LLM API + Tools + Router + Worker + Backend timeout hooks | Phase 1 | 2.5 weeks |
| 3 | MCP Integration + Skills Registry | Phase 2 | 1.5 weeks |
| 4 | Structured Outputs + Guardrails + Knowledge/RAG | Phase 2 | 2 weeks |
| 5 | Agent Memory + Context Compression (go-ai primitives) | Phases 2, 4 | 2 weeks |
| 6 | A2A via `task` tool / SubagentRegistry (no tables) | Phases 1, 2, 4, 5 | 1 week |
| 7 | Workflow Orchestration + Scheduler (+ 150ms stop-monitor) | Phases 4, 6 | 3 weeks |
| 8A | Platform Integration | Phases 2, 7 | 4 weeks |
| 8B | Security & Compliance | Phases 1, 2 | 4 weeks |
| 9 | Advanced Cost Optimization | Phases 2, 5 | 3 weeks |
| 10 | Observability & Tracing | Phases 6, 7 | 2 weeks |
| 11 | Cloud Coding Sandbox (E2B + gVisor) | Phases 2, 10 | 3 weeks |
| 12 | Claude Managed Agents Backend | Phases 2, 10 | 2 weeks |

**Parallelization:** After Phase 2, Phases 3 and 4 can run in parallel. Phase 8B can start after Phase 2. Phase 9 runs in parallel with Phases 5-8. See `PLAN.md` dependency graph for details.
