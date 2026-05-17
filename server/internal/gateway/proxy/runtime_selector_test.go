package proxy

import "testing"

func TestRuntimeProviderForSubscriptionProvider(t *testing.T) {
	tests := map[string]string{
		SubscriptionProviderCodex:      "codex",
		SubscriptionProviderClaudeCode: "claude",
	}
	for input, want := range tests {
		if got := RuntimeProviderForSubscriptionProvider(input); got != want {
			t.Fatalf("RuntimeProviderForSubscriptionProvider(%q) = %q, want %q", input, got, want)
		}
	}
}
