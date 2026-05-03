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
)

type AuthContext struct {
	KeyID       string
	WorkspaceID string
	UserID      string
	KeyPrefix   string
}

type BackendTarget struct {
	ID             string
	Slug           string
	BackendType    string
	BaseURL        string
	UpstreamSecret string
	CapturePolicy  string
}

type RequestSummary struct {
	Model     string
	Stream    bool
	Body      []byte
	BodyJSON  map[string]any
	Protocol  string
	Surface   string
	RoutePath string
	Method    string
}

type ProxyResult struct {
	StatusCode          int
	Status             string
	ErrorType          string
	ErrorMessage       string
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
