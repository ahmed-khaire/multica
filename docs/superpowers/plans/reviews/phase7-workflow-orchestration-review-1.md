# Phase 7 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase7-workflow-orchestration.md`
Source of truth: `PLAN.md` §Phase 7 (located by H2 heading — currently ~line 1157) + D1–D8 invariants

---

## Blocking Issues

- [ ] **Catch-up policies `one` and `all` are deferred with no concrete task** — Task 11 + Verification Known-limitations — PLAN.md §7.3 lists all three catch-up policies (skip, one, all) as required. The plan says they're "deferred to Task 11.1 (backfill support)" but Task 11.1 doesn't exist. Scheduler tests only exercise `skip_new` overlap + dedup. Add a concrete Task 11.1 implementing both and testing backfill replay.

- [ ] **`fan_out` executor is a stub never completed** — Task 9 executor + Task 10 `expandFanOut` — `fan_out` is one of the 6 required step types. Executor returns "handled by engine"; engine's `expandFanOut` marks step completed without expanding branches. Task 10.1 is referenced but never defined. A `fan_out` step completes immediately without running any branch work. Add Task 10.1 with branch expansion + join-policy tests.

- [ ] **`complete_when` conditional close is entirely absent** — PLAN.md §7.2 names it as a required deliverable ported from IWF — no DSL field (`Step` struct has no `complete_when`), no engine hook, no test. Add DSL field + engine check that evaluates after every state merge and flips run to `completed` when truthy.

- [ ] **`WaitUntil/Execute` timer lifecycle has no implementation** — PLAN.md §7.2 + SkipTimer mentioned in Task 13 — DSL has `WaitFor.Timers []string` but engine never reads it. No timer record, no check path, `SkipTimer` handler has no backing service impl. Required per PLAN.md. Add timer store + engine integration + working SkipTimer endpoint.

- [ ] **`ListActiveWorkflowRuns` query has no workspace filter** — Task 3 workflow.sql — violates CLAUDE.md multi-tenancy invariant and PLAN.md R3 B6. Used by engine `Start()` (resume — all-workspaces case is intentional) AND `applyOverlap` (per-workspace). The second caller pulls all rows and filters in Go, which is a data-leak surface at scale. Add a workspace-filtered variant for `applyOverlap`.

## Should Fix

- [ ] **`buffer` overlap silently equated to `concurrent` in code comment only** — Task 11 `applyOverlap` — PLAN.md §7.3 defines `buffer` as a distinct named policy. V1 equivalence needs to move from code comment into Scope Note deferred list.

- [ ] **`sub_workflow` is fire-and-forget; step completes before child run finishes** — Task 9 `execSubWorkflow` — parent advances past sub_workflow with `{sub_run_id}` immediately. PLAN.md implies blocking semantics. Either make it blocking (poll child status, mirror `human_approval` gate) or explicitly document fire-and-forget as V1 limitation.

- [ ] **Approval gate is broken: step completes before human decides** — Task 9 `execHumanApproval` + Task 10 engine — creates approval, step marked `completed`, DAG unblocks dependents. `wait_for.approvals` field exists in DSL but engine never checks it before advancing. Dependent step fires immediately. Engine must hold step in `waiting` until all `wait_for.approvals` entries reach `approved`.

- [ ] **`BudgetService.CanDispatch` listed in Preconditions but never called** — Task 9 executors — `execAgentTask` and `execToolCall` run without budget gate. Add `CheckBudget` to `StepEnv` and call before `RunAgent`/`InvokeTool`.

- [ ] **`pnpm add -w --filter` is contradictory** — Task 15 Step 1 — `-w` adds to workspace root, `--filter` targets a package. Split into two steps: edit `pnpm-workspace.yaml` catalog manually, then `pnpm add --filter @multica/views @xyflow/react@catalog:`.

## Nits

- Task 10 `mustTraceID` re-queries DB on every step start for a value the outer `runLoop` already loaded; pass through.
- Task 11 scheduler tick runs every 30 s for minute-resolution crons → 0–30 s latency; 10 s tick halves it cheaply.
- Task 15 `WorkflowBuilder` leaves the save path as a code comment; wire it through or note explicit follow-up.
- Task 7 channels `Wait` + `tryConsume` each acquire a separate pool connection — two per waiting step. Note as a resource concern for high parallelism.

## Verdict

Well-structured plan covering six step types, five overlap policies, three catch-up policies by name, dedup table, stop-monitor, D8 trace, and durable resume. Not ready to advance: four deliverables named in PLAN.md §7.2/§7.3 have no concrete task (catch-up one/all, fan_out expansion, complete_when, timer WaitUntil), the approval gate's `wait_for.approvals` contract is silently broken, and an active-runs query skips tenancy scoping. These are plan-level gaps, not implementation nits. Fix five blocking + five should-fix items, then advance to Round 2.
