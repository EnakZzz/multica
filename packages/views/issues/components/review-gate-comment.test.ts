import { describe, expect, it } from "vitest";
import {
  getReviewGatePresentation,
  isReviewGateComment,
} from "./review-gate-comment";

describe("review gate comment presentation", () => {
  const rawReviewGate = JSON.stringify({
    review_gate: {
      status: "fail",
      summary: "The AP budget can deadlock.",
      findings: [
        {
          severity: "blocker",
          title: "AP deadlock",
          details: "Collecting all optional memories exhausts AP.",
        },
      ],
      checked_against: ["issue acceptance criteria"],
    },
  });

  it("uses display_content_zh when present", () => {
    const presentation = getReviewGatePresentation(
      rawReviewGate,
      "代码评审未通过。\n\n发现：\n- [blocker] AP 会耗尽",
    );

    expect(presentation?.isDisplayZh).toBe(true);
    expect(presentation?.markdown).toContain("代码评审未通过");
  });

  it("formats legacy review_gate JSON when no Chinese display copy exists", () => {
    const presentation = getReviewGatePresentation(rawReviewGate, null);

    expect(presentation?.isDisplayZh).toBe(false);
    expect(presentation?.markdown).toContain("Review gate failed");
    expect(presentation?.markdown).toContain("[blocker] AP deadlock");
  });

  it("recognizes fenced review_gate JSON but ignores ordinary markdown", () => {
    expect(isReviewGateComment("```json\n" + rawReviewGate + "\n```")).toBe(true);
    expect(isReviewGateComment("普通评论，不是 JSON")).toBe(false);
  });
});
