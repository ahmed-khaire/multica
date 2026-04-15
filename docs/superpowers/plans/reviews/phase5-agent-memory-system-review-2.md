# Phase 5 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase5-agent-memory-system.md`
Source of truth: `PLAN.md` §Phase 5 (lines 719–873) + D1–D7 invariants

---

## Blocking Issues

- [ ] Composite FK on `(workspace_id, source_agent_id) REFERENCES agent(workspace_id, id)` will fail at DDL time unless `agent` has a UNIQUE constraint on `(workspace_id, id)` — Task 1 Step 1, `agent_memory_1024` table. Add `CREATE UNIQUE INDEX IF NOT EXISTS agent_ws_id_uniq ON agent(workspace_id, id)` in migration 130's preamble.

- [ ] `Remember` has no transaction boundary across the consolidation + insert/update/delete sub-pipeline — Task 5 Step 4, `memory.go`. A failure between `deleteRow` (action=`delete`) and subsequent `insertRow` leaves a permanent gap. Wrap the per-row consolidation block in a `pool.BeginTx` / `tx.Rollback` block.

- [ ] Worker pool `Shutdown` has a double-close hazard — Task 7 Step 3, `memory_pool.go`. Guard `close(p.jobs)` with `sync.Once`.

## Should Fix

- [ ] `Remember`'s background context does not carry `dims` — Task 10. State explicitly that `Embedder.Embed` returns the authoritative `dims` so the dim-dispatcher stays consistent when run off-request.

- [ ] `TestMemory_PrivateMemoryIsNotSharedAcrossAgents` uses `RememberOptions` / `RecallOptions` struct API — Task 17. Tasks 5 and 6 define positional signatures. Align test to actual signatures or introduce the options structs in Tasks 5/6.

- [ ] `MemoryCompositeWeights` and `MemoryRecallTopK` defined in `defaults.go` are never referenced — Task 6 `memory_score.go` and `Recall`. Replace literal `0.5/0.3/0.2` and `limit*3` with the named constants or the D7 constants are dead symbols.

- [ ] `LIKE agent_memory_1024 INCLUDING ALL` already copies CHECK constraints; the subsequent `DROP IF EXISTS` + `ADD CONSTRAINT ... NOT VALID` + `VALIDATE CONSTRAINT` sequence is redundant on fresh DB — Task 1 Step 1 for 1536/3072. Add clarifying comment that the DROP is idempotent insurance for re-runs.

- [ ] `compactAnthropic` Archivist semantic gap — Task 12. If only step 2 (ClearToolUses) suffices, the `middle` pre-edit slice includes verbatim tool content, but the remaining context has tool-use IDs replaced with placeholders. State explicitly that Archivist always receives the raw pre-edit slice (already captured) — document the rationale.

- [ ] No WS events on memory write/delete — Task 14. Add `memory.created` and `memory.deleted` WS events + TanStack Query invalidation per CLAUDE.md "WS events invalidate queries — they never write to stores directly."

## Nits

- `TestMemoryPool_BackpressureDropsExcess` uses `atomic.AddInt32` without importing `sync/atomic`.
- Task 15 duplicates `flagAgent` `StringVar` registration inside + after `newMemoryCmd`.
- `MemoryPool.run` increments `p.completed` after a panicking job (recover swallows). Don't count panics as completed.

## Verdict

Not ready for round 3. Two blocking correctness issues (missing unique index prerequisite, no transaction around consolidation delete/insert) and one shutdown-safety bug (double-close). Integration test signature mismatch will block compilation. Fix these, confirm the WS event decision, wire the defaults.go constants to their consumers, and then proceed to round 3.
