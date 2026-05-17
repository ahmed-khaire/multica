package daemon

import (
	"strings"
	"testing"
)

func TestOpenAIChatToCodexPrompt(t *testing.T) {
	body := map[string]any{
		"model": "codex:gpt-5.3-codex",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are precise."},
			map[string]any{"role": "user", "content": "Say hi."},
		},
	}
	got, err := openAIChatToCodexPrompt(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "System:\nYou are precise.") {
		t.Fatalf("prompt missing system: %s", got)
	}
	if !strings.Contains(got, "User:\nSay hi.") {
		t.Fatalf("prompt missing user: %s", got)
	}
}

func TestOpenAIChatToCodexPromptRejectsUnsupportedFeatures(t *testing.T) {
	body := map[string]any{
		"stream": true,
		"messages": []any{
			map[string]any{"role": "user", "content": "Say hi."},
		},
	}
	if _, err := openAIChatToCodexPrompt(body); err == nil {
		t.Fatal("expected stream error")
	}
}

func TestCodexOutputToOpenAIChat(t *testing.T) {
	got := codexOutputToOpenAIChat("gpt-5.3-codex", "hello")
	if got["object"] != "chat.completion" || got["model"] != "gpt-5.3-codex" {
		t.Fatalf("response = %#v", got)
	}
	choices := got["choices"].([]map[string]any)
	message := choices[0]["message"].(map[string]any)
	if message["role"] != "assistant" || message["content"] != "hello" {
		t.Fatalf("message = %#v", message)
	}
}
