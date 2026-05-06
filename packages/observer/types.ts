export type IdGenerator = () => string;
export type Clock = () => Date;
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export type SpanKind =
  | "workflow"
  | "session"
  | "task"
  | "operation"
  | "agent"
  | "tool"
  | "llm"
  | "chain"
  | "text"
  | "guardrail"
  | "http"
  | "unknown";

export type SpanStatusCode = "unset" | "ok" | "error";
export type LogSeverity = "trace" | "debug" | "info" | "warn" | "error" | "fatal";
export type ToolStatus = "success" | "error" | "blocked" | "unknown";
export type TraceStatus = "running" | "success" | "error" | "cancelled" | "unknown";

export interface ObserverClientOptions {
  gatewayBaseUrl: string;
  gatewayKey: string;
  serviceName: string;
  appId?: string;
  environment?: string;
  deploymentId?: string;
  aiSystemId?: string;
  idGenerator?: IdGenerator;
  now?: Clock;
  fetch?: FetchLike;
}

export interface StartTraceOptions {
  traceId?: string;
  sessionId?: string;
  rootSpanId?: string;
  name: string;
  status?: TraceStatus;
  agentId?: string;
  workflowId?: string;
  endUserId?: string;
  useCase?: string;
  dataDomains?: string[];
  toolHint?: string;
  tags?: string[];
  resourceAttributes?: Record<string, unknown>;
}

export interface InjectHeadersOptions {
  headers?: Record<string, string>;
  parentSpanId?: string;
}

export interface SpanOptions {
  spanId?: string;
  parentSpanId?: string;
  name: string;
  kind?: SpanKind;
  serviceName?: string;
  statusCode?: SpanStatusCode;
  statusMessage?: string;
  startedAt?: Date;
  endedAt?: Date;
  durationMs?: number;
  attributes?: Record<string, unknown>;
  resourceAttributes?: Record<string, unknown>;
}

export interface RunSpanOptions extends Omit<SpanOptions, "startedAt" | "endedAt" | "durationMs" | "statusCode" | "statusMessage"> {
  statusCode?: SpanStatusCode;
}

export interface EventOptions {
  spanId?: string;
  eventType: string;
  payload?: Record<string, unknown>;
  occurredAt?: Date;
}

export interface LogOptions {
  spanId?: string;
  severity?: LogSeverity;
  body: string;
  attributes?: Record<string, unknown>;
  occurredAt?: Date;
}

export interface AgentOptions {
  spanId?: string;
  agentId?: string;
  agentName?: string;
  role?: string;
  models?: string[];
  tools?: string[];
  handoffSource?: string;
  handoffDestination?: string;
  reasoningSummary?: string;
}

export interface ToolOptions {
  spanId?: string;
  toolId?: string;
  toolName?: string;
  description?: string;
  parameters?: unknown;
  result?: unknown;
  status?: ToolStatus;
  durationMs?: number;
}

export interface RunToolOptions extends Omit<RunSpanOptions, "kind"> {
  toolId?: string;
  toolName: string;
  description?: string;
  parameters?: unknown;
}

export interface RunAgentOptions extends Omit<RunSpanOptions, "kind"> {
  agentId?: string;
  agentName?: string;
  role?: string;
  models?: string[];
  tools?: string[];
}

export interface TraceSpanPayload {
  span_id: string;
  parent_span_id?: string;
  kind: SpanKind;
  name: string;
  service_name: string;
  status_code: SpanStatusCode;
  status_message?: string;
  started_at: string;
  ended_at?: string;
  duration_ms?: number;
  attributes?: Record<string, unknown>;
  resource_attributes?: Record<string, unknown>;
}

export interface RecordedSpan extends TraceSpanPayload {
  spanId: string;
}

export interface TraceEventPayload {
  span_id?: string;
  event_type: string;
  payload?: Record<string, unknown>;
  occurred_at: string;
}

export interface TraceLogPayload {
  span_id?: string;
  severity: LogSeverity;
  body: string;
  attributes?: Record<string, unknown>;
  occurred_at: string;
}

export interface TraceAgentPayload {
  span_id?: string;
  agent_id?: string;
  agent_name?: string;
  role?: string;
  models?: string[];
  tools?: string[];
  handoff_source?: string;
  handoff_destination?: string;
  reasoning_summary?: string;
}

export interface TraceToolPayload {
  span_id?: string;
  tool_id?: string;
  tool_name?: string;
  description?: string;
  parameters?: unknown;
  result?: unknown;
  status: ToolStatus;
  duration_ms?: number;
}

export interface TraceIngestPayload {
  trace_id: string;
  root_span_id: string;
  name: string;
  service_name: string;
  client_tool_hint: string;
  status: TraceStatus;
  tags?: string[];
  resource_attributes?: Record<string, unknown>;
  spans: TraceSpanPayload[];
  events?: TraceEventPayload[];
  logs?: TraceLogPayload[];
  agents?: TraceAgentPayload[];
  tools?: TraceToolPayload[];
}

export interface TraceIngestResponse {
  trace_id: string;
  session_id: string;
  span_count: number;
  event_count: number;
  log_count: number;
  agent_count: number;
  tool_count: number;
}

export interface TraceFlushResult {
  traceId: string;
  sessionId: string;
  spanCount: number;
  eventCount: number;
  logCount: number;
  agentCount: number;
  toolCount: number;
}
