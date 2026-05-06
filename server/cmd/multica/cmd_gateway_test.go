package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGatewayCommandTree(t *testing.T) {
	for _, name := range []string{"status", "doctor", "key", "keys", "revoke", "ingest-key", "ingest-keys", "revoke-ingest-key", "add", "backends", "credentials", "credential", "export", "default", "policy"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := gatewayCmd.Find([]string{name})
			if err != nil {
				t.Fatalf("expected gateway %s command to exist: %v", name, err)
			}
			if cmd == nil {
				t.Fatalf("expected gateway %s command to exist", name)
			}
			if cmd.Name() != name {
				t.Fatalf("command name = %q, want %q", cmd.Name(), name)
			}
		})
	}
}

func TestGatewayExportCommandCallsAPI(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/export" {
			t.Errorf("path = %s, want /api/gateway/export", r.URL.Path)
		}
		if r.URL.Query().Get("since") != "24h" || r.URL.Query().Get("limit") != "10" {
			t.Errorf("query = %s, want since=24h&limit=10", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"generated_at":     "2026-05-06T12:00:00Z",
			"workspace_id":     "workspace-1",
			"overview":         map[string]any{"summary": map[string]any{"request_count": 3}},
			"sessions":         map[string]any{"sessions": []any{}},
			"llm_calls":        map[string]any{"calls": []any{}},
			"policy_decisions": []any{},
			"evidence":         []any{},
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "export", "--workspace-id", "workspace-1", "--since", "24h", "--limit", "10")
	if err != nil {
		t.Fatalf("execute gateway export: %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
	if !strings.Contains(out, `"workspace_id": "workspace-1"`) {
		t.Fatalf("output = %q, want export JSON", out)
	}
}

func TestGatewayCredentialAddCommandCallsAPI(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/backends/backend-1/credentials" {
			t.Errorf("path = %s, want /api/gateway/backends/backend-1/credentials", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              "credential-1",
			"backend_id":      "backend-1",
			"label":           "prod pool",
			"credential_hint": "sk-or-po...cdef",
			"enabled":         true,
			"priority":        5,
			"created_at":      "2026-05-06T12:00:00Z",
			"updated_at":      "2026-05-06T12:00:00Z",
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(
		root,
		"gateway", "credential", "add", "backend-1",
		"--workspace-id", "workspace-1",
		"--key", "sk-or-pooled-1234567890abcdef",
		"--label", "prod pool",
		"--priority", "5",
	)
	if err != nil {
		t.Fatalf("execute gateway credential add: %v", err)
	}
	if gotBody["key"] != "sk-or-pooled-1234567890abcdef" {
		t.Fatalf("key = %v, want pooled key", gotBody["key"])
	}
	if gotBody["label"] != "prod pool" || gotBody["priority"] != float64(5) {
		t.Fatalf("body = %#v, want label and priority", gotBody)
	}
	if !strings.Contains(out, "Added credential credential-1") {
		t.Fatalf("output = %q, want added credential message", out)
	}
	if strings.Contains(out, "sk-or-pooled-1234567890abcdef") {
		t.Fatalf("output leaked raw key: %q", out)
	}
}

func TestGatewayCredentialsCommandListsBackendCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/backends/backend-1/credentials" {
			t.Errorf("path = %s, want /api/gateway/backends/backend-1/credentials", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id":              "credential-1",
			"backend_id":      "backend-1",
			"label":           "prod pool",
			"credential_hint": "sk-or-po...cdef",
			"enabled":         true,
			"priority":        5,
			"created_at":      "2026-05-06T12:00:00Z",
			"updated_at":      "2026-05-06T12:00:00Z",
		}})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "credentials", "backend-1", "--workspace-id", "workspace-1", "--output", "json")
	if err != nil {
		t.Fatalf("execute gateway credentials: %v", err)
	}

	var credentials []map[string]any
	if err := json.Unmarshal([]byte(out), &credentials); err != nil {
		t.Fatalf("decode output: %v\noutput: %s", err, out)
	}
	if len(credentials) != 1 || credentials[0]["credential_hint"] != "sk-or-po...cdef" {
		t.Fatalf("unexpected credentials output: %#v", credentials)
	}
	if _, ok := credentials[0]["key"]; ok {
		t.Fatalf("credentials output leaked key field: %#v", credentials[0])
	}
}

func TestGatewayTestRootDoesNotReparentProductionCommand(t *testing.T) {
	if gatewayCmd.Parent() != rootCmd {
		t.Fatalf("gatewayCmd parent before helper = %p, want rootCmd %p", gatewayCmd.Parent(), rootCmd)
	}

	root := gatewayTestRoot(t, "http://127.0.0.1")
	if _, err := executeGatewayTestCommand(root, "gateway", "--help"); err != nil {
		t.Fatalf("execute gateway help: %v", err)
	}

	if gatewayCmd.Parent() != rootCmd {
		t.Fatalf("gatewayCmd parent after helper execution = %p, want rootCmd %p", gatewayCmd.Parent(), rootCmd)
	}
}

func TestGatewayDoctorCommandShowsHealthReport(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/doctor" {
			t.Errorf("path = %s, want /api/gateway/doctor", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":       "healthy_with_warnings",
			"generated_at": "2026-05-05T10:00:00Z",
			"checks": []map[string]any{
				{
					"id":          "gateway_key",
					"category":    "workspace",
					"status":      "pass",
					"title":       "User Gateway key",
					"detail":      "This user has an active Gateway key.",
					"remediation": "",
				},
				{
					"id":          "open_incidents",
					"category":    "governance",
					"status":      "warning",
					"title":       "Open incidents",
					"detail":      "2 Gateway incident(s) need review.",
					"remediation": "Review and remediate open Gateway incidents.",
				},
			},
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "doctor", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway doctor: %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
	if !strings.Contains(out, "Observer Gateway Doctor") {
		t.Fatalf("output = %q, want doctor heading", out)
	}
	if !strings.Contains(out, "Result: healthy_with_warnings") {
		t.Fatalf("output = %q, want overall result", out)
	}
	if !strings.Contains(out, "User Gateway key") || !strings.Contains(out, "Open incidents") {
		t.Fatalf("output = %q, want check titles", out)
	}
	if !strings.Contains(out, "Review and remediate open Gateway incidents.") {
		t.Fatalf("output = %q, want remediation", out)
	}
}

func TestGatewayKeyCommandPrintsEnv(t *testing.T) {
	var called bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/key" {
			t.Errorf("path = %s, want /api/gateway/key", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                 "key-1",
			"key":                "mgw_123",
			"key_prefix":         "mgw_123",
			"openai_base_url":    srv.URL + "/v1",
			"openai_api_key":     "mgw_123",
			"anthropic_base_url": srv.URL,
			"anthropic_api_key":  "mgw_123",
			"created_at":         "2026-05-03T12:00:00Z",
			"last_used_at":       nil,
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "key", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway key: %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}

	want := strings.Join([]string{
		"OPENAI_BASE_URL=" + srv.URL + "/v1",
		"OPENAI_API_KEY=mgw_123",
		"ANTHROPIC_BASE_URL=" + srv.URL,
		"ANTHROPIC_API_KEY=mgw_123",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestGatewayIngestKeyCommandPrintsEnv(t *testing.T) {
	var gotBody map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/ingest-keys" {
			t.Errorf("path = %s, want /api/gateway/ingest-keys", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":               "ingest-key-1",
			"key":              "mig_123",
			"key_prefix":       "mig_123",
			"app_id":           "checkout",
			"display_name":     "Checkout API",
			"gateway_base_url": srv.URL,
			"created_at":       "2026-05-03T12:00:00Z",
			"last_used_at":     nil,
			"revoked_at":       nil,
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(
		root,
		"gateway", "ingest-key",
		"--workspace-id", "workspace-1",
		"--app-id", "checkout",
		"--name", "Checkout API",
	)
	if err != nil {
		t.Fatalf("execute gateway ingest-key: %v", err)
	}

	if gotBody["app_id"] != "checkout" {
		t.Fatalf("app_id = %v, want checkout", gotBody["app_id"])
	}
	if gotBody["display_name"] != "Checkout API" {
		t.Fatalf("display_name = %v, want Checkout API", gotBody["display_name"])
	}
	want := strings.Join([]string{
		"MULTICA_OBSERVER_GATEWAY_BASE_URL=" + srv.URL,
		"MULTICA_OBSERVER_KEY=mig_123",
		"MULTICA_OBSERVER_APP_ID=checkout",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestGatewayIngestKeysCommandListsKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/ingest-keys" {
			t.Errorf("path = %s, want /api/gateway/ingest-keys", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id":           "ingest-key-1",
			"key_prefix":   "mig_123",
			"app_id":       "checkout",
			"display_name": "Checkout API",
			"created_at":   "2026-05-03T12:00:00Z",
			"last_used_at": nil,
			"revoked_at":   nil,
		}})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "ingest-keys", "--workspace-id", "workspace-1", "--output", "json")
	if err != nil {
		t.Fatalf("execute gateway ingest-keys: %v", err)
	}

	var keys []map[string]any
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("decode output: %v\noutput: %s", err, out)
	}
	if len(keys) != 1 || keys[0]["app_id"] != "checkout" {
		t.Fatalf("unexpected ingest keys output: %#v", keys)
	}
}

func TestGatewayRevokeIngestKeyCommandCallsAPI(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/ingest-keys/ingest-key-1/revoke" {
			t.Errorf("path = %s, want /api/gateway/ingest-keys/ingest-key-1/revoke", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "ingest-key-1",
			"key_prefix":   "mig_123",
			"app_id":       "checkout",
			"display_name": "Checkout API",
			"created_at":   "2026-05-03T12:00:00Z",
			"last_used_at": nil,
			"revoked_at":   "2026-05-03T12:05:00Z",
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "revoke-ingest-key", "ingest-key-1", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway revoke-ingest-key: %v", err)
	}
	if !called {
		t.Fatal("server was not called")
	}
	if !strings.Contains(out, "Revoked ingest-key-1") {
		t.Fatalf("output = %q, want revoked message", out)
	}
}

func TestGatewayAddCommandSendsProviderPayload(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/backends" {
			t.Errorf("path = %s, want /api/gateway/backends", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              "backend-1",
			"slug":            "groq",
			"display_name":    "Groq",
			"backend_type":    "openai_compatible",
			"base_url":        "https://api.groq.com/openai/v1",
			"credential_hint": "gsk_...cdef",
			"enabled":         true,
			"is_default":      false,
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(
		root,
		"gateway", "add", "groq",
		"--key", "gsk_1234567890abcdef",
		"--base-url", "https://api.groq.com/openai/v1",
		"--workspace-id", "workspace-1",
	)
	if err != nil {
		t.Fatalf("execute gateway add: %v", err)
	}

	if gotBody["provider"] != "groq" {
		t.Fatalf("provider = %v, want groq", gotBody["provider"])
	}
	if gotBody["key"] != "gsk_1234567890abcdef" {
		t.Fatalf("key = %v, want gsk_1234567890abcdef", gotBody["key"])
	}
	if gotBody["base_url"] != "https://api.groq.com/openai/v1" {
		t.Fatalf("base_url = %v, want https://api.groq.com/openai/v1", gotBody["base_url"])
	}
	if !strings.Contains(out, "groq") {
		t.Fatalf("output %q does not include groq", out)
	}
	if strings.Contains(out, "gsk_1234567890abcdef") {
		t.Fatalf("output leaked raw key: %q", out)
	}
}

func TestGatewayAddRequiresKeyForCredentialProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called for missing provider key")
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	_, err := executeGatewayTestCommand(root, "gateway", "add", "openai", "--workspace-id", "workspace-1")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if got := err.Error(); !strings.Contains(got, "--key is required") {
		t.Fatalf("error = %q, want --key is required", got)
	}
}

func TestGatewayAddClaudeOAuthAllowsMissingKey(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/gateway/backends" {
			t.Errorf("path = %s, want /api/gateway/backends", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":              "backend-1",
			"slug":            "claude-oauth",
			"display_name":    "Claude OAuth",
			"backend_type":    "claude_oauth",
			"base_url":        "claude-oauth://sidecar",
			"credential_hint": "",
			"enabled":         true,
			"is_default":      false,
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "add", "claude-oauth", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway add claude-oauth: %v", err)
	}

	if gotBody["provider"] != "claude-oauth" {
		t.Fatalf("provider = %v, want claude-oauth", gotBody["provider"])
	}
	if _, ok := gotBody["key"]; ok {
		t.Fatalf("body included key for claude-oauth: %#v", gotBody)
	}
	if !strings.Contains(out, "claude-oauth") {
		t.Fatalf("output %q does not include claude-oauth", out)
	}
}

func TestGatewayBackendsJSONPreservesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/backends" {
			t.Errorf("path = %s, want /api/gateway/backends", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id":              "backend-1",
			"slug":            "groq",
			"display_name":    "Groq",
			"backend_type":    "openai_compatible",
			"base_url":        "https://api.groq.com/openai/v1",
			"credential_hint": "gsk_...cdef",
			"enabled":         true,
			"is_default":      false,
			"metadata": map[string]any{
				"region": "iad",
				"tier":   "prod",
			},
		}})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	out, err := executeGatewayTestCommand(root, "gateway", "backends", "--workspace-id", "workspace-1", "--output", "json")
	if err != nil {
		t.Fatalf("execute gateway backends: %v", err)
	}

	var backends []map[string]any
	if err := json.Unmarshal([]byte(out), &backends); err != nil {
		t.Fatalf("decode output: %v\noutput: %s", err, out)
	}
	if len(backends) != 1 {
		t.Fatalf("len(backends) = %d, want 1", len(backends))
	}
	metadata, ok := backends[0]["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing or wrong type in output: %#v", backends[0])
	}
	if metadata["region"] != "iad" || metadata["tier"] != "prod" {
		t.Fatalf("metadata = %#v, want region=iad tier=prod", metadata)
	}
}

func gatewayTestRoot(t *testing.T, serverURL string) *cobra.Command {
	t.Helper()
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("HOME", t.TempDir())

	root := &cobra.Command{
		Use:           "multica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("server-url", "", "")
	root.PersistentFlags().String("workspace-id", "", "")
	root.PersistentFlags().String("profile", "", "")
	root.AddGroup(&cobra.Group{ID: groupCore, Title: "CORE COMMANDS"})

	gateway := newGatewayCommand()
	gateway.GroupID = groupCore
	root.AddCommand(gateway)
	return root
}

func executeGatewayTestCommand(root *cobra.Command, args ...string) (string, error) {
	var out strings.Builder
	var errOut strings.Builder
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	err := root.Execute()
	if out.Len() == 0 && errOut.Len() > 0 {
		return errOut.String(), err
	}
	return out.String(), err
}
