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
	var issueID string
	var contextJSON []byte
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id::text, context, agent_id::text
		FROM agent_task_queue
		WHERE id = $1
	`, queued.TaskID).Scan(&issueID, &contextJSON, &agentID); err != nil {
		t.Fatalf("load visual extract task: %v", err)
	}
	if issueID == "" || agentID != plannerID {
		t.Fatalf("visual extract task issue_id=%q agent=%s planner=%s", issueID, agentID, plannerID)
	}
	var issueTitle, issueProjectID, assigneeType, assigneeID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT title, project_id::text, assignee_type, assignee_id::text
		FROM issue
		WHERE id = $1
	`, issueID).Scan(&issueTitle, &issueProjectID, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("load visual extract issue: %v", err)
	}
	if issueTitle != "Generate visual nodes from Project Wiki" || issueProjectID != project.ID || assigneeType != "agent" || assigneeID != plannerID {
		t.Fatalf("visual extract issue title=%q project=%s assignee=%s:%s planner=%s", issueTitle, issueProjectID, assigneeType, assigneeID, plannerID)
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
    {"id":"milo","type":"character","title":"Milo","title_zh":"米洛","description":"Lost pet protagonist with readable identity.","description_zh":"走失宠物主角，需要清晰识别。","prompt":"Warm readable pet character concept for Milo.","prompt_zh":"为米洛生成温暖、清晰易读的宠物主角概念图。","source_refs":[{"wiki_slug":"visual-brief","snippet":"角色：Milo"}],"confidence":0.91},
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
	foundMiloZh := false
	for _, node := range board.Nodes {
		types[node.Type] = true
		if node.Status != "draft" {
			t.Fatalf("extracted node %q status = %q, want draft", node.Title, node.Status)
		}
		if !strings.Contains(string(node.SourceRefs), "visual-brief") {
			t.Fatalf("extracted node source_refs = %s, want wiki slug", string(node.SourceRefs))
		}
		if node.Title == "Milo" && node.TitleZh == "米洛" && strings.Contains(node.DescriptionZh, "走失宠物主角") && strings.Contains(node.PromptZh, "温暖") {
			foundMiloZh = true
		}
	}
	if !foundMiloZh {
		t.Fatalf("expected extracted Milo node to preserve Chinese display fields: %#v", board.Nodes)
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

func TestParseVisualBoardExtractOutputAcceptsCommentArtifactAliases(t *testing.T) {
	output := `{
  "artifact_type": "visual_board_extraction",
  "nodes": [
    {
      "id": "vb-lost-pet-protagonist",
      "title": "Lost Pet 小动物主角",
      "node_type": "character_concept",
      "source_slugs": ["glossary"],
      "extracted_facts": ["玩家扮演刚离世的小动物。"],
      "visual_brief": "A gentle fantasy small-animal protagonist.",
      "must_include": ["Small animal identity"],
      "must_avoid": ["Horror death imagery"],
      "acceptance_criteria": ["Reviewer can identify the lost pet character."],
      "confidence": 0.88
    }
  ],
  "edges": []
}`

	got, err := parseVisualBoardExtractOutput(output)
	if err != nil {
		t.Fatalf("parseVisualBoardExtractOutput: %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("expected one node, got %d", len(got.Nodes))
	}
	node := got.Nodes[0]
	if node.Type != "character" {
		t.Fatalf("expected node_type alias to populate Type, got %q", node.Type)
	}
	if !strings.Contains(node.Description, "A gentle fantasy small-animal protagonist.") ||
		!strings.Contains(node.Description, "玩家扮演刚离世的小动物。") ||
		!strings.Contains(node.Description, "Horror death imagery") {
		t.Fatalf("description did not preserve artifact details: %q", node.Description)
	}
	if node.Prompt != "A gentle fantasy small-animal protagonist." {
		t.Fatalf("expected visual_brief prompt fallback, got %q", node.Prompt)
	}
	if !strings.Contains(string(node.SourceRefs), "glossary") {
		t.Fatalf("expected source_slugs alias to populate source refs, got %s", string(node.SourceRefs))
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
		TaskID          string `json:"task_id"`
		IssueID         string `json:"issue_id"`
		IssueIdentifier string `json:"issue_identifier"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode generate response: %v", err)
	}
	if resp.TaskID == "" {
		t.Fatal("generate response should include task_id")
	}
	if resp.IssueID == "" || resp.IssueIdentifier == "" {
		t.Fatalf("generate response should include issue details: %#v", resp)
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

	var issueID string
	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id::text, context
		FROM agent_task_queue
		WHERE id = $1
	`, resp.TaskID).Scan(&issueID, &contextJSON); err != nil {
		t.Fatalf("load visual task: %v", err)
	}
	if issueID != resp.IssueID {
		t.Fatalf("visual generation task issue_id = %q, response issue_id = %q", issueID, resp.IssueID)
	}
	var issueTitle, issueProjectID, assigneeType, assigneeID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT title, project_id::text, assignee_type, assignee_id::text
		FROM issue
		WHERE id = $1
	`, issueID).Scan(&issueTitle, &issueProjectID, &assigneeType, &assigneeID); err != nil {
		t.Fatalf("load visual generation issue: %v", err)
	}
	if issueTitle != "Generate visual asset: Milo portrait" || issueProjectID != project.ID || assigneeType != "agent" || assigneeID != artAgentID {
		t.Fatalf("visual generation issue title=%q project=%s assignee=%s:%s artAgent=%s", issueTitle, issueProjectID, assigneeType, assigneeID, artAgentID)
	}
	var taskContext map[string]any
	if err := json.Unmarshal(contextJSON, &taskContext); err != nil {
		t.Fatalf("decode visual task context: %v", err)
	}
	if taskContext["type"] != service.VisualNodeGenerateContextType || taskContext["node_id"] != nodeID || taskContext["project_id"] != project.ID {
		t.Fatalf("unexpected visual task context: %#v", taskContext)
	}
}

func TestProjectVisualCreateAnimationNodeFromSourceNode(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual animation project")
	sourceNodeID := createProjectVisualTestNode(t, project.ID, "character", "Milo", "draft", "")

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes", map[string]any{
		"type":           "animation",
		"title":          "Milo animation",
		"title_zh":       "米洛动画",
		"description":    "Animation node for Milo",
		"description_zh": "米洛的动画节点",
		"prompt":         "Use game-asset-pipeline and output a spritesheet.",
		"prompt_zh":      "使用 game-asset-pipeline 并输出精灵表。",
		"position_x":     340,
		"position_y":     10,
		"source_node_id": sourceNodeID,
		"relation":       "variant_of",
		"source_refs": []map[string]string{{
			"visual_node_id": sourceNodeID,
		}},
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.CreateProjectVisualNode(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProjectVisualNode: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode visual board: %v", err)
	}
	var animationNode *visualNodeResponse
	for i := range board.Nodes {
		if board.Nodes[i].Title == "Milo animation" {
			animationNode = &board.Nodes[i]
			break
		}
	}
	if animationNode == nil {
		t.Fatalf("created animation node not returned in board: %#v", board.Nodes)
	}
	if animationNode.Type != "animation" || !strings.Contains(animationNode.Prompt, "game-asset-pipeline") {
		t.Fatalf("animation node type/prompt = %q %q", animationNode.Type, animationNode.Prompt)
	}
	if animationNode.TitleZh != "米洛动画" || animationNode.DescriptionZh != "米洛的动画节点" || !strings.Contains(animationNode.PromptZh, "精灵表") {
		t.Fatalf("animation node zh fields = title %q description %q prompt %q", animationNode.TitleZh, animationNode.DescriptionZh, animationNode.PromptZh)
	}
	foundEdge := false
	for _, edge := range board.Edges {
		if edge.SourceNodeID == sourceNodeID && edge.TargetNodeID == animationNode.ID && edge.Relation == "variant_of" {
			foundEdge = true
			break
		}
	}
	if !foundEdge {
		t.Fatalf("expected variant edge from %s to %s, got %#v", sourceNodeID, animationNode.ID, board.Edges)
	}
}

func TestProjectVisualDeleteNodeRemovesAttachedEdges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual delete project")
	sourceNodeID := createProjectVisualTestNode(t, project.ID, "character", "Milo", "draft", "")
	targetNodeID := createProjectVisualTestNode(t, project.ID, "animation", "Milo animation", "draft", "")
	var boardID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT board_id::text
		FROM project_visual_node
		WHERE id = $1
	`, sourceNodeID).Scan(&boardID); err != nil {
		t.Fatalf("load visual board id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO project_visual_edge (
			board_id, workspace_id, project_id, source_node_id, target_node_id, relation
		)
		VALUES ($1, $2, $3, $4, $5, 'variant_of')
	`, boardID, testWorkspaceID, project.ID, sourceNodeID, targetNodeID); err != nil {
		t.Fatalf("insert visual edge: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/projects/"+project.ID+"/visual-nodes/"+targetNodeID, nil)
	req = withURLParams(req, "id", project.ID, "nodeId", targetNodeID)
	testHandler.DeleteProjectVisualNode(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteProjectVisualNode: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode visual board: %v", err)
	}
	if len(board.Nodes) != 1 || board.Nodes[0].ID != sourceNodeID {
		t.Fatalf("nodes after delete = %#v, want only source node %s", board.Nodes, sourceNodeID)
	}
	if len(board.Edges) != 0 {
		t.Fatalf("edges after delete = %#v, want none", board.Edges)
	}
}

func TestProjectVisualClearBoardRemovesNodesAndEdges(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual clear project")
	sourceNodeID := createProjectVisualTestNode(t, project.ID, "character", "Milo", "draft", "")
	targetNodeID := createProjectVisualTestNode(t, project.ID, "animation", "Milo animation", "draft", "")
	var boardID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT board_id::text
		FROM project_visual_node
		WHERE id = $1
	`, sourceNodeID).Scan(&boardID); err != nil {
		t.Fatalf("load visual board id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO project_visual_edge (
			board_id, workspace_id, project_id, source_node_id, target_node_id, relation
		)
		VALUES ($1, $2, $3, $4, $5, 'variant_of')
	`, boardID, testWorkspaceID, project.ID, sourceNodeID, targetNodeID); err != nil {
		t.Fatalf("insert visual edge: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodDelete, "/api/projects/"+project.ID+"/visual-board", nil)
	req = withURLParam(req, "id", project.ID)
	testHandler.ClearProjectVisualBoard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClearProjectVisualBoard: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode visual board: %v", err)
	}
	if board.ProjectID != project.ID || len(board.Nodes) != 0 || len(board.Edges) != 0 {
		t.Fatalf("cleared board mismatch: project=%s nodes=%d edges=%d", board.ProjectID, len(board.Nodes), len(board.Edges))
	}

	var nodeCount, edgeCount, boardCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM project_visual_node
		WHERE project_id = $1
	`, project.ID).Scan(&nodeCount); err != nil {
		t.Fatalf("count visual nodes: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM project_visual_edge
		WHERE project_id = $1
	`, project.ID).Scan(&edgeCount); err != nil {
		t.Fatalf("count visual edges: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM project_visual_board
		WHERE id = $1 AND project_id = $2
	`, boardID, project.ID).Scan(&boardCount); err != nil {
		t.Fatalf("count visual boards: %v", err)
	}
	if nodeCount != 0 || edgeCount != 0 || boardCount != 1 {
		t.Fatalf("cleared rows mismatch: nodes=%d edges=%d boards=%d", nodeCount, edgeCount, boardCount)
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
		"note_zh":       "第一版可用预览",
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
	if node.ResultNoteZh != "第一版可用预览" {
		t.Fatalf("node result note zh = %q", node.ResultNoteZh)
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

func TestProjectVisualGenerationHistoryListsIssuesAndRestoresVersion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual history project")
	nodeID := createProjectVisualTestNode(t, project.ID, "scene", "Street corner", "draft", "")
	artAgentID := createHandlerTestAgent(t, "Visual History Agent "+uuid.NewString(), nil)

	first := queueProjectVisualGenerationForTest(t, project.ID, nodeID, artAgentID)
	firstAttachmentID := createProjectVisualTestAttachment(t, testWorkspaceID, "street-v1.png")
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generation-result", map[string]any{
		"task_id":       first.TaskID,
		"attachment_id": firstAttachmentID,
		"note":          "first usable preview",
		"note_zh":       "第一版可用预览",
	})
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID)
	testHandler.CompleteProjectVisualNodeGeneration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete first generation: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	second := queueProjectVisualGenerationForTest(t, project.ID, nodeID, artAgentID)
	secondAttachmentID := createProjectVisualTestAttachment(t, testWorkspaceID, "street-v2.png")
	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generation-result", map[string]any{
		"task_id":       second.TaskID,
		"attachment_id": secondAttachmentID,
		"note":          "second usable preview",
		"note_zh":       "第二版可用预览",
	})
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID)
	testHandler.CompleteProjectVisualNodeGeneration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("complete second generation: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodGet, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generations", nil)
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID)
	testHandler.ListProjectVisualNodeGenerations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list generations: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var history listVisualNodeGenerationsResponse
	if err := json.NewDecoder(w.Body).Decode(&history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history.Generations) != 2 {
		t.Fatalf("history length = %d, want 2: %#v", len(history.Generations), history.Generations)
	}
	var firstGenerationID string
	foundFirstIssue := false
	foundSecondCurrent := false
	for _, generation := range history.Generations {
		if generation.AttachmentID == nil {
			continue
		}
		if *generation.AttachmentID == firstAttachmentID {
			firstGenerationID = generation.ID
			foundFirstIssue = generation.IssueID == first.IssueID &&
				generation.IssueIdentifier != "" &&
				generation.IssueTitle == "Generate visual asset: Street corner" &&
				generation.NoteZh == "第一版可用预览"
		}
		if *generation.AttachmentID == secondAttachmentID {
			foundSecondCurrent = generation.IssueID == second.IssueID && generation.IsCurrent
		}
	}
	if !foundFirstIssue || !foundSecondCurrent || firstGenerationID == "" {
		t.Fatalf("unexpected generation history: %#v", history.Generations)
	}

	var linkedIssueID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id::text FROM attachment WHERE id = $1
	`, firstAttachmentID).Scan(&linkedIssueID); err != nil {
		t.Fatalf("load first attachment issue link: %v", err)
	}
	if linkedIssueID != first.IssueID {
		t.Fatalf("first attachment issue_id = %s, want %s", linkedIssueID, first.IssueID)
	}

	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-nodes/"+nodeID+"/generations/"+firstGenerationID+"/restore", nil)
	req = withURLParams(req, "id", project.ID, "nodeId", nodeID, "generationId", firstGenerationID)
	testHandler.RestoreProjectVisualNodeGeneration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restore generation: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var board visualBoardResponse
	if err := json.NewDecoder(w.Body).Decode(&board); err != nil {
		t.Fatalf("decode restored board: %v", err)
	}
	if len(board.Nodes) != 1 || board.Nodes[0].ResultAttachmentID == nil || *board.Nodes[0].ResultAttachmentID != firstAttachmentID {
		t.Fatalf("restored board node = %#v, want attachment %s", board.Nodes, firstAttachmentID)
	}
	if board.Nodes[0].ResultNoteZh != "第一版可用预览" {
		t.Fatalf("restored result note zh = %q", board.Nodes[0].ResultNoteZh)
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
	for _, want := range []string{"Plan mode: playable_prototype", "visual_asset_manifest", "procedural", "pixel-style placeholder", "do not wait for final generated art assets"} {
		if !strings.Contains(plan.Prompt, want) {
			t.Fatalf("default playable prototype prompt missing %q: %s", want, plan.Prompt)
		}
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

func TestProjectVisualCreatePlayablePrototypePlanModePrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual prototype plan project")
	createProjectVisualTestNode(t, project.ID, "character", "Lost Pet Hero", "adopted", "")
	createInternalPlannerAgentForTest(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-board/create-plan", map[string]any{
		"gameplay_notes": "Use current frontend stack and keep the experience non-text-first.",
		"plan_mode":      "playable_prototype",
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
	for _, want := range []string{
		"Plan mode: playable_prototype",
		"visual_asset_manifest",
		"CSS, SVG, Canvas",
		"pixel-style placeholder assets",
		"do not wait for final generated art assets",
		"full Lost Pet recovery flow",
	} {
		if !strings.Contains(plan.Prompt, want) {
			t.Fatalf("playable prototype prompt missing %q: %s", want, plan.Prompt)
		}
	}
}

func TestProjectVisualCreateProductionAssetIntegrationPlanModePrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual integration plan project")
	attachmentID := createProjectVisualTestAttachment(t, testWorkspaceID, "selected-hero.png")
	createProjectVisualTestNode(t, project.ID, "character", "Selected Hero", "adopted", attachmentID)
	createProjectVisualTestNode(t, project.ID, "scene", "Missing Park", "adopted", "")
	createInternalPlannerAgentForTest(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-board/create-plan", map[string]any{
		"gameplay_notes": "Replace current placeholder assets with selected visual board results.",
		"plan_mode":      "production_asset_integration",
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
	for _, want := range []string{
		"Plan mode: production_asset_integration",
		"Do not create final asset generation tasks",
		"do not use game-asset-pipeline to produce new images",
		"result_attachment_id",
		"asset replacement map",
		"missing an integration-ready asset",
	} {
		if !strings.Contains(plan.Prompt, want) {
			t.Fatalf("production asset integration prompt missing %q: %s", want, plan.Prompt)
		}
	}
	if !strings.Contains(plan.Prompt, attachmentID) {
		t.Fatalf("production asset integration prompt missing result attachment id %s: %s", attachmentID, plan.Prompt)
	}
}

func TestProjectVisualCreatePlanRequiresAdoptedNodes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectVisualTestProject(t, "Visual plan no adopted project")
	createProjectVisualTestNode(t, project.ID, "scene", "Draft Forest", "draft", "")
	createProjectVisualTestNode(t, project.ID, "prop", "Rejected Lantern", "rejected", "")
	createInternalPlannerAgentForTest(t)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+project.ID+"/visual-board/create-plan", map[string]any{
		"plan_mode": "production_asset_integration",
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.CreatePlanFromProjectVisualBoard(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("CreatePlanFromProjectVisualBoard: expected 400 without adopted nodes, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "at least one adopted visual node is required") {
		t.Fatalf("unexpected no-adopted response: %s", w.Body.String())
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

type queuedVisualGenerationForTest struct {
	TaskID  string
	IssueID string
}

func queueProjectVisualGenerationForTest(t *testing.T, projectID, nodeID, agentID string) queuedVisualGenerationForTest {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/projects/"+projectID+"/visual-nodes/"+nodeID+"/generate", map[string]any{
		"agent_id": agentID,
	})
	req = withURLParams(req, "id", projectID, "nodeId", nodeID)
	testHandler.GenerateProjectVisualNodeImage(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("queue visual generation: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		TaskID  string `json:"task_id"`
		IssueID string `json:"issue_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode queued visual generation: %v", err)
	}
	if resp.TaskID == "" || resp.IssueID == "" {
		t.Fatalf("queued visual generation missing ids: %#v", resp)
	}
	return queuedVisualGenerationForTest{TaskID: resp.TaskID, IssueID: resp.IssueID}
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
