import type { IdGenerator } from "./types";

export const multicaHeaderNames = {
  traceId: "X-Multica-Trace-ID",
  sessionId: "X-Multica-Session-ID",
  parentSpanId: "X-Multica-Parent-Span-ID",
  appId: "X-Multica-App-ID",
  serviceName: "X-Multica-Service-Name",
  environment: "X-Multica-Environment",
  deploymentId: "X-Multica-Deployment-ID",
  aiSystemId: "X-Multica-AI-System-ID",
  agentId: "X-Multica-Agent-ID",
  workflowId: "X-Multica-Workflow-ID",
  endUserId: "X-Multica-End-User-ID",
  useCase: "X-Multica-Use-Case",
  dataDomains: "X-Multica-Data-Domains",
  toolHint: "X-Multica-Tool-Hint",
} as const;

export interface TraceContext {
  traceId: string;
  sessionId: string;
  rootSpanId: string;
}

export function defaultIdGenerator(): string {
  if (globalThis.crypto?.randomUUID) {
    return globalThis.crypto.randomUUID();
  }
  return `obs_${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`;
}

export function createTraceContext(generator: IdGenerator = defaultIdGenerator): TraceContext {
  return {
    traceId: generator(),
    sessionId: generator(),
    rootSpanId: generator(),
  };
}
