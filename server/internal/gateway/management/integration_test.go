package management

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

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
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
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

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM ai_audit_log
		WHERE workspace_id = $1
		  AND action IN ('gateway.backend.create', 'gateway.policy.update')
	`, fixture.workspaceID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("audit count = %d, want 2", auditCount)
	}
}
