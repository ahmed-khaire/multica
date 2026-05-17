import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const DEFAULT_E2E_NAME = "E2E User";
const DEFAULT_E2E_WORKSPACE = "e2e-workspace";
const BASE_URL =
  process.env.PLAYWRIGHT_BASE_URL ??
  process.env.FRONTEND_ORIGIN ??
  "http://localhost:3000";

function createE2EEmail() {
  return `e2e+${Date.now()}-${Math.random().toString(36).slice(2)}@multica.ai`;
}

/**
 * Log in as the default E2E user and ensure the workspace exists first.
 * Authenticates via API (send-code -> DB read -> verify-code), then injects
 * the token into localStorage so the browser session is authenticated.
 */
export async function loginAsDefault(page: Page) {
  const api = await createTestApi();
  await loginWithApi(page, api);
}

export async function loginWithApi(page: Page, api: TestApiClient) {
  const token = api.getToken();
  const workspaceId = api.getWorkspaceId();
  if (!token || !workspaceId) {
    throw new Error(
      "Test API client must be logged in and scoped to a workspace before browser login",
    );
  }

  await page.context().addCookies([
    {
      name: "multica_logged_in",
      value: "1",
      url: BASE_URL,
      sameSite: "Lax",
    },
  ]);
  await page.addInitScript(
    ({ token: t, workspaceId }) => {
      localStorage.setItem("multica_token", t);
      localStorage.setItem("multica_workspace_id", workspaceId);
    },
    { token, workspaceId },
  );
  await page.goto("/issues");
  await page.waitForURL("**/issues", { timeout: 10000 });
}

/**
 * Create a TestApiClient logged in as the default E2E user.
 * Call api.cleanup() in afterEach to remove test data created during the test.
 */
export async function createTestApi(): Promise<TestApiClient> {
  const api = new TestApiClient();
  await api.login(createE2EEmail(), DEFAULT_E2E_NAME);
  await api.ensureWorkspace("E2E Workspace", DEFAULT_E2E_WORKSPACE);
  return api;
}

export async function openWorkspaceMenu(page: Page) {
  await page.getByRole("button", { name: /Workspace/ }).first().click();
  // Wait for dropdown to appear
  await page.getByRole("menuitem", { name: "Log out" }).waitFor({
    state: "visible",
  });
}
