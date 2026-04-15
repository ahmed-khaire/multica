# Phase 8A Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8a-platform-integration.md`
Source of truth: `PLAN.md` §Phase 8A + D1–D8 invariants
Round 1 applied: 3 blocking + 4 should-fix (see `reviews/phase8a-platform-integration-review-1.md`).

---

## Blocking Issues

- [ ] Chat claim has no re-delivery path on worker crash — Task 7 `handleMessage` — SETNX on `chat_claim:{stream_id}` with TTL=300s dedups but doesn't re-deliver: if the claimer crashes mid-LLM-call, the pub/sub message is already consumed and the TTL expiry produces nothing. Switch to Redis Streams (XADD on producer, XREADGROUP per consumer group, XACK on success, XAUTOCLAIM reclaiming pending entries after 5 min) for at-least-once with automatic re-delivery. Or add a `pending_chat` sorted set + recovery goroutine.

- [ ] `ON CONFLICT DO NOTHING` has no conflict target — Task 2 migration 161 seed INSERT — Without an explicit target, `ON CONFLICT DO NOTHING` only matches PK conflicts; the PK is a random UUID so re-running duplicates all 5 rows. The partial UNIQUE index added in Round 1 is effectively unused. Fix: `ON CONFLICT (name) WHERE is_system = true AND workspace_id IS NULL DO NOTHING`.

- [ ] `EmbeddedWorkerInbox` unbounded + backpressure returns ctx.Err() — Task 6 `Dispatch` — Plan declares `chan<- ChatMessage` with no capacity and Dispatch's select falls back to `ctx.Done()`. Slow worker blocks the HTTP handler until the request context expires (~minutes). Bound to 64; on full, return HTTP 503 with explicit `errors.Is(err, ErrChatInboxFull)` not `ctx.Err()`.

- [ ] `model.ContextWindow()` is not defined in any prior phase — Task 7 `maybeCompact` — Plan calls `model.ContextWindow() * 4` but Phase 2's `harness.Model` interface doesn't expose context-window size. Add to Preconditions a Pre-Task 0 bootstrap extending `harness.Model` with `ContextWindow() int`, or delegate to `agent.ContextWindowFor(provider, modelID)` (a Phase 5 registry lookup) if Phase 5 provides one.

## Should Fix

- [ ] `deliver()` opens the credential vault on every call including retries — Task 11 `webhook_outbound.go` — 3 retries × N concurrent subs = N×3 vault RPCs per failed delivery. Fetch once per `retryLoop` row iteration and document explicitly there is NO persistent cache (vault is the source of truth; each retry fetches fresh).

- [ ] `publicEventName` mutates `ev.Type` in-place — Task 11 `subscribe` — If `events.Bus` delivers by pointer the rename is a data race with other subscribers reading the same event. Rename into a local `publicEv := ev; publicEv.Type = publicEventName(ev.Type)` copy.

- [ ] `delegation.completed` event name collision — Verify Phase 6's `DelegationService` emits exactly `delegation.completed` (PLAN.md §D8 explicit name). If Phase 6 emits a different internal name (e.g. `subagent.completed`), add a `publicEventName` mapping for it. Cross-check before final execution.

- [ ] `TouchAPIKeyUsage` is synchronous write on the auth hot path — Task 13 `Verify` — Every authenticated request writes `last_used_at` synchronously. Fire in a background goroutine with `context.Background()` so the response path isn't blocked; document that last_used_at is best-effort and may trail by seconds.

- [ ] `Credentials.GetRaw` byte-slice ownership contract undocumented — Task 10 — `defer agent.Zero(secret)` is only safe if GetRaw returns a freshly allocated owned `[]byte`, never a shared slice into a cache. Add to Preconditions an explicit contract: "GetRaw always returns a freshly allocated slice; caller owns and must zero it."

## Nits

- Task 6 Dispatch hardcoded 50-cent budget estimate — extract to `defaults.ChatBudgetEstimateCents`.
- Task 7 `buildChatSystemPrompt` returns generic string — load agent.Instructions from DB before production.
- `splitChannel` + `splitPath` stubs return `nil` — Placeholder Scan claims no TBDs; adjust.
- `ListActiveSubscriptionsForEvent` filters workspace in Go, not SQL — add `AND workspace_id = $2`.

## Verdict

Architecturally coherent; Round 1 addressed completeness. Round 2 finds four correctness blockers: the chat pub/sub model drops messages on worker crash (reliability gap), `ON CONFLICT DO NOTHING` without a target makes Round-1's idempotence fix a no-op on fresh DBs, `EmbeddedWorkerInbox` backpressure is undefined and could stall the HTTP handler for minutes, and `model.ContextWindow()` is never defined in any prior phase (compile blocker). The five Should Fix items address vault rate-limit exposure, a potential data race in event rename, a Phase-6 event-name cross-check, hot-path write to API-key table, and an undocumented byte-slice ownership contract. Fix the four blockers + address the five should-fix items before Round 3 (executability).
