import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WorkspaceIdProvider } from "@multica/core/hooks";
import type {
  GatewayLLMCallListResponse,
  GatewayBackend,
  GatewayBackendCredential,
  GatewayExportResponse,
  GatewayHealthReportResponse,
  GatewayAuditLogItem,
  GatewayControlMappingItem,
  GatewayDoctorResponse,
  GatewayEvidenceBundleResponse,
  GatewayEvidenceItem,
  GatewayIncidentItem,
  GatewayIngestKeyListItem,
  GatewayIngestKeyResponse,
  GatewayGovernanceInsightsResponse,
  GatewayOverviewResponse,
  GatewayGovernancePolicyItem,
  GatewayPolicyDecisionItem,
  GatewayPolicyExceptionItem,
  GatewayProviderRisk,
  GatewaySessionDetail,
  GatewaySessionListResponse,
  GatewaySessionSpansResponse,
  GatewayStatusResponse,
  GatewayUserKeyResponse,
} from "@multica/core/types";

vi.setConfig({ testTimeout: 20000 });

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
    approveGatewayPolicyDecision: vi.fn(),
    denyGatewayPolicyDecision: vi.fn(),
    listGatewayGovernancePolicies: vi.fn(),
    createGatewayGovernancePolicy: vi.fn(),
    updateGatewayGovernancePolicy: vi.fn(),
    listGatewayEvidence: vi.fn(),
    listGatewayControlMappings: vi.fn(),
    listGatewayIncidents: vi.fn(),
    listGatewayPolicyExceptions: vi.fn(),
    getGatewayDoctor: vi.fn(),
    getGatewayHealthReport: vi.fn(),
    getGatewayGovernanceInsights: vi.fn(),
    getGatewayEvidenceBundle: vi.fn(),
    createGatewayPolicyException: vi.fn(),
    updateGatewayPolicyException: vi.fn(),
    updateGatewayIncident: vi.fn(),
    createGatewayUserKey: vi.fn(),
    createGatewayBackend: vi.fn(),
    updateGatewayBackend: vi.fn(),
    deleteGatewayBackend: vi.fn(),
    listGatewayBackendCredentials: vi.fn(),
    createGatewayBackendCredential: vi.fn(),
    updateGatewayBackendCredential: vi.fn(),
    exportGatewayData: vi.fn(),
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
  tools: [{ id: "tool-obs-1", session_id: "session-1", span_row_id: "span-row-1", tool_id: "tool-1", tool_name: "search", canonical_tool_type: "web_search", tool_risk_level: "medium", description: "Search tool", parameters: { q: "hello" }, result: { ok: true }, status: "success", duration_ms: 42, created_at: "2026-05-04T10:00:13Z" }],
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

const gatewayBackendCredentials: GatewayBackendCredential[] = [
  {
    id: "credential-openrouter-primary",
    backend_id: "backend-openrouter",
    label: "Primary workspace key",
    credential_hint: "sk-or-12...cdef",
    enabled: true,
    priority: 10,
    last_used_at: "2026-05-04T12:10:00Z",
    last_error_at: null,
    last_error: "",
    rate_limited_until: null,
    rate_limit_remaining: 120,
    rate_limit_reset_at: "2026-05-04T13:00:00Z",
    created_at: "2026-05-04T09:00:00Z",
    updated_at: "2026-05-04T09:00:00Z",
  },
  {
    id: "credential-openrouter-overflow",
    backend_id: "backend-openrouter",
    label: "Overflow account",
    credential_hint: "sk-or-34...ffff",
    enabled: false,
    priority: 20,
    last_used_at: null,
    last_error_at: "2026-05-04T12:30:00Z",
    last_error: "429 rate limit exceeded",
    rate_limited_until: "2026-05-04T13:30:00Z",
    rate_limit_remaining: 0,
    rate_limit_reset_at: "2026-05-04T13:30:00Z",
    created_at: "2026-05-04T09:05:00Z",
    updated_at: "2026-05-04T12:30:00Z",
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

const gatewayDoctor: GatewayDoctorResponse = {
  status: "healthy_with_warnings",
  generated_at: "2026-05-04T13:00:00Z",
  checks: [
    {
      id: "gateway_key",
      category: "workspace",
      status: "pass",
      title: "User Gateway key",
      detail: "This user has an active Gateway key.",
      remediation: "",
    },
    {
      id: "default_backend",
      category: "backend",
      status: "pass",
      title: "Default backend",
      detail: "Default backend exists and is enabled.",
      remediation: "",
    },
    {
      id: "open_incidents",
      category: "governance",
      status: "warning",
      title: "Open incidents",
      detail: "2 Gateway incident(s) need review.",
      remediation: "Review and remediate open Gateway incidents.",
    },
  ],
};

const gatewayHealthReport: GatewayHealthReportResponse = {
  status: "healthy_with_warnings",
  generated_at: "2026-05-04T13:05:00Z",
  openai_base_url: "http://localhost:18080/v1",
  anthropic_base_url: "http://localhost:18080",
  backends: [
    {
      id: "backend-openrouter",
      slug: "openrouter",
      display_name: "OpenRouter",
      backend_type: "openai_compatible",
      base_url: "https://openrouter.ai/api/v1",
      enabled: true,
      is_default: true,
      probe_status: "pass",
      probe_latency_ms: 42,
      model_count: 12,
      last_error: "",
      credential_summary: {
        total: 2,
        enabled: 1,
        disabled: 1,
        rate_limited: 1,
        last_errors: 1,
      },
    },
  ],
  governance: {
    capture_policy: "full_content",
    governance_policy_count: 3,
    enabled_policy_count: 2,
    pending_approval_count: 1,
    provider_risk_warning_count: 0,
    open_incident_count: 2,
    evidence_count: 4,
    control_mapping_count: 2,
  },
  checks: gatewayDoctor.checks,
};

const gatewayGovernanceInsights: GatewayGovernanceInsightsResponse = {
  generated_at: "2026-05-04T13:10:00Z",
  since: "2026-04-04T13:10:00Z",
  risk_overview: {
    open_incident_count: 2,
    high_severity_open_incident_count: 1,
    pending_approval_count: 1,
    blocked_decision_count: 4,
    warn_decision_count: 3,
    high_risk_provider_count: 1,
    provider_review_warning_count: 2,
    control_gap_count: 1,
    active_policy_exception_count: 1,
  },
  behavior_trends: {
    top_models: [
      { model: "gpt-observe", call_count: 14, total_tokens: 2400, total_cost: 1.25 },
      { model: "claude-sonnet", call_count: 6, total_tokens: 900, total_cost: 0.92 },
    ],
    top_backends: [
      { backend: "openrouter", call_count: 14, error_count: 2, avg_latency_ms: 840, total_tokens: 2400, total_cost: 1.25 },
      { backend: "local", call_count: 4, error_count: 0, avg_latency_ms: 110, total_tokens: 600, total_cost: 0 },
    ],
    policy_reason_counts: [
      { reason_code: "provider_risk_rejected", count: 4 },
      { reason_code: "source_code_routes_local", count: 2 },
    ],
    blocked_resources: [
      { resource_type: "provider", resource_id: "backend-openrouter", resource_label: "OpenRouter", count: 4 },
    ],
  },
  compliance_coverage: {
    control_count: 5,
    covered_control_count: 3,
    partial_control_count: 1,
    gap_control_count: 1,
    evidence_count: 7,
    stale_control_count: 2,
    last_evidence_generated_at: "2026-05-04T12:46:00Z",
  },
  action_queue: [
    {
      kind: "incident",
      severity: "high",
      title: "Review open Gateway incident",
      detail: "Gateway blocked provider openrouter: provider_risk_rejected",
      resource_type: "incident",
      resource_id: "incident-1",
      created_at: "2026-05-04T12:48:00Z",
    },
    {
      kind: "policy_decision",
      severity: "medium",
      title: "Policy decision needs review",
      detail: "model gpt-approval requires review: model_requires_approval",
      resource_type: "model",
      resource_id: "decision-approval-1",
      created_at: "2026-05-04T12:50:00Z",
    },
    {
      kind: "policy_exception",
      severity: "medium",
      title: "Policy exception expires soon",
      detail: "Temporary exception for incident response",
      resource_type: "policy_exception",
      resource_id: "exception-1",
      created_at: "2026-05-04T12:49:00Z",
    },
    {
      kind: "provider_risk",
      severity: "high",
      title: "Review provider risk",
      detail: "OpenRouter has rejected governance review.",
      resource_type: "provider",
      resource_id: "backend-openrouter",
      created_at: "2026-05-04T12:40:00Z",
    },
  ],
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
  {
    id: "decision-approval-1",
    policy_id: "policy-1",
    policy_version: 1,
    subject_user_id: "user-1",
    subject_agent_id: "",
    resource_type: "model",
    resource_id: "gpt-approval",
    resource_label: "gpt-approval",
    decision: "require_approval",
    reason_code: "model_requires_approval",
    matched_rules: [{ id: "approval-test", action: "require_approval" }],
    request_id: "",
    session_id: "",
    span_row_id: "",
    approval_status: "requested",
    evidence_references: [],
    created_at: "2026-05-04T12:50:00Z",
  },
];

const gatewayGovernancePolicies: GatewayGovernancePolicyItem[] = [
  {
    id: "policy-1",
    name: "Block test model",
    description: "Stops unapproved model usage",
    policy_type: "model",
    enabled: true,
    version: 1,
    rule_definition: {
      rules: [
        {
          id: "block-gpt-test",
          action: "block",
          reason_code: "model_blocked",
          match: { models: ["gpt-test"] },
        },
      ],
    },
    enforcement_mode: "enforce",
    created_by: "user-1",
    updated_by: "user-1",
    created_at: "2026-05-04T12:40:00Z",
    updated_at: "2026-05-04T12:40:00Z",
  },
];

const gatewayEvidence: GatewayEvidenceItem[] = [
  {
    id: "evidence-1",
    evidence_type: "gateway_policy_decision",
    framework_refs: ["internal_gateway_governance"],
    linked_request_id: "",
    linked_session_id: "",
    linked_span_row_id: "",
    linked_policy_id: "",
    linked_backend_id: "backend-openrouter",
    linked_provider_risk_id: "risk-openrouter",
    summary: "Gateway blocked provider openrouter: provider_risk_rejected",
    payload: { reason_code: "provider_risk_rejected", resource_label: "openrouter" },
    attachment_ref: "",
    generated_at: "2026-05-04T12:46:00Z",
    retain_until: null,
  },
];

const gatewayControlMappings: GatewayControlMappingItem[] = [
  {
    id: "control-1",
    framework: "internal_gateway_governance",
    control_id: "GW-1",
    control_title: "Gateway policy blocks are evidenced",
    mapped_policy_ids: [],
    mapped_evidence_queries: [{ evidence_type: "gateway_policy_decision" }],
    status: "covered",
    owner_user_id: "user-1",
    evidence_count: 1,
    last_evidence_generated_at: "2026-05-04T12:46:00Z",
    updated_at: "2026-05-04T12:47:00Z",
  },
];

const gatewayIncidents: GatewayIncidentItem[] = [
  {
    id: "incident-1",
    severity: "high",
    category: "gateway_provider_risk_block",
    linked_request_id: "",
    linked_session_id: "",
    linked_span_row_id: "",
    linked_policy_id: "",
    linked_provider_risk_id: "risk-openrouter",
    summary: "Gateway blocked provider openrouter: provider_risk_rejected",
    status: "open",
    remediation_notes: "Review provider risk register before enabling backend.",
    opened_at: "2026-05-04T12:48:00Z",
    closed_at: null,
  },
];

const gatewayPolicyExceptions: GatewayPolicyExceptionItem[] = [
  {
    id: "exception-1",
    policy_id: "",
    requester_user_id: "user-1",
    approver_user_id: "",
    reason: "Temporary exception for incident response",
    scope: {
      resource_type: "provider",
      resource_id: "backend-openrouter",
      resource_label: "openrouter",
    },
    status: "requested",
    expires_at: null,
    evidence_references: [],
    created_at: "2026-05-04T12:49:00Z",
    updated_at: "2026-05-04T12:49:00Z",
  },
];

const gatewayEvidenceBundle: GatewayEvidenceBundleResponse = {
  evidence_bundle: {
    subject: {
      session_id: "",
      incident_id: "incident-1",
      policy_decision_id: "",
      list_limit: 25,
      generated_by: "multica web ui",
      capture_policy_note: "content visibility follows the workspace Gateway capture policy",
    },
  },
  export: {
    generated_at: "2026-05-04T12:52:00Z",
    workspace_id: "ws-1",
    subject_id: "incident-1",
    subject_type: "incident",
    digest_sha256: "bundle-digest-sha256",
    sections: ["evidence_bundle", "policy_decisions", "evidence", "incidents"],
  },
  llm_calls: llmCalls,
  policy_decisions: gatewayPolicyDecisions,
  evidence: gatewayEvidence,
  incidents: gatewayIncidents,
  provider_risks: gatewayProviderRisks,
  control_mappings: gatewayControlMappings,
  governance_policies: gatewayGovernancePolicies,
};

const gatewayExport: GatewayExportResponse = {
  generated_at: "2026-05-04T14:00:00Z",
  workspace_id: "ws-1",
  overview,
  sessions,
  llm_calls: llmCalls,
  policy_decisions: gatewayPolicyDecisions,
  evidence: gatewayEvidence,
};

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
    mockApi.listGatewayGovernancePolicies.mockResolvedValue(gatewayGovernancePolicies);
    mockApi.createGatewayGovernancePolicy.mockResolvedValue({
      ...gatewayGovernancePolicies[0],
      id: "policy-2",
      name: "Route source code",
      policy_type: "routing",
    });
    mockApi.updateGatewayGovernancePolicy.mockResolvedValue({
      ...gatewayGovernancePolicies[0],
      enabled: false,
      version: 2,
    });
    mockApi.listGatewayEvidence.mockResolvedValue(gatewayEvidence);
    mockApi.listGatewayControlMappings.mockResolvedValue(gatewayControlMappings);
    mockApi.listGatewayIncidents.mockResolvedValue(gatewayIncidents);
    mockApi.listGatewayPolicyExceptions.mockResolvedValue(gatewayPolicyExceptions);
    mockApi.getGatewayDoctor.mockResolvedValue(gatewayDoctor);
    mockApi.getGatewayHealthReport.mockResolvedValue(gatewayHealthReport);
    mockApi.getGatewayGovernanceInsights.mockResolvedValue(gatewayGovernanceInsights);
    mockApi.getGatewayEvidenceBundle.mockResolvedValue(gatewayEvidenceBundle);
    mockApi.createGatewayPolicyException.mockResolvedValue(gatewayPolicyExceptions[0]);
    mockApi.updateGatewayPolicyException.mockResolvedValue({
      ...gatewayPolicyExceptions[0],
      status: "approved",
      approver_user_id: "user-1",
      expires_at: "2026-12-31T00:00:00Z",
    });
    mockApi.updateGatewayIncident.mockResolvedValue({
      ...gatewayIncidents[0],
      status: "remediated",
      remediation_notes: "Provider review completed.",
    });
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
    mockApi.listGatewayBackendCredentials.mockImplementation((backendId: string) =>
      Promise.resolve(gatewayBackendCredentials.filter((credential) => credential.backend_id === backendId)),
    );
    mockApi.createGatewayBackendCredential.mockResolvedValue({
      ...gatewayBackendCredentials[0],
      id: "credential-openrouter-new",
      label: "New account",
      credential_hint: "sk-live...cret",
      priority: 30,
    });
    mockApi.updateGatewayBackendCredential.mockResolvedValue({
      ...gatewayBackendCredentials[0],
      enabled: false,
    });
    mockApi.exportGatewayData.mockResolvedValue(gatewayExport);
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
    expect(screen.getByText("web_search")).toBeInTheDocument();
    expect(screen.getByText("medium risk")).toBeInTheDocument();
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
    expect(screen.getByText("Capture: full_content")).toBeInTheDocument();

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

  it("shows backend credential pools and manages credential rotation without exposing raw keys", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));
    await user.click(await screen.findByRole("button", { name: /Manage credentials for OpenRouter/ }));

    const credentialPool = await screen.findByRole("table", { name: /OpenRouter credential pool/ });
    expect(within(credentialPool).getByText("Primary workspace key")).toBeInTheDocument();
    expect(within(credentialPool).getByText("sk-or-12...cdef")).toBeInTheDocument();
    expect(within(credentialPool).getByText("Overflow account")).toBeInTheDocument();
    expect(within(credentialPool).getByText("rate limited")).toBeInTheDocument();
    expect(within(credentialPool).getByText("429 rate limit exceeded")).toBeInTheDocument();
    expect(screen.queryByText("sk-live-secret")).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Credential label for OpenRouter"), "New account");
    await user.clear(screen.getByLabelText("Credential priority for OpenRouter"));
    await user.type(screen.getByLabelText("Credential priority for OpenRouter"), "30");
    await user.type(screen.getByLabelText("Credential API key for OpenRouter"), "sk-live-secret");
    await user.click(screen.getByRole("button", { name: /Add credential for OpenRouter/ }));

    await waitFor(() => {
      expect(mockApi.createGatewayBackendCredential).toHaveBeenCalledWith("backend-openrouter", {
        label: "New account",
        key: "sk-live-secret",
        priority: 30,
        enabled: true,
      });
    });
    expect(screen.queryByDisplayValue("sk-live-secret")).not.toBeInTheDocument();

    await user.click(within(credentialPool).getByRole("button", { name: /Disable Primary workspace key/ }));
    await waitFor(() => {
      expect(mockApi.updateGatewayBackendCredential).toHaveBeenCalledWith("backend-openrouter", "credential-openrouter-primary", {
        enabled: false,
      });
    });
  });

  it("exports Gateway observability data from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));
    await user.selectOptions(await screen.findByLabelText("Gateway export window"), "7d");
    await user.click(screen.getByRole("button", { name: /Export Gateway data/ }));

    await waitFor(() => {
      expect(mockApi.exportGatewayData).toHaveBeenCalledWith({ since: "7d", limit: 50 });
    });
    expect(await screen.findByText("Export generated May 4: 2 sessions, 3 LLM calls")).toBeInTheDocument();
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
    expect(mockApi.listGatewayEvidence).not.toHaveBeenCalled();
    expect(mockApi.listGatewayControlMappings).not.toHaveBeenCalled();
    expect(mockApi.listGatewayIncidents).not.toHaveBeenCalled();
    expect(mockApi.listGatewayPolicyExceptions).not.toHaveBeenCalled();
    expect(mockApi.getGatewayDoctor).not.toHaveBeenCalled();
    expect(mockApi.getGatewayHealthReport).not.toHaveBeenCalled();
  });

  it("shows Gateway health for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Gateway Health")).toBeInTheDocument();
    expect(screen.getByText("healthy_with_warnings")).toBeInTheDocument();
    expect(screen.getAllByText("openrouter").length).toBeGreaterThan(0);
    expect(screen.getByText("12 models")).toBeInTheDocument();
    expect(screen.getByText("1/2 active")).toBeInTheDocument();
    expect(screen.getByText("full_content")).toBeInTheDocument();
    expect(screen.getByText("2 open incidents")).toBeInTheDocument();
    expect(screen.getByText("1 pending approvals")).toBeInTheDocument();
    const healthTable = await screen.findByRole("table", { name: /Gateway health checks/ });
    expect(within(healthTable).getByText("User Gateway key")).toBeInTheDocument();
    expect(within(healthTable).getByText("Default backend")).toBeInTheDocument();
    expect(within(healthTable).getByText("Open incidents")).toBeInTheDocument();
    expect(within(healthTable).getByText("Review and remediate open Gateway incidents.")).toBeInTheDocument();
    expect(mockApi.getGatewayHealthReport).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) });
  });

  it("shows Gateway governance insights for admins", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Governance/ }));

    expect(await screen.findByText("Governance Insights")).toBeInTheDocument();
    expect(screen.getByText("1 high severity")).toBeInTheDocument();
    expect(screen.getByText("1 pending approvals")).toBeInTheDocument();
    expect(screen.getByText("4 blocked decisions")).toBeInTheDocument();
    expect(screen.getByText("1 high-risk providers")).toBeInTheDocument();
    expect(screen.getAllByText("gpt-observe").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("claude-sonnet")).toBeInTheDocument();
    expect(screen.getAllByText("openrouter").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("provider_risk_rejected")).toBeInTheDocument();
    expect(screen.getByText("OpenRouter")).toBeInTheDocument();
    expect(screen.getByText("3 covered")).toBeInTheDocument();
    expect(screen.getAllByText("1 gaps").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("7 evidence records").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Review open Gateway incident")).toBeInTheDocument();
    expect(screen.getByText("Review provider risk")).toBeInTheDocument();
    expect(mockApi.getGatewayGovernanceInsights).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) });
  });

  it("runs quick actions from the Gateway governance action queue", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Governance/ }));
    expect(await screen.findByText("Action Queue")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Remediate incident/ }));
    await waitFor(() => {
      expect(mockApi.updateGatewayIncident).toHaveBeenCalledWith("incident-1", {
        status: "remediated",
        remediation_notes: "Remediated from Gateway governance insights.",
      });
    });

    await user.click(screen.getByRole("button", { name: /Approve policy decision/ }));
    await waitFor(() => {
      expect(mockApi.approveGatewayPolicyDecision).toHaveBeenCalledWith("decision-approval-1", {
        reason: "Approved from Gateway governance insights",
      });
    });

    await user.click(screen.getByRole("button", { name: /Deny policy decision/ }));
    await waitFor(() => {
      expect(mockApi.denyGatewayPolicyDecision).toHaveBeenCalledWith("decision-approval-1", {
        reason: "Denied from Gateway governance insights",
      });
    });

    await user.click(screen.getByRole("button", { name: /Revoke policy exception/ }));
    await waitFor(() => {
      expect(mockApi.updateGatewayPolicyException).toHaveBeenCalledWith("exception-1", {
        status: "revoked",
        expires_at: expect.any(String),
      });
    });
    await waitFor(() => {
      expect(mockApi.getGatewayGovernanceInsights.mock.calls.length).toBeGreaterThan(1);
    });
  });

  it("opens an evidence bundle from a Gateway governance action", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Governance/ }));
    await user.click(await screen.findByRole("button", { name: /View evidence bundle for incident-1/ }));

    expect(await screen.findByText("Evidence Bundle")).toBeInTheDocument();
    expect(screen.getAllByText("incident-1").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Gateway blocked provider openrouter: provider_risk_rejected").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("provider_risk_rejected").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("1 LLM calls")).toBeInTheDocument();
    expect(screen.getByText("2 policy decisions")).toBeInTheDocument();
    expect(screen.getByText("1 evidence records")).toBeInTheDocument();
    expect(mockApi.getGatewayEvidenceBundle).toHaveBeenCalledWith({
      incident_id: "incident-1",
      limit: 25,
      signal: expect.any(AbortSignal),
    });
  });

  it("exports evidence bundle JSON and Markdown reports", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    const createObjectURL = vi.fn().mockReturnValue("blob:gateway-report");
    const revokeObjectURL = vi.fn();
    const clickedDownloads: { download: string; href: string }[] = [];
    const originalClipboard = navigator.clipboard;
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: createObjectURL,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: revokeObjectURL,
    });
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function click(this: HTMLAnchorElement) {
      clickedDownloads.push({ download: this.download, href: this.href });
    });

    try {
      renderGatewayPage();

      await user.click(await screen.findByRole("tab", { name: /Governance/ }));
      await user.click(await screen.findByRole("button", { name: /View evidence bundle for incident-1/ }));
      expect(await screen.findByText("Evidence Bundle")).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: /Copy evidence bundle JSON/ }));
      await waitFor(() => {
        expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"incident_id": "incident-1"'));
      });

      await user.click(screen.getByRole("button", { name: /Download evidence bundle JSON/ }));
      await user.click(screen.getByRole("button", { name: /Download evidence bundle Markdown report/ }));

      expect(createObjectURL).toHaveBeenCalledTimes(2);
      const markdownBlob = createObjectURL.mock.calls[1]?.[0] as Blob;
      expect(clickedDownloads.map((item) => item.download)).toEqual([
        "gateway-evidence-bundle-incident-1.json",
        "gateway-evidence-bundle-incident-1.md",
      ]);
      await expect(markdownBlob.text()).resolves.toContain("# Gateway Evidence Bundle Report");
      await expect(markdownBlob.text()).resolves.toContain("Gateway blocked provider openrouter: provider_risk_rejected");
      await expect(markdownBlob.text()).resolves.toContain("provider_risk_rejected");
      await expect(markdownBlob.text()).resolves.toContain("bundle-digest-sha256");
      await expect(markdownBlob.text()).resolves.toContain("content visibility follows the workspace Gateway capture policy");
    } finally {
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: originalClipboard,
      });
      Object.defineProperty(URL, "createObjectURL", {
        configurable: true,
        value: originalCreateObjectURL,
      });
      Object.defineProperty(URL, "revokeObjectURL", {
        configurable: true,
        value: originalRevokeObjectURL,
      });
      clickSpy.mockRestore();
    }
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

  it("approves and denies requested Gateway policy decisions from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    const decisionsTable = await screen.findByRole("table", { name: /Gateway policy decisions/ });
    await user.click(within(decisionsTable).getByRole("button", { name: /Approve gpt-approval/ }));

    await waitFor(() => {
      expect(mockApi.approveGatewayPolicyDecision).toHaveBeenCalledWith("decision-approval-1", {
        reason: "Approved from Gateway policy decisions",
      });
    });

    await user.click(within(decisionsTable).getByRole("button", { name: /Deny gpt-approval/ }));

    await waitFor(() => {
      expect(mockApi.denyGatewayPolicyDecision).toHaveBeenCalledWith("decision-approval-1", {
        reason: "Denied from Gateway policy decisions",
      });
    });
  });

  it("creates and toggles Gateway governance policies from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Governance Policies")).toBeInTheDocument();
    const policiesTable = await screen.findByRole("table", { name: /Gateway governance policies/ });
    expect(within(policiesTable).getByText("Block test model")).toBeInTheDocument();
    expect(within(policiesTable).getByText("model")).toBeInTheDocument();
    expect(within(policiesTable).getByText("enforce")).toBeInTheDocument();

    await user.clear(screen.getByLabelText("Policy name"));
    await user.type(screen.getByLabelText("Policy name"), "Route source code");
    await user.selectOptions(screen.getByLabelText("Policy type"), "routing");
    await user.selectOptions(screen.getByLabelText("Enforcement mode"), "enforce");
    fireEvent.change(screen.getByLabelText("Policy rule definition"), {
      target: {
        value: JSON.stringify({
        rules: [
          {
            id: "route-source-code",
            action: "route_to_backend",
            reason_code: "source_code_routes_local",
            route_backend_slug: "local",
            match: { data_classes: ["source_code"] },
          },
        ],
        }),
      },
    });
    await user.click(screen.getByRole("button", { name: /Create policy/ }));

    await waitFor(() => {
      expect(mockApi.createGatewayGovernancePolicy).toHaveBeenCalledWith({
        name: "Route source code",
        description: "",
        policy_type: "routing",
        enabled: true,
        enforcement_mode: "enforce",
        rule_definition: {
          rules: [
            {
              id: "route-source-code",
              action: "route_to_backend",
              reason_code: "source_code_routes_local",
              route_backend_slug: "local",
              match: { data_classes: ["source_code"] },
            },
          ],
        },
      });
    });

    await user.click(within(policiesTable).getByRole("button", { name: /Disable policy Block test model/ }));
    await waitFor(() => {
      expect(mockApi.updateGatewayGovernancePolicy).toHaveBeenCalledWith("policy-1", {
        name: "Block test model",
        description: "Stops unapproved model usage",
        policy_type: "model",
        enabled: false,
        enforcement_mode: "enforce",
        rule_definition: gatewayGovernancePolicies[0]!.rule_definition,
      });
    });
    expect(mockApi.listGatewayGovernancePolicies).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) });
  });

  it("shows Gateway evidence for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    const evidenceTable = await screen.findByRole("table", { name: /Gateway evidence/ });
    expect(within(evidenceTable).getByText("gateway_policy_decision")).toBeInTheDocument();
    expect(within(evidenceTable).getByText("Gateway blocked provider openrouter: provider_risk_rejected")).toBeInTheDocument();
    expect(within(evidenceTable).getByText("internal_gateway_governance")).toBeInTheDocument();
    expect(mockApi.listGatewayEvidence).toHaveBeenCalledWith({ limit: 20, signal: expect.any(AbortSignal) });
  });

  it("shows Gateway compliance controls for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Compliance Controls")).toBeInTheDocument();
    const controlsTable = await screen.findByRole("table", { name: /Gateway compliance controls/ });
    expect(within(controlsTable).getByText("GW-1")).toBeInTheDocument();
    expect(within(controlsTable).getByText("Gateway policy blocks are evidenced")).toBeInTheDocument();
    expect(within(controlsTable).getByText("covered")).toBeInTheDocument();
    expect(within(controlsTable).getByText("Evidence 1")).toBeInTheDocument();
    expect(mockApi.listGatewayControlMappings).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) });
  });

  it("shows Gateway incidents for admins from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Incidents")).toBeInTheDocument();
    const incidentsTable = await screen.findByRole("table", { name: /Gateway incidents/ });
    expect(within(incidentsTable).getByText("high")).toBeInTheDocument();
    expect(within(incidentsTable).getByText("gateway_provider_risk_block")).toBeInTheDocument();
    expect(within(incidentsTable).getByText("Gateway blocked provider openrouter: provider_risk_rejected")).toBeInTheDocument();
    expect(within(incidentsTable).getByText("open")).toBeInTheDocument();
    expect(mockApi.listGatewayIncidents).toHaveBeenCalledWith({ limit: 20, signal: expect.any(AbortSignal) });
  });

  it("updates Gateway incidents from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    const incidentsTable = await screen.findByRole("table", { name: /Gateway incidents/ });
    await user.click(within(incidentsTable).getByRole("button", { name: /Mark incident remediated/ }));

    await waitFor(() => {
      expect(mockApi.updateGatewayIncident).toHaveBeenCalledWith("incident-1", {
        status: "remediated",
        remediation_notes: "Provider review completed.",
      });
    });
  });

  it("shows and approves Gateway policy exceptions from the Setup tab", async () => {
    const user = userEvent.setup();
    renderGatewayPage();

    await user.click(await screen.findByRole("tab", { name: /Setup/ }));

    expect(await screen.findByText("Policy Exceptions")).toBeInTheDocument();
    const exceptionsTable = await screen.findByRole("table", { name: /Gateway policy exceptions/ });
    expect(within(exceptionsTable).getByText("Temporary exception for incident response")).toBeInTheDocument();
    expect(within(exceptionsTable).getByText("requested")).toBeInTheDocument();
    await user.click(within(exceptionsTable).getByRole("button", { name: /Approve exception/ }));

    await waitFor(() => {
      expect(mockApi.updateGatewayPolicyException).toHaveBeenCalledWith("exception-1", {
        status: "approved",
        expires_at: expect.any(String),
      });
    });
    expect(mockApi.listGatewayPolicyExceptions).toHaveBeenCalledWith({ limit: 20, signal: expect.any(AbortSignal) });
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
