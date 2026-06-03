import type { ExtensionConfig, StoredProject, StoredWorkspace } from "../shared/types";

type RuntimeResponse<T> = { ok?: boolean; response?: T; error?: string };

interface ContextResponse {
  config: ExtensionConfig;
  workspaces: StoredWorkspace[];
  projects: StoredProject[];
}

interface ViewRefs {
  serverUrl: HTMLInputElement;
  workspace: HTMLSelectElement;
  project: HTMLSelectElement;
  status: HTMLElement;
  login: HTMLButtonElement;
  refresh: HTMLButtonElement;
  save?: HTMLButtonElement;
  start?: HTMLButtonElement;
  logout?: HTMLButtonElement;
}

async function send<T>(message: unknown): Promise<T> {
  const result = await chrome.runtime.sendMessage(message) as RuntimeResponse<T>;
  if (!result.ok) throw new Error(result.error ?? "Extension request failed");
  return result.response as T;
}

function option(value: string, label: string) {
  const item = document.createElement("option");
  item.value = value;
  item.textContent = label;
  return item;
}

function setStatus(refs: ViewRefs, message: string, error = false) {
  refs.status.textContent = message;
  refs.status.style.color = error ? "#b91c1c" : "#4b5563";
}

function setSignedIn(refs: ViewRefs, signedIn: boolean) {
  refs.login.hidden = signedIn;
  if (refs.logout) refs.logout.hidden = !signedIn;
}

function renderSelects(refs: ViewRefs, context: ContextResponse) {
  refs.serverUrl.value = context.config.serverUrl;
  refs.workspace.replaceChildren();
  refs.project.replaceChildren();
  if (context.workspaces.length === 0) {
    refs.workspace.append(option("", "No workspaces available"));
  } else {
    for (const workspace of context.workspaces) {
      refs.workspace.append(option(workspace.slug, workspace.name || workspace.slug));
    }
    refs.workspace.value = context.config.workspaceSlug || context.workspaces[0]?.slug || "";
  }
  if (context.projects.length === 0) {
    refs.project.append(option("", "No projects available"));
  } else {
    for (const project of context.projects) {
      refs.project.append(option(project.id, project.name || project.title || project.id));
    }
    refs.project.value = context.config.projectId || context.projects[0]?.id || "";
  }
}

async function saveFromControls(refs: ViewRefs): Promise<ExtensionConfig> {
  return send<ExtensionConfig>({
    type: "SAVE_CONFIG",
    config: {
      serverUrl: refs.serverUrl.value,
      workspaceSlug: refs.workspace.value,
      projectId: refs.project.value,
    },
  });
}

async function refresh(refs: ViewRefs, options: { saveControls?: boolean } = {}) {
  setStatus(refs, "Refreshing...");
  if (options.saveControls ?? true) {
    await saveFromControls(refs);
  }
  const context = await send<ContextResponse>({ type: "REFRESH_CONTEXT" });
  renderSelects(refs, context);
  setStatus(refs, "Workspace and project list refreshed.");
}

export async function mountReviewExtensionUi(refs: ViewRefs) {
  try {
    const state = await send<{ config: ExtensionConfig; hasToken: boolean }>({ type: "GET_STATE" });
    refs.serverUrl.value = state.config.serverUrl;
    setSignedIn(refs, state.hasToken);
    if (state.hasToken) {
      await refresh(refs, { saveControls: false });
    } else {
      refs.workspace.append(option("", "Sign in first"));
      refs.project.append(option("", "Sign in first"));
      setStatus(refs, "Sign in to choose a project.");
    }
  } catch (err) {
    setStatus(refs, err instanceof Error ? err.message : "Failed to load settings", true);
  }

  refs.login.addEventListener("click", async () => {
    try {
      setStatus(refs, "Opening Multica login...");
      const state = await send<{ config: ExtensionConfig; hasToken: boolean }>({
        type: "START_AUTH",
        serverUrl: refs.serverUrl.value,
      });
      setSignedIn(refs, state.hasToken);
      if (state.hasToken) {
        await refresh(refs, { saveControls: false });
        setStatus(refs, "Signed in.");
      } else {
        setStatus(refs, "Login tab opened. After signing in, reopen this panel and refresh.");
      }
    } catch (err) {
      setStatus(refs, err instanceof Error ? err.message : "Sign in failed", true);
    }
  });

  refs.refresh.addEventListener("click", () => {
    void refresh(refs).catch((err: unknown) => {
      setStatus(refs, err instanceof Error ? err.message : "Refresh failed", true);
    });
  });

  refs.workspace.addEventListener("change", () => {
    refs.project.value = "";
    void refresh(refs).catch((err: unknown) => {
      setStatus(refs, err instanceof Error ? err.message : "Workspace switch failed", true);
    });
  });

  refs.project.addEventListener("change", async () => {
    try {
      await saveFromControls(refs);
      setStatus(refs, "Project saved.");
    } catch (err) {
      setStatus(refs, err instanceof Error ? err.message : "Project save failed", true);
    }
  });

  refs.serverUrl.addEventListener("blur", async () => {
    try {
      await saveFromControls(refs);
      setStatus(refs, "Settings saved.");
    } catch (err) {
      setStatus(refs, err instanceof Error ? err.message : "Settings save failed", true);
    }
  });

  refs.start?.addEventListener("click", async () => {
    try {
      await saveFromControls(refs);
      setStatus(refs, "Starting annotation mode...");
      await send({ type: "START_ANNOTATION" });
      window.close();
    } catch (err) {
      setStatus(refs, err instanceof Error ? err.message : "Could not start annotation", true);
    }
  });

  refs.logout?.addEventListener("click", async () => {
    try {
      await send({ type: "LOGOUT" });
      setSignedIn(refs, false);
      refs.workspace.replaceChildren(option("", "Sign in first"));
      refs.project.replaceChildren(option("", "Sign in first"));
      setStatus(refs, "Signed out.");
    } catch (err) {
      setStatus(refs, err instanceof Error ? err.message : "Sign out failed", true);
    }
  });
}
