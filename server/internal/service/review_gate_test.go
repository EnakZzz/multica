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
