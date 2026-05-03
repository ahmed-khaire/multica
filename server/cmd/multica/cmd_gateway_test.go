package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

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

func gatewayTestRoot(t *testing.T, serverURL string) *cobra.Command {
	t.Helper()
	t.Setenv("MULTICA_SERVER_URL", serverURL)
	t.Setenv("HOME", t.TempDir())
	resetGatewayCommandFlags(t)

	root := &cobra.Command{
		Use:           "multica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("server-url", "", "")
	root.PersistentFlags().String("workspace-id", "", "")
	root.PersistentFlags().String("profile", "", "")
	root.AddCommand(gatewayCmd)
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

func resetGatewayCommandFlags(t *testing.T) {
	t.Helper()

	var reset func(cmd *cobra.Command)
	reset = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			if err := flag.Value.Set(flag.DefValue); err != nil {
				t.Fatalf("reset flag %s: %v", flag.Name, err)
			}
			flag.Changed = false
		})
		for _, child := range cmd.Commands() {
			reset(child)
		}
	}
	reset(gatewayCmd)
}
