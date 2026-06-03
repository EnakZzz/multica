package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type createWikiPageRequest struct {
	Slug       string          `json:"slug"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	SourceRefs json.RawMessage `json:"source_refs"`
	Status     string          `json:"status"`
	ReviewedAt *string         `json:"reviewed_at"`
}

type updateWikiPageRequest struct {
	Title      *string          `json:"title"`
	Body       *string          `json:"body"`
	SourceRefs *json.RawMessage `json:"source_refs"`
	Status     *string          `json:"status"`
	ReviewedAt *string          `json:"reviewed_at"`
}

type createMemoryItemRequest struct {
	IssueID    *string         `json:"issue_id"`
	TaskID     *string         `json:"task_id"`
	CommentID  *string         `json:"comment_id"`
	Kind       string          `json:"kind"`
	Outcome    string          `json:"outcome"`
	Title      string          `json:"title"`
	Summary    string          `json:"summary"`
	Symptom    string          `json:"symptom"`
	Cause      string          `json:"cause"`
	FixPath    string          `json:"fix_path"`
	Commands   json.RawMessage `json:"commands"`
	RepoRefs   json.RawMessage `json:"repo_refs"`
	Tags       []string        `json:"tags"`
	Confidence *int32          `json:"confidence"`
	ExpiresAt  *string         `json:"expires_at"`
}

type updateMemoryItemRequest struct {
	Kind       *string          `json:"kind"`
	Outcome    *string          `json:"outcome"`
	Title      *string          `json:"title"`
	Summary    *string          `json:"summary"`
	Symptom    *string          `json:"symptom"`
	Cause      *string          `json:"cause"`
	FixPath    *string          `json:"fix_path"`
	Commands   *json.RawMessage `json:"commands"`
	RepoRefs   *json.RawMessage `json:"repo_refs"`
	Tags       []string         `json:"tags"`
	Confidence *int32           `json:"confidence"`
	ExpiresAt  *string          `json:"expires_at"`
}

type projectKnowledgeSearchRequest struct {
	Query string `json:"query"`
	Limit int32  `json:"limit"`
}

type updateRetrievalLogFeedbackRequest struct {
	Feedback     string `json:"feedback"`
	FeedbackNote string `json:"feedback_note"`
	Helpfulness  *int32 `json:"helpfulness"`
}

func (h *Handler) ListProjectWikiPages(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResourceAccess(w, r, chi.URLParam(r, "id"), false)
	if !ok {
		return
	}
	pages, err := h.ProjectKnowledge.ListWikiPages(r.Context(), project.WorkspaceID, project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list wiki pages")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wiki_pages": pages, "total": len(pages)})
}

func (h *Handler) CreateProjectWikiPage(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req createWikiPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Slug = normalizeKnowledgeSlug(req.Slug)
	req.Title = strings.TrimSpace(req.Title)
	if req.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	reviewedAt, ok := parseOptionalTimestamp(w, req.ReviewedAt, "reviewed_at")
	if !ok {
		return
	}
	page, err := h.ProjectKnowledge.CreateWikiPage(r.Context(), service.CreateWikiPageInput{
		WorkspaceID: project.WorkspaceID,
		ProjectID:   project.ID,
		Slug:        req.Slug,
		Title:       req.Title,
		Body:        req.Body,
		SourceRefs:  req.SourceRefs,
		Status:      normalizeWikiStatus(req.Status),
		UpdatedBy:   parseUUID(userID),
		ReviewedAt:  reviewedAt,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "wiki page slug already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create wiki page")
		return
	}
	writeJSON(w, http.StatusCreated, page)
}

func (h *Handler) UpdateProjectWikiPage(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	pageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "pageId"), "wiki page id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req updateWikiPageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		req.Title = &trimmed
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
	}
	status := normalizeOptionalWikiStatus(req.Status)
	reviewedAt, ok := parseOptionalTimestamp(w, req.ReviewedAt, "reviewed_at")
	if !ok {
		return
	}
	page, err := h.ProjectKnowledge.UpdateWikiPage(r.Context(), service.UpdateWikiPageInput{
		ID:         pageID,
		ProjectID:  project.ID,
		Title:      req.Title,
		Body:       req.Body,
		SourceRefs: req.SourceRefs,
		Status:     status,
		UpdatedBy:  parseUUID(userID),
		ReviewedAt: reviewedAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "wiki page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update wiki page")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) DeleteProjectWikiPage(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	pageID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "pageId"), "wiki page id")
	if !ok {
		return
	}
	if err := h.ProjectKnowledge.DeleteWikiPage(r.Context(), project.ID, pageID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete wiki page")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListProjectMemoryItems(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResourceAccess(w, r, chi.URLParam(r, "id"), false)
	if !ok {
		return
	}
	limit := parseLimit(r, 50)
	items, err := h.ProjectKnowledge.ListMemoryItems(r.Context(), project.WorkspaceID, project.ID, int32(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory items")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory_items": items, "total": len(items)})
}

func (h *Handler) CreateProjectMemoryItem(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req createMemoryItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input, ok := h.memoryInputFromRequest(w, project.WorkspaceID, project.ID, req)
	if !ok {
		return
	}
	item, err := h.ProjectKnowledge.CreateMemoryItem(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create memory item")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) UpdateProjectMemoryItem(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	itemID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memoryItemId"), "memory item id")
	if !ok {
		return
	}
	var req updateMemoryItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	expiresAt, ok := parseOptionalTimestamp(w, req.ExpiresAt, "expires_at")
	if !ok {
		return
	}
	item, err := h.ProjectKnowledge.UpdateMemoryItem(r.Context(), service.UpdateMemoryItemInput{
		ID:         itemID,
		ProjectID:  project.ID,
		Kind:       trimStringPtr(req.Kind),
		Outcome:    trimStringPtr(req.Outcome),
		Title:      trimStringPtr(req.Title),
		Summary:    req.Summary,
		Symptom:    req.Symptom,
		Cause:      req.Cause,
		FixPath:    req.FixPath,
		Commands:   req.Commands,
		RepoRefs:   req.RepoRefs,
		Tags:       req.Tags,
		TagsSet:    req.Tags != nil,
		Confidence: req.Confidence,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "memory item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update memory item")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) DeleteProjectMemoryItem(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	itemID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "memoryItemId"), "memory item id")
	if !ok {
		return
	}
	if err := h.ProjectKnowledge.DeleteMemoryItem(r.Context(), project.ID, itemID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete memory item")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) SearchProjectKnowledge(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResourceAccess(w, r, chi.URLParam(r, "id"), false)
	if !ok {
		return
	}
	var req projectKnowledgeSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	resultSet, err := h.ProjectKnowledge.SearchWithMode(r.Context(), project.WorkspaceID, project.ID, req.Query, req.Limit)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotConfigured) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "project knowledge embeddings are not configured", "results": []any{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to search project knowledge")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "results": resultSet.Results, "total": len(resultSet.Results), "search_mode": resultSet.SearchMode})
}

func (h *Handler) BackfillProjectKnowledgeEmbeddings(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	targetType := strings.TrimSpace(r.URL.Query().Get("target_type"))
	result, err := h.ProjectKnowledge.BackfillKnowledgeEmbeddings(r.Context(), project.WorkspaceID, project.ID, targetType)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotConfigured) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "queued": 0, "skipped": 0, "failed": 0, "error": "project knowledge embeddings are not configured"})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to backfill project knowledge embeddings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "queued": result.Queued, "skipped": result.Skipped, "failed": result.Failed})
}

func (h *Handler) ListProjectKnowledgeRetrievalLogs(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResourceAccess(w, r, chi.URLParam(r, "id"), false)
	if !ok {
		return
	}
	logs, err := h.ProjectKnowledge.ListRetrievalLogs(r.Context(), project.WorkspaceID, project.ID, int32(parseLimit(r, 50)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list retrieval logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retrieval_logs": logs, "total": len(logs)})
}

func (h *Handler) GetIssueKnowledgeTrace(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: issueUUID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	logs, err := h.ProjectKnowledge.ListRetrievalLogsForIssue(r.Context(), wsUUID, issueUUID, int32(parseLimit(r, 20)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load knowledge trace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retrieval_logs": logs, "total": len(logs)})
}

func (h *Handler) GetTaskKnowledgeTrace(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || !task.IssueID.Valid {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: task.IssueID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	logs, err := h.ProjectKnowledge.ListRetrievalLogsForTask(r.Context(), wsUUID, taskID, int32(parseLimit(r, 20)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load knowledge trace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retrieval_logs": logs, "total": len(logs)})
}

func (h *Handler) UpdateProjectKnowledgeRetrievalLogFeedback(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	logID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "logId"), "retrieval log id")
	if !ok {
		return
	}
	var req updateRetrievalLogFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Feedback = strings.TrimSpace(req.Feedback)
	if req.Feedback != "" && req.Feedback != "useful" && req.Feedback != "noisy" && req.Feedback != "wrong" && req.Feedback != "stale" {
		writeError(w, http.StatusBadRequest, "invalid feedback")
		return
	}
	if req.Helpfulness != nil && (*req.Helpfulness < 0 || *req.Helpfulness > 100) {
		writeError(w, http.StatusBadRequest, "helpfulness must be between 0 and 100")
		return
	}
	log, err := h.ProjectKnowledge.UpdateRetrievalFeedback(r.Context(), project.WorkspaceID, project.ID, logID, req.Feedback, req.FeedbackNote, req.Helpfulness)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "retrieval log not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update retrieval feedback")
		return
	}
	writeJSON(w, http.StatusOK, log)
}

func (h *Handler) GetIssueRelatedMemory(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: issueUUID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	h.writeRelatedMemory(w, r, issue, int32(parseLimit(r, 5)))
}

func (h *Handler) GetTaskRelatedMemory(w http.ResponseWriter, r *http.Request) {
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "task id")
	if !ok {
		return
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskID)
	if err != nil || !task.IssueID.Valid {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: task.IssueID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	h.writeRelatedMemory(w, r, issue, int32(parseLimit(r, 5)))
}

func (h *Handler) writeRelatedMemory(w http.ResponseWriter, r *http.Request, issue db.Issue, limit int32) {
	results, err := h.ProjectKnowledge.RelevantMemoryForIssue(r.Context(), issue, limit)
	if err != nil {
		if errors.Is(err, service.ErrEmbeddingNotConfigured) {
			writeJSON(w, http.StatusOK, map[string]any{"configured": false, "related_memory": []any{}, "error": "project knowledge embeddings are not configured"})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load related memory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "related_memory": results, "total": len(results)})
}

func (h *Handler) applyRelevantKnowledgeToTaskResponse(ctx context.Context, resp *AgentTaskResponse, issue db.Issue, taskID pgtype.UUID) {
	if h.ProjectKnowledge == nil || !issue.ProjectID.Valid {
		return
	}
	queryParts := []string{issue.Title}
	queryContext := map[string]any{
		"issue_id":    uuidToString(issue.ID),
		"issue_title": issue.Title,
	}
	if issue.Description.Valid {
		queryParts = append(queryParts, issue.Description.String)
		queryContext["issue_description"] = truncateForPromptTrace(issue.Description.String, 1200)
	}
	if resp.ProjectTitle != "" {
		queryParts = append(queryParts, "Project: "+resp.ProjectTitle)
		queryContext["project_title"] = resp.ProjectTitle
	}
	if resp.PlanItemNodeType != "" {
		queryParts = append(queryParts, "Node type: "+resp.PlanItemNodeType)
		queryContext["node_type"] = resp.PlanItemNodeType
	}
	if resp.ReviewTargetBranchName != "" {
		queryParts = append(queryParts, "Review target branch: "+resp.ReviewTargetBranchName)
		queryContext["review_target_branch"] = resp.ReviewTargetBranchName
	}
	query := strings.Join(queryParts, "\n\n")
	resultSet, err := h.ProjectKnowledge.CanonicalWikiContextWithMode(ctx, issue.WorkspaceID, issue.ProjectID, query, 5)
	if err != nil {
		slog.Debug("project knowledge retrieval skipped", "issue_id", uuidToString(issue.ID), "project_id", uuidToString(issue.ProjectID), "error", err)
		h.ProjectKnowledge.LogRetrieval(ctx, service.RetrievalLogInput{
			WorkspaceID:  issue.WorkspaceID,
			ProjectID:    issue.ProjectID,
			IssueID:      issue.ID,
			TaskID:       taskID,
			QueryText:    query,
			SearchMode:   "none",
			QueryContext: queryContext,
			Status:       "error",
			Error:        err.Error(),
		})
		return
	}
	results := resultSet.Results
	relevant := h.ProjectKnowledge.ToRelevant(results, 5)
	resp.RelevantKnowledge = relevant
	injected := h.ProjectKnowledge.InjectedText(results, 5, 2500)
	status := "injected"
	if len(relevant) == 0 {
		status = "no_results"
	}
	h.ProjectKnowledge.LogRetrieval(ctx, service.RetrievalLogInput{
		WorkspaceID:       issue.WorkspaceID,
		ProjectID:         issue.ProjectID,
		IssueID:           issue.ID,
		TaskID:            taskID,
		QueryText:         query,
		SearchMode:        resultSet.SearchMode,
		QueryContext:      queryContext,
		Candidates:        results,
		SelectedItems:     relevant,
		InjectedText:      injected,
		TokenBudget:       2500,
		InjectedItemCount: int32(len(relevant)),
		Status:            status,
	})
}

func (h *Handler) applyRelevantKnowledgeToIssuePlanTaskResponse(ctx context.Context, resp *AgentTaskResponse, ip service.IssuePlanContext, taskID pgtype.UUID) {
	if h.ProjectKnowledge == nil || strings.TrimSpace(ip.ProjectID) == "" {
		return
	}
	workspaceID, err := util.ParseUUID(ip.WorkspaceID)
	if err != nil {
		return
	}
	projectID, err := util.ParseUUID(ip.ProjectID)
	if err != nil {
		return
	}

	queryParts := []string{
		strings.TrimSpace(ip.Prompt),
		strings.TrimSpace(resp.ProjectTitle),
		"Planning context needed: repository, startup command, target runtime, technology stack, engineering constraints, wiki source refs.",
	}
	if hasPlanSpecContext(ip.Spec) {
		queryParts = append(queryParts,
			ip.Spec.Summary,
			ip.Spec.Goal,
			ip.Spec.Approach,
			strings.Join(ip.Spec.DesignDecisions, "\n"),
		)
	}
	query := strings.Join(nonEmptyStrings(queryParts), "\n\n")
	queryContext := map[string]any{
		"plan_id": ip.PlanID,
		"phase":   ip.Phase,
		"prompt":  ip.Prompt,
	}
	if resp.ProjectTitle != "" {
		queryContext["project_title"] = resp.ProjectTitle
	}

	resultSet, err := h.ProjectKnowledge.CanonicalWikiContextWithMode(ctx, workspaceID, projectID, query, 8)
	if err != nil {
		slog.Debug("project knowledge retrieval skipped for issue plan", "plan_id", ip.PlanID, "project_id", ip.ProjectID, "error", err)
		h.ProjectKnowledge.LogRetrieval(ctx, service.RetrievalLogInput{
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			TaskID:       taskID,
			QueryText:    query,
			SearchMode:   "none",
			QueryContext: queryContext,
			Status:       "error",
			Error:        err.Error(),
		})
		return
	}
	results := resultSet.Results
	relevant := h.ProjectKnowledge.ToRelevant(results, 8)
	resp.RelevantKnowledge = relevant
	injected := h.ProjectKnowledge.InjectedText(results, 8, 3200)
	status := "injected"
	if len(relevant) == 0 {
		status = "no_results"
	}
	h.ProjectKnowledge.LogRetrieval(ctx, service.RetrievalLogInput{
		WorkspaceID:       workspaceID,
		ProjectID:         projectID,
		TaskID:            taskID,
		QueryText:         query,
		SearchMode:        resultSet.SearchMode,
		QueryContext:      queryContext,
		Candidates:        results,
		SelectedItems:     relevant,
		InjectedText:      injected,
		TokenBudget:       3200,
		InjectedItemCount: int32(len(relevant)),
		Status:            status,
	})
}

func hasPlanSpecContext(spec service.PlanSpec) bool {
	return strings.TrimSpace(spec.Summary) != "" ||
		strings.TrimSpace(spec.Goal) != "" ||
		len(spec.Approach) > 0 ||
		len(spec.DesignDecisions) > 0
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateForPromptTrace(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func (h *Handler) memoryInputFromRequest(w http.ResponseWriter, workspaceID, projectID pgtype.UUID, req createMemoryItemRequest) (service.CreateMemoryItemInput, bool) {
	req.Kind = strings.TrimSpace(req.Kind)
	req.Title = strings.TrimSpace(req.Title)
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return service.CreateMemoryItemInput{}, false
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return service.CreateMemoryItemInput{}, false
	}
	expiresAt, ok := parseOptionalTimestamp(w, req.ExpiresAt, "expires_at")
	if !ok {
		return service.CreateMemoryItemInput{}, false
	}
	confidence := int32(60)
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	if confidence < 0 || confidence > 100 {
		writeError(w, http.StatusBadRequest, "confidence must be between 0 and 100")
		return service.CreateMemoryItemInput{}, false
	}
	issueID, ok := parseOptionalUUID(w, req.IssueID, "issue_id")
	if !ok {
		return service.CreateMemoryItemInput{}, false
	}
	taskID, ok := parseOptionalUUID(w, req.TaskID, "task_id")
	if !ok {
		return service.CreateMemoryItemInput{}, false
	}
	commentID, ok := parseOptionalUUID(w, req.CommentID, "comment_id")
	if !ok {
		return service.CreateMemoryItemInput{}, false
	}
	return service.CreateMemoryItemInput{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		IssueID:     issueID,
		TaskID:      taskID,
		CommentID:   commentID,
		Kind:        req.Kind,
		Outcome:     strings.TrimSpace(req.Outcome),
		Title:       req.Title,
		Summary:     req.Summary,
		Symptom:     req.Symptom,
		Cause:       req.Cause,
		FixPath:     req.FixPath,
		Commands:    req.Commands,
		RepoRefs:    req.RepoRefs,
		Tags:        normalizeTags(req.Tags),
		Confidence:  confidence,
		ExpiresAt:   expiresAt,
	}, true
}

func normalizeKnowledgeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var segments []string
	var segment strings.Builder
	lastDash := false
	flushSegment := func() {
		value := strings.Trim(segment.String(), "-")
		if value != "" {
			segments = append(segments, value)
		}
		segment.Reset()
		lastDash = false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			segment.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '/' {
			flushSegment()
			continue
		}
		if !lastDash {
			segment.WriteByte('-')
			lastDash = true
		}
	}
	flushSegment()
	return strings.Join(segments, "/")
}

func normalizeWikiStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "reviewed", "archived":
		return strings.TrimSpace(status)
	default:
		return "draft"
	}
}

func normalizeOptionalWikiStatus(status *string) *string {
	if status == nil {
		return nil
	}
	normalized := normalizeWikiStatus(*status)
	return &normalized
}

func parseOptionalTimestamp(w http.ResponseWriter, value *string, fieldName string) (pgtype.Timestamptz, bool) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return pgtype.Timestamptz{}, true
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+fieldName)
		return pgtype.Timestamptz{}, false
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, true
}

func parseOptionalUUID(w http.ResponseWriter, value *string, fieldName string) (pgtype.UUID, bool) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, strings.TrimSpace(*value), fieldName)
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func parseLimit(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 100 {
		return 100
	}
	return n
}
