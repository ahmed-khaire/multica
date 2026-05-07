import { createServer, type Server } from "node:http";
import { once } from "node:events";
import { test, expect } from "@playwright/test";
import { createTestApi, loginWithApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? `http://localhost:${process.env.PORT ?? "8080"}`;

interface MockUpstream {
  server: Server;
  baseUrl: string;
}

async function startMockOpenAIUpstream(): Promise<MockUpstream> {
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => {
      if (req.method === "GET" && (req.url === "/v1/models" || req.url === "/models")) {
        res.writeHead(200, { "content-type": "application/json" });
        res.end(
          JSON.stringify({
            object: "list",
            data: [
              { id: "mock-gpt-4o-mini", object: "model", owned_by: "mock" },
              { id: "mock-policy-test", object: "model", owned_by: "mock" },
            ],
          }),
        );
        return;
      }

      if (
        req.method === "POST" &&
        (req.url === "/v1/chat/completions" || req.url === "/chat/completions")
      ) {
        const payload = body ? JSON.parse(body) : {};
        const model = payload.model || "mock-gpt-4o-mini";

        if (payload.stream) {
          res.writeHead(200, {
            "content-type": "text/event-stream",
            "cache-control": "no-cache",
            connection: "keep-alive",
          });
          res.write(
            `data: ${JSON.stringify({
              id: "chatcmpl-mock-stream",
              object: "chat.completion.chunk",
              created: Math.floor(Date.now() / 1000),
              model,
              choices: [
                {
                  index: 0,
                  delta: { content: "streamed " },
                  finish_reason: null,
                },
              ],
            })}\n\n`,
          );
          setTimeout(() => {
            res.write(
              `data: ${JSON.stringify({
                id: "chatcmpl-mock-stream",
                object: "chat.completion.chunk",
                created: Math.floor(Date.now() / 1000),
                model,
                choices: [
                  {
                    index: 0,
                    delta: { content: "response" },
                    finish_reason: null,
                  },
                ],
              })}\n\n`,
            );
            res.write(
              `data: ${JSON.stringify({
                id: "chatcmpl-mock-stream",
                object: "chat.completion.chunk",
                created: Math.floor(Date.now() / 1000),
                model,
                choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
              })}\n\n`,
            );
            res.write("data: [DONE]\n\n");
            res.end();
          }, 20);
          return;
        }

        res.writeHead(200, {
          "content-type": "application/json",
          "x-ratelimit-remaining-requests": "99",
        });
        res.end(
          JSON.stringify({
            id: "chatcmpl-mock",
            object: "chat.completion",
            created: Math.floor(Date.now() / 1000),
            model,
            choices: [
              {
                index: 0,
                message: {
                  role: "assistant",
                  content: "mock response from upstream",
                },
                finish_reason: "stop",
              },
            ],
            usage: { prompt_tokens: 7, completion_tokens: 4, total_tokens: 11 },
          }),
        );
        return;
      }

      res.writeHead(404, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "not found", url: req.url }));
    });
  });

  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("Mock upstream did not bind to a TCP port");
  }
  return {
    server,
    baseUrl: `http://127.0.0.1:${address.port}/v1`,
  };
}

async function closeMockUpstream(upstream: MockUpstream | undefined) {
  if (!upstream) return;
  await new Promise<void>((resolve, reject) => {
    upstream.server.close((err) => {
      if (err) reject(err);
      else resolve();
    });
  });
}

async function authedFetch(
  api: TestApiClient,
  path: string,
  init?: RequestInit,
) {
  const token = api.getToken();
  const workspaceId = api.getWorkspaceId();
  if (!token || !workspaceId) {
    throw new Error("Gateway E2E requires an authenticated workspace");
  }
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...((init?.headers as Record<string, string>) ?? {}),
    Authorization: `Bearer ${token}`,
    "X-Workspace-ID": workspaceId,
  };
  const res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  return { res, data };
}

test.describe("Gateway", () => {
  let upstream: MockUpstream;

  test.beforeAll(async () => {
    upstream = await startMockOpenAIUpstream();
  });

  test.afterAll(async () => {
    await closeMockUpstream(upstream);
  });

  test("proxies OpenAI traffic and renders observed sessions", async ({ page }) => {
    const api = await createTestApi();
    await loginWithApi(page, api);

    const backendSlug = `mock-local-${Date.now().toString(36)}`;
    const createBackend = await authedFetch(api, "/api/gateway/backends", {
      method: "POST",
      body: JSON.stringify({
        provider: "local",
        slug: backendSlug,
        display_name: `Mock Local ${backendSlug}`,
        backend_type: "openai_compatible",
        base_url: upstream.baseUrl,
        key: "mock-upstream-key",
        enabled: true,
        set_default: true,
        metadata: { e2e: true },
      }),
    });

    test.skip(
      createBackend.res.status === 500 &&
        createBackend.data?.error === "gateway secret key is not configured",
      "Gateway E2E requires MULTICA_GATEWAY_SECRET_KEY on the backend.",
    );

    expect(createBackend.res.ok).toBe(true);
    const backend = createBackend.data;

    const createKey = await authedFetch(api, "/api/gateway/key", {
      method: "POST",
    });
    expect(createKey.res.ok).toBe(true);
    const gatewayKey = createKey.data;

    const modelsRes = await fetch(`${API_BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${gatewayKey.openai_api_key}` },
    });
    expect(modelsRes.ok).toBe(true);
    const models = await modelsRes.json();
    expect(models.data.map((model: { id: string }) => model.id)).toEqual(
      expect.arrayContaining([
        "mock-gpt-4o-mini",
        `${backend.slug}:mock-gpt-4o-mini`,
      ]),
    );

    const chatRes = await fetch(`${API_BASE}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${gatewayKey.openai_api_key}`,
        "X-Multica-Agent-ID": "gateway-e2e-agent",
        "X-Multica-Task-ID": "gateway-e2e-task",
      },
      body: JSON.stringify({
        model: "mock-gpt-4o-mini",
        messages: [{ role: "user", content: "hello from gateway e2e" }],
      }),
    });
    expect(chatRes.ok).toBe(true);
    const chat = await chatRes.json();
    expect(chat.choices[0].message.content).toBe("mock response from upstream");

    const streamRes = await fetch(`${API_BASE}/v1/chat/completions`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${gatewayKey.openai_api_key}`,
        "X-Multica-Agent-ID": "gateway-e2e-agent",
        "X-Multica-Task-ID": "gateway-e2e-stream",
      },
      body: JSON.stringify({
        model: `${backend.slug}:mock-gpt-4o-mini`,
        stream: true,
        messages: [{ role: "user", content: "stream please" }],
      }),
    });
    expect(streamRes.ok).toBe(true);
    const streamText = await streamRes.text();
    expect(streamText).toContain("streamed");
    expect(streamText).toContain("[DONE]");

    await expect
      .poll(async () => {
        const overview = await authedFetch(api, "/api/gateway/overview?since=24h");
        return overview.data.summary.request_count;
      })
      .toBeGreaterThanOrEqual(2);

    const sessions = await authedFetch(
      api,
      "/api/gateway/sessions?since=24h&limit=10",
    );
    expect(sessions.data.sessions.length).toBeGreaterThanOrEqual(1);

    const calls = await authedFetch(
      api,
      "/api/gateway/llm-calls?since=24h&limit=10",
    );
    expect(calls.data.calls.length).toBeGreaterThanOrEqual(2);

    await page.goto("/gateway");
    await expect(
      page.getByRole("heading", { name: "Gateway", exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("table", { name: "Gateway sessions" })).toBeVisible();
    await expect(page.getByText(backend.slug).first()).toBeVisible();
    await expect(page.getByText("mock-gpt-4o-mini").first()).toBeVisible();
    await page
      .getByRole("row", {
        name: new RegExp(`${backend.slug} mock-gpt-4o-mini 1 11`),
      })
      .getByRole("button")
      .click();
    await expect(page.getByText("mock response from upstream").first()).toBeVisible();
  });
});
