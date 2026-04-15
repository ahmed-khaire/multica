# Phase 9: Advanced Cost Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** drive per-workspace LLM spend down through four orthogonal levers — Anthropic/OpenAI Batch APIs (50% base discount, stacks with prompt caching for up to 95% savings on cold-path work), dim-partitioned semantic response caching (zero-token re-serves on cosine≥0.95 matches), per-agent performance metrics with SLA breach webhooks, and a cost dashboard surfacing every optimization's impact.

**Architecture:** Batch API wraps non-realtime `agent_task_queue` entries with a `priority='batch'` flag; a dispatcher collects them into Anthropic/OpenAI batches, polls for completion, and feeds results back through the normal task-completion path. Semantic cache is two-tier per D4: (L1) Redis exact-match via `RedisAdapter` keyed by SHA-256(workspace+agent+config_hash+query_text) — avoids embedding + DB round-trip on repeated identical queries; (L2) dim-partitioned pgvector tables 1024/1536/3072 mirroring the §1.5 pattern — catches semantic near-matches (cosine ≥ 0.95). LLM-API backend checks L1 → L2 → upstream; emits `cost_event` with `cost_status='included'` on either hit. Agent metrics is an hourly+daily aggregation job rolling `agent_task_queue`/`cost_event`/`chat_message` into pre-computed buckets. Cost dashboard consumes the existing `cost_event` table plus the new metrics rollup.

**Tech Stack:** Go 1.26, pgx/sqlc, pgvector (existing from Phase 4/5), the existing Phase 2 `CostCalculator`, Phase 8A `events.Bus` for SLA-breach webhook fan-out, TanStack Query + shadcn UI + Recharts for dashboard.

---

## Scope Note

Phase 9 of 12. Depends on:
- Phase 1 (`cost_event`, `workspace_embedding_config` + dim-partition routing)
- Phase 2 (LLM API backend, `CostCalculator`, task queue)
- Phase 4 (dim-partitioned vector pattern + GIN indexes)
- Phase 5 (`Embedder` interface for query embeddings, archival-worker pattern reused)
- Phase 8A (`events.Bus` for SLA-breach webhooks)

**Not in scope — deferred:**
- **Cross-agent cache sharing** — V1 cache keyed per `(workspace_id, agent_id)`; sharing across agents requires deduplicating system prompts + tool schemas. Deferred.
- **Per-workspace cost forecasting** — dashboard shows trends; predictive ML is post-V1.
- **Streaming Batch API results** — Anthropic/OpenAI batch results are JSONL fetched post-completion; real-time streaming within a batch is not supported upstream.
- **Cache warm-up on agent creation** — cold-start is tolerable; active warming is Phase 12+.
- **Redis-backed cache** — pgvector is sufficient at V1 scale; migrating to a vector DB like Pinecone/Weaviate is post-V1.

---

## Preconditions

**From Phase 1:**

| Surface | Purpose |
|---|---|
| `cost_event` table with `trace_id`, `cost_status`, `model`, token columns (§1.1) | Cache-hit emits row with `cost_status='included'`; Phase 9 migration ADDs `response_cache_id UUID` |
| `workspace_embedding_config` + dim-partition routing helpers (`embeddings.Embedder`, `routing.go`) | Cache lookup picks the correct `response_cache_{dim}` table per workspace |
| `budget_policy` table | Dashboard shows thresholds; SLA breach alerts check against `warning_threshold` |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `LLMAPIBackend.Execute(ctx, prompt, opts)` + `CostCalculator` | Cache pre-check wraps this; post-call emits cost_event |
| `agent_task_queue` table with `priority` column — if Phase 2 shipped without `priority`, Phase 9 migration 180 adds it | Batch candidates flagged via `priority='batch'` |
| `ProviderHealth` tracker (Phase 2 Task 12) | Batch API health check reuses this — `Observe(provider, status, headers)` after batch response |

**From Phase 2 (D4 Redis baseline — Phase 9 uses the adapter):**

```go
// server/pkg/redis/adapter.go — Phase 8A added Streams + pub/sub. Phase 9
// uses only string GET/SETEX. Confirm these two methods exist:
type Adapter interface {
    Get(ctx context.Context, key string) (string, error)   // "" + nil on miss
    SetEx(ctx context.Context, key, value string, ttlSec int) error
    // ... (plus Phase 8A Streams methods)
}
```

If Phase 2/8A doesn't expose `Get`/`SetEx` verbatim, add them in a
Pre-Task 0 extension before touching Task 4 (they're trivial wrappers
around go-redis `Get`/`SetEX`).

**From Phase 4/5:**

| Surface | Purpose |
|---|---|
| `embeddings.Embedder.Embed(ctx, texts) ([][]float32, dims, err)` | Embed query text for semantic cache lookup |
| pgvector extension, GIN-friendly index conventions | `response_cache_*` mirror `agent_memory_*` shape |

**From Phase 8A:**

| Surface | Purpose |
|---|---|
| `events.Bus` | SLA-breach worker publishes `sla.breached` events; outbound webhook service delivers |

**Test helpers (extend Phase 2 testsupport):**

```go
// server/internal/testsupport/ AND server/internal/testutil/
// SeedCostEvent (singular, used by unit tests): one row with explicit
// cost_cents + cost_status. SeedCostEvents (plural, used by integration
// tests): batch-seed N rows with identical cents/status='actual'.
func SeedCostEvent(db *pgxpool.Pool, wsID, agentID uuid.UUID, centsCost int, status string) uuid.UUID
func (e *Env) SeedCostEvents(wsID, agentID uuid.UUID, count int, centsEach int)
func (e *Env) WaitForCacheHit(ctx context.Context, agentID uuid.UUID) bool
func (e *Env) SubmitBatchJob(ids []uuid.UUID) uuid.UUID
func (e *Env) SimulateBatchCompletion(batchID uuid.UUID, results map[uuid.UUID]string)
// Phase 2 already ships StubModelReplying + SeedAgent
```

**Pre-Task 0 bootstrap:**
- Add `go get github.com/anthropics/anthropic-sdk-go@latest` (Batch API client surface — if Phase 2 uses a different client, adapt).
- Add `go get github.com/openai/openai-go@latest` for OpenAI Batch API (optional — only if Phase 2 supports OpenAI as a provider).
- Add `sqlNullDec` helper to `server/internal/service/uuid_helpers.go` (the same Phase 2 file that defines `sqlNullString`/`sqlNullInt`):

  ```go
  // sqlNullDec wraps a float64 into a pgtype.Numeric suitable for DECIMAL
  // columns. Used by metrics worker for CacheHitRate + AvgResponseSeconds.
  func sqlNullDec(v float64) pgtype.Numeric {
      var n pgtype.Numeric
      _ = n.Scan(fmt.Sprintf("%.6f", v))
      n.Valid = true
      return n
  }
  ```

- Confirm `embeddings.WorkspaceDimsFor(ctx context.Context, pool *pgxpool.Pool, wsID uuid.UUID) (int, error)` exists from Phase 1 §1.5 — this is the concrete helper the plan means by "workspaceDimsFor". If Phase 1 named it differently (e.g. `routing.DimsFor`), alias locally in cache_purge.go:

  ```go
  // server/internal/worker/cache_purge.go — near top
  func workspaceDimsFor(ctx context.Context, q *db.Queries, wsID uuid.UUID) int {
      cfg, err := q.GetWorkspaceEmbeddingConfig(ctx, uuidPg(wsID))
      if err != nil { return 1536 } // default when unset
      return int(cfg.Dimensions)
  }
  ```

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/180_response_cache.up.sql` | `response_cache_{1024,1536,3072}` + `cost_event.response_cache_id` |
| `server/migrations/180_response_cache.down.sql` | Reverse |
| `server/migrations/181_agent_metric.up.sql` | `agent_metric` + SLA threshold column on agent |
| `server/migrations/181_agent_metric.down.sql` | Reverse |
| `server/pkg/db/queries/cache.sql` | sqlc: cache insert/search/invalidate per dim |
| `server/pkg/db/queries/metric.sql` | sqlc: metric upsert + lookup |
| `server/pkg/agent/cache.go` | `ResponseCache` — embed + search per dim, cosine>=0.95 gate, config_version_hash logic |
| `server/pkg/agent/cache_test.go` | Hit/miss/invalidation/cross-dim routing |
| `server/pkg/agent/batch.go` | `BatchExecutor` — collect pending batch tasks, submit to Anthropic/OpenAI, poll for completion |
| `server/pkg/agent/batch_test.go` | Batching, partial-failure, provider routing |
| `server/internal/worker/metrics.go` | Hourly + daily aggregation job |
| `server/internal/worker/metrics_test.go` | Aggregation correctness, SLA breach fires |
| `server/internal/worker/cache_purge.go` | Daily stale-cache purge (config_version_hash mismatch, expired entries) |
| `server/internal/service/cost_dashboard.go` | Dashboard data service — group cost_event by model/agent/time + cache-hit rates |
| `server/internal/service/cost_dashboard_test.go` | Grouping correctness, timezone handling |
| `server/internal/handler/cost.go` | REST: `/cost/summary`, `/cost/breakdown`, `/cost/cache-hits` |
| `server/internal/handler/metric.go` | REST: `/agents/{id}/metrics?period=hourly|daily&range=7d` |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/pkg/agent/llm_api.go` (Phase 2) | Wrap `Execute` — check `ResponseCache.Lookup` first, emit cache-hit `cost_event` on match, otherwise call upstream + insert cache entry |
| `server/internal/service/task.go` (Phase 2) | If `priority='batch'` and Batch API available for provider, enqueue via `BatchExecutor` instead of immediate claim |
| `server/cmd/server/main.go` | Start `BatchExecutor.Run` + `MetricsWorker.Run` + `CachePurgeWorker.Run` goroutines |
| `server/cmd/server/router.go` | Mount `/cost/*` and `/agents/{id}/metrics` routes |
| `server/pkg/agent/defaults.go` | Append `CacheHitThreshold = 0.95`, `BatchSubmitInterval = 5 * time.Minute`, `BatchMaxSize = 1000`, `MetricsHourlyInterval = time.Hour`, `CachePurgeInterval = 24 * time.Hour` |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/cost.ts` | `CostSummary`, `CostBreakdown`, `CacheHitRate` |
| `packages/core/types/metric.ts` | `AgentMetric`, `MetricPeriod` |
| `packages/core/api/cost.ts` + `metric.ts` | REST clients |
| `packages/core/cost/queries.ts` + `metric/queries.ts` | TanStack hooks |
| `packages/views/costs/` | Dashboard pages (summary + breakdown + cache-hit + chain viz) |
| `packages/views/agents/components/tabs/performance-tab.tsx` | Per-agent metrics tab (line charts, SLA indicator) |

### External Infrastructure

| System | Change |
|---|---|
| Anthropic Batch API | Outbound calls; existing API key |
| OpenAI Batch API | Outbound calls; existing API key |
| None else | pgvector + Phase 2 worker + Phase 8A events bus all existing |

---

### Task 1: Migration 180 — Response Cache + cost_event linkage

**Files:**
- Create: `server/migrations/180_response_cache.up.sql`
- Create: `server/migrations/180_response_cache.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 180_response_cache.up.sql
-- Three dim-partition tables mirroring §1.5 pattern.

CREATE TABLE IF NOT EXISTS response_cache_1024 (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL,
    query_embedding vector(1024) NOT NULL,
    query_text TEXT,
    response TEXT NOT NULL,
    model TEXT NOT NULL,
    config_version_hash TEXT NOT NULL,
    knowledge_version TEXT,
    hit_count INT NOT NULL DEFAULT 0,
    last_hit_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS response_cache_1536 (LIKE response_cache_1024 INCLUDING ALL);
ALTER TABLE response_cache_1536 ALTER COLUMN query_embedding TYPE vector(1536);

CREATE TABLE IF NOT EXISTS response_cache_3072 (LIKE response_cache_1024 INCLUDING ALL);
ALTER TABLE response_cache_3072 ALTER COLUMN query_embedding TYPE vector(3072);

-- Vector indexes: ivfflat for 1024/1536, hnsw for 3072 (pgvector 2000-dim cap).
CREATE INDEX IF NOT EXISTS idx_rcache_1024_vec ON response_cache_1024
    USING ivfflat (query_embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_rcache_1536_vec ON response_cache_1536
    USING ivfflat (query_embedding vector_cosine_ops) WITH (lists = 100);
CREATE INDEX IF NOT EXISTS idx_rcache_3072_vec ON response_cache_3072
    USING hnsw (query_embedding vector_cosine_ops);

-- Scoping indexes for workspace + agent lookups.
CREATE INDEX IF NOT EXISTS idx_rcache_1024_ws ON response_cache_1024(workspace_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_rcache_1536_ws ON response_cache_1536(workspace_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_rcache_3072_ws ON response_cache_3072(workspace_id, agent_id);

-- Config-version filter index — cache invalidation scans by hash mismatch.
CREATE INDEX IF NOT EXISTS idx_rcache_1024_cfg ON response_cache_1024(workspace_id, agent_id, config_version_hash);
CREATE INDEX IF NOT EXISTS idx_rcache_1536_cfg ON response_cache_1536(workspace_id, agent_id, config_version_hash);
CREATE INDEX IF NOT EXISTS idx_rcache_3072_cfg ON response_cache_3072(workspace_id, agent_id, config_version_hash);

-- Expiry sweep index (daily cache_purge worker).
CREATE INDEX IF NOT EXISTS idx_rcache_1024_expires ON response_cache_1024(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rcache_1536_expires ON response_cache_1536(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rcache_3072_expires ON response_cache_3072(expires_at) WHERE expires_at IS NOT NULL;

-- Add response_cache_id column to cost_event per PLAN.md §9.2 — links
-- every `cost_status='included'` row back to the cache entry that served
-- the traffic. Nullable; pre-Phase-9 cost_events stay valid.
ALTER TABLE cost_event ADD COLUMN IF NOT EXISTS response_cache_id UUID;
CREATE INDEX IF NOT EXISTS idx_cost_event_cache ON cost_event(response_cache_id)
    WHERE response_cache_id IS NOT NULL;

-- Also add priority column to agent_task_queue for Batch API routing (§9.1).
-- Pre-Phase-9 rows default to 'normal'; the batch executor only claims
-- rows where priority='batch'.
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS priority TEXT NOT NULL DEFAULT 'normal';
ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_priority_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_priority_check
    CHECK (priority IN ('normal', 'batch', 'high')) NOT VALID;
ALTER TABLE agent_task_queue VALIDATE CONSTRAINT agent_task_queue_priority_check;
-- batch_submitted is a transient status between claim and completeBatch —
-- the ClaimBatchTasks UPDATE flips rows from pending → batch_submitted
-- atomically; ListPendingBatchTasks excludes submitted rows so the next
-- RunOnce tick never re-submits them.
CREATE INDEX IF NOT EXISTS idx_task_queue_batch ON agent_task_queue(priority, status, created_at)
    WHERE priority = 'batch';

-- batch_job persistence (R2 B3). Without this, a server restart loses
-- in-flight batch goroutines and pending tasks get stuck forever. On
-- boot the BatchExecutor reconciler scans status='submitted' rows and
-- resumes poll-and-complete for each one.
CREATE TABLE IF NOT EXISTS batch_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,              -- 'anthropic' | 'openai'
    external_batch_id TEXT NOT NULL,     -- the ID returned by prov.Submit
    task_ids UUID[] NOT NULL,            -- tasks grouped into this batch
    status TEXT NOT NULL,                -- 'submitted' | 'completed' | 'failed'
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (provider, external_batch_id)
);
ALTER TABLE batch_job DROP CONSTRAINT IF EXISTS batch_job_status_check;
ALTER TABLE batch_job ADD CONSTRAINT batch_job_status_check
    CHECK (status IN ('submitted', 'completed', 'failed')) NOT VALID;
ALTER TABLE batch_job VALIDATE CONSTRAINT batch_job_status_check;
CREATE INDEX IF NOT EXISTS idx_batch_job_active ON batch_job(provider, status)
    WHERE status = 'submitted';
```

- [ ] **Step 2: Down migration**

```sql
-- 180_response_cache.down.sql
DROP INDEX IF EXISTS idx_batch_job_active;
DROP TABLE IF EXISTS batch_job;
DROP INDEX IF EXISTS idx_task_queue_batch;
ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_priority_check;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS priority;
DROP INDEX IF EXISTS idx_cost_event_cache;
ALTER TABLE cost_event DROP COLUMN IF EXISTS response_cache_id;
DROP TABLE IF EXISTS response_cache_3072;
DROP TABLE IF EXISTS response_cache_1536;
DROP TABLE IF EXISTS response_cache_1024;
```

- [ ] **Step 3: Apply + rollback cycle + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/180_response_cache.up.sql server/migrations/180_response_cache.down.sql
git commit -m "feat(cache): migration 180 — response_cache dim-partitions + cost_event link

Mirrors §1.5 dim-partition pattern: ivfflat for 1024/1536, hnsw for 3072.
Adds cost_event.response_cache_id + agent_task_queue.priority columns —
needed by cache-hit accounting and Batch API routing respectively."
```

---

### Task 2: Migration 181 — Agent Metrics + SLA Threshold

**Files:**
- Create: `server/migrations/181_agent_metric.up.sql`
- Create: `server/migrations/181_agent_metric.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 181_agent_metric.up.sql
CREATE TABLE IF NOT EXISTS agent_metric (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_type TEXT NOT NULL,
    tasks_completed INT NOT NULL DEFAULT 0,
    tasks_failed INT NOT NULL DEFAULT 0,
    tasks_timed_out INT NOT NULL DEFAULT 0,
    avg_resolution_seconds INT,
    p95_resolution_seconds INT,
    delegations_sent INT NOT NULL DEFAULT 0,
    delegations_received INT NOT NULL DEFAULT 0,
    chat_messages_handled INT NOT NULL DEFAULT 0,
    avg_response_seconds DECIMAL,
    total_cost_cents INT NOT NULL DEFAULT 0,
    avg_cost_per_task_cents INT NOT NULL DEFAULT 0,
    cache_hits INT NOT NULL DEFAULT 0,                -- populated from response_cache_id count
    cache_hit_rate DECIMAL,                            -- cache_hits / (cache_hits + non_cached)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (agent_id, period_start, period_type),
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE CASCADE
);
ALTER TABLE agent_metric DROP CONSTRAINT IF EXISTS agent_metric_period_check;
ALTER TABLE agent_metric ADD CONSTRAINT agent_metric_period_check
    CHECK (period_type IN ('hourly', 'daily')) NOT VALID;
ALTER TABLE agent_metric VALIDATE CONSTRAINT agent_metric_period_check;
CREATE INDEX IF NOT EXISTS idx_agent_metric_lookup ON agent_metric(workspace_id, agent_id, period_type, period_start DESC);

-- SLA threshold per agent. NULL = no SLA monitored.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS sla_p95_seconds INT;
```

- [ ] **Step 2: Down + apply + commit**

```sql
-- 181_agent_metric.down.sql
ALTER TABLE agent DROP COLUMN IF EXISTS sla_p95_seconds;
DROP TABLE IF EXISTS agent_metric;
```

```bash
cd server && make migrate-up
git add server/migrations/181_agent_metric.up.sql server/migrations/181_agent_metric.down.sql
git commit -m "feat(metrics): migration 181 — agent_metric + sla_p95_seconds

Hourly + daily rollup buckets with uniqueness gate. cache_hit_rate
populated by metrics worker (Task 8). agent.sla_p95_seconds=NULL means
no SLA monitored — SLA-breach worker skips such agents."
```

---

### Task 3: sqlc Queries — Cache + Metric

**Files:**
- Create: `server/pkg/db/queries/cache.sql`
- Create: `server/pkg/db/queries/metric.sql`

- [ ] **Step 1: Write cache.sql**

```sql
-- cache.sql

-- name: InsertCache1024 :one
INSERT INTO response_cache_1024 (workspace_id, agent_id, query_embedding, query_text, response, model, config_version_hash, knowledge_version, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: SearchCache1024 :one
-- Returns the highest-similarity match above the cosine threshold,
-- scoped to (workspace_id, agent_id, config_version_hash). Returns 1 row
-- or pgx.ErrNoRows if nothing matches. The caller converts no-rows into
-- a cache miss; any other error surfaces.
SELECT *, 1 - (query_embedding <=> $3) AS similarity
FROM response_cache_1024
WHERE workspace_id = $1
  AND agent_id = $2
  AND config_version_hash = $4
  AND (expires_at IS NULL OR expires_at > NOW())
  AND (1 - (query_embedding <=> $3)) >= $5
ORDER BY query_embedding <=> $3
LIMIT 1;

-- name: BumpCacheHit1024 :exec
UPDATE response_cache_1024
SET hit_count = hit_count + 1, last_hit_at = NOW()
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteStaleCache1024 :execrows
-- Used by cache_purge worker. Deletes entries whose config_version_hash
-- no longer matches the agent's current config, whose knowledge_version
-- no longer matches the agent's current knowledge snapshot (PLAN.md §9.2 —
-- knowledge updates also invalidate cached responses), OR whose
-- expires_at has passed. Both versions are computed app-side per agent.
--
-- `$4::text IS NULL` lets callers skip the knowledge-version check for
-- agents without knowledge sources — otherwise the filter would delete
-- every row.
DELETE FROM response_cache_1024
WHERE workspace_id = $1
  AND agent_id = $2
  AND (
       config_version_hash <> $3
    OR ($4::text IS NOT NULL AND knowledge_version IS DISTINCT FROM $4)
    OR (expires_at IS NOT NULL AND expires_at <= NOW())
  );

-- (Repeat the four queries for _1536 and _3072 by substituting the table
-- name and the vector dimension — same shape, different partition.)

-- name: InsertCache1536 :one
INSERT INTO response_cache_1536 (workspace_id, agent_id, query_embedding, query_text, response, model, config_version_hash, knowledge_version, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: SearchCache1536 :one
SELECT *, 1 - (query_embedding <=> $3) AS similarity FROM response_cache_1536
WHERE workspace_id = $1 AND agent_id = $2 AND config_version_hash = $4
  AND (expires_at IS NULL OR expires_at > NOW())
  AND (1 - (query_embedding <=> $3)) >= $5
ORDER BY query_embedding <=> $3 LIMIT 1;

-- name: BumpCacheHit1536 :exec
UPDATE response_cache_1536 SET hit_count = hit_count + 1, last_hit_at = NOW()
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteStaleCache1536 :execrows
DELETE FROM response_cache_1536 WHERE workspace_id = $1 AND agent_id = $2
  AND (
       config_version_hash <> $3
    OR ($4::text IS NOT NULL AND knowledge_version IS DISTINCT FROM $4)
    OR (expires_at IS NOT NULL AND expires_at <= NOW())
  );

-- name: InsertCache3072 :one
INSERT INTO response_cache_3072 (workspace_id, agent_id, query_embedding, query_text, response, model, config_version_hash, knowledge_version, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: SearchCache3072 :one
SELECT *, 1 - (query_embedding <=> $3) AS similarity FROM response_cache_3072
WHERE workspace_id = $1 AND agent_id = $2 AND config_version_hash = $4
  AND (expires_at IS NULL OR expires_at > NOW())
  AND (1 - (query_embedding <=> $3)) >= $5
ORDER BY query_embedding <=> $3 LIMIT 1;

-- name: BumpCacheHit3072 :exec
UPDATE response_cache_3072 SET hit_count = hit_count + 1, last_hit_at = NOW()
WHERE id = $1 AND workspace_id = $2;

-- Batch job persistence + claim queries (R2 B3 + S5).

-- name: ClaimBatchTasks :many
-- Atomic claim: UPDATE pending → batch_submitted + return the claimed
-- rows for batch assembly. FOR UPDATE SKIP LOCKED lets multiple
-- replicas run RunOnce concurrently; each grabs a disjoint slice.
WITH claimed AS (
    SELECT id FROM agent_task_queue
    WHERE priority = 'batch' AND status = 'pending'
    ORDER BY created_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE agent_task_queue t
SET status = 'batch_submitted'
FROM claimed c
WHERE t.id = c.id
RETURNING t.*;

-- name: UnclaimBatchTasks :exec
-- Restore status=pending when Submit/InsertBatchJob fails after claim.
UPDATE agent_task_queue
SET status = 'pending'
WHERE id = ANY($1::uuid[]) AND status = 'batch_submitted';

-- name: InsertBatchJob :one
INSERT INTO batch_job (provider, external_batch_id, task_ids, status)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: ListSubmittedBatchJobs :many
-- Driver query for resumeInFlight on BatchExecutor.Run boot.
SELECT * FROM batch_job WHERE status = 'submitted';

-- name: MarkBatchJobComplete :exec
UPDATE batch_job
SET status = $2, completed_at = NOW()
WHERE id = $1;

-- name: DeleteStaleCache3072 :execrows
DELETE FROM response_cache_3072 WHERE workspace_id = $1 AND agent_id = $2
  AND (
       config_version_hash <> $3
    OR ($4::text IS NOT NULL AND knowledge_version IS DISTINCT FROM $4)
    OR (expires_at IS NOT NULL AND expires_at <= NOW())
  );
```

- [ ] **Step 2: Write metric.sql**

```sql
-- metric.sql

-- name: UpsertAgentMetric :one
INSERT INTO agent_metric (
    workspace_id, agent_id, period_start, period_type,
    tasks_completed, tasks_failed, tasks_timed_out,
    avg_resolution_seconds, p95_resolution_seconds,
    delegations_sent, delegations_received,
    chat_messages_handled, avg_response_seconds,
    total_cost_cents, avg_cost_per_task_cents,
    cache_hits, cache_hit_rate
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (agent_id, period_start, period_type) DO UPDATE SET
    tasks_completed = EXCLUDED.tasks_completed,
    tasks_failed = EXCLUDED.tasks_failed,
    tasks_timed_out = EXCLUDED.tasks_timed_out,
    avg_resolution_seconds = EXCLUDED.avg_resolution_seconds,
    p95_resolution_seconds = EXCLUDED.p95_resolution_seconds,
    delegations_sent = EXCLUDED.delegations_sent,
    delegations_received = EXCLUDED.delegations_received,
    chat_messages_handled = EXCLUDED.chat_messages_handled,
    avg_response_seconds = EXCLUDED.avg_response_seconds,
    total_cost_cents = EXCLUDED.total_cost_cents,
    avg_cost_per_task_cents = EXCLUDED.avg_cost_per_task_cents,
    cache_hits = EXCLUDED.cache_hits,
    cache_hit_rate = EXCLUDED.cache_hit_rate
RETURNING *;

-- name: ListAgentMetrics :many
SELECT * FROM agent_metric
WHERE workspace_id = $1 AND agent_id = $2 AND period_type = $3
  AND period_start >= $4 AND period_start < $5
ORDER BY period_start ASC;

-- name: AggregateCompletedTasks :one
-- Raw-data driver query for the hourly aggregation step. Called once per
-- (agent_id, hour-bucket) pair the worker needs to populate.
SELECT
    COUNT(*) FILTER (WHERE status = 'completed') AS completed,
    COUNT(*) FILTER (WHERE status = 'failed')    AS failed,
    COUNT(*) FILTER (WHERE status = 'timed_out') AS timed_out,
    AVG(EXTRACT(EPOCH FROM (completed_at - started_at)))::int AS avg_sec,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (completed_at - started_at)))::int AS p95_sec
FROM agent_task_queue
WHERE workspace_id = $1 AND agent_id = $2
  AND completed_at IS NOT NULL
  AND completed_at >= $3 AND completed_at < $4;

-- name: AggregateCosts :one
SELECT
    COALESCE(SUM(cost_cents), 0)::int AS total_cents,
    COUNT(*) FILTER (WHERE response_cache_id IS NOT NULL) AS cache_hits
FROM cost_event
WHERE workspace_id = $1 AND agent_id = $2
  AND created_at >= $3 AND created_at < $4;

-- name: AggregateDelegations :one
-- Counts sent vs received delegation tool-call events in the window.
-- Sent: cost_event for a parent task with response_cache_id IS NULL and
-- a matching 'task'-tool tool_call_name (Phase 6). Received: the child
-- agent's cost_event tagged with agent_id. V1 approximation: count
-- cost_events whose trace_id was created by a `task` tool invocation.
-- A follow-up (post-V1) adds a dedicated delegation_event table; for now
-- we use trace_id-join approximation.
SELECT
    0::int AS sent, -- stubbed: wire to workflow_step_run 'sub_workflow' count once Phase 7 telemetry lands
    0::int AS received
FROM cost_event
WHERE workspace_id = $1 AND agent_id = $2
  AND created_at >= $3 AND created_at < $4
LIMIT 1;

-- name: AggregateChatMessages :one
-- Messages the agent RESPONDED to in the window (assistant rows).
SELECT
    COUNT(*) FILTER (WHERE role = 'assistant')::int AS handled,
    -- time from user_message.created_at to assistant_message.created_at
    -- avg over the window. NULL if no user-followed-by-assistant pairs.
    NULL::decimal AS avg_response_seconds
FROM chat_message
WHERE workspace_id = $1 AND agent_id = $2
  AND created_at >= $3 AND created_at < $4;

-- name: CountDailyMetricsForDay :one
-- Used by MetricsWorker startup catch-up (R2 N3) to decide whether
-- yesterday's rollup already ran before rebooting.
SELECT COUNT(*)::int FROM agent_metric
WHERE period_type = 'daily' AND period_start = $1;

-- name: ListAgentsWithSLA :many
-- Intentionally global: the single in-process metrics worker iterates
-- every workspace's SLA-monitored agents on each tick (same rationale as
-- Phase 7's ListActiveScheduledWorkflows). NEVER call this from a
-- request-scoped handler — doing so exposes cross-tenant SLA configs.
-- A per-agent workspace filter lives on the caller path (metrics.go) via
-- the Workspace_id row we get back, NOT on this SELECT.
SELECT id, workspace_id, sla_p95_seconds FROM agent
WHERE sla_p95_seconds IS NOT NULL;
```

- [ ] **Step 3: Regenerate + commit**

```bash
cd server && make sqlc && go build ./...
git add server/pkg/db/queries/cache.sql server/pkg/db/queries/metric.sql server/pkg/db/generated/
git commit -m "feat(db): sqlc queries for semantic cache + agent metrics

Cache queries split per dim partition (same shape, different vector size).
AggregateCompletedTasks uses PERCENTILE_CONT(0.95) for the SLA p95 metric."
```

---

### Task 4: ResponseCache — Embed + Search + Insert

**Files:**
- Create: `server/pkg/agent/cache.go`
- Create: `server/pkg/agent/cache_test.go`

**Goal:** `ResponseCache.Lookup(ctx, wsID, agentID, queryText, cfg) (hit *CacheEntry, err)` — returns a cache hit if cosine ≥ 0.95 for a row matching `config_version_hash`. `Insert(ctx, ...)` stores a fresh entry.

- [ ] **Step 1: Write failing test**

```go
// server/pkg/agent/cache_test.go
package agent

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestCache_HitOnNearIdenticalQuery(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "cache")
    agentID := testutil.SeedAgent(db, wsID, "support", "anthropic", "claude-haiku-4-5-20251001")
    // Workspace embedding config defaults to 1536 dims (§1.5).
    testutil.SeedEmbeddingConfig(db, wsID, 1536)

    cache := NewResponseCache(db, testutil.StubEmbedder{Dim: 1536, Fixed: ones(1536)}, nil /* no L1 in unit tests */)

    _, err := cache.Insert(context.Background(), InsertCacheArgs{
        WorkspaceID: wsID, AgentID: agentID,
        QueryText: "what are your hours?",
        Response:  "Mon-Fri 9-5",
        Model:     "claude-haiku-4-5-20251001",
        ConfigVersionHash: "h1",
    })
    if err != nil { t.Fatal(err) }

    hit, err := cache.Lookup(context.Background(), LookupArgs{
        WorkspaceID: wsID, AgentID: agentID,
        QueryText: "tell me your hours", // different text but same embedding in stub
        ConfigVersionHash: "h1",
    })
    if err != nil { t.Fatal(err) }
    if hit == nil { t.Fatal("expected cache hit") }
    if hit.Response != "Mon-Fri 9-5" { t.Errorf("response = %q", hit.Response) }
}

func TestCache_MissOnConfigHashChange(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "cache")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedEmbeddingConfig(db, wsID, 1536)

    cache := NewResponseCache(db, testutil.StubEmbedder{Dim: 1536, Fixed: ones(1536)}, nil /* no L1 in unit tests */)
    _, _ = cache.Insert(context.Background(), InsertCacheArgs{
        WorkspaceID: wsID, AgentID: agentID, QueryText: "q",
        Response: "r", Model: "m", ConfigVersionHash: "old-hash",
    })

    hit, err := cache.Lookup(context.Background(), LookupArgs{
        WorkspaceID: wsID, AgentID: agentID,
        QueryText: "q", ConfigVersionHash: "new-hash", // config changed
    })
    if err != nil { t.Fatal(err) }
    if hit != nil { t.Error("expected cache miss after config hash change") }
    _ = time.Second
}

func TestCache_MissBelowThreshold(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "cache")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedEmbeddingConfig(db, wsID, 1536)

    // Embedder that returns orthogonal vectors for different inputs — so
    // cosine similarity drops to ~0, well below the 0.95 threshold.
    cache := NewResponseCache(db, testutil.StubEmbedderOrthogonal{Dim: 1536}, nil)
    _, _ = cache.Insert(context.Background(), InsertCacheArgs{
        WorkspaceID: wsID, AgentID: agentID, QueryText: "first",
        Response: "r1", Model: "m", ConfigVersionHash: "h",
    })
    hit, _ := cache.Lookup(context.Background(), LookupArgs{
        WorkspaceID: wsID, AgentID: agentID,
        QueryText: "totally unrelated", ConfigVersionHash: "h",
    })
    if hit != nil { t.Error("expected miss below threshold; got hit") }
}

func ones(n int) []float32 { v := make([]float32, n); for i := range v { v[i] = 1 }; return v }
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/ -run TestCache -v`
Expected: FAIL — `NewResponseCache` and `ResponseCache` undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/cache.go
package agent

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgtype"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/pgvector/pgvector-go"

    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/embeddings"
    "aicolab/server/pkg/redis"
)

type CacheEntry struct {
    ID                uuid.UUID `json:"id"`
    Response          string    `json:"response"`
    Model             string    `json:"model"`
    Similarity        float64   `json:"-"` // populated at Lookup time, not persisted in L1 JSON
    ConfigVersionHash string    `json:"config_version_hash"`
}

type InsertCacheArgs struct {
    WorkspaceID, AgentID uuid.UUID
    QueryText            string
    Response             string
    Model                string
    ConfigVersionHash    string
    KnowledgeVersion     string
    TTL                  time.Duration // 0 = no expiry
}

type LookupArgs struct {
    WorkspaceID, AgentID uuid.UUID
    QueryText            string
    ConfigVersionHash    string
}

type ResponseCache struct {
    pool     *pgxpool.Pool
    embedder embeddings.Embedder
    redis    redis.Adapter // L1 exact-match layer (D4)
}

func NewResponseCache(pool *pgxpool.Pool, em embeddings.Embedder, r redis.Adapter) *ResponseCache {
    return &ResponseCache{pool: pool, embedder: em, redis: r}
}

// l1Key derives the Redis key for exact-match lookup. SHA-256 covers
// collisions; the key includes workspace + agent + config hash so
// rolled-back agents don't read a stale entry from a previous config.
func l1Key(wsID, agentID uuid.UUID, configHash, queryText string) string {
    h := sha256.New()
    fmt.Fprintf(h, "%s|%s|%s|%s", wsID, agentID, configHash, queryText)
    return "rcache:l1:" + hex.EncodeToString(h.Sum(nil))
}

// Lookup is two-tier per D4. L1: Redis exact-match keyed by SHA-256 of
// (wsID, agentID, configHash, queryText) — covers identical-query
// re-serves at sub-millisecond latency without embedding. L2: dim-
// partitioned pgvector cosine search — catches semantic near-matches
// (rephrased questions). Nil hit + nil error means miss on both tiers.
func (c *ResponseCache) Lookup(ctx context.Context, a LookupArgs) (*CacheEntry, error) {
    // L1: Redis exact-match.
    if c.redis != nil {
        key := l1Key(a.WorkspaceID, a.AgentID, a.ConfigVersionHash, a.QueryText)
        if raw, err := c.redis.Get(ctx, key); err == nil && raw != "" {
            var e CacheEntry
            if json.Unmarshal([]byte(raw), &e) == nil {
                e.Similarity = 1.0 // exact match
                return &e, nil
            }
        }
    }

    // L2: pgvector cosine search.
    vecs, dims, err := c.embedder.Embed(ctx, []string{a.QueryText})
    if err != nil { return nil, fmt.Errorf("embed: %w", err) }
    if len(vecs) == 0 { return nil, nil }
    vec := pgvector.NewVector(vecs[0])

    q := db.New(c.pool)
    switch dims {
    case 1024:
        row, err := q.SearchCache1024(ctx, db.SearchCache1024Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, ConfigVersionHash: a.ConfigVersionHash,
            Column5: CacheHitThreshold,
        })
        return rowToEntry1024(row, err)
    case 1536:
        row, err := q.SearchCache1536(ctx, db.SearchCache1536Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, ConfigVersionHash: a.ConfigVersionHash,
            Column5: CacheHitThreshold,
        })
        return rowToEntry1536(row, err)
    case 3072:
        row, err := q.SearchCache3072(ctx, db.SearchCache3072Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, ConfigVersionHash: a.ConfigVersionHash,
            Column5: CacheHitThreshold,
        })
        return rowToEntry3072(row, err)
    }
    return nil, fmt.Errorf("unsupported dim: %d", dims)
}

// Insert embeds the query + persists the response to L2 pgvector, then
// writes the L1 Redis exact-match entry AFTER the L2 row is known.
// Ordering matters: L1-before-L2 would poison the cache if Embed fails
// (L1 would hold a CacheEntry with zero UUID, making future Lookup
// return a bogus ID that emitCacheHitCostEvent writes to cost_event).
func (c *ResponseCache) Insert(ctx context.Context, a InsertCacheArgs) (uuid.UUID, error) {
    vecs, dims, err := c.embedder.Embed(ctx, []string{a.QueryText})
    if err != nil { return uuid.Nil, fmt.Errorf("embed: %w", err) }
    vec := pgvector.NewVector(vecs[0])
    var exp pgtype.Timestamptz
    if a.TTL > 0 { exp = pgtype.Timestamptz{Time: time.Now().Add(a.TTL), Valid: true} }

    q := db.New(c.pool)
    switch dims {
    case 1024:
        r, err := q.InsertCache1024(ctx, db.InsertCache1024Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, QueryText: sqlNullString(a.QueryText),
            Response: a.Response, Model: a.Model,
            ConfigVersionHash: a.ConfigVersionHash,
            KnowledgeVersion: sqlNullString(a.KnowledgeVersion),
            ExpiresAt: exp,
        })
        if err != nil { return uuid.Nil, err }
        id := uuidFromPg(r.ID)
        c.populateL1(ctx, a, id) // write L1 only AFTER L2 succeeds (B1)
        return id, nil
    case 1536:
        r, err := q.InsertCache1536(ctx, db.InsertCache1536Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, QueryText: sqlNullString(a.QueryText),
            Response: a.Response, Model: a.Model,
            ConfigVersionHash: a.ConfigVersionHash,
            KnowledgeVersion: sqlNullString(a.KnowledgeVersion),
            ExpiresAt: exp,
        })
        if err != nil { return uuid.Nil, err }
        id := uuidFromPg(r.ID)
        c.populateL1(ctx, a, id)
        return id, nil
    case 3072:
        r, err := q.InsertCache3072(ctx, db.InsertCache3072Params{
            WorkspaceID: uuidPg(a.WorkspaceID), AgentID: uuidPg(a.AgentID),
            QueryEmbedding: vec, QueryText: sqlNullString(a.QueryText),
            Response: a.Response, Model: a.Model,
            ConfigVersionHash: a.ConfigVersionHash,
            KnowledgeVersion: sqlNullString(a.KnowledgeVersion),
            ExpiresAt: exp,
        })
        if err != nil { return uuid.Nil, err }
        id := uuidFromPg(r.ID)
        c.populateL1(ctx, a, id)
        return id, nil
    }
    return uuid.Nil, fmt.Errorf("unsupported dim: %d", dims)
}

// populateL1 writes the L1 Redis entry with the authoritative L2 row ID
// embedded. Called ONLY after the L2 insert succeeds (B1 fix). ID MUST
// be non-nil; callers never pass uuid.Nil here (if they did, the next
// Lookup hit would write a bogus response_cache_id into cost_event).
//
// Cost-weighted TTL: L1 TTL is deliberately SHORTER than L2 (1 hour vs
// 7 days) so a rolled-back agent config doesn't reactivate stale L1
// entries beyond a one-hour window. See R2-S2 rationale.
func (c *ResponseCache) populateL1(ctx context.Context, a InsertCacheArgs, id uuid.UUID) {
    if c.redis == nil || id == uuid.Nil { return }
    entry := CacheEntry{
        ID: id, Response: a.Response, Model: a.Model,
        ConfigVersionHash: a.ConfigVersionHash,
    }
    raw, _ := json.Marshal(entry)
    ttl := 3600 // 1 hour — see B1/S2 rollback-reactivation comment
    _ = c.redis.SetEx(ctx,
        l1Key(a.WorkspaceID, a.AgentID, a.ConfigVersionHash, a.QueryText),
        string(raw), ttl)
}

// rowToEntry{dim} wrappers convert pgx.ErrNoRows to (nil, nil) — caller
// treats as cache miss — while any other error surfaces.
func rowToEntry1024(row db.SearchCache1024Row, err error) (*CacheEntry, error) {
    if errors.Is(err, pgx.ErrNoRows) { return nil, nil }
    if err != nil { return nil, err }
    // sqlc generates Similarity as interface{} when the SELECT column
    // derives from an expression (`1 - (embedding <=> $2)`). The
    // underlying type is float64 in modern pgx; the older pgx versions
    // wrap it in pgtype.Float8. Handle both without panicking.
    var sim float64
    switch v := row.Similarity.(type) {
    case float64:
        sim = v
    case pgtype.Float8:
        if v.Valid { sim = v.Float64 }
    default:
        return nil, fmt.Errorf("unexpected Similarity type %T", v)
    }
    return &CacheEntry{
        ID: uuidFromPg(row.ID), Response: row.Response,
        Model: row.Model, Similarity: sim,
        ConfigVersionHash: row.ConfigVersionHash,
    }, nil
}
// ...same for rowToEntry1536 + rowToEntry3072 — identical shape, different row types.
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/ -run TestCache -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/cache.go server/pkg/agent/cache_test.go
git commit -m "feat(cache): ResponseCache with dim-partition routing

Lookup embeds query + searches partition matching workspace's embedding
config dimension. Cosine threshold 0.95 from defaults.go. Insert stores
post-response entries with config_version_hash + optional TTL."
```

---

### Task 5: LLM API Backend Integration — Check Cache, Emit cost_event

**Files:**
- Modify: `server/pkg/agent/llm_api.go` (Phase 2)
- Modify: `server/pkg/agent/llm_api_test.go`

**Goal:** wrap `LLMAPIBackend.Execute` — check cache pre-call, emit `cost_status='included'` cost_event on hit, otherwise call upstream + insert cache entry + emit normal cost_event.

- [ ] **Step 1: Failing test**

```go
func TestLLMAPI_CacheHitEmitsIncludedCostEvent(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "hit")
    agentID := testutil.SeedAgent(db, wsID, "bot", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedEmbeddingConfig(db, wsID, 1536)

    cache := NewResponseCache(db, testutil.StubEmbedder{Dim: 1536, Fixed: ones(1536)}, nil /* no L1 in unit tests */)
    _, _ = cache.Insert(context.Background(), InsertCacheArgs{
        WorkspaceID: wsID, AgentID: agentID,
        QueryText: "hello", Response: "hi there",
        Model: "claude-haiku-4-5-20251001",
        ConfigVersionHash: "v1",
    })

    backend := NewLLMAPIBackend(LLMAPIDeps{
        DB: db, Cache: cache,
        Model: testutil.StubModelReplying("SHOULD NOT BE CALLED"),
    })
    out, err := backend.Execute(context.Background(), "hello",
        ExecOptions{WorkspaceID: wsID, AgentID: agentID, ConfigVersionHash: "v1"})
    if err != nil { t.Fatal(err) }
    if out.Text != "hi there" { t.Errorf("text = %q; expected cached response", out.Text) }

    events := testutil.ListCostEvents(db, wsID, agentID)
    if len(events) != 1 { t.Fatalf("events = %d, want 1", len(events)) }
    if events[0].CostStatus != "included" { t.Errorf("status = %q", events[0].CostStatus) }
    if events[0].CostCents != 0 { t.Errorf("cost = %d, want 0", events[0].CostCents) }
    if !events[0].ResponseCacheID.Valid { t.Error("response_cache_id not set on hit") }
}
```

- [ ] **Step 2: Implement — wrap Execute**

```go
// Inside LLMAPIBackend.Execute — new pre-call branch:

// Semantic cache lookup (Phase 9 §9.2). Skip if disabled via ExecOptions.
if b.cache != nil && opts.AllowCache {
    hit, err := b.cache.Lookup(ctx, LookupArgs{
        WorkspaceID: opts.WorkspaceID, AgentID: opts.AgentID,
        QueryText: prompt, ConfigVersionHash: opts.ConfigVersionHash,
    })
    if err == nil && hit != nil {
        // Bump hit_count + emit zero-cost event tagged with the cache id
        // so dashboards can split cached vs uncached traffic.
        _ = b.cache.BumpHit(ctx, opts.WorkspaceID, hit.ID, b.workspaceDims(ctx, opts.WorkspaceID))
        _ = b.emitCacheHitCostEvent(ctx, opts, hit)
        return &Session{Text: hit.Response, Model: hit.Model, CacheHit: true}, nil
    }
}

// Cache miss — call upstream as before, then insert into cache.
session, err := b.executeUpstream(ctx, prompt, opts)
if err != nil { return nil, err }

if b.cache != nil && opts.AllowCache {
    _, _ = b.cache.Insert(ctx, InsertCacheArgs{
        WorkspaceID: opts.WorkspaceID, AgentID: opts.AgentID,
        QueryText: prompt, Response: session.Text, Model: session.Model,
        ConfigVersionHash: opts.ConfigVersionHash,
        KnowledgeVersion: opts.KnowledgeVersion,
        TTL: 7 * 24 * time.Hour, // 1-week default TTL
    })
}
return session, nil
```

And `emitCacheHitCostEvent`:

```go
func (b *LLMAPIBackend) emitCacheHitCostEvent(ctx context.Context, opts ExecOptions, hit *CacheEntry) error {
    q := db.New(b.db)
    _, err := q.InsertCostEvent(ctx, db.InsertCostEventParams{
        WorkspaceID: uuidPg(opts.WorkspaceID), AgentID: uuidPg(opts.AgentID),
        TaskID: uuidPgNullable(opts.TaskID),
        Model: hit.Model,
        InputTokens: sqlNullInt(0), OutputTokens: sqlNullInt(0),
        CacheReadTokens: sqlNullInt(0), CacheWriteTokens: sqlNullInt(0),
        CostCents: 0,
        CostStatus: "included",
        TraceID: uuidPg(traceIDFromContext(ctx)),
        ResponseCacheID: pgtype.UUID{Bytes: hit.ID, Valid: true},
    })
    return err
}
```

- [ ] **Step 3: Run + commit**

```bash
cd server && go test ./pkg/agent/ -run TestLLMAPI_CacheHit -v
git add server/pkg/agent/llm_api.go server/pkg/agent/llm_api_test.go
git commit -m "feat(cache): LLMAPIBackend checks cache + emits included cost_event

On hit: zero-cost cost_event linked to response_cache.id + CacheHit flag
on Session. On miss: upstream call + cache insert with 7-day TTL +
ConfigVersionHash from caller. opts.AllowCache gates per-call (chat
messages may disable for determinism)."
```

---

### Task 6: Cache Purge Worker

**Files:**
- Create: `server/internal/worker/cache_purge.go`
- Create: `server/internal/worker/cache_purge_test.go`

**Goal:** daily job sweeping stale cache entries — either expired OR config_version_hash mismatch against the agent's current config.

- [ ] **Step 1: Failing test**

```go
func TestCachePurge_RemovesStaleEntries(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "purge")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedEmbeddingConfig(db, wsID, 1536)

    // Seed two cache rows: one at current hash, one at old hash.
    testutil.SeedCacheRow(db, wsID, agentID, "current-hash", time.Now().Add(time.Hour), "fresh")
    testutil.SeedCacheRow(db, wsID, agentID, "old-hash",     time.Now().Add(time.Hour), "stale")

    w := NewCachePurgeWorker(CachePurgeDeps{DB: db, HashOf: func(agentID uuid.UUID) string { return "current-hash" }})
    n := w.RunOnce(context.Background())
    if n != 1 { t.Errorf("deleted = %d, want 1", n) }

    remaining := testutil.CountCacheRows(db, wsID, agentID)
    if remaining != 1 { t.Errorf("remaining = %d, want 1 (fresh only)", remaining) }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/worker/cache_purge.go
package worker

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type CachePurgeDeps struct {
    DB     *pgxpool.Pool
    // HashOf computes the current config_version_hash for the given agent
    // — typically sha256(instructions || tools_json). Injected so tests
    // can stub a deterministic value.
    HashOf func(agentID uuid.UUID) string
    // KnowledgeVersionOf returns the current knowledge_version identifier
    // for the given agent, or "" if the agent has no knowledge sources.
    // Empty string disables the knowledge-version check — the DB query
    // treats NULL $4 as "don't filter on knowledge_version".
    KnowledgeVersionOf func(agentID uuid.UUID) string
}

type CachePurgeWorker struct { deps CachePurgeDeps }

func NewCachePurgeWorker(deps CachePurgeDeps) *CachePurgeWorker { return &CachePurgeWorker{deps: deps} }

func (w *CachePurgeWorker) Run(ctx context.Context) {
    _ = w.RunOnce(ctx) // immediate on boot
    t := time.NewTicker(24 * time.Hour)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C: _ = w.RunOnce(ctx)
        }
    }
}

// RunOnce sweeps every agent's cache partition. N+1 queries per agent is
// fine at expected scale (10s–100s of agents per workspace); if an agent
// has no cache rows the DELETE is a no-op.
func (w *CachePurgeWorker) RunOnce(ctx context.Context) int {
    q := db.New(w.deps.DB)
    agents, err := q.ListAllAgents(ctx) // workspace-agnostic — purge is global
    if err != nil { return 0 }
    total := 0
    for _, a := range agents {
        wsID := uuidFromPg(a.WorkspaceID)
        agID := uuidFromPg(a.ID)
        hash := w.deps.HashOf(agID)
        knowV := sqlNullString(w.deps.KnowledgeVersionOf(agID)) // "" → NULL → skip filter
        dims := workspaceDimsFor(ctx, q, wsID)
        switch dims {
        case 1024:
            n, _ := q.DeleteStaleCache1024(ctx, db.DeleteStaleCache1024Params{
                WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agID),
                ConfigVersionHash: hash, Column4: knowV,
            })
            total += int(n)
        case 1536:
            n, _ := q.DeleteStaleCache1536(ctx, db.DeleteStaleCache1536Params{
                WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agID),
                ConfigVersionHash: hash, Column4: knowV,
            })
            total += int(n)
        case 3072:
            n, _ := q.DeleteStaleCache3072(ctx, db.DeleteStaleCache3072Params{
                WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agID),
                ConfigVersionHash: hash, Column4: knowV,
            })
            total += int(n)
        }
    }
    return total
}
```

- [ ] **Step 3: Commit**

```bash
cd server && go test ./internal/worker/ -run TestCachePurge -v
git add server/internal/worker/cache_purge.go server/internal/worker/cache_purge_test.go
git commit -m "feat(cache): daily stale-entry purge worker

Stale = config_version_hash mismatch OR expires_at elapsed. HashOf
injected so agent rollback (Phase 8B) automatically invalidates old
cached responses next sweep — no manual invalidation needed."
```

---

### Task 7: Batch Executor — Anthropic + OpenAI Batch API

**Files:**
- Create: `server/pkg/agent/batch.go`
- Create: `server/pkg/agent/batch_test.go`

**Goal:** collect `priority='batch'` tasks, submit in chunks to Batch APIs (50% discount), poll for completion, feed results back through task-completion path.

- [ ] **Step 1: Failing test (shape)**

```go
func TestBatchExecutor_SubmitsAndPolls(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "batch")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")
    // Seed 3 pending batch tasks.
    ids := []uuid.UUID{}
    for i := 0; i < 3; i++ {
        ids = append(ids, testutil.EnqueueTaskWithPriority(db, wsID, agentID, "q", "batch"))
    }

    fakeProv := testutil.FakeBatchProvider{ /* returns success for all 3 */ }
    ex := NewBatchExecutor(BatchDeps{DB: db, Provider: fakeProv})
    ex.RunOnce(context.Background())

    // All 3 tasks now completed with batch-provider outputs.
    for _, id := range ids {
        task := testutil.GetTask(db, id)
        if task.Status != "completed" { t.Errorf("task %s status = %q", id, task.Status) }
    }

    // Cost events record the 50% discount tier.
    events := testutil.ListCostEvents(db, wsID, agentID)
    for _, e := range events {
        if e.CostStatus != "actual" { t.Errorf("status = %q", e.CostStatus) }
        if e.Model == "" { t.Error("model not recorded") }
    }
}
```

- [ ] **Step 2: Implement (shape)**

```go
// server/pkg/agent/batch.go
package agent

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type BatchProvider interface {
    Submit(ctx context.Context, items []BatchItem) (batchID string, err error)
    Poll(ctx context.Context, batchID string) (status string, results map[string]BatchResult, err error)
    DiscountMultiplier() float64 // 0.5 for 50% Anthropic/OpenAI discount
}

type BatchItem struct {
    CustomID string // maps to task_id
    Prompt   string
    Model    string
}

type BatchResult struct {
    Text       string
    Usage      Usage   // inputs/outputs/cache
    ModelUsed  string
    StatusCode int
}

type BatchDeps struct {
    DB        *pgxpool.Pool
    Providers map[string]BatchProvider // provider name → adapter
    // PendingWindow is the age beyond which a batch-priority task is
    // considered ready to submit even if under BatchMaxSize. Default
    // BatchSubmitInterval (5m) from defaults.go.
    PendingWindow time.Duration
    // CostCalc is the Phase 2 CostCalculator — used by computeCostCents
    // to apply cache-token multipliers before the batch discount
    // multiplier. Required.
    CostCalc service.CostCalculator
}

// computeCostCents runs the Phase 2 CostCalculator against the batch
// response's usage and model. Cache-token pricing is applied INSIDE
// ComputeCents; the batch DiscountMultiplier is applied by the caller
// on top (see completeBatch).
func (e *BatchExecutor) computeCostCents(res BatchResult) int {
    return e.deps.CostCalc.ComputeCents(res.Usage, res.ModelUsed)
}

type BatchExecutor struct{ deps BatchDeps }

func NewBatchExecutor(deps BatchDeps) *BatchExecutor {
    if deps.PendingWindow == 0 { deps.PendingWindow = BatchSubmitInterval }
    return &BatchExecutor{deps: deps}
}

func (e *BatchExecutor) Run(ctx context.Context) {
    // On boot, resume any batch_job rows in status='submitted' whose
    // goroutine died with the previous server process. This closes the
    // R2-B3 restart-loss gap — without it, tasks grouped into a pending
    // batch are stuck forever.
    e.resumeInFlight(ctx)

    t := time.NewTicker(BatchSubmitInterval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C: e.RunOnce(ctx)
        }
    }
}

// resumeInFlight scans batch_job for status='submitted' rows and
// spawns pollAndComplete for each. Idempotent: if the upstream provider
// has already completed the batch, the next poll immediately finds it
// and calls completeBatch. The UNIQUE(provider, external_batch_id)
// constraint prevents double-resumption if two server replicas boot
// simultaneously — only one wins the goroutine spawn via the per-row
// SKIP LOCKED claim below.
func (e *BatchExecutor) resumeInFlight(ctx context.Context) {
    q := db.New(e.deps.DB)
    rows, err := q.ListSubmittedBatchJobs(ctx)
    if err != nil { return }
    for _, job := range rows {
        prov, ok := e.deps.Providers[job.Provider]
        if !ok { continue }
        // sqlc generates TaskIds as []pgtype.UUID for uuid[] columns.
        // Convert once here so loadTasksByID / downstream consumers can
        // work with plain uuid.UUID.
        ids := pgUUIDsToUUIDs(job.TaskIds)
        byID := e.loadTasksByID(ctx, ids)
        go e.pollAndComplete(ctx, prov, job.ExternalBatchID, byID, uuidFromPg(job.ID))
    }
}

// pgUUIDsToUUIDs converts pgx's array wrapper to the plain google/uuid
// slice the rest of the codebase uses. Skips invalid (zero) entries.
func pgUUIDsToUUIDs(in []pgtype.UUID) []uuid.UUID {
    out := make([]uuid.UUID, 0, len(in))
    for _, p := range in {
        if p.Valid { out = append(out, uuid.UUID(p.Bytes)) }
    }
    return out
}

// RunOnce groups pending batch tasks by provider, CLAIMS them via
// ClaimBatchTasks (atomic UPDATE from pending→batch_submitted inside
// FOR UPDATE SKIP LOCKED — R2-S5), submits each group, persists the
// batch_job row, then spawns pollAndComplete.
func (e *BatchExecutor) RunOnce(ctx context.Context) {
    q := db.New(e.deps.DB)

    // Claim up to BatchMaxSize pending-batch tasks. SKIP LOCKED lets
    // multiple replicas coexist — each grabs a disjoint subset.
    rows, err := q.ClaimBatchTasks(ctx, int32(BatchMaxSize))
    if err != nil || len(rows) == 0 { return }

    byProvider := map[string][]BatchItem{}
    byID := map[string]db.AgentTaskQueue{}
    ids := []uuid.UUID{}
    for _, r := range rows {
        item := BatchItem{CustomID: uuidFromPg(r.ID).String(), Prompt: r.Prompt, Model: r.Model.String}
        byProvider[r.Provider.String] = append(byProvider[r.Provider.String], item)
        byID[item.CustomID] = r
        ids = append(ids, uuidFromPg(r.ID))
    }

    for provider, items := range byProvider {
        prov, ok := e.deps.Providers[provider]
        if !ok {
            // Un-claim: restore priority=batch, status=pending so the next
            // tick can retry when the provider adapter is available.
            _ = q.UnclaimBatchTasks(ctx, idsFor(byProvider[provider], byID))
            continue
        }
        batchID, err := prov.Submit(ctx, items)
        if err != nil {
            _ = q.UnclaimBatchTasks(ctx, idsFor(byProvider[provider], byID))
            continue
        }
        // Persist batch_job before the goroutine spawns so a crash
        // between Submit and pollAndComplete is recoverable.
        job, err := q.InsertBatchJob(ctx, db.InsertBatchJobParams{
            Provider: provider, ExternalBatchID: batchID,
            TaskIds: idsFor(byProvider[provider], byID), Status: "submitted",
        })
        if err != nil {
            // Paranoid: the provider already accepted the batch but we
            // failed to persist. Log + leak the batch to manual recovery;
            // tasks remain in batch_submitted so no dup occurs.
            continue
        }
        go e.pollAndComplete(ctx, prov, batchID, byID, uuidFromPg(job.ID))
    }
}

func idsFor(items []BatchItem, byID map[string]db.AgentTaskQueue) []uuid.UUID {
    out := make([]uuid.UUID, 0, len(items))
    for _, it := range items {
        id, _ := uuid.Parse(it.CustomID)
        out = append(out, id)
    }
    return out
}

func (e *BatchExecutor) loadTasksByID(ctx context.Context, ids []uuid.UUID) map[string]db.AgentTaskQueue {
    q := db.New(e.deps.DB)
    out := map[string]db.AgentTaskQueue{}
    for _, id := range ids {
        row, err := q.GetTask(ctx, uuidPg(id))
        if err == nil { out[id.String()] = row }
    }
    return out
}

// pollAndComplete loops until the batch reaches completed/failed, then
// writes task completion + cost_events, then flips batch_job.status to
// the terminal state. The batchJobID parameter (new in R2-B3) lets the
// function update the persisted row — essential for resumeInFlight to
// skip already-completed batches on subsequent reboots.
func (e *BatchExecutor) pollAndComplete(ctx context.Context, prov BatchProvider, batchID string, byID map[string]db.AgentTaskQueue, batchJobID uuid.UUID) {
    t := time.NewTicker(30 * time.Second)
    defer t.Stop()
    deadline := time.Now().Add(24 * time.Hour) // Anthropic batch max 24h
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:
            if time.Now().After(deadline) {
                // Mark failed so resumeInFlight doesn't keep retrying;
                // the claimed tasks stay in batch_submitted — ops needs
                // to manually re-priority them to 'normal' for retry.
                _ = e.markBatchJob(ctx, batchJobID, "failed")
                return
            }
            status, results, err := prov.Poll(ctx, batchID)
            if err != nil { continue }
            if status != "completed" && status != "failed" { continue }
            e.completeBatch(ctx, prov, results, byID)
            _ = e.markBatchJob(ctx, batchJobID, status)
            return
        }
    }
}

func (e *BatchExecutor) markBatchJob(ctx context.Context, id uuid.UUID, status string) error {
    q := db.New(e.deps.DB)
    return q.MarkBatchJobComplete(ctx, db.MarkBatchJobCompleteParams{
        ID: uuidPg(id), Status: status,
    })
}

func (e *BatchExecutor) completeBatch(ctx context.Context, prov BatchProvider, results map[string]BatchResult, byID map[string]db.AgentTaskQueue) {
    for customID, res := range results {
        taskUUID, _ := uuid.Parse(customID)
        origTask := byID[customID]
        wsID := uuidFromPg(origTask.WorkspaceID)
        agentID := uuidFromPg(origTask.AgentID)

        // Transaction: CompleteTask + InsertCostEvent MUST both commit or
        // neither commits. Without this, InsertCostEvent failing after
        // CompleteTask succeeds leaves a completed task with no cost
        // record — the dashboard under-reports spend, and cost_events
        // can't be reconstructed because the upstream response is gone.
        // Rolling back keeps the task at 'pending' so the next poll
        // retries.
        tx, err := e.deps.DB.Begin(ctx)
        if err != nil {
            // Transient — next poll will retry the whole completeBatch.
            continue
        }
        txQ := db.New(tx)

        if err := txQ.CompleteTask(ctx, db.CompleteTaskParams{
            ID: uuidPg(taskUUID), WorkspaceID: uuidPg(wsID),
            Status: "completed", Output: []byte(res.Text),
        }); err != nil {
            tx.Rollback(ctx)
            continue
        }
        baseCents := e.computeCostCents(res)
        discountedCents := int(float64(baseCents) * prov.DiscountMultiplier())
        if _, err := txQ.InsertCostEvent(ctx, db.InsertCostEventParams{
            WorkspaceID: uuidPg(wsID), AgentID: uuidPg(agentID),
            TaskID: uuidPgNullable(taskUUID),
            Model: res.ModelUsed,
            InputTokens:     sqlNullInt(res.Usage.InputTokens),
            OutputTokens:    sqlNullInt(res.Usage.OutputTokens),
            CacheReadTokens: sqlNullInt(res.Usage.CacheReadTokens),  // preserves batch+prompt-cache stack (B2 R1 fix)
            CacheWriteTokens: sqlNullInt(res.Usage.CacheWriteTokens),
            CostCents:       discountedCents,
            CostStatus:      "actual",
        }); err != nil {
            tx.Rollback(ctx)
            continue
        }
        _ = tx.Commit(ctx)
    }
}
```

Provider adapters (`server/pkg/agent/batch_anthropic.go` + `batch_openai.go`) use the vendor SDKs. Keep each adapter under 100 lines — thin SDK wrapper.

**Stacking batch discount with prompt caching for 95% savings.** PLAN.md §9.1's headline 95% number requires the batch 50% discount AND Phase 2's prompt-cache reads (0.1× input-token cost on hit + 1.25× on cache-write) to compose. Mechanically:

1. When submitting a batch item, include `cache_control` blocks on stable prefixes (system prompt, tool schemas, knowledge chunks) EXACTLY as the non-batch Phase 2 path does. The Anthropic Batch API honors `cache_control` headers; prompt-cache reads are charged at the cached-read rate BEFORE the batch discount is applied.
2. `BatchResult.Usage` must carry `CacheReadTokens` and `CacheWriteTokens` separately from `InputTokens` so `CostCalculator.ComputeCents` can apply the per-token multipliers before the per-provider `DiscountMultiplier`:
   ```go
   // completeBatch:
   baseCents := cc.ComputeCents(res.Usage, res.ModelUsed)          // applies cache multipliers
   discountedCents := int(float64(baseCents) * prov.DiscountMultiplier())  // applies batch discount
   ```
3. `cost_event` records both effects — the token-count columns show the raw usage, `cost_status='actual'`, and `cost_cents` is the post-discount figure.

**Verification item for Task 11:** add a test that submits a batch item whose system prompt matches a prior cache-written prefix, polls to completion, and asserts the resulting `cost_event` has `cache_read_tokens > 0` AND `cost_cents ≈ base × 0.1 × 0.5` (within rounding). This closes the 95%-savings claim.

- [ ] **Step 3: Commit**

```bash
cd server && go test ./pkg/agent/ -run TestBatchExecutor -v
git add server/pkg/agent/batch.go server/pkg/agent/batch_test.go server/pkg/agent/batch_anthropic.go server/pkg/agent/batch_openai.go
git commit -m "feat(batch): Anthropic + OpenAI Batch API integration

Tasks with priority='batch' queued for up-to-5min aggregation, submitted
to provider Batch APIs (50% discount). pollAndComplete writes task
completion + cost_event with discount multiplier applied."
```

---

### Task 8: Agent Metrics Aggregation Worker

**Files:**
- Create: `server/internal/worker/metrics.go`
- Create: `server/internal/worker/metrics_test.go`

**Goal:** hourly + daily aggregation job. SLA breach fires `sla.breached` event (Phase 8A.3 outbound webhook picks it up).

- [ ] **Step 1: Failing test**

```go
func TestMetrics_HourlyAggregation(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "m")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")

    hour := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    testutil.SeedCompletedTask(db, wsID, agentID, hour.Add(5*time.Minute), 20*time.Second)
    testutil.SeedCompletedTask(db, wsID, agentID, hour.Add(25*time.Minute), 30*time.Second)
    testutil.SeedFailedTask(db, wsID, agentID, hour.Add(45*time.Minute))

    w := NewMetricsWorker(MetricsDeps{DB: db, Bus: testutil.NoopBus})
    w.RunHourly(context.Background(), hour)

    m := testutil.GetAgentMetric(db, agentID, hour, "hourly")
    if m.TasksCompleted != 2 { t.Errorf("completed = %d, want 2", m.TasksCompleted) }
    if m.TasksFailed    != 1 { t.Errorf("failed = %d, want 1", m.TasksFailed) }
    if m.AvgResolutionSeconds == 0 { t.Error("avg resolution zero") }
}

func TestMetrics_SLABreachFires(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sla")
    agentID := testutil.SeedAgentWithSLA(db, wsID, "slow-bot", "anthropic", "claude-haiku-4-5-20251001", 10) // 10s SLA

    hour := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    for i := 0; i < 20; i++ {
        // p95 of 20 tasks at 60s is 60s — breaches 10s SLA.
        testutil.SeedCompletedTask(db, wsID, agentID, hour.Add(time.Duration(i)*time.Minute), 60*time.Second)
    }

    bus := testutil.NewCapturingBus()
    w := NewMetricsWorker(MetricsDeps{DB: db, Bus: bus})
    w.RunHourly(context.Background(), hour)

    events := bus.Events()
    if len(events) == 0 { t.Fatal("no sla.breached event emitted") }
    if events[0].Type != "sla.breached" { t.Errorf("event = %q", events[0].Type) }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/worker/metrics.go
package worker

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
    "aicolab/server/pkg/events"
)

type MetricsDeps struct {
    DB  *pgxpool.Pool
    Bus *events.Bus
}

type MetricsWorker struct{ deps MetricsDeps }

func NewMetricsWorker(deps MetricsDeps) *MetricsWorker { return &MetricsWorker{deps: deps} }

func (w *MetricsWorker) Run(ctx context.Context) {
    // Startup catch-up: if the server restarted past 00:00 UTC, the
    // previous Run loop may have missed yesterday's daily rollup. Check
    // whether a daily metric exists for YESTERDAY's date; if not, run
    // RunDaily once before entering the normal tick loop. Avoids a
    // one-day hole in the dashboard whenever the server reboots after
    // midnight.
    yesterdayStart := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
    if !w.dailyRolledUp(ctx, yesterdayStart) {
        w.RunDaily(ctx, yesterdayStart)
    }

    // Run at top of every hour; compute the JUST-ENDED hour bucket.
    for {
        next := time.Now().Truncate(time.Hour).Add(time.Hour)
        select {
        case <-ctx.Done(): return
        case <-time.After(time.Until(next)):
            hourEnding := next.Add(-time.Hour)
            w.RunHourly(ctx, hourEnding)
            // Once per day (at 00:00), roll hourly → daily.
            if hourEnding.Hour() == 0 {
                w.RunDaily(ctx, hourEnding.Add(-24*time.Hour))
            }
        }
    }
}

// dailyRolledUp returns true iff at least one agent_metric row with
// period_type='daily' exists for the given dayStart. Used by Run to
// decide whether to trigger startup catch-up. A single SELECT EXISTS
// is cheap enough to run on every boot.
func (w *MetricsWorker) dailyRolledUp(ctx context.Context, dayStart time.Time) bool {
    q := db.New(w.deps.DB)
    n, err := q.CountDailyMetricsForDay(ctx, dayStart)
    return err == nil && n > 0
}

// RunHourly aggregates the [hourStart, hourStart+1h) window for every
// agent in every workspace.
func (w *MetricsWorker) RunHourly(ctx context.Context, hourStart time.Time) {
    q := db.New(w.deps.DB)
    agents, _ := q.ListAllAgents(ctx)
    hourEnd := hourStart.Add(time.Hour)
    for _, a := range agents {
        // Per-agent processing isolated so a mid-run agent deletion
        // (FK race: Agent deleted between ListAllAgents and UpsertAgentMetric
        // triggers 23503 FK violation against agent_metric's composite FK)
        // fails only this agent's bucket without aborting the whole tick.
        // We intentionally DON'T require agents table to still exist — metric
        // rows cascade-delete with the agent anyway.
        wsID := uuidFromPg(a.WorkspaceID)
        agentID := uuidFromPg(a.ID)
        func() {
            defer func() {
                if r := recover(); r != nil {
                    // Log but don't re-panic; continue to next agent.
                }
            }()
            tasks, err := q.AggregateCompletedTasks(ctx, db.AggregateCompletedTasksParams{
                WorkspaceID: a.WorkspaceID, AgentID: a.ID,
                Column3: hourStart, Column4: hourEnd,
            })
            if err != nil { return } // agent likely deleted — skip
            costs, err := q.AggregateCosts(ctx, db.AggregateCostsParams{
                WorkspaceID: a.WorkspaceID, AgentID: a.ID,
                Column3: hourStart, Column4: hourEnd,
            })
            if err != nil { return }

            total := int(tasks.Completed + tasks.Failed + tasks.TimedOut)
            avgCost := int64(0)
            if total > 0 { avgCost = costs.TotalCents / int64(total) }
            // NOTE: hitRate here is cache_hits / tasks_total, not
            // cache_hits / llm_calls_total. Multi-turn agents produce >1
            // LLM call per task so this is an approximation; the
            // /cost/cache-hits endpoint uses the correct per-event ratio
            // for the dashboard. See R2 N2.
            hitRate := float64(0)
            if total > 0 { hitRate = float64(costs.CacheHits) / float64(total) }

            _, err = q.UpsertAgentMetric(ctx, db.UpsertAgentMetricParams{
            WorkspaceID:     a.WorkspaceID,
            AgentID:         a.ID,
            PeriodStart:     hourStart,
            PeriodType:      "hourly",
            TasksCompleted:  int32(tasks.Completed),
            TasksFailed:     int32(tasks.Failed),
            TasksTimedOut:   int32(tasks.TimedOut),
            AvgResolutionSeconds: sqlNullInt(int(tasks.AvgSec)),
            P95ResolutionSeconds: sqlNullInt(int(tasks.P95Sec)),
            TotalCostCents:  int32(costs.TotalCents),
            AvgCostPerTaskCents: int32(avgCost),
            CacheHits:       int32(costs.CacheHits),
            CacheHitRate:    sqlNullDec(hitRate),
        })

            // SLA breach check (still inside per-agent closure so FK
            // race doesn't skip the alert path either).
            if a.SlaP95Seconds.Valid && int(tasks.P95Sec) > int(a.SlaP95Seconds.Int32) {
                w.deps.Bus.Publish(events.Event{
                    Type: "sla.breached",
                    WorkspaceID: wsID,
                    Payload: map[string]any{
                        "agent_id": agentID,
                        "p95_observed": tasks.P95Sec,
                        "p95_threshold": a.SlaP95Seconds.Int32,
                        "period_start": hourStart,
                    },
                })
            }
            _ = err // silence unused in the happy path; errors handled at call sites above
        }()
    }
}

// RunDaily rolls up 24 hourly buckets into a single daily bucket.
// Called at 00:00 UTC for the JUST-ENDED day.
func (w *MetricsWorker) RunDaily(ctx context.Context, dayStart time.Time) {
    q := db.New(w.deps.DB)
    agents, _ := q.ListAllAgents(ctx)
    dayEnd := dayStart.Add(24 * time.Hour)
    for _, a := range agents {
        row, err := q.RollupDailyMetrics(ctx, db.RollupDailyMetricsParams{
            WorkspaceID: a.WorkspaceID, AgentID: a.ID,
            Column3: dayStart, Column4: dayEnd,
        })
        if err != nil { continue }
        _, _ = q.UpsertAgentMetric(ctx, db.UpsertAgentMetricParams{
            WorkspaceID: a.WorkspaceID, AgentID: a.ID,
            PeriodStart: dayStart, PeriodType: "daily",
            TasksCompleted: row.TasksCompleted, TasksFailed: row.TasksFailed,
            TasksTimedOut: row.TasksTimedOut,
            AvgResolutionSeconds: row.AvgResolutionSeconds,
            P95ResolutionSeconds: row.P95ResolutionSeconds,
            DelegationsSent: row.DelegationsSent, DelegationsReceived: row.DelegationsReceived,
            ChatMessagesHandled: row.ChatMessagesHandled,
            AvgResponseSeconds: row.AvgResponseSeconds,
            TotalCostCents: row.TotalCostCents,
            AvgCostPerTaskCents: row.AvgCostPerTaskCents,
            CacheHits: row.CacheHits, CacheHitRate: row.CacheHitRate,
        })
    }
}
```

Add the rollup query to `metric.sql`:

```sql
-- name: RollupDailyMetrics :one
-- Aggregates 24 hourly rows in [dayStart, dayStart+24h) for one agent.
-- Average-of-averages is fine at V1; weighted average by task count is a
-- Phase 10 refinement.
SELECT
    SUM(tasks_completed)::int AS tasks_completed,
    SUM(tasks_failed)::int    AS tasks_failed,
    SUM(tasks_timed_out)::int AS tasks_timed_out,
    AVG(avg_resolution_seconds)::int AS avg_resolution_seconds,
    MAX(p95_resolution_seconds)::int AS p95_resolution_seconds,
    SUM(delegations_sent)::int      AS delegations_sent,
    SUM(delegations_received)::int  AS delegations_received,
    SUM(chat_messages_handled)::int AS chat_messages_handled,
    AVG(avg_response_seconds)       AS avg_response_seconds,
    SUM(total_cost_cents)::int      AS total_cost_cents,
    AVG(avg_cost_per_task_cents)::int AS avg_cost_per_task_cents,
    SUM(cache_hits)::int            AS cache_hits,
    AVG(cache_hit_rate)             AS cache_hit_rate
FROM agent_metric
WHERE workspace_id = $1 AND agent_id = $2
  AND period_type = 'hourly'
  AND period_start >= $3 AND period_start < $4;
```

```go
// (end of metrics.go above)
```

- [ ] **Step 3: Commit**

```bash
cd server && go test ./internal/worker/ -run TestMetrics -v
git add server/internal/worker/metrics.go server/internal/worker/metrics_test.go
git commit -m "feat(metrics): hourly + daily aggregation with SLA-breach webhook

Hourly worker runs at top of each hour for the JUST-ENDED bucket. Daily
rollup at 00:00 UTC. SLA breach (p95 > agent.sla_p95_seconds) publishes
sla.breached event — Phase 8A outbound webhooks deliver to subscribers."
```

---

### Task 9: Cost Dashboard Service + Handler

**Files:**
- Create: `server/internal/service/cost_dashboard.go`
- Create: `server/internal/handler/cost.go`
- Create: tests

**Goal:** REST surface for frontend. `GET /api/workspaces/{ws}/cost/summary?range=7d` returns aggregated spend. `GET /cost/breakdown` by agent+model+tier. `GET /cost/cache-hits` per-agent hit rate.

- [ ] **Step 1: Handler skeleton**

```go
// server/internal/handler/cost.go
func (h *CostHandler) Summary(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    rangeSpec := r.URL.Query().Get("range")
    start, end := parseRange(rangeSpec) // "7d" → (now-7d, now)
    s, err := h.svc.Summary(r.Context(), wsID, start, end)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, s)
}

// Breakdown groups cost_event by (model, agent_id, model_tier) within the
// time range. model_tier is derived app-side via ModelTierFor(model) which
// maps known model IDs to their Phase 2 tier (micro/standard/premium).
// Response includes pct_of_total for each bucket + a separate aggregate
// showing routing effectiveness (count per tier + % of total tasks).
func (h *CostHandler) Breakdown(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    start, end := parseRange(r.URL.Query().Get("range"))
    b, err := h.svc.Breakdown(r.Context(), wsID, start, end)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, b)
}

// CacheHits returns per-agent cache-hit rate in the window. Powers the
// "% cached traffic" dashboard card.
func (h *CostHandler) CacheHits(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    start, end := parseRange(r.URL.Query().Get("range"))
    out, err := h.svc.CacheHits(r.Context(), wsID, start, end)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, out)
}

// Chain groups cost_event by (trace_id, agent_id) for a specific trace.
// Powers the delegation-chain cost visualization (PLAN.md §9.4).
func (h *CostHandler) Chain(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    traceID, err := uuid.Parse(r.URL.Query().Get("trace_id"))
    if err != nil { http.Error(w, "invalid trace_id", 400); return }
    out, err := h.svc.ChainCost(r.Context(), wsID, traceID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, out)
}

// CredentialPools returns per-credential usage stats: usage_count,
// last_used_at, bound_agents count, hit/rotation telemetry. Consumes
// the Phase 8B credential metadata; no new table.
func (h *CostHandler) CredentialPools(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    out, err := h.svc.CredentialPools(r.Context(), wsID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, out)
}
```

Add the backing sqlc queries to `cost.sql`:

```sql
-- cost.sql additions

-- name: CostBreakdown :many
-- Multi-dimension grouping for the Breakdown endpoint. pct_of_total is
-- computed via a window function so one row can show its share without
-- a second query.
WITH scope AS (
    SELECT workspace_id, agent_id, model, cost_cents, cost_status
    FROM cost_event
    WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3
)
SELECT
    model, agent_id,
    SUM(cost_cents)::int AS total_cents,
    COUNT(*)::int        AS event_count,
    (SUM(cost_cents)::float /
        NULLIF(SUM(SUM(cost_cents)) OVER (), 0)) * 100 AS pct_of_total
FROM scope
GROUP BY model, agent_id
ORDER BY total_cents DESC;

-- name: CostPerTraceChain :many
-- Delegation chain visualization: every cost_event sharing a trace_id,
-- grouped by (agent_id, model) so the UI can render a per-agent bar.
SELECT
    trace_id, agent_id, model,
    SUM(cost_cents)::int AS total_cents,
    COUNT(*)::int        AS event_count,
    MIN(created_at)      AS started_at,
    MAX(created_at)      AS ended_at
FROM cost_event
WHERE workspace_id = $1 AND trace_id = $2
GROUP BY trace_id, agent_id, model
ORDER BY started_at ASC;
```

`CredentialPools` pulls directly from Phase 8B's `ListCredentialsWithBindings` / `ListAgentsUsingCredential` — no new sqlc query; the service composes existing queries into a `CredentialPoolStats` shape.

- [ ] **Step 2: Service + failing test**

```go
// server/internal/service/cost_dashboard.go
package service

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

type CostDeps struct {
    DB *pgxpool.Pool
}

type CostService struct {
    db *pgxpool.Pool
}

func NewCostService(deps CostDeps) *CostService {
    return &CostService{db: deps.DB}
}

// Breakdown + CacheHits + ChainCost + CredentialPools methods follow the
// same shape as Summary: call the matching sqlc query, map rows into
// the TS-mirrored DTO, return. Bodies implemented when each handler
// endpoint is wired — shape matches the type definitions in Task 10.
func (s *CostService) Breakdown(ctx context.Context, wsID uuid.UUID, start, end time.Time) (*CostBreakdown, error) {
    q := db.New(s.db)
    rows, err := q.CostBreakdown(ctx, db.CostBreakdownParams{
        WorkspaceID: uuidPg(wsID), Column2: start, Column3: end,
    })
    if err != nil { return nil, err }
    // Map + compute RoutingEffectiveness via ModelTierFor(row.Model).
    return &CostBreakdown{/* rows + routing — see types in Task 10 */}, nil
}

func (s *CostService) CacheHits(ctx context.Context, wsID uuid.UUID, start, end time.Time) ([]CacheHitRow, error) {
    // Event-weighted per-agent: hits / (hits+misses) in the range.
    return nil, nil /* see inline SQL comment below */
}

func (s *CostService) ChainCost(ctx context.Context, wsID, traceID uuid.UUID) ([]ChainCostRow, error) {
    q := db.New(s.db)
    rows, err := q.CostPerTraceChain(ctx, db.CostPerTraceChainParams{
        WorkspaceID: uuidPg(wsID), TraceID: uuidPg(traceID),
    })
    if err != nil { return nil, err }
    out := make([]ChainCostRow, len(rows))
    for i, r := range rows { out[i] = mapChainRow(r) }
    return out, nil
}

func (s *CostService) CredentialPools(ctx context.Context, wsID uuid.UUID) ([]CredentialPoolStats, error) {
    // Reuses Phase 8B ListCredentialsWithBindings; maps to stats shape.
    return nil, nil
}

func (s *CostService) Summary(ctx context.Context, wsID uuid.UUID, start, end time.Time) (*CostSummary, error) {
    q := db.New(s.db)
    row, err := q.SumCostInRange(ctx, db.SumCostInRangeParams{
        WorkspaceID: uuidPg(wsID), Column2: start, Column3: end,
    })
    if err != nil { return nil, err }
    // cost-weighted savings ratio — see CostSummary doc comment.
    savingsRate := float64(0)
    if row.Total > 0 { savingsRate = float64(row.CacheHitCents) / float64(row.Total) }
    return &CostSummary{
        TotalCents:                   int(row.Total),
        CacheHitCents:                int(row.CacheHitCents),  // cost_status='included'
        CacheMissCents:               int(row.CacheMissCents),
        CostWeightedCacheSavingsRate: savingsRate,
        PeriodStart:                  start,
        PeriodEnd:                    end,
    }, nil
}
```

```go
// server/internal/service/cost_dashboard_test.go
func TestCostSummary_PartitionsHitVsMiss(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "dash")
    agentID := testutil.SeedAgent(db, wsID, "a", "anthropic", "claude-haiku-4-5-20251001")

    // Seed 5 cache-hit cost_events (cost_status='included', cost_cents=0)
    // + 3 miss events (cost_status='actual', cost_cents=100 each).
    for i := 0; i < 5; i++ {
        testutil.SeedCostEvent(db, wsID, agentID, 0, "included")
    }
    for i := 0; i < 3; i++ {
        testutil.SeedCostEvent(db, wsID, agentID, 100, "actual")
    }

    svc := NewCostService(CostDeps{DB: db})
    sum, err := svc.Summary(context.Background(), wsID,
        time.Now().Add(-24*time.Hour), time.Now().Add(time.Hour))
    if err != nil { t.Fatal(err) }

    if sum.TotalCents != 300 {
        t.Errorf("total = %d, want 300 (3×100)", sum.TotalCents)
    }
    if sum.CacheHitCents != 0 {
        t.Errorf("hit cents = %d; included rows contribute 0 cents", sum.CacheHitCents)
    }
    if sum.CacheMissCents != 300 {
        t.Errorf("miss cents = %d, want 300", sum.CacheMissCents)
    }
    // CostSummary returns CostWeightedCacheSavingsRate — fraction of
    // ACTUAL cost that came from cached traffic. With 5 free hits + 3
    // paid misses: 0 / 300 = 0. Event-weighted 5/8 = 0.625 lives on
    // CacheHitRow served by /cost/cache-hits. Field name makes the
    // distinction explicit to avoid the R2-S3 confusion.
    if sum.CostWeightedCacheSavingsRate != 0 {
        t.Errorf("cost-weighted savings = %v, want 0 (hits are 0-cost)", sum.CostWeightedCacheSavingsRate)
    }
}
```

Note the subtle distinction: CostSummary's `CacheHitRate` is
*cost-weighted* (0 when hits are free). A separate `GET /cost/cache-hits`
endpoint returns the *event-weighted* hit rate per agent (5/8=0.625 in
the above seed), which matches what operators expect for "cache
effectiveness."

Add `SumCostInRange` sqlc query to `cost.sql` (Phase 1 has no equivalent query that partitions by cost_status — write it here):

```sql
-- cost.sql addition

-- name: SumCostInRange :one
-- Aggregates cost_event rows in [start, end) partitioning by cost_status.
-- CacheHitCents counts 'included' rows (always 0 cents but exposes how much
-- spend we deferred); CacheMissCents counts 'actual' rows.
SELECT
    COALESCE(SUM(cost_cents), 0)::int AS total,
    COALESCE(SUM(cost_cents) FILTER (WHERE cost_status = 'included'), 0)::int AS cache_hit_cents,
    COALESCE(SUM(cost_cents) FILTER (WHERE cost_status = 'actual'),   0)::int AS cache_miss_cents
FROM cost_event
WHERE workspace_id = $1 AND created_at >= $2 AND created_at < $3;

-- name: CacheHitsPerAgent :many
-- Event-weighted (count-based) cache hit rate per agent — the metric
-- CostSummary.CostWeightedCacheSavingsRate is NOT (see Task 10 type doc).
SELECT
    ce.agent_id,
    a.name AS agent_name,
    COUNT(*) FILTER (WHERE ce.response_cache_id IS NOT NULL)::int AS hit_count,
    COUNT(*) FILTER (WHERE ce.response_cache_id IS NULL)::int     AS miss_count,
    COALESCE(
        COUNT(*) FILTER (WHERE ce.response_cache_id IS NOT NULL)::float /
        NULLIF(COUNT(*), 0),
        0
    ) AS hit_rate
FROM cost_event ce
JOIN agent a ON a.id = ce.agent_id AND a.workspace_id = ce.workspace_id
WHERE ce.workspace_id = $1 AND ce.created_at >= $2 AND ce.created_at < $3
GROUP BY ce.agent_id, a.name
ORDER BY hit_count DESC;
```

- [ ] **Step 3: Mount + commit**

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run Cost -v
git add server/internal/service/cost_dashboard.go server/internal/handler/cost.go server/cmd/server/router.go
git commit -m "feat(cost): dashboard service + REST handlers

Three endpoints: /cost/summary, /cost/breakdown (by model+agent+tier),
/cost/cache-hits (per-agent hit rate from response_cache_id column).
Require 'admin' scope via Phase 8A middleware."
```

---

### Task 10: Frontend — Cost Dashboard + Performance Tab

**Files:**
- Create: `packages/core/types/cost.ts` + `metric.ts`
- Create: `packages/core/api/cost.ts` + `metric.ts`
- Create: `packages/core/cost/queries.ts` + `metric/queries.ts`
- Create: `packages/views/costs/` (summary + breakdown + cache-hits pages)
- Create: `packages/views/agents/components/tabs/performance-tab.tsx`

**Goal:** TanStack hooks + views + recharts line charts. Same pattern as Phase 6/7/8A.

- [ ] **Step 1: Types + client + hooks + views (sketch — mechanical)**

```ts
// packages/core/types/cost.ts
export type ModelTier = "micro" | "standard" | "premium";

export type CostSummary = {
  total_cents: number;
  cache_hit_cents: number;
  cache_miss_cents: number;
  // COST-WEIGHTED cache savings ratio: cache_hit_cents / (cache_hit_cents + cache_miss_cents).
  // When every cache hit costs 0 cents (cost_status='included'), this
  // equals 0 even if the cache served most traffic. Callers wanting
  // the intuitive "% of queries served from cache" should use
  // `CacheHitRow.hit_rate` on the /cost/cache-hits endpoint instead —
  // that's event-weighted (count_based).
  cost_weighted_cache_savings_rate: number;
  batch_savings_cents: number;  // estimated, = sum(non-batch baseline − actual) for priority='batch' rows
  period_start: string;
  period_end: string;
};

export type CostBreakdown = {
  rows: Array<{
    model: string;
    agent_id: string;
    tier: ModelTier;            // derived from model name
    total_cents: number;
    event_count: number;
    pct_of_total: number;       // 0..100
  }>;
  routing: RoutingEffectiveness;
};

// Powers the "Model routing effectiveness (% routed to each tier)" card
// from PLAN.md §9.4.
export type RoutingEffectiveness = {
  micro_pct: number;            // % of total events routed to micro tier
  standard_pct: number;
  premium_pct: number;
  total_events: number;
};

export type CacheHitRow = {
  agent_id: string;
  agent_name: string;
  hit_count: number;
  miss_count: number;
  hit_rate: number;             // 0..1
};

// Delegation-chain visualization (§9.4). Each row is one agent's slice
// of a single trace_id chain; the UI renders as a stacked bar.
export type ChainCostRow = {
  trace_id: string;
  agent_id: string;
  agent_name: string;
  model: string;
  total_cents: number;
  event_count: number;
  started_at: string;
  ended_at: string;
};

// Credential-pool stats (§9.4). Backed by Phase 8B ListCredentialsWithBindings.
export type CredentialPoolStats = {
  credential_id: string;
  name: string;
  credential_type: "provider" | "tool_secret" | "webhook_hmac" | "sso_oidc_secret";
  usage_count: number;
  last_used_at: string | null;
  last_rotated_at: string | null;
  cooldown_until: string | null;
  bound_agents_count: number;
};

export type AgentMetric = {
  agent_id: string;
  period_start: string;
  period_type: "hourly" | "daily";
  tasks_completed: number;
  tasks_failed: number;
  tasks_timed_out: number;                  // §9.3
  delegations_sent: number;                 // §9.3
  delegations_received: number;             // §9.3
  chat_messages_handled: number;            // §9.3
  avg_resolution_seconds: number | null;
  p95_resolution_seconds: number | null;
  avg_response_seconds: number | null;
  total_cost_cents: number;
  avg_cost_per_task_cents: number;
  cache_hits: number;
  cache_hit_rate: number | null;
};
```

Dashboard pages + views (all TanStack Query hooks, no direct api.* in components per CLAUDE.md):

- `CostSummaryCard` — total spend, cache-hit rate, batch savings.
- `CostBreakdownTable` — model × agent × tier rows with pct_of_total.
- `RoutingEffectivenessChart` — recharts pie chart: % per tier.
- `CacheHitsTable` — per-agent hit/miss/rate.
- `DelegationCostChart` — recharts stacked bar: one trace, bars per agent colored by model.
- `CredentialPoolsTable` — Phase 8B credential stats with rotation badges.
- `PerformanceTab` — recharts line charts for tasks_completed/p95_resolution/cache_hit_rate over time + comparison view when 2–3 agent IDs selected.
- `BudgetAlertsList` — pulls from Phase 1 `budget_policy`; displays current spend vs warning_threshold/hard_stop.

All seven components follow the Phase 6 `CapabilityBrowser` render-pattern (query hook → loading/empty state → rendered table/chart). Comparison view is a prop on `PerformanceTab` that accepts `agentIds: string[]` and overlays series.

- [ ] **Step 2: Commit**

```bash
pnpm --filter @multica/views exec vitest run costs/ agents/components/tabs/performance-tab.test.tsx
git add packages/core/ packages/views/costs/ packages/views/agents/components/tabs/performance-tab.tsx
git commit -m "feat(views): cost dashboard + per-agent performance tab

TanStack Query hooks; recharts line charts for trend views.
PerformanceTab shows SLA indicator (p95 vs agent.sla_p95_seconds)."
```

---

### Task 11: Integration Test + Phase 9 Verification

**Files:**
- Create: `server/internal/integration/cost/cache_e2e_test.go`
- Create: `docs/engineering/verification/phase-9.md`

- [ ] **Step 1: Integration test — cache round-trip**

```go
func TestCostOptimization_CacheHitSavesMoney(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("cost")
    agentID := env.SeedAgent(ws, "support", env.StubModel([]testsupport.TurnScript{{Text: "opening hours 9-5"}}))

    // First request: cache miss → upstream call → cost_event with actual cost.
    env.EnqueueTaskAndWait(ws, agentID, "what hours?")
    events1 := env.ListCostEvents(agentID)
    if len(events1) != 1 || events1[0].CostStatus != "actual" {
        t.Fatalf("first call: %+v", events1)
    }
    firstCost := events1[0].CostCents

    // Second request (near-identical): cache hit → cost_event with 0 cents.
    env.EnqueueTaskAndWait(ws, agentID, "tell me your hours")
    events2 := env.ListCostEvents(agentID)
    hit := events2[len(events2)-1]
    if hit.CostStatus != "included" { t.Errorf("status = %q", hit.CostStatus) }
    if hit.CostCents != 0 { t.Errorf("cost = %d, want 0", hit.CostCents) }
    if firstCost == 0 { t.Error("first call had zero cost — stub model too cheap to detect") }
}
```

- [ ] **Step 2: Manual verification per PLAN.md §9.5**

- [ ] Low-priority tasks routed through Batch API; completion reaches normal task-completion path.
- [ ] Batch API + prompt cache stack to ~95% savings — submit a batch item with a `cache_control` prefix matching a prior cache-written response; the resulting `cost_event` has `cache_read_tokens > 0` AND `cost_cents ≈ baseline × 0.05` (1 − 0.5 batch discount × 0.9 cache-read savings, within rounding).
- [ ] Semantic cache serves repeated near-identical queries without upstream LLM call.
- [ ] Cost dashboard shows accurate totals + cache-hit breakdown.
- [ ] Budget alerts fire at warning threshold (reuses Phase 1 BudgetService).
- [ ] Agent performance metrics aggregate correctly hourly + daily.
- [ ] Comparison view (side-by-side agents on dashboard) works.
- [ ] SLA breach triggers outbound webhook via Phase 8A subscribers.

- [ ] **Step 3: Write evidence + commit**

```markdown
# Phase 9 Verification — 2026-04-DD

- `go test ./internal/integration/cost/...` → pass
- `make check` → pass
- Manual: [results]
- Known limitations:
  - Cross-agent cache sharing deferred (per Scope Note).
  - Cost forecasting ML deferred.
```

```bash
git add server/internal/integration/cost/ docs/engineering/verification/phase-9.md
git commit -m "test(phase-9): cache round-trip e2e + verification evidence"
```

---

## Placeholder Scan

Self-reviewed. Intentional abbreviations:
1. Task 3 repeats the cache-query shape for 1024/1536/3072 — shown once in full for 1024, stubbed for the other two because they're identical except for the partition name.
2. Task 7 batch-provider adapters (`batch_anthropic.go`, `batch_openai.go`) are described by shape + "< 100 lines thin SDK wrapper" — the specific SDK surfaces vary with vendor-SDK versions; the plan shows the `BatchProvider` interface the executor consumes.
3. Task 9 `Breakdown` + `CacheHits` handlers follow the same pattern as `Summary`; repeating would bloat without information.
4. Task 10 Views: same CRUD + chart pattern as Phase 6/7/8A.

No TBDs, no unreferenced symbols.

## Execution Handoff

Plan complete at `docs/superpowers/plans/2026-04-14-phase9-cost-optimization.md`.

**1. Subagent-Driven (recommended)** — fresh subagent per task.

**2. Inline Execution** — via `superpowers:executing-plans`.

Which approach?
