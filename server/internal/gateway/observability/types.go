package observability

type OverviewResponse struct {
	Since       string           `json:"since"`
	Until       string           `json:"until"`
	BucketWidth string           `json:"bucket_width"`
	Summary     OverviewSummary  `json:"summary"`
	TimeSeries  []OverviewBucket `json:"time_series"`
	TopModels   []ModelUsage     `json:"top_models"`
	TopBackends []BackendUsage   `json:"top_backends"`
}

type OverviewSummary struct {
	SessionCount          int64    `json:"session_count"`
	RequestCount          int64    `json:"request_count"`
	LLMCallCount          int64    `json:"llm_call_count"`
	PromptTokens          int64    `json:"prompt_tokens"`
	CompletionTokens      int64    `json:"completion_tokens"`
	TotalTokens           int64    `json:"total_tokens"`
	TotalCost             *float64 `json:"total_cost"`
	ErrorCount            int64    `json:"error_count"`
	StreamingRequestCount int64    `json:"streaming_request_count"`
	AvgLatencyMS          int64    `json:"avg_latency_ms"`
}

type OverviewBucket struct {
	BucketStart  string   `json:"bucket_start"`
	RequestCount int64    `json:"request_count"`
	ErrorCount   int64    `json:"error_count"`
	TotalTokens  int64    `json:"total_tokens"`
	TotalCost    *float64 `json:"total_cost"`
}

type ModelUsage struct {
	Model       string   `json:"model"`
	CallCount   int64    `json:"call_count"`
	TotalTokens int64    `json:"total_tokens"`
	TotalCost   *float64 `json:"total_cost"`
}

type BackendUsage struct {
	Backend      string   `json:"backend"`
	CallCount    int64    `json:"call_count"`
	ErrorCount   int64    `json:"error_count"`
	AvgLatencyMS int64    `json:"avg_latency_ms"`
	TotalTokens  int64    `json:"total_tokens"`
	TotalCost    *float64 `json:"total_cost"`
}

type SessionListResponse struct {
	Sessions []SessionListItem `json:"sessions"`
	Total    int64             `json:"total"`
	Limit    int32             `json:"limit"`
	Since    string            `json:"since"`
}

type SessionListItem struct {
	ID                    string         `json:"id"`
	TraceID               string         `json:"trace_id"`
	RootSpanID            string         `json:"root_span_id"`
	Name                  string         `json:"name"`
	ClientProtocol        string         `json:"client_protocol"`
	ClientToolHint        string         `json:"client_tool_hint"`
	ServiceName           string         `json:"service_name"`
	Tags                  any            `json:"tags"`
	Status                string         `json:"status"`
	StartedAt             string         `json:"started_at"`
	EndedAt               *string        `json:"ended_at"`
	DurationMS            *int64         `json:"duration_ms"`
	SpanCount             int32          `json:"span_count"`
	ErrorCount            int32          `json:"error_count"`
	TotalCost             *float64       `json:"total_cost"`
	ResourceAttributes    map[string]any `json:"resource_attributes"`
	RequestCount          int64          `json:"request_count"`
	LLMCallCount          int64          `json:"llm_call_count"`
	PromptTokens          int64          `json:"prompt_tokens"`
	CompletionTokens      int64          `json:"completion_tokens"`
	TotalTokens           int64          `json:"total_tokens"`
	UsageCost             *float64       `json:"usage_cost"`
	RequestErrorCount     int64          `json:"request_error_count"`
	StreamingRequestCount int64          `json:"streaming_request_count"`
	AvgLatencyMS          int64          `json:"avg_latency_ms"`
	Models                []string       `json:"models"`
	Backends              []string       `json:"backends"`
}

type SessionDetailResponse struct {
	ID                 string              `json:"id"`
	TraceID            string              `json:"trace_id"`
	RootSpanID         string              `json:"root_span_id"`
	Name               string              `json:"name"`
	ClientProtocol     string              `json:"client_protocol"`
	ClientToolHint     string              `json:"client_tool_hint"`
	ServiceName        string              `json:"service_name"`
	Tags               any                 `json:"tags"`
	Status             string              `json:"status"`
	StartedAt          string              `json:"started_at"`
	EndedAt            *string             `json:"ended_at"`
	DurationMS         *int64              `json:"duration_ms"`
	SpanCount          int32               `json:"span_count"`
	ErrorCount         int32               `json:"error_count"`
	TotalCost          *float64            `json:"total_cost"`
	ResourceAttributes map[string]any      `json:"resource_attributes"`
	Requests           []RequestResponse   `json:"requests"`
	ModelCalls         []ModelCallResponse `json:"model_calls"`
	Events             []EventResponse     `json:"events"`
	Logs               []LogResponse       `json:"logs"`
	Agents             []AgentObservation  `json:"agents"`
	Tools              []ToolObservation   `json:"tools"`
}

type RequestResponse struct {
	ID               string         `json:"id"`
	SessionID        string         `json:"session_id"`
	BackendID        string         `json:"backend_id"`
	Route            string         `json:"route"`
	Method           string         `json:"method"`
	ModelRequested   string         `json:"model_requested"`
	ModelForwarded   string         `json:"model_forwarded"`
	ProviderSlug     string         `json:"provider_slug"`
	Streaming        bool           `json:"streaming"`
	Status           string         `json:"status"`
	HTTPStatus       *int32         `json:"http_status"`
	LatencyMS        *int64         `json:"latency_ms"`
	ErrorType        string         `json:"error_type"`
	ErrorMessage     string         `json:"error_message"`
	CapturePolicy    string         `json:"capture_policy"`
	RequestMetadata  map[string]any `json:"request_metadata"`
	ResponseMetadata map[string]any `json:"response_metadata"`
	CreatedAt        string         `json:"created_at"`
	CompletedAt      *string        `json:"completed_at"`
}

type ModelCallResponse struct {
	ID                       string   `json:"id"`
	RequestID                string   `json:"request_id"`
	SessionID                string   `json:"session_id"`
	BackendID                string   `json:"backend_id"`
	ProviderSlug             string   `json:"provider_slug"`
	RequestModel             string   `json:"request_model"`
	ResponseModel            string   `json:"response_model"`
	RequestType              string   `json:"request_type"`
	Streaming                bool     `json:"streaming"`
	PromptMessages           any      `json:"prompt_messages"`
	CompletionMessages       any      `json:"completion_messages"`
	CompletionChunks         any      `json:"completion_chunks"`
	PromptTokens             int64    `json:"prompt_tokens"`
	CompletionTokens         int64    `json:"completion_tokens"`
	TotalTokens              int64    `json:"total_tokens"`
	CacheCreationInputTokens int64    `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64    `json:"cache_read_input_tokens"`
	ReasoningTokens          int64    `json:"reasoning_tokens"`
	StreamingTokens          int64    `json:"streaming_tokens"`
	UsageSource              string   `json:"usage_source"`
	PromptCost               *float64 `json:"prompt_cost"`
	CompletionCost           *float64 `json:"completion_cost"`
	TotalCost                *float64 `json:"total_cost"`
	ResponseID               string   `json:"response_id"`
	FinishReason             string   `json:"finish_reason"`
	StopReason               string   `json:"stop_reason"`
	TimeToFirstTokenMS       *int64   `json:"time_to_first_token_ms"`
	TimeToGenerateMS         *int64   `json:"time_to_generate_ms"`
	StreamingDurationMS      *int64   `json:"streaming_duration_ms"`
	StreamingChunkCount      int32    `json:"streaming_chunk_count"`
	CreatedAt                string   `json:"created_at"`
}

type EventResponse struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	RequestID  string `json:"request_id"`
	SpanID     string `json:"span_id"`
	EventType  string `json:"event_type"`
	Payload    any    `json:"payload"`
	OccurredAt string `json:"occurred_at"`
}

type LogResponse struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	RequestID  string         `json:"request_id"`
	SpanID     string         `json:"span_id"`
	Severity   string         `json:"severity"`
	Body       string         `json:"body"`
	Attributes map[string]any `json:"attributes"`
	OccurredAt string         `json:"occurred_at"`
}

type AgentObservation struct {
	ID                 string `json:"id"`
	SessionID          string `json:"session_id"`
	SpanRowID          string `json:"span_row_id"`
	AgentID            string `json:"agent_id"`
	AgentName          string `json:"agent_name"`
	Role               string `json:"role"`
	Models             any    `json:"models"`
	Tools              any    `json:"tools"`
	HandoffSource      string `json:"handoff_source"`
	HandoffDestination string `json:"handoff_destination"`
	ReasoningSummary   string `json:"reasoning_summary"`
	CreatedAt          string `json:"created_at"`
}

type ToolObservation struct {
	ID                string `json:"id"`
	SessionID         string `json:"session_id"`
	SpanRowID         string `json:"span_row_id"`
	ToolID            string `json:"tool_id"`
	ToolName          string `json:"tool_name"`
	CanonicalToolType string `json:"canonical_tool_type"`
	ToolRiskLevel     string `json:"tool_risk_level"`
	Description       string `json:"description"`
	Parameters        any    `json:"parameters"`
	Result            any    `json:"result"`
	Status            string `json:"status"`
	DurationMS        *int64 `json:"duration_ms"`
	CreatedAt         string `json:"created_at"`
}

type SessionSpansResponse struct {
	SessionID string         `json:"session_id"`
	TraceID   string         `json:"trace_id"`
	Spans     []SpanResponse `json:"spans"`
}

type SpanResponse struct {
	ID                 string         `json:"id"`
	SessionID          string         `json:"session_id"`
	RequestID          string         `json:"request_id"`
	TraceID            string         `json:"trace_id"`
	SpanID             string         `json:"span_id"`
	ParentSpanID       string         `json:"parent_span_id"`
	Name               string         `json:"name"`
	SpanName           string         `json:"span_name"`
	SpanKind           string         `json:"span_kind"`
	SpanType           string         `json:"span_type"`
	ServiceName        string         `json:"service_name"`
	StartTime          string         `json:"start_time"`
	EndTime            *string        `json:"end_time"`
	Duration           int64          `json:"duration"`
	DurationMS         *int64         `json:"duration_ms"`
	StatusCode         string         `json:"status_code"`
	StatusMessage      string         `json:"status_message"`
	Attributes         map[string]any `json:"attributes"`
	ResourceAttributes map[string]any `json:"resource_attributes"`
}

type LLMCallListResponse struct {
	Calls []LLMCallListItem `json:"calls"`
	Total int64             `json:"total"`
	Limit int32             `json:"limit"`
	Since string            `json:"since"`
}

type LLMCallListItem struct {
	ModelCallResponse
	TraceID       string `json:"trace_id"`
	SessionName   string `json:"session_name"`
	Route         string `json:"route"`
	RequestStatus string `json:"request_status"`
	HTTPStatus    *int32 `json:"http_status"`
	LatencyMS     *int64 `json:"latency_ms"`
	CapturePolicy string `json:"capture_policy"`
}
