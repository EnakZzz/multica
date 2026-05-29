"use client";

import { useMemo, useState, useCallback, useRef, useEffect } from "react";
import { useDefaultLayout, usePanelRef } from "react-resizable-panels";
import { BookOpen, Check, ChevronRight, Database, Download, Eye, FileText, Image as ImageIcon, Link2, ListTodo, MoreHorizontal, PanelRight, Pencil, Pin, PinOff, Plus, Search, Trash2, Upload, UserMinus } from "lucide-react";
import { useMutation, useQuery, type QueryKey } from "@tanstack/react-query";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import type { Issue, IssueAssigneeGroup, ProjectResource, ProjectStatus, ProjectPriority, ProjectWikiPage, SourceFileResourceRef, UpdateIssueRequest } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { projectDetailOptions, projectResourcesOptions, useCreateProjectResource, useUpdateProject, useDeleteProject } from "@multica/core/projects";
import { api } from "@multica/core/api";
import {
  projectMemoryItemsOptions,
  projectKnowledgeRetrievalLogsOptions,
  projectWikiPagesOptions,
} from "@multica/core/project-knowledge";
import {
  useCreateProjectWikiPage,
  useUpdateProjectKnowledgeRetrievalLogFeedback,
  useUpdateProjectWikiPage,
} from "@multica/core/project-knowledge";
import type { ProjectKnowledgeRetrievalLog, ProjectKnowledgeSearchResult } from "@multica/core/types";
import { pinListOptions } from "@multica/core/pins";
import { useCreatePin, useDeletePin } from "@multica/core/pins";
import {
  myIssueAssigneeGroupsOptions,
  myIssueListOptions,
  projectGanttIssuesOptions,
  childIssueProgressOptions,
  type AssigneeGroupedIssuesFilter,
  type MyIssuesFilter,
} from "@multica/core/issues/queries";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useModalStore } from "@multica/core/modals";
import { memberListOptions, agentListOptions } from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { PROJECT_STATUS_ORDER, PROJECT_STATUS_CONFIG, PROJECT_PRIORITY_ORDER } from "@multica/core/projects/config";
import { BOARD_STATUSES } from "@multica/core/issues/config";
import { createIssueViewStore } from "@multica/core/issues/stores/view-store";
import { ViewStoreProvider, useViewStore } from "@multica/core/issues/stores/view-store-context";
import { filterIssues } from "../../issues/utils/filter";
import { getProjectIssueMetrics } from "./project-issue-metrics";
import { ActorAvatar } from "../../common/actor-avatar";
import { Markdown } from "../../common/markdown";
import { AppLink, useNavigation } from "../../navigation";
import { TitleEditor, ContentEditor, type ContentEditorRef } from "../../editor";
import { PriorityIcon } from "../../issues/components/priority-icon";
import { ProjectResourcesSection } from "./project-resources-section";
import { ProjectVisualCanvas } from "./project-visual-canvas";
import { IssuesHeader } from "../../issues/components/issues-header";
import { BoardView } from "../../issues/components/board-view";
import { ListView } from "../../issues/components/list-view";
import { GanttView } from "../../issues/components/gantt-view";
import { BatchActionToolbar } from "../../issues/components/batch-action-toolbar";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { ResizablePanelGroup, ResizablePanel, ResizableHandle } from "@multica/ui/components/ui/resizable";
import { Sheet, SheetContent } from "@multica/ui/components/ui/sheet";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { EmojiPicker } from "@multica/ui/components/common/emoji-picker";
import { PageHeader } from "../../layout/page-header";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../i18n";
import { useProjectStatusLabels, useProjectPriorityLabels } from "./labels";

// ---------------------------------------------------------------------------
// Property row — sidebar property display
// ---------------------------------------------------------------------------

function PropRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-8 items-center gap-2 rounded-md px-2 -mx-2 hover:bg-accent/50 transition-colors">
      <span className="w-16 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="flex min-w-0 flex-1 items-center gap-1.5 text-xs truncate">
        {children}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Project Issues — reuses the existing issues list/board components
// ---------------------------------------------------------------------------

const projectViewStore = createIssueViewStore("project_issues_view");

function ProjectIssuesContent({
  projectId,
  projectIssues,
  assigneeGroups,
  assigneeGroupQueryKey,
  assigneeGroupFilter,
  scope,
  filter,
  ganttIssues,
}: {
  projectId: string;
  projectIssues: Issue[];
  assigneeGroups?: IssueAssigneeGroup[];
  assigneeGroupQueryKey?: QueryKey;
  assigneeGroupFilter?: AssigneeGroupedIssuesFilter;
  scope: string;
  filter: MyIssuesFilter;
  ganttIssues: Issue[];
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const viewMode = useViewStore((s) => s.viewMode);
  const statusFilters = useViewStore((s) => s.statusFilters);
  const priorityFilters = useViewStore((s) => s.priorityFilters);
  const assigneeFilters = useViewStore((s) => s.assigneeFilters);
  const includeNoAssignee = useViewStore((s) => s.includeNoAssignee);
  const creatorFilters = useViewStore((s) => s.creatorFilters);
  const labelFilters = useViewStore((s) => s.labelFilters);

  const issues = useMemo(
    () => filterIssues(projectIssues, { statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters: [], includeNoProject: false, labelFilters }),
    [projectIssues, statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, labelFilters],
  );

  // Gantt rides its own dedicated query (scheduled-only) so it doesn't have
  // to wait for every status bucket to paginate in. View-store filters still
  // apply so toggling priority / assignee / label hides the same bars.
  const filteredGanttIssues = useMemo(
    () => filterIssues(ganttIssues, { statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, projectFilters: [], includeNoProject: false, labelFilters }),
    [ganttIssues, statusFilters, priorityFilters, assigneeFilters, includeNoAssignee, creatorFilters, labelFilters],
  );

  const { data: childProgressMap = new Map() } = useQuery(childIssueProgressOptions(wsId));

  const visibleStatuses = useMemo(() => {
    if (statusFilters.length > 0)
      return BOARD_STATUSES.filter((s) => statusFilters.includes(s));
    return BOARD_STATUSES;
  }, [statusFilters]);

  const hiddenStatuses = useMemo(
    () => BOARD_STATUSES.filter((s) => !visibleStatuses.includes(s)),
    [visibleStatuses],
  );

  const updateIssueMutation = useUpdateIssue();
  const handleMoveIssue = useCallback(
    (issueId: string, updates: Pick<UpdateIssueRequest, "status" | "assignee_type" | "assignee_id" | "position">) => {
      updateIssueMutation.mutate(
        { id: issueId, ...updates },
        {
          onError: (err) =>
            toast.error(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.detail.toast_move_issue_failed),
            ),
        },
      );
    },
    [updateIssueMutation, t],
  );

  // Gantt has its own data source (scheduled-only) and its own empty axis —
  // we never short-circuit it here, otherwise an unscheduled-but-non-empty
  // project would surface a misleading "no issues" CTA. For Board/List the
  // bucketed cache really is the ground truth, so an empty result means an
  // empty project.
  if (viewMode !== "gantt" && projectIssues.length === 0) {
    return (
      <div className="flex flex-1 min-h-0 flex-col items-center justify-center gap-3 text-muted-foreground">
        <ListTodo className="h-10 w-10 text-muted-foreground/40" />
        <p className="text-sm">{t(($) => $.detail.empty_issues_title)}</p>
        <p className="text-xs">{t(($) => $.detail.empty_issues_hint)}</p>
        <Button
          variant="outline"
          size="sm"
          className="mt-1"
          onClick={() =>
            useModalStore.getState().open("create-issue", { project_id: projectId })
          }
        >
          <Plus className="size-3.5 mr-1.5" />
          {t(($) => $.detail.empty_issues_new_button)}
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col flex-1 min-h-0">
      {viewMode === "board" && (
        <BoardView
          issues={assigneeGroups ? projectIssues : issues}
          assigneeGroups={assigneeGroups}
          assigneeGroupQueryKey={assigneeGroupQueryKey}
          assigneeGroupFilter={assigneeGroupFilter}
          visibleStatuses={visibleStatuses}
          hiddenStatuses={hiddenStatuses}
          onMoveIssue={handleMoveIssue}
          childProgressMap={childProgressMap}
          myIssuesScope={scope}
          myIssuesFilter={filter}
          projectId={projectId}
        />
      )}
      {viewMode === "list" && (
        <ListView
          issues={issues}
          visibleStatuses={visibleStatuses}
          childProgressMap={childProgressMap}
          myIssuesScope={scope}
          myIssuesFilter={filter}
          projectId={projectId}
        />
      )}
      {viewMode === "gantt" && <GanttView issues={filteredGanttIssues} />}
    </div>
  );
}

function ProjectIssuesSurface({
  projectId,
  scope,
  filter,
}: {
  projectId: string;
  scope: string;
  filter: MyIssuesFilter;
}) {
  const wsId = useWorkspaceId();
  const viewMode = useViewStore((s) => s.viewMode);
  const grouping = useViewStore((s) => s.grouping);
  const statusFilters = useViewStore((s) => s.statusFilters);
  const priorityFilters = useViewStore((s) => s.priorityFilters);
  const assigneeFilters = useViewStore((s) => s.assigneeFilters);
  const includeNoAssignee = useViewStore((s) => s.includeNoAssignee);
  const creatorFilters = useViewStore((s) => s.creatorFilters);
  const labelFilters = useViewStore((s) => s.labelFilters);
  const usesAssigneeBoard = viewMode === "board" && grouping === "assignee";
  const usesGantt = viewMode === "gantt";
  const assigneeGroupFilter = useMemo<AssigneeGroupedIssuesFilter>(
    () => ({
      ...filter,
      statuses: statusFilters.length > 0 ? statusFilters : [...BOARD_STATUSES],
      priorities: priorityFilters,
      assignee_filters: assigneeFilters,
      include_no_assignee: includeNoAssignee,
      creator_filters: creatorFilters,
      label_ids: labelFilters,
    }),
    [assigneeFilters, creatorFilters, filter, includeNoAssignee, labelFilters, priorityFilters, statusFilters],
  );
  const assigneeGroupsOptions = myIssueAssigneeGroupsOptions(
    wsId,
    scope,
    assigneeGroupFilter,
  );
  // Each view owns exactly one data source. Board/List ride the bucketed
  // `myIssueListOptions` cache; the assignee-grouped board uses the grouped
  // endpoint; Gantt has its own scheduled-only fetch. We gate `enabled` on
  // the current view so switching to Gantt doesn't re-trigger the full
  // per-status fetch in the background.
  const statusIssuesQuery = useQuery({
    ...myIssueListOptions(wsId, scope, filter),
    enabled: !usesAssigneeBoard && !usesGantt,
  });
  const assigneeGroupsQuery = useQuery({
    ...assigneeGroupsOptions,
    enabled: usesAssigneeBoard,
  });
  // Gantt has its own data source — a single (paginated) fetch of every
  // scheduled issue in the project. Independent from the bucketed Board/List
  // cache so it isn't bottlenecked by per-status pagination and reacts in
  // isolation to WS updates that move issues into or out of the scheduled
  // set.
  const ganttIssuesQuery = useQuery({
    ...projectGanttIssuesOptions(wsId, projectId),
    enabled: usesGantt,
  });
  const bucketedIssues = usesAssigneeBoard
    ? (assigneeGroupsQuery.data?.groups.flatMap((group) => group.issues) ?? [])
    : (statusIssuesQuery.data ?? []);
  const ganttIssues = ganttIssuesQuery.data ?? [];
  // What the header empty-state check looks at depends on the view: Gantt
  // would otherwise be blamed for an empty Board cache, even though it has
  // its own (potentially non-empty) scheduled cache.
  const projectIssues = usesGantt ? ganttIssues : bucketedIssues;

  return (
    <>
      <IssuesHeader scopedIssues={projectIssues} allowGantt />
      <ProjectIssuesContent
        projectId={projectId}
        projectIssues={projectIssues}
        assigneeGroups={usesAssigneeBoard ? assigneeGroupsQuery.data?.groups : undefined}
        assigneeGroupQueryKey={usesAssigneeBoard ? assigneeGroupsOptions.queryKey : undefined}
        assigneeGroupFilter={usesAssigneeBoard ? assigneeGroupFilter : undefined}
        scope={scope}
        filter={filter}
        ganttIssues={ganttIssues}
      />
      <BatchActionToolbar />
    </>
  );
}

function RetrievalLogCard({
  log,
  onFeedback,
}: {
  log: ProjectKnowledgeRetrievalLog;
  onFeedback: (feedback: "useful" | "noisy") => void;
}) {
  const first = log.selected_items[0];
  const label = first?.title || log.query_text || log.status;
  return (
    <div className="rounded-md border bg-background p-2">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium">{log.status}</span>
        <span className="text-[11px] text-muted-foreground">
          {log.injected_item_count} injected
        </span>
      </div>
      <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{label}</p>
      <div className="mt-1 flex flex-wrap gap-1 text-[11px] text-muted-foreground">
        <span>{log.search_mode}</span>
        {log.task_outcome && <span>· {log.task_outcome}</span>}
        {log.feedback && <span>· {log.feedback}</span>}
      </div>
      <div className="mt-2 flex gap-1">
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[11px]"
          onClick={() => onFeedback("useful")}
        >
          Useful
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[11px]"
          onClick={() => onFeedback("noisy")}
        >
          Noisy
        </Button>
      </div>
    </div>
  );
}

type WikiTreeNode = WikiTreeFolderNode | WikiTreePageNode;

interface WikiTreeFolderNode {
  kind: "folder";
  name: string;
  path: string;
  children: WikiTreeNode[];
}

interface WikiTreePageNode {
  kind: "page";
  name: string;
  path: string;
  page: ProjectWikiPage;
}

type WikiTreeRow =
  | { kind: "folder"; node: WikiTreeFolderNode; depth: number }
  | { kind: "page"; node: WikiTreePageNode; depth: number };

function wikiSegmentLabel(segment: string): string {
  return segment
    .replace(/^\d+[-_]/, "")
    .replace(/[-_]+/g, " ")
    .trim()
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

function sortWikiTreeNodes(nodes: WikiTreeNode[]): WikiTreeNode[] {
  return nodes
    .map((node) =>
      node.kind === "folder"
        ? { ...node, children: sortWikiTreeNodes(node.children) }
        : node,
    )
    .sort((a, b) => {
      if (a.kind !== b.kind) {
        return a.kind === "folder" ? -1 : 1;
      }
      return a.path.localeCompare(b.path, undefined, { numeric: true });
    });
}

function buildWikiTree(pages: ProjectWikiPage[]): WikiTreeNode[] {
  const root: WikiTreeFolderNode = { kind: "folder", name: "", path: "", children: [] };
  const folders = new Map<string, WikiTreeFolderNode>([["", root]]);

  for (const page of pages) {
    const parts = page.slug.split("/").map((part) => part.trim()).filter(Boolean);
    const pageSegment = parts.pop() ?? page.slug;
    let parent = root;
    let currentPath = "";

    for (const part of parts) {
      currentPath = currentPath ? `${currentPath}/${part}` : part;
      let folder = folders.get(currentPath);
      if (!folder) {
        folder = {
          kind: "folder",
          name: wikiSegmentLabel(part),
          path: currentPath,
          children: [],
        };
        folders.set(currentPath, folder);
        parent.children.push(folder);
      }
      parent = folder;
    }

    const pagePath = parts.length > 0 ? `${parts.join("/")}/${pageSegment}` : pageSegment;
    parent.children.push({
      kind: "page",
      name: page.title || wikiSegmentLabel(pageSegment),
      path: pagePath,
      page,
    });
  }

  return sortWikiTreeNodes(root.children);
}

function flattenWikiTree(
  nodes: WikiTreeNode[],
  expandedFolders: Record<string, boolean>,
  depth = 0,
): WikiTreeRow[] {
  const rows: WikiTreeRow[] = [];
  for (const node of nodes) {
    if (node.kind === "folder") {
      rows.push({ kind: "folder", node, depth });
      if (expandedFolders[node.path] !== false) {
        rows.push(...flattenWikiTree(node.children, expandedFolders, depth + 1));
      }
      continue;
    }
    rows.push({ kind: "page", node, depth });
  }
  return rows;
}

function isSourceFileResource(
  resource: ProjectResource,
): resource is ProjectResource & { resource_ref: SourceFileResourceRef } {
  if (resource.resource_type !== "source_file") {
    return false;
  }
  const ref = resource.resource_ref as Partial<SourceFileResourceRef>;
  return (
    typeof ref.attachment_id === "string" &&
    typeof ref.filename === "string" &&
    typeof ref.content_type === "string" &&
    typeof ref.size_bytes === "number"
  );
}

function formatFileSize(sizeBytes: number): string {
  if (!Number.isFinite(sizeBytes) || sizeBytes <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB"];
  let value = sizeBytes;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function SourceFileArchive({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [uploading, setUploading] = useState(false);
  const { data: resources = [] } = useQuery(
    projectResourcesOptions(wsId, projectId),
  );
  const createResource = useCreateProjectResource(wsId, projectId);
  const sourceFiles = resources.filter(isSourceFileResource);

  const handleUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) {
      return;
    }
    setUploading(true);
    try {
      const attachment = await api.uploadFile(file);
      if (!attachment.id) {
        throw new Error("Upload did not return an attachment id.");
      }
      await createResource.mutateAsync({
        resource_type: "source_file",
        resource_ref: {
          attachment_id: attachment.id,
          filename: attachment.filename,
          content_type: attachment.content_type,
          size_bytes: attachment.size_bytes,
        },
        label: attachment.filename,
      });
      toast.success("Source file archived.");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to archive source file.");
    } finally {
      setUploading(false);
    }
  };

  const handleDownload = async (resource: ProjectResource & { resource_ref: SourceFileResourceRef }) => {
    try {
      const attachment = await api.getAttachment(resource.resource_ref.attachment_id);
      const url = attachment.download_url || attachment.url;
      if (!url) {
        throw new Error("Download URL is missing.");
      }
      const opened = window.open(url, "_blank", "noopener,noreferrer");
      if (opened) {
        opened.opener = null;
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to open source file.");
    }
  };

  return (
    <div className="mt-6">
      <div className="mb-3 flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          <FileText className="h-3.5 w-3.5" />
          Source files
        </div>
        <input
          ref={inputRef}
          type="file"
          className="hidden"
          onChange={handleUpload}
        />
        <Button
          variant="ghost"
          size="icon-sm"
          title="Archive source file"
          disabled={uploading || createResource.isPending}
          onClick={() => inputRef.current?.click()}
        >
          <Upload className="h-4 w-4" />
        </Button>
      </div>
      <div className="space-y-1.5">
        {sourceFiles.length === 0 ? (
          <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
            No source files archived.
          </div>
        ) : (
          sourceFiles.map((resource) => {
            const ref = resource.resource_ref;
            return (
              <div key={resource.id} className="flex items-center gap-2 rounded-md border bg-background p-2 text-xs">
                <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium" title={ref.filename}>
                    {ref.filename}
                  </div>
                  <div className="truncate text-[11px] text-muted-foreground">
                    {formatFileSize(ref.size_bytes)} · {ref.content_type}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  title="Download source file"
                  onClick={() => handleDownload(resource)}
                >
                  <Download className="h-3.5 w-3.5" />
                </Button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function ProjectKnowledgeSurface({ projectId }: { projectId: string }) {
  const newWikiPageId = "__new_wiki_page__";
  const wsId = useWorkspaceId();
  const { data: wikiPages = [], isLoading: wikiLoading } = useQuery(
    projectWikiPagesOptions(wsId, projectId),
  );
  const { data: memoryItems = [], isLoading: memoryLoading } = useQuery(
    projectMemoryItemsOptions(wsId, projectId),
  );
  const { data: retrievalLogs = [], isLoading: retrievalLoading } = useQuery(
    projectKnowledgeRetrievalLogsOptions(wsId, projectId),
  );
  const createWikiPage = useCreateProjectWikiPage(projectId);
  const updateWikiPage = useUpdateProjectWikiPage(projectId);
  const updateRetrievalFeedback = useUpdateProjectKnowledgeRetrievalLogFeedback(projectId);
  const [selectedPageId, setSelectedPageId] = useState<string>("");
  const [draftTitle, setDraftTitle] = useState("");
  const [draftSlug, setDraftSlug] = useState("");
  const [draftBody, setDraftBody] = useState("");
  const [isEditingPage, setIsEditingPage] = useState(false);
  const [expandedWikiFolders, setExpandedWikiFolders] = useState<Record<string, boolean>>({});
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<ProjectKnowledgeSearchResult[]>([]);
  const [searchError, setSearchError] = useState("");
  const wikiTree = useMemo(() => buildWikiTree(wikiPages), [wikiPages]);
  const wikiTreeRows = useMemo(
    () => flattenWikiTree(wikiTree, expandedWikiFolders),
    [wikiTree, expandedWikiFolders],
  );

  const selectedPage =
    selectedPageId === newWikiPageId
      ? undefined
      : (wikiPages.find((page) => page.id === selectedPageId) ?? wikiPages[0]);

  useEffect(() => {
    if (selectedPageId === newWikiPageId) {
      return;
    }
    if (!selectedPage) {
      setSelectedPageId("");
      setDraftTitle("");
      setDraftSlug("");
      setDraftBody("");
      setIsEditingPage(false);
      return;
    }
    setSelectedPageId(selectedPage.id);
    setDraftTitle(selectedPage.title);
    setDraftSlug(selectedPage.slug);
    setDraftBody(selectedPage.body);
    setIsEditingPage(false);
  }, [selectedPage?.id, selectedPageId]);

  const searchMutation = useMutation({
    mutationFn: (value: string) =>
      api.searchProjectKnowledge(projectId, { query: value, limit: 8 }),
    onSuccess: (data) => {
      setSearchResults(data.results);
      setSearchError(data.configured ? "" : (data.error ?? "Embeddings are not configured."));
    },
    onError: (err) => {
      setSearchResults([]);
      setSearchError(err instanceof Error ? err.message : "Search failed.");
    },
  });

  const handleNewPage = () => {
    setSelectedPageId(newWikiPageId);
    setDraftTitle("New knowledge page");
    setDraftSlug(`knowledge/new-page-${Date.now().toString(36)}`);
    setDraftBody("");
    setIsEditingPage(true);
  };

  const handleSavePage = () => {
    const payload = {
      title: draftTitle.trim(),
      body: draftBody,
      status: "draft" as const,
      source_refs: [],
    };
    if (!payload.title) {
      toast.error("Title is required.");
      return;
    }
    if (selectedPageId && selectedPageId !== newWikiPageId) {
      updateWikiPage.mutate(
        { pageId: selectedPageId, ...payload },
        { onSuccess: () => {
          setIsEditingPage(false);
          toast.success("Wiki page saved.");
        } },
      );
      return;
    }
    if (!draftSlug.trim()) {
      toast.error("Slug is required.");
      return;
    }
    createWikiPage.mutate(
      { slug: draftSlug.trim(), ...payload },
      { onSuccess: (page) => {
        setSelectedPageId(page.id);
        setIsEditingPage(false);
        toast.success("Wiki page created.");
      } },
    );
  };

  return (
    <div className="flex min-h-0 flex-1 gap-0 border-t">
      <aside className="w-64 shrink-0 overflow-y-auto border-r bg-muted/20 p-3">
        <div className="mb-3 flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            <BookOpen className="h-3.5 w-3.5" />
            Wiki
          </div>
          <Button variant="ghost" size="icon-sm" onClick={handleNewPage} title="New wiki page">
            <Plus className="h-4 w-4" />
          </Button>
        </div>
        <div className="space-y-0.5">
          {wikiLoading ? (
            <Skeleton className="h-8 w-full" />
          ) : wikiPages.length === 0 ? (
            <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
              No wiki pages yet.
            </div>
          ) : (
            wikiTreeRows.map((row) => {
              const paddingLeft = 8 + row.depth * 14;
              if (row.kind === "folder") {
                const expanded = expandedWikiFolders[row.node.path] !== false;
                return (
                  <button
                    key={`folder-${row.node.path}`}
                    type="button"
                    onClick={() =>
                      setExpandedWikiFolders((current) => ({
                        ...current,
                        [row.node.path]: !expanded,
                      }))
                    }
                    className="flex h-7 w-full items-center gap-1 rounded-md pr-2 text-left text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                    style={{ paddingLeft }}
                  >
                    <ChevronRight className={cn("h-3.5 w-3.5 shrink-0 transition-transform", expanded && "rotate-90")} />
                    <span className="truncate">{row.node.name}</span>
                  </button>
                );
              }

              const page = row.node.page;
              return (
                <button
                  key={page.id}
                  type="button"
                  onClick={() => setSelectedPageId(page.id)}
                  className={cn(
                    "block w-full rounded-md py-1.5 pr-2 text-left text-sm transition-colors hover:bg-accent",
                    page.id === selectedPage?.id && "bg-accent text-accent-foreground",
                  )}
                  style={{ paddingLeft }}
                  title={page.slug}
                >
                  <span className="block truncate font-medium">{page.title}</span>
                  <span className="block truncate text-[11px] text-muted-foreground">
                    {page.slug.split("/").pop() ?? page.slug}
                  </span>
                </button>
              );
            })
          )}
        </div>

        <SourceFileArchive projectId={projectId} />

        <div className="mt-6 mb-3 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          <Database className="h-3.5 w-3.5" />
          Memory
        </div>
        <div className="space-y-2">
          {memoryLoading ? (
            <Skeleton className="h-14 w-full" />
          ) : memoryItems.length === 0 ? (
            <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
              No captured memory yet.
            </div>
          ) : (
            memoryItems.slice(0, 12).map((item) => (
              <div key={item.id} className="rounded-md border bg-background p-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-xs font-medium">{item.kind}</span>
                  <span className="text-[11px] text-muted-foreground">{item.confidence}</span>
                </div>
                <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{item.title}</p>
              </div>
            ))
          )}
        </div>

        <div className="mt-6 mb-3 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          <Search className="h-3.5 w-3.5" />
          Retrieval logs
        </div>
        <div className="space-y-2">
          {retrievalLoading ? (
            <Skeleton className="h-14 w-full" />
          ) : retrievalLogs.length === 0 ? (
            <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
              No retrieval trace yet.
            </div>
          ) : (
            retrievalLogs.slice(0, 8).map((log) => (
              <RetrievalLogCard
                key={log.id}
                log={log}
                onFeedback={(feedback) =>
                  updateRetrievalFeedback.mutate({ logId: log.id, feedback })
                }
              />
            ))
          )}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="border-b p-3">
          <div className="flex items-center gap-2">
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search project knowledge"
              className="h-8"
              onKeyDown={(event) => {
                if (event.key === "Enter" && query.trim()) {
                  searchMutation.mutate(query.trim());
                }
              }}
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => query.trim() && searchMutation.mutate(query.trim())}
              disabled={!query.trim() || searchMutation.isPending}
            >
              <Search className="mr-1.5 h-3.5 w-3.5" />
              Search
            </Button>
          </div>
          {searchError && <p className="mt-2 text-xs text-destructive">{searchError}</p>}
          {searchResults.length > 0 && (
            <div className="mt-3 grid gap-2 md:grid-cols-2">
              {searchResults.map((result) => {
                const title = result.wiki_page?.title ?? result.memory_item?.title ?? result.target_type;
                const summary = result.memory_item?.summary ?? result.wiki_page?.body ?? result.snippet;
                return (
                  <div key={`${result.target_type}-${result.wiki_page?.id ?? result.memory_item?.id}`} className="rounded-md border bg-background p-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-xs font-medium">{title}</span>
                      <span className="text-[11px] text-muted-foreground">{result.score.toFixed(2)}</span>
                    </div>
                    <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{summary}</p>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="flex min-h-0 flex-1 flex-col p-4">
          <div className="mb-3 flex items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 className="truncate text-lg font-semibold">
                {draftTitle.trim() || "Untitled wiki page"}
              </h2>
              <p className="mt-0.5 truncate text-xs text-muted-foreground">
                {draftSlug.trim() || "unsaved"}
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setIsEditingPage((value) => !value)}
            >
              {isEditingPage ? (
                <Eye className="mr-1.5 h-3.5 w-3.5" />
              ) : (
                <Pencil className="mr-1.5 h-3.5 w-3.5" />
              )}
              {isEditingPage ? "Preview" : "Edit"}
            </Button>
          </div>

          {isEditingPage ? (
            <>
              <div className="mb-3 grid gap-2 md:grid-cols-[1fr_220px]">
                <Input
                  value={draftTitle}
                  onChange={(event) => setDraftTitle(event.target.value)}
                  placeholder="Page title"
                />
                <Input
                  value={draftSlug}
                  onChange={(event) => setDraftSlug(event.target.value)}
                  placeholder="slug"
                  disabled={selectedPageId !== newWikiPageId}
                />
              </div>
              <Textarea
                value={draftBody}
                onChange={(event) => setDraftBody(event.target.value)}
                placeholder="Stable project knowledge, decisions, conventions, and handoff notes."
                className="min-h-0 flex-1 resize-none font-mono text-sm"
              />
              <div className="mt-3 flex justify-end">
                <Button size="sm" onClick={handleSavePage} disabled={createWikiPage.isPending || updateWikiPage.isPending}>
                  <Check className="mr-1.5 h-3.5 w-3.5" />
                  Save page
                </Button>
              </div>
            </>
          ) : (
            <div className="min-h-0 flex-1 overflow-y-auto rounded-md border bg-background p-5">
              {draftBody.trim() ? (
                <Markdown mode="full">{draftBody}</Markdown>
              ) : (
                <div className="text-sm text-muted-foreground">
                  No content yet.
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ProjectDetail
// ---------------------------------------------------------------------------

export function ProjectDetail({ projectId }: { projectId: string }) {
  const { t } = useT("projects");
  const statusLabels = useProjectStatusLabels();
  const priorityLabels = useProjectPriorityLabels();
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const router = useNavigation();
  const userId = useAuthStore((s) => s.user?.id);
  const workspace = useCurrentWorkspace();
  const workspaceName = workspace?.name;
  const { data: project, isLoading } = useQuery(projectDetailOptions(wsId, projectId));
  const projectScope = `project:${projectId}`;
  const projectFilter = useMemo<MyIssuesFilter>(
    () => ({ project_id: projectId }),
    [projectId],
  );
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { getActorName } = useActorName();
  const updateProject = useUpdateProject();
  const deleteProject = useDeleteProject();
  const { data: pinnedItems = [] } = useQuery({
    ...pinListOptions(wsId, userId ?? ""),
    enabled: !!userId,
  });
  const isPinned = pinnedItems.some((p) => p.item_type === "project" && p.item_id === projectId);
  const createPin = useCreatePin();
  const deletePinMut = useDeletePin();
  const descEditorRef = useRef<ContentEditorRef>(null);
  const isMobile = useIsMobile();
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [iconPickerOpen, setIconPickerOpen] = useState(false);
  const [propertiesOpen, setPropertiesOpen] = useState(true);
  const [progressOpen, setProgressOpen] = useState(true);
  const [descriptionOpen, setDescriptionOpen] = useState(true);
  const [activeTab, setActiveTab] = useState<"issues" | "knowledge" | "visual">("issues");

  // Sidebar panel
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_project_detail_layout",
  });
  const sidebarRef = usePanelRef();
  // Desktop and mobile sidebar state must be separate. A single state defaulting
  // to `true` made the mobile <Sheet> mount in the open position on first render
  // (after `useIsMobile()` flipped from false→true), briefly covering the page
  // with its modal backdrop and locking scroll — leaving the page unresponsive.
  const [desktopSidebarOpen, setDesktopSidebarOpen] = useState(true);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const sidebarOpen = isMobile ? mobileSidebarOpen : desktopSidebarOpen;

  useEffect(() => {
    if (isMobile) {
      setMobileSidebarOpen(false);
    }
  }, [isMobile]);

  // Lead popover
  const [leadOpen, setLeadOpen] = useState(false);
  const [leadFilter, setLeadFilter] = useState("");
  const leadQuery = leadFilter.toLowerCase();
  const filteredMembers = members.filter((m) => m.name.toLowerCase().includes(leadQuery));
  const filteredAgents = agents.filter((a) => !a.archived_at && a.name.toLowerCase().includes(leadQuery));

  const handleUpdateField = useCallback(
    (data: Parameters<typeof updateProject.mutate>[0] extends { id: string } & infer R ? R : never) => {
      if (!project) return;
      updateProject.mutate({ id: project.id, ...data });
    },
    [project, updateProject],
  );

  const handleDelete = useCallback(() => {
    if (!project) return;
    deleteProject.mutate(project.id, {
      onSuccess: () => {
        toast.success(t(($) => $.detail.toast_project_deleted));
        router.push(wsPaths.projects());
      },
    });
  }, [project, deleteProject, router, wsPaths, t]);

  if (isLoading) {
    return (
      <div className="mx-auto w-full max-w-4xl px-8 py-10 space-y-4">
        <Skeleton className="h-5 w-32" />
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-96" />
        <Skeleton className="h-40 w-full mt-8" />
      </div>
    );
  }

  if (!project) {
    return <div className="flex items-center justify-center h-full text-muted-foreground">{t(($) => $.detail.not_found)}</div>;
  }

  const issueMetrics = getProjectIssueMetrics(project);
  const statusCfg = PROJECT_STATUS_CONFIG[project.status];

  const sidebarContent = (
    <div className="space-y-5">
      {/* Icon + Title */}
      <div>
        <Popover open={iconPickerOpen} onOpenChange={setIconPickerOpen}>
          <PopoverTrigger
            render={
              <button
                type="button"
                className="text-2xl cursor-pointer rounded-lg p-1 -ml-1 hover:bg-accent/60 transition-colors"
                title={t(($) => $.detail.icon_tooltip)}
              >
                {project.icon || "📁"}
              </button>
            }
          />
          <PopoverContent align="start" className="w-auto p-0">
            <EmojiPicker
              onSelect={(emoji) => {
                handleUpdateField({ icon: emoji });
                setIconPickerOpen(false);
              }}
            />
          </PopoverContent>
        </Popover>
        <TitleEditor
          key={`title-${projectId}`}
          defaultValue={project.title}
          placeholder={t(($) => $.detail.title_placeholder)}
          className="mt-2 w-full text-base font-semibold leading-snug tracking-tight"
          onBlur={(value) => {
            const trimmed = value.trim();
            if (trimmed && trimmed !== project.title) handleUpdateField({ title: trimmed });
          }}
        />
      </div>

      {/* Properties */}
      <div>
        <button
          className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${propertiesOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setPropertiesOpen(!propertiesOpen)}
        >
          {t(($) => $.detail.section_properties)}
          <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${propertiesOpen ? "rotate-90" : ""}`} />
        </button>
        {propertiesOpen && <div className="space-y-0.5 pl-2">
          <PropRow label={t(($) => $.table.status)}>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-xs hover:text-foreground transition-colors">
                    <span className={cn("size-2 rounded-full", statusCfg.dotColor)} />
                    <span>{statusLabels[project.status]}</span>
                  </button>
                }
              />
              <DropdownMenuContent align="start" className="w-44">
                {PROJECT_STATUS_ORDER.map((s) => (
                  <DropdownMenuItem key={s} onClick={() => handleUpdateField({ status: s as ProjectStatus })}>
                    <span className={cn("size-2 rounded-full", PROJECT_STATUS_CONFIG[s].dotColor)} />
                    <span>{statusLabels[s]}</span>
                    {s === project.status && <Check className="ml-auto h-3.5 w-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </PropRow>
          <PropRow label={t(($) => $.table.priority)}>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-xs hover:text-foreground transition-colors">
                    <PriorityIcon priority={project.priority} />
                    <span>{priorityLabels[project.priority]}</span>
                  </button>
                }
              />
              <DropdownMenuContent align="start" className="w-44">
                {PROJECT_PRIORITY_ORDER.map((p) => (
                  <DropdownMenuItem key={p} onClick={() => handleUpdateField({ priority: p as ProjectPriority })}>
                    <PriorityIcon priority={p} />
                    <span>{priorityLabels[p]}</span>
                    {p === project.priority && <Check className="ml-auto h-3.5 w-3.5" />}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </PropRow>
          <PropRow label={t(($) => $.table.lead)}>
            <Popover open={leadOpen} onOpenChange={(v) => { setLeadOpen(v); if (!v) setLeadFilter(""); }}>
              <PopoverTrigger
                render={
                  <button type="button" className="inline-flex items-center gap-1.5 text-xs hover:text-foreground transition-colors">
                    {project.lead_type && project.lead_id ? (
                      <>
                        <ActorAvatar actorType={project.lead_type} actorId={project.lead_id} size={16} enableHoverCard showStatusDot />
                        <span className="cursor-pointer">{getActorName(project.lead_type, project.lead_id)}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">{t(($) => $.lead.no_lead)}</span>
                    )}
                  </button>
                }
              />
              <PopoverContent align="start" className="w-52 p-0">
                <div className="px-2 py-1.5 border-b">
                  <input
                    type="text"
                    value={leadFilter}
                    onChange={(e) => setLeadFilter(e.target.value)}
                    placeholder={t(($) => $.lead.assign_placeholder)}
                    className="w-full bg-transparent text-sm placeholder:text-muted-foreground outline-none"
                  />
                </div>
                <div className="p-1 max-h-60 overflow-y-auto">
                  <button
                    type="button"
                    onClick={() => { handleUpdateField({ lead_type: null, lead_id: null }); setLeadOpen(false); }}
                    className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
                  >
                    <UserMinus className="h-3.5 w-3.5 text-muted-foreground" />
                    <span className="text-muted-foreground">{t(($) => $.lead.no_lead)}</span>
                  </button>
                  {filteredMembers.length > 0 && (
                    <>
                      <div className="px-2 pt-2 pb-1 text-xs font-medium text-muted-foreground uppercase tracking-wider">{t(($) => $.lead.members_group)}</div>
                      {filteredMembers.map((m) => (
                        <button
                          type="button"
                          key={m.user_id}
                          onClick={() => { handleUpdateField({ lead_type: "member", lead_id: m.user_id }); setLeadOpen(false); }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
                        >
                          <ActorAvatar actorType="member" actorId={m.user_id} size={16} />
                          <span>{m.name}</span>
                        </button>
                      ))}
                    </>
                  )}
                  {filteredAgents.length > 0 && (
                    <>
                      <div className="px-2 pt-2 pb-1 text-xs font-medium text-muted-foreground uppercase tracking-wider">{t(($) => $.lead.agents_group)}</div>
                      {filteredAgents.map((a) => (
                        <button
                          type="button"
                          key={a.id}
                          onClick={() => { handleUpdateField({ lead_type: "agent", lead_id: a.id }); setLeadOpen(false); }}
                          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent transition-colors"
                        >
                          <ActorAvatar actorType="agent" actorId={a.id} size={16} showStatusDot />
                          <span>{a.name}</span>
                        </button>
                      ))}
                    </>
                  )}
                  {filteredMembers.length === 0 && filteredAgents.length === 0 && leadFilter && (
                    <div className="px-2 py-3 text-center text-sm text-muted-foreground">{t(($) => $.lead.no_results)}</div>
                  )}
                </div>
              </PopoverContent>
            </Popover>
          </PropRow>
        </div>}
      </div>

      {/* Progress */}
      {issueMetrics.totalCount > 0 && (() => {
        const pct = Math.round((issueMetrics.completedCount / issueMetrics.totalCount) * 100);
        return (
          <div>
            <button
              className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${progressOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
              onClick={() => setProgressOpen(!progressOpen)}
            >
              {t(($) => $.detail.section_progress)}
              <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${progressOpen ? "rotate-90" : ""}`} />
            </button>
            {progressOpen && <div className="pl-2 flex items-center gap-3">
              <div className="relative h-2 flex-1 rounded-full bg-muted overflow-hidden">
                <div
                  className="absolute inset-y-0 left-0 rounded-full bg-emerald-500 transition-all"
                  style={{ width: `${pct}%` }}
                />
              </div>
              <span className="text-xs text-muted-foreground tabular-nums shrink-0">
                {issueMetrics.completedCount}/{issueMetrics.totalCount}
              </span>
            </div>}
          </div>
        );
      })()}

      {/* Description */}
      <div>
        <button
          className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${descriptionOpen ? "" : "text-muted-foreground hover:text-foreground"}`}
          onClick={() => setDescriptionOpen(!descriptionOpen)}
        >
          {t(($) => $.detail.section_description)}
          <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${descriptionOpen ? "rotate-90" : ""}`} />
        </button>
        {descriptionOpen && <div className="pl-2">
          <ContentEditor
            ref={descEditorRef}
            key={projectId}
            defaultValue={project.description || ""}
            placeholder={t(($) => $.detail.description_placeholder)}
            onUpdate={(md) => handleUpdateField({ description: md || null })}
            debounceMs={1500}
          />
        </div>}
      </div>

      {/* Resources */}
      <ProjectResourcesSection projectId={projectId} />
    </div>
  );

  return (
    <>
    <ResizablePanelGroup orientation="horizontal" className="flex-1 min-h-0" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
      <ResizablePanel id="content" minSize="50%">
        <div className="flex h-full flex-col">
          <PageHeader className="gap-2 bg-background text-sm">
            <div className="flex flex-1 items-center gap-1.5 min-w-0">
              <AppLink href={wsPaths.projects()} className="text-muted-foreground hover:text-foreground transition-colors shrink-0">
                {workspaceName ?? t(($) => $.detail.breadcrumb_fallback)}
              </AppLink>
              <ChevronRight className="h-3 w-3 text-muted-foreground/50 shrink-0" />
              <span className="truncate">{project.title}</span>
            </div>
            <div className="flex items-center gap-1 shrink-0">
              <Button
                variant="ghost"
                size="icon-sm"
                className={cn("text-muted-foreground", isPinned && "text-foreground")}
                title={isPinned ? t(($) => $.detail.unpin_tooltip) : t(($) => $.detail.pin_tooltip)}
                onClick={() => {
                  if (isPinned) {
                    deletePinMut.mutate({ itemType: "project", itemId: projectId });
                  } else {
                    createPin.mutate({ item_type: "project", item_id: projectId });
                  }
                }}
              >
                {isPinned ? <PinOff /> : <Pin />}
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button variant="ghost" size="icon-sm" className="text-muted-foreground">
                      <MoreHorizontal />
                    </Button>
                  }
                />
                <DropdownMenuContent align="end" className="w-auto">
                  <DropdownMenuItem onClick={() => {
                    navigator.clipboard.writeText(window.location.href);
                    toast.success(t(($) => $.detail.toast_link_copied));
                  }}>
                    <Link2 className="h-3.5 w-3.5" />
                    {t(($) => $.detail.copy_link)}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    variant="destructive"
                    onClick={() => setDeleteDialogOpen(true)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    {t(($) => $.detail.delete_action)}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant={sidebarOpen ? "secondary" : "ghost"}
                      size="icon-sm"
                      className={sidebarOpen ? "" : "text-muted-foreground"}
                      onClick={() => {
                        if (isMobile) {
                          setMobileSidebarOpen((open) => !open);
                        } else {
                          const panel = sidebarRef.current;
                          if (!panel) return;
                          if (panel.isCollapsed()) panel.expand();
                          else panel.collapse();
                        }
                      }}
                    >
                      <PanelRight />
                    </Button>
                  }
                />
                <TooltipContent side="bottom">{t(($) => $.detail.sidebar_tooltip)}</TooltipContent>
              </Tooltip>
            </div>
          </PageHeader>

          <div className="flex min-h-0 flex-1 flex-col">
            <div className="flex h-10 shrink-0 items-center gap-1 border-b px-3">
              <Button
                variant={activeTab === "issues" ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setActiveTab("issues")}
              >
                <ListTodo className="mr-1.5 h-3.5 w-3.5" />
                Issues
              </Button>
              <Button
                variant={activeTab === "knowledge" ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setActiveTab("knowledge")}
              >
                <BookOpen className="mr-1.5 h-3.5 w-3.5" />
                Knowledge
              </Button>
              <Button
                variant={activeTab === "visual" ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setActiveTab("visual")}
              >
                <ImageIcon className="mr-1.5 h-3.5 w-3.5" />
                Visual Board
              </Button>
            </div>
            {activeTab === "issues" ? (
              <ViewStoreProvider store={projectViewStore}>
                <ProjectIssuesSurface
                  projectId={projectId}
                  scope={projectScope}
                  filter={projectFilter}
                />
              </ViewStoreProvider>
            ) : activeTab === "knowledge" ? (
              <ProjectKnowledgeSurface projectId={projectId} />
            ) : (
              <ProjectVisualCanvas projectId={projectId} />
            )}
          </div>
          </div>
        </ResizablePanel>
        {!isMobile && <ResizableHandle />}
        {!isMobile && (
        <ResizablePanel
          id="sidebar"
          defaultSize={desktopSidebarOpen ? 320 : 0}
          minSize={260}
          maxSize={420}
          collapsible
          groupResizeBehavior="preserve-pixel-size"
          panelRef={sidebarRef}
          onResize={(size) => setDesktopSidebarOpen(size.inPixels > 0)}
        >
          <div className="overflow-y-auto border-l h-full">
            <div className="p-4">
              {sidebarContent}
            </div>
          </div>
        </ResizablePanel>
        )}
        {isMobile && (
          <Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>
            <SheetContent side="right" showCloseButton={false} className="w-[320px] overflow-y-auto p-4">
              {sidebarContent}
            </SheetContent>
          </Sheet>
        )}
      </ResizablePanelGroup>

      {/* Delete confirmation */}
      <AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.delete_dialog.title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.delete_dialog.description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.delete_dialog.cancel)}</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete} className="bg-destructive text-white hover:bg-destructive/90">
              {t(($) => $.delete_dialog.confirm)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
