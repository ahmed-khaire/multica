# Gateway Explicit Backend Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Gateway callers route a model request to a specific managed backend with `X-Multica-Backend: <backend-slug>`.

**Architecture:** Keep default routing unchanged. Parse the optional routing header in the proxy service, pass it to the resolver, resolve by slug when present, and then reuse the same compatibility, enabled-state, provider-risk, policy-exception, credential decryption, and telemetry paths as default routing.

**Tech Stack:** Go, Chi handlers, Gateway proxy/resolver, existing sqlc gateway backend queries.

---

### Task 1: Explicit Backend Proxy Test

**Files:**
- Test: `server/internal/handler/gateway_proxy_test.go`
- Modify: `server/internal/gateway/proxy/types.go`
- Modify: `server/internal/gateway/proxy/resolver.go`
- Modify: `server/internal/gateway/proxy/service.go`

- [x] **Step 1: Write failing test**

Add a Gateway proxy integration test that creates two OpenAI-compatible backends, sets backend A as default, sends a request with `X-Multica-Backend: backend-b`, and verifies only backend B receives the upstream call and telemetry records provider slug `backend-b`.

- [x] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/handler -run TestGatewayProxyRoutesToExplicitBackendHeader -count=1`

Expected: fail because the proxy ignores `X-Multica-Backend`.

Actual: local handler tests skipped because Postgres rejected `user=multica`. Added DB-free unit test `TestSummarizeRequestCapturesExplicitBackendHeader` and verified it failed before implementation because `RequestSummary.ExplicitBackendSlug` did not exist.

- [x] **Step 3: Implement minimal routing**

Add `ExplicitBackendSlug` to `RequestSummary`, populate it from `X-Multica-Backend`, change resolver entry point to `ResolveBackend(ctx, workspaceID, protocol, explicitSlug)`, resolve by slug when provided, and keep default backend behavior when the header is absent.

- [x] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/handler -run TestGatewayProxyRoutesToExplicitBackendHeader -count=1`

Expected: pass.

Actual: `cd server && go test ./internal/gateway/proxy -run TestSummarizeRequestCapturesExplicitBackendHeader -count=1` passed. Then reran the handler integration test with `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable'` and it passed.

### Task 2: Governance Regression

**Files:**
- Test: `server/internal/handler/gateway_proxy_test.go`

- [x] **Step 1: Write failing or confirming test**

Add a test that marks an explicitly requested backend as rejected in provider risk and expects the proxy to return `403` with `provider_risk_rejected`.

- [x] **Step 2: Run test**

Run: `cd server && go test ./internal/handler -run TestGatewayProxyBlocksRejectedExplicitBackend -count=1`

Expected after implementation: pass, proving explicit routing does not bypass governance.

Actual: test is implemented and passed with `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable'`.

### Task 3: Verification

- [x] Run `git diff --check`.
- [x] Run `cd server && go test ./internal/gateway/proxy ./internal/handler -run Gateway -count=1`.
- [x] Run `cd server && go test ./...`.

Verification used `DATABASE_URL='postgres://multica:multica@localhost:15433/multica?sslmode=disable'` for handler/database tests.
