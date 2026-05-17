package daemon

import "testing"

func TestOpenAIChatToClaudeCodePrompt(t *testing.T) {
	body := map[string]any{
		"model": "claude_code:sonnet",
		"messages": []any{
			map[string]any{"role": "system", "content": "Be concise."},
			map[string]any{"role": "user", "content": "Say hello."},
		},
	}

	got, err := openAIChatToClaudeCodePrompt(body)
	if err != nil {
		t.Fatalf("openAIChatToClaudeCodePrompt returned error: %v", err)
	}
	if got == "" || got != "System:\nBe concise.\n\nUser:\nSay hello." {
		t.Fatalf("prompt = %q, want role-prefixed prompt", got)
	}
}

func TestOpenAIChatToClaudeCodePromptRejectsStreaming(t *testing.T) {
	_, err := openAIChatToClaudeCodePrompt(map[string]any{
		"stream":   true,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	if err == nil {
		t.Fatal("expected streaming rejection")
	}
}
