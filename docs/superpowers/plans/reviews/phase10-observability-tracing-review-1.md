# Phase 10 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase10-observability-tracing.md`
Source: `PLAN.md` §Phase 10 (~line 1898) + D1–D8

---

## Blocking Issues

- [ ] CSV export missing — §10.2 — SearchHandler returns JSON only; PLAN.md §10.2 requires "Export results as CSV for compliance review". No backend branch, no download button, no test. Add `?format=csv` branch OR `/export` route + streaming csv.Writer + download button in SearchPage.

- [ ] Workflow-run trace chain untested — §10.3 / §10.5 — Integration test covers delegation only; workflow-step spans' trace_id propagation never exercised. Add `TestTrace_WorkflowRunTree` seeding a workflow run, asserting workflow_step spans share the same trace_id as parent.

## Should Fix

- [ ] Error highlighting ignores Phase 1.3 classifier — §10.4 — SpanDetails renders `span.error` as raw string. PLAN.md §10.4 requires "Error highlighting with classified error types (from Phase 1.3)". Import the Phase 1.3 classifier, map category → semantic color token.

- [ ] Filter-by-workflow missing — §10.4 — Trace viewer has no workflow filter and no endpoint. Add `ListTracesForWorkflowRun` sqlc + `?workflow_run_id=` query param + UI dropdown.

- [ ] cost_event_id late-bind ordering undocumented — §10.1 / Task 4-5 — `SetCostEventID` before `End` works for happy path, loses link on panic/rollback. Document ordering OR add dedicated `UpdateSpanCostEventID` query callable post-End for rollback paths.

## Nits

- SearchPage not debounced — every keystroke fires a query.
- `TestTrace_DelegationChainTree` asserts `< 3` count but not operation names; tighten to require `llm_call` + `subagent_dispatch`.
- `mcp_call` appears in CHECK constraint + TS union but Task 6 has no MCP-specific instrumentation step. If MCP routes through ToolRegistry that's fine; document + add assertion.

## Verdict

Plan is structurally complete and covers most §10 requirements with concrete runnable code. Two blockers before execution: CSV export is listed as requirement but not implemented, and workflow-run trace chain is claimed in prose but not tested. Two should-fix items (error-classifier integration, filter-by-workflow) are §10.4 requirements absent from tasks. Fix the four items before Round 2.
