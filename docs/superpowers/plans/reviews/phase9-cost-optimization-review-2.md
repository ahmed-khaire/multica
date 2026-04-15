# Phase 9 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase9-cost-optimization.md`
Source: `PLAN.md` §Phase 9 + D1–D8
Round 1 applied: 2 blocking + 5 should-fix.

---

## Blocking Issues

- [ ] L1 cache poisoning on embedding failure — Task 4 `Insert` — L1 SetEx runs BEFORE `Embed` is called. If embedding fails, L1 holds a `CacheEntry` with zero UUID + no L2 row. Subsequent Lookup returns the poisoned L1 entry; `emitCacheHitCostEvent` writes `cost_event.response_cache_id = 00000000-...`, `BumpHit` updates nothing. Move L1 SetEx AFTER successful `InsertCache{dim}` + include the returned L2 row ID in the CacheEntry JSON.

- [ ] `completeBatch` has no transaction — Task 7 — `CompleteTask` + `InsertCostEvent` are two separate `db.New(pool)` calls. `InsertCostEvent` failing after `CompleteTask` succeeds leaves the task marked complete with no cost record. Wrap in `pool.BeginTx` / `pgx.BeginTxFunc`; on rollback the task stays pending for retry.

- [ ] Batch goroutines lost on server restart — Task 7 `pollAndComplete` — Fire-and-forget `go e.pollAndComplete(...)`. Restart kills the goroutine; tasks stuck in `pending` forever. Persist submitted batches to a `batch_job` table (`batch_id`, `provider`, `status`, `task_ids`) before launching the goroutine; add startup reconciler resuming `status='submitted'` rows.

- [ ] `ListAllAgents`/`ListAgentsWithSLA` unscoped; FK race on agent deletion — Task 3/8 — The metrics worker iterates agents globally; a mid-run deletion races the `UpsertAgentMetric` FK. Document global scoping intentionally (matches scheduler pattern) AND catch the per-agent FK violation so it doesn't abort the tick.

## Should Fix

- [ ] L1 rollback reactivation — Task 4 / Phase 8B hook — Rolling forward to a new config_hash misses old L1 keys (safe). Rolling back to an old hash can reactivate stale L1 entries that haven't expired. Either shorten L1 TTL to << daily-purge interval (e.g. 1h) or add an explicit Redis DEL on the affected key prefix in Phase 8B rollback.

- [ ] `CostSummary.cache_hit_rate` semantic ambiguity — Task 9 — Frontend consumers expect event-weighted (intuitive). Plan computes cost-weighted (0 when hits are free). Rename to `cost_weighted_cache_savings_rate` and keep event-weighted on `/cost/cache-hits` + `CacheHitRow`.

- [ ] Batch tasks re-submitted on each RunOnce tick — Task 7 — `ListPendingBatchTasks` re-selects the same rows; second tick creates a duplicate batch. Add `ClaimBatchTasks` updating `status='batch_submitted'` inside `FOR UPDATE SKIP LOCKED` SELECT before `prov.Submit`.

## Nits

- `rowToEntry1024` casts `row.Similarity.(float64)` — bare assertion panics if sqlc generates `pgtype.Float8`. Use safe assertion + error.
- `hitRate = cache_hits / (completed+failed+timed_out)` conflates LLM calls with tasks — multi-turn agents produce >1 call per task. Comment acknowledging approximation.
- `MetricsWorker.Run` tests `hourEnding.Hour() == 0` for daily rollup — misses it if the worker starts at 00:01 UTC. Add startup catch-up.

## Verdict

Architecturally sound but four correctness defects affect production data: L1 cache poisoning when embedding fails, missing transaction in `completeBatch`, lost batch goroutines on restart, and unscoped agent queries with FK races in the metrics worker. All four need implementation changes before execution. Rename `cache_hit_rate` semantic ambiguity + add `ClaimBatchTasks` to avoid duplicate submissions. Fix the four blockers + three should-fix items before Round 3.
