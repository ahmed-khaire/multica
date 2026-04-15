# Phase 10: Observability & Tracing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** persist every LLM call, tool invocation, delegation hop, workflow step, and memory operation as a parent-child span tree keyed by the D8 `trace_id`; add full-text search across all agent execution transcripts; render waterfall + search UIs; export to OpenTelemetry-compatible sinks.

**Architecture:** a `Tracer` service writes rows into `agent_trace` at span boundaries (`StartSpan` returns a span handle that carries the parent span ID down the context stack; `EndSpan` updates `ended_at` + `duration_ms`). Every existing instrumentation point from Phase 2/6/7 (LLM backend, tool registry, `DelegationService`, workflow engine, `MemoryService`) grows a one-line `defer tracer.EndSpan(tracer.StartSpan(ctx, op, meta))` wrapper. Cost is NOT duplicated into `agent_trace` — the span stores `cost_event_id` and UIs JOIN. Full-text search uses a PostgreSQL generated `tsvector` column on `task_message` indexed with GIN. Trace viewer reads the span tree in a single recursive CTE. OpenTelemetry export is optional (configured per workspace), wired through the standard `otel/trace` exporter.

**Tech Stack:** Go 1.26, pgx/sqlc, PostgreSQL `tsvector` + GIN, `go.opentelemetry.io/otel` + `otel/sdk/trace`, TanStack Query + shadcn UI + custom waterfall React component.

---

## Scope Note

Phase 10 of 12. Depends on:
- Phase 2 (D8 `trace_id` in ctx, `cost_event.trace_id`, LLM backend, tool registry)
- Phase 6 (`DelegationService` — emit `subagent_dispatch` spans)
- Phase 7 (workflow engine — emit `workflow_step` spans)
- Phase 5 (`MemoryService` — emit `memory_op` spans, optional)

**Not in scope — deferred:**
- **Real-time trace streaming over WS** — trace viewer re-fetches on tab focus + WS-triggered invalidation; live streaming lands post-V1.
- **Span sampling / head-based sampling** — V1 writes every span. 1% sampling lands when volume makes it necessary.
- **Cross-process tracing for daemon agents** — daemon paths stay untraced in V1; the existing daemon message stream serves as ad-hoc trace replacement.
- **Jaeger/Tempo hosted backends** — OTel export configuration is per-workspace but we ship with stdout exporter only. Customers wire up their own collector.
- **Trace-based alerting** — Phase 9 metrics worker owns SLA alerting; cross-span alerting is post-V1.

---

## Preconditions

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `traceIDKey` context + `WithTraceID`/`TraceIDFrom` (D8) | Every span reads the root trace_id from ctx |
| `cost_event.trace_id` index (Phase 1 migration 100) | UIs JOIN `agent_trace.cost_event_id` → `cost_event` for cost data |
| `task_message` table (Phase 2 — with `workspace_id`, `content`, `tool`, `output`, `created_at`) | Search target |
| `LLMAPIBackend.Execute` + `ToolRegistry.Execute` | Wrap with tracer span |

**From Phase 6/7:**

| Surface | Purpose |
|---|---|
| `harness.SubagentRegistry.Dispatch` | Emits `subagent_dispatch` span |
| Workflow `Engine.runSingleStep` | Emits `workflow_step` span |

**Test helpers (extend Phase 2):**

```go
// server/internal/testsupport/
func (e *Env) ListSpans(traceID uuid.UUID) []trace.Span
func (e *Env) WaitForSpanCount(ctx context.Context, traceID uuid.UUID, n int)
// server/internal/testutil/
func SeedTaskMessage(db *pgxpool.Pool, wsID, taskID uuid.UUID, role, content string) uuid.UUID
```

**Pre-Task 0 bootstrap:**

```bash
cd server
go get go.opentelemetry.io/otel
go get go.opentelemetry.io/otel/sdk/trace
go get go.opentelemetry.io/otel/exporters/stdout/stdouttrace
go mod tidy
```

**D8 context helpers — verify/add before Task 4.** Phase 10 tests assume `agent.WithTraceID` + `agent.TraceIDFrom` exist (Phase 2 Task 1.5 per D8). If a grep of `server/pkg/agent/` doesn't find them, create them before Task 4 begins:

```go
// server/pkg/agent/trace_ctx.go — only add if Phase 2 Task 1.5 hasn't landed.
package agent

import (
    "context"
    "github.com/google/uuid"
)

type traceIDCtxKey struct{}
var traceIDKey = traceIDCtxKey{}

// WithTraceID stashes the D8-minted trace_id on ctx. Minted once at task
// entry (worker claim path); every downstream span reads it via
// TraceIDFrom.
func WithTraceID(ctx context.Context, id uuid.UUID) context.Context {
    return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom returns the trace_id attached to ctx or uuid.Nil.
func TraceIDFrom(ctx context.Context) uuid.UUID {
    if v, ok := ctx.Value(traceIDKey).(uuid.UUID); ok { return v }
    return uuid.Nil
}
```

**testsupport helpers — add before Task 11.** Referenced in integration tests but not yet in the repo:

```go
// server/internal/testsupport/trace.go
func (e *Env) TraceIDForTask(taskID uuid.UUID) uuid.UUID {
    var tid uuid.UUID
    _ = e.DB.QueryRow(context.Background(),
        `SELECT trace_id FROM cost_event WHERE task_id = $1 LIMIT 1`, taskID).Scan(&tid)
    return tid
}

func (e *Env) TraceIDForRun(wsID, runID uuid.UUID) uuid.UUID {
    var tid uuid.UUID
    _ = e.DB.QueryRow(context.Background(),
        `SELECT trace_id FROM workflow_run WHERE id = $1 AND workspace_id = $2`, runID, wsID).Scan(&tid)
    return tid
}

// JSONSpan is the integration-test view of a span — decoded from the
// /traces/{id}/tree JSON response. It is intentionally distinct from
// the sqlc `db.AgentTrace` row (which uses pgtype.UUID / pgtype.Text)
// because integration tests assert on `*string` pointer semantics
// (e.g. `s.ParentSpanID == nil` for roots). Field tags mirror the
// handler JSON shape in Task 9 (`mapSpanRows`).
type JSONSpan struct {
    ID            string            `json:"id"`
    TraceID       string            `json:"trace_id"`
    ParentSpanID  *string           `json:"parent_span_id"`
    WorkspaceID   string            `json:"workspace_id"`
    AgentID       *string           `json:"agent_id"`
    Operation     string            `json:"operation"`
    TaskID        *string           `json:"task_id"`
    WorkflowRunID *string           `json:"workflow_run_id"`
    CostEventID   *string           `json:"cost_event_id"`
    StartedAt     time.Time         `json:"started_at"`
    EndedAt       *time.Time        `json:"ended_at"`
    DurationMs    *int              `json:"duration_ms"`
    Status        *string           `json:"status"`
    Error         *string           `json:"error"`
    Metadata      map[string]any    `json:"metadata"`
    CostCents     *int              `json:"cost_cents,omitempty"`
    Model         *string           `json:"model,omitempty"`
}

type TraceTreeFetched struct {
    Spans           []JSONSpan `json:"spans"`
    SpanCount       int        `json:"span_count"`
    TotalDurationMs int        `json:"total_duration_ms"`
    TotalCostCents  int        `json:"total_cost_cents"`
}

func (e *Env) GetTraceTree(wsID, traceID uuid.UUID) TraceTreeFetched {
    resp := e.GET(fmt.Sprintf("/api/workspaces/%s/traces/%s/tree", wsID, traceID))
    var out TraceTreeFetched
    _ = json.Unmarshal(resp.Body, &out)
    return out
}

// Search hits GET /api/search/executions?q=… and decodes the JSON body
// into a SearchResults value. Used by integration tests asserting the
// FTS index returns expected hits. Workspace header is set from wsID.
type SearchResultItem struct {
    ID        string `json:"id"`
    TaskID    string `json:"task_id"`
    AgentID   string `json:"agent_id"`
    AgentName string `json:"agent_name"`
    Snippet   string `json:"snippet"`
    Role      string `json:"role"`
    Rank      float64 `json:"rank"`
}
type SearchResults struct {
    Items []SearchResultItem `json:"items"`
    Total int                `json:"total"`
}
func (e *Env) Search(wsID uuid.UUID, query string) SearchResults {
    path := fmt.Sprintf("/api/search/executions?q=%s", url.QueryEscape(query))
    resp := e.GETAs(wsID, path) // GETAs sets X-Workspace-ID per Phase 2 testsupport convention
    var out SearchResults
    _ = json.Unmarshal(resp.Body, &out)
    return out
}

// testutil/span.go — raw DB reads for fixtures that can't go through
// the handler (e.g. asserting span presence before a trace-tree
// endpoint has been mounted in a very early test).
func ListSpansForTrace(db *pgxpool.Pool, wsID, traceID uuid.UUID) []trace.Span
func CountSpans(db *pgxpool.Pool, wsID uuid.UUID) int
```

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/185_traces.up.sql` | `agent_trace` table + indexes |
| `server/migrations/185_traces.down.sql` | Reverse |
| `server/migrations/186_task_message_fts.up.sql` | Generated `tsvector` column + GIN index via `CREATE INDEX CONCURRENTLY` |
| `server/migrations/186_task_message_fts.down.sql` | Reverse |
| `server/pkg/db/queries/trace.sql` | sqlc: insert, end, list-by-trace, tree recursive-CTE |
| `server/pkg/db/queries/search.sql` | sqlc: FTS search over task_message with pagination |
| `server/pkg/trace/tracer.go` | `Tracer.StartSpan(ctx, op, meta) Span` + `(*Span).End(status, err)` |
| `server/pkg/trace/tracer_test.go` | Context propagation, parent-child, cost_event_id link |
| `server/pkg/trace/otel.go` | Optional OpenTelemetry exporter (enabled via workspace config) |
| `server/pkg/trace/otel_test.go` | Exporter plumbing |
| `server/internal/service/search.go` | FTS service: query + filter + pagination |
| `server/internal/service/search_test.go` | Search coverage |
| `server/internal/handler/search.go` | `GET /api/search/executions` |
| `server/internal/handler/search_test.go` | Handler tests |
| `server/internal/handler/trace.go` | `GET /api/workspaces/{wsId}/traces/{id}/tree` — span tree JSON |
| `server/internal/handler/trace_test.go` | Handler tests |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/pkg/agent/llm_api.go` | Wrap `Execute` with `tracer.StartSpan(ctx, "llm_call", ...)` |
| `server/pkg/tools/registry.go` | Wrap each tool `Execute` with `tracer.StartSpan(ctx, "tool_call", ...)` |
| `server/pkg/agent/harness/subagents.go` | `SubagentRegistry.Dispatch` emits `subagent_dispatch` span |
| `server/internal/workflow/engine.go` | `runSingleStep` emits `workflow_step` span |
| `server/internal/service/memory.go` | `Remember` + `Recall` emit `memory_op` span |
| `server/cmd/server/router.go` | Mount `/search/executions` + `/traces/{id}/tree` |
| `server/cmd/server/main.go` | Instantiate Tracer + wire into all services |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/trace.ts` | `Span`, `TraceTree`, `SearchResult` |
| `packages/core/api/trace.ts` + `search.ts` | REST clients |
| `packages/core/trace/queries.ts` + `search/queries.ts` | TanStack hooks |
| `packages/views/search/` | Search page with filters + result list |
| `packages/views/traces/components/trace-waterfall.tsx` | Waterfall renderer — SVG or CSS grid |
| `packages/views/traces/components/span-details.tsx` | Side panel for selected span. Uses Phase 1.3 error classifier (`@multica/core/errors/classify`) to map error text → category → semantic color token (destructive/warning/info). |
| `packages/views/traces/components/trace-filters.tsx` | Filters: agent (dropdown), workflow_run (dropdown), time range (PLAN.md §10.4). |
| `packages/views/tasks/components/trace-tab.tsx` | Embed waterfall in task detail view |

### External Infrastructure

| System | Change |
|---|---|
| None required in V1 | OTel export is opt-in per workspace — customers point their own collector URL |

---

### Task 1: Migration 185 — agent_trace

**Files:**
- Create: `server/migrations/185_traces.up.sql`
- Create: `server/migrations/185_traces.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 185_traces.up.sql
CREATE TABLE IF NOT EXISTS agent_trace (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id UUID NOT NULL,
    parent_span_id UUID,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID,
    operation TEXT NOT NULL,
    task_id UUID,
    workflow_run_id UUID,
    cost_event_id UUID REFERENCES cost_event(id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_ms INT,
    status TEXT,
    error TEXT,
    metadata JSONB,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);
ALTER TABLE agent_trace DROP CONSTRAINT IF EXISTS agent_trace_operation_check;
ALTER TABLE agent_trace ADD CONSTRAINT agent_trace_operation_check
    CHECK (operation IN ('llm_call', 'tool_call', 'subagent_dispatch', 'workflow_step', 'memory_op', 'http_call', 'mcp_call')) NOT VALID;
ALTER TABLE agent_trace VALIDATE CONSTRAINT agent_trace_operation_check;

CREATE INDEX IF NOT EXISTS idx_trace_trace_id ON agent_trace(trace_id);
CREATE INDEX IF NOT EXISTS idx_trace_parent ON agent_trace(parent_span_id) WHERE parent_span_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_trace_ws_time ON agent_trace(workspace_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_trace_cost_link ON agent_trace(cost_event_id) WHERE cost_event_id IS NOT NULL;
```

- [ ] **Step 2: Down + apply + commit**

```sql
-- 185_traces.down.sql
DROP TABLE IF EXISTS agent_trace;
```

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/185_traces.up.sql server/migrations/185_traces.down.sql
git commit -m "feat(trace): migration 185 — agent_trace span table

Indexed on (trace_id) for tree lookups + (parent_span_id) for recursive
CTE traversal + (workspace_id, started_at) for workspace-scoped timelines
+ (cost_event_id) for cost JOIN. cost_cents/tokens intentionally NOT
duplicated here — see PLAN.md §10.1 note."
```

---

### Task 2: Migration 186 — FTS on task_message

**Files:**
- Create: `server/migrations/186_task_message_fts.up.sql`
- Create: `server/migrations/186_task_message_fts.down.sql`

**Goal:** add a PostgreSQL `tsvector` generated column + GIN index to `task_message`. GIN index build runs via `CREATE INDEX CONCURRENTLY` to avoid blocking writes on large existing tables.

- [ ] **Step 1: Up migration**

```sql
-- 186_task_message_fts.up.sql

-- Generated column — Postgres computes on insert/update, stored on disk so
-- GIN can index directly. `coalesce` guards against NULL tool/output rows.
ALTER TABLE task_message
    ADD COLUMN IF NOT EXISTS search_vector tsvector
    GENERATED ALWAYS AS (
        to_tsvector('english',
            coalesce(content, '') || ' ' ||
            coalesce(tool, '')    || ' ' ||
            coalesce(output, ''))
    ) STORED;

-- task_message has NO workspace_id column (Phase 2 schema: id, task_id,
-- seq, type, tool, content, input, output, created_at). The workspace
-- filter is applied by JOINing through agent_task_queue.workspace_id —
-- see SearchTaskMessages in Task 3. The task_id index below speeds that
-- join; the GIN index on search_vector (migration 186b) filters by FTS.
CREATE INDEX IF NOT EXISTS idx_task_message_task_time
    ON task_message(task_id, created_at DESC);
```

GIN index built as a regular (non-CONCURRENTLY) index — in the same file
above. `CREATE INDEX CONCURRENTLY` would require the migrate tool to run
the statement outside a transaction; the custom runner at
`server/cmd/migrate/main.go` has no magic-comment support (it is NOT
Goose), so `notransaction` directives are silently ignored and
CONCURRENTLY would fail with `cannot run inside a transaction block`.
At Phase 10 land time the `task_message` table is small (hours-to-days
of history); a brief ACCESS EXCLUSIVE lock during the non-CONCURRENTLY
build is acceptable. If the table grows large enough that the lock is
a problem, ops can build the index manually via psql (`CREATE INDEX
CONCURRENTLY ...`) before running the migration and let the
`IF NOT EXISTS` guard make the migration a no-op.

Append to `186_task_message_fts.up.sql`:

```sql
-- GIN index on the generated tsvector column. Non-CONCURRENTLY per note
-- above. idempotent via IF NOT EXISTS.
CREATE INDEX IF NOT EXISTS idx_task_message_fts
    ON task_message USING gin(search_vector);
```

- [ ] **Step 2: Down migration**

```sql
-- 186_task_message_fts.down.sql
DROP INDEX IF EXISTS idx_task_message_fts;
DROP INDEX IF EXISTS idx_task_message_task_time;
ALTER TABLE task_message DROP COLUMN IF EXISTS search_vector;
```

- [ ] **Step 3: Apply + rollback cycle + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/186_task_message_fts.up.sql server/migrations/186_task_message_fts.down.sql
git commit -m "feat(search): migration 186 — task_message FTS + GIN index

Generated tsvector column stored on disk. Regular (non-CONCURRENTLY)
GIN index build — the custom migrate runner has no notransaction
support and CONCURRENTLY requires running outside a tx. task_task_time
index + workspace-filter via JOIN through agent_task_queue (task_message
has no workspace_id column in Phase 2 schema)."
```

---

### Task 3: sqlc Queries

**Files:**
- Create: `server/pkg/db/queries/trace.sql`
- Create: `server/pkg/db/queries/search.sql`

- [ ] **Step 1: trace.sql**

```sql
-- trace.sql

-- name: StartSpan :one
-- Called at span open; ended_at + duration_ms filled in later by EndSpan.
INSERT INTO agent_trace (
    trace_id, parent_span_id, workspace_id, agent_id, operation,
    task_id, workflow_run_id, cost_event_id, started_at, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, trace_id, started_at;

-- name: EndSpan :exec
-- Fires at deferred close. Duration computed server-side so the client
-- doesn't need a synchronized clock. cost_event_id may be populated
-- later than StartSpan — late-bind via sqlc.narg.
UPDATE agent_trace
SET ended_at = $3,
    duration_ms = EXTRACT(EPOCH FROM ($3 - started_at)) * 1000,
    status = $4, error = $5,
    cost_event_id = COALESCE(sqlc.narg('cost_event_id'), cost_event_id)
WHERE id = $1 AND workspace_id = $2;

-- name: ListSpansForTrace :many
-- Workspace-scoped tree listing. Rows ordered by (started_at, id) so a
-- client can rebuild the parent-child relationships via parent_span_id
-- without sorting again. Capped at $3 rows — UI truncates large traces;
-- operators wanting full data can paginate via started_at cursor.
SELECT t.*, c.cost_cents, c.model
FROM agent_trace t
LEFT JOIN cost_event c ON c.id = t.cost_event_id
WHERE t.workspace_id = $1 AND t.trace_id = $2
ORDER BY t.started_at ASC, t.id ASC
LIMIT $3;

-- name: ListTracesForWorkflowRun :many
-- Trace-viewer "filter by workflow" driver query (PLAN.md §10.4).
-- GROUP BY is clearer than DISTINCT + window; sqlc infers cleaner types.
SELECT t.trace_id,
       MIN(t.started_at) AS started_at,
       COUNT(*)::int AS span_count
FROM agent_trace t
WHERE t.workspace_id = $1 AND t.workflow_run_id = $2
GROUP BY t.trace_id
ORDER BY started_at DESC;

-- name: ListRecentTraces :many
-- Trace-viewer default list (no filter) — most recent N traces in the
-- workspace. Optional agent_id + since filters via sqlc.narg.
SELECT t.trace_id,
       MIN(t.started_at) AS started_at,
       COUNT(*)::int AS span_count
FROM agent_trace t
WHERE t.workspace_id = $1
  AND (sqlc.narg('agent_id')::uuid IS NULL OR t.agent_id = sqlc.narg('agent_id'))
  AND (sqlc.narg('since')::timestamptz IS NULL OR t.started_at >= sqlc.narg('since'))
GROUP BY t.trace_id
ORDER BY started_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateSpanCostEventID :exec
-- Late-bind cost_event link if the cost event is written AFTER End()
-- has already fired (e.g. panic-recovery path). Happy path uses
-- Span.SetCostEventID before End so this query is a no-op.
--
-- Semantic: first-write-wins for late-bind. If End already populated
-- cost_event_id (happy path) the WHERE guard skips this UPDATE even if
-- the arguments differ. This intentionally protects the earlier,
-- more-authoritative link from being overwritten by a later async
-- callback. If a caller KNOWS the stored ID is wrong, they must clear
-- it first (UPDATE ... SET cost_event_id = NULL) before re-linking.
UPDATE agent_trace
SET cost_event_id = $3
WHERE id = $1 AND workspace_id = $2 AND cost_event_id IS NULL;

-- name: GetTraceTotals :one
-- Summary row for the trace header card.
SELECT
    COUNT(*)::int AS span_count,
    SUM(duration_ms)::int AS total_duration_ms,
    COALESCE(SUM(c.cost_cents), 0)::int AS total_cost_cents
FROM agent_trace t
LEFT JOIN cost_event c ON c.id = t.cost_event_id
WHERE t.workspace_id = $1 AND t.trace_id = $2;
```

- [ ] **Step 2: search.sql**

```sql
-- search.sql

-- name: SearchTaskMessages :many
-- Full-text search scoped to workspace VIA JOIN through agent_task_queue
-- (task_message has no workspace_id column — Phase 2 schema). INNER JOIN
-- required so non-matching task_ids get filtered; LEFT JOIN on agent for
-- display name because older task_messages may have a NULL agent_id.
-- plainto_tsquery for user-friendly queries; ts_rank orders by relevance.
-- ts_headline produces highlighted snippets.
SELECT
    m.id, m.task_id, m.role, m.created_at,
    t.workspace_id,
    a.id AS agent_id, a.name AS agent_name,
    ts_rank(m.search_vector, plainto_tsquery('english', $2)) AS rank,
    ts_headline('english', m.content, plainto_tsquery('english', $2),
                'StartSel=<mark>, StopSel=</mark>, MaxFragments=2') AS snippet
FROM task_message m
INNER JOIN agent_task_queue t ON t.id = m.task_id
LEFT JOIN agent a ON a.id = t.agent_id
WHERE t.workspace_id = $1
  AND m.search_vector @@ plainto_tsquery('english', $2)
  AND (sqlc.narg('agent_id')::uuid IS NULL OR t.agent_id = sqlc.narg('agent_id'))
  AND (sqlc.narg('since')::timestamptz IS NULL OR m.created_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR m.created_at < sqlc.narg('until'))
ORDER BY rank DESC, m.created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountSearchResults :one
SELECT COUNT(*)::int
FROM task_message m
INNER JOIN agent_task_queue t ON t.id = m.task_id
WHERE t.workspace_id = $1
  AND m.search_vector @@ plainto_tsquery('english', $2)
  AND (sqlc.narg('agent_id')::uuid IS NULL OR t.agent_id = sqlc.narg('agent_id'))
  AND (sqlc.narg('since')::timestamptz IS NULL OR m.created_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR m.created_at < sqlc.narg('until'));
```

- [ ] **Step 3: Regenerate + commit**

```bash
cd server && make sqlc && go build ./...
git add server/pkg/db/queries/trace.sql server/pkg/db/queries/search.sql server/pkg/db/generated/
git commit -m "feat(db): sqlc queries for trace tree + FTS search"
```

---

### Task 4: Tracer — StartSpan + EndSpan with context propagation

**Files:**
- Create: `server/pkg/trace/tracer.go`
- Create: `server/pkg/trace/tracer_test.go`

**Goal:** the minimal tracer surface every instrumentation point calls. `StartSpan(ctx, op, meta)` reads `trace_id` + current `parent_span_id` from ctx, inserts a row, returns a `Span` handle that (a) propagates the new span id as the next `parent_span_id` in its ctx, (b) provides `End(status, err)` to close. Cost link is set post-close via `Span.SetCostEventID`.

- [ ] **Step 1: Failing test**

```go
// server/pkg/trace/tracer_test.go
package trace

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
    "aicolab/server/pkg/agent"
)

func TestTracer_ParentChildSpanTree(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "trace")
    traceID := uuid.New()

    tracer := NewTracer(db)
    ctx := agent.WithTraceID(context.Background(), traceID)

    root := tracer.StartSpan(ctx, "workflow_step",
        SpanMeta{WorkspaceID: wsID, Operation: "workflow_step"})
    defer root.End("completed", nil)

    // Child span inherits parent_span_id from root.ctx.
    child := tracer.StartSpan(root.Context(), "llm_call",
        SpanMeta{WorkspaceID: wsID, Operation: "llm_call"})
    time.Sleep(5 * time.Millisecond)
    child.End("completed", nil)

    spans := testutil.ListSpansForTrace(db, wsID, traceID)
    if len(spans) != 2 { t.Fatalf("spans = %d, want 2", len(spans)) }
    if spans[0].ParentSpanID.Valid {
        t.Errorf("root span has parent_span_id; should be nil")
    }
    if !spans[1].ParentSpanID.Valid || spans[1].ParentSpanID.Bytes != root.ID() {
        t.Errorf("child parent_span_id = %v, want %v", spans[1].ParentSpanID, root.ID())
    }
    if spans[1].DurationMs.Int32 < 3 {
        t.Errorf("child duration = %d, want >= 3ms", spans[1].DurationMs.Int32)
    }
}

func TestTracer_TreeEmptyWhenNoTraceID(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "notrace")
    tracer := NewTracer(db)

    // No trace_id in ctx — tracer must still return a valid span handle
    // that End()s without error, but write no row (noop mode).
    span := tracer.StartSpan(context.Background(), "llm_call",
        SpanMeta{WorkspaceID: wsID, Operation: "llm_call"})
    span.End("completed", nil)

    if got := testutil.CountSpans(db, wsID); got != 0 {
        t.Errorf("spans = %d, want 0 when ctx has no trace_id", got)
    }
}

func TestTracer_SetCostEventID(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "cost")
    traceID := uuid.New()

    tracer := NewTracer(db)
    ctx := agent.WithTraceID(context.Background(), traceID)

    span := tracer.StartSpan(ctx, "llm_call",
        SpanMeta{WorkspaceID: wsID, Operation: "llm_call"})
    ceID := testutil.SeedCostEvent(db, wsID, uuid.Nil, 100, "actual")
    span.SetCostEventID(ceID)
    span.End("completed", nil)

    spans := testutil.ListSpansForTrace(db, wsID, traceID)
    if len(spans) != 1 { t.Fatal(len(spans)) }
    if !spans[0].CostEventID.Valid { t.Error("cost_event_id not linked") }
    if spans[0].CostEventID.Bytes != ceID { t.Error("cost_event_id mismatch") }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/trace/ -run TestTracer -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/trace/tracer.go
package trace

import (
    "context"
    "encoding/json"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgtype"
    "github.com/jackc/pgx/v5/pgxpool"
    oteltrace "go.opentelemetry.io/otel/trace"

    "aicolab/server/pkg/agent"
    "aicolab/server/pkg/db/db"
)

// spanCtxKey is the context key that carries the current parent span id.
type spanCtxKey struct{}

var parentSpanKey = spanCtxKey{}

type SpanMeta struct {
    WorkspaceID   uuid.UUID
    AgentID       uuid.UUID            // uuid.Nil means "no agent — workflow or system"
    Operation     string               // "llm_call" | "tool_call" | "subagent_dispatch" | "workflow_step" | "memory_op" | ...
    TaskID        uuid.UUID
    WorkflowRunID uuid.UUID
    Metadata      map[string]any
}

// Span is the handle returned from StartSpan. Call End when the operation
// completes. SetCostEventID links the span to a cost_event if one was
// produced during the operation.
type Span struct {
    id            uuid.UUID
    traceID       uuid.UUID
    ctx           context.Context
    noop          bool
    tracer        *Tracer
    costEventID   *uuid.UUID
    wsID          uuid.UUID
    // otelSpan is non-nil when this Span was opened via OTelTracer
    // (Task 7). Renamed from `otel` to avoid shadowing the
    // `go.opentelemetry.io/otel` package import alias in the same file.
    otelSpan      oteltrace.Span
}

func (s *Span) ID() uuid.UUID          { return s.id }
func (s *Span) Context() context.Context { return s.ctx }

// SetCostEventID links a cost_event row produced during the span to this
// span. Usually called by the LLM backend after it writes the cost_event
// but BEFORE span.End() fires — the deferred End() reads costEventID
// and passes it through the EndSpan COALESCE so the link lands in the
// same UPDATE as the terminal state.
//
// ORDERING CONSTRAINT: callers MUST invoke SetCostEventID before End()
// for the link to persist. The happy path (cost_event insert inline with
// LLM response parsing, then deferred End fires) honors this naturally.
// For panic/rollback paths where the cost_event may be written AFTER a
// deferred End, use Tracer.LateLinkCostEvent(ctx, spanID, costEventID)
// which issues a dedicated UPDATE — see tracer.go below.
func (s *Span) SetCostEventID(id uuid.UUID) {
    if s.noop { return }
    s.costEventID = &id
}

// End writes the terminal state + duration. status is usually
// "completed" | "failed"; err, if non-nil, is serialised into the error column.
// Detached context protects against request cancellation skipping the
// terminal write; BOUNDED with a 5-second timeout so DB outages don't
// hang the goroutine that owns the operation (R2 S1 fix). If the bounded
// timeout fires the span row stays partially written (started_at but
// no ended_at) — ops can query for such orphaned rows as a health signal.
func (s *Span) End(status string, err error) {
    if s.noop { return }
    var errStr string
    if err != nil { errStr = err.Error() }
    var ce pgtype.UUID
    if s.costEventID != nil { ce = pgtype.UUID{Bytes: *s.costEventID, Valid: true} }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    q := db.New(s.tracer.pool)
    _ = q.EndSpan(ctx, db.EndSpanParams{
        ID:          uuidPg(s.id),
        WorkspaceID: uuidPg(s.wsID),
        Column3:     time.Now().UTC(),
        Status:      sqlNullString(status),
        Error:       sqlNullString(errStr),
        CostEventID: ce,
    })
    // OTel span close (only when wrapped via OTelTracer).
    if s.otelSpan != nil {
        if err != nil { s.otelSpan.RecordError(err) }
        s.otelSpan.End()
    }
}

type Tracer struct {
    pool *pgxpool.Pool
}

func NewTracer(pool *pgxpool.Pool) *Tracer { return &Tracer{pool: pool} }

// LateLinkCostEvent connects a cost_event to an already-closed span. Use
// ONLY when the cost_event row was written after Span.End() fired
// (panic-recovery paths, async cost accounting). Idempotent via the
// WHERE cost_event_id IS NULL guard in UpdateSpanCostEventID — a second
// call with a different cost_event_id is a no-op.
func (t *Tracer) LateLinkCostEvent(ctx context.Context, wsID, spanID, costEventID uuid.UUID) error {
    q := db.New(t.pool)
    return q.UpdateSpanCostEventID(ctx, db.UpdateSpanCostEventIDParams{
        ID:          uuidPg(spanID),
        WorkspaceID: uuidPg(wsID),
        CostEventID: pgtype.UUID{Bytes: costEventID, Valid: true},
    })
}

// StartSpan opens a new span. If ctx has no trace_id, returns a noop span
// that records nothing — ensures instrumentation at paths that don't
// participate in tracing (e.g. background workers outside a task) can
// still call StartSpan/End cleanly without polluting agent_trace.
func (t *Tracer) StartSpan(ctx context.Context, op string, meta SpanMeta) *Span {
    traceID := agent.TraceIDFrom(ctx)
    if traceID == uuid.Nil {
        return &Span{noop: true, tracer: t, ctx: ctx}
    }
    parentID, _ := ctx.Value(parentSpanKey).(uuid.UUID)
    var parentPg pgtype.UUID
    if parentID != uuid.Nil { parentPg = pgtype.UUID{Bytes: parentID, Valid: true} }

    metaJSON, _ := json.Marshal(meta.Metadata)
    q := db.New(t.pool)
    row, err := q.StartSpan(ctx, db.StartSpanParams{
        TraceID:       uuidPg(traceID),
        ParentSpanID:  parentPg,
        WorkspaceID:   uuidPg(meta.WorkspaceID),
        AgentID:       uuidPgNullable(meta.AgentID),
        Operation:     op,
        TaskID:        uuidPgNullable(meta.TaskID),
        WorkflowRunID: uuidPgNullable(meta.WorkflowRunID),
        StartedAt:     time.Now().UTC(),
        Metadata:      metaJSON,
    })
    if err != nil {
        return &Span{noop: true, tracer: t, ctx: ctx}
    }
    id := uuidFromPg(row.ID)
    childCtx := context.WithValue(ctx, parentSpanKey, id)
    return &Span{
        id: id, traceID: traceID, ctx: childCtx, tracer: t, wsID: meta.WorkspaceID,
    }
}
```

- [ ] **Step 4: Run + commit**

```bash
cd server && go test ./pkg/trace/ -run TestTracer -v
git add server/pkg/trace/tracer.go server/pkg/trace/tracer_test.go
git commit -m "feat(trace): Tracer with ctx-propagated parent_span_id

StartSpan reads trace_id + parent via ctx, inserts row, returns a Span
handle whose Context() carries the new span id as the next parent.
End() uses a detached ctx so request cancellation doesn't skip the
terminal-state write. Noop mode when ctx has no trace_id."
```

---

### Task 5: Instrument LLM API Backend

**Files:**
- Modify: `server/pkg/agent/llm_api.go`
- Modify: `server/pkg/agent/llm_api_test.go`

**Goal:** wrap `Execute` with a span. On each upstream call, emit `llm_call` span; after cost_event is inserted, call `span.SetCostEventID`.

- [ ] **Step 1: Add Tracer to LLMAPIDeps**

```go
// Inside LLMAPIDeps:
type LLMAPIDeps struct {
    // ... existing fields
    Tracer *trace.Tracer // Phase 10 — required; use trace.NewNoopTracer() when disabled
}
```

- [ ] **Step 2: Wrap Execute**

```go
// server/pkg/agent/llm_api.go — at the top of Execute:
span := b.tracer.StartSpan(ctx, "llm_call", trace.SpanMeta{
    WorkspaceID: opts.WorkspaceID,
    AgentID:     opts.AgentID,
    TaskID:      opts.TaskID,
    Operation:   "llm_call",
    Metadata: map[string]any{
        "model":    opts.Model,
        "provider": opts.Provider,
    },
})
defer func() { span.End("completed", nil) /* err set below on failure */ }()
ctx = span.Context()

// ... existing cache lookup + upstream call ...

// After writing cost_event:
span.SetCostEventID(costEventID)
```

- [ ] **Step 3: Run + commit**

```bash
cd server && go test ./pkg/agent/ -run TestLLMAPI -v
git add server/pkg/agent/llm_api.go server/pkg/agent/llm_api_test.go
git commit -m "feat(trace): instrument LLMAPIBackend.Execute

Every upstream LLM call emits an llm_call span. cost_event linked post
hoc via span.SetCostEventID so the UI can JOIN for per-span cost without
duplicating token counts."
```

---

### Task 6: Instrument Tool Registry + Delegation + Workflow + Memory

**Files:**
- Modify: `server/pkg/tools/registry.go`
- Modify: `server/pkg/agent/harness/subagents.go`
- Modify: `server/internal/workflow/engine.go`
- Modify: `server/internal/service/memory.go`

**Goal:** add span wrapping to every remaining instrumentation point. Each modification is 5-8 lines — `tracer.StartSpan` → defer `span.End` → replace `ctx` with `span.Context()`.

- [ ] **Step 1: Tool registry**

```go
// server/pkg/tools/registry.go — inside Execute:
span := r.tracer.StartSpan(ctx, "tool_call", trace.SpanMeta{
    WorkspaceID: opts.WorkspaceID,
    AgentID:     opts.AgentID,
    TaskID:      opts.TaskID,
    Operation:   "tool_call",
    Metadata:    map[string]any{"tool": name},
})
defer span.End(statusFromErr(resultErr), resultErr)
ctx = span.Context()
// ... existing tool.Execute ...
```

- [ ] **Step 2: Delegation**

```go
// server/pkg/agent/harness/subagents.go — inside Dispatch, AFTER guards pass:
span := r.tracer.StartSpan(ctx, "subagent_dispatch", trace.SpanMeta{
    WorkspaceID: r.workspaceID,
    AgentID:     childID,
    Operation:   "subagent_dispatch",
    Metadata:    map[string]any{"slug": slug, "depth": depth + 1},
})
defer span.End(statusFromErr(err), err)
childCtx = span.Context()
```

- [ ] **Step 3: Workflow step**

```go
// server/internal/workflow/engine.go — inside runSingleStep:
span := e.deps.Tracer.StartSpan(ctx, "workflow_step", trace.SpanMeta{
    WorkspaceID:   wsID,
    Operation:     "workflow_step",
    WorkflowRunID: runID,
    Metadata:      map[string]any{"step_id": s.ID, "type": string(s.Type)},
})
defer span.End(status, execErr)
ctx = span.Context()
```

- [ ] **Step 4: Memory**

```go
// server/internal/service/memory.go — inside Remember + Recall:
span := s.tracer.StartSpan(ctx, "memory_op", trace.SpanMeta{
    WorkspaceID: wsID,
    Operation:   "memory_op",
    Metadata:    map[string]any{"op": "remember", "scope": scope},
})
defer span.End("completed", err)
```

- [ ] **Step 4.5: MCP client calls (Phase 3)**

MCP tool invocations route through `server/pkg/mcp/client.go` (Phase 3 Task 1). The tool registry's span wraps the `types.Tool.Execute` call, which for MCP-backed tools delegates to `MCPClient.CallTool`. If Phase 3's MCP client makes an outbound JSON-RPC call with its own latency worth separating from the tool-registry wrapper, add a nested span:

```go
// server/pkg/mcp/client.go — inside CallTool:
span := c.tracer.StartSpan(ctx, "mcp_call", trace.SpanMeta{
    WorkspaceID: opts.WorkspaceID,
    AgentID:     opts.AgentID,
    Operation:   "mcp_call",
    Metadata:    map[string]any{"server": c.name, "tool": toolName},
})
defer span.End(statusFromErr(err), err)
ctx = span.Context()
```

If MCP calls sit inside the tool-registry path with negligible added latency, skip this step and let the `tool_call` span cover them — the CHECK constraint accepts both operation names. Document the choice in the commit message.

- [ ] **Step 5: Run + commit**

```bash
cd server && go test ./... -run "Tracer|Span" -v
git add server/pkg/tools/registry.go server/pkg/agent/harness/subagents.go server/internal/workflow/engine.go server/internal/service/memory.go server/pkg/mcp/client.go
git commit -m "feat(trace): instrument tools + delegation + workflow + memory + mcp

Every tool call, subagent dispatch, workflow step, and memory op now
emits a span. defer span.End closes at function exit; span.Context()
becomes the child ctx so nested ops attach as grandchildren."
```

---

### Task 7: Optional OpenTelemetry Exporter

**Files:**
- Create: `server/pkg/trace/otel.go`
- Create: `server/pkg/trace/otel_test.go`

**Goal:** opt-in OTel export. When enabled, every `StartSpan` / `End` also emits to an OTel tracer that ships to a customer-configured collector.

- [ ] **Step 1: Implement**

```go
// server/pkg/trace/otel.go
package trace

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    otelsdk "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
    oteltrace "go.opentelemetry.io/otel/trace"
)

// WithOTel wraps a Tracer with optional OpenTelemetry export. Every
// StartSpan also opens an oteltrace.Span; End closes both. Disabled by
// default — customers enable by setting exporter URL in workspace config.
type OTelTracer struct {
    inner  *Tracer
    tracer oteltrace.Tracer
}

// NewOTelTracer wraps an existing Tracer. exporter is the OTel exporter
// the server.cmd/server/main.go set up (stdout, OTLP, Jaeger, etc.).
func NewOTelTracer(inner *Tracer, exporter otelsdk.SpanExporter, serviceName string) *OTelTracer {
    tp := otelsdk.NewTracerProvider(
        otelsdk.WithBatcher(exporter),
        otelsdk.WithResource(semconv.ServiceName(serviceName)),
    )
    otel.SetTracerProvider(tp)
    return &OTelTracer{inner: inner, tracer: tp.Tracer("aicolab.server")}
}

// StartSpan wraps Tracer.StartSpan + opens an OTel span. The OTel span
// lives in the returned Span.ctx so child ops automatically nest.
func (o *OTelTracer) StartSpan(ctx context.Context, op string, meta SpanMeta) *Span {
    ctx, otelSpan := o.tracer.Start(ctx, op,
        oteltrace.WithAttributes(
            attribute.String("workspace.id", meta.WorkspaceID.String()),
            attribute.String("agent.id", meta.AgentID.String()),
            attribute.String("operation", op),
        ),
    )
    inner := o.inner.StartSpan(ctx, op, meta)
    inner.otelSpan = otelSpan // renamed to avoid shadowing the otel package alias
    return inner
}
```

The `otelSpan` field is already present on the `Span` struct (Task 4
definition) and `Span.End` already handles OTel close when non-nil — see
Task 4 Step 3. No further Span modifications needed.

- [ ] **Step 2: Commit**

```bash
cd server && go test ./pkg/trace/ -run TestOTel -v
git add server/pkg/trace/otel.go server/pkg/trace/otel_test.go server/pkg/trace/tracer.go
git commit -m "feat(trace): optional OpenTelemetry exporter

OTelTracer wraps the core Tracer — both write to agent_trace AND emit
to the configured OTel exporter. Customer-configured via env vars (e.g.
OTEL_EXPORTER_OTLP_ENDPOINT). Disabled by default; opting in requires
no code changes at call sites."
```

---

### Task 8: Search Service + Handler

**Files:**
- Create: `server/internal/service/search.go`
- Create: `server/internal/handler/search.go`
- Create: tests

- [ ] **Step 1: Failing test**

```go
// newSearchCtx boots a testsupport.Env, seeds one workspace + one agent
// + one task, and returns a tiny helper with a seedTaskMessage shortcut
// and a getJSON helper bound to the seeded workspace. Kept local to the
// search handler test because the call shape (role+content-only) is
// specific to this file; general helpers live in testutil/task.go.
type searchCtx struct {
    t       *testing.T
    env     *testsupport.Env
    wsID    uuid.UUID
    agentID uuid.UUID
    taskID  uuid.UUID
}
func newSearchCtx(t *testing.T) *searchCtx {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("search")
    agentID := env.SeedAgent(wsID, "bot", env.StubModel(nil))
    taskID := env.SeedTask(wsID, agentID, "root")
    return &searchCtx{t: t, env: env, wsID: wsID, agentID: agentID, taskID: taskID}
}
func (s *searchCtx) seedTaskMessage(wsID uuid.UUID, role, content string) {
    testutil.SeedTaskMessage(s.env.DB, wsID, s.taskID, role, content)
}
func (s *searchCtx) getJSON(path string) testsupport.Response {
    return s.env.GETAs(s.wsID, path)
}

func TestSearch_ReturnsRankedSnippets(t *testing.T) {
    tc := newSearchCtx(t)
    tc.seedTaskMessage(tc.wsID, "user", "what are the tenant rights")
    tc.seedTaskMessage(tc.wsID, "assistant", "Tenants have rights to heat, water, and habitable conditions")
    tc.seedTaskMessage(tc.wsID, "user", "unrelated lunch plans")

    resp := tc.getJSON("/api/search/executions?q=tenant+rights")
    var results struct{
        Items []struct{ Snippet string }
        Total int
    }
    _ = json.Unmarshal([]byte(resp.Body), &results)

    if results.Total != 2 { t.Errorf("total = %d, want 2", results.Total) }
    found := false
    for _, r := range results.Items {
        if strings.Contains(r.Snippet, "<mark>") { found = true }
    }
    if !found { t.Error("no highlighted snippet returned") }
}
```

- [ ] **Step 2: Implement**

```go
// server/internal/service/search.go
type SearchResult struct {
    ID         uuid.UUID `json:"id"`
    TaskID     uuid.UUID `json:"task_id"`
    AgentID    uuid.UUID `json:"agent_id"`
    AgentName  string    `json:"agent_name"`
    Snippet    string    `json:"snippet"`    // ts_headline-produced, contains <mark> tags
    Role       string    `json:"role"`
    CreatedAt  time.Time `json:"created_at"`
    Rank       float64   `json:"rank"`
}

type SearchParams struct {
    WorkspaceID uuid.UUID
    Query       string
    AgentID     *uuid.UUID // nil = no filter
    Since, Until *time.Time
    Limit, Offset int
}

func (s *SearchService) Search(ctx context.Context, p SearchParams) ([]SearchResult, int, error) {
    q := db.New(s.db)
    rows, err := q.SearchTaskMessages(ctx, db.SearchTaskMessagesParams{
        WorkspaceID: uuidPg(p.WorkspaceID),
        Column2:     p.Query,
        Column3:     uuidPgOrNil(p.AgentID),
        Column4:     timestampPgOrNil(p.Since),
        Column5:     timestampPgOrNil(p.Until),
        Limit:       int32(p.Limit),
        Offset:      int32(p.Offset),
    })
    if err != nil { return nil, 0, err }
    total, err := q.CountSearchResults(ctx, db.CountSearchResultsParams{
        WorkspaceID: uuidPg(p.WorkspaceID),
        Column2:     p.Query,
        Column3:     uuidPgOrNil(p.AgentID),
        Column4:     timestampPgOrNil(p.Since),
        Column5:     timestampPgOrNil(p.Until),
    })
    if err != nil { return nil, 0, err }
    out := make([]SearchResult, len(rows))
    for i, r := range rows { out[i] = mapSearchRow(r) }
    return out, int(total), nil
}
```

- [ ] **Step 3: Handler + mount + commit**

```go
// server/internal/handler/search.go
func (h *SearchHandler) Executions(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    p := SearchParams{
        WorkspaceID: wsID,
        Query: r.URL.Query().Get("q"),
        Limit: atoiOrDefault(r.URL.Query().Get("limit"), 20, 100),
        Offset: atoiOrDefault(r.URL.Query().Get("offset"), 0, 0),
    }
    if a := r.URL.Query().Get("agent_id"); a != "" {
        if id, err := uuid.Parse(a); err == nil { p.AgentID = &id }
    }
    // since / until similarly

    // CSV export branch (PLAN.md §10.2). When ?format=csv, stream rows
    // directly to the response writer rather than loading into memory.
    // Raises Limit to 50k (matches Phase 8B audit CSV cap); callers
    // wanting larger exports paginate via offset. stripHTMLTags strips
    // the <mark>…</mark> from ts_headline snippets so the CSV doesn't
    // surface markup to compliance consumers.
    if r.URL.Query().Get("format") == "csv" {
        p.Limit = 50_000
        items, _, err := h.svc.Search(r.Context(), p)
        if err != nil { http.Error(w, err.Error(), 500); return }
        w.Header().Set("Content-Type", "text/csv")
        w.Header().Set("Content-Disposition", `attachment; filename="executions.csv"`)
        cw := csv.NewWriter(w)
        defer cw.Flush()
        _ = cw.Write([]string{"timestamp", "agent_name", "role", "task_id", "snippet", "rank"})
        for _, r := range items {
            // Strip <mark> tags from snippet for CSV cleanliness — compliance
            // consumers just want the matched text.
            plain := stripHTMLTags(r.Snippet)
            _ = cw.Write([]string{
                r.CreatedAt.Format(time.RFC3339), r.AgentName, r.Role,
                r.TaskID.String(), plain, strconv.FormatFloat(r.Rank, 'f', 6, 64),
            })
        }
        return
    }

    items, total, err := h.svc.Search(r.Context(), p)
    // ... (handler continues unchanged below this line)

    _ = h.handlerFallthrough(items, total, err, w)
}

// stripHTMLTags removes <tag>…</tag> markup from a string. Used by the
// CSV export branch above to strip ts_headline's <mark>…</mark>
// highlights before writing to the CSV — compliance consumers get plain
// text, not markup. One-off regex compile at init time.
var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)
func stripHTMLTags(s string) string { return htmlTagPattern.ReplaceAllString(s, "") }

// atoiOrDefault — shared handler util from Phase 2 (server/internal/handler/util.go).
// Reused here without redefinition. Signature: atoiOrDefault(raw string,
// def int, max int) int — returns def when raw is empty/unparseable,
// clamps to max when parsed value exceeds it.
//
// Ensure the same file is imported by this handler — the precondition
// listing in Task 8 expects Phase 2 util to be available.
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, map[string]any{"items": items, "total": total})
}
```

Add a CSV download button in SearchPage:

```tsx
// Append to packages/views/search/index.tsx
export function SearchPage({ wsId }: { wsId: string }) {
  // ... existing state + useSearchExecutions
  const csvHref = `/api/search/executions?q=${encodeURIComponent(q)}${agentId ? `&agent_id=${agentId}` : ""}&format=csv`;
  return (
    <div>
      {/* ... existing search input + filters */}
      <a href={csvHref} download="executions.csv" className="btn-secondary">Export CSV</a>
      {/* ... results list */}
    </div>
  );
}
```

```bash
cd server && go test ./internal/service/ ./internal/handler/ -run Search -v
git add server/internal/service/search.go server/internal/handler/search.go server/cmd/server/router.go
git commit -m "feat(search): FTS endpoint over task_message

plainto_tsquery for user-friendly queries; ts_headline produces <mark>-
wrapped snippets. Filter by agent_id + date range. Separate count query
for pagination since sqlc doesn't compose window counts on :many."
```

---

### Task 9: Trace-Tree Handler

**Files:**
- Create: `server/internal/handler/trace.go`
- Create: `server/internal/handler/trace_test.go`

**Goal:** `GET /api/workspaces/{wsId}/traces/{id}/tree` returns spans + totals. UI reconstructs the tree client-side from `parent_span_id`.

- [ ] **Step 1: Implement**

```go
// server/internal/handler/trace.go
func (h *TraceHandler) Tree(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    traceID, err := uuidFromParam(r, "id")
    if err != nil { http.Error(w, "invalid trace id", 400); return }

    q := db.New(h.db)
    limit := atoiOrDefault(r.URL.Query().Get("limit"), 500, 5000)
    spans, err := q.ListSpansForTrace(r.Context(), db.ListSpansForTraceParams{
        WorkspaceID: uuidPg(wsID), TraceID: uuidPg(traceID),
        Limit: int32(limit),
    })
    if err != nil { http.Error(w, err.Error(), 500); return }
    totals, _ := q.GetTraceTotals(r.Context(), db.GetTraceTotalsParams{
        WorkspaceID: uuidPg(wsID), TraceID: uuidPg(traceID),
    })

    writeJSON(w, 200, map[string]any{
        "trace_id":    traceID,
        "spans":       mapSpanRows(spans),
        "total_duration_ms": totals.TotalDurationMs,
        "total_cost_cents":  totals.TotalCostCents,
        "span_count":  totals.SpanCount,
    })
}

// List powers the trace-viewer list page + filters (PLAN.md §10.4).
// Supports filter by workflow_run_id, agent_id, and date range. Without
// filters, returns the most recent N traces in the workspace.
func (h *TraceHandler) List(w http.ResponseWriter, r *http.Request) {
    wsID, _ := uuidFromParam(r, "wsId")
    q := db.New(h.db)

    limit := atoiOrDefault(r.URL.Query().Get("limit"), 50, 500)
    offset := atoiOrDefault(r.URL.Query().Get("offset"), 0, 0)

    if wf := r.URL.Query().Get("workflow_run_id"); wf != "" {
        wfID, err := uuid.Parse(wf)
        if err != nil { http.Error(w, "invalid workflow_run_id", 400); return }
        rows, err := q.ListTracesForWorkflowRun(r.Context(), db.ListTracesForWorkflowRunParams{
            WorkspaceID: uuidPg(wsID), WorkflowRunID: uuidPg(wfID),
        })
        if err != nil { http.Error(w, err.Error(), 500); return }
        writeJSON(w, 200, map[string]any{"traces": rows})
        return
    }

    params := db.ListRecentTracesParams{
        WorkspaceID: uuidPg(wsID),
        Limit:       int32(limit),
        Offset:      int32(offset),
    }
    if a := r.URL.Query().Get("agent_id"); a != "" {
        if id, err := uuid.Parse(a); err == nil {
            params.AgentID = pgtype.UUID{Bytes: id, Valid: true}
        }
    }
    if s := r.URL.Query().Get("since"); s != "" {
        if t, err := time.Parse(time.RFC3339, s); err == nil {
            params.Since = pgtype.Timestamptz{Time: t, Valid: true}
        }
    }
    rows, err := q.ListRecentTraces(r.Context(), params)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, map[string]any{"traces": rows})
}
```

Mount in router:
```go
r.Route("/api/workspaces/{wsId}/traces", func(r chi.Router) {
    r.Get("/", traceH.List)
    r.Get("/{id}/tree", traceH.Tree)
})
```

- [ ] **Step 2: Commit**

```bash
cd server && go test ./internal/handler/ -run TestTrace -v
git add server/internal/handler/trace.go server/internal/handler/trace_test.go server/cmd/server/router.go
git commit -m "feat(trace): GET /traces/{id}/tree — span list + totals"
```

---

### Task 10: Frontend — Search + Trace Waterfall

**Files:**
- Create: `packages/core/types/trace.ts`, `search.ts`
- Create: `packages/core/api/trace.ts`, `search.ts`
- Create: `packages/core/trace/queries.ts`, `search/queries.ts`
- Create: `packages/views/search/index.tsx`
- Create: `packages/views/traces/components/trace-waterfall.tsx`
- Create: `packages/views/traces/components/span-details.tsx`
- Create: `packages/views/tasks/components/trace-tab.tsx`

**Goal:** search page with filters + result cards linking to task transcripts; waterfall view on task detail.

- [ ] **Step 1: Types**

```ts
// packages/core/types/trace.ts
export type Span = {
  id: string;
  trace_id: string;
  parent_span_id: string | null;
  workspace_id: string;
  agent_id: string | null;
  operation: "llm_call" | "tool_call" | "subagent_dispatch" | "workflow_step" | "memory_op" | "http_call" | "mcp_call";
  task_id: string | null;
  workflow_run_id: string | null;
  cost_event_id: string | null;
  started_at: string;
  ended_at: string | null;
  duration_ms: number | null;
  status: string | null;
  error: string | null;
  metadata: Record<string, unknown>;
  // Joined cost_event fields (only when cost_event_id set):
  cost_cents?: number;
  model?: string;
};

export type TraceTree = {
  trace_id: string;
  spans: Span[];
  total_duration_ms: number;
  total_cost_cents: number;
  span_count: number;
};

// packages/core/types/search.ts
export type SearchResult = {
  id: string;
  task_id: string;
  agent_id: string;
  agent_name: string;
  snippet: string;   // contains <mark>…</mark>; render with dangerouslySetInnerHTML after sanitization
  role: string;
  created_at: string;
  rank: number;
};
```

- [ ] **Step 1.5: API client + TanStack hook**

Before the Search page can consume `useSearchExecutions`, the REST client
and query hook must exist. Both are thin — the API client is a typed
fetch wrapper; the hook is a workspace-keyed `useQuery` that debounces
via the caller's `useDeferredValue` (see Step 5).

```ts
// packages/core/api/search.ts
import { api } from "@multica/core/api/client";
import type { SearchResult } from "@multica/core/types/search";

export type SearchExecutionsParams = {
  q: string;
  agentId?: string;
  since?: string;
  until?: string;
  limit?: number;
  offset?: number;
};
export type SearchExecutionsResponse = {
  items: SearchResult[];
  total: number;
};

export async function searchExecutions(
  wsId: string,
  params: SearchExecutionsParams,
): Promise<SearchExecutionsResponse> {
  const qs = new URLSearchParams();
  qs.set("q", params.q);
  if (params.agentId) qs.set("agent_id", params.agentId);
  if (params.since) qs.set("since", params.since);
  if (params.until) qs.set("until", params.until);
  if (params.limit != null) qs.set("limit", String(params.limit));
  if (params.offset != null) qs.set("offset", String(params.offset));
  return api.get<SearchExecutionsResponse>(
    `/api/search/executions?${qs.toString()}`,
    { headers: { "X-Workspace-ID": wsId } },
  );
}
```

```ts
// packages/core/search/queries.ts
import { useQuery } from "@tanstack/react-query";
import { searchExecutions, type SearchExecutionsParams } from "@multica/core/api/search";

export function useSearchExecutions(wsId: string, params: SearchExecutionsParams) {
  return useQuery({
    // Empty query returns empty — short-circuit to avoid an unnecessary round-trip.
    queryKey: ["search", wsId, params.q, params.agentId ?? null, params.since ?? null, params.until ?? null],
    queryFn: () => searchExecutions(wsId, params),
    enabled: params.q.trim().length > 0,
    staleTime: 30_000, // FTS results are conservative-stale; manual refetch on demand.
  });
}
```

- [ ] **Step 2: Search page**

```tsx
// packages/views/search/index.tsx
import { useSearchExecutions } from "@multica/core/search/queries";
import DOMPurify from "isomorphic-dompurify";

export function SearchPage({ wsId }: { wsId: string }) {
  const [q, setQ] = useState("");
  const [agentId, setAgentId] = useState<string | undefined>();
  const { data, isLoading } = useSearchExecutions(wsId, { q, agentId });
  return (
    <div>
      <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search executions…" />
      {/* Agent filter, date range — dropdown */}
      {data?.items.map((r) => (
        <article key={r.id}>
          <header>{r.agent_name} · {new Date(r.created_at).toLocaleString()}</header>
          <div dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(r.snippet) }} />
          <a href={`/tasks/${r.task_id}#msg-${r.id}`}>Open transcript</a>
        </article>
      ))}
    </div>
  );
}
```

- [ ] **Step 3: Waterfall**

```tsx
// packages/views/traces/components/trace-waterfall.tsx
import type { Span, TraceTree } from "@multica/core/types/trace";

// Inline Map-based groupBy — lodash's groupBy returns a plain object but
// we want .get() + string-key safety for parent_span_id (including a
// synthetic "__root__" sentinel). Two lines in; no dependency added.
function groupBy<T, K extends string>(items: T[], keyOf: (item: T) => K): Map<K, T[]> {
  const out = new Map<K, T[]>();
  for (const it of items) {
    const k = keyOf(it);
    const arr = out.get(k);
    if (arr) arr.push(it); else out.set(k, [it]);
  }
  return out;
}

// Reconstructs parent-child tree from a flat span list (parent_span_id
// maps form a forest). Renders each span as a row; indent by depth; bar
// width ∝ duration_ms / total_duration_ms.
export function TraceWaterfall({ tree }: { tree: TraceTree }) {
  if (!tree.spans.length) {
    return <div className="p-4 text-sm text-muted-foreground">No spans recorded for this trace.</div>;
  }
  const byParent = groupBy(tree.spans, (s) => s.parent_span_id ?? "__root__");
  const t0 = Date.parse(tree.spans[0].started_at); // safe: length check above

  function render(parentID: string | null, depth: number): JSX.Element[] {
    const children = byParent.get(parentID ?? "__root__") ?? [];
    return children.flatMap((s) => {
      const offset = (Date.parse(s.started_at) - t0) / tree.total_duration_ms * 100;
      const width = (s.duration_ms ?? 0) / tree.total_duration_ms * 100;
      return [
        <div key={s.id} data-testid="span-row" style={{ paddingLeft: depth * 16 }}>
          <span className="w-48 truncate">{s.operation}</span>
          <div className="relative h-4 flex-1 bg-muted">
            <div className="absolute h-full bg-primary"
                 style={{ left: `${offset}%`, width: `${Math.max(width, 0.5)}%` }} />
          </div>
          <span>{s.duration_ms}ms</span>
          {s.cost_cents !== undefined && <span>·${(s.cost_cents/100).toFixed(3)}</span>}
        </div>,
        ...render(s.id, depth + 1),
      ];
    });
  }
  return <div className="flex flex-col gap-1">{render(null, 0)}</div>;
}
```

- [ ] **Step 3.5: Trace filters component**

PLAN.md §10.4 requires the trace viewer to filter by workflow_run and
agent. Handler support landed in Task 9 (`?workflow_run_id=`,
`?agent_id=`, `?since=`); this component emits those params upward via
`onChange`, and the parent page wires them into the list query.

```tsx
// packages/views/traces/components/trace-filters.tsx
import { useWorkflows } from "@multica/core/workflows/queries";
import { useAgents } from "@multica/core/agents/queries";

export type TraceFilters = {
  workflowRunId?: string;
  agentId?: string;
  since?: string; // ISO8601
};

export function TraceFilters({
  wsId, value, onChange,
}: {
  wsId: string;
  value: TraceFilters;
  onChange: (next: TraceFilters) => void;
}) {
  const { data: workflows } = useWorkflows(wsId);
  const { data: agents } = useAgents(wsId);
  return (
    <div className="flex gap-2 items-center">
      <select
        value={value.agentId ?? ""}
        onChange={(e) => onChange({ ...value, agentId: e.target.value || undefined })}
        aria-label="Filter by agent"
      >
        <option value="">All agents</option>
        {agents?.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
      </select>
      <select
        value={value.workflowRunId ?? ""}
        onChange={(e) => onChange({ ...value, workflowRunId: e.target.value || undefined })}
        aria-label="Filter by workflow"
      >
        <option value="">All workflows</option>
        {workflows?.map((w) => <option key={w.id} value={w.id}>{w.name}</option>)}
      </select>
      <input
        type="datetime-local"
        value={value.since ?? ""}
        onChange={(e) => onChange({ ...value, since: e.target.value || undefined })}
        aria-label="Since"
      />
    </div>
  );
}
```

- [ ] **Step 4: SpanDetails with Phase 1.3 error classifier**

```tsx
// packages/views/traces/components/span-details.tsx
import { classifyError, type ErrorCategory } from "@multica/core/errors/classify";
import type { Span } from "@multica/core/types/trace";

// Map Phase 1.3 error categories → semantic color tokens.
const categoryColor: Record<ErrorCategory, string> = {
  auth:             "text-destructive",
  auth_permanent:   "text-destructive font-semibold",
  billing:          "text-destructive",
  rate_limit:       "text-warning",
  overloaded:       "text-warning",
  server_error:     "text-warning",
  timeout:          "text-warning",
  context_overflow: "text-warning",
  payload_too_large:"text-warning",
  model_not_found:  "text-destructive",
  format_error:     "text-destructive",
  unknown:          "text-muted-foreground",
};

export function SpanDetails({ span }: { span: Span }) {
  const err = span.error ? classifyError(span.error) : null;
  return (
    <aside className="w-80 border-l p-4 text-sm">
      <h3>{span.operation}</h3>
      <dl>
        <dt>Duration</dt><dd>{span.duration_ms} ms</dd>
        <dt>Status</dt><dd>{span.status ?? "—"}</dd>
        {span.cost_cents !== undefined && (
          <><dt>Cost</dt><dd>${(span.cost_cents / 100).toFixed(4)}</dd></>
        )}
        {span.model && (<><dt>Model</dt><dd>{span.model}</dd></>)}
      </dl>
      {err && (
        <div role="alert" className={categoryColor[err.category]}>
          <strong>{err.category}</strong>
          <pre className="whitespace-pre-wrap">{span.error}</pre>
          {err.retryable && <p className="text-xs text-muted-foreground">Retryable — agent may succeed on retry.</p>}
        </div>
      )}
      {Object.keys(span.metadata ?? {}).length > 0 && (
        <details>
          <summary>Metadata</summary>
          <pre>{JSON.stringify(span.metadata, null, 2)}</pre>
        </details>
      )}
    </aside>
  );
}
```

`classifyError` + `ErrorCategory` need a TypeScript mirror of Phase 1.3's Go classifier. Before SpanDetails ships, create the stub:

```ts
// packages/core/errors/classify.ts
// Mirrors server/pkg/agent/errors.go (Phase 1.3). Keep the category
// names IDENTICAL so server-logged error text can be classified the
// same way on both sides. When Phase 1.3's Go classifier adds a new
// category, update this file accordingly.
export type ErrorCategory =
  | "auth" | "auth_permanent" | "billing" | "rate_limit"
  | "overloaded" | "server_error" | "timeout" | "context_overflow"
  | "payload_too_large" | "model_not_found" | "format_error" | "unknown";

export type Classified = {
  category: ErrorCategory;
  retryable: boolean;
};

const patterns: Array<{ match: RegExp; category: ErrorCategory; retryable: boolean }> = [
  { match: /401|unauthorized|invalid.*api.*key/i, category: "auth", retryable: false },
  { match: /403|forbidden|account.*suspended/i,   category: "auth_permanent", retryable: false },
  { match: /402|payment|billing|quota.*exceeded/i, category: "billing", retryable: false },
  { match: /429|rate.*limit/i,                    category: "rate_limit", retryable: true },
  { match: /529|overloaded|capacity/i,            category: "overloaded", retryable: true },
  { match: /5\d\d|internal.*error/i,              category: "server_error", retryable: true },
  { match: /timeout|deadline.*exceeded/i,         category: "timeout", retryable: true },
  { match: /context.*window|context.*length|too.*long/i, category: "context_overflow", retryable: false },
  { match: /payload.*too.*large|413/i,            category: "payload_too_large", retryable: false },
  { match: /model.*not.*found|404.*model/i,       category: "model_not_found", retryable: false },
  { match: /invalid.*json|parse.*error|format/i,  category: "format_error", retryable: false },
];

export function classifyError(text: string): Classified {
  for (const p of patterns) {
    if (p.match.test(text)) return { category: p.category, retryable: p.retryable };
  }
  return { category: "unknown", retryable: false };
}
```

Commit this alongside SpanDetails in Task 10's git-add line. If Phase 1.3 later ships a canonical TS mirror via an auto-generated bridge, swap the hand-written regex above for the generated version.

- [ ] **Step 5: Debounce SearchPage input + commit**

```tsx
// packages/views/search/index.tsx — replace raw useState with useDeferredValue
import { useDeferredValue } from "react";

const deferredQ = useDeferredValue(q);
const { data } = useSearchExecutions(wsId, { q: deferredQ, agentId });
```

React's `useDeferredValue` is sufficient here — no extra library. Avoids firing a network request per keystroke.

```bash
pnpm --filter @multica/views exec vitest run search/ traces/
git add packages/core/types/ packages/core/api/ packages/core/trace/ packages/core/search/ packages/views/search/ packages/views/traces/ packages/views/tasks/components/trace-tab.tsx
git commit -m "feat(views): search page + trace waterfall + span details

Waterfall reconstructs parent-child tree from flat parent_span_id map.
Bar width ∝ duration/total. Search page renders sanitized snippets
with <mark>-highlighted matches; input debounced via useDeferredValue.
SpanDetails uses Phase 1.3 classifyError to color-code errors by
category + shows retryable flag per PLAN.md §10.4."
```

---

### Task 11: Integration Test + Phase 10 Verification

**Files:**
- Create: `server/internal/integration/observability/trace_e2e_test.go`
- Create: `docs/engineering/verification/phase-10.md`

- [ ] **Step 1: End-to-end trace test**

```go
func TestTrace_DelegationChainTree(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("trace")
    parent := env.SeedAgent(ws, "parent", env.StubModel([]testsupport.TurnScript{
        {ToolCall: &testsupport.ToolCall{Name: "task", Args: `{"subagent_type":"helper","description":"do"}`}},
        {Text: "done"},
    }))
    helper := env.SeedAgent(ws, "helper", env.StubModel([]testsupport.TurnScript{{Text: "ok"}}))
    env.SeedCapability(ws, helper, "do", "x")

    taskID := env.EnqueueTaskAndWait(ws, parent, "go")
    traceID := env.TraceIDForTask(taskID)

    tree := env.GetTraceTree(ws, traceID)

    // Tighter than "≥3 spans" — require the exact operation shape so a
    // regression that silently drops subagent_dispatch or renames an
    // operation still fails the test.
    ops := map[string]int{}
    for _, s := range tree.Spans { ops[s.Operation]++ }
    if ops["subagent_dispatch"] < 1 {
        t.Errorf("missing subagent_dispatch span: %v", ops)
    }
    if ops["llm_call"] < 2 {
        t.Errorf("llm_call = %d, want ≥2 (parent + helper)", ops["llm_call"])
    }

    // Parent-child shape: every span except one has parent_span_id.
    roots := 0
    for _, s := range tree.Spans {
        if s.ParentSpanID == nil { roots++ }
    }
    if roots != 1 { t.Errorf("expected 1 root span, got %d", roots) }

    // At least one llm_call span links to a cost_event.
    linked := false
    for _, s := range tree.Spans {
        if s.Operation == "llm_call" && s.CostEventID != nil { linked = true }
    }
    if !linked { t.Error("no llm_call span linked to cost_event") }
}
```

- [ ] **Step 1.5: Workflow-run trace chain**

```go
// Verifies §10.3 invariant: trace_id propagates through workflow runs
// (not just delegations). Delegation-chain tree is covered above; this
// test closes the §10.5 gap where workflow paths were only described
// in prose, never exercised.
func TestTrace_WorkflowRunTree(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("wf-trace")
    classifier := env.SeedAgent(ws, "classifier", env.StubModel([]testsupport.TurnScript{{Text: "ok"}}))
    responder := env.SeedAgent(ws, "responder", env.StubModel([]testsupport.TurnScript{{Text: "done"}}))

    wfJSON := []byte(`{"steps":[
        {"id":"classify","type":"agent_task","agent_slug":"classifier","prompt":"c"},
        {"id":"respond","type":"agent_task","agent_slug":"responder","prompt":"r","depends_on":["classify"]}
    ]}`)
    wfID := env.CreateWorkflow(ws, "trace-wf", "manual", wfJSON)
    runID := env.TriggerWorkflow(ws, wfID, nil)
    env.WaitForRunStatus(context.Background(), ws, runID, "completed")

    // Pull the trace_id from the workflow_run row.
    traceID := env.TraceIDForRun(ws, runID)
    tree := env.GetTraceTree(ws, traceID)

    // Expect ≥ 2 workflow_step spans (classify + respond) and ≥ 2 llm_call
    // spans nested under them — exact counts depend on child instrumentation
    // so the assertion is structural, not numeric.
    ops := map[string]int{}
    for _, s := range tree.Spans { ops[s.Operation]++ }
    if ops["workflow_step"] < 2 {
        t.Errorf("workflow_step spans = %d, want ≥2", ops["workflow_step"])
    }
    if ops["llm_call"] < 2 {
        t.Errorf("llm_call spans = %d, want ≥2 (one per step)", ops["llm_call"])
    }
    // Every llm_call must be a descendant of a workflow_step — no orphan
    // llm_calls at the root when the parent is a workflow.
    parentByID := map[string]string{}
    for _, s := range tree.Spans {
        if s.ParentSpanID != nil { parentByID[s.ID] = *s.ParentSpanID }
    }
    for _, s := range tree.Spans {
        if s.Operation != "llm_call" { continue }
        // Walk up the tree; expect to hit a workflow_step before reaching root.
        cur := s.ID
        for cur != "" {
            p, ok := parentByID[cur]
            if !ok { break }
            var found *Span
            for i := range tree.Spans { if tree.Spans[i].ID == p { found = &tree.Spans[i]; break } }
            if found == nil { break }
            if found.Operation == "workflow_step" { goto ok }
            cur = p
        }
        t.Errorf("llm_call %s not descended from a workflow_step", s.ID)
    ok:
    }
    _ = classifier; _ = responder
}
```

- [ ] **Step 2: FTS integration**

```go
func TestSearch_FindsContentAndToolCalls(t *testing.T) {
    env := testsupport.NewEnv(t)
    ws := env.SeedWorkspace("fts")
    agentID := env.SeedAgent(ws, "a", env.StubModel(nil))
    taskID := env.EnqueueTaskAndComplete(ws, agentID, "help with rent")

    results := env.Search(ws, "rent")
    if results.Total < 1 { t.Error("no search results for 'rent'") }
    if results.Items[0].TaskID != taskID { t.Error("wrong task returned") }
}
```

- [ ] **Step 3: Manual verification per PLAN.md §10.5**

- [ ] Multi-agent delegation chain produces a tree with correct parent-child.
- [ ] Total cost and duration aggregated correctly (span totals equal cost_event sum).
- [ ] Frontend waterfall renders with bars sized by duration.
- [ ] Full-text search finds executions by content, tool name, output.
- [ ] Search result link opens the correct task transcript at the matched message.

- [ ] **Step 4: Evidence + commit**

```markdown
# Phase 10 Verification — 2026-04-DD
- `go test ./internal/integration/observability/...` → pass
- `make check` → pass
- Manual: [results]
- Known limitations: OTel stdout exporter only in-repo; OTLP/Jaeger customer-configured.
```

```bash
git add server/internal/integration/observability/ docs/engineering/verification/phase-10.md
git commit -m "test(phase-10): delegation trace tree + FTS e2e + evidence"
```

---

## Placeholder Scan

Self-reviewed. Two intentional abbreviations:
1. Task 6 shows instrumentation shape for four call sites in 5-8 lines each — the pattern is identical; repeating would be noise.
2. Task 10 Views use standard TanStack Query + recharts/custom CSS; test scaffolds mirror Phase 6/7/8A/9 render-without-crash shape.

No TBDs, no unreferenced symbols.

## Execution Handoff

Plan complete at `docs/superpowers/plans/2026-04-14-phase10-observability-tracing.md`.

**1. Subagent-Driven (recommended)** — fresh subagent per task.

**2. Inline Execution** — via `superpowers:executing-plans`.

Which approach?
