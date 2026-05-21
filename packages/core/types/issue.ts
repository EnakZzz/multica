import type { Label } from "./label";

export type IssueStatus =
  | "backlog"
  | "todo"
  | "in_progress"
  | "in_review"
  | "done"
  | "blocked"
  | "cancelled";

export type IssuePriority = "urgent" | "high" | "medium" | "low" | "none";

export type IssueAssigneeType = "member" | "agent";

export type IssueUnitTestStatus =
  | "not_required"
  | "pending"
  | "passed"
  | "failed"
  | "blocked";

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

export interface IssueReaction {
  id: string;
  issue_id: string;
  actor_type: string;
  actor_id: string;
  emoji: string;
  created_at: string;
}

export interface Issue {
  id: string;
  workspace_id: string;
  number: number;
  identifier: string;
  title: string;
  description: string | null;
  status: IssueStatus;
  priority: IssuePriority;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  creator_type: IssueAssigneeType;
  creator_id: string;
  parent_issue_id: string | null;
  project_id: string | null;
  position: number;
  start_date: string | null;
  due_date: string | null;
  unit_test_checklist?: UnitTestCheck[];
  unit_test_status?: IssueUnitTestStatus | string;
  unit_test_iteration_count?: number;
  unit_test_max_iterations?: number;
  reactions?: IssueReaction[];
  labels?: Label[];
  created_at: string;
  updated_at: string;
}

export interface IssueDependencySummary {
  id: string;
  type: "blocked_by" | "blocks" | "related" | string;
  issue_id: string;
  identifier: string;
  title: string;
  status: IssueStatus | string;
  assignee_type: IssueAssigneeType | null;
  assignee_id: string | null;
  dependency_type: "blocked_by" | "blocks" | "related" | string;
}

export interface IssueDependenciesResponse {
  blocked_by: IssueDependencySummary[];
  blocks: IssueDependencySummary[];
}
