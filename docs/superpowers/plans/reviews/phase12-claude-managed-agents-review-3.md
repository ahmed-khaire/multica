# Phase 12 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase12-claude-managed-agents.md`
Source: `PLAN.md` §Phase 12 (lines 2227–2325) + D1–D8
R1 applied: 3+3. R2 applied: 3+4+3.

---

## Blocking Issues

- [ ] `env.SeedWorkspace`, `env.SeedIssue`, `env.TaskStatus` called in Task 9/12 test bodies but never declared in Preconditions — Preconditions test-helper block — agent hits compile errors on first `go test`. Fix: add their signatures to the testsupport block.

- [ ] FakeAnthropicServer struct declared in Preconditions is incompatible with Task 8 implementation — Preconditions vs Task 8 — the Preconditions version declares callable-field hooks (`CreateAgent func(...)`, etc.) that the Task 8 version doesn't have; the Task 8 struct has `scripts`, `calls`, `lastSendReq` instead. Fix: delete the duplicate stub from Preconditions (leave just a forward reference to Task 8) or replace the Preconditions struct with the actual Task 8 fields.

- [ ] `uuidFromPg`, `uuidPg`, `sqlNullString`, `sqlNullDec`, `sqlNullInt32` used throughout Task 9 worker but declared nowhere — Task 9 — presumed Phase 2 helpers but no precondition entry or file path. Fix: add a Preconditions line naming `server/internal/service/uuid_helpers.go` (Phase 2 Task 19.5) as the source + list the five signatures.

## Should Fix

- [ ] `tools.Dispatch(ctx, name, input)` in Task 11 — undeclared Phase 3 symbol. Fix: use the actual Phase 3 registry call (`toolRegistry.Dispatch`) with its concrete import path, or document the Phase 3 export.

- [ ] `anthropicToolSchema = anthropic.ToolSchema` type alias referenced before `ToolSchema` is appended to `anthropic/types.go` — Task 7 Step 2 — top-to-bottom file authorship will compile-error. Fix: reorder so `types.go` append comes before `claude_managed.go` implementation, or add an explicit ordering note.

- [ ] `pgx.BeginFunc` no longer exported by `jackc/pgx/v5` — Task 9 worker RunOnce — compile error. Fix: replace with the `pgx/v5`-idiomatic pattern — `tx, err := w.deps.DB.Begin(ctx); defer tx.Rollback(ctx); …; tx.Commit(ctx)`. (Note: `pgx.BeginFunc` IS still available in `pgx/v5@v5.5+` as a free function; double-check target version.)

- [ ] Task 9 Step 3 claim.go snippet too terse — shows only the new case without variable declaration context — `claim.go` — Fix: expand the snippet to show the function signature + where `claudeManagedWorker` comes from (e.g. passed in via Deps struct).

## Nits

- Task 6 Step 2's `import "math"` may duplicate an existing import. Add a one-line note: "only add if not already present."
- Task 10 smoke test `getByLabelText(/Model/i)` requires `htmlFor`/`id` or `aria-labelledby` wiring that Base UI `Select` doesn't provide by default — change to `getByText(/Model/i)` or add explicit `aria-label="Model"` to the SelectTrigger.
- Task 8's `t.Cleanup(f.Server.Close)` + Task 7 test's `defer fake.Server.Close()` is redundant but harmless — a one-line comment would avoid confusion.

## Verdict

Three blockers (missing seed helpers, FakeAnthropicServer struct mismatch, undeclared uuid/sql helpers) + `pgx.BeginFunc` version concern cause immediate compile failures when an executor follows the plan sequentially. All four are paste-level edits. Once applied the plan is ready for task-by-task execution; mark `done`.
