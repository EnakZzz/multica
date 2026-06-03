"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
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
import { Check, ChevronRight, FileText, Film, Image, LayoutGrid, Loader2, RotateCcw, Sparkles, Trash2, Volume2, Wand2, X } from "lucide-react";
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
  variantStack?: VariantStackNodeMeta;
  isSelected: boolean;
  isPrerequisite: boolean;
  onSelect: (node: ProjectVisualNode) => void;
  onStatus: (node: ProjectVisualNode, status: ProjectVisualNodeStatus) => void;
  onGenerate: (node: ProjectVisualNode) => void;
  onDelete: (node: ProjectVisualNode) => void;
  onNextVariant: (node: ProjectVisualNode) => void;
  readOnly: boolean;
};

type VariantStackNodeMeta = {
  parentId: string;
  index: number;
  count: number;
  isTop: boolean;
  switchTick: number;
};

type VisualFlowNode = Node<VisualFlowData, "visualNode">;
type ReferenceGroupData = {
  count: number;
};
type ReferenceGroupNode = Node<ReferenceGroupData, "referenceGroup">;
type VariantStackGroupData = {
  count: number;
};
type VariantStackGroupNode = Node<VariantStackGroupData, "variantStackGroup">;
type VisualCanvasNode = VisualFlowNode | ReferenceGroupNode | VariantStackGroupNode;

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
const NODE_LAYOUT_REFERENCE_X = 0;
const NODE_LAYOUT_CHARACTER_X = 420;
const NODE_LAYOUT_SCENE_X = 920;
const NODE_LAYOUT_ASSET_X = 1420;
const NODE_LAYOUT_VARIANT_OFFSET_X = 320;
const NODE_LAYOUT_VARIANT_CASCADE_STEP = 18;
const NODE_LAYOUT_ROW_GAP = 88;
const NODE_LAYOUT_COLLISION_PADDING = 32;
const REFERENCE_GROUP_PADDING = 24;
const VARIANT_STACK_GROUP_PREFIX = "__variant-stack__";
const VARIANT_STACK_GROUP_INSET_X = 14;
const VARIANT_STACK_GROUP_PADDING = 12;
const VISUAL_EDGE_STYLE = {
  filter: "drop-shadow(0 0 2px rgba(15, 23, 42, 0.25))",
  stroke: "rgba(100, 116, 139, 0.9)",
  strokeWidth: 2.6,
};
const VISUAL_HIGHLIGHT_EDGE_STYLE = {
  filter: "drop-shadow(0 0 3px rgba(59, 130, 246, 0.42))",
  stroke: "rgba(59, 130, 246, 0.98)",
  strokeWidth: 2,
};

const NODE_TYPES = { visualNode: VisualNodeCard, referenceGroup: ReferenceGroupNodeCard, variantStackGroup: VariantStackGroupNodeCard };

const TYPE_LABELS: Record<ProjectVisualNodeType, string> = {
  character: "Character",
  scene: "Scene",
  ui_element: "UI",
  prop: "Prop",
  reference: "Reference",
  gameplay_note: "Gameplay",
  generated_variant: "Variant",
  animation: "Animation",
  video: "Video",
  audio: "Audio",
};

export function ProjectVisualCanvas({ projectId, readOnly = false }: { projectId: string; readOnly?: boolean }) {
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
  const [variantStackTopByParentId, setVariantStackTopByParentId] = useState<Record<string, string>>({});
  const [variantStackSwitchTickByParentId, setVariantStackSwitchTickByParentId] = useState<Record<string, number>>({});
  const dragStartNodesRef = useRef<VisualCanvasNode[]>([]);

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

  const handleNextVariant = useCallback((node: ProjectVisualNode) => {
    if (!board) return;
    const stacks = buildVariantStacks(board.nodes, board.edges);
    const parentId = stacks.childToParent.get(node.id) ?? "";
    const stack = stacks.parentToChildren.get(parentId);
    if (!stack || stack.length <= 1) return;
    const currentIndex = Math.max(0, stack.findIndex((item) => item.id === node.id));
    const next = stack[(currentIndex + 1) % stack.length];
    if (!next) return;
    setVariantStackTopByParentId((current) => ({
      ...current,
      [parentId]: next.id,
    }));
    setVariantStackSwitchTickByParentId((current) => ({
      ...current,
      [parentId]: (current[parentId] ?? 0) + 1,
    }));
    setSelectedId(next.id);
  }, [board]);

  const syncBoardToFlow = useCallback(() => {
    if (!board) return;
    setFlowNodes((prev) => {
      const prevPositions = visualNodeAbsolutePositions(prev);
      const stacks = buildVariantStacks(board.nodes, board.edges);
      const prerequisiteIds = buildSelectedPrerequisiteIds(board.edges, selectedId);
      const variantStackPositions = buildVariantStackPositions(board.nodes, stacks, prevPositions, variantStackTopByParentId);
      const variantStackGroups = buildVariantStackGroupNodes(stacks, variantStackPositions);
      const visualNodes: VisualFlowNode[] = board.nodes.map((node) => ({
        id: node.id,
        type: "visualNode",
        ...visualNodeStackLayout(node, stacks, variantStackGroups, variantStackPositions, prevPositions, variantStackTopByParentId),
        zIndex: visualNodeZIndex(node, stacks, variantStackTopByParentId),
        data: {
          node,
          variantStack: variantStackMeta(node, stacks, variantStackTopByParentId, variantStackSwitchTickByParentId),
          isSelected: node.id === selectedId,
          isPrerequisite: prerequisiteIds.has(node.id),
          onSelect: setSelectedFromNode,
          onStatus: handleStatusChange,
          onGenerate: handleGenerateRequest,
          onDelete: handleDeleteNode,
          onNextVariant: handleNextVariant,
          readOnly,
        },
      }));
      const variantStackGroupNodes = [...variantStackGroups.values()];
      const groupNode = buildReferenceGroupNode([...variantStackGroupNodes, ...visualNodes]);
      return groupNode ? [groupNode, ...variantStackGroupNodes, ...visualNodes] : [...variantStackGroupNodes, ...visualNodes];
    });
  }, [board, readOnly, selectedArtAgentId, selectedId, variantStackTopByParentId, variantStackSwitchTickByParentId, handleNextVariant]);

  useEffect(() => {
    syncBoardToFlow();
  }, [syncBoardToFlow]);

  const setSelectedFromNode = useCallback((node: ProjectVisualNode) => {
    if (board) {
      const stacks = buildVariantStacks(board.nodes, board.edges);
      const parentId = stacks.childToParent.get(node.id);
      const siblings = parentId ? stacks.parentToChildren.get(parentId) ?? [] : [];
      if (parentId && siblings.length > 1 && variantStackTopByParentId[parentId] !== node.id) {
        setVariantStackTopByParentId((current) => ({
          ...current,
          [parentId]: node.id,
        }));
        setVariantStackSwitchTickByParentId((current) => ({
          ...current,
          [parentId]: (current[parentId] ?? 0) + 1,
        }));
      }
    }
    setSelectedId(node.id);
    setSelectedAgentId((current) => node.generation_agent_id ?? current);
  }, [board, variantStackTopByParentId]);

  const saveBoard = useCallback((nodesOverride?: VisualCanvasNode[]) => {
    if (readOnly || !board) return;
    const positionById = visualNodeAbsolutePositions(nodesOverride ? mergeVisualCanvasNodes(flowNodes, nodesOverride) : flowNodes);
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
          implementation_path: node.implementation_path,
          implementation_note: node.implementation_note,
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
  }, [board, flowNodes, readOnly, updateBoard]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setFlowNodes((prev) => applyNodeChanges(changes, prev) as VisualCanvasNode[]);
  }, []);

  const flowPositionFromEvent = useCallback((event: MouseEvent | TouchEvent | React.MouseEvent): XYPosition => {
    const point = clientPointFromEvent(event);
    if (!point || !reactFlow) return { x: 0, y: 0 };
    return reactFlow.screenToFlowPosition(point);
  }, [reactFlow]);

  function handleStatusChange(node: ProjectVisualNode, status: ProjectVisualNodeStatus) {
    if (readOnly || !board) return;
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
        implementation_path: item.implementation_path,
        implementation_note: item.implementation_note,
        source_refs: item.source_refs,
      })),
    });
  }

  function handleGenerateRequest(node: ProjectVisualNode) {
    if (readOnly) return;
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
    if (readOnly || !board || !connection.source || !connection.target || connection.source === connection.target) {
      return;
    }
    const source = board.nodes.find((node) => node.id === connection.source);
    const target = board.nodes.find((node) => node.id === connection.target);
    if (!source || !target) {
      return;
    }
    const relation = source.type === "reference" && target.type === "video" ? "prerequisite" : source.type === "reference" ? "reference" : "uses";
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
        implementation_path: node.implementation_path,
        implementation_note: node.implementation_note,
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
    if (readOnly) return;
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
    if (readOnly || !nodeDraft) return;
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
    if (readOnly) return;
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
    if (readOnly) return;
    clearBoard.mutate(undefined, {
      onSuccess: () => {
        setSelectedId("");
        setClearDialogOpen(false);
        toast.success("Visual Board cleared");
      },
      onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to clear Visual Board"),
    });
  }

  function handleOrganizeBoard() {
    if (readOnly || !board || board.nodes.length === 0) return;
    const positions = buildOrganizedNodePositions(board.nodes, board.edges);
    const stacks = buildVariantStacks(board.nodes, board.edges);
    const prerequisiteIds = buildSelectedPrerequisiteIds(board.edges, selectedId);
    const variantStackGroups = buildVariantStackGroupNodes(stacks, positions);
    const visualNodes: VisualFlowNode[] = board.nodes.map((node) => ({
      id: node.id,
      type: "visualNode",
      ...visualNodeStackLayout(node, stacks, variantStackGroups, positions, positions, variantStackTopByParentId),
      zIndex: visualNodeZIndex(node, stacks, variantStackTopByParentId),
      data: {
        node,
        variantStack: variantStackMeta(node, stacks, variantStackTopByParentId, variantStackSwitchTickByParentId),
        isSelected: node.id === selectedId,
        isPrerequisite: prerequisiteIds.has(node.id),
        onSelect: setSelectedFromNode,
        onStatus: handleStatusChange,
        onGenerate: handleGenerateRequest,
        onDelete: handleDeleteNode,
        onNextVariant: handleNextVariant,
        readOnly,
      },
    }));
    const variantStackGroupNodes = [...variantStackGroups.values()];
    const groupNode = buildReferenceGroupNode([...variantStackGroupNodes, ...visualNodes]);
    const nextNodes = groupNode ? [groupNode, ...variantStackGroupNodes, ...visualNodes] : [...variantStackGroupNodes, ...visualNodes];
    setFlowNodes(nextNodes);
    saveBoard(nextNodes);
    window.setTimeout(() => reactFlow?.fitView({ padding: 0.18, duration: 220 }), 0);
    toast.success("Visual Board 已整理");
  }

  function handleNodeDragStart() {
    dragStartNodesRef.current = flowNodes;
  }

  function handleNodeDragStop(_: ReactMouseEvent | MouseEvent, node: VisualCanvasNode, nodes: VisualCanvasNode[]) {
    if (readOnly) return;
    const stackAdjusted = moveVariantStackDrag(node, nodes, dragStartNodesRef.current);
    dragStartNodesRef.current = [];
    if (stackAdjusted) {
      setFlowNodes(stackAdjusted);
      saveBoard(stackAdjusted);
      return;
    }
    saveBoard(nodes);
  }

  function handleNodeDrag(_: ReactMouseEvent | MouseEvent, node: VisualCanvasNode, nodes: VisualCanvasNode[]) {
    if (readOnly) return;
    const stackAdjusted = moveVariantStackDrag(node, nodes, dragStartNodesRef.current);
    if (stackAdjusted) {
      setFlowNodes(stackAdjusted);
    }
  }

  function handleRestoreGeneration(generation: ProjectVisualNodeGeneration) {
    if (readOnly || !selectedNode || !generation.id || !generation.attachment_id) return;
    restoreGeneration.mutate(
      { nodeId: selectedNode.id, generationId: generation.id },
      {
        onSuccess: () => toast.success("Visual result restored"),
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to restore visual result"),
      },
    );
  }

  function updateSelectedNode(patch: Partial<ProjectVisualNode>) {
    if (readOnly || !board || !selectedNode) return;
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
        implementation_path: node.id === selectedNode.id ? patch.implementation_path ?? node.implementation_path : node.implementation_path,
        implementation_note: node.id === selectedNode.id ? patch.implementation_note ?? node.implementation_note : node.implementation_note,
        source_refs: node.source_refs,
      })),
    });
  }

  function handleCreatePlan(planMode: ProjectVisualPlanMode) {
    if (readOnly) return;
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
  const highlightedEdgeIds = useMemo(
    () => buildSelectedIncomingEdgeIds(board?.edges ?? [], selectedId),
    [board?.edges, selectedId],
  );
  const selectedTitle = selectedNode ? displayNodeText(selectedNode.title_zh, selectedNode.title) : "";
  const selectedDescription = selectedNode ? displayNodeText(selectedNode.description_zh, selectedNode.description) : "";
  const selectedPrompt = selectedNode ? displayNodeText(selectedNode.prompt_zh, selectedNode.prompt) : "";
  const selectedResultNote = selectedNode ? displayNodeText(selectedNode.result_note_zh, selectedNode.result_note) : "";
  const selectedGenerationError = selectedNode ? displayNodeText(selectedNode.generation_error_zh, selectedNode.generation_error) : "";

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center gap-2 overflow-x-auto border-b px-3">
          {!readOnly && (
            <>
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
              <Button
                size="sm"
                variant="outline"
                disabled={!board || board.nodes.length === 0 || updateBoard.isPending}
                onClick={handleOrganizeBoard}
              >
                {updateBoard.isPending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : <LayoutGrid className="mr-1.5 h-3.5 w-3.5" />}
                整理画板
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
            </>
          )}
          {readOnly && (
            <div className="text-xs text-muted-foreground">Shared project visual board is read-only in this workspace.</div>
          )}
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
            edges={(board ? visibleVisualEdges(board.nodes, board.edges, variantStackTopByParentId) : []).map((edge) => ({
              id: edge.id,
              source: edge.source_node_id,
              target: edge.target_node_id,
              label: edge.relation,
              animated: false,
              style: visualEdgeStyle(edge, highlightedEdgeIds),
              zIndex: 0,
            }))}
            nodeTypes={NODE_TYPES}
            colorMode={(resolvedTheme as "dark" | "light") ?? "light"}
            connectionLineStyle={VISUAL_EDGE_STYLE}
            fitView
            fitViewOptions={{ padding: 0.18 }}
            minZoom={0.12}
            onInit={setReactFlow}
            onNodesChange={onNodesChange}
            onConnect={readOnly ? undefined : handleConnect}
            onNodeDragStart={handleNodeDragStart}
            onNodeDrag={(event, node, nodes) => handleNodeDrag(event, node as VisualCanvasNode, nodes as VisualCanvasNode[])}
            onNodeDragStop={(event, node, nodes) => handleNodeDragStop(event, node as VisualCanvasNode, nodes as VisualCanvasNode[])}
            onPaneContextMenu={(event) => {
              event.preventDefault();
              if (readOnly) return;
              openCreateNodeDialog(flowPositionFromEvent(event));
            }}
            onConnectStart={(_, params) => setConnectingSourceId(params.nodeId ?? "")}
            onConnectEnd={(event, connectionState: FinalConnectionState) => {
              if (readOnly) return;
              if (connectionState.isValid) return;
              const sourceNodeId = connectionState.fromNode?.id ?? connectingSourceId;
              setConnectingSourceId("");
              if (!sourceNodeId || board?.nodes.find((node) => node.id === sourceNodeId)?.type === "reference") return;
              openCreateNodeDialog(flowPositionFromEvent(event), sourceNodeId);
            }}
            nodesDraggable={!readOnly}
            nodesConnectable={!readOnly}
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
            disabled={readOnly}
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
                    <Input value={selectedNode.title} disabled={readOnly} onChange={(event) => updateSelectedNode({ title: event.target.value })} />
                  </label>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    类型
                    <select
                      className="h-9 rounded-md border bg-background px-2 text-sm text-foreground"
                      value={selectedNode.type}
                      disabled={readOnly}
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
                      disabled={readOnly}
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
                      disabled={readOnly}
                      onChange={(event) => updateSelectedNode({ prompt: event.target.value })}
                    />
                  </label>
                </DetailSection>
                <DetailSection title="生成结果">
                  <ReadOnlyBlock>{selectedResultNote || "暂无生成结果"}</ReadOnlyBlock>
                </DetailSection>
                <DetailSection title="入版绑定">
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    当前入版路径
                    <Input
                      placeholder="src/assets/..."
                      value={selectedNode.implementation_path}
                      disabled={readOnly}
                      onChange={(event) => updateSelectedNode({ implementation_path: event.target.value })}
                    />
                  </label>
                  <label className="grid gap-1 text-xs text-muted-foreground">
                    入版说明
                    <Textarea
                      className="min-h-20 resize-none text-foreground"
                      placeholder="说明这个资源在当前版本里的使用位置、状态或替换关系"
                      value={selectedNode.implementation_note}
                      disabled={readOnly}
                      onChange={(event) => updateSelectedNode({ implementation_note: event.target.value })}
                    />
                  </label>
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
                          readOnly={readOnly}
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
              {!readOnly && (
                <>
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
                      {selectedNode.type === "video" ? <Film className="mr-1.5 h-3.5 w-3.5" /> : selectedNode.type === "audio" ? <Volume2 className="mr-1.5 h-3.5 w-3.5" /> : <Image className="mr-1.5 h-3.5 w-3.5" />}
                      {generateButtonLabel(selectedNode.type)}
                    </Button>
                  ) : null}
                </>
              )}
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
              {readOnly ? "Select a node to view visual details." : "Select a node to edit prompt and generation settings."}
            </div>
          )}
          {!readOnly && <div className="mt-auto">
            <label className="text-xs text-muted-foreground" htmlFor="visual-gameplay-notes">Gameplay notes for Plan</label>
            <Textarea
              id="visual-gameplay-notes"
              className="mt-1 min-h-28 resize-none"
              value={gameplayNotes}
              onChange={(event) => setGameplayNotes(event.target.value)}
            />
          </div>}
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
                    <option value="prerequisite">prerequisite</option>
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
              <OriginalTextBlock title="Implementation path" value={originalNode.implementation_path} />
              <OriginalTextBlock title="Implementation note" value={originalNode.implementation_note} />
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
  const variantStack = data.variantStack;
  const mediaUrl = node.result_attachment?.download_url;
  const contentType = node.result_attachment?.content_type ?? "";
  const isVideoResult = contentType.startsWith("video/") || node.type === "video";
  const isAudioResult = contentType.startsWith("audio/") || node.type === "audio";
  const isReference = node.type === "reference";
  const [mediaAspectRatio, setMediaAspectRatio] = useState(() => defaultMediaAspectRatio(node));
  const [variantSwitching, setVariantSwitching] = useState(false);
  const generateTooltip = node.type === "animation" ? "生成动画资源" : node.type === "video" ? "生成视频资源" : node.type === "audio" ? "生成音频资源" : "生成图片资源";
  const title = displayNodeText(node.title_zh, node.title);
  const summary = displayNodeText(node.prompt_zh, node.prompt) || displayNodeText(node.description_zh, node.description);
  const mediaStyle = { aspectRatio: mediaAspectRatio.toString() };
  useEffect(() => {
    if (!variantStack?.isTop || variantStack.switchTick === 0) return;
    setVariantSwitching(true);
    const timeout = window.setTimeout(() => setVariantSwitching(false), 220);
    return () => window.clearTimeout(timeout);
  }, [variantStack?.isTop, variantStack?.switchTick]);
  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={(
          <div
            role="button"
            tabIndex={0}
            className={cn(
              "w-64 overflow-hidden rounded-md border-2 bg-card text-left shadow-sm transition-[border-color,box-shadow,transform]",
              data.isPrerequisite && "border-blue-400/90 shadow-[0_0_10px_rgba(37,99,235,0.24)]",
              data.isSelected && "border-blue-500 shadow-[0_0_14px_rgba(37,99,235,0.34)]",
              variantSwitching && "scale-[1.025] ring-2 ring-blue-400/45",
            )}
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
          <div className="max-h-[22rem] min-h-28 bg-muted" style={mediaStyle}>
            {mediaUrl && isVideoResult ? (
              <video
                src={mediaUrl}
                className="h-full w-full object-contain"
                muted
                loop
                playsInline
                controls
                onLoadedMetadata={(event) => {
                  const { videoWidth, videoHeight } = event.currentTarget;
                  setMediaAspectRatio(aspectRatioFromSize(videoWidth, videoHeight, mediaAspectRatio));
                }}
              />
            ) : mediaUrl && isAudioResult ? (
              <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-muted-foreground">
                <Volume2 className="h-7 w-7" />
                <audio src={mediaUrl} controls className="w-full" />
              </div>
            ) : mediaUrl ? (
              <img
                src={mediaUrl}
                alt=""
                className="h-full w-full object-contain"
                onLoad={(event) => {
                  const { naturalWidth, naturalHeight } = event.currentTarget;
                  setMediaAspectRatio(aspectRatioFromSize(naturalWidth, naturalHeight, mediaAspectRatio));
                }}
              />
            ) : (
              <div className="flex h-full items-center justify-center text-muted-foreground">
                {node.status === "generating" ? <Loader2 className="h-6 w-6 animate-spin" /> : isVideoResult ? <Film className="h-6 w-6" /> : isAudioResult ? <Volume2 className="h-6 w-6" /> : <Image className="h-6 w-6" />}
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
          {node.implementation_path ? (
            <div className="line-clamp-1 rounded-sm bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] text-primary">
              {node.implementation_path}
            </div>
          ) : null}
          <div className="line-clamp-2 text-xs text-muted-foreground">{summary}</div>
          <div className="flex gap-1">
            {variantStack && variantStack.count > 1 && variantStack.isTop ? (
              <NodeActionButton label="下一张变体" className="ml-auto" onClick={() => data.onNextVariant(node)}>
                <ChevronRight className="h-3.5 w-3.5" />
              </NodeActionButton>
            ) : null}
            {!data.readOnly && (
              <>
                <NodeActionButton label="采纳节点" onClick={() => data.onStatus(node, "adopted")}>
                  <Check className="h-3.5 w-3.5" />
                </NodeActionButton>
                <NodeActionButton label="拒绝节点" onClick={() => data.onStatus(node, "rejected")}>
                  <X className="h-3.5 w-3.5" />
                </NodeActionButton>
                {!isReference ? (
                  <NodeActionButton label={generateTooltip} className={variantStack && variantStack.count > 1 && variantStack.isTop ? "" : "ml-auto"} onClick={() => data.onGenerate(node)}>
                    <Wand2 className="h-3.5 w-3.5" />
                  </NodeActionButton>
                ) : null}
              </>
            )}
          </div>
        </div>
        <Handle type="source" position={Position.Right} />
      </ContextMenuTrigger>
      {!data.readOnly && (
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
      )}
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

function VariantStackGroupNodeCard({ data }: NodeProps<VariantStackGroupNode>) {
  return (
    <div className="h-full w-full rounded-md border border-dashed border-blue-400/35 bg-blue-500/[0.04] shadow-[0_0_18px_rgba(37,99,235,0.12)]">
      <div className="absolute left-1 top-1 h-[calc(100%-0.5rem)] w-2 rounded-sm bg-blue-400/20" aria-label={`${data.count} variants`} />
    </div>
  );
}

function GenerationHistoryItem({
  generation,
  restoring,
  readOnly = false,
  onRestore,
}: {
  generation: ProjectVisualNodeGeneration;
  restoring: boolean;
  readOnly?: boolean;
  onRestore: (generation: ProjectVisualNodeGeneration) => void;
}) {
  const previewUrl = generation.attachment?.download_url;
  const isVideoPreview = generation.attachment?.content_type?.startsWith("video/") === true;
  const displayNote = displayNodeText(generation.note_zh, generation.note);
  const displayError = displayNodeText(generation.error_zh, generation.error);
  const canRestore = Boolean(generation.id && generation.attachment_id && !generation.is_current);
  return (
    <div className="rounded-md border bg-background p-2">
      <div className="flex items-start gap-2">
        {previewUrl && isVideoPreview ? (
          <video src={previewUrl} className="h-12 w-12 rounded-sm border object-cover" muted playsInline />
        ) : previewUrl ? (
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
        {!readOnly && (
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
        )}
      </div>
    </div>
  );
}

function isVisualFlowNode(node: VisualCanvasNode): node is VisualFlowNode {
  return node.type === "visualNode";
}

function visualNodeAbsolutePositions(nodes: VisualCanvasNode[]): Map<string, XYPosition> {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const positions = new Map<string, XYPosition>();
  for (const node of nodes.filter(isVisualFlowNode)) {
    const parent = node.parentId ? nodeById.get(node.parentId) : null;
    positions.set(node.id, parent ? {
      x: parent.position.x + node.position.x,
      y: parent.position.y + node.position.y,
    } : node.position);
  }
  return positions;
}

function mergeVisualCanvasNodes(current: VisualCanvasNode[], updates: VisualCanvasNode[]): VisualCanvasNode[] {
  if (updates.length === 0) return current;
  const updateById = new Map(updates.map((node) => [node.id, node]));
  const merged = current.map((node) => updateById.get(node.id) ?? node);
  const currentIds = new Set(current.map((node) => node.id));
  for (const node of updates) {
    if (!currentIds.has(node.id)) {
      merged.push(node);
    }
  }
  return merged;
}

function buildReferenceGroupNode(nodes: VisualCanvasNode[]): ReferenceGroupNode | null {
  const absolutePositions = visualNodeAbsolutePositions(nodes);
  const visualNodes = nodes.filter(isVisualFlowNode);
  const references = visualNodes.filter((node) => node.data.node.type === "reference");
  if (references.length === 0) {
    return null;
  }
  const minX = Math.min(...references.map((node) => absolutePositions.get(node.id)?.x ?? node.position.x));
  const minY = Math.min(...references.map((node) => absolutePositions.get(node.id)?.y ?? node.position.y));
  const maxX = Math.max(...references.map((node) => (absolutePositions.get(node.id)?.x ?? node.position.x) + NODE_CARD_WIDTH));
  const maxY = Math.max(...references.map((node) => (absolutePositions.get(node.id)?.y ?? node.position.y) + NODE_CARD_HEIGHT));
  const groupRect = {
    x: minX - REFERENCE_GROUP_PADDING,
    y: minY - REFERENCE_GROUP_PADDING - 34,
    width: Math.max(320, maxX - minX + REFERENCE_GROUP_PADDING * 2),
    height: Math.max(220, maxY - minY + REFERENCE_GROUP_PADDING * 2 + 34),
  };
  const overlapsAssetNode = visualNodes
    .filter((node) => node.data.node.type !== "reference")
    .some((node) => {
      const position = absolutePositions.get(node.id) ?? node.position;
      return layoutRectOverlaps(
        {
        x: position.x,
        y: position.y,
        width: NODE_CARD_WIDTH,
        height: estimatedLayoutNodeHeight(node.data.node),
      },
      [groupRect],
      );
    });
  if (overlapsAssetNode) {
    return null;
  }
  return {
    id: REFERENCE_GROUP_ID,
    type: "referenceGroup",
    position: {
      x: groupRect.x,
      y: groupRect.y,
    },
    data: { count: references.length },
    draggable: false,
    selectable: false,
    connectable: false,
    zIndex: -1,
    style: {
      width: groupRect.width,
      height: groupRect.height,
    },
  };
}

function buildSelectedPrerequisiteIds(
  edges: Array<{ source_node_id: string; target_node_id: string }>,
  selectedId: string,
): Set<string> {
  if (!selectedId) return new Set();
  return new Set(
    edges
      .filter((edge) => edge.target_node_id === selectedId)
      .map((edge) => edge.source_node_id),
  );
}

function buildSelectedIncomingEdgeIds(
  edges: Array<{ id: string; target_node_id: string }>,
  selectedId: string,
): Set<string> {
  if (!selectedId) return new Set();
  return new Set(
    edges
      .filter((edge) => edge.target_node_id === selectedId)
      .map((edge) => edge.id),
  );
}

function visualEdgeStyle(
  edge: { id: string },
  highlightedEdgeIds: Set<string>,
) {
  return highlightedEdgeIds.has(edge.id)
    ? VISUAL_HIGHLIGHT_EDGE_STYLE
    : VISUAL_EDGE_STYLE;
}

function visibleVisualEdges(
  nodes: ProjectVisualNode[],
  edges: Array<{ id: string; source_node_id: string; target_node_id: string; relation: string }>,
  topByParentId: Record<string, string>,
) {
  const stacks = buildVariantStacks(nodes, edges);
  return edges.filter((edge) => {
    if (edge.relation !== "variant_of") return true;
    const siblings = stacks.parentToChildren.get(edge.source_node_id) ?? [];
    if (siblings.length <= 1) return true;
    const topId = topByParentId[edge.source_node_id] || siblings[0]?.id || "";
    return edge.target_node_id === topId;
  });
}

function variantStackGroupId(parentId: string): string {
  return `${VARIANT_STACK_GROUP_PREFIX}${parentId}`;
}

function visualNodeStackLayout(
  node: ProjectVisualNode,
  stacks: ReturnType<typeof buildVariantStacks>,
  variantStackGroups: Map<string, VariantStackGroupNode>,
  variantStackPositions: Map<string, XYPosition>,
  prevPositions: Map<string, XYPosition>,
  topByParentId: Record<string, string>,
): Pick<VisualFlowNode, "position" | "parentId" | "extent" | "draggable"> {
  const absolutePosition = variantStackPositions.get(node.id) ?? prevPositions.get(node.id) ?? { x: node.position_x, y: node.position_y };
  const parentId = stacks.childToParent.get(node.id);
  const groupNode = parentId ? variantStackGroups.get(parentId) : null;
  if (!groupNode) {
    return { position: absolutePosition };
  }
  const isTop = isVariantStackTopNode(node, stacks, topByParentId);
  if (isTop) {
    return {
      position: absolutePosition,
      draggable: true,
    };
  }
  return {
    position: {
      x: absolutePosition.x - groupNode.position.x,
      y: absolutePosition.y - groupNode.position.y,
    },
    parentId: groupNode.id,
    extent: "parent",
    draggable: false,
  };
}

function isVariantStackTopNode(
  node: ProjectVisualNode,
  stacks: ReturnType<typeof buildVariantStacks>,
  topByParentId: Record<string, string>,
): boolean {
  const parentId = stacks.childToParent.get(node.id);
  if (!parentId) return false;
  const siblings = stacks.parentToChildren.get(parentId) ?? [];
  if (siblings.length <= 1) return false;
  return node.id === (topByParentId[parentId] || siblings[0]?.id || "");
}

function moveVariantStackDrag(
  draggedNode: VisualCanvasNode,
  dragStopNodes: VisualCanvasNode[],
  dragStartNodes: VisualCanvasNode[],
): VisualCanvasNode[] | null {
  if (!dragStartNodes.length) {
    return null;
  }
  if (isVisualFlowNode(draggedNode)) {
    return moveVariantStackFromTopDrag(draggedNode, dragStopNodes, dragStartNodes);
  }
  if (draggedNode.type === "variantStackGroup") {
    return moveVariantStackTopFromGroupDrag(draggedNode, dragStopNodes, dragStartNodes);
  }
  return null;
}

function moveVariantStackFromTopDrag(
  draggedNode: VisualFlowNode,
  dragStopNodes: VisualCanvasNode[],
  dragStartNodes: VisualCanvasNode[],
): VisualCanvasNode[] | null {
  const stackParentId = draggedNode.data.variantStack?.isTop ? draggedNode.data.variantStack.parentId : "";
  const groupId = stackParentId ? variantStackGroupId(stackParentId) : draggedNode.parentId;
  if (!groupId) {
    return null;
  }
  const startNode = dragStartNodes.find((node): node is VisualFlowNode => node.id === draggedNode.id && isVisualFlowNode(node));
  const startGroup = dragStartNodes.find((node): node is VariantStackGroupNode => node.id === groupId && node.type === "variantStackGroup");
  const stopNode = dragStopNodes.find((node): node is VisualFlowNode => node.id === draggedNode.id && isVisualFlowNode(node));
  if (!startNode || !startGroup || !stopNode) {
    return null;
  }
  const delta = {
    x: stopNode.position.x - startNode.position.x,
    y: stopNode.position.y - startNode.position.y,
  };
  if (delta.x === 0 && delta.y === 0) {
    return null;
  }
  return mergeVisualCanvasNodes(dragStartNodes, dragStopNodes).map((node) => {
    if (node.id !== startGroup.id) return node;
    return {
      ...node,
      position: {
        x: startGroup.position.x + delta.x,
        y: startGroup.position.y + delta.y,
      },
    };
  });
}

function moveVariantStackTopFromGroupDrag(
  draggedGroup: VariantStackGroupNode,
  dragStopNodes: VisualCanvasNode[],
  dragStartNodes: VisualCanvasNode[],
): VisualCanvasNode[] | null {
  const startGroup = dragStartNodes.find((node): node is VariantStackGroupNode => node.id === draggedGroup.id && node.type === "variantStackGroup");
  const stopGroup = dragStopNodes.find((node): node is VariantStackGroupNode => node.id === draggedGroup.id && node.type === "variantStackGroup");
  if (!startGroup || !stopGroup) {
    return null;
  }
  const delta = {
    x: stopGroup.position.x - startGroup.position.x,
    y: stopGroup.position.y - startGroup.position.y,
  };
  if (delta.x === 0 && delta.y === 0) {
    return null;
  }
  const stackParentId = draggedGroup.id.startsWith(VARIANT_STACK_GROUP_PREFIX)
    ? draggedGroup.id.slice(VARIANT_STACK_GROUP_PREFIX.length)
    : "";
  const topNode = dragStartNodes.find((node): node is VisualFlowNode => (
    isVisualFlowNode(node)
    && node.data.variantStack?.parentId === stackParentId
    && node.data.variantStack.isTop
  ));
  if (!topNode) {
    return null;
  }
  return mergeVisualCanvasNodes(dragStartNodes, dragStopNodes).map((node) => {
    if (node.id !== topNode.id) return node;
    return {
      ...node,
      position: {
        x: topNode.position.x + delta.x,
        y: topNode.position.y + delta.y,
      },
    };
  });
}

function buildVariantStackGroupNodes(
  stacks: ReturnType<typeof buildVariantStacks>,
  positions: Map<string, XYPosition>,
): Map<string, VariantStackGroupNode> {
  const groupNodes = new Map<string, VariantStackGroupNode>();
  for (const [parentId, siblings] of stacks.parentToChildren) {
    if (siblings.length <= 1) continue;
    const positioned = siblings
      .map((node) => ({ node, position: positions.get(node.id) }))
      .filter((item): item is { node: ProjectVisualNode; position: XYPosition } => Boolean(item.position));
    if (positioned.length === 0) continue;
    const minX = Math.min(...positioned.map((item) => item.position.x));
    const minY = Math.min(...positioned.map((item) => item.position.y));
    const maxX = Math.max(...positioned.map((item) => item.position.x + NODE_CARD_WIDTH));
    const maxY = Math.max(...positioned.map((item) => item.position.y + estimatedLayoutNodeHeight(item.node)));
    groupNodes.set(parentId, {
      id: variantStackGroupId(parentId),
      type: "variantStackGroup",
      position: {
        x: minX - VARIANT_STACK_GROUP_INSET_X,
        y: minY,
      },
      data: { count: siblings.length },
      selectable: false,
      connectable: false,
      zIndex: 20,
      style: {
        width: maxX - minX + VARIANT_STACK_GROUP_INSET_X + VARIANT_STACK_GROUP_PADDING,
        height: maxY - minY + VARIANT_STACK_GROUP_PADDING,
      },
    });
  }
  return groupNodes;
}

function buildVariantStackPositions(
  nodes: ProjectVisualNode[],
  stacks: ReturnType<typeof buildVariantStacks>,
  currentPositions: Map<string, XYPosition>,
  topByParentId: Record<string, string>,
): Map<string, XYPosition> {
  const positions = new Map<string, XYPosition>();
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  for (const [parentId, siblings] of stacks.parentToChildren) {
    if (siblings.length <= 1) continue;
    const ordered = variantStackDisplayOrder(parentId, siblings, topByParentId);
    const origin = ordered.reduce<XYPosition | null>((current, sibling) => {
      const node = nodeById.get(sibling.id);
      const position = currentPositions.get(sibling.id) ?? (node ? { x: node.position_x, y: node.position_y } : null);
      if (!position) return current;
      if (!current) return position;
      return {
        x: Math.min(current.x, position.x),
        y: Math.min(current.y, position.y),
      };
    }, null);
    if (!origin) continue;
    ordered.forEach((sibling, index) => {
      positions.set(sibling.id, {
        x: origin.x + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
        y: origin.y + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
      });
    });
  }
  return positions;
}

function variantStackDisplayOrder(
  parentId: string,
  siblings: ProjectVisualNode[],
  topByParentId: Record<string, string>,
): ProjectVisualNode[] {
  const topId = topByParentId[parentId] || siblings[0]?.id || "";
  const topIndex = Math.max(0, siblings.findIndex((item) => item.id === topId));
  return [...siblings.slice(topIndex), ...siblings.slice(0, topIndex)];
}

function variantStackDisplayIndex(
  node: ProjectVisualNode,
  siblings: ProjectVisualNode[],
  parentId: string,
  topByParentId: Record<string, string>,
): number {
  return Math.max(0, variantStackDisplayOrder(parentId, siblings, topByParentId).findIndex((item) => item.id === node.id));
}

function buildVariantStacks(
  nodes: ProjectVisualNode[],
  edges: Array<{ source_node_id: string; target_node_id: string; relation: string }>,
) {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const boardIndexById = new Map(nodes.map((node, index) => [node.id, index]));
  const childToParent = new Map<string, string>();
  const parentToChildren = new Map<string, ProjectVisualNode[]>();
  for (const edge of edges) {
    if (edge.relation !== "variant_of") continue;
    const parent = nodeById.get(edge.source_node_id);
    const child = nodeById.get(edge.target_node_id);
    if (!parent || !child || child.type === "character") continue;
    childToParent.set(child.id, parent.id);
    parentToChildren.set(parent.id, [...(parentToChildren.get(parent.id) ?? []), child]);
  }
  for (const [parentId, children] of parentToChildren) {
    parentToChildren.set(parentId, children.sort((a, b) => compareSemanticNodes(a, b, boardIndexById)));
  }
  return { childToParent, parentToChildren };
}

function variantStackMeta(
  node: ProjectVisualNode,
  stacks: ReturnType<typeof buildVariantStacks>,
  topByParentId: Record<string, string>,
  switchTickByParentId: Record<string, number>,
): VariantStackNodeMeta | undefined {
  const parentId = stacks.childToParent.get(node.id);
  if (!parentId) return undefined;
  const siblings = stacks.parentToChildren.get(parentId) ?? [];
  if (siblings.length <= 1) return undefined;
  const topId = topByParentId[parentId] || siblings[0]?.id || "";
  const displayIndex = variantStackDisplayIndex(node, siblings, parentId, topByParentId);
  return {
    parentId,
    index: displayIndex,
    count: siblings.length,
    isTop: node.id === topId,
    switchTick: switchTickByParentId[parentId] ?? 0,
  };
}

function visualNodeZIndex(
  node: ProjectVisualNode,
  stacks: ReturnType<typeof buildVariantStacks>,
  topByParentId: Record<string, string>,
): number {
  const parentId = stacks.childToParent.get(node.id);
  if (!parentId) return 10;
  const siblings = stacks.parentToChildren.get(parentId) ?? [];
  const topId = topByParentId[parentId] || siblings[0]?.id || "";
  if (node.id === topId) return 1000;
  const displayIndex = variantStackDisplayIndex(node, siblings, parentId, topByParentId);
  return 100 + siblings.length - displayIndex;
}

function buildOrganizedNodePositions(
  nodes: ProjectVisualNode[],
  edges: Array<{ source_node_id: string; target_node_id: string; relation: string }>,
): Map<string, XYPosition> {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const boardIndexById = new Map(nodes.map((node, index) => [node.id, index]));
  const variantParentByChild = new Map<string, string>();
  const variantChildrenByParent = new Map<string, ProjectVisualNode[]>();
  const linkedParentByChild = new Map<string, string>();
  const linkedChildrenByParent = new Map<string, ProjectVisualNode[]>();

  for (const edge of edges) {
    if (!nodeById.has(edge.source_node_id) || !nodeById.has(edge.target_node_id)) {
      continue;
    }
    if (edge.relation !== "variant_of") {
      const linked = linkedLayoutPair(edge, nodeById);
      if (linked && !linkedParentByChild.has(linked.child.id)) {
        linkedParentByChild.set(linked.child.id, linked.parent.id);
        linkedChildrenByParent.set(linked.parent.id, [
          ...(linkedChildrenByParent.get(linked.parent.id) ?? []),
          linked.child,
        ]);
      }
      continue;
    }
    const child = nodeById.get(edge.target_node_id);
    if (!child) continue;
    if (child.type === "character") {
      continue;
    }
    variantParentByChild.set(edge.target_node_id, edge.source_node_id);
    variantChildrenByParent.set(edge.source_node_id, [
      ...(variantChildrenByParent.get(edge.source_node_id) ?? []),
      child,
    ]);
  }

  for (const [parentId, children] of variantChildrenByParent) {
    variantChildrenByParent.set(parentId, children.sort((a, b) => compareSemanticNodes(a, b, boardIndexById)));
  }
  for (const [parentId, children] of linkedChildrenByParent) {
    linkedChildrenByParent.set(parentId, children.sort((a, b) => compareSemanticNodes(a, b, boardIndexById)));
  }

  const rootNodes = nodes
    .filter((node) => !variantParentByChild.has(node.id))
    .filter((node) => !linkedParentByChild.has(node.id))
    .sort((a, b) => compareSemanticNodes(a, b, boardIndexById));
  const groupedRoots = new Map<VisualLayoutGroup, ProjectVisualNode[]>();
  for (const node of rootNodes) {
    const group = visualLayoutGroup(node);
    groupedRoots.set(group, [...(groupedRoots.get(group) ?? []), node]);
  }

  const positions = new Map<string, XYPosition>();
  const occupiedRects: Array<{ x: number; y: number; width: number; height: number }> = [];
  const groupCursorY = new Map<VisualLayoutGroup, number>();
  const groupOrder: VisualLayoutGroup[] = ["reference", "character", "scene", "asset"];

  for (const group of groupOrder) {
    for (const node of groupedRoots.get(group) ?? []) {
      placeVisualLayoutCluster(node, {
        group,
        positions,
        occupiedRects,
        groupCursorY,
        variantChildrenByParent,
        linkedChildrenByParent,
        boardIndexById,
      });
    }
  }

  for (const node of nodes) {
    if (!positions.has(node.id)) {
      placeVisualLayoutCluster(node, {
        group: visualLayoutGroup(node),
        positions,
        occupiedRects,
        groupCursorY,
        variantChildrenByParent,
        linkedChildrenByParent,
        boardIndexById,
      });
    }
  }
  return positions;
}

type VisualLayoutGroup = "reference" | "character" | "scene" | "asset";

function placeVisualLayoutCluster(
  node: ProjectVisualNode,
  context: {
    group: VisualLayoutGroup;
    positions: Map<string, XYPosition>;
    occupiedRects: Array<{ x: number; y: number; width: number; height: number }>;
    groupCursorY: Map<VisualLayoutGroup, number>;
    variantChildrenByParent: Map<string, ProjectVisualNode[]>;
    linkedChildrenByParent: Map<string, ProjectVisualNode[]>;
    boardIndexById: Map<string, number>;
  },
) {
  if (context.positions.has(node.id)) return;
  const baseX = visualLayoutGroupX(context.group);
  const baseY = context.groupCursorY.get(context.group) ?? 0;
  const rootPosition = reserveLayoutPosition(node, baseX, baseY, context.occupiedRects);
  context.positions.set(node.id, rootPosition);

  let clusterBottom = rootPosition.y + estimatedLayoutNodeHeight(node);
  const variantChildren = (context.variantChildrenByParent.get(node.id) ?? [])
    .sort((a, b) => compareSemanticNodes(a, b, context.boardIndexById));
  variantChildren.forEach((child, index) => {
    if (context.positions.has(child.id)) return;
    const childPosition = {
      x: rootPosition.x + NODE_LAYOUT_VARIANT_OFFSET_X + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
      y: rootPosition.y + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
    };
    context.positions.set(child.id, childPosition);
    const childBottom = childPosition.y + estimatedLayoutNodeHeight(child);
    clusterBottom = Math.max(clusterBottom, childBottom);
    context.occupiedRects.push({
      x: childPosition.x,
      y: childPosition.y,
      width: NODE_CARD_WIDTH,
      height: estimatedLayoutNodeHeight(child),
    });
  });
  const linkedChildren = (context.linkedChildrenByParent.get(node.id) ?? [])
    .sort((a, b) => compareSemanticNodes(a, b, context.boardIndexById));
  linkedChildren.forEach((child, index) => {
    if (context.positions.has(child.id)) return;
    const childPosition = reserveLayoutPosition(
      child,
      rootPosition.x + NODE_LAYOUT_VARIANT_OFFSET_X,
      rootPosition.y + index * (NODE_CARD_HEIGHT + NODE_LAYOUT_ROW_GAP),
      context.occupiedRects,
    );
    context.positions.set(child.id, childPosition);
    clusterBottom = Math.max(clusterBottom, placeLinkedLayoutDescendants(child, childPosition, context));
  });
  context.groupCursorY.set(context.group, clusterBottom + NODE_LAYOUT_ROW_GAP * 2);
}

function placeLinkedLayoutDescendants(
  node: ProjectVisualNode,
  position: XYPosition,
  context: {
    positions: Map<string, XYPosition>;
    occupiedRects: Array<{ x: number; y: number; width: number; height: number }>;
    variantChildrenByParent: Map<string, ProjectVisualNode[]>;
    linkedChildrenByParent: Map<string, ProjectVisualNode[]>;
    boardIndexById: Map<string, number>;
  },
): number {
  let clusterBottom = position.y + estimatedLayoutNodeHeight(node);
  const variantChildren = (context.variantChildrenByParent.get(node.id) ?? [])
    .sort((a, b) => compareSemanticNodes(a, b, context.boardIndexById));
  variantChildren.forEach((child, index) => {
    if (context.positions.has(child.id)) return;
    const childPosition = {
      x: position.x + NODE_LAYOUT_VARIANT_OFFSET_X + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
      y: position.y + index * NODE_LAYOUT_VARIANT_CASCADE_STEP,
    };
    context.positions.set(child.id, childPosition);
    const childBottom = childPosition.y + estimatedLayoutNodeHeight(child);
    clusterBottom = Math.max(clusterBottom, childBottom);
    context.occupiedRects.push({
      x: childPosition.x,
      y: childPosition.y,
      width: NODE_CARD_WIDTH,
      height: estimatedLayoutNodeHeight(child),
    });
  });
  const children = (context.linkedChildrenByParent.get(node.id) ?? [])
    .sort((a, b) => compareSemanticNodes(a, b, context.boardIndexById));
  children.forEach((child, index) => {
    if (context.positions.has(child.id)) return;
    const childPosition = reserveLayoutPosition(
      child,
      position.x + NODE_LAYOUT_VARIANT_OFFSET_X,
      position.y + index * (NODE_CARD_HEIGHT + NODE_LAYOUT_ROW_GAP),
      context.occupiedRects,
    );
    context.positions.set(child.id, childPosition);
    clusterBottom = Math.max(clusterBottom, placeLinkedLayoutDescendants(child, childPosition, context));
  });
  return clusterBottom;
}

function linkedLayoutPair(
  edge: { source_node_id: string; target_node_id: string; relation: string },
  nodeById: Map<string, ProjectVisualNode>,
): { parent: ProjectVisualNode; child: ProjectVisualNode } | null {
  const source = nodeById.get(edge.source_node_id);
  const target = nodeById.get(edge.target_node_id);
  if (!source || !target || source.id === target.id) return null;
  if (!["uses", "reference", "prerequisite"].includes(edge.relation)) return null;
  if (target.type === "audio" && source.type !== "audio") {
    return { parent: source, child: target };
  }
  if (source.type === "audio" && target.type !== "audio") {
    return { parent: target, child: source };
  }
  if (isLayoutAttachedAsset(target) && !isLayoutAttachedAsset(source)) {
    return { parent: source, child: target };
  }
  return null;
}

function isLayoutAttachedAsset(node: ProjectVisualNode): boolean {
  return node.type === "audio" || node.type === "video" || node.type === "animation";
}

function reserveLayoutPosition(
  node: ProjectVisualNode,
  x: number,
  preferredY: number,
  occupiedRects: Array<{ x: number; y: number; width: number; height: number }>,
): XYPosition {
  const height = estimatedLayoutNodeHeight(node);
  let y = preferredY;
  while (layoutRectOverlaps({ x, y, width: NODE_CARD_WIDTH, height }, occupiedRects)) {
    y += NODE_LAYOUT_ROW_GAP;
  }
  occupiedRects.push({ x, y, width: NODE_CARD_WIDTH, height });
  return { x, y };
}

function visualLayoutGroup(node: ProjectVisualNode): VisualLayoutGroup {
  switch (node.type) {
    case "reference":
    case "gameplay_note":
      return "reference";
    case "character":
      return "character";
    case "scene":
      return "scene";
    default:
      return "asset";
  }
}

function visualLayoutGroupX(group: VisualLayoutGroup): number {
  switch (group) {
    case "reference":
      return NODE_LAYOUT_REFERENCE_X;
    case "character":
      return NODE_LAYOUT_CHARACTER_X;
    case "scene":
      return NODE_LAYOUT_SCENE_X;
    case "asset":
      return NODE_LAYOUT_ASSET_X;
  }
}

function compareSemanticNodes(
  a: ProjectVisualNode,
  b: ProjectVisualNode,
  boardIndexById: Map<string, number>,
): number {
  const groupRank = layoutGroupRank(visualLayoutGroup(a)) - layoutGroupRank(visualLayoutGroup(b));
  if (groupRank !== 0) return groupRank;
  const typeRank = layoutTypeRank(a.type) - layoutTypeRank(b.type);
  if (typeRank !== 0) return typeRank;
  const statusRank = layoutStatusRank(a.status) - layoutStatusRank(b.status);
  if (statusRank !== 0) return statusRank;
  const timeRank = layoutTimestamp(a) - layoutTimestamp(b);
  if (timeRank !== 0) return timeRank;
  const titleRank = displayNodeText(a.title_zh, a.title).localeCompare(displayNodeText(b.title_zh, b.title));
  if (titleRank !== 0) return titleRank;
  return (boardIndexById.get(a.id) ?? 0) - (boardIndexById.get(b.id) ?? 0);
}

function layoutGroupRank(group: VisualLayoutGroup): number {
  switch (group) {
    case "reference":
      return 0;
    case "character":
      return 1;
    case "scene":
      return 2;
    case "asset":
      return 3;
  }
}

function layoutTypeRank(type: ProjectVisualNodeType): number {
  switch (type) {
    case "reference":
      return 0;
    case "gameplay_note":
      return 1;
    case "scene":
      return 2;
    case "character":
      return 3;
    case "ui_element":
      return 4;
    case "prop":
      return 5;
    case "generated_variant":
      return 6;
    case "animation":
      return 7;
    case "video":
      return 8;
    default:
      return 9;
  }
}

function layoutStatusRank(status: ProjectVisualNodeStatus): number {
  switch (status) {
    case "adopted":
      return 0;
    case "draft":
      return 1;
    case "generating":
      return 2;
    case "failed":
      return 3;
    case "rejected":
      return 4;
    default:
      return 5;
  }
}

function layoutTimestamp(node: ProjectVisualNode): number {
  for (const value of [node.result_attachment?.created_at, node.created_at, node.updated_at]) {
    const timestamp = Date.parse(value || "");
    if (Number.isFinite(timestamp)) {
      return timestamp;
    }
  }
  return Number.MAX_SAFE_INTEGER;
}

function estimatedLayoutNodeHeight(node: ProjectVisualNode): number {
  if (node.type === "reference") return NODE_CARD_HEIGHT;
  const mediaHeight = NODE_CARD_WIDTH / defaultMediaAspectRatio(node);
  const bodyHeight = node.implementation_path ? 134 : 108;
  return Math.min(352, Math.max(112, mediaHeight)) + bodyHeight;
}

function layoutRectOverlaps(
  rect: { x: number; y: number; width: number; height: number },
  occupiedRects: Array<{ x: number; y: number; width: number; height: number }>,
): boolean {
  return occupiedRects.some((occupied) => (
    rect.x < occupied.x + occupied.width + NODE_LAYOUT_COLLISION_PADDING
    && rect.x + rect.width + NODE_LAYOUT_COLLISION_PADDING > occupied.x
    && rect.y < occupied.y + occupied.height + NODE_LAYOUT_COLLISION_PADDING
    && rect.y + rect.height + NODE_LAYOUT_COLLISION_PADDING > occupied.y
  ));
}

function defaultMediaAspectRatio(node: ProjectVisualNode): number {
  if (node.type === "video") return 16 / 9;
  if (node.type === "audio") return 16 / 5;
  if (node.type === "animation") return 4 / 3;
  return 1;
}

function aspectRatioFromSize(width: number, height: number, fallback: number): number {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return fallback;
  }
  return Math.min(2.4, Math.max(0.55, width / height));
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
  if (source && type === "video") return `${source.title} video`;
  if (source && type === "audio") return `${source.title} audio`;
  return `New ${TYPE_LABELS[type]}`;
}

function buildNodePrompt(type: ProjectVisualNodeType, source: ProjectVisualNode | null): string {
  if (type === "animation") {
    return buildAnimationNodePrompt(source);
  }
  if (type === "video") {
    return buildVideoNodePrompt(source);
  }
  if (type === "audio") {
    return buildAudioNodePrompt(source);
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

function generateButtonLabel(type: ProjectVisualNodeType): string {
  if (type === "animation") return "Generate Animation";
  if (type === "video") return "Generate Video";
  if (type === "audio") return "Generate Audio";
  return "Generate Image";
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

function buildVideoNodePrompt(source: ProjectVisualNode | null): string {
  return [
    source ? `Create a production-ready video or cutscene asset for "${source.title}".` : "Create a production-ready video or cutscene asset for this visual node.",
    "Use explicit reference dependencies from connected reference nodes when available. Treat reference images as prerequisite direction, not as final output by themselves.",
    "Output expectations: video file or video cover plus handoff notes, duration, aspect ratio, source references, validation notes, and final in-repo placement path if integrated.",
    "Keep the result suitable for the current game runtime and avoid text, logo, or watermark unless the node explicitly asks for UI text.",
    source?.prompt ? `Source visual prompt: ${source.prompt}` : "",
    source?.description ? `Source description: ${source.description}` : "",
  ].filter(Boolean).join("\n");
}

function buildAudioNodePrompt(source: ProjectVisualNode | null): string {
  return [
    source ? `Create a production-ready audio asset or audio requirement for "${source.title}".` : "Create a production-ready audio asset or audio requirement for this visual node.",
    "Cover the intended sound role clearly: ambience bed, music cue, foley set, UI SFX, video audio bed, or implementation note.",
    "Output expectations: audio file or detailed audio spec, loop/one-shot intent, duration, mix priority, source references, validation notes, and final in-repo placement path if integrated.",
    "Leave space for dialogue and UI where relevant; avoid literal voiceover unless the node explicitly requests it.",
    source?.prompt ? `Source visual prompt: ${source.prompt}` : "",
    source?.description ? `Source description: ${source.description}` : "",
  ].filter(Boolean).join("\n");
}
