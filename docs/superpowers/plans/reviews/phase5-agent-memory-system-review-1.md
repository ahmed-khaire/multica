# Phase 5 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase5-agent-memory-system.md`
Source of truth: `PLAN.md` §Phase 5 (located by H2 heading — current line range ~912–1099) + D1–D7 invariants (around lines 48–96)

---

## Blocking Issues

- [ ] `CountMemoriesByAgent*` / `GroupCategoriesByAgent*` sqlc queries are never defined — Task 2 Step 1 (memory.sql) — The `GET /memories/stats` handler and `MemoryService.Stats` both require these queries, but they are absent from the `memory.sql` block. Task 14 Step 3 says "add a new `CountMemoriesByAgent*` / `GroupCategoriesByAgent*` pair" without supplying the SQL. An implementer cannot proceed with Task 14 Step 3 without guessing the correct aggregate shape. Add the three dim-partitioned `CountMemoriesByAgent*` / `GroupCategoriesByAgent*` queries to Task 2 Step 1.

- [ ] `agent_ws_id_uniq` index creation is redundant with an existing Phase 1 migration — Task 1 Step 1 (up migration) — PLAN.md §1 (around line 168) already creates `CREATE UNIQUE INDEX idx_agent_workspace_id ON agent(workspace_id, id)`. The plan adds a second index named `agent_ws_id_uniq` on the same columns. At runtime this succeeds (IF NOT EXISTS isn't used on it), but it doubles the index maintenance cost and signals the migration was written without checking Phase 1 output. Either use IF NOT EXISTS and reference the Phase 1 index by its known name, or drop the Phase 5 creation entirely and add a comment asserting the Phase 1 dependency.

- [ ] `MemoryService.Stats` method is mentioned but never specified — Task 14 Step 3 — The router mounts `r.Get("/stats", mh.Stats)` and the frontend `MemoryApi` type expects `stats(): Promise<MemoryStats>`, but no implementation snippet for `MemoryHandler.Stats` or `MemoryService.Stats` is provided anywhere in the plan. This is more than a "fill in mechanical work" gap: the aggregate must union three partition tables and map scope prefixes to tier labels (core/working/archival), which is non-trivial. Add an implementation sketch and the corresponding sqlc queries in Task 14.

## Should Fix

- [ ] `stubLLM.DoGenerate` signature in Task 4 does not match the declared `agent.LanguageModel` interface — Task 4 Step 1 — The stub's method is typed as `func (s *stubLLM) DoGenerate(ctx context.Context, _ *struct{}) (struct{ Text string }, error)`, but the real `extractAtomicMemories` and `analyzeMemory` implementations (Task 4 Step 3) call `llm.DoGenerate(ctx, &agent.GenerateOptions{...})`. The stub's parameter type is `*struct{}`, not `*agent.GenerateOptions`. The test will not compile as written. Fix the stub signature to match `*agent.GenerateOptions` (or define the interface narrowly enough so both sides agree).

- [ ] Recall's `viewerAgentID` privacy filter is a `pgtype.UUID` null-handling issue — Task 6 Step 4 — The SQL queries (Task 2) filter `AND (NOT private OR source_agent_id = $4)` where `$4` is the `viewerAgentID`. When `RecallViewer` is not supplied, `o.viewerAgentID` is `uuid.Nil`. `uuid.Nil = 00000000-0000-...` is a valid non-NULL UUID; the SQL will match `source_agent_id = '00000000-...'` rather than returning no private rows. The plan relies on this working correctly for the privacy invariant tested in Task 17, but never explains or documents the nil-UUID semantics. Either change the query to `AND (NOT private OR ($4 IS NULL OR source_agent_id = $4))` (requires a `pgtype.UUID` nullable param) or document the nil-UUID sentinel explicitly with a note that `uuid.Nil` can never be a real agent ID.

- [ ] `MemoryTab` component violates the CLAUDE.md rule against calling API functions directly from `useEffect` — Task 16 Step 3 — The component stores `api.list()` result in local `useState` instead of using TanStack Query. CLAUDE.md states "TanStack Query owns all server state" and "Never duplicate server data into Zustand" (by extension, local state). The `MemoryTab` should use `useQuery` for list/stats and `useMutation` for delete, consistent with every other data-fetching component in `packages/views/`. The current implementation also recreates the query whenever the `api` prop reference changes (a stable-reference trap flagged in CLAUDE.md). Replace `useState`+`useEffect` with `useQuery`/`useMutation`.

- [ ] No `memory_mode` → `MemoryStats.by_tier` mapping is specified for the `stats` endpoint — Task 14 Step 3 — The three-tier (core/working/archival) classification lives in the scope path prefix, not in a column. The `MemoryStats.by_tier` aggregate must therefore do a `LIKE` count per tier prefix. No SQL or Go logic for this decomposition is described. Without it an implementer will either return zeros or invent their own classification, diverging from the scope-path convention defined in the Scope Note.

## Nits (optional)

- Task 5 Step 5 names the new file `memory_dim.go` but the commit in Step 7 adds `server/internal/service/memory_dim.go` — inconsistency is minor but the file is referenced nowhere else in the plan; confirm this file name is intentional.
- Task 11 Step 4's test comment "(If Task 13 isn't complete yet, the above-threshold test will fail…)" is fine for guidance, but the failing-test-first discipline will leave `TestCompact_PreservesProtectedRanges` in a permanently broken state until Task 13 lands — consider splitting the test into two tasks or annotating it with `t.Skip` until Task 13's commit.
- `UpdateMemoryMode` is named `SetMemoryMode` in the sqlc block (Task 2 Step 1) but the file structure table calls it `UpdateMemoryMode` — align the names.

## Verdict

The plan is substantively complete for Rounds 2 and 3 but has three blocking gaps that must be resolved before an agent can execute it end-to-end without re-reading PLAN.md or inventing implementation: the missing `CountMemoriesByAgent*`/`GroupCategoriesByAgent*` sqlc queries, the unspecified `MemoryService.Stats` / `MemoryHandler.Stats` implementation, and the duplicate Phase 1 unique index that will cause silent schema drift. The two "Should Fix" issues (stub type mismatch and the nil-UUID privacy filter) will cause compile or test failures that block forward progress. The `useEffect`-instead-of-TanStack-Query issue is a direct CLAUDE.md violation that will require rework before merge. Address these five items and the plan can move to Round 2.
