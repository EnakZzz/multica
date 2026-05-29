import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { issueKeys } from "../issues/queries";
import type {
  CreateProjectVisualNodeRequest,
  CreateProjectVisualPlanRequest,
  GenerateProjectVisualNodeRequest,
  UpdateProjectVisualBoardRequest,
} from "../types";
import { projectVisualKeys } from "./queries";

export function invalidateProjectVisualBoardQueries(
  qc: QueryClient,
  wsId: string,
  projectId: string,
) {
  qc.invalidateQueries({ queryKey: projectVisualKeys.board(wsId, projectId) });
}

export function invalidateProjectVisualNodeGenerationQueries(
  qc: QueryClient,
  wsId: string,
  projectId: string,
  nodeId: string,
) {
  qc.invalidateQueries({ queryKey: projectVisualKeys.nodeGenerations(wsId, projectId, nodeId) });
}

export function invalidateProjectVisualPlanQueries(
  qc: QueryClient,
  wsId: string,
  projectId: string,
) {
  invalidateProjectVisualBoardQueries(qc, wsId, projectId);
  qc.invalidateQueries({ queryKey: ["plans", wsId] });
}

export function useUpdateProjectVisualBoard(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: UpdateProjectVisualBoardRequest) =>
      api.updateProjectVisualBoard(projectId, data),
    onSettled: () => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
    },
  });
}

export function useCreateProjectVisualNode(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProjectVisualNodeRequest) =>
      api.createProjectVisualNode(projectId, data),
    onSettled: () => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
    },
  });
}

export function useDeleteProjectVisualNode(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (nodeId: string) => api.deleteProjectVisualNode(projectId, nodeId),
    onSettled: () => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
    },
  });
}

export function useClearProjectVisualBoard(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.clearProjectVisualBoard(projectId),
    onSettled: () => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
      qc.invalidateQueries({ queryKey: [...projectVisualKeys.all(wsId), projectId, "nodes"] });
    },
  });
}

export function useGenerateProjectVisualNodes(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.generateProjectVisualNodes(projectId),
    onSettled: () => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
      qc.invalidateQueries({ queryKey: issueKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.assigneeGroupsAll(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.myAssigneeGroupsAll(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.projectGanttAll(wsId) });
    },
  });
}

export function useGenerateProjectVisualNodeImage(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ nodeId, ...data }: { nodeId: string } & GenerateProjectVisualNodeRequest) =>
      api.generateProjectVisualNodeImage(projectId, nodeId, data),
    onSettled: (_data, _error, variables) => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
      invalidateProjectVisualNodeGenerationQueries(qc, wsId, projectId, variables.nodeId);
      qc.invalidateQueries({ queryKey: issueKeys.list(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.assigneeGroupsAll(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.myAssigneeGroupsAll(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.projectGanttAll(wsId) });
    },
  });
}

export function useRestoreProjectVisualNodeGeneration(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ nodeId, generationId }: { nodeId: string; generationId: string }) =>
      api.restoreProjectVisualNodeGeneration(projectId, nodeId, generationId),
    onSettled: (_data, _error, variables) => {
      invalidateProjectVisualBoardQueries(qc, wsId, projectId);
      invalidateProjectVisualNodeGenerationQueries(qc, wsId, projectId, variables.nodeId);
    },
  });
}

export function useCreatePlanFromProjectVisualBoard(projectId: string) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateProjectVisualPlanRequest) =>
      api.createPlanFromProjectVisualBoard(projectId, data),
    onSettled: () => {
      invalidateProjectVisualPlanQueries(qc, wsId, projectId);
    },
  });
}
