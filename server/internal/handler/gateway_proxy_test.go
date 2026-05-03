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

func createGatewayProxyBackend(t *testing.T, provider, slug, baseURL, key string) {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/backends", map[string]any{
		"provider":    provider,
		"slug":        slug,
		"key":         key,
		"base_url":    baseURL,
		"set_default": true,
	})
	testHandler.CreateGatewayBackend(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateGatewayBackend(%s) status = %d: %s", slug, w.Code, w.Body.String())
	}
}
