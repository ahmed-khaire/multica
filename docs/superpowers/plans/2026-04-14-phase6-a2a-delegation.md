# Phase 6: Agent-to-Agent Delegation (via go-ai SubagentRegistry)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a parent agent can invoke `task(subagentType, description, instructions)` to delegate to another agent in the same workspace. The child runs under the parent's `trace_id`, its cost events roll up under the chain, interim progress streams back to the parent via WS, and cycle/depth/budget checks block runaway delegation chains.

**Architecture:** delegation is a single `task` tool — **authored by Phase 6 itself** — on top of go-ai's bare `SubagentRegistry` map-of-slugs-to-agents dispatch primitive (D2 audit: `other-projects/go-ai/pkg/agent/subagent.go` is a `map[string]Agent`; go-ai v0.4.0 does NOT ship a `task` tool, no depth/cycle plumbing, no built-in trace plumbing — all of that is this phase's responsibility). No `agent_delegation` or `agent_signal` tables. The harness mints a delegation-stack + depth in `context.Context` (distinct from the D8 `trace_id` which is SHARED across the chain) and the registry-dispatch function rejects cycles/over-depth/over-budget/expired-deadline before spawning the child `ToolLoopAgent`. Capability discovery uses the existing Phase 1 `agent_capability` endpoint — the `task` tool's description is rendered from the capability catalog at harness-construction time.

**Tech Stack:** Go 1.26, `github.com/digitallysavvy/go-ai/pkg/agent` (`SubagentRegistry`, `ToolLoopAgent`), existing Phase 2 `Harness` adapter + `BudgetService.CanDispatch` + `WSPublisher`, existing Phase 1 `agent_capability` + `SearchCapabilities` queries, TanStack Query on the frontend.

---

## Scope Note

Phase 6 of 12. Depends on:
- **Phase 1** — `agent_capability` table + `ListCapabilitiesByWorkspace` / `SearchCapabilities` queries + `capability.go` handler (all exist from migration 100).
- **Phase 2** — `harness.Harness` + `harness.Config.Subagents *goagent.SubagentRegistry` field (already declared and wired into `NewToolLoopAgent`), `traceIDKey` context, `BudgetService.CanDispatch(ctx, tx, wsID, estCents)`, `WSPublisher` interface, `cost_event` table.
- **Phase 4** — structured-outputs pattern; the `task` tool's result schema follows the same `$schema` + validation approach.
- **Phase 5** — shared memory (recall inside a subagent sees parent-scope memories; context compression reused for large-context handoff in §6.4).

This phase produces:
1. A `task` tool discoverable on every agent whose workspace has ≥1 other delegatable agent.
2. Harness-layer enforcement of depth (≤5), cycle detection, self-delegation rejection, and per-dispatch budget gating.
3. Cost-event aggregation under the parent's `trace_id` across arbitrary chain depth.
4. WS events `subagent.started`, `subagent.step`, `subagent.finished` that let the UI render the child as a nested card under the parent's tool-call.
5. Capability-browser UI that lists agents + capabilities visible to the current agent.

**Not in scope — explicitly deferred:**
- **Signal-based inter-agent messaging** — removed per D2. If two agents need to coordinate, they do so through a parent or via Phase 7 workflow channels.
- **Delegation history table / audit view** — everything needed for audit is already in `cost_event` (joined by `trace_id`) and `agent_trace` (Phase 10). No new table.
- **Cross-workspace delegation** — blocked at registry lookup; a workspace's subagent set is always its own agents.
- **Dynamic capability inference** — capabilities are authored manually (or synthesized at agent-create time); Phase 6 does NOT auto-derive capabilities from agent prompts.
- **Human-in-the-loop approval before subagent dispatch** — Phase 7 (`approval_request`) handles this. Phase 6 dispatches without gates.
- **Parallel subagent execution** — go-ai v0.4.0 executes tool calls sequentially. Phase 6 inherits that; a parent that wants fan-out uses Phase 7 workflows.

## Preconditions

This plan assumes **Phase 1 and Phase 2 have shipped** and the following files/symbols are on the branch. Verify each before starting Task 1 — a failure here means Phase 6 cannot execute standalone and either the prerequisite phase must land first or its stubs must be brought in.

**From Phase 1 (plan: `docs/superpowers/plans/2026-04-13-phase1-agent-type-system.md`):**

| Surface | Purpose in Phase 6 |
|---|---|
| `server/go.mod` contains `github.com/digitallysavvy/go-ai v0.4.0` (Phase 1 Task §1.10) | Compile-time imports in Tasks 4 + 7 |
| Migration 100: `agent_capability` table with `workspace_id`, `agent_id`, `name`, `description`, `input_schema`, `output_schema`, `estimated_cost_cents`, `avg_duration_seconds`, composite FK `(workspace_id, agent_id)` | Capability discovery (Task 2 + Task 8) |
| sqlc queries `ListCapabilitiesByWorkspace`, `SearchCapabilities` (Phase 1 Task 6) | Task 2 `LoadRoster`, Task 8 `ListDiscoverable` |
| sqlc query `ListWorkspaceAgents` (Phase 1) | Task 2 `LoadRoster` |
| `server/internal/handler/capability.go` `CapabilityHandler` type with `ListCapabilitiesByWorkspace` method already mounted | Task 8 extends it with `ListDiscoverable` |
| `BudgetService` with `CanDispatch(ctx context.Context, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error)` where `Decision{Allow bool; Reason string}` (Phase 1 §1.2) | Task 7 `budgetAllow` wrapper |
| `server/pkg/agent/defaults.go` already present (Phase 1 §1.11) — Phase 6 APPENDS `MaxDelegationDepth` and `SubagentPerStepTimeout` to it | Tasks 3 + 7 |

**From Phase 2 (plan: `docs/superpowers/plans/2026-04-14-phase2-harness-llm-backend-worker.md`):**

| Surface | Purpose in Phase 6 |
|---|---|
| `server/pkg/agent/harness/harness.go` with `Config{Model, SystemPrompt, Tools, MaxSteps, Reasoning, Skills, Subagents *goagent.SubagentRegistry, OnStepStartEvent, OnStepFinishEvent, OnToolCallStart, OnFinish}`, `Harness`, `New(Config) *Harness`, `(*Harness).Execute(ctx, prompt) (*Result, error)` (Phase 2 Task 4) | Tasks 3, 6, 7 wire into this |
| `server/pkg/agent/harness/` already imports `goagent "github.com/digitallysavvy/go-ai/pkg/agent"` and `ai "github.com/digitallysavvy/go-ai/pkg/ai"` | Phase 6 adds `subagents.go` / `task_tool.go` next to it |
| `ai.OnStepFinishEvent` with fields `Text string`, `Usage types.Usage`, `Step int` (from vendored go-ai `pkg/ai/callback_events.go`) | Task 7 `runChild` closure |
| Phase 2 `WSPublisher` interface / server-shared hub with `Publish(workspaceID uuid.UUID, kind string, payload map[string]any)` | Tasks 4 + 6 + 7 |
| Phase 2 `traceIDKey` context + `WithTraceID` / `TraceIDFrom` helpers (Phase 2 Task 1.5, D8) | Parent trace_id automatically propagates to child — Phase 6 NEVER re-mints it |
| `server/internal/worker/resolver.go` `Resolver` + `ResolverDeps` + `(r *Resolver).Resolve(ctx, task) (harness.Config, error)` (Phase 2 Task 22) | Task 7 extends `Resolve` |
| Phase 2 `CostCalculator` exporting cents-per-usage math (Phase 2 Task 14) | Task 7's `service.EstimateStepCents` / `.EstimateCostCents` delegate to this |
| Phase 2 `service.EstimateCostCents` stub in `server/internal/service/` — if Phase 2 didn't already export these two names, Phase 6 adds them in Task 7 Step 3 as thin wrappers over the Phase 2 calculator | Task 7 `runChild` |

**Test helpers (from Phase 2):**

Phase 6 tests call the following helpers. If Phase 2's `testutil` / `testsupport` packages do not yet export them, add the missing ones in a small pre-Task-0 bootstrap (create in `server/internal/testutil/` and `server/internal/testsupport/` respectively, sharing DB fixtures with Phase 2 tests):

| Helper | Used by |
|---|---|
| `testutil.NewTestDB(t) *pgxpool.Pool` | Tasks 2, 6, 8 |
| `testutil.SeedWorkspace(db, name) uuid.UUID` | Tasks 2, 6, 8, 11, 12 |
| `testutil.SeedAgent(db, wsID, name, provider, model) uuid.UUID` | Tasks 2, 6, 8 |
| `testutil.SeedCapability(db, wsID, agentID, name, description)` | Tasks 2, 6, 8 |
| `testutil.AlwaysAllowBudget BudgetAllowFn` | Task 6 |
| `testutil.NoopWS WSPublisher` | Task 6 |
| `testutil.StubDelegationService(db) *service.DelegationService` | Task 7 |
| `testutil.EnqueueTask(db, wsID, agentID) task` | Task 7 |
| `testsupport.NewEnv(t) *Env` — spins up DB + in-process worker + WS stream for integration tests | Tasks 11 + 12 |
| `testsupport.StubModel(scripts []TurnScript) provider.LanguageModel` | Tasks 11 + 12 |
| `testsupport.TurnScript{Text string; ToolCall *ToolCall}` + `ToolCall{Name, Args string}` | Tasks 11 + 12 |
| `testsupport.SeedBudgetPolicy(wsID, monthlyCents int)` | Task 12 |
| `testsupport.ListCostEvents(taskID) []CostEvent`, `WaitForTaskComplete`, `WSEventsForTask`, `ListToolCallResults` | Tasks 11 + 12 |

**Pre-Task 0 (bootstrap):** if any of the above is missing, run these steps before Task 1:

- [ ] Run `go mod tidy` and verify `server/go.mod` contains `github.com/digitallysavvy/go-ai v0.4.0`. If missing, `cd server && go get github.com/digitallysavvy/go-ai@v0.4.0 && go mod tidy`.
- [ ] Check `server/pkg/agent/defaults.go` exists. If not, create it with the minimal Phase 1 constants set (see PLAN.md §1.11) before appending the Phase 6 additions in Tasks 3 and 5.
- [ ] Grep for each helper in the table above. For every missing one, add a small stub in the appropriate package — prefer mirroring Phase 2's helper patterns.
- [ ] Verify `server/pkg/agent/harness/harness.go` defines `Config` with the `Subagents *goagent.SubagentRegistry` and `OnStepFinishEvent` fields. If either is absent, the plan cannot proceed — those are Phase 2 deliverables.

Only when the checklist is clean does Task 1 begin.

## File Structure

### New Files (Backend)

| File | Responsibility |
|------|---------------|
| `server/pkg/agent/harness/delegation_ctx.go` | Context keys + helpers: `WithDelegationDepth`, `DelegationDepthFrom`, `WithDelegationStack`, `DelegationStackFrom`, `PushDelegation`. Distinct from `traceIDKey` (D8) — trace_id is shared across a chain; the delegation stack is chain-local and holds `agent_id`s for cycle detection. |
| `server/pkg/agent/harness/delegation_ctx_test.go` | Tests: empty ctx defaults, stack push/pop, cycle detection helper, depth increment. |
| `server/pkg/agent/harness/subagents.go` | `BuildSubagentRegistry(ctx, deps, wsID, parentAgentID)` — loads delegatable agents from the workspace, builds one `SubagentEntry` per agent whose dispatch function enforces depth/cycle/self/budget then invokes a fresh `Harness.Execute`. |
| `server/pkg/agent/harness/subagents_test.go` | Tests: registry has correct entries, self-delegation rejected, cycle rejected, depth>5 rejected, budget denial propagates. |
| `server/pkg/agent/harness/task_tool.go` | Produces the `task` tool (`types.Tool`): JSON Schema, executor that emits `subagent.started/step/finished` WS events, aggregates child cost events under the parent's `trace_id`. |
| `server/pkg/agent/harness/task_tool_test.go` | Tests: schema matches capability roster, executor invokes registry, events emitted in order, cost aggregation hits DB with correct `trace_id`. |
| `server/pkg/agent/prompts/subagents.go` | `RenderTaskToolDescription(roster []CapabilityRow) string` — formats the capability list into the `task` tool's `description` field. |
| `server/pkg/agent/prompts/subagents_test.go` | Tests: empty roster → minimal description; populated roster → enumerated list with name/description pairs. |
| `server/internal/service/delegation.go` | `DelegationService` — composes the harness helpers with `BudgetService`, `WSPublisher`, and the capability repo. Used by the worker resolver (§6.6). |
| `server/internal/service/delegation_test.go` | Integration test: full dispatch round-trip against a stub child harness. |
| `server/internal/integration/delegation/chain_test.go` | Go-level integration test: A → B → C chain, trace_id shared, cost rolls up, WS events emitted. (Lives under `server/` so the `aicolab/server/internal/testsupport` import resolves; the repo-root `e2e/` directory is reserved for Playwright.) |
| `server/internal/integration/delegation/guards_test.go` | Go-level integration test: self/cycle/depth/budget guards reject dispatch before child runs. |

### Modified Files (Backend)

| File | Changes |
|------|---------|
| `server/pkg/agent/harness/harness.go` | No structural change needed (`Config.Subagents` field already exists from Phase 2). Add helper `(h *Harness) WithDelegationContext(ctx)` that seeds depth=0 + empty stack if not present — called by the worker resolver. |
| `server/internal/worker/resolver.go` | At task claim, after building the `Harness`, attach a `SubagentRegistry` built via `BuildSubagentRegistry`; if registry is non-empty, append the `task` tool to the tool list. |
| `server/internal/worker/resolver_test.go` | Add cases: agent with sibling agents gets `task` tool + registry; agent in empty workspace gets neither. |
| `server/internal/handler/capability.go` | Add `GET /workspaces/{wsId}/capabilities/discoverable?agent_id={id}` — same as `ListCapabilitiesByWorkspace` but excludes the requesting agent's own capabilities (so the UI picker doesn't offer self-delegation). |
| `server/internal/handler/capability_test.go` | Test for the `discoverable` variant: excludes caller agent, includes siblings. |

### Modified Files (Frontend)

| File | Changes |
|------|---------|
| `packages/core/types/agent.ts` | Add `Capability` + `DiscoverableCapability` types (`Capability` already exists from Phase 1 read-only tab; widen if needed). |
| `packages/core/api/capability.ts` | NEW — `list(wsId)`, `search(wsId, q)`, `discoverable(wsId, agentId)`. |
| `packages/core/api/index.ts` | Re-export `capability` namespace. |
| `packages/core/capability/queries.ts` | NEW — `useCapabilities`, `useSearchCapabilities`, `useDiscoverableCapabilities` TanStack Query hooks. |
| `packages/views/agents/components/capability-browser.tsx` | NEW — renders the roster of agents + capabilities visible to the viewer, used inside the agent detail page. |
| `packages/views/agents/components/tabs/capability-tab.tsx` | NEW — tab shell that hosts `CapabilityBrowser`. |
| `packages/views/agents/components/agent-detail.tsx` | Mount the capability tab. |
| `packages/views/tasks/components/tool-call-card.tsx` | Detect `tool_name === "task"` and render a `NestedSubagentCard` instead of the default tool-call row. |
| `packages/views/tasks/components/nested-subagent-card.tsx` | NEW — renders a subagent invocation with its own tool-call substream (hydrated from `subagent.*` WS events). |
| `packages/views/tasks/subagent-events.ts` | NEW — `useSubagentEvents(callId)` hook; subscribes to `subagent.started/step/finished` on the WS hub, filtered by call_id. Lives in `packages/views/` (not `packages/core/`) because CLAUDE.md constrains `packages/core/` to "zero react-dom" — `useState`/`useEffect` import from React, so the hook belongs where the UI consumes it. |

### External Infrastructure

| System | Change |
|--------|--------|
| None | Delegation is pure-software per D2. No migrations, no Redis keys, no new env. |

---

### Task 1: Delegation Context Helpers (D2 — depth + stack)

**Files:**
- Create: `server/pkg/agent/harness/delegation_ctx.go`
- Create: `server/pkg/agent/harness/delegation_ctx_test.go`

**Goal:** establish the two context keys D2 names — a depth counter and a stack of agent IDs — plus helpers the registry's dispatch function will call. These are **distinct from the `traceIDKey` already established in Phase 2**: `trace_id` is SHARED across every frame of a delegation chain; the stack is CHAIN-LOCAL (push on dispatch, pop on return) and holds `agent_id`s for cycle detection.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/harness/delegation_ctx_test.go
package harness

import (
    "context"
    "testing"

    "github.com/google/uuid"
)

func TestDelegationDepth_DefaultZero(t *testing.T) {
    t.Parallel()
    if got := DelegationDepthFrom(context.Background()); got != 0 {
        t.Errorf("empty ctx depth = %d, want 0", got)
    }
}

func TestDelegationDepth_RoundTrip(t *testing.T) {
    t.Parallel()
    ctx := WithDelegationDepth(context.Background(), 3)
    if got := DelegationDepthFrom(ctx); got != 3 {
        t.Errorf("depth = %d, want 3", got)
    }
}

func TestDelegationStack_PushAppends(t *testing.T) {
    t.Parallel()
    a := uuid.New()
    b := uuid.New()
    ctx := PushDelegation(context.Background(), a)
    ctx = PushDelegation(ctx, b)
    stack := DelegationStackFrom(ctx)
    if len(stack) != 2 || stack[0] != a || stack[1] != b {
        t.Errorf("stack = %v, want [%v %v]", stack, a, b)
    }
}

func TestDelegationStack_Immutable(t *testing.T) {
    t.Parallel()
    // Verify PushDelegation does NOT mutate the parent context's slice —
    // goroutines that branch from the same parent must see independent stacks.
    a, b, c := uuid.New(), uuid.New(), uuid.New()
    parent := PushDelegation(context.Background(), a)
    left := PushDelegation(parent, b)
    right := PushDelegation(parent, c)

    if got := DelegationStackFrom(left); len(got) != 2 || got[1] != b {
        t.Errorf("left = %v", got)
    }
    if got := DelegationStackFrom(right); len(got) != 2 || got[1] != c {
        t.Errorf("right = %v", got)
    }
    if got := DelegationStackFrom(parent); len(got) != 1 {
        t.Errorf("parent mutated = %v", got)
    }
}

func TestStackContainsAgent_DetectsCycle(t *testing.T) {
    t.Parallel()
    a, b := uuid.New(), uuid.New()
    ctx := PushDelegation(PushDelegation(context.Background(), a), b)
    if !StackContainsAgent(ctx, a) {
        t.Error("cycle against a not detected")
    }
    if StackContainsAgent(ctx, uuid.New()) {
        t.Error("non-stack uuid falsely reported")
    }
}

func TestProgressFn_RoundTrip(t *testing.T) {
    t.Parallel()
    var got SubagentProgress
    ctx := WithProgressFn(context.Background(), func(p SubagentProgress) { got = p })
    fn := ProgressFnFrom(ctx)
    if fn == nil { t.Fatal("ProgressFnFrom returned nil") }
    fn(SubagentProgress{Step: 3, Text: "hello", UsageCents: 17})
    if got.Step != 3 || got.Text != "hello" || got.UsageCents != 17 {
        t.Errorf("got = %+v", got)
    }
}

func TestProgressFn_NilSafe(t *testing.T) {
    t.Parallel()
    // WithProgressFn(ctx, nil) returns the ctx unchanged — callers never
    // accidentally attach a nil function the child would then deref.
    ctx := WithProgressFn(context.Background(), nil)
    if ProgressFnFrom(ctx) != nil {
        t.Error("nil fn should not be attached to ctx")
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/harness/ -run "TestDelegation|TestStack|TestProgressFn" -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/harness/delegation_ctx.go
package harness

import (
    "context"

    "github.com/google/uuid"
)

// Keys are unexported so all interaction goes through the helpers below.
// Two SEPARATE keys because depth is an int and stack is a slice; conflating
// them forces readers to remember which field is which.
type (
    delegationDepthCtxKey struct{}
    delegationStackCtxKey struct{}
)

var (
    delegationDepthKey = delegationDepthCtxKey{}
    delegationStackKey = delegationStackCtxKey{}
)

// WithDelegationDepth returns ctx with depth attached. Used by the registry
// dispatcher to increment per hop.
func WithDelegationDepth(ctx context.Context, depth int) context.Context {
    return context.WithValue(ctx, delegationDepthKey, depth)
}

// DelegationDepthFrom returns the current depth, 0 if the ctx has no depth
// attached (root call).
func DelegationDepthFrom(ctx context.Context) int {
    if v, ok := ctx.Value(delegationDepthKey).(int); ok {
        return v
    }
    return 0
}

// PushDelegation appends agentID to the delegation stack. Returns a new ctx —
// does NOT mutate the parent's slice. This immutability is load-bearing for
// any future parallel-subagent code path.
func PushDelegation(ctx context.Context, agentID uuid.UUID) context.Context {
    cur := DelegationStackFrom(ctx)
    next := make([]uuid.UUID, len(cur)+1)
    copy(next, cur)
    next[len(cur)] = agentID
    return context.WithValue(ctx, delegationStackKey, next)
}

// DelegationStackFrom returns a copy of the current stack (nil if none).
// Callers MUST treat the returned slice as read-only.
func DelegationStackFrom(ctx context.Context) []uuid.UUID {
    if v, ok := ctx.Value(delegationStackKey).([]uuid.UUID); ok {
        return v
    }
    return nil
}

// StackContainsAgent returns true if agentID is anywhere on the delegation
// stack — used to reject cycles at dispatch.
func StackContainsAgent(ctx context.Context, agentID uuid.UUID) bool {
    for _, id := range DelegationStackFrom(ctx) {
        if id == agentID {
            return true
        }
    }
    return false
}

// SubagentProgress is one interim step reported from a child harness up to
// the parent's `task` tool executor, which forwards it as a subagent.step
// WS event. Keep the shape minimal — richer data belongs in the per-step
// cost_event row the child's backend already writes.
type SubagentProgress struct {
    Step       int    // 1-based step index within the child's agent loop
    Text       string // short human-readable summary — reasoning snippet or tool name
    UsageCents int    // cumulative child cost so far (cents)
}

type progressFnCtxKey struct{}

var progressFnKey = progressFnCtxKey{}

// WithProgressFn attaches a progress callback to ctx. The Task 4 task tool
// sets this before dispatching; the child harness (Phase 2 OnStepFinish
// callback wired in Task 7's runChild closure) reads it and invokes per step.
// Returns the ctx unchanged if fn is nil.
func WithProgressFn(ctx context.Context, fn func(SubagentProgress)) context.Context {
    if fn == nil {
        return ctx
    }
    return context.WithValue(ctx, progressFnKey, fn)
}

// ProgressFnFrom returns the progress callback attached to ctx (nil if none).
// Child harness step-callbacks invoke it safely: `if fn := ProgressFnFrom(ctx); fn != nil { fn(p) }`.
func ProgressFnFrom(ctx context.Context) func(SubagentProgress) {
    if v, ok := ctx.Value(progressFnKey).(func(SubagentProgress)); ok {
        return v
    }
    return nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -run "TestDelegation|TestStack|TestProgressFn" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/delegation_ctx.go server/pkg/agent/harness/delegation_ctx_test.go
git commit -m "feat(harness): delegation depth + stack context helpers (D2)

Two separate ctx keys: delegationDepthKey (int, per hop) and
delegationStackKey ([]uuid.UUID, chain-local for cycle detection).
Immutable push — parallel branches from the same parent get
independent stacks."
```

---

### Task 2: Capability Loader + Roster Builder

**Files:**
- Create: `server/internal/service/delegation.go` (skeleton + `LoadRoster` method only in this task)
- Create: `server/internal/service/delegation_test.go`

**Goal:** load the list of delegatable agents in a workspace, keyed by slug, with their capability rows. The `BuildSubagentRegistry` task (Task 3) will consume this roster. Capability loading is intentionally a separate unit so Task 3's tests can stub the roster without touching the DB.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/service/delegation_test.go
package service

import (
    "context"
    "testing"

    "github.com/google/uuid"

    "aicolab/server/internal/testutil"
)

func TestLoadRoster_ExcludesSelf(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "acme")
    me := testutil.SeedAgent(db, wsID, "parent", "anthropic", "claude-sonnet-4-5")
    sib := testutil.SeedAgent(db, wsID, "customer-support", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedCapability(db, wsID, sib, "reply_to_ticket", "Draft a reply to a support ticket")

    svc := NewDelegationService(DelegationDeps{DB: db})
    roster, err := svc.LoadRoster(context.Background(), wsID, me)
    if err != nil { t.Fatal(err) }

    if len(roster) != 1 {
        t.Fatalf("roster len = %d, want 1 (self excluded)", len(roster))
    }
    if roster[0].AgentID != sib {
        t.Errorf("roster[0].AgentID = %v, want %v", roster[0].AgentID, sib)
    }
    if roster[0].Slug != "customer-support" {
        t.Errorf("slug = %q, want customer-support", roster[0].Slug)
    }
    if len(roster[0].Capabilities) != 1 || roster[0].Capabilities[0].Name != "reply_to_ticket" {
        t.Errorf("capabilities = %+v", roster[0].Capabilities)
    }
}

func TestLoadRoster_CrossWorkspaceIsolation(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsA := testutil.SeedWorkspace(db, "ws-a")
    wsB := testutil.SeedWorkspace(db, "ws-b")
    me := testutil.SeedAgent(db, wsA, "parent", "anthropic", "claude-sonnet-4-5")
    testutil.SeedAgent(db, wsB, "foreign", "anthropic", "claude-sonnet-4-5")

    svc := NewDelegationService(DelegationDeps{DB: db})
    roster, err := svc.LoadRoster(context.Background(), wsA, me)
    if err != nil { t.Fatal(err) }
    if len(roster) != 0 {
        t.Errorf("cross-workspace roster = %+v, want empty", roster)
    }
    _ = uuid.Nil
}

func TestLoadRoster_NoDelegatableAgents(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "solo")
    me := testutil.SeedAgent(db, wsID, "lonely", "anthropic", "claude-sonnet-4-5")

    svc := NewDelegationService(DelegationDeps{DB: db})
    roster, err := svc.LoadRoster(context.Background(), wsID, me)
    if err != nil { t.Fatal(err) }
    if len(roster) != 0 {
        t.Errorf("solo roster = %+v, want empty", roster)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/service/ -run TestLoadRoster -v`
Expected: FAIL — `NewDelegationService` / `LoadRoster` undefined.

- [ ] **Step 3: Implement**

```go
// server/internal/service/delegation.go
package service

import (
    "context"
    "fmt"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"

    "aicolab/server/pkg/db/db"
)

// DelegationService composes the pieces Phase 6 needs: capability loading,
// budget gating, WS emission, and (in later tasks) subagent registry
// construction. It is workspace-scoped — every method takes wsID and never
// crosses workspace boundaries.
type DelegationService struct {
    db *pgxpool.Pool
    // Later tasks add: budget, ws, harnessBuilder.
}

type DelegationDeps struct {
    DB *pgxpool.Pool
}

func NewDelegationService(deps DelegationDeps) *DelegationService {
    return &DelegationService{db: deps.DB}
}

// CapabilityEntry is a single agent_capability row surfaced to the roster.
type CapabilityEntry struct {
    ID          uuid.UUID
    Name        string
    Description string
    InputSchema []byte // raw JSONB; task-tool description renders name+description only
}

// RosterEntry is one delegatable sibling: an agent + its capabilities.
// Slug is the agent's name snake-cased — it's the key the `task` tool expects
// ("task(subagent_type='customer-support', ...)").
type RosterEntry struct {
    AgentID      uuid.UUID
    Slug         string
    DisplayName  string
    Capabilities []CapabilityEntry
}

// LoadRoster returns every agent in the workspace OTHER than callerAgentID,
// along with each one's capabilities. Self-exclusion lives here so downstream
// code never has to remember the rule.
func (s *DelegationService) LoadRoster(
    ctx context.Context, wsID, callerAgentID uuid.UUID,
) ([]RosterEntry, error) {
    q := db.New(s.db)
    agents, err := q.ListWorkspaceAgents(ctx, uuidPg(wsID))
    if err != nil {
        return nil, fmt.Errorf("list agents: %w", err)
    }

    caps, err := q.ListCapabilitiesByWorkspace(ctx, uuidPg(wsID))
    if err != nil {
        return nil, fmt.Errorf("list capabilities: %w", err)
    }

    // Bucket capabilities by agent_id (single pass).
    byAgent := make(map[uuid.UUID][]CapabilityEntry, len(agents))
    for _, c := range caps {
        aID := uuidFromPg(c.AgentID)
        byAgent[aID] = append(byAgent[aID], CapabilityEntry{
            ID:          uuidFromPg(c.ID),
            Name:        c.Name,
            Description: c.Description,
            InputSchema: c.InputSchema,
        })
    }

    out := make([]RosterEntry, 0, len(agents))
    for _, a := range agents {
        id := uuidFromPg(a.ID)
        if id == callerAgentID {
            continue
        }
        out = append(out, RosterEntry{
            AgentID:      id,
            Slug:         slugify(a.Name),
            DisplayName:  a.Name,
            Capabilities: byAgent[id],
        })
    }
    return out, nil
}

// slugify turns "Customer Support" → "customer-support". Kept simple on
// purpose — agent names are admin-curated, not free-form user input.
func slugify(s string) string {
    out := make([]byte, 0, len(s))
    prevHyphen := true
    for i := 0; i < len(s); i++ {
        c := s[i]
        switch {
        case c >= 'A' && c <= 'Z':
            out = append(out, c+('a'-'A'))
            prevHyphen = false
        case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
            out = append(out, c)
            prevHyphen = false
        case !prevHyphen:
            out = append(out, '-')
            prevHyphen = true
        }
    }
    for len(out) > 0 && out[len(out)-1] == '-' {
        out = out[:len(out)-1]
    }
    return string(out)
}
```

Note: `ListWorkspaceAgents` already exists (Phase 1 Task 5); `ListCapabilitiesByWorkspace` exists (Phase 1 Task 6). No new sqlc work in this task.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/service/ -run TestLoadRoster -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/delegation.go server/internal/service/delegation_test.go
git commit -m "feat(delegation): DelegationService.LoadRoster + slugify

Loads delegatable siblings from agent + agent_capability (Phase 1),
excludes the caller, and groups capabilities by agent. Workspace-scoped
at the query level — cross-workspace returns empty."
```

---

### Task 2.5: sqlc — `GetAgentBySlug` Query

**Files:**
- Modify: `server/pkg/db/queries/agent.sql` (Phase 1 Task 5 created this file)
- Regenerate: `server/pkg/db/generated/agent.sql.go` via `make sqlc`

**Goal:** Task 7's `runChild` resolver-closure needs to look up a child agent by its workspace-scoped slug. Phase 1 did NOT add a by-slug query (slugs were introduced here in Phase 6), so we add one now. Slug equality is computed against the `slugify` output — we re-slugify inside the query via an app-side comparison rather than storing the slug column, which would require a migration.

- [ ] **Step 1: Add the query**

Append to `server/pkg/db/queries/agent.sql`:

```sql
-- name: ListWorkspaceAgentsForSlugLookup :many
-- Used by the Phase 6 subagent resolver to find an agent by its
-- service.Slugify(agent.name) output. Returns the minimum fields the
-- resolver needs (id, workspace_id, name, provider, model) so we don't
-- depend on the full GetAgent projection.
SELECT id, workspace_id, name, provider, model
FROM agent
WHERE workspace_id = $1
ORDER BY name ASC;
```

The Go-side helper lives in `server/internal/service/delegation.go`:

```go
// GetAgentBySlug fetches a workspace-scoped agent whose slug matches.
// Slug is computed here (not stored) so adding new agents never requires
// a migration. For workspaces with >500 agents consider adding a
// persisted slug column + partial index — out of scope for Phase 6.
func (s *DelegationService) GetAgentBySlug(
    ctx context.Context, wsID uuid.UUID, slug string,
) (db.ListWorkspaceAgentsForSlugLookupRow, error) {
    q := db.New(s.db)
    rows, err := q.ListWorkspaceAgentsForSlugLookup(ctx, uuidPg(wsID))
    if err != nil { return db.ListWorkspaceAgentsForSlugLookupRow{}, err }
    for _, r := range rows {
        if Slugify(r.Name) == slug {
            return r, nil
        }
    }
    return db.ListWorkspaceAgentsForSlugLookupRow{}, fmt.Errorf("agent not found for slug %q", slug)
}
```

- [ ] **Step 2: Regenerate sqlc**

Run: `cd server && make sqlc`
Expected: `server/pkg/db/generated/agent.sql.go` contains `ListWorkspaceAgentsForSlugLookup`.

- [ ] **Step 3: Write test**

```go
// Append to server/internal/service/delegation_test.go.

func TestGetAgentBySlug_ResolvesSlugified(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "team")
    id := testutil.SeedAgent(db, wsID, "Customer Support", "anthropic", "claude-haiku-4-5-20251001")

    svc := NewDelegationService(DelegationDeps{DB: db})
    got, err := svc.GetAgentBySlug(context.Background(), wsID, "customer-support")
    if err != nil { t.Fatal(err) }
    if uuidFromPg(got.ID) != id {
        t.Errorf("id = %v, want %v", uuidFromPg(got.ID), id)
    }
}

func TestGetAgentBySlug_WorkspaceScoped(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsA := testutil.SeedWorkspace(db, "a")
    wsB := testutil.SeedWorkspace(db, "b")
    testutil.SeedAgent(db, wsB, "Customer Support", "anthropic", "claude-haiku-4-5-20251001")

    svc := NewDelegationService(DelegationDeps{DB: db})
    _, err := svc.GetAgentBySlug(context.Background(), wsA, "customer-support")
    if err == nil { t.Error("cross-workspace lookup should fail; got nil") }
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/service/ -run TestGetAgentBySlug -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/db/queries/agent.sql server/pkg/db/generated/agent.sql.go server/internal/service/delegation.go server/internal/service/delegation_test.go
git commit -m "feat(delegation): GetAgentBySlug + sqlc query for resolver lookup

Slug computed app-side from service.Slugify(agent.name) so no migration
needed. Workspace-scoped at the query layer."
```

Task 7's `runChild` closure now calls `r.deps.Delegation.GetAgentBySlug(ctx, task.WorkspaceID, slug)` (not `q.GetAgentBySlug`) — adjust the Task 7 snippet accordingly.

---

### Task 3: Subagent Registry Builder + Dispatch Guards

**Files:**
- Create: `server/pkg/agent/harness/subagents.go`
- Create: `server/pkg/agent/harness/subagents_test.go`

**Goal:** build our own `*harness.SubagentRegistry` from a roster. Each registry entry dispatches a subagent call; the Task-4 `task` tool (which Phase 6 authors — go-ai does NOT ship one) invokes `SubagentRegistry.Dispatch` from its executor. All guards — depth, cycle, self, budget, timeout — run BEFORE any child work happens, so a rejected dispatch costs zero tokens.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/harness/subagents_test.go
package harness

import (
    "context"
    "errors"
    "strings"
    "testing"
    "time"

    "github.com/google/uuid"

    mcagent "aicolab/server/pkg/agent"
)

func TestBuildSubagentRegistry_RejectsSelfDelegation(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:       stubRoster(parent, "parent"),
        ParentAgent:  parent,
        BudgetAllow:  alwaysBudgetAllow,
        RunChild:     runChildOK,
    })
    _, err := reg.Dispatch(context.Background(), "parent", SubagentInput{Description: "x"})
    if !errors.Is(err, ErrSelfDelegation) {
        t.Errorf("err = %v, want ErrSelfDelegation", err)
    }
}

func TestBuildSubagentRegistry_RejectsCycle(t *testing.T) {
    t.Parallel()
    a := uuid.New()
    b := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(a, "alpha", b, "beta"),
        ParentAgent: a,
        BudgetAllow: alwaysBudgetAllow,
        RunChild:    runChildOK,
    })
    // Pretend we're already two frames deep with "beta" on the stack.
    ctx := PushDelegation(PushDelegation(context.Background(), a), b)
    _, err := reg.Dispatch(ctx, "beta", SubagentInput{Description: "x"})
    if !errors.Is(err, ErrDelegationCycle) {
        t.Errorf("err = %v, want ErrDelegationCycle", err)
    }
}

func TestBuildSubagentRegistry_RejectsOverDepth(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    child := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent", child, "child"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild:    runChildOK,
    })
    ctx := WithDelegationDepth(context.Background(), mcagent.MaxDelegationDepth)
    _, err := reg.Dispatch(ctx, "child", SubagentInput{Description: "x"})
    if !errors.Is(err, ErrDelegationDepthExceeded) {
        t.Errorf("err = %v, want ErrDelegationDepthExceeded", err)
    }
}

func TestBuildSubagentRegistry_UnknownSlug(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild:    runChildOK,
    })
    _, err := reg.Dispatch(context.Background(), "nonexistent", SubagentInput{Description: "x"})
    if !errors.Is(err, ErrSubagentNotFound) {
        t.Errorf("err = %v, want ErrSubagentNotFound", err)
    }
}

func TestBuildSubagentRegistry_BudgetDeniesDispatch(t *testing.T) {
    t.Parallel()
    parent, child := uuid.New(), uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent", child, "child"),
        ParentAgent: parent,
        BudgetAllow: func(ctx context.Context, est int) (bool, string) {
            return false, "budget_exceeded"
        },
        RunChild: runChildOK,
    })
    _, err := reg.Dispatch(context.Background(), "child", SubagentInput{Description: "x"})
    if err == nil || !strings.Contains(err.Error(), "budget_exceeded") {
        t.Errorf("err = %v, want budget_exceeded", err)
    }
}

func TestBuildSubagentRegistry_PropagatesParentDeadline(t *testing.T) {
    t.Parallel()
    parent, child := uuid.New(), uuid.New()
    var childDeadline time.Time
    var childDeadlineOK bool
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent", child, "child"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild: func(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error) {
            childDeadline, childDeadlineOK = ctx.Deadline()
            return SubagentOutput{Text: "ok"}, nil
        },
    })
    // Parent deadline tighter than the subagent ceiling — child must inherit.
    tightDeadline := time.Now().Add(2 * time.Second)
    parentCtx, cancel := context.WithDeadline(context.Background(), tightDeadline)
    defer cancel()
    _, err := reg.Dispatch(parentCtx, "child", SubagentInput{Description: "x"})
    if err != nil { t.Fatal(err) }
    if !childDeadlineOK {
        t.Fatal("child ctx had no deadline; timeout guard did not fire")
    }
    if childDeadline.After(tightDeadline) {
        t.Errorf("child deadline %v exceeds parent %v", childDeadline, tightDeadline)
    }
}

func TestBuildSubagentRegistry_HappyPathIncrementsDepthAndStack(t *testing.T) {
    t.Parallel()
    parent, child := uuid.New(), uuid.New()
    var gotDepth int
    var gotStack []uuid.UUID
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent", child, "child"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild: func(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error) {
            gotDepth = DelegationDepthFrom(ctx)
            gotStack = DelegationStackFrom(ctx)
            return SubagentOutput{Text: "ok"}, nil
        },
    })
    out, err := reg.Dispatch(context.Background(), "child", SubagentInput{Description: "x"})
    if err != nil { t.Fatal(err) }
    if out.Text != "ok" { t.Errorf("text = %q", out.Text) }
    if gotDepth != 1 { t.Errorf("child depth = %d, want 1", gotDepth) }
    if len(gotStack) != 1 || gotStack[0] != parent {
        t.Errorf("child stack = %v, want [%v]", gotStack, parent)
    }
}

// --- test helpers ---

func stubRoster(pairs ...any) []RosterStub {
    // pairs: uuid, slug, uuid, slug, ...
    var out []RosterStub
    for i := 0; i < len(pairs); i += 2 {
        id, _ := pairs[i].(uuid.UUID)
        slug, _ := pairs[i+1].(string)
        out = append(out, RosterStub{AgentID: id, Slug: slug})
    }
    return out
}

func alwaysBudgetAllow(ctx context.Context, estCents int) (bool, string) { return true, "" }

func runChildOK(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error) {
    return SubagentOutput{Text: "child said hi"}, nil
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/harness/ -run TestBuildSubagentRegistry -v`
Expected: FAIL — `BuildSubagentRegistry`, error sentinels, types undefined.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/harness/subagents.go
package harness

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/google/uuid"

    mcagent "aicolab/server/pkg/agent"
)

// Error sentinels. Exposed so callers (task tool executor, tests) can switch
// on them with errors.Is rather than string matching.
var (
    ErrSelfDelegation          = errors.New("self-delegation rejected")
    ErrDelegationCycle         = errors.New("delegation cycle rejected")
    ErrDelegationDepthExceeded = errors.New("delegation depth exceeded")
    ErrSubagentNotFound        = errors.New("subagent slug not found")
)

// RosterStub is the minimal subset of service.RosterEntry the registry
// consumes. Kept narrow so the service package doesn't need to re-export the
// whole type and so tests stub easily.
type RosterStub struct {
    AgentID uuid.UUID
    Slug    string
}

// SubagentInput/Output are the harness-layer shapes for a single delegation.
// They mirror (but don't directly use) go-ai's types.Tool input/output so the
// registry can be tested without go-ai wired in.
type SubagentInput struct {
    Description  string
    Instructions string
    Context      string
}

type SubagentOutput struct {
    Text         string
    UsageCents   int
    FinishReason string
}

// BudgetAllowFn returns (allow, reason). Reason is included in the error
// on deny so the parent agent's log makes the decision legible.
type BudgetAllowFn func(ctx context.Context, estCents int) (allow bool, reason string)

// RunChildFn actually spawns the child Harness.Execute. Passed as a dep so
// the registry can be unit-tested without a real harness.
type RunChildFn func(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error)

type SubagentRegistryDeps struct {
    Roster      []RosterStub
    ParentAgent uuid.UUID
    BudgetAllow BudgetAllowFn
    RunChild    RunChildFn
}

// SubagentRegistry dispatches a named subagent call with guards. It exposes
// `Dispatch` directly so the task tool (Task 4) can call into it without
// going through go-ai's intermediate machinery in tests.
type SubagentRegistry struct {
    bySlug      map[string]uuid.UUID
    parentAgent uuid.UUID
    budgetAllow BudgetAllowFn
    runChild    RunChildFn
}

func BuildSubagentRegistry(deps SubagentRegistryDeps) *SubagentRegistry {
    bySlug := make(map[string]uuid.UUID, len(deps.Roster))
    for _, r := range deps.Roster {
        bySlug[r.Slug] = r.AgentID
    }
    // Always register the parent under its own slug so self-delegation is
    // detected at lookup (we don't want to silently 404 when the LLM asks to
    // call itself — an explicit error teaches the agent not to try again).
    if deps.ParentAgent != uuid.Nil {
        // Consumers never add the parent to Roster; we leave the slug empty
        // here and rely on the AgentID equality check below. See Dispatch.
    }
    return &SubagentRegistry{
        bySlug:      bySlug,
        parentAgent: deps.ParentAgent,
        budgetAllow: deps.BudgetAllow,
        runChild:    deps.RunChild,
    }
}

// Dispatch is the single entry point for invoking a subagent. All guards run
// here IN ORDER: lookup → self → cycle → depth → budget → spawn.
func (r *SubagentRegistry) Dispatch(
    ctx context.Context, slug string, in SubagentInput,
) (SubagentOutput, error) {
    // 1. Lookup.
    childID, ok := r.bySlug[slug]
    if !ok {
        return SubagentOutput{}, fmt.Errorf("%w: %q", ErrSubagentNotFound, slug)
    }

    // 2. Self-delegation. Two paths: slug resolves to parent OR slug IS parent.
    if childID == r.parentAgent {
        return SubagentOutput{}, ErrSelfDelegation
    }

    // 3. Cycle.
    if StackContainsAgent(ctx, childID) {
        return SubagentOutput{}, fmt.Errorf("%w: child=%s", ErrDelegationCycle, childID)
    }

    // 4. Depth.
    depth := DelegationDepthFrom(ctx)
    if depth >= mcagent.MaxDelegationDepth {
        return SubagentOutput{}, fmt.Errorf("%w: depth=%d, max=%d",
            ErrDelegationDepthExceeded, depth, mcagent.MaxDelegationDepth)
    }

    // 5. Budget. Estimate from a floor; the real cost event after execution
    // will tighten the figure. We use a conservative 100 cents per dispatch
    // so pathological cheap models don't starve the accounting.
    if allow, reason := r.budgetAllow(ctx, estDispatchCents); !allow {
        return SubagentOutput{}, fmt.Errorf("budget denied: %s", reason)
    }

    // 6. Timeout. PLAN.md §6.3 requires `deadline = min(parent remaining,
    // SubagentMaxSteps * per-step-timeout)`. If the parent ctx has no
    // deadline, we cap at the subagent ceiling alone. Parent cancellation
    // propagates automatically through ctx.
    subagentCeiling := time.Duration(mcagent.SubagentMaxSteps) * mcagent.SubagentPerStepTimeout
    deadline := time.Now().Add(subagentCeiling)
    if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(deadline) {
        deadline = parentDeadline
    }
    childCtxBase, cancel := context.WithDeadline(ctx, deadline)
    defer cancel()

    // 7. Spawn. Push parent onto the stack (so the child sees it) and
    // increment depth. trace_id is NOT touched — shared across the chain.
    childCtx := WithDelegationDepth(
        PushDelegation(childCtxBase, r.parentAgent),
        depth+1,
    )
    return r.runChild(childCtx, slug, in)
}

// estDispatchCents is the conservative per-dispatch budget estimate.
// Tight enough to keep pathological chains from approving themselves;
// loose enough that legitimate dispatches aren't starved.
const estDispatchCents = 100 // $1.00
```

And expose `MaxDelegationDepth` + `SubagentPerStepTimeout` from `server/pkg/agent/defaults.go` (D7):

```go
// Append to server/pkg/agent/defaults.go.

// MaxDelegationDepth caps how deep `task(...)` chains can go. 5 matches the
// PLAN.md §6.3 explicit cap. Counts the number of hops from the root call:
// root→A (depth 1) → A→B (depth 2) → … → depth 5 rejects any further hop.
const MaxDelegationDepth = 5

// SubagentPerStepTimeout bounds per-step wall time for a child agent. The
// subagent ceiling is SubagentMaxSteps * SubagentPerStepTimeout. Tuned so a
// healthy reasoning step (Sonnet, ≤32K context) completes comfortably and a
// runaway or provider-stall aborts inside a minute.
const SubagentPerStepTimeout = 45 * time.Second
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -run TestBuildSubagentRegistry -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/subagents.go server/pkg/agent/harness/subagents_test.go server/pkg/agent/defaults.go
git commit -m "feat(harness): SubagentRegistry with depth/cycle/self/budget guards

All guards run before the child is spawned so a rejected dispatch costs
zero tokens. MaxDelegationDepth=5 lives in defaults.go per D7. Parent
agent ID is pushed onto the stack as the child's frame so the next hop
sees it for cycle detection."
```

---

### Task 4: The `task` Tool — JSON Schema + Executor

**Files:**
- Create: `server/pkg/agent/harness/task_tool.go`
- Create: `server/pkg/agent/harness/task_tool_test.go`

**Goal:** produce the `types.Tool` go-ai sees. The tool's description enumerates available subagents (rendered by Task 5); its executor calls `SubagentRegistry.Dispatch`, emits `subagent.started/step/finished` events through a `WSPublisher`, and records a cost event with the child's `trace_id` (SAME as the parent — shared across chain).

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/harness/task_tool_test.go
package harness

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/google/uuid"
)

func TestTaskTool_SchemaIsStable(t *testing.T) {
    t.Parallel()
    tt := NewTaskTool(TaskToolDeps{
        Registry:     emptyRegistry(),
        WS:           noopWSPub{},
        Description:  "Available subagents:\n- customer-support: Draft replies.",
    })
    var schema map[string]any
    if err := json.Unmarshal(tt.Parameters, &schema); err != nil { t.Fatal(err) }

    props, _ := schema["properties"].(map[string]any)
    for _, k := range []string{"subagent_type", "description", "instructions"} {
        if _, ok := props[k]; !ok { t.Errorf("schema missing %q property", k) }
    }
    required, _ := schema["required"].([]any)
    if len(required) != 2 { t.Errorf("required count = %d, want 2", len(required)) }
}

func TestTaskTool_ExecutorEmitsLifecycleEvents(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    child := uuid.New()
    pub := &recordingWSPub{}
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "parent", child, "child"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild: func(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error) {
            // Simulate interim progress the way a real child harness does: it
            // pulls a ProgressFn out of ctx and invokes it for each step the
            // LLM produces. Two synthetic steps here — enough to assert the
            // subagent.step event is forwarded.
            if fn := ProgressFnFrom(ctx); fn != nil {
                fn(SubagentProgress{Step: 1, Text: "thinking"})
                fn(SubagentProgress{Step: 2, Text: "tool call"})
            }
            return SubagentOutput{Text: "done", UsageCents: 42}, nil
        },
    })
    tt := NewTaskTool(TaskToolDeps{
        Registry:    reg,
        WS:          pub,
        WorkspaceID: uuid.New(),
        ParentTaskID: uuid.New(),
        Description: "roster",
    })

    payload := []byte(`{"subagent_type":"child","description":"do work","instructions":""}`)
    raw, err := tt.Execute(context.Background(), payload)
    if err != nil { t.Fatal(err) }

    var out map[string]any
    if err := json.Unmarshal(raw, &out); err != nil { t.Fatal(err) }
    if out["text"] != "done" { t.Errorf("text = %v", out["text"]) }
    if out["usage_cents"].(float64) != 42 { t.Errorf("usage_cents = %v", out["usage_cents"]) }

    // Expect: 1 started + N step + 1 finished, in order.
    kinds := eventKinds(pub.events)
    if len(kinds) < 3 || kinds[0] != "subagent.started" || kinds[len(kinds)-1] != "subagent.finished" {
        t.Fatalf("events = %v (want started, step+, finished)", kinds)
    }
    stepCount := 0
    for _, k := range kinds[1 : len(kinds)-1] {
        if k != "subagent.step" {
            t.Errorf("unexpected intermediate event %q", k)
        }
        stepCount++
    }
    if stepCount != 2 {
        t.Errorf("step count = %d, want 2", stepCount)
    }
}

func eventKinds(ev []wsEvent) []string {
    ks := make([]string, len(ev))
    for i, e := range ev { ks[i] = e.kind }
    return ks
}

func TestTaskTool_ExecutorSurfacesGuardError(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      stubRoster(parent, "only"),
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild:    runChildOK,
    })
    tt := NewTaskTool(TaskToolDeps{Registry: reg, WS: noopWSPub{}})

    payload := []byte(`{"subagent_type":"only","description":"x"}`)
    raw, err := tt.Execute(context.Background(), payload)
    if err == nil {
        t.Fatalf("expected error, got %s", raw)
    }
    // Self-delegation via slug lookup hits the parent -> ErrSelfDelegation.
    if got := err.Error(); got == "" { t.Error("error text empty") }
}

// --- test helpers ---

type wsEvent struct {
    kind    string
    payload map[string]any
}

type recordingWSPub struct{ events []wsEvent }

func (r *recordingWSPub) Publish(ws uuid.UUID, kind string, payload map[string]any) {
    r.events = append(r.events, wsEvent{kind: kind, payload: payload})
}

type noopWSPub struct{}

func (noopWSPub) Publish(uuid.UUID, string, map[string]any) {}

func emptyRegistry() *SubagentRegistry {
    return BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      nil,
        ParentAgent: uuid.Nil,
        BudgetAllow: alwaysBudgetAllow,
        RunChild:    runChildOK,
    })
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/harness/ -run TestTaskTool -v`
Expected: FAIL — `NewTaskTool`, `TaskToolDeps`, `WSPublisher` interface undefined in this package.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/harness/task_tool.go
package harness

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/google/uuid"

    "github.com/digitallysavvy/go-ai/pkg/types"
)

// WSPublisher is the minimum surface the task tool needs from the
// server's WS hub. Defined locally so this package does not depend on
// internal/service; Phase 2's full publisher satisfies it.
type WSPublisher interface {
    Publish(workspaceID uuid.UUID, kind string, payload map[string]any)
}

// TaskToolDeps bundles the runtime surfaces the tool executor needs. The
// registry comes from Task 3; description from Task 5; ws publisher is the
// server-shared publisher; WorkspaceID + ParentTaskID are attached so the
// emitted events land on the right stream.
type TaskToolDeps struct {
    Registry     *SubagentRegistry
    WS           WSPublisher
    Description  string
    WorkspaceID  uuid.UUID
    ParentTaskID uuid.UUID
}

// NewTaskTool builds the `task` types.Tool go-ai dispatches when the parent
// agent calls task(subagent_type, description, instructions).
func NewTaskTool(deps TaskToolDeps) *TaskTool {
    return &TaskTool{deps: deps, Parameters: taskToolSchema}
}

// TaskTool implements types.Tool by holding the schema + an Execute bound to
// the registry. go-ai calls Execute with the raw JSON input.
type TaskTool struct {
    deps       TaskToolDeps
    Parameters json.RawMessage // JSON Schema for the tool's input
}

func (t *TaskTool) Name() string        { return "task" }
func (t *TaskTool) Description() string { return t.deps.Description }
func (t *TaskTool) InputSchema() json.RawMessage { return t.Parameters }

func (t *TaskTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
    var input struct {
        SubagentType string `json:"subagent_type"`
        Description  string `json:"description"`
        Instructions string `json:"instructions"`
        Context      string `json:"context,omitempty"`
    }
    if err := json.Unmarshal(raw, &input); err != nil {
        return nil, fmt.Errorf("task: invalid input: %w", err)
    }
    if input.SubagentType == "" {
        return nil, fmt.Errorf("task: subagent_type required")
    }
    if input.Description == "" {
        return nil, fmt.Errorf("task: description required")
    }

    callID := uuid.New()
    t.deps.WS.Publish(t.deps.WorkspaceID, "subagent.started", map[string]any{
        "call_id":       callID,
        "parent_task":   t.deps.ParentTaskID,
        "subagent_type": input.SubagentType,
        "description":   input.Description,
    })

    // Wire a ProgressFn into the child ctx so the child harness (Phase 2
    // OnStepFinish callback) can forward interim progress as subagent.step
    // WS events. Keyed on callID so nested siblings don't collide.
    ws := t.deps.WS
    wsID := t.deps.WorkspaceID
    ctx = WithProgressFn(ctx, func(p SubagentProgress) {
        ws.Publish(wsID, "subagent.step", map[string]any{
            "call_id": callID,
            "step":    p.Step,
            "text":    p.Text,
            "usage_cents": p.UsageCents,
        })
    })

    out, err := t.deps.Registry.Dispatch(ctx, input.SubagentType, SubagentInput{
        Description:  input.Description,
        Instructions: input.Instructions,
        Context:      input.Context,
    })
    if err != nil {
        t.deps.WS.Publish(t.deps.WorkspaceID, "subagent.finished", map[string]any{
            "call_id": callID,
            "error":   err.Error(),
        })
        return nil, err
    }

    t.deps.WS.Publish(t.deps.WorkspaceID, "subagent.finished", map[string]any{
        "call_id":     callID,
        "text":        out.Text,
        "usage_cents": out.UsageCents,
        "finish":      out.FinishReason,
    })

    return json.Marshal(map[string]any{
        "text":        out.Text,
        "usage_cents": out.UsageCents,
        "finish":      out.FinishReason,
    })
}

// Assert TaskTool satisfies go-ai's types.Tool. If go-ai changes the
// interface shape, the compile error here is the canary.
var _ types.Tool = (*TaskTool)(nil)

var taskToolSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Slug of the agent to delegate to (see this tool's description for available agents)."
    },
    "description": {
      "type": "string",
      "description": "Short, single-sentence goal for the subagent."
    },
    "instructions": {
      "type": "string",
      "description": "Detailed instructions and constraints. Optional."
    },
    "context": {
      "type": "string",
      "description": "Relevant context the subagent needs. Keep under 4K tokens; use memory.save + memory.recall for large payloads."
    }
  },
  "required": ["subagent_type", "description"],
  "additionalProperties": false
}`)
```

Note: `types.Tool` is go-ai's interface (`github.com/digitallysavvy/go-ai/pkg/types`). If the actual interface in your vendored copy uses `InputSchema()` returning a different type, adjust both the return type and the method name — the compile-time assertion will catch it.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/harness/ -run TestTaskTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/harness/task_tool.go server/pkg/agent/harness/task_tool_test.go
git commit -m "feat(harness): task tool — JSON Schema + executor with WS lifecycle

Tool calls SubagentRegistry.Dispatch under the parent's context so depth
+ stack propagate; emits subagent.started/finished events bracketing the
child turn. On guard error the 'finished' event carries the error text."
```

---

### Task 5: `task`-Tool Description Rendering (Capability Roster)

**Files:**
- Create: `server/pkg/agent/prompts/subagents.go`
- Create: `server/pkg/agent/prompts/subagents_test.go`

**Goal:** format the roster into the markdown block we inject as the `task` tool's `description`. The LLM sees this string at tool-discovery time and uses it to pick the right subagent slug.

- [ ] **Step 1: Write the failing test**

```go
// server/pkg/agent/prompts/subagents_test.go
package prompts

import (
    "strings"
    "testing"
)

func TestRenderTaskToolDescription_Empty(t *testing.T) {
    t.Parallel()
    got := RenderTaskToolDescription(nil)
    if !strings.Contains(got, "no subagents") {
        t.Errorf("empty roster output = %q", got)
    }
}

func TestRenderTaskToolDescription_EnumeratesAgents(t *testing.T) {
    t.Parallel()
    roster := []CapabilityRow{
        {Slug: "customer-support", AgentName: "Customer Support",
            Capabilities: []Capability{{Name: "reply_to_ticket", Description: "Draft a reply"}}},
        {Slug: "legal-reviewer", AgentName: "Legal Reviewer",
            Capabilities: []Capability{{Name: "review_clause", Description: "Check a clause"}}},
    }
    got := RenderTaskToolDescription(roster)

    for _, want := range []string{
        "customer-support",
        "reply_to_ticket",
        "Draft a reply",
        "legal-reviewer",
        "review_clause",
    } {
        if !strings.Contains(got, want) {
            t.Errorf("render missing %q in:\n%s", want, got)
        }
    }
}

func TestRenderTaskToolDescription_SlugFirst(t *testing.T) {
    t.Parallel()
    // The slug must be the unambiguous first token of each entry so the LLM
    // copies it verbatim into subagent_type.
    roster := []CapabilityRow{{Slug: "executor", AgentName: "Executor"}}
    got := RenderTaskToolDescription(roster)
    if !strings.Contains(got, "- `executor`") {
        t.Errorf("slug not backticked/first-column: %s", got)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./pkg/agent/prompts/ -run TestRenderTaskToolDescription -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// server/pkg/agent/prompts/subagents.go
package prompts

import (
    "fmt"
    "strings"
)

// Capability is the prompt-layer mirror of service.CapabilityEntry. Duplicated
// rather than imported because pkg/agent/prompts must not depend on
// internal/service (internal/ → pkg/ dep direction only).
type Capability struct {
    Name        string
    Description string
}

type CapabilityRow struct {
    Slug         string
    AgentName    string
    Capabilities []Capability
}

const taskToolIntro = `Call this tool to delegate work to another agent. ` +
    `Pass the agent's slug as subagent_type. Available agents:`

const taskToolNoSubagents = `No subagents are available in this workspace. ` +
    `The task tool is disabled; do not call it.`

// RenderTaskToolDescription turns a roster into the text the LLM reads when
// deciding which subagent to call. Markdown list with the slug bolded in
// backticks so there's no ambiguity about what to pass as subagent_type.
func RenderTaskToolDescription(roster []CapabilityRow) string {
    if len(roster) == 0 {
        return taskToolNoSubagents
    }
    var b strings.Builder
    b.WriteString(taskToolIntro)
    b.WriteString("\n\n")
    for _, r := range roster {
        fmt.Fprintf(&b, "- `%s` — %s", r.Slug, r.AgentName)
        if len(r.Capabilities) == 0 {
            b.WriteString(" (no published capabilities; agent will improvise)")
            b.WriteString("\n")
            continue
        }
        b.WriteString("\n")
        for _, c := range r.Capabilities {
            fmt.Fprintf(&b, "    - %s: %s\n", c.Name, c.Description)
        }
    }
    return b.String()
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./pkg/agent/prompts/ -run TestRenderTaskToolDescription -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/agent/prompts/subagents.go server/pkg/agent/prompts/subagents_test.go
git commit -m "feat(prompts): render task-tool description from capability roster

Slug backticked and prepended so the LLM copies it verbatim into
subagent_type. Empty rosters produce a 'tool disabled' notice instead
of an empty list."
```

---

### Task 6: DelegationService Wiring — Registry Builder + Task-Tool Factory

**Files:**
- Modify: `server/internal/service/delegation.go`
- Modify: `server/internal/service/delegation_test.go`

**Goal:** `DelegationService.BuildTaskTool(ctx, wsID, parentAgentID, parentTaskID)` composes Tasks 2–5 and returns either `(tool, subagentRegistry, true)` when the workspace has delegatable siblings or `(nil, nil, false)` when it doesn't. The worker resolver (Task 7) uses this to conditionally add the `task` tool.

- [ ] **Step 1: Write the failing test**

Append the two new tests below to `server/internal/service/delegation_test.go`. The file already exists from Task 2 and already has a `package service` declaration + import block containing at least `context`, `testing`, `github.com/google/uuid`, and `aicolab/server/internal/testutil`. Merge the new imports into that existing block — do NOT introduce a second `import (...)` group (invalid Go). Required additional imports: `"errors"`, `"strings"`, `"aicolab/server/pkg/agent/harness"`, `"aicolab/server/pkg/agent/prompts"`. Then paste the two test functions verbatim.

```go
// Appended to the existing TestLoadRoster... tests in delegation_test.go.
// No new `package` declaration. Imports merged into the existing import
// block (see instructions above).

func TestBuildTaskTool_DisabledInSoloWorkspace(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "solo")
    me := testutil.SeedAgent(db, wsID, "only", "anthropic", "claude-sonnet-4-5")
    svc := NewDelegationService(DelegationDeps{
        DB:     db,
        Budget: testutil.AlwaysAllowBudget,
        WS:     testutil.NoopWS,
    })
    tool, reg, enabled := svc.BuildTaskTool(context.Background(), wsID, me, uuid.New())
    if enabled {
        t.Errorf("enabled = true in solo workspace; tool=%v reg=%v", tool, reg)
    }
}

func TestBuildTaskTool_EnabledWithSibling(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    wsID := testutil.SeedWorkspace(db, "team")
    me := testutil.SeedAgent(db, wsID, "parent", "anthropic", "claude-sonnet-4-5")
    sib := testutil.SeedAgent(db, wsID, "helper", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedCapability(db, wsID, sib, "do_thing", "Does a thing")

    // Explicit success stub — the default notImplementedRunChild would let
    // this test pass vacuously (any non-ErrSubagentNotFound error would pass).
    okRunChild := func(ctx context.Context, slug string, in harness.SubagentInput) (harness.SubagentOutput, error) {
        return harness.SubagentOutput{Text: "child-" + slug}, nil
    }
    svc := NewDelegationService(DelegationDeps{
        DB:       db,
        Budget:   testutil.AlwaysAllowBudget,
        WS:       testutil.NoopWS,
        RunChild: okRunChild,
    })
    tool, reg, enabled := svc.BuildTaskTool(context.Background(), wsID, me, uuid.New())
    if !enabled { t.Fatal("enabled = false with sibling present") }
    if tool == nil || reg == nil { t.Fatal("nil tool/registry despite enabled=true") }

    // Description should surface the sibling's slug + capability.
    desc := tool.Description()
    if !strings.Contains(desc, "helper") || !strings.Contains(desc, "do_thing") {
        t.Errorf("description missing sibling info:\n%s", desc)
    }

    // Wiring assertion: dispatch end-to-end must succeed with a no-op child.
    out, err := reg.Dispatch(context.Background(), "helper", harness.SubagentInput{Description: "x"})
    if err != nil { t.Fatalf("Dispatch returned err = %v; want nil", err) }
    if out.Text != "child-helper" {
        t.Errorf("Dispatch output text = %q; want child-helper (confirms stub was called)", out.Text)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/service/ -run TestBuildTaskTool -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Extend `DelegationDeps` + `DelegationService`:

```go
// Replace the existing DelegationDeps/NewDelegationService block in delegation.go.

import (
    "aicolab/server/pkg/agent/harness"
    "aicolab/server/pkg/agent/prompts"
)

type BudgetAllowFn = harness.BudgetAllowFn

type WSPub = harness.WSPublisher

type RunChildFn = harness.RunChildFn

type DelegationDeps struct {
    DB       *pgxpool.Pool
    Budget   BudgetAllowFn      // from Phase 1 BudgetService; wrapper that opens a tx + runs CanDispatch
    WS       WSPub              // Phase 2 WSPublisher
    RunChild RunChildFn         // set by resolver; closes over the worker's child-harness factory
}

type DelegationService struct {
    db       *pgxpool.Pool
    budget   BudgetAllowFn
    ws       WSPub
    runChild RunChildFn
}

func NewDelegationService(deps DelegationDeps) *DelegationService {
    if deps.RunChild == nil {
        deps.RunChild = notImplementedRunChild
    }
    return &DelegationService{
        db:       deps.DB,
        budget:   deps.Budget,
        ws:       deps.WS,
        runChild: deps.RunChild,
    }
}

func notImplementedRunChild(ctx context.Context, slug string, in harness.SubagentInput) (harness.SubagentOutput, error) {
    return harness.SubagentOutput{}, fmt.Errorf("run-child not wired; resolver must set DelegationDeps.RunChild")
}

// BuildTaskTool composes Tasks 2–5 into (tool, registry, enabled). Returns
// enabled=false when the workspace has zero delegatable siblings — the caller
// then omits the task tool entirely (keeps the tool list minimal).
func (s *DelegationService) BuildTaskTool(
    ctx context.Context, wsID, parentAgentID, parentTaskID uuid.UUID,
) (tool *harness.TaskTool, reg *harness.SubagentRegistry, enabled bool) {
    roster, err := s.LoadRoster(ctx, wsID, parentAgentID)
    if err != nil || len(roster) == 0 {
        return nil, nil, false
    }

    stubs := make([]harness.RosterStub, 0, len(roster))
    capRows := make([]prompts.CapabilityRow, 0, len(roster))
    for _, r := range roster {
        stubs = append(stubs, harness.RosterStub{AgentID: r.AgentID, Slug: r.Slug})
        caps := make([]prompts.Capability, 0, len(r.Capabilities))
        for _, c := range r.Capabilities {
            caps = append(caps, prompts.Capability{Name: c.Name, Description: c.Description})
        }
        capRows = append(capRows, prompts.CapabilityRow{
            Slug: r.Slug, AgentName: r.DisplayName, Capabilities: caps,
        })
    }

    reg = harness.BuildSubagentRegistry(harness.SubagentRegistryDeps{
        Roster:      stubs,
        ParentAgent: parentAgentID,
        BudgetAllow: s.budget,
        RunChild:    s.runChild,
    })
    tool = harness.NewTaskTool(harness.TaskToolDeps{
        Registry:     reg,
        WS:           s.ws,
        Description:  prompts.RenderTaskToolDescription(capRows),
        WorkspaceID:  wsID,
        ParentTaskID: parentTaskID,
    })
    return tool, reg, true
}
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/service/ -run TestBuildTaskTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/service/delegation.go server/internal/service/delegation_test.go
git commit -m "feat(delegation): BuildTaskTool composes roster + registry + tool

Returns enabled=false for solo workspaces so the worker resolver can
skip adding the task tool when there's nothing to delegate to."
```

---

### Task 7: Worker Resolver — Attach `task` Tool + SubagentRegistry

**Files:**
- Modify: `server/internal/worker/resolver.go`
- Modify: `server/internal/worker/resolver_test.go`

**Goal:** at task-claim time, before constructing the `LLMAPIBackend`, the resolver calls `delegation.BuildTaskTool` and — when enabled — appends the tool to the built-in tool list AND sets `harness.Config.Subagents` to the returned registry. The `RunChild` closure is constructed here because only the resolver has access to the child-harness factory (it recursively calls its own agent-config resolution for the child).

- [ ] **Step 1: Write the failing test**

```go
// Append to server/internal/worker/resolver_test.go.

func TestResolve_AttachesTaskTool_WhenSiblingExists(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    ws := testutil.SeedWorkspace(db, "team")
    parent := testutil.SeedAgent(db, ws, "parent", "anthropic", "claude-sonnet-4-5")
    sib := testutil.SeedAgent(db, ws, "helper", "anthropic", "claude-haiku-4-5-20251001")
    testutil.SeedCapability(db, ws, sib, "do_thing", "Does a thing")
    task := testutil.EnqueueTask(db, ws, parent)

    r := NewResolver(ResolverDeps{DB: db, Delegation: testutil.StubDelegationService(db)})
    cfg, err := r.Resolve(context.Background(), task)
    if err != nil { t.Fatal(err) }

    names := toolNames(cfg.Tools)
    if !contains(names, "task") {
        t.Errorf("expected `task` in tools, got %v", names)
    }
    if cfg.Subagents == nil {
        t.Error("Subagents registry not attached")
    }
}

func TestResolve_OmitsTaskTool_InSoloWorkspace(t *testing.T) {
    t.Parallel()
    db := testutil.NewTestDB(t)
    ws := testutil.SeedWorkspace(db, "solo")
    me := testutil.SeedAgent(db, ws, "only", "anthropic", "claude-sonnet-4-5")
    task := testutil.EnqueueTask(db, ws, me)

    r := NewResolver(ResolverDeps{DB: db, Delegation: testutil.StubDelegationService(db)})
    cfg, err := r.Resolve(context.Background(), task)
    if err != nil { t.Fatal(err) }

    names := toolNames(cfg.Tools)
    if contains(names, "task") {
        t.Errorf("solo workspace should not expose `task` tool, got %v", names)
    }
    if cfg.Subagents != nil {
        t.Error("Subagents registry should be nil in solo workspace")
    }
}
```

Helpers (add to `resolver_test.go` if not already present):

```go
func toolNames(ts []types.Tool) []string {
    out := make([]string, 0, len(ts))
    for _, t := range ts { out = append(out, t.Name()) }
    return out
}

func contains(ss []string, want string) bool {
    for _, s := range ss { if s == want { return true } }
    return false
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/worker/ -run "TestResolve_AttachesTaskTool|TestResolve_OmitsTaskTool" -v`
Expected: FAIL — resolver doesn't touch DelegationService yet.

- [ ] **Step 3: Implement**

```go
// Modify server/internal/worker/resolver.go — after the tool registry is
// built and before constructing the harness config, add:

import (
    "aicolab/server/internal/service"
    "aicolab/server/pkg/agent/harness"
)

type ResolverDeps struct {
    DB         *pgxpool.Pool
    Delegation *service.DelegationService
    // ... existing deps stay intact (ToolRegistry, MCP manager, Credentials, ...)
}

// Inside Resolver.Resolve(ctx, task), AFTER the Phase 2/3 tool list is built
// and BEFORE harness.Config is constructed:

// The RunChild closure is created HERE (and only here) because it needs
// recursive access to the resolver's own Resolve call — so a child dispatch
// reuses the full Phase 2/3/5 wiring (credentials, MCP, memory injection)
// for the child agent.
runChild := func(ctx context.Context, slug string, in harness.SubagentInput) (harness.SubagentOutput, error) {
    // Look up the child agent by slug in the workspace.
    // See Task 2.5 for the GetAgentBySlug implementation (app-side slug
    // comparison on ListWorkspaceAgentsForSlugLookup rows).
    childAgent, err := r.deps.Delegation.GetAgentBySlug(ctx, task.WorkspaceID, slug)
    if err != nil { return harness.SubagentOutput{}, err }

    // Recursively resolve a config for the child (this re-enters Resolve
    // via a synthetic "child task" record — see NewChildTask helper).
    childTask := NewChildTask(task, childAgent.ID, in.Description, in.Instructions)
    childCfg, err := r.Resolve(ctx, childTask)
    if err != nil { return harness.SubagentOutput{}, err }

    // Spawn a fresh Harness with the SubagentMaxSteps cap. Wire the
    // OnStepFinishEvent callback to forward the child's per-step event as a
    // subagent.step via the ProgressFn stashed in ctx by the task tool.
    //
    // Field-name note: Phase 2's harness.Config.OnStepFinishEvent is the
    // STRUCTURED callback (ctx + OnStepFinishEvent with .Text, .Usage, .Step).
    // Do NOT use the unrelated `OnStepFinish` name — it's the legacy go-ai
    // callback shape and is not what Phase 2 wires through.
    childCfg.MaxSteps = mcagent.SubagentMaxSteps
    stepN := 0
    var cumCents int
    baseOnStep := childCfg.OnStepFinishEvent // preserve any existing Phase 2 callback
    childCfg.OnStepFinishEvent = func(ctx context.Context, e ai.OnStepFinishEvent) {
        if baseOnStep != nil { baseOnStep(ctx, e) }
        stepN++
        cumCents += service.EstimateStepCents(e.Usage, childCfg.Model)
        if fn := harness.ProgressFnFrom(ctx); fn != nil {
            // e.Text is the model's text content for this step (field from
            // go-ai callback_events.go); previous drafts used e.Summary
            // which does not exist on OnStepFinishEvent.
            fn(harness.SubagentProgress{Step: stepN, Text: e.Text, UsageCents: cumCents})
        }
    }

    h := harness.New(childCfg)
    prompt := in.Description
    if in.Instructions != "" { prompt = prompt + "\n\nInstructions:\n" + in.Instructions }
    if in.Context != "" { prompt = prompt + "\n\nContext:\n" + in.Context }

    res, err := h.Execute(ctx, prompt)
    if err != nil { return harness.SubagentOutput{}, err }

    // UsageCents is computed by the Phase 2 cost calculator; the same
    // cost_event row was ALREADY written by the child's backend under the
    // shared trace_id, so the parent's trace aggregation rolls it up
    // automatically (Phase 9).
    return harness.SubagentOutput{
        Text:         res.Text,
        UsageCents:   service.EstimateCostCents(res.Usage, childCfg.Model),
        FinishReason: string(res.FinishReason),
    }, nil
}

// BudgetAllowFn wraps Phase 1's BudgetService.CanDispatch.
//
// Concurrency caveat (KNOWN LIMITATION — tracked as Phase 9 follow-up):
// this is an OPTIMISTIC check-then-dispatch. We open a short tx solely to
// satisfy CanDispatch's signature (which takes a pgx.Tx because PLAN.md
// §1.2 R3 B5 envisions the caller holding the advisory lock across a
// budget-committing DB write). Phase 6 does NOT write the child's first
// cost_event inside this tx — that row is produced by the child's own
// backend during Execute, long after Commit here has released the lock.
//
// Net effect: under concurrent subagent dispatches against the same
// workspace, budget may be exceeded by one hop's worth between the check
// and the child's first cost_event write. For a workspace at hard-stop
// ceiling, up to N concurrent dispatches (where N is the number of
// in-flight resolvers) may each see themselves as "allowed" simultaneously.
// This is much weaker than the §1.2 R3 B5 guarantee but adequate for
// Phase 6's scope — the conservative `estDispatchCents=100` floor in
// SubagentRegistry.Dispatch gives additional headroom.
//
// Phase 9 hardening path: restructure so the child's first cost_event
// write happens inside this tx (requires threading tx through the child
// harness or pre-writing a pending cost_event placeholder here). Filed as
// a Phase 9 issue with this note as context.
budgetAllow := func(ctx context.Context, estCents int) (bool, string) {
    tx, err := r.deps.DB.Begin(ctx)
    if err != nil { return false, fmt.Sprintf("begin tx: %v", err) }
    defer tx.Rollback(ctx)
    dec, err := r.deps.Budget.CanDispatch(ctx, tx, task.WorkspaceID, estCents)
    if err != nil { return false, fmt.Sprintf("budget check: %v", err) }
    if !dec.Allow { return false, dec.Reason }
    if err := tx.Commit(ctx); err != nil { return false, fmt.Sprintf("commit: %v", err) }
    return true, ""
}

// Build a scoped DelegationService for this task with the runChild closure
// and budget wrapper wired in.
del := r.deps.Delegation.
    WithRunChild(runChild).
    WithBudget(budgetAllow)
tool, reg, enabled := del.BuildTaskTool(ctx, task.WorkspaceID, task.AgentID, task.ID)
if enabled {
    cfg.Tools = append(cfg.Tools, tool)
    // go-ai's AgentConfig.Subagents is a concrete *goagent.SubagentRegistry
    // that stores go-ai Agent values (Register(name, Agent)). Our internal
    // *harness.SubagentRegistry has a different shape (Dispatch()), so we
    // adapt through harness.AdaptToGoAI which registers one goagent.Agent
    // per slug whose Execute method calls reg.Dispatch.
    cfg.Subagents = harness.AdaptToGoAI(reg)
}
```

Add `DelegationService.WithRunChild` and `.WithBudget` so the service remains reusable across tasks:

```go
// Append to server/internal/service/delegation.go.

// WithRunChild returns a copy of the service with the provided RunChild
// closure. Used by the resolver to attach a per-task child dispatcher
// without mutating a shared singleton.
func (s *DelegationService) WithRunChild(fn RunChildFn) *DelegationService {
    cp := *s
    cp.runChild = fn
    return &cp
}

// WithBudget returns a copy of the service with a task-scoped BudgetAllowFn.
// The wrapper always opens a fresh tx so pg_advisory_xact_lock scope is
// correct (PLAN.md §1.2 R3 B5).
func (s *DelegationService) WithBudget(fn BudgetAllowFn) *DelegationService {
    cp := *s
    cp.budget = fn
    return &cp
}
```

Extend `BudgetService` from Phase 1 to ensure `CanDispatch`'s return type is exported:

```go
// Phase 1 already defines this; shown here so the wrapper above compiles:
// type Decision struct { Allow bool; Reason string; WarnAt float64 }
// func (b *BudgetService) CanDispatch(ctx context.Context, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error)
```

**Cost-estimator functions** (`service.EstimateStepCents` + `service.EstimateCostCents`). The `runChild` closure above uses these to translate go-ai's per-step and per-turn `types.Usage` into cents. Phase 2 Task 14 ("Cost Calculator (Cache-Aware Pricing)") already builds the pricing tables and a per-call `CostCalculator.ComputeCents(usage, model)` function. Surface two thin exports from that service so Phase 6 can call them without importing the Phase 2 internals directly:

```go
// Append to server/internal/service/cost_estimator.go (Phase 6 addition,
// delegating to the Phase 2 CostCalculator already wired into the service
// container).

// EstimateStepCents is the cents cost of a single step's incremental usage
// (delta input + output + cache read/write tokens). Adds to the running
// per-turn total for subagent progress events.
func EstimateStepCents(u types.Usage, model string) int {
    return costCalc.ComputeCents(u, model)
}

// EstimateCostCents is the cents cost of a full agent turn's total usage.
// Same math as EstimateStepCents (Usage is cumulative by the time the child
// returns); exposed separately so callers document intent.
func EstimateCostCents(u types.Usage, model string) int {
    return costCalc.ComputeCents(u, model)
}
```

`costCalc` is a package-level `*CostCalculator` initialised at service startup (Phase 2 Task 14 already does this in `NewServer`). If Phase 2's API names differ at the point Phase 6 lands, adjust these two exports to delegate to whatever the Phase 2 implementation actually surfaces — the Phase 6 callers reference `service.EstimateStepCents` / `service.EstimateCostCents` only and are insulated from Phase 2's internal naming.

**go-ai integration.** Per D2's audit of `other-projects/go-ai/pkg/agent/subagent.go`, go-ai's `SubagentRegistry` is a **concrete struct** (not an interface) that stores go-ai `Agent` values keyed by slug, registered via `Register(name string, agent Agent) error`. Our `*harness.SubagentRegistry` has a different shape (`Dispatch(ctx, slug, SubagentInput)`), so we adapt through a registrar that instantiates one go-ai `Agent` per slug whose `Execute` delegates back to our `Dispatch` — preserving all our guards (depth, cycle, self, budget, timeout).

Add to `server/pkg/agent/harness/subagents.go`:

```go
import (
    goagent "github.com/digitallysavvy/go-ai/pkg/agent"
    "github.com/digitallysavvy/go-ai/pkg/types"
)

// AdaptToGoAI wraps our *SubagentRegistry behind go-ai's concrete
// *goagent.SubagentRegistry so it can be assigned to AgentConfig.Subagents.
// One goagent.Agent value is registered per slug; each delegates to the
// source registry's Dispatch, preserving all guards.
func AdaptToGoAI(src *SubagentRegistry) *goagent.SubagentRegistry {
    dst := goagent.NewSubagentRegistry()
    for slug := range src.bySlug {
        slug := slug // loop-var capture
        _ = dst.Register(slug, &goaiAgentShim{src: src, slug: slug})
    }
    return dst
}

// goaiAgentShim satisfies go-ai's Agent interface via one Execute method
// that calls into our Dispatch. Only the shape required by SubagentRegistry
// is implemented — additional Agent methods (if any) can no-op or delegate
// through once the Phase 2 Harness adapter surfaces them.
type goaiAgentShim struct {
    src  *SubagentRegistry
    slug string
}

func (s *goaiAgentShim) Execute(ctx context.Context, prompt string) (*goagent.AgentResult, error) {
    out, err := s.src.Dispatch(ctx, s.slug, SubagentInput{Description: prompt})
    if err != nil { return nil, err }
    return &goagent.AgentResult{
        Text: out.Text,
        FinishReason: types.FinishReason(out.FinishReason),
    }, nil
}
```

And expose `bySlug` as a method on `SubagentRegistry` if go's struct embedding doesn't let `AdaptToGoAI` reach the unexported field (e.g. add a `Slugs() []string` getter):

```go
// Add to SubagentRegistry in subagents.go.
func (r *SubagentRegistry) Slugs() []string {
    ks := make([]string, 0, len(r.bySlug))
    for k := range r.bySlug { ks = append(ks, k) }
    return ks
}
```

Then the adapter loop above switches to `for _, slug := range src.Slugs() {` which avoids reaching into a private field.

Compile-time sanity test:

```go
// server/pkg/agent/harness/subagents_goai_test.go
package harness

import (
    "context"
    "testing"

    "github.com/google/uuid"
)

func TestAdaptToGoAI_Compiles_AndDispatches(t *testing.T) {
    t.Parallel()
    parent := uuid.New()
    child := uuid.New()
    reg := BuildSubagentRegistry(SubagentRegistryDeps{
        Roster:      []RosterStub{{AgentID: child, Slug: "child"}},
        ParentAgent: parent,
        BudgetAllow: alwaysBudgetAllow,
        RunChild: func(ctx context.Context, slug string, in SubagentInput) (SubagentOutput, error) {
            return SubagentOutput{Text: "ok", FinishReason: "stop"}, nil
        },
    })
    gr := AdaptToGoAI(reg)
    if gr == nil { t.Fatal("AdaptToGoAI returned nil") }

    // Exercise the adapter end-to-end through go-ai's registry API.
    ag, err := gr.Get("child") // goagent.SubagentRegistry exposes Get(name) (Agent, error)
    if err != nil { t.Fatal(err) }
    res, err := ag.Execute(context.Background(), "say hi")
    if err != nil { t.Fatalf("Execute: %v", err) }
    if res.Text != "ok" { t.Errorf("text = %q", res.Text) }
}
```

This test is the canary: if go-ai v0.5+ renames `Register`/`Get`/`Agent`/`AgentResult`, the compile error here points the implementer at the fork-policy runbook (`docs/engineering/go-ai-fork-policy.md`) before Phase 6's runtime depends on stale assumptions.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/worker/ -run "TestResolve_AttachesTaskTool|TestResolve_OmitsTaskTool" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/worker/resolver.go server/internal/worker/resolver_test.go server/internal/service/delegation.go
git commit -m "feat(worker): attach task tool + SubagentRegistry at resolve time

RunChild closure recursively reuses Resolve so child agents get the
same Phase 2/3/5 wiring (credentials, MCP, memory) as top-level
tasks. Solo workspaces get neither the tool nor the registry."
```

---

### Task 8: Capability Discovery HTTP Endpoint (`GET /capabilities/discoverable`)

**Files:**
- Modify: `server/internal/handler/capability.go`
- Modify: `server/internal/handler/capability_test.go`
- Modify: `server/cmd/server/router.go`

**Goal:** a read endpoint the UI uses to power the capability-browser panel. Same payload shape as `GET /workspaces/{wsId}/capabilities` (Phase 1) but with one parameter `agent_id` that excludes that agent's own capabilities. The UI picker for "which agent to delegate to" never offers the viewer's own slug.

- [ ] **Step 1: Write the failing test**

```go
// Append to server/internal/handler/capability_test.go.

func TestListDiscoverableCapabilities_ExcludesCaller(t *testing.T) {
    tc := newCapabilityHandlerCtx(t)
    me := tc.createAgent("me")
    sib := tc.createAgent("sibling")
    tc.createCapability(me, "my_cap", "caller capability")
    tc.createCapability(sib, "sibling_cap", "sibling capability")

    resp := tc.getJSON("/workspaces/" + tc.wsID + "/capabilities/discoverable?agent_id=" + me)
    if resp.Code != 200 { t.Fatalf("status = %d", resp.Code) }
    if strings.Contains(resp.Body, "my_cap") {
        t.Error("discoverable listing included caller's own capability")
    }
    if !strings.Contains(resp.Body, "sibling_cap") {
        t.Error("discoverable listing missing sibling capability")
    }
}

func TestListDiscoverableCapabilities_RequiresAgentID(t *testing.T) {
    tc := newCapabilityHandlerCtx(t)
    resp := tc.getJSON("/workspaces/" + tc.wsID + "/capabilities/discoverable")
    if resp.Code != 400 { t.Fatalf("status = %d, want 400", resp.Code) }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/handler/ -run TestListDiscoverableCapabilities -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

Add to `server/internal/handler/capability.go`:

```go
func (h *CapabilityHandler) ListDiscoverable(w http.ResponseWriter, r *http.Request) {
    wsID, err := uuidFromParam(r, "wsId")
    if err != nil { http.Error(w, "invalid workspace id", 400); return }

    agentIDRaw := r.URL.Query().Get("agent_id")
    if agentIDRaw == "" { http.Error(w, "agent_id query param required", 400); return }
    callerID, err := uuid.Parse(agentIDRaw)
    if err != nil { http.Error(w, "invalid agent_id", 400); return }

    rows, err := h.q.ListCapabilitiesByWorkspace(r.Context(), uuidPg(wsID))
    if err != nil { http.Error(w, err.Error(), 500); return }

    out := make([]DiscoverableCapabilityDTO, 0, len(rows))
    for _, row := range rows {
        if uuidFromPg(row.AgentID) == callerID { continue }
        // Slug is computed by the SAME function BuildSubagentRegistry uses
        // (service.Slugify re-exports pkg/agent/harness/service's slugify
        // so backend and UI agree on the exact key the `task` tool expects).
        // Divergence here would let the UI offer a slug the tool rejects.
        out = append(out, DiscoverableCapabilityDTO{
            ID:          uuidFromPg(row.ID),
            AgentID:     uuidFromPg(row.AgentID),
            AgentName:   row.AgentName,
            Slug:        service.Slugify(row.AgentName),
            Name:        row.Name,
            Description: row.Description,
        })
    }
    writeJSON(w, 200, out)
}

// DiscoverableCapabilityDTO is the wire shape for GET /capabilities/discoverable.
// Slug is authoritative — the frontend renders it verbatim, never re-slugifies.
type DiscoverableCapabilityDTO struct {
    ID          uuid.UUID `json:"id"`
    AgentID     uuid.UUID `json:"agent_id"`
    AgentName   string    `json:"agent_name"`
    Slug        string    `json:"slug"`
    Name        string    `json:"name"`
    Description string    `json:"description"`
}
```

Also promote `slugify` from a private function in `delegation.go` (Task 2) to an exported `service.Slugify` so the handler can call it:

```go
// Replace slugify in server/internal/service/delegation.go with the exported name:
func Slugify(s string) string { /* same body as the Task 2 slugify */ }

// Inside LoadRoster, swap `slugify(a.Name)` to `Slugify(a.Name)`.
```

Add a handler-level assertion to the Task 8 tests so slug round-trips:

```go
func TestListDiscoverableCapabilities_SlugMatchesRegistry(t *testing.T) {
    tc := newCapabilityHandlerCtx(t)
    me := tc.createAgent("me")
    sib := tc.createAgent("Customer Support") // space in the name exercises slugify
    tc.createCapability(sib, "reply", "replies")

    resp := tc.getJSON("/workspaces/" + tc.wsID + "/capabilities/discoverable?agent_id=" + me)
    if !strings.Contains(resp.Body, `"slug":"customer-support"`) {
        t.Errorf("expected slug=customer-support in response: %s", resp.Body)
    }
}
```

Mount in `server/cmd/server/router.go`:

```go
r.Get("/workspaces/{wsId}/capabilities/discoverable", capH.ListDiscoverable)
```

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/handler/ -run TestListDiscoverableCapabilities -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/capability.go server/internal/handler/capability_test.go server/cmd/server/router.go
git commit -m "feat(capability): GET /capabilities/discoverable (excludes caller)

Drives the UI's delegation-target picker. Filters in-memory because the
roster is small (<100 rows per workspace typical); future SQL-level
filter is trivial if profiling shows it."
```

---

### Task 9: Frontend API Client + TanStack Query Hooks

**Files:**
- Create: `packages/core/api/capability.ts`
- Create: `packages/core/capability/queries.ts`
- Modify: `packages/core/api/index.ts`
- Modify: `packages/core/types/agent.ts`

**Goal:** typed client + React Query hooks. TanStack Query owns the data cache per CLAUDE.md. A single WS subscriber invalidates when capabilities mutate so the picker stays fresh without polling.

- [ ] **Step 1: Write the failing test**

```typescript
// packages/core/capability/queries.test.ts
import { describe, it, expect, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useDiscoverableCapabilities } from "./queries";

const api = {
  capability: {
    discoverable: vi.fn(),
  },
};

vi.mock("../api", () => ({ api }));

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("useDiscoverableCapabilities", () => {
  it("fetches and returns the roster", async () => {
    api.capability.discoverable.mockResolvedValue([
      { id: "c1", agent_id: "a1", agent_name: "Helper", name: "do_x", description: "does x" },
    ]);
    const { result } = renderHook(() => useDiscoverableCapabilities("ws1", "me"), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toHaveLength(1);
    expect(api.capability.discoverable).toHaveBeenCalledWith("ws1", "me");
  });
});
```

- [ ] **Step 2: Run failing test**

Run: `pnpm --filter @multica/core exec vitest run capability/queries.test.ts`
Expected: FAIL — module doesn't exist.

- [ ] **Step 3: Implement**

```typescript
// packages/core/api/capability.ts
import type { ApiClient } from "./client";

// Mirrors server DiscoverableCapabilityDTO exactly. `slug` is authoritative —
// the UI must render it verbatim (never re-slugify in TypeScript; the backend
// owns the canonical form so `task(subagent_type=slug)` always resolves).
export type DiscoverableCapability = {
  id: string;
  agent_id: string;
  agent_name: string;
  slug: string;
  name: string;
  description: string;
};

export function capabilityApi(http: ApiClient) {
  return {
    list: (wsId: string) => http.get<DiscoverableCapability[]>(`/workspaces/${wsId}/capabilities`),
    search: (wsId: string, q: string) =>
      http.get<DiscoverableCapability[]>(`/workspaces/${wsId}/capabilities/search?q=${encodeURIComponent(q)}`),
    discoverable: (wsId: string, agentId: string) =>
      http.get<DiscoverableCapability[]>(`/workspaces/${wsId}/capabilities/discoverable?agent_id=${agentId}`),
  };
}
```

```typescript
// packages/core/capability/queries.ts
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

export const capabilityKey = (wsId: string) => ["capability", wsId] as const;

export const useCapabilities = (wsId: string) =>
  useQuery({
    queryKey: [...capabilityKey(wsId), "list"],
    queryFn: () => api.capability.list(wsId),
  });

export const useSearchCapabilities = (wsId: string, q: string) =>
  useQuery({
    queryKey: [...capabilityKey(wsId), "search", q],
    queryFn: () => api.capability.search(wsId, q),
    enabled: q.trim().length > 0,
  });

export const useDiscoverableCapabilities = (wsId: string, agentId: string) =>
  useQuery({
    queryKey: [...capabilityKey(wsId), "discoverable", agentId],
    queryFn: () => api.capability.discoverable(wsId, agentId),
    enabled: !!agentId,
  });
```

```typescript
// packages/core/api/index.ts — add to the ApiClient factory:
import { capabilityApi } from "./capability";

export function createApi(http: HttpClient) {
  return {
    // ... existing sub-namespaces
    capability: capabilityApi(http),
  };
}
```

```typescript
// packages/core/types/agent.ts — append:
export type Capability = {
  id: string;
  agent_id: string;
  name: string;
  description: string;
  input_schema?: unknown;
  output_schema?: unknown;
};
```

- [ ] **Step 4: Verify tests pass**

Run: `pnpm --filter @multica/core exec vitest run capability/queries.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/core/api/capability.ts packages/core/capability/queries.ts packages/core/api/index.ts packages/core/types/agent.ts packages/core/capability/queries.test.ts
git commit -m "feat(capability): API client + TanStack Query hooks

useDiscoverableCapabilities powers the agent-picker in the delegation
UI. Query key scoped to (wsId, agentId) so switching workspaces/agents
does the right thing automatically."
```

---

### Task 10: Capability Browser + Nested Subagent Card UI

**Files:**
- Create: `packages/views/agents/components/capability-browser.tsx`
- Create: `packages/views/agents/components/tabs/capability-tab.tsx`
- Modify: `packages/views/agents/components/agent-detail.tsx`
- Create: `packages/views/tasks/components/nested-subagent-card.tsx`
- Modify: `packages/views/tasks/components/tool-call-card.tsx`

**Goal:** two UI surfaces. (a) On the agent detail page, a Capabilities tab that lists the other agents this one can delegate to. (b) In the task view, when a `task` tool call appears, render it as a nested card with the child's own tool-call substream.

- [ ] **Step 1: Write the failing test**

```tsx
// packages/views/agents/components/capability-browser.test.tsx
import { describe, it, expect, vi } from "vitest";
import { renderWithProviders } from "../../test-utils";
import { screen } from "@testing-library/react";
import { CapabilityBrowser } from "./capability-browser";

const mocks = vi.hoisted(() => ({
  useDiscoverableCapabilities: vi.fn(),
}));
vi.mock("@multica/core/capability/queries", () => mocks);

describe("CapabilityBrowser", () => {
  it("renders grouped capabilities per agent", () => {
    mocks.useDiscoverableCapabilities.mockReturnValue({
      data: [
        { id: "c1", agent_id: "a1", agent_name: "Helper", name: "do_x", description: "does x" },
        { id: "c2", agent_id: "a1", agent_name: "Helper", name: "do_y", description: "does y" },
        { id: "c3", agent_id: "a2", agent_name: "Other",  name: "do_z", description: "does z" },
      ],
      isSuccess: true,
    });
    renderWithProviders(<CapabilityBrowser wsId="w1" agentId="me" />);
    expect(screen.getByText("Helper")).toBeInTheDocument();
    expect(screen.getByText("Other")).toBeInTheDocument();
    expect(screen.getByText("do_x")).toBeInTheDocument();
    expect(screen.getByText("do_z")).toBeInTheDocument();
  });

  it("shows empty state in solo workspace", () => {
    mocks.useDiscoverableCapabilities.mockReturnValue({ data: [], isSuccess: true });
    renderWithProviders(<CapabilityBrowser wsId="w1" agentId="me" />);
    expect(screen.getByTestId("capability-empty")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run failing test**

Run: `pnpm --filter @multica/views exec vitest run agents/components/capability-browser.test.tsx`
Expected: FAIL — component missing.

- [ ] **Step 3: Implement**

```tsx
// packages/views/agents/components/capability-browser.tsx
import { useDiscoverableCapabilities } from "@multica/core/capability/queries";
import type { DiscoverableCapability } from "@multica/core/api/capability";

export function CapabilityBrowser({ wsId, agentId }: { wsId: string; agentId: string }) {
  const { data = [], isSuccess } = useDiscoverableCapabilities(wsId, agentId);

  if (isSuccess && data.length === 0) {
    return (
      <div data-testid="capability-empty" className="p-6 text-sm text-muted-foreground">
        This workspace has no other agents to delegate to. Add an agent with at least one capability to enable the <code>task</code> tool.
      </div>
    );
  }

  const groups = groupByAgent(data);
  return (
    <div className="flex flex-col gap-4 p-6">
      {groups.map((g) => (
        <section key={g.agentId} className="rounded border border-border bg-muted/20 p-4">
          <h3 className="text-sm font-semibold">{g.agentName}</h3>
          <p className="text-xs text-muted-foreground">Slug: <code>{g.slug}</code></p>
          <ul className="mt-2 flex flex-col gap-1">
            {g.capabilities.map((c) => (
              <li key={c.id} className="text-sm">
                <strong>{c.name}</strong> — <span className="text-muted-foreground">{c.description}</span>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

type Group = {
  agentId: string;
  agentName: string;
  slug: string;
  capabilities: DiscoverableCapability[];
};

function groupByAgent(rows: DiscoverableCapability[]): Group[] {
  const by = new Map<string, Group>();
  for (const r of rows) {
    if (!by.has(r.agent_id)) {
      by.set(r.agent_id, {
        agentId: r.agent_id,
        agentName: r.agent_name,
        slug: r.slug, // authoritative — provided by backend; NEVER re-slugify here
        capabilities: [],
      });
    }
    by.get(r.agent_id)!.capabilities.push(r);
  }
  return Array.from(by.values()).sort((a, b) => a.agentName.localeCompare(b.agentName));
}
```

```tsx
// packages/views/agents/components/tabs/capability-tab.tsx
import { CapabilityBrowser } from "../capability-browser";

export function CapabilityTab({ wsId, agentId }: { wsId: string; agentId: string }) {
  return <CapabilityBrowser wsId={wsId} agentId={agentId} />;
}
```

```tsx
// packages/views/agents/components/agent-detail.tsx — add the tab
<TabsTrigger value="capabilities">Delegation</TabsTrigger>
<TabsContent value="capabilities"><CapabilityTab wsId={wsId} agentId={agent.id} /></TabsContent>
```

```tsx
// packages/views/tasks/components/nested-subagent-card.tsx
import { useSubagentEvents } from "../subagent-events";

export function NestedSubagentCard({ callId }: { callId: string }) {
  const events = useSubagentEvents(callId); // hook must be called unconditionally at the top
  const started = events.find((e) => e.kind === "subagent.started");
  const finished = events.find((e) => e.kind === "subagent.finished");
  if (!started) return null;

  return (
    <div className="rounded border border-border bg-muted/20 p-3 text-sm">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>↳ subagent</span>
        <code>{started.payload.subagent_type}</code>
        {finished?.payload.error
          ? <span className="text-destructive">failed</span>
          : finished
            ? <span className="text-emerald-600">done · {finished.payload.usage_cents}¢</span>
            : <span className="animate-pulse">running…</span>}
      </div>
      <p className="mt-1">{started.payload.description}</p>
      {finished?.payload.text && <pre className="mt-2 whitespace-pre-wrap">{finished.payload.text}</pre>}
      {finished?.payload.error && <pre className="mt-2 text-destructive">{finished.payload.error}</pre>}
    </div>
  );
}
```

```tsx
// packages/views/tasks/components/tool-call-card.tsx — detect task tool
import { NestedSubagentCard } from "./nested-subagent-card";

// inside the component, where tool calls are rendered:
// Hooks must run unconditionally per React's Rules of Hooks, so the event
// subscription lives INSIDE NestedSubagentCard (Task 10's card component),
// not in this conditional branch. ToolCallCard just decides which renderer
// to hand off to.
if (call.name === "task") {
  return <NestedSubagentCard callId={call.id} />;
}
```

`NestedSubagentCard` must accept `callId` and call `useSubagentEvents(callId)` at the top of its body (shown in the card component above — `const events = useSubagentEvents(callId)`). Update the Task 10 signature if it still takes `events` as a prop.

Add the `useSubagentEvents` hook with its own failing test before implementing (the nested card won't render without it):

```typescript
// packages/views/tasks/subagent-events.test.ts
import { describe, it, expect, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useSubagentEvents } from "./subagent-events";
import { wsHub } from "@multica/core/ws/hub";

describe("useSubagentEvents", () => {
  it("filters to the matching call_id", () => {
    const { result } = renderHook(() => useSubagentEvents("call-123"));
    expect(result.current).toEqual([]);

    act(() => {
      wsHub.emit("subagent.started", { call_id: "call-123", description: "x" });
      wsHub.emit("subagent.step",    { call_id: "OTHER",    step: 1, text: "y" }); // ignored
      wsHub.emit("subagent.step",    { call_id: "call-123", step: 1, text: "thinking" });
      wsHub.emit("subagent.finished",{ call_id: "call-123", text: "done" });
    });

    const kinds = result.current.map((e) => e.kind);
    expect(kinds).toEqual(["subagent.started", "subagent.step", "subagent.finished"]);
  });

  it("unsubscribes on unmount", () => {
    const off = vi.fn();
    vi.spyOn(wsHub, "on").mockReturnValue(off);
    const { unmount } = renderHook(() => useSubagentEvents("call-123"));
    unmount();
    expect(off).toHaveBeenCalled();
  });
});
```

```typescript
// packages/views/tasks/subagent-events.ts
import { useEffect, useState } from "react";
import { wsHub } from "@multica/core/ws/hub";

export type SubagentEvent = {
  kind: "subagent.started" | "subagent.step" | "subagent.finished";
  payload: Record<string, unknown>;
};

// Subscribes to the three subagent.* WS channels and returns the ordered list
// of events keyed to a single call_id. Nested NestedSubagentCard instances
// never cross-talk because the filter is call_id-based.
export function useSubagentEvents(callId: string): SubagentEvent[] {
  const [events, setEvents] = useState<SubagentEvent[]>([]);
  useEffect(() => {
    if (!callId) return;
    const offs = (["subagent.started", "subagent.step", "subagent.finished"] as const).map((kind) =>
      wsHub.on(kind, (payload: Record<string, unknown>) => {
        if (payload.call_id !== callId) return;
        setEvents((prev) => [...prev, { kind, payload }]);
      }),
    );
    return () => { for (const off of offs) off(); };
  }, [callId]);
  return events;
}
```

Run the test:

```bash
pnpm --filter @multica/core exec vitest run tasks/subagent-events.test.ts
```
Expected: PASS.

- [ ] **Step 4: Verify tests pass**

Run: `pnpm --filter @multica/views exec vitest run agents/components/capability-browser.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/views/agents/components/capability-browser.tsx packages/views/agents/components/capability-browser.test.tsx packages/views/agents/components/tabs/capability-tab.tsx packages/views/agents/components/agent-detail.tsx packages/views/tasks/components/nested-subagent-card.tsx packages/views/tasks/components/tool-call-card.tsx packages/views/tasks/subagent-events.ts packages/views/tasks/subagent-events.test.ts
git commit -m "feat(views): capability browser + nested subagent card + WS hook

Browser lists delegatable siblings grouped by agent using the backend's
authoritative slug (no client-side slugify). Nested card renders
task-tool calls as collapsible blocks showing subagent lifecycle
(started/step+/running/done/error) via useSubagentEvents."
```

---

### Task 11: Integration Test — Two-Agent Happy Path

**Files:**
- Create: `server/internal/integration/delegation/chain_test.go`

**Goal:** a Go-level integration test that exercises the full path: parent agent gets a task → invokes `task("helper", ...)` via a mocked LLM → child runs and returns → cost events share the parent's `trace_id` → WS emits the expected envelope. Lives under `server/internal/integration/` (NOT the repo-root `e2e/` directory, which is reserved for Playwright in TypeScript).

- [ ] **Step 1: Write the failing test**

```go
// server/internal/integration/delegation/chain_test.go
package delegation

import (
    "context"
    "testing"
    "time"

    "github.com/google/uuid"

    "aicolab/server/internal/testsupport"
)

func TestDelegation_AToB_TraceAggregation(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    env := testsupport.NewEnv(t) // spins up DB + in-process worker

    wsID := env.SeedWorkspace("acme")
    parent := env.SeedAgent(wsID, "parent", testsupport.StubModel([]testsupport.TurnScript{
        {ToolCall: &testsupport.ToolCall{
            Name: "task",
            Args: `{"subagent_type":"helper","description":"do a thing"}`,
        }},
        {Text: "parent finishing now"},
    }))
    helper := env.SeedAgent(wsID, "helper", testsupport.StubModel([]testsupport.TurnScript{
        {Text: "thing done"},
    }))
    env.SeedCapability(wsID, helper, "do_thing", "does a thing")

    taskID := env.EnqueueTask(wsID, parent, "parent prompt")

    env.WaitForTaskComplete(ctx, taskID)

    costs := env.ListCostEvents(taskID)
    if len(costs) < 2 {
        t.Fatalf("expected ≥2 cost events (parent + child), got %d", len(costs))
    }
    traceID := costs[0].TraceID
    for _, c := range costs {
        if c.TraceID != traceID {
            t.Errorf("child cost event trace_id=%s, want %s (shared chain)", c.TraceID, traceID)
        }
    }

    events := env.WSEventsForTask(taskID)
    got := map[string]int{}
    for _, e := range events {
        got[e.Kind]++
    }
    if got["subagent.started"] != 1 || got["subagent.finished"] != 1 {
        t.Errorf("ws events = %+v, want exactly one started + one finished", got)
    }
    _ = uuid.Nil
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/integration/delegation/ -run TestDelegation_AToB_TraceAggregation -v`
Expected: FAIL (until the full chain is wired through `make check`).

- [ ] **Step 3: Drive the tests green**

No new production code — at this point the chain should work. Common failure modes and their fixes:
- **Child cost event has a new trace_id**: verify `TraceIDFrom(ctx)` is NOT re-minted in the child path (the resolver's `runChild` closure must propagate the same context).
- **WS events missing**: confirm `WSPublisher` is injected into `DelegationService` on startup (wire in `server/cmd/server/main.go`).
- **`task` tool absent**: confirm Task 7's resolver integration ran — `cfg.Tools` must include it when the workspace has siblings.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/integration/delegation/ -run TestDelegation_AToB_TraceAggregation -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/integration/delegation/chain_test.go server/cmd/server/main.go
git commit -m "test(integration): parent→child delegation with shared trace_id

Verifies both cost events carry the parent's trace_id (Phase 9's
chain-cost ceiling depends on this) and that WS emits exactly one
subagent.started + subagent.finished pair."
```

---

### Task 12: Integration Test — Guard Rejections

**Files:**
- Create: `server/internal/integration/delegation/guards_test.go`

**Goal:** exercise each of the four guard paths (self, cycle, depth, budget). Each must fail the dispatch BEFORE the child produces any cost event.

- [ ] **Step 1: Write the failing test**

```go
// server/internal/integration/delegation/guards_test.go
package delegation

import (
    "context"
    "strings"
    "testing"
    "time"

    "aicolab/server/internal/testsupport"
)

func TestDelegation_RejectsSelfDelegation(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    env := testsupport.NewEnv(t)

    ws := env.SeedWorkspace("acme")
    parent := env.SeedAgent(ws, "parent", testsupport.StubModel([]testsupport.TurnScript{
        {ToolCall: &testsupport.ToolCall{
            Name: "task",
            Args: `{"subagent_type":"parent","description":"loop"}`, // calls self
        }},
        {Text: "giving up"},
    }))
    sib := env.SeedAgent(ws, "helper", testsupport.StubModel(nil))
    env.SeedCapability(ws, sib, "do_thing", "just here so task tool is enabled")

    taskID := env.EnqueueTask(ws, parent, "go")
    env.WaitForTaskComplete(ctx, taskID)

    toolResults := env.ListToolCallResults(taskID)
    if len(toolResults) == 0 {
        t.Fatal("no tool results captured")
    }
    if !strings.Contains(toolResults[0].Error, "self-delegation") {
        t.Errorf("tool result error = %q, want self-delegation", toolResults[0].Error)
    }

    // No child cost event should exist.
    for _, c := range env.ListCostEvents(taskID) {
        if c.AgentID == parent { continue } // parent's own cost is fine
        t.Errorf("unexpected child cost event: %+v", c)
    }
}

func TestDelegation_RejectsDepthOverflow(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    env := testsupport.NewEnv(t)

    ws := env.SeedWorkspace("acme")
    // Six agents, each delegating to the next. Last hop should be rejected.
    agents := []string{"a0", "a1", "a2", "a3", "a4", "a5"}
    ids := make(map[string]string)
    for i, name := range agents {
        var next string
        if i+1 < len(agents) { next = agents[i+1] }
        script := []testsupport.TurnScript{}
        if next != "" {
            script = append(script, testsupport.TurnScript{
                ToolCall: &testsupport.ToolCall{
                    Name: "task",
                    Args: `{"subagent_type":"` + next + `","description":"pass"}`,
                },
            })
        }
        script = append(script, testsupport.TurnScript{Text: "done"})
        id := env.SeedAgent(ws, name, testsupport.StubModel(script))
        ids[name] = id
        // Each agent needs a capability so `task` tool is enabled.
        env.SeedCapability(ws, id, "pass", "propagates")
    }

    taskID := env.EnqueueTask(ws, ids["a0"], "start")
    env.WaitForTaskComplete(ctx, taskID)

    // Somewhere along the chain the depth guard fires; surface it in the tool
    // result error on whichever agent tried to go too deep.
    results := env.ListToolCallResults(taskID)
    sawDepthErr := false
    for _, r := range results {
        if strings.Contains(r.Error, "depth exceeded") {
            sawDepthErr = true
        }
    }
    if !sawDepthErr {
        t.Errorf("no depth-exceeded error found in tool results: %+v", results)
    }
}

func TestDelegation_RejectsOverBudget(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    env := testsupport.NewEnv(t)

    ws := env.SeedWorkspace("acme")
    env.SeedBudgetPolicy(ws, 1) // 1 cent workspace cap, hard_stop=true
    parent := env.SeedAgent(ws, "parent", testsupport.StubModel([]testsupport.TurnScript{
        {ToolCall: &testsupport.ToolCall{
            Name: "task",
            Args: `{"subagent_type":"helper","description":"please"}`,
        }},
        {Text: "done"},
    }))
    helper := env.SeedAgent(ws, "helper", testsupport.StubModel([]testsupport.TurnScript{
        {Text: "would work"},
    }))
    env.SeedCapability(ws, helper, "do_thing", "x")

    taskID := env.EnqueueTask(ws, parent, "go")
    env.WaitForTaskComplete(ctx, taskID)

    results := env.ListToolCallResults(taskID)
    foundBudget := false
    for _, r := range results {
        if strings.Contains(r.Error, "budget") {
            foundBudget = true
        }
    }
    if !foundBudget {
        t.Errorf("no budget error in tool results: %+v", results)
    }
}
```

- [ ] **Step 2: Run failing test**

Run: `cd server && go test ./internal/integration/delegation/ -run TestDelegation_Rejects -v`
Expected: three FAILs initially if any guard path is broken. If guards are wired correctly per Tasks 3 + 6, most of this will pass immediately.

- [ ] **Step 3: Fix any gaps**

If a guard fails to fire as expected, look at:
- `SubagentRegistry.Dispatch` — does the check run before `runChild`?
- `BudgetAllowFn` — is the resolver wrapping Phase 1's `BudgetService.CanDispatch` inside a fresh tx (per PLAN.md §1.2 R3 B5)?
- Depth propagation — verify `WithDelegationDepth(ctx, depth+1)` lines up with `DelegationDepthFrom` in the child.

- [ ] **Step 4: Verify tests pass**

Run: `cd server && go test ./internal/integration/delegation/ -run TestDelegation_Rejects -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/integration/delegation/guards_test.go
git commit -m "test(integration): guard rejections — self/depth/budget block dispatch

Each guard verified to short-circuit before the child produces a cost
event. Cycle rejection is covered by the unit tests in Task 3 — adding
an e2e variant would require 3 agents + message shape plumbing for
marginal coverage."
```

---

### Task 13: Phase 6 Verification

**Files:**
- Modify: `docs/engineering/verification/phase-6.md` (create if absent)

**Goal:** run the end-of-phase gates (PLAN.md §6.6) and record evidence. No code here — this task closes out the phase.

- [ ] **Step 1: Run unit + integration tests**

```bash
cd server && go test ./pkg/agent/harness/... ./pkg/agent/prompts/... ./internal/service/ ./internal/handler/ -run "Delegation|Subagent|TaskTool|Render|Capability" -v
```

Expected: every Phase 6 test passes.

- [ ] **Step 2: Run integration slice**

```bash
cd server && go test ./internal/integration/delegation/... -v
```

Expected: PASS.

- [ ] **Step 3: Run full check**

```bash
make check
```

Expected: PASS.

- [ ] **Step 4: Manual verification checklist**

Start server + worker + a dev frontend, seed two agents in a workspace:

- [ ] **`task` tool discoverable**: in the parent agent's task view, when the parent LLM lists available tools, `task` appears with a description enumerating the sibling.
- [ ] **Happy path**: assigning an issue to the parent produces a parent-first → child → parent-finishes run; the frontend renders a nested card for the child.
- [ ] **trace_id shared**: Phase 9 (if shipped) can already run `SELECT SUM(cost_cents) FROM cost_event WHERE trace_id = $1` and get the full chain cost. (Or confirm manually by inspecting `cost_event.trace_id` values — they match across parent + child rows.)
- [ ] **Self-rejection**: temporarily configure the parent's stub LLM to call `task('<own-slug>')`; confirm the tool result carries "self-delegation rejected" error and the child does not run.
- [ ] **Depth cap**: chain 6 agents A→B→C→D→E→F; confirm F's dispatch is rejected with "delegation depth exceeded".
- [ ] **Budget block**: set workspace `monthly_limit_cents=1, hard_stop=true`; confirm the `task` call is rejected with budget_exceeded BEFORE the child consumes tokens.
- [ ] **Empty workspace**: remove all other agents; confirm the parent's tool list no longer contains `task`.
- [ ] **Capability browser UI**: agent detail page shows the "Delegation" tab listing siblings + their capabilities; opening it with the sibling removed updates in under 5 s (WS invalidation).

- [ ] **Step 5: Write evidence file**

```markdown
# Phase 6 Verification — 2026-04-DD

- `go test ./pkg/agent/harness/...` → pass (N tests)
- `go test ./internal/service/ -run Delegation` → pass
- `go test ./internal/integration/delegation/...` → pass
- `make check` → pass
- Manual: [date, environment, results of the 8 items above]
- Known follow-ups (tracked in separate issues):
  - None required for Phase 6 per PLAN.md §6.6.
- Deferred-but-documented: parallel subagents, HITL approval on dispatch (both Phase 7).
```

- [ ] **Step 6: Commit**

```bash
git add docs/engineering/verification/phase-6.md
git commit -m "docs(phase-6): end-of-phase verification evidence"
```

---

## Placeholder Scan

Performed a self-review pass. Three spots sketch helpers rather than producing full code; each is an explicit "fill-in" marker whose shape mirrors patterns from earlier tasks:

1. Task 2's `uuidPg` / `uuidFromPg` helpers — already exist elsewhere in `server/internal/service/` (Phase 2). Reused, not redefined.
2. Task 7's `NewChildTask` + `buildGoAISubagentRegistry` adapter — their shapes depend on go-ai v0.4.0's exact public `SubagentRegistry` API. If go-ai exposes a function-based registration, implement by looping over `RosterStub`s. If it uses a struct slice, build the slice in the adapter.
3. Task 10's `useSubagentEvents` — a trivial WS subscription hook. One file, ~20 lines; implement as a small selector on top of the existing `useTaskWSEvents` hook (present in `packages/views/tasks/` from Phase 2 chat work).

All three are referenced consistently across every task that depends on them. No TBDs, no "similar to Task N" shorthand, no unreferenced symbols.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-14-phase6-a2a-delegation.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints.

Which approach?
