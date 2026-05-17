package proxy

import "testing"

func TestGatewayPolicyRequestIncludesPromptText(t *testing.T) {
	t.Parallel()

	req := gatewayPolicyRequest(
		AuthContext{WorkspaceID: "workspace-id", UserID: "user-id"},
		BackendTarget{ID: "backend-id", Slug: "local"},
		RequestSummary{BodyJSON: map[string]any{
			"system": "Follow company policy.",
			"messages": []any{
				map[string]any{"role": "user", "content": "This prompt mentions multica-block-demo."},
			},
		}},
	)

	if req.PromptText != "Follow company policy.\nThis prompt mentions multica-block-demo." {
		t.Fatalf("prompt text mismatch: %q", req.PromptText)
	}
}

func TestGatewayPolicyRequestIncludesAnthropicTextBlocks(t *testing.T) {
	t.Parallel()

	req := gatewayPolicyRequest(
		AuthContext{WorkspaceID: "workspace-id", UserID: "user-id"},
		BackendTarget{ID: "backend-id", Slug: "anthropic"},
		RequestSummary{BodyJSON: map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "Analyze the restricted keyword."},
					},
				},
			},
		}},
	)

	if req.PromptText != "Analyze the restricted keyword." {
		t.Fatalf("prompt text mismatch: %q", req.PromptText)
	}
}
