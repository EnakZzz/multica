import type { ReviewAnnotation, ReviewCapture, StoredProject, StoredWorkspace } from "./types";

function hostnameFromUrl(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return "page";
  }
}

function cleanInlineText(value: string, fallback: string): string {
  const cleaned = value.replace(/\s+/g, " ").trim();
  return cleaned || fallback;
}

export function summarizeAnnotation(annotation: ReviewAnnotation | undefined): string {
  if (!annotation) return "page feedback";
  return cleanInlineText(annotation.note, annotation.targetSummary || "page feedback").slice(0, 80);
}

export function buildIssueTitle(params: {
  project: Pick<StoredProject, "name">;
  capture: Pick<ReviewCapture, "title" | "url" | "annotations">;
}): string {
  const page = cleanInlineText(params.capture.title, hostnameFromUrl(params.capture.url));
  const summary = summarizeAnnotation(params.capture.annotations[0]);
  return `[Review] ${params.project.name} / ${page} - ${summary}`.slice(0, 180);
}

function formatRect(annotation: ReviewAnnotation): string {
  if (!annotation.rect) return "page-level note";
  const r = annotation.rect;
  return `viewport x=${Math.round(r.x)}, y=${Math.round(r.y)}, w=${Math.round(r.width)}, h=${Math.round(r.height)}`;
}

function formatAnnotationKind(annotation: ReviewAnnotation): string {
  if (annotation.kind === "note") return "Text note";
  return "Pencil mark";
}

export function buildIssueBody(params: {
  workspace: Pick<StoredWorkspace, "id" | "slug" | "name">;
  project: Pick<StoredProject, "id" | "name">;
  capture: ReviewCapture;
  screenshotFilename?: string;
  stateFilename: string;
}): string {
  const { workspace, project, capture } = params;
  const lines = [
    "## Browser review",
    "",
    `- Workspace: ${workspace.name} (${workspace.slug}, ${workspace.id})`,
    `- Project: ${project.name} (${project.id})`,
    `- Page: ${capture.title || hostnameFromUrl(capture.url)}`,
    `- URL: ${capture.url}`,
    `- Captured: ${capture.capturedAt}`,
    `- Viewport: ${capture.viewport.width}x${capture.viewport.height}, scroll ${capture.viewport.scrollX},${capture.viewport.scrollY}, dpr ${capture.viewport.devicePixelRatio}`,
    "",
    "## Attachments",
    "",
    params.screenshotFilename
      ? `- ${params.screenshotFilename}: visible viewport screenshot`
      : "- Screenshot: not available; the browser did not allow viewport capture.",
    `- ${params.stateFilename}: structured page state and annotation geometry`,
    "",
    "## Annotations",
    "",
  ];

  capture.annotations.forEach((annotation, index) => {
    lines.push(
      `### ${index + 1}. ${summarizeAnnotation(annotation)}`,
      "",
      `- Type: ${formatAnnotationKind(annotation)}`,
      `- Region: ${formatRect(annotation)}`,
      annotation.strokes?.length ? `- Pencil strokes: ${annotation.strokes.length}` : "- Pencil strokes: none",
      annotation.targetSummary ? `- Target: ${annotation.targetSummary}` : "- Target: not available",
      "",
      annotation.note || "_No note provided._",
      "",
    );
  });

  if (capture.domSummary.visibleText.length > 0) {
    lines.push("## Page state summary", "");
    for (const text of capture.domSummary.visibleText.slice(0, 12)) {
      lines.push(`- ${text}`);
    }
    lines.push("");
  } else {
    lines.push("## Page state summary", "", "_DOM summary was not available for this page._", "");
  }

  return lines.join("\n").trimEnd();
}

export function buildStateAttachment(params: {
  workspace: Pick<StoredWorkspace, "id" | "slug" | "name">;
  project: Pick<StoredProject, "id" | "name">;
  capture: ReviewCapture;
}): string {
  return JSON.stringify(
    {
      schema: "multica.browserReview.v1",
      workspace: params.workspace,
      project: params.project,
      capture: params.capture,
    },
    null,
    2,
  );
}
