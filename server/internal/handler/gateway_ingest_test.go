package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayIngestRequiresGatewayKey(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/v1/traces", map[string]any{
		"trace_id": "trace-missing-key",
		"spans": []map[string]any{{
			"span_id": "root",
			"name":    "Missing key workflow",
			"kind":    "workflow",
		}},
	})

	testHandler.PostGatewayTraceIngest(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("PostGatewayTraceIngest: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGatewayIngestPersistsTraceTelemetry(t *testing.T) {
	setGatewaySecret(t)
	gatewayKey := createGatewayIngestTestKey(t)
	traceID := "trace-ingest-handler"

	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM gateway_session WHERE workspace_id = $1 AND trace_id = $2`, testWorkspaceID, traceID); err != nil {
			t.Fatalf("cleanup gateway ingest trace: %v", err)
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", requestBody(t, map[string]any{
		"trace_id":         traceID,
		"name":             "Checkout agent run",
		"service_name":     "checkout-api",
		"client_tool_hint": "multica-ts-sdk",
		"status":           "success",
		"resource_attributes": map[string]any{
			"deployment.environment": "staging",
			"multica.app_id":         "checkout",
		},
		"spans": []map[string]any{
			{
				"span_id":     "root",
				"name":        "Checkout workflow",
				"kind":        "workflow",
				"status_code": "ok",
				"attributes": map[string]any{
					"order_id": "ord_123",
				},
			},
			{
				"span_id":        "tool-search",
				"parent_span_id": "root",
				"name":           "Search inventory",
				"kind":           "tool",
				"status_code":    "ok",
			},
		},
		"events": []map[string]any{{
			"span_id":    "tool-search",
			"event_type": "tool.completed",
			"payload": map[string]any{
				"items": 2,
			},
		}},
		"logs": []map[string]any{{
			"span_id":  "tool-search",
			"severity": "info",
			"body":     "inventory search completed",
		}},
		"agents": []map[string]any{{
			"span_id":    "root",
			"agent_id":   "agent-checkout",
			"agent_name": "Checkout Agent",
			"role":       "customer-support",
			"models":     []string{"gpt-4.1"},
			"tools":      []string{"inventory.search"},
		}},
		"tools": []map[string]any{{
			"span_id":     "tool-search",
			"tool_id":     "inventory.search",
			"tool_name":   "inventory.search",
			"description": "Search inventory availability",
			"parameters": map[string]any{
				"sku": "sku_123",
			},
			"result": map[string]any{
				"available": true,
			},
			"status":      "success",
			"duration_ms": 42,
		}},
	}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+gatewayKey)

	testHandler.PostGatewayTraceIngest(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("PostGatewayTraceIngest: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		TraceID    string `json:"trace_id"`
		SessionID  string `json:"session_id"`
		SpanCount  int    `json:"span_count"`
		EventCount int    `json:"event_count"`
		LogCount   int    `json:"log_count"`
		AgentCount int    `json:"agent_count"`
		ToolCount  int    `json:"tool_count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	if resp.TraceID != traceID || resp.SessionID == "" {
		t.Fatalf("unexpected ingest response: %+v", resp)
	}
	if resp.SpanCount != 2 || resp.EventCount != 1 || resp.LogCount != 1 || resp.AgentCount != 1 || resp.ToolCount != 1 {
		t.Fatalf("unexpected ingest counts: %+v", resp)
	}

	var persisted struct {
		SpanCount  int
		EventCount int
		LogCount   int
		AgentCount int
		ToolCount  int
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM gateway_span WHERE workspace_id = $1 AND trace_id = $2)::int,
			(SELECT count(*) FROM gateway_event e JOIN gateway_session s ON s.id = e.session_id WHERE e.workspace_id = $1 AND s.trace_id = $2)::int,
			(SELECT count(*) FROM gateway_log l JOIN gateway_session s ON s.id = l.session_id WHERE l.workspace_id = $1 AND s.trace_id = $2)::int,
			(SELECT count(*) FROM gateway_agent_observation a JOIN gateway_session s ON s.id = a.session_id WHERE a.workspace_id = $1 AND s.trace_id = $2)::int,
			(SELECT count(*) FROM gateway_tool_observation t JOIN gateway_session s ON s.id = t.session_id WHERE t.workspace_id = $1 AND s.trace_id = $2)::int
	`, testWorkspaceID, traceID).Scan(
		&persisted.SpanCount,
		&persisted.EventCount,
		&persisted.LogCount,
		&persisted.AgentCount,
		&persisted.ToolCount,
	); err != nil {
		t.Fatalf("query persisted ingest telemetry: %v", err)
	}
	if persisted.SpanCount != 2 || persisted.EventCount != 1 || persisted.LogCount != 1 || persisted.AgentCount != 1 || persisted.ToolCount != 1 {
		t.Fatalf("unexpected persisted counts: %+v", persisted)
	}
}

func createGatewayIngestTestKey(t *testing.T) string {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/gateway/key", nil)
	testHandler.CreateGatewayUserKey(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CreateGatewayUserKey: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode gateway key response: %v", err)
	}
	if resp.Key == "" {
		t.Fatal("gateway key is empty")
	}
	return resp.Key
}

func requestBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}
