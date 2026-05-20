import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { PipelineImportDialog } from "./pipeline-import-dialog";

const validateYaml = vi.hoisted(() => vi.fn());
const importYaml = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/pipelines/mutations", () => ({
  useValidatePipelineYamlImport: () => ({
    mutateAsync: validateYaml,
    isPending: false,
  }),
  useImportPipelineYaml: () => ({
    mutateAsync: importYaml,
    isPending: false,
  }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: toastSuccess,
    error: toastError,
  },
}));

describe("PipelineImportDialog", () => {
  beforeEach(() => {
    validateYaml.mockReset();
    importYaml.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("reads YAML from a selected local file before validating or importing", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const onImported = vi.fn();
    const content = "version: 1\nname: Local Flow\nnodes: []\n";
    validateYaml.mockResolvedValue({
      valid: true,
      errors: [],
      pipeline: {
        name: "Local Flow",
        description: "",
        nodes: [],
      },
    });
    const pipeline = {
      id: "pipeline-1",
      name: "Local Flow",
      description: "",
      nodes: [],
      runs: [],
      created_at: "",
      updated_at: "",
      archived_at: null,
    };
    importYaml.mockResolvedValue(pipeline);

    render(
      <PipelineImportDialog
        open
        onOpenChange={onOpenChange}
        onImported={onImported}
      />,
    );

    const file = new File([content], "local-flow.yaml", { type: "text/yaml" });
    await user.upload(screen.getByLabelText("Choose YAML file"), file);

    expect(screen.getByText("local-flow.yaml")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Validate" }));
    expect(validateYaml).toHaveBeenCalledWith({
      content,
      pipeline_id: null,
    });

    await user.click(screen.getByRole("button", { name: /Import/ }));
    expect(importYaml).toHaveBeenCalledWith({
      content,
      pipeline_id: null,
    });
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(onImported).toHaveBeenCalledWith(pipeline);
  });
});
