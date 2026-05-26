package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	visualTaskTypeGenerate = service.VisualNodeGenerateContextType
	visualTaskTypeExtract  = service.VisualBoardExtractContextType
)

type visualBoardResponse struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	ProjectID   string               `json:"project_id"`
	Viewport    json.RawMessage      `json:"viewport"`
	Metadata    json.RawMessage      `json:"metadata"`
	Nodes       []visualNodeResponse `json:"nodes"`
	Edges       []visualEdgeResponse `json:"edges"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
}

type visualNodeResponse struct {
	ID                     string              `json:"id"`
	BoardID                string              `json:"board_id"`
	WorkspaceID            string              `json:"workspace_id"`
	ProjectID              string              `json:"project_id"`
	Type                   string              `json:"type"`
	Status                 string              `json:"status"`
	Title                  string              `json:"title"`
	Description            string              `json:"description"`
	Prompt                 string              `json:"prompt"`
	PositionX              float64             `json:"position_x"`
	PositionY              float64             `json:"position_y"`
	SourceRefs             json.RawMessage     `json:"source_refs"`
	ReferenceAttachmentIDs []string            `json:"reference_attachment_ids"`
	ResultAttachmentID     *string             `json:"result_attachment_id"`
	ResultAttachment       *AttachmentResponse `json:"result_attachment"`
	ResultNote             string              `json:"result_note"`
	GenerationAgentID      *string             `json:"generation_agent_id"`
	GenerationTaskID       *string             `json:"generation_task_id"`
	GenerationError        string              `json:"generation_error"`
	CreatedAt              string              `json:"created_at"`
	UpdatedAt              string              `json:"updated_at"`
}

type visualEdgeResponse struct {
	ID           string `json:"id"`
	BoardID      string `json:"board_id"`
	WorkspaceID  string `json:"workspace_id"`
	ProjectID    string `json:"project_id"`
	SourceNodeID string `json:"source_node_id"`
	TargetNodeID string `json:"target_node_id"`
	Relation     string `json:"relation"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type visualBoardRow struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	Viewport    []byte
	Metadata    []byte
	CreatedAt   pgtype.Timestamptz
	UpdatedAt   pgtype.Timestamptz
}

type visualNodeRow struct {
	ID                     pgtype.UUID
	BoardID                pgtype.UUID
	WorkspaceID            pgtype.UUID
	ProjectID              pgtype.UUID
	Type                   string
	Status                 string
	Title                  string
	Description            string
	Prompt                 string
	PositionX              float64
	PositionY              float64
	SourceRefs             []byte
	ReferenceAttachmentIDs []pgtype.UUID
	ResultAttachmentID     pgtype.UUID
	ResultNote             string
	GenerationAgentID      pgtype.UUID
	GenerationTaskID       pgtype.UUID
	GenerationError        string
	CreatedAt              pgtype.Timestamptz
	UpdatedAt              pgtype.Timestamptz
	ResultAttachment       *AttachmentResponse
}

type visualEdgeRow struct {
	ID           pgtype.UUID
	BoardID      pgtype.UUID
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	SourceNodeID pgtype.UUID
	TargetNodeID pgtype.UUID
	Relation     string
	CreatedAt    pgtype.Timestamptz
	UpdatedAt    pgtype.Timestamptz
}

type updateVisualBoardRequest struct {
	Viewport json.RawMessage           `json:"viewport"`
	Metadata json.RawMessage           `json:"metadata"`
	Nodes    []updateVisualNodeRequest `json:"nodes"`
	Edges    []updateVisualEdgeRequest `json:"edges"`
}

type updateVisualNodeRequest struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Prompt      string          `json:"prompt"`
	PositionX   float64         `json:"position_x"`
	PositionY   float64         `json:"position_y"`
	SourceRefs  json.RawMessage `json:"source_refs"`
}

type updateVisualEdgeRequest struct {
	ID           string `json:"id"`
	SourceNodeID string `json:"source_node_id"`
	TargetNodeID string `json:"target_node_id"`
	Relation     string `json:"relation"`
}

type generateVisualNodeRequest struct {
	AgentID string `json:"agent_id"`
}

type visualGenerationResultRequest struct {
	AttachmentID string `json:"attachment_id"`
	Note         string `json:"note"`
	Error        string `json:"error"`
}

type createVisualPlanRequest struct {
	PlannerAgentID string `json:"planner_agent_id"`
	GameplayNotes  string `json:"gameplay_notes"`
	Title          string `json:"title"`
}

type visualTaskContext struct {
	Type                   string                          `json:"type"`
	WorkspaceID            string                          `json:"workspace_id"`
	ProjectID              string                          `json:"project_id"`
	BoardID                string                          `json:"board_id,omitempty"`
	NodeID                 string                          `json:"node_id"`
	NodeTitle              string                          `json:"node_title"`
	NodeType               string                          `json:"node_type"`
	NodeDescription        string                          `json:"node_description"`
	Prompt                 string                          `json:"prompt"`
	RequesterID            string                          `json:"requester_id"`
	ReferenceAttachmentIDs []string                        `json:"reference_attachment_ids"`
	WikiPages              []service.VisualWikiPageContext `json:"wiki_pages,omitempty"`
}

func (h *Handler) GetProjectVisualBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		slog.Warn("load visual board failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateProjectVisualBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req updateVisualBoardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	viewport := normalizeJSONRaw(req.Viewport, "{}")
	metadata := normalizeJSONRaw(req.Metadata, "{}")
	if _, err := h.DB.Exec(r.Context(), `
		UPDATE project_visual_board
		SET viewport = $1, metadata = $2, updated_at = now()
		WHERE id = $3 AND workspace_id = $4
	`, viewport, metadata, board.ID, project.WorkspaceID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update visual board")
		return
	}
	for _, nodeReq := range req.Nodes {
		nodeID, ok := parseUUIDOrBadRequest(w, nodeReq.ID, "visual node id")
		if !ok {
			return
		}
		nodeType := normalizeVisualNodeType(nodeReq.Type)
		status := normalizeVisualNodeStatus(nodeReq.Status)
		title := strings.TrimSpace(nodeReq.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "node title is required")
			return
		}
		_, err := h.DB.Exec(r.Context(), `
			UPDATE project_visual_node
			SET type = $1, status = $2, title = $3, description = $4, prompt = $5,
			    position_x = $6, position_y = $7, source_refs = $8, updated_at = now()
			WHERE id = $9 AND board_id = $10 AND workspace_id = $11 AND project_id = $12
		`, nodeType, status, title, nodeReq.Description, nodeReq.Prompt, nodeReq.PositionX, nodeReq.PositionY,
			normalizeJSONRaw(nodeReq.SourceRefs, "[]"), nodeID, board.ID, project.WorkspaceID, project.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update visual node")
			return
		}
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GenerateProjectVisualNodes(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	pages, err := h.ProjectKnowledge.ListWikiPages(r.Context(), project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list wiki pages")
		return
	}
	if len(pages) == 0 {
		writeError(w, http.StatusBadRequest, "project wiki has no pages to extract")
		return
	}
	agent, err := h.Queries.GetInternalPlannerAgent(r.Context(), project.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "internal planner agent is not available")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "internal planner agent is archived")
		return
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusBadRequest, "internal planner agent has no runtime")
		return
	}
	payload := visualTaskContext{
		Type:        visualTaskTypeExtract,
		WorkspaceID: uuidToString(project.WorkspaceID),
		ProjectID:   uuidToString(project.ID),
		BoardID:     uuidToString(board.ID),
		RequesterID: userID,
		WikiPages:   visualWikiPageContexts(pages),
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare visual extraction task")
		return
	}
	task, err := h.Queries.CreateContextTask(r.Context(), db.CreateContextTaskParams{
		AgentID:           agent.ID,
		RuntimeID:         agent.RuntimeID,
		Priority:          80,
		Context:           contextJSON,
		ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue visual extraction task")
		return
	}
	h.TaskService.NotifyTaskEnqueued(r.Context(), task)
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": uuidToString(task.ID)})
}

func (h *Handler) GenerateProjectVisualNodeImage(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "visual node id")
	if !ok {
		return
	}
	var req generateVisualNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: project.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusBadRequest, "agent_id does not refer to an agent of this workspace")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "agent is archived")
		return
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusBadRequest, "agent has no runtime")
		return
	}
	node, ok := h.loadVisualNode(w, r, project.WorkspaceID, project.ID, nodeID)
	if !ok {
		return
	}
	payload := visualTaskContext{
		Type:                   visualTaskTypeGenerate,
		WorkspaceID:            uuidToString(project.WorkspaceID),
		ProjectID:              uuidToString(project.ID),
		NodeID:                 uuidToString(node.ID),
		NodeTitle:              node.Title,
		NodeType:               node.Type,
		NodeDescription:        node.Description,
		Prompt:                 node.Prompt,
		RequesterID:            userID,
		ReferenceAttachmentIDs: uuidSliceToStrings(node.ReferenceAttachmentIDs),
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual task")
		return
	}
	task, err := h.Queries.CreateContextTask(r.Context(), db.CreateContextTaskParams{
		AgentID:           agentID,
		RuntimeID:         agent.RuntimeID,
		Priority:          80,
		Context:           contextJSON,
		ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue visual task")
		return
	}
	_, err = h.DB.Exec(r.Context(), `
		UPDATE project_visual_node
		SET status = 'generating', generation_agent_id = $1, generation_task_id = $2,
		    generation_error = '', updated_at = now()
		WHERE id = $3 AND workspace_id = $4 AND project_id = $5
	`, agentID, task.ID, node.ID, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update visual node")
		return
	}
	h.TaskService.NotifyTaskEnqueued(r.Context(), task)
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": uuidToString(task.ID)})
}

func (h *Handler) CompleteProjectVisualNodeGeneration(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "visual node id")
	if !ok {
		return
	}
	var req visualGenerationResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Error) != "" {
		tag, err := h.DB.Exec(r.Context(), `
			UPDATE project_visual_node
			SET status = 'failed', generation_error = $1, result_note = $2, updated_at = now()
			WHERE id = $3 AND workspace_id = $4 AND project_id = $5
		`, strings.TrimSpace(req.Error), strings.TrimSpace(req.Note), nodeID, project.WorkspaceID, project.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update visual node")
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "visual node not found")
			return
		}
		h.publishProjectVisualUpdated(project.WorkspaceID, project.ID, nodeID, "failed")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	attachmentID, ok := parseUUIDOrBadRequest(w, req.AttachmentID, "attachment_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: attachmentID, WorkspaceID: project.WorkspaceID}); err != nil {
		writeError(w, http.StatusBadRequest, "attachment_id does not refer to an attachment of this workspace")
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE project_visual_node
		SET status = CASE WHEN status = 'generating' THEN 'draft' ELSE status END,
		    result_attachment_id = $1, result_note = $2, generation_error = '', updated_at = now()
		WHERE id = $3 AND workspace_id = $4 AND project_id = $5
	`, attachmentID, strings.TrimSpace(req.Note), nodeID, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update visual node")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "visual node not found")
		return
	}
	h.publishProjectVisualUpdated(project.WorkspaceID, project.ID, nodeID, "completed")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) CreatePlanFromProjectVisualBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req createVisualPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	plannerAgentID, ok := parseUUIDOrBadRequest(w, req.PlannerAgentID, "planner_agent_id")
	if !ok {
		return
	}
	if status, msg := h.validatePlanAgent(r, plannerAgentID, project.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return
	}
	nodes, err := h.listVisualNodes(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list visual nodes")
		return
	}
	var adopted []visualNodeRow
	for _, node := range nodes {
		if node.Status == "adopted" {
			adopted = append(adopted, node)
		}
	}
	if len(adopted) == 0 {
		writeError(w, http.StatusBadRequest, "at least one adopted visual node is required")
		return
	}
	prompt := buildVisualPlanPrompt(project.Title, adopted, req.GameplayNotes)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Plan from visual canvas: " + project.Title
	}
	plan, err := h.Queries.CreatePlan(r.Context(), db.CreatePlanParams{
		WorkspaceID:    project.WorkspaceID,
		Title:          title,
		Prompt:         prompt,
		PlannerAgentID: plannerAgentID,
		CreatedBy:      parseUUID(userID),
		ProjectID:      project.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), project.WorkspaceID, parseUUID(userID), plan.ID, plannerAgentID, prompt, project.ID, service.IssuePlanPhaseSpec, service.PlanSpec{})
	if err != nil {
		h.Queries.MarkPlanFailed(r.Context(), db.MarkPlanFailedParams{ID: plan.ID, Error: pgtype.Text{String: err.Error(), Valid: true}})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err = h.Queries.SetPlanTask(r.Context(), db.SetPlanTaskParams{ID: plan.ID, TaskID: task.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to attach plan task")
		return
	}
	writeJSON(w, http.StatusCreated, planToResponse(plan, nil))
}

func (h *Handler) ensureVisualBoard(r *http.Request, workspaceID, projectID pgtype.UUID) (visualBoardRow, error) {
	var row visualBoardRow
	err := h.DB.QueryRow(r.Context(), `
		INSERT INTO project_visual_board (workspace_id, project_id)
		VALUES ($1, $2)
		ON CONFLICT (workspace_id, project_id) DO UPDATE SET updated_at = project_visual_board.updated_at
		RETURNING id, workspace_id, project_id, viewport, metadata, created_at, updated_at
	`, workspaceID, projectID).Scan(&row.ID, &row.WorkspaceID, &row.ProjectID, &row.Viewport, &row.Metadata, &row.CreatedAt, &row.UpdatedAt)
	return row, err
}

func (h *Handler) loadVisualBoardResponse(r *http.Request, board visualBoardRow) (visualBoardResponse, error) {
	nodes, err := h.listVisualNodes(r, board.WorkspaceID, board.ProjectID)
	if err != nil {
		return visualBoardResponse{}, err
	}
	edges, err := h.listVisualEdges(r, board.WorkspaceID, board.ProjectID)
	if err != nil {
		return visualBoardResponse{}, err
	}
	resp := visualBoardResponse{
		ID:          uuidToString(board.ID),
		WorkspaceID: uuidToString(board.WorkspaceID),
		ProjectID:   uuidToString(board.ProjectID),
		Viewport:    json.RawMessage(defaultJSON(board.Viewport, "{}")),
		Metadata:    json.RawMessage(defaultJSON(board.Metadata, "{}")),
		Nodes:       make([]visualNodeResponse, len(nodes)),
		Edges:       make([]visualEdgeResponse, len(edges)),
		CreatedAt:   timestampToString(board.CreatedAt),
		UpdatedAt:   timestampToString(board.UpdatedAt),
	}
	for i, node := range nodes {
		resp.Nodes[i] = h.visualNodeToResponse(node)
	}
	for i, edge := range edges {
		resp.Edges[i] = visualEdgeToResponse(edge)
	}
	return resp, nil
}

func (h *Handler) listVisualNodes(r *http.Request, workspaceID, projectID pgtype.UUID) ([]visualNodeRow, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT n.id, n.board_id, n.workspace_id, n.project_id, n.type, n.status, n.title,
		       n.description, n.prompt, n.position_x, n.position_y, n.source_refs,
		       n.reference_attachment_ids, n.result_attachment_id, n.result_note,
		       n.generation_agent_id, n.generation_task_id, n.generation_error,
		       n.created_at, n.updated_at
		FROM project_visual_node n
		WHERE n.workspace_id = $1 AND n.project_id = $2
		ORDER BY n.created_at ASC
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []visualNodeRow
	for rows.Next() {
		var n visualNodeRow
		if err := rows.Scan(&n.ID, &n.BoardID, &n.WorkspaceID, &n.ProjectID, &n.Type, &n.Status, &n.Title,
			&n.Description, &n.Prompt, &n.PositionX, &n.PositionY, &n.SourceRefs, &n.ReferenceAttachmentIDs,
			&n.ResultAttachmentID, &n.ResultNote, &n.GenerationAgentID, &n.GenerationTaskID, &n.GenerationError,
			&n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		if n.ResultAttachmentID.Valid {
			if att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: n.ResultAttachmentID, WorkspaceID: workspaceID}); err == nil {
				resp := h.attachmentToResponse(att)
				n.ResultAttachment = &resp
			}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (h *Handler) listVisualEdges(r *http.Request, workspaceID, projectID pgtype.UUID) ([]visualEdgeRow, error) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, board_id, workspace_id, project_id, source_node_id, target_node_id,
		       relation, created_at, updated_at
		FROM project_visual_edge
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY created_at ASC
	`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []visualEdgeRow
	for rows.Next() {
		var e visualEdgeRow
		if err := rows.Scan(&e.ID, &e.BoardID, &e.WorkspaceID, &e.ProjectID, &e.SourceNodeID, &e.TargetNodeID, &e.Relation, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *Handler) loadVisualNode(w http.ResponseWriter, r *http.Request, workspaceID, projectID, nodeID pgtype.UUID) (visualNodeRow, bool) {
	var n visualNodeRow
	err := h.DB.QueryRow(r.Context(), `
		SELECT id, board_id, workspace_id, project_id, type, status, title, description,
		       prompt, position_x, position_y, source_refs, reference_attachment_ids,
		       result_attachment_id, result_note, generation_agent_id, generation_task_id,
		       generation_error, created_at, updated_at
		FROM project_visual_node
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, nodeID, workspaceID, projectID).Scan(&n.ID, &n.BoardID, &n.WorkspaceID, &n.ProjectID, &n.Type, &n.Status,
		&n.Title, &n.Description, &n.Prompt, &n.PositionX, &n.PositionY, &n.SourceRefs, &n.ReferenceAttachmentIDs,
		&n.ResultAttachmentID, &n.ResultNote, &n.GenerationAgentID, &n.GenerationTaskID, &n.GenerationError,
		&n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "visual node not found")
			return visualNodeRow{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load visual node")
		return visualNodeRow{}, false
	}
	return n, true
}

func (h *Handler) visualNodeToResponse(n visualNodeRow) visualNodeResponse {
	resp := visualNodeResponse{
		ID:                     uuidToString(n.ID),
		BoardID:                uuidToString(n.BoardID),
		WorkspaceID:            uuidToString(n.WorkspaceID),
		ProjectID:              uuidToString(n.ProjectID),
		Type:                   n.Type,
		Status:                 n.Status,
		Title:                  n.Title,
		Description:            n.Description,
		Prompt:                 n.Prompt,
		PositionX:              n.PositionX,
		PositionY:              n.PositionY,
		SourceRefs:             json.RawMessage(defaultJSON(n.SourceRefs, "[]")),
		ReferenceAttachmentIDs: uuidSliceToStrings(n.ReferenceAttachmentIDs),
		ResultNote:             n.ResultNote,
		GenerationError:        n.GenerationError,
		CreatedAt:              timestampToString(n.CreatedAt),
		UpdatedAt:              timestampToString(n.UpdatedAt),
		ResultAttachment:       n.ResultAttachment,
	}
	if n.ResultAttachmentID.Valid {
		v := uuidToString(n.ResultAttachmentID)
		resp.ResultAttachmentID = &v
	}
	if n.GenerationAgentID.Valid {
		v := uuidToString(n.GenerationAgentID)
		resp.GenerationAgentID = &v
	}
	if n.GenerationTaskID.Valid {
		v := uuidToString(n.GenerationTaskID)
		resp.GenerationTaskID = &v
	}
	return resp
}

func visualEdgeToResponse(e visualEdgeRow) visualEdgeResponse {
	return visualEdgeResponse{
		ID:           uuidToString(e.ID),
		BoardID:      uuidToString(e.BoardID),
		WorkspaceID:  uuidToString(e.WorkspaceID),
		ProjectID:    uuidToString(e.ProjectID),
		SourceNodeID: uuidToString(e.SourceNodeID),
		TargetNodeID: uuidToString(e.TargetNodeID),
		Relation:     e.Relation,
		CreatedAt:    timestampToString(e.CreatedAt),
		UpdatedAt:    timestampToString(e.UpdatedAt),
	}
}

type visualNodeCandidate struct {
	NodeType    string
	Title       string
	Description string
	Prompt      string
	SourceRefs  json.RawMessage
}

type visualBoardExtractResult struct {
	Nodes []visualBoardExtractNode `json:"nodes"`
	Edges []visualBoardExtractEdge `json:"edges"`
}

type visualBoardExtractNode struct {
	ID          string          `json:"id"`
	ClientID    string          `json:"client_id"`
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Prompt      string          `json:"prompt"`
	SourceRefs  json.RawMessage `json:"source_refs"`
	Confidence  float64         `json:"confidence"`
	PositionX   float64         `json:"position_x"`
	PositionY   float64         `json:"position_y"`
}

type visualBoardExtractEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
}

var visualHeadingRE = regexp.MustCompile(`(?m)^\s{0,3}#{1,4}\s+(.+?)\s*$`)

func extractVisualNodeCandidates(pages []service.WikiPage) []visualNodeCandidate {
	var out []visualNodeCandidate
	for _, page := range pages {
		body := strings.TrimSpace(page.Body)
		if body == "" {
			continue
		}
		matches := visualHeadingRE.FindAllStringSubmatchIndex(body, -1)
		for i, match := range matches {
			title := strings.TrimSpace(body[match[2]:match[3]])
			start := match[1]
			end := len(body)
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			section := strings.TrimSpace(body[start:end])
			nodeType, ok := inferVisualNodeType(title + "\n" + section)
			if !ok {
				continue
			}
			out = append(out, makeVisualNodeCandidate(nodeType, title, section, page))
			if len(out) >= 60 {
				return out
			}
		}
		if len(matches) == 0 {
			nodeType, ok := inferVisualNodeType(page.Title + "\n" + body)
			if ok {
				out = append(out, makeVisualNodeCandidate(nodeType, page.Title, body, page))
			}
		}
	}
	return out
}

func inferVisualNodeType(text string) (string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "角色") || strings.Contains(lower, "主角") || strings.Contains(lower, "npc") || strings.Contains(lower, "character"):
		return "character", true
	case strings.Contains(lower, "场景") || strings.Contains(lower, "关卡") || strings.Contains(lower, "地图") || strings.Contains(lower, "scene") || strings.Contains(lower, "level"):
		return "scene", true
	case strings.Contains(lower, "ui") || strings.Contains(lower, "界面") || strings.Contains(lower, "按钮") || strings.Contains(lower, "hud"):
		return "ui_element", true
	case strings.Contains(lower, "道具") || strings.Contains(lower, "物品") || strings.Contains(lower, "装备") || strings.Contains(lower, "prop") || strings.Contains(lower, "item"):
		return "prop", true
	case strings.Contains(lower, "参考") || strings.Contains(lower, "reference"):
		return "reference", true
	case strings.Contains(lower, "玩法") || strings.Contains(lower, "机制") || strings.Contains(lower, "gameplay"):
		return "gameplay_note", true
	default:
		return "", false
	}
}

func makeVisualNodeCandidate(nodeType, title, section string, page service.WikiPage) visualNodeCandidate {
	description := truncateRunes(strings.TrimSpace(section), 420)
	source, _ := json.Marshal([]map[string]string{{
		"wiki_page_id": page.ID,
		"wiki_slug":    page.Slug,
		"title":        page.Title,
		"snippet":      truncateRunes(section, 220),
	}})
	prompt := fmt.Sprintf("Create a production-ready %s visual asset for %q. Preserve the project's Wiki context, visual intent, gameplay purpose, and any constraints from this source excerpt: %s", nodeType, title, truncateRunes(section, 360))
	return visualNodeCandidate{
		NodeType:    nodeType,
		Title:       truncateRunes(title, 120),
		Description: description,
		Prompt:      prompt,
		SourceRefs:  source,
	}
}

func visualWikiPageContexts(pages []service.WikiPage) []service.VisualWikiPageContext {
	out := make([]service.VisualWikiPageContext, 0, len(pages))
	for _, page := range pages {
		body := strings.TrimSpace(page.Body)
		if body == "" {
			continue
		}
		out = append(out, service.VisualWikiPageContext{
			ID:    page.ID,
			Slug:  page.Slug,
			Title: page.Title,
			Body:  truncateRunes(body, 6000),
		})
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func parseVisualBoardExtractOutput(output string) (visualBoardExtractResult, error) {
	raw := strings.TrimSpace(util.UnescapeBackslashEscapes(output))
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return visualBoardExtractResult{}, fmt.Errorf("missing JSON object")
	}
	raw = raw[start : end+1]
	var result visualBoardExtractResult
	if err := json.Unmarshal([]byte(raw), &result); err == nil && len(result.Nodes) > 0 {
		return result, nil
	}
	var wrapped struct {
		VisualBoardExtract visualBoardExtractResult `json:"visual_board_extract"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err != nil {
		return visualBoardExtractResult{}, err
	}
	if len(wrapped.VisualBoardExtract.Nodes) == 0 {
		return visualBoardExtractResult{}, fmt.Errorf("visual_board_extract output has no nodes")
	}
	return wrapped.VisualBoardExtract, nil
}

func (h *Handler) applyVisualBoardExtractCompleted(ctx context.Context, task db.AgentTaskQueue, output string) {
	visualCtx, ok := h.visualTaskContextFromTask(task)
	if !ok || visualCtx.Type != visualTaskTypeExtract {
		return
	}
	result, err := parseVisualBoardExtractOutput(output)
	if err != nil {
		slog.Warn("visual board extract completion: invalid output", "task_id", uuidToString(task.ID), "error", err)
		h.markVisualBoardExtractFailed(ctx, visualCtx, err.Error())
		return
	}
	workspaceID, err := util.ParseUUID(visualCtx.WorkspaceID)
	if err != nil {
		slog.Warn("visual board extract completion: invalid workspace id", "task_id", uuidToString(task.ID), "error", err)
		return
	}
	projectID, err := util.ParseUUID(visualCtx.ProjectID)
	if err != nil {
		slog.Warn("visual board extract completion: invalid project id", "task_id", uuidToString(task.ID), "error", err)
		return
	}
	boardID, err := util.ParseUUID(visualCtx.BoardID)
	if err != nil {
		slog.Warn("visual board extract completion: invalid board id", "task_id", uuidToString(task.ID), "error", err)
		return
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		slog.Warn("visual board extract completion: begin tx failed", "task_id", uuidToString(task.ID), "error", err)
		return
	}
	defer tx.Rollback(ctx)

	nodeIDs := map[string]pgtype.UUID{}
	created := 0
	for i, node := range result.Nodes {
		title := strings.TrimSpace(node.Title)
		if title == "" {
			continue
		}
		clientID := strings.TrimSpace(node.ClientID)
		if clientID == "" {
			clientID = strings.TrimSpace(node.ID)
		}
		if clientID == "" {
			clientID = fmt.Sprintf("node_%d", i+1)
		}
		nodeType := normalizeVisualNodeType(strings.TrimSpace(node.Type))
		description := strings.TrimSpace(node.Description)
		prompt := strings.TrimSpace(node.Prompt)
		if prompt == "" {
			prompt = fmt.Sprintf("Create a production-ready %s visual asset for %q. Use the Project Wiki context and keep the result coherent with adjacent visual nodes.", nodeType, title)
		}
		x := node.PositionX
		y := node.PositionY
		if x == 0 && y == 0 {
			x = float64((created % 3) * 320)
			y = float64((created / 3) * 260)
		}
		sourceRefs := normalizeJSONRaw(node.SourceRefs, "[]")
		var inserted pgtype.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO project_visual_node (
				board_id, workspace_id, project_id, type, status, title, description, prompt,
				position_x, position_y, source_refs
			)
			VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7, $8, $9, $10)
			RETURNING id
		`, boardID, workspaceID, projectID, nodeType, truncateRunes(title, 160), description, prompt, x, y, sourceRefs).Scan(&inserted)
		if err != nil {
			slog.Warn("visual board extract completion: insert node failed", "task_id", uuidToString(task.ID), "error", err)
			return
		}
		nodeIDs[clientID] = inserted
		created++
	}
	for _, edge := range result.Edges {
		sourceID, sourceOK := nodeIDs[strings.TrimSpace(edge.Source)]
		targetID, targetOK := nodeIDs[strings.TrimSpace(edge.Target)]
		if !sourceOK || !targetOK {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_visual_edge (
				board_id, workspace_id, project_id, source_node_id, target_node_id, relation
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, boardID, workspaceID, projectID, sourceID, targetID, truncateRunes(edge.Relation, 80)); err != nil {
			slog.Warn("visual board extract completion: insert edge failed", "task_id", uuidToString(task.ID), "error", err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE project_visual_board
		SET metadata = jsonb_set(jsonb_set(coalesce(metadata, '{}'::jsonb), '{last_extract_task_id}', to_jsonb($1::text), true), '{last_extract_error}', 'null'::jsonb, true),
		    updated_at = now()
		WHERE id = $2 AND workspace_id = $3 AND project_id = $4
	`, uuidToString(task.ID), boardID, workspaceID, projectID); err != nil {
		slog.Warn("visual board extract completion: update board metadata failed", "task_id", uuidToString(task.ID), "error", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("visual board extract completion: commit failed", "task_id", uuidToString(task.ID), "error", err)
		return
	}
	h.publishProjectVisualBoardUpdated(workspaceID, projectID, "extracted")
}

func (h *Handler) markVisualBoardExtractFailed(ctx context.Context, visualCtx visualTaskContext, message string) {
	workspaceID, err := util.ParseUUID(visualCtx.WorkspaceID)
	if err != nil {
		return
	}
	projectID, err := util.ParseUUID(visualCtx.ProjectID)
	if err != nil {
		return
	}
	boardID, err := util.ParseUUID(visualCtx.BoardID)
	if err != nil {
		return
	}
	if _, err := h.DB.Exec(ctx, `
		UPDATE project_visual_board
		SET metadata = jsonb_set(coalesce(metadata, '{}'::jsonb), '{last_extract_error}', to_jsonb($1::text), true),
		    updated_at = now()
		WHERE id = $2 AND workspace_id = $3 AND project_id = $4
	`, truncateRunes(message, 500), boardID, workspaceID, projectID); err != nil {
		slog.Warn("visual board extract completion: mark failure failed", "error", err)
		return
	}
	h.publishProjectVisualBoardUpdated(workspaceID, projectID, "failed")
}

func buildVisualPlanPrompt(projectTitle string, nodes []visualNodeRow, gameplayNotes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project visual canvas has been reviewed for %q. Create an implementation plan from adopted visual nodes only.\n\n", projectTitle)
	b.WriteString("Adopted visual nodes:\n")
	for _, node := range nodes {
		fmt.Fprintf(&b, "- [%s] %s\n  Description: %s\n  Prompt: %s\n", node.Type, node.Title, node.Description, node.Prompt)
		if node.ResultAttachmentID.Valid {
			fmt.Fprintf(&b, "  Result attachment: %s\n", uuidToString(node.ResultAttachmentID))
		}
	}
	if strings.TrimSpace(gameplayNotes) != "" {
		fmt.Fprintf(&b, "\nGameplay notes:\n%s\n", strings.TrimSpace(gameplayNotes))
	}
	b.WriteString("\nExclude every draft or rejected visual node. Treat visual nodes as art direction and context; convert only gameplay/engineering work into issues.\n")
	return b.String()
}

func normalizeJSONRaw(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(fallback)
	}
	return raw
}

func defaultJSON(raw []byte, fallback string) []byte {
	if len(raw) == 0 || !json.Valid(raw) {
		return []byte(fallback)
	}
	return raw
}

func normalizeVisualNodeType(value string) string {
	switch value {
	case "character", "scene", "ui_element", "prop", "reference", "gameplay_note", "generated_variant":
		return value
	default:
		return "reference"
	}
}

func normalizeVisualNodeStatus(value string) string {
	switch value {
	case "draft", "adopted", "rejected", "generating", "failed":
		return value
	default:
		return "draft"
	}
}

func uuidSliceToStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id.Valid {
			out = append(out, util.UUIDToString(id))
		}
	}
	return out
}

func truncateRunes(s string, limit int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func (h *Handler) visualTaskContextFromTask(task db.AgentTaskQueue) (visualTaskContext, bool) {
	if task.Context == nil {
		return visualTaskContext{}, false
	}
	var ctx visualTaskContext
	if json.Unmarshal(task.Context, &ctx) != nil || (ctx.Type != visualTaskTypeGenerate && ctx.Type != visualTaskTypeExtract) {
		return visualTaskContext{}, false
	}
	return ctx, true
}

func (h *Handler) resolveVisualTaskWorkspaceID(task db.AgentTaskQueue) string {
	if ctx, ok := h.visualTaskContextFromTask(task); ok {
		return ctx.WorkspaceID
	}
	return ""
}

func (h *Handler) publishProjectVisualUpdated(workspaceID, projectID, nodeID pgtype.UUID, status string) {
	h.publish(protocol.EventProjectVisualUpdated, uuidToString(workspaceID), "system", "", map[string]any{
		"project_id": uuidToString(projectID),
		"node_id":    uuidToString(nodeID),
		"status":     status,
	})
}

func (h *Handler) publishProjectVisualBoardUpdated(workspaceID, projectID pgtype.UUID, status string) {
	h.publish(protocol.EventProjectVisualUpdated, uuidToString(workspaceID), "system", "", map[string]any{
		"project_id": uuidToString(projectID),
		"status":     status,
	})
}

func visualTaskKind(task db.AgentTaskQueue) string {
	if task.Context == nil {
		return ""
	}
	var ctx visualTaskContext
	if json.Unmarshal(task.Context, &ctx) == nil && (ctx.Type == visualTaskTypeGenerate || ctx.Type == visualTaskTypeExtract) {
		return "visual"
	}
	return ""
}
