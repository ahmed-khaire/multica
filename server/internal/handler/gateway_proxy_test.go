package handler

import (
	"context"
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

func TestGatewayProxyTranslatesOpenAIClientToAnthropicBackend(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		if r.URL.Path != "/v1/messages" {
			t.Errorf("upstream path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-translate-anthropic" {
			t.Errorf("x-api-key = %q, want upstream key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("authorization leaked upstream: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if body["system"] != "Be concise." {
			t.Errorf("system = %#v, want Be concise.", body["system"])
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["role"] != "user" {
			t.Errorf("messages = %#v, want one user message", body["messages"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_translate",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-test",
			"content":     []any{map[string]any{"type": "text", "text": "translated hello"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 3, "output_tokens": 4},
		})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "anthropic", "translate-anthropic-proxy-test", upstream.URL, "sk-translate-anthropic")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-test","messages":[{"role":"system","content":"Be concise."},{"role":"user","content":"Hello"}],"max_tokens":64}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !sawUpstream {
		t.Fatal("upstream was not called")
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode client response: %v", err)
	}
	if resp["object"] != "chat.completion" {
		t.Fatalf("client response = %#v, want OpenAI chat.completion", resp)
	}
	choices := resp["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "translated hello" {
		t.Fatalf("message content = %#v, want translated hello", message["content"])
	}
}

func TestGatewayProxyTranslatesAnthropicClientToOpenAIBackend(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var sawUpstream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpstream = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-translate-openai" {
			t.Errorf("Authorization = %q, want upstream key", got)
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key leaked upstream: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" {
			t.Errorf("messages = %#v, want system plus user", body["messages"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl_translate",
			"object": "chat.completion",
			"model":  "gpt-test",
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": "translated hello"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 4, "total_tokens": 7},
		})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "translate-openai-proxy-test", upstream.URL+"/v1", "sk-translate-openai")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-test","system":"Be concise.","messages":[{"role":"user","content":"Hello"}],"max_tokens":64}`))
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
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode client response: %v", err)
	}
	if resp["type"] != "message" || resp["role"] != "assistant" {
		t.Fatalf("client response = %#v, want Anthropic message", resp)
	}
	content := resp["content"].([]any)
	if content[0].(map[string]any)["text"] != "translated hello" {
		t.Fatalf("content = %#v, want translated hello", content)
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

func TestGatewayProxyTranslatesAnthropicStreamToOpenAIClient(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("upstream path = %s, want /v1/messages", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if body["stream"] != true {
			t.Fatalf("stream = %#v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "anthropic", "stream-anthropic-translate-proxy-test", upstream.URL, "sk-stream-anthropic")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !w.Flushed {
		t.Fatal("expected streaming response to flush")
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"hello"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream body = %q, want OpenAI chunks and DONE", body)
	}
}

func TestGatewayProxyTranslatesOpenAIStreamToAnthropicClient(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %s, want /v1/chat/completions", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if body["stream"] != true {
			t.Fatalf("stream = %#v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, chunk := range []string{
			"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"index\":0}]}\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "stream-openai-translate-proxy-test", upstream.URL+"/v1", "sk-stream-openai")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", gatewayKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	testHandler.GatewayAnthropicMessages(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !w.Flushed {
		t.Fatal("expected streaming response to flush")
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: content_block_delta") || !strings.Contains(body, `"text":"hello"`) || !strings.Contains(body, "event: message_stop") {
		t.Fatalf("stream body = %q, want Anthropic events", body)
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

func TestGatewayProxyBlocksConfiguredModelPolicy(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-policy"})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "policy-block-model-proxy-test", upstream.URL+"/v1", "sk-policy-block")
	policyID := insertGatewayProxyPolicy(t, "policy-block-gpt-test", "model", "enforce", map[string]any{
		"rules": []map[string]any{{
			"id":          "block-gpt-test",
			"action":      "block",
			"reason_code": "model_blocked",
			"message":     "model is blocked by workspace policy",
			"match": map[string]any{
				"models": []string{"gpt-test"},
			},
		}},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", upstreamCalls)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode blocked response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok || errBody["code"] != "model_blocked" {
		t.Fatalf("blocked error body = %#v, want model_blocked", resp)
	}

	var decisions int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM gateway_policy_decision
		WHERE workspace_id = $1
		  AND decision = 'block'
		  AND reason_code = 'model_blocked'
		  AND resource_type = 'model'
		  AND resource_label = 'gpt-test'
	`, testWorkspaceID).Scan(&decisions); err != nil {
		t.Fatalf("count policy decisions: %v", err)
	}
	if decisions == 0 {
		t.Fatal("expected model policy block to record a policy decision")
	}

	var evidence int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM ai_evidence
		WHERE workspace_id = $1
		  AND linked_policy_id = $2
		  AND evidence_type = 'gateway_policy_decision'
		  AND summary LIKE '%model_blocked%'
	`, testWorkspaceID, policyID).Scan(&evidence); err != nil {
		t.Fatalf("count policy block evidence: %v", err)
	}
	if evidence == 0 {
		t.Fatal("expected model policy block to create evidence")
	}

	var incidents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM ai_incident
		WHERE workspace_id = $1
		  AND linked_policy_id = $2
		  AND category = 'gateway_policy_block'
		  AND summary LIKE '%model_blocked%'
	`, testWorkspaceID, policyID).Scan(&incidents); err != nil {
		t.Fatalf("count policy block incidents: %v", err)
	}
	if incidents == 0 {
		t.Fatal("expected model policy block to create an incident")
	}
}

func TestGatewayProxyBlocksCanonicalToolAliasPolicy(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-tool-policy"})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "policy-block-shell-tool-proxy-test", upstream.URL+"/v1", "sk-policy-tool")
	insertGatewayProxyPolicy(t, "policy-block-shell-tool-test", "tool", "enforce", map[string]any{
		"rules": []map[string]any{{
			"id":          "block-shell-tool",
			"action":      "block",
			"reason_code": "shell_tool_blocked",
			"message":     "shell tools are blocked by workspace policy",
			"match": map[string]any{
				"tools": []string{"shell"},
			},
		}},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"run a command"}],
		"tools":[{"type":"function","function":{"name":"execute_command","description":"Run a shell command","parameters":{"type":"object"}}}]
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", upstreamCalls)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode blocked response: %v", err)
	}
	errBody, ok := resp["error"].(map[string]any)
	if !ok || errBody["code"] != "shell_tool_blocked" {
		t.Fatalf("blocked error body = %#v, want shell_tool_blocked", resp)
	}
}

func TestGatewayProxyRoutesConfiguredDataPolicy(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	defaultCalls := 0
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defaultCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "default"})
	}))
	defer defaultUpstream.Close()

	routedCalls := 0
	routedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routedCalls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode routed body: %v", err)
		}
		if body["model"] != "gpt-route" {
			t.Fatalf("routed body model = %#v, want gpt-route", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "routed"})
	}))
	defer routedUpstream.Close()

	createGatewayProxyBackend(t, "local", "policy-route-default-proxy-test", defaultUpstream.URL+"/v1", "sk-policy-default")
	createGatewayProxyBackendWithDefault(t, "local", "policy-route-target-proxy-test", routedUpstream.URL+"/v1", "sk-policy-routed", false)
	setGatewayProxyDefaultBackend(t, "policy-route-default-proxy-test")
	insertGatewayProxyPolicy(t, "policy-route-source-code", "routing", "enforce", map[string]any{
		"rules": []map[string]any{{
			"id":                 "route-source-code-local",
			"action":             "route_to_backend",
			"reason_code":        "source_code_routes_local",
			"route_backend_slug": "policy-route-target-proxy-test",
			"match": map[string]any{
				"data_classes": []string{"source_code"},
			},
		}},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{\"model\":\"gpt-route\",\"messages\":[{\"role\":\"user\",\"content\":\"Please review:\\n```go\\npackage main\\nfunc main() {}\\n```\"}]}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if defaultCalls != 0 || routedCalls != 1 {
		t.Fatalf("calls default=%d routed=%d, want default=0 routed=1", defaultCalls, routedCalls)
	}

	var decisions int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM gateway_policy_decision
		WHERE workspace_id = $1
		  AND decision = 'route_to_backend'
		  AND reason_code = 'source_code_routes_local'
		  AND resource_type = 'data_class'
		  AND resource_label = 'source_code'
	`, testWorkspaceID).Scan(&decisions); err != nil {
		t.Fatalf("count policy decisions: %v", err)
	}
	if decisions == 0 {
		t.Fatal("expected routing policy to record a policy decision")
	}
}

func TestGatewayProxyMonitorPolicyRecordsButAllows(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)

	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-monitor"})
	}))
	defer upstream.Close()

	createGatewayProxyBackend(t, "local", "policy-monitor-proxy-test", upstream.URL+"/v1", "sk-policy-monitor")
	insertGatewayProxyPolicy(t, "policy-monitor-gpt-test", "model", "monitor", map[string]any{
		"rules": []map[string]any{{
			"id":          "monitor-gpt-test",
			"action":      "block",
			"reason_code": "model_monitor_only",
			"match": map[string]any{
				"models": []string{"gpt-monitor"},
			},
		}},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-monitor","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.GatewayOpenAIChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want 1", upstreamCalls)
	}

	var decisions int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM gateway_policy_decision
		WHERE workspace_id = $1
		  AND decision = 'block'
		  AND reason_code = 'model_monitor_only'
		  AND resource_label = 'gpt-monitor'
	`, testWorkspaceID).Scan(&decisions); err != nil {
		t.Fatalf("count policy decisions: %v", err)
	}
	if decisions == 0 {
		t.Fatal("expected monitor policy to record a policy decision")
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

func insertGatewayProxyPolicy(t *testing.T, name, policyType, enforcementMode string, ruleDefinition map[string]any) string {
	t.Helper()

	rules, err := json.Marshal(ruleDefinition)
	if err != nil {
		t.Fatalf("marshal policy rules: %v", err)
	}
	var policyID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO gateway_policy (
			workspace_id, name, description, policy_type, enabled,
			version, rule_definition, enforcement_mode, created_by, updated_by
		)
		VALUES ($1, $2, '', $3, TRUE, 1, $4::jsonb, $5, $6, $6)
		RETURNING id
	`, testWorkspaceID, name, policyType, string(rules), enforcementMode, testUserID).Scan(&policyID); err != nil {
		t.Fatalf("insert gateway policy %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			DELETE FROM gateway_policy
			WHERE workspace_id = $1 AND name = $2
		`, testWorkspaceID, name)
	})
	return policyID
}
