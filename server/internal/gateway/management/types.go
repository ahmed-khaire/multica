package management

import "strings"

const (
	BackendTypeOpenAICompatible = "openai_compatible"
	BackendTypeAnthropic        = "anthropic"
	BackendTypeClaudeOAuth      = "claude_oauth"

	CaptureMetadataOnly    = "metadata_only"
	CaptureRedactedContent = "redacted_content"
	CaptureFullContent     = "full_content"
	DefaultCapturePolicy   = CaptureRedactedContent
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
	ID             string
	Slug           string
	DisplayName    string
	BackendType    string
	BaseURL        string
	CredentialHint string
	Enabled        bool
	IsDefault      bool
	Metadata       map[string]any
	CreatedAt      string
	UpdatedAt      string
}

type SettingsResponse struct {
	CapturePolicy  string
	DefaultBackend *BackendResponse
}

type StatusResponse struct {
	OpenAIBaseURL       string
	AnthropicBaseURL    string
	CapturePolicy       string
	DefaultBackend      *BackendResponse
	BackendCount        int
	EnabledBackendCount int
	HasActiveKey        bool
}

type UserKeyResponse struct {
	ID               string
	Key              string
	KeyPrefix        string
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	AnthropicBaseURL string
	AnthropicAPIKey  string
	CreatedAt        string
	LastUsedAt       *string
}

type UserKeyListItem struct {
	ID         string
	KeyPrefix  string
	RevokedAt  *string
	LastUsedAt *string
	CreatedAt  string
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
		DisplayName:        "Local",
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
