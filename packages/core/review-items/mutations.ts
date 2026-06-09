import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { reviewItemKeys } from "./queries";
import type { ReviewItemActionRequest } from "../types";

export function useReviewItemAction() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: ({ id, ...data }: ReviewItemActionRequest & { id: string }) =>
      api.actOnReviewItem(id, data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: reviewItemKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: ["workspace", wsId] });
      qc.invalidateQueries({ queryKey: ["plans", wsId] });
      qc.invalidateQueries({ queryKey: ["issues", wsId] });
    },
  });
}
