package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/gateway/management"
)

type doctorCheckAssertion struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

func setGatewaySecret(t *testing.T) {
	t.Helper()
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
}

func assertDoctorCheck(t *testing.T, checks []doctorCheckAssertion, id, status string) {
	t.Helper()
	for _, check := range checks {
		if check.ID == id {
			if check.Status != status {
				t.Fatalf("doctor check %s status = %q, want %q", id, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("doctor check %s not found in %#v", id, checks)
}

func TestGatewayCreateKeyHandler(t *testing.T) {
	setGatewaySecret(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/key", nil)
	req.Host = "api.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")

	testHandler.CreateGatewayUserKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateGatewayUserKey: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Key              string `json:"key"`
		OpenAIBaseURL    string `json:"openai_base_url"`
		AnthropicBaseURL string `json:"anthropic_base_url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("CreateGatewayUserKey: failed to decode response: %v", err)
	}
	if resp.Key == "" {
		t.Fatal("CreateGatewayUserKey: expected non-empty key")
	}
	if resp.OpenAIBaseURL != "https://api.multica.ai/v1" {
		t.Fatalf("CreateGatewayUserKey: openai_base_url = %q, want %q", resp.OpenAIBaseURL, "https://api.multica.ai/v1")
	}
	if resp.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("CreateGatewayUserKey: anthropic_base_url = %q, want %q", resp.AnthropicBaseURL, "https://api.multica.ai")
	}
}

func TestGatewayDoctorHandlerReportsHealthyWithWarnings(t *testing.T) {
	setGatewaySecret(t)

	keyW := httptest.NewRecorder()
	keyReq := newRequest("POST", "/api/gateway/key", nil)
	testHandler.CreateGatewayUserKey(keyW, keyReq)
	if keyW.Code != http.StatusOK {
		t.Fatalf("CreateGatewayUserKey: expected 200, got %d: %s", keyW.Code, keyW.Body.String())
	}

	backendW := httptest.NewRecorder()
	backendReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider":    "local",
		"slug":        "doctor-local",
		"key":         "anything",
		"set_default": true,
	})
	testHandler.CreateGatewayBackend(backendW, backendReq)
	if backendW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", backendW.Code, backendW.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_incident (
			workspace_id, severity, category, summary, status, remediation_notes
		)
		VALUES (
			$1, 'medium', 'gateway_provider_risk_block',
			'Gateway blocked provider local: provider_risk_expired',
			'open', 'Review provider contract.'
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/doctor", nil)
	req.Host = "api.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")
	testHandler.GatewayDoctor(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GatewayDoctor: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status string                 `json:"status"`
		Checks []doctorCheckAssertion `json:"checks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("GatewayDoctor: failed to decode response: %v", err)
	}
	if resp.Status != "healthy_with_warnings" {
		t.Fatalf("GatewayDoctor: status = %q, want healthy_with_warnings; checks = %#v", resp.Status, resp.Checks)
	}
	assertDoctorCheck(t, resp.Checks, "gateway_key", "pass")
	assertDoctorCheck(t, resp.Checks, "default_backend", "pass")
	assertDoctorCheck(t, resp.Checks, "open_incidents", "warning")
}

func TestGatewayHealthReportHandlerSummarizesBackendsCredentialsAndGovernance(t *testing.T) {
	setGatewaySecret(t)

	if _, err := testPool.Exec(context.Background(), `
		UPDATE gateway_backend
		SET enabled = false
		WHERE workspace_id = $1
	`, testWorkspaceID); err != nil {
		t.Fatalf("disable prior test backends: %v", err)
	}

	upstream := newFakeOpenAIUpstream(t, fakeOpenAIOptions{Model: "gpt-health"})
	defer upstream.Close()

	backendW := httptest.NewRecorder()
	backendReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider":    "openai",
		"slug":        "health-openai",
		"base_url":    upstream.URL + "/v1",
		"key":         "sk-health-primary-1234567890abcdef",
		"set_default": true,
	})
	testHandler.CreateGatewayBackend(backendW, backendReq)
	if backendW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", backendW.Code, backendW.Body.String())
	}
	var backend struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(backendW.Body).Decode(&backend); err != nil {
		t.Fatalf("decode backend: %v", err)
	}

	credentialW := httptest.NewRecorder()
	credentialReq := newRequest("POST", "/api/gateway/backends/"+backend.ID+"/credentials", map[string]any{
		"label":    "probe key",
		"key":      "sk-health-pool-1234567890abcdef",
		"priority": 1,
		"enabled":  true,
	})
	credentialReq = withURLParam(credentialReq, "id", backend.ID)
	testHandler.CreateGatewayBackendCredential(credentialW, credentialReq)
	if credentialW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackendCredential: expected 201, got %d: %s", credentialW.Code, credentialW.Body.String())
	}

	disabledCredentialW := httptest.NewRecorder()
	disabledCredentialReq := newRequest("POST", "/api/gateway/backends/"+backend.ID+"/credentials", map[string]any{
		"label":    "disabled key",
		"key":      "sk-health-disabled-1234567890abcdef",
		"priority": 2,
		"enabled":  false,
	})
	disabledCredentialReq = withURLParam(disabledCredentialReq, "id", backend.ID)
	testHandler.CreateGatewayBackendCredential(disabledCredentialW, disabledCredentialReq)
	if disabledCredentialW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackendCredential disabled: expected 201, got %d: %s", disabledCredentialW.Code, disabledCredentialW.Body.String())
	}
	var disabledCredential struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(disabledCredentialW.Body).Decode(&disabledCredential); err != nil {
		t.Fatalf("decode disabled credential: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE gateway_backend_credential
		SET rate_limited_until = $1, last_error_at = now(), last_error = 'rate limit'
		WHERE id = $2
	`, time.Now().UTC().Add(time.Hour), disabledCredential.ID); err != nil {
		t.Fatalf("mark credential rate limited: %v", err)
	}

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_incident (
			workspace_id, severity, category, summary, status, remediation_notes
		)
		VALUES (
			$1, 'medium', 'gateway_provider_risk_block',
			'Gateway provider risk needs review',
			'investigating', 'Review provider contract.'
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/health-report", nil)
	req.Host = "api.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")
	testHandler.GatewayHealthReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GatewayHealthReport: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status   string `json:"status"`
		Backends []struct {
			Slug              string `json:"slug"`
			Enabled           bool   `json:"enabled"`
			IsDefault         bool   `json:"is_default"`
			ProbeStatus       string `json:"probe_status"`
			ProbeLatencyMS    int64  `json:"probe_latency_ms"`
			ModelCount        int    `json:"model_count"`
			CredentialSummary struct {
				Total       int `json:"total"`
				Enabled     int `json:"enabled"`
				Disabled    int `json:"disabled"`
				RateLimited int `json:"rate_limited"`
				LastErrors  int `json:"last_errors"`
			} `json:"credential_summary"`
		} `json:"backends"`
		Governance struct {
			CapturePolicy        string `json:"capture_policy"`
			OpenIncidentCount    int    `json:"open_incident_count"`
			ProviderRiskWarnings int    `json:"provider_risk_warning_count"`
		} `json:"governance"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode health report: %v", err)
	}
	if resp.Status != "healthy_with_warnings" {
		t.Fatalf("status = %q, want healthy_with_warnings", resp.Status)
	}
	var got *struct {
		Slug              string `json:"slug"`
		Enabled           bool   `json:"enabled"`
		IsDefault         bool   `json:"is_default"`
		ProbeStatus       string `json:"probe_status"`
		ProbeLatencyMS    int64  `json:"probe_latency_ms"`
		ModelCount        int    `json:"model_count"`
		CredentialSummary struct {
			Total       int `json:"total"`
			Enabled     int `json:"enabled"`
			Disabled    int `json:"disabled"`
			RateLimited int `json:"rate_limited"`
			LastErrors  int `json:"last_errors"`
		} `json:"credential_summary"`
	}
	for i := range resp.Backends {
		if resp.Backends[i].Slug == "health-openai" {
			got = &resp.Backends[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("health-openai backend not found in %#v", resp.Backends)
	}
	if !got.Enabled || !got.IsDefault {
		t.Fatalf("backend summary = %#v, want enabled default health-openai", got)
	}
	if got.ProbeStatus != "pass" || got.ModelCount != 1 || got.ProbeLatencyMS <= 0 {
		t.Fatalf("probe summary = %#v, want passing model probe", got)
	}
	if got.CredentialSummary.Total != 2 || got.CredentialSummary.Enabled != 1 || got.CredentialSummary.Disabled != 1 || got.CredentialSummary.RateLimited != 1 || got.CredentialSummary.LastErrors != 1 {
		t.Fatalf("credential summary = %#v, want total/enabled/disabled/rate-limited/error counts", got.CredentialSummary)
	}
	if resp.Governance.CapturePolicy != "full_content" || resp.Governance.OpenIncidentCount < 1 {
		t.Fatalf("governance = %#v, want full_content and at least one open incident", resp.Governance)
	}
}

func TestGatewayCreateBackendRedactsCredential(t *testing.T) {
	setGatewaySecret(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "openrouter",
		"slug":     "credential-openrouter",
		"key":      "sk-or-1234567890abcdef",
	})

	testHandler.CreateGatewayBackend(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("CreateGatewayBackend: failed to decode response: %v", err)
	}
	if _, ok := resp["encrypted_credential"]; ok {
		t.Fatal("CreateGatewayBackend: response must not contain encrypted_credential")
	}
	if _, ok := resp["key"]; ok {
		t.Fatal("CreateGatewayBackend: response must not contain key")
	}
	if got := resp["credential_hint"]; got != "sk-or-12...cdef" {
		t.Fatalf("CreateGatewayBackend: credential_hint = %v, want %q", got, "sk-or-12...cdef")
	}
}

func TestGatewayCreateSubscriptionBackendNormalizesRuntimeFields(t *testing.T) {
	setGatewaySecret(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider":              "codex-subscription",
		"slug":                  "codex-subscription",
		"display_name":          "Codex Subscription",
		"backend_type":          "subscription_runtime",
		"base_url":              "daemon://codex",
		"key":                   `{"token":"test-codex-token"}`,
		"transport":             "daemon_dispatch",
		"credential_type":       "subscription_bundle",
		"subscription_provider": "codex",
		"dispatch_scope":        "workspace_authenticated_daemons",
	})

	testHandler.CreateGatewayBackend(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		BackendType          string `json:"backend_type"`
		BaseURL              string `json:"base_url"`
		Transport            string `json:"transport"`
		SubscriptionProvider string `json:"subscription_provider"`
		DispatchScope        string `json:"dispatch_scope"`
		ValidationStatus     string `json:"validation_status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("CreateGatewayBackend: failed to decode response: %v", err)
	}
	if resp.BackendType != management.BackendTypeSubscriptionRuntime {
		t.Fatalf("BackendType = %q, want %q", resp.BackendType, management.BackendTypeSubscriptionRuntime)
	}
	if resp.BaseURL != "daemon://codex" {
		t.Fatalf("BaseURL = %q, want daemon://codex", resp.BaseURL)
	}
	if resp.Transport != "daemon_dispatch" {
		t.Fatalf("Transport = %q, want daemon_dispatch", resp.Transport)
	}
	if resp.SubscriptionProvider != "codex" {
		t.Fatalf("SubscriptionProvider = %q, want codex", resp.SubscriptionProvider)
	}
	if resp.DispatchScope != "workspace_authenticated_daemons" {
		t.Fatalf("DispatchScope = %q, want workspace_authenticated_daemons", resp.DispatchScope)
	}
	if resp.ValidationStatus != "pending_runtime_validation" {
		t.Fatalf("ValidationStatus = %q, want pending_runtime_validation", resp.ValidationStatus)
	}
}

func TestDaemonRegisterSchedulesPendingSubscriptionValidation(t *testing.T) {
	setGatewaySecret(t)

	slug := "codex-subscription-register"
	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider":              "codex-subscription",
		"slug":                  slug,
		"display_name":          "Codex Subscription Register",
		"backend_type":          "subscription_runtime",
		"base_url":              "daemon://codex",
		"key":                   `{"token":"test-codex-token"}`,
		"transport":             "daemon_dispatch",
		"credential_type":       "subscription_bundle",
		"subscription_provider": "codex",
		"dispatch_scope":        "workspace_authenticated_daemons",
	})
	testHandler.CreateGatewayBackend(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var before int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM gateway_subscription_runtime_validation v
		JOIN gateway_backend b ON b.id = v.backend_id
		WHERE b.workspace_id = $1 AND b.slug = $2
	`, testWorkspaceID, slug).Scan(&before); err != nil {
		t.Fatalf("count validations before register: %v", err)
	}
	if before != 0 {
		t.Fatalf("validations before register = %d, want 0", before)
	}

	registerW := httptest.NewRecorder()
	registerReq := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    "subscription-register-daemon",
		"device_name":  "Subscription Register Test",
		"runtimes": []map[string]any{
			{
				"name":    "Local Codex",
				"type":    "codex",
				"version": "test",
				"status":  "online",
			},
		},
	})
	testHandler.DaemonRegister(registerW, registerReq)
	if registerW.Code != http.StatusOK {
		t.Fatalf("DaemonRegister: expected 200, got %d: %s", registerW.Code, registerW.Body.String())
	}

	var after int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM gateway_subscription_runtime_validation v
		JOIN gateway_backend b ON b.id = v.backend_id
		JOIN agent_runtime ar ON ar.id = v.runtime_id
		WHERE b.workspace_id = $1
		  AND b.slug = $2
		  AND v.status = 'pending'
		  AND v.provider = 'codex'
		  AND ar.provider = 'codex'
		  AND ar.status = 'online'
	`, testWorkspaceID, slug).Scan(&after); err != nil {
		t.Fatalf("count validations after register: %v", err)
	}
	if after != 1 {
		t.Fatalf("validations after register = %d, want 1", after)
	}
}

func TestGatewayBackendCredentialHandlersCreateListAndUpdate(t *testing.T) {
	setGatewaySecret(t)

	createBackendW := httptest.NewRecorder()
	createBackendReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "openrouter",
		"slug":     "pool-openrouter",
		"key":      "sk-or-primary-1234567890abcdef",
	})
	testHandler.CreateGatewayBackend(createBackendW, createBackendReq)
	if createBackendW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", createBackendW.Code, createBackendW.Body.String())
	}

	var backend struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createBackendW.Body).Decode(&backend); err != nil {
		t.Fatalf("CreateGatewayBackend: decode response: %v", err)
	}

	createCredentialW := httptest.NewRecorder()
	createCredentialReq := newRequest("POST", "/api/gateway/backends/"+backend.ID+"/credentials", map[string]any{
		"label":    "production pool key",
		"key":      "sk-or-pooled-1234567890abcdef",
		"priority": 5,
		"enabled":  true,
	})
	createCredentialReq = withURLParam(createCredentialReq, "id", backend.ID)
	testHandler.CreateGatewayBackendCredential(createCredentialW, createCredentialReq)
	if createCredentialW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackendCredential: expected 201, got %d: %s", createCredentialW.Code, createCredentialW.Body.String())
	}

	var created struct {
		ID             string `json:"id"`
		BackendID      string `json:"backend_id"`
		Label          string `json:"label"`
		CredentialHint string `json:"credential_hint"`
		Enabled        bool   `json:"enabled"`
		Priority       int32  `json:"priority"`
	}
	if err := json.NewDecoder(createCredentialW.Body).Decode(&created); err != nil {
		t.Fatalf("CreateGatewayBackendCredential: decode response: %v", err)
	}
	if created.ID == "" || created.BackendID != backend.ID {
		t.Fatalf("CreateGatewayBackendCredential: response = %#v, want backend %s", created, backend.ID)
	}
	if created.CredentialHint != "sk-or-po...cdef" {
		t.Fatalf("CreateGatewayBackendCredential: credential_hint = %q, want redacted hint", created.CredentialHint)
	}

	listW := httptest.NewRecorder()
	listReq := newRequest("GET", "/api/gateway/backends/"+backend.ID+"/credentials", nil)
	listReq = withURLParam(listReq, "id", backend.ID)
	testHandler.ListGatewayBackendCredentials(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("ListGatewayBackendCredentials: expected 200, got %d: %s", listW.Code, listW.Body.String())
	}

	var listed []map[string]any
	if err := json.NewDecoder(listW.Body).Decode(&listed); err != nil {
		t.Fatalf("ListGatewayBackendCredentials: decode response: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListGatewayBackendCredentials: len = %d, want 1", len(listed))
	}
	if _, ok := listed[0]["encrypted_credential"]; ok {
		t.Fatal("ListGatewayBackendCredentials: response must not expose encrypted_credential")
	}
	if _, ok := listed[0]["key"]; ok {
		t.Fatal("ListGatewayBackendCredentials: response must not expose key")
	}

	disabled := false
	updateW := httptest.NewRecorder()
	updateReq := newRequest("PATCH", "/api/gateway/backends/"+backend.ID+"/credentials/"+created.ID, map[string]any{
		"enabled":  disabled,
		"priority": 20,
	})
	updateReq = withGatewayCredentialURLParams(updateReq, backend.ID, created.ID)
	testHandler.UpdateGatewayBackendCredential(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("UpdateGatewayBackendCredential: expected 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var updated struct {
		ID       string `json:"id"`
		Enabled  bool   `json:"enabled"`
		Priority int32  `json:"priority"`
	}
	if err := json.NewDecoder(updateW.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateGatewayBackendCredential: decode response: %v", err)
	}
	if updated.ID != created.ID || updated.Enabled || updated.Priority != 20 {
		t.Fatalf("UpdateGatewayBackendCredential: response = %#v, want disabled priority 20", updated)
	}
}

func withGatewayCredentialURLParams(req *http.Request, backendID, credentialID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", backendID)
	rctx.URLParams.Add("credentialID", credentialID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestGatewayDeleteBackendReturnsDeletedResponse(t *testing.T) {
	setGatewaySecret(t)

	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "groq",
		"key":      "gsk_1234567890abcdef",
	})

	testHandler.CreateGatewayBackend(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createW.Body).Decode(&created); err != nil {
		t.Fatalf("CreateGatewayBackend: failed to decode response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateGatewayBackend: expected non-empty backend id")
	}

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/gateway/backends/"+created.ID, nil)
	req = withURLParam(req, "id", created.ID)

	testHandler.DeleteGatewayBackend(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteGatewayBackend: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("DeleteGatewayBackend: failed to decode response: %v", err)
	}
	if !resp["deleted"] {
		t.Fatalf("DeleteGatewayBackend: deleted = %v, want true", resp["deleted"])
	}
}

func TestGatewayAuditHandlerListsBackendChanges(t *testing.T) {
	setGatewaySecret(t)

	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "local",
		"slug":     "audit-local",
		"key":      "anything",
	})

	testHandler.CreateGatewayBackend(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createW.Body).Decode(&created); err != nil {
		t.Fatalf("CreateGatewayBackend: failed to decode response: %v", err)
	}

	displayName := "Local Router"
	updateW := httptest.NewRecorder()
	updateReq := newRequest("PATCH", "/api/gateway/backends/"+created.ID, map[string]any{
		"display_name": displayName,
		"enabled":      false,
	})
	updateReq = withURLParam(updateReq, "id", created.ID)
	testHandler.UpdateGatewayBackend(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("UpdateGatewayBackend: expected 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/audit?limit=10", nil)

	testHandler.ListGatewayAudit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayAudit: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []struct {
		Action     string         `json:"action"`
		TargetID   string         `json:"target_id"`
		ActorName  string         `json:"actor_name"`
		AfterState map[string]any `json:"after_state"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayAudit: failed to decode response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListGatewayAudit: expected at least one audit row")
	}
	if resp[0].Action != "gateway.backend.update" {
		t.Fatalf("ListGatewayAudit: first action = %q, want gateway.backend.update", resp[0].Action)
	}
	if resp[0].TargetID != created.ID {
		t.Fatalf("ListGatewayAudit: target_id = %q, want %q", resp[0].TargetID, created.ID)
	}
	if resp[0].ActorName != handlerTestName {
		t.Fatalf("ListGatewayAudit: actor_name = %q, want %q", resp[0].ActorName, handlerTestName)
	}
	if resp[0].AfterState["display_name"] != displayName {
		t.Fatalf("ListGatewayAudit: after_state.display_name = %v, want %q", resp[0].AfterState["display_name"], displayName)
	}
}

func TestGatewayProviderRiskHandlersUpsertAndList(t *testing.T) {
	setGatewaySecret(t)

	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "openrouter",
		"slug":     "risk-openrouter",
		"key":      "sk-or-1234567890abcdef",
	})

	testHandler.CreateGatewayBackend(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var backend struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createW.Body).Decode(&backend); err != nil {
		t.Fatalf("CreateGatewayBackend: failed to decode response: %v", err)
	}

	upsertW := httptest.NewRecorder()
	upsertReq := newRequest("POST", "/api/gateway/governance/provider-risks", map[string]any{
		"provider_name":          "openrouter",
		"backend_id":             backend.ID,
		"approved_use_cases":     []string{"internal support", "code review"},
		"data_categories":        []string{"source_code", "customer_data"},
		"contract_status":        "approved",
		"security_review_status": "approved",
		"risk_score":             64,
		"review_cadence_days":    180,
		"next_review_at":         "2026-10-01T00:00:00Z",
	})

	testHandler.UpsertGatewayProviderRisk(upsertW, upsertReq)
	if upsertW.Code != http.StatusOK {
		t.Fatalf("UpsertGatewayProviderRisk: expected 200, got %d: %s", upsertW.Code, upsertW.Body.String())
	}

	listW := httptest.NewRecorder()
	listReq := newRequest("GET", "/api/gateway/governance/provider-risks", nil)
	testHandler.ListGatewayProviderRisks(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("ListGatewayProviderRisks: expected 200, got %d: %s", listW.Code, listW.Body.String())
	}

	var resp []struct {
		BackendID            string   `json:"backend_id"`
		ProviderName         string   `json:"provider_name"`
		ApprovedUseCases     []string `json:"approved_use_cases"`
		DataCategories       []string `json:"data_categories"`
		ContractStatus       string   `json:"contract_status"`
		SecurityReviewStatus string   `json:"security_review_status"`
		RiskScore            int      `json:"risk_score"`
		NextReviewAt         *string  `json:"next_review_at"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayProviderRisks: failed to decode response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListGatewayProviderRisks: expected at least one risk row")
	}
	var risk *struct {
		BackendID            string   `json:"backend_id"`
		ProviderName         string   `json:"provider_name"`
		ApprovedUseCases     []string `json:"approved_use_cases"`
		DataCategories       []string `json:"data_categories"`
		ContractStatus       string   `json:"contract_status"`
		SecurityReviewStatus string   `json:"security_review_status"`
		RiskScore            int      `json:"risk_score"`
		NextReviewAt         *string  `json:"next_review_at"`
	}
	for i := range resp {
		if resp[i].BackendID == backend.ID {
			risk = &resp[i]
			break
		}
	}
	if risk == nil {
		t.Fatalf("ListGatewayProviderRisks: missing backend_id %q in %#v", backend.ID, resp)
	}
	if risk.ProviderName != "openrouter" {
		t.Fatalf("ListGatewayProviderRisks: provider_name = %q, want openrouter", risk.ProviderName)
	}
	if risk.RiskScore != 64 {
		t.Fatalf("ListGatewayProviderRisks: risk_score = %d, want 64", risk.RiskScore)
	}
	if risk.SecurityReviewStatus != "approved" || risk.ContractStatus != "approved" {
		t.Fatalf("ListGatewayProviderRisks: statuses = %s/%s, want approved/approved", risk.SecurityReviewStatus, risk.ContractStatus)
	}
	if len(risk.ApprovedUseCases) != 2 || risk.ApprovedUseCases[1] != "code review" {
		t.Fatalf("ListGatewayProviderRisks: approved_use_cases = %#v, want code review", risk.ApprovedUseCases)
	}
	if risk.NextReviewAt == nil || *risk.NextReviewAt == "" {
		t.Fatal("ListGatewayProviderRisks: expected next_review_at")
	}
}

func TestGatewayPolicyDecisionHandlerListsRecentDecisions(t *testing.T) {
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO gateway_policy_decision (
			workspace_id, subject_user_id, resource_type, resource_id, resource_label,
			decision, reason_code, matched_rules, evidence_references
		)
		VALUES ($1, $2, 'provider', 'backend-openrouter', 'openrouter',
			'block', 'provider_risk_rejected', '[{"id":"provider_risk_rejected"}]'::jsonb, '[]'::jsonb)
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("insert policy decision: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/governance/policy-decisions?limit=10", nil)
	testHandler.ListGatewayPolicyDecisions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayPolicyDecisions: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []struct {
		ResourceType  string `json:"resource_type"`
		ResourceLabel string `json:"resource_label"`
		Decision      string `json:"decision"`
		ReasonCode    string `json:"reason_code"`
		CreatedAt     string `json:"created_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayPolicyDecisions: decode response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListGatewayPolicyDecisions: expected at least one decision")
	}
	if resp[0].Decision != "block" || resp[0].ReasonCode != "provider_risk_rejected" {
		t.Fatalf("ListGatewayPolicyDecisions: first decision = %#v, want provider_risk_rejected block", resp[0])
	}
	if resp[0].ResourceType != "provider" || resp[0].ResourceLabel != "openrouter" {
		t.Fatalf("ListGatewayPolicyDecisions: resource = %s/%s, want provider/openrouter", resp[0].ResourceType, resp[0].ResourceLabel)
	}
	if resp[0].CreatedAt == "" {
		t.Fatal("ListGatewayPolicyDecisions: expected created_at")
	}
}

func TestGatewayPolicyDecisionHandlersApproveAndDenyRequestedDecision(t *testing.T) {
	var policyID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO gateway_policy (
			workspace_id, name, description, policy_type, enabled,
			version, rule_definition, enforcement_mode, created_by, updated_by
		)
		VALUES (
			$1, 'Approval workflow test policy', 'Requires approval for a model',
			'model', true, 1,
			'{"rules":[{"id":"approval-test","action":"require_approval"}]}'::jsonb,
			'enforce', $2, $2
		)
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&policyID); err != nil {
		t.Fatalf("insert gateway policy: %v", err)
	}

	var approveDecisionID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO gateway_policy_decision (
			workspace_id, policy_id, policy_version, subject_user_id,
			resource_type, resource_id, resource_label, decision, reason_code,
			matched_rules, approval_status, evidence_references
		)
		VALUES (
			$1, $2, 1, $3,
			'model', 'gpt-approval', 'gpt-approval', 'require_approval', 'model_requires_approval',
			'[{"id":"approval-test"}]'::jsonb, 'requested', '[]'::jsonb
		)
		RETURNING id::text
	`, testWorkspaceID, policyID, testUserID).Scan(&approveDecisionID); err != nil {
		t.Fatalf("insert approval decision: %v", err)
	}

	approveW := httptest.NewRecorder()
	approveReq := newRequest("POST", "/api/gateway/governance/policy-decisions/"+approveDecisionID+"/approve", map[string]any{
		"reason":     "Approved for incident response",
		"expires_at": "2026-12-31T00:00:00Z",
	})
	approveReq = withURLParam(approveReq, "id", approveDecisionID)
	testHandler.ApproveGatewayPolicyDecision(approveW, approveReq)
	if approveW.Code != http.StatusOK {
		t.Fatalf("ApproveGatewayPolicyDecision: expected 200, got %d: %s", approveW.Code, approveW.Body.String())
	}

	var approved struct {
		Decision struct {
			ID             string `json:"id"`
			ApprovalStatus string `json:"approval_status"`
		} `json:"decision"`
		Exception *struct {
			PolicyID       string         `json:"policy_id"`
			ApproverUserID string         `json:"approver_user_id"`
			Status         string         `json:"status"`
			Scope          map[string]any `json:"scope"`
			ExpiresAt      *string        `json:"expires_at"`
		} `json:"exception"`
	}
	if err := json.NewDecoder(approveW.Body).Decode(&approved); err != nil {
		t.Fatalf("ApproveGatewayPolicyDecision: decode response: %v", err)
	}
	if approved.Decision.ID != approveDecisionID || approved.Decision.ApprovalStatus != "approved" {
		t.Fatalf("ApproveGatewayPolicyDecision: decision = %#v, want approved %s", approved.Decision, approveDecisionID)
	}
	if approved.Exception == nil {
		t.Fatal("ApproveGatewayPolicyDecision: expected approved exception")
	}
	if approved.Exception.PolicyID != policyID || approved.Exception.Status != "approved" || approved.Exception.ApproverUserID == "" {
		t.Fatalf("ApproveGatewayPolicyDecision: exception = %#v, want approved policy-linked exception", approved.Exception)
	}
	if approved.Exception.Scope["resource_type"] != "model" || approved.Exception.Scope["resource_id"] != "gpt-approval" {
		t.Fatalf("ApproveGatewayPolicyDecision: scope = %#v, want model/gpt-approval", approved.Exception.Scope)
	}
	if approved.Exception.ExpiresAt == nil {
		t.Fatal("ApproveGatewayPolicyDecision: expected expires_at on exception")
	}

	var denyDecisionID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO gateway_policy_decision (
			workspace_id, policy_id, policy_version, subject_user_id,
			resource_type, resource_id, resource_label, decision, reason_code,
			matched_rules, approval_status, evidence_references
		)
		VALUES (
			$1, $2, 1, $3,
			'tool', 'shell', 'shell', 'require_approval', 'tool_requires_approval',
			'[{"id":"approval-test"}]'::jsonb, 'requested', '[]'::jsonb
		)
		RETURNING id::text
	`, testWorkspaceID, policyID, testUserID).Scan(&denyDecisionID); err != nil {
		t.Fatalf("insert denial decision: %v", err)
	}

	denyW := httptest.NewRecorder()
	denyReq := newRequest("POST", "/api/gateway/governance/policy-decisions/"+denyDecisionID+"/deny", map[string]any{
		"reason": "Not approved for shell access",
	})
	denyReq = withURLParam(denyReq, "id", denyDecisionID)
	testHandler.DenyGatewayPolicyDecision(denyW, denyReq)
	if denyW.Code != http.StatusOK {
		t.Fatalf("DenyGatewayPolicyDecision: expected 200, got %d: %s", denyW.Code, denyW.Body.String())
	}

	var denied struct {
		Decision struct {
			ID             string `json:"id"`
			ApprovalStatus string `json:"approval_status"`
		} `json:"decision"`
	}
	if err := json.NewDecoder(denyW.Body).Decode(&denied); err != nil {
		t.Fatalf("DenyGatewayPolicyDecision: decode response: %v", err)
	}
	if denied.Decision.ID != denyDecisionID || denied.Decision.ApprovalStatus != "denied" {
		t.Fatalf("DenyGatewayPolicyDecision: decision = %#v, want denied %s", denied.Decision, denyDecisionID)
	}
}

func TestGatewayEvidenceHandlerListsRecentEvidence(t *testing.T) {
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_evidence (
			workspace_id, evidence_type, framework_refs, summary, payload, attachment_ref
		)
		VALUES ($1, 'gateway_policy_decision', '["internal_gateway_governance"]'::jsonb,
			'Gateway blocked provider openrouter: provider_risk_rejected',
			'{"reason_code":"provider_risk_rejected","resource_label":"openrouter"}'::jsonb, '')
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/governance/evidence?limit=10", nil)
	testHandler.ListGatewayEvidence(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayEvidence: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []struct {
		EvidenceType string         `json:"evidence_type"`
		Summary      string         `json:"summary"`
		Payload      map[string]any `json:"payload"`
		GeneratedAt  string         `json:"generated_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayEvidence: decode response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListGatewayEvidence: expected at least one evidence row")
	}
	if resp[0].EvidenceType != "gateway_policy_decision" {
		t.Fatalf("ListGatewayEvidence: evidence_type = %q, want gateway_policy_decision", resp[0].EvidenceType)
	}
	if resp[0].Payload["reason_code"] != "provider_risk_rejected" {
		t.Fatalf("ListGatewayEvidence: payload = %#v, want provider_risk_rejected", resp[0].Payload)
	}
	if resp[0].GeneratedAt == "" {
		t.Fatal("ListGatewayEvidence: expected generated_at")
	}
}

func TestGatewayControlMappingHandlerListsEvidenceCoverage(t *testing.T) {
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_control_mapping (
			workspace_id, framework, control_id, control_title,
			mapped_policy_ids, mapped_evidence_queries, status, owner_user_id
		)
		VALUES (
			$1, 'internal_gateway_governance', 'GW-TEST', 'Gateway policy blocks are evidenced',
			'[]'::jsonb, '[{"evidence_type":"gateway_policy_decision"}]'::jsonb, 'covered', $2
		)
		ON CONFLICT (workspace_id, framework, control_id)
		DO UPDATE SET
			control_title = EXCLUDED.control_title,
			mapped_evidence_queries = EXCLUDED.mapped_evidence_queries,
			status = EXCLUDED.status,
			owner_user_id = EXCLUDED.owner_user_id,
			updated_at = now()
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("upsert control mapping: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_evidence (
			workspace_id, evidence_type, framework_refs, summary, payload, attachment_ref
		)
		VALUES (
			$1, 'gateway_policy_decision', '["internal_gateway_governance"]'::jsonb,
			'Gateway blocked provider openrouter: provider_risk_rejected',
			'{"reason_code":"provider_risk_rejected"}'::jsonb, ''
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/governance/control-mappings", nil)
	testHandler.ListGatewayControlMappings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayControlMappings: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []struct {
		Framework               string `json:"framework"`
		ControlID               string `json:"control_id"`
		ControlTitle            string `json:"control_title"`
		Status                  string `json:"status"`
		EvidenceCount           int64  `json:"evidence_count"`
		LastEvidenceGeneratedAt string `json:"last_evidence_generated_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayControlMappings: decode response: %v", err)
	}
	var found *struct {
		Framework               string `json:"framework"`
		ControlID               string `json:"control_id"`
		ControlTitle            string `json:"control_title"`
		Status                  string `json:"status"`
		EvidenceCount           int64  `json:"evidence_count"`
		LastEvidenceGeneratedAt string `json:"last_evidence_generated_at"`
	}
	for i := range resp {
		if resp[i].ControlID == "GW-TEST" {
			found = &resp[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("ListGatewayControlMappings: expected GW-TEST in response, got %#v", resp)
	}
	if found.Framework != "internal_gateway_governance" || found.ControlTitle == "" {
		t.Fatalf("ListGatewayControlMappings: unexpected control row %#v", *found)
	}
	if found.Status != "covered" {
		t.Fatalf("ListGatewayControlMappings: status = %q, want covered", found.Status)
	}
	if found.EvidenceCount == 0 {
		t.Fatal("ListGatewayControlMappings: expected evidence_count > 0")
	}
	if found.LastEvidenceGeneratedAt == "" {
		t.Fatal("ListGatewayControlMappings: expected last_evidence_generated_at")
	}
}

func TestGatewayGovernanceInsightsSummarizesRiskBehaviorAndCompliance(t *testing.T) {
	seedGatewayObservabilityTelemetry(t, "insights")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO gateway_policy_decision (
			workspace_id, subject_user_id, resource_type, resource_id, resource_label,
			decision, reason_code, matched_rules, approval_status, evidence_references
		)
		VALUES
			($1, $2, 'provider', 'insights-provider', 'Insights Provider',
			 'block', 'insights_provider_blocked', '[{"id":"insights-provider-blocked"}]'::jsonb, '', '[]'::jsonb),
			($1, $2, 'model', 'gpt-insights', 'gpt-insights',
			 'require_approval', 'insights_model_requires_approval', '[{"id":"insights-approval"}]'::jsonb, 'requested', '[]'::jsonb)
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("insert policy decisions: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_incident (
			workspace_id, severity, category, summary, status, remediation_notes
		)
		VALUES (
			$1, 'high', 'gateway_insights_test',
			'Insights open incident needs review',
			'open', 'Review this insights test incident.'
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert incident: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_evidence (
			workspace_id, evidence_type, framework_refs, summary, payload, attachment_ref
		)
		VALUES (
			$1, 'gateway_policy_decision', '["internal_gateway_governance"]'::jsonb,
			'Insights evidence record',
			'{"reason_code":"insights_provider_blocked"}'::jsonb, ''
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_control_mapping (
			workspace_id, framework, control_id, control_title,
			mapped_policy_ids, mapped_evidence_queries, status, owner_user_id
		)
		VALUES (
			$1, 'internal_gateway_governance', 'GW-INSIGHTS-GAP', 'Insights control gap',
			'[]'::jsonb, '[]'::jsonb, 'gap', $2
		)
		ON CONFLICT (workspace_id, framework, control_id)
		DO UPDATE SET
			control_title = EXCLUDED.control_title,
			status = EXCLUDED.status,
			owner_user_id = EXCLUDED.owner_user_id,
			updated_at = now()
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("upsert control mapping: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_policy_exception (
			workspace_id, requester_user_id, approver_user_id, reason, scope, status, expires_at, evidence_references
		)
		VALUES (
			$1, $2, $2, 'Insights approved exception',
			'{"resource_type":"model","resource_id":"gpt-insights","resource_label":"gpt-insights"}'::jsonb,
			'approved', now() + interval '7 days', '[]'::jsonb
		)
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("insert policy exception: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_third_party_risk (
			workspace_id, provider_name, owner_user_id, approved_use_cases, data_categories,
			contract_status, security_review_status, risk_score, review_cadence_days, next_review_at
		)
		VALUES (
			$1, 'insights-provider', $2, '[]'::jsonb, '[]'::jsonb,
			'rejected', 'approved', 88, 90, now() + interval '30 days'
		)
		ON CONFLICT (workspace_id, provider_name)
		DO UPDATE SET
			contract_status = EXCLUDED.contract_status,
			security_review_status = EXCLUDED.security_review_status,
			risk_score = EXCLUDED.risk_score,
			updated_at = now()
	`, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("upsert provider risk: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/governance/insights", nil)
	testHandler.GatewayGovernanceInsights(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GatewayGovernanceInsights: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		GeneratedAt  string `json:"generated_at"`
		RiskOverview struct {
			OpenIncidentCount          int `json:"open_incident_count"`
			HighSeverityOpenCount      int `json:"high_severity_open_incident_count"`
			PendingApprovalCount       int `json:"pending_approval_count"`
			BlockedDecisionCount       int `json:"blocked_decision_count"`
			HighRiskProviderCount      int `json:"high_risk_provider_count"`
			ProviderReviewWarningCount int `json:"provider_review_warning_count"`
			ControlGapCount            int `json:"control_gap_count"`
			ActivePolicyExceptionCount int `json:"active_policy_exception_count"`
		} `json:"risk_overview"`
		BehaviorTrends struct {
			TopModels []struct {
				Model       string `json:"model"`
				CallCount   int64  `json:"call_count"`
				TotalTokens int64  `json:"total_tokens"`
			} `json:"top_models"`
			PolicyReasonCounts []struct {
				ReasonCode string `json:"reason_code"`
				Count      int    `json:"count"`
			} `json:"policy_reason_counts"`
			BlockedResources []struct {
				ResourceLabel string `json:"resource_label"`
				Count         int    `json:"count"`
			} `json:"blocked_resources"`
		} `json:"behavior_trends"`
		ComplianceCoverage struct {
			ControlCount            int    `json:"control_count"`
			GapControlCount         int    `json:"gap_control_count"`
			EvidenceCount           int    `json:"evidence_count"`
			LastEvidenceGeneratedAt string `json:"last_evidence_generated_at"`
		} `json:"compliance_coverage"`
		ActionQueue []struct {
			Kind     string `json:"kind"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Detail   string `json:"detail"`
		} `json:"action_queue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("GatewayGovernanceInsights: decode response: %v", err)
	}
	if resp.GeneratedAt == "" {
		t.Fatal("GatewayGovernanceInsights: expected generated_at")
	}
	if resp.RiskOverview.OpenIncidentCount == 0 || resp.RiskOverview.HighSeverityOpenCount == 0 {
		t.Fatalf("GatewayGovernanceInsights: incident counts = %#v, want open high severity incident", resp.RiskOverview)
	}
	if resp.RiskOverview.PendingApprovalCount == 0 || resp.RiskOverview.BlockedDecisionCount == 0 {
		t.Fatalf("GatewayGovernanceInsights: decision counts = %#v, want pending approval and blocked decision", resp.RiskOverview)
	}
	if resp.RiskOverview.HighRiskProviderCount == 0 || resp.RiskOverview.ProviderReviewWarningCount == 0 {
		t.Fatalf("GatewayGovernanceInsights: provider counts = %#v, want high risk provider warning", resp.RiskOverview)
	}
	if resp.RiskOverview.ControlGapCount == 0 || resp.RiskOverview.ActivePolicyExceptionCount == 0 {
		t.Fatalf("GatewayGovernanceInsights: control/exception counts = %#v, want gap and active exception", resp.RiskOverview)
	}
	if len(resp.BehaviorTrends.TopModels) == 0 {
		t.Fatal("GatewayGovernanceInsights: expected top model rows")
	}
	foundReason := false
	for _, reason := range resp.BehaviorTrends.PolicyReasonCounts {
		if reason.ReasonCode == "insights_provider_blocked" && reason.Count > 0 {
			foundReason = true
			break
		}
	}
	if !foundReason {
		t.Fatalf("GatewayGovernanceInsights: missing insights_provider_blocked in %#v", resp.BehaviorTrends.PolicyReasonCounts)
	}
	foundBlockedResource := false
	for _, resource := range resp.BehaviorTrends.BlockedResources {
		if resource.ResourceLabel == "Insights Provider" && resource.Count > 0 {
			foundBlockedResource = true
			break
		}
	}
	if !foundBlockedResource {
		t.Fatalf("GatewayGovernanceInsights: missing blocked Insights Provider in %#v", resp.BehaviorTrends.BlockedResources)
	}
	if resp.ComplianceCoverage.ControlCount == 0 || resp.ComplianceCoverage.GapControlCount == 0 || resp.ComplianceCoverage.EvidenceCount == 0 || resp.ComplianceCoverage.LastEvidenceGeneratedAt == "" {
		t.Fatalf("GatewayGovernanceInsights: compliance coverage = %#v, want controls, gap, and evidence", resp.ComplianceCoverage)
	}
	foundAction := false
	for _, action := range resp.ActionQueue {
		if strings.Contains(action.Detail, "Insights open incident") || strings.Contains(action.Title, "Insights open incident") {
			foundAction = true
			break
		}
	}
	if !foundAction {
		t.Fatalf("GatewayGovernanceInsights: missing incident action in %#v", resp.ActionQueue)
	}
}

func TestGatewayIncidentHandlerListsOpenIncidents(t *testing.T) {
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO ai_incident (
			workspace_id, severity, category, summary, status, remediation_notes
		)
		VALUES (
			$1, 'high', 'gateway_provider_risk_block',
			'Gateway blocked provider openrouter: provider_risk_rejected',
			'open', 'Review provider risk register before enabling backend.'
		)
	`, testWorkspaceID); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/gateway/governance/incidents?limit=10", nil)
	testHandler.ListGatewayIncidents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayIncidents: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []struct {
		Severity         string `json:"severity"`
		Category         string `json:"category"`
		Summary          string `json:"summary"`
		Status           string `json:"status"`
		RemediationNotes string `json:"remediation_notes"`
		OpenedAt         string `json:"opened_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("ListGatewayIncidents: decode response: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("ListGatewayIncidents: expected at least one incident")
	}
	if resp[0].Category != "gateway_provider_risk_block" || resp[0].Severity != "high" {
		t.Fatalf("ListGatewayIncidents: first incident = %#v, want high gateway_provider_risk_block", resp[0])
	}
	if resp[0].Status != "open" {
		t.Fatalf("ListGatewayIncidents: status = %q, want open", resp[0].Status)
	}
	if resp[0].OpenedAt == "" {
		t.Fatal("ListGatewayIncidents: expected opened_at")
	}
}

func TestGatewayIncidentHandlerUpdatesStatus(t *testing.T) {
	var incidentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO ai_incident (
			workspace_id, severity, category, summary, status, remediation_notes
		)
		VALUES (
			$1, 'high', 'gateway_provider_risk_block',
			'Gateway blocked provider openrouter: provider_risk_rejected',
			'open', ''
		)
		RETURNING id::text
	`, testWorkspaceID).Scan(&incidentID); err != nil {
		t.Fatalf("insert incident: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/gateway/governance/incidents/"+incidentID, map[string]any{
		"status":            "remediated",
		"remediation_notes": "Provider review completed and backend remains disabled.",
	})
	req = withURLParam(req, "id", incidentID)
	testHandler.UpdateGatewayIncident(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateGatewayIncident: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status           string `json:"status"`
		RemediationNotes string `json:"remediation_notes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("UpdateGatewayIncident: decode response: %v", err)
	}
	if resp.Status != "remediated" {
		t.Fatalf("UpdateGatewayIncident: status = %q, want remediated", resp.Status)
	}
	if resp.RemediationNotes == "" {
		t.Fatal("UpdateGatewayIncident: expected remediation notes")
	}
}

func TestGatewayPolicyExceptionHandlersCreateListAndApprove(t *testing.T) {
	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/governance/exceptions", map[string]any{
		"reason":         "Temporary exception for incident response",
		"resource_type":  "provider",
		"resource_id":    "backend-openrouter",
		"resource_label": "openrouter",
	})
	testHandler.CreateGatewayPolicyException(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayPolicyException: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var created struct {
		ID     string         `json:"id"`
		Status string         `json:"status"`
		Scope  map[string]any `json:"scope"`
	}
	if err := json.NewDecoder(createW.Body).Decode(&created); err != nil {
		t.Fatalf("CreateGatewayPolicyException: decode response: %v", err)
	}
	if created.ID == "" || created.Status != "requested" {
		t.Fatalf("CreateGatewayPolicyException: response = %#v, want requested with id", created)
	}
	if created.Scope["resource_id"] != "backend-openrouter" {
		t.Fatalf("CreateGatewayPolicyException: scope = %#v, want resource_id", created.Scope)
	}

	updateW := httptest.NewRecorder()
	updateReq := newRequest("PATCH", "/api/gateway/governance/exceptions/"+created.ID, map[string]any{
		"status":     "approved",
		"expires_at": "2026-12-31T00:00:00Z",
	})
	updateReq = withURLParam(updateReq, "id", created.ID)
	testHandler.UpdateGatewayPolicyException(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("UpdateGatewayPolicyException: expected 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var updated struct {
		Status     string  `json:"status"`
		ApproverID string  `json:"approver_user_id"`
		ExpiresAt  *string `json:"expires_at"`
	}
	if err := json.NewDecoder(updateW.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateGatewayPolicyException: decode response: %v", err)
	}
	if updated.Status != "approved" || updated.ApproverID == "" || updated.ExpiresAt == nil {
		t.Fatalf("UpdateGatewayPolicyException: response = %#v, want approved with approver and expiry", updated)
	}

	listW := httptest.NewRecorder()
	listReq := newRequest("GET", "/api/gateway/governance/exceptions?limit=10", nil)
	testHandler.ListGatewayPolicyExceptions(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("ListGatewayPolicyExceptions: expected 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var listed []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&listed); err != nil {
		t.Fatalf("ListGatewayPolicyExceptions: decode response: %v", err)
	}
	if len(listed) == 0 || listed[0].ID == "" {
		t.Fatalf("ListGatewayPolicyExceptions: expected exception rows, got %#v", listed)
	}
}

func TestGatewayPolicyHandlersCreateListAndUpdate(t *testing.T) {
	createW := httptest.NewRecorder()
	createReq := newRequest("POST", "/api/gateway/governance/policies", map[string]any{
		"name":             "Block test model",
		"description":      "Stops a model in enforcement mode",
		"policy_type":      "model",
		"enabled":          true,
		"enforcement_mode": "enforce",
		"rule_definition": map[string]any{
			"rules": []map[string]any{{
				"id":          "block-gpt-test",
				"action":      "block",
				"reason_code": "model_blocked",
				"message":     "This model is blocked",
				"match": map[string]any{
					"models": []string{"gpt-test"},
				},
			}},
		},
	})
	testHandler.CreateGatewayGovernancePolicy(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayGovernancePolicy: expected 201, got %d: %s", createW.Code, createW.Body.String())
	}

	var created struct {
		ID              string         `json:"id"`
		Name            string         `json:"name"`
		PolicyType      string         `json:"policy_type"`
		Enabled         bool           `json:"enabled"`
		Version         int            `json:"version"`
		EnforcementMode string         `json:"enforcement_mode"`
		RuleDefinition  map[string]any `json:"rule_definition"`
	}
	if err := json.NewDecoder(createW.Body).Decode(&created); err != nil {
		t.Fatalf("CreateGatewayGovernancePolicy: decode response: %v", err)
	}
	if created.ID == "" || created.Name != "Block test model" || created.PolicyType != "model" || created.Version != 1 {
		t.Fatalf("CreateGatewayGovernancePolicy: response = %#v", created)
	}
	if created.RuleDefinition["rules"] == nil {
		t.Fatalf("CreateGatewayGovernancePolicy: missing rule_definition rules: %#v", created.RuleDefinition)
	}

	listW := httptest.NewRecorder()
	listReq := newRequest("GET", "/api/gateway/governance/policies", nil)
	testHandler.ListGatewayGovernancePolicies(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("ListGatewayGovernancePolicies: expected 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var listed []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&listed); err != nil {
		t.Fatalf("ListGatewayGovernancePolicies: decode response: %v", err)
	}
	found := false
	for _, item := range listed {
		if item.ID == created.ID && item.Name == "Block test model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListGatewayGovernancePolicies: did not find created policy in %#v", listed)
	}

	updateW := httptest.NewRecorder()
	updateReq := newRequest("PATCH", "/api/gateway/governance/policies/"+created.ID, map[string]any{
		"name":             "Monitor test model",
		"description":      "Record only",
		"policy_type":      "model",
		"enabled":          false,
		"enforcement_mode": "monitor",
		"rule_definition": map[string]any{
			"rules": []map[string]any{{
				"id":          "warn-gpt-test",
				"action":      "warn",
				"reason_code": "model_monitored",
				"match": map[string]any{
					"models": []string{"gpt-test"},
				},
			}},
		},
	})
	updateReq = withURLParam(updateReq, "id", created.ID)
	testHandler.UpdateGatewayGovernancePolicy(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("UpdateGatewayGovernancePolicy: expected 200, got %d: %s", updateW.Code, updateW.Body.String())
	}
	var updated struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Enabled         bool   `json:"enabled"`
		Version         int    `json:"version"`
		EnforcementMode string `json:"enforcement_mode"`
	}
	if err := json.NewDecoder(updateW.Body).Decode(&updated); err != nil {
		t.Fatalf("UpdateGatewayGovernancePolicy: decode response: %v", err)
	}
	if updated.ID != created.ID || updated.Name != "Monitor test model" || updated.Enabled || updated.Version != 2 || updated.EnforcementMode != "monitor" {
		t.Fatalf("UpdateGatewayGovernancePolicy: response = %#v", updated)
	}
}

func TestGatewayPolicyHandlersRejectInvalidRuleDefinition(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/governance/policies", map[string]any{
		"name":             "Bad policy",
		"policy_type":      "model",
		"enabled":          true,
		"enforcement_mode": "enforce",
		"rule_definition": map[string]any{
			"rules": []map[string]any{{
				"id":          "bad-action",
				"action":      "blok",
				"reason_code": "invalid",
				"match": map[string]any{
					"models": []string{"gpt-test"},
				},
			}},
		},
	})

	testHandler.CreateGatewayGovernancePolicy(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreateGatewayGovernancePolicy: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGatewayServerBaseURLPrefersConfiguredGatewayURL(t *testing.T) {
	t.Setenv("MULTICA_GATEWAY_BASE_URL", "https://gateway.multica.ai/root/")
	t.Setenv("MULTICA_SERVER_URL", "https://server.multica.ai")

	req := httptest.NewRequest("GET", "/api/gateway/status", nil)
	req.Host = "request.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "forwarded.multica.ai")

	if got := gatewayServerBaseURL(req); got != "https://gateway.multica.ai/root" {
		t.Fatalf("gatewayServerBaseURL = %q, want %q", got, "https://gateway.multica.ai/root")
	}
}

func TestGatewayServerBaseURLUsesFirstForwardedValues(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/gateway/status", nil)
	req.Host = "request.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https, http")
	req.Header.Set("X-Forwarded-Host", "api.multica.ai, evil.multica.ai")

	if got := gatewayServerBaseURL(req); got != "https://api.multica.ai" {
		t.Fatalf("gatewayServerBaseURL = %q, want %q", got, "https://api.multica.ai")
	}
}

func TestGatewayServerBaseURLInvalidForwardedHostFallsBackToHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/gateway/status", nil)
	req.Host = "request.multica.ai"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.multica.ai/path")

	if got := gatewayServerBaseURL(req); got != "https://request.multica.ai" {
		t.Fatalf("gatewayServerBaseURL = %q, want %q", got, "https://request.multica.ai")
	}
}

func TestWriteGatewayResultHidesUnmappedInternalErrors(t *testing.T) {
	w := httptest.NewRecorder()

	testHandler.writeGatewayResult(w, http.StatusOK, nil, errors.New("database password leaked in detail"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("writeGatewayResult: expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("writeGatewayResult: failed to decode response: %v", err)
	}
	if resp["error"] != "gateway request failed" {
		t.Fatalf("writeGatewayResult error = %q, want %q", resp["error"], "gateway request failed")
	}
}

func TestGatewayPolicyRejectsInvalidCapturePolicy(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/policy", map[string]any{
		"capture_policy": "raw_everything",
	})

	testHandler.UpdateGatewayPolicy(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateGatewayPolicy: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
