package daemon

import "encoding/json"

// AgentEntry describes a single available agent CLI.
type AgentEntry struct {
	Path  string // path to CLI binary
	Model string // model override (optional)
}

// Runtime represents a registered daemon runtime.
type Runtime struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Status   string `json:"status"`
}

// RepoData holds repository information from the workspace.
type RepoData struct {
	URL string `json:"url"`
}

// ProjectResourceData mirrors handler.ProjectResourceData — a single project
// resource as delivered to the daemon. resource_ref is type-specific JSON.
type ProjectResourceData struct {
	ID           string          `json:"id"`
	ResourceType string          `json:"resource_type"`
	ResourceRef  json.RawMessage `json:"resource_ref"`
	Label        string          `json:"label,omitempty"`
}

// Task represents a claimed task from the server.
// Agent data (name, skills) is populated by the claim endpoint.
type Task struct {
	ID                        string                  `json:"id"`
	AgentID                   string                  `json:"agent_id"`
	RuntimeID                 string                  `json:"runtime_id"`
	IssueID                   string                  `json:"issue_id"`
	IssueIdentifier           string                  `json:"issue_identifier,omitempty"`
	PlanItemID                string                  `json:"plan_item_id,omitempty"`
	PlanItemNodeType          string                  `json:"plan_item_node_type,omitempty"`
	PlanItemExecutionKind     string                  `json:"plan_item_execution_kind,omitempty"`
	PlanItemRequiresGitCommit bool                    `json:"plan_item_requires_git_commit,omitempty"`
	PlanItemBranchName        string                  `json:"plan_item_branch_name,omitempty"`
	UnitTestChecklist         []UnitTestCheckData     `json:"unit_test_checklist,omitempty"`
	RepoCheckoutRef           string                  `json:"repo_checkout_ref,omitempty"`
	PublishBranchName         string                  `json:"publish_branch_name,omitempty"`
	ReviewTargetIssueID       string                  `json:"review_target_issue_id,omitempty"`
	ReviewTargetIdentifier    string                  `json:"review_target_identifier,omitempty"`
	ReviewTargetBranchName    string                  `json:"review_target_branch_name,omitempty"`
	ReviewTargetCommitSHA     string                  `json:"review_target_commit_sha,omitempty"`
	WorkspaceID               string                  `json:"workspace_id"`
	Agent                     *AgentData              `json:"agent,omitempty"`
	Repos                     []RepoData              `json:"repos,omitempty"`
	ProjectID                 string                  `json:"project_id,omitempty"`                // issue's project, when present
	ProjectTitle              string                  `json:"project_title,omitempty"`             // human-readable project title for context injection
	ProjectResources          []ProjectResourceData   `json:"project_resources,omitempty"`         // project-scoped resources to expose to the agent
	RelevantKnowledge         []RelevantKnowledgeData `json:"relevant_knowledge,omitempty"`        // bounded project knowledge snippets retrieved for this task
	PriorSessionID            string                  `json:"prior_session_id,omitempty"`          // Claude session ID from a previous task on this issue
	PriorWorkDir              string                  `json:"prior_work_dir,omitempty"`            // work_dir from a previous task on this issue
	TriggerCommentID          string                  `json:"trigger_comment_id,omitempty"`        // comment that triggered this task
	TriggerCommentContent     string                  `json:"trigger_comment_content,omitempty"`   // content of the triggering comment
	TriggerAuthorType         string                  `json:"trigger_author_type,omitempty"`       // "agent" or "member" — author kind for the triggering comment
	TriggerAuthorName         string                  `json:"trigger_author_name,omitempty"`       // display name of the triggering comment author
	ChatSessionID             string                  `json:"chat_session_id,omitempty"`           // non-empty for chat tasks
	ChatMessage               string                  `json:"chat_message,omitempty"`              // user message content for chat tasks
	ChatMessageAttachments    []ChatAttachmentMeta    `json:"chat_message_attachments,omitempty"`  // attachments linked to the chat message; agent uses these to `multica attachment download <id>`
	AutopilotRunID            string                  `json:"autopilot_run_id,omitempty"`          // non-empty for autopilot run_only tasks
	AutopilotID               string                  `json:"autopilot_id,omitempty"`              // autopilot that spawned this run
	AutopilotTitle            string                  `json:"autopilot_title,omitempty"`           // autopilot title used as task context
	AutopilotDescription      string                  `json:"autopilot_description,omitempty"`     // autopilot description used as task prompt
	AutopilotSource           string                  `json:"autopilot_source,omitempty"`          // manual, schedule, webhook, or api
	AutopilotTriggerPayload   json.RawMessage         `json:"autopilot_trigger_payload,omitempty"` // optional trigger payload for webhook/api runs
	QuickCreatePrompt         string                  `json:"quick_create_prompt,omitempty"`       // user's natural-language input for quick-create tasks
	// RequestingUserName + RequestingUserProfileDescription describe the human
	// the agent is working on behalf of. v1 sources them from the runtime
	// owner (the user who registered the daemon). Empty when the runtime has
	// no owner (cloud / system runtimes) or the user hasn't set a description.
	// Injected into the brief under `## Requesting User`; omitted entirely
	// when description is empty so the agent doesn't see a useless heading.
	RequestingUserName               string             `json:"requesting_user_name,omitempty"`
	RequestingUserProfileDescription string             `json:"requesting_user_profile_description,omitempty"`
	IssuePlanPrompt                  string             `json:"issue_plan_prompt,omitempty"`   // user's natural-language input for plan tasks
	IssuePlanID                      string             `json:"issue_plan_id,omitempty"`       // plan row receiving the structured output
	IssuePlanPhase                   string             `json:"issue_plan_phase,omitempty"`    // spec or items for two-stage planning
	IssuePlanSpec                    PlanSpecData       `json:"issue_plan_spec,omitempty"`     // approved spec for item-generation tasks
	AvailableAgents                  []PlanAgentData    `json:"available_agents,omitempty"`    // assignable agents planner may recommend
	AvailablePipelines               []PlanPipelineData `json:"available_pipelines,omitempty"` // runnable pipelines planner may select
}

type RelevantKnowledgeData struct {
	TargetType string  `json:"target_type"`
	ID         string  `json:"id"`
	Slug       string  `json:"slug,omitempty"`
	Kind       string  `json:"kind"`
	Outcome    string  `json:"outcome"`
	Title      string  `json:"title"`
	Summary    string  `json:"summary"`
	IssueID    string  `json:"issue_id,omitempty"`
	TaskID     string  `json:"task_id,omitempty"`
	CommentID  string  `json:"comment_id,omitempty"`
	Confidence int32   `json:"confidence"`
	Score      float64 `json:"score"`
}

type UnitTestCheckData struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Command        string `json:"command"`
	Expected       string `json:"expected"`
	Required       bool   `json:"required"`
	Status         string `json:"status"`
	LastRunAt      string `json:"last_run_at,omitempty"`
	OutputExcerpt  string `json:"output_excerpt,omitempty"`
	FailureSummary string `json:"failure_summary,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
}

type PlanSpecData struct {
	Summary              string                       `json:"summary"`
	Goal                 string                       `json:"goal"`
	SuccessCriteria      []string                     `json:"success_criteria"`
	AcceptanceScenarios  []PlanAcceptanceScenarioData `json:"acceptance_scenarios"`
	InScope              []string                     `json:"in_scope"`
	OutOfScope           []string                     `json:"out_of_scope"`
	Approach             string                       `json:"approach"`
	DesignDecisions      []string                     `json:"design_decisions"`
	VerificationCommands []string                     `json:"verification_commands"`
	Assumptions          []string                     `json:"assumptions"`
	OpenQuestions        []string                     `json:"open_questions"`
	Clarifications       []PlanClarificationData      `json:"clarifications,omitempty"`
}

type PlanAcceptanceScenarioData struct {
	Name  string `json:"name"`
	Given string `json:"given"`
	When  string `json:"when"`
	Then  string `json:"then"`
}

type PlanClarificationData struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type PlanAgentData struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions,omitempty"`
	Skills       []string `json:"skills,omitempty"`
}

type PlanPipelineData struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	IsSystem    bool                   `json:"is_system,omitempty"`
	SystemKey   string                 `json:"system_key,omitempty"`
	ReadOnly    bool                   `json:"read_only,omitempty"`
	Nodes       []PlanPipelineNodeData `json:"nodes,omitempty"`
}

type PlanPipelineNodeData struct {
	Key               string   `json:"key"`
	Type              string   `json:"type"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	AgentID           string   `json:"agent_id,omitempty"`
	Repos             []string `json:"repos,omitempty"`
	DependsOnNodeKeys []string `json:"depends_on_node_keys,omitempty"`
}

// ChatAttachmentMeta is the structured attachment metadata the daemon
// hands to the agent for chat tasks. We pass id + filename + content_type
// so the chat prompt can list them explicitly and instruct the agent to
// run `multica attachment download <id>` instead of guessing from a
// signed CDN URL (which expires).
type ChatAttachmentMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
}

// AgentData holds agent details returned by the claim endpoint.
type AgentData struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Instructions  string            `json:"instructions"`
	Skills        []SkillData       `json:"skills"`
	CustomEnv     map[string]string `json:"custom_env,omitempty"`
	CustomArgs    []string          `json:"custom_args,omitempty"`
	McpConfig     json.RawMessage   `json:"mcp_config,omitempty"`
	Model         string            `json:"model,omitempty"`
	ThinkingLevel string            `json:"thinking_level,omitempty"`
}

// SkillData represents a structured skill for task execution.
type SkillData struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Content     string          `json:"content"`
	Files       []SkillFileData `json:"files,omitempty"`
}

// SkillFileData represents a supporting file within a skill.
type SkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// TaskUsageEntry represents token usage for a single model during a task execution.
type TaskUsageEntry struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

// TaskResult is the outcome of executing a task.
type TaskResult struct {
	Status          string           `json:"status"`
	Comment         string           `json:"comment"`
	BranchName      string           `json:"branch_name,omitempty"`
	BranchCommitSHA string           `json:"branch_commit_sha,omitempty"`
	BranchPushedAt  string           `json:"branch_pushed_at,omitempty"`
	EnvType         string           `json:"env_type,omitempty"`
	SessionID       string           `json:"session_id,omitempty"` // Claude session ID for future resumption
	WorkDir         string           `json:"work_dir,omitempty"`   // working directory used during execution
	EnvRoot         string           `json:"-"`                    // env root dir for writing GC metadata (not sent to server)
	FailureReason   string           `json:"-"`                    // classifier forwarded to FailTask on the blocked path; empty falls back to 'agent_error'
	Usage           []TaskUsageEntry `json:"usage,omitempty"`      // per-model token usage
}
