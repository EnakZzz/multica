"use client";

import { Fragment, useMemo, useState } from "react";
import { ArrowLeft, ArrowRight, Bot, CheckCircle2, ChevronDown, GitBranch, Loader2, MessageSquare, RefreshCw, Save, Send, User } from "lucide-react";
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
import { useApprovePlanSpec, useClarifyPlanSpec, useCommitPlan, useRerunPlan, useUpdatePlan } from "@multica/core/plans/mutations";
import type { Issue, PlanItem, PlanSpec } from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { AppLink, useNavigation } from "../../navigation";
import { StatusIcon } from "../../issues/components";
import { PlanItemsFlowGraph, PlanningFlowSkeleton } from "./plan-flow-graph";

type PlanStatus = "planning" | "spec_review" | "ready" | "failed" | "committed";

const PLAN_STEPS: { key: PlanStatus; label: string }[] = [
  { key: "planning", label: "Planning" },
  { key: "spec_review", label: "Spec Review" },
  { key: "ready", label: "Ready" },
  { key: "committed", label: "Done" },
];

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
      <span className="shrink-0 font-mono text-[9px] font-bold uppercase tracking-[0.2em] text-muted-foreground/40">
        {label}
      </span>
      <div className="h-px flex-1 bg-border/60" />
      {meta && <span className="shrink-0 font-mono text-[9px] text-muted-foreground/35">{meta}</span>}
    </div>
  );
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
  const updatePlan = useUpdatePlan(wsId, planId);
  const rerunPlan = useRerunPlan(wsId, planId);
  const approvePlanSpec = useApprovePlanSpec(wsId, planId);
  const clarifyPlanSpec = useClarifyPlanSpec(wsId, planId);
  const commitPlan = useCommitPlan(wsId, planId);
  const [dirtyItems, setDirtyItems] = useState<PlanItem[] | null>(null);
  const [specDraft, setSpecDraft] = useState<PlanSpec | null>(null);
  const [clarificationAnswers, setClarificationAnswers] = useState<Record<string, string>>({});
  const [parentTitle, setParentTitle] = useState("");
  const [parentDescription, setParentDescription] = useState("");
  const [confirmationOpen, setConfirmationOpen] = useState(false);

  const items = dirtyItems ?? plan?.items ?? [];
  const persistedSpec = plan?.status === "committed" ? (plan.committed_spec ?? plan.spec) : plan?.spec;
  const spec = specDraft ?? persistedSpec ?? emptyPlanSpec();
  const agentsById = useMemo(() => new Map(agents.map((a) => [a.id, a])), [agents]);
  const issuesById = useMemo(() => new Map(issues.map((issue) => [issue.id, issue])), [issues]);

  if (!plan) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const status = plan.status as PlanStatus;
  const effectiveParentTitle = parentTitle || plan.parent_title || plan.title;
  const effectiveParentDescription = parentDescription || plan.parent_description;
  const editable = status !== "committed";
  const specEditable = status === "spec_review";
  const itemsVisible = status === "ready" || status === "committed";
  const specOpenQuestionCount = status === "spec_review" ? spec.open_questions.length : 0;

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

  const answerOpenQuestions = async () => {
    const answers = spec.open_questions
      .map((question) => ({ question, answer: (clarificationAnswers[question] ?? "").trim() }))
      .filter((answer) => answer.answer.length > 0);
    if (answers.length === 0) {
      toast.error("Answer at least one planner question");
      return;
    }
    try {
      const clarified = await clarifyPlanSpec.mutateAsync({ spec, answers });
      setClarificationAnswers({});
      setSpecDraft(null);
      toast.success("Answers sent to planner");
      if (clarified.id) nav.push(paths.planDetail(clarified.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to send answers");
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
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
        <div className="flex-1 overflow-auto">
          {status === "planning" ? (
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
            <div className="w-full space-y-8 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
              {plan.error && (
                <div className="rounded-md border border-destructive/25 bg-destructive/5 px-4 py-3">
                  <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-destructive/60 mb-1">Error</p>
                  <p className="text-sm text-destructive">{plan.error}</p>
                </div>
              )}

              {status === "spec_review" ? (
                <div className="space-y-8">
                  <SpecDocument
                    spec={spec}
                    editable={specEditable}
                    isCommitted={false}
                    onChange={setSpecDraft}
                  />
                  <SpecConversation
                    spec={spec}
                    answers={clarificationAnswers}
                    pending={clarifyPlanSpec.isPending}
                    onAnswerChange={(question, answer) => setClarificationAnswers((current) => ({ ...current, [question]: answer }))}
                    onSubmit={answerOpenQuestions}
                  />
                </div>
              ) : itemsVisible ? (
                <div className="space-y-8">
                  <SpecDocument
                    spec={spec}
                    editable={specEditable}
                    isCommitted={status === "committed"}
                    onChange={setSpecDraft}
                  />

                  <div>
                    <SectionRule label="Pipeline" meta={`${items.filter((i) => i.selected).length} active`} />
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
                <SpecDocument
                  spec={spec}
                  editable={specEditable}
                  isCommitted={false}
                  onChange={setSpecDraft}
                />
              )}
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
                  <p className="text-sm font-semibold">Ready to proceed?</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {specOpenQuestionCount > 0
                      ? "You can answer key questions, or approve now and let task breakdown carry them as assumptions or manual gates."
                      : "Approving starts the task breakdown phase — items will be generated from this spec."}
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
                    Approve Spec
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
  );
}

// ─── Spec document ────────────────────────────────────────────────────────────

const SPEC_SECTIONS = ["Summary", "Goal", "Approach"] as const;

function SpecDocument({
  spec,
  editable,
  isCommitted,
  onChange,
}: {
  spec: PlanSpec;
  editable: boolean;
  isCommitted: boolean;
  onChange: (spec: PlanSpec) => void;
}) {
  const [open, setOpen] = useState(!isCommitted);
  const patch = (p: Partial<PlanSpec>) => onChange({ ...spec, ...p });

  const header = (
    <div className="flex items-center gap-3">
      <SectionRule
        label="Spec"
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
    <div className="mt-6 space-y-7">
      {/* Primary fields */}
      <div className="space-y-5 border-l-2 border-muted/80 pl-5">
        <SpecTextField label="01 · Summary" value={spec.summary} disabled={!editable} placeholder="Brief summary of the plan" onChange={(v) => patch({ summary: v })} />
        <SpecTextField label="02 · Goal" value={spec.goal} disabled={!editable} placeholder="What is the main goal?" onChange={(v) => patch({ goal: v })} />
      </div>

      {/* Grid fields */}
      <div className="grid gap-6 sm:grid-cols-2 border-l-2 border-muted/80 pl-5">
        <SpecListField label="03 · Success Criteria" value={spec.success_criteria} disabled={!editable} onChange={(v) => patch({ success_criteria: v })} />
        <SpecScenarioField label="04 · Acceptance Scenarios" value={spec.acceptance_scenarios} disabled={!editable} onChange={(v) => patch({ acceptance_scenarios: v })} />
      </div>

      <div className="grid gap-6 sm:grid-cols-2 border-l-2 border-muted/80 pl-5">
        <SpecListField label="05 · In Scope" value={spec.in_scope} disabled={!editable} onChange={(v) => patch({ in_scope: v })} />
        <SpecListField label="06 · Out of Scope" value={spec.out_of_scope} disabled={!editable} onChange={(v) => patch({ out_of_scope: v })} />
      </div>

      <div className="border-l-2 border-muted/80 pl-5">
        <SpecTextField label="07 · Approach" value={spec.approach} disabled={!editable} placeholder="How will this be implemented?" rows={4} onChange={(v) => patch({ approach: v })} />
      </div>

      <div className="grid gap-6 sm:grid-cols-2 border-l-2 border-muted/80 pl-5">
        <SpecListField label="08 · Design Decisions" value={spec.design_decisions} disabled={!editable} onChange={(v) => patch({ design_decisions: v })} />
        <SpecListField label="09 · Verification Commands" value={spec.verification_commands} disabled={!editable} onChange={(v) => patch({ verification_commands: v })} />
      </div>

      <div className="grid gap-6 sm:grid-cols-2 border-l-2 border-muted/80 pl-5">
        <SpecListField label="10 · Assumptions" value={spec.assumptions} disabled={!editable} onChange={(v) => patch({ assumptions: v })} />
        <SpecListField label="11 · Open Questions" value={spec.open_questions} disabled={!editable} onChange={(v) => patch({ open_questions: v })} />
      </div>
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

// Suppress unused variable — SPEC_SECTIONS is intentionally defined for future use
void SPEC_SECTIONS;

function SpecConversation({
  spec,
  answers,
  pending,
  onAnswerChange,
  onSubmit,
}: {
  spec: PlanSpec;
  answers: Record<string, string>;
  pending: boolean;
  onAnswerChange: (question: string, answer: string) => void;
  onSubmit: () => void;
}) {
  const answered = spec.clarifications ?? [];
  const openQuestions = spec.open_questions ?? [];
  const hasDraftAnswer = openQuestions.some((question) => (answers[question] ?? "").trim().length > 0);

  if (answered.length === 0 && openQuestions.length === 0) {
    return null;
  }

  return (
    <div>
      <SectionRule
        label="Conversation"
        meta={openQuestions.length > 0 ? `${openQuestions.length} optional` : "resolved"}
      />
      <div className="mt-5 space-y-4 border-l-2 border-primary/20 pl-5">
        {answered.map((item, idx) => (
          <div key={`${item.question}-${idx}`} className="space-y-2">
            <div className="flex items-start gap-2.5">
              <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded border bg-muted/40 text-muted-foreground">
                <MessageSquare className="h-3.5 w-3.5" />
              </span>
              <p className="min-w-0 flex-1 text-sm leading-relaxed text-foreground/85">{item.question}</p>
            </div>
            <div className="ml-8 rounded-md border bg-primary/5 px-3 py-2 text-sm leading-relaxed text-foreground/85">
              {item.answer}
            </div>
          </div>
        ))}

        {openQuestions.map((question, idx) => (
          <div key={question} className="space-y-2">
            <div className="flex items-start gap-2.5">
              <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded border bg-amber-500/8 text-amber-600">
                <MessageSquare className="h-3.5 w-3.5" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-amber-600/60">
                  Question {String(idx + 1).padStart(2, "0")}
                </p>
                <p className="mt-1 text-sm leading-relaxed text-foreground/85">{question}</p>
              </div>
            </div>
            <Textarea
              value={answers[question] ?? ""}
              disabled={pending}
              placeholder="Optional: answer this if it changes the plan"
              className="ml-8 min-h-20 w-[calc(100%-2rem)] resize-none"
              onChange={(e) => onAnswerChange(question, e.target.value)}
            />
          </div>
        ))}

        {openQuestions.length > 0 && (
          <div className="flex justify-end">
            <Button size="sm" disabled={pending || !hasDraftAnswer} onClick={onSubmit}>
              {pending ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : <Send className="mr-1.5 h-3.5 w-3.5" />}
              Send answers
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

function SpecTextField({
  label,
  value,
  disabled,
  placeholder,
  rows = 3,
  onChange,
}: {
  label: string;
  value: string;
  disabled: boolean;
  placeholder?: string;
  rows?: number;
  onChange: (value: string) => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/45">{label}</div>
      {disabled ? (
        <p className={cn("text-sm leading-relaxed text-foreground/85", !value && "italic text-muted-foreground/40")}>
          {value || "—"}
        </p>
      ) : (
        <Textarea
          value={value}
          placeholder={placeholder}
          className={cn("resize-none", rows > 3 ? "min-h-28" : "min-h-[4.5rem]")}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  );
}

function SpecListField({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: string[];
  disabled: boolean;
  onChange: (value: string[]) => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/45">{label}</div>
      {disabled ? (
        value.length === 0 ? (
          <p className="text-sm italic text-muted-foreground/35">—</p>
        ) : (
          <ul className="space-y-2">
            {value.map((item, i) => (
              <li key={i} className="flex items-start gap-2.5 text-sm leading-relaxed text-foreground/85">
                <span className="mt-[7px] h-1 w-1 shrink-0 rounded-full bg-primary/60" />
                {item}
              </li>
            ))}
          </ul>
        )
      ) : (
        <div className="grid gap-1">
          <Textarea
            value={value.join("\n")}
            placeholder={`${label} (one per line)`}
            className="min-h-20 resize-none"
            onChange={(e) => onChange(parseLineList(e.target.value))}
          />
          <span className="font-mono text-[9px] text-muted-foreground/40">one item per line</span>
        </div>
      )}
    </div>
  );
}

function SpecScenarioField({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: PlanSpec["acceptance_scenarios"];
  disabled: boolean;
  onChange: (value: PlanSpec["acceptance_scenarios"]) => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/45">{label}</div>
      {disabled ? (
        value.length === 0 ? (
          <p className="text-sm italic text-muted-foreground/35">—</p>
        ) : (
          <div className="space-y-3">
            {value.map((scenario, i) => (
              <div key={`${scenario.name}-${i}`} className="rounded-md border bg-muted/10 px-3 py-2">
                <p className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/45">
                  {scenario.name || `Scenario ${i + 1}`}
                </p>
                <div className="mt-2 space-y-1 text-sm leading-relaxed text-foreground/85">
                  <p><span className="font-medium text-muted-foreground">Given:</span> {scenario.given || "—"}</p>
                  <p><span className="font-medium text-muted-foreground">When:</span> {scenario.when || "—"}</p>
                  <p><span className="font-medium text-muted-foreground">Then:</span> {scenario.then || "—"}</p>
                </div>
              </div>
            ))}
          </div>
        )
      ) : (
        <div className="grid gap-1">
          <Textarea
            value={formatScenarioLines(value)}
            placeholder="Name | Given | When | Then"
            className="min-h-24 resize-none"
            onChange={(e) => onChange(parseScenarioLines(e.target.value))}
          />
          <span className="font-mono text-[9px] text-muted-foreground/40">one scenario per line · name | given | when | then</span>
        </div>
      )}
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
      <SectionRule label="Tasks" meta={`${selectedCount}/${items.length}`} />

      {/* Parent issue */}
      <div className="mt-5 mb-7 space-y-2 rounded-md border border-dashed border-border/60 bg-muted/10 p-4">
        <div className="mb-3 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">Parent Issue</div>
        <Input
          value={effectiveParentTitle}
          disabled={!editable}
          placeholder="Parent issue title"
          className="font-medium"
          onChange={(e) => onParentTitleChange(e.target.value)}
        />
        <Textarea
          value={effectiveParentDescription}
          disabled={!editable}
          placeholder="Parent issue description"
          className="min-h-[4.5rem] resize-none text-sm"
          onChange={(e) => onParentDescriptionChange(e.target.value)}
        />
      </div>

      {/* Item list */}
      <div className="space-y-6">
        {iterationGroups.map((group) => {
          const groupDisabled = !editable || group.items.some((item) => !!item.generated_issue_id);
          return (
            <section key={group.key} className="space-y-2 border-t border-border/60 pt-4 first:border-t-0 first:pt-0">
              <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_minmax(18rem,0.8fr)]">
                <div className="min-w-0">
                  <div className="mb-1 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">
                    Iteration {group.index}
                  </div>
                  {editable ? (
                    <Input
                      value={group.title}
                      disabled={groupDisabled}
                      placeholder={`Iteration ${group.index} title`}
                      className="h-8 text-sm font-medium"
                      onChange={(e) => onChangeIterationGroup(group, { iteration_title: e.target.value })}
                    />
                  ) : (
                    <div className="truncate text-sm font-medium text-foreground">{group.title || `Iteration ${group.index}`}</div>
                  )}
                </div>
                <label className="min-w-0 grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
                  <span>Shared Branch</span>
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
                      {group.branchName || "No shared branch"}
                    </span>
                  )}
                </label>
              </div>

              <div className="space-y-1.5">
                {group.items.map((item) => {
          const agent = item.recommended_agent_id ? agentsById.get(item.recommended_agent_id) : null;
          const isHuman = item.execution_kind === "human_confirmation";
          const hasGap = !isHuman && (!item.recommended_agent_id || item.match_score < 60);
          const disabled = !editable || !!item.generated_issue_id;
          const isCommitted = status === "committed";

          const accentClass = isHuman
            ? "border-l-amber-500/70"
            : hasGap
              ? "border-l-rose-500/60"
              : isCommitted && item.generated_issue_id
                ? "border-l-emerald-500/70"
                : "border-l-primary/45";

          const typeLabel = isHuman ? "human" : hasGap ? `gap · ${item.match_score}%` : `${item.match_score}%`;
          const typeLabelClass = isHuman
            ? "bg-amber-500/8 text-amber-600/80 ring-amber-500/15"
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
                            ? { requires_git_commit: true, branch_name: group.branchName }
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
  return (
    <div className="rounded-md border border-dashed border-border/50 bg-muted/10 p-3">
      <div className="mb-3 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">
        Execution Contract
      </div>
      <div className="mb-3 grid gap-1.5">
        <div className="font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">Kind</div>
        <Select
          value={item.execution_kind}
          disabled={disabled}
          onValueChange={(v) => onChange({ execution_kind: v === "human_confirmation" ? "human_confirmation" : "agent_task" })}
        >
          <SelectTrigger className="h-8 bg-background text-xs">
            <span className="min-w-0 flex-1 truncate text-left">
              {item.execution_kind === "human_confirmation" ? "Human confirmation" : "Agent task"}
            </span>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="agent_task">Agent task</SelectItem>
            <SelectItem value="human_confirmation">Human confirmation</SelectItem>
          </SelectContent>
        </Select>
      </div>

      {item.execution_kind !== "human_confirmation" && (
        <div className="mb-3 grid gap-2.5 rounded-md border bg-background p-3">
          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <Checkbox
              checked={item.requires_git_commit}
              disabled={disabled}
              onCheckedChange={(v) => onChange({ requires_git_commit: v === true, branch_name: v === true ? iterationBranchName : "" })}
            />
            <span>Git commit expected</span>
          </label>
          <div className="truncate font-mono text-[10px] text-muted-foreground/55">
            Uses iteration branch: {item.requires_git_commit ? iterationBranchName || "not set" : "no commit"}
          </div>
        </div>
      )}

      {item.execution_kind === "human_confirmation" && (
        <div className="mb-3 grid gap-2.5 rounded-md border bg-background p-3">
          <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
            <span>Confirmation question</span>
            <Textarea
              value={item.confirmation_question}
              disabled={disabled}
              className="min-h-16 resize-none text-sm font-normal normal-case tracking-normal text-foreground"
              onChange={(e) => onChange({ confirmation_question: e.target.value })}
            />
          </label>
          <label className="grid gap-1.5 font-mono text-[9px] font-semibold uppercase tracking-widest text-muted-foreground/40">
            <span>Confirmation reason</span>
            <Textarea
              value={item.confirmation_reason}
              disabled={disabled}
              className="min-h-16 resize-none text-sm font-normal normal-case tracking-normal text-foreground"
              onChange={(e) => onChange({ confirmation_reason: e.target.value })}
            />
          </label>
          <ContractListField label="Required evidence" value={item.required_evidence} disabled={disabled} onChange={(v) => onChange({ required_evidence: v })} />
          <p className="font-mono text-[9px] text-muted-foreground/40">Downstream work waits until a human marks the created confirmation issue done.</p>
        </div>
      )}

      <div className="grid gap-3 md:grid-cols-2">
        <ContractListField label="Acceptance criteria" value={item.acceptance_criteria} disabled={disabled} onChange={(v) => onChange({ acceptance_criteria: v })} />
        <ContractListField label="Suggested test commands" value={item.suggested_test_commands} disabled={disabled} onChange={(v) => onChange({ suggested_test_commands: v })} />
        <ContractUnitTestField value={item.unit_test_checklist} disabled={disabled} onChange={(v) => onChange({ unit_test_checklist: v })} />
        <ContractListField label="Context resources" value={item.context_resources} disabled={disabled} onChange={(v) => onChange({ context_resources: v })} />
        <ContractListField label="Risks and notes" value={item.risk_notes} disabled={disabled} onChange={(v) => onChange({ risk_notes: v })} />
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
      <span>Unit test checklist</span>
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

function formatScenarioLines(value: PlanSpec["acceptance_scenarios"]) {
  return value
    .map((scenario) => [scenario.name, scenario.given, scenario.when, scenario.then].map((part) => part.trim()).join(" | "))
    .join("\n");
}

function parseScenarioLines(value: string): PlanSpec["acceptance_scenarios"] {
  return value
    .split("\n")
    .map((line) => {
      const [name = "", given = "", when = "", then = ""] = line.split("|").map((part) => part.trim());
      return { name, given, when, then };
    })
    .filter((scenario) => scenario.name || scenario.given || scenario.when || scenario.then);
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
