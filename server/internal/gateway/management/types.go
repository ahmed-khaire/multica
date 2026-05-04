package management

import "strings"

const (
	BackendTypeOpenAICompatible = "openai_compatible"
	BackendTypeAnthropic        = "anthropic"
	BackendTypeClaudeOAuth      = "claude_oauth"

	CaptureMetadataOnly    = "metadata_only"
	CaptureRedactedContent = "redacted_content"
	CaptureFullContent     = "full_content"
	DefaultCapturePolicy   = CaptureFullContent
)

type ProviderPreset struct {
	Provider           string
	Slug               string
	DisplayName        string
	BackendType        string
	BaseURL            string
	RequiresCredential bool
}

type GatewayURLs struct {
	OpenAIBaseURL    string `json:"openai_base_url"`
	AnthropicBaseURL string `json:"anthropic_base_url"`
}

type BackendResponse struct {
	ID             string         `json:"id"`
	Slug           string         `json:"slug"`
	DisplayName    string         `json:"display_name"`
	BackendType    string         `json:"backend_type"`
	BaseURL        string         `json:"base_url"`
	CredentialHint string         `json:"credential_hint"`
	Enabled        bool           `json:"enabled"`
	IsDefault      bool           `json:"is_default"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type SettingsResponse struct {
	CapturePolicy  string           `json:"capture_policy"`
	DefaultBackend *BackendResponse `json:"default_backend"`
}

type StatusResponse struct {
	OpenAIBaseURL       string           `json:"openai_base_url"`
	AnthropicBaseURL    string           `json:"anthropic_base_url"`
	CapturePolicy       string           `json:"capture_policy"`
	DefaultBackend      *BackendResponse `json:"default_backend"`
	BackendCount        int              `json:"backend_count"`
	EnabledBackendCount int              `json:"enabled_backend_count"`
	HasActiveKey        bool             `json:"has_active_key"`
}

type UserKeyResponse struct {
	ID               string  `json:"id"`
	Key              string  `json:"key"`
	KeyPrefix        string  `json:"key_prefix"`
	OpenAIBaseURL    string  `json:"openai_base_url"`
	OpenAIAPIKey     string  `json:"openai_api_key"`
	AnthropicBaseURL string  `json:"anthropic_base_url"`
	AnthropicAPIKey  string  `json:"anthropic_api_key"`
	CreatedAt        string  `json:"created_at"`
	LastUsedAt       *string `json:"last_used_at"`
}

type UserKeyListItem struct {
	ID         string  `json:"id"`
	KeyPrefix  string  `json:"key_prefix"`
	RevokedAt  *string `json:"revoked_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

type CreateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Provider    string
	Slug        string
	DisplayName string
	BackendType string
	BaseURL     string
	Key         string
	Enabled     bool
	SetDefault  bool
	Metadata    map[string]any
}

type UpdateBackendInput struct {
	WorkspaceID string
	ActorUserID string
	BackendID   string
	DisplayName *string
	BaseURL     *string
	Key         *string
	Enabled     *bool
	Metadata    map[string]any
}

type CapturePolicyInput struct {
	WorkspaceID   string
	ActorUserID   string
	CapturePolicy string
}

type SetDefaultBackendInput struct {
	WorkspaceID string
	ActorUserID string
	Slug        string
}

var providerPresets = map[string]ProviderPreset{
	"openai": {
		Provider:           "openai",
		Slug:               "openai",
		DisplayName:        "OpenAI",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://api.openai.com/v1",
		RequiresCredential: true,
	},
	"groq": {
		Provider:           "groq",
		Slug:               "groq",
		DisplayName:        "Groq",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://api.groq.com/openai/v1",
		RequiresCredential: true,
	},
	"openrouter": {
		Provider:           "openrouter",
		Slug:               "openrouter",
		DisplayName:        "OpenRouter",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "https://openrouter.ai/api/v1",
		RequiresCredential: true,
	},
	"local": {
		Provider:           "local",
		Slug:               "local",
		DisplayName:        "Local OpenAI-compatible",
		BackendType:        BackendTypeOpenAICompatible,
		BaseURL:            "http://127.0.0.1:11434/v1",
		RequiresCredential: true,
	},
	"anthropic": {
		Provider:           "anthropic",
		Slug:               "anthropic",
		DisplayName:        "Anthropic",
		BackendType:        BackendTypeAnthropic,
		BaseURL:            "https://api.anthropic.com",
		RequiresCredential: true,
	},
	"claude-oauth": {
		Provider:           "claude-oauth",
		Slug:               "claude-oauth",
		DisplayName:        "Claude OAuth",
		BackendType:        BackendTypeClaudeOAuth,
		BaseURL:            "claude-oauth://sidecar",
		RequiresCredential: false,
	},
}

func ProviderPresetFor(provider string) (ProviderPreset, bool) {
	preset, ok := providerPresets[strings.ToLower(strings.TrimSpace(provider))]
	return preset, ok
}
