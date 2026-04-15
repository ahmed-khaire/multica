# Phase 8A Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase8a-platform-integration.md`
Source of truth: `PLAN.md` §Phase 8A (located by H2 heading — ~line 1292) + D1–D8 invariants

---

## Blocking Issues

- [ ] Context compaction shipped as a stub, not implemented — Task 7 `maybeCompact` — PLAN.md §8A.1 requires compaction at 70% of model window via Phase 5 `agent.Compact(ctx, provider, messages, opts)`. The plan's worker includes a "Placeholder — real implementation uses Phase 5" stub + a Placeholder-Scan exemption. No task actually wires the real call. Add a Step 3a in Task 7 replacing the stub body with `agent.Compact(...)` and a test that crosses `ChatContextCompactThreshold`.

- [ ] `approval.needed` outbound event missing — Task 11 — PLAN.md §8A.3 names 9 events: task.completed/task.failed/workflow.completed/workflow.failed/approval.needed/budget.warning/budget.exceeded/agent.error/delegation.completed. The plan wires `events.Bus` subscription but has no step mapping Phase 7's `approval.created` WS event to an outbound `approval.needed` fire, no test in `webhook_e2e_test.go` asserts it. Add an explicit wiring step + integration-test assertion.

- [ ] `GetWebhookSubscriptionByID` used but never defined — Task 11 `retryLoop` + Task 5 `webhook.sql` — retryLoop calls `q.GetWebhookSubscriptionByID(ctx, ...)` but the query is not in Task 5's `webhook.sql` block. `make sqlc` + `go build` will fail. Add `-- name: GetWebhookSubscriptionByID :one SELECT * FROM webhook_subscription WHERE id = $1 AND workspace_id = $2;` to Task 5.

## Should Fix

- [ ] `agent_template.category` CHECK constraint silently extends PLAN.md §8A.4 — Task 2 — PLAN.md lists 4 categories (customer_support/legal/sales/operations); the plan adds content/data_analysis/other and seeds templates using them. Reasonable extension, but needs an explicit note in the migration comment + commit message documenting the divergence.

- [ ] Single-process chat fallback (Go channel bypass) never implemented — Architecture summary — PLAN.md §8A.1 mandates: "For single-process deployments (`--with-worker` mode), bypass Redis entirely — route directly to embedded worker goroutine via Go channel." Only a one-sentence mention in the summary; no task, no code. Add a conditional code path in Task 6/8 that uses an in-process `chan ChatMessage` when `--with-worker` + `memory` RedisAdapter are active; add a single test.

- [ ] `RequireScope` defined but never applied to concrete routes — Task 13 — Middleware exists but `router.go` has no snippet wrapping, e.g., `RequireScope("agents:write")` around the agent-create handler. §8A.5's "enforced per handler, not just parsed" requirement is not demonstrably met. Add router.go route-registration snippet showing wrapping.

- [ ] `chat_message.stream_id` column dependency not tracked — Task 6 — `InsertChatMessage` passes `StreamID`; Task 1's migration 160 doesn't add the column; Phase 2 may not have it. Add an `ALTER TABLE chat_message ADD COLUMN IF NOT EXISTS stream_id UUID` to migration 162 or a new 164 migration.

## Nits

- Task 7 `splitChannel` returns `nil`, so `parseWsIDFromChannel` always returns `uuid.Nil` in the committed stub. Test only passes because the stub never reads history. Flag it as part of the real-compaction fix.
- Migration 161 `ON CONFLICT DO NOTHING` is only idempotent with a `UNIQUE (workspace_id, name)` index, which the plan doesn't create. Add the unique index.
- Task 15 uses `go install github.com/swaggo/swag/cmd/swag@latest` as a manual step — pin via `tools.go` or `go.mod` tool directive for reproducibility.

## Verdict

Structurally complete: covers all 8 sub-sections, the billing note, and the verification checklist with TDD-shaped tasks and concrete code. Three blocking gaps prevent execution: context compaction is a stub with no follow-up task (violates §8A.1), `approval.needed` is missing from the outbound event wiring despite being named in §8A.3, and an undefined sqlc query will break the build. The four Should Fix items address the Go-channel single-process fallback mandated by §8A.1, per-route scope enforcement, schema drift on `chat_message.stream_id`, and documentation of the template-category extension. Fix the three blockers and the four should-fix items before Round 2.
