package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeOpenAIOptions struct {
	ResponseID       string
	Model            string
	PromptTokens     int64
	CompletionTokens int64
}

type fakeAnthropicOptions struct {
	ResponseID    string
	Model         string
	InputTokens   int64
	OutputTokens  int64
	StreamContent string
}

func newFakeOpenAIUpstream(t *testing.T, opts fakeOpenAIOptions) *httptest.Server {
	t.Helper()
	if opts.ResponseID == "" {
		opts.ResponseID = "chatcmpl-fake"
	}
	if opts.Model == "" {
		opts.Model = "gpt-fake"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []any{
					map[string]any{"id": opts.Model, "object": "model"},
				},
			})
		case "/v1/chat/completions":
			if got := r.Header.Get("Authorization"); got == "" {
				t.Errorf("OpenAI fake upstream missing Authorization header")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      opts.ResponseID,
				"object":  "chat.completion",
				"model":   opts.Model,
				"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "openai-ok"}}},
				"usage": map[string]any{
					"prompt_tokens":     opts.PromptTokens,
					"completion_tokens": opts.CompletionTokens,
					"total_tokens":      opts.PromptTokens + opts.CompletionTokens,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newFakeAnthropicUpstream(t *testing.T, opts fakeAnthropicOptions) *httptest.Server {
	t.Helper()
	if opts.ResponseID == "" {
		opts.ResponseID = "msg_fake"
	}
	if opts.Model == "" {
		opts.Model = "claude-fake"
	}
	if opts.StreamContent == "" {
		opts.StreamContent = "anthropic-ok"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("x-api-key"); got == "" {
			t.Errorf("Anthropic fake upstream missing x-api-key header")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Anthropic fake upstream request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Errorf("Anthropic fake upstream response writer cannot flush")
				return
			}
			_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"" + opts.ResponseID + "\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"" + opts.Model + "\"}}\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + opts.StreamContent + "\"}}\n\n"))
			flusher.Flush()
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			flusher.Flush()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      opts.ResponseID,
			"type":    "message",
			"role":    "assistant",
			"model":   opts.Model,
			"content": []any{map[string]any{"type": "text", "text": "anthropic-ok"}},
			"usage": map[string]any{
				"input_tokens":  opts.InputTokens,
				"output_tokens": opts.OutputTokens,
			},
		})
	}))
}
