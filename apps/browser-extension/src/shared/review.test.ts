import { describe, expect, it } from "vitest";
import { buildIssueBody, buildIssueTitle, buildStateAttachment } from "./review";
import type { ReviewCapture } from "./types";

const capture: ReviewCapture = {
  url: "https://example.test/prototype",
  title: "Prototype Review",
  capturedAt: "2026-06-02T10:00:00.000Z",
  viewport: {
    width: 1280,
    height: 720,
    scrollX: 0,
    scrollY: 240,
    devicePixelRatio: 2,
  },
  annotations: [
    {
      id: "a1",
      kind: "pencil",
      rect: { x: 10, y: 20, width: 300, height: 160 },
      pageRect: { x: 10, y: 260, width: 300, height: 160 },
      strokes: [[{ x: 10, y: 20 }, { x: 310, y: 180 }]],
      note: "Move this CTA closer to the screenshot.",
      targetSummary: "button - Start",
    },
    {
      id: "a2",
      kind: "note",
      note: "",
    },
  ],
  domSummary: {
    activeElement: "body",
    selectedText: "",
    visibleText: ["Hero title", "Start"],
  },
};

const workspace = { id: "ws1", slug: "team", name: "Team" };
const project = { id: "p1", name: "Game Prototype" };

describe("review issue formatting", () => {
  it("builds the expected title", () => {
    expect(buildIssueTitle({ project, capture })).toBe(
      "[Review] Game Prototype / Prototype Review - Move this CTA closer to the screenshot.",
    );
  });

  it("includes project, geometry, attachments, and DOM summary in the body", () => {
    const body = buildIssueBody({
      workspace,
      project,
      capture,
      screenshotFilename: "shot.png",
      stateFilename: "state.json",
    });
    expect(body).toContain("Workspace: Team (team, ws1)");
    expect(body).toContain("Project: Game Prototype (p1)");
    expect(body).toContain("shot.png");
    expect(body).toContain("Pencil mark");
    expect(body).toContain("viewport x=10, y=20, w=300, h=160");
    expect(body).toContain("Text note");
    expect(body).toContain("page-level note");
    expect(body).toContain("Hero title");
  });

  it("serializes the state attachment with schema metadata", () => {
    const json = JSON.parse(buildStateAttachment({ workspace, project, capture })) as {
      schema: string;
      project: { id: string };
      capture: { annotations: unknown[] };
    };
    expect(json.schema).toBe("multica.browserReview.v1");
    expect(json.project.id).toBe("p1");
    expect(json.capture.annotations).toHaveLength(2);
  });

  it("describes missing screenshot and DOM summary fallbacks", () => {
    const body = buildIssueBody({
      workspace,
      project,
      capture: { ...capture, domSummary: { visibleText: [] } },
      stateFilename: "state.json",
    });
    expect(body).toContain("Screenshot: not available");
    expect(body).toContain("DOM summary was not available");
    expect(body).toContain("state.json");
  });
});
