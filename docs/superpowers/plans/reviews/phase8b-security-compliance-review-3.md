# Phase 8B Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8b-security-compliance.md`
Source: `PLAN.md` §Phase 8B + D1–D8
R1 applied: 3+3; R2 applied: 2+6 (audit-diff, OIDC state, audit drop obs, revision race, limiter eviction, rollback test, SAML key, JSONB LIKE).

---

## Blocking Issues

- [ ] `NextRevisionNumber` sqlc query referenced by `Rollback` but removed in R2 — Task 9 `revision.go` — R2 embedded `MAX+1` inside `InsertAgentRevision` and dropped `NextRevisionNumber`. `Rollback()` still calls `txQ.NextRevisionNumber(ctx, …)` and also passes `RevisionNumber: int32(next)` to `InsertAgentRevision`, but the new sqlc query no longer accepts that field. Fix: rewrite `Rollback` to use the new subquery-based `InsertAgentRevision` with the same retry-on-23505 loop as `Snapshot`, removing the separate `NextRevisionNumber` lookup + explicit `RevisionNumber` param.

- [ ] `revision.go` imports incomplete — Task 9 — Code uses `pgtype.UUID`, `pgconn.PgError`, `errors.As`, `fmt.Errorf`, `atomic.*`; import block shows only `context`, `encoding/json`, `uuid`, `pgxpool`, `db`. Add `github.com/jackc/pgx/v5/pgtype`, `github.com/jackc/pgx/v5/pgconn`, `errors`, `fmt`.

- [ ] `randomBase64(16)` called by `BeginAuth` but undefined — Task 10 — Add a named definition (e.g. `func randomBase64(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return base64.URLEncoding.EncodeToString(b) }`) and include `crypto/rand` in imports.

- [ ] `oidc.go` imports + `golang.org/x/oauth2` dep missing — Task 10 + Pre-Task 0 — Code uses `crypto/hmac`, `crypto/sha256`, `crypto/subtle`, `encoding/base64`, `encoding/json`, `errors`, `fmt`, `strings`, `time`, `golang.org/x/oauth2`. Import block shows only `github.com/coreos/go-oidc/v3/oidc`. `oauth2` also missing from Pre-Task 0 `go get`. Add both.

- [ ] JSONB credential-id cast — `::text` vs stored format unspecified — Task 13 `ListAgentsUsingCredential` — `@>` containment is type-strict; `::text` works only if Phase 1 wrote credential IDs as JSON strings. Document the expected storage shape explicitly (or change to `::uuid` per Phase 1's convention) so an agent doesn't ship a silently-never-matching query.

## Should Fix

- [ ] Cookie `Path: "/auth/oidc"` — correct per RFC 6265 prefix rules but unintuitive — Task 10 — Add an explicit comment: "`/auth/oidc` matches any path under `/auth/oidc/` including `/auth/oidc/callback` per RFC 6265 §5.1.4. Do NOT change to `/auth/oidc/callback` — that restricts the cookie from being sent to the initial redirect."

- [ ] `golang.org/x/time/rate` missing from Pre-Task 0 — `ratelimit.go` uses it. Add to `go get` list.

- [ ] `harness.Config.SystemPrompt` referenced by rollback test but not confirmed in Preconditions — Task 14 — Add a Preconditions row stating `harness.Config.SystemPrompt string` is the Phase 2 field carrying an agent's instructions at claim time.

## Nits

- `AuditService.consumer` uses `atomic.AddInt64` / `atomic.LoadInt64` — add `sync/atomic` to the import block.
- The plan uses both "agent instructions" and "system prompt" terminology for the same concept. Pick one.

## Verdict

Architecture, migrations, and test structure are sound. Execution blockers are mechanical: one sqlc query that was dropped in R2 is still called from `Rollback`, two import blocks are incomplete (`revision.go`, `oidc.go`), one helper is undefined (`randomBase64`), one Go dependency is missing from Pre-Task 0 (`oauth2`), and one JSONB cast semantic is ambiguous without documenting Phase 1's storage shape. All five are targeted, paste-level edits. Once applied the plan is ready for task-by-task execution. Mark `done` after fixes land.
