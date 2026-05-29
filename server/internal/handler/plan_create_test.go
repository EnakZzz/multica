package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplyPlanClarificationsMovesAnsweredQuestions(t *testing.T) {
	spec := service.PlanSpec{
		Summary:       "Draft",
		Goal:          "Clarify before execution",
		OpenQuestions: []string{"Which repo?", "Which runtime?"},
		Clarifications: []service.PlanClarification{
			{Question: "Existing?", Answer: "Keep it"},
		},
	}

	next, answered := applyPlanClarifications(spec, []service.PlanClarification{
		{Question: "Which repo?", Answer: "multica"},
		{Question: " ", Answer: "ignored"},
	})

	if len(answered) != 1 || answered[0].Question != "Which repo?" || answered[0].Answer != "multica" {
		t.Fatalf("answered = %#v", answered)
	}
	if got := strings.Join(next.OpenQuestions, "|"); got != "Which runtime?" {
		t.Fatalf("open questions = %q", got)
	}
	if len(next.Clarifications) != 2 || next.Clarifications[1].Answer != "multica" {
		t.Fatalf("clarifications = %#v", next.Clarifications)
	}
}

func TestNormalizeUpdatePlanItemIterationsSharesBranchPerIteration(t *testing.T) {
	yes := true
	no := false
	items := normalizeUpdatePlanItemIterations("Lost Pet", []updatePlanItemRequest{
		{
			Title:               "Build playable shell",
			RequiresGitCommit:   &yes,
			BranchName:          "feature/item-shell",
			IterationIndex:      1,
			IterationTitle:      "Playable shell",
			IterationBranchName: "feature/lost-pet-loop-1-playable-shell",
			Selected:            true,
		},
		{
			Title:             "Test playable shell",
			RequiresGitCommit: &yes,
			BranchName:        "feature/item-tests",
			IterationIndex:    1,
			Selected:          true,
		},
		{
			Title:             "Confirm playable shell",
			ExecutionKind:     service.PlanItemExecutionKindHumanConfirmation,
			RequiresGitCommit: &no,
			BranchName:        "feature/should-be-cleared",
			IterationIndex:    1,
			Selected:          true,
		},
		{
			Title:             "Build memory core",
			RequiresGitCommit: &yes,
			IterationIndex:    2,
			Selected:          true,
		},
	}, "merge-agent-id")

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
	if got := items[4].IterationBranchName; got == "" || got == items[0].BranchName {
		t.Fatalf("second iteration branch = %q, want generated branch distinct from iteration 1", got)
	}
	if got := items[4].BranchName; got != items[4].IterationBranchName {
		t.Fatalf("second iteration item branch = %q, want %q", got, items[4].IterationBranchName)
	}
	if len(items) != 7 {
		t.Fatalf("items length = %d, want existing gate plus merge and generated second iteration gate plus merge", len(items))
	}
	if got := items[2].ExecutionKind; got != service.PlanItemExecutionKindHumanConfirmation {
		t.Fatalf("existing gate execution kind = %q", got)
	}
	if got := items[2].DependsOnPositions; !reflect.DeepEqual(got, []int32{1, 2}) {
		t.Fatalf("existing gate dependencies = %#v, want [1 2]", got)
	}
	if got := items[3].NodeType; got != service.PipelineNodeTypeMerge {
		t.Fatalf("first merge node type = %q", got)
	}
	if got := items[3].DependsOnPositions; !reflect.DeepEqual(got, []int32{3}) {
		t.Fatalf("first merge dependencies = %#v, want previous gate", got)
	}
	if got := items[4].DependsOnPositions; !reflect.DeepEqual(got, []int32{4}) {
		t.Fatalf("second iteration first item dependencies = %#v, want previous merge", got)
	}
	if got := items[5].ExecutionKind; got != service.PlanItemExecutionKindHumanConfirmation {
		t.Fatalf("generated gate execution kind = %q", got)
	}
	if got := items[5].DependsOnPositions; !reflect.DeepEqual(got, []int32{5}) {
		t.Fatalf("generated gate dependencies = %#v, want second iteration work item", got)
	}
	if got := items[6].NodeType; got != service.PipelineNodeTypeMerge {
		t.Fatalf("second merge node type = %q", got)
	}
}

func TestNormalizeUpdatePlanItemIterationsAddsHumanGatePerIteration(t *testing.T) {
	yes := true
	items := normalizeUpdatePlanItemIterations("Lost Pet", []updatePlanItemRequest{
		{
			Title:               "Build playable shell",
			RequiresGitCommit:   &yes,
			IterationIndex:      1,
			IterationTitle:      "Playable shell",
			IterationBranchName: "feature/lost-pet-loop-1-playable-shell",
			Selected:            true,
		},
		{
			Title:             "Test playable shell",
			RequiresGitCommit: &yes,
			IterationIndex:    1,
			Selected:          true,
		},
		{
			Title:             "Build memory core",
			RequiresGitCommit: &yes,
			IterationIndex:    2,
			IterationTitle:    "Memory core",
			Selected:          true,
		},
	}, "merge-agent-id")

	if len(items) != 7 {
		t.Fatalf("items length = %d, want work, gate, merge, work, gate, merge", len(items))
	}
	if got := items[2].ExecutionKind; got != service.PlanItemExecutionKindHumanConfirmation {
		t.Fatalf("first generated gate execution kind = %q", got)
	}
	if got := items[2].DependsOnPositions; !reflect.DeepEqual(got, []int32{1, 2}) {
		t.Fatalf("first generated gate dependencies = %#v, want [1 2]", got)
	}
	if got := items[3].NodeType; got != service.PipelineNodeTypeMerge {
		t.Fatalf("first merge node type = %q", got)
	}
	if got := items[3].DependsOnPositions; !reflect.DeepEqual(got, []int32{3}) {
		t.Fatalf("first merge dependencies = %#v, want first gate", got)
	}
	if got := items[4].DependsOnPositions; !reflect.DeepEqual(got, []int32{4}) {
		t.Fatalf("second iteration work dependencies = %#v, want first merge", got)
	}
	if got := items[5].ExecutionKind; got != service.PlanItemExecutionKindHumanConfirmation {
		t.Fatalf("second generated gate execution kind = %q", got)
	}
	if got := items[5].DependsOnPositions; !reflect.DeepEqual(got, []int32{5}) {
		t.Fatalf("second generated gate dependencies = %#v, want second iteration work", got)
	}
	if got := items[5].ConfirmationQuestion; got == "" {
		t.Fatalf("second generated gate confirmation question is empty")
	}
	if got := items[6].NodeType; got != service.PipelineNodeTypeMerge {
		t.Fatalf("second merge node type = %q", got)
	}
}

func TestCreatePlanFromSourceIssueCreatesLinkedPlannerTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	plannerID := createInternalPlannerAgentForTest(t)
	sourceIssueID := createPlanSourceIssueForTest(t, "Plan source issue", "Break this request into a plan.")

	rr := httptest.NewRecorder()
	testHandler.CreatePlan(rr, newPipelineRequest(t, http.MethodPost, "/api/plans", map[string]any{
		"title":            "Plan source issue",
		"planner_agent_id": plannerID,
		"source_issue_id":  sourceIssueID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreatePlan status = %d body=%s", rr.Code, rr.Body.String())
	}

	var plan PlanResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	if plan.ParentIssueID == nil || *plan.ParentIssueID != sourceIssueID {
		t.Fatalf("parent issue id = %v, want %s", plan.ParentIssueID, sourceIssueID)
	}
	if plan.TaskID == "" {
		t.Fatalf("created plan should have a planner task id")
	}
	if plan.Prompt != "Plan source issue\n\nBreak this request into a plan." {
		t.Fatalf("plan prompt = %q", plan.Prompt)
	}

	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT context
		FROM agent_task_queue
		WHERE id = $1
	`, plan.TaskID).Scan(&contextJSON); err != nil {
		t.Fatalf("load planner task context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(contextJSON, &payload); err != nil {
		t.Fatalf("decode planner task context: %v", err)
	}
	if payload["type"] != "issue_plan" || payload["phase"] != "spec" || payload["plan_id"] != plan.ID || payload["source_issue_id"] != sourceIssueID {
		t.Fatalf("unexpected planner task context: %#v", payload)
	}
}

func TestIsQuickCreatePlannerAgentRequiresInternalFlag(t *testing.T) {
	if isQuickCreatePlannerAgent(db.Agent{Name: internalPlannerAgentName}) {
		t.Fatalf("legacy custom planner should not be treated as the built-in planner")
	}
	if !isQuickCreatePlannerAgent(db.Agent{Name: internalPlannerAgentName, IsInternal: true}) {
		t.Fatalf("internal planner should be treated as the built-in planner")
	}
	if !isQuickCreatePlannerAgent(db.Agent{IsInternal: true, BuiltinKey: pgtype.Text{String: internalPlannerBuiltinKey, Valid: true}}) {
		t.Fatalf("internal planner should be detected by builtin_key")
	}
	if isQuickCreatePlannerAgent(db.Agent{IsInternal: true, BuiltinKey: pgtype.Text{String: "multica/merge-agent", Valid: true}, Name: "Merge Agent"}) {
		t.Fatalf("merge agent should not be treated as the built-in planner")
	}
}

func TestQuickCreatePlannerAgentCreatesPlan(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.QuickCreateIssue(rr, newRequest(http.MethodPost, "/api/issues/quick-create", map[string]any{
		"agent_id": plannerID,
		"prompt":   "Build a multiplayer snake MVP\n\nUse the web app runtime.",
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("QuickCreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp QuickCreateIssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode quick create response: %v", err)
	}
	if resp.PlanID == "" {
		t.Fatalf("quick create planner response should include plan_id")
	}
	if resp.TaskID != "" {
		t.Fatalf("quick create planner response task_id = %q, want empty", resp.TaskID)
	}

	var title, prompt, plannerAgentID, taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT title, prompt, planner_agent_id::text, task_id::text
		FROM plan
		WHERE id = $1
	`, resp.PlanID).Scan(&title, &prompt, &plannerAgentID, &taskID); err != nil {
		t.Fatalf("load created plan: %v", err)
	}
	if title != "Build a multiplayer snake MVP" {
		t.Fatalf("plan title = %q", title)
	}
	if prompt != "Build a multiplayer snake MVP\n\nUse the web app runtime." {
		t.Fatalf("plan prompt = %q", prompt)
	}
	if plannerAgentID != plannerID {
		t.Fatalf("planner_agent_id = %s, want %s", plannerAgentID, plannerID)
	}
	if taskID == "" {
		t.Fatalf("created plan should have a planner task id")
	}

	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT context
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(&contextJSON); err != nil {
		t.Fatalf("load planner task context: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(contextJSON, &payload); err != nil {
		t.Fatalf("decode planner task context: %v", err)
	}
	if payload["type"] != "issue_plan" || payload["phase"] != "spec" || payload["plan_id"] != resp.PlanID {
		t.Fatalf("unexpected planner task context: %#v", payload)
	}
}

func TestNormalizePlanTextReplacesInvalidUTF8(t *testing.T) {
	got := normalizePlanText(" \xe4abc ")
	if got != "\uFFFDabc" {
		t.Fatalf("normalizePlanText = %q, want replacement char plus suffix", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("normalizePlanText returned invalid UTF-8")
	}
}

func TestFirstLineTruncatesByRunes(t *testing.T) {
	got := firstLine(strings.Repeat("一", 121))
	if utf8.RuneCountInString(got) != 120 {
		t.Fatalf("firstLine rune count = %d, want 120", utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("firstLine returned invalid UTF-8")
	}
}

func TestQuickCreatePlannerAgentAcceptsInvalidUTF8PromptBytes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	plannerID := createInternalPlannerAgentForTest(t)
	body := []byte(`{"agent_id":"` + plannerID + `","prompt":"`)
	body = append(body, 0xe4)
	body = append(body, []byte(`abc"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/api/issues/quick-create", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)

	rr := httptest.NewRecorder()
	testHandler.QuickCreateIssue(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("QuickCreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp QuickCreateIssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode quick create response: %v", err)
	}
	var prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT prompt
		FROM plan
		WHERE id = $1
	`, resp.PlanID).Scan(&prompt); err != nil {
		t.Fatalf("load created plan prompt: %v", err)
	}
	if prompt != "\uFFFDabc" {
		t.Fatalf("plan prompt = %q, want sanitized invalid byte", prompt)
	}
	if !utf8.ValidString(prompt) {
		t.Fatalf("plan prompt should be valid UTF-8")
	}
}

func TestQuickCreatePlannerAgentAcceptsLongChinesePrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	plannerID := createInternalPlannerAgentForTest(t)
	prompt := strings.Repeat("协议测试框架", 21)

	rr := httptest.NewRecorder()
	testHandler.QuickCreateIssue(rr, newRequest(http.MethodPost, "/api/issues/quick-create", map[string]any{
		"agent_id": plannerID,
		"prompt":   prompt,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("QuickCreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp QuickCreateIssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode quick create response: %v", err)
	}
	var title string
	if err := testPool.QueryRow(context.Background(), `
		SELECT title
		FROM plan
		WHERE id = $1
	`, resp.PlanID).Scan(&title); err != nil {
		t.Fatalf("load created plan title: %v", err)
	}
	if utf8.RuneCountInString(title) != 120 {
		t.Fatalf("plan title rune count = %d, want 120", utf8.RuneCountInString(title))
	}
	if !utf8.ValidString(title) {
		t.Fatalf("plan title should be valid UTF-8")
	}
}

func TestIssuePlanRuntimeRecoveryRetriesAndRelinksPlanTask(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID, _, planID, taskID := createIssuePlanRecoveryFixture(t, 1, 2)

	rows, err := testHandler.Queries.RecoverOrphanedTasksForRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("RecoverOrphanedTasksForRuntime: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != parseUUID(taskID) {
		t.Fatalf("recovered rows = %#v, want only %s", rows, taskID)
	}

	if retried := testHandler.TaskService.HandleFailedTasks(ctx, rows); retried != 1 {
		t.Fatalf("HandleFailedTasks retried = %d, want 1", retried)
	}

	var status, currentTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT status, task_id::text
		FROM plan
		WHERE id = $1
	`, planID).Scan(&status, &currentTaskID); err != nil {
		t.Fatalf("load plan after recovery: %v", err)
	}
	if status != "planning" {
		t.Fatalf("plan status = %q, want planning while retry runs", status)
	}
	if currentTaskID == taskID {
		t.Fatalf("plan task_id still points at failed parent task %s", taskID)
	}

	var childStatus, childParentID string
	var childAttempt int
	if err := testPool.QueryRow(ctx, `
		SELECT status, attempt, parent_task_id::text
		FROM agent_task_queue
		WHERE parent_task_id = $1
	`, taskID).Scan(&childStatus, &childAttempt, &childParentID); err != nil {
		t.Fatalf("load retry task: %v", err)
	}
	if childStatus != "queued" || childAttempt != 2 || childParentID != taskID {
		t.Fatalf("retry task = status %q attempt %d parent %s", childStatus, childAttempt, childParentID)
	}
	if currentTaskID == "" {
		t.Fatalf("plan task_id should point at retry task")
	}
}

func TestIssuePlanRuntimeRecoveryMarksPlanFailedWhenRetryBudgetExhausted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID, _, planID, _ := createIssuePlanRecoveryFixture(t, 1, 1)

	rows, err := testHandler.Queries.RecoverOrphanedTasksForRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("RecoverOrphanedTasksForRuntime: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("recovered rows = %d, want 1", len(rows))
	}

	if retried := testHandler.TaskService.HandleFailedTasks(ctx, rows); retried != 0 {
		t.Fatalf("HandleFailedTasks retried = %d, want 0", retried)
	}

	var status string
	var errMsg string
	if err := testPool.QueryRow(ctx, `
		SELECT status, coalesce(error, '')
		FROM plan
		WHERE id = $1
	`, planID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("load plan after exhausted recovery: %v", err)
	}
	if status != "failed" {
		t.Fatalf("plan status = %q, want failed", status)
	}
	if !strings.Contains(errMsg, "daemon restarted while task was in flight") {
		t.Fatalf("plan error = %v, want daemon restart failure", errMsg)
	}
}

func TestIssuePlanCompletionParsesEscapedNewlinesInJSONStrings(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	_, _, planID, taskID := createIssuePlanRecoveryFixture(t, 1, 2)
	plannerOutput := `{
  "needs_plan": true,
  "title": "Escaped newline plan",
  "parent_issue": {
    "title": "Parent",
    "description": "Line one\nLine two"
  },
  "items": [
    {
      "title": "Build feature",
      "description": "Step one\nStep two",
      "acceptance_criteria": ["Works"],
      "suggested_test_commands": [],
      "context_resources": [],
      "risk_notes": [],
      "execution_kind": "agent_task",
      "confirmation_question": "",
      "confirmation_reason": "",
      "required_evidence": [],
      "recommended_agent_id": "",
      "match_score": 0,
      "match_reason": "",
      "missing_capability": "",
      "depends_on_positions": [],
      "selected": true
    }
  ]
}`
	result, err := json.Marshal(map[string]any{"output": "```json\n" + plannerOutput + "\n```"})
	if err != nil {
		t.Fatalf("marshal completion result: %v", err)
	}

	if _, err := testHandler.TaskService.CompleteTask(ctx, parseUUID(taskID), result, "", ""); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	var status, parentDescription, itemDescription string
	if err := testPool.QueryRow(ctx, `
		SELECT p.status, coalesce(p.parent_description, ''), pi.description
		FROM plan p
		JOIN plan_item pi ON pi.plan_id = p.id
		WHERE p.id = $1
	`, planID).Scan(&status, &parentDescription, &itemDescription); err != nil {
		t.Fatalf("load completed plan: %v", err)
	}
	if status != "ready" {
		t.Fatalf("plan status = %q, want ready", status)
	}
	if !strings.Contains(parentDescription, "Line one\nLine two") {
		t.Fatalf("parent description did not preserve newline: %q", parentDescription)
	}
	if !strings.Contains(itemDescription, "Step one\nStep two") {
		t.Fatalf("item description did not preserve newline: %q", itemDescription)
	}
}

func createPlanSourceIssueForTest(t *testing.T, title, description string) string {
	t.Helper()

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newPipelineRequest(t, http.MethodPost, "/api/issues", map[string]any{
		"title":       title,
		"description": description,
		"status":      "todo",
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var issue IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &issue); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	return issue.ID
}

func createIssuePlanRecoveryFixture(t *testing.T, attempt, maxAttempts int) (runtimeID, agentID, planID, taskID string) {
	t.Helper()
	ctx := context.Background()

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES (
			$1,
			'plan-recovery-' || gen_random_uuid()::text,
			'Plan recovery runtime',
			'local',
			'claude',
			'online',
			'{}',
			'{"capabilities":["issue_plan"]}'::jsonb,
			$2,
			now()
		)
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, is_internal
		)
		VALUES ($1, 'Plan recovery agent ' || gen_random_uuid()::text, '', 'local', '{}'::jsonb, $2, 'workspace', 1, $3, '', '{}'::jsonb, '[]'::jsonb, true)
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO plan (
			workspace_id, title, prompt, status, planner_agent_id,
			spec, spec_approved_at, spec_approved_by, created_by
		)
		VALUES (
			$1,
			'Runtime recovery plan',
			'Create child issues',
			'planning',
			$2,
			'{"summary":"Approved spec","goal":"Generate items"}'::jsonb,
			now(),
			$3,
			$3
		)
		RETURNING id::text
	`, testWorkspaceID, agentID, testUserID).Scan(&planID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	contextJSON, err := json.Marshal(map[string]any{
		"type":         "issue_plan",
		"phase":        "items",
		"plan_id":      planID,
		"prompt":       "Create child issues",
		"requester_id": testUserID,
		"workspace_id": testWorkspaceID,
	})
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, status, priority, context,
			attempt, max_attempts, dispatched_at, started_at
		)
		VALUES ($1, $2, 'running', 0, $3::jsonb, $4, $5, now(), now())
		RETURNING id::text
	`, agentID, runtimeID, contextJSON, attempt, maxAttempts).Scan(&taskID); err != nil {
		t.Fatalf("insert plan task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE plan SET task_id = $2 WHERE id = $1`, planID, taskID); err != nil {
		t.Fatalf("link plan task: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM plan WHERE id = $1`, planID)
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1 OR parent_task_id = $1`, taskID)
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID, agentID, planID, taskID
}
