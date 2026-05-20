"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  applyNodeChanges,
} from "@xyflow/react";
import type { NodeChange } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { ArrowLeft, Bot, Copy, GitBranch, Loader2, Plus, Save, Trash2, Upload, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useDeletePipeline, useDuplicatePipeline, useUpdatePipeline } from "@multica/core/pipelines/mutations";
import { pipelineDetailOptions } from "@multica/core/pipelines/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { Pipeline, PipelineNodeType, UpsertPipelineNodeRequest } from "@multica/core/types";
import type { Connection, Edge, Node, NodeProps, ReactFlowInstance } from "@xyflow/react";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { validatePipelineDraft } from "../validation";
import { PipelineImportDialog } from "./pipeline-import-dialog";

// ─── Types ────────────────────────────────────────────────────────────────────

interface PipelineDraft {
  name: string;
  description: string;
  nodes: UpsertPipelineNodeRequest[];
}

type PipelineFlowNodeData = {
  key: string;
  title: string;
  nodeType: PipelineNodeType;
  agentName: string;
  dependencyCount: number;
};

type PipelineFlowNodeModel = Node<PipelineFlowNodeData, "pipeline">;

type GraphContextMenu = { screenX: number; screenY: number; flowX: number; flowY: number } | null;

const NODE_TYPES_LIST: PipelineNodeType[] = ["issue", "manual", "check", "spec_review", "code_review"];
const FLOW_NODE_TYPES = { pipeline: PipelineFlowNode };

const NODE_ACCENT: Record<PipelineNodeType, string> = {
  issue: "bg-amber-400",
  manual: "bg-sky-500",
  check: "bg-emerald-500",
  spec_review: "bg-violet-500",
  code_review: "bg-rose-500",
};

// ─── Page ─────────────────────────────────────────────────────────────────────

export function PipelineDetailPage({ pipelineId: explicitPipelineId }: { pipelineId?: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const { resolvedTheme } = useTheme();
  const pipelineId =
    explicitPipelineId ??
    decodeURIComponent(nav.pathname.match(/\/pipelines\/([^/]+)$/)?.[1] ?? "");

  const { data: pipeline } = useQuery(pipelineDetailOptions(wsId, pipelineId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const updatePipeline = useUpdatePipeline(wsId, pipelineId);
  const deletePipeline = useDeletePipeline(wsId, pipelineId);
  const duplicatePipeline = useDuplicatePipeline(wsId, pipelineId);

  const [draft, setDraft] = useState<PipelineDraft | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [graphContextMenu, setGraphContextMenu] = useState<GraphContextMenu>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [selectedNodeFlowId, setSelectedNodeFlowId] = useState<string | null>(null);
  const [rfNodes, setRfNodes] = useState<PipelineFlowNodeModel[]>([]);
  const [rfEdges, setRfEdges] = useState<Edge[]>([]);

  useEffect(() => {
    if (pipeline) setDraft(pipelineToDraft(pipeline));
  }, [pipeline]);

  const activeAgents = useMemo(() => agents.filter((a) => !a.archived_at), [agents]);
  const agentsById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);

  // Sync ReactFlow nodes/edges from draft, preserving live positions during drag.
  useEffect(() => {
    if (!draft) return;
    const { nodes: newNodes, edges: newEdges } = getFlowElements(draft.nodes, agentsById);
    setRfNodes((prev) => {
      const posMap = new Map(prev.map((n) => [n.id, n.position]));
      return newNodes.map((n) => ({ ...n, position: posMap.get(n.id) ?? n.position }));
    });
    setRfEdges(newEdges);
  }, [draft, agentsById]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setRfNodes((prev) => applyNodeChanges(changes, prev) as PipelineFlowNodeModel[]);
  }, []);

  if (!pipeline || !draft) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const canEdit = pipeline.editable !== false && pipeline.is_system !== true;
  const canDelete = pipeline.deletable !== false && pipeline.is_system !== true;

  const selectedNodeIndex = draft.nodes.findIndex(
    (n, i) => getFlowNodeId(n, i) === selectedNodeFlowId,
  );
  const selectedNode = selectedNodeIndex >= 0 ? (draft.nodes[selectedNodeIndex] ?? null) : null;

  // ── Mutations ──

  const patchDraft = (patch: Partial<PipelineDraft>) => {
    if (!canEdit) return;
    setDraft((cur) => (cur ? { ...cur, ...patch } : cur));
  };

  const changeNode = (index: number, patch: Partial<UpsertPipelineNodeRequest>) => {
    if (!canEdit) return;
    patchDraft({ nodes: draft.nodes.map((n, i) => (i === index ? { ...n, ...patch } : n)) });
  };

  const changeSelectedNode = (patch: Partial<UpsertPipelineNodeRequest>) => {
    if (selectedNodeIndex < 0) return;
    if (patch.key !== undefined) {
      setSelectedNodeFlowId(patch.key.trim() || `draft-node-${selectedNodeIndex}`);
    }
    changeNode(selectedNodeIndex, patch);
  };

  const changeNodeByFlowId = (flowId: string, patch: Partial<UpsertPipelineNodeRequest>) => {
    if (!canEdit) return;
    setDraft((cur) =>
      cur
        ? {
            ...cur,
            nodes: cur.nodes.map((n, i) => (getFlowNodeId(n, i) === flowId ? { ...n, ...patch } : n)),
          }
        : cur,
    );
  };

  const connectNodes = (connection: Connection) => {
    if (!canEdit || !connection.source || !connection.target || connection.source === connection.target) return;
    patchDraft({
      nodes: draft.nodes.map((node, index) => {
        if (getFlowNodeId(node, index) !== connection.target) return node;
        const deps = node.depends_on_node_keys ?? [];
        return deps.includes(connection.source) ? node : { ...node, depends_on_node_keys: [...deps, connection.source] };
      }),
    });
  };

  const removeEdges = (edges: Edge[]) => {
    if (!canEdit) return;
    const removals = new Map<string, Set<string>>();
    for (const edge of edges) {
      if (!edge.source || !edge.target) continue;
      const deps = removals.get(edge.target) ?? new Set<string>();
      deps.add(edge.source);
      removals.set(edge.target, deps);
    }
    patchDraft({
      nodes: draft.nodes.map((node, index) => {
        const depsToRemove = removals.get(getFlowNodeId(node, index));
        if (!depsToRemove) return node;
        return { ...node, depends_on_node_keys: (node.depends_on_node_keys ?? []).filter((k) => !depsToRemove.has(k)) };
      }),
    });
  };

  const addNode = (nodeType: PipelineNodeType = "issue", position?: { x: number; y: number }) => {
    if (!canEdit) return;
    const nextIndex = draft.nodes.length;
    const key = nextKey("node", draft.nodes.map((n) => n.key));
    patchDraft({
      nodes: [
        ...draft.nodes,
        {
          key,
          type: nodeType,
          title: `${pipelineNodeTypeLabel(nodeType)} Node`,
          description: "",
          agent_id: null,
          repos: [],
          depends_on_node_keys: [],
          position_x: position ? Math.max(0, Math.round(position.x)) : (nextIndex % 3) * 280,
          position_y: position ? Math.max(0, Math.round(position.y)) : Math.floor(nextIndex / 3) * 200,
        },
      ],
    });
    setSelectedNodeFlowId(key);
  };

  const deleteSelectedNode = () => {
    if (selectedNodeIndex < 0) return;
    patchDraft({ nodes: draft.nodes.filter((_, i) => i !== selectedNodeIndex) });
    setSelectedNodeFlowId(null);
  };

  const openContextMenu = (event: MouseEvent | React.MouseEvent<Element, MouseEvent>) => {
    event.preventDefault();
    if (!canEdit) return;
    const pos = flowInstance?.screenToFlowPosition({ x: event.clientX, y: event.clientY }) ?? { x: 0, y: 0 };
    setGraphContextMenu({ screenX: event.clientX, screenY: event.clientY, flowX: pos.x, flowY: pos.y });
  };

  const addNodeFromContextMenu = (nodeType: PipelineNodeType) => {
    if (!graphContextMenu) return;
    addNode(nodeType, { x: graphContextMenu.flowX, y: graphContextMenu.flowY });
    setGraphContextMenu(null);
  };

  const save = async () => {
    if (!canEdit) { toast.error("Built-in pipelines cannot be edited. Duplicate it to customize."); return; }
    const errors = validatePipelineDraft(draft);
    if (errors.length > 0) { toast.error(errors[0]); return; }
    try {
      await updatePipeline.mutateAsync({
        name: draft.name.trim(),
        description: draft.description.trim(),
        nodes: draft.nodes.map((node) => ({
          key: node.key.trim(),
          type: node.type ?? "issue",
          title: node.title.trim(),
          description: node.description?.trim() ?? "",
          agent_id: node.agent_id || null,
          repo: null,
          repos: normalizeNodeRepoKeys(node),
          depends_on_node_keys: node.depends_on_node_keys ?? [],
          position_x: node.position_x ?? 0,
          position_y: node.position_y ?? 0,
        })),
      });
      toast.success("Pipeline saved");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save pipeline");
    }
  };

  const archive = async () => {
    if (!canDelete) { toast.error("Built-in pipelines cannot be archived."); return; }
    if (!window.confirm("Archive this pipeline? Existing run history is preserved.")) return;
    try {
      await deletePipeline.mutateAsync();
      toast.success("Pipeline archived");
      nav.push(paths.pipelines());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to archive pipeline");
    }
  };

  const duplicate = async () => {
    try {
      const copy = await duplicatePipeline.mutateAsync({});
      toast.success("Pipeline duplicated");
      nav.push(paths.pipelineDetail(copy.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to duplicate pipeline");
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
      {/* ── Header ── */}
      <PageHeader>
        <div className="flex w-full items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => nav.push(paths.pipelines())}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-sm font-semibold">{draft.name || "Pipeline"}</h1>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              {pipeline.is_system && <Badge variant="secondary" className="text-[9px]">Built-in</Badge>}
              <span>{draft.nodes.length} nodes</span>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button variant="ghost" size="sm" disabled={!canEdit} onClick={() => setImportOpen(true)}>
              <Upload className="mr-1.5 h-3.5 w-3.5" />
              Import YAML
            </Button>
            <Button variant="ghost" size="sm" disabled={duplicatePipeline.isPending} onClick={duplicate}>
              <Copy className="mr-1.5 h-3.5 w-3.5" />
              Duplicate
            </Button>
            <Button variant="ghost" size="sm" disabled={!canDelete || deletePipeline.isPending} onClick={archive}>
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Archive
            </Button>
            <Button size="sm" disabled={!canEdit || updatePipeline.isPending} onClick={save}>
              <Save className="mr-1.5 h-3.5 w-3.5" />
              Save
            </Button>
          </div>
        </div>
      </PageHeader>

      {/* ── Body: canvas + properties panel ── */}
      <div className="flex flex-1 overflow-hidden">

        {/* ── Canvas ── */}
        <div className="relative flex-1 overflow-hidden">
          <ReactFlow
            nodes={rfNodes}
            edges={rfEdges}
            nodeTypes={FLOW_NODE_TYPES}
            onInit={setFlowInstance}
            onNodesChange={onNodesChange}
            fitView
            fitViewOptions={{ padding: 0.2 }}
            colorMode={(resolvedTheme as "dark" | "light") ?? "light"}
            nodesDraggable={canEdit}
            nodesConnectable={canEdit}
            onConnect={connectNodes}
            onEdgesDelete={removeEdges}
            onNodeClick={(_, node) => setSelectedNodeFlowId(node.id)}
            onPaneClick={() => {
              setSelectedNodeFlowId(null);
              setGraphContextMenu(null);
            }}
            onPaneContextMenu={openContextMenu}
            onMoveStart={() => setGraphContextMenu(null)}
            onNodeDragStop={(_, node) =>
              changeNodeByFlowId(node.id, {
                position_x: Math.round(node.position.x),
                position_y: Math.round(node.position.y),
              })
            }
            proOptions={{ hideAttribution: true }}
            style={{ width: "100%", height: "100%" }}
          >
            <Background color="hsl(var(--muted-foreground) / 0.2)" gap={20} size={1.2} />
            <Controls showInteractive={false} />
          </ReactFlow>

          {/* Right-click context menu */}
          {canEdit && graphContextMenu && (
            <div
              className="fixed z-50 min-w-44 overflow-hidden rounded-lg border bg-popover p-1 shadow-lg"
              style={{ left: graphContextMenu.screenX, top: graphContextMenu.screenY }}
              onContextMenu={(e) => e.preventDefault()}
            >
              <div className="px-2 py-1 text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                Add node
              </div>
              {NODE_TYPES_LIST.map((type) => (
                <button
                  key={type}
                  type="button"
                  className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-muted"
                  onClick={() => addNodeFromContextMenu(type)}
                >
                  <span className={cn("h-2 w-2 shrink-0 rounded-full", NODE_ACCENT[type])} />
                  {pipelineNodeTypeLabel(type)}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* ── Properties panel ── */}
        <PipelinePropertiesPanel
          pipeline={pipeline}
          draft={draft}
          selectedNode={selectedNode}
          activeAgents={activeAgents}
          agentsById={agentsById}
          canEdit={canEdit}
          onAddNode={() => addNode()}
          onChangeNode={changeSelectedNode}
          onDeleteNode={deleteSelectedNode}
          onPatchDraft={patchDraft}
          onDeselect={() => setSelectedNodeFlowId(null)}
        />
      </div>

      <PipelineImportDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        pipelineId={canEdit ? pipeline.id : undefined}
      />
    </div>
  );
}

// ─── Properties panel ─────────────────────────────────────────────────────────

function PipelinePropertiesPanel({
  pipeline,
  draft,
  selectedNode,
  activeAgents,
  agentsById,
  canEdit,
  onAddNode,
  onChangeNode,
  onDeleteNode,
  onPatchDraft,
  onDeselect,
}: {
  pipeline: Pipeline;
  draft: PipelineDraft;
  selectedNode: UpsertPipelineNodeRequest | null;
  activeAgents: Array<{ id: string; name: string }>;
  agentsById: Map<string, { name?: string }>;
  canEdit: boolean;
  onAddNode: () => void;
  onChangeNode: (patch: Partial<UpsertPipelineNodeRequest>) => void;
  onDeleteNode: () => void;
  onPatchDraft: (patch: Partial<PipelineDraft>) => void;
  onDeselect: () => void;
}) {
  // ── Pipeline overview (no selection) ──
  if (!selectedNode) {
    return (
      <aside className="flex w-[300px] shrink-0 flex-col border-l bg-background">
        <div className="flex h-11 shrink-0 items-center border-b px-4">
          <span className="text-sm font-semibold">Pipeline</span>
        </div>
        <div className="flex-1 overflow-y-auto">
          <div className="space-y-4 p-4">
            <div className="space-y-1.5">
              <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                Name
              </div>
              <Input
                value={draft.name}
                disabled={!canEdit}
                placeholder="Pipeline name"
                onChange={(e) => onPatchDraft({ name: e.target.value })}
              />
            </div>

            <div className="space-y-1.5">
              <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                Description
              </div>
              <Textarea
                value={draft.description}
                disabled={!canEdit}
                placeholder="What does this pipeline do?"
                className="min-h-[4.5rem] resize-none text-sm"
                onChange={(e) => onPatchDraft({ description: e.target.value })}
              />
            </div>

            <div className="rounded-lg border bg-muted/30 px-3 py-2.5">
              <div className="text-xs font-medium">
                {draft.nodes.length} {draft.nodes.length === 1 ? "node" : "nodes"}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                {pipeline.is_system
                  ? "Built-in — duplicate to customize"
                  : "Click a node to view and edit its properties"}
              </div>
            </div>

            {/* Node type breakdown */}
            {draft.nodes.length > 0 && (
              <div className="space-y-1.5">
                <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                  Nodes
                </div>
                <div className="space-y-1">
                  {draft.nodes.map((node, i) => (
                    <div
                      key={`${node.key}-${i}`}
                      className="flex items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted/50"
                    >
                      <span className={cn("h-2 w-2 shrink-0 rounded-full", NODE_ACCENT[node.type ?? "issue"])} />
                      <span className="min-w-0 flex-1 truncate font-medium">{node.title || node.key}</span>
                      <span className="shrink-0 font-mono text-muted-foreground/60">{node.key}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>

        {canEdit && (
          <div className="shrink-0 border-t p-3">
            <Button variant="outline" size="sm" className="w-full" onClick={onAddNode}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Add Node
            </Button>
          </div>
        )}
      </aside>
    );
  }

  // ── Node properties (node selected) ──
  const agent = selectedNode.agent_id ? agentsById.get(selectedNode.agent_id) : undefined;
  const nodeType = selectedNode.type ?? "issue";

  return (
    <aside className="flex w-[300px] shrink-0 flex-col border-l bg-background">
      {/* Panel header */}
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b px-4">
        <div className="flex min-w-0 items-center gap-2">
          <span className={cn("h-2.5 w-2.5 shrink-0 rounded-full", NODE_ACCENT[nodeType])} />
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">
              {selectedNode.title || selectedNode.key || "Node"}
            </div>
            <div className="text-[10px] text-muted-foreground">{pipelineNodeTypeLabel(nodeType)}</div>
          </div>
        </div>
        <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" title="Deselect" onClick={onDeselect}>
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* Scrollable fields */}
      <div className="flex-1 overflow-y-auto">
        <div className="space-y-4 p-4">
          {/* Key + Type row */}
          <div className="grid grid-cols-[1fr_auto] gap-2">
            <div className="space-y-1.5">
              <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                Key
              </div>
              <Input
                value={selectedNode.key}
                disabled={!canEdit}
                placeholder="node-key"
                className="font-mono text-xs"
                onChange={(e) => onChangeNode({ key: e.target.value })}
              />
            </div>
            <div className="space-y-1.5">
              <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
                Type
              </div>
              <Select
                value={nodeType}
                disabled={!canEdit}
                onValueChange={(v) => onChangeNode({ type: v as PipelineNodeType })}
              >
                <SelectTrigger className="w-28">
                  <span className="min-w-0 flex-1 truncate text-left text-xs">
                    {pipelineNodeTypeLabel(nodeType)}
                  </span>
                </SelectTrigger>
                <SelectContent>
                  {NODE_TYPES_LIST.map((t) => (
                    <SelectItem key={t} value={t}>{pipelineNodeTypeLabel(t)}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {/* Title */}
          <div className="space-y-1.5">
            <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
              Title
            </div>
            <Input
              value={selectedNode.title}
              disabled={!canEdit}
              placeholder="Node title"
              onChange={(e) => onChangeNode({ title: e.target.value })}
            />
          </div>

          {/* Agent */}
          <div className="space-y-1.5">
            <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
              Agent
            </div>
            <Select
              value={selectedNode.agent_id || "none"}
              disabled={!canEdit}
              onValueChange={(v) => onChangeNode({ agent_id: v === "none" ? null : v })}
            >
              <SelectTrigger className="w-full">
                <span className="min-w-0 flex-1 truncate text-left">{agent?.name ?? "Bind later"}</span>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">Bind later</SelectItem>
                {activeAgents.map((a) => (
                  <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Description */}
          <div className="space-y-1.5">
            <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
              Description
            </div>
            <Textarea
              value={selectedNode.description ?? ""}
              disabled={!canEdit}
              placeholder="Node issue description"
              className="min-h-[4.5rem] resize-none text-sm"
              onChange={(e) => onChangeNode({ description: e.target.value })}
            />
          </div>

          {/* Target repos */}
          <div className="space-y-1.5">
            <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
              Target repos
            </div>
            <Input
              value={normalizeNodeRepoKeys(selectedNode).join(", ")}
              disabled={!canEdit}
              placeholder="multica, upstream"
              onChange={(e) => onChangeNode({ repo: null, repos: splitRepoAliasInput(e.target.value) })}
            />
            <div className="text-[10px] text-muted-foreground/50">
              Comma-separated aliases — resolved from the project at run time.
            </div>
          </div>

          {/* Dependencies */}
          <div className="space-y-1.5">
            <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/60">
              Depends on
            </div>
            {draft.nodes.filter((n) => n.key !== selectedNode.key).length === 0 ? (
              <div className="text-xs text-muted-foreground/50">No other nodes yet.</div>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {draft.nodes
                  .filter((candidate) => candidate.key !== selectedNode.key)
                  .map((candidate) => {
                    const checked = (selectedNode.depends_on_node_keys ?? []).includes(candidate.key);
                    return (
                      <label
                        key={candidate.key}
                        className="inline-flex cursor-pointer items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-xs hover:bg-muted/50"
                      >
                        <Checkbox
                          checked={checked}
                          disabled={!canEdit}
                          onCheckedChange={(v) =>
                            onChangeNode({
                              depends_on_node_keys: toggleValue(
                                selectedNode.depends_on_node_keys ?? [],
                                candidate.key,
                                v === true,
                              ),
                            })
                          }
                        />
                        <span className="font-mono">{candidate.key}</span>
                      </label>
                    );
                  })}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Footer: delete */}
      {canEdit && (
        <div className="shrink-0 border-t p-3">
          <Button
            variant="ghost"
            size="sm"
            className="w-full text-destructive hover:bg-destructive/10 hover:text-destructive"
            disabled={draft.nodes.length <= 1}
            onClick={onDeleteNode}
          >
            <Trash2 className="mr-1.5 h-3.5 w-3.5" />
            Remove Node
          </Button>
        </div>
      )}
    </aside>
  );
}

// ─── Flow node card ───────────────────────────────────────────────────────────

function PipelineFlowNode({ data, selected }: NodeProps<PipelineFlowNodeModel>) {
  return (
    <div
      className={cn(
        "w-56 overflow-hidden rounded-lg border bg-card shadow-sm transition-all duration-150",
        selected
          ? "border-primary shadow-[0_0_0_3px_hsl(var(--primary)/0.2),0_4px_16px_hsl(var(--primary)/0.1)]"
          : "border-border hover:border-muted-foreground/40 hover:shadow-md",
      )}
    >
      {/* Type accent strip */}
      <div className={cn("h-0.5 w-full", NODE_ACCENT[data.nodeType])} />

      <Handle
        type="target"
        position={Position.Left}
        className="!h-3 !w-3 !border-2 !border-background !bg-muted-foreground/50"
      />

      <div className="px-3 py-2.5">
        {/* Title row */}
        <div className="flex items-start gap-2">
          <span className={cn("mt-1 h-2 w-2 shrink-0 rounded-full", NODE_ACCENT[data.nodeType])} />
          <div className="min-w-0">
            <div className="truncate text-[13px] font-semibold leading-tight">
              {data.title || data.key || "Node"}
            </div>
            <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground/70">
              {data.key}
            </div>
          </div>
        </div>

        {/* Agent */}
        <div className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <Bot className="h-3 w-3 shrink-0" />
          <span className="truncate">{data.agentName}</span>
        </div>

        {/* Type chip + dep count */}
        <div className="mt-2 flex items-center gap-2">
          <span className="rounded-sm bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
            {pipelineNodeTypeLabel(data.nodeType)}
          </span>
          {data.dependencyCount > 0 && (
            <span className="flex items-center gap-0.5 text-[10px] text-muted-foreground/70">
              <GitBranch className="h-2.5 w-2.5" />
              {data.dependencyCount}
            </span>
          )}
        </div>
      </div>

      <Handle
        type="source"
        position={Position.Right}
        className="!h-3 !w-3 !border-2 !border-background !bg-muted-foreground/50"
      />
    </div>
  );
}

// ─── Flow elements ─────────────────────────────────────────────────────────────

function getFlowElements(
  nodes: UpsertPipelineNodeRequest[],
  agentsById: Map<string, { name?: string }>,
): { nodes: PipelineFlowNodeModel[]; edges: Edge[] } {
  const nodeIdByKey = new Map<string, string>();
  nodes.forEach((node, index) => {
    const key = node.key.trim();
    if (key && !nodeIdByKey.has(key)) nodeIdByKey.set(key, getFlowNodeId(node, index));
  });

  return {
    nodes: nodes.map((node, index) => {
      const agent = node.agent_id ? agentsById.get(node.agent_id) : undefined;
      return {
        id: getFlowNodeId(node, index),
        type: "pipeline",
        position: { x: node.position_x ?? 0, y: node.position_y ?? 0 },
        data: {
          key: node.key,
          title: node.title,
          nodeType: node.type ?? "issue",
          agentName: agent?.name ?? "Bind later",
          dependencyCount: (node.depends_on_node_keys ?? []).length,
        },
      };
    }),
    edges: nodes.flatMap((node, index) => {
      const target = getFlowNodeId(node, index);
      return (node.depends_on_node_keys ?? []).flatMap((depKey) => {
        const source = nodeIdByKey.get(depKey.trim());
        if (!source) return [];
        return {
          id: `${source}->${target}`,
          source,
          target,
          type: "smoothstep",
          label: "blocked",
          labelStyle: { fill: "#ffffff", fontSize: 10, fontWeight: 600 },
          labelBgStyle: { fill: "#38c878", fillOpacity: 1 },
          labelBgPadding: [6, 3] as [number, number],
          labelBgBorderRadius: 5,
          style: {
            stroke: "#38c878",
            strokeDasharray: "4 6",
            strokeLinecap: "round",
            strokeWidth: 1.5,
          },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#38c878" },
        };
      });
    }),
  };
}

// ─── Utilities ────────────────────────────────────────────────────────────────

function pipelineToDraft(pipeline: Pipeline): PipelineDraft {
  return {
    name: pipeline.name,
    description: pipeline.description,
    nodes: pipeline.nodes.map((node) => ({
      key: node.key,
      type: node.type,
      title: node.title,
      description: node.description,
      agent_id: node.agent_id,
      repo: node.repo,
      repos: normalizeNodeRepoKeys(node),
      depends_on_node_keys: node.depends_on_node_keys,
      position_x: node.position_x,
      position_y: node.position_y,
    })),
  };
}

function getFlowNodeId(node: UpsertPipelineNodeRequest, index: number) {
  return node.key.trim() || `draft-node-${index}`;
}

function normalizeNodeRepoKeys(node: Pick<UpsertPipelineNodeRequest, "repo" | "repos">) {
  const keys = new Set<string>();
  const repo = node.repo?.trim();
  if (repo) keys.add(repo);
  for (const item of node.repos ?? []) {
    const key = item.trim();
    if (key) keys.add(key);
  }
  return [...keys];
}

function splitRepoAliasInput(value: string) {
  return value.split(",").map((s) => s.trim()).filter(Boolean);
}

function pipelineNodeTypeLabel(nodeType: PipelineNodeType) {
  switch (nodeType) {
    case "manual": return "Manual";
    case "check": return "Check";
    case "spec_review": return "Spec review";
    case "code_review": return "Code review";
    default: return "Issue";
  }
}

function nextKey(prefix: string, existing: string[]) {
  let index = existing.length + 1;
  let key = `${prefix}-${index}`;
  while (existing.includes(key)) { index++; key = `${prefix}-${index}`; }
  return key;
}

function toggleValue(values: string[], value: string, checked: boolean) {
  if (checked) return values.includes(value) ? values : [...values, value];
  return values.filter((c) => c !== value);
}
