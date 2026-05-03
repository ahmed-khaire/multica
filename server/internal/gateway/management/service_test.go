package management

import (
	"errors"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/gateway/secrets"
)

func TestProviderPresetFor(t *testing.T) {
	tests := []struct {
		name               string
		provider           string
		slug               string
		backendType        string
		baseURL            string
		requiresCredential bool
	}{
		{
			name:               "openai",
			provider:           "openai",
			slug:               "openai",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://api.openai.com/v1",
			requiresCredential: true,
		},
		{
			name:               "groq",
			provider:           "groq",
			slug:               "groq",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://api.groq.com/openai/v1",
			requiresCredential: true,
		},
		{
			name:               "openrouter",
			provider:           "openrouter",
			slug:               "openrouter",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://openrouter.ai/api/v1",
			requiresCredential: true,
		},
		{
			name:               "local",
			provider:           "local",
			slug:               "local",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "http://127.0.0.1:11434/v1",
			requiresCredential: true,
		},
		{
			name:               "anthropic",
			provider:           "anthropic",
			slug:               "anthropic",
			backendType:        BackendTypeAnthropic,
			baseURL:            "https://api.anthropic.com",
			requiresCredential: true,
		},
		{
			name:               "claude-oauth",
			provider:           "claude-oauth",
			slug:               "claude-oauth",
			backendType:        BackendTypeClaudeOAuth,
			baseURL:            "claude-oauth://sidecar",
			requiresCredential: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, ok := ProviderPresetFor(tt.provider)
			if !ok {
				t.Fatalf("ProviderPresetFor(%q) did not resolve", tt.provider)
			}
			if preset.Slug != tt.slug {
				t.Fatalf("Slug = %q, want %q", preset.Slug, tt.slug)
			}
			if preset.BackendType != tt.backendType {
				t.Fatalf("BackendType = %q, want %q", preset.BackendType, tt.backendType)
			}
			if preset.BaseURL != tt.baseURL {
				t.Fatalf("BaseURL = %q, want %q", preset.BaseURL, tt.baseURL)
			}
			if preset.RequiresCredential != tt.requiresCredential {
				t.Fatalf("RequiresCredential = %v, want %v", preset.RequiresCredential, tt.requiresCredential)
			}
		})
	}
}

func TestProviderPresetForNormalizesInput(t *testing.T) {
	preset, ok := ProviderPresetFor(" OpenAI ")
	if !ok {
		t.Fatal("ProviderPresetFor did not normalize provider input")
	}
	if preset.Slug != "openai" {
		t.Fatalf("Slug = %q, want openai", preset.Slug)
	}
}

func TestValidateCapturePolicy(t *testing.T) {
	valid := []string{
		CaptureMetadataOnly,
		CaptureRedactedContent,
		CaptureFullContent,
	}
	for _, policy := range valid {
		t.Run(policy, func(t *testing.T) {
			if err := ValidateCapturePolicy(policy); err != nil {
				t.Fatalf("ValidateCapturePolicy(%q) returned error: %v", policy, err)
			}
		})
	}

	err := ValidateCapturePolicy("raw_everything")
	if !errors.Is(err, ErrInvalidCapturePolicy) {
		t.Fatalf("ValidateCapturePolicy(raw_everything) error = %v, want ErrInvalidCapturePolicy", err)
	}
}

func TestCredentialHint(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{name: "long key", secret: "sk-proj-1234567890abcdef", want: "sk-proj-...cdef"},
		{name: "short key", secret: "gsk_short", want: "****"},
		{name: "empty", secret: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CredentialHint(tt.secret); got != tt.want {
				t.Fatalf("CredentialHint(%q) = %q, want %q", tt.secret, got, tt.want)
			}
		})
	}
}

func TestBuildGatewayURLs(t *testing.T) {
	urls := BuildGatewayURLs("https://api.multica.ai/")
	if urls.OpenAIBaseURL != "https://api.multica.ai/v1" {
		t.Fatalf("OpenAIBaseURL = %q, want https://api.multica.ai/v1", urls.OpenAIBaseURL)
	}
	if urls.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("AnthropicBaseURL = %q, want https://api.multica.ai", urls.AnthropicBaseURL)
	}
}

func TestNormalizeSecretError(t *testing.T) {
	err := normalizeSecretError(fmt.Errorf("%s is required", secrets.EnvKeyName))
	if !errors.Is(err, ErrGatewaySecretNotConfigured) {
		t.Fatalf("normalizeSecretError error = %v, want ErrGatewaySecretNotConfigured", err)
	}
}
