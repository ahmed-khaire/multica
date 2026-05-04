import { createTraceContext, defaultIdGenerator, multicaHeaderNames } from "./context";
import type {
  AgentOptions,
  Clock,
  EventOptions,
  FetchLike,
  IdGenerator,
  InjectHeadersOptions,
  LogOptions,
  ObserverClientOptions,
  RecordedSpan,
  SpanOptions,
  StartTraceOptions,
  ToolOptions,
  TraceAgentPayload,
  TraceEventPayload,
  TraceFlushResult,
  TraceIngestPayload,
  TraceIngestResponse,
  TraceLogPayload,
  TraceSpanPayload,
  TraceStatus,
  TraceToolPayload,
} from "./types";

interface ClientDefaults {
  serviceName: string;
  appId?: string;
  environment?: string;
  deploymentId?: string;
  aiSystemId?: string;
  idGenerator: IdGenerator;
  now: Clock;
}

export class ObserverTrace {
  readonly traceId: string;
  readonly sessionId: string;
  readonly rootSpanId: string;

  private readonly defaults: ClientDefaults;
  private readonly options: StartTraceOptions;
  private readonly spans: TraceSpanPayload[] = [];
  private readonly events: TraceEventPayload[] = [];
  private readonly logs: TraceLogPayload[] = [];
  private readonly agents: TraceAgentPayload[] = [];
  private readonly tools: TraceToolPayload[] = [];

  constructor(defaults: ClientDefaults, options: StartTraceOptions) {
    const generated = createTraceContext(defaults.idGenerator);
    this.traceId = options.traceId ?? generated.traceId;
    this.sessionId = options.sessionId ?? generated.sessionId;
    this.rootSpanId = options.rootSpanId ?? generated.rootSpanId;
    this.defaults = defaults;
    this.options = options;

    this.spans.push({
      span_id: this.rootSpanId,
      kind: "workflow",
      name: options.name,
      service_name: defaults.serviceName,
      status_code: statusToSpanCode(options.status ?? "success"),
      started_at: defaults.now().toISOString(),
      resource_attributes: this.resourceAttributes(),
    });
  }

  span(options: SpanOptions): RecordedSpan {
    const span: TraceSpanPayload = compact({
      span_id: options.spanId ?? this.defaults.idGenerator(),
      parent_span_id: options.parentSpanId,
      kind: options.kind ?? "operation",
      name: options.name,
      service_name: options.serviceName ?? this.defaults.serviceName,
      status_code: options.statusCode ?? "unset",
      status_message: options.statusMessage,
      started_at: (options.startedAt ?? this.defaults.now()).toISOString(),
      ended_at: options.endedAt?.toISOString(),
      duration_ms: options.durationMs,
      attributes: options.attributes,
      resource_attributes: options.resourceAttributes,
    });
    this.spans.push(span);
    return { ...span, spanId: span.span_id };
  }

  event(options: EventOptions): TraceEventPayload {
    const event: TraceEventPayload = compact({
      span_id: options.spanId,
      event_type: options.eventType,
      payload: options.payload,
      occurred_at: (options.occurredAt ?? this.defaults.now()).toISOString(),
    });
    this.events.push(event);
    return event;
  }

  log(options: LogOptions): TraceLogPayload {
    const log: TraceLogPayload = compact({
      span_id: options.spanId,
      severity: options.severity ?? "info",
      body: options.body,
      attributes: options.attributes,
      occurred_at: (options.occurredAt ?? this.defaults.now()).toISOString(),
    });
    this.logs.push(log);
    return log;
  }

  agent(options: AgentOptions): TraceAgentPayload {
    const agent: TraceAgentPayload = compact({
      span_id: options.spanId,
      agent_id: options.agentId,
      agent_name: options.agentName,
      role: options.role,
      models: options.models,
      tools: options.tools,
      handoff_source: options.handoffSource,
      handoff_destination: options.handoffDestination,
      reasoning_summary: options.reasoningSummary,
    });
    this.agents.push(agent);
    return agent;
  }

  tool(options: ToolOptions): TraceToolPayload {
    const tool: TraceToolPayload = compact({
      span_id: options.spanId,
      tool_id: options.toolId,
      tool_name: options.toolName,
      description: options.description,
      parameters: options.parameters,
      result: options.result,
      status: options.status ?? "unknown",
      duration_ms: options.durationMs,
    });
    this.tools.push(tool);
    return tool;
  }

  toPayload(): TraceIngestPayload {
    return compact({
      trace_id: this.traceId,
      root_span_id: this.rootSpanId,
      name: this.options.name,
      service_name: this.defaults.serviceName,
      client_tool_hint: this.options.toolHint ?? "sdk",
      status: this.options.status ?? "success",
      tags: this.options.tags,
      resource_attributes: this.resourceAttributes(),
      spans: this.spans,
      events: this.events,
      logs: this.logs,
      agents: this.agents,
      tools: this.tools,
    });
  }

  contextAttributes(): Record<string, string> {
    return compactStrings({
      appId: this.defaults.appId,
      serviceName: this.defaults.serviceName,
      environment: this.defaults.environment,
      deploymentId: this.defaults.deploymentId,
      aiSystemId: this.defaults.aiSystemId,
      agentId: this.options.agentId,
      workflowId: this.options.workflowId,
      endUserId: this.options.endUserId,
      useCase: this.options.useCase,
      dataDomains: this.options.dataDomains?.join(","),
      toolHint: this.options.toolHint,
    });
  }

  private resourceAttributes(): Record<string, unknown> {
    return compact({
      "multica.app_id": this.defaults.appId,
      "service.name": this.defaults.serviceName,
      "deployment.environment": this.defaults.environment,
      "deployment.id": this.defaults.deploymentId,
      "multica.ai_system_id": this.defaults.aiSystemId,
      "multica.agent_id": this.options.agentId,
      "multica.workflow_id": this.options.workflowId,
      "multica.end_user_id": this.options.endUserId,
      "multica.use_case": this.options.useCase,
      "multica.data_domains": this.options.dataDomains,
      ...this.options.resourceAttributes,
    });
  }
}

export class ObserverClient {
  private readonly gatewayBaseUrl: string;
  private readonly gatewayKey: string;
  private readonly fetchImpl: FetchLike;
  private readonly defaults: ClientDefaults;

  constructor(options: ObserverClientOptions) {
    this.gatewayBaseUrl = options.gatewayBaseUrl.replace(/\/+$/, "");
    this.gatewayKey = options.gatewayKey;
    this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.defaults = {
      serviceName: options.serviceName,
      appId: options.appId,
      environment: options.environment,
      deploymentId: options.deploymentId,
      aiSystemId: options.aiSystemId,
      idGenerator: options.idGenerator ?? defaultIdGenerator,
      now: options.now ?? (() => new Date()),
    };
  }

  startTrace(options: StartTraceOptions): ObserverTrace {
    return new ObserverTrace(this.defaults, options);
  }

  injectHeaders(trace: ObserverTrace, options: InjectHeadersOptions = {}): Record<string, string> {
    const attrs = trace.contextAttributes();
    return compactStrings({
      ...options.headers,
      [multicaHeaderNames.traceId]: trace.traceId,
      [multicaHeaderNames.sessionId]: trace.sessionId,
      [multicaHeaderNames.parentSpanId]: options.parentSpanId ?? trace.rootSpanId,
      [multicaHeaderNames.appId]: attrs.appId,
      [multicaHeaderNames.serviceName]: attrs.serviceName,
      [multicaHeaderNames.environment]: attrs.environment,
      [multicaHeaderNames.deploymentId]: attrs.deploymentId,
      [multicaHeaderNames.aiSystemId]: attrs.aiSystemId,
      [multicaHeaderNames.agentId]: attrs.agentId,
      [multicaHeaderNames.workflowId]: attrs.workflowId,
      [multicaHeaderNames.endUserId]: attrs.endUserId,
      [multicaHeaderNames.useCase]: attrs.useCase,
      [multicaHeaderNames.dataDomains]: attrs.dataDomains,
      [multicaHeaderNames.toolHint]: attrs.toolHint,
    });
  }

  async flush(trace: ObserverTrace): Promise<TraceFlushResult> {
    const response = await this.fetchImpl(`${this.gatewayBaseUrl}/v1/traces`, {
      method: "POST",
      headers: {
        "Authorization": `Bearer ${this.gatewayKey}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(trace.toPayload()),
    });

    if (!response.ok) {
      throw new Error(await parseError(response));
    }

    const data = await response.json() as TraceIngestResponse;
    return {
      traceId: data.trace_id,
      sessionId: data.session_id,
      spanCount: data.span_count,
      eventCount: data.event_count,
      logCount: data.log_count,
      agentCount: data.agent_count,
      toolCount: data.tool_count,
    };
  }
}

function statusToSpanCode(status: TraceStatus | undefined): "ok" | "error" | "unset" {
  if (status === "success") return "ok";
  if (status === "error" || status === "cancelled") return "error";
  return "unset";
}

function compact<T extends Record<string, unknown>>(value: T): T {
  return Object.fromEntries(
    Object.entries(value).filter(([, item]) => item !== undefined && item !== ""),
  ) as T;
}

function compactStrings(value: Record<string, string | undefined>): Record<string, string> {
  return Object.fromEntries(
    Object.entries(value).filter(([, item]) => item !== undefined && item !== ""),
  ) as Record<string, string>;
}

async function parseError(response: Response): Promise<string> {
  try {
    const data = await response.json() as { error?: string };
    if (data.error) return data.error;
  } catch {
    // Ignore malformed error bodies.
  }
  return `Observer ingest failed: ${response.status} ${response.statusText}`;
}
