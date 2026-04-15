# Phase 5 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase5-agent-memory-system.md`

## Blocking Issues

- [ ] `findSimilarTx / insertRowTx / updateRowTx / deleteRowTx` have no implementation block — Task 5. Add the four Tx wrappers with `q := db.New(tx)` bodies.
- [ ] `Embedder.Embed` signature `(vecs, dims, err)` is never defined in plan — no task creates `server/pkg/embeddings`. Add a pre-condition block declaring the interface.
- [ ] `service.ErrMemoryNotFound` referenced in Task 14 but never declared. Add `var ErrMemoryNotFound = errors.New(...)` in Task 3 or 14.
- [ ] `WSPublisher` type referenced in `MemoryHandler` but never defined. Add an interface declaration.
- [ ] `DB` and `Logger` handler-struct fields use unresolved type names. Specify concrete types (`*db.Queries`, `*slog.Logger`).
- [ ] `newIntegrationHarness`, `h.workspaceID`, `h.insertPrivateMemory`, `h.fakeVec(1536)` used in Task 17 never defined. Add an `IntegrationHarness` helper step.
- [ ] `api.memory` does not exist. Task 16 calls `api.memory.search/list/stats/delete`. Add a step creating `packages/core/api/memory.ts`.

## Should Fix

- [ ] Task 16 frontend test uses `api` prop; component uses `useAgentMemories` hook. Reconcile: mock `@multica/core/memory/queries` with `vi.hoisted()` in the test.
- [ ] `stubEmbedder` import from `e2e/helpers/` is asserted but the helper's existence isn't confirmed. Add Step 0 to check-and-author `stub-embedder.ts` if absent.
- [ ] `newTestMemoryService` and option helpers sketched only. Add the full builder.
- [ ] Task 11 Step 0 constants are consumed by Task 6 Step 3 — state explicit ordering note or move the `defaults.go` append ahead of Task 6.
- [ ] `agent.LanguageModel` / `agent.GenerateOptions` / `res.Text` shapes not declared. Add a pre-condition block at the top describing the Phase 2 contract.

## Nits

- sqlc output path may be `server/pkg/db/generated/`, not `server/pkg/db/db/`.
- Task 8 `builtin` → `service` import may cross an internal-package boundary.
- Task 15 references `newFakeServer` + `withRecallResponse` without definitions.

## Verdict

Not agent-executable as written. Seven concrete gaps (Tx dispatchers, Embedder interface, ErrMemoryNotFound, WSPublisher, DB/Logger types, IntegrationHarness, api.memory client) will block compilation or test execution. Fix all blocking items before marking Phase 5 done.
