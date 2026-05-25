package handler

import (
	"encoding/json"
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
			Description:       strOrNullText(planItemIssueDescription(item)),
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
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusBadRequest, "project_id does not refer to a project of this workspace")
		return pgtype.UUID{}, false
	}
	return id, true
}

func (h *Handler) replacePlanItems(r *http.Request, plan db.Plan, reqItems []updatePlanItemRequest) error {
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if err := qtx.DeletePlanItems(r.Context(), plan.ID); err != nil {
		return err
	}
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
		branchName := normalizePlanBranchName(item.BranchName, strings.TrimSpace(item.Title))
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

func planItemIssueDescription(item db.PlanItem) string {
	var b strings.Builder
	description := strings.TrimSpace(item.Description)
	if description != "" {
		b.WriteString(description)
	}
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
		appendPlanItemTextSection(&b, "Git commit expected", "No")
	}
	appendPlanItemSection(&b, "Acceptance criteria", item.AcceptanceCriteria)
	appendPlanItemUnitTestSection(&b, "Unit test checklist", service.NormalizeUnitTestChecklistJSON(item.UnitTestChecklist))
	appendPlanItemSection(&b, "Suggested test commands", item.SuggestedTestCommands)
	appendPlanItemSection(&b, "Context resources", item.ContextResources)
	appendPlanItemSection(&b, "Risks and notes", item.RiskNotes)
	return strings.TrimSpace(b.String())
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
