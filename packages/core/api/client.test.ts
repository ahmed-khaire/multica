import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiClient } from "./client";

describe("ApiClient Gateway evidence bundles", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches Gateway evidence bundles from the backend export endpoint", async () => {
    const fetchMock = vi.fn().mockImplementation(async () =>
      new Response(
        JSON.stringify({
          evidence_bundle: {
            subject: {
              session_id: "",
              incident_id: "incident 1",
              policy_decision_id: "",
              list_limit: 10,
              generated_by: "multica gateway api",
              capture_policy_note: "content visibility follows the workspace Gateway capture policy",
            },
          },
          export: {
            workspace_id: "ws-1",
            subject_id: "incident 1",
            digest_sha256: "abc123",
          },
          llm_calls: { calls: [] },
          policy_decisions: [],
          evidence: [],
          incidents: [],
          provider_risks: [],
          control_mappings: [],
          governance_policies: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const signal = new AbortController().signal;
    const client = new ApiClient("https://api.example.test");
    client.setToken("token-1");
    client.setWorkspaceId("ws-1");

    const response = await client.getGatewayEvidenceBundle({
      incident_id: "incident 1",
      limit: 10,
      signal,
    });

    expect(response.export?.digest_sha256).toBe("abc123");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/gateway/governance/evidence-bundle?incident_id=incident+1&limit=10",
      expect.objectContaining({
        credentials: "include",
        signal,
        headers: expect.objectContaining({
          Authorization: "Bearer token-1",
          "X-Workspace-ID": "ws-1",
        }),
      }),
    );
  });
});
