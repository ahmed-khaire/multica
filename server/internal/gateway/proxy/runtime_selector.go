package proxy

func RuntimeProviderForSubscriptionProvider(provider string) string {
	switch provider {
	case SubscriptionProviderCodex:
		return "codex"
	case SubscriptionProviderClaudeCode:
		return "claude"
	default:
		return ""
	}
}
