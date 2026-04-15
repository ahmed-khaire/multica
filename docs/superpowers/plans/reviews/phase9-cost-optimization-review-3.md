# Phase 9 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase9-cost-optimization.md`
Source: `PLAN.md` §Phase 9 + D1–D8
R1 applied: 2+5. R2 applied: 4+3.

---

## Blocking Issues

- [ ] cache.go imports missing `crypto/sha256`, `encoding/hex`, `encoding/json`, `pgtype`, `redis` — Task 4 — l1Key uses sha256+hex, Lookup/populateL1 use json, Insert/rowToEntry uses pgtype.Timestamptz/Float8/UUID. None in the import block.
- [ ] `sqlNullDec` undefined — Task 8 — Used for CacheHitRate; define `sqlNullDec(v float64) pgtype.Numeric` OR inline.
- [ ] `workspaceDimsFor` concrete import path missing — Task 6 — Preconditions say "Phase 1 routing.go" but no concrete function name. Spell out `embeddings.WorkspaceDimsFor(ctx, pool, wsID) int` OR inline a `GetWorkspaceEmbeddingConfig` lookup.
- [ ] `NewResponseCache` signature mismatch — Task 4 — Tests call `(db, embedder)`, impl takes `(pool, em, redis)`. Add `nil` for redis in tests or change constructor to take optional redis.
- [ ] `computeCostCents` undefined on BatchExecutor — Task 7 — Called on line 1425. Add `CostCalc` field to BatchDeps + method delegating.

## Should Fix

- [ ] `SeedCostEvent` signature conflict — Preconditions declare plural `SeedCostEvents(wsID, agentID, count, cents)` but Task 9 test calls singular `SeedCostEvent(db, wsID, agentID, cents, status)`. Reconcile to a single signature.
- [ ] sqlc generates `TaskIds []pgtype.UUID` for uuid[]; `loadTasksByID` takes `[]uuid.UUID`. Add `pgUUIDsToUUIDs` helper or take pgtype.
- [ ] `CostService`/`CostDeps`/`NewCostService` never defined; `Breakdown`/`CacheHits`/`ChainCost`/`CredentialPools` methods have no bodies — sketch minimal struct + constructor + empty methods.
- [ ] `SumCostInRange` sqlc query deferred with "reuse existing" — Phase 1 has no such query. Inline the `SUM FILTER WHERE cost_status='included' / 'actual'` query.
- [ ] Hourly aggregation never fills `DelegationsSent`/`ChatMessagesHandled`; daily rollup fills them from hourly zero-values. Add `AggregateDelegations` + `AggregateChatMessages` queries or document hourly rows intentionally leave them at 0 until daily rollup.

## Nits

- `redis.Adapter` import missing from cache.go imports (see blocker 1 fix).
- `UpsertAgentMetric` struct in RunHourly has quirky indentation — cosmetic.
- `ListAgentsWithSLA` defined in metric.sql but unused — RunHourly iterates via `ListAllAgents`. Either remove ListAgentsWithSLA or switch RunHourly to use it.

## Verdict

Structurally sound, TDD-decomposed, correctly wires D1–D8. Five executability blockers prevent `go build`: missing cache.go imports, undefined `sqlNullDec`, undefined `workspaceDimsFor` path, `NewResponseCache` test/impl signature mismatch, undefined `computeCostCents`. Three should-fix items reconcile remaining test/helper signature conflicts. Fix the eight items + nits and Phase 9 is executable. Mark `done` after fixes land.
