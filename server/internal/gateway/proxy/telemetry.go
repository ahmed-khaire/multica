package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/management"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Source           string
}

type Recorder struct {
	queries *db.Queries
}

type Observation struct {
	SessionID   pgtype.UUID
	RequestID   pgtype.UUID
	WorkspaceID pgtype.UUID
	UserID      pgtype.UUID
	BackendID   pgtype.UUID
	Summary     RequestSummary
	Target      BackendTarget
	StartedAt   time.Time
}

func NewRecorder(queries *db.Queries) *Recorder {
	return &Recorder{queries: queries}
}

func (r *Recorder) Start(ctx context.Context, auth AuthContext, target BackendTarget, summary RequestSummary) *Observation {
	if r == nil || r.queries == nil {
		return nil
	}
	workspaceID, err := parseUUID(auth.WorkspaceID)
	if err != nil {
		return nil
	}
	userID, err := parseUUID(auth.UserID)
	if err != nil {
		return nil
	}
	backendID, err := parseUUID(target.ID)
	if err != nil {
		return nil
	}

	traceID := randomHex(16)
	session, err := r.queries.CreateGatewaySession(ctx, db.CreateGatewaySessionParams{
		WorkspaceID:        workspaceID,
		UserID:             userID,
		TraceID:            traceID,
		Name:               "Gateway " + summary.Protocol + " request",
		ClientProtocol:     summary.Protocol,
		ClientToolHint:     "",
		ServiceName:        "multica-gateway",
		Tags:               []byte("[]"),
		Status:             "running",
		ResourceAttributes: jsonObject(map[string]any{"gateway.surface": summary.Surface}),
	})
	if err != nil {
		return nil
	}

	request, err := r.queries.CreateGatewayRequest(ctx, db.CreateGatewayRequestParams{
		SessionID:       session.ID,
		WorkspaceID:     workspaceID,
		UserID:          userID,
		BackendID:       backendID,
		Route:           summary.RoutePath,
		Method:          summary.Method,
		ModelRequested:  requestedModel(summary),
		ModelForwarded:  forwardedModel(summary),
		ProviderSlug:    target.Slug,
		Streaming:       summary.Stream,
		Status:          "pending",
		CapturePolicy:   target.CapturePolicy,
		RequestMetadata: jsonObject(map[string]any{"protocol": summary.Protocol, "surface": summary.Surface, "key_prefix": auth.KeyPrefix, "routing_source": summary.RoutingSource}),
	})
	if err != nil {
		return nil
	}

	return &Observation{
		SessionID:   session.ID,
		RequestID:   request.ID,
		WorkspaceID: workspaceID,
		UserID:      userID,
		BackendID:   backendID,
		Summary:     summary,
		Target:      target,
		StartedAt:   time.Now(),
	}
}

func (r *Recorder) Complete(ctx context.Context, obs *Observation, result ProxyResult) {
	if r == nil || r.queries == nil || obs == nil {
		return
	}
	durationMS := result.DurationMS
	if durationMS == 0 {
		durationMS = time.Since(obs.StartedAt).Milliseconds()
	}
	status := result.Status
	if status == "" {
		status = statusForHTTP(result.StatusCode)
	}
	_, _ = r.queries.CompleteGatewayRequest(ctx, db.CompleteGatewayRequestParams{
		WorkspaceID:      obs.WorkspaceID,
		ID:               obs.RequestID,
		Status:           status,
		HttpStatus:       int4Value(result.StatusCode),
		LatencyMs:        int8Value(durationMS),
		ErrorType:        textValue(result.ErrorType),
		ErrorMessage:     textValue(result.ErrorMessage),
		ResponseMetadata: jsonObject(map[string]any{"status_code": result.StatusCode, "streaming_chunks": result.StreamingChunks}),
	})

	usage := Usage{Source: "unknown"}
	if result.ResponseJSON != nil {
		if obs.Summary.Protocol == ProtocolAnthropic {
			usage = ExtractAnthropicUsage(result.ResponseJSON)
		} else {
			usage = ExtractOpenAIUsage(result.ResponseJSON)
		}
	}
	_, _ = r.queries.CreateGatewayModelCall(ctx, db.CreateGatewayModelCallParams{
		RequestID:           obs.RequestID,
		SessionID:           obs.SessionID,
		WorkspaceID:         obs.WorkspaceID,
		BackendID:           obs.BackendID,
		ProviderSlug:        obs.Target.Slug,
		RequestModel:        forwardedModel(obs.Summary),
		ResponseModel:       responseModel(result.ResponseJSON, obs.Summary.Model),
		RequestType:         requestType(obs.Summary.Surface),
		Streaming:           result.Streaming,
		PromptMessages:      CaptureJSON(obs.Target.CapturePolicy, obs.Summary.BodyJSON),
		CompletionMessages:  CaptureJSON(obs.Target.CapturePolicy, result.ResponseJSON),
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		TotalTokens:         usage.TotalTokens,
		UsageSource:         usage.Source,
		ResponseID:          textValue(stringValue(result.ResponseJSON, "id")),
		FinishReason:        textValue(finishReason(result.ResponseJSON)),
		StopReason:          textValue(stringValue(result.ResponseJSON, "stop_reason")),
		TimeToFirstTokenMs:  int8Value(result.TimeToFirstTokenMS),
		TimeToGenerateMs:    int8Value(durationMS),
		StreamingDurationMs: int8Value(durationMS),
		StreamingChunkCount: int32(result.StreamingChunks),
	})

	sessionStatus := "success"
	errorCount := int32(0)
	if status != StatusSuccess {
		sessionStatus = "error"
		errorCount = 1
	}
	_, _ = r.queries.CompleteGatewaySession(ctx, db.CompleteGatewaySessionParams{
		WorkspaceID: obs.WorkspaceID,
		ID:          obs.SessionID,
		Status:      sessionStatus,
		EndedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		DurationMs:  int8Value(durationMS),
		SpanCount:   0,
		ErrorCount:  errorCount,
	})
}

func requestedModel(summary RequestSummary) string {
	if summary.RequestedModel != "" {
		return summary.RequestedModel
	}
	return summary.Model
}

func forwardedModel(summary RequestSummary) string {
	if summary.ForwardedModel != "" {
		return summary.ForwardedModel
	}
	return summary.Model
}

func CaptureJSON(policy string, value any) []byte {
	switch policy {
	case management.CaptureMetadataOnly:
		return nil
	case management.CaptureFullContent:
		return mustMarshal(value)
	default:
		return mustMarshal(redactValue(value))
	}
}

func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return redact.Text(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = redactValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			redacted, _ := redactValue(item).(map[string]any)
			out[i] = redacted
		}
		return out
	default:
		return v
	}
}

func ExtractOpenAIUsage(resp map[string]any) Usage {
	usageMap, _ := resp["usage"].(map[string]any)
	prompt := int64Value(usageMap["prompt_tokens"])
	completion := int64Value(usageMap["completion_tokens"])
	if prompt == 0 {
		prompt = int64Value(usageMap["input_tokens"])
	}
	if completion == 0 {
		completion = int64Value(usageMap["output_tokens"])
	}
	total := int64Value(usageMap["total_tokens"])
	if total == 0 && (prompt > 0 || completion > 0) {
		total = prompt + completion
	}
	return usageWithSource(prompt, completion, total)
}

func ExtractAnthropicUsage(resp map[string]any) Usage {
	usageMap, _ := resp["usage"].(map[string]any)
	prompt := int64Value(usageMap["input_tokens"])
	completion := int64Value(usageMap["output_tokens"])
	if prompt == 0 {
		prompt = int64Value(resp["input_tokens"])
	}
	return usageWithSource(prompt, completion, prompt+completion)
}

func usageWithSource(prompt, completion, total int64) Usage {
	source := "unknown"
	if prompt > 0 || completion > 0 || total > 0 {
		source = "upstream"
	}
	return Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
		Source:           source,
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func mustMarshal(value any) []byte {
	if value == nil {
		return nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return b
}

func jsonObject(value map[string]any) []byte {
	if len(value) == 0 {
		return []byte("{}")
	}
	return mustMarshal(value)
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "gateway-" + time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b)
}

func int4Value(value int) pgtype.Int4 {
	if value == 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(value), Valid: true}
}

func int8Value(value int64) pgtype.Int8 {
	if value == 0 {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: value, Valid: true}
}

func textValue(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func stringValue(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	v, _ := value[key].(string)
	return v
}

func responseModel(value map[string]any, fallback string) string {
	if model := stringValue(value, "model"); model != "" {
		return model
	}
	return fallback
}

func requestType(surface string) string {
	switch surface {
	case SurfaceAnthropicMessages:
		return "messages"
	case SurfaceAnthropicCountTokens:
		return "count_tokens"
	case SurfaceOpenAIResponses:
		return "responses"
	default:
		return "chat"
	}
}

func finishReason(value map[string]any) string {
	if value == nil {
		return ""
	}
	choices, _ := value["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	first, _ := choices[0].(map[string]any)
	reason, _ := first["finish_reason"].(string)
	return reason
}
