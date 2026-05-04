import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { GatewayObservabilityParams } from "../types";

type GatewayFilterKey = Omit<GatewayObservabilityParams, "signal">;

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
