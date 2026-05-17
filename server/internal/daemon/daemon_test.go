package daemon

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeServerBaseURL(t *testing.T) {
	t.Parallel()

	got, err := NormalizeServerBaseURL("ws://localhost:8080/ws")
	if err != nil {
		t.Fatalf("NormalizeServerBaseURL returned error: %v", err)
	}
	if got != "http://localhost:8080" {
		t.Fatalf("expected http://localhost:8080, got %s", got)
	}
}

func TestBuildPromptContainsIssueID(t *testing.T) {
	t.Parallel()

	issueID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	prompt := BuildPrompt(Task{
		IssueID: issueID,
		Agent: &AgentData{
			Name: "Local Codex",
			Skills: []SkillData{
				{Name: "Concise", Content: "Be concise."},
			},
		},
	})

	// Prompt should contain the issue ID and CLI hint.
	for _, want := range []string{
		issueID,
		"multica issue get",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}

	// Skills should NOT be inlined in the prompt (they're in runtime config).
	for _, absent := range []string{"## Agent Skills", "Be concise."} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("prompt should NOT contain %q (skills are in runtime config)", absent)
		}
	}
}

func TestBuildPromptNoIssueDetails(t *testing.T) {
	t.Parallel()

	prompt := BuildPrompt(Task{
		IssueID: "test-id",
		Agent:   &AgentData{Name: "Test"},
	})

	// Prompt should not contain issue title/description (agent fetches via CLI).
	for _, absent := range []string{"**Issue:**", "**Summary:**"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("prompt should NOT contain %q — agent fetches details via CLI", absent)
		}
	}
}

func TestIsWorkspaceNotFoundError(t *testing.T) {
	t.Parallel()

	err := &requestError{
		Method:     http.MethodPost,
		Path:       "/api/daemon/register",
		StatusCode: http.StatusNotFound,
		Body:       `{"error":"workspace not found"}`,
	}
	if !isWorkspaceNotFoundError(err) {
		t.Fatal("expected workspace not found error to be recognized")
	}

	if isWorkspaceNotFoundError(&requestError{StatusCode: http.StatusInternalServerError, Body: `{"error":"workspace not found"}`}) {
		t.Fatal("did not expect 500 to be treated as workspace not found")
	}
}

func TestDaemonValidateGatewaySubscription(t *testing.T) {
	t.Parallel()

	d := &Daemon{
		runtimeIndex: map[string]Runtime{
			"runtime-codex":  {ID: "runtime-codex", Provider: "codex"},
			"runtime-claude": {ID: "runtime-claude", Provider: "claude"},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if _, err := d.validateGatewaySubscription("runtime-codex", &GatewayJob{CredentialID: "credential-codex", SubscriptionProvider: "codex", Payload: "dG9rZW4="}); err != nil {
		t.Fatalf("codex validation returned error: %v", err)
	}
	if _, err := d.validateGatewaySubscription("runtime-claude", &GatewayJob{CredentialID: "credential-claude", SubscriptionProvider: "claude_code", Payload: "dG9rZW4="}); err != nil {
		t.Fatalf("claude validation returned error: %v", err)
	}
	if _, err := d.validateGatewaySubscription("runtime-codex", &GatewayJob{CredentialID: "credential-mismatch", SubscriptionProvider: "claude_code", Payload: "dG9rZW4="}); err == nil {
		t.Fatal("expected provider mismatch error")
	}
	if _, err := d.validateGatewaySubscription("runtime-codex", &GatewayJob{CredentialID: "credential-unknown", SubscriptionProvider: "unknown", Payload: "dG9rZW4="}); err == nil {
		t.Fatal("expected unsupported provider error")
	}
	if _, err := d.validateGatewaySubscription("runtime-codex", &GatewayJob{CredentialID: "credential-empty", SubscriptionProvider: "codex"}); err == nil {
		t.Fatal("expected missing payload error")
	}
}

func TestDaemonValidateGatewaySubscriptionMaterializesBundle(t *testing.T) {
	t.Parallel()

	payload := base64.StdEncoding.EncodeToString([]byte(`{
		"account_hint":"codex@example.com",
		"files":{"auth.json":"{\"token\":\"test\"}"},
		"env":{"CODEX_AUTH_FILE":"auth.json"}
	}`))
	d := &Daemon{
		cfg: Config{WorkspacesRoot: t.TempDir()},
		runtimeIndex: map[string]Runtime{
			"runtime-codex": {ID: "runtime-codex", Provider: "codex"},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	result, err := d.validateGatewaySubscription("runtime-codex", &GatewayJob{
		ID:                   "validation-1",
		CredentialID:         "credential-1",
		SubscriptionProvider: "codex",
		Payload:              payload,
		PayloadFormat:        "codex_auth_bundle_v1",
	})
	if err != nil {
		t.Fatalf("validateGatewaySubscription returned error: %v", err)
	}
	if result.AccountHint != "codex@example.com" {
		t.Fatalf("AccountHint = %q, want codex@example.com", result.AccountHint)
	}
	if result.AccountFingerprint == "" {
		t.Fatal("AccountFingerprint should be set")
	}
	if _, err := os.Stat(filepath.Join(d.cfg.WorkspacesRoot, "gateway_credentials", "credential-1", "auth.json")); err != nil {
		t.Fatalf("expected auth.json to be materialized: %v", err)
	}
	env := d.gatewayCredentialEnv(&GatewayJob{CredentialID: "credential-1", SubscriptionProvider: "codex"})
	if env["CODEX_HOME"] != filepath.Join(d.cfg.WorkspacesRoot, "gateway_credentials", "credential-1") {
		t.Fatalf("CODEX_HOME = %q, want materialized credential root", env["CODEX_HOME"])
	}
	if env["CODEX_AUTH_FILE"] != "auth.json" {
		t.Fatalf("CODEX_AUTH_FILE = %q, want auth.json", env["CODEX_AUTH_FILE"])
	}
}
