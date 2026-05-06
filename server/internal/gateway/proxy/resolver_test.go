package proxy

import (
	"errors"
	"testing"
)

func TestCompatibleBackendType(t *testing.T) {
	if !CompatibleBackendType(ProtocolOpenAI, "openai_compatible") {
		t.Fatal("OpenAI protocol should accept openai_compatible backend")
	}
	if !CompatibleBackendType(ProtocolOpenAI, "anthropic") {
		t.Fatal("OpenAI protocol should accept anthropic backend through translation")
	}
	if !CompatibleBackendType(ProtocolAnthropic, "anthropic") {
		t.Fatal("Anthropic protocol should accept anthropic backend")
	}
	if !CompatibleBackendType(ProtocolAnthropic, "openai_compatible") {
		t.Fatal("Anthropic protocol should accept openai_compatible backend through translation")
	}
}

func TestProviderErrorShapes(t *testing.T) {
	openAI := ProviderErrorBody(ProtocolOpenAI, "gateway key is required", "authentication_error", "gateway_authentication_failed")
	openAIError, ok := openAI["error"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAI error shape = %#v", openAI)
	}
	if openAIError["message"] != "gateway key is required" {
		t.Fatalf("OpenAI error message = %#v", openAI)
	}
	if openAIError["type"] != "authentication_error" || openAIError["code"] != "gateway_authentication_failed" {
		t.Fatalf("OpenAI error type/code = %#v", openAI)
	}

	anthropic := ProviderErrorBody(ProtocolAnthropic, "gateway key is required", "authentication_error", "gateway_authentication_failed")
	anthropicError, ok := anthropic["error"].(map[string]any)
	if !ok {
		t.Fatalf("Anthropic error shape = %#v", anthropic)
	}
	if anthropic["type"] != "error" || anthropicError["type"] != "authentication_error" {
		t.Fatalf("Anthropic error type = %#v", anthropic)
	}
	if anthropicError["message"] != "gateway key is required" {
		t.Fatalf("Anthropic error message = %#v", anthropic)
	}
}

func TestNormalizeUpstreamPath(t *testing.T) {
	if got := JoinUpstreamPath("https://api.openai.com/v1", "/chat/completions"); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("OpenAI path = %q", got)
	}
	if got := JoinUpstreamPath("https://api.anthropic.com", "/v1/messages"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("Anthropic path = %q", got)
	}
}

func TestGatewayErrorsWrapSentinel(t *testing.T) {
	err := GatewayError{
		StatusCode:    404,
		PublicMessage: "gateway default backend is not configured",
		Cause:         ErrDefaultBackendNotConfigured,
	}
	if !errors.Is(err, ErrDefaultBackendNotConfigured) {
		t.Fatalf("GatewayError should wrap ErrDefaultBackendNotConfigured")
	}
}

func TestProviderRiskBlockReasonBlocksRejectedOrExpiredGovernance(t *testing.T) {
	tests := []struct {
		name                 string
		contractStatus       string
		securityReviewStatus string
		wantBlocked          bool
		wantReason           string
	}{
		{
			name:                 "rejected security review",
			contractStatus:       "approved",
			securityReviewStatus: "rejected",
			wantBlocked:          true,
			wantReason:           "provider_risk_rejected",
		},
		{
			name:                 "expired contract",
			contractStatus:       "expired",
			securityReviewStatus: "approved",
			wantBlocked:          true,
			wantReason:           "provider_risk_expired",
		},
		{
			name:                 "approved governance",
			contractStatus:       "approved",
			securityReviewStatus: "approved",
			wantBlocked:          false,
		},
		{
			name:                 "missing risk register status",
			contractStatus:       "",
			securityReviewStatus: "",
			wantBlocked:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotBlocked := ProviderRiskBlockReason(tt.contractStatus, tt.securityReviewStatus)
			if gotBlocked != tt.wantBlocked {
				t.Fatalf("blocked = %v, want %v", gotBlocked, tt.wantBlocked)
			}
			if gotReason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}
