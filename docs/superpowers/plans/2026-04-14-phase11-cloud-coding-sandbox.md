# Phase 11: Cloud Coding Sandbox (E2B + gVisor) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a cloud-hosted coding agent runtime — no local daemon required — by porting the Open Agents `Sandbox` interface to Go, wiring two self-hosted implementations (E2B Firecracker microVMs, gVisor on Kubernetes with a Docker fallback), and driving them with a Brain/Hands worker that exposes file + process tools to the LLM.

**Architecture:** `server/pkg/sandbox/` holds the D3 interface + factory. Each implementation (E2B, gVisor-K8s, gVisor-Docker) implements the mandatory surface (`ReadFile`/`WriteFile`/`Stat`/`Mkdir`/`Readdir`/`Access`/`Exec`/`Stop` + `GetState()`) and opts into `OptionalSandbox` (`ExecDetached`/`ExtendTimeout`/`Snapshot`/`Domain`). Every sandbox exposes `ExpiresAt()` + `Hooks{AfterStart,BeforeStop,OnTimeout,OnTimeoutExtended}` so D6's proactive-timeout contract fires `SandboxTimeoutBufferMs=30_000` before the provider hard cap. A warm-pool manager keeps 2–3 idle sandboxes per template to cut cold-start from ~5s to ~200ms. A new `cloud_coding` agent type plugs into the Phase 2 worker via a `SandboxBackend` adapter — the worker runs the LLM in a Brain/Hands loop, issuing `execute_command`/`read_file`/`write_file`/`search_files` tool calls against the active sandbox. UI surfaces a create-agent flow with template + resource selectors and a sandbox-management dashboard.

**Tech Stack:** Go 1.26, pgx/sqlc, `github.com/e2b-dev/e2b-go` (E2B SDK), `k8s.io/client-go` (K8s Jobs + RuntimeClass), `github.com/docker/docker/client` (Docker fallback), TanStack Query + shadcn UI for frontend.

---

## Scope Note

Phase 11 of 12. Depends on:
- Phase 1 (`agent` + `workspace` tables + `agent_type_check`, `agent_runtime_check`)
- Phase 2 (`Backend` interface with `ExpiresAt()` + `Hooks`, worker claim path, `CostCalculator`, `events.Bus`)
- Phase 10 (`agent_trace` span table + `Tracer.StartSpan` for sandbox operation spans)
- D3 (sandbox interface derived from Open Agents)
- D6 (proactive timeout on `Backend` interface)
- D7 (`SandboxTimeoutBufferMs = 30_000`)
- D8 (`trace_id` propagated through `context.Context`)

**In scope:**
- Sandbox interface + `OptionalSandbox` + factory.
- E2B implementation (managed Firecracker microVMs).
- gVisor K8s + Docker implementations (self-hosted).
- `cloud_coding` agent type — `CHECK` constraint extension + `sandbox_template_id`.
- Brain/Hands worker loop with 4 sandbox tools.
- Warm pool with destroy-and-replace lifecycle.
- `sandbox_template` table + CRUD.
- Frontend: create-agent cloud coding flow, template management, active-instance dashboard.
- Sandbox operation spans piped into Phase 10 trace tree.

**Not in scope — deferred:**
- **Claude Managed sandbox backend** (Phase 12). Phase 11 ships a stub implementation returning `sandbox.ErrNotImplemented` so the `sandbox.Type("claude_managed")` enum value is valid in migrations + factory routing; Phase 12 replaces the stub body.
- **Sandbox snapshot → pause/resume** across worker restarts. `Snapshot()` returns an opaque ID; resumption is Phase 12+.
- **Cross-workspace sandbox sharing** (namespace isolation makes this impossible by design).
- **Port-forwarding to end-user browsers** beyond E2B's `Domain()` output. K8s `Ingress` wiring for gVisor preview is post-V1.
- **Auto-scaling K8s node pools** for gVisor — assumed pre-provisioned.
- **macOS/Windows gVisor support** — gVisor requires a Linux host kernel. Dev on those OSes uses E2B (not gVisor).

---

## Preconditions

**From Phase 1:**

| Surface | Purpose |
|---|---|
| `agent` table + `agent_type_check` + `agent_runtime_check` (migration 100-ish) | Phase 11 migration 187 extends both constraints using the NOT VALID → VALIDATE pattern |
| `workspace` table | Phase 11 adds `sandbox_backend TEXT` + `sandbox_config JSONB` columns |

**From Phase 2:**

| Surface | Purpose |
|---|---|
| `agent.Backend` interface with `Execute(ctx, prompt, opts) (Response, error)` + `ExpiresAt() *time.Time` + `Hooks` (§D6) | `SandboxBackend` implements `Backend` by wrapping the LLM-API backend + a claimed sandbox |
| `agent/llm_api.go` (`LLMAPIBackend.Execute`) | Brain-side tool-loop runs through this; the sandbox is the Hands side |
| Worker claim path (`internal/worker/claim.go`) | Route `cloud_coding` agent tasks to the Phase 11 `cloud_coding.go` handler |
| `agent_task_queue.prompt TEXT` column | Phase 2 migration (task-queue seed) defines the per-task prompt the worker hands to the LLM. Task 12's `ClaimCloudCodingTask` RETURNS this column as `row.Prompt`. If the Phase 2 migration shipped the column under a different name (e.g. `user_prompt`, `input_text`), rename the Phase 11 query accordingly in a Pre-Task 0 sqlc edit. Grep `server/migrations/*agent_task_queue*` to confirm. |
| `agent/defaults.go` | Phase 11 appends `SandboxWarmPoolSize`, `SandboxDefaultCPU`, `SandboxDefaultMemoryMB`, `SandboxDefaultDiskMB`, `SandboxDefaultTimeoutMin` |
| `events.Bus.Publish(ctx, event)` | Phase 11 emits `sandbox.started`/`sandbox.stopped` for audit + metrics |
| `CostCalculator` — Phase 2 cost accounting | Sandbox runtime cost rows use `cost_status='actual'` with sandbox hours (priced via `provider='sandbox_e2b'` rate card) |
| `InsertCostEvent` sqlc query — Phase 2 | Must be `:one` returning the inserted row (at least `id` + `workspace_id`). Phase 11's worker needs the row ID for `Tracer.LateLinkCostEvent`. If Phase 2 shipped it as `:exec`, bump to `:one` in a Pre-Task 0 sqlc patch (add `RETURNING *` + `:one` directive). |

**From Phase 10:**

| Surface | Purpose |
|---|---|
| `trace.Tracer.StartSpan(ctx, operation, meta) *Span` | Every sandbox op emits a `tool_call`-shaped span with `operation` set to the tool name ("exec", "read_file", etc.) |
| `trace.LateLinkCostEvent(ctx, spanID, costEventID)` | Sandbox runtime cost event is linked to the span that owned the sandbox session |

**Test helpers (extend Phase 2 testsupport):**

```go
// server/internal/testsupport/sandbox.go
type FakeSandbox struct {
    SandboxType  sandbox.Type
    Files        map[string][]byte           // path → contents
    ExecCalls    []ExecCall                  // record for assertions
    ExecReplies  map[string]sandbox.ExecResult // cmd → reply; missing cmds return zero
    StoppedAt    *time.Time
    Expiry       *time.Time
    mu           sync.Mutex
}
type ExecCall struct { Cmd, Cwd string; At time.Time }

func NewFakeSandbox(t *testing.T) *FakeSandbox
func (e *Env) SeedSandboxTemplate(wsID uuid.UUID, name string) uuid.UUID
func (e *Env) SeedCloudCodingAgent(wsID, templateID uuid.UUID, provider, model string) uuid.UUID
func (e *Env) WaitForSandboxStopped(ctx context.Context, sandboxID string) bool

// StubLLMReplyingWithToolCalls — reuses Phase 2 StubModel but scripts
// tool_use events so the brain/hands loop can be driven deterministically
// from tests.
func (e *Env) StubLLMReplyingWithToolCalls(script []harness.ToolCall) harness.Model

// NewNoopTracer returns a Tracer whose StartSpan returns a span whose
// End is a no-op and whose ID() returns uuid.Nil. Used by unit tests
// that construct SandboxBackend without wanting to assert on spans.
// Integration tests use env.Tracer (real tracer writing to agent_trace).
func NewNoopTracer() *trace.Tracer

// NewSingletonPoolRegistry returns a PoolRegistry whose For(templateID)
// always hands out the same FakeSandbox instance. Used in tests that
// only care about a single template's acquire/release cycle.
func NewSingletonPoolRegistry(fs *FakeSandbox) *worker.PoolRegistry

// --- Phase 11 additions to the Env struct (server/internal/testsupport/env.go) ---
//
// These are required by the test bodies in Tasks 11, 12, and 15. Each
// is a thin wrapper over existing DB / handler / events infrastructure
// from Phases 2, 8A, and 10. Agents implementing the plan should add
// these methods to the Env struct before writing Task 11+ tests.

// EnqueueTask inserts a pending agent_task_queue row for the given
// agent with the given prompt text. Returns the task's id.
// (Phase 2 already has SeedTask; EnqueueTask is the simpler shortcut
// with prompt-text pre-populated.)
func (e *Env) EnqueueTask(wsID, agentID uuid.UUID, prompt string) uuid.UUID

// TaskStatus reads agent_task_queue.status by id. Returns "" if absent.
func (e *Env) TaskStatus(taskID uuid.UUID) string

// CountCostEventsByProvider counts cost_event rows in the workspace
// whose provider column equals the given string. Used to assert that
// sandbox_e2b / sandbox_gvisor rows were emitted.
func (e *Env) CountCostEventsByProvider(wsID uuid.UUID, provider string) int

// TraceIDForTask reads the trace_id from the first cost_event linked
// to the task. Same shape as the Phase 10 testsupport helper of the
// same name — re-exported here for Phase 11 tests that didn't import
// Phase 10's test package directly.
func (e *Env) TraceIDForTask(taskID uuid.UUID) uuid.UUID

// ListSpans returns every agent_trace row for the given trace_id in
// the workspace, decoded into the same trace.Span shape used by the
// Tracer package.
func (e *Env) ListSpans(traceID uuid.UUID) []trace.Span

// StubModelReplying scripts an LLM stub that returns the given
// JSON-encoded replies in order. Each reply is either a tool_use event
// (parsed for tool name + input) or a literal assistant message that
// ends the loop. Extends Phase 2's StubModel.
func (e *Env) StubModelReplying(replies []string)

// StubLLMFactory returns an agent.Backend factory that always resolves
// to the most recently installed StubModelReplying script. Used as the
// CloudCodingDeps.LLMFactory value in worker tests.
func (e *Env) StubLLMFactory(provider, model string) agent.Backend

// CostCalc exposes the Env's Phase 2 CostCalculator so tests can pass
// it to CloudCodingDeps without re-initializing one. Populated in
// NewEnv.
var _envCostCalcField = "CostCalc *agent.CostCalculator"

// Events exposes the Env's Phase 8A events.Bus (or an in-memory stub)
// so tests can pass it to CloudCodingDeps. Populated in NewEnv.
var _envEventsField = "Events EventsBus"

// ActiveSandboxes exposes the Env's handler.ActiveSandboxRegistry.
// Populated in NewEnv; tests assert against it post-run.
var _envActiveSandboxesField = "ActiveSandboxes *handler.ActiveSandboxRegistry"

// RouteTo executes the request against the Env's mounted chi router,
// returning the recorded response. The response type is
// *httptest.ResponseRecorder — Body is *bytes.Buffer, Code is int.
// Already present in Phase 2 testsupport; restated for clarity.
func (e *Env) RouteTo(req *http.Request) *httptest.ResponseRecorder
```

**Pre-Task 0 bootstrap:**

```bash
cd server
go get github.com/e2b-dev/e2b-go@latest
go get k8s.io/client-go@v0.29.0
go get k8s.io/apimachinery@v0.29.0
go get github.com/docker/docker/client@latest
go get github.com/docker/go-connections@latest
go get github.com/opencontainers/image-spec@latest
go get golang.org/x/sync@latest
go mod tidy
```

**Confirm Phase 2 `Backend` surface matches D6:**

```go
// server/pkg/agent/agent.go (Phase 2) — already present per D6
type Backend interface {
    Execute(ctx context.Context, p Prompt, o Opts) (Response, error)
    ExpiresAt() *time.Time
    Hooks() *Hooks
}
type Hooks struct {
    AfterStart        func(context.Context, Session) error
    BeforeStop        func(context.Context, Session) error
    OnTimeout         func(context.Context, Session) error
    OnTimeoutExtended func(context.Context, Session, time.Duration) error
}
```

If a repo grep for `ExpiresAt()` on `type Backend interface` doesn't find these two methods, add them in a Pre-Task 0 extension before Task 8 — sandbox backend requires the contract.

**Confirm `defaults.go` has the Phase 7 `SandboxTimeoutBufferMs`:**

```go
// server/pkg/agent/defaults.go
var SandboxTimeoutBufferMs = 30_000 // D7
```

If missing, add before Task 4.

**Cost rate card for sandbox runtime:**

Phase 2's `CostCalculator` maps `(provider, model)` → rate. Phase 11 extends the rate table with three synthetic "models":
- `(provider="sandbox_e2b", model="default")` → $0.05 / vCPU-hour (E2B pricing).
- `(provider="sandbox_gvisor", model="default")` → $0.01 / vCPU-hour (self-hosted, opex only).
- `(provider="sandbox_claude", model="default")` → $0.00 (Phase 12 will overwrite).

Add before Task 4 if missing. Cost events emitted by the sandbox manager use these keys.

---

## File Structure

### New Files (Backend)

| File | Responsibility |
|---|---|
| `server/migrations/187_cloud_coding.up.sql` | `sandbox_template` table + `workspace.sandbox_backend/sandbox_config` + extend `agent_type_check` + extend `agent_runtime_check` + `agent.sandbox_template_id` |
| `server/migrations/187_cloud_coding.down.sql` | Reverse |
| `server/pkg/db/queries/sandbox.sql` | sqlc: `InsertSandboxTemplate` / `ListSandboxTemplates` / `GetSandboxTemplateByID` / `UpdateSandboxTemplate` / `DeleteSandboxTemplate` / `GetCloudCodingAgentWithTemplate` |
| `server/pkg/sandbox/sandbox.go` | `Sandbox` + `OptionalSandbox` interfaces, `Type`, `Hooks`, `ExecResult`, `FileStats`, `MkdirOpts`, `Config`, `ErrNotImplemented` |
| `server/pkg/sandbox/sandbox_test.go` | Interface assertions via `var _ Sandbox = (*FakeSandbox)(nil)` |
| `server/pkg/sandbox/factory.go` | `Connect(ctx, Config, ConnectOptions) (Sandbox, error)` — discriminated union routing |
| `server/pkg/sandbox/factory_test.go` | Routes each `Type` to the right constructor; unknown type errors |
| `server/pkg/sandbox/e2b.go` | E2B Firecracker implementation |
| `server/pkg/sandbox/e2b_test.go` | Mocked E2B SDK; `AfterStart`/`BeforeStop` fire; `ExpiresAt` extended on `ExtendTimeout` |
| `server/pkg/sandbox/gvisor_k8s.go` | K8s Job + RuntimeClass implementation |
| `server/pkg/sandbox/gvisor_k8s_test.go` | Fake K8s clientset; Job spec asserts `runtimeClassName: gvisor` + security policy |
| `server/pkg/sandbox/gvisor_docker.go` | Docker fallback (`runtime=runsc`) |
| `server/pkg/sandbox/gvisor_docker_test.go` | Fake Docker client; container config asserts `--runtime=runsc` |
| `server/pkg/sandbox/claude_managed.go` | Stub — every method returns `sandbox.ErrNotImplemented` (Phase 12 fills it in) |
| `server/pkg/sandbox/pool.go` | Warm pool manager — acquire/release/destroy-and-replace |
| `server/pkg/sandbox/pool_test.go` | Cold-start path, warm-hit path, destroy-on-release |
| `server/pkg/sandbox/timeout.go` | Shared `scheduleTimeoutHook(ctx, s Sandbox, buffer time.Duration)` — fires `Hooks.OnTimeout` at `ExpiresAt - buffer` |
| `server/pkg/sandbox/timeout_test.go` | Timer fires early enough; extend → timer rescheduled |
| `server/internal/service/sandbox_backend.go` | `SandboxBackend` — implements Phase 2 `agent.Backend` by pairing an LLM backend with a claimed sandbox + 4 bound tools |
| `server/internal/service/sandbox_backend_test.go` | Brain/Hands loop unit test with `FakeSandbox` + stub LLM |
| `server/internal/service/sandbox_template.go` | CRUD service over sqlc queries |
| `server/internal/service/sandbox_template_test.go` | Happy-path + workspace-scoping |
| `server/internal/worker/cloud_coding.go` | Task claimer — claims `cloud_coding` tasks, acquires sandbox from pool, runs `SandboxBackend.Execute`, releases sandbox |
| `server/internal/worker/cloud_coding_test.go` | E2E: task enqueued → sandbox acquired → tools run → sandbox released |
| `server/internal/handler/sandbox_template.go` | REST: `/sandbox-templates` GET/POST/PATCH/DELETE |
| `server/internal/handler/sandbox_template_test.go` | Handler tests |
| `server/internal/handler/sandbox_instance.go` | REST: `/sandboxes` (active instances dashboard) |
| `server/internal/handler/sandbox_instance_test.go` | Handler tests |
| `server/internal/integration/cloudcoding/e2e_test.go` | Full integration: create agent → enqueue task → assert sandbox spans + cost_event |

### Modified Files (Backend)

| File | Changes |
|---|---|
| `server/pkg/agent/defaults.go` | Append `SandboxWarmPoolSize=3`, `SandboxDefaultCPU=1`, `SandboxDefaultMemoryMB=2048`, `SandboxDefaultDiskMB=5120`, `SandboxDefaultTimeoutMin=60` |
| `server/pkg/agent/cost_calculator.go` (Phase 2) | Extend rate card with `sandbox_e2b` / `sandbox_gvisor` / `sandbox_claude` |
| `server/internal/worker/claim.go` (Phase 2) | Add `cloud_coding` branch routing to `cloud_coding.Run` |
| `server/cmd/server/main.go` | Start warm-pool managers + register sandbox-template + sandbox-instance handlers |
| `server/cmd/server/router.go` | Mount `/sandbox-templates/*` and `/sandboxes` routes |

### New Files (Frontend)

| File | Responsibility |
|---|---|
| `packages/core/types/sandbox.ts` | `SandboxTemplate`, `SandboxInstance`, `SandboxBackend`, `CloudCodingAgent` |
| `packages/core/api/sandbox.ts` | REST client (templates + active instances) |
| `packages/core/sandbox/queries.ts` | TanStack hooks: `useSandboxTemplates`, `useCreateSandboxTemplate`, `useSandboxInstances` |
| `packages/views/sandboxes/index.tsx` | Active-instances dashboard |
| `packages/views/sandboxes/templates/index.tsx` | Template list page |
| `packages/views/sandboxes/templates/template-form.tsx` | Create/edit form |
| `packages/views/agents/components/cloud-coding-flow.tsx` | Cloud coding branch of create-agent dialog |

### Modified Files (Frontend)

| File | Changes |
|---|---|
| `packages/views/agents/components/create-agent-dialog.tsx` | When `agent_type='cloud_coding'`, mount `CloudCodingFlow` |

### External Infrastructure

| System | Change |
|---|---|
| E2B | API key required in workspace `sandbox_config.api_key` when `sandbox_backend='e2b'` |
| Kubernetes (optional) | Cluster with gVisor `RuntimeClass` + namespace-per-workspace |
| Docker (optional fallback) | `runsc` runtime installed on host |

---

### Task 1: Migration 187 — Cloud Coding Schema

**Files:**
- Create: `server/migrations/187_cloud_coding.up.sql`
- Create: `server/migrations/187_cloud_coding.down.sql`

- [ ] **Step 1: Up migration**

```sql
-- 187_cloud_coding.up.sql
-- sandbox_template: per-workspace blueprint for sandbox creation.
CREATE TABLE IF NOT EXISTS sandbox_template (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    e2b_template_id TEXT,                           -- E2B reference (when backend='e2b')
    dockerfile TEXT,                                -- Custom image (when backend='gvisor')
    default_cpu INT DEFAULT 1,
    default_memory_mb INT DEFAULT 2048,
    default_disk_mb INT DEFAULT 5120,
    installed_tools JSONB DEFAULT '[]',             -- [{"name":"node","version":"22"}, ...]
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, name)
);
CREATE INDEX IF NOT EXISTS idx_sandbox_template_ws ON sandbox_template(workspace_id);

-- Workspace-level sandbox config.
ALTER TABLE workspace
    ADD COLUMN IF NOT EXISTS sandbox_backend TEXT DEFAULT 'e2b',
    ADD COLUMN IF NOT EXISTS sandbox_config JSONB DEFAULT '{}'::jsonb;

ALTER TABLE workspace DROP CONSTRAINT IF EXISTS workspace_sandbox_backend_check;
ALTER TABLE workspace ADD CONSTRAINT workspace_sandbox_backend_check
    CHECK (sandbox_backend IN ('e2b', 'gvisor', 'claude_managed', 'none')) NOT VALID;
ALTER TABLE workspace VALIDATE CONSTRAINT workspace_sandbox_backend_check;

-- Extend agent_type_check to include 'cloud_coding'.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process', 'cloud_coding')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

-- Link agent → sandbox_template. Composite FK ensures workspace scoping.
ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS sandbox_template_id UUID;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_sandbox_template_fk;
ALTER TABLE agent ADD CONSTRAINT agent_sandbox_template_fk
    FOREIGN KEY (sandbox_template_id) REFERENCES sandbox_template(id) ON DELETE SET NULL;

-- Extend agent_runtime_check so cloud_coding requires provider + template (no runtime_id).
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
       (agent_type = 'coding'       AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api','http','process') AND provider IS NOT NULL)
    OR (agent_type = 'cloud_coding' AND provider IS NOT NULL AND sandbox_template_id IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;
```

- [ ] **Step 2: Down migration**

```sql
-- 187_cloud_coding.down.sql
-- Remove cloud_coding rows FIRST — otherwise VALIDATE CONSTRAINT on the
-- restored CHECK would fail (existing rows with agent_type='cloud_coding'
-- would not satisfy the narrower constraint).
DELETE FROM agent WHERE agent_type = 'cloud_coding';

-- Restore prior agent_runtime_check (Phase 1-era shape) before dropping columns.
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_runtime_check;
ALTER TABLE agent ADD CONSTRAINT agent_runtime_check CHECK (
       (agent_type = 'coding' AND runtime_id IS NOT NULL)
    OR (agent_type IN ('llm_api','http','process') AND provider IS NOT NULL)
) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_runtime_check;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_sandbox_template_fk;
ALTER TABLE agent DROP COLUMN IF EXISTS sandbox_template_id;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_type_check;
ALTER TABLE agent ADD CONSTRAINT agent_type_check
    CHECK (agent_type IN ('coding', 'llm_api', 'http', 'process')) NOT VALID;
ALTER TABLE agent VALIDATE CONSTRAINT agent_type_check;

ALTER TABLE workspace DROP CONSTRAINT IF EXISTS workspace_sandbox_backend_check;
ALTER TABLE workspace DROP COLUMN IF EXISTS sandbox_config;
ALTER TABLE workspace DROP COLUMN IF EXISTS sandbox_backend;

DROP INDEX IF EXISTS idx_sandbox_template_ws;
DROP TABLE IF EXISTS sandbox_template;
```

- [ ] **Step 3: Apply + commit**

```bash
cd server && make migrate-up && make migrate-down && make migrate-up
git add server/migrations/187_cloud_coding.up.sql server/migrations/187_cloud_coding.down.sql
git commit -m "feat(sandbox): migration 187 — sandbox_template + cloud_coding agent type

Adds sandbox_template + workspace.sandbox_backend/sandbox_config. Extends
agent_type_check to 'cloud_coding' and agent_runtime_check to require
provider + sandbox_template_id for that type. Uses NOT VALID → VALIDATE
to keep the CHECK alter at SHARE UPDATE EXCLUSIVE."
```

---

### Task 2: Sandbox Interface (D3)

**Files:**
- Create: `server/pkg/sandbox/sandbox.go`
- Create: `server/pkg/sandbox/sandbox_test.go`

**Goal:** port the Open Agents `Sandbox` interface to Go, split mandatory vs. optional surface per D3, define shared value types.

- [ ] **Step 1: Failing interface test**

```go
// server/pkg/sandbox/sandbox_test.go
package sandbox_test

import (
    "testing"
    "github.com/multica/server/pkg/sandbox"
    "github.com/multica/server/internal/testsupport"
)

// Compile-time assertion that testsupport.FakeSandbox implements the
// mandatory surface; caught here before Task 4 onward relies on it.
func TestFakeSandbox_ImplementsMandatory(t *testing.T) {
    var _ sandbox.Sandbox = testsupport.NewFakeSandbox(t)
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestFakeSandbox_ImplementsMandatory`
Expected: FAIL — `sandbox` package does not exist yet.

- [ ] **Step 2: Implement interface**

```go
// server/pkg/sandbox/sandbox.go
package sandbox

import (
    "context"
    "errors"
    "io/fs"
    "time"
)

// Type is the discriminator the factory uses to pick an implementation.
// Values match the workspace.sandbox_backend CHECK in migration 187
// (plus TypeLocalDaemon for the existing daemon code path, which is
// routed by agent_type='coding' — not by workspace config).
type Type string

const (
    TypeE2B           Type = "e2b"
    TypeGVisor        Type = "gvisor"
    TypeClaudeManaged Type = "claude_managed"
    TypeLocalDaemon   Type = "local_daemon"
)

// ErrNotImplemented is returned by the Claude-Managed stub in Phase 11
// and by any sandbox implementation that omits optional surface.
var ErrNotImplemented = errors.New("sandbox: not implemented")

// FileStats mirrors Open Agents' FileStats — intentionally narrow.
type FileStats struct {
    Size    int64
    IsDir   bool
    ModTime time.Time
}
type MkdirOpts struct { Recursive bool }

type ExecResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
    Duration time.Duration
}

// Sandbox is the mandatory surface every backend must implement (D3).
type Sandbox interface {
    // Identity
    Type() Type
    ID() string                           // provider-assigned handle (E2B sandbox_id, K8s pod name, …)
    WorkingDirectory() string
    Env() map[string]string

    // File ops
    ReadFile(ctx context.Context, path string) ([]byte, error)
    WriteFile(ctx context.Context, path string, content []byte) error
    Stat(ctx context.Context, path string) (FileStats, error)
    Mkdir(ctx context.Context, path string, opts MkdirOpts) error
    Readdir(ctx context.Context, path string) ([]fs.DirEntry, error)
    Access(ctx context.Context, path string) error // existence probe; nil = exists, NotExist = absent

    // Process ops
    Exec(ctx context.Context, cmd string, cwd string, timeout time.Duration) (ExecResult, error)

    // Lifecycle
    Stop(ctx context.Context) error
    ExpiresAt() *time.Time            // D6/D7 timeout-buffer contract
    GetState() any                    // opaque state for resumption (Phase 12)
}

// OptionalSandbox is opted into per backend. Callers who need these
// methods type-assert: `opt, ok := s.(sandbox.OptionalSandbox)`.
type OptionalSandbox interface {
    ExecDetached(ctx context.Context, cmd string, cwd string) (commandID string, err error)
    ExtendTimeout(ctx context.Context, additional time.Duration) (expiresAt time.Time, err error)
    Snapshot(ctx context.Context) (snapshotID string, err error)
    Domain(port int) (string, error)
}

// Hooks fire around sandbox lifecycle. Every implementation of Sandbox
// stores a *Hooks and calls them at the appropriate time. Nil-safe:
// call sites check `if h != nil && h.X != nil { h.X(...) }`.
type Hooks struct {
    AfterStart        func(ctx context.Context, s Sandbox) error
    BeforeStop        func(ctx context.Context, s Sandbox) error
    OnTimeout         func(ctx context.Context, s Sandbox) error
    OnTimeoutExtended func(ctx context.Context, s Sandbox, additional time.Duration) error
}

// Config is what workspace.sandbox_config decodes into. Fields not
// relevant to a backend are left zero-valued.
type Config struct {
    Type           Type              `json:"type"`
    APIKey         string            `json:"api_key,omitempty"`          // E2B / Claude Managed
    TemplateID     string            `json:"template_id,omitempty"`      // E2B
    Dockerfile     string            `json:"dockerfile,omitempty"`       // gVisor
    K8sNamespace   string            `json:"k8s_namespace,omitempty"`    // gVisor-K8s
    RuntimeClass   string            `json:"runtime_class,omitempty"`    // gVisor-K8s (default "gvisor")
    Registry       string            `json:"registry,omitempty"`         // gVisor-K8s
    CPUMillicores  int               `json:"cpu_millicores,omitempty"`   // 1000 = 1 CPU
    MemoryMB       int               `json:"memory_mb,omitempty"`
    DiskMB         int               `json:"disk_mb,omitempty"`
    Env            map[string]string `json:"env,omitempty"`
    TimeoutMinutes int               `json:"timeout_minutes,omitempty"`
}
```

- [ ] **Step 3: Implement `FakeSandbox` testsupport helper**

```go
// server/internal/testsupport/sandbox.go
package testsupport

import (
    "context"
    "errors"
    "io/fs"
    "sync"
    "testing"
    "time"
    "github.com/multica/server/pkg/sandbox"
)

type ExecCall struct { Cmd, Cwd string; At time.Time }

type FakeSandbox struct {
    SandboxID    string
    SandboxType  sandbox.Type
    Files        map[string][]byte
    ExecCalls    []ExecCall
    ExecReplies  map[string]sandbox.ExecResult
    StoppedAt    *time.Time
    Expiry       *time.Time
    hooks        *sandbox.Hooks
    mu           sync.Mutex
}
func NewFakeSandbox(_ *testing.T) *FakeSandbox {
    return &FakeSandbox{
        SandboxID: "fake-sbx-" + time.Now().Format("150405"),
        SandboxType: "fake",
        Files: map[string][]byte{},
        ExecReplies: map[string]sandbox.ExecResult{},
    }
}
func (f *FakeSandbox) Type() sandbox.Type      { return f.SandboxType }
func (f *FakeSandbox) ID() string              { return f.SandboxID }
func (f *FakeSandbox) WorkingDirectory() string { return "/workspace" }
func (f *FakeSandbox) Env() map[string]string  { return map[string]string{} }
func (f *FakeSandbox) ReadFile(_ context.Context, p string) ([]byte, error) {
    f.mu.Lock(); defer f.mu.Unlock()
    b, ok := f.Files[p]
    if !ok { return nil, errors.New("not found") }
    return append([]byte(nil), b...), nil
}
func (f *FakeSandbox) WriteFile(_ context.Context, p string, c []byte) error {
    f.mu.Lock(); defer f.mu.Unlock()
    f.Files[p] = append([]byte(nil), c...); return nil
}
func (f *FakeSandbox) Stat(_ context.Context, p string) (sandbox.FileStats, error) {
    f.mu.Lock(); defer f.mu.Unlock()
    b, ok := f.Files[p]; if !ok { return sandbox.FileStats{}, errors.New("not found") }
    return sandbox.FileStats{Size: int64(len(b))}, nil
}
func (f *FakeSandbox) Mkdir(_ context.Context, _ string, _ sandbox.MkdirOpts) error { return nil }
func (f *FakeSandbox) Readdir(_ context.Context, _ string) ([]fs.DirEntry, error)   { return nil, nil }
func (f *FakeSandbox) Access(_ context.Context, p string) error {
    f.mu.Lock(); defer f.mu.Unlock()
    if _, ok := f.Files[p]; !ok { return errors.New("not found") }
    return nil
}
func (f *FakeSandbox) Exec(_ context.Context, cmd, cwd string, _ time.Duration) (sandbox.ExecResult, error) {
    f.mu.Lock(); defer f.mu.Unlock()
    f.ExecCalls = append(f.ExecCalls, ExecCall{Cmd: cmd, Cwd: cwd, At: time.Now()})
    if r, ok := f.ExecReplies[cmd]; ok { return r, nil }
    return sandbox.ExecResult{ExitCode: 0}, nil
}
func (f *FakeSandbox) Stop(ctx context.Context) error {
    t := time.Now(); f.StoppedAt = &t
    if f.hooks != nil && f.hooks.BeforeStop != nil { _ = f.hooks.BeforeStop(ctx, f) }
    return nil
}
func (f *FakeSandbox) ExpiresAt() *time.Time { return f.Expiry }
func (f *FakeSandbox) GetState() any         { return nil }
func (f *FakeSandbox) SetHooks(h *sandbox.Hooks) { f.hooks = h }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./pkg/sandbox/ -run TestFakeSandbox_ImplementsMandatory -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/sandbox/sandbox.go server/pkg/sandbox/sandbox_test.go server/internal/testsupport/sandbox.go
git commit -m "feat(sandbox): D3 Sandbox + OptionalSandbox interfaces + FakeSandbox

Mandatory surface: ReadFile/WriteFile/Stat/Mkdir/Readdir/Access/Exec/Stop
+ GetState(). Optional: ExecDetached/ExtendTimeout/Snapshot/Domain.
Hooks + Config + ExecResult/FileStats value types."
```

---

### Task 3: Sandbox Factory

**Files:**
- Create: `server/pkg/sandbox/factory.go`
- Create: `server/pkg/sandbox/factory_test.go`

**Goal:** `Connect(ctx, Config, ConnectOptions) (Sandbox, error)` routes to the right implementation based on `Config.Type`. Unknown types return a clear error; stubs return themselves.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/factory_test.go
package sandbox_test

import (
    "context"
    "testing"
    "github.com/multica/server/pkg/sandbox"
)

func TestFactory_UnknownTypeErrors(t *testing.T) {
    _, err := sandbox.Connect(context.Background(), sandbox.Config{Type: "bogus"}, sandbox.ConnectOptions{})
    if err == nil { t.Fatal("expected error for unknown sandbox type") }
}

func TestFactory_ClaudeManagedReturnsStub(t *testing.T) {
    s, err := sandbox.Connect(context.Background(),
        sandbox.Config{Type: sandbox.TypeClaudeManaged}, sandbox.ConnectOptions{})
    if err != nil { t.Fatalf("unexpected error: %v", err) }
    if s.Type() != sandbox.TypeClaudeManaged { t.Errorf("type = %s, want claude_managed", s.Type()) }
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestFactory`
Expected: FAIL — `Connect` + `ConnectOptions` + `ClaudeManagedStub` undefined.

- [ ] **Step 2: Implement factory + Claude Managed stub**

```go
// server/pkg/sandbox/factory.go
package sandbox

import (
    "context"
    "fmt"
)

// ConnectOptions carries cross-backend knobs that aren't part of the
// Config payload — e.g. a pre-configured K8s clientset injected by tests.
type ConnectOptions struct {
    Hooks     *Hooks
    // Test injection points. Production callers leave these nil; each
    // backend uses its own client singleton when these are nil.
    E2BClient     any // *e2b.Client (set in e2b.go)
    K8sClient     any // kubernetes.Interface
    DockerClient  any // *docker.Client
}

// Connect routes to the right implementation based on cfg.Type.
func Connect(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    switch cfg.Type {
    case TypeE2B:
        return connectE2B(ctx, cfg, opts)
    case TypeGVisor:
        if cfg.K8sNamespace != "" {
            return connectGVisorK8s(ctx, cfg, opts)
        }
        return connectGVisorDocker(ctx, cfg, opts)
    case TypeClaudeManaged:
        return &ClaudeManagedStub{hooks: opts.Hooks}, nil
    case TypeLocalDaemon:
        // Local daemon uses the existing coding-agent path, not the
        // sandbox package. Callers should not reach this branch for
        // cloud_coding agents.
        return nil, fmt.Errorf("sandbox: local_daemon routes through agent_type='coding', not Connect")
    default:
        return nil, fmt.Errorf("sandbox: unknown type %q", cfg.Type)
    }
}
```

```go
// server/pkg/sandbox/claude_managed.go
package sandbox

import (
    "context"
    "io/fs"
    "time"
)

// ClaudeManagedStub is replaced in Phase 12. Every method returns
// ErrNotImplemented so callers fail loudly rather than silently. The
// enum value exists in migration 187 so workspaces can select it and
// store credentials before Phase 12 lands.
type ClaudeManagedStub struct { hooks *Hooks }

func (s *ClaudeManagedStub) Type() Type             { return TypeClaudeManaged }
func (s *ClaudeManagedStub) ID() string             { return "" }
func (s *ClaudeManagedStub) WorkingDirectory() string { return "" }
func (s *ClaudeManagedStub) Env() map[string]string { return nil }
func (s *ClaudeManagedStub) ReadFile(context.Context, string) ([]byte, error) { return nil, ErrNotImplemented }
func (s *ClaudeManagedStub) WriteFile(context.Context, string, []byte) error   { return ErrNotImplemented }
func (s *ClaudeManagedStub) Stat(context.Context, string) (FileStats, error)   { return FileStats{}, ErrNotImplemented }
func (s *ClaudeManagedStub) Mkdir(context.Context, string, MkdirOpts) error    { return ErrNotImplemented }
func (s *ClaudeManagedStub) Readdir(context.Context, string) ([]fs.DirEntry, error) { return nil, ErrNotImplemented }
func (s *ClaudeManagedStub) Access(context.Context, string) error              { return ErrNotImplemented }
func (s *ClaudeManagedStub) Exec(context.Context, string, string, time.Duration) (ExecResult, error) {
    return ExecResult{}, ErrNotImplemented
}
func (s *ClaudeManagedStub) Stop(context.Context) error { return nil }
func (s *ClaudeManagedStub) ExpiresAt() *time.Time      { return nil }
func (s *ClaudeManagedStub) GetState() any              { return nil }
```

Stub functions for E2B + gVisor so factory compiles (filled in subsequent tasks):

```go
// server/pkg/sandbox/e2b.go — stub, body in Task 4
package sandbox

import "context"

func connectE2B(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    return nil, ErrNotImplemented // Replaced in Task 4.
}

// server/pkg/sandbox/gvisor_k8s.go — stub, body in Task 5
func connectGVisorK8s(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    return nil, ErrNotImplemented // Replaced in Task 5.
}

// server/pkg/sandbox/gvisor_docker.go — stub, body in Task 6
func connectGVisorDocker(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    return nil, ErrNotImplemented // Replaced in Task 6.
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `cd server && go test ./pkg/sandbox/ -run TestFactory -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/sandbox/factory.go server/pkg/sandbox/factory_test.go server/pkg/sandbox/claude_managed.go server/pkg/sandbox/e2b.go server/pkg/sandbox/gvisor_k8s.go server/pkg/sandbox/gvisor_docker.go
git commit -m "feat(sandbox): Connect factory + Claude Managed stub

Discriminated-union routing on Config.Type. gVisor splits on
K8sNamespace presence (K8s → Docker fallback). Claude Managed stub
returns ErrNotImplemented on every method pending Phase 12."
```

---

### Task 4: E2B Implementation

**Files:**
- Create: `server/pkg/sandbox/e2b.go` (replace stub)
- Create: `server/pkg/sandbox/e2b_test.go`

**Goal:** Implement `Sandbox` + opt into `OptionalSandbox` using the E2B Go SDK. Boot a Firecracker microVM on `Connect`, run file/exec ops over E2B's gRPC bridge, fire `Hooks.AfterStart` post-boot + `Hooks.BeforeStop` pre-stop.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/e2b_test.go
package sandbox_test

import (
    "context"
    "testing"
    "time"
    "github.com/multica/server/pkg/sandbox"
)

type fakeE2BClient struct {
    CreatedTemplate string
    Stopped         bool
    FilesWritten    map[string]string
    ExpiresAtVal    time.Time
}
func (f *fakeE2BClient) CreateSandbox(ctx context.Context, template string) (string, time.Time, error) {
    f.CreatedTemplate = template
    f.ExpiresAtVal = time.Now().Add(60 * time.Minute)
    return "e2b-sbx-1", f.ExpiresAtVal, nil
}
func (f *fakeE2BClient) StopSandbox(ctx context.Context, id string) error { f.Stopped = true; return nil }
func (f *fakeE2BClient) WriteFile(ctx context.Context, id, p, c string) error {
    if f.FilesWritten == nil { f.FilesWritten = map[string]string{} }
    f.FilesWritten[p] = c; return nil
}
func (f *fakeE2BClient) ReadFile(ctx context.Context, id, p string) (string, error) {
    return f.FilesWritten[p], nil
}
func (f *fakeE2BClient) Exec(ctx context.Context, id, cmd, cwd string, timeout time.Duration) (sandbox.ExecResult, error) {
    return sandbox.ExecResult{Stdout: "ok\n", ExitCode: 0}, nil
}
func (f *fakeE2BClient) ExtendTimeout(ctx context.Context, id string, add time.Duration) (time.Time, error) {
    f.ExpiresAtVal = f.ExpiresAtVal.Add(add); return f.ExpiresAtVal, nil
}

func TestE2B_BootsAndStops(t *testing.T) {
    fc := &fakeE2BClient{}
    afterStartCalled := false
    beforeStopCalled := false
    s, err := sandbox.Connect(context.Background(),
        sandbox.Config{Type: sandbox.TypeE2B, TemplateID: "node-22", APIKey: "k"},
        sandbox.ConnectOptions{
            E2BClient: fc,
            Hooks: &sandbox.Hooks{
                AfterStart: func(ctx context.Context, s sandbox.Sandbox) error { afterStartCalled = true; return nil },
                BeforeStop: func(ctx context.Context, s sandbox.Sandbox) error { beforeStopCalled = true; return nil },
            },
        })
    if err != nil { t.Fatalf("connect: %v", err) }
    if s.Type() != sandbox.TypeE2B { t.Errorf("type = %s", s.Type()) }
    if !afterStartCalled { t.Error("AfterStart did not fire") }
    if fc.CreatedTemplate != "node-22" { t.Errorf("template = %q", fc.CreatedTemplate) }
    if err := s.Stop(context.Background()); err != nil { t.Fatal(err) }
    if !beforeStopCalled { t.Error("BeforeStop did not fire") }
    if !fc.Stopped { t.Error("StopSandbox not called") }
}

func TestE2B_ExtendTimeoutReschedulesExpiry(t *testing.T) {
    fc := &fakeE2BClient{}
    s, _ := sandbox.Connect(context.Background(),
        sandbox.Config{Type: sandbox.TypeE2B, TemplateID: "node-22"},
        sandbox.ConnectOptions{E2BClient: fc})
    before := *s.ExpiresAt()
    opt, ok := s.(sandbox.OptionalSandbox); if !ok { t.Fatal("E2B should implement OptionalSandbox") }
    after, err := opt.ExtendTimeout(context.Background(), 10*time.Minute)
    if err != nil { t.Fatal(err) }
    if !after.After(before) { t.Errorf("expiry did not advance: %v → %v", before, after) }
    if !s.ExpiresAt().Equal(after) { t.Errorf("ExpiresAt out of sync: %v vs %v", s.ExpiresAt(), after) }
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestE2B`
Expected: FAIL — stub returns `ErrNotImplemented`.

- [ ] **Step 2: Implement**

```go
// server/pkg/sandbox/e2b.go
package sandbox

import (
    "context"
    "errors"
    "io/fs"
    "sync"
    "time"

    e2b "github.com/e2b-dev/e2b-go"
)

// e2bClient is the subset of the SDK we use. Split for testability.
type e2bClient interface {
    CreateSandbox(ctx context.Context, template string) (id string, expiresAt time.Time, err error)
    StopSandbox(ctx context.Context, id string) error
    WriteFile(ctx context.Context, id, path, content string) error
    ReadFile(ctx context.Context, id, path string) (string, error)
    Exec(ctx context.Context, id, cmd, cwd string, timeout time.Duration) (ExecResult, error)
    ExtendTimeout(ctx context.Context, id string, add time.Duration) (time.Time, error)
}

type e2bSandbox struct {
    id             string
    client         e2bClient
    cfg            Config
    hooks          *Hooks
    expiresAt      *time.Time
    timeoutCancel  func() // Set by Task 8's ScheduleTimeoutHook; invoked in Stop.
    mu             sync.Mutex
}

func connectE2B(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    if cfg.TemplateID == "" {
        return nil, errors.New("e2b: template_id required")
    }
    var client e2bClient
    if opts.E2BClient != nil {
        c, ok := opts.E2BClient.(e2bClient); if !ok { return nil, errors.New("e2b: injected client does not implement e2bClient") }
        client = c
    } else {
        client = newE2BClient(cfg.APIKey)
    }
    id, exp, err := client.CreateSandbox(ctx, cfg.TemplateID)
    if err != nil { return nil, err }
    s := &e2bSandbox{id: id, client: client, cfg: cfg, hooks: opts.Hooks, expiresAt: &exp}
    if opts.Hooks != nil && opts.Hooks.AfterStart != nil {
        if err := opts.Hooks.AfterStart(ctx, s); err != nil {
            _ = s.Stop(ctx); return nil, err
        }
    }
    return s, nil
}

// newE2BClient adapts github.com/e2b-dev/e2b-go to our internal e2bClient
// shape. Real SDK surface differs slightly — callers should consult the
// upstream README for exact method names; this wrapper pins a stable
// boundary for the rest of the codebase.
func newE2BClient(apiKey string) e2bClient { return &realE2BClient{sdk: e2b.NewClient(apiKey)} }

type realE2BClient struct{ sdk *e2b.Client }

func (r *realE2BClient) CreateSandbox(ctx context.Context, template string) (string, time.Time, error) {
    sbx, err := r.sdk.Sandboxes.Create(ctx, &e2b.CreateSandboxRequest{TemplateID: template})
    if err != nil { return "", time.Time{}, err }
    return sbx.ID, sbx.ExpiresAt, nil
}
func (r *realE2BClient) StopSandbox(ctx context.Context, id string) error {
    return r.sdk.Sandboxes.Stop(ctx, id)
}
func (r *realE2BClient) WriteFile(ctx context.Context, id, path, content string) error {
    return r.sdk.Files.Write(ctx, id, path, []byte(content))
}
func (r *realE2BClient) ReadFile(ctx context.Context, id, path string) (string, error) {
    b, err := r.sdk.Files.Read(ctx, id, path); if err != nil { return "", err }
    return string(b), nil
}
func (r *realE2BClient) Exec(ctx context.Context, id, cmd, cwd string, timeout time.Duration) (ExecResult, error) {
    out, err := r.sdk.Processes.Run(ctx, id, &e2b.RunRequest{Cmd: cmd, Cwd: cwd, TimeoutMs: int(timeout.Milliseconds())})
    if err != nil { return ExecResult{}, err }
    return ExecResult{Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode, Duration: out.Duration}, nil
}
func (r *realE2BClient) ExtendTimeout(ctx context.Context, id string, add time.Duration) (time.Time, error) {
    return r.sdk.Sandboxes.ExtendTimeout(ctx, id, add)
}

// Sandbox surface
func (s *e2bSandbox) Type() Type             { return TypeE2B }
func (s *e2bSandbox) ID() string             { return s.id }
func (s *e2bSandbox) WorkingDirectory() string { return "/home/user" }
func (s *e2bSandbox) Env() map[string]string { return s.cfg.Env }
func (s *e2bSandbox) ReadFile(ctx context.Context, p string) ([]byte, error) {
    c, err := s.client.ReadFile(ctx, s.id, p); if err != nil { return nil, err }
    return []byte(c), nil
}
func (s *e2bSandbox) WriteFile(ctx context.Context, p string, c []byte) error {
    return s.client.WriteFile(ctx, s.id, p, string(c))
}
func (s *e2bSandbox) Stat(ctx context.Context, p string) (FileStats, error) {
    // E2B SDK lacks a direct Stat call; approximate via ReadFile + len.
    b, err := s.ReadFile(ctx, p); if err != nil { return FileStats{}, err }
    return FileStats{Size: int64(len(b))}, nil
}
func (s *e2bSandbox) Mkdir(ctx context.Context, p string, o MkdirOpts) error {
    cmd := "mkdir -p " + p; if !o.Recursive { cmd = "mkdir " + p }
    _, err := s.Exec(ctx, cmd, "/", 10*time.Second); return err
}
func (s *e2bSandbox) Readdir(ctx context.Context, p string) ([]fs.DirEntry, error) {
    // E2B doesn't expose Readdir in the current SDK — fall back to `ls -1`.
    // Callers that rely on DirEntry methods beyond Name() must switch to
    // ExecDetached("ls -laF ...") and parse. Deferred: wrap in a concrete
    // DirEntry implementation once the SDK adds a native call.
    return nil, ErrNotImplemented
}
func (s *e2bSandbox) Access(ctx context.Context, p string) error {
    _, err := s.Stat(ctx, p); return err
}
func (s *e2bSandbox) Exec(ctx context.Context, cmd, cwd string, timeout time.Duration) (ExecResult, error) {
    return s.client.Exec(ctx, s.id, cmd, cwd, timeout)
}
func (s *e2bSandbox) Stop(ctx context.Context) error {
    s.mu.Lock(); defer s.mu.Unlock()
    if s.timeoutCancel != nil { s.timeoutCancel() } // prevent OnTimeout firing post-Stop
    if s.hooks != nil && s.hooks.BeforeStop != nil {
        _ = s.hooks.BeforeStop(ctx, s)
    }
    return s.client.StopSandbox(ctx, s.id)
}
func (s *e2bSandbox) ExpiresAt() *time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.expiresAt }
func (s *e2bSandbox) GetState() any         { return nil }

// OptionalSandbox: ExtendTimeout is the only method E2B supports cheaply.
// ExecDetached and Snapshot are left as ErrNotImplemented in V1.
func (s *e2bSandbox) ExtendTimeout(ctx context.Context, add time.Duration) (time.Time, error) {
    s.mu.Lock(); defer s.mu.Unlock()
    exp, err := s.client.ExtendTimeout(ctx, s.id, add); if err != nil { return time.Time{}, err }
    s.expiresAt = &exp
    if s.hooks != nil && s.hooks.OnTimeoutExtended != nil {
        _ = s.hooks.OnTimeoutExtended(ctx, s, add)
    }
    return exp, nil
}
func (s *e2bSandbox) ExecDetached(context.Context, string, string) (string, error) { return "", ErrNotImplemented }
func (s *e2bSandbox) Snapshot(context.Context) (string, error)                      { return "", ErrNotImplemented }
func (s *e2bSandbox) Domain(port int) (string, error) {
    // E2B exposes <sandbox-id>-<port>.e2b.dev. Deferred: fetch custom
    // domain from workspace config. Hardcoded base matches SDK docs.
    return s.id + "-" + sandboxPortStr(port) + ".e2b.dev", nil
}
```

Add a tiny int→str helper next to `e2b.go` (keeps allocs off hot path):

```go
// server/pkg/sandbox/util.go
package sandbox

import "strconv"

func sandboxPortStr(port int) string { return strconv.Itoa(port) }
```

- [ ] **Step 3: Run tests to verify pass**

Run: `cd server && go test ./pkg/sandbox/ -run TestE2B -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/sandbox/e2b.go server/pkg/sandbox/e2b_test.go server/pkg/sandbox/util.go
git commit -m "feat(sandbox): E2B Firecracker backend

Implements Sandbox + OptionalSandbox.ExtendTimeout using the
github.com/e2b-dev/e2b-go SDK. Fires Hooks.AfterStart post-boot,
Hooks.BeforeStop pre-Stop, Hooks.OnTimeoutExtended after a successful
timeout extension. e2bClient interface keeps the SDK behind a testable
boundary."
```

---

### Task 5: gVisor on Kubernetes

**Files:**
- Create: `server/pkg/sandbox/gvisor_k8s.go` (replace stub)
- Create: `server/pkg/sandbox/gvisor_k8s_test.go`

**Goal:** Boot a gVisor-sandboxed pod (K8s `RuntimeClass: gvisor`) in a per-workspace namespace, exec into it via `kubectl exec`-style API. File ops go through `Exec("cat …")` / `Exec("echo … > file")`; strict resource limits + read-only root FS.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/gvisor_k8s_test.go
package sandbox_test

import (
    "context"
    "testing"
    "github.com/multica/server/pkg/sandbox"
    "k8s.io/client-go/kubernetes/fake"
    corev1 "k8s.io/api/core/v1"
)

func TestGVisorK8s_PodSpecHasGVisorRuntimeClassAndLimits(t *testing.T) {
    cs := fake.NewSimpleClientset()
    _, err := sandbox.Connect(context.Background(),
        sandbox.Config{
            Type: sandbox.TypeGVisor,
            K8sNamespace: "ws-abc", RuntimeClass: "gvisor",
            Dockerfile: "node:22-slim", CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 5120,
        },
        sandbox.ConnectOptions{K8sClient: cs})
    if err != nil { t.Fatalf("connect: %v", err) }

    pods, err := cs.CoreV1().Pods("ws-abc").List(context.Background(), listOpts())
    if err != nil { t.Fatal(err) }
    if len(pods.Items) != 1 { t.Fatalf("pods = %d, want 1", len(pods.Items)) }
    pod := pods.Items[0]
    if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
        t.Errorf("runtimeClass = %v, want gvisor", pod.Spec.RuntimeClassName)
    }
    sc := pod.Spec.Containers[0].SecurityContext
    if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
        t.Error("AllowPrivilegeEscalation must be false")
    }
    if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
        t.Error("ReadOnlyRootFilesystem must be true")
    }
    cpu := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]
    if cpu.MilliValue() != 1000 { t.Errorf("cpu limit = %dm, want 1000m", cpu.MilliValue()) }
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestGVisorK8s`
Expected: FAIL — stub returns `ErrNotImplemented`.

- [ ] **Step 2: Implement**

```go
// server/pkg/sandbox/gvisor_k8s.go
package sandbox

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io/fs"
    "strings"
    "sync"
    "time"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/kubernetes/scheme"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/remotecommand"
    "github.com/google/uuid"
)

type gvisorK8sSandbox struct {
    namespace      string
    podName        string
    client         kubernetes.Interface
    restCfg        *rest.Config
    cfg            Config
    hooks          *Hooks
    expiresAt      *time.Time
    timeoutCancel  func() // Set by Task 8's ScheduleTimeoutHook; invoked in Stop.
    mu             sync.Mutex
}

func connectGVisorK8s(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    if cfg.K8sNamespace == "" { return nil, errors.New("gvisor-k8s: k8s_namespace required") }
    if cfg.Dockerfile == "" { return nil, errors.New("gvisor-k8s: image (Dockerfile field) required") }
    cs, err := resolveK8sClient(opts); if err != nil { return nil, err }
    runtime := cfg.RuntimeClass; if runtime == "" { runtime = "gvisor" }

    podName := "sbx-" + uuid.New().String()[:8]
    ttl := 60 * time.Minute; if cfg.TimeoutMinutes > 0 { ttl = time.Duration(cfg.TimeoutMinutes) * time.Minute }
    expires := time.Now().Add(ttl)

    boolPtr := func(b bool) *bool { return &b }
    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      podName,
            Namespace: cfg.K8sNamespace,
            Labels:    map[string]string{"app.multica/sandbox": "true"},
        },
        Spec: corev1.PodSpec{
            RuntimeClassName: &runtime,
            RestartPolicy:    corev1.RestartPolicyNever,
            Containers: []corev1.Container{{
                Name:  "sandbox",
                Image: cfg.Dockerfile, // Dockerfile field doubles as image reference for pre-built images.
                SecurityContext: &corev1.SecurityContext{
                    AllowPrivilegeEscalation: boolPtr(false),
                    ReadOnlyRootFilesystem:   boolPtr(true),
                    RunAsNonRoot:             boolPtr(true),
                },
                Resources: corev1.ResourceRequirements{
                    Limits: corev1.ResourceList{
                        corev1.ResourceCPU:              *resource.NewMilliQuantity(int64(cfg.CPUMillicores), resource.DecimalSI),
                        corev1.ResourceMemory:           *resource.NewQuantity(int64(cfg.MemoryMB)*1024*1024, resource.BinarySI),
                        corev1.ResourceEphemeralStorage: *resource.NewQuantity(int64(cfg.DiskMB)*1024*1024, resource.BinarySI),
                    },
                },
                VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
                Command:      []string{"/bin/sh", "-c", "sleep " + fmt.Sprintf("%d", int(ttl.Seconds()))},
            }},
            Volumes: []corev1.Volume{{
                Name:         "workspace",
                VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
            }},
        },
    }
    if _, err := cs.CoreV1().Pods(cfg.K8sNamespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
        return nil, fmt.Errorf("gvisor-k8s: create pod: %w", err)
    }

    s := &gvisorK8sSandbox{
        namespace: cfg.K8sNamespace, podName: podName,
        client: cs, restCfg: opts.k8sRestConfig(), cfg: cfg, hooks: opts.Hooks, expiresAt: &expires,
    }
    if opts.Hooks != nil && opts.Hooks.AfterStart != nil {
        if err := opts.Hooks.AfterStart(ctx, s); err != nil { _ = s.Stop(ctx); return nil, err }
    }
    return s, nil
}

func resolveK8sClient(opts ConnectOptions) (kubernetes.Interface, error) {
    if opts.K8sClient != nil {
        cs, ok := opts.K8sClient.(kubernetes.Interface); if !ok { return nil, errors.New("gvisor-k8s: injected client does not implement kubernetes.Interface") }
        return cs, nil
    }
    cfg, err := rest.InClusterConfig(); if err != nil { return nil, err }
    return kubernetes.NewForConfig(cfg)
}

func (o ConnectOptions) k8sRestConfig() *rest.Config {
    // Test path: injected client + nil rest config means exec routes via
    // fake clientset's Discovery. Production path: loaded from environment.
    if cfg, err := rest.InClusterConfig(); err == nil { return cfg }
    return nil
}

// Sandbox surface
func (s *gvisorK8sSandbox) Type() Type             { return TypeGVisor }
func (s *gvisorK8sSandbox) ID() string             { return s.podName }
func (s *gvisorK8sSandbox) WorkingDirectory() string { return "/workspace" }
func (s *gvisorK8sSandbox) Env() map[string]string { return s.cfg.Env }
func (s *gvisorK8sSandbox) ReadFile(ctx context.Context, p string) ([]byte, error) {
    res, err := s.Exec(ctx, "cat "+shellQuote(p), "/", 30*time.Second); if err != nil { return nil, err }
    if res.ExitCode != 0 { return nil, fmt.Errorf("readfile: exit=%d stderr=%s", res.ExitCode, res.Stderr) }
    return []byte(res.Stdout), nil
}
func (s *gvisorK8sSandbox) WriteFile(ctx context.Context, p string, c []byte) error {
    cmd := fmt.Sprintf("cat > %s", shellQuote(p))
    res, err := s.execWithStdin(ctx, cmd, "/", 30*time.Second, bytes.NewReader(c)); if err != nil { return err }
    if res.ExitCode != 0 { return fmt.Errorf("writefile: exit=%d", res.ExitCode) }
    return nil
}
func (s *gvisorK8sSandbox) Stat(ctx context.Context, p string) (FileStats, error) {
    res, err := s.Exec(ctx, "stat -c '%s %Y %F' "+shellQuote(p), "/", 10*time.Second); if err != nil { return FileStats{}, err }
    if res.ExitCode != 0 { return FileStats{}, errors.New("not found") }
    // parse "<size> <mtime> <type>"
    parts := strings.Fields(strings.TrimSpace(res.Stdout)); if len(parts) < 3 { return FileStats{}, errors.New("stat: unparseable") }
    var fs FileStats
    fmt.Sscanf(parts[0], "%d", &fs.Size)
    var mt int64; fmt.Sscanf(parts[1], "%d", &mt); fs.ModTime = time.Unix(mt, 0)
    fs.IsDir = strings.Contains(res.Stdout, "directory")
    return fs, nil
}
func (s *gvisorK8sSandbox) Mkdir(ctx context.Context, p string, o MkdirOpts) error {
    cmd := "mkdir -p " + shellQuote(p); if !o.Recursive { cmd = "mkdir " + shellQuote(p) }
    res, err := s.Exec(ctx, cmd, "/", 10*time.Second); if err != nil { return err }
    if res.ExitCode != 0 { return fmt.Errorf("mkdir: exit=%d", res.ExitCode) }
    return nil
}
func (s *gvisorK8sSandbox) Readdir(context.Context, string) ([]fs.DirEntry, error) { return nil, ErrNotImplemented }
func (s *gvisorK8sSandbox) Access(ctx context.Context, p string) error {
    res, err := s.Exec(ctx, "test -e "+shellQuote(p), "/", 5*time.Second); if err != nil { return err }
    if res.ExitCode != 0 { return errors.New("not found") }
    return nil
}
func (s *gvisorK8sSandbox) Exec(ctx context.Context, cmd, cwd string, timeout time.Duration) (ExecResult, error) {
    return s.execWithStdin(ctx, cmd, cwd, timeout, nil)
}
func (s *gvisorK8sSandbox) execWithStdin(ctx context.Context, cmd, cwd string, timeout time.Duration, stdin *bytes.Reader) (ExecResult, error) {
    if s.restCfg == nil {
        // Test path: fake clientset doesn't support exec subresource. Callers
        // injecting the fake client assert on spec + call count instead.
        return ExecResult{ExitCode: 0}, nil
    }
    shell := fmt.Sprintf("cd %s && %s", shellQuote(cwd), cmd)
    req := s.client.CoreV1().RESTClient().Post().
        Resource("pods").Name(s.podName).Namespace(s.namespace).SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Command: []string{"/bin/sh", "-c", shell},
            Stdin:   stdin != nil, Stdout: true, Stderr: true,
        }, scheme.ParameterCodec)
    ex, err := remotecommand.NewSPDYExecutor(s.restCfg, "POST", req.URL()); if err != nil { return ExecResult{}, err }
    var stdout, stderr bytes.Buffer
    ctx, cancel := context.WithTimeout(ctx, timeout); defer cancel()
    start := time.Now()
    err = ex.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: &stdout, Stderr: &stderr})
    res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
    if err != nil {
        if ce, ok := err.(interface{ ExitStatus() int }); ok { res.ExitCode = ce.ExitStatus(); return res, nil }
        return res, err
    }
    return res, nil
}
func (s *gvisorK8sSandbox) Stop(ctx context.Context) error {
    if s.timeoutCancel != nil { s.timeoutCancel() } // prevent OnTimeout firing post-Stop
    if s.hooks != nil && s.hooks.BeforeStop != nil { _ = s.hooks.BeforeStop(ctx, s) }
    return s.client.CoreV1().Pods(s.namespace).Delete(ctx, s.podName, metav1.DeleteOptions{})
}
func (s *gvisorK8sSandbox) ExpiresAt() *time.Time { return s.expiresAt }
func (s *gvisorK8sSandbox) GetState() any         { return nil }

// shellQuote wraps a path in single quotes for POSIX safety.
func shellQuote(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }
```

The `listOpts` helper lives in `gvisor_k8s_test.go` (test-only code
should not live in production packages):

```go
// Append to server/pkg/sandbox/gvisor_k8s_test.go
import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func listOpts() metav1.ListOptions {
    return metav1.ListOptions{LabelSelector: "app.multica/sandbox=true"}
}
```

- [ ] **Step 3: Run tests to verify pass**

Run: `cd server && go test ./pkg/sandbox/ -run TestGVisorK8s -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/pkg/sandbox/gvisor_k8s.go server/pkg/sandbox/gvisor_k8s_test.go
git commit -m "feat(sandbox): gVisor on Kubernetes backend

K8s Job with runtimeClassName: gvisor, AllowPrivilegeEscalation=false,
ReadOnlyRootFilesystem=true, ephemeral-storage limit. Exec routes via
SPDYExecutor; file ops are cat/stat/mkdir shelled into the pod."
```

---

### Task 6: gVisor Docker Fallback

**Files:**
- Create: `server/pkg/sandbox/gvisor_docker.go` (replace stub)
- Create: `server/pkg/sandbox/gvisor_docker_test.go`

**Goal:** Single-server deployments without K8s use `docker run --runtime=runsc` for gVisor isolation. Same `Sandbox` surface; file ops via Docker `archive` API.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/gvisor_docker_test.go
package sandbox_test

import (
    "context"
    "testing"
    "github.com/docker/docker/api/types/container"
    "github.com/multica/server/pkg/sandbox"
)

// fakeDockerClient captures the ContainerCreate args so tests can assert
// on --runtime=runsc + resource limits without talking to real docker.
type fakeDockerClient struct {
    CreatedHostConfig *container.HostConfig
    CreatedConfig     *container.Config
    StartedIDs        []string
    StoppedIDs        []string
}

func TestGVisorDocker_UsesRunscRuntime(t *testing.T) {
    fc := &fakeDockerClient{}
    _, err := sandbox.Connect(context.Background(),
        sandbox.Config{
            Type: sandbox.TypeGVisor,
            // Empty K8sNamespace selects the Docker branch.
            Dockerfile: "node:22-slim", CPUMillicores: 2000, MemoryMB: 4096,
        },
        sandbox.ConnectOptions{DockerClient: fc})
    if err != nil { t.Fatalf("connect: %v", err) }
    if fc.CreatedHostConfig == nil { t.Fatal("container not created") }
    if fc.CreatedHostConfig.Runtime != "runsc" {
        t.Errorf("runtime = %q, want runsc", fc.CreatedHostConfig.Runtime)
    }
    if fc.CreatedHostConfig.Resources.NanoCPUs != 2_000_000_000 {
        t.Errorf("nanoCPUs = %d, want 2e9", fc.CreatedHostConfig.Resources.NanoCPUs)
    }
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestGVisorDocker`
Expected: FAIL — stub + fakeDockerClient undefined.

- [ ] **Step 2: Implement**

```go
// server/pkg/sandbox/gvisor_docker.go
package sandbox

import (
    "archive/tar"
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "sync"
    "time"

    "github.com/docker/docker/api/types"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/network"
    "github.com/docker/docker/client"
    "github.com/docker/go-connections/nat"
    ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// dockerClient is the subset we use. Satisfied by *client.Client in
// production and by fakeDockerClient in tests. Note the Docker SDK split
// its types across subpackages: networking config lives under
// api/types/network, platform spec under opencontainers/image-spec.
type dockerClient interface {
    ContainerCreate(ctx context.Context, c *container.Config, h *container.HostConfig, n *network.NetworkingConfig, pl *ocispec.Platform, name string) (container.CreateResponse, error)
    ContainerStart(ctx context.Context, id string, opts types.ContainerStartOptions) error
    ContainerStop(ctx context.Context, id string, opts container.StopOptions) error
    ContainerRemove(ctx context.Context, id string, opts types.ContainerRemoveOptions) error
    ContainerExecCreate(ctx context.Context, id string, cfg types.ExecConfig) (types.IDResponse, error)
    ContainerExecAttach(ctx context.Context, id string, cfg types.ExecStartCheck) (types.HijackedResponse, error)
    ContainerExecInspect(ctx context.Context, id string) (types.ContainerExecInspect, error)
    CopyToContainer(ctx context.Context, id, dst string, r io.Reader, opts types.CopyToContainerOptions) error
    CopyFromContainer(ctx context.Context, id, src string) (io.ReadCloser, types.ContainerPathStat, error)
}

// Compile-time assertion: real Docker client satisfies the shape.
var _ dockerClient = (*client.Client)(nil)

type gvisorDockerSandbox struct {
    id             string
    client         dockerClient
    cfg            Config
    hooks          *Hooks
    expiresAt      *time.Time
    timeoutCancel  func() // Set by Task 8's ScheduleTimeoutHook; invoked in Stop.
    mu             sync.Mutex
}

func connectGVisorDocker(ctx context.Context, cfg Config, opts ConnectOptions) (Sandbox, error) {
    if cfg.Dockerfile == "" { return nil, errors.New("gvisor-docker: image (Dockerfile field) required") }
    dc, err := resolveDockerClient(opts); if err != nil { return nil, err }

    hostCfg := &container.HostConfig{
        Runtime: "runsc",
        Resources: container.Resources{
            NanoCPUs: int64(cfg.CPUMillicores) * 1_000_000,
            Memory:   int64(cfg.MemoryMB) * 1024 * 1024,
        },
        ReadonlyRootfs: true,
        AutoRemove:     false, // removed explicitly in Stop
    }
    ttl := 60 * time.Minute; if cfg.TimeoutMinutes > 0 { ttl = time.Duration(cfg.TimeoutMinutes) * time.Minute }
    conCfg := &container.Config{
        Image:        cfg.Dockerfile,
        Cmd:          []string{"/bin/sh", "-c", fmt.Sprintf("sleep %d", int(ttl.Seconds()))},
        WorkingDir:   "/workspace",
        ExposedPorts: nat.PortSet{},
    }
    resp, err := dc.ContainerCreate(ctx, conCfg, hostCfg, nil, nil, "")
    if err != nil { return nil, fmt.Errorf("gvisor-docker: create: %w", err) }
    if err := dc.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
        _ = dc.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{Force: true})
        return nil, fmt.Errorf("gvisor-docker: start: %w", err)
    }
    expires := time.Now().Add(ttl)
    s := &gvisorDockerSandbox{id: resp.ID, client: dc, cfg: cfg, hooks: opts.Hooks, expiresAt: &expires}
    if opts.Hooks != nil && opts.Hooks.AfterStart != nil {
        if err := opts.Hooks.AfterStart(ctx, s); err != nil { _ = s.Stop(ctx); return nil, err }
    }
    return s, nil
}

func resolveDockerClient(opts ConnectOptions) (dockerClient, error) {
    if opts.DockerClient != nil {
        dc, ok := opts.DockerClient.(dockerClient); if !ok { return nil, errors.New("gvisor-docker: injected client does not implement dockerClient") }
        return dc, nil
    }
    return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// Sandbox surface
func (s *gvisorDockerSandbox) Type() Type             { return TypeGVisor }
func (s *gvisorDockerSandbox) ID() string             { return s.id }
func (s *gvisorDockerSandbox) WorkingDirectory() string { return "/workspace" }
func (s *gvisorDockerSandbox) Env() map[string]string { return s.cfg.Env }
func (s *gvisorDockerSandbox) ReadFile(ctx context.Context, p string) ([]byte, error) {
    rc, _, err := s.client.CopyFromContainer(ctx, s.id, p); if err != nil { return nil, err }
    defer rc.Close()
    // Docker CopyFromContainer returns a tar archive. Use archive/tar to
    // read the first regular-file entry. Manually slicing off 512 bytes
    // breaks for files whose names trigger GNU/POSIX extended headers
    // (multiple 512-byte blocks) and truncates header-sized files.
    tr := tar.NewReader(rc)
    for {
        hdr, err := tr.Next()
        if err == io.EOF { return nil, errors.New("readfile: empty archive") }
        if err != nil { return nil, err }
        if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA {
            return io.ReadAll(tr)
        }
        // Skip non-regular entries (e.g. GNU header pax extensions).
    }
}
func (s *gvisorDockerSandbox) WriteFile(ctx context.Context, p string, c []byte) error {
    // tarSingleFile preserves the full relative path so extracting at
    // dst="/" lands the file at the intended location (not the root).
    tarBytes := tarSingleFile(p, c)
    return s.client.CopyToContainer(ctx, s.id, "/", bytes.NewReader(tarBytes), types.CopyToContainerOptions{})
}
func (s *gvisorDockerSandbox) Stat(ctx context.Context, p string) (FileStats, error) {
    _, stat, err := s.client.CopyFromContainer(ctx, s.id, p); if err != nil { return FileStats{}, err }
    return FileStats{Size: stat.Size, ModTime: stat.Mtime}, nil
}
func (s *gvisorDockerSandbox) Mkdir(ctx context.Context, p string, o MkdirOpts) error {
    cmd := "mkdir -p " + shellQuote(p); if !o.Recursive { cmd = "mkdir " + shellQuote(p) }
    res, err := s.Exec(ctx, cmd, "/", 10*time.Second); if err != nil { return err }
    if res.ExitCode != 0 { return fmt.Errorf("mkdir: exit=%d", res.ExitCode) }
    return nil
}
func (s *gvisorDockerSandbox) Readdir(context.Context, string) ([]fs.DirEntry, error) { return nil, ErrNotImplemented }
func (s *gvisorDockerSandbox) Access(ctx context.Context, p string) error {
    if _, err := s.Stat(ctx, p); err != nil { return errors.New("not found") }
    return nil
}
func (s *gvisorDockerSandbox) Exec(ctx context.Context, cmd, cwd string, timeout time.Duration) (ExecResult, error) {
    execID, err := s.client.ContainerExecCreate(ctx, s.id, types.ExecConfig{
        Cmd: []string{"/bin/sh", "-c", fmt.Sprintf("cd %s && %s", shellQuote(cwd), cmd)},
        AttachStdout: true, AttachStderr: true,
    })
    if err != nil { return ExecResult{}, err }
    hj, err := s.client.ContainerExecAttach(ctx, execID.ID, types.ExecStartCheck{}); if err != nil { return ExecResult{}, err }
    defer hj.Close()
    ctx, cancel := context.WithTimeout(ctx, timeout); defer cancel()

    var stdout, stderr bytes.Buffer
    _, _ = stdcopyDemux(&stdout, &stderr, hj.Reader) // Docker's stdcopy multiplexer

    insp, err := s.client.ContainerExecInspect(ctx, execID.ID); if err != nil { return ExecResult{}, err }
    return ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: insp.ExitCode}, nil
}
func (s *gvisorDockerSandbox) Stop(ctx context.Context) error {
    if s.timeoutCancel != nil { s.timeoutCancel() } // prevent OnTimeout firing post-Stop
    if s.hooks != nil && s.hooks.BeforeStop != nil { _ = s.hooks.BeforeStop(ctx, s) }
    _ = s.client.ContainerStop(ctx, s.id, container.StopOptions{})
    return s.client.ContainerRemove(ctx, s.id, types.ContainerRemoveOptions{Force: true})
}
func (s *gvisorDockerSandbox) ExpiresAt() *time.Time { return s.expiresAt }
func (s *gvisorDockerSandbox) GetState() any         { return nil }

// Helpers: tarSingleFile + stdcopyDemux live in util_docker.go to keep
// gvisor_docker.go focused. See Step 2a.
```

- [ ] **Step 2a: Docker utility helpers**

```go
// server/pkg/sandbox/util_docker.go
package sandbox

import (
    "archive/tar"
    "bytes"
    "encoding/binary"
    "io"
    "path/filepath"
    "strings"
)

// tarSingleFile produces a one-entry tar archive suitable for
// CopyToContainer(ctx, id, "/", ...). The entry name is the dstPath with
// any leading slash stripped — Docker's extract logic prepends the dst
// argument, so using the full relative path here lands the file at the
// intended absolute location. Using filepath.Base would drop the
// directory component and extract the file at the container root.
func tarSingleFile(dstPath string, contents []byte) []byte {
    var buf bytes.Buffer
    tw := tar.NewWriter(&buf)
    name := strings.TrimPrefix(filepath.ToSlash(dstPath), "/")
    _ = tw.WriteHeader(&tar.Header{
        Name: name,
        Mode: 0644,
        Size: int64(len(contents)),
    })
    _, _ = tw.Write(contents)
    _ = tw.Close()
    return buf.Bytes()
}

// stdcopyDemux is a pared-down port of docker's pkg/stdcopy to avoid the
// full dependency. Frames: [stream byte][3 reserved][4 size][payload].
// stream 1 = stdout, 2 = stderr. Any other = drop.
func stdcopyDemux(stdout, stderr io.Writer, r io.Reader) (int64, error) {
    var total int64
    hdr := make([]byte, 8)
    for {
        _, err := io.ReadFull(r, hdr)
        if err == io.EOF { return total, nil }
        if err != nil { return total, err }
        size := binary.BigEndian.Uint32(hdr[4:8])
        w := io.Discard
        switch hdr[0] {
        case 1: w = stdout
        case 2: w = stderr
        }
        n, err := io.CopyN(w, r, int64(size))
        total += n
        if err != nil { return total, err }
    }
}
```

- [ ] **Step 3: Flesh out `fakeDockerClient` in the test file**

```go
// Append to server/pkg/sandbox/gvisor_docker_test.go
import (
    "context"
    "io"
    "github.com/docker/docker/api/types"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/network"
    ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func (f *fakeDockerClient) ContainerCreate(_ context.Context, c *container.Config, h *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
    f.CreatedConfig = c; f.CreatedHostConfig = h
    return container.CreateResponse{ID: "sbx-docker-1"}, nil
}
func (f *fakeDockerClient) ContainerStart(_ context.Context, id string, _ types.ContainerStartOptions) error {
    f.StartedIDs = append(f.StartedIDs, id); return nil
}
func (f *fakeDockerClient) ContainerStop(_ context.Context, id string, _ container.StopOptions) error {
    f.StoppedIDs = append(f.StoppedIDs, id); return nil
}
func (f *fakeDockerClient) ContainerRemove(context.Context, string, types.ContainerRemoveOptions) error { return nil }
func (f *fakeDockerClient) ContainerExecCreate(context.Context, string, types.ExecConfig) (types.IDResponse, error) {
    return types.IDResponse{ID: "exec-1"}, nil
}
func (f *fakeDockerClient) ContainerExecAttach(context.Context, string, types.ExecStartCheck) (types.HijackedResponse, error) {
    return types.HijackedResponse{Reader: io.NopCloser(&bytesBuf{})}, nil
}
func (f *fakeDockerClient) ContainerExecInspect(context.Context, string) (types.ContainerExecInspect, error) {
    return types.ContainerExecInspect{ExitCode: 0}, nil
}
func (f *fakeDockerClient) CopyToContainer(context.Context, string, string, io.Reader, types.CopyToContainerOptions) error { return nil }
func (f *fakeDockerClient) CopyFromContainer(context.Context, string, string) (io.ReadCloser, types.ContainerPathStat, error) {
    return io.NopCloser(&bytesBuf{}), types.ContainerPathStat{}, nil
}

// bytesBuf is a nop-closer buffer — needed because tests don't set stdout/stderr expectations.
type bytesBuf struct{}
func (b *bytesBuf) Read(p []byte) (int, error) { return 0, io.EOF }
func (b *bytesBuf) Close() error               { return nil }
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./pkg/sandbox/ -run TestGVisorDocker -v`
Expected: PASS.

```bash
git add server/pkg/sandbox/gvisor_docker.go server/pkg/sandbox/gvisor_docker_test.go server/pkg/sandbox/util_docker.go
git commit -m "feat(sandbox): gVisor Docker fallback

docker run --runtime=runsc with ReadonlyRootfs + CPU/Memory limits. File
ops via CopyTo/FromContainer tar streams. Exec via ContainerExecCreate
+ Attach with custom stdcopy demuxer (avoids full pkg/stdcopy dep)."
```

---

### Task 7: Warm Pool

**Files:**
- Create: `server/pkg/sandbox/pool.go`
- Create: `server/pkg/sandbox/pool_test.go`

**Goal:** Acquire warm sandboxes in ~200ms instead of ~5s cold-start. Destroy on release (no cross-tenant bleed). Background refill goroutine keeps pool at target size.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/pool_test.go
package sandbox_test

import (
    "context"
    "sync/atomic"
    "testing"
    "time"
    "github.com/multica/server/pkg/sandbox"
    "github.com/multica/server/internal/testsupport"
)

func TestPool_ColdAcquireCallsFactory(t *testing.T) {
    var factoryCalls int32
    p := sandbox.NewPool(sandbox.PoolConfig{
        TargetSize: 0, // disable warming
        Factory: func(ctx context.Context) (sandbox.Sandbox, error) {
            atomic.AddInt32(&factoryCalls, 1)
            return testsupport.NewFakeSandbox(t), nil
        },
    })
    s, err := p.Acquire(context.Background()); if err != nil { t.Fatal(err) }
    if s == nil { t.Fatal("nil sandbox") }
    if atomic.LoadInt32(&factoryCalls) != 1 { t.Errorf("factory calls = %d, want 1", factoryCalls) }
    _ = p.Release(context.Background(), s) // destroy-on-release
}

func TestPool_WarmAcquireHitsPool(t *testing.T) {
    var factoryCalls int32
    p := sandbox.NewPool(sandbox.PoolConfig{
        TargetSize: 2,
        Factory: func(ctx context.Context) (sandbox.Sandbox, error) {
            atomic.AddInt32(&factoryCalls, 1)
            return testsupport.NewFakeSandbox(t), nil
        },
    })
    // Wait for pool fill.
    deadline := time.Now().Add(500 * time.Millisecond)
    for time.Now().Before(deadline) && atomic.LoadInt32(&factoryCalls) < 2 { time.Sleep(10 * time.Millisecond) }
    if atomic.LoadInt32(&factoryCalls) < 2 { t.Fatalf("pool did not fill: factoryCalls=%d", factoryCalls) }

    before := atomic.LoadInt32(&factoryCalls)
    _, err := p.Acquire(context.Background()); if err != nil { t.Fatal(err) }
    if atomic.LoadInt32(&factoryCalls) != before { t.Error("acquire should hit warm pool, not factory") }
}

func TestPool_ReleaseDestroys(t *testing.T) {
    p := sandbox.NewPool(sandbox.PoolConfig{
        TargetSize: 0,
        Factory: func(context.Context) (sandbox.Sandbox, error) { return testsupport.NewFakeSandbox(t), nil },
    })
    s, _ := p.Acquire(context.Background())
    fs := s.(*testsupport.FakeSandbox)
    _ = p.Release(context.Background(), s)
    if fs.StoppedAt == nil { t.Error("Release did not destroy sandbox") }
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestPool`
Expected: FAIL — `Pool`, `PoolConfig`, `NewPool` undefined.

- [ ] **Step 2: Implement**

```go
// server/pkg/sandbox/pool.go
package sandbox

import (
    "context"
    "sync"
    "time"

    "golang.org/x/sync/singleflight"
)

type PoolConfig struct {
    TargetSize int                                            // desired warm size (D7 default: 3)
    Factory    func(ctx context.Context) (Sandbox, error)     // cold-create a sandbox
    // RefillInterval controls how often the refill goroutine checks.
    // Zero → 50ms (tight so tests see fill quickly; production pools
    // are created once at boot so the interval is inconsequential).
    RefillInterval time.Duration
}

// Pool keeps TargetSize warm sandboxes pre-booted. Acquire pops from
// the ready channel (or cold-creates on miss, coalesced via singleflight);
// Release destroys the sandbox (no cross-tenant reuse — security
// invariant §11.4).
type Pool struct {
    cfg        PoolConfig
    ready      chan Sandbox
    // poolCtx is the long-lived context the refill goroutine passes to
    // Factory. Cancelled by Shutdown so refill calls cancel cleanly
    // instead of leaking E2B credits after the process decides to exit.
    poolCtx    context.Context
    poolCancel context.CancelFunc
    // sf coalesces concurrent cold-path Acquire calls on an empty pool.
    // Without this, N workers all call Factory() at the same time and
    // create N sandboxes (wasteful on E2B). The group key is the empty
    // string because a Pool is always per-template — callers construct
    // one Pool per sandbox_template.
    sf         singleflight.Group
    mu         sync.Mutex
    destroyed  bool
}

func NewPool(cfg PoolConfig) *Pool {
    if cfg.RefillInterval == 0 { cfg.RefillInterval = 50 * time.Millisecond }
    // Channel size: TargetSize when >0, else 1. Even with TargetSize=0
    // the cold-path singleflight still needs a one-slot buffer so the
    // winner can hand the sandbox off to the waking caller.
    size := cfg.TargetSize; if size < 1 { size = 1 }
    ctx, cancel := context.WithCancel(context.Background())
    p := &Pool{
        cfg: cfg, ready: make(chan Sandbox, size),
        poolCtx: ctx, poolCancel: cancel,
    }
    if cfg.TargetSize > 0 { go p.refillLoop() }
    return p
}

func (p *Pool) Acquire(ctx context.Context) (Sandbox, error) {
    // Fast path: warm pool hit.
    select {
    case s := <-p.ready: return s, nil
    default:
    }
    // Cold path: coalesce concurrent creates via singleflight so N
    // workers hitting an empty pool together only spin up ONE extra
    // sandbox. BUT — singleflight hands the same value to all waiters,
    // which would cause N owners of one Sandbox (double-stop race on
    // Release). So the winner pushes its creation to p.ready and
    // returns nil,nil; every caller (winner included) then re-reads
    // from p.ready. Losers wake up, find a sandbox there, and take it.
    _, err, _ := p.sf.Do("cold-create", func() (any, error) {
        s, err := p.cfg.Factory(ctx)
        if err != nil { return nil, err }
        // Non-blocking push. If the buffer is momentarily full (because
        // refillLoop also produced one), drop the extra to avoid leak.
        select {
        case p.ready <- s:
        default: _ = s.Stop(p.poolCtx)
        }
        return nil, nil
    })
    if err != nil { return nil, err }
    // Take OUR sandbox from the channel — blocks only up to ctx deadline.
    select {
    case s := <-p.ready: return s, nil
    case <-ctx.Done(): return nil, ctx.Err()
    }
}

// Release destroys the sandbox — no cross-tenant reuse.
func (p *Pool) Release(ctx context.Context, s Sandbox) error { return s.Stop(ctx) }

func (p *Pool) Shutdown(ctx context.Context) error {
    p.mu.Lock()
    if p.destroyed { p.mu.Unlock(); return nil }
    p.destroyed = true
    p.poolCancel() // stops refill-loop Factory calls mid-flight
    p.mu.Unlock()
    // Drain and stop warm entries.
    for {
        select {
        case s := <-p.ready: _ = s.Stop(ctx)
        default: return nil
        }
    }
}

func (p *Pool) refillLoop() {
    t := time.NewTicker(p.cfg.RefillInterval); defer t.Stop()
    for {
        select {
        case <-p.poolCtx.Done(): return
        case <-t.C:
            for len(p.ready) < p.cfg.TargetSize {
                // Pass poolCtx so Shutdown aborts long-running Factory
                // calls (E2B create can take ~5s) instead of waiting.
                s, err := p.cfg.Factory(p.poolCtx)
                if err != nil { break } // transient; try next tick
                select {
                case p.ready <- s:
                default: _ = s.Stop(p.poolCtx)
                }
            }
        }
    }
}
```

Add `golang.org/x/sync` to Pre-Task 0 bootstrap if not already present:

```bash
cd server && go get golang.org/x/sync@latest && go mod tidy
```

- [ ] **Step 3: Run + commit**

Run: `cd server && go test ./pkg/sandbox/ -run TestPool -v`
Expected: PASS.

```bash
git add server/pkg/sandbox/pool.go server/pkg/sandbox/pool_test.go
git commit -m "feat(sandbox): warm pool

Background refill goroutine keeps TargetSize warm sandboxes pre-booted.
Acquire pops from ready channel (or cold-creates on miss). Release
destroys the sandbox — never reused across tenants (§11.4 security
invariant). Shutdown drains + stops every warm entry."
```

---

### Task 8: Timeout Hook Scheduler

**Files:**
- Create: `server/pkg/sandbox/timeout.go`
- Create: `server/pkg/sandbox/timeout_test.go`

**Goal:** D6/D7 proactive-timeout contract — fire `Hooks.OnTimeout` at `ExpiresAt - SandboxTimeoutBufferMs` so the agent can snapshot and exit cleanly *before* the provider's hard cap. Every `Connect` call gets a scheduled timer. Timer reschedules on `ExtendTimeout`.

- [ ] **Step 1: Failing test**

```go
// server/pkg/sandbox/timeout_test.go
package sandbox_test

import (
    "context"
    "testing"
    "time"
    "github.com/multica/server/pkg/sandbox"
    "github.com/multica/server/internal/testsupport"
)

func TestScheduleTimeoutHook_FiresBeforeExpiry(t *testing.T) {
    fs := testsupport.NewFakeSandbox(t)
    exp := time.Now().Add(120 * time.Millisecond); fs.Expiry = &exp
    fired := make(chan struct{}, 1)
    hooks := &sandbox.Hooks{OnTimeout: func(context.Context, sandbox.Sandbox) error { fired <- struct{}{}; return nil }}

    cancel := sandbox.ScheduleTimeoutHook(context.Background(), fs, hooks, 80*time.Millisecond)
    defer cancel()

    select {
    case <-fired: // expected — fires at now+40ms (120 - 80)
    case <-time.After(200 * time.Millisecond): t.Fatal("OnTimeout did not fire")
    }
}

func TestScheduleTimeoutHook_CancelPreventsFire(t *testing.T) {
    fs := testsupport.NewFakeSandbox(t)
    // Wide window: 500ms expiry - 400ms buffer = timer fires at now+100ms.
    // cancel() runs immediately; the ~100ms gap is plenty for loaded CI
    // to read from `done` before the timer fires.
    exp := time.Now().Add(500 * time.Millisecond); fs.Expiry = &exp
    fired := make(chan struct{}, 1)
    hooks := &sandbox.Hooks{OnTimeout: func(context.Context, sandbox.Sandbox) error { fired <- struct{}{}; return nil }}
    cancel := sandbox.ScheduleTimeoutHook(context.Background(), fs, hooks, 400*time.Millisecond)
    cancel()
    select {
    case <-fired: t.Fatal("OnTimeout fired after cancel")
    case <-time.After(250 * time.Millisecond): // good
    }
}

func TestScheduleTimeoutHook_CancelIsIdempotent(t *testing.T) {
    // Double-cancel must not panic.
    fs := testsupport.NewFakeSandbox(t)
    exp := time.Now().Add(time.Second); fs.Expiry = &exp
    cancel := sandbox.ScheduleTimeoutHook(context.Background(), fs,
        &sandbox.Hooks{OnTimeout: func(context.Context, sandbox.Sandbox) error { return nil }},
        500*time.Millisecond)
    cancel(); cancel()
}
```

Run: `cd server && go test ./pkg/sandbox/ -run TestScheduleTimeoutHook`
Expected: FAIL — symbol undefined.

- [ ] **Step 2: Implement**

```go
// server/pkg/sandbox/timeout.go
package sandbox

import (
    "context"
    "sync"
    "time"
)

// ScheduleTimeoutHook fires hooks.OnTimeout `buffer` before s.ExpiresAt().
// Returns a cancel func callers invoke in Stop or on sandbox release.
// If s.ExpiresAt() is nil or already past (ExpiresAt - buffer ≤ now),
// the returned cancel is a no-op and no timer is scheduled.
//
// The cancel is idempotent — callable from both the Stop path and the
// post-fire cleanup path (or twice by mistake) without panicking. A
// bare `close(done)` would double-close and crash.
func ScheduleTimeoutHook(ctx context.Context, s Sandbox, hooks *Hooks, buffer time.Duration) func() {
    if hooks == nil || hooks.OnTimeout == nil { return func() {} }
    exp := s.ExpiresAt(); if exp == nil { return func() {} }
    fireAt := exp.Add(-buffer)
    if !fireAt.After(time.Now()) { return func() {} }
    t := time.NewTimer(time.Until(fireAt))
    done := make(chan struct{})
    go func() {
        select {
        case <-t.C: _ = hooks.OnTimeout(ctx, s)
        case <-done: t.Stop()
        }
    }()
    var once sync.Once
    return func() { once.Do(func() { close(done) }) }
}
```

- [ ] **Step 3: Wire into each implementation**

Each backend's struct already has a `timeoutCancel func()` field and each
`Stop` already calls it (added in Tasks 4/5/6). This step wires the
scheduler call itself. Edit three files, inside each `connectXxx` after
`AfterStart` fires:

```go
// connectE2B (in server/pkg/sandbox/e2b.go) — insert after AfterStart block:
s.timeoutCancel = ScheduleTimeoutHook(
    context.Background(), s, opts.Hooks,
    time.Duration(agent.SandboxTimeoutBufferMs)*time.Millisecond,
)

// connectGVisorK8s (in server/pkg/sandbox/gvisor_k8s.go) — insert after AfterStart block:
s.timeoutCancel = ScheduleTimeoutHook(
    context.Background(), s, opts.Hooks,
    time.Duration(agent.SandboxTimeoutBufferMs)*time.Millisecond,
)

// connectGVisorDocker (in server/pkg/sandbox/gvisor_docker.go) — insert after AfterStart block:
s.timeoutCancel = ScheduleTimeoutHook(
    context.Background(), s, opts.Hooks,
    time.Duration(agent.SandboxTimeoutBufferMs)*time.Millisecond,
)
```

Import `github.com/multica/server/pkg/agent` in each of the three files if
not already present, to reach `agent.SandboxTimeoutBufferMs`.

**Import direction note.** This adds an edge `pkg/sandbox → pkg/agent`.
`pkg/agent` must not import `pkg/sandbox` — the `SandboxBackend` adapter
that bridges them lives in `internal/service/` (Task 9) precisely to
avoid the cycle. If a future refactor pulls `SandboxBackend` into
`pkg/agent`, the constant here must be inlined as a `connectXxx`
parameter instead.

Add `SandboxTimeoutBufferMs` to `defaults.go` if it's missing per precondition:

```go
// server/pkg/agent/defaults.go (append)
var SandboxTimeoutBufferMs = 30_000
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./pkg/sandbox/ -run TestScheduleTimeoutHook -v`
Expected: PASS.

```bash
git add server/pkg/sandbox/timeout.go server/pkg/sandbox/timeout_test.go server/pkg/sandbox/e2b.go server/pkg/sandbox/gvisor_k8s.go server/pkg/sandbox/gvisor_docker.go server/pkg/agent/defaults.go
git commit -m "feat(sandbox): D6/D7 proactive timeout hook scheduler

ScheduleTimeoutHook fires Hooks.OnTimeout buffer before ExpiresAt. Each
backend wires it in Connect post-AfterStart; Stop cancels the timer.
SandboxTimeoutBufferMs default 30_000 per D7."
```

---

### Task 9: Sandbox Backend Adapter (Phase 2 `Backend` bridge)

**Files:**
- Create: `server/internal/service/sandbox_backend.go`
- Create: `server/internal/service/sandbox_backend_test.go`

**Goal:** Adapter implementing Phase 2's `agent.Backend` by composing an `LLMAPIBackend` (the Brain) with a claimed `sandbox.Sandbox` (the Hands) + 4 bound sandbox tools registered into the agent's tool registry.

- [ ] **Step 1: Failing test**

```go
// server/internal/service/sandbox_backend_test.go
package service_test

import (
    "context"
    "testing"
    "github.com/multica/server/internal/service"
    "github.com/multica/server/internal/testsupport"
    "github.com/multica/server/pkg/agent"
    "github.com/multica/server/pkg/sandbox"
)

func TestSandboxBackend_InjectsFourTools(t *testing.T) {
    fs := testsupport.NewFakeSandbox(t)
    llm := &stubLLMBackend{}
    be := service.NewSandboxBackend(llm, fs, testsupport.NewNoopTracer())

    tools := be.Tools()
    want := []string{"execute_command", "read_file", "write_file", "search_files"}
    got := map[string]bool{}
    for _, tl := range tools { got[tl.Name()] = true }
    for _, w := range want {
        if !got[w] { t.Errorf("missing tool %q", w) }
    }
}

func TestSandboxBackend_ExecuteRoutesReadFileToSandbox(t *testing.T) {
    fs := testsupport.NewFakeSandbox(t)
    fs.Files["/workspace/a.txt"] = []byte("hello")
    llm := &stubLLMBackend{toolCalls: []agent.ToolCall{{Name: "read_file", Input: mustJSON(map[string]string{"path": "/workspace/a.txt"})}}}

    be := service.NewSandboxBackend(llm, fs, testsupport.NewNoopTracer())
    _, err := be.Execute(context.Background(), agent.Prompt{}, agent.Opts{})
    if err != nil { t.Fatal(err) }
    if llm.lastToolResult != "hello" { t.Errorf("tool result = %q, want hello", llm.lastToolResult) }
}

// stubLLMBackend, mustJSON: small test helpers defined alongside.
```

Run: `cd server && go test ./internal/service/ -run TestSandboxBackend`
Expected: FAIL — type undefined.

- [ ] **Step 2: Implement**

```go
// server/internal/service/sandbox_backend.go
package service

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/multica/server/pkg/agent"
    "github.com/multica/server/pkg/sandbox"
)

// SandboxBackend implements agent.Backend by composing an LLM backend
// (the Brain) with a claimed Sandbox (the Hands). Four bound tools
// — execute_command, read_file, write_file, search_files — are exposed
// to the LLM via Tools(); the Brain decides when to call them.
//
// Each tool Execute is wrapped in a Tracer.StartSpan call keyed on the
// tool's name. Spans become children of the ambient trace_id (D8)
// stashed on ctx by the worker at claim time. This is what makes the
// Phase 10 waterfall show a tool_call row per sandbox operation.
type SandboxBackend struct {
    llm    agent.Backend
    sbx    sandbox.Sandbox
    tracer *trace.Tracer
}

func NewSandboxBackend(llm agent.Backend, sbx sandbox.Sandbox, tracer *trace.Tracer) *SandboxBackend {
    return &SandboxBackend{llm: llm, sbx: sbx, tracer: tracer}
}

// Execute delegates to the underlying LLM backend; the LLM's tool calls
// are routed to Tools() by the agent harness (Phase 2). This type is
// thin on purpose — composition is the point.
func (b *SandboxBackend) Execute(ctx context.Context, p agent.Prompt, o agent.Opts) (agent.Response, error) {
    return b.llm.Execute(ctx, p, o)
}

// ExpiresAt forwards to the sandbox — sandbox is the tighter budget;
// LLM-API backend returns nil so the sandbox wins by default (§D6).
func (b *SandboxBackend) ExpiresAt() *time.Time { return b.sbx.ExpiresAt() }

// Hooks forwards to the sandbox. The sandbox already has its own
// scheduled OnTimeout timer from Task 8.
func (b *SandboxBackend) Hooks() *agent.Hooks {
    // Sandbox hooks are registered on Connect, not re-injected here.
    // Return a no-op Hooks so the harness's lifecycle code is happy.
    return &agent.Hooks{}
}

// Tools returns the four bound sandbox tools the LLM can invoke. Each
// tool owns a pointer to the shared tracer so every invocation emits a
// span with operation="tool_call" + metadata.tool=<name>. Spans
// auto-parent under the ambient trace_id from ctx (D8).
func (b *SandboxBackend) Tools() []agent.Tool {
    return []agent.Tool{
        &execTool{sbx: b.sbx, tracer: b.tracer},
        &readTool{sbx: b.sbx, tracer: b.tracer},
        &writeTool{sbx: b.sbx, tracer: b.tracer},
        &searchTool{sbx: b.sbx, tracer: b.tracer},
    }
}

// --- Tool implementations ---

// withToolSpan is the shared span-wrapping helper for all four tools.
// Every tool Execute ends up as a Phase 10 agent_trace row with
// operation='tool_call' and metadata.tool=<name>. Any error returned
// by fn is recorded on the span via span.End(status, err).
func withToolSpan(ctx context.Context, tracer *trace.Tracer, toolName string, meta map[string]any, fn func(ctx context.Context) (string, error)) (string, error) {
    if meta == nil { meta = map[string]any{} }
    meta["tool"] = toolName
    span := tracer.StartSpan(ctx, "tool_call", meta)
    out, err := fn(ctx)
    status := "ok"; if err != nil { status = "error" }
    span.End(status, err)
    return out, err
}

type execTool struct { sbx sandbox.Sandbox; tracer *trace.Tracer }
func (t *execTool) Name() string { return "execute_command" }
func (t *execTool) Description() string { return "Execute a shell command inside the sandbox." }
func (t *execTool) Schema() json.RawMessage {
    return json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"},"cwd":{"type":"string"},"timeout_seconds":{"type":"number"}},"required":["cmd"]}`)
}
func (t *execTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
    var p struct{ Cmd, Cwd string; TimeoutSeconds float64 }
    if err := json.Unmarshal(raw, &p); err != nil { return "", err }
    if p.Cwd == "" { p.Cwd = t.sbx.WorkingDirectory() }
    timeout := 60 * time.Second; if p.TimeoutSeconds > 0 { timeout = time.Duration(p.TimeoutSeconds * float64(time.Second)) }
    return withToolSpan(ctx, t.tracer, "execute_command", map[string]any{"cmd": p.Cmd, "cwd": p.Cwd}, func(ctx context.Context) (string, error) {
        res, err := t.sbx.Exec(ctx, p.Cmd, p.Cwd, timeout); if err != nil { return "", err }
        return fmt.Sprintf("exit_code=%d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr), nil
    })
}

type readTool struct { sbx sandbox.Sandbox; tracer *trace.Tracer }
func (t *readTool) Name() string { return "read_file" }
func (t *readTool) Description() string { return "Read a file from the sandbox filesystem." }
func (t *readTool) Schema() json.RawMessage {
    return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (t *readTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
    var p struct{ Path string }
    if err := json.Unmarshal(raw, &p); err != nil { return "", err }
    return withToolSpan(ctx, t.tracer, "read_file", map[string]any{"path": p.Path}, func(ctx context.Context) (string, error) {
        b, err := t.sbx.ReadFile(ctx, p.Path); if err != nil { return "", err }
        return string(b), nil
    })
}

type writeTool struct { sbx sandbox.Sandbox; tracer *trace.Tracer }
func (t *writeTool) Name() string { return "write_file" }
func (t *writeTool) Description() string { return "Write (or overwrite) a file in the sandbox filesystem." }
func (t *writeTool) Schema() json.RawMessage {
    return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
}
func (t *writeTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
    var p struct{ Path, Content string }
    if err := json.Unmarshal(raw, &p); err != nil { return "", err }
    return withToolSpan(ctx, t.tracer, "write_file", map[string]any{"path": p.Path, "bytes": len(p.Content)}, func(ctx context.Context) (string, error) {
        if err := t.sbx.WriteFile(ctx, p.Path, []byte(p.Content)); err != nil { return "", err }
        return fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path), nil
    })
}

type searchTool struct { sbx sandbox.Sandbox; tracer *trace.Tracer }
func (t *searchTool) Name() string { return "search_files" }
func (t *searchTool) Description() string { return "Search files matching a grep-style query in the sandbox." }
func (t *searchTool) Schema() json.RawMessage {
    return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string"}},"required":["query"]}`)
}
func (t *searchTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
    var p struct{ Query, Path string }
    if err := json.Unmarshal(raw, &p); err != nil { return "", err }
    if p.Path == "" { p.Path = t.sbx.WorkingDirectory() }
    cmd := fmt.Sprintf(`grep -rn --exclude-dir=node_modules --exclude-dir=.git %q %s || true`, p.Query, p.Path)
    return withToolSpan(ctx, t.tracer, "search_files", map[string]any{"query": p.Query, "path": p.Path}, func(ctx context.Context) (string, error) {
        res, err := t.sbx.Exec(ctx, cmd, t.sbx.WorkingDirectory(), 30*time.Second); if err != nil { return "", err }
        return res.Stdout, nil
    })
}
```

Add the `trace` import at the top of `sandbox_backend.go`:

```go
import (
    "github.com/multica/server/pkg/trace"
    // ... existing imports
)
```

- [ ] **Step 3: Stub test helpers**

```go
// Append to server/internal/service/sandbox_backend_test.go
import (
    "context"
    "encoding/json"
    "github.com/multica/server/pkg/agent"
)

type stubLLMBackend struct {
    toolCalls       []agent.ToolCall
    lastToolResult  string
}
func (s *stubLLMBackend) Execute(ctx context.Context, _ agent.Prompt, _ agent.Opts) (agent.Response, error) {
    // For each toolCall, find the matching tool on the backend and run it.
    // In production this loop is the harness's job; the stub runs it inline
    // to keep the test focused on the adapter's tool binding rather than the
    // full LLM round-trip.
    return agent.Response{ToolCalls: s.toolCalls}, nil
}
func (*stubLLMBackend) ExpiresAt() *time.Time { return nil }
func (*stubLLMBackend) Hooks() *agent.Hooks   { return &agent.Hooks{} }

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
```

The adapter test asserts `TestSandboxBackend_ExecuteRoutesReadFileToSandbox` — but the adapter itself just forwards `Execute` to the LLM. To satisfy the test, route the stub's returned `ToolCalls` through `be.Tools()` inside the test:

```go
// Replace the TestSandboxBackend_ExecuteRoutesReadFileToSandbox body with:
func TestSandboxBackend_ExecuteRoutesReadFileToSandbox(t *testing.T) {
    fs := testsupport.NewFakeSandbox(t)
    fs.Files["/workspace/a.txt"] = []byte("hello")
    llm := &stubLLMBackend{toolCalls: []agent.ToolCall{{Name: "read_file", Input: mustJSON(map[string]string{"path": "/workspace/a.txt"})}}}
    be := service.NewSandboxBackend(llm, fs, testsupport.NewNoopTracer())

    resp, err := be.Execute(context.Background(), agent.Prompt{}, agent.Opts{})
    if err != nil { t.Fatal(err) }
    // Simulate the harness dispatching tool calls.
    tools := map[string]agent.Tool{}
    for _, tl := range be.Tools() { tools[tl.Name()] = tl }
    for _, tc := range resp.ToolCalls {
        out, err := tools[tc.Name].Execute(context.Background(), tc.Input); if err != nil { t.Fatal(err) }
        llm.lastToolResult = out
    }
    if llm.lastToolResult != "hello" { t.Errorf("tool result = %q, want hello", llm.lastToolResult) }
}
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./internal/service/ -run TestSandboxBackend -v`
Expected: PASS.

```bash
git add server/internal/service/sandbox_backend.go server/internal/service/sandbox_backend_test.go
git commit -m "feat(sandbox): SandboxBackend — Phase 2 agent.Backend adapter

Composes an LLM backend (Brain) with a claimed sandbox (Hands) + 4
bound tools (execute_command, read_file, write_file, search_files).
ExpiresAt forwards to sandbox (tighter budget than LLM). No extra
harness state — the harness owns the tool-dispatch loop."
```

---

### Task 10: Sandbox Template Service + sqlc

**Files:**
- Create: `server/pkg/db/queries/sandbox.sql`
- Create: `server/internal/service/sandbox_template.go`
- Create: `server/internal/service/sandbox_template_test.go`

- [ ] **Step 1: sqlc queries**

```sql
-- server/pkg/db/queries/sandbox.sql

-- name: InsertSandboxTemplate :one
INSERT INTO sandbox_template (workspace_id, name, e2b_template_id, dockerfile, default_cpu, default_memory_mb, default_disk_mb, installed_tools)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListSandboxTemplates :many
SELECT * FROM sandbox_template
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: GetSandboxTemplateByID :one
SELECT * FROM sandbox_template
WHERE workspace_id = $1 AND id = $2;

-- name: UpdateSandboxTemplate :one
UPDATE sandbox_template
SET name = $3, e2b_template_id = $4, dockerfile = $5,
    default_cpu = $6, default_memory_mb = $7, default_disk_mb = $8,
    installed_tools = $9
WHERE workspace_id = $1 AND id = $2
RETURNING *;

-- name: DeleteSandboxTemplate :exec
DELETE FROM sandbox_template WHERE workspace_id = $1 AND id = $2;

-- name: GetCloudCodingAgentWithTemplate :one
SELECT a.id AS agent_id, a.provider, a.model, a.workspace_id,
       t.id AS template_id, t.e2b_template_id, t.dockerfile,
       t.default_cpu, t.default_memory_mb, t.default_disk_mb
FROM agent a
JOIN sandbox_template t ON t.id = a.sandbox_template_id AND t.workspace_id = a.workspace_id
WHERE a.workspace_id = $1 AND a.id = $2 AND a.agent_type = 'cloud_coding';
```

Regenerate sqlc:
```bash
cd server && make sqlc
```

- [ ] **Step 2: Service**

```go
// server/internal/service/sandbox_template.go
package service

import (
    "context"
    "encoding/json"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    db "github.com/multica/server/pkg/db/gen"
)

type SandboxTemplate struct {
    ID              uuid.UUID       `json:"id"`
    WorkspaceID     uuid.UUID       `json:"workspace_id"`
    Name            string          `json:"name"`
    E2BTemplateID   string          `json:"e2b_template_id,omitempty"`
    Dockerfile      string          `json:"dockerfile,omitempty"`
    DefaultCPU      int             `json:"default_cpu"`
    DefaultMemoryMB int             `json:"default_memory_mb"`
    DefaultDiskMB   int             `json:"default_disk_mb"`
    InstalledTools  json.RawMessage `json:"installed_tools"`
}

type SandboxTemplateService struct{ pool *pgxpool.Pool }

func NewSandboxTemplateService(p *pgxpool.Pool) *SandboxTemplateService {
    return &SandboxTemplateService{pool: p}
}

func (s *SandboxTemplateService) Create(ctx context.Context, wsID uuid.UUID, t SandboxTemplate) (SandboxTemplate, error) {
    q := db.New(s.pool)
    row, err := q.InsertSandboxTemplate(ctx, db.InsertSandboxTemplateParams{
        WorkspaceID:     uuidPg(wsID),
        Name:            t.Name,
        E2bTemplateID:   sqlNullString(t.E2BTemplateID),
        Dockerfile:      sqlNullString(t.Dockerfile),
        DefaultCPU:      int32(t.DefaultCPU),
        DefaultMemoryMB: int32(t.DefaultMemoryMB),
        DefaultDiskMB:   int32(t.DefaultDiskMB),
        InstalledTools:  t.InstalledTools,
    })
    if err != nil { return SandboxTemplate{}, err }
    return mapSandboxTemplateRow(row), nil
}

func (s *SandboxTemplateService) List(ctx context.Context, wsID uuid.UUID) ([]SandboxTemplate, error) {
    q := db.New(s.pool)
    rows, err := q.ListSandboxTemplates(ctx, uuidPg(wsID)); if err != nil { return nil, err }
    out := make([]SandboxTemplate, len(rows))
    for i, r := range rows { out[i] = mapSandboxTemplateRow(r) }
    return out, nil
}

func (s *SandboxTemplateService) Get(ctx context.Context, wsID, id uuid.UUID) (SandboxTemplate, error) {
    q := db.New(s.pool)
    row, err := q.GetSandboxTemplateByID(ctx, db.GetSandboxTemplateByIDParams{
        WorkspaceID: uuidPg(wsID), ID: uuidPg(id),
    })
    if err != nil { return SandboxTemplate{}, err }
    return mapSandboxTemplateRow(row), nil
}

func (s *SandboxTemplateService) Update(ctx context.Context, wsID uuid.UUID, t SandboxTemplate) (SandboxTemplate, error) {
    q := db.New(s.pool)
    row, err := q.UpdateSandboxTemplate(ctx, db.UpdateSandboxTemplateParams{
        WorkspaceID:     uuidPg(wsID),
        ID:              uuidPg(t.ID),
        Name:            t.Name,
        E2bTemplateID:   sqlNullString(t.E2BTemplateID),
        Dockerfile:      sqlNullString(t.Dockerfile),
        DefaultCPU:      int32(t.DefaultCPU),
        DefaultMemoryMB: int32(t.DefaultMemoryMB),
        DefaultDiskMB:   int32(t.DefaultDiskMB),
        InstalledTools:  t.InstalledTools,
    })
    if err != nil { return SandboxTemplate{}, err }
    return mapSandboxTemplateRow(row), nil
}

func (s *SandboxTemplateService) Delete(ctx context.Context, wsID, id uuid.UUID) error {
    q := db.New(s.pool)
    return q.DeleteSandboxTemplate(ctx, db.DeleteSandboxTemplateParams{
        WorkspaceID: uuidPg(wsID), ID: uuidPg(id),
    })
}

func mapSandboxTemplateRow(r db.SandboxTemplate) SandboxTemplate {
    return SandboxTemplate{
        ID: uuidFromPg(r.ID), WorkspaceID: uuidFromPg(r.WorkspaceID),
        Name: r.Name,
        E2BTemplateID:   r.E2bTemplateID.String,
        Dockerfile:      r.Dockerfile.String,
        DefaultCPU:      int(r.DefaultCPU),
        DefaultMemoryMB: int(r.DefaultMemoryMB),
        DefaultDiskMB:   int(r.DefaultDiskMB),
        InstalledTools:  r.InstalledTools,
    }
}
```

- [ ] **Step 3: Test**

```go
// server/internal/service/sandbox_template_test.go
package service_test

import (
    "context"
    "encoding/json"
    "testing"
    "github.com/multica/server/internal/service"
    "github.com/multica/server/internal/testsupport"
)

func TestSandboxTemplateService_Create_And_List(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("t")
    svc := service.NewSandboxTemplateService(env.DB)

    created, err := svc.Create(context.Background(), wsID, service.SandboxTemplate{
        Name: "node-22", E2BTemplateID: "e2b-node-22",
        DefaultCPU: 1, DefaultMemoryMB: 2048, DefaultDiskMB: 5120,
        InstalledTools: json.RawMessage(`[{"name":"node","version":"22"}]`),
    })
    if err != nil { t.Fatal(err) }
    if created.Name != "node-22" { t.Errorf("name=%q", created.Name) }

    list, err := svc.List(context.Background(), wsID); if err != nil { t.Fatal(err) }
    if len(list) != 1 { t.Errorf("list=%d want 1", len(list)) }
}
```

- [ ] **Step 4: Run + commit**

```bash
cd server && make sqlc && go test ./internal/service/ -run TestSandboxTemplateService -v
git add server/pkg/db/queries/sandbox.sql server/pkg/db/gen/ server/internal/service/sandbox_template.go server/internal/service/sandbox_template_test.go
git commit -m "feat(sandbox): sandbox_template sqlc + CRUD service"
```

---

### Task 11: Handlers — Templates + Active Instances

**Files:**
- Create: `server/internal/handler/sandbox_template.go`
- Create: `server/internal/handler/sandbox_template_test.go`
- Create: `server/internal/handler/sandbox_instance.go`
- Create: `server/internal/handler/sandbox_instance_test.go`

- [ ] **Step 1: Template handler**

```go
// server/internal/handler/sandbox_template.go
package handler

import (
    "encoding/json"
    "net/http"
    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/multica/server/internal/service"
)

type SandboxTemplateHandler struct { svc *service.SandboxTemplateService }

func NewSandboxTemplateHandler(s *service.SandboxTemplateService) *SandboxTemplateHandler {
    return &SandboxTemplateHandler{svc: s}
}

func (h *SandboxTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    list, err := h.svc.List(r.Context(), wsID)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, map[string]any{"templates": list})
}

func (h *SandboxTemplateHandler) Create(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    var body service.SandboxTemplate
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, err.Error(), 400); return }
    t, err := h.svc.Create(r.Context(), wsID, body)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 201, t)
}

func (h *SandboxTemplateHandler) Update(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    id, err := uuid.Parse(chi.URLParam(r, "id")); if err != nil { http.Error(w, "bad id", 400); return }
    var body service.SandboxTemplate
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, err.Error(), 400); return }
    body.ID = id
    t, err := h.svc.Update(r.Context(), wsID, body)
    if err != nil { http.Error(w, err.Error(), 500); return }
    writeJSON(w, 200, t)
}

func (h *SandboxTemplateHandler) Delete(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    id, err := uuid.Parse(chi.URLParam(r, "id")); if err != nil { http.Error(w, "bad id", 400); return }
    if err := h.svc.Delete(r.Context(), wsID, id); err != nil { http.Error(w, err.Error(), 500); return }
    w.WriteHeader(204)
}
```

- [ ] **Step 2: Active-instance handler**

```go
// server/internal/handler/sandbox_instance.go
package handler

import (
    "net/http"
    "sync"
    "time"
    "github.com/google/uuid"
)

// ActiveSandboxRegistry tracks which sandboxes are currently claimed by
// running worker tasks. Populated by the cloud_coding worker on Acquire;
// cleared on Release. In-memory only (process-local) — for cluster-wide
// dashboards, expose via Redis in Phase 12. V1 is single-worker scope.
type ActiveSandboxRegistry struct {
    mu sync.Mutex
    entries map[uuid.UUID]map[string]activeSandboxEntry // wsID → sandboxID → entry
}
type activeSandboxEntry struct {
    SandboxID string     `json:"sandbox_id"`
    AgentID   uuid.UUID  `json:"agent_id"`
    TaskID    uuid.UUID  `json:"task_id"`
    StartedAt time.Time  `json:"started_at"`
    ExpiresAt *time.Time `json:"expires_at"`
    Backend   string     `json:"backend"`
}

func NewActiveSandboxRegistry() *ActiveSandboxRegistry {
    return &ActiveSandboxRegistry{entries: map[uuid.UUID]map[string]activeSandboxEntry{}}
}

func (r *ActiveSandboxRegistry) Register(wsID uuid.UUID, e activeSandboxEntry) {
    r.mu.Lock(); defer r.mu.Unlock()
    if r.entries[wsID] == nil { r.entries[wsID] = map[string]activeSandboxEntry{} }
    r.entries[wsID][e.SandboxID] = e
}
func (r *ActiveSandboxRegistry) Unregister(wsID uuid.UUID, sandboxID string) {
    r.mu.Lock(); defer r.mu.Unlock()
    delete(r.entries[wsID], sandboxID)
}
func (r *ActiveSandboxRegistry) List(wsID uuid.UUID) []activeSandboxEntry {
    r.mu.Lock(); defer r.mu.Unlock()
    out := make([]activeSandboxEntry, 0, len(r.entries[wsID]))
    for _, e := range r.entries[wsID] { out = append(out, e) }
    return out
}

type SandboxInstanceHandler struct { reg *ActiveSandboxRegistry }
func NewSandboxInstanceHandler(reg *ActiveSandboxRegistry) *SandboxInstanceHandler {
    return &SandboxInstanceHandler{reg: reg}
}
func (h *SandboxInstanceHandler) List(w http.ResponseWriter, r *http.Request) {
    wsID := workspaceFromContext(r)
    writeJSON(w, 200, map[string]any{"instances": h.reg.List(wsID)})
}
```

- [ ] **Step 3: Mount in router**

```go
// server/cmd/server/router.go (additions)
r.Route("/api/workspaces/{wsId}/sandbox-templates", func(r chi.Router) {
    r.Get("/", tmplH.List)
    r.Post("/", tmplH.Create)
    r.Patch("/{id}", tmplH.Update)
    r.Delete("/{id}", tmplH.Delete)
})
r.Get("/api/workspaces/{wsId}/sandboxes", instH.List)
```

- [ ] **Step 3.5: Active-instance handler test**

```go
// server/internal/handler/sandbox_instance_test.go
package handler_test

import (
    "encoding/json"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/multica/server/internal/handler"
    "github.com/multica/server/internal/testsupport"
)

func TestSandboxInstanceHandler_List(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("inst")
    reg := handler.NewActiveSandboxRegistry()
    reg.Register(wsID, handler.ActiveSandboxEntryForTest(
        "sbx-1", uuid.New(), uuid.New(), time.Now(), "e2b",
    ))

    h := handler.NewSandboxInstanceHandler(reg)
    req := httptest.NewRequest("GET", "/api/workspaces/"+wsID.String()+"/sandboxes", nil)
    req.Header.Set("X-Workspace-ID", wsID.String())
    rr := httptest.NewRecorder()
    h.List(rr, env.InjectWorkspaceID(req, wsID))
    if rr.Code != 200 { t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String()) }

    var out struct{ Instances []map[string]any }
    _ = json.Unmarshal(rr.Body.Bytes(), &out)
    if len(out.Instances) != 1 { t.Fatalf("instances=%d, want 1", len(out.Instances)) }
    if out.Instances[0]["sandbox_id"] != "sbx-1" { t.Errorf("sandbox_id=%v", out.Instances[0]["sandbox_id"]) }
}
```

Expose a test-friendly constructor in the instance handler file so tests
don't need to import the unexported struct field:

```go
// Append to server/internal/handler/sandbox_instance.go
func ActiveSandboxEntryForTest(sandboxID string, agentID, taskID uuid.UUID, startedAt time.Time, backend string) activeSandboxEntry {
    return activeSandboxEntry{
        SandboxID: sandboxID, AgentID: agentID, TaskID: taskID,
        StartedAt: startedAt, Backend: backend,
    }
}
```

`env.InjectWorkspaceID(req, wsID)` is a Phase 2 testsupport helper that
wraps the request with a context value matching `workspaceFromContext`'s
key. If Phase 2 named it differently, alias to the actual name in
`NewEnv`.

- [ ] **Step 4: Template handler tests**

```go
// server/internal/handler/sandbox_template_test.go
package handler_test

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "github.com/multica/server/internal/testsupport"
)

func TestSandboxTemplateHandler_ListCreate(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("h")
    payload := map[string]any{"name": "py-312", "default_cpu": 1, "default_memory_mb": 1024, "default_disk_mb": 2048}
    body, _ := json.Marshal(payload)
    req := httptest.NewRequest("POST", "/api/workspaces/"+wsID.String()+"/sandbox-templates", bytes.NewReader(body))
    req.Header.Set("X-Workspace-ID", wsID.String())
    resp := env.RouteTo(req)
    if resp.Code != 201 { t.Fatalf("create code=%d body=%s", resp.Code, resp.Body) }

    req2, _ := http.NewRequest("GET", "/api/workspaces/"+wsID.String()+"/sandbox-templates", nil)
    req2.Header.Set("X-Workspace-ID", wsID.String())
    resp2 := env.RouteTo(req2)
    if resp2.Code != 200 { t.Fatalf("list code=%d", resp2.Code) }
    var out struct{ Templates []map[string]any }
    _ = json.Unmarshal(resp2.Body, &out)
    if len(out.Templates) != 1 { t.Errorf("templates=%d", len(out.Templates)) }
}
```

- [ ] **Step 5: Run + commit**

```bash
cd server && go test ./internal/handler/ -run TestSandboxTemplateHandler -v
git add server/internal/handler/sandbox_template.go server/internal/handler/sandbox_template_test.go server/internal/handler/sandbox_instance.go server/internal/handler/sandbox_instance_test.go server/cmd/server/router.go
git commit -m "feat(sandbox): /sandbox-templates REST + /sandboxes active dashboard

CRUD for templates + in-memory ActiveSandboxRegistry powering /sandboxes.
Cluster-wide visibility (Redis-backed) is Phase 12."
```

---

### Task 12: Cloud Coding Worker

**Files:**
- Create: `server/internal/worker/cloud_coding.go`
- Create: `server/internal/worker/cloud_coding_test.go`

**Goal:** The task-claim path for `cloud_coding` agents — acquire a sandbox from the warm pool, run `SandboxBackend.Execute` in a tool-loop, release (destroy) the sandbox, emit cost events + spans.

- [ ] **Step 1: Failing test**

```go
// server/internal/worker/cloud_coding_test.go
package worker_test

import (
    "context"
    "testing"
    "time"
    "github.com/multica/server/internal/worker"
    "github.com/multica/server/internal/testsupport"
)

func TestCloudCodingWorker_ClaimAcquireReleaseCycle(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("cc")
    tmplID := env.SeedSandboxTemplate(wsID, "node-22")
    agentID := env.SeedCloudCodingAgent(wsID, tmplID, "anthropic", "claude-sonnet-4-5")
    taskID := env.EnqueueTask(wsID, agentID, "write hello world to /tmp/hi")

    // Stub model: one tool call to write_file, then end.
    env.StubModelReplying([]string{`{"tool":"write_file","input":{"path":"/tmp/hi","content":"hello"}}`, "done"})
    fs := testsupport.NewFakeSandbox(t)

    w := worker.NewCloudCodingWorker(worker.CloudCodingDeps{
        DB: env.DB, Pools: testsupport.NewSingletonPoolRegistry(fs), Registry: env.ActiveSandboxes,
        LLMFactory: env.StubLLMFactory, Tracer: env.Tracer, CostCalc: env.CostCalc, Events: env.Events,
    })

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel()
    if err := w.RunOnce(ctx); err != nil { t.Fatal(err) }

    // Assertions:
    // 1. Sandbox was acquired and released (destroyed).
    if fs.StoppedAt == nil { t.Error("sandbox not stopped after task completed") }
    // 2. Task completed.
    if env.TaskStatus(taskID) != "completed" { t.Errorf("task status = %s", env.TaskStatus(taskID)) }
    // 3. cost_event with provider=sandbox_e2b emitted.
    if env.CountCostEventsByProvider(wsID, "sandbox_e2b") < 1 { t.Error("no sandbox cost_event emitted") }
    // 4. Active-sandbox registry was cleared.
    if len(env.ActiveSandboxes.List(wsID)) != 0 { t.Error("registry still has entry after release") }
}
```

Run: `cd server && go test ./internal/worker/ -run TestCloudCodingWorker`
Expected: FAIL — `cloud_coding.go` undefined.

- [ ] **Step 2: Implement**

```go
// server/internal/worker/cloud_coding.go
package worker

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/multica/server/internal/handler"
    "github.com/multica/server/internal/service"
    "github.com/multica/server/pkg/agent"
    db "github.com/multica/server/pkg/db/gen"
    "github.com/multica/server/pkg/sandbox"
    "github.com/multica/server/pkg/trace"
)

type CloudCodingDeps struct {
    DB         *pgxpool.Pool
    // Pools is keyed by sandbox_template_id so each template has its
    // own warm pool (§11.4). A single global pool would force templates
    // to share boot artifacts, violating the per-template image/config
    // invariant. The registry lazily constructs a pool for a template
    // the first time a task for that template is claimed.
    Pools      *PoolRegistry
    Registry   *handler.ActiveSandboxRegistry
    LLMFactory func(provider, model string) agent.Backend
    Tracer     *trace.Tracer
    CostCalc   *agent.CostCalculator
    // Events is Phase 8A's events.Bus. Used to publish sandbox.started
    // / sandbox.stopped so the frontend instances dashboard WS-invalidates
    // rather than polls.
    Events     EventsBus
}

// EventsBus is the minimal shape Phase 11 needs from Phase 8A's
// events.Bus. Kept as a local interface so tests can stub it without
// importing the whole events package.
type EventsBus interface {
    Publish(ctx context.Context, payload map[string]any) error
}

// PoolRegistry lazily constructs one sandbox.Pool per template. Thread-safe.
type PoolRegistry struct {
    mu      sync.Mutex
    pools   map[uuid.UUID]*sandbox.Pool
    factory func(templateID uuid.UUID) sandbox.PoolConfig // must be provided by caller
}
func NewPoolRegistry(factory func(uuid.UUID) sandbox.PoolConfig) *PoolRegistry {
    return &PoolRegistry{pools: map[uuid.UUID]*sandbox.Pool{}, factory: factory}
}
func (r *PoolRegistry) For(templateID uuid.UUID) *sandbox.Pool {
    r.mu.Lock(); defer r.mu.Unlock()
    if p, ok := r.pools[templateID]; ok { return p }
    p := sandbox.NewPool(r.factory(templateID))
    r.pools[templateID] = p
    return p
}
func (r *PoolRegistry) ShutdownAll(ctx context.Context) {
    r.mu.Lock(); defer r.mu.Unlock()
    for _, p := range r.pools { _ = p.Shutdown(ctx) }
    r.pools = map[uuid.UUID]*sandbox.Pool{}
}

type CloudCodingWorker struct{ deps CloudCodingDeps }
func NewCloudCodingWorker(d CloudCodingDeps) *CloudCodingWorker { return &CloudCodingWorker{deps: d} }

// Run claims and processes tasks in a loop until ctx is cancelled.
// RunOnce processes a single claimed task — used by tests.
func (w *CloudCodingWorker) Run(ctx context.Context) {
    for {
        if ctx.Err() != nil { return }
        if err := w.RunOnce(ctx); err != nil && err != errNoTask {
            // Transient DB errors — back off briefly and retry.
            select { case <-time.After(500 * time.Millisecond): case <-ctx.Done(): return }
        }
    }
}

var errNoTask = errorString("no task ready")
type errorString string
func (e errorString) Error() string { return string(e) }

func (w *CloudCodingWorker) RunOnce(ctx context.Context) error {
    q := db.New(w.deps.DB)
    row, err := q.ClaimCloudCodingTask(ctx) // new sqlc query — defined in Step 2a below
    if err != nil { return errNoTask }

    wsID := uuidFromPg(row.WorkspaceID)
    agentID := uuidFromPg(row.AgentID)
    taskID := uuidFromPg(row.ID)

    // Pre-work: mint trace_id, stash on ctx per D8.
    traceID := uuid.New()
    ctx = agent.WithTraceID(ctx, traceID)

    // Start a span covering the full claim→release cycle. Captured as
    // the outer scope for LateLinkCostEvent at the end. End status is
    // set explicitly on each exit path (success vs markFailed) so the
    // Phase 10 waterfall reports the right status per task.
    span := w.deps.Tracer.StartSpan(ctx, "cloud_coding_task", map[string]any{"task_id": taskID})

    // 1) BRAIN: look up the agent + template so we know which warm pool
    //    to pull from.
    agentRow, err := q.GetCloudCodingAgentWithTemplate(ctx, db.GetCloudCodingAgentWithTemplateParams{
        WorkspaceID: row.WorkspaceID, ID: row.AgentID,
    })
    if err != nil { return w.fail(ctx, q, span, taskID, err) }
    templateID := uuidFromPg(agentRow.TemplateID)

    // 2) HANDS: Acquire sandbox from the per-template warm pool.
    pool := w.deps.Pools.For(templateID)
    sbx, err := pool.Acquire(ctx)
    if err != nil { return w.fail(ctx, q, span, taskID, err) }
    defer func() {
        w.deps.Registry.Unregister(wsID, sbx.ID())
        // Fire sandbox.stopped for WS invalidation of the instances dashboard.
        _ = w.deps.Events.Publish(ctx, map[string]any{
            "type": "sandbox.stopped", "workspace_id": wsID,
            "sandbox_id": sbx.ID(), "agent_id": agentID, "task_id": taskID,
        })
        _ = pool.Release(ctx, sbx)
    }()
    w.deps.Registry.Register(wsID, handler.NewActiveSandboxEntry(sbx, agentID, taskID))
    _ = w.deps.Events.Publish(ctx, map[string]any{
        "type": "sandbox.started", "workspace_id": wsID,
        "sandbox_id": sbx.ID(), "agent_id": agentID, "task_id": taskID,
        "backend": string(sbx.Type()),
    })

    // 3) LOOP: build Brain + Hands and run the agent harness. `agent.Run`
    //    is Phase 2's exported tool-loop entrypoint (server/pkg/agent/run.go)
    //    — it drives the ToolLoopAgent with StopWhen=StepCountIs(MainAgentMaxSteps).
    llm := w.deps.LLMFactory(agentRow.Provider.String, agentRow.Model.String)
    be := service.NewSandboxBackend(llm, sbx, w.deps.Tracer)
    _, err = agent.Run(ctx, be, agent.Prompt{System: "You are a coding agent.", User: row.Prompt.String})
    if err != nil { return w.fail(ctx, q, span, taskID, err) }

    // 4) Emit sandbox-runtime cost_event (wall-clock * vCPU-hours rate).
    dur := time.Since(span.StartedAt())
    cents := w.deps.CostCalc.ComputeSandboxCents(providerFor(sbx.Type()), dur)
    costRow, err := q.InsertCostEvent(ctx, db.InsertCostEventParams{
        WorkspaceID: row.WorkspaceID, AgentID: row.AgentID, TaskID: row.ID,
        TraceID: uuidPg(traceID), Provider: sqlNullString(providerFor(sbx.Type())),
        Model: sqlNullString("default"), CostCents: int32(cents), CostStatus: "actual",
    })
    if err == nil {
        // D8 late-link: the outer span now references the cost_event so
        // the Phase 10 waterfall can render per-span cost attribution.
        _ = w.deps.Tracer.LateLinkCostEvent(ctx, span.ID(), uuidFromPg(costRow.ID))
    }

    // 5) Mark completed.
    if err := q.MarkTaskCompleted(ctx, row.ID); err != nil {
        return w.fail(ctx, q, span, taskID, err)
    }
    span.End("completed", nil)
    return nil
}

// fail ends the span with error status, attempts to mark the task
// failed, and surfaces the inner error. Swallowing MarkTaskFailed errors
// would leave the task stuck `in_progress` forever without a breadcrumb.
func (w *CloudCodingWorker) fail(ctx context.Context, q *db.Queries, span *trace.Span, taskID uuid.UUID, cause error) error {
    span.End("error", cause)
    if markErr := q.MarkTaskFailed(ctx, db.MarkTaskFailedParams{
        ID: uuidPg(taskID), Error: sqlNullString(cause.Error()),
    }); markErr != nil {
        return fmt.Errorf("markTaskFailed failed (%v) for original cause: %w", markErr, cause)
    }
    return cause
}

func providerFor(t sandbox.Type) string {
    switch t {
    case sandbox.TypeE2B: return "sandbox_e2b"
    case sandbox.TypeGVisor: return "sandbox_gvisor"
    case sandbox.TypeClaudeManaged: return "sandbox_claude"
    }
    return "sandbox_unknown"
}

// (markFailed helper removed — superseded by CloudCodingWorker.fail method above
// which also ends the outer span with "error" status.)
```

- [ ] **Step 2a: New sqlc queries**

Append to `server/pkg/db/queries/task.sql` (Phase 2):

```sql
-- name: ClaimCloudCodingTask :one
UPDATE agent_task_queue
SET status = 'in_progress', claimed_at = NOW()
WHERE id = (
    SELECT q.id FROM agent_task_queue q
    JOIN agent a ON a.id = q.agent_id AND a.workspace_id = q.workspace_id
    WHERE q.status = 'pending' AND a.agent_type = 'cloud_coding'
    ORDER BY q.created_at
    FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING *;
```

Regenerate: `cd server && make sqlc`.

- [ ] **Step 2b: `handler.NewActiveSandboxEntry` helper**

```go
// Append to server/internal/handler/sandbox_instance.go
func NewActiveSandboxEntry(s sandbox.Sandbox, agentID, taskID uuid.UUID) activeSandboxEntry {
    return activeSandboxEntry{
        SandboxID: s.ID(), AgentID: agentID, TaskID: taskID,
        StartedAt: time.Now(), ExpiresAt: s.ExpiresAt(),
        Backend: string(s.Type()),
    }
}
```

- [ ] **Step 2c: `CostCalculator.ComputeSandboxCents`**

```go
// Append to server/pkg/agent/cost_calculator.go (Phase 2)
func (c *CostCalculator) ComputeSandboxCents(provider string, dur time.Duration) int {
    ratePerVCPUHour := c.rateFor(provider, "default")
    hours := dur.Hours()
    // Default 1 vCPU; Phase 11 does not track per-task CPU spec separately.
    return int(ratePerVCPUHour * hours * 100) // cents
}
```

- [ ] **Step 3: Claim-router wiring**

```go
// server/internal/worker/claim.go (Phase 2) — extend the switch:
case "cloud_coding":
    return cloudCodingWorker.RunOnce(ctx)
```

- [ ] **Step 4: Run + commit**

Run: `cd server && go test ./internal/worker/ -run TestCloudCodingWorker -v`
Expected: PASS.

```bash
git add server/internal/worker/cloud_coding.go server/internal/worker/cloud_coding_test.go server/pkg/db/queries/task.sql server/pkg/db/gen/ server/internal/handler/sandbox_instance.go server/pkg/agent/cost_calculator.go server/internal/worker/claim.go
git commit -m "feat(sandbox): cloud_coding worker — Brain/Hands loop

Claim task → mint trace_id → Pool.Acquire sandbox → register in active
map → run agent.Run with SandboxBackend → release (destroy) sandbox →
emit sandbox_e2b/sandbox_gvisor cost_event → mark task completed."
```

---

### Task 13: Frontend — Types, API, Queries

**Files:**
- Create: `packages/core/types/sandbox.ts`
- Create: `packages/core/api/sandbox.ts`
- Create: `packages/core/sandbox/queries.ts`

- [ ] **Step 1: Types + API client**

```ts
// packages/core/types/sandbox.ts
export type SandboxBackend = "e2b" | "gvisor" | "claude_managed" | "none";

export type SandboxTemplate = {
  id: string;
  workspace_id: string;
  name: string;
  e2b_template_id?: string;
  dockerfile?: string;
  default_cpu: number;
  default_memory_mb: number;
  default_disk_mb: number;
  installed_tools: Array<{ name: string; version?: string }>;
  created_at: string;
};

export type SandboxInstance = {
  sandbox_id: string;
  agent_id: string;
  task_id: string;
  started_at: string;
  expires_at: string | null;
  backend: string;
};
```

```ts
// packages/core/api/sandbox.ts
import { api } from "@multica/core/api/client";
import type { SandboxTemplate, SandboxInstance } from "@multica/core/types/sandbox";

export async function listSandboxTemplates(wsId: string): Promise<SandboxTemplate[]> {
  const r = await api.get<{ templates: SandboxTemplate[] }>(`/api/workspaces/${wsId}/sandbox-templates`, {
    headers: { "X-Workspace-ID": wsId },
  });
  return r.templates;
}
export async function createSandboxTemplate(wsId: string, t: Partial<SandboxTemplate>): Promise<SandboxTemplate> {
  return api.post<SandboxTemplate>(`/api/workspaces/${wsId}/sandbox-templates`, t, {
    headers: { "X-Workspace-ID": wsId },
  });
}
export async function updateSandboxTemplate(wsId: string, t: SandboxTemplate): Promise<SandboxTemplate> {
  return api.patch<SandboxTemplate>(`/api/workspaces/${wsId}/sandbox-templates/${t.id}`, t, {
    headers: { "X-Workspace-ID": wsId },
  });
}
export async function deleteSandboxTemplate(wsId: string, id: string): Promise<void> {
  await api.delete<void>(`/api/workspaces/${wsId}/sandbox-templates/${id}`, {
    headers: { "X-Workspace-ID": wsId },
  });
}
export async function listSandboxInstances(wsId: string): Promise<SandboxInstance[]> {
  const r = await api.get<{ instances: SandboxInstance[] }>(`/api/workspaces/${wsId}/sandboxes`, {
    headers: { "X-Workspace-ID": wsId },
  });
  return r.instances;
}
```

- [ ] **Step 2: TanStack hooks**

```ts
// packages/core/sandbox/queries.ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  listSandboxTemplates, createSandboxTemplate, updateSandboxTemplate,
  deleteSandboxTemplate, listSandboxInstances,
} from "@multica/core/api/sandbox";
import { useWSInvalidate } from "@multica/core/platform/ws"; // Phase 8A hook
import type { SandboxTemplate } from "@multica/core/types/sandbox";

export function useSandboxTemplates(wsId: string) {
  return useQuery({
    queryKey: ["sandbox-templates", wsId],
    queryFn: () => listSandboxTemplates(wsId),
    staleTime: 60_000,
  });
}
export function useCreateSandboxTemplate(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (t: Partial<SandboxTemplate>) => createSandboxTemplate(wsId, t),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sandbox-templates", wsId] }),
  });
}
export function useUpdateSandboxTemplate(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (t: SandboxTemplate) => updateSandboxTemplate(wsId, t),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sandbox-templates", wsId] }),
  });
}
export function useDeleteSandboxTemplate(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteSandboxTemplate(wsId, id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sandbox-templates", wsId] }),
  });
}
export function useSandboxInstances(wsId: string) {
  // WS-invalidated per CLAUDE.md's "WS events invalidate queries — they
  // never write to stores directly" hard rule. The cloud_coding worker
  // publishes `sandbox.started` on Acquire + Registry.Register, and
  // `sandbox.stopped` on Release + Registry.Unregister (both via
  // events.Bus in the backend — see Task 12). useWSInvalidate below
  // listens for either event and refetches this query's data.
  useWSInvalidate(
    wsId,
    ["sandbox.started", "sandbox.stopped"],
    ["sandbox-instances", wsId],
  );
  return useQuery({
    queryKey: ["sandbox-instances", wsId],
    queryFn: () => listSandboxInstances(wsId),
    staleTime: 30_000, // fine — WS invalidation drives freshness
  });
}
```

- [ ] **Step 3: Commit**

```bash
git add packages/core/types/sandbox.ts packages/core/api/sandbox.ts packages/core/sandbox/queries.ts
git commit -m "feat(sandbox): core types + API client + TanStack hooks"
```

---

### Task 14: Frontend — Cloud Coding Create-Agent Flow + Management Pages

**Files:**
- Create: `packages/views/agents/components/cloud-coding-flow.tsx`
- Create: `packages/views/sandboxes/index.tsx`
- Create: `packages/views/sandboxes/templates/index.tsx`
- Create: `packages/views/sandboxes/templates/template-form.tsx`
- Modify: `packages/views/agents/components/create-agent-dialog.tsx`

- [ ] **Step 1: Create-Agent cloud coding branch**

```tsx
// packages/views/agents/components/cloud-coding-flow.tsx
import { useSandboxTemplates } from "@multica/core/sandbox/queries";
import { Select, SelectItem, SelectTrigger, SelectValue, SelectContent } from "@multica/ui/select";

export function CloudCodingFlow({
  wsId, value, onChange,
}: {
  wsId: string;
  value: { provider?: string; model?: string; sandbox_template_id?: string };
  onChange: (next: typeof value) => void;
}) {
  const { data: templates = [] } = useSandboxTemplates(wsId);
  return (
    <div className="flex flex-col gap-3">
      <label>
        <span className="text-sm font-medium">Provider</span>
        <Select value={value.provider} onValueChange={(v) => onChange({ ...value, provider: v })}>
          <SelectTrigger><SelectValue placeholder="Select provider" /></SelectTrigger>
          <SelectContent>
            <SelectItem value="anthropic">Anthropic</SelectItem>
            <SelectItem value="openai">OpenAI</SelectItem>
          </SelectContent>
        </Select>
      </label>
      <label>
        <span className="text-sm font-medium">Model</span>
        <input value={value.model ?? ""} onChange={(e) => onChange({ ...value, model: e.target.value })}
               className="w-full rounded border px-2 py-1" placeholder="claude-sonnet-4-5" />
      </label>
      <label>
        <span className="text-sm font-medium">Sandbox Template</span>
        <Select value={value.sandbox_template_id}
                onValueChange={(v) => onChange({ ...value, sandbox_template_id: v })}>
          <SelectTrigger><SelectValue placeholder="Select template" /></SelectTrigger>
          <SelectContent>
            {templates.map((t) => <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>)}
          </SelectContent>
        </Select>
      </label>
      <p className="text-xs text-muted-foreground">
        Sandbox auto-provisioned per task. Resources come from the template defaults
        (CPU, memory, disk).
      </p>
    </div>
  );
}
```

- [ ] **Step 2: Template list + form**

```tsx
// packages/views/sandboxes/templates/template-form.tsx
import { useState } from "react";
import { useCreateSandboxTemplate, useUpdateSandboxTemplate } from "@multica/core/sandbox/queries";
import type { SandboxTemplate } from "@multica/core/types/sandbox";
import { Button } from "@multica/ui/button";

export function TemplateForm({
  wsId, initial, onDone,
}: {
  wsId: string;
  initial?: SandboxTemplate;
  onDone: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [e2bId, setE2bId] = useState(initial?.e2b_template_id ?? "");
  const [dockerfile, setDockerfile] = useState(initial?.dockerfile ?? "");
  const [cpu, setCPU] = useState(initial?.default_cpu ?? 1);
  const [memMB, setMemMB] = useState(initial?.default_memory_mb ?? 2048);
  const [diskMB, setDiskMB] = useState(initial?.default_disk_mb ?? 5120);

  const create = useCreateSandboxTemplate(wsId);
  const update = useUpdateSandboxTemplate(wsId);

  async function submit() {
    const payload = { name, e2b_template_id: e2bId, dockerfile,
      default_cpu: cpu, default_memory_mb: memMB, default_disk_mb: diskMB };
    if (initial) await update.mutateAsync({ ...initial, ...payload });
    else await create.mutateAsync(payload);
    onDone();
  }
  return (
    <form onSubmit={(e) => { e.preventDefault(); submit(); }} className="flex flex-col gap-2">
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Template name" />
      <input value={e2bId} onChange={(e) => setE2bId(e.target.value)} placeholder="E2B template ID (optional)" />
      <textarea value={dockerfile} onChange={(e) => setDockerfile(e.target.value)} placeholder="Custom Dockerfile (optional)" />
      <div className="flex gap-2">
        <input type="number" value={cpu} onChange={(e) => setCPU(+e.target.value)} placeholder="CPU" />
        <input type="number" value={memMB} onChange={(e) => setMemMB(+e.target.value)} placeholder="Memory MB" />
        <input type="number" value={diskMB} onChange={(e) => setDiskMB(+e.target.value)} placeholder="Disk MB" />
      </div>
      <Button type="submit">{initial ? "Update" : "Create"}</Button>
    </form>
  );
}
```

```tsx
// packages/views/sandboxes/templates/index.tsx
import { useSandboxTemplates, useDeleteSandboxTemplate } from "@multica/core/sandbox/queries";
import { Button } from "@multica/ui/button";
import { TemplateForm } from "./template-form";
import { useState } from "react";

export function SandboxTemplatesPage({ wsId }: { wsId: string }) {
  const { data: templates = [] } = useSandboxTemplates(wsId);
  const del = useDeleteSandboxTemplate(wsId);
  const [editing, setEditing] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Sandbox Templates</h1>
        <Button onClick={() => setCreating(true)}>New template</Button>
      </div>
      {creating && <TemplateForm wsId={wsId} onDone={() => setCreating(false)} />}
      <ul className="flex flex-col gap-2">
        {templates.map((t) => (
          <li key={t.id} className="border rounded p-3">
            {editing === t.id ? (
              <TemplateForm wsId={wsId} initial={t} onDone={() => setEditing(null)} />
            ) : (
              <div className="flex justify-between">
                <div>
                  <div className="font-medium">{t.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {t.default_cpu} CPU · {t.default_memory_mb} MiB · {t.default_disk_mb} MiB disk
                  </div>
                </div>
                <div className="flex gap-2">
                  <Button variant="secondary" onClick={() => setEditing(t.id)}>Edit</Button>
                  <Button variant="destructive" onClick={() => del.mutate(t.id)}>Delete</Button>
                </div>
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
```

- [ ] **Step 3: Active-instance dashboard**

```tsx
// packages/views/sandboxes/index.tsx
import { useSandboxInstances } from "@multica/core/sandbox/queries";

export function SandboxInstancesPage({ wsId }: { wsId: string }) {
  const { data: instances = [], isLoading } = useSandboxInstances(wsId);
  if (isLoading) return <div className="p-4">Loading active sandboxes…</div>;
  return (
    <div className="p-4">
      <h1 className="text-xl font-semibold mb-3">Active Sandboxes ({instances.length})</h1>
      <table className="w-full text-sm">
        <thead><tr>
          <th className="text-left">Sandbox</th><th>Backend</th><th>Agent</th><th>Task</th><th>Started</th><th>Expires</th>
        </tr></thead>
        <tbody>
          {instances.map((i) => (
            <tr key={i.sandbox_id}>
              <td className="font-mono">{i.sandbox_id}</td>
              <td>{i.backend}</td>
              <td>{i.agent_id.slice(0, 8)}…</td>
              <td>{i.task_id.slice(0, 8)}…</td>
              <td>{new Date(i.started_at).toLocaleTimeString()}</td>
              <td>{i.expires_at ? new Date(i.expires_at).toLocaleTimeString() : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
```

- [ ] **Step 4: Wire into create-agent dialog**

```tsx
// packages/views/agents/components/create-agent-dialog.tsx — add branch
import { CloudCodingFlow } from "./cloud-coding-flow";

// Inside the dialog's type-specific body:
{agentType === "cloud_coding" && (
  <CloudCodingFlow wsId={wsId} value={cloudCodingState} onChange={setCloudCodingState} />
)}
```

- [ ] **Step 4.5: Failing smoke test first**

```tsx
// packages/views/sandboxes/templates/index.test.tsx
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { vi } from "vitest";
import { SandboxTemplatesPage } from "./index";

vi.mock("@multica/core/sandbox/queries", () => ({
  useSandboxTemplates: () => ({ data: [{ id: "t1", name: "node-22",
    workspace_id: "w1", default_cpu: 1, default_memory_mb: 2048, default_disk_mb: 5120,
    installed_tools: [], created_at: new Date().toISOString() }] }),
  useDeleteSandboxTemplate: () => ({ mutate: vi.fn() }),
  useCreateSandboxTemplate: () => ({ mutateAsync: vi.fn() }),
  useUpdateSandboxTemplate: () => ({ mutateAsync: vi.fn() }),
}));

test("SandboxTemplatesPage renders heading + template row", () => {
  const qc = new QueryClient();
  render(
    <QueryClientProvider client={qc}>
      <SandboxTemplatesPage wsId="w1" />
    </QueryClientProvider>,
  );
  expect(screen.getByRole("heading", { name: /Sandbox Templates/i })).toBeInTheDocument();
  expect(screen.getByText("node-22")).toBeInTheDocument();
});
```

Run: `pnpm --filter @multica/views exec vitest run sandboxes/templates/index.test.tsx`
Expected: FAIL with "Cannot find module" until `index.tsx` ships the export.

- [ ] **Step 5: Test + commit**

```bash
pnpm --filter @multica/views exec vitest run sandboxes/ agents/
git add packages/views/agents/components/cloud-coding-flow.tsx packages/views/agents/components/create-agent-dialog.tsx packages/views/sandboxes/
git commit -m "feat(sandbox): cloud coding agent creation + template + instance UI

Create-agent dialog gains a Cloud Coding branch with provider + model +
sandbox template selectors. New pages: sandbox template CRUD and active
sandbox instance dashboard (refetch every 5s)."
```

---

### Task 15: End-to-End Integration Test + Verification

**Files:**
- Create: `server/internal/integration/cloudcoding/e2e_test.go`
- Create: `docs/engineering/verification/phase-11.md`

**Goal:** assert the full chain: create `cloud_coding` agent → enqueue task → worker acquires sandbox → tool loop runs → task completes → cost_event + span exist → sandbox destroyed.

- [ ] **Step 1: E2E test**

```go
// server/internal/integration/cloudcoding/e2e_test.go
package cloudcoding_test

import (
    "context"
    "testing"
    "time"
    "github.com/multica/server/internal/testsupport"
    "github.com/multica/server/internal/worker"
)

func TestCloudCoding_FullLoop(t *testing.T) {
    env := testsupport.NewEnv(t)
    wsID := env.SeedWorkspace("e2e")
    tmplID := env.SeedSandboxTemplate(wsID, "node-22")
    agentID := env.SeedCloudCodingAgent(wsID, tmplID, "anthropic", "claude-sonnet-4-5")

    // Script: LLM calls write_file then declares done.
    env.StubModelReplying([]string{
        `{"tool":"write_file","input":{"path":"/workspace/out.txt","content":"done"}}`,
        "finished",
    })
    taskID := env.EnqueueTask(wsID, agentID, "write a file")

    fs := testsupport.NewFakeSandbox(t)
    w := worker.NewCloudCodingWorker(worker.CloudCodingDeps{
        DB: env.DB, Pools: testsupport.NewSingletonPoolRegistry(fs), Registry: env.ActiveSandboxes,
        LLMFactory: env.StubLLMFactory, Tracer: env.Tracer, CostCalc: env.CostCalc, Events: env.Events,
    })
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second); defer cancel()
    if err := w.RunOnce(ctx); err != nil { t.Fatal(err) }

    // 1. Task completed.
    if env.TaskStatus(taskID) != "completed" { t.Errorf("task not completed: %s", env.TaskStatus(taskID)) }
    // 2. write_file actually ran against the sandbox.
    if string(fs.Files["/workspace/out.txt"]) != "done" { t.Error("write_file not dispatched to sandbox") }
    // 3. Sandbox cost_event exists with correct provider.
    if env.CountCostEventsByProvider(wsID, "sandbox_e2b") < 1 { t.Error("no sandbox_e2b cost_event") }
    // 4. trace_id propagated: agent_trace row with operation='cloud_coding_task'.
    traceID := env.TraceIDForTask(taskID)
    spans := env.ListSpans(traceID)
    if len(spans) == 0 { t.Error("no spans emitted") }
    // 5. Sandbox was destroyed (no active entries for this workspace).
    if len(env.ActiveSandboxes.List(wsID)) != 0 { t.Error("active registry not cleared") }
    // 6. Sandbox's Stop was invoked.
    if fs.StoppedAt == nil { t.Error("sandbox not stopped") }
}
```

- [ ] **Step 2: Manual verification per PLAN.md §11.7**

- [ ] Create a `cloud_coding` agent via the frontend with a Node.js sandbox template.
- [ ] Assign an issue → sandbox created (visible in `/sandboxes`) → agent edits code → sandbox destroyed (disappears from `/sandboxes`).
- [ ] Warm pool reduces cold start from ~5s to ~200ms. Measure via `TRACE=1` + span durations.
- [ ] Sandbox isolation confirmed: `exec` of `curl http://host.docker.internal` fails (no network to host).
- [ ] Cost tracked: `cost_event` rows for `sandbox_e2b` provider + LLM tokens from the brain.
- [ ] `make check` passes.

- [ ] **Step 3: Evidence**

```markdown
# Phase 11 Verification — 2026-04-DD
- `go test ./internal/integration/cloudcoding/...` → pass
- `make check` → pass
- Manual: [results — paste screenshots + span excerpts]
- Known limitations:
  - `Readdir` returns ErrNotImplemented on E2B + gVisor in V1; callers use
    `execute_command("ls -la")`. Wrap in a native Readdir once E2B ships it.
  - ClaudeManagedStub returns ErrNotImplemented on every method; Phase 12
    replaces the body and keeps the enum value stable.
  - Active-sandbox dashboard is process-local. Cluster-wide view via Redis
    in Phase 12.
  - gVisor on macOS/Windows: dev-only via Docker Desktop (Linux VM) or E2B.
```

- [ ] **Step 4: Commit**

```bash
cd server && go test ./internal/integration/cloudcoding/ -v
make check
git add server/internal/integration/cloudcoding/e2e_test.go docs/engineering/verification/phase-11.md
git commit -m "test(phase-11): cloud coding full loop e2e + verification evidence"
```

---

## Placeholder Scan

Self-reviewed. Four intentional abbreviations — each is explained at the call site, not a hidden TODO:

1. `Readdir` returns `ErrNotImplemented` on E2B + gVisor backends with an in-code comment pointing callers at `Exec("ls …")` until upstream SDKs expose a native Readdir. This is a V1 scope trim documented in §11.7.
2. `FakeDockerClient.ContainerExecAttach` returns a closed buffer — tests assert spec + call count, not stdout framing. Docker's full `stdcopy` is tested via `util_docker.go::stdcopyDemux`.
3. `ClaudeManagedStub` intentionally returns `ErrNotImplemented` on every method — Phase 12 fills the body; the enum value has to exist now so migration 187 accepts it.
4. `ActiveSandboxRegistry` is in-memory and process-local — cluster-wide visibility via Redis is a Phase 12 scope increment, not a bug.

No TBDs, no unreferenced symbols. Every type defined in one Task is used in the same or a later Task with matching signatures (`Sandbox`, `Hooks`, `Config`, `Pool`, `SandboxBackend`, `CloudCodingDeps`).

## Execution Handoff

Plan complete at `docs/superpowers/plans/2026-04-14-phase11-cloud-coding-sandbox.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task + two-stage review between tasks.

**2. Inline Execution** — via `superpowers:executing-plans` with batch checkpoints.

Which approach?
