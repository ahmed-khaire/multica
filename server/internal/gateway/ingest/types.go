package ingest

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidTracePayload = errors.New("invalid trace payload")

type TraceRequest struct {
	TraceID            string         `json:"trace_id"`
	RootSpanID         string         `json:"root_span_id"`
	Name               string         `json:"name"`
	ServiceName        string         `json:"service_name"`
	ClientToolHint     string         `json:"client_tool_hint"`
	Status             string         `json:"status"`
	StartedAt          *time.Time     `json:"started_at"`
	EndedAt            *time.Time     `json:"ended_at"`
	DurationMS         *int64         `json:"duration_ms"`
	Tags               []string       `json:"tags"`
	ResourceAttributes map[string]any `json:"resource_attributes"`
	Spans              []SpanPayload  `json:"spans"`
	Events             []EventPayload `json:"events"`
	Logs               []LogPayload   `json:"logs"`
	Agents             []AgentPayload `json:"agents"`
	Tools              []ToolPayload  `json:"tools"`
}

type SpanPayload struct {
	SpanID             string         `json:"span_id"`
	ParentSpanID       string         `json:"parent_span_id"`
	Kind               string         `json:"kind"`
	Name               string         `json:"name"`
	ServiceName        string         `json:"service_name"`
	StatusCode         string         `json:"status_code"`
	StatusMessage      string         `json:"status_message"`
	StartedAt          *time.Time     `json:"started_at"`
	EndedAt            *time.Time     `json:"ended_at"`
	DurationMS         *int64         `json:"duration_ms"`
	Attributes         map[string]any `json:"attributes"`
	ResourceAttributes map[string]any `json:"resource_attributes"`
}

type EventPayload struct {
	SpanID     string         `json:"span_id"`
	EventType  string         `json:"event_type"`
	Payload    map[string]any `json:"payload"`
	OccurredAt *time.Time     `json:"occurred_at"`
}

type LogPayload struct {
	SpanID     string         `json:"span_id"`
	Severity   string         `json:"severity"`
	Body       string         `json:"body"`
	Attributes map[string]any `json:"attributes"`
	OccurredAt *time.Time     `json:"occurred_at"`
}

type AgentPayload struct {
	SpanID             string   `json:"span_id"`
	AgentID            string   `json:"agent_id"`
	AgentName          string   `json:"agent_name"`
	Role               string   `json:"role"`
	Models             []string `json:"models"`
	Tools              []string `json:"tools"`
	HandoffSource      string   `json:"handoff_source"`
	HandoffDestination string   `json:"handoff_destination"`
	ReasoningSummary   string   `json:"reasoning_summary"`
}

type ToolPayload struct {
	SpanID      string `json:"span_id"`
	ToolID      string `json:"tool_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
	Result      any    `json:"result"`
	Status      string `json:"status"`
	DurationMS  *int64 `json:"duration_ms"`
}

type TraceResponse struct {
	TraceID    string `json:"trace_id"`
	SessionID  string `json:"session_id"`
	SpanCount  int    `json:"span_count"`
	EventCount int    `json:"event_count"`
	LogCount   int    `json:"log_count"`
	AgentCount int    `json:"agent_count"`
	ToolCount  int    `json:"tool_count"`
}

func ValidateTraceRequest(req TraceRequest) error {
	if strings.TrimSpace(req.TraceID) == "" {
		return fmt.Errorf("%w: trace_id is required", ErrInvalidTracePayload)
	}
	if req.Status != "" && !validSessionStatus(strings.TrimSpace(req.Status)) {
		return fmt.Errorf("%w: status is invalid", ErrInvalidTracePayload)
	}
	if len(req.Spans) == 0 {
		return fmt.Errorf("%w: at least one span is required", ErrInvalidTracePayload)
	}
	for i, span := range req.Spans {
		if strings.TrimSpace(span.SpanID) == "" {
			return fmt.Errorf("%w: spans[%d].span_id is required", ErrInvalidTracePayload, i)
		}
		if strings.TrimSpace(span.Name) == "" {
			return fmt.Errorf("%w: spans[%d].name is required", ErrInvalidTracePayload, i)
		}
		if !validSpanKind(defaultString(span.Kind, "unknown")) {
			return fmt.Errorf("%w: spans[%d].kind is invalid", ErrInvalidTracePayload, i)
		}
		if !validSpanStatus(defaultString(span.StatusCode, "unset")) {
			return fmt.Errorf("%w: spans[%d].status_code is invalid", ErrInvalidTracePayload, i)
		}
	}
	for i, event := range req.Events {
		if strings.TrimSpace(event.EventType) == "" {
			return fmt.Errorf("%w: events[%d].event_type is required", ErrInvalidTracePayload, i)
		}
	}
	for i, log := range req.Logs {
		if strings.TrimSpace(log.Body) == "" {
			return fmt.Errorf("%w: logs[%d].body is required", ErrInvalidTracePayload, i)
		}
		if !validLogSeverity(defaultString(log.Severity, "info")) {
			return fmt.Errorf("%w: logs[%d].severity is invalid", ErrInvalidTracePayload, i)
		}
	}
	for i, tool := range req.Tools {
		if !validToolStatus(defaultString(tool.Status, "unknown")) {
			return fmt.Errorf("%w: tools[%d].status is invalid", ErrInvalidTracePayload, i)
		}
	}
	return nil
}

func validSessionStatus(status string) bool {
	switch status {
	case "running", "success", "error", "cancelled", "unknown":
		return true
	default:
		return false
	}
}

func validSpanKind(kind string) bool {
	switch kind {
	case "workflow", "session", "task", "operation", "agent", "tool", "llm", "chain", "text", "guardrail", "http", "unknown":
		return true
	default:
		return false
	}
}

func validSpanStatus(status string) bool {
	switch status {
	case "unset", "ok", "error":
		return true
	default:
		return false
	}
}

func validLogSeverity(severity string) bool {
	switch severity {
	case "trace", "debug", "info", "warn", "error", "fatal":
		return true
	default:
		return false
	}
}

func validToolStatus(status string) bool {
	switch status {
	case "success", "error", "blocked", "unknown":
		return true
	default:
		return false
	}
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
