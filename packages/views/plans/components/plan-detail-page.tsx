"use client";

import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { ArrowLeft, ArrowRight, BookOpenText, Bot, CheckCircle2, ChevronDown, GitBranch, GitMerge, Loader2, MessageSquare, RefreshCw, Save, Send, User, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@multica/ui/components/ui/collapsible";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { issueListOptions } from "@multica/core/issues/queries";
import { planDetailOptions } from "@multica/core/plans/queries";
import { taskKnowledgeTraceOptions } from "@multica/core/project-knowledge/queries";
import { useApprovePlanSpec, useClarifyPlanSpec, useCommitPlan, useRerunPlan, useUpdatePlan } from "@multica/core/plans/mutations";
import type { Issue, PlanItem, PlanSpec, ProjectKnowledgeRetrievalLog, ProjectKnowledgeSearchResult, ProjectRelevantKnowledge } from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { AppLink, useNavigation } from "../../navigation";
import { StatusIcon } from "../../issues/components";
import { PlanItemsFlowGraph, PlanningFlowSkeleton } from "./plan-flow-graph";

type PlanStatus = "planning" | "spec_review" | "ready" | "failed" | "committed";

const PLAN_STEPS: { key: PlanStatus; label: string }[] = [
  { key: "planning", label: "规划 Planning" },
  { key: "spec_review", label: "规格评审 Spec Review" },
  { key: "ready", label: "就绪 Ready" },
  { key: "committed", label: "完成 Done" },
];

const PLAN_QUICK_LINKS = [
  { label: "重点摘要 / Summary", href: "#executive-summary", id: "executive-summary", code: "01" },
  { label: "验收标准 / Criteria", href: "#success-criteria", id: "success-criteria", code: "03" },
  { label: "场景用例 / Scenarios", href: "#acceptance-scenarios", id: "acceptance-scenarios", code: "04" },
  { label: "范围边界 / Scope", href: "#scope-boundary", id: "scope-boundary", code: "05" },
  { label: "实施路径 / Approach", href: "#approach", id: "approach", code: "07" },
  { label: "Wiki 引用 / Wiki", href: "#wiki-references", id: "wiki-references", code: "08" },
  { label: "执行流水线 / Pipeline", href: "#pipeline", id: "pipeline", code: "09" },
] as const;

// ─── Step Timeline ────────────────────────────────────────────────────────────

function PlanStepTimeline({ status }: { status: PlanStatus }) {
  const isFailed = status === "failed";
  const currentIdx = isFailed ? 0 : PLAN_STEPS.findIndex((s) => s.key === status);

  return (
    <div className="flex items-center">
      {PLAN_STEPS.map((step, i) => {
        const isPast = !isFailed && i < currentIdx;
        const isActive = !isFailed && i === currentIdx;

        return (
          <Fragment key={step.key}>
            {i > 0 && (
              <div
                className={cn(
                  "mx-1 h-px w-4 flex-shrink-0 transition-colors duration-500",
                  isPast ? "bg-primary/50" : "bg-border/50",
                )}
              />
            )}
            <div
              className={cn(
                "flex items-center gap-1.5 rounded px-1.5 py-1 transition-all duration-300",
                isActive && "bg-primary/8 ring-1 ring-inset ring-primary/20",
                isFailed && i === 0 && "bg-destructive/8 ring-1 ring-inset ring-destructive/15",
              )}
            >
              <span
                className={cn(
                  "font-mono text-[9px] font-bold tabular-nums leading-none transition-colors duration-300",
                  isPast && "text-muted-foreground/35",
                  isActive && "text-primary",
                  !isPast && !isActive && "text-muted-foreground/20",
                  isFailed && i === 0 && "text-destructive/60",
                )}
              >
                {String(i + 1).padStart(2, "0")}
              </span>
              <span
                className={cn(
                  "text-[10px] font-medium leading-none transition-colors duration-300",
                  isPast && "text-muted-foreground/35",
                  isActive && "text-foreground",
                  !isPast && !isActive && "text-muted-foreground/20",
                  isFailed && i === 0 && "text-destructive/60",
                )}
              >
                {step.label}
              </span>
              {isActive && (
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-primary" style={{ animationDuration: "1.6s" }} />
              )}
            </div>
          </Fragment>
        );
      })}
    </div>
  );
}

// ─── Section rule ─────────────────────────────────────────────────────────────

function SectionRule({ label, meta }: { label: string; meta?: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3">
      <span className="shrink-0 font-mono text-[10px] font-bold uppercase tracking-[0.16em] text-muted-foreground/55">
        {label}
      </span>
      <div className="h-px flex-1 bg-border" />
      {meta && <span className="shrink-0 font-mono text-[10px] text-muted-foreground/45">{meta}</span>}
    </div>
  );
}

function PlanDocSectionHeader({
  number,
  title,
  meta,
}: {
  number: string;
  title: string;
  meta?: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-3">
      <span className="shrink-0 font-mono text-[11px] font-bold tabular-nums text-muted-foreground/55">{number}</span>
      <h3 className="shrink-0 text-base font-bold tracking-tight text-foreground">{title}</h3>
      <div className="h-px min-w-4 flex-1 bg-border" />
      {meta && (
        <span className="inline-flex h-6 shrink-0 items-center rounded-full bg-muted px-2.5 text-xs font-semibold text-muted-foreground">
          {meta}
        </span>
      )}
    </div>
  );
}

function PlanStatusChip({ status }: { status: PlanStatus }) {
  const config: Record<PlanStatus, { label: string; className: string }> = {
    planning: { label: "规划 / Planning", className: "bg-amber-500/10 text-amber-700 ring-amber-500/20" },
    spec_review: { label: "规格评审 / Spec Review", className: "bg-sky-500/10 text-sky-700 ring-sky-500/20" },
    ready: { label: "就绪 / Ready", className: "bg-indigo-500/10 text-indigo-700 ring-indigo-500/20" },
    failed: { label: "失败 / Failed", className: "bg-rose-500/10 text-rose-700 ring-rose-500/20" },
    committed: { label: "已批准 / Approved", className: "bg-emerald-500/10 text-emerald-700 ring-emerald-500/20" },
  };
  const cfg = config[status];
  return (
    <span className={cn("inline-flex h-6 items-center rounded-full px-2.5 text-xs font-semibold ring-1", cfg.className)}>
      {cfg.label}
    </span>
  );
}

function MethodChip({ children, tone = "default" }: { children: React.ReactNode; tone?: "default" | "green" | "amber" }) {
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center rounded-full px-2.5 text-xs font-semibold ring-1",
        tone === "green" && "bg-emerald-500/10 text-emerald-700 ring-emerald-500/20",
        tone === "amber" && "bg-amber-500/10 text-amber-700 ring-amber-500/20",
        tone === "default" && "bg-blue-500/10 text-blue-700 ring-blue-500/20",
      )}
    >
      {children}
    </span>
  );
}

function PriorityPill({ label }: { label: string }) {
  const normalized = label.toUpperCase();
  return (
    <span
      className={cn(
        "inline-flex h-6 min-w-8 items-center justify-center rounded-full px-2 text-[11px] font-bold",
        normalized === "P0" && "bg-emerald-500/10 text-emerald-700",
        normalized === "P1" && "bg-amber-500/10 text-amber-700",
        normalized !== "P0" && normalized !== "P1" && "bg-muted text-muted-foreground",
      )}
    >
      {normalized}
    </span>
  );
}

function firstNonEmpty(items: string[], fallback: string) {
  return items.find((item) => item.trim().length > 0) ?? fallback;
}

function replaceAt<T>(items: T[], index: number, value: T) {
  return items.map((item, i) => (i === index ? value : item));
}

function removeAt<T>(items: T[], index: number) {
  return items.filter((_, i) => i !== index);
}

function hasPlanSpecContent(spec: PlanSpec) {
  return [
    spec.summary,
    spec.goal,
    spec.approach,
    ...spec.success_criteria,
    ...spec.in_scope,
    ...spec.out_of_scope,
    ...spec.design_decisions,
    ...spec.verification_commands,
    ...spec.assumptions,
    ...spec.open_questions,
    ...spec.clarifications.flatMap((item) => [item.question, item.answer]),
  ].some((item) => item.trim().length > 0);
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export function PlanDetailPage({ planId: explicitPlanId }: { planId?: string }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const planId = explicitPlanId ?? decodeURIComponent(nav.pathname.match(/\/plans\/([^/]+)$/)?.[1] ?? "");
  const { data: plan } = useQuery(planDetailOptions(wsId, planId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: issues = [] } = useQuery(issueListOptions(wsId));
  const wikiTraceQuery = useQuery({
    ...taskKnowledgeTraceOptions(wsId, plan?.task_id ?? ""),
    enabled: Boolean(plan?.project_id && plan?.task_id),
  });
  const updatePlan = useUpdatePlan(wsId, planId);
  const rerunPlan = useRerunPlan(wsId, planId);
  const approvePlanSpec = useApprovePlanSpec(wsId, planId);
  const clarifyPlanSpec = useClarifyPlanSpec(wsId, planId);
  const commitPlan = useCommitPlan(wsId, planId);
  const [dirtyItems, setDirtyItems] = useState<PlanItem[] | null>(null);
  const [specDraft, setSpecDraft] = useState<PlanSpec | null>(null);
  const [clarificationAnswers, setClarificationAnswers] = useState<Record<string, string>>({});
  const [clarifyingQuestion, setClarifyingQuestion] = useState<string | null>(null);
  const [parentTitle, setParentTitle] = useState("");
  const [parentDescription, setParentDescription] = useState("");
  const [confirmationOpen, setConfirmationOpen] = useState(false);
  const scrollRootRef = useRef<HTMLDivElement>(null);
  const [activeQuickLinkId, setActiveQuickLinkId] = useState<(typeof PLAN_QUICK_LINKS)[number]["id"]>(PLAN_QUICK_LINKS[0].id);

  const items = dirtyItems ?? plan?.items ?? [];
  const persistedSpec = plan?.status === "committed" ? (plan.committed_spec ?? plan.spec) : plan?.spec;
  const spec = specDraft ?? persistedSpec ?? emptyPlanSpec();
  const agentsById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const issuesById = useMemo(() => new Map(issues.map((issue) => [issue.id, issue])), [issues]);
  const planStatus = plan?.status as PlanStatus | undefined;
  const status = planStatus ?? "planning";
  const isClarifyingSpec = status === "planning" && hasPlanSpecContent(spec);
  const itemsVisible = status === "ready" || status === "committed";

  useEffect(() => {
    if (planStatus !== "planning") setClarifyingQuestion(null);
  }, [planStatus]);

  useEffect(() => {
    const root = scrollRootRef.current;
    if (!root) return;

    let frame: number | null = null;
    const updateActiveSection = () => {
      frame = null;
      const rootTop = root.getBoundingClientRect().top;
      const sections = PLAN_QUICK_LINKS
        .map((link) => root.querySelector<HTMLElement>(`#${link.id}`))
        .filter((section): section is HTMLElement => Boolean(section));

      if (sections.length === 0) return;

      let active = sections[0]!;
      for (const section of sections) {
        if (section.getBoundingClientRect().top - rootTop <= 120) {
          active = section;
        } else {
          break;
        }
      }
      setActiveQuickLinkId(active.id as (typeof PLAN_QUICK_LINKS)[number]["id"]);
    };

    const scheduleUpdate = () => {
      if (frame !== null) return;
      frame = window.requestAnimationFrame(updateActiveSection);
    };

    root.addEventListener("scroll", scheduleUpdate, { passive: true });
    window.addEventListener("resize", scheduleUpdate);
    scheduleUpdate();

    return () => {
      if (frame !== null) window.cancelAnimationFrame(frame);
      root.removeEventListener("scroll", scheduleUpdate);
      window.removeEventListener("resize", scheduleUpdate);
    };
  }, [planStatus, isClarifyingSpec, itemsVisible, items.length]);

  if (!plan) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const effectiveParentTitle = parentTitle || plan.parent_title || plan.title;
  const effectiveParentDescription = parentDescription || plan.parent_description;
  const editable = status !== "committed";
  const specEditable = status === "spec_review";

  const selectedHumanConfirmationItems = items.filter(
    (item) => item.selected && item.execution_kind === "human_confirmation" && !item.generated_issue_id,
  );

  const changeItem = (id: string, patch: Partial<PlanItem>) => {
    setDirtyItems((current) => (current ?? plan.items).map((item) => (item.id === id ? { ...item, ...patch } : item)));
  };

  const save = async () => {
    const updated = await updatePlan.mutateAsync({
      title: plan.title,
      parent_title: effectiveParentTitle,
      parent_description: effectiveParentDescription,
      items: items.map((item) => ({
        title: item.title,
        description: item.description,
        acceptance_criteria: item.acceptance_criteria,
        suggested_test_commands: item.suggested_test_commands,
        unit_test_checklist: item.unit_test_checklist,
        context_resources: item.context_resources,
        risk_notes: item.risk_notes,
        node_type: item.node_type,
        execution_kind: item.execution_kind,
        confirmation_question: item.confirmation_question,
        confirmation_reason: item.confirmation_reason,
        required_evidence: item.required_evidence,
        requires_git_commit: item.requires_git_commit,
        branch_name: item.branch_name,
        iteration_index: item.iteration_index,
        iteration_title: item.iteration_title,
        iteration_branch_name: item.iteration_branch_name,
        recommended_agent_id: item.recommended_agent_id,
        match_score: item.match_score,
        match_reason: item.match_reason,
        missing_capability: item.missing_capability,
        depends_on_positions: item.depends_on_positions,
        selected: item.selected,
      })),
      spec,
    });
    setDirtyItems(null);
    setSpecDraft(null);
    toast.success("Plan saved");
    return updated;
  };

  const saveWithToast = async () => {
    try {
      await save();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to save plan");
    }
  };

  const createIssues = async (acknowledgedItems: PlanItem[]) => {
    try {
      const saved = dirtyItems ? await save() : null;
      const source = saved?.items ?? acknowledgedItems;
      const committed = await commitPlan.mutateAsync({
        acknowledged_human_confirmation_item_ids: source
          .filter((item) => item.selected && item.execution_kind === "human_confirmation" && !item.generated_issue_id)
          .map((item) => item.id),
      });
      setConfirmationOpen(false);
      toast.success("Issues created");
      if (committed.parent_issue_id) nav.push(paths.issueDetail(committed.parent_issue_id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to commit plan");
    }
  };

  const commit = () => {
    if (selectedHumanConfirmationItems.length > 0) {
      setConfirmationOpen(true);
      return;
    }
    void createIssues([]);
  };

  const approveSpec = async () => {
    try {
      const approved = await approvePlanSpec.mutateAsync({ spec });
      setSpecDraft(null);
      toast.success("Spec approved");
      if (approved.id) nav.push(paths.planDetail(approved.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to approve spec");
    }
  };

  const answerOpenQuestion = async (question: string) => {
    const answer = (clarificationAnswers[question] ?? "").trim();
    if (answer.length === 0) {
      toast.error("Write an answer or leave the question unanswered");
      return;
    }
    setClarifyingQuestion(question);
    try {
      const clarified = await clarifyPlanSpec.mutateAsync({ spec, answers: [{ question, answer }] });
      setClarificationAnswers((current) => {
        const next = { ...current };
        delete next[question];
        return next;
      });
      setSpecDraft(null);
      toast.success("Answer sent to planner");
      if (clarified.status !== "planning") setClarifyingQuestion(null);
    } catch (e) {
      setClarifyingQuestion(null);
      toast.error(e instanceof Error ? e.message : "Failed to send answers");
    }
  };

  return (
    <div className="flex h-full flex-col bg-muted/35 p-2">
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-background shadow-sm">
      {/* ── Header ── */}
      <PageHeader>
        <div className="flex w-full items-center gap-4">
          {/* Title + breadcrumb */}
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <Button variant="ghost" size="icon" className="shrink-0" onClick={() => nav.push(paths.plans())}>
              <ArrowLeft className="h-4 w-4" />
            </Button>
            <div className="flex min-w-0 items-baseline gap-1.5">
              <span className="shrink-0 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/35">
                PLAN
              </span>
              <span className="text-muted-foreground/25">/</span>
              <h1 className="min-w-0 truncate text-sm font-semibold">{plan.title}</h1>
            </div>
          </div>

          <PlanStepTimeline status={status} />

          {/* Header actions */}
          <div className="flex flex-1 items-center justify-end gap-2">
            {status === "failed" && (
              <Button size="sm" disabled={rerunPlan.isPending} onClick={() => rerunPlan.mutate()}>
                <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", rerunPlan.isPending && "animate-spin")} />
                Rerun
              </Button>
            )}
            {status === "ready" && (
              <>
                <Button variant="ghost" size="sm" disabled={rerunPlan.isPending} onClick={() => rerunPlan.mutate()}>
                  <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", rerunPlan.isPending && "animate-spin")} />
                  Rerun
                </Button>
                <Button variant="outline" size="sm" disabled={updatePlan.isPending} onClick={saveWithToast}>
                  <Save className="mr-1.5 h-3.5 w-3.5" />
                  Save
                </Button>
                <Button size="sm" disabled={commitPlan.isPending || updatePlan.isPending} onClick={commit}>
                  {commitPlan.isPending ? (
                    <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <GitBranch className="mr-1.5 h-3.5 w-3.5" />
                  )}
                  Create Issues
                </Button>
              </>
            )}
          </div>
        </div>
      </PageHeader>

      {/* ── Body ── */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <div ref={scrollRootRef} className="flex-1 overflow-auto">
          {status === "planning" && !isClarifyingSpec ? (
            <div className="w-full space-y-4 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
              <PlanningFlowSkeleton />
              <div className="flex items-center justify-center gap-2.5 py-1">
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-primary/60" style={{ animationDuration: "1.6s" }} />
                <p className="font-mono text-[10px] font-semibold uppercase tracking-widest text-muted-foreground/50">
                  Generating spec — page refreshes automatically
                </p>
              </div>
            </div>
          ) : (
            <div className="mx-auto grid w-full max-w-[1520px] gap-6 px-4 py-7 sm:px-6 lg:grid-cols-[minmax(0,1fr)_18rem] lg:px-8">
              <main className="min-w-0 space-y-8">
              {plan.error && (
                <div className="rounded-md border border-destructive/25 bg-destructive/5 px-4 py-3">
                  <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-destructive/60 mb-1">Error</p>
                  <p className="text-sm text-destructive">{plan.error}</p>
                </div>
              )}

              {status === "spec_review" || isClarifyingSpec ? (
                <div className="space-y-8">
                  <SpecDocument
                    spec={spec}
                    editable={status === "spec_review" && specEditable}
                    isCommitted={false}
                    status={status}
                    planTitle={plan.title}
                    parentDescription={effectiveParentDescription}
                    onChange={setSpecDraft}
                  />
                  <SpecConversation
                    spec={spec}
                    answers={clarificationAnswers}
                    pending={clarifyPlanSpec.isPending}
                    pendingQuestion={clarifyingQuestion}
                    canAnswer={status === "spec_review" && !isClarifyingSpec}
                    onAnswerChange={(question, answer) => setClarificationAnswers((current) => ({ ...current, [question]: answer }))}
                    onSubmit={answerOpenQuestion}
                  />
                  <WikiReferencesSection
                    projectId={plan.project_id}
                    taskId={plan.task_id}
                    isLoading={wikiTraceQuery.isLoading}
                    logs={wikiTraceQuery.data?.retrieval_logs ?? []}
                  />
                </div>
              ) : itemsVisible ? (
                <div className="space-y-8">
                  <SpecDocument
                    spec={spec}
                    editable={specEditable}
                    isCommitted={status === "committed"}
                    status={status}
                    planTitle={plan.title}
                    parentDescription={effectiveParentDescription}
                    onChange={setSpecDraft}
                  />

                  <WikiReferencesSection
                    projectId={plan.project_id}
                    taskId={plan.task_id}
                    isLoading={wikiTraceQuery.isLoading}
                    logs={wikiTraceQuery.data?.retrieval_logs ?? []}
                  />

                  <div id="pipeline">
                    <PlanDocSectionHeader number="09" title="执行流水线 / Pipeline" meta={`${items.filter((i) => i.selected).length} active`} />
                    <div className="mt-5">
                      <PlanItemsFlowGraph items={items} agentsById={agentsById} issuesById={issuesById} />
                    </div>
                  </div>

                  <TasksSection
                    status={status}
                    items={items}
                    effectiveParentTitle={effectiveParentTitle}
                    effectiveParentDescription={effectiveParentDescription}
                    editable={editable}
                    agents={agents}
                    agentsById={agentsById}
                    issuesById={issuesById}
                    onParentTitleChange={setParentTitle}
                    onParentDescriptionChange={setParentDescription}
                    onChangeItem={changeItem}
                  />
                </div>
              ) : (
                <div className="space-y-8">
                  <SpecDocument
                    spec={spec}
                    editable={specEditable}
                    isCommitted={false}
                    status={status}
                    planTitle={plan.title}
                    parentDescription={effectiveParentDescription}
                    onChange={setSpecDraft}
                  />
                  <WikiReferencesSection
                    projectId={plan.project_id}
                    taskId={plan.task_id}
                    isLoading={wikiTraceQuery.isLoading}
                    logs={wikiTraceQuery.data?.retrieval_logs ?? []}
                  />
                </div>
              )}
              </main>
              <PlanDetailAside spec={spec} items={items} status={status} activeQuickLinkId={activeQuickLinkId} />
            </div>
          )}
        </div>

        {/* ── Persistent approval footer (spec_review only) ── */}
        {status === "spec_review" && (
          <div className="relative shrink-0">
            {/* Fade gradient above the bar */}
            <div className="pointer-events-none absolute -top-10 left-0 right-0 h-10 bg-gradient-to-t from-background to-transparent" />
            <div className="border-t bg-background/98 backdrop-blur-sm">
              <div className="flex w-full flex-col gap-3 px-4 py-4 sm:px-6 lg:flex-row lg:items-center lg:gap-4 lg:px-8">
                {/* Status indicator */}
                <div className="flex shrink-0 items-center gap-2 border-b pb-3 lg:self-stretch lg:border-b-0 lg:border-r lg:pb-0 lg:pr-4">
                  <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-500" style={{ animationDuration: "1.4s" }} />
                  <span className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/50">
                    Awaiting approval
                  </span>
                </div>

                {/* Message */}
                <div className="min-w-0 flex-1">
                <p className="text-sm font-semibold">准备进入下一步？ / Ready to proceed?</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {spec.open_questions.length > 0
                      ? "可以先回答关键问题，也可以直接批准，让任务拆解把它们作为假设或人工门禁。"
                      : "批准后会进入任务拆解阶段，并从当前规格生成执行项。"}
                  </p>
                </div>

                {/* Actions */}
                <div className="flex shrink-0 flex-wrap items-center gap-2">
                  <Button variant="ghost" size="sm" disabled={rerunPlan.isPending} onClick={() => rerunPlan.mutate()}>
                    <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", rerunPlan.isPending && "animate-spin")} />
                    Rerun
                  </Button>
                  <Button variant="outline" size="sm" disabled={updatePlan.isPending} onClick={saveWithToast}>
                    <Save className="mr-1.5 h-3.5 w-3.5" />
                    Save draft
                  </Button>
                  <Button size="sm" disabled={approvePlanSpec.isPending} onClick={approveSpec}>
                    {approvePlanSpec.isPending ? (
                      <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                    ) : (
                      <CheckCircle2 className="mr-1.5 h-3.5 w-3.5" />
                    )}
                    批准规格 / Approve Spec
                    {!approvePlanSpec.isPending && <ArrowRight className="ml-1.5 h-3.5 w-3.5" />}
                  </Button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      <HumanConfirmationDialog
        open={confirmationOpen}
        items={selectedHumanConfirmationItems}
        pending={commitPlan.isPending || updatePlan.isPending}
        onOpenChange={setConfirmationOpen}
        onConfirm={() => createIssues(selectedHumanConfirmationItems)}
      />
      </div>
    </div>
  );
}

function WikiReferencesSection({
  projectId,
  taskId,
  logs,
  isLoading,
}: {
  projectId: string | null;
  taskId: string;
  logs: ProjectKnowledgeRetrievalLog[];
  isLoading: boolean;
}) {
  const latest = logs[0];
  const wikiItems = latest?.selected_items.filter((item) => item.target_type === "wiki_page" || item.kind === "wiki_page") ?? [];
  const candidateItems = latest?.candidates.filter((item) => item.target_type === "wiki_page") ?? [];

  let emptyText = "尚未生成 Wiki 引用记录 / Wiki references have not been generated yet.";
  if (!projectId) emptyText = "未绑定项目，Plan 不会读取 Project Wiki / No project is bound to this Plan.";
  else if (!taskId) emptyText = "Plan task 尚未创建 / Plan task has not been created yet.";
  else if (latest && wikiItems.length === 0) emptyText = "本次 Plan 没有命中 Wiki 页面 / No Wiki pages were selected for this Plan.";

  return (
    <section id="wiki-references">
      <PlanDocSectionHeader
        number="08"
        title="Wiki 引用 / Wiki References"
        meta={latest ? latest.search_mode : projectId && taskId ? "pending" : "not bound"}
      />
      <div className="mt-4 rounded-lg border bg-background shadow-sm">
        <div className="flex flex-wrap items-center gap-3 border-b bg-muted/15 px-4 py-3">
          <BookOpenText className="h-4 w-4 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-semibold">本次 Plan 使用的 Project Wiki 证据 / Project Wiki evidence used by this Plan</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              检索模式会按实际路径标记为 hybrid、vector、keyword、fallback 或 none。
            </p>
          </div>
          {isLoading && <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />}
        </div>

        {wikiItems.length === 0 ? (
          <div className="p-4 text-sm text-muted-foreground">{emptyText}</div>
        ) : (
          <div className="divide-y">
            {wikiItems.map((item) => (
              <WikiReferenceRow key={item.id} item={item} searchMode={latest?.search_mode ?? "none"} />
            ))}
          </div>
        )}

        {latest && (
          <Collapsible>
            <CollapsibleTrigger className="flex w-full items-center justify-between border-t px-4 py-3 text-left text-xs font-semibold text-muted-foreground hover:bg-muted/20">
              <span>查看查询和候选列表 / Query and candidates</span>
              <ChevronDown className="h-4 w-4" />
            </CollapsibleTrigger>
            <CollapsibleContent className="border-t bg-muted/10 px-4 py-3">
              <div className="grid gap-3 text-xs md:grid-cols-[1fr_1fr]">
                <div>
                  <div className="mb-1 font-mono text-[10px] font-bold uppercase tracking-[0.12em] text-muted-foreground/55">Query</div>
                  <pre className="max-h-44 overflow-auto whitespace-pre-wrap rounded-md border bg-background p-3 text-[11px] leading-relaxed text-muted-foreground">
                    {latest.query_text || "—"}
                  </pre>
                </div>
                <div>
                  <div className="mb-1 font-mono text-[10px] font-bold uppercase tracking-[0.12em] text-muted-foreground/55">Candidates</div>
                  <div className="max-h-44 overflow-auto rounded-md border bg-background">
                    {candidateItems.length === 0 ? (
                      <div className="p-3 text-muted-foreground">无候选 / No candidates</div>
                    ) : (
                      candidateItems.map((candidate) => <WikiCandidateLine key={knowledgeResultKey(candidate)} candidate={candidate} />)
                    )}
                  </div>
                </div>
              </div>
            </CollapsibleContent>
          </Collapsible>
        )}
      </div>
    </section>
  );
}

function WikiReferenceRow({ item, searchMode }: { item: ProjectRelevantKnowledge; searchMode: string }) {
  return (
    <div className="grid gap-3 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h3 className="min-w-0 truncate text-sm font-bold">{item.title || item.slug || item.id}</h3>
          {item.slug && <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{item.slug}</span>}
        </div>
        <p className="mt-1 line-clamp-2 text-sm leading-relaxed text-muted-foreground">
          {item.snippet || item.summary || "No snippet"}
        </p>
        <p className="mt-2 text-xs text-muted-foreground/80">{item.source_reason || "Selected from Project Wiki context."}</p>
      </div>
      <div className="flex flex-wrap items-start gap-1.5 md:justify-end">
        <MethodChip tone={searchMode === "fallback" ? "amber" : "green"}>{searchMode}</MethodChip>
        {item.match_type && <MethodChip>{item.match_type}</MethodChip>}
        <span className="inline-flex h-6 items-center rounded-full bg-muted px-2.5 text-xs font-semibold text-muted-foreground">
          {formatKnowledgeScore(item.vector_score ?? item.keyword_score ?? item.score)}
        </span>
      </div>
    </div>
  );
}

function WikiCandidateLine({ candidate }: { candidate: ProjectKnowledgeSearchResult }) {
  const title = candidate.wiki_page?.title || candidate.wiki_page?.slug || "Wiki page";
  return (
    <div className="border-b px-3 py-2 last:border-b-0">
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate font-semibold">{title}</span>
        <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{formatKnowledgeScore(candidate.score)}</span>
      </div>
      <div className="mt-1 flex flex-wrap gap-1.5 text-[10px] text-muted-foreground">
        {candidate.match_type && <span>{candidate.match_type}</span>}
        {candidate.wiki_page?.slug && <span>{candidate.wiki_page.slug}</span>}
      </div>
    </div>
  );
}

function knowledgeResultKey(candidate: ProjectKnowledgeSearchResult) {
  return candidate.wiki_page?.id ?? candidate.memory_item?.id ?? `${candidate.target_type}-${candidate.score}`;
}

function formatKnowledgeScore(value: number | null | undefined) {
  if (typeof value !== "number" || Number.isNaN(value)) return "score n/a";
  return `score ${value.toFixed(value >= 1 ? 2 : 3)}`;
}

// ─── Spec document ────────────────────────────────────────────────────────────

const SPEC_SECTIONS = ["Summary", "Goal", "Approach"] as const;

function SpecDocument({
  spec,
  editable,
  isCommitted,
  status,
  planTitle,
  parentDescription,
  onChange,
}: {
  spec: PlanSpec;
  editable: boolean;
  isCommitted: boolean;
  status: PlanStatus;
  planTitle: string;
  parentDescription: string;
  onChange: (spec: PlanSpec) => void;
}) {
  const [open, setOpen] = useState(!isCommitted);
  const patch = (p: Partial<PlanSpec>) => onChange({ ...spec, ...p });
  const successItems = spec.success_criteria ?? [];
  const scenarios = spec.acceptance_scenarios ?? [];
  const inScope = spec.in_scope ?? [];
  const outOfScope = spec.out_of_scope ?? [];
  const designDecisions = spec.design_decisions ?? [];
  const verificationCommands = spec.verification_commands ?? [];
  const assumptions = spec.assumptions ?? [];

  const header = (
    <div className="flex items-center gap-3" id="summary">
      <SectionRule
        label="规格 / Spec"
        meta={
          isCommitted ? (
            <span className="flex items-center gap-1 text-emerald-600/80">
              <CheckCircle2 className="h-2.5 w-2.5" />
              approved
            </span>
          ) : editable ? (
            <span className="flex items-center gap-1 text-amber-600/70">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-500" />
              editing
            </span>
          ) : undefined
        }
      />
      {isCommitted && (
        <CollapsibleTrigger className="flex shrink-0 items-center gap-1 font-mono text-[9px] text-muted-foreground/40 hover:text-muted-foreground transition-colors">
          <ChevronDown className={cn("h-3 w-3 transition-transform duration-200", open && "rotate-180")} />
          {open ? "collapse" : "expand"}
        </CollapsibleTrigger>
      )}
    </div>
  );

  const body = (
    <div className="mt-6 space-y-8">
      <section className="border-b pb-6">
        <div className="mb-4 flex flex-wrap gap-2">
          <PlanStatusChip status={status} />
          <MethodChip>计划 / Plan</MethodChip>
          <MethodChip tone="amber">评审门禁 / Review-gated</MethodChip>
        </div>
        <h2 className="max-w-5xl text-2xl font-bold tracking-tight text-foreground">{planTitle}</h2>
        <p className="mt-2 max-w-5xl text-sm leading-relaxed text-muted-foreground">
          {parentDescription || spec.summary || "Plan 模式会把请求转成有边界的规格说明和可执行 Issue 计划。"}
        </p>
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.2fr_1fr]">
        <div className="rounded-lg border bg-background p-4" id="executive-summary">
          <div className="mb-4 flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-emerald-500" />
            <h3 className="font-mono text-xs font-bold uppercase tracking-[0.12em] text-muted-foreground">重点摘要 / Executive Summary</h3>
          </div>
          <div className="grid gap-3 text-sm">
            <SpecSummaryRow
              label="摘要 / Summary"
              content={<SpecInlineText value={spec.summary} editable={editable} placeholder="Brief summary of the plan" onChange={(v) => patch({ summary: v })} />}
            />
            <SpecSummaryRow
              label="目标 / Goal"
              content={<SpecInlineText value={spec.goal} editable={editable} placeholder="What is the main goal?" onChange={(v) => patch({ goal: v })} />}
            />
            <SpecSummaryRow
              label="重点 / Focus"
              content={<span>{firstNonEmpty(successItems, "等待 planner 生成重点。")}</span>}
            />
            <SpecSummaryRow
              label="边界 / Boundary"
              content={<span>{firstNonEmpty(outOfScope, "尚未声明 out-of-scope。")}</span>}
            />
            <SpecSummaryRow
              label="验收 / Verification"
              content={<span>{firstNonEmpty(verificationCommands, "通过 spec/code/human gate 验证。")}</span>}
            />
          </div>
        </div>

        <div className="rounded-lg border bg-background p-4">
          <div className="mb-4 flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-emerald-500" />
            <h3 className="font-mono text-xs font-bold uppercase tracking-[0.12em] text-muted-foreground">规划路径 / Plan Journey</h3>
          </div>
          {scenarios.length > 0 ? (
            <div className="grid gap-2 sm:grid-cols-2">
              {scenarios.slice(0, 4).map((scenario, idx) => (
                <div key={`${scenario.name}-${idx}`} className="rounded-md border bg-muted/15 p-3">
                  <div className="text-sm font-semibold">{scenario.name || `Scenario ${idx + 1}`}</div>
                  <p className="mt-1 max-h-[4.5rem] overflow-hidden text-xs leading-relaxed text-muted-foreground">
                    {scenario.when || scenario.then || scenario.given || "Acceptance path"}
                  </p>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">验收场景会在规划完成后显示。Acceptance scenarios will appear after planning.</p>
          )}
        </div>
      </section>

      <section id="success-criteria">
        <PlanDocSectionHeader number="03" title="验收标准 / Success Criteria" meta={`${successItems.length} items`} />
        <CriteriaCardStack
          value={successItems}
          editable={editable}
          emptyText="等待 planner 生成验收标准。"
          onChange={(v) => patch({ success_criteria: v })}
        />
      </section>

      <section id="acceptance-scenarios">
        <PlanDocSectionHeader number="04" title="验收场景 / Acceptance Scenarios" meta={`${scenarios.length} cases`} />
        <ScenarioCardGrid value={scenarios} editable={editable} onChange={(v) => patch({ acceptance_scenarios: v })} />
      </section>

      <section id="scope-boundary">
        <PlanDocSectionHeader number="05-06" title="范围边界 / Scope Boundary" meta="in / out" />
        <div className="mt-4 grid gap-3 xl:grid-cols-2">
          <ScopePanel title="范围内 / In Scope" value={inScope} editable={editable} onChange={(v) => patch({ in_scope: v })} />
          <ScopePanel title="范围外 / Out of Scope" value={outOfScope} editable={editable} danger onChange={(v) => patch({ out_of_scope: v })} />
        </div>
      </section>

      <section id="approach">
        <PlanDocSectionHeader number="07" title="实施路径 / Approach" meta="review-gated" />
        <ApproachPanel
          approach={spec.approach}
          designDecisions={designDecisions}
          verificationCommands={verificationCommands}
          assumptions={assumptions}
          editable={editable}
          onApproachChange={(v) => patch({ approach: v })}
          onDesignDecisionsChange={(v) => patch({ design_decisions: v })}
          onVerificationCommandsChange={(v) => patch({ verification_commands: v })}
          onAssumptionsChange={(v) => patch({ assumptions: v })}
        />
      </section>
    </div>
  );

  if (isCommitted) {
    return (
      <Collapsible open={open} onOpenChange={setOpen}>
        {header}
        <CollapsibleContent>{body}</CollapsibleContent>
      </Collapsible>
    );
  }

  return (
    <div>
      {header}
      {body}
    </div>
  );
}

function SpecSummaryRow({ label, content }: { label: string; content: React.ReactNode }) {
  return (
    <div className="grid gap-2 sm:grid-cols-[5rem_1fr]">
      <div className="text-sm font-semibold">{label}</div>
      <div className="min-w-0 text-sm leading-relaxed text-foreground/85">{content}</div>
    </div>
  );
}

function SpecInlineText({
  value,
  editable,
  placeholder,
  onChange,
}: {
  value: string;
  editable: boolean;
  placeholder: string;
  onChange: (value: string) => void;
}) {
  if (!editable) {
    return <span>{value || "—"}</span>;
  }
  return (
    <Textarea
      value={value}
      placeholder={placeholder}
      className="min-h-16 resize-none"
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function ScenarioLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[4rem_1fr] gap-3 py-1 text-sm">
      <div className="font-mono text-[11px] font-bold uppercase tracking-[0.12em] text-muted-foreground">{label}</div>
      <div className="min-w-0 leading-relaxed text-foreground/85">{value || "—"}</div>
    </div>
  );
}

function CriteriaCardStack({
  value,
  editable,
  emptyText,
  onChange,
}: {
  value: string[];
  editable: boolean;
  emptyText: string;
  onChange: (value: string[]) => void;
}) {
  const items = editable && value.length === 0 ? [""] : value;

  if (!editable && items.length === 0) {
    return <EmptyPlanCard text={emptyText} />;
  }

  return (
    <div className="mt-4 grid gap-2.5">
      {items.map((item, idx) => (
        <div key={`${idx}-${item}`} className="flex min-h-[4rem] items-start gap-4 rounded-lg border bg-background px-4 py-3 shadow-sm">
          <PriorityPill label={idx < 2 ? "P0" : "P1"} />
          {editable ? (
            <Textarea
              value={item}
              placeholder="写一条验收标准 / Write one criterion"
              className="min-h-10 flex-1 resize-none border-0 bg-transparent p-0 text-sm leading-relaxed shadow-none focus-visible:ring-0"
              onChange={(e) => onChange(replaceAt(items, idx, e.target.value))}
            />
          ) : (
            <p className="min-w-0 flex-1 text-sm leading-relaxed text-foreground/90">{item}</p>
          )}
          {editable && (
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 shrink-0 text-muted-foreground hover:text-destructive"
              title="删除 / Remove"
              aria-label="删除 / Remove"
              onClick={() => onChange(removeAt(items, idx))}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          )}
        </div>
      ))}
      {editable && (
        <Button variant="outline" size="sm" className="w-fit" onClick={() => onChange([...items, ""])}>
          添加标准 / Add criterion
        </Button>
      )}
    </div>
  );
}

function ScenarioCardGrid({
  value,
  editable,
  onChange,
}: {
  value: PlanSpec["acceptance_scenarios"];
  editable: boolean;
  onChange: (value: PlanSpec["acceptance_scenarios"]) => void;
}) {
  const items = editable && value.length === 0 ? [{ name: "", given: "", when: "", then: "" }] : value;

  if (!editable && items.length === 0) {
    return <EmptyPlanCard text="等待 planner 生成验收场景。" />;
  }

  return (
    <div className="mt-4 grid gap-3 xl:grid-cols-2">
      {items.map((scenario, idx) => (
        <div key={`${idx}-${scenario.name}`} className="rounded-lg border bg-background p-4 shadow-sm">
          <div className="mb-3 flex items-start justify-between gap-3">
            {editable ? (
              <Input
                value={scenario.name}
                placeholder={`场景 ${idx + 1} / Scenario ${idx + 1}`}
                className="h-8 min-w-0 border-0 bg-transparent px-0 text-sm font-bold shadow-none focus-visible:ring-0"
                onChange={(e) => onChange(replaceAt(items, idx, { ...scenario, name: e.target.value }))}
              />
            ) : (
              <h3 className="min-w-0 text-sm font-bold">{scenario.name || `场景 ${idx + 1} / Scenario ${idx + 1}`}</h3>
            )}
            <PriorityPill label={idx === 0 ? "P0" : "P1"} />
          </div>
          {editable ? (
            <div className="grid gap-2">
              <ScenarioEditLine label="前提 / Given" value={scenario.given} onChange={(v) => onChange(replaceAt(items, idx, { ...scenario, given: v }))} />
              <ScenarioEditLine label="动作 / When" value={scenario.when} onChange={(v) => onChange(replaceAt(items, idx, { ...scenario, when: v }))} />
              <ScenarioEditLine label="结果 / Then" value={scenario.then} onChange={(v) => onChange(replaceAt(items, idx, { ...scenario, then: v }))} />
              <Button
                variant="ghost"
                size="icon"
                className="mt-1 h-7 w-7 text-muted-foreground hover:text-destructive"
                title="删除场景 / Remove scenario"
                aria-label="删除场景 / Remove scenario"
                onClick={() => onChange(removeAt(items, idx))}
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>
          ) : (
            <>
              <ScenarioLine label="前提 / Given" value={scenario.given} />
              <ScenarioLine label="动作 / When" value={scenario.when} />
              <ScenarioLine label="结果 / Then" value={scenario.then} />
            </>
          )}
        </div>
      ))}
      {editable && (
        <Button
          variant="outline"
          size="sm"
          className="h-auto min-h-[6rem] border-dashed"
          onClick={() => onChange([...items, { name: "", given: "", when: "", then: "" }])}
        >
          添加场景 / Add scenario
        </Button>
      )}
    </div>
  );
}

function ScenarioEditLine({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label className="grid grid-cols-[4.75rem_1fr] gap-3 text-sm">
      <span className="pt-2 font-mono text-[11px] font-bold uppercase tracking-[0.12em] text-muted-foreground">{label}</span>
      <Textarea
        value={value}
        className="min-h-9 resize-none text-sm"
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}

function ScopePanel({
  title,
  value,
  editable,
  danger,
  onChange,
}: {
  title: string;
  value: string[];
  editable: boolean;
  danger?: boolean;
  onChange: (value: string[]) => void;
}) {
  return (
    <div className={cn("rounded-lg border bg-background p-4 shadow-sm", danger && "border-orange-300 bg-orange-500/10")}>
      <h3 className="mb-3 text-sm font-bold">{title}</h3>
      <CriteriaCardStack value={value} editable={editable} emptyText="无内容 / Empty" onChange={onChange} />
    </div>
  );
}

function ApproachPanel({
  approach,
  designDecisions,
  verificationCommands,
  assumptions,
  editable,
  onApproachChange,
  onDesignDecisionsChange,
  onVerificationCommandsChange,
  onAssumptionsChange,
}: {
  approach: string;
  designDecisions: string[];
  verificationCommands: string[];
  assumptions: string[];
  editable: boolean;
  onApproachChange: (value: string) => void;
  onDesignDecisionsChange: (value: string[]) => void;
  onVerificationCommandsChange: (value: string[]) => void;
  onAssumptionsChange: (value: string[]) => void;
}) {
  return (
    <div className="mt-4 overflow-hidden rounded-lg border bg-background shadow-sm">
      <div className="border-b bg-muted/20 p-4">
        <div className="mb-3 flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-emerald-500" />
          <h3 className="font-mono text-xs font-bold uppercase tracking-[0.12em] text-muted-foreground">
            核心路径 / Core Path
          </h3>
        </div>
        {editable ? (
          <Textarea
            value={approach}
            placeholder="写清楚实现策略、顺序、门禁和验证方式 / Describe implementation strategy, sequencing, gates, and verification."
            className="min-h-28 resize-none bg-background text-sm leading-relaxed"
            onChange={(e) => onApproachChange(e.target.value)}
          />
        ) : (
          <p className={cn("text-sm leading-relaxed text-foreground/90", !approach && "italic text-muted-foreground/45")}>
            {approach || "等待 planner 生成实施路径。"}
          </p>
        )}
      </div>

      <div className="grid gap-3 p-4 md:grid-cols-3">
        <ApproachListCard
          title="状态模型 / State Model"
          subtitle="设计决策 / Design Decisions"
          value={designDecisions}
          editable={editable}
          tone="emerald"
          onChange={onDesignDecisionsChange}
        />
        <ApproachListCard
          title="验证命令 / Verification"
          subtitle="Build / Lint / Test"
          value={verificationCommands}
          editable={editable}
          tone="blue"
          onChange={onVerificationCommandsChange}
        />
        <ApproachListCard
          title="人工确认 / Human Gate"
          subtitle="假设 / Assumptions"
          value={assumptions}
          editable={editable}
          tone="amber"
          onChange={onAssumptionsChange}
        />
      </div>
    </div>
  );
}

function ApproachListCard({
  title,
  subtitle,
  value,
  editable,
  tone,
  onChange,
}: {
  title: string;
  subtitle: string;
  value: string[];
  editable: boolean;
  tone: "emerald" | "blue" | "amber";
  onChange: (value: string[]) => void;
}) {
  const accentClass =
    tone === "emerald"
      ? "bg-emerald-500"
      : tone === "blue"
        ? "bg-blue-500"
        : "bg-amber-500";

  return (
    <div className="overflow-hidden rounded-lg border bg-muted/20">
      <div className={cn("h-1", accentClass)} />
      <div className="p-3">
        <div className="text-sm font-bold">{title}</div>
        <div className="mt-1 font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground/55">
          {subtitle}
        </div>
        {editable ? (
          <Textarea
            value={value.join("\n")}
            placeholder="一行一项 / One item per line"
            className="mt-3 min-h-24 resize-none bg-background text-sm leading-relaxed"
            onChange={(e) => onChange(parseLineList(e.target.value))}
          />
        ) : value.length === 0 ? (
          <p className="mt-3 text-sm italic text-muted-foreground/45">无内容 / Empty</p>
        ) : (
          <ul className="mt-3 space-y-2">
            {value.map((item, idx) => (
              <li key={`${item}-${idx}`} className="flex items-start gap-2 text-sm leading-relaxed text-foreground/85">
                <span className={cn("mt-[0.55rem] h-1.5 w-1.5 shrink-0 rounded-full", accentClass)} />
                <span className="min-w-0">{item}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

function EmptyPlanCard({ text = "Waiting for planner output." }: { text?: string }) {
  return (
    <div className="rounded-lg border border-dashed bg-muted/15 p-4 text-sm text-muted-foreground">
      {text}
    </div>
  );
}

function PlanDetailAside({
  spec,
  items,
  status,
  activeQuickLinkId,
}: {
  spec: PlanSpec;
  items: PlanItem[];
  status: PlanStatus;
  activeQuickLinkId: (typeof PLAN_QUICK_LINKS)[number]["id"];
}) {
  const activeItems = items.filter((item) => item.selected);
  const reviewFocus = [
    ...(spec.open_questions ?? []),
    ...(spec.assumptions ?? []),
    ...activeItems.flatMap((item) => item.risk_notes ?? []),
  ].filter((item) => item.trim().length > 0);
  const verification = [
    ...(spec.verification_commands ?? []),
    ...activeItems.flatMap((item) => item.suggested_test_commands ?? []),
  ].filter((item) => item.trim().length > 0);
  return (
    <aside className="hidden lg:block">
      <div className="sticky top-6 space-y-4">
        <div className="rounded-lg border bg-background p-4">
          <h3 className="mb-3 text-sm font-bold">快速定位 / Jump</h3>
          <div className="space-y-1">
            {PLAN_QUICK_LINKS.map(({ label, href, id, code }) => (
              <a
                key={href}
                href={href}
                className={cn(
                  "flex items-center justify-between rounded-md px-2.5 py-2 text-sm transition-colors hover:bg-muted",
                  activeQuickLinkId === id && "bg-emerald-500/10 text-emerald-800",
                )}
              >
                <span>{label}</span>
                <span className="font-mono text-[10px] font-bold text-muted-foreground">{code}</span>
              </a>
            ))}
          </div>
        </div>

        <div className="rounded-lg border bg-background p-4">
          <h3 className="mb-3 text-sm font-bold">评审重点 / Review Focus</h3>
          <div className="space-y-3">
            {(reviewFocus.length > 0 ? reviewFocus.slice(0, 4) : [`当前状态 / Current status: ${status}`]).map((item, idx) => (
              <p key={`${item}-${idx}`} className="border-l-2 border-amber-500 pl-3 text-sm leading-relaxed text-foreground/85">
                {item}
              </p>
            ))}
          </div>
        </div>

        <div className="rounded-lg border bg-background p-4" id="verification">
          <h3 className="mb-3 text-sm font-bold">验证 / Verification</h3>
          <div className="grid grid-cols-2 gap-2">
            {(verification.length > 0 ? verification.slice(0, 4) : ["规格评审 / Spec review", "代码评审 / Code review", "人工门禁 / Manual gate", "完成检查 / Final check"]).map((item, idx) => (
              <div key={`${item}-${idx}`} className="rounded-md bg-muted/35 p-3">
                <div className="text-xs font-bold">{idx === 0 ? "构建 / Build" : idx === 1 ? "检查 / Lint" : idx === 2 ? "手动 / Manual" : "门禁 / Gate"}</div>
                <p className="mt-1 max-h-10 overflow-hidden text-xs leading-relaxed text-muted-foreground">{item}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </aside>
  );
}

// Suppress unused variable — SPEC_SECTIONS is intentionally defined for future use
void SPEC_SECTIONS;

function SpecConversation({
  spec,
  answers,
  pending,
  pendingQuestion,
  canAnswer,
  onAnswerChange,
  onSubmit,
}: {
  spec: PlanSpec;
  answers: Record<string, string>;
  pending: boolean;
  pendingQuestion: string | null;
  canAnswer: boolean;
  onAnswerChange: (question: string, answer: string) => void;
  onSubmit: (question: string) => void;
}) {
  const answered = spec.clarifications ?? [];
  const openQuestions = spec.open_questions ?? [];
  const busyQuestion = pendingQuestion;

  if (answered.length === 0 && openQuestions.length === 0) {
    return null;
  }

  return (
    <div>
      <SectionRule
        label="沟通记录 / Conversation"
        meta={openQuestions.length > 0 ? "可选回答 / optional" : "resolved"}
      />
      <div className="mt-5 space-y-4">
        {answered.map((item, idx) => (
          <div key={`${item.question}-${idx}`} className="rounded-lg border bg-background p-4">
            <div className="flex items-start gap-3">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <Bot className="h-3.5 w-3.5" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/50">
                  Thread {String(idx + 1).padStart(2, "0")} / answered
                </p>
                <p className="mt-1 text-sm leading-relaxed text-foreground/85">{item.question}</p>
              </div>
            </div>
            <div className="mt-3 ml-10 rounded-md border bg-primary/5 px-3 py-2">
              <div className="mb-1 flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-widest text-primary/70">
                <User className="h-3 w-3" />
                你的回答 / Your answer
              </div>
              <p className="text-sm leading-relaxed text-foreground/85">{item.answer}</p>
            </div>
            {busyQuestion === item.question && (
              <div className="mt-3 ml-10 flex items-start gap-2 rounded-md border border-sky-500/20 bg-sky-500/5 px-3 py-2 text-sm text-sky-800 dark:text-sky-200">
                <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin" />
                <span>Agent 正在根据这个回答更新规格；可能关闭此问题，也可能生成新的跟进问题。</span>
              </div>
            )}
          </div>
        ))}

        {openQuestions.map((question, idx) => (
          <div key={question} className="rounded-lg border border-amber-500/20 bg-amber-500/[0.03] p-4">
            <div className="flex items-start gap-2.5">
              <span className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-amber-500/10 text-amber-700">
                <MessageSquare className="h-3.5 w-3.5" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-amber-600/60">
                  Thread {String(answered.length + idx + 1).padStart(2, "0")} / open
                </p>
                <p className="mt-1 text-sm leading-relaxed text-foreground/85">{question}</p>
              </div>
            </div>
            <div className="mt-3 ml-10 space-y-2">
              <Textarea
                value={answers[question] ?? ""}
                disabled={!canAnswer || pending}
                placeholder="可选回答：不确定也可以先不答 / Optional: answer only if it changes the plan"
                className="min-h-20 resize-none bg-background"
                onChange={(e) => onAnswerChange(question, e.target.value)}
              />
              <div className="flex items-center justify-between gap-3">
                <p className="text-xs text-muted-foreground">
                  每个开放问题是独立 thread；回答后只等待这个 thread 的 Agent 更新。
                </p>
                <Button
                  size="sm"
                  disabled={!canAnswer || pending || (answers[question] ?? "").trim().length === 0}
                  onClick={() => onSubmit(question)}
                >
                  {busyQuestion === question ? (
                    <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Send className="mr-1.5 h-3.5 w-3.5" />
                  )}
                  Send reply
                </Button>
              </div>
            </div>
            {busyQuestion === question && (
              <div className="mt-3 ml-10 flex items-start gap-2 rounded-md border border-sky-500/20 bg-sky-500/5 px-3 py-2 text-sm text-sky-800 dark:text-sky-200">
                <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin" />
                <span>Agent 正在回复这个 thread；新的开放问题会作为新的 thread 出现。</span>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

// ─── Tasks section ────────────────────────────────────────────────────────────

function TasksSection({
  status,
  items,
  effectiveParentTitle,
  effectiveParentDescription,
  editable,
  agents,
  agentsById,
  issuesById,
  onParentTitleChange,
  onParentDescriptionChange,
  onChangeItem,
}: {
  status: PlanStatus;
  items: PlanItem[];
  effectiveParentTitle: string;
  effectiveParentDescription: string;
  editable: boolean;
  agents: { id: string; name: string; archived_at: string | null }[];
  agentsById: Map<string, { id: string; name: string }>;
  issuesById: Map<string, Issue>;
  onParentTitleChange: (v: string) => void;
  onParentDescriptionChange: (v: string) => void;
  onChangeItem: (id: string, patch: Partial<PlanItem>) => void;
}) {
  const selectedCount = items.filter((i) => i.selected).length;
  const iterationGroups = useMemo(() => groupPlanItemsByIteration(items), [items]);
  const paths = useWorkspacePaths();

  const onChangeIterationGroup = (group: PlanIterationGroup, patch: Pick<Partial<PlanItem>, "iteration_title" | "iteration_branch_name">) => {
    group.items.forEach((item) => {
      const itemPatch: Partial<PlanItem> = { ...patch };
      if (patch.iteration_branch_name !== undefined && item.requires_git_commit) {
        itemPatch.branch_name = patch.iteration_branch_name;
      }
      onChangeItem(item.id, itemPatch);
    });
  };

  return (
    <div>
      <PlanDocSectionHeader number="10" title="任务拆解 / Tasks" meta={`${selectedCount}/${items.length}`} />

      {/* Parent issue */}
      <div className="mt-5 mb-7 space-y-3 rounded-lg border bg-background p-4 shadow-sm">
        <div className="mb-3 flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-emerald-500" />
          <div className="font-mono text-xs font-bold uppercase tracking-[0.12em] text-muted-foreground">父 Issue / Parent Issue</div>
        </div>
        <Input
          value={effectiveParentTitle}
          disabled={!editable}
          placeholder="父 Issue 标题 / Parent issue title"
          className="font-medium"
          onChange={(e) => onParentTitleChange(e.target.value)}
        />
        <Textarea
          value={effectiveParentDescription}
          disabled={!editable}
          placeholder="父 Issue 描述 / Parent issue description"
          className="min-h-[4.5rem] resize-none text-sm"
          onChange={(e) => onParentDescriptionChange(e.target.value)}
        />
      </div>

      {/* Item list */}
      <div className="space-y-6">
        {iterationGroups.map((group) => {
          const groupDisabled = !editable || group.items.some((item) => !!item.generated_issue_id);
          return (
            <section key={group.key} className="space-y-3 border-t border-border pt-5 first:border-t-0 first:pt-0">
              <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_minmax(18rem,0.8fr)]">
                <div className="min-w-0">
                  <div className="mb-1 font-mono text-[10px] font-bold uppercase tracking-[0.14em] text-muted-foreground/55">
                    迭代 / Iteration {group.index}
                  </div>
                  {editable ? (
                    <Input
                      value={group.title}
                      disabled={groupDisabled}
                      placeholder={`迭代 ${group.index} 标题 / Iteration ${group.index} title`}
                      className="h-8 text-sm font-medium"
                      onChange={(e) => onChangeIterationGroup(group, { iteration_title: e.target.value })}
                    />
                  ) : (
                    <div className="truncate text-sm font-medium text-foreground">{group.title || `Iteration ${group.index}`}</div>
                  )}
                </div>
                <label className="min-w-0 grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
                  <span>共享分支 / Shared Branch</span>
                  {editable ? (
                    <Input
                      value={group.branchName}
                      disabled={groupDisabled}
                      placeholder={`feature/plan-iter-${group.index}`}
                      className="h-8 bg-background text-xs font-normal normal-case tracking-normal text-foreground"
                      onChange={(e) => onChangeIterationGroup(group, { iteration_branch_name: e.target.value })}
                    />
                  ) : (
                    <span className="truncate rounded border bg-muted/20 px-2 py-1.5 text-xs font-normal normal-case tracking-normal text-foreground">
                      {group.branchName || "未设置共享分支 / No shared branch"}
                    </span>
                  )}
                </label>
              </div>

              <div className="space-y-1.5">
                {group.items.map((item) => {
          const agent = item.recommended_agent_id ? agentsById.get(item.recommended_agent_id) : null;
          const isHuman = item.execution_kind === "human_confirmation";
          const isMerge = item.node_type === "merge";
          const hasGap = !isHuman && !isMerge && (!item.recommended_agent_id || item.match_score < 60);
          const disabled = !editable || !!item.generated_issue_id;
          const isCommitted = status === "committed";

          const accentClass = isHuman
            ? "border-l-amber-500/70"
            : isMerge
              ? "border-l-cyan-500/70"
            : hasGap
              ? "border-l-rose-500/60"
              : isCommitted && item.generated_issue_id
                ? "border-l-emerald-500/70"
                : "border-l-primary/45";

          const typeLabel = isHuman ? "human" : isMerge ? "merge" : hasGap ? `gap · ${item.match_score}%` : `${item.match_score}%`;
          const typeLabelClass = isHuman
            ? "bg-amber-500/8 text-amber-600/80 ring-amber-500/15"
            : isMerge
              ? "bg-cyan-500/8 text-cyan-700/80 ring-cyan-500/15"
            : hasGap
              ? "bg-rose-500/8 text-rose-600/80 ring-rose-500/15"
              : "bg-primary/6 text-primary/75 ring-primary/15";

          return (
            <div
              key={item.id}
              className={cn(
                "group rounded-r-md border-l-2 bg-card/40 transition-all duration-150 hover:bg-card/70",
                accentClass,
                !item.selected && "opacity-45",
                isCommitted && item.generated_issue_id && "bg-emerald-500/3",
              )}
            >
              <div className="flex items-start">
                {/* Position ordinal */}
                  <div className="flex w-9 shrink-0 justify-center pt-3.5">
                    <span className="font-mono text-[10px] font-bold tabular-nums text-muted-foreground/30">
                      {String(item.position).padStart(2, "0")}
                    </span>
                  </div>

                {/* Checkbox */}
                <div className="flex shrink-0 items-start pt-3.5 pr-3">
                  <Checkbox
                    checked={item.selected}
                    disabled={disabled}
                    onCheckedChange={(v) => onChangeItem(item.id, { selected: v === true })}
                  />
                </div>

                {/* Content */}
                <div className="min-w-0 flex-1 space-y-2 py-3 pr-3">
                  {/* Title row + type badge */}
                  <div className="flex items-start gap-2">
                    <Input
                      value={item.title}
                      disabled={disabled}
                      className="min-w-0 flex-1 font-medium"
                      onChange={(e) => onChangeItem(item.id, { title: e.target.value })}
                    />
                    <span
                      className={cn(
                        "mt-1.5 shrink-0 rounded px-1.5 py-0.5 font-mono text-[9px] font-semibold uppercase tracking-wider ring-1",
                        typeLabelClass,
                      )}
                    >
                      {typeLabel}
                    </span>
                  </div>

                  <Textarea
                    value={item.description}
                    disabled={disabled}
                    className="resize-none text-sm"
                    onChange={(e) => onChangeItem(item.id, { description: e.target.value })}
                  />

                  {/* Agent row */}
                  <div className="flex flex-wrap items-center gap-2 pt-0.5">
                    <Select
                      value={item.recommended_agent_id ?? "none"}
                      disabled={disabled || isHuman}
                      onValueChange={(v) => onChangeItem(item.id, { recommended_agent_id: v === "none" ? null : v })}
                    >
                      <SelectTrigger className="h-7 w-auto min-w-32 max-w-48 text-xs">
                        <span className="min-w-0 flex-1 truncate text-left">
                          {item.recommended_agent_id ? (agentsById.get(item.recommended_agent_id)?.name ?? "Agent") : "No suitable agent"}
                        </span>
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="none">No suitable agent</SelectItem>
                        {agents
                          .filter((a) => !a.archived_at)
                          .map((a) => (
                            <SelectItem key={a.id} value={a.id}>
                              {a.name}
                            </SelectItem>
                          ))}
                      </SelectContent>
                    </Select>

                    {/* Assignee label */}
                    <span className="flex items-center gap-1 text-xs text-muted-foreground/60">
                      {isHuman ? (
                        <>
                          <User className="h-3.5 w-3.5 shrink-0" />
                          Human confirmation
                        </>
                      ) : isMerge ? (
                        <>
                          <GitMerge className="h-3.5 w-3.5 shrink-0" />
                          {agent?.name ?? "Merge Agent"}
                        </>
                      ) : (
                        <>
                          <Bot className="h-3.5 w-3.5 shrink-0" />
                          {hasGap ? (item.missing_capability || "No suitable agent") : (agent?.name ?? "Agent")}
                        </>
                      )}
                    </span>

                    {/* Committed issue link */}
                    {isCommitted && item.generated_issue_id && issuesById.get(item.generated_issue_id) && (
                      <AppLink
                        href={paths.issueDetail(item.generated_issue_id!)}
                        className="inline-flex items-center gap-1 rounded border bg-emerald-500/8 px-1.5 py-0.5 text-[10px] text-emerald-700 ring-1 ring-emerald-500/20 hover:bg-emerald-500/15"
                      >
                        <CheckCircle2 className="h-2.5 w-2.5" />
                        {issuesById.get(item.generated_issue_id)?.identifier}
                      </AppLink>
                    )}
                  </div>

                  {/* Contract + deps */}
                  <PlanItemContractEditor
                    item={item}
                    iterationBranchName={group.branchName}
                    disabled={disabled}
                    onChange={(patch) =>
                      onChangeItem(item.id, {
                        ...patch,
                        ...(patch.execution_kind === "human_confirmation"
                          ? {
                              recommended_agent_id: null,
                              match_score: 0,
                              match_reason: "Waiting for human confirmation.",
                              requires_git_commit: false,
                              branch_name: "",
                            }
                          : patch.execution_kind === "agent_task"
                            ? item.node_type === "merge"
                              ? { requires_git_commit: false, branch_name: "" }
                              : { requires_git_commit: true, branch_name: group.branchName }
                          : {}),
                      })
                    }
                  />

                  {/* Dependencies */}
                  <div className="rounded border border-dashed border-border/50 bg-muted/10 p-2.5">
                    <div className="mb-1.5 flex items-center gap-1.5">
                      <GitBranch className="h-3 w-3 text-muted-foreground/40" />
                      <span className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">
                        Depends on
                      </span>
                    </div>
                    {editable && !item.generated_issue_id && (
                      <Input
                        value={formatPositions(item.depends_on_positions)}
                        placeholder="Item positions, e.g. 1, 2"
                        className="mb-2 h-7 text-xs"
                        onChange={(e) => onChangeItem(item.id, { depends_on_positions: parsePositions(e.target.value, item.position) })}
                      />
                    )}
                    <PlanDependencySummary item={item} items={items} issuesById={issuesById} />
                  </div>

                  {hasGap && (
                    <Input
                      value={item.missing_capability}
                      disabled={disabled}
                      placeholder="Missing capability"
                      className="text-xs"
                      onChange={(e) => onChangeItem(item.id, { missing_capability: e.target.value })}
                    />
                  )}
                </div>
              </div>
            </div>
          );
                })}
              </div>
            </section>
          );
        })}
      </div>
    </div>
  );
}

// ─── Contract editor ──────────────────────────────────────────────────────────

function PlanItemContractEditor({
  item,
  iterationBranchName,
  disabled,
  onChange,
}: {
  item: PlanItem;
  iterationBranchName: string;
  disabled: boolean;
  onChange: (patch: Partial<PlanItem>) => void;
}) {
  const isMerge = item.node_type === "merge";
  return (
    <div className="rounded-md border border-dashed border-border/50 bg-muted/10 p-3">
      <div className="mb-3 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">
        执行契约 / Execution Contract
      </div>
      <div className="mb-3 grid gap-1.5">
        <div className="font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">类型 / Kind</div>
        <Select
          value={item.execution_kind}
          disabled={disabled}
          onValueChange={(v) => onChange({ execution_kind: v === "human_confirmation" ? "human_confirmation" : "agent_task" })}
        >
          <SelectTrigger className="h-8 bg-background text-xs">
            <span className="min-w-0 flex-1 truncate text-left">
              {item.execution_kind === "human_confirmation" ? "人工确认 / Human confirmation" : "Agent 任务 / Agent task"}
            </span>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="agent_task">Agent 任务 / Agent task</SelectItem>
            <SelectItem value="human_confirmation">人工确认 / Human confirmation</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {item.execution_kind !== "human_confirmation" && isMerge && (
        <div className="mb-3 grid gap-1.5 rounded-md border border-cyan-500/20 bg-cyan-500/5 p-3 text-xs text-cyan-900/80">
          <div className="font-medium">合入 / 集成 · Merge / Integrate</div>
          <div className="text-muted-foreground">
            Uses the confirmed iteration branch and records PR or merge result. This node does not create a new work commit.
          </div>
        </div>
      )}

      {item.execution_kind !== "human_confirmation" && !isMerge && (
        <div className="mb-3 grid gap-2.5 rounded-md border bg-background p-3">
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Checkbox
              checked={item.requires_git_commit}
              disabled={disabled}
              onCheckedChange={(v) => onChange({ requires_git_commit: v === true, branch_name: v === true ? iterationBranchName : "" })}
            />
            <span>需要 Git 提交 / Git commit expected</span>
          </label>
          <div className="truncate font-mono text-[10px] text-muted-foreground/55">
            Uses iteration branch: {item.requires_git_commit ? iterationBranchName || "not set" : "no commit"}
          </div>
        </div>
      )}

      {item.execution_kind === "human_confirmation" && (
        <div className="mb-3 grid gap-2.5 rounded-md border bg-background p-3">
          <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
            <span>确认问题 / Confirmation question</span>
            <Textarea
              value={item.confirmation_question}
              disabled={disabled}
              className="min-h-16 resize-none text-sm font-normal normal-case tracking-normal text-foreground"
              onChange={(e) => onChange({ confirmation_question: e.target.value })}
            />
          </label>
          <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
            <span>确认原因 / Confirmation reason</span>
            <Textarea
              value={item.confirmation_reason}
              disabled={disabled}
              className="min-h-16 resize-none text-sm font-normal normal-case tracking-normal text-foreground"
              onChange={(e) => onChange({ confirmation_reason: e.target.value })}
            />
          </label>
          <ContractListField label="必要证据 / Required evidence" value={item.required_evidence} disabled={disabled} onChange={(v) => onChange({ required_evidence: v })} />
          <p className="font-mono text-[9px] text-muted-foreground/40">Downstream work waits until a human marks the created confirmation issue done.</p>
        </div>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        <ContractListField label="验收标准 / Acceptance criteria" value={item.acceptance_criteria} disabled={disabled} onChange={(v) => onChange({ acceptance_criteria: v })} />
        <ContractListField label="建议测试命令 / Suggested test commands" value={item.suggested_test_commands} disabled={disabled} onChange={(v) => onChange({ suggested_test_commands: v })} />
        <ContractUnitTestField value={item.unit_test_checklist} disabled={disabled} onChange={(v) => onChange({ unit_test_checklist: v })} />
        <ContractListField label="上下文资源 / Context resources" value={item.context_resources} disabled={disabled} onChange={(v) => onChange({ context_resources: v })} />
        <ContractListField label="风险备注 / Risks and notes" value={item.risk_notes} disabled={disabled} onChange={(v) => onChange({ risk_notes: v })} />
      </div>
    </div>
  );
}

function ContractListField({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: string[] | undefined;
  disabled: boolean;
  onChange: (value: string[]) => void;
}) {
  return (
    <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
      <span>{label}</span>
      <Textarea
        value={(value ?? []).join("\n")}
        disabled={disabled}
        placeholder={label}
        className="min-h-20 resize-none bg-background text-sm font-normal normal-case tracking-normal text-foreground"
        onChange={(e) => onChange(parseLineList(e.target.value))}
      />
    </label>
  );
  }

function ContractUnitTestField({
  value,
  disabled,
  onChange,
}: {
  value: PlanItem["unit_test_checklist"] | undefined;
  disabled: boolean;
  onChange: (value: PlanItem["unit_test_checklist"]) => void;
}) {
  return (
    <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
      <span>单元测试清单 / Unit test checklist</span>
      <Textarea
        value={(value ?? []).map((check) => check.command || check.title).join("\n")}
        disabled={disabled}
        placeholder="One runnable unit test command per line"
        className="min-h-20 resize-none bg-background text-sm font-normal normal-case tracking-normal text-foreground"
        onChange={(e) => onChange(parseUnitTestChecklist(e.target.value))}
      />
    </label>
  );
}

function parseUnitTestChecklist(raw: string): PlanItem["unit_test_checklist"] {
  return parseLineList(raw).map((line) => ({
    id: unitTestLineID(line),
    title: line,
    command: line,
    expected: "passes",
    required: true,
    status: "pending",
    last_run_at: null,
    output_excerpt: "",
    failure_summary: "",
    task_id: "",
  }));
}

function unitTestLineID(line: string): string {
  const slug = line
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return slug || "unit-test";
}

  // ─── Dependency summary ───────────────────────────────────────────────────────

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
  const deps = (item.depends_on_positions ?? []).map((pos) => ({
    pos,
    dep: items.find((c) => c.position === pos),
  }));

  if (deps.length === 0) {
    return <p className="font-mono text-[9px] text-muted-foreground/35">none</p>;
  }

  return (
    <div className="flex flex-wrap gap-1.5">
      {deps.map(({ pos, dep }) => {
        const issue = dep?.generated_issue_id ? issuesById.get(dep.generated_issue_id) : undefined;
        const label = dep ? `#${pos} ${dep.title}` : `#${pos}`;
        if (issue) {
          return (
            <AppLink
              key={pos}
              href={paths.issueDetail(issue.id)}
              className="inline-flex max-w-full items-center gap-1.5 rounded border bg-background px-2 py-1 text-xs text-muted-foreground hover:bg-accent"
            >
              <StatusIcon status={issue.status} className="h-3.5 w-3.5 shrink-0" />
              <span className="shrink-0 font-mono tabular-nums">{issue.identifier}</span>
              <span className="truncate">{issue.title}</span>
            </AppLink>
          );
        }
        return (
          <span key={pos} className="inline-flex max-w-full items-center rounded border bg-background px-2 py-1 text-xs text-muted-foreground">
            <span className="truncate">{label}</span>
          </span>
        );
      })}
    </div>
  );
}

// ─── Human confirmation dialog ────────────────────────────────────────────────

function HumanConfirmationDialog({
  open,
  items,
  pending,
  onOpenChange,
  onConfirm,
}: {
  open: boolean;
  items: PlanItem[];
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className="max-w-lg">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <CheckCircle2 className="h-5 w-5" />
          </AlertDialogMedia>
          <AlertDialogTitle>Confirm manual gates</AlertDialogTitle>
          <AlertDialogDescription>
            Creating issues will add these human confirmation steps as blocking workflow items. Downstream agent work waits until each one is marked done.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="max-h-72 space-y-2 overflow-auto">
          {items.map((item) => (
            <div key={item.id} className="rounded-md border bg-background p-3 text-sm">
              <p className="font-medium">{item.title}</p>
              <p className="mt-1 text-muted-foreground">{item.confirmation_question || item.title}</p>
              {item.confirmation_reason && <p className="mt-2 text-xs text-muted-foreground">{item.confirmation_reason}</p>}
              {item.required_evidence.length > 0 && (
                <p className="mt-2 text-xs text-muted-foreground">Required evidence: {item.required_evidence.join("; ")}</p>
              )}
            </div>
          ))}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={onConfirm}>
            Create Issues
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// ─── Utilities ────────────────────────────────────────────────────────────────

function parseLineList(value: string) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const line of value.split("\n")) {
    const item = line.trim();
    if (!item || seen.has(item)) continue;
    seen.add(item);
    out.push(item);
  }
  return out;
}

function emptyPlanSpec(): PlanSpec {
  return {
    summary: "",
    goal: "",
    success_criteria: [],
    acceptance_scenarios: [],
    in_scope: [],
    out_of_scope: [],
    approach: "",
    design_decisions: [],
    verification_commands: [],
    assumptions: [],
    open_questions: [],
    clarifications: [],
  };
}

type PlanIterationGroup = {
  key: string;
  index: number;
  title: string;
  branchName: string;
  items: PlanItem[];
};

function groupPlanItemsByIteration(items: PlanItem[]): PlanIterationGroup[] {
  const groups = new Map<number, PlanIterationGroup>();
  for (const item of items) {
    const index = item.iteration_index > 0 ? item.iteration_index : 1;
    let group = groups.get(index);
    if (!group) {
      group = {
        key: `iteration-${index}`,
        index,
        title: "",
        branchName: "",
        items: [],
      };
      groups.set(index, group);
    }
    if (!group.title && item.iteration_title) {
      group.title = item.iteration_title;
    }
    if (!group.branchName && item.iteration_branch_name) {
      group.branchName = item.iteration_branch_name;
    }
    if (!group.branchName && item.requires_git_commit && item.branch_name) {
      group.branchName = item.branch_name;
    }
    group.items.push(item);
  }
  return Array.from(groups.values()).sort((a, b) => a.index - b.index);
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
