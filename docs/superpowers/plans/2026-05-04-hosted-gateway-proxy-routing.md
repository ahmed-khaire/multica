# Hosted Gateway Proxy Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add hosted Observer Gateway model-routing endpoints so Multica-issued gateway keys can call enterprise-managed OpenAI-compatible and Anthropic-compatible backends through Multica.

**Architecture:** Add a focused `server/internal/gateway/proxy` package that owns gateway-key authentication, backend/default resolution, upstream credential loading, same-protocol request forwarding, streaming copy, and telemetry capture. Mount public provider-compatible routes outside the normal Multica user-auth group: OpenAI-compatible `GET /v1/models` and `POST /v1/chat/completions`, plus Anthropic-compatible `POST /v1/messages`. This phase intentionally does not implement cross-protocol translation or Claude OAuth sidecar routing; incompatible protocol/backend combinations return provider-shaped errors.

**Tech Stack:** Go 1.26, chi, net/http, pgx/v5, sqlc-generated queries, existing `gateway/keyring`, `gateway/secrets`, `gateway/management`, PostgreSQL telemetry tables.

---

## Scope Boundary

This plan implements the next Gateway phase after `2026-05-03-gateway-management-api-cli.md`.

Included:

- Gateway-key authentication using `Authorization: Bearer mgw_...` and Anthropic-style `x-api-key: mgw_...`.
- Default-backend routing from `gateway_workspace_settings.default_backend_id`.
- Decrypting upstream backend credentials with `MULTICA_GATEWAY_SECRET_KEY`.
- Same-protocol proxying:
  - OpenAI-compatible client surface to `openai_compatible` backends.
  - Anthropic client surface to `anthropic` backends.
- Streaming pass-through from day one for OpenAI-compatible and Anthropic-compatible endpoints.
- Basic `GET /v1/models` proxying to the resolved compatible backend.
- Request/session/model-call telemetry rows for every proxied model call.
- Capture policy support for `metadata_only`, `redacted_content`, and `full_content`.

Not included:

- OpenAI-to-Anthropic or Anthropic-to-OpenAI request/stream translation.
- Claude OAuth/subscription sidecar routing.
- Multi-backend policy routing beyond the workspace default backend.
- UI dashboard views, trace drilldown APIs, SDK/OTLP ingest, or governance API screens.
- Token-cost pricing tables beyond pass-through usage capture.

## Source References

- OpenAI documents Chat Completions streaming as data-only Server-Sent Events when `stream=true`: <https://developers.openai.com/api/docs/guides/streaming-responses>
- OpenAI API authentication uses `Authorization: Bearer <api key>`: <https://developers.openai.com/api/reference/overview>
- Anthropic Messages streaming uses named Server-Sent Events when `"stream": true`: <https://platform.claude.com/docs/en/build-with-claude/streaming>
- Anthropic API authentication requires `x-api-key` and `anthropic-version`: <https://platform.claude.com/docs/en/api/overview>

## File Structure

- Create `server/internal/gateway/proxy/types.go`: protocol constants, request metadata structs, upstream target structs, provider-shaped error helpers.
- Create `server/internal/gateway/proxy/auth.go`: gateway-key extraction and lookup by hash.
- Create `server/internal/gateway/proxy/auth_test.go`: key extraction and lookup tests.
- Create `server/internal/gateway/proxy/resolver.go`: default backend and workspace settings resolver.
- Create `server/internal/gateway/proxy/resolver_test.go`: resolver unit tests using a fake store.
- Create `server/internal/gateway/proxy/forwarder.go`: upstream HTTP request building, header filtering, response forwarding, and streaming copy.
- Create `server/internal/gateway/proxy/forwarder_test.go`: non-stream and streaming pass-through tests with `httptest.Server`.
- Create `server/internal/gateway/proxy/telemetry.go`: telemetry recorder and capture-policy sanitization.
- Create `server/internal/gateway/proxy/telemetry_test.go`: capture-policy behavior tests.
- Create `server/internal/handler/gateway_proxy.go`: thin HTTP handlers for provider-compatible routes.
- Create `server/internal/handler/gateway_proxy_test.go`: handler tests for auth failures, compatible routing, incompatible routing, and streaming forwarding.
- Modify `server/internal/handler/handler.go`: add `GatewayProxy *proxy.Service` and initialize it.
- Modify `server/cmd/server/router.go`: mount public `/v1/models`, `/v1/chat/completions`, and `/v1/messages` routes outside the normal auth middleware.
- Modify `server/cmd/server/integration_test.go`: add routed integration tests for unauthenticated rejection, gateway key routing, and streaming passthrough.

## API Contract

Gateway authentication:

- OpenAI-compatible clients send `Authorization: Bearer mgw_<token>`.
- Anthropic-compatible clients may send `x-api-key: mgw_<token>`.
- For operational flexibility, the gateway also accepts `Authorization: Bearer mgw_<token>` on Anthropic-compatible routes.
- Missing, malformed, unknown, or revoked gateway keys return provider-shaped `401`.

Route mapping:

- `GET /v1/models`
  - Protocol is inferred as Anthropic when `anthropic-version` or `x-api-key` is present; otherwise OpenAI.
  - Proxies to `<backend.base_url>/models`.
- `POST /v1/chat/completions`
  - Requires a default backend with `backend_type = openai_compatible`.
  - Proxies to `<backend.base_url>/chat/completions`.
- `POST /v1/messages`
  - Requires a default backend with `backend_type = anthropic`.
  - Proxies to `<backend.base_url>/v1/messages` when the backend base URL has no `/v1` suffix, or `<backend.base_url>/messages` when it already ends in `/v1`.

Streaming:

- If the request JSON has `"stream": true`, the gateway must not buffer the full upstream response before writing to the client.
- It forwards `Content-Type: text/event-stream` when upstream returns it.
- It flushes chunks as they arrive and records streaming chunk count and duration.

Provider-shaped errors:

OpenAI-compatible error body:

```json
{
  "error": {
    "message": "gateway key is required",
    "type": "authentication_error",
    "code": "gateway_authentication_failed"
  }
}
```

Anthropic-compatible error body:

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "gateway key is required"
  }
}
```

Capture policy:

- `metadata_only`: store model, backend, route, status, latency, usage numbers, and request/response metadata. Do not store prompt messages, completion messages, or chunks.
- `redacted_content`: store request/response content after applying `server/pkg/redact.Text` recursively to string values.
- `full_content`: store request/response content exactly as seen by Gateway, excluding provider credentials and gateway keys.

## Task 1: Proxy Types And Gateway Key Authentication

**Files:**
- Create: `server/internal/gateway/proxy/types.go`
- Create: `server/internal/gateway/proxy/auth.go`
- Create: `server/internal/gateway/proxy/auth_test.go`

- [ ] **Step 1: Write failing auth tests**

Create `server/internal/gateway/proxy/auth_test.go`:

```go
package proxy

import (
	"net/http"
	"testing"
)

func TestExtractGatewayKey(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
		wantOK  bool
	}{
		{
			name: "openai bearer",
			headers: map[string]string{
				"Authorization": "Bearer mgw_123",
			},
			want:   "mgw_123",
			wantOK: true,
		},
		{
			name: "anthropic api key",
			headers: map[string]string{
				"x-api-key": "mgw_456",
			},
			want:   "mgw_456",
			wantOK: true,
		},
		{
			name: "rejects non gateway key",
			headers: map[string]string{
				"Authorization": "Bearer sk-proj-123",
			},
			wantOK: false,
		},
		{
			name:   "missing",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			got, ok := ExtractGatewayKey(req)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProtocolForRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/models", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if got := ProtocolForRequest(req, SurfaceModels); got != ProtocolOpenAI {
		t.Fatalf("default models protocol = %q, want %q", got, ProtocolOpenAI)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	if got := ProtocolForRequest(req, SurfaceModels); got != ProtocolAnthropic {
		t.Fatalf("anthropic models protocol = %q, want %q", got, ProtocolAnthropic)
	}
}
```

- [ ] **Step 2: Run auth tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestExtractGatewayKey|TestProtocolForRequest' -count=1
```

Expected: FAIL because `proxy` package does not exist.

- [ ] **Step 3: Implement proxy types and extraction helpers**

Create `server/internal/gateway/proxy/types.go`:

```go
package proxy

import "net/http"

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	SurfaceOpenAIChatCompletions = "openai_chat_completions"
	SurfaceAnthropicMessages     = "anthropic_messages"
	SurfaceModels                = "models"

	StatusSuccess       = "success"
	StatusUpstreamError = "upstream_error"
	StatusGatewayError  = "gateway_error"
)

type AuthContext struct {
	KeyID       string
	WorkspaceID string
	UserID      string
	KeyPrefix   string
}

type BackendTarget struct {
	ID             string
	Slug           string
	BackendType    string
	BaseURL        string
	UpstreamSecret string
	CapturePolicy  string
}

type RequestSummary struct {
	Model     string
	Stream    bool
	Body      []byte
	BodyJSON  map[string]any
	Protocol  string
	Surface   string
	RoutePath string
	Method    string
}

type ProxyResult struct {
	StatusCode          int
	Status             string
	ErrorType          string
	ErrorMessage       string
	ResponseBody       []byte
	ResponseJSON       map[string]any
	Streaming          bool
	StreamingChunks    int
	TimeToFirstTokenMS int64
}

func ProtocolForRequest(r *http.Request, surface string) string {
	switch surface {
	case SurfaceAnthropicMessages:
		return ProtocolAnthropic
	case SurfaceOpenAIChatCompletions:
		return ProtocolOpenAI
	case SurfaceModels:
		if r.Header.Get("anthropic-version") != "" || r.Header.Get("x-api-key") != "" {
			return ProtocolAnthropic
		}
		return ProtocolOpenAI
	default:
		return ProtocolOpenAI
	}
}
```

Create `server/internal/gateway/proxy/auth.go`:

```go
package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrGatewayKeyRequired = errors.New("gateway key is required")
	ErrGatewayKeyInvalid  = errors.New("gateway key is invalid")
)

func ExtractGatewayKey(r *http.Request) (string, bool) {
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); raw != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(raw, prefix) {
			key := strings.TrimSpace(strings.TrimPrefix(raw, prefix))
			if strings.HasPrefix(key, keyring.Prefix) {
				return key, true
			}
		}
	}
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); strings.HasPrefix(key, keyring.Prefix) {
		return key, true
	}
	return "", false
}

func AuthenticateGatewayKey(ctx context.Context, q *db.Queries, raw string) (AuthContext, error) {
	row, err := q.GetGatewayUserKeyByHash(ctx, keyring.HashGatewayKey(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthContext{}, ErrGatewayKeyInvalid
	}
	if err != nil {
		return AuthContext{}, err
	}
	_ = q.TouchGatewayUserKeyLastUsed(ctx, row.ID)
	return AuthContext{
		KeyID:       util.UUIDToString(row.ID),
		WorkspaceID: util.UUIDToString(row.WorkspaceID),
		UserID:      util.UUIDToString(row.UserID),
		KeyPrefix:   row.KeyPrefix,
	}, nil
}
```

- [ ] **Step 4: Run auth tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestExtractGatewayKey|TestProtocolForRequest' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit auth foundation**

Run:

```bash
git add server/internal/gateway/proxy/types.go server/internal/gateway/proxy/auth.go server/internal/gateway/proxy/auth_test.go
git commit -m "feat: add gateway proxy auth helpers"
```

## Task 2: Backend Resolution And Provider-Shaped Errors

**Files:**
- Create: `server/internal/gateway/proxy/errors.go`
- Create: `server/internal/gateway/proxy/resolver.go`
- Create: `server/internal/gateway/proxy/resolver_test.go`

- [ ] **Step 1: Write failing resolver tests**

Create `server/internal/gateway/proxy/resolver_test.go` with fake query/store coverage:

```go
package proxy

import (
	"errors"
	"testing"
)

func TestCompatibleBackendType(t *testing.T) {
	if !CompatibleBackendType(ProtocolOpenAI, "openai_compatible") {
		t.Fatal("OpenAI protocol should accept openai_compatible backend")
	}
	if CompatibleBackendType(ProtocolOpenAI, "anthropic") {
		t.Fatal("OpenAI protocol should reject anthropic backend in this phase")
	}
	if !CompatibleBackendType(ProtocolAnthropic, "anthropic") {
		t.Fatal("Anthropic protocol should accept anthropic backend")
	}
}

func TestProviderErrorShapes(t *testing.T) {
	openAI := ProviderErrorBody(ProtocolOpenAI, "gateway key is required", "authentication_error", "gateway_authentication_failed")
	if openAI["error"].(map[string]any)["message"] != "gateway key is required" {
		t.Fatalf("OpenAI error shape = %#v", openAI)
	}

	anthropic := ProviderErrorBody(ProtocolAnthropic, "gateway key is required", "authentication_error", "gateway_authentication_failed")
	if anthropic["type"] != "error" || anthropic["error"].(map[string]any)["type"] != "authentication_error" {
		t.Fatalf("Anthropic error shape = %#v", anthropic)
	}
}

func TestNormalizeUpstreamPath(t *testing.T) {
	if got := JoinUpstreamPath("https://api.openai.com/v1", "/chat/completions"); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("OpenAI path = %q", got)
	}
	if got := JoinUpstreamPath("https://api.anthropic.com", "/v1/messages"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("Anthropic path = %q", got)
	}
}

func TestGatewayErrorsWrapSentinel(t *testing.T) {
	err := GatewayError{StatusCode: 404, PublicMessage: "gateway default backend is not configured", Cause: ErrDefaultBackendNotConfigured}
	if !errors.Is(err, ErrDefaultBackendNotConfigured) {
		t.Fatalf("GatewayError should wrap ErrDefaultBackendNotConfigured")
	}
}
```

- [ ] **Step 2: Run resolver tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestCompatibleBackendType|TestProviderErrorShapes|TestNormalizeUpstreamPath|TestGatewayErrorsWrapSentinel' -count=1
```

Expected: FAIL because the helpers do not exist.

- [ ] **Step 3: Implement errors and resolver helpers**

Create `server/internal/gateway/proxy/errors.go`:

```go
package proxy

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrDefaultBackendNotConfigured = errors.New("gateway default backend is not configured")
	ErrBackendDisabled             = errors.New("gateway backend is disabled")
	ErrIncompatibleBackend         = errors.New("gateway backend is not compatible with requested protocol")
	ErrGatewaySecretNotConfigured  = errors.New("gateway secret key is not configured")
)

type GatewayError struct {
	StatusCode    int
	PublicMessage string
	ErrorType     string
	Code          string
	Cause         error
}

func (e GatewayError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.PublicMessage, e.Cause)
	}
	return e.PublicMessage
}

func (e GatewayError) Unwrap() error {
	return e.Cause
}

func ProviderErrorBody(protocol, message, errorType, code string) map[string]any {
	if protocol == ProtocolAnthropic {
		return map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errorType,
				"message": message,
			},
		}
	}
	return map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errorType,
			"code":    code,
		},
	}
}

func AuthenticationError(message string, cause error) GatewayError {
	return GatewayError{StatusCode: http.StatusUnauthorized, PublicMessage: message, ErrorType: "authentication_error", Code: "gateway_authentication_failed", Cause: cause}
}

func RoutingError(status int, message, code string, cause error) GatewayError {
	return GatewayError{StatusCode: status, PublicMessage: message, ErrorType: "invalid_request_error", Code: code, Cause: cause}
}
```

Create `server/internal/gateway/proxy/resolver.go`:

```go
package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/management"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func CompatibleBackendType(protocol, backendType string) bool {
	switch protocol {
	case ProtocolOpenAI:
		return backendType == management.BackendTypeOpenAICompatible
	case ProtocolAnthropic:
		return backendType == management.BackendTypeAnthropic
	default:
		return false
	}
}

func JoinUpstreamPath(base, path string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return strings.TrimRight(base, "/") + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	return parsed.String()
}

type Resolver struct {
	queries *db.Queries
	loadBox func() (*secrets.Box, error)
}

func NewResolver(queries *db.Queries) *Resolver {
	return &Resolver{queries: queries, loadBox: secrets.FromEnv}
}

func (r *Resolver) ResolveDefaultBackend(ctx context.Context, workspaceID, protocol string) (BackendTarget, error) {
	workspaceUUID, err := parseUUID(workspaceID)
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "invalid workspace", "invalid_workspace", err)
	}
	settings, err := r.queries.GetGatewayWorkspaceSettings(ctx, workspaceUUID)
	if errors.Is(err, pgx.ErrNoRows) || !settings.DefaultBackendID.Valid {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
	}
	if err != nil {
		return BackendTarget{}, err
	}
	backend, err := r.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{
		WorkspaceID: workspaceUUID,
		ID:          settings.DefaultBackendID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway default backend is not configured", "gateway_default_backend_missing", ErrDefaultBackendNotConfigured)
	}
	if err != nil {
		return BackendTarget{}, err
	}
	if !backend.Enabled {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is disabled", "gateway_backend_disabled", ErrBackendDisabled)
	}
	if !CompatibleBackendType(protocol, backend.BackendType) {
		return BackendTarget{}, RoutingError(http.StatusBadRequest, "gateway backend is not compatible with requested protocol", "gateway_backend_incompatible", ErrIncompatibleBackend)
	}
	box, err := r.loadBox()
	if err != nil {
		return BackendTarget{}, RoutingError(http.StatusServiceUnavailable, "gateway secret key is not configured", "gateway_secret_unavailable", ErrGatewaySecretNotConfigured)
	}
	secret, err := box.DecryptString(backend.EncryptedCredential)
	if err != nil {
		return BackendTarget{}, err
	}
	return BackendTarget{
		ID:             util.UUIDToString(backend.ID),
		Slug:           backend.Slug,
		BackendType:    backend.BackendType,
		BaseURL:        backend.BaseUrl,
		UpstreamSecret: secret,
		CapturePolicy:  settings.CapturePolicy,
	}, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(strings.TrimSpace(value)); err != nil {
		return pgtype.UUID{}, err
	}
	if !id.Valid {
		return pgtype.UUID{}, errors.New("invalid uuid")
	}
	return id, nil
}
```

- [ ] **Step 4: Run resolver tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestCompatibleBackendType|TestProviderErrorShapes|TestNormalizeUpstreamPath|TestGatewayErrorsWrapSentinel' -count=1
```

Expected: PASS after resolving compile issues.

- [ ] **Step 5: Commit resolver**

Run:

```bash
git add server/internal/gateway/proxy/errors.go server/internal/gateway/proxy/resolver.go server/internal/gateway/proxy/resolver_test.go
git commit -m "feat: resolve gateway proxy backends"
```

## Task 3: Upstream Forwarder With Streaming Copy

**Files:**
- Create: `server/internal/gateway/proxy/forwarder.go`
- Create: `server/internal/gateway/proxy/forwarder_test.go`

- [ ] **Step 1: Write failing forwarder tests**

Create tests for:

- `BuildUpstreamRequest` removes Gateway auth headers and replaces them with upstream credentials.
- OpenAI-compatible forwarding uses `Authorization: Bearer <provider-key>`.
- Anthropic forwarding uses `x-api-key: <provider-key>` and preserves or defaults `anthropic-version`.
- Streaming responses are flushed chunk-by-chunk without buffering the full body.

Use `httptest.NewServer` and a custom `flushRecorder` that implements `http.Flusher`.

- [ ] **Step 2: Run forwarder tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestBuildUpstreamRequest|TestForwardStreaming' -count=1
```

Expected: FAIL because forwarder code does not exist.

- [ ] **Step 3: Implement forwarder**

Implement:

- `type Forwarder struct { Client *http.Client }`
- `func NewForwarder(client *http.Client) *Forwarder`
- `func (f *Forwarder) Forward(ctx context.Context, w http.ResponseWriter, r *http.Request, target BackendTarget, summary RequestSummary) (ProxyResult, error)`
- `func BuildUpstreamRequest(ctx context.Context, inbound *http.Request, target BackendTarget, summary RequestSummary) (*http.Request, error)`
- `func copyResponseHeaders(dst, src http.Header)`
- `func copyStreamingResponse(w http.ResponseWriter, resp *http.Response) (ProxyResult, error)`

Header rules:

- Drop hop-by-hop headers: `Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`.
- Drop `Authorization`, `x-api-key`, `X-Workspace-ID`, and `X-Multica-*` before sending upstream.
- Preserve `Content-Type`, `Accept`, `User-Agent`, and provider-specific beta/version headers.
- OpenAI protocol sets `Authorization: Bearer <target.UpstreamSecret>`.
- Anthropic protocol sets `x-api-key: <target.UpstreamSecret>` and defaults `anthropic-version` to `2023-06-01` when absent.

Streaming rules:

- Forward the upstream status code before streaming body bytes.
- Use `io.Copy` through a small wrapper that counts chunks and calls `Flush()` after each read.
- Do not inspect or rewrite SSE content in this task.

- [ ] **Step 4: Run forwarder tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestBuildUpstreamRequest|TestForwardStreaming' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit forwarder**

Run:

```bash
git add server/internal/gateway/proxy/forwarder.go server/internal/gateway/proxy/forwarder_test.go
git commit -m "feat: add gateway proxy forwarder"
```

## Task 4: Capture Policy And Telemetry Recorder

**Files:**
- Create: `server/internal/gateway/proxy/telemetry.go`
- Create: `server/internal/gateway/proxy/telemetry_test.go`

- [ ] **Step 1: Write failing telemetry tests**

Tests must cover:

- `metadata_only` stores no prompt or completion content.
- `redacted_content` recursively redacts string values in request/response JSON.
- `full_content` preserves JSON content.
- OpenAI usage objects map `prompt_tokens`, `completion_tokens`, and `total_tokens`.
- Anthropic usage objects map `input_tokens` to prompt tokens and `output_tokens` to completion tokens.

- [ ] **Step 2: Run telemetry tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestCapturePolicy|TestUsageExtraction' -count=1
```

Expected: FAIL because telemetry helpers do not exist.

- [ ] **Step 3: Implement telemetry helpers**

Implement:

- `func CaptureJSON(policy string, value any) []byte`
- `func redactValue(value any) any`
- `func ExtractOpenAIUsage(resp map[string]any) Usage`
- `func ExtractAnthropicUsage(resp map[string]any) Usage`
- `type Usage struct { PromptTokens int64; CompletionTokens int64; TotalTokens int64; Source string }`

Then implement a `Recorder` that creates:

- `gateway_session` at request start.
- `gateway_request` at request start.
- `gateway_model_call` and completion updates after upstream response.

Use existing sqlc generated telemetry queries; keep this recorder best-effort in the proxy path: if telemetry insert fails after upstream succeeds, log it but do not fail the model response.

- [ ] **Step 4: Run telemetry tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -run 'TestCapturePolicy|TestUsageExtraction' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit telemetry helpers**

Run:

```bash
git add server/internal/gateway/proxy/telemetry.go server/internal/gateway/proxy/telemetry_test.go
git commit -m "feat: record gateway proxy telemetry"
```

## Task 5: Gateway Proxy Service And Handlers

**Files:**
- Create: `server/internal/gateway/proxy/service.go`
- Create: `server/internal/handler/gateway_proxy.go`
- Create: `server/internal/handler/gateway_proxy_test.go`
- Modify: `server/internal/handler/handler.go`

- [ ] **Step 1: Write failing handler tests**

Create tests in `server/internal/handler/gateway_proxy_test.go` for:

- Missing gateway key returns OpenAI-shaped `401` on `/v1/chat/completions`.
- Missing gateway key returns Anthropic-shaped `401` on `/v1/messages`.
- OpenAI chat completion forwards to a fake upstream and replaces Authorization with the backend key.
- Anthropic messages forwards to a fake upstream and replaces `x-api-key` with the backend key.
- Incompatible backend type returns `400` with provider-shaped error.
- Streaming response forwards `text/event-stream` and includes all chunks.

- [ ] **Step 2: Run handler tests and verify they fail**

Run:

```bash
cd server && go test ./internal/handler -run GatewayProxy -count=1
```

Expected: FAIL because proxy handlers do not exist.

- [ ] **Step 3: Implement service and handlers**

Create `server/internal/gateway/proxy/service.go`:

- `type Service struct { Queries *db.Queries; Resolver *Resolver; Forwarder *Forwarder; Recorder *Recorder }`
- `func NewService(queries *db.Queries, client *http.Client) *Service`
- `func (s *Service) ServeOpenAIChatCompletions(w http.ResponseWriter, r *http.Request)`
- `func (s *Service) ServeAnthropicMessages(w http.ResponseWriter, r *http.Request)`
- `func (s *Service) ServeModels(w http.ResponseWriter, r *http.Request)`

Flow:

1. Determine protocol from surface.
2. Extract gateway key.
3. Authenticate key by hash.
4. Read and restore request body for POST routes.
5. Parse model and `stream` from JSON.
6. Resolve default compatible backend.
7. Start telemetry.
8. Forward request/response.
9. Complete telemetry.

Create `server/internal/handler/gateway_proxy.go`:

```go
package handler

import "net/http"

func (h *Handler) GatewayOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeOpenAIChatCompletions(w, r)
}

func (h *Handler) GatewayAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeAnthropicMessages(w, r)
}

func (h *Handler) GatewayModels(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeModels(w, r)
}
```

Modify `server/internal/handler/handler.go`:

- import `github.com/multica-ai/multica/server/internal/gateway/proxy`
- add `GatewayProxy *proxy.Service` to `Handler`
- initialize with `proxy.NewService(queries, http.DefaultClient)` or a private default HTTP client with a sane timeout.

- [ ] **Step 4: Run handler tests**

Run:

```bash
cd server && go test ./internal/handler -run GatewayProxy -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit handlers**

Run:

```bash
git add server/internal/gateway/proxy/service.go server/internal/handler/gateway_proxy.go server/internal/handler/gateway_proxy_test.go server/internal/handler/handler.go
git commit -m "feat: add hosted gateway proxy handlers"
```

## Task 6: Router Wiring And Server Integration Tests

**Files:**
- Modify: `server/cmd/server/router.go`
- Modify: `server/cmd/server/integration_test.go`

- [ ] **Step 1: Write failing routed integration tests**

Add tests in `server/cmd/server/integration_test.go`:

- `TestGatewayProxyRequiresGatewayKey`
- `TestGatewayProxyOpenAIChatCompletionsRoutesToDefaultBackend`
- `TestGatewayProxyAnthropicMessagesRoutesToDefaultBackend`
- `TestGatewayProxyStreamingPassThrough`

Use local `httptest.Server` upstreams for default backends. Insert or create Gateway backends through the management API/service so credentials are encrypted with the test `MULTICA_GATEWAY_SECRET_KEY`.

- [ ] **Step 2: Run routed tests and verify they fail**

Run:

```bash
cd server && go test ./cmd/server -run GatewayProxy -count=1
```

Expected: FAIL until routes are mounted.

- [ ] **Step 3: Mount public provider-compatible routes**

Modify `server/cmd/server/router.go` outside the existing authenticated API group:

```go
r.Route("/v1", func(r chi.Router) {
	r.Get("/models", h.GatewayModels)
	r.Post("/chat/completions", h.GatewayOpenAIChatCompletions)
	r.Post("/messages", h.GatewayAnthropicMessages)
})
```

Do not wrap these routes in the normal user auth or workspace-member middleware; Gateway key auth happens inside the proxy service.

- [ ] **Step 4: Run routed integration tests**

Run:

```bash
cd server && go test ./cmd/server -run GatewayProxy -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit routes**

Run:

```bash
git add server/cmd/server/router.go server/cmd/server/integration_test.go
git commit -m "feat: mount hosted gateway proxy routes"
```

## Task 7: End-To-End Verification

**Files:**
- No planned source edits.

- [ ] **Step 1: Run sqlc**

Run:

```bash
make sqlc
```

Expected: succeeds with no generated drift unless query files intentionally changed.

- [ ] **Step 2: Run proxy package tests**

Run:

```bash
cd server && go test ./internal/gateway/proxy -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused handler and server tests**

Run:

```bash
cd server && go test ./internal/handler -run GatewayProxy -count=1
cd server && go test ./cmd/server -run GatewayProxy -count=1
```

Expected: PASS.

- [ ] **Step 4: Run existing Gateway regression tests**

Run:

```bash
cd server && go test ./internal/gateway/... ./internal/handler -run Gateway -count=1
cd server && go test ./cmd/server -run Gateway -count=1
cd server && go test ./cmd/multica -run Gateway -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full server suite**

Run:

```bash
cd server && go test ./...
```

Expected: PASS. If DB integration tests skip because PostgreSQL is unavailable, record the skip output in the handoff.

- [ ] **Step 6: Inspect git status**

Run:

```bash
git status --short
```

Expected: clean worktree.

## Acceptance Criteria

- `multica gateway key` values can be used as OpenAI-compatible and Anthropic-compatible API credentials.
- `POST /v1/chat/completions` accepts `Authorization: Bearer mgw_...` and forwards to an enabled default `openai_compatible` backend.
- `POST /v1/messages` accepts `x-api-key: mgw_...` or `Authorization: Bearer mgw_...` and forwards to an enabled default `anthropic` backend.
- Streaming requests pass through incrementally for both OpenAI-compatible and Anthropic-compatible routes.
- Provider credentials are never returned to clients and are never written to telemetry.
- Gateway keys are hash-validated and `last_used_at` is touched on successful authentication.
- Every proxied model request creates Gateway telemetry rows for session, request, and model call.
- Capture policy controls whether prompt/completion content is omitted, redacted, or stored.
- Incompatible default backend/protocol combinations fail with provider-shaped `400` errors.
- Normal Multica API auth routes are unchanged; provider-compatible routes authenticate only with Gateway keys.

## Manual Smoke Test

With a local server, migrated database, `MULTICA_GATEWAY_SECRET_KEY` set, and a local OpenAI-compatible server running:

```bash
multica login --server-url http://localhost:8080
multica gateway add local --key=anything --base-url=http://127.0.0.1:11434/v1 --set-default
eval "$(multica gateway key)"
curl "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"local-model","messages":[{"role":"user","content":"hello"}],"stream":true}'
```

Expected: the client receives streamed SSE chunks from the upstream local backend through Multica Gateway, and the workspace has new `gateway_session`, `gateway_request`, and `gateway_model_call` rows.
