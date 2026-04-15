# Phase 12 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase12-claude-managed-agents.md`
Source: `PLAN.md` §Phase 12 (lines 2227–2325) + D1–D8

---

## Blocking Issues

- [ ] Environment config (internet toggle + secret_credential_ids) is never persisted to the DB or fetched by the worker — Task 1 migration adds no `environment_config` column, no sqlc query retrieves it, and Task 9 Step 2 builds `ClaudeManagedConfig.EnvironmentConfig` from thin air. UI collects this data but it silently disappears. — Tasks 1, 6, 9 — Add `environment_config JSONB` column on `agent` in migration 188; extend `GetClaudeManagedAgent` to return it; deserialize + resolve secret IDs via the credential vault in the worker before building `CreateEnvRequest`.

- [ ] Container-reuse assertion left hollow — Task 9 Step 1's `TestClaudeManagedWorker_ReusesContainerIDForSameIssue` acknowledges `FakeAnthropicServer` can't capture `CreateSessRequest`, making §12.3/§12.7 verification empty. — Task 8 + Task 9 — Add `lastCreateSession anthropic.CreateSessRequest` to `FakeAnthropicServer` (captured inside the `/v1/sessions` handler) + assertion in the reuse test that task 2's request carries `ContainerID="cnt_fake"`.

- [ ] §12.1 says SSE must map to "our existing `Message` channel format" but `drain()` only accumulates into a `strings.Builder` and returns a final `Response` — no intermediate streaming progress. — Task 7 Step 2 — Either wire text_delta events to an `Opts.Messages chan<- Message` field per Phase 2's streaming contract, or explicitly document in-flight streaming as deferred in the Scope Note.

## Should Fix

- [ ] D7 requires constants in `defaults.go`; `ClaudeManagedSessionHourCents` and `ClaudeManagedContainerMaxDays` are `var`. `ClaudeManagedSSEIdleTimeout` legitimately needs `var` (time.Duration typed). — Task 2 Step 1 — Change the two int values to `const`.

- [ ] D6 requires `OnTimeout` hook for graceful-snapshot on long-running managed sessions. `Hooks()` returns an empty `&Hooks{}`. Idle timeout errors out without firing the hook. — Task 7 Step 2 — Either accept an `OnTimeout` hook via config and fire it before returning the error, or document the deferral explicitly alongside the existing snapshot/pause-resume Non-Goal.

- [ ] `GetSessionIDForIssue` uses `($3 || ' days')::interval` with `fmt.Sprintf("%d", ...)` — non-idiomatic + fragile. — Task 6 Step 3 — Use `NOW() - INTERVAL '1 day' * $3::int` with a typed int parameter.

## Nits

- Task 9 imports `pkg/trace` but File Structure table doesn't list it as a dependency.
- Task 11 references `cfg.AnthropicAPIKey` without showing the config struct extension anywhere.
- UI pricing banner shows per-1M-token rates while §12.6 says "per-1K tasks" — plan's approach is more accurate but diverges from spec wording.

## Verdict

Plan is thorough on SSE decoding, hybrid tool dispatch, and D8 tracing. Two data-flow blockers: environment config collected in the UI never round-trips through DB to the worker (so §12.2's "secrets injection from workspace vault" is non-functional), and the container-reuse test leaves §12.3/§12.7 verification hollow. §12.1's streaming message channel is structurally absent and needs either wiring or explicit deferral. Fix the three blockers + three should-fix before Round 2.
