# Phase 11 Review — Round 3 of 3 (Executability)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase11-cloud-coding-sandbox.md`
Source: `PLAN.md` §Phase 11 (lines 1984–2225) + D1–D8
R1 applied: 4+4. R2 applied: 4+4+3.

---

## Blocking Issues

- [ ] `dockerClient` interface uses wrong types — Task 6 Steps 1–2 — uses `*types.NetworkingConfig` and `*types.Platform`, but the real Docker SDK needs `*network.NetworkingConfig` (`github.com/docker/docker/api/types/network`) and `*ocispec.Platform` (`github.com/opencontainers/image-spec/specs-go/v1`). Interface + `fakeDockerClient` won't compile. Fix: correct the interface, the `fakeDockerClient` methods, and the import block.

- [ ] Ten `env.*` test helpers undeclared — Preconditions §Test helpers + Tasks 12/15 — `TaskStatus`, `CountCostEventsByProvider`, `TraceIDForTask`, `ListSpans`, `EnqueueTask`, `StubModelReplying`, `StubLLMFactory`, `CostCalc`, `Events`, `ActiveSandboxes` are called but never declared. Fix: add their signatures to the testsupport block in Preconditions with enough detail that an agent can stub them.

- [ ] `sandbox_instance_test.go` missing body — Task 11 — listed in File Structure and git-add but no test function exists anywhere. Agent will create an empty file and commit a claim of handler coverage. Fix: add at least one `TestSandboxInstanceHandler_List` body between Task 11 Steps 2 and 4 (register entry → hit handler → assert 200 + non-empty body).

## Should Fix

- [ ] Task 14 Step 5 runs `vitest run sandboxes/ agents/` but no `.test.tsx` files are authored — Task 14 Step 5 — the command passes vacuously with zero tests discovered. Fix: add a failing RTL smoke test (e.g. `SandboxTemplatesPage` renders "Sandbox Templates") before implementation steps, or replace the command with `pnpm typecheck`.

- [ ] `listOpts()` test-only helper lives inside production `gvisor_k8s.go` — Task 5 Step 2 — pollutes production package with test-only code and adds a production dependency on `metav1`. Fix: move `listOpts()` into `gvisor_k8s_test.go`.

- [ ] Task 10 Step 4 git-add omits `server/pkg/db/gen/` — Task 10 Step 4 — regenerated sqlc output untracked; later tasks referencing `db.New()` + the new param/row types break on a fresh checkout. Fix: `git add server/pkg/db/gen/` (trailing slash) alongside the query + service files.

- [ ] `row.Prompt.String` used in Task 12 but no migration adds a `prompt` column to `agent_task_queue` — Task 12 Step 2 + Step 2a — compile error on `row.Prompt`. Fix: either cite the Phase 2 migration that adds `agent_task_queue.prompt`, or document the column shape inline in Preconditions and adjust `ClaimCloudCodingTask` RETURNING accordingly.

## Nits

- `NewPool` allocates a channel of capacity 1 even when `TargetSize == 0` — contradicts the "disable warming" comment in `TestPool_ColdAcquireCallsFactory`; test passes vacuously rather than exercising the zero-target path.
- Task 8 Step 3 imports `pkg/agent` from `pkg/sandbox`, adding a directional edge `sandbox → agent`. Risk if `agent` ever imports `sandbox`. Consider passing `bufferMs int` as a param to each `connectXxx` instead of reaching for the `agent` package constant.
- `env.RouteTo` return type implied by `resp.Code`/`resp.Body` usage; state it as `*httptest.ResponseRecorder` next to `RouteTo` in Preconditions.

## Verdict

Two blockers will produce immediate compile failures (Docker interface types + ten undeclared `env.*` helpers), and the third silently ships uncovered code (`sandbox_instance_test.go`). All three + the four should-fix items are small paste-level edits. Once applied the plan is ready for task-by-task execution; mark `done`.
