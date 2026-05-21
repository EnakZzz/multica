package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	UnitTestStatusNotRequired = "not_required"
	UnitTestStatusPending     = "pending"
	UnitTestStatusPassed      = "passed"
	UnitTestStatusFailed      = "failed"
	UnitTestStatusBlocked     = "blocked"
	UnitTestMaxIterations     = 2
)

type UnitTestCheck struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Command        string  `json:"command"`
	Expected       string  `json:"expected"`
	Required       bool    `json:"required"`
	Status         string  `json:"status"`
	LastRunAt      *string `json:"last_run_at"`
	OutputExcerpt  string  `json:"output_excerpt"`
	FailureSummary string  `json:"failure_summary"`
	TaskID         string  `json:"task_id"`
}

type rawUnitTestCheck struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Command        string `json:"command"`
	Expected       string `json:"expected"`
	Required       *bool  `json:"required"`
	Status         string `json:"status"`
	LastRunAt      string `json:"last_run_at"`
	OutputExcerpt  string `json:"output_excerpt"`
	FailureSummary string `json:"failure_summary"`
	TaskID         string `json:"task_id"`
}

type unitTestReportEnvelope struct {
	UnitTestReport unitTestReport `json:"unit_test_report"`
}

type unitTestReport struct {
	Status string                `json:"status"`
	Checks []unitTestReportCheck `json:"checks"`
}

type unitTestReportCheck struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Command       string `json:"command"`
	Summary       string `json:"summary"`
	OutputExcerpt string `json:"output_excerpt"`
}

func NormalizeUnitTestChecklistJSON(data []byte) []UnitTestCheck {
	if len(data) == 0 {
		return []UnitTestCheck{}
	}
	var raw []rawUnitTestCheck
	if err := json.Unmarshal(data, &raw); err != nil {
		return []UnitTestCheck{}
	}
	return normalizeUnitTestChecks(raw)
}

func MarshalUnitTestChecklist(checks []UnitTestCheck) []byte {
	checks = NormalizeUnitTestChecks(checks)
	data, err := json.Marshal(checks)
	if err != nil {
		return []byte("[]")
	}
	return data
}

func NormalizeUnitTestChecks(checks []UnitTestCheck) []UnitTestCheck {
	raw := make([]rawUnitTestCheck, 0, len(checks))
	for _, check := range checks {
		required := check.Required
		raw = append(raw, rawUnitTestCheck{
			ID:             check.ID,
			Title:          check.Title,
			Command:        check.Command,
			Expected:       check.Expected,
			Required:       &required,
			Status:         check.Status,
			OutputExcerpt:  check.OutputExcerpt,
			FailureSummary: check.FailureSummary,
			TaskID:         check.TaskID,
		})
		if check.LastRunAt != nil {
			raw[len(raw)-1].LastRunAt = *check.LastRunAt
		}
	}
	return normalizeUnitTestChecks(raw)
}

func UnitTestStatusForChecklist(checks []UnitTestCheck) string {
	if len(checks) == 0 {
		return UnitTestStatusNotRequired
	}
	anyFailed := false
	allRequiredPassed := true
	for _, check := range checks {
		if !check.Required {
			continue
		}
		switch check.Status {
		case UnitTestStatusPassed:
		case UnitTestStatusFailed:
			anyFailed = true
			allRequiredPassed = false
		default:
			allRequiredPassed = false
		}
	}
	if anyFailed {
		return UnitTestStatusFailed
	}
	if allRequiredPassed {
		return UnitTestStatusPassed
	}
	return UnitTestStatusPending
}

func parseUnitTestReportOutput(output string) (unitTestReport, error) {
	var empty unitTestReport
	output = strings.TrimSpace(output)
	if output == "" {
		return empty, fmt.Errorf("unit test report returned empty output")
	}
	for start := strings.Index(output, "{"); start >= 0; {
		var raw map[string]json.RawMessage
		decoder := json.NewDecoder(strings.NewReader(output[start:]))
		if err := decoder.Decode(&raw); err == nil {
			reportRaw, ok := raw["unit_test_report"]
			if ok {
				var report unitTestReport
				if err := json.Unmarshal(reportRaw, &report); err != nil {
					return empty, fmt.Errorf("unit_test_report JSON is invalid: %v", err)
				}
				report = normalizeUnitTestReport(report)
				switch report.Status {
				case UnitTestStatusPassed, UnitTestStatusFailed:
					return report, nil
				default:
					return empty, fmt.Errorf("unit_test_report.status must be passed or failed")
				}
			}
		}
		next := strings.Index(output[start+1:], "{")
		if next < 0 {
			break
		}
		start += next + 1
	}
	return empty, fmt.Errorf("unit test output did not contain a JSON object with unit_test_report")
}

func applyUnitTestReport(checks []UnitTestCheck, report unitTestReport, taskID string, now time.Time) []UnitTestCheck {
	checks = NormalizeUnitTestChecks(checks)
	byID := make(map[string]int, len(checks))
	byCommand := make(map[string]int, len(checks))
	for i, check := range checks {
		if check.ID != "" {
			byID[check.ID] = i
		}
		if check.Command != "" {
			byCommand[check.Command] = i
		}
	}
	stamp := now.UTC().Format(time.RFC3339)
	for _, result := range report.Checks {
		idx, ok := byID[result.ID]
		if !ok && result.Command != "" {
			idx, ok = byCommand[result.Command]
		}
		if !ok {
			continue
		}
		check := checks[idx]
		check.Status = normalizeUnitTestCheckStatus(result.Status)
		check.LastRunAt = &stamp
		check.TaskID = taskID
		check.OutputExcerpt = truncateUnitTestText(result.OutputExcerpt, 1200)
		check.FailureSummary = ""
		if check.Status == UnitTestStatusFailed {
			check.FailureSummary = truncateUnitTestText(firstNonEmpty(result.Summary, result.OutputExcerpt), 500)
		}
		checks[idx] = check
	}
	return checks
}

func normalizeUnitTestChecks(raw []rawUnitTestCheck) []UnitTestCheck {
	out := make([]UnitTestCheck, 0, len(raw))
	seen := map[string]bool{}
	for _, check := range raw {
		command := strings.TrimSpace(check.Command)
		title := strings.TrimSpace(check.Title)
		if command == "" && title == "" {
			continue
		}
		id := strings.TrimSpace(check.ID)
		if id == "" {
			id = unitTestCheckID(title, command)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		required := true
		if check.Required != nil {
			required = *check.Required
		}
		var lastRunAt *string
		if ts := strings.TrimSpace(check.LastRunAt); ts != "" {
			lastRunAt = &ts
		}
		out = append(out, UnitTestCheck{
			ID:             id,
			Title:          title,
			Command:        command,
			Expected:       strings.TrimSpace(check.Expected),
			Required:       required,
			Status:         normalizeUnitTestCheckStatus(check.Status),
			LastRunAt:      lastRunAt,
			OutputExcerpt:  truncateUnitTestText(check.OutputExcerpt, 1200),
			FailureSummary: truncateUnitTestText(check.FailureSummary, 500),
			TaskID:         strings.TrimSpace(check.TaskID),
		})
	}
	return out
}

func normalizeUnitTestReport(report unitTestReport) unitTestReport {
	report.Status = strings.ToLower(strings.TrimSpace(report.Status))
	checks := make([]unitTestReportCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		status := normalizeUnitTestCheckStatus(check.Status)
		if status != UnitTestStatusPassed && status != UnitTestStatusFailed && status != "skipped" {
			status = UnitTestStatusFailed
		}
		id := strings.TrimSpace(check.ID)
		command := strings.TrimSpace(check.Command)
		if id == "" && command == "" {
			continue
		}
		checks = append(checks, unitTestReportCheck{
			ID:            id,
			Status:        status,
			Command:       command,
			Summary:       strings.TrimSpace(check.Summary),
			OutputExcerpt: strings.TrimSpace(check.OutputExcerpt),
		})
	}
	report.Checks = checks
	return report
}

func normalizeUnitTestCheckStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case UnitTestStatusPassed:
		return UnitTestStatusPassed
	case UnitTestStatusFailed:
		return UnitTestStatusFailed
	case "skipped":
		return "skipped"
	default:
		return UnitTestStatusPending
	}
}

func unitTestCheckID(title, command string) string {
	base := strings.ToLower(strings.TrimSpace(firstNonEmpty(title, command)))
	var b strings.Builder
	prevDash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func truncateUnitTestText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	return string(rs[:maxRunes]) + "..."
}
