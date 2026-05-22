import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import type {
  CreateProjectMemoryItemRequest,
  CreateProjectWikiPageRequest,
  UpdateProjectKnowledgeRetrievalLogFeedbackRequest,
  UpdateProjectMemoryItemRequest,
  UpdateProjectWikiPageRequest,
} from "../types";
import { projectKnowledgeKeys } from "./queries";

export function useCreateProjectWikiPage(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProjectWikiPageRequest) =>
      api.createProjectWikiPage(projectId, data),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.wikiPages(wsId, projectId),
      });
    },
  });
}

export function useUpdateProjectWikiPage(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      pageId,
      ...data
    }: { pageId: string } & UpdateProjectWikiPageRequest) =>
      api.updateProjectWikiPage(projectId, pageId, data),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.wikiPages(wsId, projectId),
      });
    },
  });
}

export function useDeleteProjectWikiPage(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (pageId: string) => api.deleteProjectWikiPage(projectId, pageId),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.wikiPages(wsId, projectId),
      });
    },
  });
}

export function useCreateProjectMemoryItem(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProjectMemoryItemRequest) =>
      api.createProjectMemoryItem(projectId, data),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.memoryItems(wsId, projectId),
      });
    },
  });
}

export function useUpdateProjectMemoryItem(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      memoryItemId,
      ...data
    }: { memoryItemId: string } & UpdateProjectMemoryItemRequest) =>
      api.updateProjectMemoryItem(projectId, memoryItemId, data),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.memoryItems(wsId, projectId),
      });
    },
  });
}

export function useDeleteProjectMemoryItem(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (memoryItemId: string) =>
      api.deleteProjectMemoryItem(projectId, memoryItemId),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.memoryItems(wsId, projectId),
      });
    },
  });
}

export function useUpdateProjectKnowledgeRetrievalLogFeedback(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      logId,
      ...data
    }: { logId: string } & UpdateProjectKnowledgeRetrievalLogFeedbackRequest) =>
      api.updateProjectKnowledgeRetrievalLogFeedback(projectId, logId, data),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: projectKnowledgeKeys.retrievalLogs(wsId, projectId),
      });
    },
  });
}
