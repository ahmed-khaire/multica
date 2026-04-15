# Phase 6 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase6-a2a-delegation.md`
Source of truth: `PLAN.md` §Phase 6 + D1–D8 invariants
Prior rounds: round 1 (completeness) + round 2 (technical soundness) applied — see reviews/phase6-a2a-delegation-review-{1,2}.md.

---

## Blocking Issues

- [ ] `testutil` and `testsupport` packages are assumed to exist but do not — Tasks 2, 6, 7, 11, 12 all call `testutil.NewTestDB`, `testutil.SeedWorkspace`, `testutil.SeedAgent`, `testutil.SeedCapability`, `testutil.AlwaysAllowBudget`, `testutil.NoopWS`, `testutil.StubDelegationService`, and `testsupport.NewEnv`, `testsupport.StubModel`, `testsupport.TurnScript`, `testsupport.SeedBudgetPolicy`, `testsupport.ListToolCallResults`, etc. `server/internal/testutil/` and `server/internal/testsupport/` do not exist. The plan never specifies a task to create them. The failing tests for Tasks 2, 6, 7, 11, and 12 all fail at `undefined: testutil`/`testsupport` rather than at the intended missing production symbol. The plan must add a Preconditions section (or pre-Task 0) that points at the Phase 2 tasks which own these helpers and enumerates exactly which symbols Phase 6 consumes.

- [ ] `go-ai` is not in `server/go.mod` — the module is `github.com/multica-ai/multica/server` and `go.mod` lists no `github.com/digitallysavvy/go-ai` dependency. Tasks 4 and 7 import `github.com/digitallysavvy/go-ai/pkg/types` and `github.com/digitallysavvy/go-ai/pkg/agent`. The compile-time assertion `var _ types.Tool = (*TaskTool)(nil)` in `task_tool.go` and the `AdaptToGoAI` function in `subagents.go` fail to build until the dependency is added. PLAN.md §1.10 assigns go-ai dependency pinning to Phase 1; the plan must either verify Phase 1 has landed or include an explicit `go get github.com/digitallysavvy/go-ai@v0.4.0` pre-step.

- [ ] Phase 2 surfaces referenced (harness package, `defaults.go`, `BudgetService`, `WSPublisher`, worker resolver, cost calculator) do not exist in the current codebase — `server/pkg/agent/harness/`, `server/pkg/agent/defaults.go`, and a `worker/resolver.go` are absent. The plan states these are "from Phase 2" and treats them as available, but Phase 2 is not shipped. A Phase 6 agent following the plan task-by-task will fail at Task 3's Step 2 (importing `mcagent "aicolab/server/pkg/agent"` for `MaxDelegationDepth`) because no `defaults.go` is present there. The plan must either (a) prefix each task with explicit precondition checks that verify the Phase 2 surface exists, or (b) list an up-front Preconditions section that names the depended-on files with the Phase 2 task numbers that create them.

## Should Fix

- [ ] `useSubagentEvents` hook placed in `packages/core/` violates package-boundary rules — `packages/core/tasks/subagent-events.ts` uses `useState` and `useEffect` which require React. CLAUDE.md for `packages/core/` says "zero react-dom, zero UI libraries" — the constraint applies to what the package imports, not to where it runs. Move the hook and its test to `packages/views/tasks/` (where it's consumed).

- [ ] Task 6's failing test snippet shows a bare `import (...)` block intended to be appended to `delegation_test.go` — this is invalid Go: a file cannot have two `import` blocks side-by-side, and the block lacks the surrounding `func` context. The snippet must either be shown as a complete valid Go source fragment that merges cleanly, or the instruction must explicitly say "merge these imports into the existing import block of `delegation_test.go`".

- [ ] Task 13's verification evidence template still references `./e2e/delegation/...` in the `go test` command — round 1 already moved the Go tests to `server/internal/integration/delegation/` but the evidence template is out of sync and would confuse an auditor.

- [ ] Task 7's `runChild` closure calls `r.deps.DB.Queries().GetAgentBySlug(ctx, task.WorkspaceID, slug)` but `GetAgentBySlug` is not in any sqlc `.sql` queries file; it is neither an existing query nor added by Phase 6. The plan must add a step before Task 7 that authors the SQL query, places it in the correct queries file, and runs `make sqlc`.

## Nits

- Task 10's `tool-call-card.tsx` snippet calls `useSubagentEvents(call.id)` unconditionally inside a conditional `if (call.name === "task")` block — Rules of Hooks violation. Lift the hook into a small `<NestedSubagentCardContainer>` child component so the hook runs at the top of its own function.
- Task 1 Step 2/4 run commands use `-run "TestDelegation|TestStack"` which does NOT match `TestProgressFn_*`. Widen the pattern to `"TestDelegation|TestStack|TestProgressFn"` so the progress-fn tests aren't silently skipped.
- `DelegationService.WithRunChild` / `WithBudget` use `cp := *s` (shallow copy). Fine for current field set but a future mutex field would silently break the semantic. Worth a one-line comment confirming shallow copy is intentional.

## Verdict

The plan is not ready to move forward as-is for **standalone** execution, but it is ready for execution **provided Phase 1 and Phase 2 plans have been implemented first**. The three blocking issues (testutil/testsupport packages, go-ai dependency, Phase 2 surfaces) are all legitimate execution-order dependencies, not design flaws — they resolve as soon as Phase 1 and Phase 2 have shipped. Adding an up-front Preconditions section that lists every precondition surface by name (and points at the Phase 1/2 tasks that produce them) closes all three blockers without restructuring the plan. The four Should Fix items (core-package boundary violation for the React hook, Go import-block snippet, evidence-template path drift, missing `GetAgentBySlug` sqlc query) are small executability polish items.
