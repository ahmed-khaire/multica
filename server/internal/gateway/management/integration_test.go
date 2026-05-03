package management

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const managementTestGatewaySecret = "0123456789abcdef0123456789abcdef"

type managementTestFixture struct {
	userID      string
	workspaceID string
	email       string
	slug        string
}

func openManagementTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	explicitDatabaseURL := dbURL != ""
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		if explicitDatabaseURL {
			t.Fatalf("database unavailable from DATABASE_URL: %v", err)
		}
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		if explicitDatabaseURL {
			t.Fatalf("database unreachable from DATABASE_URL: %v", err)
		}
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setupManagementTestFixture(t *testing.T, pool *pgxpool.Pool) managementTestFixture {
	t.Helper()

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	fixture := managementTestFixture{
		email: "management-service-" + suffix + "@multica.ai",
		slug:  "management-service-" + suffix,
	}

	cleanupManagementTestFixture(t, pool, fixture.slug, fixture.email)
	t.Cleanup(func() {
		cleanupManagementTestFixture(t, pool, fixture.slug, fixture.email)
	})

	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Management Service Test", fixture.email).Scan(&fixture.userID); err != nil {
		t.Fatalf("create test user: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Management Service Test", fixture.slug, "Temporary workspace for gateway management tests", "MGT").Scan(&fixture.workspaceID); err != nil {
		t.Fatalf("create test workspace: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatalf("create test member: %v", err)
	}

	return fixture
}

func cleanupManagementTestFixture(t *testing.T, pool *pgxpool.Pool, slug, email string) {
	t.Helper()

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug); err != nil {
		t.Fatalf("cleanup workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email); err != nil {
		t.Fatalf("cleanup user: %v", err)
	}
}

func TestManagementServiceKeyFlow(t *testing.T) {
	ctx := context.Background()
	pool := openManagementTestDB(t)
	fixture := setupManagementTestFixture(t, pool)
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(managementTestGatewaySecret)))

	svc := NewService(db.New(pool), pool)

	first, err := svc.GetOrCreateUserKey(ctx, fixture.workspaceID, fixture.userID, "https://api.multica.ai")
	if err != nil {
		t.Fatalf("GetOrCreateUserKey first returned error: %v", err)
	}
	if first.Key == "" {
		t.Fatal("expected raw key on first creation")
	}
	if first.OpenAIBaseURL != "https://api.multica.ai/v1" || first.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("unexpected gateway URLs: %+v", first)
	}
	if first.OpenAIAPIKey != first.Key || first.AnthropicAPIKey != first.Key {
		t.Fatal("expected protocol API keys to match generated raw key")
	}

	second, err := svc.GetOrCreateUserKey(ctx, fixture.workspaceID, fixture.userID, "https://api.multica.ai/")
	if err != nil {
		t.Fatalf("GetOrCreateUserKey second returned error: %v", err)
	}
	if second.Key != first.Key {
		t.Fatalf("second key = %q, want same key %q", second.Key, first.Key)
	}
	if second.ID != first.ID {
		t.Fatalf("second key ID = %q, want %q", second.ID, first.ID)
	}

	keys, err := svc.ListUserKeys(ctx, fixture.workspaceID, fixture.userID)
	if err != nil {
		t.Fatalf("ListUserKeys returned error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("ListUserKeys returned %d keys, want 1", len(keys))
	}
	if keys[0].KeyPrefix == "" || keys[0].RevokedAt != nil {
		t.Fatalf("unexpected key list item: %+v", keys[0])
	}

	revoked, err := svc.RevokeUserKey(ctx, fixture.workspaceID, fixture.userID, first.ID)
	if err != nil {
		t.Fatalf("RevokeUserKey returned error: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatalf("expected revoked timestamp: %+v", revoked)
	}
}

func TestManagementServiceGetOrCreateUserKeyConcurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := openManagementTestDB(t)
	fixture := setupManagementTestFixture(t, pool)
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(managementTestGatewaySecret)))

	svc := NewService(db.New(pool), pool)
	const workers = 8

	type result struct {
		response UserKeyResponse
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, err := svc.GetOrCreateUserKey(ctx, fixture.workspaceID, fixture.userID, "https://api.multica.ai")
			results <- result{response: response, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var wantKey string
	for result := range results {
		if result.err != nil {
			if strings.Contains(result.err.Error(), "23505") || strings.Contains(result.err.Error(), "duplicate key") {
				t.Fatalf("GetOrCreateUserKey surfaced unique-constraint error: %v", result.err)
			}
			t.Fatalf("GetOrCreateUserKey returned error: %v", result.err)
		}
		if result.response.Key == "" {
			t.Fatal("GetOrCreateUserKey returned empty key")
		}
		if wantKey == "" {
			wantKey = result.response.Key
			continue
		}
		if result.response.Key != wantKey {
			t.Fatalf("GetOrCreateUserKey returned key %q, want %q", result.response.Key, wantKey)
		}
	}
}

func TestManagementServiceBackendFlow(t *testing.T) {
	ctx := context.Background()
	pool := openManagementTestDB(t)
	fixture := setupManagementTestFixture(t, pool)
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(managementTestGatewaySecret)))

	svc := NewService(db.New(pool), pool)

	backend, err := svc.CreateBackend(ctx, CreateBackendInput{
		WorkspaceID: fixture.workspaceID,
		ActorUserID: fixture.userID,
		Provider:    "groq",
		Key:         "gsk_1234567890abcdef",
		Enabled:     true,
		Metadata:    map[string]any{"routing": "default"},
	})
	if err != nil {
		t.Fatalf("CreateBackend returned error: %v", err)
	}
	if backend.Slug != "groq" {
		t.Fatalf("backend slug = %q, want groq", backend.Slug)
	}
	if backend.BaseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("backend base URL = %q, want Groq preset URL", backend.BaseURL)
	}
	if backend.CredentialHint != "gsk_1234...cdef" {
		t.Fatalf("credential hint = %q, want gsk_1234...cdef", backend.CredentialHint)
	}
	if backend.Metadata["routing"] != "default" {
		t.Fatalf("metadata = %+v, want routing default", backend.Metadata)
	}

	var defaultAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM ai_audit_log
		WHERE workspace_id = $1
		  AND action = 'gateway.default_backend.update'
	`, fixture.workspaceID).Scan(&defaultAuditCount); err != nil {
		t.Fatalf("count default backend audit rows: %v", err)
	}
	if defaultAuditCount != 1 {
		t.Fatalf("default backend audit count = %d, want 1", defaultAuditCount)
	}

	status, err := svc.Status(ctx, fixture.workspaceID, fixture.userID, "https://api.multica.ai")
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.CapturePolicy != CaptureRedactedContent {
		t.Fatalf("capture policy = %q, want %q", status.CapturePolicy, CaptureRedactedContent)
	}
	if status.DefaultBackend == nil || status.DefaultBackend.Slug != "groq" {
		t.Fatalf("default backend = %+v, want groq", status.DefaultBackend)
	}
	if status.BackendCount != 1 || status.EnabledBackendCount != 1 {
		t.Fatalf("backend counts = %d/%d, want 1/1", status.BackendCount, status.EnabledBackendCount)
	}

	settings, err := svc.UpdateCapturePolicy(ctx, CapturePolicyInput{
		WorkspaceID:   fixture.workspaceID,
		ActorUserID:   fixture.userID,
		CapturePolicy: CaptureMetadataOnly,
	})
	if err != nil {
		t.Fatalf("UpdateCapturePolicy returned error: %v", err)
	}
	if settings.CapturePolicy != CaptureMetadataOnly {
		t.Fatalf("settings capture policy = %q, want %q", settings.CapturePolicy, CaptureMetadataOnly)
	}
	settings, err = svc.Settings(ctx, fixture.workspaceID)
	if err != nil {
		t.Fatalf("Settings returned error: %v", err)
	}
	if settings.CapturePolicy != CaptureMetadataOnly {
		t.Fatalf("persisted settings capture policy = %q, want %q", settings.CapturePolicy, CaptureMetadataOnly)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM ai_audit_log
		WHERE workspace_id = $1
		  AND action IN ('gateway.backend.create', 'gateway.default_backend.update', 'gateway.policy.update')
	`, fixture.workspaceID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 3 {
		t.Fatalf("audit count = %d, want 3", auditCount)
	}
}

func TestManagementServiceRejectsMalformedIDsBeforeDBAccess(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil, nil)
	validWorkspaceID := "00000000-0000-0000-0000-000000000001"
	validUserID := "00000000-0000-0000-0000-000000000002"

	_, err := svc.Settings(ctx, "not-a-uuid")
	if !errors.Is(err, ErrInvalidGatewayBackend) {
		t.Fatalf("Settings malformed workspace ID error = %v, want ErrInvalidGatewayBackend", err)
	}

	_, err = svc.RevokeUserKey(ctx, validWorkspaceID, "not-a-uuid", "00000000-0000-0000-0000-000000000003")
	if !errors.Is(err, ErrInvalidGatewayBackend) {
		t.Fatalf("RevokeUserKey malformed user ID error = %v, want ErrInvalidGatewayBackend", err)
	}

	_, err = svc.RevokeUserKey(ctx, validWorkspaceID, validUserID, "not-a-uuid")
	if !errors.Is(err, ErrInvalidGatewayBackend) {
		t.Fatalf("RevokeUserKey malformed key ID error = %v, want ErrInvalidGatewayBackend", err)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505"})
	if !isUniqueViolation(err) {
		t.Fatalf("isUniqueViolation(%v) = false, want true", err)
	}
	if isUniqueViolation(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("isUniqueViolation returned true for non-unique pg error")
	}
	if isUniqueViolation(errors.New("plain error")) {
		t.Fatal("isUniqueViolation returned true for non-pg error")
	}
}

func TestNormalizeCreateBackendInputAllowsCustomBackendWithoutKey(t *testing.T) {
	normalized, credential, err := normalizeCreateBackendInput(CreateBackendInput{
		WorkspaceID: "workspace-id",
		ActorUserID: "user-id",
		Slug:        "custom",
		DisplayName: "Custom Backend",
		BackendType: BackendTypeOpenAICompatible,
		BaseURL:     "https://custom.example.com/v1",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("normalizeCreateBackendInput returned error: %v", err)
	}
	if credential != "" {
		t.Fatalf("credential = %q, want empty string", credential)
	}
	if normalized.Slug != "custom" {
		t.Fatalf("slug = %q, want custom", normalized.Slug)
	}
	if normalized.BackendType != BackendTypeOpenAICompatible {
		t.Fatalf("backend type = %q, want %q", normalized.BackendType, BackendTypeOpenAICompatible)
	}
}
