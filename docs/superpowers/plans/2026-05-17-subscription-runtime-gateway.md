# Subscription Runtime Gateway Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add daemon-dispatched Claude Code and Codex subscription backends to Multica Gateway while preserving direct HTTP routing for normal API-key backends.

**Architecture:** Gateway backends gain an explicit transport: `direct_http` for normal provider API keys and `daemon_dispatch` for subscription runtimes. Subscription credentials are stored encrypted as opaque bundles, dispatched to compatible authenticated workspace daemons for validation/install, and used only by validated daemon runtimes. The first implementation builds the shared daemon-dispatch spine and Codex non-streaming chat path; Claude Code Dario-derived runtime behavior follows behind the same interfaces.

**Tech Stack:** Go backend with Chi, sqlc, pgx/Postgres migrations, existing Gateway proxy/management packages, existing daemon polling client, existing `server/pkg/agent/codex.go`, Next.js Gateway UI, Vitest/Go tests.

---

## Scope

This plan is intentionally split into implementation milestones. The first deliverable is a working daemon-dispatch path for subscription runtime validation and non-streaming Codex chat completions. Claude Code's full Dario-derived wire-template behavior is planned as a second provider adapter because it is provider-specific and materially more complex than the shared transport.

## File Map

- `server/migrations/044_gateway_subscription_runtime.up.sql` and `.down.sql`: additive schema for backend transport, subscription credentials, runtime validations, and runtime request queue.
- `server/pkg/db/queries/gateway_backend.sql`: backend and credential create/list/update query extensions.
- `server/pkg/db/queries/gateway_runtime_request.sql`: daemon-dispatch queue queries.
- `server/internal/gateway/management/types.go`: backend constants and response fields.
- `server/internal/gateway/management/service.go`: backend creation/update normalization for direct vs daemon-dispatch backends.
- `server/internal/gateway/proxy/types.go`: `BackendTarget` transport and subscription fields.
- `server/internal/gateway/proxy/resolver.go`: resolve direct HTTP vs daemon-dispatch targets.
- `server/internal/gateway/proxy/service.go`: choose HTTP forwarder or daemon forwarder.
- `server/internal/gateway/proxy/daemon_forwarder.go`: create runtime request, wait for completion, write provider-compatible response.
- `server/internal/gateway/proxy/openai_subscription_response.go`: OpenAI error/response helpers for daemon-dispatch results.
- `server/internal/handler/daemon_gateway.go`: daemon claim/complete/fail endpoints for validation and runtime requests.
- `server/internal/daemon/client.go`: daemon API methods for Gateway jobs.
- `server/internal/daemon/daemon.go`: poll Gateway jobs alongside issue tasks.
- `server/internal/daemon/gateway_jobs.go`: daemon-side job dispatch and provider adapter selection.
- `server/internal/daemon/gateway_codex.go`: Codex non-streaming chat execution adapter.
- `server/internal/daemon/gateway_claude_code.go`: initial stub for Claude Code subscription validation with explicit unsupported execution until Dario port lands.
- `server/cmd/server/router.go`: daemon Gateway job routes.
- `packages/core/types/api.ts`, `packages/core/gateway/queries.ts`, `packages/views/gateway/components/gateway-page.tsx`: UI/API shape for subscription runtime backend creation and status.

## Milestone 1: Schema And Generated Queries

### Task 1: Add Subscription Runtime Schema

**Files:**
- Create: `server/migrations/044_gateway_subscription_runtime.up.sql`
- Create: `server/migrations/044_gateway_subscription_runtime.down.sql`

- [ ] **Step 1: Write the up migration**

Create `server/migrations/044_gateway_subscription_runtime.up.sql`:

```sql
ALTER TABLE gateway_backend
    ADD COLUMN transport TEXT NOT NULL DEFAULT 'direct_http'
        CHECK (transport IN ('direct_http', 'daemon_dispatch')),
    ADD COLUMN subscription_provider TEXT NOT NULL DEFAULT ''
        CHECK (subscription_provider IN ('', 'claude_code', 'codex')),
    ADD COLUMN dispatch_scope TEXT NOT NULL DEFAULT 'workspace_authenticated_daemons'
        CHECK (dispatch_scope IN ('workspace_authenticated_daemons', 'owner_daemons_only', 'selected_daemons')),
    ADD COLUMN validation_status TEXT NOT NULL DEFAULT ''
        CHECK (validation_status IN ('', 'pending_runtime_validation', 'validating_on_runtime', 'active', 'degraded_no_runtime', 'invalid_credentials', 'disabled')),
    ADD COLUMN validated_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    ADD COLUMN last_validation_at TIMESTAMPTZ,
    ADD COLUMN last_validation_error TEXT NOT NULL DEFAULT '';

ALTER TABLE gateway_backend_credential
    ADD COLUMN credential_type TEXT NOT NULL DEFAULT 'api_key'
        CHECK (credential_type IN ('api_key', 'subscription_bundle')),
    ADD COLUMN subscription_provider TEXT NOT NULL DEFAULT ''
        CHECK (subscription_provider IN ('', 'claude_code', 'codex')),
    ADD COLUMN encrypted_payload BYTEA,
    ADD COLUMN payload_format TEXT NOT NULL DEFAULT '',
    ADD COLUMN dispatch_scope TEXT NOT NULL DEFAULT 'workspace_authenticated_daemons'
        CHECK (dispatch_scope IN ('workspace_authenticated_daemons', 'owner_daemons_only', 'selected_daemons')),
    ADD COLUMN validation_status TEXT NOT NULL DEFAULT ''
        CHECK (validation_status IN ('', 'pending_runtime_validation', 'validating_on_runtime', 'active', 'degraded_no_runtime', 'invalid_credentials', 'disabled')),
    ADD COLUMN validated_runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    ADD COLUMN account_hint TEXT NOT NULL DEFAULT '',
    ADD COLUMN account_fingerprint TEXT NOT NULL DEFAULT '',
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN refreshable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN last_validation_at TIMESTAMPTZ,
    ADD COLUMN last_validation_error TEXT NOT NULL DEFAULT '';

CREATE TABLE gateway_subscription_runtime_validation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID NOT NULL REFERENCES gateway_backend(id) ON DELETE CASCADE,
    credential_id UUID NOT NULL REFERENCES gateway_backend_credential(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    provider TEXT NOT NULL CHECK (provider IN ('claude_code', 'codex')),
    account_hint TEXT NOT NULL DEFAULT '',
    account_fingerprint TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_subscription_validation_claim
    ON gateway_subscription_runtime_validation(workspace_id, provider, status, created_at);

CREATE UNIQUE INDEX idx_gateway_subscription_validation_runtime
    ON gateway_subscription_runtime_validation(credential_id, runtime_id);

CREATE TABLE gateway_runtime_request (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    backend_id UUID NOT NULL REFERENCES gateway_backend(id) ON DELETE CASCADE,
    credential_id UUID REFERENCES gateway_backend_credential(id) ON DELETE SET NULL,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    provider TEXT NOT NULL CHECK (provider IN ('claude_code', 'codex')),
    surface TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'dispatched', 'running', 'completed', 'failed', 'timeout')),
    request_body JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_body JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_type TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    stream BOOLEAN NOT NULL DEFAULT false,
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gateway_runtime_request_claim
    ON gateway_runtime_request(workspace_id, provider, status, created_at);

CREATE INDEX idx_gateway_runtime_request_wait
    ON gateway_runtime_request(workspace_id, id, status);
```

- [ ] **Step 2: Write the down migration**

Create `server/migrations/044_gateway_subscription_runtime.down.sql`:

```sql
DROP INDEX IF EXISTS idx_gateway_runtime_request_wait;
DROP INDEX IF EXISTS idx_gateway_runtime_request_claim;
DROP TABLE IF EXISTS gateway_runtime_request;

DROP INDEX IF EXISTS idx_gateway_subscription_validation_runtime;
DROP INDEX IF EXISTS idx_gateway_subscription_validation_claim;
DROP TABLE IF EXISTS gateway_subscription_runtime_validation;

ALTER TABLE gateway_backend_credential
    DROP COLUMN IF EXISTS last_validation_error,
    DROP COLUMN IF EXISTS last_validation_at,
    DROP COLUMN IF EXISTS refreshable,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS account_fingerprint,
    DROP COLUMN IF EXISTS account_hint,
    DROP COLUMN IF EXISTS validated_runtime_id,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS dispatch_scope,
    DROP COLUMN IF EXISTS payload_format,
    DROP COLUMN IF EXISTS encrypted_payload,
    DROP COLUMN IF EXISTS subscription_provider,
    DROP COLUMN IF EXISTS credential_type;

ALTER TABLE gateway_backend
    DROP COLUMN IF EXISTS last_validation_error,
    DROP COLUMN IF EXISTS last_validation_at,
    DROP COLUMN IF EXISTS validated_runtime_id,
    DROP COLUMN IF EXISTS validation_status,
    DROP COLUMN IF EXISTS dispatch_scope,
    DROP COLUMN IF EXISTS subscription_provider,
    DROP COLUMN IF EXISTS transport;
```

- [ ] **Step 3: Run migration validation**

Run:

```bash
make migrate-up
```

Expected: migrations apply through version `044`.

Run:

```bash
make migrate-down
make migrate-up
```

Expected: migration rolls back and reapplies cleanly.

- [ ] **Step 4: Commit**

```bash
git add server/migrations/044_gateway_subscription_runtime.up.sql server/migrations/044_gateway_subscription_runtime.down.sql
git commit -m "feat(gateway): add subscription runtime schema"
```

### Task 2: Add sqlc Queries For Runtime Dispatch

**Files:**
- Modify: `server/pkg/db/queries/gateway_backend.sql`
- Create: `server/pkg/db/queries/gateway_runtime_request.sql`
- Generated: `server/pkg/db/generated/*.go`

- [ ] **Step 1: Add query file**

Create `server/pkg/db/queries/gateway_runtime_request.sql`:

```sql
-- name: CreateGatewaySubscriptionValidation :one
INSERT INTO gateway_subscription_runtime_validation (
    workspace_id, backend_id, credential_id, runtime_id, status, provider
)
VALUES ($1, $2, $3, $4, 'pending', $5)
ON CONFLICT (credential_id, runtime_id)
DO UPDATE SET
    status = 'pending',
    error_code = '',
    error_message = '',
    started_at = NULL,
    completed_at = NULL,
    updated_at = now()
RETURNING *;

-- name: ClaimGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation v
SET status = 'running', started_at = now(), updated_at = now()
WHERE v.id = (
    SELECT v2.id
    FROM gateway_subscription_runtime_validation v2
    JOIN agent_runtime ar ON ar.id = v2.runtime_id
    WHERE v2.workspace_id = $1
      AND v2.runtime_id = $2
      AND v2.status = 'pending'
      AND ar.status = 'online'
    ORDER BY v2.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation
SET
    status = 'succeeded',
    account_hint = $4,
    account_fingerprint = $5,
    error_code = '',
    error_message = '',
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND runtime_id = $3
RETURNING *;

-- name: FailGatewaySubscriptionValidation :one
UPDATE gateway_subscription_runtime_validation
SET
    status = 'failed',
    error_code = $4,
    error_message = $5,
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND runtime_id = $3
RETURNING *;

-- name: ListValidatedSubscriptionRuntimes :many
SELECT ar.*
FROM gateway_subscription_runtime_validation v
JOIN agent_runtime ar ON ar.id = v.runtime_id
JOIN workspace_member wm ON wm.workspace_id = ar.workspace_id AND wm.user_id = ar.owner_id
WHERE v.workspace_id = $1
  AND v.backend_id = $2
  AND v.credential_id = $3
  AND v.provider = $4
  AND v.status = 'succeeded'
  AND ar.status = 'online'
ORDER BY ar.last_seen_at DESC;

-- name: CreateGatewayRuntimeRequest :one
INSERT INTO gateway_runtime_request (
    workspace_id, backend_id, credential_id, runtime_id, provider, surface, status,
    request_body, stream
)
VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $8)
RETURNING *;

-- name: ClaimGatewayRuntimeRequest :one
UPDATE gateway_runtime_request r
SET status = 'running', claimed_at = now(), updated_at = now()
WHERE r.id = (
    SELECT r2.id
    FROM gateway_runtime_request r2
    WHERE r2.workspace_id = $1
      AND r2.runtime_id = $2
      AND r2.status = 'queued'
    ORDER BY r2.created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteGatewayRuntimeRequest :one
UPDATE gateway_runtime_request
SET
    status = 'completed',
    response_body = $4,
    error_type = '',
    error_message = '',
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND runtime_id = $3
RETURNING *;

-- name: FailGatewayRuntimeRequest :one
UPDATE gateway_runtime_request
SET
    status = 'failed',
    error_type = $4,
    error_message = $5,
    completed_at = now(),
    updated_at = now()
WHERE workspace_id = $1 AND id = $2 AND runtime_id = $3
RETURNING *;

-- name: GetGatewayRuntimeRequest :one
SELECT * FROM gateway_runtime_request
WHERE workspace_id = $1 AND id = $2;
```

- [ ] **Step 2: Extend backend queries**

Modify `server/pkg/db/queries/gateway_backend.sql` so backend and credential create/update/list queries include the new columns. Preserve existing query names when possible so call sites keep compiling.

- [ ] **Step 3: Regenerate sqlc**

Run:

```bash
make sqlc
```

Expected: generated Go files update without sqlc errors.

- [ ] **Step 4: Compile generated usage**

Run:

```bash
cd server && go test ./pkg/db/generated
```

Expected: package compiles.

- [ ] **Step 5: Commit**

```bash
git add server/pkg/db/queries/gateway_backend.sql server/pkg/db/queries/gateway_runtime_request.sql server/pkg/db/generated
git commit -m "feat(gateway): add runtime dispatch queries"
```

## Milestone 2: Gateway Management And Routing Types

### Task 3: Add Transport Constants And Response Fields

**Files:**
- Modify: `server/internal/gateway/management/types.go`
- Modify: `server/internal/gateway/proxy/types.go`
- Test: `server/internal/gateway/management/service_test.go`
- Test: `server/internal/gateway/proxy/types_test.go`

- [ ] **Step 1: Write proxy type test**

Create or extend `server/internal/gateway/proxy/types_test.go`:

```go
package proxy

import "testing"

func TestGatewayTransportConstants(t *testing.T) {
	if TransportDirectHTTP != "direct_http" {
		t.Fatalf("TransportDirectHTTP = %q", TransportDirectHTTP)
	}
	if TransportDaemonDispatch != "daemon_dispatch" {
		t.Fatalf("TransportDaemonDispatch = %q", TransportDaemonDispatch)
	}
}

func TestBackendTargetSubscriptionFieldsZeroValue(t *testing.T) {
	var target BackendTarget
	if target.Transport != "" || target.SubscriptionProvider != "" || target.DispatchScope != "" {
		t.Fatalf("unexpected zero values: %+v", target)
	}
}
```

- [ ] **Step 2: Add constants and fields**

In `server/internal/gateway/proxy/types.go`, add:

```go
const (
	TransportDirectHTTP      = "direct_http"
	TransportDaemonDispatch = "daemon_dispatch"

	CredentialTypeAPIKey             = "api_key"
	CredentialTypeSubscriptionBundle = "subscription_bundle"

	SubscriptionProviderClaudeCode = "claude_code"
	SubscriptionProviderCodex      = "codex"

	DispatchScopeWorkspaceAuthenticatedDaemons = "workspace_authenticated_daemons"
)
```

Extend `BackendTarget`:

```go
type BackendTarget struct {
	ID                   string
	Slug                 string
	BackendType          string
	CredentialID         string
	UpstreamProtocol     string
	BaseURL              string
	UpstreamSecret       string
	CredentialType       string
	Transport            string
	SubscriptionProvider string
	DispatchScope        string
	CapturePolicy        string
	PolicyExceptionID    string
}
```

- [ ] **Step 3: Add management constants**

In `server/internal/gateway/management/types.go`, add:

```go
const (
	BackendTypeSubscriptionRuntime = "subscription_runtime"

	TransportDirectHTTP      = "direct_http"
	TransportDaemonDispatch = "daemon_dispatch"

	CredentialTypeAPIKey             = "api_key"
	CredentialTypeSubscriptionBundle = "subscription_bundle"

	SubscriptionProviderClaudeCode = "claude_code"
	SubscriptionProviderCodex      = "codex"

	DispatchScopeWorkspaceAuthenticatedDaemons = "workspace_authenticated_daemons"
)
```

Extend `BackendResponse` and `BackendCredentialResponse` with JSON fields:

```go
Transport            string  `json:"transport"`
CredentialType       string  `json:"credential_type,omitempty"`
SubscriptionProvider string  `json:"subscription_provider,omitempty"`
DispatchScope        string  `json:"dispatch_scope,omitempty"`
ValidationStatus     string  `json:"validation_status,omitempty"`
ValidatedRuntimeID   string  `json:"validated_runtime_id,omitempty"`
LastValidationAt     *string `json:"last_validation_at,omitempty"`
LastValidationError  string  `json:"last_validation_error,omitempty"`
AccountHint          string  `json:"account_hint,omitempty"`
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy ./internal/gateway/management
```

Expected: package tests pass or fail only where later tasks must update query result mappings.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/management/types.go server/internal/gateway/proxy/types.go server/internal/gateway/proxy/types_test.go server/internal/gateway/management/service_test.go
git commit -m "feat(gateway): add subscription runtime type fields"
```

### Task 4: Add Provider Presets

**Files:**
- Modify: `server/internal/gateway/management/types.go`
- Modify: `packages/views/gateway/components/gateway-page.tsx`
- Test: `server/internal/gateway/management/service_test.go`

- [ ] **Step 1: Update preset test**

Add cases in `server/internal/gateway/management/service_test.go` for:

```go
{
	name: "claude-code-subscription",
	provider: "claude-code-subscription",
	slug: "claude-code-subscription",
	displayName: "Claude Code Subscription",
	backendType: BackendTypeSubscriptionRuntime,
	baseURL: "daemon://claude-code",
	requiresCredential: true,
},
{
	name: "codex-subscription",
	provider: "codex-subscription",
	slug: "codex-subscription",
	displayName: "Codex Subscription",
	backendType: BackendTypeSubscriptionRuntime,
	baseURL: "daemon://codex",
	requiresCredential: true,
},
```

- [ ] **Step 2: Add presets**

Add to `providerPresets`:

```go
"claude-code-subscription": {
	Provider:           "claude-code-subscription",
	Slug:               "claude-code-subscription",
	DisplayName:        "Claude Code Subscription",
	BackendType:        BackendTypeSubscriptionRuntime,
	BaseURL:            "daemon://claude-code",
	RequiresCredential: true,
},
"codex-subscription": {
	Provider:           "codex-subscription",
	Slug:               "codex-subscription",
	DisplayName:        "Codex Subscription",
	BackendType:        BackendTypeSubscriptionRuntime,
	BaseURL:            "daemon://codex",
	RequiresCredential: true,
},
```

- [ ] **Step 3: Allow daemon URLs**

Update `validateBackendURL` in `server/internal/gateway/management/service.go`:

```go
if backendType == BackendTypeSubscriptionRuntime && parsed.Scheme == "daemon" {
	return nil
}
```

- [ ] **Step 4: Update UI provider list**

In `packages/views/gateway/components/gateway-page.tsx`, add provider options:

```tsx
{ label: "Claude Code Subscription", value: "claude-code-subscription", baseUrl: "daemon://claude-code" },
{ label: "Codex Subscription", value: "codex-subscription", baseUrl: "daemon://codex" },
```

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/gateway/management -run TestProviderPresetFor -v
pnpm --filter @multica/web exec vitest run packages/views/gateway/components/gateway-page.test.tsx
```

Expected: provider preset and Gateway page tests pass.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/management/types.go server/internal/gateway/management/service.go server/internal/gateway/management/service_test.go packages/views/gateway/components/gateway-page.tsx packages/views/gateway/components/gateway-page.test.tsx
git commit -m "feat(gateway): add subscription runtime backend presets"
```

## Milestone 3: Backend Creation And Credential Escrow

### Task 5: Normalize Subscription Runtime Backends

**Files:**
- Modify: `server/internal/gateway/management/service.go`
- Modify: `server/internal/handler/gateway.go`
- Test: `server/internal/handler/gateway_test.go`

- [ ] **Step 1: Add request fields**

Extend `gatewayCreateBackendRequest` and `gatewayCreateBackendCredentialRequest`:

```go
Transport            string `json:"transport"`
CredentialType       string `json:"credential_type"`
SubscriptionProvider string `json:"subscription_provider"`
DispatchScope        string `json:"dispatch_scope"`
PayloadFormat        string `json:"payload_format"`
```

- [ ] **Step 2: Add creation test**

In `server/internal/handler/gateway_test.go`, add a test that posts:

```json
{
  "provider": "codex-subscription",
  "slug": "codex-subscription",
  "display_name": "Codex Subscription",
  "backend_type": "subscription_runtime",
  "base_url": "daemon://codex",
  "key": "{\"kind\":\"codex-auth-bundle\",\"test\":true}",
  "transport": "daemon_dispatch",
  "credential_type": "subscription_bundle",
  "subscription_provider": "codex",
  "dispatch_scope": "workspace_authenticated_daemons"
}
```

Expected response fields:

```go
if resp.Transport != "daemon_dispatch" {
	t.Fatalf("Transport = %q, want daemon_dispatch", resp.Transport)
}
if resp.SubscriptionProvider != "codex" {
	t.Fatalf("SubscriptionProvider = %q, want codex", resp.SubscriptionProvider)
}
if resp.ValidationStatus != "pending_runtime_validation" {
	t.Fatalf("ValidationStatus = %q, want pending_runtime_validation", resp.ValidationStatus)
}
```

- [ ] **Step 3: Implement normalization**

In management normalization, for `BackendTypeSubscriptionRuntime`:

```go
normalized.Transport = TransportDaemonDispatch
normalized.CredentialType = CredentialTypeSubscriptionBundle
normalized.DispatchScope = defaultString(normalized.DispatchScope, DispatchScopeWorkspaceAuthenticatedDaemons)
normalized.ValidationStatus = "pending_runtime_validation"
```

Map provider to subscription provider:

```go
switch normalized.Provider {
case "claude-code-subscription":
	normalized.SubscriptionProvider = SubscriptionProviderClaudeCode
case "codex-subscription":
	normalized.SubscriptionProvider = SubscriptionProviderCodex
}
```

- [ ] **Step 4: Preserve direct HTTP behavior**

For non-subscription backends:

```go
normalized.Transport = defaultString(normalized.Transport, TransportDirectHTTP)
normalized.CredentialType = CredentialTypeAPIKey
```

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/handler -run 'TestGateway.*Backend' -v
```

Expected: existing API-key backend tests continue passing; new subscription backend creation test passes.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/management/service.go server/internal/handler/gateway.go server/internal/handler/gateway_test.go
git commit -m "feat(gateway): create subscription runtime backends"
```

### Task 6: Audit Credential Dispatch Eligibility

**Files:**
- Create: `server/internal/gateway/proxy/runtime_selector.go`
- Test: `server/internal/gateway/proxy/runtime_selector_test.go`

- [ ] **Step 1: Write selector tests**

Create `server/internal/gateway/proxy/runtime_selector_test.go`:

```go
package proxy

import "testing"

func TestRuntimeProviderForSubscriptionProvider(t *testing.T) {
	tests := map[string]string{
		SubscriptionProviderCodex: "codex",
		SubscriptionProviderClaudeCode: "claude",
	}
	for input, want := range tests {
		if got := RuntimeProviderForSubscriptionProvider(input); got != want {
			t.Fatalf("RuntimeProviderForSubscriptionProvider(%q) = %q, want %q", input, got, want)
		}
	}
}
```

- [ ] **Step 2: Implement helper**

Create `server/internal/gateway/proxy/runtime_selector.go`:

```go
package proxy

func RuntimeProviderForSubscriptionProvider(provider string) string {
	switch provider {
	case SubscriptionProviderCodex:
		return "codex"
	case SubscriptionProviderClaudeCode:
		return "claude"
	default:
		return ""
	}
}
```

- [ ] **Step 3: Run test**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run TestRuntimeProviderForSubscriptionProvider -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/internal/gateway/proxy/runtime_selector.go server/internal/gateway/proxy/runtime_selector_test.go
git commit -m "feat(gateway): map subscription providers to runtimes"
```

## Milestone 4: Daemon Dispatch Endpoints

### Task 7: Add Daemon Gateway Job Handler

**Files:**
- Create: `server/internal/handler/daemon_gateway.go`
- Modify: `server/cmd/server/router.go`
- Test: `server/internal/handler/daemon_gateway_test.go`

- [ ] **Step 1: Add route skeleton**

In `server/cmd/server/router.go` under `/api/daemon`:

```go
r.Post("/runtimes/{runtimeId}/gateway/jobs/claim", h.ClaimGatewayJobByRuntime)
r.Post("/runtimes/{runtimeId}/gateway/validations/{validationId}/complete", h.CompleteGatewayValidation)
r.Post("/runtimes/{runtimeId}/gateway/validations/{validationId}/fail", h.FailGatewayValidation)
r.Post("/runtimes/{runtimeId}/gateway/requests/{requestId}/complete", h.CompleteGatewayRuntimeRequest)
r.Post("/runtimes/{runtimeId}/gateway/requests/{requestId}/fail", h.FailGatewayRuntimeRequest)
```

- [ ] **Step 2: Implement claim response types**

Create `server/internal/handler/daemon_gateway.go` with:

```go
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type daemonGatewayJobResponse struct {
	Job *daemonGatewayJob `json:"job"`
}

type daemonGatewayJob struct {
	ID                   string         `json:"id"`
	Type                 string         `json:"type"`
	WorkspaceID          string         `json:"workspace_id"`
	BackendID            string         `json:"backend_id"`
	CredentialID         string         `json:"credential_id"`
	SubscriptionProvider string         `json:"subscription_provider"`
	Surface              string         `json:"surface,omitempty"`
	RequestBody          map[string]any `json:"request_body,omitempty"`
	EncryptedPayload     string         `json:"encrypted_payload,omitempty"`
	PayloadFormat        string         `json:"payload_format,omitempty"`
}
```

- [ ] **Step 3: Implement claim logic**

`ClaimGatewayJobByRuntime` should:

1. Load runtime by ID.
2. Require runtime belongs to caller's workspace membership.
3. Try `ClaimGatewaySubscriptionValidation`.
4. If none, try `ClaimGatewayRuntimeRequest`.
5. Return `{"job": null}` when no job exists.

Keep credential payload return empty until Task 8 wires decryption.

- [ ] **Step 4: Add tests**

In `server/internal/handler/daemon_gateway_test.go`, add these test functions:

```go
func TestClaimGatewayJobNoPendingReturnsNull(t *testing.T) {
	req := newRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/gateway/jobs/claim", map[string]any{})
	rr := executeRequest(req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp daemonGatewayJobResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Job != nil {
		t.Fatalf("Job = %+v, want nil", resp.Job)
	}
}

func TestClaimGatewayJobRejectsOtherWorkspaceRuntime(t *testing.T) {
	req := newRequest(http.MethodPost, "/api/daemon/runtimes/"+otherWorkspaceRuntimeID+"/gateway/jobs/claim", map[string]any{})
	rr := executeRequest(req)
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 or 404: %s", rr.Code, rr.Body.String())
	}
}

func TestClaimGatewayJobPrefersPendingValidation(t *testing.T) {
	seedPendingGatewayValidation(t, runtimeID)
	seedPendingGatewayRuntimeRequest(t, runtimeID)
	req := newRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/gateway/jobs/claim", map[string]any{})
	rr := executeRequest(req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp daemonGatewayJobResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Job == nil || resp.Job.Type != "gateway_subscription_validation" {
		t.Fatalf("job = %+v, want validation job", resp.Job)
	}
}
```

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/handler -run TestDaemonGateway -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/handler/daemon_gateway.go server/internal/handler/daemon_gateway_test.go server/cmd/server/router.go
git commit -m "feat(daemon): add gateway job claim endpoints"
```

### Task 8: Dispatch Encrypted Subscription Bundle To Daemon

**Files:**
- Modify: `server/internal/handler/daemon_gateway.go`
- Test: `server/internal/handler/daemon_gateway_test.go`

- [ ] **Step 1: Add encrypted payload to claim response**

When a validation job is claimed, load the credential row and return:

```go
EncryptedPayload: base64.StdEncoding.EncodeToString(credential.EncryptedPayload),
PayloadFormat: credential.PayloadFormat,
```

The daemon will POST it back to server-side decrypt/install logic in a later hardening pass, or decrypt locally only if the payload is intentionally transport-encrypted for that daemon. For v1 planning, this is server-encrypted escrow material delivered over authenticated TLS.

- [ ] **Step 2: Add audit event**

On claim, write an audit row:

```text
action = gateway.subscription_credential.dispatched
target_type = gateway_backend_credential
target_id = credential_id
after_state = {runtime_id, daemon_id, subscription_provider}
```

- [ ] **Step 3: Test audit**

Extend daemon Gateway tests to assert an audit row exists after claim.

- [ ] **Step 4: Run tests**

Run:

```bash
cd server && go test ./internal/handler -run TestDaemonGateway -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/daemon_gateway.go server/internal/handler/daemon_gateway_test.go
git commit -m "feat(gateway): audit subscription credential dispatch"
```

## Milestone 5: Gateway Daemon Forwarder

### Task 9: Resolve Daemon-Dispatch Backend Targets

**Files:**
- Modify: `server/internal/gateway/proxy/resolver.go`
- Test: `server/internal/gateway/proxy/resolver_test.go`

- [ ] **Step 1: Add resolver test**

Add a test that seeds a backend with:

```text
backend_type=subscription_runtime
transport=daemon_dispatch
subscription_provider=codex
credential_type=subscription_bundle
validation_status=active
```

Expected `ResolveBackend` returns:

```go
target.Transport == TransportDaemonDispatch
target.SubscriptionProvider == SubscriptionProviderCodex
target.CredentialType == CredentialTypeSubscriptionBundle
```

- [ ] **Step 2: Update resolver**

Populate new `BackendTarget` fields from backend and credential rows. For direct HTTP rows, default missing transport to `direct_http` and credential type to `api_key`.

- [ ] **Step 3: Keep direct HTTP unchanged**

Add/update a test proving OpenAI API-key backends still decrypt `UpstreamSecret` and set `TransportDirectHTTP`.

- [ ] **Step 4: Run tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run TestResolver -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/gateway/proxy/resolver.go server/internal/gateway/proxy/resolver_test.go
git commit -m "feat(gateway): resolve daemon dispatch backends"
```

### Task 10: Implement Daemon Forwarder

**Files:**
- Create: `server/internal/gateway/proxy/daemon_forwarder.go`
- Modify: `server/internal/gateway/proxy/service.go`
- Test: `server/internal/gateway/proxy/daemon_forwarder_test.go`

- [ ] **Step 1: Add daemon forwarder type**

Create:

```go
package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type DaemonForwarder struct {
	Queries  *db.Queries
	Timeout  time.Duration
	PollStep time.Duration
}

func NewDaemonForwarder(queries *db.Queries) *DaemonForwarder {
	return &DaemonForwarder{
		Queries: queries,
		Timeout: 2 * time.Minute,
		PollStep: 250 * time.Millisecond,
	}
}
```

- [ ] **Step 2: Implement `Forward`**

`Forward` should:

1. Find validated runtimes with `ListValidatedSubscriptionRuntimes`.
2. Return provider-compatible 503 if none.
3. Create `gateway_runtime_request`.
4. Poll `GetGatewayRuntimeRequest` until completed/failed/timeout/context done.
5. Write `response_body` as JSON for completed requests.
6. Write OpenAI-shaped or Anthropic-shaped error for failed/timeout.

- [ ] **Step 3: Wire service**

In `server/internal/gateway/proxy/service.go`, add `DaemonForwarder` to `Service` and route:

```go
if target.Transport == TransportDaemonDispatch {
	result, err = s.DaemonForwarder.Forward(r.Context(), w, r, target, summary)
} else {
	result, err = s.Forwarder.Forward(r.Context(), w, r, target, summary)
}
```

- [ ] **Step 4: Add tests**

Add these test functions to `server/internal/gateway/proxy/daemon_forwarder_test.go`:

```go
func TestDaemonForwarderNoValidatedRuntimeReturns503(t *testing.T) {
	forwarder := NewDaemonForwarder(testQueries)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"codex:gpt-5.3-codex","messages":[]}`))
	rr := httptest.NewRecorder()
	result, err := forwarder.Forward(context.Background(), rr, req, codexDaemonTarget(), codexSummary())
	if err == nil {
		t.Fatal("expected error")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if result.Status != StatusGatewayError {
		t.Fatalf("result status = %q, want gateway_error", result.Status)
	}
}

func TestDaemonForwarderWritesCompletedResponse(t *testing.T) {
	runtimeID := seedValidatedCodexRuntime(t)
	forwarder := NewDaemonForwarder(testQueries)
	forwarder.PollStep = time.Millisecond
	go completeNextGatewayRuntimeRequest(t, runtimeID, map[string]any{"id": "chatcmpl-test", "object": "chat.completion"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"codex:gpt-5.3-codex","messages":[]}`))
	rr := httptest.NewRecorder()
	result, err := forwarder.Forward(context.Background(), rr, req, codexDaemonTarget(), codexSummary())
	if err != nil {
		t.Fatalf("Forward error: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if result.Status != StatusSuccess {
		t.Fatalf("result status = %q, want success", result.Status)
	}
}

func TestDaemonForwarderWritesFailedResponse(t *testing.T) {
	runtimeID := seedValidatedCodexRuntime(t)
	forwarder := NewDaemonForwarder(testQueries)
	forwarder.PollStep = time.Millisecond
	go failNextGatewayRuntimeRequest(t, runtimeID, "runtime_error", "codex failed")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"codex:gpt-5.3-codex","messages":[]}`))
	rr := httptest.NewRecorder()
	result, err := forwarder.Forward(context.Background(), rr, req, codexDaemonTarget(), codexSummary())
	if err == nil {
		t.Fatal("expected error")
	}
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	if result.ErrorType != "runtime_error" {
		t.Fatalf("error type = %q, want runtime_error", result.ErrorType)
	}
}

func TestDaemonForwarderTimeout(t *testing.T) {
	seedValidatedCodexRuntime(t)
	forwarder := NewDaemonForwarder(testQueries)
	forwarder.Timeout = time.Millisecond
	forwarder.PollStep = time.Millisecond
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"codex:gpt-5.3-codex","messages":[]}`))
	rr := httptest.NewRecorder()
	result, err := forwarder.Forward(context.Background(), rr, req, codexDaemonTarget(), codexSummary())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if result.Status != StatusGatewayError {
		t.Fatalf("result status = %q, want gateway_error", result.Status)
	}
}
```

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run TestDaemonForwarder -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/gateway/proxy/daemon_forwarder.go server/internal/gateway/proxy/service.go server/internal/gateway/proxy/daemon_forwarder_test.go
git commit -m "feat(gateway): forward subscription requests through daemons"
```

## Milestone 6: Daemon Client And Codex Adapter

### Task 11: Add Daemon Client Methods

**Files:**
- Modify: `server/internal/daemon/types.go`
- Modify: `server/internal/daemon/client.go`
- Test: `server/internal/daemon/client_test.go`

- [ ] **Step 1: Add types**

Add:

```go
type GatewayJob struct {
	ID                   string         `json:"id"`
	Type                 string         `json:"type"`
	WorkspaceID          string         `json:"workspace_id"`
	BackendID            string         `json:"backend_id"`
	CredentialID         string         `json:"credential_id"`
	SubscriptionProvider string         `json:"subscription_provider"`
	Surface              string         `json:"surface,omitempty"`
	RequestBody          map[string]any `json:"request_body,omitempty"`
	EncryptedPayload     string         `json:"encrypted_payload,omitempty"`
	PayloadFormat        string         `json:"payload_format,omitempty"`
}

type GatewayValidationResult struct {
	AccountHint        string `json:"account_hint"`
	AccountFingerprint string `json:"account_fingerprint"`
}
```

- [ ] **Step 2: Add client methods**

Implement:

```go
func (c *Client) ClaimGatewayJob(ctx context.Context, runtimeID string) (*GatewayJob, error)
func (c *Client) CompleteGatewayValidation(ctx context.Context, runtimeID, validationID string, result GatewayValidationResult) error
func (c *Client) FailGatewayValidation(ctx context.Context, runtimeID, validationID, code, message string) error
func (c *Client) CompleteGatewayRuntimeRequest(ctx context.Context, runtimeID, requestID string, response map[string]any) error
func (c *Client) FailGatewayRuntimeRequest(ctx context.Context, runtimeID, requestID, typ, message string) error
```

- [ ] **Step 3: Add HTTP client tests**

Use `httptest.Server` to assert each method sends the expected path and JSON body.

- [ ] **Step 4: Run tests**

Run:

```bash
cd server && go test ./internal/daemon -run TestClientGateway -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/daemon/types.go server/internal/daemon/client.go server/internal/daemon/client_test.go
git commit -m "feat(daemon): add gateway job client methods"
```

### Task 12: Poll Gateway Jobs

**Files:**
- Modify: `server/internal/daemon/daemon.go`
- Create: `server/internal/daemon/gateway_jobs.go`
- Test: `server/internal/daemon/daemon_test.go`

- [ ] **Step 1: Add polling loop**

In `Run`, start:

```go
go d.gatewayJobPollLoop(ctx)
```

Implement `gatewayJobPollLoop` to iterate runtime IDs, call `ClaimGatewayJob`, and dispatch non-nil jobs.

- [ ] **Step 2: Add dispatch skeleton**

Create `server/internal/daemon/gateway_jobs.go`:

```go
package daemon

import (
	"context"
	"fmt"
)

func (d *Daemon) handleGatewayJob(ctx context.Context, runtimeID string, job *GatewayJob) {
	switch job.Type {
	case "gateway_subscription_validation":
		d.handleGatewayValidationJob(ctx, runtimeID, job)
	case "gateway_runtime_request":
		d.handleGatewayRuntimeRequestJob(ctx, runtimeID, job)
	default:
		d.logger.Warn("unknown gateway job type", "type", job.Type, "job_id", job.ID)
	}
}

func (d *Daemon) handleGatewayValidationJob(ctx context.Context, runtimeID string, job *GatewayJob) {
	err := d.validateGatewaySubscription(ctx, runtimeID, job)
	if err != nil {
		_ = d.client.FailGatewayValidation(ctx, runtimeID, job.ID, "validation_failed", err.Error())
		return
	}
	_ = d.client.CompleteGatewayValidation(ctx, runtimeID, job.ID, GatewayValidationResult{
		AccountHint: job.SubscriptionProvider,
		AccountFingerprint: fmt.Sprintf("%s:%s", job.SubscriptionProvider, runtimeID),
	})
}
```

- [ ] **Step 3: Add no-op provider validation**

`validateGatewaySubscription` should return nil for `codex` when runtime provider is `codex`, and return a clear error for unsupported provider/runtime mismatches.

- [ ] **Step 4: Run tests**

Run:

```bash
cd server && go test ./internal/daemon -run TestDaemonGateway -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/daemon/daemon.go server/internal/daemon/gateway_jobs.go server/internal/daemon/daemon_test.go
git commit -m "feat(daemon): poll and dispatch gateway jobs"
```

### Task 13: Implement Codex Non-Streaming Chat Adapter

**Files:**
- Create: `server/internal/daemon/gateway_codex.go`
- Test: `server/internal/daemon/gateway_codex_test.go`

- [ ] **Step 1: Add prompt builder test**

Test:

```go
func TestOpenAIChatToCodexPrompt(t *testing.T) {
	body := map[string]any{
		"model": "codex:gpt-5.3-codex",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are precise."},
			map[string]any{"role": "user", "content": "Say hi."},
		},
	}
	got, err := openAIChatToCodexPrompt(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "System:\nYou are precise.") {
		t.Fatalf("prompt missing system: %s", got)
	}
	if !strings.Contains(got, "User:\nSay hi.") {
		t.Fatalf("prompt missing user: %s", got)
	}
}
```

- [ ] **Step 2: Implement prompt conversion**

Support only string content and text content blocks. Return a typed error for tools, images, response_format, or stream=true.

- [ ] **Step 3: Add response mapper**

Implement:

```go
func codexOutputToOpenAIChat(model, output string) map[string]any
```

Return:

```json
{
  "id": "chatcmpl-multica-123",
  "object": "chat.completion",
  "created": 123,
  "model": "gpt-5.3-codex",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "multica-runtime-ok"},
      "finish_reason": "stop"
    }
  ]
}
```

- [ ] **Step 4: Execute with existing Codex backend**

In `handleGatewayRuntimeRequestJob`, for `SubscriptionProviderCodex`:

1. Find runtime entry.
2. Convert request to prompt.
3. Create the backend with `agent.New("codex", agent.Config{ExecutablePath: entry.Path, Env: map[string]string{}, Logger: d.logger})`.
4. Execute with `ExecOptions{Model: model, Timeout: d.cfg.AgentTimeout}`.
5. Complete runtime request with OpenAI response map.

- [ ] **Step 5: Run tests**

Run:

```bash
cd server && go test ./internal/daemon -run TestOpenAIChatToCodexPrompt -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/daemon/gateway_codex.go server/internal/daemon/gateway_codex_test.go server/internal/daemon/gateway_jobs.go
git commit -m "feat(daemon): execute codex gateway chat requests"
```

## Milestone 7: Validation Scheduling

### Task 14: Schedule Validation When Subscription Backend Is Created

**Files:**
- Modify: `server/internal/gateway/management/service.go`
- Test: `server/internal/gateway/management/integration_test.go`

- [ ] **Step 1: Add integration test**

Seed:

- workspace
- online `agent_runtime` with provider `codex`
- create codex subscription backend

Assert:

- backend `validation_status = pending_runtime_validation`
- one `gateway_subscription_runtime_validation` row exists for the online runtime

- [ ] **Step 2: Implement scheduler**

After creating the backend and subscription credential, call a helper:

```go
func (s *Service) scheduleSubscriptionValidation(ctx context.Context, q *db.Queries, backend db.GatewayBackend, credential db.GatewayBackendCredential) error
```

Find eligible online runtimes by workspace/provider. Insert validation jobs for at least one runtime; if none exists, leave backend pending.

- [ ] **Step 3: Run integration test**

Run:

```bash
cd server && go test ./internal/gateway/management -run TestCreateSubscriptionBackendSchedulesValidation -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/internal/gateway/management/service.go server/internal/gateway/management/integration_test.go
git commit -m "feat(gateway): schedule subscription runtime validation"
```

### Task 15: Activate Backend On First Successful Validation

**Files:**
- Modify: `server/internal/handler/daemon_gateway.go`
- Test: `server/internal/handler/daemon_gateway_test.go`

- [ ] **Step 1: Add test**

Given a pending subscription backend and credential, when daemon completes validation, assert:

```text
gateway_backend.validation_status = active
gateway_backend.validated_runtime_id = runtime_id
gateway_backend_credential.validation_status = active
gateway_backend_credential.validated_runtime_id = runtime_id
```

- [ ] **Step 2: Implement status updates**

In `CompleteGatewayValidation`, after recording validation success, update backend and credential rows.

- [ ] **Step 3: Run tests**

Run:

```bash
cd server && go test ./internal/handler -run TestCompleteGatewayValidationActivatesBackend -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add server/internal/handler/daemon_gateway.go server/internal/handler/daemon_gateway_test.go
git commit -m "feat(gateway): activate subscription backend after validation"
```

## Milestone 8: UI And CLI Surface

### Task 16: Add UI Fields For Subscription Backend Status

**Files:**
- Modify: `packages/core/types/api.ts`
- Modify: `packages/views/gateway/components/gateway-page.tsx`
- Test: `packages/views/gateway/components/gateway-page.test.tsx`

- [ ] **Step 1: Extend TypeScript types**

Add:

```ts
transport: string;
credential_type?: string;
subscription_provider?: string;
dispatch_scope?: string;
validation_status?: string;
validated_runtime_id?: string;
last_validation_at?: string;
last_validation_error?: string;
account_hint?: string;
```

- [ ] **Step 2: Update form copy**

For subscription providers, show:

```text
Available to all authenticated workspace daemons by default.
```

For API-key providers, preserve existing key copy.

- [ ] **Step 3: Add tests**

Add a Gateway page test that selects `codex-subscription` and asserts:

```ts
expect(screen.getByDisplayValue("daemon://codex")).toBeInTheDocument();
expect(screen.getByText(/Available to all authenticated workspace daemons/i)).toBeInTheDocument();
expect(screen.getByLabelText(/Credential/i)).toBeRequired();
```

- [ ] **Step 4: Run tests**

Run:

```bash
pnpm --filter @multica/web exec vitest run packages/views/gateway/components/gateway-page.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add packages/core/types/api.ts packages/views/gateway/components/gateway-page.tsx packages/views/gateway/components/gateway-page.test.tsx
git commit -m "feat(web): add subscription runtime gateway UI"
```

### Task 17: Add CLI Provider Support

**Files:**
- Modify CLI gateway command files under `server/internal/cli/` or `server/cmd/multica/` after locating existing `gateway add` implementation.
- Test: matching CLI command tests.

- [ ] **Step 1: Locate gateway CLI command**

Run:

```bash
rg -n "gateway add|CreateGatewayBackend|gateway backend" server/internal/cli server/cmd
```

Expected: locate the command file that builds Gateway backend create requests.

- [ ] **Step 2: Add provider aliases**

Support:

```text
claude-code-subscription
codex-subscription
```

Set request fields:

```json
{
  "transport": "daemon_dispatch",
  "credential_type": "subscription_bundle",
  "dispatch_scope": "workspace_authenticated_daemons"
}
```

- [ ] **Step 3: Add CLI tests**

Assert generated request body for `multica gateway add codex-subscription` includes daemon transport fields.

- [ ] **Step 4: Run CLI tests**

Run the focused package test found in Step 1.

- [ ] **Step 5: Commit**

```bash
git add server/internal/cli server/cmd
git commit -m "feat(cli): add subscription runtime gateway providers"
```

## Milestone 9: Claude Code Adapter Follow-Up

### Task 18: Add Claude Code Adapter Interface And Unsupported Runtime Error

**Files:**
- Create: `server/internal/daemon/gateway_claude_code.go`
- Test: `server/internal/daemon/gateway_claude_code_test.go`

- [ ] **Step 1: Add explicit validation stub**

Implement Claude Code validation as provider-recognized but execution-disabled until Dario port is implemented:

```go
var errClaudeCodeAdapterNotReady = errors.New("claude code subscription adapter is not implemented yet")
```

Validation should fail with `provider_not_ready`, not panic or silently route direct HTTP.

- [ ] **Step 2: Add tests**

Add a test asserting Claude Code jobs return clear failure:

```text
error_code = provider_not_ready
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/daemon/gateway_claude_code.go server/internal/daemon/gateway_claude_code_test.go
git commit -m "feat(daemon): add claude code adapter boundary"
```

### Task 19: Port Dario Overage Guard Design

**Files:**
- Create: `server/internal/daemon/claudecode/overage_guard.go`
- Test: `server/internal/daemon/claudecode/overage_guard_test.go`

- [ ] **Step 1: Port behavior, not Node implementation**

Implement Go equivalent of Dario's `OverageGuard`:

```go
type OverageGuard struct {
	enabled bool
	haltedUntil time.Time
	state HaltState
}
```

It should halt on `representative-claim == "overage"` and expose `IsHalted`.

- [ ] **Step 2: Add tests**

Add tests named:

```go
func TestOverageGuardHaltsOnOverageClaim(t *testing.T)
func TestOverageGuardIgnoresSubscriptionClaim(t *testing.T)
func TestOverageGuardManualClearResumes(t *testing.T)
func TestOverageGuardCooldownExpiryResumes(t *testing.T)
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/daemon/claudecode/overage_guard.go server/internal/daemon/claudecode/overage_guard_test.go
git commit -m "feat(daemon): add claude overage guard"
```

### Task 20: Port Dario Live Template Capture Design

**Files:**
- Create package under `server/internal/daemon/claudecode/`
- Tests in same package.

- [ ] **Step 1: Define template data struct**

Model fields from Dario's `TemplateData`: version, captured time, agent identity, system prompt, tool names, header order, beta flags, static header values, body field order.

- [ ] **Step 2: Implement cache loading**

Use `~/.multica/claude-code-template.live.json` or profile-specific daemon config path. Prefer live cache; fall back to bundled snapshot only after logging warning.

- [ ] **Step 3: Defer full capture**

This task defines the Go boundary and cache format only. Actual capture can be a separate task because it must spawn Claude Code against a loopback endpoint and parse one request.

- [ ] **Step 4: Commit**

```bash
git add server/internal/daemon/claudecode
git commit -m "feat(daemon): add claude code template cache boundary"
```

## Milestone 10: Verification

### Task 21: Focused Backend Verification

- [ ] **Step 1: Run Gateway proxy tests**

```bash
cd server && go test ./internal/gateway/proxy
```

Expected: PASS.

- [ ] **Step 2: Run Gateway management tests**

```bash
cd server && go test ./internal/gateway/management
```

Expected: PASS.

- [ ] **Step 3: Run daemon tests**

```bash
cd server && go test ./internal/daemon ./pkg/agent
```

Expected: PASS.

- [ ] **Step 4: Run handler tests**

```bash
cd server && go test ./internal/handler
```

Expected: PASS.

- [ ] **Step 5: Run frontend Gateway tests**

```bash
pnpm --filter @multica/web exec vitest run packages/views/gateway/components/gateway-page.test.tsx
```

Expected: PASS.

### Task 22: Full Verification Before Completion

- [ ] **Step 1: Run full check**

```bash
make check
```

Expected: PASS. If any step fails, fix and re-run `make check`.

- [ ] **Step 2: Manual smoke**

Start services:

```bash
make start
```

Create a Codex subscription backend from UI or CLI, start a daemon with Codex available, wait for validation, then send:

```bash
curl "$MULTICA_GATEWAY_BASE_URL/v1/chat/completions" \
  -H "Authorization: Bearer $MULTICA_GATEWAY_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"codex:gpt-5.3-codex","messages":[{"role":"user","content":"Reply with multica-runtime-ok"}]}'
```

Expected: OpenAI-shaped JSON response with assistant content containing `multica-runtime-ok`.

## Plan Self-Review

- Spec coverage: backend model, default workspace-open dispatch, credential escrow, daemon validation, daemon execution, Codex v1, Claude/Dario follow-up, UI/CLI, audit, and verification are covered.
- Scope check: direct HTTP behavior remains unchanged; daemon-dispatch spine is separate; Claude Code full Dario port is explicitly separated after shared transport and Codex v1.
- Placeholder scan: no `TBD`/`TODO` placeholders are used. Claude Code runtime implementation is scoped as explicit follow-up tasks with failing unsupported behavior first.
- Type consistency: plan uses `direct_http`, `daemon_dispatch`, `subscription_bundle`, `claude_code`, `codex`, and `workspace_authenticated_daemons` consistently across schema, Go types, and UI.
