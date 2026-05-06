package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type gatewayObservabilitySeed struct {
	SessionID string
	RequestID string
	SpanID    string
}

func TestGatewayObservabilityOverviewAndSessions(t *testing.T) {
	seed := seedGatewayObservabilityTelemetry(t, "overview")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/gateway/overview?since=24h", nil)
	testHandler.GatewayOverview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GatewayOverview status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var overview struct {
		Summary struct {
			SessionCount          int64    `json:"session_count"`
			RequestCount          int64    `json:"request_count"`
			LLMCallCount          int64    `json:"llm_call_count"`
			TotalTokens           int64    `json:"total_tokens"`
			ErrorCount            int64    `json:"error_count"`
			StreamingRequestCount int64    `json:"streaming_request_count"`
			TotalCost             *float64 `json:"total_cost"`
		} `json:"summary"`
		TimeSeries []struct {
			RequestCount int64 `json:"request_count"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"time_series"`
		TopModels []struct {
			Model     string `json:"model"`
			CallCount int64  `json:"call_count"`
		} `json:"top_models"`
		TopBackends []struct {
			Backend   string `json:"backend"`
			CallCount int64  `json:"call_count"`
		} `json:"top_backends"`
	}
	if err := json.NewDecoder(w.Body).Decode(&overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.Summary.SessionCount != 1 || overview.Summary.RequestCount != 1 || overview.Summary.LLMCallCount != 1 {
		t.Fatalf("summary counts = sessions:%d requests:%d calls:%d, want 1/1/1", overview.Summary.SessionCount, overview.Summary.RequestCount, overview.Summary.LLMCallCount)
	}
	if overview.Summary.TotalTokens != 30 {
		t.Fatalf("total tokens = %d, want 30", overview.Summary.TotalTokens)
	}
	if overview.Summary.StreamingRequestCount != 1 {
		t.Fatalf("streaming request count = %d, want 1", overview.Summary.StreamingRequestCount)
	}
	if len(overview.TimeSeries) == 0 {
		t.Fatal("expected non-empty time series")
	}
	if len(overview.TopModels) == 0 || overview.TopModels[0].Model != "gpt-observe" {
		t.Fatalf("top models = %#v, want gpt-observe first", overview.TopModels)
	}
	if len(overview.TopBackends) == 0 || overview.TopBackends[0].Backend != "openrouter" {
		t.Fatalf("top backends = %#v, want openrouter first", overview.TopBackends)
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/gateway/sessions?since=24h&backend=openrouter", nil)
	testHandler.ListGatewaySessions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewaySessions status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var sessions struct {
		Sessions []struct {
			ID           string   `json:"id"`
			RequestCount int64    `json:"request_count"`
			LLMCallCount int64    `json:"llm_call_count"`
			TotalTokens  int64    `json:"total_tokens"`
			Models       []string `json:"models"`
			Backends     []string `json:"backends"`
		} `json:"sessions"`
		Total int64 `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if sessions.Total != 1 || len(sessions.Sessions) != 1 {
		t.Fatalf("sessions total/len = %d/%d, want 1/1", sessions.Total, len(sessions.Sessions))
	}
	if sessions.Sessions[0].ID != seed.SessionID {
		t.Fatalf("session id = %q, want %q", sessions.Sessions[0].ID, seed.SessionID)
	}
	if sessions.Sessions[0].RequestCount != 1 || sessions.Sessions[0].LLMCallCount != 1 || sessions.Sessions[0].TotalTokens != 30 {
		t.Fatalf("session aggregates = %#v, want 1 request, 1 call, 30 tokens", sessions.Sessions[0])
	}
	if len(sessions.Sessions[0].Models) != 1 || sessions.Sessions[0].Models[0] != "gpt-observe" {
		t.Fatalf("session models = %#v, want gpt-observe", sessions.Sessions[0].Models)
	}
	if len(sessions.Sessions[0].Backends) != 1 || sessions.Sessions[0].Backends[0] != "openrouter" {
		t.Fatalf("session backends = %#v, want openrouter", sessions.Sessions[0].Backends)
	}
}

func TestGatewayObservabilitySessionDetailAndSpans(t *testing.T) {
	seed := seedGatewayObservabilityTelemetry(t, "detail")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/gateway/sessions/"+seed.SessionID, nil)
	req = withURLParam(req, "id", seed.SessionID)
	testHandler.GetGatewaySession(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetGatewaySession status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var detail struct {
		ID         string `json:"id"`
		Requests   []any  `json:"requests"`
		ModelCalls []struct {
			RequestID          string         `json:"request_id"`
			RequestModel       string         `json:"request_model"`
			PromptMessages     map[string]any `json:"prompt_messages"`
			CompletionMessages map[string]any `json:"completion_messages"`
		} `json:"model_calls"`
		Events []any `json:"events"`
		Logs   []any `json:"logs"`
		Agents []any `json:"agents"`
		Tools  []struct {
			ToolName          string `json:"tool_name"`
			CanonicalToolType string `json:"canonical_tool_type"`
			ToolRiskLevel     string `json:"tool_risk_level"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(w.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != seed.SessionID {
		t.Fatalf("detail id = %q, want %q", detail.ID, seed.SessionID)
	}
	if len(detail.Requests) != 1 || len(detail.ModelCalls) != 1 || len(detail.Events) != 1 || len(detail.Logs) != 1 || len(detail.Agents) != 1 || len(detail.Tools) != 1 {
		t.Fatalf("detail child counts = requests:%d model:%d events:%d logs:%d agents:%d tools:%d, want all 1", len(detail.Requests), len(detail.ModelCalls), len(detail.Events), len(detail.Logs), len(detail.Agents), len(detail.Tools))
	}
	if detail.ModelCalls[0].RequestID != seed.RequestID || detail.ModelCalls[0].RequestModel != "gpt-observe" {
		t.Fatalf("model call = %#v, want request %s model gpt-observe", detail.ModelCalls[0], seed.RequestID)
	}
	if detail.ModelCalls[0].PromptMessages == nil || detail.ModelCalls[0].CompletionMessages == nil {
		t.Fatalf("expected captured prompt and completion payloads, got %#v", detail.ModelCalls[0])
	}
	if detail.Tools[0].ToolName != "search" || detail.Tools[0].CanonicalToolType != "web_search" || detail.Tools[0].ToolRiskLevel != "medium" {
		t.Fatalf("tool normalization = %#v, want search normalized to web_search/medium", detail.Tools[0])
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/gateway/sessions/"+seed.SessionID+"/spans", nil)
	req = withURLParam(req, "id", seed.SessionID)
	testHandler.ListGatewaySessionSpans(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewaySessionSpans status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var spans struct {
		Spans []struct {
			SpanID       string `json:"span_id"`
			ParentSpanID string `json:"parent_span_id"`
			SpanName     string `json:"span_name"`
			SpanKind     string `json:"span_kind"`
			SpanType     string `json:"span_type"`
			Duration     int64  `json:"duration"`
			DurationMS   *int64 `json:"duration_ms"`
		} `json:"spans"`
	}
	if err := json.NewDecoder(w.Body).Decode(&spans); err != nil {
		t.Fatalf("decode spans: %v", err)
	}
	if len(spans.Spans) != 2 {
		t.Fatalf("spans len = %d, want 2", len(spans.Spans))
	}
	if spans.Spans[0].SpanID != "root-detail" || spans.Spans[1].ParentSpanID != "root-detail" {
		t.Fatalf("span hierarchy = %#v, want child under root-detail", spans.Spans)
	}
	if spans.Spans[1].SpanType != "llm" || spans.Spans[1].Duration != 500_000_000 {
		t.Fatalf("child span type/duration = %s/%d, want llm/500000000", spans.Spans[1].SpanType, spans.Spans[1].Duration)
	}
}

func TestGatewayObservabilityLLMCallsFiltersAndErrors(t *testing.T) {
	seed := seedGatewayObservabilityTelemetry(t, "llm")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/gateway/llm-calls?since=24h&backend=openrouter&model=gpt-observe", nil)
	testHandler.ListGatewayLLMCalls(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListGatewayLLMCalls status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var calls struct {
		Calls []struct {
			SessionID      string         `json:"session_id"`
			RequestID      string         `json:"request_id"`
			RequestModel   string         `json:"request_model"`
			ResponseModel  string         `json:"response_model"`
			ProviderSlug   string         `json:"provider_slug"`
			TotalTokens    int64          `json:"total_tokens"`
			PromptMessages map[string]any `json:"prompt_messages"`
		} `json:"calls"`
		Total int64 `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&calls); err != nil {
		t.Fatalf("decode calls: %v", err)
	}
	if calls.Total != 1 || len(calls.Calls) != 1 {
		t.Fatalf("calls total/len = %d/%d, want 1/1", calls.Total, len(calls.Calls))
	}
	if calls.Calls[0].SessionID != seed.SessionID || calls.Calls[0].RequestID != seed.RequestID {
		t.Fatalf("call context = %#v, want session/request %s/%s", calls.Calls[0], seed.SessionID, seed.RequestID)
	}
	if calls.Calls[0].ProviderSlug != "openrouter" || calls.Calls[0].ResponseModel != "gpt-observe" || calls.Calls[0].TotalTokens != 30 {
		t.Fatalf("call fields = %#v, want openrouter/gpt-observe/30", calls.Calls[0])
	}
	if calls.Calls[0].PromptMessages == nil {
		t.Fatal("expected prompt_messages payload")
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/gateway/overview?since=not-a-window", nil)
	testHandler.GatewayOverview(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("GatewayOverview invalid filter status = %d, want 400: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/gateway/sessions/00000000-0000-0000-0000-000000000000", nil)
	req = withURLParam(req, "id", "00000000-0000-0000-0000-000000000000")
	testHandler.GetGatewaySession(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetGatewaySession missing status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func seedGatewayObservabilityTelemetry(t *testing.T, suffix string) gatewayObservabilitySeed {
	t.Helper()

	traceID := "gateway-observability-" + suffix
	if _, err := testPool.Exec(context.Background(), `DELETE FROM gateway_session WHERE workspace_id = $1 AND trace_id = $2`, testWorkspaceID, traceID); err != nil {
		t.Fatalf("cleanup gateway telemetry: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM gateway_session WHERE workspace_id = $1 AND trace_id = $2`, testWorkspaceID, traceID); err != nil {
			t.Fatalf("cleanup seeded gateway telemetry: %v", err)
		}
	})

	var sessionID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO gateway_session (
			workspace_id, user_id, trace_id, root_span_id, name, client_protocol,
			status, started_at, ended_at, duration_ms, span_count, error_count, total_cost,
			tags, resource_attributes
		)
		VALUES (
			$1, $2, $3, $4, $5, 'openai',
			'success', now() - interval '30 minutes', now() - interval '29 minutes', 1200, 2, 0, 0.0012,
			'["gateway"]'::jsonb, '{"service.name":"multica-gateway"}'::jsonb
		)
		RETURNING id
	`, testWorkspaceID, testUserID, traceID, "root-"+suffix, "Gateway observability "+suffix).Scan(&sessionID); err != nil {
		t.Fatalf("insert gateway session: %v", err)
	}

	var requestID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO gateway_request (
			session_id, workspace_id, user_id, route, method, model_requested, model_forwarded,
			provider_slug, streaming, status, http_status, latency_ms, capture_policy,
			request_metadata, response_metadata, created_at, completed_at
		)
		VALUES (
			$1, $2, $3, '/v1/chat/completions', 'POST', 'gpt-observe', 'gpt-observe',
			'openrouter', true, 'success', 200, 900, 'redacted_content',
			'{"surface":"openai_chat_completions"}'::jsonb, '{"status_code":200}'::jsonb,
			now() - interval '30 minutes', now() - interval '29 minutes'
		)
		RETURNING id
	`, sessionID, testWorkspaceID, testUserID).Scan(&requestID); err != nil {
		t.Fatalf("insert gateway request: %v", err)
	}

	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO gateway_model_call (
			request_id, session_id, workspace_id, provider_slug, request_model, response_model,
			request_type, streaming, prompt_messages, completion_messages,
			prompt_tokens, completion_tokens, total_tokens, usage_source, total_cost,
			response_id, finish_reason, time_to_first_token_ms, time_to_generate_ms,
			streaming_duration_ms, streaming_chunk_count, created_at
		)
		VALUES (
			$1, $2, $3, 'openrouter', 'gpt-observe', 'gpt-observe',
			'chat', true,
			'{"messages":[{"role":"user","content":"hello"}]}'::jsonb,
			'{"choices":[{"message":{"role":"assistant","content":"hi"}}]}'::jsonb,
			10, 20, 30, 'upstream', 0.0012,
			'chatcmpl-observe', 'stop', 120, 900, 900, 2, now() - interval '29 minutes'
		)
	`, requestID, sessionID, testWorkspaceID); err != nil {
		t.Fatalf("insert gateway model call: %v", err)
	}

	var spanID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO gateway_span (
			session_id, request_id, workspace_id, trace_id, span_id, parent_span_id,
			span_kind, name, service_name, status_code, started_at, ended_at, duration_ms,
			attributes, resource_attributes
		)
		VALUES (
			$1, NULL, $2, $3, $4, NULL,
			'session', 'Gateway session', 'multica-gateway', 'ok',
			now() - interval '30 minutes', now() - interval '29 minutes', 1000,
			'{"span.type":"session"}'::jsonb, '{"service.name":"multica-gateway"}'::jsonb
		)
		RETURNING id
	`, sessionID, testWorkspaceID, traceID, "root-"+suffix).Scan(&spanID); err != nil {
		t.Fatalf("insert root span: %v", err)
	}

	var childSpanID string
	if err := testPool.QueryRow(t.Context(), `
		INSERT INTO gateway_span (
			session_id, request_id, workspace_id, trace_id, span_id, parent_span_id,
			span_kind, name, service_name, status_code, started_at, ended_at, duration_ms,
			attributes, resource_attributes
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			'llm', 'OpenAI chat completion', 'multica-gateway', 'ok',
			now() - interval '29 minutes 50 seconds', now() - interval '29 minutes 49 seconds', 500,
			'{"gen_ai.request.model":"gpt-observe"}'::jsonb, '{"service.name":"multica-gateway"}'::jsonb
		)
		RETURNING id
	`, sessionID, requestID, testWorkspaceID, traceID, "llm-"+suffix, "root-"+suffix).Scan(&childSpanID); err != nil {
		t.Fatalf("insert child span: %v", err)
	}

	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO gateway_event (workspace_id, session_id, request_id, span_id, event_type, payload, occurred_at)
		VALUES ($1, $2, $3, $4, 'llm_call', '{"model":"gpt-observe"}'::jsonb, now() - interval '29 minutes')
	`, testWorkspaceID, sessionID, requestID, childSpanID); err != nil {
		t.Fatalf("insert gateway event: %v", err)
	}

	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO gateway_log (workspace_id, session_id, request_id, span_id, severity, body, attributes, occurred_at)
		VALUES ($1, $2, $3, $4, 'info', 'gateway request completed', '{"status":"success"}'::jsonb, now() - interval '29 minutes')
	`, testWorkspaceID, sessionID, requestID, childSpanID); err != nil {
		t.Fatalf("insert gateway log: %v", err)
	}

	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO gateway_agent_observation (
			workspace_id, session_id, span_row_id, agent_id, agent_name, role,
			models, tools, reasoning_summary
		)
		VALUES ($1, $2, $3, 'agent-observe', 'Observer Agent', 'assistant', '["gpt-observe"]'::jsonb, '["search"]'::jsonb, 'handled request')
	`, testWorkspaceID, sessionID, spanID); err != nil {
		t.Fatalf("insert gateway agent observation: %v", err)
	}

	if _, err := testPool.Exec(t.Context(), `
		INSERT INTO gateway_tool_observation (
			workspace_id, session_id, span_row_id, tool_id, tool_name,
			description, parameters, result, status, duration_ms
		)
		VALUES ($1, $2, $3, 'tool-search', 'search', 'Search tool', '{"q":"hello"}'::jsonb, '{"ok":true}'::jsonb, 'success', 42)
	`, testWorkspaceID, sessionID, spanID); err != nil {
		t.Fatalf("insert gateway tool observation: %v", err)
	}

	return gatewayObservabilitySeed{SessionID: sessionID, RequestID: requestID, SpanID: spanID}
}
