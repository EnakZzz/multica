package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const planMatchThreshold = 60
const daemonCapabilityIssuePlan = "issue_plan"

type PlanResponse struct {
	ID                string             `json:"id"`
	WorkspaceID       string             `json:"workspace_id"`
	Title             string             `json:"title"`
	Prompt            string             `json:"prompt"`
	Status            string             `json:"status"`
	PlannerAgentID    string             `json:"planner_agent_id"`
	TaskID            string             `json:"task_id"`
	ProjectID         *string            `json:"project_id"`
	ParentTitle       string             `json:"parent_title"`
	ParentDescription string             `json:"parent_description"`
	ParentIssueID     *string            `json:"parent_issue_id"`
	Spec              service.PlanSpec   `json:"spec"`
	CommittedSpec     *service.PlanSpec  `json:"committed_spec"`
	SpecApprovedAt    *string            `json:"spec_approved_at"`
	SpecApprovedBy    *string            `json:"spec_approved_by"`
	Error             *string            `json:"error"`
	CreatedBy         string             `json:"created_by"`
	CreatedAt         *string            `json:"created_at"`
	UpdatedAt         *string            `json:"updated_at"`
	Items             []PlanItemResponse `json:"items,omitempty"`
}

type PlanItemResponse struct {
	ID                    string                  `json:"id"`
	PlanID                string                  `json:"plan_id"`
	Position              int32                   `json:"position"`
	Title                 string                  `json:"title"`
	Description           string                  `json:"description"`
	AcceptanceCriteria    []string                `json:"acceptance_criteria"`
	SuggestedTestCommands []string                `json:"suggested_test_commands"`
	UnitTestChecklist     []service.UnitTestCheck `json:"unit_test_checklist"`
	ContextResources      []string                `json:"context_resources"`
	RiskNotes             []string                `json:"risk_notes"`
	NodeType              string                  `json:"node_type"`
	ExecutionKind         string                  `json:"execution_kind"`
	ConfirmationQuestion  string                  `json:"confirmation_question"`
	ConfirmationReason    string                  `json:"confirmation_reason"`
	RequiredEvidence      []string                `json:"required_evidence"`
	RequiresGitCommit     bool                    `json:"requires_git_commit"`
	BranchName            string                  `json:"branch_name"`
	IterationIndex        int32                   `json:"iteration_index"`
	IterationTitle        string                  `json:"iteration_title"`
	IterationBranchName   string                  `json:"iteration_branch_name"`
	RecommendedAgentID    *string                 `json:"recommended_agent_id"`
	MatchScore            int32                   `json:"match_score"`
	MatchReason           string                  `json:"match_reason"`
	MissingCapability     string                  `json:"missing_capability"`
	DependsOnPositions    []int32                 `json:"depends_on_positions"`
	Selected              bool                    `json:"selected"`
	GeneratedIssueID      *string                 `json:"generated_issue_id"`
	CreatedAt             *string                 `json:"created_at"`
	UpdatedAt             *string                 `json:"updated_at"`
}

type createPlanRequest struct {
	Title          string `json:"title"`
	Prompt         string `json:"prompt"`
	PlannerAgentID string `json:"planner_agent_id"`
	ProjectID      string `json:"project_id"`
	SourceIssueID  string `json:"source_issue_id"`
}

type updatePlanRequest struct {
	Title             string                  `json:"title"`
	ParentTitle       string                  `json:"parent_title"`
	ParentDescription string                  `json:"parent_description"`
	Spec              *service.PlanSpec       `json:"spec"`
	Items             []updatePlanItemRequest `json:"items"`
}

type approvePlanSpecRequest struct {
	Spec *service.PlanSpec `json:"spec"`
}

type clarifyPlanSpecRequest struct {
	Spec    *service.PlanSpec           `json:"spec"`
	Answers []service.PlanClarification `json:"answers"`
}

type commitPlanRequest struct {
	AcknowledgedHumanConfirmationItemIDs []string `json:"acknowledged_human_confirmation_item_ids"`
}

type updatePlanItemRequest struct {
	Title                 string                  `json:"title"`
	Description           string                  `json:"description"`
	AcceptanceCriteria    []string                `json:"acceptance_criteria"`
	SuggestedTestCommands []string                `json:"suggested_test_commands"`
	UnitTestChecklist     []service.UnitTestCheck `json:"unit_test_checklist"`
	ContextResources      []string                `json:"context_resources"`
	RiskNotes             []string                `json:"risk_notes"`
	NodeType              string                  `json:"node_type"`
	ExecutionKind         string                  `json:"execution_kind"`
	ConfirmationQuestion  string                  `json:"confirmation_question"`
	ConfirmationReason    string                  `json:"confirmation_reason"`
	RequiredEvidence      []string                `json:"required_evidence"`
	RequiresGitCommit     *bool                   `json:"requires_git_commit"`
	BranchName            string                  `json:"branch_name"`
	IterationIndex        int32                   `json:"iteration_index"`
	IterationTitle        string                  `json:"iteration_title"`
	IterationBranchName   string                  `json:"iteration_branch_name"`
	RecommendedAgentID    string                  `json:"recommended_agent_id"`
	MatchScore            int32                   `json:"match_score"`
	MatchReason           string                  `json:"match_reason"`
	MissingCapability     string                  `json:"missing_capability"`
	DependsOnPositions    []int32                 `json:"depends_on_positions"`
	Selected              bool                    `json:"selected"`
}

func normalizePlanText(s string) string {
	return strings.TrimSpace(strings.ToValidUTF8(s, "\uFFFD"))
}

func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	limit := int32(50)
	offset := int32(0)
	plans, err := h.Queries.ListPlans(r.Context(), db.ListPlansParams{
		WorkspaceID: parseUUID(wsID),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	resp := make([]PlanResponse, len(plans))
	for i, p := range plans {
		resp[i] = planToResponse(p, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": resp})
}

func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	items, err := h.Queries.ListPlanItems(r.Context(), plan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list plan items")
		return
	}
	writeJSON(w, http.StatusOK, planToResponse(plan, items))
}

func (h *Handler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := middleware.WorkspaceIDFromContext(r.Context())
	wsUUID := parseUUID(wsID)
	var req createPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Prompt = normalizePlanText(req.Prompt)
	sourceIssueID := strings.TrimSpace(req.SourceIssueID)
	if req.Prompt == "" && sourceIssueID == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	title := normalizePlanText(req.Title)
	if title == "" {
		title = firstLine(req.Prompt)
	}
	plannerAgentID, ok := parseUUIDOrBadRequest(w, req.PlannerAgentID, "planner_agent_id")
	if !ok {
		return
	}
	if status, msg := h.validatePlanAgent(r, plannerAgentID, wsUUID); status != 0 {
		writeError(w, status, msg)
		return
	}
	if sourceIssueID != "" {
		issueID, ok := parseUUIDOrBadRequest(w, sourceIssueID, "source_issue_id")
		if !ok {
			return
		}
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          issueID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "source issue not found")
			return
		}
		plannerAgent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          plannerAgentID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "planner_agent_id does not refer to an agent of this workspace")
			return
		}
		_, plan, err := h.TaskService.EnqueuePlannerIssueTask(r.Context(), issue, plannerAgent, parseUUID(userID))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, planToResponse(plan, nil))
		return
	}
	projectID, ok := h.parseOptionalProjectID(w, r, req.ProjectID, wsUUID)
	if !ok {
		return
	}

	plan, err := h.Queries.CreatePlan(r.Context(), db.CreatePlanParams{
		WorkspaceID:    wsUUID,
		Title:          title,
		Prompt:         req.Prompt,
		PlannerAgentID: plannerAgentID,
		CreatedBy:      parseUUID(userID),
		ProjectID:      projectID,
	})
	if err != nil {
		slog.Warn("plan insert failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), wsUUID, parseUUID(userID), plan.ID, plannerAgentID, req.Prompt, projectID, service.IssuePlanPhaseSpec, service.PlanSpec{})
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

func (h *Handler) RerunPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	if plan.Status == "committed" {
		writeError(w, http.StatusBadRequest, "committed plans cannot be rerun")
		return
	}
	if status, msg := h.validatePlanAgent(r, plan.PlannerAgentID, plan.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), plan.WorkspaceID, parseUUID(userID), plan.ID, plan.PlannerAgentID, plan.Prompt, plan.ProjectID, service.IssuePlanPhaseSpec, service.PlanSpec{})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err = h.Queries.MarkPlanPlanning(r.Context(), db.MarkPlanPlanningParams{ID: plan.ID, TaskID: task.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to rerun plan")
		return
	}
	writeJSON(w, http.StatusOK, planToResponse(plan, nil))
}

func (h *Handler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	if plan.Status == "committed" {
		writeError(w, http.StatusBadRequest, "committed plans cannot be edited")
		return
	}
	var req updatePlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	specJSON := plan.Spec
	if req.Spec != nil {
		data, err := service.MarshalPlanSpec(*req.Spec)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid spec")
			return
		}
		specJSON = data
	}
	updated, err := h.Queries.UpdatePlanDraft(r.Context(), db.UpdatePlanDraftParams{
		ID:                plan.ID,
		Title:             pgtype.Text{String: strings.TrimSpace(req.Title), Valid: strings.TrimSpace(req.Title) != ""},
		ParentTitle:       pgtype.Text{String: strings.TrimSpace(req.ParentTitle), Valid: true},
		ParentDescription: pgtype.Text{String: strings.TrimSpace(req.ParentDescription), Valid: true},
		Spec:              specJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update plan")
		return
	}
	if req.Items != nil {
		if err := h.replacePlanItems(r, updated, req.Items); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	items, _ := h.Queries.ListPlanItems(r.Context(), updated.ID)
	writeJSON(w, http.StatusOK, planToResponse(updated, items))
}

func (h *Handler) ApprovePlanSpec(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	if plan.Status != "spec_review" {
		writeError(w, http.StatusBadRequest, "plan spec is not ready for review")
		return
	}
	if status, msg := h.validatePlanAgent(r, plan.PlannerAgentID, plan.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return
	}
	spec := planSpecFromJSON(plan.Spec)
	var req approvePlanSpecRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if req.Spec != nil {
		spec = service.NormalizePlanSpec(*req.Spec)
	}
	if strings.TrimSpace(spec.Summary) == "" {
		writeError(w, http.StatusBadRequest, "spec.summary is required")
		return
	}
	if strings.TrimSpace(spec.Goal) == "" {
		writeError(w, http.StatusBadRequest, "spec.goal is required")
		return
	}
	specJSON, err := service.MarshalPlanSpec(spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid spec")
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), plan.WorkspaceID, parseUUID(userID), plan.ID, plan.PlannerAgentID, plan.Prompt, plan.ProjectID, service.IssuePlanPhaseItems, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	approved, err := h.Queries.ApprovePlanSpec(r.Context(), db.ApprovePlanSpecParams{
		ID:             plan.ID,
		TaskID:         task.ID,
		Spec:           specJSON,
		SpecApprovedBy: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to approve plan spec")
		return
	}
	writeJSON(w, http.StatusOK, planToResponse(approved, nil))
}

func (h *Handler) ClarifyPlanSpec(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	if plan.Status != "spec_review" {
		writeError(w, http.StatusBadRequest, "plan spec is not ready for clarification")
		return
	}
	if status, msg := h.validatePlanAgent(r, plan.PlannerAgentID, plan.WorkspaceID); status != 0 {
		writeError(w, status, msg)
		return
	}
	var req clarifyPlanSpecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	spec := planSpecFromJSON(plan.Spec)
	if req.Spec != nil {
		spec = service.NormalizePlanSpec(*req.Spec)
	}
	nextSpec, answered := applyPlanClarifications(spec, req.Answers)
	if len(answered) == 0 {
		writeError(w, http.StatusBadRequest, "at least one clarification answer is required")
		return
	}
	specJSON, err := service.MarshalPlanSpec(nextSpec)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid spec")
		return
	}
	if _, err := h.Queries.UpdatePlanDraft(r.Context(), db.UpdatePlanDraftParams{
		ID:                plan.ID,
		Title:             pgtype.Text{},
		ParentTitle:       plan.ParentTitle,
		ParentDescription: plan.ParentDescription,
		Spec:              specJSON,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update plan spec")
		return
	}
	task, err := h.TaskService.EnqueueIssuePlanTask(r.Context(), plan.WorkspaceID, parseUUID(userID), plan.ID, plan.PlannerAgentID, plan.Prompt, plan.ProjectID, service.IssuePlanPhaseSpec, nextSpec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	clarifying, err := h.Queries.MarkPlanPlanning(r.Context(), db.MarkPlanPlanningParams{ID: plan.ID, TaskID: task.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clarify plan spec")
		return
	}
	writeJSON(w, http.StatusOK, planToResponse(clarifying, nil))
}

func (h *Handler) CommitPlan(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	plan, ok := h.loadPlan(w, r)
	if !ok {
		return
	}
	if plan.Status != "ready" && plan.Status != "committed" {
		writeError(w, http.StatusBadRequest, "plan is not ready")
		return
	}
	items, err := h.Queries.ListPlanItems(r.Context(), plan.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list plan items")
		return
	}
	var req commitPlanRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	acknowledged := make(map[string]bool, len(req.AcknowledgedHumanConfirmationItemIDs))
	for _, id := range req.AcknowledgedHumanConfirmationItemIDs {
		acknowledged[strings.TrimSpace(id)] = true
	}
	if plan.Status == "ready" {
		for _, item := range items {
			if !item.Selected || item.GeneratedIssueID.Valid || normalizePlanItemExecutionKind(item.ExecutionKind) != service.PlanItemExecutionKindHumanConfirmation {
				continue
			}
			if !acknowledged[uuidToString(item.ID)] {
				writeError(w, http.StatusBadRequest, "human confirmation items must be acknowledged before creating issues")
				return
			}
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	issuesByPosition := make(map[int32]db.Issue)
	var createdChildren []db.Issue

	parentID := plan.ParentIssueID
	if !parentID.Valid {
		number, err := qtx.IncrementIssueCounter(r.Context(), plan.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
			return
		}
		parentTitle := plan.ParentTitle.String
		if strings.TrimSpace(parentTitle) == "" {
			parentTitle = plan.Title
		}
		parent, err := qtx.CreateIssue(r.Context(), db.CreateIssueParams{
			WorkspaceID: plan.WorkspaceID,
			Title:       parentTitle,
			Description: strOrNullText(plan.ParentDescription.String),
			Status:      "todo",
			Priority:    "none",
			CreatorType: "member",
			CreatorID:   parseUUID(userID),
			Number:      number,
			ProjectID:   plan.ProjectID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create parent issue")
			return
		}
		parentID = parent.ID
	}

	committedSpec := planSpecFromJSON(plan.CommittedSpec)
	if strings.TrimSpace(committedSpec.Goal) == "" && strings.TrimSpace(committedSpec.Summary) == "" {
		committedSpec = planSpecFromJSON(plan.Spec)
	}

	for _, item := range items {
		if !item.Selected {
			continue
		}
		if item.GeneratedIssueID.Valid {
			if child, err := qtx.GetIssue(r.Context(), item.GeneratedIssueID); err == nil {
				issuesByPosition[item.Position] = child
			}
			continue
		}
		number, err := qtx.IncrementIssueCounter(r.Context(), plan.WorkspaceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate issue number")
			return
		}
		executionKind := normalizePlanItemExecutionKind(item.ExecutionKind)
		var assigneeType pgtype.Text
		var assigneeID pgtype.UUID
		if executionKind != service.PlanItemExecutionKindHumanConfirmation && item.RecommendedAgentID.Valid && item.MatchScore >= planMatchThreshold {
			if agent, err := qtx.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: item.RecommendedAgentID, WorkspaceID: plan.WorkspaceID}); err == nil && !agent.ArchivedAt.Valid {
				assigneeType = pgtype.Text{String: "agent", Valid: true}
				assigneeID = item.RecommendedAgentID
			}
		}
		unitTestChecklist := service.MarshalUnitTestChecklist(service.NormalizeUnitTestChecklistJSON(item.UnitTestChecklist))
		child, err := qtx.CreateIssueWithOriginAndUnitTestsManual(r.Context(), db.CreateIssueWithOriginAndUnitTestsManualParams{
			WorkspaceID:       plan.WorkspaceID,
			Title:             item.Title,
			Description:       strOrNullText(planItemIssueDescription(item, committedSpec)),
			Status:            "todo",
			Priority:          "none",
			AssigneeType:      assigneeType,
			AssigneeID:        assigneeID,
			CreatorType:       "member",
			CreatorID:         parseUUID(userID),
			ParentIssueID:     parentID,
			Number:            number,
			ProjectID:         plan.ProjectID,
			OriginType:        pgtype.Text{String: "plan_item", Valid: true},
			OriginID:          item.ID,
			UnitTestChecklist: unitTestChecklist,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create child issue")
			return
		}
		if _, err := qtx.UpdatePlanItemGeneratedIssue(r.Context(), db.UpdatePlanItemGeneratedIssueParams{ID: item.ID, GeneratedIssueID: child.ID}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update plan item")
			return
		}
		issuesByPosition[item.Position] = child
		createdChildren = append(createdChildren, child)
	}
	for _, item := range items {
		if !item.Selected || len(item.DependsOnPositions) == 0 {
			continue
		}
		child, ok := issuesByPosition[item.Position]
		if !ok {
			continue
		}
		for _, depPosition := range item.DependsOnPositions {
			dep, ok := issuesByPosition[depPosition]
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
	plan, err = qtx.MarkPlanCommitted(r.Context(), db.MarkPlanCommittedParams{ID: plan.ID, ParentIssueID: parentID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark plan committed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit plan")
		return
	}
	for _, child := range createdChildren {
		if h.shouldEnqueueAgentTask(r.Context(), child) {
			h.TaskService.EnqueueTaskForIssue(r.Context(), child)
		}
	}
	items, _ = h.Queries.ListPlanItems(r.Context(), plan.ID)
	writeJSON(w, http.StatusOK, planToResponse(plan, items))
}

func (h *Handler) loadPlan(w http.ResponseWriter, r *http.Request) (db.Plan, bool) {
	id, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return db.Plan{}, false
	}
	plan, err := h.Queries.GetPlanInWorkspace(r.Context(), db.GetPlanInWorkspaceParams{
		ID:          id,
		WorkspaceID: parseUUID(middleware.WorkspaceIDFromContext(r.Context())),
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "plan not found")
		return plan, false
	}
	return plan, true
}

func (h *Handler) validatePlanAgent(r *http.Request, agentID, workspaceID pgtype.UUID) (int, string) {
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil {
		return http.StatusBadRequest, "planner_agent_id does not refer to an agent of this workspace"
	}
	if agent.ArchivedAt.Valid {
		return http.StatusBadRequest, "planner agent is archived"
	}
	if !h.canAccessPrivateAgent(r.Context(), agent, "member", requestUserID(r), uuidToString(workspaceID)) {
		return http.StatusForbidden, "cannot use private planner agent"
	}
	if !agent.RuntimeID.Valid {
		return http.StatusUnprocessableEntity, "planner agent has no runtime"
	}
	if !h.isRuntimeOnline(r.Context(), agent.RuntimeID) {
		return http.StatusUnprocessableEntity, "planner agent runtime is offline"
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), agent.RuntimeID)
	if err != nil {
		return http.StatusUnprocessableEntity, "planner agent runtime is no longer registered"
	}
	if !runtimeHasCapability(rt.Metadata, daemonCapabilityIssuePlan) {
		return http.StatusUnprocessableEntity, "planner agent daemon does not support Plans yet; update or restart the daemon and try again"
	}
	return 0, ""
}

func runtimeHasCapability(metadata []byte, capability string) bool {
	if len(metadata) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(metadata, &m); err != nil {
		return false
	}
	raw, ok := m["capabilities"].([]any)
	if !ok {
		return false
	}
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(s), capability) {
			return true
		}
	}
	return false
}

func (h *Handler) parseOptionalProjectID(w http.ResponseWriter, r *http.Request, raw string, workspaceID pgtype.UUID) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, true
	}
	id, ok := parseUUIDOrBadRequest(w, raw, "project_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	if _, err := h.Queries.GetProjectAccessibleInWorkspace(r.Context(), db.GetProjectAccessibleInWorkspaceParams{ID: id, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusBadRequest, "project_id does not refer to a project accessible from this workspace")
		return pgtype.UUID{}, false
	}
	return id, true
}

func (h *Handler) replacePlanItems(r *http.Request, plan db.Plan, reqItems []updatePlanItemRequest) error {
	h.ensureBuiltInAgentsForWorkspace(r.Context(), plan.WorkspaceID)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeletePlanItems(r.Context(), plan.ID); err != nil {
		return err
	}
	mergeAgentID := h.builtInMergeAgentIDString(r.Context(), qtx, plan.WorkspaceID)
	reqItems = normalizeUpdatePlanItemIterations(plan.Title, reqItems, mergeAgentID)
	for i, item := range reqItems {
		if strings.TrimSpace(item.Title) == "" {
			return &planValidationError{"plan item title is required"}
		}
		score := item.MatchScore
		if score < 0 || score > 100 {
			return &planValidationError{"match_score must be 0-100"}
		}
		var agentID pgtype.UUID
		if strings.TrimSpace(item.RecommendedAgentID) != "" && score >= planMatchThreshold {
			parsed, err := util.ParseUUID(strings.TrimSpace(item.RecommendedAgentID))
			if err != nil {
				return &planValidationError{"recommended_agent_id is invalid"}
			}
			agent, err := qtx.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: parsed, WorkspaceID: plan.WorkspaceID})
			if err != nil || agent.ArchivedAt.Valid {
				return &planValidationError{"recommended_agent_id does not refer to an active agent of this workspace"}
			}
			agentID = parsed
		}
		dependsOnPositions, err := normalizePlanDependsOnPositions(item.DependsOnPositions, int32(i+1))
		if err != nil {
			return err
		}
		executionKind := normalizePlanItemExecutionKind(item.ExecutionKind)
		nodeType := service.NormalizePlanItemNodeType(item.NodeType)
		confirmationQuestion := strings.TrimSpace(item.ConfirmationQuestion)
		confirmationReason := strings.TrimSpace(item.ConfirmationReason)
		requiredEvidence := normalizePlanItemStringList(item.RequiredEvidence)
		requiresGitCommit := true
		if item.RequiresGitCommit != nil {
			requiresGitCommit = *item.RequiresGitCommit
		}
		branchName := strings.TrimSpace(item.BranchName)
		if executionKind == service.PlanItemExecutionKindHumanConfirmation {
			nodeType = service.PipelineNodeTypeManual
			if confirmationQuestion == "" {
				confirmationQuestion = strings.TrimSpace(item.Title)
			}
			if confirmationReason == "" {
				confirmationReason = strings.TrimSpace(item.Description)
			}
			if confirmationReason == "" {
				return &planValidationError{"confirmation_reason is required for human confirmation items"}
			}
			agentID = pgtype.UUID{}
			score = 0
			requiresGitCommit = false
			branchName = ""
		} else if nodeType == service.PipelineNodeTypeMerge {
			requiresGitCommit = false
			branchName = ""
			confirmationQuestion = ""
			confirmationReason = ""
			requiredEvidence = []string{}
		} else {
			confirmationQuestion = ""
			confirmationReason = ""
			requiredEvidence = []string{}
			if !requiresGitCommit {
				branchName = ""
			} else if branchName == "" {
				branchName = normalizePlanBranchName("", strings.TrimSpace(item.Title))
			}
		}
		if _, err := qtx.CreatePlanItem(r.Context(), db.CreatePlanItemParams{
			PlanID:                plan.ID,
			Position:              int32(i + 1),
			Title:                 strings.TrimSpace(item.Title),
			Description:           strings.TrimSpace(item.Description),
			AcceptanceCriteria:    normalizePlanItemStringList(item.AcceptanceCriteria),
			SuggestedTestCommands: normalizePlanItemStringList(item.SuggestedTestCommands),
			UnitTestChecklist:     service.MarshalUnitTestChecklist(service.NormalizeUnitTestChecks(item.UnitTestChecklist)),
			ContextResources:      normalizePlanItemStringList(item.ContextResources),
			RiskNotes:             normalizePlanItemStringList(item.RiskNotes),
			NodeType:              nodeType,
			ExecutionKind:         executionKind,
			ConfirmationQuestion:  confirmationQuestion,
			ConfirmationReason:    confirmationReason,
			RequiredEvidence:      requiredEvidence,
			RequiresGitCommit:     requiresGitCommit,
			BranchName:            branchName,
			IterationIndex:        normalizePlanIterationIndex(item.IterationIndex),
			IterationTitle:        strings.TrimSpace(item.IterationTitle),
			IterationBranchName:   normalizeOptionalPlanBranchName(item.IterationBranchName),
			RecommendedAgentID:    agentID,
			MatchScore:            score,
			MatchReason:           strings.TrimSpace(item.MatchReason),
			MissingCapability:     strings.TrimSpace(item.MissingCapability),
			DependsOnPositions:    dependsOnPositions,
			Selected:              item.Selected,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(r.Context())
}

func (h *Handler) builtInMergeAgentIDString(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID) string {
	if !workspaceID.Valid {
		return ""
	}
	agent, err := qtx.GetBuiltInAgentByKey(ctx, db.GetBuiltInAgentByKeyParams{
		WorkspaceID: workspaceID,
		BuiltinKey:  pgtype.Text{String: "multica/merge-agent", Valid: true},
	})
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return ""
	}
	return uuidToString(agent.ID)
}

type planValidationError struct {
	msg string
}

func (e *planValidationError) Error() string { return e.msg }

func normalizePlanDependsOnPositions(raw []int32, position int32) ([]int32, error) {
	if len(raw) == 0 {
		return []int32{}, nil
	}
	seen := make(map[int32]bool, len(raw))
	out := make([]int32, 0, len(raw))
	for _, dep := range raw {
		if dep <= 0 || dep >= position {
			return nil, &planValidationError{"depends_on_positions must reference earlier plan item positions"}
		}
		if seen[dep] {
			continue
		}
		seen[dep] = true
		out = append(out, dep)
	}
	return out, nil
}

func normalizePlanItemStringList(raw []string) []string {
	if len(raw) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func normalizePlanItemExecutionKind(kind string) string {
	if strings.TrimSpace(kind) == service.PlanItemExecutionKindHumanConfirmation {
		return service.PlanItemExecutionKindHumanConfirmation
	}
	return service.PlanItemExecutionKindAgentTask
}

func normalizeUpdatePlanItemIterations(planTitle string, items []updatePlanItemRequest, mergeAgentID string) []updatePlanItemRequest {
	normalized := make([]updatePlanItemRequest, len(items))
	groups := make(map[int32]*updatePlanIterationGroup)
	for i, item := range items {
		item.NodeType = service.NormalizePlanItemNodeType(item.NodeType)
		item.IterationIndex = normalizePlanIterationIndex(item.IterationIndex)
		item.IterationTitle = strings.TrimSpace(item.IterationTitle)
		item.IterationBranchName = normalizeOptionalPlanBranchName(item.IterationBranchName)
		item.BranchName = normalizeOptionalPlanBranchName(item.BranchName)
		normalized[i] = item

		group := groups[item.IterationIndex]
		if group == nil {
			group = &updatePlanIterationGroup{index: item.IterationIndex, firstOldPos: int32(i + 1)}
			groups[item.IterationIndex] = group
		}
		if item.NodeType != service.PipelineNodeTypeMerge {
			group.lastOldPos = int32(i + 1)
		}
		if group.title == "" && item.IterationTitle != "" {
			group.title = item.IterationTitle
		}
		if item.IterationBranchName != "" && !group.branchLocked {
			group.branch = item.IterationBranchName
			group.branchLocked = true
		}
		if updatePlanItemRequiresGitCommit(item) && group.branch == "" && item.BranchName != "" {
			group.branch = item.BranchName
		}
		if item.Selected && item.NodeType != service.PipelineNodeTypeMerge && normalizePlanItemExecutionKind(item.ExecutionKind) != service.PlanItemExecutionKindHumanConfirmation {
			group.workOldPos = append(group.workOldPos, int32(i+1))
			if updatePlanItemRequiresGitCommit(item) {
				group.gitWorkOldPos = append(group.gitWorkOldPos, int32(i+1))
			}
		}
	}
	for _, group := range groups {
		if group.branch == "" {
			group.branch = fallbackPlanIterationBranchName(planTitle, group.index)
		}
	}
	for i, item := range normalized {
		group := groups[item.IterationIndex]
		if group == nil {
			continue
		}
		item.IterationTitle = group.title
		item.IterationBranchName = group.branch
		if updatePlanItemRequiresGitCommit(item) {
			item.BranchName = group.branch
		} else {
			item.BranchName = ""
		}
		normalized[i] = item
	}
	return ensureUpdatePlanIterationGates(normalized, groups, mergeAgentID)
}

type updatePlanIterationGroup struct {
	index         int32
	title         string
	branch        string
	branchLocked  bool
	firstOldPos   int32
	lastOldPos    int32
	workOldPos    []int32
	gitWorkOldPos []int32
}

func ensureUpdatePlanIterationGates(items []updatePlanItemRequest, groups map[int32]*updatePlanIterationGroup, mergeAgentID string) []updatePlanItemRequest {
	if len(items) == 0 {
		return items
	}
	out := make([]updatePlanItemRequest, 0, len(items)+len(groups)*2)
	oldToNew := make(map[int32]int32, len(items))
	var previousBoundaryNewPos int32
	for i, item := range items {
		oldPos := int32(i + 1)
		group := groups[item.IterationIndex]
		item.DependsOnPositions = remapUpdatePlanDependsOnPositions(item.DependsOnPositions, oldToNew)
		if service.NormalizePlanItemNodeType(item.NodeType) == service.PipelineNodeTypeMerge {
			continue
		}
		if previousBoundaryNewPos > 0 && item.Selected && normalizePlanItemExecutionKind(item.ExecutionKind) != service.PlanItemExecutionKindHumanConfirmation {
			item.DependsOnPositions = appendUniquePlanPosition(item.DependsOnPositions, previousBoundaryNewPos)
		}
		isExistingFinalGate := group != nil && group.lastOldPos == oldPos && normalizePlanItemExecutionKind(item.ExecutionKind) == service.PlanItemExecutionKindHumanConfirmation
		if isExistingFinalGate {
			item.DependsOnPositions = appendUniquePlanPosition(item.DependsOnPositions, remappedPlanPositions(group.workOldPos, oldToNew)...)
		}
		out = append(out, item)
		newPos := int32(len(out))
		oldToNew[oldPos] = newPos
		if group == nil || group.lastOldPos != oldPos {
			continue
		}
		if len(group.workOldPos) == 0 {
			continue
		}
		var gateNewPos int32
		if isExistingFinalGate {
			gateNewPos = newPos
		} else {
			gate := updatePlanIterationGateItem(*group, remappedPlanPositions(group.workOldPos, oldToNew))
			out = append(out, gate)
			gateNewPos = int32(len(out))
		}
		previousBoundaryNewPos = gateNewPos
		if len(group.gitWorkOldPos) == 0 {
			continue
		}
		merge := updatePlanIterationMergeItem(*group, mergeAgentID, []int32{gateNewPos})
		out = append(out, merge)
		previousBoundaryNewPos = int32(len(out))
	}
	return out
}

func updatePlanIterationGateItem(group updatePlanIterationGroup, dependsOn []int32) updatePlanItemRequest {
	no := false
	title := strings.TrimSpace(group.title)
	if title == "" {
		title = fmt.Sprintf("Iteration %d", group.index)
	}
	return updatePlanItemRequest{
		Title:                "迭代验收 / Iteration Gate: " + title,
		Description:          "Human confirms this iteration's deliverables, verification evidence, and residual risk before the next iteration proceeds.",
		NodeType:             service.PipelineNodeTypeManual,
		ExecutionKind:        service.PlanItemExecutionKindHumanConfirmation,
		ConfirmationQuestion: fmt.Sprintf("是否接受本轮交付并允许进入下一轮？ / Accept iteration %d and allow the next iteration to proceed?", group.index),
		ConfirmationReason:   "Iteration boundaries should stop downstream work until a human accepts the delivered behavior, evidence, and remaining risks.",
		RequiredEvidence: []string{
			"本轮选中的交付项已完成 / Selected iteration deliverables are complete",
			"验证、测试或评审证据已记录 / Verification, test, or review evidence is recorded",
			"剩余风险已接受或转成后续任务 / Residual risks are accepted or converted into follow-up work",
		},
		RequiresGitCommit:   &no,
		BranchName:          "",
		IterationIndex:      group.index,
		IterationTitle:      group.title,
		IterationBranchName: group.branch,
		MatchScore:          0,
		MatchReason:         "Waiting for human iteration acceptance.",
		DependsOnPositions:  dependsOn,
		Selected:            true,
	}
}

func updatePlanIterationMergeItem(group updatePlanIterationGroup, mergeAgentID string, dependsOn []int32) updatePlanItemRequest {
	no := false
	title := strings.TrimSpace(group.title)
	if title == "" {
		title = fmt.Sprintf("Iteration %d", group.index)
	}
	score := int32(0)
	matchReason := "Merge work is gated by the iteration human confirmation."
	if strings.TrimSpace(mergeAgentID) != "" {
		score = 100
		matchReason = "Built-in Merge Agent handles PR-first branch integration after human confirmation."
	}
	return updatePlanItemRequest{
		Title:       "合入 / 集成 · Merge / Integrate: " + title,
		Description: mergePlanItemDescription(group.branch),
		AcceptanceCriteria: []string{
			"Source branch, target branch, and integration mode are recorded.",
			"PR URL or merge commit is recorded.",
			"Test result is recorded.",
			"Final status is recorded as merged, pr_created, or failed.",
			"Failure reason and conflict files are recorded when integration fails.",
		},
		NodeType:            service.PipelineNodeTypeMerge,
		ExecutionKind:       service.PlanItemExecutionKindAgentTask,
		RequiresGitCommit:   &no,
		BranchName:          "",
		IterationIndex:      group.index,
		IterationTitle:      group.title,
		IterationBranchName: group.branch,
		RecommendedAgentID:  strings.TrimSpace(mergeAgentID),
		MatchScore:          score,
		MatchReason:         matchReason,
		DependsOnPositions:  dependsOn,
		Selected:            true,
	}
}

func mergePlanItemDescription(sourceBranch string) string {
	sourceBranch = strings.TrimSpace(sourceBranch)
	if sourceBranch == "" {
		sourceBranch = "<iteration branch>"
	}
	return fmt.Sprintf("Integrate the confirmed iteration branch using PR-first behavior. Source branch: %s. Target branch defaults to the project repo default_branch_hint, or main when no hint exists. Record source branch, target branch, PR URL or merge commit, test result, success or failure status, and conflict files on failure.", sourceBranch)
}

func remapUpdatePlanDependsOnPositions(raw []int32, oldToNew map[int32]int32) []int32 {
	if len(raw) == 0 {
		return []int32{}
	}
	out := make([]int32, 0, len(raw))
	for _, old := range raw {
		if next, ok := oldToNew[old]; ok {
			out = appendUniquePlanPosition(out, next)
		}
	}
	return out
}

func remappedPlanPositions(raw []int32, oldToNew map[int32]int32) []int32 {
	out := make([]int32, 0, len(raw))
	for _, old := range raw {
		if next, ok := oldToNew[old]; ok {
			out = appendUniquePlanPosition(out, next)
		}
	}
	return out
}

func appendUniquePlanPosition(out []int32, values ...int32) []int32 {
	for _, value := range values {
		if value <= 0 {
			continue
		}
		exists := false
		for _, existing := range out {
			if existing == value {
				exists = true
				break
			}
		}
		if !exists {
			out = append(out, value)
		}
	}
	return out
}

func updatePlanItemRequiresGitCommit(item updatePlanItemRequest) bool {
	if normalizePlanItemExecutionKind(item.ExecutionKind) == service.PlanItemExecutionKindHumanConfirmation {
		return false
	}
	if service.NormalizePlanItemNodeType(item.NodeType) == service.PipelineNodeTypeMerge {
		return false
	}
	if item.RequiresGitCommit == nil {
		return true
	}
	return *item.RequiresGitCommit
}

func normalizePlanIterationIndex(index int32) int32 {
	if index <= 0 {
		return 1
	}
	return index
}

func normalizeOptionalPlanBranchName(raw string) string {
	branch := strings.ToLower(strings.TrimSpace(raw))
	branch = strings.ReplaceAll(branch, "\\", "/")
	if branch == "" {
		return ""
	}
	parts := strings.Split(branch, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = service.SlugifyPlanBranchSegment(part)
		if part != "" {
			cleanParts = append(cleanParts, part)
		}
	}
	branch = strings.Join(cleanParts, "/")
	if branch == "" {
		return ""
	}
	if !strings.Contains(branch, "/") {
		branch = "feature/" + branch
	} else {
		prefix := strings.SplitN(branch, "/", 2)[0]
		if !isAllowedPlanBranchPrefix(prefix) {
			branch = "feature/" + strings.ReplaceAll(branch, "/", "-")
		}
	}
	return strings.Trim(branch, "/")
}

func fallbackPlanIterationBranchName(planTitle string, iterationIndex int32) string {
	planSlug := service.SlugifyPlanBranchSegment(planTitle)
	if planSlug == "" {
		planSlug = "plan"
	}
	return fmt.Sprintf("feature/%s-iter-%d", planSlug, normalizePlanIterationIndex(iterationIndex))
}

func normalizePlanBranchName(raw, fallbackTitle string) string {
	branch := strings.ToLower(strings.TrimSpace(raw))
	branch = strings.ReplaceAll(branch, "\\", "/")
	parts := strings.Split(branch, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = service.SlugifyPlanBranchSegment(part)
		if part != "" {
			cleanParts = append(cleanParts, part)
		}
	}
	branch = strings.Join(cleanParts, "/")
	if branch == "" && strings.TrimSpace(fallbackTitle) != "" {
		titleSlug := service.SlugifyPlanBranchSegment(fallbackTitle)
		if titleSlug == "" {
			titleSlug = "plan-item"
		}
		branch = "feature/" + titleSlug
	}
	if branch == "" {
		return ""
	}
	if !strings.Contains(branch, "/") {
		branch = "feature/" + branch
	} else {
		prefix := strings.SplitN(branch, "/", 2)[0]
		if !isAllowedPlanBranchPrefix(prefix) {
			branch = "feature/" + strings.ReplaceAll(branch, "/", "-")
		}
	}
	return strings.Trim(branch, "/")
}

func isAllowedPlanBranchPrefix(prefix string) bool {
	switch prefix {
	case "feature", "fix", "chore", "docs", "refactor", "test", "ci":
		return true
	default:
		return false
	}
}

func planToResponse(p db.Plan, items []db.PlanItem) PlanResponse {
	var projectID *string
	if p.ProjectID.Valid {
		v := uuidToString(p.ProjectID)
		projectID = &v
	}
	var parentIssueID *string
	if p.ParentIssueID.Valid {
		v := uuidToString(p.ParentIssueID)
		parentIssueID = &v
	}
	var specApprovedBy *string
	if p.SpecApprovedBy.Valid {
		v := uuidToString(p.SpecApprovedBy)
		specApprovedBy = &v
	}
	itemResp := make([]PlanItemResponse, len(items))
	for i, item := range items {
		itemResp[i] = planItemToResponse(item)
	}
	return PlanResponse{
		ID:                uuidToString(p.ID),
		WorkspaceID:       uuidToString(p.WorkspaceID),
		Title:             p.Title,
		Prompt:            p.Prompt,
		Status:            p.Status,
		PlannerAgentID:    uuidToString(p.PlannerAgentID),
		TaskID:            uuidToString(p.TaskID),
		ProjectID:         projectID,
		ParentTitle:       p.ParentTitle.String,
		ParentDescription: p.ParentDescription.String,
		ParentIssueID:     parentIssueID,
		Spec:              planSpecFromJSON(p.Spec),
		CommittedSpec:     planSpecPtrFromJSON(p.CommittedSpec),
		SpecApprovedAt:    timestampToPtr(p.SpecApprovedAt),
		SpecApprovedBy:    specApprovedBy,
		Error:             textToPtr(p.Error),
		CreatedBy:         uuidToString(p.CreatedBy),
		CreatedAt:         timestampToPtr(p.CreatedAt),
		UpdatedAt:         timestampToPtr(p.UpdatedAt),
		Items:             itemResp,
	}
}

func planSpecPtrFromJSON(data []byte) *service.PlanSpec {
	if len(data) == 0 {
		return nil
	}
	spec := planSpecFromJSON(data)
	return &spec
}

func planSpecFromJSON(data []byte) service.PlanSpec {
	var spec service.PlanSpec
	if len(data) == 0 {
		return service.NormalizePlanSpec(spec)
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return service.NormalizePlanSpec(service.PlanSpec{})
	}
	return service.NormalizePlanSpec(spec)
}

func applyPlanClarifications(spec service.PlanSpec, answers []service.PlanClarification) (service.PlanSpec, []service.PlanClarification) {
	spec = service.NormalizePlanSpec(spec)
	answered := make([]service.PlanClarification, 0, len(answers))
	byQuestion := make(map[string]int, len(spec.Clarifications))
	for i, c := range spec.Clarifications {
		byQuestion[strings.ToLower(strings.TrimSpace(c.Question))] = i
	}
	for _, answer := range answers {
		question := strings.TrimSpace(answer.Question)
		value := strings.TrimSpace(answer.Answer)
		if question == "" || value == "" {
			continue
		}
		normalized := service.PlanClarification{Question: question, Answer: value}
		if idx, ok := byQuestion[strings.ToLower(question)]; ok {
			spec.Clarifications[idx] = normalized
		} else {
			byQuestion[strings.ToLower(question)] = len(spec.Clarifications)
			spec.Clarifications = append(spec.Clarifications, normalized)
		}
		answered = append(answered, normalized)
	}
	if len(answered) == 0 {
		return spec, answered
	}
	answeredQuestions := make(map[string]bool, len(answered))
	for _, item := range answered {
		answeredQuestions[strings.ToLower(strings.TrimSpace(item.Question))] = true
	}
	open := spec.OpenQuestions[:0]
	for _, question := range spec.OpenQuestions {
		if !answeredQuestions[strings.ToLower(strings.TrimSpace(question))] {
			open = append(open, question)
		}
	}
	spec.OpenQuestions = open
	return service.NormalizePlanSpec(spec), answered
}

func planItemToResponse(item db.PlanItem) PlanItemResponse {
	var agentID *string
	if item.RecommendedAgentID.Valid {
		v := uuidToString(item.RecommendedAgentID)
		agentID = &v
	}
	var issueID *string
	if item.GeneratedIssueID.Valid {
		v := uuidToString(item.GeneratedIssueID)
		issueID = &v
	}
	return PlanItemResponse{
		ID:                    uuidToString(item.ID),
		PlanID:                uuidToString(item.PlanID),
		Position:              item.Position,
		Title:                 item.Title,
		Description:           item.Description,
		AcceptanceCriteria:    normalizePlanItemStringList(item.AcceptanceCriteria),
		SuggestedTestCommands: normalizePlanItemStringList(item.SuggestedTestCommands),
		UnitTestChecklist:     service.NormalizeUnitTestChecklistJSON(item.UnitTestChecklist),
		ContextResources:      normalizePlanItemStringList(item.ContextResources),
		RiskNotes:             normalizePlanItemStringList(item.RiskNotes),
		NodeType:              service.NormalizePlanItemNodeType(item.NodeType),
		ExecutionKind:         normalizePlanItemExecutionKind(item.ExecutionKind),
		ConfirmationQuestion:  strings.TrimSpace(item.ConfirmationQuestion),
		ConfirmationReason:    strings.TrimSpace(item.ConfirmationReason),
		RequiredEvidence:      normalizePlanItemStringList(item.RequiredEvidence),
		RequiresGitCommit:     item.RequiresGitCommit,
		BranchName:            strings.TrimSpace(item.BranchName),
		IterationIndex:        normalizePlanIterationIndex(item.IterationIndex),
		IterationTitle:        strings.TrimSpace(item.IterationTitle),
		IterationBranchName:   strings.TrimSpace(item.IterationBranchName),
		RecommendedAgentID:    agentID,
		MatchScore:            item.MatchScore,
		MatchReason:           item.MatchReason,
		MissingCapability:     item.MissingCapability,
		DependsOnPositions:    item.DependsOnPositions,
		Selected:              item.Selected,
		GeneratedIssueID:      issueID,
		CreatedAt:             timestampToPtr(item.CreatedAt),
		UpdatedAt:             timestampToPtr(item.UpdatedAt),
	}
}

func planItemIssueDescription(item db.PlanItem, spec service.PlanSpec) string {
	var b strings.Builder
	description := strings.TrimSpace(item.Description)
	if description != "" {
		b.WriteString(description)
	}
	appendPlanSpecInheritanceSection(&b, item, spec)
	if normalizePlanItemExecutionKind(item.ExecutionKind) == service.PlanItemExecutionKindHumanConfirmation {
		appendPlanItemTextSection(&b, "Human confirmation question", item.ConfirmationQuestion)
		appendPlanItemTextSection(&b, "Why human confirmation is required", item.ConfirmationReason)
		appendPlanItemSection(&b, "Required evidence", item.RequiredEvidence)
	}
	if reviewContract := service.ReviewGateContract(item.NodeType); reviewContract != "" && !strings.Contains(strings.ToLower(b.String()), "review_gate") {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(reviewContract)
	}
	if item.RequiresGitCommit {
		appendPlanItemTextSection(&b, "Planned branch", item.BranchName)
	} else {
		if branch := strings.TrimSpace(item.IterationBranchName); branch != "" {
			appendPlanItemTextSection(&b, "Inherited implementation branch", branch)
		}
		appendPlanItemTextSection(&b, "Git commit expected", "No")
	}
	appendPlanItemSection(&b, "Acceptance criteria", item.AcceptanceCriteria)
	appendPlanItemUnitTestSection(&b, "Unit test checklist", service.NormalizeUnitTestChecklistJSON(item.UnitTestChecklist))
	appendPlanItemSection(&b, "Suggested test commands", item.SuggestedTestCommands)
	appendPlanItemSection(&b, "Context resources", item.ContextResources)
	appendPlanItemSection(&b, "Risks and notes", item.RiskNotes)
	return strings.TrimSpace(b.String())
}

func appendPlanSpecInheritanceSection(b *strings.Builder, item db.PlanItem, spec service.PlanSpec) {
	spec = service.NormalizePlanSpec(spec)
	if shouldSkipPlanSpecInheritance(item) {
		return
	}
	goal := strings.TrimSpace(spec.Goal)
	summary := strings.TrimSpace(spec.Summary)
	success := firstNNonEmpty(spec.SuccessCriteria, 6)
	decisions := firstNNonEmpty(spec.DesignDecisions, 4)
	outOfScope := firstNNonEmpty(spec.OutOfScope, 4)
	if goal == "" && summary == "" && len(success) == 0 && len(decisions) == 0 && len(outOfScope) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("Plan-level constraints inherited by this issue:\n")
	b.WriteString("- This issue is a slice of the approved plan. Do not weaken the plan goal, success criteria, or design decisions just because this node has a narrower title.\n")
	if goal != "" {
		b.WriteString("- Plan goal: ")
		b.WriteString(goal)
		b.WriteString("\n")
	} else if summary != "" {
		b.WriteString("- Plan summary: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	for _, item := range success {
		b.WriteString("- Success criterion: ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	for _, item := range decisions {
		b.WriteString("- Design decision: ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	for _, item := range outOfScope {
		b.WriteString("- Out of scope: ")
		b.WriteString(item)
		b.WriteString("\n")
	}
}

func shouldSkipPlanSpecInheritance(item db.PlanItem) bool {
	nodeType := service.NormalizePlanItemNodeType(item.NodeType)
	if nodeType == service.PipelineNodeTypeMerge {
		return true
	}
	return normalizePlanItemExecutionKind(item.ExecutionKind) == service.PlanItemExecutionKindHumanConfirmation
}

func firstNNonEmpty(items []string, n int) []string {
	out := make([]string, 0, n)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= n {
			break
		}
	}
	return out
}

func appendPlanItemUnitTestSection(b *strings.Builder, title string, checks []service.UnitTestCheck) {
	checks = service.NormalizeUnitTestChecks(checks)
	if len(checks) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(title)
	b.WriteString(":\n")
	for _, check := range checks {
		b.WriteString("- ")
		if check.Required {
			b.WriteString("[required] ")
		} else {
			b.WriteString("[optional] ")
		}
		if strings.TrimSpace(check.Command) != "" {
			b.WriteString("`")
			b.WriteString(check.Command)
			b.WriteString("`")
		} else {
			b.WriteString(check.Title)
		}
		if expected := strings.TrimSpace(check.Expected); expected != "" {
			b.WriteString(" -> ")
			b.WriteString(expected)
		}
		b.WriteString("\n")
	}
}

func appendPlanItemSection(b *strings.Builder, title string, items []string) {
	items = normalizePlanItemStringList(items)
	if len(items) == 0 {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(title)
	b.WriteString(":\n")
	for _, item := range items {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
}

func appendPlanItemTextSection(b *strings.Builder, title string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(title)
	b.WriteString(":\n")
	b.WriteString(value)
	b.WriteString("\n")
}

func firstLine(s string) string {
	line := normalizePlanText(strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")[0])
	runes := []rune(line)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return line
}
