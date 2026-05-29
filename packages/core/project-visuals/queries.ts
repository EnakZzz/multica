import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectVisualKeys = {
  all: (wsId: string) => ["project-visuals", wsId] as const,
  board: (wsId: string, projectId: string) =>
    [...projectVisualKeys.all(wsId), projectId, "board"] as const,
  nodeGenerations: (wsId: string, projectId: string, nodeId: string) =>
    [...projectVisualKeys.all(wsId), projectId, "nodes", nodeId, "generations"] as const,
};

export function projectVisualBoardOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectVisualKeys.board(wsId, projectId),
    queryFn: () => api.getProjectVisualBoard(projectId),
    enabled: Boolean(projectId),
  });
}

export function projectVisualNodeGenerationsOptions(wsId: string, projectId: string, nodeId: string) {
  return queryOptions({
    queryKey: projectVisualKeys.nodeGenerations(wsId, projectId, nodeId),
    queryFn: () => api.listProjectVisualNodeGenerations(projectId, nodeId),
    enabled: Boolean(projectId && nodeId),
  });
}
