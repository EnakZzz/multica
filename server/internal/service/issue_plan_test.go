package service

import (
	"strings"
	"testing"
)

func TestParseIssuePlanOutputAcceptsDependencies(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"title": "Launch plan",
		"parent_issue": { "title": "Launch", "description": "Ship the project" },
		"items": [
			{
				"title": "Build backend",
				"description": "Implement APIs",
				"recommended_agent_id": "",
				"match_score": 0,
				"match_reason": "No backend agent",
				"missing_capability": "Backend Engineer",
				"depends_on_positions": [],
				"selected": true
			},
			{
				"title": "Run integration test",
				"description": "Verify the full flow",
				"recommended_agent_id": "",
				"match_score": 0,
				"match_reason": "No QA agent",
				"missing_capability": "QA Tester",
				"depends_on_positions": [1],
				"selected": true
			}
		]
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanOutput returned error: %v", err)
	}
	if got := out.Items[1].DependsOnPositions; len(got) != 1 || got[0] != 1 {
		t.Fatalf("DependsOnPositions = %v, want [1]", got)
	}
}

func TestParseIssuePlanOutputRejectsForwardDependencies(t *testing.T) {
	_, err := parseIssuePlanOutput(`{
		"title": "Launch plan",
		"parent_issue": { "title": "Launch", "description": "Ship the project" },
		"items": [
			{
				"title": "Run integration test",
				"description": "Verify the full flow",
				"recommended_agent_id": "",
				"match_score": 0,
				"match_reason": "",
				"missing_capability": "",
				"depends_on_positions": [2],
				"selected": true
			},
			{
				"title": "Build backend",
				"description": "Implement APIs",
				"recommended_agent_id": "",
				"match_score": 0,
				"match_reason": "",
				"missing_capability": "",
				"depends_on_positions": [],
				"selected": true
			}
		]
	}`)
	if err == nil || !strings.Contains(err.Error(), "depends_on_positions must reference earlier item positions") {
		t.Fatalf("error = %v, want forward dependency validation error", err)
	}
}
