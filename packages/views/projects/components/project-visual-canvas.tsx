"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  applyNodeChanges,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Check, Image, Loader2, Sparkles, Wand2, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Input } from "@multica/ui/components/ui/input";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { Agent, ProjectVisualNode, ProjectVisualNodeStatus, ProjectVisualNodeType } from "@multica/core/types";
import { projectVisualBoardOptions } from "@multica/core/project-visuals";
import {
  useCreatePlanFromProjectVisualBoard,
  useGenerateProjectVisualNodeImage,
  useGenerateProjectVisualNodes,
  useUpdateProjectVisualBoard,
} from "@multica/core/project-visuals";

type VisualFlowData = {
  node: ProjectVisualNode;
  onSelect: (node: ProjectVisualNode) => void;
  onStatus: (node: ProjectVisualNode, status: ProjectVisualNodeStatus) => void;
  onGenerate: (node: ProjectVisualNode) => void;
};

type VisualFlowNode = Node<VisualFlowData, "visualNode">;

const NODE_TYPES = { visualNode: VisualNodeCard };

const TYPE_LABELS: Record<ProjectVisualNodeType, string> = {
  character: "Character",
  scene: "Scene",
  ui_element: "UI",
  prop: "Prop",
  reference: "Reference",
  gameplay_note: "Gameplay",
  generated_variant: "Variant",
};

export function ProjectVisualCanvas({ projectId }: { projectId: string }) {
  const wsId = useWorkspaceId();
  const { resolvedTheme } = useTheme();
  const { data: board } = useQuery(projectVisualBoardOptions(wsId, projectId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const updateBoard = useUpdateProjectVisualBoard(projectId);
  const generateNodes = useGenerateProjectVisualNodes(projectId);
  const generateImage = useGenerateProjectVisualNodeImage(projectId);
  const createPlan = useCreatePlanFromProjectVisualBoard(projectId);
  const [flowNodes, setFlowNodes] = useState<VisualFlowNode[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [selectedAgentId, setSelectedAgentId] = useState("");
  const [gameplayNotes, setGameplayNotes] = useState("");
  const [plannerAgentId, setPlannerAgentId] = useState("");

  const runnableAgents = useMemo(
    () => agents.filter((agent) => !agent.archived_at && agent.runtime_id),
    [agents],
  );

  const selectedNode = useMemo(
    () => board?.nodes.find((node) => node.id === selectedId) ?? null,
    [board?.nodes, selectedId],
  );

  const syncBoardToFlow = useCallback(() => {
    if (!board) return;
    setFlowNodes((prev) => {
      const prevPositions = new Map(prev.map((node) => [node.id, node.position]));
      return board.nodes.map((node) => ({
        id: node.id,
        type: "visualNode",
        position: prevPositions.get(node.id) ?? { x: node.position_x, y: node.position_y },
        data: {
          node,
          onSelect: setSelectedFromNode,
          onStatus: handleStatusChange,
          onGenerate: handleGenerateRequest,
        },
      }));
    });
  }, [board, selectedAgentId]);

  useEffect(() => {
    syncBoardToFlow();
  }, [syncBoardToFlow]);

  const setSelectedFromNode = useCallback((node: ProjectVisualNode) => {
    setSelectedId(node.id);
    setSelectedAgentId((current) => node.generation_agent_id ?? current);
  }, []);

  const saveBoard = useCallback((nodesOverride?: VisualFlowNode[]) => {
    if (!board) return;
    const latest = nodesOverride ?? flowNodes;
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
          description: node.description,
          prompt: node.prompt,
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
    setFlowNodes((prev) => applyNodeChanges(changes, prev) as VisualFlowNode[]);
  }, []);

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
        description: item.description,
        prompt: item.prompt,
        position_x: item.position_x,
        position_y: item.position_y,
        source_refs: item.source_refs,
      })),
    });
  }

  function handleGenerateRequest(node: ProjectVisualNode) {
    setSelectedFromNode(node);
    if (!selectedAgentId) {
      toast.message("Select an art agent first.");
      return;
    }
    generateImage.mutate(
      { nodeId: node.id, agent_id: selectedAgentId },
      {
        onSuccess: () => toast.success("Generation task queued"),
        onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to queue generation"),
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
        description: node.id === selectedNode.id ? patch.description ?? node.description : node.description,
        prompt: node.id === selectedNode.id ? patch.prompt ?? node.prompt : node.prompt,
        position_x: node.position_x,
        position_y: node.position_y,
        source_refs: node.source_refs,
      })),
    });
  }

  const adoptedCount = board?.nodes.filter((node) => node.status === "adopted").length ?? 0;

  return (
    <div className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
          <Button
            size="sm"
            variant="outline"
            disabled={generateNodes.isPending}
            onClick={() => generateNodes.mutate(undefined, {
              onSuccess: () => toast.success("Visual extraction task queued"),
              onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to generate nodes"),
            })}
          >
            {generateNodes.isPending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : <Sparkles className="mr-1.5 h-3.5 w-3.5" />}
            Generate Nodes from Wiki
          </Button>
          <Button size="sm" variant="ghost" disabled={!board || updateBoard.isPending} onClick={() => saveBoard()}>
            <Check className="mr-1.5 h-3.5 w-3.5" />
            Save
          </Button>
          <div className="ml-auto flex items-center gap-2">
            <select
              className="h-8 rounded-md border bg-background px-2 text-xs"
              value={plannerAgentId}
              onChange={(event) => setPlannerAgentId(event.target.value)}
            >
              <option value="">Planner agent</option>
              {runnableAgents.map((agent) => (
                <option key={agent.id} value={agent.id}>{agent.name}</option>
              ))}
            </select>
            <Button
              size="sm"
              disabled={!plannerAgentId || adoptedCount === 0 || createPlan.isPending}
              onClick={() => createPlan.mutate(
                { planner_agent_id: plannerAgentId, gameplay_notes: gameplayNotes },
                {
                  onSuccess: (plan) => toast.success(`Plan created: ${plan.title}`),
                  onError: (err) => toast.error(err instanceof Error ? err.message : "Failed to create plan"),
                },
              )}
            >
              <Wand2 className="mr-1.5 h-3.5 w-3.5" />
              Create Plan from Adopted
            </Button>
          </div>
        </div>
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
            onNodesChange={onNodesChange}
            onNodeDragStop={(_, __, nodes) => saveBoard(nodes as VisualFlowNode[])}
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
            {runnableAgents.map((agent: Agent) => (
              <option key={agent.id} value={agent.id}>{agent.name}</option>
            ))}
          </select>
          {selectedNode ? (
            <>
              <Input value={selectedNode.title} onChange={(event) => updateSelectedNode({ title: event.target.value })} />
              <select
                className="h-9 rounded-md border bg-background px-2 text-sm"
                value={selectedNode.type}
                onChange={(event) => updateSelectedNode({ type: event.target.value as ProjectVisualNodeType })}
              >
                {Object.entries(TYPE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </select>
              <Textarea
                className="min-h-24 resize-none"
                value={selectedNode.description}
                onChange={(event) => updateSelectedNode({ description: event.target.value })}
              />
              <Textarea
                className="min-h-40 resize-none font-mono text-xs"
                value={selectedNode.prompt}
                onChange={(event) => updateSelectedNode({ prompt: event.target.value })}
              />
              <div className="grid grid-cols-2 gap-2">
                <Button variant="outline" size="sm" onClick={() => handleStatusChange(selectedNode, "adopted")}>Adopt</Button>
                <Button variant="outline" size="sm" onClick={() => handleStatusChange(selectedNode, "rejected")}>Reject</Button>
              </div>
              <Button size="sm" disabled={!selectedAgentId || generateImage.isPending} onClick={() => handleGenerateRequest(selectedNode)}>
                <Image className="mr-1.5 h-3.5 w-3.5" />
                Generate Image
              </Button>
            </>
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
    </div>
  );
}

function VisualNodeCard({ data }: NodeProps<VisualFlowNode>) {
  const node = data.node;
  const imageUrl = node.result_attachment?.download_url;
  return (
    <button
      type="button"
      className="w-64 overflow-hidden rounded-md border bg-card text-left shadow-sm"
      onClick={() => data.onSelect(node)}
    >
      <Handle type="target" position={Position.Left} />
      <div className="h-32 bg-muted">
        {imageUrl ? (
          <img src={imageUrl} alt="" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            {node.status === "generating" ? <Loader2 className="h-6 w-6 animate-spin" /> : <Image className="h-6 w-6" />}
          </div>
        )}
      </div>
      <div className="space-y-2 p-3">
        <div className="flex items-center gap-2">
          <span className="rounded-sm bg-muted px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{TYPE_LABELS[node.type]}</span>
          <span className={cn("ml-auto text-[10px] uppercase", node.status === "failed" ? "text-destructive" : "text-muted-foreground")}>{node.status}</span>
        </div>
        <div className="line-clamp-1 text-sm font-medium">{node.title}</div>
        <div className="line-clamp-2 text-xs text-muted-foreground">{node.prompt || node.description}</div>
        <div className="flex gap-1">
          <Button type="button" size="icon" variant="ghost" className="h-7 w-7" aria-label={`Adopt ${node.title}`} onClick={(event) => { event.stopPropagation(); data.onStatus(node, "adopted"); }}>
            <Check className="h-3.5 w-3.5" />
          </Button>
          <Button type="button" size="icon" variant="ghost" className="h-7 w-7" aria-label={`Reject ${node.title}`} onClick={(event) => { event.stopPropagation(); data.onStatus(node, "rejected"); }}>
            <X className="h-3.5 w-3.5" />
          </Button>
          <Button type="button" size="icon" variant="ghost" className="ml-auto h-7 w-7" aria-label={`Generate visual for ${node.title}`} onClick={(event) => { event.stopPropagation(); data.onGenerate(node); }}>
            <Wand2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
      <Handle type="source" position={Position.Right} />
    </button>
  );
}
