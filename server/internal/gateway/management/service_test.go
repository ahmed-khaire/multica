package management

import (
	"encoding/json"
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
		displayName        string
		requiresCredential bool
	}{
		{
			name:               "openai",
			provider:           "openai",
			slug:               "openai",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://api.openai.com/v1",
			displayName:        "OpenAI",
			requiresCredential: true,
		},
		{
			name:               "groq",
			provider:           "groq",
			slug:               "groq",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://api.groq.com/openai/v1",
			displayName:        "Groq",
			requiresCredential: true,
		},
		{
			name:               "openrouter",
			provider:           "openrouter",
			slug:               "openrouter",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "https://openrouter.ai/api/v1",
			displayName:        "OpenRouter",
			requiresCredential: true,
		},
		{
			name:               "local",
			provider:           "local",
			slug:               "local",
			backendType:        BackendTypeOpenAICompatible,
			baseURL:            "http://127.0.0.1:11434/v1",
			displayName:        "Local OpenAI-compatible",
			requiresCredential: true,
		},
		{
			name:               "anthropic",
			provider:           "anthropic",
			slug:               "anthropic",
			backendType:        BackendTypeAnthropic,
			baseURL:            "https://api.anthropic.com",
			displayName:        "Anthropic",
			requiresCredential: true,
		},
		{
			name:               "claude-oauth",
			provider:           "claude-oauth",
			slug:               "claude-oauth",
			backendType:        BackendTypeClaudeOAuth,
			baseURL:            "claude-oauth://sidecar",
			displayName:        "Claude OAuth",
			requiresCredential: false,
		},
		{
			name:               "claude-code-subscription",
			provider:           "claude-code-subscription",
			slug:               "claude-code-subscription",
			backendType:        BackendTypeSubscriptionRuntime,
			baseURL:            "daemon://claude-code",
			displayName:        "Claude Code Subscription",
			requiresCredential: true,
		},
		{
			name:               "codex-subscription",
			provider:           "codex-subscription",
			slug:               "codex-subscription",
			backendType:        BackendTypeSubscriptionRuntime,
			baseURL:            "daemon://codex",
			displayName:        "Codex Subscription",
			requiresCredential: true,
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
			if preset.DisplayName != tt.displayName {
				t.Fatalf("DisplayName = %q, want %q", preset.DisplayName, tt.displayName)
			}
			if preset.RequiresCredential != tt.requiresCredential {
				t.Fatalf("RequiresCredential = %v, want %v", preset.RequiresCredential, tt.requiresCredential)
			}
		})
	}
}

func TestProviderPresetForUnknown(t *testing.T) {
	if _, ok := ProviderPresetFor("unknown"); ok {
		t.Fatal("ProviderPresetFor resolved unknown provider")
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

func TestRuntimeProviderForSubscriptionProvider(t *testing.T) {
	tests := map[string]string{
		SubscriptionProviderCodex:      "codex",
		SubscriptionProviderClaudeCode: "claude",
		"unknown":                      "",
	}
	for input, want := range tests {
		if got := runtimeProviderForSubscriptionProvider(input); got != want {
			t.Fatalf("runtimeProviderForSubscriptionProvider(%q) = %q, want %q", input, got, want)
		}
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

func TestDefaultCapturePolicyIsFullContent(t *testing.T) {
	if DefaultCapturePolicy != CaptureFullContent {
		t.Fatalf("DefaultCapturePolicy = %q, want %q", DefaultCapturePolicy, CaptureFullContent)
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
		{name: "trims whitespace", secret: "  sk-proj-1234567890abcdef  ", want: "sk-proj-...cdef"},
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

	urls = BuildGatewayURLs("https://api.multica.ai///")
	if urls.OpenAIBaseURL != "https://api.multica.ai/v1" {
		t.Fatalf("OpenAIBaseURL = %q, want https://api.multica.ai/v1", urls.OpenAIBaseURL)
	}
	if urls.AnthropicBaseURL != "https://api.multica.ai" {
		t.Fatalf("AnthropicBaseURL = %q, want https://api.multica.ai", urls.AnthropicBaseURL)
	}
}

func TestNormalizeSecretError(t *testing.T) {
	if err := normalizeSecretError(nil); err != nil {
		t.Fatalf("normalizeSecretError(nil) = %v, want nil", err)
	}

	err := normalizeSecretError(fmt.Errorf("%s is required", secrets.EnvKeyName))
	if !errors.Is(err, ErrGatewaySecretNotConfigured) {
		t.Fatalf("normalizeSecretError error = %v, want ErrGatewaySecretNotConfigured", err)
	}

	unrelated := errors.New("unrelated error")
	if err := normalizeSecretError(unrelated); err != unrelated {
		t.Fatalf("normalizeSecretError unrelated error = %v, want original error", err)
	}
}

func TestResponseDTOJSONTags(t *testing.T) {
	defaultBackend := &BackendResponse{
		ID:             "backend_1",
		Slug:           "openai",
		DisplayName:    "OpenAI",
		BackendType:    BackendTypeOpenAICompatible,
		BaseURL:        "https://api.openai.com/v1",
		CredentialHint: "sk-proj-...cdef",
		Transport:      TransportDirectHTTP,
		Enabled:        true,
		IsDefault:      true,
		Metadata:       map[string]any{"tier": "prod"},
		CreatedAt:      "2026-05-03T00:00:00Z",
		UpdatedAt:      "2026-05-03T00:00:00Z",
	}

	assertJSONKeys(t, "BackendResponse", defaultBackend, []string{
		"id",
		"slug",
		"display_name",
		"backend_type",
		"base_url",
		"credential_hint",
		"transport",
		"enabled",
		"is_default",
		"metadata",
		"created_at",
		"updated_at",
	})

	assertJSONKeys(t, "SettingsResponse", SettingsResponse{
		CapturePolicy:  CaptureRedactedContent,
		DefaultBackend: defaultBackend,
	}, []string{
		"capture_policy",
		"default_backend",
	})

	assertJSONKeys(t, "StatusResponse", StatusResponse{
		OpenAIBaseURL:       "https://api.multica.ai/v1",
		AnthropicBaseURL:    "https://api.multica.ai",
		CapturePolicy:       CaptureRedactedContent,
		DefaultBackend:      defaultBackend,
		BackendCount:        2,
		EnabledBackendCount: 1,
		HasActiveKey:        true,
	}, []string{
		"openai_base_url",
		"anthropic_base_url",
		"capture_policy",
		"default_backend",
		"backend_count",
		"enabled_backend_count",
		"has_active_key",
	})

	lastUsedAt := "2026-05-03T00:00:00Z"
	assertJSONKeys(t, "UserKeyResponse", UserKeyResponse{
		ID:               "key_1",
		Key:              "mk_live_123",
		KeyPrefix:        "mk_live",
		OpenAIBaseURL:    "https://api.multica.ai/v1",
		OpenAIAPIKey:     "mk_live_123",
		AnthropicBaseURL: "https://api.multica.ai",
		AnthropicAPIKey:  "mk_live_123",
		CreatedAt:        "2026-05-03T00:00:00Z",
		LastUsedAt:       &lastUsedAt,
	}, []string{
		"id",
		"key",
		"key_prefix",
		"openai_base_url",
		"openai_api_key",
		"anthropic_base_url",
		"anthropic_api_key",
		"created_at",
		"last_used_at",
	})

	assertJSONKeys(t, "UserKeyListItem", UserKeyListItem{
		ID:         "key_1",
		KeyPrefix:  "mk_live",
		RevokedAt:  &lastUsedAt,
		LastUsedAt: &lastUsedAt,
		CreatedAt:  "2026-05-03T00:00:00Z",
	}, []string{
		"id",
		"key_prefix",
		"revoked_at",
		"last_used_at",
		"created_at",
	})
}

func assertJSONKeys(t *testing.T, name string, value any, wantKeys []string) {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%s) returned error: %v", name, err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) returned error: %v", name, err)
	}

	for _, key := range wantKeys {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s JSON missing key %q; got keys %v", name, key, got)
		}
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("%s JSON key count = %d, want %d; got keys %v", name, len(got), len(wantKeys), got)
	}
}
