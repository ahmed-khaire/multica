package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/gateway/proxy"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type txStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Service struct {
	queries   *db.Queries
	txStarter txStarter
	now       func() time.Time
}

func NewService(queries *db.Queries, txStarter txStarter) *Service {
	return &Service{
		queries:   queries,
		txStarter: txStarter,
		now:       time.Now,
	}
}

func (s *Service) IngestTrace(ctx context.Context, gatewayKey string, req TraceRequest) (TraceResponse, error) {
	if strings.TrimSpace(gatewayKey) == "" {
		return TraceResponse{}, proxy.ErrGatewayKeyRequired
	}
	if err := ValidateTraceRequest(req); err != nil {
		return TraceResponse{}, err
	}

	authContext, err := s.authenticateTraceKey(ctx, gatewayKey)
	if err != nil {
		return TraceResponse{}, err
	}
	workspaceID := authContext.workspaceID
	userID := authContext.userID
	if !workspaceID.Valid {
		return TraceResponse{}, fmt.Errorf("%w: ingest key resolved invalid workspace", ErrInvalidTracePayload)
	}

	var resp TraceResponse
	err = s.withTx(ctx, func(q *db.Queries) error {
		created, err := s.ingestWithQueries(ctx, q, workspaceID, userID, req)
		if err != nil {
			return err
		}
		resp = created
		return nil
	})
	return resp, err
}

func (s *Service) withTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.txStarter == nil {
		return fn(s.queries)
	}

	tx, err := s.txStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) ingestWithQueries(ctx context.Context, q *db.Queries, workspaceID, userID pgtype.UUID, req TraceRequest) (TraceResponse, error) {
	now := s.now().UTC()
	startedAt := firstTime(req.StartedAt, firstSpanStart(req.Spans), &now)
	endedAt := optionalTime(req.EndedAt)
	durationMS := optionalDuration(req.DurationMS, startedAt, req.EndedAt)
	rootSpanID := defaultString(req.RootSpanID, req.Spans[0].SpanID)
	sessionStatus := defaultString(req.Status, "success")
	if !validSessionStatus(sessionStatus) {
		return TraceResponse{}, fmt.Errorf("%w: status is invalid", ErrInvalidTracePayload)
	}

	tags, err := jsonValue(req.Tags, []string{})
	if err != nil {
		return TraceResponse{}, err
	}
	resourceAttributes, err := jsonValue(req.ResourceAttributes, map[string]any{})
	if err != nil {
		return TraceResponse{}, err
	}

	session, err := q.UpsertGatewaySessionForIngest(ctx, db.UpsertGatewaySessionForIngestParams{
		WorkspaceID:        workspaceID,
		UserID:             userID,
		TraceID:            strings.TrimSpace(req.TraceID),
		RootSpanID:         textValue(rootSpanID),
		Name:               defaultString(req.Name, "Observed trace"),
		ClientToolHint:     defaultString(req.ClientToolHint, "sdk"),
		ServiceName:        defaultString(req.ServiceName, "multica-sdk"),
		Tags:               tags,
		Status:             sessionStatus,
		StartedAt:          timeValue(startedAt),
		EndedAt:            endedAt,
		DurationMs:         durationMS,
		ResourceAttributes: resourceAttributes,
	})
	if err != nil {
		return TraceResponse{}, err
	}

	spanRows := make(map[string]pgtype.UUID, len(req.Spans))
	for _, span := range req.Spans {
		row, err := s.upsertSpan(ctx, q, workspaceID, session.ID, req.TraceID, startedAt, span)
		if err != nil {
			return TraceResponse{}, err
		}
		spanRows[span.SpanID] = row.ID
	}

	for _, event := range req.Events {
		spanRowID, err := spanRowID(ctx, q, workspaceID, req.TraceID, spanRows, event.SpanID)
		if err != nil {
			return TraceResponse{}, err
		}
		payload, err := jsonValue(event.Payload, map[string]any{})
		if err != nil {
			return TraceResponse{}, err
		}
		if _, err := q.CreateGatewayEvent(ctx, db.CreateGatewayEventParams{
			WorkspaceID: workspaceID,
			SessionID:   session.ID,
			SpanID:      spanRowID,
			EventType:   strings.TrimSpace(event.EventType),
			Payload:     payload,
			OccurredAt:  timeValue(firstTime(event.OccurredAt, nil, &now)),
		}); err != nil {
			return TraceResponse{}, err
		}
	}

	for _, log := range req.Logs {
		spanRowID, err := spanRowID(ctx, q, workspaceID, req.TraceID, spanRows, log.SpanID)
		if err != nil {
			return TraceResponse{}, err
		}
		attributes, err := jsonValue(log.Attributes, map[string]any{})
		if err != nil {
			return TraceResponse{}, err
		}
		if _, err := q.CreateGatewayLog(ctx, db.CreateGatewayLogParams{
			WorkspaceID: workspaceID,
			SessionID:   session.ID,
			SpanID:      spanRowID,
			Severity:    defaultString(log.Severity, "info"),
			Body:        strings.TrimSpace(log.Body),
			Attributes:  attributes,
			OccurredAt:  timeValue(firstTime(log.OccurredAt, nil, &now)),
		}); err != nil {
			return TraceResponse{}, err
		}
	}

	for _, agent := range req.Agents {
		spanRowID, err := spanRowID(ctx, q, workspaceID, req.TraceID, spanRows, agent.SpanID)
		if err != nil {
			return TraceResponse{}, err
		}
		models, err := jsonValue(agent.Models, []string{})
		if err != nil {
			return TraceResponse{}, err
		}
		tools, err := jsonValue(agent.Tools, []string{})
		if err != nil {
			return TraceResponse{}, err
		}
		if _, err := q.CreateGatewayAgentObservation(ctx, db.CreateGatewayAgentObservationParams{
			WorkspaceID:        workspaceID,
			SessionID:          session.ID,
			SpanRowID:          spanRowID,
			AgentID:            strings.TrimSpace(agent.AgentID),
			AgentName:          strings.TrimSpace(agent.AgentName),
			Role:               strings.TrimSpace(agent.Role),
			Models:             models,
			Tools:              tools,
			HandoffSource:      strings.TrimSpace(agent.HandoffSource),
			HandoffDestination: strings.TrimSpace(agent.HandoffDestination),
			ReasoningSummary:   strings.TrimSpace(agent.ReasoningSummary),
		}); err != nil {
			return TraceResponse{}, err
		}
	}

	for _, tool := range req.Tools {
		spanRowID, err := spanRowID(ctx, q, workspaceID, req.TraceID, spanRows, tool.SpanID)
		if err != nil {
			return TraceResponse{}, err
		}
		parameters, err := nullableJSONValue(tool.Parameters)
		if err != nil {
			return TraceResponse{}, err
		}
		result, err := nullableJSONValue(tool.Result)
		if err != nil {
			return TraceResponse{}, err
		}
		if _, err := q.CreateGatewayToolObservation(ctx, db.CreateGatewayToolObservationParams{
			WorkspaceID: workspaceID,
			SessionID:   session.ID,
			SpanRowID:   spanRowID,
			ToolID:      strings.TrimSpace(tool.ToolID),
			ToolName:    strings.TrimSpace(tool.ToolName),
			Description: strings.TrimSpace(tool.Description),
			Parameters:  parameters,
			Result:      result,
			Status:      defaultString(tool.Status, "unknown"),
			DurationMs:  int8Ptr(tool.DurationMS),
		}); err != nil {
			return TraceResponse{}, err
		}
	}

	session, err = q.RefreshGatewaySessionIngestSummary(ctx, db.RefreshGatewaySessionIngestSummaryParams{
		WorkspaceID: workspaceID,
		SessionID:   session.ID,
	})
	if err != nil {
		return TraceResponse{}, err
	}

	return TraceResponse{
		TraceID:    session.TraceID,
		SessionID:  util.UUIDToString(session.ID),
		SpanCount:  int(session.SpanCount),
		EventCount: len(req.Events),
		LogCount:   len(req.Logs),
		AgentCount: len(req.Agents),
		ToolCount:  len(req.Tools),
	}, nil
}

func (s *Service) upsertSpan(ctx context.Context, q *db.Queries, workspaceID, sessionID pgtype.UUID, traceID string, sessionStartedAt time.Time, span SpanPayload) (db.GatewaySpan, error) {
	startedAt := firstTime(span.StartedAt, nil, &sessionStartedAt)
	attributes, err := jsonValue(span.Attributes, map[string]any{})
	if err != nil {
		return db.GatewaySpan{}, err
	}
	resourceAttributes, err := jsonValue(span.ResourceAttributes, map[string]any{})
	if err != nil {
		return db.GatewaySpan{}, err
	}

	return q.UpsertGatewaySpanForIngest(ctx, db.UpsertGatewaySpanForIngestParams{
		SessionID:          sessionID,
		WorkspaceID:        workspaceID,
		TraceID:            strings.TrimSpace(traceID),
		SpanID:             strings.TrimSpace(span.SpanID),
		ParentSpanID:       textValue(span.ParentSpanID),
		SpanKind:           defaultString(span.Kind, "unknown"),
		Name:               strings.TrimSpace(span.Name),
		ServiceName:        defaultString(span.ServiceName, "multica-sdk"),
		StatusCode:         defaultString(span.StatusCode, "unset"),
		StatusMessage:      strings.TrimSpace(span.StatusMessage),
		StartedAt:          timeValue(startedAt),
		EndedAt:            optionalTime(span.EndedAt),
		DurationMs:         optionalDuration(span.DurationMS, startedAt, span.EndedAt),
		Attributes:         attributes,
		ResourceAttributes: resourceAttributes,
	})
}

func spanRowID(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, traceID string, known map[string]pgtype.UUID, externalSpanID string) (pgtype.UUID, error) {
	externalSpanID = strings.TrimSpace(externalSpanID)
	if externalSpanID == "" {
		return pgtype.UUID{}, nil
	}
	if id, ok := known[externalSpanID]; ok {
		return id, nil
	}
	row, err := q.GetGatewaySpanByTraceSpanID(ctx, db.GetGatewaySpanByTraceSpanIDParams{
		WorkspaceID: workspaceID,
		TraceID:     strings.TrimSpace(traceID),
		SpanID:      externalSpanID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, fmt.Errorf("%w: referenced span_id %q was not found", ErrInvalidTracePayload, externalSpanID)
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	return row.ID, nil
}

func firstSpanStart(spans []SpanPayload) *time.Time {
	for _, span := range spans {
		if span.StartedAt != nil {
			return span.StartedAt
		}
	}
	return nil
}

func firstTime(values ...*time.Time) time.Time {
	for _, value := range values {
		if value != nil && !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Now().UTC()
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil || value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func timeValue(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func textValue(value string) pgtype.Text {
	value = strings.TrimSpace(value)
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func optionalDuration(value *int64, startedAt time.Time, endedAt *time.Time) pgtype.Int8 {
	if value != nil {
		return int8Value(*value)
	}
	if endedAt != nil && !endedAt.IsZero() {
		return int8Value(endedAt.Sub(startedAt).Milliseconds())
	}
	return pgtype.Int8{}
}

func int8Ptr(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return int8Value(*value)
}

func int8Value(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: true}
}

func jsonValue(value any, fallback any) ([]byte, error) {
	if value == nil || isNilValue(value) {
		value = fallback
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid json value: %v", ErrInvalidTracePayload, err)
	}
	return raw, nil
}

func nullableJSONValue(value any) ([]byte, error) {
	if value == nil || isNilValue(value) {
		return nil, nil
	}
	return jsonValue(value, nil)
}

func isNilValue(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
