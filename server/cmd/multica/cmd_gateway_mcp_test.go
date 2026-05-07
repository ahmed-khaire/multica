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
		"gateway_evidence_bundle",
		"gateway_governance_insights",
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

func TestGatewayMCPListsResourcesAndTemplates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called while listing MCP resources")
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/templates/list","params":{}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	responses := decodeMCPResponses(t, out)
	if len(responses) != 3 {
		t.Fatalf("responses = %#v, want initialize, resources/list, and templates/list", responses)
	}

	resourceResult, ok := responses[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/list result = %#v, want object", responses[1]["result"])
	}
	rawResources, ok := resourceResult["resources"].([]any)
	if !ok {
		t.Fatalf("resources = %#v, want array", resourceResult["resources"])
	}
	resourceURIs := make(map[string]bool)
	for _, rawResource := range rawResources {
		resource, ok := rawResource.(map[string]any)
		if !ok {
			t.Fatalf("resource = %#v, want object", rawResource)
		}
		uri, _ := resource["uri"].(string)
		resourceURIs[uri] = true
		if resource["mimeType"] != "application/json" {
			t.Fatalf("resource %s mimeType = %#v, want application/json", uri, resource["mimeType"])
		}
	}
	for _, expected := range []string{"gateway://status", "gateway://health-report", "gateway://governance/insights", "gateway://governance/evidence", "gateway://governance/incidents"} {
		if !resourceURIs[expected] {
			t.Fatalf("resources missing %s; resources = %#v", expected, resourceURIs)
		}
	}

	templateResult, ok := responses[2]["result"].(map[string]any)
	if !ok {
		t.Fatalf("resources/templates/list result = %#v, want object", responses[2]["result"])
	}
	rawTemplates, ok := templateResult["resourceTemplates"].([]any)
	if !ok {
		t.Fatalf("resourceTemplates = %#v, want array", templateResult["resourceTemplates"])
	}
	templates := make(map[string]bool)
	for _, rawTemplate := range rawTemplates {
		template, ok := rawTemplate.(map[string]any)
		if !ok {
			t.Fatalf("template = %#v, want object", rawTemplate)
		}
		uriTemplate, _ := template["uriTemplate"].(string)
		templates[uriTemplate] = true
	}
	for _, expected := range []string{"gateway://sessions/{session_id}", "gateway://sessions/{session_id}/spans", "gateway://evidence-bundles/session/{session_id}"} {
		if !templates[expected] {
			t.Fatalf("templates missing %s; templates = %#v", expected, templates)
		}
	}
}

func TestGatewayMCPReadResourceFetchesAPIAndRedactsSecrets(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			"status":         "healthy",
			"openai_api_key": "sk-resource-secret",
			"total_tokens":   123,
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"gateway://health-report"}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	if !called.Load() {
		t.Fatal("server was not called")
	}
	if strings.Contains(out, "sk-resource-secret") {
		t.Fatalf("MCP resource output leaked secret: %s", out)
	}
	for _, expected := range []string{"gateway://health-report", "application/json", "[redacted]", "total_tokens"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("MCP output = %s, missing %q", out, expected)
		}
	}
}

func TestGatewayMCPGovernanceInsightsToolAndResource(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/gateway/governance/insights" {
			t.Errorf("path = %s, want /api/gateway/governance/insights", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"risk_overview": map[string]any{
				"open_incident_count": 1,
				"api_key":             "sk-insights-secret",
			},
			"action_queue": []map[string]any{{
				"kind":  "incident",
				"title": "Review provider risk",
			}},
		})
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_governance_insights","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"gateway://governance/insights"}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %#v, want tool and resource reads", calls)
	}
	if strings.Contains(out, "sk-insights-secret") {
		t.Fatalf("MCP governance insights leaked secret: %s", out)
	}
	for _, expected := range []string{"gateway://governance/insights", "open_incident_count", "Review provider risk", "[redacted]"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("MCP output = %s, missing %q", out, expected)
		}
	}
}

func TestGatewayMCPEvidenceBundleComposesReadOnlyAPIs(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Errorf("X-Workspace-ID = %q, want workspace-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.RequestURI() {
		case "/api/gateway/sessions/session-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "session-1", "api_key": "sk-bundle-secret"})
		case "/api/gateway/sessions/session-1/spans":
			_ = json.NewEncoder(w).Encode(map[string]any{"spans": []any{map[string]any{"span_id": "root"}}})
		case "/api/gateway/llm-calls?limit=25":
			_ = json.NewEncoder(w).Encode(map[string]any{"calls": []any{map[string]any{"session_id": "session-1", "request_model": "gpt-bundle"}}})
		case "/api/gateway/governance/policy-decisions?limit=25":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "decision-1", "resource_id": "gpt-bundle"}})
		case "/api/gateway/governance/evidence?limit=25":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "evidence-1", "payload": map[string]any{"session_id": "session-1"}}})
		case "/api/gateway/governance/incidents?limit=25":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "incident-1", "status": "open"}})
		case "/api/gateway/governance/provider-risks":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"provider": "openrouter", "risk_level": "medium"}})
		case "/api/gateway/governance/control-mappings":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"control_id": "GW-1", "status": "covered"}})
		case "/api/gateway/governance/policies":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "policy-1", "name": "Approved models"}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
		}
	}))
	defer srv.Close()

	root := gatewayTestRoot(t, srv.URL)
	root.SetIn(strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gateway_evidence_bundle","arguments":{"session_id":"session-1","limit":25}}}`,
	}, "\n") + "\n"))

	out, err := executeGatewayTestCommand(root, "gateway", "mcp", "--workspace-id", "workspace-1")
	if err != nil {
		t.Fatalf("execute gateway mcp: %v\noutput: %s", err, out)
	}
	for _, expectedCall := range []string{
		"GET /api/gateway/sessions/session-1",
		"GET /api/gateway/sessions/session-1/spans",
		"GET /api/gateway/llm-calls?limit=25",
		"GET /api/gateway/governance/policy-decisions?limit=25",
		"GET /api/gateway/governance/evidence?limit=25",
		"GET /api/gateway/governance/incidents?limit=25",
		"GET /api/gateway/governance/provider-risks",
		"GET /api/gateway/governance/control-mappings",
		"GET /api/gateway/governance/policies",
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
	if strings.Contains(out, "sk-bundle-secret") {
		t.Fatalf("MCP evidence bundle leaked secret: %s", out)
	}
	for _, expected := range []string{"evidence_bundle", "session_detail", "policy_decisions", "provider_risks", "control_mappings", "gpt-bundle", "[redacted]"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("MCP output = %s, missing %q", out, expected)
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
