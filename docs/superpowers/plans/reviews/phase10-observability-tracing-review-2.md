# Phase 10 Review — Round 2 of 3 (Technical Soundness)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase10-observability-tracing.md`
Source: `PLAN.md` §Phase 10 + D1–D8
Round 1 applied: 2 blocking + 3 should-fix.

---

## Blocking Issues

- [ ] `task_message` has no `workspace_id` column — Task 2/3 — Existing Phase 2 schema is `(id, task_id, seq, type, tool, content, input, output, created_at)`. Plan's FTS index + SearchTaskMessages both filter on `m.workspace_id`. Fix: rewrite SearchTaskMessages to JOIN through `agent_task_queue.workspace_id`; drop the composite index from migration 186 or rewrite to use task_id.

- [ ] `-- +migrate Up notransaction` is Goose syntax; custom migrate runner ignores it — Task 2 — `server/cmd/migrate/main.go` does not parse magic comments; CREATE INDEX CONCURRENTLY will fail inside implicit transaction. Fix: use regular `CREATE INDEX` (empty table, brief lock acceptable) + document upgrade path.

- [ ] D8 ctx helpers + testsupport methods undefined — Task 4/11 — `agent.WithTraceID`/`TraceIDFrom`, `TraceIDForRun`, `TraceIDForTask`, `GetTraceTree`, `ListSpans`, `CountSpans` none exist in repo. Add explicit Task 0 bootstrap before Task 4's failing test.

## Should Fix

- [ ] `Span.End` uses `context.Background()` unbounded — Task 4 — Deferred span write blocks forever on DB outage. Add `context.WithTimeout(ctx.Background(), 5s)` wrapper.

- [ ] `LateLinkCostEvent` idempotency semantics — Task 4 — `WHERE cost_event_id IS NULL` is first-write-wins, not idempotent. Document OR broaden to `WHERE cost_event_id IS NULL OR cost_event_id = $3`.

- [ ] OTel field name collides with package import alias — Task 7 — `otel oteltrace.Span` field vs `otel.SetTracerProvider(tp)` package both named `otel` in same file. Rename field to `otelSpan`.

- [ ] `classify.ts` TS mirror of Phase 1.3 classifier not established — Task 10 — `packages/core/errors/classify` doesn't exist. Add explicit stub-creation sub-step before SpanDetails task.

## Nits

- `ListTracesForWorkflowRun` uses DISTINCT + window functions; cleaner as GROUP BY.
- Waterfall `t0` calculation crashes on empty-span tree (`Date.parse(undefined)` = NaN).
- Trace-tree endpoint unbounded — add `?limit=500` cap.

## Verdict

Three blockers prevent execution: task_message FTS depends on a workspace_id column that doesn't exist; the migrate tool has no mechanism for the `notransaction` directive so CONCURRENTLY would fail; D8 helpers + testsupport methods are assumed present but never defined. Four should-fix items address unbounded End() latency, LateLink semantics, OTel import alias collision, and missing classify.ts. All seven plus three nits should land before Round 3.
