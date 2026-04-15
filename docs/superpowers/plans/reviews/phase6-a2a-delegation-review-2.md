# Phase 6 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase6-a2a-delegation.md`
Source of truth: `PLAN.md` §Phase 6 (located by H2 heading) + D1–D8 invariants
Round 1 (applied): see `phase6-a2a-delegation-review-1.md` — 3 blocking + 5 should-fix applied.

---

## Blocking Issues

- [ ] `e.Summary` field does not exist on go-ai's `OnStepFinishEvent` — Task 7, Step 3 `runChild` closure (line ~1689) — The actual field name in `other-projects/go-ai/pkg/ai/callback_events.go:155` is `Text string`. Change `Text: e.Summary` to `Text: e.Text`. Without this fix the `runChild` closure will not compile.

- [ ] `cfg.Subagents = reg` type mismatch: `AgentConfig.Subagents` is `*goagent.SubagentRegistry` (a concrete struct with `Register(name string, agent Agent) error`), but `reg` is `*harness.SubagentRegistry` — unrelated types — Task 7, Step 3 (line ~1739) — The plan must provide the adapter that instantiates `goagent.NewSubagentRegistry()`, loops over our `bySlug` map, and calls `.Register(slug, agentAdapter)` where `agentAdapter` is a thin `Agent`-interface implementation whose `Execute` delegates to `harness.SubagentRegistry.Dispatch`. The Task 7 prose at lines 1775–1801 describes two hypothetical paths; Round 2 confirms only the adapter path is viable, so the plan must pick it and show the code.

- [ ] `budgetAllow` closure in Task 7 commits the transaction (line ~1725) before `runChild` runs, releasing any `pg_advisory_xact_lock` held by `CanDispatch` before the child writes its first cost event — Task 7, Step 3 (lines 1713–1727) — The advisory-lock serialization PLAN.md §1.2 R3 B5 requires is only effective if the child's first DB write happens INSIDE the same transaction that holds the lock. Either (a) pass the open `tx` into `runChild` so the child writes its first cost event before `Commit`, or (b) stop claiming §1.2 R3 B5 compliance — document explicitly that Phase 6's check is optimistic check-then-dispatch with a small race window, and track the stronger serialization as a Phase 9 follow-up.

## Should Fix

- [ ] `TestBuildTaskTool_EnabledWithSibling` passes vacuously: the test calls `reg.Dispatch(ctx, "helper", ...)` and only checks that the error is not `ErrSubagentNotFound`. `notImplementedRunChild` returns a non-nil error that is ALSO not `ErrSubagentNotFound`, so the test passes regardless of whether the registry resolved the slug correctly — Task 6, Step 1 (lines 1446–1451) — Either assert `err == nil` after setting an explicit success stub on `RunChild`, or split the assertion so a failed Dispatch is a test failure. The current "will fail because RunChild is a stub" comment describes a known false pass, not genuine test signal.

- [ ] `service.EstimateStepCents` and `service.EstimateCostCents` are referenced in Task 7 `runChild` (lines 1687 and 1707) but never defined anywhere in the plan — Task 7, Step 3 — Add a sub-step that either (a) references the Phase 2 cost calculator if those functions already exist there (Phase 2 Task 14 defines `CostCalculator`), or (b) provides their definitions with input types (`types.Usage`, model string) and return types (cents as `int`).

- [ ] `childCfg.OnStepFinish` used in Task 7 (lines 1683–1690) does not match Phase 2's `harness.Config` field name — Phase 2's struct has `OnStepFinishEvent func(ctx context.Context, e ai.OnStepFinishEvent)` — Task 7, Step 3 — Rename the field assignment from `OnStepFinish` to `OnStepFinishEvent` and update the closure's signature to `func(ctx context.Context, e ai.OnStepFinishEvent)`. The plan's `baseOnStep := childCfg.OnStepFinish` line also needs the same rename.

## Nits

- Task 13's verification file evidence template still mentions `./e2e/delegation/...` even though Tasks 11–12 were relocated to `server/internal/integration/delegation/` in round 1. Minor — update for consistency.
- `TestBuildSubagentRegistry_PropagatesParentDeadline` uses `time.Now().Add(2 * time.Second)` as the parent deadline. Since `runChildOK` returns immediately, a 100 ms deadline is sufficient and less flaky under CI load.

## Verdict

The plan is not ready to advance to Round 3. Two blocking issues are compile-time errors (the non-existent `e.Summary` field on `OnStepFinishEvent`, and the `cfg.Subagents = reg` type mismatch between `*harness.SubagentRegistry` and go-ai's concrete `*agent.SubagentRegistry` which takes `Agent` interface values). The third blocking issue is a correctness claim the code does not deliver: the plan's own comment cites PLAN.md §1.2 R3 B5 but the transaction is committed before the child runs, releasing the advisory lock too early. Fix the three blocking items plus the three should-fix items (vacuous-pass test, undefined cost-estimator functions, callback-field name) before Round 3.
