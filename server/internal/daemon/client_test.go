package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientClaimGatewayJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertDaemonRequest(t, r, "/api/daemon/runtimes/runtime-1/gateway/jobs/claim")
		writeTestJSON(t, w, map[string]any{
			"job": map[string]any{
				"id":                    "job-1",
				"type":                  "gateway_subscription_validation",
				"workspace_id":          "workspace-1",
				"backend_id":            "backend-1",
				"credential_id":         "credential-1",
				"subscription_provider": "codex",
				"encrypted_payload":     "ZW5jcnlwdGVk",
				"payload_format":        "codex_auth_bundle_v1",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	client.SetToken("test-token")
	job, err := client.ClaimGatewayJob(context.Background(), "runtime-1")
	if err != nil {
		t.Fatalf("ClaimGatewayJob returned error: %v", err)
	}
	if job == nil || job.ID != "job-1" || job.SubscriptionProvider != "codex" {
		t.Fatalf("job = %#v, want codex job", job)
	}
}

func TestClientGatewayValidationMethods(t *testing.T) {
	seen := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		seen[r.URL.Path] = body
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.CompleteGatewayValidation(context.Background(), "runtime-1", "validation-1", GatewayValidationResult{
		AccountHint:        "user@example.com",
		AccountFingerprint: "fingerprint",
	}); err != nil {
		t.Fatalf("CompleteGatewayValidation returned error: %v", err)
	}
	if err := client.FailGatewayValidation(context.Background(), "runtime-1", "validation-2", "invalid", "bad token"); err != nil {
		t.Fatalf("FailGatewayValidation returned error: %v", err)
	}

	complete := seen["/api/daemon/runtimes/runtime-1/gateway/validations/validation-1/complete"]
	if complete["account_hint"] != "user@example.com" || complete["account_fingerprint"] != "fingerprint" {
		t.Fatalf("complete body = %#v", complete)
	}
	failed := seen["/api/daemon/runtimes/runtime-1/gateway/validations/validation-2/fail"]
	if failed["error_code"] != "invalid" || failed["error_message"] != "bad token" {
		t.Fatalf("fail body = %#v", failed)
	}
}

func TestClientGatewayRuntimeRequestMethods(t *testing.T) {
	seen := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		seen[r.URL.Path] = body
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if err := client.CompleteGatewayRuntimeRequest(context.Background(), "runtime-1", "request-1", map[string]any{"id": "chatcmpl-test"}); err != nil {
		t.Fatalf("CompleteGatewayRuntimeRequest returned error: %v", err)
	}
	if err := client.FailGatewayRuntimeRequest(context.Background(), "runtime-1", "request-2", "runtime_error", "codex failed"); err != nil {
		t.Fatalf("FailGatewayRuntimeRequest returned error: %v", err)
	}

	complete := seen["/api/daemon/runtimes/runtime-1/gateway/requests/request-1/complete"]
	response, ok := complete["response"].(map[string]any)
	if !ok || response["id"] != "chatcmpl-test" {
		t.Fatalf("complete body = %#v", complete)
	}
	failed := seen["/api/daemon/runtimes/runtime-1/gateway/requests/request-2/fail"]
	if failed["error_type"] != "runtime_error" || failed["error_message"] != "codex failed" {
		t.Fatalf("fail body = %#v", failed)
	}
}

func assertDaemonRequest(t *testing.T, r *http.Request, path string) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", r.Method)
	}
	if r.URL.Path != path {
		t.Fatalf("path = %s, want %s", r.URL.Path, path)
	}
	if r.Header.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
