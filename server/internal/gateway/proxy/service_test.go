package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSummarizeRequestCapturesExplicitBackendHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	req.Header.Set("X-Multica-Backend", "  openrouter-prod  ")

	summary, err := summarizeRequest(req, SurfaceOpenAIChatCompletions, ProtocolOpenAI)
	if err != nil {
		t.Fatalf("summarizeRequest: %v", err)
	}
	if summary.ExplicitBackendSlug != "openrouter-prod" {
		t.Fatalf("ExplicitBackendSlug = %q, want openrouter-prod", summary.ExplicitBackendSlug)
	}
}
