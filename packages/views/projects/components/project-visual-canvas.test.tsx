import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Agent, ProjectVisualBoard, ProjectVisualNode } from "@multica/core/types";
import { ProjectVisualCanvas } from "./project-visual-canvas";

const visualMocks = vi.hoisted(() => ({
  board: null as ProjectVisualBoard | null,
  agents: [] as Agent[],
  generateNodesMutate: vi.fn(),
  generateImageMutate: vi.fn(),
  updateBoardMutate: vi.fn(),
  createPlanMutate: vi.fn(),
  toastMessage: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("@xyflow/react", () => ({
  Background: () => <div data-testid="flow-background" />,
  Controls: () => <div data-testid="flow-controls" />,
  Handle: () => null,
  Position: { Left: "left", Right: "right" },
  applyNodeChanges: (_changes: unknown, nodes: unknown) => nodes,
  ReactFlow: ({ nodes, nodeTypes }: any) => (
    <div data-testid="visual-flow">
      {nodes.map((node: any) => {
        const NodeComponent = nodeTypes[node.type];
        return (
          <NodeComponent
            key={node.id}
            id={node.id}
            type={node.type}
            data={node.data}
            selected={false}
            isConnectable={false}
            dragging={false}
            zIndex={0}
            xPos={node.position.x}
            yPos={node.position.y}
          />
        );
      })}
    </div>
  ),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/ui/components/common/theme-provider", () => ({
  useTheme: () => ({ resolvedTheme: "light" }),
}));

vi.mock("sonner", () => ({
  toast: {
    message: (...args: unknown[]) => visualMocks.toastMessage(...args),
    success: (...args: unknown[]) => visualMocks.toastSuccess(...args),
    error: (...args: unknown[]) => visualMocks.toastError(...args),
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "agents"],
    queryFn: async () => visualMocks.agents,
  }),
}));

vi.mock("@multica/core/project-visuals", () => ({
  projectVisualBoardOptions: (_wsId: string, projectId: string) => ({
    queryKey: ["project-visuals", "ws-1", projectId, "board"],
    queryFn: async () => visualMocks.board,
  }),
  useGenerateProjectVisualNodes: () => ({
    mutate: visualMocks.generateNodesMutate,
    isPending: false,
  }),
  useGenerateProjectVisualNodeImage: () => ({
    mutate: visualMocks.generateImageMutate,
    isPending: false,
  }),
  useUpdateProjectVisualBoard: () => ({
    mutate: visualMocks.updateBoardMutate,
    isPending: false,
  }),
  useCreatePlanFromProjectVisualBoard: () => ({
    mutate: visualMocks.createPlanMutate,
    isPending: false,
  }),
}));

function createQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function renderCanvas() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <ProjectVisualCanvas projectId="project-1" />
    </QueryClientProvider>,
  );
}

function makeNode(patch: Partial<ProjectVisualNode>): ProjectVisualNode {
  return {
    id: "node-1",
    board_id: "board-1",
    workspace_id: "ws-1",
    project_id: "project-1",
    type: "character",
    status: "draft",
    title: "Hero",
    description: "Hero description",
    prompt: "Hero prompt",
    position_x: 0,
    position_y: 0,
    source_refs: [],
    reference_attachment_ids: [],
    result_attachment_id: null,
    result_attachment: null,
    result_note: "",
    generation_agent_id: null,
    generation_task_id: null,
    generation_error: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...patch,
  };
}

function makeBoard(nodes: ProjectVisualNode[]): ProjectVisualBoard {
  return {
    id: "board-1",
    workspace_id: "ws-1",
    project_id: "project-1",
    viewport: {},
    metadata: {},
    nodes,
    edges: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function makeAgent(patch: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "Art Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "cloud",
    runtime_config: {},
    custom_env: {},
    custom_args: [],
    custom_env_redacted: false,
    visibility: "workspace",
    status: "idle",
    max_concurrent_tasks: 1,
    model: "",
    is_internal: false,
    owner_id: "user-1",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...patch,
  };
}

beforeEach(() => {
  visualMocks.generateNodesMutate.mockReset();
  visualMocks.generateImageMutate.mockReset();
  visualMocks.updateBoardMutate.mockReset();
  visualMocks.createPlanMutate.mockReset();
  visualMocks.toastMessage.mockReset();
  visualMocks.toastSuccess.mockReset();
  visualMocks.toastError.mockReset();
  visualMocks.agents = [makeAgent()];
  visualMocks.board = makeBoard([
    makeNode({
      id: "node-adopted",
      title: "Adopted Hero",
      status: "adopted",
      result_attachment_id: "att-1",
      result_attachment: {
        id: "att-1",
        workspace_id: "ws-1",
        issue_id: null,
        comment_id: null,
        chat_session_id: null,
        chat_message_id: null,
        uploader_type: "member",
        uploader_id: "user-1",
        filename: "hero.png",
        url: "https://cdn.example.test/hero.png",
        download_url: "https://cdn.example.test/hero.png",
        content_type: "image/png",
        size_bytes: 42,
        created_at: "2026-01-01T00:00:00Z",
      },
    }),
    makeNode({
      id: "node-draft",
      type: "scene",
      title: "Draft Forest",
      status: "draft",
    }),
    makeNode({
      id: "node-rejected",
      type: "prop",
      title: "Rejected Lantern",
      status: "rejected",
    }),
  ]);
});

describe("ProjectVisualCanvas", () => {
  it("renders visual node cards with generated preview images", async () => {
    const { container } = renderCanvas();

    expect(await screen.findByText("Adopted Hero")).toBeInTheDocument();
    expect(screen.getByText("Draft Forest")).toBeInTheDocument();
    expect(container.querySelector('img[src="https://cdn.example.test/hero.png"]')).not.toBeNull();
  });

  it("asks for an art agent before queuing a single-node image generation", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    fireEvent.click(screen.getByRole("button", { name: "Generate visual for Draft Forest" }));

    expect(visualMocks.toastMessage).toHaveBeenCalledWith("Select an art agent first.");
    expect(visualMocks.generateImageMutate).not.toHaveBeenCalled();
  });

  it("queues image generation after selecting an art agent", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    const artAgentSelect = screen.getByDisplayValue("Select agent");
    fireEvent.change(artAgentSelect, { target: { value: "agent-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Generate visual for Draft Forest" }));

    expect(visualMocks.generateImageMutate).toHaveBeenCalledWith(
      { nodeId: "node-draft", agent_id: "agent-1" },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("disables create-plan until adopted nodes and a planner are available", async () => {
    visualMocks.board = makeBoard([
      makeNode({ id: "node-draft", title: "Draft Forest", status: "draft" }),
      makeNode({ id: "node-rejected", title: "Rejected Lantern", status: "rejected" }),
    ]);
    renderCanvas();

    const createButton = await screen.findByRole("button", { name: /Create Plan from Adopted/i });
    expect(createButton).toBeDisabled();
  });

  it("creates a plan from adopted visual context after planner selection", async () => {
    renderCanvas();

    await screen.findByText("Adopted Hero");
    const plannerSelect = screen.getByDisplayValue("Planner agent");
    fireEvent.change(plannerSelect, { target: { value: "agent-1" } });
    fireEvent.change(screen.getByLabelText("Gameplay notes for Plan"), {
      target: { value: "Use adopted nodes only." },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create Plan from Adopted/i }));

    expect(visualMocks.createPlanMutate).toHaveBeenCalledWith(
      { planner_agent_id: "agent-1", gameplay_notes: "Use adopted nodes only." },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });
});
