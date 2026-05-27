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
}

export function getReviewGatePresentation(
  content: string | undefined,
  displayContentZh?: string | null,
): ReviewGatePresentation | null {
  const display = displayContentZh?.trim();
  if (display) {
    return { markdown: display, isDisplayZh: true };
  }
  const review = parseReviewGateContent(content ?? "");
  if (!review) return null;
  return { markdown: formatReviewGateMarkdown(review), isDisplayZh: false };
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
  if (presentation.isDisplayZh) {
    return (
      <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
        <ReadonlyContent content={presentation.markdown} attachments={attachments} />
      </div>
    );
  }

  const review = parseReviewGateContent(content);
  if (!review) {
    return (
      <ReadonlyContent content={presentation.markdown} attachments={attachments} />
    );
  }
  const failed = review.status === "fail";
  return (
    <div className="rounded-md border border-border/60 bg-muted/20 px-3 py-2">
      <div className="mb-2 flex items-center gap-2">
        <Badge variant={failed ? "destructive" : "secondary"}>
          {failed ? "Review failed" : "Review passed"}
        </Badge>
        <span className="text-xs text-muted-foreground">review_gate</span>
      </div>
      {review.summary && (
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
