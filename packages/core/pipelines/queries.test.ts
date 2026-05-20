import { describe, expect, it } from "vitest";
import { pipelineKeys } from "./queries";

describe("pipeline query keys", () => {
  it("keeps list and detail under the workspace-scoped pipeline prefix", () => {
    expect(pipelineKeys.all("ws-1")).toEqual(["workspaces", "ws-1", "pipelines"]);
    expect(pipelineKeys.list("ws-1")).toEqual([
      "workspaces",
      "ws-1",
      "pipelines",
      "list",
    ]);
    expect(pipelineKeys.detail("ws-1", "pipe-1")).toEqual([
      "workspaces",
      "ws-1",
      "pipelines",
      "pipe-1",
    ]);
  });
});
