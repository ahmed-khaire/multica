package policy

import "testing"

func TestEvaluateAllowsWhenNoRulesMatch(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "blocked-provider",
		Action:     ActionBlock,
		ReasonCode: "provider_blocked",
		Match: Match{
			Providers: []string{"shadow"},
		},
	}}, Request{Provider: "openai", Model: "gpt-4.1"})

	if got.Action != ActionAllow {
		t.Fatalf("expected allow, got %s", got.Action)
	}
	if len(got.MatchedRules) != 0 {
		t.Fatalf("expected no matches, got %#v", got.MatchedRules)
	}
}

func TestEvaluateBlocksMatchingProvider(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "block-openrouter",
		Action:     ActionBlock,
		ReasonCode: "provider_not_approved",
		Match: Match{
			Providers: []string{"openrouter"},
		},
	}}, Request{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"})

	if got.Action != ActionBlock {
		t.Fatalf("expected block, got %s", got.Action)
	}
	if got.ReasonCode != "provider_not_approved" {
		t.Fatalf("reason mismatch: %q", got.ReasonCode)
	}
}

func TestEvaluateRequiresApprovalForSensitiveDataClass(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "approval-sensitive",
		Action:     ActionRequireApproval,
		ReasonCode: "sensitive_data_requires_approval",
		Match: Match{
			DataClasses: []string{"customer_pii"},
		},
	}}, Request{DataClasses: []string{"customer_pii", "source_code"}})

	if got.Action != ActionRequireApproval {
		t.Fatalf("expected require approval, got %s", got.Action)
	}
}

func TestEvaluateChoosesHighestSeverity(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{
		{
			ID:         "warn-expensive",
			Action:     ActionWarn,
			ReasonCode: "high_cost",
			Match: Match{
				Models: []string{"gpt-4.1"},
			},
		},
		{
			ID:         "block-tool",
			Action:     ActionBlock,
			ReasonCode: "tool_blocked",
			Match: Match{
				Tools: []string{"prod-deploy"},
			},
		},
	}, Request{Model: "gpt-4.1", Tools: []string{"prod-deploy"}})

	if got.Action != ActionBlock {
		t.Fatalf("expected block to win, got %s", got.Action)
	}
	if got.ReasonCode != "tool_blocked" {
		t.Fatalf("reason mismatch: %q", got.ReasonCode)
	}
	if len(got.MatchedRules) != 2 {
		t.Fatalf("expected two matched rules, got %d", len(got.MatchedRules))
	}
}

func TestEvaluateRoutesWhenRouteRuleMatches(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:               "route-local",
		Action:           ActionRouteToBackend,
		ReasonCode:       "source_code_routes_local",
		RouteBackendSlug: "local",
		Match: Match{
			DataClasses: []string{"source_code"},
		},
	}}, Request{Provider: "openai", Model: "gpt-4.1", DataClasses: []string{"source_code"}})

	if got.Action != ActionRouteToBackend {
		t.Fatalf("expected route, got %s", got.Action)
	}
	if got.RouteBackendSlug != "local" {
		t.Fatalf("route mismatch: %q", got.RouteBackendSlug)
	}
}

func TestEvaluateRedactsMatchingTool(t *testing.T) {
	t.Parallel()

	got := Evaluate([]Rule{{
		ID:         "redact-shell",
		Action:     ActionRedact,
		ReasonCode: "tool_payload_redacted",
		Match: Match{
			Tools: []string{"shell"},
		},
	}}, Request{Tools: []string{"shell"}})

	if got.Action != ActionRedact {
		t.Fatalf("expected redact, got %s", got.Action)
	}
}
