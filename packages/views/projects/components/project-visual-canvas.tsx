"use client";

import { useCallback, useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import {
  Background,
  Controls,
  type Connection,
  type FinalConnectionState,
  Handle,
  Position,
  ReactFlow,
  applyNodeChanges,
  type Node,
  type NodeChange,
  type NodeProps,
  type ReactFlowInstance,
  type XYPosition,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Check, FileText, Film, Image, Loader2, RotateCcw, Sparkles, Trash2, Wand2, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Input } from "@multica/ui/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@multica/ui/components/ui/context-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import type {
  Agent,
  ProjectVisualNode,
  ProjectVisualNodeGeneration,
  ProjectVisualNodeStatus,
  ProjectVisualNodeType,
  ProjectVisualPlanMode,
} from "@multica/core/types";
import { projectVisualBoardOptions, projectVisualNodeGenerationsOptions } from "@multica/core/project-visuals";
import {
  useClearProjectVisualBoard,
  useCreateProjectVisualNode,
  useDeleteProjectVisualNode,
  useCreatePlanFromProjectVisualBoard,
  useGenerateProjectVisualNodeImage,
  useGenerateProjectVisualNodes,
  useRestoreProjectVisualNodeGeneration,
  useUpdateProjectVisualBoard,
} from "@multica/core/project-visuals";

type VisualFlowData = {
  node: ProjectVisualNode;
  onSelect: (node: ProjectVisualNode) => void;
  onStatus: (node: ProjectVisualNode, status: ProjectVisualNodeStatus) => void;
  onGenerate: (node: ProjectVisualNode) => void;
  onDelete: (node: ProjectVisualNode) => void;
};

type VisualFlowNode = Node<VisualFlowData, "visualNode">;
type ReferenceGroupData = {
  count: number;
};
type ReferenceGroupNode = Node<ReferenceGroupData, "referenceGroup">;
type VisualCanvasNode = VisualFlowNode | ReferenceGroupNode;

type NodeCreationDraft = {
  position: XYPosition;
  sourceNodeId: string;
  type: ProjectVisualNodeType;
  title: string;
  description: string;
  prompt: string;
  relation: string;
};

const REFERENCE_GROUP_ID = "__reference-library";
const NODE_CARD_WIDTH = 256;
const NODE_CARD_HEIGHT = 210;
const REFERENCE_GROUP_PADDING = 24;

const NODE_TYPES = { visualNode: VisualNodeCard, referenceGroup: ReferenceGroupNodeCard };

const TYPE_LABELS: Record<ProjectVisualNodeType, string> = {
  character: "Character",
  scene: "Scene",
  ui_element: "UI",
  prop: "Prop",
  reference: "Reference",
  gameplay_note: "Gameplay",
  generated_variant: "Variant",
  animation: "Animation",
};

export function ProjectVisualCanvas({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { resolvedTheme } = useTheme();
  const { data: board } = useQuery(projectVisualBoardOptions(wsId, projectId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const updateBoard = useUpdateProjectVisualBoard(projectId);
  const clearBoard = useClearProjectVisualBoard(projectId);
  const createNode = useCreateProjectVisualNode(projectId);
  const deleteNode = useDeleteProjectVisualNode(projectId);
  const generateNodes = useGenerateProjectVisualNodes(projectId);
  const generateImage = useGenerateProjectVisualNodeImage(projectId);
  const restoreGeneration = useRestoreProjectVisualNodeGeneration(projectId);
  const createPlan = useCreatePlanFromProjectVisualBoard(projectId);
  const [flowNodes, setFlowNodes] = useState<VisualCanvasNode[]>([]);
  const [reactFlow, setReactFlow] = useState<ReactFlowInstance<VisualCanvasNode> | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [selectedAgentId, setSelectedAgentId] = useState("");
  const [gameplayNotes, setGameplayNotes] = useState("");
  const [connectingSourceId, setConnectingSourceId] = useState("");
  const [nodeDraft, setNodeDraft] = useState<NodeCreationDraft | null>(null);
  const [originalNode, setOriginalNode] = useState<ProjectVisualNode | null>(null);
  const [clearDialogOpen, setClearDialogOpen] = useState(false);

  const artAgents = useMemo(
    () => agents.filter((agent) => !agent.archived_at && agent.runtime_id && !agent.is_internal),
    [agents],
  );
  const selectedArtAgentId = useMemo(
    () => artAgents.some((agent) => agent.id === selectedAgentId) ? selectedAgentId : "",
    [artAgents, selectedAgentId],
  );

  const selectedNode = useMemo(
    () => board?.nodes.find((node) => node.id === selectedId) ?? null,
    [board?.nodes, selectedId],
  );
  const { data: generationHistory, isLoading: generationHistoryLoading } = useQuery(
    projectVisualNodeGenerationsOptions(wsId, projectId, selectedNode?.id ?? ""),
  );
  const selectedGenerations = generationHistory?.generations ?? [];

  const syncBoardToFlow = useCallback(() => {
    if (!board) return;
    setFlowNodes((prev) => {
      const prevPositions = new Map(prev.filter(isVisualFlowNode).map((node) => [node.id, node.position]));
      const visualNodes: VisualFlowNode[] = board.nodes.map((node) => ({
        id: node.id,
        type: "visualNode",
        position: prevPositions.get(node.id) ?? { x: node.position_x, y: node.position_y },
        data: {
          node,
          onSelect: setSelectedFromNode,
          onStatus: handleStatusChange,
          onGenerate: handleGenerateRequest,
          onDelete: handleDeleteNode,
        },
      }));
      const groupNode = buildReferenceGroupNode(visualNodes);
      return groupNode ? [groupNode, ...visualNodes] : visualNodes;
    });
  }, [board, selectedArtAgentId]);

  useEffect(() => {
    syncBoardToFlow();
  }, [syncBoardToFlow]);

  const setSelectedFromNode = useCallback((node: ProjectVisualNode) => {
    setSelectedId(node.id);
    setSelectedAgentId((current) => node.generation_agent_id ?? current);
  }, []);

  const saveBoard = useCallback((nodesOverride?: VisualCanvasNode[]) => {
    if (!board) return;
    const latest = (nodesOverride ?? flowNodes).filter(isVisualFlowNode);
    const positionById = new Map(latest.map((node) => [node.id, node.position]));
    updateBoard.mutate({
      viewport: board.viewport,
      metadata: board.metadata,
      nodes: board.nodes.map((node) => {
        const position = positionById.get(node.id);
        return {
          id: node.id,
          type: node.type,
          status: node.status,
          title: node.title,
          title_zh: node.title_zh,
          description: node.description,
          description_zh: node.description_zh,
          prompt: node.prompt,
          prompt_zh: node.prompt_zh,
          position_x: position?.x ?? node.position_x,
          position_y: position?.y ?? node.position_y,
          source_refs: node.source_refs,
        };
      }),
      edges: board.edges.map((edge) => ({
        id: edge.id,
        source_node_id: edge.source_node_id,
        target_node_id: edge.target_node_id,
        relation: edge.relation,
      })),
    });
  }, [board, flowNodes, updateBoard]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setFlowNodes((prev) => applyNodeChanges(changes, prev) as VisualCanvasNode[]);
  }, []);

  const flowPositionFromEvent = useCallback((event: MouseEvent | TouchEvent | React.MouseEvent): XYPosition => {
    const point = clientPointFromEvent(event);
    if (!point || !reactFlow) return { x: 0, y: 0 };
    return reactFlow.screenToFlowPosition(point);
  }, [reactFlow]);

  function handleStatusChange(node: ProjectVisualNode, status: ProjectVisualNodeStatus) {
    if (!board) return;
    updateBoard.mutate({
      viewport: board.viewport,
      metadata: board.metadata,
      nodes: board.nodes.map((item) => ({
        id: item.id,
        type: item.type,
        status: item.id === node.id ? status : item.status,
        title: item.title,
        title_zh: item.title_zh,
        description: item.description,
        description_zh: item.description_zh,
        prompt: item.prompt,
        prompt_zh: item.prompt_zh,
        position_x: item.position_x,
        position_y: item.position_y,
        source_refs: item.source_refs,
      })),
    });
  }

  function handleGenerateRequest(node: ProjectVisualNode) {
    if (node.type === "reference") {
      return;
    }
    setSelectedFromNode(node);
    if (!selectedArtAgentId) {
      toast.message("Select an art agent first.");
      return;
    }
    generateImage.mutate(
      { nodeId: node.id, agent_id: selectedArtAgentId },
      {
        onSuccess: (result) => {
          const issueLabel = result.issue_identifier || result.issue_id;
          toast.success(issueLabel ? `Generation issue queued: ${issueLabel}` : "Generation issue queued");
        },
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to queue generation"),
      },
    );
  }

  function handleConnect(connection: Connection) {
    if (!board || !connection.source || !connection.target || connection.source === connection.target) {
      return;
    }
    const source = board.nodes.find((node) => node.id === connection.source);
    const target = board.nodes.find((node) => node.id === connection.target);
    if (!source || !target) {
      return;
    }
    const relation = source.type === "reference" ? "reference" : "uses";
    if (board.edges.some((edge) => edge.source_node_id === source.id && edge.target_node_id === target.id && edge.relation === relation)) {
      toast.message("Reference already linked.");
      return;
    }
    updateBoard.mutate({
      viewport: board.viewport,
      metadata: board.metadata,
      nodes: board.nodes.map((node) => ({
        id: node.id,
        type: node.type,
        status: node.status,
        title: node.title,
        title_zh: node.title_zh,
        description: node.description,
        description_zh: node.description_zh,
        prompt: node.prompt,
        prompt_zh: node.prompt_zh,
        position_x: node.position_x,
        position_y: node.position_y,
        source_refs: node.source_refs,
      })),
      edges: [
        ...board.edges.map((edge) => ({
          id: edge.id,
          source_node_id: edge.source_node_id,
          target_node_id: edge.target_node_id,
          relation: edge.relation,
        })),
        {
          id: `new-${source.id}-${target.id}-${Date.now()}`,
          source_node_id: source.id,
          target_node_id: target.id,
          relation,
        },
      ],
    }, {
      onSuccess: () => toast.success("Reference linked"),
      onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to link reference"),
    });
  }

  function openCreateNodeDialog(position: XYPosition, sourceNodeId = "") {
    const source = board?.nodes.find((node) => node.id === sourceNodeId) ?? null;
    const type: ProjectVisualNodeType = source && (source.type === "character" || source.type === "generated_variant")
      ? "animation"
      : "character";
    setNodeDraft({
      position,
      sourceNodeId,
      type,
      title: source ? `${source.title} animation` : "New Character",
      description: source ? `Animation generation node derived from ${source.title}.` : "",
      prompt: buildNodePrompt(type, source),
      relation: source ? "variant_of" : "",
    });
  }

  function updateNodeDraftType(type: ProjectVisualNodeType) {
    setNodeDraft((draft) => {
      if (!draft) return draft;
      const source = board?.nodes.find((node) => node.id === draft.sourceNodeId) ?? null;
      const previousDefaultTitle = defaultNodeTitle(draft.type, source);
      return {
        ...draft,
        type,
        title: !draft.title || draft.title === previousDefaultTitle ? defaultNodeTitle(type, source) : draft.title,
        prompt: buildNodePrompt(type, source),
      };
    });
  }

  function submitNodeDraft() {
    if (!nodeDraft) return;
    const title = nodeDraft.title.trim();
    if (!title) {
      toast.message("Node title is required.");
      return;
    }
    createNode.mutate(
      {
        type: nodeDraft.type,
        title,
        description: nodeDraft.description.trim(),
        prompt: nodeDraft.prompt.trim(),
        position_x: nodeDraft.position.x,
        position_y: nodeDraft.position.y,
        source_refs: nodeDraft.sourceNodeId ? [{
          visual_node_id: nodeDraft.sourceNodeId,
        }] : [],
        source_node_id: nodeDraft.sourceNodeId || undefined,
        relation: nodeDraft.relation || undefined,
      },
      {
        onSuccess: () => {
          setNodeDraft(null);
          toast.success("Visual node created");
        },
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to create visual node"),
      },
    );
  }

  function handleDeleteNode(node: ProjectVisualNode) {
    deleteNode.mutate(node.id, {
      onSuccess: () => {
        if (selectedId === node.id) {
          setSelectedId("");
        }
        toast.success("Visual node deleted");
      },
      onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to delete visual node"),
    });
  }

  function handleClearBoard() {
    clearBoard.mutate(undefined, {
      onSuccess: () => {
        setSelectedId("");
        setClearDialogOpen(false);
        toast.success("Visual Board cleared");
      },
      onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to clear Visual Board"),
    });
  }

  function handleRestoreGeneration(generation: ProjectVisualNodeGeneration) {
    if (!selectedNode || !generation.id || !generation.attachment_id) return;
    restoreGeneration.mutate(
      { nodeId: selectedNode.id, generationId: generation.id },
      {
        onSuccess: () => toast.success("Visual result restored"),
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to restore visual result"),
      },
    );
  }

  function updateSelectedNode(patch: Partial<ProjectVisualNode>) {
    if (!board || !selectedNode) return;
    updateBoard.mutate({
      viewport: board.viewport,
      metadata: board.metadata,
      nodes: board.nodes.map((node) => ({
        id: node.id,
        type: (node.id === selectedNode.id ? patch.type ?? node.type : node.type),
        status: (node.id === selectedNode.id ? patch.status ?? node.status : node.status),
        title: node.id === selectedNode.id ? patch.title ?? node.title : node.title,
        title_zh: node.id === selectedNode.id ? patch.title_zh ?? node.title_zh : node.title_zh,
        description: node.id === selectedNode.id ? patch.description ?? node.description : node.description,
        description_zh: node.id === selectedNode.id ? patch.description_zh ?? node.description_zh : node.description_zh,
        prompt: node.id === selectedNode.id ? patch.prompt ?? node.prompt : node.prompt,
        prompt_zh: node.id === selectedNode.id ? patch.prompt_zh ?? node.prompt_zh : node.prompt_zh,
        position_x: node.position_x,
        position_y: node.position_y,
        source_refs: node.source_refs,
      })),
    });
  }

  function handleCreatePlan(planMode: ProjectVisualPlanMode) {
    createPlan.mutate(
      { gameplay_notes: gameplayNotes, plan_mode: planMode },
      {
        onSuccess: (plan) => {
          const prefix = planMode === "production_asset_integration"
            ? "Production asset integration plan created"
            : "Playable prototype plan created";
          toast.success(`${prefix}: ${plan.title}`);
        },
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to create plan"),
      },
    );
  }

  const adoptedCount = board?.nodes.filter((node) => node.status === "adopted").length ?? 0;
  const selectedTitle = selectedNode ? displayNodeText(selectedNode.title_zh, selectedNode.title) : "";
  const selectedDescription = selectedNode ? displayNodeText(selectedNode.description_zh, selectedNode.description) : "";
  const selectedPrompt = selectedNode ? displayNodeText(selectedNode.prompt_zh, selectedNode.prompt) : "";
  const selectedResultNote = selectedNode ? displayNodeText(selectedNode.result_note_zh, selectedNode.result_note) : "";
  const selectedGenerationError = selectedNode ? displayNodeText(selectedNode.generation_error_zh, selectedNode.generation_error) : "";

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center gap-2 overflow-x-auto border-b px-3">
          <Button
            size="sm"
            variant="outline"
            disabled={generateNodes.isPending}
            onClick={() => generateNodes.mutate(undefined, {
              onSuccess: (result) => {
                const issueLabel = result.issue_identifier || result.issue_id;
                toast.success(issueLabel ? `Visual extraction issue queued: ${issueLabel}` : "Visual extraction issue queued");
              },
              onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to generate nodes"),
            })}
          >
            {generateNodes.isPending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : <Sparkles className="mr-1.5 h-3.5 w-3.5" />}
            Generate Nodes from Wiki
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={!board || board.nodes.length === 0 || clearBoard.isPending}
            onClick={() => setClearDialogOpen(true)}
          >
            {clearBoard.isPending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : <Trash2 className="mr-1.5 h-3.5 w-3.5" />}
            清空画板
          </Button>
          <div className="ml-auto flex shrink-0 items-center gap-2">
            <Button
              size="sm"
              className="shrink-0"
              disabled={adoptedCount === 0 || createPlan.isPending}
              onClick={() => handleCreatePlan("playable_prototype")}
            >
              <Wand2 className="mr-1.5 h-3.5 w-3.5" />
              Create Playable Prototype Plan
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="shrink-0"
              disabled={adoptedCount === 0 || createPlan.isPending}
              onClick={() => handleCreatePlan("production_asset_integration")}
            >
              <Image className="mr-1.5 h-3.5 w-3.5" />
              Create Production Asset Integration Plan
            </Button>
          </div>
        </div>
        <AlertDialog open={clearDialogOpen} onOpenChange={(open) => {
          if (!clearBoard.isPending) {
            setClearDialogOpen(open);
          }
        }}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>清空 Visual Board？</AlertDialogTitle>
              <AlertDialogDescription>
                这会删除当前画板里的所有 visual 节点和连线。已创建的 issue、wiki 和上传附件不会被删除。
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={clearBoard.isPending}>取消</AlertDialogCancel>
              <AlertDialogAction
                disabled={clearBoard.isPending}
                className="bg-destructive text-white hover:bg-destructive/90"
                onClick={handleClearBoard}
              >
                {clearBoard.isPending ? "清空中..." : "确认清空"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
        <div className="min-h-0 flex-1">
          <ReactFlow
            nodes={flowNodes}
            edges={(board?.edges ?? []).map((edge) => ({
              id: edge.id,
              source: edge.source_node_id,
              target: edge.target_node_id,
              label: edge.relation,
            }))}
            nodeTypes={NODE_TYPES}
            colorMode={(resolvedTheme as "dark" | "light") ?? "light"}
            fitView
            fitViewOptions={{ padding: 0.18 }}
            onInit={setReactFlow}
            onNodesChange={onNodesChange}
            onConnect={handleConnect}
            onNodeDragStop={(_, __, nodes) => saveBoard(nodes as VisualCanvasNode[])}
            onPaneContextMenu={(event) => {
              event.preventDefault();
              openCreateNodeDialog(flowPositionFromEvent(event));
            }}
            onConnectStart={(_, params) => setConnectingSourceId(params.nodeId ?? "")}
            onConnectEnd={(event, connectionState: FinalConnectionState) => {
              if (connectionState.isValid) return;
              const sourceNodeId = connectionState.fromNode?.id ?? connectingSourceId;
              setConnectingSourceId("");
              if (!sourceNodeId || board?.nodes.find((node) => node.id === sourceNodeId)?.type === "reference") return;
              openCreateNodeDialog(flowPositionFromEvent(event), sourceNodeId);
            }}
            proOptions={{ hideAttribution: true }}
          >
            <Background color="hsl(var(--muted-foreground) / 0.18)" gap={20} size={1} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      </div>
      <aside className="flex w-80 shrink-0 flex-col border-l bg-background">
        <div className="border-b p-3">
          <div className="text-sm font-medium">Visual Node</div>
          <p className="mt-1 text-xs text-muted-foreground">{adoptedCount} adopted nodes</p>
        </div>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
          <label className="text-xs text-muted-foreground" htmlFor="visual-art-agent-select">Art agent</label>
          <select
            id="visual-art-agent-select"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={selectedAgentId}
            onChange={(event) => setSelectedAgentId(event.target.value)}
          >
            <option value="">Select agent</option>
            {artAgents.map((agent: Agent) => (
              <option key={agent.id} value={agent.id}>{agent.name}</option>
            ))}
          </select>
          {selectedNode ? (
            <ContextMenu>
              <ContextMenuTrigger render={<div className="grid gap-4" />}>
                <DetailSection title="基础信息">
                  <div className="rounded-md border bg-muted/30 p-2 text-sm">{selectedTitle || "暂无标题"}</div>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    原文标题
                    <Input value={selectedNode.title} onChange={(event) => updateSelectedNode({ title: event.target.value })} />
                  </label>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    类型
                    <select
                      className="h-9 rounded-md border bg-background px-2 text-sm text-foreground"
                      value={selectedNode.type}
                      onChange={(event) => updateSelectedNode({ type: event.target.value as ProjectVisualNodeType })}
                    >
                      {Object.entries(TYPE_LABELS).map(([value, label]) => (
                        <option key={value} value={value}>{label}</option>
                      ))}
                    </select>
                  </label>
                </DetailSection>
                <DetailSection title="视觉描述">
                  <ReadOnlyBlock>{selectedDescription || "暂无描述"}</ReadOnlyBlock>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    原文描述
                    <Textarea
                      className="min-h-24 resize-none text-foreground"
                      value={selectedNode.description}
                      onChange={(event) => updateSelectedNode({ description: event.target.value })}
                    />
                  </label>
                </DetailSection>
                <DetailSection title="生成提示词">
                  <ReadOnlyBlock mono>{selectedPrompt || "暂无提示词"}</ReadOnlyBlock>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    原文提示词
                    <Textarea
                      className="min-h-40 resize-none font-mono text-xs text-foreground"
                      value={selectedNode.prompt}
                      onChange={(event) => updateSelectedNode({ prompt: event.target.value })}
                    />
                  </label>
                </DetailSection>
                <DetailSection title="生成结果">
                  <ReadOnlyBlock>{selectedResultNote || "暂无生成结果"}</ReadOnlyBlock>
                </DetailSection>
                <DetailSection title="过往生成 issue">
                  {generationHistoryLoading ? (
                    <div className="rounded-md border bg-muted/30 p-2 text-xs text-muted-foreground">加载中...</div>
                  ) : selectedGenerations.length === 0 ? (
                    <div className="rounded-md border border-dashed p-2 text-xs text-muted-foreground">暂无过往生图 issue</div>
                  ) : (
                    <div className="grid gap-2">
                      {selectedGenerations.map((generation) => (
                        <GenerationHistoryItem
                          key={generation.id || generation.task_id}
                          generation={generation}
                          restoring={restoreGeneration.isPending}
                          onRestore={handleRestoreGeneration}
                        />
                      ))}
                    </div>
                  )}
                </DetailSection>
                <DetailSection title="错误信息">
                  <ReadOnlyBlock>{selectedGenerationError || "暂无错误"}</ReadOnlyBlock>
                </DetailSection>
                <DetailSection title="来源信息">
                  <ReadOnlyBlock mono>{formatSourceRefs(selectedNode.source_refs)}</ReadOnlyBlock>
                </DetailSection>
              <div className="grid grid-cols-2 gap-2">
                <Button variant="outline" size="sm" onClick={() => handleStatusChange(selectedNode, "adopted")}>Adopt</Button>
                <Button variant="outline" size="sm" onClick={() => handleStatusChange(selectedNode, "rejected")}>Reject</Button>
              </div>
              {selectedNode.type === "character" || selectedNode.type === "generated_variant" ? (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={createNode.isPending}
                  onClick={() => openCreateNodeDialog({ x: selectedNode.position_x + 340, y: selectedNode.position_y }, selectedNode.id)}
                >
                  <Film className="mr-1.5 h-3.5 w-3.5" />
                  Add Animation Node
                </Button>
              ) : null}
              {selectedNode.type !== "reference" ? (
                <Button size="sm" disabled={!selectedArtAgentId || generateImage.isPending} onClick={() => handleGenerateRequest(selectedNode)}>
                  <Image className="mr-1.5 h-3.5 w-3.5" />
                  {selectedNode.type === "animation" ? "Generate Animation" : "Generate Image"}
                </Button>
              ) : null}
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onClick={() => setOriginalNode(selectedNode)}>
                  <FileText className="h-4 w-4" />
                  查看智能体原文
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          ) : (
            <div className="flex flex-1 items-center justify-center text-center text-xs text-muted-foreground">
              Select a node to edit prompt and generation settings.
            </div>
          )}
          <div className="mt-auto">
            <label className="text-xs text-muted-foreground" htmlFor="visual-gameplay-notes">Gameplay notes for Plan</label>
            <Textarea
              id="visual-gameplay-notes"
              className="mt-1 min-h-28 resize-none"
              value={gameplayNotes}
              onChange={(event) => setGameplayNotes(event.target.value)}
            />
          </div>
        </div>
      </aside>
      <Dialog open={nodeDraft !== null} onOpenChange={(open) => { if (!open) setNodeDraft(null); }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Create Visual Node</DialogTitle>
          </DialogHeader>
          {nodeDraft ? (
            <div className="grid gap-3">
              <label className="grid gap-1 text-xs text-muted-foreground">
                Type
                <select
                  className="h-9 rounded-md border bg-background px-2 text-sm text-foreground"
                  value={nodeDraft.type}
                  onChange={(event) => updateNodeDraftType(event.target.value as ProjectVisualNodeType)}
                >
                  {Object.entries(TYPE_LABELS).map(([value, label]) => (
                    <option key={value} value={value}>{label}</option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1 text-xs text-muted-foreground">
                Title
                <Input value={nodeDraft.title} onChange={(event) => setNodeDraft({ ...nodeDraft, title: event.target.value })} />
              </label>
              {nodeDraft.sourceNodeId ? (
                <label className="grid gap-1 text-xs text-muted-foreground">
                  Relation
                  <select
                    className="h-9 rounded-md border bg-background px-2 text-sm text-foreground"
                    value={nodeDraft.relation}
                    onChange={(event) => setNodeDraft({ ...nodeDraft, relation: event.target.value })}
                  >
                    <option value="variant_of">variant_of</option>
                    <option value="reference">reference</option>
                    <option value="uses">uses</option>
                    <option value="supports_gameplay">supports_gameplay</option>
                  </select>
                </label>
              ) : null}
              <label className="grid gap-1 text-xs text-muted-foreground">
                Description
                <Textarea className="min-h-20 resize-none" value={nodeDraft.description} onChange={(event) => setNodeDraft({ ...nodeDraft, description: event.target.value })} />
              </label>
              <label className="grid gap-1 text-xs text-muted-foreground">
                Prompt
                <Textarea className="min-h-36 resize-none font-mono text-xs" value={nodeDraft.prompt} onChange={(event) => setNodeDraft({ ...nodeDraft, prompt: event.target.value })} />
              </label>
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setNodeDraft(null)}>Cancel</Button>
            <Button disabled={createNode.isPending} onClick={submitNodeDraft}>
              {createNode.isPending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : null}
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={originalNode !== null} onOpenChange={(open) => { if (!open) setOriginalNode(null); }}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>智能体原文</DialogTitle>
          </DialogHeader>
          {originalNode ? (
            <div className="grid max-h-[70vh] gap-3 overflow-auto text-sm">
              <OriginalTextBlock title="Title" value={originalNode.title} />
              <OriginalTextBlock title="Description" value={originalNode.description} />
              <OriginalTextBlock title="Prompt" value={originalNode.prompt} mono />
              <OriginalTextBlock title="Result note" value={originalNode.result_note} />
              <OriginalTextBlock title="Generation error" value={originalNode.generation_error} />
              <OriginalTextBlock title="Source refs" value={formatSourceRefs(originalNode.source_refs)} mono />
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function VisualNodeCard({ data }: NodeProps<VisualFlowNode>) {
  const node = data.node;
  const imageUrl = node.result_attachment?.download_url;
  const isReference = node.type === "reference";
  const generateTooltip = node.type === "animation" ? "生成动画资源" : "生成图片资源";
  const title = displayNodeText(node.title_zh, node.title);
  const summary = displayNodeText(node.prompt_zh, node.prompt) || displayNodeText(node.description_zh, node.description);
  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={(
          <div
            role="button"
            tabIndex={0}
            className="w-64 overflow-hidden rounded-md border bg-card text-left shadow-sm"
            onClick={() => data.onSelect(node)}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                data.onSelect(node);
              }
            }}
          />
        )}
      >
        <Handle type="target" position={Position.Left} />
        {!isReference ? (
          <div className="h-32 bg-muted">
            {imageUrl ? (
              <img src={imageUrl} alt="" className="h-full w-full object-cover" />
            ) : (
              <div className="flex h-full items-center justify-center text-muted-foreground">
                {node.status === "generating" ? <Loader2 className="h-6 w-6 animate-spin" /> : <Image className="h-6 w-6" />}
              </div>
            )}
          </div>
        ) : null}
        <div className="space-y-2 p-3">
          <div className="flex items-center gap-2">
            <span className="rounded-sm bg-muted px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{TYPE_LABELS[node.type]}</span>
            <span className={cn("ml-auto text-[10px] uppercase", node.status === "failed" ? "text-destructive" : "text-muted-foreground")}>{node.status}</span>
          </div>
          <div className="line-clamp-1 text-sm font-medium">{title}</div>
          <div className="line-clamp-2 text-xs text-muted-foreground">{summary}</div>
          <div className="flex gap-1">
            <NodeActionButton label="采纳节点" onClick={() => data.onStatus(node, "adopted")}>
              <Check className="h-3.5 w-3.5" />
            </NodeActionButton>
            <NodeActionButton label="拒绝节点" onClick={() => data.onStatus(node, "rejected")}>
              <X className="h-3.5 w-3.5" />
            </NodeActionButton>
            {!isReference ? (
              <NodeActionButton label={generateTooltip} className="ml-auto" onClick={() => data.onGenerate(node)}>
                <Wand2 className="h-3.5 w-3.5" />
              </NodeActionButton>
            ) : null}
          </div>
        </div>
        <Handle type="source" position={Position.Right} />
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem
          variant="destructive"
          onClick={(event) => {
            event.stopPropagation();
            data.onDelete(node);
          }}
        >
          <Trash2 className="h-4 w-4" />
          删除节点
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function ReferenceGroupNodeCard({ data }: NodeProps<ReferenceGroupNode>) {
  return (
    <div className="h-full w-full rounded-md border border-dashed border-muted-foreground/35 bg-muted/15 p-3">
      <div className="text-xs font-semibold uppercase text-muted-foreground">Reference Library</div>
      <div className="mt-1 text-[11px] text-muted-foreground">{data.count} references</div>
    </div>
  );
}

function GenerationHistoryItem({
  generation,
  restoring,
  onRestore,
}: {
  generation: ProjectVisualNodeGeneration;
  restoring: boolean;
  onRestore: (generation: ProjectVisualNodeGeneration) => void;
}) {
  const previewUrl = generation.attachment?.download_url;
  const displayNote = displayNodeText(generation.note_zh, generation.note);
  const displayError = displayNodeText(generation.error_zh, generation.error);
  const canRestore = Boolean(generation.id && generation.attachment_id && !generation.is_current);
  return (
    <div className="rounded-md border bg-background p-2">
      <div className="flex items-start gap-2">
        {previewUrl ? (
          <img src={previewUrl} alt="" className="h-12 w-12 rounded-sm border object-cover" />
        ) : (
          <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-sm border bg-muted text-muted-foreground">
            <Image className="h-4 w-4" />
          </div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-xs font-medium">
              {generation.issue_identifier || generation.issue_title || "未关联 issue"}
            </span>
            {generation.is_current ? (
              <span className="rounded-sm bg-primary/10 px-1.5 py-0.5 text-[10px] uppercase text-primary">current</span>
            ) : null}
          </div>
          <div className="mt-0.5 line-clamp-1 text-[11px] text-muted-foreground">
            {generation.issue_title || generation.task_status || "生成任务"}
          </div>
          {displayNote || displayError ? (
            <div className={cn("mt-1 line-clamp-2 text-[11px]", displayError ? "text-destructive" : "text-muted-foreground")}>
              {displayError || displayNote}
            </div>
          ) : null}
        </div>
      </div>
      <div className="mt-2 flex items-center justify-between gap-2">
        <span className="text-[11px] text-muted-foreground">
          {formatShortDate(generation.completed_at || generation.created_at)}
        </span>
        <Button
          size="sm"
          variant="outline"
          className="h-7 px-2 text-xs"
          disabled={!canRestore || restoring}
          onClick={() => onRestore(generation)}
        >
          {restoring ? <Loader2 className="mr-1 h-3 w-3 animate-spin" /> : <RotateCcw className="mr-1 h-3 w-3" />}
          回滚到此版本
        </Button>
      </div>
    </div>
  );
}

function isVisualFlowNode(node: VisualCanvasNode): node is VisualFlowNode {
  return node.type === "visualNode";
}

function buildReferenceGroupNode(nodes: VisualFlowNode[]): ReferenceGroupNode | null {
  const references = nodes.filter((node) => node.data.node.type === "reference");
  if (references.length === 0) {
    return null;
  }
  const minX = Math.min(...references.map((node) => node.position.x));
  const minY = Math.min(...references.map((node) => node.position.y));
  const maxX = Math.max(...references.map((node) => node.position.x + NODE_CARD_WIDTH));
  const maxY = Math.max(...references.map((node) => node.position.y + NODE_CARD_HEIGHT));
  return {
    id: REFERENCE_GROUP_ID,
    type: "referenceGroup",
    position: {
      x: minX - REFERENCE_GROUP_PADDING,
      y: minY - REFERENCE_GROUP_PADDING - 34,
    },
    data: { count: references.length },
    draggable: false,
    selectable: false,
    connectable: false,
    zIndex: -1,
    style: {
      width: Math.max(320, maxX - minX + REFERENCE_GROUP_PADDING * 2),
      height: Math.max(220, maxY - minY + REFERENCE_GROUP_PADDING * 2 + 34),
    },
  };
}

function NodeActionButton({
  label,
  className,
  children,
  onClick,
}: {
  label: string;
  className?: string;
  children: ReactNode;
  onClick: () => void;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={(
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className={cn("h-7 w-7", className)}
            aria-label={label}
            title={label}
            onClick={(event) => {
              event.stopPropagation();
              onClick();
            }}
          />
        )}
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={6}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
}

function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="grid gap-2">
      <h3 className="text-xs font-semibold text-foreground">{title}</h3>
      {children}
    </section>
  );
}

function ReadOnlyBlock({ children, mono = false }: { children: ReactNode; mono?: boolean }) {
  return (
    <div className={cn(
      "min-h-10 whitespace-pre-wrap rounded-md border bg-muted/30 p-2 text-xs text-foreground",
      mono && "font-mono",
    )}>
      {children}
    </div>
  );
}

function OriginalTextBlock({ title, value, mono = false }: { title: string; value: string; mono?: boolean }) {
  return (
    <section className="grid gap-1">
      <h3 className="text-xs font-semibold text-muted-foreground">{title}</h3>
      <ReadOnlyBlock mono={mono}>{value.trim() || "empty"}</ReadOnlyBlock>
    </section>
  );
}

function displayNodeText(preferred: string, fallback: string): string {
  return preferred.trim() || fallback.trim();
}

function formatShortDate(value: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatSourceRefs(sourceRefs: unknown[]): string {
  if (!Array.isArray(sourceRefs) || sourceRefs.length === 0) {
    return "[]";
  }
  return JSON.stringify(sourceRefs, null, 2);
}

function clientPointFromEvent(event: MouseEvent | TouchEvent | ReactMouseEvent): XYPosition | null {
  if ("changedTouches" in event) {
    const touch = event.changedTouches[0];
    return touch ? { x: touch.clientX, y: touch.clientY } : null;
  }
  return { x: event.clientX, y: event.clientY };
}

function defaultNodeTitle(type: ProjectVisualNodeType, source: ProjectVisualNode | null): string {
  if (source && type === "animation") return `${source.title} animation`;
  return `New ${TYPE_LABELS[type]}`;
}

function buildNodePrompt(type: ProjectVisualNodeType, source: ProjectVisualNode | null): string {
  if (type === "animation") {
    return buildAnimationNodePrompt(source);
  }
  const sourceLines = source
    ? [
      `Source node: ${source.title}`,
      source.prompt ? `Source visual prompt: ${source.prompt}` : "",
      source.description ? `Source description: ${source.description}` : "",
    ].filter(Boolean)
    : [];
  const transparency = type === "character" || type === "generated_variant"
    ? "Preserve transparent alpha in the selected handoff asset; do not bake a scene/background unless explicitly requested."
    : "";
  return [
    `Create a production-ready ${type} visual asset.`,
    "Use the game-asset-pipeline skill as the required workflow: artstyle/artrule, asset_manifest.json, bounded generation, deterministic validation, QA notes, retry notes, and handoff paths.",
    transparency,
    ...sourceLines,
  ].filter(Boolean).join("\n");
}

function buildAnimationNodePrompt(source: ProjectVisualNode | null): string {
  return [
    source ? `Create a production-ready animation asset set for "${source.title}".` : "Create a production-ready animation asset set for the selected character or visual subject.",
    "Use the game-asset-pipeline skill as the required workflow: artstyle/artrule, animation_manifest.json, bounded generation, deterministic validation, alpha/background rules, preview sheet/GIF QA, retry notes, and handoff paths.",
    "Produce the same result class inside this Multica visual-node workflow: transparent spritesheet PNG/WebP, per-action previews, validation JSON, QA notes, and final handoff paths.",
    "Do not bake a scene/background into character frames. Preserve transparent alpha in the selected handoff asset; if GPT Image 2 is used, generate on a flat removable chroma-key background and remove the key locally before validation.",
    "Default actions: idle, walk-right, walk-left, wave, jump, attack, hit, die. Keep the action set smaller only when the source node or gameplay role clearly requires it.",
    "Output expectations: spritesheet PNG/WebP, per-action previews, validation JSON, and a short note that lists generated paths and QA status.",
    source?.prompt ? `Source visual prompt: ${source.prompt}` : "",
    source?.description ? `Source description: ${source.description}` : "",
  ].filter(Boolean).join("\n");
}
