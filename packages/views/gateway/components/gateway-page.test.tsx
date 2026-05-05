import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkspaceIdProvider } from "@multica/core/hooks";
import type {
  GatewayLLMCallListResponse,
  GatewayBackend,
  GatewayAuditLogItem,
  GatewayIngestKeyListItem,
  GatewayIngestKeyResponse,
  GatewayOverviewResponse,
  GatewayPolicyDecisionItem,
  GatewayProviderRisk,
  GatewaySessionDetail,
  GatewaySessionListResponse,
  GatewaySessionSpansResponse,
  GatewayStatusResponse,
  GatewayUserKeyResponse,
} from "@multica/core/types";

const { mockApi, mockAuthState } = vi.hoisted(() => ({
  mockAuthState: {
    user: {
      id: "user-1",
      name: "Admin User",
      email: "admin@example.com",
      avatar_url: null,
      created_at: "2026-05-04T00:00:00Z",
      updated_at: "2026-05-04T00:00:00Z",
    },
  },
  mockApi: {
    listMembers: vi.fn(),
    getGatewayOverview: vi.fn(),
    listGatewaySessions: vi.fn(),
    getGatewaySession: vi.fn(),
    getGatewaySessionSpans: vi.fn(),
    listGatewayLLMCalls: vi.fn(),
    listGatewayIngestKeys: vi.fn(),
    createGatewayIngestKey: vi.fn(),
    revokeGatewayIngestKey: vi.fn(),
    getGatewayStatus: vi.fn(),
    listGatewayBackends: vi.fn(),
    listGatewayAudit: vi.fn(),
    listGatewayProviderRisks: vi.fn(),
    listGatewayPolicyDecisions: vi.fn(),
    createGatewayUserKey: vi.fn(),
    createGatewayBackend: vi.fn(),
    updateGatewayBackend: vi.fn(),
    deleteGatewayBackend: vi.fn(),
    upsertGatewayProviderRisk: vi.fn(),
    setGatewayDefaultBackend: vi.fn(),
    updateGatewayCapturePolicy: vi.fn(),
  },
}));

vi.mock("@multica/core/api", () => ({ api: mockApi }));
vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: typeof mockAuthState) => unknown) => selector(mockAuthState),
}));

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

const ingestKeys: GatewayIngestKeyListItem[] = [
  {
    id: "ingest-key-1",
    key_prefix: "mig_checkout",
    app_id: "checkout",
    display_name: "Checkout API",
    revoked_at: null,
    last_used_at: null,
    created_at: "2026-05-04T10:00:00Z",
  },
];

const createdIngestKey: GatewayIngestKeyResponse = {
  id: "ingest-key-2",
  key: "mig_secret",
  key_prefix: "mig_secret",
  app_id: "billing",
  display_name: "Billing Agent",
  revoked_at: null,
  last_used_at: null,
  created_at: "2026-05-04T11:00:00Z",
  gateway_base_url: "http://localhost:18080",
};

const gatewayBackends: GatewayBackend[] = [
  {
    id: "backend-openrouter",
    slug: "openrouter",
    display_name: "OpenRouter",
    backend_type: "openai_compatible",
    base_url: "https://openrouter.ai/api/v1",
    credential_hint: "sk-or-12...cdef",
    enabled: true,
    is_default: true,
    metadata: {},
    created_at: "2026-05-04T09:00:00Z",
    updated_at: "2026-05-04T09:00:00Z",
  },
  {
    id: "backend-local",
    slug: "local",
    display_name: "Local OpenAI-compatible",
    backend_type: "openai_compatible",
    base_url: "http://127.0.0.1:11434/v1",
    credential_hint: "any...hing",
    enabled: true,
    is_default: false,
    metadata: {},
    created_at: "2026-05-04T09:30:00Z",
    updated_at: "2026-05-04T09:30:00Z",
  },
];

const gatewayStatus: GatewayStatusResponse = {
  openai_base_url: "http://localhost:18080/v1",
  anthropic_base_url: "http://localhost:18080",
  capture_policy: "full_content",
  default_backend: gatewayBackends[0]!,
  backend_count: 2,
  enabled_backend_count: 2,
  has_active_key: true,
};

const gatewayUserKey: GatewayUserKeyResponse = {
  id: "gateway-key-1",
  key: "mgw_secret",
  key_prefix: "mgw_secret",
  openai_base_url: "http://localhost:18080/v1",
  openai_api_key: "mgw_secret",
  anthropic_base_url: "http://localhost:18080",
  anthropic_api_key: "mgw_secret",
  created_at: "2026-05-04T12:00:00Z",
  last_used_at: null,
};

const gatewayAudit: GatewayAuditLogItem[] = [
  {
    id: "audit-1",
    actor_user_id: "user-1",
    actor_name: "Admin User",
    actor_email: "admin@example.com",
    action: "gateway.backend.update",
    target_type: "gateway_backend",
    target_id: "backend-local",
    before_state: { display_name: "Local OpenAI-compatible", enabled: true },
    after_state: { display_name: "Local Router", enabled: false },
    request_id: "",
    created_at: "2026-05-04T12:30:00Z",
  },
];

const gatewayProviderRisks: GatewayProviderRisk[] = [
  {
    id: "risk-openrouter",
    backend_id: "backend-openrouter",
    provider_name: "openrouter",
    owner_user_id: "user-1",
    approved_use_cases: ["internal support"],
    data_categories: ["source_code"],
    regions: ["us"],
    hosting_notes: "Enterprise account",
    contract_status: "approved",
    security_review_status: "approved",
    evidence_links: ["https://security.example/openrouter"],
    limitations: "No regulated workloads",
    prohibited_uses: "No PHI",
    model_list: ["gpt-4o"],
    capability_class: "general_purpose_llm",
    risk_score: 72,
    review_cadence_days: 180,
    last_assessment_at: "2026-04-01T00:00:00Z",
    next_review_at: "2026-10-01T00:00:00Z",
    active_exception_count: 0,
    created_at: "2026-05-04T12:00:00Z",
    updated_at: "2026-05-04T12:00:00Z",
  },
];

const gatewayPolicyDecisions: GatewayPolicyDecisionItem[] = [
  {
    id: "decision-1",
    policy_id: "",
    policy_version: null,
    subject_user_id: "user-1",
    subject_agent_id: "",
    resource_type: "provider",
    resource_id: "backend-openrouter",
    resource_label: "openrouter",
    decision: "block",
    reason_code: "provider_risk_rejected",
    matched_rules: [{ id: "provider_risk_rejected", action: "block" }],
    request_id: "",
    session_id: "",
    span_row_id: "",
    approval_status: "",
    evidence_references: [],
    created_at: "2026-05-04T12:45:00Z",
  },
];

const adminMembers = [
  {
    id: "member-1",
    workspace_id: "ws-1",
    user_id: "user-1",
    role: "admin",
    created_at: "2026-05-04T00:00:00Z",
    name: "Admin User",
    email: "admin@example.com",
    avatar_url: null,
  },
] as const;

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
    mockApi.listMembers.mockResolvedValue(adminMembers);
    mockApi.getGatewayOverview.mockResolvedValue(overview);
    mockApi.listGatewaySessions.mockResolvedValue(sessions);
    mockApi.getGatewaySession.mockResolvedValue(sessionDetail);
    mockApi.getGatewaySessionSpans.mockResolvedValue(spans);
    mockApi.listGatewayLLMCalls.mockResolvedValue(llmCalls);
    mockApi.listGatewayIngestKeys.mockResolvedValue(ingestKeys);
    mockApi.createGatewayIngestKey.mockResolvedValue(createdIngestKey);
    mockApi.revokeGatewayIngestKey.mockResolvedValue({ ...ingestKeys[0], revoked_at: "2026-05-04T12:00:00Z" });
    mockApi.getGatewayStatus.mockResolvedValue(gatewayStatus);
    mockApi.listGatewayBackends.mockResolvedValue(gatewayBackends);
    mockApi.listGatewayAudit.mockResolvedValue(gatewayAudit);
    mockApi.listGatewayProviderRisks.mockResolvedValue(gatewayProviderRisks);
    mockApi.listGatewayPolicyDecisions.mockResolvedValue(gatewayPolicyDecisions);
    mockApi.createGatewayUserKey.mockResolvedValue(gatewayUserKey);
    mockApi.createGatewayBackend.mockResolvedValue({ ...gatewayBackends[1], slug: "groq", display_name: "Groq", base_url: "https://api.groq.com/openai/v1" });
    mockApi.updateGatewayBackend.mockResolvedValue({
      ...gatewayBackends[1],
      display_name: "Local Router",
      base_url: "http://127.0.0.1:11435/v1",
      credential_hint: "new...cret",
      enabled: false,
    });
    mockApi.deleteGatewayBackend.mockResolvedValue({ deleted: true });
    mockApi.upsertGatewayProviderRisk.mockResolvedValue({
      ...gatewayProviderRisks[0],
      approved_use_cases: ["internal support", "code review"],
      data_categories: ["source_code", "customer_data"],
      security_review_status: "approved",
      contract_status: "approved",
      risk_score: 64,
    });
    mockApi.setGatewayDefaultBackend.mockResolvedValue({ capture_policy: "full_content", default_backend: gatewayBackends[1] });
    mockApi.updateGatewayCapturePolicy.mockResolvedValue({ capture_policy: "metadata_only", default_backend: gatewayBackends[0] });
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

  it("creates and displays Observer SDK ingest keys from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Checkout API")).toBeInTheDocument();
    expect(screen.getByText("mig_checkout")).toBeInTheDocument();

    await user.type(screen.getByLabelText("App ID"), "billing");
    await user.type(screen.getByLabelText("Name"), "Billing Agent");
    await user.click(screen.getByRole("button", { name: /Create ingest key/ }));

    await waitFor(() => {
      expect(mockApi.createGatewayIngestKey).toHaveBeenCalledWith({
        app_id: "billing",
        display_name: "Billing Agent",
      });
    });
    expect(await screen.findByText(/MULTICA_OBSERVER_GATEWAY_BASE_URL=http:\/\/localhost:18080/)).toBeInTheDocument();
    expect(screen.getByText(/MULTICA_OBSERVER_KEY=mig_secret/)).toBeInTheDocument();
  });

  it("shows Gateway URLs and creates a user gateway key from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("http://localhost:18080/v1")).toBeInTheDocument();
    expect(screen.getByText("http://localhost:18080")).toBeInTheDocument();
    expect(screen.getByText("full_content")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Generate Gateway key/ }));

    await waitFor(() => {
      expect(mockApi.createGatewayUserKey).toHaveBeenCalledWith();
    });
    expect(await screen.findByText(/OPENAI_API_KEY=mgw_secret/)).toBeInTheDocument();
    expect(screen.getByText(/ANTHROPIC_API_KEY=mgw_secret/)).toBeInTheDocument();
  });

  it("adds Gateway backends, changes the default backend, and updates capture policy", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));
    const backendsTable = await screen.findByRole("table", { name: /Gateway backends/ });
    expect(within(backendsTable).getByText("OpenRouter")).toBeInTheDocument();
    expect(within(backendsTable).getByText("Local OpenAI-compatible")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Provider"), "groq");
    await user.clear(screen.getByLabelText("Backend base URL"));
    await user.type(screen.getByLabelText("Backend base URL"), "https://api.groq.com/openai/v1");
    await user.type(screen.getByLabelText("Backend API key"), "gsk_test");
    await user.click(screen.getByRole("button", { name: /Add backend/ }));

    await waitFor(() => {
      expect(mockApi.createGatewayBackend).toHaveBeenCalledWith({
        provider: "groq",
        base_url: "https://api.groq.com/openai/v1",
        key: "gsk_test",
        set_default: false,
      });
    });

    await user.click(screen.getByRole("button", { name: /Make Local OpenAI-compatible default/ }));
    await waitFor(() => {
      expect(mockApi.setGatewayDefaultBackend).toHaveBeenCalledWith("local");
    });

    await user.click(screen.getByRole("button", { name: "metadata_only" }));
    await waitFor(() => {
      expect(mockApi.updateGatewayCapturePolicy).toHaveBeenCalledWith("metadata_only");
    });
  });

  it("renders Gateway administration as read-only for non-admin members", async () => {
    const user = userEvent.setup();
    mockApi.listMembers.mockResolvedValueOnce([{ ...adminMembers[0], role: "member" }]);

    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText(/Gateway administration requires owner or admin access/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Generate Gateway key/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add backend/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Edit Local OpenAI-compatible/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Create ingest key/ })).not.toBeInTheDocument();
    expect(mockApi.listGatewayAudit).not.toHaveBeenCalled();
    expect(mockApi.listGatewayProviderRisks).not.toHaveBeenCalled();
    expect(mockApi.listGatewayPolicyDecisions).not.toHaveBeenCalled();
  });

  it("shows Gateway audit history for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Gateway Change History")).toBeInTheDocument();
    expect(screen.getByText("gateway.backend.update")).toBeInTheDocument();
    expect(screen.getByText("Admin User")).toBeInTheDocument();
    expect(screen.getByText("backend-local")).toBeInTheDocument();
    expect(mockApi.listGatewayAudit).toHaveBeenCalledWith({ limit: 20, signal: expect.any(AbortSignal) });
  });

  it("shows and updates Gateway provider risk governance for admins", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Provider Risk Register")).toBeInTheDocument();
    const riskTable = await screen.findByRole("table", { name: /Gateway provider risk register/ });
    expect(within(riskTable).getByText("openrouter")).toBeInTheDocument();
    expect(within(riskTable).getAllByText("approved").length).toBeGreaterThanOrEqual(1);
    expect(within(riskTable).getByText("source_code")).toBeInTheDocument();
    expect(within(riskTable).getByText("Risk 72")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Governance provider"), "openrouter");
    await user.clear(screen.getByLabelText("Risk score"));
    await user.type(screen.getByLabelText("Risk score"), "64");
    await user.selectOptions(screen.getByLabelText("Security review"), "approved");
    await user.selectOptions(screen.getByLabelText("Contract status"), "approved");
    await user.clear(screen.getByLabelText("Approved use cases"));
    await user.type(screen.getByLabelText("Approved use cases"), "internal support, code review");
    await user.clear(screen.getByLabelText("Data categories"));
    await user.type(screen.getByLabelText("Data categories"), "source_code, customer_data");
    await user.click(screen.getByRole("button", { name: /Save provider risk/ }));

    await waitFor(() => {
      expect(mockApi.upsertGatewayProviderRisk).toHaveBeenCalledWith({
        provider_name: "openrouter",
        backend_id: "backend-openrouter",
        approved_use_cases: ["internal support", "code review"],
        data_categories: ["source_code", "customer_data"],
        contract_status: "approved",
        security_review_status: "approved",
        risk_score: 64,
      });
    });
  });

  it("shows Gateway policy decisions for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Policy Decisions")).toBeInTheDocument();
    const decisionsTable = await screen.findByRole("table", { name: /Gateway policy decisions/ });
    expect(within(decisionsTable).getByText("block")).toBeInTheDocument();
    expect(within(decisionsTable).getByText("provider_risk_rejected")).toBeInTheDocument();
    expect(within(decisionsTable).getByText("openrouter")).toBeInTheDocument();
    expect(mockApi.listGatewayPolicyDecisions).toHaveBeenCalledWith({ limit: 20, signal: expect.any(AbortSignal) });
  });

  it("edits, rotates, disables, and deletes Gateway backends from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));
    const backendsTable = await screen.findByRole("table", { name: /Gateway backends/ });
    await user.click(within(backendsTable).getByRole("button", { name: /Edit Local OpenAI-compatible/ }));

    await user.clear(screen.getByLabelText("Edit backend name"));
    await user.type(screen.getByLabelText("Edit backend name"), "Local Router");
    await user.clear(screen.getByLabelText("Edit backend base URL"));
    await user.type(screen.getByLabelText("Edit backend base URL"), "http://127.0.0.1:11435/v1");
    await user.type(screen.getByLabelText("Rotate backend API key"), "new-secret");
    await user.click(screen.getByRole("button", { name: /Disable backend/ }));
    await user.click(screen.getByRole("button", { name: /Save backend changes/ }));

    await waitFor(() => {
      expect(mockApi.updateGatewayBackend).toHaveBeenCalledWith("backend-local", {
        display_name: "Local Router",
        base_url: "http://127.0.0.1:11435/v1",
        key: "new-secret",
        enabled: false,
      });
    });

    await user.click(within(backendsTable).getByRole("button", { name: /Delete Local OpenAI-compatible/ }));
    await waitFor(() => {
      expect(mockApi.deleteGatewayBackend).toHaveBeenCalledWith("backend-local");
    });
  });

  it("revokes ingest keys from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));
    await user.click(await screen.findByRole("button", { name: /Revoke Checkout API ingest key/ }));

    await waitFor(() => {
      expect(mockApi.revokeGatewayIngestKey).toHaveBeenCalledWith("ingest-key-1");
    });
  });
});
