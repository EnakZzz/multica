"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { ArrowRight, ClipboardList, Loader2, Plus, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
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

const STATUS_CONFIG: Record<string, { dot: string; label: string; textClass: string; borderClass: string }> = {
  planning: {
    dot: "bg-amber-500 animate-pulse",
    label: "Planning",
    textClass: "text-amber-600/75",
    borderClass: "border-l-amber-500/60",
  },
  spec_review: {
    dot: "bg-amber-400",
    label: "Review",
    textClass: "text-amber-600/60",
    borderClass: "border-l-amber-400/50",
  },
  ready: {
    dot: "bg-primary/80",
    label: "Ready",
    textClass: "text-primary/70",
    borderClass: "border-l-primary/50",
  },
  committed: {
    dot: "bg-emerald-500",
    label: "Done",
    textClass: "text-emerald-600/80",
    borderClass: "border-l-emerald-500/60",
  },
  failed: {
    dot: "bg-rose-500",
    label: "Failed",
    textClass: "text-rose-600/75",
    borderClass: "border-l-rose-500/55",
  },
};

function getStatusConfig(status: string) {
  return STATUS_CONFIG[status] ?? { dot: "bg-muted-foreground/40", label: status, textClass: "text-muted-foreground/50", borderClass: "border-l-border" };
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
          <div className="flex items-center gap-2.5">
            <ClipboardList className="h-3.5 w-3.5 text-muted-foreground/50" />
            <span className="font-mono text-[10px] font-bold uppercase tracking-widest text-muted-foreground/60">
              Plans
            </span>
            {plans.length > 0 && (
              <span className="font-mono text-[9px] tabular-nums text-muted-foreground/30">
                {plans.length}
              </span>
            )}
          </div>
          <div className="flex items-center gap-1.5">
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7"
              disabled={isLoading}
              onClick={() => refetch()}
              title="Refresh"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", isLoading && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="mr-1 h-3.5 w-3.5" />
              New Plan
            </Button>
          </div>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <PlanListSkeleton />
        ) : plans.length === 0 ? (
          <EmptyPlans onNew={() => setOpen(true)} />
        ) : (
          <div className="divide-y divide-border/40">
            {plans.map((plan, idx) => {
              const cfg = getStatusConfig(plan.status);
              return (
                <button
                  key={plan.id}
                  className={cn(
                    "group relative flex w-full items-center gap-3 border-l-2 px-4 py-3.5 text-left transition-all duration-150 hover:bg-muted/25",
                    cfg.borderClass,
                  )}
                  onClick={() => nav.push(paths.planDetail(plan.id))}
                >
                  {/* Sequence */}
                  <span className="w-5 shrink-0 font-mono text-[9px] font-bold tabular-nums text-muted-foreground/25">
                    {String(idx + 1).padStart(2, "0")}
                  </span>

                  {/* Status dot */}
                  <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", cfg.dot)} />

                  {/* Title + prompt */}
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold leading-snug">{plan.title}</div>
                    <div className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground/45">
                      {plan.prompt}
                    </div>
                  </div>

                  {/* Status label */}
                  <span className={cn("shrink-0 font-mono text-[9px] font-semibold uppercase tracking-wider", cfg.textClass)}>
                    {cfg.label}
                  </span>

                  {/* Arrow — appears on hover */}
                  <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground/25 opacity-0 transition-opacity duration-150 group-hover:opacity-100" />
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
    <div className="flex h-full flex-col items-center justify-center gap-5 p-8">
      {/* Dot-matrix background behind the icon */}
      <div className="relative flex items-center justify-center">
        <div
          className="absolute h-32 w-48 opacity-[0.035]"
          style={{
            backgroundImage: "radial-gradient(circle, hsl(var(--foreground)) 1px, transparent 1px)",
            backgroundSize: "12px 12px",
          }}
        />
        <div className="relative flex h-12 w-12 items-center justify-center rounded-lg border bg-card">
          <ClipboardList className="h-5 w-5 text-muted-foreground/40" />
        </div>
      </div>

      <div className="text-center">
        <p className="font-mono text-[10px] font-bold uppercase tracking-widest text-muted-foreground/50">
          No plans yet
        </p>
        <p className="mt-1 text-xs text-muted-foreground/40">
          Create your first execution plan to get started
        </p>
      </div>

      <Button size="sm" onClick={onNew}>
        <Plus className="mr-1.5 h-3.5 w-3.5" />
        New Plan
      </Button>
    </div>
  );
}

// ─── Loading skeleton ──────────────────────────────────────────────────────────

function PlanListSkeleton() {
  return (
    <div className="divide-y divide-border/40">
      {Array.from({ length: 5 }, (_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 border-l-2 border-l-muted px-4 py-3.5"
          style={{ animationDelay: `${i * 0.06}s` }}
        >
          <span className="h-2.5 w-4 animate-pulse rounded bg-muted-foreground/10" />
          <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-muted-foreground/15" />
          <div className="flex-1 space-y-1.5">
            <div
              className="h-3.5 animate-pulse rounded bg-muted-foreground/12"
              style={{ width: `${48 + ((i * 17) % 34)}%` }}
            />
            <div
              className="h-2.5 animate-pulse rounded bg-muted-foreground/8"
              style={{ width: `${62 + ((i * 11) % 24)}%` }}
            />
          </div>
          <div className="h-2.5 w-10 animate-pulse rounded bg-muted-foreground/10" />
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
