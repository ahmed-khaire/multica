import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkspaceIdProvider } from "@multica/core/hooks";
import type {
  GatewayLLMCallListResponse,
  GatewayOverviewResponse,
  GatewaySessionDetail,
  GatewaySessionListResponse,
  GatewaySessionSpansResponse,
} from "@multica/core/types";

const { mockApi } = vi.hoisted(() => ({
  mockApi: {
    getGatewayOverview: vi.fn(),
    listGatewaySessions: vi.fn(),
    getGatewaySession: vi.fn(),
    getGatewaySessionSpans: vi.fn(),
    listGatewayLLMCalls: vi.fn(),
  },
}));

vi.mock("@multica/core/api", () => ({ api: mockApi }));

import { GatewayPage } from "./gateway-page";

const overview: GatewayOverviewResponse = {
  since: "2026-05-03T00:00:00Z",
  until: "2026-05-04T00:00:00Z",
  bucket_width: "hour",
  summary: {
    session_count: 2,
    request_count: 3,
    llm_call_count: 3,
    prompt_tokens: 120,
    completion_tokens: 80,
    total_tokens: 200,
    total_cost: 0.42,
    error_count: 1,
    streaming_request_count: 2,
    avg_latency_ms: 840,
  },
  time_series: [
    { bucket_start: "2026-05-04T10:00:00Z", request_count: 2, error_count: 0, total_tokens: 140, total_cost: 0.3 },
    { bucket_start: "2026-05-04T11:00:00Z", request_count: 1, error_count: 1, total_tokens: 60, total_cost: 0.12 },
  ],
  top_models: [{ model: "gpt-observe", call_count: 3, total_tokens: 200, total_cost: 0.42 }],
  top_backends: [{ backend: "openrouter", call_count: 3, error_count: 1, avg_latency_ms: 840, total_tokens: 200, total_cost: 0.42 }],
};

const sessions: GatewaySessionListResponse = {
  total: 1,
  limit: 50,
  since: "2026-05-03T00:00:00Z",
  sessions: [
    {
      id: "session-1",
      trace_id: "trace-1",
      root_span_id: "root-1",
      name: "Gateway openai request",
      client_protocol: "openai",
      client_tool_hint: "claude-code",
      service_name: "multica-gateway",
      tags: [],
      status: "success",
      started_at: "2026-05-04T10:00:00Z",
      ended_at: "2026-05-04T10:01:00Z",
      duration_ms: 60000,
      span_count: 2,
      error_count: 0,
      total_cost: 0.42,
      resource_attributes: {},
      request_count: 2,
      llm_call_count: 2,
      prompt_tokens: 120,
      completion_tokens: 80,
      total_tokens: 200,
      usage_cost: 0.42,
      request_error_count: 0,
      streaming_request_count: 2,
      avg_latency_ms: 840,
      models: ["gpt-observe"],
      backends: ["openrouter"],
    },
  ],
};

const sessionListItem = sessions.sessions[0]!;

const sessionDetail: GatewaySessionDetail = {
  ...sessionListItem,
  requests: [],
  model_calls: [
    {
      id: "call-1",
      request_id: "request-1",
      session_id: "session-1",
      backend_id: "",
      provider_slug: "openrouter",
      request_model: "gpt-observe",
      response_model: "gpt-observe",
      request_type: "chat",
      streaming: true,
      prompt_messages: { messages: [{ role: "user", content: "hello" }] },
      completion_messages: { choices: [{ message: { role: "assistant", content: "hi" } }] },
      completion_chunks: null,
      prompt_tokens: 120,
      completion_tokens: 80,
      total_tokens: 200,
      cache_creation_input_tokens: 0,
      cache_read_input_tokens: 0,
      reasoning_tokens: 0,
      streaming_tokens: 0,
      usage_source: "upstream",
      prompt_cost: null,
      completion_cost: null,
      total_cost: 0.42,
      response_id: "chatcmpl-1",
      finish_reason: "stop",
      stop_reason: "",
      time_to_first_token_ms: 120,
      time_to_generate_ms: 840,
      streaming_duration_ms: 840,
      streaming_chunk_count: 4,
      created_at: "2026-05-04T10:00:10Z",
    },
  ],
  events: [{ id: "event-1", session_id: "session-1", request_id: "request-1", span_id: "span-row-1", event_type: "llm_call", payload: {}, occurred_at: "2026-05-04T10:00:10Z" }],
  logs: [{ id: "log-1", session_id: "session-1", request_id: "request-1", span_id: "span-row-1", severity: "info", body: "gateway request completed", attributes: {}, occurred_at: "2026-05-04T10:00:11Z" }],
  agents: [{ id: "agent-obs-1", session_id: "session-1", span_row_id: "span-row-1", agent_id: "agent-1", agent_name: "Observer Agent", role: "assistant", models: ["gpt-observe"], tools: ["search"], handoff_source: "", handoff_destination: "", reasoning_summary: "handled request", created_at: "2026-05-04T10:00:12Z" }],
  tools: [{ id: "tool-obs-1", session_id: "session-1", span_row_id: "span-row-1", tool_id: "tool-1", tool_name: "search", description: "Search tool", parameters: { q: "hello" }, result: { ok: true }, status: "success", duration_ms: 42, created_at: "2026-05-04T10:00:13Z" }],
};

const spans: GatewaySessionSpansResponse = {
  session_id: "session-1",
  trace_id: "trace-1",
  spans: [
    { id: "span-row-1", session_id: "session-1", request_id: "", trace_id: "trace-1", span_id: "root-1", parent_span_id: "", name: "Gateway session", span_name: "Gateway session", span_kind: "session", span_type: "session", service_name: "multica-gateway", start_time: "2026-05-04T10:00:00Z", end_time: "2026-05-04T10:01:00Z", duration: 60000000000, duration_ms: 60000, status_code: "ok", status_message: "", attributes: {}, resource_attributes: {} },
    { id: "span-row-2", session_id: "session-1", request_id: "request-1", trace_id: "trace-1", span_id: "llm-1", parent_span_id: "root-1", name: "OpenAI chat completion", span_name: "OpenAI chat completion", span_kind: "llm", span_type: "llm", service_name: "multica-gateway", start_time: "2026-05-04T10:00:10Z", end_time: "2026-05-04T10:00:11Z", duration: 840000000, duration_ms: 840, status_code: "ok", status_message: "", attributes: {}, resource_attributes: {} },
  ],
};

const llmCalls: GatewayLLMCallListResponse = {
  total: 1,
  limit: 50,
  since: "2026-05-03T00:00:00Z",
  calls: [
    {
      ...sessionDetail.model_calls[0]!,
      trace_id: "trace-1",
      session_name: "Gateway openai request",
      route: "/v1/chat/completions",
      request_status: "success",
      http_status: 200,
      latency_ms: 840,
      capture_policy: "redacted_content",
    },
  ],
};

function renderGatewayPage() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={qc}>
      <WorkspaceIdProvider wsId="ws-1">
        <GatewayPage />
      </WorkspaceIdProvider>
    </QueryClientProvider>,
  );
}

describe("GatewayPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getGatewayOverview.mockResolvedValue(overview);
    mockApi.listGatewaySessions.mockResolvedValue(sessions);
    mockApi.getGatewaySession.mockResolvedValue(sessionDetail);
    mockApi.getGatewaySessionSpans.mockResolvedValue(spans);
    mockApi.listGatewayLLMCalls.mockResolvedValue(llmCalls);
  });

  it("renders overview metrics and breakdowns", async () => {
    renderGatewayPage();

    expect(screen.getByText("Gateway")).toBeInTheDocument();
    expect(await screen.findByText("2 sessions")).toBeInTheDocument();
    expect(screen.getByText("3 requests")).toBeInTheDocument();
    expect(screen.getAllByText("200 tokens").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("$0.4200").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("gpt-observe").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("openrouter").length).toBeGreaterThanOrEqual(1);
  });

  it("renders session list and drilldown", async () => {
    renderGatewayPage();

    const row = await screen.findByRole("button", { name: /Gateway openai request/ });
    expect(row).toBeInTheDocument();
    expect(await screen.findByText("Session Drilldown")).toBeInTheDocument();
    expect(screen.getByText("OpenAI chat completion")).toBeInTheDocument();
    expect(screen.getByText("gateway request completed")).toBeInTheDocument();
    expect(screen.getByText("Observer Agent")).toBeInTheDocument();
    expect(screen.getByText("search")).toBeInTheDocument();
  });

  it("renders LLM call table on tab switch", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /LLM Calls/ }));
    const table = await screen.findByRole("table", { name: /LLM calls/ });
    expect(within(table).getByText("/v1/chat/completions")).toBeInTheDocument();
    expect(within(table).getByText("redacted_content")).toBeInTheDocument();
  });
});
