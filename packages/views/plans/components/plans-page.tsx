"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { ClipboardList, Plus, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { Badge } from "@multica/ui/components/ui/badge";
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
          </div>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh">
              <RefreshCw className="h-4 w-4" />
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
          <div className="text-sm text-muted-foreground">Loading plans...</div>
        ) : plans.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            No plans yet.
          </div>
        ) : (
          <div className="divide-y rounded-md border">
            {plans.map((plan) => (
              <button
                key={plan.id}
                className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left hover:bg-muted/50"
                onClick={() => nav.push(paths.planDetail(plan.id))}
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{plan.title}</div>
                  <div className="truncate text-xs text-muted-foreground">{plan.prompt}</div>
                </div>
                <Badge variant={plan.status === "failed" ? "destructive" : "secondary"}>{plan.status}</Badge>
              </button>
            ))}
          </div>
        )}
      </div>

      <NewPlanDialog open={open} onOpenChange={setOpen} />
    </div>
  );
}

function NewPlanDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const editorRef = useRef<ContentEditorRef>(null);
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const createPlan = useCreatePlan(wsId);
  const visibleAgents = useMemo(() => agents.filter((a) => !a.archived_at && a.runtime_id), [agents]);
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [agentId, setAgentId] = useState("");
  const [projectId, setProjectId] = useState<string>("none");
  const selectedAgentId = agentId || visibleAgents[0]?.id || "";
  const selectedAgentName = visibleAgents.find((agent) => agent.id === selectedAgentId)?.name ?? "Planner agent";
  const selectedProjectName = projectId === "none" ? "No project" : (projects.find((project) => project.id === projectId)?.title ?? "Project");
  const { uploadWithToast, uploading } = useFileUpload(api, (error) => toast.error(error.message));
  const handleUploadFile = useCallback((file: File) => uploadWithToast(file), [uploadWithToast]);
  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((file) => editorRef.current?.uploadFile(file)),
  });

  const submit = async () => {
    const md = editorRef.current?.getMarkdown()?.trim() ?? prompt.trim();
    const planner = agentId || visibleAgents[0]?.id;
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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>New Plan</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Input placeholder="Plan title" value={title} onChange={(e) => setTitle(e.target.value)} />
          <div
            {...dropZoneProps}
            className="relative flex min-h-40 rounded-md border bg-background px-3 py-2 focus-within:ring-2 focus-within:ring-ring/50"
          >
            <ContentEditor
              ref={editorRef}
              placeholder="Describe the large goal to split into issues..."
              className="min-h-36"
              onUpdate={(markdown) => setPrompt(markdown)}
              onUploadFile={handleUploadFile}
              onSubmit={submit}
              debounceMs={150}
            />
            {isDragOver && <FileDropOverlay />}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Select value={selectedAgentId} onValueChange={(value) => setAgentId(value ?? "")}>
              <SelectTrigger className="w-full">
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
            <Select value={projectId} onValueChange={(value) => setProjectId(value ?? "none")}>
              <SelectTrigger className="w-full">
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
          <div className="flex justify-end gap-2">
            <FileUploadButton
              disabled={uploading}
              onSelect={(file) => editorRef.current?.uploadFile(file)}
            />
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={!prompt.trim() || visibleAgents.length === 0 || createPlan.isPending || uploading}>
              {uploading ? "Uploading..." : "Create Plan"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
