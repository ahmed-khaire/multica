# Phase 3 Plan — Post-Blocker Audit

## Summary

The Phase 3 plan (MCP client + Skills registry) predates the D1–D8 blocker fixes in PLAN.md. Most Phase 3 mechanics (Redis key shape, 4h TTL, Hermes `tools/mcp_tool.py` citation, per-agent `runtime_config.mcp_servers`, `RegisterCleanup` additive change, go-ai `SkillRegistry` wrapping) are already aligned with the updated spec. Two material drifts require edits:

1. **D8 `trace_id` propagation is entirely missing.** Task 8's resolver calls `context.Background()` in four places (MCP `Connect`, `Discover`, `SkillsBuilder.Build`, and the cleanup closure), which discards the task's `trace_id` — breaking the cost-event attribution chain PLAN.md §80–83 relies on. Every MCP tool call must carry `trace_id` because Phase 9's chain-cost ceiling queries `cost_event.trace_id`.
2. **Workspace-safety acknowledgement is missing from Task 9.** PLAN.md §235–238 requires every new handler to call out workspace-scoped reads/writes; Task 9 silently piggybacks on the existing create/update handler but does not state the invariant, making review fragile.

Minor drifts: a stale self-review gap note (Task 5) already resolved inline; a `context.Background()` in the skills invalidator's bus handler (unavoidable, since WS events have no ambient task context — but worth documenting); and PLAN.md §3.2 example uses `"transport": "sse"` while the Phase 3 plan correctly exposes only `stdio|http` (SSE is a subtransport of HTTP in go-ai). That's a drift in PLAN.md, not in the audited plan.

**Verdict: approve with edits.** Five required edits, three small; the D8 edit is the only one with correctness impact.

## Required edits

### E1. Propagate `trace_id` through MCP + Skills resolver (D8)

- **Old:** Task 8 resolver uses `context.Background()` for MCP connect, tool discovery, and skills build.
- **Where:** `docs/superpowers/plans/2026-04-14-phase3-mcp-skills-registry.md`, Task 8 Step 4 (around lines 1559–1609).
- **New:** Thread the inbound resolver context through every call; drop `context.Background()`; add a one-line note that MCP tool executions preserve `trace_id` via the `Execute` closure (which already receives `ctx` from the harness).

### E2. Assert workspace-scoping in Task 9 handler validation

- **Old:** Task 9 only describes the `mcp.ParseServerConfigs` call — silent on the workspace-membership invariant PLAN.md §235–238 requires of every handler that touches agent rows.
- **Where:** Task 9 Step 1.
- **New:** Add the explicit workspace-load check before the config parse.

### E3. Remove obsolete go-ai Skill-shape gap in the self-review

- **Old:** Self-review lists as an open gap: `Task 5 assumes goagent.Skill has {Name, Description, Body, Files} fields. Verify against live go-ai source...` — but Task 5's Step 3 already states it has been verified.
- **Where:** "Gaps flagged during review" block.

### E4. Document the invalidator's `context.Background()` as intentional

- **Old:** `skills_invalidator.go` uses `context.Background()` inside the event handler without explaining why.
- **Where:** Task 7 Step 3 implementation.

### E5. Align PLAN.md §3.2 example on transport names (informational, NOT an edit to Phase 3 plan)

PLAN.md §3.2 example still uses `"transport": "sse"`, but go-ai uses `http` as the top-level transport name. Follow-up patch to PLAN.md; out of scope for the Phase 3 plan audit.

## Missing tasks

None that block Phase 3 completeness. The plan already covers every §3.1, §3.1.1, §3.2, §3.3, §3.4 spec item.

One soft addition worth considering: a Task 11 verification step that greps `cost_event` rows after the smoke-test task to confirm MCP tool invocations attribute to the same `trace_id` as the outer task. This is the D8 integration test.

## Obsolete tasks

None. Every Phase 3 task maps to a current PLAN.md line.

## Verdict

**Approve with 5 edits.** E1 (trace_id propagation) is the only correctness-affecting drift and must land before Phase 3 ships. E2 is a safety-documentation hygiene fix. E3–E5 are cleanup.
