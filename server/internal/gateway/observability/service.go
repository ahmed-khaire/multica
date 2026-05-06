package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	gatewaypolicy "github.com/multica-ai/multica/server/internal/gateway/policy"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type Service struct {
	queries *db.Queries
}

func NewService(queries *db.Queries) *Service {
	return &Service{queries: queries}
}

func (s *Service) Overview(ctx context.Context, workspaceID string, filter Filter) (OverviewResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return OverviewResponse{}, err
	}

	summary, err := s.queries.GetGatewayOverviewSummary(ctx, db.GetGatewayOverviewSummaryParams{
		WorkspaceID: workspaceUUID,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
		Model:       filter.Model,
	})
	if err != nil {
		return OverviewResponse{}, err
	}

	buckets, err := s.queries.ListGatewayOverviewBuckets(ctx, db.ListGatewayOverviewBucketsParams{
		WorkspaceID: workspaceUUID,
		BucketWidth: filter.BucketWidth,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
		Model:       filter.Model,
	})
	if err != nil {
		return OverviewResponse{}, err
	}

	models, err := s.queries.ListGatewayTopModels(ctx, db.ListGatewayTopModelsParams{
		WorkspaceID: workspaceUUID,
		Limit:       10,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
		Model:       filter.Model,
	})
	if err != nil {
		return OverviewResponse{}, err
	}

	backends, err := s.queries.ListGatewayTopBackends(ctx, db.ListGatewayTopBackendsParams{
		WorkspaceID: workspaceUUID,
		Limit:       10,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
		Model:       filter.Model,
	})
	if err != nil {
		return OverviewResponse{}, err
	}

	return OverviewResponse{
		Since:       filter.Since.Format(time.RFC3339),
		Until:       filter.Until.Format(time.RFC3339),
		BucketWidth: filter.BucketWidth,
		Summary: OverviewSummary{
			SessionCount:          summary.SessionCount,
			RequestCount:          summary.RequestCount,
			LLMCallCount:          summary.LlmCallCount,
			PromptTokens:          summary.PromptTokens,
			CompletionTokens:      summary.CompletionTokens,
			TotalTokens:           summary.TotalTokens,
			TotalCost:             numericFloat(summary.TotalCost),
			ErrorCount:            summary.ErrorCount,
			StreamingRequestCount: summary.StreamingRequestCount,
			AvgLatencyMS:          summary.AvgLatencyMs,
		},
		TimeSeries:  overviewBuckets(buckets),
		TopModels:   modelUsage(models),
		TopBackends: backendUsage(backends),
	}, nil
}

func (s *Service) ListSessions(ctx context.Context, workspaceID string, filter Filter) (SessionListResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return SessionListResponse{}, err
	}
	rows, err := s.queries.ListGatewaySessionsDashboard(ctx, db.ListGatewaySessionsDashboardParams{
		WorkspaceID: workspaceUUID,
		Limit:       filter.Limit,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
	})
	if err != nil {
		return SessionListResponse{}, err
	}

	total := int64(0)
	items := make([]SessionListItem, 0, len(rows))
	for _, row := range rows {
		if total == 0 {
			total = row.TotalCount
		}
		items = append(items, sessionListItem(row))
	}
	return SessionListResponse{
		Sessions: items,
		Total:    total,
		Limit:    filter.Limit,
		Since:    filter.Since.Format(time.RFC3339),
	}, nil
}

func (s *Service) GetSession(ctx context.Context, workspaceID, sessionID string) (SessionDetailResponse, error) {
	workspaceUUID, sessionUUID, err := scopedSessionIDs(workspaceID, sessionID)
	if err != nil {
		return SessionDetailResponse{}, err
	}
	session, err := s.queries.GetGatewaySession(ctx, db.GetGatewaySessionParams{
		WorkspaceID: workspaceUUID,
		ID:          sessionUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionDetailResponse{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionDetailResponse{}, err
	}

	requests, err := s.queries.ListGatewayRequestsForSession(ctx, db.ListGatewayRequestsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}
	modelCalls, err := s.queries.ListGatewayModelCallsForSession(ctx, db.ListGatewayModelCallsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}
	events, err := s.queries.ListGatewayEventsForSession(ctx, db.ListGatewayEventsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}
	logs, err := s.queries.ListGatewayLogsForSession(ctx, db.ListGatewayLogsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}
	agents, err := s.queries.ListGatewayAgentObservationsForSession(ctx, db.ListGatewayAgentObservationsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}
	tools, err := s.queries.ListGatewayToolObservationsForSession(ctx, db.ListGatewayToolObservationsForSessionParams{WorkspaceID: workspaceUUID, SessionID: sessionUUID})
	if err != nil {
		return SessionDetailResponse{}, err
	}

	resp := sessionDetail(session)
	resp.Requests = mapRequests(requests)
	resp.ModelCalls = mapModelCalls(modelCalls)
	resp.Events = mapEvents(events)
	resp.Logs = mapLogs(logs)
	resp.Agents = mapAgents(agents)
	resp.Tools = mapTools(tools)
	return resp, nil
}

func (s *Service) ListSessionSpans(ctx context.Context, workspaceID, sessionID string) (SessionSpansResponse, error) {
	workspaceUUID, sessionUUID, err := scopedSessionIDs(workspaceID, sessionID)
	if err != nil {
		return SessionSpansResponse{}, err
	}
	session, err := s.queries.GetGatewaySession(ctx, db.GetGatewaySessionParams{
		WorkspaceID: workspaceUUID,
		ID:          sessionUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionSpansResponse{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionSpansResponse{}, err
	}
	rows, err := s.queries.ListGatewaySpansForSession(ctx, db.ListGatewaySpansForSessionParams{
		WorkspaceID: workspaceUUID,
		SessionID:   sessionUUID,
	})
	if err != nil {
		return SessionSpansResponse{}, err
	}
	spans := make([]SpanResponse, 0, len(rows))
	for _, row := range rows {
		spans = append(spans, spanResponse(row))
	}
	return SessionSpansResponse{
		SessionID: util.UUIDToString(session.ID),
		TraceID:   session.TraceID,
		Spans:     spans,
	}, nil
}

func (s *Service) ListLLMCalls(ctx context.Context, workspaceID string, filter Filter) (LLMCallListResponse, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return LLMCallListResponse{}, err
	}
	rows, err := s.queries.ListGatewayLLMCalls(ctx, db.ListGatewayLLMCallsParams{
		WorkspaceID: workspaceUUID,
		Limit:       filter.Limit,
		Since:       timestamp(filter.Since),
		Status:      filter.Status,
		Backend:     filter.Backend,
		Model:       filter.Model,
	})
	if err != nil {
		return LLMCallListResponse{}, err
	}
	total := int64(0)
	calls := make([]LLMCallListItem, 0, len(rows))
	for _, row := range rows {
		if total == 0 {
			total = row.TotalCount
		}
		calls = append(calls, llmCallListItem(row))
	}
	return LLMCallListResponse{
		Calls: calls,
		Total: total,
		Limit: filter.Limit,
		Since: filter.Since.Format(time.RFC3339),
	}, nil
}

func scopedSessionIDs(workspaceID, sessionID string) (pgtype.UUID, pgtype.UUID, error) {
	workspaceUUID, err := uuidValue(workspaceID, "workspace_id")
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	sessionUUID, err := uuidValue(sessionID, "session_id")
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	return workspaceUUID, sessionUUID, nil
}

func uuidValue(id, field string) (pgtype.UUID, error) {
	var value pgtype.UUID
	if err := value.Scan(strings.TrimSpace(id)); err != nil || !value.Valid {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid %s", ErrInvalidID, field)
	}
	return value, nil
}

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func overviewBuckets(rows []db.ListGatewayOverviewBucketsRow) []OverviewBucket {
	items := make([]OverviewBucket, 0, len(rows))
	for _, row := range rows {
		items = append(items, OverviewBucket{
			BucketStart:  util.TimestampToString(row.BucketStart),
			RequestCount: row.RequestCount,
			ErrorCount:   row.ErrorCount,
			TotalTokens:  row.TotalTokens,
			TotalCost:    numericFloat(row.TotalCost),
		})
	}
	return items
}

func modelUsage(rows []db.ListGatewayTopModelsRow) []ModelUsage {
	items := make([]ModelUsage, 0, len(rows))
	for _, row := range rows {
		items = append(items, ModelUsage{
			Model:       row.Model,
			CallCount:   row.CallCount,
			TotalTokens: row.TotalTokens,
			TotalCost:   numericFloat(row.TotalCost),
		})
	}
	return items
}

func backendUsage(rows []db.ListGatewayTopBackendsRow) []BackendUsage {
	items := make([]BackendUsage, 0, len(rows))
	for _, row := range rows {
		items = append(items, BackendUsage{
			Backend:      row.Backend,
			CallCount:    row.CallCount,
			ErrorCount:   row.ErrorCount,
			AvgLatencyMS: row.AvgLatencyMs,
			TotalTokens:  row.TotalTokens,
			TotalCost:    numericFloat(row.TotalCost),
		})
	}
	return items
}

func sessionListItem(row db.ListGatewaySessionsDashboardRow) SessionListItem {
	return SessionListItem{
		ID:                    util.UUIDToString(row.ID),
		TraceID:               row.TraceID,
		RootSpanID:            text(row.RootSpanID),
		Name:                  row.Name,
		ClientProtocol:        row.ClientProtocol,
		ClientToolHint:        row.ClientToolHint,
		ServiceName:           row.ServiceName,
		Tags:                  jsonAny(row.Tags),
		Status:                row.Status,
		StartedAt:             util.TimestampToString(row.StartedAt),
		EndedAt:               util.TimestampToPtr(row.EndedAt),
		DurationMS:            int8(row.DurationMs),
		SpanCount:             row.SpanCount,
		ErrorCount:            row.ErrorCount,
		TotalCost:             numericFloat(row.TotalCost),
		ResourceAttributes:    jsonMap(row.ResourceAttributes),
		RequestCount:          row.RequestCount,
		LLMCallCount:          row.LlmCallCount,
		PromptTokens:          row.PromptTokens,
		CompletionTokens:      row.CompletionTokens,
		TotalTokens:           row.TotalTokens,
		UsageCost:             numericFloat(row.UsageCost),
		RequestErrorCount:     row.RequestErrorCount,
		StreamingRequestCount: row.StreamingRequestCount,
		AvgLatencyMS:          row.AvgLatencyMs,
		Models:                row.Models,
		Backends:              row.Backends,
	}
}

func sessionDetail(row db.GatewaySession) SessionDetailResponse {
	return SessionDetailResponse{
		ID:                 util.UUIDToString(row.ID),
		TraceID:            row.TraceID,
		RootSpanID:         text(row.RootSpanID),
		Name:               row.Name,
		ClientProtocol:     row.ClientProtocol,
		ClientToolHint:     row.ClientToolHint,
		ServiceName:        row.ServiceName,
		Tags:               jsonAny(row.Tags),
		Status:             row.Status,
		StartedAt:          util.TimestampToString(row.StartedAt),
		EndedAt:            util.TimestampToPtr(row.EndedAt),
		DurationMS:         int8(row.DurationMs),
		SpanCount:          row.SpanCount,
		ErrorCount:         row.ErrorCount,
		TotalCost:          numericFloat(row.TotalCost),
		ResourceAttributes: jsonMap(row.ResourceAttributes),
	}
}

func mapRequests(rows []db.GatewayRequest) []RequestResponse {
	items := make([]RequestResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, RequestResponse{
			ID:               util.UUIDToString(row.ID),
			SessionID:        util.UUIDToString(row.SessionID),
			BackendID:        util.UUIDToString(row.BackendID),
			Route:            row.Route,
			Method:           row.Method,
			ModelRequested:   row.ModelRequested,
			ModelForwarded:   row.ModelForwarded,
			ProviderSlug:     row.ProviderSlug,
			Streaming:        row.Streaming,
			Status:           row.Status,
			HTTPStatus:       int4(row.HttpStatus),
			LatencyMS:        int8(row.LatencyMs),
			ErrorType:        text(row.ErrorType),
			ErrorMessage:     text(row.ErrorMessage),
			CapturePolicy:    row.CapturePolicy,
			RequestMetadata:  jsonMap(row.RequestMetadata),
			ResponseMetadata: jsonMap(row.ResponseMetadata),
			CreatedAt:        util.TimestampToString(row.CreatedAt),
			CompletedAt:      util.TimestampToPtr(row.CompletedAt),
		})
	}
	return items
}

func mapModelCalls(rows []db.GatewayModelCall) []ModelCallResponse {
	items := make([]ModelCallResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, modelCallResponse(row))
	}
	return items
}

func modelCallResponse(row db.GatewayModelCall) ModelCallResponse {
	return ModelCallResponse{
		ID:                       util.UUIDToString(row.ID),
		RequestID:                util.UUIDToString(row.RequestID),
		SessionID:                util.UUIDToString(row.SessionID),
		BackendID:                util.UUIDToString(row.BackendID),
		ProviderSlug:             row.ProviderSlug,
		RequestModel:             row.RequestModel,
		ResponseModel:            row.ResponseModel,
		RequestType:              row.RequestType,
		Streaming:                row.Streaming,
		PromptMessages:           jsonAny(row.PromptMessages),
		CompletionMessages:       jsonAny(row.CompletionMessages),
		CompletionChunks:         jsonAny(row.CompletionChunks),
		PromptTokens:             row.PromptTokens,
		CompletionTokens:         row.CompletionTokens,
		TotalTokens:              row.TotalTokens,
		CacheCreationInputTokens: row.CacheCreationInputTokens,
		CacheReadInputTokens:     row.CacheReadInputTokens,
		ReasoningTokens:          row.ReasoningTokens,
		StreamingTokens:          row.StreamingTokens,
		UsageSource:              row.UsageSource,
		PromptCost:               numericFloat(row.PromptCost),
		CompletionCost:           numericFloat(row.CompletionCost),
		TotalCost:                numericFloat(row.TotalCost),
		ResponseID:               text(row.ResponseID),
		FinishReason:             text(row.FinishReason),
		StopReason:               text(row.StopReason),
		TimeToFirstTokenMS:       int8(row.TimeToFirstTokenMs),
		TimeToGenerateMS:         int8(row.TimeToGenerateMs),
		StreamingDurationMS:      int8(row.StreamingDurationMs),
		StreamingChunkCount:      row.StreamingChunkCount,
		CreatedAt:                util.TimestampToString(row.CreatedAt),
	}
}

func mapEvents(rows []db.GatewayEvent) []EventResponse {
	items := make([]EventResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, EventResponse{
			ID:         util.UUIDToString(row.ID),
			SessionID:  util.UUIDToString(row.SessionID),
			RequestID:  util.UUIDToString(row.RequestID),
			SpanID:     util.UUIDToString(row.SpanID),
			EventType:  row.EventType,
			Payload:    jsonAny(row.Payload),
			OccurredAt: util.TimestampToString(row.OccurredAt),
		})
	}
	return items
}

func mapLogs(rows []db.GatewayLog) []LogResponse {
	items := make([]LogResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, LogResponse{
			ID:         util.UUIDToString(row.ID),
			SessionID:  util.UUIDToString(row.SessionID),
			RequestID:  util.UUIDToString(row.RequestID),
			SpanID:     util.UUIDToString(row.SpanID),
			Severity:   row.Severity,
			Body:       row.Body,
			Attributes: jsonMap(row.Attributes),
			OccurredAt: util.TimestampToString(row.OccurredAt),
		})
	}
	return items
}

func mapAgents(rows []db.GatewayAgentObservation) []AgentObservation {
	items := make([]AgentObservation, 0, len(rows))
	for _, row := range rows {
		items = append(items, AgentObservation{
			ID:                 util.UUIDToString(row.ID),
			SessionID:          util.UUIDToString(row.SessionID),
			SpanRowID:          util.UUIDToString(row.SpanRowID),
			AgentID:            row.AgentID,
			AgentName:          row.AgentName,
			Role:               row.Role,
			Models:             jsonAny(row.Models),
			Tools:              jsonAny(row.Tools),
			HandoffSource:      row.HandoffSource,
			HandoffDestination: row.HandoffDestination,
			ReasoningSummary:   row.ReasoningSummary,
			CreatedAt:          util.TimestampToString(row.CreatedAt),
		})
	}
	return items
}

func mapTools(rows []db.GatewayToolObservation) []ToolObservation {
	items := make([]ToolObservation, 0, len(rows))
	for _, row := range rows {
		toolName := row.ToolName
		if strings.TrimSpace(toolName) == "" {
			toolName = row.ToolID
		}
		profile := gatewaypolicy.DescribeTool(toolName)
		items = append(items, ToolObservation{
			ID:                util.UUIDToString(row.ID),
			SessionID:         util.UUIDToString(row.SessionID),
			SpanRowID:         util.UUIDToString(row.SpanRowID),
			ToolID:            row.ToolID,
			ToolName:          row.ToolName,
			CanonicalToolType: profile.CanonicalName,
			ToolRiskLevel:     profile.RiskLevel,
			Description:       row.Description,
			Parameters:        jsonAny(row.Parameters),
			Result:            jsonAny(row.Result),
			Status:            row.Status,
			DurationMS:        int8(row.DurationMs),
			CreatedAt:         util.TimestampToString(row.CreatedAt),
		})
	}
	return items
}

func spanResponse(row db.GatewaySpan) SpanResponse {
	durationMS := int8(row.DurationMs)
	durationNS := int64(0)
	if durationMS != nil {
		durationNS = *durationMS * int64(time.Millisecond)
	}
	return SpanResponse{
		ID:                 util.UUIDToString(row.ID),
		SessionID:          util.UUIDToString(row.SessionID),
		RequestID:          util.UUIDToString(row.RequestID),
		TraceID:            row.TraceID,
		SpanID:             row.SpanID,
		ParentSpanID:       text(row.ParentSpanID),
		Name:               row.Name,
		SpanName:           row.Name,
		SpanKind:           row.SpanKind,
		SpanType:           spanType(row),
		ServiceName:        row.ServiceName,
		StartTime:          util.TimestampToString(row.StartedAt),
		EndTime:            util.TimestampToPtr(row.EndedAt),
		Duration:           durationNS,
		DurationMS:         durationMS,
		StatusCode:         row.StatusCode,
		StatusMessage:      row.StatusMessage,
		Attributes:         jsonMap(row.Attributes),
		ResourceAttributes: jsonMap(row.ResourceAttributes),
	}
}

func llmCallListItem(row db.ListGatewayLLMCallsRow) LLMCallListItem {
	call := modelCallResponse(db.GatewayModelCall{
		ID:                       row.ID,
		RequestID:                row.RequestID,
		SessionID:                row.SessionID,
		WorkspaceID:              row.WorkspaceID,
		BackendID:                row.BackendID,
		ProviderSlug:             row.ProviderSlug,
		RequestModel:             row.RequestModel,
		ResponseModel:            row.ResponseModel,
		RequestType:              row.RequestType,
		Streaming:                row.Streaming,
		PromptMessages:           row.PromptMessages,
		CompletionMessages:       row.CompletionMessages,
		CompletionChunks:         row.CompletionChunks,
		PromptTokens:             row.PromptTokens,
		CompletionTokens:         row.CompletionTokens,
		TotalTokens:              row.TotalTokens,
		CacheCreationInputTokens: row.CacheCreationInputTokens,
		CacheReadInputTokens:     row.CacheReadInputTokens,
		ReasoningTokens:          row.ReasoningTokens,
		StreamingTokens:          row.StreamingTokens,
		UsageSource:              row.UsageSource,
		PromptCost:               row.PromptCost,
		CompletionCost:           row.CompletionCost,
		TotalCost:                row.TotalCost,
		ResponseID:               row.ResponseID,
		FinishReason:             row.FinishReason,
		StopReason:               row.StopReason,
		TimeToFirstTokenMs:       row.TimeToFirstTokenMs,
		TimeToGenerateMs:         row.TimeToGenerateMs,
		StreamingDurationMs:      row.StreamingDurationMs,
		StreamingChunkCount:      row.StreamingChunkCount,
		CreatedAt:                row.CreatedAt,
	})
	return LLMCallListItem{
		ModelCallResponse: call,
		TraceID:           row.TraceID,
		SessionName:       row.SessionName,
		Route:             row.Route,
		RequestStatus:     row.RequestStatus,
		HTTPStatus:        int4(row.HttpStatus),
		LatencyMS:         int8(row.LatencyMs),
		CapturePolicy:     row.CapturePolicy,
	}
}

func spanType(row db.GatewaySpan) string {
	attrs := jsonMap(row.Attributes)
	if value, ok := attrs["span.type"].(string); ok && value != "" {
		return value
	}
	if row.SpanKind != "" {
		return row.SpanKind
	}
	return "unknown"
}

func jsonAny(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func jsonMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	return value
}

func text(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func int4(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}

func int8(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func numericFloat(value pgtype.Numeric) *float64 {
	if !value.Valid || value.Int == nil || value.NaN {
		return nil
	}

	rat := new(big.Rat).SetInt(value.Int)
	if value.Exp < 0 {
		rat.Quo(rat, new(big.Rat).SetInt(pow10(-value.Exp)))
	} else if value.Exp > 0 {
		rat.Mul(rat, new(big.Rat).SetInt(pow10(value.Exp)))
	}
	f, _ := rat.Float64()
	return &f
}

func pow10(exp int32) *big.Int {
	out := big.NewInt(1)
	ten := big.NewInt(10)
	for i := int32(0); i < exp; i++ {
		out.Mul(out, ten)
	}
	return out
}
