# Phase 7 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase7-workflow-orchestration.md`
Source of truth: `PLAN.md` §Phase 7 + D1–D8
Prior rounds: R1 (5+5 applied), R2 (5+3 applied).

---

## Blocking Issues

- [ ] `engine.go` import block missing `"strings"` — Task 10 — `strings.EqualFold` in `CompleteWhen` block + `strings.HasPrefix`/`SplitN`/`TrimPrefix` in `expandFanOut`/`evaluateFanOutJoin`. File won't compile as copy-pasted. Add `"strings"` to the import block.

- [ ] `scheduler.go` imports missing `"errors"` and `"github.com/jackc/pgx/v5"` — Task 11 — `errors.Is(err, pgx.ErrNoRows)` in both `Tick` and `CatchUp`. Will not compile.

- [ ] `mustTraceID` uses `dbPkg` alias that's never declared — Task 10 — comment says "actual file uses `db.New`" but snippet uses `dbPkg.New` and `dbPkg.GetWorkflowRunParams`. Inconsistent for a copy-paste agent. Rewrite the function to use `db.New` consistently OR remove the alias comment.

- [ ] `IncrementStepRunRetry` called with wrong ID — Task 10 `runSingleStep` — `IncrementStepRunRetry(ctx, db.IncrementStepRunRetryParams{ID: uuidPg(runID), ...})` passes the workflow run UUID, but the query targets `workflow_step_run.id` (the row's own PK). Retries silently no-op. Capture the row from `UpsertStepRun` and pass its `.ID`.

- [ ] `testsupport` package has no definition — Task 16 — `testsupport.NewEnv`, `.StubModel`, `.TurnScript`, `env.WaitForApproval`, `env.WSEventsForRun`, `env.DecideApproval`, `env.DefaultUser` used throughout. Package path `aicolab/server/internal/testsupport` is never defined in File Structure, Preconditions, or any task. Add to Preconditions with signatures.

## Should Fix

- [ ] Seven `testutil.*` helpers used in Tasks 11–12 never specified — Tasks 11, 12 — `testutil.SeedScheduledWorkflow`, `testutil.SeedRunningWorkflowRun`, `testutil.GetRunStatus`, `testutil.SeedScheduleFire`, `testutil.SeedApproval`, `testutil.SeedUser`, `stubEngine`. Add signatures to Preconditions.

- [ ] `uuidPg`/`uuidFromPg`/`sqlNullString`/`nowUnix` helpers used across six files but never located — Tasks 6, 7, 10, 11, 12 — the plan says "mirror Phase 1/2" but never names the file. Add a one-line anchor in Preconditions pointing to the Phase 2 file.

- [ ] Handler test helpers `newWorkflowHandlerCtx`/`newApprovalHandlerCtx` undefined — Task 13 — Step 1 tests call `tc.postJSON`, `.createWorkflow`, `.triggerWorkflow`, `.createApproval` on them. Task 13 Step 2 labels the handler code "abbreviated — standard chi + service delegation" but the test helpers aren't. Add shape (or a note that they follow the Phase 2 `newHandlerTestCtx` pattern).

- [ ] `ListActiveScheduledWorkflows` has no workspace filter — Task 3 `workflow.sql` — no CROSS-tenant rationale documented (unlike `ListActiveWorkflowRuns` which is noted as startup-only). Add the same "startup scheduler scan only, never from request-scoped code" comment.

- [ ] `WorkflowBuilder` Save-button comment says "calls api.workflow.create/update" — Task 15 — direct `api.*` call inside a component violates CLAUDE.md. Route through a `useCreateWorkflow`/`useUpdateWorkflow` mutation hook; add these to Task 14's `queries.ts`.

## Nits

- Task 6 `state_test.go` has `_ = uuid.Nil` / `_ = json.RawMessage{}` as dead-import suppressors; note to remove once real assertions are wired.
- Task 11 commit message reads "skip is live here; one/all land in Task 11.1" — readers may think they're cross-phase; make it "Task 11.1 below" for clarity.
- `@xyflow/react: "^12"` catalog uses a range when the project convention pins exact versions.

## Verdict

Architecturally sound after R1+R2. Five executability blockers: three missing imports (strings, errors, pgx), the `dbPkg`/`db` alias inconsistency, the wrong-ID `IncrementStepRunRetry` call silently dropping retries, and the undefined `testsupport` package that makes Task 16 unrunnable. Five Should Fix items specify missing helpers (`testutil.*`, `uuidPg` helpers, handler-test ctx), tighten the scheduler-query scoping justification, and route the builder's save through a mutation hook. Fix the five blockers plus the five should-fix items and an agent can execute Phase 7 task-by-task with no re-reading. Ready to mark `done` once applied.
