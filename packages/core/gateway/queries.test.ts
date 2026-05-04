import { beforeEach, describe, expect, it, vi } from "vitest";

const { mockApi } = vi.hoisted(() => ({
  mockApi: {
    getGatewayOverview: vi.fn(),
    listGatewaySessions: vi.fn(),
    getGatewaySession: vi.fn(),
    getGatewaySessionSpans: vi.fn(),
    listGatewayLLMCalls: vi.fn(),
    listGatewayIngestKeys: vi.fn(),
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
  gatewayIngestKeysOptions,
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
  });

  it("routes query functions to the Gateway API client", async () => {
    mockApi.getGatewayOverview.mockResolvedValueOnce({ summary: {} });
    mockApi.listGatewaySessions.mockResolvedValueOnce({ sessions: [] });
    mockApi.getGatewaySession.mockResolvedValueOnce({ id: "session-1" });
    mockApi.getGatewaySessionSpans.mockResolvedValueOnce({ spans: [] });
    mockApi.listGatewayLLMCalls.mockResolvedValueOnce({ calls: [] });
    mockApi.listGatewayIngestKeys.mockResolvedValueOnce([]);

    const overviewQuery = gatewayOverviewOptions("ws-1", { since: "24h" });
    const sessionsQuery = gatewaySessionsOptions("ws-1", { limit: 25 });
    const sessionQuery = gatewaySessionDetailOptions("ws-1", "session-1");
    const spansQuery = gatewaySessionSpansOptions("ws-1", "session-1");
    const callsQuery = gatewayLLMCallsOptions("ws-1", { model: "gpt-4.1" });
    const ingestKeysQuery = gatewayIngestKeysOptions("ws-1");

    expect(overviewQuery.queryFn).toBeTypeOf("function");
    expect(sessionsQuery.queryFn).toBeTypeOf("function");
    expect(sessionQuery.queryFn).toBeTypeOf("function");
    expect(spansQuery.queryFn).toBeTypeOf("function");
    expect(callsQuery.queryFn).toBeTypeOf("function");
    expect(ingestKeysQuery.queryFn).toBeTypeOf("function");

    await overviewQuery.queryFn!({} as never);
    await sessionsQuery.queryFn!({} as never);
    await sessionQuery.queryFn!({} as never);
    await spansQuery.queryFn!({} as never);
    await callsQuery.queryFn!({} as never);
    await ingestKeysQuery.queryFn!({} as never);

    expect(mockApi.getGatewayOverview).toHaveBeenCalledWith({ since: "24h" });
    expect(mockApi.listGatewaySessions).toHaveBeenCalledWith({ limit: 25 });
    expect(mockApi.getGatewaySession).toHaveBeenCalledWith("session-1");
    expect(mockApi.getGatewaySessionSpans).toHaveBeenCalledWith("session-1");
    expect(mockApi.listGatewayLLMCalls).toHaveBeenCalledWith({ model: "gpt-4.1" });
    expect(mockApi.listGatewayIngestKeys).toHaveBeenCalledWith();
  });

  it("passes abort signals through to the Gateway API client", async () => {
    const signal = new AbortController().signal;
    mockApi.getGatewayOverview.mockResolvedValueOnce({ summary: {} });
    mockApi.getGatewaySession.mockResolvedValueOnce({ id: "session-1" });
    mockApi.listGatewayIngestKeys.mockResolvedValueOnce([]);

    await gatewayOverviewOptions("ws-1", { since: "24h" }).queryFn!({ signal } as never);
    await gatewaySessionDetailOptions("ws-1", "session-1").queryFn!({ signal } as never);
    await gatewayIngestKeysOptions("ws-1").queryFn!({ signal } as never);

    expect(mockApi.getGatewayOverview).toHaveBeenCalledWith({ since: "24h", signal });
    expect(mockApi.getGatewaySession).toHaveBeenCalledWith("session-1", { signal });
    expect(mockApi.listGatewayIngestKeys).toHaveBeenCalledWith({ signal });
  });
});
