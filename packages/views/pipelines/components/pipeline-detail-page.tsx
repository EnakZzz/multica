"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  ArrowLeft,
  Gauge,
  GitBranch,
  PanelRightClose,
  PanelRightOpen,
  Plus,
  Save,
  Trash2,
  Upload,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useDeletePipeline,
  useUpdatePipeline,
} from "@multica/core/pipelines/mutations";
import { pipelineDetailOptions } from "@multica/core/pipelines/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import type {
  Pipeline,
  PipelineNodeType,
  UpsertPipelineNodeRequest,
} from "@multica/core/types";
import type { Connection, Edge, Node, NodeProps, ReactFlowInstance } from "@xyflow/react";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { validatePipelineDraft } from "../validation";
import { PipelineImportDialog } from "./pipeline-import-dialog";

interface PipelineDraft {
  name: string;
  description: string;
  nodes: UpsertPipelineNodeRequest[];
}

const nodeTypes: PipelineNodeType[] = ["issue", "manual", "check"];
const flowNodeTypes = { pipeline: PipelineFlowNode };

type PipelineFlowNodeData = {
  key: string;
  title: string;
  nodeType: PipelineNodeType;
  agentName: string;
  dependencyCount: number;
  accentClass: string;
  progressClass: string;
  progressPercent: number;
};

type PipelineFlowNodeModel = Node<PipelineFlowNodeData, "pipeline">;

type GraphContextMenu = {
  screenX: number;
  screenY: number;
  flowX: number;
  flowY: number;
} | null;

export function PipelineDetailPage({ pipelineId: explicitPipelineId }: { pipelineId?: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const { resolvedTheme } = useTheme();
  const pipelineId = explicitPipelineId ?? decodeURIComponent(nav.pathname.match(/\/pipelines\/([^/]+)$/)?.[1] ?? "");
  const { data: pipeline } = useQuery(pipelineDetailOptions(wsId, pipelineId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const updatePipeline = useUpdatePipeline(wsId, pipelineId);
  const deletePipeline = useDeletePipeline(wsId, pipelineId);
  const [draft, setDraft] = useState<PipelineDraft | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [graphContextMenu, setGraphContextMenu] = useState<GraphContextMenu>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [propertiesDockOpen, setPropertiesDockOpen] = useState(true);

  useEffect(() => {
    if (pipeline) setDraft(pipelineToDraft(pipeline));
  }, [pipeline]);

  const activeAgents = useMemo(() => agents.filter((agent) => !agent.archived_at), [agents]);
  const agentsById = useMemo(() => new Map(agents.map((agent) => [agent.id, agent])), [agents]);

  if (!pipeline || !draft) {
    return <div className="p-4 text-sm text-muted-foreground">Loading pipeline...</div>;
  }

  const flow = getFlowElements(draft.nodes, agentsById);

  const patchDraft = (patch: Partial<PipelineDraft>) => {
    setDraft((current) => (current ? { ...current, ...patch } : current));
  };

  const changeNode = (index: number, patch: Partial<UpsertPipelineNodeRequest>) => {
    patchDraft({
      nodes: draft.nodes.map((node, i) => (i === index ? { ...node, ...patch } : node)),
    });
  };

  const changeNodeByFlowId = (flowId: string, patch: Partial<UpsertPipelineNodeRequest>) => {
    setDraft((current) =>
      current
        ? {
            ...current,
            nodes: current.nodes.map((node, index) =>
              getFlowNodeId(node, index) === flowId ? { ...node, ...patch } : node,
            ),
          }
        : current,
    );
  };

  const connectNodes = (connection: Connection) => {
    if (!connection.source || !connection.target || connection.source === connection.target) return;
    patchDraft({
      nodes: draft.nodes.map((node, index) => {
        if (getFlowNodeId(node, index) !== connection.target) return node;
        const deps = node.depends_on_node_keys ?? [];
        return deps.includes(connection.source)
          ? node
          : { ...node, depends_on_node_keys: [...deps, connection.source] };
      }),
    });
  };

  const removeEdges = (edges: Edge[]) => {
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
        return {
          ...node,
          depends_on_node_keys: (node.depends_on_node_keys ?? []).filter((key) => !depsToRemove.has(key)),
        };
      }),
    });
  };

  const addNode = (
    nodeType: PipelineNodeType = "issue",
    position?: { x: number; y: number },
  ) => {
    const nextIndex = draft.nodes.length;
    const key = nextKey("node", draft.nodes.map((node) => node.key));
    patchDraft({
      nodes: [
        ...draft.nodes,
        {
          key,
          type: nodeType,
          title: nodeType === "issue" ? "Issue Node" : nodeType === "manual" ? "Manual Node" : "Check Node",
          description: "",
          agent_id: null,
          repos: [],
          depends_on_node_keys: [],
          position_x: position ? Math.max(0, Math.round(position.x)) : (nextIndex % 3) * 280,
          position_y: position ? Math.max(0, Math.round(position.y)) : Math.floor(nextIndex / 3) * 180,
        },
      ],
    });
  };

  const openGraphContextMenu = (event: MouseEvent | React.MouseEvent<Element, MouseEvent>) => {
    event.preventDefault();
    const flowPosition = flowInstance?.screenToFlowPosition({
      x: event.clientX,
      y: event.clientY,
    }) ?? { x: 0, y: 0 };
    setGraphContextMenu({
      screenX: event.clientX,
      screenY: event.clientY,
      flowX: flowPosition.x,
      flowY: flowPosition.y,
    });
  };

  const addNodeFromContextMenu = (nodeType: PipelineNodeType) => {
    if (!graphContextMenu) return;
    addNode(nodeType, { x: graphContextMenu.flowX, y: graphContextMenu.flowY });
    setGraphContextMenu(null);
  };

  const save = async () => {
    const errors = validatePipelineDraft(draft);
    if (errors.length > 0) {
      toast.error(errors[0]);
      return;
    }
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
    if (!window.confirm("Archive this pipeline? Existing run history is preserved.")) return;
    try {
      await deletePipeline.mutateAsync();
      toast.success("Pipeline archived");
      nav.push(paths.pipelines());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to archive pipeline");
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader>
        <div className="flex w-full items-center justify-between gap-4">
          <div className="flex min-w-0 items-center gap-2">
            <Button variant="ghost" size="icon" onClick={() => nav.push(paths.pipelines())}>
              <ArrowLeft className="h-4 w-4" />
            </Button>
            <div className="min-w-0">
              <h1 className="truncate text-sm font-semibold">{draft.name || "Pipeline"}</h1>
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <GitBranch className="h-3.5 w-3.5" />
                <span>pipeline</span>
                <Badge variant="outline">{draft.nodes.length} nodes</Badge>
              </div>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button variant="ghost" size="sm" onClick={() => setImportOpen(true)}>
              <Upload className="mr-1 h-4 w-4" />
              Import YAML
            </Button>
            <Button variant="ghost" size="sm" onClick={archive} disabled={deletePipeline.isPending}>
              <Trash2 className="mr-1 h-4 w-4" />
              Archive
            </Button>
            <Button variant="outline" size="sm" onClick={save} disabled={updatePipeline.isPending}>
              <Save className="mr-1 h-4 w-4" />
              Save
            </Button>
          </div>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-4">
        <div className="mb-4 grid gap-3">
          <Input
            value={draft.name}
            onChange={(e) => patchDraft({ name: e.target.value })}
            placeholder="Pipeline name"
          />
          <Textarea
            value={draft.description}
            onChange={(e) => patchDraft({ description: e.target.value })}
            placeholder="Reusable process description"
            className="min-h-24"
          />
        </div>

        <div className="grid gap-4">
          <section className="space-y-3">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Flow Graph</h2>
              <div className="flex items-center gap-2">
                {!propertiesDockOpen && (
                  <Button variant="outline" size="sm" onClick={() => setPropertiesDockOpen(true)}>
                    <PanelRightOpen className="mr-1 h-4 w-4" />
                    Properties
                  </Button>
                )}
                <Button variant="outline" size="sm" onClick={() => addNode()}>
                  <Plus className="mr-1 h-4 w-4" />
                  Add Node
                </Button>
              </div>
            </div>
            <div className="rounded-md border bg-muted/30 p-3 dark:bg-muted/10">
              <div className="relative h-[620px] min-h-[480px] overflow-hidden rounded-md border bg-background xl:h-[calc(100vh-260px)]">
                <ReactFlow
                  nodes={flow.nodes}
                  edges={flow.edges}
                  nodeTypes={flowNodeTypes}
                  onInit={setFlowInstance}
                  fitView
                  fitViewOptions={{ padding: 0.18 }}
                  colorMode={(resolvedTheme as "dark" | "light") ?? "light"}
                  onConnect={connectNodes}
                  onEdgesDelete={removeEdges}
                  onPaneClick={() => setGraphContextMenu(null)}
                  onPaneContextMenu={openGraphContextMenu}
                  onMoveStart={() => setGraphContextMenu(null)}
                  onNodeDragStop={(_, node) =>
                    changeNodeByFlowId(node.id, {
                      position_x: Math.round(node.position.x),
                      position_y: Math.round(node.position.y),
                    })
                  }
                  proOptions={{ hideAttribution: true }}
                >
                  <Background color="hsl(var(--muted-foreground) / 0.32)" gap={18} size={1.4} />
                  <Controls showInteractive={false} />
                </ReactFlow>
                {propertiesDockOpen && (
                  <NodePropertiesDock
                    draft={draft}
                    activeAgents={activeAgents}
                    agentsById={agentsById}
                    changeNode={changeNode}
                    patchDraft={patchDraft}
                    onClose={() => setPropertiesDockOpen(false)}
                  />
                )}
                {graphContextMenu && (
                  <div
                    className="fixed z-50 min-w-44 rounded-md border bg-popover p-1 text-sm shadow-lg"
                    style={{ left: graphContextMenu.screenX, top: graphContextMenu.screenY }}
                    onContextMenu={(event) => event.preventDefault()}
                  >
                    {nodeTypes.map((type) => (
                      <button
                        key={type}
                        type="button"
                        className="flex w-full items-center justify-between rounded-sm px-2 py-1.5 text-left hover:bg-muted"
                        onClick={() => addNodeFromContextMenu(type)}
                      >
                        <span className="capitalize">{type}</span>
                        <span className="text-xs text-muted-foreground">node</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </section>
        </div>
      </div>
      <PipelineImportDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        pipelineId={pipeline.id}
      />
    </div>
  );
}

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

function NodePropertiesDock({
  draft,
  activeAgents,
  agentsById,
  changeNode,
  patchDraft,
  onClose,
}: {
  draft: PipelineDraft;
  activeAgents: Array<{ id: string; name: string }>;
  agentsById: Map<string, { name?: string }>;
  changeNode: (index: number, patch: Partial<UpsertPipelineNodeRequest>) => void;
  patchDraft: (patch: Partial<PipelineDraft>) => void;
  onClose: () => void;
}) {
  return (
    <aside className="pointer-events-auto absolute bottom-3 right-3 top-3 z-20 flex w-[min(400px,calc(100%-24px))] flex-col overflow-hidden rounded-md border bg-background/95 shadow-2xl backdrop-blur">
      <div className="flex h-11 shrink-0 items-center justify-between border-b bg-muted/40 px-3">
        <div className="flex min-w-0 items-center gap-2">
          <PanelRightClose className="h-4 w-4 text-muted-foreground" />
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold">Node Properties</div>
            <div className="text-[11px] text-muted-foreground">{draft.nodes.length} nodes</div>
          </div>
        </div>
        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onClose} title="Collapse properties">
          <PanelRightClose className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 space-y-3 overflow-y-auto p-3">
        {draft.nodes.map((node, index) => {
          const agent = node.agent_id ? agentsById.get(node.agent_id) : undefined;
          return (
            <div key={`${node.key}-${index}`} className="space-y-2 rounded-md border bg-card/80 p-3">
              <div className="grid grid-cols-[minmax(0,1fr)_116px] gap-2">
                <Input value={node.key} onChange={(e) => changeNode(index, { key: e.target.value })} placeholder="key" />
                <Select value={node.type ?? "issue"} onValueChange={(value) => changeNode(index, { type: value as PipelineNodeType })}>
                  <SelectTrigger className="w-full">
                    <span className="min-w-0 flex-1 truncate text-left">{node.type ?? "issue"}</span>
                  </SelectTrigger>
                  <SelectContent>
                    {nodeTypes.map((type) => (
                      <SelectItem key={type} value={type}>
                        {type}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <Input value={node.title} onChange={(e) => changeNode(index, { title: e.target.value })} placeholder="title" />
              <Select value={node.agent_id || "none"} onValueChange={(value) => changeNode(index, { agent_id: value === "none" ? null : value })}>
                <SelectTrigger className="w-full">
                  <span className="min-w-0 flex-1 truncate text-left">{agent?.name ?? "Bind later"}</span>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">Bind later</SelectItem>
                  {activeAgents.map((candidate) => (
                    <SelectItem key={candidate.id} value={candidate.id}>
                      {candidate.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Textarea
                value={node.description ?? ""}
                onChange={(e) => changeNode(index, { description: e.target.value })}
                placeholder="Node issue description"
                className="min-h-20"
              />
              <div className="grid grid-cols-2 gap-2">
                <Input
                  type="number"
                  value={node.position_x ?? 0}
                  onChange={(e) => changeNode(index, { position_x: Number(e.target.value) || 0 })}
                  placeholder="x"
                />
                <Input
                  type="number"
                  value={node.position_y ?? 0}
                  onChange={(e) => changeNode(index, { position_y: Number(e.target.value) || 0 })}
                  placeholder="y"
                />
              </div>
              <div className="space-y-1 rounded-md border bg-muted/20 p-2">
                <div className="text-xs font-medium text-muted-foreground">Target repository aliases</div>
                <Input
                  value={normalizeNodeRepoKeys(node).join(", ")}
                  onChange={(event) =>
                    changeNode(index, {
                      repo: null,
                      repos: splitRepoAliasInput(event.target.value),
                    })
                  }
                  placeholder="multica, upstream"
                />
                <div className="text-xs text-muted-foreground">
                  Aliases resolve from the project selected when this pipeline is run.
                </div>
              </div>
              <div className="space-y-1 rounded-md border bg-muted/20 p-2">
                <div className="text-xs font-medium text-muted-foreground">Depends on nodes</div>
                {draft.nodes.length <= 1 ? (
                  <div className="text-xs text-muted-foreground">No other nodes.</div>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {draft.nodes
                      .filter((candidate) => candidate.key !== node.key)
                      .map((candidate) => {
                        const checked = (node.depends_on_node_keys ?? []).includes(candidate.key);
                        return (
                          <label key={candidate.key} className="inline-flex items-center gap-2 rounded-md border bg-background px-2 py-1 text-xs">
                            <Checkbox
                              checked={checked}
                              onCheckedChange={(value) =>
                                changeNode(index, {
                                  depends_on_node_keys: toggleValue(node.depends_on_node_keys ?? [], candidate.key, value === true),
                                })
                              }
                            />
                            <span>{candidate.key}</span>
                          </label>
                        );
                      })}
                  </div>
                )}
              </div>
              <div className="flex justify-end">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={draft.nodes.length === 1}
                  onClick={() => patchDraft({ nodes: draft.nodes.filter((_, i) => i !== index) })}
                >
                  <Trash2 className="mr-1 h-4 w-4" />
                  Remove
                </Button>
              </div>
            </div>
          );
        })}
      </div>
    </aside>
  );
}

function getFlowElements(
  nodes: UpsertPipelineNodeRequest[],
  agentsById: Map<string, { name?: string }>,
): { nodes: PipelineFlowNodeModel[]; edges: Edge[] } {
  const nodeIdByKey = new Map<string, string>();
  nodes.forEach((node, index) => {
    const key = node.key.trim();
    if (key && !nodeIdByKey.has(key)) {
      nodeIdByKey.set(key, getFlowNodeId(node, index));
    }
  });

  return {
    nodes: nodes.map((node, index) => {
      const agent = node.agent_id ? agentsById.get(node.agent_id) : undefined;
      const style = nodeVisualStyle(node.type ?? "issue", node.depends_on_node_keys?.length ?? 0);
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
          accentClass: style.accentClass,
          progressClass: style.progressClass,
          progressPercent: style.progressPercent,
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
          labelStyle: {
            fill: "#ffffff",
            fontSize: 12,
            fontWeight: 700,
          },
          labelBgStyle: {
            fill: "#38c878",
            fillOpacity: 1,
          },
          labelBgPadding: [8, 4],
          labelBgBorderRadius: 8,
          style: {
            stroke: "#38c878",
            strokeDasharray: "3 7",
            strokeLinecap: "round",
            strokeWidth: 2,
          },
          markerEnd: { type: MarkerType.ArrowClosed },
        };
      });
    }),
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
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function PipelineFlowNode({ data }: NodeProps<PipelineFlowNodeModel>) {
  return (
    <div className="w-72 rounded-md border border-border bg-card px-4 py-3 text-card-foreground shadow-[0_8px_24px_rgba(15,23,42,0.08)] dark:shadow-[0_8px_24px_rgba(0,0,0,0.35)]">
      <Handle type="target" position={Position.Left} className="!h-3 !w-3 !border-2 !border-card !bg-muted-foreground" />
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-[15px] font-semibold leading-5">{data.title || data.key || "Node"}</div>
          <div className="mt-1 flex items-center gap-1.5 text-[12px] text-muted-foreground">
            <span className={`h-2.5 w-2.5 rounded-full ${data.accentClass}`} />
            <span className="truncate">pipeline node</span>
            <span>•</span>
            <span className="truncate">{data.agentName}</span>
          </div>
        </div>
        <span className="shrink-0 text-[12px] font-medium text-muted-foreground">Details ↗</span>
      </div>
      <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full rounded-full ${data.progressClass}`}
          style={{ width: `${data.progressPercent}%` }}
        />
      </div>
      <div className="mt-3 grid grid-cols-3 gap-3 border-t border-border/70 pt-3 text-[12px]">
        <div>
          <div className="text-muted-foreground">Type</div>
          <div className="mt-0.5 font-semibold capitalize text-foreground">{data.nodeType}</div>
        </div>
        <div>
          <div className="text-muted-foreground">Deps</div>
          <div className="mt-0.5 font-semibold text-foreground">{data.dependencyCount}</div>
        </div>
        <div>
          <div className="text-muted-foreground">Status</div>
          <div className="mt-0.5 flex items-center gap-1 font-semibold text-emerald-600">
            <Gauge className="h-3 w-3" />
            Ready
          </div>
        </div>
      </div>
      <Handle type="source" position={Position.Right} className="!h-3 !w-3 !border-2 !border-card !bg-muted-foreground" />
    </div>
  );
}

function nodeVisualStyle(nodeType: PipelineNodeType, dependencyCount: number) {
  if (nodeType === "manual") {
    return {
      accentClass: "bg-sky-500",
      progressClass: "bg-sky-400",
      progressPercent: dependencyCount > 0 ? 70 : 100,
    };
  }
  if (nodeType === "check") {
    return {
      accentClass: "bg-emerald-500",
      progressClass: "bg-emerald-400",
      progressPercent: dependencyCount > 0 ? 85 : 100,
    };
  }
  return {
    accentClass: "bg-amber-400",
    progressClass: "bg-amber-300",
    progressPercent: dependencyCount > 0 ? 50 : 100,
  };
}

function nextKey(prefix: string, existing: string[]) {
  let index = existing.length + 1;
  let key = `${prefix}-${index}`;
  while (existing.includes(key)) {
    index += 1;
    key = `${prefix}-${index}`;
  }
  return key;
}

function toggleValue(values: string[], value: string, checked: boolean) {
  if (checked) return values.includes(value) ? values : [...values, value];
  return values.filter((candidate) => candidate !== value);
}
