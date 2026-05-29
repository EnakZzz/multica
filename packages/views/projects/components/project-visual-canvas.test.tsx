import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Agent, ProjectVisualBoard, ProjectVisualNode, ProjectVisualNodeGeneration } from "@multica/core/types";
import { ProjectVisualCanvas } from "./project-visual-canvas";

const visualMocks = vi.hoisted(() => ({
  board: null as ProjectVisualBoard | null,
  generations: [] as ProjectVisualNodeGeneration[],
  agents: [] as Agent[],
  generateNodesMutate: vi.fn(),
  generateImageMutate: vi.fn(),
  createNodeMutate: vi.fn(),
  deleteNodeMutate: vi.fn(),
  clearBoardMutate: vi.fn(),
  restoreGenerationMutate: vi.fn(),
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
  ReactFlow: ({ nodes, nodeTypes, onPaneContextMenu, onConnect, onConnectStart, onConnectEnd }: any) => (
    <div data-testid="visual-flow" onContextMenu={onPaneContextMenu}>
      <button
        type="button"
        data-testid="reference-connect"
        onClick={() => onConnect?.({ source: "node-reference", target: "node-draft" })}
      />
      <button
        type="button"
        data-testid="connection-drop"
        onClick={() => {
          onConnectStart?.(new MouseEvent("mousedown"), { nodeId: "node-draft", handleId: null, handleType: "source" });
          onConnectEnd?.(new MouseEvent("mouseup", { clientX: 400, clientY: 320 }), {
            isValid: false,
            fromNode: { id: "node-draft" },
          });
        }}
      />
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

vi.mock("@multica/ui/components/ui/context-menu", () => ({
  ContextMenu: ({ children }: any) => <div>{children}</div>,
  ContextMenuTrigger: ({ children, render }: any) => {
    if (render) {
      const Component = render.type;
      return <Component {...render.props}>{children}</Component>;
    }
    return <div>{children}</div>;
  },
  ContextMenuContent: ({ children }: any) => <div role="menu">{children}</div>,
  ContextMenuItem: ({ children, onClick }: any) => (
    <button type="button" role="menuitem" onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: any) => <>{children}</>,
  TooltipTrigger: ({ children, render }: any) => {
    if (render) {
      const Component = render.type;
      return <Component {...render.props}>{children}</Component>;
    }
    return <>{children}</>;
  },
  TooltipContent: ({ children }: any) => <div role="tooltip">{children}</div>,
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
  projectVisualNodeGenerationsOptions: (_wsId: string, projectId: string, nodeId: string) => ({
    queryKey: ["project-visuals", "ws-1", projectId, "nodes", nodeId, "generations"],
    queryFn: async () => ({ generations: visualMocks.generations }),
    enabled: Boolean(nodeId),
  }),
  useGenerateProjectVisualNodes: () => ({
    mutate: visualMocks.generateNodesMutate,
    isPending: false,
  }),
  useGenerateProjectVisualNodeImage: () => ({
    mutate: visualMocks.generateImageMutate,
    isPending: false,
  }),
  useCreateProjectVisualNode: () => ({
    mutate: visualMocks.createNodeMutate,
    isPending: false,
  }),
  useDeleteProjectVisualNode: () => ({
    mutate: visualMocks.deleteNodeMutate,
    isPending: false,
  }),
  useClearProjectVisualBoard: () => ({
    mutate: visualMocks.clearBoardMutate,
    isPending: false,
  }),
  useRestoreProjectVisualNodeGeneration: () => ({
    mutate: visualMocks.restoreGenerationMutate,
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
    title_zh: "",
    description: "Hero description",
    description_zh: "",
    prompt: "Hero prompt",
    prompt_zh: "",
    position_x: 0,
    position_y: 0,
    source_refs: [],
    reference_attachment_ids: [],
    result_attachment_id: null,
    result_attachment: null,
    result_note: "",
    result_note_zh: "",
    generation_agent_id: null,
    generation_task_id: null,
    generation_error: "",
    generation_error_zh: "",
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

function makeGeneration(patch: Partial<ProjectVisualNodeGeneration> = {}): ProjectVisualNodeGeneration {
  return {
    id: "generation-1",
    task_id: "task-1",
    task_status: "completed",
    issue_id: "issue-1",
    issue_identifier: "LOC-42",
    issue_title: "Generate visual asset: Hero",
    issue_status: "done",
    attachment_id: "att-1",
    attachment: {
      id: "att-1",
      workspace_id: "ws-1",
      issue_id: "issue-1",
      comment_id: null,
      chat_session_id: null,
      chat_message_id: null,
      uploader_type: "agent",
      uploader_id: "agent-1",
      filename: "hero.png",
      url: "https://cdn.example.test/hero.png",
      download_url: "https://cdn.example.test/hero.png",
      content_type: "image/png",
      size_bytes: 42,
      created_at: "2026-01-01T00:00:00Z",
    },
    note: "First result",
    note_zh: "第一版结果",
    error: "",
    error_zh: "",
    is_current: false,
    created_at: "2026-01-01T00:00:00Z",
    completed_at: "2026-01-01T00:10:00Z",
    ...patch,
  };
}

function makeAgent(patch: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "Art Agent",
    display_name: "Art Agent",
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
    builtin_key: null,
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
  visualMocks.createNodeMutate.mockReset();
  visualMocks.deleteNodeMutate.mockReset();
  visualMocks.clearBoardMutate.mockReset();
  visualMocks.restoreGenerationMutate.mockReset();
  visualMocks.updateBoardMutate.mockReset();
  visualMocks.createPlanMutate.mockReset();
  visualMocks.toastMessage.mockReset();
  visualMocks.toastSuccess.mockReset();
  visualMocks.toastError.mockReset();
  visualMocks.agents = [makeAgent()];
  visualMocks.generations = [];
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
    fireEvent.click(screen.getAllByRole("button", { name: "生成图片资源" })[1]!);

    expect(visualMocks.toastMessage).toHaveBeenCalledWith("Select an art agent first.");
    expect(visualMocks.generateImageMutate).not.toHaveBeenCalled();
  });

  it("queues image generation after selecting an art agent", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    const artAgentSelect = screen.getByDisplayValue("Select agent");
    fireEvent.change(artAgentSelect, { target: { value: "agent-1" } });
    fireEvent.click(screen.getAllByRole("button", { name: "生成图片资源" })[1]!);

    expect(visualMocks.generateImageMutate).toHaveBeenCalledWith(
      { nodeId: "node-draft", agent_id: "agent-1" },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("opens node creation from the canvas context menu", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    fireEvent.contextMenu(screen.getByTestId("visual-flow"), { clientX: 240, clientY: 180 });
    fireEvent.click(await screen.findByRole("button", { name: "Create" }));

    expect(visualMocks.createNodeMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "character",
        title: "New Character",
        source_node_id: undefined,
        prompt: expect.stringContaining("game-asset-pipeline"),
      }),
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("opens node creation from an edge dragged to empty canvas", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    fireEvent.click(screen.getByTestId("connection-drop"));
    fireEvent.change(await screen.findByLabelText("Type"), { target: { value: "animation" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(visualMocks.createNodeMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "animation",
        title: "Draft Forest animation",
        source_node_id: "node-draft",
        relation: "variant_of",
        prompt: expect.stringContaining("game-asset-pipeline"),
      }),
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
    expect(visualMocks.createNodeMutate.mock.calls[0]?.[0]?.prompt).toContain("transparent spritesheet");
    expect(visualMocks.createNodeMutate.mock.calls[0]?.[0]?.prompt).toContain("flat removable chroma-key background");
  });

  it("deletes a node from its context menu", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    fireEvent.click(screen.getAllByRole("menuitem", { name: /删除节点/i })[1]!);

    expect(visualMocks.deleteNodeMutate).toHaveBeenCalledWith(
      "node-draft",
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("clears the visual board after confirmation", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");
    fireEvent.click(screen.getByRole("button", { name: /清空画板/i }));

    expect(await screen.findByText("清空 Visual Board？")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "确认清空" }));

    expect(visualMocks.clearBoardMutate).toHaveBeenCalledWith(
      undefined,
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("shows Chinese tooltips for node icon buttons", async () => {
    renderCanvas();

    await screen.findByText("Draft Forest");

    expect(screen.getAllByRole("button", { name: "采纳节点" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "拒绝节点" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("button", { name: "生成图片资源" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("tooltip", { name: "采纳节点" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("tooltip", { name: "拒绝节点" }).length).toBeGreaterThan(0);
    expect(screen.getAllByRole("tooltip", { name: "生成图片资源" }).length).toBeGreaterThan(0);
  });

  it("renders reference nodes without image preview or image generation action", async () => {
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-reference",
        type: "reference",
        title: "Mood Reference",
        result_attachment_id: "att-ref",
        result_attachment: {
          id: "att-ref",
          workspace_id: "ws-1",
          issue_id: null,
          comment_id: null,
          chat_session_id: null,
          chat_message_id: null,
          uploader_type: "member",
          uploader_id: "user-1",
          filename: "reference.png",
          url: "https://cdn.example.test/reference.png",
          download_url: "https://cdn.example.test/reference.png",
          content_type: "image/png",
          size_bytes: 42,
          created_at: "2026-01-01T00:00:00Z",
        },
      }),
    ]);
    const { container } = renderCanvas();

    expect(await screen.findByText("Mood Reference")).toBeInTheDocument();
    expect(await screen.findByText("Reference Library")).toBeInTheDocument();
    expect(container.querySelector('img[src="https://cdn.example.test/reference.png"]')).toBeNull();
    expect(screen.queryByRole("button", { name: "生成图片资源" })).not.toBeInTheDocument();
  });

  it("links a reference node to an asset node as a persisted reference edge", async () => {
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-reference",
        type: "reference",
        title: "Mood Reference",
      }),
      makeNode({
        id: "node-draft",
        type: "scene",
        title: "Draft Forest",
      }),
    ]);
    renderCanvas();

    await screen.findByText("Mood Reference");
    fireEvent.click(screen.getByTestId("reference-connect"));

    expect(visualMocks.updateBoardMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        edges: expect.arrayContaining([
          expect.objectContaining({
            source_node_id: "node-reference",
            target_node_id: "node-draft",
            relation: "reference",
          }),
        ]),
      }),
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("shows Chinese display fields in the detail panel", async () => {
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-zh",
        title: "Hero",
        title_zh: "主角",
        description: "English visual description",
        description_zh: "中文视觉描述",
        prompt: "English generation prompt",
        prompt_zh: "中文生成提示词",
        result_note: "English result note",
        result_note_zh: "中文生成结果",
        generation_error: "English error",
        generation_error_zh: "中文错误信息",
      }),
    ]);
    renderCanvas();

    fireEvent.click(await screen.findByText("主角"));

    expect(screen.getByText("基础信息")).toBeInTheDocument();
    expect(screen.getByText("视觉描述")).toBeInTheDocument();
    expect(screen.getByText("生成提示词")).toBeInTheDocument();
    expect(screen.getByText("生成结果")).toBeInTheDocument();
    expect(screen.getByText("错误信息")).toBeInTheDocument();
    expect(screen.getByText("来源信息")).toBeInTheDocument();
    expect(screen.getByText("中文视觉描述")).toBeInTheDocument();
    expect(screen.getAllByText("中文生成提示词").length).toBeGreaterThan(0);
    expect(screen.getByText("中文生成结果")).toBeInTheDocument();
    expect(screen.getByText("中文错误信息")).toBeInTheDocument();
  });

  it("shows past generation issues in the detail panel", async () => {
    visualMocks.generations = [
      makeGeneration({
        id: "generation-current",
        issue_identifier: "LOC-42",
        note_zh: "当前版本",
        is_current: true,
      }),
      makeGeneration({
        id: "generation-old",
        task_id: "task-old",
        issue_id: "issue-old",
        issue_identifier: "LOC-21",
        issue_title: "Generate visual asset: old Hero",
        note_zh: "上一版结果",
        attachment_id: "att-old",
        attachment: {
          ...makeGeneration().attachment!,
          id: "att-old",
          download_url: "https://cdn.example.test/old-hero.png",
        },
      }),
    ];
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-history",
        title: "History Hero",
      }),
    ]);
    renderCanvas();

    fireEvent.click(await screen.findByText("History Hero"));

    expect(await screen.findByText("过往生成 issue")).toBeInTheDocument();
    expect(await screen.findByText("LOC-42")).toBeInTheDocument();
    expect(await screen.findByText("LOC-21")).toBeInTheDocument();
    expect(screen.getByText("当前版本")).toBeInTheDocument();
    expect(screen.getByText("上一版结果")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /回滚到此版本/i })[0]).toBeDisabled();
  });

  it("restores a previous generation result", async () => {
    visualMocks.generations = [
      makeGeneration({
        id: "generation-old",
        issue_identifier: "LOC-21",
        attachment_id: "att-old",
      }),
    ];
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-history",
        title: "History Hero",
      }),
    ]);
    renderCanvas();

    fireEvent.click(await screen.findByText("History Hero"));
    fireEvent.click(await screen.findByRole("button", { name: /回滚到此版本/i }));

    expect(visualMocks.restoreGenerationMutate).toHaveBeenCalledWith(
      { nodeId: "node-history", generationId: "generation-old" },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("falls back to original fields when Chinese display fields are missing", async () => {
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-en",
        title: "Fallback Hero",
        description: "English fallback description",
        prompt: "English fallback prompt",
      }),
    ]);
    renderCanvas();

    fireEvent.click(await screen.findByText("Fallback Hero"));

    expect(screen.getAllByText("English fallback description").length).toBeGreaterThan(0);
    expect(screen.getAllByText("English fallback prompt").length).toBeGreaterThan(0);
  });

  it("opens original agent text from the detail context menu", async () => {
    visualMocks.board = makeBoard([
      makeNode({
        id: "node-original",
        title: "Hero",
        title_zh: "主角",
        description: "English visual description",
        description_zh: "中文视觉描述",
        prompt: "English generation prompt",
        prompt_zh: "中文生成提示词",
        source_refs: [{ wiki_slug: "visual-brief" }],
      }),
    ]);
    renderCanvas();

    fireEvent.click(await screen.findByText("主角"));
    fireEvent.click(screen.getByRole("menuitem", { name: /查看智能体原文/i }));

    expect(await screen.findByText("智能体原文")).toBeInTheDocument();
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("English visual description")).toBeInTheDocument();
    expect(within(dialog).getByText("English generation prompt")).toBeInTheDocument();
    expect(within(dialog).getByText(/visual-brief/)).toBeInTheDocument();
  });

  it("hides internal planner agents from the art agent selector", async () => {
    visualMocks.agents = [
      makeAgent(),
      makeAgent({ id: "planner-1", name: "规划Agent", is_internal: true }),
    ];
    renderCanvas();

    await screen.findByRole("option", { name: "Art Agent" });
    expect(screen.queryByRole("option", { name: "规划Agent" })).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue("Planner agent")).not.toBeInTheDocument();
  });

  it("disables create-plan modes until adopted nodes are available", async () => {
    visualMocks.board = makeBoard([
      makeNode({ id: "node-draft", title: "Draft Forest", status: "draft" }),
      makeNode({ id: "node-rejected", title: "Rejected Lantern", status: "rejected" }),
    ]);
    renderCanvas();

    const prototypeButton = await screen.findByRole("button", { name: /Create Playable Prototype Plan/i });
    const integrationButton = await screen.findByRole("button", { name: /Create Production Asset Integration Plan/i });
    expect(prototypeButton).toBeDisabled();
    expect(integrationButton).toBeDisabled();
  });

  it("creates a playable prototype plan from adopted visual context", async () => {
    renderCanvas();

    await screen.findByText("Adopted Hero");
    fireEvent.change(screen.getByLabelText("Gameplay notes for Plan"), {
      target: { value: "Use adopted nodes only." },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create Playable Prototype Plan/i }));

    expect(visualMocks.createPlanMutate).toHaveBeenCalledWith(
      { gameplay_notes: "Use adopted nodes only.", plan_mode: "playable_prototype" },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("creates a production asset integration plan from adopted visual context", async () => {
    renderCanvas();

    await screen.findByText("Adopted Hero");
    fireEvent.change(screen.getByLabelText("Gameplay notes for Plan"), {
      target: { value: "Replace placeholders with selected board assets." },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create Production Asset Integration Plan/i }));

    expect(visualMocks.createPlanMutate).toHaveBeenCalledWith(
      { gameplay_notes: "Replace placeholders with selected board assets.", plan_mode: "production_asset_integration" },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });
});
