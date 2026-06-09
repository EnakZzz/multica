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
	TitleZh                string              `json:"title_zh"`
	Description            string              `json:"description"`
	DescriptionZh          string              `json:"description_zh"`
	Prompt                 string              `json:"prompt"`
	PromptZh               string              `json:"prompt_zh"`
	PositionX              float64             `json:"position_x"`
	PositionY              float64             `json:"position_y"`
	ImplementationPath     string              `json:"implementation_path"`
	ImplementationNote     string              `json:"implementation_note"`
	SourceRefs             json.RawMessage     `json:"source_refs"`
	ReferenceAttachmentIDs []string            `json:"reference_attachment_ids"`
	ResultAttachmentID     *string             `json:"result_attachment_id"`
	ResultAttachment       *AttachmentResponse `json:"result_attachment"`
	ResultNote             string              `json:"result_note"`
	ResultNoteZh           string              `json:"result_note_zh"`
	GenerationAgentID      *string             `json:"generation_agent_id"`
	GenerationTaskID       *string             `json:"generation_task_id"`
	GenerationError        string              `json:"generation_error"`
	GenerationErrorZh      string              `json:"generation_error_zh"`
	CreatedAt              string              `json:"created_at"`
	UpdatedAt              string              `json:"updated_at"`
}

type visualNodeGenerationResponse struct {
	ID              string              `json:"id"`
	TaskID          string              `json:"task_id"`
	TaskStatus      string              `json:"task_status"`
	IssueID         string              `json:"issue_id"`
	IssueIdentifier string              `json:"issue_identifier"`
	IssueTitle      string              `json:"issue_title"`
	IssueStatus     string              `json:"issue_status"`
	AttachmentID    *string             `json:"attachment_id"`
	Attachment      *AttachmentResponse `json:"attachment"`
	Note            string              `json:"note"`
	NoteZh          string              `json:"note_zh"`
	Error           string              `json:"error"`
	ErrorZh         string              `json:"error_zh"`
	IsCurrent       bool                `json:"is_current"`
	CreatedAt       string              `json:"created_at"`
	CompletedAt     string              `json:"completed_at"`
}

type listVisualNodeGenerationsResponse struct {
	Generations []visualNodeGenerationResponse `json:"generations"`
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
	TitleZh                string
	Description            string
	DescriptionZh          string
	Prompt                 string
	PromptZh               string
	PositionX              float64
	PositionY              float64
	ImplementationPath     string
	ImplementationNote     string
	SourceRefs             []byte
	ReferenceAttachmentIDs []pgtype.UUID
	ResultAttachmentID     pgtype.UUID
	ResultNote             string
	ResultNoteZh           string
	GenerationAgentID      pgtype.UUID
	GenerationTaskID       pgtype.UUID
	GenerationError        string
	GenerationErrorZh      string
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
	ID                 string          `json:"id"`
	Type               string          `json:"type"`
	Status             string          `json:"status"`
	Title              string          `json:"title"`
	TitleZh            string          `json:"title_zh"`
	Description        string          `json:"description"`
	DescriptionZh      string          `json:"description_zh"`
	Prompt             string          `json:"prompt"`
	PromptZh           string          `json:"prompt_zh"`
	PositionX          float64         `json:"position_x"`
	PositionY          float64         `json:"position_y"`
	ImplementationPath string          `json:"implementation_path"`
	ImplementationNote string          `json:"implementation_note"`
	SourceRefs         json.RawMessage `json:"source_refs"`
}

type createVisualNodeRequest struct {
	Type               string          `json:"type"`
	Title              string          `json:"title"`
	TitleZh            string          `json:"title_zh"`
	Description        string          `json:"description"`
	DescriptionZh      string          `json:"description_zh"`
	Prompt             string          `json:"prompt"`
	PromptZh           string          `json:"prompt_zh"`
	PositionX          float64         `json:"position_x"`
	PositionY          float64         `json:"position_y"`
	ImplementationPath string          `json:"implementation_path"`
	ImplementationNote string          `json:"implementation_note"`
	SourceRefs         json.RawMessage `json:"source_refs"`
	SourceNodeID       string          `json:"source_node_id"`
	Relation           string          `json:"relation"`
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
	TaskID       string `json:"task_id"`
	Note         string `json:"note"`
	NoteZh       string `json:"note_zh"`
	Error        string `json:"error"`
	ErrorZh      string `json:"error_zh"`
}

type createVisualPlanRequest struct {
	GameplayNotes string `json:"gameplay_notes"`
	PlanMode      string `json:"plan_mode"`
	Title         string `json:"title"`
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
	project, ok := h.loadProjectForResourceAccess(w, r, chi.URLParam(r, "id"), false)
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
			SET type = $1, status = $2, title = $3,
			    title_zh = CASE WHEN $4 = '' THEN COALESCE(NULLIF(title_zh, ''), $3) ELSE $4 END,
			    description = $5,
			    description_zh = CASE WHEN $6 = '' THEN COALESCE(NULLIF(description_zh, ''), $5) ELSE $6 END,
			    prompt = $7,
			    prompt_zh = CASE WHEN $8 = '' THEN COALESCE(NULLIF(prompt_zh, ''), $7) ELSE $8 END,
			    position_x = $9, position_y = $10,
			    implementation_path = $11, implementation_note = $12,
			    source_refs = $13, updated_at = now()
			WHERE id = $14 AND board_id = $15 AND workspace_id = $16 AND project_id = $17
		`, nodeType, status, title, strings.TrimSpace(nodeReq.TitleZh),
			nodeReq.Description, strings.TrimSpace(nodeReq.DescriptionZh),
			nodeReq.Prompt, strings.TrimSpace(nodeReq.PromptZh),
			nodeReq.PositionX, nodeReq.PositionY,
			strings.TrimSpace(nodeReq.ImplementationPath), strings.TrimSpace(nodeReq.ImplementationNote),
			normalizeJSONRaw(nodeReq.SourceRefs, "[]"),
			nodeID, board.ID, project.WorkspaceID, project.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update visual node")
			return
		}
	}
	if req.Edges != nil {
		if err := h.syncVisualBoardEdges(r.Context(), board, project.WorkspaceID, project.ID, req.Edges); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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

func (h *Handler) ClearProjectVisualBoard(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear visual board")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		DELETE FROM project_visual_edge
		WHERE board_id = $1 AND workspace_id = $2 AND project_id = $3
	`, board.ID, project.WorkspaceID, project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear visual board")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		DELETE FROM project_visual_node
		WHERE board_id = $1 AND workspace_id = $2 AND project_id = $3
	`, board.ID, project.WorkspaceID, project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear visual board")
		return
	}
	if err := tx.QueryRow(r.Context(), `
		UPDATE project_visual_board
		SET updated_at = now()
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
		RETURNING id, workspace_id, project_id, viewport, metadata, created_at, updated_at
	`, board.ID, project.WorkspaceID, project.ID).Scan(&board.ID, &board.WorkspaceID, &board.ProjectID, &board.Viewport, &board.Metadata, &board.CreatedAt, &board.UpdatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear visual board")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear visual board")
		return
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	h.publishProjectVisualBoardUpdated(project.WorkspaceID, project.ID, "cleared")
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) syncVisualBoardEdges(ctx context.Context, board visualBoardRow, workspaceID, projectID pgtype.UUID, edges []updateVisualEdgeRequest) error {
	keep := map[string]struct{}{}
	for _, edgeReq := range edges {
		sourceID, err := util.ParseUUID(strings.TrimSpace(edgeReq.SourceNodeID))
		if err != nil {
			return fmt.Errorf("invalid visual edge source_node_id")
		}
		targetID, err := util.ParseUUID(strings.TrimSpace(edgeReq.TargetNodeID))
		if err != nil {
			return fmt.Errorf("invalid visual edge target_node_id")
		}
		if uuidToString(sourceID) == uuidToString(targetID) {
			continue
		}
		relation := strings.TrimSpace(edgeReq.Relation)
		if relation == "" {
			relation = "reference"
		}
		if edgeID, err := util.ParseUUID(strings.TrimSpace(edgeReq.ID)); err == nil {
			tag, err := h.DB.Exec(ctx, `
				UPDATE project_visual_edge
				SET source_node_id = $1, target_node_id = $2, relation = $3, updated_at = now()
				WHERE id = $4 AND board_id = $5 AND workspace_id = $6 AND project_id = $7
			`, sourceID, targetID, truncateRunes(relation, 80), edgeID, board.ID, workspaceID, projectID)
			if err != nil {
				return fmt.Errorf("update visual edge: %w", err)
			}
			if tag.RowsAffected() > 0 {
				keep[uuidToString(edgeID)] = struct{}{}
				continue
			}
		}
		var inserted pgtype.UUID
		err = h.DB.QueryRow(ctx, `
			INSERT INTO project_visual_edge (
				board_id, workspace_id, project_id, source_node_id, target_node_id, relation
			)
			SELECT $1, $2, $3, $4, $5, $6
			WHERE EXISTS (
				SELECT 1 FROM project_visual_node
				WHERE id = $4 AND board_id = $1 AND workspace_id = $2 AND project_id = $3
			)
			AND EXISTS (
				SELECT 1 FROM project_visual_node
				WHERE id = $5 AND board_id = $1 AND workspace_id = $2 AND project_id = $3
			)
			AND NOT EXISTS (
				SELECT 1 FROM project_visual_edge
				WHERE board_id = $1 AND workspace_id = $2 AND project_id = $3
				  AND source_node_id = $4 AND target_node_id = $5 AND relation = $6
			)
			RETURNING id
		`, board.ID, workspaceID, projectID, sourceID, targetID, truncateRunes(relation, 80)).Scan(&inserted)
		if err != nil {
			if err == pgx.ErrNoRows {
				continue
			}
			return fmt.Errorf("insert visual edge: %w", err)
		}
		keep[uuidToString(inserted)] = struct{}{}
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id
		FROM project_visual_edge
		WHERE board_id = $1 AND workspace_id = $2 AND project_id = $3
	`, board.ID, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("list visual edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan visual edge: %w", err)
		}
		if _, ok := keep[uuidToString(id)]; ok {
			continue
		}
		if _, err := h.DB.Exec(ctx, `
			DELETE FROM project_visual_edge
			WHERE id = $1 AND board_id = $2 AND workspace_id = $3 AND project_id = $4
		`, id, board.ID, workspaceID, projectID); err != nil {
			return fmt.Errorf("delete visual edge: %w", err)
		}
	}
	return rows.Err()
}

func (h *Handler) CreateProjectVisualNode(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req createVisualNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	nodeType := normalizeVisualNodeType(req.Type)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "node title is required")
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual node")
		return
	}
	defer tx.Rollback(r.Context())
	var inserted pgtype.UUID
	description := strings.TrimSpace(req.Description)
	prompt := strings.TrimSpace(req.Prompt)
	err = tx.QueryRow(r.Context(), `
		INSERT INTO project_visual_node (
			board_id, workspace_id, project_id, type, status, title, title_zh, description, description_zh, prompt, prompt_zh,
			position_x, position_y, implementation_path, implementation_note, source_refs
		)
		VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id
	`, board.ID, project.WorkspaceID, project.ID, nodeType, truncateRunes(title, 160),
		visualTextOrFallback(req.TitleZh, title), description, visualTextOrFallback(req.DescriptionZh, description),
		prompt, visualTextOrFallback(req.PromptZh, prompt), req.PositionX, req.PositionY,
		strings.TrimSpace(req.ImplementationPath), strings.TrimSpace(req.ImplementationNote),
		normalizeJSONRaw(req.SourceRefs, "[]")).Scan(&inserted)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual node")
		return
	}
	if strings.TrimSpace(req.SourceNodeID) != "" {
		sourceNodeID, ok := parseUUIDOrBadRequest(w, req.SourceNodeID, "source_node_id")
		if !ok {
			return
		}
		if _, ok := h.loadVisualNode(w, r, project.WorkspaceID, project.ID, sourceNodeID); !ok {
			return
		}
		relation := strings.TrimSpace(req.Relation)
		if relation == "" {
			relation = "variant_of"
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO project_visual_edge (
				board_id, workspace_id, project_id, source_node_id, target_node_id, relation
			)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, board.ID, project.WorkspaceID, project.ID, sourceNodeID, inserted, relation); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to link visual node")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual node")
		return
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	h.publishProjectVisualUpdated(project.WorkspaceID, project.ID, inserted, "created")
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) DeleteProjectVisualNode(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "visual node id")
	if !ok {
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	if _, ok := h.loadVisualNode(w, r, project.WorkspaceID, project.ID, nodeID); !ok {
		return
	}
	if _, err := h.DB.Exec(r.Context(), `
		DELETE FROM project_visual_node
		WHERE id = $1 AND board_id = $2 AND workspace_id = $3 AND project_id = $4
	`, nodeID, board.ID, project.WorkspaceID, project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete visual node")
		return
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	h.publishProjectVisualUpdated(project.WorkspaceID, project.ID, nodeID, "deleted")
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
	agent, ok := h.loadInternalPlannerAgent(w, r, project.WorkspaceID)
	if !ok {
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

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue visual extraction task")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	issueNumber, err := qtx.IncrementIssueCounter(r.Context(), project.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
		return
	}
	issueTitle := "Generate visual nodes from Project Wiki"
	issue, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
		WorkspaceID:   project.WorkspaceID,
		Title:         issueTitle,
		Description:   strOrNullText(visualBoardExtractIssueDescription(project.Title, board.ID)),
		Status:        "todo",
		Priority:      "none",
		AssigneeType:  pgtype.Text{String: "agent", Valid: true},
		AssigneeID:    agent.ID,
		CreatorType:   "member",
		CreatorID:     parseUUID(userID),
		ParentIssueID: pgtype.UUID{},
		Position:      0,
		Number:        issueNumber,
		ProjectID:     project.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual extraction issue")
		return
	}
	task, err := qtx.CreateContextTask(r.Context(), db.CreateContextTaskParams{
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
	if err := qtx.LinkTaskToIssue(r.Context(), db.LinkTaskToIssueParams{ID: task.ID, IssueID: issue.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link visual extraction issue")
		return
	}
	task, err = qtx.GetAgentTask(r.Context(), task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual extraction task")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue visual extraction task")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	issueResp := issueToResponse(issue, prefix)
	h.publish(protocol.EventIssueCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{"issue": issueResp})
	h.TaskService.NotifyTaskEnqueued(r.Context(), task)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id":          uuidToString(task.ID),
		"issue_id":         uuidToString(issue.ID),
		"issue_identifier": issueResp.Identifier,
	})
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue visual task")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	issueNumber, err := qtx.IncrementIssueCounter(r.Context(), project.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
		return
	}
	issueTitle := "Generate " + visualNodeAssetKind(node.Type) + ": " + node.Title
	issue, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
		WorkspaceID:   project.WorkspaceID,
		Title:         truncateRunes(issueTitle, 180),
		Description:   strOrNullText(visualNodeGenerateIssueDescription(project.Title, node)),
		Status:        "todo",
		Priority:      "none",
		AssigneeType:  pgtype.Text{String: "agent", Valid: true},
		AssigneeID:    agent.ID,
		CreatorType:   "member",
		CreatorID:     parseUUID(userID),
		ParentIssueID: pgtype.UUID{},
		Position:      0,
		Number:        issueNumber,
		ProjectID:     project.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create visual generation issue")
		return
	}
	task, err := qtx.CreateContextTask(r.Context(), db.CreateContextTaskParams{
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
	if err := qtx.LinkTaskToIssue(r.Context(), db.LinkTaskToIssueParams{ID: task.ID, IssueID: issue.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link visual generation issue")
		return
	}
	_, err = tx.Exec(r.Context(), `
		UPDATE project_visual_node
		SET status = 'generating', generation_agent_id = $1, generation_task_id = $2,
		    generation_error = '', generation_error_zh = '', updated_at = now()
		WHERE id = $3 AND workspace_id = $4 AND project_id = $5
	`, agentID, task.ID, node.ID, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update visual node")
		return
	}
	task, err = qtx.GetAgentTask(r.Context(), task.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual task")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to queue visual task")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	issueResp := issueToResponse(issue, prefix)
	h.publish(protocol.EventIssueCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{"issue": issueResp})
	h.TaskService.NotifyTaskEnqueued(r.Context(), task)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id":          uuidToString(task.ID),
		"issue_id":         uuidToString(issue.ID),
		"issue_identifier": issueResp.Identifier,
	})
}

func (h *Handler) ListProjectVisualNodeGenerations(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "visual node id")
	if !ok {
		return
	}
	node, ok := h.loadVisualNode(w, r, project.WorkspaceID, project.ID, nodeID)
	if !ok {
		return
	}
	generations, err := h.listVisualNodeGenerations(r, project.WorkspaceID, project.ID, node)
	if err != nil {
		slog.Warn("list visual node generations failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list visual node generations")
		return
	}
	writeJSON(w, http.StatusOK, listVisualNodeGenerationsResponse{Generations: generations})
}

func (h *Handler) RestoreProjectVisualNodeGeneration(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeId"), "visual node id")
	if !ok {
		return
	}
	generationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "generationId"), "visual generation id")
	if !ok {
		return
	}
	_, ok = h.loadVisualNode(w, r, project.WorkspaceID, project.ID, nodeID)
	if !ok {
		return
	}
	var attachmentID pgtype.UUID
	var note, noteZh string
	var taskID, agentID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT g.attachment_id, g.note, g.note_zh, g.task_id, atq.agent_id
		FROM project_visual_node_generation g
		LEFT JOIN agent_task_queue atq ON atq.id = g.task_id
		WHERE g.id = $1 AND g.workspace_id = $2 AND g.project_id = $3 AND g.node_id = $4
		  AND g.attachment_id IS NOT NULL
	`, generationID, project.WorkspaceID, project.ID, nodeID).Scan(&attachmentID, &note, &noteZh, &taskID, &agentID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "visual generation result not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load visual generation")
		return
	}
	_, err = h.DB.Exec(r.Context(), `
		UPDATE project_visual_node
		SET status = CASE WHEN status = 'generating' THEN 'draft' ELSE status END,
		    result_attachment_id = $1, result_note = $2, result_note_zh = $3,
		    generation_task_id = $4, generation_agent_id = $5,
		    generation_error = '', generation_error_zh = '', updated_at = now()
		WHERE id = $6 AND workspace_id = $7 AND project_id = $8
	`, attachmentID, note, noteZh, taskID, agentID, nodeID, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore visual generation")
		return
	}
	board, err := h.ensureVisualBoard(r, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	resp, err := h.loadVisualBoardResponse(r, board)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load visual board")
		return
	}
	h.publishProjectVisualUpdated(project.WorkspaceID, project.ID, nodeID, "restored")
	writeJSON(w, http.StatusOK, resp)
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
	node, ok := h.loadVisualNode(w, r, project.WorkspaceID, project.ID, nodeID)
	if !ok {
		return
	}
	taskID, issueID, _, ok := h.visualGenerationTaskForCompletion(w, r, project.WorkspaceID, project.ID, node, req.TaskID)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Error) != "" {
		errorText := strings.TrimSpace(req.Error)
		errorZh := visualTextOrFallback(req.ErrorZh, errorText)
		note := strings.TrimSpace(req.Note)
		noteZh := visualTextOrFallback(req.NoteZh, note)
		tag, err := h.DB.Exec(r.Context(), `
			UPDATE project_visual_node
			SET status = 'failed', generation_error = $1, generation_error_zh = $2,
			    result_note = $3, result_note_zh = $4,
			    generation_task_id = CASE WHEN $5::uuid IS NULL THEN generation_task_id ELSE $5 END,
			    updated_at = now()
			WHERE id = $6 AND workspace_id = $7 AND project_id = $8
		`, errorText, errorZh, note, noteZh, taskID, nodeID, project.WorkspaceID, project.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update visual node")
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "visual node not found")
			return
		}
		if err := h.recordVisualNodeGeneration(r.Context(), node, taskID, issueID, pgtype.UUID{}, note, noteZh, errorText, errorZh); err != nil {
			slog.Warn("record visual node generation failed", append(logger.RequestAttrs(r), "node_id", uuidToString(nodeID), "task_id", uuidToString(taskID), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to record visual generation")
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
	if issueID.Valid {
		h.linkAttachmentsByIssueIDs(r.Context(), issueID, project.WorkspaceID, []pgtype.UUID{attachmentID})
	}
	note := strings.TrimSpace(req.Note)
	noteZh := visualTextOrFallback(req.NoteZh, note)
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE project_visual_node
		SET status = CASE WHEN status = 'generating' THEN 'draft' ELSE status END,
		    result_attachment_id = $1, result_note = $2, result_note_zh = $3,
		    generation_task_id = CASE WHEN $4::uuid IS NULL THEN generation_task_id ELSE $4 END,
		    generation_error = '', generation_error_zh = '', updated_at = now()
		WHERE id = $5 AND workspace_id = $6 AND project_id = $7
	`, attachmentID, note, noteZh, taskID, nodeID, project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update visual node")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "visual node not found")
		return
	}
	if err := h.recordVisualNodeGeneration(r.Context(), node, taskID, issueID, attachmentID, note, noteZh, "", ""); err != nil {
		slog.Warn("record visual node generation failed", append(logger.RequestAttrs(r), "node_id", uuidToString(nodeID), "task_id", uuidToString(taskID), "attachment_id", uuidToString(attachmentID), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to record visual generation")
		return
	}
	h.createArtifactReviewItemForVisualNode(r.Context(), project, node, nodeID, taskID, issueID, attachmentID, note, noteZh)
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
	plannerAgent, ok := h.loadInternalPlannerAgent(w, r, project.WorkspaceID)
	if !ok {
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
	planMode := normalizeVisualPlanMode(req.PlanMode)
	prompt := buildVisualPlanPrompt(project.Title, adopted, req.GameplayNotes, planMode)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Plan from visual canvas: " + project.Title
	}
	plan, err := h.Queries.CreatePlan(r.Context(), db.CreatePlanParams{
		WorkspaceID:    project.WorkspaceID,
		Title:          title,
		Prompt:         prompt,
		PlannerAgentID: plannerAgent.ID,
		CreatedBy:      parseUUID(userID),
		ProjectID:      project.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), project.WorkspaceID, parseUUID(userID), plan.ID, plannerAgent.ID, prompt, project.ID, service.IssuePlanPhaseSpec, service.PlanSpec{})
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

func (h *Handler) loadInternalPlannerAgent(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (db.Agent, bool) {
	agent, err := h.Queries.GetInternalPlannerAgent(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "internal planner agent is not available")
		return db.Agent{}, false
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "internal planner agent is archived")
		return db.Agent{}, false
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusBadRequest, "internal planner agent has no runtime")
		return db.Agent{}, false
	}
	return agent, true
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
		       n.title_zh, n.description, n.description_zh, n.prompt, n.prompt_zh,
		       n.position_x, n.position_y, n.implementation_path, n.implementation_note, n.source_refs,
		       n.reference_attachment_ids, n.result_attachment_id, n.result_note, n.result_note_zh,
		       n.generation_agent_id, n.generation_task_id, n.generation_error, n.generation_error_zh,
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
			&n.TitleZh, &n.Description, &n.DescriptionZh, &n.Prompt, &n.PromptZh,
			&n.PositionX, &n.PositionY, &n.ImplementationPath, &n.ImplementationNote,
			&n.SourceRefs, &n.ReferenceAttachmentIDs,
			&n.ResultAttachmentID, &n.ResultNote, &n.ResultNoteZh,
			&n.GenerationAgentID, &n.GenerationTaskID, &n.GenerationError, &n.GenerationErrorZh,
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
		       title_zh, description_zh, prompt, prompt_zh, position_x, position_y,
		       implementation_path, implementation_note, source_refs, reference_attachment_ids,
		       result_attachment_id, result_note, result_note_zh, generation_agent_id, generation_task_id,
		       generation_error, generation_error_zh, created_at, updated_at
		FROM project_visual_node
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, nodeID, workspaceID, projectID).Scan(&n.ID, &n.BoardID, &n.WorkspaceID, &n.ProjectID, &n.Type, &n.Status,
		&n.Title, &n.Description, &n.TitleZh, &n.DescriptionZh, &n.Prompt, &n.PromptZh,
		&n.PositionX, &n.PositionY, &n.ImplementationPath, &n.ImplementationNote,
		&n.SourceRefs, &n.ReferenceAttachmentIDs,
		&n.ResultAttachmentID, &n.ResultNote, &n.ResultNoteZh, &n.GenerationAgentID, &n.GenerationTaskID,
		&n.GenerationError, &n.GenerationErrorZh,
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
		TitleZh:                n.TitleZh,
		Description:            n.Description,
		DescriptionZh:          n.DescriptionZh,
		Prompt:                 n.Prompt,
		PromptZh:               n.PromptZh,
		PositionX:              n.PositionX,
		PositionY:              n.PositionY,
		ImplementationPath:     n.ImplementationPath,
		ImplementationNote:     n.ImplementationNote,
		SourceRefs:             json.RawMessage(defaultJSON(n.SourceRefs, "[]")),
		ReferenceAttachmentIDs: uuidSliceToStrings(n.ReferenceAttachmentIDs),
		ResultNote:             n.ResultNote,
		ResultNoteZh:           n.ResultNoteZh,
		GenerationError:        n.GenerationError,
		GenerationErrorZh:      n.GenerationErrorZh,
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

func (h *Handler) visualGenerationTaskForCompletion(w http.ResponseWriter, r *http.Request, workspaceID, projectID pgtype.UUID, node visualNodeRow, requestedTaskID string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	taskID := node.GenerationTaskID
	if strings.TrimSpace(requestedTaskID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, requestedTaskID, "task_id")
		if !ok {
			return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
		}
		taskID = parsed
	}
	if !taskID.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, true
	}
	var issueID, agentID pgtype.UUID
	err := h.DB.QueryRow(r.Context(), `
		SELECT atq.issue_id, atq.agent_id
		FROM agent_task_queue atq
		JOIN issue i ON i.id = atq.issue_id
		WHERE atq.id = $1
		  AND i.workspace_id = $2
		  AND i.project_id = $3
		  AND atq.context->>'type' = $4
		  AND atq.context->>'node_id' = $5
	`, taskID, workspaceID, projectID, visualTaskTypeGenerate, uuidToString(node.ID)).Scan(&issueID, &agentID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusBadRequest, "task_id does not refer to this visual node generation")
			return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load visual generation task")
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return taskID, issueID, agentID, true
}

func (h *Handler) recordVisualNodeGeneration(ctx context.Context, node visualNodeRow, taskID, issueID, attachmentID pgtype.UUID, note, noteZh, errorText, errorZh string) error {
	if taskID.Valid {
		_, err := h.DB.Exec(ctx, `
			INSERT INTO project_visual_node_generation (
				board_id, workspace_id, project_id, node_id, task_id, issue_id, attachment_id,
				note, note_zh, error, error_zh
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (task_id) DO UPDATE SET
				issue_id = EXCLUDED.issue_id,
				attachment_id = EXCLUDED.attachment_id,
				note = EXCLUDED.note,
				note_zh = EXCLUDED.note_zh,
				error = EXCLUDED.error,
				error_zh = EXCLUDED.error_zh,
				updated_at = now()
		`, node.BoardID, node.WorkspaceID, node.ProjectID, node.ID, taskID, issueID, attachmentID, note, noteZh, errorText, errorZh)
		return err
	}
	_, err := h.DB.Exec(ctx, `
		INSERT INTO project_visual_node_generation (
			board_id, workspace_id, project_id, node_id, issue_id, attachment_id,
			note, note_zh, error, error_zh
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, node.BoardID, node.WorkspaceID, node.ProjectID, node.ID, issueID, attachmentID, note, noteZh, errorText, errorZh)
	return err
}

func (h *Handler) listVisualNodeGenerations(r *http.Request, workspaceID, projectID pgtype.UUID, node visualNodeRow) ([]visualNodeGenerationResponse, error) {
	prefix := h.getIssuePrefix(r.Context(), workspaceID)
	currentAttachmentID := uuidToString(node.ResultAttachmentID)
	rows, err := h.DB.Query(r.Context(), `
		WITH generation_rows AS (
			SELECT g.id, g.task_id, COALESCE(atq.status, '') AS task_status, g.issue_id,
			       COALESCE(i.number, 0) AS number, COALESCE(i.title, '') AS title, COALESCE(i.status, '') AS issue_status,
			       g.attachment_id, g.note, g.note_zh, g.error, g.error_zh,
			       g.created_at, COALESCE(atq.completed_at, g.updated_at) AS completed_at
			FROM project_visual_node_generation g
			LEFT JOIN agent_task_queue atq ON atq.id = g.task_id
			LEFT JOIN issue i ON i.id = g.issue_id
			WHERE g.workspace_id = $1 AND g.project_id = $2 AND g.node_id = $3
		),
		task_rows AS (
			SELECT NULL::uuid AS id, atq.id AS task_id, atq.status AS task_status, atq.issue_id,
			       i.number, i.title, i.status AS issue_status,
			       NULL::uuid AS attachment_id, ''::text AS note, ''::text AS note_zh,
			       COALESCE(atq.error, '')::text AS error, COALESCE(atq.error, '')::text AS error_zh,
			       atq.created_at, atq.completed_at
			FROM agent_task_queue atq
			JOIN issue i ON i.id = atq.issue_id
			LEFT JOIN project_visual_node_generation g ON g.task_id = atq.id
			WHERE i.workspace_id = $1 AND i.project_id = $2
			  AND atq.context->>'type' = $4
			  AND atq.context->>'node_id' = $5
			  AND g.id IS NULL
		)
		SELECT id, task_id, task_status, issue_id, number, title, issue_status,
		       attachment_id, note, note_zh, error, error_zh, created_at, completed_at
		FROM generation_rows
		UNION ALL
		SELECT id, task_id, task_status, issue_id, number, title, issue_status,
		       attachment_id, note, note_zh, error, error_zh, created_at, completed_at
		FROM task_rows
		ORDER BY created_at DESC
	`, workspaceID, projectID, node.ID, visualTaskTypeGenerate, uuidToString(node.ID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []visualNodeGenerationResponse
	for rows.Next() {
		var generationID, taskID, issueID, attachmentID pgtype.UUID
		var taskStatus, issueTitle, issueStatus, note, noteZh, errorText, errorZh string
		var issueNumber int32
		var createdAt, completedAt pgtype.Timestamptz
		if err := rows.Scan(&generationID, &taskID, &taskStatus, &issueID, &issueNumber, &issueTitle, &issueStatus,
			&attachmentID, &note, &noteZh, &errorText, &errorZh, &createdAt, &completedAt); err != nil {
			return nil, err
		}
		item := visualNodeGenerationResponse{
			ID:          uuidToString(generationID),
			TaskID:      uuidToString(taskID),
			TaskStatus:  taskStatus,
			IssueID:     uuidToString(issueID),
			IssueTitle:  issueTitle,
			IssueStatus: issueStatus,
			Note:        note,
			NoteZh:      noteZh,
			Error:       errorText,
			ErrorZh:     errorZh,
			CreatedAt:   timestampToString(createdAt),
			CompletedAt: timestampToString(completedAt),
		}
		if issueID.Valid && issueNumber > 0 {
			item.IssueIdentifier = fmt.Sprintf("%s-%d", prefix, issueNumber)
		}
		if attachmentID.Valid {
			v := uuidToString(attachmentID)
			item.AttachmentID = &v
			item.IsCurrent = v != "" && v == currentAttachmentID
			if att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: attachmentID, WorkspaceID: workspaceID}); err == nil {
				resp := h.attachmentToResponse(att)
				item.Attachment = &resp
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
	ID                 string          `json:"id"`
	ClientID           string          `json:"client_id"`
	Type               string          `json:"type"`
	NodeType           string          `json:"node_type"`
	Title              string          `json:"title"`
	TitleZh            string          `json:"title_zh"`
	Description        string          `json:"description"`
	DescriptionZh      string          `json:"description_zh"`
	VisualBrief        string          `json:"visual_brief"`
	Prompt             string          `json:"prompt"`
	PromptZh           string          `json:"prompt_zh"`
	ImplementationPath string          `json:"implementation_path"`
	ImplementationNote string          `json:"implementation_note"`
	SourceRefs         json.RawMessage `json:"source_refs"`
	SourceSlugs        []string        `json:"source_slugs"`
	ExtractedFacts     []string        `json:"extracted_facts"`
	MustInclude        []string        `json:"must_include"`
	MustAvoid          []string        `json:"must_avoid"`
	AcceptanceCriteria []string        `json:"acceptance_criteria"`
	Confidence         float64         `json:"confidence"`
	PositionX          float64         `json:"position_x"`
	PositionY          float64         `json:"position_y"`
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
	case strings.Contains(lower, "视频") || strings.Contains(lower, "短片") || strings.Contains(lower, "过场") || strings.Contains(lower, "video") || strings.Contains(lower, "cinematic") || strings.Contains(lower, "cutscene"):
		return "video", true
	case strings.Contains(lower, "音频") || strings.Contains(lower, "声音") || strings.Contains(lower, "音效") || strings.Contains(lower, "音乐") || strings.Contains(lower, "环境音") || strings.Contains(lower, "声效") || strings.Contains(lower, "audio") || strings.Contains(lower, "sfx") || strings.Contains(lower, "sound") || strings.Contains(lower, "music") || strings.Contains(lower, "ambience") || strings.Contains(lower, "ambiance"):
		return "audio", true
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
	assetKind := nodeType + " visual asset"
	if nodeType == "audio" {
		assetKind = "audio asset or audio requirement"
	}
	prompt := fmt.Sprintf("Create a production-ready %s for %q. Preserve the project's Wiki context, gameplay purpose, and any constraints from this source excerpt: %s", assetKind, title, truncateRunes(section, 360))
	return visualNodeCandidate{
		NodeType:    nodeType,
		Title:       truncateRunes(title, 120),
		Description: description,
		Prompt:      prompt,
		SourceRefs:  source,
	}
}

func visualBoardExtractIssueDescription(projectTitle string, boardID pgtype.UUID) string {
	title := strings.TrimSpace(projectTitle)
	if title == "" {
		title = "this project"
	}
	return strings.Join([]string{
		fmt.Sprintf("Extract reviewable visual nodes from the Project Wiki for %s.", title),
		"",
		"Expected output:",
		"- Read the Project Wiki excerpts attached to this task.",
		"- Return the strict visual board extraction JSON requested by the runtime prompt.",
		"- Do not create or edit implementation issues from this task.",
		"",
		fmt.Sprintf("Visual board ID: %s", uuidToString(boardID)),
	}, "\n")
}

func visualNodeGenerateIssueDescription(projectTitle string, node visualNodeRow) string {
	title := strings.TrimSpace(projectTitle)
	if title == "" {
		title = "this project"
	}
	assetKind := visualNodeAssetKind(node.Type)
	requiredPipeline := []string{
		"Required pipeline:",
		"- Use the `game-asset-pipeline` skill as the production contract for style docs, rule docs, manifest, bounded generation, deterministic validation, QA sheets, retry notes, and handoff.",
		"- For character, generated-variant, and animation nodes, the selected handoff asset must preserve transparency: PNG/WebP with alpha and no baked scene/background unless the node explicitly asks for an environment background.",
		"- When using GPT Image 2 for transparent game assets, generate on a flat removable chroma-key background and remove the key locally, matching the `game-asset-pipeline` transparency rule.",
		"- For animation work, produce the same class of deliverables inside this visual-node workflow: animation manifest, spritesheet, per-action previews, validation output, QA notes, and final handoff paths.",
		"- Keep the work inside the current Multica visual-node issue and completion flow.",
	}
	expectedOutput := []string{
		"Expected output:",
		"- Create or obtain a usable image or animation asset for this visual node.",
		"- For character/animation deliverables, do not accept a baked background; verify alpha/transparent-pixel hygiene before completion.",
		"- Upload the generated image through `multica visual-node complete`.",
		"- If generation fails, complete the visual node with an explicit error.",
	}
	if node.Type == "audio" {
		requiredPipeline = []string{
			"Required pipeline:",
			"- Treat this as an audio requirement or audio asset task inside the current Multica visual-node issue and completion flow.",
			"- Produce a concise audio spec before generation: sound role, loop/one-shot intent, duration, mix priority, source references, and implementation path if known.",
			"- If a usable audio file is produced or selected, validate that it matches the node prompt and leaves room for dialogue/UI where relevant.",
		}
		expectedOutput = []string{
			"Expected output:",
			"- Create or obtain a usable audio asset, or write a production-ready audio requirement if generation is not available.",
			"- Include loop points or one-shot timing, duration, mix priority, source references, and final handoff path if integrated.",
			"- Upload the generated audio through `multica visual-node complete` when an audio file exists.",
			"- If generation fails, complete the visual node with an explicit error or requirement-only note.",
		}
	}
	parts := []string{
		fmt.Sprintf("Generate a %s for %s.", assetKind, title),
		"",
	}
	parts = append(parts, requiredPipeline...)
	parts = append(parts,
		"Visual node:",
		fmt.Sprintf("- ID: %s", uuidToString(node.ID)),
		fmt.Sprintf("- Type: %s", node.Type),
		fmt.Sprintf("- Title: %s", node.Title),
		"",
		"Prompt:",
		strings.TrimSpace(node.Prompt),
		"",
	)
	parts = append(parts, expectedOutput...)
	return strings.Join(parts, "\n")
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
		return normalizeVisualBoardExtractResult(result), nil
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
	return normalizeVisualBoardExtractResult(wrapped.VisualBoardExtract), nil
}

func normalizeVisualBoardExtractResult(result visualBoardExtractResult) visualBoardExtractResult {
	for i := range result.Nodes {
		node := &result.Nodes[i]
		if strings.TrimSpace(node.Type) == "" {
			node.Type = strings.TrimSpace(node.NodeType)
		}
		node.Type = normalizeExtractedVisualNodeType(*node)
		if strings.TrimSpace(node.Description) == "" {
			node.Description = visualExtractDescriptionFallback(*node)
		}
		if strings.TrimSpace(node.Prompt) == "" && strings.TrimSpace(node.VisualBrief) != "" {
			node.Prompt = strings.TrimSpace(node.VisualBrief)
		}
		node.TitleZh = visualTextOrFallback(node.TitleZh, node.Title)
		node.DescriptionZh = visualTextOrFallback(node.DescriptionZh, node.Description)
		node.PromptZh = visualTextOrFallback(node.PromptZh, node.Prompt)
		if len(node.SourceRefs) == 0 && len(node.SourceSlugs) > 0 {
			refs := make([]map[string]string, 0, len(node.SourceSlugs))
			for _, slug := range node.SourceSlugs {
				slug = strings.TrimSpace(slug)
				if slug == "" {
					continue
				}
				refs = append(refs, map[string]string{"wiki_slug": slug})
			}
			if len(refs) > 0 {
				if data, err := json.Marshal(refs); err == nil {
					node.SourceRefs = data
				}
			}
		}
	}
	return result
}

func visualExtractDescriptionFallback(node visualBoardExtractNode) string {
	parts := make([]string, 0, 5)
	if brief := strings.TrimSpace(node.VisualBrief); brief != "" {
		parts = append(parts, brief)
	}
	if facts := compactStringList(node.ExtractedFacts); len(facts) > 0 {
		parts = append(parts, "Facts: "+strings.Join(facts, "; "))
	}
	if includes := compactStringList(node.MustInclude); len(includes) > 0 {
		parts = append(parts, "Must include: "+strings.Join(includes, "; "))
	}
	if avoids := compactStringList(node.MustAvoid); len(avoids) > 0 {
		parts = append(parts, "Must avoid: "+strings.Join(avoids, "; "))
	}
	if criteria := compactStringList(node.AcceptanceCriteria); len(criteria) > 0 {
		parts = append(parts, "Acceptance: "+strings.Join(criteria, "; "))
	}
	return strings.Join(parts, "\n")
}

func normalizeExtractedVisualNodeType(node visualBoardExtractNode) string {
	nodeType := normalizeVisualNodeType(strings.TrimSpace(node.Type))
	if nodeType == "generated_variant" && isPlayablePetCharacterNode(node) {
		return "character"
	}
	return nodeType
}

func isPlayablePetCharacterNode(node visualBoardExtractNode) bool {
	text := strings.ToLower(strings.Join([]string{
		node.Title,
		node.TitleZh,
		node.Description,
		node.DescriptionZh,
		node.VisualBrief,
		node.Prompt,
		node.PromptZh,
		node.ImplementationPath,
		node.ImplementationNote,
	}, "\n"))
	hasPetSpecies := strings.Contains(text, "pet_cat") ||
		strings.Contains(text, "pet_dog") ||
		strings.Contains(text, "小猫") ||
		strings.Contains(text, "小狗") ||
		strings.Contains(text, " cat") ||
		strings.Contains(text, " dog")
	hasPlayableCue := strings.Contains(text, "主角") ||
		strings.Contains(text, "protagonist") ||
		strings.Contains(text, "playable") ||
		strings.Contains(text, "player") ||
		strings.Contains(text, "玩家") ||
		strings.Contains(text, "pet_cat") ||
		strings.Contains(text, "pet_dog")
	return hasPetSpecies && hasPlayableCue
}

func compactStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func visualTextOrFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func (h *Handler) applyVisualBoardExtractCompleted(ctx context.Context, task db.AgentTaskQueue, output string) {
	visualCtx, ok := h.visualTaskContextFromTask(task)
	if !ok || visualCtx.Type != visualTaskTypeExtract {
		return
	}
	result, err := parseVisualBoardExtractOutput(output)
	if err != nil {
		commentOutput, commentErr := h.latestVisualBoardExtractCommentOutput(ctx, task, visualCtx)
		if commentErr == nil {
			result, err = parseVisualBoardExtractOutput(commentOutput)
		}
	}
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
			assetKind := nodeType + " visual asset"
			if nodeType == "audio" {
				assetKind = "audio asset or audio requirement"
			}
			prompt = fmt.Sprintf("Create a production-ready %s for %q. Use the Project Wiki context and keep the result coherent with adjacent visual nodes.", assetKind, title)
		}
		titleZh := visualTextOrFallback(node.TitleZh, title)
		descriptionZh := visualTextOrFallback(node.DescriptionZh, description)
		promptZh := visualTextOrFallback(node.PromptZh, prompt)
		implementationPath := strings.TrimSpace(node.ImplementationPath)
		implementationNote := strings.TrimSpace(node.ImplementationNote)
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
				board_id, workspace_id, project_id, type, status, title, title_zh, description, description_zh, prompt, prompt_zh,
				position_x, position_y, implementation_path, implementation_note, source_refs
			)
			VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			RETURNING id
		`, boardID, workspaceID, projectID, nodeType, truncateRunes(title, 160), titleZh, description, descriptionZh, prompt, promptZh, x, y, implementationPath, implementationNote, sourceRefs).Scan(&inserted)
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

func (h *Handler) latestVisualBoardExtractCommentOutput(ctx context.Context, task db.AgentTaskQueue, visualCtx visualTaskContext) (string, error) {
	if !task.IssueID.Valid {
		return "", fmt.Errorf("visual extract task has no issue comments")
	}
	workspaceID, err := util.ParseUUID(visualCtx.WorkspaceID)
	if err != nil {
		return "", err
	}
	since := task.StartedAt
	if !since.Valid {
		since = task.CreatedAt
	}
	comments, err := h.Queries.ListCommentsSinceForIssue(ctx, db.ListCommentsSinceForIssueParams{
		IssueID:     task.IssueID,
		WorkspaceID: workspaceID,
		CreatedAt:   since,
		Limit:       50,
	})
	if err != nil {
		return "", err
	}
	var parseErr error
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.AuthorType != "agent" || uuidToString(comment.AuthorID) != uuidToString(task.AgentID) {
			continue
		}
		if _, err := parseVisualBoardExtractOutput(comment.Content); err == nil {
			return comment.Content, nil
		} else {
			parseErr = err
		}
	}
	if parseErr != nil {
		return "", parseErr
	}
	return "", fmt.Errorf("no agent comment contained visual board extraction JSON")
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

func buildVisualPlanPrompt(projectTitle string, nodes []visualNodeRow, gameplayNotes, planMode string) string {
	if normalizeVisualPlanMode(planMode) == "production_asset_integration" {
		return buildProductionAssetIntegrationPlanPrompt(projectTitle, nodes, gameplayNotes)
	}
	return buildPlayablePrototypePlanPrompt(projectTitle, nodes, gameplayNotes)
}

func buildPlayablePrototypePlanPrompt(projectTitle string, nodes []visualNodeRow, gameplayNotes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project visual canvas has been reviewed for %q. Create a playable prototype implementation plan from adopted visual nodes only.\n\n", projectTitle)
	writeAdoptedVisualNodes(&b, nodes)
	writeVisualGameplayNotes(&b, gameplayNotes)
	b.WriteString("\nPlan mode: playable_prototype.\n")
	b.WriteString("- Create a visual_asset_manifest that maps characters, pets, backgrounds, props, clues, map points, UI states, and animation/status needs to placeholder assets.\n")
	b.WriteString("- Use procedural, CSS, SVG, Canvas, or pixel-style placeholder assets for the current implementation slice.\n")
	b.WriteString("- Implement UI and gameplay directly with the current project technology stack; do not wait for final generated art assets.\n")
	b.WriteString("- The delivered experience must be a runnable, verifiable, mostly non-text visual prototype, not a text-card specification.\n")
	b.WriteString("- Acceptance criteria must verify that users can complete one full Lost Pet recovery flow with visual characters, pet, locations, clues, progress, and feedback.\n")
	b.WriteString("\nExclude every draft or rejected visual node. Treat adopted visual nodes as art direction and context for the placeholder-driven prototype.\n")
	return b.String()
}

func buildProductionAssetIntegrationPlanPrompt(projectTitle string, nodes []visualNodeRow, gameplayNotes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Project visual canvas has been reviewed for %q. Create a production asset integration plan from adopted visual nodes only.\n\n", projectTitle)
	writeAdoptedVisualNodes(&b, nodes)
	writeVisualGameplayNotes(&b, gameplayNotes)
	b.WriteString("\nPlan mode: production_asset_integration.\n")
	b.WriteString("- Do not create final asset generation tasks and do not use game-asset-pipeline to produce new images in this plan.\n")
	b.WriteString("- Treat each adopted visual node result_attachment_id and result_attachment as the formal asset input selected from the visual node workflow.\n")
	b.WriteString("- Create an asset replacement map that connects visual board assets to concrete in-game files, components, scenes, UI states, animation/status uses, and replacement points.\n")
	b.WriteString("- Include import/adaptation work for file placement, sizing, transparency/alpha, format validation, animation or state mapping, and regression checks.\n")
	b.WriteString("- If an adopted node lacks a result attachment, mark it as missing an integration-ready asset that must return to visual node generation/selection first; do not plan automatic asset production.\n")
	b.WriteString("- Focus on replacing the current game placeholders with the current visual board results, not rebuilding gameplay or producing new art.\n")
	b.WriteString("\nExclude every draft or rejected visual node.\n")
	return b.String()
}

func writeAdoptedVisualNodes(b *strings.Builder, nodes []visualNodeRow) {
	b.WriteString("Adopted visual nodes:\n")
	for _, node := range nodes {
		fmt.Fprintf(b, "- [%s] %s\n  Description: %s\n  Prompt: %s\n", node.Type, node.Title, node.Description, node.Prompt)
		if node.TitleZh != "" {
			fmt.Fprintf(b, "  Title zh: %s\n", node.TitleZh)
		}
		if node.DescriptionZh != "" {
			fmt.Fprintf(b, "  Description zh: %s\n", node.DescriptionZh)
		}
		if node.PromptZh != "" {
			fmt.Fprintf(b, "  Prompt zh: %s\n", node.PromptZh)
		}
		if node.ImplementationPath != "" {
			fmt.Fprintf(b, "  Current implementation path: %s\n", node.ImplementationPath)
		}
		if node.ImplementationNote != "" {
			fmt.Fprintf(b, "  Implementation note: %s\n", node.ImplementationNote)
		}
		if node.ResultAttachmentID.Valid {
			fmt.Fprintf(b, "  Result attachment id: %s\n", uuidToString(node.ResultAttachmentID))
		}
		if node.ResultAttachment != nil {
			fmt.Fprintf(b, "  Result attachment: %s (%s)\n", node.ResultAttachment.Filename, node.ResultAttachment.ContentType)
		}
	}
}

func writeVisualGameplayNotes(b *strings.Builder, gameplayNotes string) {
	if strings.TrimSpace(gameplayNotes) != "" {
		fmt.Fprintf(b, "\nGameplay notes:\n%s\n", strings.TrimSpace(gameplayNotes))
	}
}

func normalizeVisualPlanMode(value string) string {
	switch strings.TrimSpace(value) {
	case "production_asset_integration":
		return "production_asset_integration"
	default:
		return "playable_prototype"
	}
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
	case "character", "scene", "ui_element", "prop", "reference", "gameplay_note", "generated_variant", "animation", "video", "audio":
		return value
	case "character_concept":
		return "character"
	case "environment_concept", "scene_concept":
		return "scene"
	case "ui_concept", "ui_element_concept":
		return "ui_element"
	case "prop_concept":
		return "prop"
	case "reference_concept":
		return "reference"
	case "gameplay_note_concept", "mechanic_visual_note":
		return "gameplay_note"
	case "video_concept", "cinematic", "movie", "cutscene":
		return "video"
	case "audio_concept", "sound", "sound_effect", "sfx", "music", "ambience", "ambiance":
		return "audio"
	default:
		return "reference"
	}
}

func visualNodeAssetKind(nodeType string) string {
	switch nodeType {
	case "audio":
		return "audio asset"
	default:
		return "visual asset"
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
