import { describe, expect, it, vi } from "vitest";
import { ObserverClient } from "./index";

function ids(values: string[]) {
  let index = 0;
  return () => {
    const value = values[index];
    if (!value) throw new Error("id sequence exhausted");
    index += 1;
    return value;
  };
}

describe("ObserverClient", () => {
  it("creates trace context and injects Gateway correlation headers", () => {
    const client = new ObserverClient({
      gatewayBaseUrl: "https://gateway.example.com",
      gatewayKey: "mgw_test",
      serviceName: "checkout-api",
      appId: "checkout",
      environment: "production",
      deploymentId: "deploy-1",
      aiSystemId: "support-bot",
      idGenerator: ids(["trace-1", "session-1", "root-1"]),
    });

    const trace = client.startTrace({
      name: "Checkout run",
      agentId: "agent-checkout",
      workflowId: "workflow-checkout",
      endUserId: "user-123",
      useCase: "customer support",
      dataDomains: ["orders", "payments"],
      toolHint: "openai-sdk",
    });

    expect(trace.traceId).toBe("trace-1");
    expect(trace.sessionId).toBe("session-1");
    expect(trace.rootSpanId).toBe("root-1");

    expect(client.injectHeaders(trace, {
      headers: { "Content-Type": "application/json" },
      parentSpanId: "tool-1",
    })).toEqual({
      "Content-Type": "application/json",
      "X-Multica-Trace-ID": "trace-1",
      "X-Multica-Session-ID": "session-1",
      "X-Multica-Parent-Span-ID": "tool-1",
      "X-Multica-App-ID": "checkout",
      "X-Multica-Service-Name": "checkout-api",
      "X-Multica-Environment": "production",
      "X-Multica-Deployment-ID": "deploy-1",
      "X-Multica-AI-System-ID": "support-bot",
      "X-Multica-Agent-ID": "agent-checkout",
      "X-Multica-Workflow-ID": "workflow-checkout",
      "X-Multica-End-User-ID": "user-123",
      "X-Multica-Use-Case": "customer support",
      "X-Multica-Data-Domains": "orders,payments",
      "X-Multica-Tool-Hint": "openai-sdk",
    });
  });

  it("records spans, logs, tools, and flushes to the trace ingest endpoint", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      trace_id: "trace-1",
      session_id: "server-session-1",
      span_count: 2,
      event_count: 1,
      log_count: 1,
      agent_count: 1,
      tool_count: 1,
    }), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    }));
    const fixedNow = new Date("2026-05-04T12:00:00.000Z");
    const client = new ObserverClient({
      gatewayBaseUrl: "https://gateway.example.com/",
      gatewayKey: "mig_test",
      serviceName: "checkout-api",
      appId: "checkout",
      environment: "staging",
      fetch: fetchMock,
      now: () => fixedNow,
      idGenerator: ids(["trace-1", "session-1", "root-1", "span-tool-1"]),
    });

    const trace = client.startTrace({ name: "Checkout run", agentId: "agent-checkout" });
    const toolSpan = trace.span({
      name: "Search inventory",
      kind: "tool",
      parentSpanId: trace.rootSpanId,
      attributes: { "tool.name": "inventory.search" },
      statusCode: "ok",
      durationMs: 42,
    });
    trace.event({ spanId: toolSpan.spanId, eventType: "tool.completed", payload: { items: 2 } });
    trace.log({ spanId: toolSpan.spanId, severity: "info", body: "inventory search completed" });
    trace.agent({
      spanId: trace.rootSpanId,
      agentId: "agent-checkout",
      agentName: "Checkout Agent",
      role: "customer-support",
      models: ["gpt-4.1"],
      tools: ["inventory.search"],
    });
    trace.tool({
      spanId: toolSpan.spanId,
      toolId: "inventory.search",
      toolName: "inventory.search",
      status: "success",
      parameters: { sku: "sku_123" },
      result: { available: true },
      durationMs: 42,
    });

    const result = await client.flush(trace);

    expect(result).toEqual({
      traceId: "trace-1",
      sessionId: "server-session-1",
      spanCount: 2,
      eventCount: 1,
      logCount: 1,
      agentCount: 1,
      toolCount: 1,
    });
    expect(fetchMock).toHaveBeenCalledOnce();
    const firstCall = fetchMock.mock.calls[0] as [string, RequestInit] | undefined;
    if (!firstCall) throw new Error("expected fetch to be called");
    const [url, init] = firstCall;
    expect(url).toBe("https://gateway.example.com/v1/traces");
    expect(init.method).toBe("POST");
    expect(init.headers).toEqual({
      "Authorization": "Bearer mig_test",
      "Content-Type": "application/json",
    });

    const body = JSON.parse(String(init.body));
    expect(body).toMatchObject({
      trace_id: "trace-1",
      root_span_id: "root-1",
      name: "Checkout run",
      service_name: "checkout-api",
      client_tool_hint: "sdk",
      status: "success",
      resource_attributes: {
        "multica.app_id": "checkout",
        "deployment.environment": "staging",
      },
    });
    expect(body.spans).toHaveLength(2);
    expect(body.spans[0]).toMatchObject({
      span_id: "root-1",
      name: "Checkout run",
      kind: "workflow",
      status_code: "ok",
      started_at: "2026-05-04T12:00:00.000Z",
    });
    expect(body.spans[1]).toMatchObject({
      span_id: "span-tool-1",
      parent_span_id: "root-1",
      name: "Search inventory",
      kind: "tool",
      duration_ms: 42,
    });
    expect(body.logs[0]).toMatchObject({
      span_id: "span-tool-1",
      severity: "info",
      body: "inventory search completed",
    });
    expect(body.tools[0]).toMatchObject({
      span_id: "span-tool-1",
      tool_id: "inventory.search",
      status: "success",
      parameters: { sku: "sku_123" },
      result: { available: true },
    });
  });

  it("throws a readable error when ingest fails", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      error: "gateway key is invalid",
    }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    }));
    const client = new ObserverClient({
      gatewayBaseUrl: "https://gateway.example.com",
      gatewayKey: "mig_bad",
      serviceName: "checkout-api",
      fetch: fetchMock,
      idGenerator: ids(["trace-1", "session-1", "root-1"]),
    });

    await expect(client.flush(client.startTrace({ name: "Bad key run" }))).rejects.toThrow("gateway key is invalid");
  });

  it("wraps async work as timed span, tool, and agent observations", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      trace_id: "trace-1",
      session_id: "server-session-1",
      span_count: 4,
      event_count: 0,
      log_count: 0,
      agent_count: 1,
      tool_count: 1,
    }), {
      status: 201,
      headers: { "Content-Type": "application/json" },
    }));
    const times = [
      new Date("2026-05-04T12:00:00.000Z"),
      new Date("2026-05-04T12:00:01.000Z"),
      new Date("2026-05-04T12:00:01.250Z"),
      new Date("2026-05-04T12:00:02.000Z"),
      new Date("2026-05-04T12:00:02.040Z"),
      new Date("2026-05-04T12:00:03.000Z"),
      new Date("2026-05-04T12:00:03.075Z"),
    ];
    const client = new ObserverClient({
      gatewayBaseUrl: "https://gateway.example.com",
      gatewayKey: "mig_test",
      serviceName: "checkout-api",
      fetch: fetchMock,
      now: () => {
        const value = times.shift();
        if (!value) throw new Error("clock exhausted");
        return value;
      },
      idGenerator: ids(["trace-1", "session-1", "root-1", "span-1", "tool-1", "agent-1"]),
    });
    const trace = client.startTrace({ name: "Wrapped run" });

    const spanResult = await trace.runSpan({ name: "Load cart", kind: "operation" }, async () => "cart-ready");
    const toolResult = await trace.runTool({ name: "Inventory lookup", toolName: "inventory.lookup" }, async () => ({ available: true }));
    const agentResult = await trace.runAgent({ name: "Support agent", agentName: "Support Agent", agentId: "support-agent" }, async () => "answered");

    expect(spanResult).toBe("cart-ready");
    expect(toolResult).toEqual({ available: true });
    expect(agentResult).toBe("answered");

    await client.flush(trace);
    const firstCall = fetchMock.mock.calls[0] as [string, RequestInit] | undefined;
    if (!firstCall) throw new Error("expected fetch to be called");
    const body = JSON.parse(String(firstCall[1].body));
    expect(body.spans).toEqual([
      expect.objectContaining({ span_id: "root-1", name: "Wrapped run" }),
      expect.objectContaining({ span_id: "span-1", name: "Load cart", kind: "operation", status_code: "ok", duration_ms: 250 }),
      expect.objectContaining({ span_id: "tool-1", name: "Inventory lookup", kind: "tool", status_code: "ok", duration_ms: 40 }),
      expect.objectContaining({ span_id: "agent-1", name: "Support agent", kind: "agent", status_code: "ok", duration_ms: 75 }),
    ]);
    expect(body.tools[0]).toMatchObject({
      span_id: "tool-1",
      tool_name: "inventory.lookup",
      status: "success",
      result: { available: true },
      duration_ms: 40,
    });
    expect(body.agents[0]).toMatchObject({
      span_id: "agent-1",
      agent_id: "support-agent",
      agent_name: "Support Agent",
    });
  });
});
