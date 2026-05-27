package service

import (
	"strings"
	"testing"
)

func TestParseReviewGateOutputAcceptsPass(t *testing.T) {
	review, err := parseReviewGateOutput(`{
		"review_gate": {
			"status": "pass",
			"summary": "Implementation satisfies the spec.",
			"findings": [],
			"checked_against": ["accepted spec", "changed files"]
		}
	}`)
	if err != nil {
		t.Fatalf("parseReviewGateOutput returned error: %v", err)
	}
	if review.Status != "pass" || review.Summary == "" || len(review.CheckedAgainst) != 2 {
		t.Fatalf("review = %#v", review)
	}
}

func TestParseReviewGateOutputAcceptsHumanDisplayZh(t *testing.T) {
	parsed, err := parseReviewGateOutputWithDisplay(`{
		"review_gate": {
			"status": "fail",
			"summary": "AP deadlock blocks completion.",
			"findings": [
				{ "severity": "blocker", "title": "AP deadlock", "details": "No AP restoration exists." }
			],
			"checked_against": ["acceptance criteria"]
		},
		"human_display_zh": {
			"summary": "AP 会在收集死亡阶段记忆前耗尽。",
			"findings": [
				{ "severity": "blocker", "title": "AP 死锁", "details": "没有 AP 恢复机制。" }
			]
		}
	}`)
	if err != nil {
		t.Fatalf("parseReviewGateOutputWithDisplay returned error: %v", err)
	}
	if parsed.Review.Status != "fail" || parsed.Review.Summary == "" {
		t.Fatalf("review = %#v", parsed.Review)
	}
	if parsed.HumanDisplayZh.Summary == "" || parsed.HumanDisplayZh.Findings[0].Title != "AP 死锁" {
		t.Fatalf("human display = %#v", parsed.HumanDisplayZh)
	}
}

func TestFormatReviewGateCanonicalCommentOmitsHumanDisplayZh(t *testing.T) {
	comment := formatReviewGateCanonicalComment(reviewGateResult{
		Status:  "fail",
		Summary: "English canonical summary.",
		Findings: []reviewGateFinding{{
			Severity: "major",
			Title:    "English finding",
			Details:  "English details",
		}},
	})
	if strings.Contains(comment, "human_display_zh") {
		t.Fatalf("canonical comment leaked human_display_zh: %s", comment)
	}
	if !strings.Contains(comment, `"review_gate"`) || !strings.Contains(comment, "English canonical summary") {
		t.Fatalf("canonical comment = %s", comment)
	}
}

func TestParseReviewGateOutputAcceptsFailFindings(t *testing.T) {
	review, err := parseReviewGateOutput(`prefix {
		"review_gate": {
			"status": "fail",
			"summary": "Missing required tests.",
			"findings": [
				{ "severity": "blocker", "title": "No regression test", "details": "Add coverage before handoff." }
			],
			"checked_against": ["test plan"]
		}
	} suffix`)
	if err != nil {
		t.Fatalf("parseReviewGateOutput returned error: %v", err)
	}
	if review.Status != "fail" || len(review.Findings) != 1 || review.Findings[0].Severity != "blocker" {
		t.Fatalf("review = %#v", review)
	}
}

func TestParseReviewGateOutputRejectsMissingStatus(t *testing.T) {
	_, err := parseReviewGateOutput(`{
		"review_gate": {
			"summary": "No decision."
		}
	}`)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("error = %v, want status error", err)
	}
}
