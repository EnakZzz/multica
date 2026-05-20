import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const pipelineKeys = {
  all: (wsId: string) => ["workspaces", wsId, "pipelines"] as const,
  list: (wsId: string) => [...pipelineKeys.all(wsId), "list"] as const,
  detail: (wsId: string, pipelineId: string) =>
    [...pipelineKeys.all(wsId), pipelineId] as const,
};

export function pipelineListOptions(wsId: string) {
  return queryOptions({
    queryKey: pipelineKeys.list(wsId),
    queryFn: () => api.listPipelines(),
  });
}

export function pipelineDetailOptions(wsId: string, pipelineId: string) {
  return queryOptions({
    queryKey: pipelineKeys.detail(wsId, pipelineId),
    queryFn: () => api.getPipeline(pipelineId),
    enabled: !!wsId && !!pipelineId,
  });
}
