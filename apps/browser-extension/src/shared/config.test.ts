import { describe, expect, it } from "vitest";
import { DEFAULT_CONFIG, isConfigReady, normalizeProjects, normalizeServerUrl, upsertRecentSelection } from "./config";

describe("extension config", () => {
  it("normalizes http and https server URLs", () => {
    expect(normalizeServerUrl(" https://app.multica.ai/// ")).toBe("https://app.multica.ai");
    expect(normalizeServerUrl("")).toBe(DEFAULT_CONFIG.serverUrl);
  });

  it("rejects unsupported URL protocols", () => {
    expect(() => normalizeServerUrl("chrome://extensions")).toThrow(/http/);
  });

  it("requires server, workspace, and project before submit", () => {
    expect(isConfigReady({ ...DEFAULT_CONFIG, workspaceSlug: "team", projectId: "p1" })).toBe(true);
    expect(isConfigReady({ ...DEFAULT_CONFIG, workspaceSlug: "team" })).toBe(false);
  });

  it("deduplicates recent selections by workspace and project", () => {
    const first = upsertRecentSelection([], {
      workspaceSlug: "team",
      workspaceName: "Team",
      projectId: "p1",
      projectName: "Project",
    }, new Date("2026-01-01T00:00:00.000Z"));
    const second = upsertRecentSelection(first, {
      workspaceSlug: "team",
      workspaceName: "Team",
      projectId: "p1",
      projectName: "Project Renamed",
    }, new Date("2026-01-02T00:00:00.000Z"));
    expect(second).toHaveLength(1);
    expect(second[0]?.projectName).toBe("Project Renamed");
    expect(second[0]?.usedAt).toBe("2026-01-02T00:00:00.000Z");
  });

  it("normalizes API project title fields into display names", () => {
    expect(normalizeProjects([
      { id: "p1", title: "Lost Pet" },
      { id: "p2", name: "Protocol Tests" },
    ])).toEqual([
      { id: "p1", title: "Lost Pet", name: "Lost Pet" },
      { id: "p2", name: "Protocol Tests" },
    ]);
  });
});
