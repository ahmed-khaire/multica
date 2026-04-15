# Phase 2 Plan — Post-Blocker Audit

## Summary

Phase 2 plan is structurally sound (29 tasks, right granularity, D2 correctly handled by omission) but needs 12 concrete edits + 7 additions before matching the post-blocker PLAN.md. Three clusters of drift:

1. **D8 `trace_id` not threaded through any layer** — context, callbacks, CostEvent, CostSink, sqlc params, worker. Biggest single gap.
2. **BudgetService uses stale `CanExecute(ctx, wsID, agentID)` API** instead of §1.2's race-free `CanDispatch(ctx, tx, wsID, estCents)` with `pg_advisory_xact_lock`. Task 22 never calls it inside the claim tx.
3. **Fallback wrapper (Task 13) doesn't implement active-child timer composition** required by §2.1.1. No `require_ontimeout` validation. Wrapper is typed as `LanguageModel` instead of `Backend`.

## Required edits (condensed)

**E1.** Thread `trace_id` — add `server/pkg/agent/trace.go` (new file) with `traceIDKey`, `WithTraceID(ctx, uuid.UUID)`, `TraceIDFrom(ctx)`. Call `WithTraceID(ctx, uuid.New())` in Worker.claimAndExecute (Task 22) before `backend.Execute`.

**E2.** `CostEvent` struct rename/add fields — `CachedTokens` → `CacheReadTokens`; add `CacheTTLMinutes`, `CostStatus`, `TraceID uuid.UUID`, `ResponseCacheID *uuid.UUID`, `SessionHours float64`, `SessionCostCents int`.

**E3.** `LLMAPIBackend.Execute` CostSink.Record — pull `TraceID` from ctx; populate `CacheReadTokens`; set `CostStatus="actual"`.

**E4.** `dbCostSink.CreateCostEventParams` — column name `CacheReadTokens`, pass `TraceID uuidToPg(e.TraceID)`, `CostStatus e.CostStatus`, `CacheTtlMinutes`.

**E5.** `BudgetService.CanExecute` → `CanDispatch(ctx, tx pgx.Tx, wsID uuid.UUID, estCents int) (Decision, error)`. Signature and semantics per PLAN.md §1.2. Drop agent-scoped branch.

**E6.** `Worker.claimAndExecute` — wrap `ClaimTaskForProvider` in pgx.Tx; call `Budget.CanDispatch(ctx, tx, wsID, 10)` before COMMIT; rollback on `!Allow`.

**E7.** Fallback wrapper — retype `FallbackModel` → `FallbackBackend` (implements `Backend`). Add `mu sync.Mutex`, `activeIdx int`, `timer *time.Timer`. `ExpiresAt()` + `Hooks` forward to active child only. On swap: prev `BeforeStop`, advance idx, re-read `ExpiresAt`, cancel+reschedule `OnTimeout`. `NewFallbackBackend` validates mixed chains (if any entry `require_ontimeout=true`, all earlier entries must expose `OnTimeout`).

**E8.** `harness.go` doc-comment — mark Anthropic context-management primitives (`ClearToolUsesEdit`/`ClearThinkingEdit`/`CompactEdit`) as Anthropic-only; non-Anthropic providers fall back to Phase 5 summarise-truncate.

**E9.** Task 23.5 Step 3 preamble — add verification bullet that Phase 1's sqlc `CreateCostEvent` query includes all new columns (`cache_read_tokens`, `cache_ttl_minutes`, `trace_id`, `response_cache_id`, `session_hours`, `session_cost_cents`). Regenerate sqlc if needed.

**E10.** Task 13 tests — add `TestFallback_ExpiresAtForwardsToActiveChild` that verifies `ExpiresAt()` changes after a swap.

**E11.** `runtime_config.fallback_chain[n].require_ontimeout` — add field (default false) to JSON schema example. Default-false entries for Phase 2 LLM backends; Phase 11 cloud_coding will set true.

**E12.** Self-Review coverage table — add rows for D8, §1.2 CanDispatch, §2.1.1 fallback composition, Anthropic-only caveat (all marked ❌ pending edits).

## Missing tasks

1. **New micro-task:** `server/pkg/agent/trace.go` — traceIDKey + context helpers.
2. Thread trace_id into harness callbacks (Task 4 Config doc).
3. Fallback active-child timer mechanics (new Task 13 Step 3b).
4. Fallback chain validator at agent-create (`server/internal/handler/agent.go`).
5. `BudgetService.CanDispatch` call inside `Worker.claimAndExecute`.
6. Verify Phase 1 sqlc `CreateCostEvent` query includes new columns.
7. E2E verification step — assert `cost_event.trace_id IS NOT NULL` for test task.

## Obsolete tasks

1. Task 16's entire `CanExecute` design (agent-scoped branch, `errNotFound`, `GetAgentBudgetPolicy`, `SumCostByAgentMonth`).
2. `CostEvent.CachedTokens` field references everywhere.
3. `db.CreateCostEventParams.CachedTokens` — field renames to `CacheReadTokens` after Phase 1 sqlc regen.
4. Redis adapter "split into 4 interfaces" is deferred (unified adapter stays per current PLAN.md).

## Verdict

After these edits, plan is executable. Biggest gap is D8 trace_id (no threading at all). Second biggest is Task 16 BudgetService rewrite. Third is Task 13 fallback wrapper rewrite. D2 `task` tool is correctly deferred to Phase 6 (no leakage to fix).
