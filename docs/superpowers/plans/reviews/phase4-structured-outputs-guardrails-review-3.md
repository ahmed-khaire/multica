## Blocking Issues

- [ ] `stubAnthropic` intercepts the network at the E2E layer using `msw/node`, but the Go backend makes real HTTP calls to Anthropic — `msw/node` only intercepts Node.js `fetch`, not Go's `net/http`. The stub never reaches the server. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 27 — Replace with an actual HTTP server (`httptest.NewServer` in Go, or a small Express/msw Node proxy) that the backend env var `MULTICA_ANTHROPIC_BASE_URL` points to; or use Playwright's `route.fulfill` to intercept via the browser. The current snippet as written will make the E2E step silently test nothing.

- [ ] `ListGuardrailEventsForTask` in `guardrail_event.go:List` is called with only `taskId`, but the sqlc query (`ListGuardrailEventsForTask`) takes two params `(task_id, workspace_id)` — the handler never passes `wsID`. A task ID leaked across workspaces would return another tenant's guardrail events. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 21, Step 2 implementation — Add `wsID := chi.URLParam(r, "wsId")` and pass it as the second argument.

- [ ] `AttachKnowledgeToAgent` sqlc query (`knowledge.sql`) deletes by `(agent_id, knowledge_source_id)` with no `workspace_id` predicate — an agent from workspace A could detach a source belonging to workspace B if IDs collide. The handler (`Attach`) also passes only `agentID` without workspace scoping. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 2 Step 3 (`DetachKnowledgeFromAgent`) and §Task 20 Step 2 (`Attach`) — Add `AND workspace_id = $3` to the DELETE and pass the authenticated workspace to the handler call.

## Should Fix

- [ ] `ingest.go` references `fmt.Errorf` but the import block only lists `"context"` and `"github.com/pgvector/pgvector-go"`. The file won't compile. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 15, Step 2 — Add `"fmt"` to the import list in the provided snippet.

- [ ] `retriever.go` references `fmt.Errorf` but imports only `"context"`. Same compile failure as above. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 16, Step 2 — Add `"fmt"` to imports.

- [ ] Task 27's `stubEmbedder` uses `msw/node` and will hit the same Go-process interception gap described in Blocking Issue 1 above — the Go server's embed call goes to OpenAI via `net/http`, not via Node. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 27 — Same resolution as the Anthropic stub: use a real TCP server the Go env var points to.

- [ ] `handlerTestCtx` in `knowledge_test.go` has `newHandlerTestCtx` returning `panic("TODO: ...")`. This is not a self-verifiable failing test as required by TDD ordering — the step will panic, not produce a useful error. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 20, Step 1 — Either provide a minimal real implementation of `newHandlerTestCtx` (boot test DB, register routes, return a working client) or note explicitly that it must be completed as sub-steps before the TDD step can run.

- [ ] The `KnowledgeHandler.Create` implementation passes `wsID` as a string into `GetWorkspaceEmbeddingConfig` and `UpdateKnowledgeSourceStatus`, but the sqlc-generated signatures use `pgtype.UUID` / `uuid.UUID` parameters throughout the codebase (per Phase 1 convention). The snippet will not compile against the real sqlc types without `uuidFrom()` wrapping. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 20, Step 2 — Use `uuidFrom(wsID)` consistently, as already done in `store.go`'s `pickOps`.

- [ ] Task 25 ("Output Schema & Guardrails Tab UI") defers Step 2 to "use existing `MonacoEditor` wrapper from packages/ui; fall back to `textarea` in tests" with no concrete implementation snippet. The test asserts `getByLabelText(/output schema/i)` which requires an actual element; a stub-implementation step is missing. — `docs/superpowers/plans/2026-04-14-phase4-structured-outputs-guardrails.md` §Task 25, Step 2 — Add a minimal `<textarea aria-label="Output Schema" />` implementation so the failing test in Step 1 can become green.

## Nits (optional)

- `schema_test.go` (Task 7, Step 1) uses `strings.Contains` but omits `"strings"` from the import block in the snippet; a copy-paste will fail to compile.
- `executor_test.go` similarly uses `strings.Contains` without showing `"strings"` imported; will not compile verbatim.
- The `<KNOWLEDGE>` block is prepended before the system prompt (`prefix + "\n\n" + systemPrompt`), but Anthropic's prompt-caching requires the cacheable block to be a stable prefix. If the system prompt also contains dynamic content that changes per task, the prefix loses its cache benefits. Worth a note in Task 18 or Task 17.
- Task 20 Step 3 mounts `r.Post("/agents/{agentId}/knowledge", kh.Attach)` at the workspace router level, but the route defined in Step 3 is outside the `/knowledge` sub-router. Double-check the Chi nesting to ensure `wsId` is still accessible at that route.

## Verdict

The plan is strong in structure, TDD ordering, and invariant cross-referencing, and is nearly ready — but two blocking issues must be resolved before handing it to an agent: the msw/node stub approach in Task 27 fundamentally cannot intercept Go's `net/http` and will produce a silently green E2E that proves nothing; and the guardrail-event and knowledge-detach handlers expose cross-workspace data leakage bugs in the provided snippets. Fix those three (Blocking Issues 1–3), patch the missing `"fmt"` imports, and flesh out `newHandlerTestCtx` before the plan can be executed task-by-task.
