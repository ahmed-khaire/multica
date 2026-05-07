package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

const gatewayMCPProtocolVersion = "2024-11-05"

type gatewayMCPTool struct {
	Name        string                               `json:"name"`
	Description string                               `json:"description"`
	InputSchema map[string]any                       `json:"inputSchema"`
	Annotations map[string]any                       `json:"annotations"`
	Path        func(map[string]any) (string, error) `json:"-"`
	Call        gatewayMCPToolHandler                `json:"-"`
}

type gatewayMCPToolHandler func(context.Context, *cli.APIClient, map[string]any) (any, error)

type gatewayMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type gatewayMCPResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *gatewayMCPError `json:"error,omitempty"`
}

type gatewayMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type gatewayMCPToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func runGatewayMCP(cmd *cobra.Command, _ []string) error {
	client, err := gatewayClient(cmd)
	if err != nil {
		return err
	}
	return serveGatewayMCP(context.Background(), cmd.InOrStdin(), cmd.OutOrStdout(), client)
}

func serveGatewayMCP(ctx context.Context, in io.Reader, out io.Writer, client *cli.APIClient) error {
	tools := gatewayMCPTools()
	toolByName := make(map[string]gatewayMCPTool, len(tools))
	for _, tool := range tools {
		toolByName[tool.Name] = tool
	}

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req gatewayMCPRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeGatewayMCPResponse(out, gatewayMCPResponse{
				JSONRPC: "2.0",
				Error:   &gatewayMCPError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}
		if len(req.ID) == 0 {
			continue
		}

		resp := gatewayMCPResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{
				"protocolVersion": gatewayMCPProtocolVersion,
				"serverInfo": map[string]any{
					"name":    "multica-gateway",
					"version": version,
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			}
		case "tools/list":
			resp.Result = map[string]any{"tools": tools}
		case "tools/call":
			result, err := callGatewayMCPTool(ctx, client, toolByName, req.Params)
			if err != nil {
				resp.Error = &gatewayMCPError{Code: -32602, Message: err.Error()}
			} else {
				resp.Result = result
			}
		default:
			resp.Error = &gatewayMCPError{Code: -32601, Message: "method not found: " + req.Method}
		}
		writeGatewayMCPResponse(out, resp)
	}
	return scanner.Err()
}

func callGatewayMCPTool(ctx context.Context, client *cli.APIClient, toolByName map[string]gatewayMCPTool, params json.RawMessage) (map[string]any, error) {
	var call gatewayMCPToolCallParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &call); err != nil {
			return nil, fmt.Errorf("invalid tools/call params: %w", err)
		}
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	tool, ok := toolByName[call.Name]
	if !ok {
		return nil, fmt.Errorf("unknown read-only Gateway MCP tool: %s", call.Name)
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var data any
	if tool.Call != nil {
		var err error
		data, err = tool.Call(callCtx, client, call.Arguments)
		if err != nil {
			return nil, err
		}
	} else {
		path, err := tool.Path(call.Arguments)
		if err != nil {
			return nil, err
		}
		if err := client.GetJSON(callCtx, path, &data); err != nil {
			return nil, err
		}
	}
	data = redactGatewayMCPSecrets(data)
	text, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": string(text),
		}},
		"structuredContent": data,
	}, nil
}

func writeGatewayMCPResponse(out io.Writer, resp gatewayMCPResponse) {
	_ = json.NewEncoder(out).Encode(resp)
}

func gatewayMCPTools() []gatewayMCPTool {
	noArgs := gatewayMCPObjectSchema(nil)
	filterArgs := gatewayMCPObjectSchema(map[string]any{
		"since":   map[string]any{"type": "string", "description": "Lookback window such as 24h, 7d, or an RFC3339 timestamp."},
		"backend": map[string]any{"type": "string", "description": "Backend slug filter."},
		"model":   map[string]any{"type": "string", "description": "Model name filter."},
		"status":  map[string]any{"type": "string", "description": "Request or session status filter."},
		"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum rows to return."},
	})
	limitArgs := gatewayMCPObjectSchema(map[string]any{
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum rows to return."},
	})
	sessionIDArgs := gatewayMCPObjectSchema(map[string]any{
		"session_id": map[string]any{"type": "string", "description": "Gateway session ID to inspect."},
	})
	sessionIDArgs["required"] = []string{"session_id"}

	return []gatewayMCPTool{
		gatewayMCPReadTool("gateway_status", "Show Observer Gateway base URLs, capture policy, backend counts, and user key readiness.", noArgs, staticGatewayMCPPath("/api/gateway/status")),
		gatewayMCPReadTool("gateway_doctor", "Show Observer Gateway diagnostic checks and remediation guidance.", noArgs, staticGatewayMCPPath("/api/gateway/doctor")),
		gatewayMCPReadTool("gateway_health_report", "Show backend, credential, probe, and governance rollout health.", noArgs, staticGatewayMCPPath("/api/gateway/health-report")),
		gatewayMCPReadTool("gateway_backends", "List configured Gateway backends without raw credentials.", noArgs, staticGatewayMCPPath("/api/gateway/backends")),
		gatewayMCPReadTool("gateway_overview", "Read aggregate Gateway observability dashboard metrics.", filterArgs, observabilityGatewayMCPPath("/api/gateway/overview")),
		gatewayMCPReadTool("gateway_sessions", "List observed Gateway sessions and traces.", filterArgs, observabilityGatewayMCPPath("/api/gateway/sessions")),
		gatewayMCPReadTool("gateway_llm_calls", "List observed model calls, token usage, latency, and cost telemetry.", filterArgs, observabilityGatewayMCPPath("/api/gateway/llm-calls")),
		gatewayMCPReadTool("gateway_session", "Get one observed Gateway session with requests, model calls, events, logs, agents, and tools.", sessionIDArgs, sessionGatewayMCPPath("/api/gateway/sessions")),
		gatewayMCPReadTool("gateway_session_spans", "Get the span tree for one observed Gateway session.", sessionIDArgs, sessionGatewayMCPPath("/api/gateway/sessions", "spans")),
		gatewayMCPReadWorkflowTool("gateway_session_drilldown", "Get one observed Gateway session and its span tree in a single read-only bundle.", sessionIDArgs, callGatewayMCPSessionDrilldown),
		gatewayMCPReadTool("gateway_policy_decisions", "List Gateway governance policy decisions waiting for review or audit.", limitArgs, limitedGatewayMCPPath("/api/gateway/governance/policy-decisions")),
		gatewayMCPReadTool("gateway_governance_policies", "List configured Gateway governance policies.", noArgs, staticGatewayMCPPath("/api/gateway/governance/policies")),
		gatewayMCPReadTool("gateway_policy_exceptions", "List Gateway governance policy exceptions.", limitArgs, limitedGatewayMCPPath("/api/gateway/governance/exceptions")),
		gatewayMCPReadTool("gateway_incidents", "List Gateway governance incidents.", limitArgs, limitedGatewayMCPPath("/api/gateway/governance/incidents")),
		gatewayMCPReadTool("gateway_evidence", "List generated compliance evidence for Gateway activity.", limitArgs, limitedGatewayMCPPath("/api/gateway/governance/evidence")),
		gatewayMCPReadTool("gateway_provider_risks", "List third-party AI provider risk records.", noArgs, staticGatewayMCPPath("/api/gateway/governance/provider-risks")),
		gatewayMCPReadTool("gateway_control_mappings", "List governance control mappings and evidence coverage.", noArgs, staticGatewayMCPPath("/api/gateway/governance/control-mappings")),
	}
}

func gatewayMCPReadTool(name string, description string, inputSchema map[string]any, path func(map[string]any) (string, error)) gatewayMCPTool {
	return gatewayMCPTool{
		Name:        name,
		Description: description,
		InputSchema: inputSchema,
		Annotations: map[string]any{
			"readOnlyHint":    true,
			"destructiveHint": false,
			"idempotentHint":  true,
			"openWorldHint":   true,
		},
		Path: path,
	}
}

func gatewayMCPReadWorkflowTool(name string, description string, inputSchema map[string]any, call gatewayMCPToolHandler) gatewayMCPTool {
	tool := gatewayMCPReadTool(name, description, inputSchema, nil)
	tool.Call = call
	return tool
}

func gatewayMCPObjectSchema(properties map[string]any) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
}

func staticGatewayMCPPath(path string) func(map[string]any) (string, error) {
	return func(map[string]any) (string, error) {
		return path, nil
	}
}

func observabilityGatewayMCPPath(path string) func(map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		values := url.Values{}
		for _, key := range []string{"since", "backend", "model", "status"} {
			if value := gatewayMCPStringArg(args, key); value != "" {
				values.Set(key, value)
			}
		}
		if limit, ok, err := gatewayMCPLimitArg(args); err != nil {
			return "", err
		} else if ok {
			values.Set("limit", strconv.Itoa(limit))
		}
		return gatewayMCPPathWithQuery(path, values), nil
	}
}

func sessionGatewayMCPPath(path string, child ...string) func(map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		sessionID := gatewayMCPStringArg(args, "session_id")
		if sessionID == "" {
			return "", fmt.Errorf("session_id is required")
		}
		result := path + "/" + url.PathEscape(sessionID)
		if len(child) > 0 && child[0] != "" {
			result += "/" + url.PathEscape(child[0])
		}
		return result, nil
	}
}

func callGatewayMCPSessionDrilldown(ctx context.Context, client *cli.APIClient, args map[string]any) (any, error) {
	detailPath, err := sessionGatewayMCPPath("/api/gateway/sessions")(args)
	if err != nil {
		return nil, err
	}
	spansPath, err := sessionGatewayMCPPath("/api/gateway/sessions", "spans")(args)
	if err != nil {
		return nil, err
	}

	var detail any
	if err := client.GetJSON(ctx, detailPath, &detail); err != nil {
		return nil, err
	}
	var spans any
	if err := client.GetJSON(ctx, spansPath, &spans); err != nil {
		return nil, err
	}
	return map[string]any{
		"session_detail": detail,
		"session_spans":  spans,
	}, nil
}

func limitedGatewayMCPPath(path string) func(map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		values := url.Values{}
		if limit, ok, err := gatewayMCPLimitArg(args); err != nil {
			return "", err
		} else if ok {
			values.Set("limit", strconv.Itoa(limit))
		}
		return gatewayMCPPathWithQuery(path, values), nil
	}
}

func gatewayMCPPathWithQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func gatewayMCPStringArg(args map[string]any, key string) string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func gatewayMCPLimitArg(args map[string]any) (int, bool, error) {
	raw, ok := args["limit"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	var limit int
	switch value := raw.(type) {
	case float64:
		limit = int(value)
	case int:
		limit = value
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, false, fmt.Errorf("limit must be an integer")
		}
		limit = int(parsed)
	default:
		return 0, false, fmt.Errorf("limit must be an integer")
	}
	if limit < 1 {
		return 0, false, fmt.Errorf("limit must be at least 1")
	}
	if limit > 100 {
		limit = 100
	}
	return limit, true, nil
}

func redactGatewayMCPSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			if gatewayMCPIsSensitiveKey(key) {
				redacted[key] = "[redacted]"
				continue
			}
			redacted[key] = redactGatewayMCPSecrets(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactGatewayMCPSecrets(item)
		}
		return redacted
	default:
		return typed
	}
}

func gatewayMCPIsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "credential_hint", "key_prefix", "has_active_key", "backend_credential_ids", "credential_summary":
		return false
	}
	for _, fragment := range []string{"api_key", "authorization", "bearer", "secret", "credential", "encrypted"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	switch normalized {
	case "key", "token", "access_token", "refresh_token", "id_token", "session_token", "bearer_token":
		return true
	default:
		return false
	}
}
