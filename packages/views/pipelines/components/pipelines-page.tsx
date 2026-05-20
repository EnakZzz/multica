"use client";

import { useMemo, useState } from "react";
import { GitBranch, Plus, RefreshCw, Upload } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger } from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useCreatePipeline } from "@multica/core/pipelines/mutations";
import { pipelineListOptions } from "@multica/core/pipelines/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import { PageHeader } from "../../layout/page-header";
import { useNavigation } from "../../navigation";
import { validatePipelineDraft } from "../validation";
import { PipelineImportDialog } from "./pipeline-import-dialog";

export function PipelinesPage() {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const { data, isLoading, refetch } = useQuery(pipelineListOptions(wsId));
  const [open, setOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const pipelines = data?.pipelines ?? [];

  return (
    <div className="flex h-full flex-col bg-background">
      <PageHeader>
        <div className="flex w-full items-center justify-between">
          <div className="flex items-center gap-2">
            <GitBranch className="h-4 w-4" />
            <h1 className="text-sm font-semibold">Pipelines</h1>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="icon" onClick={() => refetch()} title="Refresh">
              <RefreshCw className="h-4 w-4" />
            </Button>
            <Button variant="outline" size="sm" onClick={() => setImportOpen(true)}>
              <Upload className="mr-1 h-4 w-4" />
              Import YAML
            </Button>
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="mr-1 h-4 w-4" />
              New Pipeline
            </Button>
          </div>
        </div>
      </PageHeader>

      <div className="flex-1 overflow-auto p-4">
        {isLoading ? (
          <div className="text-sm text-muted-foreground">Loading pipelines...</div>
        ) : pipelines.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            No pipelines yet.
          </div>
        ) : (
          <div className="divide-y rounded-md border">
            {pipelines.map((pipeline) => (
              <button
                key={pipeline.id}
                className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left hover:bg-muted/50"
                onClick={() => nav.push(paths.pipelineDetail(pipeline.id))}
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{pipeline.name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {pipeline.description || `${pipeline.nodes.length} nodes`}
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  {pipeline.is_system && <Badge variant="outline">Built-in</Badge>}
                  <Badge variant="secondary">{pipeline.nodes.length} nodes</Badge>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>

      <NewPipelineDialog open={open} onOpenChange={setOpen} />
      <PipelineImportDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onImported={(pipeline) => nav.push(paths.pipelineDetail(pipeline.id))}
      />
    </div>
  );
}

function NewPipelineDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const createPipeline = useCreatePipeline(wsId);
  const activeAgents = useMemo(() => agents.filter((agent) => !agent.archived_at), [agents]);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [agentId, setAgentId] = useState("none");

  const selectedAgentId = agentId === "none" ? "" : agentId || activeAgents[0]?.id || "";
  const selectedAgentName = selectedAgentId
    ? activeAgents.find((agent) => agent.id === selectedAgentId)?.name ?? "Agent"
    : "Unassigned";

  const submit = async () => {
    const payload = {
      name: name.trim(),
      description: description.trim(),
      nodes: [
        {
          key: "node-1",
          type: "issue" as const,
          title: name.trim() || "First node",
          description: description.trim(),
          agent_id: selectedAgentId || null,
          depends_on_node_keys: [],
          position_x: 0,
          position_y: 0,
        },
      ],
    };
    const errors = validatePipelineDraft(payload);
    if (errors.length > 0) {
      toast.error(errors[0]);
      return;
    }
    try {
      const pipeline = await createPipeline.mutateAsync(payload);
      setName("");
      setDescription("");
      setAgentId("none");
      onOpenChange(false);
      toast.success("Pipeline created");
      nav.push(paths.pipelineDetail(pipeline.id));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to create pipeline");
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>New Pipeline</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Input placeholder="Pipeline name" value={name} onChange={(e) => setName(e.target.value)} />
          <Textarea
            placeholder="Reusable process description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="min-h-24"
          />
          <div className="grid gap-3 sm:grid-cols-2">
            <Select value={selectedAgentId || "none"} onValueChange={(value) => setAgentId(value ?? "none")}>
              <SelectTrigger className="w-full">
                <span className="min-w-0 flex-1 truncate text-left">{selectedAgentName}</span>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">Unassigned</SelectItem>
                {activeAgents.map((agent) => (
                  <SelectItem key={agent.id} value={agent.id}>
                    {agent.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              onClick={submit}
              disabled={!name.trim() || createPipeline.isPending}
            >
              Create Pipeline
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
