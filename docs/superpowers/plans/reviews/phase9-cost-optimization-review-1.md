# Phase 9 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase9-cost-optimization.md`
Source: `PLAN.md` §Phase 9 (~line 1766) + D1–D8 invariants

---

## Blocking Issues

- [ ] D4 violated — Redis not used for semantic cache — §9.2 — D4 explicitly names Phase 9 semantic cache as a Redis consumer. The plan implements cache entirely in pgvector + defers Redis to "post-V1" in the Scope Note. Add a RedisAdapter L1 lookup in front of pgvector (hash-keyed exact-match) OR flag explicit D4 deviation. pgvector remains the persistent store either way.

- [ ] 95% savings stack (batch + prompt cache) not mechanically specified — §9.1 + §9.5 — Intro promises 95%; Task 7 covers the 50% batch discount in isolation. No task shows how `cache_read_tokens`/`cache_write_tokens` from Phase 2's `CostCalculator` pass through `BatchResult.Usage` so both discounts apply. No test covers "batch hit on a prompt-cached prefix". Add a step in Task 7 + a verification item in Task 11.

## Should Fix

- [ ] `CostBreakdown` + `RoutingEffectiveness` types missing; Breakdown handler body empty — §9.4 / Task 9–10 — §9.4 requires model-routing effectiveness (% per tier). Plan has no type, no chart. Define `CostBreakdown{model, tier, token_count, cost_cents, pct_of_total}` + `RoutingEffectiveness` aggregate + wire Breakdown handler + dashboard card.

- [ ] Delegation chain cost visualization — named requirement with zero implementation — §9.4 — Add `GET /cost/chain?trace_id=…` endpoint grouping `cost_event` by `trace_id → agent_id` + `DelegationCostChart` frontend component.

- [ ] Credential pool usage stats — named requirement with zero implementation — §9.4 — Add `GET /cost/credential-pools` endpoint + `CredentialPoolStats` type + dashboard card backed by Phase 1 credential tables.

- [ ] `knowledge_version` invalidation missing from purge — §9.2 / Task 3 + 6 — `response_cache_*` schema has `knowledge_version` column but `DeleteStaleCache*` only checks `config_version_hash`. Add `current_knowledge_version` param to purge queries + `KnowledgeVersionOf` callback in `CachePurgeDeps`.

- [ ] Dashboard aggregation endpoints untested — §9.5 Verification item 3 — Task 11 integration covers cache round-trip but not Summary/Breakdown. Add `cost_dashboard_test.go` table-driven test asserting Summary correctly partitions `cache_hit_cents` vs `cache_miss_cents`.

## Nits

- Task 9 `Breakdown` + `CacheHits` handlers shown as `/* ... */` with "same pattern as Summary" — `Breakdown` is the most complex (multi-dimension grouping); fill in the SQL + Go shape.
- Task 10 `AgentMetric` TS type omits `tasks_timed_out`, `delegations_sent`, `delegations_received`, `chat_messages_handled` despite being in the DB schema + required by §9.3.
- Task 8 `RunDaily` body is a comment ("trivial SQL aggregation") — add a `RollupDailyMetrics :one` query stub + minimal Go body.

## Verdict

Cache schema, batch executor, metrics worker, and SLA webhook are structurally solid — every named component maps to a concrete task. Two blockers prevent execution: D4's Redis baseline for semantic cache is bypassed without justification, and the 95%-savings composition (batch + prompt cache) is claimed but not mechanically tied through the Batch API task. Three dashboard features from §9.4 (routing effectiveness, delegation-chain viz, credential pool stats) are named but receive zero implementation detail; knowledge_version invalidation exists in schema but is not wired through the purge worker. Fix the two blockers + five should-fix items before Round 2.
