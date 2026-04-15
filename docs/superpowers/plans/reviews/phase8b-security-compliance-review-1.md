# Phase 8B Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8b-security-compliance.md`
Source of truth: `PLAN.md` §Phase 8B (~line 1596) + D1–D8 invariants

---

## Blocking Issues

- [ ] Credentials UI missing last-rotation, usage-count, cooldown status, and which-agents binding — §8B.1 / Task 13 — PLAN.md §8B.1 names all four display fields as requirements. Task 13's `CredentialsView` description only covers list/create/rotation. Expand the `Credential` type + view to include `last_rotated_at`, `usage_count`, `cooldown_until`, and a list of agents using each credential (backed by a new `ListAgentsUsingCredential` query or a join in the existing list).

- [ ] `ProviderTracker.Observe` never wired into the LLM backend — §8B.3 / Task 8 — Dead code without a call site; the 429 feedback loop specified in PLAN.md §8B.3 ("dynamic adjustment: when provider returns 429, automatically throttle all agents using that provider") cannot work. Add Phase 2's LLM API backend to Modified Files and a task step injecting the tracker + calling `Observe(provider, respHeaders, statusCode)` after every LLM response.

- [ ] Cadence multi-stage quota pattern not implemented — §8B.3 / Task 8 — PLAN.md §8B.3 says "Port from Cadence `common/quotas/` multi-stage rate limiter." The plan implements a flat two-stage bucket (workspace + agent) with the provider tracker sidecar. Multi-stage adds a cluster/global stage so a single-workspace burst cannot exhaust the global quota. Either add the global stage or explicitly justify the deviation in the Scope Note.

## Should Fix

- [ ] Default retention policies never seeded — §8B.6 / Task 11 — PLAN.md §8B.6 specifies audit_log default 365d + trace default 90d. Plan is policy-driven so fresh workspaces without policies never delete anything. Seed defaults via workspace-creation hook OR migration DEFAULT INSERT.

- [ ] In-flight-task isolation on rollback asserted but not tested — §8B.4 / Task 9 — Comment defers to Phase 2 harness closure behavior; no test verifies it. Add an integration test: start task → rollback → assert running task's config unchanged.

- [ ] `credential_access_log` verification missing from §8B.7 checklist — Task 14 — PLAN.md §8B.7 lists "Credential access logged in credential_access_log" as a distinct item; the plan omits it (only checks that the raw key isn't surfaced). Add a checkbox item.

## Nits

- `TestProviderTracker_429SetsThrottle` unconditionally sleeps 5.1 s. Gate with `-short` skip.
- `ExportCSV` loads 50k rows into an in-memory slice. Stream from a cursor would avoid OOM on large workspaces.
- `actionFromRequest` strips trailing `s` naively; breaks for `sso`, `audit`. Use a resource-name lookup map.

## Verdict

Architecturally sound — covers migrations, Paperclip `activity_log` pattern as immutable audit_log with triggers, SAML + OIDC split, enforce_sso middleware, 90-day audit floor enforced at service + DB, is_rollback/rolled_back_from chain for versioning. Three blockers need fixing before execution: the credentials UI is missing four of the five fields §8B.1 explicitly requires; `ProviderTracker.Observe` is dead code without a call site; and the Cadence multi-stage rate-limit pattern is cited in PLAN.md but the plan implements a flat two-stage. Three should-fix items close the retention-defaults gap, add a real rollback-isolation test, and fix the missing §8B.7 checklist item.
