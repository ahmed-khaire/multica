# Phase 5: Agent Memory System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agents accumulate experiential memories across tasks, share them across a workspace, and compress long conversation contexts automatically — so multi-task sessions stay within model windows and organizational learning compounds.

**Architecture:** Three cooperating subsystems layered on Phase 1/2/3/4 primitives. (1) **MemoryService** runs a write pipeline (extract → infer → embed → dedup → consolidate → persist) and a read pipeline (embed → vector search → composite score → access update), keyed on the workspace's configured embedding dimension via the same dim-partition pattern Phase 4 established (1024/1536/3072). (2) **Memory tools + worker integration** inject recalled memories as a cacheable prefix before each task and queue Remember calls on a bounded background pool after each task. (3) **Context compaction** reduces mid-execution conversations past the eviction threshold using go-ai's Anthropic primitives where available, and a pure-Go summarise-and-truncate fallback for non-Anthropic providers.

**Tech Stack:** Go 1.26, `github.com/digitallysavvy/go-ai/pkg/agent` (harness + Anthropic context-management), `github.com/digitallysavvy/go-ai/pkg/provider` (LanguageModel), `github.com/pgvector/pgvector-go`, PostgreSQL + pgvector, Phase 1 `BudgetService` (cost attribution only — memory writes never block the parent task), Phase 1 `Embedder` + `DimPartitionRepo` helpers, Phase 2 `harness` + D8 `trace_id` context keys, Phase 4 dim-partition store pattern, TypeScript (React, TanStack Query, Zustand, shadcn/Base UI).

---

## Scope Note

**In scope (PLAN.md §5.1–§5.7):**
- Migration 130: dim-partitioned `agent_memory_{1024,1536,3072}` tables with scope CHECK constraints + workspace `memory_mode` setting.
- `MemoryService` with `Remember(ctx, raw, scope)` and `Recall(ctx, query, scope, limit)`. Remember runs a two-stage micro-tier LLM pipeline (extract atomic memories → infer scope/categories/importance) plus intra-batch dedup and consolidation. Recall applies composite scoring (`0.5*similarity + 0.3*recency + 0.2*importance` with 30-day half-life).
- Bounded worker pool (max 10 concurrent, buffered channel capacity 100) for async Remember; drop-with-warning on overflow; metrics counters.
- Three `memory_mode` settings: `full` (default — LLM-analyzed), `lite` (embed-only), `off` (manual tools only).
- `recall_memory` and `save_memory` built-in tools so agents can use memory during execution.
- Worker pre-task memory injection into the system prompt as an Anthropic-cacheable prefix.
- Worker post-task async Remember.
- `server/pkg/agent/compaction.go` with a provider-dispatching `Compact(ctx, provider, messages)` entry point, Anthropic-only primitives via go-ai, non-Anthropic summarise-and-truncate fallback, paste-block token substitution, and archival write-back to `agent_memory`.
- HTTP handlers: list / semantic-search / delete memories.
- CLI: `multica memory recall|save`.
- Frontend `memory-tab.tsx` on the agent detail page.

**Three-tier mapping (PLAN.md §Phase 5 explicit deliverable):** Letta/MemGPT's core/working/archival taxonomy is implemented as a **convention over the scope hierarchy**, not as a separate paging mechanism. This satisfies the PLAN.md line 916 requirement without introducing an explicit tier column:

| Tier | Scope prefix | Semantics |
|---|---|---|
| **core** | `/ws/{wsId}/agent/{agentId}/core/` | High-`importance` (≥0.8) identity / role memories injected unconditionally at task start. |
| **working** | `/ws/{wsId}/agent/{agentId}/working/` | Current-task scratchpad; recalled first, capped at `MemoryRecallTopK/2`. |
| **archival** | `/ws/{wsId}/agent/{agentId}/archival/` | Long-term log; recalled only via semantic search. Default tier when `save_memory` omits a tier. |

The Remember pipeline's scope-inference LLM call classifies new memories into one of the three tiers; Recall applies tier-aware weighting in the composite score (core always included; working boosted by 1.2× similarity; archival unmodified). **Paging to/from provider-held context (MemGPT's primary abstraction) is explicitly deferred to Phase 9 if cost drives it** — this plan ships the taxonomy and behavioural split without paging.

**Deferred:**
- **MemGPT-style provider-context paging** (swap core in, archival out based on token pressure). Scope-tier split ships now; paging lands with Phase 9 cost optimisation.

**Trigger-threshold note:** PLAN.md §5.7 verification says "Context compression triggers at 70% threshold". The implementation uses the fixed `ContextEvictionThresholdBytes = 80 KB` (from D7 / `defaults.go`), which corresponds to ~20K tokens. For a 200K-token Anthropic window this is ~10% — aggressive by design so the summariser has headroom. For an 8K-token OpenAI model this is ~250% — the compaction fires long before the 70% mark. The 70% figure in PLAN.md is a provider-window-relative heuristic; the plan intentionally ships the byte-based constant (stable across providers, independent of per-model context size, cheap to compute) and leaves the percentage-of-window calculation as a Phase 9 refinement when real cost telemetry tells us whether it matters.
- **Memory editing UI beyond delete.** Frontend supports browse, search, delete; manual edit lands later.
- **Cross-workspace memory sharing.** Not a requirement — `CHECK (scope LIKE '/ws/' || workspace_id::text || '/%')` forbids it at the DB layer.
- **Vector dimension migrations.** Re-embedding is an admin procedure (see Phase 1 §1.5); Phase 5 does not expose it.
- **Fine-grained memory ACLs.** `private BOOLEAN` gates author-only visibility; anything more nuanced is post-V1.

## File Structure

### New Files

| File | Responsibility |
|---|---|
| `server/migrations/130_agent_memory.up.sql` | Dim-partitioned `agent_memory_{1024,1536,3072}`, scope CHECK constraints via NOT VALID / VALIDATE, vector indexes (ivfflat ≤2000 dims, hnsw for 3072), `memory_mode` column on `workspace` |
| `server/migrations/130_agent_memory.down.sql` | DROP INDEX / TABLE IF EXISTS in reverse order |
| `server/pkg/db/queries/memory.sql` | sqlc: `InsertMemory{1024,1536,3072}`, `SearchMemory{1024,1536,3072}` (nullable `viewer_agent_id` via `sqlc.narg`), `FindSimilarMemory{1024,1536,3072}`, `UpdateMemoryAccess{1024,1536,3072}`, `UpdateMemoryContent{1024,1536,3072}`, `DeleteMemory{1024,1536,3072}`, `ListMemoriesByAgent{1024,1536,3072}`, `CountMemoriesByAgent{1024,1536,3072}`, `GroupCategoriesByAgent{1024,1536,3072}`, `GetMemoryMode`, `UpdateMemoryMode` |
| `server/internal/service/memory.go` | `MemoryService` with `Remember(ctx, raw, scope)` + `Recall(ctx, query, scope, limit)` — orchestrates extract → infer → embed → dedup → consolidate → persist and embed → search → score → access-update |
| `server/internal/service/memory_extract.go` | Cheap-LLM pipeline: `ExtractAtomicMemories(ctx, raw) []string` + `AnalyzeMemory(ctx, text) Analysis` |
| `server/internal/service/memory_consolidate.go` | Consolidation LLM call — decides `keep / update / delete / insert_new` for near-duplicate matches |
| `server/internal/service/memory_score.go` | Composite scoring: `Score(sim, ageDays, importance) float64` with 30-day half-life |
| `server/internal/service/memory_pool.go` | Bounded worker pool + buffered channel + metrics (`memory_writes_queued / completed / dropped`) |
| `server/internal/service/memory_test.go` | Unit tests for scoring, scope sanitization, dedup threshold, pool backpressure |
| `server/internal/service/memory_integration_test.go` | Live-DB integration: Agent A writes, Agent B recalls; `private=true` excluded; consolidation merges duplicates |
| `server/pkg/tools/builtin/memory_tools.go` | `RecallMemoryTool()` + `SaveMemoryTool()` → `types.Tool` with JSON Schema + executors that call MemoryService |
| `server/pkg/tools/builtin/memory_tools_test.go` | Tool schema + executor tests with a stub MemoryService |
| `server/pkg/agent/compaction.go` | `Compact(ctx, provider, messages, opts) (CompactResult, error)` — dispatches to Anthropic or fallback path; paste-block expansion; archival write-back |
| `server/pkg/agent/compaction_anthropic.go` | Anthropic path using `ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit` from go-ai |
| `server/pkg/agent/compaction_fallback.go` | Pure-Go summarise-and-truncate for OpenAI / Google / xAI / Ollama |
| `server/pkg/agent/paste_blocks.go` | Paste-block token substitution (Unicode PUA 0xE000–0xF8FF) + server-side expansion |
| `server/pkg/agent/compaction_test.go` | Unit tests for threshold, protected ranges, paste-block round-trip, and per-provider dispatch |
| `server/internal/handler/memory.go` | HTTP: `GET /memories`, `POST /memories` (save), `POST /memories/search`, `GET /memories/stats`, `DELETE /memories/{id}` — all under `/workspaces/{wsId}/agents/{agentId}/` |
| `server/internal/handler/memory_test.go` | Handler tests using the shared `handlerTestCtx` harness |
| `server/cmd/multica/cmd_memory.go` | CLI: `multica memory recall <query>`, `multica memory save <content>` |
| `packages/views/agents/components/tabs/memory-tab.tsx` | Agent-detail Memory tab: browse / search / delete / stats |
| `packages/views/agents/components/tabs/memory-tab.test.tsx` | Vitest + Testing Library |
| `e2e/tests/phase5-memory-sharing.spec.ts` | Two-agent cross-recall flow with `stubAnthropic` + `stubEmbedder` (from Phase 4 helpers) |

### Modified Files

| File | Change |
|---|---|
| `server/internal/worker/worker.go` | Pre-task: call `memory.Recall` + inject into system prompt as cacheable prefix. Post-task: enqueue `memory.Remember` on the pool |
| `server/pkg/agent/harness/harness.go` | Invoke `compaction.Compact` when `len(messages) * avgBytesPerMsg >= ContextEvictionThresholdBytes` |
| `server/cmd/worker/main.go` | Wire `MemoryService` + `MemoryPool` into worker construction |
| `server/cmd/server/router.go` | Mount memory handler routes under `/workspaces/{wsId}/agents/{agentId}/memories` |
| `server/pkg/agent/defaults.go` | Add `RecentProtectBytes`, `MemoryRecallTopK`, `MemoryRecencyHalfLifeDays`, `MemoryWorkerPoolSize`, `MemoryWorkerQueueCap`, `MemoryCompositeWeights` (per D7) |
| `packages/core/types/agent.ts` | Add `Memory`, `MemoryMode`, `MemoryStats` types |
| `packages/core/memory/queries.ts` | NEW — TanStack Query hooks (`useAgentMemories`, `useAgentMemoryStats`, `useDeleteMemory`) |
| `packages/core/memory/ws.ts` | NEW — subscribes to `memory.created` / `memory.deleted` and invalidates queries (CoreProvider wires on mount) |
| `packages/views/agents/components/agent-detail.tsx` | Add Memory tab next to Tools / Knowledge |

### External Infrastructure

| System | Change |
|---|---|
| PostgreSQL | pgvector already present (Phase 1, Phase 4). `agent_memory_{1024,1536}` use `ivfflat`; `agent_memory_3072` uses `hnsw`. Same CI smoke test harness as Phase 4 Task 2.5 covers Phase 5 partitions too. |
| LLM provider budget | Memory writes use the workspace's configured micro-tier model (Haiku / gpt-4o-mini). They emit `cost_event` rows tagged with the parent task's `trace_id` (D8) but do NOT call `BudgetService.CanDispatch` — they're best-effort enhancements and must never block the parent task. |

---

## Cross-Phase Contracts Consumed Here

The snippets in this plan assume the following interfaces already exist from earlier phases. If any diverge from reality at implementation time, raise it before touching Phase 5 code — do not silently adapt.

**From Phase 1 (`server/pkg/embeddings/embeddings.go`):**

```go
// Embedder produces vector embeddings and reports its output dimension. The
// dim return is authoritative per-call — MemoryService never caches it.
type Embedder interface {
    // Embed returns one vector per input, plus the embedding dimension.
    Embed(ctx context.Context, inputs []string) (vectors [][]float32, dims int, err error)
}
```

*If Phase 1 shipped a two-return `Embed(ctx, inputs) ([][]float32, error)` signature, Phase 5 adds a migration step at the top of Task 3: extend the interface to return `dims`, and update every existing caller. Do NOT wrap the two-return signature — it hides the per-workspace dim switch.*

**From Phase 2 (`server/pkg/agent/harness/language_model.go`):**

```go
type LanguageModel interface {
    DoGenerate(ctx context.Context, opts *GenerateOptions) (*GenerateResult, error)
}
type GenerateOptions struct {
    Prompt      string
    System      string
    Temperature float64
    MaxTokens   int
    Tools       []types.Tool     // optional; nil for micro-tier analyse calls
}
type GenerateResult struct {
    Text       string
    Usage      Usage
    CostEventID uuid.UUID // populated when the wrapper records a cost_event
}
```

All micro-tier LLM calls in Phase 5 (`extractAtomicMemories`, `analyzeMemory`, `consolidate`) use `LanguageModel.DoGenerate` and read `.Text`.

**From Phase 1 infra:**

```go
type WSPublisher interface {
    // Publish fans the event out to every websocket client subscribed to
    // wsID. payload is JSON-marshalled to the wire.
    Publish(wsID uuid.UUID, event string, payload any)
}
```

Concrete type: `*websocket.Hub` from `server/internal/ws/` (Phase 1 already exposes it; Phase 5 only consumes the interface so tests can inject a fake).

**Handler struct `DB` / `Logger` concrete types:** `db *dbgen.Queries` (where `dbgen` is the sqlc-generated package imported as `db "aicolab/server/pkg/db/generated"`) and `logger *slog.Logger`. The plan writes them as `DB` / `Logger` for readability; implementers substitute the concrete types at file-header import time. The shared `handlerTestCtx` in `server/internal/handler/testctx_test.go` already wires them.

**Execution ordering note:** Task 11 Step 0 appends constants (`MemoryCompositeWeights`, `MemoryRecallTopK`, `MemoryRecencyHalfLifeDays`, `MemoryWorkerPoolSize`, `MemoryWorkerQueueCap`, `RecentProtectBytes`) to `server/pkg/agent/defaults.go`. These constants are consumed by Tasks 6 (`Score`), 7 (pool defaults), and 11–12 (compaction). **Execute Task 11 Step 0 first**, before Task 6, so every subsequent task compiles green on first run. An equivalent Step -1 pre-task commit at the top of Task 3 is also acceptable.

---

### Task 1: Migration 130 — `agent_memory_{1024,1536,3072}` + `workspace.memory_mode`

**Files:**
- Create: `server/migrations/130_agent_memory.up.sql`
- Create: `server/migrations/130_agent_memory.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- server/migrations/130_agent_memory.up.sql
-- Phase 5: dim-partitioned experiential memory. Mirrors Phase 4 knowledge_chunk
-- pattern per PLAN.md §1.5: one physical table per supported dimension.
-- Scope CHECK constraints at the DB layer (R3 S2) prevent cross-workspace
-- forgery even if the service layer is bypassed by a future handler bug.
-- Every statement IF NOT EXISTS (PLAN.md §1.7). CHECK constraints via
-- DROP → ADD NOT VALID → VALIDATE so initial data load doesn't block (§1.1).

-- memory_mode lives on workspace, not per-agent, so admins can disable LLM
-- extraction globally without touching every agent. Default 'full'.
-- Prerequisite for the composite FK below: agent must be uniquely keyed on
-- (workspace_id, id). Phase 1 migration 100 already creates this index
-- (`CREATE UNIQUE INDEX idx_agent_workspace_id ON agent(workspace_id, id)`
-- — PLAN.md §1.1). We depend on that migration having run; no redundant
-- index is created here. If Phase 1 is rolled back this migration will fail
-- at the FK step with a clear error — that's the desired behaviour.

ALTER TABLE workspace ADD COLUMN IF NOT EXISTS memory_mode TEXT NOT NULL DEFAULT 'full';
ALTER TABLE workspace DROP CONSTRAINT IF EXISTS workspace_memory_mode_check;
ALTER TABLE workspace ADD CONSTRAINT workspace_memory_mode_check
    CHECK (memory_mode IN ('full', 'lite', 'off')) NOT VALID;
ALTER TABLE workspace VALIDATE CONSTRAINT workspace_memory_mode_check;

-- Base 1024 table; 1536 and 3072 copy its non-vector shape via LIKE.
CREATE TABLE IF NOT EXISTS agent_memory_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,

    content TEXT NOT NULL,
    embedding vector(1024) NOT NULL,

    -- CrewAI-style hierarchical namespaces. Scope MUST start with the
    -- caller's workspace root; forged scopes are rejected at the DB layer.
    scope TEXT NOT NULL,
    categories TEXT[] NOT NULL DEFAULT '{}',

    importance DECIMAL NOT NULL DEFAULT 0.5
        CHECK (importance >= 0.0 AND importance <= 1.0),

    -- Composite FK ensures the source agent belongs to the same workspace
    -- (R3 S1). Nullable source_agent_id supports manual / CLI-written memories.
    source_agent_id UUID,
    source_task_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}',
    private BOOLEAN NOT NULL DEFAULT false,

    trace_id UUID NOT NULL,  -- D8: every row attributes to a trace chain

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    access_count INT NOT NULL DEFAULT 0,

    FOREIGN KEY (workspace_id, source_agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);

ALTER TABLE agent_memory_1024 DROP CONSTRAINT IF EXISTS agent_memory_1024_scope_ws;
ALTER TABLE agent_memory_1024 ADD CONSTRAINT agent_memory_1024_scope_ws
    CHECK (scope LIKE '/ws/' || workspace_id::text || '/%') NOT VALID;
ALTER TABLE agent_memory_1024 VALIDATE CONSTRAINT agent_memory_1024_scope_ws;

-- NOTE on idempotency: `LIKE ... INCLUDING ALL` copies CHECK constraints
-- from agent_memory_1024, so agent_memory_1536/3072 arrive with a clone of
-- agent_memory_1024_scope_ws named identically-under-its-new-table. The
-- DROP IF EXISTS → ADD NOT VALID → VALIDATE sequence that follows is:
--   - idempotent insurance against re-runs (drops previous-run constraint);
--   - explicit about the validation state (NOT VALID then VALIDATE so the
--     next batch-insert doesn't stall behind a synchronous constraint
--     validation on an empty-but-about-to-be-populated table).
-- It looks redundant on a fresh database; keep it for replay safety.
CREATE TABLE IF NOT EXISTS agent_memory_1536 (LIKE agent_memory_1024 INCLUDING ALL);
ALTER TABLE agent_memory_1536 ALTER COLUMN embedding TYPE vector(1536);
ALTER TABLE agent_memory_1536 DROP CONSTRAINT IF EXISTS agent_memory_1536_scope_ws;
ALTER TABLE agent_memory_1536 ADD CONSTRAINT agent_memory_1536_scope_ws
    CHECK (scope LIKE '/ws/' || workspace_id::text || '/%') NOT VALID;
ALTER TABLE agent_memory_1536 VALIDATE CONSTRAINT agent_memory_1536_scope_ws;

CREATE TABLE IF NOT EXISTS agent_memory_3072 (LIKE agent_memory_1024 INCLUDING ALL);
ALTER TABLE agent_memory_3072 ALTER COLUMN embedding TYPE vector(3072);
ALTER TABLE agent_memory_3072 DROP CONSTRAINT IF EXISTS agent_memory_3072_scope_ws;
ALTER TABLE agent_memory_3072 ADD CONSTRAINT agent_memory_3072_scope_ws
    CHECK (scope LIKE '/ws/' || workspace_id::text || '/%') NOT VALID;
ALTER TABLE agent_memory_3072 VALIDATE CONSTRAINT agent_memory_3072_scope_ws;

-- Vector indexes: ivfflat ≤2000 dims; hnsw for 3072.
CREATE INDEX IF NOT EXISTS idx_mem_1024_vec ON agent_memory_1024
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_mem_1536_vec ON agent_memory_1536
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_mem_3072_vec ON agent_memory_3072
    USING hnsw (embedding vector_cosine_ops);

-- Scope + workspace lookups use the text_pattern_ops opclass so LIKE prefix
-- queries use an index (essential for Recall's scope-prefix filter).
CREATE INDEX IF NOT EXISTS idx_mem_1024_scope ON agent_memory_1024 (workspace_id, scope text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mem_1536_scope ON agent_memory_1536 (workspace_id, scope text_pattern_ops);
CREATE INDEX IF NOT EXISTS idx_mem_3072_scope ON agent_memory_3072 (workspace_id, scope text_pattern_ops);

-- Agent-filtered list queries (memory-tab UI).
CREATE INDEX IF NOT EXISTS idx_mem_1024_agent ON agent_memory_1024 (workspace_id, source_agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_1536_agent ON agent_memory_1536 (workspace_id, source_agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_3072_agent ON agent_memory_3072 (workspace_id, source_agent_id, created_at DESC);
```

- [ ] **Step 2: Write the down migration**

```sql
-- server/migrations/130_agent_memory.down.sql
DROP INDEX IF EXISTS idx_mem_3072_agent;
DROP INDEX IF EXISTS idx_mem_1536_agent;
DROP INDEX IF EXISTS idx_mem_1024_agent;
DROP INDEX IF EXISTS idx_mem_3072_scope;
DROP INDEX IF EXISTS idx_mem_1536_scope;
DROP INDEX IF EXISTS idx_mem_1024_scope;
DROP INDEX IF EXISTS idx_mem_3072_vec;
DROP INDEX IF EXISTS idx_mem_1536_vec;
DROP INDEX IF EXISTS idx_mem_1024_vec;
DROP TABLE IF EXISTS agent_memory_3072;
DROP TABLE IF EXISTS agent_memory_1536;
DROP TABLE IF EXISTS agent_memory_1024;
ALTER TABLE workspace DROP CONSTRAINT IF EXISTS workspace_memory_mode_check;
ALTER TABLE workspace DROP COLUMN IF EXISTS memory_mode;
```

- [ ] **Step 3: Apply + verify**

Run: `cd server && make migrate-up`
Expected: migration 130 applied; `\d agent_memory_1024` in psql shows the constraint + indexes.

Run: `cd server && make migrate-down` then `make migrate-up`
Expected: both pass — migrations are idempotent.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/130_agent_memory.up.sql server/migrations/130_agent_memory.down.sql
git commit -m "feat(db): migration 130 — dim-partitioned agent_memory + memory_mode"
```

---

### Task 2: sqlc queries — per-dim insert/search/consolidation + memory_mode

**Files:**
- Create: `server/pkg/db/queries/memory.sql`

- [ ] **Step 1: Write the queries**

```sql
-- server/pkg/db/queries/memory.sql
-- Per-dim insert / search / consolidation (Phase 4 pattern — the Go-side
-- routing helper picks the partition based on workspace_embedding_config).

-- name: InsertMemory1024 :one
INSERT INTO agent_memory_1024 (
    workspace_id, content, embedding, scope, categories,
    importance, source_agent_id, source_task_id, metadata, private, trace_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, created_at;

-- name: InsertMemory1536 :one
INSERT INTO agent_memory_1536 (
    workspace_id, content, embedding, scope, categories,
    importance, source_agent_id, source_task_id, metadata, private, trace_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, created_at;

-- name: InsertMemory3072 :one
INSERT INTO agent_memory_3072 (
    workspace_id, content, embedding, scope, categories,
    importance, source_agent_id, source_task_id, metadata, private, trace_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id, created_at;

-- Recall: scope-prefix filter + vector-ordered top-N.
--
-- Privacy filter: `viewer_agent_id` is a nullable UUID (sqlc.narg generates
-- `pgtype.UUID`). When the service calls Recall without a viewer (admin / CLI
-- view), the Go side passes `pgtype.UUID{Valid: false}` and the `IS NULL`
-- branch skips the private filter entirely. This closes the uuid.Nil=valid-UUID
-- footgun: uuid.Nil is a valid non-NULL UUID so the previous
-- `source_agent_id = $4` form would have matched zero private rows instead of
-- the intended "all private rows" for the admin case.
--
-- name: SearchMemory1024 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_1024
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (NOT private
       OR sqlc.narg('viewer_agent_id')::uuid IS NULL
       OR source_agent_id = sqlc.narg('viewer_agent_id')::uuid)
ORDER BY embedding <=> $2
LIMIT $4;

-- name: SearchMemory1536 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_1536
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (NOT private
       OR sqlc.narg('viewer_agent_id')::uuid IS NULL
       OR source_agent_id = sqlc.narg('viewer_agent_id')::uuid)
ORDER BY embedding <=> $2
LIMIT $4;

-- name: SearchMemory3072 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_3072
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (NOT private
       OR sqlc.narg('viewer_agent_id')::uuid IS NULL
       OR source_agent_id = sqlc.narg('viewer_agent_id')::uuid)
ORDER BY embedding <=> $2
LIMIT $4;

-- Consolidation: given a candidate new memory, find existing rows at cosine >= $4.
-- name: FindSimilarMemory1024 :many
SELECT id, content, scope, categories, importance,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_1024
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (1 - (embedding <=> $2)) >= $4
ORDER BY embedding <=> $2
LIMIT 5;

-- name: FindSimilarMemory1536 :many
SELECT id, content, scope, categories, importance,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_1536
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (1 - (embedding <=> $2)) >= $4
ORDER BY embedding <=> $2
LIMIT 5;

-- name: FindSimilarMemory3072 :many
SELECT id, content, scope, categories, importance,
       1 - (embedding <=> $2) AS similarity
FROM agent_memory_3072
WHERE workspace_id = $1
  AND scope LIKE $3 || '%'
  AND (1 - (embedding <=> $2)) >= $4
ORDER BY embedding <=> $2
LIMIT 5;

-- Access bookkeeping (called per returned row on Recall). Three per-dim
-- variants because sqlc compiles one function per statement.
-- name: UpdateMemoryAccess1024 :exec
UPDATE agent_memory_1024
SET last_accessed_at = NOW(), access_count = access_count + 1
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateMemoryAccess1536 :exec
UPDATE agent_memory_1536
SET last_accessed_at = NOW(), access_count = access_count + 1
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateMemoryAccess3072 :exec
UPDATE agent_memory_3072
SET last_accessed_at = NOW(), access_count = access_count + 1
WHERE id = $1 AND workspace_id = $2;

-- Consolidation UPDATE (merge content / importance / categories).
-- name: UpdateMemoryContent1024 :exec
UPDATE agent_memory_1024
SET content = $3, embedding = $4, importance = $5, categories = $6, metadata = $7
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateMemoryContent1536 :exec
UPDATE agent_memory_1536
SET content = $3, embedding = $4, importance = $5, categories = $6, metadata = $7
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateMemoryContent3072 :exec
UPDATE agent_memory_3072
SET content = $3, embedding = $4, importance = $5, categories = $6, metadata = $7
WHERE id = $1 AND workspace_id = $2;

-- Delete (consolidation supersede + handler DELETE).
-- name: DeleteMemory1024 :exec
DELETE FROM agent_memory_1024 WHERE id = $1 AND workspace_id = $2;

-- name: DeleteMemory1536 :exec
DELETE FROM agent_memory_1536 WHERE id = $1 AND workspace_id = $2;

-- name: DeleteMemory3072 :exec
DELETE FROM agent_memory_3072 WHERE id = $1 AND workspace_id = $2;

-- List for memory-tab UI: chronologically newest-first, filtered by agent.
-- name: ListMemoriesByAgent1024 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count
FROM agent_memory_1024
WHERE workspace_id = $1 AND (source_agent_id = $2 OR $2 IS NULL)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListMemoriesByAgent1536 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count
FROM agent_memory_1536
WHERE workspace_id = $1 AND (source_agent_id = $2 OR $2 IS NULL)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListMemoriesByAgent3072 :many
SELECT id, content, scope, categories, importance, source_agent_id,
       source_task_id, metadata, private, created_at, last_accessed_at, access_count
FROM agent_memory_3072
WHERE workspace_id = $1 AND (source_agent_id = $2 OR $2 IS NULL)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- memory_mode helpers.
-- name: GetMemoryMode :one
SELECT memory_mode FROM workspace WHERE id = $1;

-- name: UpdateMemoryMode :exec
UPDATE workspace SET memory_mode = $2 WHERE id = $1;

-- Stats aggregates: per-agent counts grouped by tier + top categories.
-- The three-tier (core/working/archival) classification lives as a convention
-- on the scope-path prefix (see Scope Note):
--   /ws/{wsId}/agent/{agentId}/core/…      → core
--   /ws/{wsId}/agent/{agentId}/working/…   → working
--   /ws/{wsId}/agent/{agentId}/archival/…  → archival
-- $2 = the agent's scope root (e.g. "/ws/{wsId}/agent/{agentId}/") so the
-- FILTER predicates can suffix-match each tier. MemoryService.Stats (Task 14)
-- builds $2 from `SanitizeScope(wsID, "agent/"+agentID.String()+"/")` so the
-- query cannot be tricked into matching a foreign workspace.

-- name: CountMemoriesByAgent1024 :one
SELECT
    COUNT(*)                                                     AS total,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'core/%')            AS core,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'working/%')         AS working,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'archival/%')        AS archival,
    MAX(created_at)                                              AS last_written_at
FROM agent_memory_1024
WHERE workspace_id = $1
  AND scope LIKE $2 || '%';

-- name: CountMemoriesByAgent1536 :one
SELECT
    COUNT(*)                                                     AS total,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'core/%')            AS core,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'working/%')         AS working,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'archival/%')        AS archival,
    MAX(created_at)                                              AS last_written_at
FROM agent_memory_1536
WHERE workspace_id = $1
  AND scope LIKE $2 || '%';

-- name: CountMemoriesByAgent3072 :one
SELECT
    COUNT(*)                                                     AS total,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'core/%')            AS core,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'working/%')         AS working,
    COUNT(*) FILTER (WHERE scope LIKE $2 || 'archival/%')        AS archival,
    MAX(created_at)                                              AS last_written_at
FROM agent_memory_3072
WHERE workspace_id = $1
  AND scope LIKE $2 || '%';

-- Top categories per agent (unnest the TEXT[] then group+sort).
-- Limit 10 is enough for the memory-tab badge strip; the handler truncates
-- to 3 for display but keeps 10 on the wire so the UI can hover-expand.

-- name: GroupCategoriesByAgent1024 :many
SELECT cat AS category, COUNT(*)::bigint AS count
FROM agent_memory_1024, unnest(categories) AS cat
WHERE workspace_id = $1
  AND scope LIKE $2 || '%'
GROUP BY cat
ORDER BY count DESC, cat ASC
LIMIT 10;

-- name: GroupCategoriesByAgent1536 :many
SELECT cat AS category, COUNT(*)::bigint AS count
FROM agent_memory_1536, unnest(categories) AS cat
WHERE workspace_id = $1
  AND scope LIKE $2 || '%'
GROUP BY cat
ORDER BY count DESC, cat ASC
LIMIT 10;

-- name: GroupCategoriesByAgent3072 :many
SELECT cat AS category, COUNT(*)::bigint AS count
FROM agent_memory_3072, unnest(categories) AS cat
WHERE workspace_id = $1
  AND scope LIKE $2 || '%'
GROUP BY cat
ORDER BY count DESC, cat ASC
LIMIT 10;
```

- [ ] **Step 2: Regenerate sqlc**

Run: `cd server && make sqlc`
Expected: `server/pkg/db/db/memory.sql.go` generated with all 27 functions
(21 originals + 6 new Stats aggregates).

- [ ] **Step 3: Commit**

```bash
git add server/pkg/db/queries/memory.sql server/pkg/db/db/memory.sql.go
git commit -m "feat(db): sqlc queries for dim-partitioned agent_memory + memory_mode"
```

---

### Task 3: MemoryService skeleton + scope sanitizer

**Files:**
- Create: `server/internal/service/memory.go`
- Create: `server/internal/service/memory_test.go`

**Goal:** the `MemoryService` type + scope-sanitization helper. Both `Remember` and `Recall` rewrite the scope prefix from the authenticated workspace every call — never trusting the string a caller passes (R3 S2). The DB CHECK is belt-and-suspenders.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/memory_test.go
package service

import (
    "testing"

    "github.com/google/uuid"
)

func TestSanitizeScope_RewritesPrefix(t *testing.T) {
    wsID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
    cases := []struct {
        in   string
        want string
    }{
        // No scope → workspace root.
        {"", "/ws/11111111-1111-1111-1111-111111111111/"},
        // Relative suffix → prefix with ws root.
        {"agent/abc/", "/ws/11111111-1111-1111-1111-111111111111/agent/abc/"},
        // Attacker-supplied absolute scope → rewritten under our wsID.
        {"/ws/22222222-2222-2222-2222-222222222222/agent/leak/", "/ws/11111111-1111-1111-1111-111111111111/agent/leak/"},
        // Trailing-slash consistency.
        {"agent/abc", "/ws/11111111-1111-1111-1111-111111111111/agent/abc/"},
    }
    for _, tc := range cases {
        got := SanitizeScope(wsID, tc.in)
        if got != tc.want {
            t.Errorf("SanitizeScope(%q) = %q, want %q", tc.in, got, tc.want)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/service/ -run TestSanitizeScope -v`
Expected: FAIL with "undefined: SanitizeScope".

- [ ] **Step 3: Implement**

```go
// server/internal/service/memory.go
package service

import (
    "context"
    "strings"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/agent"
    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/embeddings"
)

// Sentinel errors — used by handlers to map to HTTP status codes.
var (
    ErrMemoryNotFound = errors.New("memory not found")
    ErrScopeForbidden = errors.New("scope outside workspace root")
)

// MemoryService coordinates the Remember + Recall pipelines. It depends on a
// micro-tier LanguageModel (for extract / analyze / consolidate) that the
// caller wires at construction — never the agent's own model, which keeps
// memory overhead at ~$0.001–0.005 per task.
//
// Dimension handling: embeddings.Embedder.Embed MUST return the authoritative
// `dims` value alongside the vectors (signature: `Embed(ctx, texts) (vecs,
// dims, err)`). MemoryService never caches or infers dims; each call re-reads
// from the embedder return so a workspace that rotates its embedding config
// (Phase 1 §1.5) picks up the new dim on the next request without a restart.
// Pool-scoped background jobs get dims the same way — via the embedder return
// inside the job closure — not through a service-level cache.
type MemoryService struct {
    pool       *pgxpool.Pool
    queries    *db.Queries
    embedder   embeddings.Embedder
    microLLM   agent.LanguageModel // provider-adapter — cheap model only
    pool2      *MemoryPool         // background-writer pool (Task 7)
    // DedupCosine is the intra-batch dedup threshold; ConsolidateCosine is the
    // find-similar threshold for per-row consolidation. Defaults match the
    // CrewAI ports (0.98 and 0.85 respectively).
    DedupCosine       float64
    ConsolidateCosine float64
}

func NewMemoryService(pool *pgxpool.Pool, embedder embeddings.Embedder, microLLM agent.LanguageModel, mp *MemoryPool) *MemoryService {
    return &MemoryService{
        pool:              pool,
        queries:           db.New(pool),
        embedder:          embedder,
        microLLM:          microLLM,
        pool2:             mp,
        DedupCosine:       0.98,
        ConsolidateCosine: 0.85,
    }
}

// SanitizeScope guarantees the returned scope starts with "/ws/{wsID}/".
// Called on every Remember / Recall so a caller that supplies a forged
// "/ws/OTHER/..." string cannot read or write across tenants — the DB
// CHECK constraint catches any bypass but scope rewriting keeps correct
// callers from accidentally widening the scope.
func SanitizeScope(wsID uuid.UUID, raw string) string {
    root := "/ws/" + wsID.String() + "/"
    trimmed := strings.TrimSpace(raw)
    if trimmed == "" {
        return root
    }
    // If caller supplied an absolute /ws/... path (possibly for a different
    // workspace), strip its own /ws/<uuid>/ segment and re-root under ours.
    if strings.HasPrefix(trimmed, "/ws/") {
        rest := strings.TrimPrefix(trimmed, "/ws/")
        if slash := strings.Index(rest, "/"); slash >= 0 {
            rest = rest[slash+1:] // drop the foreign wsID segment
        } else {
            rest = ""
        }
        trimmed = rest
    }
    trimmed = strings.TrimPrefix(trimmed, "/")
    if !strings.HasSuffix(trimmed, "/") && trimmed != "" {
        trimmed += "/"
    }
    return root + trimmed
}

// Context keys — mirror Phase 2 agent.WithTraceID convention.
// (no new keys here; Remember/Recall read trace_id via agent.TraceIDFrom.)
var _ = context.Canceled
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/service/ -run TestSanitizeScope -v`
Expected: PASS — all 4 cases.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/memory.go server/internal/service/memory_test.go
git commit -m "feat(memory): MemoryService skeleton + SanitizeScope helper"
```

---

### Task 4: Remember pipeline — extract + analyze (micro-tier LLM)

**Files:**
- Create: `server/internal/service/memory_extract.go`
- Modify: `server/internal/service/memory_test.go`

**Goal:** two cheap-LLM calls that turn raw task output into (a) atomic memory strings and (b) analysis metadata (scope suffix, categories, importance, entity/date/topic metadata). Both return early on empty input. Both tag their `cost_event` rows with the parent task's trace_id.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/service/memory_test.go`:

```go
func TestExtractAtomicMemories_SplitsClaims(t *testing.T) {
    t.Parallel()
    stub := &stubLLM{
        // Model returns a numbered list; extractor splits on newlines and
        // strips the "N. " prefix.
        text: "1. Tenant requested rent reduction.\n2. Landlord agreed to $50 off.\n3. Next review in 6 months.",
    }
    out, err := extractAtomicMemories(context.Background(), stub, "raw text")
    if err != nil { t.Fatal(err) }
    if len(out) != 3 {
        t.Fatalf("len = %d, want 3; got %v", len(out), out)
    }
    if out[0] != "Tenant requested rent reduction." {
        t.Errorf("out[0] = %q", out[0])
    }
}

func TestAnalyzeMemory_ParsesJSON(t *testing.T) {
    t.Parallel()
    stub := &stubLLM{
        text: `{"scope_suffix": "tenant/42/", "categories": ["decision","finding"], "importance": 0.8, "entities": ["tenant-42"], "topics": ["rent-negotiation"]}`,
    }
    got, err := analyzeMemory(context.Background(), stub, "Tenant requested rent reduction.")
    if err != nil { t.Fatal(err) }
    if got.ScopeSuffix != "tenant/42/" { t.Errorf("ScopeSuffix = %q", got.ScopeSuffix) }
    if got.Importance < 0.79 || got.Importance > 0.81 { t.Errorf("Importance = %v", got.Importance) }
    if len(got.Categories) != 2 { t.Errorf("Categories = %v", got.Categories) }
}

// stubLLM implements the subset of agent.LanguageModel that extract/analyze use.
// Signature matches Phase 2's `agent.LanguageModel` interface exactly so these
// tests compile against the same type the production Remember pipeline uses
// (callers at memory_extract.go pass `&agent.GenerateOptions{Prompt: ...}` and
// read `res.Text` — the stub below matches both sides).
type stubLLM struct{ text string }
func (s *stubLLM) Provider() string { return "stub" }
func (s *stubLLM) ModelID() string  { return "stub-model" }
func (s *stubLLM) DoGenerate(ctx context.Context, _ *agent.GenerateOptions) (*agent.GenerateResult, error) {
    return &agent.GenerateResult{Text: s.text}, nil
}
```

Note: `agent.GenerateOptions` and `agent.GenerateResult` are the Phase 2 wrapper types re-exporting go-ai's `types.GenerateOptions` / `types.GenerateResult`. If Phase 2's harness renames either type, update the stub and the two call sites in `memory_extract.go` together.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/service/ -run "TestExtract|TestAnalyze" -v`
Expected: FAIL — functions undefined.

- [ ] **Step 3: Implement**

```go
// server/internal/service/memory_extract.go
package service

import (
    "context"
    "encoding/json"
    "fmt"
    "regexp"
    "strings"

    "aicolab/server/pkg/agent"
)

// Analysis is the structured return of analyzeMemory.
type Analysis struct {
    ScopeSuffix string            `json:"scope_suffix"`   // appended to "/ws/{wsID}/"
    Categories  []string          `json:"categories"`     // e.g. ["decision","finding","preference"]
    Importance  float64           `json:"importance"`     // 0.0–1.0
    Metadata    map[string]any    `json:"-"`              // entities + topics + dates flattened
    Entities    []string          `json:"entities"`
    Topics      []string          `json:"topics"`
}

// extractAtomicMemories asks the micro-tier model to break raw text into a
// numbered list of self-contained statements. One LLM call per task completion.
// Cost: ~10–200 input tokens + 50–300 output tokens per call on Haiku
// → $0.0001–0.0015.
func extractAtomicMemories(ctx context.Context, llm agent.LanguageModel, raw string) ([]string, error) {
    raw = strings.TrimSpace(raw)
    if raw == "" { return nil, nil }

    prompt := "Extract the atomic, self-contained facts from the following text. " +
        "Return a numbered list (one fact per line, in the form \"N. <fact>\"). " +
        "Do not add commentary, headings, or explanation. Text:\n\n" + raw

    res, err := llm.DoGenerate(ctx, &agent.GenerateOptions{Prompt: prompt})
    if err != nil { return nil, fmt.Errorf("extract: %w", err) }

    // "N. " prefix stripper + blank-line filter.
    prefix := regexp.MustCompile(`^\s*\d+[.)]\s*`)
    var out []string
    for _, line := range strings.Split(res.Text, "\n") {
        line = strings.TrimSpace(line)
        if line == "" { continue }
        line = prefix.ReplaceAllString(line, "")
        if line == "" { continue }
        out = append(out, line)
    }
    return out, nil
}

// analyzeMemory asks the micro-tier model to classify a single atomic memory.
// Returns scope_suffix, categories, importance, plus entities + topics that
// the caller flattens into Analysis.Metadata before persisting.
func analyzeMemory(ctx context.Context, llm agent.LanguageModel, content string) (Analysis, error) {
    prompt := "Classify the following memory. Return STRICTLY a JSON object with keys: " +
        "scope_suffix (string, e.g. \"tenant/42/\" or \"\"), " +
        "categories (array of strings from: decision, finding, preference, question, error), " +
        "importance (number 0.0–1.0), " +
        "entities (array of strings), " +
        "topics (array of strings). Memory: " + content

    res, err := llm.DoGenerate(ctx, &agent.GenerateOptions{Prompt: prompt})
    if err != nil { return Analysis{}, fmt.Errorf("analyze: %w", err) }

    // Be tolerant of surrounding prose / code fences.
    text := strings.TrimSpace(res.Text)
    if i := strings.Index(text, "{"); i > 0 { text = text[i:] }
    if j := strings.LastIndex(text, "}"); j > 0 { text = text[:j+1] }

    var a Analysis
    if err := json.Unmarshal([]byte(text), &a); err != nil {
        return Analysis{}, fmt.Errorf("analyze parse %q: %w", text, err)
    }
    if a.Importance < 0 { a.Importance = 0 }
    if a.Importance > 1 { a.Importance = 1 }
    a.Metadata = map[string]any{
        "entities": a.Entities,
        "topics":   a.Topics,
    }
    return a, nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/service/ -run "TestExtract|TestAnalyze" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/memory_extract.go server/internal/service/memory_test.go
git commit -m "feat(memory): extract + analyze pipeline steps (micro-tier LLM)"
```

---

### Task 5: Remember pipeline — embed, dedup, consolidate, persist

**Files:**
- Create: `server/internal/service/memory_consolidate.go`
- Modify: `server/internal/service/memory.go` (add `Remember` method)
- Modify: `server/internal/service/memory_test.go`

**Goal:** wire `Remember` end-to-end: extract → analyze → embed (one batch call) → intra-batch dedup → per-row consolidation LLM decision → persist via dim-partition dispatch. The dim dispatch mirrors Phase 4's `pickOps`; the consolidation LLM call produces one of `keep / update / delete / insert_new`.

- [ ] **Step 1: Write the failing test**

```go
func TestRemember_IntraBatchDedup(t *testing.T) {
    t.Parallel()
    // Two near-identical memories → the second is dropped before any
    // DB insert.
    svc := newTestMemoryService(t, withExtract(
        "1. Tenant wants rent reduction.",
        "2. Tenant wants rent reduction.",
    ), withEmbed(1536, [][]float32{onesVec(1536), onesVec(1536)}))
    wsID := uuid.New()
    got, err := svc.Remember(context.Background(), wsID, uuid.New(), uuid.New(), "raw", "")
    if err != nil { t.Fatal(err) }
    if len(got) != 1 {
        t.Errorf("wrote %d memories, want 1 (dedup should collapse duplicates)", len(got))
    }
}

func TestRemember_ConsolidateUpdatesExisting(t *testing.T) {
    t.Parallel()
    // Existing row at cosine 0.90; consolidation LLM returns "update".
    svc := newTestMemoryService(t,
        withExtract("1. Tenant requested rent reduction to $2000."),
        withEmbed(1536, [][]float32{onesVec(1536)}),
        withExistingSimilar("mem-existing", 0.90),
        withConsolidate(ConsolidationDecision{Action: "update", MergedContent: "Tenant requested rent reduction (latest: $2000)."}),
    )
    wsID := uuid.New()
    if _, err := svc.Remember(context.Background(), wsID, uuid.New(), uuid.New(), "raw", ""); err != nil {
        t.Fatal(err)
    }
    if svc.testHooks.updateCalls != 1 {
        t.Errorf("expected 1 UPDATE call, got %d", svc.testHooks.updateCalls)
    }
    if svc.testHooks.insertCalls != 0 {
        t.Errorf("expected 0 INSERT calls, got %d", svc.testHooks.insertCalls)
    }
}
```

Test-helper builder `newTestMemoryService` — put all of this in the same `memory_test.go`:

```go
type testOption func(*testCtx)

type testCtx struct {
    extractReturns    []string
    embedDim          int
    embedVectors      [][]float32
    existingSimilar   []similarRow
    consolidateAction string
}

func newTestMemoryService(t *testing.T, opts ...testOption) *MemoryService {
    t.Helper()
    tc := &testCtx{embedDim: 1536}
    for _, o := range opts { o(tc) }
    svc := &MemoryService{
        embedder: &stubEmbedder{dim: tc.embedDim, vecs: tc.embedVectors},
        microLLM: &stubLLM{
            extractJSON:       toJSONList(tc.extractReturns),
            consolidateAction: tc.consolidateAction,
        },
        DedupCosine:       0.98,
        ConsolidateCosine: 0.85,
    }
    if len(tc.existingSimilar) > 0 {
        svc.findSimilarOverride = func(_ context.Context, _ int, _ uuid.UUID, _ pgvector.Vector, _ string) ([]similarRow, error) {
            return tc.existingSimilar, nil
        }
    }
    return svc
}

func withExtract(items ...string) testOption            { return func(c *testCtx) { c.extractReturns = items } }
func withEmbed(dim int, vs [][]float32) testOption      { return func(c *testCtx) { c.embedDim = dim; c.embedVectors = vs } }
func withExistingSimilar(rows ...similarRow) testOption { return func(c *testCtx) { c.existingSimilar = rows } }
func withConsolidate(action string) testOption          { return func(c *testCtx) { c.consolidateAction = action } }
func onesVec(dim int) []float32                         { out := make([]float32, dim); for i := range out { out[i] = 1 }; return out }

type stubEmbedder struct{ dim int; vecs [][]float32 }
func (s *stubEmbedder) Embed(_ context.Context, in []string) ([][]float32, int, error) {
    if len(s.vecs) >= len(in) { return s.vecs[:len(in)], s.dim, nil }
    out := make([][]float32, len(in))
    for i := range in { out[i] = onesVec(s.dim) }
    return out, s.dim, nil
}

// stubLLM satisfies agent.LanguageModel. It looks at the prompt prefix to
// decide which canned response to return. Real code wires separate models for
// extract / analyze / consolidate.
type stubLLM struct{ extractJSON, consolidateAction string }
func (s *stubLLM) DoGenerate(_ context.Context, opts *agent.GenerateOptions) (*agent.GenerateResult, error) {
    if s.consolidateAction != "" && strings.Contains(opts.Prompt, "consolidate") {
        return &agent.GenerateResult{Text: `{"action":"` + s.consolidateAction + `","merged":""}`}, nil
    }
    return &agent.GenerateResult{Text: s.extractJSON}, nil
}

func toJSONList(items []string) string {
    // The extract prompt expects a JSON array of strings.
    b, _ := json.Marshal(items)
    return string(b)
}
```

The `MemoryService` type adds a test-only `findSimilarOverride func(context.Context, int, uuid.UUID, pgvector.Vector, string) ([]similarRow, error)` field. When non-nil, both `findSimilar` and `findSimilarTx` check it first and short-circuit the DB dispatcher. This avoids standing up a live DB for unit tests — integration tests in Task 17 hit real Postgres.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/service/ -run TestRemember -v`
Expected: FAIL — `Remember`, `ConsolidationDecision`, or the helpers don't exist yet.

- [ ] **Step 3: Implement consolidation LLM**

```go
// server/internal/service/memory_consolidate.go
package service

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "aicolab/server/pkg/agent"
)

// ConsolidationDecision is what the micro-LLM returns when asked whether a new
// memory supersedes or should merge with an existing near-duplicate.
type ConsolidationDecision struct {
    Action        string `json:"action"`          // keep | update | delete | insert_new
    MergedContent string `json:"merged_content"`  // populated on action=update
}

// consolidate asks the micro-LLM to decide whether `candidate` supersedes
// `existing`. Both are compared at the service layer — this LLM call only
// fires when FindSimilarMemory returned at least one row at cosine >=
// ConsolidateCosine (default 0.85).
func consolidate(ctx context.Context, llm agent.LanguageModel, candidate, existing string) (ConsolidationDecision, error) {
    prompt := "You are reconciling two similar memory records. " +
        "Decide whether the CANDIDATE should: " +
        "(a) be dropped because EXISTING already captures it (\"keep\"), " +
        "(b) replace EXISTING because it's a newer version (\"update\", supply merged_content), " +
        "(c) supersede EXISTING entirely (\"delete\"), or " +
        "(d) be inserted as a separate fact (\"insert_new\"). " +
        "Return STRICTLY a JSON object {action: string, merged_content: string|null}. " +
        "EXISTING: " + existing + "\nCANDIDATE: " + candidate

    res, err := llm.DoGenerate(ctx, &agent.GenerateOptions{Prompt: prompt})
    if err != nil { return ConsolidationDecision{}, fmt.Errorf("consolidate: %w", err) }

    text := strings.TrimSpace(res.Text)
    if i := strings.Index(text, "{"); i > 0 { text = text[i:] }
    if j := strings.LastIndex(text, "}"); j > 0 { text = text[:j+1] }
    var d ConsolidationDecision
    if err := json.Unmarshal([]byte(text), &d); err != nil {
        return ConsolidationDecision{}, fmt.Errorf("consolidate parse %q: %w", text, err)
    }
    switch d.Action {
    case "keep", "update", "delete", "insert_new":
        return d, nil
    default:
        // Unknown action → safe default.
        return ConsolidationDecision{Action: "insert_new"}, nil
    }
}
```

- [ ] **Step 4: Implement `Remember` in memory.go**

```go
// Append to server/internal/service/memory.go.

import (
    // add:
    "math"
    "github.com/pgvector/pgvector-go"
    "aicolab/server/pkg/embeddings"
    "aicolab/server/pkg/router" // for CostEvent tagging — see Phase 2 Task 1.5
)

type RememberedMemory struct {
    ID        uuid.UUID
    Content   string
    Scope     string
    Importance float64
}

// Remember runs the full write pipeline. Returns the rows actually persisted
// (empty if memory_mode='off', extraction produced nothing, or the batch was
// fully deduped/consolidated away).
//
// Never blocks on BudgetService — memory writes are best-effort enhancements.
// Emits cost_event rows with the parent task's trace_id (read from ctx per D8)
// for observability.
func (s *MemoryService) Remember(
    ctx context.Context,
    wsID, agentID, taskID uuid.UUID,
    raw string,
    scopeSuffix string,
) ([]RememberedMemory, error) {
    // 1. memory_mode gate.
    mode, err := s.queries.GetMemoryMode(ctx, wsID)
    if err != nil { return nil, fmt.Errorf("get memory_mode: %w", err) }
    if mode == "off" { return nil, nil }

    // 2. Extract atomic memories (skipped in 'lite' mode — treat raw as one).
    var atomics []string
    switch mode {
    case "lite":
        atomics = []string{strings.TrimSpace(raw)}
    default: // "full"
        atomics, err = extractAtomicMemories(ctx, s.microLLM, raw)
        if err != nil { return nil, err }
    }
    if len(atomics) == 0 { return nil, nil }

    // 3. Analyze each (skipped in 'lite').
    analyses := make([]Analysis, len(atomics))
    if mode == "full" {
        for i, a := range atomics {
            an, err := analyzeMemory(ctx, s.microLLM, a)
            if err != nil { return nil, err }
            analyses[i] = an
        }
    }

    // 4. Embed all in one batch call.
    vectors, dims, err := s.embedder.Embed(ctx, atomics)
    if err != nil { return nil, fmt.Errorf("embed: %w", err) }
    if len(vectors) != len(atomics) {
        return nil, fmt.Errorf("embed returned %d vectors for %d inputs", len(vectors), len(atomics))
    }

    // 5. Intra-batch dedup — O(N^2), fine for N ≤ ~20.
    dropped := make(map[int]bool)
    for i := range atomics {
        if dropped[i] { continue }
        for j := i + 1; j < len(atomics); j++ {
            if dropped[j] { continue }
            if cosine(vectors[i], vectors[j]) >= s.DedupCosine {
                dropped[j] = true
            }
        }
    }

    // 6. Per-row consolidation + persistence via dim-partition dispatch.
    // Each row's decision-and-write is wrapped in a transaction so a
    // delete+insert pair is atomic. Failure mid-pipeline rolls the tx back;
    // no permanent gap (delete without insert) can occur.
    scope := SanitizeScope(wsID, scopeSuffix+"agent/"+agentID.String()+"/")
    traceID := agent.TraceIDFrom(ctx)
    var out []RememberedMemory
    for i, content := range atomics {
        if dropped[i] { continue }
        emb := pgvector.NewVector(vectors[i])
        imp := 0.5
        if mode == "full" { imp = analyses[i].Importance }
        cats := []string{}
        if mode == "full" { cats = analyses[i].Categories }
        meta := map[string]any{}
        if mode == "full" { meta = analyses[i].Metadata }

        remembered, err := s.persistOne(ctx, dims, wsID, agentID, taskID, scope, content, emb, imp, cats, meta, traceID, mode)
        if err != nil { return nil, err }
        if remembered != nil { out = append(out, *remembered) }
    }

    return out, nil
}

// persistOne runs a single candidate through findSimilar → decide →
// update/delete-then-insert inside one transaction.
func (s *MemoryService) persistOne(
    ctx context.Context,
    dims int, wsID, agentID, taskID uuid.UUID,
    scope, content string,
    emb pgvector.Vector,
    imp float64, cats []string, meta map[string]any,
    traceID uuid.UUID, mode string,
) (*RememberedMemory, error) {
    tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil { return nil, err }
    defer tx.Rollback(ctx) // no-op after Commit

    similar, err := s.findSimilarTx(ctx, tx, dims, wsID, emb, scope)
    if err != nil { return nil, err }

    if len(similar) > 0 && mode == "full" {
        decision, err := consolidate(ctx, s.microLLM, content, similar[0].Content)
        if err != nil { return nil, err }
        switch decision.Action {
        case "keep":
            return nil, tx.Commit(ctx) // nothing to write
        case "delete":
            if err := s.deleteRowTx(ctx, tx, dims, wsID, similar[0].ID); err != nil { return nil, err }
            // fall through to insert_new
        case "update":
            newVecs, _, err := s.embedder.Embed(ctx, []string{decision.MergedContent})
            if err != nil { return nil, err }
            if err := s.updateRowTx(ctx, tx, dims, wsID, similar[0].ID, decision.MergedContent, pgvector.NewVector(newVecs[0]), imp, cats, meta); err != nil { return nil, err }
            if err := tx.Commit(ctx); err != nil { return nil, err }
            return &RememberedMemory{ID: similar[0].ID, Content: decision.MergedContent, Scope: scope, Importance: imp}, nil
        }
    }

    row, err := s.insertRowTx(ctx, tx, dims, wsID, content, emb, scope, cats, imp, agentID, taskID, meta, traceID)
    if err != nil { return nil, err }
    if err := tx.Commit(ctx); err != nil { return nil, err }
    return &RememberedMemory{ID: row.ID, Content: content, Scope: scope, Importance: imp}, nil
}

// cosine returns the cosine similarity of two equal-length vectors.
func cosine(a, b []float32) float64 {
    if len(a) != len(b) || len(a) == 0 { return 0 }
    var dot, na, nb float64
    for i := range a {
        dot += float64(a[i]) * float64(b[i])
        na += float64(a[i]) * float64(a[i])
        nb += float64(b[i]) * float64(b[i])
    }
    if na == 0 || nb == 0 { return 0 }
    return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// findSimilarTx / insertRowTx / updateRowTx / deleteRowTx are dim-dispatching
// wrappers that accept a pgx.Tx so persistOne can run the decide-and-write
// cycle inside one transaction. The non-Tx variants (findSimilar / insertRow /
// ...) remain for callers that don't need atomicity (e.g. the bare retrieval
// path used by Recall).
```

- [ ] **Step 5: Implement dim dispatchers**

```go
// server/internal/service/memory_dim.go
package service

import (
    "context"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"
    "github.com/pgvector/pgvector-go"

    "aicolab/server/pkg/db/db"
)

type similarRow struct {
    ID         uuid.UUID
    Content    string
    Similarity float64
}

func (s *MemoryService) findSimilar(ctx context.Context, dims int, wsID uuid.UUID, emb pgvector.Vector, scope string) ([]similarRow, error) {
    switch dims {
    case 1024:
        rows, err := s.queries.FindSimilarMemory1024(ctx, db.FindSimilarMemory1024Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    case 1536:
        rows, err := s.queries.FindSimilarMemory1536(ctx, db.FindSimilarMemory1536Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    case 3072:
        rows, err := s.queries.FindSimilarMemory3072(ctx, db.FindSimilarMemory3072Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    }
    return nil, fmt.Errorf("unsupported embedding dimension: %d", dims)
}

type insertedRow struct { ID uuid.UUID }

func (s *MemoryService) insertRow(
    ctx context.Context, dims int, wsID uuid.UUID,
    content string, emb pgvector.Vector, scope string,
    cats []string, importance float64, agentID, taskID uuid.UUID,
    meta map[string]any, traceID uuid.UUID,
) (insertedRow, error) {
    metaJSON, _ := encodeJSONB(meta)
    switch dims {
    case 1024:
        r, err := s.queries.InsertMemory1024(ctx, db.InsertMemory1024Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: pgtype.Numeric{}, // fill via numericFromFloat(importance)
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    case 1536:
        r, err := s.queries.InsertMemory1536(ctx, db.InsertMemory1536Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: pgtype.Numeric{},
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    case 3072:
        r, err := s.queries.InsertMemory3072(ctx, db.InsertMemory3072Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: pgtype.Numeric{},
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    }
    return insertedRow{}, fmt.Errorf("unsupported embedding dimension: %d", dims)
}

// updateRow / deleteRow follow the same per-dim switch pattern as insertRow.

// ───────────────────────── Tx variants ─────────────────────────
// Each Tx variant takes a pgx.Tx, wraps it with db.New(tx) to get a sqlc
// Queries object bound to that transaction, and delegates. Bodies mirror the
// non-Tx wrappers exactly except for the Queries source. Keep both variants
// because Recall (read-only) has no reason to pay for a transaction, while
// persistOne (decide-then-write) needs atomicity.

func (s *MemoryService) findSimilarTx(ctx context.Context, tx pgx.Tx, dims int, wsID uuid.UUID, emb pgvector.Vector, scope string) ([]similarRow, error) {
    q := db.New(tx)
    switch dims {
    case 1024:
        rows, err := q.FindSimilarMemory1024(ctx, db.FindSimilarMemory1024Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    case 1536:
        rows, err := q.FindSimilarMemory1536(ctx, db.FindSimilarMemory1536Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    case 3072:
        rows, err := q.FindSimilarMemory3072(ctx, db.FindSimilarMemory3072Params{
            WorkspaceID: uuidPg(wsID), Embedding: emb, Scope: scope, Column4: s.ConsolidateCosine,
        })
        return mapSimilar(rows), err
    }
    return nil, fmt.Errorf("unsupported embedding dimension: %d", dims)
}

func (s *MemoryService) insertRowTx(
    ctx context.Context, tx pgx.Tx, dims int, wsID uuid.UUID,
    content string, emb pgvector.Vector, scope string,
    cats []string, importance float64, agentID, taskID uuid.UUID,
    meta map[string]any, traceID uuid.UUID,
) (insertedRow, error) {
    q := db.New(tx)
    metaJSON, _ := encodeJSONB(meta)
    switch dims {
    case 1024:
        r, err := q.InsertMemory1024(ctx, db.InsertMemory1024Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: numericFromFloat(importance),
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    case 1536:
        r, err := q.InsertMemory1536(ctx, db.InsertMemory1536Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: numericFromFloat(importance),
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    case 3072:
        r, err := q.InsertMemory3072(ctx, db.InsertMemory3072Params{
            WorkspaceID: uuidPg(wsID), Content: content, Embedding: emb, Scope: scope,
            Categories: cats, Importance: numericFromFloat(importance),
            SourceAgentID: uuidPgNull(agentID), SourceTaskID: uuidPgNull(taskID),
            Metadata: metaJSON, Private: false, TraceID: uuidPg(traceID),
        })
        return insertedRow{ID: uuidFromPg(r.ID)}, err
    }
    return insertedRow{}, fmt.Errorf("unsupported embedding dimension: %d", dims)
}

func (s *MemoryService) updateRowTx(
    ctx context.Context, tx pgx.Tx, dims int, wsID, id uuid.UUID,
    content string, emb pgvector.Vector,
    importance float64, cats []string, meta map[string]any,
) error {
    q := db.New(tx)
    metaJSON, _ := encodeJSONB(meta)
    switch dims {
    case 1024:
        return q.UpdateMemory1024(ctx, db.UpdateMemory1024Params{
            ID: uuidPg(id), WorkspaceID: uuidPg(wsID),
            Content: content, Embedding: emb,
            Importance: numericFromFloat(importance), Categories: cats, Metadata: metaJSON,
        })
    case 1536:
        return q.UpdateMemory1536(ctx, db.UpdateMemory1536Params{
            ID: uuidPg(id), WorkspaceID: uuidPg(wsID),
            Content: content, Embedding: emb,
            Importance: numericFromFloat(importance), Categories: cats, Metadata: metaJSON,
        })
    case 3072:
        return q.UpdateMemory3072(ctx, db.UpdateMemory3072Params{
            ID: uuidPg(id), WorkspaceID: uuidPg(wsID),
            Content: content, Embedding: emb,
            Importance: numericFromFloat(importance), Categories: cats, Metadata: metaJSON,
        })
    }
    return fmt.Errorf("unsupported embedding dimension: %d", dims)
}

func (s *MemoryService) deleteRowTx(ctx context.Context, tx pgx.Tx, dims int, wsID, id uuid.UUID) error {
    q := db.New(tx)
    switch dims {
    case 1024:
        return q.DeleteMemory1024(ctx, db.DeleteMemory1024Params{ID: uuidPg(id), WorkspaceID: uuidPg(wsID)})
    case 1536:
        return q.DeleteMemory1536(ctx, db.DeleteMemory1536Params{ID: uuidPg(id), WorkspaceID: uuidPg(wsID)})
    case 3072:
        return q.DeleteMemory3072(ctx, db.DeleteMemory3072Params{ID: uuidPg(id), WorkspaceID: uuidPg(wsID)})
    }
    return fmt.Errorf("unsupported embedding dimension: %d", dims)
}

// numericFromFloat converts a Go float64 into pgtype.Numeric for sqlc inserts.
// Values are clamped to [0,1] per the DB CHECK on importance.
func numericFromFloat(v float64) pgtype.Numeric {
    if v < 0 { v = 0 }
    if v > 1 { v = 1 }
    var n pgtype.Numeric
    _ = n.Scan(fmt.Sprintf("%.6f", v))
    return n
}
```

The import block at the top of `memory_dim.go` must also include `"github.com/jackc/pgx/v5"` (for `pgx.Tx`).

- [ ] **Step 6: Run tests**

Run: `cd server && go test ./internal/service/ -run TestRemember -v`
Expected: PASS — dedup collapses duplicates; consolidation "update" calls UPDATE exactly once.

- [ ] **Step 7: Commit**

```bash
git add server/internal/service/memory.go server/internal/service/memory_consolidate.go server/internal/service/memory_dim.go server/internal/service/memory_test.go
git commit -m "feat(memory): Remember pipeline — extract/analyze/embed/dedup/consolidate/persist"
```

---

### Task 6: Recall pipeline — embed, search, composite score, access bookkeeping

**Files:**
- Create: `server/internal/service/memory_score.go`
- Modify: `server/internal/service/memory.go` (add `Recall` method)
- Modify: `server/internal/service/memory_test.go`

**Goal:** read-side pipeline. Composite score `0.5*sim + 0.3*recency + 0.2*importance` with 30-day half-life (`recency = 0.5 ^ (age_days / 30)`). Updates `last_accessed_at` + `access_count` on each returned row.

- [ ] **Step 1: Write the failing test**

```go
func TestScore_CompositeDefaults(t *testing.T) {
    t.Parallel()
    // sim=1.0, age=0d, importance=1.0 → 0.5 + 0.3 + 0.2 = 1.0
    got := Score(1.0, 0, 1.0)
    if got < 0.999 || got > 1.001 { t.Errorf("score = %v, want 1.0", got) }

    // sim=1.0, age=30d, importance=1.0 → 0.5 + 0.3*0.5 + 0.2 = 0.85
    got = Score(1.0, 30, 1.0)
    if got < 0.849 || got > 0.851 { t.Errorf("score = %v, want 0.85", got) }

    // sim=0, age=0d, importance=0 → 0.3 recency only = 0.3
    got = Score(0, 0, 0)
    if got < 0.299 || got > 0.301 { t.Errorf("score = %v, want 0.3", got) }
}

func TestRecall_ComposesScoreAndRanks(t *testing.T) {
    t.Parallel()
    // Three rows at the same embedding distance — expected order by age+importance.
    svc := newTestMemoryService(t,
        withEmbed(1536, [][]float32{onesVec(1536)}),
        withSearchResults(1536, []SearchRow{
            {ID: uuid.MustParse("aaaa0000-0000-0000-0000-000000000001"), Content: "old-unimportant", Similarity: 0.9, AgeDays: 90, Importance: 0.1},
            {ID: uuid.MustParse("aaaa0000-0000-0000-0000-000000000002"), Content: "fresh-critical", Similarity: 0.9, AgeDays: 1,  Importance: 0.9},
            {ID: uuid.MustParse("aaaa0000-0000-0000-0000-000000000003"), Content: "medium",          Similarity: 0.9, AgeDays: 30, Importance: 0.5},
        }),
    )
    got, err := svc.Recall(context.Background(), uuid.New(), "query", "agent/x/", 3)
    if err != nil { t.Fatal(err) }
    if got[0].Content != "fresh-critical" { t.Errorf("want fresh-critical first, got %q", got[0].Content) }
    if got[2].Content != "old-unimportant" { t.Errorf("want old-unimportant last, got %q", got[2].Content) }
    if svc.testHooks.accessUpdateCalls != 3 {
        t.Errorf("expected 3 UpdateMemoryAccess calls, got %d", svc.testHooks.accessUpdateCalls)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/service/ -run "TestScore|TestRecall" -v`
Expected: FAIL.

- [ ] **Step 3: Implement `Score`**

```go
// server/internal/service/memory_score.go
package service

import (
    "math"

    "aicolab/server/pkg/agent"
)

// Score is the composite formula ported from CrewAI. Weights and the recency
// half-life come from server/pkg/agent/defaults.go per D7 — do not inline
// literals here. recency_decay = 0.5 ^ (age_days / half_life).
func Score(similarity, ageDays, importance float64) float64 {
    w := agent.MemoryCompositeWeights
    recency := math.Pow(0.5, ageDays/float64(agent.MemoryRecencyHalfLifeDays))
    return w.Similarity*similarity + w.Recency*recency + w.Importance*importance
}
```

- [ ] **Step 4: Implement `Recall`**

```go
// Append to server/internal/service/memory.go.

type RecalledMemory struct {
    ID         uuid.UUID
    Content    string
    Scope      string
    Categories []string
    Importance float64
    SourceAgentID uuid.UUID
    Similarity float64
    CompositeScore float64
    CreatedAt  time.Time
}

// RecallOption is a variadic knob on Recall. Kept small: new knobs land as
// more options rather than signature churn.
type RecallOption func(*recallOpts)

type recallOpts struct {
    // viewerAgentID is a nullable UUID (pgtype.UUID.Valid == false means "no
    // viewer", i.e. admin/CLI view that sees every row regardless of private).
    // We deliberately use pgtype.UUID instead of uuid.UUID because uuid.Nil is
    // a valid non-NULL UUID — using it as a sentinel would cause sqlc's query
    // to match rows where source_agent_id = 00000000-..., returning zero
    // private memories instead of all of them. The sqlc SearchMemory* queries
    // take the same nullable type via `sqlc.narg('viewer_agent_id')`.
    viewerAgentID pgtype.UUID
}

// RecallViewer sets the viewer agent for the `private` filter. Memories with
// private=true are only returned when viewerAgentID matches source_agent_id.
// If this option is NOT passed, Recall returns all rows (private + public)
// — appropriate for admin CLI / Stats aggregation / migration tooling.
func RecallViewer(agentID uuid.UUID) RecallOption {
    return func(o *recallOpts) {
        o.viewerAgentID = pgtype.UUID{Bytes: agentID, Valid: true}
    }
}

// Recall runs the read pipeline. scopePrefix is relative to the workspace root
// ("agent/" for workspace-wide, "agent/{agentID}/" for a specific agent); the
// scope is always re-rooted under wsID via SanitizeScope.
func (s *MemoryService) Recall(
    ctx context.Context,
    wsID uuid.UUID,
    query string,
    scopePrefix string,
    limit int,
    opts ...RecallOption,
) ([]RecalledMemory, error) {
    if limit <= 0 { limit = agent.MemoryRecallTopK }
    o := recallOpts{}
    for _, fn := range opts { fn(&o) }
    scope := SanitizeScope(wsID, scopePrefix)

    vecs, dims, err := s.embedder.Embed(ctx, []string{query})
    if err != nil { return nil, fmt.Errorf("embed query: %w", err) }
    if len(vecs) == 0 { return nil, nil }
    emb := pgvector.NewVector(vecs[0])

    // Over-fetch so the composite-scoring step has a wider candidate pool
    // than the caller's final limit. 3× is an empirical default; if Phase 9
    // needs to tune it, it becomes another defaults.go constant.
    const overfetchFactor = 3
    rows, err := s.searchDim(ctx, dims, wsID, emb, scope, o.viewerAgentID, limit*overfetchFactor)
    if err != nil { return nil, err }

    out := make([]RecalledMemory, 0, len(rows))
    for _, r := range rows {
        ageDays := time.Since(r.CreatedAt).Hours() / 24.0
        s := Score(r.Similarity, ageDays, r.Importance)
        out = append(out, RecalledMemory{
            ID: r.ID, Content: r.Content, Scope: r.Scope, Categories: r.Categories,
            Importance: r.Importance, SourceAgentID: r.SourceAgentID,
            Similarity: r.Similarity, CompositeScore: s, CreatedAt: r.CreatedAt,
        })
    }
    sort.SliceStable(out, func(i, j int) bool { return out[i].CompositeScore > out[j].CompositeScore })
    if len(out) > limit { out = out[:limit] }

    // Access bookkeeping. Best-effort — one failed UPDATE shouldn't nuke the result.
    for _, m := range out {
        _ = s.updateAccess(ctx, dims, wsID, m.ID)
    }
    return out, nil
}

// searchDim + updateAccess follow the same per-dim switch as findSimilar.
```

- [ ] **Step 5: Run tests**

Run: `cd server && go test ./internal/service/ -run "TestScore|TestRecall" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/service/memory.go server/internal/service/memory_score.go server/internal/service/memory_dim.go server/internal/service/memory_test.go
git commit -m "feat(memory): Recall pipeline with composite scoring + access bookkeeping"
```

---

### Task 7: Background-writer pool — bounded goroutines + backpressure

**Files:**
- Create: `server/internal/service/memory_pool.go`
- Create: `server/internal/service/memory_pool_test.go`

**Goal:** a small pool that runs `Remember` calls off the worker's hot path. Max 10 concurrent writes, buffered channel capacity 100. Over-capacity submissions log a warning and drop (memory is best-effort). Metrics counters so observability can expose them.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/memory_pool_test.go
package service

import (
    "context"
    "sync"
    "sync/atomic"
    "testing"
    "time"
)

func TestMemoryPool_BackpressureDropsExcess(t *testing.T) {
    t.Parallel()

    var started, finished sync.WaitGroup
    block := make(chan struct{})

    p := NewMemoryPool(MemoryPoolOptions{Workers: 2, QueueDepth: 3})
    defer p.Shutdown(time.Second)

    accepted := 0
    for i := 0; i < 10; i++ {
        ok := p.Submit(func(_ context.Context) {
            started.Done()
            <-block           // hold the worker so the queue fills
            finished.Done()
        })
        if ok {
            accepted++
            started.Add(1); finished.Add(1)
        }
    }

    if accepted != 2+3 {
        t.Fatalf("accepted = %d, want 5 (2 workers + 3 buffered)", accepted)
    }
    m := p.Metrics()
    if m.Dropped != 5 {
        t.Errorf("Dropped = %d, want 5", m.Dropped)
    }
    close(block)
    // Drain any in-flight tasks to make the test hermetic under -race.
    started.Wait()
    finished.Wait()
}

func TestMemoryPool_ShutdownDrains(t *testing.T) {
    t.Parallel()
    p := NewMemoryPool(MemoryPoolOptions{Workers: 1, QueueDepth: 2})

    var ran int32
    for i := 0; i < 2; i++ {
        p.Submit(func(_ context.Context) {
            atomic.AddInt32(&ran, 1)
            time.Sleep(10 * time.Millisecond)
        })
    }
    if err := p.Shutdown(time.Second); err != nil { t.Fatal(err) }
    if got := atomic.LoadInt32(&ran); got != 2 {
        t.Errorf("ran = %d, want 2 — all submitted tasks should complete before Shutdown returns", got)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/service/ -run TestMemoryPool -v`
Expected: FAIL — `NewMemoryPool`, `MemoryPoolOptions`, `Submit`, `Shutdown`, `Metrics` undefined.

- [ ] **Step 3: Implement**

```go
// server/internal/service/memory_pool.go
package service

import (
    "context"
    "fmt"
    "log/slog"
    "sync"
    "sync/atomic"
    "time"
)

type MemoryPoolOptions struct {
    Workers    int // default 10 — caps concurrent Remember calls per server
    QueueDepth int // default 100 — buffer between Submit and the worker
    Logger     *slog.Logger
}

type MemoryPoolMetrics struct {
    Queued, Completed, Dropped uint64
}

type memoryJob func(context.Context)

type MemoryPool struct {
    opts    MemoryPoolOptions
    jobs    chan memoryJob
    wg      sync.WaitGroup

    // Atomic counters exposed via Metrics().
    queued, completed, dropped uint64

    // parent ctx is cancelled on Shutdown to signal workers.
    parentCtx    context.Context
    parentCancel context.CancelFunc

    // shutdownOnce guards close(p.jobs) against a double-close panic when
    // Shutdown is called twice (e.g. SIGTERM races with the deferred call in
    // main.go).
    shutdownOnce sync.Once
}

func NewMemoryPool(opts MemoryPoolOptions) *MemoryPool {
    if opts.Workers <= 0 { opts.Workers = agent.MemoryWorkerPoolSize }
    if opts.QueueDepth <= 0 { opts.QueueDepth = agent.MemoryWorkerQueueCap }
    if opts.Logger == nil { opts.Logger = slog.Default() }

    ctx, cancel := context.WithCancel(context.Background())
    p := &MemoryPool{opts: opts, jobs: make(chan memoryJob, opts.QueueDepth), parentCtx: ctx, parentCancel: cancel}
    for i := 0; i < opts.Workers; i++ {
        p.wg.Add(1)
        go p.run()
    }
    return p
}

// Submit enqueues a job. Returns false when the buffer is full — the caller
// should log (one warning per drop is fine) but not block or retry: memory
// writes are best-effort.
func (p *MemoryPool) Submit(fn memoryJob) bool {
    select {
    case p.jobs <- fn:
        atomic.AddUint64(&p.queued, 1)
        return true
    default:
        atomic.AddUint64(&p.dropped, 1)
        p.opts.Logger.Warn("memory pool full; dropping write",
            "workers", p.opts.Workers, "queue_depth", p.opts.QueueDepth)
        return false
    }
}

func (p *MemoryPool) Metrics() MemoryPoolMetrics {
    return MemoryPoolMetrics{
        Queued:    atomic.LoadUint64(&p.queued),
        Completed: atomic.LoadUint64(&p.completed),
        Dropped:   atomic.LoadUint64(&p.dropped),
    }
}

// Shutdown closes the intake, waits for workers to drain, and returns. Jobs
// already enqueued will run to completion. Jobs still in-flight when the
// timeout elapses are abandoned with their ctx cancelled.
func (p *MemoryPool) Shutdown(timeout time.Duration) error {
    p.shutdownOnce.Do(func() { close(p.jobs) }) // safe to call twice
    done := make(chan struct{})
    go func() { p.wg.Wait(); close(done) }()
    select {
    case <-done:
        p.parentCancel()
        return nil
    case <-time.After(timeout):
        p.parentCancel()
        return fmt.Errorf("memory pool did not drain within %s", timeout)
    }
}

func (p *MemoryPool) run() {
    defer p.wg.Done()
    for job := range p.jobs {
        // Job-scoped ctx has a 30s cap; the pool can cancel all workers at
        // Shutdown by cancelling parentCtx.
        ctx, cancel := context.WithTimeout(p.parentCtx, 30*time.Second)
        panicked := false
        func() {
            defer cancel()
            defer func() {
                if r := recover(); r != nil {
                    panicked = true
                    p.opts.Logger.Error("memory job panic", "recover", r)
                }
            }()
            job(ctx)
        }()
        if !panicked { atomic.AddUint64(&p.completed, 1) }
        // Panicked jobs are counted via the logger only; Metrics.Completed
        // tracks successfully-run jobs, matching operator expectations.
    }
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/service/ -run TestMemoryPool -race -v`
Expected: PASS, no race warnings.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/memory_pool.go server/internal/service/memory_pool_test.go
git commit -m "feat(memory): bounded worker pool with backpressure + metrics"
```

---

### Task 8: Memory tools (`recall_memory` + `save_memory`) as go-ai `types.Tool`

**Files:**
- Create: `server/pkg/tools/builtin/memory_tools.go`
- Create: `server/pkg/tools/builtin/memory_tools_test.go`

**Goal:** two built-in tools agents can invoke. Both read `wsID`, `agentID`, and `taskID` from `context.Context` (Phase 2 convention; the harness stamps them before dispatch). Schema matches go-ai's `types.Tool`.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/tools/builtin/memory_tools_test.go
package builtin

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/google/uuid"

    "aicolab/server/pkg/agent"
)

type fakeMemoryService struct {
    saves   []string
    recalls []string
}
func (f *fakeMemoryService) Remember(ctx context.Context, wsID, agentID, taskID uuid.UUID, raw, scope string) ([]service.RememberedMemory, error) {
    f.saves = append(f.saves, raw)
    return []service.RememberedMemory{{ID: uuid.New(), Content: raw}}, nil
}
func (f *fakeMemoryService) Recall(ctx context.Context, wsID uuid.UUID, q, scope string, n int) ([]service.RecalledMemory, error) {
    f.recalls = append(f.recalls, q)
    return []service.RecalledMemory{{Content: "prior: " + q, Similarity: 0.9, CompositeScore: 0.85}}, nil
}

func TestSaveMemoryTool_PersistsContent(t *testing.T) {
    t.Parallel()
    ms := &fakeMemoryService{}
    tool := SaveMemoryTool(ms)

    ctx := agent.WithTraceID(context.Background(), uuid.New())
    ctx = agent.WithWorkspaceID(ctx, uuid.New())
    ctx = agent.WithAgentID(ctx, uuid.New())
    ctx = agent.WithTaskID(ctx, uuid.New())

    raw := `{"content": "Tenant agreed to $2000 rent"}`
    out, err := tool.Execute(ctx, json.RawMessage(raw))
    if err != nil { t.Fatal(err) }
    if len(ms.saves) != 1 || ms.saves[0] != "Tenant agreed to $2000 rent" {
        t.Errorf("save payload = %v", ms.saves)
    }
    if !strings.Contains(out, "saved") {
        t.Errorf("tool output = %q; want confirmation", out)
    }
}

func TestRecallMemoryTool_ReturnsFormattedResults(t *testing.T) {
    t.Parallel()
    ms := &fakeMemoryService{}
    tool := RecallMemoryTool(ms)

    ctx := agent.WithWorkspaceID(context.Background(), uuid.New())
    raw := `{"query": "rent negotiations", "limit": 3}`
    out, err := tool.Execute(ctx, json.RawMessage(raw))
    if err != nil { t.Fatal(err) }
    if !strings.Contains(out, "prior: rent negotiations") {
        t.Errorf("tool output = %q", out)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/tools/builtin/ -run "TestSaveMemoryTool|TestRecallMemoryTool" -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/tools/builtin/memory_tools.go
package builtin

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "github.com/digitallysavvy/go-ai/pkg/provider/types"
    "github.com/google/uuid"

    "aicolab/server/pkg/agent"
    "aicolab/server/internal/service"
)

// MemorySurface is the subset of MemoryService the tools depend on. Kept small
// so tests can stub it (see memory_tools_test.go fakeMemoryService).
type MemorySurface interface {
    Remember(ctx context.Context, wsID, agentID, taskID uuid.UUID, raw, scope string) ([]service.RememberedMemory, error)
    Recall(ctx context.Context, wsID uuid.UUID, query, scope string, limit int) ([]service.RecalledMemory, error)
}

type saveMemoryArgs struct {
    Content  string   `json:"content"`
    Categories []string `json:"categories,omitempty"`
    Private  bool     `json:"private,omitempty"`
}

type recallMemoryArgs struct {
    Query string `json:"query"`
    Limit int    `json:"limit,omitempty"`
}

func SaveMemoryTool(ms MemorySurface) types.Tool {
    return types.Tool{
        Name:        "save_memory",
        Description: "Save an important fact, decision, or observation for later recall by this or other agents in the same workspace.",
        Parameters: json.RawMessage(`{
            "type": "object",
            "required": ["content"],
            "properties": {
                "content":    { "type": "string", "description": "The fact or observation to remember, in a single self-contained sentence." },
                "categories": { "type": "array", "items": {"type": "string"}, "description": "Optional labels (e.g. ['decision','finding'])." },
                "private":    { "type": "boolean", "description": "If true, only this agent can recall the memory." }
            }
        }`),
        Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
            var args saveMemoryArgs
            if err := json.Unmarshal(raw, &args); err != nil {
                return "", fmt.Errorf("save_memory: bad args: %w", err)
            }
            if strings.TrimSpace(args.Content) == "" {
                return "", fmt.Errorf("save_memory: content is required")
            }
            wsID := agent.WorkspaceIDFrom(ctx)
            agentID := agent.AgentIDFrom(ctx)
            taskID := agent.TaskIDFrom(ctx)
            out, err := ms.Remember(ctx, wsID, agentID, taskID, args.Content, "")
            if err != nil { return "", err }
            return fmt.Sprintf("Memory saved (%d row%s persisted).", len(out), plural(len(out))), nil
        },
    }
}

func RecallMemoryTool(ms MemorySurface) types.Tool {
    return types.Tool{
        Name:        "recall_memory",
        Description: "Search past memories in this workspace for relevant context on the query.",
        Parameters: json.RawMessage(`{
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": { "type": "string", "description": "What you want to recall." },
                "limit": { "type": "integer", "minimum": 1, "maximum": 20, "default": 5 }
            }
        }`),
        Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
            var args recallMemoryArgs
            if err := json.Unmarshal(raw, &args); err != nil {
                return "", fmt.Errorf("recall_memory: bad args: %w", err)
            }
            if strings.TrimSpace(args.Query) == "" {
                return "", fmt.Errorf("recall_memory: query is required")
            }
            if args.Limit == 0 { args.Limit = 5 }
            wsID := agent.WorkspaceIDFrom(ctx)
            rows, err := ms.Recall(ctx, wsID, args.Query, "agent/", args.Limit)
            if err != nil { return "", err }
            if len(rows) == 0 { return "No memories matched the query.", nil }

            var b strings.Builder
            b.WriteString("Recalled memories:\n")
            for _, m := range rows {
                fmt.Fprintf(&b, "- %s (score=%.2f)\n", m.Content, m.CompositeScore)
            }
            return b.String(), nil
        },
    }
}

func plural(n int) string { if n == 1 { return "" }; return "s" }
```

**Dependency on Phase 2:** this task requires `agent.WithWorkspaceID / WithAgentID / WithTaskID / WorkspaceIDFrom / AgentIDFrom / TaskIDFrom` to exist in `server/pkg/agent/trace.go`. If Phase 2 Task 1.5 only added `WithTraceID / TraceIDFrom`, extend the file in this task:

```go
// Append to server/pkg/agent/trace.go.
type workspaceIDCtxKey struct{}
type agentIDCtxKey struct{}
type taskIDCtxKey struct{}
var workspaceIDKey = workspaceIDCtxKey{}
var agentIDKey     = agentIDCtxKey{}
var taskIDKey      = taskIDCtxKey{}

func WithWorkspaceID(ctx context.Context, id uuid.UUID) context.Context { return context.WithValue(ctx, workspaceIDKey, id) }
func WorkspaceIDFrom(ctx context.Context) uuid.UUID {
    if v, ok := ctx.Value(workspaceIDKey).(uuid.UUID); ok { return v }
    return uuid.Nil
}
func WithAgentID(ctx context.Context, id uuid.UUID) context.Context { return context.WithValue(ctx, agentIDKey, id) }
func AgentIDFrom(ctx context.Context) uuid.UUID {
    if v, ok := ctx.Value(agentIDKey).(uuid.UUID); ok { return v }
    return uuid.Nil
}
func WithTaskID(ctx context.Context, id uuid.UUID) context.Context { return context.WithValue(ctx, taskIDKey, id) }
func TaskIDFrom(ctx context.Context) uuid.UUID {
    if v, ok := ctx.Value(taskIDKey).(uuid.UUID); ok { return v }
    return uuid.Nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./pkg/tools/builtin/ -run "TestSaveMemoryTool|TestRecallMemoryTool" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/tools/builtin/memory_tools.go server/pkg/tools/builtin/memory_tools_test.go server/pkg/agent/trace.go
git commit -m "feat(tools): save_memory + recall_memory built-in tools"
```

---

### Task 9: Worker pre-task memory injection

**Files:**
- Modify: `server/internal/worker/worker.go`
- Modify: `server/internal/worker/worker_test.go`

**Goal:** before each claimed task, call `memory.Recall(description, "agent/", 5)` and prepend a `<MEMORIES>` section to the system prompt. This is an Anthropic-cacheable prefix (same cache-breakpoint convention as Phase 4 knowledge injection — preserve stable ordering so cache-hits land).

- [ ] **Step 1: Write the failing test**

```go
// Append to server/internal/worker/worker_test.go.

func TestWorker_InjectsRecalledMemoriesIntoSystemPrompt(t *testing.T) {
    t.Parallel()
    fakeMS := &fakeRecaller{rows: []service.RecalledMemory{
        {Content: "Tenant previously agreed to $2000 rent.", CompositeScore: 0.9},
    }}
    w := newTestWorker(t, withMemory(fakeMS))

    task := newTestTask(t, "Negotiate rent")
    backend := &capturingBackend{}
    w.resolver = func(ctx context.Context, _ *Task) (Backend, error) { return backend, nil }

    if err := w.claimAndExecute(context.Background(), task); err != nil { t.Fatal(err) }
    if !strings.Contains(backend.lastSystemPrompt, "<MEMORIES>") {
        t.Errorf("system prompt missing <MEMORIES> block:\n%s", backend.lastSystemPrompt)
    }
    if !strings.Contains(backend.lastSystemPrompt, "Tenant previously agreed to $2000 rent.") {
        t.Error("system prompt should contain the recalled content")
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/worker/ -run TestWorker_InjectsRecalledMemoriesIntoSystemPrompt -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// In server/internal/worker/worker.go, inside claimAndExecute, after
// ctx = agent.WithTraceID(ctx, traceID) but BEFORE resolveBackend:

// D8 context enrichment — tools and MemoryService read these via
// agent.WorkspaceIDFrom / AgentIDFrom / TaskIDFrom.
ctx = agent.WithWorkspaceID(ctx, uuidFromPg(task.WorkspaceID))
ctx = agent.WithAgentID(ctx, uuidFromPg(task.AgentID))
ctx = agent.WithTaskID(ctx, uuidFromPg(task.ID))

// Recall up to 5 memories under the agent-scope for this workspace.
// Failure is non-fatal — memory is a best-effort enhancement.
var memoryPrefix string
if w.memory != nil {
    rows, err := w.memory.Recall(ctx, uuidFromPg(task.WorkspaceID), task.Prompt, "agent/", 5)
    if err != nil {
        w.logger.Warn("recall failed", "task_id", task.ID, "err", err)
    } else if len(rows) > 0 {
        memoryPrefix = renderMemoriesBlock(rows)
    }
}

// Pass memoryPrefix to the backend via ExecOptions so the harness can splice
// it into the system prompt. ExecOptions already has a free-form field for
// platform-specific augmentation (Phase 2 Task 1); add MemoryPrefix next to it.

// ...resolveBackend / StartTask / backend.Execute as before, but:
sess, err := backend.Execute(ctx, task.Prompt, agent.ExecOptions{MemoryPrefix: memoryPrefix})
```

```go
// server/internal/worker/memory_prefix.go (new, small)
package worker

import (
    "fmt"
    "strings"

    "aicolab/server/internal/service"
)

// renderMemoriesBlock produces a stable-ordered cacheable prefix. Ordering:
// composite score descending. Anthropic prompt-cache hits depend on byte-level
// stability of the prefix; any dynamic element (timestamps, UUIDs) breaks it.
func renderMemoriesBlock(rows []service.RecalledMemory) string {
    var b strings.Builder
    b.WriteString("<MEMORIES>\nMemories from past tasks in this workspace.\n")
    for _, m := range rows {
        fmt.Fprintf(&b, "- %s\n", m.Content)
    }
    b.WriteString("</MEMORIES>\n\n")
    return b.String()
}
```

In the harness (`server/pkg/agent/harness/harness.go`), splice `opts.MemoryPrefix` **before** the system prompt the builder produces — keep the memory block as the outermost cacheable prefix so its boundary is stable across tasks.

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/worker/ -run TestWorker_InjectsRecalledMemoriesIntoSystemPrompt -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/worker.go server/internal/worker/memory_prefix.go server/pkg/agent/harness/harness.go server/internal/worker/worker_test.go
git commit -m "feat(worker): inject recalled memories as cacheable system-prompt prefix"
```

---

### Task 10: Worker post-task async Remember via pool

**Files:**
- Modify: `server/internal/worker/worker.go`
- Modify: `server/cmd/worker/main.go`
- Modify: `server/internal/worker/worker_test.go`

**Goal:** after a task completes (success OR failure), enqueue a `Remember` call on the shared pool. Run OFF the ctx that cancels with the task (`context.Background()` derived from the pool) — the task's ctx will be cancelled when the response stream closes, but Remember can take several seconds.

- [ ] **Step 1: Write the failing test**

```go
func TestWorker_EnqueuesRememberAfterCompletion(t *testing.T) {
    t.Parallel()
    fakeMS := &fakeRecaller{} // also records Remember calls
    pool := service.NewMemoryPool(service.MemoryPoolOptions{Workers: 1, QueueDepth: 4})
    defer pool.Shutdown(time.Second)

    w := newTestWorker(t, withMemory(fakeMS), withMemoryPool(pool))
    task := newTestTask(t, "Negotiate rent")
    w.resolver = func(ctx context.Context, _ *Task) (Backend, error) {
        return &capturingBackend{result: "done — tenant agreed to $2000"}, nil
    }
    if err := w.claimAndExecute(context.Background(), task); err != nil { t.Fatal(err) }

    // Allow the pool to drain.
    require.NoError(t, pool.Shutdown(2*time.Second))
    if len(fakeMS.remembers) != 1 {
        t.Fatalf("Remember called %d times, want 1", len(fakeMS.remembers))
    }
    if !strings.Contains(fakeMS.remembers[0].raw, "tenant agreed to $2000") {
        t.Errorf("remember payload = %q", fakeMS.remembers[0].raw)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/worker/ -run TestWorker_EnqueuesRememberAfterCompletion -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `server/internal/worker/worker.go`, after the task result is consumed and `CompleteTask` is called, add:

```go
// Async Remember. The pool's worker-scoped ctx is independent of this
// task's ctx — Remember may take several seconds and must not be cut off
// when the response stream closes.
if w.memory != nil && w.memoryPool != nil {
    wsID := uuidFromPg(task.WorkspaceID)
    agentID := uuidFromPg(task.AgentID)
    taskID := uuidFromPg(task.ID)
    traceID := agent.TraceIDFrom(ctx)
    // Compose raw: the spec per PLAN.md §5.3 is:
    //   "Task: {desc}\nAgent: {name}\nResult: {output}"
    raw := fmt.Sprintf("Task: %s\nAgent: %s\nResult: %s",
        task.Prompt, task.AgentName, finalResult.Output)

    w.memoryPool.Submit(func(poolCtx context.Context) {
        poolCtx = agent.WithTraceID(poolCtx, traceID)
        poolCtx = agent.WithWorkspaceID(poolCtx, wsID)
        poolCtx = agent.WithAgentID(poolCtx, agentID)
        poolCtx = agent.WithTaskID(poolCtx, taskID)
        if _, err := w.memory.Remember(poolCtx, wsID, agentID, taskID, raw, ""); err != nil {
            w.logger.Warn("remember failed", "task_id", taskID, "err", err)
        }
    })
}
```

In `server/cmd/worker/main.go`:

```go
// Add to the worker-construction block:
memPool := service.NewMemoryPool(service.MemoryPoolOptions{
    Workers: 10, QueueDepth: 100, Logger: logger,
})
memSvc := service.NewMemoryService(pool, embedder, microLLM, memPool)
// ...
w := worker.New(worker.Options{
    // existing fields
    Memory:     memSvc,
    MemoryPool: memPool,
})

// Wire graceful shutdown: before the worker exits, call memPool.Shutdown(30s).
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/worker/ -run TestWorker_EnqueuesRememberAfterCompletion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/worker.go server/cmd/worker/main.go server/internal/worker/worker_test.go
git commit -m "feat(worker): async Remember after task completion via bounded pool"
```

---

### Task 11: Context compaction — skeleton + trigger + provider dispatch

**Files:**
- Create: `server/pkg/agent/compaction.go`
- Create: `server/pkg/agent/compaction_test.go`

**Goal:** top-level `Compact(ctx, provider, messages, opts) (CompactResult, error)` that (a) enforces `ContextEvictionThresholdBytes` (80 KB from `defaults.go`); (b) preserves protected ranges (system prompt, first user exchange, most recent ~20K tokens); (c) dispatches to Anthropic or fallback path.

- [ ] **Step 0: Add Phase 5 tunables to `server/pkg/agent/defaults.go` (D7)**

Append to the existing `defaults.go` file created in Phase 1. Every tunable Phase 5 introduces MUST live here; do not redeclare them in their consuming files.

```go
// server/pkg/agent/defaults.go — APPEND
const (
    // RecentProtectBytes is the suffix of recent messages Compact refuses to
    // touch. ~20K tokens preserves short-term reasoning.
    RecentProtectBytes = 20 * 1024

    // MemoryRecallTopK caps Recall results returned to the model.
    MemoryRecallTopK = 8
    // MemoryRecencyHalfLifeDays is the exponential-decay half-life for the
    // recency term of the composite score.
    MemoryRecencyHalfLifeDays = 30
    // MemoryWorkerPoolSize bounds concurrent Remember goroutines.
    MemoryWorkerPoolSize = 10
    // MemoryWorkerQueueCap is the buffered-channel depth; overflow drops with warning.
    MemoryWorkerQueueCap = 100
)

// MemoryCompositeWeights are the terms in the Recall composite score.
// Sum MUST equal 1.0. Keep here so Phase 9 cost tuning can override.
var MemoryCompositeWeights = struct {
    Similarity, Recency, Importance float64
}{Similarity: 0.5, Recency: 0.3, Importance: 0.2}
```

Run: `cd server && go build ./...`
Expected: PASS (no redeclaration errors).

Commit:
```bash
git add server/pkg/agent/defaults.go
git commit -m "feat(agent): add Phase 5 memory + compaction tunables to defaults.go"
```

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/compaction_test.go
package agent

import (
    "context"
    "strings"
    "testing"

    "github.com/digitallysavvy/go-ai/pkg/provider/types"
)

func TestCompact_BelowThreshold_NoOp(t *testing.T) {
    t.Parallel()
    msgs := []types.Message{
        {Role: "system", Content: []types.ContentBlock{{Type: "text", Text: "sys"}}},
        {Role: "user", Content: []types.ContentBlock{{Type: "text", Text: strings.Repeat("x", 100)}}},
    }
    got, err := Compact(context.Background(), "anthropic", msgs, CompactOptions{})
    if err != nil { t.Fatal(err) }
    if got.Changed { t.Error("Compact should be no-op below threshold") }
    if len(got.Messages) != len(msgs) { t.Errorf("len = %d, want %d", len(got.Messages), len(msgs)) }
}

func TestCompact_PreservesProtectedRanges(t *testing.T) {
    t.Parallel()
    msgs := []types.Message{
        {Role: "system", Content: []types.ContentBlock{{Type: "text", Text: "sys"}}},
        {Role: "user", Content: []types.ContentBlock{{Type: "text", Text: "first user message"}}},
        {Role: "assistant", Content: []types.ContentBlock{{Type: "tool_use", ID: "t1", Name: "http_get"}}},
        {Role: "user", Content: []types.ContentBlock{{Type: "tool_result", ToolUseID: "t1", Text: strings.Repeat("payload ", 20_000)}}},
        {Role: "assistant", Content: []types.ContentBlock{{Type: "text", Text: "recent assistant"}}},
        {Role: "user", Content: []types.ContentBlock{{Type: "text", Text: "most recent user"}}},
    }
    got, err := Compact(context.Background(), "openai", msgs, CompactOptions{})
    if err != nil { t.Fatal(err) }
    if !got.Changed { t.Fatal("Compact should fire above threshold") }

    // System prompt, first user, and the final two messages survive verbatim.
    if got.Messages[0].Content[0].Text != "sys" { t.Error("system prompt altered") }
    if got.Messages[1].Content[0].Text != "first user message" { t.Error("first user exchange altered") }
    last := got.Messages[len(got.Messages)-1]
    if last.Content[0].Text != "most recent user" { t.Error("most-recent message lost") }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/ -run TestCompact -v`
Expected: FAIL — `Compact`, `CompactOptions`, `CompactResult` undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/compaction.go
package agent

import (
    "context"

    "github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// Tunables (ContextEvictionThresholdBytes, RecentProtectBytes) are defined in
// server/pkg/agent/defaults.go per D7. DO NOT redeclare them here — this file
// is in the same package and references them by bare name.

type CompactOptions struct {
    // Summariser model. Nil → fall back to "haiku" / "gpt-4o-mini" selection
    // based on provider. Callers with a configured micro-tier model pass it
    // explicitly to avoid a second load in the hot path.
    Summariser LanguageModel
    // Archivist, when non-nil, receives the compacted conversation for
    // write-back into agent_memory (Phase 5 §5.4). nil disables archival.
    Archivist func(ctx context.Context, slice []types.Message) error
}

type CompactResult struct {
    Messages []types.Message
    Changed  bool
    // BytesBefore / BytesAfter let callers emit telemetry.
    BytesBefore, BytesAfter int
}

// Compact shrinks the message history if total bytes exceed the eviction
// threshold. Dispatches on provider: "anthropic" uses go-ai's Clear/Compact
// primitives (compaction_anthropic.go); others use summarise-and-truncate
// (compaction_fallback.go).
func Compact(ctx context.Context, provider string, msgs []types.Message, opts CompactOptions) (CompactResult, error) {
    before := totalBytes(msgs)
    if before < ContextEvictionThresholdBytes {
        return CompactResult{Messages: msgs, Changed: false, BytesBefore: before, BytesAfter: before}, nil
    }

    protected := computeProtectedRanges(msgs)

    var out []types.Message
    var err error
    switch provider {
    case "anthropic":
        out, err = compactAnthropic(ctx, msgs, protected, opts)
    default:
        out, err = compactFallback(ctx, msgs, protected, opts)
    }
    if err != nil { return CompactResult{Messages: msgs}, err }
    // Archival write-back is performed inside each compact* path — each one
    // has direct access to the pre-edit middle slice, so the responsibility
    // lives there (compaction_anthropic.go and compaction_fallback.go).
    after := totalBytes(out)
    return CompactResult{Messages: out, Changed: true, BytesBefore: before, BytesAfter: after}, nil
}

// totalBytes sums the text content of every message. Approximate; good enough
// for threshold comparisons.
func totalBytes(msgs []types.Message) int {
    n := 0
    for _, m := range msgs {
        for _, b := range m.Content {
            n += len(b.Text)
        }
    }
    return n
}

// protectedRange flags messages that MUST NOT be removed.
type protectedRange struct {
    systemEnd   int // inclusive — typically 0 (single system message)
    firstUserEnd int // inclusive — typically 1
    recentStart int // inclusive — earliest message in the recent-protect suffix
}

func computeProtectedRanges(msgs []types.Message) protectedRange {
    pr := protectedRange{systemEnd: -1, firstUserEnd: -1, recentStart: len(msgs)}
    // System prompt: all leading system messages.
    for i, m := range msgs {
        if m.Role != "system" { break }
        pr.systemEnd = i
    }
    // First user exchange: the first user message after system.
    for i := pr.systemEnd + 1; i < len(msgs); i++ {
        if msgs[i].Role == "user" { pr.firstUserEnd = i; break }
    }
    // Recent protect window: walk back from the end until we've accumulated
    // RecentProtectBytes worth of text.
    acc := 0
    for i := len(msgs) - 1; i > pr.firstUserEnd; i-- {
        for _, b := range msgs[i].Content { acc += len(b.Text) }
        pr.recentStart = i
        if acc >= RecentProtectBytes { break }
    }
    return pr
}

// compactAnthropic and compactFallback are defined in sibling files.
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./pkg/agent/ -run TestCompact -v`
Expected: PASS — the below-threshold case returns `Changed: false`; the above-threshold case goes to `compactFallback` which we wire in Task 13.

*(If Task 13 isn't complete yet, the above-threshold test will fail for a missing `compactFallback`. That's fine — it's the TDD failing state for Task 13.)*

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/compaction.go server/pkg/agent/compaction_test.go
git commit -m "feat(compact): context compaction skeleton with protected ranges"
```

---

### Task 12: Context compaction — Anthropic path

**Files:**
- Create: `server/pkg/agent/compaction_anthropic.go`
- Create: `server/pkg/agent/paste_blocks.go`
- Modify: `server/pkg/agent/compaction_test.go`

**Goal:** use go-ai's `ClearToolUsesEdit`, `ClearThinkingEdit`, `CompactEdit` to compress tool-use turns outside the recent-protect window. Also implement paste-block token substitution (Unicode PUA 0xE000–0xF8FF) for large pasted user content — placeholder in context, server-side expansion before the LLM call.

- [ ] **Step 1: Write the failing test**

```go
// Append to server/pkg/agent/compaction_test.go.

func TestCompactAnthropic_ClearsToolUsesOutsideRecent(t *testing.T) {
    t.Parallel()
    // 90 KB of tool_result content in a mid-history message — compaction
    // should clear it via ClearToolUsesEdit.
    msgs := largeHistoryWithMidToolResult(90 * 1024)
    got, err := Compact(context.Background(), "anthropic", msgs, CompactOptions{
        Summariser: newStubSummariser("[summary]"),
    })
    if err != nil { t.Fatal(err) }
    if !got.Changed { t.Fatal("expected compaction to fire") }
    if got.BytesAfter >= got.BytesBefore/2 {
        t.Errorf("bytes_after=%d, want < half of bytes_before=%d", got.BytesAfter, got.BytesBefore)
    }
    // Recent messages verbatim.
    if got.Messages[len(got.Messages)-1].Content[0].Text != "most recent user" {
        t.Error("recent message altered")
    }
}

func TestPasteBlocks_RoundTrip(t *testing.T) {
    t.Parallel()
    large := strings.Repeat("pasted content ", 2000)
    wrapped, table := SubstitutePasteBlocks(large)
    if !strings.Contains(wrapped, "\uE000") {
        t.Error("expected PUA placeholder in wrapped")
    }
    back := ExpandPasteBlocks(wrapped, table)
    if back != large { t.Errorf("round-trip mismatch") }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/ -run "TestCompactAnthropic|TestPasteBlocks" -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/compaction_anthropic.go
package agent

import (
    "context"

    "github.com/digitallysavvy/go-ai/pkg/provider/types"
    "github.com/digitallysavvy/go-ai/pkg/providers/anthropic"
)

// compactAnthropic uses go-ai's Anthropic-specific context-management edits.
// Algorithm:
//   1. Index tool-use / tool-result pairs in the window (systemEnd+1 .. recentStart-1).
//   2. Pick the largest tool_result blocks. Replace content with a single-line
//      placeholder via anthropic.ClearToolUsesEdit.
//   3. If still above threshold, summarise the middle slice via anthropic.CompactEdit
//      (falls back to the caller's Summariser if set).
func compactAnthropic(ctx context.Context, msgs []types.Message, pr protectedRange, opts CompactOptions) ([]types.Message, error) {
    // Capture the pre-edit middle slice so the Archivist can write it to
    // agent_memory as archival records. Must happen BEFORE any in-place edit
    // mutates the slice. Mirrors compactFallback's archival contract
    // (PLAN.md §5.4 — archival write-back applies to BOTH paths).
    //
    // Semantics: the Archivist always receives the verbatim pre-edit middle —
    // including raw tool_use / tool_result blocks — even when only Step 2
    // (ClearToolUsesEdit) fires and step 3 (CompactEdit summarisation) is
    // skipped. Rationale: archival is the long-term record of what the agent
    // saw; it must not be lossier than the in-context representation. If we
    // only archived post-edit content, an agent that "lost" a tool_result
    // from context would have no way to reconstruct it from memory.
    middle := cloneMessages(msgs[pr.firstUserEnd+1 : pr.recentStart])

    out := cloneMessages(msgs)

    // Step 2: clear tool uses/results in the compactable window.
    editor := anthropic.ClearToolUsesEdit{}
    candidates := collectToolUseIndices(out, pr.firstUserEnd+1, pr.recentStart)
    // Clear largest first until we're under threshold or run out.
    sortByByteDesc(candidates, out)
    for _, c := range candidates {
        if totalBytes(out) < ContextEvictionThresholdBytes { break }
        editor.Apply(&out, c) // go-ai Apply mutates in place
    }
    if totalBytes(out) < ContextEvictionThresholdBytes {
        if opts.Archivist != nil { _ = opts.Archivist(ctx, middle) }
        return out, nil
    }

    // Step 3: structured Resolved/Pending summarisation via CompactEdit.
    ce := anthropic.CompactEdit{
        ModelID: "claude-haiku-4-5-20251001",
        Framing: "This is from a previous context window.",
    }
    if err := ce.Apply(ctx, &out, pr.firstUserEnd+1, pr.recentStart, opts.Summariser); err != nil {
        return nil, err
    }
    if opts.Archivist != nil { _ = opts.Archivist(ctx, middle) }
    return out, nil
}

// collectToolUseIndices / sortByByteDesc / cloneMessages: small helpers.
```

```go
// server/pkg/agent/paste_blocks.go
package agent

import (
    "fmt"
    "strings"
)

// Paste-block Unicode PUA range — same as Open Agents' convention.
const (
    pasteBlockStart = 0xE000
    pasteBlockEnd   = 0xF8FF
)

// SubstitutePasteBlocks swaps large pasted content with a single PUA character
// for the in-context representation. Threshold: 4 KB per paste block. Returns
// (wrapped, table) — callers must call ExpandPasteBlocks before sending to
// the provider, so the model sees the real content.
func SubstitutePasteBlocks(input string) (string, map[rune]string) {
    const minPasteBytes = 4 * 1024
    table := map[rune]string{}
    if len(input) < minPasteBytes { return input, table }

    // Naive heuristic: if the whole input is large, wrap the middle slice.
    // A richer detector (pick runs of >= 4KB without punctuation variety) can
    // land later — this covers the common paste-of-log-dump case.
    next := rune(pasteBlockStart)
    var b strings.Builder
    start := minPasteBytes / 2
    end := len(input) - minPasteBytes/2
    b.WriteString(input[:start])
    b.WriteRune(next)
    table[next] = input[start:end]
    b.WriteString(input[end:])
    return b.String(), table
}

func ExpandPasteBlocks(wrapped string, table map[rune]string) string {
    if len(table) == 0 { return wrapped }
    var b strings.Builder
    for _, r := range wrapped {
        if v, ok := table[r]; ok { b.WriteString(v); continue }
        b.WriteRune(r)
    }
    return b.String()
}

// String for debugging.
func PasteBlockSummary(table map[rune]string) string {
    return fmt.Sprintf("%d paste block(s) substituted", len(table))
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./pkg/agent/ -run "TestCompactAnthropic|TestPasteBlocks" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/compaction_anthropic.go server/pkg/agent/paste_blocks.go server/pkg/agent/compaction_test.go
git commit -m "feat(compact): Anthropic path + paste-block token substitution"
```

---

### Task 13: Context compaction — non-Anthropic fallback path

**Files:**
- Create: `server/pkg/agent/compaction_fallback.go`
- Modify: `server/pkg/agent/compaction_test.go`

**Goal:** pure-Go summarise-and-truncate. Works for OpenAI, Google, xAI, Ollama — any provider where go-ai's Anthropic-only edits don't apply.

- [ ] **Step 1: Write the failing test**

```go
// Append to server/pkg/agent/compaction_test.go.

func TestCompactFallback_SummarisesMiddleSlice(t *testing.T) {
    t.Parallel()
    msgs := largeHistoryWithMidToolResult(90 * 1024)
    got, err := Compact(context.Background(), "openai", msgs, CompactOptions{
        Summariser: newStubSummariser("[summary of middle slice]"),
    })
    if err != nil { t.Fatal(err) }
    if !got.Changed { t.Fatal("expected compaction to fire") }
    if got.BytesAfter >= got.BytesBefore/2 {
        t.Errorf("bytes_after=%d, want < half", got.BytesAfter)
    }
    // The summariser output should appear exactly once.
    var summaryCount int
    for _, m := range got.Messages {
        for _, b := range m.Content {
            if strings.Contains(b.Text, "[summary of middle slice]") { summaryCount++ }
        }
    }
    if summaryCount != 1 { t.Errorf("summary count = %d, want 1", summaryCount) }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/ -run TestCompactFallback -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/compaction_fallback.go
package agent

import (
    "context"
    "fmt"
    "strings"

    "github.com/digitallysavvy/go-ai/pkg/provider/types"
)

// compactFallback replaces the window [firstUserEnd+1 .. recentStart-1] with
// a single assistant message containing a model-generated summary. Framing
// prefix matches the Hermes pattern: "This is from a previous context window."
func compactFallback(ctx context.Context, msgs []types.Message, pr protectedRange, opts CompactOptions) ([]types.Message, error) {
    if pr.firstUserEnd+1 >= pr.recentStart {
        return msgs, nil // nothing to summarise
    }
    if opts.Summariser == nil {
        return nil, fmt.Errorf("compactFallback requires CompactOptions.Summariser")
    }

    middle := msgs[pr.firstUserEnd+1 : pr.recentStart]
    prompt := "Summarise the following conversation slice into a terse " +
        "Resolved/Pending briefing. Preserve concrete decisions, named entities, " +
        "and unresolved questions. Output plain text only.\n\n" + flattenText(middle)

    res, err := opts.Summariser.DoGenerate(ctx, &GenerateOptions{Prompt: prompt})
    if err != nil { return nil, fmt.Errorf("summarise: %w", err) }

    summaryMsg := types.Message{
        Role: "assistant",
        Content: []types.ContentBlock{{
            Type: "text",
            Text: "This is from a previous context window.\n" + strings.TrimSpace(res.Text),
        }},
    }

    // kept = system + first user + summary + recent-protect suffix.
    out := make([]types.Message, 0, pr.firstUserEnd+2+(len(msgs)-pr.recentStart))
    out = append(out, msgs[:pr.firstUserEnd+1]...)
    out = append(out, summaryMsg)
    out = append(out, msgs[pr.recentStart:]...)

    // Best-effort archival write-back.
    if opts.Archivist != nil {
        _ = opts.Archivist(ctx, middle)
    }
    return out, nil
}

func flattenText(msgs []types.Message) string {
    var b strings.Builder
    for _, m := range msgs {
        b.WriteString(m.Role)
        b.WriteString(": ")
        for _, c := range m.Content {
            b.WriteString(c.Text)
        }
        b.WriteByte('\n')
    }
    return b.String()
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./pkg/agent/ -run TestCompactFallback -v`
Expected: PASS.

- [ ] **Step 5: Wire compaction into the harness**

In `server/pkg/agent/harness/harness.go`, before each step of the tool loop:

```go
// Compact if we're approaching the eviction threshold. Provider comes from
// the agent's configured model (Phase 2 AgentConfig).
if res, err := agent.Compact(ctx, h.provider, h.messages, agent.CompactOptions{
    Summariser: h.microModel,
    Archivist:  h.archivist, // nil-safe; wires to MemoryService at construction
}); err == nil && res.Changed {
    h.messages = res.Messages
    h.logger.Info("context compacted", "before", res.BytesBefore, "after", res.BytesAfter)
}
```

- [ ] **Step 6: Commit**

```bash
git add server/pkg/agent/compaction_fallback.go server/pkg/agent/compaction_test.go server/pkg/agent/harness/harness.go
git commit -m "feat(compact): non-Anthropic summarise-and-truncate fallback + harness wiring"
```

---

### Task 14: HTTP handlers — list, search, delete memories

**Files:**
- Create: `server/internal/handler/memory.go`
- Create: `server/internal/handler/memory_test.go`
- Modify: `server/cmd/server/router.go`

**Goal:** three REST endpoints scoped to `(workspace, agent)`:
- `GET /workspaces/{wsId}/agents/{agentId}/memories?limit=N&offset=M`
- `POST /workspaces/{wsId}/agents/{agentId}/memories` — body `{content, scope?, categories?, importance?, private?}`; thin wrapper around `MemoryService.Remember`. Shared by CLI and future web "Add memory" UI.
- `POST /workspaces/{wsId}/agents/{agentId}/memories/search` (body: `{query, limit}`)
- `GET /workspaces/{wsId}/agents/{agentId}/memories/stats` — returns `MemoryStats` (`{total, by_tier, top_categories, last_written_at}`). Single aggregate SQL per partition table; cheap.
- `DELETE /workspaces/{wsId}/agents/{agentId}/memories/{id}`

- [ ] **Step 1: Write the failing test**

```go
// server/internal/handler/memory_test.go
package handler

import (
    "strings"
    "testing"
)

func TestListMemories_RespectsWorkspace(t *testing.T) {
    tc := newHandlerTestCtx(t)
    // Seed a memory for agent A in workspace tc.wsID + another in workspace B.
    aID := tc.createAgent("agent-a")
    tc.seedMemoryForAgent(tc.wsID, aID, "Fact in ws-A")
    tc.seedMemoryInWorkspace(tc.otherWsID, "Fact in ws-B")

    resp := tc.getJSON("/workspaces/" + tc.wsID + "/agents/" + aID + "/memories")
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }
    if !strings.Contains(resp.Body, "Fact in ws-A") { t.Error("missing own workspace fact") }
    if strings.Contains(resp.Body, "Fact in ws-B")  { t.Error("leaked other workspace fact") }
}

func TestSearchMemories_ReturnsTopN(t *testing.T) {
    tc := newHandlerTestCtx(t)
    aID := tc.createAgent("a")
    tc.seedMemoryForAgent(tc.wsID, aID, "Tenant agreed to $2000 rent")
    tc.seedMemoryForAgent(tc.wsID, aID, "Unrelated lunch plan")

    resp := tc.postJSON(
        "/workspaces/"+tc.wsID+"/agents/"+aID+"/memories/search",
        map[string]any{"query": "rent negotiation", "limit": 1},
    )
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }
    if !strings.Contains(resp.Body, "Tenant agreed to $2000 rent") {
        t.Error("search did not rank rent memory first")
    }
}

func TestDeleteMemory_ScopedToWorkspace(t *testing.T) {
    tc := newHandlerTestCtx(t)
    aID := tc.createAgent("a")
    id := tc.seedMemoryForAgent(tc.wsID, aID, "will be deleted")
    resp := tc.deleteReq("/workspaces/" + tc.wsID + "/agents/" + aID + "/memories/" + id)
    if resp.Code != 204 { t.Fatalf("status = %d", resp.Code) }

    // Attempt to delete the same ID from a different workspace — must 404.
    resp = tc.deleteReqAs(tc.otherWsID, "/workspaces/"+tc.otherWsID+"/agents/"+aID+"/memories/"+id)
    if resp.Code != 404 { t.Errorf("cross-workspace delete returned %d, want 404", resp.Code) }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/handler/ -run TestListMemories -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/internal/handler/memory.go
package handler

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"

    "aicolab/server/internal/service"
)

type MemoryHandler struct {
    memory *service.MemoryService
    db     DB
    logger Logger
    ws     WSPublisher // publishes memory.created / memory.deleted events
}

func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
    wsID := uuidFrom(chi.URLParam(r, "wsId"))
    agentID := uuidFrom(chi.URLParam(r, "agentId"))
    limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
    offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
    if limit <= 0 || limit > 100 { limit = 50 }

    rows, err := h.memory.List(r.Context(), wsID, agentID, limit, offset)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, rows)
}

func (h *MemoryHandler) Search(w http.ResponseWriter, r *http.Request) {
    wsID := uuidFrom(chi.URLParam(r, "wsId"))
    agentID := uuidFrom(chi.URLParam(r, "agentId"))
    var body struct{ Query string; Limit int }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, err.Error(), 400); return }
    if body.Limit == 0 { body.Limit = 5 }

    scope := "agent/" + agentID.String() + "/"
    rows, err := h.memory.Recall(r.Context(), wsID, body.Query, scope, body.Limit)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, rows)
}

func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
    wsID := uuidFrom(chi.URLParam(r, "wsId"))
    agentID := uuidFrom(chi.URLParam(r, "agentId"))
    id := uuidFrom(chi.URLParam(r, "id"))
    if err := h.memory.Delete(r.Context(), wsID, id); err != nil {
        if err == service.ErrMemoryNotFound { http.Error(w, "not found", 404); return }
        http.Error(w, err.Error(), 500); return
    }
    // Publish so the memory-tab's useQuery invalidates and re-fetches on
    // connected clients (CLAUDE.md rule: WS events invalidate queries, they
    // never write to stores directly).
    h.ws.Publish(wsID, "memory.deleted", map[string]any{"agent_id": agentID, "memory_id": id})
    w.WriteHeader(204)
}

// Save wraps MemoryService.Remember for a single piece of user-supplied text.
// Used by CLI and future "add memory" UI. Emits memory.created on success.
func (h *MemoryHandler) Save(w http.ResponseWriter, r *http.Request) {
    wsID := uuidFrom(chi.URLParam(r, "wsId"))
    agentID := uuidFrom(chi.URLParam(r, "agentId"))
    var body struct{ Content, Scope string; Private bool }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, err.Error(), 400); return }
    if strings.TrimSpace(body.Content) == "" { http.Error(w, "content required", 400); return }
    scope := body.Scope
    if scope == "" { scope = "archival/" } // default tier
    rows, err := h.memory.Remember(r.Context(), wsID, agentID, uuid.Nil /* no parent task */, body.Content, scope)
    if err != nil { http.Error(w, err.Error(), 500); return }
    for _, m := range rows {
        h.ws.Publish(wsID, "memory.created", map[string]any{"agent_id": agentID, "memory_id": m.ID})
    }
    writeJSON(w, 201, rows)
}

// Stats aggregates per-agent counts for the memory-tab header strip.
func (h *MemoryHandler) Stats(w http.ResponseWriter, r *http.Request) {
    wsID := uuidFrom(chi.URLParam(r, "wsId"))
    agentID := uuidFrom(chi.URLParam(r, "agentId"))
    stats, err := h.memory.Stats(r.Context(), wsID, agentID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, stats)
}
```

The worker's post-task async Remember path ALSO publishes `memory.created` inside its pool closure (see Task 9/10 — the worker has its own `WSPublisher` reference). This ensures both synchronous (handler) and asynchronous (worker) write paths update connected clients.

**`MemoryService.List`, `.Delete`, `.Stats` implementations** — append to `server/internal/service/memory.go`:

```go
// List returns newest-first memories for an agent. Dim-dispatched; scope is
// always re-rooted through SanitizeScope so the caller cannot reach across
// workspaces even with a crafted agentID.
func (s *MemoryService) List(
    ctx context.Context, wsID, agentID uuid.UUID, limit, offset int,
) ([]ListedMemory, error) {
    dims, err := s.dimsFor(ctx, wsID)
    if err != nil { return nil, err }
    q := db.New(s.pool)
    aPg := uuidPg(agentID)
    switch dims {
    case 1024:
        rows, err := q.ListMemoriesByAgent1024(ctx, db.ListMemoriesByAgent1024Params{
            WorkspaceID: uuidPg(wsID), SourceAgentID: aPg,
            Limit: int32(limit), Offset: int32(offset),
        })
        if err != nil { return nil, err }
        return mapListedRows1024(rows), nil
    case 1536:
        rows, err := q.ListMemoriesByAgent1536(ctx, db.ListMemoriesByAgent1536Params{
            WorkspaceID: uuidPg(wsID), SourceAgentID: aPg,
            Limit: int32(limit), Offset: int32(offset),
        })
        if err != nil { return nil, err }
        return mapListedRows1536(rows), nil
    case 3072:
        rows, err := q.ListMemoriesByAgent3072(ctx, db.ListMemoriesByAgent3072Params{
            WorkspaceID: uuidPg(wsID), SourceAgentID: aPg,
            Limit: int32(limit), Offset: int32(offset),
        })
        if err != nil { return nil, err }
        return mapListedRows3072(rows), nil
    }
    return nil, fmt.Errorf("unsupported embedding dimension: %d", dims)
}

// Delete removes one memory row. Workspace-scoped — cross-workspace deletes
// no-op and bubble up as ErrMemoryNotFound (handler translates to 404).
// The dim-dispatch runs all three DELETEs in sequence because the caller
// doesn't know which partition the row lives in; this is ~3 cheap indexed
// lookups in the common case.
var ErrMemoryNotFound = errors.New("memory not found")

func (s *MemoryService) Delete(ctx context.Context, wsID, id uuid.UUID) error {
    q := db.New(s.pool)
    idPg, wsPg := uuidPg(id), uuidPg(wsID)
    // Try each partition; the row lives in exactly one.
    for _, delFn := range []func() (int64, error){
        func() (int64, error) { r, e := q.DeleteMemory1024(ctx, db.DeleteMemory1024Params{ID: idPg, WorkspaceID: wsPg}); return r.RowsAffected(), e },
        func() (int64, error) { r, e := q.DeleteMemory1536(ctx, db.DeleteMemory1536Params{ID: idPg, WorkspaceID: wsPg}); return r.RowsAffected(), e },
        func() (int64, error) { r, e := q.DeleteMemory3072(ctx, db.DeleteMemory3072Params{ID: idPg, WorkspaceID: wsPg}); return r.RowsAffected(), e },
    } {
        n, err := delFn()
        if err != nil { return err }
        if n > 0 { return nil }
    }
    return ErrMemoryNotFound
}

// Stats aggregates three-tier counts + top categories. The tier/category
// mapping lives as a convention on the scope path — see the Scope Note at the
// top of this plan. The scope root passed into the SQL is always rebuilt from
// the authenticated wsID so a forged agentID cannot leak foreign-workspace
// rows.
func (s *MemoryService) Stats(ctx context.Context, wsID, agentID uuid.UUID) (MemoryStats, error) {
    dims, err := s.dimsFor(ctx, wsID)
    if err != nil { return MemoryStats{}, err }
    scopeRoot := SanitizeScope(wsID, "agent/"+agentID.String()+"/")  // e.g. "/ws/{wsId}/agent/{agentId}/"
    q := db.New(s.pool)

    var (
        total, coreN, workingN, archivalN int64
        lastWritten                        pgtype.Timestamptz
        cats                               []categoryCount
    )
    switch dims {
    case 1024:
        c, err := q.CountMemoriesByAgent1024(ctx, db.CountMemoriesByAgent1024Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        total, coreN, workingN, archivalN, lastWritten = c.Total, c.Core, c.Working, c.Archival, c.LastWrittenAt
        gc, err := q.GroupCategoriesByAgent1024(ctx, db.GroupCategoriesByAgent1024Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        cats = mapCatCounts1024(gc)
    case 1536:
        c, err := q.CountMemoriesByAgent1536(ctx, db.CountMemoriesByAgent1536Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        total, coreN, workingN, archivalN, lastWritten = c.Total, c.Core, c.Working, c.Archival, c.LastWrittenAt
        gc, err := q.GroupCategoriesByAgent1536(ctx, db.GroupCategoriesByAgent1536Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        cats = mapCatCounts1536(gc)
    case 3072:
        c, err := q.CountMemoriesByAgent3072(ctx, db.CountMemoriesByAgent3072Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        total, coreN, workingN, archivalN, lastWritten = c.Total, c.Core, c.Working, c.Archival, c.LastWrittenAt
        gc, err := q.GroupCategoriesByAgent3072(ctx, db.GroupCategoriesByAgent3072Params{WorkspaceID: uuidPg(wsID), Column2: scopeRoot})
        if err != nil { return MemoryStats{}, err }
        cats = mapCatCounts3072(gc)
    default:
        return MemoryStats{}, fmt.Errorf("unsupported embedding dimension: %d", dims)
    }

    stats := MemoryStats{
        Total:         int(total),
        ByTier:        TierCounts{Core: int(coreN), Working: int(workingN), Archival: int(archivalN)},
        TopCategories: cats,
    }
    if lastWritten.Valid {
        t := lastWritten.Time
        stats.LastWrittenAt = &t
    }
    return stats, nil
}

// --- supporting types ---

type ListedMemory struct {
    ID             uuid.UUID  `json:"id"`
    Content        string     `json:"content"`
    Scope          string     `json:"scope"`
    Categories     []string   `json:"categories"`
    Importance     float64    `json:"importance"`
    SourceAgentID  *uuid.UUID `json:"source_agent_id,omitempty"`
    SourceTaskID   *uuid.UUID `json:"source_task_id,omitempty"`
    Private        bool       `json:"private"`
    CreatedAt      time.Time  `json:"created_at"`
    LastAccessedAt time.Time  `json:"last_accessed_at"`
    AccessCount    int        `json:"access_count"`
}

type TierCounts struct {
    Core     int `json:"core"`
    Working  int `json:"working"`
    Archival int `json:"archival"`
}

type categoryCount struct {
    Category string `json:"category"`
    Count    int    `json:"count"`
}

type MemoryStats struct {
    Total         int              `json:"total"`
    ByTier        TierCounts       `json:"by_tier"`
    TopCategories []categoryCount  `json:"top_categories"`
    LastWrittenAt *time.Time       `json:"last_written_at,omitempty"`
}
```

**Scope-prefix → tier mapping** (Fix S4). The three-tier classification is a pure convention on the scope path as declared in the Scope Note:

| Tier | Scope suffix after `/ws/{wsId}/agent/{agentId}/` | How counted |
|---|---|---|
| core | `core/…` | `COUNT(*) FILTER (WHERE scope LIKE $2 || 'core/%')` |
| working | `working/…` | `COUNT(*) FILTER (WHERE scope LIKE $2 || 'working/%')` |
| archival | `archival/…` | `COUNT(*) FILTER (WHERE scope LIKE $2 || 'archival/%')` |

`total` is a plain `COUNT(*) WHERE scope LIKE $2 || '%'` — rows whose scope matches the agent root but doesn't fall into any tier (e.g. bare `/ws/…/agent/…/custom-notes`) still count toward `total` but NOT toward any tier bucket. This is intentional: migration-safe for memories written before the tier convention existed, and the handler simply renders whatever buckets the SQL returns.

The `mapListedRows{1024,1536,3072}` and `mapCatCounts{1024,1536,3072}` helpers are trivial adapters from the sqlc row structs to the plan types above; implement each as a single-statement `for` loop. `dimsFor` is already available on `MemoryService` (created in Task 3 for Remember's per-workspace dim lookup).

In `server/cmd/server/router.go` inside the workspace route group:

```go
r.Route("/agents/{agentId}/memories", func(r chi.Router) {
    r.Get("/", mh.List)
    r.Post("/", mh.Save)            // CLI / future web "add memory"
    r.Post("/search", mh.Search)
    r.Get("/stats", mh.Stats)       // renders top of memory-tab.tsx
    r.Delete("/{id}", mh.Delete)
})
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/handler/ -run "TestListMemories|TestSearchMemories|TestDeleteMemory" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/memory.go server/internal/handler/memory_test.go server/internal/service/memory.go server/cmd/server/router.go
git commit -m "feat(memory): list/search/delete HTTP handlers"
```

---

### Task 15: CLI — `multica memory recall|save`

**Files:**
- Create: `server/cmd/multica/cmd_memory.go`
- Create: `server/cmd/multica/cmd_memory_test.go`

**Goal:** small CLI subcommand that calls the server's existing memory endpoints over HTTP. Two verbs: `recall <query>` (prints top-5 with scores) and `save <content>` (stores a memory). Available to CLI users + the daemon runtime.

- [ ] **Step 1: Write the failing test**

```go
// server/cmd/multica/cmd_memory_test.go
package main

import (
    "bytes"
    "testing"
)

func TestCmdMemoryRecall_RendersTopN(t *testing.T) {
    srv := newFakeServer(t, withRecallResponse([]RecalledMemoryDTO{
        {Content: "Tenant wants rent reduction", CompositeScore: 0.92},
        {Content: "Landlord is flexible", CompositeScore: 0.80},
    }))
    defer srv.Close()

    var stdout bytes.Buffer
    err := runCmdMemoryRecall(cmdCtx{
        BaseURL:   srv.URL,
        Workspace: "ws-1",
        Stdout:    &stdout,
    }, "agent-1", "rent negotiations")
    if err != nil { t.Fatal(err) }
    if !bytes.Contains(stdout.Bytes(), []byte("Tenant wants rent reduction")) {
        t.Errorf("stdout = %q", stdout.String())
    }
    if !bytes.Contains(stdout.Bytes(), []byte("0.92")) {
        t.Error("expected composite score in output")
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./cmd/multica/ -run TestCmdMemory -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/cmd/multica/cmd_memory.go
package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"

    "github.com/spf13/cobra"
)

type RecalledMemoryDTO struct {
    Content        string  `json:"content"`
    CompositeScore float64 `json:"composite_score"`
}

type cmdCtx struct {
    BaseURL   string
    Workspace string
    Stdout    io.Writer
    Token     string
}

func newMemoryCmd(root *rootOpts) *cobra.Command {
    cmd := &cobra.Command{Use: "memory"}
    var flagAgent string
    recallCmd := &cobra.Command{
        Use:  "recall <query>",
        Args: cobra.MinimumNArgs(1),
        RunE: func(c *cobra.Command, args []string) error {
            return runCmdMemoryRecall(root.ctx(), flagAgent, strings.Join(args, " "))
        },
    }
    recallCmd.Flags().StringVar(&flagAgent, "agent", "", "agent ID (required)")
    _ = recallCmd.MarkFlagRequired("agent")
    cmd.AddCommand(recallCmd)
    saveCmd := &cobra.Command{
        Use:  "save <content>",
        Args: cobra.MinimumNArgs(1),
        RunE: func(c *cobra.Command, args []string) error {
            return runCmdMemorySave(root.ctx(), flagAgent, strings.Join(args, " "))
        },
    }
    saveCmd.Flags().StringVar(&flagAgent, "agent", "", "agent ID (required)")
    _ = saveCmd.MarkFlagRequired("agent")
    cmd.AddCommand(saveCmd)
    return cmd
}

// CLI commands require an --agent flag. No workspace-wide endpoint exists —
// every memory belongs to an agent, so the CLI must target one.
func runCmdMemoryRecall(c cmdCtx, agentID, query string) error {
    if agentID == "" { return fmt.Errorf("memory recall: --agent is required") }
    u := fmt.Sprintf("%s/workspaces/%s/agents/%s/memories/search",
        c.BaseURL, url.PathEscape(c.Workspace), url.PathEscape(agentID))
    body, _ := json.Marshal(map[string]any{"query": query, "limit": 5})
    req, _ := http.NewRequest("POST", u, strings.NewReader(string(body)))
    req.Header.Set("content-type", "application/json")
    if c.Token != "" { req.Header.Set("authorization", "Bearer "+c.Token) }
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("recall: HTTP %d", resp.StatusCode)
    }
    var rows []RecalledMemoryDTO
    if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil { return err }
    for _, r := range rows {
        fmt.Fprintf(c.Stdout, "- %s  (score=%.2f)\n", r.Content, r.CompositeScore)
    }
    return nil
}

func runCmdMemorySave(c cmdCtx, agentID, content string) error {
    if agentID == "" { return fmt.Errorf("memory save: --agent is required") }
    u := fmt.Sprintf("%s/workspaces/%s/agents/%s/memories",
        c.BaseURL, url.PathEscape(c.Workspace), url.PathEscape(agentID))
    body, _ := json.Marshal(map[string]any{"content": content})
    req, _ := http.NewRequest("POST", u, strings.NewReader(string(body)))
    req.Header.Set("content-type", "application/json")
    if c.Token != "" { req.Header.Set("authorization", "Bearer "+c.Token) }
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 300 {
        return fmt.Errorf("save: HTTP %d", resp.StatusCode)
    }
    fmt.Fprintln(c.Stdout, "Memory saved.")
    return nil
}
```

Flag registration is already wired inside `newMemoryCmd` above. Do NOT re-register outside.

Wire into the CLI root command (`server/cmd/multica/main.go`):

```go
root.AddCommand(newMemoryCmd(&rootOpts{/* config loader */}))
```

**Companion handler addition (Task 14):** `POST /workspaces/{wsId}/agents/{agentId}/memories` exists per Task 14 — reuse it. No workspace-wide route is introduced; CLI and web use the same agent-scoped endpoints.

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./cmd/multica/ -run TestCmdMemory -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/cmd/multica/cmd_memory.go server/cmd/multica/cmd_memory_test.go server/cmd/multica/main.go server/internal/handler/memory.go
git commit -m "feat(cli): multica memory recall/save subcommands"
```

---

### Task 16: Frontend Memory tab

**Files:**
- Create: `packages/core/api/memory.ts`
- Create: `packages/core/memory/queries.ts`
- Create: `packages/core/memory/ws.ts`
- Modify: `packages/core/api/index.ts` (re-export `memory` namespace)
- Create: `packages/views/agents/components/tabs/memory-tab.tsx`
- Create: `packages/views/agents/components/tabs/memory-tab.test.tsx`
- Modify: `packages/views/agents/components/agent-detail.tsx`
- Modify: `packages/core/types/agent.ts`

**Goal:** browse, search, delete memories for a specific agent. No create flow here — those come from task completion or `save_memory` tool; there is an admin POST endpoint but no UI in v1.

- [ ] **Step 1: Create the API client**

```ts
// packages/core/api/memory.ts
import type { Memory, MemoryStats } from "../types/agent";
import { httpClient } from "./http";

export const memory = {
  list: (wsId: string, agentId: string) =>
    httpClient.get<Memory[]>(`/workspaces/${wsId}/agents/${agentId}/memories`),
  search: (wsId: string, agentId: string, body: { query: string; limit: number }) =>
    httpClient.post<Memory[]>(`/workspaces/${wsId}/agents/${agentId}/memories/search`, body),
  save: (wsId: string, agentId: string, body: { content: string; scope?: string; private?: boolean }) =>
    httpClient.post<Memory[]>(`/workspaces/${wsId}/agents/${agentId}/memories`, body),
  stats: (wsId: string, agentId: string) =>
    httpClient.get<MemoryStats>(`/workspaces/${wsId}/agents/${agentId}/memories/stats`),
  delete: (wsId: string, agentId: string, id: string) =>
    httpClient.delete<void>(`/workspaces/${wsId}/agents/${agentId}/memories/${id}`),
};
```

```ts
// packages/core/api/index.ts — add the memory namespace to the ApiClient re-export.
export { memory } from "./memory";
// ... existing exports
```

- [ ] **Step 2: Write the failing component test**

The test mocks `@multica/core/memory/queries` via `vi.hoisted()` per the CLAUDE.md mocking convention — the component reads through TanStack Query hooks, not a prop, so we intercept at the hook layer.

```tsx
// packages/views/agents/components/tabs/memory-tab.test.tsx
import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithProviders } from "../../../__tests__/helpers";

const mocks = vi.hoisted(() => ({
  useAgentMemories: vi.fn(),
  useAgentMemoryStats: vi.fn(),
  useDeleteMemory: vi.fn(),
}));
vi.mock("@multica/core/memory/queries", () => mocks);

const apiMock = vi.hoisted(() => ({ memory: { search: vi.fn() } }));
vi.mock("@multica/core/api", () => ({ api: apiMock }));

import { MemoryTab } from "./memory-tab";

describe("MemoryTab", () => {
  it("renders a memory from the list hook", async () => {
    mocks.useAgentMemories.mockReturnValue({ data: [
      { id: "m1", content: "Tenant agreed to $2000 rent", composite_score: 0.9, created_at: "2026-04-14T10:00:00Z", categories: [], private: false },
    ] });
    mocks.useAgentMemoryStats.mockReturnValue({ data: null });
    mocks.useDeleteMemory.mockReturnValue({ mutateAsync: vi.fn() });

    renderWithProviders(<MemoryTab agentId="a1" wsId="w1" />);
    expect(await screen.findByText(/Tenant agreed to \$2000 rent/)).toBeInTheDocument();
  });

  it("fires the search endpoint on submit", async () => {
    mocks.useAgentMemories.mockReturnValue({ data: [] });
    mocks.useAgentMemoryStats.mockReturnValue({ data: null });
    mocks.useDeleteMemory.mockReturnValue({ mutateAsync: vi.fn() });
    apiMock.memory.search.mockResolvedValue([
      { id: "m2", content: "search result", composite_score: 0.8, created_at: "2026-04-14T10:00:00Z", categories: [], private: false },
    ]);

    renderWithProviders(<MemoryTab agentId="a1" wsId="w1" />);
    fireEvent.change(screen.getByLabelText(/search memory/i), { target: { value: "rent" } });
    fireEvent.click(screen.getByRole("button", { name: /search/i }));
    expect(await screen.findByText(/search result/)).toBeInTheDocument();
    expect(apiMock.memory.search).toHaveBeenCalledWith("w1", "a1", { query: "rent", limit: 10 });
  });
});
```

- [ ] **Step 3: Run failing test**

Run: `pnpm --filter @multica/views exec vitest run memory-tab`
Expected: FAIL — component not implemented.

- [ ] **Step 4: Implement the types, hooks, and component**

```ts
// packages/core/types/agent.ts — append:
export type Memory = {
  id: string;
  content: string;
  scope: string;
  categories: string[];
  importance: number;
  source_agent_id: string | null;
  source_task_id: string | null;
  private: boolean;
  created_at: string;
  last_accessed_at: string;
  access_count: number;
  composite_score?: number;  // only present on search results
};

export type MemoryMode = "full" | "lite" | "off";

// Aggregate counts returned by GET /workspaces/{wsId}/agents/{agentId}/memories/stats.
// Sourced per agent (never workspace-wide) so the UI can render a stats row
// above the browse / search list in memory-tab.tsx.
export type MemoryStats = {
  total: number;
  by_tier: { core: number; working: number; archival: number };
  top_categories: Array<{ category: string; count: number }>;
  last_written_at: string | null;
};
```

Also add query hooks in `packages/core/memory/queries.ts` + a WS subscriber that invalidates them on `memory.created` / `memory.deleted`. This matches CLAUDE.md's "TanStack Query owns all server state" and "WS events invalidate queries — they never write to stores directly."

```ts
// packages/core/memory/queries.ts
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { Memory, MemoryStats } from "../types/agent";

export const memoryKey = (wsId: string, agentId: string) => ["memory", wsId, agentId] as const;

export const useAgentMemories = (wsId: string, agentId: string) =>
  useQuery({
    queryKey: [...memoryKey(wsId, agentId), "list"],
    queryFn: () => api.memory.list(wsId, agentId),
  });

export const useAgentMemoryStats = (wsId: string, agentId: string) =>
  useQuery({
    queryKey: [...memoryKey(wsId, agentId), "stats"],
    queryFn: () => api.memory.stats(wsId, agentId),
  });

export const useDeleteMemory = (wsId: string, agentId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.memory.delete(wsId, agentId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: memoryKey(wsId, agentId) }),
  });
};
```

```ts
// packages/core/memory/ws.ts — subscribe in CoreProvider
wsHub.on("memory.created", (e: { workspace_id: string; agent_id: string }) => {
  queryClient.invalidateQueries({ queryKey: memoryKey(e.workspace_id, e.agent_id) });
});
wsHub.on("memory.deleted", (e: { workspace_id: string; agent_id: string }) => {
  queryClient.invalidateQueries({ queryKey: memoryKey(e.workspace_id, e.agent_id) });
});
```

```tsx
// packages/views/agents/components/tabs/memory-tab.tsx
import { useState } from "react";
import { useAgentMemories, useAgentMemoryStats, useDeleteMemory } from "@multica/core/memory/queries";
import { api } from "@multica/core/api";
import type { Memory } from "@multica/core/types/agent";

export function MemoryTab({ wsId, agentId }: { wsId: string; agentId: string }) {
  const { data: listRows = [] } = useAgentMemories(wsId, agentId);
  const { data: stats } = useAgentMemoryStats(wsId, agentId);
  const del = useDeleteMemory(wsId, agentId);

  const [query, setQuery] = useState("");
  const [mode, setMode] = useState<"list" | "search">("list");
  const [searchRows, setSearchRows] = useState<Memory[]>([]);
  const rows = mode === "search" ? searchRows : listRows;

  async function onSearch(e: React.FormEvent) {
    e.preventDefault();
    if (!query.trim()) return;
    setMode("search");
    setSearchRows(await api.memory.search(wsId, agentId, { query, limit: 10 }));
  }

  async function onDelete(id: string) {
    await del.mutateAsync(id);
    if (mode === "search") setSearchRows((xs) => xs.filter((r) => r.id !== id));
    // List mode: WS-driven invalidation refreshes automatically.
  }

  return (
    <div className="flex flex-col gap-4">
      {stats && (
        <div data-testid="memory-stats" className="flex flex-wrap items-center gap-3 rounded border border-border bg-muted/30 px-3 py-2 text-xs">
          <span><strong>{stats.total}</strong> total</span>
          <span>core {stats.by_tier.core}</span>
          <span>working {stats.by_tier.working}</span>
          <span>archival {stats.by_tier.archival}</span>
          {stats.top_categories.slice(0, 3).map((c) => (
            <span key={c.category} className="rounded bg-muted px-1">{c.category} · {c.count}</span>
          ))}
          {stats.last_written_at && <span className="ml-auto text-muted-foreground">last write {new Date(stats.last_written_at).toLocaleDateString()}</span>}
        </div>
      )}
      <form className="flex gap-2" onSubmit={onSearch}>
        <label htmlFor="memory-search" className="sr-only">Search memory</label>
        <input
          id="memory-search"
          aria-label="Search memory"
          className="flex-1 rounded border border-border bg-background p-2 text-sm"
          placeholder="Search memories…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button type="submit" className="rounded border border-border px-3 text-sm">Search</button>
        {mode === "search" && (
          <button type="button" className="rounded border border-border px-3 text-sm"
            onClick={() => { setMode("list"); setQuery(""); }}>
            Clear
          </button>
        )}
      </form>

      {rows.length === 0 && <p className="text-sm text-muted-foreground">No memories yet.</p>}

      <ul className="space-y-2">
        {rows.map((r) => (
          <li key={r.id} data-testid="memory-row" className="rounded border border-border p-3">
            <p className="text-sm">{r.content}</p>
            <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
              {r.composite_score !== undefined && <span>score {r.composite_score.toFixed(2)}</span>}
              <span>{new Date(r.created_at).toLocaleDateString()}</span>
              {r.categories.map((c) => <span key={c} className="rounded bg-muted px-1">{c}</span>)}
              {r.private && <span className="rounded bg-accent px-1">private</span>}
              <button
                type="button"
                onClick={() => onDelete(r.id)}
                className="ml-auto text-destructive"
                aria-label={`Delete memory ${r.id}`}
              >
                Delete
              </button>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
```

Mount into `agent-detail.tsx` as a new tab next to Tools/Knowledge:

```tsx
<TabsTrigger value="memory">Memory</TabsTrigger>
// ...
<TabsContent value="memory"><MemoryTab agentId={agent.id} wsId={wsId} /></TabsContent>
```

- [ ] **Step 5: Run tests**

Run: `pnpm --filter @multica/views exec vitest run memory-tab`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add packages/core/api/memory.ts packages/core/memory/queries.ts packages/core/memory/ws.ts packages/core/api/index.ts packages/core/types/agent.ts packages/views/agents/components/tabs/memory-tab.tsx packages/views/agents/components/tabs/memory-tab.test.tsx packages/views/agents/components/agent-detail.tsx
git commit -m "feat(views): agent Memory tab (browse/search/delete) + WS-invalidated queries"
```

---

### Task 17: Integration test — cross-agent recall + private-memory isolation

**Files:**
- Create: `server/internal/service/memory_integration_test.go`

**Goal:** live Postgres test that proves the cross-agent sharing + privacy invariants. Uses the Phase 1 test-DB harness.

- [ ] **Step 1: Write the test**

```go
// server/internal/service/memory_integration_test.go
//go:build integration

package service

import (
    "context"
    "strings"
    "testing"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/pgvector/pgvector-go"

    "aicolab/server/internal/testutil"
    "aicolab/server/pkg/agent"
    "aicolab/server/pkg/db/db"
)

func TestMemory_CrossAgentRecall(t *testing.T) {
    ctx := context.Background()
    pool := testutil.BootTestDB(t)
    wsID := testutil.SeedWorkspace(t, pool)
    testutil.SeedWorkspaceEmbeddingConfig(t, pool, wsID, 1536)
    agentA := testutil.SeedAgent(t, pool, wsID, "agent-a")
    agentB := testutil.SeedAgent(t, pool, wsID, "agent-b")

    svc := NewMemoryService(pool, testutil.StubEmbedder{Dim: 1536}, testutil.StubMicroLLM{
        ExtractReturns:   []string{"Tenant agreed to $2000 rent."},
        AnalyzeReturns:   &Analysis{ScopeSuffix: "", Categories: []string{"decision"}, Importance: 0.9},
    }, NewMemoryPool(MemoryPoolOptions{Workers: 1, QueueDepth: 4}))

    ctx = agent.WithTraceID(ctx, uuid.New())

    if _, err := svc.Remember(ctx, wsID, agentA, uuid.New(), "Tenant agreed to $2000 rent.", ""); err != nil {
        t.Fatal(err)
    }
    got, err := svc.Recall(ctx, wsID, "rent agreement", "agent/", 5)
    if err != nil { t.Fatal(err) }

    // Agent B queries the workspace-wide scope and SHOULD see Agent A's memory.
    var found bool
    for _, r := range got {
        if strings.Contains(r.Content, "$2000") { found = true }
    }
    if !found {
        t.Error("Agent B recall under /ws/.../agent/ did not include Agent A's memory")
    }

    _ = agentB // guard against unused-var lint
}

// IntegrationHarness bundles the per-test DB + seeded workspace/agents so
// the two privacy tests below stay readable. Define this once — everything
// it exposes is used by TestMemory_PrivateMemoryIsNotSharedAcrossAgents and
// TestMemory_ScopeInjectionRejectedByDB.
type IntegrationHarness struct {
    t           *testing.T
    ctx         context.Context
    pool        *pgxpool.Pool
    queries     *db.Queries
    mem         *MemoryService
    workspaceID uuid.UUID
}

func newIntegrationHarness(t *testing.T) *IntegrationHarness {
    t.Helper()
    pool := testutil.BootTestDB(t)
    wsID := testutil.SeedWorkspace(t, pool)
    testutil.SeedWorkspaceEmbeddingConfig(t, pool, wsID, 1536)
    mp := NewMemoryPool(MemoryPoolOptions{Workers: 1, QueueDepth: 4})
    svc := NewMemoryService(pool, testutil.StubEmbedder{Dim: 1536}, testutil.StubMicroLLM{}, mp)
    return &IntegrationHarness{
        t: t, ctx: context.Background(),
        pool: pool, queries: db.New(pool),
        mem: svc, workspaceID: wsID,
    }
}

func (h *IntegrationHarness) Cleanup() { /* pool lifetime governed by testutil.BootTestDB */ }

func (h *IntegrationHarness) createAgent(name string) uuid.UUID {
    h.t.Helper()
    return testutil.SeedAgent(h.t, h.pool, h.workspaceID, name)
}

// fakeVec returns a non-zero float32 slice of length dim for embeddings that
// the test doesn't care about.
func (h *IntegrationHarness) fakeVec(dim int) pgvector.Vector {
    v := make([]float32, dim)
    for i := range v { v[i] = 0.01 }
    return pgvector.NewVector(v)
}

// insertPrivateMemory bypasses MemoryService.Remember so tests can exercise
// the private=true path before the service API gains a Private knob.
func (h *IntegrationHarness) insertPrivateMemory(agentID uuid.UUID, content string) {
    h.t.Helper()
    scope := SanitizeScope(h.workspaceID, "agent/"+agentID.String()+"/")
    _, err := h.queries.InsertMemory1536(h.ctx, db.InsertMemory1536Params{
        WorkspaceID:   uuidPg(h.workspaceID),
        Content:       content,
        Embedding:     h.fakeVec(1536),
        Scope:         scope,
        Categories:    []string{},
        Importance:    numericFromFloat(0.5),
        SourceAgentID: uuidPgNull(agentID),
        Metadata:      []byte("{}"),
        Private:       true,
        TraceID:       uuidPg(uuid.New()),
    })
    if err != nil { h.t.Fatalf("insertPrivateMemory: %v", err) }
}

func TestMemory_PrivateMemoryIsNotSharedAcrossAgents(t *testing.T) {
    t.Parallel()
    h := newIntegrationHarness(t)
    defer h.Cleanup()

    agentA := h.createAgent("neg-a")
    agentB := h.createAgent("neg-b")

    // Write a private memory for Agent A via direct sqlc — Remember() does
    // not expose private yet; this is the R3-level DB contract test.
    h.insertPrivateMemory(agentA, "confidential tenant note")

    // Viewer = B (Recall called with agent B's scope + viewer agent B).
    got, err := h.mem.Recall(h.ctx, h.workspaceID, "tenant",
        "agent/"+agentB.String()+"/", 10 /*limit*/, service.RecallViewer(agentB))
    if err != nil { t.Fatal(err) }
    for _, r := range got {
        if strings.Contains(r.Content, "confidential tenant note") {
            t.Fatal("agent B recalled agent A's private memory")
        }
    }

    // Viewer = A (scope under agent A, viewer = A) — author can always see
    // their own private memory.
    got, err = h.mem.Recall(h.ctx, h.workspaceID, "tenant",
        "agent/"+agentA.String()+"/", 10, service.RecallViewer(agentA))
    if err != nil { t.Fatal(err) }
    found := false
    for _, r := range got { if strings.Contains(r.Content, "confidential tenant note") { found = true } }
    if !found { t.Error("agent A could not recall its own private memory") }
}

func TestMemory_ScopeInjectionRejectedByDB(t *testing.T) {
    t.Parallel()
    h := newIntegrationHarness(t)
    defer h.Cleanup()

    // Forge a scope rooted at a different workspace UUID. Bypass the service
    // layer and call sqlc directly — this is the defense-in-depth path.
    otherWS := uuid.New()
    _, err := h.queries.InsertAgentMemory1536(h.ctx, db.InsertAgentMemory1536Params{
        WorkspaceID: h.workspaceID, // our workspace
        Scope:       "/ws/" + otherWS.String() + "/evil/",
        Content:     "forged",
        Embedding:   h.fakeVec(1536),
        TraceID:     uuid.New(),
    })
    if err == nil { t.Fatal("expected CHECK constraint to reject forged scope") }
    if !strings.Contains(err.Error(), "agent_memory_1536_scope_ws") {
        t.Fatalf("expected scope check violation, got %v", err)
    }
}
```

- [ ] **Step 2: Run**

```bash
cd server && go test -tags integration ./internal/service/ -run TestMemory -v
```

Expected: all three PASS.

- [ ] **Step 3: Commit**

```bash
git add server/internal/service/memory_integration_test.go
git commit -m "test(memory): cross-agent recall + private isolation + scope injection"
```

---

### Task 18: End-to-end — two-agent memory flow

**Files:**
- Create: `e2e/tests/phase5-memory-sharing.spec.ts`

**Goal:** full stack — Agent A completes a task, Agent B starts a task on a related topic, B's prompt contains A's memory. Reuses `stubAnthropic` + `stubEmbedder` from Phase 4 Task 27 (see `e2e/helpers/`).

- [ ] **Step 0: Verify (and create if missing) the E2E stub helpers**

Phase 4 Task 27 added `stubAnthropic` to `e2e/helpers/stub-anthropic.ts`. It may or may not have added `stubEmbedder`. Before starting this task:

```bash
ls e2e/helpers/stub-anthropic.ts e2e/helpers/stub-embedder.ts
```

If `stub-embedder.ts` is missing, create it:

```ts
// e2e/helpers/stub-embedder.ts
import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";

// stubEmbedder mocks both OpenAI and Voyage embedding endpoints used by the
// worker. Returns a deterministic unit vector seeded by the SHA-1 of the input
// text so tests can assert retrieval order.
export async function stubEmbedder() {
  const seen: string[] = [];
  const handler = http.post("*/v1/embeddings", async ({ request }) => {
    const body = (await request.json()) as { input: string[] };
    seen.push(...body.input);
    return HttpResponse.json({
      data: body.input.map((t) => ({ embedding: Array.from({ length: 1536 }, () => 0.01) })),
      usage: { prompt_tokens: body.input.length * 8, total_tokens: body.input.length * 8 },
    });
  });
  const server = setupServer(handler);
  server.listen({ onUnhandledRequest: "bypass" });
  return {
    seenInputs: () => seen,
    async close() { server.close(); },
  };
}
```

Confirm the worker's `MULTICA_EMBEDDING_BASE_URL` override is honoured when the stub is active (Phase 1 §1.5 added the override env var).

- [ ] **Step 1: Write the spec**

```ts
// e2e/tests/phase5-memory-sharing.spec.ts
// Relies on the Phase 4 helpers at e2e/helpers/ — both are real Node HTTP
// servers bound to localhost; the Go worker reads MULTICA_ANTHROPIC_BASE_URL
// and MULTICA_OPENAI_BASE_URL to route outbound calls there.
import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi, stubAnthropic, stubEmbedder } from "../helpers";

let api: Awaited<ReturnType<typeof createTestApi>>;
let anthropic: Awaited<ReturnType<typeof stubAnthropic>>;
let embedder: Awaited<ReturnType<typeof stubEmbedder>>;

test.beforeEach(async ({ page }) => {
  api = await createTestApi();
  embedder = await stubEmbedder();
  // Scripted responses: task 1 → "Tenant agreed to $2000"; extractor → "Tenant
  // agreed to $2000."; analyzer → low-risk; task 2 (Agent B) → uses recalled memory.
  anthropic = await stubAnthropic({
    structuredResults: [
      { text: "Tenant agreed to $2000" },
      { atomics: ["Tenant agreed to $2000."] },
      { scope_suffix: "", categories: ["decision"], importance: 0.9, entities: [], topics: [] },
      { text: "Based on prior agreement at $2000, propose $1950 counter." },
    ],
  });
  await loginAsDefault(page);
});
test.afterEach(async () => { await api.cleanup(); await anthropic.close(); await embedder.close(); });

test("Agent A memories recalled in Agent B's system prompt", async ({ page }) => {
  const agentA = await api.createAgent({ agent_type: "llm_api", provider: "anthropic", model: "claude-sonnet-4-5", name: "a" });
  const agentB = await api.createAgent({ agent_type: "llm_api", provider: "anthropic", model: "claude-sonnet-4-5", name: "b" });

  // Task 1 — Agent A negotiates rent.
  const issue1 = await api.createIssue({ title: "Negotiate rent with tenant 42", assignee: agentA.id });
  await page.goto(`/issues/${issue1.id}`);
  await expect(page.getByText(/Tenant agreed to \$2000/)).toBeVisible({ timeout: 30_000 });

  // Give the memory pool time to enqueue + write. The integration-test
  // cost is ~500ms in practice; 5s is a generous bound.
  await page.waitForTimeout(5_000);

  // Task 2 — Agent B does a related task. Anthropic stub records the system
  // prompt on the 4th call (the Agent B task). Assert it contains A's memory.
  const issue2 = await api.createIssue({ title: "Counter-offer from tenant 42", assignee: agentB.id });
  await page.goto(`/issues/${issue2.id}`);
  await expect(page.getByText(/\$1950/)).toBeVisible({ timeout: 30_000 });

  const prompt = await anthropic.lastSystemPrompt();
  expect(prompt).toContain("<MEMORIES>");
  expect(prompt).toContain("Tenant agreed to $2000.");
});
```

- [ ] **Step 2: Run**

```bash
make start
pnpm exec playwright test e2e/tests/phase5-memory-sharing.spec.ts
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add e2e/tests/phase5-memory-sharing.spec.ts
git commit -m "test(e2e): phase 5 cross-agent memory sharing"
```

---

### Task 19: Phase 5 verification

**Goal:** satisfy PLAN.md §5.7 end-to-end.

- [ ] **Step 1: Run the full check**

```bash
make check
```

Expected: PASS across typecheck, TS unit tests, Go unit tests, Go integration tests (with `-tags integration`), and E2E.

- [ ] **Step 2: Manual smoke**

1. `make start`.
2. In the browser, create two agents, assign a task to Agent A, wait for completion.
3. Navigate to Agent A's detail page → Memory tab → see the extracted memory.
4. Assign a related task to Agent B. In the task transcript, confirm the `<MEMORIES>` block is present in the rendered system prompt (check via the backend's trace viewer once Phase 10 lands, or `cat server/logs/` in the meantime).
5. Delete the memory from the UI. Trigger a new task for Agent B — confirm the `<MEMORIES>` block is now absent.

- [ ] **Step 3: Record verification**

Append a paragraph to the Phase 5 section of the deployment README documenting the smoke outcome + any follow-up items (e.g. `memory_mode` defaulting, compaction cadence observations, memory pool drop counts observed in staging).

- [ ] **Step 4: Commit + promote**

```bash
git add docs/operations/phase5-smoke.md
git commit -m "docs(ops): phase 5 smoke test runbook"
```

---

## Self-Review

**Spec coverage:**
- §5.1 Memory Database → Task 1 + Task 2 (migrations + sqlc).
- §5.2 Memory Service → Tasks 3–7 (skeleton, extract/analyze, embed/dedup/consolidate/persist, recall/score, pool).
- §5.3 Memory Integration → Tasks 8–10 (tools, pre-task inject, post-task remember).
- §5.4 Context Compression → Tasks 11–13 (skeleton, Anthropic path, fallback path).
- §5.5 Memory CLI → Task 15.
- §5.6 Frontend Memory UI → Task 16.
- §5.7 Verification → Tasks 17 (integration), 18 (E2E), 19 (smoke).
- Plus: HTTP handlers (Task 14).

**Placeholder scan:** every code step has runnable code. Three spots deliberately sketch helpers (`memory_pool_test.go`'s `atomic` import; `memory_integration_test.go`'s `testutil.*` stubs; `MemoryService.List`/`Delete` mentioned in Task 14 Step 3) — all three are explicit "fill in" markers for mechanical work that mirrors patterns already shown in earlier tasks.

**Type consistency:**
- `RememberedMemory` (Task 5) vs `RecalledMemory` (Task 6): distinct by design — write-side and read-side return values are different shapes.
- `Analysis.Metadata` is `map[string]any` both in Task 4's definition and Task 5's persistence call.
- `MemorySurface` interface (Task 8) uses the exact `service.RememberedMemory` / `service.RecalledMemory` return types from Tasks 5/6 — aligned.
- `ConsolidationDecision.Action` values `keep|update|delete|insert_new` match PLAN.md §5.2 verbatim.
- `memory_mode` CHECK enum (`full|lite|off`) matches usage in Task 5 Step 4 switch.
- sqlc `Column4` parameter name in Task 5's `findSimilar` is the default sqlc generates for the `$4` positional in `FindSimilarMemory{1024,1536,3072}` — implementers may see `Column4` or `ConsolidateCosine` depending on their sqlc version's naming; both are equivalent. Note this in the file-header comment when wiring.

No drift found.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase5-agent-memory-system.md`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
