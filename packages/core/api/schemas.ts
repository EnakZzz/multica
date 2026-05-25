import { z } from "zod";
import type {
  Agent,
  AgentTemplate,
  AgentTemplateSummary,
  Attachment,
  CreateAgentFromTemplateResponse,
  GroupedIssuesResponse,
  Issue,
  IssueDependenciesResponse,
  ListIssuesResponse,
  ListPipelinesResponse,
  ListWebhookDeliveriesResponse,
  ListPlansResponse,
  Pipeline,
  PipelineImportValidationResponse,
  PipelineRun,
  Plan,
  PlanSpec,
  ProjectKnowledgeRetrievalLog,
  ProjectKnowledgeRetrievalLogsResponse,
  ProjectKnowledgeSearchResponse,
  ProjectMemoryItem,
  ProjectRelevantKnowledge,
  ProjectWikiPage,
  RelatedMemoryResponse,
  TimelineEntry,
  User,
  WebhookDelivery,
} from "../types";

// ---------------------------------------------------------------------------
// Schemas for the highest-risk API endpoints — those whose responses drive
// the issue detail page (timeline, comments, subscribers) and the issues
// list. These are the surfaces that white-screened in #2143 / #2147 / #2192.
//
// These schemas are intentionally LENIENT:
//   - String enums are stored as `z.string()` rather than `z.enum([...])`.
//     A new server-side enum value should render as a generic fallback in
//     the UI, never crash a `safeParse`.
//   - Optional fields are unioned with `null` and given fallbacks where
//     existing UI code already coerces them.
//   - Arrays default to `[]` so a missing `reactions` / `attachments` /
//     `entries` field doesn't take the page down.
//   - Every object schema ends with `.loose()` so unknown server-side
//     fields pass through unchanged. zod 4's `.object()` defaults to STRIP,
//     which would silently delete fields the schema didn't explicitly list
//     — fine while the TS type doesn't claim them, but the moment a future
//     PR adds a TS field without updating the schema, the cast `as T` lies
//     and the field shows up as `undefined` at runtime. `.loose()` removes
//     that synchronisation hazard.
//
// These schemas are deliberately not typed as `z.ZodType<TimelineEntry>` /
// `z.ZodType<Issue>` etc. — the strict TS types narrow string fields to
// literal unions, which would defeat the leniency above. `parseWithFallback`
// returns the parsed value cast to the caller-supplied `T`, so the strict
// type still flows out at the call site; the schema only guards shape.
// ---------------------------------------------------------------------------

const ReactionSchema = z.object({
  id: z.string(),
  comment_id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  emoji: z.string(),
  created_at: z.string(),
});

// Nested attachments embedded in timeline/comment responses stay lenient on
// purpose: a single malformed attachment must not knock the whole timeline
// into the fallback `[]`.
const AttachmentSchema = z.object({
  id: z.string(),
}).loose();

// Standalone attachment lookup (`GET /api/attachments/{id}`) is the source of
// truth for click-time download URLs. The two fields the download flow opens
// in a new tab — `download_url` and `url` — must be strings, otherwise we'd
// happily `window.open(undefined)`. `filename` gates the toast/title and is
// also enforced so a missing value falls back to the empty record below.
export const AttachmentResponseSchema = z.object({
  id: z.string(),
  url: z.string(),
  download_url: z.string(),
  filename: z.string(),
  chat_session_id: z.string().nullable().optional(),
  chat_message_id: z.string().nullable().optional(),
}).loose();

export const EMPTY_ATTACHMENT: Attachment = {
  id: "",
  workspace_id: "",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "",
  uploader_id: "",
  filename: "",
  url: "",
  download_url: "",
  content_type: "",
  size_bytes: 0,
  created_at: "",
};

// All object schemas use `.loose()` so unknown server-side fields pass
// through unchanged. zod 4's `.object()` defaults to STRIP, which would
// silently drop new fields and surface as a "field neither showed up in
// the UI" mystery the next time the TS type adopted them but the schema
// wasn't updated in lock-step. `.loose()` removes that synchronisation
// hazard — the schema validates the shape it knows about and leaves the
// rest alone.
const TimelineEntrySchema = z.object({
  type: z.string(),
  id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  created_at: z.string(),
  action: z.string().optional(),
  details: z.record(z.string(), z.unknown()).optional(),
  content: z.string().optional(),
  parent_id: z.string().nullable().optional(),
  updated_at: z.string().optional(),
  comment_type: z.string().optional(),
  reactions: z.array(ReactionSchema).optional(),
  attachments: z.array(AttachmentSchema).optional(),
  coalesced_count: z.number().optional(),
}).loose();

// /timeline returns a flat array of TimelineEntry, oldest first. The
// previously cursor-paginated wrapper was removed (#1929) — at observed data
// sizes (p99 ~30 entries per issue) paged delivery only created bugs.
export const TimelineEntriesSchema = z.array(TimelineEntrySchema);

export const EMPTY_TIMELINE_ENTRIES: TimelineEntry[] = [];

export const CommentSchema = z.object({
  id: z.string(),
  issue_id: z.string(),
  author_type: z.string(),
  author_id: z.string(),
  content: z.string(),
  type: z.string(),
  parent_id: z.string().nullable(),
  reactions: z.array(ReactionSchema).default([]),
  attachments: z.array(AttachmentSchema).default([]),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CommentsListSchema = z.array(CommentSchema);

const UnitTestCheckSchema = z.object({
  id: z.string().catch("").default(""),
  title: z.string().catch("").default(""),
  command: z.string().catch("").default(""),
  expected: z.string().catch("").default(""),
  required: z.boolean().catch(true).default(true),
  status: z.string().catch("pending").default("pending"),
  last_run_at: z.string().nullable().catch(null).default(null),
  output_excerpt: z.string().catch("").default(""),
  failure_summary: z.string().catch("").default(""),
  task_id: z.string().catch("").default(""),
}).loose();

export const IssueSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  number: z.number(),
  identifier: z.string(),
  title: z.string(),
  description: z.string().nullable(),
  status: z.string(),
  priority: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  creator_type: z.string(),
  creator_id: z.string(),
  parent_issue_id: z.string().nullable(),
  project_id: z.string().nullable(),
  position: z.number(),
    start_date: z.string().nullable(),
    due_date: z.string().nullable(),
    unit_test_checklist: z.array(UnitTestCheckSchema).catch([]).default([]),
    unit_test_status: z.string().catch("not_required").default("not_required"),
    unit_test_iteration_count: z.number().catch(0).default(0),
    unit_test_max_iterations: z.number().catch(2).default(2),
    reactions: z.array(z.unknown()).optional(),
  labels: z.array(z.unknown()).optional(),
  created_at: z.string(),
  updated_at: z.string(),
  }).loose();

export const EMPTY_ISSUE: Issue = {
  id: "",
  workspace_id: "",
  number: 0,
  identifier: "",
  title: "",
  description: null,
  status: "backlog",
  priority: "none",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  start_date: null,
  due_date: null,
  unit_test_checklist: [],
  unit_test_status: "not_required",
  unit_test_iteration_count: 0,
  unit_test_max_iterations: 2,
  created_at: "",
  updated_at: "",
};

export const ListIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_LIST_ISSUES_RESPONSE: ListIssuesResponse = {
  issues: [],
  total: 0,
};

const UnknownArraySchema = z.array(z.unknown()).catch([]).default([]);

export const ProjectWikiPageSchema = z.object({
  id: z.string().catch("").default(""),
  workspace_id: z.string().catch("").default(""),
  project_id: z.string().catch("").default(""),
  slug: z.string().catch("").default(""),
  title: z.string().catch("").default(""),
  body: z.string().catch("").default(""),
  source_refs: UnknownArraySchema,
  status: z.string().catch("draft").default("draft"),
  updated_by: z.string().nullable().catch(null).default(null),
  reviewed_at: z.string().nullable().catch(null).default(null),
  created_at: z.string().catch("").default(""),
  updated_at: z.string().catch("").default(""),
}).loose();

export const EMPTY_PROJECT_WIKI_PAGE: ProjectWikiPage = {
  id: "",
  workspace_id: "",
  project_id: "",
  slug: "",
  title: "",
  body: "",
  source_refs: [],
  status: "draft",
  updated_by: null,
  reviewed_at: null,
  created_at: "",
  updated_at: "",
};

export const ListProjectWikiPagesResponseSchema = z.object({
  wiki_pages: z.array(ProjectWikiPageSchema).catch([]).default([]),
  total: z.number().catch(0).default(0),
}).loose();

export const EMPTY_LIST_PROJECT_WIKI_PAGES_RESPONSE = {
  wiki_pages: [],
  total: 0,
};

export const ProjectMemoryItemSchema = z.object({
  id: z.string().catch("").default(""),
  workspace_id: z.string().catch("").default(""),
  project_id: z.string().catch("").default(""),
  issue_id: z.string().nullable().catch(null).default(null),
  task_id: z.string().nullable().catch(null).default(null),
  comment_id: z.string().nullable().catch(null).default(null),
  kind: z.string().catch("").default(""),
  outcome: z.string().catch("").default(""),
  title: z.string().catch("").default(""),
  summary: z.string().catch("").default(""),
  symptom: z.string().catch("").default(""),
  cause: z.string().catch("").default(""),
  fix_path: z.string().catch("").default(""),
  commands: UnknownArraySchema,
  repo_refs: UnknownArraySchema,
  tags: z.array(z.string()).catch([]).default([]),
  confidence: z.number().catch(0).default(0),
  expires_at: z.string().nullable().catch(null).default(null),
  created_at: z.string().catch("").default(""),
  updated_at: z.string().catch("").default(""),
}).loose();

export const EMPTY_PROJECT_MEMORY_ITEM: ProjectMemoryItem = {
  id: "",
  workspace_id: "",
  project_id: "",
  issue_id: null,
  task_id: null,
  comment_id: null,
  kind: "",
  outcome: "",
  title: "",
  summary: "",
  symptom: "",
  cause: "",
  fix_path: "",
  commands: [],
  repo_refs: [],
  tags: [],
  confidence: 0,
  expires_at: null,
  created_at: "",
  updated_at: "",
};

export const ListProjectMemoryItemsResponseSchema = z.object({
  memory_items: z.array(ProjectMemoryItemSchema).catch([]).default([]),
  total: z.number().catch(0).default(0),
}).loose();

export const EMPTY_LIST_PROJECT_MEMORY_ITEMS_RESPONSE = {
  memory_items: [],
  total: 0,
};

export const ProjectKnowledgeSearchResultSchema = z.object({
  target_type: z.string().catch("").default(""),
  score: z.number().catch(0).default(0),
  match_type: z.string().optional(),
  vector_score: z.number().nullable().optional(),
  keyword_score: z.number().nullable().optional(),
  wiki_page: ProjectWikiPageSchema.optional(),
  memory_item: ProjectMemoryItemSchema.optional(),
  snippet: z.string().catch("").default(""),
  source_refs: UnknownArraySchema.optional(),
}).loose();

export const ProjectRelevantKnowledgeSchema = z.object({
  target_type: z.string().catch("").default(""),
  id: z.string().catch("").default(""),
  kind: z.string().catch("").default(""),
  outcome: z.string().catch("").default(""),
  title: z.string().catch("").default(""),
  summary: z.string().catch("").default(""),
  issue_id: z.string().optional(),
  task_id: z.string().optional(),
  comment_id: z.string().optional(),
  confidence: z.number().catch(0).default(0),
  score: z.number().catch(0).default(0),
}).loose();

export const EMPTY_PROJECT_RELEVANT_KNOWLEDGE: ProjectRelevantKnowledge = {
  target_type: "",
  id: "",
  kind: "",
  outcome: "",
  title: "",
  summary: "",
  confidence: 0,
  score: 0,
};

const UnknownRecordSchema = z.record(z.string(), z.unknown()).catch({}).default({});

export const ProjectKnowledgeRetrievalLogSchema = z.object({
  id: z.string().catch("").default(""),
  workspace_id: z.string().catch("").default(""),
  project_id: z.string().catch("").default(""),
  issue_id: z.string().nullable().catch(null).default(null),
  task_id: z.string().nullable().catch(null).default(null),
  query_text: z.string().catch("").default(""),
  returned_items: UnknownArraySchema,
  search_mode: z.string().catch("hybrid").default("hybrid"),
  query_context: UnknownRecordSchema,
  candidates: z.array(ProjectKnowledgeSearchResultSchema).catch([]).default([]),
  selected_items: z.array(ProjectRelevantKnowledgeSchema).catch([]).default([]),
  injected_text: z.string().catch("").default(""),
  token_budget: z.number().nullable().catch(null).default(null),
  injected_item_count: z.number().catch(0).default(0),
  prompt_section_hash: z.string().nullable().catch(null).default(null),
  status: z.string().catch("").default(""),
  error: z.string().nullable().catch(null).default(null),
  task_outcome: z.string().nullable().catch(null).default(null),
  helpfulness: z.number().nullable().catch(null).default(null),
  feedback: z.string().nullable().catch(null).default(null),
  feedback_note: z.string().nullable().catch(null).default(null),
  created_at: z.string().catch("").default(""),
  updated_at: z.string().catch("").default(""),
}).loose();

export const EMPTY_PROJECT_KNOWLEDGE_RETRIEVAL_LOG: ProjectKnowledgeRetrievalLog = {
  id: "",
  workspace_id: "",
  project_id: "",
  issue_id: null,
  task_id: null,
  query_text: "",
  returned_items: [],
  search_mode: "hybrid",
  query_context: {},
  candidates: [],
  selected_items: [],
  injected_text: "",
  token_budget: null,
  injected_item_count: 0,
  prompt_section_hash: null,
  status: "",
  error: null,
  task_outcome: null,
  helpfulness: null,
  feedback: null,
  feedback_note: null,
  created_at: "",
  updated_at: "",
};

export const ProjectKnowledgeRetrievalLogsResponseSchema = z.object({
  retrieval_logs: z.array(ProjectKnowledgeRetrievalLogSchema).catch([]).default([]),
  total: z.number().catch(0).default(0),
}).loose();

export const EMPTY_PROJECT_KNOWLEDGE_RETRIEVAL_LOGS_RESPONSE: ProjectKnowledgeRetrievalLogsResponse = {
  retrieval_logs: [],
  total: 0,
};

export const ProjectKnowledgeSearchResponseSchema = z.object({
  configured: z.boolean().catch(false).default(false),
  results: z.array(ProjectKnowledgeSearchResultSchema).catch([]).default([]),
  total: z.number().catch(0).default(0),
  error: z.string().optional(),
}).loose();

export const EMPTY_PROJECT_KNOWLEDGE_SEARCH_RESPONSE: ProjectKnowledgeSearchResponse = {
  configured: false,
  results: [],
  total: 0,
};

export const RelatedMemoryResponseSchema = z.object({
  configured: z.boolean().catch(false).default(false),
  related_memory: z.array(ProjectKnowledgeSearchResultSchema).catch([]).default([]),
  total: z.number().catch(0).default(0),
  error: z.string().optional(),
}).loose();

export const EMPTY_RELATED_MEMORY_RESPONSE: RelatedMemoryResponse = {
  configured: false,
  related_memory: [],
  total: 0,
};

const IssueAssigneeGroupSchema = z.object({
  id: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const GroupedIssuesResponseSchema = z.object({
  groups: z.array(IssueAssigneeGroupSchema).default([]),
}).loose();

export const EMPTY_GROUPED_ISSUES_RESPONSE: GroupedIssuesResponse = {
  groups: [],
};

export const QuickCreateIssueResponseSchema = z.object({
  task_id: z.string().catch("").default(""),
  plan_id: z.string().catch("").default(""),
}).loose();

export const EMPTY_QUICK_CREATE_ISSUE_RESPONSE = {
  task_id: "",
  plan_id: "",
};

const IssueDependencySummarySchema = z.object({
  id: z.string(),
  type: z.string().default("blocked_by"),
  issue_id: z.string(),
  identifier: z.string(),
  title: z.string(),
  status: z.string(),
  assignee_type: z.string().nullable().default(null),
  assignee_id: z.string().nullable().default(null),
  dependency_type: z.string().default("blocked_by"),
}).loose();

export const IssueDependenciesResponseSchema = z.object({
  blocked_by: z.array(IssueDependencySummarySchema).default([]),
  blocks: z.array(IssueDependencySummarySchema).default([]),
}).loose();

export const EMPTY_ISSUE_DEPENDENCIES_RESPONSE: IssueDependenciesResponse = {
  blocked_by: [],
  blocks: [],
};

const StringListSchema = z.array(z.string()).catch([]).default([]);
const NumberListSchema = z.array(z.number()).catch([]).default([]);
const PlanItemExecutionKindSchema = z.preprocess(
  (value) => (value === "human_confirmation" ? value : "agent_task"),
  z.enum(["agent_task", "human_confirmation"]),
);
const PlanItemNodeTypeSchema = z.preprocess(
  (value) =>
    value === "manual" ||
    value === "check" ||
    value === "spec_review" ||
    value === "code_review"
      ? value
      : "issue",
  z.enum(["issue", "manual", "check", "spec_review", "code_review"]),
);

const PlanClarificationSchema = z.object({
  question: z.string().default(""),
  answer: z.string().default(""),
});

const PlanAcceptanceScenarioSchema = z.object({
  name: z.string().default(""),
  given: z.string().default(""),
  when: z.string().default(""),
  then: z.string().default(""),
});

export const PlanSpecSchema = z.object({
  summary: z.string().default(""),
  goal: z.string().default(""),
  success_criteria: StringListSchema,
  acceptance_scenarios: z.array(PlanAcceptanceScenarioSchema).catch([]).default([]),
  in_scope: StringListSchema,
  out_of_scope: StringListSchema,
  approach: z.string().default(""),
  design_decisions: StringListSchema,
  verification_commands: StringListSchema,
  assumptions: StringListSchema,
  open_questions: StringListSchema,
  clarifications: z.array(PlanClarificationSchema).catch([]).default([]),
});

export const EMPTY_PLAN_SPEC: PlanSpec = {
  summary: "",
  goal: "",
  success_criteria: [],
  acceptance_scenarios: [],
  in_scope: [],
  out_of_scope: [],
  approach: "",
  design_decisions: [],
  verification_commands: [],
  assumptions: [],
  open_questions: [],
  clarifications: [],
};

const PlanItemSchema = z.object({
  id: z.string(),
  plan_id: z.string(),
  position: z.number(),
  title: z.string(),
  description: z.string().default(""),
    acceptance_criteria: StringListSchema,
    suggested_test_commands: StringListSchema,
    unit_test_checklist: z.array(UnitTestCheckSchema).catch([]).default([]),
    context_resources: StringListSchema,
  risk_notes: StringListSchema,
  node_type: PlanItemNodeTypeSchema,
  execution_kind: PlanItemExecutionKindSchema,
  confirmation_question: z.string().catch("").default(""),
  confirmation_reason: z.string().catch("").default(""),
  required_evidence: StringListSchema,
  requires_git_commit: z.boolean().catch(true).default(true),
  branch_name: z.string().catch("").default(""),
  recommended_agent_id: z.string().nullable().default(null),
  match_score: z.number().default(0),
  match_reason: z.string().default(""),
  missing_capability: z.string().default(""),
  depends_on_positions: NumberListSchema,
  selected: z.boolean().default(true),
  generated_issue_id: z.string().nullable().default(null),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const PlanSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  title: z.string(),
  prompt: z.string(),
  status: z.string(),
  planner_agent_id: z.string(),
  task_id: z.string().default(""),
  project_id: z.string().nullable().default(null),
  parent_title: z.string().default(""),
  parent_description: z.string().default(""),
  parent_issue_id: z.string().nullable().default(null),
  spec: PlanSpecSchema.catch(EMPTY_PLAN_SPEC as z.infer<typeof PlanSpecSchema>).default(EMPTY_PLAN_SPEC as z.infer<typeof PlanSpecSchema>),
  committed_spec: PlanSpecSchema.nullable().catch(null).default(null),
  spec_approved_at: z.string().nullable().default(null),
  spec_approved_by: z.string().nullable().default(null),
  error: z.string().nullable().default(null),
  created_by: z.string().default(""),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
  items: z.array(PlanItemSchema).default([]),
}).loose();

export const EMPTY_PLAN: Plan = {
  id: "",
  workspace_id: "",
  title: "",
  prompt: "",
  status: "failed",
  planner_agent_id: "",
  task_id: "",
  project_id: null,
  parent_title: "",
  parent_description: "",
  parent_issue_id: null,
  spec: EMPTY_PLAN_SPEC,
  committed_spec: null,
  spec_approved_at: null,
  spec_approved_by: null,
  error: null,
  created_by: "",
  created_at: "",
  updated_at: "",
  items: [],
};

export const ListPlansResponseSchema = z.object({
  plans: z.array(PlanSchema).default([]),
}).loose();

export const EMPTY_LIST_PLANS_RESPONSE: ListPlansResponse = {
  plans: [],
};

const PipelineNodeTypeSchema = z.preprocess(
  (value) =>
    value === "manual" || value === "check" || value === "issue" || value === "spec_review" || value === "code_review" ? value : "issue",
  z.enum(["issue", "manual", "check", "spec_review", "code_review"]),
);

const PipelineNodeSchema = z.object({
  id: z.string(),
  pipeline_id: z.string(),
  key: z.string(),
  type: PipelineNodeTypeSchema,
  title: z.string(),
  description: z.string().default(""),
  agent_id: z.string().nullable().default(null),
  repo: z.string().nullable().default(null),
  repos: z.array(z.string()).default([]),
  depends_on_node_keys: z.array(z.string()).default([]),
  position: z.number().default(0),
  position_x: z.number().default(0),
  position_y: z.number().default(0),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const PipelineSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  description: z.string().default(""),
  is_system: z.boolean().default(false),
  system_key: z.string().nullable().default(null),
  editable: z.boolean().default(true),
  deletable: z.boolean().default(true),
  created_by: z.string().default(""),
  archived_at: z.string().nullable().default(null),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
  nodes: z.array(PipelineNodeSchema).default([]),
}).loose();

export const EMPTY_PIPELINE: Pipeline = {
  id: "",
  workspace_id: "",
  name: "",
  description: "",
  is_system: false,
  system_key: null,
  editable: true,
  deletable: true,
  created_by: "",
  archived_at: null,
  created_at: "",
  updated_at: "",
  nodes: [],
};

export const ListPipelinesResponseSchema = z.object({
  pipelines: z.array(PipelineSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_LIST_PIPELINES_RESPONSE: ListPipelinesResponse = {
  pipelines: [],
  total: 0,
};

const PipelineRunNodeSchema = z.object({
  id: z.string(),
  pipeline_run_id: z.string(),
  pipeline_node_id: z.string().nullable().default(null),
  node_key: z.string(),
  issue_id: z.string(),
  created_at: z.string().default(""),
}).loose();

export const PipelineRunSchema = z.object({
  id: z.string(),
  pipeline_id: z.string(),
  workspace_id: z.string(),
  project_id: z.string().nullable().default(null),
  parent_issue_id: z.string(),
  status: z.string(),
  created_by: z.string().default(""),
  created_at: z.string().default(""),
  nodes: z.array(PipelineRunNodeSchema).default([]),
}).loose();

export const EMPTY_PIPELINE_RUN: PipelineRun = {
  id: "",
  pipeline_id: "",
  workspace_id: "",
  project_id: null,
  parent_issue_id: "",
  status: "failed",
  created_by: "",
  created_at: "",
  nodes: [],
};

const ImportPipelineNodeSchema = z.object({
  key: z.string().default(""),
  type: PipelineNodeTypeSchema.optional(),
  title: z.string().default(""),
  description: z.string().default(""),
  agent_id: z.string().nullable().default(null),
  repo: z.string().nullable().default(null),
  repos: z.array(z.string()).default([]),
  depends_on_node_keys: z.array(z.string()).default([]),
  position_x: z.number().default(0),
  position_y: z.number().default(0),
}).loose();

const PipelineImportPreviewSchema = z.object({
  name: z.string().default(""),
  description: z.string().default(""),
  nodes: z.array(ImportPipelineNodeSchema).default([]),
}).loose();

export const PipelineImportValidationResponseSchema = z.object({
  valid: z.boolean().default(false),
  errors: z.array(z.string()).default([]),
  pipeline: PipelineImportPreviewSchema.nullable().default(null),
}).loose();

export const EMPTY_PIPELINE_IMPORT_VALIDATION_RESPONSE: PipelineImportValidationResponse = {
  valid: false,
  errors: [],
  pipeline: null,
};

const SubscriberSchema = z.object({
  issue_id: z.string(),
  user_type: z.string(),
  user_id: z.string(),
  reason: z.string(),
  created_at: z.string(),
}).loose();

export const SubscribersListSchema = z.array(SubscriberSchema);

export const ChildIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
}).loose();

export const OnboardingRuntimeBootstrapResponseSchema = z.object({
  workspace_id: z.string(),
  agent_id: z.string(),
  issue_id: z.string(),
}).loose();

export const OnboardingNoRuntimeBootstrapResponseSchema = z.object({
  workspace_id: z.string(),
  issue_id: z.string(),
}).loose();

// ---------------------------------------------------------------------------
// Workspace dashboard schemas
//
// The dashboard hits three independent rollup endpoints. Each returns a flat
// array, and every field is consumed by chart / KPI math — a missing number
// silently degrades to NaN downstream, so we coerce missing numbers to 0.
// String fields stay lenient (no enum narrowing) to survive future model /
// agent ID drift.
// ---------------------------------------------------------------------------

const DashboardUsageDailySchema = z.object({
  date: z.string(),
  model: z.string(),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageDailyListSchema = z.array(DashboardUsageDailySchema);

const DashboardUsageByAgentSchema = z.object({
  agent_id: z.string(),
  model: z.string(),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageByAgentListSchema = z.array(DashboardUsageByAgentSchema);

const DashboardAgentRunTimeSchema = z.object({
  agent_id: z.string(),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardAgentRunTimeListSchema = z.array(DashboardAgentRunTimeSchema);

const DashboardRunTimeDailySchema = z.object({
  date: z.string(),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardRunTimeDailyListSchema = z.array(DashboardRunTimeDailySchema);

// ---------------------------------------------------------------------------
// Agent template catalog — `/api/agent-templates*` and the
// create-from-template response. The desktop app's create-agent picker
// reaches these endpoints, and a future server change to the template shape
// would white-screen older installed builds (#2192 pattern) without these
// parsers. Lenient by the same rules as IssueSchema above: arrays default to
// `[]`, optional fields stay optional, `.loose()` lets unknown fields pass
// through unchanged.
// ---------------------------------------------------------------------------

const AgentTemplateSkillRefSchema = z.object({
  source_url: z.string(),
  cached_name: z.string().default(""),
  cached_description: z.string().default(""),
}).loose();

const AgentTemplateSummarySchemaBase = z.object({
  slug: z.string(),
  name: z.string(),
  description: z.string().default(""),
  category: z.string().optional(),
  icon: z.string().optional(),
  accent: z.string().optional(),
  // skills MUST default to [] — picker code reads `template.skills.length`
  // and `.map(...)`, both of which crash on `undefined`. The most common
  // future drift (field renamed / wrapped) lands here.
  skills: z.array(AgentTemplateSkillRefSchema).default([]),
}).loose();

export const AgentTemplateSummarySchema = AgentTemplateSummarySchemaBase;

// List endpoint historically returns a bare array. Server could legitimately
// migrate to `{templates: [...]}` later — we accept either shape so an old
// desktop survives the upgrade.
export const AgentTemplateSummaryListSchema = z.union([
  z.array(AgentTemplateSummarySchemaBase),
  z.object({ templates: z.array(AgentTemplateSummarySchemaBase).default([]) })
    .loose()
    .transform((v) => v.templates),
]);

export const EMPTY_AGENT_TEMPLATE_SUMMARY_LIST: AgentTemplateSummary[] = [];

export const AgentTemplateSchema = AgentTemplateSummarySchemaBase.extend({
  // Detail-only field. Default "" so a malformed detail still renders the
  // header + skill list; the user just sees an empty Instructions block.
  instructions: z.string().default(""),
}).loose();

// Used as the parse fallback for `GET /api/agent-templates/:slug`. Slug comes
// from the URL, so we round-trip the requested one back into the fallback
// at the call site (see `getAgentTemplate` in client.ts).
export const EMPTY_AGENT_TEMPLATE_DETAIL: AgentTemplate = {
  slug: "",
  name: "",
  description: "",
  skills: [],
  instructions: "",
};

// `agent` is a full Agent record — schematising every field would duplicate
// a 50-field interface and bit-rot fast. We keep it loose and require only
// `id`, the one field the create-from-template flow consumes (used to
// navigate to the new agent's detail page). Downstream code already
// optional-chains the rest.
const MinimalAgentSchema = z.object({
  id: z.string(),
}).loose();

export const CreateAgentFromTemplateResponseSchema = z.object({
  agent: MinimalAgentSchema,
  imported_skill_ids: z.array(z.string()).default([]),
  reused_skill_ids: z.array(z.string()).default([]),
}).loose();

// Fallback when the success response fails to parse. The agent server-side
// has likely been created already, so we can't pretend nothing happened —
// the caller (`create-agent-dialog.tsx`) is responsible for noticing
// `agent.id === ""` and skipping navigation while keeping the list
// invalidation, so the user finds their new agent in the list.
export const EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE: CreateAgentFromTemplateResponse = {
  agent: { id: "" } as Agent,
  imported_skill_ids: [],
  reused_skill_ids: [],
};

// ---------------------------------------------------------------------------
// Structured error body — POST /api/workspaces/:wsId/issues 409 conflict.
//
// When the server detects an active issue with the same title in the same
// workspace, it returns `{ code: "active_duplicate_issue", error, issue }`
// instead of letting the create through. The UI uses the embedded issue ref
// to offer "view existing" rather than dropping the user into a generic
// "create failed" toast.
//
// Strict guarantees:
//   - `code` is a literal so a future server rename (e.g. `duplicate_issue`)
//     fails the parse and falls back to a normal error toast — drift never
//     ships as a broken duplicate UI.
//   - `issue` is required; without an id/identifier/title the "view existing"
//     button has nothing to point at, so we'd rather fall back than guess.
//   - `issue.status` is intentionally OMITTED: the duplicate toast doesn't
//     render a StatusIcon (which has no fallback for unknown enum values),
//     so a future server-side rename of `status` must not knock this branch
//     out. `.loose()` lets the field pass through unchanged for any other
//     consumer.
// ---------------------------------------------------------------------------

export const DuplicateIssueErrorBodySchema = z.object({
  code: z.literal("active_duplicate_issue"),
  error: z.string().optional(),
  issue: z.object({
    id: z.string(),
    identifier: z.string(),
    title: z.string(),
  }).loose(),
}).loose();

export interface DuplicateIssueErrorBody {
  code: "active_duplicate_issue";
  error?: string;
  issue: {
    id: string;
    identifier: string;
    title: string;
  };
}

// ---------------------------------------------------------------------------
// Webhook delivery schemas — backing the Autopilot Deliveries section. Enums
// (`status`, `signature_status`, `provider`) are kept as `z.string()` so a
// future server-side value (e.g. a Stripe provider, a new dedupe state)
// degrades to a generic UI fallback rather than collapsing the list into
// the empty array. `.loose()` lets unknown fields pass through, matching
// the rule used by every other endpoint here.
// ---------------------------------------------------------------------------

const WebhookDeliverySchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  autopilot_id: z.string(),
  trigger_id: z.string(),
  provider: z.string(),
  event: z.string(),
  dedupe_key: z.string().nullable(),
  dedupe_source: z.string().nullable(),
  signature_status: z.string(),
  status: z.string(),
  attempt_count: z.number().default(0),
  content_type: z.string().nullable(),
  response_status: z.number().nullable(),
  autopilot_run_id: z.string().nullable(),
  replayed_from_delivery_id: z.string().nullable(),
  error: z.string().nullable(),
  received_at: z.string(),
  last_attempt_at: z.string(),
  created_at: z.string(),
  // Detail-only fields. The list endpoint omits them; the detail endpoint
  // populates raw_body / selected_headers / response_body.
  selected_headers: z.record(z.string(), z.unknown()).nullable().optional(),
  raw_body: z.string().nullable().optional(),
  response_body: z.string().nullable().optional(),
}).loose();

export const ListWebhookDeliveriesResponseSchema = z.object({
  deliveries: z.array(WebhookDeliverySchema).default([]),
  total: z.number().default(0),
}).loose();

export const WebhookDeliveryResponseSchema = WebhookDeliverySchema;

export const EMPTY_LIST_WEBHOOK_DELIVERIES_RESPONSE: ListWebhookDeliveriesResponse = {
  deliveries: [],
  total: 0,
};

export const EMPTY_WEBHOOK_DELIVERY: WebhookDelivery = {
  id: "",
  workspace_id: "",
  autopilot_id: "",
  trigger_id: "",
  provider: "",
  event: "",
  dedupe_key: null,
  dedupe_source: null,
  signature_status: "not_required",
  status: "queued",
  attempt_count: 0,
  content_type: null,
  response_status: null,
  autopilot_run_id: null,
  replayed_from_delivery_id: null,
  error: null,
  received_at: "",
  last_attempt_at: "",
  created_at: "",
};

// ---------------------------------------------------------------------------
// User (`/api/me` GET + PATCH). The auth store and Settings → Account both
// trust this shape — a drift here would knock both surfaces out. Kept
// lenient by the same rules as IssueSchema: enums stay `z.string()`,
// nullable fields are unioned with `null`, unknown server fields pass
// through via `.loose()`. `profile_description` is the field added in
// MUL-2406; the server emits `""` when unset (NOT NULL DEFAULT ''), so
// the schema defaults to `""` too — keeps the type tight without
// breaking older backends that don't return the column yet.
// ---------------------------------------------------------------------------

export const UserSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  email: z.string().default(""),
  avatar_url: z.string().nullable().default(null),
  onboarded_at: z.string().nullable().default(null),
  onboarding_questionnaire: z.record(z.string(), z.unknown()).default({}),
  starter_content_state: z.string().nullable().default(null),
  language: z.string().nullable().default(null),
  profile_description: z.string().default(""),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const EMPTY_USER: User = {
  id: "",
  name: "",
  email: "",
  avatar_url: null,
  onboarded_at: null,
  onboarding_questionnaire: {},
  starter_content_state: null,
  language: null,
  profile_description: "",
  created_at: "",
  updated_at: "",
};
