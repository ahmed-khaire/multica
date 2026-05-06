package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayProxyOpenAIRequiresGatewayKey(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	req.Header.Set("Content-Type", "application/json")

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAI error shape = %#v", resp)
	}
	if errBody["type"] != "authentication_error" {
		t.Fatalf("error type = %#v", errBody)
	}
}

func TestGatewayProxyAnthropicRequiresGatewayKey(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	testHandler.GatewayAnthropicMessages(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if resp["type"] != "error" || !ok || errBody["type"] != "authentication_error" {
		t.Fatalf("Anthropic error shape = %#v", resp)
	}
}

func TestGatewayProxyOpenAIChatCompletionsRoutesToDefaultBackend(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream-openai" {
			t.Errorf("Authorization = %q, want upstream key", got)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "" {
			t.Errorf("workspace header leaked upstream: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"model":   "gpt-test",
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens":     1,
				"completion_tokens": 2,
				"total_tokens":      3,
			},
		})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "local-openai-proxy-test", upstream.URL+"/v1", "sk-upstream-openai")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !sawUpstream {
		t.Fatal("upstream was not called")
	}
	if !strings.Contains(w.Body.String(), "chatcmpl-test") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestGatewayProxyAnthropicMessagesRoutesToDefaultBackend(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		if r.URL.Path != "/v1/messages" {
			t.Errorf("upstream path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-upstream-anthropic" {
			t.Errorf("x-api-key = %q, want upstream key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("authorization leaked upstream: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "msg_test",
			"type":    "message",
			"role":    "assistant",
			"model":   "claude-test",
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":  1,
				"output_tokens": 2,
			},
		})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "anthropic", "anthropic-proxy-test", upstream.URL, "sk-upstream-anthropic")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", gatewayKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	testHandler.GatewayAnthropicMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !sawUpstream {
		t.Fatal("upstream was not called")
	}
	if !strings.Contains(w.Body.String(), "msg_test") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestGatewayProxyStreamingPassThrough(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{"data: one\n\n", "data: two\n\n"} {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "local-stream-proxy-test", upstream.URL+"/v1", "sk-upstream-stream")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !w.Flushed {
		t.Fatal("expected streaming response to flush")
	}
	if got := w.Body.String(); got != "data: one\n\ndata: two\n\n" {
		t.Fatalf("stream body = %q", got)
	}
}

func TestGatewayProxyRoutesToExplicitBackendHeader(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultCalls := 0
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-default",
			"object":  "chat.completion",
			"model":   "gpt-default",
			"choices": []any{},
		})
	}))
	defer defaultUpstream.Close()

	explicitCalls := 0
	explicitUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls++
		if got := r.Header.Get("Authorization"); got != "Bearer sk-explicit" {
			t.Errorf("Authorization = %q, want explicit backend key", got)
		}
		if got := r.Header.Get("X-Multica-Backend"); got != "" {
			t.Errorf("X-Multica-Backend leaked upstream: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-explicit",
			"object":  "chat.completion",
			"model":   "gpt-explicit",
			"choices": []any{},
		})
	}))
	defer explicitUpstream.Close()

	createGatewayProxyBackend(t, "local", "local-explicit-default-proxy-test", defaultUpstream.URL+"/v1", "sk-default")
	explicitID := createGatewayProxyBackendWithDefault(t, "local", "local-explicit-target-proxy-test", explicitUpstream.URL+"/v1", "sk-explicit", false)
	setGatewayProxyDefaultBackend(t, "local-explicit-default-proxy-test")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	req.Header.Set("X-Multica-Backend", "local-explicit-target-proxy-test")

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if defaultCalls != 0 {
		t.Fatalf("default upstream calls = %d, want 0", defaultCalls)
	}
	if explicitCalls != 1 {
		t.Fatalf("explicit upstream calls = %d, want 1", explicitCalls)
	}
	if !strings.Contains(w.Body.String(), "chatcmpl-explicit") {
		t.Fatalf("body = %s", w.Body.String())
	}

	var providerSlug string
	if err := testPool.QueryRow(req.Context(), `
		SELECT provider_slug
		FROM gateway_request
		WHERE workspace_id = $1
		  AND backend_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, testWorkspaceID, explicitID).Scan(&providerSlug); err != nil {
		t.Fatalf("read explicit gateway request telemetry: %v", err)
	}
	if providerSlug != "local-explicit-target-proxy-test" {
		t.Fatalf("provider_slug = %q, want explicit backend slug", providerSlug)
	}
}

func TestGatewayProxyRoutesProviderPrefixedModel(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultCalls := 0
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer defaultUpstream.Close()

	explicitCalls := 0
	explicitUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if got := body["model"]; got != "gpt-upstream" {
			t.Errorf("upstream model = %#v, want gpt-upstream", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-prefixed",
			"object":  "chat.completion",
			"model":   "gpt-upstream",
			"choices": []any{},
		})
	}))
	defer explicitUpstream.Close()

	createGatewayProxyBackend(t, "local", "local-prefix-default-proxy-test", defaultUpstream.URL+"/v1", "sk-prefix-default")
	explicitID := createGatewayProxyBackendWithDefault(t, "local", "local-prefix-target-proxy-test", explicitUpstream.URL+"/v1", "sk-prefix-target", false)
	setGatewayProxyDefaultBackend(t, "local-prefix-default-proxy-test")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"local-prefix-target-proxy-test:gpt-upstream","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if defaultCalls != 0 {
		t.Fatalf("default upstream calls = %d, want 0", defaultCalls)
	}
	if explicitCalls != 1 {
		t.Fatalf("explicit upstream calls = %d, want 1", explicitCalls)
	}

	var got struct {
		ModelRequested string
		ModelForwarded string
		ProviderSlug   string
	}
	if err := testPool.QueryRow(req.Context(), `
		SELECT model_requested, model_forwarded, provider_slug
		FROM gateway_request
		WHERE workspace_id = $1
		  AND backend_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, testWorkspaceID, explicitID).Scan(&got.ModelRequested, &got.ModelForwarded, &got.ProviderSlug); err != nil {
		t.Fatalf("read prefixed gateway request telemetry: %v", err)
	}
	if got.ModelRequested != "local-prefix-target-proxy-test:gpt-upstream" || got.ModelForwarded != "gpt-upstream" || got.ProviderSlug != "local-prefix-target-proxy-test" {
		t.Fatalf("telemetry = %#v, want requested prefix, forwarded model, and provider slug", got)
	}
}

func TestGatewayProxyRejectsConflictingBackendHeaderAndModelPrefix(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "conflict-openrouter-proxy-test", upstream.URL+"/v1", "sk-conflict-openrouter")
	createGatewayProxyBackendWithDefault(t, "local", "conflict-groq-proxy-test", upstream.URL+"/v1", "sk-conflict-groq", false)
	setGatewayProxyDefaultBackend(t, "conflict-openrouter-proxy-test")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"conflict-groq-proxy-test:llama","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	req.Header.Set("X-Multica-Backend", "conflict-openrouter-proxy-test")

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if sawUpstream {
		t.Fatal("upstream should not be called for conflicting backend routes")
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok || errBody["code"] != "gateway_backend_conflict" {
		t.Fatalf("conflict error body = %#v, want gateway_backend_conflict", resp)
	}
}

func TestGatewayModelsAggregatesEnabledBackends(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("default upstream path = %s, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-models-default" {
			t.Errorf("default Authorization = %q, want upstream key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "gpt-default", "object": "model"},
			},
		})
	}))
	defer defaultUpstream.Close()

	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("second upstream path = %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "anthropic/claude-sonnet-4", "object": "model"},
			},
		})
	}))
	defer secondUpstream.Close()

	createGatewayProxyBackend(t, "local", "models-default-proxy-test", defaultUpstream.URL+"/v1", "sk-models-default")
	createGatewayProxyBackendWithDefault(t, "local", "models-openrouter-proxy-test", secondUpstream.URL+"/v1", "sk-models-openrouter", false)
	setGatewayProxyDefaultBackend(t, "models-default-proxy-test")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode models response: %v", err)
	}
	if resp.Object != "list" {
		t.Fatalf("object = %q, want list", resp.Object)
	}
	got := map[string]string{}
	for _, model := range resp.Data {
		got[model.ID] = model.OwnedBy
	}
	want := map[string]string{
		"gpt-default":                                            "models-default-proxy-test",
		"models-default-proxy-test:gpt-default":                  "models-default-proxy-test",
		"models-openrouter-proxy-test:anthropic/claude-sonnet-4": "models-openrouter-proxy-test",
	}
	for id, owner := range want {
		if got[id] != owner {
			t.Fatalf("models[%q] owned_by = %q, want %q; all models = %#v", id, got[id], owner, got)
		}
	}
}

func TestGatewayModelsHidesRejectedBackends(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "gpt-default", "object": "model"},
			},
		})
	}))
	defer defaultUpstream.Close()

	rejectedCalls := 0
	rejectedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rejectedCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{"id": "blocked-model", "object": "model"},
			},
		})
	}))
	defer rejectedUpstream.Close()

	createGatewayProxyBackend(t, "local", "models-risk-default-proxy-test", defaultUpstream.URL+"/v1", "sk-models-risk-default")
	rejectedID := createGatewayProxyBackendWithDefault(t, "local", "models-rejected-proxy-test", rejectedUpstream.URL+"/v1", "sk-models-rejected", false)
	setGatewayProxyDefaultBackend(t, "models-risk-default-proxy-test")

	upsertW := httptest.NewRecorder()
	upsertReq := newRequest(http.MethodPost, "/api/gateway/governance/provider-risks", map[string]any{
		"provider_name":          "models-rejected-proxy-test",
		"backend_id":             rejectedID,
		"security_review_status": "rejected",
		"contract_status":        "approved",
		"risk_score":             95,
		"review_cadence_days":    90,
	})
	testHandler.UpsertGatewayProviderRisk(upsertW, upsertReq)
	if upsertW.Code != http.StatusOK {
		t.Fatalf("UpsertGatewayProviderRisk status = %d: %s", upsertW.Code, upsertW.Body.String())
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayModels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if rejectedCalls != 0 {
		t.Fatalf("rejected upstream calls = %d, want 0", rejectedCalls)
	}
	if strings.Contains(w.Body.String(), "models-rejected-proxy-test:blocked-model") || strings.Contains(w.Body.String(), "blocked-model") {
		t.Fatalf("rejected backend model leaked into catalog: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "models-risk-default-proxy-test:gpt-default") {
		t.Fatalf("default backend model missing from catalog: %s", w.Body.String())
	}
}

func TestGatewayProxyBlocksRejectedExplicitBackend(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer defaultUpstream.Close()
	explicitCalls := 0
	explicitUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		explicitCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer explicitUpstream.Close()

	createGatewayProxyBackend(t, "local", "local-explicit-risk-default-proxy-test", defaultUpstream.URL+"/v1", "sk-default-risk")
	explicitID := createGatewayProxyBackendWithDefault(t, "local", "local-explicit-risk-target-proxy-test", explicitUpstream.URL+"/v1", "sk-explicit-risk", false)
	setGatewayProxyDefaultBackend(t, "local-explicit-risk-default-proxy-test")

	upsertW := httptest.NewRecorder()
	upsertReq := newRequest(http.MethodPost, "/api/gateway/governance/provider-risks", map[string]any{
		"provider_name":          "local-explicit-risk-target-proxy-test",
		"backend_id":             explicitID,
		"security_review_status": "rejected",
		"contract_status":        "approved",
		"risk_score":             95,
		"review_cadence_days":    90,
	})
	testHandler.UpsertGatewayProviderRisk(upsertW, upsertReq)
	if upsertW.Code != http.StatusOK {
		t.Fatalf("UpsertGatewayProviderRisk status = %d: %s", upsertW.Code, upsertW.Body.String())
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	req.Header.Set("X-Multica-Backend", "local-explicit-risk-target-proxy-test")

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if explicitCalls != 0 {
		t.Fatalf("explicit upstream calls = %d, want 0", explicitCalls)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode blocked response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok || errBody["code"] != "provider_risk_rejected" {
		t.Fatalf("blocked error body = %#v, want provider_risk_rejected", resp)
	}
}

func TestGatewayProxyBlocksRejectedProviderRisk(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	backendID := createGatewayProxyBackend(t, "local", "local-risk-rejected-proxy-test", upstream.URL+"/v1", "sk-upstream-risk")
	upsertW := httptest.NewRecorder()
	upsertReq := newRequest(http.MethodPost, "/api/gateway/governance/provider-risks", map[string]any{
		"provider_name":          "local-risk-rejected-proxy-test",
		"backend_id":             backendID,
		"security_review_status": "rejected",
		"contract_status":        "approved",
		"risk_score":             95,
		"approved_use_cases":     []string{"sandbox only"},
		"active_exception_count": 0,
		"review_cadence_days":    90,
	})
	testHandler.UpsertGatewayProviderRisk(upsertW, upsertReq)
	if upsertW.Code != http.StatusOK {
		t.Fatalf("UpsertGatewayProviderRisk status = %d: %s", upsertW.Code, upsertW.Body.String())
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if sawUpstream {
		t.Fatal("upstream should not be called for rejected provider risk")
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode blocked response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok || errBody["code"] != "provider_risk_rejected" {
		t.Fatalf("blocked error body = %#v, want provider_risk_rejected", resp)
	}

	var decisionCount int
	if err := testPool.QueryRow(req.Context(), `
		SELECT count(*)
		FROM gateway_policy_decision
		WHERE workspace_id = $1
		  AND resource_label = 'local-risk-rejected-proxy-test'
		  AND decision = 'block'
		  AND reason_code = 'provider_risk_rejected'
	`, testWorkspaceID).Scan(&decisionCount); err != nil {
		t.Fatalf("count policy decisions: %v", err)
	}
	if decisionCount == 0 {
		t.Fatal("expected provider risk block to record a policy decision")
	}

	var evidenceCount int
	if err := testPool.QueryRow(req.Context(), `
		SELECT count(*)
		FROM ai_evidence
		WHERE workspace_id = $1
		  AND evidence_type = 'gateway_policy_decision'
		  AND linked_backend_id = $2
		  AND summary LIKE '%provider_risk_rejected%'
	`, testWorkspaceID, backendID).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceCount == 0 {
		t.Fatal("expected provider risk block to create evidence")
	}

	var incidentCount int
	if err := testPool.QueryRow(req.Context(), `
		SELECT count(*)
		FROM ai_incident
		WHERE workspace_id = $1
		  AND linked_provider_risk_id IS NOT NULL
		  AND category = 'gateway_provider_risk_block'
		  AND status = 'open'
		  AND summary LIKE '%provider_risk_rejected%'
	`, testWorkspaceID).Scan(&incidentCount); err != nil {
		t.Fatalf("count incidents: %v", err)
	}
	if incidentCount == 0 {
		t.Fatal("expected provider risk block to create an incident")
	}
}

func TestGatewayProxyAllowsRejectedProviderWithApprovedException(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-exception",
			"object":  "chat.completion",
			"model":   "gpt-test",
			"choices": []any{},
		})
	}))
	defer upstream.Close()

	backendID := createGatewayProxyBackend(t, "local", "local-risk-exception-proxy-test", upstream.URL+"/v1", "sk-upstream-risk-exception")
	upsertW := httptest.NewRecorder()
	upsertReq := newRequest(http.MethodPost, "/api/gateway/governance/provider-risks", map[string]any{
		"provider_name":          "local-risk-exception-proxy-test",
		"backend_id":             backendID,
		"security_review_status": "rejected",
		"contract_status":        "approved",
		"risk_score":             95,
		"review_cadence_days":    90,
	})
	testHandler.UpsertGatewayProviderRisk(upsertW, upsertReq)
	if upsertW.Code != http.StatusOK {
		t.Fatalf("UpsertGatewayProviderRisk status = %d: %s", upsertW.Code, upsertW.Body.String())
	}
	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO ai_policy_exception (
			workspace_id, requester_user_id, approver_user_id, reason, scope, status, expires_at, evidence_references
		)
		VALUES (
			$1, $2, $2, 'Temporary approval for incident response',
			jsonb_build_object('resource_type', 'provider', 'resource_id', $3::text),
			'approved', now() + interval '1 day', '[]'::jsonb
		)
	`, testWorkspaceID, testUserID, backendID); err != nil {
		t.Fatalf("insert policy exception: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !sawUpstream {
		t.Fatal("upstream should be called when active exception exists")
	}

	var decisionCount int
	if err := testPool.QueryRow(req.Context(), `
		SELECT count(*)
		FROM gateway_policy_decision
		WHERE workspace_id = $1
		  AND resource_label = 'local-risk-exception-proxy-test'
		  AND decision = 'allow'
		  AND reason_code = 'provider_risk_exception_active'
	`, testWorkspaceID).Scan(&decisionCount); err != nil {
		t.Fatalf("count exception policy decisions: %v", err)
	}
	if decisionCount == 0 {
		t.Fatal("expected active exception to record an allow policy decision")
	}
}

func createGatewayProxyKey(t *testing.T) string {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/key", nil)
	testHandler.CreateGatewayUserKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateGatewayUserKey status = %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode gateway key: %v", err)
	}
	if resp.Key == "" {
		t.Fatal("gateway key is empty")
	}
	return resp.Key
}

func createGatewayProxyBackend(t *testing.T, provider, slug, baseURL, key string) string {
	return createGatewayProxyBackendWithDefault(t, provider, slug, baseURL, key, true)
}

func createGatewayProxyBackendWithDefault(t *testing.T, provider, slug, baseURL, key string, setDefault bool) string {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/backends", map[string]any{
		"provider":    provider,
		"slug":        slug,
		"key":         key,
		"base_url":    baseURL,
		"set_default": setDefault,
	})
	testHandler.CreateGatewayBackend(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend(%s) status = %d: %s", slug, w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode backend response: %v", err)
	}
	if resp.ID == "" {
		t.Fatalf("CreateGatewayBackend(%s) returned empty id", slug)
	}
	return resp.ID
}

func setGatewayProxyDefaultBackend(t *testing.T, slug string) {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/default", map[string]any{
		"backend_slug": slug,
	})
	testHandler.SetGatewayDefaultBackend(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SetGatewayDefaultBackend(%s) status = %d: %s", slug, w.Code, w.Body.String())
	}
	var resp struct {
		DefaultBackend *struct {
			Slug string `json:"slug"`
		} `json:"default_backend"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode default backend response: %v", err)
	}
	if resp.DefaultBackend == nil {
		t.Fatalf("default backend response is missing default_backend")
	}
	if resp.DefaultBackend.Slug != slug {
		t.Fatalf("default backend slug = %q, want %q", resp.DefaultBackend.Slug, slug)
	}
}
