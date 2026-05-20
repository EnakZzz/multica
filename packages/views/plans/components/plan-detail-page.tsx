"use client";

import { useMemo, useState } from "react";
import { ArrowLeft, Bot, CheckCircle2, GitBranch, RefreshCw, Save } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { issueListOptions } from "@multica/core/issues/queries";
import { planDetailOptions } from "@multica/core/plans/queries";
import { useCommitPlan, useRerunPlan, useUpdatePlan } from "@multica/core/plans/mutations";
import type { Issue, PlanItem } from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { AppLink, useNavigation } from "../../navigation";
import { StatusIcon } from "../../issues/components";

export function PlanDetailPage({ planId: explicitPlanId }: { planId?: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const planId = explicitPlanId ?? decodeURIComponent(nav.pathname.match(/\/plans\/([^/]+)$/)?.[1] ?? "");
  const { data: plan } = useQuery(planDetailOptions(wsId, planId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: issues = [] } = useQuery(issueListOptions(wsId));
  const updatePlan = useUpdatePlan(wsId, planId);
  const rerunPlan = useRerunPlan(wsId, planId);
  const commitPlan = useCommitPlan(wsId, planId);
  const [dirtyItems, setDirtyItems] = useState<PlanItem[] | null>(null);
  const [parentTitle, setParentTitle] = useState("");
  const [parentDescription, setParentDescription] = useState("");

  const items = dirtyItems ?? plan?.items ?? [];
  const agentsById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const issuesById = useMemo(() => new Map(issues.map((issue) => [issue.id, issue])), [issues]);

  if (!plan) {
    return <div className="p-4 text-sm text-muted-foreground">Loading plan...</div>;
  }
  const effectiveParentTitle = parentTitle || plan.parent_title || plan.title;
  const effectiveParentDescription = parentDescription || plan.parent_description;
  const editable = plan.status !== "committed";

  const changeItem = (id: string, patch: Partial<PlanItem>) => {
    const base = dirtyItems ?? plan.items;
    setDirtyItems(base.map((item) => (item.id === id ? { ...item, ...patch } : item)));
  };

  const save = async () => {
    await updatePlan.mutateAsync({
      title: plan.title,
      parent_title: effectiveParentTitle,
      parent_description: effectiveParentDescription,
      items: items.map((item) => ({
        title: item.title,
        description: item.description,
        recommended_agent_id: item.recommended_agent_id,
        match_score: item.match_score,
        match_reason: item.match_reason,
        missing_capability: item.missing_capability,
        depends_on_positions: item.depends_on_positions,
        selected: item.selected,
      })),
    });
    setDirtyItems(null);
    toast.success("Plan saved");
  };

  const saveWithToast = async () => {
    try {
      await save();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save plan");
    }
  };

  const commit = async () => {
    try {
      if (dirtyItems) await save();
      const committed = await commitPlan.mutateAsync();
      toast.success("Issues created");
      if (committed.parent_issue_id) nav.push(paths.issueDetail(committed.parent_issue_id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to commit plan");
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader>
        <div className="flex w-full items-center justify-between">
          <div className="flex min-w-0 items-center gap-2">
            <Button variant="ghost" size="icon" onClick={() => nav.push(paths.plans())}>
              <ArrowLeft className="h-4 w-4" />
            </Button>
            <div className="min-w-0">
              <h1 className="truncate text-sm font-semibold">{plan.title}</h1>
              <div className="text-xs text-muted-foreground">Plan</div>
            </div>
            <Badge variant={plan.status === "failed" ? "destructive" : "secondary"}>{plan.status}</Badge>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" disabled={!editable || rerunPlan.isPending} onClick={() => rerunPlan.mutate()}>
              <RefreshCw className="mr-1 h-4 w-4" />
              Rerun
            </Button>
            <Button variant="outline" size="sm" disabled={!editable || updatePlan.isPending} onClick={saveWithToast}>
              <Save className="mr-1 h-4 w-4" />
              Save
            </Button>
            <Button size="sm" disabled={plan.status !== "ready" || commitPlan.isPending} onClick={commit}>
              <CheckCircle2 className="mr-1 h-4 w-4" />
              Create Issues
            </Button>
          </div>
        </div>
      </PageHeader>
      <div className="flex-1 overflow-auto p-4">
        {plan.status === "planning" && (
          <div className="mb-4 rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">
            Planner agent is working. This page refreshes automatically.
          </div>
        )}
        {plan.error && <div className="mb-4 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{plan.error}</div>}
        <div className="mb-4 grid gap-3">
          <Input value={effectiveParentTitle} disabled={!editable} onChange={(e) => setParentTitle(e.target.value)} />
          <Textarea
            value={effectiveParentDescription}
            disabled={!editable}
            onChange={(e) => setParentDescription(e.target.value)}
            className="min-h-28"
          />
        </div>
        <div className="space-y-3">
          {items.map((item) => {
            const agent = item.recommended_agent_id ? agentsById.get(item.recommended_agent_id) : null;
            const gap = !item.recommended_agent_id || item.match_score < 60;
            return (
              <div key={item.id} className="rounded-md border p-3">
                <div className="flex items-start gap-3">
                  <Checkbox checked={item.selected} disabled={!editable || !!item.generated_issue_id} onCheckedChange={(v) => changeItem(item.id, { selected: v === true })} />
                  <div className="min-w-0 flex-1 space-y-2">
                    <Input value={item.title} disabled={!editable || !!item.generated_issue_id} onChange={(e) => changeItem(item.id, { title: e.target.value })} />
                    <Textarea value={item.description} disabled={!editable || !!item.generated_issue_id} onChange={(e) => changeItem(item.id, { description: e.target.value })} />
                    <div className="grid gap-2 sm:grid-cols-[220px_100px_1fr]">
                      <Select
                        value={item.recommended_agent_id ?? "none"}
                        disabled={!editable || !!item.generated_issue_id}
                        onValueChange={(v) => changeItem(item.id, { recommended_agent_id: v === "none" ? null : v })}
                      >
                        <SelectTrigger className="w-full">
                          <span className="min-w-0 flex-1 truncate text-left">
                            {item.recommended_agent_id ? (agentsById.get(item.recommended_agent_id)?.name ?? "Agent") : "No suitable agent"}
                          </span>
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="none">No suitable agent</SelectItem>
                          {agents.filter((a) => !a.archived_at).map((a) => (
                            <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Badge variant={gap ? "destructive" : "secondary"} className="justify-center">{item.match_score}%</Badge>
                      <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Bot className="h-4 w-4" />
                        {gap ? (item.missing_capability || "缺乏合适智能体") : (agent?.name ?? "Agent")}
                      </div>
                    </div>
                    <Input value={item.match_reason} disabled={!editable || !!item.generated_issue_id} onChange={(e) => changeItem(item.id, { match_reason: e.target.value })} />
                    <div className="rounded-md border bg-muted/20 p-2">
                      <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
                        <GitBranch className="h-3.5 w-3.5" />
                        Depends on
                      </div>
                      {editable && !item.generated_issue_id && (
                        <Input
                          value={formatPositions(item.depends_on_positions)}
                          placeholder="Item positions, e.g. 1, 2"
                          onChange={(e) => changeItem(item.id, { depends_on_positions: parsePositions(e.target.value, item.position) })}
                        />
                      )}
                      <PlanDependencySummary item={item} items={items} issuesById={issuesById} />
                    </div>
                    {gap && (
                      <Input
                        value={item.missing_capability}
                        disabled={!editable || !!item.generated_issue_id}
                        placeholder="Missing capability"
                        onChange={(e) => changeItem(item.id, { missing_capability: e.target.value })}
                      />
                    )}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function formatPositions(positions: number[] | undefined) {
  return (positions ?? []).join(", ");
}

function parsePositions(value: string, currentPosition: number) {
  const seen = new Set<number>();
  const out: number[] = [];
  for (const part of value.split(",")) {
    const n = Number.parseInt(part.trim(), 10);
    if (!Number.isFinite(n) || n <= 0 || n >= currentPosition || seen.has(n)) continue;
    seen.add(n);
    out.push(n);
  }
  return out;
}

function PlanDependencySummary({
  item,
  items,
  issuesById,
}: {
  item: PlanItem;
  items: PlanItem[];
  issuesById: Map<string, Issue>;
}) {
  const paths = useWorkspacePaths();
  const dependencies = (item.depends_on_positions ?? []).map((position) => ({
    position,
    item: items.find((candidate) => candidate.position === position),
  }));

  if (dependencies.length === 0) {
    return <div className="mt-1 text-sm text-muted-foreground">No prerequisites</div>;
  }

  return (
    <div className="mt-2 flex flex-wrap gap-1.5">
      {dependencies.map(({ position, item: dep }) => {
        const issue = dep?.generated_issue_id ? issuesById.get(dep.generated_issue_id) : undefined;
        const label = dep ? `#${position} ${dep.title}` : `#${position}`;
        if (issue) {
          return (
            <AppLink
              key={position}
              href={paths.issueDetail(issue.id)}
              className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-xs text-muted-foreground hover:bg-accent"
            >
              <StatusIcon status={issue.status} className="h-3.5 w-3.5 shrink-0" />
              <span className="shrink-0 tabular-nums">{issue.identifier}</span>
              <span className="truncate">{issue.title}</span>
            </AppLink>
          );
        }
        return (
          <span
            key={position}
            className="inline-flex max-w-full items-center rounded-md border bg-background px-2 py-1 text-xs text-muted-foreground"
          >
            <span className="truncate">{label}</span>
          </span>
        );
      })}
    </div>
  );
}
