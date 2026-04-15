# Phase 11 Review — Round 1 of 3 (Completeness & Fidelity)

Reviewer: `feature-dev:code-reviewer` (fresh subagent)
Plan: `docs/superpowers/plans/2026-04-14-phase11-cloud-coding-sandbox.md`
Source: `PLAN.md` §Phase 11 (lines 1984–2225) + D1–D8

---

## Blocking Issues

- [ ] D8 trace_id propagation is incomplete for tool-call spans — Task 12 mints `trace_id` on the worker's `RunOnce` and starts one span for the outer `cloud_coding_task`, but no span is emitted per tool invocation (`execute_command`, `read_file`, etc.). PLAN.md §11.3 and D8 require sandbox ops to emit `tool_call`-shaped spans via Phase 10 `Tracer.StartSpan`. `ScheduleTimeoutHook` and `SandboxBackend.Execute` (Task 9) have no span wiring. — Tasks 9 and 12 — add `Tracer.StartSpan("execute_command", ...)` inside each tool's `Execute` method or add span wrappers in the `SandboxBackend.Tools()` bindings.

- [ ] `trace.LateLinkCostEvent` never called — the cost event is inserted in Task 12 Step 2 with no call. PLAN.md D8 + the plan's own Preconditions (line 70) state this is required. — Task 12, Step 2 — after `InsertCostEvent`, call `w.deps.Tracer.LateLinkCostEvent(ctx, span.ID(), costEventRow.ID)`.

- [ ] The `Pool` in `CloudCodingDeps` is typed as a single global `*sandbox.Pool` — violates §11.4's per-template pool requirement. Two templates would share warm sandboxes with different images/configs. — Task 12 `CloudCodingDeps` — change to `Pools map[uuid.UUID]*sandbox.Pool` keyed on `templateID` + pool-lookup logic before `Acquire`.

- [ ] Down migration missing VALIDATE + cleanup guard — Task 1 Step 2's restored `agent_runtime_check` and `agent_type_check` are added `NOT VALID` with no matching `VALIDATE`. If existing `cloud_coding` rows remain, `VALIDATE` would fail anyway. — Task 1 Step 2 — add `DELETE FROM agent WHERE agent_type = 'cloud_coding';` guard before the constraint restoration, then `VALIDATE CONSTRAINT` after each `ADD CONSTRAINT … NOT VALID`.

## Should Fix

- [ ] gVisor-K8s and gVisor-Docker `Stop` bodies in Tasks 5/6 don't show `timeoutCancel` field or invocation — Task 8 Step 3 says "edit each backend" but doesn't show the update. Without this the cancel is leaked on every sandbox stop. — Tasks 5 and 6 — add `timeoutCancel func()` field to both sandbox structs and show the updated `Stop` bodies explicitly.

- [ ] `useSandboxInstances` uses hard-coded `refetchInterval: 5_000` instead of WS invalidation — violates CLAUDE.md's "WS events invalidate queries" rule. Silent drift vs. acknowledged V1 exception. — Task 13 Step 2 — add inline comment acknowledging polling as V1 exception until Phase 12 adds a WS event for sandbox lifecycle.

- [ ] `Pool.refillLoop` uses `context.Background()` in `Factory` call — refill goroutines leak E2B credits until `Shutdown()` is explicitly invoked. — Task 7 Step 2 — store a pool-owned cancel context at construction time; pass that context to `Factory`.

- [ ] `Pool.Acquire` cold path has a race — concurrent Acquire on an empty pool creates N sandboxes instead of coalescing to one. Particularly wasteful on E2B. — Task 7 Step 2 `Acquire` — add `singleflight.Group` or per-template mutex coalescing guard for cold-path creates.

## Nits

- `fakeDockerClient.ContainerExecAttach` returns `bytesBuf` that always yields `(0, io.EOF)` on Read — bypasses `stdcopyDemux` header check; tests never exercise the multiplex logic.
- `realE2BClient` method names assumed without SDK verification — Pre-Task 0 should `go doc github.com/e2b-dev/e2b-go` and confirm actual surface.
- `sandbox.Pool.Shutdown` has no integration with `server/cmd/server/main.go` graceful shutdown path — file structure lists main.go as "Start warm-pool managers" but Task 12 never shows the wiring.

## Verdict

Structural fidelity is strong: all four implementations present, NOT VALID/VALIDATE in up migration, D3 mandatory/optional split correct, GetState() present, Hooks shape matches D3, SandboxTimeoutBufferMs=30_000 wired via D7, Brain/Hands four-tool pattern matches §11.3. Two blockers will produce incorrect runtime behavior: (1) tool-call spans + LateLinkCostEvent specified in Preconditions but never invoked — D8 observability incomplete; (2) warm pool is global rather than per-template — violates §11.4. Plus a down-migration correctness gap and one incomplete "edit each backend" instruction. Substantive edits needed to Tasks 1, 7, 9, 12 before round 2.
