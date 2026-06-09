package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	reviewTypeSkill    = "skill_review"
	reviewTypeIssue    = "issue_change_review"
	reviewTypePlan     = "plan_review"
	reviewTypeArtifact = "artifact_review"
)

type ReviewItemResponse struct {
	ID               string   `json:"id"`
	WorkspaceID      string   `json:"workspace_id"`
	Type             string   `json:"type"`
	Status           string   `json:"status"`
	RiskLevel        string   `json:"risk_level"`
	Title            string   `json:"title"`
	Summary          string   `json:"summary"`
	SourceActorType  *string  `json:"source_actor_type"`
	SourceActorID    *string  `json:"source_actor_id"`
	SourceObjectType string   `json:"source_object_type"`
	SourceObjectID   *string  `json:"source_object_id"`
	TargetObjectType string   `json:"target_object_type"`
	TargetObjectID   *string  `json:"target_object_id"`
	Payload          any      `json:"payload"`
	Diff             string   `json:"diff"`
	AvailableActions []string `json:"available_actions"`
	ReviewerID       *string  `json:"reviewer_id"`
	ReviewNote       string   `json:"review_note"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ReviewedAt       *string  `json:"reviewed_at"`
}

type reviewItemActionRequest struct {
	Action string `json:"action"`
	Note   string `json:"note"`
}

type reviewIssuePatchPayload struct {
	IssuePatch json.RawMessage `json:"issue_patch"`
}

func reviewItemToResponse(item db.ReviewItem) ReviewItemResponse {
	return ReviewItemResponse{
		ID:               uuidToString(item.ID),
		WorkspaceID:      uuidToString(item.WorkspaceID),
		Type:             item.Type,
		Status:           item.Status,
		RiskLevel:        item.RiskLevel,
		Title:            item.Title,
		Summary:          item.Summary,
		SourceActorType:  textToPtr(item.SourceActorType),
		SourceActorID:    uuidToPtr(item.SourceActorID),
		SourceObjectType: item.SourceObjectType,
		SourceObjectID:   uuidToPtr(item.SourceObjectID),
		TargetObjectType: item.TargetObjectType,
		TargetObjectID:   uuidToPtr(item.TargetObjectID),
		Payload:          decodeJSONWithDefault(item.Payload, map[string]any{}),
		Diff:             item.Diff,
		AvailableActions: item.AvailableActions,
		ReviewerID:       uuidToPtr(item.ReviewerID),
		ReviewNote:       item.ReviewNote,
		CreatedAt:        timestampToString(item.CreatedAt),
		UpdatedAt:        timestampToString(item.UpdatedAt),
		ReviewedAt:       timestampToPtr(item.ReviewedAt),
	}
}

func (h *Handler) ListReviewItems(w http.ResponseWriter, r *http.Request) {
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	h.syncDecisionDeskReviewItems(r.Context(), wsUUID)

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	var statusText pgtype.Text
	if status != "" {
		statusText = pgtype.Text{String: status, Valid: true}
	}
	itemType := strings.TrimSpace(r.URL.Query().Get("type"))
	var typeText pgtype.Text
	if itemType != "" {
		typeText = pgtype.Text{String: itemType, Valid: true}
	}
	limit := int32(200)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = int32(n)
		}
	}
	items, err := h.Queries.ListReviewItemsByWorkspace(r.Context(), db.ListReviewItemsByWorkspaceParams{
		WorkspaceID: wsUUID,
		Limit:       limit,
		Status:      statusText,
		Type:        typeText,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list review items")
		return
	}
	resp := make([]ReviewItemResponse, len(items))
	for i, item := range items {
		resp[i] = reviewItemToResponse(item)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ActOnReviewItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "review item id")
	if !ok {
		return
	}
	item, err := h.Queries.GetReviewItemInWorkspace(r.Context(), db.GetReviewItemInWorkspaceParams{
		ID:          id,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "review item not found")
		return
	}
	if item.Status != "pending" {
		writeError(w, http.StatusConflict, "review item is not pending")
		return
	}
	var req reviewItemActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return
	}

	if action == "open_source" {
		writeJSON(w, http.StatusOK, reviewItemToResponse(item))
		return
	}
	if action == "assign" || action == "promote" {
		writeError(w, http.StatusBadRequest, "action is not available for this review item")
		return
	}

	nextStatus := reviewStatusForAction(action)
	if nextStatus == "" {
		writeError(w, http.StatusBadRequest, "unsupported review action")
		return
	}

	if action == "approve" {
		if ok := h.applyReviewItemApproval(w, r, item, parseUUID(userID)); !ok {
			return
		}
	}
	if action == "rerun" {
		if ok := h.rerunReviewItem(w, r, item, parseUUID(userID)); !ok {
			return
		}
	}

	reviewed, err := h.Queries.MarkReviewItemReviewed(r.Context(), db.MarkReviewItemReviewedParams{
		ID:          item.ID,
		WorkspaceID: item.WorkspaceID,
		Status:      nextStatus,
		ReviewerID:  parseUUID(userID),
		ReviewNote:  sanitizeNullBytes(strings.TrimSpace(req.Note)),
	})
	if err != nil {
		writeError(w, http.StatusConflict, "review item is not pending")
		return
	}
	writeJSON(w, http.StatusOK, reviewItemToResponse(reviewed))
}

func reviewStatusForAction(action string) string {
	switch action {
	case "approve":
		return "approved"
	case "reject":
		return "rejected"
	case "request_changes":
		return "changes_requested"
	case "rerun":
		return "superseded"
	default:
		return ""
	}
}

func (h *Handler) rerunReviewItem(w http.ResponseWriter, r *http.Request, item db.ReviewItem, userID pgtype.UUID) bool {
	if item.Type != reviewTypePlan || item.TargetObjectType != "plan" || !item.TargetObjectID.Valid {
		writeError(w, http.StatusBadRequest, "rerun is not available for this review item")
		return false
	}
	plan, err := h.Queries.GetPlanInWorkspace(r.Context(), db.GetPlanInWorkspaceParams{
		ID:          item.TargetObjectID,
		WorkspaceID: item.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "plan not found")
		return false
	}
	if plan.Status == "committed" {
		writeError(w, http.StatusBadRequest, "committed plans cannot be rerun")
		return false
	}
	if status, msg := h.validatePlanAgent(r, plan.PlannerAgentID, plan.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return false
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), plan.WorkspaceID, userID, plan.ID, plan.PlannerAgentID, plan.Prompt, plan.ProjectID, service.IssuePlanPhaseSpec, service.PlanSpec{})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := h.Queries.MarkPlanPlanning(r.Context(), db.MarkPlanPlanningParams{ID: plan.ID, TaskID: task.ID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rerun plan")
		return false
	}
	return true
}

func (h *Handler) applyReviewItemApproval(w http.ResponseWriter, r *http.Request, item db.ReviewItem, userID pgtype.UUID) bool {
	switch item.Type {
	case reviewTypeSkill:
		if item.SourceObjectType != "skill_proposal" || !item.SourceObjectID.Valid {
			return true
		}
		return h.applySkillProposalForReview(w, r, item, userID)
	case reviewTypePlan:
		if item.TargetObjectType != "plan" || !item.TargetObjectID.Valid {
			return true
		}
		return h.approvePlanForReview(w, r, item, userID)
	case reviewTypeIssue:
		return h.applyIssueChangeReview(w, r, item, userID)
	case reviewTypeArtifact:
		return true
	default:
		return true
	}
}

func (h *Handler) applySkillProposalForReview(w http.ResponseWriter, r *http.Request, item db.ReviewItem, userID pgtype.UUID) bool {
	proposal, err := h.Queries.GetSkillProposalInWorkspace(r.Context(), db.GetSkillProposalInWorkspaceParams{
		ID:          item.SourceObjectID,
		WorkspaceID: item.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "skill proposal not found")
		return false
	}
	if proposal.Status != "pending" {
		return true
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return false
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	appliedSkill, err := h.applySkillProposalWithQueries(r.Context(), qtx, proposal, userID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return false
	}
	if _, err := qtx.MarkSkillProposalApplied(r.Context(), db.MarkSkillProposalAppliedParams{
		ID:             proposal.ID,
		ReviewedBy:     userID,
		AppliedSkillID: appliedSkill.ID,
	}); err != nil {
		writeError(w, http.StatusConflict, "skill proposal is not pending")
		return false
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return false
	}
	actorType, actorID := h.resolveActor(r, uuidToString(userID), uuidToString(item.WorkspaceID))
	h.publish(protocol.EventSkillUpdated, uuidToString(item.WorkspaceID), actorType, actorID, map[string]any{"skill": skillToResponse(appliedSkill)})
	return true
}

func (h *Handler) applySkillProposalWithQueries(ctx context.Context, qtx *db.Queries, proposal db.SkillProposal, userID pgtype.UUID) (db.Skill, error) {
	workspaceUUID := proposal.WorkspaceID
	switch proposal.Operation {
	case "insert":
		appliedSkill, err := qtx.CreateSkill(ctx, db.CreateSkillParams{
			WorkspaceID: workspaceUUID,
			Name:        sanitizeNullBytes(proposal.ProposedName),
			Description: sanitizeNullBytes(proposal.ProposedDescription),
			Content:     sanitizeNullBytes(proposal.ProposedContent),
			Config:      []byte(`{"curation":{"source":"review_item"}}`),
			CreatedBy:   userID,
		})
		if err == nil && proposalHasFiles(proposal.ProposedFiles) {
			err = upsertProposalFiles(ctx, qtx, appliedSkill.ID, proposal.ProposedFiles)
		}
		return appliedSkill, err
	case "update":
		if !proposal.TargetSkillID.Valid {
			return db.Skill{}, errBadRequest("target_skill_id is required")
		}
		current, err := qtx.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
			ID:          proposal.TargetSkillID,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			return db.Skill{}, errBadRequest("target skill not found")
		}
		if proposal.BaseContentHash != "" && skillContentHash(current.Content) != proposal.BaseContentHash {
			return db.Skill{}, errBadRequest("target skill changed since proposal was created")
		}
		appliedSkill, err := qtx.UpdateSkill(ctx, db.UpdateSkillParams{
			ID:          proposal.TargetSkillID,
			Name:        pgtype.Text{String: sanitizeNullBytes(proposal.ProposedName), Valid: proposal.ProposedName != ""},
			Description: pgtype.Text{String: sanitizeNullBytes(proposal.ProposedDescription), Valid: true},
			Content:     pgtype.Text{String: sanitizeNullBytes(proposal.ProposedContent), Valid: true},
		})
		if err == nil && proposalHasFiles(proposal.ProposedFiles) {
			err = upsertProposalFiles(ctx, qtx, appliedSkill.ID, proposal.ProposedFiles)
		}
		return appliedSkill, err
	case "delete":
		if !proposal.TargetSkillID.Valid {
			return db.Skill{}, errBadRequest("target_skill_id is required")
		}
		current, err := qtx.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
			ID:          proposal.TargetSkillID,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			return db.Skill{}, errBadRequest("target skill not found")
		}
		config := decodeSkillConfig(current.Config)
		configMap, _ := config.(map[string]any)
		if configMap == nil {
			configMap = map[string]any{}
		}
		configMap["deprecated"] = true
		configMap["deprecated_by_proposal_id"] = uuidToString(proposal.ID)
		configBytes, _ := json.Marshal(configMap)
		return qtx.UpdateSkill(ctx, db.UpdateSkillParams{
			ID:     proposal.TargetSkillID,
			Config: configBytes,
		})
	default:
		return db.Skill{}, errBadRequest("unsupported proposal operation")
	}
}

func (h *Handler) approvePlanForReview(w http.ResponseWriter, r *http.Request, item db.ReviewItem, userID pgtype.UUID) bool {
	plan, err := h.Queries.GetPlanInWorkspace(r.Context(), db.GetPlanInWorkspaceParams{
		ID:          item.TargetObjectID,
		WorkspaceID: item.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "plan not found")
		return false
	}
	if plan.Status != "spec_review" {
		return true
	}
	if status, msg := h.validatePlanAgent(r, plan.PlannerAgentID, plan.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return false
	}
	spec := planSpecFromJSON(plan.Spec)
	if strings.TrimSpace(spec.Summary) == "" || strings.TrimSpace(spec.Goal) == "" {
		writeError(w, http.StatusBadRequest, "plan spec is incomplete")
		return false
	}
	specJSON, err := service.MarshalPlanSpec(spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid spec")
		return false
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), plan.WorkspaceID, userID, plan.ID, plan.PlannerAgentID, plan.Prompt, plan.ProjectID, service.IssuePlanPhaseItems, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	if _, err := h.Queries.ApprovePlanSpec(r.Context(), db.ApprovePlanSpecParams{
		ID:             plan.ID,
		TaskID:         task.ID,
		Spec:           specJSON,
		SpecApprovedBy: userID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve plan spec")
		return false
	}
	return true
}

func (h *Handler) applyIssueChangeReview(w http.ResponseWriter, r *http.Request, item db.ReviewItem, userID pgtype.UUID) bool {
	if item.TargetObjectType != "issue" || !item.TargetObjectID.Valid {
		return true
	}
	var payload reviewIssuePatchPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil || len(payload.IssuePatch) == 0 {
		return true
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          item.TargetObjectID,
		WorkspaceID: item.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return false
	}
	updated, ok := h.applyIssuePatchJSON(w, r, issue, payload.IssuePatch)
	if !ok {
		return false
	}
	prefix := h.getIssuePrefix(r.Context(), updated.WorkspaceID)
	resp := issueToResponse(updated, prefix)
	h.publish(protocol.EventIssueUpdated, uuidToString(updated.WorkspaceID), "member", uuidToString(userID), map[string]any{
		"issue":               resp,
		"assignee_changed":    issue.AssigneeType.String != updated.AssigneeType.String || uuidToString(issue.AssigneeID) != uuidToString(updated.AssigneeID),
		"status_changed":      issue.Status != updated.Status,
		"priority_changed":    issue.Priority != updated.Priority,
		"description_changed": issue.Description.String != updated.Description.String,
		"title_changed":       issue.Title != updated.Title,
		"prev_title":          issue.Title,
		"prev_assignee_type":  textToPtr(issue.AssigneeType),
		"prev_assignee_id":    uuidToPtr(issue.AssigneeID),
		"prev_status":         issue.Status,
		"prev_priority":       issue.Priority,
		"prev_description":    textToPtr(issue.Description),
		"creator_type":        issue.CreatorType,
		"creator_id":          uuidToString(issue.CreatorID),
	})
	return true
}

func (h *Handler) applyIssuePatchJSON(w http.ResponseWriter, r *http.Request, prevIssue db.Issue, patch json.RawMessage) (db.Issue, bool) {
	var req UpdateIssueRequest
	if err := json.Unmarshal(patch, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid issue patch")
		return db.Issue{}, false
	}
	var rawFields map[string]json.RawMessage
	_ = json.Unmarshal(patch, &rawFields)
	params := db.UpdateIssueParams{
		ID:            prevIssue.ID,
		AssigneeType:  prevIssue.AssigneeType,
		AssigneeID:    prevIssue.AssigneeID,
		StartDate:     prevIssue.StartDate,
		DueDate:       prevIssue.DueDate,
		ParentIssueID: prevIssue.ParentIssueID,
		ProjectID:     prevIssue.ProjectID,
	}
	if req.Title != nil {
		params.Title = pgtype.Text{String: *req.Title, Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.Status != nil {
		params.Status = pgtype.Text{String: *req.Status, Valid: true}
	}
	if req.Priority != nil {
		params.Priority = pgtype.Text{String: *req.Priority, Valid: true}
	}
	if req.Position != nil {
		params.Position = pgtype.Float8{Float64: *req.Position, Valid: true}
	}
	if _, ok := rawFields["assignee_type"]; ok {
		if req.AssigneeType != nil {
			params.AssigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
		} else {
			params.AssigneeType = pgtype.Text{Valid: false}
		}
	}
	if _, ok := rawFields["assignee_id"]; ok {
		if req.AssigneeID != nil {
			id, ok := parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
			if !ok {
				return db.Issue{}, false
			}
			params.AssigneeID = id
		} else {
			params.AssigneeID = pgtype.UUID{Valid: false}
		}
	}
	if _, ok := rawFields["parent_issue_id"]; ok {
		if req.ParentIssueID != nil {
			id, err := util.ParseUUID(*req.ParentIssueID)
			if err != nil || id == prevIssue.ID {
				writeError(w, http.StatusBadRequest, "invalid parent_issue_id")
				return db.Issue{}, false
			}
			params.ParentIssueID = id
		} else {
			params.ParentIssueID = pgtype.UUID{Valid: false}
		}
	}
	if _, ok := rawFields["project_id"]; ok {
		if req.ProjectID != nil {
			id, ok := parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
			if !ok {
				return db.Issue{}, false
			}
			params.ProjectID = id
		} else {
			params.ProjectID = pgtype.UUID{Valid: false}
		}
	}
	if _, ok := rawFields["start_date"]; ok {
		if req.StartDate != nil && *req.StartDate != "" {
			t, err := time.Parse(time.RFC3339, *req.StartDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid start_date format, expected RFC3339")
				return db.Issue{}, false
			}
			params.StartDate = pgtype.Timestamptz{Time: t, Valid: true}
		} else {
			params.StartDate = pgtype.Timestamptz{Valid: false}
		}
	}
	if _, ok := rawFields["due_date"]; ok {
		if req.DueDate != nil && *req.DueDate != "" {
			t, err := time.Parse(time.RFC3339, *req.DueDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid due_date format, expected RFC3339")
				return db.Issue{}, false
			}
			params.DueDate = pgtype.Timestamptz{Time: t, Valid: true}
		} else {
			params.DueDate = pgtype.Timestamptz{Valid: false}
		}
	}
	updated, err := h.Queries.UpdateIssue(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to apply issue patch")
		return db.Issue{}, false
	}
	return updated, true
}

func (h *Handler) syncDecisionDeskReviewItems(ctx context.Context, workspaceID pgtype.UUID) {
	h.syncSkillProposalReviewItems(ctx, workspaceID)
	h.syncPlanReviewItems(ctx, workspaceID)
}

func (h *Handler) createArtifactReviewItemForVisualNode(ctx context.Context, project db.Project, node visualNodeRow, nodeID, taskID, issueID, attachmentID pgtype.UUID, note, noteZh string) {
	payload, _ := json.Marshal(map[string]any{
		"project_id":     uuidToString(project.ID),
		"project_title":  project.Title,
		"visual_node_id": uuidToString(nodeID),
		"node_title":     firstNonEmpty(node.TitleZh, node.Title),
		"node_type":      node.Type,
		"task_id":        uuidToString(taskID),
		"issue_id":       uuidToPtr(issueID),
		"attachment_id":  uuidToString(attachmentID),
		"note":           note,
		"note_zh":        noteZh,
	})
	_, _ = h.Queries.UpsertPendingReviewItemBySource(ctx, db.UpsertPendingReviewItemBySourceParams{
		WorkspaceID:       project.WorkspaceID,
		Type:              reviewTypeArtifact,
		RiskLevel:         "low",
		Title:             "Artifact 审阅 · " + firstNonEmpty(node.TitleZh, node.Title, project.Title),
		Summary:           firstNonEmpty(noteZh, note, "画布生成了新的 artifact，等待确认。"),
		SourceObjectType:  "project_visual_node",
		SourceObjectID:    nodeID,
		TargetObjectType:  "attachment",
		TargetObjectID:    attachmentID,
		Payload:           payload,
		Diff:              "",
		AvailableActions:  []string{"approve", "reject", "request_changes", "open_source"},
	})
}

func (h *Handler) syncSkillProposalReviewItems(ctx context.Context, workspaceID pgtype.UUID) {
	proposals, err := h.Queries.ListSkillProposalsByWorkspace(ctx, db.ListSkillProposalsByWorkspaceParams{
		WorkspaceID: workspaceID,
		Status:      pgtype.Text{String: "pending", Valid: true},
		Limit:       100,
	})
	if err != nil {
		return
	}
	for _, proposal := range proposals {
		payload, _ := json.Marshal(map[string]any{"skill_proposal": skillProposalToResponse(proposal)})
		_, _ = h.Queries.UpsertPendingReviewItemBySource(ctx, db.UpsertPendingReviewItemBySourceParams{
			WorkspaceID:       workspaceID,
			Type:              reviewTypeSkill,
			RiskLevel:         normalizeProposalRisk(proposal.RiskLevel),
			Title:             proposal.Title,
			Summary:           proposal.Summary,
			SourceObjectType:  "skill_proposal",
			SourceObjectID:    proposal.ID,
			TargetObjectType:  "skill",
			TargetObjectID:    proposal.TargetSkillID,
			Payload:           payload,
			Diff:              proposal.Diff,
			AvailableActions:  []string{"approve", "reject", "request_changes", "open_source"},
		})
	}
}

func (h *Handler) syncPlanReviewItems(ctx context.Context, workspaceID pgtype.UUID) {
	plans, err := h.Queries.ListPlans(ctx, db.ListPlansParams{
		WorkspaceID: workspaceID,
		Limit:       100,
		Offset:      0,
	})
	if err != nil {
		return
	}
	for _, plan := range plans {
		if plan.Status != "spec_review" {
			continue
		}
		spec := planSpecFromJSON(plan.Spec)
		payload, _ := json.Marshal(map[string]any{
			"plan": planToResponse(plan, nil),
			"spec": spec,
		})
		_, _ = h.Queries.UpsertPendingReviewItemBySource(ctx, db.UpsertPendingReviewItemBySourceParams{
			WorkspaceID:       workspaceID,
			Type:              reviewTypePlan,
			RiskLevel:         "medium",
			Title:             "Plan 审阅 · " + plan.Title,
			Summary:           firstNonEmpty(spec.Summary, spec.Goal, plan.Prompt),
			SourceObjectType:  "plan",
			SourceObjectID:    plan.ID,
			TargetObjectType:  "plan",
			TargetObjectID:    plan.ID,
			Payload:           payload,
			Diff:              "",
			AvailableActions:  []string{"approve", "reject", "request_changes", "rerun", "open_source"},
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

func errBadRequest(message string) error { return badRequestError(message) }
