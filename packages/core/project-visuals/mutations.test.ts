import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import {
  invalidateProjectVisualBoardQueries,
  invalidateProjectVisualPlanQueries,
} from "./mutations";
import { projectVisualKeys } from "./queries";

describe("project visual mutation invalidation", () => {
  it("invalidates the current visual board after visual mutations settle", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateProjectVisualBoardQueries(qc, "ws-1", "project-1");

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: projectVisualKeys.board("ws-1", "project-1"),
    });
  });

  it("invalidates visual board and plans after creating a plan from adopted nodes", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateProjectVisualPlanQueries(qc, "ws-1", "project-1");

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: projectVisualKeys.board("ws-1", "project-1"),
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ["plans", "ws-1"],
    });
  });
});
