package proxy

import "net/http"

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"

	SurfaceOpenAIChatCompletions = "openai_chat_completions"
	SurfaceAnthropicMessages     = "anthropic_messages"
	SurfaceModels                = "models"

	StatusSuccess       = "success"
	StatusUpstreamError = "upstream_error"
	StatusGatewayError  = "gateway_error"
	StatusPolicyBlocked = "policy_blocked"

	TransportDirectHTTP     = "direct_http"
	TransportDaemonDispatch = "daemon_dispatch"

	CredentialTypeAPIKey             = "api_key"
	CredentialTypeSubscriptionBundle = "subscription_bundle"

	SubscriptionProviderClaudeCode = "claude_code"
	SubscriptionProviderCodex      = "codex"

	DispatchScopeWorkspaceAuthenticatedDaemons = "workspace_authenticated_daemons"
)

type TranslationMode string

const (
	TranslationNone              TranslationMode = "none"
	TranslationOpenAIToAnthropic TranslationMode = "openai_to_anthropic"
	TranslationAnthropicToOpenAI TranslationMode = "anthropic_to_openai"
)

type AuthContext struct {
	KeyID       string
	WorkspaceID string
	UserID      string
	KeyPrefix   string
}

type BackendTarget struct {
	ID                   string
	Slug                 string
	BackendType          string
	CredentialID         string
	UpstreamProtocol     string
	BaseURL              string
	UpstreamSecret       string
	CredentialType       string
	Transport            string
	SubscriptionProvider string
	DispatchScope        string
	CapturePolicy        string
	PolicyExceptionID    string
}

type ModelRouting struct {
	RequestedModel string
	ForwardedModel string
	BackendSlug    string
	Source         string
}

type RequestSummary struct {
	Model               string
	RequestedModel      string
	ForwardedModel      string
	Stream              bool
	Body                []byte
	BodyJSON            map[string]any
	Protocol            string
	Surface             string
	RoutePath           string
	Method              string
	ExplicitBackendSlug string
	BackendSlug         string
	RoutingSource       string
	TranslationMode     TranslationMode
}

type ProxyResult struct {
	StatusCode         int
	Status             string
	ErrorType          string
	ErrorMessage       string
	ResponseHeaders    http.Header
	DurationMS         int64
	ResponseBody       []byte
	ResponseJSON       map[string]any
	Streaming          bool
	StreamingChunks    int
	TimeToFirstTokenMS int64
}

func ProtocolForRequest(r *http.Request, surface string) string {
	switch surface {
	case SurfaceAnthropicMessages:
		return ProtocolAnthropic
	case SurfaceOpenAIChatCompletions:
		return ProtocolOpenAI
	case SurfaceModels:
		if r.Header.Get("anthropic-version") != "" || r.Header.Get("x-api-key") != "" {
			return ProtocolAnthropic
		}
		return ProtocolOpenAI
	default:
		return ProtocolOpenAI
	}
}
