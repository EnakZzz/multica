import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const projectVisualKeys = {
  all: (wsId: string) => ["project-visuals", wsId] as const,
  board: (wsId: string, projectId: string) =>
    [...projectVisualKeys.all(wsId), projectId, "board"] as const,
};

export function projectVisualBoardOptions(wsId: string, projectId: string) {
  return queryOptions({
    queryKey: projectVisualKeys.board(wsId, projectId),
    queryFn: () => api.getProjectVisualBoard(projectId),
    enabled: Boolean(projectId),
  });
}
