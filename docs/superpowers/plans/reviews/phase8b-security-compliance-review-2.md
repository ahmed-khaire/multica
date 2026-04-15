# Phase 8B Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8b-security-compliance.md`
Source of truth: `PLAN.md` §Phase 8B + D1–D8 invariants
Round 1 applied: 3 blocking + 3 should-fix (see reviews/phase8b-security-compliance-review-1.md).

---

## Blocking Issues

- [ ] Audit diff uses REQUEST body as "after" — broken for every PATCH — Task 6 Step 3 — `capturingWriter` captures only status; `bodyBuf` holds the request body. `after` parsed from the PATCH body is a partial diff, not the post-mutation state. The full `before` vs partial `after` produces bogus "deleted every other field" diffs. Fix: either capture response body in `capturingWriter` (with a reasonable size cap) and parse as `after`, OR require every mutating handler to set BOTH `X-Audit-Before` and `X-Audit-After` headers explicitly.

- [ ] OIDC `validateState` is undefined — CSRF vulnerability — Task 10 Step 2 — `validateState(stateRaw)` is called but never implemented. State parameter is the only CSRF protection in the OAuth2 flow. Implement signed-cookie nonce or server-side state store + comparison + test asserting invalid state → 400.

## Should Fix

- [ ] `AuditService.Record` silently drops entries under load — compliance risk — Task 6 — A regulator reading PCI-DSS/SOC 2 controls can treat silent audit gaps as evidence of inadequate logging. Either make `Record` synchronous, use unbounded channel + lag metric, OR emit structured CRITICAL log + metric on every drop (minimum viable). The verification evidence doc must acknowledge the gap explicitly.

- [ ] `NextRevisionNumber` has TOCTOU race — concurrent updates fail — Task 5/9 — Two concurrent PATCHes to the same agent both read the same max, both attempt `INSERT` with the same `revision_number`. UNIQUE(agent_id, revision_number) fails one of them → 500 to client. Fix: wrap next-number + insert in an advisory-lock tx, OR embed the subquery directly in the INSERT (`INSERT ... SELECT ... COALESCE(MAX,0)+1 FROM ...`), OR retry on unique-violation up to 3 times.

- [ ] Rate-limiter maps grow unbounded — memory leak — Task 8 — `ws` and `agent` maps never evict. Long-running server in a multi-tenant deploy leaks one token-bucket per workspace/agent forever. Add last-access timestamp + 5-min periodic cleanup goroutine evicting buckets idle for >24h, or replace with a size-bounded LRU.

- [ ] Rollback test asserts only `strings.Contains(result, "hello")` — doesn't verify v2 config was used — Task 14 Step 1 — A task that ran with the wrong (rolled-back) config still passes. Blocking model must capture which instructions were active at claim time; test asserts captured=="v2" even after rollback changed the agent row.

- [ ] SAML signing key shared across all workspaces — Task 10 Step 3 — `SAMLService.MiddlewareFor` uses server-level `s.signingKey`. Per-workspace SPs should have per-workspace keypairs. At minimum document as V1 limitation + add per-SP keypair as Phase 9 follow-up.

- [ ] `ListCredentialsWithBindings` uses `LIKE '%uuid%'` on JSONB — unsafe + slow — Task 13 — Full table scan on `agent`, false positives if a UUID appears in any JSONB field. Use a proper FK column (`agent.provider_credential_id`) from Phase 1 or drop binding list from V1 and compute on the frontend from separate queries.

## Nits

- `_ = time.Now()` dead-code line in `AuditService.consumer` — remove.
- `EnforceSSO` wsID extraction from "body or form" is a placeholder — extract from URL path like other middleware.
- CSV export hard-cap 50k rows OOM risk — stream from cursor.
- `oidc.NewProvider` called on every callback — outbound HTTP per auth; cache per workspace config.

## Verdict

Architecturally sound — ordering, tx boundaries, SET LOCAL app.role scoping (checked: transaction-scoped, safe across pool reuse) all correct. Two blockers prevent execution: audit diff is fundamentally wrong (compares full-before against partial-request-body), and OIDC state validation is a named-but-undefined function leaving the callback CSRF-vulnerable. Six should-fix items (audit drop observability, NextRevisionNumber race, limiter memory leak, weak rollback assertion, shared SAML key, LIKE-on-JSONB join) are important but non-blocking. Fix the two blockers + the audit-drop + revision-race + limiter-eviction trio before Round 3.
