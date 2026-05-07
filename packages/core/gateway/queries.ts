import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { GatewayEvidenceBundleParams, GatewayObservabilityParams } from "../types";

type GatewayFilterKey = Omit<GatewayObservabilityParams, "signal">;
type GatewayEvidenceBundleKey = Omit<GatewayEvidenceBundleParams, "signal">;

function withSignal(
  params: GatewayObservabilityParams | undefined,
  signal: AbortSignal | undefined,
): GatewayObservabilityParams | undefined {
  if (!signal) return params;
  return { ...params, signal };
}

function filterKey(params?: GatewayObservabilityParams): GatewayFilterKey {
  const key: GatewayFilterKey = {};
  if (params?.since) key.since = params.since;
  if (params?.limit !== undefined) key.limit = params.limit;
  if (params?.status) key.status = params.status;
  if (params?.backend) key.backend = params.backend;
  if (params?.model) key.model = params.model;
  return key;
}

function evidenceBundleKey(params: GatewayEvidenceBundleParams): GatewayEvidenceBundleKey {
  const key: GatewayEvidenceBundleKey = {};
  if (params.session_id) key.session_id = params.session_id;
  if (params.incident_id) key.incident_id = params.incident_id;
  if (params.policy_decision_id) key.policy_decision_id = params.policy_decision_id;
  if (params.limit !== undefined) key.limit = params.limit;
  return key;
}

export const gatewayKeys = {
  all: (wsId: string) => ["gateway", wsId] as const,
  overview: (wsId: string, params?: GatewayObservabilityParams) =>
    [...gatewayKeys.all(wsId), "overview", filterKey(params)] as const,
  sessions: (wsId: string, params?: GatewayObservabilityParams) =>
    [...gatewayKeys.all(wsId), "sessions", filterKey(params)] as const,
  session: (wsId: string, id: string) =>
    [...gatewayKeys.all(wsId), "sessions", id] as const,
  sessionSpans: (wsId: string, id: string) =>
    [...gatewayKeys.session(wsId, id), "spans"] as const,
  llmCalls: (wsId: string, params?: GatewayObservabilityParams) =>
    [...gatewayKeys.all(wsId), "llm-calls", filterKey(params)] as const,
  export: (wsId: string, params?: GatewayObservabilityParams) =>
    [...gatewayKeys.all(wsId), "export", filterKey(params)] as const,
  ingestKeys: (wsId: string) =>
    [...gatewayKeys.all(wsId), "ingest-keys"] as const,
  status: (wsId: string) =>
    [...gatewayKeys.all(wsId), "status"] as const,
  doctor: (wsId: string) =>
    [...gatewayKeys.all(wsId), "doctor"] as const,
  healthReport: (wsId: string) =>
    [...gatewayKeys.all(wsId), "health-report"] as const,
  backends: (wsId: string) =>
    [...gatewayKeys.all(wsId), "backends"] as const,
  backendCredentials: (wsId: string, backendId: string) =>
    [...gatewayKeys.backends(wsId), backendId, "credentials"] as const,
  audit: (wsId: string, limit = 20) =>
    [...gatewayKeys.all(wsId), "audit", limit] as const,
  providerRisks: (wsId: string) =>
    [...gatewayKeys.all(wsId), "provider-risks"] as const,
  governanceInsights: (wsId: string) =>
    [...gatewayKeys.all(wsId), "governance-insights"] as const,
  evidenceBundle: (wsId: string, params: GatewayEvidenceBundleParams) =>
    [...gatewayKeys.all(wsId), "evidence-bundle", evidenceBundleKey(params)] as const,
  policyDecisions: (wsId: string, limit = 20) =>
    [...gatewayKeys.all(wsId), "policy-decisions", limit] as const,
  governancePolicies: (wsId: string) =>
    [...gatewayKeys.all(wsId), "governance-policies"] as const,
  evidence: (wsId: string, limit = 20) =>
    [...gatewayKeys.all(wsId), "evidence", limit] as const,
  controlMappings: (wsId: string) =>
    [...gatewayKeys.all(wsId), "control-mappings"] as const,
  incidents: (wsId: string, limit = 20) =>
    [...gatewayKeys.all(wsId), "incidents", limit] as const,
  policyExceptions: (wsId: string, limit = 20) =>
    [...gatewayKeys.all(wsId), "policy-exceptions", limit] as const,
};

export function gatewayOverviewOptions(wsId: string, params?: GatewayObservabilityParams) {
  return queryOptions({
    queryKey: gatewayKeys.overview(wsId, params),
    queryFn: ({ signal }) => api.getGatewayOverview(withSignal(params, signal)),
    enabled: !!wsId,
  });
}

export function gatewaySessionsOptions(wsId: string, params?: GatewayObservabilityParams) {
  return queryOptions({
    queryKey: gatewayKeys.sessions(wsId, params),
    queryFn: ({ signal }) => api.listGatewaySessions(withSignal(params, signal)),
    enabled: !!wsId,
  });
}

export function gatewaySessionDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: gatewayKeys.session(wsId, id),
    queryFn: ({ signal }) =>
      signal ? api.getGatewaySession(id, { signal }) : api.getGatewaySession(id),
    enabled: !!wsId && !!id,
  });
}

export function gatewaySessionSpansOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: gatewayKeys.sessionSpans(wsId, id),
    queryFn: ({ signal }) =>
      signal ? api.getGatewaySessionSpans(id, { signal }) : api.getGatewaySessionSpans(id),
    enabled: !!wsId && !!id,
  });
}

export function gatewayLLMCallsOptions(wsId: string, params?: GatewayObservabilityParams) {
  return queryOptions({
    queryKey: gatewayKeys.llmCalls(wsId, params),
    queryFn: ({ signal }) => api.listGatewayLLMCalls(withSignal(params, signal)),
    enabled: !!wsId,
  });
}

export function gatewayExportOptions(wsId: string, params?: GatewayObservabilityParams) {
  return queryOptions({
    queryKey: gatewayKeys.export(wsId, params),
    queryFn: ({ signal }) => api.exportGatewayData(withSignal(params, signal)),
    enabled: !!wsId,
  });
}

export function gatewayIngestKeysOptions(wsId: string) {
  return queryOptions({
    queryKey: gatewayKeys.ingestKeys(wsId),
    queryFn: ({ signal }) =>
      signal ? api.listGatewayIngestKeys({ signal }) : api.listGatewayIngestKeys(),
    enabled: !!wsId,
  });
}

export function gatewayStatusOptions(wsId: string) {
  return queryOptions({
    queryKey: gatewayKeys.status(wsId),
    queryFn: ({ signal }) =>
      signal ? api.getGatewayStatus({ signal }) : api.getGatewayStatus(),
    enabled: !!wsId,
  });
}

export function gatewayDoctorOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.doctor(wsId),
    queryFn: ({ signal }) => api.getGatewayDoctor({ signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayHealthReportOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.healthReport(wsId),
    queryFn: ({ signal }) => api.getGatewayHealthReport({ signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayBackendsOptions(wsId: string) {
  return queryOptions({
    queryKey: gatewayKeys.backends(wsId),
    queryFn: ({ signal }) =>
      signal ? api.listGatewayBackends({ signal }) : api.listGatewayBackends(),
    enabled: !!wsId,
  });
}

export function gatewayBackendCredentialsOptions(wsId: string, backendId: string) {
  return queryOptions({
    queryKey: gatewayKeys.backendCredentials(wsId, backendId),
    queryFn: ({ signal }) =>
      signal
        ? api.listGatewayBackendCredentials(backendId, { signal })
        : api.listGatewayBackendCredentials(backendId),
    enabled: !!wsId && !!backendId,
  });
}

export function gatewayAuditOptions(wsId: string, limit = 20, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.audit(wsId, limit),
    queryFn: ({ signal }) => api.listGatewayAudit({ limit, signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayProviderRisksOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.providerRisks(wsId),
    queryFn: ({ signal }) =>
      signal ? api.listGatewayProviderRisks({ signal }) : api.listGatewayProviderRisks(),
    enabled: !!wsId && enabled,
  });
}

export function gatewayGovernanceInsightsOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.governanceInsights(wsId),
    queryFn: ({ signal }) => api.getGatewayGovernanceInsights({ signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayEvidenceBundleOptions(
  wsId: string,
  params: GatewayEvidenceBundleParams,
  enabled = true,
) {
  return queryOptions({
    queryKey: gatewayKeys.evidenceBundle(wsId, params),
    queryFn: ({ signal }) => api.getGatewayEvidenceBundle({ ...params, signal }),
    enabled: !!wsId && enabled && Boolean(params.session_id || params.incident_id || params.policy_decision_id),
  });
}

export function gatewayPolicyDecisionsOptions(wsId: string, limit = 20, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.policyDecisions(wsId, limit),
    queryFn: ({ signal }) => api.listGatewayPolicyDecisions({ limit, signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayGovernancePoliciesOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.governancePolicies(wsId),
    queryFn: ({ signal }) =>
      signal ? api.listGatewayGovernancePolicies({ signal }) : api.listGatewayGovernancePolicies(),
    enabled: !!wsId && enabled,
  });
}

export function gatewayEvidenceOptions(wsId: string, limit = 20, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.evidence(wsId, limit),
    queryFn: ({ signal }) => api.listGatewayEvidence({ limit, signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayControlMappingsOptions(wsId: string, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.controlMappings(wsId),
    queryFn: ({ signal }) => api.listGatewayControlMappings({ signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayIncidentsOptions(wsId: string, limit = 20, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.incidents(wsId, limit),
    queryFn: ({ signal }) => api.listGatewayIncidents({ limit, signal }),
    enabled: !!wsId && enabled,
  });
}

export function gatewayPolicyExceptionsOptions(wsId: string, limit = 20, enabled = true) {
  return queryOptions({
    queryKey: gatewayKeys.policyExceptions(wsId, limit),
    queryFn: ({ signal }) => api.listGatewayPolicyExceptions({ limit, signal }),
    enabled: !!wsId && enabled,
  });
}
