package service

import (
	"strings"
	"testing"
	"time"
)

func TestParseUnitTestReportOutputAcceptsJSONWithSurroundingText(t *testing.T) {
	report, err := parseUnitTestReportOutput(`notes before
{
  "unit_test_report": {
    "status": "failed",
    "checks": [
      {
        "id": "service-regression",
        "status": "failed",
        "command": "go test ./internal/service -run TestRegression -count=1",
        "summary": "regression failed",
        "output_excerpt": "expected true"
      }
    ]
  },
  "branch": "feature/service-regression"
}`)
	if err != nil {
		t.Fatalf("parseUnitTestReportOutput returned error: %v", err)
	}
	if report.Status != UnitTestStatusFailed || len(report.Checks) != 1 {
		t.Fatalf("report = %#v", report)
	}
	if report.Checks[0].ID != "service-regression" || report.Checks[0].Status != UnitTestStatusFailed {
		t.Fatalf("check = %#v", report.Checks[0])
	}
}

func TestApplyUnitTestReportUpdatesChecklistByIDAndCommand(t *testing.T) {
	now := time.Date(2026, 5, 21, 3, 0, 0, 0, time.UTC)
	longOutput := strings.Repeat("x", 1300)
	checks := []UnitTestCheck{
		{ID: "required-check", Title: "Required", Command: "go test ./required", Required: true},
		{ID: "optional-check", Title: "Optional", Command: "go test ./optional", Required: false},
	}
	report := unitTestReport{
		Status: UnitTestStatusFailed,
		Checks: []unitTestReportCheck{
			{ID: "required-check", Status: UnitTestStatusFailed, Summary: "required failed", OutputExcerpt: longOutput},
			{Command: "go test ./optional", Status: UnitTestStatusPassed, Summary: "optional passed"},
		},
	}

	updated := applyUnitTestReport(checks, report, "task-1", now)
	if got := UnitTestStatusForChecklist(updated); got != UnitTestStatusFailed {
		t.Fatalf("UnitTestStatusForChecklist = %s, want failed", got)
	}
	if updated[0].Status != UnitTestStatusFailed || updated[0].FailureSummary != "required failed" || updated[0].TaskID != "task-1" {
		t.Fatalf("required check = %#v", updated[0])
	}
	if updated[0].LastRunAt == nil || *updated[0].LastRunAt != "2026-05-21T03:00:00Z" {
		t.Fatalf("LastRunAt = %v", updated[0].LastRunAt)
	}
	if len([]rune(updated[0].OutputExcerpt)) != 1203 {
		t.Fatalf("OutputExcerpt was not truncated, length=%d", len([]rune(updated[0].OutputExcerpt)))
	}
	if updated[1].Status != UnitTestStatusPassed {
		t.Fatalf("optional check = %#v", updated[1])
	}
}
