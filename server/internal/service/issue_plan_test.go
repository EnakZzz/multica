package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseIssuePlanSpecOutputAcceptsSpec(t *testing.T) {
	spec, err := parseIssuePlanSpecOutput(`{
		"summary": "Build a two-stage planner.",
		"goal": "Let users approve a spec before issue generation.",
		"success_criteria": ["Plan enters spec review", "Approval generates items"],
		"acceptance_scenarios": [
			{
				"name": "Approve spec",
				"given": "A plan is in spec review",
				"when": "The user approves the spec",
				"then": "The planner generates executable items from that spec"
			}
		],
		"in_scope": ["Plans UI"],
		"out_of_scope": ["Automatic issue creation"],
		"approach": "Add a spec phase before items.",
		"design_decisions": ["Keep item generation separate from spec generation"],
		"verification_commands": ["go test ./internal/handler -run TestPlan"],
		"assumptions": ["Planner returns JSON"],
		"open_questions": []
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanSpecOutput returned error: %v", err)
	}
	if spec.Summary != "Build a two-stage planner." || spec.Goal == "" || len(spec.SuccessCriteria) != 2 {
		t.Fatalf("spec = %#v", spec)
	}
	if len(spec.AcceptanceScenarios) != 1 || spec.AcceptanceScenarios[0].Then == "" {
		t.Fatalf("acceptance scenarios = %#v", spec.AcceptanceScenarios)
	}
	if got := strings.Join(spec.DesignDecisions, "|"); got != "Keep item generation separate from spec generation" {
		t.Fatalf("DesignDecisions = %q", got)
	}
	if got := strings.Join(spec.VerificationCommands, "|"); got != "go test ./internal/handler -run TestPlan" {
		t.Fatalf("VerificationCommands = %q", got)
	}
}

func TestNormalizePlanSpecLimitsOpenQuestions(t *testing.T) {
	spec := normalizePlanSpec(PlanSpec{
		Summary:       "Draft",
		Goal:          "Reduce planner back-and-forth.",
		Assumptions:   []string{"Use existing planner UI."},
		OpenQuestions: []string{"Which repo?", "Which runtime?", "Which style?", "Which rollout?"},
	})

	if got := strings.Join(spec.OpenQuestions, "|"); got != "Which repo?|Which runtime?" {
		t.Fatalf("OpenQuestions = %q, want first two questions only", got)
	}
	if got := strings.Join(spec.Assumptions, "|"); got != "Use existing planner UI.|Which style?|Which rollout?" {
		t.Fatalf("Assumptions = %q, want overflow questions carried as assumptions", got)
	}
}

func TestNormalizePlanSpecCleansReviewContractFields(t *testing.T) {
	spec := normalizePlanSpec(PlanSpec{
		AcceptanceScenarios: []PlanAcceptanceScenario{
			{Name: " Save ", Given: " Draft ", When: " Approve ", Then: " Snapshot "},
			{Name: "Save", Given: "Draft", When: "Approve", Then: "Snapshot"},
			{},
		},
		DesignDecisions:      []string{"Use existing plan table", "Use existing plan table", " "},
		VerificationCommands: []string{"go test ./internal/service", "go test ./internal/service"},
	})

	if len(spec.AcceptanceScenarios) != 1 {
		t.Fatalf("AcceptanceScenarios = %#v, want one normalized scenario", spec.AcceptanceScenarios)
	}
	if got := spec.AcceptanceScenarios[0]; got.Name != "Save" || got.Given != "Draft" || got.When != "Approve" || got.Then != "Snapshot" {
		t.Fatalf("AcceptanceScenarios[0] = %#v", got)
	}
	if got := strings.Join(spec.DesignDecisions, "|"); got != "Use existing plan table" {
		t.Fatalf("DesignDecisions = %q", got)
	}
	if got := strings.Join(spec.VerificationCommands, "|"); got != "go test ./internal/service" {
		t.Fatalf("VerificationCommands = %q", got)
	}
}

func TestNormalizeIssuePlanItemIterationsSharesBranchPerIteration(t *testing.T) {
	yes := true
	no := false
	items := normalizeIssuePlanItemIterations("Lost Pet", []issuePlanResultItem{
		{
			Title:               "Build playable shell",
			RequiresGitCommit:   &yes,
			BranchName:          "feature/item-shell",
			IterationIndex:      1,
			IterationTitle:      "Playable shell",
			IterationBranchName: "feature/lost-pet-loop-1-playable-shell",
		},
		{
			Title:             "Test playable shell",
			RequiresGitCommit: &yes,
			BranchName:        "feature/item-tests",
			IterationIndex:    1,
		},
		{
			Title:             "Confirm playable shell",
			ExecutionKind:     PlanItemExecutionKindHumanConfirmation,
			RequiresGitCommit: &no,
			BranchName:        "feature/should-be-cleared",
			IterationIndex:    1,
		},
		{
			Title:             "Build memory core",
			RequiresGitCommit: &yes,
			IterationIndex:    2,
		},
	})

	if got := items[0].BranchName; got != "feature/lost-pet-loop-1-playable-shell" {
		t.Fatalf("first item branch = %q", got)
	}
	if got := items[1].BranchName; got != items[0].BranchName {
		t.Fatalf("same iteration branch mismatch: %q vs %q", got, items[0].BranchName)
	}
	if got := items[2].BranchName; got != "" {
		t.Fatalf("non-commit branch = %q, want empty", got)
	}
	if got := items[2].IterationBranchName; got != items[0].BranchName {
		t.Fatalf("non-commit iteration branch = %q, want %q", got, items[0].BranchName)
	}
	if got := items[3].IterationBranchName; got == "" || got == items[0].BranchName {
		t.Fatalf("second iteration branch = %q, want generated branch distinct from iteration 1", got)
	}
	if got := items[3].BranchName; got != items[3].IterationBranchName {
		t.Fatalf("second iteration item branch = %q, want %q", got, items[3].IterationBranchName)
	}
}

func TestMergeExistingPlanClarificationsPreservesHistory(t *testing.T) {
	existing, err := json.Marshal(PlanSpec{
		Summary: "Old",
		Clarifications: []PlanClarification{
			{Question: "Which repo?", Answer: "multica"},
		},
	})
	if err != nil {
		t.Fatalf("marshal existing spec: %v", err)
	}

	merged := mergeExistingPlanClarifications(existing, PlanSpec{Summary: "New", Goal: "Regenerate spec"})
	if len(merged.Clarifications) != 1 || merged.Clarifications[0].Answer != "multica" {
		t.Fatalf("clarifications = %#v", merged.Clarifications)
	}
}

func TestParseIssuePlanSpecOutputRejectsMissingGoal(t *testing.T) {
	_, err := parseIssuePlanSpecOutput(`{
		"summary": "Build a two-stage planner."
	}`)
	if err == nil || !strings.Contains(err.Error(), "missing goal") {
		t.Fatalf("error = %v, want missing goal", err)
	}
}

func TestParseIssuePlanOutputAcceptsDependencies(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"title": "Launch plan",
		"parent_issue": { "title": "Launch", "description": "Ship the project" },
		"items": [
			{
				"title": "Build backend",
				"description": "Implement APIs",
				"acceptance_criteria": ["API creates plan items", "API creates plan items", "No old items remain"],
				"suggested_test_commands": ["go test ./internal/handler"],
				"unit_test_checklist": [
					{
						"id": "plan-item-save",
						"title": "Plan item save test",
						"command": "go test ./internal/handler -run TestPlanItemSave -count=1",
						"expected": "passes",
						"required": true
					}
				],
				"context_resources": ["server/internal/handler/plan.go"],
				"risk_notes": ["Migration must keep existing plans readable"],
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
	if got := out.Items[0].AcceptanceCriteria; len(got) != 2 || got[0] != "API creates plan items" || got[1] != "No old items remain" {
		t.Fatalf("AcceptanceCriteria = %v, want normalized criteria", got)
	}
	if got := out.Items[0].SuggestedTestCommands; len(got) != 1 || got[0] != "go test ./internal/handler" {
		t.Fatalf("SuggestedTestCommands = %v, want test command", got)
	}
	if got := out.Items[0].UnitTestChecklist; len(got) != 1 || got[0].ID != "plan-item-save" || got[0].Status != UnitTestStatusPending {
		t.Fatalf("UnitTestChecklist = %#v, want normalized pending check", got)
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

func TestParseIssuePlanOutputAcceptsNoPlanDecision(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"needs_plan": false,
		"reason": "single small issue"
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanOutput returned error: %v", err)
	}
	if out.shouldCreatePlan() {
		t.Fatal("shouldCreatePlan = true, want false")
	}
}

func TestParseIssuePlanOutputAcceptsNoPlanDirectIssue(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"needs_plan": false,
		"reason": "small bug fix",
		"direct_issue": {
			"title": "Fix settings crash",
			"description": "Opening settings crashes after login.",
			"recommended_agent_id": "11111111-1111-1111-1111-111111111111",
			"match_score": 95,
			"match_reason": "The agent owns UI bugs."
		}
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanOutput returned error: %v", err)
	}
	if out.shouldCreatePlan() {
		t.Fatal("shouldCreatePlan = true, want false")
	}
	direct, ok := out.directIssue()
	if !ok {
		t.Fatal("directIssue ok = false, want true")
	}
	if direct.Title != "Fix settings crash" || direct.MatchScore != 95 {
		t.Fatalf("directIssue = %#v", direct)
	}
}

func TestParseIssuePlanOutputAcceptsNullDirectIssueAgent(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"needs_plan": false,
		"reason": "no current agent matches this subsystem",
		"direct_issue": {
			"title": "Fix subsystem crash",
			"description": "Route manually because no available agent fits.",
			"recommended_agent_id": null,
			"match_score": 0,
			"match_reason": "No suitable agent.",
			"missing_capability": "Subsystem owner"
		}
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanOutput returned error: %v", err)
	}
	direct, ok := out.directIssue()
	if !ok {
		t.Fatal("directIssue ok = false, want true")
	}
	if direct.RecommendedAgentID != "" || direct.MatchScore != 0 {
		t.Fatalf("directIssue = %#v", direct)
	}
}

func TestParseIssuePlanOutputAcceptsPipelineNodes(t *testing.T) {
	out, err := parseIssuePlanOutput(`{
		"needs_plan": true,
		"pipeline_id": "11111111-1111-1111-1111-111111111111",
		"pipeline": {
			"nodes": [
				{ "key": "design", "title": "Draft design", "description": "Plan it", "agent_id": "" },
				{ "key": "build", "title": "Build", "description": "Implement it", "agent_id": "" }
			]
		}
	}`)
	if err != nil {
		t.Fatalf("parseIssuePlanOutput returned error: %v", err)
	}
	if len(out.Pipeline.Nodes) != 2 || out.Pipeline.Nodes[1].Key != "build" {
		t.Fatalf("pipeline nodes not parsed: %#v", out.Pipeline.Nodes)
	}
	if len(out.Items) != 2 || out.Items[0].Title != "Draft design" {
		t.Fatalf("pipeline nodes were not converted to plan items: %#v", out.Items)
	}
}
