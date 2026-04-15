## Blocking Issues

- [ ] Interface mismatch on `Embedder` — `ingest.go` (Task 15, Step 2, line 1830) defines `Embedder.Embed` as returning `(vectors [][]float32, dims int, err error)`, but `ingest_test.go` (Task 15, Step 1, line 1765) declares `fakeEmbedder.Embed` returning `([][]float32, error)` with a separate `Dimensions() int` method. The production interface and the test fake are mutually incompatible — the test cannot satisfy the interface and will not compile. The plan must resolve to one signature (returning dims from `Embed`, or adding `Dimensions()` to the interface) and be consistent everywhere.

- [ ] `Retriever.Search` signature mismatch — `retriever.go` (Task 16, Step 2, line 2033) defines `SearchBackend.Search` as `(ctx, agentID, workspaceID string, queryEmbedding []float32, dims, k int)`, but `retriever_test.go` (Task 16, Step 1, line 1997) declares `fakeSearch.Search` as `(ctx, agentID, workspaceID string, queryEmbedding []float32, k int)` — `dims` is missing. The fake cannot satisfy the interface, so the test fails to compile.

- [ ] `Ingest` signature drift between test and implementation — `ingest_test.go` (Task 15, Step 1) calls `Ingest(ctx, src, fakeEmbedder{dim:1536}, store)` with 4 arguments; the implementation at Task 15 Step 2 declares `Ingest(ctx, src, emb, expectedDims int, store)` with 5 arguments (the `expectedDims` parameter is injected by the caller). The test passes no `expectedDims`, so it will not compile.

- [ ] `guardrail_event` composite FK is missing for `task_id` — `guardrail_event` references `agent_task_queue(workspace_id, id)` (Task 1, Step 1), but there is no `CREATE UNIQUE INDEX` on `agent_task_queue(workspace_id, id)` that is guaranteed to exist before migration 104 runs. The plan adds `idx_agent_task_queue_ws_id` in the same migration as a "belt-and-suspenders", but this does not resolve the ordering issue: `CREATE UNIQUE INDEX IF NOT EXISTS` on an existing table cannot be created inside the same DDL block that also defines the FK that depends on it in some PostgreSQL versions. The plan should note that the index must be committed before the FK constraint is checked.

- [ ] `SetTaskOutputSchema` and `WriteStructuredResult` sqlc queries (Task 1, Step 3) do not include `workspace_id` in the `WHERE` clause. Every query that targets `agent_task_queue` by `id` alone bypasses the multi-tenancy filter required by PLAN.md ("all queries filter by workspace_id"). Without `AND workspace_id = $2` a compromised task ID from workspace A can overwrite workspace B's result.

## Should Fix

- [ ] `TestExecutor_AccumulateRetryCritiques` and `TestExecutor_StopOnReject` reference `fake` from `guardrail_test.go` — but `fake` is defined in that file's `package guardrails` with an unexported `critique` field, while `executor_test.go` tries to set `fake{action: ActionRetry, critique: "A"}` (Task 13, Step 1). The struct literal initialisation works only if `critique` is exported or the two files are in the same package and build together. The plan lists both files as `package guardrails`, so this compiles, but the `critique` field on `fake` in `guardrail_test.go` is named `critique` (lowercase). That is consistent, so this is actually fine — remove this note. (Self-correcting: not an issue.)

- [ ] `ListKnowledgeSourcesForAgent` (Task 2, Step 3) does not include `workspace_id` in the `WHERE` clause (`WHERE ak.agent_id = $1`). A cross-workspace agent ID collision could return sources from the wrong workspace. Add `AND ks.workspace_id = $2`.

- [ ] `KnowledgeHandler.Create` launches a bare `go func()` with `context.Background()` (Task 20, Step 2). When the server shuts down mid-ingest the goroutine is leaked and the DB connection pool is held open. The plan should wire the handler's lifetime context or use a worker pool. PLAN.md §1.7 does not permit unbounded goroutine leaks in handlers.

- [ ] `TestCreateKnowledgeSource_EndToEnd` calls `tc.waitForChunks(resp.ID, 1)` (Task 20, Step 1) but `waitForChunks` is not defined in the plan or in any existing test helper referenced elsewhere. This is an unresolved dependency that will prevent the test from compiling without further work the plan doesn't specify.

- [ ] E2E spec imports `stubAnthropic` from `"./helpers"` (Task 27, Step 1) but the E2E helpers directory is `e2e/helpers/`, not a sibling of the spec. The E2E spec is at `e2e/tests/`, so the import should be `"../helpers"`. Also, `stubAnthropic` is described as "author as part of this task" with no implementation snippet — an implementing agent cannot write it without knowing the exact `msw/node` API surface and how `MULTICA_ANTHROPIC_BASE_URL` is injected into the worker process. A minimal stub implementation or explicit file-path stub should be included.

## Nits

- Task 3, `validator.go`: `containsPath` and `flatten` are defined inside `validator.go` per the snippet but the test calls them directly. They would need to be in the same package — which they are — but they are placed in the main implementation file rather than test helpers. Minor: no functional impact but the plan should clarify file placement.
- Task 18 doesn't include a failing-test run command before Step 2 (implementation), unlike most other tasks. Breaks the TDD ordering the plan claims to follow.
- `BuildPrefix` uses a variable named `cap` (Task 17, Step 2) which shadows the builtin `cap` function in Go. Rename to `maxChars`.
- Task 26's `GuardrailEventsPanel` passes `wsId` to `useGuardrailEvents(wsId, taskId)` but `ListGuardrailEventsForTask` SQL query filters only by `task_id` — the `wsId` argument serves no backend purpose. Either the query should add `AND workspace_id = $2` (correct for multi-tenancy) or the frontend hook should drop the `wsId` parameter. Currently inconsistent.

## Verdict

The plan is not ready to advance to Round 3. Three compile-time interface mismatches (`Embedder`, `SearchBackend`, and `Ingest` parameter count) are blocking — an agent following the plan task-by-task would hit irrecoverable build failures at Tasks 15 and 16. The multi-tenancy gap on `WriteStructuredResult`/`SetTaskOutputSchema` is a security correctness issue that mirrors exactly the category of bug PLAN.md calls out ("all queries filter by workspace_id"). These must be resolved before executability can be meaningfully assessed in Round 3.
