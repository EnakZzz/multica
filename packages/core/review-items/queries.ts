import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { ReviewItemStatus, ReviewItemType } from "../types";

export const reviewItemKeys = {
  all: (wsId: string) => ["review-items", wsId] as const,
  list: (
    wsId: string,
    filters: { status?: ReviewItemStatus | "all"; type?: ReviewItemType | "all" } = {},
  ) => [...reviewItemKeys.all(wsId), "list", filters] as const,
};

export function reviewItemListOptions(
  wsId: string,
  filters: { status?: ReviewItemStatus | "all"; type?: ReviewItemType | "all" } = {},
) {
  return queryOptions({
    queryKey: reviewItemKeys.list(wsId, filters),
    queryFn: () => api.listReviewItems(filters),
  });
}
