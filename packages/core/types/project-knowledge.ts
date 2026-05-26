export type ProjectWikiPageStatus = "draft" | "reviewed" | "archived";

export interface ProjectWikiPage {
  id: string;
  workspace_id: string;
  project_id: string;
  slug: string;
  title: string;
  body: string;
  source_refs: unknown[];
  status: ProjectWikiPageStatus;
  updated_by: string | null;
  reviewed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ProjectMemoryItem {
  id: string;
  workspace_id: string;
  project_id: string;
  issue_id: string | null;
  task_id: string | null;
  comment_id: string | null;
  kind: string;
  outcome: string;
  title: string;
  summary: string;
  symptom: string;
  cause: string;
  fix_path: string;
  commands: unknown[];
  repo_refs: unknown[];
  tags: string[];
  confidence: number;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ProjectKnowledgeSearchResult {
  target_type: "wiki_page" | "memory_item" | string;
  score: number;
  match_type?: "hybrid" | "vector" | "keyword" | string;
  vector_score?: number | null;
  keyword_score?: number | null;
  wiki_page?: ProjectWikiPage;
  memory_item?: ProjectMemoryItem;
  snippet: string;
  source_refs?: unknown[];
}

export interface ListProjectWikiPagesResponse {
  wiki_pages: ProjectWikiPage[];
  total: number;
}

export interface ListProjectMemoryItemsResponse {
  memory_items: ProjectMemoryItem[];
  total: number;
}

export interface ProjectKnowledgeSearchResponse {
  configured: boolean;
  results: ProjectKnowledgeSearchResult[];
  total: number;
  error?: string;
}

export interface ProjectKnowledgeRetrievalLog {
  id: string;
  workspace_id: string;
  project_id: string;
  issue_id: string | null;
  task_id: string | null;
  query_text: string;
  returned_items: unknown[];
  search_mode: string;
  query_context: Record<string, unknown>;
  candidates: ProjectKnowledgeSearchResult[];
  selected_items: ProjectRelevantKnowledge[];
  injected_text: string;
  token_budget: number | null;
  injected_item_count: number;
  prompt_section_hash: string | null;
  status: string;
  error: string | null;
  task_outcome: string | null;
  helpfulness: number | null;
  feedback: "useful" | "noisy" | "wrong" | "stale" | string | null;
  feedback_note: string | null;
  created_at: string;
  updated_at: string;
}

export interface ProjectRelevantKnowledge {
  target_type: string;
  id: string;
  slug?: string;
  kind: string;
  outcome: string;
  title: string;
  summary: string;
  issue_id?: string;
  task_id?: string;
  comment_id?: string;
  confidence: number;
  score: number;
}

export interface ProjectKnowledgeRetrievalLogsResponse {
  retrieval_logs: ProjectKnowledgeRetrievalLog[];
  total: number;
}

export interface UpdateProjectKnowledgeRetrievalLogFeedbackRequest {
  feedback?: "useful" | "noisy" | "wrong" | "stale" | "";
  feedback_note?: string;
  helpfulness?: number | null;
}

export interface RelatedMemoryResponse {
  configured: boolean;
  related_memory: ProjectKnowledgeSearchResult[];
  total: number;
  error?: string;
}

export interface CreateProjectWikiPageRequest {
  slug: string;
  title: string;
  body?: string;
  source_refs?: unknown[];
  status?: ProjectWikiPageStatus;
  reviewed_at?: string | null;
}

export interface UpdateProjectWikiPageRequest {
  title?: string;
  body?: string;
  source_refs?: unknown[];
  status?: ProjectWikiPageStatus;
  reviewed_at?: string | null;
}

export interface CreateProjectMemoryItemRequest {
  issue_id?: string | null;
  task_id?: string | null;
  comment_id?: string | null;
  kind: string;
  outcome?: string;
  title: string;
  summary?: string;
  symptom?: string;
  cause?: string;
  fix_path?: string;
  commands?: unknown[];
  repo_refs?: unknown[];
  tags?: string[];
  confidence?: number;
  expires_at?: string | null;
}

export interface UpdateProjectMemoryItemRequest {
  kind?: string;
  outcome?: string;
  title?: string;
  summary?: string;
  symptom?: string;
  cause?: string;
  fix_path?: string;
  commands?: unknown[];
  repo_refs?: unknown[];
  tags?: string[];
  confidence?: number;
  expires_at?: string | null;
}
