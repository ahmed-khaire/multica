package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGatewayMCPListsReadOnlyTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called while listing MCP tools")
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}

	responses := decodeMCPResponses(t, out)
	if len(responses) != 2 {
		t.Fatalf("responses = %#v, want initialize and tools/list responses", responses)
	}
	toolsResult, ok := responses[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result = %#v, want object", responses[1]["result"])
	}
	rawTools, ok := toolsResult["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, want array", toolsResult["tools"])
	}

	names := make(map[string]bool)
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			t.Fatalf("tool = %#v, want object", rawTool)
		}
		name, _ := tool["name"].(string)
		names[name] = true
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Fatalf("tool %s annotations = %#v, want object", name, tool["annotations"])
		}
		if annotations["readOnlyHint"] != true {
			t.Fatalf("tool %s readOnlyHint = %#v, want true", name, annotations["readOnlyHint"])
		}
		if annotations["destructiveHint"] != false {
			t.Fatalf("tool %s destructiveHint = %#v, want false", name, annotations["destructiveHint"])
		}
		for _, fragment := range []string{"add", "create", "update", "delete", "revoke", "approve", "deny", "disable", "set"} {
			if strings.Contains(name, fragment) {
				t.Fatalf("MCP exposed mutating-looking tool %q", name)
			}
		}
	}

	for _, expected := range []string{
		"gateway_status",
		"gateway_doctor",
		"gateway_health_report",
		"gateway_backends",
		"gateway_overview",
		"gateway_sessions",
		"gateway_llm_calls",
		"gateway_session",
		"gateway_session_spans",
		"gateway_session_drilldown",
		"gateway_policy_decisions",
		"gateway_governance_policies",
		"gateway_policy_exceptions",
		"gateway_incidents",
		"gateway_evidence",
		"gateway_provider_risks",
		"gateway_control_mappings",
	} {
		if !names[expected] {
			t.Fatalf("MCP tools missing %s; names = %#v", expected, names)
		}
	}
}

func TestGatewayMCPSessionDrilldownFetchesDetailAndSpans(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/api/gateway/sessions/session%20with%20space":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "session with space",
				"model_calls": []map[string]any{{
					"request_model":   "gpt-drill",
					"prompt_tokens":   12,
					"completion_text": "visible according to workspace capture policy",
					"api_key":         "sk-session-secret",
				}},
			})
		case "/api/gateway/sessions/session%20with%20space/spans":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session_id": "session with space",
				"spans": []map[string]any{{
					"span_id":      "root",
					"span_name":    "agent.run",
					"access_token": "token-session-secret",
				}},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_session_drilldown","arguments":{"session_id":"session with space"}}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	for _, expectedCall := range []string{
		"GET /api/gateway/sessions/session%20with%20space",
		"GET /api/gateway/sessions/session%20with%20space/spans",
	} {
		found := false
		for _, call := range calls {
			if call == expectedCall {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing %s", calls, expectedCall)
		}
	}
	for _, leaked := range []string{"sk-session-secret", "token-session-secret"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("MCP output leaked secret %q: %s", leaked, out)
		}
	}
	for _, expected := range []string{"session_detail", "session_spans", "gpt-drill", `"prompt_tokens":12`, "[redacted]"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("MCP output = %s, missing %q", out, expected)
		}
	}
}

func TestGatewayMCPSessionToolRequiresSessionIDWithoutHTTPCall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Fatalf("server should not be called when session_id is missing")
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_session","arguments":{}}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp should return JSON-RPC error without command failure: %v\noutput: %s", err, out)
	}
	if calls.Load() != 0 {
		t.Fatalf("server calls = %d, want 0", calls.Load())
	}
	if !strings.Contains(out, "session_id is required") {
		t.Fatalf("output = %s, want session_id required error", out)
	}
}

func TestGatewayMCPToolCallFetchesHealthReportAndRedactsSecrets(t *testing.T) {
	var called atomic.Bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/health-report" {
			t.Errorf("path = %s, want /api/gateway/health-report", r.URL.Path)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":                 "healthy",
			"openai_api_key":         "sk-secret-that-must-not-leak",
			"encrypted_credential":   "ciphertext-that-must-not-leak",
			"authorization":          "Bearer token-that-must-not-leak",
			"credential_hint":        "sk-se...leak",
			"openai_base_url":        srv.URL + "/v1",
			"anthropic_base_url":     srv.URL,
			"nested":                 map[string]any{"api_key": "gsk-also-secret", "key_prefix": "mgw_123", "prompt_tokens": 42},
			"backend_credential_ids": []string{"credential-1"},
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_health_report","arguments":{}}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	if !called.Load() {
		t.Fatal("server was not called")
	}
	for _, leaked := range []string{"sk-secret-that-must-not-leak", "ciphertext-that-must-not-leak", "token-that-must-not-leak", "gsk-also-secret"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("MCP output leaked secret %q: %s", leaked, out)
		}
	}
	for _, expected := range []string{"[redacted]", "sk-se...leak", "mgw_123", "healthy", `"prompt_tokens":42`} {
		if !strings.Contains(out, expected) {
			t.Fatalf("MCP output = %s, missing %q", out, expected)
		}
	}
}

func TestGatewayMCPRejectsMutationToolWithoutHTTPCall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Fatalf("server should not be called for forbidden MCP tool")
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_create_key","arguments":{}}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp should return JSON-RPC error without command failure: %v\noutput: %s", err, out)
	}
	if calls.Load() != 0 {
		t.Fatalf("server calls = %d, want 0", calls.Load())
	}
	responses := decodeMCPResponses(t, out)
	if len(responses) != 2 {
		t.Fatalf("responses = %#v, want initialize and tool error responses", responses)
	}
	if responses[1]["error"] == nil {
		t.Fatalf("tool call response = %#v, want JSON-RPC error", responses[1])
	}
	if !strings.Contains(out, "unknown read-only Gateway MCP tool") {
		t.Fatalf("output = %s, want unknown read-only tool error", out)
	}
}

func decodeMCPResponses(t *testing.T, out string) []map[string]any {
	t.Helper()

	var responses []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode MCP response line %q: %v", line, err)
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan MCP responses: %v", err)
	}
	return responses
}
