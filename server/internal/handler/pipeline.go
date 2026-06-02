package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"go.yaml.in/yaml/v2"
)

type PipelineResponse struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspace_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	IsSystem    bool                   `json:"is_system"`
	SystemKey   *string                `json:"system_key"`
	Editable    bool                   `json:"editable"`
	Deletable   bool                   `json:"deletable"`
	CreatedBy   string                 `json:"created_by"`
	ArchivedAt  *string                `json:"archived_at"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
	Nodes       []PipelineNodeResponse `json:"nodes"`
}

type PipelineNodeResponse struct {
	ID                string   `json:"id"`
	PipelineID        string   `json:"pipeline_id"`
	Key               string   `json:"key"`
	Type              string   `json:"type"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	AgentID           *string  `json:"agent_id"`
	Repo              *string  `json:"repo"`
	Repos             []string `json:"repos"`
	DependsOnNodeKeys []string `json:"depends_on_node_keys"`
	Position          int32    `json:"position"`
	PositionX         int32    `json:"position_x"`
	PositionY         int32    `json:"position_y"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
}

type PipelineRunResponse struct {
	ID            string                    `json:"id"`
	PipelineID    string                    `json:"pipeline_id"`
	WorkspaceID   string                    `json:"workspace_id"`
	ProjectID     *string                   `json:"project_id"`
	ParentIssueID string                    `json:"parent_issue_id"`
	Status        string                    `json:"status"`
	CreatedBy     string                    `json:"created_by"`
	CreatedAt     string                    `json:"created_at"`
	Nodes         []PipelineRunNodeResponse `json:"nodes"`
}

type PipelineRunNodeResponse struct {
	ID             string  `json:"id"`
	PipelineRunID  string  `json:"pipeline_run_id"`
	PipelineNodeID *string `json:"pipeline_node_id"`
	NodeKey        string  `json:"node_key"`
	IssueID        string  `json:"issue_id"`
	CreatedAt      string  `json:"created_at"`
}

type listPipelinesResponse struct {
	Pipelines []PipelineResponse `json:"pipelines"`
	Total     int                `json:"total"`
}

type createPipelineRequest struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Nodes       []upsertPipelineNodeRequest `json:"nodes"`
}

type updatePipelineRequest struct {
	Name        *string                     `json:"name"`
	Description *string                     `json:"description"`
	Nodes       []upsertPipelineNodeRequest `json:"nodes"`
}

type upsertPipelineNodeRequest struct {
	Key               string   `json:"key"`
	Type              string   `json:"type"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	AgentID           *string  `json:"agent_id"`
	Repo              *string  `json:"repo"`
	Repos             []string `json:"repos"`
	DependsOnNodeKeys []string `json:"depends_on_node_keys"`
	PositionX         int32    `json:"position_x"`
	PositionY         int32    `json:"position_y"`
}

type runPipelineRequest struct {
	Title     string  `json:"title"`
	ProjectID *string `json:"project_id"`
}

type duplicatePipelineRequest struct {
	Name *string `json:"name"`
}

type importPipelineYAMLRequest struct {
	Content    string  `json:"content"`
	PipelineID *string `json:"pipeline_id"`
}

type pipelineImportValidationResponse struct {
	Valid    bool                   `json:"valid"`
	Errors   []string               `json:"errors"`
	Pipeline *pipelineImportPreview `json:"pipeline,omitempty"`
}

type pipelineImportPreview struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Nodes       []upsertPipelineNodeRequest `json:"nodes"`
}

type pipelineYAMLDefinition struct {
	Version          int                `yaml:"version"`
	Name             string             `yaml:"name"`
	Description      string             `yaml:"description"`
	DefaultProjectID string             `yaml:"default_project_id"`
	Nodes            []pipelineYAMLNode `yaml:"nodes"`
}

type pipelineYAMLNode struct {
	Key               string               `yaml:"key"`
	Type              string               `yaml:"type"`
	Title             string               `yaml:"title"`
	Description       string               `yaml:"description"`
	Agent             string               `yaml:"agent"`
	AgentID           string               `yaml:"agent_id"`
	Repo              string               `yaml:"repo"`
	Repos             []string             `yaml:"repos"`
	DependsOn         []string             `yaml:"depends_on"`
	DependsOnNodeKeys []string             `yaml:"depends_on_node_keys"`
	Position          pipelineYAMLPosition `yaml:"position"`
}

type pipelineYAMLPosition struct {
	X int32 `yaml:"x"`
	Y int32 `yaml:"y"`
}

type pipelineValidationError struct {
	msg string
}

func (e *pipelineValidationError) Error() string { return e.msg }

func pipelineToResponse(p db.Pipeline, nodes []db.PipelineStage) PipelineResponse {
	nodeResp := make([]PipelineNodeResponse, len(nodes))
	for i, node := range nodes {
		nodeResp[i] = pipelineNodeToResponse(node)
	}
	return PipelineResponse{
		ID:          uuidToString(p.ID),
		WorkspaceID: uuidToString(p.WorkspaceID),
		Name:        p.Name,
		Description: p.Description,
		IsSystem:    p.IsSystem,
		SystemKey:   textToPtr(p.SystemKey),
		Editable:    !p.IsSystem,
		Deletable:   !p.IsSystem,
		CreatedBy:   uuidToString(p.CreatedBy),
		ArchivedAt:  timestampToPtr(p.ArchivedAt),
		CreatedAt:   timestampToString(p.CreatedAt),
		UpdatedAt:   timestampToString(p.UpdatedAt),
		Nodes:       nodeResp,
	}
}

func pipelineNodeToResponse(node db.PipelineStage) PipelineNodeResponse {
	nodeType := node.NodeType
	if nodeType == "" {
		nodeType = "issue"
	}
	repoKeys := normalizeRepoKeys(node.RepoKeys)
	return PipelineNodeResponse{
		ID:                uuidToString(node.ID),
		PipelineID:        uuidToString(node.PipelineID),
		Key:               node.Key,
		Type:              nodeType,
		Title:             node.Title,
		Description:       node.Description,
		AgentID:           uuidToPtr(node.AgentID),
		Repo:              singleRepoKeyPtr(repoKeys),
		Repos:             repoKeys,
		DependsOnNodeKeys: node.DependsOnStageKeys,
		Position:          node.Position,
		PositionX:         node.PositionX,
		PositionY:         node.PositionY,
		CreatedAt:         timestampToString(node.CreatedAt),
		UpdatedAt:         timestampToString(node.UpdatedAt),
	}
}

func pipelineRunToResponse(run db.PipelineRun, stages []db.PipelineRunStage) PipelineRunResponse {
	nodeResp := make([]PipelineRunNodeResponse, len(stages))
	for i, stage := range stages {
		nodeResp[i] = PipelineRunNodeResponse{
			ID:             uuidToString(stage.ID),
			PipelineRunID:  uuidToString(stage.PipelineRunID),
			PipelineNodeID: uuidToPtr(stage.PipelineStageID),
			NodeKey:        stage.StageKey,
			IssueID:        uuidToString(stage.IssueID),
			CreatedAt:      timestampToString(stage.CreatedAt),
		}
	}
	return PipelineRunResponse{
		ID:            uuidToString(run.ID),
		PipelineID:    uuidToString(run.PipelineID),
		WorkspaceID:   uuidToString(run.WorkspaceID),
		ProjectID:     uuidToPtr(run.ProjectID),
		ParentIssueID: uuidToString(run.ParentIssueID),
		Status:        run.Status,
		CreatedBy:     uuidToString(run.CreatedBy),
		CreatedAt:     timestampToString(run.CreatedAt),
		Nodes:         nodeResp,
	}
}

func (h *Handler) ListPipelines(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	if err := h.ensureSystemPipelines(r.Context(), parseUUID(wsID), parseUUID(userID)); err != nil {
		slog.Error("failed to ensure system pipelines", "workspace_id", wsID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to ensure system pipelines")
		return
	}
	pipelines, err := h.Queries.ListPipelines(r.Context(), parseUUID(wsID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pipelines")
		return
	}
	resp := make([]PipelineResponse, 0, len(pipelines))
	for _, p := range pipelines {
		nodes, _ := h.Queries.ListPipelineStages(r.Context(), p.ID)
		resp = append(resp, pipelineToResponse(p, nodes))
	}
	writeJSON(w, http.StatusOK, listPipelinesResponse{Pipelines: resp, Total: len(resp)})
}

func (h *Handler) GetPipeline(w http.ResponseWriter, r *http.Request) {
	pipeline, ok := h.loadPipeline(w, r)
	if !ok {
		return
	}
	nodes, err := h.Queries.ListPipelineStages(r.Context(), pipeline.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pipeline nodes")
		return
	}
	writeJSON(w, http.StatusOK, pipelineToResponse(pipeline, nodes))
}

func (h *Handler) ValidatePipelineYAMLImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	var req importPipelineYAMLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	preview, _, err := h.parsePipelineYAMLImport(r, wsUUID, req.Content)
	if err != nil {
		writeJSON(w, http.StatusOK, pipelineImportValidationResponse{
			Valid:  false,
			Errors: []string{err.Error()},
		})
		return
	}
	writeJSON(w, http.StatusOK, pipelineImportValidationResponse{
		Valid:    true,
		Errors:   []string{},
		Pipeline: &preview,
	})
}

func (h *Handler) ImportPipelineYAML(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	var req importPipelineYAMLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	_, normalized, err := h.parsePipelineYAMLImport(r, wsUUID, req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	var pipeline db.Pipeline
	if req.PipelineID != nil && strings.TrimSpace(*req.PipelineID) != "" {
		pipelineID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*req.PipelineID), "pipeline_id")
		if !ok {
			return
		}
		existing, err := qtx.GetPipelineInWorkspace(r.Context(), db.GetPipelineInWorkspaceParams{
			ID:          pipelineID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "pipeline not found")
			return
		}
		if existing.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, "archived pipelines cannot be imported into")
			return
		}
		if existing.IsSystem {
			writeError(w, http.StatusForbidden, "built-in pipelines cannot be imported into")
			return
		}
		pipeline, err = qtx.UpdatePipeline(r.Context(), db.UpdatePipelineParams{
			ID:               existing.ID,
			Name:             pgtype.Text{String: normalized.name, Valid: true},
			Description:      pgtype.Text{String: normalized.description, Valid: true},
			DefaultProjectID: pgtype.UUID{},
		})
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "pipeline name already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to update pipeline")
			return
		}
	} else {
		pipeline, err = qtx.CreatePipeline(r.Context(), db.CreatePipelineParams{
			WorkspaceID:      wsUUID,
			Name:             normalized.name,
			Description:      normalized.description,
			DefaultProjectID: pgtype.UUID{},
			CreatedBy:        parseUUID(userID),
		})
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "pipeline name already exists")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create pipeline")
			return
		}
	}

	nodes, err := h.replacePipelineDefinition(r, qtx, pipeline.ID, normalized)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit pipeline import")
		return
	}
	status := http.StatusCreated
	if req.PipelineID != nil && strings.TrimSpace(*req.PipelineID) != "" {
		status = http.StatusOK
	}
	writeJSON(w, status, pipelineToResponse(pipeline, nodes))
}

func (h *Handler) CreatePipeline(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	var req createPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	normalized, err := h.normalizePipelineDefinition(r, wsUUID, req.Name, req.Description, req.Nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	pipeline, err := qtx.CreatePipeline(r.Context(), db.CreatePipelineParams{
		WorkspaceID:      wsUUID,
		Name:             normalized.name,
		Description:      normalized.description,
		DefaultProjectID: pgtype.UUID{},
		CreatedBy:        parseUUID(userID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "pipeline name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create pipeline")
		return
	}
	nodes, err := h.replacePipelineDefinition(r, qtx, pipeline.ID, normalized)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit pipeline")
		return
	}
	writeJSON(w, http.StatusCreated, pipelineToResponse(pipeline, nodes))
}

func (h *Handler) UpdatePipeline(w http.ResponseWriter, r *http.Request) {
	pipeline, ok := h.loadPipeline(w, r)
	if !ok {
		return
	}
	if pipeline.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "archived pipelines cannot be updated")
		return
	}
	if pipeline.IsSystem {
		writeError(w, http.StatusForbidden, "built-in pipelines cannot be updated")
		return
	}
	var req updatePipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := pipeline.Name
	if req.Name != nil {
		name = *req.Name
	}
	description := pipeline.Description
	if req.Description != nil {
		description = *req.Description
	}
	normalized, err := h.normalizePipelineDefinition(r, pipeline.WorkspaceID, name, description, req.Nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	updated, err := qtx.UpdatePipeline(r.Context(), db.UpdatePipelineParams{
		ID:               pipeline.ID,
		Name:             pgtype.Text{String: normalized.name, Valid: true},
		Description:      pgtype.Text{String: normalized.description, Valid: true},
		DefaultProjectID: pgtype.UUID{},
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "pipeline name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update pipeline")
		return
	}
	nodes, err := h.replacePipelineDefinition(r, qtx, updated.ID, normalized)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit pipeline")
		return
	}
	writeJSON(w, http.StatusOK, pipelineToResponse(updated, nodes))
}

func (h *Handler) DeletePipeline(w http.ResponseWriter, r *http.Request) {
	pipeline, ok := h.loadPipeline(w, r)
	if !ok {
		return
	}
	if pipeline.IsSystem {
		writeError(w, http.StatusForbidden, "built-in pipelines cannot be archived")
		return
	}
	if _, err := h.Queries.ArchivePipeline(r.Context(), pipeline.ID); err != nil {
		if isNotFound(err) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to archive pipeline")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DuplicatePipeline(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	source, ok := h.loadPipeline(w, r)
	if !ok {
		return
	}
	if source.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "archived pipelines cannot be duplicated")
		return
	}
	var req duplicatePipelineRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	sourceNodes, err := h.Queries.ListPipelineStages(r.Context(), source.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pipeline nodes")
		return
	}
	if len(sourceNodes) == 0 {
		writeError(w, http.StatusBadRequest, "pipeline must have at least one node")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	name := strings.TrimSpace(source.Name + " Copy")
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		name = strings.TrimSpace(*req.Name)
	}
	name, err = h.uniquePipelineName(r.Context(), qtx, source.WorkspaceID, name, pgtype.UUID{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to choose pipeline name")
		return
	}
	created, err := qtx.CreatePipeline(r.Context(), db.CreatePipelineParams{
		WorkspaceID:      source.WorkspaceID,
		Name:             name,
		Description:      source.Description,
		DefaultProjectID: pgtype.UUID{},
		CreatedBy:        parseUUID(userID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "pipeline name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to duplicate pipeline")
		return
	}
	copiedNodes, err := h.copyPipelineStages(r.Context(), qtx, created.ID, sourceNodes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to duplicate pipeline nodes")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit pipeline duplicate")
		return
	}
	writeJSON(w, http.StatusCreated, pipelineToResponse(created, copiedNodes))
}

func (h *Handler) RunPipeline(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	pipeline, ok := h.loadPipeline(w, r)
	if !ok {
		return
	}
	if pipeline.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "archived pipelines cannot be run")
		return
	}
	var req runPipelineRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	projectID, ok := h.parseOptionalProjectIDPtr(w, r, req.ProjectID, pipeline.WorkspaceID)
	if !ok {
		return
	}
	nodes, err := h.Queries.ListPipelineStages(r.Context(), pipeline.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pipeline nodes")
		return
	}
	if len(nodes) == 0 {
		writeError(w, http.StatusBadRequest, "pipeline must have at least one node")
		return
	}
	for _, node := range nodes {
		if !node.AgentID.Valid {
			continue
		}
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          node.AgentID,
			WorkspaceID: pipeline.WorkspaceID,
		})
		if err != nil || agent.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("node %s agent is not active", node.Key))
			return
		}
	}
	repoTargetsByStage, err := h.pipelineRepoTargets(r.Context(), projectID, nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	parentTitle := strings.TrimSpace(req.Title)
	if parentTitle == "" {
		parentTitle = pipeline.Name
	}
	number, err := qtx.IncrementIssueCounter(r.Context(), pipeline.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
		return
	}
	parent, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
		WorkspaceID: pipeline.WorkspaceID,
		Title:       parentTitle,
		Description: strOrNullText(pipeline.Description),
		Status:      "todo",
		Priority:    "none",
		CreatorType: "member",
		CreatorID:   parseUUID(userID),
		Number:      number,
		ProjectID:   projectID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create parent issue")
		return
	}
	run, err := qtx.CreatePipelineRun(r.Context(), db.CreatePipelineRunParams{
		PipelineID:    pipeline.ID,
		WorkspaceID:   pipeline.WorkspaceID,
		ProjectID:     projectID,
		ParentIssueID: parent.ID,
		Status:        "completed",
		CreatedBy:     parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create pipeline run")
		return
	}
	issuesByStageKey := make(map[string]db.Issue, len(nodes))
	var createdChildren []db.Issue
	var runStages []db.PipelineRunStage
	for _, node := range nodes {
		number, err := qtx.IncrementIssueCounter(r.Context(), pipeline.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
			return
		}
		assigneeType := pgtype.Text{}
		assigneeID := pgtype.UUID{}
		if node.AgentID.Valid {
			assigneeType = pgtype.Text{String: "agent", Valid: true}
			assigneeID = node.AgentID
		}
		description := pipelineIssueDescription(node.Description, node.NodeType, repoTargetsByStage[node.Key])
		child, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
			WorkspaceID:   pipeline.WorkspaceID,
			Title:         node.Title,
			Description:   strOrNullText(description),
			Status:        "todo",
			Priority:      "none",
			AssigneeType:  assigneeType,
			AssigneeID:    assigneeID,
			CreatorType:   "member",
			CreatorID:     parseUUID(userID),
			ParentIssueID: parent.ID,
			Number:        number,
			ProjectID:     projectID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create pipeline node issue")
			return
		}
		issuesByStageKey[node.Key] = child
		createdChildren = append(createdChildren, child)
		runStage, err := qtx.CreatePipelineRunStage(r.Context(), db.CreatePipelineRunStageParams{
			PipelineRunID:   run.ID,
			PipelineStageID: node.ID,
			StageKey:        node.Key,
			IssueID:         child.ID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record pipeline run node")
			return
		}
		runStages = append(runStages, runStage)
	}
	for _, node := range nodes {
		child := issuesByStageKey[node.Key]
		for _, depKey := range node.DependsOnStageKeys {
			dep, ok := issuesByStageKey[depKey]
			if !ok {
				continue
			}
			if _, err := qtx.CreateIssueDependency(r.Context(), db.CreateIssueDependencyParams{
				IssueID:          child.ID,
				DependsOnIssueID: dep.ID,
				Type:             "blocked_by",
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create issue dependency")
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit pipeline run")
		return
	}
	for _, child := range createdChildren {
		if h.shouldEnqueueAgentTask(r.Context(), child) {
			h.TaskService.EnqueueTaskForIssue(r.Context(), child)
		}
	}
	writeJSON(w, http.StatusCreated, pipelineRunToResponse(run, runStages))
}

func (h *Handler) loadPipeline(w http.ResponseWriter, r *http.Request) (db.Pipeline, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return db.Pipeline{}, false
	}
	pipeline, err := h.Queries.GetPipelineInWorkspace(r.Context(), db.GetPipelineInWorkspaceParams{
		ID:          id,
		WorkspaceID: parseUUID(middleware.WorkspaceIDFromContext(r.Context())),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "pipeline not found")
		return db.Pipeline{}, false
	}
	return pipeline, true
}

type normalizedPipelineDefinition struct {
	name        string
	description string
	nodes       []normalizedPipelineNode
}

type normalizedPipelineNode struct {
	key               string
	nodeType          string
	title             string
	description       string
	agentID           pgtype.UUID
	repoKeys          []string
	dependsOnNodeKeys []string
	positionX         int32
	positionY         int32
}

func (h *Handler) parsePipelineYAMLImport(r *http.Request, workspaceID pgtype.UUID, rawContent string) (pipelineImportPreview, normalizedPipelineDefinition, error) {
	content := strings.TrimSpace(rawContent)
	if content == "" {
		return pipelineImportPreview{}, normalizedPipelineDefinition{}, &pipelineValidationError{"yaml content is required"}
	}
	var doc pipelineYAMLDefinition
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return pipelineImportPreview{}, normalizedPipelineDefinition{}, &pipelineValidationError{"invalid pipeline yaml: " + err.Error()}
	}
	if doc.Version != 0 && doc.Version != 1 {
		return pipelineImportPreview{}, normalizedPipelineDefinition{}, &pipelineValidationError{"unsupported pipeline yaml version"}
	}
	preview, err := h.pipelineYAMLToPreview(r, workspaceID, doc)
	if err != nil {
		return pipelineImportPreview{}, normalizedPipelineDefinition{}, err
	}
	normalized, err := h.normalizePipelineDefinition(r, workspaceID, preview.Name, preview.Description, preview.Nodes)
	if err != nil {
		return pipelineImportPreview{}, normalizedPipelineDefinition{}, err
	}
	return preview, normalized, nil
}

func (h *Handler) pipelineYAMLToPreview(r *http.Request, workspaceID pgtype.UUID, doc pipelineYAMLDefinition) (pipelineImportPreview, error) {
	if strings.TrimSpace(doc.DefaultProjectID) != "" {
		return pipelineImportPreview{}, &pipelineValidationError{"default_project_id is not supported on pipeline templates; pass project_id when running the pipeline"}
	}

	nodes := make([]upsertPipelineNodeRequest, 0, len(doc.Nodes))
	for i, node := range doc.Nodes {
		key := strings.TrimSpace(node.Key)
		if key == "" {
			key = fmt.Sprintf("node-%d", i+1)
		}
		agentID, err := h.resolvePipelineImportAgent(r, workspaceID, key, node.AgentID, node.Agent)
		if err != nil {
			return pipelineImportPreview{}, err
		}
		deps := node.DependsOn
		if len(deps) == 0 {
			deps = node.DependsOnNodeKeys
		}
		nodes = append(nodes, upsertPipelineNodeRequest{
			Key:               key,
			Type:              strings.TrimSpace(node.Type),
			Title:             strings.TrimSpace(node.Title),
			Description:       strings.TrimSpace(node.Description),
			AgentID:           agentID,
			Repo:              strPtrOrNil(strings.TrimSpace(node.Repo)),
			Repos:             node.Repos,
			DependsOnNodeKeys: deps,
			PositionX:         node.Position.X,
			PositionY:         node.Position.Y,
		})
	}

	return pipelineImportPreview{
		Name:        strings.TrimSpace(doc.Name),
		Description: strings.TrimSpace(doc.Description),
		Nodes:       nodes,
	}, nil
}

func (h *Handler) resolvePipelineImportAgent(r *http.Request, workspaceID pgtype.UUID, nodeKey, rawAgentID, rawAgent string) (*string, error) {
	agentID := strings.TrimSpace(rawAgentID)
	agentRef := strings.TrimSpace(rawAgent)
	if agentID != "" && agentRef != "" {
		return nil, &pipelineValidationError{fmt.Sprintf("node %s cannot set both agent_id and agent", nodeKey)}
	}
	if agentID != "" {
		parsed, err := util.ParseUUID(agentID)
		if err != nil {
			return nil, &pipelineValidationError{fmt.Sprintf("node %s agent_id is invalid", nodeKey)}
		}
		if err := h.ensurePipelineImportAgentAccessible(r, workspaceID, parsed, nodeKey); err != nil {
			return nil, err
		}
		resolved := uuidToString(parsed)
		return &resolved, nil
	}
	if agentRef == "" {
		return nil, nil
	}
	if parsed, err := util.ParseUUID(agentRef); err == nil {
		if err := h.ensurePipelineImportAgentAccessible(r, workspaceID, parsed, nodeKey); err != nil {
			return nil, err
		}
		resolved := uuidToString(parsed)
		return &resolved, nil
	}

	agents, err := h.Queries.ListAgents(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	lowerRef := strings.ToLower(agentRef)
	var nameMatches []db.Agent
	for _, agent := range agents {
		if strings.ToLower(agent.Name) == lowerRef {
			nameMatches = append(nameMatches, agent)
		}
	}
	if len(nameMatches) == 1 {
		if err := h.ensurePipelineImportAgentCanAccess(r, nameMatches[0], nodeKey); err != nil {
			return nil, err
		}
		resolved := uuidToString(nameMatches[0].ID)
		return &resolved, nil
	}
	if len(nameMatches) > 1 {
		return nil, &pipelineValidationError{fmt.Sprintf("node %s agent matches multiple active agents", nodeKey)}
	}

	var prefixMatches []db.Agent
	if len(agentRef) >= 8 {
		for _, agent := range agents {
			if strings.HasPrefix(strings.ToLower(uuidToString(agent.ID)), lowerRef) {
				prefixMatches = append(prefixMatches, agent)
			}
		}
	}
	if len(prefixMatches) == 1 {
		if err := h.ensurePipelineImportAgentCanAccess(r, prefixMatches[0], nodeKey); err != nil {
			return nil, err
		}
		resolved := uuidToString(prefixMatches[0].ID)
		return &resolved, nil
	}
	if len(prefixMatches) > 1 {
		return nil, &pipelineValidationError{fmt.Sprintf("node %s agent matches multiple active agents", nodeKey)}
	}
	return nil, &pipelineValidationError{fmt.Sprintf("node %s agent was not found in this workspace", nodeKey)}
}

func (h *Handler) ensurePipelineImportAgentAccessible(r *http.Request, workspaceID pgtype.UUID, agentID pgtype.UUID, nodeKey string) error {
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: workspaceID,
	})
	if err != nil || agent.ArchivedAt.Valid {
		return &pipelineValidationError{fmt.Sprintf("node %s agent is not active in this workspace", nodeKey)}
	}
	return h.ensurePipelineImportAgentCanAccess(r, agent, nodeKey)
}

func (h *Handler) ensurePipelineImportAgentCanAccess(r *http.Request, agent db.Agent, nodeKey string) error {
	if !h.canAccessPrivateAgent(r.Context(), agent, "member", requestUserID(r), uuidToString(agent.WorkspaceID)) {
		return &pipelineValidationError{fmt.Sprintf("node %s cannot use private agent", nodeKey)}
	}
	return nil
}

func (h *Handler) normalizePipelineDefinition(r *http.Request, workspaceID pgtype.UUID, rawName, rawDescription string, reqNodes []upsertPipelineNodeRequest) (normalizedPipelineDefinition, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return normalizedPipelineDefinition{}, &pipelineValidationError{"name is required"}
	}
	if len(reqNodes) == 0 {
		return normalizedPipelineDefinition{}, &pipelineValidationError{"at least one node is required"}
	}
	nodes := make([]normalizedPipelineNode, 0, len(reqNodes))
	nodeKeys := make(map[string]bool, len(reqNodes))
	for _, req := range reqNodes {
		key := strings.TrimSpace(req.Key)
		if key == "" {
			return normalizedPipelineDefinition{}, &pipelineValidationError{"node key is required"}
		}
		if nodeKeys[key] {
			return normalizedPipelineDefinition{}, &pipelineValidationError{"node keys must be unique"}
		}
		nodeKeys[key] = true
		nodeType := strings.TrimSpace(req.Type)
		if nodeType == "" {
			nodeType = "issue"
		}
		if nodeType != "issue" && nodeType != "manual" && nodeType != "check" && nodeType != "spec_review" && nodeType != "code_review" && nodeType != "merge" && nodeType != "subagent-driven-development" {
			return normalizedPipelineDefinition{}, &pipelineValidationError{"node type is invalid"}
		}
		title := strings.TrimSpace(req.Title)
		if title == "" {
			return normalizedPipelineDefinition{}, &pipelineValidationError{"node title is required"}
		}
		agentID := pgtype.UUID{}
		if req.AgentID != nil && strings.TrimSpace(*req.AgentID) != "" {
			parsed, err := util.ParseUUID(strings.TrimSpace(*req.AgentID))
			if err != nil {
				return normalizedPipelineDefinition{}, &pipelineValidationError{"node agent_id is invalid"}
			}
			agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: parsed, WorkspaceID: workspaceID})
			if err != nil || agent.ArchivedAt.Valid {
				return normalizedPipelineDefinition{}, &pipelineValidationError{"node agent_id does not refer to an active agent of this workspace"}
			}
			if !h.canAccessPrivateAgent(r.Context(), agent, "member", requestUserID(r), uuidToString(workspaceID)) {
				return normalizedPipelineDefinition{}, &pipelineValidationError{"cannot use private agent in pipeline node"}
			}
			agentID = parsed
		}
		repoKeys, err := normalizePipelineNodeRepoKeys(req.Repo, req.Repos)
		if err != nil {
			return normalizedPipelineDefinition{}, err
		}
		deps := make([]string, 0, len(req.DependsOnNodeKeys))
		seenDeps := map[string]bool{}
		for _, rawDep := range req.DependsOnNodeKeys {
			dep := strings.TrimSpace(rawDep)
			if dep == "" {
				continue
			}
			if dep == key {
				return normalizedPipelineDefinition{}, &pipelineValidationError{"node cannot depend on itself"}
			}
			if seenDeps[dep] {
				continue
			}
			seenDeps[dep] = true
			deps = append(deps, dep)
		}
		nodes = append(nodes, normalizedPipelineNode{
			key:               key,
			nodeType:          nodeType,
			title:             title,
			description:       strings.TrimSpace(req.Description),
			agentID:           agentID,
			repoKeys:          repoKeys,
			dependsOnNodeKeys: deps,
			positionX:         req.PositionX,
			positionY:         req.PositionY,
		})
	}
	for _, node := range nodes {
		for _, dep := range node.dependsOnNodeKeys {
			if !nodeKeys[dep] {
				return normalizedPipelineDefinition{}, &pipelineValidationError{"node depends_on_node_keys must reference existing nodes"}
			}
		}
	}
	return normalizedPipelineDefinition{
		name:        name,
		description: strings.TrimSpace(rawDescription),
		nodes:       nodes,
	}, nil
}

func (h *Handler) replacePipelineDefinition(r *http.Request, qtx *db.Queries, pipelineID pgtype.UUID, normalized normalizedPipelineDefinition) ([]db.PipelineStage, error) {
	if err := qtx.DeletePipelineStages(r.Context(), pipelineID); err != nil {
		return nil, err
	}
	if err := qtx.DeletePipelineRoles(r.Context(), pipelineID); err != nil {
		return nil, err
	}
	nodes := make([]db.PipelineStage, 0, len(normalized.nodes))
	for i, node := range normalized.nodes {
		created, err := qtx.CreatePipelineStage(r.Context(), db.CreatePipelineStageParams{
			PipelineID:         pipelineID,
			Key:                node.key,
			Title:              node.title,
			Description:        node.description,
			RoleKey:            "",
			NodeType:           node.nodeType,
			AgentID:            node.agentID,
			DependsOnStageKeys: node.dependsOnNodeKeys,
			Position:           int32(i + 1),
			PositionX:          node.positionX,
			PositionY:          node.positionY,
			RepoKeys:           node.repoKeys,
		})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, created)
	}
	return nodes, nil
}

func (h *Handler) copyPipelineStages(ctx context.Context, qtx *db.Queries, pipelineID pgtype.UUID, sourceNodes []db.PipelineStage) ([]db.PipelineStage, error) {
	nodes := make([]db.PipelineStage, 0, len(sourceNodes))
	for _, node := range sourceNodes {
		created, err := qtx.CreatePipelineStage(ctx, db.CreatePipelineStageParams{
			PipelineID:         pipelineID,
			Key:                node.Key,
			Title:              node.Title,
			Description:        node.Description,
			RoleKey:            node.RoleKey,
			NodeType:           node.NodeType,
			AgentID:            node.AgentID,
			DependsOnStageKeys: node.DependsOnStageKeys,
			Position:           node.Position,
			PositionX:          node.PositionX,
			PositionY:          node.PositionY,
			RepoKeys:           normalizeRepoKeys(node.RepoKeys),
		})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, created)
	}
	return nodes, nil
}

type pipelineRepoTarget struct {
	Key string
	URL string
}

func normalizePipelineNodeRepoKeys(rawRepo *string, rawRepos []string) ([]string, error) {
	repo := ""
	if rawRepo != nil {
		repo = strings.TrimSpace(*rawRepo)
	}
	if repo != "" && len(rawRepos) > 0 {
		for _, raw := range rawRepos {
			if strings.TrimSpace(raw) != "" {
				return nil, &pipelineValidationError{"node cannot set both repo and repos"}
			}
		}
	}
	if repo != "" {
		return []string{repo}, nil
	}
	return normalizeRepoKeys(rawRepos), nil
}

func normalizeRepoKeys(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		key := strings.TrimSpace(item)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func singleRepoKeyPtr(repoKeys []string) *string {
	if len(repoKeys) == 1 {
		return &repoKeys[0]
	}
	return nil
}

func (h *Handler) pipelineRepoTargets(ctx context.Context, projectID pgtype.UUID, nodes []db.PipelineStage) (map[string][]pipelineRepoTarget, error) {
	targets := map[string][]pipelineRepoTarget{}
	needsRepos := false
	for _, node := range nodes {
		if len(normalizeRepoKeys(node.RepoKeys)) > 0 {
			needsRepos = true
			break
		}
	}
	if !needsRepos {
		return targets, nil
	}
	if !projectID.Valid {
		return nil, &pipelineValidationError{"pipeline nodes reference repos but run request has no project_id"}
	}
	resources, err := h.Queries.ListProjectResources(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list project resources")
	}
	reposByKey := map[string]pipelineRepoTarget{}
	for _, resource := range resources {
		if resource.ResourceType != "git_repo" && resource.ResourceType != "github_repo" {
			continue
		}
		key := strings.TrimSpace(resource.Label.String)
		if key == "" {
			continue
		}
		url := projectResourceRepoURL(resource)
		if url == "" {
			continue
		}
		reposByKey[key] = pipelineRepoTarget{Key: key, URL: url}
	}
	for _, node := range nodes {
		repoKeys := normalizeRepoKeys(node.RepoKeys)
		if len(repoKeys) == 0 {
			continue
		}
		for _, key := range repoKeys {
			target, ok := reposByKey[key]
			if !ok {
				return nil, &pipelineValidationError{fmt.Sprintf("node %s references unknown repo %q", node.Key, key)}
			}
			targets[node.Key] = append(targets[node.Key], target)
		}
	}
	return targets, nil
}

func projectResourceRepoURL(resource db.ProjectResource) string {
	var ref struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
		return ""
	}
	return strings.TrimSpace(ref.URL)
}

func pipelineIssueDescription(description, nodeType string, repos []pipelineRepoTarget) string {
	description = strings.TrimSpace(description)
	reviewContract := pipelineReviewGateContract(nodeType)
	if len(repos) == 0 && reviewContract == "" {
		return description
	}
	var b strings.Builder
	if description != "" {
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	if len(repos) > 0 {
		b.WriteString("Target repositories:\n")
		for _, repo := range repos {
			fmt.Fprintf(&b, "- %s: %s\n", repo.Key, repo.URL)
		}
	}
	if reviewContract != "" {
		if len(repos) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(reviewContract)
	}
	return strings.TrimSpace(b.String())
}

func pipelineReviewGateContract(nodeType string) string {
	switch nodeType {
	case "spec_review":
		return `Review gate output contract:
Return a final JSON object with this exact shape:
{
  "review_gate": {
    "status": "pass" | "fail",
    "summary": "Brief spec compliance review summary.",
    "findings": [
      { "severity": "blocker" | "major" | "minor", "title": "Finding title", "details": "Finding details" }
    ],
    "checked_against": ["Spec, issue, plan, or requirement checked"]
  }
}

Use "pass" only when the implementation satisfies the requested spec. Use "fail" when downstream work must stay blocked.
For game, visual prototype, UI, Canvas, or other interactive product work, "pass" is forbidden when the reviewed result is text-only, list/card-driven, emoji-only, or lacks the visible interactive gameplay/visual interaction required by the spec. Treat that as a blocking finding.`
	case "code_review":
		return `Review gate output contract:
Return a final JSON object with this exact shape:
{
  "review_gate": {
    "status": "pass" | "fail",
    "summary": "Brief code quality review summary.",
    "findings": [
      { "severity": "blocker" | "major" | "minor", "title": "Finding title", "details": "Finding details" }
    ],
    "checked_against": ["Diff, tests, architecture, or risk area checked"]
  }
}

Use "pass" only when the code quality review has no blocking findings. Use "fail" when downstream work must stay blocked.
For game, visual prototype, UI, Canvas, or other interactive product work, "pass" is forbidden when the reviewed result is text-only, list/card-driven, emoji-only, or lacks the visible interactive gameplay/visual interaction required by the spec. Treat that as a blocking finding.`
	case "merge":
		return `Merge / integrate output contract:
Record the integration result in the issue comment or task output with these fields:
- Source branch
- Target branch
- PR URL or merge commit
- Test result
- Status: merged | pr_created | failed
- Failure reason and conflict files when status is failed

Use PR-first behavior by default. Do not direct-merge or push protected branches unless the task explicitly authorizes direct integration.`
	default:
		return ""
	}
}

func (h *Handler) parseOptionalProjectIDPtr(w http.ResponseWriter, r *http.Request, raw *string, workspaceID pgtype.UUID) (pgtype.UUID, bool) {
	if raw == nil {
		return pgtype.UUID{}, true
	}
	return h.parseOptionalProjectID(w, r, *raw, workspaceID)
}
