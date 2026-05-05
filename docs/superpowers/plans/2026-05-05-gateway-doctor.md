# Gateway Doctor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Gateway Doctor API, CLI command, and Settings UI health panel that explain whether Observer Gateway is ready for enterprise agent traffic.

**Architecture:** Implement a small diagnostic service inside `server/internal/gateway/management` that returns normalized checks grouped by workspace, gateway, backend, policy, observability, and governance. Expose it through `GET /api/gateway/doctor`, consume it from `@multica/core`, render it in Settings -> Gateway, and add `multica gateway doctor`.

**Tech Stack:** Go, Chi, sqlc, Cobra, TypeScript, React, TanStack Query, Vitest.

---

### Task 1: Backend Doctor API

**Files:**
- Modify: `server/internal/gateway/management/types.go`
- Modify: `server/internal/gateway/management/service.go`
- Modify: `server/internal/handler/gateway.go`
- Modify: `server/cmd/server/router.go`
- Test: `server/internal/handler/gateway_test.go`

- [ ] **Step 1: Write failing handler test**

Add `TestGatewayDoctorHandlerReportsHealthyWithWarnings` that creates a gateway key and backend, inserts one open incident, calls `testHandler.GatewayDoctor`, and expects `status=healthy_with_warnings`, a passing `gateway_key` check, a passing `default_backend` check, and a warning `open_incidents` check.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler -run TestGatewayDoctorHandlerReportsHealthyWithWarnings -count=1`

Expected: fail because `GatewayDoctor` is undefined.

- [ ] **Step 3: Implement minimal backend**

Add response types `DoctorResponse` and `DoctorCheck`. Add `Service.Doctor(ctx, workspaceID, userID, serverBaseURL)` that composes checks from existing settings, keys, backends, provider risks, policy decisions, evidence, control mappings, and incidents. Add `Handler.GatewayDoctor` and route `GET /api/gateway/doctor`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/handler -run TestGatewayDoctorHandlerReportsHealthyWithWarnings -count=1`

Expected: pass.

### Task 2: Core Client And Query Binding

**Files:**
- Modify: `packages/core/types/api.ts`
- Modify: `packages/core/api/client.ts`
- Modify: `packages/core/gateway/queries.ts`
- Test: `packages/core/gateway/queries.test.ts`

- [ ] **Step 1: Write failing query test**

Add a test that `gatewayDoctorOptions("ws-1")` uses query key `["gateway","ws-1","doctor"]` and calls `api.getGatewayDoctor({ signal })`.

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/core test -- gateway/queries.test.ts`

Expected: fail because the doctor query binding does not exist.

- [ ] **Step 3: Implement core bindings**

Add `GatewayDoctorResponse`, `GatewayDoctorCheck`, `GatewayDoctorStatus`, and `GatewayDoctorCheckStatus` types. Add `api.getGatewayDoctor`. Add `gatewayKeys.doctor` and `gatewayDoctorOptions`.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/core test -- gateway/queries.test.ts`

Expected: pass.

### Task 3: Gateway Setup Health Panel

**Files:**
- Modify: `packages/views/gateway/components/gateway-page.tsx`
- Test: `packages/views/gateway/components/gateway-page.test.tsx`

- [ ] **Step 1: Write failing UI test**

Add a test that opens Setup, sees `Gateway Health`, sees a `healthy_with_warnings` badge, and verifies `api.getGatewayDoctor` is called only for admins.

- [ ] **Step 2: Run test to verify it fails**

Run: `pnpm --filter @multica/views test -- gateway/components/gateway-page.test.tsx`

Expected: fail because the health panel does not exist.

- [ ] **Step 3: Implement UI panel**

Add `GatewayHealthPanel` near the top of Setup. Render the overall status, grouped check rows, remediation text, and a refresh button using existing cards, badges, tables, and query invalidation patterns.

- [ ] **Step 4: Run test to verify it passes**

Run: `pnpm --filter @multica/views test -- gateway/components/gateway-page.test.tsx`

Expected: pass.

### Task 4: CLI Doctor Command

**Files:**
- Modify: `server/cmd/multica/cmd_gateway.go`
- Test: `server/cmd/multica/cmd_gateway_test.go`

- [ ] **Step 1: Write failing CLI test**

Add a test that `multica gateway doctor --workspace-id workspace-1` calls `GET /api/gateway/doctor` and prints the overall result plus check rows.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./cmd/multica -run TestGatewayDoctorCommand -count=1`

Expected: fail because the command does not exist.

- [ ] **Step 3: Implement command**

Add `doctor` subcommand with `--output table|json`. Table output should show `Observer Gateway Doctor`, `Result: ...`, and one row per check with status, category, title, and remediation.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./cmd/multica -run TestGatewayDoctorCommand -count=1`

Expected: pass.

### Task 5: Verification

- [ ] Run `git diff --check`.
- [ ] Run `cd server && go test ./...`.
- [ ] Run `pnpm --filter @multica/core typecheck`.
- [ ] Run `pnpm --filter @multica/views typecheck`.
- [ ] Run targeted Gateway tests for core and views.
- [ ] Run lint and report any pre-existing unrelated lint debt separately.
