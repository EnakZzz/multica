import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const planKeys = {
  all: (wsId: string) => ["workspaces", wsId, "plans"] as const,
  list: (wsId: string) => [...planKeys.all(wsId), "list"] as const,
  detail: (wsId: string, planId: string) => [...planKeys.all(wsId), planId] as const,
};

export function planListOptions(wsId: string) {
  return queryOptions({
    queryKey: planKeys.list(wsId),
    queryFn: () => api.listPlans(),
  });
}

export function planDetailOptions(wsId: string, planId: string) {
  return queryOptions({
    queryKey: planKeys.detail(wsId, planId),
    queryFn: () => api.getPlan(planId),
    enabled: !!wsId && !!planId,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "planning" ? 2000 : false;
    },
  });
}
