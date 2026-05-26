package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/mention"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type TaskService struct {
	Queries          *db.Queries
	TxStarter        TxStarter
	Hub              *realtime.Hub
	Bus              *events.Bus
	Analytics        analytics.Client
	Wakeup           TaskWakeupNotifier
	ProjectKnowledge *ProjectKnowledgeService
	// EmptyClaim caches "this runtime has no queued task" so the daemon
	// poll path can skip a Postgres scan on the steady-state empty case.
	// Optional — a nil cache disables the fast path and every claim
	// goes through the DB. Wired in router.go from the shared Redis
	// client.
	EmptyClaim *EmptyClaimCache

	analyticsContextMu    sync.Mutex
	analyticsContextCache map[string]analytics.TaskContext
	analyticsContextOrder []string
}

type TaskWakeupNotifier interface {
	NotifyTaskAvailable(runtimeID, taskID string)
}

// triggerSummaryMaxLen caps the snapshot length so the row stays cheap to
// transmit (it ends up in every task list response). 200 is enough for a
// recognisable preview of a one-paragraph comment.
const triggerSummaryMaxLen = 200

const (
	PipelineNodeTypeIssue      = "issue"
	PipelineNodeTypeManual     = "manual"
	PipelineNodeTypeCheck      = "check"
	PipelineNodeTypeSpecReview = "spec_review"
	PipelineNodeTypeCodeReview = "code_review"
	reviewGateStatusPass       = "pass"
	reviewGateStatusFail       = "fail"
	reviewGateRepairOriginType = "review_gate_repair"
)

type reviewGateOutput struct {
	ReviewGate reviewGateResult `json:"review_gate"`
}

type reviewGateResult struct {
	Status         string              `json:"status"`
	Summary        string              `json:"summary"`
	Findings       []reviewGateFinding `json:"findings"`
	CheckedAgainst []string            `json:"checked_against"`
}

type reviewGateFinding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Details  string `json:"details"`
}

// truncateForSummary returns s shortened to maxRunes, with a trailing
// `…` when truncated. Operates on runes (not bytes) so multibyte characters
// — Chinese / emoji — count as one each. Strips surrounding whitespace
// first so a leading newline doesn't waste budget.
func truncateForSummary(s string, maxRunes int) string {
	// strings.Builder + Grow avoids the O(N²) realloc cycle of `+=` in
	// a loop. Grow uses byte length, which is an upper bound for the
	// rune-equivalent output (replacing \n/\r/\t with space is byte-equal
	// for ASCII whitespace), so we never reallocate.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	rs := []rune(strings.TrimSpace(b.String()))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes]) + "…"
}

const taskAnalyticsContextCacheMax = 4096

// buildCommentTriggerSummary fetches the comment content and truncates
// it for storage on the task row. Returns an invalid pgtype.Text when
// the comment is missing (deleted / wrong workspace / etc) so the column
// stays NULL — front-end falls back to a structural label in that case.
func (s *TaskService) buildCommentTriggerSummary(ctx context.Context, commentID pgtype.UUID) pgtype.Text {
	if !commentID.Valid {
		return pgtype.Text{}
	}
	comment, err := s.Queries.GetComment(ctx, commentID)
	if err != nil {
		return pgtype.Text{}
	}
	summary := truncateForSummary(comment.Content, triggerSummaryMaxLen)
	if summary == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: summary, Valid: true}
}

func NewTaskService(q *db.Queries, tx TxStarter, hub *realtime.Hub, bus *events.Bus, wakeups ...TaskWakeupNotifier) *TaskService {
	var wakeup TaskWakeupNotifier
	if len(wakeups) > 0 {
		wakeup = wakeups[0]
	}
	return &TaskService{Queries: q, TxStarter: tx, Hub: hub, Bus: bus, Wakeup: wakeup}
}

var trivialDoneMarkers = []string{
	"done",
	"готово",
	"готова",
	"сделано",
	"完成",
	"完了",
}

func isTrivialDoneOutput(output string) bool {
	normalized := strings.TrimSpace(strings.ToLower(output))
	normalized = strings.Trim(normalized, ".!！。… ")
	for _, marker := range trivialDoneMarkers {
		if normalized == marker {
			return true
		}
	}
	return false
}

func (s *TaskService) captureTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskEvent(ctx, analytics.AgentTaskQueued(s.taskAnalyticsContext(ctx, task)))
}

func (s *TaskService) captureTaskDispatched(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskEvent(ctx, analytics.AgentTaskDispatched(s.taskAnalyticsContext(ctx, task)))
}

func (s *TaskService) AnalyticsContextForTask(ctx context.Context, task db.AgentTaskQueue) analytics.TaskContext {
	return s.taskAnalyticsContext(ctx, task)
}

func (s *TaskService) captureTaskStarted(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskEvent(ctx, analytics.AgentTaskStarted(s.taskAnalyticsContext(ctx, task)))
}

func (s *TaskService) captureTaskCompleted(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskEvent(ctx, analytics.AgentTaskCompleted(
		s.taskAnalyticsContext(ctx, task),
		taskDurationMS(task),
	))
}

func (s *TaskService) captureTaskFailed(ctx context.Context, task db.AgentTaskQueue) {
	failureReason := taskFailureReason(task)
	s.captureTaskEvent(ctx, analytics.AgentTaskFailed(
		s.taskAnalyticsContext(ctx, task),
		taskDurationMS(task),
		failureReason,
		taskErrorType(failureReason),
		s.willRetryTask(task),
	))
}

func (s *TaskService) captureTaskCancelled(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskEvent(ctx, analytics.AgentTaskCancelled(
		s.taskAnalyticsContext(ctx, task),
		taskDurationMS(task),
	))
}

func (s *TaskService) captureTaskEvent(ctx context.Context, event analytics.Event) {
	if s.Analytics == nil {
		return
	}
	if event.WorkspaceID == "" {
		return
	}
	s.Analytics.Capture(event)
}

func (s *TaskService) cachedTaskAnalyticsContext(task db.AgentTaskQueue) (analytics.TaskContext, bool) {
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return analytics.TaskContext{}, false
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		return analytics.TaskContext{}, false
	}
	tc, ok := s.analyticsContextCache[key]
	return tc, ok
}

func (s *TaskService) storeTaskAnalyticsContext(task db.AgentTaskQueue, tc analytics.TaskContext) {
	if tc.WorkspaceID == "" {
		return
	}
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		s.analyticsContextCache = make(map[string]analytics.TaskContext)
	}
	if _, ok := s.analyticsContextCache[key]; !ok {
		s.analyticsContextOrder = append(s.analyticsContextOrder, key)
		if len(s.analyticsContextOrder) > taskAnalyticsContextCacheMax {
			oldest := s.analyticsContextOrder[0]
			s.analyticsContextOrder = s.analyticsContextOrder[1:]
			delete(s.analyticsContextCache, oldest)
		}
	}
	s.analyticsContextCache[key] = tc
}

func taskAnalyticsContextKey(task db.AgentTaskQueue) string {
	taskID := util.UUIDToString(task.ID)
	if taskID == "" {
		return ""
	}
	return strings.Join([]string{
		taskID,
		util.UUIDToString(task.RuntimeID),
		util.UUIDToString(task.IssueID),
		util.UUIDToString(task.ChatSessionID),
		util.UUIDToString(task.AutopilotRunID),
	}, "|")
}

func (s *TaskService) taskAnalyticsContext(ctx context.Context, task db.AgentTaskQueue) analytics.TaskContext {
	if tc, ok := s.cachedTaskAnalyticsContext(task); ok {
		return tc
	}
	tc := analytics.TaskContext{
		AgentID: util.UUIDToString(task.AgentID),
		TaskID:  util.UUIDToString(task.ID),
		Source:  analytics.SourceManual,
	}
	if task.IssueID.Valid {
		tc.IssueID = util.UUIDToString(task.IssueID)
	}
	if task.ChatSessionID.Valid {
		tc.ChatSessionID = util.UUIDToString(task.ChatSessionID)
		tc.Source = analytics.SourceChat
	}
	if task.AutopilotRunID.Valid {
		tc.AutopilotRunID = util.UUIDToString(task.AutopilotRunID)
		tc.Source = analytics.SourceAutopilot
	}

	if task.RuntimeID.Valid {
		if rt, err := s.Queries.GetAgentRuntime(ctx, task.RuntimeID); err == nil {
			tc.WorkspaceID = util.UUIDToString(rt.WorkspaceID)
			tc.RuntimeMode = rt.RuntimeMode
			tc.Provider = rt.Provider
		}
	}
	if tc.WorkspaceID == "" || tc.RuntimeMode == "" {
		if agent, err := s.Queries.GetAgent(ctx, task.AgentID); err == nil {
			if tc.WorkspaceID == "" {
				tc.WorkspaceID = util.UUIDToString(agent.WorkspaceID)
			}
			if tc.RuntimeMode == "" {
				tc.RuntimeMode = agent.RuntimeMode
			}
		}
	}

	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			tc.WorkspaceID = util.UUIDToString(issue.WorkspaceID)
			if issue.CreatorType == "member" {
				tc.UserID = util.UUIDToString(issue.CreatorID)
			}
			if issue.OriginType.Valid {
				switch issue.OriginType.String {
				case "autopilot":
					tc.Source = analytics.SourceAutopilot
					if ap, err := s.Queries.GetAutopilot(ctx, issue.OriginID); err == nil {
						if ap.CreatedByType == "member" {
							tc.UserID = util.UUIDToString(ap.CreatedByID)
						}
					}
				case "quick_create":
					tc.Source = analytics.SourceManual
				}
			}
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			tc.WorkspaceID = util.UUIDToString(cs.WorkspaceID)
			tc.UserID = util.UUIDToString(cs.CreatorID)
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := s.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := s.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil {
				tc.WorkspaceID = util.UUIDToString(ap.WorkspaceID)
				if ap.CreatedByType == "member" {
					tc.UserID = util.UUIDToString(ap.CreatedByID)
				}
			}
		}
	}
	if qc, ok := s.parseQuickCreateContext(task); ok {
		tc.WorkspaceID = qc.WorkspaceID
		tc.UserID = qc.RequesterID
		tc.Source = analytics.SourceManual
	}
	if ip, ok := s.parseIssuePlanContext(task); ok {
		tc.WorkspaceID = ip.WorkspaceID
		tc.UserID = ip.RequesterID
		tc.Source = analytics.SourceManual
	}
	s.storeTaskAnalyticsContext(task, tc)
	return tc
}

func taskDurationMS(task db.AgentTaskQueue) int64 {
	if !task.CompletedAt.Valid {
		return 0
	}
	start := task.CreatedAt
	if task.StartedAt.Valid {
		start = task.StartedAt
	} else if task.DispatchedAt.Valid {
		start = task.DispatchedAt
	}
	if !start.Valid {
		return 0
	}
	ms := task.CompletedAt.Time.Sub(start.Time).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func taskFailureReason(task db.AgentTaskQueue) string {
	if task.FailureReason.Valid && task.FailureReason.String != "" {
		return task.FailureReason.String
	}
	return "agent_error"
}

func taskErrorType(reason string) string {
	switch reason {
	case "runtime_offline", "runtime_recovery":
		return "runtime"
	case "timeout", "codex_semantic_inactivity":
		return "timeout"
	case "iteration_limit", "agent_fallback_message":
		return "agent_output"
	case "cancelled", "user_cancelled":
		return "cancelled"
	default:
		return "agent_error"
	}
}

func (s *TaskService) willRetryTask(task db.AgentTaskQueue) bool {
	reason := taskFailureReason(task)
	if !retryableReasons[reason] {
		return false
	}
	if task.Attempt >= task.MaxAttempts {
		return false
	}
	if task.AutopilotRunID.Valid {
		return false
	}
	return task.IssueID.Valid || task.ChatSessionID.Valid
}

// EnqueueTaskForIssue creates a queued task for an agent-assigned issue.
// No context snapshot is stored — the agent fetches all data it needs at
// runtime via the multica CLI.
func (s *TaskService) EnqueueTaskForIssue(ctx context.Context, issue db.Issue, triggerCommentID ...pgtype.UUID) (db.AgentTaskQueue, error) {
	var commentID pgtype.UUID
	if len(triggerCommentID) > 0 {
		commentID = triggerCommentID[0]
	}
	return s.enqueueIssueTask(ctx, issue, commentID, false)
}

// enqueueIssueTask is the shared implementation behind EnqueueTaskForIssue
// and the manual rerun path. forceFreshSession=true marks the task so the
// daemon claim handler skips the (agent_id, issue_id) resume lookup — the
// user already judged the prior output bad, a fresh agent session is the
// expected behavior.
func (s *TaskService) enqueueIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, forceFreshSession bool) (db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "issue has no assignee")
		return db.AgentTaskQueue{}, fmt.Errorf("issue has no assignee")
	}

	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "agent has no runtime")
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           issue.AssigneeID,
		RuntimeID:         agent.RuntimeID,
		IssueID:           issue.ID,
		Priority:          priorityToInt(issue.Priority),
		TriggerCommentID:  triggerCommentID,
		TriggerSummary:    s.buildCommentTriggerSummary(ctx, triggerCommentID),
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
	})
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issue.ID),
		"agent_id", util.UUIDToString(issue.AssigneeID),
		"force_fresh_session", forceFreshSession,
	)
	// Order matters: broadcast first, notify daemon second. notifyTaskAvailable
	// kicks an in-process channel that the daemon picks up over HTTP and
	// claims; the claim path then emits its own task:dispatch. Doing the
	// queued broadcast afterwards risks the dispatch event reaching clients
	// before the queued one (rare but unsafe-by-construction). Publishing
	// in the desired observe-order makes correctness independent of timing.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// EnqueueTaskForMention creates a queued task for a mentioned agent on an issue.
// Unlike EnqueueTaskForIssue, this takes an explicit agent ID rather than
// deriving it from the issue assignee.
func (s *TaskService) EnqueueTaskForMention(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, false)
}

func (s *TaskService) enqueueMentionTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, forceFreshSession bool) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("mention task enqueue failed: agent not found", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("mention task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("mention task enqueue failed: agent has no runtime", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           agentID,
		RuntimeID:         agent.RuntimeID,
		IssueID:           issue.ID,
		Priority:          priorityToInt(issue.Priority),
		TriggerCommentID:  triggerCommentID,
		TriggerSummary:    s.buildCommentTriggerSummary(ctx, triggerCommentID),
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
	})
	if err != nil {
		slog.Error("mention task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("mention task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// QuickCreateContext is the JSON payload stored on a quick-create task's
// context column. The daemon detects this variant via Type == "quick_create"
// and switches to the quick-create prompt template; the completion path
// uses RequesterID + WorkspaceID to write the inbox notification.
//
// ProjectID is the optional project the user picked in the modal. When
// non-empty the daemon claim handler resolves the project's title +
// resources, and the prompt template instructs the agent to pass
// `--project <uuid>` so the new issue lands in that project.
type QuickCreateContext struct {
	Type        string `json:"type"`
	Prompt      string `json:"prompt"`
	RequesterID string `json:"requester_id"`
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
}

// QuickCreateContextType marks a task as a quick-create job.
const QuickCreateContextType = "quick_create"

// IssuePlanContext is the JSON payload stored on a planner task. The task has
// no issue link: it produces a structured plan that the server validates and
// writes into plan / plan_item rows.
type IssuePlanContext struct {
	Type          string   `json:"type"`
	PlanID        string   `json:"plan_id,omitempty"`
	SourceIssueID string   `json:"source_issue_id,omitempty"`
	Phase         string   `json:"phase,omitempty"`
	Prompt        string   `json:"prompt"`
	RequesterID   string   `json:"requester_id"`
	WorkspaceID   string   `json:"workspace_id"`
	ProjectID     string   `json:"project_id,omitempty"`
	Spec          PlanSpec `json:"spec,omitempty"`
}

const IssuePlanContextType = "issue_plan"
const IssuePlanPhaseSpec = "spec"
const IssuePlanPhaseItems = "items"

const PlanItemExecutionKindAgentTask = "agent_task"
const PlanItemExecutionKindHumanConfirmation = "human_confirmation"
const maxPlanSpecOpenQuestions = 2

type PlanSpec struct {
	Summary              string                   `json:"summary"`
	Goal                 string                   `json:"goal"`
	SuccessCriteria      []string                 `json:"success_criteria"`
	AcceptanceScenarios  []PlanAcceptanceScenario `json:"acceptance_scenarios"`
	InScope              []string                 `json:"in_scope"`
	OutOfScope           []string                 `json:"out_of_scope"`
	Approach             string                   `json:"approach"`
	DesignDecisions      []string                 `json:"design_decisions"`
	VerificationCommands []string                 `json:"verification_commands"`
	Assumptions          []string                 `json:"assumptions"`
	OpenQuestions        []string                 `json:"open_questions"`
	Clarifications       []PlanClarification      `json:"clarifications,omitempty"`
}

type PlanAcceptanceScenario struct {
	Name  string `json:"name"`
	Given string `json:"given"`
	When  string `json:"when"`
	Then  string `json:"then"`
}

type PlanClarification struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type issuePlanResult struct {
	NeedsPlan    *bool                 `json:"needs_plan"`
	Reason       string                `json:"reason"`
	Spec         PlanSpec              `json:"spec"`
	DirectIssue  issuePlanResultItem   `json:"direct_issue"`
	Title        string                `json:"title"`
	ParentIssue  issuePlanParent       `json:"parent_issue"`
	Items        []issuePlanResultItem `json:"items"`
	PipelineID   string                `json:"pipeline_id"`
	PipelineName string                `json:"pipeline_name"`
	Pipeline     issuePlanPipeline     `json:"pipeline"`
}

type issuePlanParent struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type issuePlanResultItem struct {
	Title                 string          `json:"title"`
	Description           string          `json:"description"`
	AcceptanceCriteria    []string        `json:"acceptance_criteria"`
	SuggestedTestCommands []string        `json:"suggested_test_commands"`
	UnitTestChecklist     []UnitTestCheck `json:"unit_test_checklist"`
	ContextResources      []string        `json:"context_resources"`
	RiskNotes             []string        `json:"risk_notes"`
	NodeType              string          `json:"node_type"`
	ExecutionKind         string          `json:"execution_kind"`
	ConfirmationQuestion  string          `json:"confirmation_question"`
	ConfirmationReason    string          `json:"confirmation_reason"`
	RequiredEvidence      []string        `json:"required_evidence"`
	RequiresGitCommit     *bool           `json:"requires_git_commit"`
	BranchName            string          `json:"branch_name"`
	IterationIndex        int32           `json:"iteration_index"`
	IterationTitle        string          `json:"iteration_title"`
	IterationBranchName   string          `json:"iteration_branch_name"`
	RecommendedAgentID    string          `json:"recommended_agent_id"`
	MatchScore            int32           `json:"match_score"`
	MatchReason           string          `json:"match_reason"`
	MissingCapability     string          `json:"missing_capability"`
	DependsOnPositions    []int32         `json:"depends_on_positions"`
	Selected              *bool           `json:"selected"`
}

type issuePlanPipeline struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	ParentIssue issuePlanParent         `json:"parent_issue"`
	Nodes       []issuePlanPipelineNode `json:"nodes"`
}

type issuePlanPipelineNode struct {
	Key                   string          `json:"key"`
	Type                  string          `json:"type"`
	NodeType              string          `json:"node_type"`
	Title                 string          `json:"title"`
	Description           string          `json:"description"`
	AcceptanceCriteria    []string        `json:"acceptance_criteria"`
	SuggestedTestCommands []string        `json:"suggested_test_commands"`
	UnitTestChecklist     []UnitTestCheck `json:"unit_test_checklist"`
	ContextResources      []string        `json:"context_resources"`
	RiskNotes             []string        `json:"risk_notes"`
	ExecutionKind         string          `json:"execution_kind"`
	ConfirmationQuestion  string          `json:"confirmation_question"`
	ConfirmationReason    string          `json:"confirmation_reason"`
	RequiredEvidence      []string        `json:"required_evidence"`
	RequiresGitCommit     *bool           `json:"requires_git_commit"`
	BranchName            string          `json:"branch_name"`
	IterationIndex        int32           `json:"iteration_index"`
	IterationTitle        string          `json:"iteration_title"`
	IterationBranchName   string          `json:"iteration_branch_name"`
	AgentID               string          `json:"agent_id"`
	RepoKeys              []string        `json:"repo_keys"`
	DependsOnNodeKeys     []string        `json:"depends_on_node_keys"`
	Selected              *bool           `json:"selected"`
}

func (r issuePlanResult) shouldCreatePlan() bool {
	return r.NeedsPlan == nil || *r.NeedsPlan
}

func (r issuePlanResult) directIssue() (issuePlanResultItem, bool) {
	if strings.TrimSpace(r.DirectIssue.Title) != "" ||
		strings.TrimSpace(r.DirectIssue.Description) != "" ||
		strings.TrimSpace(r.DirectIssue.RecommendedAgentID) != "" {
		return r.DirectIssue, true
	}
	if !r.shouldCreatePlan() && len(r.Items) == 1 {
		item := r.Items[0]
		if strings.TrimSpace(item.Title) != "" ||
			strings.TrimSpace(item.Description) != "" ||
			strings.TrimSpace(item.RecommendedAgentID) != "" {
			return item, true
		}
	}
	return issuePlanResultItem{}, false
}

func (r issuePlanResult) hasPipelineSelection() bool {
	return strings.TrimSpace(r.PipelineID) != "" ||
		strings.TrimSpace(r.Pipeline.ID) != "" ||
		strings.TrimSpace(r.PipelineName) != "" ||
		strings.TrimSpace(r.Pipeline.Name) != ""
}

func normalizeIssuePlanPhase(phase string) string {
	phase = strings.TrimSpace(phase)
	if phase == IssuePlanPhaseSpec || phase == IssuePlanPhaseItems {
		return phase
	}
	return IssuePlanPhaseItems
}

func normalizePlanSpec(spec PlanSpec) PlanSpec {
	spec.Summary = strings.TrimSpace(spec.Summary)
	spec.Goal = strings.TrimSpace(spec.Goal)
	spec.SuccessCriteria = normalizeSpecStringList(spec.SuccessCriteria)
	spec.AcceptanceScenarios = normalizePlanAcceptanceScenarios(spec.AcceptanceScenarios)
	spec.InScope = normalizeSpecStringList(spec.InScope)
	spec.OutOfScope = normalizeSpecStringList(spec.OutOfScope)
	spec.Approach = strings.TrimSpace(spec.Approach)
	spec.DesignDecisions = normalizeSpecStringList(spec.DesignDecisions)
	spec.VerificationCommands = normalizeSpecStringList(spec.VerificationCommands)
	spec.Assumptions = normalizeSpecStringList(spec.Assumptions)
	spec.OpenQuestions = normalizeSpecStringList(spec.OpenQuestions)
	if len(spec.OpenQuestions) > maxPlanSpecOpenQuestions {
		spec.Assumptions = normalizeSpecStringList(append(spec.Assumptions, spec.OpenQuestions[maxPlanSpecOpenQuestions:]...))
		spec.OpenQuestions = spec.OpenQuestions[:maxPlanSpecOpenQuestions]
	}
	spec.Clarifications = normalizePlanClarifications(spec.Clarifications)
	return spec
}

func normalizePlanAcceptanceScenarios(in []PlanAcceptanceScenario) []PlanAcceptanceScenario {
	if len(in) == 0 {
		return []PlanAcceptanceScenario{}
	}
	out := make([]PlanAcceptanceScenario, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, item := range in {
		normalized := PlanAcceptanceScenario{
			Name:  strings.TrimSpace(item.Name),
			Given: strings.TrimSpace(item.Given),
			When:  strings.TrimSpace(item.When),
			Then:  strings.TrimSpace(item.Then),
		}
		if normalized.Name == "" && normalized.Given == "" && normalized.When == "" && normalized.Then == "" {
			continue
		}
		key := strings.ToLower(normalized.Name + "\x00" + normalized.Given + "\x00" + normalized.When + "\x00" + normalized.Then)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, normalized)
	}
	return out
}

func NormalizePlanSpec(spec PlanSpec) PlanSpec {
	return normalizePlanSpec(spec)
}

func normalizeSpecStringList(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func normalizePlanClarifications(in []PlanClarification) []PlanClarification {
	if len(in) == 0 {
		return []PlanClarification{}
	}
	out := make([]PlanClarification, 0, len(in))
	seen := make(map[string]int, len(in))
	for _, item := range in {
		question := strings.TrimSpace(item.Question)
		answer := strings.TrimSpace(item.Answer)
		if question == "" || answer == "" {
			continue
		}
		normalized := PlanClarification{Question: question, Answer: answer}
		key := strings.ToLower(question)
		if idx, ok := seen[key]; ok {
			out[idx] = normalized
			continue
		}
		seen[key] = len(out)
		out = append(out, normalized)
	}
	return out
}

func hasPlanSpecContext(spec PlanSpec) bool {
	spec = normalizePlanSpec(spec)
	return spec.Summary != "" ||
		spec.Goal != "" ||
		len(spec.SuccessCriteria) > 0 ||
		len(spec.AcceptanceScenarios) > 0 ||
		len(spec.InScope) > 0 ||
		len(spec.OutOfScope) > 0 ||
		spec.Approach != "" ||
		len(spec.DesignDecisions) > 0 ||
		len(spec.VerificationCommands) > 0 ||
		len(spec.Assumptions) > 0 ||
		len(spec.OpenQuestions) > 0 ||
		len(spec.Clarifications) > 0
}

func marshalPlanSpec(spec PlanSpec) ([]byte, error) {
	spec = normalizePlanSpec(spec)
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func MarshalPlanSpec(spec PlanSpec) ([]byte, error) {
	return marshalPlanSpec(spec)
}

// EnqueueQuickCreateTask creates a queued task that has no issue / chat /
// autopilot link — the user's natural-language prompt is stored in the
// task's context JSONB and the agent is expected to translate it into a
// `multica issue create` call. Pre-validates that the agent is reachable
// (not archived, has a runtime) so the API can reject up-front rather than
// queue a task no one will ever claim.
//
// projectID is optional (zero-valued pgtype.UUID when the user didn't pick
// one). The handler is responsible for validating it belongs to the same
// workspace before passing it in.
func (s *TaskService) EnqueueQuickCreateTask(ctx context.Context, workspaceID, requesterID pgtype.UUID, agentID pgtype.UUID, prompt string, projectID pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	payload := QuickCreateContext{
		Type:        QuickCreateContextType,
		Prompt:      prompt,
		RequesterID: util.UUIDToString(requesterID),
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if projectID.Valid {
		payload.ProjectID = util.UUIDToString(projectID)
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("marshal quick-create context: %w", err)
	}

	task, err := s.Queries.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{
		AgentID:   agentID,
		RuntimeID: agent.RuntimeID,
		Priority:  priorityToInt("high"),
		Context:   contextJSON,
	})
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("create quick-create task: %w", err)
	}

	slog.Info("quick-create task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"agent_id", util.UUIDToString(agentID),
		"requester_id", util.UUIDToString(requesterID),
		"workspace_id", util.UUIDToString(workspaceID),
		"project_id", payload.ProjectID,
	)
	// Match every other Enqueue* path: kick the daemon WS so the task
	// gets claimed promptly instead of waiting for the next 30 s poll
	// cycle. Without this the user perceives "quick create never
	// triggered" because the modal closes immediately and the task
	// sits in 'queued' until the next sleepWithContextOrWakeup tick.
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

func (s *TaskService) EnqueueIssuePlanTask(ctx context.Context, workspaceID, requesterID, planID pgtype.UUID, agentID pgtype.UUID, prompt string, projectID pgtype.UUID, phase string, spec PlanSpec) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	payload := IssuePlanContext{
		Type:        IssuePlanContextType,
		PlanID:      util.UUIDToString(planID),
		Phase:       normalizeIssuePlanPhase(phase),
		Prompt:      prompt,
		RequesterID: util.UUIDToString(requesterID),
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if projectID.Valid {
		payload.ProjectID = util.UUIDToString(projectID)
	}
	if payload.Phase == IssuePlanPhaseItems || hasPlanSpecContext(spec) {
		payload.Spec = normalizePlanSpec(spec)
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("marshal issue-plan context: %w", err)
	}

	task, err := s.Queries.CreateContextTask(ctx, db.CreateContextTaskParams{
		AgentID:           agentID,
		RuntimeID:         agent.RuntimeID,
		Priority:          priorityToInt("high"),
		Context:           contextJSON,
		ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("create issue-plan task: %w", err)
	}

	slog.Info("issue-plan task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"plan_id", payload.PlanID,
		"agent_id", util.UUIDToString(agentID),
		"requester_id", util.UUIDToString(requesterID),
		"workspace_id", util.UUIDToString(workspaceID),
		"project_id", payload.ProjectID,
		"phase", payload.Phase,
	)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

func (s *TaskService) EnqueuePlannerIssueTask(ctx context.Context, issue db.Issue, plannerAgent db.Agent, requesterID pgtype.UUID) (db.AgentTaskQueue, db.Plan, error) {
	if plannerAgent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, db.Plan{}, fmt.Errorf("agent is archived")
	}
	if !plannerAgent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, db.Plan{}, fmt.Errorf("agent has no runtime")
	}
	prompt := strings.TrimSpace(issue.Title)
	if issue.Description.Valid && strings.TrimSpace(issue.Description.String) != "" {
		prompt = prompt + "\n\n" + strings.TrimSpace(issue.Description.String)
	}
	var task db.AgentTaskQueue
	var plan db.Plan
	payload := IssuePlanContext{
		Type:          IssuePlanContextType,
		SourceIssueID: util.UUIDToString(issue.ID),
		Phase:         IssuePlanPhaseSpec,
		Prompt:        prompt,
		RequesterID:   util.UUIDToString(requesterID),
		WorkspaceID:   util.UUIDToString(issue.WorkspaceID),
	}
	if issue.ProjectID.Valid {
		payload.ProjectID = util.UUIDToString(issue.ProjectID)
	}
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		parentDescription := pgtype.Text{}
		if issue.Description.Valid {
			parentDescription = serviceStrOrNullText(issue.Description.String)
		}
		var err error
		plan, err = qtx.CreatePlanForIssue(ctx, db.CreatePlanForIssueParams{
			WorkspaceID:       issue.WorkspaceID,
			Title:             issue.Title,
			Prompt:            prompt,
			PlannerAgentID:    plannerAgent.ID,
			ParentTitle:       pgtype.Text{String: issue.Title, Valid: true},
			ParentDescription: parentDescription,
			ParentIssueID:     issue.ID,
			ProjectID:         issue.ProjectID,
			CreatedBy:         requesterID,
		})
		if err != nil {
			return fmt.Errorf("create issue plan: %w", err)
		}
		payload.PlanID = util.UUIDToString(plan.ID)
		contextJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal planner issue context: %w", err)
		}
		task, err = qtx.CreateContextTask(ctx, db.CreateContextTaskParams{
			AgentID:           plannerAgent.ID,
			RuntimeID:         plannerAgent.RuntimeID,
			Priority:          priorityToInt(issue.Priority),
			Context:           contextJSON,
			ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("create planner issue task: %w", err)
		}
		plan, err = qtx.SetPlanTask(ctx, db.SetPlanTaskParams{ID: plan.ID, TaskID: task.ID})
		if err != nil {
			return fmt.Errorf("link planner issue plan task: %w", err)
		}
		return nil
	}); err != nil {
		return db.AgentTaskQueue{}, db.Plan{}, err
	}
	slog.Info("planner issue task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"plan_id", util.UUIDToString(plan.ID),
		"source_issue_id", payload.SourceIssueID,
		"agent_id", util.UUIDToString(plannerAgent.ID),
		"requester_id", payload.RequesterID,
		"workspace_id", payload.WorkspaceID,
		"project_id", payload.ProjectID,
	)
	s.NotifyTaskEnqueued(ctx, task)
	return task, plan, nil
}

// EnqueueChatTask creates a queued task for a chat session.
// Unlike issue tasks, chat tasks have no issue_id.
func (s *TaskService) EnqueueChatTask(ctx context.Context, chatSession db.ChatSession) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, chatSession.AgentID)
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	task, err := s.Queries.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:       chatSession.AgentID,
		RuntimeID:     agent.RuntimeID,
		Priority:      2, // medium priority for chat
		ChatSessionID: chatSession.ID,
	})
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create chat task: %w", err)
	}

	slog.Info("chat task enqueued", "task_id", util.UUIDToString(task.ID), "chat_session_id", util.UUIDToString(chatSession.ID), "agent_id", util.UUIDToString(chatSession.AgentID))
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// CancelTasksForIssue cancels every active task on the issue, reconciles each
// affected agent's status, and broadcasts task:cancelled events so frontends
// clear their live cards.
//
// Before #1587 this path was "cancel rows and return" — issue-status flips
// (e.g. user marks the issue `done` or `cancelled` while a task is still
// running) left the agent stuck at status="working" indefinitely, requiring a
// manual `multica agent update <id> --status idle` to unwedge. Matches the
// pattern already used by CancelTask and RerunIssue.
func (s *TaskService) CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// CancelTasksForAgent cancels every active task belonging to an agent
// (queued + dispatched + running), reconciles the agent's status, and
// broadcasts task:cancelled events. Used by the agent-level "Cancel all
// tasks" action — same shape as CancelTasksForIssue but scoped on agent_id.
//
// Returns the cancelled rows so callers can report counts / log them.
func (s *TaskService) CancelTasksForAgent(ctx context.Context, agentID pgtype.UUID) ([]db.AgentTaskQueue, error) {
	cancelled, err := s.Queries.CancelAgentTasksByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once after the loop — agent transitions from
	// working→available based on remaining task counts, no need to call
	// per row (the rows we just cancelled all belong to the same agent).
	s.ReconcileAgentStatus(ctx, agentID)
	return cancelled, nil
}

// CancelTasksByTriggerComment cancels active tasks whose trigger is the given
// comment. Called from DeleteComment so an agent does not run with the
// now-deleted content already embedded in its prompt. Must be invoked BEFORE
// the comment row is deleted because the FK ON DELETE SET NULL would
// otherwise nullify trigger_comment_id and we'd lose the ability to find
// the affected tasks.
func (s *TaskService) CancelTasksByTriggerComment(ctx context.Context, commentID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByTriggerComment(ctx, commentID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// BroadcastCancelledTasks reconciles each affected agent's status and emits
// task:cancelled for every row. Callers must invoke this AFTER committing the
// cancellation so subscribers don't observe a "cancelled" event for a row
// that the tx might still roll back.
func (s *TaskService) BroadcastCancelledTasks(ctx context.Context, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
}

func (s *TaskService) CaptureCancelledTasks(ctx context.Context, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
	}
}

// CancelTask cancels a single task by ID. It broadcasts a task:cancelled event
// so frontends can update immediately.
func (s *TaskService) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.CancelAgentTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("cancel task: %w", err)
		}
		return &existing, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("task cancelled", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCancelled(ctx, task)

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast cancellation as a task:failed event so frontends clear the live card
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)

	return &task, nil
}

// ClaimTask atomically claims the next queued task for an agent,
// respecting max_concurrent_tasks.
func (s *TaskService) ClaimTask(ctx context.Context, agentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome                                                              = "unknown"
		getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64
	)
	defer func() {
		s.maybeLogClaimSlow(agentID, outcome, start, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs)
	}()

	t0 := start
	agent, err := s.Queries.GetAgent(ctx, agentID)
	getAgentMs = time.Since(t0).Milliseconds()
	if err != nil {
		outcome = "error_get_agent"
		return nil, fmt.Errorf("agent not found: %w", err)
	}

	t0 = time.Now()
	running, err := s.Queries.CountRunningTasks(ctx, agentID)
	countRunningMs = time.Since(t0).Milliseconds()
	if err != nil {
		outcome = "error_count_running"
		return nil, fmt.Errorf("count running tasks: %w", err)
	}
	if running >= int64(agent.MaxConcurrentTasks) {
		slog.Debug("task claim: no capacity", "agent_id", util.UUIDToString(agentID), "running", running, "max", agent.MaxConcurrentTasks)
		outcome = "no_capacity"
		return nil, nil // No capacity
	}

	t0 = time.Now()
	task, err := s.Queries.ClaimAgentTask(ctx, agentID)
	claimAgentMs = time.Since(t0).Milliseconds()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Debug("task claim: no tasks available", "agent_id", util.UUIDToString(agentID))
			outcome = "no_tasks"
			return nil, nil // No tasks available
		}
		outcome = "error_claim"
		return nil, fmt.Errorf("claim task: %w", err)
	}

	slog.Info("task claimed", "task_id", util.UUIDToString(task.ID), "agent_id", util.UUIDToString(agentID))
	s.captureTaskDispatched(ctx, task)

	// Refresh agent status from active tasks. This avoids a stale unconditional
	// working write racing after a just-cancelled claim.
	t0 = time.Now()
	s.ReconcileAgentStatus(ctx, agentID)
	updateStatusMs = time.Since(t0).Milliseconds()

	// Broadcast task:dispatch. ResolveTaskWorkspaceID inside this path can
	// re-query issue/chat_session/autopilot_run, so it can also be a real
	// contributor to claim latency.
	t0 = time.Now()
	s.broadcastTaskDispatch(ctx, task)
	dispatchMs = time.Since(t0).Milliseconds()

	outcome = "claimed"
	return &task, nil
}

// ClaimTaskForRuntime claims the next runnable task for a runtime while
// still respecting each agent's max_concurrent_tasks limit.
//
// Empty-claim fast path: when EmptyClaim is configured and a recent
// check verified the runtime had no queued tasks, returns immediately
// without touching Postgres. The cache is invalidated synchronously on
// every enqueue (notifyTaskAvailable), so a queued task becomes
// claimable on the next call rather than waiting for the TTL.
func (s *TaskService) ClaimTaskForRuntime(ctx context.Context, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome          = "no_task"
		listMs, loopMs   int64
		listCount, tried int
		claimedFlag      bool
	)
	defer func() {
		totalMs := time.Since(start).Milliseconds()
		if totalMs < 300 {
			return
		}
		slog.Info("claim_for_runtime slow",
			"runtime_id", util.UUIDToString(runtimeID),
			"outcome", outcome,
			"total_ms", totalMs,
			"list_pending_ms", listMs,
			"list_pending_count", listCount,
			"agents_tried", tried,
			"claim_loop_ms", loopMs,
			"claimed", claimedFlag,
		)
	}()

	runtimeKey := util.UUIDToString(runtimeID)
	if s.EmptyClaim.IsEmpty(ctx, runtimeKey) {
		outcome = "empty_cache_hit"
		return nil, nil
	}

	// Sample the invalidation version BEFORE the SELECT. If a
	// concurrent enqueue Bumps between this read and the post-SELECT
	// MarkEmpty, the next IsEmpty will see the empty key tagged with
	// a stale version and reject it — closing the race that would
	// otherwise stall the just-queued task until the empty key's TTL
	// expired.
	preSelectVersion := s.EmptyClaim.CurrentVersion(ctx, runtimeKey)

	t0 := time.Now()
	tasks, err := s.Queries.ListQueuedClaimCandidatesByRuntime(ctx, runtimeID)
	listMs = time.Since(t0).Milliseconds()
	listCount = len(tasks)
	if err != nil {
		outcome = "error_list"
		return nil, fmt.Errorf("list queued claim candidates: %w", err)
	}

	if len(tasks) == 0 {
		s.EmptyClaim.MarkEmpty(ctx, runtimeKey, preSelectVersion)
		outcome = "empty_db"
		return nil, nil
	}

	loopStart := time.Now()
	triedAgents := map[string]struct{}{}
	var claimed *db.AgentTaskQueue
	for _, candidate := range tasks {
		agentKey := util.UUIDToString(candidate.AgentID)
		if _, seen := triedAgents[agentKey]; seen {
			continue
		}
		triedAgents[agentKey] = struct{}{}
		tried++

		task, err := s.ClaimTask(ctx, candidate.AgentID)
		if err != nil {
			loopMs = time.Since(loopStart).Milliseconds()
			outcome = "error_claim"
			return nil, err
		}
		if task != nil && task.RuntimeID == runtimeID {
			claimed = task
			break
		}
	}
	loopMs = time.Since(loopStart).Milliseconds()
	if claimed != nil {
		claimedFlag = true
		outcome = "claimed"
	}

	return claimed, nil
}

// maybeLogClaimSlow emits one structured log per ClaimTask call when its total
// latency exceeds 300ms, so the prod tail can be diagnosed without flooding
// logs at normal poll rates. Called via defer so it captures the full path
// including post-claim updateAgentStatus / broadcastTaskDispatch (both of
// which can hit the DB) and any error exit.
func (s *TaskService) maybeLogClaimSlow(agentID pgtype.UUID, outcome string, start time.Time, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 300 {
		return
	}
	slog.Info("claim_task slow",
		"agent_id", util.UUIDToString(agentID),
		"outcome", outcome,
		"total_ms", totalMs,
		"get_agent_ms", getAgentMs,
		"count_running_ms", countRunningMs,
		"claim_agent_ms", claimAgentMs,
		"update_status_ms", updateStatusMs,
		"dispatch_ms", dispatchMs,
	)
}

// StartTask transitions a dispatched task to running.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) StartTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.StartAgentTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("start task: %w", err)
	}

	slog.Info("task started", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskStarted(ctx, task)
	return &task, nil
}

// CompleteTask marks a task as completed.
// Issue status is generally managed by the agent via the CLI. Pipeline review
// gates are the exception: their final JSON decision moves the gate issue to
// done or blocked so dependency gating can continue.
//
// For chat tasks, CompleteAgentTask and the chat_session resume-pointer
// update run in a single transaction. This closes a race where the next
// queued chat message could be claimed in the window between the task
// flipping to 'completed' and chat_session.session_id being refreshed,
// causing the new task to resume against a stale (or NULL) session.
func (s *TaskService) CompleteTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.CompleteAgentTask(ctx, db.CompleteAgentTaskParams{
			ID:        taskID,
			Result:    result,
			SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		if t.ChatSessionID.Valid {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			// COALESCE in SQL guarantees empty inputs don't wipe the
			// existing resume pointer; we still surface DB errors.
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		// When parallel agents race, a task may already be completed,
		// cancelled, or failed by the time this call runs. The UPDATE
		// … WHERE status = 'running' returns no rows in that case.
		// Treat it as an idempotent success — same pattern as CancelTask.
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("complete task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("complete task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("complete task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("complete task: %w", err)
	}

	slog.Info("task completed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCompleted(ctx, task)
	if s.ProjectKnowledge != nil {
		s.ProjectKnowledge.UpdateRetrievalOutcomeForTask(ctx, task.ID, "completed")
		s.ProjectKnowledge.CaptureTaskCompleted(ctx, task, result)
	}

	reviewGateHandled := false
	unitTestHandled := false
	if task.IssueID.Valid {
		reviewGateHandled = s.applyReviewGateCompleted(ctx, task, result)
		if !reviewGateHandled {
			unitTestHandled = s.applyUnitTestChecklistCompleted(ctx, task, result)
			if !unitTestHandled && !s.applyReviewGateRepairTaskCompleted(ctx, task) {
				s.applyPlanAgentTaskCompleted(ctx, task)
			}
		}
	}

	// Invariant: every completed issue task must have at least one agent
	// comment on the issue, so the user always sees something when a run
	// ends. If the agent posted a comment during execution (result, progress
	// ping, or CLI reply), HasAgentCommentedSince returns true and we skip.
	// Otherwise, synthesize one from the final output. For comment-triggered
	// tasks, TriggerCommentID threads the fallback under the original comment;
	// for assignment-triggered tasks it is NULL and the fallback is top-level.
	// Chat tasks have no IssueID and are handled separately below.
	if task.IssueID.Valid && !reviewGateHandled && !unitTestHandled {
		agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
			IssueID:  task.IssueID,
			AuthorID: task.AgentID,
			Since:    task.StartedAt,
		})
		if !agentCommented {
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil {
				if payload.Output != "" {
					// Match the CLI's --content / --description behavior: agents that
					// emit literal `\n` 4-char sequences (Python/JSON-style) get them
					// decoded into real newlines before the comment hits the DB. See
					// util.UnescapeBackslashEscapes for the exact contract.
					body := util.UnescapeBackslashEscapes(payload.Output)
					if task.TriggerCommentID.Valid && isTrivialDoneOutput(body) {
						slog.Warn("suppressing trivial comment-trigger fallback output",
							"task_id", util.UUIDToString(task.ID),
							"issue_id", util.UUIDToString(task.IssueID),
							"agent_id", util.UUIDToString(task.AgentID),
						)
					} else {
						s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(body), "comment", task.TriggerCommentID)
					}
				}
			}
		}
	}

	// Quick-create tasks: locate the issue the agent just created and push
	// an inbox confirmation to the requester. The agent has no issue / chat
	// link, so the regular completion paths above don't apply. We find the
	// new issue by querying for the most recent issue this agent created in
	// the requester's workspace since the task started — more robust than
	// parsing the agent's stdout for an identifier.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		s.notifyQuickCreateCompleted(ctx, task, qc)
	}
	if ip, ok := s.parseIssuePlanContext(task); ok {
		s.applyIssuePlanCompleted(ctx, task, ip, result)
	}

	// For chat tasks, save assistant reply and broadcast chat:done. The
	// resume pointer was already persisted inside the transaction above.
	if task.ChatSessionID.Valid {
		var assistantMsg *db.ChatMessage
		var payload protocol.TaskCompletedPayload
		if err := json.Unmarshal(result, &payload); err == nil && payload.Output != "" {
			// Same unescape as the issue-comment path above: literal `\n` from
			// agent stdout becomes a real newline so the chat panel renders
			// paragraph breaks instead of one wall of prose.
			body := util.UnescapeBackslashEscapes(payload.Output)
			row, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
				ChatSessionID: task.ChatSessionID,
				Role:          "assistant",
				Content:       redact.Text(body),
				TaskID:        task.ID,
				ElapsedMs:     computeChatElapsedMs(task),
			})
			if err != nil {
				slog.Error("failed to save assistant chat message", "task_id", util.UUIDToString(task.ID), "error", err)
			} else {
				assistantMsg = &row
				// Event-driven unread: stamp unread_since on the first unread
				// assistant message. No-op if the session already has unread.
				// If the user is actively viewing the session, the frontend's
				// auto-mark-read effect will clear this within a tick.
				if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
					slog.Warn("failed to set unread_since", "chat_session_id", util.UUIDToString(task.ChatSessionID), "error", err)
				}
			}
		}
		s.broadcastChatDone(ctx, task, assistantMsg)
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, task)

	return &task, nil
}

// FailTask marks a task as failed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// sessionID/workDir are optional: when the agent established a real session
// before failing (e.g. crashed mid-conversation, was cancelled, or hit a
// tool error), the daemon should pass them so we can preserve the resume
// pointer on both the task row and the chat_session — otherwise the next
// chat turn would silently start a brand-new session and lose memory.
//
// failureReason is a coarse classifier consumed by the auto-retry path.
// Pass "" when unknown (treated as 'agent_error').
func (s *TaskService) FailTask(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, failureReason string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.FailAgentTask(ctx, db.FailAgentTaskParams{
			ID:            taskID,
			Error:         pgtype.Text{String: errMsg, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			SessionID:     pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:       pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		// Keep resume-unsafe sessions on the task row for observability, but
		// do not promote them to the chat-level resume pointer.
		if t.ChatSessionID.Valid && !resumeUnsafeFailureReason(failureReason) {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		return nil
	}); err != nil {
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("fail task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("fail task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("fail task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("fail task: %w", err)
	}

	slog.Warn("task failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "error", errMsg, "failure_reason", failureReason)
	s.captureTaskFailed(ctx, task)
	if s.ProjectKnowledge != nil {
		s.ProjectKnowledge.UpdateRetrievalOutcomeForTask(ctx, task.ID, "failed")
		s.ProjectKnowledge.CaptureTaskFailed(ctx, task, errMsg, failureReason)
	}

	// Auto-retry eligible failures (orphan, timeout, runtime_offline,
	// runtime_recovery). The helper itself enforces attempt < max_attempts
	// and only triggers for issue/chat tasks.
	retried, _ := s.MaybeRetryFailedTask(ctx, task)

	// Skip the per-failure system comment when we'll immediately retry —
	// the new task will surface its own status to the user, and we don't
	// want to spam the issue with "task timed out" messages on every
	// daemon hiccup.
	if errMsg != "" && task.IssueID.Valid && retried == nil {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(errMsg), "system", task.TriggerCommentID)
	}

	// Mirror the issue fallback for chat tasks: write an assistant
	// chat_message tagged with the daemon-reported failure_reason so the
	// conversation history shows what happened. Skip when auto-retry is
	// pending (the new attempt will write its own outcome) — same guard as
	// the issue path above.
	if task.ChatSessionID.Valid && retried == nil {
		if _, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: task.ChatSessionID,
			Role:          "assistant",
			Content:       redact.Text(errMsg),
			TaskID:        pgtype.UUID{Bytes: task.ID.Bytes, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			ElapsedMs:     computeChatElapsedMs(task),
		}); err != nil {
			slog.Error("failed to save failure chat message",
				"task_id", util.UUIDToString(task.ID),
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		} else if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
			slog.Warn("failed to set unread_since on failure",
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		}
	}

	// Quick-create tasks: push a failure inbox notification to the
	// requester so they can either retry or fall back to the advanced form
	// without losing their original prompt. Skipped when an auto-retry is
	// pending — the new attempt will write its own outcome.
	if retried == nil {
		if qc, ok := s.parseQuickCreateContext(task); ok {
			s.notifyQuickCreateFailed(ctx, task, qc, errMsg)
		}
		if planID, _, ok := s.issuePlanIDFromTask(task); ok {
			s.markIssuePlanFailed(ctx, planID, issuePlanTaskFailureMessage(task))
		}
	}
	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskFailed, task)

	return &task, nil
}

// retryableReasons enumerates failure reasons that the auto-retry path is
// allowed to act on. Agent-side errors (compile failures, model rejections,
// etc.) are intentionally excluded — those are real problems that the user
// should see, not infrastructure flakiness.
var retryableReasons = map[string]bool{
	"runtime_offline":           true,
	"runtime_recovery":          true,
	"timeout":                   true,
	"codex_semantic_inactivity": true,
}

func resumeUnsafeFailureReason(reason string) bool {
	switch reason {
	// Keep in sync with GetLastTaskSession / GetLastChatTaskSession and
	// CreateRetryTask's fresh-session CASE WHEN.
	case "iteration_limit", "agent_fallback_message", "api_invalid_request", "codex_semantic_inactivity":
		return true
	default:
		return false
	}
}

// MaybeRetryFailedTask spawns a fresh queued attempt for a recently-failed
// task when the failure was infrastructure-shaped (daemon crash, runtime
// went offline, dispatch/run timeout) and the task hasn't exhausted its
// max_attempts budget. The child task inherits agent/runtime/issue/chat
// links and, for resume-safe failures, the parent's session_id/work_dir so
// the agent can resume the conversation when the backend supports it. Returns
// the new task, or nil when no retry was created.
//
// Autopilot tasks are NOT auto-retried here; the autopilot scheduler owns
// its own re-run cadence and we don't want to double-fire it.
func (s *TaskService) MaybeRetryFailedTask(ctx context.Context, parent db.AgentTaskQueue) (*db.AgentTaskQueue, error) {
	if parent.Status != "failed" {
		return nil, nil
	}
	reason := ""
	if parent.FailureReason.Valid {
		reason = parent.FailureReason.String
	}
	if !retryableReasons[reason] {
		return nil, nil
	}
	if parent.Attempt >= parent.MaxAttempts {
		slog.Info("task auto-retry skipped: budget exhausted",
			"task_id", util.UUIDToString(parent.ID),
			"attempt", parent.Attempt,
			"max_attempts", parent.MaxAttempts,
		)
		return nil, nil
	}
	if parent.AutopilotRunID.Valid {
		// Autopilot has its own retry semantics; do not double-trigger.
		return nil, nil
	}
	planID, _, isIssuePlan := s.issuePlanIDFromTask(parent)
	if !parent.IssueID.Valid && !parent.ChatSessionID.Valid && !isIssuePlan {
		return nil, nil
	}

	var child db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		var err error
		child, err = qtx.CreateRetryTask(ctx, parent.ID)
		if err != nil {
			return err
		}
		if isIssuePlan {
			if _, err := qtx.SetPlanTask(ctx, db.SetPlanTaskParams{ID: planID, TaskID: child.ID}); err != nil {
				return fmt.Errorf("relink issue plan retry task: %w", err)
			}
		}
		return nil
	}); err != nil {
		slog.Warn("task auto-retry failed",
			"parent_task_id", util.UUIDToString(parent.ID),
			"reason", reason,
			"error", err,
		)
		return nil, err
	}
	slog.Info("task auto-retry enqueued",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"reason", reason,
		"attempt", child.Attempt,
		"max_attempts", child.MaxAttempts,
	)
	// Retry creates a fresh queued row, same status transition (∅ → queued)
	// as EnqueueTaskFor*. Broadcast queued first, then notify the daemon —
	// see EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
	s.NotifyTaskEnqueued(ctx, child)
	return &child, nil
}

// RerunIssue creates a fresh queued task for an agent on the issue. Used by
// the manual rerun endpoint.
//
// Target agent resolution:
//   - sourceTaskID Valid: rerun the agent that ran that task. This is what
//     the execution log retry button uses
//     so a per-row retry survives a subsequent assignee change and correctly
//     re-fires the prior assignee or mention agent whose row was clicked. The
//     source task's trigger_comment_id is also inherited (when the caller
//     didn't pass one) so a per-row rerun of a comment- or mention-triggered
//     task stays comment-triggered — the daemon's buildCommentPrompt path
//     keys on TriggerCommentID, and losing it would degrade the rerun into
//     a generic issue run that no longer carries the original comment.
//   - sourceTaskID empty: fall back to the issue's current agent assignee.
//     This preserves the CLI / API contract for callers
//     that have an issue ID but no specific task to target.
//
// The new task is flagged force_fresh_session=true so the daemon starts a
// clean agent session instead of resuming the prior (agent_id, issue_id)
// session. A user clicking rerun has just judged the prior output bad —
// resuming the same conversation would replay the same poisoned state.
// Auto-retry of an orphaned mid-flight failure (HandleFailedTasks →
// MaybeRetryFailedTask → CreateRetryTask) does NOT take this path, so
// MUL-1128's mid-flight resume contract is preserved.
//
// Only tasks belonging to the target agent on this issue are cancelled.
// Tasks owned by other agents on the same issue (e.g. a parallel
// @-mention agent) are left alone — rerun must not collateral-cancel
// them.
func (s *TaskService) RerunIssue(ctx context.Context, issueID pgtype.UUID, sourceTaskID pgtype.UUID, triggerCommentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}

	// Determine the target agent for the rerun.
	var agentID pgtype.UUID
	if sourceTaskID.Valid {
		sourceTask, err := s.Queries.GetAgentTask(ctx, sourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("load source task: %w", err)
		}
		if !sourceTask.IssueID.Valid || util.UUIDToString(sourceTask.IssueID) != util.UUIDToString(issueID) {
			return nil, fmt.Errorf("source task does not belong to this issue")
		}
		agentID = sourceTask.AgentID
		// Inherit trigger provenance so a per-row rerun of a comment- or
		// mention-triggered task stays a comment-triggered task. Without
		// this the daemon's buildCommentPrompt path is skipped (it keys on
		// TriggerCommentID) and the rerun degrades into a generic issue
		// run that has lost the original comment context. Only override
		// when the caller didn't pass one explicitly.
		if !triggerCommentID.Valid && sourceTask.TriggerCommentID.Valid {
			triggerCommentID = sourceTask.TriggerCommentID
		}
	} else {
		if !issue.AssigneeID.Valid || issue.AssigneeType.String != "agent" {
			return nil, fmt.Errorf("issue is not assigned to an agent")
		}
		agentID = issue.AssigneeID
	}
	// Cancel only the assignee's active/queued tasks on this issue. This
	// covers both the unique-index conflict (queued/dispatched) and a
	// stuck running task without touching other agents on the issue.
	cancelled, err := s.Queries.CancelAgentTasksByIssueAndAgent(ctx, db.CancelAgentTasksByIssueAndAgentParams{
		IssueID: issueID,
		AgentID: agentID,
	})
	if err != nil {
		slog.Warn("rerun: cancel prior tasks failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"error", err,
		)
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}

	task, err := s.enqueueRerunTask(ctx, issue, agentID, triggerCommentID)
	if err != nil {
		return nil, err
	}
	slog.Info("issue rerun enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issueID),
		"agent_id", util.UUIDToString(agentID),
		"source_task_id", util.UUIDToString(sourceTaskID),
		"cancelled_prior", len(cancelled),
	)
	return &task, nil
}

// enqueueRerunTask enqueues a fresh task for the given agent on the issue.
// When the target agent is the issue's single-agent assignee we use the
// assignee-driven path (enqueueIssueTask) so the issue-assignee bookkeeping
// stays in sync; otherwise (prior assignee that has since been reassigned,
// mention agent) we use the mention path with the same
// force_fresh_session=true contract.
func (s *TaskService) enqueueRerunTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentTaskQueue, error) {
	if issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid &&
		util.UUIDToString(issue.AssigneeID) == util.UUIDToString(agentID) {
		return s.enqueueIssueTask(ctx, issue, triggerCommentID, true)
	}
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, true)
}

// HandleFailedTasks runs the post-failure side effects for a batch of
// freshly-failed tasks: optional auto-retry, task:failed event broadcast,
// agent status reconciliation, and (when an issue has no remaining active
// task and isn't being retried) resetting the issue back to todo so the
// daemon can pick it up again.
//
// All callers that surface a task as failed — sweepers, FailTask,
// recover-orphans — funnel through here so the same UI-consistency
// guarantees apply on every code path.
func (s *TaskService) HandleFailedTasks(ctx context.Context, tasks []db.AgentTaskQueue) int {
	if len(tasks) == 0 {
		return 0
	}

	affectedAgents := make(map[string]pgtype.UUID)
	processedIssues := make(map[string]bool)
	retriedIssues := make(map[string]bool)
	retriedPlans := make(map[string]bool)
	retried := 0

	for _, t := range tasks {
		// Auto-retry first so the issue stays in_progress rather than
		// flapping todo → in_progress within a tick.
		if child, _ := s.MaybeRetryFailedTask(ctx, t); child != nil {
			retried++
			if t.IssueID.Valid {
				retriedIssues[util.UUIDToString(t.IssueID)] = true
			}
			if planID, _, ok := s.issuePlanIDFromTask(t); ok {
				retriedPlans[util.UUIDToString(planID)] = true
			}
		}

		failureReason := "agent_error"
		if t.FailureReason.Valid && t.FailureReason.String != "" {
			failureReason = t.FailureReason.String
		}
		s.captureTaskFailed(ctx, t)

		workspaceID := ""
		if t.IssueID.Valid {
			if issue, err := s.Queries.GetIssue(ctx, t.IssueID); err == nil {
				workspaceID = util.UUIDToString(issue.WorkspaceID)
				// Reset stuck in_progress issues only when no other active
				// task exists for the issue and no retry was just enqueued.
				issueKey := util.UUIDToString(t.IssueID)
				if issue.Status == "in_progress" && !processedIssues[issueKey] && !retriedIssues[issueKey] {
					processedIssues[issueKey] = true
					hasActive, checkErr := s.Queries.HasActiveTaskForIssue(ctx, t.IssueID)
					if checkErr != nil {
						slog.Warn("handle failed tasks: active check failed",
							"issue_id", issueKey,
							"error", checkErr,
						)
					} else if !hasActive {
						if _, updateErr := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
							ID:     t.IssueID,
							Status: "todo",
						}); updateErr != nil {
							slog.Warn("handle failed tasks: reset stuck issue failed",
								"issue_id", issueKey,
								"error", updateErr,
							)
						}
					}
				}
			}
		}
		if planID, _, ok := s.issuePlanIDFromTask(t); ok && !retriedPlans[util.UUIDToString(planID)] {
			s.markIssuePlanFailed(ctx, planID, issuePlanTaskFailureMessage(t))
		}
		if workspaceID == "" {
			workspaceID = s.ResolveTaskWorkspaceID(ctx, t)
		}

		if workspaceID != "" {
			s.Bus.Publish(events.Event{
				Type:        protocol.EventTaskFailed,
				WorkspaceID: workspaceID,
				ActorType:   "system",
				Payload: map[string]any{
					"task_id":        util.UUIDToString(t.ID),
					"agent_id":       util.UUIDToString(t.AgentID),
					"issue_id":       util.UUIDToString(t.IssueID),
					"status":         "failed",
					"failure_reason": failureReason,
				},
			})
		}

		affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
	}

	for _, agentID := range affectedAgents {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	return retried
}

// runInTx executes fn inside a single DB transaction. If TxStarter is nil
// (e.g. some tests construct TaskService directly), fn runs against the
// regular Queries handle without transactional guarantees.
func (s *TaskService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReportProgress broadcasts a progress update via the event bus.
func (s *TaskService) ReportProgress(ctx context.Context, taskID string, workspaceID string, summary string, step, total int) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload: protocol.TaskProgressPayload{
			TaskID:  taskID,
			Summary: summary,
			Step:    step,
			Total:   total,
		},
	})
}

// ReconcileAgentStatus refreshes agent status from the current active task set.
func (s *TaskService) ReconcileAgentStatus(ctx context.Context, agentID pgtype.UUID) {
	agent, err := s.Queries.RefreshAgentStatusFromTasks(ctx, agentID)
	if err != nil {
		return
	}
	slog.Debug("agent status reconciled", "agent_id", util.UUIDToString(agentID), "status", agent.Status)
	s.publishAgentStatus(agent)
}

func (s *TaskService) updateAgentStatus(ctx context.Context, agentID pgtype.UUID, status string) {
	agent, err := s.Queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
		ID:     agentID,
		Status: status,
	})
	if err != nil {
		return
	}
	s.publishAgentStatus(agent)
}

func (s *TaskService) publishAgentStatus(agent db.Agent) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventAgentStatus,
		WorkspaceID: util.UUIDToString(agent.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"agent": agentToMap(agent)},
	})
}

// LoadAgentSkills loads an agent's skills with their files for task execution.
func (s *TaskService) LoadAgentSkills(ctx context.Context, agentID pgtype.UUID) []AgentSkillData {
	skills, err := s.Queries.ListAgentSkills(ctx, agentID)
	if err != nil || len(skills) == 0 {
		return nil
	}

	result := make([]AgentSkillData, 0, len(skills))
	for _, sk := range skills {
		data := AgentSkillData{Name: sk.Name, Description: sk.Description, Content: sk.Content}
		files, _ := s.Queries.ListSkillFiles(ctx, sk.ID)
		for _, f := range files {
			data.Files = append(data.Files, AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		result = append(result, data)
	}
	return result
}

// AgentSkillData represents a skill for task execution responses.
type AgentSkillData struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Content     string               `json:"content"`
	Files       []AgentSkillFileData `json:"files,omitempty"`
}

// AgentSkillFileData represents a supporting file within a skill.
type AgentSkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// computeChatElapsedMs returns the wall-clock duration from task creation
// (user hit send) to terminal state (completed/failed). Stored on the
// assistant chat_message so the UI can render "Replied in 38s" /
// "Failed after 12s". Uses created_at — not started_at — because users
// experience total wait time, including queue + dispatch, not just the
// daemon's actual run time.
func computeChatElapsedMs(task db.AgentTaskQueue) pgtype.Int8 {
	if !task.CompletedAt.Valid || !task.CreatedAt.Valid {
		return pgtype.Int8{}
	}
	ms := task.CompletedAt.Time.Sub(task.CreatedAt.Time).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return pgtype.Int8{Int64: ms, Valid: true}
}

func priorityToInt(p string) int32 {
	switch p {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// NotifyTaskEnqueued is the cross-package shim for callers outside
// TaskService (e.g. AutopilotService.dispatchRunOnly) that insert a
// row into agent_task_queue directly. Invalidates the empty-claim
// cache and kicks the daemon WS so the new task is claimed without
// waiting for the next poll.
func (s *TaskService) NotifyTaskEnqueued(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskQueued(ctx, task)
	s.notifyTaskAvailable(task)
}

// notifyTaskAvailable runs after a task has been inserted: bumps the
// runtime's invalidation version so any in-flight claim that is about
// to write an "empty" verdict will have it rejected on read, then
// kicks the daemon WS so the daemon claims without waiting for its
// next poll. Order matters — Bump must happen before the wakeup,
// otherwise the wakeup-driven claim could read the still-current
// empty verdict and return null.
func (s *TaskService) notifyTaskAvailable(task db.AgentTaskQueue) {
	if !task.RuntimeID.Valid {
		return
	}
	runtimeKey := util.UUIDToString(task.RuntimeID)
	// Use a background context: the cache bump / wakeup must outlive
	// the request that created the task, otherwise an early client
	// disconnect could leave the empty verdict in place and stall the
	// just-queued task until the TTL expires. The cache itself bounds
	// every Redis call with a short timeout so a wedged Redis cannot
	// block enqueue.
	s.EmptyClaim.Bump(context.Background(), runtimeKey)
	if s.Wakeup == nil {
		return
	}
	s.Wakeup.NotifyTaskAvailable(runtimeKey, util.UUIDToString(task.ID))
}

func (s *TaskService) broadcastTaskDispatch(ctx context.Context, task db.AgentTaskQueue) {
	var payload map[string]any
	if task.Context != nil {
		json.Unmarshal(task.Context, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task_id"] = util.UUIDToString(task.ID)
	payload["runtime_id"] = util.UUIDToString(task.RuntimeID)
	payload["issue_id"] = util.UUIDToString(task.IssueID)
	payload["agent_id"] = util.UUIDToString(task.AgentID)
	// chat_session_id is the routing key the chat window uses to writethrough
	// `chatKeys.pendingTask` to status="running" the moment the daemon claims
	// the task. Without it the pill stays stuck at "Queued" until completion.
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}

	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskDispatch,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

func (s *TaskService) broadcastTaskEvent(ctx context.Context, eventType string, task db.AgentTaskQueue) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	payload := map[string]any{
		"task_id":  util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(task.IssueID),
		"status":   task.Status,
	}
	if task.Result != nil {
		var result map[string]any
		if json.Unmarshal(task.Result, &result) == nil {
			if branchName, ok := result["branch_name"].(string); ok && strings.TrimSpace(branchName) != "" {
				payload["branch_name"] = branchName
			}
			if commitSHA, ok := result["branch_commit_sha"].(string); ok && strings.TrimSpace(commitSHA) != "" {
				payload["branch_commit_sha"] = commitSHA
			}
			if pushedAt, ok := result["branch_pushed_at"].(string); ok && strings.TrimSpace(pushedAt) != "" {
				payload["branch_pushed_at"] = pushedAt
			}
		}
	}
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}
	if ip, ok := s.parseIssuePlanContext(task); ok {
		payload["plan_id"] = ip.PlanID
	}
	s.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

// ResolveTaskWorkspaceID determines the workspace ID for a task.
// For issue tasks, it comes from the issue. For chat tasks, from the chat session.
// For autopilot tasks, from the autopilot via its run.
// Returns "" when none of the links resolve — callers treat that as "not found".
func (s *TaskService) ResolveTaskWorkspaceID(ctx context.Context, task db.AgentTaskQueue) string {
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			return util.UUIDToString(issue.WorkspaceID)
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			return util.UUIDToString(cs.WorkspaceID)
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := s.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := s.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil {
				return util.UUIDToString(ap.WorkspaceID)
			}
		}
	}
	// Quick-create tasks have no issue / chat / autopilot link — workspace
	// lives in the context JSONB. Returning "" here is what blocked
	// requireDaemonTaskAccess (404 on /start, /progress, /complete, /fail
	// for the daemon) and silently dropped task:dispatch / task:completed
	// broadcasts, which is why quick-create tasks appeared stuck queued.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		return qc.WorkspaceID
	}
	if ip, ok := s.parseIssuePlanContext(task); ok {
		return ip.WorkspaceID
	}
	return ""
}

func (s *TaskService) broadcastChatDone(ctx context.Context, task db.AgentTaskQueue, msg *db.ChatMessage) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	payload := protocol.ChatDonePayload{
		ChatSessionID: util.UUIDToString(task.ChatSessionID),
		TaskID:        util.UUIDToString(task.ID),
	}
	if msg != nil {
		payload.MessageID = util.UUIDToString(msg.ID)
		payload.Content = msg.Content
		if msg.CreatedAt.Valid {
			payload.CreatedAt = msg.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if msg.ElapsedMs.Valid {
			payload.ElapsedMs = msg.ElapsedMs.Int64
		}
	}
	s.Bus.Publish(events.Event{
		Type:          protocol.EventChatDone,
		WorkspaceID:   workspaceID,
		ActorType:     "system",
		ActorID:       "",
		ChatSessionID: util.UUIDToString(task.ChatSessionID),
		Payload:       payload,
	})
}

func (s *TaskService) broadcastIssueUpdated(issue db.Issue) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"issue": issueToMap(issue, prefix)},
	})
}

func (s *TaskService) getIssuePrefix(workspaceID pgtype.UUID) string {
	ws, err := s.Queries.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return ""
	}
	return ws.IssuePrefix
}

func (s *TaskService) createAgentComment(ctx context.Context, issueID, agentID pgtype.UUID, content, commentType string, parentID pgtype.UUID) {
	if content == "" {
		return
	}
	// Look up issue to get workspace ID for mention expansion and broadcasting.
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	// Resolve thread root: if parentID points to a reply (has its own parent),
	// use that parent instead so the comment lands in the top-level thread.
	// rootComment captures the root row so we can auto-unresolve it after the
	// reply is committed (see AutoUnresolveThreadOnReply).
	var rootComment *db.Comment
	if parentID.Valid {
		if parent, err := s.Queries.GetComment(ctx, parentID); err == nil {
			if parent.ParentID.Valid {
				if root, err := s.Queries.GetComment(ctx, parent.ParentID); err == nil {
					rootComment = &root
					parentID = root.ID
				}
			} else {
				rootComment = &parent
			}
		}
	}
	// Expand bare issue identifiers (e.g. MUL-117) into mention links.
	content = mention.ExpandIssueIdentifiers(ctx, s.Queries, issue.WorkspaceID, content)
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
		Content:     content,
		Type:        commentType,
		ParentID:    parentID,
	})
	if err != nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(comment.ID),
				"issue_id":    util.UUIDToString(comment.IssueID),
				"author_type": comment.AuthorType,
				"author_id":   util.UUIDToString(comment.AuthorID),
				"content":     comment.Content,
				"type":        comment.Type,
				"parent_id":   util.UUIDToPtr(comment.ParentID),
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
	s.AutoUnresolveThreadOnReply(ctx, rootComment, util.UUIDToString(issue.WorkspaceID), "agent", util.UUIDToString(agentID))
}

// AutoUnresolveThreadOnReply clears resolved_at on the thread root when a
// reply lands in a resolved thread, and broadcasts comment:unresolved. Shared
// between the user-facing Handler.CreateComment path and the agent-facing
// TaskService.createAgentComment path so the resolved-then-replied state can
// never desync (one of the bugs Emacs flagged on PR #2300). Errors are logged
// — the reply itself already committed, the desync is recoverable on next read.
func (s *TaskService) AutoUnresolveThreadOnReply(ctx context.Context, parent *db.Comment, workspaceID, actorType, actorID string) {
	if parent == nil || !parent.ResolvedAt.Valid {
		return
	}
	updated, err := s.Queries.UnresolveComment(ctx, parent.ID)
	if err != nil {
		slog.Warn("auto-unresolve on reply failed", "error", err, "comment_id", util.UUIDToString(parent.ID))
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentUnresolved,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		Payload: map[string]any{
			"comment": map[string]any{
				"id":               util.UUIDToString(updated.ID),
				"issue_id":         util.UUIDToString(updated.IssueID),
				"author_type":      updated.AuthorType,
				"author_id":        util.UUIDToString(updated.AuthorID),
				"content":          updated.Content,
				"type":             updated.Type,
				"parent_id":        util.UUIDToPtr(updated.ParentID),
				"created_at":       util.TimestampToString(updated.CreatedAt),
				"updated_at":       util.TimestampToString(updated.UpdatedAt),
				"resolved_at":      util.TimestampToPtr(updated.ResolvedAt),
				"resolved_by_type": util.TextToPtr(updated.ResolvedByType),
				"resolved_by_id":   util.UUIDToPtr(updated.ResolvedByID),
			},
		},
	})
}

func issueToMap(issue db.Issue, issuePrefix string) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"number":          issue.Number,
		"identifier":      issuePrefix + "-" + strconv.Itoa(int(issue.Number)),
		"title":           issue.Title,
		"description":     util.TextToPtr(issue.Description),
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   util.TextToPtr(issue.AssigneeType),
		"assignee_id":     util.UUIDToPtr(issue.AssigneeID),
		"creator_type":    issue.CreatorType,
		"creator_id":      util.UUIDToString(issue.CreatorID),
		"parent_issue_id": util.UUIDToPtr(issue.ParentIssueID),
		"position":        issue.Position,
		"start_date":      util.TimestampToPtr(issue.StartDate),
		"due_date":        util.TimestampToPtr(issue.DueDate),
		"created_at":      util.TimestampToString(issue.CreatedAt),
		"updated_at":      util.TimestampToString(issue.UpdatedAt),
	}
}

// parseQuickCreateContext returns the quick-create payload if the task's
// context JSONB contains type == "quick_create"; otherwise the bool is
// false so callers can short-circuit. Tasks linked to an issue / chat /
// autopilot are never quick-create even if they happen to carry a
// context blob, so those are filtered up front.
func (s *TaskService) parseQuickCreateContext(task db.AgentTaskQueue) (QuickCreateContext, bool) {
	if task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid {
		return QuickCreateContext{}, false
	}
	if len(task.Context) == 0 {
		return QuickCreateContext{}, false
	}
	var qc QuickCreateContext
	if err := json.Unmarshal(task.Context, &qc); err != nil {
		return QuickCreateContext{}, false
	}
	if qc.Type != QuickCreateContextType {
		return QuickCreateContext{}, false
	}
	return qc, true
}

func (s *TaskService) parseIssuePlanContext(task db.AgentTaskQueue) (IssuePlanContext, bool) {
	if task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid {
		return IssuePlanContext{}, false
	}
	if len(task.Context) == 0 {
		return IssuePlanContext{}, false
	}
	var ip IssuePlanContext
	if err := json.Unmarshal(task.Context, &ip); err != nil {
		return IssuePlanContext{}, false
	}
	if ip.Type != IssuePlanContextType {
		return IssuePlanContext{}, false
	}
	return ip, true
}

func (s *TaskService) issuePlanIDFromTask(task db.AgentTaskQueue) (pgtype.UUID, IssuePlanContext, bool) {
	ip, ok := s.parseIssuePlanContext(task)
	if !ok {
		return pgtype.UUID{}, IssuePlanContext{}, false
	}
	planID, err := util.ParseUUID(ip.PlanID)
	if err != nil {
		return pgtype.UUID{}, ip, false
	}
	return planID, ip, true
}

func issuePlanTaskFailureMessage(task db.AgentTaskQueue) string {
	if task.Error.Valid && strings.TrimSpace(task.Error.String) != "" {
		return strings.TrimSpace(task.Error.String)
	}
	if task.FailureReason.Valid && strings.TrimSpace(task.FailureReason.String) != "" {
		return "planner task failed: " + strings.TrimSpace(task.FailureReason.String)
	}
	return "planner task failed"
}

func (s *TaskService) applyIssuePlanCompleted(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, result []byte) {
	if ip.SourceIssueID != "" {
		s.applyPlannerIssueCompleted(ctx, task, ip, result)
		return
	}
	planID, err := util.ParseUUID(ip.PlanID)
	if err != nil {
		slog.Warn("issue-plan completion: invalid plan id", "task_id", util.UUIDToString(task.ID), "plan_id", ip.PlanID, "error", err)
		return
	}
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(result, &payload); err != nil {
		s.markIssuePlanFailed(ctx, planID, fmt.Sprintf("invalid task result: %v", err))
		return
	}
	output := strings.TrimSpace(payload.Output)
	if normalizeIssuePlanPhase(ip.Phase) == IssuePlanPhaseSpec {
		spec, err := parseIssuePlanSpecOutput(output)
		if err != nil {
			s.markIssuePlanFailed(ctx, planID, err.Error())
			return
		}
		if err := s.writeIssuePlanSpec(ctx, planID, spec); err != nil {
			slog.Warn("issue-plan completion: failed to write spec", "task_id", util.UUIDToString(task.ID), "plan_id", ip.PlanID, "error", err)
			s.markIssuePlanFailed(ctx, planID, "failed to save planner spec")
		}
		return
	}
	parsed, err := parseIssuePlanOutput(output)
	if err != nil {
		s.markIssuePlanFailed(ctx, planID, err.Error())
		return
	}
	if parsed.hasPipelineSelection() {
		err = s.writePipelinePlanDraft(ctx, task, ip, planID, parsed)
		if err != nil {
			slog.Warn("issue-plan completion: failed to save pipeline plan", "task_id", util.UUIDToString(task.ID), "plan_id", ip.PlanID, "error", err)
			s.markIssuePlanFailed(ctx, planID, err.Error())
		}
		return
	}
	if err := s.writeIssuePlanResult(ctx, planID, parsed); err != nil {
		slog.Warn("issue-plan completion: failed to write result", "task_id", util.UUIDToString(task.ID), "plan_id", ip.PlanID, "error", err)
		s.markIssuePlanFailed(ctx, planID, "failed to save planner result")
		return
	}
}

func (s *TaskService) applyPlannerIssueCompleted(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, result []byte) {
	sourceIssueID, err := util.ParseUUID(ip.SourceIssueID)
	if err != nil {
		slog.Warn("planner issue completion: invalid source issue id", "task_id", util.UUIDToString(task.ID), "source_issue_id", ip.SourceIssueID, "error", err)
		return
	}
	planID, err := s.ensurePlannerIssuePlan(ctx, task, ip, sourceIssueID)
	if err != nil {
		slog.Warn("planner issue completion: failed to prepare plan", "task_id", util.UUIDToString(task.ID), "source_issue_id", ip.SourceIssueID, "error", err)
		s.writePlannerIssueComment(ctx, sourceIssueID, task.AgentID, "规划Agent 准备 plan 草稿失败: "+err.Error())
		return
	}
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(result, &payload); err != nil {
		s.markIssuePlanFailed(ctx, planID, fmt.Sprintf("invalid task result: %v", err))
		return
	}
	output := strings.TrimSpace(payload.Output)
	if normalizeIssuePlanPhase(ip.Phase) == IssuePlanPhaseSpec {
		spec, err := parseIssuePlanSpecOutput(output)
		if err != nil {
			s.markIssuePlanFailed(ctx, planID, err.Error())
			return
		}
		if err := s.writeIssuePlanSpec(ctx, planID, spec); err != nil {
			slog.Warn("planner issue completion: failed to save plan spec", "task_id", util.UUIDToString(task.ID), "source_issue_id", ip.SourceIssueID, "error", err)
			s.markIssuePlanFailed(ctx, planID, "failed to save planner spec")
		}
		return
	}
	parsed, err := parseIssuePlanOutput(output)
	if err != nil {
		s.markIssuePlanFailed(ctx, planID, err.Error())
		return
	}
	if parsed.hasPipelineSelection() {
		err = s.writePipelinePlanDraftFromPlannerIssue(ctx, task, ip, sourceIssueID, planID, parsed)
		if err != nil {
			slog.Warn("planner issue completion: failed to save pipeline plan", "task_id", util.UUIDToString(task.ID), "source_issue_id", ip.SourceIssueID, "error", err)
			s.markIssuePlanFailed(ctx, planID, err.Error())
		}
		return
	}
	if err := s.writeIssuePlanResult(ctx, planID, parsed); err != nil {
		slog.Warn("planner issue completion: failed to save plan result", "task_id", util.UUIDToString(task.ID), "source_issue_id", ip.SourceIssueID, "error", err)
		s.markIssuePlanFailed(ctx, planID, "failed to save planner result")
		return
	}
}

func (s *TaskService) ensurePlannerIssuePlan(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, sourceIssueID pgtype.UUID) (pgtype.UUID, error) {
	if strings.TrimSpace(ip.PlanID) != "" {
		planID, err := util.ParseUUID(ip.PlanID)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("invalid plan id")
		}
		return planID, nil
	}
	requesterID, err := util.ParseUUID(ip.RequesterID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid requester id")
	}
	sourceIssue, err := s.Queries.GetIssue(ctx, sourceIssueID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("source issue not found")
	}
	if sourceIssue.WorkspaceID != taskWorkspaceUUID(ip.WorkspaceID) {
		return pgtype.UUID{}, fmt.Errorf("source issue workspace mismatch")
	}
	prompt := strings.TrimSpace(ip.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(sourceIssue.Title)
	}
	projectID := sourceIssue.ProjectID
	if ip.ProjectID != "" {
		if parsedProjectID, err := util.ParseUUID(ip.ProjectID); err == nil {
			projectID = parsedProjectID
		}
	}
	parentDescription := pgtype.Text{}
	if sourceIssue.Description.Valid {
		parentDescription = serviceStrOrNullText(sourceIssue.Description.String)
	}
	var plan db.Plan
	err = s.runInTx(ctx, func(qtx *db.Queries) error {
		var err error
		plan, err = qtx.CreatePlanForIssue(ctx, db.CreatePlanForIssueParams{
			WorkspaceID:       sourceIssue.WorkspaceID,
			Title:             sourceIssue.Title,
			Prompt:            prompt,
			PlannerAgentID:    task.AgentID,
			ParentTitle:       pgtype.Text{String: sourceIssue.Title, Valid: true},
			ParentDescription: parentDescription,
			ParentIssueID:     sourceIssue.ID,
			ProjectID:         projectID,
			CreatedBy:         requesterID,
		})
		if err != nil {
			return fmt.Errorf("create issue plan: %w", err)
		}
		if _, err := qtx.SetPlanTask(ctx, db.SetPlanTaskParams{ID: plan.ID, TaskID: task.ID}); err != nil {
			return fmt.Errorf("link planner issue plan task: %w", err)
		}
		return nil
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return plan.ID, nil
}

func parseIssuePlanOutput(output string) (issuePlanResult, error) {
	var out issuePlanResult
	if output == "" {
		return out, fmt.Errorf("planner returned empty output")
	}
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return out, fmt.Errorf("planner output did not contain a JSON object; update or restart the planner daemon so it supports Plans")
	}
	raw := output[start : end+1]
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, fmt.Errorf("planner output JSON is invalid: %v", err)
	}
	if !out.shouldCreatePlan() {
		return out, nil
	}
	if strings.TrimSpace(out.Pipeline.ID) != "" && strings.TrimSpace(out.PipelineID) == "" {
		out.PipelineID = out.Pipeline.ID
	}
	if strings.TrimSpace(out.Pipeline.Name) != "" && strings.TrimSpace(out.PipelineName) == "" {
		out.PipelineName = out.Pipeline.Name
	}
	if strings.TrimSpace(out.Pipeline.ParentIssue.Title) != "" && strings.TrimSpace(out.ParentIssue.Title) == "" {
		out.ParentIssue = out.Pipeline.ParentIssue
	}
	if len(out.Pipeline.Nodes) > 0 {
		seenKeys := map[string]bool{}
		for i, node := range out.Pipeline.Nodes {
			key := strings.TrimSpace(node.Key)
			if key == "" {
				return out, fmt.Errorf("planner output pipeline node %d missing key", i+1)
			}
			if seenKeys[key] {
				return out, fmt.Errorf("planner output pipeline node %q is duplicated", key)
			}
			seenKeys[key] = true
			out.Pipeline.Nodes[i].Key = key
		}
		if len(out.Items) == 0 {
			nodePositions := make(map[string]int32, len(out.Pipeline.Nodes))
			for i, node := range out.Pipeline.Nodes {
				nodePositions[node.Key] = int32(i + 1)
			}
			for i, node := range out.Pipeline.Nodes {
				selected := true
				if node.Selected != nil {
					selected = *node.Selected
				}
				dependsOnPositions := make([]int32, 0, len(node.DependsOnNodeKeys))
				for _, depKey := range normalizeStringSlice(node.DependsOnNodeKeys) {
					depPosition, ok := nodePositions[depKey]
					if !ok {
						return out, fmt.Errorf("planner output pipeline node %q depends on unknown node %q", node.Key, depKey)
					}
					if depPosition >= int32(i+1) {
						return out, fmt.Errorf("planner output pipeline node %q depends_on_node_keys must reference earlier nodes", node.Key)
					}
					dependsOnPositions = append(dependsOnPositions, depPosition)
				}
				out.Items = append(out.Items, issuePlanResultItem{
					Title:                 strings.TrimSpace(node.Title),
					Description:           strings.TrimSpace(node.Description),
					AcceptanceCriteria:    normalizeStringSlice(node.AcceptanceCriteria),
					SuggestedTestCommands: normalizeStringSlice(node.SuggestedTestCommands),
					UnitTestChecklist:     NormalizeUnitTestChecks(node.UnitTestChecklist),
					ContextResources:      normalizeStringSlice(node.ContextResources),
					RiskNotes:             normalizeStringSlice(node.RiskNotes),
					NodeType:              NormalizePlanItemNodeType(firstNonEmpty(node.NodeType, node.Type)),
					ExecutionKind:         normalizePlanItemExecutionKind(node.ExecutionKind),
					ConfirmationQuestion:  strings.TrimSpace(node.ConfirmationQuestion),
					ConfirmationReason:    strings.TrimSpace(node.ConfirmationReason),
					RequiredEvidence:      normalizeStringSlice(node.RequiredEvidence),
					RequiresGitCommit:     node.RequiresGitCommit,
					BranchName:            strings.TrimSpace(node.BranchName),
					IterationIndex:        node.IterationIndex,
					IterationTitle:        strings.TrimSpace(node.IterationTitle),
					IterationBranchName:   strings.TrimSpace(node.IterationBranchName),
					RecommendedAgentID:    strings.TrimSpace(node.AgentID),
					MatchScore:            100,
					MatchReason:           "Selected by pipeline node assignment.",
					DependsOnPositions:    dependsOnPositions,
					Selected:              &selected,
				})
			}
		}
	}
	if strings.TrimSpace(out.ParentIssue.Title) == "" {
		out.ParentIssue.Title = strings.TrimSpace(out.Title)
	}
	if strings.TrimSpace(out.ParentIssue.Title) == "" && len(out.Pipeline.Nodes) == 0 {
		return out, fmt.Errorf("planner output missing parent_issue.title")
	}
	if len(out.Items) == 0 && len(out.Pipeline.Nodes) == 0 {
		return out, fmt.Errorf("planner output missing items")
	}
	for i, item := range out.Items {
		if strings.TrimSpace(item.Title) == "" {
			return out, fmt.Errorf("planner output item %d missing title", i+1)
		}
		if item.MatchScore < 0 || item.MatchScore > 100 {
			return out, fmt.Errorf("planner output item %d match_score must be 0-100", i+1)
		}
		seenDeps := make(map[int32]bool)
		normalizedDeps := make([]int32, 0, len(item.DependsOnPositions))
		for _, dep := range item.DependsOnPositions {
			if dep <= 0 || dep >= int32(i+1) {
				return out, fmt.Errorf("planner output item %d depends_on_positions must reference earlier item positions", i+1)
			}
			if seenDeps[dep] {
				return out, fmt.Errorf("planner output item %d has duplicate dependency position %d", i+1, dep)
			}
			seenDeps[dep] = true
			normalizedDeps = append(normalizedDeps, dep)
		}
		out.Items[i].DependsOnPositions = normalizedDeps
		out.Items[i].AcceptanceCriteria = normalizeStringSlice(item.AcceptanceCriteria)
		out.Items[i].SuggestedTestCommands = normalizeStringSlice(item.SuggestedTestCommands)
		out.Items[i].UnitTestChecklist = NormalizeUnitTestChecks(item.UnitTestChecklist)
		out.Items[i].ContextResources = normalizeStringSlice(item.ContextResources)
		out.Items[i].RiskNotes = normalizeStringSlice(item.RiskNotes)
		out.Items[i] = normalizeIssuePlanResultItemContract(out.Items[i])
	}
	return out, nil
}

func parseIssuePlanSpecOutput(output string) (PlanSpec, error) {
	var wrapper struct {
		Spec PlanSpec `json:"spec"`
	}
	var spec PlanSpec
	if output == "" {
		return spec, fmt.Errorf("planner returned empty output")
	}
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return spec, fmt.Errorf("planner output did not contain a JSON object; update or restart the planner daemon so it supports Plans")
	}
	raw := output[start : end+1]
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return spec, fmt.Errorf("planner output JSON is invalid: %v", err)
	}
	spec = normalizePlanSpec(wrapper.Spec)
	if spec.Summary == "" && spec.Goal == "" {
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return spec, fmt.Errorf("planner output JSON is invalid: %v", err)
		}
		spec = normalizePlanSpec(spec)
	}
	if spec.Summary == "" {
		return spec, fmt.Errorf("planner spec missing summary")
	}
	if spec.Goal == "" {
		return spec, fmt.Errorf("planner spec missing goal")
	}
	return spec, nil
}

func (s *TaskService) applyReviewGateCompleted(ctx context.Context, task db.AgentTaskQueue, result []byte) bool {
	nodeType, ok := s.reviewGateNodeTypeForIssue(ctx, task.IssueID)
	if !ok {
		return false
	}

	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("review gate issue lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return true
	}

	review, parseErr := s.parseReviewGateResultForTask(ctx, task, issue, result)
	nextStatus := "blocked"
	commentType := "comment"
	comment := ""
	if parseErr != nil {
		commentType = "system"
		comment = formatInvalidReviewGateComment(nodeType, parseErr)
	} else {
		comment = formatReviewGateComment(nodeType, review)
		if review.Status == reviewGateStatusPass {
			nextStatus = "done"
		}
	}

	agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
		IssueID:  task.IssueID,
		AuthorID: task.AgentID,
		Since:    task.StartedAt,
	})
	if !agentCommented {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(comment), commentType, task.TriggerCommentID)
	}

	issue, err = s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:     task.IssueID,
		Status: nextStatus,
	})
	if err != nil {
		slog.Warn("review gate status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "status", nextStatus, "error", err)
		return true
	}
	s.broadcastIssueUpdated(issue)
	if nextStatus == "done" {
		s.enqueueUnblockedIssueTasks(ctx, task.IssueID)
	} else if parseErr == nil && review.Status == reviewGateStatusFail {
		if s.ProjectKnowledge != nil {
			s.ProjectKnowledge.CaptureReviewFinding(ctx, task, issue, nodeType, review)
		}
		s.ensureReviewGateRepairIssue(ctx, task, issue, nodeType, review)
	}
	return true
}

func (s *TaskService) parseReviewGateResultForTask(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, result []byte) (reviewGateResult, error) {
	output := taskCompletedOutput(result)
	review, outputErr := parseReviewGateOutput(output)
	if outputErr == nil {
		return review, nil
	}
	review, commentErr := s.parseReviewGateResultFromAgentComment(ctx, task, issue)
	if commentErr == nil {
		return review, nil
	}
	return reviewGateResult{}, outputErr
}

func (s *TaskService) parseReviewGateResultFromAgentComment(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) (reviewGateResult, error) {
	since := task.StartedAt
	if !since.Valid {
		since = task.CreatedAt
	}
	comments, err := s.Queries.ListCommentsSinceForIssue(ctx, db.ListCommentsSinceForIssueParams{
		IssueID:     task.IssueID,
		WorkspaceID: issue.WorkspaceID,
		CreatedAt:   since,
		Limit:       50,
	})
	if err != nil {
		return reviewGateResult{}, err
	}

	var parseErr error
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.AuthorType != "agent" || util.UUIDToString(comment.AuthorID) != util.UUIDToString(task.AgentID) {
			continue
		}
		if !strings.Contains(strings.ToLower(comment.Content), "review_gate") {
			continue
		}
		review, err := parseReviewGateOutput(comment.Content)
		if err == nil {
			return review, nil
		}
		parseErr = err
	}
	if parseErr != nil {
		return reviewGateResult{}, parseErr
	}
	return reviewGateResult{}, fmt.Errorf("no agent comment contained review_gate JSON")
}

func (s *TaskService) reviewGateNodeTypeForIssue(ctx context.Context, issueID pgtype.UUID) (string, bool) {
	stage, err := s.Queries.GetPipelineRunStageForIssue(ctx, issueID)
	if err == nil {
		nodeType := NormalizePlanItemNodeType(stage.NodeType)
		return nodeType, IsReviewGateNodeType(nodeType)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("review gate pipeline lookup failed", "issue_id", util.UUIDToString(issueID), "error", err)
		return "", false
	}
	item, err := s.Queries.GetPlanItemByGeneratedIssue(ctx, issueID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("review gate plan item lookup failed", "issue_id", util.UUIDToString(issueID), "error", err)
		}
		return "", false
	}
	nodeType := NormalizePlanItemNodeType(item.NodeType)
	return nodeType, IsReviewGateNodeType(nodeType)
}

func (s *TaskService) applyReviewGateRepairTaskCompleted(ctx context.Context, task db.AgentTaskQueue) bool {
	if !task.IssueID.Valid || task.TriggerCommentID.Valid {
		return false
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return false
	}
	if !issue.OriginType.Valid || issue.OriginType.String != reviewGateRepairOriginType || !issue.OriginID.Valid {
		return false
	}
	switch issue.Status {
	case "done", "blocked", "cancelled":
		return false
	}
	updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:     task.IssueID,
		Status: "done",
	})
	if err != nil {
		slog.Warn("review gate repair status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return false
	}
	s.broadcastIssueUpdated(updated)
	s.enqueueUnblockedIssueTasks(ctx, task.IssueID)
	return true
}

func (s *TaskService) ensureReviewGateRepairIssue(ctx context.Context, task db.AgentTaskQueue, reviewIssue db.Issue, nodeType string, review reviewGateResult) {
	openRepairs, err := s.Queries.ListOpenReviewGateRepairIssues(ctx, reviewIssue.ID)
	if err == nil && len(openRepairs) > 0 {
		s.createAgentComment(ctx, reviewIssue.ID, task.AgentID, redact.Text(formatExistingReviewGateRepairComment(openRepairs[0])), "system", pgtype.UUID{})
		return
	}
	if err != nil {
		slog.Warn("review gate repair lookup failed", "issue_id", util.UUIDToString(reviewIssue.ID), "task_id", util.UUIDToString(task.ID), "error", err)
	}

	targetIssue, ok := s.findReviewGateRepairTargetIssue(ctx, reviewIssue.ID)
	if !ok {
		s.createAgentComment(ctx, reviewIssue.ID, task.AgentID, redact.Text("Review gate failed, but no upstream agent-assigned implementation issue was found for automatic repair assignment."), "system", pgtype.UUID{})
		return
	}

	var repairIssue db.Issue
	err = s.runInTx(ctx, func(qtx *db.Queries) error {
		existing, err := qtx.ListOpenReviewGateRepairIssues(ctx, reviewIssue.ID)
		if err != nil {
			return fmt.Errorf("list open review gate repairs: %w", err)
		}
		if len(existing) > 0 {
			repairIssue = existing[0]
			return nil
		}
		number, err := qtx.IncrementIssueCounter(ctx, reviewIssue.WorkspaceID)
		if err != nil {
			return fmt.Errorf("allocate repair issue number: %w", err)
		}
		created, err := qtx.CreateIssueWithOrigin(ctx, db.CreateIssueWithOriginParams{
			WorkspaceID:   reviewIssue.WorkspaceID,
			Title:         reviewGateRepairTitle(reviewIssue),
			Description:   serviceStrOrNullText(formatReviewGateRepairDescription(nodeType, reviewIssue, targetIssue, review)),
			Status:        "todo",
			Priority:      reviewIssue.Priority,
			AssigneeType:  pgtype.Text{String: "agent", Valid: true},
			AssigneeID:    targetIssue.AssigneeID,
			CreatorType:   "agent",
			CreatorID:     task.AgentID,
			ParentIssueID: reviewIssue.ParentIssueID,
			Number:        number,
			ProjectID:     reviewIssue.ProjectID,
			OriginType:    pgtype.Text{String: reviewGateRepairOriginType, Valid: true},
			OriginID:      reviewIssue.ID,
		})
		if err != nil {
			return fmt.Errorf("create repair issue: %w", err)
		}
		repairIssue = created
		if _, err := qtx.CreateIssueDependency(ctx, db.CreateIssueDependencyParams{
			IssueID:          reviewIssue.ID,
			DependsOnIssueID: repairIssue.ID,
			Type:             "blocked_by",
		}); err != nil {
			return fmt.Errorf("link repair dependency: %w", err)
		}
		return nil
	})
	if err != nil {
		slog.Warn("review gate repair creation failed", "issue_id", util.UUIDToString(reviewIssue.ID), "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.broadcastIssueUpdated(reviewIssue)
	if repairIssue.ID.Valid {
		_, _ = s.EnqueueTaskForIssue(ctx, repairIssue)
		s.createAgentComment(ctx, reviewIssue.ID, task.AgentID, redact.Text(formatCreatedReviewGateRepairComment(repairIssue)), "system", pgtype.UUID{})
	}
}

func (s *TaskService) findReviewGateRepairTargetIssue(ctx context.Context, reviewIssueID pgtype.UUID) (db.Issue, bool) {
	visited := map[string]bool{}
	queue := []pgtype.UUID{reviewIssueID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		key := util.UUIDToString(current)
		if visited[key] {
			continue
		}
		visited[key] = true
		deps, err := s.Queries.ListIssueBlockingDependencies(ctx, current)
		if err != nil {
			return db.Issue{}, false
		}
		for _, dep := range deps {
			if s.isRepairTargetCandidate(ctx, dep) {
				return dep, true
			}
		}
		for _, dep := range deps {
			if _, isReviewGate := s.reviewGateNodeTypeForIssue(ctx, dep.ID); isReviewGate {
				queue = append(queue, dep.ID)
			}
		}
	}
	return db.Issue{}, false
}

func (s *TaskService) isRepairTargetCandidate(ctx context.Context, issue db.Issue) bool {
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return false
	}
	if issue.OriginType.Valid && issue.OriginType.String == reviewGateRepairOriginType {
		return false
	}
	if _, isReviewGate := s.reviewGateNodeTypeForIssue(ctx, issue.ID); isReviewGate {
		return false
	}
	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid || agent.IsInternal {
		return false
	}
	return true
}

func reviewGateRepairTitle(reviewIssue db.Issue) string {
	title := strings.TrimSpace(reviewIssue.Title)
	if title == "" {
		return "Fix review gate findings"
	}
	return "Fix review findings: " + title
}

func formatReviewGateRepairDescription(nodeType string, reviewIssue, targetIssue db.Issue, review reviewGateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s failed and requires a repair before downstream work can continue.", reviewGateLabel(nodeType))
	if review.Summary != "" {
		fmt.Fprintf(&b, "\n\nSummary:\n%s", review.Summary)
	}
	if len(review.Findings) > 0 {
		b.WriteString("\n\nBlocking findings:")
		for _, finding := range review.Findings {
			title := finding.Title
			if title == "" {
				title = finding.Details
			}
			fmt.Fprintf(&b, "\n- [%s] %s", finding.Severity, title)
			if finding.Details != "" && finding.Details != title {
				fmt.Fprintf(&b, ": %s", finding.Details)
			}
		}
	}
	fmt.Fprintf(&b, "\n\nReview issue: #%d %s", reviewIssue.Number, strings.TrimSpace(reviewIssue.Title))
	fmt.Fprintf(&b, "\nTarget implementation issue: #%d %s", targetIssue.Number, strings.TrimSpace(targetIssue.Title))
	if len(review.CheckedAgainst) > 0 {
		b.WriteString("\n\nChecked against:")
		for _, item := range review.CheckedAgainst {
			fmt.Fprintf(&b, "\n- %s", item)
		}
	}
	return strings.TrimSpace(b.String())
}

func formatCreatedReviewGateRepairComment(repairIssue db.Issue) string {
	return fmt.Sprintf("Created repair issue #%d and assigned it to the upstream implementation agent. This review gate remains blocked until that repair issue is done.", repairIssue.Number)
}

func formatExistingReviewGateRepairComment(repairIssue db.Issue) string {
	return fmt.Sprintf("Review gate remains blocked. Existing repair issue #%d is still open, so no duplicate repair issue was created.", repairIssue.Number)
}

func (s *TaskService) applyUnitTestChecklistCompleted(ctx context.Context, task db.AgentTaskQueue, result []byte) bool {
	if !task.IssueID.Valid || task.TriggerCommentID.Valid {
		return false
	}
	item, err := s.Queries.GetPlanItemByGeneratedIssue(ctx, task.IssueID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("unit test gate plan item lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		}
		return false
	}
	if normalizePlanItemExecutionKind(item.ExecutionKind) != PlanItemExecutionKindAgentTask {
		return false
	}
	fields, err := s.Queries.GetIssueUnitTestFields(ctx, task.IssueID)
	if err != nil {
		slog.Warn("unit test gate field lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return false
	}
	checks := NormalizeUnitTestChecklistJSON(fields.UnitTestChecklist)
	if len(checks) == 0 {
		return false
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("unit test gate issue lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return true
	}
	if issue.Status == "cancelled" {
		return true
	}

	output := taskCompletedOutput(result)
	report, parseErr := parseUnitTestReportOutput(output)
	nextChecks := checks
	nextStatus := UnitTestStatusFailed
	if parseErr == nil {
		nextChecks = applyUnitTestReport(checks, report, util.UUIDToString(task.ID), time.Now())
		nextStatus = UnitTestStatusForChecklist(nextChecks)
		if report.Status == UnitTestStatusFailed || nextStatus != UnitTestStatusPassed {
			nextStatus = UnitTestStatusFailed
		}
	}

	nextCount := fields.UnitTestIterationCount
	if nextStatus != UnitTestStatusPassed {
		nextCount++
	}
	if nextStatus != UnitTestStatusPassed && nextCount > UnitTestMaxIterations {
		nextStatus = UnitTestStatusBlocked
	}
	if _, err := s.Queries.UpdateIssueUnitTestFields(ctx, db.UpdateIssueUnitTestFieldsParams{
		ID:                     task.IssueID,
		UnitTestChecklist:      MarshalUnitTestChecklist(nextChecks),
		UnitTestStatus:         nextStatus,
		UnitTestIterationCount: nextCount,
		UnitTestLastTaskID:     task.ID,
	}); err != nil {
		slog.Warn("unit test gate update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return true
	}

	switch nextStatus {
	case UnitTestStatusPassed:
		updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: task.IssueID, Status: "done"})
		if err != nil {
			slog.Warn("unit test gate pass status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
			return true
		}
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(formatUnitTestGateComment(nextStatus, nextChecks, nextCount, nil, false)), "system", pgtype.UUID{})
		s.broadcastIssueUpdated(updated)
		s.enqueueUnblockedIssueTasks(ctx, task.IssueID)
	case UnitTestStatusBlocked:
		updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: task.IssueID, Status: "blocked"})
		if err != nil {
			slog.Warn("unit test gate blocked status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
			return true
		}
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(formatUnitTestGateComment(nextStatus, nextChecks, nextCount, parseErr, false)), "system", pgtype.UUID{})
		s.broadcastIssueUpdated(updated)
	default:
		updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: task.IssueID, Status: "todo"})
		if err != nil {
			slog.Warn("unit test gate retry status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
			return true
		}
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(formatUnitTestGateComment(nextStatus, nextChecks, nextCount, parseErr, true)), "system", pgtype.UUID{})
		s.broadcastIssueUpdated(updated)
		if _, err := s.EnqueueTaskForIssue(ctx, updated); err != nil {
			slog.Warn("unit test gate retry enqueue failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		}
	}
	return true
}

func formatUnitTestGateComment(status string, checks []UnitTestCheck, iteration int32, parseErr error, willRetry bool) string {
	var b strings.Builder
	switch status {
	case UnitTestStatusPassed:
		b.WriteString("Unit test checklist passed. The server marked this issue done.")
	case UnitTestStatusBlocked:
		fmt.Fprintf(&b, "Unit test checklist failed after %d iteration(s). The server marked this issue blocked.", iteration)
	default:
		fmt.Fprintf(&b, "Unit test checklist failed on iteration %d.", iteration)
		if willRetry {
			b.WriteString(" The server requeued this same issue for the same agent.")
		}
	}
	if parseErr != nil {
		fmt.Fprintf(&b, "\n\nReport error: %s", parseErr.Error())
	}
	if len(checks) > 0 {
		b.WriteString("\n\nChecks:")
		for _, check := range NormalizeUnitTestChecks(checks) {
			label := strings.TrimSpace(check.Title)
			if label == "" {
				label = strings.TrimSpace(check.Command)
			}
			if label == "" {
				label = check.ID
			}
			fmt.Fprintf(&b, "\n- %s: %s", label, check.Status)
			if check.FailureSummary != "" {
				fmt.Fprintf(&b, " - %s", check.FailureSummary)
			}
		}
	}
	return b.String()
}

func (s *TaskService) applyPlanAgentTaskCompleted(ctx context.Context, task db.AgentTaskQueue) bool {
	if !task.IssueID.Valid || task.TriggerCommentID.Valid {
		return false
	}
	item, err := s.Queries.GetPlanItemByGeneratedIssue(ctx, task.IssueID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("plan item execution contract lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		}
		return false
	}
	if normalizePlanItemExecutionKind(item.ExecutionKind) != PlanItemExecutionKindAgentTask {
		return false
	}
	issue, err := s.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		slog.Warn("plan agent task issue lookup failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return false
	}
	switch issue.Status {
	case "done", "blocked", "cancelled":
		return false
	}
	updated, err := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID:     task.IssueID,
		Status: "done",
	})
	if err != nil {
		slog.Warn("plan agent task status update failed", "issue_id", util.UUIDToString(task.IssueID), "task_id", util.UUIDToString(task.ID), "error", err)
		return false
	}
	s.broadcastIssueUpdated(updated)
	s.enqueueUnblockedIssueTasks(ctx, task.IssueID)
	return true
}

func taskCompletedOutput(result []byte) string {
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(util.UnescapeBackslashEscapes(payload.Output))
}

func parseReviewGateOutput(output string) (reviewGateResult, error) {
	var empty reviewGateResult
	if strings.TrimSpace(output) == "" {
		return empty, fmt.Errorf("review gate returned empty output")
	}
	for start := strings.Index(output, "{"); start >= 0; {
		var raw map[string]json.RawMessage
		decoder := json.NewDecoder(strings.NewReader(output[start:]))
		if err := decoder.Decode(&raw); err == nil {
			reviewRaw, ok := raw["review_gate"]
			if ok {
				var review reviewGateResult
				if err := json.Unmarshal(reviewRaw, &review); err != nil {
					return empty, fmt.Errorf("review_gate JSON is invalid: %v", err)
				}
				review = normalizeReviewGateResult(review)
				switch review.Status {
				case reviewGateStatusPass, reviewGateStatusFail:
					return review, nil
				default:
					return empty, fmt.Errorf("review_gate.status must be pass or fail")
				}
			}
		}
		next := strings.Index(output[start+1:], "{")
		if next < 0 {
			break
		}
		start += next + 1
	}
	return empty, fmt.Errorf("review gate output did not contain a JSON object with review_gate")
}

func normalizeReviewGateResult(review reviewGateResult) reviewGateResult {
	review.Status = strings.ToLower(strings.TrimSpace(review.Status))
	review.Summary = strings.TrimSpace(review.Summary)
	review.CheckedAgainst = normalizeStringSlice(review.CheckedAgainst)
	findings := make([]reviewGateFinding, 0, len(review.Findings))
	for _, finding := range review.Findings {
		severity := strings.ToLower(strings.TrimSpace(finding.Severity))
		if severity != "blocker" && severity != "major" && severity != "minor" {
			severity = "major"
		}
		title := strings.TrimSpace(finding.Title)
		details := strings.TrimSpace(finding.Details)
		if title == "" && details == "" {
			continue
		}
		findings = append(findings, reviewGateFinding{
			Severity: severity,
			Title:    title,
			Details:  details,
		})
	}
	review.Findings = findings
	return review
}

func formatReviewGateComment(nodeType string, review reviewGateResult) string {
	label := reviewGateLabel(nodeType)
	status := "failed"
	if review.Status == reviewGateStatusPass {
		status = "passed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s.", label, status)
	if review.Summary != "" {
		fmt.Fprintf(&b, "\n\n%s", review.Summary)
	}
	if len(review.Findings) > 0 {
		b.WriteString("\n\nFindings:")
		for _, finding := range review.Findings {
			title := finding.Title
			if title == "" {
				title = finding.Details
			}
			fmt.Fprintf(&b, "\n- [%s] %s", finding.Severity, title)
			if finding.Details != "" && finding.Details != title {
				fmt.Fprintf(&b, ": %s", finding.Details)
			}
		}
	}
	if len(review.CheckedAgainst) > 0 {
		b.WriteString("\n\nChecked against:")
		for _, item := range review.CheckedAgainst {
			fmt.Fprintf(&b, "\n- %s", item)
		}
	}
	return strings.TrimSpace(b.String())
}

func formatInvalidReviewGateComment(nodeType string, err error) string {
	return fmt.Sprintf("%s blocked because the agent did not return a valid review_gate JSON result: %v", reviewGateLabel(nodeType), err)
}

func reviewGateLabel(nodeType string) string {
	switch nodeType {
	case PipelineNodeTypeSpecReview:
		return "Spec review gate"
	case PipelineNodeTypeCodeReview:
		return "Code review gate"
	default:
		return "Review gate"
	}
}

func (s *TaskService) enqueueUnblockedIssueTasks(ctx context.Context, completedIssueID pgtype.UUID) {
	issues, err := s.Queries.ListIssuesUnblockedByIssue(ctx, completedIssueID)
	if err != nil {
		return
	}
	for _, issue := range issues {
		if !s.shouldEnqueueAgentTask(ctx, issue) {
			continue
		}
		hasPending, err := s.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: issue.AssigneeID,
		})
		if err != nil || hasPending {
			continue
		}
		_, _ = s.EnqueueTaskForIssue(ctx, issue)
	}
}

func (s *TaskService) shouldEnqueueAgentTask(ctx context.Context, issue db.Issue) bool {
	if issue.Status == "backlog" || issue.Status == "done" || issue.Status == "cancelled" {
		return false
	}
	count, err := s.Queries.CountOpenDependenciesForIssue(ctx, issue.ID)
	if err != nil || count > 0 {
		return false
	}
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return false
	}
	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid || agent.IsInternal {
		return false
	}
	return true
}

func (s *TaskService) writeIssuePlanSpec(ctx context.Context, planID pgtype.UUID, spec PlanSpec) error {
	spec = normalizePlanSpec(spec)
	return s.runInTx(ctx, func(qtx *db.Queries) error {
		existing, err := qtx.GetPlan(ctx, planID)
		if err != nil {
			return fmt.Errorf("load plan: %w", err)
		}
		spec = mergeExistingPlanClarifications(existing.Spec, spec)
		specJSON, err := marshalPlanSpec(spec)
		if err != nil {
			return fmt.Errorf("marshal plan spec: %w", err)
		}
		title := strings.TrimSpace(existing.Title)
		if title == "" {
			title = firstNonEmptyLine(spec.Summary)
		}
		if title == "" {
			title = firstNonEmptyLine(spec.Goal)
		}
		if _, err := qtx.MarkPlanSpecReview(ctx, db.MarkPlanSpecReviewParams{
			ID:    planID,
			Title: title,
			Spec:  specJSON,
		}); err != nil {
			return fmt.Errorf("mark plan spec review: %w", err)
		}
		if err := qtx.DeletePlanItems(ctx, planID); err != nil {
			return fmt.Errorf("delete old plan items: %w", err)
		}
		return nil
	})
}

func mergeExistingPlanClarifications(existingJSON []byte, spec PlanSpec) PlanSpec {
	if len(spec.Clarifications) > 0 || len(existingJSON) == 0 {
		return normalizePlanSpec(spec)
	}
	var existing PlanSpec
	if err := json.Unmarshal(existingJSON, &existing); err != nil {
		return normalizePlanSpec(spec)
	}
	existing = normalizePlanSpec(existing)
	if len(existing.Clarifications) == 0 {
		return normalizePlanSpec(spec)
	}
	spec.Clarifications = existing.Clarifications
	return normalizePlanSpec(spec)
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 120 {
				return line[:120]
			}
			return line
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *TaskService) writeIssuePlanResult(ctx context.Context, planID pgtype.UUID, result issuePlanResult) error {
	if !result.shouldCreatePlan() {
		direct, ok := result.directIssue()
		if !ok {
			return fmt.Errorf("planner output missing direct_issue")
		}
		selected := true
		direct.Selected = &selected
		result.Items = []issuePlanResultItem{direct}
		if strings.TrimSpace(result.Title) == "" {
			result.Title = strings.TrimSpace(direct.Title)
		}
	}
	return s.runInTx(ctx, func(qtx *db.Queries) error {
		existing, err := qtx.GetPlan(ctx, planID)
		if err != nil {
			return fmt.Errorf("load plan: %w", err)
		}
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = strings.TrimSpace(result.ParentIssue.Title)
		}
		if title == "" {
			title = existing.Title
		}
		parentTitle := strings.TrimSpace(result.ParentIssue.Title)
		if parentTitle == "" {
			parentTitle = strings.TrimSpace(existing.ParentTitle.String)
		}
		if parentTitle == "" {
			parentTitle = title
		}
		parentDescription := strings.TrimSpace(result.ParentIssue.Description)
		if parentDescription == "" {
			parentDescription = strings.TrimSpace(existing.ParentDescription.String)
		}
		plan, err := qtx.MarkPlanReady(ctx, db.MarkPlanReadyParams{
			ID:                planID,
			Title:             title,
			ParentTitle:       pgtype.Text{String: parentTitle, Valid: true},
			ParentDescription: pgtype.Text{String: parentDescription, Valid: parentDescription != ""},
		})
		if err != nil {
			return fmt.Errorf("mark plan ready: %w", err)
		}
		if err := qtx.DeletePlanItems(ctx, plan.ID); err != nil {
			return fmt.Errorf("delete old plan items: %w", err)
		}
		return s.createPlanItemsFromResult(ctx, qtx, plan, result.Items)
	})
}

func (s *TaskService) createPlanItemsFromResult(ctx context.Context, qtx *db.Queries, plan db.Plan, items []issuePlanResultItem) error {
	items = normalizeIssuePlanItemIterations(plan.Title, items)
	for i, item := range items {
		selected := true
		if item.Selected != nil {
			selected = *item.Selected
		}
		score := item.MatchScore
		var recommended pgtype.UUID
		if strings.TrimSpace(item.RecommendedAgentID) != "" && score >= 60 {
			agentID, err := util.ParseUUID(strings.TrimSpace(item.RecommendedAgentID))
			if err == nil {
				if agent, loadErr := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
					ID:          agentID,
					WorkspaceID: plan.WorkspaceID,
				}); loadErr == nil && !agent.ArchivedAt.Valid && agent.RuntimeID.Valid {
					recommended = agentID
				}
			}
		}
		if !recommended.Valid && score >= 60 {
			score = 0
		}
		if !recommended.Valid {
			selected = true
		}
		if item.ExecutionKind == PlanItemExecutionKindHumanConfirmation {
			recommended = pgtype.UUID{}
			score = 0
		}
		requiresGitCommit := itemRequiresGitCommit(item)
		dependsOnPositions := item.DependsOnPositions
		if dependsOnPositions == nil {
			dependsOnPositions = []int32{}
		}
		if _, err := qtx.CreatePlanItem(ctx, db.CreatePlanItemParams{
			PlanID:                plan.ID,
			Position:              int32(i + 1),
			Title:                 strings.TrimSpace(item.Title),
			Description:           strings.TrimSpace(item.Description),
			AcceptanceCriteria:    normalizeStringSlice(item.AcceptanceCriteria),
			SuggestedTestCommands: normalizeStringSlice(item.SuggestedTestCommands),
			UnitTestChecklist:     MarshalUnitTestChecklist(NormalizeUnitTestChecks(item.UnitTestChecklist)),
			ContextResources:      normalizeStringSlice(item.ContextResources),
			RiskNotes:             normalizeStringSlice(item.RiskNotes),
			NodeType:              item.NodeType,
			ExecutionKind:         item.ExecutionKind,
			ConfirmationQuestion:  strings.TrimSpace(item.ConfirmationQuestion),
			ConfirmationReason:    strings.TrimSpace(item.ConfirmationReason),
			RequiredEvidence:      normalizeStringSlice(item.RequiredEvidence),
			RequiresGitCommit:     requiresGitCommit,
			BranchName:            item.BranchName,
			IterationIndex:        item.IterationIndex,
			IterationTitle:        strings.TrimSpace(item.IterationTitle),
			IterationBranchName:   item.IterationBranchName,
			RecommendedAgentID:    recommended,
			MatchScore:            score,
			MatchReason:           strings.TrimSpace(item.MatchReason),
			MissingCapability:     strings.TrimSpace(item.MissingCapability),
			DependsOnPositions:    dependsOnPositions,
			Selected:              selected,
		}); err != nil {
			return fmt.Errorf("create plan item: %w", err)
		}
	}
	return nil
}

func normalizeIssuePlanItemIterations(planTitle string, items []issuePlanResultItem) []issuePlanResultItem {
	type iterationGroup struct {
		index        int32
		title        string
		branch       string
		branchLocked bool
	}
	normalized := make([]issuePlanResultItem, len(items))
	groups := make(map[int32]*iterationGroup)
	for i, item := range items {
		item = normalizeIssuePlanResultItemContract(item)
		item.IterationIndex = normalizePlanIterationIndex(item.IterationIndex)
		item.IterationTitle = strings.TrimSpace(item.IterationTitle)
		item.IterationBranchName = normalizeOptionalIssuePlanBranchName(item.IterationBranchName)
		item.BranchName = normalizeOptionalIssuePlanBranchName(item.BranchName)
		normalized[i] = item

		group := groups[item.IterationIndex]
		if group == nil {
			group = &iterationGroup{index: item.IterationIndex}
			groups[item.IterationIndex] = group
		}
		if group.title == "" && item.IterationTitle != "" {
			group.title = item.IterationTitle
		}
		if item.IterationBranchName != "" && !group.branchLocked {
			group.branch = item.IterationBranchName
			group.branchLocked = true
		}
		if itemRequiresGitCommit(item) && group.branch == "" && item.BranchName != "" {
			group.branch = item.BranchName
		}
	}
	for _, group := range groups {
		if group.branch == "" {
			group.branch = fallbackIterationBranchName(planTitle, group.index)
		}
	}
	for i, item := range normalized {
		group := groups[item.IterationIndex]
		if group == nil {
			continue
		}
		item.IterationTitle = group.title
		item.IterationBranchName = group.branch
		if itemRequiresGitCommit(item) {
			item.BranchName = group.branch
		} else {
			item.BranchName = ""
		}
		normalized[i] = item
	}
	return normalized
}

func (s *TaskService) markIssuePlanFailed(ctx context.Context, planID pgtype.UUID, msg string) {
	if _, err := s.Queries.MarkPlanFailed(ctx, db.MarkPlanFailedParams{ID: planID, Error: pgtype.Text{String: msg, Valid: true}}); err != nil {
		slog.Warn("issue-plan completion: failed to mark plan failed", "plan_id", util.UUIDToString(planID), "error", err)
	}
}

func (s *TaskService) writePipelinePlanDraftFromPlannerIssue(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, sourceIssueID, planID pgtype.UUID, result issuePlanResult) error {
	sourceIssue, err := s.Queries.GetIssue(ctx, sourceIssueID)
	if err != nil {
		return fmt.Errorf("source issue not found")
	}
	if sourceIssue.WorkspaceID != taskWorkspaceUUID(ip.WorkspaceID) {
		return fmt.Errorf("source issue workspace mismatch")
	}
	projectID := sourceIssue.ProjectID
	if ip.ProjectID != "" {
		if parsedProjectID, err := util.ParseUUID(ip.ProjectID); err == nil {
			projectID = parsedProjectID
		}
	}

	return s.runInTx(ctx, func(qtx *db.Queries) error {
		pipeline, stages, err := s.resolvePlannerPipeline(ctx, qtx, sourceIssue.WorkspaceID, result)
		if err != nil {
			return err
		}
		overrides, err := s.normalizePlannerPipelineOverrides(ctx, qtx, sourceIssue.WorkspaceID, result.Pipeline.Nodes)
		if err != nil {
			return err
		}
		if err := validatePlannerPipelineOverrides(stages, overrides); err != nil {
			return err
		}
		repoTargetsByStage, err := s.plannerPipelineRepoTargets(ctx, qtx, projectID, stages)
		if err != nil {
			return err
		}
		existing, err := qtx.GetPlan(ctx, planID)
		if err != nil {
			return fmt.Errorf("load plan: %w", err)
		}
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = strings.TrimSpace(result.ParentIssue.Title)
		}
		if title == "" {
			title = strings.TrimSpace(pipeline.Name)
		}
		if title == "" {
			title = existing.Title
		}
		parentTitle := strings.TrimSpace(result.ParentIssue.Title)
		if parentTitle == "" {
			parentTitle = strings.TrimSpace(existing.ParentTitle.String)
		}
		if parentTitle == "" {
			parentTitle = sourceIssue.Title
		}
		parentDescription := strings.TrimSpace(result.ParentIssue.Description)
		if parentDescription == "" {
			parentDescription = strings.TrimSpace(existing.ParentDescription.String)
		}
		plan, err := qtx.MarkPlanReady(ctx, db.MarkPlanReadyParams{
			ID:                planID,
			Title:             title,
			ParentTitle:       pgtype.Text{String: parentTitle, Valid: true},
			ParentDescription: pgtype.Text{String: parentDescription, Valid: parentDescription != ""},
		})
		if err != nil {
			return fmt.Errorf("mark plan ready: %w", err)
		}
		if err := qtx.DeletePlanItems(ctx, plan.ID); err != nil {
			return fmt.Errorf("delete old plan items: %w", err)
		}
		items := pipelinePlanItemsFromStages(stages, overrides, repoTargetsByStage)
		return s.createPlanItemsFromResult(ctx, qtx, plan, items)
	})
}

func (s *TaskService) writePipelinePlanDraft(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, planID pgtype.UUID, result issuePlanResult) error {
	return s.runInTx(ctx, func(qtx *db.Queries) error {
		existing, err := qtx.GetPlan(ctx, planID)
		if err != nil {
			return fmt.Errorf("load plan: %w", err)
		}
		projectID := existing.ProjectID
		if ip.ProjectID != "" {
			if parsedProjectID, err := util.ParseUUID(ip.ProjectID); err == nil {
				projectID = parsedProjectID
			}
		}
		pipeline, stages, err := s.resolvePlannerPipeline(ctx, qtx, existing.WorkspaceID, result)
		if err != nil {
			return err
		}
		overrides, err := s.normalizePlannerPipelineOverrides(ctx, qtx, existing.WorkspaceID, result.Pipeline.Nodes)
		if err != nil {
			return err
		}
		if err := validatePlannerPipelineOverrides(stages, overrides); err != nil {
			return err
		}
		repoTargetsByStage, err := s.plannerPipelineRepoTargets(ctx, qtx, projectID, stages)
		if err != nil {
			return err
		}
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = strings.TrimSpace(result.ParentIssue.Title)
		}
		if title == "" {
			title = strings.TrimSpace(pipeline.Name)
		}
		if title == "" {
			title = existing.Title
		}
		parentTitle := strings.TrimSpace(result.ParentIssue.Title)
		if parentTitle == "" {
			parentTitle = strings.TrimSpace(existing.ParentTitle.String)
		}
		parentDescription := strings.TrimSpace(result.ParentIssue.Description)
		if parentDescription == "" {
			parentDescription = strings.TrimSpace(existing.ParentDescription.String)
		}
		plan, err := qtx.MarkPlanReady(ctx, db.MarkPlanReadyParams{
			ID:                planID,
			Title:             title,
			ParentTitle:       pgtype.Text{String: parentTitle, Valid: parentTitle != ""},
			ParentDescription: pgtype.Text{String: parentDescription, Valid: parentDescription != ""},
		})
		if err != nil {
			return fmt.Errorf("mark plan ready: %w", err)
		}
		if err := qtx.DeletePlanItems(ctx, plan.ID); err != nil {
			return fmt.Errorf("delete old plan items: %w", err)
		}
		items := pipelinePlanItemsFromStages(stages, overrides, repoTargetsByStage)
		return s.createPlanItemsFromResult(ctx, qtx, plan, items)
	})
}

func pipelinePlanItemsFromStages(stages []db.PipelineStage, overrides map[string]plannerPipelineOverride, repoTargetsByStage map[string][]plannerPipelineRepoTarget) []issuePlanResultItem {
	items := make([]issuePlanResultItem, 0, len(stages))
	for i, stage := range stages {
		override := overrides[stage.Key]
		title := strings.TrimSpace(override.title)
		if title == "" {
			title = stage.Title
		}
		description := strings.TrimSpace(override.description)
		if description == "" {
			description = stage.Description
		}
		nodeType := NormalizePlanItemNodeType(stage.NodeType)
		description = plannerPipelineIssueDescription(description, nodeType, repoTargetsByStage[stage.Key])
		agentID := stage.AgentID
		if override.agentID.Valid {
			agentID = override.agentID
		}
		agentIDString := ""
		if agentID.Valid {
			agentIDString = util.UUIDToString(agentID)
		}
		executionKind := normalizePlanItemExecutionKind(override.executionKind)
		if stage.NodeType == "manual" {
			executionKind = PlanItemExecutionKindHumanConfirmation
			agentID = pgtype.UUID{}
			agentIDString = ""
		}
		selected := !override.skip
		items = append(items, issuePlanResultItem{
			Title:                 title,
			Description:           description,
			AcceptanceCriteria:    override.acceptanceCriteria,
			SuggestedTestCommands: override.suggestedTestCommands,
			UnitTestChecklist:     override.unitTestChecklist,
			ContextResources:      override.contextResources,
			RiskNotes:             override.riskNotes,
			NodeType:              nodeType,
			ExecutionKind:         executionKind,
			ConfirmationQuestion:  override.confirmationQuestion,
			ConfirmationReason:    override.confirmationReason,
			RequiredEvidence:      override.requiredEvidence,
			RequiresGitCommit:     override.requiresGitCommit,
			BranchName:            override.branchName,
			RecommendedAgentID:    agentIDString,
			MatchScore:            plannerMatchScore(agentID),
			MatchReason:           "Selected by pipeline node assignment.",
			MissingCapability:     "",
			DependsOnPositions:    plannerDependencyPositions(stages, i, overrides),
			Selected:              &selected,
		})
	}
	return items
}

func (s *TaskService) createPipelinePlanFromPlannerIssue(ctx context.Context, task db.AgentTaskQueue, ip IssuePlanContext, sourceIssueID pgtype.UUID, result issuePlanResult) (pgtype.UUID, []db.Issue, error) {
	requesterID, err := util.ParseUUID(ip.RequesterID)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("invalid requester id")
	}
	sourceIssue, err := s.Queries.GetIssue(ctx, sourceIssueID)
	if err != nil {
		return pgtype.UUID{}, nil, fmt.Errorf("source issue not found")
	}
	if sourceIssue.WorkspaceID != taskWorkspaceUUID(ip.WorkspaceID) {
		return pgtype.UUID{}, nil, fmt.Errorf("source issue workspace mismatch")
	}
	projectID := sourceIssue.ProjectID
	if ip.ProjectID != "" {
		if parsedProjectID, err := util.ParseUUID(ip.ProjectID); err == nil {
			projectID = parsedProjectID
		}
	}

	var createdChildren []db.Issue
	var createdPlanID pgtype.UUID
	err = s.runInTx(ctx, func(qtx *db.Queries) error {
		pipeline, stages, err := s.resolvePlannerPipeline(ctx, qtx, sourceIssue.WorkspaceID, result)
		if err != nil {
			return err
		}
		overrides, err := s.normalizePlannerPipelineOverrides(ctx, qtx, sourceIssue.WorkspaceID, result.Pipeline.Nodes)
		if err != nil {
			return err
		}
		if err := validatePlannerPipelineOverrides(stages, overrides); err != nil {
			return err
		}
		repoTargetsByStage, err := s.plannerPipelineRepoTargets(ctx, qtx, projectID, stages)
		if err != nil {
			return err
		}
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "Plan: " + sourceIssue.Title
		}
		plan, err := qtx.CreatePlan(ctx, db.CreatePlanParams{
			WorkspaceID:    sourceIssue.WorkspaceID,
			Title:          title,
			Prompt:         ip.Prompt,
			PlannerAgentID: task.AgentID,
			CreatedBy:      requesterID,
			ProjectID:      projectID,
		})
		if err != nil {
			return fmt.Errorf("create plan: %w", err)
		}
		createdPlanID = plan.ID
		if _, err := qtx.SetPlanTask(ctx, db.SetPlanTaskParams{ID: plan.ID, TaskID: task.ID}); err != nil {
			return fmt.Errorf("link plan task: %w", err)
		}

		run, err := qtx.CreatePipelineRun(ctx, db.CreatePipelineRunParams{
			PipelineID:    pipeline.ID,
			WorkspaceID:   sourceIssue.WorkspaceID,
			ProjectID:     projectID,
			ParentIssueID: sourceIssue.ID,
			Status:        "completed",
			CreatedBy:     requesterID,
		})
		if err != nil {
			return fmt.Errorf("create pipeline run: %w", err)
		}

		issuesByStageKey := make(map[string]db.Issue, len(stages))
		for i, stage := range stages {
			override := overrides[stage.Key]
			if override.skip {
				continue
			}
			title := strings.TrimSpace(override.title)
			if title == "" {
				title = stage.Title
			}
			description := strings.TrimSpace(override.description)
			if description == "" {
				description = stage.Description
			}
			nodeType := NormalizePlanItemNodeType(stage.NodeType)
			description = plannerPipelineIssueDescription(description, nodeType, repoTargetsByStage[stage.Key])
			agentID := stage.AgentID
			if override.agentID.Valid {
				agentID = override.agentID
			}
			executionKind := normalizePlanItemExecutionKind(override.executionKind)
			if stage.NodeType == "manual" {
				executionKind = PlanItemExecutionKindHumanConfirmation
				agentID = pgtype.UUID{}
			}
			depPositions := plannerDependencyPositions(stages, i, overrides)
			itemContract := normalizeIssuePlanResultItemContract(issuePlanResultItem{
				Title:                title,
				Description:          description,
				NodeType:             nodeType,
				ExecutionKind:        executionKind,
				ConfirmationQuestion: override.confirmationQuestion,
				ConfirmationReason:   override.confirmationReason,
				RequiredEvidence:     override.requiredEvidence,
				RequiresGitCommit:    override.requiresGitCommit,
				BranchName:           override.branchName,
				RecommendedAgentID:   util.UUIDToString(agentID),
				MatchScore:           plannerMatchScore(agentID),
				MatchReason:          "Selected by pipeline node assignment.",
			})
			requiresGitCommit := itemRequiresGitCommit(itemContract)
			branchName := ""
			if requiresGitCommit {
				branchName = normalizeIssuePlanBranchName(itemContract.BranchName, itemContract.Title)
			}
			item, err := qtx.CreatePlanItem(ctx, db.CreatePlanItemParams{
				PlanID:                plan.ID,
				Position:              int32(i + 1),
				Title:                 title,
				Description:           description,
				AcceptanceCriteria:    override.acceptanceCriteria,
				SuggestedTestCommands: override.suggestedTestCommands,
				UnitTestChecklist:     MarshalUnitTestChecklist(NormalizeUnitTestChecks(itemContract.UnitTestChecklist)),
				ContextResources:      override.contextResources,
				RiskNotes:             override.riskNotes,
				NodeType:              itemContract.NodeType,
				ExecutionKind:         itemContract.ExecutionKind,
				ConfirmationQuestion:  itemContract.ConfirmationQuestion,
				ConfirmationReason:    itemContract.ConfirmationReason,
				RequiredEvidence:      itemContract.RequiredEvidence,
				RequiresGitCommit:     requiresGitCommit,
				BranchName:            branchName,
				IterationIndex:        1,
				IterationTitle:        "",
				IterationBranchName:   branchName,
				RecommendedAgentID:    agentID,
				MatchScore:            itemContract.MatchScore,
				MatchReason:           itemContract.MatchReason,
				MissingCapability:     "",
				DependsOnPositions:    depPositions,
				Selected:              true,
			})
			if err != nil {
				return fmt.Errorf("create plan item: %w", err)
			}
			number, err := qtx.IncrementIssueCounter(ctx, sourceIssue.WorkspaceID)
			if err != nil {
				return fmt.Errorf("allocate issue number: %w", err)
			}
			assigneeType := pgtype.Text{}
			if agentID.Valid {
				assigneeType = pgtype.Text{String: "agent", Valid: true}
			}
			unitTestChecklist := MarshalUnitTestChecklist(NormalizeUnitTestChecks(itemContract.UnitTestChecklist))
			child, err := qtx.CreateIssueWithOriginAndUnitTestsManual(ctx, db.CreateIssueWithOriginAndUnitTestsManualParams{
				WorkspaceID:       sourceIssue.WorkspaceID,
				Title:             title,
				Description:       serviceStrOrNullText(description),
				Status:            "todo",
				Priority:          "none",
				AssigneeType:      assigneeType,
				AssigneeID:        agentID,
				CreatorType:       "member",
				CreatorID:         requesterID,
				ParentIssueID:     sourceIssue.ID,
				Number:            number,
				ProjectID:         projectID,
				OriginType:        pgtype.Text{String: "plan_item", Valid: true},
				OriginID:          item.ID,
				UnitTestChecklist: unitTestChecklist,
			})
			if err != nil {
				return fmt.Errorf("create pipeline child issue: %w", err)
			}
			issuesByStageKey[stage.Key] = child
			createdChildren = append(createdChildren, child)
			if _, err := qtx.UpdatePlanItemGeneratedIssue(ctx, db.UpdatePlanItemGeneratedIssueParams{
				ID:               item.ID,
				GeneratedIssueID: child.ID,
			}); err != nil {
				return fmt.Errorf("link plan item issue: %w", err)
			}
			if _, err := qtx.CreatePipelineRunStage(ctx, db.CreatePipelineRunStageParams{
				PipelineRunID:   run.ID,
				PipelineStageID: stage.ID,
				StageKey:        stage.Key,
				IssueID:         child.ID,
			}); err != nil {
				return fmt.Errorf("record pipeline run node: %w", err)
			}
		}
		for _, stage := range stages {
			child, ok := issuesByStageKey[stage.Key]
			if !ok {
				continue
			}
			depKeys := stage.DependsOnStageKeys
			if override, ok := overrides[stage.Key]; ok && len(override.dependsOnKeys) > 0 {
				depKeys = override.dependsOnKeys
			}
			for _, depKey := range depKeys {
				dep, ok := issuesByStageKey[depKey]
				if !ok {
					continue
				}
				if _, err := qtx.CreateIssueDependency(ctx, db.CreateIssueDependencyParams{
					IssueID:          child.ID,
					DependsOnIssueID: dep.ID,
					Type:             "blocked_by",
				}); err != nil {
					return fmt.Errorf("create issue dependency: %w", err)
				}
			}
		}
		if _, err := qtx.MarkPlanCommitted(ctx, db.MarkPlanCommittedParams{
			ID:            plan.ID,
			ParentIssueID: sourceIssue.ID,
		}); err != nil {
			return fmt.Errorf("mark plan committed: %w", err)
		}
		return nil
	})
	if err != nil {
		return pgtype.UUID{}, nil, err
	}
	return createdPlanID, createdChildren, nil
}

type plannerPipelineOverride struct {
	title                 string
	description           string
	acceptanceCriteria    []string
	suggestedTestCommands []string
	unitTestChecklist     []UnitTestCheck
	contextResources      []string
	riskNotes             []string
	executionKind         string
	confirmationQuestion  string
	confirmationReason    string
	requiredEvidence      []string
	requiresGitCommit     *bool
	branchName            string
	agentID               pgtype.UUID
	dependsOnKeys         []string
	skip                  bool
}

type plannerPipelineRepoTarget struct {
	Key string
	URL string
}

func (s *TaskService) resolvePlannerPipeline(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, result issuePlanResult) (db.Pipeline, []db.PipelineStage, error) {
	pipelineIDRaw := strings.TrimSpace(result.PipelineID)
	if pipelineIDRaw == "" {
		pipelineIDRaw = strings.TrimSpace(result.Pipeline.ID)
	}
	var pipeline db.Pipeline
	var err error
	if pipelineIDRaw != "" {
		pipelineID, parseErr := util.ParseUUID(pipelineIDRaw)
		if parseErr != nil {
			return db.Pipeline{}, nil, fmt.Errorf("pipeline_id is invalid")
		}
		pipeline, err = qtx.GetPipelineInWorkspace(ctx, db.GetPipelineInWorkspaceParams{ID: pipelineID, WorkspaceID: workspaceID})
		if err != nil {
			return db.Pipeline{}, nil, fmt.Errorf("pipeline not found")
		}
	} else {
		name := strings.TrimSpace(result.PipelineName)
		if name == "" {
			name = strings.TrimSpace(result.Pipeline.Name)
		}
		if name == "" {
			return db.Pipeline{}, nil, fmt.Errorf("planner output missing pipeline_id or pipeline_name")
		}
		pipelines, err := qtx.ListPipelines(ctx, workspaceID)
		if err != nil {
			return db.Pipeline{}, nil, fmt.Errorf("list pipelines: %w", err)
		}
		for _, p := range pipelines {
			if strings.EqualFold(strings.TrimSpace(p.Name), name) {
				pipeline = p
				break
			}
		}
		if !pipeline.ID.Valid {
			return db.Pipeline{}, nil, fmt.Errorf("pipeline %q not found", name)
		}
	}
	if pipeline.ArchivedAt.Valid {
		return db.Pipeline{}, nil, fmt.Errorf("pipeline is archived")
	}
	stages, err := qtx.ListPipelineStages(ctx, pipeline.ID)
	if err != nil {
		return db.Pipeline{}, nil, fmt.Errorf("list pipeline nodes: %w", err)
	}
	if len(stages) == 0 {
		return db.Pipeline{}, nil, fmt.Errorf("pipeline has no nodes")
	}
	return pipeline, stages, nil
}

func (s *TaskService) normalizePlannerPipelineOverrides(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, nodes []issuePlanPipelineNode) (map[string]plannerPipelineOverride, error) {
	out := make(map[string]plannerPipelineOverride, len(nodes))
	for _, node := range nodes {
		key := strings.TrimSpace(node.Key)
		if key == "" {
			continue
		}
		override := plannerPipelineOverride{
			title:                 strings.TrimSpace(node.Title),
			description:           strings.TrimSpace(node.Description),
			acceptanceCriteria:    normalizeStringSlice(node.AcceptanceCriteria),
			suggestedTestCommands: normalizeStringSlice(node.SuggestedTestCommands),
			unitTestChecklist:     NormalizeUnitTestChecks(node.UnitTestChecklist),
			contextResources:      normalizeStringSlice(node.ContextResources),
			riskNotes:             normalizeStringSlice(node.RiskNotes),
			executionKind:         normalizePlanItemExecutionKind(node.ExecutionKind),
			confirmationQuestion:  strings.TrimSpace(node.ConfirmationQuestion),
			confirmationReason:    strings.TrimSpace(node.ConfirmationReason),
			requiredEvidence:      normalizeStringSlice(node.RequiredEvidence),
			requiresGitCommit:     node.RequiresGitCommit,
			branchName:            normalizeIssuePlanBranchName(node.BranchName, node.Title),
			dependsOnKeys:         normalizeStringSlice(node.DependsOnNodeKeys),
		}
		if node.Selected != nil && !*node.Selected {
			override.skip = true
		}
		if strings.TrimSpace(node.AgentID) != "" {
			agentID, err := util.ParseUUID(strings.TrimSpace(node.AgentID))
			if err != nil {
				return nil, fmt.Errorf("node %s agent_id is invalid", key)
			}
			agent, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
			if err != nil || agent.ArchivedAt.Valid {
				return nil, fmt.Errorf("node %s agent is not active", key)
			}
			override.agentID = agentID
		}
		out[key] = override
	}
	return out, nil
}

func validatePlannerPipelineOverrides(stages []db.PipelineStage, overrides map[string]plannerPipelineOverride) error {
	stageIndex := make(map[string]int, len(stages))
	for i, stage := range stages {
		stageIndex[stage.Key] = i
	}
	for key, override := range overrides {
		index, ok := stageIndex[key]
		if !ok {
			return fmt.Errorf("planner output references unknown pipeline node %q", key)
		}
		for _, depKey := range override.dependsOnKeys {
			depIndex, ok := stageIndex[depKey]
			if !ok {
				return fmt.Errorf("planner output node %s references unknown dependency %q", key, depKey)
			}
			if depIndex >= index {
				return fmt.Errorf("planner output node %s dependency %q must be an earlier pipeline node", key, depKey)
			}
		}
	}
	return nil
}

func plannerDependencyPositions(stages []db.PipelineStage, index int, overrides map[string]plannerPipelineOverride) []int32 {
	keys := stages[index].DependsOnStageKeys
	if override, ok := overrides[stages[index].Key]; ok && len(override.dependsOnKeys) > 0 {
		keys = override.dependsOnKeys
	}
	if len(keys) == 0 {
		return []int32{}
	}
	positionByKey := make(map[string]int32, len(stages))
	for i, stage := range stages {
		positionByKey[stage.Key] = int32(i + 1)
	}
	out := make([]int32, 0, len(keys))
	for _, key := range keys {
		if pos, ok := positionByKey[key]; ok && pos < int32(index+1) {
			out = append(out, pos)
		}
	}
	return out
}

func plannerMatchScore(agentID pgtype.UUID) int32 {
	if agentID.Valid {
		return 100
	}
	return 0
}

func normalizePlanItemExecutionKind(kind string) string {
	if strings.TrimSpace(kind) == PlanItemExecutionKindHumanConfirmation {
		return PlanItemExecutionKindHumanConfirmation
	}
	return PlanItemExecutionKindAgentTask
}

func NormalizePlanItemNodeType(nodeType string) string {
	switch strings.TrimSpace(nodeType) {
	case PipelineNodeTypeManual:
		return PipelineNodeTypeManual
	case PipelineNodeTypeCheck:
		return PipelineNodeTypeCheck
	case PipelineNodeTypeSpecReview:
		return PipelineNodeTypeSpecReview
	case PipelineNodeTypeCodeReview:
		return PipelineNodeTypeCodeReview
	default:
		return PipelineNodeTypeIssue
	}
}

func IsReviewGateNodeType(nodeType string) bool {
	nodeType = NormalizePlanItemNodeType(nodeType)
	return nodeType == PipelineNodeTypeSpecReview || nodeType == PipelineNodeTypeCodeReview
}

func ReviewGateContract(nodeType string) string {
	switch NormalizePlanItemNodeType(nodeType) {
	case PipelineNodeTypeSpecReview:
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

Use "pass" only when the implementation satisfies the requested spec. Use "fail" when downstream work must stay blocked.`
	case PipelineNodeTypeCodeReview:
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

Use "pass" only when the code quality review has no blocking findings. Use "fail" when downstream work must stay blocked.`
	default:
		return ""
	}
}

func normalizeIssuePlanResultItemContract(item issuePlanResultItem) issuePlanResultItem {
	item.NodeType = NormalizePlanItemNodeType(item.NodeType)
	item.ExecutionKind = normalizePlanItemExecutionKind(item.ExecutionKind)
	item.ConfirmationQuestion = strings.TrimSpace(item.ConfirmationQuestion)
	item.ConfirmationReason = strings.TrimSpace(item.ConfirmationReason)
	item.RequiredEvidence = normalizeStringSlice(item.RequiredEvidence)
	item.UnitTestChecklist = NormalizeUnitTestChecks(item.UnitTestChecklist)
	item.BranchName = normalizeOptionalIssuePlanBranchName(item.BranchName)
	item.IterationIndex = normalizePlanIterationIndex(item.IterationIndex)
	item.IterationTitle = strings.TrimSpace(item.IterationTitle)
	item.IterationBranchName = normalizeOptionalIssuePlanBranchName(item.IterationBranchName)
	if item.ExecutionKind != PlanItemExecutionKindHumanConfirmation {
		if item.RequiresGitCommit == nil {
			v := true
			item.RequiresGitCommit = &v
		}
		if item.RequiresGitCommit != nil && !*item.RequiresGitCommit {
			item.BranchName = ""
		}
		item.ConfirmationQuestion = ""
		item.ConfirmationReason = ""
		item.RequiredEvidence = []string{}
		return item
	}
	item.NodeType = PipelineNodeTypeManual
	if item.ConfirmationQuestion == "" {
		item.ConfirmationQuestion = strings.TrimSpace(item.Title)
	}
	if item.ConfirmationReason == "" {
		item.ConfirmationReason = strings.TrimSpace(item.Description)
	}
	if item.ConfirmationReason == "" {
		item.ConfirmationReason = "Human confirmation is required before downstream work can proceed."
	}
	item.RecommendedAgentID = ""
	item.MatchScore = 0
	item.MatchReason = "Waiting for human confirmation."
	v := false
	item.RequiresGitCommit = &v
	item.BranchName = ""
	return item
}

func itemRequiresGitCommit(item issuePlanResultItem) bool {
	if normalizePlanItemExecutionKind(item.ExecutionKind) == PlanItemExecutionKindHumanConfirmation {
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

func normalizeOptionalIssuePlanBranchName(raw string) string {
	branch := strings.ToLower(strings.TrimSpace(raw))
	branch = strings.ReplaceAll(branch, "\\", "/")
	if branch == "" {
		return ""
	}
	parts := strings.Split(branch, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = SlugifyPlanBranchSegment(part)
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

func fallbackIterationBranchName(planTitle string, iterationIndex int32) string {
	planSlug := SlugifyPlanBranchSegment(planTitle)
	if planSlug == "" {
		planSlug = "plan"
	}
	return fmt.Sprintf("feature/%s-iter-%d", planSlug, normalizePlanIterationIndex(iterationIndex))
}

func normalizeIssuePlanBranchName(raw, title string) string {
	branch := strings.ToLower(strings.TrimSpace(raw))
	branch = strings.ReplaceAll(branch, "\\", "/")
	parts := strings.Split(branch, "/")
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = SlugifyPlanBranchSegment(part)
		if part != "" {
			cleanParts = append(cleanParts, part)
		}
	}
	branch = strings.Join(cleanParts, "/")
	if branch == "" {
		titleSlug := SlugifyPlanBranchSegment(title)
		if titleSlug == "" {
			titleSlug = "plan-item"
		}
		branch = "feature/" + titleSlug
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

func SlugifyPlanBranchSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.TrimRight(out[:48], "-")
	}
	return out
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *TaskService) plannerPipelineRepoTargets(ctx context.Context, qtx *db.Queries, projectID pgtype.UUID, stages []db.PipelineStage) (map[string][]plannerPipelineRepoTarget, error) {
	targets := map[string][]plannerPipelineRepoTarget{}
	needsRepos := false
	for _, stage := range stages {
		if len(normalizeStringSlice(stage.RepoKeys)) > 0 {
			needsRepos = true
			break
		}
	}
	if !needsRepos {
		return targets, nil
	}
	if !projectID.Valid {
		return nil, fmt.Errorf("pipeline nodes reference repos but source issue has no project")
	}
	resources, err := qtx.ListProjectResources(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project resources: %w", err)
	}
	reposByKey := map[string]plannerPipelineRepoTarget{}
	for _, resource := range resources {
		if resource.ResourceType != "git_repo" && resource.ResourceType != "github_repo" {
			continue
		}
		key := strings.TrimSpace(resource.Label.String)
		if key == "" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
			continue
		}
		url := strings.TrimSpace(ref.URL)
		if url == "" {
			continue
		}
		reposByKey[key] = plannerPipelineRepoTarget{Key: key, URL: url}
	}
	for _, stage := range stages {
		for _, key := range normalizeStringSlice(stage.RepoKeys) {
			target, ok := reposByKey[key]
			if !ok {
				return nil, fmt.Errorf("node %s references unknown repo %q", stage.Key, key)
			}
			targets[stage.Key] = append(targets[stage.Key], target)
		}
	}
	return targets, nil
}

func plannerPipelineIssueDescription(description, nodeType string, repos []plannerPipelineRepoTarget) string {
	description = strings.TrimSpace(description)
	reviewContract := ReviewGateContract(nodeType)
	if strings.Contains(strings.ToLower(description), "review_gate") {
		reviewContract = ""
	}
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

func serviceStrOrNullText(s string) pgtype.Text {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func taskWorkspaceUUID(workspaceID string) pgtype.UUID {
	u, _ := util.ParseUUID(workspaceID)
	return u
}

func (s *TaskService) writePlannerIssueComment(ctx context.Context, issueID, agentID pgtype.UUID, content string) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{},
	})
	if err != nil {
		slog.Warn("planner issue comment: failed to create comment", "issue_id", util.UUIDToString(issueID), "error", err)
		return
	}
	if s.Bus == nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment_id": util.UUIDToString(comment.ID),
			"issue_id":   util.UUIDToString(issue.ID),
			"content":    comment.Content,
		},
	})
}

// notifyQuickCreateCompleted writes a success inbox notification to the
// requester pointing at the issue the agent just created. The issue is
// stamped with origin_type=quick_create + origin_id=<task_id> by the
// daemon-injected MULTICA_QUICK_CREATE_TASK_ID env var, so this lookup is
// deterministic — robust against the same agent creating other issues in
// parallel (e.g. assignment task running while max_concurrent_tasks > 1
// permits another quick-create alongside it).
func (s *TaskService) notifyQuickCreateCompleted(ctx context.Context, task db.AgentTaskQueue, qc QuickCreateContext) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		slog.Warn("quick-create completion: invalid requester id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		slog.Warn("quick-create completion: invalid workspace id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	issue, err := s.Queries.GetIssueByOrigin(ctx, db.GetIssueByOriginParams{
		WorkspaceID: workspaceID,
		OriginType:  pgtype.Text{String: "quick_create", Valid: true},
		OriginID:    task.ID,
	})
	if err != nil {
		// No issue created — agent ran to completion but the CLI call must
		// have failed. Surface as a failure inbox so the user sees something.
		slog.Warn("quick-create completion: no issue found, writing failure inbox",
			"task_id", util.UUIDToString(task.ID),
			"agent_id", util.UUIDToString(task.AgentID),
			"workspace_id", qc.WorkspaceID,
		)
		s.notifyQuickCreateFailed(ctx, task, qc, "agent finished without creating an issue")
		return
	}

	// Link the new issue back to this task so subsequent reads of the task
	// (Activity tab, Recent work, etc.) render it as a normal issue task
	// (kind = "direct") instead of staying on the "Creating issue" active-
	// wording label. Best-effort: a write failure here doesn't block the
	// inbox notification, which is the more important signal to the user.
	if err := s.Queries.LinkTaskToIssue(ctx, db.LinkTaskToIssueParams{
		ID:      task.ID,
		IssueID: issue.ID,
	}); err != nil {
		slog.Warn("quick-create completion: link task→issue failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", err,
		)
	}

	// Subscribe the requester so they receive notifications for follow-up
	// comments and updates. The DB row's creator_type/creator_id is the
	// agent (it ran the CLI), but the human who triggered the quick-create
	// is the semantic creator from a UX perspective — without this they
	// only see the one-shot completion inbox and miss everything after.
	// Best-effort: log on failure but don't block the inbox notification.
	if err := s.Queries.AddIssueSubscriber(ctx, db.AddIssueSubscriberParams{
		IssueID:  issue.ID,
		UserType: "member",
		UserID:   requesterID,
		Reason:   "creator",
	}); err != nil {
		slog.Warn("quick-create completion: subscribe requester failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"requester_id", qc.RequesterID,
			"error", err,
		)
	} else {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventSubscriberAdded,
			WorkspaceID: qc.WorkspaceID,
			ActorType:   "agent",
			ActorID:     util.UUIDToString(task.AgentID),
			Payload: map[string]any{
				"issue_id":  util.UUIDToString(issue.ID),
				"user_type": "member",
				"user_id":   qc.RequesterID,
				"reason":    "creator",
			},
		})
	}
	prefix := s.getIssuePrefix(workspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"issue_id":        util.UUIDToString(issue.ID),
		"identifier":      identifier,
		"original_prompt": qc.Prompt,
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_done",
		Severity:      "info",
		IssueID:       issue.ID,
		Title:         issue.Title,
		Body:          pgtype.Text{},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create completion: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), issue.Status)
}

// notifyQuickCreateFailed writes a failure inbox notification carrying the
// original prompt + agent ID so the frontend can render an "Edit as
// advanced form" entry that pre-fills the legacy create-issue modal
// without asking the user to retype.
func (s *TaskService) notifyQuickCreateFailed(ctx context.Context, task db.AgentTaskQueue, qc QuickCreateContext, errMsg string) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		return
	}
	if errMsg == "" {
		errMsg = "Quick create did not finish successfully"
	}
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"original_prompt": qc.Prompt,
		"error":           redact.Text(errMsg),
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_failed",
		Severity:      "action_required",
		IssueID:       pgtype.UUID{},
		Title:         "Quick create failed",
		Body:          pgtype.Text{String: redact.Text(errMsg), Valid: true},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create failure: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), "")
}

// publishQuickCreateInbox emits the WS event so the requester's inbox list
// updates immediately. Mirrors the payload shape used by the other inbox
// listeners (notification_listeners.go).
func (s *TaskService) publishQuickCreateInbox(item db.InboxItem, workspaceID, agentID, issueStatus string) {
	resp := map[string]any{
		"id":             util.UUIDToString(item.ID),
		"workspace_id":   util.UUIDToString(item.WorkspaceID),
		"recipient_type": item.RecipientType,
		"recipient_id":   util.UUIDToString(item.RecipientID),
		"type":           item.Type,
		"severity":       item.Severity,
		"issue_id":       util.UUIDToPtr(item.IssueID),
		"title":          item.Title,
		"body":           util.TextToPtr(item.Body),
		"read":           item.Read,
		"archived":       item.Archived,
		"created_at":     util.TimestampToString(item.CreatedAt),
		"actor_type":     util.TextToPtr(item.ActorType),
		"actor_id":       util.UUIDToPtr(item.ActorID),
		"details":        json.RawMessage(item.Details),
		"issue_status":   issueStatus,
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: workspaceID,
		ActorType:   "agent",
		ActorID:     agentID,
		Payload:     map[string]any{"item": resp},
	})
}

// agentToMap builds a simple map for broadcasting agent status updates.
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToString(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"visibility":           a.Visibility,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"is_internal":          a.IsInternal,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
