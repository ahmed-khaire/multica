package proxy

import (
	"encoding/json"
	"regexp"
	"strings"

	gatewaypolicy "github.com/multica-ai/multica/server/internal/gateway/policy"
)

var secretLikePattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{12,}|api[_-]?key|token|password|secret)`)

func gatewayPolicyRequest(authCtx AuthContext, target BackendTarget, summary RequestSummary) gatewaypolicy.RequestContext {
	return gatewaypolicy.RequestContext{
		WorkspaceID: authCtx.WorkspaceID,
		UserID:      authCtx.UserID,
		ProviderID:  target.ID,
		Provider:    target.Slug,
		Model:       policyModel(summary),
		Tools:       extractToolNames(summary.BodyJSON),
		DataClasses: detectDataClasses(summary.BodyJSON),
	}
}

func policyModel(summary RequestSummary) string {
	if summary.ForwardedModel != "" {
		return summary.ForwardedModel
	}
	if summary.Model != "" {
		return summary.Model
	}
	return summary.RequestedModel
}

func extractToolNames(body map[string]any) []string {
	if body == nil {
		return nil
	}
	rawTools, _ := body["tools"].([]any)
	names := []string{}
	for _, raw := range rawTools {
		tool, _ := raw.(map[string]any)
		if tool == nil {
			continue
		}
		if name, _ := tool["name"].(string); name != "" {
			names = append(names, name)
			continue
		}
		fn, _ := tool["function"].(map[string]any)
		if name, _ := fn["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func detectDataClasses(body map[string]any) []string {
	if body == nil {
		return nil
	}
	text := strings.ToLower(strings.Join(policyTextParts(body), "\n"))
	classes := []string{}
	add := func(class string) {
		for _, existing := range classes {
			if existing == class {
				return
			}
		}
		classes = append(classes, class)
	}
	if strings.Contains(text, "```") ||
		strings.Contains(text, "package ") ||
		strings.Contains(text, "func ") ||
		strings.Contains(text, "import ") ||
		strings.Contains(text, "class ") {
		add("source_code")
	}
	if secretLikePattern.MatchString(text) {
		add("secret")
	}
	if strings.Contains(text, "@") {
		add("personal_data")
	}
	return classes
}

func policyTextParts(body map[string]any) []string {
	parts := []string{}
	if system := textContent(body["system"]); system != "" {
		parts = append(parts, system)
	}
	if messages, ok := body["messages"].([]any); ok {
		for _, raw := range messages {
			msg, _ := raw.(map[string]any)
			if msg == nil {
				continue
			}
			if content := textContent(msg["content"]); content != "" {
				parts = append(parts, content)
			}
		}
	}
	if len(parts) == 0 {
		if encoded, err := json.Marshal(body); err == nil {
			parts = append(parts, string(encoded))
		}
	}
	return parts
}
