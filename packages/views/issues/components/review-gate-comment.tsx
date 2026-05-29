"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import type { Attachment } from "@multica/core/types";
import { ReadonlyContent } from "../../editor";

type ReviewGateStatus = "pass" | "fail";

interface ReviewGateFinding {
  severity: string;
  title: string;
  details: string;
}

interface ReviewGateResult {
  status: ReviewGateStatus;
  summary: string;
  findings: ReviewGateFinding[];
  checked_against: string[];
}

interface ReviewGatePayload {
  review_gate: ReviewGateResult;
}

export interface ReviewGatePresentation {
  markdown: string;
  isDisplayZh: boolean;
  review?: ReviewGateResult;
}

export function getReviewGatePresentation(
  content: string | undefined,
  displayContentZh?: string | null,
): ReviewGatePresentation | null {
  const review = parseReviewGateContent(content ?? "");
  const display = displayContentZh?.trim();
  if (display) {
    const displayReview = parseReviewGateDisplayZh(display, review);
    return { markdown: display, isDisplayZh: true, review: displayReview ?? review ?? undefined };
  }
  if (!review) return null;
  return { markdown: formatReviewGateMarkdown(review), isDisplayZh: false, review };
}

export function isReviewGateComment(
  content: string | undefined,
  displayContentZh?: string | null,
): boolean {
  return getReviewGatePresentation(content, displayContentZh) !== null;
}

export function ReviewGateCommentContent({
  content,
  displayContentZh,
  attachments,
}: {
  content: string;
  displayContentZh?: string | null;
  attachments?: Attachment[];
}) {
  const presentation = getReviewGatePresentation(content, displayContentZh);
  if (!presentation) {
    return null;
  }
  const review = presentation.review;
  if (!review) {
    return (
      <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
        <ReadonlyContent content={presentation.markdown} attachments={attachments} />
      </div>
    );
  }
  return (
    <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
      {review.findings.length === 0 && (
        <ReviewGateHeader review={review} isDisplayZh={presentation.isDisplayZh} />
      )}
      {review.summary && review.findings.length === 0 && (
        <p className="mb-2 text-sm text-foreground/85">{review.summary}</p>
      )}
      {review.findings.length > 0 && (
        <div className="space-y-2">
          {review.findings.map((finding, index) => (
            <div key={`${finding.severity}-${index}`} className="rounded border border-border/50 bg-background/60 p-2">
              <div className="mb-1 flex items-center gap-2">
                <Badge
                  variant={finding.severity === "blocker" ? "destructive" : "outline"}
                  className={cn(
                    finding.severity === "major" && "border-amber-500/40 text-amber-700 dark:text-amber-300",
                    finding.severity === "minor" && "text-muted-foreground",
                  )}
                >
                  {finding.severity}
                </Badge>
                <span className="font-medium">{finding.title || finding.details}</span>
              </div>
              {finding.details && finding.details !== finding.title && (
                <p className="text-sm text-muted-foreground">{finding.details}</p>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ReviewGateHeader({
  review,
  isDisplayZh,
}: {
  review: ReviewGateResult;
  isDisplayZh: boolean;
}) {
  const failed = review.status === "fail";
  return (
    <div className="mb-2 flex items-center gap-2">
      <Badge variant={failed ? "destructive" : "secondary"}>
        {isDisplayZh ? (failed ? "评审未通过" : "评审通过") : (failed ? "Review failed" : "Review passed")}
      </Badge>
      <span className="text-xs text-muted-foreground">review_gate</span>
    </div>
  );
}

function parseReviewGateContent(content: string): ReviewGateResult | null {
  const source = stripJsonFence(content.trim());
  for (let start = source.indexOf("{"); start >= 0; ) {
    try {
      const raw = JSON.parse(source.slice(start)) as Partial<ReviewGatePayload>;
      const review = normalizeReviewGate(raw.review_gate);
      if (review) return review;
    } catch {
      // Keep scanning: legacy outputs may contain prose before the JSON.
    }
    const next = source.indexOf("{", start + 1);
    if (next < 0) break;
    start = next;
  }
  return null;
}

function parseReviewGateDisplayZh(display: string, canonical: ReviewGateResult | null): ReviewGateResult | null {
  const lines = display.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
  if (lines.length === 0) return null;

  const findingMarkerIndex = lines.findIndex((line) => /^发现[:：]?$/.test(line));
  const summaryLines = findingMarkerIndex >= 0 ? lines.slice(0, findingMarkerIndex) : lines.filter((line) => !isDisplayFindingLine(line));
  const findingLines = findingMarkerIndex >= 0 ? lines.slice(findingMarkerIndex + 1) : lines.filter(isDisplayFindingLine);
  const statusLine = summaryLines[0] ?? "";
  const summary = summaryLines
    .filter((line, index) => !(index === 0 && isDisplayStatusLine(line)))
    .join("\n\n")
    .trim();
  const findings = findingLines.map(parseDisplayFinding).filter((f): f is ReviewGateFinding => f !== null);
  if (!summary && findings.length === 0) return null;

  return {
    status: canonical?.status ?? inferDisplayStatus(statusLine) ?? "fail",
    summary: summary || canonical?.summary || "",
    findings: findings.length > 0 ? findings : (canonical?.findings ?? []),
    checked_against: canonical?.checked_against ?? [],
  };
}

function isDisplayStatusLine(line: string): boolean {
  return /评审/.test(line) && /(通过|未通过)/.test(line);
}

function inferDisplayStatus(line: string): ReviewGateStatus | null {
  if (/未通过|失败/.test(line)) return "fail";
  if (/已通过|通过/.test(line)) return "pass";
  return null;
}

function isDisplayFindingLine(line: string): boolean {
  return /^-\s*\[(?:blocker|major|minor)\]/i.test(line);
}

function parseDisplayFinding(line: string): ReviewGateFinding | null {
  const match = line.match(/^-\s*\[(blocker|major|minor)\]\s*(.+)$/i);
  if (!match) return null;
  const body = match[2]?.trim() ?? "";
  if (!body) return null;
  const splitAt = findFindingSeparator(body);
  const title = splitAt >= 0 ? body.slice(0, splitAt).trim() : body;
  const details = splitAt >= 0 ? body.slice(splitAt + 1).replace(/^[:：]\s*/, "").trim() : "";
  return {
    severity: match[1]?.toLowerCase() ?? "major",
    title,
    details,
  };
}

function findFindingSeparator(body: string): number {
  const ascii = body.indexOf(": ");
  const fullWidth = body.indexOf("：");
  if (ascii < 0) return fullWidth;
  if (fullWidth < 0) return ascii;
  return Math.min(ascii, fullWidth);
}

function stripJsonFence(content: string): string {
  const match = content.match(/^```(?:json)?\s*([\s\S]*?)\s*```$/i);
  return match?.[1]?.trim() ?? content;
}

function normalizeReviewGate(raw: unknown): ReviewGateResult | null {
  if (!raw || typeof raw !== "object") return null;
  const obj = raw as Record<string, unknown>;
  const status = String(obj.status ?? "").trim().toLowerCase();
  if (status !== "pass" && status !== "fail") return null;
  const findings = Array.isArray(obj.findings)
    ? obj.findings.map(normalizeFinding).filter((f): f is ReviewGateFinding => f !== null)
    : [];
  const checkedAgainst = Array.isArray(obj.checked_against)
    ? obj.checked_against.map((item) => String(item).trim()).filter(Boolean)
    : [];
  return {
    status,
    summary: String(obj.summary ?? "").trim(),
    findings,
    checked_against: checkedAgainst,
  };
}

function normalizeFinding(raw: unknown): ReviewGateFinding | null {
  if (!raw || typeof raw !== "object") return null;
  const obj = raw as Record<string, unknown>;
  const title = String(obj.title ?? "").trim();
  const details = String(obj.details ?? "").trim();
  if (!title && !details) return null;
  const severity = String(obj.severity ?? "major").trim().toLowerCase();
  return {
    severity: ["blocker", "major", "minor"].includes(severity) ? severity : "major",
    title,
    details,
  };
}

function formatReviewGateMarkdown(review: ReviewGateResult): string {
  const lines = [
    `Review gate ${review.status === "pass" ? "passed" : "failed"}.`,
  ];
  if (review.summary) {
    lines.push("", review.summary);
  }
  if (review.findings.length > 0) {
    lines.push("", "Findings:");
    for (const finding of review.findings) {
      const title = finding.title || finding.details;
      const suffix = finding.details && finding.details !== title ? `: ${finding.details}` : "";
      lines.push(`- [${finding.severity}] ${title}${suffix}`);
    }
  }
  if (review.checked_against.length > 0) {
    lines.push("", "Checked against:");
    for (const item of review.checked_against) {
      lines.push(`- ${item}`);
    }
  }
  return lines.join("\n");
}
