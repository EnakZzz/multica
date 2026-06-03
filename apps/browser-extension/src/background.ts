import { DEFAULT_CONFIG, findSelectedProject, findSelectedWorkspace, isConfigReady, normalizeProjects, normalizeServerUrl, upsertRecentSelection } from "./shared/config";
import { buildIssueBody, buildIssueTitle, buildStateAttachment } from "./shared/review";
import type { ExtensionConfig, ReviewCapture, StoredProject, StoredWorkspace, SubmitReviewResult } from "./shared/types";
import { createId } from "./shared/uuid";

const CONFIG_KEY = "multicaReviewConfig";
const TOKEN_KEY = "multicaReviewToken";
const AUTH_STATE_KEY = "multicaReviewAuthState";

interface RuntimeState {
  config: ExtensionConfig;
  hasToken: boolean;
}

interface ApiContext {
  config: ExtensionConfig;
  token: string;
}

type IncomingMessage =
  | { type: "GET_STATE" }
  | { type: "SAVE_CONFIG"; config: Partial<ExtensionConfig> }
  | { type: "START_AUTH"; serverUrl?: string }
  | { type: "AUTH_CALLBACK"; token: string; state: string }
  | { type: "LOGOUT" }
  | { type: "REFRESH_CONTEXT" }
  | { type: "START_ANNOTATION" }
  | { type: "SUBMIT_REVIEW"; capture: ReviewCapture };

async function getConfig(): Promise<ExtensionConfig> {
  const stored = await chrome.storage.local.get({ [CONFIG_KEY]: DEFAULT_CONFIG });
  return { ...DEFAULT_CONFIG, ...stored[CONFIG_KEY] as Partial<ExtensionConfig> };
}

async function saveConfig(next: ExtensionConfig): Promise<ExtensionConfig> {
  const normalized = {
    ...next,
    serverUrl: normalizeServerUrl(next.serverUrl),
    recentSelections: next.recentSelections ?? [],
  };
  await chrome.storage.local.set({ [CONFIG_KEY]: normalized });
  return normalized;
}

async function getToken(): Promise<string> {
  const stored = await chrome.storage.local.get(TOKEN_KEY);
  return typeof stored[TOKEN_KEY] === "string" ? stored[TOKEN_KEY] : "";
}

async function getRuntimeState(): Promise<RuntimeState> {
  const [config, token] = await Promise.all([getConfig(), getToken()]);
  return { config, hasToken: Boolean(token) };
}

function jsonHeaders(ctx: ApiContext, workspaceSlug?: string): Record<string, string> {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${ctx.token}`,
    Accept: "application/json",
  };
  if (workspaceSlug) headers["X-Workspace-Slug"] = workspaceSlug;
  return headers;
}

async function apiFetch<T>(
  ctx: ApiContext,
  path: string,
  init: RequestInit = {},
  workspaceSlug?: string,
): Promise<T> {
  const headers = new Headers(init.headers);
  for (const [key, value] of Object.entries(jsonHeaders(ctx, workspaceSlug))) {
    headers.set(key, value);
  }
  if (init.body && !(init.body instanceof FormData)) headers.set("Content-Type", "application/json");
  const res = await fetch(`${ctx.config.serverUrl}${path}`, { ...init, headers });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text || `Multica API returned ${res.status}`);
  }
  return res.json() as Promise<T>;
}

async function uploadFile(
  ctx: ApiContext,
  file: Blob,
  filename: string,
  issueId: string,
  workspaceSlug: string,
): Promise<void> {
  const form = new FormData();
  form.set("file", file, filename);
  form.set("issue_id", issueId);
  const headers = new Headers(jsonHeaders(ctx, workspaceSlug));
  const res = await fetch(`${ctx.config.serverUrl}/api/upload-file`, {
    method: "POST",
    headers,
    body: form,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text || `Upload failed with ${res.status}`);
  }
}

function dataUrlToBlob(dataUrl: string): Promise<Blob> {
  return fetch(dataUrl).then((res) => res.blob());
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function getApiContext(): Promise<ApiContext> {
  const [config, token] = await Promise.all([getConfig(), getToken()]);
  if (!token) throw new Error("Please sign in to Multica first.");
  if (!isConfigReady(config)) throw new Error("Choose a workspace and project before submitting.");
  return { config, token };
}

async function refreshContext(): Promise<{
  workspaces: StoredWorkspace[];
  projects: StoredProject[];
  config: ExtensionConfig;
}> {
  const config = await getConfig();
  const token = await getToken();
  if (!token) throw new Error("Please sign in to Multica first.");
  const ctx = { config, token };
  const workspaces = await apiFetch<StoredWorkspace[]>(ctx, "/api/workspaces");
  const selectedWorkspace = findSelectedWorkspace(workspaces, config.workspaceSlug) ?? workspaces[0];
  let nextConfig = config;
  if (selectedWorkspace && selectedWorkspace.slug !== config.workspaceSlug) {
    nextConfig = await saveConfig({ ...config, workspaceSlug: selectedWorkspace.slug, projectId: "" });
  }
  const projects = selectedWorkspace
    ? normalizeProjects((await apiFetch<{ projects: StoredProject[] }>(ctx, "/api/projects?", {}, selectedWorkspace.slug)).projects)
    : [];
  if (selectedWorkspace) {
    const selectedProject = findSelectedProject(projects, nextConfig.projectId);
    const nextProjectId = selectedProject?.id ?? projects[0]?.id ?? "";
    if (nextProjectId !== nextConfig.projectId) {
      nextConfig = await saveConfig({ ...nextConfig, projectId: nextProjectId });
    }
  }
  return { workspaces, projects, config: nextConfig };
}

async function startAuth(serverUrl?: string): Promise<RuntimeState> {
  const current = await getConfig();
  const config = await saveConfig({ ...current, serverUrl: serverUrl ?? current.serverUrl });
  const redirectUrl = chrome.runtime.getURL("auth-callback.html");
  const state = createId();
  await chrome.storage.local.set({ [AUTH_STATE_KEY]: state });
  const loginUrl = `${config.serverUrl}/login?cli_callback=${encodeURIComponent(redirectUrl)}&cli_state=${encodeURIComponent(state)}`;
  await chrome.tabs.create({ url: loginUrl });
  return getRuntimeState();
}

async function completeAuthCallback(token: string, returnedState: string): Promise<RuntimeState> {
  const stored = await chrome.storage.local.get(AUTH_STATE_KEY);
  if (!token || !returnedState || returnedState !== stored[AUTH_STATE_KEY]) {
    throw new Error("Authentication callback did not include a valid token.");
  }
  await chrome.storage.local.set({ [TOKEN_KEY]: token });
  await chrome.storage.local.remove(AUTH_STATE_KEY);
  return getRuntimeState();
}

async function getActiveTab(): Promise<{ id: number; windowId: number }> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id || tab.windowId === undefined) throw new Error("No active tab is available.");
  return { id: tab.id, windowId: tab.windowId };
}

async function startAnnotation(tabId?: number): Promise<void> {
  const tab = tabId ? { id: tabId } : await getActiveTab();
  if (!tab.id) throw new Error("No active tab is available.");
  await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    files: ["content.js"],
  });
  await chrome.tabs.sendMessage(tab.id, { type: "MULTICA_REVIEW_START" });
}

async function setContentCaptureMode(tabId: number | undefined, enabled: boolean): Promise<void> {
  if (tabId === undefined) return;
  try {
    await chrome.tabs.sendMessage(tabId, { type: "MULTICA_REVIEW_CAPTURE_MODE", enabled });
    await delay(80);
  } catch {
    // If the content script went away, keep the submit flow best-effort.
  }
}

async function submitReview(
  capture: ReviewCapture,
  senderWindowId?: number,
  senderTabId?: number,
): Promise<SubmitReviewResult> {
  const ctx = await getApiContext();
  const { workspaces, projects } = await refreshContext();
  const workspace = findSelectedWorkspace(workspaces, ctx.config.workspaceSlug);
  const project = findSelectedProject(projects, ctx.config.projectId);
  if (!workspace || !project) throw new Error("Configured workspace or project no longer exists.");

  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  let screenshotFilename: string | undefined = `review-screenshot-${stamp}.png`;
  const stateFilename = `review-state-${stamp}.json`;
  let screenshotBlob: Blob | undefined;
  await setContentCaptureMode(senderTabId, true);
  try {
    const tab = senderWindowId === undefined ? await getActiveTab() : { windowId: senderWindowId };
    const screenshotDataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: "png" });
    screenshotBlob = await dataUrlToBlob(screenshotDataUrl);
  } catch {
    screenshotFilename = undefined;
  } finally {
    await setContentCaptureMode(senderTabId, false);
  }
  const body = buildIssueBody({ workspace, project, capture, screenshotFilename, stateFilename });
  const title = buildIssueTitle({ project, capture });
  const issue = await apiFetch<{ id: string; identifier?: string }>(
    ctx,
    "/api/issues",
    {
      method: "POST",
      body: JSON.stringify({
        title,
        description: body,
        project_id: project.id,
      }),
    },
    workspace.slug,
  );

  const stateBlob = new Blob([buildStateAttachment({ workspace, project, capture })], { type: "application/json" });
  if (screenshotBlob && screenshotFilename) {
    await uploadFile(ctx, screenshotBlob, screenshotFilename, issue.id, workspace.slug);
  }
  await uploadFile(ctx, stateBlob, stateFilename, issue.id, workspace.slug);

  const nextConfig = {
    ...ctx.config,
    recentSelections: upsertRecentSelection(ctx.config.recentSelections, {
      workspaceSlug: workspace.slug,
      workspaceName: workspace.name,
      projectId: project.id,
      projectName: project.name,
    }),
  };
  await saveConfig(nextConfig);

  const issueUrl = `${ctx.config.serverUrl.replace(/\/api$/, "")}/${workspace.slug}/issues/${issue.id}`;
  return { issueId: issue.id, issueIdentifier: issue.identifier, issueUrl };
}

chrome.action.onClicked.addListener((tab) => {
  void startAnnotation(tab.id).catch(() => undefined);
});

chrome.commands.onCommand.addListener((command) => {
  if (command === "toggle-annotation") void startAnnotation().catch(() => undefined);
});

chrome.runtime.onMessage.addListener((message: unknown, sender, sendResponse) => {
  const run = async () => {
    const msg = message as IncomingMessage;
    switch (msg.type) {
      case "GET_STATE":
        return getRuntimeState();
      case "SAVE_CONFIG": {
        const current = await getConfig();
        return saveConfig({ ...current, ...msg.config });
      }
      case "START_AUTH":
        return startAuth(msg.serverUrl);
      case "AUTH_CALLBACK":
        return completeAuthCallback(msg.token, msg.state);
      case "LOGOUT":
        await chrome.storage.local.remove(TOKEN_KEY);
        return getRuntimeState();
      case "REFRESH_CONTEXT":
        return refreshContext();
      case "START_ANNOTATION":
        await startAnnotation();
        return { ok: true };
      case "SUBMIT_REVIEW":
        return submitReview(msg.capture, sender.tab?.windowId, sender.tab?.id);
      default:
        throw new Error("Unsupported extension message");
    }
  };
  run().then(
    (response) => sendResponse({ ok: true, response }),
    (error: unknown) => sendResponse({ ok: false, error: error instanceof Error ? error.message : "Unknown error" }),
  );
  return true;
});
