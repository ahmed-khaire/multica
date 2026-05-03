package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func setGatewaySecret(t *testing.T) {
	t.Helper()
	t.Setenv("MULTICA_GATEWAY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
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

func TestGatewayCreateBackendRedactsCredential(t *testing.T) {
	setGatewaySecret(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/gateway/backends", map[string]any{
		"provider": "openrouter",
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
