package proxy

import (
	"strings"
	"testing"
)

func TestTranslateAnthropicStreamToOpenAIChunks(t *testing.T) {
	frame := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")

	chunks := TranslateStreamChunk(frame, TranslationOpenAIToAnthropic)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %#v, want one translated chunk", chunks)
	}
	got := string(chunks[0])
	if !strings.HasPrefix(got, "data: ") || !strings.Contains(got, `"content":"hello"`) {
		t.Fatalf("translated chunk = %q", got)
	}

	stop := TranslateStreamChunk([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), TranslationOpenAIToAnthropic)
	if len(stop) != 1 || string(stop[0]) != "data: [DONE]\n\n" {
		t.Fatalf("stop chunk = %#v, want OpenAI DONE", stop)
	}
}

func TestTranslateOpenAIStreamToAnthropicEvents(t *testing.T) {
	frame := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"index\":0}]}\n\n")

	chunks := TranslateStreamChunk(frame, TranslationAnthropicToOpenAI)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %#v, want one translated chunk", chunks)
	}
	got := string(chunks[0])
	if !strings.Contains(got, "event: content_block_delta") || !strings.Contains(got, `"text":"hello"`) {
		t.Fatalf("translated chunk = %q", got)
	}

	stop := TranslateStreamChunk([]byte("data: [DONE]\n\n"), TranslationAnthropicToOpenAI)
	if len(stop) != 1 || !strings.Contains(string(stop[0]), "event: message_stop") {
		t.Fatalf("stop chunk = %#v, want Anthropic message_stop", stop)
	}
}
