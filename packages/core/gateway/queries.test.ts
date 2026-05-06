import { beforeEach, describe, expect, it, vi } from "vitest";

const { mockApi } = vi.hoisted(() => ({
  mockApi: {
    getGatewayOverview: vi.fn(),
    listGatewaySessions: vi.fn(),
    getGatewaySession: vi.fn(),
    getGatewaySessionSpans: vi.fn(),
    listGatewayLLMCalls: vi.fn(),
    exportGatewayData: vi.fn(),
    listGatewayIngestKeys: vi.fn(),
    getGatewayStatus: vi.fn(),
    listGatewayBackends: vi.fn(),
    listGatewayBackendCredentials: vi.fn(),
    listGatewayAudit: vi.fn(),
    listGatewayProviderRisks: vi.fn(),
    listGatewayPolicyDecisions: vi.fn(),
    listGatewayGovernancePolicies: vi.fn(),
    listGatewayEvidence: vi.fn(),
    listGatewayControlMappings: vi.fn(),
    listGatewayIncidents: vi.fn(),
    listGatewayPolicyExceptions: vi.fn(),
    getGatewayDoctor: vi.fn(),
  },
}));

vi.mock("../api", () => ({ api: mockApi }));

import {
  gatewayKeys,
  gatewayOverviewOptions,
  gatewaySessionsOptions,
  gatewaySessionDetailOptions,
  gatewaySessionSpansOptions,
  gatewayLLMCallsOptions,
  gatewayExportOptions,
  gatewayIngestKeysOptions,
  gatewayStatusOptions,
  gatewayBackendsOptions,
  gatewayBackendCredentialsOptions,
  gatewayAuditOptions,
  gatewayProviderRisksOptions,
  gatewayPolicyDecisionsOptions,
  gatewayGovernancePoliciesOptions,
  gatewayEvidenceOptions,
  gatewayControlMappingsOptions,
  gatewayIncidentsOptions,
  gatewayPolicyExceptionsOptions,
  gatewayDoctorOptions,
} from "./queries";

describe("gateway query options", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("builds stable keys scoped by workspace and filters", () => {
    expect(gatewayKeys.overview("ws-1", { since: "24h", backend: "openrouter" })).toEqual([
      "gateway",
      "ws-1",
      "overview",
      { since: "24h", backend: "openrouter" },
    ]);
    expect(gatewayKeys.session("ws-1", "session-1")).toEqual([
      "gateway",
      "ws-1",
      "sessions",
      "session-1",
    ]);
    expect(gatewayKeys.ingestKeys("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "ingest-keys",
    ]);
    expect(gatewayKeys.status("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "status",
    ]);
    expect(gatewayKeys.backends("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "backends",
    ]);
    expect(gatewayKeys.backendCredentials("ws-1", "backend-1")).toEqual([
      "gateway",
      "ws-1",
      "backends",
      "backend-1",
      "credentials",
    ]);
    expect(gatewayKeys.export("ws-1", { since: "24h", limit: 10 })).toEqual([
      "gateway",
      "ws-1",
      "export",
      { since: "24h", limit: 10 },
    ]);
    expect(gatewayKeys.audit("ws-1", 20)).toEqual([
      "gateway",
      "ws-1",
      "audit",
      20,
    ]);
    expect(gatewayKeys.providerRisks("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "provider-risks",
    ]);
    expect(gatewayKeys.policyDecisions("ws-1", 20)).toEqual([
      "gateway",
      "ws-1",
      "policy-decisions",
      20,
    ]);
    expect(gatewayKeys.governancePolicies("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "governance-policies",
    ]);
    expect(gatewayKeys.evidence("ws-1", 20)).toEqual([
      "gateway",
      "ws-1",
      "evidence",
      20,
    ]);
    expect(gatewayKeys.controlMappings("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "control-mappings",
    ]);
    expect(gatewayKeys.incidents("ws-1", 20)).toEqual([
      "gateway",
      "ws-1",
      "incidents",
      20,
    ]);
    expect(gatewayKeys.policyExceptions("ws-1", 20)).toEqual([
      "gateway",
      "ws-1",
      "policy-exceptions",
      20,
    ]);
    expect(gatewayKeys.doctor("ws-1")).toEqual([
      "gateway",
      "ws-1",
      "doctor",
    ]);
  });

  it("routes query functions to the Gateway API client", async () => {
    mockApi.getGatewayOverview.mockResolvedValueOnce({ summary: {} });
    mockApi.listGatewaySessions.mockResolvedValueOnce({ sessions: [] });
    mockApi.getGatewaySession.mockResolvedValueOnce({ id: "session-1" });
    mockApi.getGatewaySessionSpans.mockResolvedValueOnce({ spans: [] });
    mockApi.listGatewayLLMCalls.mockResolvedValueOnce({ calls: [] });
    mockApi.exportGatewayData.mockResolvedValueOnce({ generated_at: "now" });
    mockApi.listGatewayIngestKeys.mockResolvedValueOnce([]);
    mockApi.getGatewayStatus.mockResolvedValueOnce({ capture_policy: "full_content" });
    mockApi.listGatewayBackends.mockResolvedValueOnce([]);
    mockApi.listGatewayBackendCredentials.mockResolvedValueOnce([]);
    mockApi.listGatewayAudit.mockResolvedValueOnce([]);
    mockApi.listGatewayProviderRisks.mockResolvedValueOnce([]);
    mockApi.listGatewayPolicyDecisions.mockResolvedValueOnce([]);
    mockApi.listGatewayGovernancePolicies.mockResolvedValueOnce([]);
    mockApi.listGatewayEvidence.mockResolvedValueOnce([]);
    mockApi.listGatewayControlMappings.mockResolvedValueOnce([]);
    mockApi.listGatewayIncidents.mockResolvedValueOnce([]);
    mockApi.listGatewayPolicyExceptions.mockResolvedValueOnce([]);
    mockApi.getGatewayDoctor.mockResolvedValueOnce({ status: "healthy", checks: [] });

    const overviewQuery = gatewayOverviewOptions("ws-1", { since: "24h" });
    const sessionsQuery = gatewaySessionsOptions("ws-1", { limit: 25 });
    const sessionQuery = gatewaySessionDetailOptions("ws-1", "session-1");
    const spansQuery = gatewaySessionSpansOptions("ws-1", "session-1");
    const callsQuery = gatewayLLMCallsOptions("ws-1", { model: "gpt-4.1" });
    const exportQuery = gatewayExportOptions("ws-1", { since: "24h", limit: 10 });
    const ingestKeysQuery = gatewayIngestKeysOptions("ws-1");
    const statusQuery = gatewayStatusOptions("ws-1");
    const backendsQuery = gatewayBackendsOptions("ws-1");
    const credentialsQuery = gatewayBackendCredentialsOptions("ws-1", "backend-1");
    const auditQuery = gatewayAuditOptions("ws-1");
    const providerRisksQuery = gatewayProviderRisksOptions("ws-1");
    const policyDecisionsQuery = gatewayPolicyDecisionsOptions("ws-1");
    const governancePoliciesQuery = gatewayGovernancePoliciesOptions("ws-1");
    const evidenceQuery = gatewayEvidenceOptions("ws-1");
    const controlsQuery = gatewayControlMappingsOptions("ws-1");
    const incidentsQuery = gatewayIncidentsOptions("ws-1");
    const exceptionsQuery = gatewayPolicyExceptionsOptions("ws-1");
    const doctorQuery = gatewayDoctorOptions("ws-1");

    expect(overviewQuery.queryFn).toBeTypeOf("function");
    expect(sessionsQuery.queryFn).toBeTypeOf("function");
    expect(sessionQuery.queryFn).toBeTypeOf("function");
    expect(spansQuery.queryFn).toBeTypeOf("function");
    expect(callsQuery.queryFn).toBeTypeOf("function");
    expect(exportQuery.queryFn).toBeTypeOf("function");
    expect(ingestKeysQuery.queryFn).toBeTypeOf("function");
    expect(statusQuery.queryFn).toBeTypeOf("function");
    expect(backendsQuery.queryFn).toBeTypeOf("function");
    expect(credentialsQuery.queryFn).toBeTypeOf("function");
    expect(auditQuery.queryFn).toBeTypeOf("function");
    expect(providerRisksQuery.queryFn).toBeTypeOf("function");
    expect(policyDecisionsQuery.queryFn).toBeTypeOf("function");
    expect(governancePoliciesQuery.queryFn).toBeTypeOf("function");
    expect(evidenceQuery.queryFn).toBeTypeOf("function");
    expect(controlsQuery.queryFn).toBeTypeOf("function");
    expect(incidentsQuery.queryFn).toBeTypeOf("function");
    expect(exceptionsQuery.queryFn).toBeTypeOf("function");
    expect(doctorQuery.queryFn).toBeTypeOf("function");

    await overviewQuery.queryFn!({} as never);
    await sessionsQuery.queryFn!({} as never);
    await sessionQuery.queryFn!({} as never);
    await spansQuery.queryFn!({} as never);
    await callsQuery.queryFn!({} as never);
    await exportQuery.queryFn!({} as never);
    await ingestKeysQuery.queryFn!({} as never);
    await statusQuery.queryFn!({} as never);
    await backendsQuery.queryFn!({} as never);
    await credentialsQuery.queryFn!({} as never);
    await auditQuery.queryFn!({} as never);
    await providerRisksQuery.queryFn!({} as never);
    await policyDecisionsQuery.queryFn!({} as never);
    await governancePoliciesQuery.queryFn!({} as never);
    await evidenceQuery.queryFn!({} as never);
    await controlsQuery.queryFn!({} as never);
    await incidentsQuery.queryFn!({} as never);
    await exceptionsQuery.queryFn!({} as never);
    await doctorQuery.queryFn!({} as never);

    expect(mockApi.getGatewayOverview).toHaveBeenCalledWith({ since: "24h" });
    expect(mockApi.listGatewaySessions).toHaveBeenCalledWith({ limit: 25 });
    expect(mockApi.getGatewaySession).toHaveBeenCalledWith("session-1");
    expect(mockApi.getGatewaySessionSpans).toHaveBeenCalledWith("session-1");
    expect(mockApi.listGatewayLLMCalls).toHaveBeenCalledWith({ model: "gpt-4.1" });
    expect(mockApi.exportGatewayData).toHaveBeenCalledWith({ since: "24h", limit: 10 });
    expect(mockApi.listGatewayIngestKeys).toHaveBeenCalledWith();
    expect(mockApi.getGatewayStatus).toHaveBeenCalledWith();
    expect(mockApi.listGatewayBackends).toHaveBeenCalledWith();
    expect(mockApi.listGatewayBackendCredentials).toHaveBeenCalledWith("backend-1");
    expect(mockApi.listGatewayAudit).toHaveBeenCalledWith({ limit: 20, signal: undefined });
    expect(mockApi.listGatewayProviderRisks).toHaveBeenCalledWith();
    expect(mockApi.listGatewayPolicyDecisions).toHaveBeenCalledWith({ limit: 20, signal: undefined });
    expect(mockApi.listGatewayGovernancePolicies).toHaveBeenCalledWith();
    expect(mockApi.listGatewayEvidence).toHaveBeenCalledWith({ limit: 20, signal: undefined });
    expect(mockApi.listGatewayControlMappings).toHaveBeenCalledWith({ signal: undefined });
    expect(mockApi.listGatewayIncidents).toHaveBeenCalledWith({ limit: 20, signal: undefined });
    expect(mockApi.listGatewayPolicyExceptions).toHaveBeenCalledWith({ limit: 20, signal: undefined });
    expect(mockApi.getGatewayDoctor).toHaveBeenCalledWith({ signal: undefined });
  });

  it("passes abort signals through to the Gateway API client", async () => {
    const signal = new AbortController().signal;
    mockApi.getGatewayOverview.mockResolvedValueOnce({ summary: {} });
    mockApi.getGatewaySession.mockResolvedValueOnce({ id: "session-1" });
    mockApi.exportGatewayData.mockResolvedValueOnce({ generated_at: "now" });
    mockApi.listGatewayIngestKeys.mockResolvedValueOnce([]);
    mockApi.getGatewayStatus.mockResolvedValueOnce({ capture_policy: "full_content" });
    mockApi.listGatewayBackends.mockResolvedValueOnce([]);
    mockApi.listGatewayBackendCredentials.mockResolvedValueOnce([]);
    mockApi.listGatewayAudit.mockResolvedValueOnce([]);
    mockApi.listGatewayProviderRisks.mockResolvedValueOnce([]);
    mockApi.listGatewayPolicyDecisions.mockResolvedValueOnce([]);
    mockApi.listGatewayGovernancePolicies.mockResolvedValueOnce([]);
    mockApi.listGatewayEvidence.mockResolvedValueOnce([]);
    mockApi.listGatewayControlMappings.mockResolvedValueOnce([]);
    mockApi.listGatewayIncidents.mockResolvedValueOnce([]);
    mockApi.listGatewayPolicyExceptions.mockResolvedValueOnce([]);
    mockApi.getGatewayDoctor.mockResolvedValueOnce({ status: "healthy", checks: [] });

    await gatewayOverviewOptions("ws-1", { since: "24h" }).queryFn!({ signal } as never);
    await gatewaySessionDetailOptions("ws-1", "session-1").queryFn!({ signal } as never);
    await gatewayExportOptions("ws-1", { since: "24h", limit: 10 }).queryFn!({ signal } as never);
    await gatewayIngestKeysOptions("ws-1").queryFn!({ signal } as never);
    await gatewayStatusOptions("ws-1").queryFn!({ signal } as never);
    await gatewayBackendsOptions("ws-1").queryFn!({ signal } as never);
    await gatewayBackendCredentialsOptions("ws-1", "backend-1").queryFn!({ signal } as never);
    await gatewayAuditOptions("ws-1", 10).queryFn!({ signal } as never);
    await gatewayProviderRisksOptions("ws-1").queryFn!({ signal } as never);
    await gatewayPolicyDecisionsOptions("ws-1", 10).queryFn!({ signal } as never);
    await gatewayGovernancePoliciesOptions("ws-1").queryFn!({ signal } as never);
    await gatewayEvidenceOptions("ws-1", 10).queryFn!({ signal } as never);
    await gatewayControlMappingsOptions("ws-1").queryFn!({ signal } as never);
    await gatewayIncidentsOptions("ws-1", 10).queryFn!({ signal } as never);
    await gatewayPolicyExceptionsOptions("ws-1", 10).queryFn!({ signal } as never);
    await gatewayDoctorOptions("ws-1").queryFn!({ signal } as never);

    expect(mockApi.getGatewayOverview).toHaveBeenCalledWith({ since: "24h", signal });
    expect(mockApi.getGatewaySession).toHaveBeenCalledWith("session-1", { signal });
    expect(mockApi.exportGatewayData).toHaveBeenCalledWith({ since: "24h", limit: 10, signal });
    expect(mockApi.listGatewayIngestKeys).toHaveBeenCalledWith({ signal });
    expect(mockApi.getGatewayStatus).toHaveBeenCalledWith({ signal });
    expect(mockApi.listGatewayBackends).toHaveBeenCalledWith({ signal });
    expect(mockApi.listGatewayBackendCredentials).toHaveBeenCalledWith("backend-1", { signal });
    expect(mockApi.listGatewayAudit).toHaveBeenCalledWith({ limit: 10, signal });
    expect(mockApi.listGatewayProviderRisks).toHaveBeenCalledWith({ signal });
    expect(mockApi.listGatewayPolicyDecisions).toHaveBeenCalledWith({ limit: 10, signal });
    expect(mockApi.listGatewayGovernancePolicies).toHaveBeenCalledWith({ signal });
    expect(mockApi.listGatewayEvidence).toHaveBeenCalledWith({ limit: 10, signal });
    expect(mockApi.listGatewayControlMappings).toHaveBeenCalledWith({ signal });
    expect(mockApi.listGatewayIncidents).toHaveBeenCalledWith({ limit: 10, signal });
    expect(mockApi.listGatewayPolicyExceptions).toHaveBeenCalledWith({ limit: 10, signal });
    expect(mockApi.getGatewayDoctor).toHaveBeenCalledWith({ signal });
  });
});
