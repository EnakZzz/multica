import type { Attachment } from "./attachment";
import type { Plan } from "./plan";

export type ProjectVisualNodeType =
  | "character"
  | "scene"
  | "ui_element"
  | "prop"
  | "reference"
  | "gameplay_note"
  | "generated_variant"
  | "animation";

export type ProjectVisualNodeStatus =
  | "draft"
  | "adopted"
  | "rejected"
  | "generating"
  | "failed";

export type ProjectVisualPlanMode =
  | "playable_prototype"
  | "production_asset_integration";

export interface ProjectVisualNode {
  id: string;
  board_id: string;
  workspace_id: string;
  project_id: string;
  type: ProjectVisualNodeType;
  status: ProjectVisualNodeStatus;
  title: string;
  title_zh: string;
  description: string;
  description_zh: string;
  prompt: string;
  prompt_zh: string;
  position_x: number;
  position_y: number;
  source_refs: unknown[];
  reference_attachment_ids: string[];
  result_attachment_id: string | null;
  result_attachment: Attachment | null;
  result_note: string;
  result_note_zh: string;
  generation_agent_id: string | null;
  generation_task_id: string | null;
  generation_error: string;
  generation_error_zh: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectVisualEdge {
  id: string;
  board_id: string;
  workspace_id: string;
  project_id: string;
  source_node_id: string;
  target_node_id: string;
  relation: string;
  created_at: string;
  updated_at: string;
}

export interface ProjectVisualBoard {
  id: string;
  workspace_id: string;
  project_id: string;
  viewport: Record<string, unknown>;
  metadata: Record<string, unknown>;
  nodes: ProjectVisualNode[];
  edges: ProjectVisualEdge[];
  created_at: string;
  updated_at: string;
}

export interface ProjectVisualNodeGeneration {
  id: string;
  task_id: string;
  task_status: string;
  issue_id: string;
  issue_identifier: string;
  issue_title: string;
  issue_status: string;
  attachment_id: string | null;
  attachment: Attachment | null;
  note: string;
  note_zh: string;
  error: string;
  error_zh: string;
  is_current: boolean;
  created_at: string;
  completed_at: string;
}

export interface ListProjectVisualNodeGenerationsResponse {
  generations: ProjectVisualNodeGeneration[];
}

export interface UpdateProjectVisualBoardRequest {
  viewport?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  nodes?: Array<Pick<ProjectVisualNode, "id" | "type" | "status" | "title" | "title_zh" | "description" | "description_zh" | "prompt" | "prompt_zh" | "position_x" | "position_y" | "source_refs">>;
  edges?: Array<Pick<ProjectVisualEdge, "id" | "source_node_id" | "target_node_id" | "relation">>;
}

export interface CreateProjectVisualNodeRequest {
  type: ProjectVisualNodeType;
  title: string;
  title_zh?: string;
  description?: string;
  description_zh?: string;
  prompt?: string;
  prompt_zh?: string;
  position_x?: number;
  position_y?: number;
  source_refs?: unknown[];
  source_node_id?: string;
  relation?: string;
}

export interface GenerateProjectVisualNodeRequest {
  agent_id: string;
}

export interface GenerateProjectVisualNodesResponse {
  task_id?: string;
  issue_id?: string;
  issue_identifier?: string;
}

export type GenerateProjectVisualNodeImageResponse = GenerateProjectVisualNodesResponse;

export interface CreateProjectVisualPlanRequest {
  gameplay_notes?: string;
  plan_mode?: ProjectVisualPlanMode;
  title?: string;
}

export type CreateProjectVisualPlanResponse = Plan;
