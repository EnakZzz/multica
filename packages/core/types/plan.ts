export type PlanStatus = "planning" | "spec_review" | "ready" | "failed" | "committed";
export type PlanItemExecutionKind = "agent_task" | "human_confirmation";
export type PlanItemNodeType =
  | "issue"
  | "manual"
  | "check"
  | "spec_review"
  | "code_review"
  | "merge"
  | "subagent-driven-development";
export type HarnessStrategyMode =
  | "none"
  | "classify_and_act"
  | "fan_out_synthesize"
  | "adversarial_verification"
  | "generate_and_filter"
  | "tournament"
  | "loop_until_done";

export interface HarnessStrategy {
  mode: HarnessStrategyMode;
  summary: string;
  rationale: string;
  stop_condition: string;
  parallelism: number;
  requires_isolated_worktree: boolean;
}

export type ExecutionBranchPolicy = "auto" | "shared" | "per_item" | "per_iteration" | "per_agent";
export type ExecutionMergePolicy = "none" | "manual" | "pr_required" | "auto_when_green";

export interface ExecutionRouting {
  requires_isolated_worktree: boolean;
  branch_policy: ExecutionBranchPolicy;
  merge_policy: ExecutionMergePolicy;
}

export interface UnitTestCheck {
  id: string;
  title: string;
  command: string;
  expected: string;
  required: boolean;
  status: "pending" | "passed" | "failed" | "skipped" | string;
  last_run_at: string | null;
  output_excerpt: string;
  failure_summary: string;
  task_id: string;
}

export interface PlanClarification {
  question: string;
  answer: string;
}

export interface PlanAcceptanceScenario {
  name: string;
  given: string;
  when: string;
  then: string;
}

export interface PlanSpec {
  summary: string;
  goal: string;
  success_criteria: string[];
  acceptance_scenarios: PlanAcceptanceScenario[];
  in_scope: string[];
  out_of_scope: string[];
  approach: string;
  design_decisions: string[];
  verification_commands: string[];
  assumptions: string[];
  open_questions: string[];
  clarifications: PlanClarification[];
}

export interface PlanItem {
  id: string;
  plan_id: string;
  position: number;
  title: string;
  description: string;
  acceptance_criteria: string[];
  suggested_test_commands: string[];
  unit_test_checklist: UnitTestCheck[];
  context_resources: string[];
  risk_notes: string[];
  node_type: PlanItemNodeType;
  execution_kind: PlanItemExecutionKind;
  confirmation_question: string;
  confirmation_reason: string;
  required_evidence: string[];
  requires_git_commit: boolean;
  branch_name: string;
  iteration_index: number;
  iteration_title: string;
  iteration_branch_name: string;
  execution_routing: ExecutionRouting;
  recommended_agent_id: string | null;
  match_score: number;
  match_reason: string;
  missing_capability: string;
  depends_on_positions: number[];
  selected: boolean;
  generated_issue_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface Plan {
  id: string;
  workspace_id: string;
  title: string;
  prompt: string;
  status: PlanStatus;
  planner_agent_id: string;
  task_id: string;
  project_id: string | null;
  parent_title: string;
  parent_description: string;
  parent_issue_id: string | null;
  harness_strategy: HarnessStrategy;
  spec: PlanSpec;
  committed_spec: PlanSpec | null;
  spec_approved_at: string | null;
  spec_approved_by: string | null;
  error: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  items: PlanItem[];
}

export interface ListPlansResponse {
  plans: Plan[];
}

export interface CreatePlanRequest {
  title?: string;
  prompt: string;
  planner_agent_id: string;
  project_id?: string | null;
  source_issue_id?: string;
}

export interface UpdatePlanItemRequest {
  title: string;
  description: string;
  acceptance_criteria?: string[];
  suggested_test_commands?: string[];
  unit_test_checklist?: UnitTestCheck[];
  context_resources?: string[];
  risk_notes?: string[];
  node_type?: PlanItemNodeType;
  execution_kind?: PlanItemExecutionKind;
  confirmation_question?: string;
  confirmation_reason?: string;
  required_evidence?: string[];
  requires_git_commit?: boolean;
  branch_name?: string;
  iteration_index?: number;
  iteration_title?: string;
  iteration_branch_name?: string;
  execution_routing?: ExecutionRouting;
  recommended_agent_id?: string | null;
  match_score: number;
  match_reason: string;
  missing_capability: string;
  depends_on_positions?: number[];
  selected: boolean;
}

export interface UpdatePlanRequest {
  title?: string;
  parent_title: string;
  parent_description: string;
  spec?: PlanSpec;
  items: UpdatePlanItemRequest[];
}

export interface ApprovePlanSpecRequest {
  spec?: PlanSpec;
}

export interface ClarifyPlanSpecRequest {
  spec?: PlanSpec;
  answers: PlanClarification[];
}

export interface CommitPlanRequest {
  acknowledged_human_confirmation_item_ids?: string[];
}
