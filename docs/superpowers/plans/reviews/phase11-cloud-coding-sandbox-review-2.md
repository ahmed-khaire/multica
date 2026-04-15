# Phase 11 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase11-cloud-coding-sandbox.md`
Source: `PLAN.md` §Phase 11 (lines 1984–2225) + D1–D8
Round 1 applied: 4 blocking + 4 should-fix.

---

## Blocking Issues

- [ ] singleflight coalescing shares one Sandbox across N concurrent callers — `pool.go` Task 7 `Acquire` — `sf.Do("cold-create", ...)` returns the *same* `Sandbox` pointer to every waiter; multiple workers own and eventually `Stop` the same sandbox (double-stop + data race on `e2bSandbox.mu`). Fix: let the winner push the sandbox to `p.ready` and have losers re-enter the top of `Acquire` for a second non-blocking pop, falling back to a fresh `Factory` only on still-empty channel.

- [ ] `ScheduleTimeoutHook` cancel is not concurrency-safe — `timeout.go` Task 8 — returned `func()` calls `close(done)` unguarded; if both Stop and the timer-fired goroutine close it (or Stop is invoked twice), the second `close` panics. Fix: wrap in `sync.Once`: `var once sync.Once; return func() { once.Do(func() { close(done) }) }`.

- [ ] `defer span.End("completed", nil)` misreports failures — `cloud_coding.go` Task 12 — worker failure paths call `markFailed` and return early, but the deferred End still fires with status `"completed"`. Fix: drop the `defer`; call `span.End("completed", nil)` only on the success path and `span.End("error", err)` inside `markFailed`.

- [ ] `PoolRegistry` uses `sync.Mutex` without importing `"sync"` — `cloud_coding.go` Task 12 import block — the file will not compile. Fix: add `"sync"` to the import list.

## Should Fix

- [ ] `TestScheduleTimeoutHook_CancelPreventsFire` is timing-flaky — `timeout_test.go` Task 8 — 100ms expiry − 50ms buffer leaves ~50ms for cancel+read+select races on loaded CI. Fix: widen to 500ms expiry / 400ms buffer so the cancel has headroom.

- [ ] Docker `ReadFile` strips a hardcoded 512-byte tar header — `gvisor_docker.go` Task 6 — breaks on files with GNU/POSIX extended headers. Fix: parse the tar stream via `archive/tar.NewReader` and read the first entry body.

- [ ] `markFailed` silently discards the DB error via `_ = q.MarkTaskFailed(...)` — `cloud_coding.go` Task 12 — if the mark-failed write fails, the task is stuck `in_progress` forever with no log. Fix: surface the inner error: `if markErr := q.MarkTaskFailed(...); markErr != nil { return fmt.Errorf("markFailed(%w): %w", markErr, err) }`.

- [ ] `useSandboxInstances` polling violates CLAUDE.md's "WS events invalidate queries" rule — `queries.ts` Task 13 — the plan already requires `events.Bus` to emit `sandbox.started`/`sandbox.stopped`; wire those into WS invalidation instead of polling, or remove the dashboard hook from Phase 11 scope. Fix: replace `refetchInterval` with a `useWSInvalidate(["sandbox.started","sandbox.stopped"], ["sandbox-instances", wsId])` subscription + keep the query at default staleTime.

## Nits

- `podExecParamCodec.EncodeParameters` always returns `map[string][]string{}` — `gvisor_k8s.go` Task 5 — real K8s exec would lose Command/Stdin/Stdout/Stderr. Use `scheme.ParameterCodec` from `k8s.io/client-go/kubernetes/scheme`.
- `tarSingleFile` uses `filepath.Base(dstPath)` discarding the directory — `util_docker.go` Task 6 — should write the *relative* path with `CopyToContainer(ctx, id, "/", ...)` so extract lands at the intended location.
- `agent.Run(ctx, be, prompt)` referenced in `cloud_coding.go` Task 12 is not a cited Phase 2 symbol — add a Preconditions line naming the exact package export.

## Verdict

Round 1's pool-registry + span-wiring + down-migration edits landed cleanly. Round 2 surfaces four new blockers at the concurrency/lifecycle layer: singleflight double-stop race, `close(done)` double-panic, span always-completed, and a missing `sync` import. Plus four should-fix items that will bite during integration (flaky test, brittle tar parsing, swallowed DB errors, CLAUDE.md polling violation). Fix the four blockers + four should-fix before Round 3.
