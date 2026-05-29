"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  applyNodeChanges,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Bot, CheckCircle2, GitMerge, User } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useTheme } from "@multica/ui/components/common/theme-provider";
import type { Issue, PlanItem } from "@multica/core/types";
import type { Edge, Node, NodeChange, NodeProps } from "@xyflow/react";
import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";

// ─── Types ────────────────────────────────────────────────────────────────────

type PlanItemNodeData = {
  position: number;
  title: string;
  nodeType: PlanItem["node_type"];
  executionKind: "agent_task" | "human_confirmation";
  agentName: string | null;
  selected: boolean;
  matchScore: number;
  hasGap: boolean;
  isCommitted: boolean;
  issue: Issue | undefined;
};

type PlanItemFlowNode = Node<PlanItemNodeData, "planItem">;
const FLOW_NODE_TYPES = { planItem: PlanItemNode };

// ─── Main component ───────────────────────────────────────────────────────────

export function PlanItemsFlowGraph({
  items,
  agentsById,
  issuesById,
}: {
  items: PlanItem[];
  agentsById: Map<string, { id: string; name: string }>;
  issuesById: Map<string, Issue>;
}) {
  const { resolvedTheme } = useTheme();
  const { nodes: initialNodes } = planItemsToFlow(items, agentsById, issuesById);
  const [rfNodes, setRfNodes] = useState(initialNodes);

  useEffect(() => {
    const { nodes, edges: newEdges } = planItemsToFlow(items, agentsById, issuesById);
    setRfNodes((prev) => {
      const posMap = new Map(prev.map((n) => [n.id, n.position]));
      return nodes.map((n) => ({ ...n, position: posMap.get(n.id) ?? n.position }));
    });
    // edges are derived from items directly, no need to preserve
    void newEdges;
  }, [items, agentsById, issuesById]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setRfNodes((prev) => applyNodeChanges(changes, prev) as PlanItemFlowNode[]);
  }, []);

  const { nodes: _, edges: latestEdges } = planItemsToFlow(items, agentsById, issuesById);

  return (
    <div className="h-[360px] overflow-hidden rounded-lg border bg-muted/10 xl:h-[440px] 2xl:h-[480px]">
      <ReactFlow
        nodes={rfNodes}
        edges={latestEdges}
        nodeTypes={FLOW_NODE_TYPES}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        colorMode={(resolvedTheme as "dark" | "light") ?? "light"}
        nodesDraggable
        nodesConnectable={false}
        elementsSelectable={false}
        onNodesChange={onNodesChange}
        proOptions={{ hideAttribution: true }}
        style={{ width: "100%", height: "100%" }}
      >
        <Background color="hsl(var(--muted-foreground) / 0.18)" gap={18} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}

// ─── Skeleton shown during `planning` ─────────────────────────────────────────

export function PlanningFlowSkeleton() {
  const GHOST_NODES = [
    { id: "g1", x: 40, y: 130, wide: false },
    { id: "g2", x: 260, y: 60, wide: true },
    { id: "g3", x: 260, y: 200, wide: false },
    { id: "g4", x: 490, y: 130, wide: true },
  ];
  const GHOST_EDGES = [
    { from: "g1", to: "g2" },
    { from: "g1", to: "g3" },
    { from: "g2", to: "g4" },
    { from: "g3", to: "g4" },
  ];

  return (
    <div className="relative h-[360px] overflow-hidden rounded-lg border bg-muted/10">
      {/* SVG edges */}
      <svg className="pointer-events-none absolute inset-0 h-full w-full">
        <defs>
          <marker id="ghost-arrow" markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
            <polygon points="0 0, 8 3, 0 6" className="fill-border" />
          </marker>
        </defs>
        {GHOST_EDGES.map(({ from, to }) => {
          const src = GHOST_NODES.find((n) => n.id === from)!;
          const dst = GHOST_NODES.find((n) => n.id === to)!;
          const x1 = src.x + (src.wide ? 168 : 148);
          const y1 = src.y + 40;
          const x2 = dst.x;
          const y2 = dst.y + 40;
          const mx = (x1 + x2) / 2;
          return (
            <path
              key={`${from}-${to}`}
              d={`M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}`}
              fill="none"
              className="stroke-border"
              strokeWidth={1.5}
              strokeDasharray="4 5"
              markerEnd="url(#ghost-arrow)"
            />
          );
        })}
      </svg>

      {/* Ghost nodes */}
      {GHOST_NODES.map((node, i) => (
        <div
          key={node.id}
          className="absolute overflow-hidden rounded-lg border bg-card/80"
          style={{
            left: node.x,
            top: node.y,
            width: node.wide ? 168 : 148,
            animationDelay: `${i * 0.15}s`,
          }}
        >
          <div className="h-0.5 w-full animate-pulse bg-muted-foreground/20" />
          <div className="space-y-2 p-3">
            <div
              className="h-3 animate-pulse rounded bg-muted-foreground/15"
              style={{ animationDelay: `${i * 0.15}s`, width: node.wide ? "75%" : "60%" }}
            />
            <div
              className="h-2 animate-pulse rounded bg-muted-foreground/10"
              style={{ animationDelay: `${i * 0.15 + 0.1}s`, width: "50%" }}
            />
            <div className="flex gap-1.5 pt-0.5">
              <div
                className="h-4 w-14 animate-pulse rounded-sm bg-muted-foreground/10"
                style={{ animationDelay: `${i * 0.15 + 0.2}s` }}
              />
            </div>
          </div>
        </div>
      ))}

      {/* Overlay label */}
      <div className="pointer-events-none absolute bottom-3 left-1/2 -translate-x-1/2">
        <span className="rounded-full border bg-background/80 px-3 py-1 text-[10px] font-medium tracking-wide text-muted-foreground backdrop-blur-sm">
          Generating task graph…
        </span>
      </div>
    </div>
  );
}

// ─── Flow node card ───────────────────────────────────────────────────────────

function PlanItemNode({ data }: NodeProps<PlanItemFlowNode>) {
  const paths = useWorkspacePaths();
  const isHuman = data.executionKind === "human_confirmation";
  const isMerge = data.nodeType === "merge";
  const accentClass = isHuman
    ? "bg-amber-400"
    : isMerge
      ? "bg-cyan-500"
    : data.hasGap
      ? "bg-rose-400"
      : "bg-indigo-500";

  return (
    <div
      className={cn(
        "w-52 overflow-hidden rounded-lg border bg-card shadow-sm transition-shadow",
        !data.selected && "opacity-50",
        data.isCommitted && "border-emerald-500/30 bg-emerald-500/5",
      )}
    >
      <div className={cn("h-0.5 w-full", accentClass)} />

      <Handle
        type="target"
        position={Position.Left}
        className="!h-2.5 !w-2.5 !border-2 !border-background !bg-muted-foreground/40"
      />

      <div className="px-3 py-2">
        {/* Position + title */}
        <div className="flex items-start gap-2">
          <span className="mt-px shrink-0 font-mono text-[9px] font-semibold text-muted-foreground/50">
            {String(data.position).padStart(2, "0")}
          </span>
          <span className="min-w-0 truncate text-[12px] font-semibold leading-tight">{data.title}</span>
        </div>

        {/* Agent / human row */}
        <div className="mt-1.5 flex items-center gap-1 text-[10px] text-muted-foreground">
          {isHuman ? (
            <>
              <User className="h-3 w-3 shrink-0" />
              <span>Human confirmation</span>
            </>
          ) : isMerge ? (
            <>
              <GitMerge className="h-3 w-3 shrink-0" />
              <span className="truncate">{data.agentName ?? "Merge Agent"}</span>
            </>
          ) : (
            <>
              <Bot className="h-3 w-3 shrink-0" />
              <span className="truncate">{data.agentName ?? "Bind later"}</span>
            </>
          )}
        </div>

        {/* Footer badges */}
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span
            className={cn(
              "rounded-sm px-1.5 py-0.5 text-[9px] font-medium",
              isHuman
                ? "bg-amber-500/10 text-amber-600"
                : isMerge
                  ? "bg-cyan-500/10 text-cyan-700"
                : data.hasGap
                  ? "bg-rose-500/10 text-rose-600"
                  : "bg-indigo-500/10 text-indigo-600",
            )}
          >
            {isHuman ? "human" : isMerge ? "merge" : data.hasGap ? `gap ${data.matchScore}%` : `${data.matchScore}%`}
          </span>

          {data.isCommitted && data.issue && (
            <AppLink
              href={paths.issueDetail(data.issue.id)}
              className="inline-flex items-center gap-0.5 rounded-sm border bg-background px-1.5 py-0.5 text-[9px] text-muted-foreground hover:bg-accent"
            >
              <CheckCircle2 className="h-2.5 w-2.5 text-emerald-500" />
              {data.issue.identifier}
            </AppLink>
          )}
        </div>
      </div>

      <Handle
        type="source"
        position={Position.Right}
        className="!h-2.5 !w-2.5 !border-2 !border-background !bg-muted-foreground/40"
      />
    </div>
  );
}

// ─── Conversion helpers ───────────────────────────────────────────────────────

function planItemsToFlow(
  items: PlanItem[],
  agentsById: Map<string, { id: string; name: string }>,
  issuesById: Map<string, Issue>,
): { nodes: PlanItemFlowNode[]; edges: Edge[] } {
  const posToId = new Map(items.map((item) => [item.position, String(item.id)]));

  const nodes: PlanItemFlowNode[] = items.map((item) => {
    const agent = item.recommended_agent_id ? agentsById.get(item.recommended_agent_id) : null;
    const isHuman = item.execution_kind === "human_confirmation";
    const hasGap = !isHuman && (!item.recommended_agent_id || item.match_score < 60);
    const issue = item.generated_issue_id ? issuesById.get(item.generated_issue_id) : undefined;

    // Default layout: place nodes in a left-to-right grid
    const col = computeColumn(item.position, items);
    const row = computeRow(item.position, items);

    return {
      id: String(item.id),
      type: "planItem" as const,
      position: { x: col * 230, y: row * 130 },
      data: {
        position: item.position,
        title: item.title,
        nodeType: item.node_type,
        executionKind: item.execution_kind,
        agentName: agent?.name ?? null,
        selected: item.selected,
        matchScore: item.match_score,
        hasGap,
        isCommitted: !!item.generated_issue_id,
        issue,
      },
    };
  });

  const edges: Edge[] = items.flatMap((item) =>
    (item.depends_on_positions ?? []).flatMap((depPos) => {
      const sourceId = posToId.get(depPos);
      if (!sourceId) return [];
      return [
        {
          id: `${sourceId}->${item.id}`,
          source: sourceId,
          target: String(item.id),
          type: "smoothstep",
          style: { stroke: "#38c878", strokeDasharray: "4 6", strokeWidth: 1.5, strokeLinecap: "round" },
          markerEnd: { type: MarkerType.ArrowClosed, color: "#38c878" },
        },
      ];
    }),
  );

  return { nodes, edges };
}

/** Estimate column from dependency depth */
function computeColumn(position: number, items: PlanItem[]): number {
  const item = items.find((i) => i.position === position);
  if (!item || !item.depends_on_positions?.length) return 0;
  return (
    1 +
    Math.max(
      0,
      ...item.depends_on_positions.map((dep) => computeColumn(dep, items)),
    )
  );
}

/** Stack items in the same column vertically */
function computeRow(position: number, items: PlanItem[]): number {
  const col = computeColumn(position, items);
  const sameCol = items.filter((i) => computeColumn(i.position, items) === col);
  return sameCol.findIndex((i) => i.position === position);
}
