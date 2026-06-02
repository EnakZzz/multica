import type { ExtensionConfig, RecentSelection, StoredProject, StoredWorkspace } from "./types";

export const DEFAULT_CONFIG: ExtensionConfig = {
  serverUrl: "http://127.0.0.1:8081",
  workspaceSlug: "",
  projectId: "",
  recentSelections: [],
};

export function normalizeServerUrl(input: string): string {
  const raw = input.trim().replace(/\/+$/, "");
  if (!raw) return DEFAULT_CONFIG.serverUrl;
  const parsed = new URL(raw);
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("Multica server URL must start with http:// or https://");
  }
  parsed.pathname = parsed.pathname.replace(/\/+$/, "");
  parsed.search = "";
  parsed.hash = "";
  return parsed.toString().replace(/\/+$/, "");
}

export function isConfigReady(config: ExtensionConfig): boolean {
  return Boolean(config.serverUrl && config.workspaceSlug && config.projectId);
}

export function findSelectedWorkspace(
  workspaces: StoredWorkspace[],
  slug: string,
): StoredWorkspace | undefined {
  return workspaces.find((workspace) => workspace.slug === slug);
}

export function findSelectedProject(
  projects: StoredProject[],
  projectId: string,
): StoredProject | undefined {
  return projects.find((project) => project.id === projectId);
}

export function normalizeProjects(projects: Array<StoredProject | (Partial<StoredProject> & { title?: string })>): StoredProject[] {
  return projects
    .filter((project): project is StoredProject | (Partial<StoredProject> & { id: string; title?: string }) => {
      return typeof project.id === "string" && project.id.length > 0;
    })
    .map((project) => ({
      ...project,
      name: project.name || project.title || project.id,
    }));
}

export function upsertRecentSelection(
  current: RecentSelection[],
  selection: Omit<RecentSelection, "usedAt">,
  now = new Date(),
): RecentSelection[] {
  const item: RecentSelection = {
    ...selection,
    usedAt: now.toISOString(),
  };
  return [
    item,
    ...current.filter(
      (recent) =>
        recent.workspaceSlug !== selection.workspaceSlug ||
        recent.projectId !== selection.projectId,
    ),
  ].slice(0, 6);
}
