package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestProjectVisualGenerateNodesFromWikiCreatesBoardNodes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual wiki extraction project")
	plannerID := createInternalPlannerAgentForTest(t)
	createProjectVisualTestWikiPage(t, project.ID, "visual-brief", "Visual Brief", `
# 角色：Milo
一只走失宠物的主角，需要温暖、清晰的识别特征。

# 场景：雨夜街角
玩家主要寻找宠物的城市街角场景，需要路灯和告示牌。

# UI：线索按钮
用于触发线索查看的圆形按钮。
`)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-board/generate-nodes", nil)
	req = withURLParam(req, "id", project.ID)
	testHandler.GenerateProjectVisualNodes(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("GenerateProjectVisualNodes: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var queued struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&queued); err != nil {
		t.Fatalf("decode queued task: %v", err)
	}
	if queued.TaskID == "" {
		t.Fatal("GenerateProjectVisualNodes should return task_id")
	}
	var issueIDIsNull bool
	var contextJSON []byte
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id IS NULL, context, agent_id::text
		FROM agent_task_queue
		WHERE id = $1
	`, queued.TaskID).Scan(&issueIDIsNull, &contextJSON, &agentID); err != nil {
		t.Fatalf("load visual extract task: %v", err)
	}
	if !issueIDIsNull || agentID != plannerID {
		t.Fatalf("visual extract task issue_null=%v agent=%s planner=%s", issueIDIsNull, agentID, plannerID)
	}
	var taskContext map[string]any
	if err := json.Unmarshal(contextJSON, &taskContext); err != nil {
		t.Fatalf("decode visual extract context: %v", err)
	}
	if taskContext["type"] != service.VisualBoardExtractContextType || taskContext["project_id"] != project.ID {
		t.Fatalf("unexpected visual extract task context: %#v", taskContext)
	}

	gotEvent := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventProjectVisualUpdated, func(e events.Event) {
		select {
		case gotEvent <- e:
		default:
		}
	})

	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_task_queue SET status = 'running', started_at = now() WHERE id = $1
	`, queued.TaskID); err != nil {
		t.Fatalf("mark extract task running: %v", err)
	}
	output := `{
  "nodes": [
    {"id":"milo","type":"character","title":"Milo","description":"走失宠物主角，需要清晰识别。","prompt":"Warm readable pet character concept for Milo.","source_refs":[{"wiki_slug":"visual-brief","snippet":"角色：Milo"}],"confidence":0.91},
    {"id":"street","type":"scene","title":"雨夜街角","description":"带路灯和告示牌的城市街角。","prompt":"Rainy city street corner with warm lamp and missing-pet poster.","source_refs":[{"wiki_slug":"visual-brief","snippet":"场景：雨夜街角"}],"confidence":0.88},
    {"id":"clue","type":"ui_element","title":"线索按钮","description":"触发线索查看的圆形按钮。","prompt":"Round clue button UI element, readable and game-ready.","source_refs":[{"wiki_slug":"visual-brief","snippet":"UI：线索按钮"}],"confidence":0.84}
  ],
  "edges": [
    {"source":"street","target":"clue","relation":"supports_gameplay"}
  ]
}`
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+queued.TaskID+"/complete", map[string]any{"output": output}, testWorkspaceID, "visual-extract-daemon")
	req = withURLParams(req, "taskId", queued.TaskID)
	testHandler.CompleteTask(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/projects/"+project.ID+"/visual-board", nil)
	req = withURLParam(req, "id", project.ID)
	testHandler.GetProjectVisualBoard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectVisualBoard: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode board: %v", err)
	}
	if board.ProjectID != project.ID || len(board.Nodes) != 3 || len(board.Edges) != 1 {
		t.Fatalf("board project/nodes/edges mismatch: project=%s nodes=%d edges=%d", board.ProjectID, len(board.Nodes), len(board.Edges))
	}
	types := map[string]bool{}
	for _, node := range board.Nodes {
		types[node.Type] = true
		if node.Status != "draft" {
			t.Fatalf("extracted node %q status = %q, want draft", node.Title, node.Status)
		}
		if !strings.Contains(string(node.SourceRefs), "visual-brief") {
			t.Fatalf("extracted node source_refs = %s, want wiki slug", string(node.SourceRefs))
		}
	}
	for _, want := range []string{"character", "scene", "ui_element"} {
		if !types[want] {
			t.Fatalf("extracted node types = %#v, missing %s", types, want)
		}
	}
	select {
	case event := <-gotEvent:
		payload, ok := event.Payload.(map[string]any)
		if !ok || payload["project_id"] != project.ID || payload["status"] != "extracted" {
			t.Fatalf("visual extract event payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected project_visual:updated event after extraction")
	}
}

func TestProjectVisualGenerateImageValidatesAgentWorkspaceAndRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual image generation project")
	nodeID := createProjectVisualTestNode(t, project.ID, "character", "Milo portrait", "draft", "")
	foreignAgentID := createProjectVisualForeignAgent(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generate", map[string]any{
		"agent_id": foreignAgentID,
	})
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID)
	testHandler.GenerateProjectVisualNodeImage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("foreign agent: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	artAgentID := createHandlerTestAgent(t, "Visual Art Agent "+uuid.NewString(), nil)
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generate", map[string]any{
		"agent_id": artAgentID,
	})
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID)
	testHandler.GenerateProjectVisualNodeImage(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("valid agent: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	if resp.TaskID == "" {
		t.Fatal("generate response should include task_id")
	}

	var status, generationAgentID, generationTaskID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, generation_agent_id::text, generation_task_id::text
		FROM project_visual_node
		WHERE id = $1
	`, nodeID).Scan(&status, &generationAgentID, &generationTaskID); err != nil {
		t.Fatalf("load visual node after generate: %v", err)
	}
	if status != "generating" || generationAgentID != artAgentID || generationTaskID != resp.TaskID {
		t.Fatalf("node generation state = status %q agent %s task %s", status, generationAgentID, generationTaskID)
	}

	var issueIDIsNull bool
	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id IS NULL, context
		FROM agent_task_queue
		WHERE id = $1
	`, resp.TaskID).Scan(&issueIDIsNull, &contextJSON); err != nil {
		t.Fatalf("load visual task: %v", err)
	}
	if !issueIDIsNull {
		t.Fatal("visual generation task should not create or link an issue")
	}
	var taskContext map[string]any
	if err := json.Unmarshal(contextJSON, &taskContext); err != nil {
		t.Fatalf("decode visual task context: %v", err)
	}
	if taskContext["type"] != service.VisualNodeGenerateContextType || taskContext["node_id"] != nodeID || taskContext["project_id"] != project.ID {
		t.Fatalf("unexpected visual task context: %#v", taskContext)
	}
}

func TestProjectVisualGenerationResultRequiresSameProjectNodeAndBindsAttachment(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	projectA := createProjectVisualTestProject(t, "Visual result project A")
	projectB := createProjectVisualTestProject(t, "Visual result project B")
	nodeID := createProjectVisualTestNode(t, projectA.ID, "scene", "Street corner", "generating", "")
	attachmentID := createProjectVisualTestAttachment(t, testWorkspaceID, "street-corner.png")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+projectB.ID+"/visual-nodes/"+nodeID+"/generation-result", map[string]any{
		"attachment_id": attachmentID,
		"note":          "wrong project",
	})
	req = withURLParams(req, "id", projectB.ID, "nodeId", nodeID)
	testHandler.CompleteProjectVisualNodeGeneration(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign project node: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	gotEvent := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventProjectVisualUpdated, func(e events.Event) {
		select {
		case gotEvent <- e:
		default:
		}
	})

	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+projectA.ID+"/visual-nodes/"+nodeID+"/generation-result", map[string]any{
		"attachment_id": attachmentID,
		"note":          "first usable preview",
	})
	req = withURLParams(req, "id", projectA.ID, "nodeId", nodeID)
	testHandler.CompleteProjectVisualNodeGeneration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same project result: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/projects/"+projectA.ID+"/visual-board", nil)
	req = withURLParam(req, "id", projectA.ID)
	testHandler.GetProjectVisualBoard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectVisualBoard: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode visual board: %v", err)
	}
	if len(board.Nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(board.Nodes))
	}
	node := board.Nodes[0]
	if node.Status != "draft" || node.ResultAttachmentID == nil || *node.ResultAttachmentID != attachmentID {
		t.Fatalf("node result state = status %q attachment %v", node.Status, node.ResultAttachmentID)
	}
	if node.ResultAttachment == nil || node.ResultAttachment.Filename != "street-corner.png" {
		t.Fatalf("node result attachment = %#v", node.ResultAttachment)
	}
	if node.ResultNote != "first usable preview" {
		t.Fatalf("node result note = %q", node.ResultNote)
	}

	select {
	case event := <-gotEvent:
		if event.WorkspaceID != testWorkspaceID {
			t.Fatalf("visual update workspace = %q, want %q", event.WorkspaceID, testWorkspaceID)
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			t.Fatalf("visual update payload = %#v", event.Payload)
		}
		if payload["project_id"] != projectA.ID || payload["node_id"] != nodeID || payload["status"] != "completed" {
			t.Fatalf("visual update payload = %#v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("expected project_visual:updated event")
	}
}

func TestProjectVisualCreatePlanIncludesOnlyAdoptedNodes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual plan project")
	attachmentID := createProjectVisualTestAttachment(t, testWorkspaceID, "adopted.png")
	createProjectVisualTestNode(t, project.ID, "character", "Adopted Hero", "adopted", attachmentID)
	createProjectVisualTestNode(t, project.ID, "scene", "Draft Forest", "draft", "")
	createProjectVisualTestNode(t, project.ID, "prop", "Rejected Lantern", "rejected", "")
	plannerID := createInternalPlannerAgentForTest(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-board/create-plan", map[string]any{
		"gameplay_notes": "Use the adopted hero as art direction for stealth gameplay.",
		"title":          "Visual canvas implementation plan",
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.CreatePlanFromProjectVisualBoard(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreatePlanFromProjectVisualBoard: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var plan PlanResponse
	if err := json.NewDecoder(w.Body).Decode(&plan); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	if plan.ProjectID == nil || *plan.ProjectID != project.ID {
		t.Fatalf("plan project_id = %v, want %s", plan.ProjectID, project.ID)
	}
	if plan.PlannerAgentID != plannerID {
		t.Fatalf("plan planner_agent_id = %s, want internal planner %s", plan.PlannerAgentID, plannerID)
	}
	if !strings.Contains(plan.Prompt, "Adopted Hero") || !strings.Contains(plan.Prompt, attachmentID) {
		t.Fatalf("plan prompt missing adopted node or attachment: %s", plan.Prompt)
	}
	if strings.Contains(plan.Prompt, "Draft Forest") || strings.Contains(plan.Prompt, "Rejected Lantern") {
		t.Fatalf("plan prompt should exclude draft/rejected nodes: %s", plan.Prompt)
	}
	if !strings.Contains(plan.Prompt, "Use the adopted hero as art direction") {
		t.Fatalf("plan prompt missing gameplay notes: %s", plan.Prompt)
	}

	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT context
		FROM agent_task_queue
		WHERE id = $1
	`, plan.TaskID).Scan(&contextJSON); err != nil {
		t.Fatalf("load plan task context: %v", err)
	}
	var taskContext map[string]any
	if err := json.Unmarshal(contextJSON, &taskContext); err != nil {
		t.Fatalf("decode plan task context: %v", err)
	}
	if taskContext["type"] != "issue_plan" || taskContext["phase"] != "spec" || taskContext["project_id"] != project.ID {
		t.Fatalf("unexpected plan task context: %#v", taskContext)
	}
}

func createProjectVisualTestProject(t *testing.T, title string) ProjectResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		req := newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})
	return project
}

func createProjectVisualTestWikiPage(t *testing.T, projectID, slug, title, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+projectID+"/wiki/pages", map[string]any{
		"slug":        slug,
		"title":       title,
		"body":        body,
		"source_refs": []any{},
		"status":      "reviewed",
	})
	req = withURLParam(req, "id", projectID)
	testHandler.CreateProjectWikiPage(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProjectWikiPage: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func createProjectVisualTestNode(t *testing.T, projectID, nodeType, title, status, resultAttachmentID string) string {
	t.Helper()
	var boardID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project_visual_board (workspace_id, project_id)
		VALUES ($1, $2)
		ON CONFLICT (workspace_id, project_id) DO UPDATE SET updated_at = project_visual_board.updated_at
		RETURNING id::text
	`, testWorkspaceID, projectID).Scan(&boardID); err != nil {
		t.Fatalf("ensure visual board: %v", err)
	}

	var nodeID string
	if strings.TrimSpace(resultAttachmentID) == "" {
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO project_visual_node (
				board_id, workspace_id, project_id, type, status, title, description, prompt
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id::text
		`, boardID, testWorkspaceID, projectID, nodeType, status, title, title+" description", title+" prompt").Scan(&nodeID); err != nil {
			t.Fatalf("insert visual node: %v", err)
		}
		return nodeID
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project_visual_node (
			board_id, workspace_id, project_id, type, status, title, description, prompt, result_attachment_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id::text
	`, boardID, testWorkspaceID, projectID, nodeType, status, title, title+" description", title+" prompt", resultAttachmentID).Scan(&nodeID); err != nil {
		t.Fatalf("insert visual node with result: %v", err)
	}
	return nodeID
}

func createProjectVisualTestAttachment(t *testing.T, workspaceID, filename string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO attachment (
			id, workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes
		)
		VALUES ($1, $2, 'member', $3, $4, $5, 'image/png', 12345)
	`, id, workspaceID, testUserID, filename, "http://example.test/uploads/"+id+".png"); err != nil {
		t.Fatalf("create visual attachment: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, id)
	})
	return id
}

func createProjectVisualForeignAgent(t *testing.T) string {
	t.Helper()
	workspaceID := createProjectVisualForeignWorkspace(t)
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, 'Visual foreign runtime', 'cloud', 'handler_test_runtime', 'online', '{}', '{}'::jsonb, $3, now())
		RETURNING id::text
	`, workspaceID, "visual-foreign-"+uuid.NewString(), testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 1, $4, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id::text
	`, workspaceID, "Visual Foreign Agent "+uuid.NewString(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}
	return agentID
}

func createProjectVisualForeignWorkspace(t *testing.T) string {
	t.Helper()
	var workspaceID string
	slug := "visual-foreign-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Visual Foreign Workspace', $1, '', 'VFW')
		RETURNING id::text
	`, slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	return workspaceID
}
