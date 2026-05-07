import { test, expect } from "@playwright/test";
import { loginWithApi, createTestApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Issues", () => {
  let api: TestApiClient;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    await loginWithApi(page, api);
  });

  test.afterEach(async () => {
    await api?.cleanup();
  });

  test("issues page loads with board view", async ({ page }) => {
    await expect(page.locator("text=Issues").first()).toBeVisible();
    await expect(page.locator("text=No issues yet")).toBeVisible();

    await expect(page.locator("text=Create an issue to get started.")).toBeVisible();
  });

  test("can switch between board and list view", async ({ page }) => {
    await api.createIssue("E2E View Toggle " + Date.now());
    await page.reload();
    await expect(page.locator("text=Issues").first()).toBeVisible();

    // Switch to list view
    await page.getByRole("button", { name: "Change view from board" }).click();
    await page.getByRole("menuitem", { name: "List" }).click();
    await expect(page.getByRole("button", { name: "Change view from list" })).toBeVisible();

    // Switch back to board view
    await page.getByRole("button", { name: "Change view from list" }).click();
    await page.getByRole("menuitem", { name: "Board" }).click();
    await expect(page.getByRole("button", { name: "Change view from board" })).toBeVisible();
  });

  test("can create a new issue", async ({ page }) => {
    await page.click("text=New Issue");

    const title = "E2E Created " + Date.now();
    await page.getByRole("textbox", { name: "Issue title" }).fill(title);
    await page.click("text=Create Issue");

    // New issue should appear on the page
    await expect(page.locator(`text=${title}`).first()).toBeVisible({
      timeout: 10000,
    });
  });

  test("can navigate to issue detail page", async ({ page }) => {
    // Create a known issue via API so the test controls its own fixture
    const issue = await api.createIssue("E2E Detail Test " + Date.now());

    // Reload to see the new issue
    await page.reload();
    await expect(page.locator("text=Issues").first()).toBeVisible();

    // Navigate to the issue detail
    const issueLink = page.locator(`a[href="/issues/${issue.id}"]`);
    await expect(issueLink).toBeVisible({ timeout: 5000 });
    await issueLink.click();

    await page.waitForURL(/\/issues\/[\w-]+/);

    // Should show Properties panel
    await expect(page.locator("text=Properties")).toBeVisible();
    // Should show breadcrumb link back to Issues
    await expect(
      page.locator("a", { hasText: "Issues" }).first(),
    ).toBeVisible();
  });

  test("can cancel issue creation", async ({ page }) => {
    await page.click("text=New Issue");

    await expect(
      page.getByRole("textbox", { name: "Issue title" }),
    ).toBeVisible();

    await page.keyboard.press("Escape");

    await expect(
      page.getByRole("textbox", { name: "Issue title" }),
    ).not.toBeVisible();
    await expect(page.locator("text=New Issue")).toBeVisible();
  });
});
