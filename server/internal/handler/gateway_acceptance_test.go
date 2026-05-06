package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayAcceptanceSmokeRecordsTrafficAndExportsIt(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayProxyKey(t)
	openAIBackend := "acceptance-openai-proxy-test"
	anthropicBackend := "acceptance-anthropic-proxy-test"
	cleanupGatewayAcceptanceSmoke(t, openAIBackend, anthropicBackend)
	openAIUpstream := newFakeOpenAIUpstream(t, fakeOpenAIOptions{
		ResponseID:       "chatcmpl-acceptance",
		Model:            "gpt-acceptance",
		PromptTokens:     11,
		CompletionTokens: 13,
	})
	defer openAIUpstream.Close()
	anthropicUpstream := newFakeAnthropicUpstream(t, fakeAnthropicOptions{
		ResponseID:    "msg_acceptance",
		Model:         "claude-acceptance",
		InputTokens:   17,
		OutputTokens:  19,
		StreamContent: "anthropic-stream-ok",
	})
	defer anthropicUpstream.Close()

	createGatewayProxyBackend(t, "local", openAIBackend, openAIUpstream.URL+"/v1", "sk-openai-acceptance")
	createGatewayProxyBackendWithDefault(t, "anthropic", anthropicBackend, anthropicUpstream.URL, "sk-anthropic-acceptance", false)
	setGatewayProxyDefaultBackend(t, openAIBackend)

	openAIW := httptest.NewRecorder()
	openAIReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-acceptance","messages":[{"role":"user","content":"hello acceptance"}]}`))
	openAIReq.Header.Set("Content-Type", "application/json")
	openAIReq.Header.Set("Authorization", "Bearer "+gatewayKey)
	testHandler.GatewayOpenAIChatCompletions(openAIW, openAIReq)
	if openAIW.Code != http.StatusOK {
		t.Fatalf("OpenAI-compatible smoke status = %d, want 200: %s", openAIW.Code, openAIW.Body.String())
	}
	if !strings.Contains(openAIW.Body.String(), "chatcmpl-acceptance") {
		t.Fatalf("OpenAI-compatible smoke body = %s", openAIW.Body.String())
	}

	anthropicW := httptest.NewRecorder()
	anthropicReq := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-acceptance","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello stream"}]}`))
	anthropicReq.Header.Set("Content-Type", "application/json")
	anthropicReq.Header.Set("x-api-key", gatewayKey)
	anthropicReq.Header.Set("anthropic-version", "2023-06-01")
	anthropicReq.Header.Set("X-Multica-Backend", anthropicBackend)
	testHandler.GatewayAnthropicMessages(anthropicW, anthropicReq)
	if anthropicW.Code != http.StatusOK {
		t.Fatalf("Anthropic-compatible stream smoke status = %d, want 200: %s", anthropicW.Code, anthropicW.Body.String())
	}
	if !anthropicW.Flushed {
		t.Fatal("expected Anthropic-compatible smoke stream to flush")
	}
	if !strings.Contains(anthropicW.Body.String(), "anthropic-stream-ok") {
		t.Fatalf("Anthropic-compatible smoke stream body = %s", anthropicW.Body.String())
	}

	overviewW := httptest.NewRecorder()
	overviewReq := newRequest(http.MethodGet, "/api/gateway/overview?since=24h&backend="+openAIBackend, nil)
	testHandler.GatewayOverview(overviewW, overviewReq)
	if overviewW.Code != http.StatusOK {
		t.Fatalf("GatewayOverview smoke status = %d, want 200: %s", overviewW.Code, overviewW.Body.String())
	}
	var overview struct {
		Summary struct {
			SessionCount int64 `json:"session_count"`
			RequestCount int64 `json:"request_count"`
			LLMCallCount int64 `json:"llm_call_count"`
			TotalTokens  int64 `json:"total_tokens"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(overviewW.Body).Decode(&overview); err != nil {
		t.Fatalf("decode GatewayOverview smoke response: %v", err)
	}
	if overview.Summary.SessionCount != 1 || overview.Summary.RequestCount != 1 || overview.Summary.LLMCallCount != 1 || overview.Summary.TotalTokens != 24 {
		t.Fatalf("overview smoke summary = %#v, want one OpenAI session/request/call and 24 tokens", overview.Summary)
	}

	streamSessionsW := httptest.NewRecorder()
	streamSessionsReq := newRequest(http.MethodGet, "/api/gateway/sessions?since=24h&backend="+anthropicBackend, nil)
	testHandler.ListGatewaySessions(streamSessionsW, streamSessionsReq)
	if streamSessionsW.Code != http.StatusOK {
		t.Fatalf("ListGatewaySessions smoke status = %d, want 200: %s", streamSessionsW.Code, streamSessionsW.Body.String())
	}
	var streamSessions struct {
		Total    int64 `json:"total"`
		Sessions []struct {
			StreamingRequestCount int64    `json:"streaming_request_count"`
			Models                []string `json:"models"`
			Backends              []string `json:"backends"`
		} `json:"sessions"`
	}
	if err := json.NewDecoder(streamSessionsW.Body).Decode(&streamSessions); err != nil {
		t.Fatalf("decode ListGatewaySessions smoke response: %v", err)
	}
	if streamSessions.Total != 1 || len(streamSessions.Sessions) != 1 || streamSessions.Sessions[0].StreamingRequestCount != 1 {
		t.Fatalf("stream session smoke response = %#v, want one streaming Anthropic session", streamSessions)
	}

	exportW := httptest.NewRecorder()
	exportReq := newRequest(http.MethodGet, "/api/gateway/export?since=24h&backend="+openAIBackend+"&limit=10", nil)
	testHandler.ExportGatewayData(exportW, exportReq)
	if exportW.Code != http.StatusOK {
		t.Fatalf("ExportGatewayData smoke status = %d, want 200: %s", exportW.Code, exportW.Body.String())
	}
	var exported struct {
		Sessions struct {
			Total    int64 `json:"total"`
			Sessions []struct {
				Backends []string `json:"backends"`
			} `json:"sessions"`
		} `json:"sessions"`
		LLMCalls struct {
			Total int64 `json:"total"`
			Calls []struct {
				ProviderSlug string `json:"provider_slug"`
				RequestModel string `json:"request_model"`
				TotalTokens  int64  `json:"total_tokens"`
			} `json:"calls"`
		} `json:"llm_calls"`
	}
	if err := json.NewDecoder(exportW.Body).Decode(&exported); err != nil {
		t.Fatalf("decode ExportGatewayData smoke response: %v", err)
	}
	if exported.Sessions.Total != 1 || exported.LLMCalls.Total != 1 || len(exported.LLMCalls.Calls) != 1 {
		t.Fatalf("export smoke counts = sessions:%d calls:%d/%d, want 1/1", exported.Sessions.Total, exported.LLMCalls.Total, len(exported.LLMCalls.Calls))
	}
	if exported.LLMCalls.Calls[0].ProviderSlug != openAIBackend || exported.LLMCalls.Calls[0].RequestModel != "gpt-acceptance" || exported.LLMCalls.Calls[0].TotalTokens != 24 {
		t.Fatalf("export smoke LLM call = %#v, want OpenAI backend/model/tokens", exported.LLMCalls.Calls[0])
	}

	auditW := httptest.NewRecorder()
	auditReq := newRequest(http.MethodGet, "/api/gateway/audit?limit=5", nil)
	testHandler.ListGatewayAudit(auditW, auditReq)
	if auditW.Code != http.StatusOK {
		t.Fatalf("ListGatewayAudit smoke status = %d, want 200: %s", auditW.Code, auditW.Body.String())
	}
	var auditRows []struct {
		Action     string         `json:"action"`
		TargetType string         `json:"target_type"`
		AfterState map[string]any `json:"after_state"`
	}
	if err := json.NewDecoder(auditW.Body).Decode(&auditRows); err != nil {
		t.Fatalf("decode ListGatewayAudit smoke response: %v", err)
	}
	if len(auditRows) == 0 || auditRows[0].Action != "gateway.export.read" || auditRows[0].TargetType != "gateway_export" {
		t.Fatalf("audit smoke row = %#v, want latest gateway export read", auditRows)
	}
}

func cleanupGatewayAcceptanceSmoke(t *testing.T, backendSlugs ...string) {
	t.Helper()
	cleanup := func() {
		if _, err := testPool.Exec(context.Background(), `
			DELETE FROM gateway_session
			WHERE workspace_id = $1
			  AND EXISTS (
			    SELECT 1
			    FROM gateway_request
			    WHERE gateway_request.session_id = gateway_session.id
			      AND gateway_request.provider_slug = ANY($2::text[])
			  )
		`, testWorkspaceID, backendSlugs); err != nil {
			t.Fatalf("cleanup acceptance gateway sessions: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `
			DELETE FROM ai_audit_log
			WHERE workspace_id = $1
			  AND action = 'gateway.export.read'
			  AND target_type = 'gateway_export'
		`, testWorkspaceID); err != nil {
			t.Fatalf("cleanup acceptance gateway export audit: %v", err)
		}
		if _, err := testPool.Exec(context.Background(), `
			DELETE FROM gateway_backend
			WHERE workspace_id = $1
			  AND slug = ANY($2::text[])
		`, testWorkspaceID, backendSlugs); err != nil {
			t.Fatalf("cleanup acceptance gateway backends: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
}
