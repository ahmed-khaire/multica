# Phase 6 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase6-a2a-delegation.md`
Source of truth: `PLAN.md` §Phase 6 (located by H2 heading — current line range ~1102–1154) + D1–D8 invariants (~lines 48–96)

---

## Blocking Issues

- [ ] D2 says go-ai's `SubagentRegistry` is a bare `map[string]Agent` dispatch primitive with **no built-in tool definition**; the plan's §6.1 and PLAN.md §6.1 both contradict this with "A single `task` tool (built-in from go-ai) is registered …" — plan §Architecture note + §6.1 prose — The plan must acknowledge that Phase 6 **authors** the `task` tool itself (which it does correctly in Tasks 4–5), and the §6.1 / Architecture Note prose must be corrected to remove the fiction that go-ai provides a built-in `task` tool; otherwise an implementer will waste time searching for something that doesn't exist.

- [ ] The `subagent.step` WS event is listed as a deliverable in the Scope Note ("interim progress streaming back to parent") and in the file-structure table (`task_tool.go` responsibility row), but it is **never emitted** anywhere in the Task 4 implementation — `Execute` only publishes `subagent.started` and `subagent.finished`. The test in Task 4 (`TestTaskTool_ExecutorEmitsLifecycleEvents`) asserts `len(pub.events) == 2` and only checks for `started` and `finished`, cementing the omission. PLAN.md §6.3(d) lists "interim progress streaming back to parent" as a D2 requirement. — Task 4 — Either implement `subagent.step` emission (child harness progress callback → WSPublisher) or explicitly move interim streaming to a named future task; it cannot be silently dropped.

- [ ] The `DiscoverableCapability` type (defined in `packages/core/api/capability.ts`) lacks a `slug` field — `agent_name` is returned from the backend but the frontend `CapabilityBrowser` derives the slug client-side via a second `slugify` call (Task 10 implementation). This means the slug shown in the UI can differ from the slug the Go `BuildSubagentRegistry` derives via `slugify` in Task 2 (two independent slugify implementations, one in Go, one in TypeScript, with no shared contract). If slugs diverge the capability browser will show a slug the `task` tool rejects. — Task 9 / Task 10 — The backend `GET /capabilities/discoverable` response must include a `slug` field computed by the authoritative Go `slugify`; the frontend should display it verbatim, not re-derive it.

## Should Fix

- [ ] The `DelegationService.BuildTaskTool` signature in Task 6 requires `Budget BudgetAllowFn` in `DelegationDeps`, but the resolver snippet in Task 7 constructs `del := r.deps.Delegation.WithRunChild(runChild)` without showing how `BudgetAllowFn` is wired from `BudgetService.CanDispatch`. PLAN.md §6.3 says "call `budget.CanExecute(workspaceID, agentID)` (Phase 1)" — that API is `BudgetService.CanDispatch(ctx, tx, wsID, estCents)` and requires a transaction. Task 7 gives no guidance on transaction management for the budget check. — Task 7, Step 3 — Add a sentence showing how `BudgetService.CanDispatch` is wrapped into a `BudgetAllowFn` (opens a tx, calls CanDispatch, commits/rolls back).

- [ ] `buildGoAISubagentRegistry` in Task 7 is introduced as "a thin shim" but its signature, file location, and implementation are entirely undefined — the plan says "its shape depends on go-ai's public API" and leaves it to the implementer. Since the whole point of the task-by-task plan is that an agent can execute it without re-reading PLAN.md, this gap makes Task 7 non-executable. — Task 7, Step 3 — Either provide the shim implementation (or a testable stub) in this task, or add a dedicated Task 7.5 that audits `other-projects/go-ai/pkg/agent/subagent.go` and produces the adapter before Task 7 needs it.

- [ ] The `useSubagentEvents` hook referenced in Task 10's `tool-call-card.tsx` snippet is described only as "trivial — out-of-scope for this step's failing-test code," yet the failing test (`NestedSubagentCard`) doesn't test this hook at all, and no task owns it. The WS subscription path is needed for the nested card to function in production and for the Phase 6.6 manual verification step to pass. — Task 10, Step 3 — Add an explicit step (or a Task 10.5) for `packages/core/tasks/subagent-events.ts` with a test asserting the hook subscribes to `subagent.*` events keyed by call_id.

- [ ] The `e2e/delegation/chain_test.go` (Task 11) is placed under `e2e/` but the run command is `cd server && go test ./e2e/delegation/...`. The `e2e/` directory at repo root is for Playwright tests (TypeScript). A Go integration test that spins up a DB + in-process worker belongs in `server/internal/integration/` or `server/e2e/`, not `e2e/`. The `testsupport` import path `aicolab/e2e/testsupport` would not resolve from `server/`. — Tasks 11–12 — Move both chain and guard Go tests to `server/internal/integration/delegation/` (or similar); update import paths and run commands accordingly.

- [ ] PLAN.md §6.3 mentions a **timeout guardrail** ("each subagent call runs under a context with deadline = min(parent remaining, subagent `SubagentMaxSteps * per-step-timeout`)"), but the plan has no task, no code, and no test for it. The budget guard is tested; the timeout guard is completely absent. — Plan-wide — Add a step in Task 3 or Task 7 that attaches a `context.WithTimeout` to `childCtx` before calling `runChild`; add a test asserting a context with an expired deadline propagates to the child.

## Nits

- The `PLAN.md §6.3` cycle-detection description uses "delegation stack (`parent.trace_id → child.agent_id`)" — this is the old conflated wording D8 explicitly corrected. The plan document correctly distinguishes them, so this is only a PLAN.md authoring leftover, but worth flagging to prevent future confusion.
- Task 13 creates `docs/engineering/verification/phase-6.md` but the file-structure tables list no such file. Minor inconsistency; no impact on execution.
- `TestDelegation_RejectsDepthOverflow` (Task 12) seeds 6 agents (a0–a5) to test depth > 5, but each agent also gets a capability seeded on itself (`env.SeedCapability(ws, id, ...)`), which means `a0`'s registry will include `a0` in its own roster — this doesn't break the test but the comment "each agent needs a capability so `task` tool is enabled" is misleading (capabilities are on the *sibling*, not the agent itself). No functional impact given BuildSubagentRegistry excludes the caller.

## Verdict

The plan is close to ready but has three blocking issues that would cause an agent to build something that doesn't match the invariants or doesn't work end-to-end: the go-ai "built-in task tool" fiction needs correction, the `subagent.step` interim-streaming deliverable is silently missing, and the dual-slugify bug will cause slug mismatches between backend and UI. Additionally, the Go integration tests are placed in the wrong directory (will fail to compile), and two concrete code surfaces (`buildGoAISubagentRegistry` adapter and `useSubagentEvents` hook) are left undefined in tasks that claim to be self-contained. Fix the three blocking issues and the four Should Fix items before moving to Round 2.
