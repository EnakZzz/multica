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
    type: check
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
