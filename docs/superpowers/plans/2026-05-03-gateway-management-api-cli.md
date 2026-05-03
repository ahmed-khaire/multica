# Gateway Management API CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the workspace-scoped Gateway management API and `multica gateway` CLI so users can retrieve Observer Gateway base URLs and gateway keys, while admins can configure enterprise-managed upstream backends, defaults, and capture policy.

**Architecture:** Add a focused `server/internal/gateway/management` service on top of the foundation schema and generated sqlc queries, then expose it through thin handlers mounted under `/api/gateway`. Add a `multica gateway` Cobra command group that reuses the existing `multica login` profile, token, server URL, and workspace resolution helpers.

**Tech Stack:** Go 1.26, chi, Cobra, pgx/v5, sqlc-generated queries, existing `server/internal/cli.APIClient`, existing `server/internal/gateway/secrets`, existing `server/internal/gateway/keyring`.

---

## Phase Boundary

This plan implements Phase 2 only:

- Gateway management API routes under `/api/gateway`.
- `multica gateway` CLI command group.
- User gateway key creation/retrieval/revocation.
- Admin-managed upstream backend creation/listing/default selection.
- Workspace capture policy update with `metadata_only`, `redacted_content`, and `full_content`.
- Audit log rows for key, backend, default backend, and capture policy changes.

This plan does not implement hosted model proxy routes (`/v1/...`), Anthropic-compatible proxy routes, streaming proxy behavior, AgentOps-style trace ingestion, dashboard UI, governance UI, or the Claude OAuth sidecar. Those phases depend on the management surface produced here.

Milestone 1 now includes a lightweight SDK/OTLP enterprise app and agent observability layer, but this Phase 2 plan remains scoped to Gateway management. SDK context propagation, ingest keys, OTLP-compatible ingestion, application inventory resolution, artifact/log submission, and pre-action policy evaluation start in the telemetry-ingest phase after hosted routing exists.

## API Contract

All routes require `Authorization: Bearer <token>`. All routes require workspace membership through `X-Workspace-ID` or `workspace_id`. Admin routes additionally require workspace role `owner` or `admin`.

Member routes:

- `GET /api/gateway/status`: returns Observer Gateway URLs, current capture policy, default backend summary, backend counts, and whether the user has an active key.
- `GET /api/gateway/settings`: returns capture policy and default backend summary.
- `GET /api/gateway/backends`: returns backend summaries without raw credentials.
- `GET /api/gateway/key`: returns the existing active gateway key for the signed-in user, decrypting it so the CLI can print it.
- `POST /api/gateway/key`: creates an active gateway key if one does not exist, or returns the existing key.
- `GET /api/gateway/keys`: lists the signed-in user's key metadata without key hashes or encrypted values.
- `POST /api/gateway/keys/{id}/revoke`: revokes one active key owned by the signed-in user.

Admin routes:

- `POST /api/gateway/backends`: creates an upstream backend from a provider preset and optional explicit fields.
- `PATCH /api/gateway/backends/{id}`: updates backend display name, base URL, enabled state, and optionally rotates the credential.
- `DELETE /api/gateway/backends/{id}`: deletes a backend and clears it as default through the database foreign key.
- `POST /api/gateway/default`: sets the default backend by slug.
- `POST /api/gateway/policy`: sets the workspace capture policy.

`POST /api/gateway/key` response shape:

```json
{
  "id": "9a8f0c76-d1db-42d6-9d42-b48d98f4d6f3",
  "key": "mgw_0123456789abcdef0123456789abcdef01234567",
  "key_prefix": "mgw_01234567",
  "openai_base_url": "https://api.multica.ai/v1",
  "openai_api_key": "mgw_0123456789abcdef0123456789abcdef01234567",
  "anthropic_base_url": "https://api.multica.ai",
  "anthropic_api_key": "mgw_0123456789abcdef0123456789abcdef01234567",
  "created_at": "2026-05-03T12:00:00Z",
  "last_used_at": null
}
```

`multica gateway key` default output:

```bash
OPENAI_BASE_URL=https://api.multica.ai/v1
OPENAI_API_KEY=mgw_0123456789abcdef0123456789abcdef01234567
ANTHROPIC_BASE_URL=https://api.multica.ai
ANTHROPIC_API_KEY=mgw_0123456789abcdef0123456789abcdef01234567
```

## File Structure

- Create `server/internal/gateway/management/types.go`: public service DTOs, provider presets, capture policy constants, backend type constants, response mapping helpers.
- Create `server/internal/gateway/management/service.go`: service methods for settings, status, backend lifecycle, default backend selection, user key lifecycle, secret loading, encryption, and audit logging.
- Create `server/internal/gateway/management/service_test.go`: pure unit tests for provider defaults, validation, credential hints, URL generation, and secret-loader error normalization.
- Create `server/internal/gateway/management/integration_test.go`: DB-backed tests for key create/retrieve, backend creation, default backend selection, capture policy update, and audit writes.
- Modify `server/internal/handler/handler.go`: add `Gateway *management.Service` to `Handler` and initialize it in `New`.
- Create `server/internal/handler/gateway.go`: HTTP handlers for the API contract.
- Create `server/internal/handler/gateway_test.go`: handler-level tests for request decoding, response redaction, and direct handler authorization fallback.
- Modify `server/cmd/server/router.go`: mount `/api/gateway` routes inside the existing workspace-member group and add admin middleware to admin-only routes.
- Modify `server/cmd/server/integration_test.go`: add router tests for auth, workspace membership, admin-only route enforcement, key flow, and backend flow.
- Modify `server/internal/cli/client.go`: add `PatchJSON` because the API exposes a canonical `PATCH /api/gateway/backends/{id}` update route.
- Create `server/cmd/multica/cmd_gateway.go`: Cobra command group and CLI API DTOs for `multica gateway`.
- Modify `server/cmd/multica/main.go`: register `gatewayCmd` as a core command.
- Create `server/cmd/multica/cmd_gateway_test.go`: CLI command tests using `httptest.Server`.
- Modify `server/cmd/multica/cmd_compat_test.go`: include `gateway` in command-availability coverage if that file asserts the root command set.

## Provider Presets

Use these provider presets in `management.ProviderPresetFor` and in CLI help examples:

| Provider | Slug | Display Name | Backend Type | Default Base URL | Credential Required |
| --- | --- | --- | --- | --- | --- |
| `openai` | `openai` | `OpenAI` | `openai_compatible` | `https://api.openai.com/v1` | yes |
| `groq` | `groq` | `Groq` | `openai_compatible` | `https://api.groq.com/openai/v1` | yes |
| `openrouter` | `openrouter` | `OpenRouter` | `openai_compatible` | `https://openrouter.ai/api/v1` | yes |
| `local` | `local` | `Local OpenAI-compatible` | `openai_compatible` | `http://127.0.0.1:11434/v1` | yes |
| `anthropic` | `anthropic` | `Anthropic` | `anthropic` | `https://api.anthropic.com` | yes |
| `claude-oauth` | `claude-oauth` | `Claude OAuth` | `claude_oauth` | `claude-oauth://sidecar` | no |

For `claude-oauth`, store the encrypted credential value as `sidecar-managed` so the existing `gateway_backend.encrypted_credential BYTEA NOT NULL` constraint remains satisfied. Do not implement OAuth account linking in this phase.

## Task 1: Management DTOs And Pure Helpers

**Files:**
- Create: `server/internal/gateway/management/types.go`
- Create: `server/internal/gateway/management/service.go`
- Create: `server/internal/gateway/management/service_test.go`

- [ ] **Step 1: Write failing helper tests**

Create `server/internal/gateway/management/service_test.go` with these tests:

```go
package management

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func TestProviderPresetFor(t *testing.T) {
	cases := []struct {
		provider           string
		wantSlug           string
		wantBackendType    string
		wantBaseURL        string
		wantRequiresSecret bool
	}{
		{"openai", "openai", BackendTypeOpenAICompatible, "https://api.openai.com/v1", true},
		{"groq", "groq", BackendTypeOpenAICompatible, "https://api.groq.com/openai/v1", true},
		{"openrouter", "openrouter", BackendTypeOpenAICompatible, "https://openrouter.ai/api/v1", true},
		{"local", "local", BackendTypeOpenAICompatible, "http://127.0.0.1:11434/v1", true},
		{"anthropic", "anthropic", BackendTypeAnthropic, "https://api.anthropic.com", true},
		{"claude-oauth", "claude-oauth", BackendTypeClaudeOAuth, "claude-oauth://sidecar", false},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			got, ok := ProviderPresetFor(tc.provider)
			if !ok {
				t.Fatalf("ProviderPresetFor(%q) did not find a preset", tc.provider)
			}
			if got.Slug != tc.wantSlug {
				t.Fatalf("Slug = %q, want %q", got.Slug, tc.wantSlug)
			}
			if got.BackendType != tc.wantBackendType {
				t.Fatalf("BackendType = %q, want %q", got.BackendType, tc.wantBackendType)
			}
			if got.BaseURL != tc.wantBaseURL {
				t.Fatalf("BaseURL = %q, want %q", got.BaseURL, tc.wantBaseURL)
			}
			if got.RequiresCredential != tc.wantRequiresSecret {
				t.Fatalf("RequiresCredential = %v, want %v", got.RequiresCredential, tc.wantRequiresSecret)
			}
		})
	}
}

func TestProviderPresetForNormalizesInput(t *testing.T) {
	got, ok := ProviderPresetFor(" OpenAI ")
	if !ok {
		t.Fatal("expected OpenAI provider to resolve")
	}
	if got.Slug != "openai" {
		t.Fatalf("Slug = %q, want openai", got.Slug)
	}
}

func TestValidateCapturePolicy(t *testing.T) {
	for _, policy := range []string{CaptureMetadataOnly, CaptureRedactedContent, CaptureFullContent} {
		if err := ValidateCapturePolicy(policy); err != nil {
			t.Fatalf("ValidateCapturePolicy(%q) returned error: %v", policy, err)
		}
	}

	err := ValidateCapturePolicy("raw_everything")
	if !errors.Is(err, ErrInvalidCapturePolicy) {
		t.Fatalf("expected ErrInvalidCapturePolicy, got %v", err)
	}
}

func TestCredentialHint(t *testing.T) {
	cases := []struct {
		secret string
		want   string
	}{
		{"sk-proj-1234567890abcdef", "sk-proj-...cdef"},
		{"gsk_short", "****"},
		{"", ""},
	}

	for _, tc := range cases {
		if got := CredentialHint(tc.secret); got != tc.want {
			t.Fatalf("CredentialHint(%q) = %q, want %q", tc.secret, got, tc.want)
		}
	}
}

func TestBuildGatewayURLs(t *testing.T) {
	got := BuildGatewayURLs("https://api.multica.ai/")
	if got.OpenAIBaseURL != "https://api.multica.ai/v1" {
		t.Fatalf("OpenAIBaseURL = %q", got.OpenAIBaseURL)
	}
	if got.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("AnthropicBaseURL = %q", got.AnthropicBaseURL)
	}
}

func TestNormalizeSecretError(t *testing.T) {
	err := normalizeSecretError(errors.New(secrets.EnvKeyName + " is required"))
	if !errors.Is(err, ErrGatewaySecretNotConfigured) {
		t.Fatalf("expected ErrGatewaySecretNotConfigured, got %v", err)
	}
}
```

- [ ] **Step 2: Run helper tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/management -run 'TestProviderPresetFor|TestValidateCapturePolicy|TestCredentialHint|TestBuildGatewayURLs|TestNormalizeSecretError' -count=1
```

Expected: FAIL because `server/internal/gateway/management` does not exist or the referenced constants/functions do not exist.

- [ ] **Step 3: Add DTOs, constants, and helper implementations**

Create `server/internal/gateway/management/types.go` with:

```go
package management

import "strings"

const (
	BackendTypeOpenAICompatible = "openai_compatible"
	BackendTypeAnthropic        = "anthropic"
	BackendTypeClaudeOAuth      = "claude_oauth"

	CaptureMetadataOnly    = "metadata_only"
	CaptureRedactedContent = "redacted_content"
	CaptureFullContent     = "full_content"

	DefaultCapturePolicy = CaptureRedactedContent
)

type ProviderPreset struct {
	Provider           string
	Slug               string
	DisplayName        string
	BackendType        string
	BaseURL            string
	RequiresCredential bool
}

type GatewayURLs struct {
	OpenAIBaseURL     string `json:"openai_base_url"`
	AnthropicBaseURL string `json:"anthropic_base_url"`
}

type BackendResponse struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	DisplayName    string         `json:"display_name"`
	BackendType    string         `json:"backend_type"`
	BaseURL        string         `json:"base_url"`
	CredentialHint string         `json:"credential_hint"`
	Enabled        bool           `json:"enabled"`
	IsDefault      bool           `json:"is_default"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type SettingsResponse struct {
	CapturePolicy  string           `json:"capture_policy"`
	DefaultBackend *BackendResponse `json:"default_backend"`
}

type StatusResponse struct {
	OpenAIBaseURL       string           `json:"openai_base_url"`
	AnthropicBaseURL   string           `json:"anthropic_base_url"`
	CapturePolicy      string           `json:"capture_policy"`
	DefaultBackend     *BackendResponse `json:"default_backend"`
	BackendCount       int              `json:"backend_count"`
	EnabledBackendCount int             `json:"enabled_backend_count"`
	HasActiveKey       bool             `json:"has_active_key"`
}

type UserKeyResponse struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	KeyPrefix        string  `json:"key_prefix"`
	OpenAIBaseURL    string  `json:"openai_base_url"`
	OpenAIAPIKey     string  `json:"openai_api_key"`
	AnthropicBaseURL string  `json:"anthropic_base_url"`
	AnthropicAPIKey  string  `json:"anthropic_api_key"`
	CreatedAt        string  `json:"created_at"`
	LastUsedAt        *string `json:"last_used_at"`
}

type UserKeyListItem struct {
	ID         string  `json:"id"`
	KeyPrefix  string  `json:"key_prefix"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type CreateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Provider    string
	Slug        string
	DisplayName string
	BackendType string
	BaseURL     string
	Key         string
	Enabled     bool
	SetDefault  bool
	Metadata    map[string]any
}

type UpdateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	BackendID   string
	DisplayName *string
	BaseURL     *string
	Key         *string
	Enabled    *bool
	Metadata    map[string]any
}

type CapturePolicyInput struct {
	WorkspaceID    string
	ActorUserID    string
	CapturePolicy  string
}

type SetDefaultBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Slug        string
}

var providerPresets = map[string]ProviderPreset{
	"openai": {
		Provider: "openai", Slug: "openai", DisplayName: "OpenAI",
		BackendType: BackendTypeOpenAICompatible, BaseURL: "https://api.openai.com/v1", RequiresCredential: true,
	},
	"groq": {
		Provider: "groq", Slug: "groq", DisplayName: "Groq",
		BackendType: BackendTypeOpenAICompatible, BaseURL: "https://api.groq.com/openai/v1", RequiresCredential: true,
	},
	"openrouter": {
		Provider: "openrouter", Slug: "openrouter", DisplayName: "OpenRouter",
		BackendType: BackendTypeOpenAICompatible, BaseURL: "https://openrouter.ai/api/v1", RequiresCredential: true,
	},
	"local": {
		Provider: "local", Slug: "local", DisplayName: "Local OpenAI-compatible",
		BackendType: BackendTypeOpenAICompatible, BaseURL: "http://127.0.0.1:11434/v1", RequiresCredential: true,
	},
	"anthropic": {
		Provider: "anthropic", Slug: "anthropic", DisplayName: "Anthropic",
		BackendType: BackendTypeAnthropic, BaseURL: "https://api.anthropic.com", RequiresCredential: true,
	},
	"claude-oauth": {
		Provider: "claude-oauth", Slug: "claude-oauth", DisplayName: "Claude OAuth",
		BackendType: BackendTypeClaudeOAuth, BaseURL: "claude-oauth://sidecar", RequiresCredential: false,
	},
}

func ProviderPresetFor(provider string) (ProviderPreset, bool) {
	preset, ok := providerPresets[strings.ToLower(strings.TrimSpace(provider))]
	return preset, ok
}
```

Create `server/internal/gateway/management/service.go` with the pure helpers first:

```go
package management

import (
	"errors"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

var (
	ErrInvalidCapturePolicy      = errors.New("invalid gateway capture policy")
	ErrInvalidGatewayBackend     = errors.New("invalid gateway backend")
	ErrGatewaySecretNotConfigured = errors.New("gateway secret key is not configured")
	ErrGatewayBackendNotFound    = errors.New("gateway backend not found")
	ErrGatewayKeyNotFound        = errors.New("gateway key not found")
)

func ValidateCapturePolicy(policy string) error {
	switch policy {
	case CaptureMetadataOnly, CaptureRedactedContent, CaptureFullContent:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrInvalidCapturePolicy, policy)
	}
}

func CredentialHint(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) < 13 {
		return "****"
	}
	return secret[:8] + "..." + secret[len(secret)-4:]
}

func BuildGatewayURLs(serverBaseURL string) GatewayURLs {
	base := strings.TrimRight(strings.TrimSpace(serverBaseURL), "/")
	return GatewayURLs{
		OpenAIBaseURL:     base + "/v1",
		AnthropicBaseURL: base,
	}
}

func normalizeSecretError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), secrets.EnvKeyName) {
		return ErrGatewaySecretNotConfigured
	}
	return err
}
```

- [ ] **Step 4: Run helper tests and verify they pass**

Run:

```bash
cd server && go test ./internal/gateway/management -run 'TestProviderPresetFor|TestValidateCapturePolicy|TestCredentialHint|TestBuildGatewayURLs|TestNormalizeSecretError' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit helper layer**

Run:

```bash
git add server/internal/gateway/management/types.go server/internal/gateway/management/service.go server/internal/gateway/management/service_test.go
git commit -m "feat: add gateway management service helpers"
```

Expected: commit succeeds.

## Task 2: Management Persistence Service

**Files:**
- Modify: `server/internal/gateway/management/service.go`
- Create: `server/internal/gateway/management/integration_test.go`

- [ ] **Step 1: Write failing DB-backed service tests**

Create `server/internal/gateway/management/integration_test.go`:

```go
package management

import (
	"context"
	"encoding/base64"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func TestManagementServiceKeyFlow(t *testing.T) {
	ctx := context.Background()
	pool := openManagementTestDB(t)
	workspaceID, userID := setupManagementFixture(t, pool)
	t.Setenv(secrets.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	svc := NewService(db.New(pool), pool)

	first, err := svc.GetOrCreateUserKey(ctx, workspaceID, userID, "https://api.multica.ai")
	if err != nil {
		t.Fatalf("GetOrCreateUserKey first call: %v", err)
	}
	if first.Key == "" || first.OpenAIBaseURL != "https://api.multica.ai/v1" || first.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("unexpected first key response: %+v", first)
	}

	second, err := svc.GetOrCreateUserKey(ctx, workspaceID, userID, "https://api.multica.ai/")
	if err != nil {
		t.Fatalf("GetOrCreateUserKey second call: %v", err)
	}
	if second.Key != first.Key {
		t.Fatalf("second call returned different key: first=%q second=%q", first.Key, second.Key)
	}

	keys, err := svc.ListUserKeys(ctx, workspaceID, userID)
	if err != nil {
		t.Fatalf("ListUserKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].KeyPrefix == "" || keys[0].RevokedAt != nil {
		t.Fatalf("unexpected key list: %+v", keys)
	}

	revoked, err := svc.RevokeUserKey(ctx, workspaceID, userID, first.ID)
	if err != nil {
		t.Fatalf("RevokeUserKey: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatalf("expected revoked_at to be set: %+v", revoked)
	}
}

func TestManagementServiceBackendFlow(t *testing.T) {
	ctx := context.Background()
	pool := openManagementTestDB(t)
	workspaceID, userID := setupManagementFixture(t, pool)
	t.Setenv(secrets.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	svc := NewService(db.New(pool), pool)

	backend, err := svc.CreateBackend(ctx, CreateBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		Provider:    "groq",
		Key:         "gsk_1234567890abcdef",
		Enabled:     true,
		Metadata:    map[string]any{"routing": "default"},
	})
	if err != nil {
		t.Fatalf("CreateBackend: %v", err)
	}
	if backend.Slug != "groq" || backend.BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("unexpected backend: %+v", backend)
	}
	if backend.CredentialHint != "gsk_1234...cdef" {
		t.Fatalf("CredentialHint = %q", backend.CredentialHint)
	}

	status, err := svc.Status(ctx, workspaceID, userID, "https://api.multica.ai")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.CapturePolicy != CaptureRedactedContent {
		t.Fatalf("CapturePolicy = %q", status.CapturePolicy)
	}
	if status.DefaultBackend == nil || status.DefaultBackend.Slug != "groq" {
		t.Fatalf("expected groq as default backend: %+v", status.DefaultBackend)
	}

	if _, err := svc.UpdateCapturePolicy(ctx, CapturePolicyInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		CapturePolicy: CaptureMetadataOnly,
	}); err != nil {
		t.Fatalf("UpdateCapturePolicy: %v", err)
	}

	settings, err := svc.Settings(ctx, workspaceID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if settings.CapturePolicy != CaptureMetadataOnly {
		t.Fatalf("CapturePolicy = %q", settings.CapturePolicy)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM ai_audit_log
		WHERE workspace_id = $1
		  AND action IN ('gateway.backend.create', 'gateway.policy.update')
	`, workspaceID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("auditCount = %d, want 2", auditCount)
	}
}

func openManagementTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setupManagementFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID string, userID string) {
	t.Helper()
	ctx := context.Background()
	slug := "gateway-management-test-" + t.Name()
	email := slug + "@multica.ai"

	_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = $1`, slug)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE email = $1`, email)
	})

	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`, "Gateway Management Test", email).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description) VALUES ($1, $2, $3) RETURNING id`, "Gateway Management Test", slug, "Gateway management tests").Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	return workspaceID, userID
}
```

- [ ] **Step 2: Run service DB tests and verify they fail**

Run:

```bash
cd server && go test ./internal/gateway/management -run 'TestManagementServiceKeyFlow|TestManagementServiceBackendFlow' -count=1
```

Expected: FAIL because `NewService`, `GetOrCreateUserKey`, `CreateBackend`, `Status`, `UpdateCapturePolicy`, `Settings`, `ListUserKeys`, and `RevokeUserKey` are not implemented.

- [ ] **Step 3: Implement service state and transactions**

Extend `server/internal/gateway/management/service.go` with:

```go
import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/keyring"
	"github.com/multica-ai/multica/server/internal/gateway/secrets"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type txStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Service struct {
	queries   *db.Queries
	txStarter txStarter
	loadBox   func() (*secrets.Box, error)
}

func NewService(queries *db.Queries, txStarter txStarter) *Service {
	return &Service{
		queries:   queries,
		txStarter: txStarter,
		loadBox:   secrets.FromEnv,
	}
}

func (s *Service) withTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.txStarter == nil {
		return fn(s.queries)
	}
	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func uuidValue(id string) pgtype.UUID {
	return util.ParseUUID(id)
}

func uuidString(id pgtype.UUID) string {
	return util.UUIDToString(id)
}

func textTimestamp(ts pgtype.Timestamptz) string {
	return util.TimestampToString(ts)
}

func optionalTimestamp(ts pgtype.Timestamptz) *string {
	return util.TimestampToPtr(ts)
}

func validateBackendURL(raw string, backendType string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid base_url", ErrInvalidGatewayBackend)
	}
	if backendType == BackendTypeClaudeOAuth && parsed.Scheme == "claude-oauth" {
		return nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: base_url must use http or https", ErrInvalidGatewayBackend)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: base_url host is required", ErrInvalidGatewayBackend)
	}
	return nil
}
```

Keep `normalizeSecretError` in the same file. Go imports must be consolidated into one import block.

- [ ] **Step 4: Implement key lifecycle methods**

Add these methods to `server/internal/gateway/management/service.go`:

```go
func (s *Service) GetOrCreateUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}

	workspaceUUID := uuidValue(workspaceID)
	userUUID := uuidValue(userID)

	existing, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	})
	if err == nil {
		raw, err := keyring.DecryptStoredGatewayKey(box, existing.EncryptedKeyValue)
		if err != nil {
			return UserKeyResponse{}, err
		}
		return userKeyResponse(existing, raw, BuildGatewayURLs(serverBaseURL)), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return UserKeyResponse{}, err
	}

	var created db.GatewayUserKey
	var raw string
	if err := s.withTx(ctx, func(q *db.Queries) error {
		prepared, err := keyring.PrepareNewGatewayKey(box)
		if err != nil {
			return err
		}
		row, err := q.CreateGatewayUserKey(ctx, db.CreateGatewayUserKeyParams{
			WorkspaceID:       workspaceUUID,
			UserID:            userUUID,
			KeyHash:           prepared.Hash,
			EncryptedKeyValue: prepared.Encrypted,
			KeyPrefix:         prepared.DisplayPrefix,
		})
		if err != nil {
			return err
		}
		raw = prepared.Raw
		created = row
		return s.audit(ctx, q, workspaceUUID, userUUID, "gateway.key.create", "gateway_user_key", uuidString(row.ID), nil, map[string]any{"key_prefix": row.KeyPrefix})
	}); err != nil {
		return UserKeyResponse{}, err
	}

	return userKeyResponse(created, raw, BuildGatewayURLs(serverBaseURL)), nil
}

func (s *Service) GetActiveUserKey(ctx context.Context, workspaceID, userID, serverBaseURL string) (UserKeyResponse, error) {
	box, err := s.loadBox()
	if err != nil {
		return UserKeyResponse{}, normalizeSecretError(err)
	}
	row, err := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{WorkspaceID: uuidValue(workspaceID), UserID: uuidValue(userID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return UserKeyResponse{}, ErrGatewayKeyNotFound
	}
	if err != nil {
		return UserKeyResponse{}, err
	}
	raw, err := keyring.DecryptStoredGatewayKey(box, row.EncryptedKeyValue)
	if err != nil {
		return UserKeyResponse{}, err
	}
	return userKeyResponse(row, raw, BuildGatewayURLs(serverBaseURL)), nil
}

func (s *Service) ListUserKeys(ctx context.Context, workspaceID, userID string) ([]UserKeyListItem, error) {
	rows, err := s.queries.ListGatewayUserKeys(ctx, db.ListGatewayUserKeysParams{WorkspaceID: uuidValue(workspaceID), UserID: uuidValue(userID)})
	if err != nil {
		return nil, err
	}
	out := make([]UserKeyListItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserKeyListItem{
			ID:         uuidString(row.ID),
			KeyPrefix:  row.KeyPrefix,
			RevokedAt:  optionalTimestamp(row.RevokedAt),
			LastUsedAt: optionalTimestamp(row.LastUsedAt),
			CreatedAt:  textTimestamp(row.CreatedAt),
		})
	}
	return out, nil
}

func (s *Service) RevokeUserKey(ctx context.Context, workspaceID, userID, keyID string) (UserKeyListItem, error) {
	var revoked db.GatewayUserKey
	err := s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.RevokeGatewayUserKey(ctx, db.RevokeGatewayUserKeyParams{WorkspaceID: uuidValue(workspaceID), UserID: uuidValue(userID), ID: uuidValue(keyID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrGatewayKeyNotFound
		}
		if err != nil {
			return err
		}
		revoked = row
		return s.audit(ctx, q, uuidValue(workspaceID), uuidValue(userID), "gateway.key.revoke", "gateway_user_key", uuidString(row.ID), map[string]any{"key_prefix": row.KeyPrefix}, map[string]any{"revoked": true})
	})
	if err != nil {
		return UserKeyListItem{}, err
	}
	return UserKeyListItem{ID: uuidString(revoked.ID), KeyPrefix: revoked.KeyPrefix, RevokedAt: optionalTimestamp(revoked.RevokedAt), LastUsedAt: optionalTimestamp(revoked.LastUsedAt), CreatedAt: textTimestamp(revoked.CreatedAt)}, nil
}

func userKeyResponse(row db.GatewayUserKey, raw string, urls GatewayURLs) UserKeyResponse {
	return UserKeyResponse{
		ID:               uuidString(row.ID),
		Key:              raw,
		KeyPrefix:        row.KeyPrefix,
		OpenAIBaseURL:    urls.OpenAIBaseURL,
		OpenAIAPIKey:     raw,
		AnthropicBaseURL: urls.AnthropicBaseURL,
		AnthropicAPIKey:  raw,
		CreatedAt:        textTimestamp(row.CreatedAt),
		LastUsedAt:        optionalTimestamp(row.LastUsedAt),
	}
}
```

- [ ] **Step 5: Implement settings, backend lifecycle, default selection, and audit**

Add these methods to `server/internal/gateway/management/service.go`:

```go
func (s *Service) Settings(ctx context.Context, workspaceID string) (SettingsResponse, error) {
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
	if err != nil {
		return SettingsResponse{}, err
	}
	var defaultBackend *BackendResponse
	if settings.DefaultBackendID.Valid {
		backend, err := s.queries.GetGatewayBackendByID(ctx, db.GetGatewayBackendByIDParams{WorkspaceID: uuidValue(workspaceID), ID: settings.DefaultBackendID})
		if err == nil {
			mapped := backendResponse(backend, settings.DefaultBackendID)
			defaultBackend = &mapped
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return SettingsResponse{}, err
		}
	}
	return SettingsResponse{CapturePolicy: settings.CapturePolicy, DefaultBackend: defaultBackend}, nil
}

func (s *Service) Status(ctx context.Context, workspaceID, userID, serverBaseURL string) (StatusResponse, error) {
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
	if err != nil {
		return StatusResponse{}, err
	}
	backends, err := s.queries.ListGatewayBackends(ctx, uuidValue(workspaceID))
	if err != nil {
		return StatusResponse{}, err
	}
	enabledCount := 0
	var defaultBackend *BackendResponse
	for _, backend := range backends {
		if backend.Enabled {
			enabledCount++
		}
		if settings.DefaultBackendID.Valid && backend.ID == settings.DefaultBackendID {
			mapped := backendResponse(backend, settings.DefaultBackendID)
			defaultBackend = &mapped
		}
	}
	_, keyErr := s.queries.GetActiveGatewayUserKey(ctx, db.GetActiveGatewayUserKeyParams{WorkspaceID: uuidValue(workspaceID), UserID: uuidValue(userID)})
	if keyErr != nil && !errors.Is(keyErr, pgx.ErrNoRows) {
		return StatusResponse{}, keyErr
	}
	urls := BuildGatewayURLs(serverBaseURL)
	return StatusResponse{
		OpenAIBaseURL:       urls.OpenAIBaseURL,
		AnthropicBaseURL:   urls.AnthropicBaseURL,
		CapturePolicy:      settings.CapturePolicy,
		DefaultBackend:     defaultBackend,
		BackendCount:       len(backends),
		EnabledBackendCount: enabledCount,
		HasActiveKey:       keyErr == nil,
	}, nil
}

func (s *Service) ListBackends(ctx context.Context, workspaceID string) ([]BackendResponse, error) {
	settings, err := s.getSettingsOrDefault(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListGatewayBackends(ctx, uuidValue(workspaceID))
	if err != nil {
		return nil, err
	}
	out := make([]BackendResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, backendResponse(row, settings.DefaultBackendID))
	}
	return out, nil
}

func (s *Service) CreateBackend(ctx context.Context, input CreateBackendInput) (BackendResponse, error) {
	normalized, credential, err := normalizeCreateBackendInput(input)
	if err != nil {
		return BackendResponse{}, err
	}
	box, err := s.loadBox()
	if err != nil {
		return BackendResponse{}, normalizeSecretError(err)
	}
	encrypted, err := box.EncryptString(credential)
	if err != nil {
		return BackendResponse{}, err
	}
	metadata, err := json.Marshal(normalized.Metadata)
	if err != nil {
		return BackendResponse{}, err
	}

	var created db.GatewayBackend
	var settings db.GatewayWorkspaceSetting
	err = s.withTx(ctx, func(q *db.Queries) error {
		row, err := q.CreateGatewayBackend(ctx, db.CreateGatewayBackendParams{
			WorkspaceID:         uuidValue(normalized.WorkspaceID),
			Slug:                normalized.Slug,
			DisplayName:         normalized.DisplayName,
			BackendType:         normalized.BackendType,
			BaseUrl:             normalized.BaseURL,
			EncryptedCredential: encrypted,
			CredentialHint:      CredentialHint(credential),
			Enabled:             normalized.Enabled,
			Metadata:            metadata,
			CreatedBy:           uuidValue(normalized.ActorUserID),
		})
		if err != nil {
			return err
		}
		created = row

		current, settingsErr := s.getSettingsOrDefaultWithQueries(ctx, q, normalized.WorkspaceID)
		if settingsErr != nil {
			return settingsErr
		}
		shouldSetDefault := normalized.SetDefault || !current.DefaultBackendID.Valid
		settings = current
		if shouldSetDefault {
			updated, err := q.UpsertGatewayWorkspaceSettings(ctx, db.UpsertGatewayWorkspaceSettingsParams{
				WorkspaceID:      uuidValue(normalized.WorkspaceID),
				CapturePolicy:    current.CapturePolicy,
				DefaultBackendID: row.ID,
			})
			if err != nil {
				return err
			}
			settings = updated
		}
		return s.audit(ctx, q, uuidValue(normalized.WorkspaceID), uuidValue(normalized.ActorUserID), "gateway.backend.create", "gateway_backend", uuidString(row.ID), nil, backendResponse(row, settings.DefaultBackendID))
	})
	if err != nil {
		return BackendResponse{}, err
	}
	return backendResponse(created, settings.DefaultBackendID), nil
}

func (s *Service) UpdateCapturePolicy(ctx context.Context, input CapturePolicyInput) (SettingsResponse, error) {
	if err := ValidateCapturePolicy(input.CapturePolicy); err != nil {
		return SettingsResponse{}, err
	}
	var updated db.GatewayWorkspaceSetting
	err := s.withTx(ctx, func(q *db.Queries) error {
		current, err := s.getSettingsOrDefaultWithQueries(ctx, q, input.WorkspaceID)
		if err != nil {
			return err
		}
		row, err := q.UpsertGatewayWorkspaceSettings(ctx, db.UpsertGatewayWorkspaceSettingsParams{
			WorkspaceID:      uuidValue(input.WorkspaceID),
			CapturePolicy:    input.CapturePolicy,
			DefaultBackendID: current.DefaultBackendID,
		})
		if err != nil {
			return err
		}
		updated = row
		return s.audit(ctx, q, uuidValue(input.WorkspaceID), uuidValue(input.ActorUserID), "gateway.policy.update", "gateway_workspace_settings", input.WorkspaceID, map[string]any{"capture_policy": current.CapturePolicy}, map[string]any{"capture_policy": row.CapturePolicy})
	})
	if err != nil {
		return SettingsResponse{}, err
	}
	return SettingsResponse{CapturePolicy: updated.CapturePolicy}, nil
}
```

Also implement:

- `SetDefaultBackend(ctx, SetDefaultBackendInput) (SettingsResponse, error)`: lookup backend by slug, preserve current capture policy, upsert settings with the backend ID, and audit action `gateway.default_backend.update`.
- `UpdateBackend(ctx, UpdateBackendInput) (BackendResponse, error)`: fetch current backend, preserve fields not provided, rotate encrypted credential only when `Key` is non-nil, and audit action `gateway.backend.update`.
- `DeleteBackend(ctx, workspaceID, actorUserID, backendID string) error`: fetch current backend, delete it, and audit action `gateway.backend.delete`.
- `normalizeCreateBackendInput(input CreateBackendInput) (CreateBackendInput, string, error)`: apply provider preset defaults, require credentials for all presets except `claude-oauth`, use `sidecar-managed` for `claude-oauth`, preserve the `Enabled` value supplied by handlers/CLI, validate base URL, and require `Slug`.
- `getSettingsOrDefault(ctx, workspaceID)` and `getSettingsOrDefaultWithQueries(ctx, q, workspaceID)`: return `CaptureRedactedContent` and a null `DefaultBackendID` when `pgx.ErrNoRows`.
- `backendResponse(row, defaultID)`: unmarshal metadata into `map[string]any`, set `IsDefault`, and never expose `EncryptedCredential`.
- `audit(ctx, q, workspaceID, actorUserID, action, targetType, targetID, before, after)`: JSON marshal states and call `CreateAIAuditLog` with empty `RequestID`.

- [ ] **Step 6: Run service tests and verify they pass**

Run:

```bash
cd server && go test ./internal/gateway/management -count=1
```

Expected: PASS. If the local PostgreSQL server is unavailable, DB-backed tests print a skip reason and the pure unit tests still pass.

- [ ] **Step 7: Commit service persistence**

Run:

```bash
git add server/internal/gateway/management/service.go server/internal/gateway/management/integration_test.go
git commit -m "feat: add gateway management service"
```

Expected: commit succeeds.

## Task 3: Gateway HTTP Handlers

**Files:**
- Modify: `server/internal/handler/handler.go`
- Create: `server/internal/handler/gateway.go`
- Create: `server/internal/handler/gateway_test.go`

- [ ] **Step 1: Write failing handler tests**

Create `server/internal/handler/gateway_test.go`:

```go
package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func TestGatewayCreateKeyHandler(t *testing.T) {
	t.Setenv(secrets.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/key", nil)
	req.Host = "api.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")

	testHandler.CreateGatewayUserKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateGatewayUserKey status = %d body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Key              string `json:"key"`
		OpenAIBaseURL    string `json:"openai_base_url"`
		AnthropicBaseURL string `json:"anthropic_base_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Key == "" {
		t.Fatal("expected key")
	}
	if body.OpenAIBaseURL != "https://api.multica.ai/v1" {
		t.Fatalf("openai_base_url = %q", body.OpenAIBaseURL)
	}
	if body.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("anthropic_base_url = %q", body.AnthropicBaseURL)
	}
}

func TestGatewayCreateBackendRedactsCredential(t *testing.T) {
	t.Setenv(secrets.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/backends", map[string]any{
		"provider": "openrouter",
		"key":      "sk-or-1234567890abcdef",
	})

	testHandler.CreateGatewayBackend(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend status = %d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["encrypted_credential"]; ok {
		t.Fatal("response must not contain encrypted_credential")
	}
	if _, ok := body["key"]; ok {
		t.Fatal("response must not contain raw key")
	}
	if body["credential_hint"] != "sk-or-12...cdef" {
		t.Fatalf("credential_hint = %v", body["credential_hint"])
	}
}

func TestGatewayPolicyRejectsInvalidCapturePolicy(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/policy", map[string]any{
		"capture_policy": "raw_everything",
	})

	testHandler.UpdateGatewayPolicy(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateGatewayPolicy status = %d body=%s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run handler tests and verify they fail**

Run:

```bash
cd server && go test ./internal/handler -run Gateway -count=1
```

Expected: FAIL because the handler methods do not exist and `Handler` has no `Gateway` service.

- [ ] **Step 3: Wire management service into Handler**

Modify `server/internal/handler/handler.go`:

```go
import (
	"github.com/multica-ai/multica/server/internal/gateway/management"
)
```

Add field:

```go
Gateway *management.Service
```

Initialize it in `New`:

```go
Gateway: management.NewService(queries, txStarter),
```

- [ ] **Step 4: Implement gateway handlers**

Create `server/internal/handler/gateway.go`:

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/gateway/management"
)

type gatewayCreateBackendRequest struct {
	Provider    string         `json:"provider"`
	Slug        string         `json:"slug"`
	DisplayName string         `json:"display_name"`
	BackendType string         `json:"backend_type"`
	BaseURL     string         `json:"base_url"`
	Key         string         `json:"key"`
	Enabled     *bool          `json:"enabled"`
	SetDefault  bool           `json:"set_default"`
	Metadata    map[string]any `json:"metadata"`
}

type gatewayUpdateBackendRequest struct {
	DisplayName *string        `json:"display_name"`
	BaseURL     *string        `json:"base_url"`
	Key         *string        `json:"key"`
	Enabled     *bool          `json:"enabled"`
	Metadata    map[string]any `json:"metadata"`
}

type gatewaySetDefaultRequest struct {
	BackendSlug string `json:"backend_slug"`
}

type gatewayPolicyRequest struct {
	CapturePolicy string `json:"capture_policy"`
}

func (h *Handler) GatewayStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.Status(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) GetGatewaySettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.Settings(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) ListGatewayBackends(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.ListBackends(r.Context(), workspaceID)
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) GetGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.GetActiveUserKey(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) CreateGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.GetOrCreateUserKey(r.Context(), workspaceID, userID, gatewayServerBaseURL(r))
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) ListGatewayUserKeys(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.ListUserKeys(r.Context(), workspaceID, userID)
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) RevokeGatewayUserKey(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	out, err := h.Gateway.RevokeUserKey(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) CreateGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	var body gatewayCreateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	out, err := h.Gateway.CreateBackend(r.Context(), management.CreateBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		Provider:    body.Provider,
		Slug:        body.Slug,
		DisplayName: body.DisplayName,
		BackendType: body.BackendType,
		BaseURL:     body.BaseURL,
		Key:         body.Key,
		Enabled:     enabled,
		SetDefault:  body.SetDefault,
		Metadata:    body.Metadata,
	})
	h.writeGatewayResult(w, http.StatusCreated, out, err)
}

func (h *Handler) UpdateGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	var body gatewayUpdateBackendRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.Gateway.UpdateBackend(r.Context(), management.UpdateBackendInput{
		WorkspaceID: workspaceID,
		ActorUserID: userID,
		BackendID:   chi.URLParam(r, "id"),
		DisplayName: body.DisplayName,
		BaseURL:     body.BaseURL,
		Key:         body.Key,
		Enabled:     body.Enabled,
		Metadata:    body.Metadata,
	})
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) DeleteGatewayBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	err := h.Gateway.DeleteBackend(r.Context(), workspaceID, userID, chi.URLParam(r, "id"))
	h.writeGatewayResult(w, http.StatusOK, map[string]bool{"deleted": true}, err)
}

func (h *Handler) SetGatewayDefaultBackend(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	var body gatewaySetDefaultRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.Gateway.SetDefaultBackend(r.Context(), management.SetDefaultBackendInput{WorkspaceID: workspaceID, ActorUserID: userID, Slug: body.BackendSlug})
	h.writeGatewayResult(w, http.StatusOK, out, err)
}

func (h *Handler) UpdateGatewayPolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := h.gatewayRequestScope(w, r)
	if !ok {
		return
	}
	var body gatewayPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.Gateway.UpdateCapturePolicy(r.Context(), management.CapturePolicyInput{WorkspaceID: workspaceID, ActorUserID: userID, CapturePolicy: body.CapturePolicy})
	h.writeGatewayResult(w, http.StatusOK, out, err)
}
```

Also add helper functions in the same file:

- `gatewayRequestScope(w, r) (workspaceID, userID string, ok bool)`: require user ID, resolve workspace ID from middleware/header/query, and call `workspaceMember` for direct handler tests.
- `gatewayServerBaseURL(r) string`: prefer `X-Forwarded-Proto` and `X-Forwarded-Host`, then `X-Forwarded-Proto` plus `Host`, then `https` for TLS requests, then `http`.
- `writeGatewayResult(w, status, payload, err)`: map `ErrInvalidCapturePolicy` and `ErrInvalidGatewayBackend` to 400, `ErrGatewayBackendNotFound` and `ErrGatewayKeyNotFound` to 404, `ErrGatewaySecretNotConfigured` to 500 with message `gateway secret key is not configured`, and all other errors to 500.

- [ ] **Step 5: Run handler tests and verify they pass**

Run:

```bash
cd server && go test ./internal/handler -run Gateway -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit handler layer**

Run:

```bash
git add server/internal/handler/handler.go server/internal/handler/gateway.go server/internal/handler/gateway_test.go
git commit -m "feat: add gateway management handlers"
```

Expected: commit succeeds.

## Task 4: Router Wiring And Authorization

**Files:**
- Modify: `server/cmd/server/router.go`
- Modify: `server/cmd/server/integration_test.go`

- [ ] **Step 1: Write failing router integration tests**

Append these tests to `server/cmd/server/integration_test.go`:

```go
func TestGatewayRoutesRequireAuth(t *testing.T) {
	paths := []string{"/api/gateway/status", "/api/gateway/key", "/api/gateway/backends"}
	for _, path := range paths {
		resp, err := http.Get(testServer.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func TestGatewayRouterKeyFlow(t *testing.T) {
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	resp := authRequest(t, http.MethodPost, "/api/gateway/key", nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status = %d body=%s", resp.StatusCode, string(body))
	}
	var body struct {
		Key            string `json:"key"`
		OpenAIBaseURL  string `json:"openai_base_url"`
		OpenAIAPIKey   string `json:"openai_api_key"`
		AnthropicAPIKey string `json:"anthropic_api_key"`
	}
	readJSON(t, resp, &body)
	if body.Key == "" || body.OpenAIAPIKey != body.Key || body.AnthropicAPIKey != body.Key {
		t.Fatalf("unexpected key response: %+v", body)
	}
	if !strings.HasSuffix(body.OpenAIBaseURL, "/v1") {
		t.Fatalf("openai_base_url = %q", body.OpenAIBaseURL)
	}
}

func TestGatewayRouterBackendAdminFlow(t *testing.T) {
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))

	resp := authRequest(t, http.MethodPost, "/api/gateway/backends", map[string]any{
		"provider": "local",
		"key":      "anything",
		"base_url": "http://127.0.0.1:11434/v1",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("create backend status = %d body=%s", resp.StatusCode, string(body))
	}
	readJSON(t, resp, &map[string]any{})

	resp = authRequest(t, http.MethodGet, "/api/gateway/backends", nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("list backend status = %d body=%s", resp.StatusCode, string(body))
	}
	var backends []map[string]any
	readJSON(t, resp, &backends)
	if len(backends) == 0 {
		t.Fatal("expected at least one backend")
	}
	for _, backend := range backends {
		if _, ok := backend["encrypted_credential"]; ok {
			t.Fatalf("backend response leaked encrypted credential: %+v", backend)
		}
	}
}
```

Add `encoding/base64` to the import block for this file.

- [ ] **Step 2: Run router tests and verify they fail**

Run:

```bash
cd server && go test ./cmd/server -run Gateway -count=1
```

Expected: FAIL because `/api/gateway/...` routes are not mounted.

- [ ] **Step 3: Mount gateway routes in router**

In `server/cmd/server/router.go`, inside the existing workspace-scoped group that already uses `middleware.RequireWorkspaceMember(queries)`, add this route block before the `// Issues` section:

```go
// Gateway
r.Route("/api/gateway", func(r chi.Router) {
	r.Get("/status", h.GatewayStatus)
	r.Get("/settings", h.GetGatewaySettings)
	r.Get("/backends", h.ListGatewayBackends)
	r.Get("/key", h.GetGatewayUserKey)
	r.Post("/key", h.CreateGatewayUserKey)
	r.Get("/keys", h.ListGatewayUserKeys)
	r.Post("/keys/{id}/revoke", h.RevokeGatewayUserKey)

	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceRole(queries, "owner", "admin"))
		r.Post("/backends", h.CreateGatewayBackend)
		r.Patch("/backends/{id}", h.UpdateGatewayBackend)
		r.Delete("/backends/{id}", h.DeleteGatewayBackend)
		r.Post("/default", h.SetGatewayDefaultBackend)
		r.Post("/policy", h.UpdateGatewayPolicy)
	})
})
```

- [ ] **Step 4: Run router tests and verify they pass**

Run:

```bash
cd server && go test ./cmd/server -run Gateway -count=1
```

Expected: PASS.

- [ ] **Step 5: Run protected route smoke tests**

Run:

```bash
cd server && go test ./cmd/server -run 'TestProtectedRoutesRequireAuth|TestInvalidJWT|TestGateway' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit router wiring**

Run:

```bash
git add server/cmd/server/router.go server/cmd/server/integration_test.go
git commit -m "feat: mount gateway management routes"
```

Expected: commit succeeds.

## Task 5: CLI API Client And Gateway Commands

**Files:**
- Modify: `server/internal/cli/client.go`
- Create: `server/cmd/multica/cmd_gateway.go`
- Create: `server/cmd/multica/cmd_gateway_test.go`

- [ ] **Step 1: Write failing CLI command tests**

Create `server/cmd/multica/cmd_gateway_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGatewayKeyCommandPrintsEnv(t *testing.T) {
	var gotWorkspaceID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspaceID = r.Header.Get("X-Workspace-ID")
		if r.Method != http.MethodPost || r.URL.Path != "/api/gateway/key" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id":                  "key-id",
			"key":                 "mgw_123",
			"key_prefix":          "mgw_123",
			"openai_base_url":     serverURL(r) + "/v1",
			"openai_api_key":      "mgw_123",
			"anthropic_base_url":  serverURL(r),
			"anthropic_api_key":   "mgw_123",
			"created_at":          "2026-05-03T12:00:00Z",
		})
	}))
	defer server.Close()

	cmd := gatewayTestRoot(t, server.URL)
	cmd.SetArgs([]string{"gateway", "key", "--workspace-id", "workspace-1"})
	output := executeGatewayTestCommand(t, cmd)

	if gotWorkspaceID != "workspace-1" {
		t.Fatalf("X-Workspace-ID = %q", gotWorkspaceID)
	}
	for _, want := range []string{
		"OPENAI_BASE_URL=" + server.URL + "/v1",
		"OPENAI_API_KEY=mgw_123",
		"ANTHROPIC_BASE_URL=" + server.URL,
		"ANTHROPIC_API_KEY=mgw_123",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestGatewayAddCommandSendsProviderPayload(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/gateway/backends" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id": "backend-id", "slug": "groq", "display_name": "Groq",
			"backend_type": "openai_compatible", "base_url": "https://api.groq.com/openai/v1",
			"credential_hint": "gsk_1234...cdef", "enabled": true, "is_default": true,
		})
	}))
	defer server.Close()

	cmd := gatewayTestRoot(t, server.URL)
	cmd.SetArgs([]string{"gateway", "add", "groq", "--key", "gsk_1234567890abcdef", "--base-url", "https://api.groq.com/openai/v1", "--workspace-id", "workspace-1"})
	output := executeGatewayTestCommand(t, cmd)

	if body["provider"] != "groq" || body["key"] != "gsk_1234567890abcdef" || body["base_url"] != "https://api.groq.com/openai/v1" {
		t.Fatalf("unexpected request body: %+v", body)
	}
	if !strings.Contains(output, "groq") || strings.Contains(output, "gsk_1234567890abcdef") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func TestGatewayAddRequiresKeyForCredentialProvider(t *testing.T) {
	cmd := gatewayTestRoot(t, "http://127.0.0.1")
	cmd.SetArgs([]string{"gateway", "add", "openai", "--workspace-id", "workspace-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected openai without --key to fail")
	}
}

func TestGatewayAddClaudeOAuthAllowsMissingKey(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/gateway/backends" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id": "backend-id", "slug": "claude-oauth", "display_name": "Claude OAuth",
			"backend_type": "claude_oauth", "base_url": "claude-oauth://sidecar",
			"credential_hint": "****", "enabled": true, "is_default": false,
		})
	}))
	defer server.Close()

	cmd := gatewayTestRoot(t, server.URL)
	cmd.SetArgs([]string{"gateway", "add", "claude-oauth", "--workspace-id", "workspace-1"})
	output := executeGatewayTestCommand(t, cmd)

	if body["provider"] != "claude-oauth" {
		t.Fatalf("provider = %v", body["provider"])
	}
	if _, ok := body["key"]; ok {
		t.Fatalf("claude-oauth request must not include key: %+v", body)
	}
	if !strings.Contains(output, "claude-oauth") {
		t.Fatalf("unexpected output:\n%s", output)
	}
}

func gatewayTestRoot(t *testing.T, serverURL string) *cobra.Command {
	t.Helper()
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("HOME", t.TempDir())
	root := &cobra.Command{Use: "multica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server-url", "", "")
	root.PersistentFlags().String("workspace-id", "", "")
	root.PersistentFlags().String("profile", "", "")
	root.AddCommand(gatewayCmd)
	return root
}

func executeGatewayTestCommand(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v\noutput:\n%s", err, out.String())
	}
	return out.String()
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
```

- [ ] **Step 2: Run CLI tests and verify they fail**

Run:

```bash
cd server && go test ./cmd/multica -run Gateway -count=1
```

Expected: FAIL because `gatewayCmd` does not exist and `APIClient.PatchJSON` does not exist.

- [ ] **Step 3: Add `PatchJSON` to API client**

Modify `server/internal/cli/client.go` by adding:

```go
// PatchJSON performs a PATCH request with a JSON body.
func (c *APIClient) PatchJSON(ctx context.Context, path string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respData, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("PATCH %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respData)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Implement `multica gateway` commands**

Create `server/cmd/multica/cmd_gateway.go` with these commands:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/multica-ai/multica/server/internal/cli"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Manage Observer Gateway keys and backends",
}

var gatewayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Observer Gateway status",
	RunE:  runGatewayStatus,
}

var gatewayKeyCmd = &cobra.Command{
	Use:   "key",
	Short: "Create or print your Observer Gateway key",
	RunE:  runGatewayKey,
}

var gatewayKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "List your Observer Gateway keys",
	RunE:  runGatewayKeys,
}

var gatewayRevokeCmd = &cobra.Command{
	Use:   "revoke <key-id>",
	Short: "Revoke one Observer Gateway key",
	Args:  exactArgs(1),
	RunE:  runGatewayRevoke,
}

var gatewayAddCmd = &cobra.Command{
	Use:   "add <provider>",
	Short: "Add an enterprise-managed upstream backend",
	Args:  exactArgs(1),
	RunE:  runGatewayAdd,
}

var gatewayBackendsCmd = &cobra.Command{
	Use:   "backends",
	Short: "List configured Gateway backends",
	RunE:  runGatewayBackends,
}

var gatewayDefaultCmd = &cobra.Command{
	Use:   "default <backend-slug>",
	Short: "Set the default Gateway backend",
	Args:  exactArgs(1),
	RunE:  runGatewayDefault,
}

var gatewayPolicyCmd = &cobra.Command{
	Use:   "policy <capture-policy>",
	Short: "Set Gateway capture policy",
	Args:  exactArgs(1),
	RunE:  runGatewayPolicy,
}

func init() {
	gatewayCmd.AddCommand(gatewayStatusCmd, gatewayKeyCmd, gatewayKeysCmd, gatewayRevokeCmd, gatewayAddCmd, gatewayBackendsCmd, gatewayDefaultCmd, gatewayPolicyCmd)

	gatewayStatusCmd.Flags().String("output", "table", "Output format: table or json")
	gatewayKeyCmd.Flags().String("output", "env", "Output format: env or json")
	gatewayKeysCmd.Flags().String("output", "table", "Output format: table or json")
	gatewayAddCmd.Flags().String("key", "", "Upstream provider API key")
	gatewayAddCmd.Flags().String("base-url", "", "Upstream provider base URL")
	gatewayAddCmd.Flags().String("name", "", "Backend display name")
	gatewayAddCmd.Flags().String("slug", "", "Backend slug")
	gatewayAddCmd.Flags().Bool("set-default", false, "Set this backend as the workspace default")
	gatewayAddCmd.Flags().String("output", "table", "Output format: table or json")
	gatewayBackendsCmd.Flags().String("output", "table", "Output format: table or json")
	gatewayDefaultCmd.Flags().String("output", "table", "Output format: table or json")
	gatewayPolicyCmd.Flags().String("output", "table", "Output format: table or json")
}
```

Also define CLI DTOs in the same file:

```go
type gatewayBackendDTO struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	DisplayName    string `json:"display_name"`
	BackendType    string `json:"backend_type"`
	BaseURL        string `json:"base_url"`
	CredentialHint string `json:"credential_hint"`
	Enabled        bool   `json:"enabled"`
	IsDefault      bool   `json:"is_default"`
}

type gatewayStatusDTO struct {
	OpenAIBaseURL       string             `json:"openai_base_url"`
	AnthropicBaseURL   string             `json:"anthropic_base_url"`
	CapturePolicy      string             `json:"capture_policy"`
	DefaultBackend     *gatewayBackendDTO `json:"default_backend"`
	BackendCount       int                `json:"backend_count"`
	EnabledBackendCount int               `json:"enabled_backend_count"`
	HasActiveKey       bool               `json:"has_active_key"`
}

type gatewayKeyDTO struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	KeyPrefix        string  `json:"key_prefix"`
	OpenAIBaseURL    string  `json:"openai_base_url"`
	OpenAIAPIKey     string  `json:"openai_api_key"`
	AnthropicBaseURL string  `json:"anthropic_base_url"`
	AnthropicAPIKey  string  `json:"anthropic_api_key"`
	CreatedAt        string  `json:"created_at"`
	LastUsedAt        *string `json:"last_used_at"`
}

type gatewayKeyListItemDTO struct {
	ID         string  `json:"id"`
	KeyPrefix  string  `json:"key_prefix"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}
```

Implement run functions in the same file:

- `gatewayClient(cmd)`: call `newAPIClient(cmd)`, require workspace ID with `requireWorkspaceID`, and return a client with `WorkspaceID` populated.
- `runGatewayKey`: `POST /api/gateway/key`; for `--output env`, print exactly `OPENAI_BASE_URL`, `OPENAI_API_KEY`, `ANTHROPIC_BASE_URL`, and `ANTHROPIC_API_KEY`; for JSON use `cli.PrintJSON`.
- `runGatewayStatus`: `GET /api/gateway/status`; table output prints base URLs, capture policy, default backend slug or `none`, backend counts, and active key yes/no.
- `runGatewayKeys`: `GET /api/gateway/keys`; table columns `ID`, `PREFIX`, `CREATED`, `LAST USED`, `REVOKED`.
- `runGatewayRevoke`: `POST /api/gateway/keys/{id}/revoke`; table output prints `Revoked <key-id>`.
- `runGatewayAdd`: validate provider; require `--key` unless provider is `claude-oauth`; POST `/api/gateway/backends`; never print the raw upstream key.
- `runGatewayBackends`: `GET /api/gateway/backends`; table columns `SLUG`, `TYPE`, `BASE URL`, `ENABLED`, `DEFAULT`, `KEY`.
- `runGatewayDefault`: `POST /api/gateway/default` with `backend_slug`.
- `runGatewayPolicy`: validate input is one of `metadata_only`, `redacted_content`, `full_content`, then `POST /api/gateway/policy`.

Use `context.WithTimeout(context.Background(), 15*time.Second)` in every run function. Use `cmd.OutOrStdout()` for all normal output.

- [ ] **Step 5: Run CLI tests and fix compile issues**

Run:

```bash
cd server && go test ./cmd/multica -run Gateway -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit CLI implementation**

Run:

```bash
git add server/internal/cli/client.go server/cmd/multica/cmd_gateway.go server/cmd/multica/cmd_gateway_test.go
git commit -m "feat: add multica gateway commands"
```

Expected: commit succeeds.

## Task 6: CLI Registration And Compatibility

**Files:**
- Modify: `server/cmd/multica/main.go`
- Modify: `server/cmd/multica/cmd_compat_test.go`
- Modify: `server/cmd/multica/cmd_gateway_test.go`

- [ ] **Step 1: Write or extend command registration test**

In `server/cmd/multica/cmd_gateway_test.go`, add:

```go
func TestGatewayCommandTree(t *testing.T) {
	want := []string{"status", "key", "keys", "revoke", "add", "backends", "default", "policy"}
	for _, name := range want {
		if cmd, _, err := gatewayCmd.Find([]string{name}); err != nil || cmd == nil || cmd.Name() != name {
			t.Fatalf("gateway subcommand %q not found: cmd=%v err=%v", name, cmd, err)
		}
	}
}
```

If `server/cmd/multica/cmd_compat_test.go` has an explicit expected command list, add `gateway` to that list. Do not remove existing commands from compatibility coverage.

- [ ] **Step 2: Run command registration tests and verify they fail**

Run:

```bash
cd server && go test ./cmd/multica -run 'GatewayCommandTree|Compat|Command' -count=1
```

Expected: FAIL until `gatewayCmd` is registered on `rootCmd` or compatibility expected lists are updated.

- [ ] **Step 3: Register `gateway` as a core command**

Modify `server/cmd/multica/main.go`:

```go
gatewayCmd.GroupID = groupCore
```

Add it to root command registration near the other core commands:

```go
rootCmd.AddCommand(gatewayCmd)
```

Keep help initialization after all `AddCommand` calls.

- [ ] **Step 4: Run CLI package tests**

Run:

```bash
cd server && go test ./cmd/multica -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit registration**

Run:

```bash
git add server/cmd/multica/main.go server/cmd/multica/cmd_compat_test.go server/cmd/multica/cmd_gateway_test.go
git commit -m "feat: register gateway cli command"
```

Expected: commit succeeds.

## Task 7: End-To-End Verification

**Files:**
- No planned source edits.

- [ ] **Step 1: Run generated query compile check**

Run:

```bash
make sqlc
```

Expected: succeeds and produces no unexpected changes. If `make sqlc` changes generated files, inspect the diff and commit generated changes only if the implementation modified SQL query files.

- [ ] **Step 2: Run focused Gateway tests**

Run:

```bash
cd server && go test ./internal/gateway/... ./internal/handler -run Gateway -count=1
```

Expected: PASS.

- [ ] **Step 3: Run server and CLI focused tests**

Run:

```bash
cd server && go test ./cmd/server -run Gateway -count=1
cd server && go test ./cmd/multica -run Gateway -count=1
```

Expected: both commands PASS.

- [ ] **Step 4: Run broader backend test suite**

Run:

```bash
cd server && go test ./...
```

Expected: PASS. If integration tests skip because PostgreSQL is unavailable, record the skip output in the final handoff and make sure pure unit tests passed.

- [ ] **Step 5: Inspect git status**

Run:

```bash
git status --short
```

Expected: clean worktree. If source files changed during verification, inspect and commit intentional changes.

## Acceptance Criteria

- `multica login` remains the login entry point. This plan only adds commands under `multica gateway`.
- `multica gateway key` prints OpenAI-compatible and Anthropic-compatible environment variables that use the Observer Gateway key.
- User gateway keys are encrypted at rest, hash-indexed for future proxy authentication, and retrievable through authenticated API only for the owning user.
- Admin backend credentials are encrypted at rest and are never returned by API handlers or CLI output.
- Admins can add OpenAI, Groq, OpenRouter, local OpenAI-compatible, Anthropic, and Claude OAuth backends.
- Admins can set default backend and capture policy.
- Non-admin workspace members can list backends and retrieve their own key, but cannot create/update/delete backends or change policy/defaults through routed API.
- Capture policy defaults to `redacted_content`.
- Backend/key/policy/default changes write `ai_audit_log` records.
- All new API routes are workspace-scoped and use existing Multica auth and workspace middleware.
- No hosted model proxy behavior is added in this phase.

## Final Manual Smoke Test

After all automated tests pass, run against a local server with a migrated database and `MULTICA_GATEWAY_SECRET_KEY` set to a base64-encoded 32-byte key:

```bash
multica login --server-url http://localhost:8080
multica gateway add local --key=anything --base-url=http://127.0.0.1:11434/v1
multica gateway default local
multica gateway policy redacted_content
multica gateway key
multica gateway status
```

Expected `multica gateway key` output:

```bash
OPENAI_BASE_URL=http://localhost:8080/v1
OPENAI_API_KEY=mgw_<generated>
ANTHROPIC_BASE_URL=http://localhost:8080
ANTHROPIC_API_KEY=mgw_<generated>
```

Expected `multica gateway status` output includes:

```text
Capture policy: redacted_content
Default backend: local
OpenAI base URL: http://localhost:8080/v1
Anthropic base URL: http://localhost:8080
Active key: yes
```
