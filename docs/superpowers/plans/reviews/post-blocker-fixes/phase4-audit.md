# Phase 4 Plan — Post-Blocker Audit

## Summary

Major surgery required. Half the plan is salvageable (guardrails, structured-output adapter). The entire Knowledge/RAG substrate (Task 2, 15, 16, 19, 20) was written against the obsolete single-table schema and must be rewritten against dim-partitioned tables with composite FKs.

## Critical blocking edits (C1–C7)

**C1.** Task 2 migration 105 — rewrite entire body: `knowledge_source` (with `idx_knowledge_source_ws_id` UNIQUE index); three dim-partitioned chunk tables `knowledge_chunk_1024/1536/3072` each with typed `vector(N)`, `workspace_id NOT NULL`, composite FK `(workspace_id, source_id) → knowledge_source(workspace_id, id)`; ivfflat indexes for 1024/1536, hnsw for 3072; `agent_knowledge` with composite FKs on BOTH `(workspace_id, agent_id)` and `(workspace_id, knowledge_source_id)`. `IF NOT EXISTS` everywhere.

**C2.** Task 2 sqlc queries — one `SearchAgentKnowledge` is impossible. sqlc compiles one function per statement, so split into three per-dim queries: `InsertChunk{1024,1536,3072}`, `DeleteChunks{1024,1536,3072}ForSource`, `SearchAgentKnowledge{1024,1536,3072}`. JOIN uses `ak.workspace_id = kc.workspace_id`.

**C3.** Task 15 `Embedder` interface — change to `Embed(ctx, texts) ([][]float32, dims int, err error)` (dims returned for fail-fast). `PgxStore.ReplaceChunks(ctx, sourceID, wsID, dims, args)` dispatches on dim via `pickOps(q, dims)` returning the right (delete, insert) pair.

**C4.** Task 16 `SearchBackend.Search(ctx, agentID, wsID, queryEmbedding, dims, k) ([]Chunk, error)` — add `dims` param. `Retriever.Retrieve` calls `emb.Embed` to discover dims and forwards to backend.

**C5.** Task 19 resolver — load `workspace_embedding_config` per agent; pass `EmbeddingDims` into `LLMAPIBackend.Deps`; reject when knowledge_source_ids non-empty AND no embedding config.

**C6.** Task 20 handler — replace handler-level `h.embedder` singleton with per-workspace `h.embedders.For(ctx, wsID, cfg)` factory (different workspaces may use different providers/dims).

**C7.** D8 trace_id on embedding cost_events. Currently missing. Tasks 15 (ingest), 10 (judge), 20 (handler) all need `trace_id` propagation. Ingest goroutine mints its own trace_id since HTTP context is cancelled on return.

## Important edits (I1–I9)

**I1.** Architecture blurb mentions only ivfflat — add hnsw for 3072.
**I2.** File Structure table row for migration 105 must describe dim-partitioned tables.
**I3.** External Infrastructure table must mention both ivfflat + hnsw; CI smoke test against pgvector/pg17.
**I4.** Task 1 migration 104 — `ADD COLUMN IF NOT EXISTS` on output_schema/structured_result. `guardrail_event`: `CREATE TABLE IF NOT EXISTS`, composite FKs `(workspace_id, agent_id)` and `(workspace_id, task_id)`, add `trace_id UUID NOT NULL`.
**I5.** Task 9 port cite — correct as-is.
**I6.** Task 13 — document that `trace_id` is read from `ctx` by the store.
**I7.** Task 18 retry loop tests — assert per-attempt cost_event emission tagged with task's trace_id.
**I8.** Task 27 E2E — add `stubEmbedder` to intercept `POST https://api.openai.com/v1/embeddings` returning 1536-dim zero vectors.
**I9.** Self-Review D4 note — add sentence about new per-claim `GetWorkspaceEmbeddingConfig` call.

## Missing tasks (M1–M4)

**M1. Task 14.5** (before Task 15) — `server/pkg/embeddings/routing.go` with generic `DimPartitionRepo[T]` dispatcher. Reused by Phase 5/9.
**M2.** Task 15 test for dim-mismatch (`expected=1536` but embedder returns 1024 → reject pre-insert).
**M3. Task 2.5** — CI smoke test that all three chunk tables and their indexes come up cleanly on `pgvector/pgvector:pg17`.
**M4.** Task 2 test — composite FK on `agent_knowledge` rejects cross-workspace rows.

## Obsolete content

**O1.** Untyped `embedding vector` comment in Task 2.
**O2.** Single `CREATE INDEX ... USING ivfflat` on `knowledge_chunk`.
**O3.** Single `SearchAgentKnowledge` sqlc query.
**O4.** `InsertKnowledgeChunk` references in `PgxStore.ReplaceChunks`.

## Verdict

Task 2, 15, 16, 19, 20 require substantive rewrites. Tasks 1, 13 need smaller additions (composite FKs + trace_id + idempotency). Tasks 3, 4, 5, 6–12, 18 (guardrails + structured output) survive mostly unchanged. Three new helper tasks (Task 2.5, 14.5) and tests (M2, M4). After edits, plan is executable.
