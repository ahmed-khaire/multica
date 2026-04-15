# Phase 7 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase7-workflow-orchestration.md`
Source of truth: `PLAN.md` §Phase 7 + D1–D8 invariants
Round 1 applied: 5 blocking + 5 should-fix — see `reviews/phase7-workflow-orchestration-review-1.md`.

---

## Blocking Issues

- [ ] **`WaitingError` is retried up to `maxAttempts` before being detected** — `engine.go` Task 10 `runSingleStep` — The retry loop increments `attempt` and calls `IncrementStepRunRetry` on every error, including `*WaitingError`. The `errors.As` check only runs after the loop exits. A `human_approval` or `sub_workflow` step creates 3 spurious approval requests / 3 sub-runs before being parked. Add an early-exit inside the loop body: after `ExecuteStep` returns, if `errors.As(execErr, &waiting) { break }` (no retry, no `IncrementStepRunRetry`).

- [ ] **`expandFanOut` mutates `wf.Steps` in-memory but synthetic steps are never persisted to `steps_snapshot`** — `engine.go` Task 10 `expandFanOut` + `runLoop` — Each `runLoop` iteration calls `json.Unmarshal(run.StepsSnapshot, &wf)`, giving a fresh `wf` with no synthetic steps. On the next tick, `expandFanOut` appends them again. Restarts drop them entirely — a waiting fan_out can never be resumed. Fix: after expanding, write the augmented `wf` back to `steps_snapshot` via `UPDATE workflow_run SET steps_snapshot = $3` in the same transaction as the child `UpsertStepRun` inserts.

- [ ] **`InsertScheduleFire` DO NOTHING returns `pgx.ErrNoRows`, but code treats any non-nil error as "duplicate already fired"** — `scheduler.go` Task 11 `Tick` — `ON CONFLICT DO NOTHING RETURNING *` returns zero rows on conflict. The code does `if err != nil { continue }`, silently swallowing network failures, lock timeouts, etc. as dedup successes, causing missed fires. Fix: check `errors.Is(err, pgx.ErrNoRows)` specifically; other errors log and continue.

- [ ] **`CreateTimer` UPSERT resets `expires_at` on every `runSingleStep` tick for a waiting step** — `engine.go` Task 10 timer block + Task 3 `CreateTimer` query — `ON CONFLICT (run_id, step_id, timer_name) DO UPDATE SET expires_at = EXCLUDED.expires_at` revives elapsed timers if a stale `pending` status race occurs. Fix: `DO NOTHING` (insert-only), or `DO UPDATE ... WHERE workflow_timer.elapsed_at IS NULL`.

- [ ] **`resumeWaiters` passes `runID` as the row id to `UpdateStepRunComplete`, but the query targets `workflow_step_run.id`** — `engine.go` Task 10 `resumeWaiters` — The code passes `ID: uuidPg(runID)` to every `UpdateStepRunComplete` call. `UpdateStepRunComplete` is `WHERE id = $1 AND workspace_id = $2` where `id` is the step_run UUID. Using `runID` matches no row (or the wrong one if IDs collide). Fix: use `uuidPg(uuidFromPg(r.ID))` — the row's own primary key — in all four resume branches.

## Should Fix

- [ ] **`resumeWaiters` timer branch calls `env.TimerElapsed(ctx, ref)` where `ref` is a timer NAME, not an ID** — `engine.go` Task 10 `runSingleStep` timer park + `resumeWaiters` — The park path sets `"waiting_ref": s.WaitFor.Timers[0]` (a name string). `env.TimerElapsed` signature is `func(ctx, timerID string)`. The existing `TimerElapsedByName(run_id, step_id, timer_name)` query exists but is unused. Fix: change `resumeWaiters` timer case to call `TimerElapsedByName(runID, r.StepID, ref)`. Remove `TimerElapsed` from StepEnv if unused.

- [ ] **WS event payloads missing `workspace_id` for frontend invalidation** — `engine.go` `runSingleStep` + `resumeWaiters` + `expandFanOut` Publish calls / `ws.ts` Task 14 — Frontend reads `e.workspace_id` to pick the right query key. Backend sends `{"run_id": runID, "step_id": s.ID, ...}` without it → frontend invalidates nothing. Add `"workspace_id": wsID` to every `Publisher.Publish` payload.

- [ ] **`evaluateFanOutJoin` compares `r.Status == "completed"` but sqlc may generate a named type** — `engine.go` `evaluateFanOutJoin` — If the generated struct types `Status` as a named type (NullString, or a string alias), raw-string equality won't compile. Document in Task 5 that comparisons against `workflow_step_run.status` use `string(r.Status)` or equivalent.

- [ ] **Concurrent `state.Merge` between `resumeWaiters` and parallel `runSingleStep` goroutines — atomicity ok, ordering not** — `engine.go` `runLoop` — Postgres JSONB concat is atomic, but both paths also write to `workflow_step_run` rows and the `step_results` JSONB column. The current code already runs `resumeWaiters` serially before the parallel goroutine fan-out, which is correct — but this ordering must be an invariant. Add a comment on `runLoop` that `resumeWaiters` must complete before `eg.Wait()` dispatch, and include a regression test for the ordering.

## Nits

- `expandFanOut` synthetic ID separator `::` breaks if branch or inner IDs contain `::`. Validate in `ValidateStep` that IDs do not contain `::`.
- `mustTraceID` re-queries DB every step start for a value already on the `workflow_run` row loaded at the top of `runLoop`.

## Verdict

Five blocking correctness bugs remain, each exercisable by a simple test. The `WaitingError`-retry bug produces duplicate side-effects (3× approvals/sub-runs) the first time a human_approval step runs. The fan-out non-persistence means fan-out is broken across any multi-tick run or restart. `CreateTimer` reset would make timers never naturally expire under certain races. `resumeWaiters` writes to the wrong row id. `InsertScheduleFire` swallows real DB errors as dedup successes. These must be fixed before Round 3. The two shown-fix items (timer-name vs id; missing workspace_id in WS) will silently break the approval/timer UX and all WS-driven invalidation.
