# Phase 8A Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8a-platform-integration.md`
Source of truth: `PLAN.md` §Phase 8A + D1–D8 invariants
Prior rounds: R1 (3+4 applied), R2 (4+4 applied — switched chat to Redis Streams, bounded inbox, etc.).

---

## Blocking Issues

- [ ] `redis.Adapter` Streams methods never declared — Tasks 6, 7 — Round-2's switch to Streams calls `XAdd`, `XGroupCreate`, `XReadGroup`, `XAck`, `XAutoClaim`, `Keys` — none of these are listed in the Preconditions' `RedisAdapter` entry. Agents starting Task 6 fail at compile. Add method signatures explicitly to Preconditions + a Pre-Task 0 extension if the Phase 2 adapter doesn't already expose them.

- [ ] `maybeCompact` signature mismatches call site — Task 7 — Definition: `maybeCompact(ctx, history, model, archivist agent.ArchivistFn)` (4 params). Call: `maybeCompact(ctx, history, w.deps.Model)` (3 params). Won't compile. Fix: add `Archivist` to `ChatWorkerDeps` and thread to the call, OR drop archivist from the signature (nil-safe path already inside `agent.Compact`).

- [ ] `errors` missing from `chat.go` import block — Task 6 — `ErrChatInboxFull = errors.New(...)` but `"errors"` isn't imported. Add to the import list.

- [ ] `InsertChatMessage` / `ListRecentChatMessages` + `chat_message` columns don't exist — Tasks 6, 7 — The plan calls `q.InsertChatMessage(..., WorkspaceID, AgentID, UserID, Role, Content, StreamID)` and `q.ListRecentChatMessages(...)`. Existing `chat_message` schema (Phase 2) only has `chat_session_id`, `role`, `content`, `task_id`, `created_at`. Migration 162 added `stream_id` only. Fix: (a) extend migration 162 with ADD COLUMN `workspace_id UUID NOT NULL`, `agent_id UUID`, `user_id UUID` — or pick existing columns; (b) add a `server/pkg/db/queries/chat.sql` file to Task 5 with the two queries; (c) align the Go call shapes.

- [ ] `testutil` chat helpers have no creation step — Tasks 6, 7 — References `testutil.NewFakeRedis`, `StubModelReplying`, `StubModelWithContextWindow`, `SeedChatMessage`, `AlwaysAllowBudget`, `DenyBudget`, `JSONBytes`, `NoopWS`, `PublishTest`, `WaitForResponse`. None defined anywhere. Add Pre-Task 0 creation step listing each helper's signature.

- [ ] `BudgetAllowFn` type never defined — Task 6 `ChatDeps` — Commented as "from Phase 6 service/delegation.go" but not declared in the plan. Inline definition `type BudgetAllowFn func(ctx context.Context, estCents int) (bool, string)` in Preconditions, or define inline in Task 6.

## Should Fix

- [ ] `EmbeddedWorkerInbox` consumer never wired — Tasks 6, 7 — `ChatService.Dispatch` sends to `inbox` but no worker reads from it. `ChatWorker.Run` unconditionally goes through Redis. Add an `else`-branch to `Run` that reads from `EmbeddedWorkerInbox` when set.

- [ ] `publicEventName` has no unit test — Tasks 11/18 — Rename is covered only via integration. Add a table-driven `TestPublicEventName` unit test covering all 9 public names + default passthrough.

- [ ] `maybeCompact` depends on Phase 5 `agent.Compact`/`CompactOptions`/`ArchivistFn`/`ChatContextCompactThreshold` — none verified in Preconditions — Task 7 — Add an explicit Preconditions row + a no-op fallback if Phase 5 hasn't shipped this surface.

- [ ] Task 15 Swagger annotations are hand-wavy — specify which handlers are in-scope (at minimum the eight new Phase 8A ones) + give the annotation template for each.

## Nits

- `strings` used in Task 7's compaction test but absent from the test-file import block.
- `consumerName = "worker-" + ""` placeholder in `subscribe()` collides across worker instances; thread `InstanceID` through `ChatWorkerDeps`.
- `splitChannel` / `splitPath` stubs return `nil` — copy-paste agent gets runtime panic on `parts[1]`.
- Task 8's test uses pub/sub `PublishTest` for `chat_response:`, while inbound uses Streams. Document the split explicitly (Streams in, pub/sub out).

## Verdict

Architecturally solid after R1 + R2. Execution-readiness is the issue: six blockers prevent Tasks 6–8 from compiling for an agent picking the plan up cold (Streams methods undeclared, signature mismatch, missing import, schema + query shapes off, testutil helpers uncreated, BudgetAllowFn undefined). Four Should-Fix items close remaining gaps (embedded consumer not wired, missing rename unit test, Phase 5 fallback, swagger annotation scope). Fix these ten items and the plan is ready for task-by-task execution. The remaining tasks (webhooks, templates, API keys, playground, OpenAPI, onboarding) are independently executable as-is.
