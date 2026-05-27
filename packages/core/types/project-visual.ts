import type { Attachment } from "./attachment";
import type { Plan } from "./plan";

export type ProjectVisualNodeType =
  | "character"
  | "scene"
  | "ui_element"
  | "prop"
  | "reference"
  | "gameplay_note"
  | "generated_variant";

export type ProjectVisualNodeStatus =
  | "draft"
  | "adopted"
  | "rejected"
  | "generating"
  | "failed";

export interface ProjectVisualNode {
  id: string;
  board_id: string;
  workspace_id: string;
  project_id: string;
  type: ProjectVisualNodeType;
  status: ProjectVisualNodeStatus;
  title: string;
  description: string;
  prompt: string;
  position_x: number;
  position_y: number;
  source_refs: unknown[];
  reference_attachment_ids: string[];
  result_attachment_id: string | null;
  result_attachment: Attachment | null;
  result_note: string;
  generation_agent_id: string | null;
  generation_task_id: string | null;
  generation_error: string;
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

export interface UpdateProjectVisualBoardRequest {
  viewport?: Record<string, unknown>;
  metadata?: Record<string, unknown>;
  nodes?: Array<Pick<ProjectVisualNode, "id" | "type" | "status" | "title" | "description" | "prompt" | "position_x" | "position_y" | "source_refs">>;
  edges?: Array<Pick<ProjectVisualEdge, "id" | "source_node_id" | "target_node_id" | "relation">>;
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
  title?: string;
}

export type CreateProjectVisualPlanResponse = Plan;
