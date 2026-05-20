export type PlanStatus = "planning" | "ready" | "failed" | "committed";

export interface PlanItem {
  id: string;
  plan_id: string;
  position: number;
  title: string;
  description: string;
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
}

export interface UpdatePlanItemRequest {
  title: string;
  description: string;
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
  items: UpdatePlanItemRequest[];
}
