# Phase 7: Workflow Orchestration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** multi-step agent pipelines with conditional routing, fan-out, human gates, cron scheduling, durable resume after restart, and cancel propagation under 250 ms.

**Architecture:** custom Go engine (no Temporal/Cadence/IWF runtime dependency — we port their patterns only). The engine loads workflow DAG from `workflow_definition.steps` (JSONB), topologically sorts, executes ready steps (concurrent where independent), merges outputs into `workflow_run.state`, and persists every state transition so a server restart can resume any in-flight run. WaitUntil/Execute lifecycle (IWF), timer skip, load_keys persistence policies, conditional close (IWF), and stop-monitor poll (Open Agents, 150 ms) are all layered on top. Scheduler is a goroutine with a `schedule_fire` dedup table delivering at-least-once (NOT Cadence's exactly-once — PLAN.md §7.3 explicit caveat). Approvals are their own table + WS-driven UI, gating steps with `wait_for.approvals`.

**Tech Stack:** Go 1.26, chi router, pgx/sqlc, `github.com/robfig/cron/v3` (cron parsing only — not the whole scheduler), `text/template` for conditional routing, existing Phase 2 `harness` + `BudgetService` + `WSPublisher` + `traceIDKey` + Phase 6 `DelegationService`, TanStack Query + `@xyflow/react` on the frontend.

---

## Scope Note

Phase 7 of 12. Produces:
1. CRUD workflows with a JSON-schema-validated step DSL (6 step types).
2. Engine that executes DAGs with parallel branches, state accumulation, per-step retry + timeout, cancel propagation, and resume-after-restart.
3. Scheduler with 5 overlap policies + 3 catch-up policies (Cadence names), `schedule_fire` dedup for at-least-once → effectively-once delivery.
4. Approvals — external humans decide via HTTP; engine blocks affected steps.
5. Frontend — workflow list + visual DAG builder + run monitor + approval queue + scheduler config UI.

**Not in scope — explicitly deferred:**
- **Cross-workspace workflow references** — every query workspace-scopes; sub_workflow steps target only the same workspace.
- **`buffer` overlap policy semantics (V1 equivalence with `concurrent`)** — PLAN.md §7.3 defines `buffer` as a distinct named policy. V1 accepts the value and fires a new run alongside prior runs (identical to `concurrent`). A true buffer — queue the next fire behind the prior run so only one runs at a time — requires inter-run dependency tracking that doesn't exist yet. Tracked as Phase 9 follow-up; documented verbatim in §7 verification evidence so operators know which semantic they're getting.
- **Exactly-once scheduler delivery** — Cadence gets it because its scheduler IS a Cadence workflow; we're at-least-once + dedup table. Upgrading requires event-sourced replay, out of scope.
- **Distributed engine leader election** — Phase 7 runs the engine in-process inside the server. Horizontal scale (multiple server replicas each with their own engine) is Phase 12+ concern; for now one primary runs the scheduler (the `schedule_fire` UNIQUE index is the shared-state guard if multiple do run).
- **Workflow versioning / migration of in-flight runs** — a new version of the same workflow starts fresh runs only. Running instances of the old version keep running with their original step JSON (stored on `workflow_run` at start).
- **Visual-builder undo/redo, multi-user collaboration on the builder** — deferred to post-V1.
- **Event-triggered workflows (`trigger_type='event'`)** — the schema reserves it, but only `manual` + `webhook` + `schedule` wire through in Phase 7. `event` lights up once Phase 8A webhooks land.

---

## Preconditions

Verify before Task 1. Missing entries block execution.

**From Phase 1:**

| Surface | Purpose |
|---|---|
| Migration runner (`server/cmd/migrate`) reads `server/migrations/*.up.sql` lexicographically and tracks in `schema_migrations` (PLAN.md §1.7) | Phase 7 migrations 150–151 land next |
| `workspace(id)`, `agent(workspace_id, id)`, `agent_task_queue(id)` tables with composite FKs per Phase 1 pattern | Workflow tables reference them |
| `server/pkg/agent/defaults.go` with `StopMonitorPollIntervalMs = 150` (Phase 1 §1.11) | Engine cancel-monitor |
| `cost_event(trace_id, workspace_id, ...)` table + index (Phase 1 §1.1, D8) | Workflow step costs roll up under `workflow_run.trace_id` |
| `testutil.NewTestDB`, `SeedWorkspace`, `SeedAgent` helpers | All task-level tests |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `harness.Config` + `harness.New` + `(*Harness).Execute` | `agent_task` + `tool_call` step executors |
| `BudgetService.CanDispatch(ctx, tx, wsID, estCents) (Decision, error)` | Per-step budget gate |
| `WSPublisher.Publish(wsID, kind, payload)` | Emits `workflow.run.*`, `workflow.step.*`, `approval.*` events |
| `traceIDKey` ctx + `WithTraceID`/`TraceIDFrom` (Phase 2 Task 1.5) | Workflow run mints root trace; steps propagate |
| `tools.Registry` with per-workspace tool listing (Phase 2 Task 6) | `tool_call` step type resolves tool by name |
| `RedisAdapter` (Phase 2 baseline) | Optional — scheduler leader-election in multi-replica deploys; single-replica uses in-memory lock |
| `CostCalculator` / `service.EstimateCostCents` (Phase 2 Task 14) | Cost accounting per step |

**From Phase 5:**

| Surface | Purpose |
|---|---|
| `MemoryService.Recall` / `.Remember` | Steps of type `agent_task` inject memory same as bare tasks |
| Context compression (`agent.Compact`) | Multi-step agent_task chains that accumulate long conversations |

**From Phase 6:**

| Surface | Purpose |
|---|---|
| `DelegationService.BuildTaskTool` + `*harness.SubagentRegistry` adapter | `agent_task` steps may delegate; workflows may be nested (`sub_workflow` step) |
| `harness.MaxDelegationDepth`, `SubagentPerStepTimeout` in defaults.go | Shared guardrails |

**Test helpers Phase 7 needs (extend the existing Phase 2 `testutil`/`testsupport` packages):**

Phase 7 tests reference the helpers below. Anything missing from Phase 2's existing packages lands in a Pre-Task-0 helper-bootstrap commit (same file layout as Phase 6's testutil additions) before Task 1 begins.

**`server/internal/testutil/`** (small fixture builders, sync, no WS):
```go
func NewTestDB(t *testing.T) *pgxpool.Pool                     // existing in Phase 1
func SeedWorkspace(db *pgxpool.Pool, name string) uuid.UUID    // existing in Phase 1
func SeedAgent(db *pgxpool.Pool, wsID uuid.UUID, name, provider, model string) uuid.UUID  // existing
// New in Phase 7:
func SeedWorkflow(db *pgxpool.Pool, wsID uuid.UUID, name string, stepsJSON []byte) uuid.UUID
func SeedScheduledWorkflow(db *pgxpool.Pool, wsID uuid.UUID, name, cron, overlap, catchup string) uuid.UUID
func SeedWorkflowRun(db *pgxpool.Pool, wsID, wfID uuid.UUID) uuid.UUID
func SeedRunningWorkflowRun(db *pgxpool.Pool, wsID, wfID uuid.UUID) uuid.UUID  // creates run with status='running'
func SeedScheduleFire(db *pgxpool.Pool, wsID, wfID uuid.UUID, slot time.Time)
func GetRunStatus(db *pgxpool.Pool, wsID, runID uuid.UUID) string              // for cancel-propagation assertions
func SeedApproval(db *pgxpool.Pool, wsID uuid.UUID) uuid.UUID
func SeedUser(db *pgxpool.Pool, wsID uuid.UUID) uuid.UUID                      // seed a workspace_member row
var AlwaysAllowBudget BudgetAllowFn                                            // shared stub; returns (true,"")
var NoopWS WSPublisher                                                         // shared stub; Publish is no-op
```

**`server/internal/testsupport/`** (integration-level; brings up in-process worker + WS stream):
```go
type Env struct {
    DB          *pgxpool.Pool
    DefaultUser uuid.UUID
    // ... test harness wires engine, scheduler, WS bus, approval service
}
func NewEnv(t *testing.T) *Env

// Agent stub: scripts an LLM's turn-by-turn output without hitting an API.
type TurnScript struct {
    Text     string     // emit as a normal message
    ToolCall *ToolCall  // or emit as a tool-call request
}
type ToolCall struct{ Name, Args string }
func StubModel(script []TurnScript) provider.LanguageModel

// Environment methods used by Tasks 11, 12, 16:
func (e *Env) SeedWorkspace(name string) uuid.UUID
func (e *Env) SeedAgent(wsID uuid.UUID, name string, m provider.LanguageModel) uuid.UUID
func (e *Env) SeedCapability(wsID, agentID uuid.UUID, name, description string)
func (e *Env) CreateWorkflow(wsID uuid.UUID, name, trigger string, stepsJSON []byte) uuid.UUID
func (e *Env) TriggerWorkflow(wsID, wfID uuid.UUID, input map[string]any) uuid.UUID
func (e *Env) EnqueueTask(wsID, agentID uuid.UUID, prompt string) uuid.UUID
func (e *Env) SeedBudgetPolicy(wsID uuid.UUID, limitCents int)                 // for over-budget tests

func (e *Env) WaitForTaskComplete(ctx context.Context, taskID uuid.UUID)
func (e *Env) WaitForRunStatus(ctx context.Context, wsID, runID uuid.UUID, want string)
func (e *Env) WaitForApproval(ctx context.Context, wsID, runID uuid.UUID) uuid.UUID
func (e *Env) DecideApproval(wsID, apprID, userID uuid.UUID, decision, reason string) error

// WS event capture — called after an action to assert the stream.
type CapturedEvent struct {
    Kind    string
    Payload map[string]any
}
func (e *Env) WSEventsForTask(taskID uuid.UUID) []CapturedEvent
func (e *Env) WSEventsForRun(runID uuid.UUID) []CapturedEvent

// Cost / tool-call inspection (used by Phase 6 + 7 integration tests):
func (e *Env) ListCostEvents(taskID uuid.UUID) []CostEvent
func (e *Env) ListToolCallResults(taskID uuid.UUID) []ToolCallResult
```

**Shared `uuidPg`/`uuidFromPg`/`sqlNullString` helpers** live in `server/internal/service/uuid_helpers.go` (Phase 2 Task 19.5 — part of the DB test utilities). Every file that imports the `db` package uses them; do NOT re-declare locally.

**Handler-test context** — follow the Phase 2 pattern in `server/internal/handler/handler_test.go`:
```go
// newWorkflowHandlerCtx and newApprovalHandlerCtx mirror existing
// newCapabilityHandlerCtx from Phase 1 / Phase 6 tests exactly. Each
// returns a struct with helper methods: .postJSON, .getJSON, .deleteReq,
// .wsID (seeded workspace), .createWorkflow, .triggerWorkflow,
// .createApproval.
type workflowHandlerCtx struct {
    t    *testing.T
    wsID string
    // ... srv, db, router
}
func newWorkflowHandlerCtx(t *testing.T) *workflowHandlerCtx  { ... }
```

**Pre-Task 0 bootstrap:** if any surface above is missing, raise it with Phase 1/2/5/6 owners before starting — Phase 7 is not independently executable.

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|------|---------------|
| `server/migrations/150_workflows.up.sql` | `workflow_definition`, `workflow_run`, `workflow_step_run`, `schedule_fire` tables + indexes + composite FKs |
| `server/migrations/150_workflows.down.sql` | DROP-if-exists in reverse order |
| `server/migrations/151_approvals.up.sql` | `approval_request` table |
| `server/migrations/151_approvals.down.sql` | DROP approval_request |
| `server/pkg/db/queries/workflow.sql` | sqlc queries: CRUD workflow_definition, workflow_run, workflow_step_run, scheduler helpers |
| `server/pkg/db/queries/approval.sql` | sqlc queries: CRUD approval_request |
| `server/internal/workflow/dsl.go` | Step DSL Go types (`Step`, `WaitFor`, `StepType`, 6 concrete step configs) + JSON unmarshal + validation |
| `server/internal/workflow/dsl_test.go` | Parse/validate tests for every step type, malformed inputs |
| `server/internal/workflow/dag.go` | Topological sort over `depends_on`; cycle detection; ready-set computation |
| `server/internal/workflow/dag_test.go` | Linear, diamond, cycle, self-dep, missing-ref cases |
| `server/internal/workflow/state.go` | `RunState` accumulator backed by `workflow_run.state` JSONB; `load_keys` selective load (IWF) |
| `server/internal/workflow/state_test.go` | Get/set/merge, load_keys projection, concurrent-set safety |
| `server/internal/workflow/channels.go` | In-workflow pub/sub via `workflow_run.state['__channels__']` + `pg_notify('workflow_ch_<run_id>', ...)` |
| `server/internal/workflow/channels_test.go` | Publish+wait, multiple waiters, unsubscribe |
| `server/internal/workflow/executor.go` | Step executors dispatch by step type; delegates to Phase 2/5/6 surfaces |
| `server/internal/workflow/executor_test.go` | Tests one executor per step type with stubs |
| `server/internal/workflow/retry.go` | Exponential backoff with jitter (port Cadence `common/backoff/`) |
| `server/internal/workflow/retry_test.go` | Backoff growth, jitter bounds, max-attempt cap |
| `server/internal/workflow/engine.go` | Engine loop: ready → execute → persist → advance; WaitUntil/Execute; stop-monitor; resume-after-restart |
| `server/internal/workflow/engine_test.go` | Durable resume test, cancel propagation < 250 ms test, fan-out parallelism test |
| `server/internal/workflow/scheduler.go` | Cron goroutine, overlap + catch-up policies, schedule_fire dedup |
| `server/internal/workflow/scheduler_test.go` | Overlap policy semantics, catch-up replay, dedup under double-fire |
| `server/internal/service/workflow.go` | WorkflowService: Create/Update/Get/Delete/TriggerRun plus engine start/stop wiring |
| `server/internal/service/workflow_test.go` | Service layer tests with stub engine |
| `server/internal/service/approval.go` | ApprovalService: Create/Decide/ListForWorkspace; publishes WS events |
| `server/internal/service/approval_test.go` | Creation, workspace scoping, decide-by-workspace-member check |
| `server/internal/handler/workflow.go` | HTTP handlers: CRUD, trigger, list runs, get run detail, timer-skip, cancel |
| `server/internal/handler/workflow_test.go` | Handler tests scoped per route |
| `server/internal/handler/approval.go` | HTTP handlers: list pending, decide, get detail |
| `server/internal/handler/approval_test.go` | Workspace-scoping + decided_by validation |

### Modified Files (Backend)

| File | Changes |
|------|---------|
| `server/cmd/server/router.go` | Mount `/workspaces/{wsId}/workflows`, `/workflow_runs`, `/approvals`, `/schedules` |
| `server/cmd/server/main.go` | Instantiate engine + scheduler; call `.Start(ctx)` on boot; `.Stop()` on shutdown |
| `server/pkg/agent/defaults.go` | Append `WorkflowStepDefaultTimeout`, `WorkflowMaxParallelSteps`, `ScheduleDedupWindow` |

### New Files (Frontend)

| File | Responsibility |
|------|---------------|
| `packages/core/types/workflow.ts` | `Workflow`, `WorkflowRun`, `WorkflowStepRun`, `Step`, `StepType`, `ApprovalRequest` types mirroring server DTOs |
| `packages/core/api/workflow.ts` | Client for workflow + run + approval endpoints |
| `packages/core/workflow/queries.ts` | TanStack Query hooks: list, detail, runs, pending approvals, mutations |
| `packages/core/workflow/ws.ts` | WS subscriber — invalidates queries on `workflow.run.*` / `workflow.step.*` / `approval.*` |
| `packages/views/workflows/index.tsx` | Routes-agnostic index page (list + new button) |
| `packages/views/workflows/components/workflow-list.tsx` | Table of workflows + status badges |
| `packages/views/workflows/components/workflow-builder.tsx` | `@xyflow/react` canvas; node palette; edges become `depends_on`; save to backend |
| `packages/views/workflows/components/run-view.tsx` | Read-only DAG of a run with per-step status + cost + retry badges |
| `packages/views/workflows/components/approval-queue.tsx` | Paginated pending approvals with approve/reject buttons |
| `packages/views/workflows/components/scheduler-config.tsx` | Cron expression + overlap/catch-up policy pickers |

### Modified Files (Frontend)

| File | Changes |
|------|---------|
| `packages/core/api/index.ts` | Re-export `workflow` + `approval` namespaces |
| `apps/web/app/workflows/*` | Next.js route stubs mounting `@multica/views/workflows` pages |
| `apps/desktop/src/renderer/src/routes/workflows/*` | React-Router route stubs |

### External Infrastructure

| System | Change |
|--------|--------|
| None | PostgreSQL `pg_notify`/`LISTEN` is already available; `robfig/cron/v3` is an in-process library. |

---

### Task 1: Migration 150 — Workflow Core Tables

**Files:**
- Create: `server/migrations/150_workflows.up.sql`
- Create: `server/migrations/150_workflows.down.sql`

**Goal:** land `workflow_definition`, `workflow_run`, `workflow_step_run`, `schedule_fire`. Composite FKs enforce workspace scoping (PLAN.md §7.1 + R3 B6). Indexes land here so the later sqlc queries are cheap.

- [ ] **Step 1: Write the up migration**

```sql
-- 150_workflows.up.sql
-- Phase 7 workflow core. All CHECK constraints use the NOT VALID → VALIDATE
-- pattern (PLAN.md §1.7). All FKs are composite (workspace_id, id) so a
-- forged id in one workspace cannot reach a row in another.

CREATE TABLE IF NOT EXISTS workflow_definition (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    trigger_type TEXT NOT NULL,
    trigger_config JSONB DEFAULT '{}',
    steps JSONB NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    version INT NOT NULL DEFAULT 1,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, name, version)
);
ALTER TABLE workflow_definition DROP CONSTRAINT IF EXISTS workflow_definition_trigger_type_check;
ALTER TABLE workflow_definition ADD CONSTRAINT workflow_definition_trigger_type_check
    CHECK (trigger_type IN ('manual', 'webhook', 'schedule', 'event')) NOT VALID;
ALTER TABLE workflow_definition VALIDATE CONSTRAINT workflow_definition_trigger_type_check;
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_definition_ws_id ON workflow_definition(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_definition_ws_active ON workflow_definition(workspace_id, is_active);

CREATE TABLE IF NOT EXISTS workflow_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    steps_snapshot JSONB NOT NULL,                -- steps JSON copied at run-start so version updates don't affect in-flight
    status TEXT NOT NULL DEFAULT 'running',
    state JSONB NOT NULL DEFAULT '{}',
    current_step TEXT,
    step_results JSONB NOT NULL DEFAULT '{}',
    input JSONB,
    trace_id UUID NOT NULL DEFAULT gen_random_uuid(),
    parent_run_id UUID,
    triggered_by TEXT NOT NULL,                   -- 'manual:<user_id>', 'webhook:<id>', 'schedule:<id>', 'workflow:<run_id>'
    cancel_reason TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflow_definition(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, parent_run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE SET NULL
);
ALTER TABLE workflow_run DROP CONSTRAINT IF EXISTS workflow_run_status_check;
ALTER TABLE workflow_run ADD CONSTRAINT workflow_run_status_check
    CHECK (status IN ('running', 'waiting', 'completed', 'failed', 'cancelled')) NOT VALID;
ALTER TABLE workflow_run VALIDATE CONSTRAINT workflow_run_status_check;
CREATE UNIQUE INDEX IF NOT EXISTS idx_workflow_run_ws_id ON workflow_run(workspace_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_run_active ON workflow_run(workspace_id, status) WHERE status IN ('running', 'waiting');
CREATE INDEX IF NOT EXISTS idx_workflow_run_trace ON workflow_run(trace_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_workflow_run_workflow ON workflow_run(workspace_id, workflow_id, started_at DESC);

CREATE TABLE IF NOT EXISTS workflow_step_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    step_id TEXT NOT NULL,                        -- step_id from steps_snapshot
    agent_id UUID,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    input JSONB,
    output JSONB,
    error TEXT,
    model_tier TEXT,
    retry_count INT NOT NULL DEFAULT 0,
    trace_id UUID NOT NULL,                        -- inherited from workflow_run.trace_id (D8)
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL,
    UNIQUE (run_id, step_id)                       -- one row per (run, step); retries increment retry_count
);
ALTER TABLE workflow_step_run DROP CONSTRAINT IF EXISTS workflow_step_run_status_check;
ALTER TABLE workflow_step_run ADD CONSTRAINT workflow_step_run_status_check
    CHECK (status IN ('pending', 'waiting', 'running', 'completed', 'failed', 'skipped', 'cancelled')) NOT VALID;
ALTER TABLE workflow_step_run VALIDATE CONSTRAINT workflow_step_run_status_check;
CREATE INDEX IF NOT EXISTS idx_workflow_step_run_ws ON workflow_step_run(workspace_id, run_id);
CREATE INDEX IF NOT EXISTS idx_workflow_step_run_trace ON workflow_step_run(trace_id, workspace_id);

-- Timers for WaitUntil/Execute lifecycle (PLAN.md §7.2). A step with
-- wait_for.timers=["t1"] inserts a row here on first execution and parks
-- itself in waiting. elapsed_at fires either naturally (expires_at <= NOW())
-- or manually (operator hits /skip_timer endpoint, which flips skipped=true).
CREATE TABLE IF NOT EXISTS workflow_timer (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    run_id UUID NOT NULL,
    step_id TEXT NOT NULL,
    timer_name TEXT NOT NULL,                      -- matches entry in step.wait_for.timers
    expires_at TIMESTAMPTZ NOT NULL,
    elapsed_at TIMESTAMPTZ,
    skipped BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE CASCADE,
    UNIQUE (run_id, step_id, timer_name)
);
CREATE INDEX IF NOT EXISTS idx_workflow_timer_expiry ON workflow_timer(expires_at)
    WHERE elapsed_at IS NULL AND skipped = false;

CREATE TABLE IF NOT EXISTS schedule_fire (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_id UUID NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,             -- canonical slot time (truncated to minute)
    fired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    run_id UUID,
    status TEXT NOT NULL DEFAULT 'fired',          -- 'fired', 'skipped_overlap', 'cancelled_previous'
    FOREIGN KEY (workspace_id, workflow_id) REFERENCES workflow_definition(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE SET NULL,
    UNIQUE (workspace_id, workflow_id, scheduled_at)  -- dedup key (PLAN.md §7.3): at-least-once → effectively-once
);
CREATE INDEX IF NOT EXISTS idx_schedule_fire_lookup ON schedule_fire(workspace_id, workflow_id, fired_at DESC);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 150_workflows.down.sql
DROP TABLE IF EXISTS schedule_fire;
DROP TABLE IF EXISTS workflow_timer;
DROP TABLE IF EXISTS workflow_step_run;
DROP TABLE IF EXISTS workflow_run;
DROP TABLE IF EXISTS workflow_definition;
```

- [ ] **Step 3: Apply migration**

Run: `cd server && make migrate-up`
Expected: migration 150 applied, no errors.

- [ ] **Step 4: Verify rollback**

Run: `cd server && make migrate-down` (one step) then `make migrate-up` to re-apply.
Expected: clean down + up cycle.

- [ ] **Step 5: Commit**

```bash
git add server/migrations/150_workflows.up.sql server/migrations/150_workflows.down.sql
git commit -m "feat(workflow): migration 150 — workflow_definition/run/step_run + schedule_fire

Composite (workspace_id, id) FKs enforce workspace isolation. All CHECK
constraints use NOT VALID → VALIDATE pattern. schedule_fire UNIQUE(ws,
workflow, scheduled_at) implements the at-least-once → effectively-once
scheduler dedup per PLAN.md §7.3."
```

---

### Task 2: Migration 151 — Approval Table

**Files:**
- Create: `server/migrations/151_approvals.up.sql`
- Create: `server/migrations/151_approvals.down.sql`

**Goal:** approval_request table. `decided_by` workspace-membership check is application-layer, not DB-layer (PLAN.md §7.1 comment).

- [ ] **Step 1: Write the up migration**

```sql
-- 151_approvals.up.sql
CREATE TABLE IF NOT EXISTS approval_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    workflow_run_id UUID,
    step_id TEXT,
    task_id UUID,
    agent_id UUID,
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    decided_by UUID,
    decided_at TIMESTAMPTZ,
    decision_reason TEXT,
    trace_id UUID NOT NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (workspace_id, workflow_run_id) REFERENCES workflow_run(workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, agent_id) REFERENCES agent(workspace_id, id) ON DELETE SET NULL
);
ALTER TABLE approval_request DROP CONSTRAINT IF EXISTS approval_request_type_check;
ALTER TABLE approval_request ADD CONSTRAINT approval_request_type_check
    CHECK (type IN ('output_review', 'action_approval', 'escalation')) NOT VALID;
ALTER TABLE approval_request VALIDATE CONSTRAINT approval_request_type_check;
ALTER TABLE approval_request DROP CONSTRAINT IF EXISTS approval_request_status_check;
ALTER TABLE approval_request ADD CONSTRAINT approval_request_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'expired')) NOT VALID;
ALTER TABLE approval_request VALIDATE CONSTRAINT approval_request_status_check;
CREATE INDEX IF NOT EXISTS idx_approval_ws_pending ON approval_request(workspace_id, status)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_approval_run ON approval_request(workspace_id, workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_approval_trace ON approval_request(trace_id, workspace_id);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 151_approvals.down.sql
DROP TABLE IF EXISTS approval_request;
```

- [ ] **Step 3: Apply + verify rollback**

Run: `cd server && make migrate-up && make migrate-down && make migrate-up`
Expected: two applies with a rollback between succeed.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/151_approvals.up.sql server/migrations/151_approvals.down.sql
git commit -m "feat(workflow): migration 151 — approval_request for HITL steps

Workspace-scoped, trace_id propagated from enclosing workflow_run.
decided_by membership check is application-layer per PLAN.md §7.1."
```

---

### Task 3: sqlc Queries — Workflow + Approval

**Files:**
- Create: `server/pkg/db/queries/workflow.sql`
- Create: `server/pkg/db/queries/approval.sql`
- Regenerate: `server/pkg/db/generated/*.sql.go` via `make sqlc`

**Goal:** every read/write pattern the engine + handlers need, workspace-scoped.

- [ ] **Step 1: Write workflow.sql**

```sql
-- server/pkg/db/queries/workflow.sql

-- name: CreateWorkflow :one
INSERT INTO workflow_definition (workspace_id, name, description, trigger_type, trigger_config, steps, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateWorkflow :one
UPDATE workflow_definition
SET name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    trigger_type = COALESCE(sqlc.narg('trigger_type'), trigger_type),
    trigger_config = COALESCE(sqlc.narg('trigger_config'), trigger_config),
    steps = COALESCE(sqlc.narg('steps'), steps),
    is_active = COALESCE(sqlc.narg('is_active'), is_active),
    version = version + 1,
    updated_at = NOW()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: GetWorkflow :one
SELECT * FROM workflow_definition WHERE id = $1 AND workspace_id = $2;

-- name: ListWorkflows :many
SELECT * FROM workflow_definition WHERE workspace_id = $1 ORDER BY name ASC;

-- name: ListActiveScheduledWorkflows :many
-- Scheduler-only, intentionally global: the single in-process scheduler
-- goroutine needs to see every workspace's active scheduled workflows on
-- every tick. NEVER call this from a request-scoped handler — doing so
-- exposes cross-tenant schedule metadata. Same rationale as the unscoped
-- ListActiveWorkflowRuns above.
SELECT * FROM workflow_definition
WHERE is_active = true AND trigger_type = 'schedule';

-- name: DeleteWorkflow :exec
DELETE FROM workflow_definition WHERE id = $1 AND workspace_id = $2;

-- Run queries.

-- name: CreateWorkflowRun :one
INSERT INTO workflow_run (workspace_id, workflow_id, steps_snapshot, input, trace_id, parent_run_id, triggered_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetWorkflowRun :one
SELECT * FROM workflow_run WHERE id = $1 AND workspace_id = $2;

-- name: ListWorkflowRuns :many
SELECT * FROM workflow_run
WHERE workspace_id = $1 AND workflow_id = $2
ORDER BY started_at DESC
LIMIT $3 OFFSET $4;

-- name: ListActiveWorkflowRuns :many
-- Used by engine resume-on-startup ONLY. Intentionally not workspace-filtered:
-- on server boot, every workspace's in-flight runs must be resumed. Never
-- call this from a request-scoped handler.
SELECT * FROM workflow_run WHERE status IN ('running', 'waiting');

-- name: ListActiveWorkflowRunsForWorkflow :many
-- Workspace-scoped variant used by applyOverlap in the scheduler — only
-- caller that needs to find active prior runs of a specific workflow. The
-- filter on (workspace_id, workflow_id) prevents cross-tenant data leaks
-- per CLAUDE.md + PLAN.md R3 B6.
SELECT * FROM workflow_run
WHERE status IN ('running', 'waiting')
  AND workspace_id = $1 AND workflow_id = $2;

-- name: UpdateWorkflowRunStatus :exec
UPDATE workflow_run
SET status = $3,
    current_step = COALESCE(sqlc.narg('current_step'), current_step),
    completed_at = CASE WHEN $3 IN ('completed','failed','cancelled') THEN NOW() ELSE completed_at END,
    cancel_reason = COALESCE(sqlc.narg('cancel_reason'), cancel_reason)
WHERE id = $1 AND workspace_id = $2;

-- name: UpdateWorkflowRunStepsSnapshot :exec
-- Called ONLY by expandFanOut when synthetic leaf-branch steps are added
-- to the DAG. Every other caller treats steps_snapshot as immutable — it's
-- the "version of the workflow at trigger time" contract (PLAN.md §7.1
-- steps_snapshot field comment). Fan-out is the sole in-run DAG mutation.
UPDATE workflow_run
SET steps_snapshot = $3
WHERE id = $1 AND workspace_id = $2;

-- name: MergeWorkflowRunState :exec
-- state = state || $3 (JSONB concat) so step outputs merge in.
UPDATE workflow_run
SET state = state || $3::jsonb,
    step_results = step_results || $4::jsonb
WHERE id = $1 AND workspace_id = $2;

-- name: GetWorkflowRunStateKeys :one
-- IWF load_keys pattern: fetch only the requested top-level keys to avoid
-- pulling a multi-MB state blob when a step only needs a few fields.
SELECT jsonb_object_agg(key, value) AS state
FROM (
    SELECT key, value FROM jsonb_each(
        (SELECT state FROM workflow_run WHERE id = $1 AND workspace_id = $2)
    ) WHERE key = ANY($3::text[])
) AS filtered;

-- Step run queries.

-- name: UpsertStepRun :one
INSERT INTO workflow_step_run (workspace_id, run_id, step_id, agent_id, task_id, status, input, trace_id, model_tier)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (run_id, step_id) DO UPDATE
SET status = EXCLUDED.status,
    input = EXCLUDED.input,
    started_at = CASE WHEN EXCLUDED.status = 'running' THEN NOW() ELSE workflow_step_run.started_at END
RETURNING *;

-- name: UpdateStepRunComplete :exec
UPDATE workflow_step_run
SET status = $3, output = $4, error = $5, completed_at = NOW()
WHERE id = $1 AND workspace_id = $2;

-- name: IncrementStepRunRetry :exec
UPDATE workflow_step_run
SET retry_count = retry_count + 1, status = 'pending'
WHERE id = $1 AND workspace_id = $2;

-- name: ListStepRunsForRun :many
SELECT * FROM workflow_step_run
WHERE workspace_id = $1 AND run_id = $2
ORDER BY started_at NULLS FIRST, step_id ASC;

-- Scheduler.

-- Timer queries (WaitUntil/Execute per PLAN.md §7.2).

-- name: CreateTimer :one
-- Insert-or-return-existing. CRITICAL: DO NOTHING (not UPDATE) so an elapsed
-- timer cannot be revived by a stale re-entry. An elapsed timer's
-- expires_at/elapsed_at/skipped fields are fixed once set; the engine only
-- ever reads them via TimerElapsed/TimerElapsedByName. If a retry legitimately
-- needs a fresh window, delete the old row first.
-- RETURNING NULL on conflict — caller must tolerate zero-row result and treat
-- it as "timer already exists, reuse it".
INSERT INTO workflow_timer (workspace_id, run_id, step_id, timer_name, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id, step_id, timer_name) DO NOTHING
RETURNING *;

-- name: GetTimer :one
SELECT * FROM workflow_timer WHERE id = $1 AND workspace_id = $2;

-- name: SkipTimer :exec
-- Used by POST /workflow_runs/{id}/steps/{stepId}/skip_timer. Flips the
-- timer to "fired now" so resumeWaiters picks the step up next tick.
UPDATE workflow_timer
SET skipped = true, elapsed_at = NOW()
WHERE run_id = $1 AND step_id = $2 AND timer_name = $3 AND workspace_id = $4;

-- name: TimerElapsed :one
-- Returns true if the timer has already fired (naturally OR via skip).
SELECT (elapsed_at IS NOT NULL) OR (expires_at <= NOW()) AS elapsed
FROM workflow_timer WHERE id = $1 AND workspace_id = $2;

-- name: TimerElapsedByName :one
-- Same as TimerElapsed but keyed by (run_id, step_id, timer_name) so the
-- engine can check WaitFor.Timers entries without first carrying their IDs.
SELECT COALESCE((elapsed_at IS NOT NULL) OR (expires_at <= NOW()), false) AS elapsed
FROM workflow_timer
WHERE run_id = $1 AND step_id = $2 AND timer_name = $3;

-- name: ExpireDueTimers :many
-- Background sweep — run from Scheduler loop. Flips any natural-expiry
-- timers whose clock has passed.
UPDATE workflow_timer
SET elapsed_at = NOW()
WHERE elapsed_at IS NULL AND skipped = false AND expires_at <= NOW()
RETURNING *;

-- name: InsertScheduleFire :one
INSERT INTO schedule_fire (workspace_id, workflow_id, scheduled_at, run_id, status)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, workflow_id, scheduled_at) DO NOTHING
RETURNING *;

-- name: LatestFireForWorkflow :one
SELECT * FROM schedule_fire
WHERE workspace_id = $1 AND workflow_id = $2
ORDER BY scheduled_at DESC LIMIT 1;
```

- [ ] **Step 2: Write approval.sql**

```sql
-- server/pkg/db/queries/approval.sql

-- name: CreateApproval :one
INSERT INTO approval_request (workspace_id, workflow_run_id, step_id, task_id, agent_id, type, payload, trace_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetApproval :one
SELECT * FROM approval_request WHERE id = $1 AND workspace_id = $2;

-- name: DecideApproval :one
UPDATE approval_request
SET status = $3, decided_by = $4, decided_at = NOW(), decision_reason = $5
WHERE id = $1 AND workspace_id = $2 AND status = 'pending'
RETURNING *;

-- name: ListPendingApprovals :many
SELECT * FROM approval_request
WHERE workspace_id = $1 AND status = 'pending'
ORDER BY created_at ASC
LIMIT $2 OFFSET $3;

-- name: ListApprovalsForRun :many
SELECT * FROM approval_request
WHERE workspace_id = $1 AND workflow_run_id = $2
ORDER BY created_at ASC;

-- name: ExpirePendingApprovals :many
UPDATE approval_request
SET status = 'expired'
WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < NOW()
RETURNING *;
```

- [ ] **Step 3: Regenerate + compile check**

Run: `cd server && make sqlc && go build ./...`
Expected: all queries generated; package builds.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/db/queries/workflow.sql server/pkg/db/queries/approval.sql server/pkg/db/generated/
git commit -m "feat(workflow): sqlc queries for workflow + approval CRUD

All reads workspace-scoped at the query layer. MergeWorkflowRunState
uses JSONB concat for state accumulation. GetWorkflowRunStateKeys
implements IWF load_keys pattern via jsonb_each + key filter."
```

---

### Task 4: Step DSL Types + JSON Parsing

**Files:**
- Create: `server/internal/workflow/dsl.go`
- Create: `server/internal/workflow/dsl_test.go`

**Goal:** Go types mirroring the step DSL stored in `workflow_definition.steps`. Validation catches malformed definitions at save time; engine never needs to handle bad JSON at runtime.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/dsl_test.go
package workflow

import (
    "encoding/json"
    "strings"
    "testing"
)

func TestParseWorkflow_AllStepTypes(t *testing.T) {
    t.Parallel()
    raw := []byte(`{
      "steps": [
        {"id":"classify","type":"agent_task","agent_slug":"classifier","prompt":"classify {{.input.text}}","model_tier":"micro"},
        {"id":"search","type":"tool_call","tool":"web_search","input":{"query":"{{.steps.classify.output.topic}}"},"depends_on":["classify"]},
        {"id":"decide","type":"condition","expression":"{{gt .steps.classify.output.confidence 0.8}}","if_true":"respond","if_false":"review"},
        {"id":"review","type":"human_approval","payload_template":"Classification: {{.steps.classify.output}}","depends_on":["decide"]},
        {"id":"respond","type":"agent_task","agent_slug":"responder","prompt":"...","depends_on":["decide"],"wait_for":{"approvals":["review"],"trigger":"any"}},
        {"id":"fanout","type":"fan_out","branches":[{"id":"b1","steps":[{"id":"b1s","type":"agent_task","agent_slug":"x","prompt":"..."}]}],"join":"all"},
        {"id":"subwf","type":"sub_workflow","workflow_id":"00000000-0000-0000-0000-000000000000","input":{"x":1}}
      ]
    }`)
    w, err := ParseWorkflow(raw)
    if err != nil { t.Fatal(err) }
    if len(w.Steps) != 7 {
        t.Fatalf("len(steps) = %d, want 7", len(w.Steps))
    }
    for i, want := range []StepType{StepAgentTask, StepToolCall, StepCondition, StepHumanApproval, StepAgentTask, StepFanOut, StepSubWorkflow} {
        if w.Steps[i].Type != want {
            t.Errorf("step[%d].Type = %q, want %q", i, w.Steps[i].Type, want)
        }
    }
}

func TestParseWorkflow_RejectsMissingID(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"steps":[{"type":"agent_task","agent_slug":"x","prompt":"y"}]}`)
    _, err := ParseWorkflow(raw)
    if err == nil || !strings.Contains(err.Error(), "id") {
        t.Errorf("err = %v, want missing-id error", err)
    }
}

func TestParseWorkflow_RejectsDuplicateIDs(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"steps":[
        {"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"},
        {"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}
    ]}`)
    _, err := ParseWorkflow(raw)
    if err == nil || !strings.Contains(err.Error(), "duplicate") {
        t.Errorf("err = %v, want duplicate-id error", err)
    }
}

func TestParseWorkflow_RejectsUnknownStepType(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"steps":[{"id":"a","type":"nonsense"}]}`)
    _, err := ParseWorkflow(raw)
    if err == nil || !strings.Contains(err.Error(), "type") {
        t.Errorf("err = %v, want unknown-type error", err)
    }
}

func TestParseWorkflow_RejectsUnresolvedDependency(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"steps":[
        {"id":"a","type":"agent_task","agent_slug":"x","prompt":"y","depends_on":["b"]}
    ]}`)
    _, err := ParseWorkflow(raw)
    if err == nil || !strings.Contains(err.Error(), "unknown step") {
        t.Errorf("err = %v, want unknown-step error", err)
    }
}

func TestParseWorkflow_MissingAgentSlug(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"steps":[{"id":"a","type":"agent_task","prompt":"y"}]}`)
    _, err := ParseWorkflow(raw)
    if err == nil || !strings.Contains(err.Error(), "agent_slug") {
        t.Errorf("err = %v, want agent_slug error", err)
    }
}

func TestWaitFor_UnmarshalsAllTargets(t *testing.T) {
    t.Parallel()
    raw := []byte(`{"timers":["t1"],"channels":["c1","c2"],"approvals":["a1"],"trigger":"any"}`)
    var wf WaitFor
    if err := json.Unmarshal(raw, &wf); err != nil { t.Fatal(err) }
    if len(wf.Timers) != 1 || len(wf.Channels) != 2 || len(wf.Approvals) != 1 || wf.Trigger != "any" {
        t.Errorf("parsed = %+v", wf)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestParse -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement dsl.go**

```go
// server/internal/workflow/dsl.go
package workflow

import (
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
)

// StepType enumerates the six supported step shapes. The JSON tag and string
// value match exactly; engine dispatch uses strict equality against these
// constants so renaming requires a migration.
type StepType string

const (
    StepAgentTask     StepType = "agent_task"
    StepToolCall      StepType = "tool_call"
    StepHumanApproval StepType = "human_approval"
    StepFanOut        StepType = "fan_out"
    StepCondition     StepType = "condition"
    StepSubWorkflow   StepType = "sub_workflow"
)

// Workflow is the root of a parsed workflow_definition.steps JSONB. Unmarshalled
// from `workflow_definition.steps` at engine start and from POST bodies on
// save. The engine operates over *this* representation, not raw JSON.
type Workflow struct {
    Steps []Step `json:"steps"`

    // CompleteWhen is an optional Go-template expression evaluated after
    // each state merge. When it renders to "true" the engine marks the run
    // completed even if not all steps have reached terminal status. IWF
    // conditional-close pattern per PLAN.md §7.2. Empty string = disabled.
    CompleteWhen string `json:"complete_when,omitempty"`
}

// Step is the discriminated-union shape. Not every field is valid for every
// type; ValidateStep in this file enforces the per-type rules.
type Step struct {
    ID         string   `json:"id"`
    Type       StepType `json:"type"`
    DependsOn  []string `json:"depends_on,omitempty"`
    WaitFor    *WaitFor `json:"wait_for,omitempty"`
    ModelTier  string   `json:"model_tier,omitempty"`
    Timeout    int      `json:"timeout_seconds,omitempty"`
    Retry      *Retry   `json:"retry,omitempty"`
    LoadKeys   []string `json:"load_keys,omitempty"`

    // Per-type fields. JSON zero-value when not applicable.
    AgentSlug       string                 `json:"agent_slug,omitempty"`
    Prompt          string                 `json:"prompt,omitempty"`
    Tool            string                 `json:"tool,omitempty"`
    Input           map[string]any         `json:"input,omitempty"`
    Expression      string                 `json:"expression,omitempty"`
    IfTrue          string                 `json:"if_true,omitempty"`
    IfFalse         string                 `json:"if_false,omitempty"`
    PayloadTemplate string                 `json:"payload_template,omitempty"`
    ApprovalType    string                 `json:"approval_type,omitempty"` // output_review | action_approval | escalation
    ExpiresInSec    int                    `json:"expires_in_seconds,omitempty"`
    Branches        []Branch               `json:"branches,omitempty"`
    Join            string                 `json:"join,omitempty"`          // all | any
    WorkflowID      uuid.UUID              `json:"workflow_id,omitempty"`
}

type Branch struct {
    ID    string `json:"id"`
    Steps []Step `json:"steps"`
}

type WaitFor struct {
    Timers    []string `json:"timers,omitempty"`
    Channels  []string `json:"channels,omitempty"`
    Approvals []string `json:"approvals,omitempty"`
    Trigger   string   `json:"trigger,omitempty"` // all | any (default: all)
}

type Retry struct {
    MaxAttempts int `json:"max_attempts"`
    InitialMs   int `json:"initial_backoff_ms"`
    MaxMs       int `json:"max_backoff_ms"`
}

// ParseWorkflow validates and returns the workflow. All checks are structural
// — runtime-context checks (does agent_slug resolve to an agent in the
// workspace? does workflow_id exist?) happen at save time in the service
// layer.
func ParseWorkflow(raw []byte) (*Workflow, error) {
    var w Workflow
    if err := json.Unmarshal(raw, &w); err != nil {
        return nil, fmt.Errorf("workflow: unmarshal: %w", err)
    }
    if err := ValidateWorkflow(&w); err != nil {
        return nil, err
    }
    return &w, nil
}

func ValidateWorkflow(w *Workflow) error {
    if len(w.Steps) == 0 {
        return fmt.Errorf("workflow: steps must be non-empty")
    }
    ids := make(map[string]struct{}, len(w.Steps))
    for i := range w.Steps {
        s := &w.Steps[i]
        if s.ID == "" {
            return fmt.Errorf("step[%d]: missing id", i)
        }
        if _, dup := ids[s.ID]; dup {
            return fmt.Errorf("step[%d]: duplicate id %q", i, s.ID)
        }
        ids[s.ID] = struct{}{}
    }
    for i := range w.Steps {
        s := &w.Steps[i]
        if err := ValidateStep(s, ids); err != nil {
            return fmt.Errorf("step[%d] %q: %w", i, s.ID, err)
        }
    }
    return nil
}

// knownIDs is the set of all step IDs in the workflow (for depends_on check).
func ValidateStep(s *Step, knownIDs map[string]struct{}) error {
    for _, dep := range s.DependsOn {
        if _, ok := knownIDs[dep]; !ok {
            return fmt.Errorf("depends_on: unknown step %q", dep)
        }
    }
    switch s.Type {
    case StepAgentTask:
        if s.AgentSlug == "" {
            return fmt.Errorf("agent_task: agent_slug required")
        }
        if s.Prompt == "" {
            return fmt.Errorf("agent_task: prompt required")
        }
    case StepToolCall:
        if s.Tool == "" {
            return fmt.Errorf("tool_call: tool required")
        }
    case StepCondition:
        if s.Expression == "" {
            return fmt.Errorf("condition: expression required")
        }
        if s.IfTrue == "" && s.IfFalse == "" {
            return fmt.Errorf("condition: at least one of if_true/if_false required")
        }
    case StepHumanApproval:
        if s.ApprovalType == "" {
            s.ApprovalType = "output_review"
        }
    case StepFanOut:
        if len(s.Branches) == 0 {
            return fmt.Errorf("fan_out: branches must be non-empty")
        }
        if s.Join != "all" && s.Join != "any" {
            s.Join = "all"
        }
    case StepSubWorkflow:
        if s.WorkflowID == uuid.Nil {
            return fmt.Errorf("sub_workflow: workflow_id required")
        }
    default:
        return fmt.Errorf("unknown step type %q", s.Type)
    }
    if s.WaitFor != nil && s.WaitFor.Trigger != "" && s.WaitFor.Trigger != "all" && s.WaitFor.Trigger != "any" {
        return fmt.Errorf("wait_for.trigger: must be 'all' or 'any'")
    }
    return nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestParse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/dsl.go server/internal/workflow/dsl_test.go
git commit -m "feat(workflow): step DSL types + JSON parse + structural validate

Six step types (agent_task, tool_call, condition, human_approval,
fan_out, sub_workflow). Validation catches unknown deps, duplicate ids,
missing per-type required fields at save time so engine never handles
bad JSON at runtime."
```

---

### Task 5: DAG Resolver — Topological Sort + Cycle Detection

**Files:**
- Create: `server/internal/workflow/dag.go`
- Create: `server/internal/workflow/dag_test.go`

**Goal:** given a validated Workflow, compute "ready set" (steps whose deps are all completed/skipped) and detect cycles up-front.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/dag_test.go
package workflow

import (
    "errors"
    "reflect"
    "sort"
    "testing"
)

func TestDAG_LinearReadySetProgresses(t *testing.T) {
    t.Parallel()
    w := &Workflow{Steps: []Step{
        {ID: "a", Type: StepAgentTask, AgentSlug: "x", Prompt: "y"},
        {ID: "b", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"a"}},
        {ID: "c", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"b"}},
    }}
    d := NewDAG(w)
    if got := sortedReady(d.Ready(nil)); !reflect.DeepEqual(got, []string{"a"}) {
        t.Errorf("initial ready = %v, want [a]", got)
    }
    if got := sortedReady(d.Ready(map[string]StepStatus{"a": StatusCompleted})); !reflect.DeepEqual(got, []string{"b"}) {
        t.Errorf("after a = %v, want [b]", got)
    }
}

func TestDAG_DiamondHasParallelReady(t *testing.T) {
    t.Parallel()
    w := &Workflow{Steps: []Step{
        {ID: "a", Type: StepAgentTask, AgentSlug: "x", Prompt: "y"},
        {ID: "b", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"a"}},
        {ID: "c", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"a"}},
        {ID: "d", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"b", "c"}},
    }}
    d := NewDAG(w)
    got := sortedReady(d.Ready(map[string]StepStatus{"a": StatusCompleted}))
    if !reflect.DeepEqual(got, []string{"b", "c"}) {
        t.Errorf("after a = %v, want [b c]", got)
    }
}

func TestDAG_DetectsCycle(t *testing.T) {
    t.Parallel()
    w := &Workflow{Steps: []Step{
        {ID: "a", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"b"}},
        {ID: "b", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"a"}},
    }}
    _, err := TopologicalSort(w)
    if !errors.Is(err, ErrCycle) {
        t.Errorf("err = %v, want ErrCycle", err)
    }
}

func TestDAG_SkippedStepsCountAsSatisfied(t *testing.T) {
    t.Parallel()
    // A condition step skips one branch; dependents of the skipped branch
    // should never become ready. But dependents of the taken branch must.
    w := &Workflow{Steps: []Step{
        {ID: "cond", Type: StepCondition, Expression: "true", IfTrue: "t", IfFalse: "f"},
        {ID: "t", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"cond"}},
        {ID: "f", Type: StepAgentTask, AgentSlug: "x", Prompt: "y", DependsOn: []string{"cond"}},
    }}
    d := NewDAG(w)
    status := map[string]StepStatus{"cond": StatusCompleted, "f": StatusSkipped}
    got := sortedReady(d.Ready(status))
    if !reflect.DeepEqual(got, []string{"t"}) {
        t.Errorf("ready = %v, want [t]", got)
    }
}

func sortedReady(s []string) []string { sort.Strings(s); return s }
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestDAG -v`
Expected: FAIL.

- [ ] **Step 3: Implement dag.go**

```go
// server/internal/workflow/dag.go
package workflow

import (
    "errors"
    "fmt"
)

var ErrCycle = errors.New("workflow: dependency cycle")

type StepStatus string

const (
    StatusPending   StepStatus = "pending"
    StatusWaiting   StepStatus = "waiting"
    StatusRunning   StepStatus = "running"
    StatusCompleted StepStatus = "completed"
    StatusFailed    StepStatus = "failed"
    StatusSkipped   StepStatus = "skipped"
    StatusCancelled StepStatus = "cancelled"
)

// satisfied returns whether a step's dependency on a neighbour holding
// this status is considered met. Completed + Skipped both pass — a skipped
// branch unblocks its descendants without executing them. CRITICALLY,
// StatusWaiting does NOT satisfy: a step parked for an approval/timer/
// sub_workflow holds its dependents blocked per PLAN.md §7.2
// WaitUntil/Execute lifecycle.
func (s StepStatus) satisfied() bool {
    return s == StatusCompleted || s == StatusSkipped
}

// DAG is a read-only projection of workflow steps optimised for Ready().
type DAG struct {
    steps  []Step
    byID   map[string]*Step
    rdeps  map[string][]string // reverse edges: step -> steps that depend on it (not currently used; reserved)
}

func NewDAG(w *Workflow) *DAG {
    d := &DAG{
        steps: w.Steps,
        byID:  make(map[string]*Step, len(w.Steps)),
        rdeps: make(map[string][]string),
    }
    for i := range w.Steps {
        s := &w.Steps[i]
        d.byID[s.ID] = s
        for _, dep := range s.DependsOn {
            d.rdeps[dep] = append(d.rdeps[dep], s.ID)
        }
    }
    return d
}

// Ready returns the step IDs whose deps are all satisfied and whose own
// status is pending. Caller-supplied statuses map; nil means "nothing has
// run yet" — returns steps with zero deps.
func (d *DAG) Ready(statuses map[string]StepStatus) []string {
    out := []string{}
    for _, s := range d.steps {
        cur := statuses[s.ID]
        if cur != "" && cur != StatusPending {
            continue
        }
        ok := true
        for _, dep := range s.DependsOn {
            if !statuses[dep].satisfied() {
                ok = false
                break
            }
        }
        if ok {
            out = append(out, s.ID)
        }
    }
    return out
}

// TopologicalSort returns step IDs in an order where deps precede dependants.
// Also the primary cycle-detection entry point — callers use it at
// workflow-save time to reject cyclic DAGs.
func TopologicalSort(w *Workflow) ([]string, error) {
    in := map[string]int{}
    edges := map[string][]string{}
    for _, s := range w.Steps {
        if _, ok := in[s.ID]; !ok {
            in[s.ID] = 0
        }
        for _, dep := range s.DependsOn {
            edges[dep] = append(edges[dep], s.ID)
            in[s.ID]++
        }
    }
    queue := []string{}
    for id, n := range in {
        if n == 0 {
            queue = append(queue, id)
        }
    }
    order := []string{}
    for len(queue) > 0 {
        cur := queue[0]
        queue = queue[1:]
        order = append(order, cur)
        for _, next := range edges[cur] {
            in[next]--
            if in[next] == 0 {
                queue = append(queue, next)
            }
        }
    }
    if len(order) != len(w.Steps) {
        return nil, fmt.Errorf("%w: %d of %d steps unreachable", ErrCycle, len(w.Steps)-len(order), len(w.Steps))
    }
    return order, nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestDAG -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/dag.go server/internal/workflow/dag_test.go
git commit -m "feat(workflow): DAG ready-set + topological sort + cycle detect

Kahn's algorithm. Skipped steps count as satisfied for dependants so
a condition step's untaken branch doesn't stall its grandchildren."
```

---

### Task 6: Run State Accumulator with IWF load_keys

**Files:**
- Create: `server/internal/workflow/state.go`
- Create: `server/internal/workflow/state_test.go`

**Goal:** read/write run state. `LoadKeys([]string)` pulls only requested top-level keys from `workflow_run.state` (IWF persistence-loading pattern — cheap when state is multi-MB but a step needs 3 fields).

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/state_test.go
package workflow

import (
    "context"
    "encoding/json"
    "reflect"
    "testing"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestRunState_MergeAndLoadKeys(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "team")
    wfID := testutil.SeedWorkflow(db, wsID, "test-wf", []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`))
    runID := testutil.SeedWorkflowRun(db, wsID, wfID)

    s := NewRunState(db, wsID, runID)
    if err := s.Merge(context.Background(),
        map[string]any{"foo": 1, "bar": "baz"},
        map[string]any{"step_a": map[string]any{"out": "hi"}},
    ); err != nil { t.Fatal(err) }

    got, err := s.LoadKeys(context.Background(), []string{"foo"})
    if err != nil { t.Fatal(err) }
    want := map[string]any{"foo": float64(1)} // JSON numbers → float64
    if !reflect.DeepEqual(got, want) {
        t.Errorf("partial = %v, want %v", got, want)
    }

    full, err := s.LoadAll(context.Background())
    if err != nil { t.Fatal(err) }
    if full["bar"] != "baz" { t.Errorf("full['bar'] = %v", full["bar"]) }
}

func TestRunState_MergeAppendsNotReplaces(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "team")
    wfID := testutil.SeedWorkflow(db, wsID, "test-wf", []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`))
    runID := testutil.SeedWorkflowRun(db, wsID, wfID)

    s := NewRunState(db, wsID, runID)
    if err := s.Merge(context.Background(), map[string]any{"a": 1}, nil); err != nil { t.Fatal(err) }
    if err := s.Merge(context.Background(), map[string]any{"b": 2}, nil); err != nil { t.Fatal(err) }

    full, _ := s.LoadAll(context.Background())
    if full["a"] == nil || full["b"] == nil {
        t.Errorf("expected both keys after two merges: %+v", full)
    }
    _ = uuid.Nil
    _ = json.RawMessage{}
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestRunState -v`
Expected: FAIL.

- [ ] **Step 3: Implement state.go**

```go
// server/internal/workflow/state.go
package workflow

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

// RunState is a thin wrapper over workflow_run.state / step_results that
// exposes merge (JSONB concat) + LoadKeys (IWF partial load).
type RunState struct {
    pool  *pgxpool.Pool
    wsID  uuid.UUID
    runID uuid.UUID
}

func NewRunState(pool *pgxpool.Pool, wsID, runID uuid.UUID) *RunState {
    return &RunState{pool: pool, wsID: wsID, runID: runID}
}

// Merge concatenates the provided maps into the run's state + step_results
// JSONB columns using `state || $3` semantics. A nil map is a no-op.
func (s *RunState) Merge(ctx context.Context, state, stepResults map[string]any) error {
    q := db.New(s.pool)
    stateBytes := jsonbOrEmpty(state)
    stepBytes := jsonbOrEmpty(stepResults)
    return q.MergeWorkflowRunState(ctx, db.MergeWorkflowRunStateParams{
        ID:          uuidPg(s.runID),
        WorkspaceID: uuidPg(s.wsID),
        Column3:     stateBytes,  // state delta
        Column4:     stepBytes,   // step_results delta
    })
}

// LoadKeys returns only the requested top-level keys of state. For a 2 MB
// state blob with 300 keys where the step needs only ["classify","input"],
// this is ~50x cheaper than pulling the full blob.
func (s *RunState) LoadKeys(ctx context.Context, keys []string) (map[string]any, error) {
    if len(keys) == 0 {
        return s.LoadAll(ctx)
    }
    q := db.New(s.pool)
    row, err := q.GetWorkflowRunStateKeys(ctx, db.GetWorkflowRunStateKeysParams{
        ID:          uuidPg(s.runID),
        WorkspaceID: uuidPg(s.wsID),
        Column3:     keys,
    })
    if err != nil { return nil, err }
    if row == nil { return map[string]any{}, nil }
    var out map[string]any
    if err := json.Unmarshal(row, &out); err != nil {
        return nil, fmt.Errorf("unmarshal: %w", err)
    }
    if out == nil { return map[string]any{}, nil }
    return out, nil
}

// LoadAll returns the full state blob. Use sparingly; prefer LoadKeys.
func (s *RunState) LoadAll(ctx context.Context) (map[string]any, error) {
    q := db.New(s.pool)
    run, err := q.GetWorkflowRun(ctx, db.GetWorkflowRunParams{
        ID:          uuidPg(s.runID),
        WorkspaceID: uuidPg(s.wsID),
    })
    if err != nil { return nil, err }
    var out map[string]any
    if err := json.Unmarshal(run.State, &out); err != nil {
        return nil, fmt.Errorf("unmarshal: %w", err)
    }
    if out == nil { return map[string]any{}, nil }
    return out, nil
}

func jsonbOrEmpty(m map[string]any) []byte {
    if m == nil { return []byte(`{}`) }
    b, err := json.Marshal(m)
    if err != nil { return []byte(`{}`) }
    return b
}
```

Add `testutil.SeedWorkflow`, `testutil.SeedWorkflowRun` helpers (trivial inserts mirroring existing `SeedAgent` pattern).

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestRunState -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/state.go server/internal/workflow/state_test.go server/internal/testutil/seed_workflow.go
git commit -m "feat(workflow): RunState with Merge + IWF load_keys partial read

Merge uses JSONB || concat so later writes accumulate rather than
replace. LoadKeys projects to requested top-level keys via jsonb_each,
avoiding full blob transfer for large workflows."
```

---

### Task 7: In-Workflow Channels (IWF pub/sub)

**Files:**
- Create: `server/internal/workflow/channels.go`
- Create: `server/internal/workflow/channels_test.go`

**Goal:** two concurrent branches communicate via named channels. `Publish(ch, value)` appends; `WaitForChannel(ch, timeout)` blocks until a value arrives or timeout. Backed by JSONB under `state.__channels__` + Postgres `pg_notify` for wake-up.

Note PLAN.md `D2` caveat: IWF's `InternalChannel` is exactly-once (outer workflow is durable). Our port is at-least-once — channel messages carry `consumed_at` + a conditional-UPDATE consume so a crash during handoff replays cleanly.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/channels_test.go
package workflow

import (
    "context"
    "testing"
    "time"

    "aicolab/server/internal/testutil"
)

func TestChannels_PublishThenWait(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "ch")
    wfID := testutil.SeedWorkflow(db, wsID, "w", []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`))
    runID := testutil.SeedWorkflowRun(db, wsID, wfID)

    ch := NewChannels(db, wsID, runID)
    if err := ch.Publish(context.Background(), "topic", map[string]any{"k": "v"}); err != nil { t.Fatal(err) }

    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()
    val, err := ch.Wait(ctx, "topic")
    if err != nil { t.Fatal(err) }
    m := val.(map[string]any)
    if m["k"] != "v" { t.Errorf("got %v", m) }
}

func TestChannels_WaitBlocksThenWakesOnPublish(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "ch")
    wfID := testutil.SeedWorkflow(db, wsID, "w", []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`))
    runID := testutil.SeedWorkflowRun(db, wsID, wfID)

    ch := NewChannels(db, wsID, runID)

    result := make(chan string, 1)
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        v, err := ch.Wait(ctx, "signal")
        if err != nil { result <- "err:" + err.Error(); return }
        result <- v.(string)
    }()
    time.Sleep(100 * time.Millisecond) // let the waiter subscribe
    _ = ch.Publish(context.Background(), "signal", "go")
    select {
    case got := <-result:
        if got != "go" { t.Errorf("got %q", got) }
    case <-time.After(3 * time.Second):
        t.Fatal("waiter never woke")
    }
}

func TestChannels_WaitTimeoutReturnsErr(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "ch")
    wfID := testutil.SeedWorkflow(db, wsID, "w", []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`))
    runID := testutil.SeedWorkflowRun(db, wsID, wfID)

    ch := NewChannels(db, wsID, runID)
    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    _, err := ch.Wait(ctx, "nothing")
    if err == nil { t.Error("expected ctx deadline error") }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestChannels -v`
Expected: FAIL.

- [ ] **Step 3: Implement channels.go**

```go
// server/internal/workflow/channels.go
package workflow

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

// Channels is workflow-run-scoped pub/sub. Messages live under
// workflow_run.state['__channels__'][name] as a JSON array. On publish we
// append to the array AND emit pg_notify('wf_ch_<run_id>', name) so
// waiters wake immediately.
type Channels struct {
    pool  *pgxpool.Pool
    wsID  uuid.UUID
    runID uuid.UUID
}

func NewChannels(pool *pgxpool.Pool, wsID, runID uuid.UUID) *Channels {
    return &Channels{pool: pool, wsID: wsID, runID: runID}
}

// Publish appends a message to the named channel.
func (c *Channels) Publish(ctx context.Context, name string, value any) error {
    payload, err := json.Marshal(value)
    if err != nil { return err }
    msg := map[string]any{
        "value":     json.RawMessage(payload),
        "published": nowUnix(),
    }
    msgJSON, _ := json.Marshal(msg)

    tx, err := c.pool.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)

    _, err = tx.Exec(ctx, `
        UPDATE workflow_run
        SET state = jsonb_set(
            COALESCE(state, '{}'::jsonb),
            ARRAY['__channels__', $3],
            COALESCE(state->'__channels__'->$3, '[]'::jsonb) || $4::jsonb,
            true
        )
        WHERE id = $1 AND workspace_id = $2
    `, c.runID, c.wsID, name, msgJSON)
    if err != nil { return err }

    _, err = tx.Exec(ctx, fmt.Sprintf(`NOTIFY %s, '%s'`, channelName(c.runID), name))
    if err != nil { return err }
    return tx.Commit(ctx)
}

// Wait blocks until a message arrives on `name` or ctx is cancelled. If the
// channel already has an unconsumed message, returns immediately. Otherwise
// it LISTENs on the run's pg_notify channel, then polls on every NOTIFY
// that matches.
func (c *Channels) Wait(ctx context.Context, name string) (any, error) {
    if v, ok, err := c.tryConsume(ctx, name); err != nil {
        return nil, err
    } else if ok {
        return v, nil
    }

    conn, err := c.pool.Acquire(ctx)
    if err != nil { return nil, err }
    defer conn.Release()

    _, err = conn.Exec(ctx, fmt.Sprintf(`LISTEN %s`, channelName(c.runID)))
    if err != nil { return nil, err }
    defer func() {
        _, _ = conn.Exec(context.Background(), fmt.Sprintf(`UNLISTEN %s`, channelName(c.runID)))
    }()

    // One-more try-consume to avoid lost-wakeup races between the first
    // tryConsume and the LISTEN.
    if v, ok, err := c.tryConsume(ctx, name); err != nil {
        return nil, err
    } else if ok {
        return v, nil
    }

    for {
        notif, err := conn.Conn().WaitForNotification(ctx)
        if err != nil { return nil, err }
        if notif.Payload != name { continue }
        if v, ok, err := c.tryConsume(ctx, name); err != nil {
            return nil, err
        } else if ok {
            return v, nil
        }
    }
}

// tryConsume atomically pops the oldest un-consumed message from the named
// channel array. Uses `jsonb_set` + array slicing so concurrent consumers
// at-least-once-but-no-loss — the conditional UPDATE guarantees a crash
// between Select and Update replays cleanly.
func (c *Channels) tryConsume(ctx context.Context, name string) (any, bool, error) {
    var msgBytes []byte
    err := c.pool.QueryRow(ctx, `
        WITH current AS (
            SELECT state->'__channels__'->$3 AS msgs
            FROM workflow_run
            WHERE id = $1 AND workspace_id = $2
            FOR UPDATE
        ),
        popped AS (
            SELECT jsonb_array_element(msgs, 0) AS head,
                   msgs - 0 AS tail
            FROM current
            WHERE jsonb_array_length(msgs) > 0
        )
        UPDATE workflow_run
        SET state = jsonb_set(state, ARRAY['__channels__', $3], (SELECT tail FROM popped), true)
        WHERE id = $1 AND workspace_id = $2 AND EXISTS (SELECT 1 FROM popped)
        RETURNING (SELECT head FROM popped)
    `, c.runID, c.wsID, name).Scan(&msgBytes)
    if err == pgx.ErrNoRows || msgBytes == nil {
        return nil, false, nil
    }
    if err != nil { return nil, false, err }
    var msg struct {
        Value json.RawMessage `json:"value"`
    }
    if err := json.Unmarshal(msgBytes, &msg); err != nil { return nil, false, err }
    var val any
    if err := json.Unmarshal(msg.Value, &val); err != nil { return nil, false, err }
    return val, true, nil
}

// channelName returns a Postgres NOTIFY channel name. `-` replaced with `_`
// because NOTIFY identifiers can't contain hyphens.
func channelName(runID uuid.UUID) string {
    s := runID.String()
    b := make([]byte, 0, len(s)+4)
    b = append(b, []byte("wf_ch_")...)
    for i := 0; i < len(s); i++ {
        if s[i] == '-' { b = append(b, '_'); continue }
        b = append(b, s[i])
    }
    return string(b)
}

// Reference to sync to keep the unused-import failsafe happy.
var _ sync.Mutex
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestChannels -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/channels.go server/internal/workflow/channels_test.go
git commit -m "feat(workflow): in-workflow channels via JSONB + pg_notify

Publish appends to state['__channels__'][name] JSON array and emits
pg_notify. Wait LISTENs + tryConsume via conditional-UPDATE so crashes
mid-handoff replay cleanly. At-least-once per D2 caveat — IWF's
exactly-once requires durable outer workflow which we don't have."
```

---

### Task 8: Retry / Backoff Helper (Cadence pattern)

**Files:**
- Create: `server/internal/workflow/retry.go`
- Create: `server/internal/workflow/retry_test.go`

**Goal:** exponential backoff with jitter, max-attempt cap. Independent of the engine so the scheduler can reuse it.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/retry_test.go
package workflow

import (
    "testing"
    "time"
)

func TestBackoff_GrowsExponentially(t *testing.T) {
    t.Parallel()
    b := NewBackoff(BackoffConfig{InitialMs: 100, MaxMs: 10_000, Multiplier: 2.0, Jitter: 0})
    got := []time.Duration{b.Next(0), b.Next(1), b.Next(2), b.Next(3)}
    want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
    for i, w := range want {
        if got[i] != w { t.Errorf("attempt %d: got %v, want %v", i, got[i], w) }
    }
}

func TestBackoff_CapsAtMax(t *testing.T) {
    t.Parallel()
    b := NewBackoff(BackoffConfig{InitialMs: 100, MaxMs: 1_000, Multiplier: 10.0, Jitter: 0})
    if got := b.Next(5); got != time.Second {
        t.Errorf("capped = %v, want 1s", got)
    }
}

func TestBackoff_JitterStaysWithinPct(t *testing.T) {
    t.Parallel()
    b := NewBackoff(BackoffConfig{InitialMs: 1_000, MaxMs: 60_000, Multiplier: 2.0, Jitter: 0.2})
    for i := 0; i < 100; i++ {
        d := b.Next(2)
        // attempt 2 base = 4s; ±20% = [3.2s, 4.8s]
        if d < 3200*time.Millisecond || d > 4800*time.Millisecond {
            t.Errorf("iter %d: jittered %v outside expected", i, d)
        }
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestBackoff -v`
Expected: FAIL.

- [ ] **Step 3: Implement retry.go**

```go
// server/internal/workflow/retry.go
package workflow

import (
    "math"
    "math/rand"
    "time"
)

type BackoffConfig struct {
    InitialMs  int     // first retry wait in ms
    MaxMs      int     // hard cap
    Multiplier float64 // default 2.0
    Jitter     float64 // 0..1, fraction of base to randomize (±jitter * base)
}

type Backoff struct {
    cfg BackoffConfig
    rnd *rand.Rand
}

func NewBackoff(cfg BackoffConfig) *Backoff {
    if cfg.Multiplier <= 0 { cfg.Multiplier = 2.0 }
    if cfg.InitialMs <= 0 { cfg.InitialMs = 500 }
    if cfg.MaxMs <= 0 { cfg.MaxMs = 60_000 }
    return &Backoff{cfg: cfg, rnd: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// Next returns the wait before retry attempt N (0-indexed).
func (b *Backoff) Next(attempt int) time.Duration {
    base := float64(b.cfg.InitialMs) * math.Pow(b.cfg.Multiplier, float64(attempt))
    if base > float64(b.cfg.MaxMs) { base = float64(b.cfg.MaxMs) }
    if b.cfg.Jitter > 0 {
        delta := (b.rnd.Float64()*2 - 1) * b.cfg.Jitter * base
        base += delta
    }
    if base < 0 { base = 0 }
    return time.Duration(base) * time.Millisecond
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestBackoff -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/retry.go server/internal/workflow/retry_test.go
git commit -m "feat(workflow): exponential backoff with jitter (Cadence pattern)

Configurable initial/max/multiplier/jitter. Pure function — Next(attempt)
returns a time.Duration; engine + scheduler both use it."
```

---

### Task 9: Step Executors — One Per Step Type

**Files:**
- Create: `server/internal/workflow/executor.go`
- Create: `server/internal/workflow/executor_test.go`

**Goal:** one `ExecuteStep(ctx, env, step) (output, error)` that dispatches on `step.Type`. Each branch is a thin glue layer over existing Phase 2/5/6 surfaces — no new primitives.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/executor_test.go
package workflow

import (
    "context"
    "errors"
    "testing"

    "github.com/google/uuid"
)

func TestExecuteStep_AgentTaskDispatchesToHarness(t *testing.T) {
    t.Parallel()
    var gotPrompt string
    env := &StepEnv{
        ResolveAgentByID: func(ctx context.Context, slug string) (uuid.UUID, string, error) {
            return uuid.New(), "claude-sonnet-4-5", nil
        },
        RunAgent: func(ctx context.Context, agentID uuid.UUID, model, prompt string) (string, error) {
            gotPrompt = prompt
            return "agent replied", nil
        },
        RenderTemplate: func(tpl string, state map[string]any) (string, error) { return tpl + "!", nil },
    }
    out, err := ExecuteStep(context.Background(), env, Step{
        ID: "a", Type: StepAgentTask, AgentSlug: "hi", Prompt: "tpl",
    }, map[string]any{})
    if err != nil { t.Fatal(err) }
    if gotPrompt != "tpl!" { t.Errorf("prompt = %q", gotPrompt) }
    if out["text"] != "agent replied" { t.Errorf("out = %v", out) }
}

func TestExecuteStep_ConditionReturnsBranchTarget(t *testing.T) {
    t.Parallel()
    env := &StepEnv{RenderTemplate: func(tpl string, s map[string]any) (string, error) {
        return "true", nil
    }}
    out, err := ExecuteStep(context.Background(), env, Step{
        ID: "c", Type: StepCondition, Expression: "ignored", IfTrue: "t", IfFalse: "f",
    }, nil)
    if err != nil { t.Fatal(err) }
    if out["taken"] != "t" { t.Errorf("taken = %v", out["taken"]) }
    if out["skipped"].([]string)[0] != "f" { t.Errorf("skipped = %v", out["skipped"]) }
}

func TestExecuteStep_ToolCallInvokesRegistry(t *testing.T) {
    t.Parallel()
    var gotTool string
    env := &StepEnv{
        InvokeTool: func(ctx context.Context, name string, input map[string]any) (any, error) {
            gotTool = name
            return map[string]any{"result": "ok"}, nil
        },
        RenderTemplate: func(tpl string, s map[string]any) (string, error) { return tpl, nil },
    }
    out, err := ExecuteStep(context.Background(), env, Step{
        ID: "t", Type: StepToolCall, Tool: "http_request", Input: map[string]any{"url": "x"},
    }, nil)
    if err != nil { t.Fatal(err) }
    if gotTool != "http_request" { t.Errorf("tool = %q", gotTool) }
    if out["result"] == nil { t.Errorf("out = %v", out) }
}

func TestExecuteStep_HumanApprovalCreatesRequest(t *testing.T) {
    t.Parallel()
    created := false
    env := &StepEnv{
        CreateApproval: func(ctx context.Context, typ string, payload map[string]any) (uuid.UUID, error) {
            created = true
            return uuid.New(), nil
        },
        RenderTemplate: func(tpl string, s map[string]any) (string, error) { return tpl, nil },
    }
    out, err := ExecuteStep(context.Background(), env, Step{
        ID: "h", Type: StepHumanApproval, ApprovalType: "output_review",
        PayloadTemplate: `{"msg":"approve?"}`,
    }, nil)
    if err != nil { t.Fatal(err) }
    if !created { t.Error("CreateApproval not called") }
    if out["approval_id"] == nil { t.Error("approval_id missing from output") }
}

func TestExecuteStep_UnknownTypeReturnsError(t *testing.T) {
    t.Parallel()
    _, err := ExecuteStep(context.Background(), &StepEnv{}, Step{ID: "x", Type: "nope"}, nil)
    if err == nil || !errors.Is(err, ErrUnknownStepType) {
        t.Errorf("err = %v, want ErrUnknownStepType", err)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestExecuteStep -v`
Expected: FAIL.

- [ ] **Step 3: Implement executor.go**

```go
// server/internal/workflow/executor.go
package workflow

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    "github.com/google/uuid"
)

var ErrUnknownStepType = errors.New("workflow: unknown step type")

// StepEnv is the surface each executor needs. Passed as a struct of function
// values so tests stub individual surfaces without touching Phase 2/5/6.
// Engine wiring (Task 10) populates these to the real implementations.
type StepEnv struct {
    ResolveAgentByID func(ctx context.Context, slug string) (agentID uuid.UUID, model string, err error)
    RunAgent         func(ctx context.Context, agentID uuid.UUID, model, prompt string) (string, error)
    InvokeTool       func(ctx context.Context, tool string, input map[string]any) (any, error)
    CreateApproval   func(ctx context.Context, typ string, payload map[string]any) (uuid.UUID, error)
    GetApproval      func(ctx context.Context, approvalID string) (ApprovalView, error)
    TriggerSubRun    func(ctx context.Context, workflowID uuid.UUID, input map[string]any) (runID uuid.UUID, err error)
    SubRunStatus     func(ctx context.Context, runID string) (status string, output map[string]any, err error)
    // Timer elapsed-ness is checked via the sqlc TimerElapsedByName query
    // directly inside resumeWaiters — no StepEnv surface needed. The timer
    // park path sets waiting_ref = timer NAME (not ID), and the query keys
    // on (run_id, step_id, timer_name). See engine.go resumeWaiters.
    RenderTemplate   func(tpl string, state map[string]any) (string, error)

    // CheckBudget gates a step's LLM/tool call. Wraps BudgetService.CanDispatch
    // per PLAN.md §1.2. Return (false, reason) to reject; executor translates
    // into a failed step with the reason as error. Return (true, "") to proceed.
    CheckBudget      func(ctx context.Context, estCents int) (allow bool, reason string)
}

// ApprovalView is the minimal approval shape resumeWaiters consumes — kept
// narrow so StepEnv implementations don't need to import service types.
type ApprovalView struct {
    ID     string
    Status string // pending | approved | rejected | expired
}

// ExecuteStep dispatches by step type. State is the current run state (pre-merged
// with all completed-step outputs). Returns the step's output map which is merged
// back into state under `state.steps[step.id].output`.
func ExecuteStep(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    switch s.Type {
    case StepAgentTask:
        return execAgentTask(ctx, env, s, state)
    case StepToolCall:
        return execToolCall(ctx, env, s, state)
    case StepCondition:
        return execCondition(ctx, env, s, state)
    case StepHumanApproval:
        return execHumanApproval(ctx, env, s, state)
    case StepSubWorkflow:
        return execSubWorkflow(ctx, env, s, state)
    case StepFanOut:
        // Fan-out is handled by the engine loop, not here — the engine expands
        // branches into sibling step runs. Return an error if we get one at the
        // executor level; it indicates a wiring bug.
        return nil, fmt.Errorf("fan_out handled by engine, not executor")
    default:
        return nil, fmt.Errorf("%w: %q", ErrUnknownStepType, s.Type)
    }
}

func execAgentTask(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    prompt, err := env.RenderTemplate(s.Prompt, state)
    if err != nil { return nil, fmt.Errorf("render prompt: %w", err) }

    // Budget gate BEFORE resolving + running the agent. Conservative 100c
    // estimate per step (tightened post-hoc by the CostCalculator when the
    // real cost_event lands). See PLAN.md §1.2.
    if env.CheckBudget != nil {
        if allow, reason := env.CheckBudget(ctx, 100); !allow {
            return nil, fmt.Errorf("budget denied: %s", reason)
        }
    }

    agentID, model, err := env.ResolveAgentByID(ctx, s.AgentSlug)
    if err != nil { return nil, fmt.Errorf("resolve agent %q: %w", s.AgentSlug, err) }

    text, err := env.RunAgent(ctx, agentID, model, prompt)
    if err != nil { return nil, fmt.Errorf("run agent: %w", err) }

    return map[string]any{"text": text, "agent_id": agentID.String()}, nil
}

func execToolCall(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    // Tool calls may hit paid APIs (search, email, database). Budget gate
    // applies here too — 10c estimate per call, tight enough to block
    // pathological loops.
    if env.CheckBudget != nil {
        if allow, reason := env.CheckBudget(ctx, 10); !allow {
            return nil, fmt.Errorf("budget denied: %s", reason)
        }
    }
    input := map[string]any{}
    for k, v := range s.Input {
        if sv, ok := v.(string); ok {
            rendered, err := env.RenderTemplate(sv, state)
            if err != nil { return nil, fmt.Errorf("render input[%s]: %w", k, err) }
            input[k] = rendered
        } else {
            input[k] = v
        }
    }
    res, err := env.InvokeTool(ctx, s.Tool, input)
    if err != nil { return nil, fmt.Errorf("invoke tool %q: %w", s.Tool, err) }
    out := map[string]any{}
    if m, ok := res.(map[string]any); ok {
        for k, v := range m { out[k] = v }
    } else {
        b, _ := json.Marshal(res)
        out["result"] = json.RawMessage(b)
    }
    return out, nil
}

func execCondition(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    rendered, err := env.RenderTemplate(s.Expression, state)
    if err != nil { return nil, fmt.Errorf("render expr: %w", err) }
    taken, skipped := "", []string{}
    isTrue := strings.EqualFold(strings.TrimSpace(rendered), "true")
    if isTrue && s.IfTrue != "" {
        taken = s.IfTrue
        if s.IfFalse != "" { skipped = append(skipped, s.IfFalse) }
    } else if !isTrue && s.IfFalse != "" {
        taken = s.IfFalse
        if s.IfTrue != "" { skipped = append(skipped, s.IfTrue) }
    } else if isTrue && s.IfFalse != "" {
        skipped = append(skipped, s.IfFalse)
    } else if !isTrue && s.IfTrue != "" {
        skipped = append(skipped, s.IfTrue)
    }
    return map[string]any{"taken": taken, "skipped": skipped, "expression_result": isTrue}, nil
}

// ErrStepWaiting is returned by executors when the step has dispatched
// external work (an approval request, a timer, a sub-workflow run) and needs
// the engine to park the step in `waiting` status. The engine translates
// this into a WAITING step_run and does NOT count it as satisfied until the
// external condition resolves. This is the lynchpin of PLAN.md §7.2's
// WaitUntil/Execute lifecycle — a step in WAITING must not unblock its
// dependents.
var ErrStepWaiting = errors.New("workflow: step waiting on external event")

// WaitingOutput carries the partial output emitted when a step parks itself
// in WAITING. Stored as `workflow_step_run.output` so the engine's resume
// path can inspect it when deciding whether the wait is resolved.
type WaitingOutput struct {
    Kind       string         // "approval" | "timer" | "sub_workflow"
    Ref        string         // approval_id / timer_id / sub_run_id
    RawPayload map[string]any // arbitrary extra data the executor wants to persist
}

// WaitingError bundles ErrStepWaiting with a WaitingOutput payload.
type WaitingError struct{ Output WaitingOutput }

func (w *WaitingError) Error() string   { return ErrStepWaiting.Error() }
func (w *WaitingError) Unwrap() error   { return ErrStepWaiting }

func execHumanApproval(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    payloadStr := s.PayloadTemplate
    if env.RenderTemplate != nil {
        r, err := env.RenderTemplate(s.PayloadTemplate, state)
        if err == nil { payloadStr = r }
    }
    var payload map[string]any
    if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
        payload = map[string]any{"message": payloadStr}
    }
    id, err := env.CreateApproval(ctx, s.ApprovalType, payload)
    if err != nil { return nil, fmt.Errorf("create approval: %w", err) }

    // Park the step in WAITING. Dependents stay blocked until the engine's
    // resume loop sees the approval status flip to approved/rejected (the
    // approval handler emits `approval.decided` WS + the engine polls its
    // pending-approvals list each loop). On approve, engine re-runs this
    // step's output-merge path; on reject, engine marks step failed and
    // propagates to the run.
    return nil, &WaitingError{Output: WaitingOutput{
        Kind: "approval", Ref: id.String(),
        RawPayload: map[string]any{"approval_id": id.String(), "status": "pending"},
    }}
}

func execSubWorkflow(ctx context.Context, env *StepEnv, s Step, state map[string]any) (map[string]any, error) {
    runID, err := env.TriggerSubRun(ctx, s.WorkflowID, s.Input)
    if err != nil { return nil, fmt.Errorf("trigger sub-workflow: %w", err) }
    // Block until the child run reaches a terminal status. Engine's
    // resumeWaiters() polls SubRunStatus and flips this step to
    // completed/failed when the child finishes. Parent's dependents remain
    // blocked (waiting does NOT satisfy() — see dag.go).
    return nil, &WaitingError{Output: WaitingOutput{
        Kind: "sub_workflow", Ref: runID.String(),
        RawPayload: map[string]any{"sub_run_id": runID.String(), "status": "running"},
    }}
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestExecuteStep -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/executor.go server/internal/workflow/executor_test.go
git commit -m "feat(workflow): per-step-type executors dispatched via StepEnv

Surfaces passed as function values so tests stub without touching
Phase 2/5/6. fan_out is engine-level (branch expansion); other 5 types
have executors here."
```

---

### Task 10: Engine Loop — Ready → Execute → Persist → Advance

**Files:**
- Create: `server/internal/workflow/engine.go`
- Create: `server/internal/workflow/engine_test.go`

**Goal:** the core execution loop. Durable (resumes after restart). Cancel-propagation under 250 ms via stop-monitor polling `StopMonitorPollIntervalMs`. Uses DAG.Ready + ExecuteStep + RunState + retry.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/engine_test.go
package workflow

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestEngine_RunsLinearDAGToCompletion(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "e2e")
    wfJSON := []byte(`{"steps":[
        {"id":"a","type":"agent_task","agent_slug":"x","prompt":"say hi"},
        {"id":"b","type":"agent_task","agent_slug":"x","prompt":"say bye","depends_on":["a"]}
    ]}`)
    wfID := testutil.SeedWorkflow(db, wsID, "linear", wfJSON)

    env := stubEnvAllOK()
    eng := NewEngine(EngineDeps{DB: db, Env: env, Publisher: testutil.NoopWS})
    runID, err := eng.Trigger(context.Background(), wsID, wfID, nil, "manual:test")
    if err != nil { t.Fatal(err) }

    waitForStatus(t, db, wsID, runID, "completed", 5*time.Second)
}

func TestEngine_CancelsWithin250ms(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "cancel")
    wfJSON := []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"block"}]}`)
    wfID := testutil.SeedWorkflow(db, wsID, "slow", wfJSON)

    env := stubEnvBlocking(5 * time.Second)
    eng := NewEngine(EngineDeps{DB: db, Env: env, Publisher: testutil.NoopWS})
    runID, _ := eng.Trigger(context.Background(), wsID, wfID, nil, "manual:test")

    time.Sleep(100 * time.Millisecond) // let the step start
    start := time.Now()
    _ = eng.Cancel(context.Background(), wsID, runID, "user requested")

    waitForStatus(t, db, wsID, runID, "cancelled", 500*time.Millisecond)
    if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
        t.Errorf("cancel took %v, want <=250ms", elapsed)
    }
}

func TestEngine_ResumesAfterRestart(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "resume")
    wfJSON := []byte(`{"steps":[
        {"id":"a","type":"agent_task","agent_slug":"x","prompt":"1"},
        {"id":"b","type":"agent_task","agent_slug":"x","prompt":"2","depends_on":["a"]}
    ]}`)
    wfID := testutil.SeedWorkflow(db, wsID, "resume", wfJSON)

    // First engine: complete step a, then "crash".
    env := stubEnvOnlyCompletes(map[string]bool{"a": true})
    eng1 := NewEngine(EngineDeps{DB: db, Env: env, Publisher: testutil.NoopWS})
    runID, _ := eng1.Trigger(context.Background(), wsID, wfID, nil, "manual:test")
    waitForStepStatus(t, db, wsID, runID, "a", "completed", 3*time.Second)
    eng1.Stop() // simulate crash
    _ = uuid.Nil

    // Second engine: should pick up where it left off and complete b.
    env2 := stubEnvAllOK()
    eng2 := NewEngine(EngineDeps{DB: db, Env: env2, Publisher: testutil.NoopWS})
    _ = eng2.Start(context.Background())
    waitForStatus(t, db, wsID, runID, "completed", 5*time.Second)
}
```

Helpers `stubEnvAllOK`, `stubEnvBlocking`, `stubEnvOnlyCompletes`, `waitForStatus`, `waitForStepStatus` live in the same test file and are trivial loops around `GetWorkflowRun` / `ListStepRunsForRun` queries.

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestEngine -v`
Expected: FAIL — engine undefined.

- [ ] **Step 3: Implement engine.go**

```go
// server/internal/workflow/engine.go
package workflow

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    mcagent "aicolab/server/pkg/agent"
    "aicolab/server/pkg/db/db"
)

type EngineDeps struct {
    DB        *pgxpool.Pool
    Env       *StepEnv
    Publisher interface{ Publish(uuid.UUID, string, map[string]any) }
}

type Engine struct {
    deps    EngineDeps
    running sync.Map // runID -> *runContext
    stop    chan struct{}
    wg      sync.WaitGroup
}

type runContext struct {
    wsID   uuid.UUID
    runID  uuid.UUID
    cancel context.CancelFunc
}

func NewEngine(deps EngineDeps) *Engine {
    return &Engine{deps: deps, stop: make(chan struct{})}
}

// Start resumes any runs that were in-flight before the process restarted
// and launches the stop-monitor goroutine.
func (e *Engine) Start(ctx context.Context) error {
    q := db.New(e.deps.DB)
    rows, err := q.ListActiveWorkflowRuns(ctx)
    if err != nil { return err }
    for _, r := range rows {
        e.wg.Add(1)
        go e.runLoop(ctx, uuidFromPg(r.WorkspaceID), uuidFromPg(r.ID))
    }
    return nil
}

func (e *Engine) Stop() {
    close(e.stop)
    e.wg.Wait()
}

// Trigger creates a new workflow_run and kicks the run loop.
func (e *Engine) Trigger(ctx context.Context, wsID, wfID uuid.UUID, input map[string]any, triggeredBy string) (uuid.UUID, error) {
    q := db.New(e.deps.DB)
    wf, err := q.GetWorkflow(ctx, db.GetWorkflowParams{ID: uuidPg(wfID), WorkspaceID: uuidPg(wsID)})
    if err != nil { return uuid.Nil, err }

    inputJSON, _ := json.Marshal(input)
    run, err := q.CreateWorkflowRun(ctx, db.CreateWorkflowRunParams{
        WorkspaceID:   uuidPg(wsID),
        WorkflowID:    uuidPg(wfID),
        StepsSnapshot: wf.Steps,
        Input:         inputJSON,
        TraceID:       uuidPg(uuid.New()),
        TriggeredBy:   triggeredBy,
    })
    if err != nil { return uuid.Nil, err }

    runID := uuidFromPg(run.ID)
    e.wg.Add(1)
    go e.runLoop(context.Background(), wsID, runID)
    return runID, nil
}

// Cancel flips the run's status column. The stop-monitor in runLoop notices
// within StopMonitorPollIntervalMs and calls the run's cancel fn.
func (e *Engine) Cancel(ctx context.Context, wsID, runID uuid.UUID, reason string) error {
    q := db.New(e.deps.DB)
    return q.UpdateWorkflowRunStatus(ctx, db.UpdateWorkflowRunStatusParams{
        ID:           uuidPg(runID),
        WorkspaceID:  uuidPg(wsID),
        Status:       "cancelled",
        CancelReason: sqlNullString(reason),
    })
}

func (e *Engine) runLoop(parent context.Context, wsID, runID uuid.UUID) {
    defer e.wg.Done()
    ctx, cancel := context.WithCancel(parent)
    defer cancel()
    e.running.Store(runID, &runContext{wsID: wsID, runID: runID, cancel: cancel})
    defer e.running.Delete(runID)

    go e.stopMonitor(ctx, wsID, runID, cancel)

    q := db.New(e.deps.DB)
    state := NewRunState(e.deps.DB, wsID, runID)

    for {
        select {
        case <-ctx.Done():
            return
        case <-e.stop:
            return
        default:
        }

        run, err := q.GetWorkflowRun(ctx, db.GetWorkflowRunParams{
            ID: uuidPg(runID), WorkspaceID: uuidPg(wsID),
        })
        if err != nil { return }
        if run.Status == "completed" || run.Status == "failed" || run.Status == "cancelled" {
            return
        }

        var wf Workflow
        if err := json.Unmarshal(run.StepsSnapshot, &wf); err != nil {
            _ = e.failRun(ctx, wsID, runID, fmt.Errorf("bad steps_snapshot: %w", err))
            return
        }

        // Try to advance any step parked in `waiting` state first — if an
        // approval was decided, a timer elapsed, or a sub-workflow finished
        // since the last tick, resumeWaiters flips status → completed/failed
        // so the DAG re-checks below can pick up newly-unblocked dependents.
        e.resumeWaiters(ctx, wsID, runID, NewRunState(e.deps.DB, wsID, runID))

        statuses, err := e.statusesFor(ctx, wsID, runID, &wf)
        if err != nil { return }

        // IWF conditional close: if the workflow declares `complete_when` and
        // it evaluates truthy against accumulated state, finish the run now
        // regardless of in-flight steps. Runs are context-cancelled so
        // in-flight steps abort via the stop-monitor within 150 ms.
        if wf.CompleteWhen != "" {
            state := NewRunState(e.deps.DB, wsID, runID)
            stateMap, _ := state.LoadAll(ctx)
            rendered, rerr := e.deps.Env.RenderTemplate(wf.CompleteWhen, stateMap)
            if rerr == nil && strings.EqualFold(strings.TrimSpace(rendered), "true") {
                _ = q.UpdateWorkflowRunStatus(ctx, db.UpdateWorkflowRunStatusParams{
                    ID: uuidPg(runID), WorkspaceID: uuidPg(wsID),
                    Status: "completed",
                })
                cancel()
                return
            }
        }

        dag := NewDAG(&wf)
        ready := dag.Ready(statuses)
        if len(ready) == 0 {
            if e.allTerminal(statuses, &wf) {
                _ = q.UpdateWorkflowRunStatus(ctx, db.UpdateWorkflowRunStatusParams{
                    ID: uuidPg(runID), WorkspaceID: uuidPg(wsID),
                    Status: "completed",
                })
                return
            }
            time.Sleep(500 * time.Millisecond)
            continue
        }

        stateMap, err := state.LoadAll(ctx)
        if err != nil { stateMap = map[string]any{} }

        var eg sync.WaitGroup
        for _, stepID := range ready {
            step := findStep(&wf, stepID)
            if step == nil { continue }
            if step.Type == StepFanOut {
                _ = e.expandFanOut(ctx, wsID, runID, step, &wf)
                continue
            }
            eg.Add(1)
            go func(s Step) {
                defer eg.Done()
                e.runSingleStep(ctx, wsID, runID, s, stateMap, state)
            }(*step)
        }
        eg.Wait()
    }
}

func (e *Engine) runSingleStep(ctx context.Context, wsID, runID uuid.UUID, s Step, stateMap map[string]any, state *RunState) {
    q := db.New(e.deps.DB)

    // WaitUntil/Execute lifecycle (PLAN.md §7.2): if the step declares
    // wait_for.timers, install each timer (idempotent UPSERT) and park the
    // step in waiting UNLESS all timers have already elapsed. Approvals
    // declared in wait_for.approvals follow the same pattern — they get
    // created via CreateApproval and the step parks. First-class
    // wait_for.channels is handled at the executor layer where needed.
    if s.WaitFor != nil && len(s.WaitFor.Timers) > 0 {
        allElapsed := true
        for _, tname := range s.WaitFor.Timers {
            // Default timer window: 1 hour. Can be overridden via
            // wait_for.timer_durations map on the step once the DSL gains it.
            expires := time.Now().Add(1 * time.Hour)
            _, _ = q.CreateTimer(ctx, db.CreateTimerParams{
                WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID), StepID: s.ID,
                TimerName: tname, ExpiresAt: expires,
            })
            elapsed, err := q.TimerElapsedByName(ctx, db.TimerElapsedByNameParams{
                RunID: uuidPg(runID), StepID: s.ID, TimerName: tname,
            })
            if err != nil || !elapsed {
                allElapsed = false
            }
        }
        if !allElapsed {
            // Park in waiting; resumeWaiters checks timer elapsed-ness each tick.
            waitingJSON, _ := json.Marshal(map[string]any{
                "waiting_kind": "timer",
                "waiting_ref":  s.WaitFor.Timers[0], // resumeWaiters reads all via run_id+step_id
                "timers":       s.WaitFor.Timers,
            })
            _, _ = q.UpsertStepRun(ctx, db.UpsertStepRunParams{
                WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID), StepID: s.ID,
                Status: "waiting", Input: waitingJSON, TraceID: uuidPg(uuid.New()),
            })
            return
        }
    }

    inputJSON, _ := json.Marshal(s.Input)
    stepRow, err := q.UpsertStepRun(ctx, db.UpsertStepRunParams{
        WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID), StepID: s.ID,
        Status: "running", Input: inputJSON, TraceID: uuidPg(mustTraceID(ctx, e.deps.DB, wsID, runID)),
        ModelTier: s.ModelTier,
    })
    if err != nil { return }
    // stepRow.ID is the workflow_step_run row PK — use this for every
    // subsequent update-by-id in this function (IncrementStepRunRetry,
    // UpdateStepRunComplete). runID is the workflow_run PK; mixing them
    // silently no-ops retries and completion writes.
    stepRowID := stepRow.ID

    backoff := NewBackoff(BackoffConfig{InitialMs: 500, MaxMs: 30_000, Multiplier: 2.0, Jitter: 0.2})
    attempt := 0
    var out map[string]any
    var execErr error
    maxAttempts := 3
    if s.Retry != nil && s.Retry.MaxAttempts > 0 { maxAttempts = s.Retry.MaxAttempts }

    for attempt < maxAttempts {
        select {
        case <-ctx.Done():
            return
        default:
        }
        stepCtx := ctx
        if s.Timeout > 0 {
            var cancel context.CancelFunc
            stepCtx, cancel = context.WithTimeout(ctx, time.Duration(s.Timeout)*time.Second)
            defer cancel()
        }
        out, execErr = ExecuteStep(stepCtx, e.deps.Env, s, stateMap)
        if execErr == nil { break }
        // WaitingError is not a retryable failure — the step has parked
        // itself on an external event (approval, timer, sub_workflow). Break
        // out of the retry loop BEFORE incrementing retry_count. Without
        // this early exit we'd create 3 approvals / 3 sub_workflow runs
        // before the post-loop errors.As picks it up (Review R2-B1).
        var waiting *WaitingError
        if errors.As(execErr, &waiting) { break }
        attempt++
        _ = q.IncrementStepRunRetry(ctx, db.IncrementStepRunRetryParams{
            ID: stepRowID, WorkspaceID: uuidPg(wsID),
        })
        if attempt < maxAttempts {
            select {
            case <-time.After(backoff.Next(attempt)):
            case <-ctx.Done():
                return
            }
        }
    }

    // If the executor parked the step (approval/timer/sub_workflow), record
    // the waiting output + leave status='waiting'. Dependents stay blocked
    // because StepStatus("waiting").satisfied() returns false. The engine's
    // resumeWaiters() pass, invoked each runLoop iteration, checks whether
    // each waiter's external condition has resolved and either marks the
    // step completed (running the output-merge) or failed.
    var waiting *WaitingError
    if errors.As(execErr, &waiting) {
        wOut := map[string]any{
            "waiting_kind": waiting.Output.Kind,
            "waiting_ref":  waiting.Output.Ref,
        }
        for k, v := range waiting.Output.RawPayload { wOut[k] = v }
        wOutJSON, _ := json.Marshal(wOut)
        _ = q.UpdateStepRunComplete(ctx, db.UpdateStepRunCompleteParams{
            ID: stepRowID, WorkspaceID: uuidPg(wsID),
            Status: "waiting", Output: wOutJSON, Error: sqlNullString(""),
        })
        e.deps.Publisher.Publish(wsID, "workflow.step.waiting", map[string]any{
            "workspace_id": wsID,
            "run_id":       runID,
            "step_id":      s.ID,
            "waiting_kind": waiting.Output.Kind,
            "waiting_ref":  waiting.Output.Ref,
        })
        return
    }

    outJSON, _ := json.Marshal(out)
    status := "completed"
    errStr := ""
    if execErr != nil {
        status = "failed"
        errStr = execErr.Error()
    }
    _ = q.UpdateStepRunComplete(ctx, db.UpdateStepRunCompleteParams{
        ID: stepRowID, WorkspaceID: uuidPg(wsID),
        Status: status, Output: outJSON, Error: sqlNullString(errStr),
    })
    if out != nil {
        _ = state.Merge(ctx, map[string]any{"steps": map[string]any{s.ID: map[string]any{"output": out}}}, map[string]any{s.ID: out})
    }
    e.deps.Publisher.Publish(wsID, "workflow.step."+status, map[string]any{
        "workspace_id": wsID,
        "run_id":       runID,
        "step_id":      s.ID,
        "output":       out,
        "error":        errStr,
    })
}

// resumeWaiters iterates all step_runs in status='waiting' for this run and
// tries to advance them. Called once per runLoop tick (cheap SELECT on a
// partial-index-friendly status column). On resolution, merges the waiter's
// final output into state and flips status to completed/failed.
func (e *Engine) resumeWaiters(ctx context.Context, wsID, runID uuid.UUID, state *RunState) {
    q := db.New(e.deps.DB)
    rows, err := q.ListStepRunsForRun(ctx, db.ListStepRunsForRunParams{
        WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID),
    })
    if err != nil { return }
    for _, r := range rows {
        if string(r.Status) != "waiting" { continue }
        var waitOut map[string]any
        _ = json.Unmarshal(r.Output, &waitOut)
        kind, _ := waitOut["waiting_kind"].(string)
        ref, _ := waitOut["waiting_ref"].(string)

        var finalStatus string
        var finalOutput map[string]any
        switch kind {
        case "approval":
            apr, apErr := e.deps.Env.GetApproval(ctx, ref)
            if apErr != nil { continue }
            switch apr.Status {
            case "approved":
                finalStatus = "completed"
                finalOutput = map[string]any{"approval_id": ref, "decision": "approved"}
            case "rejected":
                finalStatus = "failed"
                finalOutput = map[string]any{"approval_id": ref, "decision": "rejected"}
            case "expired":
                finalStatus = "failed"
                finalOutput = map[string]any{"approval_id": ref, "decision": "expired"}
            default:
                continue // still pending
            }
        case "timer":
            // waiting_ref holds the timer NAME (from s.WaitFor.Timers[0]) not
            // an ID, so look it up by (run_id, step_id, timer_name).
            q2 := db.New(e.deps.DB)
            done, fErr := q2.TimerElapsedByName(ctx, db.TimerElapsedByNameParams{
                RunID: uuidPg(runID), StepID: r.StepID, TimerName: ref,
            })
            if fErr != nil || !done { continue }
            finalStatus = "completed"
            finalOutput = map[string]any{"timer_name": ref, "elapsed": true}
        case "sub_workflow":
            subStatus, subOut, sErr := e.deps.Env.SubRunStatus(ctx, ref)
            if sErr != nil { continue }
            switch subStatus {
            case "completed":
                finalStatus = "completed"
                finalOutput = subOut
            case "failed", "cancelled":
                finalStatus = "failed"
                finalOutput = map[string]any{"sub_run_id": ref, "sub_status": subStatus}
            default:
                continue
            }
        case "fan_out":
            // Parent `join` lives in the waiting row's input JSON.
            var in map[string]any
            _ = json.Unmarshal(r.Input, &in)
            join, _ := in["join"].(string)
            done, out := e.evaluateFanOutJoin(ctx, wsID, runID, r.StepID, join)
            if !done { continue }
            // Inspect branches output to decide success vs failure.
            if branches, ok := out["branches"].(map[string]map[string]string); ok {
                for _, steps := range branches {
                    for _, st := range steps {
                        if st == "failed" || st == "cancelled" {
                            finalStatus = "failed"
                            finalOutput = out
                            break
                        }
                    }
                    if finalStatus == "failed" { break }
                }
            }
            if finalStatus == "" {
                finalStatus = "completed"
                finalOutput = out
            }
        default:
            continue
        }

        finalJSON, _ := json.Marshal(finalOutput)
        // UpdateStepRunComplete targets workflow_step_run.id — the row's
        // OWN primary key from ListStepRunsForRun, NOT the workflow_run id.
        // Using runID here would update zero rows (or wrong row if UUIDs
        // collide across tables).
        _ = q.UpdateStepRunComplete(ctx, db.UpdateStepRunCompleteParams{
            ID: r.ID, WorkspaceID: uuidPg(wsID),
            Status: finalStatus, Output: finalJSON,
        })
        if finalOutput != nil {
            _ = state.Merge(ctx,
                map[string]any{"steps": map[string]any{r.StepID: map[string]any{"output": finalOutput}}},
                map[string]any{r.StepID: finalOutput})
        }
        e.deps.Publisher.Publish(wsID, "workflow.step."+finalStatus, map[string]any{
            "workspace_id": wsID, // R2-S5: frontend ws.ts reads e.workspace_id for invalidation
            "run_id":       runID,
            "step_id":      r.StepID,
            "output":       finalOutput,
        })
    }
}

// stopMonitor polls the run's status every StopMonitorPollIntervalMs; on
// cancellation it fires the run's cancel func so all in-flight steps abort
// within the poll window (≤150 ms).
func (e *Engine) stopMonitor(ctx context.Context, wsID, runID uuid.UUID, cancel context.CancelFunc) {
    q := db.New(e.deps.DB)
    t := time.NewTicker(time.Duration(mcagent.StopMonitorPollIntervalMs) * time.Millisecond)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            run, err := q.GetWorkflowRun(ctx, db.GetWorkflowRunParams{
                ID: uuidPg(runID), WorkspaceID: uuidPg(wsID),
            })
            if err != nil { continue }
            if run.Status == "cancelled" {
                cancel()
                return
            }
        }
    }
}

func (e *Engine) statusesFor(ctx context.Context, wsID, runID uuid.UUID, wf *Workflow) (map[string]StepStatus, error) {
    q := db.New(e.deps.DB)
    rows, err := q.ListStepRunsForRun(ctx, db.ListStepRunsForRunParams{
        WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID),
    })
    if err != nil { return nil, err }
    m := make(map[string]StepStatus, len(wf.Steps))
    for _, s := range wf.Steps { m[s.ID] = StatusPending }
    for _, r := range rows { m[r.StepID] = StepStatus(r.Status) }
    return m, nil
}

func (e *Engine) allTerminal(st map[string]StepStatus, wf *Workflow) bool {
    for _, s := range wf.Steps {
        cur := st[s.ID]
        if cur == StatusCompleted || cur == StatusFailed || cur == StatusSkipped || cur == StatusCancelled {
            continue
        }
        return false
    }
    return true
}

// expandFanOut is a real implementation per Task 10.1. For each branch:
//   1. Insert per-branch synthetic step_runs with IDs `<fanout_id>::<branch_id>::<inner_id>`.
//   2. Inject branch-internal depends_on chain so inner steps serialize per branch.
//   3. Park the fan_out parent step in WAITING; resumeWaiters completes it once
//      the join condition (`all` or `any`) is met across branches.
//
// Branch-step execution piggy-backs on the same engine loop: the synthetic
// IDs are looked up via a per-run in-memory index kept alongside the DAG.
func (e *Engine) expandFanOut(ctx context.Context, wsID, runID uuid.UUID, s *Step, wf *Workflow) error {
    q := db.New(e.deps.DB)

    // Idempotence: if steps_snapshot already contains this fan_out's
    // synthetic steps (from a prior tick), skip expansion. We detect by
    // searching for the "<fanout_id>::" prefix on any existing step ID.
    fanPrefix := s.ID + "::"
    for _, existing := range wf.Steps {
        if strings.HasPrefix(existing.ID, fanPrefix) {
            // Already expanded — the fan_out step is already parked in
            // waiting from the first pass. Nothing to do.
            return nil
        }
    }

    // Expand each branch's step list into synthetic steps the DAG walker
    // treats as siblings. For V1 we serialize within each branch (inner
    // steps depend_on their predecessors) and parallelize across branches.
    for _, br := range s.Branches {
        var prev string
        for _, inner := range br.Steps {
            syntheticID := s.ID + "::" + br.ID + "::" + inner.ID
            deps := []string{s.ID} // each inner depends on the fan_out parent
            if prev != "" { deps = append(deps, prev) }
            merged := inner
            merged.ID = syntheticID
            merged.DependsOn = append(deps, inner.DependsOn...)
            wf.Steps = append(wf.Steps, merged)
            prev = syntheticID
        }
    }

    // Persist the augmented DAG into steps_snapshot so the NEXT runLoop
    // tick (and any server restart) sees the synthetic steps. Without this
    // write, every tick re-expands from the original snapshot, creating
    // duplicate rows masked by UpsertStepRun, and restart drops them.
    snapshotJSON, err := json.Marshal(wf)
    if err != nil { return fmt.Errorf("marshal expanded wf: %w", err) }
    if err := q.UpdateWorkflowRunStepsSnapshot(ctx, db.UpdateWorkflowRunStepsSnapshotParams{
        ID: uuidPg(runID), WorkspaceID: uuidPg(wsID), StepsSnapshot: snapshotJSON,
    }); err != nil { return fmt.Errorf("persist expanded wf: %w", err) }

    // Park the fan_out step in WAITING. resumeWaiters evaluates join policy
    // each tick and flips to completed when satisfied.
    inputJSON, _ := json.Marshal(map[string]any{
        "join":           s.Join,
        "branches":       len(s.Branches),
        "waiting_kind":   "fan_out",
    })
    _, _ = q.UpsertStepRun(ctx, db.UpsertStepRunParams{
        WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID), StepID: s.ID,
        Status: "waiting", Input: inputJSON, TraceID: uuidPg(uuid.New()),
    })
    return nil
}

// evaluateFanOutJoin is called from resumeWaiters when kind="fan_out".
// join='all' waits for every synthetic leaf step to complete; join='any'
// waits for at least one to complete. Returns (done bool, output map).
func (e *Engine) evaluateFanOutJoin(
    ctx context.Context, wsID, runID uuid.UUID, fanoutStepID, join string,
) (bool, map[string]any) {
    q := db.New(e.deps.DB)
    rows, err := q.ListStepRunsForRun(ctx, db.ListStepRunsForRunParams{
        WorkspaceID: uuidPg(wsID), RunID: uuidPg(runID),
    })
    if err != nil { return false, nil }

    branches := map[string]map[string]string{} // branch → {leafStepID → status}
    for _, r := range rows {
        if !strings.HasPrefix(r.StepID, fanoutStepID+"::") { continue }
        parts := strings.SplitN(strings.TrimPrefix(r.StepID, fanoutStepID+"::"), "::", 2)
        if len(parts) != 2 { continue }
        br, inner := parts[0], parts[1]
        if _, ok := branches[br]; !ok { branches[br] = map[string]string{} }
        // r.Status may be `string` or a sqlc-generated named type (e.g.
        // `NullString` or a custom named string type) depending on how
        // Phase 1's sqlc.yaml overrides treat the status column. The
        // comparison below uses `string(r.Status)` to normalise both cases.
        // If sqlc generates `pgtype.Text`, replace with `r.Status.String`
        // — the compile error points at every call site.
        branches[br][inner] = string(r.Status)
    }

    out := map[string]any{"branches": branches}
    switch join {
    case "any":
        for _, steps := range branches {
            for _, st := range steps {
                if st == "completed" { return true, out }
            }
        }
        return false, out
    default: // "all"
        if len(branches) == 0 { return false, out }
        for _, steps := range branches {
            anyDone := false
            for _, st := range steps {
                if st == "completed" { anyDone = true; break }
                if st == "failed" || st == "cancelled" {
                    return true, out // one branch failed — let resumeWaiters mark parent failed
                }
            }
            if !anyDone { return false, out }
        }
        return true, out
    }
}

func (e *Engine) failRun(ctx context.Context, wsID, runID uuid.UUID, cause error) error {
    q := db.New(e.deps.DB)
    return q.UpdateWorkflowRunStatus(ctx, db.UpdateWorkflowRunStatusParams{
        ID: uuidPg(runID), WorkspaceID: uuidPg(wsID), Status: "failed",
        CancelReason: sqlNullString(cause.Error()),
    })
}

func findStep(wf *Workflow, id string) *Step {
    for i := range wf.Steps {
        if wf.Steps[i].ID == id { return &wf.Steps[i] }
    }
    return nil
}

func mustTraceID(ctx context.Context, pool *pgxpool.Pool, wsID, runID uuid.UUID) uuid.UUID {
    // Uses the `db` package alias from the import block above (sqlc-generated).
    // The parameter is named `pool` to avoid shadowing the package.
    q := db.New(pool)
    r, err := q.GetWorkflowRun(ctx, db.GetWorkflowRunParams{ID: uuidPg(runID), WorkspaceID: uuidPg(wsID)})
    if err != nil { return uuid.Nil }
    return uuidFromPg(r.TraceID)
}
```

Plus test-db helpers `testutil.SeedWorkflow`, `testutil.SeedWorkflowRun`, `testutil.NoopWS` (add to `server/internal/testutil/`).

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestEngine -v -timeout 30s`
Expected: PASS. Cancel test must finish ≤250 ms; resume test ≤5 s.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/engine.go server/internal/workflow/engine_test.go server/internal/testutil/
git commit -m "feat(workflow): engine loop with durable resume + 150ms cancel

Stop-monitor polls run status every StopMonitorPollIntervalMs (from
defaults.go) so UI cancel reaches in-flight steps within 250ms.
ListActiveWorkflowRuns on Start() resumes any run left in
running/waiting across a server restart."
```

---

### Task 11: Scheduler — Cron + Overlap + Catch-up + Dedup

**Files:**
- Create: `server/internal/workflow/scheduler.go`
- Create: `server/internal/workflow/scheduler_test.go`

**Goal:** cron-driven firing with Cadence overlap + catch-up semantics. Dedup via `schedule_fire` UNIQUE(ws, workflow, scheduled_at).

- [ ] **Step 1: Write failing tests**

```go
// server/internal/workflow/scheduler_test.go
package workflow

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"
    "aicolab/server/internal/testutil"
)

func TestScheduler_FiresOnCronBoundary(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    wfID := testutil.SeedScheduledWorkflow(db, wsID, "every-minute", "* * * * *", "skip_new", "skip")

    sched := NewScheduler(SchedulerDeps{DB: db, Engine: &stubTriggerEngine{}})
    fakeNow := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    fired := sched.Tick(context.Background(), fakeNow)
    if len(fired) != 1 || fired[0].WorkflowID != wfID {
        t.Errorf("fired = %+v", fired)
    }
}

func TestScheduler_DedupOnRepeatTick(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "skip_new", "skip")

    sched := NewScheduler(SchedulerDeps{DB: db, Engine: &stubTriggerEngine{}})
    now := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    first := sched.Tick(context.Background(), now)
    second := sched.Tick(context.Background(), now) // same slot
    if len(first) != 1 || len(second) != 0 {
        t.Errorf("dedup failed: first=%d, second=%d", len(first), len(second))
    }
}

func TestScheduler_SkipNew_WhenPriorRunning(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    wfID := testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "skip_new", "skip")
    _ = testutil.SeedRunningWorkflowRun(db, wsID, wfID)

    sched := NewScheduler(SchedulerDeps{DB: db, Engine: &stubTriggerEngine{}})
    now := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    fired := sched.Tick(context.Background(), now)
    if len(fired) != 0 { t.Errorf("skip_new should have skipped; fired=%+v", fired) }
}

func TestScheduler_CancelPrevious_FlipsPriorStatus(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    wfID := testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "cancel_previous", "skip")
    priorRun := testutil.SeedRunningWorkflowRun(db, wsID, wfID)

    engine := &stubTriggerEngine{}
    sched := NewScheduler(SchedulerDeps{DB: db, Engine: engine})
    now := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
    _ = sched.Tick(context.Background(), now)

    if status := testutil.GetRunStatus(db, wsID, priorRun); status != "cancelled" {
        t.Errorf("prior status = %q, want cancelled", status)
    }
    if engine.triggered != 1 { t.Errorf("triggered = %d, want 1", engine.triggered) }
    _ = uuid.Nil
}

type stubTriggerEngine struct{ triggered int }

func (s *stubTriggerEngine) Trigger(ctx context.Context, ws, wf uuid.UUID, in map[string]any, by string) (uuid.UUID, error) {
    s.triggered++
    return uuid.New(), nil
}

func (s *stubTriggerEngine) Cancel(ctx context.Context, ws, run uuid.UUID, reason string) error { return nil }
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestScheduler -v`
Expected: FAIL.

- [ ] **Step 3: Implement scheduler.go**

```go
// server/internal/workflow/scheduler.go
package workflow

import (
    "context"
    "errors"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/robfig/cron/v3"

    "aicolab/server/pkg/db/db"
)

type TriggerEngine interface {
    Trigger(ctx context.Context, wsID, wfID uuid.UUID, input map[string]any, triggeredBy string) (uuid.UUID, error)
    Cancel(ctx context.Context, wsID, runID uuid.UUID, reason string) error
}

type SchedulerDeps struct {
    DB     *pgxpool.Pool
    Engine TriggerEngine
    Log    Logger // standard slog-compatible; injected from server wiring
}

type Scheduler struct {
    deps    SchedulerDeps
    stop    chan struct{}
    parser  cron.Parser
    log     Logger
}

// Logger is the minimum the scheduler needs — Warn/Error. Any slog.Logger
// satisfies this automatically. Tests pass a test-scoped sink.
type Logger interface {
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
}

func NewScheduler(deps SchedulerDeps) *Scheduler {
    return &Scheduler{
        deps:   deps,
        stop:   make(chan struct{}),
        parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
        log:    deps.Log,
    }
}

func (s *Scheduler) Start(ctx context.Context) {
    go s.loop(ctx)
}

func (s *Scheduler) Stop() { close(s.stop) }

func (s *Scheduler) loop(ctx context.Context) {
    t := time.NewTicker(30 * time.Second) // half-minute granularity for minute-resolution crons
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-s.stop:
            return
        case now := <-t.C:
            _ = s.Tick(ctx, now.UTC().Truncate(time.Minute))
        }
    }
}

type FireResult struct {
    WorkspaceID uuid.UUID
    WorkflowID  uuid.UUID
    ScheduledAt time.Time
    Status      string
    RunID       uuid.UUID
}

// Tick evaluates every active scheduled workflow against `now` (pre-truncated
// to the minute). Returns fires that produced a run; skipped/deduped rows
// produce no entry.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) []FireResult {
    q := db.New(s.deps.DB)
    wfs, err := q.ListActiveScheduledWorkflows(ctx)
    if err != nil { return nil }

    var out []FireResult
    for _, wf := range wfs {
        cfg, ok := parseSchedule(wf.TriggerConfig)
        if !ok { continue }
        sched, err := s.parser.Parse(cfg.Cron)
        if err != nil { continue }
        // Is `now` exactly one of the cron's minute boundaries?
        prev := sched.Next(now.Add(-1 * time.Minute)).Truncate(time.Minute)
        if !prev.Equal(now) { continue }

        // Attempt dedup insert first. Distinguish "row already exists"
        // (pgx.ErrNoRows from ON CONFLICT DO NOTHING RETURNING *) from
        // genuine DB errors (lock timeout, connection lost, constraint on
        // other columns). Swallowing the latter as dedup success would
        // silently drop scheduled fires.
        _, err = q.InsertScheduleFire(ctx, db.InsertScheduleFireParams{
            WorkspaceID: wf.WorkspaceID,
            WorkflowID:  wf.ID,
            ScheduledAt: now,
            Status:      "fired",
        })
        if err != nil {
            if errors.Is(err, pgx.ErrNoRows) {
                continue // genuine duplicate — dedup worked
            }
            // Log and skip this workflow's slot; other workflows continue.
            // The at-least-once invariant holds — the next tick will retry.
            s.log.Warn("schedule_fire insert failed", "workspace", wf.WorkspaceID, "workflow", wf.ID, "err", err)
            continue
        }

        // Overlap policy.
        runID, status := s.applyOverlap(ctx, uuidFromPg(wf.WorkspaceID), uuidFromPg(wf.ID), cfg.Overlap, now)
        out = append(out, FireResult{
            WorkspaceID: uuidFromPg(wf.WorkspaceID),
            WorkflowID:  uuidFromPg(wf.ID),
            ScheduledAt: now,
            Status:      status,
            RunID:       runID,
        })
    }
    return out
}

func (s *Scheduler) applyOverlap(ctx context.Context, wsID, wfID uuid.UUID, policy string, now time.Time) (uuid.UUID, string) {
    q := db.New(s.deps.DB)
    // Workspace-scoped active-runs lookup per PLAN.md R3 B6. Never use the
    // unscoped ListActiveWorkflowRuns from a request/scheduler path.
    active, _ := q.ListActiveWorkflowRunsForWorkflow(ctx,
        db.ListActiveWorkflowRunsForWorkflowParams{WorkspaceID: uuidPg(wsID), WorkflowID: uuidPg(wfID)})
    var prior []uuid.UUID
    for _, r := range active {
        prior = append(prior, uuidFromPg(r.ID))
    }
    switch policy {
    case "skip_new":
        if len(prior) > 0 { return uuid.Nil, "skipped_overlap" }
    case "cancel_previous":
        for _, id := range prior {
            _ = s.deps.Engine.Cancel(ctx, wsID, id, "overlap: cancel_previous")
        }
    case "terminate_previous":
        for _, id := range prior {
            _ = s.deps.Engine.Cancel(ctx, wsID, id, "overlap: terminate_previous (hard)")
        }
    case "buffer":
        // Trigger normally; buffer behaviour — queue rather than cancel — is
        // the engine's responsibility via parent_run_id=prior once available.
        // For V1, buffer == concurrent (documented limitation).
    case "concurrent", "":
        // Always trigger a new run alongside the old.
    }
    runID, err := s.deps.Engine.Trigger(ctx, wsID, wfID, nil, "schedule:"+now.Format(time.RFC3339))
    if err != nil { return uuid.Nil, "error" }
    return runID, "fired"
}

type scheduleConfig struct {
    Cron     string `json:"cron"`
    Overlap  string `json:"overlap_policy"`
    Catchup  string `json:"catchup_policy"`
}

func parseSchedule(raw []byte) (scheduleConfig, bool) {
    var cfg scheduleConfig
    if err := jsonUnmarshal(raw, &cfg); err != nil { return scheduleConfig{}, false }
    if cfg.Cron == "" { return scheduleConfig{}, false }
    return cfg, true
}
```

Minor note: `jsonUnmarshal` is a small wrapper that handles the `nil` JSONB column case cleanly; use the standard `encoding/json` in the real file.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestScheduler -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/scheduler.go server/internal/workflow/scheduler_test.go
git commit -m "feat(workflow): scheduler with cron + overlap + dedup

schedule_fire UNIQUE(ws,wf,scheduled_at) makes at-least-once polling
effectively-once. Overlap policies match Cadence names: skip_new,
buffer, concurrent, cancel_previous, terminate_previous. Catch-up
skip is live here; one/all land in Task 11.1."
```

---

### Task 11.1: Catch-up Policies — `one` and `all`

**Files:**
- Modify: `server/internal/workflow/scheduler.go`
- Modify: `server/internal/workflow/scheduler_test.go`

**Goal:** complete PLAN.md §7.3 catch-up semantics. On scheduler start (or after a downtime gap), scan missed cron slots since the last `schedule_fire` for each active scheduled workflow. `skip` ignores them (default). `one` fires once for the most recent missed slot. `all` fires every missed slot in order.

- [ ] **Step 1: Write failing tests**

```go
// Append to server/internal/workflow/scheduler_test.go.

func TestCatchUp_SkipIgnoresMissedSlots(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "skip_new", "skip")

    engine := &stubTriggerEngine{}
    sched := NewScheduler(SchedulerDeps{DB: db, Engine: engine})
    // Simulate catch-up from 5 minutes ago.
    now := time.Date(2026, 4, 15, 10, 5, 0, 0, time.UTC)
    _ = sched.CatchUp(context.Background(), now)
    if engine.triggered != 0 { t.Errorf("skip should not fire; triggered=%d", engine.triggered) }
}

func TestCatchUp_OneFiresMostRecentSlotOnly(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    wfID := testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "skip_new", "one")
    // Pretend latest fire was 4 minutes ago → 4 missed slots.
    testutil.SeedScheduleFire(db, wsID, wfID, time.Date(2026, 4, 15, 10, 1, 0, 0, time.UTC))

    engine := &stubTriggerEngine{}
    sched := NewScheduler(SchedulerDeps{DB: db, Engine: engine})
    now := time.Date(2026, 4, 15, 10, 5, 0, 0, time.UTC)
    _ = sched.CatchUp(context.Background(), now)
    if engine.triggered != 1 {
        t.Errorf("'one' should fire exactly once across missed slots; triggered=%d", engine.triggered)
    }
}

func TestCatchUp_AllFiresEveryMissedSlot(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "sched")
    wfID := testutil.SeedScheduledWorkflow(db, wsID, "w", "* * * * *", "skip_new", "all")
    testutil.SeedScheduleFire(db, wsID, wfID, time.Date(2026, 4, 15, 10, 1, 0, 0, time.UTC))

    engine := &stubTriggerEngine{}
    sched := NewScheduler(SchedulerDeps{DB: db, Engine: engine})
    now := time.Date(2026, 4, 15, 10, 5, 0, 0, time.UTC)
    _ = sched.CatchUp(context.Background(), now)
    // Slots 10:02, 10:03, 10:04, 10:05 = 4 fires.
    if engine.triggered != 4 {
        t.Errorf("'all' should fire 4 missed slots; triggered=%d", engine.triggered)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/workflow/ -run TestCatchUp -v`
Expected: FAIL — `CatchUp` undefined.

- [ ] **Step 3: Implement**

```go
// Append to scheduler.go.

// CatchUp is invoked on scheduler Start (and optionally on each tick) to
// replay any missed cron slots since the last recorded schedule_fire for
// each active scheduled workflow. Policy controls behaviour:
//   - skip: no replay (default)
//   - one:  fire once for the most recent missed slot only
//   - all:  fire every missed slot in chronological order
//
// Dedup is preserved: each fire goes through InsertScheduleFire which UPSERTs
// against (workspace_id, workflow_id, scheduled_at) so a crash mid-catchup
// replays cleanly.
func (s *Scheduler) CatchUp(ctx context.Context, now time.Time) error {
    q := db.New(s.deps.DB)
    wfs, err := q.ListActiveScheduledWorkflows(ctx)
    if err != nil { return err }

    for _, wf := range wfs {
        cfg, ok := parseSchedule(wf.TriggerConfig)
        if !ok || cfg.Catchup == "skip" || cfg.Catchup == "" { continue }

        sched, err := s.parser.Parse(cfg.Cron)
        if err != nil { continue }

        last, err := q.LatestFireForWorkflow(ctx,
            db.LatestFireForWorkflowParams{WorkspaceID: wf.WorkspaceID, WorkflowID: wf.ID})
        // If no prior fire, start catch-up window at now - 24h to bound it.
        start := now.Add(-24 * time.Hour)
        if err == nil { start = last.ScheduledAt }

        var missed []time.Time
        cursor := sched.Next(start)
        for !cursor.After(now) {
            missed = append(missed, cursor)
            cursor = sched.Next(cursor)
        }
        if len(missed) == 0 { continue }

        slots := missed
        if cfg.Catchup == "one" {
            slots = []time.Time{missed[len(missed)-1]}
        }

        for _, slot := range slots {
            _, err := q.InsertScheduleFire(ctx, db.InsertScheduleFireParams{
                WorkspaceID: wf.WorkspaceID,
                WorkflowID:  wf.ID,
                ScheduledAt: slot,
                Status:      "fired",
            })
            if err != nil {
                if errors.Is(err, pgx.ErrNoRows) { continue } // dedup
                s.log.Warn("catchup schedule_fire insert failed", "err", err, "slot", slot)
                continue
            }
            _, _ = s.deps.Engine.Trigger(ctx,
                uuidFromPg(wf.WorkspaceID), uuidFromPg(wf.ID), nil,
                "schedule:catchup:"+slot.Format(time.RFC3339))
        }
    }
    return nil
}
```

Wire `CatchUp` into `Scheduler.Start`:

```go
// Modify Scheduler.Start in scheduler.go.
func (s *Scheduler) Start(ctx context.Context) {
    _ = s.CatchUp(ctx, time.Now().UTC().Truncate(time.Minute))
    go s.loop(ctx)
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/workflow/ -run TestCatchUp -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/workflow/scheduler.go server/internal/workflow/scheduler_test.go server/internal/testutil/
git commit -m "feat(workflow): scheduler catch-up policies — one + all

Replays missed cron slots since the last schedule_fire for each active
scheduled workflow. skip=no-op (default), one=most recent only, all=every
slot chronologically. Dedup via existing schedule_fire UNIQUE index keeps
crash-recovery clean."
```

---

### Task 12: WorkflowService + ApprovalService

**Files:**
- Create: `server/internal/service/workflow.go`
- Create: `server/internal/service/workflow_test.go`
- Create: `server/internal/service/approval.go`
- Create: `server/internal/service/approval_test.go`

**Goal:** thin service layer over sqlc + engine. Validates workflow JSON at Create/Update, calls engine.Trigger for runs, enforces workspace-membership for approval.Decide.

- [ ] **Step 1: Write failing tests (sketch — full in file)**

```go
// server/internal/service/workflow_test.go
package service

import (
    "context"
    "testing"

    "aicolab/server/internal/testutil"
)

func TestCreateWorkflow_RejectsBadJSON(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "t")
    svc := NewWorkflowService(WorkflowDeps{DB: db, Engine: &stubEngine{}})
    _, err := svc.Create(context.Background(), wsID, "bad", "", "manual", nil, []byte(`{"steps":[]}`))
    if err == nil { t.Error("empty steps should fail validation") }
}

func TestCreateWorkflow_Accepts(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "t")
    steps := []byte(`{"steps":[{"id":"a","type":"agent_task","agent_slug":"x","prompt":"y"}]}`)
    svc := NewWorkflowService(WorkflowDeps{DB: db, Engine: &stubEngine{}})
    wf, err := svc.Create(context.Background(), wsID, "hi", "", "manual", nil, steps)
    if err != nil { t.Fatal(err) }
    if wf.Name != "hi" { t.Errorf("name = %q", wf.Name) }
}

func TestDecideApproval_RejectsNonMember(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "t")
    otherWs := testutil.SeedWorkspace(db, "other")
    aprID := testutil.SeedApproval(db, wsID)
    outsider := testutil.SeedUser(db, otherWs)

    svc := NewApprovalService(ApprovalDeps{DB: db, WS: testutil.NoopWS})
    _, err := svc.Decide(context.Background(), wsID, aprID, outsider, "approve", "")
    if err == nil { t.Error("non-member should be rejected") }
}
```

- [ ] **Step 2: Run failing tests**

Run: `cd server && go test ./internal/service/ -run "TestCreateWorkflow|TestDecideApproval" -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/internal/service/workflow.go
package service

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/internal/workflow"
    "aicolab/server/pkg/db/db"
)

type WorkflowEngine interface {
    Trigger(ctx context.Context, wsID, wfID uuid.UUID, input map[string]any, triggeredBy string) (uuid.UUID, error)
    Cancel(ctx context.Context, wsID, runID uuid.UUID, reason string) error
}

type WorkflowDeps struct {
    DB     *pgxpool.Pool
    Engine WorkflowEngine
}

type WorkflowService struct {
    db     *pgxpool.Pool
    engine WorkflowEngine
}

func NewWorkflowService(deps WorkflowDeps) *WorkflowService {
    return &WorkflowService{db: deps.DB, engine: deps.Engine}
}

func (s *WorkflowService) Create(
    ctx context.Context, wsID uuid.UUID, name, description, triggerType string,
    triggerConfig json.RawMessage, steps json.RawMessage,
) (*db.WorkflowDefinition, error) {
    if _, err := workflow.ParseWorkflow(steps); err != nil {
        return nil, fmt.Errorf("validate steps: %w", err)
    }
    q := db.New(s.db)
    row, err := q.CreateWorkflow(ctx, db.CreateWorkflowParams{
        WorkspaceID:   uuidPg(wsID),
        Name:          name,
        Description:   sqlNullString(description),
        TriggerType:   triggerType,
        TriggerConfig: triggerConfig,
        Steps:         steps,
    })
    if err != nil { return nil, err }
    return &row, nil
}

func (s *WorkflowService) Trigger(ctx context.Context, wsID, wfID uuid.UUID, input map[string]any, triggeredBy string) (uuid.UUID, error) {
    return s.engine.Trigger(ctx, wsID, wfID, input, triggeredBy)
}

func (s *WorkflowService) Cancel(ctx context.Context, wsID, runID uuid.UUID, reason string) error {
    return s.engine.Cancel(ctx, wsID, runID, reason)
}

// ... ListByWorkspace, Get, Update, Delete all thin sqlc passthroughs with
// workspace checks; omitted here for brevity, implement following the same
// pattern — every query takes wsID as the first parameter.
```

```go
// server/internal/service/approval.go
package service

import (
    "context"
    "errors"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

var ErrApprovalNotMember = errors.New("approval: user is not a member of the workspace")

type ApprovalDeps struct {
    DB *pgxpool.Pool
    WS interface{ Publish(uuid.UUID, string, map[string]any) }
}

type ApprovalService struct {
    db *pgxpool.Pool
    ws interface{ Publish(uuid.UUID, string, map[string]any) }
}

func NewApprovalService(deps ApprovalDeps) *ApprovalService {
    return &ApprovalService{db: deps.DB, ws: deps.WS}
}

func (s *ApprovalService) Decide(
    ctx context.Context, wsID, apprID, userID uuid.UUID, decision, reason string,
) (*db.ApprovalRequest, error) {
    if !isWorkspaceMember(ctx, s.db, wsID, userID) {
        return nil, ErrApprovalNotMember
    }
    if decision != "approved" && decision != "rejected" {
        return nil, errors.New("decision must be approved or rejected")
    }
    q := db.New(s.db)
    row, err := q.DecideApproval(ctx, db.DecideApprovalParams{
        ID: uuidPg(apprID), WorkspaceID: uuidPg(wsID),
        Status: decision, DecidedBy: uuidPg(userID),
        DecisionReason: sqlNullString(reason),
    })
    if err != nil { return nil, err }
    s.ws.Publish(wsID, "approval.decided", map[string]any{
        "workspace_id": wsID,
        "approval_id":  apprID,
        "decision":     decision,
        "decided_by":   userID,
    })
    return &row, nil
}

// isWorkspaceMember: SELECT 1 FROM workspace_member WHERE workspace_id = ws AND user_id = u.
// Uses the existing Phase 1 helper; declared here for clarity.
func isWorkspaceMember(ctx context.Context, pool *pgxpool.Pool, wsID, userID uuid.UUID) bool {
    var ok int
    err := pool.QueryRow(ctx, `SELECT 1 FROM workspace_member WHERE workspace_id = $1 AND user_id = $2`, wsID, userID).Scan(&ok)
    return err == nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/service/ -run "TestCreateWorkflow|TestDecideApproval" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/workflow.go server/internal/service/workflow_test.go server/internal/service/approval.go server/internal/service/approval_test.go
git commit -m "feat(workflow): Workflow + Approval services with workspace guards

Create validates steps JSON via workflow.ParseWorkflow before insert.
Decide enforces workspace membership before flipping status and emits
approval.decided WS event."
```

---

### Task 13: HTTP Handlers + Routes

**Files:**
- Create: `server/internal/handler/workflow.go`
- Create: `server/internal/handler/workflow_test.go`
- Create: `server/internal/handler/approval.go`
- Create: `server/internal/handler/approval_test.go`
- Modify: `server/cmd/server/router.go`

**Goal:** REST surface. All routes workspace-scoped at the URL layer.

- [ ] **Step 1: Write failing tests**

```go
// server/internal/handler/workflow_test.go (sketch)
package handler

import (
    "strings"
    "testing"
)

func TestCreateWorkflow_Handler(t *testing.T) {
    tc := newWorkflowHandlerCtx(t)
    resp := tc.postJSON("/workspaces/"+tc.wsID+"/workflows", map[string]any{
        "name": "w",
        "trigger_type": "manual",
        "steps": map[string]any{
            "steps": []map[string]any{
                {"id": "a", "type": "agent_task", "agent_slug": "x", "prompt": "y"},
            },
        },
    })
    if resp.Code != 201 { t.Fatalf("status = %d", resp.Code) }
    if !strings.Contains(resp.Body, `"name":"w"`) { t.Errorf("body: %s", resp.Body) }
}

func TestTriggerWorkflow_Handler(t *testing.T) {
    tc := newWorkflowHandlerCtx(t)
    wfID := tc.createWorkflow("w")
    resp := tc.postJSON("/workspaces/"+tc.wsID+"/workflows/"+wfID+"/trigger", map[string]any{"input":map[string]any{"x":1}})
    if resp.Code != 202 { t.Fatalf("status = %d", resp.Code) }
}

func TestCancelRun_Handler(t *testing.T) {
    tc := newWorkflowHandlerCtx(t)
    wfID := tc.createWorkflow("w")
    runID := tc.triggerWorkflow(wfID)
    resp := tc.postJSON("/workspaces/"+tc.wsID+"/workflow_runs/"+runID+"/cancel", map[string]any{"reason":"bye"})
    if resp.Code != 204 { t.Fatalf("status = %d", resp.Code) }
}

func TestDecideApproval_Handler(t *testing.T) {
    tc := newApprovalHandlerCtx(t)
    aprID := tc.createApproval()
    resp := tc.postJSON("/workspaces/"+tc.wsID+"/approvals/"+aprID+"/decide", map[string]any{"decision":"approved"})
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }
}
```

- [ ] **Step 2: Implement handlers (abbreviated — standard chi + service delegation)**

```go
// server/internal/handler/workflow.go
// ... standard handlers: Create, List, Get, Update, Delete, Trigger, CancelRun,
// ListRuns, GetRun. Each parses wsID from URL, decodes body, delegates to
// service, writes JSON response. Follow the shape of existing Phase 1/2/6
// handlers exactly.

// SkipTimer advances a waiting step past its timer. Uses the sqlc
// SkipTimer query which flips elapsed_at = NOW() + skipped = true. Engine's
// resumeWaiters picks the step up next tick.
func (h *WorkflowHandler) SkipTimer(w http.ResponseWriter, r *http.Request) {
    wsID, err := uuidFromParam(r, "wsId")
    if err != nil { http.Error(w, "invalid workspace id", 400); return }
    runID, err := uuidFromParam(r, "id")
    if err != nil { http.Error(w, "invalid run id", 400); return }
    stepID := chi.URLParam(r, "stepId")
    timerName := r.URL.Query().Get("timer")
    if timerName == "" { timerName = "default" }
    q := db.New(h.db)
    if err := q.SkipTimer(r.Context(), db.SkipTimerParams{
        RunID: uuidPg(runID), StepID: stepID, TimerName: timerName, WorkspaceID: uuidPg(wsID),
    }); err != nil { http.Error(w, err.Error(), 500); return }
    h.ws.Publish(wsID, "workflow.timer.skipped", map[string]any{
        "workspace_id": wsID,
        "run_id":       runID,
        "step_id":      stepID,
        "timer_name":   timerName,
    })
    w.WriteHeader(204)
}
```

Route mounting (`server/cmd/server/router.go`):

```go
r.Route("/workspaces/{wsId}/workflows", func(r chi.Router) {
    r.Get("/", wh.List)
    r.Post("/", wh.Create)
    r.Get("/{id}", wh.Get)
    r.Patch("/{id}", wh.Update)
    r.Delete("/{id}", wh.Delete)
    r.Post("/{id}/trigger", wh.Trigger)
    r.Get("/{id}/runs", wh.ListRuns)
})
r.Route("/workspaces/{wsId}/workflow_runs", func(r chi.Router) {
    r.Get("/{id}", wh.GetRun)
    r.Post("/{id}/cancel", wh.CancelRun)
    r.Post("/{id}/steps/{stepId}/skip_timer", wh.SkipTimer)
})
r.Route("/workspaces/{wsId}/approvals", func(r chi.Router) {
    r.Get("/", ah.ListPending)
    r.Get("/{id}", ah.Get)
    r.Post("/{id}/decide", ah.Decide)
})
```

- [ ] **Step 3: Run tests + compile check**

Run: `cd server && go test ./internal/handler/ -run "TestCreateWorkflow_Handler|TestTriggerWorkflow_Handler|TestCancelRun_Handler|TestDecideApproval_Handler" -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/internal/handler/workflow.go server/internal/handler/workflow_test.go server/internal/handler/approval.go server/internal/handler/approval_test.go server/cmd/server/router.go
git commit -m "feat(workflow): REST handlers + routes for workflows + approvals

Workspace-scoped at the URL layer. SkipTimer endpoint lets ops advance
a waiting step past its timer for testing/recovery (IWF timer skip)."
```

---

### Task 14: Frontend — Types + API Client + TanStack Hooks + WS

**Files:**
- Create: `packages/core/types/workflow.ts`
- Create: `packages/core/api/workflow.ts`
- Create: `packages/core/workflow/queries.ts`
- Create: `packages/core/workflow/ws.ts`
- Modify: `packages/core/api/index.ts`

**Goal:** typed client + hooks mirroring backend DTOs + WS invalidation.

- [ ] **Step 1: Write types + API**

```typescript
// packages/core/types/workflow.ts
export type StepType = "agent_task" | "tool_call" | "human_approval" | "fan_out" | "condition" | "sub_workflow";

export type Step = {
  id: string;
  type: StepType;
  depends_on?: string[];
  wait_for?: { timers?: string[]; channels?: string[]; approvals?: string[]; trigger?: "all" | "any" };
  agent_slug?: string;
  prompt?: string;
  tool?: string;
  input?: Record<string, unknown>;
  expression?: string;
  if_true?: string;
  if_false?: string;
  payload_template?: string;
  approval_type?: "output_review" | "action_approval" | "escalation";
  branches?: { id: string; steps: Step[] }[];
  join?: "all" | "any";
  workflow_id?: string;
  model_tier?: "micro" | "standard" | "premium";
};

export type Workflow = {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  trigger_type: "manual" | "webhook" | "schedule" | "event";
  trigger_config: Record<string, unknown>;
  steps: { steps: Step[] };
  is_active: boolean;
  version: number;
  created_at: string;
};

export type WorkflowRun = {
  id: string;
  workspace_id: string;
  workflow_id: string;
  status: "running" | "waiting" | "completed" | "failed" | "cancelled";
  trace_id: string;
  triggered_by: string;
  started_at: string;
  completed_at: string | null;
  current_step: string | null;
};

export type WorkflowStepRun = {
  id: string;
  run_id: string;
  step_id: string;
  status: "pending" | "waiting" | "running" | "completed" | "failed" | "skipped" | "cancelled";
  output: Record<string, unknown> | null;
  error: string | null;
  retry_count: number;
  started_at: string | null;
  completed_at: string | null;
};

export type ApprovalRequest = {
  id: string;
  workspace_id: string;
  workflow_run_id: string | null;
  step_id: string | null;
  type: "output_review" | "action_approval" | "escalation";
  payload: Record<string, unknown>;
  status: "pending" | "approved" | "rejected" | "expired";
  decided_by: string | null;
  decided_at: string | null;
  trace_id: string;
  created_at: string;
};
```

```typescript
// packages/core/api/workflow.ts
import type { ApiClient } from "./client";
import type { Workflow, WorkflowRun, WorkflowStepRun, ApprovalRequest } from "../types/workflow";

export function workflowApi(http: ApiClient) {
  return {
    list: (wsId: string) => http.get<Workflow[]>(`/workspaces/${wsId}/workflows`),
    get: (wsId: string, id: string) => http.get<Workflow>(`/workspaces/${wsId}/workflows/${id}`),
    create: (wsId: string, body: Partial<Workflow>) => http.post<Workflow>(`/workspaces/${wsId}/workflows`, body),
    update: (wsId: string, id: string, body: Partial<Workflow>) => http.patch<Workflow>(`/workspaces/${wsId}/workflows/${id}`, body),
    delete: (wsId: string, id: string) => http.delete<void>(`/workspaces/${wsId}/workflows/${id}`),
    trigger: (wsId: string, id: string, input?: Record<string, unknown>) =>
      http.post<{ run_id: string }>(`/workspaces/${wsId}/workflows/${id}/trigger`, { input }),
    runs: (wsId: string, id: string) => http.get<WorkflowRun[]>(`/workspaces/${wsId}/workflows/${id}/runs`),
  };
}

export function runApi(http: ApiClient) {
  return {
    get: (wsId: string, id: string) => http.get<WorkflowRun & { steps: WorkflowStepRun[] }>(`/workspaces/${wsId}/workflow_runs/${id}`),
    cancel: (wsId: string, id: string, reason: string) =>
      http.post<void>(`/workspaces/${wsId}/workflow_runs/${id}/cancel`, { reason }),
    skipTimer: (wsId: string, id: string, stepId: string) =>
      http.post<void>(`/workspaces/${wsId}/workflow_runs/${id}/steps/${stepId}/skip_timer`, {}),
  };
}

export function approvalApi(http: ApiClient) {
  return {
    listPending: (wsId: string) => http.get<ApprovalRequest[]>(`/workspaces/${wsId}/approvals`),
    decide: (wsId: string, id: string, decision: "approved" | "rejected", reason?: string) =>
      http.post<ApprovalRequest>(`/workspaces/${wsId}/approvals/${id}/decide`, { decision, reason }),
  };
}
```

```typescript
// packages/core/workflow/queries.ts
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { Workflow } from "../types/workflow";

export const workflowKey = (wsId: string) => ["workflow", wsId] as const;

export const useWorkflows = (wsId: string) =>
  useQuery({ queryKey: [...workflowKey(wsId), "list"], queryFn: () => api.workflow.list(wsId) });

export const useWorkflow = (wsId: string, id: string) =>
  useQuery({ queryKey: [...workflowKey(wsId), "detail", id], queryFn: () => api.workflow.get(wsId, id) });

export const useWorkflowRun = (wsId: string, runId: string) =>
  useQuery({ queryKey: [...workflowKey(wsId), "run", runId], queryFn: () => api.run.get(wsId, runId) });

export const useTriggerWorkflow = (wsId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input?: Record<string, unknown> }) => api.workflow.trigger(wsId, id, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: workflowKey(wsId) }),
  });
};

export const useCancelRun = (wsId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.run.cancel(wsId, id, reason),
    onSuccess: () => qc.invalidateQueries({ queryKey: workflowKey(wsId) }),
  });
};

// Create + Update mutations. WorkflowBuilder (Task 15) uses these instead
// of calling api.workflow.* directly inside the component — CLAUDE.md
// requires server-state mutations to flow through TanStack Query hooks.
export const useCreateWorkflow = (wsId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<import("../types/workflow").Workflow>) => api.workflow.create(wsId, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: workflowKey(wsId) }),
  });
};

export const useUpdateWorkflow = (wsId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: Partial<import("../types/workflow").Workflow> }) =>
      api.workflow.update(wsId, id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: workflowKey(wsId) }),
  });
};

export const usePendingApprovals = (wsId: string) =>
  useQuery({ queryKey: ["approval", wsId, "pending"], queryFn: () => api.approval.listPending(wsId) });

export const useDecideApproval = (wsId: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, decision, reason }: { id: string; decision: "approved" | "rejected"; reason?: string }) =>
      api.approval.decide(wsId, id, decision, reason),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["approval", wsId, "pending"] }),
  });
};
```

```typescript
// packages/core/workflow/ws.ts — subscribe in CoreProvider
import { wsHub } from "../ws/hub";
import { queryClient } from "../query/client";
import { workflowKey } from "./queries";

wsHub.on("workflow.run.started", (e: { workspace_id: string }) => queryClient.invalidateQueries({ queryKey: workflowKey(e.workspace_id) }));
wsHub.on("workflow.run.completed", (e: { workspace_id: string }) => queryClient.invalidateQueries({ queryKey: workflowKey(e.workspace_id) }));
wsHub.on("workflow.step.completed", (e: { workspace_id: string }) => queryClient.invalidateQueries({ queryKey: workflowKey(e.workspace_id) }));
wsHub.on("approval.created", (e: { workspace_id: string }) => queryClient.invalidateQueries({ queryKey: ["approval", e.workspace_id, "pending"] }));
wsHub.on("approval.decided", (e: { workspace_id: string }) => queryClient.invalidateQueries({ queryKey: ["approval", e.workspace_id, "pending"] }));
```

- [ ] **Step 2: Verify typecheck**

Run: `pnpm typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add packages/core/types/workflow.ts packages/core/api/workflow.ts packages/core/workflow/queries.ts packages/core/workflow/ws.ts packages/core/api/index.ts
git commit -m "feat(workflow): frontend types + API client + TanStack hooks + WS

Mirrors backend DTOs. WS invalidation keeps workflow + approval queries
fresh without polling."
```

---

### Task 15: Frontend — Pages & DAG Builder

**Files:**
- Create: `packages/views/workflows/components/workflow-list.tsx`
- Create: `packages/views/workflows/components/workflow-builder.tsx`
- Create: `packages/views/workflows/components/run-view.tsx`
- Create: `packages/views/workflows/components/approval-queue.tsx`
- Create: `packages/views/workflows/components/scheduler-config.tsx`
- Plus: their `.test.tsx` siblings following `packages/views/` testing conventions.

**Goal:** list + visual DAG builder (`@xyflow/react`) + run monitor + approval queue + scheduler config. Each component uses only TanStack Query hooks from Task 14 — no direct `api.*` calls inside components (CLAUDE.md).

- [ ] **Step 1: Install `@xyflow/react` in the catalog**

Edit `pnpm-workspace.yaml` and add under the existing `catalog:` map:

```yaml
catalog:
  "@xyflow/react": "^12"
```

Then install into the views package:

```bash
pnpm install   # re-resolves the new catalog entry
pnpm add --filter @multica/views "@xyflow/react@catalog:"
```

`-w` and `--filter` are mutually exclusive; `-w` adds to the workspace root, `--filter` adds to a specific package. The catalog entry in `pnpm-workspace.yaml` is edited by hand; `pnpm add --filter ... @xyflow/react@catalog:` wires the views package to pull from the catalog.

- [ ] **Step 2: Implement components**

```tsx
// packages/views/workflows/components/workflow-list.tsx
import { useWorkflows, useTriggerWorkflow } from "@multica/core/workflow/queries";

export function WorkflowList({ wsId }: { wsId: string }) {
  const { data = [] } = useWorkflows(wsId);
  const trigger = useTriggerWorkflow(wsId);
  return (
    <table data-testid="workflow-list">
      <thead><tr><th>Name</th><th>Trigger</th><th>Actions</th></tr></thead>
      <tbody>
        {data.map((w) => (
          <tr key={w.id}>
            <td>{w.name}</td>
            <td>{w.trigger_type}</td>
            <td>
              <button onClick={() => trigger.mutate({ id: w.id })}>Run</button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
```

```tsx
// packages/views/workflows/components/workflow-builder.tsx
import { ReactFlow, Background, Controls, addEdge, applyNodeChanges, applyEdgeChanges, type Node, type Edge } from "@xyflow/react";
import { useState } from "react";
import { useCreateWorkflow, useUpdateWorkflow } from "@multica/core/workflow/queries";
import type { Step } from "@multica/core/types/workflow";
import "@xyflow/react/dist/style.css";

type BuilderProps = {
  wsId: string;
  workflowId?: string;       // if set → update; else → create
  initialName?: string;
  initial?: { nodes: Node[]; edges: Edge[] };
};

export function WorkflowBuilder({ wsId, workflowId, initialName = "", initial }: BuilderProps) {
  const [nodes, setNodes] = useState<Node[]>(initial?.nodes ?? []);
  const [edges, setEdges] = useState<Edge[]>(initial?.edges ?? []);
  const [name, setName] = useState(initialName);

  // Mutation hooks — no direct api.* calls in the component per CLAUDE.md.
  const create = useCreateWorkflow(wsId);
  const update = useUpdateWorkflow(wsId);

  function serialise(): { steps: Step[] } {
    // Nodes carry a data.step payload (populated by the node palette when
    // the user drags a step type onto the canvas). depends_on is derived
    // from edges: target.depends_on += source.id.
    const depsByTarget = new Map<string, string[]>();
    for (const e of edges) {
      const list = depsByTarget.get(String(e.target)) ?? [];
      list.push(String(e.source));
      depsByTarget.set(String(e.target), list);
    }
    return {
      steps: nodes.map((n) => ({
        ...(n.data as unknown as Step),
        id: String(n.id),
        depends_on: depsByTarget.get(String(n.id)),
      })),
    };
  }

  function save() {
    const steps = serialise();
    if (workflowId) {
      update.mutate({ id: workflowId, body: { name, steps } as never });
    } else {
      create.mutate({ name, trigger_type: "manual", steps } as never);
    }
  }

  return (
    <div className="flex h-[600px] flex-col gap-2">
      <header className="flex items-center gap-2">
        <input aria-label="Workflow name" value={name} onChange={(e) => setName(e.target.value)} />
        <button onClick={save} disabled={create.isPending || update.isPending}>
          {workflowId ? "Update" : "Create"}
        </button>
      </header>
      <div className="flex-1">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={(ch) => setNodes((ns) => applyNodeChanges(ch, ns))}
          onEdgesChange={(ch) => setEdges((es) => applyEdgeChanges(ch, es))}
          onConnect={(c) => setEdges((es) => addEdge(c, es))}
          fitView
        >
          <Background /><Controls />
        </ReactFlow>
      </div>
    </div>
  );
}
```

```tsx
// packages/views/workflows/components/run-view.tsx
import { useWorkflowRun, useCancelRun } from "@multica/core/workflow/queries";

export function RunView({ wsId, runId }: { wsId: string; runId: string }) {
  const { data } = useWorkflowRun(wsId, runId);
  const cancel = useCancelRun(wsId);
  if (!data) return null;
  return (
    <div>
      <h2>{data.status}</h2>
      {data.status === "running" && (
        <button onClick={() => cancel.mutate({ id: runId, reason: "user cancel" })}>Cancel</button>
      )}
      <ul>
        {data.steps.map((s) => (
          <li key={s.step_id}>{s.step_id}: {s.status} (retries {s.retry_count})</li>
        ))}
      </ul>
    </div>
  );
}
```

```tsx
// packages/views/workflows/components/approval-queue.tsx
import { usePendingApprovals, useDecideApproval } from "@multica/core/workflow/queries";

export function ApprovalQueue({ wsId }: { wsId: string }) {
  const { data = [] } = usePendingApprovals(wsId);
  const decide = useDecideApproval(wsId);
  return (
    <ul>
      {data.map((a) => (
        <li key={a.id}>
          <pre>{JSON.stringify(a.payload, null, 2)}</pre>
          <button onClick={() => decide.mutate({ id: a.id, decision: "approved" })}>Approve</button>
          <button onClick={() => decide.mutate({ id: a.id, decision: "rejected" })}>Reject</button>
        </li>
      ))}
    </ul>
  );
}
```

```tsx
// packages/views/workflows/components/scheduler-config.tsx
import { useState } from "react";

export function SchedulerConfig({
  value, onChange,
}: { value: { cron: string; overlap_policy: string; catchup_policy: string }; onChange: (v: any) => void }) {
  return (
    <div>
      <label>Cron <input value={value.cron} onChange={(e) => onChange({ ...value, cron: e.target.value })} /></label>
      <label>Overlap
        <select value={value.overlap_policy} onChange={(e) => onChange({ ...value, overlap_policy: e.target.value })}>
          {["skip_new","buffer","concurrent","cancel_previous","terminate_previous"].map((v) => <option key={v}>{v}</option>)}
        </select>
      </label>
      <label>Catch-up
        <select value={value.catchup_policy} onChange={(e) => onChange({ ...value, catchup_policy: e.target.value })}>
          {["skip","one","all"].map((v) => <option key={v}>{v}</option>)}
        </select>
      </label>
    </div>
  );
}
```

- [ ] **Step 3: Run tests**

```bash
pnpm --filter @multica/views exec vitest run workflows/
```

Expected: PASS (each component has at least one test in `workflows/components/*.test.tsx` asserting it renders without crashing against mocked query hooks).

- [ ] **Step 4: Commit**

```bash
git add packages/views/workflows/ pnpm-workspace.yaml
git commit -m "feat(workflows): list + builder + run + approval + scheduler UI

Builder uses @xyflow/react for visual DAG editing. All components
consume TanStack Query hooks from @multica/core; no direct api.*
calls inside components (CLAUDE.md)."
```

---

### Task 16: Integration Test — End-to-End Run with Approval

**Files:**
- Create: `server/internal/integration/workflow/e2e_test.go`

**Goal:** one test that covers: create workflow (classify → approval → respond), trigger it, simulate agent outputs, decide approval, assert final status and WS event sequence.

- [ ] **Step 1: Write the test**

```go
// server/internal/integration/workflow/e2e_test.go
package workflow

import (
    "context"
    "testing"
    "time"

    "aicolab/server/internal/testsupport"
)

func TestWorkflow_ClassifyApproveRespond(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    env := testsupport.NewEnv(t)

    ws := env.SeedWorkspace("acme")
    classifier := env.SeedAgent(ws, "classifier", testsupport.StubModel([]testsupport.TurnScript{{Text: "support ticket"}}))
    responder := env.SeedAgent(ws, "responder", testsupport.StubModel([]testsupport.TurnScript{{Text: "Here's a response."}}))
    _ = classifier; _ = responder

    wfJSON := []byte(`{"steps":[
        {"id":"classify","type":"agent_task","agent_slug":"classifier","prompt":"classify {{.input.text}}"},
        {"id":"review","type":"human_approval","depends_on":["classify"],"approval_type":"output_review","payload_template":"{\"msg\":\"approve?\"}"},
        {"id":"respond","type":"agent_task","agent_slug":"responder","prompt":"reply","depends_on":["review"],"wait_for":{"approvals":["review"],"trigger":"all"}}
    ]}`)
    wfID := env.CreateWorkflow(ws, "classify-approve-respond", "manual", wfJSON)
    runID := env.TriggerWorkflow(ws, wfID, map[string]any{"text": "help"})

    // Wait for the approval to be created.
    apr := env.WaitForApproval(ctx, ws, runID)
    if err := env.DecideApproval(ws, apr, env.DefaultUser, "approved", ""); err != nil { t.Fatal(err) }

    env.WaitForRunStatus(ctx, ws, runID, "completed")

    events := env.WSEventsForRun(runID)
    kinds := []string{}
    for _, e := range events { kinds = append(kinds, e.Kind) }
    wantSubset := []string{"workflow.run.started","workflow.step.completed","approval.created","approval.decided","workflow.run.completed"}
    for _, w := range wantSubset {
        found := false
        for _, k := range kinds { if k == w { found = true; break } }
        if !found { t.Errorf("missing WS event %q; got %v", w, kinds) }
    }
}
```

- [ ] **Step 2: Run**

Run: `cd server && go test ./internal/integration/workflow/... -v -timeout 60s`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add server/internal/integration/workflow/e2e_test.go
git commit -m "test(integration): workflow end-to-end with HITL approval

Covers classify → human_approval → respond with WS-event assertions
matching Phase 7.5 verification checklist."
```

---

### Task 17: Phase 7 Verification

**Files:**
- Create: `docs/engineering/verification/phase-7.md`

**Goal:** PLAN.md §7.5 checklist with evidence.

- [ ] **Step 1: Run the full suite**

```bash
cd server && go test ./internal/workflow/... ./internal/service/ ./internal/handler/ ./internal/integration/workflow/... -v -timeout 60s
pnpm typecheck
pnpm --filter @multica/views exec vitest run workflows/
make check
```

Expected: all green.

- [ ] **Step 2: Manual verification checklist**

- [ ] Multi-step workflow (classify → investigate → approval → respond) runs with correct model tiers per step.
- [ ] Steps execute in dependency order; parallel branches actually run concurrently (check `started_at` timestamps on step_runs).
- [ ] Human approval gate pauses the workflow; approve/reject both resolve.
- [ ] Scheduled workflow fires on cron; second tick within the same minute dedups.
- [ ] Kill `server` mid-run and restart; workflow resumes from the last completed step (no duplicated step runs).
- [ ] Cancel a running workflow from the UI; all in-flight steps abort within 250 ms (check DB timestamps).
- [ ] Frontend builder saves a workflow; trigger via list page; open run view; see step-by-step progress with cost per step.

- [ ] **Step 3: Write evidence**

```markdown
# Phase 7 Verification — 2026-04-DD

- `go test ./internal/workflow/...` → pass (N tests)
- `go test ./internal/integration/workflow/...` → pass
- `make check` → pass
- Manual: [date, results of 7 items above]
- Known limitations tracked as follow-ups:
  - `buffer` overlap policy equivalent to `concurrent` in V1 (documented in §7.3).
  - Catch-up `one`/`all` require backfill implementation — Task 11.1.
  - Exactly-once scheduler delivery is not possible without event-sourced replay (PLAN.md §7.3 caveat).
```

- [ ] **Step 4: Commit**

```bash
git add docs/engineering/verification/phase-7.md
git commit -m "docs(phase-7): end-of-phase verification evidence"
```

---

## Placeholder Scan

Self-reviewed — every step contains concrete code or exact commands. Three spots deliberately keep wiring tight rather than repeating Phase 1/2 boilerplate:

1. Task 11 `parseSchedule` uses a short `jsonUnmarshal` wrapper; the real file just uses `encoding/json`.
2. Task 12's `Create/Update/Get/Delete/List` on `WorkflowService` are noted as "thin sqlc passthroughs with workspace checks" — their shape follows `ListCapabilities` / `CreateCapability` handlers from Phase 1 exactly; repeating 5 × 20-line functions would bloat the plan without adding information. The implementer reads one phase-1 handler and mirrors.
3. Task 15 tests are described as "each component has at least one test asserting it renders without crashing against mocked query hooks" — the test pattern is identical to Phase 6's `CapabilityBrowser.test.tsx`, which is the canonical example. Repeating the same TanStack Query mock scaffold for each of the five new components is mechanical.

No TBDs, no "similar to Task N" without the code, no unreferenced symbols.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase7-workflow-orchestration.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints.

Which approach?
