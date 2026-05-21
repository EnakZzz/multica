"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { ArrowRight, ClipboardList, Loader2, Plus, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { projectListOptions } from "@multica/core/projects/queries";
import { planListOptions } from "@multica/core/plans/queries";
import { useCreatePlan } from "@multica/core/plans/mutations";
import { api } from "@multica/core/api";
import { ContentEditor, FileDropOverlay, type ContentEditorRef, useFileDropZone } from "../../editor";
import { FileUploadButton } from "@multica/ui/components/common/file-upload-button";
import { useNavigation } from "../../navigation";
import { PageHeader } from "../../layout/page-header";

// ─── Status config ─────────────────────────────────────────────────────────────

const STATUS_CONFIG: Record<string, { dot: string; label: string; badgeClass: string }> = {
  planning: {
    dot: "bg-amber-500 animate-pulse",
    label: "Planning",
    badgeClass: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  spec_review: {
    dot: "bg-amber-400",
    label: "Spec review",
    badgeClass: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  },
  ready: {
    dot: "bg-primary",
    label: "Ready",
    badgeClass: "border-primary/30 bg-primary/10 text-primary",
  },
  committed: {
    dot: "bg-emerald-500",
    label: "Done",
    badgeClass: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  },
  failed: {
    dot: "bg-rose-500",
    label: "Failed",
    badgeClass: "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-300",
  },
};

function getStatusConfig(status: string) {
  return STATUS_CONFIG[status] ?? { dot: "bg-muted-foreground", label: status, badgeClass: "border-border bg-muted text-muted-foreground" };
}

// ─── Plans page ────────────────────────────────────────────────────────────────

export function PlansPage() {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const { data, isLoading, refetch } = useQuery(planListOptions(wsId));
  const [open, setOpen] = useState(false);
  const plans = data?.plans ?? [];

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader>
        <div className="flex w-full items-center justify-between">
          <div className="flex items-center gap-2">
            <ClipboardList className="h-4 w-4" />
            <h1 className="text-sm font-semibold">Plans</h1>
            {plans.length > 0 && (
              <Badge variant="secondary" className="h-5 px-1.5 text-xs tabular-nums">
                {plans.length}
              </Badge>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="icon"
              disabled={isLoading}
              onClick={() => refetch()}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isLoading && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="mr-1 h-4 w-4" />
              New Plan
            </Button>
          </div>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-4">
        {isLoading ? (
          <PlanListSkeleton />
        ) : plans.length === 0 ? (
          <EmptyPlans onNew={() => setOpen(true)} />
        ) : (
          <div className="divide-y rounded-md border bg-background">
            {plans.map((plan, idx) => {
              const cfg = getStatusConfig(plan.status);
              return (
                <button
                  key={plan.id}
                  className={cn(
                    "group flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50",
                  )}
                  onClick={() => nav.push(paths.planDetail(plan.id))}
                >
                  <span className="w-5 shrink-0 text-xs tabular-nums text-muted-foreground">
                    {String(idx + 1).padStart(2, "0")}
                  </span>

                  <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border bg-muted/30">
                    <span className={cn("h-2 w-2 rounded-full", cfg.dot)} />
                  </div>

                  <div className="min-w-0 flex-1 space-y-0.5">
                    <div className="truncate text-sm font-medium text-foreground">{plan.title}</div>
                    <div className="truncate text-xs text-muted-foreground">
                      {plan.prompt || "No prompt"}
                    </div>
                  </div>

                  <Badge variant="outline" className={cn("shrink-0", cfg.badgeClass)}>
                    {cfg.label}
                  </Badge>

                  <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                </button>
              );
            })}
          </div>
        )}
      </div>

      <NewPlanDialog open={open} onOpenChange={setOpen} />
    </div>
  );
}

// ─── Empty state ───────────────────────────────────────────────────────────────

function EmptyPlans({ onNew }: { onNew: () => void }) {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="flex max-w-sm flex-col items-center gap-4 rounded-md border bg-background px-6 py-8 text-center">
        <div className="flex h-11 w-11 items-center justify-center rounded-md border bg-muted/40">
          <ClipboardList className="h-5 w-5 text-muted-foreground" />
        </div>

        <div>
          <p className="text-sm font-medium">No plans yet</p>
          <p className="mt-1 text-sm text-muted-foreground">
            Create an execution plan when a goal needs review before issues are generated.
          </p>
        </div>

        <Button size="sm" onClick={onNew}>
          <Plus className="mr-1.5 h-4 w-4" />
          New Plan
        </Button>
      </div>
    </div>
  );
}

// ─── Loading skeleton ──────────────────────────────────────────────────────────

function PlanListSkeleton() {
  return (
    <div className="divide-y rounded-md border">
      {Array.from({ length: 5 }, (_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 px-4 py-3"
          style={{ animationDelay: `${i * 0.06}s` }}
        >
          <span className="h-3 w-5 animate-pulse rounded bg-muted" />
          <span className="h-8 w-8 animate-pulse rounded-md bg-muted" />
          <div className="flex-1 space-y-1.5">
            <div
              className="h-4 animate-pulse rounded bg-muted"
              style={{ width: `${48 + ((i * 17) % 34)}%` }}
            />
            <div
              className="h-3 animate-pulse rounded bg-muted/70"
              style={{ width: `${62 + ((i * 11) % 24)}%` }}
            />
          </div>
          <div className="h-5 w-20 animate-pulse rounded-full bg-muted" />
        </div>
      ))}
    </div>
  );
}

// ─── New Plan dialog ───────────────────────────────────────────────────────────

function NewPlanDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const editorRef = useRef<ContentEditorRef>(null);
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const createPlan = useCreatePlan(wsId);
  const visibleAgents = useMemo(() => agents.filter((a) => !a.archived_at && a.runtime_id), [agents]);
  const defaultPlannerAgent = useMemo(
    () => visibleAgents.find((a) => a.is_internal && a.name === "规划Agent") ?? visibleAgents[0],
    [visibleAgents],
  );
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [agentId, setAgentId] = useState("");
  const [projectId, setProjectId] = useState<string>("none");
  const selectedAgentId = agentId || defaultPlannerAgent?.id || "";
  const selectedAgentName = visibleAgents.find((agent) => agent.id === selectedAgentId)?.name ?? "Planner agent";
  const selectedProjectName =
    projectId === "none" ? "No project" : (projects.find((project) => project.id === projectId)?.title ?? "Project");
  const { uploadWithToast, uploading } = useFileUpload(api, (error) => toast.error(error.message));
  const handleUploadFile = useCallback((file: File) => uploadWithToast(file), [uploadWithToast]);
  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((file) => editorRef.current?.uploadFile(file)),
  });

  const submit = async () => {
    const md = editorRef.current?.getMarkdown()?.trim() ?? prompt.trim();
    const planner = agentId || defaultPlannerAgent?.id;
    if (!md || !planner || uploading || editorRef.current?.hasActiveUploads()) return;
    try {
      const plan = await createPlan.mutateAsync({
        title: title.trim() || undefined,
        prompt: md,
        planner_agent_id: planner,
        project_id: projectId === "none" ? null : projectId,
      });
      setPrompt("");
      setTitle("");
      setAgentId("");
      setProjectId("none");
      editorRef.current?.clearContent();
      onOpenChange(false);
      toast.success("Plan created");
      nav.push(paths.planDetail(plan.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to create plan");
    }
  };

  const canSubmit = !!prompt.trim() && visibleAgents.length > 0 && !createPlan.isPending && !uploading;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl gap-0 p-0 overflow-hidden">
        {/* Dialog header */}
        <DialogHeader className="border-b px-5 py-4">
          <div className="flex items-center gap-2.5">
            <span className="font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/40">
              New Plan
            </span>
            <div className="h-px flex-1 bg-border/60" />
            <span className="h-1.5 w-1.5 rounded-full bg-primary/60" />
          </div>
          <DialogTitle className="sr-only">New Plan</DialogTitle>
        </DialogHeader>

        <div className="space-y-0">
          {/* Title field — document-title style */}
          <div className="border-b px-5 py-4">
            <Input
              placeholder="Plan title (optional)"
              value={title}
              className="border-0 bg-transparent p-0 text-base font-semibold placeholder:text-muted-foreground/30 focus-visible:ring-0 shadow-none"
              onChange={(e) => setTitle(e.target.value)}
            />
          </div>

          {/* Prompt area — the dominant element */}
          <div
            {...dropZoneProps}
            className="relative"
            style={{
              backgroundImage: "radial-gradient(circle, hsl(var(--muted-foreground) / 0.06) 1px, transparent 1px)",
              backgroundSize: "20px 20px",
            }}
          >
            <div className="relative min-h-44 px-5 py-4">
              <ContentEditor
                ref={editorRef}
                placeholder="Describe the goal in detail — the planner agent will use this to draft a spec and break it into tasks..."
                className="min-h-40 text-sm"
                onUpdate={(markdown) => setPrompt(markdown)}
                onUploadFile={handleUploadFile}
                onSubmit={submit}
                debounceMs={150}
              />
              {isDragOver && <FileDropOverlay />}
            </div>
          </div>

          {/* Config row */}
          <div className="border-t bg-muted/10 px-5 py-3">
            <div className="flex items-center gap-3">
              <div className="flex flex-1 items-center gap-2">
                {/* Agent select */}
                <div className="flex items-center gap-1.5 min-w-0">
                  <span className="shrink-0 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/35">
                    Agent
                  </span>
                  <Select value={selectedAgentId} onValueChange={(value) => setAgentId(value ?? "")}>
                    <SelectTrigger className="h-7 w-auto min-w-28 max-w-44 border-muted/60 bg-background text-xs">
                      <span className="min-w-0 flex-1 truncate text-left">{selectedAgentName}</span>
                    </SelectTrigger>
                    <SelectContent>
                      {visibleAgents.map((a) => (
                        <SelectItem key={a.id} value={a.id}>
                          {a.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                {/* Divider */}
                <div className="h-4 w-px bg-border/60" />

                {/* Project select */}
                <div className="flex items-center gap-1.5 min-w-0">
                  <span className="shrink-0 font-mono text-[9px] font-bold uppercase tracking-widest text-muted-foreground/35">
                    Project
                  </span>
                  <Select value={projectId} onValueChange={(value) => setProjectId(value ?? "none")}>
                    <SelectTrigger className="h-7 w-auto min-w-24 max-w-40 border-muted/60 bg-background text-xs">
                      <span className="min-w-0 flex-1 truncate text-left">{selectedProjectName}</span>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="none">No project</SelectItem>
                      {projects.map((p) => (
                        <SelectItem key={p.id} value={p.id}>
                          {p.title}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>

              {/* Actions */}
              <div className="flex shrink-0 items-center gap-1.5">
                <FileUploadButton
                  disabled={uploading}
                  onSelect={(file) => editorRef.current?.uploadFile(file)}
                />
                <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
                  Cancel
                </Button>
                <Button
                  size="sm"
                  onClick={submit}
                  disabled={!canSubmit}
                  className="gap-1.5"
                >
                  {createPlan.isPending ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : uploading ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <ArrowRight className="h-3.5 w-3.5" />
                  )}
                  {uploading ? "Uploading…" : createPlan.isPending ? "Creating…" : "Create Plan"}
                </Button>
              </div>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
