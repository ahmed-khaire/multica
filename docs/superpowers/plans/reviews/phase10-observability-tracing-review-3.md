# Phase 10 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase10-observability-tracing.md`
Source: `PLAN.md` §Phase 10 + D1–D8
R1 applied: 2+3. R2 applied: 3+4+3.

---

## Blocking Issues

- [ ] `groupBy` called in trace-waterfall.tsx but never defined/imported — Task 10 Step 3 — add inline `Map`-based helper.

- [ ] `stripHTMLTags` called in search CSV branch but never defined — Task 8 — add a one-liner `regexp.MustCompile(<[^>]+>).ReplaceAllString(...)` helper.

- [ ] `env.Search(ws, "rent")` used in Task 11 FTS test but `Env.Search` never declared in testsupport — add signature.

## Should Fix

- [ ] `tracer.go` Span struct uses `otelSpan oteltrace.Span` but import is absent from Task 4 block — add `oteltrace "go.opentelemetry.io/otel/trace"`.

- [ ] `useSearchExecutions` hook + `packages/core/api/search.ts` bodies never specified — file-structure table names them but no content. Add a Task 10 Step 1.5 showing the hook + API client.

- [ ] `trace-filters.tsx` listed in file structure but no implementation + not in git-add. Either remove from table or add a Step 3.5 body.

- [ ] `TraceTreeFetched.Spans` typed as `[]Span` but no Span struct defined in the testsupport block; integration tests use `s.ParentSpanID == nil` pointer semantics while unit tests use `pgtype.UUID.Valid`. Define a distinct JSON-decoded Span type.

## Nits

- `atoiOrDefault` used across three handlers with no definition shown — point to shared util file or inline.
- Task 8 `newSearchCtx` referenced via `tc.seedTaskMessage(...)` with no body shown.
- Task 8 `CountSearchResults` call uses `/* same params */` placeholder — write it out.

## Verdict

R1+R2 fixes landed cleanly. Three remaining executability blockers (`groupBy`, `stripHTMLTags`, `env.Search`) are small paste-level additions. Four should-fix items (tracer oteltrace import, search hook body, trace-filters component body, testsupport Span type) are also small. Once these land the plan is ready for task-by-task execution; mark `done`.
