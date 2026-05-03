package proxy

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/management"
)

func TestCapturePolicyMetadataOnlyOmitsContent(t *testing.T) {
	got := CaptureJSON(management.CaptureMetadataOnly, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "secret prompt"},
		},
	})
	if got != nil {
		t.Fatalf("metadata_only capture = %s, want nil", string(got))
	}
}

func TestCapturePolicyRedactedContentMasksSecrets(t *testing.T) {
	got := CaptureJSON(management.CaptureRedactedContent, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "use sk-proj-12345678901234567890"},
		},
	})
	if got == nil {
		t.Fatal("redacted_content returned nil")
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	messages := decoded["messages"].([]any)
	message := messages[0].(map[string]any)
	if message["content"] == "use sk-proj-12345678901234567890" {
		t.Fatalf("content was not redacted: %s", string(got))
	}
	if message["content"] != "use [REDACTED API KEY]" {
		t.Fatalf("redacted content = %#v", message["content"])
	}
}

func TestCapturePolicyFullContentPreservesContent(t *testing.T) {
	got := CaptureJSON(management.CaptureFullContent, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "plain prompt"},
		},
	})
	if got == nil {
		t.Fatal("full_content returned nil")
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode capture: %v", err)
	}
	messages := decoded["messages"].([]any)
	message := messages[0].(map[string]any)
	if message["content"] != "plain prompt" {
		t.Fatalf("content = %#v", message["content"])
	}
}

func TestOpenAIUsageExtraction(t *testing.T) {
	got := ExtractOpenAIUsage(map[string]any{
		"usage": map[string]any{
			"prompt_tokens":     float64(12),
			"completion_tokens": float64(8),
			"total_tokens":      float64(20),
		},
	})
	if got.PromptTokens != 12 || got.CompletionTokens != 8 || got.TotalTokens != 20 || got.Source != "upstream" {
		t.Fatalf("usage = %#v", got)
	}
}

func TestAnthropicUsageExtraction(t *testing.T) {
	got := ExtractAnthropicUsage(map[string]any{
		"usage": map[string]any{
			"input_tokens":  float64(10),
			"output_tokens": float64(7),
		},
	})
	if got.PromptTokens != 10 || got.CompletionTokens != 7 || got.TotalTokens != 17 || got.Source != "upstream" {
		t.Fatalf("usage = %#v", got)
	}
}
