# Phase 12 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase12-claude-managed-agents.md`
Source: `PLAN.md` §Phase 12 (lines 2227–2325) + D1–D8
Round 1 applied: 3 blocking + 3 should-fix.

---

## Blocking Issues

- [ ] Watchdog `cancelRead()` cannot unblock `stream.Next()` — Task 7 Step 2 `drain` — the SSE `stream.Next()` reads from `resp.Body` which is not context-aware; cancelling a derived ctx has no effect on a blocking `bufio.Scanner.Scan()`. The idle timeout never fires. Fix: close the HTTP body from the watchdog goroutine (`stream.Close()` or direct `resp.Body.Close()`) instead of only cancelling the context.

- [ ] Non-atomic post-Execute writes — Task 9 Step 2 — `UpsertTaskSessionID`, `UpdateAnthropicAgentID`, `InsertSessionCostEvent`, `MarkTaskCompleted` are four independent statements. A crash between any of them leaves the task `in_progress` but replays with lost container_id → duplicate `CreateAgent` calls, dangling Anthropic container. Fix: wrap the four writes in a single pgx transaction; roll back on any error so the task is safe to retry.

- [ ] `InsertSessionCostEvent` failure silently swallowed — Task 9 Step 2 — `if err == nil { LateLinkCostEvent(...) }` but `MarkTaskCompleted` runs unconditionally, producing completed tasks with no cost row. Fix: treat cost-insert failure as task failure: `if err != nil { return w.fail(ctx, q, span, taskID, err) }`.

## Should Fix

- [ ] `time.AfterFunc` + `watchdog.Reset` + `defer watchdog.Stop` races — Task 7 Step 2 `drain` — Go docs warn against Reset on a fired timer without draining; concurrent Reset/Stop when the fire-goroutine is mid-callback is undefined. Fix: use `time.NewTimer` with the Stop+drain pattern (`if !t.Stop() { select { case <-t.C: default: } }`) before every `Reset`.

- [ ] `sessionOut`/`agentOut` synchronous sends can deadlock on retry — Task 7 Step 2 — if `Execute` is called twice on the same backend instance (unlikely today but Phase 2's `agent.Run` may retry internally), the second send to a cap-1 buffered channel with an unread first value blocks forever. Fix: non-blocking send `select { case ch <- v: default: }`.

- [ ] Down migration `NOT VALID → VALIDATE` pair grabs `SHARE UPDATE EXCLUSIVE` twice — Task 1 Step 2 — acceptable for rollback but the plan should note the concurrent-write window. Fix: add an inline comment acknowledging the lock window; consolidate both ADD CONSTRAINT into a single block followed by one VALIDATE pair at the end.

- [ ] `FakeAnthropicServer` hard-codes `/v1/sessions/sess_fake/...` paths — Task 8 Step 1 — any future test that scripts a different session ID returns 404 with an opaque error. Fix: register `mux.HandleFunc("/v1/sessions/", ...)` and dispatch on path suffix so any session ID works.

## Nits

- `ClaudeManagedSSEIdleTimeout` as `var` makes tests that want a shorter timeout mutate the global non-safely. Consider adding an optional `IdleTimeout time.Duration` on `ClaudeManagedConfig` with zero-value fallback.
- `_ = readCtx // keep compiler happy` is a code smell — either thread `readCtx` into `stream.Next()` once the blocker above is fixed, or delete it.
- `<Checkbox>` label wiring in `ClaudeManagedFlow` may not be `getByLabelText`-compatible with Base UI's primitives; the smoke test could fail to locate the element.

## Verdict

Three blockers will cause silent data loss or hang: a watchdog that can't interrupt a blocking SSE read, non-atomic post-Execute writes that leak containers + miss cost rows on crash, and a swallowed cost-insert error. Four should-fix items cover a Timer.Reset race, potential channel-send deadlock on retry, migration lock-window documentation, and brittle mux registration. All seven require targeted edits to Task 1, Task 7, Task 8, and Task 9 before Round 3.
