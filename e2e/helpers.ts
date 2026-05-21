import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";

const DEFAULT_E2E_NAME = "E2E User";
const DEFAULT_E2E_EMAIL = "e2e@multica.ai";
const DEFAULT_E2E_WORKSPACE = "e2e-workspace";
const USE_DEV_VERIFICATION_CODE = process.env.MULTICA_E2E_USE_DEV_CODE === "true";

let defaultSessionPromise: Promise<{ token: string; workspaceSlug: string }> | null = null;

async function createDefaultSession() {
  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const workspace = await api.ensureWorkspace(
    "E2E Workspace",
    DEFAULT_E2E_WORKSPACE,
  );
  const token = api.getToken();
  if (!token) throw new Error("Default E2E login did not return a token");
  return { token, workspaceSlug: workspace.slug };
}

function getDefaultSession() {
  if (!USE_DEV_VERIFICATION_CODE) return createDefaultSession();
  defaultSessionPromise ??= createDefaultSession();
  return defaultSessionPromise;
}

/**
 * Log in as the default E2E user and ensure the workspace exists first.
 * Authenticates via API (send-code → DB read → verify-code), then injects
 * the token into localStorage so the browser session is authenticated.
 *
 * Returns the E2E workspace slug so callers can build workspace-scoped URLs.
 */
export async function loginAsDefault(page: Page): Promise<string> {
  const { token, workspaceSlug } = await getDefaultSession();
  await page.goto("/login");
  await page.evaluate((t) => {
    localStorage.setItem("multica_token", t);
  }, token);
  await page.goto(`/${workspaceSlug}/issues`);
  await page.waitForURL("**/issues", { timeout: 10000 });
  return workspaceSlug;
}

/**
 * Create a TestApiClient logged in as the default E2E user.
 * Call api.cleanup() in afterEach to remove test data created during the test.
 */
export async function createTestApi(): Promise<TestApiClient> {
  if (USE_DEV_VERIFICATION_CODE) {
    const { token, workspaceSlug } = await getDefaultSession();
    const api = new TestApiClient();
    api.setToken(token);
    api.setWorkspaceSlug(workspaceSlug);
    return api;
  }

  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  await api.ensureWorkspace("E2E Workspace", DEFAULT_E2E_WORKSPACE);
  return api;
}

export async function openWorkspaceMenu(page: Page) {
  await page.getByRole("button", { name: /E2E Workspace/ }).first().click();
  await page.getByText("Log out").waitFor({ state: "visible" });
}
