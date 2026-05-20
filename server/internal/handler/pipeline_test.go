package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPipelineCRUDIsWorkspaceIsolated(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createPipelineForTest(t, "Workspace isolated pipeline", agentID)
	otherWorkspaceID := createOtherWorkspaceForPipelineTest(t)
	insertPipelineForWorkspace(t, otherWorkspaceID, "Other workspace pipeline")

	rr := httptest.NewRecorder()
	testHandler.ListPipelines(rr, newPipelineRequest(t, http.MethodGet, "/api/pipelines", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("ListPipelines status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp listPipelinesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	found := false
	for _, p := range resp.Pipelines {
		if p.ID == pipeline.ID {
			found = true
		}
		if p.WorkspaceID == otherWorkspaceID {
			t.Fatalf("list leaked pipeline from another workspace: %#v", p)
		}
	}
	if !found {
		t.Fatalf("created pipeline %s missing from workspace list", pipeline.ID)
	}
}

func TestListPipelinesSeedsBuiltInPipelinesIdempotently(t *testing.T) {
	first := listPipelinesForTest(t)
	systemByKey := map[string]PipelineResponse{}
	for _, pipeline := range first.Pipelines {
		if pipeline.IsSystem && pipeline.SystemKey != nil {
			systemByKey[*pipeline.SystemKey] = pipeline
		}
	}
	for _, key := range []string{"systematic-debugging", "test-driven-development", "review-gated-feature-development"} {
		pipeline, ok := systemByKey[key]
		if !ok {
			t.Fatalf("built-in pipeline %s was not seeded", key)
		}
		if pipeline.Editable || pipeline.Deletable {
			t.Fatalf("built-in pipeline %s should be readonly: %#v", key, pipeline)
		}
		if len(pipeline.Nodes) == 0 {
			t.Fatalf("built-in pipeline %s has no nodes", key)
		}
	}

	second := listPipelinesForTest(t)
	counts := map[string]int{}
	for _, pipeline := range second.Pipelines {
		if pipeline.IsSystem && pipeline.SystemKey != nil {
			counts[*pipeline.SystemKey]++
		}
	}
	for _, key := range []string{"systematic-debugging", "test-driven-development", "review-gated-feature-development"} {
		if counts[key] != 1 {
			t.Fatalf("built-in pipeline %s count = %d, want 1", key, counts[key])
		}
	}
}

func TestBuiltInPipelineRejectsMutationAndCanBeDuplicated(t *testing.T) {
	systemPipeline := systemPipelineForTest(t, "systematic-debugging")

	updateRR := httptest.NewRecorder()
	testHandler.UpdatePipeline(updateRR, withURLParam(newPipelineRequest(t, http.MethodPatch, "/api/pipelines/"+systemPipeline.ID, updatePipelineRequest{
		Name:        strPtr("Renamed system pipeline"),
		Description: strPtr("Should not update"),
		Nodes:       []upsertPipelineNodeRequest{{Key: "node", Title: "Node"}},
	}), "id", systemPipeline.ID))
	if updateRR.Code != http.StatusForbidden {
		t.Fatalf("UpdatePipeline status = %d body=%s, want 403", updateRR.Code, updateRR.Body.String())
	}

	importRR := httptest.NewRecorder()
	testHandler.ImportPipelineYAML(importRR, newPipelineRequest(t, http.MethodPost, "/api/pipelines/import", importPipelineYAMLRequest{
		PipelineID: &systemPipeline.ID,
		Content:    "version: 1\nname: Replacement\nnodes:\n  - key: node\n    title: Node\n",
	}))
	if importRR.Code != http.StatusForbidden {
		t.Fatalf("ImportPipelineYAML status = %d body=%s, want 403", importRR.Code, importRR.Body.String())
	}

	deleteRR := httptest.NewRecorder()
	testHandler.DeletePipeline(deleteRR, withURLParam(newPipelineRequest(t, http.MethodDelete, "/api/pipelines/"+systemPipeline.ID, nil), "id", systemPipeline.ID))
	if deleteRR.Code != http.StatusForbidden {
		t.Fatalf("DeletePipeline status = %d body=%s, want 403", deleteRR.Code, deleteRR.Body.String())
	}

	duplicateRR := httptest.NewRecorder()
	testHandler.DuplicatePipeline(duplicateRR, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+systemPipeline.ID+"/duplicate", duplicatePipelineRequest{
		Name: strPtr("Custom systematic debugging copy " + uuid.NewString()),
	}), "id", systemPipeline.ID))
	if duplicateRR.Code != http.StatusCreated {
		t.Fatalf("DuplicatePipeline status = %d body=%s", duplicateRR.Code, duplicateRR.Body.String())
	}
	var duplicated PipelineResponse
	if err := json.Unmarshal(duplicateRR.Body.Bytes(), &duplicated); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if duplicated.IsSystem || !duplicated.Editable || !duplicated.Deletable {
		t.Fatalf("duplicate should be a normal editable pipeline: %#v", duplicated)
	}
	if len(duplicated.Nodes) != len(systemPipeline.Nodes) {
		t.Fatalf("duplicated nodes = %d, want %d", len(duplicated.Nodes), len(systemPipeline.Nodes))
	}
}

func TestRunPipelineCreatesIssuesAndDependencies(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createPipelineForTest(t, "Run pipeline", agentID)

	rr := httptest.NewRecorder()
	testHandler.RunPipeline(rr, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+pipeline.ID+"/run", nil), "id", pipeline.ID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("RunPipeline status = %d body=%s", rr.Code, rr.Body.String())
	}

	var run PipelineRunResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if run.ParentIssueID == "" || len(run.Nodes) != 2 {
		t.Fatalf("unexpected run response: %#v", run)
	}

	childByNode := map[string]string{}
	for _, node := range run.Nodes {
		childByNode[node.NodeKey] = node.IssueID
		var assigneeType, assigneeID string
		var parentIssueID string
		if err := testPool.QueryRow(context.Background(), `
			SELECT assignee_type, assignee_id::text, parent_issue_id::text
			FROM issue
			WHERE id = $1
		`, node.IssueID).Scan(&assigneeType, &assigneeID, &parentIssueID); err != nil {
			t.Fatalf("load child issue %s: %v", node.NodeKey, err)
		}
		if assigneeType != "agent" || assigneeID != agentID {
			t.Fatalf("node %s assigned to %s/%s, want agent/%s", node.NodeKey, assigneeType, assigneeID, agentID)
		}
		if parentIssueID != run.ParentIssueID {
			t.Fatalf("node %s parent = %s, want %s", node.NodeKey, parentIssueID, run.ParentIssueID)
		}
	}

	var depType string
	if err := testPool.QueryRow(context.Background(), `
		SELECT type
		FROM issue_dependency
		WHERE issue_id = $1 AND depends_on_issue_id = $2
	`, childByNode["build"], childByNode["design"]).Scan(&depType); err != nil {
		t.Fatalf("load node dependency: %v", err)
	}
	if depType != "blocked_by" {
		t.Fatalf("dependency type = %q, want blocked_by", depType)
	}
}

func TestRunPipelineWritesTargetReposIntoChildIssueDescription(t *testing.T) {
	agentID := handlerTestAgentID(t)
	projectID := createPipelineTestProjectWithRepo(t, "app", "https://github.com/acme/app.git")
	body := createPipelineRequest{
		Name:        "Repo targeted pipeline",
		Description: "Pipeline with target repos",
		Nodes: []upsertPipelineNodeRequest{
			{
				Key:         "build",
				Type:        "issue",
				Title:       "Build app",
				Description: "Build the app.",
				AgentID:     &agentID,
				Repos:       []string{"app"},
			},
		},
	}
	createRR := httptest.NewRecorder()
	testHandler.CreatePipeline(createRR, newPipelineRequest(t, http.MethodPost, "/api/pipelines", body))
	if createRR.Code != http.StatusCreated {
		t.Fatalf("CreatePipeline status = %d body=%s", createRR.Code, createRR.Body.String())
	}
	var pipeline PipelineResponse
	if err := json.Unmarshal(createRR.Body.Bytes(), &pipeline); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(pipeline.Nodes) != 1 || len(pipeline.Nodes[0].Repos) != 1 || pipeline.Nodes[0].Repos[0] != "app" {
		t.Fatalf("pipeline node repos were not persisted: %#v", pipeline.Nodes)
	}

	runRR := httptest.NewRecorder()
	testHandler.RunPipeline(runRR, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+pipeline.ID+"/run", runPipelineRequest{ProjectID: &projectID}), "id", pipeline.ID))
	if runRR.Code != http.StatusCreated {
		t.Fatalf("RunPipeline status = %d body=%s", runRR.Code, runRR.Body.String())
	}
	var run PipelineRunResponse
	if err := json.Unmarshal(runRR.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	var description string
	if err := testPool.QueryRow(context.Background(), `SELECT description FROM issue WHERE id = $1`, run.Nodes[0].IssueID).Scan(&description); err != nil {
		t.Fatalf("load child issue description: %v", err)
	}
	if !strings.Contains(description, "Target repositories:") ||
		!strings.Contains(description, "- app: https://github.com/acme/app.git") {
		t.Fatalf("child issue description missing target repo:\n%s", description)
	}
}

func TestReviewGatePassMarksDoneAndEnqueuesDownstream(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createReviewGatePipelineForTest(t, "Review gate pass", agentID)
	run := runPipelineForTest(t, pipeline.ID)
	issues := runIssueByNode(run)

	if !issueDescriptionContains(t, issues["spec-review"], "review_gate") {
		t.Fatalf("spec review issue description missing review gate contract")
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'done' WHERE id = $1`, issues["implement"]); err != nil {
		t.Fatalf("mark implementation done: %v", err)
	}
	testHandler.enqueueUnblockedIssueTasks(context.Background(), parseUUID(issues["implement"]))
	specTaskID := latestTaskForIssue(t, issues["spec-review"])

	completeReviewGateTaskForTest(t, specTaskID, `{
		"review_gate": {
			"status": "pass",
			"summary": "Implementation satisfies the accepted spec.",
			"findings": [],
			"checked_against": ["accepted spec"]
		}
	}`)

	var specStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issues["spec-review"]).Scan(&specStatus); err != nil {
		t.Fatalf("load spec review status: %v", err)
	}
	if specStatus != "done" {
		t.Fatalf("spec review status = %s, want done", specStatus)
	}
	if taskCount := taskCountForIssue(t, issues["code-review"]); taskCount != 1 {
		t.Fatalf("code review task count = %d, want 1", taskCount)
	}
}

func TestReviewGateFailBlocksDownstream(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createReviewGatePipelineForTest(t, "Review gate fail", agentID)
	run := runPipelineForTest(t, pipeline.ID)
	issues := runIssueByNode(run)

	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'done' WHERE id = $1`, issues["implement"]); err != nil {
		t.Fatalf("mark implementation done: %v", err)
	}
	testHandler.enqueueUnblockedIssueTasks(context.Background(), parseUUID(issues["implement"]))
	specTaskID := latestTaskForIssue(t, issues["spec-review"])

	completeReviewGateTaskForTest(t, specTaskID, `{
		"review_gate": {
			"status": "fail",
			"summary": "Required behavior is missing.",
			"findings": [
				{ "severity": "blocker", "title": "Missing behavior", "details": "The implementation does not satisfy the spec." }
			],
			"checked_against": ["accepted spec"]
		}
	}`)

	var specStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issues["spec-review"]).Scan(&specStatus); err != nil {
		t.Fatalf("load spec review status: %v", err)
	}
	if specStatus != "blocked" {
		t.Fatalf("spec review status = %s, want blocked", specStatus)
	}
	if taskCount := taskCountForIssue(t, issues["code-review"]); taskCount != 0 {
		t.Fatalf("code review task count = %d, want 0", taskCount)
	}
	var repairID string
	var repairAssigneeID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, assignee_id::text
		FROM issue
		WHERE origin_type = 'review_gate_repair' AND origin_id = $1
	`, issues["spec-review"]).Scan(&repairID, &repairAssigneeID); err != nil {
		t.Fatalf("load direct pipeline repair issue: %v", err)
	}
	if repairAssigneeID != agentID {
		t.Fatalf("repair assignee = %s, want %s", repairAssigneeID, agentID)
	}
	var dependencyCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue_dependency
		WHERE issue_id = $1 AND depends_on_issue_id = $2 AND type = 'blocked_by'
	`, issues["spec-review"], repairID).Scan(&dependencyCount); err != nil {
		t.Fatalf("count direct pipeline repair dependency: %v", err)
	}
	if dependencyCount != 1 {
		t.Fatalf("direct pipeline repair dependency count = %d, want 1", dependencyCount)
	}
}

func TestReviewGateMalformedOutputBlocksDownstream(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createReviewGatePipelineForTest(t, "Review gate malformed", agentID)
	run := runPipelineForTest(t, pipeline.ID)
	issues := runIssueByNode(run)

	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status = 'done' WHERE id = $1`, issues["implement"]); err != nil {
		t.Fatalf("mark implementation done: %v", err)
	}
	testHandler.enqueueUnblockedIssueTasks(context.Background(), parseUUID(issues["implement"]))
	specTaskID := latestTaskForIssue(t, issues["spec-review"])

	completeReviewGateTaskForTest(t, specTaskID, `review passed`)

	var specStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issues["spec-review"]).Scan(&specStatus); err != nil {
		t.Fatalf("load spec review status: %v", err)
	}
	if specStatus != "blocked" {
		t.Fatalf("spec review status = %s, want blocked", specStatus)
	}
	if taskCount := taskCountForIssue(t, issues["code-review"]); taskCount != 0 {
		t.Fatalf("code review task count = %d, want 0", taskCount)
	}
	var commentContent string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content
		FROM comment
		WHERE issue_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, issues["spec-review"]).Scan(&commentContent); err != nil {
		t.Fatalf("load malformed review comment: %v", err)
	}
	if !strings.Contains(commentContent, "valid review_gate JSON") {
		t.Fatalf("malformed review comment = %q", commentContent)
	}
	var repairCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue
		WHERE origin_type = 'review_gate_repair' AND origin_id = $1
	`, issues["spec-review"]).Scan(&repairCount); err != nil {
		t.Fatalf("count malformed repair issues: %v", err)
	}
	if repairCount != 0 {
		t.Fatalf("malformed review repair issue count = %d, want 0", repairCount)
	}
}

func TestRunPipelineRejectsArchivedNodeAgent(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Pipeline Archived Agent", nil)
	pipeline := createPipelineForTest(t, "Archived agent pipeline", agentID)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}

	rr := httptest.NewRecorder()
	testHandler.RunPipeline(rr, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+pipeline.ID+"/run", nil), "id", pipeline.ID))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RunPipeline status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestInternalPlannerIssueCompletionCreatesPipelinePlanDraft(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Planner Pipeline Worker "+uuid.NewString(), nil)
	pipeline := createPipelineForTest(t, "Planner selected pipeline "+uuid.NewString(), agentID)
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Plan this workflow",
		"description":   "Create the implementation pipeline.",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	var issueID *string
	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, issue_id::text, context
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID, &issueID, &contextJSON); err != nil {
		t.Fatalf("load planner task: %v", err)
	}
	if issueID != nil {
		t.Fatalf("planner task should be context-only, got issue_id %s", *issueID)
	}
	var ctxPayload map[string]any
	if err := json.Unmarshal(contextJSON, &ctxPayload); err != nil {
		t.Fatalf("decode task context: %v", err)
	}
	if ctxPayload["type"] != "issue_plan" || ctxPayload["source_issue_id"] != source.ID || ctxPayload["plan_id"] == "" {
		t.Fatalf("unexpected planner task context: %#v", ctxPayload)
	}
	if ctxPayload["phase"] != "spec" {
		t.Fatalf("planner task phase = %#v, want spec", ctxPayload["phase"])
	}

	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	output := `{
		"needs_plan": true,
		"reason": "needs multiple pipeline stages",
		"pipeline_id": "` + pipeline.ID + `",
		"title": "Pipeline plan",
		"pipeline": {
			"id": "` + pipeline.ID + `",
			"nodes": [
				{ "key": "design", "title": "Draft design", "description": "Design the workflow.", "agent_id": "` + agentID + `" },
				{ "key": "build", "title": "Build workflow", "description": "Implement it.", "agent_id": "` + agentID + `", "depends_on_node_keys": ["design"] }
			]
		}
	}`
	completePlannerTaskForTest(t, itemsTaskID, output)

	var planStatus string
	var parentIssueID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, parent_issue_id::text
		FROM plan
		WHERE id = $1
	`, planID).Scan(&planStatus, &parentIssueID); err != nil {
		t.Fatalf("load planner plan: %v", err)
	}
	if planStatus != "ready" || parentIssueID != source.ID {
		t.Fatalf("plan status/parent = %s/%s, want ready/%s", planStatus, parentIssueID, source.ID)
	}

	var childCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue
		WHERE parent_issue_id = $1
	`, source.ID).Scan(&childCount); err != nil {
		t.Fatalf("count child issues: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("child issue count = %d, want 0 before committing the plan", childCount)
	}
	var runCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM pipeline_run
		WHERE pipeline_id = $1 AND parent_issue_id = $2
	`, pipeline.ID, source.ID).Scan(&runCount); err != nil {
		t.Fatalf("count pipeline run: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("pipeline run count = %d, want 0 before committing the plan", runCount)
	}

	var itemCount int
	var secondAgentID string
	var deps []int32
	var generatedIssueID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM plan_item
		WHERE plan_id = $1
	`, planID).Scan(&itemCount); err != nil {
		t.Fatalf("count plan items: %v", err)
	}
	if itemCount != 2 {
		t.Fatalf("plan item count = %d, want 2", itemCount)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT recommended_agent_id::text, depends_on_positions, generated_issue_id::text
		FROM plan_item
		WHERE plan_id = $1 AND position = 2
	`, planID).Scan(&secondAgentID, &deps, &generatedIssueID); err != nil {
		t.Fatalf("load second plan item: %v", err)
	}
	if secondAgentID != agentID || len(deps) != 1 || deps[0] != 1 || generatedIssueID != nil {
		t.Fatalf("second plan item agent/deps/generated = %s/%v/%v, want %s/[1]/nil", secondAgentID, deps, generatedIssueID, agentID)
	}
}

func TestInternalPlannerPipelineManualNodeCreatesHumanConfirmationDraft(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Planner Review Gate Worker "+uuid.NewString(), nil)
	pipeline := createReviewGatePipelineForTest(t, "Planner review gated "+uuid.NewString(), agentID)
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Plan review gated delivery",
		"description":   "Create a pipeline with a final manual handoff.",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID); err != nil {
		t.Fatalf("load planner task: %v", err)
	}
	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	output := `{
		"needs_plan": true,
		"reason": "needs gated delivery",
		"pipeline_id": "` + pipeline.ID + `",
		"title": "Review gated plan",
		"pipeline": {
			"id": "` + pipeline.ID + `",
			"nodes": [
				{
					"key": "ready-for-human",
					"title": "Ready for human",
					"description": "Human handoff",
					"confirmation_question": "Can this delivery be handed off?",
					"confirmation_reason": "A human must accept release risk.",
					"required_evidence": ["Release owner approval", "Verification notes"]
				}
			]
		}
	}`
	completePlannerTaskForTest(t, itemsTaskID, output)

	var planStatus string
	var planError *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, error
		FROM plan
		WHERE id = $1
	`, planID).Scan(&planStatus, &planError); err != nil {
		t.Fatalf("load plan status: %v", err)
	}
	if planStatus != "ready" {
		errText := ""
		if planError != nil {
			errText = *planError
		}
		t.Fatalf("plan status = %s error=%q, want ready", planStatus, errText)
	}

	var manualKind string
	var manualAgentID *string
	var question string
	var reason string
	var evidence []string
	if err := testPool.QueryRow(context.Background(), `
		SELECT execution_kind, recommended_agent_id::text, confirmation_question, confirmation_reason, required_evidence
		FROM plan_item
		WHERE plan_id = $1 AND execution_kind = 'human_confirmation'
	`, planID).Scan(&manualKind, &manualAgentID, &question, &reason, &evidence); err != nil {
		titles := planItemTitlesForTest(t, planID)
		t.Fatalf("load manual plan item: %v; items=%v", err, titles)
	}
	if manualKind != "human_confirmation" || manualAgentID != nil {
		t.Fatalf("manual item kind/agent = %s/%v, want human_confirmation/nil", manualKind, manualAgentID)
	}
	if question != "Can this delivery be handed off?" || reason != "A human must accept release risk." || len(evidence) != 2 {
		t.Fatalf("manual confirmation contract = %q/%q/%v", question, reason, evidence)
	}

	var reviewKinds []string
	rows, err := testPool.Query(context.Background(), `
		SELECT execution_kind
		FROM plan_item
		WHERE plan_id = $1 AND title IN ('Spec review', 'Code review')
		ORDER BY position
	`, planID)
	if err != nil {
		t.Fatalf("load review plan items: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan review plan item: %v", err)
		}
		reviewKinds = append(reviewKinds, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate review plan items: %v", err)
	}
	if len(reviewKinds) != 2 || reviewKinds[0] != "agent_task" || reviewKinds[1] != "agent_task" {
		t.Fatalf("review item execution kinds = %v, want agent_task/agent_task", reviewKinds)
	}

	var reviewNodeTypes []string
	rows, err = testPool.Query(context.Background(), `
		SELECT node_type
		FROM plan_item
		WHERE plan_id = $1 AND title IN ('Spec review', 'Code review')
		ORDER BY position
	`, planID)
	if err != nil {
		t.Fatalf("load review plan item node types: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeType string
		if err := rows.Scan(&nodeType); err != nil {
			t.Fatalf("scan review plan item node type: %v", err)
		}
		reviewNodeTypes = append(reviewNodeTypes, nodeType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate review plan item node types: %v", err)
	}
	if len(reviewNodeTypes) != 2 || reviewNodeTypes[0] != "spec_review" || reviewNodeTypes[1] != "code_review" {
		t.Fatalf("review item node types = %v, want spec_review/code_review", reviewNodeTypes)
	}
}

func TestPlanItemCodeReviewFailCreatesRepairAndRerunsReview(t *testing.T) {
	workerID := createHandlerTestAgent(t, "Review Repair Worker "+uuid.NewString(), nil)
	reviewerID := createHandlerTestAgent(t, "Review Repair Reviewer "+uuid.NewString(), nil)
	pipeline := createReviewGatePipelineWithAgentsForTest(t, "Plan review repair "+uuid.NewString(), workerID, reviewerID)
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Ship review repair workflow",
		"description":   "Use a review-gated pipeline and repair failed code review automatically.",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID); err != nil {
		t.Fatalf("load planner task: %v", err)
	}
	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	completePlannerTaskForTest(t, itemsTaskID, `{
		"needs_plan": true,
		"reason": "needs review gated implementation",
		"pipeline_id": "`+pipeline.ID+`",
		"title": "Review repair plan",
		"parent_issue": { "title": "Review repair parent", "description": "Parent issue" },
		"pipeline": {
			"id": "`+pipeline.ID+`",
			"nodes": [
				{ "key": "ready-for-human", "title": "Ready for human" }
			]
		}
	}`)

	var codeReviewItemID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM plan_item
		WHERE plan_id = $1 AND node_type = 'code_review'
	`, planID).Scan(&codeReviewItemID); err != nil {
		t.Fatalf("load code review plan item: %v", err)
	}

	commitRR := httptest.NewRecorder()
	testHandler.CommitPlan(commitRR, withURLParam(newPipelineRequest(t, "POST", "/api/plans/"+planID+"/commit", map[string]any{
		"acknowledged_human_confirmation_item_ids": []string{humanConfirmationItemIDForPlan(t, planID)},
	}), "id", planID))
	if commitRR.Code != http.StatusOK {
		t.Fatalf("CommitPlan status = %d body=%s", commitRR.Code, commitRR.Body.String())
	}

	issues := childIssuesByTitleForTest(t, source.ID)
	implementIssueID := issues["Implement"]
	specReviewIssueID := issues["Spec review"]
	codeReviewIssueID := issues["Code review"]
	if implementIssueID == "" || specReviewIssueID == "" || codeReviewIssueID == "" {
		t.Fatalf("missing committed review-gated issues: %#v", issues)
	}

	var codeReviewDescription string
	var codeReviewOriginType string
	var codeReviewOriginID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT description, origin_type, origin_id::text
		FROM issue
		WHERE id = $1
	`, codeReviewIssueID).Scan(&codeReviewDescription, &codeReviewOriginType, &codeReviewOriginID); err != nil {
		t.Fatalf("load code review issue: %v", err)
	}
	if codeReviewOriginType != "plan_item" || codeReviewOriginID != codeReviewItemID {
		t.Fatalf("code review origin = %s/%s, want plan_item/%s", codeReviewOriginType, codeReviewOriginID, codeReviewItemID)
	}
	if !strings.Contains(codeReviewDescription, "review_gate") {
		t.Fatalf("code review issue description missing review_gate contract:\n%s", codeReviewDescription)
	}

	completeAgentTaskWithBranchForTest(t, latestTaskForIssue(t, implementIssueID), "implementation completed", "agent/backend-engineer/LOC-5", "abc123")
	completeReviewGateTaskForTest(t, latestTaskForIssue(t, specReviewIssueID), `{
		"review_gate": {
			"status": "pass",
			"summary": "Spec requirements are covered.",
			"findings": [],
			"checked_against": ["approved spec"]
		}
	}`)
	codeReviewTaskID := latestTaskForIssue(t, codeReviewIssueID)
	completeReviewGateTaskWithAgentCommentForTest(t, codeReviewTaskID, `{
		"review_gate": {
			"status": "fail",
			"summary": "Implementation leaks rooms.",
			"findings": [
				{ "severity": "blocker", "title": "Room leak", "details": "Finished rooms are never deleted." }
			],
			"checked_against": ["code review"]
		}
	}`, "Code review completed and posted. Found blocking issues.")

	var codeReviewStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, codeReviewIssueID).Scan(&codeReviewStatus); err != nil {
		t.Fatalf("load code review status: %v", err)
	}
	if codeReviewStatus != "blocked" {
		t.Fatalf("code review status = %s, want blocked", codeReviewStatus)
	}

	var repairID string
	var repairAssigneeID string
	var repairDescription string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, assignee_id::text, description
		FROM issue
		WHERE origin_type = 'review_gate_repair' AND origin_id = $1
	`, codeReviewIssueID).Scan(&repairID, &repairAssigneeID, &repairDescription); err != nil {
		t.Fatalf("load repair issue: %v", err)
	}
	if repairAssigneeID != workerID {
		t.Fatalf("repair assignee = %s, want %s", repairAssigneeID, workerID)
	}
	if !strings.Contains(repairDescription, "Room leak") || !strings.Contains(repairDescription, "Target implementation issue") {
		t.Fatalf("repair description missing review findings: %s", repairDescription)
	}
	if taskCount := taskCountForIssue(t, repairID); taskCount != 1 {
		t.Fatalf("repair task count = %d, want 1", taskCount)
	}
	repairIssue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(repairID))
	if err != nil {
		t.Fatalf("load repair issue for branch target: %v", err)
	}
	branchTarget, err := testHandler.repairIssueBranchTarget(context.Background(), repairIssue)
	if err != nil {
		t.Fatalf("load repair branch target: %v", err)
	}
	if branchTarget.BranchName != "agent/backend-engineer/LOC-5" || branchTarget.CommitSHA != "abc123" {
		t.Fatalf("repair branch target = %s/%s, want original implementation branch", branchTarget.BranchName, branchTarget.CommitSHA)
	}
	var dependencyCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue_dependency
		WHERE issue_id = $1 AND depends_on_issue_id = $2 AND type = 'blocked_by'
	`, codeReviewIssueID, repairID).Scan(&dependencyCount); err != nil {
		t.Fatalf("count repair dependency: %v", err)
	}
	if dependencyCount != 1 {
		t.Fatalf("repair dependency count = %d, want 1", dependencyCount)
	}

	completeAgentTaskWithBranchForTest(t, latestTaskForIssue(t, repairID), "repair completed", "agent/backend-engineer/LOC-5", "def456")
	var repairStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, repairID).Scan(&repairStatus); err != nil {
		t.Fatalf("load repair status: %v", err)
	}
	if repairStatus != "done" {
		t.Fatalf("repair status = %s, want done", repairStatus)
	}
	if taskCount := taskCountForIssue(t, codeReviewIssueID); taskCount != 2 {
		t.Fatalf("code review task count after repair = %d, want 2", taskCount)
	}
	reviewIssue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(codeReviewIssueID))
	if err != nil {
		t.Fatalf("load review issue for latest repair target: %v", err)
	}
	latestRepairTarget, err := testHandler.latestReviewRepairTarget(context.Background(), reviewIssue)
	if err != nil {
		t.Fatalf("load latest repair target: %v", err)
	}
	if latestRepairTarget.BranchName != "agent/backend-engineer/LOC-5" || latestRepairTarget.CommitSHA != "def456" {
		t.Fatalf("latest repair target = %s/%s, want repair branch", latestRepairTarget.BranchName, latestRepairTarget.CommitSHA)
	}
}

func TestInternalPlannerIssueCompletionCreatesDirectPlanDraft(t *testing.T) {
	workerID := createHandlerTestAgent(t, "Planner Direct Worker "+uuid.NewString(), nil)
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Fix settings crash",
		"description":   "Opening settings crashes after login.",
		"status":        "todo",
		"priority":      "high",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID); err != nil {
		t.Fatalf("load planner task: %v", err)
	}

	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	output := `{
		"needs_plan": false,
		"reason": "single-agent bug fix",
		"direct_issue": {
			"title": "Fix settings crash after login",
			"description": "Opening settings crashes after login. Investigate and patch the bug.",
			"acceptance_criteria": ["Settings opens after login", "Regression test covers the route"],
			"suggested_test_commands": ["go test ./internal/handler"],
			"context_resources": ["server/internal/handler/plan.go"],
			"risk_notes": ["Do not change unrelated auth behavior"],
			"recommended_agent_id": "` + workerID + `",
			"match_score": 94,
			"match_reason": "Worker is the best fit for product bug fixes."
		}
	}`
	completePlannerTaskForTest(t, itemsTaskID, output)

	var planStatus string
	var parentIssueID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, parent_issue_id::text
		FROM plan
		WHERE id = $1
	`, planID).Scan(&planStatus, &parentIssueID); err != nil {
		t.Fatalf("load direct plan: %v", err)
	}
	if planStatus != "ready" || parentIssueID != source.ID {
		t.Fatalf("plan status/parent = %s/%s, want ready/%s", planStatus, parentIssueID, source.ID)
	}

	var itemID string
	var assigneeID string
	var acceptanceCriteria []string
	var suggestedTestCommands []string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, recommended_agent_id::text, acceptance_criteria, suggested_test_commands
		FROM plan_item
		WHERE plan_id = $1 AND title = 'Fix settings crash after login'
	`, planID).Scan(&itemID, &assigneeID, &acceptanceCriteria, &suggestedTestCommands); err != nil {
		t.Fatalf("load direct plan item: %v", err)
	}
	if assigneeID != workerID {
		t.Fatalf("plan item assignee = %s, want %s", assigneeID, workerID)
	}
	if len(acceptanceCriteria) != 2 || acceptanceCriteria[0] != "Settings opens after login" {
		t.Fatalf("plan item acceptance criteria = %v", acceptanceCriteria)
	}
	if len(suggestedTestCommands) != 1 || suggestedTestCommands[0] != "go test ./internal/handler" {
		t.Fatalf("plan item test commands = %v", suggestedTestCommands)
	}

	var childCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue
		WHERE parent_issue_id = $1 AND title = 'Fix settings crash after login'
	`, source.ID).Scan(&childCount); err != nil {
		t.Fatalf("count direct child issues: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("child issue count = %d, want 0 before committing the plan", childCount)
	}

	var workerTaskCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE agent_id = $1 AND issue_id IS NOT NULL
	`, workerID).Scan(&workerTaskCount); err != nil {
		t.Fatalf("count worker task: %v", err)
	}
	if workerTaskCount != 0 {
		t.Fatalf("worker task count = %d, want 0 before committing the plan", workerTaskCount)
	}

	commitRR := httptest.NewRecorder()
	testHandler.CommitPlan(commitRR, withURLParam(newPipelineRequest(t, "POST", "/api/plans/"+planID+"/commit", nil), "id", planID))
	if commitRR.Code != http.StatusOK {
		t.Fatalf("CommitPlan status = %d body=%s", commitRR.Code, commitRR.Body.String())
	}
	var childID string
	var childDescription string
	var originType string
	var originID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, description, origin_type, origin_id::text
		FROM issue
		WHERE parent_issue_id = $1 AND title = 'Fix settings crash after login' AND assignee_id = $2
	`, source.ID, workerID).Scan(&childID, &childDescription, &originType, &originID); err != nil {
		t.Fatalf("load committed child issue: %v", err)
	}
	if originType != "plan_item" || originID != itemID {
		t.Fatalf("committed child origin = %s/%s, want plan_item/%s", originType, originID, itemID)
	}
	if !strings.Contains(childDescription, "Acceptance criteria:") || !strings.Contains(childDescription, "Settings opens after login") || !strings.Contains(childDescription, "Suggested test commands:") {
		t.Fatalf("committed child description missing execution contract: %s", childDescription)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE agent_id = $1 AND issue_id = $2
	`, workerID, childID).Scan(&workerTaskCount); err != nil {
		t.Fatalf("count committed worker task: %v", err)
	}
	if workerTaskCount != 1 {
		t.Fatalf("committed worker task count = %d, want 1", workerTaskCount)
	}

	completeAgentTaskForTest(t, latestTaskForIssue(t, childID), "implementation completed")
	var childStatus string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status
		FROM issue
		WHERE id = $1
	`, childID).Scan(&childStatus); err != nil {
		t.Fatalf("load completed child status: %v", err)
	}
	if childStatus != "done" {
		t.Fatalf("plan agent task child status = %s, want done", childStatus)
	}
}

func TestHumanConfirmationPlanItemRequiresAckAndBlocksDownstream(t *testing.T) {
	workerID := createHandlerTestAgent(t, "Human Confirmation Worker "+uuid.NewString(), nil)
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Prepare risky cleanup",
		"description":   "Cleanup needs explicit human approval before implementation.",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID); err != nil {
		t.Fatalf("load planner task: %v", err)
	}
	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	output := `{
		"needs_plan": true,
		"reason": "implementation must wait for human approval",
		"title": "Risky cleanup plan",
		"parent_issue": { "title": "Prepare risky cleanup", "description": "Cleanup needs explicit human approval." },
		"items": [
			{
				"title": "Approve destructive cleanup",
				"description": "Confirm the cleanup scope before implementation starts.",
				"execution_kind": "human_confirmation",
				"confirmation_question": "Is the destructive cleanup approved for this branch?",
				"confirmation_reason": "The cleanup can delete user-authored state and cannot be safely approved by the planner.",
				"required_evidence": ["Reviewed affected paths", "Rollback path is documented"],
				"selected": true
			},
			{
				"title": "Implement approved cleanup",
				"description": "Implement only after approval.",
				"recommended_agent_id": "` + workerID + `",
				"match_score": 95,
				"match_reason": "Worker can implement the cleanup.",
				"depends_on_positions": [1],
				"selected": true
			}
		]
	}`
	completePlannerTaskForTest(t, itemsTaskID, output)

	var confirmationItemID string
	var executionKind string
	var recommendedAgentID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, execution_kind, recommended_agent_id::text
		FROM plan_item
		WHERE plan_id = $1 AND position = 1
	`, planID).Scan(&confirmationItemID, &executionKind, &recommendedAgentID); err != nil {
		t.Fatalf("load confirmation plan item: %v", err)
	}
	if executionKind != "human_confirmation" || recommendedAgentID != nil {
		t.Fatalf("confirmation item kind/agent = %s/%v, want human_confirmation/nil", executionKind, recommendedAgentID)
	}

	commitRR := httptest.NewRecorder()
	testHandler.CommitPlan(commitRR, withURLParam(newPipelineRequest(t, "POST", "/api/plans/"+planID+"/commit", nil), "id", planID))
	if commitRR.Code != http.StatusBadRequest {
		t.Fatalf("CommitPlan without ack status = %d body=%s, want 400", commitRR.Code, commitRR.Body.String())
	}

	commitRR = httptest.NewRecorder()
	testHandler.CommitPlan(commitRR, withURLParam(newPipelineRequest(t, "POST", "/api/plans/"+planID+"/commit", map[string]any{
		"acknowledged_human_confirmation_item_ids": []string{confirmationItemID},
	}), "id", planID))
	if commitRR.Code != http.StatusOK {
		t.Fatalf("CommitPlan with ack status = %d body=%s", commitRR.Code, commitRR.Body.String())
	}

	var confirmationIssueID string
	var confirmationAssigneeType *string
	var confirmationDescription string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, assignee_type, description
		FROM issue
		WHERE parent_issue_id = $1 AND title = 'Approve destructive cleanup'
	`, source.ID).Scan(&confirmationIssueID, &confirmationAssigneeType, &confirmationDescription); err != nil {
		t.Fatalf("load confirmation issue: %v", err)
	}
	if confirmationAssigneeType != nil {
		t.Fatalf("confirmation assignee type = %v, want nil", confirmationAssigneeType)
	}
	if !strings.Contains(confirmationDescription, "Human confirmation question:") || !strings.Contains(confirmationDescription, "Required evidence:") {
		t.Fatalf("confirmation issue description missing contract:\n%s", confirmationDescription)
	}

	var workerIssueID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM issue
		WHERE parent_issue_id = $1 AND title = 'Implement approved cleanup'
	`, source.ID).Scan(&workerIssueID); err != nil {
		t.Fatalf("load worker issue: %v", err)
	}
	if taskCount := taskCountForIssue(t, workerIssueID); taskCount != 0 {
		t.Fatalf("worker task count before confirmation = %d, want 0", taskCount)
	}

	doneRR := httptest.NewRecorder()
	doneReq := newRequest("PUT", "/api/issues/"+confirmationIssueID, map[string]any{"status": "done"})
	doneReq = withURLParam(doneReq, "id", confirmationIssueID)
	testHandler.UpdateIssue(doneRR, doneReq)
	if doneRR.Code != http.StatusOK {
		t.Fatalf("UpdateIssue done status = %d body=%s", doneRR.Code, doneRR.Body.String())
	}
	if taskCount := taskCountForIssue(t, workerIssueID); taskCount != 1 {
		t.Fatalf("worker task count after confirmation = %d, want 1", taskCount)
	}
}

func TestInternalPlannerIssueCompletionCreatesUnassignedDirectPlanDraft(t *testing.T) {
	plannerID := createInternalPlannerAgentForTest(t)

	rr := httptest.NewRecorder()
	testHandler.CreateIssue(rr, newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":         "Fix unknown subsystem crash",
		"description":   "The crash belongs to a subsystem with no suitable current agent.",
		"status":        "todo",
		"assignee_type": "agent",
		"assignee_id":   plannerID,
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateIssue status = %d body=%s", rr.Code, rr.Body.String())
	}
	var source IssueResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &source); err != nil {
		t.Fatalf("decode source issue: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE agent_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, plannerID).Scan(&taskID); err != nil {
		t.Fatalf("load planner task: %v", err)
	}

	planID, itemsTaskID := completeSpecAndApproveForTest(t, taskID, source.ID)

	output := `{
		"needs_plan": false,
		"reason": "single bug but no suitable agent exists",
		"direct_issue": {
			"title": "Fix unknown subsystem crash",
			"description": "Investigate the unknown subsystem crash.",
			"recommended_agent_id": "",
			"match_score": 0,
			"match_reason": "No current agent matches this subsystem.",
			"missing_capability": "Unknown subsystem maintainer"
		}
	}`
	completePlannerTaskForTest(t, itemsTaskID, output)

	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM plan
		WHERE task_id = $1 AND status = 'ready' AND parent_issue_id = $2
	`, itemsTaskID, source.ID).Scan(&planID); err != nil {
		t.Fatalf("load unassigned plan: %v", err)
	}
	var assigneeID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT recommended_agent_id::text
		FROM plan_item
		WHERE plan_id = $1 AND title = 'Fix unknown subsystem crash'
	`, planID).Scan(&assigneeID); err != nil {
		t.Fatalf("load unassigned plan item: %v", err)
	}
	if assigneeID != nil {
		t.Fatalf("plan item assignee = %v, want nil", assigneeID)
	}

	var childCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM issue
		WHERE parent_issue_id = $1 AND title = 'Fix unknown subsystem crash'
	`, source.ID).Scan(&childCount); err != nil {
		t.Fatalf("count child issues: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("child issue count = %d, want 0 before committing the plan", childCount)
	}
}

func TestValidatePipelineYAMLImportResolvesAgent(t *testing.T) {
	agentName := "Pipeline YAML Agent " + uuid.NewString()
	agentID := createHandlerTestAgent(t, agentName, nil)
	content := `
version: 1
name: YAML validation pipeline
description: Imported from local YAML
nodes:
  - key: design
    type: issue
    title: Design
    agent: "` + agentName + `"
    position: { x: 100, y: 120 }
  - key: build
    type: spec_review
    title: Build
    depends_on: [design]
    agent: "` + agentName + `"
    repos: [app, api]
    position: { x: 360, y: 120 }
`

	rr := httptest.NewRecorder()
	testHandler.ValidatePipelineYAMLImport(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines/import/validate", importPipelineYAMLRequest{Content: content}))
	if rr.Code != http.StatusOK {
		t.Fatalf("ValidatePipelineYAMLImport status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp pipelineImportValidationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if !resp.Valid || resp.Pipeline == nil {
		t.Fatalf("expected valid import preview, got %#v", resp)
	}
	if len(resp.Pipeline.Nodes) != 2 {
		t.Fatalf("expected 2 preview nodes, got %#v", resp.Pipeline.Nodes)
	}
	if resp.Pipeline.Nodes[0].AgentID == nil || *resp.Pipeline.Nodes[0].AgentID != agentID {
		t.Fatalf("agent was not resolved to id %s: %#v", agentID, resp.Pipeline.Nodes[0].AgentID)
	}
	if got := resp.Pipeline.Nodes[1].DependsOnNodeKeys; len(got) != 1 || got[0] != "design" {
		t.Fatalf("depends_on was not normalized: %#v", got)
	}
	if got := resp.Pipeline.Nodes[1].Repos; len(got) != 2 || got[0] != "app" || got[1] != "api" {
		t.Fatalf("repos were not normalized: %#v", got)
	}
	if got := resp.Pipeline.Nodes[1].Type; got != "spec_review" {
		t.Fatalf("node type = %s, want spec_review", got)
	}
}

func TestImportPipelineYAMLCreatesPipeline(t *testing.T) {
	agentName := "Pipeline YAML Create Agent " + uuid.NewString()
	agentID := createHandlerTestAgent(t, agentName, nil)
	content := `
version: 1
name: YAML create pipeline
description: Imported from a file
nodes:
  - key: first
    type: issue
    title: First node
    agent: "` + agentName + `"
  - key: second
    type: issue
    title: Second node
    agent_id: "` + agentID + `"
    depends_on: [first]
`

	rr := httptest.NewRecorder()
	testHandler.ImportPipelineYAML(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines/import", importPipelineYAMLRequest{Content: content}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("ImportPipelineYAML status = %d body=%s", rr.Code, rr.Body.String())
	}

	var pipeline PipelineResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pipeline); err != nil {
		t.Fatalf("decode import response: %v", err)
	}
	if pipeline.Name != "YAML create pipeline" || len(pipeline.Nodes) != 2 {
		t.Fatalf("unexpected imported pipeline: %#v", pipeline)
	}
	if pipeline.Nodes[0].AgentID == nil || *pipeline.Nodes[0].AgentID != agentID {
		t.Fatalf("first node agent = %#v, want %s", pipeline.Nodes[0].AgentID, agentID)
	}
	if got := pipeline.Nodes[1].DependsOnNodeKeys; len(got) != 1 || got[0] != "first" {
		t.Fatalf("second node dependencies = %#v", got)
	}
}

func TestImportPipelineYAMLUpdatesExistingPipeline(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createPipelineForTest(t, "YAML update source", agentID)
	content := `
version: 1
name: YAML updated pipeline
description: Replaced through import
nodes:
  - key: replacement
    type: manual
    title: Replacement node
`

	rr := httptest.NewRecorder()
	testHandler.ImportPipelineYAML(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines/import", importPipelineYAMLRequest{
		Content:    content,
		PipelineID: &pipeline.ID,
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("ImportPipelineYAML update status = %d body=%s", rr.Code, rr.Body.String())
	}

	var updated PipelineResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode import update response: %v", err)
	}
	if updated.ID != pipeline.ID || updated.Name != "YAML updated pipeline" {
		t.Fatalf("unexpected updated pipeline: %#v", updated)
	}
	if len(updated.Nodes) != 1 || updated.Nodes[0].Key != "replacement" || updated.Nodes[0].Type != "manual" {
		t.Fatalf("pipeline nodes were not replaced: %#v", updated.Nodes)
	}
}

func TestValidatePipelineYAMLImportReportsMissingAgent(t *testing.T) {
	content := `
version: 1
name: YAML missing agent pipeline
nodes:
  - key: only
    title: Only node
    agent: definitely-missing-agent
`

	rr := httptest.NewRecorder()
	testHandler.ValidatePipelineYAMLImport(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines/import/validate", importPipelineYAMLRequest{Content: content}))
	if rr.Code != http.StatusOK {
		t.Fatalf("ValidatePipelineYAMLImport status = %d body=%s", rr.Code, rr.Body.String())
	}

	var resp pipelineImportValidationResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if resp.Valid || len(resp.Errors) == 0 {
		t.Fatalf("expected validation errors, got %#v", resp)
	}
}

func TestArchivePipelineHidesListAndPreservesRunHistory(t *testing.T) {
	agentID := handlerTestAgentID(t)
	pipeline := createPipelineForTest(t, "Archive pipeline", agentID)

	runRR := httptest.NewRecorder()
	testHandler.RunPipeline(runRR, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+pipeline.ID+"/run", nil), "id", pipeline.ID))
	if runRR.Code != http.StatusCreated {
		t.Fatalf("RunPipeline status = %d body=%s", runRR.Code, runRR.Body.String())
	}

	deleteRR := httptest.NewRecorder()
	testHandler.DeletePipeline(deleteRR, withURLParam(newPipelineRequest(t, http.MethodDelete, "/api/pipelines/"+pipeline.ID, nil), "id", pipeline.ID))
	if deleteRR.Code != http.StatusNoContent {
		t.Fatalf("DeletePipeline status = %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	listRR := httptest.NewRecorder()
	testHandler.ListPipelines(listRR, newPipelineRequest(t, http.MethodGet, "/api/pipelines", nil))
	if listRR.Code != http.StatusOK {
		t.Fatalf("ListPipelines status = %d body=%s", listRR.Code, listRR.Body.String())
	}
	var listResp listPipelinesResponse
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, p := range listResp.Pipelines {
		if p.ID == pipeline.ID {
			t.Fatalf("archived pipeline still appears in list: %#v", p)
		}
	}

	var runCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM pipeline_run WHERE pipeline_id = $1`, pipeline.ID).Scan(&runCount); err != nil {
		t.Fatalf("count pipeline runs: %v", err)
	}
	if runCount == 0 {
		t.Fatalf("pipeline run history was not preserved")
	}
}

func completeSpecAndApproveForTest(t *testing.T, taskID, sourceIssueID string) (string, string) {
	t.Helper()

	specOutput := `{
		"spec": {
			"summary": "Reviewable implementation draft",
			"goal": "Create a safe executable plan after review.",
			"success_criteria": ["Spec is approved", "Items are generated after approval"],
			"in_scope": ["Plan drafting"],
			"out_of_scope": ["Committing issues before approval"],
			"approach": "Draft the spec first, then generate plan items.",
			"assumptions": ["Planner output is JSON"],
			"open_questions": []
		}
	}`
	completePlannerTaskForTest(t, taskID, specOutput)

	var planID string
	var planStatus string
	var parentIssueID string
	var specJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, status, parent_issue_id::text, spec
		FROM plan
		WHERE task_id = $1
	`, taskID).Scan(&planID, &planStatus, &parentIssueID, &specJSON); err != nil {
		t.Fatalf("load spec review plan: %v", err)
	}
	if planStatus != "spec_review" || parentIssueID != sourceIssueID {
		t.Fatalf("plan status/parent = %s/%s, want spec_review/%s", planStatus, parentIssueID, sourceIssueID)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode plan spec: %v", err)
	}
	if spec["summary"] != "Reviewable implementation draft" || spec["goal"] != "Create a safe executable plan after review." {
		t.Fatalf("unexpected stored spec: %#v", spec)
	}
	var itemCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM plan_item
		WHERE plan_id = $1
	`, planID).Scan(&itemCount); err != nil {
		t.Fatalf("count spec review plan items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("spec review plan item count = %d, want 0", itemCount)
	}

	approved := approvePlanSpecForTest(t, planID, nil)
	if approved.Status != "planning" || approved.TaskID == "" || approved.TaskID == taskID {
		t.Fatalf("approved plan status/task = %s/%s, want planning/new task", approved.Status, approved.TaskID)
	}
	if approved.SpecApprovedBy == nil || *approved.SpecApprovedBy != testUserID {
		t.Fatalf("spec approved by = %v, want %s", approved.SpecApprovedBy, testUserID)
	}
	if approved.SpecApprovedAt == nil {
		t.Fatalf("spec approved at is nil")
	}

	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT context
		FROM agent_task_queue
		WHERE id = $1
	`, approved.TaskID).Scan(&contextJSON); err != nil {
		t.Fatalf("load approved planner task context: %v", err)
	}
	var ctxPayload map[string]any
	if err := json.Unmarshal(contextJSON, &ctxPayload); err != nil {
		t.Fatalf("decode approved task context: %v", err)
	}
	if ctxPayload["type"] != "issue_plan" || ctxPayload["phase"] != "items" || ctxPayload["plan_id"] != planID {
		t.Fatalf("unexpected approved planner task context: %#v", ctxPayload)
	}
	specPayload, ok := ctxPayload["spec"].(map[string]any)
	if !ok || specPayload["goal"] != "Create a safe executable plan after review." {
		t.Fatalf("approved task missing spec context: %#v", ctxPayload)
	}

	return planID, approved.TaskID
}

func approvePlanSpecForTest(t *testing.T, planID string, body any) PlanResponse {
	t.Helper()
	rr := httptest.NewRecorder()
	testHandler.ApprovePlanSpec(rr, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/plans/"+planID+"/approve-spec", body), "id", planID))
	if rr.Code != http.StatusOK {
		t.Fatalf("ApprovePlanSpec status = %d body=%s", rr.Code, rr.Body.String())
	}
	var plan PlanResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode approve spec response: %v", err)
	}
	return plan
}

func completePlannerTaskForTest(t *testing.T, taskID, output string) {
	t.Helper()
	completeAgentTaskForTest(t, taskID, output)
}

func completeReviewGateTaskForTest(t *testing.T, taskID, output string) {
	t.Helper()
	completeAgentTaskForTest(t, taskID, output)
}

func completeReviewGateTaskWithAgentCommentForTest(t *testing.T, taskID, comment, output string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("mark review task running: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		SELECT t.issue_id, i.workspace_id, 'agent', t.agent_id, $2, 'comment'
		FROM agent_task_queue t
		JOIN issue i ON i.id = t.issue_id
		WHERE t.id = $1
	`, taskID, comment); err != nil {
		t.Fatalf("insert review gate comment: %v", err)
	}
	result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: taskID, Output: output})
	if _, err := testHandler.TaskService.CompleteTask(context.Background(), parseUUID(taskID), result, "", ""); err != nil {
		t.Fatalf("complete review gate task: %v", err)
	}
}

func completeAgentTaskForTest(t *testing.T, taskID, output string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("mark planner task running: %v", err)
	}
	result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: taskID, Output: output})
	if _, err := testHandler.TaskService.CompleteTask(context.Background(), parseUUID(taskID), result, "", ""); err != nil {
		t.Fatalf("complete agent task: %v", err)
	}
}

func completeAgentTaskWithBranchForTest(t *testing.T, taskID, output, branchName, commitSHA string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue
		SET status = 'running', started_at = now()
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("mark planner task running: %v", err)
	}
	result, _ := json.Marshal(map[string]any{
		"task_id":           taskID,
		"output":            output,
		"branch_name":       branchName,
		"branch_commit_sha": commitSHA,
	})
	if _, err := testHandler.TaskService.CompleteTask(context.Background(), parseUUID(taskID), result, "", ""); err != nil {
		t.Fatalf("complete agent task with branch: %v", err)
	}
}

func handlerTestAgentID(t *testing.T) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent
		WHERE workspace_id = $1 AND archived_at IS NULL
		ORDER BY created_at ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load handler test agent: %v", err)
	}
	return agentID
}

func createPipelineForTest(t *testing.T, name, agentID string) PipelineResponse {
	t.Helper()
	body := createPipelineRequest{
		Name:        name,
		Description: "Pipeline used by handler tests",
		Nodes: []upsertPipelineNodeRequest{
			{Key: "design", Type: "issue", Title: "Design", Description: "Design node", AgentID: &agentID},
			{Key: "build", Type: "issue", Title: "Build", Description: "Build node", AgentID: &agentID, DependsOnNodeKeys: []string{"design"}},
		},
	}
	rr := httptest.NewRecorder()
	testHandler.CreatePipeline(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines", body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreatePipeline status = %d body=%s", rr.Code, rr.Body.String())
	}
	var pipeline PipelineResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pipeline); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return pipeline
}

func createReviewGatePipelineForTest(t *testing.T, name, agentID string) PipelineResponse {
	t.Helper()
	return createReviewGatePipelineWithAgentsForTest(t, name, agentID, agentID)
}

func createReviewGatePipelineWithAgentsForTest(t *testing.T, name, implementAgentID, reviewAgentID string) PipelineResponse {
	t.Helper()
	body := createPipelineRequest{
		Name:        name,
		Description: "Pipeline with review gates",
		Nodes: []upsertPipelineNodeRequest{
			{Key: "implement", Type: "issue", Title: "Implement", Description: "Implement node", AgentID: &implementAgentID},
			{Key: "spec-review", Type: "spec_review", Title: "Spec review", Description: "Review spec compliance", AgentID: &reviewAgentID, DependsOnNodeKeys: []string{"implement"}},
			{Key: "code-review", Type: "code_review", Title: "Code review", Description: "Review code quality", AgentID: &reviewAgentID, DependsOnNodeKeys: []string{"spec-review"}},
			{Key: "ready-for-human", Type: "manual", Title: "Ready for human", Description: "Human handoff", DependsOnNodeKeys: []string{"code-review"}},
		},
	}
	rr := httptest.NewRecorder()
	testHandler.CreatePipeline(rr, newPipelineRequest(t, http.MethodPost, "/api/pipelines", body))
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreatePipeline status = %d body=%s", rr.Code, rr.Body.String())
	}
	var pipeline PipelineResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pipeline); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got := pipeline.Nodes[1].Type; got != "spec_review" {
		t.Fatalf("second node type = %s, want spec_review", got)
	}
	if got := pipeline.Nodes[2].Type; got != "code_review" {
		t.Fatalf("third node type = %s, want code_review", got)
	}
	if len(pipeline.Nodes) != 4 || pipeline.Nodes[3].Type != "manual" {
		t.Fatalf("manual node was not persisted: %#v", pipeline.Nodes)
	}
	return pipeline
}

func runPipelineForTest(t *testing.T, pipelineID string) PipelineRunResponse {
	t.Helper()
	rr := httptest.NewRecorder()
	testHandler.RunPipeline(rr, withURLParam(newPipelineRequest(t, http.MethodPost, "/api/pipelines/"+pipelineID+"/run", nil), "id", pipelineID))
	if rr.Code != http.StatusCreated {
		t.Fatalf("RunPipeline status = %d body=%s", rr.Code, rr.Body.String())
	}
	var run PipelineRunResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	return run
}

func humanConfirmationItemIDForPlan(t *testing.T, planID string) string {
	t.Helper()
	var itemID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM plan_item
		WHERE plan_id = $1 AND execution_kind = 'human_confirmation'
		ORDER BY position ASC
		LIMIT 1
	`, planID).Scan(&itemID); err != nil {
		t.Fatalf("load human confirmation plan item: %v", err)
	}
	return itemID
}

func childIssuesByTitleForTest(t *testing.T, parentIssueID string) map[string]string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT title, id::text
		FROM issue
		WHERE parent_issue_id = $1
	`, parentIssueID)
	if err != nil {
		t.Fatalf("list child issues: %v", err)
	}
	defer rows.Close()
	issues := map[string]string{}
	for rows.Next() {
		var title string
		var id string
		if err := rows.Scan(&title, &id); err != nil {
			t.Fatalf("scan child issue: %v", err)
		}
		issues[title] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate child issues: %v", err)
	}
	return issues
}

func listPipelinesForTest(t *testing.T) listPipelinesResponse {
	t.Helper()
	rr := httptest.NewRecorder()
	testHandler.ListPipelines(rr, newPipelineRequest(t, http.MethodGet, "/api/pipelines", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("ListPipelines status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp listPipelinesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return resp
}

func systemPipelineForTest(t *testing.T, systemKey string) PipelineResponse {
	t.Helper()
	resp := listPipelinesForTest(t)
	for _, pipeline := range resp.Pipelines {
		if pipeline.IsSystem && pipeline.SystemKey != nil && *pipeline.SystemKey == systemKey {
			return pipeline
		}
	}
	t.Fatalf("system pipeline %s not found", systemKey)
	return PipelineResponse{}
}

func strPtr(s string) *string {
	return &s
}

func runIssueByNode(run PipelineRunResponse) map[string]string {
	issues := make(map[string]string, len(run.Nodes))
	for _, node := range run.Nodes {
		issues[node.NodeKey] = node.IssueID
	}
	return issues
}

func latestTaskForIssue(t *testing.T, issueID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent_task_queue
		WHERE issue_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, issueID).Scan(&taskID); err != nil {
		t.Fatalf("load latest task for issue %s: %v", issueID, err)
	}
	return taskID
}

func taskCountForIssue(t *testing.T, issueID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_queue
		WHERE issue_id = $1
	`, issueID).Scan(&count); err != nil {
		t.Fatalf("count tasks for issue %s: %v", issueID, err)
	}
	return count
}

func planItemTitlesForTest(t *testing.T, planID string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT title || ':' || execution_kind
		FROM plan_item
		WHERE plan_id = $1
		ORDER BY position
	`, planID)
	if err != nil {
		t.Fatalf("list plan items: %v", err)
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("scan plan item title: %v", err)
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan items: %v", err)
	}
	return titles
}

func issueDescriptionContains(t *testing.T, issueID, needle string) bool {
	t.Helper()
	var description string
	if err := testPool.QueryRow(context.Background(), `SELECT coalesce(description, '') FROM issue WHERE id = $1`, issueID).Scan(&description); err != nil {
		t.Fatalf("load issue description %s: %v", issueID, err)
	}
	return strings.Contains(description, needle)
}

func createInternalPlannerAgentForTest(t *testing.T) string {
	t.Helper()

	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime
		SET metadata = jsonb_set(coalesce(metadata, '{}'::jsonb), '{capabilities}', '["issue_plan"]'::jsonb, true)
		WHERE id = $1
	`, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("mark handler test runtime plan-capable: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM agent
		WHERE workspace_id = $1 AND name = $2 AND is_internal = TRUE
		LIMIT 1
	`, testWorkspaceID, internalPlannerAgentName).Scan(&agentID); err == nil {
		return agentID
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, is_internal
		)
		VALUES ($1, $2, 'Built-in planner for tests', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb, true)
		RETURNING id
	`, testWorkspaceID, internalPlannerAgentName, handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("failed to create internal planner agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

func createPipelineTestProjectWithRepo(t *testing.T, alias, url string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project (workspace_id, title, description, status, priority)
		VALUES ($1, $2, '', 'planned', 'none')
		RETURNING id::text
	`, testWorkspaceID, "Pipeline repo project "+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create pipeline test project: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, label, position, created_by)
		VALUES ($1, $2, 'git_repo', jsonb_build_object('url', $3::text), $4, 1, $5)
	`, projectID, testWorkspaceID, url, alias, testUserID); err != nil {
		t.Fatalf("create pipeline test project repo: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	return projectID
}

func newPipelineRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(testUserID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load handler test member: %v", err)
	}
	return req.WithContext(middleware.SetMemberContext(req.Context(), testWorkspaceID, member))
}

func createOtherWorkspaceForPipelineTest(t *testing.T) string {
	t.Helper()
	var workspaceID string
	slug := "pipeline-other-" + uuid.NewString()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Other Pipeline Workspace', $1, '', 'OTH')
		RETURNING id::text
	`, slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	return workspaceID
}

func insertPipelineForWorkspace(t *testing.T, workspaceID, name string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO pipeline (workspace_id, name, description, created_by)
		VALUES ($1, $2, '', $3)
	`, workspaceID, name, testUserID); err != nil {
		t.Fatalf("insert other workspace pipeline: %v", err)
	}
}
