export type PipelineNodeType = "issue" | "manual" | "check" | "spec_review" | "code_review";

export interface PipelineNode {
  id: string;
  pipeline_id: string;
  key: string;
  type: PipelineNodeType;
  title: string;
  description: string;
  agent_id: string | null;
  repo: string | null;
  repos: string[];
  depends_on_node_keys: string[];
  position: number;
  position_x: number;
  position_y: number;
  created_at: string;
  updated_at: string;
}

export interface Pipeline {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  is_system: boolean;
  system_key: string | null;
  editable: boolean;
  deletable: boolean;
  created_by: string;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
  nodes: PipelineNode[];
}

export interface PipelineRunNode {
  id: string;
  pipeline_run_id: string;
  pipeline_node_id: string | null;
  node_key: string;
  issue_id: string;
  created_at: string;
}

export interface PipelineRun {
  id: string;
  pipeline_id: string;
  workspace_id: string;
  project_id: string | null;
  parent_issue_id: string;
  status: "completed" | "failed";
  created_by: string;
  created_at: string;
  nodes: PipelineRunNode[];
}

export interface ListPipelinesResponse {
  pipelines: Pipeline[];
  total: number;
}

export interface UpsertPipelineNodeRequest {
  key: string;
  type?: PipelineNodeType;
  title: string;
  description?: string;
  agent_id?: string | null;
  repo?: string | null;
  repos?: string[];
  depends_on_node_keys?: string[];
  position_x?: number;
  position_y?: number;
}

export interface CreatePipelineRequest {
  name: string;
  description?: string;
  nodes: UpsertPipelineNodeRequest[];
}

export interface UpdatePipelineRequest {
  name?: string;
  description?: string;
  nodes: UpsertPipelineNodeRequest[];
}

export interface RunPipelineRequest {
  title?: string;
  project_id?: string | null;
}

export interface DuplicatePipelineRequest {
  name?: string;
}

export interface ImportPipelineYamlRequest {
  content: string;
  pipeline_id?: string | null;
}

export interface PipelineImportPreview {
  name: string;
  description: string;
  nodes: UpsertPipelineNodeRequest[];
}

export interface PipelineImportValidationResponse {
  valid: boolean;
  errors: string[];
  pipeline: PipelineImportPreview | null;
}
