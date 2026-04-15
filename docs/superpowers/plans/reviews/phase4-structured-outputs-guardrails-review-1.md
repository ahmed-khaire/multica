# Phase 4 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md`
Source of truth: `PLAN.md` §Phase 4 (lines 643–718) + D1–D7 invariants (lines 37–79)

---

## Blocking Issues

- [ ] `knowledge_source` table is missing a `status` column — Task 20 implementation — The `Create` handler calls `h.db.SetKnowledgeSourceStatus(ctx, src.ID, "ready"/"error", msg)` after async ingest, but migration 105 (`knowledge_source` DDL in Task 2) has no `status TEXT` or `ingest_error TEXT` column, and no sqlc query for `SetKnowledgeSourceStatus`. At runtime the handler would call a non-existent DB method. Add `status TEXT NOT NULL DEFAULT 'pending'` + `ingest_error TEXT` to the migration and a `UpdateKnowledgeSourceStatus` sqlc query.

- [ ] `PgxStore.DeleteChunks` and `InsertChunks` are called as two separate transactions — Task 15 Step 3 (`store.go`) — PLAN.md §4.3 specifies "chunks embedded in batches → insert", and the plan's own ingest test asserts "rollback on partial embed failure." But the `Store` interface splits delete and insert into two independent `BeginTx`/`Commit` calls; a crash between them leaves the source with zero chunks. Fix: either accept a `pgx.Tx` parameter and let the caller wrap both operations, or merge `DeleteChunks`+`InsertChunks` into a single `ReplaceChunks(ctx, sourceID, []InsertChunkArgs)` that opens one transaction.

- [ ] `KnowledgeTab` passes `attached={agent.runtime_config.knowledge_source_ids ?? []}` which is a `string[]` of IDs, but the component types `Attached` as `{ id: string; name: string }` — Task 24 Step 3 mount — The mount snippet passes raw IDs where the component expects resolved objects. The prop shape or the data source must be reconciled (resolve sources before passing, or change the prop type to `string[]` and resolve inside the component via `useKnowledgeSources`).

## Should Fix

- [ ] Phase 1 dependency on `workspace_embedding_config` is not verified at agent create/update — Task 22 — PLAN.md §4.3 says dimension is "keyed to the workspace's `workspace_embedding_config`" (Phase 1.5). The validation block in Task 22 checks guardrail config and knowledge scope, but never guards "does this workspace have an embedding config?" before accepting `knowledge_source_ids`. Add a check that returns 400 if no embedding config is set and knowledge sources are referenced.

- [ ] `guardrail_event` table has no `workspace_id` foreign key to `workspace` — Task 1 Step 1 — The `workspace_id` column stores a UUID but there is no `REFERENCES workspace(id)` constraint, unlike every other workspace-scoped table in the codebase. This breaks the multi-tenancy guarantee from CLAUDE.md. Add the FK.

- [ ] D7 violation: new constants block uses the bare package name `agentdefaults` in Task 18 (`agentdefaults.GuardrailMaxRetries`) but `defaults.go` lives in `server/pkg/agent` — Tasks 18 and 19 — Task 19's `llm_api_knowledge.go` imports `"aicolab/server/pkg/agent/defaults"` (a sub-package that doesn't exist per D7; the file is `server/pkg/agent/defaults.go` in the `agent` package). Task 18 references `agentdefaults.GuardrailMaxRetries`. Standardise to a single package path consistent with how Phase 1 created `defaults.go`.

- [ ] `guardrails.Input.SystemPromptContext` is populated in the retry loop inside `llm_api.go` as `prefix + "\n\n" + b.systemPrompt(agent)` but the `PromptInjectionGuardrail` is designed to scan the system prompt for injected content from **user-supplied knowledge** — it should run before each attempt, not just after. The plan's own §4.2 Hermes port note says "scan agent context files" — wording implies pre-execution, not post. Clarify in Task 18 that `prompt_injection` guardrail should execute before the harness call (or document the deliberate post-execution choice).

- [ ] `SearchAgentKnowledge` sqlc query has no `workspace_id` filter — Task 2 Step 3 — the query joins `knowledge_chunk → agent_knowledge` by `agent_id` only. If two workspaces share an agent UUID collision (or a bug attaches a source from another workspace), cross-workspace chunks can leak. Add `AND kc.workspace_id = $4` and pass `workspaceID` from the retriever.

## Nits

- Task 15 `fakeEmbedder` in `ingest_test.go` references `errors.New` without an `"errors"` import in the snippet. Easy fix but will break compile.
- Task 24 `KnowledgeTab` has `"use client"` directive — this is a `packages/views/` component where `next/*` imports are banned. `"use client"` is a Next.js compiler directive and should be removed; the consuming app page adds it.
- Task 27 references a `stubAnthropic` helper that does not exist in the E2E fixture set. The plan should note this helper must be authored as part of the task, or the step will fail silently.

## Verdict

The plan covers §4.1–§4.4 faithfully and maintains the dependency order (Phase 1 Embedder and cost tracking, Phase 2 harness/LLMAPIBackend). There is no Phase 5 scope drift. The three blocking issues — missing `status` column on `knowledge_source` (handler calls a non-existent DB method on every upload), non-atomic chunk replacement (violates the test assertion the plan itself writes), and `KnowledgeTab` type mismatch (string IDs passed where objects are required) — will each cause a hard failure at runtime or CI. These must be resolved before moving to round 2.
