import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectKnowledgeKeys = {
  all: (wsId: string) => ["project-knowledge", wsId] as const,
  project: (wsId: string, projectId: string) =>
    [...projectKnowledgeKeys.all(wsId), projectId] as const,
  wikiPages: (wsId: string, projectId: string) =>
    [...projectKnowledgeKeys.project(wsId, projectId), "wiki-pages"] as const,
  memoryItems: (wsId: string, projectId: string) =>
    [...projectKnowledgeKeys.project(wsId, projectId), "memory-items"] as const,
  search: (wsId: string, projectId: string, query: string) =>
    [...projectKnowledgeKeys.project(wsId, projectId), "search", query] as const,
  retrievalLogs: (wsId: string, projectId: string) =>
    [...projectKnowledgeKeys.project(wsId, projectId), "retrieval-logs"] as const,
  issueRelated: (wsId: string, issueId: string) =>
    [...projectKnowledgeKeys.all(wsId), "issue", issueId, "related"] as const,
  issueTrace: (wsId: string, issueId: string) =>
    [...projectKnowledgeKeys.all(wsId), "issue", issueId, "trace"] as const,
  taskRelated: (wsId: string, taskId: string) =>
    [...projectKnowledgeKeys.all(wsId), "task", taskId, "related"] as const,
  taskTrace: (wsId: string, taskId: string) =>
    [...projectKnowledgeKeys.all(wsId), "task", taskId, "trace"] as const,
};

export function projectWikiPagesOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.wikiPages(wsId, projectId),
    queryFn: () => api.listProjectWikiPages(projectId),
    enabled: Boolean(projectId),
    select: (data) => data.wiki_pages,
  });
}

export function projectMemoryItemsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.memoryItems(wsId, projectId),
    queryFn: () => api.listProjectMemoryItems(projectId),
    enabled: Boolean(projectId),
    select: (data) => data.memory_items,
  });
}

export function issueRelatedMemoryOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.issueRelated(wsId, issueId),
    queryFn: () => api.getIssueRelatedMemory(issueId, { limit: 5 }),
    enabled: Boolean(issueId),
  });
}

export function taskRelatedMemoryOptions(wsId: string, taskId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.taskRelated(wsId, taskId),
    queryFn: () => api.getTaskRelatedMemory(taskId, { limit: 5 }),
    enabled: Boolean(taskId),
  });
}

export function projectKnowledgeRetrievalLogsOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.retrievalLogs(wsId, projectId),
    queryFn: () => api.listProjectKnowledgeRetrievalLogs(projectId, { limit: 50 }),
    enabled: Boolean(projectId),
    select: (data) => data.retrieval_logs,
  });
}

export function issueKnowledgeTraceOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.issueTrace(wsId, issueId),
    queryFn: () => api.getIssueKnowledgeTrace(issueId, { limit: 10 }),
    enabled: Boolean(issueId),
  });
}

export function taskKnowledgeTraceOptions(wsId: string, taskId: string) {
  return queryOptions({
    queryKey: projectKnowledgeKeys.taskTrace(wsId, taskId),
    queryFn: () => api.getTaskKnowledgeTrace(taskId, { limit: 10 }),
    enabled: Boolean(taskId),
  });
}
