package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildUpstreamRequestOpenAIReplacesGatewayHeaders(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	inbound.Header.Set("Authorization", "Bearer mgw_gateway")
	inbound.Header.Set("X-Workspace-ID", "workspace-1")
	inbound.Header.Set("X-Multica-Trace-ID", "trace-1")
	inbound.Header.Set("Content-Type", "application/json")
	inbound.Header.Set("Accept", "text/event-stream")
	inbound.Header.Set("Connection", "keep-alive")

	req, err := BuildUpstreamRequest(context.Background(), inbound, BackendTarget{
		BaseURL:        "https://api.openai.com/v1",
		UpstreamSecret: "sk-upstream",
	}, RequestSummary{
		Protocol:  ProtocolOpenAI,
		RoutePath: "/chat/completions",
		Body:      []byte(`{"model":"gpt-test"}`),
		Method:    http.MethodPost,
	})
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}

	if req.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("upstream URL = %q", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Fatalf("Authorization = %q, want provider key", got)
	}
	if got := req.Header.Get("X-Workspace-ID"); got != "" {
		t.Fatalf("X-Workspace-ID leaked upstream: %q", got)
	}
	if got := req.Header.Get("X-Multica-Trace-ID"); got != "" {
		t.Fatalf("X-Multica-Trace-ID leaked upstream: %q", got)
	}
	if got := req.Header.Get("Connection"); got != "" {
		t.Fatalf("Connection leaked upstream: %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestBuildUpstreamRequestAnthropicUsesAPIKeyAndDefaultVersion(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	inbound.Header.Set("x-api-key", "mgw_gateway")
	inbound.Header.Set("Content-Type", "application/json")

	req, err := BuildUpstreamRequest(context.Background(), inbound, BackendTarget{
		BaseURL:        "https://api.anthropic.com",
		UpstreamSecret: "sk-ant-upstream",
	}, RequestSummary{
		Protocol:  ProtocolAnthropic,
		RoutePath: "/v1/messages",
		Body:      []byte(`{"model":"claude-test"}`),
		Method:    http.MethodPost,
	})
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}

	if req.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("upstream URL = %q", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "sk-ant-upstream" {
		t.Fatalf("x-api-key = %q, want provider key", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization leaked upstream: %q", got)
	}
	if got := req.Header.Get("anthropic-version"); got != defaultAnthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", got, defaultAnthropicVersion)
	}
}

func TestForwardNonStreamingCopiesStatusHeadersAndBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-upstream" {
			t.Errorf("Authorization = %q, want upstream key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-test"})
	}))
	defer upstream.Close()

	inbound, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	inbound.Header.Set("Authorization", "Bearer mgw_gateway")
	inbound.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	result, err := NewForwarder(upstream.Client()).Forward(context.Background(), rec, inbound, BackendTarget{
		BaseURL:        upstream.URL + "/v1",
		UpstreamSecret: "sk-upstream",
	}, RequestSummary{
		Protocol:  ProtocolOpenAI,
		RoutePath: "/chat/completions",
		Body:      []byte(`{"model":"gpt-test"}`),
		Method:    http.MethodPost,
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if result.StatusCode != http.StatusCreated || result.Streaming {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(rec.Body.String(), "chatcmpl-test") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if !strings.Contains(string(result.ResponseBody), "chatcmpl-test") {
		t.Fatalf("result body = %q", string(result.ResponseBody))
	}
}

func TestForwardStreamingFlushesChunks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream recorder lacks flusher")
		}
		for _, chunk := range []string{"data: one\n\n", "data: two\n\n"} {
			if _, err := w.Write([]byte(chunk)); err != nil {
				t.Fatalf("write chunk: %v", err)
			}
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	inbound, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	inbound.Header.Set("Authorization", "Bearer mgw_gateway")
	inbound.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	result, err := NewForwarder(upstream.Client()).Forward(context.Background(), rec, inbound, BackendTarget{
		BaseURL:        upstream.URL + "/v1",
		UpstreamSecret: "sk-upstream",
	}, RequestSummary{
		Protocol:  ProtocolOpenAI,
		RoutePath: "/chat/completions",
		Body:      []byte(`{"model":"gpt-test","stream":true}`),
		Method:    http.MethodPost,
		Stream:    true,
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if !rec.Flushed {
		t.Fatal("expected streaming response to flush")
	}
	if got := rec.Body.String(); got != "data: one\n\ndata: two\n\n" {
		t.Fatalf("body = %q", got)
	}
	if !result.Streaming || result.StreamingChunks == 0 {
		t.Fatalf("stream result = %#v", result)
	}
}
